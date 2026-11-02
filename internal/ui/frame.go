package ui

import (
	"slices"
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/mhersson/aim/internal/core"
	"github.com/mhersson/aim/internal/git/invisible"
	"github.com/mhersson/aim/internal/ui/theme"
)

// bodyRect is the area under the rule on row 1, down to the last row.
func bodyRect(width, height int) core.Rect {
	return core.Rect{Y: 2, W: width, H: max(height-2, 0)}
}

// minFrame is the smallest pane rect, in rows and columns: a frame around one
// content cell.
const minFrame = 3

// layoutBody is the rect panes are laid out in: the body itself, or the zero
// rect when it is under minFrame square and stays blank.
func layoutBody(body core.Rect) core.Rect {
	if body.W < minFrame || body.H < minFrame {
		return core.Rect{}
	}

	return body
}

// contentRect is a pane rect inside its frame: inset by one cell on every side.
func contentRect(r core.Rect) core.Rect {
	return core.Rect{X: r.X + 1, Y: r.Y + 1, W: max(r.W-2, 0), H: max(r.H-2, 0)}
}

// geometry is the pane rects (frame included) of one View, computed once.
type geometry struct {
	rects map[core.PaneID]core.Rect
	// hiddenLeft and hiddenRight are the scrolling columns off screen on each side.
	hiddenLeft, hiddenRight int
}

// layoutRects returns each visible pane's rect. A zoomed pane is the only one
// and fills the body.
func (a *App) layoutRects() map[core.PaneID]core.Rect {
	return a.geometry().rects
}

func (a *App) geometry() geometry {
	st := a.store.State()

	arr := a.arrangeIn(st, st.Active)

	return geometry{rects: arr.Rects, hiddenLeft: arr.HiddenLeft, hiddenRight: arr.HiddenRight}
}

// tabRects lays tab i out in the body with its own zoom: a zoomed tab's
// focused pane gets the whole body.
func (a *App) tabRects(i int) map[core.PaneID]core.Rect {
	return a.layoutIn(a.store.State(), i)
}

func (a *App) layoutIn(st core.State, i int) map[core.PaneID]core.Rect {
	return a.arrangeIn(st, i).Rects
}

// arrangeIn is tab i's drawn rects and hidden-column counts; a zoomed tab has
// its one pane and no hidden columns.
func (a *App) arrangeIn(st core.State, i int) core.Arrangement {
	if i < 0 || i >= len(st.Tabs) {
		return core.Arrangement{}
	}

	if z, ok := a.zoomedRects(st, i); ok {
		return core.Arrangement{Rects: z}
	}

	return core.Arrange(st.Tabs[i], a.bodyAreaIn(st.Rail), st.Layout)
}

// sizesIn is the pane rects of tab i whose content is each PTY's size (the
// caller insets them), or the zoomed pane's alone when the tab is zoomed.
func (a *App) sizesIn(st core.State, i int) map[core.PaneID]core.Rect {
	if z, ok := a.zoomedRects(st, i); ok {
		return z
	}

	return core.Arrange(st.Tabs[i], a.bodyAreaIn(st.Rail), st.Layout).Sizes
}

// zoomedRects is the one rect of a zoomed tab: its focused pane fills the
// body.
func (a *App) zoomedRects(st core.State, i int) (map[core.PaneID]core.Rect, bool) {
	t := st.Tabs[i]
	if !t.Zoomed {
		return nil, false
	}

	if _, ok := st.Panes[t.Focus]; !ok {
		return nil, false
	}

	rects := map[core.PaneID]core.Rect{}
	if body := a.bodyAreaIn(st.Rail); body.W >= minFrame && body.H >= minFrame {
		rects[t.Focus] = body
	}

	return rects, true
}

// bodyArea is the rect panes are laid out in: the body less the rail's columns
// while the rail shows.
func (a *App) bodyArea() core.Rect { return a.bodyAreaIn(a.store.State().Rail) }

func (a *App) bodyAreaIn(rail bool) core.Rect {
	body := bodyRect(a.width, a.height)
	if a.railShows(rail) {
		w := railWidthFor(body)
		body.X, body.W = w, body.W-w
	}

	return layoutBody(body)
}

// segment is a run of cells already styled, starting at column x.
type segment struct {
	x, w int
	text string
}

// frameAt draws the whole screen: the workspace bar, the rule and the body,
// exactly height lines of exactly width cells. Height 1 is the workspace bar
// only and height 2 adds the rule. An empty tab shows the welcome card.
func (a *App) frameAt(g geometry) string {
	if a.width <= 0 || a.height <= 0 {
		return ""
	}

	lk := a.current()
	st := a.store.State()
	lines := make([]string, 0, a.height)

	lines = append(lines, a.workspaceBar(a.width))

	if a.height >= 2 {
		lines = append(lines, lk.styles.border.Render(strings.Repeat("─", a.width)))
	}

	if bodyH := a.height - 2; bodyH > 0 {
		rail, railW := a.railFrame(st, lk)

		if st.Tab().Empty() {
			lines = append(lines, a.welcomeBody(bodyH, lk, rail, railW)...)
		} else {
			lines = append(lines, a.frameBody(g, st, lk, rail, railW)...)
		}
	}

	return strings.Join(lines, "\n")
}

// welcomeBody is the welcome card centred in the body to the right of the rail.
func (a *App) welcomeBody(bodyH int, lk look, rail []string, railW int) []string {
	w := a.width - railW

	lines := strings.Split(renderBody(w, bodyH, lk.styles, lk.sample, a.welcomeHints()), "\n")
	for i := range min(len(rail), len(lines)) {
		lines[i] = rail[i] + lines[i]
	}

	return lines
}

// frameBody draws the body rows under the rule: the rail, then each pane's box,
// title and content.
func (a *App) frameBody(g geometry, st core.State, lk look, rail []string, railW int) []string {
	rows := make([][]segment, a.height)
	add := func(y int, s segment) {
		if y >= 0 && y < len(rows) && s.w > 0 {
			rows[y] = append(rows[y], s)
		}
	}

	for i, l := range rail {
		add(2+i, segment{0, railW, l})
	}

	focus := st.Tab().Focus
	leftPane, rightPane := edgePanes(g)
	now := a.clock.Now()
	// The focused pane draws as unfocused while the rail or the drawer has the
	// keyboard, so one element shows it.
	railKeys := (a.rail.focused && a.railShows(st.Rail)) || a.drawerHasKeys()

	for id, r := range g.rects {
		own := ownNone
		if id == focus && !railKeys {
			own = kindOwner(st.Panes[id].Kind)
		}

		var hl, hr int
		if id == leftPane {
			hl = g.hiddenLeft
		}

		if id == rightPane {
			hr = g.hiddenRight
		}

		styles := lk.styles
		if id == a.rail.flashPane && now.Before(a.rail.flashUntil) {
			styles = flashStyles(styles, lk.theme.UI.Match)
		}

		drawBox(add, r, a.topBorder(st, id, r, id == focus, id == focus && !railKeys, styles), a.bottomBorder(id, r, hl, hr, styles), own, styles)
	}

	for id, r := range g.rects {
		c := contentRect(r)

		var lines []string

		fit := false

		if p := a.panes[id]; p != nil {
			lines = a.withSelection(p)
			cols, prows := p.size()
			fit = cols != c.W || prows != c.H
		}

		for i := range c.H {
			text := ""
			if i < len(lines) {
				text = lines[i]
			}

			if fit || i >= len(lines) {
				text = fitWidth(text, c.W, lk.styles.body)
			}

			add(c.Y+i, segment{c.X, c.W, text})
		}
	}

	lines := make([]string, 0, a.height-2)

	var sb strings.Builder

	for _, segs := range rows[2:a.height] {
		slices.SortFunc(segs, func(p, q segment) int { return p.x - q.x })
		sb.Reset()

		x := 0

		for _, s := range segs {
			if s.x < x {
				continue
			}

			sb.WriteString(spaces(lk.styles.body, s.x-x))
			sb.WriteString(s.text)
			x = s.x + s.w
		}

		sb.WriteString(spaces(lk.styles.body, a.width-x))
		lines = append(lines, sb.String())
	}

	return lines
}

// flashStyles draws a pane's whole frame, title included, in the jump flash colour.
func flashStyles(st styles, match theme.Hex) styles {
	st.border = st.border.Foreground(match)
	st.borderAgent, st.borderShell = st.border, st.border
	st.kindAgent, st.kindShell = st.kindAgent.Foreground(match), st.kindShell.Foreground(match)
	st.titleMuted = st.titleMuted.Foreground(match)
	st.runs = &runCache{}

	return st
}

// edgePanes are the bottom panes of the leftmost and rightmost visible columns,
// where the hidden-column counts go; both are empty without hidden columns.
func edgePanes(g geometry) (left, right core.PaneID) {
	if g.hiddenLeft == 0 && g.hiddenRight == 0 {
		return "", ""
	}

	var lr, rr core.Rect

	for id, r := range g.rects {
		if left == "" || r.X < lr.X || r.X == lr.X && r.Y+r.H > lr.Y+lr.H {
			left, lr = id, r
		}

		if right == "" || r.X+r.W > rr.X+rr.W || r.X+r.W == rr.X+rr.W && r.Y+r.H > rr.Y+rr.H {
			right, rr = id, r
		}
	}

	return left, right
}

// welcomeHints lists the commands that make sense with no pane open, with
// their full key sequences.
func (a *App) welcomeHints() []hint {
	ids := []string{"palette.open", "agent.new", "pane.split.down", "pane.split.right", "theme.pick", "app.quit"}

	hints := make([]hint, 0, len(ids))
	for _, id := range ids {
		key, ok := a.keymap.keyFor(id)

		c, found := a.reg.get(id)
		if ok && found {
			hints = append(hints, hint{key: key, title: c.title})
		}
	}

	return hints
}

// spaces is n body-coloured blank cells.
func spaces(body lipgloss.Style, n int) string {
	if n <= 0 {
		return ""
	}

	return body.Render(strings.Repeat(" ", n))
}

// fitWidth makes s exactly w cells: a guest row that does not match its rect
// (before the pane is resized) is cut or padded rather than breaking the frame.
func fitWidth(s string, w int, body lipgloss.Style) string {
	got := ansi.StringWidth(s)
	switch {
	case got == w:
		return s
	case got > w:
		return ansi.Truncate(s, w, "")
	}

	return s + spaces(body, w-got)
}

// printable drops the control and invisible characters that guest or
// filesystem text may carry. Strip goes first and turns U+2028 and U+2029 into
// a line feed, which the filter then drops.
func printable(t string) string {
	return strings.Map(func(r rune) rune {
		if r < 0x20 || r == 0x7f || (r >= 0x80 && r < 0xa0) {
			return -1
		}

		return r
	}, invisible.Strip(t))
}

// cursorView places the cursor on screen: the prompt's, copy mode's block or
// the focused pane's. It is nil under an overlay, confirm, popup, the usage
// popover or the toast.
func (a *App) cursorView() *tea.Cursor { return a.cursorAt(a.geometry()) }

func (a *App) cursorAt(g geometry) *tea.Cursor {
	cur := a.cursorBase(g)
	if cur != nil && toastCovers(a.width, a.height, a.notice, a.icons, cur.X, cur.Y) {
		return nil
	}

	return cur
}

func (a *App) cursorBase(g geometry) *tea.Cursor {
	if a.prompt != nil {
		return a.promptCursor(g)
	}

	if a.composeShown() {
		if a.otherModalOpen() || a.popup || a.sequenceOpen() {
			return nil
		}

		return a.composeCursor()
	}

	if a.overlay != nil || a.plan != nil || a.conv != nil || a.checks != nil || a.merge != nil || a.prList != nil || a.popup || a.sequenceOpen() || a.confirm != nil {
		return nil
	}

	if a.copy != nil {
		return a.copyCursor(g)
	}

	if a.usageShows() {
		return nil
	}

	st := a.store.State()
	if a.rail.focused && a.railShows(st.Rail) {
		return a.railCursor(st)
	}

	if a.drawerHasKeys() {
		return nil
	}

	p := a.panes[st.Tab().Focus]

	r, ok := g.rects[st.Tab().Focus]
	if p == nil || !ok {
		return nil
	}

	pos, visible := p.cursor()

	c := contentRect(r)
	if !visible || pos.X < 0 || pos.Y < 0 || pos.X >= c.W || pos.Y >= c.H {
		return nil
	}

	if dr, ok := a.drawerRect(st); ok && c.X+pos.X >= dr.X && c.Y+pos.Y >= dr.Y && c.Y+pos.Y < dr.Y+dr.H {
		return nil
	}

	return tea.NewCursor(c.X+pos.X, c.Y+pos.Y)
}
