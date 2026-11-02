package core

import (
	"reflect"
	"slices"
	"testing"
)

var neighbourArea = Rect{X: 0, Y: 2, W: 200, H: 48}

func skipping(ids ...PaneID) func(PaneID, Rect) bool {
	return func(id PaneID, _ Rect) bool { return slices.Contains(ids, id) }
}

func TestNeighbourSkipping(t *testing.T) {
	// p1 takes the left half, p2 and p3 split the right half side by side.
	row := func(focus PaneID) Tab {
		return stateOf(vsplit(leaf("p1"), vsplit(leaf("p2"), leaf("p3"))), focus).Tab()
	}

	tests := []struct {
		name  string
		focus PaneID
		dir   Dir
		skip  func(PaneID, Rect) bool
		want  PaneID // "" for none
	}{
		{"nothing skipped picks the nearest", "p1", Right, skipping(), "p2"},
		{"skipping the nearest takes the next", "p1", Right, skipping("p2"), "p3"},
		{"skipping every candidate finds none", "p1", Right, skipping("p2", "p3"), ""},
		{"left, the nearest", "p3", Left, skipping(), "p2"},
		{"left, skipping the nearest", "p3", Left, skipping("p2"), "p1"},
		{"nothing lies that way", "p2", Down, skipping(), ""},
		{"no predicate skips nothing", "p1", Right, nil, "p2"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := NeighbourSkipping(row(tt.focus), tt.dir, neighbourArea, LayoutConfig{}, tt.skip)
			if got != tt.want || ok != (tt.want != "") {
				t.Errorf("NeighbourSkipping() = %q, %v, want %q", got, ok, tt.want)
			}
		})
	}

	t.Run("the predicate sees the rect the pane is drawn at", func(t *testing.T) {
		tab := row("p1")
		want := Arrange(tab, neighbourArea, LayoutConfig{}).Rects
		seen := map[PaneID]Rect{}

		NeighbourSkipping(tab, Right, neighbourArea, LayoutConfig{}, func(id PaneID, r Rect) bool {
			seen[id] = r

			return false
		})

		if len(seen) == 0 {
			t.Fatal("the predicate was never asked")
		}

		for id, r := range seen {
			if r != want[id] {
				t.Errorf("%s: predicate got %+v, want %+v", id, r, want[id])
			}
		}
	})
}

// With nothing to skip the search is the one a focus move uses.
func TestNeighbourSkippingMatchesFocusDir(t *testing.T) {
	for _, mode := range []LayoutMode{ModeBSP, ModeMaster, ModeScrolling} {
		s := buildTabs(t, []LayoutMode{mode}, []int{4})

		id := PaneID("p2")

		for _, d := range []Dir{Left, Down, Up, Right} {
			s.Tabs[0].Focus = id

			after := dispatch(t, s, FocusDir{Dir: d, Area: neighbourArea})
			want := after.Tab().Focus

			if want == id {
				want = ""
			}

			got, ok := NeighbourSkipping(s.Tab(), d, neighbourArea, s.Layout, skipping())
			if got != want || ok != (want != "") {
				t.Errorf("%s from %s going %v: NeighbourSkipping() = %q, %v, want %q", mode, id, d, got, ok, want)
			}
		}
	}
}

// A skipped column in the scrolling layout is stepped over, judged where the
// pane would be drawn once the focus has moved to it.
func TestNeighbourSkippingScrolling(t *testing.T) {
	s := buildTabs(t, []LayoutMode{ModeScrolling}, []int{3})
	s.Tabs[0].Focus = "p1"
	s.Tabs[0].KeepVisible()
	tab := s.Tab()

	// Two columns fit; the right half is out of reach.
	rightHalf := func(_ PaneID, r Rect) bool { return r.X >= neighbourArea.W/2 }

	tests := []struct {
		name string
		skip func(PaneID, Rect) bool
		want PaneID
	}{
		{"nothing skipped", skipping(), "p2"},
		{"the next column is out of reach, and so is the one after it", rightHalf, ""},
		{"one column skipped by name", skipping("p2"), "p3"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := NeighbourSkipping(tab, Right, neighbourArea, s.Layout, tt.skip)
			if got != tt.want || ok != (tt.want != "") {
				t.Errorf("NeighbourSkipping() = %q, %v, want %q", got, ok, tt.want)
			}
		})
	}
}

// The search reads the tab; nothing of it changes.
func TestNeighbourSkippingChangesNothing(t *testing.T) {
	s := buildTabs(t, []LayoutMode{ModeScrolling}, []int{4})
	s.Tabs[0].Focus = "p1"
	before := s.clone()

	for _, d := range []Dir{Left, Down, Up, Right} {
		NeighbourSkipping(s.Tab(), d, neighbourArea, s.Layout, skipping("p2", "p3"))
	}

	if !reflect.DeepEqual(before, s) {
		t.Error("the search changed the state")
	}
}

// Beside places the pane as if its anchor had been focused first, and the new
// pane takes the focus either way.
func TestOpenPaneBeside(t *testing.T) {
	for _, mode := range []LayoutMode{ModeBSP, ModeMaster, ModeScrolling} {
		t.Run(string(mode), func(t *testing.T) {
			s := buildTabs(t, []LayoutMode{mode}, []int{3})
			s.Tabs[0].Focus = "p3"
			open := OpenPane{Kind: KindShell, Split: SplitVertical, Area: modeInner}

			open.Beside = "p1"
			got := dispatch(t, s, open)

			open.Beside = ""
			want := dispatch(t, dispatch(t, s, FocusPane{ID: "p1"}), open)

			if !reflect.DeepEqual(got.Tab(), want.Tab()) {
				t.Errorf("tab = %+v, want %+v", got.Tab(), want.Tab())
			}

			if got.Tab().Focus != "p4" {
				t.Errorf("Focus = %q, want the new pane p4", got.Tab().Focus)
			}
		})
	}

	t.Run("an anchor outside the tab is ignored", func(t *testing.T) {
		s := buildTabs(t, []LayoutMode{ModeBSP, ModeBSP}, []int{2, 2})
		open := OpenPane{Kind: KindShell, Split: SplitVertical, Area: modeInner}

		open.Beside = "p3"
		got := dispatch(t, s, open)

		open.Beside = ""
		want := dispatch(t, s, open)

		if !reflect.DeepEqual(got.Tab(), want.Tab()) {
			t.Errorf("tab = %+v, want %+v", got.Tab(), want.Tab())
		}
	})
}
