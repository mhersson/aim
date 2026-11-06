package ui

import (
	"context"
	"errors"
	"log/slog"
	"maps"
	"slices"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/mhersson/aim/internal/core"
	"github.com/mhersson/aim/internal/git"
)

const (
	// reviewLoadTimeout bounds one diff load or full-file read.
	reviewLoadTimeout = 30 * time.Second
	// reviewFlashFor is how long rows a live update added flash.
	reviewFlashFor = 600 * time.Millisecond
	// maxQueuedPaths is how many changed paths a stale drawer remembers before
	// it settles for "everything changed".
	maxQueuedPaths = 1000
)

// errWorktreeRemoved says the worktree a drawer reviews is gone.
type errWorktreeRemoved struct{ root string }

func (e errWorktreeRemoved) Error() string { return noticeRemoved + e.root }

var (
	errNotRepo  = errors.New(noticeNotRepo)
	errDiffsOff = errors.New("diffs are off: no run directory for temporary indexes")
)

// reviewInfo is what a load learns beyond the diff: what the title, the mode
// tag, the mode row and the info rows show, and the files' hidden flags.
type reviewInfo struct {
	project, branch, base string
	turns                 int
	noSnapshot            bool
	foreign               bool
	hidden                []bool
}

// reviewOpenMsg answers the repository check of a drawer that is opening.
type reviewOpenMsg struct {
	tab   core.TabID
	pane  core.PaneID
	cwd   string
	focus bool
	repo  git.Repo
	turns int // how many turns the pane's session has
	err   error
}

// reviewLoadedMsg answers one load. gen is the view's epoch when it was asked.
type reviewLoadedMsg struct {
	tab      core.TabID
	gen      int
	res      git.Result
	err      error
	live     bool       // the answer to a watcher batch, not to a new source
	infoOnly bool       // only info is new: a past turn learning its turn count
	src      git.Source // the source shown when it is not the one asked for
	session  string     // the session info.turns counts the turns of
	info     reviewInfo
	common   string       // the repository's marks key, "" when it was not found
	ids      [][]string   // the id of every hunk of res, per file and hunk
	marks    *loadedMarks // set when the job read the marks file
}

// reviewFullMsg answers the read of one file's two sides. gen is the view's
// epoch when it was asked and ids the blobs it read.
type reviewFullMsg struct {
	tab  core.TabID
	gen  int
	path string
	ids  blobIDs
	read int // the serial of the read that made this answer
	text fullText
}

// reviewMDMsg answers the parse of one rendered file. gen is the view's epoch
// when it was asked and ids the blobs the file was parsed from.
type reviewMDMsg struct {
	tab  core.TabID
	gen  int
	path string
	ids  blobIDs
	file *mdFile
}

// reviewFlashMsg ends the flash of rows a live update added.
type reviewFlashMsg struct{ tab core.TabID }

// nextRead hands out the serial of a full-file read.
func (d *drawerState) nextRead() int {
	d.reads++

	return d.reads
}

// adoptTurns takes the turn count a load probed, unless it probed for a session
// the pane has since left: the count then stays the other session's, which
// turnsFor says, and a probe for the current session is due.
func (v *reviewView) adoptTurns(session string, turns int) {
	if session == v.session {
		v.turns, v.turnsFor = turns, session
	}
}

// nextEpoch hands out an epoch no view has used, so an answer from a view that
// was replaced never matches its successor.
func (d *drawerState) nextEpoch() int {
	d.epochs++

	return d.epochs
}

// reviewedPanes are the panes some tab's drawer reviews.
func reviewedPanes(st core.State) map[core.PaneID]bool {
	var out map[core.PaneID]bool

	for _, t := range st.Tabs {
		if t.Review == nil {
			continue
		}

		if out == nil {
			out = map[core.PaneID]bool{}
		}

		out[t.Review.Pane] = true
	}

	return out
}

// paneSession is the session of an agent pane; a shell has none.
func paneSession(st core.State, id core.PaneID) string {
	if m := st.Panes[id]; m.Kind == core.KindAgent {
		return string(m.Session)
	}

	return ""
}

// repoProject is the name the rail gives the repository of r.
func repoProject(r git.Repo) string { return r.Name() }

// reviewSync brings the active tab's view in line with core's drawer: it makes
// the view for a new or retargeted drawer, loads it when it has no result or
// the pane's repository or session changed, and runs a load that changes seen
// while the tab was hidden left waiting. Views of tabs without a drawer go.
func (a *App) reviewSync() tea.Cmd {
	st := a.store.State()

	for id, v := range a.drawer.views {
		if t, ok := tabByID(st, id); ok && t.Review != nil {
			continue
		}

		v.stop()
		delete(a.drawer.views, id)
	}

	tab := st.Tab()

	rv := tab.Review
	if rv == nil {
		return nil
	}

	v := a.drawer.views[tab.ID]
	if v == nil || v.pane != rv.Pane {
		v = a.reviewReset(tab.ID, rv.Pane)
	}

	root, resolved := a.paneRoot(rv.Pane)
	if pinnedTo(tab, rv.Pane) {
		root, resolved = tab.Pin.Root, true
	}

	// A drawer in PR mode holds the root its pull request was opened from.
	var shownPR core.ReviewPR

	if rv.PR != nil {
		shownPR, root, resolved = *rv.PR, rv.PR.Root, true
	}

	switch {
	case !resolved:
		// A pane that is in no repository, or left its repository, shows the error once.
		if w := a.watches.panes[rv.Pane]; (v.root != "" || w != nil && w.notRepo) && !errors.Is(v.err, errNotRepo) {
			v = a.reviewReset(tab.ID, rv.Pane)
			v.err, v.loaded, v.mode = errNotRepo, true, rv.Mode
			v.session = paneSession(st, rv.Pane)
		}

		return nil
	case v.root != "" && v.root != root:
		v = a.reviewReset(tab.ID, rv.Pane)
	}

	need := !v.loaded && !v.loading

	if v.root != root {
		v.root, need = root, true
	}

	if shownPR != v.pr {
		v.pr, need = shownPR, true
	}

	if session := paneSession(st, rv.Pane); session != v.session {
		v.session, v.after = session, nil
		need = need || rv.Mode == core.ReviewTurn && rv.PR == nil
	}

	if rv.Mode != v.mode {
		v.mode, need = rv.Mode, true
	}

	// A turn drawer that showed the branch because its worktree had no turns
	// shows the worktree's latest turn as soon as there is one.
	if _, isTurn := v.src.(git.TurnSource); rv.Mode == core.ReviewTurn && rv.PR == nil && !isTurn && v.loaded && v.session != "" &&
		v.turnsFor == v.session && v.turns > 0 {
		need = true
	}

	if need {
		return a.reviewRetarget(tab.ID, v, 0)
	}

	// The count is the previous session's, or the own directory moved, which
	// changes whether the shown root is the agent's own.
	if v.loaded && v.session != "" && (v.turnsFor != v.session || v.own != st.Panes[rv.Pane].WorkDir()) {
		return a.reviewCount(tab.ID, v)
	}

	return a.reviewFlush(true, tab.ID, v)
}

func tabByID(st core.State, id core.TabID) (core.Tab, bool) {
	i := slices.IndexFunc(st.Tabs, func(t core.Tab) bool { return t.ID == id })
	if i < 0 {
		return core.Tab{}, false
	}

	return st.Tabs[i], true
}

// reviewReset replaces the tab's view by an empty one for pane, ending the
// work of the old one.
func (a *App) reviewReset(tab core.TabID, pane core.PaneID) *reviewView {
	old := a.drawer.views[tab]
	if old != nil {
		old.stop()
	}

	a.clearDrawerSel(tab)

	delete(a.drawer.views, tab)

	v := a.reviewViewOf(tab)
	v.pane, v.split = pane, a.drawer.cfg.Split()

	if old != nil {
		v.prBase, v.prBranch, v.unpushed, v.showRead = old.prBase, old.prBranch, old.unpushed, old.showRead
	}

	return v
}

// reviewRetarget points the view at its mode's source, turn being the turn to
// show (0 is the latest), and loads it. A turn view of a pane with no session
// shows the branch; core keeps the mode.
func (a *App) reviewRetarget(tab core.TabID, v *reviewView, turn int) tea.Cmd {
	if v.root == "" {
		return nil
	}

	switch {
	case v.pr != (core.ReviewPR{}):
		v.src = git.PRSource{Worktree: v.root, Number: v.pr.Number, Copy: v.pr.Copy}
	case v.mode == core.ReviewTurn && v.session != "":
		v.src = git.TurnSource{Worktree: v.root, Session: v.session, Turn: turn}
	default:
		v.src = git.BranchSource{Worktree: v.root}
	}

	v.epoch = a.drawer.nextEpoch()
	v.queued, v.after, v.then = nil, nil, thenNothing
	v.stopReads()
	a.dropPages(v)

	return a.reviewStart(tab, v, nil, false)
}

// reviewJob is what one load needs, copied off the view so the command reads
// no view state.
type reviewJob struct {
	tab      core.TabID
	epoch    int
	src      git.Source
	root     string
	session  string
	own      string     // the pane's own directory, which decides the no-turns notice
	batch    *git.Batch // nil: a full diff of src
	infoOnly bool       // skip the diff: refresh only what describe finds
	prBase   string     // the base of the pull request src shows
	prBranch string     // its head branch, when it is shown from a copy
	prCopy   bool       // src shows a pull request from a copy: its HEAD and turns mean nothing
	prev     git.Result // the result batch updates
	keep     reviewInfo // what a load that skips a probe keeps
	eng      DiffEngine
	git      Git
	log      *slog.Logger

	// The reviewed hunks: the common dir already found for root, the
	// repositories core holds marks of, and the file to read the others from.
	common, commonRoot string
	known              map[string]core.RepoMarks
	files              markFiles
	now                time.Time
}

// reviewStart loads the view's source off the update loop, ending a load still
// running for it. It always answers, with an error when diffs are off. With
// infoOnly it only refreshes what the drawing shows besides the diff.
func (a *App) reviewStart(tab core.TabID, v *reviewView, b *git.Batch, infoOnly bool) tea.Cmd {
	if v.cancel != nil {
		v.cancel()
	}

	ctx, cancel := context.WithTimeout(a.ctx, reviewLoadTimeout)
	v.cancel, v.loading = cancel, true

	keep := reviewInfo{project: v.project, branch: v.branch, base: v.base, foreign: v.foreign}
	if v.turnsFor == v.session {
		keep.turns = v.turns
	}

	v.own = a.store.State().Panes[v.pane].WorkDir()

	job := reviewJob{
		tab: tab, epoch: v.epoch, src: v.src, root: v.root, session: v.session, own: v.own, batch: b, infoOnly: infoOnly, prev: v.res, prBase: v.prBase, prBranch: v.prBranch, prCopy: v.pr.Copy,
		keep: keep, eng: a.engine, git: a.git, log: a.log,
		common: v.common, commonRoot: v.commonRoot, known: a.store.State().Marks, files: a.marks, now: a.clock.Now(),
	}

	return func() tea.Msg {
		defer cancel()

		return job.run(ctx)
	}
}

func (j reviewJob) run(ctx context.Context) reviewLoadedMsg {
	msg := reviewLoadedMsg{tab: j.tab, gen: j.epoch, live: j.batch != nil, infoOnly: j.infoOnly, session: j.session, info: j.keep}

	if j.eng == nil {
		msg.err = errDiffsOff

		return msg
	}

	// What the view last knew stays, so the title still names the worktree.
	if !usableDir(j.root) {
		msg.err, msg.infoOnly = errWorktreeRemoved{root: j.root}, false

		return msg
	}

	msg.info.foreign = j.foreign(ctx)

	if j.infoOnly {
		j.describe(ctx, &msg.info)

		return msg
	}

	// What the drawing shows besides the diff is found beside the diff, so a
	// save reaches the screen as soon as the diff is done.
	described := make(chan struct{})

	go func() {
		defer close(described)

		j.describe(ctx, &msg.info)
	}()

	res, err := j.diff(ctx, j.src)
	if errors.Is(err, git.ErrNoTurns) {
		// A session with no turns yet shows the branch until its first turn.
		msg.src = git.BranchSource{Worktree: j.root}
		res, err = j.eng.Diff(ctx, msg.src)
	}

	<-described

	if msg.src != nil {
		msg.info.turns = 0
	}

	switch {
	case errors.Is(err, git.ErrNoSnapshot):
		msg.info.noSnapshot = true
		j.countTurns(ctx, &msg.info)
	case err != nil:
		msg.err = err
	default:
		// Tree order, before anything indexes by file. Update sorts its own result, so prev's order is free.
		res.Files = inTreeOrder(res.Files)
		msg.res = res
		msg.info.hidden = hiddenFlags(res.Files)
		j.marked(ctx, &msg)

		switch {
		case j.countsFromDiff() && msg.src == nil:
			msg.info.turns = res.Turns
		case j.countsAfterDiff() && msg.src == nil:
			j.countTurnsAt(ctx, &msg.info, res)
		}
	}

	return msg
}

// countsFromDiff reports whether a load of j.src learns the turn count from the
// diff itself, so describe leaves it alone.
func (j reviewJob) countsFromDiff() bool {
	_, ok := j.src.(git.TurnSource)

	return ok && !j.infoOnly
}

// countsAfterDiff reports whether a load of j.src counts the turns once the
// diff is done, from the working tree the diff already snapshotted.
func (j reviewJob) countsAfterDiff() bool {
	_, ok := j.src.(git.BranchSource)

	return ok && !j.infoOnly
}

// countTurnsAt is countTurns for a live result: its New side is the working
// tree, so the count needs no snapshot of its own.
func (j reviewJob) countTurnsAt(ctx context.Context, info *reviewInfo, res git.Result) {
	if !res.Live || j.session == "" {
		j.countTurns(ctx, info)

		return
	}

	list, err := j.eng.TurnListAt(ctx, j.root, j.session, res.New)
	if err != nil {
		j.log.Debug("review: turns", "root", j.root, "err", err)

		return
	}

	info.turns = len(list.Turns)
}

// countTurns sets info.turns to the length of the root's list of turns, unless
// the root has none or the count is not available.
func (j reviewJob) countTurns(ctx context.Context, info *reviewInfo) {
	if j.session == "" || j.prCopy {
		info.turns = 0

		return
	}

	list, err := j.eng.TurnList(ctx, j.root, j.session)
	if err != nil {
		j.log.Debug("review: turns", "root", j.root, "err", err)

		return
	}

	info.turns = len(list.Turns)
}

// diff computes src, updating the previous result when a batch asks for it.
func (j reviewJob) diff(ctx context.Context, src git.Source) (git.Result, error) {
	if j.batch != nil {
		return j.eng.Update(ctx, j.prev, src, *j.batch)
	}

	return j.eng.Diff(ctx, src)
}

// foreign reports whether the root the job loads is not the root of the agent's
// own directory. A pane with no session has no turns to be foreign to.
func (j reviewJob) foreign(ctx context.Context) bool {
	if j.session == "" {
		return false
	}

	foreign, err := isForeign(ctx, j.git, j.own, j.root)
	if err != nil {
		return j.keep.foreign
	}

	return foreign
}

// isForeign reports whether root is not the root of the agent's own directory
// own, as when own is in no repository or gone. It returns ctx's error when the
// check was cut short.
func isForeign(ctx context.Context, g Git, own, root string) (bool, error) {
	switch own {
	case "":
		return true, nil
	case root:
		return false, nil
	}

	r, err := g.Open(ctx, own)
	if err != nil && ctx.Err() != nil {
		return false, ctx.Err()
	}

	return err != nil || !sameDir(r.Root, root), nil
}

// describe fills what the drawing shows besides the diff. The head and the base
// change only with git's own state, so a batch of plain paths keeps them.
func (j reviewJob) describe(ctx context.Context, info *reviewInfo) {
	probe := j.batch == nil || j.batch.Git || j.batch.Overflow || info.project == ""
	if !probe && (j.session == "" || j.countsFromDiff() || j.countsAfterDiff()) {
		return
	}

	r, err := j.git.Open(ctx, j.root)
	if err != nil {
		j.log.Debug("review: open", "root", j.root, "err", err)

		return
	}

	if probe {
		info.project = repoProject(r)

		if j.prCopy {
			info.branch = j.prBranch
		} else if h, err := j.git.Head(ctx, r); err == nil {
			info.branch = h.Branch
			if h.Detached {
				info.branch = h.OID[:min(shortOIDLen, len(h.OID))]
			}
		}

		base := ""

		switch s := j.src.(type) {
		case git.BranchSource:
			base = s.Base
		case git.PRSource:
			base = j.prBase
		}

		if base == "" && !j.prCopy {
			if base, err = j.git.DefaultBranch(ctx, r); err != nil {
				j.log.Debug("review: default branch", "root", j.root, "err", err)
			}
		}

		info.base = base
	}

	if !j.countsFromDiff() && !j.countsAfterDiff() {
		j.countTurns(ctx, info)
	}
}

// onReviewLoaded applies a load's answer unless its source was replaced since.
func (a *App) onReviewLoaded(msg reviewLoadedMsg) tea.Cmd {
	v := a.drawer.views[msg.tab]
	if v == nil || msg.gen != v.epoch {
		return nil
	}

	v.loading, v.cancel = false, nil
	active := a.store.State().Tab().ID == msg.tab

	if msg.infoOnly {
		in := msg.info
		v.project, v.branch, v.base, v.foreign = in.project, in.branch, in.base, in.foreign

		before := v.turns
		v.adoptTurns(msg.session, in.turns)

		// The latest turn is whichever the list ends with, so a new count moves it.
		if ts, ok := v.src.(git.TurnSource); ok && ts.Turn == 0 && v.turns != before {
			v.queued = nil

			return tea.Batch(a.reviewResume(active, v), a.reviewStart(msg.tab, v, nil, false))
		}

		return tea.Batch(a.reviewResume(active, v), a.reviewFlush(active, msg.tab, v))
	}

	a.adoptMarks(v, msg)

	flash := a.reviewApply(v, msg)

	return tea.Batch(flash, a.reviewFullSync(msg.tab, v), a.reviewResume(active, v), a.reviewFlush(active, msg.tab, v))
}

// reviewCount refreshes the turn count, which is the previous session's, with
// a load that skips the diff. A load already running answers first, and the
// next sync asks again if its count is still not the session's.
func (a *App) reviewCount(tab core.TabID, v *reviewView) tea.Cmd {
	if v.loading || v.root == "" {
		return nil
	}

	return a.reviewStart(tab, v, nil, true)
}

// reviewResume runs the turn key that waited for the count, now that the count
// is the session's. A key pressed in a tab that is no longer shown is dropped.
func (a *App) reviewResume(active bool, v *reviewView) tea.Cmd {
	if v.after == nil || v.turnsFor != v.session {
		return nil
	}

	resume := v.after
	v.after = nil

	if !active {
		return nil
	}

	return resume()
}

// reviewApply replaces the view's result and runs what was asked of a pull
// request whose diff this is.
func (a *App) reviewApply(v *reviewView, msg reviewLoadedMsg) tea.Cmd {
	flash := a.reviewReplace(v, msg)

	return tea.Batch(flash, a.reviewThen(v, msg))
}

// reviewReplace replaces the view's result. A new source starts at the top with
// no full-file view; a live update keeps the cursor on its hunk and flashes
// the rows it added.
func (a *App) reviewReplace(v *reviewView, msg reviewLoadedMsg) tea.Cmd {
	in := msg.info
	live := msg.live && msg.src == nil && v.loaded && v.err == nil && msg.err == nil
	prev := v.res

	v.project, v.branch, v.base, v.noSnapshot, v.foreign = in.project, in.branch, in.base, in.noSnapshot, in.foreign
	v.adoptTurns(msg.session, in.turns)
	v.hiddenIn, v.err, v.loaded = in.hidden, msg.err, true

	if msg.src != nil {
		v.src = msg.src
	}

	if live {
		v.forgetGone(msg.res)
		v.keepSpans()
	} else {
		v.full, v.fullOn, v.fullHave, v.fullAsk, v.folded = nil, nil, nil, nil, nil
		v.mdOn, v.mdFiles, v.mdAsk = nil, nil, nil
		v.carry = nil
	}

	v.gen++
	v.rebuildFor(msg.res, v.rowsSplit)

	if !live {
		v.cursor, v.top, v.treeSel, v.fresh = 0, 0, 0, nil

		return nil
	}

	v.treeSel = followFile(prev, msg.res, v.treeSel)

	fresh := freshRows(prev, msg.res, v.rows)

	switch {
	case len(fresh) > 0:
		v.fresh = fresh
	case sameResult(prev, msg.res):
		// A reload that changed nothing leaves a running flash alone.
		return nil
	default:
		v.fresh = nil

		return nil
	}

	v.flashUntil = a.clock.Now().Add(reviewFlashFor)
	tab := msg.tab

	return a.tick(reviewFlashFor, func(time.Time) tea.Msg { return reviewFlashMsg{tab: tab} })
}

// sameResult reports whether two results are the same diff: trees are named by
// their content.
func sameResult(a, b git.Result) bool {
	return a.Old == b.Old && a.New == b.New && a.Turn == b.Turn &&
		a.Live == b.Live && len(a.Files) == len(b.Files)
}

// followFile returns the index in next of the file at index i of prev, else the
// nearest index.
func followFile(prev, next git.Result, i int) int {
	if i >= 0 && i < len(prev.Files) {
		path := prev.Files[i].Path
		if j := slices.IndexFunc(next.Files, func(f git.FileDiff) bool { return f.Path == path }); j >= 0 {
			return j
		}
	}

	return min(max(i, 0), max(len(next.Files)-1, 0))
}

// forgetGone drops the full-file and rendered state of files res no longer
// holds, or holds as binary, and the rendered state of files it holds as deleted.
func (v *reviewView) forgetGone(res git.Result) {
	paths := slices.Collect(maps.Keys(v.fullOn))
	paths = slices.AppendSeq(paths, maps.Keys(v.mdOn))

	for _, path := range paths {
		i := slices.IndexFunc(res.Files, func(f git.FileDiff) bool { return f.Path == path })
		kept := i >= 0 && !res.Files[i].Binary

		if !kept {
			delete(v.fullOn, path)
		}

		if !kept || res.Files[i].Status == git.Deleted {
			delete(v.mdOn, path)
			delete(v.mdFiles, path)
			delete(v.mdAsk, path)
		}

		if v.fullOn[path] || v.mdOn[path] {
			continue
		}

		if stop := v.fullStop[path]; stop != nil {
			stop()
		}

		delete(v.full, path)
		delete(v.fullHave, path)
		delete(v.fullAsk, path)
		delete(v.fullStop, path)
	}
}

// onReviewFlash ends the flash once its time is up; a later flash that moved
// the end is left to its own tick.
func (a *App) onReviewFlash(msg reviewFlashMsg) {
	v := a.drawer.views[msg.tab]
	if v == nil || a.clock.Now().Before(v.flashUntil) {
		return
	}

	v.fresh, v.flashUntil = nil, time.Time{}
}

// mergeBatch adds b to the changes q holds. Past maxQueuedPaths it settles for
// "everything changed".
func mergeBatch(q *git.Batch, b git.Batch) *git.Batch {
	if q == nil {
		c := b

		return &c
	}

	out := git.Batch{Git: q.Git || b.Git, Overflow: q.Overflow || b.Overflow}
	if len(q.Paths)+len(b.Paths) > maxQueuedPaths {
		out.Overflow = true

		return &out
	}

	out.Paths = slices.Compact(slices.Sorted(slices.Values(slices.Concat(q.Paths, b.Paths))))

	return &out
}

// reviewBatch routes a watcher batch to the drawer of every tab that reviews
// the root. The active tab's recomputes at once; a hidden tab's waits for its
// tab to show.
func (a *App) reviewBatch(root string, b git.Batch) tea.Cmd {
	if len(a.drawer.views) == 0 {
		return nil
	}

	active := a.store.State().Tab().ID

	var cmds []tea.Cmd

	for id, v := range a.drawer.views {
		if v.root != root {
			continue
		}

		// A page is a file of the root, whatever the drawer shows, pull request mode included.
		if c := a.pageBatch(id, v); c != nil {
			cmds = append(cmds, c)
		}

		if v.pr != (core.ReviewPR{}) {
			continue
		}

		v.queued = mergeBatch(v.queued, b)
		cmds = append(cmds, a.reviewFlush(id == active, id, v))
	}

	return tea.Batch(cmds...)
}

// reviewHook recomputes the drawers that review the pane when a hook event
// arrives that the watcher cannot report: a turn's start, whose snapshot ref
// never wakes it, or any event while a drawer's root is not watched, which then
// stands in for the file changes. A turn start whose snapshot is still being
// taken (snapped) recomputes once it is done, as the turn exists only then.
func (a *App) reviewHook(pane core.PaneID, turnStart bool, snapped <-chan struct{}) tea.Cmd {
	if turnStart && snapped != nil {
		return a.reviewAfterSnapshot(pane, snapped)
	}

	if turnStart {
		return a.reviewRefresh(pane)
	}

	return a.refreshViews(pane, func(v *reviewView) bool { return !a.rootWatched(v.root) })
}

// reviewSnappedMsg says a turn's snapshot is done.
type reviewSnappedMsg struct{ pane core.PaneID }

// reviewAfterSnapshot waits for a turn's snapshot or the app's end, off the
// update loop, and answers with reviewSnappedMsg for the former.
func (a *App) reviewAfterSnapshot(pane core.PaneID, snapped <-chan struct{}) tea.Cmd {
	ctx := a.ctx

	return func() tea.Msg {
		select {
		case <-snapped:
			return reviewSnappedMsg{pane: pane}
		case <-ctx.Done():
			return nil
		}
	}
}

// reviewRefresh queues a full recompute for every drawer that reviews the pane.
// A hidden tab's drawer is marked stale, as a batch would.
func (a *App) reviewRefresh(pane core.PaneID) tea.Cmd { return a.refreshViews(pane, nil) }

// refreshViews is reviewRefresh for the drawers of the pane that pick holds for.
func (a *App) refreshViews(pane core.PaneID, pick func(v *reviewView) bool) tea.Cmd {
	active := a.store.State().Tab().ID

	var cmds []tea.Cmd

	for id, v := range a.drawer.views {
		if v.pane != pane || v.root == "" || v.pr != (core.ReviewPR{}) || pick != nil && !pick(v) {
			continue
		}

		v.queued = mergeBatch(v.queued, git.Batch{Overflow: true})
		cmds = append(cmds, a.reviewFlush(id == active, id, v))
	}

	return tea.Batch(cmds...)
}

// reviewFlush starts the load the changes in v.queued call for. A load already
// running keeps them for its answer; a hidden tab keeps them until it shows. A
// turn that is not live does not change with the files, but the count of turns
// may: it is recounted on every batch, since the open turn may have started to
// differ from the working tree.
func (a *App) reviewFlush(active bool, tab core.TabID, v *reviewView) tea.Cmd {
	if !active || v.queued == nil || v.loading || !v.loaded {
		return nil
	}

	b := *v.queued
	v.queued = nil

	if v.err == nil && !v.res.Live {
		_, isTurn := v.src.(git.TurnSource)

		switch {
		case isTurn || b.Git || b.Overflow:
			return a.reviewStart(tab, v, &b, true)
		case !v.noSnapshot:
			return nil
		}
	}

	return a.reviewStart(tab, v, &b, false)
}
