package ui

import (
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/mhersson/aim/internal/core"
)

// sideBySide is a rig with a drawer on p1, which is focused and shown on the
// left, over an agent pane p2 that the drawer covers.
func sideBySide(t *testing.T) (*drawerRig, *reviewView) {
	t.Helper()

	r, _ := fakeKeysRig(t)
	r.run(r.openPane(core.KindAgent, core.SplitVertical))
	openIn(t, r.App, core.FocusPane{ID: "p1"})

	v := r.opened(t)

	if !r.drawer.focused || r.focus() != "p1" {
		t.Fatalf("setup: drawer has the keyboard = %v, focus = %s, want the drawer and p1", r.drawer.focused, r.focus())
	}

	return r, v
}

func TestOverlaysWhileTheDrawerHasTheKeyboard(t *testing.T) {
	t.Parallel()
	r, v := sideBySide(t)

	cases := []struct {
		name        string
		open, close []tea.KeyPressMsg
		closed      func() bool
	}{
		{"copy mode", []tea.KeyPressMsg{keyLeader, keyRune('[')}, []tea.KeyPressMsg{keyEsc}, func() bool { return r.copy == nil }},
		{"confirm", []tea.KeyPressMsg{keyLeader, keyRune('q')}, []tea.KeyPressMsg{keyRune('n')}, func() bool { return r.confirm == nil }},
		{"palette", []tea.KeyPressMsg{keyLeader, keyRune(':')}, []tea.KeyPressMsg{keyEsc}, func() bool { return r.overlay == nil }},
	}

	for _, c := range cases {
		r.keys(c.open...)

		at := v.cursor

		r.keys(keyRune('j'))

		if v.cursor != at {
			t.Fatalf("with %s open the drawer took j", c.name)
		}

		if c.name == "copy mode" && (r.copy == nil || r.copy.pane != "p1") {
			t.Fatalf("copy mode is on %v, want it on the shown pane p1", r.copy)
		}

		r.keys(c.close...)
		r.until(t, c.name+" to close", c.closed)

		if !r.drawer.focused || r.store.State().Tab().Review == nil {
			t.Fatalf("closing %s left the drawer open = %v with the keyboard = %v, want both", c.name, r.store.State().Tab().Review != nil, r.drawer.focused)
		}

		r.keys(keyRune('j'))

		if v.cursor != at+1 {
			t.Errorf("after %s the drawer's cursor went %d -> %d, want it to move down", c.name, at, v.cursor)
		}
	}
}

// Copy mode on a pane the drawer covers would have no owner on screen, so the
// drawer closes without a slide first, as for any action that focuses a covered pane.
func TestCopyModeOnACoveredPaneClosesTheDrawer(t *testing.T) {
	t.Parallel()
	r, _ := sideBySide(t)

	// The agent pane is the focused one when the drawer opens on it.
	r.drawerRelease()
	openIn(t, r.App, core.FocusPane{ID: "p2"})
	r.settle()

	if r.store.State().Tab().Review == nil || !r.drawer.focused {
		t.Fatalf("setup: drawer open = %v, has the keyboard = %v, want the drawer to have taken the keyboard from the covered p2",
			r.store.State().Tab().Review != nil, r.drawer.focused)
	}

	r.leader('[')

	if r.copy == nil || r.copy.pane != "p2" {
		t.Fatalf("copy mode = %v, want it on p2", r.copy)
	}

	if r.store.State().Tab().Review != nil || r.drawer.closing || r.drawer.focused {
		t.Errorf("drawer open = %v, closing = %v, has the keyboard = %v, want it closed at once", r.store.State().Tab().Review != nil, r.drawer.closing, r.drawer.focused)
	}

	r.keys(keyEsc)

	if r.copy != nil {
		t.Error("esc did not end copy mode")
	}
}

func TestKeyboardOwnerWhenPanesClose(t *testing.T) {
	t.Parallel()
	t.Run("the drawer's pane closes while the drawer has the keyboard", func(t *testing.T) {
		r, _ := sideBySide(t)

		r.run(r.closePane("p1"))
		r.settle()

		st := r.store.State()
		if st.Tab().Review != nil || r.drawer.focused || r.focus() != "p2" {
			t.Fatalf("drawer open = %v, has the keyboard = %v, focus = %s, want it closed with the keyboard on p2", st.Tab().Review != nil, r.drawer.focused, r.focus())
		}

		r.keys(keyRune('x'))
		eventually(t, "x to reach p2", func() bool { return r.sentTo("p2") == "x" })
	})

	t.Run("the operator closes the drawer's pane with leader x", func(t *testing.T) {
		r, _ := sideBySide(t)

		r.leader('x')

		st := r.store.State()
		if _, ok := st.Panes["p1"]; ok || st.Tab().Review != nil || r.drawer.focused {
			t.Fatalf("p1 exists = %v, drawer open = %v, has the keyboard = %v, want the pane gone and the drawer closed", ok, st.Tab().Review != nil, r.drawer.focused)
		}
	})

	t.Run("another pane closes", func(t *testing.T) {
		r, dir := fakeKeysRig(t)
		r.openShell(t, dir, core.SplitVertical)
		openIn(t, r.App, core.FocusPane{ID: "p1"})

		below := r.openShell(t, dir, core.SplitHorizontal)

		openIn(t, r.App, core.FocusPane{ID: "p1"})
		r.opened(t)

		r.run(r.closePane(below))
		r.settle()

		st := r.store.State()
		if st.Tab().Review == nil || st.Tab().Review.Pane != "p1" || !r.drawer.focused || r.focus() != "p1" {
			t.Errorf("drawer open = %v, has the keyboard = %v, focus = %s, want the drawer on p1 to keep the keyboard", st.Tab().Review != nil, r.drawer.focused, r.focus())
		}
	})

	t.Run("the focused pane closes and the drawer is not on it", func(t *testing.T) {
		r, dir := fakeKeysRig(t)
		r.openShell(t, dir, core.SplitVertical)
		openIn(t, r.App, core.FocusPane{ID: "p1"})

		below := r.openShell(t, dir, core.SplitHorizontal)

		openIn(t, r.App, core.FocusPane{ID: "p1"})
		r.opened(t)
		r.leader('h')

		openIn(t, r.App, core.FocusPane{ID: below})
		r.run(r.closePane(below))
		r.settle()

		st := r.store.State()
		if st.Tab().Review == nil || r.drawer.focused || r.focus() != "p1" {
			t.Errorf("drawer open = %v, has the keyboard = %v, focus = %s, want the drawer open and p1 to have the keyboard", st.Tab().Review != nil, r.drawer.focused, r.focus())
		}
	})
}

// A tab whose focused pane is under its drawer gives that drawer the keyboard
// when the tab shows again.
func TestTabSwitchToACoveredFocusGivesTheDrawerTheKeyboard(t *testing.T) {
	t.Parallel()
	r, dir := fakeKeysRig(t)
	r.openShell(t, dir, core.SplitVertical)
	r.opened(t)

	if r.focus() != "p2" || !r.drawer.focused {
		t.Fatalf("setup: focus = %s, drawer has the keyboard = %v, want the covered p2 and the drawer", r.focus(), r.drawer.focused)
	}

	r.leader('c')

	if r.drawer.focused {
		t.Fatal("a new tab left the keyboard with the first tab's drawer")
	}

	r.leader('1')

	st := r.store.State()
	if st.Active != 0 || st.Tab().Review == nil || !r.drawer.focused || r.drawer.closing || r.focus() != "p2" {
		t.Errorf("tab %d, drawer open = %v, has the keyboard = %v, closing = %v, focus = %s, want the first tab's drawer to have the keyboard",
			st.Active, st.Tab().Review != nil, r.drawer.focused, r.drawer.closing, r.focus())
	}
}

// A drawer restored into a hidden tab has no view until its tab shows. A pane
// moved into that tab and followed still closes the drawer when it lands under it.
func TestPaneMovedIntoARestoredDrawersTab(t *testing.T) {
	t.Parallel()
	r, dir := fakeKeysRig(t)

	openIn(t, r.App, core.OpenReview{Pane: "p1", Mode: core.ReviewBranch})
	r.settle()

	first := r.store.State().Tab().ID

	r.leader('c')
	r.openShell(t, dir, core.SplitVertical)

	// A restore leaves a hidden tab's drawer with no view.
	delete(r.drawer.views, first)
	r.settle()

	if len(r.drawer.views) != 0 {
		t.Fatalf("setup: %d views, want none", len(r.drawer.views))
	}

	moved := r.focus()

	r.keys(keyLeader, keyRune('>'), keyRune('1'))

	st := r.store.State()
	if st.Active != 0 || st.Tab().Focus != moved || st.Tab().Review != nil || r.drawer.focused {
		t.Errorf("tab %d, focus %s, drawer open = %v, has the keyboard = %v, want the moved pane focused and the drawer closed",
			st.Active, st.Tab().Focus, st.Tab().Review != nil, r.drawer.focused)
	}
}

// closeLandsCovered is a rig with a four pane master-stack tab and a drawer open
// on p1, the focus on a shown pane x whose close moves the focus to a pane y that
// the drawer then covers, and the keyboard with the panes. The pair is found from
// the layout, not assumed.
func closeLandsCovered(t *testing.T) (r *drawerRig, x, y core.PaneID) {
	t.Helper()

	var dir string

	r, dir = fakeKeysRig(t)

	openIn(t, r.App, core.SetLayoutMode{Mode: core.ModeMaster, Area: r.bodyArea()})

	for range 3 {
		r.openShell(t, dir, core.SplitVertical)
	}

	openIn(t, r.App, core.FocusPane{ID: "p1"})
	r.opened(t)

	st := r.store.State()
	box, _ := r.drawerFull(st)
	shown := r.layoutRects()

	for _, id := range st.Tab().Panes {
		if rect, ok := shown[id]; id == "p1" || !ok || covered(rect, box) {
			continue
		}

		sim := core.NewStore(st)
		if sim.Dispatch(core.FocusPane{ID: id}) != nil || sim.Dispatch(core.ClosePane{ID: id}) != nil {
			continue
		}

		after := sim.State()
		if rect, ok := core.Arrange(after.Tab(), r.bodyArea(), after.Layout).Rects[after.Tab().Focus]; ok && covered(rect, box) {
			x, y = id, after.Tab().Focus

			break
		}
	}

	if x == "" {
		t.Fatal("setup: no shown pane's close moves the focus under the drawer")
	}

	openIn(t, r.App, core.FocusPane{ID: x})
	r.drawerRelease()
	r.settle()

	if st := r.store.State(); st.Tab().Review == nil || r.drawer.focused || r.focus() != x {
		t.Fatalf("setup: drawer open = %v, has the keyboard = %v, focus = %s, want the drawer open, the panes holding the keyboard and focus on %s",
			st.Tab().Review != nil, r.drawer.focused, r.focus(), x)
	}

	return r, x, y
}

// When the focused pane closes and the next focus is under the drawer, the way it
// closes decides: a process that exits hands the drawer the keyboard, and a close
// the operator asks for is their action on a covered pane, so the drawer closes.
func TestFocusedPaneClosesOntoACoveredPane(t *testing.T) {
	t.Parallel()
	t.Run("the process exits", func(t *testing.T) {
		r, x, y := closeLandsCovered(t)

		r.run(r.closePane(x))
		r.settle()

		st := r.store.State()
		if st.Tab().Review == nil || !r.drawer.focused || r.focus() != y {
			t.Errorf("drawer open = %v, has the keyboard = %v, focus = %s, want the drawer open with the keyboard and focus on %s",
				st.Tab().Review != nil, r.drawer.focused, r.focus(), y)
		}
	})

	t.Run("the operator closes it", func(t *testing.T) {
		r, _, y := closeLandsCovered(t)

		r.leader('x')

		st := r.store.State()
		if st.Tab().Review != nil || r.drawer.focused || r.drawer.closing || r.focus() != y {
			t.Errorf("drawer open = %v, has the keyboard = %v, closing = %v, focus = %s, want the drawer closed at once and focus on %s",
				st.Tab().Review != nil, r.drawer.focused, r.drawer.closing, r.focus(), y)
		}
	})
}

// hiddenDrawerState is a saved state with a drawer open in the first tab while
// the second tab, two shells, is the active one.
func hiddenDrawerState() core.State {
	return core.State{
		Theme: "catppuccin-mocha", Active: 1, NextID: 3, NextTabID: 2,
		Tabs: []core.Tab{
			{
				ID: "t1", Focus: "p1", Mode: core.ModeBSP, Panes: []core.PaneID{"p1"}, Layout: &core.Node{Pane: "p1"},
				Review: &core.Review{Pane: "p1", Mode: core.ReviewBranch},
			},
			{ID: "t2", Focus: "p3", Mode: core.ModeBSP, Panes: []core.PaneID{"p2", "p3"}, Layout: vsplit("p2", "p3")},
		},
		Panes: map[core.PaneID]core.Pane{
			"p1": {Kind: core.KindShell, Cwd: "/srv/api"},
			"p2": {Kind: core.KindShell, Cwd: "/srv/web"},
			"p3": {Kind: core.KindShell, Cwd: "/srv/web"},
		},
	}
}

// Restored through New, a drawer in a hidden tab is known to the app only from
// the state it was given, with no view and no dispatch before the first key. A
// pane moved into that tab and followed still closes the drawer when it lands
// under it.
func TestPaneMovedIntoARestoredHiddenDrawersTab(t *testing.T) {
	t.Parallel()
	r := restoreApp(t, hiddenDrawerState())
	r.size(200, 50)

	if len(r.a.drawer.views) != 0 || r.a.store.State().Tabs[0].Review == nil {
		t.Fatalf("setup: %d views, first tab's drawer = %+v, want a drawer in the state and no view", len(r.a.drawer.views), r.a.store.State().Tabs[0].Review)
	}

	press(r.a, keyLeader, keyRune('>'), keyRune('1'))

	st := r.a.store.State()
	if st.Active != 0 || st.Tab().Focus != "p3" || st.Tab().Review != nil || r.a.drawer.focused || r.a.drawer.closing {
		t.Errorf("tab %d, focus %s, drawer open = %v, has the keyboard = %v, closing = %v, want the moved pane focused and the drawer closed at once",
			st.Active, st.Tab().Focus, st.Tab().Review != nil, r.a.drawer.focused, r.a.drawer.closing)
	}
}
