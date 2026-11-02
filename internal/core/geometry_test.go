package core

import (
	"fmt"
	"testing"
)

// sixPanes is a deep tree that cannot fit in a tiny area.
func sixPanes() *Node {
	return vsplit(leaf("p1"), vsplit(leaf("p2"), vsplit(leaf("p3"),
		hsplit(leaf("p4"), vsplit(leaf("p5"), leaf("p6"))))))
}

func TestArrangeRects(t *testing.T) {
	tests := []struct {
		name string
		tree *Node
		body Rect
		want map[PaneID]Rect
	}{
		{
			"side by side",
			vsplit(leaf("p1"), leaf("p2")),
			Rect{0, 0, 81, 11},
			map[PaneID]Rect{"p1": {0, 0, 40, 11}, "p2": {40, 0, 41, 11}},
		},
		{
			"stacked",
			hsplit(leaf("p1"), leaf("p2")),
			Rect{0, 0, 80, 11},
			map[PaneID]Rect{"p1": {0, 0, 80, 5}, "p2": {0, 5, 80, 6}},
		},
		{
			"offset area",
			vsplit(leaf("p1"), hsplit(leaf("p2"), leaf("p3"))),
			Rect{2, 3, 21, 11},
			map[PaneID]Rect{"p1": {2, 3, 10, 11}, "p2": {12, 3, 11, 5}, "p3": {12, 8, 11, 6}},
		},
		{
			"single pane",
			leaf("p1"),
			Rect{0, 0, 5, 5},
			map[PaneID]Rect{"p1": {0, 0, 5, 5}},
		},
		{
			"a 3x3 pane is a frame around one content cell",
			leaf("p1"),
			Rect{4, 2, 3, 3},
			map[PaneID]Rect{"p1": {4, 2, 3, 3}},
		},
		{
			"a 2x2 tile is left out",
			vsplit(leaf("p1"), leaf("p2")),
			Rect{0, 0, 7, 3},
			map[PaneID]Rect{"p1": {0, 0, 3, 3}, "p2": {3, 0, 4, 3}},
		},
		{
			"a split whose first tile is 2 wide leaves it out",
			vsplit(leaf("p1"), leaf("p2")),
			Rect{0, 0, 5, 4},
			map[PaneID]Rect{"p2": {2, 0, 3, 4}},
		},
		{"a body under 3x3 stays blank", leaf("p1"), Rect{0, 0, 2, 2}, map[PaneID]Rect{}},
		{"too short", vsplit(leaf("p1"), leaf("p2")), Rect{0, 0, 10, 2}, map[PaneID]Rect{}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := Arrange(tabOfTree(tt.tree), tt.body, LayoutConfig{})
			if fmt.Sprint(got.Rects) != fmt.Sprint(tt.want) || fmt.Sprint(got.Sizes) != fmt.Sprint(tt.want) {
				t.Errorf("Rects = %v, Sizes = %v, want %v", got.Rects, got.Sizes, tt.want)
			}
		})
	}
}

func TestArrangeTinyAreas(t *testing.T) {
	if got := len(Arrange(tabOfTree(sixPanes()), Rect{0, 0, 20, 5}, LayoutConfig{}).Rects); got >= 6 {
		t.Errorf("20x5 kept %d panes, want some omitted", got)
	}
}
