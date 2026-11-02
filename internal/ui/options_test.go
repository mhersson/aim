package ui

import (
	"slices"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/mhersson/aim/internal/core"
)

// release delivers a left release and runs the command it returns.
func release(a *App, x, y int) {
	_, cmd := a.Update(tea.MouseReleaseMsg{X: x, Y: y, Button: tea.MouseLeft})
	run(a, cmd)
}

// dragRun is drag with the release's command run, as the program would.
func dragRun(a *App, x0, y0, x1, y1 int, mod tea.KeyMod) {
	mdown(a, x0, y0, tea.MouseLeft, mod)
	mmove(a, x1, y1, tea.MouseLeft)
	release(a, x1, y1)
}

func clickRun(a *App, x, y int) {
	mdown(a, x, y, tea.MouseLeft, 0)
	release(a, x, y)
}

// lastCopied is the text of the latest native clipboard write (primary-only
// writes skipped), "" when none happened.
func lastCopied(f *fakeRun) string {
	if w := clipboardWrites(f); len(w) > 0 {
		return w[len(w)-1]
	}

	return ""
}

func hover(a *App, x, y int) {
	_, cmd := a.Update(tea.MouseMotionMsg{X: x, Y: y, Button: tea.MouseNone})
	run(a, cmd)
}

func focusOf(a *App) core.PaneID { return a.store.State().Tab().Focus }

func TestMouseModeFollowsTheOption(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		focus bool
		want  tea.MouseMode
	}{
		{"off", false, tea.MouseModeCellMotion},
		{"on", true, tea.MouseModeAllMotion},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			a, _, _ := shellsWith(t, "a", "b")
			a.focusFollows = tt.focus

			if got := a.View().MouseMode; got != tt.want {
				t.Errorf("MouseMode = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestHoverFocusesThePaneUnderThePointer(t *testing.T) {
	t.Parallel()
	a, _, _ := shellsWith(t, "a", "b")
	a.focusFollows = true
	c1, c2 := contentRectOf(a, "p1"), contentRectOf(a, "p2")

	if focusOf(a) != "p2" {
		t.Fatalf("setup: focus = %q, want p2", focusOf(a))
	}

	hover(a, c2.X+1, c2.Y+1)

	if focusOf(a) != "p2" {
		t.Errorf("hovering the focused pane changed focus to %q", focusOf(a))
	}

	hover(a, c1.X+1, c1.Y+1)

	if focusOf(a) != "p1" {
		t.Errorf("focus = %q after hovering p1's content, want p1", focusOf(a))
	}

	hover(a, c2.X+1, c2.Y-1)

	if focusOf(a) != "p2" {
		t.Errorf("focus = %q after hovering p2's header, want p2", focusOf(a))
	}
}

func TestHoverIgnoresBordersBarsAndEmptyCells(t *testing.T) {
	t.Parallel()
	a, _, _ := shellsWith(t, "a", "b")
	a.focusFollows = true
	c1 := contentRectOf(a, "p1")

	for _, cell := range [][2]int{{c1.X + c1.W, c1.Y + 1}, {c1.X, 0}, {c1.X, a.height - 1}, {-3, -3}, {a.width + 5, 4}} {
		hover(a, cell[0], cell[1])

		if focusOf(a) != "p2" {
			t.Errorf("hovering %v moved focus to %q", cell, focusOf(a))
		}
	}
}

func TestHoverDoesNothingWhenTheOptionIsOff(t *testing.T) {
	t.Parallel()
	a, _, _ := shellsWith(t, "a", "b")
	c1 := contentRectOf(a, "p1")

	hover(a, c1.X+1, c1.Y+1)

	if focusOf(a) != "p2" {
		t.Errorf("focus = %q, want p2 with focus follows mouse off", focusOf(a))
	}
}

func TestHoverNeverStealsFocusUnderModalUI(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		open func(a *App)
	}{
		{"pending leader", func(a *App) { a.pending = true }},
		{"pending leader with popup", func(a *App) { a.pending, a.popup = true, true }},
		{"confirm", func(a *App) { a.confirm = &confirm{} }},
		{"prompt", func(a *App) { a.prompt = &prompt{} }},
		{"overlay", func(a *App) { a.overlay = &overlay{} }},
		{"copy mode", func(a *App) { a.copy = &copyState{pane: "p2"} }},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			a, _, _ := shellsWith(t, "a", "b")
			a.focusFollows = true
			c1 := contentRectOf(a, "p1")

			tt.open(a)
			hover(a, c1.X+1, c1.Y+1)

			if focusOf(a) != "p2" {
				t.Errorf("focus = %q under %s, want p2", focusOf(a), tt.name)
			}
		})
	}
}

func TestHoverDoesNotFocusDuringADrag(t *testing.T) {
	t.Parallel()
	a, _, _ := shellsWith(t, sampleText, "RIGHT")
	a.focusFollows = true
	c1, c2 := contentRectOf(a, "p1"), contentRectOf(a, "p2")

	mdown(a, c1.X, c1.Y, tea.MouseLeft, 0)
	hover(a, c2.X+1, c2.Y+1)

	if focusOf(a) != "p1" {
		t.Errorf("focus = %q while the button is held in p1, want p1", focusOf(a))
	}

	mmove(a, c2.X+1, c2.Y+1, tea.MouseLeft)

	if focusOf(a) != "p1" {
		t.Errorf("focus = %q after dragging over p2, want p1", focusOf(a))
	}
}

func TestHoverReportsFocusToGuests(t *testing.T) {
	t.Parallel()
	a, procs, _ := shellsWith(t, "a", "b")
	a.focusFollows = true
	feedAndWait(t, a.panes["p1"], procs["p1"], "\x1b[?1004h")
	feedAndWait(t, a.panes["p2"], procs["p2"], "\x1b[?1004h")
	c1 := contentRectOf(a, "p1")

	hover(a, c1.X+1, c1.Y+1)

	eventually(t, "focus in", func() bool { return strings.Contains(procs["p1"].sent(), "\x1b[I") })
	eventually(t, "focus out", func() bool { return strings.Contains(procs["p2"].sent(), "\x1b[O") })
}

func TestCopyOnSelect(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		option  bool
		gesture func(a *App, c core.Rect)
		want    string
	}{
		{"drag copies", true, func(a *App, c core.Rect) { dragRun(a, c.X, c.Y, c.X+4, c.Y, 0) }, "hello"},
		{"block drag copies", true, func(a *App, c core.Rect) { dragRun(a, c.X, c.Y, c.X+1, c.Y+1, tea.ModAlt) }, "he\nse"},
		{"double click copies the word", true, func(a *App, c core.Rect) {
			clickRun(a, c.X+1, c.Y)
			clickRun(a, c.X+1, c.Y)
		}, "hello"},
		{"triple click copies the line", true, func(a *App, c core.Rect) {
			clickRun(a, c.X+1, c.Y)
			clickRun(a, c.X+1, c.Y)
			clickRun(a, c.X+1, c.Y)
		}, "hello world"},
		{"plain click does not copy", true, func(a *App, c core.Rect) { clickRun(a, c.X+1, c.Y) }, ""},
		{"drag with the option off does not copy", false, func(a *App, c core.Rect) { dragRun(a, c.X, c.Y, c.X+4, c.Y, 0) }, ""},
		{"double click with the option off does not copy", false, func(a *App, c core.Rect) {
			clickRun(a, c.X+1, c.Y)
			clickRun(a, c.X+1, c.Y)
		}, ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			a, _, f := shellsWith(t, sampleText, "RIGHT")
			a.copyOnSelect = tt.option
			c := contentRectOf(a, "p1")

			tt.gesture(a, c)

			if got := lastCopied(f); got != tt.want {
				t.Errorf("copied %q, want %q", got, tt.want)
			}
		})
	}
}

func TestRedrawDuringAGestureWaitsForRelease(t *testing.T) {
	t.Parallel()

	// hold leaves the button down; relX is where it is released.
	drag := func(a *App, c core.Rect) int {
		mdown(a, c.X, c.Y, tea.MouseLeft, 0)
		mmove(a, c.X+4, c.Y, tea.MouseLeft)

		return c.X + 4
	}
	double := func(a *App, c core.Rect) int {
		clickRun(a, c.X+1, c.Y)
		mdown(a, c.X+1, c.Y, tea.MouseLeft, 0)

		return c.X + 1
	}

	tests := []struct {
		name       string
		option     bool
		hold       func(a *App, c core.Rect) int
		wantCopied string
	}{
		{"drag, copy on", true, drag, "jello"},
		{"double click, copy on", true, double, "jello"},
		{"drag, copy off", false, drag, ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			a, procs, f := shellsWith(t, sampleText, "RIGHT")
			a.copyOnSelect = tt.option
			c := contentRectOf(a, "p1")

			relX := tt.hold(a, c)

			feedAndWait(t, a.panes["p1"], procs["p1"], "\x1b[1;1Hjello")
			a.Update(paneDirtyMsg{id: "p1", p: a.panes["p1"]})

			if a.sel == nil {
				t.Fatal("selection cleared while the button is down, want kept")
			}

			release(a, relX, c.Y)

			if got := lastCopied(f); got != tt.wantCopied {
				t.Errorf("copied %q, want %q", got, tt.wantCopied)
			}

			if a.sel != nil {
				t.Error("selection kept after the release, want cleared")
			}
		})
	}
}

func TestRedrawDuringAGestureIsCheckedWhenItIsCutShort(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		cut  func(a *App)
	}{
		{"leader opens", func(a *App) {
			press(a, keyLeader)
			mmove(a, 0, 0, tea.MouseLeft)
		}},
		{"middle press elsewhere", func(a *App) {
			c := contentRectOf(a, "p2")
			middleRun(a, c.X, c.Y, 0)
		}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			a, procs, _ := shellsWith(t, sampleText, "RIGHT")
			a.copyOnSelect = false
			c := contentRectOf(a, "p1")

			mdown(a, c.X, c.Y, tea.MouseLeft, 0)
			mmove(a, c.X+4, c.Y, tea.MouseLeft)

			feedAndWait(t, a.panes["p1"], procs["p1"], "\x1b[1;1Hjello")
			a.Update(paneDirtyMsg{id: "p1", p: a.panes["p1"]})

			if a.sel == nil {
				t.Fatal("selection cleared while the button is down, want kept")
			}

			tt.cut(a)

			if a.sel != nil {
				t.Error("selection kept after the gesture was cut short, want cleared")
			}
		})
	}
}

func TestSelectingWritesThePrimary(t *testing.T) {
	t.Parallel()

	drag := func(a *App, c core.Rect) { dragRun(a, c.X, c.Y, c.X+4, c.Y, 0) }
	double := func(a *App, c core.Rect) {
		clickRun(a, c.X+1, c.Y)
		clickRun(a, c.X+1, c.Y)
	}
	triple := func(a *App, c core.Rect) {
		double(a, c)
		clickRun(a, c.X+1, c.Y)
	}
	click := func(a *App, c core.Rect) { clickRun(a, c.X+1, c.Y) }

	tests := []struct {
		name          string
		option        bool
		gesture       func(a *App, c core.Rect)
		wantPrimary   []string
		wantClipboard string
		wantSel       bool
	}{
		{"drag, off", false, drag, []string{"hello"}, "", true},
		{"double click, off", false, double, []string{"hello"}, "", true},
		{"triple click, off", false, triple, []string{"hello", "hello world"}, "", true},
		{"plain click, off", false, click, nil, "", false},
		{"drag, on", true, drag, []string{"hello"}, "hello", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			a, _, f := shellsWith(t, sampleText, "RIGHT")
			a.copyOnSelect = tt.option

			tt.gesture(a, contentRectOf(a, "p1"))

			if got := primaryWrites(f); !slices.Equal(got, tt.wantPrimary) {
				t.Errorf("primary writes %q, want %q", got, tt.wantPrimary)
			}

			if got := lastCopied(f); got != tt.wantClipboard {
				t.Errorf("clipboard %q, want %q", got, tt.wantClipboard)
			}

			if got := a.sel != nil; got != tt.wantSel {
				t.Errorf("selection present = %v, want %v", got, tt.wantSel)
			}
		})
	}
}

func TestSelectionRedrawnDuringTheHoldWritesNothing(t *testing.T) {
	t.Parallel()
	a, procs, f := shellsWith(t, sampleText, "RIGHT")
	a.copyOnSelect = false
	c := contentRectOf(a, "p1")

	mdown(a, c.X, c.Y, tea.MouseLeft, 0)
	mmove(a, c.X+4, c.Y, tea.MouseLeft)

	feedAndWait(t, a.panes["p1"], procs["p1"], "\x1b[1;1Hjello")
	a.Update(paneDirtyMsg{id: "p1", p: a.panes["p1"]})
	release(a, c.X+4, c.Y)

	f.mu.Lock()
	n := len(f.calls)
	f.mu.Unlock()

	if n != 0 {
		t.Errorf("%d native writes, want none", n)
	}

	if a.sel != nil {
		t.Error("selection kept after the release, want cleared")
	}
}

func TestCopyOnSelectClearsOnRelease(t *testing.T) {
	t.Parallel()

	drag := func(a *App, c core.Rect) { dragRun(a, c.X, c.Y, c.X+4, c.Y, 0) }
	double := func(a *App, c core.Rect) {
		clickRun(a, c.X+1, c.Y)
		clickRun(a, c.X+1, c.Y)
	}
	triple := func(a *App, c core.Rect) {
		double(a, c)
		clickRun(a, c.X+1, c.Y)
	}

	tests := []struct {
		name       string
		option     bool
		gesture    func(a *App, c core.Rect)
		wantSel    bool
		wantCopied string
	}{
		{"drag on", true, drag, false, "hello"},
		{"double click on", true, double, false, "hello"},
		{"triple click on", true, triple, false, "hello world"},
		{"drag off", false, drag, true, ""},
		{"double click off", false, double, true, ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			a, _, f := shellsWith(t, sampleText, "RIGHT")
			a.copyOnSelect = tt.option

			tt.gesture(a, contentRectOf(a, "p1"))

			if got := a.sel != nil; got != tt.wantSel {
				t.Errorf("selection present = %v, want %v", got, tt.wantSel)
			}

			if got := lastCopied(f); got != tt.wantCopied {
				t.Errorf("copied %q, want %q", got, tt.wantCopied)
			}
		})
	}
}

func TestCopyOnSelectShowsTheSelectionUntilRelease(t *testing.T) {
	t.Parallel()
	a, _, _ := shellsWith(t, sampleText, "RIGHT")
	a.copyOnSelect = true
	c := contentRectOf(a, "p1")

	mdown(a, c.X, c.Y, tea.MouseLeft, 0)
	mmove(a, c.X+4, c.Y, tea.MouseLeft)

	if !selBgAt(a, c.X, c.Y) {
		t.Error("selection not drawn while dragging")
	}

	release(a, c.X+4, c.Y)

	if selBgAt(a, c.X, c.Y) {
		t.Error("selection still drawn after release")
	}
}

func TestCopyOnSelectCopiesOnReleaseNotWhileDragging(t *testing.T) {
	t.Parallel()
	a, _, f := shellsWith(t, sampleText, "RIGHT")
	a.copyOnSelect = true
	c := contentRectOf(a, "p1")

	mdown(a, c.X, c.Y, tea.MouseLeft, 0)
	mmove(a, c.X+4, c.Y, tea.MouseLeft)

	if got := copiedText(f); got != "" {
		t.Fatalf("copied %q before release", got)
	}

	release(a, c.X+4, c.Y)

	if got := copiedText(f); got != "hello" {
		t.Errorf("copied %q after release, want hello", got)
	}
}

func TestCopyOnSelectSkipsABlankSelection(t *testing.T) {
	t.Parallel()
	a, _, f := shellsWith(t, "", "RIGHT")
	a.copyOnSelect = true
	c := contentRectOf(a, "p1")

	dragRun(a, c.X, c.Y, c.X+4, c.Y, 0)

	if len(f.calls) != 0 {
		t.Errorf("native writes = %v, want none for an empty selection", f.calls)
	}
}

func TestOptionsReachTheApp(t *testing.T) {
	t.Parallel()

	a, _, _ := shellsWith(t, "a", "b")
	if a.focusFollows || a.copyOnSelect || !a.clip.cfg.OSC52 || !a.clip.cfg.Native {
		t.Errorf("zero Options: focusFollows=%v copyOnSelect=%v clip=%+v, want mouse off and clipboard on",
			a.focusFollows, a.copyOnSelect, a.clip.cfg)
	}
}

func TestOptionsSetEachFlagOnItsOwnField(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		opts Options
		want [4]bool // focusFollows, copyOnSelect, OSC52, Native
	}{
		{"focus follows mouse", Options{FocusFollowsMouse: true}, [4]bool{true, false, true, true}},
		{"copy on select", Options{CopyOnSelect: true}, [4]bool{false, true, true, true}},
		{"no OSC 52", Options{NoOSC52: true}, [4]bool{false, false, false, true}},
		{"no native clipboard", Options{NoNativeClipboard: true}, [4]bool{false, false, true, false}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tt.opts.Logger = testLogger()
			tt.opts.Git = newFakeGit(t)
			tt.opts.Store = core.NewStore(core.State{Theme: "catppuccin-mocha"})

			a, err := New(tt.opts)
			if err != nil {
				t.Fatal(err)
			}

			a.tick = (&ticked{}).tick

			got := [4]bool{a.focusFollows, a.copyOnSelect, a.clip.cfg.OSC52, a.clip.cfg.Native}
			if got != tt.want {
				t.Errorf("focusFollows, copyOnSelect, OSC52, Native = %v, want %v", got, tt.want)
			}
		})
	}
}
