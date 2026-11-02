package ui

import (
	"context"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/mhersson/aim/internal/core"
	"github.com/mhersson/aim/internal/github"
	"github.com/mhersson/aim/internal/ui/theme"
)

// seedBranch gives pane id a head probe on branch and a resolved repository, as
// the probes leave them for a pane in a GitHub repository.
func seedBranch(a *App, id core.PaneID, cwd, branch string) github.Repo {
	repo := github.Repo{Host: "github.com", Owner: "acme", Name: filepath.Base(cwd)}

	// The rest of the probe stays as the fixture left it.
	p := &a.railEntry(id).probe
	p.known, p.repo, p.branch, p.headCwd = true, true, branch, cwd

	if p.project == "" {
		p.project = repo.Name
	}

	if a.prs.panes == nil {
		a.prs.panes = map[core.PaneID]*prPane{}
	}

	key := prKey{cwd: cwd, branch: branch}
	a.prs.panes[id] = &prPane{key: key, answered: key, repo: repo, ok: true}

	return repo
}

// seedPR is seedBranch and the pull request of that branch in the store: in the
// poll's open list while it is open, else as the branch lookup's find.
func seedPR(t *testing.T, a *App, id core.PaneID, cwd, branch string, pr core.PRInfo) {
	t.Helper()

	key := seedBranch(a, id, cwd, branch).String()
	pr.HeadBranch = branch

	if pr.State == "OPEN" {
		open := append(slices.Clone(a.store.State().PRs[key].Open), pr)
		openIn(t, a, core.SetRepoPRs{Repo: key, DefaultBranch: "main", Open: open})

		return
	}

	openIn(t, a, core.SetBranchPR{Repo: key, Branch: branch, PR: pr, Found: true})
}

// The check counts of a pull request: some running, all passed, one run failing.
func checksRunning(p *core.PRInfo) { p.ChecksTotal, p.ChecksDone = 5, 3 }
func checksPassed(p *core.PRInfo)  { p.ChecksTotal, p.ChecksDone = 5, 5 }
func checksFailing(p *core.PRInfo) { p.ChecksTotal, p.ChecksDone, p.ChecksFailed = 5, 3, 2 }

// Failures that do not look like checksFailing: every check done, and none counted at all.
func checksDoneFailing(p *core.PRInfo)     { p.ChecksTotal, p.ChecksDone, p.ChecksFailed = 5, 5, 2 }
func checksFailedUncounted(p *core.PRInfo) { p.ChecksTotal, p.ChecksDone, p.ChecksFailed = 0, 0, 1 }

func prInfo(state string, tunes ...func(p *core.PRInfo)) core.PRInfo {
	p := core.PRInfo{Number: 24, State: state}
	for _, tune := range tunes {
		tune(&p)
	}

	return p
}

func prReview(decision string) func(p *core.PRInfo) {
	return func(p *core.PRInfo) { p.ReviewDecision = decision }
}

func prDraft(p *core.PRInfo) { p.Draft = true }

func prNumber(n int) func(p *core.PRInfo) {
	return func(p *core.PRInfo) { p.Number = n }
}

func TestRailLineTwoPR(t *testing.T) {
	t.Parallel()

	const cw = 40

	a := newTestApp(t)
	ui := a.current().theme.UI

	// right is the text at the line's right end; the branch takes the rest.
	line := func(icons bool, right string) string {
		lead := "  "
		if icons {
			lead += iconGit + " "
		}

		if right == "" {
			return lead + "feat/login"
		}

		pad := cw - ansi.StringWidth(lead+"feat/login"+right)

		return lead + "feat/login" + strings.Repeat(" ", pad) + right
	}

	tests := []struct {
		name  string
		pr    *core.PRInfo
		ahead int
		icons bool
		right string
	}{
		{"open, checks running", new(prInfo("OPEN", checksRunning)), 0, true, iconPR + " #24 " + iconRunning + " 3/5"},
		{"open, checks passed", new(prInfo("OPEN", checksPassed)), 0, true, iconPR + " #24 " + iconPassed + " 5/5"},
		{"open, checks failed, others still running", new(prInfo("OPEN", checksFailing)), 0, true, iconPR + " #24 " + iconFailed + " 2 failed"},
		{"open, no checks", new(prInfo("OPEN")), 0, true, iconPR + " #24"},
		{"failed with every check done", new(prInfo("OPEN", checksDoneFailing)), 0, true, iconPR + " #24 " + iconFailed + " 2 failed"},
		{"failed with no check counted", new(prInfo("OPEN", checksFailedUncounted)), 0, true, iconPR + " #24 " + iconFailed + " 1 failed"},
		{"approved", new(prInfo("OPEN", checksRunning, prReview("APPROVED"))), 0, true, iconPR + " #24 " + iconRunning + " 3/5 " + iconApproved},
		{"approved, no checks", new(prInfo("OPEN", prReview("APPROVED"))), 0, true, iconPR + " #24 " + iconApproved},
		{"changes requested", new(prInfo("OPEN", checksPassed, prReview("CHANGES_REQUESTED"))), 0, true, iconPR + " #24 " + iconPassed + " 5/5 " + iconChanges},
		{"review required reads as no review", new(prInfo("OPEN", checksRunning, prReview("REVIEW_REQUIRED"))), 0, true, iconPR + " #24 " + iconRunning + " 3/5"},
		{"draft", new(prInfo("OPEN", prDraft, checksRunning, prReview("APPROVED"))), 0, true, iconPRDraft + " #24 draft"},
		{"merged", new(prInfo("MERGED", checksPassed, prReview("APPROVED"))), 0, true, iconMerge + " #24 merged"},
		{"closed", new(prInfo("CLOSED", checksRunning)), 0, true, iconPRClosed + " #24 closed"},
		{"after the commits ahead", new(prInfo("OPEN", checksRunning)), 2, true, "↑2  " + iconPR + " #24 " + iconRunning + " 3/5"},
		{"merged after the commits ahead", new(prInfo("MERGED")), 12, true, "↑12  " + iconMerge + " #24 merged"},
		{"no pull request", nil, 0, true, ""},
		{"no pull request, commits ahead", nil, 2, true, "↑2"},

		{"icons off, running", new(prInfo("OPEN", checksRunning)), 0, false, "#24 ci 3/5"},
		{"icons off, failed", new(prInfo("OPEN", checksFailing)), 0, false, "#24 ci 2 failed"},
		{"icons off, approved", new(prInfo("OPEN", checksRunning, prReview("APPROVED"))), 0, false, "#24 ci 3/5 approved"},
		{"icons off, changes requested", new(prInfo("OPEN", checksPassed, prReview("CHANGES_REQUESTED"))), 0, false, "#24 ci 5/5 changes"},
		{"icons off, merged", new(prInfo("MERGED")), 0, false, "#24 merged"},
		{"icons off, after the commits ahead", new(prInfo("OPEN", checksRunning)), 2, false, "↑2  #24 ci 3/5"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			a.icons = tt.icons

			c := railCard{pane: "p1", state: core.AgentIdle, project: "api", branch: "feat/login", ahead: tt.ahead}
			if tt.pr != nil {
				c.pr, c.hasPR = *tt.pr, true
			}

			runs := a.railCardLine(ui, c, nil, 1, cw)

			want := line(tt.icons, tt.right)
			if got := runsText(runs); got != want {
				t.Errorf("line = %q, want %q", got, want)
			}

			if w := runsWidth(runs); w > cw {
				t.Errorf("line is %d cells wide, want at most %d", w, cw)
			}
		})
	}
}

func TestRailLineTwoPRColours(t *testing.T) {
	t.Parallel()
	a := newTestApp(t)
	ui := a.current().theme.UI

	type piece struct {
		text string
		fg   theme.Hex
		bold bool
	}

	tests := []struct {
		name  string
		pr    core.PRInfo
		icons bool
		want  []piece
	}{
		{"running", prInfo("OPEN", checksRunning), true, []piece{
			{iconPR, ui.Info, false}, {"#24", ui.Fg, true}, {iconRunning, ui.Warning, false}, {"3/5", ui.Warning, false},
		}},
		{"passed", prInfo("OPEN", checksPassed), true, []piece{{iconPassed, ui.Success, false}, {"5/5", ui.Success, false}}},
		{"failed", prInfo("OPEN", checksFailing), true, []piece{{iconFailed, ui.Error, true}, {"2 failed", ui.Error, true}}},
		{"approved", prInfo("OPEN", prReview("APPROVED")), true, []piece{{iconApproved, ui.Success, false}}},
		{"changes requested", prInfo("OPEN", prReview("CHANGES_REQUESTED")), true, []piece{{iconChanges, ui.Warning, false}}},
		{"draft", prInfo("OPEN", prDraft), true, []piece{{iconPRDraft, ui.Muted, false}, {"#24", ui.Muted, false}, {"draft", ui.Muted, false}}},
		{"merged", prInfo("MERGED"), true, []piece{{iconMerge, ui.Accent, false}, {"#24", ui.Accent, false}, {"merged", ui.Accent, false}}},
		{"closed", prInfo("CLOSED"), true, []piece{{iconPRClosed, ui.Muted, false}, {"#24", ui.Muted, false}, {"closed", ui.Muted, false}}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			a.icons = tt.icons

			c := railCard{pane: "p1", state: core.AgentIdle, project: "api", branch: "feat/login", pr: tt.pr, hasPR: true}
			runs := a.railCardLine(ui, c, nil, 1, 40)

			for _, w := range tt.want {
				i := slices.IndexFunc(runs, func(r railRun) bool { return r.text == w.text })
				if i < 0 {
					t.Errorf("no run %q in %q", w.text, runsText(runs))

					continue
				}

				if r := runs[i]; r.fg != w.fg || r.bold != w.bold {
					t.Errorf("%q is %v bold %v, want %v bold %v", w.text, r.fg, r.bold, w.fg, w.bold)
				}
			}
		})
	}
}

// Short of room, the branch is cut to 8 cells, then the review goes, then the
// commits ahead, then the check counts; the number always stays.
func TestRailLineTwoDropOrder(t *testing.T) {
	t.Parallel()
	a := newTestApp(t)
	ui := a.current().theme.UI

	c := railCard{
		pane: "p1", state: core.AgentIdle, project: "api", branch: "feat/login-flow", ahead: 2,
		pr: prInfo("OPEN", checksRunning, prReview("APPROVED")), hasPR: true,
	}

	lead := "  " + iconGit + " "
	pr := iconPR + " #24"
	checks := iconRunning + " 3/5"

	tests := []struct {
		cw   int
		want string
	}{
		{37, lead + "feat/login-flow" + " " + "↑2  " + pr + " " + checks + " " + iconApproved},
		{36, lead + "feat/login-fl…" + " " + "↑2  " + pr + " " + checks + " " + iconApproved},
		{30, lead + "feat/lo…" + " " + "↑2  " + pr + " " + checks + " " + iconApproved},
		{29, lead + "feat/log…" + " " + "↑2  " + pr + " " + checks},
		{28, lead + "feat/lo…" + " " + "↑2  " + pr + " " + checks},
		{27, lead + "feat/login…" + " " + pr + " " + checks},
		{24, lead + "feat/lo…" + " " + pr + " " + checks},
		{23, lead + "feat/login…" + " " + pr + " " + iconRunning},
		{20, lead + "feat/lo…" + " " + pr + " " + iconRunning},
		{19, lead + "feat/log…" + " " + pr},
		{18, lead + "feat/lo…" + " " + pr},
		{17, lead + "feat/l…" + " " + pr},
		{12, lead + "f…" + " " + pr},
		{11, lead + "…" + " " + pr},
		{10, lead + " " + pr},
	}

	a.icons = true

	for _, tt := range tests {
		runs := a.railCardLine(ui, c, nil, 1, tt.cw)

		if got := runsText(runs); got != tt.want {
			t.Errorf("cw %d: line = %q, want %q", tt.cw, got, tt.want)
		}

		if w := runsWidth(runs); w > tt.cw {
			t.Errorf("cw %d: line is %d cells wide, want at most %d", tt.cw, w, tt.cw)
		}
	}
}

// A draft, merged or closed pull request gives up its word after the commits
// ahead, so the branch keeps its 8 cells while the icon and the number fit.
func TestRailLineTwoDropOrderOtherStates(t *testing.T) {
	t.Parallel()
	a := newTestApp(t)
	ui := a.current().theme.UI

	a.icons = true

	c := railCard{
		pane: "p1", state: core.AgentIdle, project: "api", branch: "feat/login-flow", ahead: 2,
		pr: prInfo("MERGED", checksPassed, prReview("APPROVED")), hasPR: true,
	}

	lead := "  " + iconGit + " "
	merged := iconMerge + " #24"

	tests := []struct {
		cw   int
		want string
	}{
		{36, lead + "feat/login-flow" + " " + "↑2  " + merged + " merged"},
		{35, lead + "feat/login-fl…" + " " + "↑2  " + merged + " merged"},
		{29, lead + "feat/lo…" + " " + "↑2  " + merged + " merged"},
		{28, lead + "feat/login…" + " " + merged + " merged"},
		{25, lead + "feat/lo…" + " " + merged + " merged"},
		{24, lead + "feat/login-fl…" + " " + merged},
		{18, lead + "feat/lo…" + " " + merged},
		{17, lead + "feat/l…" + " " + merged},
	}

	for _, tt := range tests {
		runs := a.railCardLine(ui, c, nil, 1, tt.cw)

		if got := runsText(runs); got != tt.want {
			t.Errorf("cw %d: line = %q, want %q", tt.cw, got, tt.want)
		}

		if w := runsWidth(runs); w > tt.cw {
			t.Errorf("cw %d: line is %d cells wide, want at most %d", tt.cw, w, tt.cw)
		}
	}
}

// The branch keeps 8 cells (or all of a shorter name) before anything else goes.
func TestRailLineTwoKeepsAShortBranch(t *testing.T) {
	t.Parallel()
	a := newTestApp(t)
	ui := a.current().theme.UI

	a.icons = true

	c := railCard{
		pane: "p1", state: core.AgentIdle, project: "api", branch: "main", ahead: 2,
		pr: prInfo("OPEN", checksRunning), hasPR: true,
	}

	lead := "  " + iconGit + " "
	pr := iconPR + " #24"
	checks := iconRunning + " 3/5"

	tests := []struct {
		cw   int
		want string
	}{
		{24, lead + "main" + " " + "↑2  " + pr + " " + checks},
		{23, lead + "main" + strings.Repeat(" ", 4) + pr + " " + checks},
	}

	for _, tt := range tests {
		if got := runsText(a.railCardLine(ui, c, nil, 1, tt.cw)); got != tt.want {
			t.Errorf("cw %d: line = %q, want %q", tt.cw, got, tt.want)
		}
	}
}

func TestRailLineTwoPRKeepsTheSearchHighlight(t *testing.T) {
	t.Parallel()
	a := newTestApp(t)
	ui := a.current().theme.UI

	c := railCard{
		pane: "p1", state: core.AgentIdle, project: "api", branch: "feat/login", ahead: 2,
		pr: prInfo("OPEN", checksRunning), hasPR: true,
	}

	hits, _ := c.matches("log")
	runs := a.railCardLine(ui, c, hits, 1, 40)

	var matched string

	for _, r := range runs {
		if r.fg == ui.Match {
			matched += r.text
		}
	}

	if matched != "log" {
		t.Errorf("highlighted %q, want log", matched)
	}
}

func TestRailCardsTakeTheirPRFromTheStore(t *testing.T) {
	t.Parallel()

	agents := []railAgent{
		{newTab: true, project: "api", branch: "feat/login", state: core.AgentIdle, pr: new(prInfo("OPEN", checksRunning))},
		{project: "web", branch: "feat/old", state: core.AgentIdle, pr: new(prInfo("MERGED"))},
		{project: "docs", branch: "main", state: core.AgentIdle},
		{project: "infra", branch: "feat/infra", state: core.AgentIdle, pr: new(prInfo("OPEN"))},
		{project: "ops", branch: "feat/ops", state: core.AgentIdle, pr: new(prInfo("OPEN"))},
		{project: "tools", branch: "a1b2c3d", detached: true, state: core.AgentIdle, pr: new(prInfo("OPEN"))},
		{project: "lint", branch: "feat/lint", state: core.AgentIdle, pr: new(prInfo("OPEN"))},
	}

	a := drawApp(t, "catppuccin-mocha", true, 100, 30, agents)

	// p4's repository is not on GitHub, p5's was resolved for another branch and
	// p7's latest resolution failed, so its answer is for an earlier branch.
	a.prs.panes["p4"].ok = false
	a.prs.panes["p5"].answered.branch = "feat/elsewhere"
	a.prs.panes["p7"].failed, a.prs.panes["p7"].answered.branch = true, "feat/old"

	byProject := map[string]railCard{}

	for _, g := range a.railCards(a.store.State()) {
		for _, c := range g.cards {
			byProject[c.project] = c
		}
	}

	tests := []struct {
		project string
		hasPR   bool
		state   string
	}{
		{"api", true, "OPEN"},
		{"web", true, "MERGED"},
		{"docs", false, ""},
		{"infra", false, ""},
		{"ops", false, ""},
		{"tools", false, ""},
		{"lint", false, ""},
	}

	for _, tt := range tests {
		c := byProject[tt.project]
		if c.hasPR != tt.hasPR || c.pr.State != tt.state {
			t.Errorf("%s: pr %q (found %v), want %q (found %v)", tt.project, c.pr.State, c.hasPR, tt.state, tt.hasPR)
		}
	}
}

// While a pane's move into another repository is still being resolved, its card
// and the tray show nothing of the repository it left, even for a branch of the
// same name; the new repository's PR shows once the answer arrives.
func TestPRWaitsForTheNewRepositoryAfterAMove(t *testing.T) {
	t.Parallel()

	repoB := github.Repo{Host: "github.com", Owner: "o", Name: "other"}

	tests := []struct {
		name     string
		seedA    func(t *testing.T, p *prApp)
		wantA    int
		trayShow bool // the PR is open, so the tray shows it
	}{
		{"an open PR", func(t *testing.T, p *prApp) { p.churn(t, p.step(snapshotOf(openPRIn(5, "x")))) }, 5, true},
		{"a looked-up merged PR", func(t *testing.T, p *prApp) {
			openIn(t, p.App, core.SetBranchPR{Repo: prRepoKey, Branch: "x", PR: core.PRInfo{Number: 5, State: "MERGED"}, Found: true})
		}, 5, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			p := newPRApp(t)
			p.prs.resolve = func(_ context.Context, cwd, _ string) (github.Repo, error) {
				if cwd == "/w/b" {
					return repoB, nil
				}

				return prRepo, nil
			}

			var id core.PaneID

			// The head probes are not run; their results are fed in by hand.
			head := func(id core.PaneID, cwd string) tea.Cmd {
				return p.step(railHeadMsg{pane: id, cwd: cwd, own: cwd, res: railProbe{known: true, repo: true, project: "p", branch: "x", headCwd: cwd}})
			}
			// check compares the card's PR number (0 for none) and the tray module.
			check := func(stage string, wantPR int, wantTray bool) {
				t.Helper()

				c, ok := p.card(id)
				if !ok {
					t.Fatalf("%s: no card", stage)
				}

				gotPR := 0
				if c.hasPR {
					gotPR = c.pr.Number
				}

				if gotPR != wantPR {
					t.Errorf("%s: the card shows PR #%d, want #%d", stage, gotPR, wantPR)
				}

				if _, shows := findPRModule(p.App); shows != wantTray {
					t.Errorf("%s: tray module shown = %v, want %v", stage, shows, wantTray)
				}
			}

			id = p.addAgent(t, false, "/w/a")
			p.settle()
			p.churn(t, head(id, "/w/a"))
			tt.seedA(t, p)
			check("in the first repository", tt.wantA, tt.trayShow)

			// The agent moves; its new head result is in and the resolution is not.
			hook(p.App, cwdHook(id, "/w/b"))

			held := head(id, "/w/b")

			check("while the new repository resolves", 0, false)

			p.churn(t, held)
			check("once the new repository answered, before its poll", 0, false)

			p.churn(t, p.step(prSnapshotMsg{Repo: repoB, PRs: []github.PR{openPRIn(9, "x")}}))
			check("after the new repository's poll", 9, true)
		})
	}
}
