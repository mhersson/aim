package ui

import (
	"fmt"
	"path/filepath"
	"slices"
	"strconv"
	"strings"

	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/mhersson/aim/internal/git"
	"github.com/mhersson/aim/internal/ui/palette"
	"github.com/mhersson/aim/internal/ui/theme"
)

// agentLabels names the harnesses the picker offers, in the chooser's order.
func agentLabels() []struct{ id, label string } {
	return []struct{ id, label string }{{"claude-code", "Claude Code"}, {"codex", "Codex"}}
}

func agentLabel(id string) string {
	for _, l := range agentLabels() {
		if l.id == id {
			return l.label
		}
	}

	return id
}

// pickerBox is the box's left column, top row and width; ok is false when the
// terminal is too small to draw it.
func (a *App) pickerBox() (left, top, w int, ok bool) {
	w = min(maxOverlayWidth, a.width-4)
	if w < 2 || a.height < 2 {
		return 0, 0, 0, false
	}

	return (a.width - w) / 2, a.height / 4, w, true
}

// overlayPicker draws the picker over base, centred with its top at a quarter
// of the height and clipped to the body rows.
func (a *App) overlayPicker(f *frameLayers, base string, lk look) string {
	_, top, w, ok := a.pickerBox()
	if !ok {
		return base
	}

	p := a.picker.open
	pt := palette.NewBox(lk.theme, w)

	var lines []string

	switch p.page {
	case pageAgent:
		fill := pt.On(pt.UI().Fg)
		lines = []string{pt.Top(a.titled(iconAgent, "New agent"))}

		for _, choice := range agentLabels() {
			row := fill
			if choice.id == p.agent {
				row = row.Background(pt.UI().Selection).Bold(true)
			}

			lines = append(lines, pt.Row(row.Render(" "+choice.label), row))
		}

		lines = append(lines, pt.Divider(), pt.Row(pt.Hints([][2]string{{"up/down", "move"}, {"enter", "select"}, {"esc", "close"}}), fill), pt.Bottom(""))
	case pageWorktrees:
		lines = a.pickerListLines(p, pt)
	case pageNewWorktree:
		lines = a.pickerFormLines(p, pt)
	}

	return overlayLines(f, base, a.width, a.height, top, lines, w, nil)
}

// pickerCursor places the terminal's cursor in the field that has the keyboard;
// nil when the box is clipped away.
func (a *App) pickerCursor() *tea.Cursor {
	p := a.picker.open

	if p.page == pageAgent || p.page == pageWorktrees && !p.filtering {
		return nil
	}

	left, top, w, ok := a.pickerBox()
	if !ok {
		return nil
	}

	inner := w - 2
	row, col := 1, 0

	if p.page == pageNewWorktree {
		in := p.form.branch
		if p.form.onBase {
			in, row = p.form.base, 2
		}

		_, c := fieldView(in, max(inner-formValueAt-1, 0), lipgloss.NewStyle())
		col = formValueAt + c
	} else {
		// A cell and the prompt come first.
		_, c := fieldView(p.query, inner-4, lipgloss.NewStyle())
		col = 3 + c
	}

	if top+row >= a.height-1 {
		return nil
	}

	return tea.NewCursor(left+1+col, top+row)
}

// fieldView draws an input's text in at most w cells, scrolled left so the
// cursor's cell stays inside, and returns the cursor's column in it. The text
// is drawn without control and invisible characters, a default branch from git
// among it, while the input keeps its value; the cursor stands after the visible
// characters before it, so it does not move over a hidden one.
func fieldView(in textinput.Model, w int, style lipgloss.Style) (string, int) {
	raw := []rune(in.Value())
	pos := min(in.Position(), len(raw))

	runes := []rune(printable(string(raw)))
	pos = min(len([]rune(printable(string(raw[:pos])))), len(runes))
	start := 0

	for start < pos && drawnWidth(string(runes[start:pos])) >= w {
		start++
	}

	return style.Render(ansi.Truncate(string(runes[start:]), max(w, 0), "")), drawnWidth(string(runes[start:pos]))
}

// pickerSeg is a run of a row: text and its style.
type pickerSeg struct {
	text  string
	style lipgloss.Style
}

func segsWidth(segs []pickerSeg) int {
	w := 0

	for i, s := range segs {
		if i > 0 {
			w += 2
		}

		w += lipgloss.Width(s.text)
	}

	return w
}

// renderSegs joins segs two cells apart, the gap in gap's style.
func renderSegs(segs []pickerSeg, gap lipgloss.Style) string {
	var sb strings.Builder

	for i, s := range segs {
		if i > 0 {
			sb.WriteString(gap.Render("  "))
		}

		sb.WriteString(s.style.Render(s.text))
	}

	return sb.String()
}

func (a *App) pickerListLines(p *picker, pt palette.Box) []string {
	ui := pt.UI()
	fill := pt.On(ui.Fg)
	rows := len(p.matches) + 1
	counts := a.agentCounts()

	query, _ := fieldView(p.query, pt.Inner()-4, fill)
	lines := []string{
		pt.Top(a.titled(iconAgent, "New agent · "+agentLabel(p.agent))),
		pt.Row(fill.Render(" ")+pt.On(ui.Accent).Render("❯ ")+query, fill),
		pt.Divider(),
	}

	for i := p.top; i < min(rows, p.top+palette.MaxRows); i++ {
		lines = append(lines, a.pickerRow(p, pt, counts, i))
	}

	return append(lines,
		pt.Divider(),
		pt.Row(pt.Hints([][2]string{{"j/k", "move"}, {"/", "filter"}, {"tab", "agent"}, {"enter", "start"}}), fill),
		pt.Bottom(" "+strconv.Itoa(p.sel+1)+" of "+strconv.Itoa(rows)+" "),
	)
}

// pickerRow draws row i: a worktree, or the new worktree row after the last.
func (a *App) pickerRow(p *picker, pt palette.Box, counts map[string]int, i int) string {
	ui := pt.UI()
	bg, selected := ui.Panel, i == p.sel

	if selected {
		bg = ui.Selection
	}

	on := func(fg theme.Hex) lipgloss.Style { return lipgloss.NewStyle().Background(bg).Foreground(fg) }
	base := on(ui.Fg)
	title := base

	if selected {
		title = base.Bold(true)
	}

	iconColour := ui.Muted
	if selected {
		iconColour = ui.Accent
	}

	if i == len(p.matches) {
		label, lead := "+ new worktree…", ""
		if a.icons {
			label, lead = "new worktree…", on(iconColour).Render(iconNewWorktree)+base.Render(" ")
		}

		return pt.Row(base.Render(" ")+lead+title.Render(label), base)
	}

	m := p.matches[i]
	t := p.trees[m.tree]
	avail := pt.Inner() - 2 // a cell of margin on each side
	lead := ""

	if a.icons {
		colour := ui.Git
		if selected {
			colour = ui.Accent
		}

		lead = on(colour).Render(iconGit) + base.Render(" ")
		avail -= 2
	}

	var marks []pickerSeg

	if n := counts[filepath.Clean(t.wt.Path)]; n > 0 {
		text := "● " + strconv.Itoa(n) + " agent"
		if n > 1 {
			text += "s"
		}

		marks = append(marks, pickerSeg{text, on(ui.Agent)})
	}

	if t.wt.Locked {
		marks = append(marks, pickerSeg{"locked", on(ui.Warning)})
	}

	if t.wt.Prunable {
		marks = append(marks, pickerSeg{"prunable", on(ui.Subtle)})
	}

	// The path goes first when the row is too narrow, then the label is cut.
	right := append(slices.Clone(marks), pickerSeg{t.short, on(ui.Muted)})
	if lipgloss.Width(t.label)+2+segsWidth(right) > avail {
		right = marks
	}

	room := avail
	if len(right) > 0 {
		room = max(avail-segsWidth(right)-2, 1)
	}

	branch, used := pickerLabel(t.label, room, m.hit, title, on(ui.Match).Bold(true))
	content := base.Render(" ") + lead + branch

	if len(right) > 0 {
		content += base.Render(strings.Repeat(" ", max(avail-used-segsWidth(right), 0))) + renderSegs(right, base)
	}

	return pt.Row(content, base)
}

// pickerLabel draws text in room cells, cut with an ellipsis, with the runes at
// the byte offsets in hit emphasised; it returns the cells used.
func pickerLabel(text string, room int, hit map[int]bool, normal, match lipgloss.Style) (string, int) {
	cut := lipgloss.Width(text) > room
	limit := room

	if cut {
		limit = max(room-1, 0)
	}

	var sb, run strings.Builder

	used, runHit := 0, false
	flush := func() {
		if run.Len() == 0 {
			return
		}

		if runHit {
			sb.WriteString(match.Render(run.String()))
		} else {
			sb.WriteString(normal.Render(run.String()))
		}

		run.Reset()
	}

	for bi, r := range text {
		w := lipgloss.Width(string(r))
		if used+w > limit {
			break
		}

		used += w

		if hit[bi] != runHit {
			flush()

			runHit = hit[bi]
		}

		run.WriteRune(r)
	}

	flush()

	if cut && room > 0 {
		sb.WriteString(normal.Render("…"))

		used++
	}

	return sb.String(), used
}

func (a *App) pickerFormLines(p *picker, pt palette.Box) []string {
	ui, f := pt.UI(), p.form
	fill := pt.On(ui.Fg)
	valueW := max(pt.Inner()-formValueAt-1, 0)

	field := func(label, text string, focused bool) string {
		marker := fill.Render("  ")
		if focused {
			marker = pt.On(ui.Accent).Render("❯") + fill.Render(" ")
		}

		return pt.Row(fill.Render(" ")+marker+pt.On(ui.Muted).Render(fmt.Sprintf("%-*s", formLabelW, label))+fill.Render("  ")+text, fill)
	}

	branch, _ := fieldView(f.branch, valueW, fill)
	base, _ := fieldView(f.base, valueW, fill)

	var path string

	if name := strings.TrimSpace(f.branch.Value()); name == "" {
		path = pt.On(ui.Subtle).Render("(from the branch name)")
	} else if dest, err := git.WorktreePath(p.repo, a.rootStyle(), name); err != nil {
		path = pt.On(ui.Error).Render(worktreeReason(err))
	} else {
		path = pt.On(ui.Muted).Render(shortPath(dest, p.main, p.home))
	}

	var status string

	switch {
	case f.busy:
		status = pt.On(ui.Muted).Render("creating the worktree…")
	case f.err != "":
		status = pt.On(ui.Error).Render(ansi.Truncate(f.err, max(pt.Inner()-2, 0), "…")) // a cell of margin on each side
	}

	lines := []string{
		pt.Top(a.titled(iconNewWorktree, "New worktree")),
		field("branch", branch, !f.onBase),
		field("base", base, f.onBase),
		field("path", path, false),
		pt.Row(fill.Render(" ")+status, fill),
	}

	for _, t := range f.tools {
		lead, room := "", pt.Inner()-2 // a cell of margin on each side
		if a.icons {
			lead, room = pt.On(ui.Warning).Render(iconAlert)+fill.Render(" "), room-2
		}

		text := ansi.Truncate(t.Name+": "+t.Hint, max(room, 0), "…")
		lines = append(lines, pt.Row(fill.Render(" ")+lead+pt.On(ui.Warning).Render(text), fill))
	}

	return append(lines,
		pt.Divider(),
		pt.Row(pt.Hints([][2]string{{"tab", "next field"}, {"enter", "create"}, {"esc", "back"}}), fill),
		pt.Bottom(""),
	)
}
