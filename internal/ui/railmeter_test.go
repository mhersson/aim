package ui

import (
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/x/ansi"

	"github.com/mhersson/aim/internal/core"
	"github.com/mhersson/aim/internal/ui/theme"
)

func runsText(runs []railRun) string {
	var sb strings.Builder

	for _, r := range runs {
		sb.WriteString(r.text)
	}

	return sb.String()
}

// cellBelow is the column and row of the first occurrence of text on the rows of
// view from row from, counting cells.
func cellBelow(t *testing.T, view string, from int, text string) (x, y int) {
	t.Helper()

	for y, line := range strings.Split(view, "\n")[from:] {
		if i := strings.Index(ansi.Strip(line), text); i >= 0 {
			return ansi.StringWidth(ansi.Strip(line)[:i]), y + from
		}
	}

	t.Fatalf("no %q below row %d", text, from)

	return 0, 0
}

func TestRailMeterColours(t *testing.T) {
	t.Parallel()
	ui := mochaUI(t)

	tests := []struct {
		pct  int
		fg   theme.Hex
		bold bool
	}{
		{69, ui.Meter, false},
		{70, ui.Warning, false},
		{89, ui.Warning, false},
		{90, ui.Error, true},
	}

	for _, tt := range tests {
		t.Run(strconv.Itoa(tt.pct), func(t *testing.T) {
			agents := []railAgent{{newTab: true, project: "api", branch: "main", state: core.AgentIdle, age: 8 * time.Second, model: "Opus 5.5", meter: new(tt.pct)}}
			a := drawApp(t, "catppuccin-mocha", true, 100, 30, agents)
			vt := viewVT(t, a)

			// The rail's cards start under the header, row 3 is the first blank row.
			text := strconv.Itoa(tt.pct) + "%"
			x, y := cellBelow(t, a.View().Content, 3, text)

			for i, r := range []rune(text) {
				assertColours(t, vt, x+i, y, string(r), tt.fg, ui.Bg, tt.bold)
			}

			// The model on the same line is Subtle.
			mx, my := cellBelow(t, a.View().Content, 3, "Opus")
			if my != y {
				t.Fatalf("model on row %d, meter on row %d", my, y)
			}

			assertColours(t, vt, mx, my, "O", ui.Subtle, ui.Bg, false)
		})
	}
}

func TestRailLineOneDropOrder(t *testing.T) {
	t.Parallel()
	a := newTestApp(t)
	a.clock = &fakeClock{t: t0}

	ui := a.current().theme.UI
	head := iconIdle + " multiplex"
	spaces := func(n int) string { return strings.Repeat(" ", n) }

	base := railCard{
		pane: "p1", state: core.AgentIdle, project: "multiplex",
		model: "Opus 5.5", meter: 62, meterKnown: true,
		active: t0.Add(-8 * time.Second), unseen: true,
	}

	tests := []struct {
		name string
		cw   int
		tune func(c *railCard)
		want string
	}{
		{"everything fits", 40, nil, head + "  Opus 5.5" + spaces(10) + "62%  8s ●"},
		{"everything fits exactly", 31, nil, head + "  Opus 5.5" + " " + "62%  8s ●"},
		{"the meter goes first", 30, nil, head + "  Opus 5.5" + spaces(5) + "8s ●"},
		{"the meter goes first, to the last cell", 26, nil, head + "  Opus 5.5" + " " + "8s ●"},
		{"then the model", 25, nil, head + spaces(10) + "8s ●"},
		{"then the model, to the last cell", 16, nil, head + " " + "8s ●"},
		{"then the name is cut", 15, nil, iconIdle + " multipl…" + " " + "8s ●"},
		{"the name is cut to the glyph", 8, nil, iconIdle + " …" + " " + "8s ●"},
		{"no dot", 40, func(c *railCard) { c.unseen = false }, head + "  Opus 5.5" + spaces(12) + "62%  8s"},
		{"no time", 40, func(c *railCard) { c.unseen, c.active = false, time.Time{} }, head + "  Opus 5.5" + spaces(16) + "62%"},
		{"zero is shown", 40, func(c *railCard) { c.meter = 0 }, head + "  Opus 5.5" + spaces(11) + "0%  8s ●"},
		{"a model alone", 40, func(c *railCard) { c.meterKnown = false }, head + "  Opus 5.5" + spaces(15) + "8s ●"},
		{"a meter alone", 40, func(c *railCard) { c.model = "" }, head + spaces(20) + "62%  8s ●"},
		{"no status", 40, func(c *railCard) { c.model, c.meterKnown = "", false }, head + spaces(25) + "8s ●"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := base
			if tt.tune != nil {
				tt.tune(&c)
			}

			runs := a.railTitleLine(ui, c, nil, tt.cw)
			if got := runsText(runs); got != tt.want {
				t.Errorf("line = %q, want %q", got, tt.want)
			}

			if w := runsWidth(runs); w > tt.cw {
				t.Errorf("line is %d cells wide, want at most %d", w, tt.cw)
			}
		})
	}
}

// Core clamps what it records, so the drawing must not rely on that: a card and a
// frame tag never show a value outside 0-100.
func TestMeterIsClampedForDrawing(t *testing.T) {
	t.Parallel()

	tests := []struct {
		held int
		want string
	}{
		{101, "100%"},
		{100, "100%"},
		{0, "0%"},
		{-1, "0%"},
	}

	for _, tt := range tests {
		t.Run(strconv.Itoa(tt.held), func(t *testing.T) {
			agents := []railAgent{{newTab: true, project: "api", branch: "main", state: core.AgentWorking, model: "Opus 5.5", meter: new(50)}}
			a := drawApp(t, "catppuccin-mocha", true, 100, 30, agents)

			st := a.store.State()
			p := st.Panes["p1"]
			p.Context = tt.held
			st.Panes["p1"] = p
			a.store, a.storeVer = core.NewStore(st), a.storeVer+1

			out := ansi.Strip(a.View().Content)

			// Once on the card and once in the frame's tag.
			if got := strings.Count(out, " "+tt.want+" "); got != 2 {
				t.Errorf("%q shown %d times, want 2 (card and tag)", tt.want, got)
			}

			if shown := strconv.Itoa(tt.held) + "%"; tt.held != 100 && tt.held != 0 && strings.Contains(out, shown) {
				t.Errorf("the view shows %q, want it clamped", shown)
			}
		})
	}
}
