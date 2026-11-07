package ui

import (
	"context"
	"math"
	"slices"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/mhersson/aim/internal/core"
	"github.com/mhersson/aim/internal/git"
	"github.com/mhersson/aim/internal/ui/diffview"
)

const (
	// slideFor is how long the drawer takes to slide in or out.
	slideFor = 120 * time.Millisecond
	// drawerMinW is the narrowest the drawer is, unless the body is narrower.
	drawerMinW = 60
	// splitMinCode is the least code columns each side of a side by side row needs.
	splitMinCode = 24
	// hintsMinInner is the inner height from which the key hints show.
	hintsMinInner = 12
)

// reviewView is one tab's drawer content: the loaded diff, its rows and where
// the cursor is. Only the update loop touches it.
type reviewView struct {
	pane     core.PaneID     // the pane reviewed
	zoom     bool            // the drawer covers the whole body
	mode     core.ReviewMode // the mode the view follows, as core holds it
	pr       core.ReviewPR   // the pull request the view shows, as core holds it; zero for none
	prBase   string          // the shown pull request's base branch
	prBranch string          // its head branch, for a copy, whose own HEAD means nothing
	unpushed int             // commits the pull request's worktree has that its remote lacks, read with the place
	then     prThen          // run once the pull request's diff has loaded
	pollThen prThen          // a copy's approve or merge held until its repository's poll answers
	src      git.Source
	root     string
	session  string         // the reviewed agent's session; "" for none
	own      string         // the pane's own directory (WorkDir) the last load ran for
	turnsFor string         // the session turns counts; another one until a probe for session answers
	after    func() tea.Cmd // the turn key waiting for the count
	loaded   bool           // an answer, a result or an error, has been applied
	loading  bool           // a load is in flight
	epoch    int            // names the source in force; every answer carries the one it was asked for
	cancel   context.CancelFunc
	queued   *git.Batch                     // changes seen since the last load started
	hiddenIn []bool                         // per file of res: handed over with it, so derived scans no text
	carry    map[chunkSum][][]diffview.Span // the last generation's spans by chunk text, for the next to take
	hlTop    int                            // the top of the last highlighting scan
	hlUp     bool                           // the top of the last scan is above the one before

	// The files the operator wants shown whole, the blobs res names for them
	// that full holds, and the blobs being read.
	fullOn   map[string]bool
	fullHave map[string]blobIDs
	fullAsk  map[string]fullRead
	fullStop map[string]context.CancelFunc // ends the read of a file's text

	// The files shown rendered, by path: the parsed file once its parse has
	// answered, the parses being made and the width and icons the rows were
	// laid out at.
	mdOn    map[string]bool
	mdFiles map[string]*mdFile
	mdAsk   map[string]blobIDs
	mdW     int
	mdIcons bool

	// The markdown pages shown over the diff column, the last one on top, and
	// the serials of the asks to read one (a new page, and a page read again)
	// whose answers are still wanted. UI state, not saved.
	pages        []*mdPage
	pageSeq      int
	reloadSeq    int
	reloadNotice bool // a failed re-read of the shown page says why

	// The reviewed hunks: the repository's common dir (for the root commonRoot),
	// the id of every hunk of res, and the marks and their Rev the rows were
	// built from. showRead and readOpen are UI state, kept across reloads.
	common, commonRoot string
	ids                [][]string
	marks              map[string]string
	marksRev           uint64
	showRead           bool
	readOpen           map[string]bool

	res         git.Result
	gen         int // bumps whenever res is replaced; the caches follow it
	rows        []reviewRow
	cursor, top int
	// sel is the text selection over rows, in the diff column's cells, and
	// selLay the layout it was made in.
	sel        *selection
	selLay     drawerLayout
	selSide    int  // the side of side by side rows the selection is on
	rowTexts   int  // rows' copy text read; tests read it
	split      bool // side by side is wanted; rowsSplit says whether the rows are laid out so
	full       map[string]fullText
	folded     map[string]bool
	flashUntil time.Time
	fresh      map[int]bool
	search     *string // the open search prompt's text; nil when closed
	last       string  // the last search
	err        error

	// What the title, the mode tag, the mode row and the info rows show and
	// git.Result does not carry.
	project, branch string
	base            string // the base the branch is compared with, resolved
	turns           int    // how many turns turnsFor has
	noSnapshot      bool   // the turn shown has no snapshot
	foreign         bool   // the shown root is not the root of the agent's own directory

	wrapW     codeWidths // the code columns the rows wrap at; zero wraps nothing
	rowsSplit bool       // rows hold side by side pairs
	rowsRev   int        // bumps whenever rows are replaced; rebuild is the only place that does
	treeSel   int        // the file selected while the tree has the keyboard

	cache reviewCache
}

// reviewCache holds what is derived from a result once per generation. The
// fields below the spans follow their own keys as well.
type reviewCache struct {
	ok     bool
	gen    int
	files  int
	tree   []treeItem
	hidden []bool // per file: its paths or text carry a hidden character
	digits int    // digits of the highest line number in the hunks
	spans  map[spanKey][][]diffview.Span

	// The tree column's width at one inner width, the layout for one set of
	// inputs, and the rows last searched for chunks to tokenise.
	treeW, treeInner int
	treeOK           bool
	lay              drawerLayout
	layKey           layoutKey
	layOK            bool
	scan             scanKey
	scanned          bool

	// The drawn lines and what they were drawn from. spansRev counts the chunks
	// spans gained, as the drawing shows them.
	drawn     []string
	drawnPics []picRow // the picture rows of drawn, relative to the drawer
	drawnFit  bool     // the drawn lines can be spliced into a frame, see drawerDrawn
	drawnKey  drawKey
	drawnOK   bool
	spansRev  int
	bySum     map[chunkSum][][]diffview.Span // spans by the text of their chunk
}

// drawerState is the drawer's UI state outside the views.
type drawerState struct {
	focused, tree bool // the drawer has the keyboard; within it, the tree has
	slideFrom     time.Time
	closing       bool
	resizeFrom    int // the width a zoom or un-zoom slide started from; 0 for an open or close slide
	views         map[core.TabID]*reviewView
	list          *wtList      // the open worktree list, over the active tab's drawer
	listGen       int          // names the list wanted; an answer for another is stale
	links         *linkChooser // the open link chooser, over the active tab's drawer
	linksOf       linksOwner
	hl            *diffview.Highlighter // the caches' lexers
	caches        *drawCaches
	dv            *diffview.Theme // the diff view's theme for dvName
	dvName        string
	dvFlash       *diffview.Theme // the same on the flash ground
	cfg           core.DiffConfig // [diff]: the layout and mode a new drawer starts in
	epochs        int             // the last epoch handed out; unique across views
	reads         int             // the last full-file read serial handed out
	prSeq         int             // the last pull request request; its answers carry it
	fetching      map[int]int     // pull request fetches in flight, by number; an entry goes at 0
	prNext        *prNext         // a drawer being opened for a pull request
	hlJob         *hlJob          // the batch being tokenised; at most one runs
	tokenised     int             // lines handed to the batch the last settle started; 0 for none
	layouts       int             // layouts computed rather than reused; tests read it
	scans         int             // searches of the visible rows for chunks to tokenise; tests read it
	renders       int             // drawers drawn rather than reused; tests read it
	fallbacks     int             // frames whose drawer the compositor drew because the splice declined; tests and benches read it
}

// reviewViewOf returns the tab's view, creating it.
func (a *App) reviewViewOf(id core.TabID) *reviewView {
	if a.drawer.views == nil {
		a.drawer.views = map[core.TabID]*reviewView{}
	}

	v, ok := a.drawer.views[id]
	if !ok {
		v = &reviewView{}
		a.drawer.views[id] = v
	}

	return v
}

// derived is the view's cache, rebuilt when the result changed.
func (v *reviewView) derived() *reviewCache {
	c := &v.cache
	if c.ok && c.gen == v.gen && c.files == len(v.res.Files) {
		return c
	}

	*c = reviewCache{ok: true, gen: v.gen, files: len(v.res.Files), spans: map[spanKey][][]diffview.Span{}, bySum: map[chunkSum][][]diffview.Span{}}
	c.tree = treeItems(v.res.Files)

	c.hidden = v.hiddenIn
	if len(c.hidden) != len(v.res.Files) {
		c.hidden = hiddenFlags(v.res.Files)
	}

	hi := 0

	for _, f := range v.res.Files {
		for _, h := range f.Hunks {
			hi = max(hi, h.OldStart+h.OldLines, h.NewStart+h.NewLines)
		}
	}

	c.digits = numDigits(hi)

	return c
}

// keepSpans hands the spans tokenised so far to the next generation of the
// result (a live update or a full-file toggle), which takes those of the chunks
// it reads the same.
func (v *reviewView) keepSpans() {
	if len(v.cache.bySum) > 0 {
		v.carry = v.cache.bySum
	}
}

// treeWidthAt is the tree column's width at an inner width. It walks every
// tree item, so it is computed once per width and generation.
func (c *reviewCache) treeWidthAt(files []git.FileDiff, inner int) int {
	if !c.treeOK || c.treeInner != inner {
		c.treeW, c.treeInner, c.treeOK = treeWidth(c.tree, files, c.hidden, inner), inner, true
	}

	return c.treeW
}

func numDigits(n int) int {
	d := 1
	for ; n >= 10; n /= 10 {
		d++
	}

	return d
}

// rebuild lays the rows out again, for side by side or not, and keeps the
// cursor on what it was on.
func (v *reviewView) rebuild(split bool) { v.rebuildFor(v.res, split) }

// rebuildFor lays res out in place of the current result and keeps the cursor
// on what it was on. It is the only place that replaces the rows.
func (v *reviewView) rebuildFor(res git.Result, split bool) {
	anchor := anchorAt(v.res, v.rows, v.cursor)
	anchor.part = 0 // the cursor sits on its line's first row

	var ends [2]rowAnchor

	if v.sel != nil {
		ends = [2]rowAnchor{anchorAt(v.res, v.rows, v.sel.anchor.row), anchorAt(v.res, v.rows, v.sel.head.row)}
	}

	v.res = res
	v.rowsSplit = split
	v.rowsRev++
	v.rows = buildRows(res, v.rowOpts(split))
	v.cursor = restoreAnchor(res, v.rows, anchor)

	if v.sel != nil {
		v.sel.anchor.row = restoreAnchor(res, v.rows, ends[0])
		v.sel.head.row = restoreAnchor(res, v.rows, ends[1])
	}
}

// picturesMoved reports whether a pending picture change leaves the rows of a
// rendered file out of date: its pictures moved since the rows were laid out.
func (v *reviewView) picturesMoved() bool {
	for path := range v.mdOn {
		if f := v.mdFiles[path]; f != nil && f.doc.hasImages && f.doc.pics.reg != nil && f.doc.pics.reg.gen() != f.rowsGen {
			return true
		}
	}

	return false
}

// rebuildPictures is rebuild for a change the pictures caused: the cursor keeps
// its line and the view its screen row, as the rows of a picture come and go.
func (v *reviewView) rebuildPictures(split bool) {
	before := v.cursor
	file, line := v.mdRowAt(v.cursor)

	var ends [2]struct{ file, line int }

	if v.sel != nil {
		ends[0].file, ends[0].line = v.mdRowAt(v.sel.anchor.row)
		ends[1].file, ends[1].line = v.mdRowAt(v.sel.head.row)
	}

	v.rebuild(split)

	moved := false

	for path, f := range v.mdFiles {
		old, ok := f.doc.pictureMove()
		if !ok {
			continue
		}

		moved = true

		fi := slices.IndexFunc(v.res.Files, func(f git.FileDiff) bool { return f.Path == path })
		if fi < 0 {
			continue
		}

		lay := f.doc.layout(max(v.mdW, 1), v.mdIcons)
		first := slices.IndexFunc(v.rows, func(r reviewRow) bool { return r.kind == rowMD && r.file == fi && r.line == 0 })

		if first < 0 {
			continue
		}

		shifted := func(l int) int { return first + max(min(l+pictureShift(old, lay, l), len(lay.Lines)-1), 0) }

		if file == fi {
			v.cursor = shifted(line)
		}

		if v.sel != nil {
			for k, p := range []*point{&v.sel.anchor, &v.sel.head} {
				if ends[k].file == fi {
					p.row = shifted(ends[k].line)
				}
			}
		}
	}

	if moved {
		v.top = max(v.top+v.cursor-before, 0)
	}
}

// mdRowAt is the file and layout line of row i when it is a rendered row, else -1 and 0.
func (v *reviewView) mdRowAt(i int) (file, line int) {
	if i < 0 || i >= len(v.rows) || v.rows[i].kind != rowMD {
		return -1, 0
	}

	return v.rows[i].file, v.rows[i].line
}

// mdRowsOf lays out every rendered file whose parse has answered, with its
// change marks; a file still parsing has a nil entry.
func (v *reviewView) mdRowsOf() map[string]*mdRows {
	if len(v.mdOn) == 0 {
		return nil
	}

	out := make(map[string]*mdRows, len(v.mdOn))

	for path := range v.mdOn {
		out[path] = nil

		if f := v.mdFiles[path]; f != nil {
			lay := f.doc.layout(max(v.mdW, 1), v.mdIcons)
			f.rowsGen = f.doc.picGen
			out[path] = &mdRows{lay: lay, marks: changeMarks(lay.Source, f.text)}
		}
	}

	return out
}

// stop ends the work running for the view: its load and its full-file reads.
func (v *reviewView) stop() {
	if v.cancel != nil {
		v.cancel()
	}

	v.stopReads()
}

// stopReads ends the full-file reads running for the view.
func (v *reviewView) stopReads() {
	for _, cancel := range v.fullStop {
		cancel()
	}

	v.fullStop = nil
}

// hiddenFlags says per file whether its paths or diff text hold a hidden character.
func hiddenFlags(files []git.FileDiff) []bool {
	out := make([]bool, len(files))
	for i, f := range files {
		out[i] = fileHidden(f)
	}

	return out
}

// easeOutCubic maps the slide's progress to how far the drawer has moved.
func easeOutCubic(t float64) float64 {
	u := 1 - t

	return 1 - u*u*u
}

// slideEase is how far along the slide is, 0 to 1, eased; 1 when none runs.
func (a *App) slideEase() float64 {
	if a.drawer.slideFrom.IsZero() {
		return 1
	}

	t := float64(a.clock.Now().Sub(a.drawer.slideFrom)) / float64(slideFor)

	return easeOutCubic(min(max(t, 0), 1))
}

// drawerTree reports whether the active tab's drawer has its file tree open.
func drawerTree(st core.State) bool {
	rv := st.Tab().Review

	return rv != nil && rv.Tree
}

// drawerFull is the active tab's drawer once it has slid in: the whole body
// while zoomed, else the half. ok is false with no drawer open or a body too
// small for a frame.
func (a *App) drawerFull(st core.State) (core.Rect, bool) {
	half, ok := a.drawerHalf(st)
	if !ok {
		return core.Rect{}, false
	}

	if v := a.drawer.views[st.Tab().ID]; v != nil && v.zoom {
		return a.bodyAreaIn(st.Rail), true
	}

	return half, true
}

// drawerHalf is the drawer unzoomed: the right half of the body, at least 60
// columns, full height. The checks for panes it covers measure it.
func (a *App) drawerHalf(st core.State) (core.Rect, bool) {
	if st.Tab().Review == nil {
		return core.Rect{}, false
	}

	body := a.bodyAreaIn(st.Rail)
	if body.W == 0 {
		return core.Rect{}, false
	}

	w := max(body.W-body.W/2, min(body.W, drawerMinW))

	return core.Rect{X: body.X + body.W - w, Y: body.Y, W: w, H: body.H}, true
}

// drawerBox is the box the drawer is laid out at: drawerFull, except during a
// width slide, where it is the wider of the two widths, so the slide lays the
// drawer out once and shows its left part.
func (a *App) drawerBox(st core.State) (core.Rect, bool) {
	box, ok := a.drawerFull(st)
	if !ok {
		return core.Rect{}, false
	}

	if from := a.drawer.resizeFrom; from > box.W {
		box.X += box.W - from
		box.W = from
	}

	return box, true
}

// drawerRect is the part of the drawer on screen: all of it when it has slid
// in, and less while it slides, from the right edge.
func (a *App) drawerRect(st core.State) (core.Rect, bool) {
	full, ok := a.drawerFull(st)
	if !ok {
		return core.Rect{}, false
	}

	if from := a.drawer.resizeFrom; from > 0 {
		shown := int(math.Round(float64(from) + float64(full.W-from)*a.slideEase()))
		full.X += full.W - shown
		full.W = shown

		return full, true
	}

	frac := a.slideEase()
	if a.drawer.closing {
		frac = 1 - frac
	}

	shown := int(math.Round(frac * float64(full.W)))
	if shown <= 0 {
		return core.Rect{}, false
	}

	full.X += full.W - shown
	full.W = shown

	return full, true
}

// blobIDs names the two sides of a file in the object database; a full-file
// text is current while res still names the same pair for the file.
type blobIDs struct{ old, new string }

// fullRead is the read of one file's two sides that is under way. Only the
// answer that carries its serial is applied, so a read that was cancelled and
// replaced by another of the same blobs cannot stand in for it.
type fullRead struct {
	ids  blobIDs
	read int
}
