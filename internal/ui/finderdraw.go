package ui

import (
	"path/filepath"
	"strings"

	"github.com/charmbracelet/x/ansi"

	"github.com/mhersson/aim/internal/ui/palette"
)

// finderCell is one character of a row's label with how it is painted: the
// folder part of a result is muted and a character the query matched is a hit.
type finderCell struct {
	text       string
	width      int
	muted, hit bool
}

// finderCells lays out s, whose bytes start at off in the row's rel, as cells.
// It goes rune by rune through printable so the hits, which are bytes of rel,
// still line up with what is drawn.
func finderCells(s string, off int, hits map[int]bool, muted bool) []finderCell {
	cells := make([]finderCell, 0, len(s))

	for bi, r := range s {
		p := printable(string(r))
		if p == "" {
			continue
		}

		w := ansi.StringWidth(p)
		if w == 0 && len(cells) > 0 {
			cells[len(cells)-1].text += p

			continue
		}

		cells = append(cells, finderCell{text: p, width: w, muted: muted, hit: hits[off+bi]})
	}

	return cells
}

func cellsWidth(cells []finderCell) int {
	n := 0
	for _, c := range cells {
		n += c.width
	}

	return n
}

// finderLabel is the row's text in w cells. A result inside a folder reads its
// name, two spaces and the folder, dimmed, and is cut on the right; a plain
// name is cut on the left, so its end stays.
func finderLabel(r finderRow, w int) []finderCell {
	hits := make(map[int]bool, len(r.hits))
	for _, h := range r.hits {
		hits[h] = true
	}

	slash := ""
	if r.dir {
		slash = "/"
	}

	ellipsis := finderCell{text: "…", width: 1, muted: true}

	if bs := strings.LastIndexByte(r.rel, '/') + 1; r.kind == finderEntry && bs > 0 {
		cells := finderCells(r.rel[bs:], bs, hits, false)
		if slash != "" {
			cells = append(cells, finderCell{text: slash, width: 1})
		}

		cells = append(cells, finderCell{text: " ", width: 1}, finderCell{text: " ", width: 1})
		cells = append(cells, finderCells(r.rel[:bs-1], 0, hits, true)...)

		return cutCellsRight(cells, w, ellipsis)
	}

	cells := finderCells(r.rel, 0, hits, false)
	if slash != "" {
		cells = append(cells, finderCell{text: slash, width: 1})
	}

	if cellsWidth(cells) <= w {
		return cells
	}

	used, i := 1, len(cells)
	for i > 0 && used+cells[i-1].width <= w {
		i--
		used += cells[i].width
	}

	return append([]finderCell{ellipsis}, cells[i:]...)
}

// cutCellsRight keeps the cells that fit w, with the ellipsis last when some
// were cut.
func cutCellsRight(cells []finderCell, w int, ellipsis finderCell) []finderCell {
	if cellsWidth(cells) <= w {
		return cells
	}

	used, i := 1, 0
	for i < len(cells) && used+cells[i].width <= w {
		used += cells[i].width
		i++
	}

	return append(cells[:i:i], ellipsis)
}

// finderRowLine draws one list row w cells wide: a cell of margin, the icon
// when icons are on, the label.
func (a *App) finderRowLine(box palette.Box, r finderRow, selected bool, w int) string {
	ui := box.UI()
	base := box.On(ui.Fg)
	icon := base.Foreground(ui.Muted)
	name := base

	if selected {
		base = base.Background(ui.Selection)
		icon = base.Foreground(ui.Accent)
		name = base.Bold(true)
	}

	room := w - 2
	lead := ""

	if a.icons {
		glyph := iconMarkdown

		switch {
		case r.kind != finderEntry:
			glyph = iconArrowUp
		case r.dir:
			glyph = iconFolder
		}

		lead = icon.Render(glyph) + base.Render(" ")
		room -= 2
	}

	var sb strings.Builder

	sb.WriteString(base.Render(" ") + lead)

	cells := finderLabel(r, max(room, 0))
	for i := 0; i < len(cells); {
		j := i
		for j < len(cells) && cells[j].hit == cells[i].hit && cells[j].muted == cells[i].muted {
			j++
		}

		var run strings.Builder
		for _, c := range cells[i:j] {
			run.WriteString(c.text)
		}

		st := name

		switch {
		case cells[i].hit:
			st = base.Foreground(ui.Match).Bold(true)
		case cells[i].muted:
			st = base.Foreground(ui.Muted)
		}

		sb.WriteString(st.Render(run.String()))

		i = j
	}

	used := 1 + (w - 2 - room) + cellsWidth(cells)
	sb.WriteString(base.Render(strings.Repeat(" ", max(w-used, 0))))

	return sb.String()
}

// finderBody is the list's rows in w cells and h lines: the visible rows from
// the top, the message when there is one, blank lines after. It scrolls the
// cursor into view.
func (a *App) finderBody(box palette.Box, f *finderView, w, h int) []string {
	ui := box.UI()
	blank := box.On(ui.Fg).Render(strings.Repeat(" ", w))

	f.sel = max(min(f.sel, len(f.rows)-1), 0)
	if f.sel < f.top {
		f.top = f.sel
	}

	if f.sel >= f.top+h {
		f.top = f.sel - h + 1
	}

	f.top = max(min(f.top, len(f.rows)-h), 0)

	lines := make([]string, 0, h)
	for i := f.top; i < min(len(f.rows), f.top+h); i++ {
		lines = append(lines, a.finderRowLine(box, f.rows[i], i == f.sel, w))
	}

	if text, isErr := finderMessage(f); text != "" {
		fg := ui.Subtle
		if isErr {
			fg = ui.Error
		}

		// Under ".." when that is the only row, else at the top.
		at := len(f.rows) - f.top
		if at >= 0 && at < h {
			for len(lines) <= at {
				lines = append(lines, blank)
			}

			msg := ansi.Truncate(text, max(w-2, 0), "…")
			lines[at] = box.On(ui.Fg).Render(" ") + box.On(fg).Render(msg) +
				box.On(ui.Fg).Render(strings.Repeat(" ", max(w-1-ansi.StringWidth(msg), 0)))
		}
	}

	for len(lines) < h {
		lines = append(lines, blank)
	}

	return lines
}

// finderLines draws the finder in a box w x h: the folder in the title, the
// prompt, the list and, from finderPreviewMin columns, the preview column, the
// hints, and the count in the bottom border.
func (a *App) finderLines(lk look, w, h int) []string {
	f := a.finder
	box := palette.NewBox(lk.theme, w)
	ui := lk.theme.UI
	inner := box.Inner()
	panel := box.On(ui.Fg)
	body := h - 6
	home := a.getenv("HOME")

	split := a.width >= finderPreviewMin
	lw := inner

	if split {
		lw = finderListWidth(inner)
	}

	room := inner - 3
	if a.icons {
		room -= 2
	}

	title := a.titled(iconMarkdown, planCutStart(finderTitle(f.dir, home), room))

	in := f.input
	in.SetWidth(max(1, inner-4))

	lines := []string{box.Top(title), box.Row(panel.Render(" ")+in.View(), panel)}

	if split {
		lines = append(lines, box.DividerJoin(lw+1, "┬"))
	} else {
		lines = append(lines, box.Divider())
	}

	var pv []string
	if split {
		pv = a.finderPreviewLines(lk, box, f, inner-lw-1, body)
	}

	for i, l := range a.finderBody(box, f, lw, body) {
		if split {
			l += box.Bar() + pv[i]
		}

		lines = append(lines, box.Row(l, panel))
	}

	if split {
		lines = append(lines, box.DividerJoin(lw+1, "┴"))
	} else {
		lines = append(lines, box.Divider())
	}

	enter := "open"

	if r, ok := f.selected(); ok {
		switch {
		case r.kind == finderUp:
			enter = "go up"
		case r.kind == finderJump || r.dir:
			enter = "go in"
		}
	}

	canUp := f.input.Value() == "" && filepath.Dir(f.dir) != f.dir
	hints := box.Hints(finderHints(enter, canUp, inner))
	count := " " + finderCountText(f.sel, len(f.rows), f.countTail()) + " "

	return append(lines, box.Row(hints, panel), box.Bottom(count))
}

// countTail is what the walk adds to the count, shown while a search is typed.
func (f *finderView) countTail() string {
	if !finderSearches(f.query) {
		return ""
	}

	return f.walk.tail()
}

// overlayFinder draws the finder over base; a terminal too small for the box,
// or for a list row, draws nothing.
func (a *App) overlayFinder(f *frameLayers, base string, lk look) string {
	x, y, w, h, ok := planBox(a.width, a.height)
	if !ok || h < finderMinH {
		return base
	}

	return overlayAt(f, base, a.width, a.height, x, y, a.finderLines(lk, w, h), nil)
}
