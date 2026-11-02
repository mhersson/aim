package ui

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/mhersson/aim/internal/core"
	"github.com/mhersson/aim/internal/git"
)

const (
	testSession  core.SessionID = "11111111-2222-4333-8444-555555555555"
	otherSession core.SessionID = "aaaaaaaa-bbbb-4ccc-8ddd-eeeeeeeeeeee"
)

// newRepoDir is an empty repository on main in a temp dir without symlinks.
func newRepoDir(t *testing.T) string {
	t.Helper()

	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}

	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}

	gitIn(t, "git", dir, "init", "-q", "-b", "main")

	return dir
}

func commitEverything(t *testing.T, dir, msg string) {
	t.Helper()
	gitIn(t, "git", dir, "add", "-A")
	gitIn(t, "git", dir, "commit", "-q", "-m", msg)
}

func numberedLines(prefix string, n int) string {
	var b strings.Builder
	for i := 1; i <= n; i++ {
		fmt.Fprintf(&b, "%s %d\n", prefix, i)
	}

	return b.String()
}

// snapshotTurn takes the session's next turn in dir's worktree. Like the next
// prompt, it ends the worktree's latest turn when that is open.
func snapshotTurn(t *testing.T, dir string, session core.SessionID) {
	t.Helper()

	opts := git.SnapshotOptions{TempDir: t.TempDir()}

	r, err := git.Open(context.Background(), dir)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}

	if turns, err := git.Turns(context.Background(), r, string(session)); err == nil && len(turns) > 0 {
		if last := turns[len(turns)-1]; last.EndCommit == "" && !last.Missing {
			opts.Ends = last.N
		}
	}

	if _, err := git.Snapshot(context.Background(), dir, string(session), opts); err != nil {
		t.Fatalf("Snapshot: %v", err)
	}
}

// threeTurnRepo has three turns of testSession: turn 1 edits a.txt, turn 2
// edits b.txt, deletes d.txt and adds an untracked file, and turn 3 edits
// dir/c.txt and commits.
func threeTurnRepo(t *testing.T, f *fakeGit) *fakeRepo {
	t.Helper()

	r := f.repo(t, "")
	dir := r.Root()
	write(t, filepath.Join(dir, "a.txt"), "a\n")
	write(t, filepath.Join(dir, "b.txt"), "b\n")
	write(t, filepath.Join(dir, "dir/c.txt"), "c\n")
	r.commit("init")
	write(t, filepath.Join(dir, "d.txt"), "d\n")
	write(t, filepath.Join(dir, "e.txt"), "e\n")
	r.commit("more")

	r.turn(string(testSession))
	write(t, filepath.Join(dir, "a.txt"), "turn one\n")

	r.turn(string(testSession))
	write(t, filepath.Join(dir, "b.txt"), "turn two\n")

	if err := os.Remove(filepath.Join(dir, "d.txt")); err != nil {
		t.Fatal(err)
	}

	write(t, filepath.Join(dir, "new.txt"), "untracked\n")

	r.turn(string(testSession))
	write(t, filepath.Join(dir, "dir/c.txt"), "turn three\n")
	r.commit("agent commit")

	return r
}

// editedRepo has f.txt of 60 lines committed on main with lines 5 and 50
// edited in the working tree: two hunks.
func editedRepo(t *testing.T, f *fakeGit) *fakeRepo {
	t.Helper()

	r := f.repo(t, "")
	write(t, filepath.Join(r.Root(), "f.txt"), numberedLines("line", 60))
	r.commit("init")
	write(t, filepath.Join(r.Root(), "f.txt"), editedF(0))

	return r
}

// realEditedRepo is editedRepo on real git.
func realEditedRepo(t *testing.T) string {
	t.Helper()

	dir := newRepoDir(t)
	write(t, filepath.Join(dir, "f.txt"), numberedLines("line", 60))
	commitEverything(t, dir, "init")
	write(t, filepath.Join(dir, "f.txt"), editedF(0))

	return dir
}

// editedF is f.txt with lines 5 and 50 edited, under n inserted lines.
func editedF(n int) string {
	var b strings.Builder

	for i := range n {
		fmt.Fprintf(&b, "inserted %d\n", i+1)
	}

	for i := 1; i <= 60; i++ {
		switch i {
		case 5:
			b.WriteString("five edited\n")
		case 50:
			b.WriteString("fifty edited\n")
		default:
			fmt.Fprintf(&b, "line %d\n", i)
		}
	}

	return b.String()
}

// heldTicks records the ticks the app schedules and keeps their messages
// until a test fires them.
type heldTicks struct {
	delays []time.Duration
	msgs   []tea.Msg
}

func (h *heldTicks) tick(d time.Duration, fn func(time.Time) tea.Msg) tea.Cmd {
	h.delays = append(h.delays, d)
	h.msgs = append(h.msgs, fn(t0))

	return nil
}

func (h *heldTicks) fire() []tea.Msg {
	out := h.msgs
	h.msgs = nil

	return out
}

func (h *heldTicks) scheduled(d time.Duration) bool { return slices.Contains(h.delays, d) }

// drawerRig is an app with a diff engine on a fake git and its watch set, or
// on real git and a real watch set when the options carry no gitOf. A pump
// stands in for the Bubble Tea runtime, so loads run off the test's goroutine.
type drawerRig struct {
	*App
	ticks *heldTicks
	clk   *fakeClock
	pm    *pump
	set   *git.WatchSet // nil on a fake git
	fake  *fakeGit      // nil on real git
	wait  time.Duration // how long until gives up

	reported map[string]bool // the paths, absolute, that watcher batches fed to step named
}

// turnFirst makes a new drawer open in turn mode when its pane has turns; the
// rig's zero [diff] config opens in branch mode.
func turnFirst(o *Options) { o.Diff.Mode = string(core.ReviewTurn) }

func newDrawerRig(t *testing.T, opts ...func(*Options)) *drawerRig {
	t.Helper()

	return newDrawerRigWatching(t, git.WatchOptions{Debounce: 20 * time.Millisecond}, opts...)
}

func newDrawerRigWatching(t *testing.T, wo git.WatchOptions, opts ...func(*Options)) *drawerRig {
	t.Helper()

	// The options run twice, here and in kindApp, so they must be pure.
	var probe Options

	for _, f := range opts {
		f(&probe)
	}

	fake, _ := probe.Git.(*fakeGit)

	// The app's wiring: a run directory for temporary indexes and a cache of
	// refreshed index copies.
	tmp, cache := t.TempDir(), t.TempDir()
	set0 := func(o *Options) { o.GitTempDir, o.IndexCache = tmp, cache }

	var set *git.WatchSet

	if fake == nil {
		set = git.NewWatchSet(context.Background(), wo)

		t.Cleanup(func() { _ = set.Close() })

		set0 = func(o *Options) { o.GitTempDir, o.IndexCache, o.Watches = tmp, cache, set }
	}

	a, _, clk := kindApp(t, append([]func(*Options){set0}, opts...)...)
	r := &drawerRig{App: a, ticks: &heldTicks{}, clk: clk, pm: newPump(t), set: set, fake: fake, wait: watchWait, reported: map[string]bool{}}
	r.pm.stopWith(t, a)
	a.tick = r.ticks.tick

	sized(t, a, 200, 50)
	r.pm.run(a.waitWatch())

	return r
}

func (r *drawerRig) step(msg tea.Msg) tea.Cmd {
	if w, ok := msg.(watchMsg); ok {
		for _, p := range w.Batch.Paths {
			r.reported[filepath.Join(w.Root, p)] = true
		}
	}

	_, cmd := r.Update(msg)

	return cmd
}

// reportedAll is a condition: watcher batches fed to step have named every one
// of paths. A watcher reports a write only after its debounce, so a test that
// must not see that report later waits for it first.
func (r *drawerRig) reportedAll(paths ...string) func() bool {
	return func() bool {
		return !slices.ContainsFunc(paths, func(p string) bool { return !r.reported[p] })
	}
}

func (r *drawerRig) run(cmd tea.Cmd) { r.pm.run(cmd) }

// settle runs the after-update work for a change made outside Update.
func (r *drawerRig) settle() { r.run(r.step(struct{}{})) }

// until feeds queued messages to Update until cond holds.
func (r *drawerRig) until(t *testing.T, what string, cond func() bool) {
	t.Helper()

	deadline := time.Now().Add(r.wait)

	for !cond() {
		select {
		case m := <-r.pm.msgs:
			r.run(r.step(m))
		case <-time.After(10 * time.Millisecond):
			r.settle()
		}

		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
	}
}

func (r *drawerRig) view() *reviewView { return r.drawer.views[r.store.State().Tab().ID] }

// viewHolds is a condition on the active tab's view, false while it has none.
func (r *drawerRig) viewHolds(cond func(v *reviewView) bool) func() bool {
	return func() bool {
		v := r.view()

		return v != nil && cond(v)
	}
}

// idle waits until the drawer has a result and no load is in flight.
func (r *drawerRig) idle(t *testing.T) *reviewView {
	t.Helper()

	r.until(t, "the drawer to load", func() bool {
		v := r.view()

		return v != nil && v.loaded && !v.loading
	})

	return r.view()
}

func (r *drawerRig) shell(t *testing.T, cwd string) core.PaneID {
	t.Helper()

	id := r.store.State().NewPaneID()
	openIn(t, r.App, core.OpenPane{Kind: core.KindShell, Cwd: cwd, Split: core.SplitVertical, Area: r.bodyArea()})

	return id
}

func (r *drawerRig) agent(t *testing.T, cwd string, session core.SessionID) core.PaneID {
	t.Helper()

	id := r.store.State().NewPaneID()
	openIn(t, r.App, core.OpenPane{Kind: core.KindAgent, Cwd: cwd, Split: core.SplitVertical, Harness: "claude-code", Area: r.bodyArea()})

	if session != "" {
		openIn(t, r.App, core.SetAgentSession{ID: id, Session: session})
	}

	return id
}

// open opens the drawer on a pane and waits for its first diff.
func (r *drawerRig) open(t *testing.T, id core.PaneID) *reviewView {
	t.Helper()

	r.run(r.openReview(id, true))
	r.until(t, "the drawer to open", func() bool { return r.store.State().Tab().Review != nil })

	return r.idle(t)
}

// held reports whether the pane's watch reference is taken.
func (r *drawerRig) held(id core.PaneID) string {
	if pw := r.watches.panes[id]; pw != nil {
		return pw.root
	}

	return ""
}

func (r *drawerRig) watched(root string) bool {
	if r.fake != nil {
		return r.fake.watches.holds(root) > 0
	}

	_, ok := r.set.Seq(root)

	return ok
}

// watching waits until the pane's worktree is watched.
func (r *drawerRig) watching(t *testing.T, id core.PaneID, root string) {
	t.Helper()
	r.until(t, "the worktree to be watched", func() bool { return r.held(id) == root && r.watched(root) })
}

// batch hands the app a watcher batch as the set would.
func (r *drawerRig) batch(root string, b git.Batch) tea.Cmd {
	return r.onWatch(watchMsg{Root: root, Batch: b})
}

func rootOf(t *testing.T, dir string) string {
	t.Helper()

	repo, err := git.Open(context.Background(), dir)
	if err != nil {
		t.Fatal(err)
	}

	return repo.Root
}

// paths are the sorted paths of the diff the view holds.
func viewPaths(v *reviewView) []string {
	out := make([]string, 0, len(v.res.Files))
	for _, f := range v.res.Files {
		out = append(out, f.Path)
	}

	slices.Sort(out)

	return out
}

// drawn is the drawer as text.
func (r *drawerRig) drawn(t *testing.T) string {
	t.Helper()

	st := r.store.State()

	rect, ok := r.drawerRect(st)
	if !ok {
		t.Fatal("no drawer on screen")
	}

	return ansi.Strip(strings.Join(r.drawerLines(st, rect), "\n"))
}

// layout is the active drawer's layout as the update loop reads it.
func (r *drawerRig) layout(t *testing.T) drawerLayout {
	t.Helper()

	return r.drawerLayoutOf(mustRect(t, r), r.view(), treeIsOpen(r.App))
}

// showTree opens the active drawer's file tree in core; the diff keeps the keyboard.
func (r *drawerRig) showTree(t *testing.T) {
	t.Helper()
	openIn(t, r.App, core.SetReviewTree{Open: true})
	r.settle()
}

// treeIsOpen reports whether the active tab's drawer has its file tree open.
func treeIsOpen(a *App) bool {
	rv := a.store.State().Tab().Review

	return rv != nil && rv.Tree
}

// diffLineAt is the text of the diff line under row i, "" for other rows.
func diffLineAt(v *reviewView, i int) string {
	r := v.rows[i]
	if r.kind != rowLine && r.kind != rowPair {
		return ""
	}

	return v.res.Files[r.file].Hunks[r.hunk].Lines[r.line].Text
}

func mustRect(t *testing.T, r *drawerRig) core.Rect {
	t.Helper()

	rect, ok := r.drawerRect(r.store.State())
	if !ok {
		t.Fatal("no drawer on screen")
	}

	return rect
}

// settled feeds queued messages until the active view has no load running or
// queued.
func (r *drawerRig) settled(t *testing.T) {
	t.Helper()

	r.until(t, "the drawer to settle", func() bool {
		v := r.view()

		return v != nil && !v.loading && v.queued == nil
	})
}

// quiet feeds messages until nothing arrives for d and no load is pending.
func (r *drawerRig) quiet(t *testing.T, d time.Duration) {
	t.Helper()

	deadline := time.Now().Add(r.wait)

	for {
		select {
		case m := <-r.pm.msgs:
			r.run(r.step(m))
		case <-time.After(d):
			if v := r.view(); !v.loading && v.queued == nil {
				return
			}
		}

		if time.Now().After(deadline) {
			t.Fatal("the drawer never went quiet")
		}
	}
}
