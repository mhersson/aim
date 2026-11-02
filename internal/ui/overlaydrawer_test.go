package ui

import (
	"fmt"
	"slices"
	"strings"
	"testing"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/mhersson/aim/internal/core"
)

// gridOf parses s into cells as the compositor does.
func gridOf(s string, w, h int) *lipgloss.Canvas {
	c := lipgloss.NewCanvas(w, h)
	c.Compose(lipgloss.NewLayer(s))

	return c
}

// requireSameCells fails unless got and want parse to the same cells, styles
// and links on a w by h screen.
func requireSameCells(t *testing.T, got, want string, w, h int) {
	t.Helper()

	g, o := gridOf(got, w, h), gridOf(want, w, h)

	for y := range h {
		for x := range w {
			if gc, wc := g.CellAt(x, y), o.CellAt(x, y); !gc.Equal(wc) {
				t.Fatalf("cell (%d,%d) is %+v, the compositor leaves %+v", x, y, *gc, *wc)
			}
		}
	}
}

// spliceWidth, spliceHeight and spliceAt are the screen the boundary cases draw
// on: a drawer of 16 columns at the right edge, from row 1 down.
const (
	spliceWidth, spliceHeight = 40, 8
	spliceAt                  = 24
)

const (
	sgrOpen = "\x1b[1;3;4;7;38;2;255;0;0;48;5;21m"
	linkOn  = "\x1b]8;;https://example.com/a\x1b\\"
	linkOff = "\x1b]8;;\x1b\\"
	reset   = "\x1b[m"

	// The string sequences uv rides on the next cell.
	apc = "\x1b_Gi=1,a=T;AAAA\x1b\\"
	dcs = "\x1bPq#0;2;0;0;0~-\x1b\\"
	sos = "\x1bXnote\x1b\\"
	pm  = "\x1b^note\x1b\\"
)

func repeatA(n int) string { return strings.Repeat("a", n) }

// styledDrawer is a drawer line as the drawer draws them: every cell styled.
func styledDrawer(i int) string {
	return "\x1b[48;2;30;30;46m\x1b[38;2;205;214;244m│" + fmt.Sprintf(" line %-8d", i) + "│\x1b[m"
}

// bareDrawer is a line with no style at all, so any state the base leaves open
// shows in it.
func bareDrawer(i int) string { return fmt.Sprintf("|%-14d|", i) }

// taggedDrawer is a line that starts with a hidden-character tag.
func taggedDrawer(i int) string { return "\x1b[1;31m⟨U+200B⟩\x1b[m" + strings.Repeat("z", 8) }

func baseOf(row func(y int) string) string {
	rows := make([]string, spliceHeight)
	for y := range rows {
		rows[y] = row(y)
	}

	return strings.Join(rows, "\n")
}

// The splice must leave the cells the compositor leaves, wherever the base has
// something open or wide at the drawer's left edge; a base it cannot splice
// without changing a cell goes through the compositor.
func TestOverlayDrawerMatchesTheCompositorAtTheEdge(t *testing.T) {
	t.Parallel()

	short := func(plain string) func(y int) string {
		return func(y int) string {
			if y%2 == 0 {
				return repeatA(spliceWidth)
			}

			return plain
		}
	}

	tests := []struct {
		name    string
		row     func(y int) string
		spliced bool // the splice applies; else the compositor draws
	}{
		{"plain", func(y int) string { return repeatA(spliceWidth) }, true},
		{"wide character straddles the edge", func(y int) string {
			return repeatA(spliceAt-1) + "中" + repeatA(spliceWidth-spliceAt-1)
		}, true},
		{"wide character straddles the edge in a style", func(y int) string {
			return sgrOpen + repeatA(spliceAt-1) + "中" + repeatA(spliceWidth-spliceAt-1) + reset
		}, true},
		{"a style change follows the wide character that straddles", func(y int) string {
			return "\x1b[44m" + repeatA(spliceAt-1) + "中\x1b[1;31m" + repeatA(spliceWidth-spliceAt-1) + reset
		}, true},
		{"emoji straddles the edge", func(y int) string {
			return "\x1b[32m" + repeatA(spliceAt-1) + "😀" + repeatA(spliceWidth-spliceAt-1) + reset
		}, true},
		{"wide character starts at the edge", func(y int) string {
			return repeatA(spliceAt) + "中" + repeatA(spliceWidth-spliceAt-2)
		}, true},
		{"wide character ends before the edge", func(y int) string {
			return repeatA(spliceAt-2) + "中" + repeatA(spliceWidth-spliceAt)
		}, true},
		{"wide characters all along the row", func(y int) string { return strings.Repeat("中", spliceWidth/2) }, true},
		{"style open across the edge", func(y int) string { return sgrOpen + repeatA(spliceWidth) + reset }, true},
		{"style changes after the edge", func(y int) string {
			return repeatA(spliceAt) + "\x1b[1;35;43m" + repeatA(spliceWidth-spliceAt) + reset
		}, true},
		{"link open across the edge", func(y int) string { return linkOn + repeatA(spliceWidth) + linkOff }, true},
		{"link open at the wide character that straddles", func(y int) string {
			return linkOn + repeatA(spliceAt-1) + "中" + repeatA(spliceWidth-spliceAt-1) + linkOff
		}, true},
		{"style and link open across the edge", func(y int) string {
			return sgrOpen + linkOn + repeatA(spliceAt-1) + "中" + repeatA(spliceWidth-spliceAt-1) + linkOff + reset
		}, true},
		{"a row shorter than the edge", short(repeatA(10)), true},
		{"a row that ends at the edge", short("\x1b[44m" + repeatA(spliceAt) + reset), true},
		{"an empty row", func(y int) string {
			if y == 3 {
				return ""
			}

			return repeatA(spliceWidth)
		}, true},
		{"styled blanks to the right edge", func(y int) string {
			return repeatA(8) + "\x1b[48;2;24;24;37m" + strings.Repeat(" ", spliceWidth-8) + reset
		}, true},
		{"a hidden-character tag across the edge", func(y int) string {
			return repeatA(spliceAt-4) + "\x1b[1;31m⟨U+200B⟩\x1b[m" + repeatA(spliceWidth-spliceAt-4)
		}, true},
		{"a combining mark before the edge", func(y int) string {
			return repeatA(spliceAt-2) + "e\u0301" + repeatA(spliceWidth-spliceAt+1)
		}, true},
		{"a combining mark after the edge", func(y int) string {
			return repeatA(spliceAt-1) + "e\u0301" + repeatA(spliceWidth-spliceAt)
		}, true},
		{"a control character in the row", func(y int) string { return repeatA(20) + "\x07" + repeatA(spliceWidth-20) }, true},

		// A string sequence rides on the next cell, so one at the edge would
		// reach the drawer's first cell, and a carriage return moves the column.
		{"image sequences before and after the edge", func(y int) string {
			return apc + repeatA(spliceAt-2) + "\x1b_Gi=2,a=T;BBBB\x1b\\" + repeatA(spliceWidth-spliceAt+2)
		}, false},
		{"an APC just before the edge", func(y int) string { return repeatA(spliceAt) + apc + repeatA(spliceWidth-spliceAt) }, false},
		{"a DCS just before the edge", func(y int) string { return repeatA(spliceAt) + dcs + repeatA(spliceWidth-spliceAt) }, false},
		{"an SOS just before the edge", func(y int) string { return repeatA(spliceAt) + sos + repeatA(spliceWidth-spliceAt) }, false},
		{"a PM just before the edge", func(y int) string { return repeatA(spliceAt) + pm + repeatA(spliceWidth-spliceAt) }, false},
		{"an APC well before the edge", func(y int) string { return repeatA(5) + apc + repeatA(spliceWidth-5) }, false},
		{"an APC at the end of a short row", short(repeatA(10) + apc), false},
		{"a DCS at the end of a short row", short(repeatA(10) + dcs), false},
		{"an APC at the end of a full row", func(y int) string { return repeatA(spliceWidth) + apc }, false},
		{"an APC before the wide character that straddles", func(y int) string {
			return repeatA(spliceAt-1) + apc + "中" + repeatA(spliceWidth-spliceAt-1)
		}, false},
		{"a carriage return before the edge", func(y int) string { return repeatA(14) + "\r" + repeatA(spliceWidth-14) }, false},
		{"a carriage return after the edge", func(y int) string { return repeatA(30) + "\r" + repeatA(10) }, false},

		// A row that leaves a style or link open carries it into the next row.
		{"style never reset", func(y int) string { return sgrOpen + repeatA(spliceWidth) }, false},
		{"link never closed", func(y int) string { return linkOn + repeatA(spliceWidth) }, false},
		{"a styled row shorter than the edge", short(sgrOpen + repeatA(10)), false},
		{"style and link never closed", func(y int) string {
			return sgrOpen + linkOn + repeatA(spliceAt-1) + "中" + repeatA(spliceWidth-spliceAt-1)
		}, false},
	}

	drawers := []struct {
		name string
		line func(i int) string
	}{{"styled drawer", styledDrawer}, {"unstyled drawer", bareDrawer}, {"drawer led by a tag", taggedDrawer}}

	r := core.Rect{X: spliceAt, Y: 1, W: spliceWidth - spliceAt, H: spliceHeight - 1}

	for _, tt := range tests {
		for _, dr := range drawers {
			t.Run(tt.name+"/"+dr.name, func(t *testing.T) {
				base := baseOf(tt.row)
				lines := make([]string, r.H)

				for i := range lines {
					lines[i] = dr.line(i)

					if w := ansi.StringWidth(lines[i]); w != r.W || !endsClean(lines[i]) {
						t.Fatalf("fixture line %d is %d cells or ends open, want %d cells and clean", i, w, r.W)
					}
				}

				want := overlayAt(nil, base, spliceWidth, spliceHeight, r.X, r.Y, lines, nil)
				got, _ := overlayDrawer(nil, base, spliceWidth, spliceHeight, r, lines, nil, true)

				requireSameCells(t, got, want, spliceWidth, spliceHeight)

				if _, ok := spliceDrawer(base, spliceWidth, spliceHeight, r, lines, true); ok != tt.spliced {
					t.Errorf("spliced = %v, want %v", ok, tt.spliced)
				}

				if got == base {
					t.Fatal("the drawer was not drawn")
				}
			})
		}
	}
}

// The drawer can reach the left edge of the screen.
func TestOverlayDrawerMatchesTheCompositorAtTheLeftEdge(t *testing.T) {
	t.Parallel()

	base := baseOf(func(y int) string { return sgrOpen + linkOn + repeatA(spliceWidth) + linkOff + reset })
	r := core.Rect{X: 0, Y: 1, W: spliceWidth, H: spliceHeight - 1}
	lines := slices.Repeat([]string{strings.Repeat("-", spliceWidth)}, r.H)

	want := overlayAt(nil, base, spliceWidth, spliceHeight, r.X, r.Y, lines, nil)
	got, _ := overlayDrawer(nil, base, spliceWidth, spliceHeight, r, lines, nil, true)

	requireSameCells(t, got, want, spliceWidth, spliceHeight)

	if _, ok := spliceDrawer(base, spliceWidth, spliceHeight, r, lines, true); !ok {
		t.Error("the drawer over the whole width was not spliced")
	}
}

// What the splice cannot do cell for cell it hands to the compositor, and the
// result is the compositor's.
func TestOverlayDrawerFallsBackToTheCompositor(t *testing.T) {
	t.Parallel()

	plain := baseOf(func(y int) string { return repeatA(spliceWidth) })
	dashes := func(h, w int) []string { return slices.Repeat([]string{strings.Repeat("d", w)}, h) }

	tests := []struct {
		name  string
		r     core.Rect
		lines []string
		fits  bool
		base  string
	}{
		{"the lines are not all as wide as the rect", core.Rect{X: 24, Y: 1, W: 16, H: 7}, dashes(7, 16), false, plain},
		{"the rect stops short of the right edge", core.Rect{X: 20, Y: 1, W: 16, H: 7}, dashes(7, 16), true, plain},
		{"the rect runs off the bottom", core.Rect{X: 24, Y: 3, W: 16, H: 7}, dashes(7, 16), true, plain},
		{"the rect starts above the screen", core.Rect{X: 24, Y: -1, W: 16, H: 7}, dashes(7, 16), true, plain},
		{"the base has more rows than the screen", core.Rect{X: 24, Y: 1, W: 16, H: 6}, dashes(6, 16), true, strings.Join(slices.Repeat([]string{repeatA(spliceWidth)}, spliceHeight+2), "\n")},
		{"no lines", core.Rect{X: 24, Y: 1, W: 16, H: 0}, nil, true, plain},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, ok := spliceDrawer(tt.base, spliceWidth, spliceHeight, tt.r, tt.lines, tt.fits); ok {
				t.Error("the splice applied, want the compositor")
			}

			want := overlayAt(nil, tt.base, spliceWidth, spliceHeight, tt.r.X, tt.r.Y, tt.lines, nil)
			if got, spliced := overlayDrawer(nil, tt.base, spliceWidth, spliceHeight, tt.r, tt.lines, nil, tt.fits); got != want || spliced {
				t.Errorf("spliced = %v, or the compositor's result was not used", spliced)
			}
		})
	}
}

// cutRow keeps the first x cells, closes what the row left open and drops
// everything after x.
func TestCutRow(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		row  string
		x    int
		want string
	}{
		{"open style and link", sgrOpen + linkOn + repeatA(30), 10, sgrOpen + linkOn + repeatA(10) + drawerReset},
		{"sequences after the cut go", repeatA(12) + "\x1b[31m" + repeatA(5) + "\x1b_Gi=1;AA\x1b\\", 10, repeatA(10) + drawerReset},
		{"wide character across the cut", "\x1b[31m" + repeatA(9) + "中tail", 10, "\x1b[31m" + repeatA(9) + " " + drawerReset},
		{"short row", repeatA(4), 10, repeatA(4) + drawerReset + strings.Repeat(" ", 6)},
		{"row of exactly x cells", repeatA(10), 10, repeatA(10) + drawerReset},
		{"nothing kept", repeatA(10), 0, drawerReset},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := cutRow(tt.row, tt.x); got != tt.want {
				t.Errorf("cutRow = %q, want %q", got, tt.want)
			}
		})
	}
}

// endsClean is true only when nothing is left open at the end.
func TestEndsClean(t *testing.T) {
	t.Parallel()

	tests := []struct {
		s    string
		want bool
	}{
		{"", true},
		{"plain", true},
		{sgrOpen + "x" + reset, true},
		{sgrOpen + "x\x1b[0m", true},
		{sgrOpen + "x", false},
		{sgrOpen + "x" + reset + "\x1b[1m", false},
		{linkOn + "x" + linkOff, true},
		{linkOn + "x\x1b]8;;\a", true},
		{linkOn + "x", false},
		{linkOn + "x" + linkOff + linkOn, false},
		{"x\x1b[2K", false},
	}

	for _, tt := range tests {
		if got := endsClean(tt.s); got != tt.want {
			t.Errorf("endsClean(%q) = %v, want %v", tt.s, got, tt.want)
		}
	}
}

// carriesAcrossCells finds what uv carries from one cell to another.
func TestCarriesAcrossCells(t *testing.T) {
	t.Parallel()

	tests := []struct {
		s    string
		want bool
	}{
		{"", false},
		{"plain", false},
		{sgrOpen + "x" + reset + linkOn + "y" + linkOff, false},
		{"\x1b[2K", false},
		{"x" + apc, true},
		{dcs + "x", true},
		{"x" + sos, true},
		{pm, true},
		{"abc\rdef", true},
	}

	for _, tt := range tests {
		if got := carriesAcrossCells(tt.s); got != tt.want {
			t.Errorf("carriesAcrossCells(%q) = %v, want %v", tt.s, got, tt.want)
		}
	}
}

// drawDrawer counts the frames the compositor draws in the splice's place, and
// draws what the compositor draws.
func TestDrawDrawerCountsFallbacks(t *testing.T) {
	t.Parallel()
	a := cacheScene().build(t)
	r := drawerRectOf(t, a, 0)
	base := a.frameAt(a.geometry())
	lines, _, fits := a.drawerDrawn(a.store.State(), r)

	if !fits {
		t.Fatal("the drawer's lines do not fit the splice")
	}

	a.drawDrawer(nil, base, r, lines, nil, fits)

	if a.drawer.fallbacks != 0 {
		t.Fatalf("%d fallbacks for a frame the splice covers, want 0", a.drawer.fallbacks)
	}

	rows := strings.Split(base, "\n")
	rows[r.Y+3] += apc
	held := strings.Join(rows, "\n")

	got := a.drawDrawer(nil, held, r, lines, nil, fits)
	requireSameCells(t, got, overlayAt(nil, held, a.width, a.height, r.X, r.Y, lines, nil), a.width, a.height)

	if a.drawer.fallbacks != 1 {
		t.Errorf("%d fallbacks after a row the splice declines, want 1", a.drawer.fallbacks)
	}

	a.drawDrawer(nil, base, r, lines, nil, false)

	if a.drawer.fallbacks != 2 {
		t.Errorf("%d fallbacks after lines that do not fit, want 2", a.drawer.fallbacks)
	}
}

// Every drawer scene, drawn whole and part way through a slide, must leave
// the cells the compositor leaves.
func TestOverlayDrawerMatchesTheCompositorOnTheDrawerScenes(t *testing.T) {
	t.Parallel()

	for _, c := range reviewCases() {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()

			a := c.scene("catppuccin-mocha", true).build(t)
			st := a.store.State()

			// The whole drawer first: its frame is also the View's.
			for _, slide := range []int{0, 7} {
				r := drawerRectOf(t, a, slide)
				r.X += slide

				base := a.frameAt(a.geometry())
				lines, _, fits := a.drawerDrawn(st, r)

				want := overlayAt(nil, base, a.width, a.height, r.X, r.Y, lines, nil)
				got, _ := overlayDrawer(nil, base, a.width, a.height, r, lines, nil, fits)

				requireSameCells(t, got, want, a.width, a.height)

				if _, ok := spliceDrawer(base, a.width, a.height, r, lines, fits); !ok {
					t.Errorf("the drawer scene was not spliced at slide %d", slide)
				}

				if slide == 0 {
					requireSameCells(t, a.View().Content, want, a.width, a.height)

					if a.drawer.fallbacks != 0 {
						t.Errorf("View drew the drawer with the compositor %d times", a.drawer.fallbacks)
					}
				}
			}
		})
	}
}
