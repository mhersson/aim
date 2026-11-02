package ui

import (
	"context"
	"fmt"
	"os"
	"slices"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/mhersson/aim/internal/core"
	"github.com/mhersson/aim/internal/harness"
	"github.com/mhersson/aim/internal/term"
)

// cwdProbeEvery bounds how often a pane's working directory is looked up.
const cwdProbeEvery = 500 * time.Millisecond

// agentGrace is how long a launched agent may take to show Claude in the
// foreground before the kind rule can demote it.
const agentGrace = 10 * time.Second

// paneCwdMsg carries a pane's looked-up working directory and foreground
// process name; each is empty when unknown. due marks any probe outside the
// cwd throttle, hook the one that held hooks started.
type paneCwdMsg struct {
	id   core.PaneID
	p    *pane
	cwd  string
	fg   string
	due  bool
	hook bool
}

// paneKindDueMsg is the probe agentGrace after an agent launch.
type paneKindDueMsg struct {
	id core.PaneID
	p  *pane
}

// paneCwdDueMsg ends the wait before a throttled cwd lookup.
type paneCwdDueMsg struct {
	id core.PaneID
	p  *pane
}

// termStart adapts term.Start to the proc interface; on error it returns a nil
// interface, not a nil *term.Process.
func termStart(ctx context.Context, c term.Command, cols, rows int) (proc, error) {
	p, err := term.Start(ctx, c, cols, rows)
	if err != nil {
		return nil, err
	}

	return p, nil
}

// contentSize is the PTY size for a pane rect: its content area, at least 1x1.
func contentSize(r core.Rect) (cols, rows int) {
	c := contentRect(r)

	return max(c.W, 1), max(c.H, 1)
}

// openPane starts a pane of kind next to the focused one, in its working
// directory, and focuses it. Nothing changes when the process does not start.
func (a *App) openPane(kind core.PaneKind, split core.Split) tea.Cmd {
	return a.openPaneIn(kind, split, a.focusedCwd(a.store.State()))
}

// openPaneIn is openPane in cwd.
func (a *App) openPaneIn(kind core.PaneKind, split core.Split, cwd string) tea.Cmd {
	cmd, _ := a.openPaneAt(kind, split, a.store.State().Tab().Focus, cwd, nil)

	return cmd
}

// focusedCwd is the directory st's focused pane works in, or the launch dir
// without one.
func (a *App) focusedCwd(st core.State) string {
	m := st.Panes[st.Tab().Focus]
	if dir := m.WorkDir(); dir != "" {
		return dir
	}

	return a.cwd
}

// paneRun is the command line a shell pane starts in place of the plain shell.
// The shell reads program as git reads $EDITOR, so it may carry arguments; args
// are passed as given.
type paneRun struct {
	program string
	args    []string
	once    bool // the pane ends with the program, with no shell after it
}

// openPaneAt starts a pane of kind in cwd next to anchor, a pane of the active
// tab, and focuses it. A shell pane with run set starts that command line in
// place of the plain shell and, unless run.once is set, keeps a shell after it.
// It reports whether the pane opened; when it did not, nothing changed.
func (a *App) openPaneAt(kind core.PaneKind, split core.Split, anchor core.PaneID, cwd string, run *paneRun) (tea.Cmd, bool) {
	// Without a size there is no rect to start at.
	if !a.started {
		return nil, false
	}

	st := a.store.State()
	if a.panesFull(st) {
		return nil, false
	}

	act := core.OpenPane{Kind: kind, Cwd: cwd, Split: split, Area: a.bodyArea(), Beside: anchor}
	if kind == core.KindAgent {
		act.Harness = a.harness.ID()
	}

	id := st.NewPaneID()

	// The pane starts at its final size, found by applying the action to a copy.
	cols, rows := 1, 1

	sim := core.NewStore(st)
	if err := sim.Dispatch(act); err == nil {
		if r, ok := core.Arrange(sim.State().Tab(), act.Area, st.Layout).Sizes[id]; ok {
			cols, rows = contentSize(r)
		}
	}

	pn, cwd, err := a.startPaneRunning(id, kind, cwd, cols, rows, "", run)
	if err != nil {
		return nil, false
	}

	act.Cwd = cwd

	if err := a.dispatch(act); err != nil {
		delete(a.panes, id)
		a.closeAsync(pn)
		a.notice = fmt.Sprintf("cannot open %s: %v", kind, err)

		return nil, false
	}

	cmds := []tea.Cmd{pn.wait(), a.relayout()}
	if kind == core.KindAgent {
		cmds = append(cmds, a.kindDueCmd(pn))
	}

	return tea.Batch(cmds...), true
}

// panesFull sets the pane-limit notice and reports true when st holds
// core.MaxPanes panes, before any process is started.
func (a *App) panesFull(st core.State) bool {
	if len(st.Panes) < core.MaxPanes {
		return false
	}

	a.notice = fmt.Sprintf("at most %d panes", core.MaxPanes)

	return true
}

// kindDueCmd schedules the probe at the end of the pane's grace period.
func (a *App) kindDueCmd(p *pane) tea.Cmd {
	return a.tick(agentGrace, func(time.Time) tea.Msg { return paneKindDueMsg{id: p.id, p: p} })
}

// startPane starts the process of pane id at cols x rows and registers the
// pane, in cwd or, when that directory is gone, the launch dir; it returns the
// directory used. An agent resumes session resume when it is set. On failure it
// leaves a notice and registers nothing.
func (a *App) startPane(id core.PaneID, kind core.PaneKind, cwd string, cols, rows int, resume core.SessionID) (*pane, string, error) {
	return a.startPaneRunning(id, kind, cwd, cols, rows, resume, nil)
}

// startPaneRunning is startPane for a shell pane that starts the command line
// run, when it is set, in place of the plain shell.
func (a *App) startPaneRunning(id core.PaneID, kind core.PaneKind, cwd string, cols, rows int, resume core.SessionID, run *paneRun) (*pane, string, error) {
	env := []string{"AIM_PANE=" + string(id)}
	if a.hookSock != "" {
		env = append(env, "AIM_SOCK="+a.hookSock)
	}

	c := term.ShellCommand(a.getenv, a.shell, cwd, env)

	switch {
	case kind == core.KindAgent:
		// Restored and restarted agents use the app's harness; a second harness
		// must pass the pane's own (harness.ByID(Pane.Harness)).
		c = term.AgentCommand(a.getenv, a.shell, cwd, a.harness.Launch(a.hookSettings, resume), env)
	case run != nil && run.once:
		c = term.EditorOnlyCommand(a.getenv, a.shell, cwd, run.program, run.args, env)
	case run != nil:
		c = term.EditorCommand(a.getenv, a.shell, cwd, run.program, run.args, env)
	}

	// On the update loop: user-initiated, fork/exec takes a millisecond or two, and State and the registry stay in step.
	began := time.Now()

	p, err := a.start(a.ctx, c, cols, rows)
	if err != nil && cwd != a.cwd && !usableDir(cwd) {
		// The focused pane's directory is gone; the launch dir always works.
		a.log.Warn("pane cwd unusable, using launch dir", "pane", id, "cwd", cwd, "err", err)
		a.notice = fmt.Sprintf("%s is not available, started in %s", cwd, a.cwd)

		c.Dir = a.cwd
		cwd = a.cwd
		p, err = a.start(a.ctx, c, cols, rows)
	}

	if err != nil {
		a.log.Error("start pane", "kind", kind, "err", err)
		a.notice = fmt.Sprintf("cannot start %s: %v", kind, err)

		return nil, cwd, err
	}

	a.log.Debug("pane started", "pane", id, "kind", kind, "took", time.Since(began))

	ui := a.current().theme.UI
	opts := []paneOption{withColors(ui.Fg, ui.Bg)}

	if a.recordDir != "" {
		tp, err := openTap(a.log, a.recordDir, id, cols, rows, a.clock.Now)
		if err != nil {
			// A recording problem never blocks the pane.
			a.log.Warn("recording disabled for pane", "pane", id, "err", err)
			a.notice = fmt.Sprintf("recording is off for %s: %v", id, err)
		} else {
			opts = append(opts, withTap(tp))
		}
	}

	pn := newPane(id, p, cols, rows, a.clock, opts...)
	if kind == core.KindAgent {
		pn.launched = a.clock.Now()
	}

	a.panes[id] = pn

	return pn, cwd, nil
}

// usableDir reports whether dir is an existing directory; exec reports a
// missing one as a failed fork/exec, so the start error cannot say.
func usableDir(dir string) bool {
	fi, err := os.Stat(dir)

	return err == nil && fi.IsDir()
}

// closePane removes a pane from the state and the registry at once and closes
// its process in the background. An open confirm about it closes.
func (a *App) closePane(id core.PaneID) tea.Cmd {
	handBack := a.composeEdited(id)

	p := a.panes[id]
	delete(a.panes, id)
	a.clearSelectionOf(id)
	a.endCopyOf(id)

	if a.confirm != nil && a.confirm.pane == id {
		a.confirm = nil
	}

	if _, ok := a.store.State().Panes[id]; ok {
		// dispatch logs its own failures.
		_ = a.dispatch(core.ClosePane{ID: id})
	}

	if p == nil {
		return handBack
	}

	a.closeAsync(p)

	return tea.Batch(handBack, a.relayout())
}

// closeAsync closes a pane that has left the registry. Pane.close can take a
// second, so it never runs on the update loop; Close waits for it.
func (a *App) closeAsync(p *pane) {
	a.closing.Go(func() {
		if err := p.close(); err != nil {
			a.log.Warn("close pane", "pane", p.id, "err", err)
		}
	})
}

// act dispatches a layout action and refits the panes to the new geometry.
func (a *App) act(act core.Action) tea.Cmd {
	// dispatch logs its own failures.
	_ = a.dispatch(act)

	return a.relayout()
}

// relayout resizes every pane to its content rect in its own tab's layout,
// hidden tabs included, and renders the visible resized ones at once. A pane the
// layout drops gets 1x1; panes hidden by their tab's zoom keep their size.
func (a *App) relayout() tea.Cmd {
	st := a.store.State()
	rects := make([]map[core.PaneID]core.Rect, len(st.Tabs))

	for i := range st.Tabs {
		rects[i] = a.sizesIn(st, i)
	}

	var cmds []tea.Cmd

	for id, p := range a.panes {
		i, ok := st.TabOf(id)
		if !ok {
			continue
		}

		cols, rows := 1, 1

		if r, ok := rects[i][id]; ok {
			cols, rows = contentSize(r)
		} else if t := st.Tabs[i]; t.Zoomed {
			if _, focused := st.Panes[t.Focus]; focused {
				continue
			}
		}

		if p.resize(cols, rows) {
			a.clearSelectionOf(id)
			a.endCopyOf(id)
			cmds = append(cmds, a.renderPane(p))
		}
	}

	return tea.Batch(cmds...)
}

// hiddenIn reports whether the pane belongs to a tab other than the active one.
func hiddenIn(st core.State, id core.PaneID) bool {
	i, ok := st.TabOf(id)

	return ok && i != st.Active
}

// renderStale renders the panes of the active tab that changed while it was
// hidden. The hold ticks it may arm run after the current update.
func (a *App) renderStale(st core.State) {
	for id, p := range a.panes {
		if !p.stale || hiddenIn(st, id) {
			continue
		}

		p.stale = false

		if cmd := a.renderPane(p); cmd != nil {
			a.later = append(a.later, cmd)
		}
	}
}

// renderPane renders p and, when a sync hold keeps it from drawing, arms one
// tick for the hold's end. A pane in a hidden tab only notes that it is stale.
func (a *App) renderPane(p *pane) tea.Cmd {
	if st := a.store.State(); hiddenIn(st, p.id) {
		p.stale = true

		return nil
	}

	_, held := p.render()

	if p.takeAltSwitch() {
		a.clearSelectionOf(p.id)
		a.endCopyOf(p.id)
	}

	if held.IsZero() {
		a.dropRedrawnSelection(p)
	}

	if held.IsZero() || p.holdArmed {
		return nil
	}

	p.holdArmed = true

	return p.holdTick(held)
}

// onPaneDirty renders a pane with output and waits for its next message.
func (a *App) onPaneDirty(msg paneDirtyMsg) tea.Cmd {
	p, ok := a.livePane(msg.id, msg.p)
	if !ok {
		return nil
	}

	p.lastOut = a.clock.Now()

	cmds := []tea.Cmd{a.renderPane(p), p.wait(), a.probeCwd(p)}

	if text, ok := p.takeClip(); ok {
		cmds = append(cmds, a.clip.copy(text))
	}

	a.dropFallenSelection(p)
	a.syncCopy(p)

	return tea.Batch(cmds...)
}

func (a *App) onHoldExpired(msg holdExpiredMsg) tea.Cmd {
	p, ok := a.livePane(msg.id, msg.p)
	if !ok {
		return nil
	}

	p.holdArmed = false

	return a.renderPane(p)
}

// onPaneExited closes the pane without asking. It never re-arms wait: the exit
// signal stays closed and a re-arm would spin.
func (a *App) onPaneExited(msg paneExitedMsg) tea.Cmd {
	if _, ok := a.livePane(msg.id, msg.p); !ok {
		return nil
	}

	return a.closePane(msg.id)
}

// livePane is the registered pane id, unless the message came from another
// pane that has since been replaced under that id.
func (a *App) livePane(id core.PaneID, from *pane) (*pane, bool) {
	p, ok := a.panes[id]
	if !ok || from != p {
		return nil, false
	}

	return p, true
}

// probeCwd looks up a pane's working directory off the update loop, at most
// once per cwdProbeEvery; a lookup that arrives early is deferred to the end of
// the window so the last change is not missed.
func (a *App) probeCwd(p *pane) tea.Cmd {
	if p.cwdBusy || p.cwdWait {
		return nil
	}

	if since := a.clock.Now().Sub(p.cwdAt); !p.cwdAt.IsZero() && since < cwdProbeEvery {
		p.cwdWait = true

		return tea.Tick(cwdProbeEvery-since, func(time.Time) tea.Msg { return paneCwdDueMsg{id: p.id, p: p} })
	}

	return a.runProbe(p)
}

func (a *App) runProbe(p *pane) tea.Cmd {
	p.cwdBusy = true
	p.cwdAt = a.clock.Now()

	return probe(p, false)
}

// probe reads the pane's cwd and foreground process off the update loop.
func probe(p *pane, due bool) tea.Cmd {
	return func() tea.Msg {
		return paneCwdMsg{id: p.id, p: p, cwd: p.workingDir(), fg: p.foreground(), due: due}
	}
}

// hookProbe is the probe a held hook starts; like the grace probe it leaves the
// cwd throttle alone.
func hookProbe(p *pane) tea.Cmd {
	return func() tea.Msg {
		return paneCwdMsg{id: p.id, p: p, cwd: p.workingDir(), fg: p.foreground(), due: true, hook: true}
	}
}

// onKindDue probes an agent pane at the end of its grace period. It leaves the
// cwd throttle alone.
func (a *App) onKindDue(msg paneKindDueMsg) tea.Cmd {
	p, ok := a.livePane(msg.id, msg.p)
	if !ok {
		return nil
	}

	return probe(p, true)
}

func (a *App) onCwdDue(msg paneCwdDueMsg) tea.Cmd {
	p, ok := a.livePane(msg.id, msg.p)
	if !ok {
		return nil
	}

	p.cwdWait = false

	if p.cwdBusy {
		return nil
	}

	return a.runProbe(p)
}

func (a *App) onCwd(msg paneCwdMsg) tea.Cmd {
	p, ok := a.livePane(msg.id, msg.p)
	if !ok {
		return nil
	}

	if !msg.due {
		p.cwdBusy = false
	}

	if msg.hook {
		p.heldProbe = false
	}

	m := a.store.State().Panes[msg.id]

	// An agent's Cwd is its launch directory; the foreground's cwd may be stale.
	if m.Kind == core.KindShell && msg.cwd != "" && m.Cwd != msg.cwd {
		// dispatch logs its own failures.
		_ = a.dispatch(core.SetPaneCwd{ID: msg.id, Cwd: msg.cwd})
	}

	kind := a.applyKind(p, msg.fg)

	switch {
	case len(p.held) > 0 && a.store.State().Panes[msg.id].Kind == core.KindAgent:
		return tea.Batch(kind, a.replayHeld(p))
	case msg.hook:
		p.held = nil
	}

	return kind
}

// harnessOf is the harness that recognises the pane's agent: its own, else the
// app's when the saved id is unknown.
func (a *App) harnessOf(m core.Pane) harness.Harness {
	if h, ok := harness.ByID(m.Harness); ok && m.Kind == core.KindAgent {
		return h
	}

	return a.harness
}

// applyKind makes the pane follow its foreground process. An unknown name
// changes nothing, and a launched agent keeps its kind until Claude was seen
// and left or the grace period passed. A resuming agent is never demoted before
// the grace period; after it, one whose Claude is not in the foreground is
// restarted fresh instead of following the kind rule.
func (a *App) applyKind(p *pane, fg string) tea.Cmd {
	if fg == "" {
		return nil
	}

	m, ok := a.store.State().Panes[p.id]
	if !ok {
		return nil
	}

	h := a.harnessOf(m)

	if p.resuming && m.Kind == core.KindAgent {
		switch {
		case a.clock.Now().Before(p.launched.Add(agentGrace)):
			p.seen = p.seen || fg == h.Program()

			return nil
		case fg != h.Program():
			return a.restartFresh(p.id)
		default:
			// A refused resume exits within about a second, so Claude still
			// in the foreground now did resume, with or without its hooks.
			p.resuming = false
		}
	}

	switch {
	case m.Kind == core.KindShell && fg == h.Program():
		p.seen = true
		p.launched = time.Time{}
		p.launchDue = true

		// dispatch logs its own failures.
		_ = a.dispatch(core.SetPaneKind{ID: p.id, Kind: core.KindAgent, Harness: h.ID()})
	case m.Kind == core.KindAgent && fg == h.Program():
		p.seen = true
	// An agent pane is either seen or has the launch time startPane recorded.
	case m.Kind == core.KindAgent && (p.seen || !a.clock.Now().Before(p.launched.Add(agentGrace))):
		p.seen = false
		p.launched = time.Time{}
		p.launchDue = false

		// dispatch logs its own failures.
		_ = a.dispatch(core.SetPaneKind{ID: p.id, Kind: core.KindShell})
	}

	return nil
}

// hasAgents reports whether any open pane runs an agent.
func (a *App) hasAgents() bool {
	for _, m := range a.store.State().Panes {
		if m.Kind == core.KindAgent {
			return true
		}
	}

	return false
}

// closeFocused implements pane.close: agents ask first.
func (a *App) closeFocused() tea.Cmd {
	st := a.store.State()

	id := st.Tab().Focus
	m, ok := st.Panes[id]

	switch {
	case !ok:
		return nil
	case m.Kind != core.KindAgent:
		return a.closePane(id)
	}

	a.openConfirm(confirm{
		action: "Close pane",
		prompt: "Close this agent pane?",
		detail: "The agent stops running.",
		pane:   id,
		yes:    func(a *App) tea.Cmd { return a.closePane(id) },
	})

	return nil
}

// quit implements app.quit: open agents ask first.
func (a *App) quit() tea.Cmd {
	if !a.hasAgents() {
		return a.pictures.quit(tea.Quit)
	}

	a.openConfirm(confirm{
		action: "Quit",
		prompt: "Quit AIM?",
		detail: "Agent panes are running and will stop.",
		yes:    func(a *App) tea.Cmd { return a.pictures.quit(tea.Quit) },
	})

	return nil
}

// newTab implements tab.new: a shell in the focused pane's working directory,
// in a new tab that becomes active. Nothing changes when the process does not start.
func (a *App) newTab() tea.Cmd {
	if !a.started {
		return nil
	}

	st := a.store.State()
	if a.panesFull(st) {
		return nil
	}

	cwd := a.focusedCwd(st)
	id := st.NewPaneID()
	cols, rows := contentSize(a.bodyArea())

	pn, cwd, err := a.startPane(id, core.KindShell, cwd, cols, rows, "")
	if err != nil {
		return nil
	}

	if err := a.dispatch(core.NewTab{Kind: core.KindShell, Cwd: cwd}); err != nil {
		delete(a.panes, id)
		a.closeAsync(pn)
		a.notice = fmt.Sprintf("cannot open tab: %v", err)

		return nil
	}

	return tea.Batch(pn.wait(), a.relayout())
}

// closeTab implements tab.close: a tab with agents asks first.
func (a *App) closeTab() tea.Cmd {
	st := a.store.State()
	id := st.Tab().ID

	if !st.TabHasAgents(st.Active) {
		return a.closeTabByID(id)
	}

	a.openConfirm(confirm{
		action: "Close tab",
		prompt: fmt.Sprintf("Close tab %d \"%s\"?", st.Active+1, a.tabInfos()[st.Active].name),
		detail: "Its agent panes will stop running.",
		tab:    id,
		yes:    func(a *App) tea.Cmd { return a.closeTabByID(id) },
	})

	return nil
}

// closeTabByID removes the tab and its panes from the state and the registry
// at once and closes the processes in the background.
func (a *App) closeTabByID(id core.TabID) tea.Cmd {
	st := a.store.State()

	i := slices.IndexFunc(st.Tabs, func(t core.Tab) bool { return t.ID == id })
	if i < 0 {
		return nil
	}

	var (
		closing  []*pane
		handBack []tea.Cmd
	)

	for pid, p := range a.panes {
		if at, ok := st.TabOf(pid); ok && at == i {
			closing = append(closing, p)
			handBack = append(handBack, a.composeEdited(pid))

			delete(a.panes, pid)
		}
	}

	// dispatch logs its own failures.
	_ = a.dispatch(core.CloseTab{ID: id})

	for _, p := range closing {
		a.closeAsync(p)
	}

	return tea.Batch(append(handBack, a.relayout())...)
}

// renameTab implements tab.rename: edit the active tab's custom name in the bar.
func (a *App) renameTab() tea.Cmd {
	t := a.store.State().Tab()

	a.openPrompt(t.ID, t.Name, func(a *App, value string) tea.Cmd {
		return a.act(core.RenameTab{ID: t.ID, Name: strings.TrimSpace(value)})
	})

	return nil
}

// Close closes every pane, concurrently, and waits for closes already running.
// ctx bounds the wait, for the picture loader's workers too; when it ends first
// Close cancels the app context, and exec's context watcher then SIGKILLs each
// remaining child's process group and its terminal's foreground group,
// asynchronously after Close returns.
// It is safe to call twice, and only after the program stopped running Update.
func (a *App) Close(ctx context.Context) error {
	a.endSlide()
	a.removeEditFiles()
	a.bodyFiles.closeAll()
	a.watches.holds.closeAll()

	for id, p := range a.panes {
		delete(a.panes, id)
		a.closeAsync(p)
	}

	done := make(chan struct{})

	go func() {
		a.loader.close()
		a.closing.Wait()
		close(done)
	}()

	defer a.cancel()

	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return fmt.Errorf("close panes: %w", ctx.Err())
	}
}
