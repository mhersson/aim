package ui

import (
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/mhersson/aim/internal/core"
)

// maxConfirmWidth caps the confirm box width.
const maxConfirmWidth = 46

// confirm asks a yes/no question. It owns every key while open. action names
// the answer and titles the box, prompt is the question and detail the line
// under it. pane and tab are what the question is about, or empty; the confirm
// closes when that pane or tab does.
type confirm struct {
	action, prompt, detail string
	pane                   core.PaneID
	tab                    core.TabID
	yes                    func(a *App) tea.Cmd
	// push, when set, makes this the push confirm: it draws pushBox and takes only
	// enter and esc.
	push *pushTarget
	// approve, when set, makes this the approve confirm: it draws approveBox and
	// takes only enter and esc.
	approve *approveTarget
	// ready, when set, makes this the ready confirm: it draws readyBox and takes
	// only enter and esc.
	ready *readyTarget
	// cleanup, when set, makes this the cleanup confirm: it draws cleanupBox and takes
	// w, b, enter and esc.
	cleanup *cleanupTarget
}

// openConfirm shows c and ends a pending leader sequence.
func (a *App) openConfirm(c confirm) {
	a.endLeader()
	a.confirm = &c
}

// handleConfirmKey answers the open confirm; any other key is swallowed.
func (a *App) handleConfirmKey(msg tea.KeyPressMsg) tea.Cmd {
	c := a.confirm

	key := msg.String()

	if c.cleanup != nil && c.cleanup.toggle(key) {
		return nil
	}

	if (c.push != nil || c.approve != nil || c.ready != nil || c.cleanup != nil) && key != "enter" && key != "esc" {
		return nil
	}

	switch key {
	case "y", "enter":
		a.confirm = nil

		return c.yes(a)
	case "n", "esc":
		a.confirm = nil
	}

	return nil
}

// boxStyle is what differs between the confirm boxes: the border and title
// styles, and the marker that ends a row that does not fit.
type boxStyle struct {
	border, title lipgloss.Style
	cut           string
}

// boxFrame draws the frame of a confirm box w cells wide: the top border with
// title set into it two cells from the corner, the row function that indents its
// content two cells from the border and fills to the edge, and the bottom border.
func boxFrame(w int, title string, bs boxStyle, st styles) (top string, row func(content string) string, bottom string) {
	inner := w - 2

	row = func(content string) string {
		content = ansi.Truncate(st.popup.Render("  ")+content, inner, bs.cut)
		fill := st.popup.Render(strings.Repeat(" ", inner-ansi.StringWidth(content)))

		return bs.border.Render("│") + content + fill + bs.border.Render("│")
	}

	// The title starts two cells from the corner: the corner, one dash, then the title.
	title = ansi.Truncate(title, max(inner-1, 0), "")
	top = bs.border.Render("╭─") + bs.title.Render(title) +
		bs.border.Render(strings.Repeat("─", max(inner-1-ansi.StringWidth(title), 0))+"╮")
	bottom = bs.border.Render("╰" + strings.Repeat("─", inner) + "╯")

	return top, row, bottom
}

// confirmBox draws the box w cells wide: a rounded border in Error with the
// action set into the top edge, the question, its detail and the answer keys.
func confirmBox(c confirm, w int, icons bool, st styles) []string {
	title := " " + c.action + " "
	if icons {
		title = " " + iconAlert + " " + c.action + " "
	}

	top, row, bottom := boxFrame(w, title, boxStyle{border: st.confirmBorder, title: st.confirmTitle}, st)

	answer := func(key, label string) string {
		return st.popupSubtle.Render("[") + st.confirmKey.Render(key) + st.popupSubtle.Render("]") +
			st.popup.Render(" "+label)
	}

	return []string{
		top,
		row(""),
		row(st.confirmQuestion.Render(c.prompt)),
		row(st.popupMuted.Render(c.detail)),
		row(""),
		row(answer("y", c.action) + st.popup.Render("    ") + answer("n", "Cancel")),
		bottom,
	}
}

// overlayConfirm draws the confirm box over base, centred with its top at a
// third of the height, clipped to the terminal.
func overlayConfirm(f *frameLayers, base string, width, height int, c confirm, icons bool, st styles) string {
	maxW := maxConfirmWidth

	switch {
	case c.push != nil:
		maxW = maxPushWidth
	case c.approve != nil, c.ready != nil:
		maxW = maxApproveWidth
	case c.cleanup != nil:
		maxW = maxCleanupWidth
	}

	w := min(maxW, width-2)
	if w < 4 {
		return base
	}

	var lines []string

	switch {
	case c.push != nil:
		lines = pushBox(*c.push, w, icons, st)
	case c.approve != nil:
		lines = approveBox(*c.approve, w, icons, st)
	case c.ready != nil:
		lines = readyBox(*c.ready, w, icons, st)
	case c.cleanup != nil:
		top := max(1, height/3)

		return overlayLines(f, base, width, height, top, cleanupBox(*c.cleanup, w, max(height-1-top, 0), icons, st), w, nil)
	default:
		lines = confirmBox(c, w, icons, st)
	}

	return overlayLines(f, base, width, height, max(1, height/3), lines, w, nil)
}
