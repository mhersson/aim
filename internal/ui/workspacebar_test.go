package ui

import (
	"fmt"
	"io"
	"slices"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	uv "github.com/charmbracelet/ultraviolet"
	"github.com/charmbracelet/x/ansi"

	"github.com/mhersson/aim/internal/core"
	"github.com/mhersson/aim/internal/term"
	"github.com/mhersson/aim/internal/ui/theme"
)

// tabsApp has one tab per spec, in order, with the first tab's initial pane
// opened through the store. Names are custom when non-empty.
type tabSpec struct {
	kind core.PaneKind
	cwd  string
	name string
}

func tabsApp(t *testing.T, w, h int, specs ...tabSpec) *App {
	t.Helper()

	a := newTestApp(t)
	sized(t, a, w, h)

	for i, s := range specs {
		var act core.Action = core.NewTab{Kind: s.kind, Cwd: s.cwd}
		if i == 0 {
			act = core.OpenPane{Kind: s.kind, Cwd: s.cwd, Split: core.SplitVertical}
		}

		if err := a.dispatch(act); err != nil {
			t.Fatal(err)
		}

		if s.name != "" {
			if err := a.dispatch(core.RenameTab{ID: a.store.State().Tab().ID, Name: s.name}); err != nil {
				t.Fatal(err)
			}
		}
	}

	return a
}

func selectTab(t *testing.T, a *App, i int) {
	t.Helper()

	if err := a.dispatch(core.SelectTab{Index: i}); err != nil {
		t.Fatal(err)
	}
}

// lineVT replays one styled line into a virtual terminal of its own width.
func lineVT(t *testing.T, line string, w int) term.VT {
	t.Helper()

	vt := term.NewVT(w, 1, io.Discard)

	t.Cleanup(func() { _ = vt.Close() })

	if _, err := vt.Write([]byte(line)); err != nil {
		t.Fatal(err)
	}

	return vt
}

func hexOf(c interface{ RGBA() (r, g, b, a uint32) }) string {
	r, g, b, _ := c.RGBA()

	return fmt.Sprintf("#%02x%02x%02x", r>>8, g>>8, b>>8)
}

// assertColours checks a cell's content, foreground, background and weight.
func assertColours(t *testing.T, vt term.VT, x, y int, content string, fg, bg theme.Hex, bold bool) {
	t.Helper()

	c := vt.CellAt(x, y)
	if c == nil || c.Style.Fg == nil || c.Style.Bg == nil {
		t.Errorf("cell (%d, %d) lacks a colour: %+v", x, y, c)

		return
	}

	if c.Content != content {
		t.Errorf("cell (%d, %d) = %q, want %q", x, y, c.Content, content)
	}

	if got := hexOf(c.Style.Fg); got != string(fg) {
		t.Errorf("cell (%d, %d) %q foreground = %s, want %s", x, y, content, got, fg)
	}

	if got := hexOf(c.Style.Bg); got != string(bg) {
		t.Errorf("cell (%d, %d) %q background = %s, want %s", x, y, content, got, bg)
	}

	if isBold := c.Style.Attrs&uv.AttrBold != 0; isBold != bold {
		t.Errorf("cell (%d, %d) %q bold = %v, want %v", x, y, content, isBold, bold)
	}
}

func TestWorkspaceBar(t *testing.T) {
	t.Parallel()

	agent, shellIcon := iconAgent, iconShell
	layoutTray := trayOpen + " " + iconLayoutBSP + " bsp "

	tests := []struct {
		name  string
		specs []tabSpec
		sel   int
		icons bool
		want  string // the bar without styling, before the right padding
		tray  string // the tray without styling; the bar ends with it
	}{
		{
			"one tab",
			[]tabSpec{{core.KindShell, "/w/proj", ""}},
			0, true,
			" 1 " + shellIcon + " proj " + edgeSolid, layoutTray,
		},
		{
			"three tabs, active in the middle",
			[]tabSpec{{core.KindShell, "/w/a", "api"}, {core.KindAgent, "/w/web", ""}, {core.KindShell, "", ""}},
			1, true,
			" 1 " + shellIcon + " api " + edgeSolid + " 2 " + agent + " web " + edgeSolid + " 3 " + shellIcon + " shell " + edgeSolid,
			layoutTray,
		},
		{
			"inactive neighbours get a thin edge",
			[]tabSpec{{core.KindShell, "/w/a", "one"}, {core.KindShell, "/w/b", "two"}, {core.KindShell, "/w/c", "three"}},
			2, true,
			" 1 " + shellIcon + " one " + edgeThin + " 2 " + shellIcon + " two " + edgeSolid + " 3 " + shellIcon + " three " + edgeSolid, layoutTray,
		},
		{
			"icons off",
			[]tabSpec{{core.KindShell, "/w/a", "one"}, {core.KindAgent, "/w/b", "two"}},
			0, false,
			" 1 one   2 two ",
			" layout bsp ",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			a := tabsApp(t, 60, 12, tt.specs...)
			a.icons = tt.icons
			selectTab(t, a, tt.sel)

			bar := a.workspaceBar(60)
			plain := ansi.Strip(bar)

			if got := lipgloss.Width(bar); got != 60 {
				t.Errorf("width = %d, want 60", got)
			}

			pad := 60 - lipgloss.Width(tt.want) - lipgloss.Width(tt.tray)
			if want := tt.want + strings.Repeat(" ", pad) + tt.tray; plain != want {
				t.Errorf("bar = %q, want %q", plain, want)
			}
		})
	}
}

func TestWorkspaceBarColours(t *testing.T) {
	t.Parallel()
	ui := mochaUI(t)

	a := tabsApp(t, 80, 12,
		tabSpec{core.KindShell, "/w/a", "one"},
		tabSpec{core.KindShell, "/w/b", "two"},
		tabSpec{core.KindAgent, "/w/c", "three"},
	)
	selectTab(t, a, 1)

	vt := lineVT(t, a.workspaceBar(80), 80)

	// " 1 <shell> one " is 9 cells wide: tab 1 is inactive, 0..8, edge at 9.
	assertColours(t, vt, 1, 0, "1", ui.Muted, ui.Surface, true)
	assertColours(t, vt, 3, 0, iconShell, ui.Dim, ui.Surface, false)
	assertColours(t, vt, 5, 0, "o", ui.Dim, ui.Surface, false)
	// inactive -> active: the segment colour on the next segment's colour
	assertColours(t, vt, 9, 0, edgeSolid, ui.Surface, ui.Accent, false)
	// " 2 <shell> two " is the active tab: 10..18, edge at 19
	assertColours(t, vt, 10, 0, " ", ui.OnAccent, ui.Accent, true)
	assertColours(t, vt, 11, 0, "2", ui.OnAccent, ui.Accent, true)
	assertColours(t, vt, 13, 0, iconShell, ui.OnAccent, ui.Accent, true)
	// active -> inactive
	assertColours(t, vt, 19, 0, edgeSolid, ui.Accent, ui.Surface, false)
	// the last segment ends on the bar: " 3 <agent> three " is 20..30, edge at 31
	assertColours(t, vt, 21, 0, "3", ui.Muted, ui.Surface, true)
	assertColours(t, vt, 23, 0, iconAgent, ui.Dim, ui.Surface, false)
	assertColours(t, vt, 31, 0, edgeSolid, ui.Surface, ui.Bar, false)

	if c := vt.CellAt(50, 0); c == nil || c.Style.Bg == nil || hexOf(c.Style.Bg) != string(ui.Bar) {
		t.Errorf("padding cell = %+v, want background %s", c, ui.Bar)
	}

	// two inactive tabs side by side get the thin edge
	selectTab(t, a, 2)
	vt = lineVT(t, a.workspaceBar(80), 80)
	assertColours(t, vt, 9, 0, edgeThin, ui.Bar, ui.Surface, false)
}

// testModules are stand-ins for the tray modules, left to right.
func testModules() []trayModule {
	mk := func(id string, w int) trayModule {
		return trayModule{id: id, width: w, render: func() string { return strings.Repeat("x", w) }}
	}

	return []trayModule{mk(modPR, 8), mk(modLoad, 20), mk(modClock, 9)}
}

func planTabs() []tabInfo {
	return []tabInfo{
		{n: 1, name: "api", agent: false},
		{n: 2, name: "multiplex", agent: true},
		{n: 3, name: "documentation", agent: false},
	}
}

// signature names a plan: the tray modules left, then each visible tab as
// number and shrink level (0 whole, 1 without the name, 2 the number only).
func signature(p barPlan) string {
	var ids []string
	for _, m := range p.tray {
		ids = append(ids, m.id)
	}

	var tabs []string
	for _, tb := range p.tabs {
		tabs = append(tabs, fmt.Sprintf("%d:%d", tb.idx+1, tb.level))
	}

	return strings.Join(ids, ",") + " | " + strings.Join(tabs, " ")
}

// planEdges lists the widths in 1..maxW where the bar changes: each width where
// the plan (or whether it fits at all) differs from the one below, with that
// one below, plus 1 and maxW. A stage of planBar drops or returns there.
func planEdges(tabs []tabInfo, active int, mods []trayModule, icons bool, maxW int) []int {
	key := func(w int) string {
		p := planBar(tabs, active, mods, w, icons)

		need := p.tabsW
		if p.trayW > 0 {
			need += trayGap + p.trayW
		}

		return signature(p) + fmt.Sprint(need <= w)
	}

	edges := []int{1, maxW}

	for w := 2; w <= maxW; w++ {
		if key(w) != key(w-1) {
			edges = append(edges, w-1, w)
		}
	}

	slices.Sort(edges)

	return slices.Compact(edges)
}

// As the bar narrows it drops load and PR, shrinks inactive names longest
// first, drops the clock, shrinks the active name, then the farthest segments.
func TestWorkspaceBarPlan(t *testing.T) {
	t.Parallel()

	want := []string{
		"pr,load,clock | 1:0 2:0 3:0",
		"pr,clock | 1:0 2:0 3:0", // load leaves
		"clock | 1:0 2:0 3:0",    // pr leaves
		"clock | 1:0 2:0 3:1",    // the longest inactive name goes first
		"clock | 1:1 2:0 3:1",    //
		"clock | 1:1 2:0 3:2",    // then down to the number
		"clock | 1:2 2:0 3:2",    //
		" | 1:2 2:0 3:2",         // the clock leaves
		" | 1:2 2:1 3:2",         // the active name shrinks
		" | 1:2 2:2 3:2",         //
		" | 1:2 2:2",             // the farthest segment goes
		" | 2:2",                 //
	}

	var got []string

	for w := 200; w >= 1; w-- {
		sig := signature(planBar(planTabs(), 1, testModules(), w, true))
		if len(got) == 0 || got[len(got)-1] != sig {
			got = append(got, sig)
		}
	}

	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Errorf("stages =\n%s\nwant\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
}

// The omit stage never takes the active tab: every edge of the narrow bar, on
// both sides, still holds it.
func TestWorkspaceBarPlanActiveStays(t *testing.T) {
	t.Parallel()

	for _, icons := range []bool{true, false} {
		for active := range 3 {
			for _, w := range planEdges(planTabs(), active, testModules(), icons, 160) {
				p := planBar(planTabs(), active, testModules(), w, icons)

				found := false
				for _, tb := range p.tabs {
					found = found || tb.idx == active
				}

				if !found {
					t.Errorf("icons=%v active=%d width=%d: active tab omitted: %s", icons, active, w, signature(p))
				}
			}
		}
	}
}

// Whatever the plan, the bar is exactly the width.
func TestWorkspaceBarExactWidth(t *testing.T) {
	t.Parallel()
	ui := mochaUI(t)

	for _, icons := range []bool{true, false} {
		for active := range 3 {
			for _, w := range planEdges(planTabs(), active, testModules(), icons, 160) {
				bar := renderBar(planTabs(), active, testModules(), w, icons, ui)
				if got := lipgloss.Width(bar); got != w {
					t.Fatalf("icons=%v active=%d width=%d: bar is %d cells: %q", icons, active, w, got, ansi.Strip(bar))
				}
			}
		}
	}
}

func TestWorkspaceBarTraySpacing(t *testing.T) {
	t.Parallel()
	ui := mochaUI(t)
	mods := testModules()
	tray := trayWidth(mods, true)

	// 3 tabs whole: 3 segments plus 3 edges, then a gap of at least 2.
	tabs := planTabs()
	full := planBar(tabs, 1, mods, 200, true).tabsW

	w := full + 2 + tray
	p := planBar(tabs, 1, mods, w, true)

	if len(p.tray) != len(mods) {
		t.Errorf("tray at the tight width = %s, want every module", signature(p))
	}

	if p := planBar(tabs, 1, mods, w-1, true); len(p.tray) == len(mods) {
		t.Errorf("tray kept all modules one cell under the tight width: %s", signature(p))
	}

	line := ansi.Strip(renderBar(tabs, 1, mods, w, true, ui))
	if !strings.Contains(line, "  "+trayOpen) {
		t.Errorf("fewer than two cells between tabs and tray: %q", line)
	}

	if !strings.HasSuffix(line, strings.Repeat("x", 9)) {
		t.Errorf("tray is not flush right: %q", line)
	}
}

func TestTrayWidth(t *testing.T) {
	t.Parallel()

	mk := func(ids ...string) []trayModule {
		var out []trayModule

		for _, m := range testModules() {
			for _, id := range ids {
				if m.id == id {
					out = append(out, m)
				}
			}
		}

		return out
	}

	tests := []struct {
		name  string
		mods  []trayModule
		icons bool
		want  int
	}{
		{"none", nil, true, 0},
		{"clock alone", mk(modClock), true, 1 + 9},
		{"one module", mk(modLoad), true, 1 + 20},
		{"module and clock", mk(modLoad, modClock), true, 1 + 20 + 1 + 9},
		{"three", mk(modPR, modLoad, modClock), true, 1 + 8 + 1 + 20 + 1 + 9},
		{"icons off, separated by one cell", mk(modPR, modLoad, modClock), false, 8 + 1 + 20 + 1 + 9},
	}

	for _, tt := range tests {
		if got := trayWidth(tt.mods, tt.icons); got != tt.want {
			t.Errorf("%s: trayWidth = %d, want %d", tt.name, got, tt.want)
		}
	}
}

// attachTabPanes gives every pane of every tab a fake process sized to its
// content rect in its own tab's layout.
func attachTabPanes(t *testing.T, a *App) map[core.PaneID]*fakeProc {
	t.Helper()

	st := a.store.State()
	procs := map[core.PaneID]*fakeProc{}

	for id := range st.Panes {
		i, ok := st.TabOf(id)
		if !ok {
			t.Fatalf("pane %s is in no tab", id)
		}

		c := contentRect(a.tabRects(i)[id])
		fp := newFakeProc()
		p := newPane(id, fp, max(c.W, 1), max(c.H, 1), realClock{})

		t.Cleanup(func() { _ = p.close() })

		a.panes[id] = p
		procs[id] = fp
	}

	return procs
}

func openIn(t *testing.T, a *App, acts ...core.Action) {
	t.Helper()

	for _, act := range acts {
		if err := a.dispatch(act); err != nil {
			t.Fatal(err)
		}
	}
}

// A window resize refits every tab to its own layout, hidden tabs included,
// and never to 1x1.
func TestHiddenTabsKeepSize(t *testing.T) {
	t.Parallel()
	a := newTestApp(t)
	sized(t, a, 100, 30)

	openIn(t, a,
		core.OpenPane{Kind: core.KindShell, Cwd: "/w/a", Split: core.SplitVertical},
		core.OpenPane{Kind: core.KindShell, Cwd: "/w/b", Split: core.SplitHorizontal},
		core.NewTab{Kind: core.KindShell, Cwd: "/w/c"},
		core.NewTab{Kind: core.KindAgent, Cwd: "/w/d"},
		core.ToggleZoom{},
		core.SelectTab{Index: 1},
	)

	procs := attachTabPanes(t, a)

	a.Update(tea.WindowSizeMsg{Width: 80, Height: 24})

	st := a.store.State()
	if len(st.Panes) != 4 {
		t.Fatalf("panes = %d, want 4", len(st.Panes))
	}

	for id := range st.Panes {
		i, _ := st.TabOf(id)
		r, ok := a.tabRects(i)[id]

		if !ok {
			t.Fatalf("pane %s has no rect in tab %d", id, i)
		}

		c := contentRect(r)
		want := [2]int{c.W, c.H}

		if want[0] <= 1 || want[1] <= 1 {
			t.Fatalf("pane %s rect %+v is degenerate", id, r)
		}

		got := procs[id].resized()
		if len(got) == 0 || got[len(got)-1] != want {
			t.Errorf("pane %s (tab %d) resizes = %v, want the last to be %v", id, i+1, got, want)
		}

		if cols, rows := a.panes[id].size(); [2]int{cols, rows} != want {
			t.Errorf("pane %s size = %dx%d, want %v", id, cols, rows, want)
		}
	}

	// the zoomed tab 3 fills the body (80x24 -> 80x22, content 78x20)
	if got := procs["p4"].resized(); len(got) == 0 || got[len(got)-1] != [2]int{78, 20} {
		t.Errorf("zoomed pane resizes = %v, want 78x20 last", got)
	}
}

// Output that arrives while a tab is hidden does not render; showing the tab
// does, before the next View.
func TestHiddenPaneRendersOnSwitch(t *testing.T) {
	t.Parallel()
	a := newTestApp(t)
	sized(t, a, 60, 12)

	openIn(t, a,
		core.OpenPane{Kind: core.KindShell, Cwd: "/w/a", Split: core.SplitVertical},
		core.NewTab{Kind: core.KindShell, Cwd: "/w/b"},
	)

	procs := attachTabPanes(t, a)
	p1 := a.panes["p1"]

	feedAndWait(t, p1, procs["p1"], "hidden text")
	a.Update(paneDirtyMsg{id: "p1", p: a.panes["p1"]})

	if rows := strings.Join(p1.lines(), ""); strings.Contains(rows, "hidden text") {
		t.Errorf("hidden pane rendered its rows: %q", rows)
	}

	if !p1.stale {
		t.Error("hidden pane with output is not stale")
	}

	openIn(t, a, core.SelectTab{Index: 0})

	if rows := strings.Join(p1.lines(), ""); !strings.Contains(rows, "hidden text") {
		t.Errorf("switching tabs did not render the stale pane: %q", rows)
	}

	if p1.stale {
		t.Error("pane is still stale after its tab was shown")
	}

	if !strings.Contains(ansi.Strip(a.View().Content), "hidden text") {
		t.Error("view lacks the text of the shown tab")
	}
}

// Resizing while hidden also leaves the pane stale until its tab is shown.
func TestHiddenResizeRendersOnSwitch(t *testing.T) {
	t.Parallel()
	a := newTestApp(t)
	sized(t, a, 60, 12)

	openIn(t, a,
		core.OpenPane{Kind: core.KindShell, Cwd: "/w/a", Split: core.SplitVertical},
		core.NewTab{Kind: core.KindShell, Cwd: "/w/b"},
	)

	attachTabPanes(t, a)
	a.Update(tea.WindowSizeMsg{Width: 70, Height: 14})

	p1 := a.panes["p1"]
	if !p1.stale {
		t.Fatal("resized hidden pane is not stale")
	}

	openIn(t, a, core.SelectTab{Index: 0})

	if p1.stale {
		t.Error("pane is still stale after its tab was shown")
	}
}

// Height 1 is the workspace bar only, height 2 adds the rule; every line is the
// width and a body under 3 rows or columns stays blank.
func TestScreenRows(t *testing.T) {
	t.Parallel()

	specs := []tabSpec{
		{core.KindShell, "/w/a", "one"},
		{core.KindAgent, "/w/b", "two"},
		{core.KindShell, "/w/c", "three"},
	}

	for _, sz := range [][2]int{{1, 1}, {2, 2}, {100, 2}, {20, 5}, {20, 6}, {40, 1}, {40, 3}, {40, 4}, {2, 12}, {3, 12}, {3, 3}} {
		a := tabsApp(t, sz[0], sz[1], specs...)
		attachTabPanes(t, a)

		lines := strings.Split(a.View().Content, "\n")
		if len(lines) != sz[1] {
			t.Fatalf("%v: %d lines, want %d", sz, len(lines), sz[1])
		}

		for i, l := range lines {
			if got := lipgloss.Width(l); got != sz[0] {
				t.Errorf("%v line %d: width %d, want %d: %q", sz, i, got, sz[0], ansi.Strip(l))
			}
		}

		plain := strings.Split(ansi.Strip(a.View().Content), "\n")

		if sz[0] >= 14 && !strings.Contains(plain[0], " 1 ") {
			t.Errorf("%v: row 0 is not the workspace bar: %q", sz, plain[0])
		}

		if sz[1] >= 2 && sz[0] >= 14 && plain[1] != strings.Repeat("─", sz[0]) {
			t.Errorf("%v: row 1 is not the rule: %q", sz, plain[1])
		}

		if sz[0] < minFrame || sz[1]-2 < minFrame {
			for _, l := range plain[min(2, len(plain)):] {
				if strings.TrimSpace(l) != "" {
					t.Errorf("%v: body row %q, want blank", sz, l)
				}
			}
		}
	}
}

// Three tabs with panes keep every line the width at each width where the bar's
// plan changes, for each active tab. Height 10 shows a body; TestScreenRows
// covers the heights below.
func TestFrameSizesTabs(t *testing.T) {
	t.Parallel()

	specs := []tabSpec{
		{core.KindShell, "/w/a", "one"},
		{core.KindAgent, "/w/b", "a very long tab name indeed"},
		{core.KindShell, "/w/c", "日本語のタブ"},
	}

	const h = 10

	a := tabsApp(t, 60, h, specs...)
	attachTabPanes(t, a)

	for active := range 3 {
		selectTab(t, a, active)

		for _, w := range planEdges(a.tabInfos(), active, a.trayModules(), a.icons, 60) {
			sized(t, a, w, h)

			lines := strings.Split(a.View().Content, "\n")
			if len(lines) != h {
				t.Fatalf("%dx%d tab %d: %d lines, want %d", w, h, active, len(lines), h)
			}

			for i, l := range lines {
				if got := lipgloss.Width(l); got != w {
					t.Errorf("%dx%d tab %d line %d: width %d: %q", w, h, active, i, got, ansi.Strip(l))
				}
			}
		}
	}
}

// The workspace bar paints every cell of row 0, the last column included: the
// ground is Bar and only the segments, the tray and the clock take other
// backgrounds. The real tray runs at each width; a narrow bar ends on the
// tray's Surface, a wider one on the clock's own segment.
func TestBarGroundReachesLastColumn(t *testing.T) {
	t.Parallel()
	ui := mochaUI(t)

	tests := []struct {
		w    int
		last theme.Hex
	}{
		{20, ui.Bar},
		{40, ui.Clock},
		{160, ui.Clock},
	}

	for _, icons := range []bool{true, false} {
		for _, tt := range tests {
			a := tabsApp(t, tt.w, 12,
				tabSpec{core.KindShell, "/w/a", "one"},
				tabSpec{core.KindAgent, "/w/b", "two"},
			)
			a.icons = icons
			fixtureTray(a)

			buf := uv.NewScreenBuffer(tt.w, a.height)
			uv.NewStyledString(a.View().Content).Draw(buf, buf.Bounds())

			for x := range tt.w {
				if c := buf.CellAt(x, 0); c == nil || c.Style.Bg == nil {
					t.Errorf("icons=%v width %d: bar cell %d has no background", icons, tt.w, x)
				}
			}

			// The plain clock is narrow enough to stay at 20 columns; the icon one is not.
			last := tt.last
			if tt.w == 20 && !icons {
				last = ui.Clock
			}

			c := buf.CellAt(tt.w-1, 0)
			if c == nil || c.Style.Bg == nil || hexOf(c.Style.Bg) != string(last) {
				t.Errorf("icons=%v width %d: last column = %+v, want background %s", icons, tt.w, c, last)
			}
		}
	}

	// A bar without the tray ends on the Bar ground.
	ground := func(line string, w int) {
		t.Helper()

		vt := lineVT(t, line, w)
		for x := range w {
			if c := vt.CellAt(x, 0); c == nil || c.Style.Bg == nil {
				t.Errorf("cell %d has no background", x)
			}
		}

		if c := vt.CellAt(w-1, 0); c == nil || c.Style.Bg == nil || hexOf(c.Style.Bg) != string(ui.Bar) {
			t.Errorf("last cell = %+v, want background %s", c, ui.Bar)
		}
	}

	ground(renderBar(planTabs(), 0, nil, 100, true, ui), 100)
	ground(renderBar(planTabs(), 0, nil, 100, false, ui), 100)
}

func TestPaintRunMatchesLipgloss(t *testing.T) {
	t.Parallel()

	ui := mochaUI(t)
	colors := []theme.Hex{ui.Bg, ui.Fg, "", "#zz0000"}

	texts := []string{
		"", " ", "abc", "\u4e16\u754c", "e\u0301", "\U0001F600", "\u00a0",
		"\x1b[1mx\x1b[m", "\u200b", endingTag, "\t", "a\tb", "x\r", "a\rb", "a\r\nb", "a\nb",
	}

	for _, fg := range colors {
		for _, bg := range colors {
			for _, bold := range []bool{false, true} {
				for _, text := range texts {
					if got, want := paintRun(fg, bg, bold, text), paint(fg, bg, bold).Render(text); got != want {
						t.Fatalf("paintRun(%q, %q, %v, %q) = %q, want %q", fg, bg, bold, text, got, want)
					}
				}
			}
		}
	}
}
