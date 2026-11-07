package ui

import (
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/mhersson/aim/internal/core"
	"github.com/mhersson/aim/internal/core/tiling"
)

// slideFrame is the pause between two frames of the drawer's slide.
const slideFrame = 16 * time.Millisecond

// drawerSlideMsg is one frame of the drawer's slide.
type drawerSlideMsg struct{}

// slideOpenMsg is the answer to a drawer opened by leader d, which slides in.
type slideOpenMsg struct{ reviewOpenMsg }

// drawerHasKeys reports whether the drawer is open and has the keyboard,
// whether or not it has slid into view. It reads the state only when the drawer
// has been given the keyboard, which keeps it off the path of a keystroke.
func (a *App) drawerHasKeys() bool {
	if !a.drawer.focused {
		return false
	}

	_, ok := a.drawerFull(a.store.State())

	return ok
}

// drawerTake gives the drawer the keyboard, with the diff holding it; the rail
// gives it up.
func (a *App) drawerTake() {
	a.railRelease()
	a.drawer.focused, a.drawer.tree = true, false
	a.drawer.takes++
}

// drawerRelease takes the keyboard from the drawer and un-zooms it: a zoomed
// drawer always has the keyboard.
func (a *App) drawerRelease() {
	a.releaseKeys()
	a.unzoom()
}

// unzoom takes every drawer back to its half.
func (a *App) unzoom() {
	st := a.store.State()
	active := st.Tab().ID

	for id, v := range a.drawer.views {
		if !v.zoom || (id == active && a.drawer.closing) {
			continue
		}

		// Only the active tab's drawer shows. A slide running ends at once and the
		// drawer takes its width with no slide; a closing one keeps its width.
		full, shown := a.drawerFull(st)
		slide := id == active && shown

		if slide && !a.drawer.slideFrom.IsZero() {
			a.endSlide()

			slide = false
		}

		v.zoom = false

		if slide {
			a.startResize(full.W)
		}
	}
}

// reviewZoom is review.zoom: it widens the drawer to the whole body and back,
// and gives it the keyboard if it lacks it.
func (a *App) reviewZoom() tea.Cmd {
	tab, v, ok := a.reviewActive()
	if !ok {
		return nil
	}

	// A slide running ends at once, and the flip takes the new width with no slide.
	from := 0
	if full, shown := a.drawerFull(a.store.State()); shown && a.drawer.slideFrom.IsZero() {
		from = full.W
	}

	a.endSlide()
	a.clearDrawerSel(tab.ID)

	v.zoom = !v.zoom

	if !a.drawerHasKeys() {
		a.drawerTake()
	}

	a.startResize(from)

	if a.drawer.resizeFrom == 0 {
		return nil
	}

	return a.slideFrameCmd()
}

// releaseKeys takes the keyboard from the drawer and closes its search prompt
// and the worktree list; a zoom stays.
func (a *App) releaseKeys() {
	a.drawer.focused, a.drawer.tree = false, false
	a.closeWorktrees()
	a.closeLinks()

	for _, v := range a.drawer.views {
		v.search = nil
	}
}

// reviewToggle is leader d: it opens the drawer on the focused pane, or gives
// an open drawer the keyboard (after retargeting it to the focused pane), or
// closes a drawer that has it.
func (a *App) reviewToggle() tea.Cmd {
	a.endSlide()

	st := a.store.State()
	tab := st.Tab()

	switch {
	case tab.Review == nil:
		return a.openSliding(tab.Focus, false)
	case a.drawerHasKeys():
		return a.closeDrawer(true)
	}

	var cmd tea.Cmd
	if tab.Focus != "" && tab.Focus != tab.Review.Pane {
		cmd = a.openReview(tab.Focus, true)
	}

	a.drawerTake()

	return cmd
}

// openSliding opens the drawer on a pane the way openReview does and makes the
// answer slide it in.
func (a *App) openSliding(pane core.PaneID, anyDir bool) tea.Cmd {
	cmd := a.openReviewFor(pane, true, anyDir)
	if cmd == nil {
		return nil
	}

	return func() tea.Msg {
		m := cmd()
		if o, ok := m.(reviewOpenMsg); ok {
			return slideOpenMsg{o}
		}

		return m
	}
}

// onSlideOpen opens the drawer and, if that opened one, starts its slide in.
func (a *App) onSlideOpen(msg slideOpenMsg) tea.Cmd {
	was := a.store.State().Tab().Review != nil
	cmd := a.onReviewOpen(msg.reviewOpenMsg)

	if _, shown := a.drawerFull(a.store.State()); was || !shown {
		return cmd
	}

	return tea.Batch(cmd, a.startSlide(false))
}

// closeDrawer closes the active tab's drawer and returns the keyboard to the
// panes, sliding out when slide is set.
func (a *App) closeDrawer(slide bool) tea.Cmd {
	a.drawer.prNext = nil
	a.releaseKeys()
	a.clearDrawerSel(a.store.State().Tab().ID)

	if _, shown := a.drawerFull(a.store.State()); slide && shown {
		return a.startSlide(true)
	}

	// The drawer closes at once, so any slide, an un-zoom or a close, ends with it.
	a.drawer.slideFrom, a.drawer.closing, a.drawer.resizeFrom = time.Time{}, false, 0

	// The pages stay while the drawer slides out, so it does not change as it goes.
	if v := a.drawer.views[a.store.State().Tab().ID]; v != nil {
		a.dropPages(v)
	}

	// dispatch logs its own failures.
	_ = a.dispatch(core.CloseReview{})

	return nil
}

// startSlide begins a slide in, or out when closing, and asks for its frames.
func (a *App) startSlide(closing bool) tea.Cmd {
	a.drawer.slideFrom, a.drawer.closing, a.drawer.resizeFrom = a.clock.Now(), closing, 0

	return a.slideFrameCmd()
}

// startResize begins a width slide from from columns to the drawer's width. It
// does nothing with no drawer showing or no change of width.
func (a *App) startResize(from int) {
	full, shown := a.drawerFull(a.store.State())
	if !shown || from <= 0 || from == full.W {
		return
	}

	a.drawer.slideFrom, a.drawer.closing, a.drawer.resizeFrom = a.clock.Now(), false, from
}

// slideFrameCmd asks for the next frame unless one is already on its way; the
// one outstanding tick is the slide's only owner.
func (a *App) slideFrameCmd() tea.Cmd {
	if a.slideTicking {
		return nil
	}

	a.slideTicking = true

	return a.tick(slideFrame, func(time.Time) tea.Msg { return drawerSlideMsg{} })
}

// onDrawerSlide draws a frame and asks for the next while the slide runs.
func (a *App) onDrawerSlide() tea.Cmd {
	a.slideTicking = false

	if a.drawer.slideFrom.IsZero() {
		return nil
	}

	if a.clock.Now().Sub(a.drawer.slideFrom) >= slideFor {
		a.endSlide()

		return nil
	}

	return a.slideFrameCmd()
}

// endSlide ends a running slide at once. A drawer that was sliding out is
// closed; the active tab is the one that closes, since any action that could
// change it ends the slide first.
func (a *App) endSlide() {
	closing := a.drawer.closing
	a.drawer.slideFrom, a.drawer.closing, a.drawer.resizeFrom = time.Time{}, false, 0

	if closing && a.store.State().Tab().Review != nil {
		// dispatch logs its own failures.
		_ = a.dispatch(core.CloseReview{})
	}
}

// covered reports whether pane rect r lies wholly inside the drawer's box.
func covered(r, box core.Rect) bool {
	return r.X >= box.X && r.Y >= box.Y && r.X+r.W <= box.X+box.W && r.Y+r.H <= box.Y+box.H
}

// focusDir is leader h, j, k and l: a focus move that skips the panes the
// drawer covers. With the drawer showing, leader l with no visible pane to the
// right gives the drawer the keyboard, and leader h from the drawer leaves it.
func (a *App) focusDir(d core.Dir) tea.Cmd {
	st := a.store.State()

	box, shown := a.drawerHalf(st)
	if !shown || a.drawer.closing {
		return a.act(core.FocusDir{Dir: d, Area: a.bodyArea()})
	}

	if a.drawer.focused && d == core.Left {
		return a.drawerLeave()
	}

	if id, ok := a.visibleNeighbour(st, d, box); ok {
		return a.act(core.FocusPane{ID: id})
	}

	if d == core.Right {
		a.drawerTake()
	}

	return nil
}

// visibleNeighbour is the pane a focus move reaches among those the drawer does
// not cover.
func (a *App) visibleNeighbour(st core.State, d core.Dir, box core.Rect) (core.PaneID, bool) {
	return core.NeighbourSkipping(st.Tab(), d, a.bodyAreaIn(st.Rail), st.Layout, func(_ core.PaneID, r core.Rect) bool {
		return covered(r, box)
	})
}

// drawerLeave gives the keyboard to the shown pane nearest the drawer's left
// edge; with no pane showing it stays with the drawer.
func (a *App) drawerLeave() tea.Cmd {
	st := a.store.State()

	box, shown := a.drawerHalf(st)
	if !shown {
		return nil
	}

	rects := a.geometry().rects

	var (
		ids   []core.PaneID
		cands []tiling.Rect
	)

	for _, id := range st.Tab().Panes {
		if r, ok := rects[id]; ok && !covered(r, box) {
			ids = append(ids, id)
			cands = append(cands, tiling.Rect(r))
		}
	}

	// Neighbour takes a pane for one on the drawer's left only when its right edge
	// is left of the drawer's, which a pane reaching the body's edge is not: the
	// drawer is stretched to the right so that those count too.
	from := tiling.Rect{X: box.X, Y: box.Y, W: box.W + a.width, H: box.H}

	i := tiling.Neighbour(from, cands, tiling.SideLeft, false)
	if i < 0 {
		return nil
	}

	a.drawerRelease()

	return a.focusPane(ids[i])
}

// uncoverFocus closes the active tab's drawer, with no slide, when the focused
// pane lies under its half: the pane an action just focused must show. A zoomed
// drawer goes back to its half first.
func (a *App) uncoverFocus() {
	if !a.drawer.closing {
		a.unzoom()
	}

	st := a.store.State()

	box, shown := a.drawerHalf(st)
	if !shown || a.drawer.closing {
		return
	}

	if r, ok := a.geometry().rects[st.Tab().Focus]; ok && covered(r, box) {
		a.closeDrawer(false)
	}
}

// settleKey is what settleDrawer's outcome depends on.
type settleKey struct {
	ok              bool
	ver             uint64
	w, h            int
	drawer, closing bool
	rail            bool
}

// settleDrawer keeps the keyboard with something that shows after an update. A
// drawer that cannot show holds nothing. When the keyboard is with the panes and
// the focused pane is under the drawer, either an action of the operator's just
// focused that pane, and the drawer closes, or something else did, and the
// drawer takes the keyboard. prev is the state before the update, and acted says
// the update was the operator's and changed the state. Updates that change none
// of what the outcome depends on cost one comparison.
func (a *App) settleDrawer(prev core.State, acted bool) {
	key := settleKey{
		ok: true, ver: a.storeVer, w: a.width, h: a.height,
		drawer: a.drawer.focused, closing: a.drawer.closing, rail: a.rail.focused,
	}
	if a.settled == key {
		return
	}

	if !a.settleDrawerNow(prev, acted) {
		a.settled = key
	}
}

// settleDrawerNow does settleDrawer's work and reports whether it changed anything.
func (a *App) settleDrawerNow(prev core.State, acted bool) bool {
	st := a.store.State()

	// The tree has the keyboard only while the active drawer shows it; a tab switch
	// that is not a key leaves the flag where it was.
	if a.drawer.tree && !drawerTree(st) {
		a.drawer.tree = false
	}

	box, shown := a.drawerFull(st)
	if !shown {
		if a.drawer.focused {
			a.drawerRelease()

			return true
		}

		return false
	}

	if a.drawer.closing || a.drawer.focused || (a.rail.focused && a.railVisible()) {
		return false
	}

	tab := st.Tab()
	if r, ok := a.geometry().rects[tab.Focus]; !ok || !covered(r, box) {
		return false
	}

	if was, found := tabByID(prev, tab.ID); acted && found && was.Focus != tab.Focus {
		a.closeDrawer(false)
	} else {
		a.drawerTake()
	}

	return true
}
