package store

import (
	"encoding/json"
	"math"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/mhersson/aim/internal/core"
)

func noErr(t *testing.T, err error) {
	t.Helper()

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func wantErr(t *testing.T, err error) {
	t.Helper()

	if err == nil {
		t.Fatal("expected an error, got nil")
	}
}

func eq(t *testing.T, got, want any) {
	t.Helper()

	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %+v, want %+v", got, want)
	}
}

func leaf(id core.PaneID) *core.Node { return &core.Node{Pane: id} }

func split(s core.Split, ratio float64, a, b *core.Node) *core.Node {
	return &core.Node{Split: s, Ratio: ratio, A: a, B: b}
}

func TestLayoutRoundTrip(t *testing.T) {
	deep := split(core.SplitVertical, 0.5,
		split(core.SplitHorizontal, 0.25, leaf("p1"), split(core.SplitVertical, 0.75, leaf("p2"), leaf("p3"))),
		leaf("p4"))

	tests := []struct {
		name  string
		in    *core.Node
		panes []core.PaneID
		want  *core.Node
	}{
		{"leaf", leaf("p1"), []core.PaneID{"p1"}, leaf("p1")},
		{"three levels", deep, []core.PaneID{"p4", "p3", "p2", "p1"}, deep},
		{
			"ratio clamped high", split(core.SplitVertical, 5, leaf("p1"), leaf("p2")),
			[]core.PaneID{"p1", "p2"},
			split(core.SplitVertical, 0.9, leaf("p1"), leaf("p2")),
		},
		{
			"ratio clamped low", split(core.SplitVertical, -3, leaf("p1"), leaf("p2")),
			[]core.PaneID{"p1", "p2"},
			split(core.SplitVertical, 0.1, leaf("p1"), leaf("p2")),
		},
		{"none", nil, nil, nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			raw, err := encodeLayout(tt.in)
			noErr(t, err)
			got, err := decodeLayout(raw, tt.panes)
			noErr(t, err)
			eq(t, got, tt.want)
		})
	}
}

func TestEncodeLayoutNull(t *testing.T) {
	raw, err := encodeLayout(nil)
	noErr(t, err)
	eq(t, string(raw), "null")
}

func TestEncodeLayoutRejects(t *testing.T) {
	tests := []struct {
		name string
		in   *core.Node
	}{
		{"unknown split", split("diagonal", 0.5, leaf("p1"), leaf("p2"))},
		{"missing child", &core.Node{Split: core.SplitVertical, Ratio: 0.5, A: leaf("p1")}},
		{"nan ratio", split(core.SplitVertical, math.NaN(), leaf("p1"), leaf("p2"))},
		{"empty node", &core.Node{}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := encodeLayout(tt.in)
			wantErr(t, err)
		})
	}
}

func TestDecodeLayoutRejects(t *testing.T) {
	two := []core.PaneID{"p1", "p2"}

	tests := []struct {
		name  string
		raw   string
		panes []core.PaneID
	}{
		{"unknown split", `{"split":"diagonal","ratio":0.5,"children":[{"pane":"p1"},{"pane":"p2"}]}`, two},
		{"one child", `{"split":"vertical","ratio":0.5,"children":[{"pane":"p1"}]}`, two},
		{"leaf with children", `{"pane":"p1","children":[{"pane":"p2"}]}`, []core.PaneID{"p1"}},
		{"outside pane", `{"split":"vertical","ratio":0.5,"children":[{"pane":"p1"},{"pane":"p9"}]}`, two},
		{"pane twice", `{"split":"vertical","ratio":0.5,"children":[{"pane":"p1"},{"pane":"p1"}]}`, two},
		{"missing leaf", `{"pane":"p1"}`, two},
		{"extra leaf", `{"split":"vertical","ratio":0.5,"children":[{"pane":"p1"},{"pane":"p2"}]}`, []core.PaneID{"p1"}},
		{"missing ratio", `{"split":"vertical","children":[{"pane":"p1"},{"pane":"p2"}]}`, two},
		{"huge ratio", `{"split":"vertical","ratio":1e999,"children":[{"pane":"p1"},{"pane":"p2"}]}`, two},
		{"leaf and split", `{"pane":"p1","split":"vertical","ratio":0.5,"children":[{"pane":"p1"},{"pane":"p2"}]}`, two},
		{"empty pane", `{"pane":""}`, []core.PaneID{""}},
		{"null with panes", `null`, two},
		{"layout without panes", `{"pane":"p1"}`, nil},
		{"not an object", `[1,2]`, two},
		{"children null", `{"split":"vertical","ratio":0.5,"children":[null,{"pane":"p2"}]}`, two},
		{"truncated", `{"split":"vert`, two},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			n, err := decodeLayout(json.RawMessage(tt.raw), tt.panes)
			wantErr(t, err)

			if n != nil {
				t.Fatalf("got %+v with an error", n)
			}
		})
	}
}

func TestDecodeLayoutDeepNesting(t *testing.T) {
	const depth = 1030 // just over maxLayoutDepth

	// The nesting runs down the first child; every pane is distinct, so only
	// the depth limit can reject the tree.
	panes := []core.PaneID{"p0"}

	var tail strings.Builder

	for i := 1; i <= depth; i++ {
		id := "p" + strconv.Itoa(i)
		panes = append(panes, core.PaneID(id))
		tail.WriteString(`,{"pane":"` + id + `"}]}`)
	}

	raw := strings.Repeat(`{"split":"vertical","ratio":0.5,"children":[`, depth) + `{"pane":"p0"}` + tail.String()

	_, err := decodeLayout(json.RawMessage(raw), panes)
	wantErr(t, err)
}

func TestDefaultLayout(t *testing.T) {
	if defaultLayout(nil) != nil {
		t.Fatal("defaultLayout(nil) is not nil")
	}

	for _, n := range []int{1, 2, 10, 11} {
		panes := make([]core.PaneID, n)
		for i := range panes {
			panes[i] = core.PaneID("p" + strconv.Itoa(i+1))
		}

		t.Run(strconv.Itoa(n), func(t *testing.T) {
			root := defaultLayout(panes)
			eq(t, leafOrder(root), panes)

			var check func(*core.Node)

			check = func(nd *core.Node) {
				if nd.Pane != "" {
					return
				}

				eq(t, nd.Split, core.SplitVertical)

				if math.Abs(math.Round(nd.Ratio*100)/100-nd.Ratio) > 1e-12 {
					t.Fatalf("ratio %v is off the 0.01 grid", nd.Ratio)
				}

				if nd.Ratio < 0.1 || nd.Ratio > 0.9 {
					t.Fatalf("ratio %v is outside [0.1, 0.9]", nd.Ratio)
				}

				check(nd.A)
				check(nd.B)
			}
			check(root)

			if n > 10 {
				return // the 0.1 floor makes the columns unequal
			}

			const width = 120

			tab := core.Tab{Mode: core.ModeBSP, Panes: panes, Layout: root}
			rects := core.Arrange(tab, core.Rect{W: width, H: 11}, core.LayoutConfig{}).Rects

			ws := make([]int, 0, n)
			for _, id := range panes {
				ws = append(ws, rects[id].W)
			}

			avail := float64(width-(n-1)) / float64(n)
			for i, w := range ws {
				if math.Abs(avail-float64(w)) > 2 {
					t.Fatalf("pane %d width in %v, want about %.1f", i, ws, avail)
				}
			}

			if !slices.IsSorted(sortedX(rects, panes)) {
				t.Fatal("columns do not run left to right")
			}
		})
	}
}

// defaultLayout is the BSP default tree core builds for panes.
func defaultLayout(panes []core.PaneID) *core.Node {
	return core.DefaultLayout(core.Tab{Panes: panes}, core.LayoutConfig{}).Layout
}

func sortedX(rects map[core.PaneID]core.Rect, order []core.PaneID) []int {
	xs := make([]int, len(order))
	for i, id := range order {
		xs[i] = rects[id].X
	}

	return xs
}
