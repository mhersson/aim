package ui

import (
	"image/color"
	"slices"
	"strings"

	"charm.land/lipgloss/v2"

	"github.com/mhersson/aim/internal/md"
)

// mdView is a cursor, scrolling, folds, heading jumps, search and link targets
// over one mdDoc. The owner calls size before keys and draws.
type mdView struct {
	doc         mdSource
	cursor, top int

	w, h  int
	icons bool

	sel *selection // rows are layout lines, columns the view's cells
}

// mdTarget is somewhere a rendered line leads: a URL or a local path.
type mdTarget struct{ Text, URL, Path string }

// size sets the view's cells; the text is laid out four cells narrower.
func (v *mdView) size(w, h int, icons bool) {
	v.w, v.h, v.icons = w, h, icons
	v.clamp()
}

func (v *mdView) textWidth() int { return max(v.w-4, 1) }

// layout is the doc laid out for the view's text width.
func (v *mdView) layout() md.Layout {
	if v.doc == nil {
		return md.Layout{}
	}

	lay := v.doc.layout(v.textWidth(), v.icons)
	if old, ok := v.doc.pictureMove(); ok {
		v.keepRow(old, lay)
	}

	return lay
}

// keepRow moves the cursor and the top by the cursor's shift between two layouts
// that differ in picture rows, so the cursor keeps its text and its screen row.
func (v *mdView) keepRow(old, cur md.Layout) {
	d := pictureShift(old, cur, v.cursor)
	n, h := len(cur.Lines), max(v.h, 1)

	v.cursor = max(min(v.cursor+d, n-1), 0)
	v.top = max(min(v.top+d, n-h), 0)

	if v.sel != nil {
		for _, p := range []*point{&v.sel.anchor, &v.sel.head} {
			p.row = max(min(p.row+pictureShift(old, cur, p.row), n-1), 0)
		}
	}
}

// clamp keeps the cursor on a line and in the window.
func (v *mdView) clamp() {
	n := len(v.layout().Lines)
	v.cursor = max(min(v.cursor, n-1), 0)

	h := max(v.h, 1)
	if v.cursor < v.top {
		v.top = v.cursor
	}

	if v.cursor >= v.top+h {
		v.top = v.cursor - h + 1
	}

	v.top = max(min(v.top, n-h), 0)
}

func (v *mdView) move(delta int) {
	v.cursor += delta
	v.clamp()
}

// page moves half the height.
func (v *mdView) page(dir int) { v.move(dir * max(v.h/2, 1)) }

// pageFull moves the window and the cursor by the height less one line.
func (v *mdView) pageFull(dir int) {
	d := dir * max(v.h-1, 1)
	n, h := len(v.layout().Lines), max(v.h, 1)

	v.top = min(max(v.top+d, 0), max(n-h, 0))
	v.cursor = min(max(v.cursor+d, 0), max(n-1, 0))
	v.clamp()
}

// scroll moves the window by delta lines; the cursor moves only to stay in it.
func (v *mdView) scroll(delta int) {
	n, h := len(v.layout().Lines), max(v.h, 1)

	v.top = min(max(v.top+delta, 0), max(n-h, 0))
	v.cursor = min(max(v.cursor, v.top), max(min(v.top+h-1, n-1), 0))
}

func (v *mdView) end(bottom bool) {
	v.cursor = 0
	if bottom {
		v.cursor = len(v.layout().Lines) - 1
	}

	v.clamp()
}

func isHeadingLine(l md.Line) bool { return l.IsHeading() }

// heading moves to the first line of the next (dir > 0) or previous heading and
// stays put when there is none.
func (v *mdView) heading(dir int) {
	lay := v.layout()
	start := func(i int) bool {
		return isHeadingLine(lay.Lines[i]) && (i == 0 || !isHeadingLine(lay.Lines[i-1]) || lay.Source[i] != lay.Source[i-1])
	}

	for i := v.cursor + dir; i >= 0 && i < len(lay.Lines); i += dir {
		if start(i) {
			v.cursor = i
			v.clamp()

			return
		}
	}
}

// find moves to the next line holding q, ignoring case, from the line after the
// cursor and wrapping.
func (v *mdView) find(q string) bool {
	lay := v.layout()
	if q == "" || len(lay.Lines) == 0 {
		return false
	}

	plain := v.doc.text()
	q = strings.ToLower(q)

	for k := 1; k <= len(plain); k++ {
		i := (v.cursor + k) % len(plain)
		if strings.Contains(strings.ToLower(plain[i]), q) {
			v.cursor = i
			v.clamp()

			return true
		}
	}

	return false
}

// findPrev moves to the previous line holding q, ignoring case, from the line
// before the cursor and wrapping.
func (v *mdView) findPrev(q string) bool {
	if q == "" || len(v.layout().Lines) == 0 {
		return false
	}

	plain := v.doc.text()
	q = strings.ToLower(q)

	for k := 1; k <= len(plain); k++ {
		i := ((v.cursor-k)%len(plain) + len(plain)) % len(plain)
		if strings.Contains(strings.ToLower(plain[i]), q) {
			v.cursor = i
			v.clamp()

			return true
		}
	}

	return false
}

// fold toggles the fold whose head is the cursor's line; false when there is none.
func (v *mdView) fold() bool {
	for _, f := range v.layout().Folds {
		if f.Line == v.cursor {
			v.doc.toggleFold(f.ID)
			v.clamp()

			return true
		}
	}

	return false
}

func (v *mdView) targets() []mdTarget { return lineTargets(v.layout(), v.cursor) }

// lineTargets are the links and paths of line i in order, each once, and the
// link of an image on it. mailto links are left out: the terminal opens them.
func lineTargets(lay md.Layout, i int) []mdTarget {
	if i < 0 || i >= len(lay.Lines) {
		return nil
	}

	var out []mdTarget

	seen := map[string]int{}
	last := ""

	add := func(key, text string, t mdTarget) {
		if at, ok := seen[key]; ok {
			if key == last {
				out[at].Text += text
			}

			last = key

			return
		}

		t.Text = text
		seen[key] = len(out)
		out = append(out, t)
		last = key
	}

	for _, r := range lay.Lines[i] {
		switch {
		case r.Link >= 0 && r.Link < len(lay.Links):
			u := lay.Links[r.Link].URL
			if !strings.HasPrefix(strings.ToLower(u), "mailto:") {
				add("u:"+u, r.Text, mdTarget{URL: u})

				continue
			}

			last = ""
		case r.Path != "":
			add("p:"+r.Path, r.Text, mdTarget{Path: r.Path})
		default:
			last = ""
		}
	}

	for _, im := range lay.Images {
		if i < im.Line || i >= im.Line+imageRows(im) || im.Link < 0 || im.Link >= len(lay.Links) {
			continue
		}

		u := lay.Links[im.Link].URL
		if strings.HasPrefix(strings.ToLower(u), "mailto:") {
			continue
		}

		if at, ok := seen["u:"+u]; ok {
			if im.Alt != "" {
				out[at].Text = im.Alt
			}

			continue
		}

		seen["u:"+u] = len(out)
		out = append(out, mdTarget{Text: im.Alt, URL: u})
	}

	for k := range out {
		out[k].Text = strings.TrimSpace(out[k].Text)
	}

	return out
}

// sourceLine is the source line an editor opens at for the cursor's line.
func (v *mdView) sourceLine() int { return blockEditLine(v.layout().Source, v.cursor) }

// blockEditLine is the first source line of layout line i, or for a line no source
// made the first line of the next block, else the last line of the previous
// one, else 1.
func blockEditLine(src []md.SourceRange, i int) int {
	if i < 0 || i >= len(src) {
		return 1
	}

	if src[i].First != 0 {
		return src[i].First
	}

	for _, r := range src[i+1:] {
		if r.First != 0 {
			return r.First
		}
	}

	for j := i - 1; j >= 0; j-- {
		if src[j].First != 0 {
			return src[j].Last
		}
	}

	return 1
}

// goSource puts the cursor on the first line of the narrowest block holding
// source line n, else the next block, else the last line.
func (v *mdView) goSource(n int) {
	v.cursor = sourceLineIndex(v.layout().Source, n)
	v.clamp()
}

// sourceLineIndex is the layout line of the narrowest range holding source
// line n, else of the first range after it, else the last line.
func sourceLineIndex(src []md.SourceRange, n int) int {
	best, width := -1, 0

	for i, r := range src {
		if r.First != 0 && r.First <= n && n <= r.Last && (best < 0 || r.Last-r.First < width) {
			best, width = i, r.Last-r.First
		}
	}

	if best < 0 {
		next := 0

		for i, r := range src {
			if r.First > n && (best < 0 || r.First < next) {
				best, next = i, r.First
			}
		}
	}

	if best < 0 {
		best = len(src) - 1
	}

	return best
}

// draw is h rows of w cells. Cell 0 of the cursor's row holds ▎ on the cursor
// ground, cell 1 is blank, the text starts at cell 2 and two cells stay free
// on the right. An unfocused view shows no cursor.
func (v *mdView) draw(p mdPainter, focused bool) []string {
	rows, _ := v.drawPictures(p, focused)

	return rows
}

// drawPictures is draw, and the picture rows in view: each at cell X, row Y of
// the view, on the ground of its row.
func (v *mdView) drawPictures(p mdPainter, focused bool) ([]string, []picRow) {
	lay := v.layout()
	lead, tw := min(textLead, v.w), max(v.w-4, 0)
	trail := max(v.w-lead-tw, 0)
	rows := make([]string, 0, v.h)

	var pics []picRow

	pad := func(pp mdPainter, n int) string {
		return lipgloss.NewStyle().Background(pp.ground).Render(strings.Repeat(" ", n))
	}

	for r := range v.h {
		i := v.top + r
		pp, mark := p, pad(p, lead)

		if i == v.cursor && focused && i < len(lay.Lines) {
			pp = p.withGround(mixHex(p.ground, p.ui.Accent, tintCursor))

			mark = lipgloss.NewStyle().Foreground(p.ui.Accent).Background(pp.ground).Render("▎")
			if lead > 1 {
				mark += pad(pp, lead-1)
			}
		}

		text := pad(pp, tw)
		if i < len(lay.Lines) {
			text = pp.line(lay.Lines[i], lay.Links, tw)

			if im, row, ok := pictureAt(lay, i); ok {
				pics = append(pics, picRow{X: 2 + pictureCol(lay.Lines[i]), Y: r, ID: im.ID, Row: row, Cols: im.Cols, Ground: pp.ground})
			}
		}

		row := mark + text + pad(pp, trail)
		if spans := v.selSpans(lay, i, lead+tw, p.ui.Selection); len(spans) > 0 {
			row = paintSpans(row, v.w, spans)
		}

		rows = append(rows, row)
	}

	return rows, pics
}

// textLead is the cells left of the text.
const textLead = 2

// lineText is what layout line i copies, in view cells.
func (v *mdView) lineText(i int) md.LineText {
	v.layout()

	if v.doc == nil {
		return md.LineText{}
	}

	return shiftText(v.doc.lineText(i))
}

// shiftText moves a laid-out line's pieces right to where the view draws them.
func shiftText(lt md.LineText) md.LineText {
	lt.Pieces = slices.Clone(lt.Pieces)
	for k := range lt.Pieces {
		lt.Pieces[k].Col += textLead
	}

	return lt
}

// grid is the view's lines as a selection grid.
func (v *mdView) grid() textGrid {
	lay := v.layout()
	doc := v.doc

	return textGrid{
		lines: func(i int) md.LineText {
			if doc == nil {
				return md.LineText{}
			}

			return shiftText(doc.lineText(i))
		},
		rows: len(lay.Lines), cols: v.w,
	}
}

// unitText is the text of the unit lineAt takes at line i.
func (v *mdView) unitText(i int) string {
	g := v.grid()
	if g.rows == 0 {
		return ""
	}

	return textCopy(g.lines, lineAt(g, point{i, 0}), g.cols)
}

// selSpans are the cells of line i the selection paints, within cells below end.
func (v *mdView) selSpans(lay md.Layout, i, end int, bg color.Color) []spanPaint {
	if v.sel == nil || v.doc == nil || i >= len(lay.Lines) {
		return nil
	}

	from, to, ok := v.sel.rowSpan(i, v.w)
	if !ok {
		return nil
	}

	var spans []spanPaint

	for _, c := range textPaint(shiftText(v.doc.lineText(i)), from, to) {
		if c[0] < end {
			spans = append(spans, spanPaint{from: c[0], to: min(c[1], end-1), bg: bg})
		}
	}

	return spans
}
