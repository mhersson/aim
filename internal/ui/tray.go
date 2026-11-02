package ui

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/mhersson/aim/internal/core"
	"github.com/mhersson/aim/internal/ui/theme"
)

const (
	loadEvery = 2 * time.Second

	// loadHistory is the CPU samples the sparkline covers: two per cell.
	loadHistory = 10
	sparkCells  = loadHistory / 2
)

// loadSample is one reading of the machine: CPU percent and memory in use.
type loadSample struct {
	CPU int
	Mem uint64
}

// loadInfo is what the load module shows: the last CPU samples and the memory text.
type loadInfo struct {
	CPU []int
	Mem string
}

// loadSampler reads the machine load; nil means the platform has none.
type loadSampler interface {
	Sample(ctx context.Context) (loadSample, error)
}

// trayState is the sampled data behind the tray and the usage popover's open
// flag. Only Update changes it; the samplers run in commands and report back by
// message.
type trayState struct {
	on    bool // sampling is armed; set by Init
	clock string
	load  *loadInfo

	usageOpen bool // the usage popover is open; it shows only while a window does
}

type (
	clockMsg struct{ text string }
	loadMsg  struct {
		sample loadSample
		err    error
	}
)

// sleepCtx waits d and reports false when ctx ended first.
func sleepCtx(ctx context.Context, d time.Duration) bool {
	t := time.NewTimer(d)
	defer t.Stop()

	select {
	case <-t.C:
		return true
	case <-ctx.Done():
		return false
	}
}

func clockText(t time.Time) string { return t.Format("15:04") }

// untilNextMinute is the wait to the next minute boundary; on the boundary it
// is a whole minute.
func untilNextMinute(now time.Time) time.Duration {
	return time.Minute - time.Duration(now.Second())*time.Second - time.Duration(now.Nanosecond())
}

// initTray arms every sampler.
func (a *App) initTray() tea.Cmd {
	a.tray.on = true

	cmds := []tea.Cmd{a.clockCmd(false)}
	if a.load != nil {
		cmds = append(cmds, a.loadCmd(false))
	}

	return tea.Batch(cmds...)
}

// clockWake decides what a clock wake-up does: the new text, or how long to
// wait again when the minute has not changed (a timer that fired early).
func clockWake(now time.Time, last string) (text string, again time.Duration) {
	if text = clockText(now); text == last {
		return "", untilNextMinute(now)
	}

	return text, 0
}

// clockCmd waits for the next minute boundary (at once when !wait) and reads the time.
func (a *App) clockCmd(wait bool) tea.Cmd {
	ctx, clk, last := a.ctx, a.clock, a.tray.clock

	return func() tea.Msg {
		if !wait {
			return clockMsg{text: clockText(clk.Now())}
		}

		d := untilNextMinute(clk.Now())

		for {
			if !sleepCtx(ctx, d) {
				return nil
			}

			text, again := clockWake(clk.Now(), last)
			if again == 0 {
				return clockMsg{text: text}
			}

			d = again
		}
	}
}

func (a *App) onClock(msg clockMsg) tea.Cmd {
	a.tray.clock = msg.text

	if a.tray.usageOpen && !a.usageShows() {
		a.tray.usageOpen = false
	}

	return a.clockCmd(true)
}

// loadCmd waits loadEvery (not at all when !wait) and takes one sample.
func (a *App) loadCmd(wait bool) tea.Cmd {
	ctx, s := a.ctx, a.load

	return func() tea.Msg {
		if wait && !sleepCtx(ctx, loadEvery) {
			return nil
		}

		sample, err := s.Sample(ctx)

		return loadMsg{sample: sample, err: err}
	}
}

func (a *App) onLoad(msg loadMsg) tea.Cmd {
	if a.ctx.Err() != nil {
		return nil
	}

	if msg.err != nil {
		a.log.Debug("load sample", "err", msg.err)

		return a.loadCmd(true)
	}

	hist := []int{msg.sample.CPU}
	if a.tray.load != nil {
		hist = append(slices.Clone(a.tray.load.CPU), msg.sample.CPU)
	}

	if len(hist) > loadHistory {
		hist = hist[len(hist)-loadHistory:]
	}

	next := loadInfo{CPU: hist, Mem: formatMem(msg.sample.Mem)}
	a.tray.load = &next

	return a.loadCmd(true)
}

// formatMem writes bytes as 512M, 9.8G or 128G.
func formatMem(b uint64) string {
	const gib = 1 << 30

	switch {
	case b < gib:
		return fmt.Sprintf("%dM", b>>20)
	case b < 100*gib:
		return fmt.Sprintf("%.1fG", float64(b)/gib)
	}

	return fmt.Sprintf("%dG", b/gib)
}

// sparkColumns are the braille dots of the left and right column, bottom to top.
func sparkColumns() (left, right [4]rune) {
	return [4]rune{0x40, 0x04, 0x02, 0x01}, [4]rune{0x80, 0x20, 0x10, 0x08}
}

// sparkline draws the last ten CPU samples as five braille cells, two samples
// per cell, the newer on the right. A sample fills one dot per started quarter
// of CPU, at least one; samples not taken yet leave their column empty.
func sparkline(samples []int) string {
	if len(samples) > loadHistory {
		samples = samples[len(samples)-loadHistory:]
	}

	padded := make([]int, loadHistory-len(samples), loadHistory)
	for i := range padded {
		padded[i] = -1
	}

	padded = append(padded, samples...)

	column := func(pct int, dots [4]rune) rune {
		if pct < 0 {
			return 0
		}

		var bits rune

		for _, d := range dots[:min(max((pct+24)/25, 1), 4)] {
			bits |= d
		}

		return bits
	}

	var sb strings.Builder

	left, right := sparkColumns()

	for i := range sparkCells {
		sb.WriteRune(0x2800 + column(padded[2*i], left) + column(padded[2*i+1], right))
	}

	return sb.String()
}

// trayRun is a piece of text in one colour on the Surface ground.
type trayRun struct {
	text string
	fg   theme.Hex
	bold bool
}

// surfaceModule joins runs into a module whose width is their text's.
func surfaceModule(id string, ui theme.UI, runs ...trayRun) trayModule {
	w := 0
	for _, r := range runs {
		w += ansi.StringWidth(r.text)
	}

	return trayModule{id: id, width: w, render: func() string {
		var sb strings.Builder
		for _, r := range runs {
			sb.WriteString(paint(r.fg, ui.Surface, r.bold).Render(r.text))
		}

		return sb.String()
	}}
}

// trayModules lists the modules that have data, left to right.
func (a *App) trayModules() []trayModule {
	ui := a.current().theme.UI
	icons := a.icons

	var mods []trayModule

	st := a.store.State()
	if m := st.Tab().Mode; m != "" {
		mods = append(mods, layoutModule(m, icons, ui))
	}

	// A draft, merged or closed pull request has no module; the card shows it.
	if pr, ok := a.prOfPane(st, st.Tab().Focus); ok && pr.State == prOpenState && !pr.Draft {
		mods = append(mods, prModule(pr, icons, ui))
	}

	if m, ok := usageModule(st.Usage, a.clock.Now(), icons, ui); ok {
		mods = append(mods, m)
	}

	if l := a.tray.load; l != nil && len(l.CPU) > 0 {
		mods = append(mods, loadModule(*l, icons, ui))
	}

	if a.tray.clock != "" {
		mods = append(mods, clockModule(a.tray.clock, icons, ui))
	}

	return mods
}

// layoutModule shows the active tab's layout mode: its icon in Accent, then the
// name.
func layoutModule(m core.LayoutMode, icons bool, ui theme.UI) trayModule {
	if !icons {
		return surfaceModule(modLayout, ui, trayRun{text: " layout " + string(m) + " ", fg: ui.Fg})
	}

	icon := iconLayoutBSP

	switch m {
	case core.ModeMaster:
		icon = iconLayoutMaster
	case core.ModeScrolling:
		icon = iconLayoutScrolling
	}

	return surfaceModule(modLayout, ui,
		trayRun{text: " ", fg: ui.Fg},
		trayRun{text: icon, fg: ui.Accent},
		trayRun{text: " " + string(m) + " ", fg: ui.Fg},
	)
}

// prModule shows an open pull request: its number and the state of its checks,
// as the card writes them.
func prModule(pr core.PRInfo, icons bool, ui theme.UI) trayModule {
	runs := []trayRun{{text: " ", fg: ui.Fg}}

	for _, r := range prTextOf(pr, icons, ui).runs(prStage{counts: true, glyph: true}) {
		runs = append(runs, trayRun{text: r.text, fg: r.fg, bold: r.bold})
	}

	return surfaceModule(modPR, ui, append(runs, trayRun{text: " ", fg: ui.Fg})...)
}

func loadModule(l loadInfo, icons bool, ui theme.UI) trayModule {
	lead := trayRun{text: " cpu ", fg: ui.Muted}
	if icons {
		lead = trayRun{text: " " + iconLoad + " ", fg: ui.Load}
	}

	mem := "  " + iconMem + " " + l.Mem + " "
	if !icons {
		mem = "  mem " + l.Mem + " "
	}

	return surfaceModule(modLoad, ui,
		lead,
		trayRun{text: sparkline(l.CPU), fg: ui.Load},
		trayRun{text: fmt.Sprintf(" %2d%%", l.CPU[len(l.CPU)-1]), fg: ui.Fg},
		trayRun{text: mem, fg: ui.Muted},
	)
}

// clockModule is the clock's own segment; the tray draws the edge before it.
func clockModule(text string, icons bool, ui theme.UI) trayModule {
	label := " " + text + " "
	if icons {
		label = " " + iconClock + " " + text + " "
	}

	return trayModule{id: modClock, width: ansi.StringWidth(label), render: func() string {
		return paint(ui.OnAccent, ui.Clock, true).Render(label)
	}}
}
