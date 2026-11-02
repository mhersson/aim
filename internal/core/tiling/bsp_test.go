// Ported from tuios internal/layout/bsp_test.go at commit
// aaaa128cd35b38ba8aa08a5f69e688f01385ec3e (github.com/Gaurav-Gosain/tuios).
// Copyright (c) 2025 Gaurav Gosain. MIT License; see THIRD_PARTY.md.

package tiling

import (
	"testing"
)

// TestApplyLayoutNeverOverlaps guards the invariant a tiling layout must hold:
// no two windows share a cell, whatever the window count or the terminal size.
//
// It regressed when a leaf was grown to a fixed minimum size: a layout
// holding more panes than fit at that minimum had its smallest tiles inflated
// past the rectangle the tree gave them and into their neighbours, so they
// rendered overlapping instead of simply small.
func TestApplyLayoutNeverOverlaps(t *testing.T) {
	// Small enough that a spiral of several panes drives the smallest tiles well
	// below the old fixed minimum, which is exactly where the overlap lived.
	bounds := Rect{X: 0, Y: 0, W: 120, H: 40}

	for _, n := range []int{4, 12} {
		tree := NewBSPTree()
		tree.AutoScheme = SchemeSpiral
		last := 0
		for i := range n {
			tree.InsertWindow(i+1, last, SplitNone, 0.5, bounds, 0)
			last = i + 1
		}

		rects := tree.ApplyLayout(bounds, 0)
		if len(rects) != n {
			t.Fatalf("n=%d: laid out %d windows, want %d", n, len(rects), n)
		}

		type box struct {
			id int
			r  Rect
		}
		boxes := make([]box, 0, n)
		for id, r := range rects {
			if r.W < 1 || r.H < 1 {
				t.Errorf("n=%d: window %d has a non-positive size %dx%d", n, id, r.W, r.H)
			}
			if r.X < bounds.X || r.Y < bounds.Y || r.X+r.W > bounds.X+bounds.W || r.Y+r.H > bounds.Y+bounds.H {
				t.Errorf("n=%d: window %d at (%d,%d) %dx%d escapes bounds %dx%d",
					n, id, r.X, r.Y, r.W, r.H, bounds.W, bounds.H)
			}
			boxes = append(boxes, box{id, r})
		}

		for a := range boxes {
			for b := a + 1; b < len(boxes); b++ {
				ra, rb := boxes[a].r, boxes[b].r
				if ra.X < rb.X+rb.W && rb.X < ra.X+ra.W && ra.Y < rb.Y+rb.H && rb.Y < ra.Y+ra.H {
					t.Errorf("n=%d: windows %d (%d,%d %dx%d) and %d (%d,%d %dx%d) overlap",
						n, boxes[a].id, ra.X, ra.Y, ra.W, ra.H,
						boxes[b].id, rb.X, rb.Y, rb.W, rb.H)
				}
			}
		}
	}
}

// TestParseAutoSchemeRoundTrips checks every AutoScheme String()s to the text
// ParseAutoScheme reads back, which is the text a caller stores to name a
// scheme.
func TestParseAutoSchemeRoundTrips(t *testing.T) {
	cases := []struct {
		text string
		want AutoScheme
	}{
		{"spiral", SchemeSpiral},
		{"longest_side", SchemeLongestSide},
		{"alternate", SchemeAlternate},
		{"smart_split", SchemeSmartSplit},
	}
	for _, tc := range cases {
		if got := ParseAutoScheme(tc.text); got != tc.want {
			t.Errorf("ParseAutoScheme(%q) = %v, want %v", tc.text, got, tc.want)
		}
		if got := tc.want.String(); got != tc.text {
			t.Errorf("%v.String() = %q, want %q", tc.want, got, tc.text)
		}
	}
	// An unrecognised value falls back rather than panicking or zeroing
	// silently to something a caller would not expect.
	if got := ParseAutoScheme("nonsense"); got != SchemeLongestSide {
		t.Errorf("ParseAutoScheme(\"nonsense\") = %v, want the longest_side fallback", got)
	}
}
