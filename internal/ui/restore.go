package ui

import (
	"fmt"
	"path/filepath"
	"strings"

	tea "charm.land/bubbletea/v2"

	"github.com/mhersson/aim/internal/core"
	"github.com/mhersson/aim/internal/harness"
)

// startRestored starts every pane of every tab at its own rect: shells in
// their cwd, agents with their session resumed. A pane that fails to start is
// closed as ClosePane does; if none is left, one shell opens in the launch dir.
// Its dispatches are not saved, and the notices of the start join the one the
// app was given, in order.
func (a *App) startRestored() tea.Cmd {
	a.restoring = true
	defer func() { a.restoring = false }()

	st := a.store.State()
	body := a.bodyArea()

	var notices []string
	if a.notice != "" {
		notices = append(notices, a.notice)
	}

	// collect gathers the notice a start left behind.
	collect := func() {
		if a.notice != "" {
			notices = append(notices, a.notice)
			a.notice = ""
		}
	}

	var cmds []tea.Cmd

	for _, t := range st.Tabs {
		rects := core.Arrange(t, body, st.Layout).Sizes
		if t.Zoomed {
			if _, ok := st.Panes[t.Focus]; ok {
				rects[t.Focus] = body
			}
		}

		for _, id := range t.Panes {
			cols, rows := 1, 1
			if r, ok := rects[id]; ok {
				cols, rows = contentSize(r)
			}

			cmds = append(cmds, a.startRestoredPane(id, st.Panes[id], cols, rows, collect))
		}
	}

	if len(a.store.State().Panes) == 0 {
		a.notice = ""
		cmds = append(cmds, a.openPane(core.SplitVertical))

		collect()
	}

	a.notice = strings.Join(notices, "; ")

	return tea.Batch(append(cmds, a.relayout())...)
}

// startRestoredPane starts one saved pane, or closes it when it cannot start.
func (a *App) startRestoredPane(id core.PaneID, m core.Pane, cols, rows int, collect func()) tea.Cmd {
	a.notice = ""
	pn, used, err := a.startPane(id, m, cols, rows)

	collect()

	if err != nil {
		// dispatch logs its own failures.
		_ = a.dispatch(core.ClosePane{ID: id})

		return nil
	}

	// Codex can wait for hook trust with a live session. The foreground probe
	// decides whether it resumed even when SessionStart never arrives.
	if m.Kind == core.KindAgent && m.Session != "" {
		h := a.harnessOf(m)
		pn.resuming = h.ID() == (harness.Codex{}).ID() || a.settingsFor(h) != ""
	}

	if used != m.Cwd {
		_ = a.dispatch(core.SetPaneCwd{ID: id, Cwd: used})
	}

	if m.Kind == core.KindAgent {
		return tea.Batch(pn.wait(), a.kindDueCmd(pn))
	}

	return pn.wait()
}

// restartFresh replaces the pane's agent with a fresh one in the same pane, at
// the same size, and drops the session it could not resume. The old process
// closes in the background and its late messages are dropped.
func (a *App) restartFresh(id core.PaneID) tea.Cmd {
	old, ok := a.panes[id]
	m, known := a.store.State().Panes[id]

	if !ok || !known || m.Kind != core.KindAgent {
		return nil
	}

	cols, rows := old.size()

	launch := m
	launch.Session = ""

	pn, used, err := a.startPane(id, launch, cols, rows)
	if err != nil {
		// Give up the restart so the kind rule applies and no probe retries it.
		old.resuming = false

		return nil
	}

	a.clearSelectionOf(id)
	a.endCopyOf(id)
	a.closeAsync(old)

	// dispatch logs its own failures.
	_ = a.dispatch(core.SetAgentSession{ID: id})
	_ = a.dispatch(core.SetAgentState{ID: id, State: core.AgentUnknown})

	if used != m.Cwd {
		_ = a.dispatch(core.SetPaneCwd{ID: id, Cwd: used})
	}

	a.notice = fmt.Sprintf("started a fresh %s in %s: its session could not be resumed", a.harnessOf(m).Program(), filepath.Base(used))

	return tea.Batch(pn.wait(), a.kindDueCmd(pn))
}
