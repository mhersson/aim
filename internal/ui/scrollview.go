package ui

import (
	uv "github.com/charmbracelet/ultraviolet"

	"github.com/mhersson/aim/internal/term"
)

// Rows are absolute: history index 0 is the oldest line and the live screen
// follows the history. A scrolled view is anchored by a stable row (absolute
// row plus the lines trimmed from the history so far), so it stays on the same
// text while output scrolls and the 10000-line cap drops old lines. The view is
// always live on the alt screen, which has no history.

// scrollStep is the lines one wheel notch scrolls.
const scrollStep = 3

// viewKey identifies a built scrolled view: any change of it rebuilds the rows.
type viewKey struct {
	top, hist, cols, rows int
	trimmed, gen          uint64
}

// viewCache holds the scrolled view's rows. hist keeps the rendered history
// rows of the last build by stable row, so scrolling by a few lines renders
// only the rows that came into view.
type viewCache struct {
	valid bool
	key   viewKey
	rows  []string
	hist  map[uint64]string
}

// histLenLocked is the length of the history the view can scroll through.
func (p *pane) histLenLocked() int {
	if p.vt.AltScreen() {
		return 0
	}

	return p.vt.ScrollbackLen()
}

// viewTopLocked returns the absolute row of the first view row and the history
// length. A pane that is not scrolled, or whose anchor fell off the end of the
// history, is live: top equals the history length.
func (p *pane) viewTopLocked() (top, hist int) {
	hist = p.histLenLocked()
	if !p.scrolled {
		return hist, hist
	}

	top = max(int(int64(p.anchor)-int64(p.vt.HistoryTrimmed())), 0)
	if top >= hist {
		p.scrolled = false

		return hist, hist
	}

	p.setTopLocked(top, hist)

	return top, hist
}

// setTopLocked anchors the view at absolute row top; the history length and
// beyond is live.
func (p *pane) setTopLocked(top, hist int) {
	top = max(top, 0)
	if top >= hist {
		p.scrolled = false

		return
	}

	p.scrolled = true
	p.anchor = uint64(top) + p.vt.HistoryTrimmed()
}

// scrollBy moves the view n lines toward older output (negative: toward the
// live screen), clamped to the history, and reports whether it moved.
func (p *pane) scrollBy(n int) bool {
	p.mu.Lock()
	defer p.mu.Unlock()

	if p.closed {
		return false
	}

	top, hist := p.viewTopLocked()
	next := min(max(top-n, 0), hist)

	if next == top {
		return false
	}

	p.setTopLocked(next, hist)

	return true
}

// scrollLive returns the view to the live screen.
func (p *pane) scrollLive() {
	p.mu.Lock()
	defer p.mu.Unlock()

	p.scrolled = false
}

// viewTop returns the absolute row of the first view row; the history length
// when live.
func (p *pane) viewTop() int {
	p.mu.Lock()
	defer p.mu.Unlock()

	top, _ := p.viewTopLocked()

	return top
}

// linesAbove is how many lines the view is above the live screen; 0 when live.
func (p *pane) linesAbove() int {
	p.mu.Lock()
	defer p.mu.Unlock()

	top, hist := p.viewTopLocked()

	return hist - top
}

// setViewTop shows the history from absolute row top, clamped to the history;
// the history length and beyond is live.
func (p *pane) setViewTop(top int) {
	p.mu.Lock()
	defer p.mu.Unlock()

	if p.closed {
		return
	}

	_, hist := p.viewTopLocked()
	p.setTopLocked(min(top, hist), hist)
}

// viewLocked returns the rows to draw while the pane is scrolled; the caller holds
// mu. The history part is cached by stable row and the whole view by viewKey.
func (p *pane) viewLocked(top, hist int) []string {
	cols, rows := p.vt.Size()
	trimmed := p.vt.HistoryTrimmed()
	key := viewKey{top: top, hist: hist, cols: cols, rows: rows, trimmed: trimmed, gen: p.gen}

	c := &p.viewCache
	if c.valid && c.key == key {
		return c.rows
	}

	out := make([]string, rows)
	hists := make(map[uint64]string, rows)
	blank := ""

	for i := range out {
		abs := top + i

		if abs >= hist {
			if sr := abs - hist; sr < len(p.rows) {
				out[i] = p.rows[sr]
			} else {
				if blank == "" {
					blank = renderCells(func(int) *uv.Cell { return nil }, cols, nil)
				}

				out[i] = blank
			}

			continue
		}

		stable := uint64(abs) + trimmed

		s, ok := "", false
		if c.key.cols == cols {
			s, ok = c.hist[stable]
		}

		if !ok {
			cells := p.vt.ScrollbackLine(abs)
			s = renderCells(func(x int) *uv.Cell {
				if x < len(cells) {
					return &cells[x]
				}

				return nil
			}, cols, nil)
		}

		out[i] = s
		hists[stable] = s
	}

	*c = viewCache{valid: true, key: key, rows: out, hist: hists}

	return out
}

// lineCacheSize is how many decoded history lines a paneGrid keeps: enough for
// the neighbouring rows a motion reads, few enough that a walk over a long
// history does not hold it in memory.
const lineCacheSize = 8

// paneGrid is the grid over a pane's history and screen. It lives for one
// withGrid call and keeps the last few history lines it decodes.
type paneGrid struct {
	vt         term.VT
	hist, rows int
	cols       int
	lines      [lineCacheSize]struct {
		row  int
		line []uv.Cell
	}
	used, next int
}

func (g *paneGrid) Rows() int { return g.hist + g.rows }
func (g *paneGrid) Cols() int { return g.cols }

func (g *paneGrid) Cell(row, col int) *uv.Cell {
	if row < 0 || col < 0 || col >= g.cols {
		return nil
	}

	if row >= g.hist {
		if row-g.hist >= g.rows {
			return nil
		}

		return g.vt.CellAt(col, row-g.hist)
	}

	line := g.historyLine(row)
	if col >= len(line) {
		return nil
	}

	return &line[col]
}

func (g *paneGrid) historyLine(row int) []uv.Cell {
	for _, e := range g.lines[:g.used] {
		if e.row == row {
			return e.line
		}
	}

	line := g.vt.ScrollbackLine(row)
	g.lines[g.next].row, g.lines[g.next].line = row, line
	g.next = (g.next + 1) % lineCacheSize
	g.used = min(g.used+1, lineCacheSize)

	return line
}

func (g *paneGrid) historyRows() int { return g.hist }

func (g *paneGrid) scanText(from, end int, fn func(row int, cells []term.TextCell) bool) {
	g.vt.ScrollbackText(from, end, fn)
}

func (g *paneGrid) Wrapped(row int) bool {
	switch {
	case row < 0 || row >= g.Rows():
		return false
	case row < g.hist:
		return g.vt.ScrollbackWrapped(row)
	}

	return g.vt.RowWrapped(row - g.hist)
}

// withGrid runs fn under the pane lock with the pane's text in absolute rows.
// The grid is only valid inside fn. A closed pane runs nothing.
func (p *pane) withGrid(fn func(g grid)) {
	p.withView(func(g grid, _ int, _ uint64) { fn(g) })
}

// withView is withGrid plus the absolute row of the first view row and the
// lines trimmed from the history so far, which turn rows into stable rows.
func (p *pane) withView(fn func(g grid, top int, trimmed uint64)) {
	p.mu.Lock()
	defer p.mu.Unlock()

	if p.closed {
		return
	}

	top, hist := p.viewTopLocked()
	cols, rows := p.vt.Size()

	fn(&paneGrid{
		vt: p.vt, hist: hist, rows: rows, cols: cols,
	}, top, p.vt.HistoryTrimmed())
}
