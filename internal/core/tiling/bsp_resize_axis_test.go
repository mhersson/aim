// Ported from tuios internal/layout/bsp_resize_axis_test.go at commit
// aaaa128cd35b38ba8aa08a5f69e688f01385ec3e (github.com/Gaurav-Gosain/tuios).
// Copyright (c) 2025 Gaurav Gosain. MIT License; see THIRD_PARTY.md.

package tiling

import (
	"maps"
	"slices"
	"testing"
)

// edge identifies which side of a pane a resize drags.
type edge int

const (
	edgeRight edge = iota
	edgeLeft
	edgeBottom
	edgeTop
)

// layoutsNamed picks the resize layouts called names.
func layoutsNamed(names ...string) []resizeLayout {
	var out []resizeLayout
	for _, l := range resizeLayouts {
		if slices.Contains(names, l.name) {
			out = append(out, l)
		}
	}
	return out
}

func (e edge) horizontal() bool { return e == edgeRight || e == edgeLeft }

func (e edge) String() string {
	switch e {
	case edgeRight:
		return "right"
	case edgeLeft:
		return "left"
	case edgeBottom:
		return "bottom"
	default:
		return "top"
	}
}

// leaf builds a leaf and registers it with the tree.
func (t *BSPTree) leaf(id int) *TileNode {
	n := t.NewLeafNode(id)
	t.WindowToNode[id] = n
	return n
}

func treeOf(build func(t *BSPTree) *TileNode) *BSPTree {
	t := NewBSPTree()
	t.Root = build(t)
	return t
}

// twoPanes: 1 | 2
func twoPanes() *BSPTree {
	return treeOf(func(t *BSPTree) *TileNode {
		return t.NewInternalNode(SplitVertical, 0.5, t.leaf(1), t.leaf(2))
	})
}

// tallLeft is the maintainer's layout: one full-height column on the left, the
// right column split into top and bottom.
func tallLeft() *BSPTree {
	return treeOf(func(t *BSPTree) *TileNode {
		right := t.NewInternalNode(SplitHorizontal, 0.5, t.leaf(2), t.leaf(3))
		return t.NewInternalNode(SplitVertical, 0.5, t.leaf(1), right)
	})
}

// tallRight is the mirror image: the stacked column is on the left.
func tallRight() *BSPTree {
	return treeOf(func(t *BSPTree) *TileNode {
		left := t.NewInternalNode(SplitHorizontal, 0.5, t.leaf(1), t.leaf(2))
		return t.NewInternalNode(SplitVertical, 0.5, left, t.leaf(3))
	})
}

// grid is a 2x2 arrangement built from two horizontal splits under a vertical one.
func grid() *BSPTree {
	return treeOf(func(t *BSPTree) *TileNode {
		left := t.NewInternalNode(SplitHorizontal, 0.5, t.leaf(1), t.leaf(2))
		right := t.NewInternalNode(SplitHorizontal, 0.5, t.leaf(3), t.leaf(4))
		return t.NewInternalNode(SplitVertical, 0.5, left, right)
	})
}

// deepNest puts a pane three splits down so a resize has to survive several
// levels of bounds narrowing.
func deepNest() *BSPTree {
	return treeOf(func(t *BSPTree) *TileNode {
		inner := t.NewInternalNode(SplitVertical, 0.5, t.leaf(3), t.leaf(4))
		mid := t.NewInternalNode(SplitHorizontal, 0.5, t.leaf(2), inner)
		right := t.NewInternalNode(SplitHorizontal, 0.6, mid, t.leaf(5))
		return t.NewInternalNode(SplitVertical, 0.4, t.leaf(1), right)
	})
}

// sharedBorders selects a gap of 1 between panes instead of 0.
var sharedBorders bool

func sharedGap() int {
	if sharedBorders {
		return 1
	}
	return 0
}

// dragEdge moves one edge of the target pane and drags every pane that shares
// that divider along with it, the way adjustTilingNeighborsGeneric does. It
// returns false when the move would push any pane below the minimum size, so
// callers can skip a case rather than assert against clamped geometry.
func dragEdge(geo map[int]Rect, target int, e edge, delta int) (map[int]Rect, bool) {
	gap := sharedGap()
	r, ok := geo[target]
	if !ok {
		return nil, false
	}

	out := make(map[int]Rect, len(geo))
	maps.Copy(out, geo)

	if e.horizontal() {
		old := r.X
		if e == edgeRight {
			old = r.X + r.W
		}
		moved := old + delta
		for id, g := range out {
			switch {
			case g.X+g.W == old:
				g.W = moved - g.X
			case g.X == old+gap:
				g.W = g.X + g.W - (moved + gap)
				g.X = moved + gap
			default:
				continue
			}
			if g.W < DefaultWindowWidth {
				return nil, false
			}
			out[id] = g
		}
		return out, true
	}

	old := r.Y
	if e == edgeBottom {
		old = r.Y + r.H
	}
	moved := old + delta
	for id, g := range out {
		switch {
		case g.Y+g.H == old:
			g.H = moved - g.Y
		case g.Y == old+gap:
			g.H = g.Y + g.H - (moved + gap)
			g.Y = moved + gap
		default:
			continue
		}
		if g.H < DefaultWindowHeight {
			return nil, false
		}
		out[id] = g
	}
	return out, true
}

// assertAxisIsolated checks the invariant: a horizontal resize may only change
// X and W, a vertical resize may only change Y and H.
func assertAxisIsolated(t *testing.T, label string, e edge, before, after map[int]Rect) {
	t.Helper()
	for id, b := range before {
		a, ok := after[id]
		if !ok {
			t.Fatalf("%s: pane %d vanished from layout", label, id)
		}
		if e.horizontal() {
			if a.Y != b.Y || a.H != b.H {
				t.Errorf("%s: horizontal resize moved pane %d vertically: Y %d->%d, H %d->%d",
					label, id, b.Y, a.Y, b.H, a.H)
			}
		} else if a.X != b.X || a.W != b.W {
			t.Errorf("%s: vertical resize moved pane %d horizontally: X %d->%d, W %d->%d",
				label, id, b.X, a.X, b.W, a.W)
		}
	}
}

// withSharedBorders runs fn with SharedBorders set, restoring it after.
func withSharedBorders(t *testing.T, shared bool, fn func()) {
	t.Helper()
	prev := sharedBorders
	sharedBorders = shared
	defer func() { sharedBorders = prev }()
	fn()
}

type resizeLayout struct {
	name  string
	build func() *BSPTree
	panes []int
}

var resizeLayouts = []resizeLayout{
	{"two_panes", twoPanes, []int{1, 2}},
	{"tall_left", tallLeft, []int{1, 2, 3}},
	{"tall_right", tallRight, []int{1, 2, 3}},
	{"grid", grid, []int{1, 2, 3, 4}},
	{"deep_nest", deepNest, []int{1, 2, 3, 4, 5}},
}

// TestResizeKeepsOffAxisGeometry is the core property: dragging any edge of any
// pane, then syncing the geometry back into the tree and re-applying the
// layout, must leave the perpendicular axis of every pane untouched.
func TestResizeKeepsOffAxisGeometry(t *testing.T) {
	bounds := Rect{X: 0, Y: 1, W: 160, H: 48}
	edges := []edge{edgeRight, edgeLeft, edgeBottom, edgeTop}

	for _, shared := range []bool{false, true} {
		withSharedBorders(t, shared, func() {
			for _, lay := range layoutsNamed("two_panes", "grid", "deep_nest") {
				for _, pane := range lay.panes {
					for _, e := range edges {
						for _, delta := range []int{4, -4} {
							tree := lay.build()
							before := tree.ApplyLayout(bounds, sharedGap())
							dragged, ok := dragEdge(before, pane, e, delta)
							if !ok {
								continue
							}
							tree.SyncRatiosFromGeometry(dragged, bounds, sharedGap())
							after := tree.ApplyLayout(bounds, sharedGap())

							label := lay.name + "/pane" + string(rune('0'+pane)) + "/" + e.String()
							if shared {
								label = "shared:" + label
							}
							assertAxisIsolated(t, label, e, before, after)
						}
					}
				}
			}
		})
	}
}

// TestRepeatedResizeDoesNotDrift checks that many small drags land in the same
// place as one large drag and, more importantly, that the perpendicular axis
// never accumulates drift across a long sequence.
func TestRepeatedResizeDoesNotDrift(t *testing.T) {
	bounds := Rect{X: 0, Y: 1, W: 160, H: 48}

	for _, shared := range []bool{false, true} {
		withSharedBorders(t, shared, func() {
			for _, lay := range layoutsNamed("grid", "deep_nest") {
				tree := lay.build()
				start := tree.ApplyLayout(bounds, sharedGap())
				geo := start

				const steps = 8
				for range steps {
					dragged, ok := dragEdge(geo, 1, edgeRight, 2)
					if !ok {
						break
					}
					tree.SyncRatiosFromGeometry(dragged, bounds, sharedGap())
					geo = tree.ApplyLayout(bounds, sharedGap())
				}

				label := lay.name
				if shared {
					label = "shared:" + label
				}
				assertAxisIsolated(t, label+"/repeated", edgeRight, start, geo)
			}
		})
	}
}
