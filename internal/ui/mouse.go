package ui

import (
	"time"

	tea "charm.land/bubbletea/v2"
	uv "github.com/charmbracelet/ultraviolet"

	"github.com/mhersson/aim/internal/core"
)

// multiClickGap is the longest pause between presses on one cell that still
// counts as a double or triple click.
const multiClickGap = 400 * time.Millisecond

type hitArea int

const (
	hitNone hitArea = iota
	hitContent
	hitHeader
	hitTab
	hitUsage
)

// hitResult is what lies under a screen cell. local is the cell inside the
// pane's content (row, col) and is only set for hitContent.
type hitResult struct {
	area  hitArea
	pane  core.PaneID
	local point
	tab   int
}

// clickState counts consecutive presses on one cell.
type clickState struct {
	at   time.Time
	x, y int
	n    int
}

// heldPress is the left button held down in a pane's content.
type heldPress struct {
	pane     core.PaneID
	x, y     int   // screen cell of the press
	local    point // cell inside the content
	alt      bool  // block selection
	tracking bool  // the guest wants the click; sent on release unless it turns into a drag
	mod      tea.KeyMod
	anchor   stablePoint
	dragging bool
	multi    bool // a double or triple click, which selects on press
}

// forwarded is a middle or right button whose press went to a guest.
type forwarded struct {
	pane     core.PaneID
	button   tea.MouseButton
	col, row int // last cell sent, in pane content coordinates
}

type mouseState struct {
	click clickState
	press *heldPress
	fwd   *forwarded
	text  *textPress
	seq   int // the last edge-scroll tick's number
}

// hit finds what lies under the screen cell (x, y): a pane's content or top
// border, a tab segment or the usage module of the workspace bar, or nothing.
// A pane's side and bottom borders, the rule under the workspace bar and
// whatever the review drawer covers are nothing.
func (a *App) hit(x, y int) hitResult {
	if y == 0 {
		return a.hitTab(x)
	}

	if a.inDrawer(x, y) {
		return hitResult{}
	}

	for id, r := range a.geometry().rects {
		if x < r.X || x >= r.X+r.W {
			continue
		}

		c := contentRect(r)

		switch {
		case y == r.Y:
			return hitResult{area: hitHeader, pane: id}
		case y >= c.Y && y < c.Y+c.H && x >= c.X && x < c.X+c.W:
			return hitResult{area: hitContent, pane: id, local: point{row: y - c.Y, col: x - c.X}}
		}
	}

	return hitResult{}
}

// inRail reports whether the cell lies inside the rail's box.
func (a *App) inRail(x, y int) bool {
	r := a.railRect()

	return r.W > 0 && x >= r.X && x < r.X+r.W && y >= r.Y && y < r.Y+r.H
}

// hitTab maps a cell of row 0 to the tab segment or the usage module drawn
// there; the edge cell after each segment is nothing. Both come from the plan
// the bar draws with.
func (a *App) hitTab(x int) hitResult {
	tabs := a.tabInfos()
	plan := planBar(tabs, a.store.State().Active, a.trayModules(), a.width, a.icons)
	at := 0

	if mx, mw, ok := trayModuleAt(plan, a.width, a.icons, modUsage); ok && x >= mx && x < mx+mw {
		return hitResult{area: hitUsage}
	}

	for _, v := range plan.tabs {
		w := segWidth(tabs[v.idx], v.level, a.icons)
		if x >= at && x < at+w {
			return hitResult{area: hitTab, tab: v.idx}
		}

		at += w + 1
	}

	return hitResult{}
}

// mouseBlocked reports whether modal UI owns the pointer: any modal, a pending
// leader (with or without its popup), or the shown usage popover. It also drops
// any held button, releasing a forwarded one in its guest, since the modal
// would swallow the release.
func (a *App) mouseBlocked() bool {
	if !a.modalOpen() && !a.pending && !a.usageShows() {
		return false
	}

	a.releaseForwarded()
	a.endPress()

	return true
}

// endPress ends a held press without a release and runs the redraw check the
// press was deferring.
func (a *App) endPress() {
	pr := a.mouse.press
	a.mouse.press = nil

	if pr == nil {
		return
	}

	if p := a.panes[pr.pane]; p != nil {
		a.dropRedrawnSelection(p)
	}
}

// focusPane focuses id unless it already is.
func (a *App) focusPane(id core.PaneID) tea.Cmd {
	if a.store.State().Tab().Focus == id {
		return nil
	}

	return a.act(core.FocusPane{ID: id})
}

// countClick returns how many presses in a row landed on this cell.
func (a *App) countClick(x, y int) int {
	now := a.clock.Now()
	c := a.mouse.click
	n := 1

	if c.n > 0 && c.n < 3 && c.x == x && c.y == y && now.Sub(c.at) <= multiClickGap {
		n = c.n + 1
	}

	a.mouse.click = clickState{at: now, x: x, y: y, n: n}

	return n
}

func (a *App) handleClick(msg tea.MouseClickMsg) tea.Cmd {
	if msg.Button == tea.MouseLeft {
		a.mouse.text = nil
	}

	if a.usageShows() && !a.modalOpen() && !a.pending {
		a.releaseForwarded()
		a.endPress()
		a.usageClick(msg.Mouse())

		return nil
	}

	if a.textClick(msg.Mouse()) {
		return nil
	}

	if a.mouseBlocked() {
		return nil
	}

	m := msg.Mouse()
	h := a.hit(m.X, m.Y)

	a.releaseForwarded()
	a.endPress()

	if a.inRail(m.X, m.Y) {
		if m.Button != tea.MouseLeft {
			a.mouse.click = clickState{}

			return nil
		}

		return a.railClick(m.X, m.Y)
	}

	if a.inDrawer(m.X, m.Y) {
		a.mouse.click = clickState{}

		if m.Button == tea.MouseLeft && !a.drawer.closing {
			a.drawerClick(m.X, m.Y)
		}

		return nil
	}

	if h.area != hitContent || m.Button != tea.MouseLeft {
		a.mouse.click = clickState{}
	}

	if h.area == hitContent || h.area == hitHeader {
		if a.rail.focused {
			a.railRelease()
		}

		if a.drawer.focused {
			a.drawerRelease()
		}
	}

	switch {
	case h.area == hitUsage && m.Button == tea.MouseLeft:
		return a.usageToggle()
	case h.area == hitTab && m.Button == tea.MouseLeft:
		return a.act(core.SelectTab{Index: h.tab})
	case h.area == hitHeader && m.Button == tea.MouseLeft:
		return a.focusPane(h.pane)
	case h.area != hitContent:
		return nil
	case m.Button != tea.MouseLeft:
		return a.otherPress(h, m)
	}

	return a.contentPress(h, m)
}

// otherPress handles the middle and right buttons: they focus the pane and go
// to a guest that tracks the mouse, and never select. Otherwise a middle press
// pastes.
func (a *App) otherPress(h hitResult, m tea.Mouse) tea.Cmd {
	cmd := a.focusPane(h.pane)

	p := a.panes[h.pane]
	if p == nil {
		return cmd
	}

	if tracking, _ := p.inputModes(); tracking {
		p.sendMouse(uv.MouseClickEvent(uv.Mouse{X: h.local.col, Y: h.local.row, Button: m.Button, Mod: m.Mod}))
		a.mouse.fwd = &forwarded{pane: h.pane, button: m.Button, col: h.local.col, row: h.local.row}

		return cmd
	}

	if m.Button == tea.MouseMiddle {
		return tea.Batch(cmd, a.middlePaste(p))
	}

	return cmd
}

// contentPress handles a left press in a pane's content. A guest that tracks the
// mouse gets the click on release, so a drag can become a selection instead.
func (a *App) contentPress(h hitResult, m tea.Mouse) tea.Cmd {
	cmd := a.focusPane(h.pane)

	p := a.panes[h.pane]
	if p == nil {
		return cmd
	}

	n := a.countClick(m.X, m.Y)
	tracking, _ := p.inputModes()
	// The second and third press of a multi-click select; the guest keeps only the first click.
	tracking = tracking && n == 1
	pr := &heldPress{
		pane: h.pane, x: m.X, y: m.Y, local: h.local, tracking: tracking,
		alt: m.Mod.Contains(tea.ModAlt), mod: m.Mod, multi: n > 1,
	}

	a.clearSelections()

	p.withView(func(g grid, top int, trimmed uint64) {
		at := clampPoint(g, point{top + h.local.row, h.local.col})
		pr.anchor = stable(at, trimmed)

		switch n {
		case 2:
			a.sel = newPaneSelection(h.pane, wordAt(g, at), trimmed)
		case 3:
			a.sel = newPaneSelection(h.pane, lineAt(g, at), trimmed)
		}

		if a.sel != nil {
			a.sel.watch(g, trimmed)
		}
	})

	a.mouse.press = pr

	return cmd
}

func (a *App) handleMotion(msg tea.MouseMotionMsg) tea.Cmd {
	m := msg.Mouse()

	if pr := a.mouse.text; pr != nil && m.Button == tea.MouseLeft {
		return a.textDrag(pr, m.X, m.Y)
	}

	if a.mouseBlocked() {
		return nil
	}

	switch {
	case a.mouse.press != nil && m.Button == tea.MouseLeft:
		a.dragTo(a.mouse.press, m.X, m.Y)
	case a.mouse.fwd != nil && m.Button == a.mouse.fwd.button:
		a.forwardHeld(a.mouse.fwd, m, func(m uv.Mouse) uv.MouseEvent { return uv.MouseMotionEvent(m) })
	case m.Button == tea.MouseNone:
		h := a.hit(m.X, m.Y)

		var cmd tea.Cmd
		if a.focusFollows && a.mouse.press == nil && a.mouse.fwd == nil && a.mouse.text == nil {
			cmd = a.hoverFocus(m.X, m.Y, h)
		}

		if p := a.panes[h.pane]; h.area == hitContent && p != nil {
			p.sendMouse(uv.MouseMotionEvent(uv.Mouse{X: h.local.col, Y: h.local.row, Button: m.Button, Mod: m.Mod}))
		}

		return cmd
	}

	return nil
}

// hoverHeld reports whether hover moves nothing: a zoomed drawer shows, or the
// rail's or the drawer's search is being typed.
func (a *App) hoverHeld() bool {
	if a.rail.searching {
		return true
	}

	if !a.drawersOpen {
		return false
	}

	st := a.store.State()
	v := a.drawer.views[st.Tab().ID]

	return st.Tab().Review != nil && v != nil && (v.zoom || v.search != nil)
}

// hoverFocus gives the keyboard to what lies under (x, y): the rail, the open
// drawer, or the pane h names. A drawer sliding out is not a target.
func (a *App) hoverFocus(x, y int, h hitResult) tea.Cmd {
	if a.hoverHeld() {
		return nil
	}

	switch {
	case a.inRail(x, y):
		if !a.rail.focused {
			a.railTake()
		}
	case a.inDrawer(x, y):
		if !a.drawer.closing && !a.drawer.focused {
			a.drawerTake()
		}
	case h.area == hitContent || h.area == hitHeader:
		if a.rail.focused {
			a.railRelease()
		}

		if a.drawer.focused {
			a.drawerRelease()
		}

		return a.focusPane(h.pane)
	}

	return nil
}

func (a *App) handleRelease(msg tea.MouseReleaseMsg) tea.Cmd {
	m := msg.Mouse()

	if pr := a.mouse.text; pr != nil && m.Button == tea.MouseLeft {
		return a.textRelease(pr, m)
	}

	if a.mouseBlocked() {
		return nil
	}

	if f := a.mouse.fwd; f != nil && m.Button == f.button {
		a.mouse.fwd = nil
		a.forwardHeld(f, m, func(m uv.Mouse) uv.MouseEvent { return uv.MouseReleaseEvent(m) })

		return nil
	}

	pr := a.mouse.press
	if pr == nil || m.Button != tea.MouseLeft {
		return nil
	}

	a.mouse.press = nil

	if !pr.dragging && (m.X != pr.x || m.Y != pr.y) {
		a.dragTo(pr, m.X, m.Y)
	}

	if pr.dragging || pr.multi {
		if a.copyOnSelect {
			cmd := a.copySelection()
			a.sel = nil

			return cmd
		}

		if p := a.panes[pr.pane]; p != nil {
			a.dropRedrawnSelection(p)
		}

		return a.clip.copyPrimary(a.selectedText())
	}

	if !pr.tracking {
		return nil
	}

	if p := a.panes[pr.pane]; p != nil {
		mouse := uv.Mouse{X: pr.local.col, Y: pr.local.row, Button: tea.MouseLeft, Mod: pr.mod}
		p.sendMouse(uv.MouseClickEvent(mouse))
		p.sendMouse(uv.MouseReleaseEvent(mouse))
	}

	return nil
}

// contentCell clamps the screen cell (x, y) into pane id's content rect and
// returns it relative to the content.
func (a *App) contentCell(id core.PaneID, x, y int) (col, row int, ok bool) {
	r, ok := a.geometry().rects[id]
	if !ok {
		return 0, 0, false
	}

	c := contentRect(r)

	return min(max(x-c.X, 0), max(c.W-1, 0)), min(max(y-c.Y, 0), max(c.H-1, 0)), true
}

// dragTo extends the selection of a held press to the pointer, clamped to the
// content of the pane where the press began. The first move off the press
// cell turns the press into a selection and the guest never sees it.
func (a *App) dragTo(pr *heldPress, x, y int) {
	p := a.panes[pr.pane]

	col, row, ok := a.contentCell(pr.pane, x, y)
	if p == nil || !ok {
		a.mouse.press = nil

		return
	}

	if !pr.dragging && x == pr.x && y == pr.y {
		return
	}

	pr.dragging = true

	mode := selStream
	if pr.alt {
		mode = selBlock
	}

	p.withView(func(g grid, top int, trimmed uint64) {
		head := clampPoint(g, point{top + row, col})
		a.sel = &paneSelection{pane: pr.pane, mode: mode, anchor: pr.anchor, head: stable(head, trimmed)}
		a.sel.watch(g, trimmed)
	})
}

// forwardHeld sends a motion or release of a forwarded button to its pane.
func (a *App) forwardHeld(f *forwarded, m tea.Mouse, ev func(uv.Mouse) uv.MouseEvent) {
	p := a.panes[f.pane]

	col, row, ok := a.contentCell(f.pane, m.X, m.Y)
	if p == nil || !ok {
		return
	}

	f.col, f.row = col, row
	p.sendMouse(ev(uv.Mouse{X: col, Y: row, Button: f.button, Mod: m.Mod}))
}

// releaseForwarded ends a forwarded button still held, so the guest does not
// see it stuck down, and forgets it.
func (a *App) releaseForwarded() {
	f := a.mouse.fwd
	a.mouse.fwd = nil

	if f == nil {
		return
	}

	if p := a.panes[f.pane]; p != nil {
		p.sendMouse(uv.MouseReleaseEvent(uv.Mouse{X: f.col, Y: f.row, Button: f.button}))
	}
}

// planWheel scrolls the plan overlay when the wheel is over its box and reports
// whether it took the event.
func (a *App) planWheel(msg tea.MouseWheelMsg) bool {
	if a.plan == nil {
		return false
	}

	m := msg.Mouse()

	x, y, w, h, ok := planBox(a.width, a.height)
	if !ok || m.X < x || m.X >= x+w || m.Y < y || m.Y >= y+h {
		return false
	}

	a.sizePlan()

	switch m.Button {
	case tea.MouseWheelDown:
		a.plan.shown().scroll(scrollStep)
	case tea.MouseWheelUp:
		a.plan.shown().scroll(-scrollStep)
	}

	return true
}

// convWheel scrolls the conversation overlay when the wheel is over its box and
// reports whether it took the event. The box takes the wheel while the chooser
// is open, but does not scroll.
func (a *App) convWheel(msg tea.MouseWheelMsg) bool {
	v := a.conv
	if v == nil {
		return false
	}

	m := msg.Mouse()

	x, y, w, h, ok := planBox(a.width, a.height)
	if !ok || m.X < x || m.X >= x+w || m.Y < y || m.Y >= y+h {
		return false
	}

	if v.chooser != nil || v.doc == nil {
		return true
	}

	a.sizeConv()

	switch m.Button {
	case tea.MouseWheelDown:
		v.view.scroll(scrollStep)
	case tea.MouseWheelUp:
		v.view.scroll(-scrollStep)
	}

	return true
}

// handleWheel acts on the pane under the pointer without moving focus: a guest
// that tracks the mouse gets the event, the alt screen gets cursor-key presses,
// and any other pane scrolls its view.
func (a *App) handleWheel(msg tea.MouseWheelMsg) {
	if a.planWheel(msg) || a.convWheel(msg) || a.logWheel(msg) || a.mouseBlocked() {
		return
	}

	m := msg.Mouse()

	if a.inRail(m.X, m.Y) {
		switch m.Button {
		case tea.MouseWheelDown:
			a.onRailWheel(1)
		case tea.MouseWheelUp:
			a.onRailWheel(-1)
		}

		return
	}

	if a.inDrawer(m.X, m.Y) {
		switch m.Button {
		case tea.MouseWheelDown:
			a.drawerWheel(1)
		case tea.MouseWheelUp:
			a.drawerWheel(-1)
		}

		return
	}

	h := a.hit(m.X, m.Y)

	p := a.panes[h.pane]
	if h.area != hitContent || p == nil {
		return
	}

	x, y := h.local.col, h.local.row
	tracking, alt := p.inputModes()

	if tracking {
		p.scrollLive()
		p.sendMouse(uv.MouseWheelEvent(uv.Mouse{X: x, Y: y, Button: m.Button, Mod: m.Mod}))

		return
	}

	var code rune

	switch m.Button {
	case tea.MouseWheelUp:
		code = tea.KeyUp
	case tea.MouseWheelDown:
		code = tea.KeyDown
	default:
		return
	}

	if alt {
		for range scrollStep {
			p.sendKey(uv.KeyPressEvent{Code: code})
		}

		return
	}

	if code == tea.KeyUp {
		p.scrollBy(scrollStep)
	} else {
		p.scrollBy(-scrollStep)
	}
}
