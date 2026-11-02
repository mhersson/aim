package ui

import (
	"context"
	"errors"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/charmbracelet/x/exp/golden"

	"github.com/mhersson/aim/internal/core"
	"github.com/mhersson/aim/internal/ui/theme"
)

func TestUntilNextMinute(t *testing.T) {
	t.Parallel()

	base := time.Date(2026, 10, 26, 12, 0, 0, 0, time.UTC)
	tests := []struct {
		name string
		now  time.Time
		want time.Duration
	}{
		{"on the boundary", base, time.Minute},
		{"mid minute", base.Add(30*time.Second + 250*time.Millisecond), 29*time.Second + 750*time.Millisecond},
		{"just before", base.Add(59*time.Second + 999*time.Millisecond), time.Millisecond},
		{"just after", base.Add(time.Nanosecond), time.Minute - time.Nanosecond},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := untilNextMinute(tt.now); got != tt.want {
				t.Errorf("untilNextMinute(%v) = %v, want %v", tt.now, got, tt.want)
			}
		})
	}
}

// The clock text changes at minute boundaries only, and a message that repeats
// it changes nothing.
func TestClockTextChangesAtMinuteBoundaries(t *testing.T) {
	t.Parallel()
	a := newTestApp(t)
	clk := &fakeClock{t: time.Date(2026, 10, 26, 14, 32, 5, 0, time.UTC)}
	a.clock = clk

	step := func() string {
		msg := a.clockCmd(false)()

		_, cmd := a.Update(msg)
		if cmd == nil {
			t.Fatal("clockMsg did not re-arm the clock")
		}

		return a.tray.clock
	}

	if got := step(); got != "14:32" {
		t.Fatalf("clock = %q, want 14:32", got)
	}

	clk.Advance(54 * time.Second)

	if got := step(); got != "14:32" {
		t.Errorf("clock at 14:32:59 = %q, want 14:32", got)
	}

	clk.Advance(time.Second)

	if got := step(); got != "14:33" {
		t.Errorf("clock at 14:33:00 = %q, want 14:33", got)
	}
}

func modIDs(mods []trayModule) []string {
	var ids []string
	for _, m := range mods {
		ids = append(ids, m.id)
	}

	return ids
}

func TestTrayHasNoAgentsModule(t *testing.T) {
	t.Parallel()
	a := trayApp(t, true)
	openIn(t, a, core.OpenPane{Kind: core.KindAgent, Cwd: "/w/b", Split: core.SplitVertical})

	for _, id := range modIDs(a.trayModules()) {
		if id == "agents" {
			t.Fatal("the tray carries an agents module")
		}
	}
}

func TestSparkline(t *testing.T) {
	t.Parallel()

	const blank = "\u2800"

	tests := []struct {
		name string
		in   []int
		want string
	}{
		{"nothing sampled yet", nil, strings.Repeat(blank, 5)},
		{"idle fills one dot each", make([]int, 10), strings.Repeat("\u28C0", 5)},
		{"quarters", []int{1, 25, 26, 50, 51, 75, 76, 100, 100, 100}, "\u28C0\u28E4\u28F6\u28FF\u28FF"},
		{"right column", []int{0, 1, 0, 26, 0, 51, 0, 100, 0, 0}, "\u28C0\u28E0\u28F0\u28F8\u28C0"},
		{"newest sample is last", []int{100, 0}, strings.Repeat(blank, 4) + "\u28C7"},
		{"only the last ten", []int{100, 100, 100, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0}, strings.Repeat("\u28C0", 5)},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := sparkline(tt.in); got != tt.want {
				t.Errorf("sparkline(%v) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}

func TestFormatMem(t *testing.T) {
	t.Parallel()

	const gib = 1 << 30

	tests := []struct {
		in   uint64
		want string
	}{
		{0, "0M"},
		{512 << 20, "512M"},
		{gib, "1.0G"},
		{gib * 98 / 10, "9.8G"},
		{15*gib + gib/2, "15.5G"},
		{128 * gib, "128G"},
	}

	for _, tt := range tests {
		if got := formatMem(tt.in); got != tt.want {
			t.Errorf("formatMem(%d) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

// fixtureTray fills every sampled module with fixed values.
func fixtureTray(a *App) {
	a.tray.clock = "14:32"
	a.tray.load = &loadInfo{CPU: []int{5, 12, 30, 55, 80, 12, 3, 12, 40, 12}, Mem: "9.8G"}
}

func trayApp(t *testing.T, icons bool) *App {
	t.Helper()

	a := newTestApp(t)
	a.icons = icons
	sized(t, a, 100, 30)
	openIn(t, a,
		core.OpenPane{Kind: core.KindAgent, Cwd: "/w/api", Split: core.SplitVertical},
		core.NewTab{Kind: core.KindShell, Cwd: "/w/web"},
	)
	fixtureTray(a)

	return a
}

// prTrayApp is trayApp with a pull request on the branch of the focused shell pane.
func prTrayApp(t *testing.T, icons bool, pr core.PRInfo) *App {
	t.Helper()

	a := trayApp(t, icons)
	seedPR(t, a, a.store.State().Tab().Focus, "/w/web", "feat/login", pr)

	return a
}

// findPRModule is the tray's PR module, if it shows.
func findPRModule(a *App) (trayModule, bool) {
	for _, m := range a.trayModules() {
		if m.id == modPR {
			return m, true
		}
	}

	return trayModule{}, false
}

func TestTrayModuleWidths(t *testing.T) {
	t.Parallel()

	for _, icons := range []bool{true, false} {
		a := trayApp(t, icons)
		mods := a.trayModules()

		if want := []string{modLayout, modLoad, modClock}; !slices.Equal(modIDs(mods), want) {
			t.Fatalf("icons=%v modules = %v, want %v", icons, modIDs(mods), want)
		}

		for _, m := range mods {
			if got := lipgloss.Width(m.render()); got != m.width {
				t.Errorf("icons=%v %s renders %d cells, width says %d", icons, m.id, got, m.width)
			}
		}
	}
}

func TestTrayModuleWidthsWithAPR(t *testing.T) {
	t.Parallel()

	for _, icons := range []bool{true, false} {
		for _, pr := range []core.PRInfo{prInfo("OPEN", checksRunning), prInfo("OPEN", checksPassed), prInfo("OPEN", checksFailing), prInfo("OPEN")} {
			a := prTrayApp(t, icons, pr)
			mods := a.trayModules()

			if want := []string{modLayout, modPR, modLoad, modClock}; !slices.Equal(modIDs(mods), want) {
				t.Fatalf("icons=%v modules = %v, want %v", icons, modIDs(mods), want)
			}

			for _, m := range mods {
				if got := lipgloss.Width(m.render()); got != m.width {
					t.Errorf("icons=%v %s renders %d cells, width says %d", icons, m.id, got, m.width)
				}
			}
		}
	}
}

func TestTrayPRModule(t *testing.T) {
	t.Parallel()
	agentApp := func(t *testing.T, icons bool, pr *core.PRInfo) *App {
		t.Helper()

		a := newTestApp(t)
		a.icons = icons
		sized(t, a, 100, 30)
		openIn(t, a, core.OpenPane{Kind: core.KindAgent, Cwd: "/w/api", Split: core.SplitVertical})
		fixtureTray(a)

		if pr != nil {
			seedPR(t, a, "p1", "/w/api", "feat/login", *pr)
		} else {
			seedBranch(a, "p1", "/w/api", "feat/login")
		}

		return a
	}
	shellApp := func(t *testing.T, icons bool, pr *core.PRInfo) *App {
		t.Helper()

		a := trayApp(t, icons)
		id := a.store.State().Tab().Focus

		if pr != nil {
			seedPR(t, a, id, "/w/web", "feat/login", *pr)
		} else {
			seedBranch(a, id, "/w/web", "feat/login")
		}

		return a
	}

	tests := []struct {
		name  string
		icons bool
		build func(t *testing.T, icons bool, pr *core.PRInfo) *App
		pr    *core.PRInfo
		tune  func(a *App)
		want  string // the module's text; empty when it is hidden
	}{
		{"the focused agent's PR with checks running", true, agentApp, new(prInfo("OPEN", checksRunning)), nil, " " + iconPR + " #24 " + iconRunning + " 3/5 "},
		{"checks passed", true, agentApp, new(prInfo("OPEN", checksPassed)), nil, " " + iconPR + " #24 " + iconPassed + " 5/5 "},
		{"checks failed, others still running", true, agentApp, new(prInfo("OPEN", checksFailing)), nil, " " + iconPR + " #24 " + iconFailed + " 2 failed "},
		{"no checks", true, agentApp, new(prInfo("OPEN")), nil, " " + iconPR + " #24 "},
		{"failed with every check done", true, agentApp, new(prInfo("OPEN", checksDoneFailing)), nil, " " + iconPR + " #24 " + iconFailed + " 2 failed "},
		{"failed with no check counted", true, agentApp, new(prInfo("OPEN", checksFailedUncounted)), nil, " " + iconPR + " #24 " + iconFailed + " 1 failed "},
		{"the review decision stays on the card", true, agentApp, new(prInfo("OPEN", checksRunning, prReview("APPROVED"))), nil, " " + iconPR + " #24 " + iconRunning + " 3/5 "},
		{"a focused shell pane on a branch with a PR", true, shellApp, new(prInfo("OPEN", checksRunning)), nil, " " + iconPR + " #24 " + iconRunning + " 3/5 "},
		{"icons off", false, agentApp, new(prInfo("OPEN", checksRunning)), nil, " #24 ci 3/5 "},
		{"icons off, passed", false, shellApp, new(prInfo("OPEN", checksPassed)), nil, " #24 ci 5/5 "},
		{"icons off, failed", false, agentApp, new(prInfo("OPEN", checksFailing)), nil, " #24 ci 2 failed "},
		{"icons off, no checks", false, agentApp, new(prInfo("OPEN")), nil, " #24 "},

		{"no PR hides the module", true, agentApp, nil, nil, ""},
		{"no PR hides the module for a shell pane", true, shellApp, nil, nil, ""},
		{"a draft hides it", true, agentApp, new(prInfo("OPEN", prDraft, checksRunning)), nil, ""},
		{"a merged PR hides it", true, agentApp, new(prInfo("MERGED")), nil, ""},
		{"a closed PR hides it", true, shellApp, new(prInfo("CLOSED")), nil, ""},
		{"a repository not on GitHub hides it", true, agentApp, new(prInfo("OPEN")), func(a *App) { a.prs.panes["p1"].ok = false }, ""},
		{"a repository resolved for another branch hides it", true, agentApp, new(prInfo("OPEN")), func(a *App) { a.prs.panes["p1"].answered.branch = "other" }, ""},
		{"a repository resolved for another directory hides it", true, agentApp, new(prInfo("OPEN")), func(a *App) { a.prs.panes["p1"].answered.cwd = "/w/elsewhere" }, ""},
		{"a failed resolution hides it", true, agentApp, new(prInfo("OPEN")), func(a *App) {
			pp := a.prs.panes["p1"]
			pp.failed, pp.answered.branch = true, "feat/old"
		}, ""},
		{"a detached head hides it", true, agentApp, new(prInfo("OPEN")), func(a *App) { a.railEntry("p1").probe.detached = true }, ""},
		{"a pane the head probe has not answered for hides it", true, agentApp, new(prInfo("OPEN")), func(a *App) { a.railEntry("p1").probe.known = false }, ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			a := tt.build(t, tt.icons, tt.pr)
			if tt.tune != nil {
				tt.tune(a)
			}

			m, ok := findPRModule(a)
			if !ok {
				if tt.want != "" {
					t.Fatalf("no PR module, want %q (modules %v)", tt.want, modIDs(a.trayModules()))
				}

				return
			}

			if tt.want == "" {
				t.Fatalf("PR module %q shows, want it hidden", ansi.Strip(m.render()))
			}

			if got := ansi.Strip(m.render()); got != tt.want {
				t.Errorf("module = %q, want %q", got, tt.want)
			}

			if got := lipgloss.Width(m.render()); got != m.width {
				t.Errorf("module renders %d cells, width says %d", got, m.width)
			}
		})
	}
}

func TestTrayPRModuleSitsBetweenLayoutAndLoad(t *testing.T) {
	t.Parallel()
	a := prTrayApp(t, true, prInfo("OPEN", checksRunning))

	if want := []string{modLayout, modPR, modLoad, modClock}; !slices.Equal(modIDs(a.trayModules()), want) {
		t.Errorf("modules = %v, want %v", modIDs(a.trayModules()), want)
	}
}

func TestTrayPRModuleFollowsTheFocus(t *testing.T) {
	t.Parallel()
	a := trayApp(t, true)
	seedPR(t, a, "p1", "/w/api", "feat/login", prInfo("OPEN", checksPassed))

	// The shell tab has the focus, and its pane is on no branch of a PR.
	if _, ok := findPRModule(a); ok {
		t.Fatal("the PR of an agent in another tab shows")
	}

	openIn(t, a, core.SelectTab{Index: 0})

	m, ok := findPRModule(a)
	if !ok {
		t.Fatal("the focused agent's PR does not show")
	}

	if got, want := ansi.Strip(m.render()), " "+iconPR+" #24 "+iconPassed+" 5/5 "; got != want {
		t.Errorf("module = %q, want %q", got, want)
	}
}

func TestTrayShowsFollowedPR(t *testing.T) {
	p := newPRApp(t)
	agent, dirB := p.followRepos(t)

	p.churn(t, p.step(snapshotOfRepo(repoAt("a"), openPRIn(3, "feat-a"))))
	p.churn(t, p.step(snapshotOfRepo(repoAt("b"), openPRIn(7, "feat-b"))))

	text := func() string {
		m, ok := findPRModule(p.App)
		if !ok {
			return ""
		}

		return ansi.Strip(m.render())
	}

	if got := text(); !strings.Contains(got, "#3") {
		t.Fatalf("module = %q before the edit, want the PR of the agent's own branch, #3", got)
	}

	p.editRepo(t, agent, dirB)

	if got := text(); !strings.Contains(got, "#7") || strings.Contains(got, "#3") {
		t.Errorf("module = %q after the agent edited in the other repository, want the PR of feat-b, #7", got)
	}
}

func TestTrayPRModuleColours(t *testing.T) {
	t.Parallel()
	ui := mochaUI(t)

	tests := []struct {
		name string
		pr   core.PRInfo
		fg   theme.Hex
		bold bool
	}{
		{"running", prInfo("OPEN", checksRunning), ui.Warning, false},
		{"passed", prInfo("OPEN", checksPassed), ui.Success, false},
		{"failed", prInfo("OPEN", checksFailing), ui.Error, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m, ok := findPRModule(prTrayApp(t, true, tt.pr))
			if !ok {
				t.Fatal("no PR module")
			}

			vt := lineVT(t, m.render(), m.width)

			assertColours(t, vt, 1, 0, iconPR, ui.Info, ui.Surface, false)
			assertColours(t, vt, 3, 0, "#", ui.Fg, ui.Surface, true)
			assertColours(t, vt, 4, 0, "2", ui.Fg, ui.Surface, true)
			assertColours(t, vt, 7, 0, map[string]string{"running": iconRunning, "passed": iconPassed, "failed": iconFailed}[tt.name], tt.fg, ui.Surface, tt.bold)
			assertColours(t, vt, 9, 0, map[string]string{"running": "3", "passed": "5", "failed": "2"}[tt.name], tt.fg, ui.Surface, tt.bold)
		})
	}
}

func TestTrayOmitsAbsentModules(t *testing.T) {
	t.Parallel()
	a := newTestApp(t)
	sized(t, a, 100, 30)

	if got := a.trayModules(); len(got) != 0 {
		t.Errorf("modules before any sample = %v, want none", modIDs(got))
	}

	a.tray.clock = "09:05"

	if got := modIDs(a.trayModules()); !slices.Equal(got, []string{modClock}) {
		t.Errorf("modules = %v, want only the clock", got)
	}
}

func TestBarLinesStayExactWidth(t *testing.T) {
	t.Parallel()

	for _, icons := range []bool{true, false} {
		for _, a := range []*App{trayApp(t, icons), prTrayApp(t, icons, prInfo("OPEN", checksFailing))} {
			for w := 1; w <= 130; w++ {
				if got := lipgloss.Width(a.workspaceBar(w)); got != w {
					t.Fatalf("icons=%v width %d: bar is %d cells", icons, w, got)
				}
			}
		}
	}
}

// As the bar narrows the tray drops load, then usage, then PR, then the layout
// mode, then the clock.
func TestTrayDropOrder(t *testing.T) {
	t.Parallel()

	rank := map[string]int{modLoad: 0, modUsage: 1, modPR: 2, modLayout: 3, modClock: 4}

	for _, icons := range []bool{true, false} {
		withPR := prTrayApp(t, icons, prInfo("OPEN", checksRunning))
		if _, ok := findPRModule(withPR); !ok {
			t.Fatalf("icons=%v: no PR module to order", icons)
		}

		withPR.clock = &fakeClock{t: t0}
		seedUsage(t, withPR, usageFive, usageWeek)

		if !slices.Contains(modIDs(withPR.trayModules()), modUsage) {
			t.Fatalf("icons=%v: no usage module to order", icons)
		}

		for _, a := range []*App{trayApp(t, icons), withPR} {
			checkTrayDropOrder(t, a, icons, rank)
		}
	}
}

func checkTrayDropOrder(t *testing.T, a *App, icons bool, rank map[string]int) {
	t.Helper()

	tabs := a.tabInfos()
	full := modIDs(a.trayModules())

	for w := 130; w >= 1; w-- {
		p := planBar(tabs, a.store.State().Active, a.trayModules(), w, icons)
		kept := modIDs(p.tray)

		for _, id := range full {
			if slices.Contains(kept, id) {
				continue
			}

			for _, other := range kept {
				if rank[other] < rank[id] {
					t.Fatalf("icons=%v width %d: %s stayed after %s left (kept %v)", icons, w, other, id, kept)
				}
			}
		}
	}
}

func TestBarGoldens(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		icons bool
		width int
		pr    *core.PRInfo
		usage *[2]core.UsageWindow // seeded when set; width 0 means the widest without load
	}{
		{"icons wide", true, 100, nil, nil},
		{"icons without load", true, 60, nil, nil},
		{"icons layout and clock", true, 50, nil, nil},
		{"plain wide", false, 100, nil, nil},
		{"plain without load", false, 60, nil, nil},
		{"icons wide with a PR running", true, 100, new(prInfo("OPEN", checksRunning)), nil},
		{"icons wide with a PR passed", true, 100, new(prInfo("OPEN", checksPassed)), nil},
		{"icons wide with a PR failed", true, 100, new(prInfo("OPEN", checksFailing)), nil},
		{"icons without load with a PR", true, 70, new(prInfo("OPEN", checksRunning)), nil},
		{"plain wide with a PR running", false, 100, new(prInfo("OPEN", checksRunning)), nil},
		{"plain wide with a PR failed", false, 100, new(prInfo("OPEN", checksFailing)), nil},
		{"plain without load with a PR", false, 70, new(prInfo("OPEN", checksPassed)), nil},
		{"icons wide with usage", true, 100, nil, &[2]core.UsageWindow{usageFive, usageWeek}},
		{"plain wide with usage", false, 100, nil, &[2]core.UsageWindow{usageFive, usageWeek}},
		{"icons without load with usage", true, 0, nil, &[2]core.UsageWindow{usageFive, usageWeek}},
		{"icons hot usage", true, 100, nil, &[2]core.UsageWindow{
			{Used: 95, ResetsAt: usageFive.ResetsAt}, {Used: 74, ResetsAt: usageWeek.ResetsAt},
		}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			a := trayApp(t, tt.icons)
			if tt.pr != nil {
				a = prTrayApp(t, tt.icons, *tt.pr)
			}

			width := tt.width

			if tt.usage != nil {
				a.clock = &fakeClock{t: t0}
				seedUsage(t, a, tt.usage[0], tt.usage[1])

				if width == 0 {
					width = widestWithoutLoad(t, a)
				}
			}

			golden.RequireEqual(t, []byte(a.workspaceBar(width)))
		})
	}
}

// widestWithoutLoad is the widest bar at which the tray has lost the load
// module and keeps the usage module.
func widestWithoutLoad(t *testing.T, a *App) int {
	t.Helper()

	tabs := a.tabInfos()

	for w := 200; w >= 1; w-- {
		kept := modIDs(planBar(tabs, a.store.State().Active, a.trayModules(), w, a.icons).tray)
		if !slices.Contains(kept, modLoad) && slices.Contains(kept, modUsage) {
			return w
		}
	}

	t.Fatal("no width drops load and keeps usage")

	return 0
}

func TestTrayOnlyClockEdge(t *testing.T) {
	t.Parallel()
	a := newTestApp(t)
	sized(t, a, 40, 10)
	a.tray.clock = "14:32"

	vt := lineVT(t, a.workspaceBar(40), 40)
	ui := mochaUI(t)

	assertColours(t, vt, 30, 0, trayOpen, ui.Clock, ui.Bar, false)
	assertColours(t, vt, 32, 0, iconClock, ui.OnAccent, ui.Clock, true)
}

func TestTrayColours(t *testing.T) {
	t.Parallel()
	a := trayApp(t, true)
	ui := mochaUI(t)
	w := 100
	vt := lineVT(t, a.workspaceBar(w), w)

	// Walk the tray from the right: clock segment, load, layout.
	clockW := lipgloss.Width(trayModuleByID(a, modClock).render())
	x := w - clockW
	assertColours(t, vt, x-1, 0, trayOpen, ui.Clock, ui.Surface, false)
	assertColours(t, vt, x+1, 0, iconClock, ui.OnAccent, ui.Clock, true)
	assertColours(t, vt, x+3, 0, "1", ui.OnAccent, ui.Clock, true)

	loadW := trayModuleByID(a, modLoad).width
	lx := x - 1 - loadW
	assertColours(t, vt, lx+1, 0, iconLoad, ui.Load, ui.Surface, false)

	lyW := trayModuleByID(a, modLayout).width
	yx := lx - 1 - lyW
	assertColours(t, vt, lx-1, 0, edgeThin, ui.Bar, ui.Surface, false)
	assertColours(t, vt, yx-1, 0, trayOpen, ui.Surface, ui.Bar, false)
}

func trayModuleByID(a *App, id string) trayModule {
	for _, m := range a.trayModules() {
		if m.id == id {
			return m
		}
	}

	return trayModule{}
}

// Messages that change nothing neither re-render state nor start work.
// Samples that repeat what the bar shows leave its row byte-identical.
func TestUnchangedSamplesLeaveBarIdentical(t *testing.T) {
	t.Parallel()
	a := newTestApp(t)
	sized(t, a, 100, 30)

	for range loadHistory {
		a.Update(loadMsg{sample: loadSample{CPU: 12, Mem: 9 << 30}})
	}

	a.Update(clockMsg{text: "14:32"})

	before := a.workspaceBar(100)

	a.Update(clockMsg{text: "14:32"})
	a.Update(loadMsg{sample: loadSample{CPU: 12, Mem: 9<<30 + 1}})

	if after := a.workspaceBar(100); after != before {
		t.Errorf("bar changed:\n%q\n%q", before, after)
	}
}

func TestClockWake(t *testing.T) {
	t.Parallel()

	base := time.Date(2026, 10, 26, 14, 32, 0, 0, time.UTC)
	tests := []struct {
		name      string
		now       time.Time
		last      string
		wantText  string
		wantAgain time.Duration
	}{
		{"new minute", base.Add(time.Minute), "14:32", "14:33", 0},
		{"woke early", base.Add(59*time.Second + 900*time.Millisecond), "14:32", "", 100 * time.Millisecond},
		{"clock stepped back", base.Add(-30 * time.Second), "14:32", "14:31", 0},
		{"first reading", base, "", "14:32", 0},
	}

	for _, tt := range tests {
		text, again := clockWake(tt.now, tt.last)
		if text != tt.wantText || again != tt.wantAgain {
			t.Errorf("%s: clockWake = %q, %v, want %q, %v", tt.name, text, again, tt.wantText, tt.wantAgain)
		}
	}
}

func TestLoadHistoryKeepsLastTen(t *testing.T) {
	t.Parallel()
	a := newTestApp(t)

	for i := range 15 {
		a.Update(loadMsg{sample: loadSample{CPU: i, Mem: 1 << 30}})
	}

	want := []int{5, 6, 7, 8, 9, 10, 11, 12, 13, 14}
	if got := a.tray.load.CPU; !slices.Equal(got, want) {
		t.Errorf("history = %v, want %v", got, want)
	}
}

func TestLoadErrorKeepsPreviousAndRearms(t *testing.T) {
	t.Parallel()
	a := newTestApp(t)
	a.load = &fakeLoad{}

	a.Update(loadMsg{sample: loadSample{CPU: 7, Mem: 1 << 30}})

	_, cmd := a.Update(loadMsg{err: errors.New("boom")})
	if cmd == nil {
		t.Error("a failed sample did not re-arm")
	}

	if a.tray.load == nil || a.tray.load.CPU[len(a.tray.load.CPU)-1] != 7 {
		t.Errorf("load after an error = %+v, want the previous value", a.tray.load)
	}
}

type fakeLoad struct{ calls atomic.Int32 }

func (f *fakeLoad) Sample(context.Context) (loadSample, error) {
	f.calls.Add(1)

	return loadSample{CPU: 3, Mem: 2 << 30}, nil
}

// No tray work runs inside Update: it only returns commands.
func TestNoIOInUpdate(t *testing.T) {
	t.Parallel()
	a := newTestApp(t)
	fl := &fakeLoad{}
	a.load = fl

	a.Init()
	sized(t, a, 100, 30)
	openIn(t, a, core.OpenPane{Kind: core.KindShell, Cwd: "/w/a", Split: core.SplitVertical})
	a.Update(clockMsg{text: "10:00"})
	a.Update(loadMsg{sample: loadSample{CPU: 1, Mem: 1 << 30}})

	if fl.calls.Load() != 0 {
		t.Errorf("Update ran sampling: load %d", fl.calls.Load())
	}
}

func TestTrayHasNoGitModule(t *testing.T) {
	t.Parallel()

	for _, icons := range []bool{true, false} {
		a := trayApp(t, icons)

		if got := modIDs(a.trayModules()); slices.Contains(got, "git") {
			t.Errorf("icons=%v modules = %v, want no git module", icons, got)
		}

		if bar := a.workspaceBar(100); strings.Contains(bar, "feat/tabs") || strings.Contains(bar, iconGit) {
			t.Errorf("icons=%v bar shows a branch: %q", icons, bar)
		}
	}
}
