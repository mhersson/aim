package core

import (
	"cmp"
	"fmt"
	"maps"
	"path"
	"reflect"
	"slices"
	"testing"
	"time"
)

func agentState(panes map[PaneID]Pane, layout *Node, focus PaneID) State {
	s := stateOf(layout, focus)
	maps.Copy(s.Panes, panes)

	return s
}

// samePane compares panes, with Edits in Root order: their order is not part of
// what a pane promises.
func samePane(a, b Pane) bool {
	byRoot := func(p Pane) Pane {
		p.Edits = slices.SortedFunc(slices.Values(p.Edits), func(x, y Edit) int { return cmp.Compare(x.Root, y.Root) })

		return p
	}

	return reflect.DeepEqual(byRoot(a), byRoot(b))
}

// editAt is an edit of root's worktree whose time follows its sequence number.
func editAt(root string, seq uint64) Edit {
	return Edit{Root: root, Common: root + "/.git", Repo: path.Base(root), Seq: seq, At: time.Unix(1_000+int64(seq), 0)}
}

func withAt(e Edit, at time.Time) Edit {
	e.At = at

	return e
}

// editsOf lists n edits of distinct roots, the first with the highest Seq.
func editsOf(n int) []Edit {
	out := make([]Edit, n)
	for i := range out {
		out[i] = editAt(fmt.Sprintf("/r%02d", i), uint64(10+n-1-i))
	}

	return out
}

func TestAgentStateString(t *testing.T) {
	if AgentUnknown != 0 {
		t.Error("AgentUnknown must be the zero value")
	}
}

func TestSetPaneKind(t *testing.T) {
	full := Pane{Kind: KindAgent, Cwd: "/w", Harness: "claude-code", Session: "s1", Transcript: "/t", State: AgentWorking, Action: "Edit a.go", AgentCwd: "/w/sub", Model: "Opus 4.8", Context: 42, ContextKnown: true, StatusSession: "s1"}

	tests := []struct {
		name string
		from Pane
		act  SetPaneKind
		want Pane
	}{
		{"shell to agent", Pane{Kind: KindShell, Cwd: "/w"}, SetPaneKind{ID: "p1", Kind: KindAgent, Harness: "claude-code"}, Pane{Kind: KindAgent, Cwd: "/w", Harness: "claude-code"}},
		{"agent to shell clears agent fields", full, SetPaneKind{ID: "p1", Kind: KindShell}, Pane{Kind: KindShell, Cwd: "/w"}},
		{"agent to shell ignores harness", full, SetPaneKind{ID: "p1", Kind: KindShell, Harness: "x"}, Pane{Kind: KindShell, Cwd: "/w"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := dispatch(t, agentState(map[PaneID]Pane{"p1": tt.from}, leaf("p1"), "p1"), tt.act)
			if !samePane(got.Panes["p1"], tt.want) {
				t.Errorf("pane = %+v, want %+v", got.Panes["p1"], tt.want)
			}
		})
	}
}

func TestAgentActionErrors(t *testing.T) {
	base := func() State {
		return agentState(map[PaneID]Pane{
			"p1": {Kind: KindShell},
			"p2": {Kind: KindAgent, Harness: "claude-code"},
		}, vsplit(leaf("p1"), leaf("p2")), "p1")
	}

	tests := []struct {
		name string
		act  Action
	}{
		{"kind of unknown pane", SetPaneKind{ID: "p9", Kind: KindShell}},
		{"agent without harness", SetPaneKind{ID: "p1", Kind: KindAgent}},
		{"unknown kind", SetPaneKind{ID: "p1", Kind: "x"}},
		{"session of unknown pane", SetAgentSession{ID: "p9", Session: "s"}},
		{"session of shell pane", SetAgentSession{ID: "p1", Session: "s"}},
		{"state of unknown pane", SetAgentState{ID: "p9", State: AgentIdle}},
		{"state out of range", SetAgentState{ID: "p2", State: AgentState(99)}},
		{"state of shell pane", SetAgentState{ID: "p1", State: AgentIdle}},
		{"action of unknown pane", SetAgentAction{ID: "p9", Action: "x"}},
		{"action of shell pane", SetAgentAction{ID: "p1", Action: "x"}},
		{"cwd of unknown pane", SetAgentCwd{ID: "p9", Cwd: "/x"}},
		{"cwd of shell pane", SetAgentCwd{ID: "p1", Cwd: "/x"}},
		{"status of unknown pane", SetAgentStatus{ID: "p9", Session: "s", Model: "Opus 4.8"}},
		{"status of shell pane", SetAgentStatus{ID: "p1", Session: "s", Model: "Opus 4.8"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			st := NewStore(base())
			if err := st.Dispatch(tt.act); err == nil {
				t.Fatal("want error")
			}

			want := base()
			for id, p := range want.Panes {
				if !samePane(st.State().Panes[id], p) {
					t.Errorf("pane %s changed: %+v", id, st.State().Panes[id])
				}
			}
		})
	}
}

func TestSetAgentAction(t *testing.T) {
	s := agentState(map[PaneID]Pane{"p1": {Kind: KindAgent, Harness: "claude-code"}}, leaf("p1"), "p1")

	s = dispatch(t, s, SetAgentAction{ID: "p1", Action: "Edit a.go"})
	if got := s.Panes["p1"].Action; got != "Edit a.go" {
		t.Fatalf("action = %q", got)
	}

	s = dispatch(t, s, SetAgentState{ID: "p1", State: AgentWorking})
	if got := s.Panes["p1"].Action; got != "Edit a.go" {
		t.Errorf("a state change cleared the action: %q", got)
	}

	s = dispatch(t, s, SetAgentAction{ID: "p1"})
	if got := s.Panes["p1"].Action; got != "" {
		t.Errorf("empty action did not clear: %q", got)
	}

	s = dispatch(t, s, SetAgentAction{ID: "p1", Action: "Bash ls"})
	s = dispatch(t, s, SetPaneKind{ID: "p1", Kind: KindShell})

	if got := s.Panes["p1"].Action; got != "" {
		t.Errorf("a shell pane kept the action: %q", got)
	}
}

func TestSetAgentCwd(t *testing.T) {
	s := agentState(map[PaneID]Pane{"p1": {Kind: KindAgent, Cwd: "/w", Harness: "claude-code"}}, leaf("p1"), "p1")

	tests := []struct {
		name    string
		cwd     string
		want    string
		wantDir string
	}{
		{"set", "/w/sub", "/w/sub", "/w/sub"},
		{"overwrite", "/other", "/other", "/other"},
		{"clear", "", "", "/w"},
	}
	for _, tt := range tests {
		s = dispatch(t, s, SetAgentCwd{ID: "p1", Cwd: tt.cwd})

		p := s.Panes["p1"]
		if p.AgentCwd != tt.want || p.Cwd != "/w" || p.WorkDir() != tt.wantDir {
			t.Errorf("%s: pane = %+v, WorkDir = %q; want AgentCwd %q, Cwd /w, WorkDir %q", tt.name, p, p.WorkDir(), tt.want, tt.wantDir)
		}
	}
}

func TestPaneWorkDir(t *testing.T) {
	tests := []struct {
		name string
		pane Pane
		want string
	}{
		{"agent cwd without cwd", Pane{AgentCwd: "/w/sub"}, "/w/sub"},
		{"neither", Pane{}, ""},
	}
	for _, tt := range tests {
		if got := tt.pane.WorkDir(); got != tt.want {
			t.Errorf("%s: WorkDir() = %q, want %q", tt.name, got, tt.want)
		}
	}
}

func TestSetAgentSessionAndState(t *testing.T) {
	s := agentState(map[PaneID]Pane{"p1": {Kind: KindAgent, Harness: "claude-code"}}, leaf("p1"), "p1")
	s = dispatch(t, s, SetAgentSession{ID: "p1", Session: "s1", Transcript: "/t.jsonl"})

	s = dispatch(t, s, SetAgentState{ID: "p1", State: AgentBlocked})
	if p := s.Panes["p1"]; p.Session != "s1" || p.Transcript != "/t.jsonl" || p.State != AgentBlocked {
		t.Fatalf("pane = %+v", p)
	}

	s = dispatch(t, s, SetAgentState{ID: "p1", State: AgentUnknown})
	if s.Panes["p1"].State != AgentUnknown {
		t.Error("AgentUnknown did not clear the state")
	}

	s = dispatch(t, s, SetAgentSession{ID: "p1"})
	if p := s.Panes["p1"]; p.Session != "" || p.Transcript != "" {
		t.Errorf("empty session did not clear: %+v", p)
	}
}

func TestTabAgentState(t *testing.T) {
	ag := func(st AgentState) Pane { return Pane{Kind: KindAgent, Harness: "claude-code", State: st} }

	tests := []struct {
		name   string
		panes  map[PaneID]Pane
		layout *Node
		want   AgentState
	}{
		{"no agents", map[PaneID]Pane{"p1": {Kind: KindShell}}, leaf("p1"), AgentUnknown},
		{"idle", map[PaneID]Pane{"p1": ag(AgentIdle), "p2": {Kind: KindShell}}, vsplit(leaf("p1"), leaf("p2")), AgentIdle},
		{"working over idle", map[PaneID]Pane{"p1": ag(AgentIdle), "p2": ag(AgentWorking)}, vsplit(leaf("p1"), leaf("p2")), AgentWorking},
		{"blocked over working", map[PaneID]Pane{"p1": ag(AgentWorking), "p2": ag(AgentBlocked), "p3": ag(AgentIdle)}, vsplit(leaf("p1"), hsplit(leaf("p2"), leaf("p3"))), AgentBlocked},
		{"unknown agent", map[PaneID]Pane{"p1": ag(AgentUnknown)}, leaf("p1"), AgentUnknown},
		{"shell state ignored", map[PaneID]Pane{"p1": {Kind: KindShell, State: AgentBlocked}}, leaf("p1"), AgentUnknown},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := agentState(tt.panes, tt.layout, "p1")
			if got := s.TabAgentState(0); got != tt.want {
				t.Errorf("TabAgentState(0) = %v, want %v", got, tt.want)
			}
		})
	}

	s := agentState(map[PaneID]Pane{"p1": ag(AgentBlocked)}, leaf("p1"), "p1")
	if got := s.TabAgentState(5); got != AgentUnknown {
		t.Errorf("out of range = %v", got)
	}

	if got := s.TabAgentState(-1); got != AgentUnknown {
		t.Errorf("negative = %v", got)
	}
}

func TestAgentPanes(t *testing.T) {
	s := agentState(map[PaneID]Pane{
		"p1": {Kind: KindShell},
		"p2": {Kind: KindAgent, Harness: "claude-code"},
		"p3": {Kind: KindAgent, Harness: "claude-code"},
		"p4": {Kind: KindAgent, Harness: "claude-code"},
		"p5": {Kind: KindShell},
	}, vsplit(hsplit(leaf("p3"), leaf("p1")), leaf("p2")), "p3")
	t2 := Tab{ID: "t2", Focus: "p4"}
	setLayout(&t2, hsplit(leaf("p5"), leaf("p4")))
	s.Tabs = append(s.Tabs, t2)
	got := s.AgentPanes()

	want := []PaneID{"p3", "p2", "p4"}
	if len(got) != len(want) {
		t.Fatalf("AgentPanes() = %v, want %v", got, want)
	}

	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("AgentPanes() = %v, want %v", got, want)
		}
	}
}

func TestSetAgentStatus(t *testing.T) {
	reported := Pane{Kind: KindAgent, Harness: "claude-code", Session: "s1", Transcript: "/t", State: AgentWorking, Action: "Edit a.go", Model: "Opus 4.8", Context: 55, ContextKnown: true, StatusSession: "s1"}

	tests := []struct {
		name    string
		from    Pane
		act     SetAgentStatus
		want    Pane
		wantErr bool
	}{
		{
			name: "set",
			from: Pane{Kind: KindAgent, Harness: "claude-code"},
			act:  SetAgentStatus{Session: "s1", Model: "Opus 4.8", Context: 42, ContextKnown: true},
			want: Pane{Kind: KindAgent, Harness: "claude-code", Model: "Opus 4.8", Context: 42, ContextKnown: true, StatusSession: "s1"},
		},
		{
			name: "69.9 floors to 69",
			from: Pane{Kind: KindAgent, Harness: "claude-code"},
			act:  SetAgentStatus{Session: "s1", Model: "Opus 4.8", Context: 69.9, ContextKnown: true},
			want: Pane{Kind: KindAgent, Harness: "claude-code", Model: "Opus 4.8", Context: 69, ContextKnown: true, StatusSession: "s1"},
		},
		{
			name: "a null percentage keeps the model and clears the meter",
			from: reported,
			act:  SetAgentStatus{Session: "s1", Model: "Opus 4.8", Context: 77},
			want: Pane{Kind: KindAgent, Harness: "claude-code", Session: "s1", Transcript: "/t", State: AgentWorking, Action: "Edit a.go", Model: "Opus 4.8", StatusSession: "s1"},
		},
		{
			name: "a new model and percentage replace the old",
			from: reported,
			act:  SetAgentStatus{Session: "s1", Model: "Sonnet 5.5", Context: 91.2, ContextKnown: true},
			want: Pane{Kind: KindAgent, Harness: "claude-code", Session: "s1", Transcript: "/t", State: AgentWorking, Action: "Edit a.go", Model: "Sonnet 5.5", Context: 91, ContextKnown: true, StatusSession: "s1"},
		},
		{
			name:    "a shell pane errors",
			from:    Pane{Kind: KindShell, Cwd: "/w"},
			act:     SetAgentStatus{Session: "s1", Model: "Opus 4.8", Context: 42, ContextKnown: true},
			want:    Pane{Kind: KindShell, Cwd: "/w"},
			wantErr: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tt.act.ID = "p1"
			st := NewStore(agentState(map[PaneID]Pane{"p1": tt.from}, leaf("p1"), "p1"))

			err := st.Dispatch(tt.act)
			if (err != nil) != tt.wantErr {
				t.Fatalf("Dispatch() error = %v, wantErr %v", err, tt.wantErr)
			}

			if got := st.State().Panes["p1"]; !samePane(got, tt.want) {
				t.Errorf("pane = %+v, want %+v", got, tt.want)
			}
		})
	}
}

func TestSessionChangeClearsStatus(t *testing.T) {
	reported := func(session SessionID) Pane {
		return Pane{Kind: KindAgent, Harness: "claude-code", Session: session, Transcript: "/t", Model: "Opus 4.8", Context: 40, ContextKnown: true, StatusSession: "s1"}
	}

	tests := []struct {
		name string
		from Pane
		act  SetAgentSession
		want Pane
	}{
		{
			name: "a new session clears it",
			from: reported("s1"),
			act:  SetAgentSession{Session: "s2", Transcript: "/t2"},
			want: Pane{Kind: KindAgent, Harness: "claude-code", Session: "s2", Transcript: "/t2"},
		},
		{
			name: "the same session keeps it",
			from: reported("s1"),
			act:  SetAgentSession{Session: "s1", Transcript: "/t"},
			want: reported("s1"),
		},
		{
			name: "an end clears it",
			from: reported("s1"),
			act:  SetAgentSession{},
			want: Pane{Kind: KindAgent, Harness: "claude-code"},
		},
		{
			name: "an end clears a status that came before any session",
			from: Pane{Kind: KindAgent, Harness: "claude-code", Model: "Opus 4.8", Context: 40, ContextKnown: true, StatusSession: "s1"},
			act:  SetAgentSession{},
			want: Pane{Kind: KindAgent, Harness: "claude-code"},
		},
		{
			name: "a status that already carries the new session survives its SessionStart",
			from: Pane{Kind: KindAgent, Harness: "claude-code", Model: "Opus 4.8", Context: 40, ContextKnown: true, StatusSession: "s1"},
			act:  SetAgentSession{Session: "s1", Transcript: "/t"},
			want: reported("s1"),
		},
		{
			name: "a status from another session does not survive",
			from: Pane{Kind: KindAgent, Harness: "claude-code", Model: "Opus 4.8", Context: 40, ContextKnown: true, StatusSession: "s9"},
			act:  SetAgentSession{Session: "s1", Transcript: "/t"},
			want: Pane{Kind: KindAgent, Harness: "claude-code", Session: "s1", Transcript: "/t"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tt.act.ID = "p1"

			got := dispatch(t, agentState(map[PaneID]Pane{"p1": tt.from}, leaf("p1"), "p1"), tt.act)
			if !samePane(got.Panes["p1"], tt.want) {
				t.Errorf("pane = %+v, want %+v", got.Panes["p1"], tt.want)
			}
		})
	}
}

func TestSetAgentStatusMatches(t *testing.T) {
	p := Pane{Kind: KindAgent, Model: "Opus 4.8", Context: 69, ContextKnown: true, StatusSession: "s1"}

	tests := []struct {
		name string
		pane Pane
		act  SetAgentStatus
		want bool
	}{
		{"identical", p, SetAgentStatus{Session: "s1", Model: "Opus 4.8", Context: 69, ContextKnown: true}, true},
		{"same floor", p, SetAgentStatus{Session: "s1", Model: "Opus 4.8", Context: 69.9, ContextKnown: true}, true},
		{"other floor", p, SetAgentStatus{Session: "s1", Model: "Opus 4.8", Context: 70, ContextKnown: true}, false},
		{"same clamp", Pane{Kind: KindAgent, Model: "Opus 4.8", Context: 100, ContextKnown: true, StatusSession: "s1"}, SetAgentStatus{Session: "s1", Model: "Opus 4.8", Context: 130, ContextKnown: true}, true},
		{"other model", p, SetAgentStatus{Session: "s1", Model: "Sonnet 5.5", Context: 69, ContextKnown: true}, false},
		{"other session", p, SetAgentStatus{Session: "s2", Model: "Opus 4.8", Context: 69, ContextKnown: true}, false},
		{"meter cleared", p, SetAgentStatus{Session: "s1", Model: "Opus 4.8"}, false},
		{"a null percentage ignores the number", Pane{Kind: KindAgent, Model: "Opus 4.8", StatusSession: "s1"}, SetAgentStatus{Session: "s1", Model: "Opus 4.8", Context: 33}, true},
		{"nothing yet", Pane{Kind: KindAgent}, SetAgentStatus{Session: "s1", Model: "Opus 4.8"}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.act.Matches(tt.pane); got != tt.want {
				t.Errorf("Matches() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestNoteEdit(t *testing.T) {
	t.Parallel()

	agent := func(edits ...Edit) Pane {
		return Pane{Kind: KindAgent, Harness: "claude-code", Cwd: "/w", Session: "s1", Edits: edits}
	}

	late := editAt("/a", 99).At
	full := editsOf(MaxEdits)

	swapped := slices.Clone(full)
	swapped[3] = editAt(full[3].Root, 100)

	tests := []struct {
		name string
		from Pane
		act  NoteEdit
		want Pane
	}{
		{
			name: "first edit",
			from: agent(),
			act:  NoteEdit{Session: "s1", Edit: editAt("/a", 3)},
			want: agent(editAt("/a", 3)),
		},
		{
			name: "a second root joins the first",
			from: agent(editAt("/a", 3)),
			act:  NoteEdit{Session: "s1", Edit: editAt("/b", 4)},
			want: agent(editAt("/a", 3), editAt("/b", 4)),
		},
		{
			name: "same root, higher Seq",
			from: agent(editAt("/a", 2)),
			act:  NoteEdit{Session: "s1", Edit: editAt("/a", 7)},
			want: agent(editAt("/a", 7)),
		},
		{
			name: "same root, lower Seq keeps the higher",
			from: agent(editAt("/a", 7)),
			act:  NoteEdit{Session: "s1", Edit: editAt("/a", 2)},
			want: agent(editAt("/a", 7)),
		},
		{
			name: "same root, lower Seq but later At keeps the Seq and takes the At",
			from: agent(editAt("/a", 7)),
			act:  NoteEdit{Session: "s1", Edit: withAt(editAt("/a", 2), late)},
			want: agent(withAt(editAt("/a", 7), late)),
		},
		{
			name: "same root, higher Seq but earlier At takes the Seq and keeps the At",
			from: agent(withAt(editAt("/a", 2), late)),
			act:  NoteEdit{Session: "s1", Edit: editAt("/a", 7)},
			want: agent(withAt(editAt("/a", 7), late)),
		},
		{
			name: "another session is ignored",
			from: agent(editAt("/a", 3)),
			act:  NoteEdit{Session: "s2", Edit: editAt("/b", 4)},
			want: agent(editAt("/a", 3)),
		},
		{
			name: "a shell pane is ignored",
			from: Pane{Kind: KindShell, Cwd: "/w"},
			act:  NoteEdit{Session: "s1", Edit: editAt("/b", 4)},
			want: Pane{Kind: KindShell, Cwd: "/w"},
		},
		{
			name: "the 32nd root fits",
			from: agent(full[:MaxEdits-1]...),
			act:  NoteEdit{Session: "s1", Edit: full[MaxEdits-1]},
			want: agent(full...),
		},
		{
			name: "the 33rd root drops the lowest Seq",
			from: agent(full...),
			act:  NoteEdit{Session: "s1", Edit: editAt("/new", 100)},
			want: agent(append(slices.Clone(full[:MaxEdits-1]), editAt("/new", 100))...),
		},
		{
			name: "the 33rd root drops itself when it has the lowest Seq",
			from: agent(full...),
			act:  NoteEdit{Session: "s1", Edit: editAt("/new", 1)},
			want: agent(full...),
		},
		{
			name: "a kept root at the limit drops nothing",
			from: agent(full...),
			act:  NoteEdit{Session: "s1", Edit: editAt(full[3].Root, 100)},
			want: agent(swapped...),
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			tt.act.ID = "p1"

			got := dispatch(t, agentState(map[PaneID]Pane{"p1": tt.from}, leaf("p1"), "p1"), tt.act)
			if !samePane(got.Panes["p1"], tt.want) {
				t.Errorf("pane = %+v, want %+v", got.Panes["p1"], tt.want)
			}
		})
	}
}

func TestNoteEditUnknownPaneChangesNothing(t *testing.T) {
	t.Parallel()

	s := agentState(map[PaneID]Pane{"p1": {Kind: KindAgent, Harness: "claude-code", Session: "s1"}}, leaf("p1"), "p1")

	got := dispatch(t, s, NoteEdit{ID: "p9", Session: "s1", Edit: editAt("/a", 1)})
	if len(got.Panes) != 1 || len(got.Panes["p1"].Edits) != 0 {
		t.Errorf("panes = %+v", got.Panes)
	}
}

func TestFollowDir(t *testing.T) {
	t.Parallel()

	agent := Pane{Kind: KindAgent, Harness: "claude-code", Cwd: "/w", Session: "s1"}
	note := func(root string, seq uint64) Action {
		return NoteEdit{ID: "p1", Session: "s1", Edit: editAt(root, seq)}
	}

	tests := []struct {
		name  string
		from  Pane
		steps []Action
		want  string
	}{
		{"no edits gives Cwd", agent, nil, "/w"},
		{"no edits gives AgentCwd over Cwd", agent, []Action{SetAgentCwd{ID: "p1", Cwd: "/w/sub"}}, "/w/sub"},
		{"an edit gives its root", agent, []Action{note("/a", 1)}, "/a"},
		{"two edits give the higher Seq's root", agent, []Action{note("/a", 1), note("/b", 2)}, "/b"},
		{"two edits arriving out of order give the higher Seq's root", agent, []Action{note("/b", 2), note("/a", 1)}, "/b"},
		{"an edit in the own directory's root after another moves back", agent, []Action{note("/a", 1), note("/w", 2)}, "/w"},
		{"an AgentCwd change after an edit leaves it alone", agent, []Action{note("/a", 1), SetAgentCwd{ID: "p1", Cwd: "/w/sub"}}, "/a"},
		{"AgentCwd changes before any edit move it", agent, []Action{SetAgentCwd{ID: "p1", Cwd: "/x"}, SetAgentCwd{ID: "p1", Cwd: "/y"}}, "/y"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			s := agentState(map[PaneID]Pane{"p1": tt.from}, leaf("p1"), "p1")
			for _, a := range tt.steps {
				s = dispatch(t, s, a)
			}

			if got := s.Panes["p1"].FollowDir(); got != tt.want {
				t.Errorf("FollowDir() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestPaneFollowed(t *testing.T) {
	t.Parallel()

	if _, ok := (Pane{}).Followed(); ok {
		t.Error("a pane with no edits followed one")
	}

	p := Pane{Edits: []Edit{editAt("/a", 5), editAt("/b", 9), editAt("/c", 7)}}

	got, ok := p.Followed()
	if !ok || got.Root != "/b" || got.Seq != 9 {
		t.Errorf("Followed() = %+v, %v; want the edit with Seq 9", got, ok)
	}
}

func TestForgetEdit(t *testing.T) {
	t.Parallel()

	from := Pane{Kind: KindAgent, Harness: "claude-code", Cwd: "/w", AgentCwd: "/w/sub", Session: "s1", Edits: slices.Grow([]Edit{editAt("/a", 1), editAt("/b", 2)}, 4)}
	forget := func(root string) Action { return ForgetEdit{ID: "p1", Root: root} }

	t.Run("forgetting the latest falls back to the next, then to the working directory", func(t *testing.T) {
		t.Parallel()

		s := agentState(map[PaneID]Pane{"p1": from}, leaf("p1"), "p1")

		for _, step := range []struct{ root, want string }{{"/b", "/a"}, {"/a", "/w/sub"}} {
			s = dispatch(t, s, forget(step.root))
			if got := s.Panes["p1"].FollowDir(); got != step.want {
				t.Fatalf("after forgetting %s: FollowDir() = %q, want %q", step.root, got, step.want)
			}
		}

		if len(s.Panes["p1"].Edits) != 0 {
			t.Errorf("Edits = %+v", s.Panes["p1"].Edits)
		}
	})

	t.Run("a root that was never edited changes nothing", func(t *testing.T) {
		t.Parallel()

		s := dispatch(t, agentState(map[PaneID]Pane{"p1": from}, leaf("p1"), "p1"), forget("/zzz"))
		if !samePane(s.Panes["p1"], from) {
			t.Errorf("pane = %+v, want %+v", s.Panes["p1"], from)
		}
	})

	t.Run("a pane that is gone or a shell changes nothing", func(t *testing.T) {
		t.Parallel()

		s := agentState(map[PaneID]Pane{"p1": {Kind: KindShell, Cwd: "/w"}}, leaf("p1"), "p1")
		s = dispatch(t, s, ForgetEdit{ID: "p9", Root: "/a"})
		s = dispatch(t, s, ForgetEdit{ID: "p1", Root: "/a"})

		if !samePane(s.Panes["p1"], Pane{Kind: KindShell, Cwd: "/w"}) {
			t.Errorf("pane = %+v", s.Panes["p1"])
		}
	})

	t.Run("a state taken before keeps its edits", func(t *testing.T) {
		t.Parallel()

		st := NewStore(agentState(map[PaneID]Pane{"p1": from}, leaf("p1"), "p1"))
		before := st.State()
		want := slices.Clone(before.Panes["p1"].Edits)

		if err := st.Dispatch(forget("/a")); err != nil {
			t.Fatal(err)
		}

		if got := before.Panes["p1"].Edits; !slices.Equal(got, want) {
			t.Errorf("Edits = %+v, want %+v", got, want)
		}
	})
}

func TestSessionChangeClearsEdits(t *testing.T) {
	t.Parallel()

	edited := func(session SessionID) Pane {
		return Pane{Kind: KindAgent, Harness: "claude-code", Cwd: "/w", Session: session, Transcript: "/t", Edits: []Edit{editAt("/a", 1), editAt("/b", 2)}}
	}

	tests := []struct {
		name string
		act  SetAgentSession
		want Pane
	}{
		{
			name: "a new session clears them",
			act:  SetAgentSession{Session: "s2", Transcript: "/t2"},
			want: Pane{Kind: KindAgent, Harness: "claude-code", Cwd: "/w", Session: "s2", Transcript: "/t2"},
		},
		{
			name: "the same session keeps them",
			act:  SetAgentSession{Session: "s1", Transcript: "/t"},
			want: edited("s1"),
		},
		{
			name: "an end clears them",
			act:  SetAgentSession{},
			want: Pane{Kind: KindAgent, Harness: "claude-code", Cwd: "/w"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			tt.act.ID = "p1"

			got := dispatch(t, agentState(map[PaneID]Pane{"p1": edited("s1")}, leaf("p1"), "p1"), tt.act)
			if !samePane(got.Panes["p1"], tt.want) {
				t.Errorf("pane = %+v, want %+v", got.Panes["p1"], tt.want)
			}
		})
	}
}

func TestNoteEditCopiesOnWrite(t *testing.T) {
	t.Parallel()

	from := Pane{Kind: KindAgent, Harness: "claude-code", Session: "s1", Edits: slices.Grow([]Edit{editAt("/a", 1), editAt("/b", 2)}, 4)}

	full := Pane{Kind: KindAgent, Harness: "claude-code", Session: "s1", Edits: slices.Grow(editsOf(MaxEdits), 4)}

	tests := []struct {
		name string
		from Pane
		edit Edit
	}{
		{"a new root", from, editAt("/c", 3)},
		{"a higher Seq for a known root", from, editAt("/a", 3)},
		{"a new root past the limit", full, editAt("/new", 100)},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			st := NewStore(agentState(map[PaneID]Pane{"p1": tt.from}, leaf("p1"), "p1"))
			before := st.State()
			want := slices.Clone(before.Panes["p1"].Edits)

			if err := st.Dispatch(NoteEdit{ID: "p1", Session: "s1", Edit: tt.edit}); err != nil {
				t.Fatal(err)
			}

			if got := before.Panes["p1"].Edits; !slices.Equal(got, want) {
				t.Errorf("Edits = %+v, want %+v", got, want)
			}
		})
	}
}
