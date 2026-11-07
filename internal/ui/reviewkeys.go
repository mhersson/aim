package ui

import (
	"cmp"
	"path/filepath"
	"slices"

	tea "charm.land/bubbletea/v2"

	"github.com/mhersson/aim/internal/core"
	"github.com/mhersson/aim/internal/git"
)

// The drawer's key notices; the others are in reviewops.go.
const (
	noticeFileDeleted = "the file is deleted"
	noticeNarrow      = "side by side needs a wider drawer"
	noticeNotFound    = "not found: "
)

// reviewKey binds a key, while the drawer has the keyboard, to a command.
type reviewKey struct{ key, command string }

// reviewKeys is the drawer's default keymap; [keys.drawer] is laid over it.
// The leader works as everywhere; a key that is not here is dropped.
var reviewKeys = []reviewKey{
	{"j", "review.line.next"},
	{"down", "review.line.next"},
	{"k", "review.line.prev"},
	{"up", "review.line.prev"},
	{"]", "review.hunk.next"},
	{"[", "review.hunk.prev"},
	{"}", "review.file.next"},
	{"{", "review.file.prev"},
	{"ctrl+d", "review.page.down"},
	{"ctrl+u", "review.page.up"},
	{"g", "review.top"},
	{"G", "review.bottom"},
	{"tab", "review.tree"},
	{"f", "review.full"},
	{"m", "review.markdown"},
	{"u", "review.split"},
	{"enter", "review.fold"},
	{"space", "review.mark"},
	{"v", "review.show.reviewed"},
	{"b", "review.mode.branch"},
	{"t", "review.mode.turn"},
	{"p", "review.mode.pr"},
	{"i", "review.conversation"},
	{"c", "review.checks"},
	{"R", "review.ready"},
	{"a", "review.approve"},
	{"M", "review.merge"},
	{"O", "review.browser"},
	{",", "review.turn.prev"},
	{".", "review.turn.next"},
	{"/", "review.search"},
	{"n", "review.match.next"},
	{"N", "review.match.prev"},
	{"y", "review.copy"},
	{"z", "review.zoom"},
	{"w", "review.worktrees"},
	{"e", "review.edit"},
	{"r", "review.refresh"},
	{"esc", "review.back"},
}

// treeCommand reports whether a drawer command works while the tree has the keyboard.
func treeCommand(id string) bool {
	switch id {
	case "review.line.next", "review.line.prev", "review.fold", "review.tree", "review.back", "review.zoom":
		return true
	}

	return false
}

// reviewCommands are the drawer's commands, each bound in reviewKeys or the leader keymap.
func reviewCommands() []command {
	cmd := func(id, title string, run func(*App) tea.Cmd) command {
		return command{id: id, icon: iconReview, title: title, run: run}
	}
	line := func(d int) func(*App) tea.Cmd { return func(a *App) tea.Cmd { return a.reviewLine(d) } }
	hunk := func(d int) func(*App) tea.Cmd {
		return func(a *App) tea.Cmd {
			if a.onPage(func(_ *reviewView, p *mdPage) { p.view.heading(d) }) {
				return nil
			}

			return a.reviewJump((*reviewView).blockStarts, d)
		}
	}
	file := func(d int) func(*App) tea.Cmd {
		return func(a *App) tea.Cmd { return a.reviewJump((*reviewView).fileStarts, d) }
	}
	page := func(d int) func(*App) tea.Cmd { return func(a *App) tea.Cmd { return a.reviewPage(d) } }
	mode := func(m core.ReviewMode) func(*App) tea.Cmd { return func(a *App) tea.Cmd { return a.setReviewMode(m) } }
	turn := func(d int) func(*App) tea.Cmd { return func(a *App) tea.Cmd { return a.stepReviewTurn(d) } }
	match := func(d int) func(*App) tea.Cmd {
		return func(a *App) tea.Cmd {
			if _, v, ok := a.reviewActive(); ok {
				a.reviewMatch(v, v.last, d)
			}

			return nil
		}
	}

	cmds := []command{
		cmd("review.toggle", "Review drawer", (*App).reviewToggle),
		cmd("review.line.next", "Review: next line", line(1)),
		cmd("review.line.prev", "Review: previous line", line(-1)),
		cmd("review.hunk.next", "Review: next hunk", hunk(1)),
		cmd("review.hunk.prev", "Review: previous hunk", hunk(-1)),
		cmd("review.file.next", "Review: next file", file(1)),
		cmd("review.file.prev", "Review: previous file", file(-1)),
		cmd("review.page.down", "Review: half a page down", page(1)),
		cmd("review.page.up", "Review: half a page up", page(-1)),
		cmd("review.top", "Review: top", func(a *App) tea.Cmd { return a.reviewEnd(false) }),
		cmd("review.bottom", "Review: bottom", func(a *App) tea.Cmd { return a.reviewEnd(true) }),
		cmd("review.tree", "Review: file tree", (*App).reviewTree),
		cmd("review.full", "Review: full file", (*App).toggleReviewFull),
		cmd("review.markdown", "Review: rendered or source", (*App).toggleReviewMarkdown),
		cmd("review.split", "Review: unified or side by side", (*App).reviewSplit),
		cmd("review.fold", "Review: fold or show removed lines", (*App).reviewFold),
		cmd("review.mark", "Review: mark the hunk reviewed", (*App).reviewMark),
		cmd("review.show.reviewed", "Review: show reviewed hunks too", (*App).reviewShowRead),
		cmd("review.mode.branch", "Review: branch mode", mode(core.ReviewBranch)),
		cmd("review.mode.turn", "Review: turn mode", mode(core.ReviewTurn)),
		cmd("review.mode.pr", "Review: pull request", (*App).reviewPR),
		cmd("review.conversation", "Review: conversation", func(a *App) tea.Cmd { return a.prAct(thenConversation) }),
		cmd("review.checks", "Review: checks", func(a *App) tea.Cmd { return a.prAct(thenChecks) }),
		cmd("review.ready", "Review: mark ready", (*App).reviewReady),
		cmd("review.approve", "Review: approve", (*App).reviewApprove),
		cmd("review.merge", "Review: merge", (*App).reviewMerge),
		cmd("review.browser", "Review: open in browser", func(a *App) tea.Cmd { return a.prAct(thenBrowser) }),
		cmd("review.turn.prev", "Review: previous turn", turn(-1)),
		cmd("review.turn.next", "Review: next turn", turn(1)),
		cmd("review.search", "Review: search", (*App).reviewSearch),
		cmd("review.match.next", "Review: next match", match(1)),
		cmd("review.match.prev", "Review: previous match", match(-1)),
		cmd("review.copy", "Review: copy the selection", (*App).reviewCopy),
		cmd("review.zoom", "Review: zoom", (*App).reviewZoom),
		cmd("review.worktrees", "Review: worktrees", (*App).openWorktrees),
		cmd("review.edit", "Review: open in the editor", (*App).reviewEdit),
		cmd("review.refresh", "Review: refresh", (*App).reviewReload),
		cmd("review.back", "Review: back, else close the drawer", (*App).reviewBack),
	}

	// A markdown page answers only its own keys; the others are dropped.
	for i, c := range cmds {
		if c.id == "review.toggle" || pageCommand(c.id) {
			continue
		}

		run := c.run
		cmds[i].run = func(a *App) tea.Cmd {
			if a.pageShown() {
				return nil
			}

			return run(a)
		}
	}

	return cmds
}

// ---- keyboard ----

// handleDrawerKey runs a key while the drawer has the keyboard.
func (a *App) handleDrawerKey(msg tea.KeyPressMsg) tea.Cmd {
	tab, v, ok := a.reviewActive()
	if !ok {
		return nil
	}

	if a.listFor(tab) != nil {
		a.clearDrawerSel(tab.ID)

		return a.handleWorktreesKey(msg)
	}

	if c := a.linksFor(tab); c != nil {
		a.clearDrawerSel(tab.ID)

		return a.handleLinksKey(c, msg)
	}

	if v.search != nil {
		if a.searchKey(v, msg) {
			a.clearDrawerSel(tab.ID)
		}

		return nil
	}

	key := bindingKey(msg)

	id, ok := a.keys.drawer.lookup(key)
	if !ok || (a.drawer.tree && !treeCommand(id)) {
		return nil
	}

	c, ok := a.reg.get(id)
	if !ok {
		a.log.Error("drawer key names a missing command", "key", key, "command", id)

		return nil
	}

	// A command the page drops does not act, so it keeps the selection.
	if id != "review.copy" && (id == "review.toggle" || !a.pageShown() || pageCommand(id)) {
		a.clearDrawerSel(tab.ID)
	}

	return c.run(a)
}

// clearDrawerSel drops the text selection when the drawer of tab holds it.
func (a *App) clearDrawerSel(tab core.TabID) {
	if a.text != nil && a.text.tab == tab {
		a.clearTextSel()
	}
}

// reviewCopy copies the drawer's selection and keeps it. With none it copies
// the cursor's unit: a paragraph or item on a page, a line or header of the
// diff, a paragraph of a rendered file.
func (a *App) reviewCopy() tea.Cmd {
	tab, v, ok := a.reviewActive()
	if !ok {
		return nil
	}

	if ts := a.text; ts != nil && ts.tab == tab.ID {
		return a.clip.copy(a.textSelected())
	}

	if _, p := a.activePage(); p != nil {
		return a.clip.copy(p.view.unitText(p.view.cursor))
	}

	if lay, ok := a.rowsLayout(tab.ID, v); ok {
		return a.clip.copy(v.unitText(lay, v.cursor))
	}

	return nil
}

// searchKey types into the open search prompt and reports whether the prompt
// took the key.
func (a *App) searchKey(v *reviewView, msg tea.KeyPressMsg) bool {
	switch key := msg.String(); {
	case key == "enter":
		a.reviewFind(v)
	case key == "esc":
		v.search = nil
	case key == "backspace":
		if r := []rune(*v.search); len(r) > 0 {
			*v.search = string(r[:len(r)-1])
		}
	case msg.Text != "":
		*v.search += printable(msg.Text)
	default:
		return false
	}

	return true
}

// drawerPaste appends pasted text to the search prompt while it is open;
// otherwise it is dropped.
func (a *App) drawerPaste(text string) {
	if v := a.drawer.views[a.store.State().Tab().ID]; v != nil && v.search != nil {
		*v.search += printable(text)
	}
}

// reviewFind jumps to the next row holding the typed query and closes the
// prompt; an empty query repeats the last one.
func (a *App) reviewFind(v *reviewView) {
	q := cmp.Or(*v.search, v.last)
	v.search = nil

	a.reviewMatch(v, q, 1)
}

// reviewMatch keeps q as the last search and moves to its next match, or its
// previous one when dir is -1; a miss says so.
func (a *App) reviewMatch(v *reviewView, q string, dir int) {
	if q == "" {
		return
	}

	v.last = q

	if _, p := a.activePage(); p != nil {
		find := p.view.find
		if dir < 0 {
			find = p.view.findPrev
		}

		if !find(q) {
			a.notice = noticeNotFound + q
		}

		p.rev++

		return
	}

	i, ok := searchRows(v.res, v.rows, v.cursor, dir, q)
	if !ok {
		a.notice = noticeNotFound + q

		return
	}

	a.reviewGo(v, i)
}

// reviewSearch opens the search prompt and gives the drawer the keyboard to type in it.
func (a *App) reviewSearch() tea.Cmd {
	_, v, ok := a.reviewActive()
	if !ok {
		return nil
	}

	a.drawerTake()
	a.closeWorktrees()
	a.closeLinks()

	q := ""
	v.search = &q

	return nil
}

// reviewBack steps back one level: it closes the search prompt, pops a page,
// leaves the tree or un-zooms the drawer; with none of those it closes the
// drawer as leader d does. It does nothing while the drawer slides out.
func (a *App) reviewBack() tea.Cmd {
	_, v, ok := a.reviewActive()
	if !ok || a.drawer.closing {
		return nil
	}

	switch {
	case v.search != nil:
		v.search = nil
	case v.page() != nil:
		return a.popPage(a.store.State().Tab().ID, v)
	case a.drawer.tree:
		a.reviewLeaveTree(v, false)
	case v.zoom:
		return a.reviewZoom()
	default:
		a.endSlide()

		return a.closeDrawer(true)
	}

	return nil
}

// ---- moving ----

// reviewBodyH is the rows of the drawer's diff column.
func (a *App) reviewBodyH(v *reviewView) int {
	st := a.store.State()

	box, ok := a.drawerBox(st)
	if !ok {
		return 0
	}

	return a.drawerLayoutOf(box, v, drawerTree(st)).diffH
}

// reviewGo puts the cursor on a row and scrolls only as far as it needs.
func (a *App) reviewGo(v *reviewView, row int) {
	if len(v.rows) == 0 {
		return
	}

	v.cursor = v.lineStart(min(max(row, 0), len(v.rows)-1))
	v.top = v.revealTop(a.reviewBodyH(v))
}

// reviewLine moves the cursor a row; in the tree it moves the selected file.
func (a *App) reviewLine(delta int) tea.Cmd {
	if a.onPage(func(_ *reviewView, p *mdPage) { p.view.move(delta) }) {
		return nil
	}

	_, v, ok := a.reviewActive()
	if !ok {
		return nil
	}

	if !a.drawer.tree {
		a.reviewStep(v, delta)

		return nil
	}

	files := v.treeFiles()
	if len(files) == 0 {
		return nil
	}

	v.treeSel = files[min(max(slices.Index(files, v.treeSel)+delta, 0), len(files)-1)]

	return nil
}

// reviewStep moves the cursor delta lines. A line taller than the view starts
// at the top when the cursor lands on it.
func (a *App) reviewStep(v *reviewView, delta int) {
	if len(v.rows) == 0 {
		return
	}

	i := v.lineStart(min(max(v.cursor, 0), len(v.rows)-1))

	for ; delta > 0 && v.lineEnd(i) < len(v.rows); delta-- {
		i = v.lineEnd(i)
	}

	for ; delta < 0 && i > 0; delta++ {
		i = v.lineStart(i - 1)
	}

	a.reviewGo(v, i)

	if h := a.reviewBodyH(v); v.lineEnd(i)-i > h && v.top < i {
		v.top = i
	}
}

func (a *App) reviewPage(dir int) tea.Cmd {
	if a.onPage(func(_ *reviewView, p *mdPage) { p.view.page(dir) }) {
		return nil
	}

	_, v, ok := a.reviewActive()
	if !ok {
		return nil
	}

	to := v.cursor + dir*max(a.reviewBodyH(v)/2, 1)
	if dir > 0 && v.lineStart(min(to, len(v.rows)-1)) == v.lineStart(v.cursor) {
		to = v.lineEnd(v.cursor) // a tall line: on to the next line
	}

	a.reviewGo(v, to)

	return nil
}

func (a *App) reviewEnd(bottom bool) tea.Cmd {
	if a.onPage(func(_ *reviewView, p *mdPage) { p.view.end(bottom) }) {
		return nil
	}

	_, v, ok := a.reviewActive()
	if !ok {
		return nil
	}

	if bottom {
		a.reviewGo(v, len(v.rows)-1)
	} else {
		a.reviewGo(v, 0)
	}

	return nil
}

// reviewJump moves the cursor to the next (dir 1) or previous (-1) row among
// the starts the view lists; at the end it stays.
func (a *App) reviewJump(starts func(*reviewView) []int, dir int) tea.Cmd {
	_, v, ok := a.reviewActive()
	if !ok {
		return nil
	}

	rows := starts(v)
	if dir < 0 {
		slices.Reverse(rows)
	}

	if i := slices.IndexFunc(rows, func(r int) bool { return (r-v.cursor)*dir > 0 }); i >= 0 {
		a.reviewGo(v, rows[i])
	}

	return nil
}

// blockStarts are the rows where a hunk's first changed line is or, in the
// full-file view, a block of changes begins.
func (v *reviewView) blockStarts() []int {
	var out []int

	inBlock := false

	for i, r := range v.rows {
		change := (r.kind == rowFull && r.other < 0) || r.kind == rowGhost || r.kind == rowFold || (r.kind == rowMD && r.mark != markNone)

		switch {
		case r.kind == rowHunk:
			out = append(out, v.firstChange(i))
		case change && (!inBlock || (r.kind == rowMD && !v.sameMDBlock(i))):
			out = append(out, i)
		}

		inBlock = change
	}

	return out
}

// sameMDBlock reports whether rendered row i goes on the block the row before
// it is in: the same file and source range.
func (v *reviewView) sameMDBlock(i int) bool {
	if i == 0 {
		return false
	}

	prev, r := v.rows[i-1], v.rows[i]

	return prev.kind == rowMD && prev.file == r.file && prev.edit == r.edit && prev.other == r.other
}

// firstChange is the row of the first added or removed line of the hunk whose
// header is row hdr; a hunk with none stands for its header.
func (v *reviewView) firstChange(hdr int) int {
	for i := hdr + 1; i < len(v.rows); i++ {
		r := v.rows[i]
		if r.kind != rowLine && r.kind != rowPair {
			break
		}

		lines := v.res.Files[r.file].Hunks[r.hunk].Lines
		if lines[r.line].Kind != git.Context || (r.other >= 0 && lines[r.other].Kind != git.Context) {
			return i
		}
	}

	return hdr
}

// fileStarts are the rows of the files' headers.
func (v *reviewView) fileStarts() []int {
	var out []int

	for i, r := range v.rows {
		if r.kind == rowFile {
			out = append(out, i)
		}
	}

	return out
}

// reviewFold folds the ghost block under the cursor into one row, or shows it
// again from that row; on a rendered line it toggles a fold, else follows the
// line's links; in the tree it jumps to the selected file.
func (a *App) reviewFold() tea.Cmd {
	if v, p := a.activePage(); p != nil {
		if p.view.fold() {
			p.rev++

			return nil
		}

		return a.followLine(a.store.State().Tab().ID, v, p.view.targets())
	}

	_, v, ok := a.reviewActive()
	if !ok {
		return nil
	}

	if a.drawer.tree {
		a.reviewLeaveTree(v, true)

		return nil
	}

	if v.cursor < 0 || v.cursor >= len(v.rows) {
		return nil
	}

	r := v.rows[v.cursor]
	if r.kind == rowMD {
		if a.foldRendered(v, r) {
			return nil
		}

		return a.followLine(a.store.State().Tab().ID, v, a.renderedTargets(v, r))
	}

	if a.foldRead(v, r) {
		return nil
	}

	if r.kind != rowGhost && r.kind != rowFold {
		return nil
	}

	fold := r.kind == rowGhost

	if v.folded == nil {
		v.folded = map[string]bool{}
	}

	v.folded[foldKey(v.res.Files[r.file].Path, r.block)] = fold
	v.rebuild(v.rowsSplit)

	// The block's row is another one now: the fold row, or the first ghost line.
	if i := slices.IndexFunc(v.rows, func(n reviewRow) bool {
		return n.file == r.file && n.block == r.block && (n.kind == rowFold || (!fold && n.kind == rowGhost))
	}); i >= 0 {
		a.reviewGo(v, i)
	}

	return nil
}

// foldRendered opens or closes the fold whose head is rendered row r and
// reports whether there was one.
func (a *App) foldRendered(v *reviewView, r reviewRow) bool {
	f := v.mdFiles[v.res.Files[r.file].Path]
	if f == nil {
		return false
	}

	for _, fold := range f.doc.layout(max(v.mdW, 1), v.mdIcons).Folds {
		if fold.Line == r.line {
			f.doc.toggleFold(fold.ID)
			v.rebuild(v.rowsSplit)

			return true
		}
	}

	return false
}

// renderedTargets are the links and paths of rendered row r.
func (a *App) renderedTargets(v *reviewView, r reviewRow) []mdTarget {
	f := v.mdFiles[v.res.Files[r.file].Path]
	if f == nil {
		return nil
	}

	return lineTargets(f.doc.layout(max(v.mdW, 1), v.mdIcons), r.line)
}

// reviewSplit switches between unified and side by side; a drawer too narrow
// for two sides keeps showing unified and says so.
func (a *App) reviewSplit() tea.Cmd {
	_, v, ok := a.reviewActive()
	if !ok {
		return nil
	}

	v.split = !v.split

	st := a.store.State()

	if box, shown := a.drawerBox(st); v.split && shown && !a.drawerLayoutOf(box, v, drawerTree(st)).split {
		a.notice = noticeNarrow
	}

	return nil
}

// ---- tree ----

// treeFiles are the files' indexes in the order the tree lists them.
func (v *reviewView) treeFiles() []int {
	var out []int

	for _, it := range v.derived().tree {
		if !it.dir {
			out = append(out, it.file)
		}
	}

	return out
}

// reviewTree is tab: it opens a hidden tree and gives it the keyboard, gives a
// shown tree the keyboard, and hides a tree that has it.
func (a *App) reviewTree() tea.Cmd {
	tab, v, ok := a.reviewActive()
	if !ok {
		return nil
	}

	if a.drawer.tree {
		// dispatch logs its own failures.
		_ = a.dispatch(core.SetReviewTree{Open: false})
		a.drawer.tree = false

		return nil
	}

	if !tab.Review.Tree {
		if err := a.dispatch(core.SetReviewTree{Open: true}); err != nil {
			return nil
		}
	}

	a.drawerTake()
	a.drawer.tree, v.treeSel = true, 0

	if v.cursor >= 0 && v.cursor < len(v.rows) {
		v.treeSel = v.rows[v.cursor].file
	}

	return nil
}

// reviewLeaveTree gives the diff the keyboard back; the tree stays open. With
// jump the cursor first goes to the selected file's header.
func (a *App) reviewLeaveTree(v *reviewView, jump bool) {
	if jump {
		a.reviewJumpFile(v, v.treeSel)
	}

	a.drawer.tree = false
}

// reviewJumpFile puts the cursor on a file's header and scrolls it to the top.
func (a *App) reviewJumpFile(v *reviewView, file int) {
	i := slices.IndexFunc(v.rows, func(r reviewRow) bool { return r.kind == rowFile && r.file == file })
	if i < 0 {
		return
	}

	v.cursor, v.top = i, i
	v.top = v.revealTop(a.reviewBodyH(v))
}

// ---- editor ----

// reviewEditor is the editor the e key opens: $VISUAL, else $EDITOR, else vi.
func reviewEditor(getenv func(string) string) string {
	for _, k := range [...]string{"VISUAL", "EDITOR"} {
		if e := getenv(k); e != "" {
			return e
		}
	}

	return "vi"
}

// editLine is the line of f the row stands for: the cursor line's new number,
// its old number on a removed line of a hunk, the hunk's new start on a hunk
// header, the first new line after the block on a ghost line of the full view
// (the last line when the block ends the file), the first source line of a
// rendered row and 1 on a file header.
func editLine(f git.FileDiff, r reviewRow) int {
	line := 1

	switch r.kind {
	case rowHunk:
		line = f.Hunks[r.hunk].NewStart
	case rowLine, rowPair:
		h := f.Hunks[r.hunk]

		li := r.other
		if li < 0 {
			li = r.line
		}

		if l := h.Lines[li]; l.Kind == git.Del {
			line = l.Old
		} else {
			line = l.New
		}
	case rowFull:
		line = r.line + 1
	case rowGhost, rowFold, rowMD:
		line = r.edit
	}

	return max(line, 1)
}

// reviewEdit opens the file at the cursor line in the editor, in a new shell
// pane beside the drawer's pane, and closes the drawer so the pane shows.
func (a *App) reviewEdit() tea.Cmd {
	v, p := a.activePage()

	var tab core.Tab

	if p == nil {
		var ok bool

		if tab, v, ok = a.reviewActive(); !ok {
			return nil
		}
	}

	if v.pr.Copy {
		a.notice = noticeCopyNoEdit

		return nil
	}

	if p != nil {
		return a.openEditor(a.store.State().Tab(), v, p.path, p.view.sourceLine())
	}

	if v.cursor < 0 || v.cursor >= len(v.rows) {
		return nil
	}

	r := v.rows[v.cursor]
	f := v.res.Files[r.file]

	if f.Status == git.Deleted {
		a.notice = noticeFileDeleted

		return nil
	}

	// An editor reads +{command} and a leading dash as options, and the pane
	// starts elsewhere when the root is gone; the absolute path is neither.
	return a.openEditor(tab, v, filepath.Join(v.root, f.Path), editLine(f, r))
}
