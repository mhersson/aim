package core

import (
	"fmt"
	"math/rand/v2"
	"slices"
	"testing"
)

// randomTree builds a tree of n leaves p1..pn with ratios on the 0.01 grid.
func randomTree(r *rand.Rand, n int) *Node {
	next := 0

	var build func(n int) *Node

	build = func(n int) *Node {
		if n == 1 {
			next++

			return leaf(PaneID(fmt.Sprintf("p%d", next)))
		}

		k := 1 + r.IntN(n-1)

		split := SplitVertical
		if r.IntN(2) == 0 {
			split = SplitHorizontal
		}

		ratio := float64(10+r.IntN(81)) / 100

		return &Node{Split: split, Ratio: ratio, A: build(k), B: build(n - k)}
	}

	return build(n)
}

func treeLeaves(n *Node) []PaneID {
	var out []PaneID

	leaves(n, func(id PaneID) { out = append(out, id) })

	return out
}

func tabOfTree(n *Node) Tab {
	var t Tab
	setLayout(&t, n)

	return t
}

func TestNodeTreeRoundTripsThroughTiling(t *testing.T) {
	r := rand.New(rand.NewPCG(1, 2))
	for i := range 20 {
		n := 1 + r.IntN(12)
		root := randomTree(r, n)

		tree, err := toTiling(root)
		if err != nil {
			t.Fatalf("tree %d: toTiling: %v", i, err)
		}

		if tree.WindowCount() != n {
			t.Fatalf("tree %d: %d windows, want %d", i, tree.WindowCount(), n)
		}

		if got := fromTiling(tree); show(got) != show(root) || !sameRatios(got, root) {
			t.Fatalf("tree %d: round trip = %s, want %s", i, show(got), show(root))
		}
	}
}

func sameRatios(a, b *Node) bool {
	if a.leaf() || b.leaf() {
		return a.leaf() && b.leaf() && a.Pane == b.Pane
	}

	return a.Split == b.Split && a.Ratio == b.Ratio && sameRatios(a.A, b.A) && sameRatios(a.B, b.B)
}

func TestToTilingRejectsBadPaneIDs(t *testing.T) {
	for _, id := range []PaneID{"x1", "p1x", ""} {
		if _, err := toTiling(vsplit(leaf("p1"), leaf(id))); err == nil {
			t.Errorf("toTiling with pane %q: want an error", id)
		}
	}
}

func TestArrangeEmptyTab(t *testing.T) {
	a := Arrange(Tab{ID: "t1"}, Rect{1, 1, 98, 27}, LayoutConfig{})
	if len(a.Rects) != 0 || len(a.Sizes) != 0 {
		t.Errorf("empty tab arranged %v %v", a.Rects, a.Sizes)
	}
}

func TestArrangeNeverOverlaps(t *testing.T) {
	r := rand.New(rand.NewPCG(3, 4))

	sizes := [][2]int{{1, 1}, {1, 5}, {5, 1}, {2, 2}, {3, 3}, {4, 2}, {7, 3}, {20, 6}, {80, 24}, {240, 64}}

	for _, sz := range sizes {
		for range 2 {
			n := 1 + r.IntN(16)
			tab := tabOfTree(randomTree(r, n))
			inner := Rect{X: r.IntN(3), Y: r.IntN(3), W: sz[0], H: sz[1]}
			a := Arrange(tab, inner, LayoutConfig{})
			cells := map[[2]int]PaneID{}

			for id, rc := range a.Rects {
				if rc.W < 1 || rc.H < 1 || rc.X < inner.X || rc.Y < inner.Y ||
					rc.X+rc.W > inner.X+inner.W || rc.Y+rc.H > inner.Y+inner.H {
					t.Fatalf("%dx%d: %s = %+v outside %+v", sz[0], sz[1], id, rc, inner)
				}

				for y := rc.Y; y < rc.Y+rc.H; y++ {
					for x := rc.X; x < rc.X+rc.W; x++ {
						if o, dup := cells[[2]int{x, y}]; dup {
							t.Fatalf("%dx%d: %s and %s overlap at %d,%d", sz[0], sz[1], id, o, x, y)
						}

						cells[[2]int{x, y}] = id
					}
				}
			}

			for _, id := range tab.Panes {
				_, inRects := a.Rects[id]

				_, inSizes := a.Sizes[id]
				if inRects != inSizes {
					t.Fatalf("%dx%d: %s in Rects %v, in Sizes %v", sz[0], sz[1], id, inRects, inSizes)
				}
			}
		}
	}
}

func TestArrangeKeepsTabPanesAndTreeInStep(t *testing.T) {
	st := NewStore(InitialState(Config{}))
	area := Rect{1, 1, 100, 30}

	acts := []Action{
		OpenPane{Kind: KindShell, Area: area},
		OpenPane{Kind: KindShell, Split: SplitVertical, Area: area},
		OpenPane{Kind: KindShell, Split: SplitHorizontal, Area: area},
		OpenPane{Kind: KindAgent, Split: SplitVertical, Area: area, Harness: "claude-code"},
		SwapDir{Dir: Left, Area: area},
		ClosePane{ID: "p2"},
	}
	for _, a := range acts {
		if err := st.Dispatch(a); err != nil {
			t.Fatalf("%T: %v", a, err)
		}

		tab := st.State().Tab()
		if tab.Mode != ModeBSP {
			t.Fatalf("Mode = %q", tab.Mode)
		}

		if !slices.Equal(tab.Panes, treeLeaves(tab.Layout)) {
			t.Fatalf("after %T: Panes %v, tree leaves %v", a, tab.Panes, treeLeaves(tab.Layout))
		}
	}
}

func TestOpenPaneAutoSplitUsesTheArea(t *testing.T) {
	// No direction: the auto scheme decides from the area, so a wide area
	// splits side by side and a tall one stacks.
	tests := []struct {
		name string
		area Rect
		want string
	}{
		{"wide", Rect{1, 1, 120, 20}, "v(p1,p2)"},
		{"tall", Rect{1, 1, 30, 40}, "h(p1,p2)"},
		{"zero area is wide", Rect{}, "v(p1,p2)"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := stateOf(leaf("p1"), "p1")

			tab := &s.Tabs[0]
			if err := tab.placeBSP("p2", "", tt.area, LayoutConfig{}); err != nil {
				t.Fatal(err)
			}

			if got := show(tab.Layout); got != tt.want {
				t.Errorf("layout = %s, want %s", got, tt.want)
			}
		})
	}
}

func TestPlaceAndRemoveReportBadPaneIDs(t *testing.T) {
	s := stateOf(leaf("p1"), "p1")

	tab := &s.Tabs[0]
	if err := tab.placeBSP("bogus", SplitVertical, Rect{}, LayoutConfig{}); err == nil {
		t.Error("place of a bad id: want an error")
	}

	if _, err := tab.removeBSP("bogus"); err == nil {
		t.Error("remove of a bad id: want an error")
	}

	if show(tab.Layout) != "p1" {
		t.Errorf("layout changed to %s", show(tab.Layout))
	}
}

// tinyArea has room for p1 and p3 of the stack below but not for p2: the pane
// left out has no rect there, though the virtual rect has one.
var tinyArea = Rect{1, 1, 20, 9}

func stackTree() *Node {
	return hsplit(leaf("p1"), hsplit(leaf("p2"), leaf("p3")))
}

func TestFocusDirUsesTheArea(t *testing.T) {
	s := stateOf(stackTree(), "p1")

	a := Arrange(s.Tab(), tinyArea, LayoutConfig{})
	if _, ok := a.Rects["p2"]; ok || len(a.Rects) != 2 {
		t.Fatalf("p2 should be left out of %+v: %v", tinyArea, a.Rects)
	}

	if got := dispatch(t, s, FocusDir{Dir: Down}); got.Tab().Focus != "p2" {
		t.Errorf("virtual rect: Focus = %q, want p2", got.Tab().Focus)
	}

	if got := dispatch(t, s, FocusDir{Dir: Down, Area: tinyArea}); got.Tab().Focus != "p3" {
		t.Errorf("tiny area: Focus = %q, want p3 (p2 has no rect there)", got.Tab().Focus)
	}

	if got := dispatch(t, stateOf(stackTree(), "p2"), FocusDir{Dir: Down, Area: tinyArea}); got.Tab().Focus != "p3" {
		t.Errorf("undrawn focus: Focus = %q, want p3 (moves as on the virtual rect)", got.Tab().Focus)
	}
}

func TestSwapDirUsesTheArea(t *testing.T) {
	s := stateOf(stackTree(), "p1")

	got := dispatch(t, s, SwapDir{Dir: Down, Area: tinyArea})
	if show(got.Tab().Layout) != "h(p3,h(p2,p1))" {
		t.Errorf("tiny area swapped with a pane that has no rect: %s", show(got.Tab().Layout))
	}

	got = dispatch(t, s, SwapDir{Dir: Down})
	if show(got.Tab().Layout) != "h(p2,h(p1,p3))" || !slices.Equal(got.Tab().Panes, []PaneID{"p2", "p1", "p3"}) {
		t.Errorf("virtual rect: %s %v", show(got.Tab().Layout), got.Tab().Panes)
	}
}
