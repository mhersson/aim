package ui

import (
	"path/filepath"
	"reflect"
	"slices"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/mhersson/aim/internal/core"
	"github.com/mhersson/aim/internal/harness"
)

// tickRec records scheduled ticks with their delays and returns their messages at once.
type tickRec struct{ delays []time.Duration }

func (r *tickRec) tick(d time.Duration, fn func(time.Time) tea.Msg) tea.Cmd {
	r.delays = append(r.delays, d)
	msg := fn(t0)

	return func() tea.Msg { return msg }
}

// railApp is a sized app with a recording tick and a fake git in fake.
type railApp struct {
	*App
	ticks *tickRec
	clk   *fakeClock
	fake  *fakeGit
}

func newRailApp(t *testing.T, stats bool, opts ...func(*Options)) *railApp {
	t.Helper()

	f := newFakeGit(t)
	tmp := ""

	if stats {
		tmp = t.TempDir()
	}

	// No watch set: the rail's tests count the probes alone; the watch tests set their own.
	all := append([]func(*Options){gitOf(f), func(o *Options) { o.Watches, o.GitTempDir = nil, tmp }}, opts...)
	a, _, clk := kindApp(t, all...)
	r := &railApp{App: a, clk: clk, ticks: &tickRec{}, fake: f}
	a.tick = r.ticks.tick

	return r
}

// gitCalls counts the git calls the fake has logged.
func (r *railApp) gitCalls() int {
	n := 0

	r.fake.edit(func() { n = len(r.fake.log) })

	return n
}

// step delivers a message and returns the commands the update raised.
func (r *railApp) step(msg tea.Msg) tea.Cmd {
	_, cmd := r.Update(msg)

	return cmd
}

// settle runs the after-update work for state changed outside Update.
func (r *railApp) settle() tea.Cmd { return r.step(struct{}{}) }

// deliver runs cmd, feeds every message back and returns the messages it ran.
func (r *railApp) deliver(cmd tea.Cmd) []tea.Msg {
	msgs := flatten(cmd)
	for _, m := range msgs {
		if m != nil {
			r.step(m)
		}
	}

	return msgs
}

func countOf[T any](msgs []tea.Msg) int {
	n := 0

	for _, m := range msgs {
		if _, ok := m.(T); ok {
			n++
		}
	}

	return n
}

func (r *railApp) addAgent(t *testing.T, newTab bool, cwd string) core.PaneID {
	t.Helper()

	id := r.store.State().NewPaneID()
	if newTab {
		openIn(t, r.App, core.NewTab{Kind: core.KindAgent, Cwd: cwd})
	} else {
		openIn(t, r.App, core.OpenPane{Kind: core.KindAgent, Cwd: cwd, Split: core.SplitVertical, Harness: "claude-code", Area: r.bodyArea()})
	}

	return id
}

func (r *railApp) setState(t *testing.T, id core.PaneID, s core.AgentState) {
	t.Helper()
	openIn(t, r.App, core.SetAgentState{ID: id, State: s})
}

func (r *railApp) card(id core.PaneID) (railCard, bool) { return railCardOf(r.App, id) }

func railCardOf(a *App, id core.PaneID) (railCard, bool) {
	for _, c := range a.railShown(a.store.State()) {
		if c.pane == id {
			return c, true
		}
	}

	return railCard{}, false
}

func ids(gs []railGroup) [][]core.PaneID {
	var out [][]core.PaneID

	for _, g := range gs {
		var row []core.PaneID
		for _, c := range g.cards {
			row = append(row, c.pane)
		}

		out = append(out, row)
	}

	return out
}

func TestRailCardsGroupsAndOrder(t *testing.T) {
	t.Parallel()

	r := newRailApp(t, false)
	dir := t.TempDir()

	idle := r.addAgent(t, false, dir)
	blocked := r.addAgent(t, false, dir)
	blocked1b := r.addAgent(t, false, dir)
	openIn(t, r.App, core.RenameTab{ID: r.store.State().Tab().ID, Name: "Backend"})

	working := r.addAgent(t, true, dir)
	nohooks := r.addAgent(t, false, dir)
	blocked2 := r.addAgent(t, true, dir)
	shell := r.store.State().NewPaneID()
	openIn(t, r.App, core.OpenPane{Kind: core.KindShell, Cwd: dir, Split: core.SplitVertical, Area: r.bodyArea()})

	r.setState(t, idle, core.AgentIdle)
	r.setState(t, blocked, core.AgentBlocked)
	r.setState(t, blocked1b, core.AgentBlocked)
	r.setState(t, working, core.AgentWorking)
	r.setState(t, blocked2, core.AgentBlocked)
	openIn(t, r.App, core.SetAgentAction{ID: blocked, Action: "Allow Bash ls?"}, core.SetAgentAction{ID: working, Action: "Edit a.go"})

	got := r.railCards(r.store.State())
	want := [][]core.PaneID{{blocked, blocked1b, blocked2}, {working}, {idle}, {nohooks}}

	if !reflect.DeepEqual(ids(got), want) {
		t.Fatalf("groups = %v, want %v", ids(got), want)
	}

	type exp struct {
		action, tabName string
		tab             int
	}

	base := filepath.Base(dir)
	wantCard := map[core.PaneID]exp{
		blocked:   {"Allow Bash ls?", "Backend", 0},
		blocked1b: {"", "Backend", 0},
		blocked2:  {"", base, 2},
		working:   {"Edit a.go", base, 1},
		idle:      {idleAction, "Backend", 0},
		nohooks:   {"", base, 1},
	}

	for _, g := range got {
		for _, c := range g.cards {
			if c.pane == shell {
				t.Error("a shell is listed")
			}

			if w := wantCard[c.pane]; c.action != w.action || c.tab != w.tab || c.tabName != w.tabName {
				t.Errorf("%s = action %q tab %d name %q; want %v", c.pane, c.action, c.tab, c.tabName, w)
			}
		}
	}

	r.railSetSearch(false, "tab 2")

	if got := ids(r.railCards(r.store.State())); !reflect.DeepEqual(got, [][]core.PaneID{{working}, {nohooks}}) {
		t.Errorf("tab 2 -> %v", got)
	}

	r.railSetSearch(false, "backend")

	if n := len(r.railShown(r.store.State())); n != 3 {
		t.Errorf("backend matched %d cards, want the 3 of tab 1", n)
	}
}

func TestRailMatcher(t *testing.T) {
	t.Parallel()

	c := railCard{tab: 1, tabName: "Backend", project: "multiplex", branch: "feat/Rail", action: "Edit internal/core/modes.go"}

	tests := []struct {
		q    string
		want []railHit
	}{
		{"", nil},
		{"MUL", []railHit{{railFieldProject, 0, 3}}},
		{"rail", []railHit{{railFieldBranch, 5, 9}}},
		{"modes", []railHit{{railFieldAction, 19, 24}}},
		{"tab 2", []railHit{{railFieldTabNumber, 0, 5}}},
		{"back", []railHit{{railFieldTabName, 0, 4}}},
		{"e", []railHit{{railFieldProject, 7, 8}, {railFieldBranch, 1, 2}, {railFieldAction, 0, 1}, {railFieldTabName, 4, 5}}},
		{"nothing", nil},
	}

	for _, tt := range tests {
		hits, ok := c.matches(tt.q)
		if ok != (tt.q == "" || tt.want != nil) || !reflect.DeepEqual(hits, tt.want) {
			t.Errorf("matches(%q) = %v, %v; want %v", tt.q, hits, ok, tt.want)
		}
	}
}

func TestRailSearchMatchesTheName(t *testing.T) {
	t.Parallel()

	c := railCard{project: "multiplex", name: "muxr-Orchestrator", branch: "feat/orch"}

	tests := []struct {
		q    string
		want []railHit
	}{
		{"orch", []railHit{{railFieldName, 5, 9}, {railFieldBranch, 5, 9}}},
		{"ORCHESTRA", []railHit{{railFieldName, 5, 14}}},
		{"mu", []railHit{{railFieldProject, 0, 2}, {railFieldName, 0, 2}}},
		{"plex", []railHit{{railFieldProject, 5, 9}}},
		{"nothing", nil},
	}

	for _, tt := range tests {
		hits, ok := c.matches(tt.q)
		if ok != (tt.want != nil) || !reflect.DeepEqual(hits, tt.want) {
			t.Errorf("matches(%q) = %v, %v; want %v", tt.q, hits, ok, tt.want)
		}
	}

	if _, ok := (railCard{project: "multiplex"}).matches("orch"); ok {
		t.Error("a card with no name matched the text of one")
	}

	a := drawApp(t, "catppuccin-mocha", true, 100, 30, namedAgents())

	for _, q := range []struct {
		query string
		want  []core.PaneID
	}{
		{"orch", []core.PaneID{"p1"}},
		{"T16", []core.PaneID{"p2"}},
		{"reviewer", []core.PaneID{"p3"}},
		{"multiplex", []core.PaneID{"p1", "p2", "p3", "p4", "p5"}}, // the project still matches a named card
		{"zzz", nil},
	} {
		a.railSetSearch(false, q.query)

		var got []core.PaneID

		for _, c := range a.railShown(a.store.State()) {
			got = append(got, c.pane)
		}

		slices.Sort(got)

		if !slices.Equal(got, q.want) {
			t.Errorf("search %q lists %v, want %v", q.query, got, q.want)
		}
	}
}

func TestRailSelectionFollowsItsAgent(t *testing.T) {
	t.Parallel()

	r := newRailApp(t, false)
	dir := t.TempDir()
	a1 := r.addAgent(t, false, dir)
	a2 := r.addAgent(t, false, dir)
	a3 := r.addAgent(t, false, dir)

	for _, id := range []core.PaneID{a1, a2, a3} {
		r.setState(t, id, core.AgentIdle)
	}

	r.railOpened()
	r.railSelect(a2)

	// a3 becomes blocked and moves above a2: the selection stays on a2.
	r.setState(t, a3, core.AgentBlocked)
	r.settle()

	if sel := r.rail.sel; sel != a2 || r.rail.selIdx != 2 {
		t.Fatalf("selection = %s at %d, want %s at 2", sel, r.rail.selIdx, a2)
	}

	// a2 goes: the card now at its index (a1's, the last) is selected.
	openIn(t, r.App, core.ClosePane{ID: a2})
	r.settle()

	if sel := r.rail.sel; sel != a1 {
		t.Errorf("after the agent went: %s, want %s", sel, a1)
	}

	// Under a filter that hides the selection, the nearest shown card takes over.
	r.railSelect(a3)
	r.railSetSearch(true, "no such text")

	if r.rail.sel != "" {
		t.Error("selection kept with an empty list")
	}

	r.railSetSearch(true, "")

	if r.rail.sel == "" {
		t.Error("no selection once cards are shown again")
	}

	r.railMove(10)

	if sel := r.rail.sel; sel != a1 {
		t.Errorf("moved past the end: %s, want the last card %s", sel, a1)
	}
}

func TestRailUnseenMarks(t *testing.T) {
	t.Parallel()

	r := newRailApp(t, false)
	dir := t.TempDir()
	a1 := r.addAgent(t, false, dir)
	a2 := r.addAgent(t, false, dir) // focused
	unseen := func(id core.PaneID) bool { return r.rail.panes[id] != nil && r.rail.panes[id].unseen }

	hook(r.App, harness.Event{Pane: a1, Name: "UserPromptSubmit", State: core.AgentWorking})

	if !unseen(a1) {
		t.Error("a state change of an unfocused pane left no mark")
	}

	hook(r.App, harness.Event{Pane: a2, Name: "UserPromptSubmit", State: core.AgentWorking})

	if unseen(a2) {
		t.Error("the focused pane was marked")
	}

	openIn(t, r.App, core.FocusPane{ID: a1})
	r.settle()

	if unseen(a1) {
		t.Error("focusing did not clear the mark")
	}

	// Idle again while already idle still marks.
	hook(r.App, harness.Event{Pane: a2, Name: "Stop", State: core.AgentIdle})
	hook(r.App, harness.Event{Pane: a2, Name: "Stop", State: core.AgentIdle})
	r.settle()

	if !unseen(a2) {
		t.Fatal("a hook to idle left no mark")
	}

	openIn(t, r.App, core.FocusPane{ID: a2})
	r.settle()

	// The quiet rule.
	hook(r.App, harness.Event{Pane: a1, Name: "UserPromptSubmit", State: core.AgentWorking})
	openIn(t, r.App, core.FocusPane{ID: a2})
	r.settle()
	r.rail.panes[a1].unseen = false
	r.panes[a1] = &pane{id: a1}

	t.Cleanup(func() { delete(r.panes, a1) })
	tickAt(r.App, r.clk, 10*time.Second)

	if agentState(r.App, a1) != core.AgentIdle || !unseen(a1) {
		t.Errorf("quiet rule: state %v unseen %v", agentState(r.App, a1), unseen(a1))
	}

	// A dialog key.
	openIn(t, r.App, core.SetAgentState{ID: a1, State: core.AgentBlocked})
	r.rail.panes[a1].unseen = false
	r.answerDialog(&pane{id: a1}, "enter")

	if !unseen(a1) {
		t.Error("a dialog answer left no mark")
	}

	// A hidden tab's agent is cleared by showing it.
	a3 := r.addAgent(t, true, dir)
	openIn(t, r.App, core.SelectTab{Index: 0})
	hook(r.App, harness.Event{Pane: a3, Name: "UserPromptSubmit", State: core.AgentWorking})

	if !unseen(a3) {
		t.Fatal("hidden tab agent not marked")
	}

	openIn(t, r.App, core.SelectTab{Index: 1})
	r.settle()

	if unseen(a3) {
		t.Error("switching to its tab did not clear the mark")
	}
}

func TestRailActiveTimeIsTheLaterOfOutputAndHook(t *testing.T) {
	t.Parallel()

	r := newRailApp(t, false)
	id := r.addAgent(t, false, t.TempDir())
	r.panes[id] = &pane{id: id, lastOut: t0.Add(5 * time.Second)}

	t.Cleanup(func() { delete(r.panes, id) })

	r.clk.Set(t0.Add(2 * time.Second))
	hook(r.App, harness.Event{Pane: id, Name: "PostToolUse", State: core.AgentWorking})

	if c, _ := r.card(id); !c.active.Equal(t0.Add(5 * time.Second)) {
		t.Errorf("active = %v, want the later output time", c.active)
	}

	r.clk.Set(t0.Add(9 * time.Second))
	hook(r.App, harness.Event{Pane: id, Name: "PostToolUse", State: core.AgentWorking})

	if c, _ := r.card(id); !c.active.Equal(t0.Add(9 * time.Second)) {
		t.Errorf("active = %v, want the hook time", c.active)
	}
}

func TestRailForgetsClosedPanes(t *testing.T) {
	t.Parallel()

	r := newRailApp(t, false)
	id := r.addAgent(t, false, t.TempDir())
	hook(r.App, harness.Event{Pane: id, Name: "Stop", State: core.AgentIdle})

	if r.rail.panes[id] == nil {
		t.Fatal("no entry")
	}

	openIn(t, r.App, core.ClosePane{ID: id})
	r.settle()

	if _, ok := r.rail.panes[id]; ok {
		t.Error("the closed pane keeps its entry")
	}
}

func TestRailUIStateSurvivesUpdates(t *testing.T) {
	t.Parallel()

	r := newRailApp(t, false)
	r.addAgent(t, false, t.TempDir())
	openIn(t, r.App, core.SetRail{Open: true})
	r.rail.focused, r.rail.top = true, 3
	r.railOpened()
	r.settle()

	// One card fits the list, so the stored top is clamped to the start.
	if !r.rail.focused || r.rail.top != 0 {
		t.Errorf("focused %v top %d, want the keyboard kept and top clamped to 0", r.rail.focused, r.rail.top)
	}
}

func TestRailSelectionUnderAFilterThatHidesIt(t *testing.T) {
	t.Parallel()

	r := newRailApp(t, false)
	a1 := r.addAgent(t, true, "/w/alpha")
	a2 := r.addAgent(t, true, "/w/beta")
	a3 := r.addAgent(t, true, "/w/alphabet")

	for _, id := range []core.PaneID{a1, a2, a3} {
		r.setState(t, id, core.AgentIdle)
	}

	r.railSelect(a2)
	r.railSetSearch(true, "alpha")

	if sel := r.rail.sel; sel != a3 {
		t.Errorf("selection = %s, want %s (the card now at its old index)", sel, a3)
	}

	r.railSetSearch(true, "alphab")

	if sel := r.rail.sel; sel != a3 {
		t.Errorf("selection = %s, want %s", sel, a3)
	}

	r.railSetSearch(true, "alpha")
	r.railSelect(a1)
	r.railSetSearch(true, "beta")

	if sel := r.rail.sel; sel != a2 {
		t.Errorf("selection = %s, want %s (the only card)", sel, a2)
	}
}
