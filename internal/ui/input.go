package ui

import (
	"time"

	tea "charm.land/bubbletea/v2"
	uv "github.com/charmbracelet/ultraviolet"

	"github.com/mhersson/aim/internal/core"
)

// The esc guard's waits: how long esc must stay up before an esc reaches a
// pane again. The first, after the esc that handed the pane the keyboard,
// outlasts the common key repeat delays (500 to 660 ms), so a held esc's first
// repeat is dropped too; after a dropped esc the wait is escQuiet.
const (
	escCloseWait = 750 * time.Millisecond
	escQuiet     = 500 * time.Millisecond
)

// escGuard is the esc guard's state: the last esc it counted and how long esc
// must stay up after it. The zero value runs no guard.
type escGuard struct {
	at   time.Time
	wait time.Duration
}

// drops reports whether an esc at now comes within the wait.
func (g escGuard) drops(now time.Time) bool { return g.wait > 0 && now.Sub(g.at) < g.wait }

// handleKey routes a key press and runs the esc guard. In Claude Code one esc
// interrupts a turn, so after an esc hands a pane the keyboard, an esc bound
// for a pane is dropped until escCloseWait passes with no esc, or escQuiet
// after a dropped one; each one dropped restarts the wait, and any other key
// ends the guard. Esc is matched by its code, since a terminal can deliver two
// presses in one read as alt+esc.
func (a *App) handleKey(msg tea.KeyPressMsg) tea.Cmd {
	// An esc cancels a plan read that has not answered yet, even a dropped one.
	if msg.String() == "esc" {
		a.planSeq++
	}

	if msg.Code != tea.KeyEscape {
		a.esc = escGuard{}

		return a.routeKey(msg)
	}

	now := a.clock.Now()

	if !a.paneHasKeys() {
		a.esc = escGuard{}
		cmd := a.routeKey(msg)

		if a.paneHasKeys() {
			a.esc = escGuard{at: now, wait: escCloseWait}
		}

		return cmd
	}

	if a.esc.drops(now) {
		a.esc = escGuard{at: now, wait: escQuiet}
		a.notice = ""

		return nil
	}

	a.esc = escGuard{}

	return a.routeKey(msg)
}

// routeKey routes a key press: an open confirm first, then an open prompt, then
// copy mode, then the open overlay, compose box, merge box, plan overlay,
// conversation, checks, PR list or worktree picker, then the leader state
// machine, then the usage popover while it shows, then the rail while it has
// the keyboard, then the review drawer while it has it, then the focused pane.
func (a *App) routeKey(msg tea.KeyPressMsg) tea.Cmd {
	if a.confirm != nil {
		return a.handleConfirmKey(msg)
	}

	if a.prompt != nil {
		return a.handlePromptKey(msg)
	}

	if a.copy != nil {
		return a.handleCopyKey(msg)
	}

	if a.overlay != nil {
		// The palette answers esc with a ClosedMsg a later update applies; closing
		// here hands the pane the keyboard within this key, where the esc guard sees it.
		if msg.String() == "esc" {
			a.closeOverlay()

			return nil
		}

		return a.updateOverlay(msg)
	}

	if a.composeShown() {
		return a.handleComposeKey(msg)
	}

	if a.merge != nil {
		return a.handleMergeKey(msg)
	}

	if a.plan != nil {
		return a.handlePlanKey(msg)
	}

	if a.conv != nil {
		return a.handleConvKey(msg)
	}

	if a.checks != nil {
		return a.handleChecksKey(msg)
	}

	if a.prList != nil {
		return a.handlePRListKey(msg)
	}

	if a.picker.open != nil {
		return a.handlePickerKey(msg)
	}

	a.notice = ""
	if cmd, ok := a.runLeader(msg); ok {
		return cmd
	}

	if a.usageShows() {
		return a.handleUsageKey(msg)
	}

	if a.rail.focused {
		return a.handleRailKey(msg)
	}

	if a.drawerHasKeys() {
		return a.handleDrawerKey(msg)
	}

	a.keyToFocused(msg)

	return nil
}

// paneHasKeys reports whether the next key goes to the focused pane: nothing
// before it in routeKey's order takes it. It must follow that order.
func (a *App) paneHasKeys() bool {
	return !a.aboveDrawerModalOpen() && !a.composeShown() && !a.pending && !a.usageShows() && !a.rail.focused && !a.drawerHasKeys()
}

// handlePaste sends a paste to the topmost text input: prompt, overlay, compose box,
// plan search, picker, then the rail's or drawer's search, else the focused pane.
// Confirm, copy mode, checks, merge, the PR list and the usage popover drop it;
// the conversation takes it only into its search prompt.
func (a *App) handlePaste(msg tea.PasteMsg) tea.Cmd {
	if a.confirm != nil {
		return nil
	}

	if a.prompt != nil {
		return a.updatePrompt(msg)
	}

	if a.copy != nil {
		return nil
	}

	if a.overlay != nil {
		return a.updateOverlay(msg)
	}

	if a.composeShown() {
		return a.handleComposePaste(msg)
	}

	if a.plan != nil {
		a.planPaste(msg.Content)

		return nil
	}

	if a.conv != nil {
		a.convPaste(msg.Content)

		return nil
	}

	if a.checks != nil || a.merge != nil || a.prList != nil {
		return nil
	}

	if a.picker.open != nil {
		a.picker.open.edit(msg)

		return nil
	}

	a.endLeader()

	if a.usageShows() {
		return nil
	}

	if a.rail.focused {
		a.railPaste(msg.Content)

		return nil
	}

	if a.drawerHasKeys() {
		a.drawerPaste(msg.Content)

		return nil
	}

	a.sendToFocused(func(p *pane) {
		a.clearSelectionOf(p.id)
		p.sendPaste(msg.Content)
	})

	return nil
}

// keyToFocused sends a key to the focused pane; typing ends that pane's selection.
func (a *App) keyToFocused(msg tea.KeyPressMsg) {
	a.sendToFocused(func(p *pane) {
		a.clearSelectionOf(p.id)
		a.answerDialog(p, msg.String())
		p.sendKey(uv.KeyPressEvent(msg))
	})
}

// sendToFocused runs f on the focused pane; with none it does nothing.
func (a *App) sendToFocused(f func(*pane)) {
	if p, ok := a.panes[a.store.State().Tab().Focus]; ok {
		f(p)
	}
}

// reportFocusChange moves focus reports from the old pane to the new one while
// the outer terminal is focused.
func (a *App) reportFocusChange(from, to core.PaneID) {
	if from == to || !a.outerFocused {
		return
	}

	if p, ok := a.panes[from]; ok {
		p.sendFocus(false)
	}

	if p, ok := a.panes[to]; ok {
		p.sendFocus(true)
	}
}

// modalOpen reports whether any modal owns the keyboard: a confirm, prompt,
// copy mode, the palette or another overlay, the compose box, the merge box,
// the plan, the conversation, the checks, the pull request list, the worktree
// picker or the drawer's worktree list or link chooser.
func (a *App) modalOpen() bool { return a.composeShown() || a.otherModalOpen() }

// otherModalOpen is modalOpen without the compose box.
func (a *App) otherModalOpen() bool {
	return a.aboveDrawerModalOpen() || a.drawer.list != nil || a.drawer.links != nil
}

// aboveDrawerModalOpen is otherModalOpen without the drawer's worktree list and
// link chooser, which the leader opens overlays over.
func (a *App) aboveDrawerModalOpen() bool {
	return a.confirm != nil || a.prompt != nil || a.copy != nil || a.overlay != nil || a.merge != nil ||
		a.plan != nil || a.conv != nil || a.checks != nil || a.prList != nil || a.picker.open != nil
}
