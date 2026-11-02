package ui

import (
	"slices"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/mhersson/aim/internal/core"
	"github.com/mhersson/aim/internal/harness"
)

// kindApp is a sized app with a fake clock.
func kindApp(t *testing.T, opts ...func(*Options)) (*App, *starts, *fakeClock) {
	t.Helper()

	o := Options{
		Logger: testLogger(),
		Git:    SystemGit{},
		Store:  core.NewStore(core.InitialState(core.Config{Theme: "catppuccin-mocha"})),
		Getenv: testGetenv,
		Cwd:    testCwd,
		Icons:  true,
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

	st := &starts{}
	a.start = st.start
	clk := &fakeClock{t: t0}
	a.clock = clk

	t.Cleanup(func() { closeApp(t, a) })

	sized(t, a, 100, 30)

	return a, st, clk
}

// probeKind runs one pane probe after the throttle window and applies it.
func probeKind(t *testing.T, a *App, clk *fakeClock, id core.PaneID) {
	t.Helper()

	clk.Advance(time.Second)

	cmd := a.probeCwd(a.panes[id])
	if cmd == nil {
		t.Fatalf("probe of %s returned no command", id)
	}

	a.Update(cmd())
}

func kindOf(a *App, id core.PaneID) core.PaneKind { return a.store.State().Panes[id].Kind }

func TestKindFollowsForeground(t *testing.T) {
	t.Parallel()
	t.Run("shell becomes agent and back", func(t *testing.T) {
		a, st, clk := kindApp(t)

		st.proc(0).setFg("claude")
		probeKind(t, a, clk, "p1")

		m := a.store.State().Panes["p1"]
		if m.Kind != core.KindAgent || m.Harness != "claude-code" {
			t.Fatalf("pane = %+v, want a claude-code agent", m)
		}

		if len(a.store.State().AgentPanes()) != 1 || !a.tabInfos()[0].agent {
			t.Errorf("agents = %d, tab agent = %v, want the tab to follow", len(a.store.State().AgentPanes()), a.tabInfos()[0].agent)
		}

		if !a.panes["p1"].launched.IsZero() || !a.panes["p1"].seen {
			t.Errorf("launched = %v, seen = %v, want zero and true", a.panes["p1"].launched, a.panes["p1"].seen)
		}

		st.proc(0).setFg("zsh")
		probeKind(t, a, clk, "p1")

		if m := a.store.State().Panes["p1"]; m.Kind != core.KindShell || m.Harness != "" {
			t.Errorf("pane = %+v, want a shell", m)
		}

		if n := len(a.store.State().AgentPanes()); n != 0 {
			t.Errorf("agents = %d, want 0", n)
		}
	})

	t.Run("unknown foreground changes nothing", func(t *testing.T) {
		a, st, clk := kindApp(t)

		st.proc(0).setFg("claude")
		probeKind(t, a, clk, "p1")

		st.proc(0).setFg("") // Foreground reports an error
		probeKind(t, a, clk, "p1")

		if kindOf(a, "p1") != core.KindAgent {
			t.Error("an unreadable foreground demoted the agent")
		}
	})

	t.Run("launched agent survives the shell at startup", func(t *testing.T) {
		a, st, clk := kindApp(t)
		chord(a, 'n')

		if st.count() != 2 || kindOf(a, "p2") != core.KindAgent {
			t.Fatalf("starts = %d, kind = %q, want an agent in p2", st.count(), kindOf(a, "p2"))
		}

		st.proc(1).setFg("zsh")
		probeKind(t, a, clk, "p2")

		if kindOf(a, "p2") != core.KindAgent {
			t.Fatal("the pane became a shell before Claude had its chance")
		}

		clk.Advance(agentGrace)

		a.Update(paneKindDueMsg{id: "p2", p: a.panes["p2"]})
		a.Update(probe(a.panes["p2"], true, false)())

		if kindOf(a, "p2") != core.KindShell {
			t.Error("Claude never appeared, yet the pane is still an agent after the grace period")
		}
	})

	t.Run("launched agent that is seen is demoted when Claude leaves", func(t *testing.T) {
		a, st, clk := kindApp(t)
		chord(a, 'n')

		st.proc(1).setFg("claude")
		probeKind(t, a, clk, "p2")

		if !a.panes["p2"].seen {
			t.Fatal("Claude in the foreground did not mark the pane seen")
		}

		st.proc(1).setFg("zsh")
		probeKind(t, a, clk, "p2")

		if kindOf(a, "p2") != core.KindShell {
			t.Error("the pane stayed an agent after Claude left")
		}
	})

	t.Run("demotion clears the session", func(t *testing.T) {
		a, st, clk := kindApp(t)
		chord(a, 'n')

		if err := a.dispatch(core.SetAgentSession{ID: "p2", Session: "s-1", Transcript: "/t.jsonl"}); err != nil {
			t.Fatal(err)
		}

		st.proc(1).setFg("claude")
		probeKind(t, a, clk, "p2")
		st.proc(1).setFg("zsh")
		probeKind(t, a, clk, "p2")

		if m := a.store.State().Panes["p2"]; m.Session != "" || m.Transcript != "" || m.State != core.AgentUnknown {
			t.Errorf("pane = %+v, want session, transcript and state cleared", m)
		}
	})

	t.Run("a demoted pane closes without a confirm and quit does not ask", func(t *testing.T) {
		a, st, clk := kindApp(t)
		chord(a, 'n')

		st.proc(1).setFg("claude")
		probeKind(t, a, clk, "p2")
		st.proc(1).setFg("zsh")
		probeKind(t, a, clk, "p2")

		if cmd := chord(a, 'q'); a.confirm != nil || !isQuit(cmd) {
			t.Error("quit asked about a pane that is a shell now")
		}

		chord(a, 'x')

		if a.confirm != nil {
			t.Error("closing a demoted pane asked first")
		}

		if _, ok := a.store.State().Panes["p2"]; ok {
			t.Error("the demoted pane was not closed")
		}
	})

	t.Run("the grace probe leaves the throttle alone", func(t *testing.T) {
		a, _, clk := kindApp(t)
		chord(a, 'n')

		p := a.panes["p2"]
		p.cwdBusy, p.cwdWait = true, true

		clk.Advance(agentGrace)

		cmd := a.onKindDue(paneKindDueMsg{id: "p2", p: a.panes["p2"]})
		if cmd == nil {
			t.Fatal("the grace probe returned no command")
		}

		a.Update(cmd())

		if !p.cwdBusy || !p.cwdWait {
			t.Errorf("cwdBusy = %v, cwdWait = %v, want the regular probe's state kept", p.cwdBusy, p.cwdWait)
		}
	})

	t.Run("a harness the saved id does not name falls back to the app's", func(t *testing.T) {
		a, st, clk := kindApp(t)

		if err := a.dispatch(core.SetPaneKind{ID: "p1", Kind: core.KindAgent, Harness: "unknown"}); err != nil {
			t.Fatal(err)
		}

		a.panes["p1"].seen = true

		st.proc(0).setFg("zsh")
		probeKind(t, a, clk, "p1")

		if kindOf(a, "p1") != core.KindShell {
			t.Error("an agent with an unknown harness id was not demoted")
		}
	})
}

func TestAgentLaunch(t *testing.T) {
	t.Parallel()
	t.Run("hooks and pane environment", func(t *testing.T) {
		a, st, _ := kindApp(t, func(o *Options) {
			o.HookSettings = "/run/1-claude.json"
			o.HookSock = "/run/1.sock"
		})
		chord(a, 'n')

		agent := st.cmds[1]
		if !hasArgs(argvOf(agent), "--settings", "/run/1-claude.json") {
			t.Errorf("agent argv = %q, want --settings with the file", agent.Argv)
		}

		for i, c := range st.cmds {
			id := "AIM_PANE=p" + string(rune('1'+i))
			if !slices.Contains(c.Env, id) || !slices.Contains(c.Env, "AIM_SOCK=/run/1.sock") {
				t.Errorf("command %d env lacks %s and the socket", i, id)
			}
		}

		if m := a.store.State().Panes["p2"]; m.Harness != (harness.Claude{}).ID() {
			t.Errorf("harness = %q, want claude-code", m.Harness)
		}

		if a.panes["p2"].launched.IsZero() || a.panes["p2"].seen {
			t.Error("a launched agent must record launched and not be seen")
		}
	})

	t.Run("no settings and no socket", func(t *testing.T) {
		a, st, _ := kindApp(t)
		chord(a, 'n')

		agent := st.cmds[1]
		if slices.Contains(argvOf(agent), "--settings") {
			t.Errorf("agent argv = %q, want no --settings", agent.Argv)
		}

		for _, kv := range agent.Env {
			if strings.HasPrefix(kv, "AIM_SOCK=") {
				t.Errorf("env holds %q without a socket", kv)
			}
		}

		if !slices.Contains(agent.Env, "AIM_PANE=p2") {
			t.Error("env lacks AIM_PANE")
		}
	})

	t.Run("resume reaches the argv", func(t *testing.T) {
		a, st, _ := kindApp(t)

		if _, _, err := a.startPane("p9", core.KindAgent, testCwd, 80, 24, "abc-123"); err != nil {
			t.Fatal(err)
		}

		cmd := st.cmds[st.count()-1]
		if !hasArgs(argvOf(cmd), "--resume", "abc-123") {
			t.Errorf("argv = %q, want --resume", cmd.Argv)
		}
	})

	t.Run("a resumed agent is demoted only after the grace period", func(t *testing.T) {
		a, st, clk := kindApp(t)

		id := a.store.State().NewPaneID()

		if _, _, err := a.startPane(id, core.KindAgent, testCwd, 80, 24, "abc-123"); err != nil {
			t.Fatal(err)
		}

		if err := a.dispatch(core.OpenPane{Kind: core.KindAgent, Cwd: testCwd, Split: core.SplitVertical, Harness: "claude-code"}); err != nil {
			t.Fatal(err)
		}

		st.proc(st.count() - 1).setFg("zsh")
		probeKind(t, a, clk, id)

		if kindOf(a, id) != core.KindAgent {
			t.Fatal("a resumed agent was demoted before the grace period")
		}

		clk.Advance(agentGrace)
		probeKind(t, a, clk, id)

		if kindOf(a, id) != core.KindShell {
			t.Error("a resumed agent whose Claude never appeared stayed an agent")
		}
	})

	t.Run("opening an agent schedules the grace probe", func(t *testing.T) {
		a, _, _ := kindApp(t)

		var delays []time.Duration

		var msgs []tea.Msg

		a.tick = func(d time.Duration, fn func(time.Time) tea.Msg) tea.Cmd {
			delays = append(delays, d)
			msgs = append(msgs, fn(t0))

			return func() tea.Msg { return nil }
		}

		if a.openPane(core.KindAgent, core.SplitVertical) == nil {
			t.Fatal("no command")
		}

		if len(delays) != 1 || delays[0] != agentGrace {
			t.Fatalf("ticks = %v, want one of agentGrace", delays)
		}

		if m, ok := msgs[0].(paneKindDueMsg); !ok || m.id != "p2" {
			t.Errorf("tick message = %#v, want paneKindDueMsg for p2", msgs[0])
		}
	})
}
