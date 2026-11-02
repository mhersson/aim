package ui

import (
	"fmt"
	"math"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/mhersson/aim/internal/core"
	"github.com/mhersson/aim/internal/ui/theme"
)

// noticeNoUsage is what the usage command says with no window to show.
const noticeNoUsage = "no Claude usage yet"

const (
	usageBarCells = 20

	// usageEighths are the left-eighth blocks, one to eight eighths of a cell.
	usageEighths = "\u258F\u258E\u258D\u258C\u258B\u258A\u2589\u2588"
)

// usageBar draws used percent over cells cells: a track on Border, filled from
// the left in fg with eighth-cell blocks. Any use shows at least one eighth.
func usageBar(used, cells int, fg theme.Hex, ui theme.UI) string {
	used = min(max(used, 0), 100)

	eighths := 0
	if used > 0 {
		eighths = max(1, int(math.Round(float64(used*cells*8)/100)))
	}

	blocks := []rune(usageEighths)
	full, part := eighths/8, eighths%8

	var sb strings.Builder

	sb.WriteString(paintRun(fg, ui.Border, false, strings.Repeat(string(blocks[7]), full)))

	rest := cells - full
	if part > 0 && rest > 0 {
		sb.WriteString(paintRun(fg, ui.Border, false, string(blocks[part-1])))

		rest--
	}

	sb.WriteString(paintRun(fg, ui.Border, false, strings.Repeat(" ", max(rest, 0))))

	return sb.String()
}

// untilText is a wait as the popover writes it: "under a minute", "59 min",
// "2h 5m" or "2d 4h".
func untilText(d time.Duration) string {
	switch {
	case d < time.Minute:
		return "under a minute"
	case d < time.Hour:
		return fmt.Sprintf("%d min", int(d/time.Minute))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh %dm", int(d/time.Hour), int(d%time.Hour/time.Minute))
	}

	return fmt.Sprintf("%dd %dh", int(d/(24*time.Hour)), int(d%(24*time.Hour)/time.Hour))
}

// resetText is "resets in 2d 4h (Sat 04:00)": the wait and the reset's clock
// time in now's zone, with the weekday when it falls on another day than now.
func resetText(at, now time.Time) string {
	at = at.In(now.Location())

	layout := "15:04"
	if ay, am, ad := at.Date(); now.Day() != ad || now.Month() != am || now.Year() != ay {
		layout = "Mon 15:04"
	}

	return fmt.Sprintf("resets in %s (%s)", untilText(at.Sub(now)), at.Format(layout))
}

// updatedText is how long ago the figures last changed.
func updatedText(changed, now time.Time) string {
	d := now.Sub(changed)

	switch {
	case d < time.Minute:
		return "updated just now"
	case d < time.Hour:
		return fmt.Sprintf("updated %d min ago", int(d/time.Minute))
	case d < 24*time.Hour:
		return fmt.Sprintf("updated %dh ago", int(d/time.Hour))
	}

	return fmt.Sprintf("updated %dd ago", int(d/(24*time.Hour)))
}

// usagePopover draws the usage box and says where it goes: top border on row 1,
// left edge at the module's first cell, kept one cell off the right edge; with
// the module off the bar it hangs at the right edge. ok is false when it does
// not show or the screen is too narrow for a box.
func (a *App) usagePopover(lk look) (lines []string, r core.Rect, ok bool) {
	if !a.usageShows() {
		return nil, core.Rect{}, false
	}

	now := a.clock.Now()
	wins := usageWindows(a.store.State().Usage, now)
	ui := lk.theme.UI
	pad := 0

	for _, w := range wins {
		pad = max(pad, len(w.name)+2)
	}

	on := func(fg theme.Hex, bold bool, s string) string { return paintRun(fg, ui.Panel, bold, s) }
	space := func(n int) string { return on(ui.Fg, false, strings.Repeat(" ", n)) }

	var rows []string

	for _, w := range wins {
		_, level := meterOf(w.Used)
		fg, bold := level.colour(ui)

		rows = append(rows,
			on(ui.Fg, true, w.name)+space(pad-len(w.name))+usageBar(w.Used, usageBarCells, fg, ui)+
				on(fg, bold, fmt.Sprintf(" %3d%%", w.Used)),
			space(pad)+on(ui.Muted, false, resetText(w.ResetsAt, now)),
			"",
		)
	}

	rows = append(rows,
		on(ui.Subtle, false, updatedText(a.store.State().Usage.Changed, now)),
		"",
		on(ui.Key, true, "esc")+on(ui.Muted, false, " close"),
	)

	widest := 0
	for _, row := range rows {
		widest = max(widest, ansi.StringWidth(row))
	}

	w := min(widest+6, a.width)
	if w < 4 {
		return nil, core.Rect{}, false
	}

	st := lk.styles
	top, row, bottom := boxFrame(w, " "+a.titled(iconUsage, "Claude usage")+" ", boxStyle{border: st.popupBorder, title: st.popupHead}, st)

	lines = append(lines, top)
	for _, content := range rows {
		lines = append(lines, row(content))
	}

	lines = append(lines, bottom)

	x := a.width - 1 - w

	p := planBar(a.tabInfos(), a.store.State().Active, a.trayModules(), a.width, a.icons)
	if mx, _, found := trayModuleAt(p, a.width, a.icons, modUsage); found {
		x = min(mx, x)
	}

	return lines, core.Rect{X: max(x, 0), Y: 1, W: w, H: len(lines)}, true
}

// usageShows reports whether the popover is open with a window to show.
func (a *App) usageShows() bool {
	return a.tray.usageOpen && len(usageWindows(a.store.State().Usage, a.clock.Now())) > 0
}

// usageToggle closes the popover when it shows, else opens it; with nothing to
// show it says so.
func (a *App) usageToggle() tea.Cmd {
	switch {
	case a.usageShows():
		a.tray.usageOpen = false
	case len(usageWindows(a.store.State().Usage, a.clock.Now())) == 0:
		a.notice = noticeNoUsage
	default:
		a.tray.usageOpen = true
	}

	return nil
}

// handleUsageKey takes the keys while the popover shows: esc closes it and
// everything else is dropped.
func (a *App) handleUsageKey(msg tea.KeyPressMsg) tea.Cmd {
	if msg.String() == "esc" {
		a.tray.usageOpen = false
	}

	return nil
}
