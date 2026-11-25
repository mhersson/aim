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
	since      time.Time     // when last's ready run on its thread began
	busyAt     time.Time     // the launch's first working or thinking status
	hookAt     time.Time     // the latest real hook this launch; zero means none yet
	permission bool          // the block came from a PermissionRequest hook, so y and p answer it
}

func codexBusy(state string) bool { return state == "working" || state == "thinking" }

func (c *codexStatus) note(ev harness.Event, now time.Time) {
	if ev.RunState == "ready" && (c.last.RunState != "ready" || ev.Session != c.last.Session) {
		c.since = now
	}

	if c.busyAt.IsZero() && codexBusy(ev.RunState) {
		c.busyAt = now
	}

	c.last = ev
}

// threadOK reports whether the status describes the pane's recorded session;
// a subagent or an unprompted resume target on screen does not count.
func (c *codexStatus) threadOK(m core.Pane) bool {
	return m.Session == "" || c.last.Session == m.Session
}

// checkHooks: the launch's first busy status is agentQuiet old and no hook has arrived.
func (c *codexStatus) checkHooks(now time.Time) bool {
	return c.hookAt.IsZero() && !c.busyAt.IsZero() && now.Sub(c.busyAt) >= agentQuiet
}

// mayIdle reports whether the quiet rule may idle m: working or blocked, with a ready status on its thread.
func (c *codexStatus) mayIdle(m core.Pane) bool {
	return (m.State == core.AgentWorking || m.State == core.AgentBlocked) && c.last.RunState == "ready" && c.threadOK(m)
}

// quiet is the Codex form of the quiet rule: a ready that began no earlier than the latest
// hook and lasted agentQuiet idles the pane, covering a missed Stop or Interrupt.
func (c *codexStatus) quiet(m core.Pane, now time.Time) bool {
	return c.mayIdle(m) && !c.since.Before(c.hookAt) && now.Sub(c.since) >= agentQuiet
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

	if p.codex.hookAt.IsZero() && p.codex.threadOK(m) {
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
