package ui

import (
	"slices"
	"sync"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/exp/teatest/v2"

	"github.com/mhersson/aim/internal/core"
)

// keysSnap is what a flow test reads from the running app.
type keysSnap struct {
	review core.PaneID // the pane the active tab's drawer reviews; empty when none is open
	focus  core.PaneID
	drawer bool // the drawer has the keyboard
}

// keysProbe holds the app's state after its latest update, for a test on another goroutine.
type keysProbe struct {
	mu   sync.Mutex
	snap keysSnap
}

func (p *keysProbe) wait(t *testing.T, what string, cond func(keysSnap) bool) {
	t.Helper()

	deadline := time.Now().Add(10 * time.Second)

	for {
		p.mu.Lock()
		s := p.snap
		p.mu.Unlock()

		if cond(s) {
			return
		}

		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s; the app is at %+v", what, s)
		}

		time.Sleep(10 * time.Millisecond)
	}
}

// keysFlow is the app that records its state after every update.
type keysFlow struct {
	*App
	probe *keysProbe
}

func (m keysFlow) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	_, cmd := m.App.Update(msg)
	tab := m.store.State().Tab()
	s := keysSnap{focus: tab.Focus, drawer: m.drawer.focused}

	if tab.Review != nil {
		s.review = tab.Review.Pane
	}

	m.probe.mu.Lock()
	m.probe.snap = s
	m.probe.mu.Unlock()

	return m, cmd
}

// leader d opens the drawer with the keyboard, esc hands it back, a second leader d
// retargets it to the focused pane, and a third closes it.
func TestLeaderDOpensFocusesClosesAndRetargets(t *testing.T) {
	t.Parallel()
	f := newFakeGit(t)
	dir := editedRepo(t, f).Root()
	a, _, clk := kindApp(t, gitOf(f), func(o *Options) { o.Cwd, o.GitTempDir = dir, t.TempDir() })

	probe := &keysProbe{}

	// The slide frames come from the runtime's ticks; each poll moves the clock past the slide, so a frame ends it.
	waitSlide := func(what string, cond func(keysSnap) bool) {
		t.Helper()

		probe.wait(t, what, func(s keysSnap) bool {
			clk.Advance(slideFor)

			return cond(s)
		})
	}

	tm := teatest.NewTestModel(t, keysFlow{App: a, probe: probe}, teatest.WithInitialTermSize(200, 50))
	send := func(ks ...tea.KeyPressMsg) {
		for _, k := range ks {
			tm.Send(k)
		}
	}

	send(keyLeader, keyRune('v'))
	probe.wait(t, "a second pane", func(s keysSnap) bool { return s.focus == "p2" })

	send(keyLeader, keyRune('d'))
	waitSlide("the drawer on p2 with the keyboard", func(s keysSnap) bool { return s.review == "p2" && s.drawer })
	waitForText(t, tm, "fifty")

	// p2 lies under the drawer, so the keyboard goes to the pane nearest it that shows.
	send(keyLeader, keyRune('h'))
	probe.wait(t, "the keyboard back with p1", func(s keysSnap) bool { return !s.drawer && s.focus == "p1" && s.review == "p2" })

	send(keyLeader, keyRune('d'))
	waitSlide("the drawer on p1 with the keyboard", func(s keysSnap) bool { return s.review == "p1" && s.drawer })

	send(keyLeader, keyRune('d'))
	waitSlide("the drawer to close", func(s keysSnap) bool { return s.review == "" && !s.drawer })

	if err := tm.Quit(); err != nil {
		t.Fatalf("Quit() error = %v", err)
	}

	tm.FinalModel(t, teatest.WithFinalTimeout(5*time.Second))
}

func TestFocusSkipsCoveredPanes(t *testing.T) {
	t.Parallel()
	r, dir := fakeKeysRig(t)

	// p1 across the top; p2 and p3 side by side below it. The drawer covers the
	// right half: p3 lies wholly under it, p2 and p1 do not.
	p2 := r.openShell(t, dir, core.SplitHorizontal)
	p3 := r.openShell(t, dir, core.SplitVertical)

	openIn(t, r.App, core.FocusPane{ID: "p1"}, core.OpenReview{Pane: "p1", Mode: core.ReviewBranch})
	r.settle()

	box, _ := r.drawerFull(r.store.State())
	if p := r.layoutRects()[p3]; p.X < box.X || p.X+p.W > box.X+box.W {
		t.Fatalf("setup: p3 at %+v is not under the drawer %+v", p, box)
	}

	steps := []struct {
		key      rune
		from, to core.PaneID
		keyboard bool // the drawer takes the keyboard instead
	}{
		{'j', "p1", p2, false},
		{'l', p2, p2, true},
		{'k', p2, "p1", false},
		{'h', "p1", "p1", false},
	}

	for _, s := range steps {
		openIn(t, r.App, core.FocusPane{ID: s.from})
		r.drawer.focused = false
		r.leader(s.key)

		if got := r.focus(); got != s.to {
			t.Errorf("leader %c from %s focuses %s, want %s", s.key, s.from, got, s.to)
		}

		if r.drawer.focused != s.keyboard {
			t.Errorf("leader %c from %s: drawer has the keyboard = %v, want %v", s.key, s.from, r.drawer.focused, s.keyboard)
		}
	}

	t.Run("scrolling", func(t *testing.T) {
		// A layout switch rebuilds the tab without its drawer, so the drawer opens after it.
		openIn(t, r.App, core.SetLayoutMode{Mode: core.ModeScrolling, Area: r.bodyArea()}, core.FocusPane{ID: "p1"},
			core.OpenReview{Pane: "p1", Mode: core.ReviewBranch})

		for range 3 {
			r.drawer.focused = false
			r.leader('l')

			if p := r.layoutRects()[r.focus()]; p.X >= box.X {
				t.Fatalf("leader l in scrolling focuses %s at %+v, under the drawer %+v", r.focus(), p, box)
			}
		}

		if !r.drawer.focused {
			t.Error("leader l past the last visible column did not give the drawer the keyboard")
		}
	})
}

func TestLeaderLEntersTheDrawer(t *testing.T) {
	t.Parallel()
	r, _ := fakeKeysRig(t)
	r.opened(t)
	r.leader('h')

	if r.drawer.focused {
		t.Fatal("leader h left the keyboard with the drawer")
	}

	r.leader('l')

	if !r.drawer.focused {
		t.Error("leader l with no visible pane to the right did not give the drawer the keyboard")
	}

	if got := r.focus(); got != "p1" {
		t.Errorf("focus = %s, want it to stay on p1", got)
	}
}

func TestLeaderHLeavesToTheNearestVisiblePane(t *testing.T) {
	t.Parallel()
	r, dir := fakeKeysRig(t)

	// p1 and p3 share the left half, p2 holds the right half under the drawer.
	p2 := r.openShell(t, dir, core.SplitVertical)
	openIn(t, r.App, core.FocusPane{ID: "p1"})
	p3 := r.openShell(t, dir, core.SplitVertical)
	openIn(t, r.App, core.FocusPane{ID: p2})
	r.opened(t)

	r.leader('h')

	if got := r.focus(); got != p3 {
		t.Errorf("leader h focuses %s, want %s, the pane nearest the drawer", got, p3)
	}

	if r.drawer.focused || r.store.State().Tab().Review == nil {
		t.Errorf("keyboard with the drawer = %v, drawer open = %v, want the panes to have the keyboard and the drawer to stay",
			r.drawer.focused, r.store.State().Tab().Review != nil)
	}

	// leader h leaves the same way.
	r.leader('l')

	if !r.drawer.focused {
		t.Fatal("leader l did not enter the drawer")
	}

	openIn(t, r.App, core.FocusPane{ID: p2})
	r.leader('h')

	if got := r.focus(); got != p3 || r.drawer.focused {
		t.Errorf("leader h: focus = %s, drawer has the keyboard = %v, want %s and false", got, r.drawer.focused, p3)
	}
}

func TestEscStepsBackThenLeaderHReturnsTheKeyboard(t *testing.T) {
	t.Parallel()
	t.Run("search then drawer", func(t *testing.T) {
		r, _ := fakeKeysRig(t)
		v := r.opened(t)

		r.keys(keyRune('/'))

		if v.search == nil {
			t.Fatal("/ opened no search prompt")
		}

		r.keys(keyEsc)

		if v.search != nil || !r.drawer.focused {
			t.Errorf("first esc: search open = %v, drawer has the keyboard = %v, want false and true", v.search != nil, r.drawer.focused)
		}

		r.leader('h')

		if r.drawer.focused {
			t.Fatal("leader h left the keyboard with the drawer")
		}

		r.keys(keyRune('x'))
		eventually(t, "x to reach the pane", func() bool { return r.sentTo("p1") != "" })

		if got := r.sentTo("p1"); got != "x" {
			t.Errorf("the pane was sent %q after leader h, want x", got)
		}
	})

	t.Run("tree then drawer", func(t *testing.T) {
		r, _ := fakeKeysRig(t)
		v := r.opened(t)
		r.keys(keyTab, keyRune('j'))

		if !r.drawer.tree {
			t.Fatal("tab did not give the tree the keyboard")
		}

		cursor := v.cursor

		r.keys(keyEsc)

		if r.drawer.tree || !r.drawer.focused || !treeIsOpen(r.App) || v.cursor != cursor {
			t.Errorf("esc in the tree: tree has the keyboard = %v, drawer = %v, tree open = %v, cursor %d -> %d, want false, true, true and the cursor kept",
				r.drawer.tree, r.drawer.focused, treeIsOpen(r.App), cursor, v.cursor)
		}

		r.leader('h')

		if r.drawer.focused {
			t.Error("leader h left the keyboard with the drawer")
		}
	})
}

// Each esc closes exactly the topmost layer: confirm, prompt, copy mode, overlay,
// a pending leader, the rail, the drawer, then it reaches the pane.
func TestRoutingOrder(t *testing.T) {
	t.Parallel()
	r, _ := fakeKeysRig(t)
	r.opened(t)
	r.leader('a')
	// The rail took the keyboard from the drawer; both claim it so routing decides.
	r.drawer.focused = true

	r.openConfirm(confirm{action: "Test", prompt: "Sure?", yes: func(*App) tea.Cmd { return nil }})
	r.openPrompt(r.store.State().Tab().ID, "x", func(*App, string) tea.Cmd { return nil })
	r.enterCopyMode()
	r.openCommandPalette()

	r.pending = true

	layers := []struct {
		name string
		open func() bool
	}{
		{"confirm", func() bool { return r.confirm != nil }},
		{"prompt", func() bool { return r.prompt != nil }},
		{"copy mode", func() bool { return r.copy != nil }},
		{"overlay", func() bool { return r.overlay != nil }},
		{"leader", func() bool { return r.pending }},
		{"rail", func() bool { return r.rail.focused }},
		{"drawer", func() bool { return r.drawer.focused }},
	}

	for i, l := range layers {
		if !l.open() {
			t.Fatalf("setup: the %s is not open", l.name)
		}

		r.keys(keyEsc)
		r.until(t, "the "+l.name+" to close", func() bool { return !l.open() })

		for _, later := range layers[i+1:] {
			if !later.open() {
				t.Fatalf("esc closed the %s together with the %s", later.name, l.name)
			}
		}
	}

	// The drawer's close started the esc guard.
	r.clk.Advance(escCloseWait)
	r.keys(keyEsc)
	eventually(t, "the last esc to reach the pane", func() bool { return r.sentTo("p1") != "" })

	if got := r.sentTo("p1"); got != "\x1b" {
		t.Errorf("the pane was sent %q, want only the last esc", got)
	}
}

func TestRailAndDrawerShareTheKeyboard(t *testing.T) {
	t.Parallel()
	r, _ := fakeKeysRig(t)
	r.opened(t)

	holders := func() string {
		switch {
		case r.rail.focused && r.drawer.focused:
			return "both"
		case r.rail.focused:
			return "rail"
		case r.drawer.focused:
			return "drawer"
		}

		return "panes"
	}

	if got := holders(); got != "drawer" {
		t.Fatalf("after leader d the keyboard is with the %s, want the drawer", got)
	}

	r.leader('a')

	if got := holders(); got != "rail" {
		t.Fatalf("after leader a the keyboard is with the %s, want the rail", got)
	}

	r.leader('d')

	if got := holders(); got != "drawer" {
		t.Fatalf("after leader d again the keyboard is with the %s, want the drawer", got)
	}

	rail, box := r.railRect(), mustRect(t, r)

	click(r.App, rail.X+2, rail.Y+3)

	if got := holders(); got != "rail" {
		t.Fatalf("after a click on the rail the keyboard is with the %s, want the rail", got)
	}

	click(r.App, box.X+3, box.Y+3)

	if got := holders(); got != "drawer" {
		t.Errorf("after a click on the drawer the keyboard is with the %s, want the drawer", got)
	}
}

func TestSlideLastsAbout120ms(t *testing.T) {
	t.Parallel()
	r, _ := fakeKeysRig(t)
	r.leader('d')
	r.until(t, "the drawer to open", func() bool { return r.store.State().Tab().Review != nil })
	r.idle(t)

	full, _ := r.drawerFull(r.store.State())
	width := func() int {
		rect, ok := r.drawerRect(r.store.State())
		if !ok {
			return 0
		}

		return rect.W
	}

	if width() != 0 || r.ticks.count(slideFrame) != 1 {
		t.Fatalf("a new drawer is %d wide with %d frame ticks scheduled, want out of sight and one tick", width(), r.ticks.count(slideFrame))
	}

	// run advances the clock a frame at a time until no frame is held and returns the time it took.
	run := func(grow bool) time.Duration {
		prev, elapsed := width(), time.Duration(0)

		for {
			r.clk.Advance(slideFrame)

			if r.fireSlide() == 0 {
				return elapsed
			}

			elapsed += slideFrame

			if w := width(); (grow && w < prev) || (!grow && w > prev) {
				t.Fatalf("at %v the drawer went from %d to %d columns, the wrong way", elapsed, prev, w)
			}

			prev = width()

			if elapsed > 3*slideFor {
				t.Fatal("the slide never ends")
			}
		}
	}

	if took := run(true); took < slideFor || took > slideFor+slideFrame || width() != full.W {
		t.Fatalf("slide in took %v and ended %d wide, want about %v and %d", took, width(), slideFor, full.W)
	}

	ticks := r.ticks.count(slideFrame)

	// Idle: no frame is asked for.
	r.clk.Advance(time.Second)
	r.settle()
	r.keys(keyRune('j'))

	if r.ticks.count(slideFrame) != ticks || r.fireSlide() != 0 {
		t.Errorf("an idle drawer asked for frames: %d ticks, want %d", r.ticks.count(slideFrame), ticks)
	}

	r.leader('d')

	if !r.drawer.closing || width() != full.W || r.drawer.focused {
		t.Fatalf("leader d with the keyboard: closing = %v, width %d, keyboard with the drawer = %v, want a slide out from %d with the panes holding the keyboard",
			r.drawer.closing, width(), r.drawer.focused, full.W)
	}

	if took := run(false); took < slideFor || took > slideFor+slideFrame || r.store.State().Tab().Review != nil || r.drawer.closing {
		t.Errorf("slide out took %v, drawer open = %v, closing = %v, want about %v and a closed drawer", took, r.store.State().Tab().Review != nil, r.drawer.closing, slideFor)
	}
}

func TestResizeEndsTheSlide(t *testing.T) {
	t.Parallel()
	r, _ := fakeKeysRig(t)
	r.leader('d')
	r.until(t, "the drawer to open", func() bool { return r.store.State().Tab().Review != nil })
	r.idle(t)

	sized(t, r.App, 160, 40)

	full, _ := r.drawerFull(r.store.State())
	if rect := mustRect(t, r); rect.W != full.W {
		t.Fatalf("after a resize the drawer is %d wide, want %d at once", rect.W, full.W)
	}

	ticks := r.ticks.count(slideFrame)
	if r.fireSlide() != 1 || r.ticks.count(slideFrame) != ticks {
		t.Errorf("the frame held across the resize asked for another tick")
	}

	// A slide out ends the same way, with the drawer gone.
	r.leader('d')
	sized(t, r.App, 170, 40)

	if r.store.State().Tab().Review != nil || r.drawer.closing {
		t.Errorf("after a resize during the slide out: drawer open = %v, closing = %v, want it gone", r.store.State().Tab().Review != nil, r.drawer.closing)
	}
}

func TestTabSwitchShowsTheDrawerWithoutASlide(t *testing.T) {
	t.Parallel()

	for _, midSlide := range []bool{false, true} {
		r, _ := fakeKeysRig(t)

		if midSlide {
			r.leader('d')
			r.until(t, "the drawer to open", func() bool { return r.store.State().Tab().Review != nil })
			r.idle(t)
		} else {
			r.opened(t)
		}

		ticks := r.ticks.count(slideFrame)

		r.leader('c')
		r.leader('1')

		full, _ := r.drawerFull(r.store.State())
		if r.store.State().Active != 0 || mustRect(t, r).W != full.W || !r.drawer.slideFrom.IsZero() {
			t.Errorf("mid slide = %v: back on the first tab the drawer is %d wide of %d, slide running = %v, want it in at once",
				midSlide, mustRect(t, r).W, full.W, !r.drawer.slideFrom.IsZero())
		}

		r.fireSlide()

		if got := r.ticks.count(slideFrame); got != ticks {
			t.Errorf("mid slide = %v: switching tabs scheduled %d frames", midSlide, got-ticks)
		}
	}
}

func TestSecondLeaderDEndsTheSlide(t *testing.T) {
	t.Parallel()
	r, _ := fakeKeysRig(t)
	r.leader('d')
	r.until(t, "the drawer to open", func() bool { return r.store.State().Tab().Review != nil })
	r.idle(t)

	full, _ := r.drawerFull(r.store.State())

	// The slide in ends at once; the press then closes the drawer from fully in.
	r.leader('d')

	if rect, ok := r.drawerRect(r.store.State()); !ok || rect.W != full.W || !r.drawer.closing {
		t.Fatalf("second leader d: drawer %+v, closing = %v, want the full drawer sliding out", rect, r.drawer.closing)
	}

	// A third press ends the slide out, which closes the drawer, and the same press
	// opens a new one, sliding in with the keyboard.
	r.leader('d')
	r.until(t, "the drawer to open again", func() bool { return r.store.State().Tab().Review != nil })

	if r.drawer.closing || r.drawer.slideFrom.IsZero() || !r.drawer.focused {
		t.Errorf("third leader d: closing = %v, slide running = %v, drawer has the keyboard = %v, want a new drawer sliding in with the keyboard",
			r.drawer.closing, !r.drawer.slideFrom.IsZero(), r.drawer.focused)
	}

	if rect, ok := r.drawerRect(r.store.State()); ok && rect.W != 0 {
		t.Errorf("the new drawer is %d wide at the start of its slide, want out of sight", rect.W)
	}
}

func TestZoomedTabWithADrawer(t *testing.T) {
	t.Parallel()
	r, dir := fakeKeysRig(t)
	r.openShell(t, dir, core.SplitVertical)
	openIn(t, r.App, core.FocusPane{ID: "p1"}, core.ToggleZoom{})
	r.opened(t)

	// The zoomed pane fills the body, so the drawer does not cover it.
	r.leader('h')

	if r.drawer.focused || r.focus() != "p1" {
		t.Fatalf("leader h: drawer has the keyboard = %v, focus = %s, want the zoomed p1 to have it", r.drawer.focused, r.focus())
	}

	r.keys(keyRune('x'))
	eventually(t, "x to reach the zoomed pane", func() bool { return r.sentTo("p1") != "" })

	if got := r.sentTo("p1"); got != "x" {
		t.Errorf("the zoomed pane was sent %q, want x", got)
	}

	// p2 would lie under the drawer once unzoomed, so there is no pane to go to.
	r.leader('l')

	if !r.drawer.focused || r.focus() != "p1" || !r.store.State().Tab().Zoomed {
		t.Errorf("leader l: drawer has the keyboard = %v, focus = %s, zoomed = %v, want the drawer to take it and the tab to stay zoomed",
			r.drawer.focused, r.focus(), r.store.State().Tab().Zoomed)
	}
}

func TestRailJumpToACoveredPaneClosesTheDrawer(t *testing.T) {
	t.Parallel()
	r, dir := fakeKeysRig(t)
	agent := r.agent(t, dir, "")

	r.leader('a')
	r.opened(t)

	box, _ := r.drawerFull(r.store.State())
	if p := r.layoutRects()[agent]; p.X < box.X {
		t.Fatalf("setup: the agent at %+v is not under the drawer %+v", p, box)
	}

	// A second tab, where the rail takes the keyboard to jump back.
	r.leader('c')
	r.leader('a')
	r.keys(keyEnter)

	st := r.store.State()
	if st.Active != 0 || st.Tab().Focus != agent || st.Tab().Review != nil {
		t.Errorf("after the jump: tab %d, focus %s, drawer open = %v, want the agent's tab, the agent and no drawer", st.Active, st.Tab().Focus, st.Tab().Review != nil)
	}

	if r.drawer.closing || r.drawer.focused || r.rail.focused {
		t.Errorf("after the jump: closing = %v, drawer has the keyboard = %v, rail has it = %v, want the pane to have it and no slide",
			r.drawer.closing, r.drawer.focused, r.rail.focused)
	}
}

func TestPaneMovedIntoATabWithADrawer(t *testing.T) {
	t.Parallel()
	// secondTab opens a tab with two shells and leaves the second one focused.
	secondTab := func(t *testing.T, r *drawerRig, dir string) core.PaneID {
		t.Helper()
		r.leader('c')

		return r.openShell(t, dir, core.SplitVertical)
	}

	t.Run("lands under the drawer", func(t *testing.T) {
		r, dir := fakeKeysRig(t)
		r.opened(t)
		moved := secondTab(t, r, dir)

		r.keys(keyLeader, keyRune('>'), keyRune('1'))

		st := r.store.State()
		if st.Active != 0 || st.Tab().Focus != moved || st.Tab().Review != nil || r.drawer.closing {
			t.Errorf("tab %d, focus %s, drawer open = %v, closing = %v, want the moved pane focused and the drawer closed at once",
				st.Active, st.Tab().Focus, st.Tab().Review != nil, r.drawer.closing)
		}
	})

	t.Run("lands beside the drawer", func(t *testing.T) {
		r, dir := fakeKeysRig(t)
		r.openShell(t, dir, core.SplitVertical)
		openIn(t, r.App, core.FocusPane{ID: "p1"})
		r.opened(t)

		moved := secondTab(t, r, dir)
		r.keys(keyLeader, keyRune('>'), keyRune('1'))

		st := r.store.State()
		if st.Active != 0 || st.Tab().Focus != moved || st.Tab().Review == nil || r.drawer.focused {
			t.Errorf("tab %d, focus %s, drawer open = %v, drawer has the keyboard = %v, want the drawer open and the moved pane to have the keyboard",
				st.Active, st.Tab().Focus, st.Tab().Review != nil, r.drawer.focused)
		}
	})

	t.Run("sent away", func(t *testing.T) {
		r, dir := fakeKeysRig(t)
		r.opened(t)
		secondTab(t, r, dir)

		r.keys(keyLeader, keyRune('.'), keyRune('1'))

		if st := r.store.State(); st.Active != 1 || st.Tabs[0].Review == nil || st.Tabs[0].Review.Pane != "p1" {
			t.Errorf("tab %d, first tab's drawer = %+v, want the second tab and the drawer still on p1", st.Active, st.Tabs[0].Review)
		}
	})
}

// drawerCell is the screen cell at column dx, row dy of the drawer's box.
func (r *drawerRig) drawerCell(t *testing.T, dx, dy int) (x, y int) {
	t.Helper()

	box := mustRect(t, r)

	return box.X + dx, box.Y + dy
}

func TestClickOnARowMovesTheCursor(t *testing.T) {
	t.Parallel()
	r, _ := fakeKeysRig(t)
	v := r.opened(t)
	r.leader('h')

	// The tree is hidden, so the diff starts at the drawer's first inner column.
	x, y := r.drawerCell(t, 1+8, 1+5)

	click(r.App, x, y)

	if v.cursor != 5 || !r.drawer.focused || r.drawer.tree {
		t.Errorf("a click on row 5: cursor = %d, drawer has the keyboard = %v, tree = %v, want 5, true and false", v.cursor, r.drawer.focused, r.drawer.tree)
	}

	// With the tree open, a click on one of its files jumps the diff to it.
	r.keys(keyTab, keyEsc)
	r.leader('h')

	if !treeIsOpen(r.App) || r.drawer.focused {
		t.Fatalf("setup: tree open = %v, drawer has the keyboard = %v, want the tree open and the keyboard with the panes", treeIsOpen(r.App), r.drawer.focused)
	}

	z := slices.IndexFunc(v.derived().tree, func(it treeItem) bool { return !it.dir && v.res.Files[it.file].Path == "z.txt" })
	x, y = r.drawerCell(t, 2, 1+3+z)

	click(r.App, x, y)

	if fileAtCursor(v) != "z.txt" || v.rows[v.cursor].kind != rowFile || !r.drawer.focused {
		t.Errorf("a click on z.txt in the tree: cursor on %s (kind %d), drawer has the keyboard = %v, want z.txt's header and the keyboard",
			fileAtCursor(v), v.rows[v.cursor].kind, r.drawer.focused)
	}

	// Beside the open tree, a click on a diff row moves the cursor there and keeps the tree.
	lay := r.layout(t)
	if lay.treeW == 0 {
		t.Fatal("setup: the open tree is not drawn")
	}

	want := v.viewTop(lay.bodyH) + 5
	x, y = r.drawerCell(t, 1+lay.treeW+1+8, 1+5)

	click(r.App, x, y)

	if v.cursor != want || !r.drawer.focused || r.drawer.tree || !treeIsOpen(r.App) {
		t.Errorf("a click on row 5 of the diff beside the tree: cursor = %d, drawer has the keyboard = %v, tree has it = %v, tree open = %v, want %d, true, false and true",
			v.cursor, r.drawer.focused, r.drawer.tree, treeIsOpen(r.App), want)
	}

	if got := r.sentSoFar(t, "p1"); got != "" {
		t.Errorf("a click in the drawer sent the pane %q, want nothing", got)
	}
}

// A click on a tree file jumps to that file's header and gives the diff the
// keyboard; the tree stays open, in a narrow drawer as in a wide one.
func TestClickOnATreeFileKeepsTheTreeOpen(t *testing.T) {
	t.Parallel()
	r, _ := fakeKeysRig(t)
	sized(t, r.App, 100, 30)

	v := r.opened(t)
	r.keys(keyTab)

	lay := r.layout(t)
	if lay.treeW == 0 || !r.drawer.tree {
		t.Fatalf("setup: tree width %d, tree has the keyboard = %v, want the tree shown", lay.treeW, r.drawer.tree)
	}

	z := slices.IndexFunc(v.derived().tree, func(it treeItem) bool { return !it.dir && v.res.Files[it.file].Path == "z.txt" })
	x, y := r.drawerCell(t, 2, 1+3+z)

	click(r.App, x, y)

	if fileAtCursor(v) != "z.txt" || v.rows[v.cursor].kind != rowFile {
		t.Errorf("a click on z.txt in the tree: cursor on %s (kind %d), want z.txt's header", fileAtCursor(v), v.rows[v.cursor].kind)
	}

	if r.drawer.tree || !r.drawer.focused || !treeIsOpen(r.App) || r.layout(t).treeW == 0 {
		t.Errorf("after the click: tree has the keyboard = %v, drawer has it = %v, tree open = %v, tree drawn = %v, want the diff to have it and the tree still shown",
			r.drawer.tree, r.drawer.focused, treeIsOpen(r.App), r.layout(t).treeW != 0)
	}
}

func TestWheelScrolls(t *testing.T) {
	t.Parallel()
	r, _ := fakeKeysRig(t)
	v := r.opened(t)
	lay := r.layout(t)
	x, y := r.drawerCell(t, 1+lay.treeW+1+8, 1+5)

	wheelAt(r.App, x, y, tea.MouseWheelDown)

	if v.top != 3 || v.cursor != 3 {
		t.Fatalf("one notch down: top = %d, cursor = %d, want 3 rows and the cursor with them", v.top, v.cursor)
	}

	wheelAt(r.App, x, y, tea.MouseWheelDown)
	wheelAt(r.App, x, y, tea.MouseWheelUp)

	if v.top != 3 || v.cursor != 6 {
		t.Errorf("down, down, up: top = %d, cursor = %d, want 3 and 6: the cursor stays while it is in view", v.top, v.cursor)
	}

	for range len(v.rows) {
		wheelAt(r.App, x, y, tea.MouseWheelDown)
	}

	if want := len(v.rows) - lay.bodyH; v.top != want || v.cursor < v.top || v.cursor >= v.top+lay.bodyH {
		t.Errorf("at the end: top = %d, cursor = %d, want top %d with the cursor in view", v.top, v.cursor, want)
	}

	if got := r.sentSoFar(t, "p1"); got != "" {
		t.Errorf("the wheel over the drawer sent the pane %q, want nothing", got)
	}
}

func TestClickOutsideKeepsTheDrawerOpen(t *testing.T) {
	t.Parallel()
	r, dir := fakeKeysRig(t)
	r.openShell(t, dir, core.SplitVertical)
	openIn(t, r.App, core.FocusPane{ID: "p1"})
	r.opened(t)

	x, y := centre(r.App, "p1")
	click(r.App, x, y)

	if r.drawer.focused || r.store.State().Tab().Review == nil || r.focus() != "p1" {
		t.Fatalf("a click on p1: drawer has the keyboard = %v, drawer open = %v, focus = %s, want the panes to have it and the drawer open",
			r.drawer.focused, r.store.State().Tab().Review != nil, r.focus())
	}

	// p2 lies under the drawer: a click on the drawer is the drawer's.
	x, y = r.drawerCell(t, mustRect(t, r).W/2, mustRect(t, r).H-3)
	click(r.App, x, y)

	if r.focus() != "p1" || !r.drawer.focused {
		t.Errorf("a click on the drawer over p2: focus = %s, drawer has the keyboard = %v, want p1 and true", r.focus(), r.drawer.focused)
	}
}
