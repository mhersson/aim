package ui

import (
	"bytes"
	"fmt"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/charmbracelet/x/exp/teatest/v2"

	"github.com/mhersson/aim/internal/core"
	"github.com/mhersson/aim/internal/ui/palette"
	"github.com/mhersson/aim/internal/ui/theme"
)

var keyUp = tea.KeyPressMsg{Code: tea.KeyUp}

// flowApp is a sized app with fake pane processes and a recording tick.
type flowApp struct {
	*App
	st    *starts
	ticks *tickRec
	clk   *fakeClock
}

func newFlowApp(t *testing.T) *flowApp {
	t.Helper()

	a, st, clk := kindApp(t, gitOf(newFakeGit(t)))
	f := &flowApp{App: a, st: st, clk: clk, ticks: &tickRec{}}
	a.tick = f.ticks.tick

	return f
}

// twoTabs makes tab 1 = p1 shell + p2 agent and tab 2 = p3 shell + p4 agent,
// with tab 2 active, p2 working and p4 blocked.
func (f *flowApp) twoTabs(t *testing.T) {
	t.Helper()

	chord(f.App, 'n')
	chord(f.App, 'c')
	chord(f.App, 'n')

	if got := f.store.State().Tab().Focus; got != "p4" || len(f.store.State().AgentPanes()) != 2 {
		t.Fatalf("setup: focus %s, agents %v", got, f.store.State().AgentPanes())
	}

	openIn(t, f.App, core.SetAgentState{ID: "p2", State: core.AgentWorking}, core.SetAgentState{ID: "p4", State: core.AgentBlocked})
}

// feed runs cmd, delivers every message and returns the commands those raised.
func (f *flowApp) feed(cmd tea.Cmd) tea.Cmd {
	var out []tea.Cmd

	for _, m := range flatten(cmd) {
		if m != nil {
			_, c := f.Update(m)
			out = append(out, c)
		}
	}

	return tea.Batch(out...)
}

// open opens the rail with the leader key; it takes the keyboard.
func (f *flowApp) open(t *testing.T) tea.Cmd {
	t.Helper()

	cmd := chord(f.App, 'a')
	if !f.store.State().Rail || !f.rail.focused {
		t.Fatalf("leader a: rail open = %v, focused = %v", f.store.State().Rail, f.rail.focused)
	}

	return cmd
}

func (f *flowApp) sent() string {
	var b strings.Builder
	for i := range f.st.count() {
		b.WriteString(f.st.proc(i).sent())
	}

	return b.String()
}

// waitSent waits for the panes to have been sent want and returns everything sent.
func (f *flowApp) waitSent(t *testing.T, want string) string {
	t.Helper()

	deadline := time.Now().Add(2 * time.Second)
	for !strings.Contains(f.sent(), want) && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}

	return f.sent()
}

// requireQuiet fails when a pane was sent anything but a marker key written
// straight to the focused pane; the marker's arrival proves earlier writes landed.
func (f *flowApp) requireQuiet(t *testing.T, what string) {
	t.Helper()

	f.keyToFocused(keyRune('~'))

	if got := f.waitSent(t, "~"); got != "~" {
		t.Errorf("%s: panes were sent %q, want only the marker", what, got)
	}
}

func (f *flowApp) delays(d time.Duration) int {
	n := 0

	for _, x := range f.ticks.delays {
		if x == d {
			n++
		}
	}

	return n
}

// cardXY is a cell on the second line of the agent's card.
func (f *flowApp) cardXY(t *testing.T, id core.PaneID) (int, int) {
	t.Helper()

	v, ok := f.railViewOf(f.store.State())
	if !ok {
		t.Fatal("the rail is hidden")
	}

	idx := slices.IndexFunc(v.shown, func(c railCard) bool { return c.pane == id })
	for i, r := range v.rows {
		if r.kind == railRowCard && r.card == idx && r.line == 1 {
			return v.geom.list.X + 1, v.geom.list.Y + i - v.top
		}
	}

	t.Fatalf("no card for %s", id)

	return 0, 0
}

func TestRailToggleCases(t *testing.T) {
	t.Parallel()

	t.Run("closed opens and takes the keyboard", func(t *testing.T) {
		f := newFlowApp(t)
		f.open(t)

		if !f.railIsOpen() {
			t.Error("the rail context did not start")
		}
	})

	t.Run("open with the keyboard closes and returns it to the pane", func(t *testing.T) {
		f := newFlowApp(t)
		f.open(t)
		chord(f.App, 'a')

		if f.store.State().Rail || f.rail.focused || f.railIsOpen() {
			t.Errorf("rail = %v, focused = %v, context = %v, want all closed", f.store.State().Rail, f.rail.focused, f.railIsOpen())
		}

		press(f.App, keyRune('x'))

		if !strings.Contains(f.waitSent(t, "x"), "x") {
			t.Error("the focused pane did not get the key after the rail closed")
		}
	})

	t.Run("open without the keyboard takes it", func(t *testing.T) {
		f := newFlowApp(t)
		f.open(t)
		press(f.App, keyEsc)

		if f.rail.focused || !f.store.State().Rail {
			t.Fatalf("setup: focused = %v, open = %v", f.rail.focused, f.store.State().Rail)
		}

		chord(f.App, 'a')

		if !f.rail.focused || !f.store.State().Rail {
			t.Errorf("focused = %v, open = %v, want both true", f.rail.focused, f.store.State().Rail)
		}
	})

	t.Run("hidden by the width rule closes", func(t *testing.T) {
		f := newFlowApp(t)
		f.open(t)
		sized(t, f.App, 60, 30)

		if f.rail.focused || !f.store.State().Rail {
			t.Fatalf("a hidden rail: focused = %v, open = %v, want it open without the keyboard", f.rail.focused, f.store.State().Rail)
		}

		chord(f.App, 'a')

		if f.store.State().Rail {
			t.Error("leader a left a hidden rail open")
		}
	})

	t.Run("opening where it is hidden does not take the keyboard", func(t *testing.T) {
		f := newFlowApp(t)
		sized(t, f.App, 60, 30)
		chord(f.App, 'a')

		if !f.store.State().Rail || f.rail.focused {
			t.Errorf("open = %v, focused = %v, want open without the keyboard", f.store.State().Rail, f.rail.focused)
		}

		sized(t, f.App, 100, 30)

		if f.rail.focused {
			t.Error("the rail took the keyboard when it showed again")
		}
	})

	t.Run("closing on the width rule gives the keyboard back", func(t *testing.T) {
		f := newFlowApp(t)
		f.open(t)
		sized(t, f.App, 60, 30)
		sized(t, f.App, 100, 30)

		press(f.App, keyRune('x'))

		if !strings.Contains(f.waitSent(t, "x"), "x") {
			t.Error("the pane did not get the key after the rail was hidden and shown")
		}
	})
}

func TestRailRoutesKeys(t *testing.T) {
	t.Parallel()

	f := newFlowApp(t)
	f.twoTabs(t)
	f.open(t)

	f.notice = "hello"

	press(f.App, keyRune('x'), keyRune('q'), keyRune('1'), keyRune('?'), keyLeader, keyLeader)
	f.requireQuiet(t, "keys in the rail")

	if f.notice != "" && !strings.Contains(f.notice, "ctrl+space") {
		t.Errorf("notice = %q, want it gone with the first key", f.notice)
	}

	chord(f.App, 'z')

	if !f.store.State().Tab().Zoomed {
		t.Error("the leader did not work while the rail had the keyboard")
	}

	if !f.rail.focused {
		t.Error("a leader command took the keyboard from the rail")
	}

	press(f.App, keyEsc)
	press(f.App, keyRune('x'))

	if !strings.Contains(f.waitSent(t, "x"), "x") {
		t.Error("esc did not return the keyboard to the pane")
	}
}

func TestRailToastGoesWithTheFirstKey(t *testing.T) {
	t.Parallel()

	f := newFlowApp(t)
	f.open(t)

	f.notice = "hello"
	press(f.App, keyRune('j'))

	if f.notice != "" {
		t.Errorf("notice = %q, want it cleared by a key the rail took", f.notice)
	}
}

func TestRailMoveStopsAtTheEnds(t *testing.T) {
	t.Parallel()

	f := newFlowApp(t)
	f.twoTabs(t)
	f.open(t)

	steps := []struct {
		key  tea.KeyPressMsg
		want core.PaneID
	}{
		{keyRune('k'), "p4"}, {keyUp, "p4"}, {keyRune('j'), "p2"}, {keyDown, "p2"}, {keyRune('k'), "p4"},
	}
	for _, s := range steps {
		press(f.App, s.key)

		if f.rail.sel != s.want {
			t.Fatalf("after %s: selected %s, want %s", s.key, f.rail.sel, s.want)
		}
	}
}

func TestRailSearchKeys(t *testing.T) {
	t.Parallel()

	f := newFlowApp(t)
	f.twoTabs(t)
	f.open(t)

	press(f.App, keyRune('/'))

	if !f.rail.searching || f.rail.query != "" {
		t.Fatalf("after /: searching = %v, query = %q", f.rail.searching, f.rail.query)
	}

	for _, r := range "tab 1" {
		press(f.App, keyRune(r))
	}

	if f.rail.query != "tab 1" {
		t.Fatalf("query = %q, want j/k and space typed as text", f.rail.query)
	}

	if got := f.railShown(f.store.State()); len(got) != 1 || got[0].pane != "p2" || f.rail.sel != "p2" {
		t.Errorf("shown = %v, selected %s, want only p2 selected", got, f.rail.sel)
	}

	if f.cursorView() == nil {
		t.Error("no search cursor while typing")
	}

	press(f.App, keyBackspace, keyRune('j'), keyUp, keyDown)

	if f.rail.query != "tab j" {
		t.Errorf("query = %q, want backspace to delete one rune and arrows to be dropped", f.rail.query)
	}

	press(f.App, keyEnter)

	if f.rail.searching || f.rail.query != "tab j" {
		t.Errorf("enter: searching = %v, query = %q, want the filter kept", f.rail.searching, f.rail.query)
	}

	if f.cursorView() != nil {
		t.Error("a cursor while the rail has the keyboard and is not typing")
	}

	press(f.App, keyEsc)

	if f.rail.query != "" || f.rail.searching || !f.rail.focused {
		t.Errorf("esc with a filter: query = %q, searching = %v, focused = %v, want it cleared and the rail still focused", f.rail.query, f.rail.searching, f.rail.focused)
	}

	press(f.App, keyEsc)

	if f.rail.focused {
		t.Error("esc without a filter kept the keyboard")
	}

	press(f.App, keyRune('x'))

	if !strings.Contains(f.waitSent(t, "x"), "x") {
		t.Error("the pane did not get the key after esc twice")
	}
}

func TestRailSearchEscWhileTyping(t *testing.T) {
	t.Parallel()

	f := newFlowApp(t)
	f.twoTabs(t)
	f.open(t)
	press(f.App, keyRune('/'), keyRune('a'), keyEsc)

	if f.rail.searching || f.rail.query != "" || !f.rail.focused {
		t.Errorf("esc while typing: searching = %v, query = %q, focused = %v", f.rail.searching, f.rail.query, f.rail.focused)
	}

	press(f.App, keyRune('/'), keyEnter)

	if f.rail.searching {
		t.Error("enter on an empty text left the search row open")
	}

	press(f.App, keyRune('/'), keyRune('p'), keyEnter, keyRune('/'), keyRune('4'))

	if f.rail.query != "p4" {
		t.Errorf("query = %q, want typing to append to a kept filter", f.rail.query)
	}
}

func TestRailPaste(t *testing.T) {
	t.Parallel()

	f := newFlowApp(t)
	f.twoTabs(t)
	f.open(t)

	f.Update(tea.PasteMsg{Content: "tab\n2"})

	if f.rail.query != "" {
		t.Errorf("a paste outside the search: query = %q, want it dropped", f.rail.query)
	}

	press(f.App, keyRune('/'))
	f.Update(tea.PasteMsg{Content: "tab\n2"})

	if f.rail.query != "tab2" {
		t.Errorf("a paste into the search: query = %q, want text without the newline", f.rail.query)
	}

	f.requireQuiet(t, "pastes in the rail")
}

func TestRailJump(t *testing.T) {
	t.Parallel()

	t.Run("into a hidden tab", func(t *testing.T) {
		f := newFlowApp(t)
		f.twoTabs(t)
		f.open(t)
		press(f.App, keyRune('/'), keyRune('t'), keyEnter, keyRune('j'))

		if f.rail.sel != "p2" {
			t.Fatalf("setup: selected %s", f.rail.sel)
		}

		cmd := f.Update2(keyEnter)

		st := f.store.State()
		if st.Active != 0 || st.Tab().Focus != "p2" {
			t.Fatalf("active tab %d, focus %s, want tab 1 and p2", st.Active, st.Tab().Focus)
		}

		if f.rail.focused || !st.Rail {
			t.Errorf("focused = %v, open = %v, want the keyboard on the pane and the rail open", f.rail.focused, st.Rail)
		}

		if f.rail.query != "t" {
			t.Errorf("query = %q, want the filter kept", f.rail.query)
		}

		if f.rail.flashPane != "p2" || !f.clock.Now().Before(f.rail.flashUntil) || f.rail.flashUntil.Sub(f.clock.Now()) != 600*time.Millisecond {
			t.Errorf("flash = %s until %v, now %v, want p2 for 600ms", f.rail.flashPane, f.rail.flashUntil, f.clock.Now())
		}

		if f.delays(600*time.Millisecond) != 1 {
			t.Errorf("flash ticks = %d, want 1", f.delays(600*time.Millisecond))
		}

		f.feed(cmd)

		if f.rail.flashPane != "" {
			t.Errorf("flash pane = %s after its tick, want none", f.rail.flashPane)
		}

		press(f.App, keyRune('x'))

		if !strings.Contains(f.waitSent(t, "x"), "x") || !strings.Contains(f.st.proc(1).sent(), "x") {
			t.Error("the jumped-to pane did not get the key")
		}
	})

	t.Run("a later jump supersedes the earlier clear", func(t *testing.T) {
		f := newFlowApp(t)
		f.twoTabs(t)
		f.open(t)

		first := f.Update2(keyEnter)

		f.rail.focused = true
		press(f.App, keyRune('j'))
		f.Update2(keyEnter)

		f.feed(first)

		if f.rail.flashPane == "" {
			t.Error("the first jump's tick cleared the second jump's flash")
		}
	})

	t.Run("out of a zoomed tab", func(t *testing.T) {
		f := newFlowApp(t)
		f.twoTabs(t)
		chord(f.App, 'n')
		openIn(t, f.App, core.SetAgentState{ID: "p5", State: core.AgentIdle})
		chord(f.App, 'z')

		if !f.store.State().Tab().Zoomed || f.store.State().Tab().Focus != "p5" {
			t.Fatalf("setup: zoomed = %v focus %s", f.store.State().Tab().Zoomed, f.store.State().Tab().Focus)
		}

		f.open(t)

		if f.rail.sel != "p4" {
			t.Fatalf("setup: selected %s", f.rail.sel)
		}

		press(f.App, keyEnter)

		if tab := f.store.State().Tab(); tab.Zoomed || tab.Focus != "p4" {
			t.Errorf("zoomed = %v, focus %s, want p4 unzoomed", tab.Zoomed, tab.Focus)
		}
	})

	t.Run("into a hidden scrolling column", func(t *testing.T) {
		f := newFlowApp(t)

		c, _ := f.reg.get("layout.set.scrolling")
		c.run(f.App)

		for range 4 {
			chord(f.App, 'n')
		}

		f.open(t)

		g := f.geometry()
		if _, ok := g.rects["p2"]; ok || g.hiddenLeft == 0 {
			t.Fatalf("setup: p2 visible = %v, hidden left %d, want p2 off screen", ok, g.hiddenLeft)
		}

		press(f.App, keyRune('G'))

		for range 4 {
			press(f.App, keyUp)
		}

		if f.rail.sel != "p2" {
			t.Fatalf("setup: selected %s", f.rail.sel)
		}

		press(f.App, keyEnter)

		if _, ok := f.geometry().rects["p2"]; !ok || f.store.State().Tab().Focus != "p2" {
			t.Errorf("p2 visible = %v, focus %s, want it revealed and focused", ok, f.store.State().Tab().Focus)
		}
	})

	t.Run("a pane that is gone does nothing", func(t *testing.T) {
		f := newFlowApp(t)
		f.twoTabs(t)
		f.open(t)

		f.rail.sel = "p9"

		if cmd := f.Update2(keyEnter); cmd != nil || f.rail.flashPane != "" || !f.rail.focused {
			t.Errorf("jump to a missing pane: cmd %v, flash %q, focused %v", cmd != nil, f.rail.flashPane, f.rail.focused)
		}
	})
}

// Update2 delivers one message and returns the commands the update raised.
func (f *flowApp) Update2(msg tea.Msg) tea.Cmd {
	_, cmd := f.Update(msg)

	return cmd
}

func TestRailFlashDrawsTheFrame(t *testing.T) {
	t.Parallel()

	f := newFlowApp(t)
	f.twoTabs(t)
	f.open(t)
	press(f.App, keyRune('j'), keyEnter)

	th, _ := theme.ByName(f.store.State().Theme)
	if f.rail.flashPane != "p2" || !strings.Contains(f.View().Content, "38;2;"+rgb(th.UI.Match)) {
		t.Errorf("flash pane %q, view lacks the Match colour %s", f.rail.flashPane, rgb(th.UI.Match))
	}
}

func rgb(h theme.Hex) string {
	r, g, b, _ := h.RGBA()

	return fmt.Sprintf("%d;%d;%d", r>>8, g>>8, b>>8)
}

func TestRailMouse(t *testing.T) {
	t.Parallel()

	t.Run("click selects, focuses the rail and never reaches a pane", func(t *testing.T) {
		f := newFlowApp(t)
		f.twoTabs(t)
		f.open(t)
		press(f.App, keyEsc)

		x, y := f.cardXY(t, "p2")
		click(f.App, x, y)

		if f.rail.sel != "p2" || !f.rail.focused {
			t.Errorf("selected %s, focused %v, want p2 and the rail focused", f.rail.sel, f.rail.focused)
		}

		f.requireQuiet(t, "a click in the rail")
	})

	t.Run("a click on the rail's blank space gives it the keyboard", func(t *testing.T) {
		f := newFlowApp(t)
		f.twoTabs(t)
		f.open(t)
		press(f.App, keyEsc)

		click(f.App, 3, 28)

		if !f.rail.focused {
			t.Error("the rail did not take the keyboard")
		}
	})

	t.Run("double click jumps", func(t *testing.T) {
		f := newFlowApp(t)
		f.twoTabs(t)
		f.open(t)

		x, y := f.cardXY(t, "p2")
		click(f.App, x, y)
		click(f.App, x, y)

		if st := f.store.State(); st.Active != 0 || st.Tab().Focus != "p2" || f.rail.focused {
			t.Errorf("active %d, focus %s, rail focused %v, want a jump to p2", st.Active, st.Tab().Focus, f.rail.focused)
		}
	})

	t.Run("a slow second click is a plain click", func(t *testing.T) {
		f := newFlowApp(t)
		f.twoTabs(t)
		f.open(t)

		x, y := f.cardXY(t, "p2")
		click(f.App, x, y)
		f.clk.Advance(time.Second)
		click(f.App, x, y)

		if f.store.State().Active != 1 || !f.rail.focused {
			t.Error("two slow clicks jumped")
		}
	})

	t.Run("wheel moves one card per notch", func(t *testing.T) {
		f := newFlowApp(t)
		f.twoTabs(t)
		f.open(t)

		f.Update(tea.MouseWheelMsg{X: 5, Y: 8, Button: tea.MouseWheelDown})

		if f.rail.sel != "p2" {
			t.Errorf("selected %s after wheel down, want p2", f.rail.sel)
		}

		f.Update(tea.MouseWheelMsg{X: 5, Y: 8, Button: tea.MouseWheelUp})

		if f.rail.sel != "p4" {
			t.Errorf("selected %s after wheel up, want p4", f.rail.sel)
		}

		f.requireQuiet(t, "the wheel over the rail")
	})

	t.Run("a click on a pane gives the keyboard back", func(t *testing.T) {
		f := newFlowApp(t)
		f.twoTabs(t)
		f.open(t)

		click(f.App, 70, 10)

		if f.rail.focused {
			t.Error("the rail kept the keyboard after a click on a pane")
		}

		press(f.App, keyRune('x'))

		if !strings.Contains(f.waitSent(t, "x"), "x") {
			t.Error("the pane did not get the key after the click")
		}
	})

	t.Run("modal UI ignores the rail", func(t *testing.T) {
		f := newFlowApp(t)
		f.twoTabs(t)
		f.open(t)
		press(f.App, keyEsc)
		chord(f.App, ':')

		x, y := f.cardXY(t, "p2")
		click(f.App, x, y)

		if f.rail.focused || f.rail.sel == "p2" {
			t.Error("a click in the rail got through the open palette")
		}
	})
}

func TestRailKeepsKeyboardThroughModals(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		open func(f *flowApp)
		end  tea.KeyPressMsg
	}{
		{"copy mode", func(f *flowApp) { chord(f.App, '[') }, keyEsc},
		{"prompt", func(f *flowApp) { chord(f.App, ',') }, keyEsc},
		{"overlay", func(f *flowApp) { chord(f.App, ':') }, keyEsc},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := newFlowApp(t)
			f.twoTabs(t)
			f.open(t)

			sel := f.rail.sel
			tt.open(f)

			if f.copy == nil && f.prompt == nil && f.overlay == nil {
				t.Fatal("setup: nothing opened")
			}

			press(f.App, keyRune('j'))

			if f.rail.sel != sel {
				t.Error("the rail took a key that belonged to the modal")
			}

			pressRun(f.App, tt.end)

			if f.copy != nil || f.prompt != nil || f.overlay != nil {
				t.Fatal("the modal did not end")
			}

			if !f.rail.focused {
				t.Error("the rail lost the keyboard when the modal ended")
			}

			press(f.App, keyRune('j'))

			if f.rail.sel == sel {
				t.Error("the rail did not take keys after the modal ended")
			}
		})
	}
}

func TestRailPulseArming(t *testing.T) {
	t.Parallel()

	f := newFlowApp(t)
	f.twoTabs(t)
	f.ticks.delays = nil

	cmd := f.open(t)

	if n := f.delays(500 * time.Millisecond); n != 1 {
		t.Fatalf("pulse ticks after opening over a blocked agent = %d, want 1", n)
	}

	press(f.App, keyRune('j'), keyRune('k'))

	if n := f.delays(500 * time.Millisecond); n != 1 {
		t.Errorf("pulse ticks = %d with one outstanding, want 1", n)
	}

	cmd = f.feed(cmd)

	if !f.rail.pulseDim {
		t.Error("the pulse did not dim the bar")
	}

	if n := f.delays(500 * time.Millisecond); n != 2 {
		t.Fatalf("pulse ticks = %d after one fired, want it re-armed (2)", n)
	}

	cmd = f.feed(cmd)

	if f.rail.pulseDim {
		t.Error("the second pulse did not undim the bar")
	}

	sized(t, f.App, 60, 30)
	f.ticks.delays = nil
	f.feed(cmd)

	if n := f.delays(500 * time.Millisecond); n != 0 {
		t.Errorf("pulse ticks = %d while the rail is hidden, want 0", n)
	}
}

func TestRailPulseStopsAndRestarts(t *testing.T) {
	t.Parallel()

	f := newFlowApp(t)
	f.twoTabs(t)
	f.ticks.delays = nil

	cmd := f.open(t)

	openIn(t, f.App, core.SetAgentState{ID: "p4", State: core.AgentIdle})
	f.ticks.delays = nil
	f.feed(cmd)

	if n := f.delays(500 * time.Millisecond); n != 0 {
		t.Errorf("pulse re-armed with no blocked agent: %d", n)
	}

	openIn(t, f.App, core.SetAgentState{ID: "p4", State: core.AgentBlocked})
	pending := f.Update2(struct{}{})

	if n := f.delays(500 * time.Millisecond); n != 1 {
		t.Errorf("pulse ticks after a state change to blocked = %d, want 1", n)
	}

	// Hidden: the outstanding tick fires and does not re-arm; showing again arms.
	sized(t, f.App, 60, 30)
	f.ticks.delays = nil
	f.feed(pending)
	sized(t, f.App, 100, 30)

	if n := f.delays(500 * time.Millisecond); n != 1 {
		t.Errorf("pulse ticks after the rail showed again = %d, want 1", n)
	}
}

func TestRailIdleCost(t *testing.T) {
	t.Parallel()

	t.Run("idle shells", func(t *testing.T) {
		f := newFlowApp(t)
		chord(f.App, 'v')
		press(f.App, keyLeader)
		f.ticks.delays = nil

		cmd := press(f.App, keyRune('a'))
		if !f.store.State().Rail || !f.railIsOpen() {
			t.Fatal("the rail did not open")
		}

		msgs := flatten(cmd)

		for _, m := range msgs {
			if m != nil {
				t.Errorf("opening the rail over idle shells returned %T", m)
			}
		}

		if len(f.ticks.delays) != 0 {
			t.Errorf("ticks scheduled over idle shells: %v", f.ticks.delays)
		}
	})

	t.Run("idle agents after the first probes", func(t *testing.T) {
		r := newRailApp(t, true)
		dir := t.TempDir()

		a1 := r.addAgent(t, false, dir)
		a2 := r.addAgent(t, true, dir)
		r.setState(t, a1, core.AgentIdle)
		r.setState(t, a2, core.AgentIdle)

		cmd := chord(r.App, 'a')
		if !r.store.State().Rail || !r.railIsOpen() {
			t.Fatal("the rail did not open")
		}

		r.deliver(cmd)
		r.ticks.delays = nil

		for _, k := range []tea.KeyPressMsg{keyRune('j'), keyRune('k'), keyRune('/'), keyRune('a'), keyEsc} {
			if c := r.step(k); c != nil {
				if msgs := flatten(c); slices.ContainsFunc(msgs, func(m tea.Msg) bool { return m != nil }) {
					t.Errorf("%s returned messages %v", k, msgs)
				}
			}
		}

		r.step(tea.WindowSizeMsg{Width: 110, Height: 30})

		if len(r.ticks.delays) != 0 {
			t.Errorf("ticks with the rail open over idle agents: %v", r.ticks.delays)
		}
	})
}

func TestRailPaletteAndWhichKey(t *testing.T) {
	t.Parallel()
	a := newTestApp(t)
	sized(t, a, 120, 40)

	c, ok := a.reg.get("rail.toggle")
	if !ok || c.title != "Agents rail" || c.icon != "\U000F167A" {
		t.Fatalf("command = %+v, %v", c, ok)
	}

	if key, _ := a.keymap.keyFor("rail.toggle"); key != "ctrl+space a" {
		t.Errorf("key = %q, want ctrl+space a", key)
	}

	pressRun(a, keyLeader, keyRune(':'))
	typeText(a, "agents rail")

	view := ansi.Strip(a.View().Content)
	if !strings.Contains(view, "Agents rail") || !strings.Contains(view, "ctrl+space a") || !strings.Contains(view, c.icon) {
		t.Errorf("palette lacks the rail entry with its key and icon:\n%s", view)
	}

	found := false

	for _, h := range newTestApp(t).hints() {
		if h.key == "a" && h.title == "Agents rail" && h.group == "Panes" {
			found = true
		}
	}

	if !found {
		t.Error("which-key hints lack a: Agents rail in Panes")
	}
}

func TestRailWhichKeyPopup(t *testing.T) {
	t.Parallel()
	a := newTestApp(t)
	sized(t, a, 140, 40)
	press(a, keyLeader)
	a.Update(whichKeyMsg{seq: a.seq})

	if !regexp.MustCompile(`│ a +Agents rail +│`).MatchString(ansi.Strip(a.View().Content)) {
		t.Errorf("the which-key popup lacks the a: Agents rail row:\n%s", ansi.Strip(a.View().Content))
	}
}

func TestRailManyAgentsStayWhole(t *testing.T) {
	t.Parallel()

	r := newRailApp(t, false)
	openIn(t, r.App, core.ClosePane{ID: "p1"})

	dir := t.TempDir()
	states := []core.AgentState{core.AgentBlocked, core.AgentWorking, core.AgentIdle}

	var ids []core.PaneID

	for i := range core.MaxPanes {
		ids = append(ids, r.addAgent(t, i%7 == 0 && i < 63, dir))
		r.setState(t, ids[i], states[i%3])
	}

	if n := len(r.store.State().AgentPanes()); n != core.MaxPanes || len(r.store.State().Tabs) != 9 {
		t.Fatalf("setup: %d agents in %d tabs", n, len(r.store.State().Tabs))
	}

	sized(t, r.App, 100, 22)
	chord(r.App, 'a')

	whole := func(when string) {
		t.Helper()

		st := r.store.State()
		v, ok := r.railViewOf(st)

		if !ok || v.sel < 0 {
			t.Fatalf("%s: no view or no selection (%v, %d)", when, ok, v.sel)
		}

		if r.rail.top != v.top {
			t.Fatalf("%s: stored top %d, clamped %d", when, r.rail.top, v.top)
		}

		first, last := -1, -1

		for i, row := range v.rows {
			if row.kind == railRowCard && row.card == v.sel {
				if first < 0 {
					first = i
				}

				last = i
			}
		}

		if first < v.top || last >= v.top+v.geom.list.H {
			t.Fatalf("%s: card rows %d-%d outside the shown %d-%d", when, first, last, v.top, v.top+v.geom.list.H-1)
		}
	}

	for i := range core.MaxPanes - 1 {
		press(r.App, keyRune('j'))
		whole("down " + strconv.Itoa(i))
	}

	// The selected agent closes.
	openIn(t, r.App, core.ClosePane{ID: r.rail.sel})
	r.step(struct{}{})
	whole("selected agent closed")

	// A tab closes under the selection.
	tabs := r.store.State().Tabs
	openIn(t, r.App, core.CloseTab{ID: tabs[len(tabs)-1].ID})
	r.step(struct{}{})
	whole("last tab closed")

	for range 30 {
		press(r.App, keyRune('k'))
	}

	whole("up 30")
	openIn(t, r.App, core.CloseTab{ID: r.store.State().Tabs[0].ID})
	r.step(struct{}{})
	whole("first tab closed")

	if r.rail.sel == "" {
		t.Error("no selection after the closes")
	}
}

// TestRailFlow drives the rail through a real Bubble Tea program.
func TestRailFlow(t *testing.T) {
	t.Parallel()
	a := newTestApp(t)
	tm := teatest.NewTestModel(t, a, teatest.WithInitialTermSize(100, 30))

	tm.Send(keyLeader)
	tm.Send(keyRune('a'))
	teatest.WaitFor(t, tm.Output(), func(out []byte) bool { return bytes.Contains(out, []byte("Agents")) },
		teatest.WithDuration(3*time.Second))

	tm.Send(keyRune('/'))
	tm.Send(keyRune('z'))
	tm.Send(keyEsc)
	tm.Send(keyEsc)
	tm.Send(keyLeader)
	tm.Send(keyRune('a'))

	if err := tm.Quit(); err != nil {
		t.Fatalf("Quit() error = %v", err)
	}

	final := tm.FinalModel(t, teatest.WithFinalTimeout(3*time.Second)).(*App)
	if !final.rail.focused || final.rail.query != "" || !final.store.State().Rail {
		t.Errorf("final: focused = %v, query = %q, open = %v, want the rail open, focused again, with no filter", final.rail.focused, final.rail.query, final.store.State().Rail)
	}
}

func TestRailKeyboardAfterLeaderCommands(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		key      rune
		stays    bool
		afterTab int // tab to activate first, -1 for none
	}{
		{"focus move", 'h', false, -1},
		{"next tab", 0, false, -1},
		{"tab jump", '1', false, -1},
		{"new agent pane", 'n', false, -1},
		{"split", 's', false, -1},
		{"new tab", 'c', false, -1},
		{"break pane", '!', false, 0},
		{"zoom", 'z', true, -1},
		{"cycle layout", ' ', true, -1},
		{"resize", 'L', true, -1},
		{"rotate", 'r', true, -1},
		{"palette", ':', true, -1},
		{"rename", ',', true, -1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := newFlowApp(t)
			f.twoTabs(t)

			if tt.afterTab >= 0 {
				openIn(t, f.App, core.SelectTab{Index: tt.afterTab})
			}

			f.open(t)

			switch tt.key {
			case 0:
				press(f.App, keyLeader, keyTab)
			case ' ':
				press(f.App, keyLeader, tea.KeyPressMsg{Code: tea.KeySpace, Text: " "})
			default:
				chord(f.App, tt.key)
			}

			if f.rail.focused != tt.stays {
				t.Errorf("rail focused = %v, want %v", f.rail.focused, tt.stays)
			}
		})
	}
}

func TestRailPulseResetsWhenItStops(t *testing.T) {
	t.Parallel()

	f := newFlowApp(t)
	f.twoTabs(t)

	cmd := f.feed(f.open(t))
	if !f.rail.pulseDim {
		t.Fatal("setup: the bar did not dim")
	}

	openIn(t, f.App, core.SetAgentState{ID: "p4", State: core.AgentIdle})
	f.feed(cmd)

	if f.rail.pulseDim {
		t.Error("pulse stopped while dim, want the next blocked agent to start bright")
	}
}

func TestRailIgnoresInputUnderModals(t *testing.T) {
	t.Parallel()

	t.Run("wheel under an overlay", func(t *testing.T) {
		f := newFlowApp(t)
		f.twoTabs(t)
		f.open(t)
		chord(f.App, ':')

		f.Update(tea.MouseWheelMsg{X: 5, Y: 8, Button: tea.MouseWheelDown})

		if f.rail.sel != "p4" {
			t.Errorf("selected %s, want the wheel ignored", f.rail.sel)
		}
	})

	t.Run("click under a prompt", func(t *testing.T) {
		f := newFlowApp(t)
		f.twoTabs(t)
		f.open(t)
		press(f.App, keyEsc)
		chord(f.App, ',')

		x, y := f.cardXY(t, "p2")
		click(f.App, x, y)

		if f.rail.focused || f.rail.sel != "p4" {
			t.Errorf("focused %v selected %s, want the click ignored", f.rail.focused, f.rail.sel)
		}
	})
}

func TestRailKeyboardAfterPaletteCommands(t *testing.T) {
	t.Parallel()

	for id, stays := range map[string]bool{"tab.next": false, "pane.focus.left": false, "pane.zoom": true} {
		t.Run(id, func(t *testing.T) {
			f := newFlowApp(t)
			f.twoTabs(t)
			f.open(t)
			chord(f.App, ':')
			f.Update(palette.ChosenMsg{ID: id})

			if f.rail.focused != stays {
				t.Errorf("rail focused = %v, want %v", f.rail.focused, stays)
			}
		})
	}
}

func TestRailKeyboardAfterUserFocusChanges(t *testing.T) {
	t.Parallel()

	t.Run("a click on another tab", func(t *testing.T) {
		f := newFlowApp(t)
		f.twoTabs(t)
		f.open(t)

		x := -1

		for cx := range f.width {
			if h := f.hit(cx, 0); h.area == hitTab && h.tab == 0 {
				x = cx

				break
			}
		}

		if x < 0 {
			t.Fatal("no cell of the first tab")
		}

		click(f.App, x, 0)

		if f.store.State().Active != 0 || f.rail.focused {
			t.Errorf("active %d, rail focused %v, want tab 1 and the keyboard on the pane", f.store.State().Active, f.rail.focused)
		}
	})

	t.Run("confirming the close of the focused agent", func(t *testing.T) {
		f := newFlowApp(t)
		f.twoTabs(t)
		f.open(t)

		chord(f.App, 'x')

		if f.confirm == nil || !f.rail.focused {
			t.Fatalf("setup: confirm %v, focused %v", f.confirm, f.rail.focused)
		}

		press(f.App, keyRune('y'))

		if got := f.store.State().Tab().Focus; got != "p3" || f.rail.focused {
			t.Errorf("focus %s, rail focused %v, want p3 and the keyboard on the pane", got, f.rail.focused)
		}
	})

	t.Run("hover focus onto another pane", func(t *testing.T) {
		f := newFlowApp(t)
		f.twoTabs(t)
		f.focusFollows = true
		f.open(t)

		c := contentRectOf(f.App, "p3")
		hover(f.App, c.X+1, c.Y+1)

		if got := f.store.State().Tab().Focus; got != "p3" || f.rail.focused {
			t.Errorf("focus %s, rail focused %v, want p3 and the keyboard on the pane", got, f.rail.focused)
		}
	})

	t.Run("a pane exit keeps the keyboard where it is", func(t *testing.T) {
		f := newFlowApp(t)
		f.twoTabs(t)
		f.open(t)

		f.Update(exitOf(t, f.App, "p4", f.st.proc(3)))

		if got := f.store.State().Tab().Focus; got != "p3" || !f.rail.focused {
			t.Errorf("focus %s, rail focused %v, want p3 and the rail still holding the keyboard", got, f.rail.focused)
		}
	})

	t.Run("the toggle decides for itself", func(t *testing.T) {
		f := newFlowApp(t)
		f.twoTabs(t)
		f.open(t)
		press(f.App, keyEsc)
		chord(f.App, 'a')

		if !f.rail.focused {
			t.Error("leader a did not give the rail the keyboard")
		}
	})
}

func TestRailLosingTheKeyboardEndsTyping(t *testing.T) {
	t.Parallel()

	f := newFlowApp(t)
	f.twoTabs(t)
	f.open(t)
	press(f.App, keyRune('/'), keyRune('t'), keyRune('a'), keyRune('b'))

	if !f.rail.searching {
		t.Fatal("setup: not typing")
	}

	x, y := f.cardXY(t, "p4")
	click(f.App, x, y)
	click(f.App, x, y)

	if f.rail.focused || f.rail.searching || f.rail.query != "tab" {
		t.Fatalf("focused %v, searching %v, query %q, want the keyboard on the pane, typing ended and the filter kept", f.rail.focused, f.rail.searching, f.rail.query)
	}

	chord(f.App, 'a')
	press(f.App, keyRune('j'))

	if f.rail.query != "tab" || f.rail.sel != "p2" {
		t.Errorf("query %q, selected %s, want j to move the selection and leave the query", f.rail.query, f.rail.sel)
	}
}
