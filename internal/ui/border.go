package ui

import (
	"fmt"
	"slices"
	"strings"
	"unicode/utf8"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/mhersson/aim/internal/core"
)

// Who owns a box's colour: the focused pane's kind, or nobody (Border).
const (
	ownNone uint8 = iota
	ownAgent
	ownShell
)

// drawBox adds the rounded box around a pane rect: corners, sides, and the top
// and bottom borders with their title, tag and prompt segments set in. Every
// pane has its own box, so neighbours touch without shared cells.
func drawBox(add func(y int, s segment), r core.Rect, top, bottom []segment, own uint8, st styles) {
	if r.W < minFrame || r.H < minFrame {
		return
	}

	right := r.X + r.W - 1
	last := r.Y + r.H - 1

	line := func(x, w int, glyphs string) segment { return segment{x, w, st.borderRun(own, glyphs)} }

	// A border is runs of corner and dashes between its segments; the last run
	// ends in the other corner.
	border := func(y int, segs []segment, left, rightCorner string) {
		pos, prefix := r.X, left
		run := func(end int, tail string) {
			if end > pos {
				dashes := end - pos - utf8.RuneCountInString(prefix+tail)
				add(y, line(pos, end-pos, prefix+strings.Repeat("─", dashes)+tail))
			}
		}

		slices.SortFunc(segs, func(p, q segment) int { return p.x - q.x })

		for _, s := range segs {
			if s.x < max(pos, r.X+1) || s.x+s.w > right {
				continue
			}

			run(s.x, "")
			add(y, s)
			pos, prefix = s.x+s.w, ""
		}

		run(right+1, rightCorner)
	}

	border(r.Y, top, "╭", "╮")

	for y := r.Y + 1; y < last; y++ {
		add(y, line(r.X, 1, "│"))
		add(y, line(right, 1, "│"))
	}

	border(last, bottom, "╰", "╯")
}

func kindOwner(k core.PaneKind) uint8 {
	if k == core.KindAgent {
		return ownAgent
	}

	return ownShell
}

// titleRoomMin is the room, in cells, a title needs to be shown at all.
const titleRoomMin = 4

// topBorder returns the title and tags set into the top border of the pane at
// r: the title after the left corner and a dash, the tags before the right
// corner. The cells between them stay border. bright draws the title in the
// kind colour; focused alone decides the focus-only tags.
func (a *App) topBorder(s core.State, id core.PaneID, r core.Rect, focused, bright bool, st styles) []segment {
	meta := s.Panes[id]

	kindStyle, titleStyle := st.kind(meta.Kind), st.titleMuted
	if bright {
		titleStyle = kindStyle
	}

	// left is where the title must end.
	left := r.X + r.W - 1

	// rendered is the styled text of a tag drawn in several styles; otherwise style draws text.
	type tag struct {
		text     string
		style    lipgloss.Style
		rendered string
	}

	var copyTag, nextTag, scrollTag, zoomTag, badgeTag, modelTag *tag

	if meta.Kind == core.KindAgent {
		if text, style, ok := st.badge(meta.State, a.spin, a.icons); ok {
			badgeTag = &tag{text: text, style: style}
		}

		if model := cleanModel(meta.Model); model != "" {
			text, rendered := st.modelTag(model, meta.Context, meta.ContextKnown)
			modelTag = &tag{text: text, rendered: rendered}
		}
	}

	_, focusExists := s.Panes[s.Tab().Focus]
	if s.Tab().Zoomed && focusExists {
		text := " zoom "
		if a.icons {
			text = " " + iconZoom + " zoom "
		}

		zoomTag = &tag{text: text, style: st.tagZoom}
	}

	if p := a.panes[id]; p != nil {
		if n := p.linesAbove(); n > 0 {
			arrow := "\u2191"
			if a.icons {
				arrow = iconScroll
			}

			scrollTag = &tag{text: fmt.Sprintf(" %s %d ", arrow, n), style: st.tagScroll}
		}
	}

	if dir := s.Tab().Preselect; focused && dir != core.NoDir {
		nextTag = &tag{text: " " + a.preselectArrow(dir) + " next ", style: st.tagNext}
	}

	if a.copy != nil && a.copy.pane == id {
		text := " COPY "
		if a.icons {
			text = " " + iconCopy + " COPY "
		}

		copyTag = &tag{text: text, style: st.tagCopy}
	}

	title := "shell"
	if meta.Kind == core.KindAgent {
		title = a.harnessOf(meta).Program()
	}

	if p := a.panes[id]; p != nil {
		if t := printable(p.title()); t != "" {
			title = t
		}
	}

	lead := 2
	if a.icons {
		lead = 4
	}

	titleW := ansi.StringWidth(title)

	// titleFits reports whether the whole title is drawn when the tags take tagsW cells.
	titleFits := func(tagsW int) bool {
		end := r.X + r.W - 1
		if tagsW > 0 {
			end = r.X + r.W - 2 - tagsW
		}

		room := end - (r.X + 1) - lead - 1

		return room >= titleRoomMin && titleW <= room
	}

	// The tags fit between the corners with one border cell between two and one
	// before the right corner; COPY claims room first, then the badge, zoom,
	// preselection and scroll tags. The model tag comes last and only when every
	// other tag found room and the whole title still fits beside it: it never
	// costs the title any cells.
	avail := r.W - 3
	used := 0
	dropped := false
	claim := func(t *tag) *tag {
		if t == nil {
			return nil
		}

		need := lipgloss.Width(t.text)
		if used > 0 {
			need++
		}

		if used+need > avail {
			dropped = true

			return nil
		}

		used += need

		return t
	}

	copyTag, badgeTag, zoomTag, nextTag, scrollTag = claim(copyTag), claim(badgeTag), claim(zoomTag), claim(nextTag), claim(scrollTag)

	if modelTag != nil {
		need := lipgloss.Width(modelTag.text) + boolInt(used > 0)
		if dropped || !titleFits(used+need) {
			modelTag = nil
		}
	}

	modelTag = claim(modelTag)

	var segs []segment

	// Tags sit right to left with one border cell between them and before the corner.
	tagEnd := r.X + r.W - 2

	for _, t := range []*tag{badgeTag, modelTag, zoomTag, scrollTag, nextTag, copyTag} {
		if t == nil {
			continue
		}

		tw := lipgloss.Width(t.text)
		x := tagEnd - tw

		text := t.rendered
		if text == "" {
			text = t.style.Render(t.text)
		}

		segs = append(segs, segment{x, tw, text})
		left = x
		tagEnd = x - 1
	}

	room := left - (r.X + 1) - lead - 1
	if room < titleRoomMin {
		return segs
	}

	if titleW > room {
		title = ansi.Truncate(title, room, "…")
		titleW = ansi.StringWidth(title)
	}

	var sb strings.Builder
	sb.WriteString(st.body.Render(" "))

	if a.icons {
		sb.WriteString(kindStyle.Render(kindIcon(meta.Kind)))
		sb.WriteString(st.body.Render(" "))
	}

	sb.WriteString(titleStyle.Render(title))

	w := lead - 1 + titleW + 1

	sb.WriteString(st.body.Render(" "))

	return append(segs, segment{r.X + 2, w, sb.String()})
}

// searchChrome is the cells a search prompt takes besides its query: the
// blank, the mark and the cell under the cursor.
const searchChrome = 3

// searchPrompt is the open search prompt drawn into the bottom border of the
// pane at r: after the corner and its dash, a blank, the mark (/ or ?), the
// query and the cell under the cursor. cursorX is the cursor's column, -1 when
// the query is cut before it; ok is false when no prompt belongs to the pane or
// the border has no room for one.
func (a *App) searchPrompt(id core.PaneID, r core.Rect, st styles) (seg segment, cursorX int, ok bool) {
	p := a.prompt
	if p == nil || p.kind != promptSearch || p.pane != id {
		return segment{}, 0, false
	}

	// The corner and its dash take two cells, the dash before the other corner one.
	avail := r.W - 4
	if avail < searchChrome {
		return segment{}, 0, false
	}

	mark := "/"
	if p.backward {
		mark = "?"
	}

	text := ansi.Truncate(printable(p.input.Value()), avail-searchChrome, "")
	body := st.body.Render(" ") + st.tagCopy.Render(mark) + st.searchText.Render(text) + st.body.Render(" ")
	w := searchChrome + ansi.StringWidth(text)

	cursorX = -1

	before := ansi.StringWidth(printable(string([]rune(p.input.Value())[:p.input.Position()])))
	if before <= ansi.StringWidth(text) {
		cursorX = r.X + 4 + before
	}

	return segment{r.X + 2, w, body}, cursorX, true
}

// preselectArrow is the arrow of a preselected side.
func (a *App) preselectArrow(d core.Dir) string {
	arrows := map[core.Dir][2]string{
		core.Left:  {"\u2190", iconArrowLeft},
		core.Down:  {"\u2193", iconArrowDown},
		core.Up:    {"\u2191", iconArrowUp},
		core.Right: {"\u2192", iconArrowRight},
	}

	if a.icons {
		return arrows[d][1]
	}

	return arrows[d][0]
}

// bottomBorder is what is set into the bottom border of the pane at r: the
// search prompt or the hidden-column count on the left, and the count on the
// right. left and right are the hidden columns to count on each side; a count
// that does not fit is left out.
func (a *App) bottomBorder(id core.PaneID, r core.Rect, left, right int, st styles) []segment {
	var segs []segment

	// The last border cell before the corner is a dash.
	edge := r.X + r.W - 2
	end := r.X + 2

	if seg, _, ok := a.searchPrompt(id, r, st); ok {
		segs, end = append(segs, seg), seg.x+seg.w
	} else if left > 0 {
		text := fmt.Sprintf(" \u2039 %d ", left)
		if w := lipgloss.Width(text); end+w <= edge {
			segs, end = append(segs, segment{end, w, st.tagHidden.Render(text)}), end+w
		}
	}

	if right > 0 {
		text := fmt.Sprintf(" %d \u203a ", right)
		if w, x := lipgloss.Width(text), edge-lipgloss.Width(text); x >= end+1 && x >= r.X+2 {
			segs = append(segs, segment{x, w, st.tagHidden.Render(text)})
		}
	}

	return segs
}

func kindIcon(k core.PaneKind) string {
	if k == core.KindAgent {
		return iconAgent
	}

	return iconShell
}
