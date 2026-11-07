package ui

import (
	"fmt"
	"slices"
	"strings"

	tea "charm.land/bubbletea/v2"

	"github.com/mhersson/aim/internal/core"
	"github.com/mhersson/aim/internal/github"
)

// noticePRListNone is what the list says when no agent's branch has an open
// pull request.
const noticePRListNone = "no open pull requests on agent branches"

// What a row of the other tabs says when it cannot open: the tab has no pane
// to open the drawer on, or there is no folder for a copy (it takes the number).
const (
	noticePRNoPane = "no pane here to open the drawer on; O opens it in the browser"
	noticeCopyOff  = "no cache folder for a copy of #%d; O opens it in the browser"
)

// prListKey identifies a pull request in the list: its repository keyed
// "host/owner/name" and its number.
type prListKey struct {
	repo   string
	number int
}

// prListView is the open pull request list: the shown tab and, per tab, the
// selection. A selection is kept by key so a poll that reorders the rows keeps
// it; idx is where it last stood, for the nearest row when the pull request
// leaves the list.
type prListView struct {
	tab prListTab
	sel [prTabCount]prListKey
	idx [prTabCount]int
}

// prListRow is one listed pull request and the agent the keys act through.
type prListRow struct {
	key  prListKey
	pr   core.PRInfo
	pane core.PaneID
	root string // the agent's working directory, where the pull request is read from
}

// prListRows lists the open pull requests of the agents' branches, one row per
// pull request however many agents share its branch, with the first of them in
// rail order as its agent. Repositories come by name and each one's rows newest
// update first, so the rows of a repository are adjacent.
func (a *App) prListRows(st core.State) []prListRow {
	panes := st.AgentPanes()
	rank := func(id core.PaneID) int {
		return slices.Index(railGroupOrder[:], st.Panes[id].State)
	}

	// A state the rail has no group for comes last, as an unknown one does.
	slices.SortStableFunc(panes, func(x, y core.PaneID) int {
		rx, ry := rank(x), rank(y)
		if rx < 0 {
			rx = len(railGroupOrder)
		}

		if ry < 0 {
			ry = len(railGroupOrder)
		}

		return rx - ry
	})

	seen := map[prListKey]bool{}

	var rows []prListRow

	for _, id := range panes {
		repo, ok := a.prRepoOf(id)
		if !ok {
			continue
		}

		pr, ok := a.prOfPane(st, id)
		if !ok || pr.State != prOpenState {
			continue
		}

		key := prListKey{repo: repo.String(), number: pr.Number}
		if seen[key] {
			continue
		}

		seen[key] = true

		rows = append(rows, prListRow{key: key, pr: pr, pane: id, root: a.rail.panes[id].probe.headCwd})
	}

	sortPRListRows(rows)

	return rows
}

// sortPRListRows puts the repositories by name and each one's rows newest
// update first, so the rows of a repository are adjacent.
func sortPRListRows(rows []prListRow) {
	slices.SortStableFunc(rows, func(x, y prListRow) int {
		if c := strings.Compare(prRepoName(x.key.repo), prRepoName(y.key.repo)); c != 0 {
			return c
		}

		if c := strings.Compare(x.key.repo, y.key.repo); c != 0 {
			return c
		}

		if c := y.pr.UpdatedAt.Compare(x.pr.UpdatedAt); c != 0 {
			return c
		}

		return y.key.number - x.key.number
	})
}

// prRepoName is a repository as the list names it: owner/name on github.com,
// host/owner/name elsewhere.
func prRepoName(key string) string {
	return printable(strings.TrimPrefix(key, "github.com/"))
}

// selected is the index of the selected row, or -1 for an empty list. A pull
// request that left the list moves the selection to the row now where it stood.
func (v *prListView) selected(rows []prListRow) int {
	t := v.tab
	if len(rows) == 0 {
		v.idx[t] = 0

		return -1
	}

	for i, r := range rows {
		if r.key == v.sel[t] {
			v.idx[t] = i

			return i
		}
	}

	v.idx[t] = min(v.idx[t], len(rows)-1)
	v.sel[t] = rows[v.idx[t]].key

	return v.idx[t]
}

// move selects the row delta away, stopping at the ends.
func (v *prListView) move(rows []prListRow, delta int) {
	i := v.selected(rows)
	if i < 0 {
		return
	}

	v.idx[v.tab] = min(max(i+delta, 0), len(rows)-1)
	v.sel[v.tab] = rows[v.idx[v.tab]].key
}

// prListCommand is `leader g l`.
func (a *App) prListCommand() tea.Cmd {
	a.endLeader()

	if a.prs.src == nil {
		a.notice = noticePRsOff

		return nil
	}

	a.prList = &prListView{}

	return a.prSearchOpen()
}

// prListShow shows tab. A searched tab needs the reads; without them the list
// stays where it is.
func (a *App) prListShow(tab prListTab) {
	if tab != prTabAgents && a.reads == nil {
		a.notice = noticePRsOff

		return
	}

	a.prList.tab = tab
}

// prListStep shows the next tab (forward) or the previous one, wrapping.
func (a *App) prListStep(forward bool) {
	t := a.prList.tab

	switch {
	case forward && t == prTabCount-1:
		t = 0
	case forward:
		t++
	case t == 0:
		t = prTabCount - 1
	default:
		t--
	}

	a.prListShow(t)
}

// handlePRListKey answers the open list; any other key is swallowed.
func (a *App) handlePRListKey(msg tea.KeyPressMsg) tea.Cmd {
	return a.runKey(a.keys.prList, bindingKey(msg))
}

// prListRefresh asks the poller for each listed repository.
func (a *App) prListRefresh(rows []prListRow) {
	if a.prs.src == nil {
		return
	}

	done := map[string]bool{}

	for _, r := range rows {
		if done[r.key.repo] {
			continue
		}

		done[r.key.repo] = true

		if repo, ok := repoOfKey(r.key.repo); ok {
			a.prs.src.Refresh(repo)
		}
	}
}

// prCheckout is the directory a pull request of repo is read from when no agent
// has its branch: a pane resolved to the repository, the active tab's focused
// pane when it is one, else the lowest pane id.
func (a *App) prCheckout(repo string) (dir string, ok bool) {
	focus := a.store.State().Tab().Focus

	var (
		best  core.PaneID
		found bool
	)

	for id, pp := range a.prs.panes {
		if !pp.ok || pp.repo.String() != repo || pp.answered.cwd == "" {
			continue
		}

		if id == focus {
			return pp.answered.cwd, true
		}

		if !found || paneIDLess(id, best) {
			best, found = id, true
		}
	}

	if !found {
		return "", false
	}

	return a.prs.panes[best].answered.cwd, true
}

// paneIDLess orders pane ids "p2" before "p10".
func paneIDLess(x, y core.PaneID) bool {
	if len(x) != len(y) {
		return len(x) < len(y)
	}

	return x < y
}

// prListOpen shows the row's pull request. A row with an agent switches to the
// agent's tab, as the rail's jump does; any other opens the active tab's drawer
// on the focused pane, or says there is no pane to open it on.
func (a *App) prListOpen(row prListRow) tea.Cmd {
	st := a.store.State()

	if row.pane == "" {
		return a.prListOpenHere(st, row)
	}

	a.prList = nil

	ti, ok := st.TabOf(row.pane)
	if !ok {
		return nil
	}

	var cmds []tea.Cmd

	if ti != st.Active {
		cmds = append(cmds, a.act(core.SelectTab{Index: ti}))
	}

	cmds = append(cmds, a.focusPane(row.pane))
	a.railRelease()

	want := &prWant{repo: row.key.repo, number: row.key.number, root: row.root, base: row.pr.BaseBranch}
	cmds = append(cmds, a.openWithPR(row.pane, want, thenNothing))

	return tea.Batch(cmds...)
}

// prListOpenHere opens the drawer on the focused pane with the row's pull
// request: from a pane's checkout of the repository when there is one, else from
// AIM's private copy of it.
func (a *App) prListOpenHere(st core.State, row prListRow) tea.Cmd {
	focus := st.Tab().Focus
	if focus == "" {
		a.notice = noticePRNoPane

		return nil
	}

	want := &prWant{repo: row.key.repo, number: row.key.number, base: row.pr.BaseBranch}

	if dir, ok := a.prCheckout(row.key.repo); ok {
		want.root = dir
	} else {
		if a.reads == nil {
			a.notice = noticePRsOff

			return nil
		}

		root, ok := a.prCopyRoot(row.key.repo)
		if !ok {
			a.notice = fmt.Sprintf(noticeCopyOff, row.key.number)

			return nil
		}

		want.root, want.inCopy, want.branch = root, true, row.pr.HeadBranch
	}

	a.prList = nil

	return a.openWithPR(focus, want, thenNothing)
}

// prListReread asks the poller for each repository of the shown tab and reads
// the searches again, as `r` does; nil when the list is closed.
func (a *App) prListReread() tea.Cmd {
	if a.prList == nil {
		return nil
	}

	a.prListRefresh(a.prListRowsOf(a.store.State(), a.prList.tab))

	return a.prSearchCmd()
}

// prListMerge is `M` on a row: the merge box opens over the list at once when
// the row's repository has been polled, since the box freezes the repository's
// settings when it opens. Otherwise the merge holds on the list until the poll
// answers.
func (a *App) prListMerge(row prListRow) {
	a.prHoldEnd()

	if repo, ok := repoOfKey(row.key.repo); ok && !a.prs.polled[repo] && a.prs.src != nil {
		a.prs.held = row.key
		a.prs.dirty = true
		a.notice = fmt.Sprintf(noticePRFetching, row.key.number)
		a.prs.src.Refresh(repo)

		return
	}

	a.prListMergeBox(row)
}

// prListMergeBox opens the merge box over the list from the row: the details
// the poll has, else the row's, and the head the row lists. The root is the
// row's agent's, else a pane's checkout of the repository, else none, which
// offers no clean-up.
func (a *App) prListMergeBox(row prListRow) {
	root := row.root
	if root == "" {
		root, _ = a.prCheckout(row.key.repo)
	}

	a.openMergeBox(row.key.repo, row.key.number, row.pr, row.pr.HeadOID, root, true)
}

// prHoldEnd drops the list's merge hold and the notice it set.
func (a *App) prHoldEnd() {
	h := a.prs.held
	if h == (prListKey{}) {
		return
	}

	a.prs.held = prListKey{}
	a.prs.dirty = true

	if a.notice == fmt.Sprintf(noticePRFetching, h.number) {
		a.notice = ""
	}
}

// prHoldSync drops the hold once the list is closed or its selected row is not
// the held one, so the repository goes back to the watch it had.
func (a *App) prHoldSync() {
	h := a.prs.held
	if h == (prListKey{}) {
		return
	}

	if a.prList != nil {
		rows := a.prListRowsOf(a.store.State(), a.prList.tab)
		if i := a.prList.selected(rows); i >= 0 && rows[i].key == h {
			return
		}
	}

	a.prHoldEnd()
}

// prHoldAnswered ends the hold when its repository's poll answers for the held
// pull request and opens the merge box from the row, or says why not. An answer
// that reaches a list with something open above it is dropped silently.
func (a *App) prHoldAnswered(s github.Snapshot) {
	h := a.prs.held
	if h == (prListKey{}) || s.Repo.String() != h.repo || s.Err == nil && s.Asked != h.number {
		return
	}

	a.prHoldEnd()

	if a.prList == nil || a.modalAboveList() {
		return
	}

	if s.Err != nil {
		a.notice = fmt.Sprintf(noticePRsFailed, h.repo, prReason(s.Err))

		return
	}

	// Before the row check: a poll that loses the pull request also removes an
	// Agents row, and that says so rather than dropping silently.
	if _, known := prInfoByNumber(a.store.State(), h.repo, h.number); !known {
		if f := s.Focused; f != nil && f.Number == h.number && f.Err != nil {
			a.notice = fmt.Sprintf(noticePRsFailed, h.repo, prReason(f.Err))
		} else {
			a.notice = fmt.Sprintf(noticePRGone, h.number)
		}

		return
	}

	rows := a.prListRowsOf(a.store.State(), a.prList.tab)
	if i := a.prList.selected(rows); i >= 0 && rows[i].key == h {
		a.prListMergeBox(rows[i])
	}
}
