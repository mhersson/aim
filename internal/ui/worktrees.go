package ui

import (
	"cmp"
	"context"
	"errors"
	"slices"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/mhersson/aim/internal/core"
	"github.com/mhersson/aim/internal/git"
)

// wtRowKind says what a row of the worktree list is.
type wtRowKind int

const (
	wtFollow wtRowKind = iota // the row that turns following back on
	wtHead                    // a section header
	wtGap                     // a blank row between sections
	wtPlace                   // a worktree
)

// wtRow is one row of the worktree list.
type wtRow struct {
	kind               wtRowKind
	text               string // a header's text
	root, repo, branch string
	here, latest       bool
	at                 time.Time // the latest edit there; zero for none
	stats              git.Stats
	statsKnown         bool
}

// wtList is the open worktree list. It is a snapshot taken when it opens; only
// the counts fill in afterwards. Only the update loop touches it.
type wtList struct {
	tab           core.TabID
	pane          core.PaneID
	gen, sel, top int
	rows          []wtRow
	now           string // the followed place as "repo · branch"
	cancel        context.CancelFunc
}

// wtInput is what listPlaces builds the rows from.
type wtInput struct {
	own, followed, pin, agent string // the agent's own and followed directories, the pinned root, its name
	edits                     []core.Edit
}

// wtOpenMsg answers the build of a list. gen is the listGen it was asked for.
type wtOpenMsg struct {
	tab  core.TabID
	pane core.PaneID
	gen  int
	rows []wtRow
	now  string
	pin  string // the pinned root with symlinks resolved, as the rows name it; "" when not pinned
	err  error
}

// wtStatsMsg answers one row's diff stats. gen is the list's generation.
type wtStatsMsg struct {
	gen   int
	root  string
	stats git.Stats
	err   error
}

// wtPlaceAt is a worktree found for a directory.
type wtPlaceAt struct {
	repo git.Repo
	row  wtRow
	ok   bool
}

// wtFinder finds the worktree of a directory once, whoever asks.
type wtFinder struct {
	ctx   context.Context //nolint:containedctx // one build's context
	git   Git
	found map[string]wtPlaceAt
	real  map[string]string
}

// at opens dir and reads its branch; ok is false for a directory that is gone
// or in no repository.
func (f *wtFinder) at(dir string) wtPlaceAt {
	if dir == "" {
		return wtPlaceAt{}
	}

	if p, ok := f.found[dir]; ok {
		return p
	}

	var p wtPlaceAt

	if repo, err := f.git.Open(f.ctx, dir); err == nil {
		p = wtPlaceAt{repo: repo, ok: true, row: wtRow{kind: wtPlace, root: repo.Root, repo: repo.Name()}}

		// A head that cannot be read leaves the branch out and the place listed.
		if h, err := f.git.Head(f.ctx, repo); err == nil {
			p.row.branch = headLabel(h)
		}
	}

	if f.ctx.Err() == nil {
		f.found[dir] = p
	}

	return p
}

// resolve is p with symlinks resolved, which is how git names a worktree it
// finds by a path through one.
func (f *wtFinder) resolve(p string) string {
	if r, ok := f.real[p]; ok {
		return r
	}

	r := resolved(p)
	f.real[p] = r

	return r
}

// headLabel is the branch a HEAD shows as, or 7 characters of a detached commit.
func headLabel(h git.HeadInfo) string {
	if h.Detached {
		return h.OID[:min(shortOIDLen, len(h.OID))]
	}

	return h.Branch
}

// listPlaces builds the list's rows, and the followed place as "repo · branch",
// off the update loop: the Follow row; the places the agent touched, newest
// edit first, with its own directory and a pinned place that no edit names added
// at the end; then the other worktrees of its own repository. A place that is
// gone, or in no repository, is left out. It fails only when ctx ends.
func listPlaces(ctx context.Context, g Git, in wtInput) (rows []wtRow, now string, err error) {
	f := &wtFinder{ctx: ctx, git: g, found: map[string]wtPlaceAt{}, real: map[string]string{}}
	rows = []wtRow{{kind: wtFollow}, {kind: wtHead, text: "Touched by " + in.agent}}
	listed := map[string]bool{}
	own := f.at(in.own)

	add := func(row wtRow) {
		listed[f.resolve(row.root)] = true
		rows = append(rows, row)
	}

	edits := slices.Clone(in.edits)
	slices.SortFunc(edits, func(a, b core.Edit) int { return cmp.Compare(b.Seq, a.Seq) })

	for i, e := range edits {
		p := f.at(e.Root)
		if !p.ok || listed[f.resolve(p.row.root)] {
			continue
		}

		row := p.row
		row.at, row.latest = e.At, i == 0
		row.here = own.ok && f.resolve(row.root) == f.resolve(own.row.root)
		add(row)
	}

	if own.ok && !listed[f.resolve(own.row.root)] {
		row := own.row
		row.here = true
		add(row)
	}

	if p := f.at(in.pin); p.ok && !listed[f.resolve(p.row.root)] {
		add(p.row)
	}

	if own.ok {
		if others := f.others(own, listed); len(others) > 0 {
			rows = append(rows, wtRow{kind: wtGap}, wtRow{kind: wtHead, text: "Other worktrees of " + own.row.repo})
			rows = append(rows, others...)
		}
	}

	if p := f.at(in.followed); p.ok {
		now = p.row.repo
		if p.row.branch != "" {
			now += " · " + p.row.branch
		}
	}

	if err := ctx.Err(); err != nil {
		return nil, "", err
	}

	return rows, now, nil
}

// others are the checked-out worktrees of own's repository that listed does not
// hold, by the paths git reports, resolved so that a path through a symlink is
// the root git finds for the worktree. A failed listing leaves them out.
func (f *wtFinder) others(own wtPlaceAt, listed map[string]bool) []wtRow {
	wts, err := f.git.ListWorktrees(f.ctx, own.repo)
	if err != nil {
		return nil
	}

	var out []wtRow

	for _, w := range wts {
		root := f.resolve(w.Path)
		if w.Bare || w.Prunable || listed[root] {
			continue
		}

		listed[root] = true

		branch := w.Branch
		if branch == "" {
			branch = w.Head[:min(shortOIDLen, len(w.Head))]
		}

		out = append(out, wtRow{kind: wtPlace, root: root, repo: own.row.repo, branch: branch})
	}

	return out
}

// ---- opening and closing ----

// listFor is the open list when it belongs to the active tab and the drawer's pane.
func (a *App) listFor(tab core.Tab) *wtList {
	l := a.drawer.list
	if l == nil || tab.Review == nil || l.tab != tab.ID || l.pane != tab.Review.Pane {
		return nil
	}

	return l
}

// openWorktrees is review.worktrees: it gives the drawer the keyboard and opens
// the worktree list, or builds it again when it is open. The list shows once the
// build answers; a later press makes the earlier build's answer stale.
func (a *App) openWorktrees() tea.Cmd {
	tab, v, ok := a.reviewActive()
	if !ok {
		return nil
	}

	a.drawerTake()
	a.closeLinks()

	v.search = nil

	st := a.store.State()
	pane := tab.Review.Pane
	m := st.Panes[pane]

	in := wtInput{own: m.WorkDir(), followed: m.FollowDir(), agent: a.cardName(st, pane), edits: slices.Clone(m.Edits)}
	if pinnedTo(tab, pane) {
		in.pin = tab.Pin.Root
	}

	a.drawer.listGen++

	id, gen, parent, g := tab.ID, a.drawer.listGen, a.ctx, a.git

	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(parent, reviewOpenTimeout)
		defer cancel()

		rows, now, err := listPlaces(ctx, g, in)

		// The rows name worktrees as git does, so the selection compares strings on the loop.
		pin := ""
		if in.pin != "" {
			pin = resolved(in.pin)
		}

		return wtOpenMsg{tab: id, pane: pane, gen: gen, rows: rows, now: now, pin: pin, err: err}
	}
}

// onWorktreesOpen shows the built list and starts counting each place's
// changes. An answer for a list that was closed or built again since, or for a
// drawer that moved on, is dropped. A list built again replaces the one shown
// and keeps its selection by root.
func (a *App) onWorktreesOpen(msg wtOpenMsg) tea.Cmd {
	tab := a.store.State().Tab()

	if msg.gen != a.drawer.listGen || tab.ID != msg.tab || tab.Review == nil || tab.Review.Pane != msg.pane {
		return nil
	}

	switch {
	case errors.Is(msg.err, context.Canceled):
		return nil
	case msg.err != nil:
		a.log.Warn("worktree list", "pane", msg.pane, "err", msg.err)
		a.notice = "worktrees: " + msg.err.Error()

		return nil
	}

	l := &wtList{tab: msg.tab, pane: msg.pane, gen: msg.gen, rows: msg.rows, now: msg.now}
	l.sel = a.listSelection(tab, l, msg.pin)

	if old := a.drawer.list; old != nil {
		old.cancel()
	}

	ctx, cancel := context.WithCancel(a.ctx)
	l.cancel = cancel
	a.drawer.list = l
	a.keepSelected(l)

	return a.countPlaces(ctx, l)
}

// listSelection is the row a list opens on: the one the shown list had selected
// when this one replaces it, else the pinned place, else Follow. The rows' roots
// and pin are in the same resolved form, so it compares strings and reads no
// file.
func (a *App) listSelection(tab core.Tab, l *wtList, pin string) int {
	root := pin
	if old := a.listFor(tab); old != nil {
		root = old.selectedRoot()
	}

	if root != "" {
		if i := slices.IndexFunc(l.rows, func(r wtRow) bool { return r.kind == wtPlace && r.root == root }); i >= 0 {
			return i
		}
	}

	return 0
}

// selectedRoot is the root of the selected row; "" for the Follow row.
func (l *wtList) selectedRoot() string {
	if l.sel < 0 || l.sel >= len(l.rows) || l.rows[l.sel].kind != wtPlace {
		return ""
	}

	return l.rows[l.sel].root
}

// countPlaces asks for the changes of every place row, each under one probe
// slot, so they share the rail's limit. ctx ends them with the list.
func (a *App) countPlaces(ctx context.Context, l *wtList) tea.Cmd {
	eng, sem, budget, gen := a.engine, a.probeSem, a.probeBudget, l.gen
	if eng == nil {
		return nil
	}

	var cmds []tea.Cmd

	for _, r := range l.rows {
		if r.kind != wtPlace {
			continue
		}

		root := r.root

		cmds = append(cmds, func() tea.Msg {
			ctx, done, err := admit(ctx, sem, budget)
			if err != nil {
				return wtStatsMsg{gen: gen, root: root, err: err}
			}
			defer done()

			st, err := eng.Stats(ctx, git.BranchSource{Worktree: root})

			return wtStatsMsg{gen: gen, root: root, stats: st, err: err}
		})
	}

	return tea.Batch(cmds...)
}

// onWorktreeStats fills in a row's counts. An answer for another list, or a
// failed one, leaves the row without counts.
func (a *App) onWorktreeStats(msg wtStatsMsg) {
	l := a.drawer.list
	if l == nil || msg.gen != l.gen {
		return
	}

	if msg.err != nil {
		if !errors.Is(msg.err, context.Canceled) {
			a.log.Debug("worktree list stats", "root", msg.root, "err", msg.err)
		}

		return
	}

	for i := range l.rows {
		if r := &l.rows[i]; r.kind == wtPlace && r.root == msg.root {
			r.stats, r.statsKnown = msg.stats, true
		}
	}
}

// closeWorktrees closes the list, ends its row probes and makes any build still
// running stale.
func (a *App) closeWorktrees() {
	if l := a.drawer.list; l != nil {
		l.cancel()

		a.drawer.list = nil
	}

	a.drawer.listGen++
}

// worktreesSync closes a list whose drawer left it behind: another tab shows,
// or the drawer closed or reviews another pane.
func (a *App) worktreesSync() {
	if a.drawer.list != nil && a.listFor(a.store.State().Tab()) == nil {
		a.closeWorktrees()
	}
}

// ---- keys ----

// handleWorktreesKey runs a key while the list has the keyboard. The leader
// works as everywhere and every key that is not here is dropped.
func (a *App) handleWorktreesKey(msg tea.KeyPressMsg) tea.Cmd {
	l := a.drawer.list

	switch bindingKey(msg) {
	case "j", "down":
		a.moveWorktrees(l, 1)
	case "k", "up":
		a.moveWorktrees(l, -1)
	case "esc":
		a.closeWorktrees()
	case "enter":
		a.pickWorktree(l)
	case "w":
		return a.openWorktrees()
	}

	return nil
}

// moveWorktrees selects the next row in a direction that a pick can name,
// skipping headers and gaps; at the end it stays.
func (a *App) moveWorktrees(l *wtList, dir int) {
	for i := l.sel + dir; i >= 0 && i < len(l.rows); i += dir {
		if k := l.rows[i].kind; k == wtFollow || k == wtPlace {
			l.sel = i

			break
		}
	}

	a.keepSelected(l)
}

// keepSelected scrolls the list by whole rows so that the selected one shows.
func (a *App) keepSelected(l *wtList) {
	box, ok := a.drawerBox(a.store.State())
	if !ok {
		return
	}

	if g, ok := wtGeometry(box.W, box.H, len(l.rows)); ok {
		l.top = wtTop(l.top, l.sel, g.vis, len(l.rows))
	}
}

// pickWorktree pins the drawer to the selected place, or turns following back
// on with the Follow row, and closes the list; the drawer retargets itself.
func (a *App) pickWorktree(l *wtList) {
	root := l.selectedRoot()

	a.closeWorktrees()

	// dispatch logs its own failures.
	_ = a.dispatch(core.PinReview{Root: root})
}
