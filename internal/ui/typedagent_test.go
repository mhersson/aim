package ui

import (
	"testing"
	"time"

	"github.com/mhersson/aim/internal/core"
	"github.com/mhersson/aim/internal/harness"
)

const tr = "/tmp/transcript.jsonl"

func startEvent(launch string) harness.Event {
	return harness.Event{Pane: "p1", Name: "SessionStart", Session: "s1", Transcript: tr, Start: true, State: core.AgentIdle, Cwd: launch}
}

func TestHeldHooksWaitForTheProbe(t *testing.T) {
	t.Parallel()

	launch := t.TempDir()
	ev := startEvent(launch)

	t.Run("claude in front", func(t *testing.T) {
		t.Parallel()

		a, st, _ := kindApp(t)
		st.proc(0).setFg("claude")

		cmd := hook(a, ev)
		if m := a.store.State().Panes["p1"]; m.Kind != core.KindShell || m.Session != "" {
			t.Fatalf("pane = %+v, want an untouched shell", m)
		}

		for _, msg := range collect(cmd) {
			a.Update(msg)
		}

		m := a.store.State().Panes["p1"]
		if m.Kind != core.KindAgent || m.Session != "s1" || m.Transcript != tr || m.State != core.AgentIdle || m.Cwd != launch {
			t.Errorf("pane = %+v, want an idle agent of s1 in %s", m, launch)
		}

		if n := len(a.panes["p1"].held); n != 0 {
			t.Errorf("held = %d, want 0", n)
		}
	})

	for _, fg := range []string{"zsh", ""} {
		t.Run("shell in front "+fg, func(t *testing.T) {
			t.Parallel()

			a, st, clk := kindApp(t)
			st.proc(0).setFg(fg)

			for _, msg := range collect(hook(a, ev)) {
				a.Update(msg)
			}

			if m := a.store.State().Panes["p1"]; m.Kind != core.KindShell {
				t.Fatalf("kind = %v, want shell", m.Kind)
			}

			if n := len(a.panes["p1"].held); n != 0 {
				t.Errorf("held = %d, want 0", n)
			}

			st.proc(0).setFg("claude")
			probeKind(t, a, clk, "p1")

			if m := a.store.State().Panes["p1"]; m.Kind != core.KindAgent || m.Session != "" {
				t.Errorf("pane = %+v, want an agent with no session", m)
			}
		})
	}

	t.Run("status before the probe", func(t *testing.T) {
		t.Parallel()

		a, st, _ := kindApp(t)
		st.proc(0).setFg("claude")

		cmd := hook(a, ev)
		if second := hook(a, harness.Event{Pane: "p1", Status: true, Session: "s1", Model: "Opus"}); second != nil {
			t.Error("the second hook started another probe")
		}

		for _, msg := range collect(cmd) {
			a.Update(msg)
		}

		if m := a.store.State().Panes["p1"]; m.Kind != core.KindAgent || m.Model != "Opus" {
			t.Errorf("pane = %+v, want an agent on Opus", m)
		}
	})

	t.Run("a probe already out", func(t *testing.T) {
		t.Parallel()

		a, st, clk := kindApp(t)
		st.proc(0).setFg("zsh")
		clk.Advance(time.Second)

		stale := collect(a.probeCwd(a.panes["p1"]))

		st.proc(0).setFg("claude")

		cmd := hook(a, ev)

		for _, msg := range stale {
			a.Update(msg)
		}

		if m := a.store.State().Panes["p1"]; m.Kind != core.KindShell {
			t.Fatalf("kind = %v after the stale probe, want shell", m.Kind)
		}

		if n := len(a.panes["p1"].held); n != 1 {
			t.Fatalf("held = %d after the stale probe, want 1", n)
		}

		for _, msg := range collect(cmd) {
			a.Update(msg)
		}

		if m := a.store.State().Panes["p1"]; m.Kind != core.KindAgent || m.Session != "s1" {
			t.Errorf("pane = %+v, want an agent of s1", m)
		}
	})

	t.Run("at most maxHeld", func(t *testing.T) {
		t.Parallel()

		a, st, _ := kindApp(t)
		st.proc(0).setFg("zsh")

		probes := 0

		for range 20 {
			if hook(a, ev) != nil {
				probes++
			}
		}

		if n := len(a.panes["p1"].held); n != maxHeld {
			t.Errorf("held = %d, want %d", n, maxHeld)
		}

		if probes != 1 {
			t.Errorf("probes = %d, want 1", probes)
		}
	})
}

func TestTypedAgentResumesWhereClaudeStarted(t *testing.T) {
	t.Parallel()

	launch := t.TempDir()
	a, st, clk := kindApp(t)
	cwd := func() string { return a.store.State().Panes["p1"].Cwd }

	_ = a.dispatch(core.SetPaneCwd{ID: "p1", Cwd: "/start"})

	st.proc(0).setCwd("/stale")
	st.proc(0).setFg("claude")
	probeKind(t, a, clk, "p1")

	if m := a.store.State().Panes["p1"]; m.Kind != core.KindAgent || m.Cwd != "/stale" {
		t.Fatalf("pane = %+v, want an agent in /stale", m)
	}

	hook(a, startEvent(launch))

	if got := cwd(); got != launch {
		t.Fatalf("cwd = %q after SessionStart, want %q", got, launch)
	}

	probeKind(t, a, clk, "p1")

	if got := cwd(); got != launch {
		t.Fatalf("cwd = %q after a probe, want %q", got, launch)
	}

	hook(a, harness.Event{Pane: "p1", Name: "SessionStart", Session: "s2", Start: true, NewConversation: true, State: core.AgentIdle, Cwd: "/elsewhere"})

	if m := a.store.State().Panes["p1"]; m.Cwd != launch || m.Session != "s2" {
		t.Fatalf("pane = %+v, want s2 still in %s", m, launch)
	}

	st.proc(0).setCwd("/after")
	st.proc(0).setFg("zsh")
	probeKind(t, a, clk, "p1")
	probeKind(t, a, clk, "p1")

	if m := a.store.State().Panes["p1"]; m.Kind != core.KindShell || m.Cwd != "/after" {
		t.Errorf("pane = %+v, want a shell in /after", m)
	}
}

func TestLaunchedAgentKeepsItsCwd(t *testing.T) {
	t.Parallel()

	a, _, clk := agentApp(t)

	hook(a, harness.Event{Pane: "p2", Name: "SessionStart", Session: "s1", Start: true, State: core.AgentIdle, Cwd: t.TempDir()})

	if got := a.store.State().Panes["p2"].Cwd; got != testCwd {
		t.Fatalf("cwd = %q after SessionStart, want %q", got, testCwd)
	}

	fp := a.panes["p2"].proc.(*fakeProc)
	fp.setCwd(t.TempDir())
	fp.setFg("claude")

	probeKind(t, a, clk, "p2")

	if got := a.store.State().Panes["p2"].Cwd; got != testCwd {
		t.Errorf("cwd = %q after a probe, want %q", got, testCwd)
	}
}
