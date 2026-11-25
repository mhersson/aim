package ui

import (
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/mhersson/aim/internal/core"
	"github.com/mhersson/aim/internal/harness"
)

// codexApp is an app whose p1 a probe has made an interactive Codex agent owned by pid 42.
func codexApp(t *testing.T) (*App, *pane, *fakeClock) {
	t.Helper()

	a, st, clk := kindApp(t)
	st.proc(0).setFg("codex")

	p := a.panes["p1"]
	a.onCwd(paneCwdMsg{p: p, id: p.id, fg: "codex", pid: 42})

	if m := a.store.State().Panes["p1"]; m.Kind != core.KindAgent || m.Harness != "codex" {
		t.Fatalf("setup: p1 = %+v, want a Codex agent", m)
	}

	return a, p, clk
}

func codexStatusEv(run string, session core.SessionID) harness.Event {
	return harness.Event{Pane: "p1", Harness: "codex", Name: "statusline", Status: true, OwnerPID: 42, Session: session, RunState: run}
}

func codexHook(state core.AgentState) harness.Event {
	return harness.Event{Pane: "p1", Harness: "codex", Name: "UserPromptSubmit", OwnerPID: 42, Session: "s1", State: state}
}

func checksHooks(t *testing.T, a *App) bool {
	t.Helper()

	return cardOf(t, a, "p1").action == "check /hooks"
}

func TestCodexStatusState(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		recorded core.SessionID
		from     core.AgentState
		run      string
		thread   core.SessionID
		want     core.AgentState
	}{
		{"ready idles", "", core.AgentWorking, "ready", "s1", core.AgentIdle},
		{"working", "", core.AgentIdle, "working", "s1", core.AgentWorking},
		{"thinking", "", core.AgentIdle, "thinking", "s1", core.AgentWorking},
		{"starting changes nothing", "", core.AgentWorking, "starting", "s1", core.AgentWorking},
		{"waiting never blocks", "", core.AgentWorking, "waiting", "s1", core.AgentWorking},
		{"the recorded thread counts", "s1", core.AgentWorking, "ready", "s1", core.AgentIdle},
		{"another thread changes nothing", "s1", core.AgentWorking, "ready", "s2", core.AgentWorking},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			a, p, _ := codexApp(t)
			a.Update(tea.BlurMsg{})
			openIn(t, a, core.SetAgentState{ID: "p1", State: tt.from})

			if tt.recorded != "" {
				openIn(t, a, core.SetAgentSession{ID: "p1", Session: tt.recorded})
			}

			sent := raws(hook(a, codexStatusEv(tt.run, tt.thread)))

			if got := agentState(a, "p1"); got != tt.want {
				t.Errorf("state = %v, want %v", got, tt.want)
			}

			if len(sent) != 0 {
				t.Errorf("status sent notices %q", sent)
			}

			if p.hooksSeen {
				t.Error("a status set hooksSeen")
			}
		})
	}
}

func TestCodexStatusQuietRule(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		from   core.AgentState
		thread core.SessionID // the status's thread; the hooks record s1
		hookAt time.Duration  // a working hook after the status; 0 means none
		tick   time.Duration
		want   core.AgentState
	}{
		{"ready under agentQuiet", core.AgentWorking, "s1", 0, agentQuiet - spinEvery, core.AgentWorking},
		{"ready for agentQuiet idles a working pane", core.AgentWorking, "s1", 0, agentQuiet, core.AgentIdle},
		{"a hook since keeps it working", core.AgentWorking, "s1", agentQuiet / 2, agentQuiet, core.AgentWorking},
		{"a hook newer than ready outlives an older ready", core.AgentWorking, "s1", spinEvery, spinEvery + agentQuiet, core.AgentWorking},
		{"ready for agentQuiet idles a blocked pane", core.AgentBlocked, "s1", 0, agentQuiet, core.AgentIdle},
		{"another thread's ready keeps it working", core.AgentWorking, "s2", 0, agentQuiet, core.AgentWorking},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			a, _, clk := codexApp(t)
			openIn(t, a, core.SetAgentSession{ID: "p1", Session: "s1"})
			hook(a, codexHook(tt.from))
			hook(a, codexStatusEv("ready", tt.thread))

			if tt.hookAt != 0 {
				clk.Set(t0.Add(tt.hookAt))
				hook(a, codexHook(core.AgentWorking))
			}

			tickAt(a, clk, tt.tick)

			if got := agentState(a, "p1"); got != tt.want {
				t.Errorf("state = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestCodexStatusBeforeProbe(t *testing.T) {
	t.Parallel()

	a, st, _ := kindApp(t)
	st.proc(0).setFg("codex")

	p := a.panes["p1"]
	hook(a, codexStatusEv("ready", "s1"))

	if m := a.store.State().Panes["p1"]; m.Kind != core.KindShell {
		t.Fatalf("status before the probe changed the shell: %+v", m)
	}

	a.onCwd(paneCwdMsg{p: p, id: p.id, fg: "codex", pid: 42})

	if m := a.store.State().Panes["p1"]; m.Kind != core.KindAgent || m.Harness != "codex" || m.State != core.AgentIdle {
		t.Fatalf("after the probe: %+v, want an idle Codex agent", m)
	}
}

func TestCodexCheckHooks(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		run  func(a *App, clk *fakeClock)
		want bool
	}{
		{"no status", func(_ *App, clk *fakeClock) { clk.Set(t0.Add(2 * agentQuiet)) }, false},
		{"working under agentQuiet", func(a *App, clk *fakeClock) {
			hook(a, codexStatusEv("working", "s1"))
			clk.Set(t0.Add(agentQuiet - spinEvery))
		}, false},
		{"working for agentQuiet", func(a *App, clk *fakeClock) {
			hook(a, codexStatusEv("working", "s1"))
			clk.Set(t0.Add(agentQuiet))
		}, true},
		{"stays after the status turns ready", func(a *App, clk *fakeClock) {
			hook(a, codexStatusEv("working", "s1"))
			clk.Set(t0.Add(agentQuiet))
			hook(a, codexStatusEv("ready", "s1"))
			clk.Set(t0.Add(3 * agentQuiet))
		}, true},
		{"a hook clears it", func(a *App, clk *fakeClock) {
			hook(a, codexStatusEv("working", "s1"))
			clk.Set(t0.Add(agentQuiet))
			hook(a, codexHook(core.AgentWorking))
		}, false},
		{"ready only", func(a *App, clk *fakeClock) {
			hook(a, codexStatusEv("ready", "s1"))
			clk.Set(t0.Add(2 * agentQuiet))
		}, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			a, _, clk := codexApp(t)
			tt.run(a, clk)

			if got := checksHooks(t, a); got != tt.want {
				t.Errorf("check /hooks shown = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestCodexWithoutStatusKeepsHookBehaviour(t *testing.T) {
	t.Parallel()

	a, _, clk := codexApp(t)

	if checksHooks(t, a) {
		t.Fatal("check /hooks before any status")
	}

	start := codexHook(core.AgentIdle)
	start.Name, start.Start, start.Model = "SessionStart", true, "gpt-6.2-sol"

	steps := []struct {
		name string
		ev   harness.Event
		want core.AgentState
	}{
		{"session start", start, core.AgentIdle},
		{"prompt", codexHook(core.AgentWorking), core.AgentWorking},
		{"permission", codexHook(core.AgentBlocked), core.AgentBlocked},
		{"answered", codexHook(core.AgentWorking), core.AgentWorking},
	}

	for i, s := range steps {
		hook(a, s.ev)
		tickAt(a, clk, time.Duration(i+1)*2*agentQuiet)

		if got := agentState(a, "p1"); got != s.want {
			t.Errorf("%s: state = %v, want %v", s.name, got, s.want)
		}

		if checksHooks(t, a) {
			t.Errorf("%s: check /hooks shown", s.name)
		}
	}

	if got := cardOf(t, a, "p1").model; got != "gpt-6.2-sol" {
		t.Errorf("model = %q, want the hook's", got)
	}
}

func TestLateStatusAfterCodexExit(t *testing.T) {
	t.Parallel()

	a, p, _ := codexApp(t)
	hook(a, codexHook(core.AgentWorking))

	a.onCwd(paneCwdMsg{p: p, id: p.id, fg: "zsh", pid: 7})
	hook(a, codexStatusEv("ready", "s1"))

	if m := a.store.State().Panes["p1"]; m.Kind != core.KindShell {
		t.Fatalf("after the exit: %+v, want a shell", m)
	}

	a.onCwd(paneCwdMsg{p: p, id: p.id, fg: "codex", pid: 43})

	if m := a.store.State().Panes["p1"]; m.Kind != core.KindAgent || m.State != core.AgentUnknown {
		t.Fatalf("the retired owner's status reached its replacement: %+v", m)
	}
}

func TestCodexStatusSurvivesSuspend(t *testing.T) {
	t.Parallel()

	a, p, clk := codexApp(t)
	hook(a, codexStatusEv("working", "s1"))

	a.onCwd(paneCwdMsg{p: p, id: p.id, fg: "zsh", pid: 7, liveOwner: 42})
	a.onCwd(paneCwdMsg{p: p, id: p.id, fg: "codex", pid: 42})

	clk.Set(t0.Add(agentQuiet))

	if !checksHooks(t, a) {
		t.Fatal("the status was lost across the suspend")
	}
}

func TestCodexCardNameAndModel(t *testing.T) {
	t.Parallel()

	a, _, _ := codexApp(t)

	early := codexHook(core.AgentWorking)
	early.Model = "gpt-6.2-sol"
	hook(a, early)

	if got := cardOf(t, a, "p1").model; got != "gpt-6.2-sol" {
		t.Fatalf("model before a status = %q, want the hook's", got)
	}

	status := codexStatusEv("working", "s1")
	status.ThreadName, status.Model, status.Context, status.ContextKnown = "renamed", "GPT-6.2-Sol", 41.5, true
	hook(a, status)

	if got := a.cardName(a.store.State(), "p1"); got != "renamed" {
		t.Errorf("card name = %q, want the thread's", got)
	}

	late := codexHook(core.AgentWorking)
	late.Model = "x"
	hook(a, late)

	a.railSetSearch(true, "ren")

	c := cardOf(t, a, "p1")
	if c.model != "GPT-6.2-Sol" || !c.meterKnown || c.meter != 41 {
		t.Errorf("card model %q meter %d known %v, want the status's GPT-6.2-Sol at 41", c.model, c.meter, c.meterKnown)
	}
}

func TestCodexStatusAfterSessionSwitch(t *testing.T) {
	t.Parallel()

	a, _, _ := codexApp(t)

	start := codexHook(core.AgentIdle)
	start.Name, start.Start = "SessionStart", true
	hook(a, start)

	status := codexStatusEv("ready", "s1")
	status.Model = "GPT-6.2-Sol"
	hook(a, status)

	start.Session = "s2"
	hook(a, start)

	if got := cardOf(t, a, "p1").model; got != "GPT-6.2-Sol" {
		t.Errorf("model after the switch = %q, want the status's", got)
	}
}

func TestCodexDialogKeys(t *testing.T) {
	t.Parallel()

	permission := codexHook(core.AgentBlocked)
	permission.Name = "PermissionRequest"

	question := codexHook(core.AgentBlocked)
	question.Name = "PreToolUse"

	tests := []struct {
		name string
		pre  []harness.Event // hooks delivered before ev
		ev   harness.Event
		key  string
		want core.AgentState
	}{
		{"enter answers a permission", nil, permission, "enter", core.AgentWorking},
		{"esc answers a permission", nil, permission, "esc", core.AgentWorking},
		{"y answers a permission", nil, permission, "y", core.AgentWorking},
		{"p answers a permission", nil, permission, "p", core.AgentWorking},
		{"enter answers a question", nil, question, "enter", core.AgentWorking},
		{"y does not answer a question", nil, question, "y", core.AgentBlocked},
		{"y does not answer a question after a permission", []harness.Event{permission}, question, "y", core.AgentBlocked},
		{"a digit answers nothing", nil, permission, "1", core.AgentBlocked},
		{"another key answers nothing", nil, permission, "x", core.AgentBlocked},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			a, p, _ := codexApp(t)

			for _, ev := range tt.pre {
				hook(a, ev)
			}

			hook(a, tt.ev)

			if got := agentState(a, "p1"); got != core.AgentBlocked {
				t.Fatalf("setup: state = %v, want blocked", got)
			}

			a.answerDialog(p, tt.key)

			if got := agentState(a, "p1"); got != tt.want {
				t.Errorf("state = %v, want %v", got, tt.want)
			}
		})
	}
}
