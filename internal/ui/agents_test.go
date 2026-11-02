package ui

import (
	"reflect"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/mhersson/aim/internal/core"
	"github.com/mhersson/aim/internal/harness"
)

// tickLog records the scheduled ticks and their messages.
type tickLog struct{ msgs []tea.Msg }

func (l *tickLog) tick(_ time.Duration, fn func(time.Time) tea.Msg) tea.Cmd {
	msg := fn(t0)
	l.msgs = append(l.msgs, msg)

	return func() tea.Msg { return msg }
}

func (l *tickLog) agentTicks() int {
	n := 0

	for _, m := range l.msgs {
		if _, ok := m.(agentTickMsg); ok {
			n++
		}
	}

	return n
}

// agentApp is a sized app whose pane p2 is an agent and has the focus.
func agentApp(t *testing.T) (*App, *tickLog, *fakeClock) {
	t.Helper()

	a, _, clk := kindApp(t)
	chord(a, 'n')

	tl := &tickLog{}
	a.tick = tl.tick

	if kindOf(a, "p2") != core.KindAgent {
		t.Fatal("p2 is not an agent")
	}

	return a, tl, clk
}

func hook(a *App, ev harness.Event) tea.Cmd {
	_, cmd := a.Update(hookMsg{ev: ev})

	return cmd
}

func agentState(a *App, id core.PaneID) core.AgentState { return a.store.State().Panes[id].State }

// tickAt moves the clock to t0+d and delivers one agent tick.
func tickAt(a *App, clk *fakeClock, d time.Duration) {
	clk.Set(t0.Add(d))
	a.Update(agentTickMsg{})
}

func TestHookEvents(t *testing.T) {
	t.Parallel()
	a, _, _ := agentApp(t)

	steps := []struct {
		name    string
		ev      harness.Event
		state   core.AgentState
		session core.SessionID
		trans   string
	}{
		{"session start", harness.Event{Pane: "p2", Name: "SessionStart", Session: "s1", Transcript: "/t/1.jsonl", Start: true, State: core.AgentIdle}, core.AgentIdle, "s1", "/t/1.jsonl"},
		{"prompt", harness.Event{Pane: "p2", Name: "UserPromptSubmit", State: core.AgentWorking}, core.AgentWorking, "s1", "/t/1.jsonl"},
		{"permission", harness.Event{Pane: "p2", Name: "PermissionRequest", State: core.AgentBlocked}, core.AgentBlocked, "s1", "/t/1.jsonl"},
		{"tool done", harness.Event{Pane: "p2", Name: "PostToolUse", State: core.AgentWorking}, core.AgentWorking, "s1", "/t/1.jsonl"},
		{"stop", harness.Event{Pane: "p2", Name: "Stop", State: core.AgentIdle}, core.AgentIdle, "s1", "/t/1.jsonl"},
		{"clear starts a new session", harness.Event{Pane: "p2", Name: "SessionStart", Session: "s2", Transcript: "/t/2.jsonl", Start: true, State: core.AgentIdle}, core.AgentIdle, "s2", "/t/2.jsonl"},
		{"compact keeps the state", harness.Event{Pane: "p2", Name: "UserPromptSubmit", State: core.AgentWorking}, core.AgentWorking, "s2", "/t/2.jsonl"},
		{"compact start", harness.Event{Pane: "p2", Name: "SessionStart", Session: "s2", Transcript: "/t/2.jsonl", Start: true}, core.AgentWorking, "s2", "/t/2.jsonl"},
		{"session end", harness.Event{Pane: "p2", Name: "SessionEnd", End: true}, core.AgentUnknown, "", ""},
		{"stop after end", harness.Event{Pane: "p2", Name: "Stop", State: core.AgentIdle}, core.AgentIdle, "", ""},
		{"start without an id", harness.Event{Pane: "p2", Name: "SessionStart", State: core.AgentIdle}, core.AgentIdle, "", ""},
	}

	for _, s := range steps {
		hook(a, s.ev)

		m := a.store.State().Panes["p2"]
		if m.State != s.state || m.Session != s.session || m.Transcript != s.trans {
			t.Errorf("%s: pane = state %v session %q transcript %q, want %v %q %q", s.name, m.State, m.Session, m.Transcript, s.state, s.session, s.trans)
		}
	}
}

func TestHookEventsCarryTheAction(t *testing.T) {
	t.Parallel()
	a, _, _ := agentApp(t)

	steps := []struct {
		name   string
		ev     harness.Event
		state  core.AgentState
		action string
	}{
		{"tool call", harness.Event{Pane: "p2", Name: "PreToolUse", State: core.AgentWorking, Action: "Edit a.go", ActionSet: true}, core.AgentWorking, "Edit a.go"},
		{"elicitation result leaves it", harness.Event{Pane: "p2", Name: "ElicitationResult", State: core.AgentWorking}, core.AgentWorking, "Edit a.go"},
		{"no action field leaves it", harness.Event{Pane: "p2", Name: "Notification", State: core.AgentBlocked}, core.AgentBlocked, "Edit a.go"},
		{"compact start clears without a state", harness.Event{Pane: "p2", Name: "SessionStart", Start: true, Session: "s1", ActionSet: true}, core.AgentBlocked, ""},
		{"prompt", harness.Event{Pane: "p2", Name: "UserPromptSubmit", State: core.AgentWorking, Action: "thinking", ActionSet: true}, core.AgentWorking, "thinking"},
		{"stop clears", harness.Event{Pane: "p2", Name: "Stop", State: core.AgentIdle, ActionSet: true}, core.AgentIdle, ""},
		{"set again", harness.Event{Pane: "p2", Name: "PreToolUse", State: core.AgentWorking, Action: "Bash ls", ActionSet: true}, core.AgentWorking, "Bash ls"},
		{"end clears", harness.Event{Pane: "p2", Name: "SessionEnd", End: true, ActionSet: true}, core.AgentUnknown, ""},
	}

	for _, s := range steps {
		hook(a, s.ev)

		m := a.store.State().Panes["p2"]
		if m.State != s.state || m.Action != s.action {
			t.Errorf("%s: pane = state %v action %q, want %v %q", s.name, m.State, m.Action, s.state, s.action)
		}
	}

	hook(a, harness.Event{Pane: "p1", Name: "PreToolUse", Action: "Bash ls", ActionSet: true})

	if got := a.store.State().Panes["p1"].Action; got != "" {
		t.Errorf("a shell pane took the action %q", got)
	}
}

func TestAnsweredPermissionClearsFromTheCard(t *testing.T) {
	t.Parallel()
	a, _, _ := agentApp(t)

	for _, step := range []struct {
		payload string
		state   core.AgentState
		action  string
	}{
		{`{"hook_event_name":"PermissionRequest","cwd":"/w","tool_name":"Bash","tool_input":{"command":"rm x"}}`, core.AgentBlocked, "Allow Bash rm x?"},
		{`{"hook_event_name":"PostToolUse","cwd":"/w","tool_name":"Bash","tool_input":{"command":"rm x"}}`, core.AgentWorking, "Bash rm x"},
	} {
		ev, err := harness.Claude{}.ParseEvent([]byte(step.payload))
		if err != nil {
			t.Fatal(err)
		}

		ev.Pane = "p2"
		hook(a, ev)

		if m := a.store.State().Panes["p2"]; m.State != step.state || m.Action != step.action {
			t.Errorf("after %s: state %v action %q, want %v %q", ev.Name, m.State, m.Action, step.state, step.action)
		}
	}
}

func TestHookEventsThatDoNotFit(t *testing.T) {
	t.Parallel()
	a, _, _ := agentApp(t)

	hook(a, harness.Event{Pane: "p2", Name: "SessionStart", Session: "s1", Start: true, State: core.AgentIdle})

	before := a.store.State()

	tests := []struct {
		name string
		ev   harness.Event
	}{
		{"unknown pane", harness.Event{Pane: "p9", Name: "Stop", State: core.AgentIdle}},
		{"empty pane", harness.Event{Name: "Stop", State: core.AgentIdle}},
		{"shell pane", harness.Event{Pane: "p1", Name: "SessionStart", Session: "s9", Start: true, State: core.AgentWorking}},
		{"out of range state", harness.Event{Pane: "p2", Name: "Stop", State: core.AgentState(99)}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if cmd := hook(a, tt.ev); tt.name != "out of range state" && cmd != nil {
				// Only the re-armed wait may remain, and there is no channel here.
				t.Errorf("command = %v, want none", cmd)
			}

			if got := a.store.State(); !reflect.DeepEqual(got.Panes["p1"], before.Panes["p1"]) || !reflect.DeepEqual(got.Panes["p2"], before.Panes["p2"]) {
				t.Errorf("panes changed: %+v", got.Panes)
			}
		})
	}

	t.Run("closed pane", func(t *testing.T) {
		a.closePane("p2")

		if _, ok := a.store.State().Panes["p2"]; ok {
			t.Fatal("p2 is still open")
		}

		hook(a, harness.Event{Pane: "p2", Name: "UserPromptSubmit", State: core.AgentWorking})

		if _, ok := a.store.State().Panes["p2"]; ok {
			t.Error("an event reopened a closed pane")
		}
	})
}

func TestQuietRule(t *testing.T) {
	t.Parallel()
	t.Run("silence idles a working pane", func(t *testing.T) {
		a, _, clk := agentApp(t)

		hook(a, harness.Event{Pane: "p2", Name: "UserPromptSubmit", State: core.AgentWorking})

		tickAt(a, clk, agentQuiet-spinEvery)

		if agentState(a, "p2") != core.AgentWorking {
			t.Fatal("idle before agentQuiet passed")
		}

		tickAt(a, clk, agentQuiet)

		if agentState(a, "p2") != core.AgentIdle {
			t.Errorf("state = %v after agentQuiet of silence, want idle", agentState(a, "p2"))
		}
	})

	t.Run("output keeps it working", func(t *testing.T) {
		a, _, clk := agentApp(t)

		hook(a, harness.Event{Pane: "p2", Name: "UserPromptSubmit", State: core.AgentWorking})

		clk.Set(t0.Add(2 * time.Second))
		a.Update(paneDirtyMsg{id: "p2", p: a.panes["p2"]})

		tickAt(a, clk, 4*time.Second)

		if agentState(a, "p2") != core.AgentWorking {
			t.Fatal("idle although output arrived 2s ago")
		}

		tickAt(a, clk, 5*time.Second)

		if agentState(a, "p2") != core.AgentIdle {
			t.Error("still working 3s after the last output")
		}
	})

	t.Run("silence before the prompt does not count", func(t *testing.T) {
		a, _, clk := agentApp(t)

		clk.Set(t0.Add(time.Minute))
		hook(a, harness.Event{Pane: "p2", Name: "UserPromptSubmit", State: core.AgentWorking})
		tickAt(a, clk, time.Minute+spinEvery)

		if agentState(a, "p2") != core.AgentWorking {
			t.Error("a pane silent before the prompt went idle at the first tick")
		}
	})

	t.Run("a hook beats the rule", func(t *testing.T) {
		a, _, clk := agentApp(t)

		hook(a, harness.Event{Pane: "p2", Name: "UserPromptSubmit", State: core.AgentWorking})
		hook(a, harness.Event{Pane: "p2", Name: "PermissionRequest", State: core.AgentBlocked})
		tickAt(a, clk, time.Minute)

		if agentState(a, "p2") != core.AgentBlocked {
			t.Errorf("state = %v, want blocked to survive the quiet rule", agentState(a, "p2"))
		}
	})
}

func TestDialogKeys(t *testing.T) {
	t.Parallel()

	enter := tea.KeyPressMsg{Code: tea.KeyEnter}

	for _, k := range []tea.KeyPressMsg{enter, keyEsc, keyRune('1'), keyRune('9')} {
		t.Run(k.String(), func(t *testing.T) {
			a, tl, clk := agentApp(t)

			hook(a, harness.Event{Pane: "p2", Name: "PermissionRequest", State: core.AgentBlocked})

			clk.Set(t0.Add(time.Minute))
			press(a, k)

			if agentState(a, "p2") != core.AgentWorking {
				t.Fatalf("state = %v after %s, want working", agentState(a, "p2"), k)
			}

			if tl.agentTicks() != 1 {
				t.Errorf("agent ticks = %d, want 1", tl.agentTicks())
			}

			tickAt(a, clk, time.Minute+agentQuiet-spinEvery)

			if agentState(a, "p2") != core.AgentWorking {
				t.Fatal("the quiet timer did not restart at the key")
			}

			tickAt(a, clk, time.Minute+agentQuiet)

			if agentState(a, "p2") != core.AgentIdle {
				t.Error("still working 3s after the key")
			}
		})
	}

	t.Run("other keys change nothing", func(t *testing.T) {
		a, tl, _ := agentApp(t)

		hook(a, harness.Event{Pane: "p2", Name: "PermissionRequest", State: core.AgentBlocked})
		press(a, keyRune('k'))

		if agentState(a, "p2") != core.AgentBlocked || tl.agentTicks() != 0 {
			t.Errorf("state = %v, ticks = %d, want blocked and none", agentState(a, "p2"), tl.agentTicks())
		}
	})

	t.Run("an idle pane ignores dialog keys", func(t *testing.T) {
		a, _, _ := agentApp(t)

		hook(a, harness.Event{Pane: "p2", Name: "Stop", State: core.AgentIdle})
		press(a, enter)

		if agentState(a, "p2") != core.AgentIdle {
			t.Errorf("state = %v, want idle", agentState(a, "p2"))
		}
	})
}

func TestAgentTick(t *testing.T) {
	t.Parallel()
	t.Run("one tick at a time and none once nothing works", func(t *testing.T) {
		a, tl, clk := agentApp(t)

		hook(a, harness.Event{Pane: "p2", Name: "UserPromptSubmit", State: core.AgentWorking})
		hook(a, harness.Event{Pane: "p2", Name: "PostToolUse", State: core.AgentWorking})

		if tl.agentTicks() != 1 {
			t.Fatalf("agent ticks = %d after two working events, want 1", tl.agentTicks())
		}

		spin := a.spin

		tickAt(a, clk, spinEvery)

		if a.spin != spin+1 || tl.agentTicks() != 2 {
			t.Errorf("spin = %d, ticks = %d, want the spinner advanced and the tick re-armed", a.spin, tl.agentTicks())
		}

		tickAt(a, clk, agentQuiet+spinEvery)

		if agentState(a, "p2") != core.AgentIdle || tl.agentTicks() != 2 {
			t.Errorf("state = %v, ticks = %d, want idle and no re-arm", agentState(a, "p2"), tl.agentTicks())
		}

		hook(a, harness.Event{Pane: "p2", Name: "UserPromptSubmit", State: core.AgentWorking})

		if tl.agentTicks() != 3 {
			t.Errorf("agent ticks = %d, want a new tick when work starts again", tl.agentTicks())
		}
	})

	t.Run("idle and blocked events start no tick", func(t *testing.T) {
		a, tl, _ := agentApp(t)

		hook(a, harness.Event{Pane: "p2", Name: "PermissionRequest", State: core.AgentBlocked})
		hook(a, harness.Event{Pane: "p2", Name: "Stop", State: core.AgentIdle})

		if tl.agentTicks() != 0 {
			t.Errorf("agent ticks = %d, want none", tl.agentTicks())
		}
	})
}

func TestWaitEvent(t *testing.T) {
	t.Parallel()
	t.Run("nil channel starts no wait", func(t *testing.T) {
		a, _ := lifeApp(t)

		if a.waitEvent() != nil {
			t.Error("waitEvent returned a command without a channel")
		}
	})

	t.Run("delivers and re-arms", func(t *testing.T) {
		ch := make(chan harness.Event, 2)
		a, _, _ := kindApp(t, func(o *Options) { o.Events = ch })

		ch <- harness.Event{Pane: "p1", Name: "Stop"}

		msg, ok := a.waitEvent()().(hookMsg)
		if !ok || msg.ev.Name != "Stop" {
			t.Fatalf("message = %#v, want the Stop event", msg)
		}

		_, cmd := a.Update(msg)
		if cmd == nil {
			t.Fatal("the update did not re-arm the wait")
		}

		ch <- harness.Event{Name: "Second"}

		if got, ok := collectHook(cmd); !ok || got.ev.Name != "Second" {
			t.Errorf("re-armed wait gave %#v, want the second event", got)
		}
	})

	t.Run("ends with the app context", func(t *testing.T) {
		ch := make(chan harness.Event)
		a, _, _ := kindApp(t, func(o *Options) { o.Events = ch })

		done := make(chan tea.Msg, 1)
		cmd := a.waitEvent()

		go func() { done <- cmd() }()

		a.cancel()

		select {
		case m := <-done:
			if m != nil {
				t.Errorf("message = %#v, want none", m)
			}
		case <-time.After(5 * time.Second):
			t.Fatal("waitEvent kept waiting after the context ended")
		}
	})

	t.Run("a closed channel ends the wait", func(t *testing.T) {
		ch := make(chan harness.Event)
		close(ch)

		a, _, _ := kindApp(t, func(o *Options) { o.Events = ch })

		if m := a.waitEvent()(); m != nil {
			t.Errorf("message = %#v, want none", m)
		}
	})
}

// collectHook runs the commands of cmd and returns the hook message among them.
func collectHook(cmd tea.Cmd) (hookMsg, bool) {
	for _, m := range collect(cmd) {
		if h, ok := m.(hookMsg); ok {
			return h, true
		}
	}

	return hookMsg{}, false
}

func TestStartNotice(t *testing.T) {
	t.Parallel()
	a, _, _ := kindApp(t, func(o *Options) { o.Notice = "agent hooks are off: boom" })

	if a.notice != "agent hooks are off: boom" {
		t.Errorf("notice = %q, want the start notice", a.notice)
	}
}

func statusEvent(pane core.PaneID, session core.SessionID, model string, pct float64, known bool) harness.Event {
	return harness.Event{Pane: pane, Name: "statusline", Status: true, Session: session, Model: model, Context: pct, ContextKnown: known}
}

func TestStatusEventSkipsHookEffects(t *testing.T) {
	t.Parallel()
	// With no watch a hook event refreshes the drawer, so the drawer's silence is the test.
	r := newDrawerRig(t, func(o *Options) { o.Watches = nil })
	id := r.agent(t, editedRepo(t), "s1")
	v := r.open(t, id)

	r.run(r.railOpened())
	r.until(t, "the rail's head probe", func() bool { return r.rail.panes[id] != nil && r.rail.panes[id].probe.known })

	// Another pane has the focus, so a hook that idles the agent marks it unseen.
	openIn(t, r.App, core.FocusPane{ID: "p1"})
	r.settle()

	// Past every probe interval, so a hook event would start probes at once.
	r.clk.Advance(time.Minute)

	entry := *r.railEntry(id)

	if cmd := hook(r.App, statusEvent(id, "s1", "Opus 4.8", 41.7, true)); cmd != nil {
		t.Errorf("a status event raised a command: %v", flatten(cmd))
	}

	if got := *r.railEntry(id); !reflect.DeepEqual(got, entry) {
		t.Errorf("the card's memory changed:\n got %+v\nwant %+v", got, entry)
	}

	if r.view() != v || v.queued != nil || v.loading {
		t.Errorf("a status event refreshed the drawer: queued %v, loading %v", v.queued, v.loading)
	}

	m := r.store.State().Panes[id]
	if m.Model != "Opus 4.8" || m.Context != 41 || !m.ContextKnown || m.StatusSession != "s1" || m.Session != "s1" {
		t.Errorf("pane = %+v, want the model, 41%% and both sessions s1", m)
	}

	// The same checks fail for a hook event, so they would catch a status that took its path.
	hook(r.App, idleHook(id))

	if got := r.railEntry(id); !got.hookAt.After(entry.hookAt) || !got.unseen || !got.head.busy || !v.loading {
		t.Errorf("a hook event left the card's memory (hookAt %v, unseen %v), the head probe (busy %v) or the drawer (loading %v) alone",
			got.hookAt, got.unseen, got.head.busy, v.loading)
	}
}

func TestStatusFloodBumpsNoVersion(t *testing.T) {
	t.Parallel()
	a, _, _ := agentApp(t)
	hook(a, harness.Event{Pane: "p2", Name: "SessionStart", Session: "s1", Start: true})

	saves := 0
	a.save = func(core.State) { saves++ }

	steps := []struct {
		name  string
		ev    harness.Event
		moves bool
	}{
		{"first status", statusEvent("p2", "s1", "Opus 4.8", 41.2, true), true},
		{"repeated", statusEvent("p2", "s1", "Opus 4.8", 41.2, true), false},
		{"same floor", statusEvent("p2", "s1", "Opus 4.8", 41.9, true), false},
		{"next floor", statusEvent("p2", "s1", "Opus 4.8", 42, true), true},
		{"other model", statusEvent("p2", "s1", "Sonnet 5.5", 42, true), true},
		{"meter cleared", statusEvent("p2", "s1", "Sonnet 5.5", 0, false), true},
		{"still null, other number", statusEvent("p2", "s1", "Sonnet 5.5", 12, false), false},
		{"meter back", statusEvent("p2", "s1", "Sonnet 5.5", 12, true), true},
		{"repeated again", statusEvent("p2", "s1", "Sonnet 5.5", 12, true), false},
	}

	for _, s := range steps {
		ver, was := a.storeVer, saves

		if cmd := hook(a, s.ev); cmd != nil {
			t.Errorf("%s: command = %v, want none", s.name, cmd)
		}

		if moved := a.storeVer != ver; moved != s.moves {
			t.Errorf("%s: store version moved = %v, want %v", s.name, moved, s.moves)
		}

		if saved := saves != was; saved != s.moves {
			t.Errorf("%s: saved = %v, want %v", s.name, saved, s.moves)
		}
	}

	t.Run("status session on a pane with no session", func(t *testing.T) {
		a, _, _ := agentApp(t)

		for _, s := range []struct {
			name  string
			ev    harness.Event
			moves bool
		}{
			{"first", statusEvent("p2", "s1", "Opus 4.8", 10, true), true},
			{"repeated", statusEvent("p2", "s1", "Opus 4.8", 10, true), false},
			{"other session", statusEvent("p2", "s2", "Opus 4.8", 10, true), true},
			{"repeated other", statusEvent("p2", "s2", "Opus 4.8", 10, true), false},
		} {
			ver := a.storeVer

			hook(a, s.ev)

			if moved := a.storeVer != ver; moved != s.moves {
				t.Errorf("%s: store version moved = %v, want %v", s.name, moved, s.moves)
			}
		}
	})
}

func TestStatusEventsThatDoNotFit(t *testing.T) {
	t.Parallel()
	a, _, _ := agentApp(t)

	hook(a, harness.Event{Pane: "p2", Name: "SessionStart", Session: "s2", Start: true})
	hook(a, statusEvent("p2", "s2", "Opus 4.8", 50, true))

	before := a.store.State()

	tests := []struct {
		name string
		ev   harness.Event
	}{
		{"unknown pane", statusEvent("p9", "s2", "Haiku 4.5", 10, true)},
		{"empty pane", statusEvent("", "s2", "Haiku 4.5", 10, true)},
		{"shell pane", statusEvent("p1", "s2", "Haiku 4.5", 10, true)},
		{"empty session", statusEvent("p2", "", "Haiku 4.5", 10, true)},
		{"older session", statusEvent("p2", "s1", "Haiku 4.5", 10, true)},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ver := a.storeVer

			if cmd := hook(a, tt.ev); cmd != nil {
				t.Errorf("command = %v, want none", cmd)
			}

			if got := a.store.State(); !reflect.DeepEqual(got.Panes["p1"], before.Panes["p1"]) || !reflect.DeepEqual(got.Panes["p2"], before.Panes["p2"]) || a.storeVer != ver {
				t.Errorf("panes changed: %+v", got.Panes)
			}
		})
	}

	t.Run("closed pane", func(t *testing.T) {
		a.closePane("p2")

		hook(a, statusEvent("p2", "s2", "Opus 4.8", 50, true))

		if _, ok := a.store.State().Panes["p2"]; ok {
			t.Error("a status event reopened a closed pane")
		}
	})
}

func TestStatusFollowsTheSession(t *testing.T) {
	t.Parallel()
	a, _, _ := agentApp(t)

	steps := []struct {
		name    string
		ev      harness.Event
		model   string
		context int
		known   bool
	}{
		{"status before its session starts", statusEvent("p2", "s1", "Opus 4.8", 20, true), "Opus 4.8", 20, true},
		{"its session starts", harness.Event{Pane: "p2", Name: "SessionStart", Session: "s1", Start: true}, "Opus 4.8", 20, true},
		{"a status of the session", statusEvent("p2", "s1", "Opus 4.8", 33.3, true), "Opus 4.8", 33, true},
		{"compact empties the meter", statusEvent("p2", "s1", "Opus 4.8", 0, false), "Opus 4.8", 0, false},
		{"clear starts a new session", harness.Event{Pane: "p2", Name: "SessionStart", Session: "s2", Start: true}, "", 0, false},
		{"a late status of the old session", statusEvent("p2", "s1", "Opus 4.8", 90, true), "", 0, false},
		{"a status of the new session", statusEvent("p2", "s2", "Sonnet 5.5", 5, true), "Sonnet 5.5", 5, true},
		{"the session ends", harness.Event{Pane: "p2", Name: "SessionEnd", End: true}, "", 0, false},
	}

	for _, s := range steps {
		hook(a, s.ev)

		m := a.store.State().Panes["p2"]
		if m.Model != s.model || m.Context != s.context || m.ContextKnown != s.known {
			t.Errorf("%s: model %q context %d known %v, want %q %d %v", s.name, m.Model, m.Context, m.ContextKnown, s.model, s.context, s.known)
		}
	}
}

func usageStatus(pane core.PaneID, session core.SessionID, five, week *harness.RateWindow) harness.Event {
	ev := statusEvent(pane, session, "Opus 4.8", 10, true)
	ev.FiveHour, ev.SevenDay = five, week

	return ev
}

func TestStatusNotesUsage(t *testing.T) {
	t.Parallel()
	a, _, clk := agentApp(t)
	hook(a, harness.Event{Pane: "p2", Name: "SessionStart", Session: "s1", Start: true})
	hook(a, statusEvent("p2", "s1", "Opus 4.8", 10, true))

	saves := 0
	a.save = func(core.State) { saves++ }

	r1, r2 := t0.Add(2*time.Hour), t0.Add(50*time.Hour)
	win := func(p float64, r time.Time) *harness.RateWindow {
		return &harness.RateWindow{Used: p, ResetsAt: r.Unix()}
	}

	steps := []struct {
		name    string
		advance time.Duration
		ev      harness.Event
		moves   bool
	}{
		{"first limits", 0, usageStatus("p2", "s1", win(23.4, r1), win(41.2, r2)), true},
		{"repeated", 0, usageStatus("p2", "s1", win(23.4, r1), win(41.2, r2)), false},
		{"idle rerun, same reset, lower", 0, usageStatus("p2", "s1", win(12, r1), nil), false},
		{"no rate limits", 0, statusEvent("p2", "s1", "Opus 4.8", 10, true), false},
		{"same reset, higher", time.Minute, usageStatus("p2", "s1", win(30, r1), nil), true},
		{"later reset", 0, usageStatus("p2", "s1", win(2, r1.Add(5*time.Hour)), nil), true},
	}

	for _, s := range steps {
		clk.Advance(s.advance)

		ver, was := a.storeVer, saves

		hook(a, s.ev)

		if moved := a.storeVer != ver; moved != s.moves {
			t.Errorf("%s: store version moved = %v, want %v", s.name, moved, s.moves)
		}

		if saved := saves != was; saved != s.moves {
			t.Errorf("%s: saved = %v, want %v", s.name, saved, s.moves)
		}

		switch s.name {
		case "first limits":
			want := core.Usage{
				FiveHour: core.UsageWindow{Used: 23, ResetsAt: r1},
				SevenDay: core.UsageWindow{Used: 41, ResetsAt: r2},
				Changed:  clk.Now(),
			}
			if got := a.store.State().Usage; !got.FiveHour.ResetsAt.Equal(r1) || got.FiveHour.Used != 23 ||
				!got.SevenDay.ResetsAt.Equal(r2) || got.SevenDay.Used != 41 || !got.Changed.Equal(want.Changed) {
				t.Errorf("%s: Usage = %+v, want %+v", s.name, got, want)
			}
		case "idle rerun, same reset, lower", "no rate limits":
			if got := a.store.State().Usage.FiveHour.Used; got != 23 {
				t.Errorf("%s: 5h = %d, want 23", s.name, got)
			}
		case "same reset, higher":
			if got := a.store.State().Usage; got.FiveHour.Used != 30 || !got.Changed.Equal(clk.Now()) {
				t.Errorf("%s: Usage = %+v, want 5h 30 changed %v", s.name, got, clk.Now())
			}
		case "later reset":
			got := a.store.State().Usage
			if got.FiveHour.Used != 2 || !got.FiveHour.ResetsAt.Equal(r1.Add(5*time.Hour)) {
				t.Errorf("%s: 5h = %+v, want 2 resetting %v", s.name, got.FiveHour, r1.Add(5*time.Hour))
			}

			if got.SevenDay.Used != 41 || !got.SevenDay.ResetsAt.Equal(r2) {
				t.Errorf("%s: 7d = %+v, want 41 resetting %v", s.name, got.SevenDay, r2)
			}
		}
	}
}

func TestStatusUsageNeedsTheSession(t *testing.T) {
	t.Parallel()
	a, _, _ := agentApp(t)
	hook(a, harness.Event{Pane: "p2", Name: "SessionStart", Session: "s1", Start: true})

	win := &harness.RateWindow{Used: 23, ResetsAt: t0.Add(2 * time.Hour).Unix()}
	ver := a.storeVer

	hook(a, usageStatus("p2", "s0", win, win))
	hook(a, usageStatus("p1", "s1", win, win))

	if got := a.store.State().Usage; got != (core.Usage{}) {
		t.Errorf("Usage = %+v, want zero", got)
	}

	if a.storeVer != ver {
		t.Error("store version moved")
	}
}

func TestStatusUsageDoesNotReopenThePopover(t *testing.T) {
	t.Parallel()
	a, _, clk := agentApp(t)
	hook(a, harness.Event{Pane: "p2", Name: "SessionStart", Session: "s1", Start: true})
	seedUsage(t, a, usageFive, usageWeek)
	chord(a, 'u')

	if !a.usageShows() {
		t.Fatal("the popover did not open")
	}

	clk.Set(t0.Add(60 * time.Hour))

	fresh := func(r time.Time) *harness.RateWindow {
		return &harness.RateWindow{Used: 5, ResetsAt: r.Unix()}
	}
	hook(a, usageStatus("p2", "s1", fresh(t0.Add(63*time.Hour)), fresh(t0.Add(220*time.Hour))))

	if a.tray.usageOpen || a.usageShows() {
		t.Errorf("open = %v, shows = %v after fresh data, want both false", a.tray.usageOpen, a.usageShows())
	}
}

func TestStatusUsageChangedIsWallClock(t *testing.T) {
	t.Parallel()
	a, _, _ := agentApp(t)
	hook(a, harness.Event{Pane: "p2", Name: "SessionStart", Session: "s1", Start: true})

	a.clock = realClock{}
	win := &harness.RateWindow{Used: 23, ResetsAt: time.Now().Add(time.Hour).Unix()}
	hook(a, usageStatus("p2", "s1", win, nil))

	got := a.store.State().Usage.Changed
	if got.IsZero() || strings.Contains(got.String(), "m=") {
		t.Errorf("Changed = %q, want set and without a monotonic reading", got.String())
	}
}
