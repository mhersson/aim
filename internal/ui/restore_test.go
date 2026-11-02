package ui

import (
	"context"
	"errors"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/mhersson/aim/internal/core"
	"github.com/mhersson/aim/internal/harness"
	"github.com/mhersson/aim/internal/term"
)

const (
	restoreSession = "7c0b6f1e-3a52-4d8e-9b41-2f5e8a1d0c37"
	freshNotice    = "started a fresh claude in api: its session could not be resumed"
)

func vsplit(a, b core.PaneID) *core.Node {
	return &core.Node{Split: core.SplitVertical, Ratio: 0.5, A: &core.Node{Pane: a}, B: &core.Node{Pane: b}}
}

// restoreState is two tabs: an agent with a session beside a shell, then an
// agent that never had a session.
func restoreState() core.State {
	return core.State{
		Theme: "catppuccin-mocha", Active: 1, NextID: 3, NextTabID: 2,
		Tabs: []core.Tab{
			{ID: "t1", Focus: "p1", Mode: core.ModeBSP, Panes: []core.PaneID{"p1", "p2"}, Layout: vsplit("p1", "p2")},
			{ID: "t2", Focus: "p3", Mode: core.ModeBSP, Panes: []core.PaneID{"p3"}, Layout: &core.Node{Pane: "p3"}},
		},
		Panes: map[core.PaneID]core.Pane{
			"p1": {Kind: core.KindAgent, Cwd: "/srv/api", Harness: "claude-code", Session: restoreSession, Transcript: "/x/t.jsonl"},
			"p2": {Kind: core.KindShell, Cwd: "/srv/web"},
			"p3": {Kind: core.KindAgent, Cwd: "/tmp", Harness: "claude-code"},
		},
	}
}

type saves struct {
	mu     sync.Mutex
	states []core.State
}

func (s *saves) save(st core.State) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.states = append(s.states, st)
}

func (s *saves) count() int {
	s.mu.Lock()
	defer s.mu.Unlock()

	return len(s.states)
}

func (s *saves) last() core.State {
	s.mu.Lock()
	defer s.mu.Unlock()

	return s.states[len(s.states)-1]
}

// ticked records the delay and message of every tick the app asks for.
type ticked struct {
	delays []time.Duration
	msgs   []tea.Msg
}

func (k *ticked) tick(d time.Duration, fn func(time.Time) tea.Msg) tea.Cmd {
	k.delays = append(k.delays, d)
	k.msgs = append(k.msgs, fn(t0))

	return func() tea.Msg { return nil }
}

type restored struct {
	a     *App
	st    *starts
	clk   *fakeClock
	saves *saves
	ticks *ticked
}

// restoreApp is an app over st that has not seen a size yet.
func restoreApp(t *testing.T, st core.State, opts ...func(*Options)) *restored {
	t.Helper()

	r := &restored{st: &starts{}, clk: &fakeClock{t: t0}, saves: &saves{}, ticks: &ticked{}}
	o := Options{
		Logger: testLogger(), Git: newFakeGit(t), Store: core.NewStore(st), Getenv: testGetenv, Cwd: testCwd, Icons: true,
		Save: r.saves.save, HookSettings: "/run/1-claude.json",
	}

	for _, f := range opts {
		f(&o)
	}

	a, err := New(o)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	a.clip.run = (&fakeRun{}).run
	a.openLink = (&fakeOpen{}).open
	a.start = r.st.start
	a.clock = r.clk
	a.tick = r.ticks.tick
	r.a = a

	t.Cleanup(func() { closeApp(t, a) })

	return r
}

// size delivers the first window size and returns the command it raised.
func (r *restored) size(w, h int) tea.Cmd {
	_, cmd := r.a.Update(tea.WindowSizeMsg{Width: w, Height: h})

	return cmd
}

// argvOf is the agent's own argv: what follows the script and $0 of the
// sh-family login shell the tests run under.
func argvOf(c term.Command) []string { return c.Argv[6:] }

// hasArgs reports whether argv holds flag directly followed by value.
func hasArgs(argv []string, flag, value string) bool {
	i := slices.Index(argv, flag)

	return i >= 0 && i+1 < len(argv) && argv[i+1] == value
}

func TestRestoreStartsEveryPane(t *testing.T) {
	t.Parallel()
	r := restoreApp(t, restoreState(), func(o *Options) {
		o.HookSettings = "/run/1-claude.json"
		o.HookSock = "/run/1.sock"
	})

	cmd := r.size(100, 30)
	if cmd == nil {
		t.Fatal("no command")
	}

	if r.st.count() != 3 {
		t.Fatalf("starts = %d, want 3", r.st.count())
	}

	byPane := map[string]int{}

	for i, c := range r.st.cmds {
		for _, kv := range c.Env {
			if id, ok := strings.CutPrefix(kv, "AIM_PANE="); ok {
				byPane[id] = i
			}
		}
	}

	if got := argvOf(r.st.cmds[byPane["p1"]]); !hasArgs(got, "--resume", restoreSession) {
		t.Errorf("p1 argv = %q, want --resume %s", got, restoreSession)
	}

	if got := argvOf(r.st.cmds[byPane["p3"]]); slices.Contains(got, "--resume") || !slices.Contains(got, "--settings") {
		t.Errorf("p3 argv = %q, want a fresh claude with hooks", got)
	}

	if c := r.st.cmds[byPane["p2"]]; c.Dir != "/srv/web" || c.Argv[len(c.Argv)-1] != "-l" {
		t.Errorf("p2 command = %+v, want a login shell in /srv/web", c)
	}

	if c := r.st.cmds[byPane["p1"]]; c.Dir != "/srv/api" {
		t.Errorf("p1 dir = %q, want /srv/api", c.Dir)
	}

	st := r.a.store.State()
	for _, id := range []core.PaneID{"p1", "p2", "p3"} {
		c, rw := paneSizeOf(r.a, id)
		size := [2]int{c, rw}
		i, _ := st.TabOf(id)

		rect, ok := r.a.layoutIn(st, i)[id]
		if !ok {
			t.Fatalf("no rect for %s", id)
		}

		cols, rows := contentSize(rect)
		if size != [2]int{cols, rows} {
			t.Errorf("%s size = %v, want %dx%d", id, size, cols, rows)
		}

		if got := r.st.sizes[byPane[string(id)]]; got != [2]int{cols, rows} {
			t.Errorf("%s started at %v, want %dx%d", id, got, cols, rows)
		}
	}

	if !r.a.panes["p1"].resuming || r.a.panes["p3"].resuming || r.a.panes["p2"].resuming {
		t.Errorf("resuming = %v %v %v, want only p1", r.a.panes["p1"].resuming, r.a.panes["p2"].resuming, r.a.panes["p3"].resuming)
	}

	if r.saves.count() != 0 {
		t.Errorf("saves = %d, want none while the panes start", r.saves.count())
	}

	if want := []time.Duration{agentGrace, agentGrace}; !slices.Equal(r.ticks.delays, want) {
		t.Errorf("tick delays = %v, want a grace probe per agent", r.ticks.delays)
	}

	due := map[core.PaneID]bool{}

	for _, m := range r.ticks.msgs {
		if d, ok := m.(paneKindDueMsg); ok {
			due[d.id] = d.p == r.a.panes[d.id]
		}
	}

	if !due["p1"] || !due["p3"] || len(due) != 2 {
		t.Errorf("grace probes = %v, want p1 and p3 bound to their panes", due)
	}

	if st.Active != 1 || st.Tab().Focus != "p3" {
		t.Errorf("active = %d, focus = %q, want the saved ones", st.Active, st.Tab().Focus)
	}

	// The next change saves the state as it then is.
	if err := r.a.dispatch(core.RenameTab{ID: "t1", Name: "api"}); err != nil {
		t.Fatal(err)
	}

	if r.saves.count() != 1 || r.saves.last().Tabs[0].Name != "api" {
		t.Errorf("saves = %d, want one holding the rename", r.saves.count())
	}

	if err := r.a.dispatch(core.RenameTab{ID: "t9", Name: "x"}); err == nil {
		t.Fatal("renaming an unknown tab succeeded")
	}

	if r.saves.count() != 1 {
		t.Errorf("saves = %d, want a failed dispatch not saved", r.saves.count())
	}

	// A second size message does not start the panes again.
	r.size(90, 28)

	if r.st.count() != 3 {
		t.Errorf("starts = %d after a resize, want 3", r.st.count())
	}
}

func TestRestoreZoomedTabHiddenPaneKeepsLayoutSize(t *testing.T) {
	t.Parallel()

	st := restoreState()
	st.Tabs[0].Zoomed = true

	r := restoreApp(t, st)
	r.size(100, 30)

	fullCols, fullRows := contentSize(layoutBody(bodyRect(100, 30)))
	if c, rows := paneSizeOf(r.a, "p1"); c != fullCols || rows != fullRows {
		t.Errorf("zoomed pane = %dx%d, want %dx%d", c, rows, fullCols, fullRows)
	}

	if c, _ := paneSizeOf(r.a, "p2"); c >= fullCols {
		t.Errorf("hidden pane is %d wide, want less than the zoomed %d", c, fullCols)
	}
}

func paneSizeOf(a *App, id core.PaneID) (cols, rows int) { return a.panes[id].size() }

// failingStart fails for the listed panes and delegates otherwise.
func failingStart(s *starts, panes ...string) func(context.Context, term.Command, int, int) (proc, error) {
	return func(ctx context.Context, c term.Command, cols, rows int) (proc, error) {
		for _, kv := range c.Env {
			if id, ok := strings.CutPrefix(kv, "AIM_PANE="); ok && slices.Contains(panes, id) {
				return nil, errors.New("boom")
			}
		}

		return s.start(ctx, c, cols, rows)
	}
}

func TestRestoreFailingPanes(t *testing.T) {
	t.Parallel()
	t.Run("a tab whose panes all fail goes away", func(t *testing.T) {
		r := restoreApp(t, restoreState())
		r.a.start = failingStart(r.st, "p3")
		r.size(100, 30)

		st := r.a.store.State()
		if len(st.Tabs) != 1 || st.Active != 0 || len(r.a.panes) != 2 {
			t.Fatalf("tabs = %d, active = %d, panes = %d, want 1, 0 and 2", len(st.Tabs), st.Active, len(r.a.panes))
		}

		if !strings.Contains(r.a.notice, "cannot start agent: boom") {
			t.Errorf("notice = %q, want the start failure", r.a.notice)
		}

		if r.saves.count() != 0 {
			t.Errorf("saves = %d, want none", r.saves.count())
		}
	})

	t.Run("one pane failing leaves its sibling", func(t *testing.T) {
		r := restoreApp(t, restoreState())
		r.a.start = failingStart(r.st, "p1")
		r.size(100, 30)

		st := r.a.store.State()
		if _, ok := st.Panes["p1"]; ok || len(st.Tabs) != 2 || st.Tabs[0].Layout.Pane != "p2" {
			t.Fatalf("state = %+v, want p1 gone and p2 alone in tab 1", st)
		}

		if r.saves.count() != 0 {
			t.Errorf("saves = %d, want none", r.saves.count())
		}
	})

	t.Run("no pane left opens one shell in the launch dir", func(t *testing.T) {
		one := core.State{
			Theme: "catppuccin-mocha", NextID: 2, NextTabID: 1,
			Tabs:  []core.Tab{{ID: "t1", Focus: "p1", Mode: core.ModeBSP, Panes: []core.PaneID{"p1", "p2"}, Layout: vsplit("p1", "p2")}},
			Panes: map[core.PaneID]core.Pane{"p1": {Kind: core.KindShell, Cwd: "/srv/a"}, "p2": {Kind: core.KindShell, Cwd: "/srv/b"}},
		}
		r := restoreApp(t, one)
		r.a.start = failingStart(r.st, "p1", "p2")
		r.size(100, 30)

		st := r.a.store.State()
		if len(st.Panes) != 1 || len(r.a.panes) != 1 {
			t.Fatalf("panes = %d, registry = %d, want one shell", len(st.Panes), len(r.a.panes))
		}

		for id, m := range st.Panes {
			if m.Kind != core.KindShell || m.Cwd != testCwd {
				t.Errorf("pane %s = %+v, want a shell in %s", id, m, testCwd)
			}

			if id == "p1" || id == "p2" {
				t.Errorf("pane id %s was reused", id)
			}
		}

		if r.saves.count() != 0 {
			t.Errorf("saves = %d, want none", r.saves.count())
		}
	})
}

func TestRestoreNotices(t *testing.T) {
	t.Parallel()

	st := restoreState()
	st.Panes["p2"] = core.Pane{Kind: core.KindShell, Cwd: "/gone/web"}
	st.Panes["p3"] = core.Pane{Kind: core.KindShell, Cwd: "/gone/db"}

	const bad = "the saved layout could not be read; it was kept as state.json.bad"

	r := restoreApp(t, st, func(o *Options) { o.Notice = bad })
	r.a.start = func(ctx context.Context, c term.Command, cols, rows int) (proc, error) {
		if strings.HasPrefix(c.Dir, "/gone") {
			return nil, errors.New("no such directory")
		}

		return r.st.start(ctx, c, cols, rows)
	}
	r.size(100, 30)

	want := bad + "; /gone/web is not available, started in /work; /gone/db is not available, started in /work"
	if r.a.notice != want {
		t.Errorf("notice = %q, want %q", r.a.notice, want)
	}

	for _, id := range []core.PaneID{"p2", "p3"} {
		if got := r.a.store.State().Panes[id].Cwd; got != testCwd {
			t.Errorf("%s cwd = %q, want the launch dir", id, got)
		}
	}

	// The notice stays until the first key.
	r.a.Update(keyRune('x'))

	if r.a.notice != "" {
		t.Errorf("notice after a key = %q, want none", r.a.notice)
	}
}

func TestRestoreEmptyCwdStartsInLaunchDir(t *testing.T) {
	t.Parallel()

	st := restoreState()
	st.Panes["p2"] = core.Pane{Kind: core.KindShell}

	r := restoreApp(t, st)
	r.size(100, 30)

	found := false

	for _, c := range r.st.cmds {
		if slices.Contains(c.Env, "AIM_PANE=p2") {
			found = true

			if c.Dir != testCwd {
				t.Errorf("p2 dir = %q, want %q", c.Dir, testCwd)
			}
		}
	}

	if !found {
		t.Fatal("p2 was not started")
	}
}

// resumeApp is the restored app after its first size.
func resumeApp(t *testing.T) *restored {
	t.Helper()

	r := restoreApp(t, restoreState())
	r.size(100, 30)

	return r
}

func probeAt(t *testing.T, r *restored, id core.PaneID, d time.Duration) {
	t.Helper()

	r.clk.Advance(d)
	r.a.Update(probe(r.a.panes[id], true, false)())
}

func TestResumeRestartsAgentWhoseClaudeNeverAppeared(t *testing.T) {
	t.Parallel()
	r := resumeApp(t)
	old := r.a.panes["p1"]

	r.st.proc(0).setFg("zsh")
	probeAt(t, r, "p1", agentGrace-time.Second)

	if r.st.count() != 3 || r.a.panes["p1"] != old {
		t.Fatal("restarted before the grace period")
	}

	probeAt(t, r, "p1", time.Second)

	if r.st.count() != 4 {
		t.Fatalf("starts = %d, want a fresh claude", r.st.count())
	}

	fresh := r.a.panes["p1"]
	if fresh == old || fresh.resuming || fresh.seen {
		t.Fatalf("pane = %p (old %p), resuming = %v, seen = %v, want a new pane that is not resuming or seen", fresh, old, fresh.resuming, fresh.seen)
	}

	c := r.st.cmds[3]
	if got := argvOf(c); slices.Contains(got, "--resume") || got[0] != "claude" {
		t.Errorf("restart argv = %q, want a fresh claude", got)
	}

	if c.Dir != "/srv/api" || !slices.Contains(c.Env, "AIM_PANE=p1") {
		t.Errorf("restart = %+v, want the same pane and cwd", c)
	}

	if got, want := r.st.sizes[3], r.st.sizes[0]; got != want {
		t.Errorf("restart size = %v, want %v", got, want)
	}

	if r.a.notice != freshNotice {
		t.Errorf("notice = %q, want %q", r.a.notice, freshNotice)
	}

	m := r.a.store.State().Panes["p1"]
	if m.Kind != core.KindAgent || m.Session != "" || m.Transcript != "" {
		t.Errorf("pane = %+v, want an agent with the session cleared", m)
	}

	eventually2(t, 5*time.Second, "the old process to close", func() bool { return isClosed(r.st.proc(0)) })

	// The old pane's late exit must not close the new one.
	r.a.Update(paneExitedMsg{id: "p1", p: old})
	r.a.Update(paneDirtyMsg{id: "p1", p: old})

	if r.a.panes["p1"] != fresh || kindOf(r.a, "p1") != core.KindAgent {
		t.Fatal("a message from the replaced pane disturbed the new one")
	}

	// The restarted pane is a new launch: not demoted at once, demoted after its own grace.
	r.st.proc(3).setFg("zsh")
	probeAt(t, r, "p1", time.Second)

	if kindOf(r.a, "p1") != core.KindAgent || r.st.count() != 4 {
		t.Fatal("the restarted pane was demoted or restarted again at once")
	}

	probeAt(t, r, "p1", agentGrace)

	if kindOf(r.a, "p1") != core.KindShell || r.st.count() != 4 {
		t.Errorf("kind = %q, starts = %d, want a shell after the second grace and no third start", kindOf(r.a, "p1"), r.st.count())
	}
}

// Claude Code files its sessions under the directory it was launched in, so an
// agent resumes there even when its last hook named another directory.
func TestResumeStartsInTheLaunchDirNotTheAgentCwd(t *testing.T) {
	t.Parallel()

	const worktree = "/srv/api/.worktrees/feat"

	t.Run("a restored agent", func(t *testing.T) {
		st := restoreState()
		p := st.Panes["p1"]
		p.AgentCwd = worktree
		st.Panes["p1"] = p

		r := restoreApp(t, st)
		r.size(100, 30)

		if c := r.st.cmds[0]; c.Dir != "/srv/api" || !hasArgs(argvOf(c), "--resume", restoreSession) {
			t.Errorf("p1 start = dir %q argv %q, want a resume in /srv/api", c.Dir, argvOf(c))
		}
	})

	t.Run("a fresh restart", func(t *testing.T) {
		r := resumeApp(t)

		hook(r.a, harness.Event{Pane: "p1", Name: "PostToolUse", Cwd: worktree})

		if m := r.a.store.State().Panes["p1"]; m.AgentCwd != worktree || m.Cwd != "/srv/api" {
			t.Fatalf("pane = Cwd %q AgentCwd %q, want the hook's directory beside the launch directory", m.Cwd, m.AgentCwd)
		}

		r.st.proc(0).setFg("zsh")
		probeAt(t, r, "p1", agentGrace)

		if r.st.count() != 4 {
			t.Fatalf("starts = %d, want a fresh claude", r.st.count())
		}

		if c := r.st.cmds[3]; c.Dir != "/srv/api" {
			t.Errorf("restart dir = %q, want /srv/api", c.Dir)
		}

		if r.a.notice != freshNotice {
			t.Errorf("notice = %q, want %q", r.a.notice, freshNotice)
		}
	})
}

func TestResumeRestartSchedulesItsOwnProbe(t *testing.T) {
	t.Parallel()
	r := resumeApp(t)

	r.st.proc(0).setFg("zsh")
	r.clk.Advance(agentGrace)

	r.ticks.delays, r.ticks.msgs = nil, nil

	_, cmd := r.a.Update(probe(r.a.panes["p1"], true, false)())
	if cmd == nil {
		t.Fatal("no command after a restart")
	}

	if len(r.ticks.delays) != 1 || r.ticks.delays[0] != agentGrace {
		t.Fatalf("ticks = %v, want one grace probe", r.ticks.delays)
	}

	if m, ok := r.ticks.msgs[0].(paneKindDueMsg); !ok || m.id != "p1" || m.p != r.a.panes["p1"] {
		t.Errorf("tick message = %#v, want a probe bound to the new pane", r.ticks.msgs[0])
	}
}

func TestResumeKept(t *testing.T) {
	t.Parallel()
	t.Run("a SessionStart ends the resume", func(t *testing.T) {
		r := resumeApp(t)
		r.a.Update(hookMsg{ev: harness.Event{Pane: "p1", Name: "SessionStart", Session: restoreSession, Transcript: "/x/t.jsonl", Start: true, State: core.AgentIdle}})

		if r.a.panes["p1"].resuming {
			t.Fatal("pane still resuming after its SessionStart")
		}

		r.st.proc(0).setFg("zsh")
		probeAt(t, r, "p1", agentGrace)

		if r.st.count() != 3 {
			t.Errorf("starts = %d, want the resumed agent left alone", r.st.count())
		}

		if kindOf(r.a, "p1") != core.KindShell {
			t.Errorf("kind = %q, want the kind rule to apply as to any launch", kindOf(r.a, "p1"))
		}
	})

	t.Run("claude in the foreground after the grace period stays", func(t *testing.T) {
		r := resumeApp(t)
		r.st.proc(0).setFg("claude")
		probeAt(t, r, "p1", agentGrace)

		if r.st.count() != 3 || kindOf(r.a, "p1") != core.KindAgent {
			t.Errorf("starts = %d, kind = %q, want nothing to change", r.st.count(), kindOf(r.a, "p1"))
		}
	})

	t.Run("claude seen without a SessionStart ends the resume", func(t *testing.T) {
		r := resumeApp(t)
		r.st.proc(0).setFg("claude")
		probeAt(t, r, "p1", agentGrace)

		if r.a.panes["p1"].resuming {
			t.Fatal("pane still resuming with claude in the foreground after the grace period")
		}

		r.st.proc(0).setFg("zsh")
		probeAt(t, r, "p1", time.Second)

		if r.st.count() != 3 {
			t.Errorf("starts = %d, want no restart after claude exits", r.st.count())
		}

		if kindOf(r.a, "p1") != core.KindShell {
			t.Errorf("kind = %q, want a shell", kindOf(r.a, "p1"))
		}

		if r.a.notice != "" {
			t.Errorf("notice = %q, want none", r.a.notice)
		}
	})

	t.Run("an unknown foreground changes nothing", func(t *testing.T) {
		r := resumeApp(t)
		r.st.proc(0).setFg("")
		probeAt(t, r, "p1", agentGrace)

		if r.st.count() != 3 || kindOf(r.a, "p1") != core.KindAgent || !r.a.panes["p1"].resuming {
			t.Errorf("starts = %d, kind = %q, want nothing to change and the next probe to decide", r.st.count(), kindOf(r.a, "p1"))
		}

		r.st.proc(0).setFg("zsh")
		probeAt(t, r, "p1", time.Second)

		if r.st.count() != 4 {
			t.Errorf("starts = %d, want the next probe to restart", r.st.count())
		}
	})
}

func TestResumeRefusedIsRestartedNotDemoted(t *testing.T) {
	t.Parallel()
	r := resumeApp(t)

	// Claude showed up, refused the resume, fired SessionEnd and left.
	r.st.proc(0).setFg("claude")
	probeAt(t, r, "p1", time.Second)
	r.a.Update(hookMsg{ev: harness.Event{Pane: "p1", Name: "SessionEnd", End: true}})

	if !r.a.panes["p1"].resuming {
		t.Fatal("SessionEnd ended the resuming mark")
	}

	if m := r.a.store.State().Panes["p1"]; m.Session != "" || m.State != core.AgentUnknown {
		t.Fatalf("pane = %+v, want session and state cleared", m)
	}

	r.st.proc(0).setFg("zsh")
	probeAt(t, r, "p1", time.Second)

	if kindOf(r.a, "p1") != core.KindAgent || r.st.count() != 3 {
		t.Fatalf("kind = %q, starts = %d, want no demotion and no restart before the grace period", kindOf(r.a, "p1"), r.st.count())
	}

	probeAt(t, r, "p1", agentGrace)

	if r.st.count() != 4 || kindOf(r.a, "p1") != core.KindAgent || r.a.notice != freshNotice {
		t.Errorf("starts = %d, kind = %q, notice = %q, want one restart at the probe", r.st.count(), kindOf(r.a, "p1"), r.a.notice)
	}
}

func TestMessagesFromReplacedPaneAreDropped(t *testing.T) {
	t.Parallel()
	r := resumeApp(t)
	cur := r.a.panes["p2"]
	stale := &pane{id: "p2"}

	for _, msg := range []tea.Msg{
		paneDirtyMsg{id: "p2", p: stale},
		holdExpiredMsg{id: "p2", p: stale},
		paneExitedMsg{id: "p2", p: stale},
		paneCwdMsg{id: "p2", p: stale, cwd: "/elsewhere", fg: "claude"},
		paneCwdDueMsg{id: "p2", p: stale},
		paneKindDueMsg{id: "p2", p: stale},
	} {
		_, cmd := r.a.Update(msg)
		if cmd != nil {
			t.Errorf("%T from a replaced pane raised a command", msg)
		}
	}

	if r.a.panes["p2"] != cur || !cur.lastOut.IsZero() || cur.cwdWait {
		t.Error("a message from a replaced pane changed the live one")
	}

	m := r.a.store.State().Panes["p2"]
	if m.Cwd != "/srv/web" || m.Kind != core.KindShell {
		t.Errorf("pane = %+v, want it untouched", m)
	}
}

func TestProbeMessagesCarryTheirPane(t *testing.T) {
	t.Parallel()
	r := resumeApp(t)
	p := r.a.panes["p2"]

	if m, ok := probe(p, false, false)().(paneCwdMsg); !ok || m.p != p {
		t.Errorf("probe message = %#v, want one bound to its pane", m)
	}

	_, _ = r.st.proc(1).feed.Write([]byte("hi"))

	if m, ok := p.wait()().(paneDirtyMsg); !ok || m.p != p {
		t.Errorf("dirty message = %#v, want one bound to its pane", m)
	}
}

func TestResumeWithoutHooksIsNotRestarted(t *testing.T) {
	t.Parallel()
	r := restoreApp(t, restoreState(), func(o *Options) { o.HookSettings = "" })
	r.size(100, 30)

	if r.a.panes["p1"].resuming {
		t.Fatal("marked resuming although no SessionStart can arrive")
	}

	// Claude ran and was exited by the user: the pane becomes a shell.
	r.st.proc(0).setFg("claude")
	probeAt(t, r, "p1", time.Second)
	r.st.proc(0).setFg("zsh")
	probeAt(t, r, "p1", time.Second)

	if kindOf(r.a, "p1") != core.KindShell || r.st.count() != 3 {
		t.Errorf("kind = %q, starts = %d, want a shell and no restart", kindOf(r.a, "p1"), r.st.count())
	}
}

func TestResumeRestartFailureIsNotRetried(t *testing.T) {
	t.Parallel()
	r := resumeApp(t)
	r.a.start = failingStart(r.st, "p1")

	r.st.proc(0).setFg("zsh")
	probeAt(t, r, "p1", agentGrace)

	if !strings.Contains(r.a.notice, "cannot start agent: boom") || r.a.panes["p1"].resuming {
		t.Fatalf("notice = %q, resuming = %v, want the failure and the mark cleared", r.a.notice, r.a.panes["p1"].resuming)
	}

	// The next probe applies the kind rule instead of starting again.
	r.a.notice = ""
	probeAt(t, r, "p1", time.Second)

	if r.a.notice != "" || kindOf(r.a, "p1") != core.KindShell {
		t.Errorf("notice = %q, kind = %q, want no retry and a shell", r.a.notice, kindOf(r.a, "p1"))
	}
}

func TestPaneLimitNotice(t *testing.T) {
	t.Parallel()
	r := resumeApp(t)

	for len(r.a.store.State().Panes) < core.MaxPanes {
		if err := r.a.dispatch(core.OpenPane{Kind: core.KindShell, Split: core.SplitVertical}); err != nil {
			t.Fatal(err)
		}
	}

	panes, starts := len(r.a.store.State().Panes), r.st.count()

	for name, open := range map[string]func() tea.Cmd{
		"split":    func() tea.Cmd { return r.a.openPane(core.KindShell, core.SplitVertical) },
		"agent":    func() tea.Cmd { return r.a.openPane(core.KindAgent, core.SplitVertical) },
		"new tab":  r.a.newTab,
		"leader n": func() tea.Cmd { return press(r.a, keyLeader, keyRune('n')) },
	} {
		t.Run(name, func(t *testing.T) {
			r.a.notice = ""

			open()

			if r.a.notice != "at most 64 panes" {
				t.Errorf("notice = %q, want the pane limit", r.a.notice)
			}

			if got := len(r.a.store.State().Panes); got != panes || r.st.count() != starts {
				t.Errorf("panes = %d (want %d), starts = %d (want %d)", got, panes, r.st.count(), starts)
			}
		})
	}
}
