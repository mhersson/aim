package ui

import (
	"strings"
	"unicode"
	"unicode/utf8"

	uv "github.com/charmbracelet/ultraviolet"

	"github.com/mhersson/aim/internal/term"
)

// grid is a pane's text in absolute rows: history rows first, then the live screen.
type grid interface {
	Rows() int
	Cols() int
	// Cell returns nil outside the grid.
	Cell(row, col int) *uv.Cell
	// Wrapped reports whether row soft-wraps into row+1.
	Wrapped(row int) bool
}

type point struct{ row, col int }

type selMode int

const (
	selStream selMode = iota
	selLine
	selBlock
)

// selection is inclusive on both ends. Anchor and head may be in either order.
type selection struct {
	mode         selMode
	anchor, head point
}

// Character classes shared by double-click and the copy-mode word motions.
const (
	classBlank = iota
	classWord
	classOther
)

// charClass classifies a cell's content by its first rune.
func charClass(content string) int {
	if content == "" || content == " " {
		return classBlank
	}

	r, _ := utf8.DecodeRuneInString(content)
	if unicode.IsLetter(r) || unicode.IsDigit(r) || r == '_' || strings.ContainsRune("-./~:@", r) {
		return classWord
	}

	return classOther
}

// clampPoint keeps p inside the grid.
func clampPoint(g grid, p point) point {
	p.row = min(max(p.row, 0), max(g.Rows()-1, 0))
	p.col = min(max(p.col, 0), max(g.Cols()-1, 0))

	return p
}

// ordered returns the selection's endpoints in reading order.
func (s selection) ordered() (start, end point) {
	a, h := s.anchor, s.head
	if a.row > h.row || (a.row == h.row && a.col > h.col) {
		a, h = h, a
	}

	return a, h
}

// below returns the part of s on rows at or after row, in reading order. A start
// above row moves to row, at column 0 unless s is a block. It reports false when
// s ends above row.
func (s selection) below(row int) (selection, bool) {
	start, end := s.ordered()
	if end.row < row {
		return selection{}, false
	}

	if start.row < row {
		col := 0
		if s.mode == selBlock {
			col = start.col
		}

		start = point{row, col}
	}

	return selection{mode: s.mode, anchor: start, head: end}, true
}

// rowBounds returns the first and last selected row.
func (s selection) rowBounds() (first, last int) {
	return min(s.anchor.row, s.head.row), max(s.anchor.row, s.head.row)
}

// rowSpan returns the inclusive columns selected on row, within [0, cols-1].
func (s selection) rowSpan(row, cols int) (from, to int, ok bool) {
	if cols <= 0 {
		return 0, 0, false
	}

	first, last := s.rowBounds()
	if row < first || row > last {
		return 0, 0, false
	}

	switch s.mode {
	case selBlock:
		from = min(s.anchor.col, s.head.col)
		to = max(s.anchor.col, s.head.col)
	case selLine:
		from, to = 0, cols-1
	default:
		start, end := s.ordered()

		from, to = 0, cols-1
		if row == start.row {
			from = start.col
		}

		if row == end.row {
			to = end.col
		}
	}

	from = max(from, 0)

	to = min(to, cols-1)
	if from > to {
		return 0, 0, false
	}

	return from, to, true
}

// cellClass classifies the cell at (row, col); a wide char's continuation cell
// takes the class of the character before it.
func cellClass(g grid, row, col int) int {
	c := g.Cell(row, col)
	if c == nil {
		return classBlank
	}

	if isContinuation(g, row, col) {
		return cellClass(g, row, col-1)
	}

	return charClass(c.Content)
}

// isContinuation reports whether the cell is the right half of a wide char.
func isContinuation(g grid, row, col int) bool {
	c := g.Cell(row, col)
	if c == nil || c.Width != 0 || col == 0 {
		return false
	}

	prev := g.Cell(row, col-1)

	return prev != nil && prev.Width >= 2
}

// wordAt selects the word under p, joining rows that soft-wrap into each other.
func wordAt(g grid, p point) selection {
	p = clampPoint(g, p)

	sel := selection{mode: selStream, anchor: p, head: p}
	if g.Rows() == 0 || g.Cols() == 0 || cellClass(g, p.row, p.col) != classWord {
		return sel
	}

	start := p
	for {
		prev := start
		switch {
		case start.col > 0:
			prev.col--
		case start.row > 0 && g.Wrapped(start.row-1):
			prev = point{start.row - 1, g.Cols() - 1}
		default:
			prev = start
		}

		if prev == start || cellClass(g, prev.row, prev.col) != classWord {
			break
		}

		start = prev
	}

	end := p
	for {
		next := end
		switch {
		case end.col < g.Cols()-1:
			next.col++
		case end.row+1 < g.Rows() && g.Wrapped(end.row):
			next = point{end.row + 1, 0}
		}

		if next == end || cellClass(g, next.row, next.col) != classWord {
			break
		}

		end = next
	}

	sel.anchor, sel.head = start, end

	return sel
}

// lineAt selects the logical line containing p, across soft wraps.
func lineAt(g grid, p point) selection {
	p = clampPoint(g, p)

	first, last := p.row, p.row
	for first > 0 && g.Wrapped(first-1) {
		first--
	}

	for last+1 < g.Rows() && g.Wrapped(last) {
		last++
	}

	return selection{
		mode:   selLine,
		anchor: point{first, 0},
		head:   point{last, max(g.Cols()-1, 0)},
	}
}

// extract returns the selected text. Rows are clamped to the grid, so a
// selection that outlives trimmed history yields what remains.
func extract(g grid, s selection) string {
	rows, cols := g.Rows(), g.Cols()
	if rows == 0 || cols == 0 {
		return ""
	}

	first, last := s.rowBounds()
	first = max(first, 0)
	last = min(last, rows-1)

	var b strings.Builder

	for row := first; row <= last; row++ {
		from, to, ok := s.rowSpan(row, cols)

		var text string
		if ok {
			text = rowText(g, row, from, to)
		}

		joined := s.mode != selBlock && row < last && g.Wrapped(row)
		if !joined {
			text = strings.TrimRight(text, " ")
		}

		b.WriteString(text)

		if row < last && !joined {
			b.WriteByte('\n')
		}
	}

	return b.String()
}

// extractFor is extract, except that when claude is set it also joins the rows
// Claude Code wrapped itself (see claudeJoin), without the newline and without
// the indent and quote bar of the joined row. A block selection copies as extract.
func extractFor(g grid, s selection, claude bool) string {
	rows, cols := g.Rows(), g.Cols()
	if !claude || s.mode == selBlock || rows == 0 || cols == 0 {
		return extract(g, s)
	}

	first, last := s.rowBounds()
	first = max(first, 0)
	last = min(last, rows-1)

	if first > last {
		return ""
	}

	_, start := claudeLine(g, first)

	var (
		b      strings.Builder
		sep    string // written before the next row's text, if it has any
		minCol int    // the joined row's text starts here
	)

	for row := first; row <= last; row++ {
		from, to, ok := s.rowSpan(row, cols)
		from = max(from, minCol)

		var text string
		if ok && from <= to {
			text = rowText(g, row, from, to)
		}

		soft := row < last && g.Wrapped(row)
		join, cc := wrapKeep, 0

		if row < last && !soft {
			join, cc = claudeJoin(g, row, start)
		}

		trimmed := strings.TrimRight(text, " ")
		if trimmed != "" {
			b.WriteString(sep)
		}

		if soft {
			b.WriteString(text)
		} else {
			b.WriteString(trimmed)
		}

		sep, minCol = "", 0

		switch {
		case soft || row == last:
		case join == wrapKeep:
			b.WriteByte('\n')

			start = readClaudeRow(g, row+1).textStart()
		default:
			minCol = cc

			if join == wrapSpace {
				sep = " "
			}
		}
	}

	return b.String()
}

// rowText reads columns from..to of a row, emitting a wide char once even when
// only its continuation column is selected.
func rowText(g grid, row, from, to int) string {
	if ts, ok := g.(textScanner); ok && row < ts.historyRows() {
		return historyRowText(ts, row, from, to)
	}

	var b strings.Builder

	for col := from; col <= to; col++ {
		c := g.Cell(row, col)
		switch {
		case isContinuation(g, row, col):
			if col == from {
				b.WriteString(g.Cell(row, col-1).Content)
			}
		case c == nil || c.Content == "":
			b.WriteByte(' ')
		default:
			b.WriteString(c.Content)
		}
	}

	return b.String()
}

// historyRowText is rowText for a history row read as text cells.
func historyRowText(ts textScanner, row, from, to int) string {
	var b strings.Builder

	read := false

	ts.scanText(row, row+1, func(_ int, cells []term.TextCell) bool {
		read = true

		for col := from; col <= to; col++ {
			switch {
			case col >= len(cells):
				b.WriteByte(' ')
			case cells[col].Width == 0 && col > 0 && cells[col-1].Width >= 2:
				if col == from {
					b.Write(cells[col-1].Content)
				}
			case len(cells[col].Content) == 0:
				b.WriteByte(' ')
			default:
				b.Write(cells[col].Content)
			}
		}

		return false
	})

	if !read {
		return strings.Repeat(" ", max(to-from+1, 0))
	}

	return b.String()
}
