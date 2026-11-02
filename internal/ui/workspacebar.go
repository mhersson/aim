package ui

import (
	"path/filepath"
	"slices"
	"strconv"
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/mhersson/aim/internal/core"
	"github.com/mhersson/aim/internal/ui/theme"
)

// Tray module IDs, left to right as the tray shows them.
const (
	modLayout = "layout"
	modPR     = "pr"
	modUsage  = "usage"
	modLoad   = "load"
	modClock  = "clock"
)

// trayGap is the least number of bar cells between the tabs and the tray.
const trayGap = 2

// trayModule is one tray entry. render returns its styled cells, exactly width
// wide: ` icon value ` on Surface, or the clock's own Clock segment.
type trayModule struct {
	id     string
	width  int
	render func() string
}

// tabInfo is what the bar needs to know about a tab; name is already resolved.
type tabInfo struct {
	n        int // 1-based position
	name     string
	agent    bool
	state    core.AgentState // the most urgent state among the tab's agents
	renaming bool            // the segment is a text input holding name
}

// tabLevel is how much of a tab segment shows.
type tabLevel int

const (
	levelFull   tabLevel = iota // ` N icon name `
	levelIcon                   // ` N icon `
	levelNumber                 // ` N `
)

// planTab is a visible tab segment.
type planTab struct {
	idx   int
	level tabLevel
}

// barPlan is what the workspace bar shows: the visible tab segments, the tray
// modules that stayed, and the cells each takes.
type barPlan struct {
	tabs         []planTab
	tray         []trayModule
	tabsW, trayW int
}

// segWidth is the cells of a tab segment, its edge excluded.
func segWidth(t tabInfo, level tabLevel, icons bool) int {
	d := len(strconv.Itoa(t.n))

	switch {
	case level == levelNumber:
		return d + 2
	case !icons:
		return d + ansi.StringWidth(t.name) + 3
	case level == levelIcon:
		return d + 4
	}

	return d + ansi.StringWidth(t.name) + 5
}

// tabsWidth is the cells of the visible segments with their edges: one edge
// cell after each with icons, one bar cell between them without.
func tabsWidth(tabs []tabInfo, vis []planTab, icons bool) int {
	w := 0
	for _, v := range vis {
		w += segWidth(tabs[v.idx], v.level, icons)
	}

	if icons {
		return w + len(vis)
	}

	return w + max(len(vis)-1, 0)
}

// trayWidth is the cells of the tray: the opening edge, the modules with one
// divider cell between them, and the clock's own edge. Without icons there are
// no edges.
func trayWidth(mods []trayModule, icons bool) int {
	if len(mods) == 0 {
		return 0
	}

	w := 0
	for _, m := range mods {
		w += m.width
	}

	w += len(mods) - 1

	if !icons {
		return w
	}

	// Each module after the first follows a divider or the clock's edge; the
	// first follows the opening edge.
	return w + 1
}

// planBar decides what the bar shows in width cells. As the width shrinks it
// gives way in this order: load, usage, PR and the layout mode leave the tray;
// inactive tab names shrink to the icon and then the number, longest names
// first; the clock leaves; the active name shrinks the same way; finally the
// segments farthest from the active tab are left out.
func planBar(tabs []tabInfo, active int, mods []trayModule, width int, icons bool) barPlan {
	active = min(max(active, 0), len(tabs)-1)
	tray := slices.Clone(mods)
	vis := make([]planTab, len(tabs))

	for i := range vis {
		vis[i] = planTab{idx: i}
	}

	measure := func() (int, int) { return tabsWidth(tabs, vis, icons), trayWidth(tray, icons) }
	fits := func() bool {
		tw, rw := measure()
		if rw > 0 {
			tw += trayGap + rw
		}

		return tw <= width
	}
	drop := func(id string) func() bool {
		return func() bool {
			i := slices.IndexFunc(tray, func(m trayModule) bool { return m.id == id })
			if i < 0 {
				return false
			}

			tray = slices.Delete(tray, i, i+1)

			return true
		}
	}
	// shrink lowers the longest name among the segments selected by active.
	shrink := func(to tabLevel, isActive bool) func() bool {
		return func() bool {
			best := -1

			for i, v := range vis {
				if (v.idx == active) != isActive || v.level >= to {
					continue
				}

				if best < 0 || ansi.StringWidth(tabs[v.idx].name) > ansi.StringWidth(tabs[vis[best].idx].name) {
					best = i
				}
			}

			if best < 0 {
				return false
			}

			vis[best].level = to

			return true
		}
	}
	// omit leaves out the farthest segment; on equal distance the right-hand one goes first.
	omit := func() bool {
		best, far := -1, 0

		for i, v := range vis {
			if d := abs(v.idx - active); v.idx != active && d >= far {
				best, far = i, d
			}
		}

		if best < 0 {
			return false
		}

		vis = slices.Delete(vis, best, best+1)

		return true
	}

	stages := []func() bool{drop(modLoad), drop(modUsage), drop(modPR), drop(modLayout)}
	if icons {
		stages = append(stages, shrink(levelIcon, false))
	}

	stages = append(stages, shrink(levelNumber, false), drop(modClock))
	if icons {
		stages = append(stages, shrink(levelIcon, true))
	}

	stages = append(stages, shrink(levelNumber, true), omit)

	for _, stage := range stages {
		for !fits() {
			if !stage() {
				break
			}
		}
	}

	tw, rw := measure()

	return barPlan{tabs: vis, tray: tray, tabsW: tw, trayW: rw}
}

func abs(n int) int {
	if n < 0 {
		return -n
	}

	return n
}

// tabInfos lists the tabs with their resolved names.
func (a *App) tabInfos() []tabInfo {
	st := a.store.State()
	if len(st.Tabs) == 0 {
		return []tabInfo{{n: 1, name: "shell"}}
	}

	out := make([]tabInfo, len(st.Tabs))

	for i, t := range st.Tabs {
		name := t.Name
		if name == "" {
			if cwd := st.Panes[t.Focus].WorkDir(); cwd != "" {
				name = printable(filepath.Base(cwd))
			}
		}

		if name == "" {
			name = "shell"
		}

		out[i] = tabInfo{n: i + 1, name: name, agent: st.TabHasAgents(i), state: st.TabAgentState(i)}
	}

	if a.prompt != nil && a.prompt.kind == promptRename && st.Tab().ID == a.prompt.tab {
		out[st.Active].name = printable(a.prompt.input.Value())
		out[st.Active].renaming = true
	}

	return out
}

// workspaceBar draws row 0: the tab segments and the tray, exactly width cells.
func (a *App) workspaceBar(width int) string {
	return renderBar(a.tabInfos(), a.store.State().Active, a.trayModules(), width, a.icons, a.current().theme.UI)
}

func paint(fg, bg theme.Hex, bold bool) lipgloss.Style {
	return lipgloss.NewStyle().Foreground(fg).Background(bg).Bold(bold)
}

// paintRun is paint(fg, bg, bold).Render(text), byte for byte. Render rewrites
// tabs and CRLFs and styles each line apart; for text without a tab, CR or LF
// it only wraps text in bold, foreground and background SGR, built here directly.
func paintRun(fg, bg theme.Hex, bold bool, text string) string {
	if strings.ContainsAny(text, "\t\r\n") {
		return paint(fg, bg, bold).Render(text)
	}

	var st ansi.Style
	if bold {
		st = st.Bold()
	}

	return st.ForegroundColor(fg).BackgroundColor(bg).Styled(text)
}

// renderBar plans the bar for width and draws it.
func renderBar(tabs []tabInfo, active int, mods []trayModule, width int, icons bool, ui theme.UI) string {
	if width <= 0 {
		return ""
	}

	p := planBar(tabs, active, mods, width, icons)
	left := renderTabs(p, tabs, active, icons, ui)

	right := ""
	if p.trayW > 0 && p.tabsW+trayGap+p.trayW <= width {
		right = renderTray(p.tray, icons, ui)
	}

	ground := lipgloss.NewStyle().Background(ui.Bar)
	gap := width - p.tabsW - p.trayW

	if right == "" {
		gap = width - p.tabsW
	}

	if gap < 0 {
		return ansi.Truncate(left, width, "")
	}

	return left + ground.Render(strings.Repeat(" ", gap)) + right
}

// trayModuleAt is the first cell and the width of module id on a bar width
// cells wide, as renderBar draws it. It is false when the bar does not draw the
// tray or the module. With icons one edge cell precedes every module, without
// them one divider cell precedes every module but the first.
func trayModuleAt(p barPlan, width int, icons bool, id string) (x, w int, ok bool) {
	if p.trayW == 0 || p.tabsW+trayGap+p.trayW > width {
		return 0, 0, false
	}

	x = width - p.trayW

	for i, m := range p.tray {
		if icons || i > 0 {
			x++
		}

		if m.id == id {
			return x, m.width, true
		}

		x += m.width
	}

	return 0, 0, false
}

func renderTabs(p barPlan, tabs []tabInfo, active int, icons bool, ui theme.UI) string {
	ground := lipgloss.NewStyle().Background(ui.Bar)
	active = min(max(active, 0), len(tabs)-1)

	var sb strings.Builder

	for i, v := range p.tabs {
		info := tabs[v.idx]
		isActive := v.idx == active
		bg := segmentBg(info, v.level, isActive, ui)

		sb.WriteString(renderSegment(info, v.level, isActive, icons, ui))

		if i == len(p.tabs)-1 {
			if icons {
				sb.WriteString(paint(bg, ui.Bar, false).Render(edgeSolid))
			}

			continue
		}

		if !icons {
			sb.WriteString(ground.Render(" "))

			continue
		}

		next := p.tabs[i+1]
		nextActive := next.idx == active

		switch {
		case !isActive && !nextActive:
			sb.WriteString(paint(ui.Bar, ui.Surface, false).Render(edgeThin))
		default:
			sb.WriteString(paint(bg, segmentBg(tabs[next.idx], next.level, nextActive, ui), false).Render(edgeSolid))
		}
	}

	return sb.String()
}

// segmentBg is the background of a tab segment; a segment drawn as the rename input sits on Bg.
func segmentBg(t tabInfo, level tabLevel, isActive bool, ui theme.UI) theme.Hex {
	switch {
	case isActive && t.renaming && level == levelFull:
		return ui.Bg
	case isActive:
		return ui.Accent
	}

	return ui.Surface
}

// renameSegment draws the active tab as a text input: ` N icon name `, with the
// trailing cell holding the cursor at the end of the text.
func renameSegment(t tabInfo, icons bool, ui theme.UI) string {
	text := paint(ui.Fg, ui.Bg, false)
	accent := paint(ui.Accent, ui.Bg, true)
	out := text.Render(" ") + accent.Render(strconv.Itoa(t.n)) + text.Render(" ")

	if icons {
		out += accent.Render(iconRename) + text.Render(" ")
	}

	return out + text.Render(t.name+" ")
}

// drawnWidth is the cells s takes in the bar, which draws only its printable text.
func drawnWidth(s string) int { return ansi.StringWidth(printable(s)) }

// renameCursorX is the column of the rename input's cursor in the bar. It is
// false when no tab is being renamed or its segment is shortened.
func (a *App) renameCursorX() (int, bool) {
	tabs := a.tabInfos()
	active := a.store.State().Active

	if active >= len(tabs) || !tabs[active].renaming {
		return 0, false
	}

	p := planBar(tabs, active, a.trayModules(), a.width, a.icons)
	x := 0

	for _, v := range p.tabs {
		t := tabs[v.idx]
		if v.idx != active {
			x += segWidth(t, v.level, a.icons) + 1

			continue
		}

		if v.level != levelFull {
			return 0, false
		}

		// The leading space, the number, and with icons the icon and its space.
		x += 1 + len(strconv.Itoa(t.n)) + 1
		if a.icons {
			x += 2
		}

		in := a.prompt.input
		x += drawnWidth(string([]rune(in.Value())[:in.Position()]))

		return x, x < a.width
	}

	return 0, false
}

// stateColour is the theme colour of an agent state, or none for no state.
func stateColour(st core.AgentState, ui theme.UI, none theme.Hex) theme.Hex {
	switch st {
	case core.AgentWorking:
		return ui.Working
	case core.AgentBlocked:
		return ui.Blocked
	case core.AgentIdle:
		return ui.Idle
	}

	return none
}

func renderSegment(t tabInfo, level tabLevel, isActive, icons bool, ui theme.UI) string {
	if isActive && t.renaming && level == levelFull {
		return renameSegment(t, icons, ui)
	}

	icon := iconShell
	if t.agent {
		icon = iconAgent
	}

	if isActive {
		text := " " + strconv.Itoa(t.n) + " "
		if icons && level != levelNumber {
			text += icon + " "
		}

		if level == levelFull {
			text += t.name + " "
		}

		return paint(ui.OnAccent, ui.Accent, true).Render(text)
	}

	dim := paint(ui.Dim, ui.Surface, false)
	out := dim.Render(" ") + paint(ui.Muted, ui.Surface, true).Render(strconv.Itoa(t.n)) + dim.Render(" ")

	if icons && level != levelNumber {
		if t.state == core.AgentUnknown {
			out += dim.Render(icon + " ")
		} else {
			out += paint(stateColour(t.state, ui, ui.Dim), ui.Surface, false).Render(icon) + dim.Render(" ")
		}
	}

	if level == levelFull {
		out += dim.Render(t.name + " ")
	}

	return out
}

// renderTray draws the tray: one Surface region opened by an edge, the modules
// split by thin dividers, and the clock on its own segment.
func renderTray(mods []trayModule, icons bool, ui theme.UI) string {
	var sb strings.Builder

	for i, m := range mods {
		isClock := m.id == modClock

		switch {
		case !icons && i > 0:
			sb.WriteString(paint(ui.Bar, ui.Surface, false).Render("│"))
		case !icons:
		case isClock && i > 0:
			sb.WriteString(paint(ui.Clock, ui.Surface, false).Render(trayOpen))
		case isClock:
			sb.WriteString(paint(ui.Clock, ui.Bar, false).Render(trayOpen))
		case i == 0:
			sb.WriteString(paint(ui.Surface, ui.Bar, false).Render(trayOpen))
		default:
			sb.WriteString(paint(ui.Bar, ui.Surface, false).Render(edgeThin))
		}

		sb.WriteString(m.render())
	}

	return sb.String()
}
