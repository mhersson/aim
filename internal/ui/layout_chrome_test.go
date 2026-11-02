package ui

import (
	"fmt"
	"slices"
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
	"github.com/charmbracelet/x/exp/golden"

	"github.com/mhersson/aim/internal/core"
)

// scrollingApp is five shell panes in scrolling columns at 70x12, focused on
// pane p<focus>. widen grows the focused column to the whole body.
func scrollingApp(t *testing.T, themeName string, icons bool, focus int, widen bool) *App {
	t.Helper()

	a := newThemedApp(t, themeName)
	a.icons = icons
	sized(t, a, 70, 12)

	for i := range 5 {
		openIn(t, a, core.OpenPane{Kind: core.KindShell, Cwd: fmt.Sprintf("/w/c%d", i+1), Split: core.SplitVertical})
	}

	openIn(t, a, core.SetLayoutMode{Mode: core.ModeScrolling, Area: a.bodyArea()}, core.FocusPane{ID: core.PaneID(fmt.Sprintf("p%d", focus))})

	if widen {
		for range 12 {
			openIn(t, a, core.ResizeDir{Dir: core.Right})
		}
	}

	attachPanes(t, a, nil)

	return a
}

// bottomRow is the plain bottom border of pane id.
func bottomRow(a *App, id core.PaneID) string {
	r := a.layoutRects()[id]
	lines := strings.Split(ansi.Strip(a.View().Content), "\n")

	return string([]rune(lines[r.Y+r.H-1])[r.X : r.X+r.W])
}

func TestHiddenColumnCounts(t *testing.T) {
	t.Parallel()
	a := scrollingApp(t, "catppuccin-mocha", true, 2, false)

	if got := bottomRow(a, "p2"); !strings.Contains(got, "╰─ ‹ 1 ─") {
		t.Errorf("left edge bottom border = %q, want the hidden-left count after the corner", got)
	}

	if got := bottomRow(a, "p3"); !strings.Contains(got, " 2 › ─╯") {
		t.Errorf("right edge bottom border = %q, want the hidden-right count before the corner", got)
	}

	// The counts go on the edge columns only, and a zero count shows nothing.
	if got := bottomRow(a, "p3"); strings.Contains(got, "‹") {
		t.Errorf("right edge shows a left count: %q", got)
	}

	openIn(t, a, core.FocusPane{ID: "p1"})

	if got := bottomRow(a, "p1"); strings.Contains(got, "‹") {
		t.Errorf("first column shows a left count: %q", got)
	}
}

func TestHiddenColumnCountColour(t *testing.T) {
	t.Parallel()
	a := scrollingApp(t, "catppuccin-mocha", true, 2, false)
	ui := mochaUI(t)
	r := a.layoutRects()["p2"]
	vt := lineVT(t, strings.Split(a.View().Content, "\n")[r.Y+r.H-1], a.width)

	assertColours(t, vt, r.X+3, 0, "‹", ui.Accent, ui.Bg, true)
}

func TestSearchPromptTakesTheCountsPlace(t *testing.T) {
	t.Parallel()
	a := scrollingApp(t, "catppuccin-mocha", true, 2, false)

	enterCopy(a)
	keys(a, "/")
	typeText(a, "ab")

	got := bottomRow(a, "p2")
	if strings.Contains(got, "‹") || !strings.Contains(got, "/ab") {
		t.Errorf("bottom border = %q, want the search prompt in place of the left count", got)
	}

	keys(a, "esc")

	if got := bottomRow(a, "p2"); !strings.Contains(got, " ‹ 1 ") {
		t.Errorf("bottom border after the prompt closed = %q, want the count back", got)
	}
}

func TestHiddenColumnCountsLeftOutWhenTheyDoNotFit(t *testing.T) {
	t.Parallel()
	a := scrollingApp(t, "catppuccin-mocha", true, 2, true)
	lk := a.current()

	for _, w := range []int{3, 8, 9, 14, 15, 16} {
		r := core.Rect{X: 0, Y: 2, W: w, H: 6}
		got := 0

		for _, s := range a.bottomBorder("p3", r, 2, 2, lk.styles) {
			if s.x < 1 || s.x+s.w > w-1 {
				t.Errorf("width %d: count at %d..%d leaves the border", w, s.x, s.x+s.w)
			}

			got++
		}

		// Each count is 5 cells between the corner dashes; two need a dash between.
		switch {
		case w >= 15 && got != 2, w < 9 && got != 0:
			t.Errorf("width %d: %d counts", w, got)
		}
	}
}

func TestZoomedScrollingTabShowsNoCounts(t *testing.T) {
	t.Parallel()
	a := scrollingApp(t, "catppuccin-mocha", true, 2, false)
	openIn(t, a, core.ToggleZoom{})

	if got := bottomRow(a, "p2"); strings.ContainsAny(got, "‹›") {
		t.Errorf("zoomed bottom border = %q, want no counts", got)
	}
}

// preApp is one focused shell pane at width w with a preselection set.
func preApp(t *testing.T, themeName string, icons bool, w int) *App {
	t.Helper()

	a := newThemedApp(t, themeName)
	a.icons = icons
	sized(t, a, w, 12)
	openIn(t, a, core.OpenPane{Kind: core.KindShell, Cwd: "/w/a", Split: core.SplitVertical})
	procs := attachPanes(t, a, nil)
	feedAndWait(t, a.panes["p1"], procs["p1"], strings.Repeat("line\r\n", 40))
	a.panes["p1"].render()

	return a
}

func TestPreselectTag(t *testing.T) {
	t.Parallel()

	tests := []struct {
		dir   core.Dir
		icons bool
		want  string
	}{
		{core.Left, true, " " + iconArrowLeft + " next "},
		{core.Down, true, " " + iconArrowDown + " next "},
		{core.Up, true, " " + iconArrowUp + " next "},
		{core.Right, true, " " + iconArrowRight + " next "},
		{core.Left, false, " ← next "},
		{core.Down, false, " ↓ next "},
		{core.Up, false, " ↑ next "},
		{core.Right, false, " → next "},
	}

	for _, tt := range tests {
		a := preApp(t, "catppuccin-mocha", tt.icons, 40)

		if strings.Contains(topRow(a), "next") {
			t.Fatalf("tag shown before any preselection: %q", topRow(a))
		}

		openIn(t, a, core.Preselect{Dir: tt.dir})

		if got := topRow(a); !strings.Contains(got, tt.want) {
			t.Errorf("dir %d icons %v: top border = %q, want %q", tt.dir, tt.icons, got, tt.want)
		}

		openIn(t, a, core.ClearPreselect{})

		if got := topRow(a); strings.Contains(got, "next") {
			t.Errorf("tag still shown after clearing: %q", got)
		}
	}
}

func TestPreselectTagColour(t *testing.T) {
	t.Parallel()
	a := preApp(t, "catppuccin-mocha", true, 40)
	openIn(t, a, core.Preselect{Dir: core.Right})

	ui := mochaUI(t)
	row := strings.Split(a.View().Content, "\n")[2]
	vt := lineVT(t, row, a.width)
	plain := ansi.Strip(row)

	before, _, ok := strings.Cut(plain, "next")
	if !ok {
		t.Fatalf("row has no next tag: %q", plain)
	}

	x := ansi.StringWidth(before)

	assertColours(t, vt, x, 0, "n", ui.Accent, ui.Bg, true)
}

func TestPreselectTagDroppedBeforeCopy(t *testing.T) {
	t.Parallel()

	for _, tt := range []struct {
		w          int
		copy, next bool // what the border must show
	}{
		{11, false, true},
		{11, true, false},
		{20, true, true},
	} {
		a := preApp(t, "catppuccin-mocha", false, tt.w)
		openIn(t, a, core.Preselect{Dir: core.Right})

		if tt.copy {
			enterCopy(a)
		}

		row := topRow(a)
		if got := strings.Contains(row, "COPY"); got != tt.copy {
			t.Errorf("width %d: COPY shown = %v in %q", tt.w, got, row)
		}

		if got := strings.Contains(row, "next"); got != (tt.next && (!tt.copy || tt.w >= 20)) {
			t.Errorf("width %d: preselect tag shown = %v in %q", tt.w, got, row)
		}
	}
}

func trayLayoutText(icons bool, m core.LayoutMode) string {
	if !icons {
		return " layout " + string(m) + " "
	}

	icon := map[core.LayoutMode]string{
		core.ModeBSP: iconLayoutBSP, core.ModeMaster: iconLayoutMaster, core.ModeScrolling: iconLayoutScrolling,
	}[m]

	return " " + icon + " " + string(m) + " "
}

func TestLayoutModuleShowsTheActiveTabsMode(t *testing.T) {
	t.Parallel()

	for _, m := range []core.LayoutMode{core.ModeBSP, core.ModeMaster, core.ModeScrolling} {
		for _, icons := range []bool{true, false} {
			a := trayApp(t, icons)
			openIn(t, a, core.SetLayoutMode{Mode: m, Area: a.bodyArea()})

			mod := trayModuleByID(a, modLayout)
			if got := ansi.Strip(mod.render()); got != trayLayoutText(icons, m) {
				t.Errorf("mode %s icons=%v: module = %q, want %q", m, icons, got, trayLayoutText(icons, m))
			}

			if mod.width != ansi.StringWidth(trayLayoutText(icons, m)) {
				t.Errorf("mode %s icons=%v: width %d", m, icons, mod.width)
			}
		}
	}

	// Another tab's mode does not show.
	a := trayApp(t, true)
	openIn(t, a, core.SetLayoutMode{Mode: core.ModeScrolling, Area: a.bodyArea()}, core.SelectTab{Index: 0})

	if got := ansi.Strip(trayModuleByID(a, modLayout).render()); got != trayLayoutText(true, core.ModeBSP) {
		t.Errorf("module on the other tab = %q", got)
	}
}

func TestLayoutModuleColours(t *testing.T) {
	t.Parallel()
	a := trayApp(t, true)
	ui := mochaUI(t)
	w := 100
	vt := lineVT(t, a.workspaceBar(w), w)

	mods := a.trayModules()
	x := w - trayWidth(mods, true) + 1

	assertColours(t, vt, x+1, 0, iconLayoutBSP, ui.Accent, ui.Surface, false)
	assertColours(t, vt, x+3, 0, "b", ui.Fg, ui.Surface, false)
}

// The layout module leaves before any tab name shrinks.
func TestLayoutModuleGivesWayAfterLoadAndBeforeTabNames(t *testing.T) {
	t.Parallel()

	for _, icons := range []bool{true, false} {
		a := trayApp(t, icons)
		tabs := a.tabInfos()
		active := a.store.State().Active
		gone := false

		for w := 130; w >= 1; w-- {
			p := planBar(tabs, active, a.trayModules(), w, icons)
			kept := modIDs(p.tray)

			if !slices.Contains(kept, modLayout) {
				gone = true

				if slices.Contains(kept, modLoad) {
					t.Fatalf("icons=%v width %d: load stayed after the layout left (kept %v)", icons, w, kept)
				}

				continue
			}

			for _, v := range p.tabs {
				if v.level != levelFull {
					t.Fatalf("icons=%v width %d: a tab name shrank while the layout module stayed", icons, w)
				}
			}
		}

		if !gone {
			t.Errorf("icons=%v: the layout module never left", icons)
		}
	}
}

func chromeThemes() []string { return []string{"catppuccin-mocha", "catppuccin-latte"} }

type layoutChromeCase struct {
	name  string
	build func(t *testing.T, th string, icons bool) *App
}

func layoutChromeCases() []layoutChromeCase {
	return []layoutChromeCase{
		{"scrolling-both-sides", func(t *testing.T, th string, icons bool) *App { return scrollingApp(t, th, icons, 2, false) }},
		{"scrolling-one-column", func(t *testing.T, th string, icons bool) *App { return scrollingApp(t, th, icons, 2, true) }},
		{"scrolling-right-only", func(t *testing.T, th string, icons bool) *App { return scrollingApp(t, th, icons, 1, false) }},
		{"scrolling-search-prompt", func(t *testing.T, th string, icons bool) *App {
			a := scrollingApp(t, th, icons, 2, false)
			enterCopy(a)
			keys(a, "/")
			typeText(a, "ab")

			return a
		}},
		{"preselect", func(t *testing.T, th string, icons bool) *App {
			a := preApp(t, th, icons, 70)
			openIn(t, a, core.Preselect{Dir: core.Right})

			return a
		}},
		{"preselect-all-tags", func(t *testing.T, th string, icons bool) *App {
			a := preApp(t, th, icons, 70)
			a.panes["p1"].scrollBy(3)
			openIn(t, a, core.Preselect{Dir: core.Down}, core.ToggleZoom{})
			enterCopy(a)

			return a
		}},
		{"preselect-tags-dropped", func(t *testing.T, th string, icons bool) *App {
			a := preApp(t, th, icons, 16)
			a.panes["p1"].scrollBy(3)
			openIn(t, a, core.Preselect{Dir: core.Left}, core.ToggleZoom{})
			enterCopy(a)

			return a
		}},
	}
}

func TestLayoutChromeGolden(t *testing.T) {
	t.Parallel()

	for _, c := range layoutChromeCases() {
		t.Run("catppuccin-mocha/icons=true/"+c.name, func(t *testing.T) {
			t.Parallel()
			golden.RequireEqual(t, []byte(c.build(t, "catppuccin-mocha", true).View().Content))
		})
	}
}

// TestLayoutChromeText is the same screens without styling and with icons off,
// so the layout reads; TestLayoutChromeGolden draws them with icons.
func TestLayoutChromeText(t *testing.T) {
	t.Parallel()

	for _, c := range layoutChromeCases() {
		t.Run("icons=false/"+c.name, func(t *testing.T) {
			t.Parallel()
			golden.RequireEqual(t, []byte(ansi.Strip(c.build(t, "catppuccin-mocha", false).View().Content)))
		})
	}
}

// layoutTrayCase is one tray width and layout mode; build returns the app
// whose workspaceBar(width) is the golden.
type layoutTrayCase struct {
	name  string
	width int
	build func(t *testing.T, th string, icons bool) *App
}

func layoutTrayCases() []layoutTrayCase {
	widths := []struct {
		name string
		w    int
		pr   *core.PRInfo
	}{
		{"wide", 110, nil},
		{"narrow", 58, nil},
		{"layout-gone", 42, nil},
		{"pr-wide", 110, new(prInfo("OPEN", checksRunning))},
		{"pr-failed", 110, new(prInfo("OPEN", checksFailing))},
		{"pr-no-load", 70, new(prInfo("OPEN", checksPassed))},
		{"pr-gone", 48, new(prInfo("OPEN", checksRunning))},
		{"pr-layout-gone", 36, new(prInfo("OPEN", checksRunning))},
	}

	var cases []layoutTrayCase

	for _, m := range []core.LayoutMode{core.ModeBSP, core.ModeMaster, core.ModeScrolling} {
		for _, wd := range widths {
			// The PR module does not depend on the layout mode, so one mode shows it.
			if wd.pr != nil && m != core.ModeBSP {
				continue
			}

			cases = append(cases, layoutTrayCase{
				name:  fmt.Sprintf("%s/%s", m, wd.name),
				width: wd.w,
				build: func(t *testing.T, th string, icons bool) *App {
					a := newThemedApp(t, th)
					a.icons = icons
					sized(t, a, 100, 30)
					openIn(t, a,
						core.OpenPane{Kind: core.KindAgent, Cwd: "/w/api", Split: core.SplitVertical},
						core.NewTab{Kind: core.KindShell, Cwd: "/w/web"},
						core.SetLayoutMode{Mode: m, Area: a.bodyArea()},
					)
					fixtureTray(a)

					if wd.pr != nil {
						seedPR(t, a, a.store.State().Tab().Focus, "/w/web", "feat/login", *wd.pr)
					}

					return a
				},
			})
		}
	}

	return cases
}

// Trays drawn in colour or text by a golden test, skipping the names another
// golden shows the same way (the narrowest of each same-screen class stays):
// TestBarGoldens draws the wide bsp trays, bsp/layout-gone stands for bsp/narrow
// and bsp/pr-gone, and bsp/pr-layout-gone for the other modes' layout-gone.
// layoutTrayCases keeps all of them for the theme colour check.
var (
	trayGoldenSkip = []string{
		"bsp/wide", "bsp/pr-wide", "bsp/pr-failed", "bsp/narrow", "bsp/pr-gone",
		"master-stack/layout-gone", "scrolling/layout-gone",
	}
	trayTextSkip = []string{
		"bsp/wide", "bsp/pr-wide", "bsp/pr-failed", "bsp/pr-no-load", "bsp/narrow", "bsp/layout-gone", "bsp/pr-gone",
		"master-stack/layout-gone", "scrolling/layout-gone",
	}
)

func TestLayoutTrayGolden(t *testing.T) {
	t.Parallel()

	for _, c := range layoutTrayCases() {
		if slices.Contains(trayGoldenSkip, c.name) {
			continue
		}

		t.Run("catppuccin-mocha/icons=true/"+c.name, func(t *testing.T) {
			t.Parallel()
			a := c.build(t, "catppuccin-mocha", true)
			golden.RequireEqual(t, []byte(a.workspaceBar(c.width)))
		})
	}
}

// TestLayoutTrayText is the same trays without styling and with icons off, so
// the layout reads.
func TestLayoutTrayText(t *testing.T) {
	t.Parallel()

	for _, c := range layoutTrayCases() {
		if slices.Contains(trayTextSkip, c.name) {
			continue
		}

		t.Run("icons=false/"+c.name, func(t *testing.T) {
			t.Parallel()
			a := c.build(t, "catppuccin-mocha", false)
			golden.RequireEqual(t, []byte(ansi.Strip(a.workspaceBar(c.width))))
		})
	}
}
