package ui

import (
	"slices"

	tea "charm.land/bubbletea/v2"
	uv "github.com/charmbracelet/ultraviolet"

	"github.com/mhersson/aim/internal/core"
)

// stablePoint is a cell whose row survives scrollback trimming: the absolute
// row plus the lines trimmed from the history so far.
type stablePoint struct {
	row uint64
	col int
}

// paneSelection is the app's one selection, anchored to stable rows so it stays
// on the same text while output scrolls.
type paneSelection struct {
	pane         core.PaneID
	mode         selMode
	anchor, head stablePoint

	// watched is set when some part was on the live screen at watch time; the
	// live rows from watchFrom (a stable row) then read shown.
	watched   bool
	watchFrom uint64
	shown     string
}

func stable(p point, trimmed uint64) stablePoint {
	return stablePoint{row: uint64(p.row) + trimmed, col: p.col}
}

// newPaneSelection stores s, whose rows are absolute, against trimmed.
func newPaneSelection(id core.PaneID, s selection, trimmed uint64) *paneSelection {
	return &paneSelection{pane: id, mode: s.mode, anchor: stable(s.anchor, trimmed), head: stable(s.head, trimmed)}
}

// resolve converts the selection back to absolute rows of g. An end trimmed off
// the history clamps to its first row; it reports false once both ends are gone.
func (s paneSelection) resolve(g grid, trimmed uint64) (selection, bool) {
	conv := func(p stablePoint) (point, bool) {
		row := int64(p.row) - int64(trimmed)
		if row < 0 {
			col := 0
			if s.mode == selBlock {
				col = p.col
			}

			return clampPoint(g, point{0, col}), false
		}

		return clampPoint(g, point{int(row), p.col}), true
	}

	anchor, aok := conv(s.anchor)
	head, hok := conv(s.head)

	return selection{mode: s.mode, anchor: anchor, head: head}, aok || hok
}

// liveTop is the absolute row of g's first live screen row.
func liveTop(g grid) int {
	if ts, ok := g.(textScanner); ok {
		return ts.historyRows()
	}

	return 0
}

// watch records the text the selection covers on the live rows, for redrawn.
// A selection wholly in history never changes and is not watched.
func (s *paneSelection) watch(g grid, trimmed uint64) {
	s.watched, s.shown = false, ""

	sel, ok := s.resolve(g, trimmed)
	if !ok {
		return
	}

	top := liveTop(g)

	below, ok := sel.below(top)
	if !ok {
		return
	}

	s.watched = true
	s.watchFrom = uint64(top) + trimmed
	s.shown = extract(g, below)
}

// redrawn reports whether the text under a watched selection's live rows
// differs from what watch saw. A fallen selection is dropFallenSelection's.
func (s *paneSelection) redrawn(g grid, trimmed uint64) bool {
	if !s.watched {
		return false
	}

	sel, ok := s.resolve(g, trimmed)
	if !ok {
		return false
	}

	var text string

	if below, ok := sel.below(int(max(int64(s.watchFrom)-int64(trimmed), 0))); ok {
		text = extract(g, below)
	}

	return text != s.shown
}

// clearSelectionOf drops the selection and any press in progress that belong to pane id.
func (a *App) clearSelectionOf(id core.PaneID) {
	if a.sel != nil && a.sel.pane == id {
		a.sel = nil
	}

	if pr := a.mouse.press; pr != nil && pr.pane == id {
		a.mouse.press = nil
	}

	if f := a.mouse.fwd; f != nil && f.pane == id {
		a.mouse.fwd = nil
	}
}

// dropFallenSelection clears the selection of p once its text left the history.
func (a *App) dropFallenSelection(p *pane) {
	if a.sel == nil || a.sel.pane != p.id {
		return
	}

	alive := false

	p.withView(func(g grid, _ int, trimmed uint64) { _, alive = a.sel.resolve(g, trimmed) })

	if !alive {
		a.sel = nil
	}
}

// dropRedrawnSelection clears the selection of p once a render changed the
// text it covers; it waits for the release of the press that is making it.
func (a *App) dropRedrawnSelection(p *pane) {
	if a.sel == nil || a.sel.pane != p.id || !a.sel.watched {
		return
	}

	if pr := a.mouse.press; pr != nil && pr.pane == p.id {
		return
	}

	redrawn := false

	p.withView(func(g grid, _ int, trimmed uint64) { redrawn = a.sel.redrawn(g, trimmed) })

	if redrawn {
		a.sel = nil
	}
}

// selectedText is the text of the selection, "" when none or its pane is gone.
func (a *App) selectedText() string {
	s := a.sel
	if s == nil {
		return a.textSelected()
	}

	p := a.panes[s.pane]
	if p == nil {
		return ""
	}

	var text string

	claude := isClaudePane(a.store.State().Panes[s.pane])

	p.withView(func(g grid, _ int, trimmed uint64) {
		if sel, ok := s.resolve(g, trimmed); ok {
			text = extractFor(g, sel, claude)
		}
	})

	return text
}

// copySelection implements copy.selection; the selection stays.
func (a *App) copySelection() tea.Cmd {
	return a.clip.copy(a.selectedText())
}

// inView expresses the selection in rows relative to base, the stable row of
// the first row drawn, so it lands on the rows as drawn.
func (s paneSelection) inView(base uint64) selection {
	rel := func(p stablePoint) point {
		return point{row: int(int64(p.row) - int64(base)), col: p.col}
	}

	return selection{mode: s.mode, anchor: rel(s.anchor), head: rel(s.head)}
}

// selectionOf is the selection to draw on p: copy mode's own while it is active
// on p, else the mouse selection.
func (a *App) selectionOf(p *pane) *paneSelection {
	if a.copy != nil && a.copy.pane == p.id {
		if a.copy.m.sel == nil {
			return nil
		}

		return newPaneSelection(p.id, *a.copy.m.sel, a.copy.trimmed)
	}

	if a.sel != nil && a.sel.pane == p.id {
		return a.sel
	}

	return nil
}

// withSelection returns the pane's drawn rows with the ones the selection
// touches repainted on the theme's selection background. They are repainted
// from the cached rows themselves, never the live emulator, so a held sync
// update or output not yet rendered cannot tear them from the rest.
func (a *App) withSelection(p *pane) []string {
	lines, base := p.linesAt()

	ps := a.selectionOf(p)
	if ps == nil {
		return lines
	}

	cols, _ := p.size()
	sel := ps.inView(base)
	bg := a.current().theme.UI.Selection
	out, cloned := lines, false

	for i, line := range lines {
		from, to, ok := sel.rowSpan(i, cols)
		if !ok {
			continue
		}

		if !cloned {
			out, cloned = slices.Clone(lines), true
		}

		out[i] = paintSpans(line, cols, []spanPaint{{from: from, to: to, bg: bg}})
	}

	return out
}

// paintSpans repaints a rendered row with spans.
func paintSpans(line string, cols int, spans []spanPaint) string {
	buf := uv.NewScreenBuffer(cols, 1)
	uv.NewStyledString(line).Draw(buf, buf.Bounds())

	return renderCells(func(x int) *uv.Cell { return buf.CellAt(x, 0) }, cols, spans)
}
