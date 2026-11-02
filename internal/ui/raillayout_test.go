package ui

import (
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/mhersson/aim/internal/core"
)

func TestRailWidthRule(t *testing.T) {
	t.Parallel()

	tests := []struct {
		w, h, want int
	}{
		{200, 28, 44},
		{84, 28, 44},
		{83, 28, 43},
		{70, 28, 30},
		{64, 28, 24},
		{63, 28, 0},
		{10, 28, 0},
		{100, 3, 44},
		{100, 2, 0},
		{100, 0, 0},
	}

	for _, tt := range tests {
		if got := railWidthFor(core.Rect{Y: 2, W: tt.w, H: tt.h}); got != tt.want {
			t.Errorf("railWidthFor(%dx%d) = %d, want %d", tt.w, tt.h, got, tt.want)
		}
	}
}

func TestRailLayout(t *testing.T) {
	t.Parallel()

	groups := []railGroup{
		{state: core.AgentBlocked, cards: []railCard{{pane: "p1"}}},
		{state: core.AgentIdle, cards: []railCard{{pane: "p2"}, {pane: "p3"}}},
	}

	var sb strings.Builder

	for _, r := range railLayout(groups) {
		switch r.kind {
		case railRowHeader:
			fmt.Fprintf(&sb, "H%d ", r.group)
		case railRowBlank:
			sb.WriteString("_ ")
		case railRowCard:
			fmt.Fprintf(&sb, "c%d.%d ", r.card, r.line)
		}
	}

	want := "H0 _ c0.0 c0.1 c0.2 c0.3 _ H1 _ c1.0 c1.1 c1.2 c1.3 _ c2.0 c2.1 c2.2 c2.3 _ "
	if got := sb.String(); got != want {
		t.Errorf("rows = %q, want %q", got, want)
	}
}

func TestRailClampTop(t *testing.T) {
	t.Parallel()

	groups := []railGroup{
		{state: core.AgentWorking, cards: []railCard{{pane: "p1"}, {pane: "p2"}, {pane: "p3"}, {pane: "p4"}}},
		{state: core.AgentIdle, cards: []railCard{{pane: "p5"}, {pane: "p6"}}},
	}
	rows := railLayout(groups)
	// Rows: header 0; card k has its blank at 1+5k and lines 2+5k to 5+5k; the group's
	// closing blank is row 21 and the second header row 22.
	tests := []struct {
		name         string
		h, top, card int
		want         int
	}{
		{"fits already", 20, 0, 0, 0},
		{"first card shows its header", 20, 5, 0, 0},
		{"scrolls down to show the blank below, on a whole card", 10, 0, 3, 16},
		{"scrolls up to show the blank above", 10, 20, 1, 6},
		{"first card of a group brings its header", 10, 30, 4, 22},
		{"last card: the top snaps down, keeping its blank rows", 10, 0, 5, 28},
		{"too short: the first line wins", 3, 0, 2, 12},
		{"no selection: the clamped top snaps to a blank row", 10, 99, -1, 23},
		{"mid-card top moves up to the card's blank row", 10, 3, -1, 1},
		{"mid-card top in a later card", 10, 14, -1, 11},
		{"a top on a blank row stays", 10, 11, -1, 11},
		{"a top on a header stays", 10, 22, -1, 22},
		{"selected card whole: snap down past the mid-card top", 10, 13, 3, 16},
		{"selected card whole: snap up when it still fits", 12, 9, 2, 6},
		{"negative top", 10, -4, -1, 0},
	}

	for _, tt := range tests {
		if got := railClampTop(rows, tt.h, tt.top, tt.card); got != tt.want {
			t.Errorf("%s: railClampTop(h=%d, top=%d, card=%d) = %d, want %d", tt.name, tt.h, tt.top, tt.card, got, tt.want)
		}
	}
}

// The clamp is idempotent, keeps the selected card whole whenever a card and its
// blank rows fit, and starts the list on a group header or a card's blank row.
func TestRailClampTopProperties(t *testing.T) {
	t.Parallel()

	shapes := map[string][]int{
		"no groups":      nil,
		"two groups":     {4, 2},
		"empty then one": {0, 1},
	}

	// A card, the blank rows above and below it.
	const roomy = railCardLines + 2

	for name, shape := range shapes {
		var groups []railGroup

		cards := 0

		for _, n := range shape {
			g := railGroup{state: core.AgentWorking}
			for range n {
				g.cards = append(g.cards, railCard{pane: core.PaneID(fmt.Sprintf("p%d", cards))})
				cards++
			}

			groups = append(groups, g)
		}

		rows := railLayout(groups)

		// Past the end, the start, the middle of a card, and the heights either side of a card's room.
		mid := max(0, slices.IndexFunc(rows, func(r railRow) bool { return r.kind == railRowCard && r.line == 1 }))

		for _, h := range []int{-1, 0, 1, roomy - 1, roomy, len(rows)} {
			for _, top := range []int{-3, 0, mid, len(rows) + 3} {
				for card := -1; card < cards; card++ {
					got := railClampTop(rows, h, top, card)
					id := fmt.Sprintf("%s: railClampTop(h=%d, top=%d, card=%d) = %d", name, h, top, card, got)

					if again := railClampTop(rows, h, got, card); again != got {
						t.Errorf("%s: clamping again gives %d", id, again)
					}

					if len(rows) == 0 || h <= 0 {
						continue
					}

					first := -1
					if card >= 0 {
						first = slices.IndexFunc(rows, func(r railRow) bool { return r.kind == railRowCard && r.card == card && r.line == 0 })
					}

					if card >= 0 && h >= roomy && (first-1 < got || first+railCardLines > got+h-1) {
						t.Errorf("%s: the selected card (rows %d to %d) or its blank rows are not visible in %d rows", id, first, first+railCardLines-1, h)
					}

					if (card < 0 || h >= roomy) && !railBoundary(rows, got) {
						t.Errorf("%s: row %d (%v) is neither a header nor a card's blank row", id, got, rows[got].kind)
					}
				}
			}
		}
	}
}

func TestRailAge(t *testing.T) {
	t.Parallel()

	now := t0

	tests := []struct {
		ago  time.Duration
		want string
	}{
		{0, "0s"},
		{8 * time.Second, "8s"},
		{59*time.Second + 900*time.Millisecond, "59s"},
		{60 * time.Second, "1m"},
		{4 * time.Minute, "4m"},
		{59 * time.Minute, "59m"},
		{60 * time.Minute, "1h"},
		{23 * time.Hour, "23h"},
		{24 * time.Hour, "1d"},
		{80 * time.Hour, "3d"},
		{-time.Second, "0s"},
	}

	for _, tt := range tests {
		if got := railAge(now.Add(-tt.ago), now); got != tt.want {
			t.Errorf("railAge(%v ago) = %q, want %q", tt.ago, got, tt.want)
		}
	}

	if got := railAge(time.Time{}, now); got != "" {
		t.Errorf("railAge(zero) = %q, want empty", got)
	}
}

func TestRailCardAt(t *testing.T) {
	t.Parallel()
	a := drawApp(t, "catppuccin-mocha", true, 100, 30, railFixtureAgents())
	a.rail.sel = "p5"

	v, ok := a.railViewOf(a.store.State())
	if !ok {
		t.Fatal("rail hidden")
	}

	l := v.geom.list

	for i := range l.H {
		row := v.rows[v.top+i]
		got, found := v.cardAt(l.X+3, l.Y+i)

		switch row.kind {
		case railRowCard:
			if !found || got != row.card {
				t.Errorf("row %d (card %d line %d): cardAt = %d, %v", i, row.card, row.line, got, found)
			}
		case railRowHeader:
			if found {
				t.Errorf("row %d (header): cardAt = %d, want none", i, got)
			}
		case railRowBlank:
			if found {
				t.Errorf("row %d (blank): cardAt = %d, want none", i, got)
			}
		}
	}

	if _, found := v.cardAt(l.X-1, l.Y+3); found {
		t.Error("cardAt found a card on the rail's border")
	}

	if _, found := v.cardAt(l.X+3, l.Y+l.H); found {
		t.Error("cardAt found a card under the list")
	}
}

func TestRailHidden(t *testing.T) {
	t.Parallel()
	// One group of four cards: the header is row 0, card k fills rows 5k+2 to 5k+5.
	rows := railLayout([]railGroup{{cards: make([]railCard, 4)}})

	tests := []struct {
		name         string
		h, top       int
		above, below int
	}{
		{"top of the list", 10, 0, 0, 3},
		{"middle", 10, 7, 1, 1},
		{"bottom of the list", 10, 12, 2, 0},
		{"card cut at the top edge", 10, 3, 1, 2},
		{"card cut at the bottom edge", 8, 2, 0, 3},
		{"everything fits", 30, 0, 0, 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			above, below := railHidden(rows, tt.h, tt.top)
			if above != tt.above || below != tt.below {
				t.Errorf("railHidden(h=%d, top=%d) = %d, %d; want %d, %d", tt.h, tt.top, above, below, tt.above, tt.below)
			}
		})
	}
}
