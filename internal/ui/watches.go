package ui

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"slices"
	"sync"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/mhersson/aim/internal/core"
	"github.com/mhersson/aim/internal/git"
)

// watchResolveTimeout bounds finding the repository of a pane's cwd.
const watchResolveTimeout = 2 * time.Second

// paneRepoMsg is the answer of resolving the repository of a pane's cwd.
type paneRepoMsg struct {
	id   core.PaneID
	cwd  string
	repo git.Repo
	err  error
	gone bool // err is set and cwd is no directory
}

// watchAcquiredMsg carries the reference a finished Acquire took.
type watchAcquiredMsg struct {
	id      core.PaneID
	repo    git.Repo
	release func()
	err     error
}

// watchMsg carries one batch from the watch set.
type watchMsg struct{ git.RootBatch }

// paneWatch is one agent pane's share of a watch.
type paneWatch struct {
	cwd     string // the cwd the latest resolution was started for
	want    string // the resolved root of cwd; "" outside a repository or until resolved
	root    string // the root release holds a reference on
	release func()
	failed  bool // the latest resolution failed; a hook retries it
	notRepo bool // the latest resolution found no repository
}

// pinWatch is a tab's reference on its pinned root: held when release is set,
// being acquired or failed when it is nil.
type pinWatch struct {
	root    string
	release func()
}

// pinAcquiredMsg carries the reference the Acquire for a tab's pin took.
type pinAcquiredMsg struct {
	tab     core.TabID
	root    string
	release func()
	err     error
}

// mdAcquiredMsg carries the reference the Acquire for the markdown file
// overlay's root took.
type mdAcquiredMsg struct {
	root    string
	release func()
	err     error
}

type watchState struct {
	set    watchSet
	open   func(context.Context, string) (git.Repo, error)
	panes  map[core.PaneID]*paneWatch
	pins   map[core.TabID]*pinWatch
	md     *pinWatch       // the markdown file overlay's reference on its root
	failed map[string]bool // roots whose failure was already shown
	holds  holds
}

// holds tracks every reference taken, so App.Close frees those whose message
// never reached the update loop.
type holds struct {
	mu     sync.Mutex
	closed bool
	next   int
	refs   map[int]func()
}

// add returns a release that frees rel once and forgets it. After closeAll it
// frees rel at once and returns a no-op.
func (h *holds) add(rel func()) func() {
	h.mu.Lock()

	if h.closed {
		h.mu.Unlock()
		rel()

		return func() {}
	}

	if h.refs == nil {
		h.refs = map[int]func(){}
	}

	n := h.next
	h.next++
	h.refs[n] = rel
	h.mu.Unlock()

	return func() {
		h.mu.Lock()
		r := h.refs[n]
		delete(h.refs, n)
		h.mu.Unlock()

		if r != nil {
			r()
		}
	}
}

func (h *holds) closeAll() {
	h.mu.Lock()
	h.closed = true
	refs := h.refs
	h.refs = nil
	h.mu.Unlock()

	for _, r := range refs {
		r()
	}
}

// paneRoot returns the worktree root resolved for a pane's working directory.
func (a *App) paneRoot(id core.PaneID) (string, bool) {
	w := a.watches.panes[id]
	if w == nil || w.want == "" {
		return "", false
	}

	return w.want, true
}

// waitWatch reads the next watch batch, or returns nothing once the set's
// channel closes or the app's context ends. The update loop re-arms it.
func (a *App) waitWatch() tea.Cmd {
	if a.watches.set == nil {
		return nil
	}

	ch := a.watches.set.Batches()

	return func() tea.Msg {
		select {
		case b, ok := <-ch:
			if !ok {
				return nil
			}

			return watchMsg{RootBatch: b}
		case <-a.ctx.Done():
			return nil
		}
	}
}

// releaseAsync frees a reference off the update loop; releasing stops a
// watcher, which waits for its goroutines.
func (a *App) releaseAsync(rel func()) {
	if rel != nil {
		a.closing.Go(rel)
	}
}

func (w *paneWatch) clear(a *App) {
	w.drop(a)
	w.want = ""
}

// drop frees the held reference and keeps the root the pane wants.
func (w *paneWatch) drop(a *App) {
	a.releaseAsync(w.release)
	w.release, w.root = nil, ""
}

// watchSync reconciles the held watches with the panes that need one, the
// agents (while there is a set to hold watches in) and the panes a drawer
// reviews: it starts resolving a pane whose followed directory changed and
// releases those of panes that closed, stopped being agents or are no longer
// reviewed. It also syncs the pins' watches and the markdown overlay's.
func (a *App) watchSync() tea.Cmd {
	st := a.store.State()
	reviewed := reviewedPanes(st)
	agents := a.watches.set != nil

	for id, w := range a.watches.panes {
		if m, ok := st.Panes[id]; ok && (reviewed[id] || agents && m.Kind == core.KindAgent) {
			continue
		}

		w.clear(a)
		delete(a.watches.panes, id)
	}

	var ids []core.PaneID
	if agents {
		ids = st.AgentPanes()
	}

	for id := range reviewed {
		if !slices.Contains(ids, id) {
			ids = append(ids, id)
		}
	}

	var cmds []tea.Cmd

	for _, id := range ids {
		cwd := st.Panes[id].FollowDir()
		if cwd == "" {
			continue
		}

		w := a.watches.panes[id]
		if w == nil {
			w = &paneWatch{}
			a.watches.panes[id] = w
		}

		if w.cwd == cwd {
			continue
		}

		w.cwd = cwd
		cmds = append(cmds, a.resolveCmd(id, cwd))
	}

	return tea.Batch(append(cmds, a.pinSync(), a.mdWatchSync())...)
}

func (a *App) resolveCmd(id core.PaneID, cwd string) tea.Cmd {
	open := a.watches.open

	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(a.ctx, watchResolveTimeout)
		defer cancel()

		r, err := open(ctx, cwd)

		return paneRepoMsg{id: id, cwd: cwd, repo: r, err: err, gone: err != nil && !usableDir(cwd)}
	}
}

// onPaneRepo starts holding the watch of the resolved root. An answer for a
// pane that closed, stopped being an agent or moved on to another directory is dropped.
func (a *App) onPaneRepo(msg paneRepoMsg) tea.Cmd {
	w := a.watches.panes[msg.id]
	if w == nil || w.cwd != msg.cwd {
		return nil
	}

	switch {
	case errors.Is(msg.err, git.ErrNotRepo), msg.err != nil && msg.gone:
		w.clear(a)

		w.failed, w.notRepo = false, true

		return nil
	case msg.err != nil:
		a.log.Warn("resolve watch repo", "pane", msg.id, "err", msg.err)
		w.clear(a)

		w.failed, w.notRepo = true, false

		a.noticeOnce("project "+filepath.Base(msg.cwd), filepath.Base(msg.cwd), msg.err)

		return nil
	}

	w.failed, w.notRepo = false, false
	delete(a.watches.failed, "project "+filepath.Base(msg.cwd))

	w.want = msg.repo.Root
	sweep := a.refSweep(msg.repo)

	if w.root == msg.repo.Root || a.watches.set == nil {
		return sweep
	}

	set, r, id := a.watches.set, msg.repo, msg.id

	return tea.Batch(sweep, func() tea.Msg {
		rel, err := set.Acquire(a.ctx, r)
		if err != nil {
			return watchAcquiredMsg{id: id, repo: r, err: err}
		}

		return watchAcquiredMsg{id: id, repo: r, release: a.watches.holds.add(rel)}
	})
}

// onWatchAcquired keeps the new reference for its pane. The Acquire error is
// the one source of the failure notice: the set may report one root several
// times and drops reports when its channel is full.
func (a *App) onWatchAcquired(msg watchAcquiredMsg) {
	if msg.err != nil {
		a.noticeWatchFailed(msg.repo, msg.err)

		// The pane moved here from another root, whose watch would be taken for this one's.
		if w := a.watches.panes[msg.id]; w != nil && w.want == msg.repo.Root && w.root != "" && w.root != msg.repo.Root {
			w.drop(a)
		}

		return
	}

	delete(a.watches.failed, "root "+msg.repo.Root)

	w := a.watches.panes[msg.id]
	if w == nil || w.want != msg.repo.Root || w.root == msg.repo.Root {
		a.releaseAsync(msg.release)

		return
	}

	a.releaseAsync(w.release)
	w.release, w.root = msg.release, msg.repo.Root
}

func (a *App) noticeWatchFailed(r git.Repo, err error) {
	a.log.Warn("watch failed", "root", r.Root, "err", err)

	a.noticeOnce("root "+r.Root, repoProject(r), err)
}

// noticeOnce shows the failure notice unless key was shown before.
func (a *App) noticeOnce(key, project string, err error) {
	if a.watches.failed[key] || errors.Is(err, context.Canceled) {
		return
	}

	a.watches.failed[key] = true
	a.notice = fmt.Sprintf("watching %s failed: %v", project, err)
}

// watchRetry makes the next sync resolve a pane whose last resolution failed.
func (a *App) watchRetry(id core.PaneID) {
	if w := a.watches.panes[id]; w != nil && w.failed {
		w.cwd = ""
	}
}

// onWatch re-probes every agent in the batch's root, the head always and the
// stats while the rail is open, and hands the batch to the drawers that review it.
func (a *App) onWatch(msg watchMsg) tea.Cmd {
	if msg.Err != nil {
		a.log.Debug("watch error", "root", msg.Root, "err", msg.Err)

		return nil
	}

	b := msg.Batch
	if !b.Git && !b.Overflow && len(b.Paths) == 0 {
		return nil
	}

	st := a.store.State()

	var cmds []tea.Cmd

	for _, id := range st.AgentPanes() {
		if w := a.watches.panes[id]; w == nil || w.root != msg.Root {
			continue
		}

		// A head change shows at once; its totals follow in onRailHead. Other
		// changes keep the stats interval.
		if b.Git || b.Overflow {
			a.railEntry(id).head.force = true

			cmds = append(cmds, a.railFire(st, id, probeHead))
		}

		if len(b.Paths) > 0 || b.Overflow {
			cmds = append(cmds, a.railFire(st, id, probeStats))
		}
	}

	cmds = append(cmds, a.reviewBatch(msg.Root, b), a.mdBatch(msg.Root))

	return tea.Batch(cmds...)
}

// rootWatched reports whether a held reference watches root, so its changes
// reach the drawers that review it without a hook event.
func (a *App) rootWatched(root string) bool {
	if root == "" {
		return false
	}

	for _, w := range a.watches.panes {
		if w.root == root {
			return true
		}
	}

	for _, p := range a.watches.pins {
		if p.release != nil && p.root == root {
			return true
		}
	}

	return a.watches.md != nil && a.watches.md.release != nil && a.watches.md.root == root
}

// pinSync reconciles the pin watches with the tabs whose open drawer reviews
// its pin's pane: it starts taking the watch of a root not yet held and
// releases the watch of a pin that changed, ended or lost its drawer.
func (a *App) pinSync() tea.Cmd {
	st := a.store.State()

	want := map[core.TabID]string{}

	if a.watches.set != nil {
		for _, t := range st.Tabs {
			if t.Review != nil && pinnedTo(t, t.Review.Pane) {
				want[t.ID] = t.Pin.Root
			}
		}
	}

	for id, pw := range a.watches.pins {
		if root, ok := want[id]; ok && root == pw.root {
			continue
		}

		a.releaseAsync(pw.release)
		delete(a.watches.pins, id)
	}

	var cmds []tea.Cmd

	for id, root := range want {
		if a.watches.pins[id] != nil {
			continue
		}

		a.watches.pins[id] = &pinWatch{root: root}
		cmds = append(cmds, a.pinAcquire(id, root))
	}

	return tea.Batch(cmds...)
}

// pinAcquire takes the watch of a pinned root off the update loop. A root that
// is gone is no failure: the drawer says so.
func (a *App) pinAcquire(tab core.TabID, root string) tea.Cmd {
	set, open := a.watches.set, a.watches.open

	return func() tea.Msg {
		rel, err := a.acquireRoot(a.ctx, set, open, root)

		return pinAcquiredMsg{tab: tab, root: root, release: rel, err: err}
	}
}

// acquireRoot opens root's repository and takes a reference on its watch,
// registered with holds so App.Close frees it. A root that is gone answers
// errWorktreeRemoved. It blocks, so it runs off the update loop.
func (a *App) acquireRoot(ctx context.Context, set watchSet, open func(context.Context, string) (git.Repo, error), root string) (func(), error) {
	octx, cancel := context.WithTimeout(ctx, watchResolveTimeout)
	defer cancel()

	r, err := open(octx, root)
	if err != nil {
		if !usableDir(root) {
			err = errWorktreeRemoved{root: root}
		}

		return nil, err
	}

	rel, err := set.Acquire(ctx, r)
	if err != nil {
		return nil, err //nolint:wrapcheck // noticeOnce names the root
	}

	return a.watches.holds.add(rel), nil
}

// onPinAcquired keeps the new reference for its tab's pin. One for a root the
// pin has since left, or already holds, is released. A failure is not retried
// until the pin changes or its drawer closes; the drawer's hook events stand
// in for the watch meanwhile.
func (a *App) onPinAcquired(msg pinAcquiredMsg) {
	pw := a.watches.pins[msg.tab]
	current := pw != nil && pw.root == msg.root && pw.release == nil

	var gone errWorktreeRemoved

	switch {
	case msg.err == nil:
		delete(a.watches.failed, "root "+msg.root)

		if !current {
			a.releaseAsync(msg.release)

			return
		}

		pw.release = msg.release
	case current && !errors.As(msg.err, &gone):
		a.log.Warn("watch pinned root", "root", msg.root, "err", msg.err)
		a.noticeOnce("root "+msg.root, filepath.Base(msg.root), msg.err)
	}
}

// mdWatchSync keeps one reference on the root of the open markdown file
// overlay when it is in a repository, as pinSync does for a pin: it releases one the overlay no longer
// wants and starts taking the wanted one off the update loop.
func (a *App) mdWatchSync() tea.Cmd {
	want := ""
	if a.watches.set != nil && a.plan != nil && a.plan.file != nil && a.plan.file.repo != "" {
		want = a.plan.root
	}

	if w := a.watches.md; w != nil {
		if w.root == want {
			return nil
		}

		a.releaseAsync(w.release)
		a.watches.md = nil
	}

	if want == "" {
		return nil
	}

	a.watches.md = &pinWatch{root: want}
	set, open := a.watches.set, a.watches.open

	return func() tea.Msg {
		rel, err := a.acquireRoot(a.ctx, set, open, want)

		return mdAcquiredMsg{root: want, release: rel, err: err}
	}
}

// onMDAcquired keeps the new reference while the overlay still wants its root,
// else releases it. A root that is gone is no failure.
func (a *App) onMDAcquired(msg mdAcquiredMsg) {
	w := a.watches.md
	current := w != nil && w.root == msg.root && w.release == nil

	var gone errWorktreeRemoved

	switch {
	case msg.err == nil:
		delete(a.watches.failed, "root "+msg.root)

		if !current {
			a.releaseAsync(msg.release)

			return
		}

		w.release = msg.release
	case current && !errors.As(msg.err, &gone):
		a.log.Warn("watch markdown root", "root", msg.root, "err", msg.err)
		a.noticeOnce("root "+msg.root, filepath.Base(msg.root), msg.err)
	}
}
