package ui

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"path/filepath"
	"slices"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/mhersson/aim/internal/core"
	"github.com/mhersson/aim/internal/git"
	"github.com/mhersson/aim/internal/md"
)

// reviewOpenTimeout bounds the repository check of a drawer that is opening.
const reviewOpenTimeout = 5 * time.Second

// The drawer's notices.
const (
	noticeNoDrawer   = "no review drawer is open"
	noticeCopyNoEdit = "no file to edit: this pull request has no checkout here"
	noticeNoTurns    = "no turns: this pane has no agent session"
	noticeNoTurnsYet = "no turns yet"
	noticeNoTurnsIn  = "no turns in %s · %s yet"
	noticeRemoved    = "worktree removed: "
	noticeNotRepo    = "not a git repository"
	noticeBinaryFull = "a binary file has no full view"
	noticeFirstTurn  = "turn 1 is the first turn"
	noticeNotMD      = "not a markdown file"
)

// drawerDir is the directory the drawer reviews for a pane of tab: the root it
// is pinned to, else the pane's followed place.
func (a *App) drawerDir(st core.State, tab core.Tab, pane core.PaneID) string {
	if pinnedTo(tab, pane) {
		return tab.Pin.Root
	}

	return st.Panes[pane].FollowDir()
}

// pinnedTo reports whether tab holds its drawer for pane on a pinned root.
func pinnedTo(tab core.Tab, pane core.PaneID) bool {
	return tab.Pin != nil && tab.Pin.Pane == pane
}

// openReview opens the active tab's drawer on a pane of that tab. Whether the
// pane is in a repository, and whether it has turns, are found off the update
// loop; the drawer opens when they answer, in turn mode when the pane has turns
// and the config allows it, else in branch mode.
func (a *App) openReview(pane core.PaneID, focus bool) tea.Cmd {
	return a.openReviewFor(pane, focus, false)
}

// openReviewFor is openReview; with anyDir a pane in no repository, or with no
// directory, still opens, for a pull request whose diff needs nothing from it.
func (a *App) openReviewFor(pane core.PaneID, focus, anyDir bool) tea.Cmd {
	a.drawer.prNext = nil

	st := a.store.State()
	tab := st.Tab()

	if _, ok := st.Panes[pane]; !ok || !slices.Contains(tab.Panes, pane) {
		return nil
	}

	cwd := a.drawerDir(st, tab, pane)
	if cwd == "" {
		if !anyDir {
			a.notice = noticeNotRepo

			return nil
		}

		tabID := tab.ID

		return func() tea.Msg {
			return reviewOpenMsg{tab: tabID, pane: pane, focus: focus, anyDir: true, err: git.ErrNotRepo}
		}
	}

	session, tabID, pinned, eng := paneSession(st, pane), tab.ID, pinnedTo(tab, pane), a.engine

	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(a.ctx, reviewOpenTimeout)
		defer cancel()

		msg := reviewOpenMsg{tab: tabID, pane: pane, cwd: cwd, focus: focus, anyDir: anyDir}

		if msg.repo, msg.err = a.git.Open(ctx, cwd); msg.err != nil {
			// A pinned worktree that went still opens a drawer, which says so.
			if pinned && !usableDir(cwd) {
				msg.err = errWorktreeRemoved{root: cwd}
			}

			return msg
		}

		if session == "" {
			return msg
		}

		if eng == nil {
			return msg
		}

		if list, err := eng.TurnList(ctx, msg.repo.Root, session); err == nil {
			msg.turns = len(list.Turns)
		}

		return msg
	}
}

// onReviewOpen opens the drawer once the repository check answered. An answer
// for another tab, or a pane that moved or went, is dropped.
func (a *App) onReviewOpen(msg reviewOpenMsg) tea.Cmd {
	st := a.store.State()

	next := a.drawer.prNext
	if next != nil && next.tab == msg.tab && next.pane == msg.pane {
		a.drawer.prNext = nil
	} else {
		next = nil
	}

	if _, ok := st.Panes[msg.pane]; !ok || st.Tab().ID != msg.tab || a.drawerDir(st, st.Tab(), msg.pane) != msg.cwd || !slices.Contains(st.Tab().Panes, msg.pane) {
		return nil
	}

	if st.Tab().Review == nil {
		if v := a.drawer.views[msg.tab]; v != nil {
			v.zoom = false
		}
	}

	var gone errWorktreeRemoved

	switch {
	case errors.Is(msg.err, context.Canceled):
		return nil
	case errors.As(msg.err, &gone):
		// The drawer opens to show it.
	case msg.err != nil && msg.anyDir:
		// The pull request's diff needs nothing from the pane.
		if next != nil {
			next.noRepo = true
		}
	case errors.Is(msg.err, git.ErrNotRepo):
		a.notice = noticeNotRepo

		return nil
	case msg.err != nil:
		a.log.Warn("review: open", "pane", msg.pane, "err", msg.err)
		a.notice = "review: " + msg.err.Error()

		return nil
	}

	mode := core.ReviewBranch
	if msg.turns > 0 && !a.drawer.cfg.BranchFirst() {
		mode = core.ReviewTurn
	}

	if err := a.dispatch(core.OpenReview{Pane: msg.pane, Mode: mode}); err != nil {
		return nil
	}

	if msg.focus {
		a.drawerTake()
	}

	if next != nil {
		next.open = true
		a.drawer.prNext = next
	}

	// The check found the pane's repository: hand it to the watch bookkeeping
	// so the drawer need not resolve it again. A pinned root is not the pane's.
	if pinnedTo(st.Tab(), msg.pane) {
		return nil
	}

	w := a.watches.panes[msg.pane]
	if w == nil {
		w = &paneWatch{}
		a.watches.panes[msg.pane] = w
	}

	// Only a real no-repository answer is the watch's to record: another error
	// says nothing about the pane, and the watch re-resolves only on a new cwd.
	if msg.err != nil && msg.anyDir {
		if !errors.Is(msg.err, git.ErrNotRepo) {
			return nil
		}

		w.cwd = msg.cwd

		return a.onPaneRepo(paneRepoMsg{id: msg.pane, cwd: msg.cwd, err: git.ErrNotRepo})
	}

	if w.cwd == msg.cwd && w.want == msg.repo.Root {
		return nil
	}

	w.cwd = msg.cwd

	return a.onPaneRepo(paneRepoMsg{id: msg.pane, cwd: msg.cwd, repo: msg.repo})
}

// reviewActive returns the active tab and its view; with no drawer open it
// shows the notice and reports false.
func (a *App) reviewActive() (core.Tab, *reviewView, bool) {
	tab := a.store.State().Tab()
	if tab.Review == nil {
		a.notice = noticeNoDrawer

		return core.Tab{}, nil, false
	}

	return tab, a.reviewViewOf(tab.ID), true
}

// setReviewMode switches the open drawer to branch or turn mode and loads it;
// turn mode starts on the latest turn.
func (a *App) setReviewMode(mode core.ReviewMode) tea.Cmd {
	tab, v, ok := a.reviewActive()
	if !ok {
		return nil
	}

	session := paneSession(a.store.State(), tab.Review.Pane)
	v.after = nil
	a.dropPages(v)

	if mode == core.ReviewTurn {
		if ok, wait := a.turnsGate(tab.ID, v, session, func() tea.Cmd { return a.setReviewMode(mode) }); !ok {
			return wait
		}
	}

	if err := a.dispatch(core.SetReviewMode{Mode: mode}); err != nil {
		return nil
	}

	// Leaving PR mode may also leave its root: the sync after this update loads
	// the place the drawer follows.
	if v.pr != (core.ReviewPR{}) {
		return nil
	}

	v.mode, v.session = mode, session

	return a.reviewRetarget(tab.ID, v, 0)
}

// turnsGate decides whether a turn key can go on. A pane with no session, or a
// worktree with no turns, gets the notice that says why. While the count is
// another session's, which a new session leaves behind, the key waits: the
// answer of the probe for the new session runs retry again.
func (a *App) turnsGate(tab core.TabID, v *reviewView, session string, retry func() tea.Cmd) (bool, tea.Cmd) {
	switch {
	case v.pr.Copy && session != "":
		// The copy's turn count says nothing about the pane's.
		return true, nil
	case session == "":
		a.notice = noticeNoTurns
	case v.loaded && v.turnsFor != session:
		// Hold the key only while a probe can answer it.
		if probe := a.reviewCount(tab, v); probe != nil || v.loading {
			v.after = retry

			return false, probe
		}

		a.notice = noticeNoTurnsYet
	case v.loaded && v.turns == 0 && v.foreign:
		a.notice = fmt.Sprintf(noticeNoTurnsIn, printable(v.project), printable(v.branch))
	case v.loaded && v.turns == 0:
		a.notice = noticeNoTurnsYet
	default:
		return true, nil
	}

	return false, nil
}

// stepReviewTurn shows the previous (delta -1) or next (1) turn. In branch
// mode it enters turn mode on the latest turn instead.
func (a *App) stepReviewTurn(delta int) tea.Cmd {
	tab, v, ok := a.reviewActive()
	if !ok {
		return nil
	}

	v.after = nil

	if ok, wait := a.turnsGate(tab.ID, v, paneSession(a.store.State(), tab.Review.Pane), func() tea.Cmd { return a.stepReviewTurn(delta) }); !ok {
		return wait
	}

	ts, isTurn := v.src.(git.TurnSource)
	if tab.Review.Mode != core.ReviewTurn || !isTurn {
		return a.setReviewMode(core.ReviewTurn)
	}

	latest := v.turns
	if latest == 0 {
		a.notice = noticeNoTurnsYet

		return nil
	}

	cur := ts.Turn
	if cur == 0 {
		cur = latest
	}

	next := cur + delta

	switch {
	case next < 1:
		a.notice = noticeFirstTurn

		return nil
	case next > latest:
		a.notice = fmt.Sprintf("turn %d is the latest turn", latest)

		return nil
	case next == latest:
		next = 0
	}

	return a.reviewRetarget(tab.ID, v, next)
}

// toggleReviewFull shows the file under the cursor whole, or back as hunks.
func (a *App) toggleReviewFull() tea.Cmd {
	tab, v, ok := a.reviewActive()
	if !ok || v.cursor < 0 || v.cursor >= len(v.rows) {
		return nil
	}

	f := v.res.Files[v.rows[v.cursor].file]
	if f.Binary {
		a.notice = noticeBinaryFull

		return nil
	}

	if v.fullOn[f.Path] {
		delete(v.fullOn, f.Path)

		// A rendered file keeps its text and the read being made of it.
		if v.mdOn[f.Path] {
			return nil
		}

		if stop := v.fullStop[f.Path]; stop != nil {
			stop()
		}

		delete(v.fullHave, f.Path)
		delete(v.fullAsk, f.Path)
		delete(v.fullStop, f.Path)

		if _, shown := v.full[f.Path]; shown {
			delete(v.full, f.Path)
			v.keepSpans()
			v.gen++
			v.rebuild(v.rowsSplit)
		}

		return nil
	}

	if v.fullOn == nil {
		v.fullOn = map[string]bool{}
	}

	v.fullOn[f.Path] = true

	// The text of a rendered file is already here; show it whole when m goes.
	if _, have := v.full[f.Path]; have {
		v.rebuild(v.rowsSplit)
	}

	return a.reviewFullSync(tab.ID, v)
}

// toggleReviewMarkdown shows the markdown file under the cursor rendered, or
// back as its diff, or its full text while f is on.
func (a *App) toggleReviewMarkdown() tea.Cmd {
	tab, v, ok := a.reviewActive()
	if !ok || v.cursor < 0 || v.cursor >= len(v.rows) {
		return nil
	}

	f := v.res.Files[v.rows[v.cursor].file]

	switch {
	case v.mdOn[f.Path]:
		a.dropRendered(v, f.Path)
		v.rebuild(v.rowsSplit)

		return nil
	case !md.IsMarkdown(f.Path) || f.Binary:
		a.notice = noticeNotMD

		return nil
	case f.Status == git.Deleted:
		a.notice = noticeFileDeleted

		return nil
	}

	if v.mdOn == nil {
		v.mdOn = map[string]bool{}
	}

	v.mdOn[f.Path] = true
	v.rebuild(v.rowsSplit)

	return a.reviewFullSync(tab.ID, v)
}

// dropRendered shows path as source again: its doc goes, and its text with it
// unless the full view wants that.
func (a *App) dropRendered(v *reviewView, path string) {
	delete(v.mdOn, path)
	delete(v.mdFiles, path)
	delete(v.mdAsk, path)

	if v.fullOn[path] {
		return
	}

	if stop := v.fullStop[path]; stop != nil {
		stop()
	}

	delete(v.full, path)
	delete(v.fullHave, path)
	delete(v.fullAsk, path)
	delete(v.fullStop, path)
	v.keepSpans()
	v.gen++
}

// reviewFullSync reads the two sides of every file the operator wants shown
// whole or rendered whose blobs differ from the ones the view holds or is
// reading, and parses the rendered files whose text is here.
func (a *App) reviewFullSync(tab core.TabID, v *reviewView) tea.Cmd {
	if len(v.fullOn)+len(v.mdOn) == 0 || a.engine == nil {
		return nil
	}

	var cmds []tea.Cmd

	paths := slices.Collect(maps.Keys(v.fullOn))
	for path := range v.mdOn {
		if !v.fullOn[path] {
			paths = append(paths, path)
		}
	}

	for _, path := range paths {
		i := slices.IndexFunc(v.res.Files, func(f git.FileDiff) bool { return f.Path == path })
		if i < 0 {
			continue
		}

		f := v.res.Files[i]
		ids := blobIDs{f.OldOID, f.NewOID}

		if have, ok := v.fullHave[path]; ok && have == ids {
			continue
		}

		if ask, ok := v.fullAsk[path]; ok && ask.ids == ids {
			continue
		}

		if v.fullAsk == nil {
			v.fullAsk = map[string]fullRead{}
		}

		read := a.drawer.nextRead()
		v.fullAsk[path] = fullRead{ids: ids, read: read}
		cmds = append(cmds, a.reviewFullCmd(tab, v, f, ids, read))
	}

	return tea.Batch(append(cmds, a.reviewMDParse(tab, v))...)
}

// reviewFullCmd reads both sides of f and their edit script. The view ends the
// read when it is replaced, closed or turned away from the file.
func (a *App) reviewFullCmd(tab core.TabID, v *reviewView, f git.FileDiff, ids blobIDs, read int) tea.Cmd {
	eng, root, epoch := a.engine, v.root, v.epoch

	ctx, cancel := context.WithTimeout(a.ctx, reviewLoadTimeout)

	if stop := v.fullStop[f.Path]; stop != nil {
		stop()
	}

	if v.fullStop == nil {
		v.fullStop = map[string]context.CancelFunc{}
	}

	v.fullStop[f.Path] = cancel

	return func() tea.Msg {
		defer cancel()

		old, nw, err := eng.FileText(ctx, root, f)
		if err == nil {
			// The diff can run long, so a read the view has ended skips it.
			err = ctx.Err()
		}

		text := fullText{err: err}
		if err == nil {
			text = newFullText(old, nw, f.Hunks)
		}

		return reviewFullMsg{tab: tab, gen: epoch, path: f.Path, ids: ids, read: read, text: text}
	}
}

// onReviewFull shows a file whole once its text arrived. The text is dropped
// when the source changed since, or the operator turned the full view off; when
// the file changed under the read, the text is read again.
func (a *App) onReviewFull(msg reviewFullMsg) tea.Cmd {
	v := a.drawer.views[msg.tab]
	if v == nil || msg.gen != v.epoch {
		return nil
	}

	if ask, ok := v.fullAsk[msg.path]; !ok || ask.read != msg.read {
		return nil
	}

	delete(v.fullAsk, msg.path)
	delete(v.fullStop, msg.path)

	if !v.fullOn[msg.path] && !v.mdOn[msg.path] {
		return nil
	}

	i := slices.IndexFunc(v.res.Files, func(f git.FileDiff) bool { return f.Path == msg.path })
	if i < 0 || (blobIDs{v.res.Files[i].OldOID, v.res.Files[i].NewOID}) != msg.ids {
		return a.reviewFullSync(msg.tab, v)
	}

	if v.full == nil {
		v.full = map[string]fullText{}
	}

	if v.fullHave == nil {
		v.fullHave = map[string]blobIDs{}
	}

	v.full[msg.path], v.fullHave[msg.path] = msg.text, msg.ids
	v.keepSpans()
	v.gen++
	v.rebuild(v.rowsSplit)

	return a.reviewMDParse(msg.tab, v)
}

// reviewMDParse parses, off the update loop, each rendered file whose text is
// here and whose blobs no doc or parse in flight holds.
func (a *App) reviewMDParse(tab core.TabID, v *reviewView) tea.Cmd {
	var cmds []tea.Cmd

	for path := range v.mdOn {
		i := slices.IndexFunc(v.res.Files, func(f git.FileDiff) bool { return f.Path == path })
		if i < 0 {
			continue
		}

		ids := blobIDs{v.res.Files[i].OldOID, v.res.Files[i].NewOID}
		text, have := v.full[path]

		if !have || v.fullHave[path] != ids || text.err != nil {
			continue
		}

		if f := v.mdFiles[path]; f != nil && f.ids == ids {
			continue
		}

		if ask, ok := v.mdAsk[path]; ok && ask == ids {
			continue
		}

		if v.mdAsk == nil {
			v.mdAsk = map[string]blobIDs{}
		}

		v.mdAsk[path] = ids
		epoch := v.epoch
		src := []byte(strings.Join(text.new, "\n") + "\n")
		base := filepath.Join(v.root, filepath.Dir(path))

		fold := a.foldKey()

		cmds = append(cmds, parseCmd(src, base, a.drawer.hl.HighlightLang, func(d *mdDoc) tea.Msg {
			return reviewMDMsg{tab: tab, gen: epoch, path: path, ids: ids, file: &mdFile{doc: d.withFoldKey(fold), text: text, ids: ids}}
		}))
	}

	return tea.Batch(cmds...)
}

// onReviewMD shows a rendered file once its parse answered. The answer is
// dropped when the source changed since, or the operator turned the file back
// to source; when the file changed under the parse, it is read again.
func (a *App) onReviewMD(msg reviewMDMsg) tea.Cmd {
	v := a.drawer.views[msg.tab]
	if v == nil || msg.gen != v.epoch {
		return nil
	}

	if ask, ok := v.mdAsk[msg.path]; ok && ask == msg.ids {
		delete(v.mdAsk, msg.path)
	}

	if !v.mdOn[msg.path] {
		return nil
	}

	i := slices.IndexFunc(v.res.Files, func(f git.FileDiff) bool { return f.Path == msg.path })
	if i < 0 || (blobIDs{v.res.Files[i].OldOID, v.res.Files[i].NewOID}) != msg.ids {
		return a.reviewFullSync(msg.tab, v)
	}

	if v.mdFiles == nil {
		v.mdFiles = map[string]*mdFile{}
	}

	msg.file.doc.withPictures(docPictures{reg: a.pictures, root: v.root})
	v.mdFiles[msg.path] = msg.file
	v.rebuild(v.rowsSplit)

	return nil
}
