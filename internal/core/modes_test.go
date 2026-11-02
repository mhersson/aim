package core

import (
	"errors"
	"fmt"
	"maps"
	"slices"
	"testing"
)

var modeInner = Rect{1, 1, 98, 27}

var allModes = []LayoutMode{ModeBSP, ModeMaster, ModeScrolling}

// openN is a state whose first tab holds n shell panes in mode, each opened
// beside the last with a vertical split (a column, in scrolling).
func openN(t *testing.T, mode LayoutMode, n int, lc LayoutConfig) State {
	t.Helper()

	cfg := DefaultConfig()
	cfg.Layout = lc
	cfg.Layout.DefaultMode = string(mode)
	s := InitialState(cfg)

	st := NewStore(s)
	for range n {
		if err := st.Dispatch(OpenPane{Kind: KindShell, Split: SplitVertical, Area: modeInner}); err != nil {
			t.Fatalf("open pane: %v", err)
		}
	}

	return st.State()
}

func defaultLayout() LayoutConfig { return DefaultConfig().Layout }

func columnsOf(t Tab) [][]PaneID {
	var out [][]PaneID
	for _, c := range t.Columns {
		out = append(out, c.Panes)
	}

	return out
}

// checkArrangement asserts the drawing rules in inner: every rect is inside
// inner, at least minPane square and disjoint from the others; Sizes covers Rects; a pane
// missing from Rects is missing from Sizes unless its scrolling column is
// hidden.
func checkArrangement(t *testing.T, tab Tab, inner Rect, lc LayoutConfig) Arrangement {
	t.Helper()

	a := Arrange(tab, inner, lc)
	label := fmt.Sprintf("%s %d panes in %+v", tab.Mode, len(tab.Panes), inner)

	ids := slices.Sorted(func(yield func(PaneID) bool) {
		for id := range a.Rects {
			if !yield(id) {
				return
			}
		}
	})
	for i, id := range ids {
		r := a.Rects[id]
		if r.W < minPane || r.H < minPane {
			t.Fatalf("%s: %s has rect %+v", label, id, r)
		}

		if r.X < inner.X || r.Y < inner.Y || r.X+r.W > inner.X+inner.W || r.Y+r.H > inner.Y+inner.H {
			t.Fatalf("%s: %s rect %+v outside inner", label, id, r)
		}

		if a.Sizes[id] != r {
			t.Fatalf("%s: %s Sizes %+v != Rects %+v", label, id, a.Sizes[id], r)
		}

		for _, o := range ids[i+1:] {
			q := a.Rects[o]
			if r.X < q.X+q.W && q.X < r.X+r.W && r.Y < q.Y+q.H && q.Y < r.Y+r.H {
				t.Fatalf("%s: %s %+v overlaps %s %+v", label, id, r, o, q)
			}
		}
	}

	hidden := map[PaneID]bool{}

	if tab.Mode == ModeScrolling {
		for i, c := range tab.Columns {
			if i < a.HiddenLeft || i >= len(tab.Columns)-a.HiddenRight {
				for _, id := range c.Panes {
					hidden[id] = true
				}
			}
		}
	}

	for _, id := range tab.Panes {
		if _, drawn := a.Rects[id]; drawn {
			continue
		}

		if _, sized := a.Sizes[id]; sized && !hidden[id] {
			t.Fatalf("%s: %s is not drawn but has a size", label, id)
		}
	}

	for id, r := range a.Sizes {
		if r.W < 1 || r.H < 1 {
			t.Fatalf("%s: %s size %+v is empty", label, id, r)
		}

		if !slices.Contains(tab.Panes, id) {
			t.Fatalf("%s: size for unknown pane %s", label, id)
		}
	}

	return a
}

// TestEveryModeLaysOutWithoutOverlap lays 1, 3 and 64 panes out in every mode
// at two body sizes: rects touch but never overlap, and the few panes that fit
// are all drawn.
func TestEveryModeLaysOutWithoutOverlap(t *testing.T) {
	bodies := []Rect{{0, 1, 80, 24}, {0, 1, 20, 6}}

	for _, mode := range allModes {
		for _, n := range []int{1, 3, 64} {
			for _, body := range bodies {
				t.Run(fmt.Sprintf("%s/%d/%dx%d", mode, n, body.W, body.H), func(t *testing.T) {
					s := openN(t, mode, n, defaultLayout())

					a := checkArrangement(t, s.Tab(), body, s.Layout)
					if fits := n <= 4 && body.W >= 80; fits && mode != ModeScrolling && len(a.Rects) != n {
						t.Errorf("drew %d of %d panes", len(a.Rects), n)
					}
				})
			}
		}
	}
}

func TestMasterStackGeometry(t *testing.T) {
	s := openN(t, ModeMaster, 3, defaultLayout())
	a := Arrange(s.Tab(), modeInner, s.Layout)

	p1 := a.Rects["p1"]
	if p1.X != modeInner.X || p1.H != modeInner.H {
		t.Errorf("master rect %+v: want the full height at the left edge", p1)
	}

	for _, id := range []PaneID{"p2", "p3"} {
		if r := a.Rects[id]; r.X < p1.X+p1.W {
			t.Errorf("%s rect %+v is not right of the master %+v", id, r, p1)
		}
	}

	if a.Rects["p3"].Y <= a.Rects["p2"].Y {
		t.Errorf("stack out of order: p2 %+v p3 %+v", a.Rects["p2"], a.Rects["p3"])
	}

	if s.Tab().Master != 0.5 {
		t.Errorf("Master = %v, want the configured 0.5", s.Tab().Master)
	}
}

func TestMasterStackConfig(t *testing.T) {
	lc := defaultLayout()
	lc.MasterPosition, lc.MasterCount, lc.MasterRatio, lc.MasterGrid = "right", 2, 70, new(false)

	s := openN(t, ModeMaster, 4, lc)
	if s.Tab().Master != 0.7 {
		t.Errorf("Master = %v, want 0.7", s.Tab().Master)
	}

	a := Arrange(s.Tab(), modeInner, s.Layout)
	if a.Rects["p1"].X != a.Rects["p2"].X || a.Rects["p1"].X <= a.Rects["p3"].X {
		t.Errorf("two masters on the right: p1 %+v p2 %+v p3 %+v", a.Rects["p1"], a.Rects["p2"], a.Rects["p3"])
	}
}

func TestScrollingVisibleColumns(t *testing.T) {
	s := openN(t, ModeScrolling, 3, defaultLayout())
	if got := columnsOf(s.Tab()); fmt.Sprint(got) != "[[p1] [p2] [p3]]" {
		t.Fatalf("columns = %v", got)
	}
	// The new pane is focused, so the run scrolled to keep it in view.
	a := checkArrangement(t, s.Tab(), modeInner, s.Layout)
	if a.HiddenLeft != 1 || a.HiddenRight != 0 || len(a.Rects) != 2 {
		t.Errorf("focus p3: hidden %d/%d, drew %d", a.HiddenLeft, a.HiddenRight, len(a.Rects))
	}

	s = dispatch(t, s, FocusPane{ID: "p1"})

	a = checkArrangement(t, s.Tab(), modeInner, s.Layout)
	if a.HiddenLeft != 0 || a.HiddenRight != 1 || len(a.Rects) != 2 {
		t.Errorf("focus p1: hidden %d/%d, drew %d", a.HiddenLeft, a.HiddenRight, len(a.Rects))
	}

	if _, ok := a.Rects["p3"]; ok {
		t.Error("hidden pane p3 is drawn")
	}
	// Hidden panes are sized as their column alone at its nominal width.
	want := Rect{X: modeInner.X, Y: modeInner.Y, W: 49, H: modeInner.H}
	if got := a.Sizes["p3"]; got.W != want.W || got.H != want.H {
		t.Errorf("hidden p3 size %+v, want width %d height %d", got, want.W, want.H)
	}

	s = dispatch(t, s, FocusPane{ID: "p3"})

	a = Arrange(s.Tab(), modeInner, s.Layout)
	if a.HiddenLeft != 1 || a.HiddenRight != 0 {
		t.Errorf("focus p3: hidden %d/%d, want 1/0", a.HiddenLeft, a.HiddenRight)
	}

	if got := a.Sizes["p1"]; got.W != 49 {
		t.Errorf("hidden p1 width %d, want round(0.5 x 98) = 49", got.W)
	}
}

func TestScrollingHiddenColumnIsAtLeastOneFramedCell(t *testing.T) {
	tab := Tab{
		Mode: ModeScrolling, Focus: "p1", Panes: []PaneID{"p1", "p2", "p3", "p4"},
		Columns: []Column{{Panes: []PaneID{"p1"}, Width: 0.95}, {Panes: []PaneID{"p2"}, Width: 0.1}, {Panes: []PaneID{"p3", "p4"}, Width: 0.1}},
	}

	a := Arrange(tab, Rect{0, 1, 20, 9}, LayoutConfig{})
	if a.HiddenRight != 2 || len(a.Rects) != 1 {
		t.Fatalf("hidden %d/%d, drew %v", a.HiddenLeft, a.HiddenRight, a.Rects)
	}

	for id, wantH := range map[PaneID]int{"p2": 9, "p3": 5, "p4": 4} {
		if got := a.Sizes[id]; got.W != minPane || got.H != wantH {
			t.Errorf("hidden %s size %+v, want %dx%d", id, got, minPane, wantH)
		}
	}
}

func TestScrollingVisibleColumnsStretch(t *testing.T) {
	lc := defaultLayout()
	lc.ColumnWidth = 30
	s := openN(t, ModeScrolling, 3, lc)
	s = dispatch(t, s, FocusPane{ID: "p1"})

	a := checkArrangement(t, s.Tab(), modeInner, s.Layout)
	if a.HiddenRight != 0 || len(a.Rects) != 3 {
		t.Fatalf("hidden %d/%d, drew %d, want all three", a.HiddenLeft, a.HiddenRight, len(a.Rects))
	}

	last := a.Rects["p3"]
	if last.X+last.W != modeInner.X+modeInner.W {
		t.Errorf("last column ends at %d, want the body edge %d", last.X+last.W, modeInner.X+modeInner.W)
	}

	if a.Rects["p1"].X+a.Rects["p1"].W != a.Rects["p2"].X {
		t.Errorf("columns do not touch: %+v %+v", a.Rects["p1"], a.Rects["p2"])
	}
}

func TestScrollingSplitAddsBelow(t *testing.T) {
	s := openN(t, ModeScrolling, 2, defaultLayout())
	s = dispatch(t, s, FocusPane{ID: "p1"})

	s = dispatch(t, s, OpenPane{Kind: KindShell, Split: SplitHorizontal, Area: modeInner})
	if got := fmt.Sprint(columnsOf(s.Tab())); got != "[[p1 p3] [p2]]" {
		t.Errorf("columns = %s, want p3 below p1", got)
	}

	if !slices.Equal(s.Tab().Panes, []PaneID{"p1", "p3", "p2"}) {
		t.Errorf("Panes = %v", s.Tab().Panes)
	}
	// A column opens after the focused column, not at the end.
	s = dispatch(t, s, FocusPane{ID: "p1"})

	s = dispatch(t, s, OpenPane{Kind: KindShell, Split: SplitVertical, Area: modeInner})
	if got := fmt.Sprint(columnsOf(s.Tab())); got != "[[p1 p3] [p4] [p2]]" {
		t.Errorf("columns = %s, want p4 after the focused column", got)
	}
}

func TestOpenAppendsInMasterStack(t *testing.T) {
	s := openN(t, ModeMaster, 3, defaultLayout())
	s = dispatch(t, s, FocusPane{ID: "p1"})

	s = dispatch(t, s, OpenPane{Kind: KindShell, Split: SplitHorizontal, Area: modeInner})
	if !slices.Equal(s.Tab().Panes, []PaneID{"p1", "p2", "p3", "p4"}) || s.Tab().Focus != "p4" {
		t.Errorf("Panes = %v, Focus = %s", s.Tab().Panes, s.Tab().Focus)
	}
}

func TestCloseFocusRules(t *testing.T) {
	t.Run("master-stack passes focus to the pane now at the index", func(t *testing.T) {
		s := openN(t, ModeMaster, 4, defaultLayout())
		s = dispatch(t, s, FocusPane{ID: "p2"})

		s = dispatch(t, s, ClosePane{ID: "p2"})
		if s.Tab().Focus != "p3" || !slices.Equal(s.Tab().Panes, []PaneID{"p1", "p3", "p4"}) {
			t.Errorf("Focus = %s, Panes = %v", s.Tab().Focus, s.Tab().Panes)
		}

		s = dispatch(t, s, FocusPane{ID: "p4"})

		s = dispatch(t, s, ClosePane{ID: "p4"})
		if s.Tab().Focus != "p3" {
			t.Errorf("last pane closed: Focus = %s, want p3", s.Tab().Focus)
		}

		s = dispatch(t, s, ClosePane{ID: "p1"})
		if s.Tab().Focus != "p3" {
			t.Errorf("unfocused close: Focus = %s, want p3", s.Tab().Focus)
		}
	})
	t.Run("scrolling", func(t *testing.T) {
		build := func() State {
			s := openN(t, ModeScrolling, 3, defaultLayout()) // [p1] [p2] [p3]
			s = dispatch(t, s, FocusPane{ID: "p2"})
			s = dispatch(t, s, OpenPane{Kind: KindShell, Split: SplitHorizontal, Area: modeInner}) // [p2 p4]
			s = dispatch(t, s, FocusPane{ID: "p2"})

			return dispatch(t, s, OpenPane{Kind: KindShell, Split: SplitHorizontal, Area: modeInner}) // [p2 p5 p4]
		}

		s := build()
		if got := fmt.Sprint(columnsOf(s.Tab())); got != "[[p1] [p2 p5 p4] [p3]]" {
			t.Fatalf("columns = %s", got)
		}
		// Middle of a column: the pane now at that index.
		s1 := dispatch(t, s, FocusPane{ID: "p5"})

		s1 = dispatch(t, s1, ClosePane{ID: "p5"})
		if s1.Tab().Focus != "p4" {
			t.Errorf("middle close: Focus = %s, want p4", s1.Tab().Focus)
		}
		// Bottom of a column: the one above.
		s2 := dispatch(t, s, FocusPane{ID: "p4"})

		s2 = dispatch(t, s2, ClosePane{ID: "p4"})
		if s2.Tab().Focus != "p5" {
			t.Errorf("bottom close: Focus = %s, want p5", s2.Tab().Focus)
		}
		// Only pane of a column: the column to the left, else to the right.
		s3 := dispatch(t, s, FocusPane{ID: "p3"})

		s3 = dispatch(t, s3, ClosePane{ID: "p3"})
		if s3.Tab().Focus == "" || !slices.Contains([]PaneID{"p2", "p5", "p4"}, s3.Tab().Focus) {
			t.Errorf("last column close: Focus = %s, want a pane of the left column", s3.Tab().Focus)
		}

		s4 := dispatch(t, s, FocusPane{ID: "p1"})

		s4 = dispatch(t, s4, ClosePane{ID: "p1"})
		if !slices.Contains([]PaneID{"p2", "p5", "p4"}, s4.Tab().Focus) {
			t.Errorf("first column close: Focus = %s, want a pane of the right column", s4.Tab().Focus)
		}

		if got := fmt.Sprint(columnsOf(s4.Tab())); got != "[[p2 p5 p4] [p3]]" {
			t.Errorf("columns after closing p1 = %s", got)
		}
		// An unfocused close keeps focus.
		s5 := dispatch(t, s, FocusPane{ID: "p5"})

		s5 = dispatch(t, s5, ClosePane{ID: "p1"})
		if s5.Tab().Focus != "p5" {
			t.Errorf("unfocused close: Focus = %s, want p5", s5.Tab().Focus)
		}
	})
}

func TestScrollingFocusDir(t *testing.T) {
	s := openN(t, ModeScrolling, 3, defaultLayout())
	s = dispatch(t, s, FocusPane{ID: "p2"})
	s = dispatch(t, s, OpenPane{Kind: KindShell, Split: SplitHorizontal, Area: modeInner}) // [p1] [p2 p4] [p3]

	s = dispatch(t, s, FocusDir{Dir: Right, Area: modeInner})
	if s.Tab().Focus != "p3" {
		t.Fatalf("right from p4: Focus = %s, want p3 (same index clamped)", s.Tab().Focus)
	}

	s = dispatch(t, s, FocusDir{Dir: Right, Area: modeInner})
	if s.Tab().Focus != "p3" {
		t.Errorf("right at the last column: Focus = %s", s.Tab().Focus)
	}

	s = dispatch(t, s, FocusDir{Dir: Left, Area: modeInner})
	if s.Tab().Focus != "p2" {
		t.Errorf("left from p3 (index 0): Focus = %s, want p2", s.Tab().Focus)
	}

	s = dispatch(t, s, FocusDir{Dir: Down, Area: modeInner})
	if s.Tab().Focus != "p4" {
		t.Errorf("down in the column: Focus = %s, want p4", s.Tab().Focus)
	}

	s = dispatch(t, s, FocusDir{Dir: Down, Area: modeInner})
	if s.Tab().Focus != "p4" {
		t.Errorf("down at the bottom: Focus = %s", s.Tab().Focus)
	}

	s = dispatch(t, s, FocusDir{Dir: Left, Area: modeInner})
	if s.Tab().Focus != "p1" {
		t.Errorf("left from p4 (index 1) into a one-pane column: Focus = %s, want p1", s.Tab().Focus)
	}

	if s.Tab().First != 0 {
		t.Errorf("First = %d, want 0 with p1 focused", s.Tab().First)
	}
	// Up and down never reach a pane of a hidden column.
	s = dispatch(t, s, FocusDir{Dir: Down, Area: modeInner})
	if s.Tab().Focus != "p1" {
		t.Errorf("down in a one-pane column: Focus = %s", s.Tab().Focus)
	}
}

func TestScrollingFirstFollowsFocus(t *testing.T) {
	s := openN(t, ModeScrolling, 5, defaultLayout()) // widths 0.5: two columns fit
	if s.Tab().First != 3 {
		t.Errorf("after opening five: First = %d, want 3", s.Tab().First)
	}

	for _, tt := range []struct {
		do    Action
		first int
	}{
		{FocusPane{ID: "p3"}, 2},                   // left of First
		{FocusPane{ID: "p2"}, 1},                   // left of First
		{FocusPane{ID: "p1"}, 0},                   // left of First
		{FocusPane{ID: "p2"}, 0},                   // still in the run
		{FocusPane{ID: "p5"}, 3},                   // right of the run: the smallest First that fits
		{FocusDir{Dir: Left, Area: modeInner}, 3},  // p4 is in the run
		{FocusDir{Dir: Left, Area: modeInner}, 2},  // p3 is left of First
		{FocusDir{Dir: Right, Area: modeInner}, 2}, // p4 is in the run
		{FocusDir{Dir: Right, Area: modeInner}, 3}, // p5 is right of the run
		{ResizeDir{Dir: Right}, 4},                 // 0.55 wide: p4 and p5 no longer fit together
		{ResizeDir{Dir: Left}, 4},                  // First moves only as far as needed
	} {
		s = dispatch(t, s, tt.do)
		if got := s.Tab().First; got != tt.first {
			t.Errorf("after %T %+v: First = %d, want %d (focus %s)", tt.do, tt.do, got, tt.first, s.Tab().Focus)
		}
	}
}

func TestScrollingCloseLeftOfFirstKeepsVisibleSet(t *testing.T) {
	s := openN(t, ModeScrolling, 4, defaultLayout()) // p3 and p4 visible, focus p4
	if s.Tab().First != 2 || s.Tab().Focus != "p4" {
		t.Fatalf("setup: First %d, Focus %s", s.Tab().First, s.Tab().Focus)
	}

	s = dispatch(t, s, ClosePane{ID: "p1"})
	if got := s.Tab().First; got != 1 || s.Tab().Columns[got].Panes[0] != "p3" {
		t.Errorf("First = %d, columns = %v", got, columnsOf(s.Tab()))
	}
}

func TestSwap(t *testing.T) {
	t.Run("master-stack swaps with the neighbour in the order", func(t *testing.T) {
		s := openN(t, ModeMaster, 3, defaultLayout()) // p1 left; p2 above p3
		s = dispatch(t, s, FocusPane{ID: "p2"})

		s = dispatch(t, s, SwapDir{Dir: Left, Area: modeInner})
		if !slices.Equal(s.Tab().Panes, []PaneID{"p2", "p1", "p3"}) || s.Tab().Focus != "p2" {
			t.Errorf("Panes = %v, Focus = %s", s.Tab().Panes, s.Tab().Focus)
		}
	})
	t.Run("scrolling across columns", func(t *testing.T) {
		s := openN(t, ModeScrolling, 2, defaultLayout())

		s = dispatch(t, s, SwapDir{Dir: Left, Area: modeInner})
		if got := fmt.Sprint(columnsOf(s.Tab())); got != "[[p2] [p1]]" || s.Tab().Focus != "p2" {
			t.Errorf("columns = %s, Focus = %s", got, s.Tab().Focus)
		}

		if !slices.Equal(s.Tab().Panes, []PaneID{"p2", "p1"}) {
			t.Errorf("Panes = %v", s.Tab().Panes)
		}
	})
	t.Run("scrolling within a column", func(t *testing.T) {
		s := openN(t, ModeScrolling, 1, defaultLayout())
		s = dispatch(t, s, OpenPane{Kind: KindShell, Split: SplitHorizontal, Area: modeInner})

		s = dispatch(t, s, SwapDir{Dir: Up, Area: modeInner})
		if got := fmt.Sprint(columnsOf(s.Tab())); got != "[[p2 p1]]" || s.Tab().Focus != "p2" {
			t.Errorf("columns = %s, Focus = %s", got, s.Tab().Focus)
		}
	})
	t.Run("scrolling keeps the moved pane in view", func(t *testing.T) {
		s := openN(t, ModeScrolling, 4, defaultLayout()) // focus p4, First 2
		s = dispatch(t, s, SwapDir{Dir: Left, Area: modeInner})

		s = dispatch(t, s, SwapDir{Dir: Left, Area: modeInner})
		if got := fmt.Sprint(columnsOf(s.Tab())); got != "[[p1] [p4] [p2] [p3]]" {
			t.Fatalf("columns = %s", got)
		}

		a := Arrange(s.Tab(), modeInner, s.Layout)
		if _, ok := a.Rects["p4"]; !ok {
			t.Errorf("focused p4 is not drawn, First = %d", s.Tab().First)
		}
	})
}

func TestResize(t *testing.T) {
	t.Run("master ratio on its axis", func(t *testing.T) {
		s := openN(t, ModeMaster, 3, defaultLayout())
		before := Arrange(s.Tab(), modeInner, s.Layout).Rects["p1"].W

		s = dispatch(t, s, ResizeDir{Dir: Right})
		if got := s.Tab().Master; got != 0.55 {
			t.Errorf("Master = %v, want 0.55", got)
		}

		if after := Arrange(s.Tab(), modeInner, s.Layout).Rects["p1"].W; after <= before {
			t.Errorf("master width %d -> %d, want wider", before, after)
		}

		s = dispatch(t, s, ResizeDir{Dir: Down})
		if s.Tab().Master != 0.55 {
			t.Errorf("Down moved the ratio: %v", s.Tab().Master)
		}

		for range 20 {
			s = dispatch(t, s, ResizeDir{Dir: Left})
		}

		if s.Tab().Master != 0.1 {
			t.Errorf("Master = %v, want the 0.1 floor", s.Tab().Master)
		}

		for range 30 {
			s = dispatch(t, s, ResizeDir{Dir: Right})
		}

		if s.Tab().Master != 0.9 {
			t.Errorf("Master = %v, want the 0.9 ceiling", s.Tab().Master)
		}
	})
	t.Run("master on a row axis", func(t *testing.T) {
		lc := defaultLayout()
		lc.MasterPosition = "top"
		s := openN(t, ModeMaster, 3, lc)

		s = dispatch(t, s, ResizeDir{Dir: Right})
		if s.Tab().Master != 0.5 {
			t.Errorf("Right moved a row split: %v", s.Tab().Master)
		}

		s = dispatch(t, s, ResizeDir{Dir: Down})
		if s.Tab().Master != 0.55 {
			t.Errorf("Master = %v, want 0.55", s.Tab().Master)
		}
	})
	t.Run("master on the far side grows the left region", func(t *testing.T) {
		lc := defaultLayout()
		lc.MasterPosition = "right"
		s := openN(t, ModeMaster, 3, lc)

		s = dispatch(t, s, ResizeDir{Dir: Right})
		if s.Tab().Master != 0.45 {
			t.Errorf("Master = %v, want 0.45 (the stack on the left grew)", s.Tab().Master)
		}
	})
	t.Run("scrolling column width", func(t *testing.T) {
		s := openN(t, ModeScrolling, 2, defaultLayout())

		s = dispatch(t, s, ResizeDir{Dir: Left})
		if got := s.Tab().Columns[1].Width; got != 0.45 {
			t.Errorf("width = %v, want 0.45", got)
		}

		if got := s.Tab().Columns[0].Width; got != 0.5 {
			t.Errorf("other column width = %v, want 0.5", got)
		}

		s = dispatch(t, s, ResizeDir{Dir: Up})

		s = dispatch(t, s, ResizeDir{Dir: Down})
		if got := s.Tab().Columns[1].Width; got != 0.45 {
			t.Errorf("vertical resize changed the width: %v", got)
		}

		for range 20 {
			s = dispatch(t, s, ResizeDir{Dir: Left})
		}

		if got := s.Tab().Columns[1].Width; got != 0.1 {
			t.Errorf("width = %v, want the 0.1 floor", got)
		}

		for range 30 {
			s = dispatch(t, s, ResizeDir{Dir: Right})
		}

		if got := s.Tab().Columns[1].Width; got != 1.0 {
			t.Errorf("width = %v, want the 1.0 ceiling", got)
		}
	})
}

func TestSetLayoutModeRebuilds(t *testing.T) {
	for _, c := range []struct{ from, to LayoutMode }{
		{ModeBSP, ModeMaster}, {ModeMaster, ModeScrolling}, {ModeScrolling, ModeBSP},
	} {
		from, to := c.from, c.to

		t.Run(fmt.Sprintf("%s to %s", from, to), func(t *testing.T) {
			s := openN(t, from, 5, defaultLayout())
			s = dispatch(t, s, FocusPane{ID: "p2"})
			s.Tabs[0].Zoomed = true
			s.Tabs[0].Preselect = Right
			panes := copyPanes(s.Panes)
			ids := slices.Clone(s.Tab().Panes)
			next := dispatch(t, s, SetLayoutMode{Mode: to, Area: modeInner})

			tab := next.Tab()
			if tab.Mode != to || tab.Focus != "p2" || tab.Zoomed || tab.Preselect != NoDir {
				t.Errorf("Mode %q Focus %q Zoomed %v Preselect %v", tab.Mode, tab.Focus, tab.Zoomed, tab.Preselect)
			}

			got := slices.Clone(tab.Panes)
			slices.Sort(got)

			want := slices.Clone(ids)
			slices.Sort(want)

			if !slices.Equal(got, want) {
				t.Errorf("Panes %v, want the same set as %v", tab.Panes, ids)
			}

			if fmt.Sprint(next.Panes) != fmt.Sprint(panes) {
				t.Errorf("pane metadata changed: %v", next.Panes)
			}

			switch to {
			case ModeBSP:
				if tab.Layout == nil || len(tab.Columns) != 0 {
					t.Errorf("BSP tab: Layout %v Columns %v", tab.Layout, tab.Columns)
				}

				if !slices.Equal(tab.Panes, leafOrder(tab.Layout)) {
					t.Errorf("Panes %v != leaf order %v", tab.Panes, leafOrder(tab.Layout))
				}
			case ModeMaster:
				if tab.Layout != nil || len(tab.Columns) != 0 || tab.Master != 0.5 || !slices.Equal(tab.Panes, ids) {
					t.Errorf("master tab: Layout %v Columns %v Master %v Panes %v", tab.Layout, tab.Columns, tab.Master, tab.Panes)
				}
			case ModeScrolling:
				if tab.Layout != nil || len(tab.Columns) != 5 || !slices.Equal(tab.Panes, ids) {
					t.Fatalf("scrolling tab: Layout %v Columns %v Panes %v", tab.Layout, tab.Columns, tab.Panes)
				}

				for _, c := range tab.Columns {
					if len(c.Panes) != 1 || c.Width != 0.5 {
						t.Errorf("column %+v, want one pane at 0.5", c)
					}
				}

				if a := Arrange(tab, modeInner, next.Layout); a.Rects["p2"] == (Rect{}) {
					t.Errorf("focused p2 is not drawn after the switch (First %d)", tab.First)
				}
			}

			checkArrangement(t, tab, modeInner, next.Layout)
		})
	}
}

func copyPanes(m map[PaneID]Pane) map[PaneID]Pane {
	out := make(map[PaneID]Pane, len(m))
	maps.Copy(out, m)

	return out
}

func TestSetLayoutModeResetsRatiosAndWidths(t *testing.T) {
	s := openN(t, ModeScrolling, 3, defaultLayout())
	s = dispatch(t, s, ResizeDir{Dir: Right})
	s = dispatch(t, s, SetLayoutMode{Mode: ModeMaster, Area: modeInner})
	s = dispatch(t, s, ResizeDir{Dir: Right})

	s = dispatch(t, s, SetLayoutMode{Mode: ModeScrolling, Area: modeInner})
	for _, c := range s.Tab().Columns {
		if c.Width != 0.5 {
			t.Errorf("column %+v, want the configured width again", c)
		}
	}

	s = dispatch(t, s, SetLayoutMode{Mode: ModeMaster, Area: modeInner})
	if s.Tab().Master != 0.5 {
		t.Errorf("Master = %v, want the configured ratio again", s.Tab().Master)
	}
}

func TestSetLayoutModeSameModeChangesNothing(t *testing.T) {
	s := openN(t, ModeScrolling, 3, defaultLayout())
	s = dispatch(t, s, ResizeDir{Dir: Right})
	s.Tabs[0].Zoomed = true

	got := dispatch(t, s, SetLayoutMode{Mode: ModeScrolling, Area: modeInner})
	if fmt.Sprint(got.Tab()) != fmt.Sprint(s.Tab()) {
		t.Errorf("tab changed: %+v -> %+v", s.Tab(), got.Tab())
	}
}

func TestSetLayoutModeBSPUsesTheScheme(t *testing.T) {
	lc := defaultLayout()
	lc.AutoScheme = "alternate"
	s := openN(t, ModeScrolling, 3, lc)
	s = dispatch(t, s, SetLayoutMode{Mode: ModeBSP, Area: modeInner})
	// Alternate: p2 splits p1 vertically, p3 splits p2 horizontally.
	if got := show(s.Tab().Layout); got != "v(p1,h(p2,p3))" {
		t.Errorf("layout = %s, want v(p1,h(p2,p3))", got)
	}
}

func TestSetLayoutModeEdges(t *testing.T) {
	t.Run("empty tab only gets its mode", func(t *testing.T) {
		s := InitialState(DefaultConfig())
		for _, mode := range allModes {
			s = dispatch(t, s, SetLayoutMode{Mode: mode, Area: modeInner})

			tab := s.Tab()
			if tab.Mode != mode || !tab.Empty() || tab.Layout != nil || tab.Columns != nil || tab.Focus != "" {
				t.Errorf("%s: %+v", mode, tab)
			}
		}
		// The next pane opens in the chosen mode.
		s = dispatch(t, s, SetLayoutMode{Mode: ModeScrolling, Area: modeInner})

		s = dispatch(t, s, NewTab{Kind: KindShell})
		if s.Tab().Mode != ModeScrolling || len(s.Tab().Columns) != 1 || len(s.Tabs) != 1 {
			t.Errorf("filled empty tab: %+v", s.Tab())
		}
	})
	t.Run("empty tab cycles", func(t *testing.T) {
		s := dispatch(t, InitialState(DefaultConfig()), CycleLayoutMode{Area: modeInner})
		if s.Tab().Mode != ModeMaster {
			t.Errorf("Mode = %q, want master-stack", s.Tab().Mode)
		}
	})

	for _, c := range []struct {
		n    int
		from LayoutMode
	}{{1, ModeBSP}, {64, ModeMaster}} {
		n, from := c.n, c.from

		t.Run(fmt.Sprintf("%d panes from %s", n, from), func(t *testing.T) {
			s := openN(t, from, n, defaultLayout())
			s.Tabs[0].Zoomed = true

			ids := copyPanes(s.Panes)
			for _, to := range []LayoutMode{ModeMaster, ModeScrolling, ModeBSP, ModeScrolling} {
				if to == s.Tab().Mode {
					continue
				}

				s.Tabs[0].Zoomed = true
				s = dispatch(t, s, SetLayoutMode{Mode: to, Area: modeInner})

				tab := s.Tab()
				if tab.Mode != to || len(tab.Panes) != n || len(s.Panes) != n || tab.Zoomed || tab.Focus != PaneID(fmt.Sprintf("p%d", n)) {
					t.Fatalf("to %s: %d panes, Focus %q, Zoomed %v", to, len(tab.Panes), tab.Focus, tab.Zoomed)
				}

				checkArrangement(t, tab, modeInner, s.Layout)
			}

			if fmt.Sprint(s.Panes) != fmt.Sprint(ids) {
				t.Error("pane metadata changed")
			}
		})
	}
}

func TestSetLayoutModeUnknown(t *testing.T) {
	s := openN(t, ModeBSP, 2, defaultLayout())

	err := NewStore(s).Dispatch(SetLayoutMode{Mode: "tiled", Area: modeInner})
	if !errors.Is(err, ErrUnknownMode) {
		t.Errorf("error = %v, want ErrUnknownMode", err)
	}

	err = NewStore(s).Dispatch(SetLayoutMode{Area: modeInner})
	if !errors.Is(err, ErrUnknownMode) {
		t.Errorf("empty mode: error = %v, want ErrUnknownMode", err)
	}
}

func TestCycleLayoutMode(t *testing.T) {
	s := openN(t, ModeBSP, 3, defaultLayout())
	for _, want := range []LayoutMode{ModeMaster, ModeScrolling, ModeBSP, ModeMaster} {
		s = dispatch(t, s, CycleLayoutMode{Area: modeInner})
		if s.Tab().Mode != want {
			t.Fatalf("Mode = %q, want %q", s.Tab().Mode, want)
		}

		if len(s.Tab().Panes) != 3 || s.Tab().Focus != "p3" {
			t.Fatalf("%s: Panes %v Focus %s", want, s.Tab().Panes, s.Tab().Focus)
		}
	}
}

func TestNewTabStartsInTheDefaultMode(t *testing.T) {
	for _, mode := range allModes {
		lc := defaultLayout()
		lc.DefaultMode = string(mode)
		s := InitialState(Config{Layout: lc})
		s = dispatch(t, s, NewTab{Kind: KindShell})

		s = dispatch(t, s, NewTab{Kind: KindShell})
		if len(s.Tabs) != 2 || s.Tabs[1].Mode != mode || s.Tabs[0].Mode != mode {
			t.Errorf("%s: tab modes %q %q", mode, s.Tabs[0].Mode, s.Tabs[1].Mode)
		}

		checkArrangement(t, s.Tabs[1], modeInner, s.Layout)
	}
}

func TestZeroLayoutConfigBehavesAsDefaults(t *testing.T) {
	s := State{}

	st := NewStore(s)
	for range 3 {
		if err := st.Dispatch(OpenPane{Kind: KindShell, Split: SplitVertical, Area: modeInner}); err != nil {
			t.Fatal(err)
		}
	}

	if st.State().Tab().Mode != ModeBSP {
		t.Errorf("Mode = %q, want bsp", st.State().Tab().Mode)
	}

	if err := st.Dispatch(SetLayoutMode{Mode: ModeMaster, Area: modeInner}); err != nil {
		t.Fatal(err)
	}

	if st.State().Tab().Master != 0.5 {
		t.Errorf("Master = %v", st.State().Tab().Master)
	}
}

// TestTinyAndOddSizesInEveryMode lays out 1, 7 and 64 panes in a range of body
// sizes, including a resize from 240x64 to 20x6 and back.
func TestTinyAndOddSizesInEveryMode(t *testing.T) {
	sizes := []Rect{
		{0, 0, 1, 1},
		{0, 0, 2, 2},
		{0, 0, 3, 3},
		{0, 0, 7, 3},
		{2, 1, 20, 6},
		{0, 0, 240, 64},
		{3, 2, 61, 5},
		{0, 0, 4, 40},
	}

	for _, mode := range allModes {
		for _, n := range []int{1, 7, 64} {
			t.Run(fmt.Sprintf("%s/%d", mode, n), func(t *testing.T) {
				s := openN(t, mode, n, defaultLayout())

				tab := s.Tab()
				if len(tab.Panes) != n {
					t.Fatalf("%d panes", len(tab.Panes))
				}

				for _, focus := range []PaneID{tab.Panes[0], tab.Focus} {
					tab.Focus = focus

					tab.First = min(tab.First, max(len(tab.Columns)-1, 0))
					for _, sz := range sizes {
						checkArrangement(t, tab, sz, s.Layout)
					}
				}

				big, small := Rect{0, 0, 240, 64}, Rect{0, 0, 20, 6}
				before := Arrange(tab, big, s.Layout)
				checkArrangement(t, tab, small, s.Layout)

				after := Arrange(tab, big, s.Layout)
				if fmt.Sprint(before) != fmt.Sprint(after) {
					t.Error("arranging at 240x64 again after 20x6 gave a different result")
				}
			})
		}
	}
}

func TestTinyAreaStillMovesFocus(t *testing.T) {
	// A body too small to draw the focused pane: the move uses the virtual rect.
	t.Run("bsp", func(t *testing.T) {
		s := stateOf(stackTree(), "p2")
		if _, ok := Arrange(s.Tab(), tinyArea, s.Layout).Rects["p2"]; ok {
			t.Fatal("p2 should have no rect in the tiny area")
		}

		got := dispatch(t, s, FocusDir{Dir: Up, Area: tinyArea})
		if got.Tab().Focus != "p1" {
			t.Errorf("Focus = %q, want p1", got.Tab().Focus)
		}

		got = dispatch(t, s, SwapDir{Dir: Up, Area: tinyArea})
		if show(got.Tab().Layout) != "h(p2,h(p1,p3))" {
			t.Errorf("layout = %s, want p2 swapped above p1", show(got.Tab().Layout))
		}
	})
	t.Run("master-stack", func(t *testing.T) {
		s := openN(t, ModeMaster, 64, defaultLayout())

		area := Rect{0, 0, 10, 4}
		if _, ok := Arrange(s.Tab(), area, s.Layout).Rects[s.Tab().Focus]; ok {
			t.Fatal("the focused pane should have no tile in the tiny area")
		}

		got := dispatch(t, s, FocusDir{Dir: Up, Area: area})
		if got.Tab().Focus == s.Tab().Focus {
			t.Errorf("Focus stayed on %q", got.Tab().Focus)
		}
	})
}

// stackedColumn is a scrolling tab whose first column holds n panes, opened
// with horizontal splits, followed by extra single-pane columns.
func stackedColumn(t *testing.T, n, extra int) State {
	t.Helper()
	s := openN(t, ModeScrolling, 1, defaultLayout())

	st := NewStore(s)
	for range n - 1 {
		if err := st.Dispatch(OpenPane{Kind: KindShell, Split: SplitHorizontal, Area: modeInner}); err != nil {
			t.Fatal(err)
		}
	}

	if err := st.Dispatch(FocusPane{ID: "p1"}); err != nil {
		t.Fatal(err)
	}

	for range extra {
		if err := st.Dispatch(OpenPane{Kind: KindShell, Split: SplitVertical, Area: modeInner}); err != nil {
			t.Fatal(err)
		}
	}

	if err := st.Dispatch(FocusPane{ID: "p1"}); err != nil {
		t.Fatal(err)
	}

	return st.State()
}

func TestStackedScrollingColumnNeverOverlaps(t *testing.T) {
	t.Run("64 panes in one column", func(t *testing.T) {
		s := stackedColumn(t, 64, 0)
		if got := len(columnsOf(s.Tab())); got != 1 {
			t.Fatalf("%d columns, want 1", got)
		}

		for _, sz := range []Rect{{0, 0, 1, 1}, {0, 0, 20, 6}, {0, 0, 240, 64}} {
			checkArrangement(t, s.Tab(), sz, s.Layout)
		}
	})
	t.Run("5 panes at small heights", func(t *testing.T) {
		s := stackedColumn(t, 5, 0)
		for _, h := range []int{1, 4, 9} {
			checkArrangement(t, s.Tab(), Rect{1, 1, 40, h}, s.Layout)
		}
	})
	t.Run("stacked column next to a hidden one", func(t *testing.T) {
		s := stackedColumn(t, 5, 2)
		for _, h := range []int{3, 6, 12} {
			a := checkArrangement(t, s.Tab(), Rect{1, 1, 60, h}, s.Layout)
			if a.HiddenRight != 1 {
				t.Fatalf("h=%d: hidden right %d, want 1", h, a.HiddenRight)
			}

			if h >= 6 {
				if got, ok := a.Sizes["p7"]; !ok || got.W < 1 || got.H < 1 {
					t.Errorf("h=%d: hidden p7 size %+v", h, got)
				}
			}
		}
	})
}

func TestDefaultLayout(t *testing.T) {
	panes := []PaneID{"p1", "p2", "p3"}
	base := Tab{ID: "t1", Name: "n", Focus: "p2", Zoomed: true, Panes: panes, Preselect: Right}

	tests := []struct {
		name  string
		lc    LayoutConfig
		check func(*testing.T, Tab)
	}{
		{"bsp", LayoutConfig{}, func(t *testing.T, got Tab) {
			if got.Mode != ModeBSP || got.Layout == nil || got.Master != 0 || got.Columns != nil {
				t.Fatalf("got %+v", got)
			}

			if !slices.Equal(got.Panes, panes) || !slices.Equal(leafOrder(got.Layout), panes) {
				t.Fatalf("order %v / %v", got.Panes, leafOrder(got.Layout))
			}

			if got.Layout.Split != SplitVertical || got.Layout.Ratio != 0.33 {
				t.Fatalf("root %+v", got.Layout)
			}
		}},
		{"master", LayoutConfig{DefaultMode: "master-stack", MasterRatio: 60}, func(t *testing.T, got Tab) {
			if got.Mode != ModeMaster || got.Master != 0.6 || got.Layout != nil || got.Columns != nil || !slices.Equal(got.Panes, panes) {
				t.Fatalf("got %+v", got)
			}
		}},
		{"scrolling", LayoutConfig{DefaultMode: "scrolling", ColumnWidth: 40}, func(t *testing.T, got Tab) {
			if got.Mode != ModeScrolling || got.First != 0 || got.Layout != nil || len(got.Columns) != 3 {
				t.Fatalf("got %+v", got)
			}

			for i, c := range got.Columns {
				if c.Width != 0.4 || !slices.Equal(c.Panes, panes[i:i+1]) {
					t.Fatalf("column %d %+v", i, c)
				}
			}
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := DefaultLayout(base, tt.lc)
			tt.check(t, got)

			if got.ID != "t1" || got.Name != "n" || got.Focus != "p2" || !got.Zoomed || got.Preselect != NoDir {
				t.Fatalf("identity fields changed: %+v", got)
			}
		})
	}

	empty := DefaultLayout(Tab{ID: "t2"}, LayoutConfig{DefaultMode: "scrolling"})
	if !empty.Empty() || empty.Layout != nil || empty.Columns != nil {
		t.Fatalf("empty tab: %+v", empty)
	}
}
