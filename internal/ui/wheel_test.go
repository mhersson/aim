package ui

import (
	"fmt"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/mhersson/aim/internal/core"
)

// manyLines is n printed lines.
func manyLines(n int) string {
	var sb strings.Builder
	for i := range n {
		sb.WriteString(lineName(i) + "\r\n")
	}

	return sb.String()
}

// wheelAt sends one wheel notch at screen cell (x, y).
func wheelAt(a *App, x, y int, b tea.MouseButton) {
	a.Update(tea.MouseWheelMsg{X: x, Y: y, Button: b})
}

// centre is a content cell of pane id.
func centre(a *App, id core.PaneID) (x, y int) {
	c := contentRect(a.layoutRects()[id])

	return c.X + c.W/2, c.Y + c.H/2
}

// twoShells is a 60x14 app with two side-by-side shells; p2 is focused and p1
// holds scrollback.
func twoShells(t *testing.T) (*App, map[core.PaneID]*fakeProc) {
	t.Helper()

	a, procs := framedApp(t, 60, 14,
		shellSpec("/w/a", manyLines(30)),
		shellSpec("/w/b", ""))

	if got := a.store.State().Tab().Focus; got != "p2" {
		t.Fatalf("focus = %q, want p2", got)
	}

	return a, procs
}

func TestWheelScrollsUnfocusedPane(t *testing.T) {
	t.Parallel()
	a, _ := twoShells(t)
	x, y := centre(a, "p1")

	wheelAt(a, x, y, tea.MouseWheelUp)

	if got := a.panes["p1"].linesAbove(); got != 3 {
		t.Fatalf("p1 linesAbove = %d, want 3", got)
	}

	if got := a.store.State().Tab().Focus; got != "p2" {
		t.Errorf("focus = %q after the wheel, want it unchanged (p2)", got)
	}

	if got := a.panes["p2"].linesAbove(); got != 0 {
		t.Errorf("p2 scrolled by a wheel over p1")
	}

	wheelAt(a, x, y, tea.MouseWheelUp)
	wheelAt(a, x, y, tea.MouseWheelDown)

	if got := a.panes["p1"].linesAbove(); got != 3 {
		t.Errorf("after up, up, down linesAbove = %d, want 3", got)
	}

	wheelAt(a, x, y, tea.MouseWheelDown)

	if got := a.panes["p1"].linesAbove(); got != 0 {
		t.Errorf("scrolling down to the bottom left linesAbove = %d, want 0 (live)", got)
	}
}

func TestWheelForwardsToMouseTrackingGuest(t *testing.T) {
	t.Parallel()
	a, procs := twoShells(t)
	p := a.panes["p1"]

	feedAndWait(t, p, procs["p1"], "\x1b[?1000h\x1b[?1006h")

	c := contentRect(a.layoutRects()["p1"])
	wheelAt(a, c.X+4, c.Y+2, tea.MouseWheelUp)

	// Pane-relative, 1-based: (4, 2) -> (5, 3). Wheel up is button 64.
	want := "\x1b[<64;5;3M"

	eventually(t, "forwarded wheel", func() bool { return strings.Contains(procs["p1"].sent(), want) })

	if got := p.linesAbove(); got != 0 {
		t.Errorf("a tracking pane scrolled its view (%d)", got)
	}

	wheelAt(a, c.X+4, c.Y+2, tea.MouseWheelDown)

	want = "\x1b[<65;5;3M"

	eventually(t, "forwarded wheel down", func() bool { return strings.Contains(procs["p1"].sent(), want) })
}

func TestWheelToTrackingGuestReturnsLive(t *testing.T) {
	t.Parallel()
	a, procs := twoShells(t)
	p := a.panes["p1"]
	x, y := centre(a, "p1")

	wheelAt(a, x, y, tea.MouseWheelUp)

	if p.linesAbove() != 3 {
		t.Fatal("p1 did not scroll")
	}

	feedAndWait(t, p, procs["p1"], "\x1b[?1000h\x1b[?1006h")
	wheelAt(a, x, y, tea.MouseWheelDown)

	if got := p.linesAbove(); got != 0 {
		t.Errorf("linesAbove = %d once the guest tracks the mouse, want 0", got)
	}
}

func TestWheelOnAltScreenSendsCursorKeys(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		modes  string
		button tea.MouseButton
		want   string
	}{
		{"up", "", tea.MouseWheelUp, "\x1b[A\x1b[A\x1b[A"},
		{"down", "", tea.MouseWheelDown, "\x1b[B\x1b[B\x1b[B"},
		{"application cursor keys", "\x1b[?1h", tea.MouseWheelUp, "\x1bOA\x1bOA\x1bOA"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			a, procs := twoShells(t)
			feedAndWait(t, a.panes["p1"], procs["p1"], "\x1b[?1049h"+tt.modes)

			x, y := centre(a, "p1")
			wheelAt(a, x, y, tt.button)

			eventually(t, "cursor keys", func() bool { return procs["p1"].sent() == tt.want })

			if got := a.panes["p1"].linesAbove(); got != 0 {
				t.Errorf("alt screen pane scrolled its view (%d)", got)
			}
		})
	}
}

func TestWheelIgnoredOutsideContent(t *testing.T) {
	t.Parallel()
	a, procs := twoShells(t)
	r1 := a.layoutRects()["p1"]
	c1 := contentRect(r1)

	cells := []struct {
		name string
		x, y int
	}{
		{"header", c1.X + 2, r1.Y},
		{"left border", c1.X - 1, c1.Y + 2},
		{"workspace bar", c1.X + 2, 0},
		{"off screen", 500, 500},
		{"bottom border", c1.X + 2, c1.Y + c1.H},
	}

	for _, tt := range cells {
		t.Run(tt.name, func(t *testing.T) {
			wheelAt(a, tt.x, tt.y, tea.MouseWheelUp)
		})
	}

	for id, fp := range procs {
		if got := a.panes[id].linesAbove(); got != 0 {
			t.Errorf("%s scrolled %d lines from a wheel outside any content", id, got)
		}

		if got := sentAfterMark(t, a.panes[id], fp); got != markOf(a.panes[id]) {
			t.Errorf("%s was sent %q, want only the marker", id, got)
		}
	}
}

func TestKeyToScrolledPaneReturnsLive(t *testing.T) {
	t.Parallel()
	a, _ := twoShells(t)
	x, y := centre(a, "p2")

	// p2 is the focused pane; give it history first.
	feedAndWait(t, a.panes["p2"], a.panes["p2"].proc.(*fakeProc), manyLines(30))
	a.panes["p2"].render()

	wheelAt(a, x, y, tea.MouseWheelUp)

	if a.panes["p2"].linesAbove() != 3 {
		t.Fatal("p2 did not scroll")
	}

	press(a, keyRune('a'))

	if got := a.panes["p2"].linesAbove(); got != 0 {
		t.Errorf("linesAbove = %d after a key, want 0", got)
	}
}

func TestLeaderCommandLeavesScrollAlone(t *testing.T) {
	t.Parallel()
	a, _ := twoShells(t)
	x, y := centre(a, "p1")
	wheelAt(a, x, y, tea.MouseWheelUp)

	// A leader command goes to the app, never to a pane.
	press(a, keyLeader, keyRune(':'))

	if a.overlay == nil {
		t.Fatal("the palette did not open")
	}

	if got := a.panes["p1"].linesAbove(); got != 3 {
		t.Errorf("linesAbove = %d after a leader command, want 3", got)
	}
}

func TestScrollTag(t *testing.T) {
	t.Parallel()

	zoom := " " + iconZoom + " zoom "
	scroll := " " + iconScroll + " 3 "
	tests := []struct {
		name  string
		width int
		icons bool
		zoom  bool
		want  string
	}{
		{"tag", 40, true, false, fill("╭─ "+iconShell+" shell ", 33) + scroll + "─╮"},
		{"no icons", 40, false, false, fill("╭─ shell ", 33) + " \u2191 3 ─╮"},
		{"left of zoom", 50, true, true, fill("╭─ "+iconShell+" shell ", 34) + scroll + "─" + zoom + "─╮"},
		{"takes title room", 17, true, false, "╭─ " + iconShell + " she… " + scroll + "─╮"},
		{"alone", 8, true, false, "╭" + scroll + "─╮"},
		{"does not fit", 7, true, false, dashes("╭", 7)},
		{"both alone", 17, true, true, "╭" + scroll + "─" + zoom + "─╮"},
		{"zoom wins when only one fits", 11, true, true, "╭" + zoom + "─╮"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			a, _ := framedApp(t, tt.width, 8, shellSpec("/home/u/proj", manyLines(30)))
			a.icons = tt.icons

			if tt.zoom {
				if err := a.dispatch(core.ToggleZoom{}); err != nil {
					t.Fatal(err)
				}
			}

			x, y := centre(a, "p1")
			wheelAt(a, x, y, tea.MouseWheelUp)

			if got := topBorder(a); got != tt.want {
				t.Errorf("top border = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestScrollTagFollowsLines(t *testing.T) {
	t.Parallel()
	a, procs := framedApp(t, 50, 8, shellSpec("/w/proj", manyLines(30)))
	x, y := centre(a, "p1")

	wheelAt(a, x, y, tea.MouseWheelUp)
	wheelAt(a, x, y, tea.MouseWheelUp)

	if got := topBorder(a); !strings.Contains(got, fmt.Sprintf(" %s 6 ", iconScroll)) {
		t.Fatalf("top border = %q, want the tag with 6", got)
	}

	feedAndWait(t, a.panes["p1"], procs["p1"], "a\r\nb\r\n")
	a.panes["p1"].render()

	if got := topBorder(a); !strings.Contains(got, fmt.Sprintf(" %s 8 ", iconScroll)) {
		t.Fatalf("top border = %q, want the tag with 8 after two more lines", got)
	}

	wheelAt(a, x, y, tea.MouseWheelDown)
	wheelAt(a, x, y, tea.MouseWheelDown)
	wheelAt(a, x, y, tea.MouseWheelDown)

	if got := topBorder(a); strings.Contains(got, iconScroll) {
		t.Fatalf("top border = %q, want no tag when live", got)
	}
}
