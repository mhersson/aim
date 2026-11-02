package ui

import (
	"fmt"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/mhersson/aim/internal/core"
	"github.com/mhersson/aim/internal/ui/theme"
)

// usageWin is one shown rate-limit window with its tray label and its name.
type usageWin struct {
	label, name string
	core.UsageWindow
}

// usageWindows lists the windows of u that show at now, the 5-hour one first.
func usageWindows(u core.Usage, now time.Time) []usageWin {
	var wins []usageWin

	if u.FiveHour.Shown(now) {
		wins = append(wins, usageWin{"5h", "Session", u.FiveHour})
	}

	if u.SevenDay.Shown(now) {
		wins = append(wins, usageWin{"7d", "Week", u.SevenDay})
	}

	return wins
}

// usageModule shows each shown window's percent in the context meter's
// colours; the icon takes the hottest window's. It reports false when no
// window shows.
func usageModule(u core.Usage, now time.Time, icons bool, ui theme.UI) (trayModule, bool) {
	wins := usageWindows(u, now)
	if len(wins) == 0 {
		return trayModule{}, false
	}

	runs := []trayRun{{text: " ", fg: ui.Fg}}

	if icons {
		hottest := wins[0].Used
		for _, w := range wins[1:] {
			hottest = max(hottest, w.Used)
		}

		_, level := meterOf(hottest)
		fg, _ := level.colour(ui)
		runs = append(runs, trayRun{text: iconUsage, fg: fg}, trayRun{text: " ", fg: ui.Fg})
	}

	for i, w := range wins {
		if i > 0 {
			runs = append(runs, trayRun{text: "  ", fg: ui.Fg})
		}

		_, level := meterOf(w.Used)
		fg, bold := level.colour(ui)
		runs = append(runs,
			trayRun{text: w.label + " ", fg: ui.Muted},
			trayRun{text: fmt.Sprintf("%2d%%", w.Used), fg: fg, bold: bold},
		)
	}

	return surfaceModule(modUsage, ui, append(runs, trayRun{text: " ", fg: ui.Fg})...), true
}

// usageClick takes a press while the popover shows: inside its box nothing
// happens, anywhere else it closes the popover and goes no further.
func (a *App) usageClick(m tea.Mouse) {
	_, r, ok := a.usagePopover(a.current())
	if ok && m.X >= r.X && m.X < r.X+r.W && m.Y >= r.Y && m.Y < r.Y+r.H {
		return
	}

	a.tray.usageOpen = false
}
