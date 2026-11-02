package ui

import (
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/charmbracelet/x/exp/golden"

	"github.com/mhersson/aim/internal/core"
	"github.com/mhersson/aim/internal/ui/theme"
)

var (
	usageFive = core.UsageWindow{Used: 23, ResetsAt: t0.Add(2 * time.Hour)}
	usageWeek = core.UsageWindow{Used: 41, ResetsAt: t0.Add(52 * time.Hour)}
)

// seedUsage offers the windows to the store at the app's clock and fails the
// test when they are refused.
func seedUsage(t *testing.T, a *App, five, week core.UsageWindow) {
	t.Helper()

	act := core.NoteUsage{FiveHour: five, SevenDay: week, At: a.clock.Now()}
	if !act.Changes(a.store.State().Usage) {
		t.Fatalf("usage %+v is refused at %v", act, act.At)
	}

	if err := a.dispatch(act); err != nil {
		t.Fatal(err)
	}
}

func TestUsageModuleText(t *testing.T) {
	t.Parallel()

	withUsed := func(w core.UsageWindow, used int) core.UsageWindow {
		w.Used = used

		return w
	}

	tests := []struct {
		name  string
		icons bool
		five  core.UsageWindow
		week  core.UsageWindow
		at    time.Duration
		want  string // empty: no module
	}{
		{"both", true, usageFive, usageWeek, 0, " \U000F04C5 5h 23%  7d 41% "},
		{"both, no icons", false, usageFive, usageWeek, 0, " 5h 23%  7d 41% "},
		{"weekly only", true, core.UsageWindow{}, usageWeek, 0, " \U000F04C5 7d 41% "},
		{"one digit", false, withUsed(usageFive, 5), core.UsageWindow{}, 0, " 5h  5% "},
		{"full", false, core.UsageWindow{}, withUsed(usageWeek, 100), 0, " 7d 100% "},
		{"5h reset passed", true, usageFive, usageWeek, 3 * time.Hour, " \U000F04C5 7d 41% "},
		{"both passed", true, usageFive, usageWeek, 53 * time.Hour, ""},
		{"none", true, core.UsageWindow{}, core.UsageWindow{}, 0, ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			u := core.Usage{FiveHour: tt.five, SevenDay: tt.week}
			m, ok := usageModule(u, t0.Add(tt.at), tt.icons, mochaUI(t))

			if (tt.want != "") != ok {
				t.Fatalf("ok = %v, want %v", ok, tt.want != "")
			}

			if !ok {
				return
			}

			if got := ansi.Strip(m.render()); got != tt.want {
				t.Errorf("text = %q, want %q", got, tt.want)
			}

			if got := lipgloss.Width(m.render()); got != m.width {
				t.Errorf("renders %d cells, width says %d", got, m.width)
			}
		})
	}
}

func TestUsageWindows(t *testing.T) {
	t.Parallel()

	u := core.Usage{FiveHour: usageFive, SevenDay: usageWeek}
	got := usageWindows(u, t0)

	if len(got) != 2 || got[0].label != "5h" || got[0].name != "Session" || got[1].label != "7d" || got[1].name != "Week" {
		t.Fatalf("windows = %+v", got)
	}

	if got := usageWindows(u, t0.Add(3*time.Hour)); len(got) != 1 || got[0].label != "7d" {
		t.Errorf("after the 5h reset: %+v", got)
	}
}

func TestUsageModuleColours(t *testing.T) {
	t.Parallel()

	ui := mochaUI(t)
	win := func(used int, resets time.Duration) core.UsageWindow {
		return core.UsageWindow{Used: used, ResetsAt: t0.Add(resets)}
	}

	tests := []struct {
		name       string
		five, week int
		fiveFg     theme.Hex
		weekFg     theme.Hex
		iconFg     theme.Hex
		fiveBold   bool
		weekBold   bool
	}{
		{"cool", 23, 69, ui.Meter, ui.Meter, ui.Meter, false, false},
		{"warm", 70, 89, ui.Warning, ui.Warning, ui.Warning, false, false},
		{"hot", 90, 100, ui.Error, ui.Error, ui.Error, true, true},
		{"hottest icon", 95, 41, ui.Error, ui.Meter, ui.Error, true, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			a := trayApp(t, true)
			a.clock = &fakeClock{t: t0}
			seedUsage(t, a, win(tt.five, 2*time.Hour), win(tt.week, 52*time.Hour))

			w := 100
			vt := lineVT(t, a.workspaceBar(w), w)

			clockW := lipgloss.Width(trayModuleByID(a, modClock).render())
			loadX := w - clockW - 1 - trayModuleByID(a, modLoad).width
			x := loadX - 1 - trayModuleByID(a, modUsage).width

			text := func(at int, s string, fg theme.Hex, bold bool) {
				for i, r := range []rune(s) {
					assertColours(t, vt, at+i, 0, string(r), fg, ui.Surface, bold)
				}
			}

			// " icon 5h NN%  7d NN% "
			text(x+1, iconUsage, tt.iconFg, false)
			text(x+3, "5h", ui.Muted, false)
			text(x+6, fmt.Sprintf("%2d%%", tt.five), tt.fiveFg, tt.fiveBold)
			text(x+11, "7d", ui.Muted, false)
			text(x+14, fmt.Sprintf("%2d%%", tt.week), tt.weekFg, tt.weekBold)

			for _, at := range []int{x, x + 2, x + 5, x + 9, x + 10, x + 13, x + 17} {
				c := vt.CellAt(at, 0)
				if c == nil || c.Style.Bg == nil || hexOf(c.Style.Bg) != string(ui.Surface) {
					t.Errorf("separator cell %d: %+v, want a space on Surface", at, c)
				}
			}
		})
	}
}

func TestTrayUsageModuleSitsBetweenPRAndLoad(t *testing.T) {
	t.Parallel()

	a := prTrayApp(t, true, prInfo("OPEN", checksRunning))
	a.clock = &fakeClock{t: t0}
	seedUsage(t, a, usageFive, usageWeek)

	want := []string{modLayout, modPR, modUsage, modLoad, modClock}
	if got := modIDs(a.trayModules()); !slices.Equal(got, want) {
		t.Errorf("modules = %v, want %v", got, want)
	}
}

// usageAppWith is a tray app on a fake clock with the windows seeded three
// minutes before t0, so the age reads "updated 3 min ago"; the clock then
// stands at t0.
func usageAppWith(t *testing.T, icons bool, five, week core.UsageWindow) (*App, *fakeClock) {
	t.Helper()

	a := trayApp(t, icons)
	clk := &fakeClock{t: t0.Add(-3 * time.Minute)}
	a.clock = clk
	seedUsage(t, a, five, week)
	clk.Set(t0)

	return a, clk
}

func usageApp(t *testing.T, icons bool) (*App, *fakeClock) {
	t.Helper()

	return usageAppWith(t, icons, usageFive, usageWeek)
}

// usageBox is the popover's lines, stripped, and its rect.
func usageBox(t *testing.T, a *App) ([]string, core.Rect) {
	t.Helper()

	lines, r, ok := a.usagePopover(a.current())
	if !ok {
		t.Fatal("the popover does not show")
	}

	out := make([]string, len(lines))
	for i, l := range lines {
		out[i] = ansi.Strip(l)
	}

	return out, r
}

func TestUsagePopoverText(t *testing.T) {
	t.Parallel()

	bar := func(n int) string { return ansi.Strip(usageBar(n, 20, "", mochaUI(t))) }
	both := []string{
		"Session  " + bar(23) + "  23%",
		"         resets in 2h 0m (02:00)",
		"",
		"Week     " + bar(41) + "  41%",
		"         resets in 2d 4h (Sat 04:00)",
		"",
		"updated 3 min ago",
		"",
		"esc close",
	}

	tests := []struct {
		name  string
		icons bool
		five  core.UsageWindow
		title string
		rows  []string
		w     int
	}{
		{"both", true, usageFive, "\u256d\u2500 \U000F04C5 Claude usage ", both, 42},
		{"icons off", false, usageFive, "\u256d\u2500 Claude usage ", both, 42},
		{"weekly only", true, core.UsageWindow{}, "\u256d\u2500 \U000F04C5 Claude usage ", []string{
			"Week  " + bar(41) + "  41%",
			"      resets in 2d 4h (Sat 04:00)",
			"",
			"updated 3 min ago",
			"",
			"esc close",
		}, 39},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			a, _ := usageAppWith(t, tt.icons, tt.five, usageWeek)
			chord(a, 'u')

			lines, r := usageBox(t, a)
			if len(lines) != len(tt.rows)+2 || r.W != tt.w || r.H != len(lines) {
				t.Fatalf("box is %dx%d with %d lines, want %d wide and %d tall", r.W, r.H, len(lines), tt.w, len(tt.rows)+2)
			}

			if !strings.HasPrefix(lines[0], tt.title) {
				t.Errorf("top = %q, want prefix %q", lines[0], tt.title)
			}

			for i, want := range tt.rows {
				row := strings.TrimPrefix(lines[i+1], "\u2502")
				row = strings.TrimSuffix(row, "\u2502")

				if got := strings.TrimRight(strings.TrimPrefix(row, "  "), " "); got != want {
					t.Errorf("row %d = %q, want %q", i, got, want)
				}
			}
		})
	}
}

func TestUsagePopoverGolden(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		icons      bool
		five, week int
	}{
		{"both calm", true, 23, 41},
		{"hot and warm", true, 95, 74},
		{"icons off", false, 23, 41},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			five, week := usageFive, usageWeek
			five.Used, week.Used = tt.five, tt.week

			a, _ := usageAppWith(t, tt.icons, five, week)
			chord(a, 'u')

			lines, _, ok := a.usagePopover(a.current())
			if !ok {
				t.Fatal("the popover does not show")
			}

			golden.RequireEqual(t, []byte(strings.Join(lines, "\n")))
		})
	}
}

func TestUsagePopoverPlacement(t *testing.T) {
	t.Parallel()

	a, _ := usageApp(t, true)
	chord(a, 'u')

	check := func(t *testing.T, width int) core.Rect {
		t.Helper()

		sized(t, a, width, 30)

		lines, r := usageBox(t, a)
		view := strings.Split(a.View().Content, "\n")

		for i, l := range view {
			if w := lipgloss.Width(l); w != width {
				t.Fatalf("width %d: line %d is %d cells", width, i, w)
			}
		}

		got := ansi.Strip(ansi.Cut(view[1], r.X, r.X+r.W))
		if got != lines[0] {
			t.Errorf("width %d: row 1 at the box shows %q, want %q", width, got, lines[0])
		}

		return r
	}

	t.Run("on the bar", func(t *testing.T) {
		r := check(t, 100)

		p := planBar(a.tabInfos(), a.store.State().Active, a.trayModules(), 100, a.icons)

		x, w, ok := trayModuleAt(p, 100, a.icons, modUsage)
		if cx, cw, cok := trayModuleAt(p, 100, a.icons, modClock); !cok || cx+cw != 100 {
			t.Errorf("clock at %d, %d wide (%v), want it to end the bar", cx, cw, cok)
		}

		if !ok || r.Y != 1 || r.X != x || w != trayModuleByID(a, modUsage).width {
			t.Errorf("rect %+v, module at %d, %d wide (%v), want y 1 and x %d", r, x, w, ok, x)
		}
	})

	t.Run("off the bar", func(t *testing.T) {
		width := 99

		for ; width > 40; width-- {
			sized(t, a, width, 30)

			p := planBar(a.tabInfos(), a.store.State().Active, a.trayModules(), width, a.icons)
			if _, _, ok := trayModuleAt(p, width, a.icons, modUsage); !ok {
				break
			}
		}

		sized(t, a, width, 30)

		p := planBar(a.tabInfos(), a.store.State().Active, a.trayModules(), width, a.icons)
		if _, _, ok := trayModuleAt(p, width, a.icons, modUsage); ok {
			t.Fatalf("the module is still on the bar at width %d", width)
		}

		r := check(t, width)
		if r.X != width-1-r.W {
			t.Errorf("width %d: x = %d, want %d", width, r.X, width-1-r.W)
		}
	})

	t.Run("narrow", func(t *testing.T) {
		r := check(t, 30)
		if r.X != 0 || r.W != 30 {
			t.Errorf("rect %+v, want x 0 and 30 wide", r)
		}
	})
}

func TestUsageToggle(t *testing.T) {
	t.Parallel()

	t.Run("no usage", func(t *testing.T) {
		t.Parallel()

		a := trayApp(t, true)
		a.clock = &fakeClock{t: t0}
		chord(a, 'u')

		if a.tray.usageOpen || a.notice != "no Claude usage yet" {
			t.Errorf("open = %v, notice = %q", a.tray.usageOpen, a.notice)
		}
	})

	t.Run("usage", func(t *testing.T) {
		t.Parallel()

		a, _ := usageApp(t, true)

		chord(a, 'u')

		if !a.usageShows() {
			t.Fatal("leader u did not open it")
		}

		chord(a, 'u')

		if a.usageShows() {
			t.Fatal("leader u again did not close it")
		}

		a.openCommandPalette()

		for _, it := range a.overlay.items {
			if it.ID == "usage.toggle" {
				if it.Title != "Claude usage" || it.Hint != "ctrl+space u" {
					t.Errorf("palette row = %+v", it)
				}

				return
			}
		}

		t.Error("the palette has no usage.toggle row")
	})
}

func TestUsagePopoverHoldsKeys(t *testing.T) {
	t.Parallel()

	a, _ := escApp(t)
	id := a.store.State().Tab().Focus
	seedUsage(t, a, usageFive, usageWeek)

	chord(a, 'u')
	press(a, keyRune('x'))
	a.Update(tea.PasteMsg{Content: "pasted"})

	if got := sentSince(t, a, id); got != "" {
		t.Errorf("the pane was sent %q while the popover showed", got)
	}

	if !a.usageShows() {
		t.Fatal("a key or paste closed it")
	}

	press(a, keyLeader, keyLeader)

	if got := sentSince(t, a, id); got != "" || !a.usageShows() {
		t.Fatalf("double leader: the pane was sent %q, popover shows = %v", got, a.usageShows())
	}

	chord(a, ':')

	if a.overlay == nil || !a.usageShows() {
		t.Fatalf("palette open = %v, popover shows = %v", a.overlay != nil, a.usageShows())
	}

	press(a, keyEsc)

	if a.overlay != nil || !a.usageShows() {
		t.Fatalf("after esc: palette open = %v, popover shows = %v", a.overlay != nil, a.usageShows())
	}

	press(a, keyEsc)

	if a.usageShows() {
		t.Error("esc did not close the popover")
	}
}

func TestUsagePopoverClosesWhenSpent(t *testing.T) {
	t.Parallel()

	a, clk := usageApp(t, true)
	chord(a, 'u')

	clk.Set(t0.Add(53 * time.Hour))
	a.Update(clockMsg{text: "05:00"})

	if a.tray.usageOpen || slices.Contains(modIDs(a.trayModules()), modUsage) {
		t.Fatalf("open = %v, modules = %v", a.tray.usageOpen, modIDs(a.trayModules()))
	}

	seedUsage(t, a, core.UsageWindow{Used: 5, ResetsAt: t0.Add(58 * time.Hour)}, core.UsageWindow{Used: 6, ResetsAt: t0.Add(200 * time.Hour)})

	if !slices.Contains(modIDs(a.trayModules()), modUsage) {
		t.Error("the module did not return with new data")
	}

	if a.usageShows() {
		t.Error("new data reopened the popover")
	}
}

// usageClickRig is a two-pane tab and a second tab, with the usage module on
// the bar. text goes to the first pane.
func usageClickRig(t *testing.T, width int, text string) *App {
	t.Helper()

	a, _ := framedApp(t, width, 30, shellSpec("/w/a", text), shellSpec("/w/b", ""))
	a.clock = &fakeClock{t: t0}

	openIn(t, a, core.NewTab{Kind: core.KindShell, Cwd: "/w/c"}, core.SelectTab{Index: 0})
	fixtureTray(a)
	seedUsage(t, a, usageFive, usageWeek)

	return a
}

func usageModuleCells(t *testing.T, a *App) (x, w int) {
	t.Helper()

	p := planBar(a.tabInfos(), a.store.State().Active, a.trayModules(), a.width, a.icons)

	x, w, ok := trayModuleAt(p, a.width, a.icons, modUsage)
	if !ok {
		t.Fatal("the bar does not draw the usage module")
	}

	return x, w
}

func TestUsageModuleHit(t *testing.T) {
	t.Parallel()

	a := usageClickRig(t, 120, "")
	ux, uw := usageModuleCells(t, a)

	for x := ux; x < ux+uw; x++ {
		if got := a.hit(x, 0).area; got != hitUsage {
			t.Errorf("hit(%d, 0) = %v, want hitUsage", x, got)
		}
	}

	for _, x := range []int{ux - 1, ux + uw} {
		if got := a.hit(x, 0).area; got == hitUsage {
			t.Errorf("hit(%d, 0) is hitUsage outside the module", x)
		}
	}

	t.Run("off the bar", func(t *testing.T) {
		sized(t, a, 20, 30)

		for x := range 20 {
			if got := a.hit(x, 0).area; got == hitUsage {
				t.Errorf("width 20: hit(%d, 0) is hitUsage", x)
			}
		}
	})

	t.Run("no usage", func(t *testing.T) {
		b, _ := framedApp(t, 120, 30, shellSpec("/w/a", ""))
		b.clock = &fakeClock{t: t0}
		fixtureTray(b)

		for x := range 120 {
			if got := b.hit(x, 0).area; got == hitUsage {
				t.Errorf("hit(%d, 0) is hitUsage with no usage", x)
			}
		}
	})
}

func TestUsageClick(t *testing.T) {
	t.Parallel()

	a := usageClickRig(t, 120, "")
	ux, _ := usageModuleCells(t, a)
	focus := focusOf(a)
	tab := a.store.State().Active

	state := func(t *testing.T, step string, open bool) {
		t.Helper()

		if got := a.usageShows(); got != open {
			t.Fatalf("%s: shows = %v, want %v", step, got, open)
		}

		if got := focusOf(a); got != focus {
			t.Fatalf("%s: focus = %q, want %q", step, got, focus)
		}

		if got := a.store.State().Active; got != tab {
			t.Fatalf("%s: active tab = %d, want %d", step, got, tab)
		}
	}

	leftClick(a, ux, 0)
	state(t, "module opens", true)

	_, r := usageBox(t, a)
	leftClick(a, r.X+r.W/2, r.Y+r.H/2)
	state(t, "inside the box", true)

	leftClick(a, ux, 0)
	state(t, "module closes", false)

	other := core.PaneID("p1")
	if focus == other {
		other = "p2"
	}

	c := contentRectOf(a, other)

	leftClick(a, ux, 0)
	leftClick(a, c.X+1, c.Y+1)
	state(t, "pane content", false)

	segX := -1

	for x := range a.width {
		if h := a.hit(x, 0); h.area == hitTab && h.tab != tab {
			segX = x

			break
		}
	}

	if segX < 0 {
		t.Fatal("no other tab segment on the bar")
	}

	leftClick(a, ux, 0)
	leftClick(a, segX, 0)
	state(t, "other tab", false)

	leftClick(a, ux, 0)
	a.Update(tea.MouseClickMsg{X: c.X + 1, Y: c.Y + 1, Button: tea.MouseRight})
	state(t, "right click", false)
}

func TestUsagePopoverBlocksTheMouse(t *testing.T) {
	t.Parallel()

	a := usageClickRig(t, 120, manyLines(60))
	a.focusFollows = true

	ux, _ := usageModuleCells(t, a)
	leftClick(a, ux, 0)

	if !a.usageShows() {
		t.Fatal("the popover is not open")
	}

	focus := focusOf(a)

	other := core.PaneID("p1")
	if focus == other {
		other = "p2"
	}

	x, y := centre(a, other)
	wheelAt(a, x, y, tea.MouseWheelUp)

	if got := a.panes[other].linesAbove(); got != 0 {
		t.Errorf("wheel scrolled the pane by %d", got)
	}

	hover(a, x, y)

	if got := focusOf(a); got != focus {
		t.Errorf("focus = %q after motion, want %q", got, focus)
	}
}

func TestUsageClickUnderAModal(t *testing.T) {
	t.Parallel()

	t.Run("palette over the popover", func(t *testing.T) {
		t.Parallel()

		a := usageClickRig(t, 120, "")
		ux, _ := usageModuleCells(t, a)
		leftClick(a, ux, 0)
		chord(a, ':')

		if !a.modalOpen() {
			t.Fatal("the palette did not open")
		}

		x, y := centre(a, "p1")
		leftClick(a, x, y)

		if !a.modalOpen() || !a.tray.usageOpen {
			t.Errorf("modal = %v, usageOpen = %v, want both still open", a.modalOpen(), a.tray.usageOpen)
		}
	})

	t.Run("confirm without the popover", func(t *testing.T) {
		t.Parallel()

		a := usageClickRig(t, 120, "")
		ux, _ := usageModuleCells(t, a)
		a.openConfirm(confirm{tab: a.store.State().Tab().ID, yes: func(*App) tea.Cmd { return nil }})
		leftClick(a, ux, 0)

		if a.tray.usageOpen {
			t.Error("a click on the module opened the popover under a confirm")
		}
	})
}

func TestUsagePopoverHidesTheCursor(t *testing.T) {
	t.Parallel()

	a, _ := framedApp(t, 100, 30, shellSpec("/w/a", "ab"))
	a.clock = &fakeClock{t: t0}
	seedUsage(t, a, usageFive, usageWeek)

	if a.View().Cursor == nil {
		t.Fatal("the pane has no cursor to hide")
	}

	chord(a, 'u')

	if cur := a.View().Cursor; cur != nil {
		t.Errorf("cursor = %+v under the popover, want nil", cur)
	}

	press(a, keyEsc)

	if a.View().Cursor == nil {
		t.Error("the cursor did not return after esc")
	}
}
