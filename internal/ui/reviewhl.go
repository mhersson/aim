package ui

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"io"

	tea "charm.land/bubbletea/v2"

	"github.com/mhersson/aim/internal/core"
	"github.com/mhersson/aim/internal/git"
	"github.com/mhersson/aim/internal/ui/diffview"
)

const (
	// hlChunk is how many lines of one side of a hunk, or of a whole file, are
	// tokenised as one text.
	hlChunk = 512
	// hlBatch bounds the lines one highlighting batch tokenises; a chunk is at
	// most hlChunk lines, so each fits a batch on its own.
	hlBatch = 512
	// hlAhead and hlBehind are the screens tokenised ahead of the view, the way
	// its top last moved, and behind it.
	hlAhead  = 2
	hlBehind = 1
)

// hlWindow is the rows to search for chunks, in order: the visible rows top to
// bottom, then the rest nearest first, the screens ahead before the one behind.
func hlWindow(top, h, n int, up bool) (visible, rest []int) {
	end := min(top+h, n)
	for i := top; i < end; i++ {
		visible = append(visible, i)
	}

	below := func(screens int) {
		for i := end; i < min(end+screens*h, n); i++ {
			rest = append(rest, i)
		}
	}
	above := func(screens int) {
		for i := top - 1; i >= max(top-screens*h, 0); i-- {
			rest = append(rest, i)
		}
	}

	if up {
		above(hlAhead)
		below(hlBehind)
	} else {
		below(hlAhead)
		above(hlBehind)
	}

	return visible, rest
}

// Sides of a hunk that are tokenised on their own.
const (
	sideOld uint8 = iota
	sideNew
)

// spanKey names one chunk of tokenised lines: a side of a hunk, or with hunk
// -1 the new text of a whole file.
type spanKey struct {
	file, hunk int
	side       uint8
	chunk      int
}

// chunkSum names a chunk by its file's path and its text. Spans are a function
// of the two, so a chunk that reads the same in the next result keeps them.
type chunkSum [sha256.Size]byte

// sumOf is the chunkSum of a path and the lines of its chunk; each part is
// length-prefixed so no two chunks hash the same bytes.
func sumOf(path string, lines []string) chunkSum {
	h := sha256.New()

	var n [binary.MaxVarintLen64]byte

	put := func(s string) {
		_, _ = h.Write(n[:binary.PutUvarint(n[:], uint64(len(s)))])
		_, _ = io.WriteString(h, s)
	}

	put(path)

	for _, l := range lines {
		put(l)
	}

	var sum chunkSum

	h.Sum(sum[:0])

	return sum
}

// hlText is one text to tokenise and the chunks that read it.
type hlText struct {
	keys  []spanKey
	sum   chunkSum
	path  string
	lines []string
}

// hlJob is one batch of texts to tokenise. It holds copies of what it reads, so
// its command never touches the app or the view.
type hlJob struct {
	tab   core.TabID
	view  *reviewView
	gen   int
	texts []hlText
}

// drawerSpansMsg answers a job: spans[i] belong to job.texts[i].
type drawerSpansMsg struct {
	job   *hlJob
	spans [][][]diffview.Span
}

// run tokenises the job's texts in order. It returns nil once ctx has ended, which
// happens only as the app closes, so the drawer's hlJob stays set and nothing waits.
func (j *hlJob) run(ctx context.Context, hl *diffview.Highlighter) tea.Msg {
	spans := make([][][]diffview.Span, len(j.texts))

	for i, t := range j.texts {
		if ctx.Err() != nil {
			return nil
		}

		spans[i] = hl.Highlight(t.path, t.lines)
	}

	return drawerSpansMsg{job: j, spans: spans}
}

// drawerAfterUpdate settles the open drawer after an update: it lays the rows
// out for the width, then starts a batch tokenising the rows near the view. The
// batch's command answers with drawerSpansMsg, so nothing runs while the drawer
// is idle. A slide that runs gets its frames here too, since one may begin
// inside keyboardCheck's release.
func (a *App) drawerAfterUpdate() tea.Cmd {
	var tick tea.Cmd
	if !a.drawer.slideFrom.IsZero() {
		tick = a.slideFrameCmd()
	}

	return tea.Batch(tick, a.settleDrawerRows())
}

// settleDrawerRows is drawerAfterUpdate's layout and highlighting.
func (a *App) settleDrawerRows() tea.Cmd {
	a.drawer.tokenised = 0

	st := a.store.State()

	// A drawer that no longer shows has no zoom to keep, whichever way its tab was left.
	for id, o := range a.drawer.views {
		if id != st.Tab().ID {
			o.zoom = false
		}
	}

	box, ok := a.drawerBox(st)
	if !ok {
		return nil
	}

	v := a.drawer.views[st.Tab().ID]
	if v == nil {
		return nil
	}

	lay := a.drawerLayoutOf(box, v, drawerTree(st))

	// The rows under a page are laid out again when it goes.
	if p := v.page(); p != nil {
		p.resize(lay.diffW, lay.diffH, a.icons)

		return nil
	}

	// Rendered rows are laid out four cells narrower than the column.
	mdW := max(lay.diffW-4, 1)
	moved := v.mdW != mdW || v.mdIcons != a.icons
	v.mdW, v.mdIcons = mdW, a.icons

	stale := v.marksStale(st)
	if stale {
		v.syncMarks(st)
	}

	pics := v.picturesMoved()

	wrap := lay.codeWidths()
	rewrap := v.wrapW != wrap
	v.wrapW = wrap

	switch {
	case pics:
		v.rebuildPictures(lay.split)
	case v.rowsSplit != lay.split || rewrap || (moved && len(v.mdFiles) > 0) || stale:
		v.rebuild(lay.split)
	}

	j := a.highlightRows(st.Tab().ID, v, lay)
	if j == nil {
		return nil
	}

	ctx, hl := a.ctx, a.drawer.hl

	return func() tea.Msg { return j.run(ctx, hl) }
}

// highlightRows gathers the chunks no cache holds into a job of at most hlBatch
// lines: those of the visible rows, and only when they have all got spans those
// of the screens ahead and behind. It returns nil while a job runs, and when
// nothing is left to tokenise; chunks past the batch wait for the next one.
func (a *App) highlightRows(tab core.TabID, v *reviewView, lay drawerLayout) *hlJob {
	if a.drawer.hlJob != nil || len(v.rows) == 0 || lay.diffH <= 0 {
		return nil
	}

	c := v.derived()

	top := v.viewTop(lay.diffH)
	if top != v.hlTop {
		v.hlUp, v.hlTop = top < v.hlTop, top
	}

	key := scanKey{rev: v.rowsRev, top: top, bodyH: lay.diffH}

	if c.scanned && c.scan == key {
		return nil
	}

	a.drawer.scans++

	var (
		texts  []hlText
		bySum  = map[chunkSum]int{}
		queued = map[spanKey]bool{}
		budget = hlBatch
	)

	// holdOnly stores the chunks a cache holds and adds no text to the job.
	walk := func(rows []int, holdOnly bool) {
		for _, i := range rows {
			for _, ref := range v.rowSpans(i) {
				if _, done := c.spans[ref.key]; done || queued[ref.key] {
					continue
				}

				path, lines := v.chunkLines(ref.key)
				sum := sumOf(path, lines)

				spans, known := v.carry[sum]
				if !known {
					spans, known = c.bySum[sum]
				}

				if known {
					c.spans[ref.key], c.bySum[sum] = spans, spans
					c.spansRev++

					continue
				}

				if holdOnly {
					continue
				}

				if at, ok := bySum[sum]; ok {
					texts[at].keys = append(texts[at].keys, ref.key)
					queued[ref.key] = true

					continue
				}

				if len(lines) > budget {
					queued[ref.key] = true

					continue
				}

				budget -= len(lines)
				bySum[sum] = len(texts)
				queued[ref.key] = true
				texts = append(texts, hlText{keys: []spanKey{ref.key}, sum: sum, path: path, lines: lines})
			}
		}
	}

	visible, rest := hlWindow(top, lay.diffH, len(v.rows), v.hlUp)

	walk(visible, false)

	switch {
	case len(texts) == 0:
		walk(rest, false)
	case len(v.carry) > 0:
		// A second generation would otherwise drop the look-ahead spans that only carry holds.
		walk(rest, true)
	}

	c.scan = key

	if len(texts) == 0 {
		c.scanned = true

		return nil
	}

	c.scanned = false
	j := &hlJob{tab: tab, view: v, gen: c.gen, texts: texts}
	a.drawer.hlJob = j

	for _, t := range texts {
		a.drawer.tokenised += len(t.lines)
	}

	return j
}

// onDrawerSpans takes a job's answer. The spans always join the view's by-text
// cache; they reach the by-key one only while the result is the job's own
// generation. The scan is then stale, so the next settle picks the next batch.
func (a *App) onDrawerSpans(msg drawerSpansMsg) {
	if msg.job != a.drawer.hlJob {
		return
	}

	a.drawer.hlJob = nil

	job := msg.job
	if a.drawer.views[job.tab] != job.view {
		return
	}

	c := job.view.derived()

	for i, t := range job.texts {
		c.bySum[t.sum] = msg.spans[i]

		if c.gen != job.gen {
			continue
		}

		for _, k := range t.keys {
			c.spans[k] = msg.spans[i]
			c.spansRev++
		}
	}

	c.scanned = false
}

// scanKey is what the rows searched for chunks to tokenise depend on besides
// the result's generation: the rows and the part of them in view.
type scanKey struct{ rev, top, bodyH int }

// spanRef locates one line's spans: the chunk, and the line's place in it.
type spanRef struct {
	key spanKey
	at  int
}

// viewTop is the first visible row for a view h rows tall: the stored top,
// clamped to the rows. It moves only when no row of the cursor's line shows,
// and then shows the line whole when it fits, else from its first row. A line
// that is partly in view stays where it is, so a scroll can move past it.
func (v *reviewView) viewTop(h int) int {
	return v.topFor(h, false)
}

// revealTop is viewTop for a key move: a line that fits shows whole, and a
// taller one keeps a row in view, starting at the top when it was out of view.
func (v *reviewView) revealTop(h int) int {
	return v.topFor(h, true)
}

func (v *reviewView) topFor(h int, whole bool) int {
	if h <= 0 {
		return 0
	}

	top := min(max(v.top, 0), max(len(v.rows)-h, 0))
	if v.cursor < 0 || v.cursor >= len(v.rows) {
		return max(top, 0)
	}

	start, end := v.lineStart(v.cursor), v.lineEnd(v.cursor)
	out := end <= top || start >= top+h

	switch {
	case end-start > h:
		if out {
			top = start
		}
	case out || whole:
		if start < top {
			top = start
		} else if end > top+h {
			top = end - h
		}
	}

	return max(top, 0)
}

// lineRef is where line li of hunk hi lies among the tokenised lines: its side
// is the new one unless the line was removed. ok is false for a line whose
// number does not fit the hunk.
func lineRef(file, hi int, h git.Hunk, li int) (spanRef, bool) {
	l := h.Lines[li]
	side, idx := sideNew, l.New-h.NewStart

	if l.Kind == git.Del {
		side, idx = sideOld, l.Old-h.OldStart
	}

	if idx < 0 {
		return spanRef{}, false
	}

	return spanRef{spanKey{file, hi, side, idx / hlChunk}, idx % hlChunk}, true
}

// rowSpans lists the spans of the lines row i shows; a row without code has none.
func (v *reviewView) rowSpans(i int) []spanRef {
	r := v.rows[i]

	switch r.kind {
	case rowFull:
		return []spanRef{{spanKey{r.file, -1, sideNew, r.line / hlChunk}, r.line % hlChunk}}
	case rowLine, rowPair:
		h := v.res.Files[r.file].Hunks[r.hunk]

		var out []spanRef

		for _, li := range [...]int{r.line, r.other} {
			if li < 0 {
				continue
			}

			if ref, ok := lineRef(r.file, r.hunk, h, li); ok {
				out = append(out, ref)
			}
		}

		return out
	}

	return nil
}

// chunkLines is the path and the cleaned lines of one chunk, as they are drawn.
func (v *reviewView) chunkLines(k spanKey) (path string, lines []string) {
	f := v.res.Files[k.file]
	start, end := k.chunk*hlChunk, (k.chunk+1)*hlChunk

	if k.hunk < 0 {
		text := v.full[f.Path].new
		for _, s := range text[min(start, len(text)):min(end, len(text))] {
			c, _ := shownLine(s)
			lines = append(lines, c)
		}

		return f.Path, lines
	}

	n := 0

	for _, l := range f.Hunks[k.hunk].Lines {
		if (k.side == sideNew && l.Kind == git.Del) || (k.side == sideOld && l.Kind == git.Add) {
			continue
		}

		if n >= end {
			break
		}

		if n >= start {
			c, _ := shownLine(l.Text)
			lines = append(lines, c)
		}

		n++
	}

	return f.Path, lines
}
