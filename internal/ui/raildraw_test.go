package ui

import (
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/charmbracelet/x/exp/golden"

	"github.com/mhersson/aim/internal/core"
	"github.com/mhersson/aim/internal/git"
	"github.com/mhersson/aim/internal/ui/theme"
)

// railAgent is one agent of the rail fixtures.
type railAgent struct {
	newTab         bool
	project        string
	branch         string
	place          string // the other repository the agent follows; empty in its own
	detached       bool
	state          core.AgentState
	action         string
	age            time.Duration
	stats          *git.Stats
	unseen         bool
	noRepo, noHook bool
	name           string       // the session's custom name, read from its transcript
	model          string       // the model its status line reported
	meter          *int         // the context percentage its status line reported; nil reports none
	ahead          int          // the commits its branch has past its upstream
	pr             *core.PRInfo // the pull request of its branch; its head branch is the agent's
	plan           bool         // an unread plan file
}

// longNotice is wide enough that its toast box reaches into the rail's columns at 64 columns.
const longNotice = "ctrl+space > x is not bound, press ctrl+space for the keys"

func railFixtureAgents() []railAgent {
	return []railAgent{
		{newTab: true, project: "api", branch: "main", state: core.AgentBlocked, action: "Allow Bash rm -rf build/?", age: 8 * time.Second, stats: &git.Stats{Files: 2, Added: 12, Removed: 3}, unseen: true},
		{project: "web", branch: "feat/login", state: core.AgentWorking, action: "Edit src/login.tsx", age: 4 * time.Minute, stats: &git.Stats{Files: 5, Added: 120, Removed: 14}},
		{newTab: true, project: "multiplex", branch: "feat/agents-rail", state: core.AgentWorking, action: "Bash go test ./...", age: 95 * time.Second, stats: &git.Stats{Files: 9, Added: 340, Removed: 12}},
		{project: "docs", branch: "main", state: core.AgentIdle, age: 26 * time.Hour, stats: &git.Stats{}},
		{newTab: true, project: "infra", branch: "a1b2c3d", detached: true, state: core.AgentIdle, age: 12 * time.Minute, stats: &git.Stats{Files: 1, Added: 2, Removed: 0}},
		{project: "scratch", noRepo: true, noHook: true},
	}
}

// drawApp builds an app with the agents opened through the store, the rail
// open, a fixed clock and the probe results set by hand.
func drawApp(t *testing.T, themeName string, icons bool, w, h int, agents []railAgent) *App {
	t.Helper()

	a := newThemedApp(t, themeName)
	a.icons = icons
	a.clock = &fakeClock{t: t0}

	sized(t, a, w, h)

	for i, ag := range agents {
		var act core.Action = core.OpenPane{Kind: core.KindAgent, Cwd: "/w/" + ag.project, Split: core.SplitVertical}
		if ag.newTab {
			act = core.NewTab{Kind: core.KindAgent, Cwd: "/w/" + ag.project}
		}

		if err := a.dispatch(act); err != nil {
			t.Fatal(err)
		}

		id := core.PaneID(fmt.Sprintf("p%d", i+1))
		if !ag.noHook {
			openIn(t, a, core.SetAgentState{ID: id, State: ag.state}, core.SetAgentAction{ID: id, Action: ag.action})
		}

		rp := a.railEntry(id)
		rp.unseen = ag.unseen
		rp.head.target, rp.head.own = "/w/"+ag.project, "/w/"+ag.project // the probe results below stand for an answered head probe

		if ag.name != "" {
			transcript := "/w/" + ag.project + "/" + string(id) + ".jsonl"
			openIn(t, a, core.SetAgentSession{ID: id, Session: core.SessionID("s-" + string(id)), Transcript: transcript})
			rp.name = railName{text: ag.name, transcript: transcript}
		}

		if ag.plan {
			plan := core.Plan{Path: "/w/" + ag.project + "/docs/plans/x.md", Root: "/w/" + ag.project, Kind: core.PlanFile, At: t0, Unread: true}
			openIn(t, a, core.NotePlan{ID: id, Plan: plan})
		}

		if ag.model != "" || ag.meter != nil {
			status := core.SetAgentStatus{ID: id, Session: core.SessionID("s-" + string(id)), Model: ag.model}
			if ag.meter != nil {
				status.Context, status.ContextKnown = float64(*ag.meter), true
			}

			openIn(t, a, status)
		}

		if !ag.noHook {
			rp.hookAt = t0.Add(-ag.age)
		}

		if !ag.noRepo {
			rp.probe = railProbe{known: true, repo: true, project: ag.project, branch: ag.branch, place: ag.place, detached: ag.detached, ahead: ag.ahead}
		}

		if ag.pr != nil && !ag.noRepo {
			seedPR(t, a, id, "/w/"+ag.project, ag.branch, *ag.pr)
		}

		// The counts are of the place the head names, which seedPR may have set.
		if ag.stats != nil {
			rp.probe.stats, rp.probe.statsKnown, rp.probe.statsCwd = *ag.stats, true, rp.probe.headCwd
		}
	}

	openIn(t, a, core.SetRail{Open: true})

	return a
}

// aheadAgents is a mix of branches ahead of their upstream by one, two and three
// digits, one cut by the rail's width, and some with nothing ahead.
func aheadAgents() []railAgent {
	return []railAgent{
		{newTab: true, project: "api", branch: "main", state: core.AgentIdle, age: 8 * time.Second, stats: &git.Stats{}},
		{project: "web", branch: "feat/login", state: core.AgentWorking, action: "Edit src/login.tsx", age: 4 * time.Minute, stats: &git.Stats{Files: 5, Added: 120, Removed: 14}, ahead: 2},
		{newTab: true, project: "multiplex", branch: "feat/agents-rail-with-a-long-branch-name", state: core.AgentBlocked, action: "Allow Bash go test ./...?", age: 95 * time.Second, stats: &git.Stats{Files: 9, Added: 340, Removed: 12}, ahead: 14},
		{project: "docs", branch: "main", state: core.AgentIdle, age: 2 * time.Hour, stats: &git.Stats{}, ahead: 123},
	}
}

// namedAgents is one project's agents told apart by their session names, an
// agent with no name, one named as its project and one with a name too long
// for the rail.
func namedAgents() []railAgent {
	return []railAgent{
		{newTab: true, project: "multiplex", branch: "feat/review-drawer", state: core.AgentWorking, action: "Edit internal/ui/rail.go", age: 8 * time.Second, stats: &git.Stats{Files: 4, Added: 80, Removed: 6}, unseen: true, name: "muxr-orchestrator"},
		{project: "multiplex", branch: "feat/review-drawer", state: core.AgentBlocked, action: "Allow Bash go test ./...?", age: 40 * time.Second, stats: &git.Stats{Files: 2, Added: 12, Removed: 3}, name: "t16-implementer"},
		{project: "multiplex", branch: "feat/review-drawer", state: core.AgentIdle, age: 5 * time.Minute, stats: &git.Stats{}, name: "t15-reviewer"},
		{project: "multiplex", branch: "main", state: core.AgentIdle, age: 2 * time.Hour, stats: &git.Stats{}},
		{project: "multiplex", branch: "main", state: core.AgentIdle, age: 3 * time.Hour, stats: &git.Stats{}, name: "multiplex"},
		{project: "docs", branch: "main", state: core.AgentWorking, action: "Read README.md", age: 3 * time.Second, name: "a-session-name-far-too-long-for-a-forty-four-column-rail"},
	}
}

// meteredAgents is agents whose status lines reported a model and a context
// percentage in each colour, one with a model alone, one named, one with no hooks.
func meteredAgents() []railAgent {
	return []railAgent{
		{newTab: true, project: "api", branch: "main", state: core.AgentBlocked, action: "Allow Bash rm -rf build/?", age: 8 * time.Second, stats: &git.Stats{Files: 2, Added: 12, Removed: 3}, unseen: true, model: "Opus 5.5", meter: new(62)},
		{project: "web", branch: "feat/login", state: core.AgentWorking, action: "Edit src/login.tsx", age: 4 * time.Minute, stats: &git.Stats{Files: 5, Added: 120, Removed: 14}, model: "Sonnet 5.5", meter: new(78), name: "t16-implementer"},
		{newTab: true, project: "multiplex", branch: "feat/agents-rail", state: core.AgentWorking, action: "Bash go test ./...", age: 95 * time.Second, stats: &git.Stats{Files: 9, Added: 340, Removed: 12}, model: "Opus 5.5", meter: new(93)},
		{project: "docs", branch: "main", state: core.AgentIdle, age: 26 * time.Hour, stats: &git.Stats{}, model: "Haiku 5.5", meter: new(0)},
		{newTab: true, project: "infra", branch: "a1b2c3d", detached: true, state: core.AgentIdle, age: 12 * time.Minute, stats: &git.Stats{Files: 1, Added: 2, Removed: 0}, model: "Opus 5.5"},
		{project: "scratch", noRepo: true, noHook: true},
	}
}

// planAgents is agents with an unread plan, with and without a meter, an age and the dot.
func planAgents() []railAgent {
	return []railAgent{
		{newTab: true, project: "api", branch: "main", state: core.AgentBlocked, action: "Allow Bash rm -rf build/?", age: 8 * time.Second, stats: &git.Stats{}, unseen: true, model: "Opus 5.5", meter: new(62), plan: true},
		{project: "web", branch: "feat/login", state: core.AgentWorking, action: "Edit src/login.tsx", age: 4 * time.Minute, stats: &git.Stats{}, model: "Sonnet 5.5", plan: true, name: "t16-implementer-with-a-long-name"},
		{newTab: true, project: "docs", branch: "main", state: core.AgentIdle, age: 26 * time.Hour, stats: &git.Stats{}, model: "Haiku 5.5", meter: new(5)},
	}
}

// prAgents is agents on branches with a pull request in each form, and some
// with commits ahead, a model and a meter.
func prAgents() []railAgent {
	return []railAgent{
		{newTab: true, project: "api", branch: "feat/api-keys", state: core.AgentBlocked, action: "Allow Bash rm -rf build/?", age: 8 * time.Second, stats: &git.Stats{Files: 2, Added: 12, Removed: 3}, unseen: true, model: "Opus 5.5", meter: new(62), ahead: 1, pr: new(prInfo("OPEN", checksFailing, prNumber(31)))},
		{project: "web", branch: "feat/login", state: core.AgentWorking, action: "Edit src/login.tsx", age: 4 * time.Minute, stats: &git.Stats{Files: 5, Added: 120, Removed: 14}, model: "Sonnet 5.5", meter: new(78), ahead: 2, pr: new(prInfo("OPEN", checksRunning, prReview("APPROVED"), prNumber(8)))},
		{newTab: true, project: "multiplex", branch: "feat/agents-rail-with-a-long-branch-name", state: core.AgentWorking, action: "Bash go test ./...", age: 95 * time.Second, stats: &git.Stats{Files: 9, Added: 340, Removed: 12}, ahead: 14, pr: new(prInfo("OPEN", checksPassed, prReview("CHANGES_REQUESTED"), prNumber(1042)))},
		{project: "docs", branch: "docs/readme", state: core.AgentIdle, age: 26 * time.Hour, stats: &git.Stats{}, pr: new(prInfo("OPEN", prDraft, checksRunning, prNumber(7)))},
		{newTab: true, project: "infra", branch: "fix/infra", state: core.AgentIdle, age: 12 * time.Minute, stats: &git.Stats{Files: 1, Added: 2, Removed: 0}, pr: new(prInfo("MERGED", prNumber(19)))},
		{project: "ops", branch: "fix/ops", state: core.AgentIdle, age: 3 * time.Hour, stats: &git.Stats{}, pr: new(prInfo("CLOSED", prNumber(20)))},
		{project: "tools", branch: "feat/tools", state: core.AgentIdle, age: 5 * time.Minute, stats: &git.Stats{}, pr: new(prInfo("OPEN", prNumber(5)))},
		{project: "scratch", noRepo: true, noHook: true},
	}
}

// placeAgents is an agent on a worktree of its own repository and one on a
// branch of another repository, with commits ahead.
func placeAgents() []railAgent {
	return []railAgent{
		{newTab: true, project: "multiplex", branch: "feat/follow-edits", state: core.AgentWorking, action: "Edit internal/ui/rail.go", age: 8 * time.Second, stats: &git.Stats{Files: 4, Added: 80, Removed: 6}, model: "Opus 5.5", meter: new(41)},
		{project: "contextmatrix", branch: "feat/retry", place: "contextmatrix-agent", state: core.AgentWorking, action: "Write internal/claim/retry.go", age: 6 * time.Second, stats: &git.Stats{Files: 4, Added: 120, Removed: 18}, model: "Opus 5.5", meter: new(62), ahead: 2},
	}
}

func manyAgents(n, tabs int) []railAgent {
	states := []core.AgentState{core.AgentWorking, core.AgentIdle, core.AgentBlocked, core.AgentUnknown}
	out := make([]railAgent, n)

	for i := range out {
		out[i] = railAgent{
			newTab:  i == 0 || i*tabs/n != (i-1)*tabs/n,
			project: fmt.Sprintf("proj%02d", i+1),
			branch:  fmt.Sprintf("feat/b%d", i+1),
			state:   states[i%len(states)],
			action:  fmt.Sprintf("Edit file%d.go", i+1),
			age:     time.Duration(i+1) * time.Minute,
			stats:   &git.Stats{Files: i % 7, Added: i * 3, Removed: i},
			noHook:  states[i%len(states)] == core.AgentUnknown,
		}
	}

	return out
}

type railCase struct {
	name  string
	build func(t *testing.T, th string, icons bool) *App
}

func railCases() []railCase {
	fixture := func(w, h int, tune func(a *App)) func(t *testing.T, th string, icons bool) *App {
		return func(t *testing.T, th string, icons bool) *App {
			a := drawApp(t, th, icons, w, h, railFixtureAgents())
			a.rail.sel = "p2"

			if tune != nil {
				tune(a)
			}

			return a
		}
	}

	named := func(w, h int, tune func(a *App)) func(t *testing.T, th string, icons bool) *App {
		return func(t *testing.T, th string, icons bool) *App {
			a := drawApp(t, th, icons, w, h, namedAgents())
			a.rail.sel = "p2"

			if tune != nil {
				tune(a)
			}

			return a
		}
	}

	withPRs := func(w, h int) func(t *testing.T, th string, icons bool) *App {
		return func(t *testing.T, th string, icons bool) *App {
			a := drawApp(t, th, icons, w, h, prAgents())
			a.rail.sel = "p2"

			return a
		}
	}

	ahead := func(w, h int) func(t *testing.T, th string, icons bool) *App {
		return func(t *testing.T, th string, icons bool) *App {
			a := drawApp(t, th, icons, w, h, aheadAgents())
			a.rail.sel = "p2"

			return a
		}
	}

	metered := func(w, h int) func(t *testing.T, th string, icons bool) *App {
		return func(t *testing.T, th string, icons bool) *App {
			a := drawApp(t, th, icons, w, h, meteredAgents())
			a.rail.sel = "p2"

			return a
		}
	}

	planned := func(w, h int) func(t *testing.T, th string, icons bool) *App {
		return func(t *testing.T, th string, icons bool) *App {
			a := drawApp(t, th, icons, w, h, planAgents())
			a.rail.sel = "p2"

			return a
		}
	}

	withPlace := func(w, h int) func(t *testing.T, th string, icons bool) *App {
		return func(t *testing.T, th string, icons bool) *App {
			a := drawApp(t, th, icons, w, h, placeAgents())
			a.rail.sel = "p2"

			return a
		}
	}

	return []railCase{
		{"cards", fixture(100, 30, nil)},
		{"focused-blocked-selected", fixture(100, 30, func(a *App) { a.rail.focused, a.rail.sel = true, "p1" })},
		{"pulse-dim", fixture(100, 30, func(a *App) { a.rail.pulseDim = true })},
		{"search-typing", fixture(100, 30, func(a *App) {
			a.rail.focused, a.rail.searching, a.rail.query = true, true, "feat"
			a.railNormalize(a.store.State())
		})},
		{"search-kept", fixture(100, 30, func(a *App) {
			a.rail.focused, a.rail.query = true, "ma"
			a.railNormalize(a.store.State())
		})},
		{"search-no-match", fixture(100, 30, func(a *App) {
			a.rail.focused, a.rail.searching, a.rail.query = true, true, "zzz"
			a.railNormalize(a.store.State())
		})},
		{"width-84", fixture(84, 30, nil)},
		{"width-70", fixture(70, 30, nil)},
		{"width-64", fixture(64, 30, nil)},
		{"overflow-70", fixture(70, 30, func(a *App) { a.rail.sel = "p5" })},
		{"fits-no-counts", fixture(100, 50, nil)},
		{"width-63", fixture(63, 30, nil)},
		{"body-20-rows", fixture(100, 22, func(a *App) { a.rail.sel = "p5" })},
		{"body-9-rows", fixture(100, 11, func(a *App) { a.rail.sel = "p3" })},
		{"body-3-rows", fixture(100, 5, nil)},
		{"toast", fixture(100, 30, func(a *App) { a.notice = "layout: bsp" })},
		{"toast-over-rail", fixture(64, 30, func(a *App) { a.notice = longNotice })},
		{"no-hooks-selected", fixture(100, 30, func(a *App) { a.rail.sel = "p6" })},
		{"palette", fixture(100, 30, func(a *App) { pressRun(a, keyLeader, keyRune(':')) })},
		{"many", func(t *testing.T, th string, icons bool) *App {
			a := drawApp(t, th, icons, 100, 30, manyAgents(64, 9))
			a.rail.focused, a.rail.sel = true, "p58"

			return a
		}},
		{"many-small", func(t *testing.T, th string, icons bool) *App {
			a := drawApp(t, th, icons, 64, 20, manyAgents(64, 9))
			a.rail.focused, a.rail.sel = true, "p37"

			return a
		}},
		{"no-agents", func(t *testing.T, th string, icons bool) *App { return drawApp(t, th, icons, 100, 30, nil) }},
		{"named", named(100, 50, nil)},
		{"named-width-70", named(70, 50, nil)},
		{"metered", metered(100, 50)},
		{"metered-width-70", metered(70, 50)},
		{"metered-width-64", metered(64, 50)},
		{"prs", withPRs(100, 50)},
		{"prs-width-70", withPRs(70, 50)},
		{"prs-width-64", withPRs(64, 50)},
		{"ahead", ahead(100, 40)},
		{"ahead-width-70", ahead(70, 40)},
		{"ahead-width-64", ahead(64, 40)},
		{"place", withPlace(100, 30)},
		{"plan", planned(100, 30)},
		{"named-search", named(100, 30, func(a *App) {
			a.rail.focused, a.rail.query = true, "orch"
			a.railNormalize(a.store.State())
		})},
	}
}

// TestRailGolden draws each case once in colour, in the default theme with icons.
// TestRailText covers icons off and TestScreensTakeTheirColoursFromTheTheme the other theme
// for the scenes it picks.
// It compares highlighted colours (the no-agents welcome card's sample), so it runs serially (docs/gotchas.md).
func TestRailGolden(t *testing.T) {
	for _, c := range railCases() {
		t.Run("catppuccin-mocha/icons=true/"+c.name, func(t *testing.T) {
			golden.RequireEqual(t, []byte(c.build(t, "catppuccin-mocha", true).View().Content))
		})
	}
}

// TestRailText is the same frames without styling and without icons, so the layout reads.
// Its pulse-dim frame equals cards (the pulse is colour only), so it is left out.
func TestRailText(t *testing.T) {
	t.Parallel()

	for _, c := range railCases() {
		if c.name == "pulse-dim" {
			continue
		}

		t.Run("icons=false/"+c.name, func(t *testing.T) {
			golden.RequireEqual(t, []byte(ansi.Strip(c.build(t, "catppuccin-mocha", false).View().Content)))
		})
	}
}

func TestRailTakesColumnsFromPanes(t *testing.T) {
	t.Parallel()
	a := drawApp(t, "catppuccin-mocha", true, 100, 30, railFixtureAgents())
	a.relayout()

	if !a.railVisible() {
		t.Fatal("rail not visible at 100 columns")
	}

	if got, want := a.railRect(), (core.Rect{X: 0, Y: 2, W: 44, H: 28}); got != want {
		t.Errorf("railRect() = %+v, want %+v", got, want)
	}

	if got, want := a.bodyArea(), (core.Rect{X: 44, Y: 2, W: 56, H: 28}); got != want {
		t.Errorf("bodyArea() = %+v, want %+v", got, want)
	}

	for i := range a.store.State().Tabs {
		for id, r := range a.tabRects(i) {
			if r.X < 44 {
				t.Errorf("tab %d pane %s at x=%d, under the rail", i, id, r.X)
			}
		}
	}

	// The active tab holds p5 and p6, side by side across the body.
	rects := a.layoutRects()
	if got := rects["p5"].W + rects["p6"].W; got != 56 {
		t.Errorf("panes of the active tab are %d columns wide with frames, want the body's 56", got)
	}

	st := a.store.State()
	for i := range st.Tabs {
		for id, r := range a.sizesIn(st, i) {
			if r.X < 44 {
				t.Errorf("PTY size rect of %s in tab %d at x=%d, under the rail", id, i, r.X)
			}
		}
	}

	openIn(t, a, core.SetRail{Open: false})

	if got := a.bodyArea(); got != (core.Rect{Y: 2, W: 100, H: 28}) {
		t.Errorf("closed rail: bodyArea() = %+v, want the whole body", got)
	}

	openIn(t, a, core.SetRail{Open: true})
	sized(t, a, 63, 30)

	if a.railVisible() {
		t.Error("rail visible at 63 columns")
	}

	if got := a.bodyArea(); got.X != 0 || got.W != 63 {
		t.Errorf("hidden rail: bodyArea() = %+v, want the whole body", got)
	}
}

func TestRailFramesKeepTheirWidth(t *testing.T) {
	t.Parallel()

	// Widths either side of the rail's 64 and 84 column edges. The rail box is the screen
	// height less 2 rows: it is drawn from 5 rows (minFrame is 3) and shows its hint row from
	// 14 (an inner height of railHintsMinInner, 10), so 4 and 5, 13 and 14 sit either side.
	for _, w := range []int{63, 64, 83, 84, 110} {
		for _, h := range []int{4, 5, 13, 14, 30} {
			a := drawApp(t, "catppuccin-mocha", true, w, h, railFixtureAgents())
			a.rail.focused, a.rail.searching, a.rail.query = true, true, "a"
			a.rail.sel = "p3"

			lines := strings.Split(a.View().Content, "\n")
			if len(lines) != h {
				t.Fatalf("%dx%d: %d lines, want %d", w, h, len(lines), h)
			}

			for i, l := range lines {
				if got := lipgloss.Width(l); got != w {
					t.Fatalf("%dx%d line %d: width %d, want %d: %q", w, h, i, got, w, ansi.Strip(l))
				}
			}
		}
	}
}

func TestRailCutRuns(t *testing.T) {
	t.Parallel()

	runs := []railRun{{text: "ab"}, {text: "日本語テキスト"}}

	for _, w := range []int{0, 1, 4, 15, 16} {
		var sb strings.Builder
		for _, r := range cutRuns(runs, w) {
			sb.WriteString(r.text)
		}

		if got := ansi.StringWidth(sb.String()); got > w {
			t.Errorf("cutRuns(%d) is %d cells wide: %q", w, got, sb.String())
		}
	}

	var sb strings.Builder
	for _, r := range cutRuns(runs, 7) {
		sb.WriteString(r.text)
	}

	if got, want := sb.String(), "ab日本…"; got != want {
		t.Errorf("cutRuns(7) = %q, want %q", got, want)
	}
}

func TestRailCursor(t *testing.T) {
	t.Parallel()
	a := drawApp(t, "catppuccin-mocha", true, 100, 30, railFixtureAgents())

	a.rail.searching, a.rail.query = true, "ab"
	if c := a.cursorView(); c != nil {
		t.Errorf("cursor with the rail unfocused = %+v, want none", c)
	}

	a.rail.focused = true

	c := a.cursorView()
	if c == nil {
		t.Fatal("no cursor while typing in the rail")
	}

	// Content starts at column 2; the icon, a space and the text come before the cursor cell.
	if c.X != 2+2+2 || c.Y != 3 {
		t.Errorf("cursor at (%d, %d), want (6, 3)", c.X, c.Y)
	}

	a.rail.searching = false
	if c := a.cursorView(); c != nil && c.X < 44 {
		t.Errorf("cursor at (%d, %d) in the rail while not typing", c.X, c.Y)
	}

	a.rail.searching = true
	a.overlay = nil
	a.notice = "layout: bsp"
	// The toast sits at the right edge; a rail cursor on the left stays.
	if a.cursorView() == nil {
		t.Error("toast away from the cursor hid it")
	}
}

func TestRailFlashColoursTheWholeFrame(t *testing.T) {
	t.Parallel()
	a := drawApp(t, "catppuccin-mocha", true, 100, 30, railFixtureAgents())
	ui := a.current().theme.UI
	a.relayout()

	r := a.layoutRects()["p5"]
	a.rail.flashPane, a.rail.flashUntil = "p5", t0.Add(600*time.Millisecond)

	lines := strings.Split(a.View().Content, "\n")
	assertColours(t, lineVT(t, lines[r.Y], 100), r.X, 0, "╭", ui.Match, ui.Bg, false)
	assertColours(t, lineVT(t, lines[r.Y+r.H-1], 100), r.X+r.W-1, 0, "╯", ui.Match, ui.Bg, false)

	// The flash ends with the clock.
	a.clock = &fakeClock{t: t0.Add(600 * time.Millisecond)}
	lines = strings.Split(a.View().Content, "\n")
	assertColours(t, lineVT(t, lines[r.Y], 100), r.X, 0, "╭", ui.Border, ui.Bg, false)
}

func TestRailToastOverRailKeepsItsRows(t *testing.T) {
	t.Parallel()
	base := drawApp(t, "catppuccin-mocha", true, 64, 30, railFixtureAgents())
	with := drawApp(t, "catppuccin-mocha", true, 64, 30, railFixtureAgents())
	with.notice = longNotice

	_, x, y, w, ok := toastLayout(64, 30, longNotice, true)
	if !ok {
		t.Fatal("no toast box")
	}

	if x >= with.railRect().W {
		t.Fatalf("toast starts at column %d, right of the rail (%d wide): it does not overlap", x, with.railRect().W)
	}

	// The toast pass re-encodes styles, so the rows are compared by their cells' text.
	was := strings.Split(ansi.Strip(base.View().Content), "\n")
	got := strings.Split(ansi.Strip(with.View().Content), "\n")

	for i := range was {
		if i < y || i >= y+toastRows {
			if was[i] != got[i] {
				t.Errorf("row %d changed outside the toast: %q vs %q", i, was[i], got[i])
			}

			continue
		}

		if l, r := ansi.Cut(was[i], 0, x), ansi.Cut(got[i], 0, x); l != r {
			t.Errorf("row %d left of the toast changed: %q vs %q", i, l, r)
		}

		if l, r := ansi.Cut(was[i], x+w, 64), ansi.Cut(got[i], x+w, 64); l != r {
			t.Errorf("row %d right of the toast changed: %q vs %q", i, l, r)
		}
	}

	if !strings.Contains(got[y+1], "ctrl+space") {
		t.Errorf("toast text row = %q", got[y+1])
	}
}

func TestRailHighlightOnCutText(t *testing.T) {
	t.Parallel()
	a := drawApp(t, "catppuccin-mocha", true, 100, 30, railFixtureAgents())
	ui := a.current().theme.UI
	a.rail.query = "web"

	marked := func(project string, cw int) (hit string, cut bool) {
		c := railCard{pane: "p1", project: project, state: core.AgentIdle}
		hits, _ := c.matches(a.rail.query)
		runs := a.railCardLine(ui, c, hits, 0, cw)

		for _, r := range runs {
			if r.fg == ui.Match && r.bold {
				hit += r.text
			}

			cut = cut || strings.HasSuffix(r.text, "…")
		}

		return hit, cut
	}

	// Wide runes before the match: the offsets are runes, the widths are cells.
	if hit, _ := marked("日本web", 20); hit != "web" {
		t.Errorf("highlight after wide runes = %q, want web", hit)
	}

	// The match lies past the cut: nothing is highlighted and the line ends in the ellipsis.
	if hit, cut := marked("日本語日本語日本語日本語web", 12); hit != "" || !cut {
		t.Errorf("highlight past the cut = %q, cut = %v, want none and a cut", hit, cut)
	}

	// A match that straddles the cut keeps only the shown part; the cut's
	// ellipsis takes the colour of the run it ends.
	if hit, cut := marked("日本語日本語web", 16); strings.TrimSuffix(hit, "…") != "w" || !cut {
		t.Errorf("highlight across the cut = %q, cut = %v, want only the shown w and a cut", hit, cut)
	}
}

func TestRailCardsAreMadePrintableOnce(t *testing.T) {
	t.Parallel()

	agents := railFixtureAgents()[:2]
	agents[1].project, agents[1].branch, agents[1].action = "we\x07b", "ma\x1bin", "Ed\x01it web"
	a := drawApp(t, "catppuccin-mocha", true, 100, 30, agents)
	a.rail.query = "web"

	var web railCard

	for _, c := range a.railShown(a.store.State()) {
		if c.pane == "p2" {
			web = c
		}
	}

	if web.project != "web" || web.branch != "main" || web.action != "Edit web" {
		t.Fatalf("card = %q %q %q, want printable fields", web.project, web.branch, web.action)
	}

	hits, ok := web.matches("web")
	if !ok || len(hitsOf(hits, railFieldProject)) != 1 || len(hitsOf(hits, railFieldAction)) != 1 {
		t.Errorf("hits = %+v, want one in project and one in action", hits)
	}
}

// railTextLines is the rail's lines as plain text, after a frame is drawn.
func railTextLines(a *App) []string {
	a.View()

	out := make([]string, len(a.rail.cache.lines))
	for i, l := range a.rail.cache.lines {
		out[i] = ansi.Strip(l)
	}

	return out
}

// linesWith lists the lines that hold sub.
func linesWith(lines []string, sub string) []string {
	var out []string

	for _, l := range lines {
		if strings.Contains(l, sub) {
			out = append(out, l)
		}
	}

	return out
}

func cardOf(t *testing.T, a *App, id core.PaneID) railCard {
	t.Helper()

	for _, c := range a.railShown(a.store.State()) {
		if c.pane == id {
			return c
		}
	}

	t.Fatalf("no card for %s", id)

	return railCard{}
}

func TestRailCardFallsBackToTheProject(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name   string
		agent  railAgent
		change func(t *testing.T, a *App)
	}{
		{"no name", railAgent{project: "proj", state: core.AgentIdle}, nil},
		{"an empty name", railAgent{project: "proj", state: core.AgentIdle, name: "orch"}, func(_ *testing.T, a *App) {
			a.railEntry("p1").name.text = ""
		}},
		{"a name read from an earlier transcript", railAgent{project: "proj", state: core.AgentIdle, name: "orch"}, func(_ *testing.T, a *App) {
			a.railEntry("p1").name.transcript = "/w/proj/earlier.jsonl"
		}},
		{"a session that ended", railAgent{project: "proj", state: core.AgentIdle, name: "orch"}, func(t *testing.T, a *App) {
			openIn(t, a, core.SetAgentSession{ID: "p1"})
		}},
		{"only blanks and invisible characters", railAgent{project: "proj", state: core.AgentIdle, name: "orch"}, func(_ *testing.T, a *App) {
			a.railEntry("p1").name.text = " \u00a0\u200b\u2028 "
		}},
		{"the project's own name", railAgent{project: "proj", state: core.AgentIdle, name: "orch"}, func(_ *testing.T, a *App) {
			a.railEntry("p1").name.text = "proj"
		}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			a := drawApp(t, "catppuccin-mocha", true, 100, 30, []railAgent{tt.agent})

			if tt.change != nil {
				tt.change(t, a)
			}

			if got := cardOf(t, a, "p1").name; got != "" {
				t.Errorf("card name = %q, want none", got)
			}

			lines := railTextLines(a)
			if rows := linesWith(lines, "proj"); len(rows) != 1 || !strings.Contains(rows[0], " proj ") {
				t.Errorf("the project shows on %q, want once on the card's first line", rows)
			}

			if rows := linesWith(lines, "orch"); len(rows) != 0 {
				t.Errorf("the name shows on %q", rows)
			}
		})
	}
}

func TestRailCardCutsTheName(t *testing.T) {
	t.Parallel()

	const (
		wide   = "\u6f22" // two cells
		family = "\U0001F468\u200D\U0001F469\u200D\U0001F467"
	)

	tests := []struct {
		name string
		text string
		keep string // the name before the cut mark
		unit string // what the last cell group of keep must be whole, if set
	}{
		{"ascii", strings.Repeat("0123456789", 6), "01234567890123456789012345678901", ""},
		{"wide characters on the boundary", strings.Repeat(wide, 60), strings.Repeat(wide, 16), wide},
		{"wide characters, odd aligned", "a" + strings.Repeat(wide, 59), "a" + strings.Repeat(wide, 15), wide},
		{"joined emoji on the boundary", strings.Repeat(family, 20), strings.Repeat(family, 16), family},
		{"joined emoji, odd aligned", "x" + strings.Repeat(family, 20), "x" + strings.Repeat(family, 15), family},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			a := drawApp(t, "catppuccin-mocha", true, 100, 44, []railAgent{
				{project: "proj", state: core.AgentWorking, action: "x", age: 8 * time.Second, unseen: true, name: tt.text},
			})

			rows := linesWith(railTextLines(a), " 8s ●")
			if len(rows) != 1 {
				t.Fatalf("card lines = %d, want 1", len(rows))
			}

			row := rows[0]

			// 40 content columns: the glyph and a space, the name, the time and the dot.
			// The time and the dot stay whole; the name gives way with a cut mark.
			before, _, ok := strings.Cut(row, "…")
			if !ok || !strings.Contains(row, " 8s ●") {
				t.Fatalf("row = %q, want a cut mark before 8s and the dot", row)
			}

			// The row starts with the edge, a space, the glyph and a space.
			keep := string([]rune(before)[4:])
			if keep != tt.keep {
				t.Errorf("name before the cut = %q, want %q", keep, tt.keep)
			}

			if !strings.HasSuffix(keep, tt.unit) {
				t.Errorf("the cut split %q", tt.unit)
			}

			if w := ansi.StringWidth(row); w != 44 {
				t.Errorf("row is %d cells wide, want 44", w)
			}
		})
	}

	// The same rule cuts a project.
	name := strings.Repeat("0123456789", 6)

	b := drawApp(t, "catppuccin-mocha", true, 100, 30, []railAgent{
		{project: name, state: core.AgentWorking, action: "x", age: 8 * time.Second, unseen: true},
	})

	if rows := linesWith(railTextLines(b), "0123"); len(rows) != 1 || !strings.Contains(rows[0], " "+name[:32]+"… ") {
		t.Errorf("project rows = %q, want the project cut to %q", rows, name[:32]+"…")
	}
}

// The pane's frame keeps its own title, the guest's OSC title, however its
// card is named.
func TestRailNameLeavesThePaneTitleAlone(t *testing.T) {
	t.Parallel()
	a, _ := framedApp(t, 100, 12, paneSpec{kind: core.KindAgent, cwd: "/w/proj", split: core.SplitVertical, text: "\x1b]2;vim main.go\x07"})
	openIn(t, a, core.SetRail{Open: true}, core.SetAgentSession{ID: "p1", Session: "s-p1", Transcript: "/w/proj/p1.jsonl"})
	a.railEntry("p1").name = railName{text: "muxr-orchestrator", transcript: "/w/proj/p1.jsonl"}

	lines := strings.Split(ansi.Strip(a.View().Content), "\n")

	// Row 2 is the top border of both the rail and the pane's frame.
	if !strings.Contains(lines[2], "vim main.go") || strings.Contains(lines[2], "muxr-orchestrator") {
		t.Errorf("top border = %q, want the pane's OSC title and not the name", lines[2])
	}

	if rows := linesWith(lines, "muxr-orchestrator"); len(rows) != 1 || strings.Contains(rows[0], "vim main.go") {
		t.Errorf("the name is on %q, want the rail's card alone", rows)
	}
}

func TestRailCardNameIsCleaned(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name, raw, want string
	}{
		{"an escape sequence", "a\x1b[31mb\x1b]0;pwn\x07c", "a[31mb]0;pwnc"},
		{"a bidi override", "ab\u202ecd", "abcd"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			a := drawApp(t, "catppuccin-mocha", true, 100, 30, []railAgent{{project: "proj", state: core.AgentIdle, name: "orch"}})
			a.railEntry("p1").name.text = tt.raw

			if got := cardOf(t, a, "p1").name; got != tt.want {
				t.Errorf("card name = %q, want %q", got, tt.want)
			}

			view := a.View().Content
			for _, bad := range []string{"\x1b]", "\u202e", "\u009b", "\u200b", "\x07", "\u2028"} {
				if strings.Contains(view, bad) {
					t.Errorf("the frame holds %q", bad)
				}
			}

			if rows := linesWith(railTextLines(a), tt.want); len(rows) != 1 {
				t.Errorf("the cleaned name is on %d lines, want 1", len(rows))
			}
		})
	}
}

func TestSpinFrame(t *testing.T) {
	t.Parallel()

	frames := []rune(spinFrames)

	for _, n := range []int{0, len(frames) - 1, len(frames)} {
		if got, want := spinFrame(n), string(frames[n%len(frames)]); got != want {
			t.Errorf("spinFrame(%d) = %q, want %q", n, got, want)
		}
	}
}

func TestRailBottomCounts(t *testing.T) {
	t.Parallel()
	ui := mustTheme(t, "catppuccin-mocha").UI

	tests := []struct {
		name         string
		w            int
		above, below int
		want         string
	}{
		{"none", 12, 0, 0, "╰──────────╯"},
		{"both", 20, 3, 12, "╰─ ↑ 3 ───── 12 ↓ ─╯"},
		{"above only", 12, 3, 0, "╰─ ↑ 3 ────╯"},
		{"below only", 12, 0, 3, "╰──── 3 ↓ ─╯"},
		{"the right count is left out when both do not fit", 10, 3, 3, "╰─ ↑ 3 ──╯"},
		{"neither fits", 6, 3, 3, "╰────╯"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ansi.Strip(railBottom(ui, tt.w, ui.Border, tt.above, tt.below))
			if got != tt.want || ansi.StringWidth(got) != tt.w {
				t.Errorf("railBottom = %q (%d wide), want %q", got, ansi.StringWidth(got), tt.want)
			}
		})
	}
}

// While the rail has the keyboard the tab's focused pane draws as an unfocused
// one: Border frame, Muted title. The jump flash still wins.
func TestRailFocusedPaneDrawsUnfocused(t *testing.T) {
	t.Parallel()
	a := drawApp(t, "catppuccin-mocha", true, 100, 30, railFixtureAgents())
	ui := a.current().theme.UI
	a.relayout()

	st := a.store.State()
	id := st.Tab().Focus
	r := a.layoutRects()[id]

	kindC := ui.Shell
	if st.Panes[id].Kind == core.KindAgent {
		kindC = ui.Agent
	}

	// The corner is the frame, the cell after "icon " is the first title cell.
	check := func(name string, border, title theme.Hex) {
		t.Helper()

		vt := lineVT(t, strings.Split(a.View().Content, "\n")[r.Y], 100)
		if c := vt.CellAt(r.X, 0); c == nil || hexOf(c.Style.Fg) != string(border) {
			t.Errorf("%s: frame = %+v, want %v", name, c, border)
		}

		if c := vt.CellAt(r.X+5, 0); c == nil || hexOf(c.Style.Fg) != string(title) {
			t.Errorf("%s: title = %+v, want %v", name, c, title)
		}
	}

	check("panes have the keyboard", kindC, kindC)

	a.rail.focused = true

	check("rail has the keyboard", ui.Border, ui.Muted)

	a.rail.flashPane, a.rail.flashUntil = id, t0.Add(600*time.Millisecond)

	check("flashed", ui.Match, ui.Match)
}

// paneColours is the colour of a pane's frame corner and of the first cell of
// its title, as the frame draws them.
func paneColours(t *testing.T, a *App, id core.PaneID) (border, title string) {
	t.Helper()

	r := a.layoutRects()[id]
	vt := lineVT(t, strings.Split(a.View().Content, "\n")[r.Y], a.width)

	b, ti := vt.CellAt(r.X, 0), vt.CellAt(r.X+5, 0)
	if b == nil || ti == nil {
		t.Fatalf("pane %s: no cell at its corner or title", id)
	}

	return hexOf(b.Style.Fg), hexOf(ti.Style.Fg)
}

// frameCells is every cell right of the rail and below the bar, as colours and
// content, leaving out the rect skip (the focused pane's).
func frameCells(t *testing.T, a *App, skip core.Rect) map[[2]int]string {
	t.Helper()

	out := map[[2]int]string{}
	railW := railWidthFor(bodyRect(a.width, a.height))

	for y, line := range strings.Split(a.View().Content, "\n") {
		if y < 2 {
			continue
		}

		vt := lineVT(t, line, a.width)

		for x := railW; x < a.width; x++ {
			if x >= skip.X && x < skip.X+skip.W && y >= skip.Y && y < skip.Y+skip.H {
				continue
			}

			c := vt.CellAt(x, 0)
			if c == nil {
				continue
			}

			fg, bg := "", ""
			if c.Style.Fg != nil {
				fg = hexOf(c.Style.Fg)
			}

			if c.Style.Bg != nil {
				bg = hexOf(c.Style.Bg)
			}

			out[[2]int{x, y}] = c.Content + fg + bg
		}
	}

	return out
}

func TestRailFocusRules(t *testing.T) {
	t.Parallel()
	t.Run("a rail the width rule hides cannot take the colours", func(t *testing.T) {
		a := drawApp(t, "catppuccin-mocha", true, 63, 30, railFixtureAgents())
		ui := a.current().theme.UI
		a.relayout()

		id := a.store.State().Tab().Focus
		kindC := ui.Shell

		if a.store.State().Panes[id].Kind == core.KindAgent {
			kindC = ui.Agent
		}

		a.rail.focused = true

		if a.railShows(true) {
			t.Fatal("setup: the rail shows at 63 columns")
		}

		if border, title := paneColours(t, a, id); border != string(kindC) || title != string(kindC) {
			t.Errorf("focused pane under a hidden rail: frame %s, title %s, want %s", border, title, kindC)
		}
	})

	t.Run("other panes are unchanged while the rail has the keyboard", func(t *testing.T) {
		a := drawApp(t, "catppuccin-mocha", true, 100, 30, railFixtureAgents())
		a.relayout()

		for tab := range a.store.State().Tabs {
			openIn(t, a, core.SelectTab{Index: tab})
			a.relayout()

			a.rail.focused = false
			skip := a.layoutRects()[a.store.State().Tab().Focus]
			before := frameCells(t, a, skip)

			a.rail.focused = true
			after := frameCells(t, a, skip)

			if len(after) != len(before) || len(before) == 0 {
				t.Fatalf("tab %d: %d cells before, %d after", tab, len(before), len(after))
			}

			for pos, c := range before {
				if after[pos] != c {
					t.Errorf("tab %d: cell %v changed from %q to %q", tab, pos, c, after[pos])

					break
				}
			}
		}
	})
}

func TestRailLineTwoPlace(t *testing.T) {
	t.Parallel()

	a := newTestApp(t)
	ui := a.current().theme.UI

	lead := func(icons bool) string {
		if icons {
			return "  " + iconGit + " "
		}

		return "  "
	}

	tests := []struct {
		name     string
		project  string
		place    string
		branch   string
		detached bool
		ahead    int
		icons    bool
		cw       int
		want     string
	}{
		{"same repository", "contextmatrix", "", "feat/claim-retry", false, 0, true, 50, lead(true) + "feat/claim-retry"},
		{"another repository", "contextmatrix", "contextmatrix-agent", "feat/claim-retry", false, 0, true, 50, lead(true) + "contextmatrix-agent · feat/claim-retry"},
		{"own directory in no repository", "scratch", "api", "main", false, 0, true, 50, lead(true) + "api · main"},
		{"detached HEAD in another repository", "contextmatrix", "api", "a1b2c3d", true, 0, true, 50, lead(true) + "api · a1b2c3d"},
		{"icons off", "contextmatrix", "contextmatrix-agent", "feat/claim-retry", false, 0, false, 50, lead(false) + "contextmatrix-agent · feat/claim-retry"},
		{"room for the commits ahead", "contextmatrix", "contextmatrix-agent", "feat/claim-retry", false, 2, true, 45, lead(true) + "contextmatrix-agent · feat/claim-retry ↑2"},
		{"the branch is cut first", "contextmatrix", "contextmatrix-agent", "feat/claim-retry", false, 2, true, 40, lead(true) + "contextmatrix-agent · feat/claim… ↑2"},
		{"then the name", "contextmatrix", "contextmatrix-agent", "feat/claim-retry", false, 2, true, 25, lead(true) + "contextmatrix-age… ↑2"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			a.icons = tt.icons

			c := railCard{
				pane: "p1", state: core.AgentIdle, project: tt.project, place: tt.place,
				branch: tt.branch, detached: tt.detached, ahead: tt.ahead,
			}

			runs := a.railCardLine(ui, c, nil, 1, tt.cw)
			if got := runsText(runs); got != tt.want {
				t.Errorf("line = %q, want %q", got, tt.want)
			}

			if w := runsWidth(runs); w > tt.cw {
				t.Errorf("line is %d cells wide, want at most %d", w, tt.cw)
			}
		})
	}
}

func TestRailLineTwoPlaceColours(t *testing.T) {
	t.Parallel()

	a := newTestApp(t)
	ui := a.current().theme.UI

	if ui.Dim == ui.Muted {
		t.Fatal("the theme's Dim and Muted are one colour, so this test tells nothing")
	}

	c := railCard{pane: "p1", state: core.AgentIdle, project: "contextmatrix", place: "contextmatrix-agent", branch: "feat/claim-retry"}
	runs := a.railCardLine(ui, c, nil, 1, 60)

	want := []railRun{
		{text: "contextmatrix-agent", fg: ui.Dim, bold: true},
		{text: " · ", fg: ui.Muted},
		{text: "feat/claim-retry", fg: ui.Dim},
	}

	for _, w := range want {
		i := slices.IndexFunc(runs, func(r railRun) bool { return r.text == w.text })
		if i < 0 {
			t.Errorf("no run %q in %q", w.text, runsText(runs))

			continue
		}

		if r := runs[i]; r.fg != w.fg || r.bold != w.bold {
			t.Errorf("%q is %v bold %v, want %v bold %v", w.text, r.fg, r.bold, w.fg, w.bold)
		}
	}
}

func TestRailSearchMatchesPlace(t *testing.T) {
	t.Parallel()

	a := drawApp(t, "catppuccin-mocha", true, 100, 30, placeAgents())
	ui := a.current().theme.UI
	a.rail.focused, a.rail.query = true, "agent"
	a.railNormalize(a.store.State())

	shown := a.railShown(a.store.State())
	if len(shown) != 1 || shown[0].pane != "p2" {
		t.Fatalf("shown = %+v, want only the card that follows contextmatrix-agent", shown)
	}

	hits, _ := shown[0].matches("agent")
	if got := hitsOf(hits, railFieldPlace); len(got) != 1 || got[0].start != 14 || got[0].end != 19 {
		t.Errorf("place hits = %+v, want one at [14, 19)", got)
	}

	for y, l := range strings.Split(a.View().Content, "\n") {
		if x := strings.Index(ansi.Strip(l), "contextmatrix-agent"); x >= 0 && y > 4 {
			col := ansi.StringWidth(ansi.Strip(l)[:x]) + 14

			vt := lineVT(t, l, 100)
			assertColours(t, vt, col, 0, "a", ui.Match, ui.Bg, true)
			assertColours(t, vt, col-1, 0, "-", ui.Dim, ui.Bg, true)

			return
		}
	}

	t.Fatal("no card line names the place")
}
