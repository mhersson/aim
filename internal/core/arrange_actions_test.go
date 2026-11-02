package core

import (
	"errors"
	"slices"
	"testing"
)

func must(t *testing.T, st *Store, a Action) {
	t.Helper()

	if err := st.Dispatch(a); err != nil {
		t.Fatalf("dispatch %T: %v", a, err)
	}
}

func storeOf(t *testing.T, mode LayoutMode, n int) *Store {
	t.Helper()

	return NewStore(openN(t, mode, n, defaultLayout()))
}

func TestPreselectOpensOnThatSide(t *testing.T) {
	for _, tc := range []struct {
		dir   Dir
		split Split
		axis  Split
		first bool
	}{
		{Left, SplitHorizontal, SplitVertical, true},
		{Right, SplitHorizontal, SplitVertical, false},
		{Up, SplitVertical, SplitHorizontal, true},
		{Down, SplitVertical, SplitHorizontal, false},
	} {
		st := storeOf(t, ModeBSP, 1)
		must(t, st, Preselect{Dir: tc.dir})
		must(t, st, OpenPane{Kind: KindShell, Split: tc.split, Area: modeInner})

		tab := st.State().Tabs[0]
		if tab.Preselect != NoDir {
			t.Errorf("%v: preselect not cleared", tc.dir)
		}

		l := tab.Layout
		if l.Split != tc.axis || l.Ratio != 0.5 {
			t.Fatalf("%v: root = %+v", tc.dir, l)
		}

		want := PaneID("p2")

		got := l.B.Pane
		if tc.first {
			got = l.A.Pane
		}

		if got != want {
			t.Errorf("%v: new pane on wrong side: %+v", tc.dir, l)
		}

		if tab.Focus != "p2" {
			t.Errorf("%v: focus = %s", tc.dir, tab.Focus)
		}
	}
}

func TestPreselectSurvivesFocusAndAppliesToNewFocus(t *testing.T) {
	st := storeOf(t, ModeBSP, 2)
	must(t, st, FocusPane{ID: "p1"})
	must(t, st, Preselect{Dir: Up})
	must(t, st, FocusDir{Dir: Right, Area: modeInner})

	if st.State().Tabs[0].Preselect != Up {
		t.Fatal("focus move dropped the preselection")
	}

	must(t, st, OpenPane{Kind: KindAgent, Harness: "claude-code", Split: SplitVertical, Area: modeInner})

	l := st.State().Tabs[0].Layout
	if l.B.Split != SplitHorizontal || l.B.A.Pane != "p3" || l.B.B.Pane != "p2" {
		t.Fatalf("new pane not above the focused one: %+v", l.B)
	}
}

func TestPreselectOnEmptyTab(t *testing.T) {
	st := NewStore(InitialState(DefaultConfig()))
	must(t, st, Preselect{Dir: Left})
	must(t, st, OpenPane{Kind: KindShell, Area: modeInner})

	tab := st.State().Tabs[0]
	if tab.Preselect != NoDir || tab.Layout == nil || !tab.Layout.leaf() {
		t.Fatalf("tab = %+v", tab)
	}
}

func TestClearPreselect(t *testing.T) {
	st := storeOf(t, ModeBSP, 1)
	must(t, st, ClearPreselect{})
	must(t, st, Preselect{Dir: Down})
	must(t, st, ClearPreselect{})

	if st.State().Tabs[0].Preselect != NoDir {
		t.Fatal("not cleared")
	}

	must(t, st, OpenPane{Kind: KindShell, Split: SplitVertical, Area: modeInner})

	if l := st.State().Tabs[0].Layout; l.Split != SplitVertical {
		t.Fatalf("cleared preselection still applied: %+v", l)
	}
}

func TestLayoutActionsOutsideTheirMode(t *testing.T) {
	for _, mode := range []LayoutMode{ModeMaster, ModeScrolling} {
		st := storeOf(t, mode, 2)
		for _, a := range []Action{Preselect{Dir: Left}, ClearPreselect{}, RotateSplit{}} {
			if err := st.Dispatch(a); !errors.Is(err, ErrNeedsBSP) {
				t.Errorf("%s %T: err = %v", mode, a, err)
			}
		}
	}

	for _, mode := range []LayoutMode{ModeBSP, ModeScrolling} {
		st := storeOf(t, mode, 2)
		if err := st.Dispatch(Promote{}); !errors.Is(err, ErrNeedsMaster) {
			t.Errorf("%s promote: err = %v", mode, err)
		}
	}

	for _, mode := range allModes {
		st := storeOf(t, mode, 2)
		if err := st.Dispatch(Equalize{}); err != nil {
			t.Errorf("%s equalize: %v", mode, err)
		}
	}
}

func TestRotateSplit(t *testing.T) {
	st := storeOf(t, ModeBSP, 2)
	must(t, st, ResizeDir{Dir: Right})
	must(t, st, RotateSplit{})

	l := st.State().Tabs[0].Layout
	if l.Split != SplitHorizontal || l.Ratio != 0.55 {
		t.Fatalf("root = %+v", l)
	}

	must(t, st, RotateSplit{})

	if l := st.State().Tabs[0].Layout; l.Split != SplitVertical || l.Ratio != 0.55 {
		t.Fatalf("root = %+v", l)
	}

	for _, n := range []int{0, 1} {
		st := storeOf(t, ModeBSP, n)
		if err := st.Dispatch(RotateSplit{}); !errors.Is(err, ErrNothingToRotate) {
			t.Errorf("%d panes: err = %v", n, err)
		}
	}
}

func TestRotateEqualizePromoteClearZoom(t *testing.T) {
	for name, tc := range map[string]struct {
		mode LayoutMode
		a    Action
	}{
		"rotate":   {ModeBSP, RotateSplit{}},
		"equalize": {ModeBSP, Equalize{}},
		"promote":  {ModeMaster, Promote{}},
	} {
		st := storeOf(t, tc.mode, 2)
		must(t, st, ToggleZoom{})
		must(t, st, tc.a)

		if st.State().Tabs[0].Zoomed {
			t.Errorf("%s: zoom kept", name)
		}
	}

	st := storeOf(t, ModeBSP, 1)
	must(t, st, ToggleZoom{})
	must(t, st, Equalize{})

	if err := st.Dispatch(RotateSplit{}); !errors.Is(err, ErrNothingToRotate) || !st.State().Tabs[0].Zoomed {
		t.Fatalf("no-op changed zoom: %v", err)
	}
}

func spread(v []int) int { return slices.Max(v) - slices.Min(v) }

func TestEqualizeBSP(t *testing.T) {
	t.Run("grid of four", func(t *testing.T) {
		st := storeOf(t, ModeBSP, 1)
		must(t, st, OpenPane{Kind: KindShell, Split: SplitVertical, Area: modeInner})
		must(t, st, OpenPane{Kind: KindShell, Split: SplitHorizontal, Area: modeInner})
		must(t, st, FocusPane{ID: "p1"})
		must(t, st, OpenPane{Kind: KindShell, Split: SplitHorizontal, Area: modeInner})
		must(t, st, ResizeDir{Dir: Right})
		must(t, st, ResizeDir{Dir: Down})
		must(t, st, Equalize{})
		s := st.State()
		a := Arrange(s.Tabs[0], modeInner, s.Layout)

		var ws, hs []int
		for _, id := range s.Tabs[0].Panes {
			ws, hs = append(ws, a.Rects[id].W), append(hs, a.Rects[id].H)
		}

		if len(ws) != 4 || spread(ws) > 1 || spread(hs) > 1 {
			t.Fatalf("widths %v heights %v", ws, hs)
		}
	})
	t.Run("nested non-chain", func(t *testing.T) {
		// Ratios round to 0.01, so 1/3 becomes 0.33 and, with no gap cells to
		// absorb it, some widths spread by 2; 100 keeps the one-cell check meaningful.
		s := stateOf(vsplit(leaf("p1"), hsplit(vsplit(leaf("p2"), leaf("p3")), vsplit(leaf("p4"), leaf("p5")))), "p1")
		st := NewStore(s)
		must(t, st, Equalize{})
		s = st.State()
		a := Arrange(s.Tabs[0], Rect{0, 0, 100, 40}, s.Layout)

		var ws []int
		for _, id := range s.Tabs[0].Panes {
			ws = append(ws, a.Rects[id].W)
		}

		if len(ws) != 5 || spread(ws) > 1 {
			t.Fatalf("widths = %v", ws)
		}
	})
	t.Run("ratios", func(t *testing.T) {
		st := storeOf(t, ModeBSP, 3)
		must(t, st, Equalize{})

		l := st.State().Tabs[0].Layout
		if l.Ratio != 0.33 || l.B.Ratio != 0.5 {
			t.Fatalf("ratios %v %v", l.Ratio, l.B.Ratio)
		}
	})
	t.Run("lone panes", func(t *testing.T) {
		for n := range 2 {
			st := storeOf(t, ModeBSP, n)
			before := st.State().Tabs[0]
			must(t, st, Equalize{})

			after := st.State().Tabs[0]
			if after.Zoomed != before.Zoomed || !slices.Equal(after.Panes, before.Panes) {
				t.Errorf("%d panes: changed", n)
			}
		}
	})
}

func TestEqualizeMasterAndScrolling(t *testing.T) {
	lc := defaultLayout()
	lc.MasterRatio, lc.ColumnWidth = 60, 40

	st := NewStore(openN(t, ModeMaster, 3, lc))
	must(t, st, ResizeDir{Dir: Right})
	must(t, st, Equalize{})

	if m := st.State().Tabs[0].Master; m != 0.6 {
		t.Fatalf("master = %v", m)
	}

	s := openN(t, ModeScrolling, 5, lc)
	st = NewStore(s)
	must(t, st, ResizeDir{Dir: Right})
	must(t, st, Equalize{})

	tab := st.State().Tabs[0]
	for _, c := range tab.Columns {
		if c.Width != 0.4 {
			t.Fatalf("columns = %+v", tab.Columns)
		}
	}

	c, _, _ := tab.colOf(tab.Focus)
	if c < tab.First || c >= tab.runEnd(tab.First) {
		t.Fatalf("focused column %d not visible from %d", c, tab.First)
	}
}

func TestPromote(t *testing.T) {
	st := storeOf(t, ModeMaster, 3)
	must(t, st, Promote{})

	tab := st.State().Tabs[0]
	if !slices.Equal(tab.Panes, []PaneID{"p3", "p2", "p1"}) || tab.Focus != "p3" {
		t.Fatalf("tab = %+v", tab)
	}

	must(t, st, Promote{})

	tab = st.State().Tabs[0]
	if !slices.Equal(tab.Panes, []PaneID{"p2", "p3", "p1"}) || tab.Focus != "p2" {
		t.Fatalf("tab = %+v", tab)
	}

	st = storeOf(t, ModeMaster, 1)
	must(t, st, Promote{})

	if tab := st.State().Tabs[0]; len(tab.Panes) != 1 || tab.Focus != "p1" {
		t.Fatalf("tab = %+v", tab)
	}
}

func TestModeSwitchClearsPreselection(t *testing.T) {
	st := NewStore(InitialState(DefaultConfig()))
	must(t, st, Preselect{Dir: Left})
	must(t, st, SetLayoutMode{Mode: ModeMaster, Area: modeInner})

	if err := st.Dispatch(Preselect{Dir: Left}); !errors.Is(err, ErrNeedsBSP) {
		t.Fatalf("err = %v", err)
	}
}
