package ui

import (
	"encoding/binary"
	"errors"
	"fmt"
	"image"
	"image/color"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/charmbracelet/x/ansi"

	"github.com/mhersson/aim/internal/core"
	"github.com/mhersson/aim/internal/git"
	"github.com/mhersson/aim/internal/md"
	"github.com/mhersson/aim/internal/ui/diffview"
	"github.com/mhersson/aim/internal/ui/theme"
)

const (
	// Tints mixed into a ground.
	tintHunk     = 0.10
	tintGhost    = 0.06
	tintFlash    = 0.35
	tintCursor   = 0.24
	tintHunkLine = 0.16
	tintGhostNr  = 0.05

	markGlyph    = "▎" // ▎ at the first cell of the row under the cursor
	wrapGlyph    = "↪" // ↪ in the sign column of a line's continuation rows
	markReviewed = "✓ reviewed"
	tagDigitsMin = 4 // the least hex digits of a hidden-character tag
	minNumW      = 3
)

// drawerLayout is how the drawer's inner box divides up.
type drawerLayout struct {
	box            core.Rect
	innerW, innerH int
	treeW, diffW   int  // treeW 0 hides the tree
	bodyH          int  // rows of the tree and diff columns
	headH          int  // rows at the top of the diff column that hold the pull request header
	diffH          int  // rows of the diff column under the header
	modeShort      bool // the mode row drops " in this worktree" for lack of room
	hints          bool // the last inner row holds the key hints
	split          bool // side by side fits the diff column
	gw             int  // width of one line number column
}

// layoutKey is what the layout depends on besides the result's generation.
type layoutKey struct {
	box        core.Rect
	tree       bool // the tree is open
	split      bool
	modeW      int // the mode row's width
	modeShortW int // and its width without " in this worktree"
	headH      int // rows of the pull request header the diff column starts with
	fullDigits int // digits of the longest file shown whole
	page       bool
}

// fullDigits is the digits of the highest line number among the files shown whole.
func fullDigits(v *reviewView) int {
	n := 0

	for path, ft := range v.full {
		if v.fullOn[path] && !v.mdOn[path] {
			n = max(n, numDigits(max(len(ft.old), len(ft.new))))
		}
	}

	return n
}

// drawerLayoutOf divides the box for the view; tree says the drawer's tree is
// open. Side by side is used only when the view wants it and 24 code columns
// fit on each side. The layout is kept until one of its inputs changes, as it
// is asked for on every update.
func (a *App) drawerLayoutOf(box core.Rect, v *reviewView, tree bool) drawerLayout {
	c := v.derived()
	full, short := modeWidth(v, a.icons, false), modeWidth(v, a.icons, true)
	page := v.page() != nil
	headH := 0

	// A page takes the whole column: no tree, no pull request header.
	if page {
		tree = false
	} else {
		headH = a.prHeaderH(v, max(box.H-2, 0))
	}

	key := layoutKey{box: box, tree: tree, split: v.split, modeW: full, modeShortW: short, headH: headH, fullDigits: fullDigits(v), page: page}

	if c.layOK && c.layKey == key {
		return c.lay
	}

	a.drawer.layouts++

	l := drawerLayout{box: box, innerW: max(box.W-2, 0), innerH: max(box.H-2, 0)}
	l.hints = l.innerH >= hintsMinInner
	l.bodyH = l.innerH

	if l.hints {
		l.bodyH--
	}

	l.headH, l.diffH = headH, l.bodyH-headH
	l.diffW = l.innerW

	if tree {
		tw := c.treeWidthAt(v.res.Files, l.innerW)
		limit := l.innerW * treeMaxShare / 100

		// The mode row is a row of the tree too, up to the same share of the
		// width. It drops its words about the worktree before it is cut.
		mw := full

		if full > max(tw, limit) {
			l.modeShort, mw = true, short
		}

		tw = max(tw, min(mw, limit))

		if l.innerW-tw-1 >= 1 {
			l.treeW, l.diffW = tw, l.innerW-tw-1
		}
	}

	l.gw = max(c.digits, key.fullDigits, minNumW) + 1
	l.split = v.split && l.splitCode() >= splitMinCode

	c.lay, c.layKey, c.layOK = l, key, true

	return l
}

// markW is the cell at the start of a row that holds the cursor mark.
const markW = 1

// unifiedCode is the code columns of a unified row, with n number columns.
func (l drawerLayout) unifiedCode(n int) int {
	return l.diffW - markW - n*l.gw - diffview.SignWidth
}

// codeWidths are the columns the rows wrap at, as the drawer draws them. The
// full view stays unified when side by side is on; the two sides are set only
// when split.
func (l drawerLayout) codeWidths() codeWidths {
	w := codeWidths{line: l.unifiedCode(2), full: l.unifiedCode(1)}
	if l.split {
		w.left, w.right = l.splitCode(), l.splitRightCode()
	}

	return w
}

// splitCode is the code columns of the left side of a side by side row, the
// narrower one.
func (l drawerLayout) splitCode() int {
	return (l.diffW - markW - 1 - 2*(l.gw+diffview.SignWidth)) / 2
}

// splitRightCode is the code columns of the right side of a side by side row.
func (l drawerLayout) splitRightCode() int {
	return l.diffW - markW - 1 - 2*(l.gw+diffview.SignWidth) - l.splitCode()
}

// splitSep is the column of a side by side row's "│".
func (l drawerLayout) splitSep() int {
	return markW + l.gw + diffview.SignWidth + l.splitCode()
}

// splitSide is the side of a side by side row that column col belongs to: the
// left up to and including the "│", else the right.
func (l drawerLayout) splitSide(col int) int {
	if col <= l.splitSep() {
		return 0
	}

	return 1
}

// splitCols are the columns of a side, without the "│".
func (l drawerLayout) splitCols(side int) (lo, hi int) {
	if side == 0 {
		return 0, l.splitSep() - 1
	}

	return l.splitSep() + 1, l.diffW - 1
}

// splitCodeAt is where a side's code starts and how many columns it has.
func (l drawerLayout) splitCodeAt(side int) (col, width int) {
	if side == 0 {
		return markW + l.gw + diffview.SignWidth, l.splitCode()
	}

	return l.splitSep() + 1 + l.gw + diffview.SignWidth, l.splitRightCode()
}

// drawCtx is what drawing one frame of the drawer needs.
type drawCtx struct {
	ui    theme.UI
	th    theme.Theme
	icons bool
	v     *reviewView
	c     *reviewCache
	lay   drawerLayout
	dv    *diffview.Theme
	flash *diffview.Theme
	keys  bool // the drawer has the keyboard
	tree  bool // and within it the tree has
	now   time.Time
	query string
	top   int

	mdp      *mdPainter // paints rendered rows; made on first use
	pageRows []string   // the shown page's rows, drawn on first use
	pagePics []picRow   // the picture rows of pageRows, relative to the page
	mdPics   []picRow   // the picture rows of the rendered rows drawn, relative to the diff column's body

	picGen uint64            // the picture registry's gen, which re-lays the page out
	covers []image.Rectangle // the boxes drawn over the drawer's body, relative to the drawer

	pr      core.PRInfo  // the pull request the header shows; zero when headH is 0
	pinned  bool         // the drawer is held on its root
	home    string       // shortens the paths it names
	list    *wtList      // the open worktree list, drawn over the drawer
	links   *linkChooser // the open link chooser, drawn over the drawer
	bind    keyTable     // the drawer's keys, as the hint row shows them
	pushKey string       // the leader sequence of git.push, "" when unbound
}

// diffKeys reports whether the diff, not the tree, has the keyboard.
func (d *drawCtx) diffKeys() bool { return d.keys && !d.tree }

func (d *drawCtx) glyph(icon string) string {
	if !d.icons {
		return ""
	}

	return icon
}

// diffThemes returns the diff view's theme and its flash twin for the look,
// building them once per theme.
func (a *App) diffThemes(lk look) (*diffview.Theme, *diffview.Theme) {
	if a.drawer.dv == nil || a.drawer.dvName != lk.theme.Name {
		a.drawer.dv, a.drawer.dvFlash = a.drawer.caches.diffThemes(lk)
		a.drawer.dvName = lk.theme.Name
	}

	return a.drawer.dv, a.drawer.dvFlash
}

// drawKey is every input the drawing reads, so drawerLines can keep its lines
// while the key stays the same. An input the drawing gains needs a field here.
type drawKey struct {
	w, h              int // the cells shown, which a slide narrows
	lay               drawerLayout
	theme             string
	ui                theme.UI
	icons, keys, tree bool

	rowsRev, spansRev    int
	rowsSplit            bool
	top, cursor, treeSel int
	query                string
	searching            bool
	fresh                string // the rows in view that flash, while a flash is on
	src                  git.Source
	project, branch      string
	base                 string
	turns                int
	resTurn              int
	noSnapshot           bool
	pinned               bool
	list                 wtKey
	links                linksKey
	err                  string
	hasErr               bool
	page                 *mdPage
	pageRev              int
	picGen               uint64
	sel                  selection // the shown page's or the rows' selection, when hasSel
	hasSel               bool

	pr       core.PRInfo
	unpushed int
	age      string
	head     string // the loaded diff's head, which the header compares with the poll's
}

// key is the drawKey of d drawn into r, for the theme called name.
func (d *drawCtx) key(r core.Rect, name string) drawKey {
	v := d.v
	k := drawKey{
		w: r.W, h: r.H, lay: d.lay, theme: name, ui: d.ui,
		icons: d.icons, keys: d.keys, tree: d.tree,
		rowsRev: v.rowsRev, spansRev: d.c.spansRev, rowsSplit: v.rowsSplit,
		top: d.top, cursor: v.cursor, treeSel: v.treeSel,
		query: d.query, searching: v.search != nil,
		src: v.src, project: v.project, branch: v.branch, base: v.base,
		turns: v.turns, resTurn: v.res.Turn, noSnapshot: v.noSnapshot, pinned: d.pinned,
		list: d.listKey(), links: d.linksKey(),
		pr: d.pr, unpushed: v.unpushed, head: v.res.Head,
	}

	if d.lay.headH > 0 {
		k.age = railAge(d.pr.UpdatedAt, d.now)
	}

	if v.err != nil {
		k.err, k.hasErr = v.err.Error(), true
	}

	if p := v.page(); p != nil {
		k.page, k.pageRev, k.picGen = p, p.rev, d.picGen

		if p.view.sel != nil {
			k.sel, k.hasSel = *p.view.sel, true
		}
	} else {
		if len(v.mdFiles) > 0 {
			k.picGen = d.picGen
		}

		if v.sel != nil {
			k.sel, k.hasSel = *v.sel, true
		}
	}

	if len(v.fresh) > 0 && d.now.Before(v.flashUntil) {
		k.fresh = d.freshInView()
	}

	return k
}

// freshInView names the rows in view that flash; a string keeps the key comparable.
func (d *drawCtx) freshInView() string {
	var rows []byte

	for i := d.top; i < min(d.top+d.lay.diffH, len(d.v.rows)); i++ {
		if d.v.fresh[i] {
			rows = binary.AppendVarint(rows, int64(i))
		}
	}

	return string(rows)
}

// drawerLines draws the drawer for r, the rect drawerRect returned: r.H lines
// of r.W cells. While it slides, r is narrower than the drawer and shows its
// left part. The lines are drawn again only when a drawKey field changed; they
// are shared with the cache, so callers must not change them. View uses
// drawerDrawn; this is the seam the drawing tests call.
func (a *App) drawerLines(st core.State, r core.Rect) []string {
	lines, _, _ := a.drawerDrawn(st, r)

	return lines
}

// drawerDrawn is drawerLines, and whether every line is exactly r.W cells, ends
// with no style or link open and carries nothing across cells, as splicing the
// lines into a frame needs. pics are the picture rows the lines show, relative
// to r: none while the drawer slides, and none a box over the page covers.
func (a *App) drawerDrawn(st core.State, r core.Rect) (lines []string, pics []picRow, fits bool) {
	d, name, ok := a.drawCtxFor(st)
	if !ok || r.W <= 0 || r.H <= 0 {
		return nil, nil, false
	}

	key := d.key(r, name)
	if d.c.drawnOK && d.c.drawnKey == key {
		return d.c.drawn, a.slidPics(d.c.drawnPics), d.c.drawnFit
	}

	a.drawer.renders++

	lines = d.lines(r)
	fits = !slices.ContainsFunc(lines, func(l string) bool { return ansi.StringWidth(l) != r.W || !endsClean(l) || carriesAcrossCells(l) })
	pics = d.pictureRows(r)
	d.c.drawn, d.c.drawnPics, d.c.drawnFit, d.c.drawnKey, d.c.drawnOK = lines, pics, fits, key, true

	return lines, a.slidPics(pics), fits
}

// slidPics is pics, or none while the drawer slides.
func (a *App) slidPics(pics []picRow) []picRow {
	if !a.drawer.slideFrom.IsZero() {
		return nil
	}

	return pics
}

// pictureRows are the page's picture rows in the drawer's cells, less any picture a
// box drawn over the body overlaps. The page fills the diff column from the
// first body row.
func (d *drawCtx) pictureRows(r core.Rect) []picRow {
	pics := d.pagePics // only one of the page and the rendered rows draws pictures
	if len(pics) == 0 {
		pics = d.mdPics
	}

	if len(pics) == 0 {
		return nil
	}

	x := 1
	if d.lay.treeW > 0 {
		x += d.lay.treeW + 1
	}

	var rows []picRow

	for _, p := range pics {
		if p.Y >= d.lay.bodyH || x+p.X+p.Cols > r.W {
			continue
		}

		p.X += x
		p.Y++
		rows = append(rows, p)
	}

	return dropCovered(rows, d.covers)
}

// dropCovered is rows without the pictures a box overlaps.
func dropCovered(rows []picRow, boxes []image.Rectangle) []picRow {
	if len(boxes) == 0 {
		return rows
	}

	var out []picRow

	for _, id := range picIDs(rows) {
		var group []picRow

		for _, r := range rows {
			if r.ID == id {
				group = append(group, r)
			}
		}

		rect := picRect(group)
		if !slices.ContainsFunc(boxes, rect.Overlaps) {
			out = append(out, group...)
		}
	}

	return out
}

// drawCtxFor is what drawing the active tab's drawer needs, and the name of the
// theme it draws in; ok is false with no drawer to draw.
func (a *App) drawCtxFor(st core.State) (d *drawCtx, name string, ok bool) {
	box, ok := a.drawerBox(st)
	if !ok {
		return nil, "", false
	}

	lk := a.current()
	v := a.drawer.views[st.Tab().ID]

	if v == nil {
		v = &reviewView{}
	}

	tab := st.Tab()

	d = &drawCtx{
		ui: lk.theme.UI, th: lk.theme, icons: a.icons, v: v, c: v.derived(), now: a.clock.Now(), keys: a.drawer.focused, tree: a.drawer.focused && a.drawer.tree,
		pinned: tab.Review != nil && pinnedTo(tab, tab.Review.Pane), home: a.getenv("HOME"), list: a.listFor(tab), links: a.linksFor(tab), bind: a.keys.drawer,
	}
	d.pushKey, _ = a.keymap.keyFor("git.push")
	d.lay = a.drawerLayoutOf(box, v, drawerTree(st))

	if d.lay.headH > 0 {
		d.pr, _ = a.prHeaderInfo(v)
	}

	d.dv, d.flash = a.diffThemes(lk)
	d.top = v.viewTop(d.lay.diffH)
	d.picGen = a.pictures.gen()

	switch {
	case v.search != nil:
		d.query = *v.search
	default:
		d.query = v.last
	}

	return d, lk.theme.Name, true
}

// lines draws the box into r, r.H lines of r.W cells.
func (d *drawCtx) lines(r core.Rect) []string {
	lines := d.render()
	for i, l := range lines {
		lines[i] = ansi.Truncate(l, r.W, "")
	}

	return lines[:min(len(lines), r.H)]
}

func colorHex(c color.Color) theme.Hex {
	r, g, b, _ := c.RGBA()

	return theme.Hex(fmt.Sprintf("#%02x%02x%02x", r>>8, g>>8, b>>8))
}

func mixHex(a, b theme.Hex, t float64) theme.Hex { return colorHex(diffview.MixColors(a, b, t)) }

func (d *drawCtx) edge() theme.Hex {
	if d.keys {
		return d.ui.Review
	}

	return d.ui.Border
}

// render draws the whole box: exactly box.H lines of box.W cells.
func (d *drawCtx) render() []string {
	box, lay := d.lay.box, d.lay
	blank := railPainter{bg: d.ui.Bg}.line(nil, box.W)

	if box.W < 2 || box.H < 2 {
		return slices.Repeat([]string{blank}, max(box.H, 0))
	}

	lines := make([]string, 0, box.H)
	lines = append(lines, d.topBorder())

	side := paint(d.edge(), d.ui.Bg, false).Render("│")
	rule := paint(d.ui.Border, d.ui.Bg, false).Render("│")

	for y := range lay.bodyH {
		row := side
		if lay.treeW > 0 {
			row += d.treeRow(y) + rule
		}

		lines = append(lines, row+d.diffColumn(y)+side)
	}

	if lay.hints {
		hints := append([]railRun{{text: " "}}, d.hintRuns(lay.innerW-1)...)
		lines = append(lines, side+railPainter{bg: d.ui.Bg}.line(hints, lay.innerW)+side)
	}

	lines = append(lines, d.bottomBorder())
	d.overlayList(lines)
	d.overlayLinks(lines)

	return lines
}

// borderLine is a border row: the corner and a dash, the left runs, dashes,
// the right runs, a dash and the other corner. What does not fit is cut.
func (d *drawCtx) borderLine(left, right string, lruns, rruns []railRun) string {
	w := d.lay.box.W
	fg := d.edge()
	edge := func(s string) railRun { return railRun{text: s, fg: fg} }

	runs := append([]railRun{edge(left + "─")}, lruns...)
	fill := w - runsWidth(runs) - runsWidth(rruns) - 2

	if fill < 0 {
		rruns, fill = nil, w-runsWidth(runs)-2
	}

	runs = append(runs, edge(strings.Repeat("─", max(fill, 0))))
	runs = append(runs, rruns...)
	runs = append(runs, edge("─"+right))

	return railPainter{bg: d.ui.Bg}.line(runs, w)
}

// topBorder holds the title on the left and the tags on the right.
func (d *drawCtx) topBorder() string {
	ui, v := d.ui, d.v
	fg, bold := ui.Muted, false

	if d.keys {
		fg, bold = ui.Review, true
	}

	// Tags sit on the right, the mode tag last. When the title would be left no
	// room, the zoom tag drops first, then split, then pinned, then the counter.
	zoom, split, pin := v.zoom, d.lay.split && v.rowsSplit, d.pinned
	count, haveCount := d.counterTag()
	tags := d.tagRuns(zoom, split, pin, count, haveCount)

	// The corner and dash take two cells on each side, and a dash parts the title from the tags.
	room := func() int { return d.lay.box.W - 4 - runsWidth(tags) - 1 }

	if room() < titleRoomMin+2 && zoom {
		tags = d.tagRuns(false, split, pin, count, haveCount)
	}

	if room() < titleRoomMin+2 && split {
		tags = d.tagRuns(false, false, pin, count, haveCount)
	}

	if room() < titleRoomMin+2 && pin {
		tags = d.tagRuns(false, false, false, count, haveCount)
	}

	if room() < titleRoomMin+2 && haveCount {
		tags = d.tagRuns(false, false, false, railRun{}, false)
	}

	var title []railRun

	if text := d.titleText(); text != "" && room() >= titleRoomMin+2 {
		text = ansi.Truncate(text, room()-2-ansi.StringWidth(d.glyph(iconReview+" ")), "…")

		title = append(title, railRun{text: " "})

		if d.icons {
			title = append(title, railRun{text: iconReview + " ", fg: ui.Review, bold: true})
		}

		title = append(title, railRun{text: text, fg: fg, bold: bold}, railRun{text: " "})
	}

	return d.borderLine("╭", "╮", title, tags)
}

// counterTag is the reviewed counter, ok false where marks do not count: with
// no hunks, over a markdown page and on a rendered file.
func (d *drawCtx) counterTag() (railRun, bool) {
	v := d.v
	if v.page() != nil {
		return railRun{}, false
	}

	if cur := d.cursorFile(); cur != nil && v.mdOn[cur.Path] {
		return railRun{}, false
	}

	read, all := v.countHunks(v.hunkRead)
	if all == 0 {
		return railRun{}, false
	}

	fg := d.ui.Accent
	if read == all {
		fg = d.ui.Success
	}

	return railRun{text: " ✓ " + strconv.Itoa(read) + "/" + strconv.Itoa(all) + " ", fg: fg, bold: true}, true
}

// tagRuns are the tags on the right of the top border, left to right: zoom,
// split, pinned, the counter, then the mode tag, each followed by a dash but
// the last.
func (d *drawCtx) tagRuns(zoom, split, pinned bool, counter railRun, haveCounter bool) []railRun {
	ui := d.ui
	tag := func(text string) railRun { return railRun{text: " " + text + " ", fg: ui.Accent, bold: true} }
	dash := railRun{text: "─", fg: d.edge()}

	var tags []railRun

	if zoom {
		tags = append(tags, tag(d.withIcon(iconZoom, "zoom")), dash)
	}

	if split {
		tags = append(tags, tag(d.withIcon(iconSplit, "split")), dash)
	}

	if pinned {
		tags = append(tags, tag(d.withIcon(iconPin, "pinned")), dash)
	}

	if haveCounter {
		tags = append(tags, counter, dash)
	}

	return append(tags, tag(d.modeTag()))
}

func (d *drawCtx) withIcon(icon, text string) string {
	if !d.icons {
		return text
	}

	return icon + " " + text
}

// titleText is "project · branch", or just "Review" before the view loaded.
func (d *drawCtx) titleText() string {
	v := d.v
	text, _ := cleanText(v.project)

	switch {
	case text == "":
		return "Review"
	case v.branch != "":
		b, _ := cleanText(v.branch)

		return text + " · " + b
	}

	return text
}

// shownTurn is the turn the drawer shows.
func (v *reviewView) shownTurn() int {
	if v.res.Turn > 0 {
		return v.res.Turn
	}

	if ts, ok := v.src.(git.TurnSource); ok && ts.Turn > 0 {
		return ts.Turn
	}

	return v.turns
}

// showsTurn reports whether the drawer shows a turn. A turn-mode drawer with no
// session or no turns yet shows the branch while core keeps turn mode.
func (v *reviewView) showsTurn() bool {
	_, ok := v.src.(git.TurnSource)

	return ok
}

// modeTag is the text of the mode tag.
func (d *drawCtx) modeTag() string {
	if p := d.v.page(); p != nil {
		return d.withIcon(iconMarkdown, printable(p.rel))
	}

	if src, ok := d.v.src.(git.PRSource); ok {
		return d.withIcon(iconPR, prNumberText(src.Number))
	}

	if d.v.showsTurn() {
		return d.withIcon(iconTurn, "turn "+strconv.Itoa(d.v.shownTurn())+"/"+strconv.Itoa(d.v.turns))
	}

	return d.withIcon(iconBranchMode, "branch")
}

// bottomBorder holds the search prompt while it is open.
func (d *drawCtx) bottomBorder() string {
	if d.v.search == nil {
		return d.borderLine("╰", "╯", nil, nil)
	}

	// Corner, dash, dash and corner take four cells; the prompt's blank, mark,
	// caret and blank take four more.
	room := d.lay.box.W - 8
	if room < 0 {
		return d.borderLine("╰", "╯", nil, nil)
	}

	q, _ := cleanText(*d.v.search)
	if w := ansi.StringWidth(q); w > room {
		q = ansi.TruncateLeft(q, w-room, "")
	}

	runs := []railRun{
		{text: " "},
		{text: "/", fg: d.ui.Info, bold: true},
		{text: q, fg: d.ui.Fg},
		{text: "▏", fg: d.ui.Fg},
		{text: " "},
	}

	return d.borderLine("╰", "╯", runs, nil)
}

// hintRuns are the key hints that fit cw cells; pairs that do not are dropped
// from the end.
func (d *drawCtx) hintRuns(cw int) []railRun {
	pair := func(label string, ids ...string) [2]string { return [2]string{d.bind.hint(ids...), label} }
	next, prev := "review.line.next", "review.line.prev"

	if d.v.page() != nil {
		return d.fitHints([][2]string{
			pair("line", next, prev), pair("heading", "review.hunk.next", "review.hunk.prev"),
			pair("zoom", "review.zoom"), pair("open", "review.fold"), pair("search", "review.search"), pair("back", "review.back"),
		}, cw)
	}

	cur := d.cursorFile()
	render := cur != nil && md.IsMarkdown(cur.Path) && !cur.Binary && cur.Status != git.Deleted

	pairs := [][2]string{
		pair("line", next, prev),
		pair("hunk", "review.hunk.next", "review.hunk.prev"),
		pair("file", "review.file.next", "review.file.prev"),
		pair("zoom", "review.zoom"),
		pair("tree", "review.tree"),
		pair("worktree", "review.worktrees"),
	}

	if render {
		pairs = append(pairs, pair("render", "review.markdown"))
	}

	pairs = append(pairs,
		pair("full", "review.full"),
		pair("split", "review.split"),
		pair("edit", "review.edit"), pair("search", "review.search"),
	)

	// space marks hunks, so it does nothing in a file that does not show them.
	if cur != nil && d.v.hunksShown(*cur) {
		pairs = append(pairs, pair("reviewed", "review.mark"), d.showReadPair(pair))
	}

	pairs = append(pairs, pair("mode", "review.mode.branch", "review.mode.turn"))

	if d.v.showsTurn() {
		pairs = append(pairs, pair("turn", "review.turn.prev", "review.turn.next"))
	}

	if _, ok := d.v.src.(git.PRSource); ok {
		// Only a draft can be marked ready; fitHints drops the empty pair.
		var ready [2]string
		if d.pr.Draft {
			ready = pair("ready", "review.ready")
		}

		pairs = [][2]string{
			pair("line", next, prev), pair("hunk", "review.hunk.next", "review.hunk.prev"),
			pair("zoom", "review.zoom"), pair("conversation", "review.conversation"),
			pair("checks", "review.checks"), ready, pair("approve", "review.approve"), pair("merge", "review.merge"),
			pair("browser", "review.browser"),
			pair("reviewed", "review.mark"), d.showReadPair(pair),
			pair("mode", "review.mode.branch", "review.mode.turn"),
		}
	}

	if cur != nil && d.v.mdOn[cur.Path] {
		pairs = [][2]string{
			pair("line", next, prev), pair("change", "review.hunk.next", "review.hunk.prev"),
			pair("zoom", "review.zoom"), pair("open", "review.fold"),
			pair("source", "review.markdown"), pair("full", "review.full"), pair("search", "review.search"),
		}
	}

	if d.tree {
		pairs = [][2]string{pair("file", next, prev), pair("jump", "review.fold"), pair("zoom", "review.zoom"), pair("hide", "review.tree"), pair("back", "review.back")}
	}

	return d.fitHints(pairs, cw)
}

// showReadPair is the hint for the switch that shows reviewed hunks.
func (d *drawCtx) showReadPair(pair func(string, ...string) [2]string) [2]string {
	if d.v.showRead {
		return pair("unread only", "review.show.reviewed")
	}

	return pair("show reviewed", "review.show.reviewed")
}

// fitHints draws the key and label pairs that fit cw cells; a pair with no
// key, its command unbound, is left out.
func (d *drawCtx) fitHints(pairs [][2]string, cw int) []railRun {
	pairs = slices.DeleteFunc(pairs, func(kv [2]string) bool { return kv[0] == "" })

	var out []railRun

	used := 0

	for i, kv := range pairs {
		need := ansi.StringWidth(kv[0]) + 1 + ansi.StringWidth(kv[1])
		if i > 0 {
			need += 3
		}

		if used+need > cw {
			break
		}

		if i > 0 {
			out = append(out, railRun{text: "   ", fg: d.ui.Muted})
		}

		out = append(out, railRun{text: kv[0], fg: d.ui.Key, bold: true}, railRun{text: " " + kv[1], fg: d.ui.Muted})
		used += need
	}

	return out
}

// cursorFile is the file the cursor is in, nil with no rows.
func (d *drawCtx) cursorFile() *git.FileDiff {
	v := d.v
	if v.cursor < 0 || v.cursor >= len(v.rows) {
		return nil
	}

	return &v.res.Files[v.rows[v.cursor].file]
}

// ---- diff column ----

// stickyHeader is the row of the header to draw on the first row instead of
// the top one: the top row's file header, when it has scrolled off.
func (d *drawCtx) stickyHeader() (int, bool) {
	rows, top := d.v.rows, d.top
	if top <= 0 || top >= len(rows) || rows[top].kind == rowFile {
		return 0, false
	}

	if c := d.v.cursor; c >= 0 && c < len(rows) && d.v.lineStart(top) == d.v.lineStart(c) {
		return 0, false
	}

	if top+1 < len(rows) && rows[top+1].kind == rowFile {
		return 0, false
	}

	for i := top - 1; i >= 0; i-- {
		if rows[i].kind == rowFile && rows[i].file == rows[top].file {
			return i, true
		}
	}

	return 0, false
}

// diffRow draws row y of the diff column.
func (d *drawCtx) diffRow(y int) string {
	w := d.lay.diffW
	p := railPainter{bg: d.ui.Bg}

	if len(d.v.rows) == 0 {
		return p.line(d.emptyRuns(y), w)
	}

	i := d.top + y

	if y == 0 {
		if h, ok := d.stickyHeader(); ok {
			i = h
		}
	}

	if i >= len(d.v.rows) {
		return p.line(nil, w)
	}

	cursor := d.onCursor(i)
	row := d.exact(d.rowCells(i, cursor), w)
	d.notePicture(i, d.lay.headH+y, cursor)

	if spans := d.selSpans(i, w); len(spans) > 0 {
		row = paintSpans(row, w, spans)
	}

	return row
}

// onCursor reports whether row i is a row of the cursor's line while the diff
// has the keyboard.
func (d *drawCtx) onCursor(i int) bool {
	v := d.v

	if !d.diffKeys() || v.cursor < 0 || v.cursor >= len(v.rows) {
		return false
	}

	return i >= v.lineStart(v.cursor) && i < v.lineEnd(v.cursor)
}

// exact makes s w cells: cut, or padded on the ground.
func (d *drawCtx) exact(s string, w int) string {
	got := ansi.StringWidth(s)

	switch {
	case got > w:
		return ansi.Truncate(s, w, "")
	case got < w:
		return s + railPainter{bg: d.ui.Bg}.line(nil, w-got)
	}

	return s
}

// emptyRuns are the info rows of a result with no files, at row y.
func (d *drawCtx) emptyRuns(y int) []railRun {
	v := d.v

	var lines []string

	switch src := v.src.(type) {
	case git.BranchSource:
		base := v.base
		if base == "" {
			base = src.Base
		}

		if base == "" {
			base = "the default branch"
		}

		lines = []string{"no changes against " + base}
	case git.PRSource:
		lines = []string{"no changes against " + prBaseText(v)}
	case git.TurnSource:
		n := strconv.Itoa(v.shownTurn())
		if v.noSnapshot {
			lines = append([]string{"turn " + n + " has no snapshot"}, d.noSnapshotHint()...)
		} else {
			lines = []string{"turn " + n + " changed nothing"}
		}
	}

	fg := d.ui.Subtle

	if v.err != nil {
		lines = []string{v.err.Error()}

		if gone, ok := errors.AsType[errWorktreeRemoved](v.err); ok {
			lines, fg = []string{noticeRemoved + shortPath(gone.root, "", d.home)}, d.ui.Muted
		}
	}

	if y >= len(lines) {
		return nil
	}

	text, _ := cleanText(lines[y])

	return []railRun{{text: "    "}, {text: text, fg: fg}}
}

// infoText is an info row's text with the key it names taken from the table,
// and the clause dropped when the command has no key.
func (d *drawCtx) infoText(r reviewRow) string {
	switch r.text {
	case infoTruncated:
		if key, ok := d.bind.first("review.full"); ok {
			return "too large, press " + key + " for the full file"
		}

		return "too large"
	case infoAllRead:
		key, bound := d.bind.first("review.show.reviewed")

		switch {
		case r.block == 1 && bound:
			return "the one hunk is reviewed; " + key + " shows it"
		case r.block == 1:
			return "the one hunk is reviewed"
		case bound:
			return "all " + strconv.Itoa(r.block) + " hunks reviewed; " + key + " shows them"
		}

		return "all " + strconv.Itoa(r.block) + " hunks reviewed"
	}

	return r.text
}

// noSnapshotHint is the second line of an empty turn without a snapshot: the
// bound keys that step through the turns and show the branch.
func (d *drawCtx) noSnapshotHint() []string {
	var parts []string

	if prev, ok := d.bind.first("review.turn.prev"); ok {
		if next, ok := d.bind.first("review.turn.next"); ok {
			parts = append(parts, prev+" and "+next+" step through the turns")
		}
	}

	if key, ok := d.bind.first("review.mode.branch"); ok {
		parts = append(parts, key+" shows the branch")
	}

	if len(parts) == 0 {
		return nil
	}

	return []string{strings.Join(parts, "; ")}
}

// markRun is the first cell of a row: the cursor mark or a blank.
func (d *drawCtx) markRun(cursor bool) railRun {
	if cursor {
		return railRun{text: markGlyph, fg: d.ui.Accent}
	}

	return railRun{text: " "}
}

// rowCells draws row i, a row of the diff, as cells of any width; the caller
// fits them.
func (d *drawCtx) rowCells(i int, cursor bool) string {
	r, v, w := d.v.rows[i], d.v, d.lay.diffW
	ui := d.ui
	f := v.res.Files[r.file]
	fresh := v.fresh[i] && d.now.Before(v.flashUntil)

	switch r.kind {
	case rowFile:
		return d.fileRow(r.file, r.read, cursor)
	case rowInfo:
		bg := d.cursorGround(ui.Bg, cursor)
		text, _ := cleanText(d.infoText(r))

		return railPainter{bg: bg}.line([]railRun{d.markRun(cursor), {text: "   "}, {text: text, fg: ui.Subtle}}, w)
	case rowHunk:
		h := f.Hunks[r.hunk]
		bg := d.cursorGround(mixHex(ui.Bg, ui.Info, tintHunk), cursor)
		section, _ := cleanText(h.Section)

		runs := []railRun{d.markRun(cursor), {text: " " + hunkHeader(h), fg: ui.Info}}
		if section != "" {
			runs = append(runs, railRun{text: " " + section, fg: ui.Muted})
		}

		if r.read {
			gap := max(w-runsWidth(runs)-ansi.StringWidth(markReviewed)-1, 1)
			runs = append(runs, railRun{text: strings.Repeat(" ", gap)}, railRun{text: markReviewed, fg: ui.Success})
		}

		return railPainter{bg: bg}.line(runs, w)
	case rowLine:
		return d.unifiedRow(r, f, cursor, fresh)
	case rowPair:
		return d.pairRow(r, f, cursor, fresh)
	case rowFull:
		return d.fullRow(r, cursor, fresh)
	case rowGhost:
		return d.ghostRow(r, cursor, fresh)
	case rowFold:
		return d.foldRow(r, cursor, fresh)
	case rowMD:
		return d.mdRow(r, f, cursor)
	}

	return ""
}

// cursorGround is bg, with the accent mixed in under the cursor.
func (d *drawCtx) cursorGround(bg theme.Hex, cursor bool) theme.Hex {
	if cursor {
		return mixHex(bg, d.ui.Accent, tintCursor)
	}

	return bg
}

// fileHeadW is the cells before a file header's path: the mark, a blank, the
// status letter and a blank. hunkHeadW is the mark and a blank of a hunk header.
const (
	fileHeadW = 4
	hunkHeadW = 2
)

// fileRight is the flags and counts at the end of a file's header.
func (d *drawCtx) fileRight(fi int, read bool) []railRun {
	f := d.v.res.Files[fi]
	ui, w := d.ui, d.lay.diffW

	var right []railRun

	if fi < len(d.c.hidden) && d.c.hidden[fi] {
		text := d.hiddenFlag()
		if w >= 90 {
			text += " hidden characters"
		}

		right = append(right, railRun{text: text, fg: ui.Warning, bold: true}, railRun{text: "  "})
	}

	_, have := d.v.full[f.Path]

	switch {
	case d.v.mdOn[f.Path]:
		right = append(right, railRun{text: d.withIcon(iconMarkdown, "rendered"), fg: ui.Accent, bold: true}, railRun{text: "  "})
	case have && d.v.fullOn[f.Path]:
		right = append(right, railRun{text: d.withIcon(iconFull, "full"), fg: ui.Accent, bold: true}, railRun{text: "  "})
	}

	switch {
	case read:
		right = append(right, railRun{text: markReviewed, fg: ui.Success})
	case f.Binary:
		right = append(right, railRun{text: "bin", fg: ui.Subtle})
	default:
		right = append(right, railRun{text: "+" + strconv.Itoa(f.Added), fg: ui.Success}, railRun{text: " -" + strconv.Itoa(f.Removed), fg: ui.Error})
	}

	return append(right, railRun{text: " "})
}

// filePath is a file header's path, "old → new" for a rename, and as much of
// it as the row has room for beside the flags in right.
func (d *drawCtx) filePath(f git.FileDiff, right []railRun) (text, shown string) {
	text, _ = cleanText(f.Path)
	if f.OldPath != "" {
		old, _ := cleanText(f.OldPath)
		text = old + " → " + text
	}

	// Two cells part the path from the flags.
	room := max(d.lay.diffW-fileHeadW-runsWidth(right)-2, 0)

	if pw := ansi.StringWidth(text); pw > room {
		return text, ansi.TruncateLeft(text, pw-room+1, "…")
	}

	return text, text
}

// fileRow is a file's header: status, path, flags and counts.
func (d *drawCtx) fileRow(fi int, read, cursor bool) string {
	f := d.v.res.Files[fi]
	ui, w := d.ui, d.lay.diffW
	bg := d.cursorGround(ui.Surface, cursor)
	letter, letterFg := d.statusMark(f)
	right := d.fileRight(fi, read)
	_, path := d.filePath(f, right)

	runs := []railRun{d.markRun(cursor), {text: " "}, {text: letter, fg: letterFg, bold: true}, {text: " "}}
	runs = append(runs, d.matchRuns(path, ui.Fg, true)...)
	runs = append(runs, railRun{text: strings.Repeat(" ", max(w-fileHeadW-ansi.StringWidth(path)-runsWidth(right), 1))})

	return railPainter{bg: bg}.line(append(runs, right...), w)
}

// matchRuns splits text into runs, the search matches in Match and bold.
func (d *drawCtx) matchRuns(text string, fg theme.Hex, bold bool) []railRun {
	var runs []railRun

	pos := 0

	for _, m := range matchRanges(text, d.query) {
		if m.Start > pos {
			runs = append(runs, railRun{text: text[pos:m.Start], fg: fg, bold: bold})
		}

		runs = append(runs, railRun{text: text[m.Start:m.End], fg: d.ui.Match, bold: true})
		pos = m.End
	}

	return append(runs, railRun{text: text[pos:], fg: fg, bold: bold})
}

// matchRanges are the places q occurs in text, ignoring case. It finds none
// when lower-casing changes the length of text, which a byte range cannot follow.
func matchRanges(text, q string) []diffview.Range {
	if q == "" {
		return nil
	}

	lt, lq := strings.ToLower(text), strings.ToLower(q)
	if len(lt) != len(text) || lq == "" {
		return nil
	}

	var out []diffview.Range

	for at := 0; ; {
		i := strings.Index(lt[at:], lq)
		if i < 0 {
			return out
		}

		out = append(out, diffview.Range{Start: at + i, End: at + i + len(lq)})
		at += i + len(lq)
	}
}

// mdRow draws a line of a rendered file: the change mark or the cursor in cell
// 0, a blank, the text at the column's width less four, two blanks. A row whose
// doc is gone draws blank.
func (d *drawCtx) mdRow(r reviewRow, f git.FileDiff, cursor bool) string {
	w, ui := d.lay.diffW, d.ui
	bg := d.cursorGround(ui.Bg, cursor)
	lead, tw := min(2, w), max(w-4, 0)

	mark := railRun{text: " "}

	switch {
	case cursor:
		mark = d.markRun(true)
	case r.mark == markAdded:
		mark = railRun{text: markGlyph, fg: ui.Success}
	case r.mark == markRemoved:
		mark = railRun{text: markGlyph, fg: ui.Error}
	}

	text := railPainter{bg: bg}.line(nil, tw)

	if mf := d.v.mdFiles[f.Path]; mf != nil {
		lay := mf.doc.layout(max(d.v.mdW, 1), d.v.mdIcons)
		if r.line < len(lay.Lines) {
			text = d.mdPainter().withGround(bg).line(lay.Lines[r.line], lay.Links, tw)
		}
	}

	return railPainter{bg: bg}.line([]railRun{mark}, lead) + text + railPainter{bg: bg}.line(nil, max(w-lead-tw, 0))
}

// notePicture records the picture row that row i of the diff is, drawn at row y
// of the body, if it is one.
func (d *drawCtx) notePicture(i, y int, cursor bool) {
	r := d.v.rows[i]
	if r.kind != rowMD {
		return
	}

	mf := d.v.mdFiles[d.v.res.Files[r.file].Path]
	if mf == nil {
		return
	}

	lay := mf.doc.layout(max(d.v.mdW, 1), d.v.mdIcons)
	if r.line >= len(lay.Lines) {
		return
	}

	if im, row, ok := pictureAt(lay, r.line); ok {
		d.mdPics = append(d.mdPics, picRow{X: 2 + pictureCol(lay.Lines[r.line]), Y: y, ID: im.ID, Row: row, Cols: im.Cols, Ground: d.cursorGround(d.ui.Bg, cursor)})
	}
}

func (d *drawCtx) mdPainter() mdPainter {
	if d.mdp == nil {
		p := newMDPainter(d.th, d.ui.Bg)
		d.mdp = &p
	}

	return *d.mdp
}

// pageRow is row y of the shown markdown page, which fills the diff column.
func (d *drawCtx) pageRow(p *mdPage, y int) string {
	if d.pageRows == nil {
		d.pageRows, d.pagePics = p.view.drawPictures(d.mdPainter(), d.diffKeys())
	}

	if y >= len(d.pageRows) {
		return railPainter{bg: d.ui.Bg}.line(nil, d.lay.diffW)
	}

	return d.exact(d.pageRows[y], d.lay.diffW)
}
