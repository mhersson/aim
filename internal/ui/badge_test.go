package ui

import (
	"strconv"
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
	"github.com/charmbracelet/x/exp/golden"

	"github.com/mhersson/aim/internal/core"
	"github.com/mhersson/aim/internal/ui/theme"
)

// badgeApp is a w x 12 app with one agent pane, p1, in state st.
func badgeApp(t *testing.T, w int, st core.AgentState, icons bool) *App {
	t.Helper()

	a := newTestApp(t)
	a.icons = icons
	sized(t, a, w, 12)
	openIn(t, a, core.OpenPane{Kind: core.KindAgent, Cwd: "/home/u/proj", Split: core.SplitVertical})
	attachPanes(t, a, map[core.PaneID]string{"p1": "hello"})
	setAgentStates(t, a, map[core.PaneID]core.AgentState{"p1": st})

	return a
}

func setAgentStates(t *testing.T, a *App, states map[core.PaneID]core.AgentState) {
	t.Helper()

	for id, st := range states {
		if err := a.dispatch(core.SetAgentState{ID: id, State: st}); err != nil {
			t.Fatal(err)
		}
	}
}

// topRow is the first pane border row, below the workspace bar, without styling.
func topRow(a *App) string {
	return ansi.Strip(strings.Split(a.View().Content, "\n")[2])
}

func TestStateBadge(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		st    core.AgentState
		spin  int
		icons bool
		want  string
	}{
		{"working wraps", core.AgentWorking, 10, true, " ⠋ working "},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			a := badgeApp(t, 50, tt.st, tt.icons)
			a.spin = tt.spin

			if row, want := topRow(a), tt.want+"─╮"; !strings.HasSuffix(row, want) {
				t.Errorf("top border = %q, want it to end with %q", row, want)
			}
		})
	}
}

func TestStateBadgeDroppedWhenNarrow(t *testing.T) {
	t.Parallel()

	// The badge is 11 cells: it shows from 14, and 13 is one short.
	for _, w := range []int{8, 13, 14, 40} {
		a := badgeApp(t, w, core.AgentWorking, true)
		row := topRow(a)

		if got := ansi.StringWidth(row); got != w {
			t.Errorf("width %d: top border is %d cells wide", w, got)
		}

		if !strings.HasSuffix(row, "╮") {
			t.Errorf("width %d: top border %q lost its corner", w, row)
		}

		fits := strings.Contains(row, "working")
		// The badge is 11 cells and needs the corner and one border cell on each side.
		if want := w-2 >= 11+1; fits != want {
			t.Errorf("width %d: badge shown = %v, want %v (%q)", w, fits, want, row)
		}
	}
}

func TestStateBadgeColours(t *testing.T) {
	t.Parallel()
	ui := mochaUI(t)

	for _, tt := range []struct {
		st   core.AgentState
		icon string
		text string
		fg   theme.Hex
	}{
		{core.AgentBlocked, iconBlocked, "blocked", ui.Blocked},
		{core.AgentIdle, iconIdle, "idle", ui.Idle},
		{core.AgentWorking, "⠋", "working", ui.Working},
	} {
		t.Run(tt.text, func(t *testing.T) {
			a := badgeApp(t, 50, tt.st, true)
			vt := viewVT(t, a)

			x := strings.Index(topRow(a), tt.icon)
			if x < 0 {
				t.Fatalf("no %q in %q", tt.icon, topRow(a))
			}

			// The row has only single-width runes before the badge.
			x = len([]rune(topRow(a)[:x]))
			assertColours(t, vt, x, 2, tt.icon, tt.fg, ui.Bg, true)
			assertColours(t, vt, x+2, 2, tt.text[:1], tt.fg, ui.Bg, true)
		})
	}
}

func TestStateBadgeGolden(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		st    core.AgentState
		spin  int
		icons bool
	}{
		{"working-0", core.AgentWorking, 0, true},
		{"working-3", core.AgentWorking, 3, true},
		{"blocked", core.AgentBlocked, 0, true},
		{"idle", core.AgentIdle, 0, true},
		{"none", core.AgentUnknown, 0, true},
		{"working-plain", core.AgentWorking, 3, false},
		{"blocked-plain", core.AgentBlocked, 0, false},
		{"idle-plain", core.AgentIdle, 0, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			a := badgeApp(t, 40, tt.st, tt.icons)
			a.spin = tt.spin
			golden.RequireEqual(t, []byte(a.View().Content))
		})
	}
}

func TestInactiveTabIconTakesMostUrgentState(t *testing.T) {
	t.Parallel()
	ui := mochaUI(t)

	a := tabsApp(t, 80, 12,
		tabSpec{core.KindShell, "/w/a", "one"},
		tabSpec{core.KindAgent, "/w/b", "two"},
		tabSpec{core.KindAgent, "/w/c", "three"},
	)

	// Tab 2 also holds a working agent beside its blocked one.
	selectTab(t, a, 1)
	openIn(t, a, core.OpenPane{Kind: core.KindAgent, Cwd: "/w/d", Split: core.SplitVertical})

	panes := a.store.State().AgentPanes()
	if len(panes) != 3 {
		t.Fatalf("agent panes = %v, want 3", panes)
	}

	setAgentStates(t, a, map[core.PaneID]core.AgentState{
		panes[0]: core.AgentBlocked, panes[1]: core.AgentWorking, panes[2]: core.AgentIdle,
	})

	// Tab 1 is inactive and has no agent; tab 2 is active; tab 3 is idle.
	// " 1 <shell> one " is 0..8 and the edge sits at 9; the active segment follows.
	vt := lineVT(t, a.workspaceBar(80), 80)
	assertColours(t, vt, 3, 0, iconShell, ui.Dim, ui.Surface, false)
	assertColours(t, vt, 13, 0, iconAgent, ui.OnAccent, ui.Accent, true)
	assertColours(t, vt, 23, 0, iconAgent, ui.Idle, ui.Surface, false)

	selectTab(t, a, 0)
	vt = lineVT(t, a.workspaceBar(80), 80)

	// " 1 <shell> one " active (0..8), edge, then " 2 <agent> two " with the blocked agent.
	assertColours(t, vt, 13, 0, iconAgent, ui.Blocked, ui.Surface, false)
	// Tab 3 holds an idle agent: " 3 <agent> three " follows tab 2's thin edge.
	assertColours(t, vt, 23, 0, iconAgent, ui.Idle, ui.Surface, false)

	if err := a.dispatch(core.SetAgentState{ID: panes[0], State: core.AgentUnknown}); err != nil {
		t.Fatal(err)
	}

	vt = lineVT(t, a.workspaceBar(80), 80)
	assertColours(t, vt, 13, 0, iconAgent, ui.Working, ui.Surface, false)
}

func TestTrayBlockedAgentGolden(t *testing.T) {
	t.Parallel()

	t.Run("icons=true", func(t *testing.T) {
		a := trayApp(t, true)
		setAgentStates(t, a, map[core.PaneID]core.AgentState{a.store.State().AgentPanes()[0]: core.AgentBlocked})
		golden.RequireEqual(t, []byte(a.workspaceBar(100)))
	})
}

// modelApp is a w x 12 app with one working agent, p1, whose status line
// reported model and, unless meter is nil, a context percentage.
func modelApp(t *testing.T, w int, model string, meter *int, icons bool) *App {
	t.Helper()

	a := badgeApp(t, w, core.AgentWorking, icons)
	status := core.SetAgentStatus{ID: "p1", Session: "s1", Model: model}

	if meter != nil {
		status.Context, status.ContextKnown = float64(*meter), true
	}

	openIn(t, a, status)

	return a
}

// zoomedModelApp is modelApp with a shell beside the agent and the agent zoomed
// and, when scrolled, scrolled back three lines, so its top border holds every
// tag but the preselection and COPY. Without status the agent's status line has
// reported nothing, so the border has no model tag.
func zoomedModelApp(t *testing.T, w int, scrolled, status bool) *App {
	t.Helper()

	a := newTestApp(t)
	sized(t, a, w, 12)
	openIn(t, a,
		core.OpenPane{Kind: core.KindAgent, Cwd: "/home/u/proj", Split: core.SplitVertical},
		core.OpenPane{Kind: core.KindShell, Cwd: "/home/u/other", Split: core.SplitVertical},
		core.FocusPane{ID: "p1"},
	)

	procs := attachPanes(t, a, nil)

	if scrolled {
		p := a.panes["p1"]
		feedAndWait(t, p, procs["p1"], strings.Repeat("line\r\n", 40))
		p.render()

		if !p.scrollBy(3) {
			t.Fatal("setup: the pane did not scroll")
		}
	}

	openIn(t, a, core.ToggleZoom{}, core.SetAgentState{ID: "p1", State: core.AgentWorking})

	if status {
		openIn(t, a, core.SetAgentStatus{ID: "p1", Session: "s1", Model: "Opus 5.5", Context: 62, ContextKnown: true})
	}

	return a
}

func TestFrameModelTag(t *testing.T) {
	t.Parallel()
	t.Run("right of the zoom and scroll tags", func(t *testing.T) {
		row := topRow(zoomedModelApp(t, 70, true, true))
		scroll, zoom, model, badge := strings.Index(row, iconScroll), strings.Index(row, "zoom"), strings.Index(row, "Opus"), strings.Index(row, "working")

		if scroll < 0 || zoom < scroll || model < zoom || badge < model || !strings.HasSuffix(row, "Opus 5.5 62% ─ ⠋ working ─╮") {
			t.Errorf("top border = %q, want scroll, zoom, model, then the badge rightmost", row)
		}
	})

	t.Run("without a badge", func(t *testing.T) {
		a := modelApp(t, 50, "Opus 5.5", new(62), true)
		openIn(t, a, core.SetAgentState{ID: "p1", State: core.AgentUnknown})

		if row, want := topRow(a), " Opus 5.5 62% ─╮"; !strings.HasSuffix(row, want) {
			t.Errorf("top border = %q, want it to end with %q", row, want)
		}
	})

	// The title starts after the corner and its dash, the tags end before a dash
	// and the corner, and one border cell separates two tags.
	badge, zoom, scroll, model := " ⠋ working ", " "+iconZoom+" zoom ", " "+iconScroll+" 3 ", " Opus 5.5 62% "
	title := " " + iconAgent + " claude "
	need := func(tags ...string) int {
		n := 4 + ansi.StringWidth(title) + len(tags) - 1

		for _, tg := range tags {
			n += ansi.StringWidth(tg)
		}

		return n
	}

	t.Run("dropped before every other tag, and for the whole title", func(t *testing.T) {
		// 20 is the smallest with the title, then one short of the room for the
		// model and that room, and 70 the widest the border tells apart.
		for _, w := range []int{20, need(badge, zoom, scroll, model) - 1, need(badge, zoom, scroll, model), 70} {
			row := topRow(zoomedModelApp(t, w, true, true))

			if got := ansi.StringWidth(row); got != w {
				t.Errorf("width %d: top border is %d cells wide", w, got)
			}

			shown := map[string]bool{
				"badge": strings.Contains(row, "working"), "zoom": strings.Contains(row, "zoom"),
				"scroll": strings.Contains(row, iconScroll), "model": strings.Contains(row, "Opus"),
				"title": strings.Contains(row, "claude "),
			}

			if want := w >= need(badge, zoom, scroll, model); shown["model"] != want {
				t.Errorf("width %d: model shown = %v, want %v (%q)", w, shown["model"], want, row)
			}

			if shown["model"] && (!shown["badge"] || !shown["zoom"] || !shown["scroll"] || !shown["title"]) {
				t.Errorf("width %d: model shown but another tag or the whole title is not (%q)", w, row)
			}
		}

		// One cell short of the room for the model and the whole title, the other tags and the title are all still there.
		w := need(badge, zoom, scroll, model) - 1
		if row := topRow(zoomedModelApp(t, w, true, true)); strings.Contains(row, "Opus") || !strings.Contains(row, "working") || !strings.Contains(row, "zoom") || !strings.Contains(row, iconScroll) || !strings.Contains(row, "claude ") {
			t.Errorf("width %d: top border = %q, want the model dropped and the other tags and the title kept", w, row)
		}

		if row := topRow(zoomedModelApp(t, w+1, true, true)); !strings.Contains(row, "Opus 5.5 62%") || !strings.Contains(row, "claude ") {
			t.Errorf("width %d: top border = %q, want the model and the whole title", w+1, row)
		}
	})

	t.Run("never shrinks the title", func(t *testing.T) {
		// Per layout: 12 (the title cut), then one short of the room for the badge
		// and zoom tag (and the scroll tag) and that room, the same for the model, and 70.
		tests := []struct {
			scrolled bool
			widths   []int
		}{
			{false, []int{12, need(badge, zoom) - 1, need(badge, zoom), need(badge, zoom, model) - 1, need(badge, zoom, model), 70}},
			{true, []int{12, need(badge, zoom, scroll) - 1, need(badge, zoom, scroll), need(badge, zoom, scroll, model) - 1, need(badge, zoom, scroll, model), 70}},
		}

		for _, tt := range tests {
			for _, w := range tt.widths {
				with := topRow(zoomedModelApp(t, w, tt.scrolled, true))
				without := topRow(zoomedModelApp(t, w, tt.scrolled, false))

				// The model tag may add cells to the border but never changes the title.
				for _, mark := range []string{"claude ", "…"} {
					if strings.Contains(with, mark) != strings.Contains(without, mark) {
						t.Errorf("scrolled=%v width %d: %q differs with a model tag\nwith:    %q\nwithout: %q", tt.scrolled, w, mark, with, without)
					}
				}

				if strings.Contains(with, "Opus") && (strings.Contains(with, "…") || !strings.Contains(with, "claude ")) {
					t.Errorf("scrolled=%v width %d: model tag beside a cut title: %q", tt.scrolled, w, with)
				}
			}
		}
	})

	t.Run("no model shows nothing", func(t *testing.T) {
		for name, a := range map[string]*App{
			"a meter without a model": modelApp(t, 50, "", new(62), true),
			"no status":               badgeApp(t, 50, core.AgentWorking, true),
		} {
			if row := topRow(a); strings.Contains(row, "%") || strings.Contains(row, "Opus") || !strings.HasSuffix(row, " ⠋ working ─╮") {
				t.Errorf("%s: top border = %q, want only the badge", name, row)
			}
		}
	})
}

func TestFrameModelTagColours(t *testing.T) {
	t.Parallel()
	ui := mochaUI(t)

	tests := []struct {
		pct  int
		fg   theme.Hex
		bold bool
	}{
		{0, ui.Meter, false},
		{70, ui.Warning, false},
		{90, ui.Error, true},
	}

	for _, tt := range tests {
		t.Run(strconv.Itoa(tt.pct), func(t *testing.T) {
			a := modelApp(t, 50, "Opus 5.5", new(tt.pct), true)
			vt := viewVT(t, a)
			row := topRow(a)

			meter := strconv.Itoa(tt.pct) + "%"
			model, at := strings.Index(row, "Opus"), strings.Index(row, meter)

			if model < 0 || at < 0 {
				t.Fatalf("top border = %q, want the model and %q", row, meter)
			}

			// Everything left of the tag is a single-width rune.
			x, mx := len([]rune(row[:model])), len([]rune(row[:at]))
			assertColours(t, vt, x, 2, "O", ui.Muted, ui.Bg, false)

			for i, r := range []rune(meter) {
				assertColours(t, vt, mx+i, 2, string(r), tt.fg, ui.Bg, tt.bold)
			}
		})
	}
}
