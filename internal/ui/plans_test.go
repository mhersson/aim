package ui

import (
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mhersson/aim/internal/core"
	"github.com/mhersson/aim/internal/harness"
)

func TestPlanFileRoot(t *testing.T) {
	t.Parallel()

	def := core.DefaultConfig().Plans.Paths

	tests := []struct {
		name  string
		paths []string
		path  string
		root  string
		ok    bool
	}{
		{"plans", def, "/r/docs/superpowers/plans/a.md", "/r", true},
		{"specs, any case", def, "/r/.worktrees/x/docs/superpowers/specs/s.MD", "/r/.worktrees/x", true},
		{"nested", def, "/r/docs/superpowers/plans/sub/a.md", "/r", true},
		{"last occurrence", def, "/r/a/docs/superpowers/plans/b/docs/superpowers/plans/c.md", "/r/a/docs/superpowers/plans/b", true},
		{"not markdown", def, "/r/docs/superpowers/plans/a.go", "", false},
		{"folder name is a file", def, "/r/docs/superpowers/plans.md", "", false},
		{"folder prefix only", def, "/r/docs/superpowers/plansX/a.md", "", false},
		{"relative", def, "docs/superpowers/plans/a.md", "", false},
		{"custom folder", []string{"notes"}, "/r/notes/a.md", "/r", true},
		{"match at the path start", def, "/docs/superpowers/plans/a.md", "/", true},
		{"two folders, the deepest wins", []string{"docs", "docs/plans"}, "/r/docs/a/docs/plans/x.md", "/r/docs/a", true},
		{"two folders, the deeper one first", []string{"a", "b"}, "/r/b/x/a/f.md", "/r/b/x", true},
		{"no folders", nil, "/r/docs/superpowers/plans/a.md", "", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			root, ok := planFileRoot(tt.paths, tt.path)
			if root != tt.root || ok != tt.ok {
				t.Errorf("planFileRoot(%q, %q) = %q, %v; want %q, %v", tt.paths, tt.path, root, ok, tt.root, tt.ok)
			}
		})
	}
}

func TestHookNotesPlans(t *testing.T) {
	t.Parallel()

	tmp := t.TempDir()
	plan := filepath.Join(tmp, "docs", "superpowers", "plans", "p.md")
	other := filepath.Join(tmp, "docs", "superpowers", "specs", "q.md")
	modePath := "/home/u/.claude/plans/x.md"
	later := t0.Add(time.Hour)

	pre := func(p string) harness.Event { return harness.Event{Pane: "p2", Name: "PreToolUse", Plan: p} }
	post := func(p string) harness.Event { return harness.Event{Pane: "p2", Name: "PostToolUse", Edit: p} }
	file := func(p string, at time.Time) core.Plan {
		return core.Plan{Path: p, Root: tmp, Kind: core.PlanFile, At: at, Unread: true}
	}

	tests := []struct {
		name string
		pane core.PaneID
		prep []harness.Event // delivered at t0
		read bool            // ReadPlan after prep
		ev   harness.Event   // delivered at later
		want core.Plan
	}{
		{name: "plan mode", pane: "p2", ev: pre(modePath), want: core.Plan{Path: modePath, Kind: core.PlanMode, At: later, Unread: true}},
		{name: "plan mode, not markdown", pane: "p2", ev: pre("/home/u/.ssh/config")},
		{name: "write", pane: "p2", ev: post(plan), want: file(plan, later)},
		{name: "write not yet done", pane: "p2", ev: harness.Event{Pane: "p2", Name: "PreToolUse", Edit: plan, Before: true}},
		{name: "bash list", pane: "p2", ev: harness.Event{Pane: "p2", Name: "PostToolUse", Changed: []string{filepath.Join(tmp, "a.go"), plan}}, want: file(plan, later)},
		{name: "other file", pane: "p2", ev: post(filepath.Join(tmp, "README.md"))},
		{
			name: "rewrite of the mode plan", pane: "p2", prep: []harness.Event{pre(modePath)}, read: true, ev: post(modePath),
			want: core.Plan{Path: modePath, Kind: core.PlanMode, At: later, Unread: true},
		},
		{name: "file replaces mode", pane: "p2", prep: []harness.Event{pre(modePath)}, ev: post(plan), want: file(plan, later)},
		{name: "latest wins", pane: "p2", prep: []harness.Event{post(plan)}, ev: post(other), want: file(other, later)},
		{
			name: "subagent", pane: "p2", ev: harness.Event{Pane: "p2", Name: "PostToolUse", Edit: plan, Subagent: true},
			want: file(plan, later),
		},
		{name: "shell pane", pane: "p1", ev: post(plan)},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			a, _, clk := agentApp(t)
			a.plans = core.DefaultConfig().Plans

			for _, ev := range tt.prep {
				hook(a, ev)
			}

			if tt.read {
				if err := a.dispatch(core.ReadPlan{ID: "p2"}); err != nil {
					t.Fatal(err)
				}
			}

			clk.Set(later)

			ev := tt.ev
			ev.Pane = tt.pane
			hook(a, ev)

			// A prep event that recorded a plan carries the earlier time.
			if got := a.store.State().Panes[tt.pane].Plan; got != tt.want {
				t.Errorf("plan = %+v, want %+v", got, tt.want)
			}
		})
	}
}

// sessionStart is the SessionStart hook of pane p2 for session; an empty
// session is a payload without one.
func sessionStart(session core.SessionID, newConversation bool) harness.Event {
	return harness.Event{
		Pane: "p2", Name: "SessionStart", Session: session, State: core.AgentIdle,
		Start: session != "", NewConversation: newConversation,
	}
}

func TestNewConversationDropsPlan(t *testing.T) {
	t.Parallel()

	plan := core.Plan{Path: "/home/u/.claude/plans/x.md", Kind: core.PlanMode, At: t0, Unread: true}

	tests := []struct {
		name string
		ev   harness.Event
		want core.Plan
	}{
		{"clear", sessionStart("s2", true), core.Plan{}},
		{"resume", sessionStart("s1", false), plan},
		{"fork", sessionStart("s2", false), plan},
		{"no session", sessionStart("", true), plan},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			a, _, _ := agentApp(t)
			hook(a, sessionStart("s1", false))
			openIn(t, a, core.NotePlan{ID: "p2", Plan: plan})

			var saved core.State

			a.save = func(st core.State) { saved = st }

			hook(a, tt.ev)

			if got := a.store.State().Panes["p2"].Plan; got != tt.want {
				t.Fatalf("plan = %+v, want %+v", got, tt.want)
			}

			if tt.want.Path != "" {
				return
			}

			if p, ok := saved.Panes["p2"]; !ok || p.Plan != (core.Plan{}) {
				t.Errorf("saved pane = %+v, %v; want it saved with no plan", p, ok)
			}

			press(a, keyLeader, keyRune('p'))

			if want := fmt.Sprintf(noticeNoPlan, a.cardName(a.store.State(), "p2")); a.notice != want {
				t.Errorf("notice = %q, want %q", a.notice, want)
			}
		})
	}
}

// TestNewConversationKeepsOverlay: the open overlay keeps showing what it read
// when a new conversation drops its plan.
func TestNewConversationKeepsOverlay(t *testing.T) {
	t.Parallel()

	r, _ := openPlanRig(t, "# Keep the plan\n\nThe agent was cleared.\n")
	shown := r.a.plan.plan

	hook(r.a, sessionStart("s2", true))

	if got := r.a.store.State().Panes["p2"].Plan; got != (core.Plan{}) {
		t.Fatalf("plan = %+v, want none", got)
	}

	if r.a.plan == nil || r.a.plan.plan != shown {
		t.Fatalf("overlay = %+v, want it open on %+v", r.a.plan, shown)
	}

	for _, want := range []string{"docs/superpowers/plans/p.md", planKindWord(core.PlanFile) + " · 4m", "Keep the plan", "The agent was cleared."} {
		if !strings.Contains(r.shown(t), want) {
			t.Errorf("the overlay no longer shows %q", want)
		}
	}

	r.keys("esc")

	if msgs := planMsgs(r.ask()); len(msgs) != 0 || r.a.plan != nil {
		t.Errorf("leader p after the clear raised %d reads, overlay open %v", len(msgs), r.a.plan != nil)
	}
}
