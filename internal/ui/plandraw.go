package ui

import (
	"image"
	"slices"
	"strconv"
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/mhersson/aim/internal/core"
	"github.com/mhersson/aim/internal/ui/theme"
)

const (
	// planMaxBox and planMaxView cap the box and the view inside it; the text is
	// four cells narrower than the view.
	planMaxBox  = 110
	planMaxView = 104
	// planMinBox is the narrowest box that is drawn.
	planMinBox = 24
	// planMinRows is how many doc rows the box keeps before its blank rows and
	// then its hint row go.
	planMinRows = 3
	// planHintGap is the cells between two hints.
	planHintGap = 4
)

// planBox is the plan overlay's box on a width x height terminal. ok is false
// for a terminal too small to draw it; the overlay then stays open unseen.
func planBox(width, height int) (x, y, w, h int, ok bool) {
	w, h = min(planMaxBox, width-4), height-4
	if w < planMinBox || h < 3 {
		return 0, 0, 0, 0, false
	}

	return (width - w) / 2, 2, w, h, true
}

// planRows splits a box of h rows: the doc rows, and whether the two blank rows
// and the hint row fit. The doc keeps planMinRows rows while it can: the blank
// rows go first, then the hint row.
func planRows(h int) (rows int, blanks, hint bool) {
	switch {
	case h-5 >= planMinRows:
		return h - 5, true, true
	case h-3 >= planMinRows:
		return h - 3, false, true
	}

	return max(h-2, 1), false, false
}

// planViewWidth is the width of the view in a box w cells wide.
func planViewWidth(w int) int { return min(w-4, planMaxView) }

// planTextWidth is the width the overlay lays a doc out at on a width x height
// terminal, 0 for a box too small to draw.
func planTextWidth(width, height int) int {
	_, _, w, _, ok := planBox(width, height)
	if !ok {
		return 0
	}

	return (&mdView{w: planViewWidth(w)}).textWidth()
}

// planDoc places the view in a box w x h, in the box's cells: the view's left
// cell and first row, its width and its rows.
func planDoc(w, h int) (x, y, vw, rows int) {
	rows, blanks, _ := planRows(h)
	vw = planViewWidth(w)
	x, y = 1+(w-2-vw)/2, 1

	if blanks {
		y++
	}

	return x, y, vw, rows
}

// planDocRect is the screen cells of the overlay's doc rows. ok is false for a
// terminal too small to draw the box.
func planDocRect(width, height int) (image.Rectangle, bool) {
	bx, by, w, h, ok := planBox(width, height)
	if !ok {
		return image.Rectangle{}, false
	}

	x, y, vw, rows := planDoc(w, h)

	return image.Rect(bx+x, by+y, bx+x+vw, by+y+rows), true
}

// planIndicator says where the view is in the doc.
func planIndicator(lines, rows, top int) string {
	switch {
	case lines <= rows:
		return " all "
	case top <= 0:
		return " top "
	case top >= lines-rows:
		return " end "
	}

	span := lines - rows

	return " " + strconv.Itoa((top*200+span)/(2*span)) + "% "
}

// planHints are the key hints that fit w cells, with the keys as kt binds them; the
// least needed drop first and close always stays. A pair with no key is left out.
func planHints(kt keyTable, w int) [][2]string {
	var pairs [][2]string

	for _, d := range []struct {
		label string
		ids   []string
	}{
		{"line", []string{"reader.line.next", "reader.line.prev"}},
		{"half page", []string{"reader.page.down", "reader.page.up"}},
		{"heading", []string{"reader.section.next", "reader.section.prev"}},
		{"search", []string{"reader.search"}},
		{"open", []string{"reader.open"}},
		{"close", []string{"reader.close"}},
	} {
		if k := kt.hint(d.ids...); k != "" {
			pairs = append(pairs, [2]string{k, d.label})
		}
	}

	width := func() int {
		n := planHintGap * (len(pairs) - 1)
		for _, p := range pairs {
			n += ansi.StringWidth(p[0]) + 1 + ansi.StringWidth(p[1])
		}

		return n
	}

	for _, drop := range []string{"search", "heading", "half page", "open"} {
		if width() > w {
			pairs = slices.DeleteFunc(pairs, func(p [2]string) bool { return p[1] == drop })
		}
	}

	return pairs
}

// planCutStart is s cut from its start with … to n cells.
func planCutStart(s string, n int) string {
	if w := ansi.StringWidth(s); w > n {
		return ansi.TruncateLeft(s, w-max(n-1, 0), "…")
	}

	return s
}

// planRewritten is the tag's age while a reload shows a rewrite.
const planRewritten = "rewritten"

// planTagStyled renders tag in muted, with a trailing "rewritten" in loud.
func planTagStyled(tag string, muted, loud lipgloss.Style) string {
	if head, ok := strings.CutSuffix(tag, planRewritten+" "); ok {
		return muted.Render(head) + loud.Render(planRewritten) + muted.Render(" ")
	}

	return muted.Render(tag)
}

// planTop fits the title and the tag into the top border of a box w cells
// wide. A path too long for its room is cut from its start; when that would
// leave it under 12 cells the tag drops and the path takes the room, at least
// 4 cells.
func planTop(lead, path, tag string, w int) (title, tagOut string) {
	room := w - 4
	fixed := ansi.StringWidth(lead) + 1
	avail := room - ansi.StringWidth(tag) - 1

	switch {
	case fixed+ansi.StringWidth(path) <= avail:
		return lead + path + " ", tag
	case avail-fixed >= 12:
		return lead + planCutStart(path, avail-fixed) + " ", tag
	}

	return ansi.Truncate(lead+planCutStart(path, max(room-fixed, 4))+" ", room, ""), ""
}

// planHead is what the top border holds before it is fitted: the lead, the
// path to shorten and the tag. A shown page replaces the path and drops the
// tag; a markdown file has no tag.
func (a *App) planHead(v *planView) (lead, path, tag string) {
	icon, name := iconPlan, v.agent
	if f := v.file; f != nil {
		icon, name, path = iconMarkdown, f.repo, f.path
	}

	lead = " " + name + " · "
	if a.icons {
		lead = " " + icon + " " + name + " · "
	}

	// A file outside every repository is titled by its path alone.
	if v.file != nil && v.file.repo == "" {
		lead = " "
		if a.icons {
			lead = " " + icon + " "
		}
	}

	if v.file == nil {
		tag = " " + planKindWord(v.plan.Kind)
		if v.rewritten {
			tag += " · " + planRewritten
		} else if age := railAge(v.plan.At, a.clock.Now()); age != "" {
			tag += " · " + age
		}

		tag += " "
		path = v.plan.Path
	}

	if n := len(v.pages); n > 0 {
		path, tag = v.pages[n-1].path, ""
	}

	return lead, path, tag
}

// titledTop is the top row of a box w cells wide with title at its left.
func titledTop(border, titleStyle lipgloss.Style, title string, w int) string {
	return border.Render("╭─") + titleStyle.Render(title) + border.Render(strings.Repeat("─", w-3-ansi.StringWidth(title))+"╮")
}

// labelledBottom is the bottom row of a box w cells wide with label at its right.
func labelledBottom(border, labelStyle lipgloss.Style, label string, w int) string {
	return border.Render("╰"+strings.Repeat("─", w-3-ansi.StringWidth(label))) + labelStyle.Render(label) + border.Render("─╯")
}

// planLines draws the box w x h: the title and tag in the top border, the view
// centred in the inner width, the hints and the indicator. pics are the view's
// picture rows in the box's cells, less any picture the link chooser overlaps.
func (a *App) planLines(lk look, w, h int) (out []string, pics []picRow) {
	v := a.plan
	ui := lk.theme.UI
	on := func(fg theme.Hex) lipgloss.Style { return lipgloss.NewStyle().Foreground(fg).Background(ui.Panel) }
	border := on(ui.BorderFocus)
	pad := func(n int) string { return on(ui.Fg).Render(strings.Repeat(" ", max(n, 0))) }

	rows, blanks, hint := planRows(h)
	inner := w - 2
	dx, _, vw, _ := planDoc(w, h)
	off := dx - 1

	row := func(content string, cw int) string {
		return border.Render("│") + pad(off) + content + pad(inner-off-cw) + border.Render("│")
	}

	lead, path, tag := a.planHead(v)

	main := v.root
	if v.file != nil && v.file.repo == "" {
		main = ""
	}

	title, tag := planTop(lead, shortPath(path, main, a.getenv("HOME")), tag, w)
	titleStyle := on(ui.Info).Bold(true)

	var top string
	if tag != "" {
		top = border.Render("╭─") + titleStyle.Render(title) + border.Render(strings.Repeat("─", w-4-ansi.StringWidth(title)-ansi.StringWidth(tag))) + planTagStyled(tag, on(ui.Muted), on(ui.Accent).Bold(true)) + border.Render("─╮")
	} else {
		top = titledTop(border, titleStyle, title, w)
	}

	a.sizePlan()

	shown := v.shown()
	ind := planIndicator(len(shown.layout().Lines), rows, shown.top)
	bottom := labelledBottom(border, on(ui.Accent).Bold(true), ind, w)

	out = []string{top}
	if blanks {
		out = append(out, row("", 0))
	}

	drawn, viewPics := shown.drawPictures(newMDPainter(lk.theme, ui.Panel).withQuery(v.query), true)
	for _, l := range drawn {
		out = append(out, row(l, vw))
	}

	for _, p := range viewPics {
		p.X += 1 + off
		p.Y += len(out) - len(drawn)
		pics = append(pics, p)
	}

	if blanks {
		out = append(out, row("", 0))
	}

	if hint {
		hints := a.planHintRow(ui, vw-4)
		out = append(out, row(pad(2)+hints, 2+ansi.StringWidth(hints)))
	}

	out = append(out, bottom)

	if v.chooser != nil {
		if covered := a.chooserLines(v.chooser, ui, out); covered.Dx() > 0 {
			pics = dropCovered(pics, []image.Rectangle{covered})
		}
	}

	return out, pics
}

// chooserLines draws the link chooser c over the box's rows, two cells in from
// each side from the fourth row; a box without a row for it draws nothing. It
// returns the cells the box covers.
func (a *App) chooserLines(c *linkChooser, ui theme.UI, lines []string) image.Rectangle {
	box := c.lines(ui, ansi.StringWidth(lines[0])-2*wtInsetX, len(lines)-1-wtTopY, a.icons, a.getenv("HOME"))
	if box == nil {
		return image.Rectangle{}
	}

	gap := lipgloss.NewStyle().Background(ui.Panel).Render(" ")
	side := lipgloss.NewStyle().Foreground(ui.BorderFocus).Background(ui.Panel).Render("│")

	for i, l := range box {
		y := wtTopY + i
		lines[y] = cutRow(lines[y], wtInsetX) + l + gap + side
	}

	return image.Rect(wtInsetX, wtTopY, ansi.StringWidth(lines[0]), wtTopY+len(box))
}

// planHintRow is the hints that fit w cells, a gap apart.
func (a *App) planHintRow(ui theme.UI, w int) string {
	if a.plan.searching {
		return a.planPromptRow(ui, w)
	}

	pairs := planHints(a.keys.reader, w)
	for i := range pairs {
		if pairs[i][1] == "close" && a.finder != nil {
			pairs[i][1] = "back"
		}
	}

	return hintRow(ui, pairs, w)
}

// hintRow is the pairs of key and label that fit w cells, a gap apart.
func hintRow(ui theme.UI, pairs [][2]string, w int) string {
	on := func(fg theme.Hex) lipgloss.Style { return lipgloss.NewStyle().Foreground(fg).Background(ui.Panel) }

	var b strings.Builder

	for i, p := range pairs {
		if i > 0 {
			b.WriteString(on(ui.Fg).Render(strings.Repeat(" ", planHintGap)))
		}

		b.WriteString(on(ui.Key).Bold(true).Render(p[0]) + on(ui.Muted).Render(" "+p[1]))
	}

	return ansi.Truncate(b.String(), max(w, 0), "")
}

// planPromptRow is the search prompt in w cells: the query cut from its start
// to leave the caret in view, then the prompt's hints when they fit.
func (a *App) planPromptRow(ui theme.UI, w int) string {
	return searchPromptRow(ui, w, a.plan.query)
}

// searchPromptRow is the prompt for query in w cells.
func searchPromptRow(ui theme.UI, w int, query string) string {
	on := func(fg theme.Hex) lipgloss.Style { return lipgloss.NewStyle().Foreground(fg).Background(ui.Panel) }
	gap := on(ui.Fg).Render(strings.Repeat(" ", planHintGap))
	hints := on(ui.Key).Bold(true).Render("enter") + on(ui.Muted).Render(" find") + gap +
		on(ui.Key).Bold(true).Render("esc") + on(ui.Muted).Render(" cancel")

	q, _ := cleanText(query)
	room := w - 3 // "/ " and the caret

	if tail := planHintGap + ansi.StringWidth(ansi.Strip(hints)); room-tail >= 8 {
		room -= tail
	} else {
		hints = ""
	}

	if qw := ansi.StringWidth(q); qw > room {
		q = ansi.TruncateLeft(q, qw-max(room, 0), "")
	}

	row := on(ui.Info).Bold(true).Render("/ ") + on(ui.Fg).Render(q+"▏")
	if hints != "" {
		row += gap + hints
	}

	return ansi.Truncate(row, max(w, 0), "")
}

func planKindWord(k core.PlanKind) string {
	if k == core.PlanFile {
		return "plan file"
	}

	return "plan mode"
}

// overlayPlan draws the plan overlay over base; a terminal too small for the
// box draws nothing.
func (a *App) overlayPlan(f *frameLayers, base string, lk look) string {
	x, y, w, h, ok := planBox(a.width, a.height)
	if !ok {
		return base
	}

	lines, pics := a.planLines(lk, w, h)

	return overlayAt(f, base, a.width, a.height, x, y, lines, pics)
}
