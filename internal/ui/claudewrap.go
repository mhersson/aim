package ui

import (
	"slices"
	"strings"
	"unicode/utf8"

	"github.com/mhersson/aim/internal/term"
)

// claudeCell is one cell as the join rule reads it.
type claudeCell struct {
	content string
	width   int
}

// claudeRow is a whole row of cells. Every width the join rule compares is
// counted in cells, so a wide character counts as two columns.
type claudeRow []claudeCell

// claudeGlyphs mark a row that starts something new, whether or not a blank
// follows them.
const claudeGlyphs = "●⎿❯✻⏸─│╭╰┌└├"

// readClaudeRow returns exactly g.Cols() cells of row. A cell that is missing,
// or on a history row the scanner skipped as blank, reads as a blank cell.
func readClaudeRow(g grid, row int) claudeRow {
	cols := g.Cols()
	cells := make(claudeRow, cols)

	for i := range cells {
		cells[i] = claudeCell{width: 1}
	}

	if ts, ok := g.(textScanner); ok && row < ts.historyRows() {
		ts.scanText(row, row+1, func(_ int, tc []term.TextCell) bool {
			for i, c := range tc[:min(len(tc), cols)] {
				// Content aliases history storage and is valid only inside this callback.
				cells[i] = claudeCell{content: string(c.Content), width: c.Width}
			}

			return false
		})

		return cells
	}

	for col := range cells {
		if c := g.Cell(row, col); c != nil {
			cells[col] = claudeCell{content: c.Content, width: c.Width}
		}
	}

	return cells
}

// spacer reports whether cell i is the empty right half of a wide character.
func (r claudeRow) spacer(i int) bool {
	return i > 0 && r[i].width == 0 && r[i].content == "" && r[i-1].width >= 2
}

func (r claudeRow) blank(i int) bool {
	c := r[i].content

	return (c == "" || c == " ") && !r.spacer(i)
}

// blankAt is blank, and false past the row.
func (r claudeRow) blankAt(i int) bool {
	return i < len(r) && r.blank(i)
}

// skipBlanks returns the first column from i on that is not blank, or len(r).
func (r claudeRow) skipBlanks(i int) int {
	for r.blankAt(i) {
		i++
	}

	return i
}

// rightEdge is one past the last non-blank cell, and 0 for a blank row.
func (r claudeRow) rightEdge() int {
	for i := range slices.Backward(r) {
		if !r.blank(i) {
			return i + 1
		}
	}

	return 0
}

// wordWidth is the number of non-blank cells from column i on.
func (r claudeRow) wordWidth(i int) int {
	n := 0
	for i+n < len(r) && !r.blank(i+n) {
		n++
	}

	return n
}

// lastWordWidth is the width of the run of non-blank cells that ends at the
// right edge.
func (r claudeRow) lastWordWidth() int {
	end := r.rightEdge()

	start := end
	for start > 0 && !r.blank(start-1) {
		start--
	}

	return end - start
}

// gutter reports whether cell i is a quote's bar: a "▎" and a blank after it.
func (r claudeRow) gutter(i int) bool {
	return i < len(r) && r[i].content == "▎" && r.blankAt(i+1)
}

// marker returns how many cells the list or tool marker at column i spans, and
// 0 when there is none.
func (r claudeRow) marker(i int) int {
	if i >= len(r) {
		return 0
	}

	c := r[i]
	if first, _ := utf8.DecodeRuneInString(c.content); strings.ContainsRune(claudeGlyphs, first) {
		if c.width >= 2 && i+1 < len(r) && r.spacer(i+1) {
			return 2
		}

		return 1
	}

	switch c.content {
	case "-", "*", "•":
		if r.blankAt(i + 1) {
			return 1
		}

		return 0
	}

	return r.numberSpan(i)
}

// numberSpan returns the width of the "12." or "3)" that starts at column i and
// has a blank after it, or 0.
func (r claudeRow) numberSpan(i int) int {
	n := 0
	for i+n < len(r) && len(r[i+n].content) == 1 && r[i+n].content[0] >= '0' && r[i+n].content[0] <= '9' {
		n++
	}

	if n == 0 || i+n >= len(r) {
		return 0
	}

	if c := r[i+n].content; (c == "." || c == ")") && r.blankAt(i+n+1) {
		return n + 1
	}

	return 0
}

// textStart is the column where the row's text begins: after the leading
// blanks, a quote's bar and a list or tool marker, with the blanks after each.
// A blank row returns len(r).
func (r claudeRow) textStart() int {
	i := r.skipBlanks(0)
	if r.gutter(i) {
		i = r.skipBlanks(i + 1)
	}

	if m := r.marker(i); m > 0 {
		i = r.skipBlanks(i + m)
	}

	return i
}

// wrapJoin is what claudeJoin decides about the break between two rows.
type wrapJoin int

const (
	// wrapKeep keeps the newline.
	wrapKeep wrapJoin = iota
	// wrapTight joins the rows with nothing: the break falls inside a word.
	wrapTight
	// wrapSpace joins the rows with one space.
	wrapSpace
)

// claudeJoin decides whether Claude Code wrapped its own text between rows r and
// r+1, and returns the column where r+1's text starts. start is the text start
// of the line r belongs to. Claude Code draws every row with cursor
// positioning, so no row carries a soft-wrap flag: the rows say so themselves.
// The caller ensures r+1 is a row of g and r is not soft-wrapped.
func claudeJoin(g grid, r, start int) (wrapJoin, int) {
	up, lo := readClaudeRow(g, r), readClaudeRow(g, r+1)
	cols := g.Cols()
	cc := lo.skipBlanks(0)

	// A quote repeats its bar on every row.
	if up.gutter(up.skipBlanks(0)) && lo.gutter(cc) {
		cc = lo.skipBlanks(cc + 1)
	}

	edge, first := up.rightEdge(), lo.wordWidth(cc)

	// Claude Code wraps only at a word that does not fit. A blank row, a marker, an
	// indent past the line's text start or a first word that fits on r means a new line.
	if edge == 0 || lo.rightEdge() == 0 || lo.marker(cc) > 0 || cc > start || edge+1+first <= cols {
		return wrapKeep, cc
	}

	if edge == cols && up.lastWordWidth()+first > cols-cc {
		return wrapTight, cc
	}

	return wrapSpace, cc
}

// claudeLine returns the first row of row's line and that line's text start.
func claudeLine(g grid, row int) (first, start int) {
	// With the row width as start, no content column is past it, so the indent
	// test never ends the walk; only a row that starts a line does.
	k := row
	for k > 0 {
		if !g.Wrapped(k - 1) {
			if j, _ := claudeJoin(g, k-1, g.Cols()); j == wrapKeep {
				break
			}
		}

		k--
	}

	first, start = k, readClaudeRow(g, k).textStart()

	for r := k; r < row; r++ {
		if g.Wrapped(r) {
			continue
		}

		if j, _ := claudeJoin(g, r, start); j == wrapKeep {
			first, start = r+1, readClaudeRow(g, r+1).textStart()
		}
	}

	return first, start
}
