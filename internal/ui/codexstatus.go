package ui

import (
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/mhersson/aim/internal/core"
	"github.com/mhersson/aim/internal/harness"
)

// codexStatus is what a pane keeps of its Codex's status line for the current
// launch. The provider sends only on change, so it survives a suspend.
type codexStatus struct {
	last       harness.Event // latest status from the pane's owner; Session "" means none yet
	since      time.Time     // when last's thread and run class (ready, busy, other) began
	hookAt     time.Time     // the latest real hook this launch
	permission bool          // the block came from a PermissionRequest hook, so y and p answer it
	longBusy   bool          // a busy run lasted agentQuiet before the status left it
}

func codexBusy(state string) bool { return state == "working" || state == "thinking" }

func codexClass(state string) int {
	switch {
	case state == "ready":
		return 1
	case codexBusy(state):
		return 2
	}

	return 0
}

func (c *codexStatus) note(ev harness.Event, now time.Time) {
	if ev.Session != c.last.Session || codexClass(ev.RunState) != codexClass(c.last.RunState) {
		if codexBusy(c.last.RunState) && now.Sub(c.since) >= agentQuiet {
			c.longBusy = true
		}

		c.since = now
	}

	c.last = ev
}

// threadOK reports whether the status describes the pane's recorded session;
// a subagent or an unprompted resume target on screen does not count.
func (c *codexStatus) threadOK(m core.Pane) bool {
	return m.Session == "" || c.last.Session == m.Session
}

// checkHooks: the status line has shown a busy run for agentQuiet and no hook arrived this launch.
func (c *codexStatus) checkHooks(now time.Time, hooksSeen bool) bool {
	return !hooksSeen && (c.longBusy || codexBusy(c.last.RunState) && now.Sub(c.since) >= agentQuiet)
}

// quiet is the Codex form of the quiet rule: a ready that began no earlier than the latest
// hook and lasted agentQuiet idles a working or blocked pane, covering a missed Stop or Interrupt.
func (c *codexStatus) quiet(m core.Pane, now time.Time) bool {
	return (m.State == core.AgentWorking || m.State == core.AgentBlocked) &&
		c.last.RunState == "ready" && c.threadOK(m) &&
		!c.since.Before(c.hookAt) && now.Sub(c.since) >= agentQuiet
}

// onCodexStatus applies a Codex status from the pane's owner, or keeps it until
// the probe that always follows a Codex launch confirms the owner.
func (a *App) onCodexStatus(ev harness.Event) tea.Cmd {
	p := a.panes[ev.Pane]

	m, ok := a.store.State().Panes[ev.Pane]
	if p == nil || !ok {
		return nil
	}

	if m.Kind != core.KindAgent || m.Harness != "codex" || p.ownerPID == 0 || p.ownerPID != ev.OwnerPID {
		p.codexPending = ev

		return nil
	}

	return a.applyCodexStatus(m, p, ev)
}

// applyCodexStatus sets the state from a status until the launch's first hook.
// A status never blocks a pane and never ends a turn, so it sends no notice.
func (a *App) applyCodexStatus(m core.Pane, p *pane, ev harness.Event) tea.Cmd {
	p.codex.note(ev, a.clock.Now())
	a.codexModel(ev.Pane, p)

	var state tea.Cmd

	if !p.hooksSeen && p.codex.threadOK(m) {
		switch {
		case ev.RunState == "ready":
			state = a.setAgentState(ev.Pane, core.AgentIdle)
		case codexBusy(ev.RunState):
			state = a.setAgentState(ev.Pane, core.AgentWorking)
		}
	}

	// A blocked pane whose status turned ready needs the tick the quiet rule runs on.
	return tea.Batch(state, a.agentTickCmd())
}

// codexModel shows the latest status's model and meter. The name, model and
// meter follow the thread on screen, so no thread rule applies.
func (a *App) codexModel(id core.PaneID, p *pane) {
	ev := p.codex.last
	if ev.Session == "" {
		return
	}

	act := core.SetAgentStatus{ID: id, Session: ev.Session, Model: ev.Model, Context: ev.Context, ContextKnown: ev.ContextKnown}
	if !act.Matches(a.store.State().Panes[id]) {
		_ = a.dispatch(act)
	}
}
