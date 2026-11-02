package core

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

var planTime = time.Date(2026, 10, 30, 9, 0, 0, 0, time.UTC)

func filePlan(path, root string) Plan {
	return Plan{Path: path, Root: root, Kind: PlanFile, At: planTime}
}

func modePlan(path string) Plan {
	return Plan{Path: path, Kind: PlanMode, At: planTime, Unread: true}
}

func TestPlanValid(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		plan Plan
		want bool
	}{
		{"plan file", filePlan("/r/docs/superpowers/plans/a.md", "/r"), true},
		{"plan mode", modePlan("/home/me/.claude/plans/x.md"), true},
		{"root is the filesystem root", filePlan("/a.md", "/"), true},
		{"no path", Plan{Kind: PlanMode}, false},
		{"relative path", modePlan("plans/x.md"), false},
		{"uncleaned path", modePlan("/a/../b.md"), false},
		{"no kind", Plan{Path: "/a/b.md"}, false},
		{"file plan without root", filePlan("/r/a.md", ""), false},
		{"root does not hold the path", filePlan("/other/a.md", "/r"), false},
		{"root is a prefix of a sibling", filePlan("/rr/a.md", "/r"), false},
		{"root equals the path", filePlan("/r", "/r"), false},
		{"plan mode with a root", Plan{Path: "/a/b.md", Root: "/a", Kind: PlanMode}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if got := tt.plan.Valid(); got != tt.want {
				t.Errorf("%+v.Valid() = %v, want %v", tt.plan, got, tt.want)
			}
		})
	}
}

func planState() State {
	return agentState(map[PaneID]Pane{
		"p1": {Kind: KindAgent, Harness: "claude-code", Session: "s1"},
		"p2": {Kind: KindShell},
	}, &Node{Pane: "p1"}, "p1")
}

func TestNotePlan(t *testing.T) {
	t.Parallel()

	first, second := modePlan("/h/.claude/plans/a.md"), filePlan("/r/docs/plans/b.md", "/r")

	tests := []struct {
		name    string
		prior   Plan
		id      PaneID
		plan    Plan
		want    Plan
		wantErr bool
	}{
		{name: "sets an agent's plan", id: "p1", plan: first, want: first},
		{name: "a second plan replaces the first", prior: first, id: "p1", plan: second, want: second},
		{name: "a shell is a no-op", id: "p2", plan: first},
		{name: "an unknown pane is a no-op", id: "p9", plan: first},
		{name: "an invalid plan is an error", prior: first, id: "p1", plan: Plan{Path: "rel.md", Kind: PlanMode}, want: first, wantErr: true},
		{name: "a shell with an invalid plan is a no-op", id: "p2", plan: Plan{}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			s := planState()
			if tt.prior.Path != "" {
				p := s.Panes["p1"]
				p.Plan = tt.prior
				s.Panes["p1"] = p
			}

			st := NewStore(s)
			err := st.Dispatch(NotePlan{ID: tt.id, Plan: tt.plan})

			if (err != nil) != tt.wantErr {
				t.Fatalf("Dispatch error = %v, wantErr %v", err, tt.wantErr)
			}

			got := st.State()
			if got.Panes["p1"].Plan != tt.want {
				t.Errorf("p1 plan = %+v, want %+v", got.Panes["p1"].Plan, tt.want)
			}

			if _, ok := got.Panes["p9"]; ok {
				t.Error("a pane appeared")
			}

			if got.Panes["p2"].Plan != (Plan{}) {
				t.Errorf("the shell has a plan: %+v", got.Panes["p2"].Plan)
			}
		})
	}
}

func TestReadPlan(t *testing.T) {
	t.Parallel()

	s := planState()
	p := s.Panes["p1"]
	p.Plan = filePlan("/r/docs/plans/b.md", "/r")
	p.Plan.Unread = true
	s.Panes["p1"] = p

	got := dispatch(t, s, ReadPlan{ID: "p1"})

	want := filePlan("/r/docs/plans/b.md", "/r")
	if got.Panes["p1"].Plan != want {
		t.Errorf("plan = %+v, want %+v", got.Panes["p1"].Plan, want)
	}

	t.Run("no plan is a no-op", func(t *testing.T) {
		t.Parallel()

		got := dispatch(t, planState(), ReadPlan{ID: "p1"})
		if got.Panes["p1"].Plan != (Plan{}) {
			t.Errorf("plan = %+v, want none", got.Panes["p1"].Plan)
		}
	})

	t.Run("an unknown pane is a no-op", func(t *testing.T) {
		t.Parallel()

		dispatch(t, planState(), ReadPlan{ID: "p9"})
	})
}

func TestPlanLifetime(t *testing.T) {
	t.Parallel()

	plan := modePlan("/h/.claude/plans/a.md")

	withPlan := func() State {
		return dispatch(t, planState(), NotePlan{ID: "p1", Plan: plan})
	}

	tests := []struct {
		name string
		act  Action
		want Plan
	}{
		{"a new session keeps it", SetAgentSession{ID: "p1", Session: "s2"}, plan},
		{"an empty session keeps it", SetAgentSession{ID: "p1"}, plan},
		{"a new conversation drops it", SetAgentSession{ID: "p1", Session: "s2", NewConversation: true}, Plan{}},
		{"a new conversation in the same session drops it", SetAgentSession{ID: "p1", Session: "s1", NewConversation: true}, Plan{}},
		{"becoming a shell drops it", SetPaneKind{ID: "p1", Kind: KindShell}, Plan{}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got := dispatch(t, withPlan(), tt.act)
			if got.Panes["p1"].Plan != tt.want {
				t.Errorf("plan = %+v, want %+v", got.Panes["p1"].Plan, tt.want)
			}
		})
	}

	t.Run("a state copy keeps the old plan", func(t *testing.T) {
		t.Parallel()

		st := NewStore(planState())
		before := st.State()

		if err := st.Dispatch(NotePlan{ID: "p1", Plan: plan}); err != nil {
			t.Fatal(err)
		}

		if before.Panes["p1"].Plan != (Plan{}) {
			t.Errorf("the earlier copy changed: %+v", before.Panes["p1"].Plan)
		}
	})
}

func TestPlansConfig(t *testing.T) {
	t.Parallel()

	defaults := []string{"docs/superpowers/plans/", "docs/superpowers/specs/"}

	tests := []struct {
		name    string
		content string
		want    []string
		errHas  []string
	}{
		{name: "no section", want: defaults},
		{name: "one folder", content: "[plans]\npaths = [\"notes/plans/\"]\n", want: []string{"notes/plans/"}},
		{name: "none", content: "[plans]\npaths = []\n", want: []string{}},
		{name: "absolute", content: "[plans]\npaths = [\"/abs\"]\n", errHas: []string{"plans.paths", `"/abs"`}},
		{name: "dot", content: "[plans]\npaths = [\".\"]\n", errHas: []string{"plans.paths", `"."`}},
		{name: "leaves the root", content: "[plans]\npaths = [\"ok\", \"a/../../b\"]\n", errHas: []string{"plans.paths", `"a/../../b"`}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			path := filepath.Join(t.TempDir(), "config.toml")
			if err := os.WriteFile(path, []byte(tt.content), 0o600); err != nil {
				t.Fatal(err)
			}

			cfg, err := LoadConfig(path)
			if err == nil {
				err = cfg.Plans.Validate()
			}

			if tt.errHas != nil {
				if err == nil {
					t.Fatalf("got %+v, want an error", cfg.Plans)
				}

				for _, s := range tt.errHas {
					if !strings.Contains(err.Error(), s) {
						t.Errorf("error %q does not contain %q", err, s)
					}
				}

				return
			}

			if err != nil {
				t.Fatal(err)
			}

			if len(cfg.Plans.Paths) != len(tt.want) || (len(tt.want) > 0 && strings.Join(cfg.Plans.Paths, "|") != strings.Join(tt.want, "|")) {
				t.Errorf("paths = %q, want %q", cfg.Plans.Paths, tt.want)
			}
		})
	}
}
