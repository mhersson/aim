package ui

import (
	"fmt"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/charmbracelet/x/exp/golden"

	"github.com/mhersson/aim/internal/core"
)

var (
	keyTab = tea.KeyPressMsg{Code: tea.KeyTab}
	// keyShiftTab is what CSI Z decodes to; keyShiftTabKitty is the Kitty form with a base key.
	keyShiftTab      = tea.KeyPressMsg{Code: tea.KeyTab, Mod: tea.ModShift}
	keyShiftTabKitty = tea.KeyPressMsg{Code: tea.KeyTab, Mod: tea.ModShift, BaseCode: tea.KeyTab}
	keyBackspace     = tea.KeyPressMsg{Code: tea.KeyBackspace}
)

// tabsOf starts an app with n tabs of one shell each; the last tab is active.
func tabsOf(t *testing.T, n int) (*App, *starts) {
	t.Helper()

	a, st := lifeAppSized(t, 100, 30)
	for range n - 1 {
		chord(a, 'c')
	}

	return a, st
}

func activeIndex(a *App) int { return a.store.State().Active }

// assertTabSizes checks that every pane has the content size of its rect in its
// own tab's layout, hidden tabs included.
func assertTabSizes(t *testing.T, a *App) {
	t.Helper()

	st := a.store.State()

	for id, p := range a.panes {
		i, ok := st.TabOf(id)
		if !ok {
			t.Errorf("pane %s is in no tab", id)

			continue
		}

		r, ok := a.tabRects(i)[id]
		if !ok {
			continue
		}

		c := contentRect(r)
		if cols, rows := p.size(); cols != max(c.W, 1) || rows != max(c.H, 1) {
			t.Errorf("pane %s size = %dx%d, want %dx%d", id, cols, rows, c.W, c.H)
		}
	}
}

func TestTabKeys(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		key        tea.KeyPressMsg
		wantTabs   int
		wantActive int
		wantPrompt bool
	}{
		{"c", keyRune('c'), 4, 3, false},
		{",", keyRune(','), 3, 2, true},
		{"X with text", keyRune('X'), 2, 1, false},
		{"X shifted code", tea.KeyPressMsg{Code: 'x', Mod: tea.ModShift, ShiftedCode: 'X', Text: "X"}, 2, 1, false},
		{"X shifted code without text", tea.KeyPressMsg{Code: 'x', Mod: tea.ModShift, ShiftedCode: 'X'}, 2, 1, false},
		{"1", keyRune('1'), 3, 0, false},
		{"2", keyRune('2'), 3, 1, false},
		{"9 is past the last tab", keyRune('9'), 3, 2, false},
		{"shift+tab kitty", keyShiftTabKitty, 3, 1, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			a, _ := tabsOf(t, 3)

			press(a, keyLeader, tt.key)

			st := a.store.State()
			if len(st.Tabs) != tt.wantTabs || st.Active != tt.wantActive {
				t.Errorf("tabs = %d, active = %d, want %d, %d", len(st.Tabs), st.Active, tt.wantTabs, tt.wantActive)
			}

			if (a.prompt != nil) != tt.wantPrompt {
				t.Errorf("prompt open = %v, want %v", a.prompt != nil, tt.wantPrompt)
			}

			if a.pending {
				t.Error("the leader is still pending")
			}
		})
	}
}

func TestTabCycleWraps(t *testing.T) {
	t.Parallel()
	a, _ := tabsOf(t, 3)

	for _, want := range []int{0, 1, 2} {
		press(a, keyLeader, keyTab)

		if got := activeIndex(a); got != want {
			t.Fatalf("active after tab = %d, want %d", got, want)
		}
	}

	for _, want := range []int{1, 0, 2} {
		press(a, keyLeader, keyShiftTab)

		if got := activeIndex(a); got != want {
			t.Fatalf("active after shift+tab = %d, want %d", got, want)
		}
	}
}

func TestNewTabStartsShell(t *testing.T) {
	t.Parallel()
	a, st := lifeAppSized(t, 100, 30)

	if err := a.dispatch(core.SetPaneCwd{ID: "p1", Cwd: "/elsewhere"}); err != nil {
		t.Fatal(err)
	}

	chord(a, 'c')

	s := a.store.State()
	if len(s.Tabs) != 2 || s.Active != 1 || s.Tab().Focus != "p2" || s.Panes["p2"].Kind != core.KindShell || s.Panes["p2"].Cwd != "/elsewhere" {
		t.Fatalf("state = %+v, want tab 2 active with a shell p2 in /elsewhere", s)
	}

	if got := st.cmds[1]; got.Dir != "/elsewhere" {
		t.Errorf("command dir = %q, want /elsewhere", got.Dir)
	}

	// The body is 100x28; the frame takes one cell on every side.
	if st.sizes[1] != [2]int{98, 26} {
		t.Errorf("start size = %v, want [98 26]", st.sizes[1])
	}

	if a.panes["p2"] == nil {
		t.Fatal("p2 is not in the registry")
	}

	assertTabSizes(t, a)
}

func TestNewTabInLaunchDir(t *testing.T) {
	t.Parallel()
	a, st := lifeAppSized(t, 100, 30)

	chord(a, 'x')
	chord(a, 'c')

	if got := st.cmds[1].Dir; got != testCwd {
		t.Errorf("command dir = %q, want the launch dir %q", got, testCwd)
	}

	if got := len(a.store.State().Tabs); got != 1 {
		t.Errorf("tabs = %d, want the emptied tab refilled", got)
	}
}

func TestNewTabStartFailure(t *testing.T) {
	t.Parallel()
	a, st := lifeAppSized(t, 100, 30)
	st.err = fmt.Errorf("no such shell")

	chord(a, 'c')

	if got := len(a.store.State().Tabs); got != 1 {
		t.Errorf("tabs after a failed start = %d, want 1", got)
	}

	if !strings.Contains(a.notice, "no such shell") {
		t.Errorf("notice = %q, want the start error", a.notice)
	}
}

func TestRenamePrompt(t *testing.T) {
	t.Parallel()
	a, st := lifeAppSized(t, 100, 30)

	cursorAtColumn := func(x int) {
		t.Helper()

		got := a.View().Cursor
		if got == nil || got.X != x || got.Y != 0 {
			t.Errorf("cursor = %+v, want column %d on row 0", got, x)
		}
	}
	barRow := func() string { return strings.Split(ansi.Strip(a.View().Content), "\n")[0] }
	tabName := func() string { return a.store.State().Tab().Name }

	chord(a, ',')

	if a.prompt == nil || a.prompt.input.Value() != "" {
		t.Fatalf("prompt = %+v, want one pre-filled with the empty custom name", a.prompt)
	}

	if got := strings.Join(screenRows(a), "\n"); strings.Contains(got, "Rename tab") || strings.Contains(got, "enter save") {
		t.Errorf("screen shows a rename label or legend:\n%s", got)
	}

	// " 1 " then the rename icon and a space.
	cursorAtColumn(5)

	for _, r := range "api" {
		press(a, keyRune(r))
	}

	if got := barRow(); !strings.HasPrefix(got, " 1 "+iconRename+" api ") {
		t.Errorf("bar = %q, want the input in the first segment", got)
	}

	cursorAtColumn(8)

	if tabName() != "" {
		t.Errorf("name = %q before enter, want unchanged", tabName())
	}

	press(a, keyEnter)

	if a.prompt != nil || tabName() != "api" {
		t.Errorf("prompt = %v, name = %q, want a closed prompt and the name api", a.prompt, tabName())
	}

	if got := barRow(); !strings.HasPrefix(got, " 1 "+iconShell+" api ") {
		t.Errorf("bar = %q, want the plain segment named api", got)
	}

	// Pre-filled with the custom name; typing and paste edit it, esc drops the edit.
	chord(a, ',')

	if got := a.prompt.input.Value(); got != "api" {
		t.Fatalf("pre-filled value = %q, want api", got)
	}

	press(a, keyRune('x'))
	a.Update(tea.PasteMsg{Content: "-two"})

	if got := a.prompt.input.Value(); got != "apix-two" {
		t.Errorf("value = %q, want apix-two", got)
	}

	press(a, keyLeader, keyRune('q'))

	if a.prompt == nil || a.pending {
		t.Error("the prompt did not own the leader and the q key")
	}

	press(a, keyEsc)

	if a.prompt != nil || tabName() != "api" {
		t.Errorf("prompt = %v, name = %q after esc, want a closed prompt and the name api", a.prompt, tabName())
	}

	if got := st.proc(0).sent(); got != "" {
		t.Errorf("the pane received %q behind the prompt", got)
	}

	// An empty value restores the automatic name.
	chord(a, ',')
	press(a, keyBackspace, keyBackspace, keyBackspace, keyEnter)

	if tabName() != "" {
		t.Errorf("name = %q after clearing, want the automatic name", tabName())
	}

	if got := barRow(); !strings.HasPrefix(got, " 1 "+iconShell+" work ") {
		t.Errorf("bar = %q, want the automatic name work", got)
	}
}

func TestRenamePromptWithoutIcons(t *testing.T) {
	t.Parallel()
	a, _ := lifeAppSized(t, 100, 30)
	a.icons = false

	chord(a, ',')
	press(a, keyRune('a'))

	if got, _, _ := strings.Cut(ansi.Strip(a.View().Content), "\n"); !strings.HasPrefix(got, " 1 a ") {
		t.Errorf("bar = %q, want ` 1 a `", got)
	}

	if got := a.View().Cursor; got == nil || got.X != 4 || got.Y != 0 {
		t.Errorf("cursor = %+v, want column 4 on row 0", got)
	}
}

func TestRenameSegmentColours(t *testing.T) {
	t.Parallel()
	ui := mochaUI(t)
	a, _ := lifeAppSized(t, 60, 8)

	chord(a, ',')
	press(a, keyRune('a'), keyRune('p'), keyRune('i'))

	vt := lineVT(t, a.workspaceBar(60), 60)

	assertColours(t, vt, 0, 0, " ", ui.Fg, ui.Bg, false)
	assertColours(t, vt, 1, 0, "1", ui.Accent, ui.Bg, true)
	assertColours(t, vt, 3, 0, iconRename, ui.Accent, ui.Bg, true)
	assertColours(t, vt, 5, 0, "a", ui.Fg, ui.Bg, false)
	assertColours(t, vt, 8, 0, " ", ui.Fg, ui.Bg, false)
	assertColours(t, vt, 9, 0, edgeSolid, ui.Bg, ui.Bar, false)
}

func TestRenameGolden(t *testing.T) {
	t.Parallel()
	a, _ := lifeAppSized(t, 60, 8)

	chord(a, 'c')
	press(a, keyLeader, keyRune('1'))
	chord(a, ',')
	press(a, keyRune('a'), keyRune('p'), keyRune('i'))

	golden.RequireEqual(t, []byte(a.View().Content))
}

func TestCloseTabConfirmGolden(t *testing.T) {
	t.Parallel()
	a, _ := lifeAppSized(t, 60, 14)

	chord(a, 'c')
	chord(a, 'n')
	chord(a, 'X')

	if a.confirm == nil {
		t.Fatal("no confirm")
	}

	golden.RequireEqual(t, []byte(a.View().Content))
}

func TestConfirmBoxShape(t *testing.T) {
	t.Parallel()
	a, _ := lifeAppSized(t, 100, 30)

	chord(a, 'n')
	chord(a, 'x')

	lines := strings.Split(ansi.Strip(a.View().Content), "\n")

	top := 10 // a third of 30
	if !strings.Contains(lines[top], "╭") || !strings.Contains(lines[top+6], "╰") {
		t.Errorf("box not at row %d:\n%s", top, strings.Join(lines, "\n"))
	}

	x := strings.Index(lines[top], "╭")
	if w := len([]rune(lines[top][x:])); w < 46 {
		t.Errorf("box row %q is narrower than 46 cells", lines[top][x:])
	}

	for _, want := range []string{"[y]", "[n]"} {
		if !strings.Contains(strings.Join(lines[top+1:top+6], "\n"), want) {
			t.Errorf("box lacks %q:\n%s", want, strings.Join(lines[top:top+7], "\n"))
		}
	}

	// A narrow terminal clips the box instead of wrapping it.
	sized(t, a, 20, 14)

	for i, l := range strings.Split(a.View().Content, "\n") {
		if w := ansi.StringWidth(l); w != 20 {
			t.Errorf("line %d is %d cells, want 20", i, w)
		}
	}
}

func TestCloseTabConfirm(t *testing.T) {
	t.Parallel()
	t.Run("shells close at once", func(t *testing.T) {
		a, st := lifeAppSized(t, 100, 30)

		chord(a, 'c')
		chord(a, 'v')
		chord(a, 'X')

		s := a.store.State()
		if a.confirm != nil || len(s.Tabs) != 1 || len(s.Panes) != 1 || s.Active != 0 || len(a.panes) != 1 {
			t.Fatalf("state = %+v, confirm = %v, panes = %d, want one tab with p1 and no confirm", s, a.confirm, len(a.panes))
		}

		eventually(t, "tab processes closed", func() bool { return isClosed(st.proc(1)) && isClosed(st.proc(2)) })

		if isClosed(st.proc(0)) {
			t.Error("the other tab's process was closed")
		}

		assertSizes(t, a)
	})

	t.Run("agents ask", func(t *testing.T) {
		tests := []struct {
			name      string
			key       tea.KeyPressMsg
			wantTabs  int
			wantClose bool
		}{
			{"n keeps", keyRune('n'), 2, false},
			{"y closes", keyRune('y'), 1, true},
		}

		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				a, st := lifeAppSized(t, 100, 30)

				chord(a, 'c')
				chord(a, 'n')
				chord(a, 'X')

				if a.confirm == nil || a.confirm.tab == "" {
					t.Fatal("no confirm for the tab")
				}

				if got := len(a.store.State().Tabs); got != 2 {
					t.Fatalf("tabs before the answer = %d, want 2", got)
				}

				press(a, tt.key)

				s := a.store.State()
				if len(s.Tabs) != tt.wantTabs || a.confirm != nil {
					t.Errorf("tabs = %d, confirm = %v, want %d tabs and no confirm", len(s.Tabs), a.confirm, tt.wantTabs)
				}

				if tt.wantClose {
					eventually(t, "tab processes closed", func() bool { return isClosed(st.proc(1)) && isClosed(st.proc(2)) })

					if s.Active != 0 || len(a.panes) != 1 {
						t.Errorf("active = %d, panes = %d, want the first tab and one pane", s.Active, len(a.panes))
					}
				}

				if isClosed(st.proc(0)) {
					t.Error("the other tab's process was closed")
				}
			})
		}
	})

	t.Run("the only tab stays empty", func(t *testing.T) {
		a, st := lifeAppSized(t, 100, 30)

		chord(a, 'X')

		s := a.store.State()
		if len(s.Tabs) != 1 || !s.Tab().Empty() || len(s.Panes) != 0 || len(a.panes) != 0 {
			t.Errorf("state = %+v, want one empty tab", s)
		}

		eventually(t, "process closed", func() bool { return isClosed(st.proc(0)) })
	})
}

func TestHiddenPaneExit(t *testing.T) {
	t.Parallel()
	// hiddenTab leaves tab 1 active and tab 2 hidden with p2 and p3.
	hiddenTab := func(t *testing.T) (*App, *starts) {
		t.Helper()

		a, st := lifeAppSized(t, 100, 30)

		chord(a, 'c')
		chord(a, 'v')

		if err := a.dispatch(core.SelectTab{Index: 0}); err != nil {
			t.Fatal(err)
		}

		return a, st
	}

	t.Run("a hidden tab loses the pane", func(t *testing.T) {
		a, st := hiddenTab(t)

		a.Update(exitOf(t, a, "p3", st.proc(2)))

		s := a.store.State()
		if len(s.Tabs) != 2 || s.Active != 0 || s.Tab().Focus != "p1" || s.Tabs[1].Focus != "p2" || s.Tabs[1].Empty() || a.panes["p3"] != nil {
			t.Fatalf("state = %+v, want tab 2 left with p2 and tab 1 untouched", s)
		}

		assertTabSizes(t, a)
		eventually(t, "exited pane's process closed", func() bool { return isClosed(st.proc(2)) })

		if isClosed(st.proc(0)) || isClosed(st.proc(1)) {
			t.Error("a surviving pane was closed")
		}
	})

	t.Run("an emptied hidden tab disappears", func(t *testing.T) {
		a, st := hiddenTab(t)

		a.Update(exitOf(t, a, "p3", st.proc(2)))
		a.Update(exitOf(t, a, "p2", st.proc(1)))

		s := a.store.State()
		if len(s.Tabs) != 1 || s.Active != 0 || s.Tab().Focus != "p1" || len(s.Panes) != 1 {
			t.Errorf("state = %+v, want only tab 1 with p1", s)
		}

		assertTabSizes(t, a)
	})

	t.Run("an emptied tab to the left keeps the active tab", func(t *testing.T) {
		a, st := lifeAppSized(t, 100, 30)

		chord(a, 'c')

		if got := activeIndex(a); got != 1 {
			t.Fatalf("active = %d, want 1", got)
		}

		a.Update(exitOf(t, a, "p1", st.proc(0)))

		s := a.store.State()
		if len(s.Tabs) != 1 || s.Tab().Focus != "p2" || s.Tab().ID != "t2" {
			t.Errorf("state = %+v, want only tab t2 with p2 active", s)
		}
	})

	t.Run("its confirm closes", func(t *testing.T) {
		a, st := lifeAppSized(t, 100, 30)

		chord(a, 'c')
		chord(a, 'n')
		chord(a, 'X')

		if a.confirm == nil {
			t.Fatal("no confirm")
		}

		if err := a.dispatch(core.SelectTab{Index: 0}); err != nil {
			t.Fatal(err)
		}

		a.Update(exitOf(t, a, "p3", st.proc(2)))

		if a.confirm == nil {
			t.Error("the confirm closed while its tab still has a pane")
		}

		a.Update(exitOf(t, a, "p2", st.proc(1)))

		if a.confirm != nil {
			t.Error("the confirm for the vanished tab stayed open")
		}

		// Late answers are harmless.
		press(a, keyRune('y'))

		if s := a.store.State(); len(s.Tabs) != 1 || s.Tab().Focus != "p1" {
			t.Errorf("state = %+v, want only tab 1", s)
		}
	})

	t.Run("its prompt closes", func(t *testing.T) {
		a, st := lifeAppSized(t, 100, 30)

		chord(a, 'c')
		chord(a, ',')

		if a.prompt == nil {
			t.Fatal("no prompt")
		}

		if err := a.dispatch(core.SelectTab{Index: 0}); err != nil {
			t.Fatal(err)
		}

		if got, _, _ := strings.Cut(ansi.Strip(a.View().Content), "\n"); strings.Contains(got, iconRename) {
			t.Errorf("bar draws the input of a hidden tab: %q", got)
		}

		a.Update(exitOf(t, a, "p2", st.proc(1)))

		if a.prompt != nil {
			t.Error("the prompt for the vanished tab stayed open")
		}
	})

	t.Run("an unrelated confirm survives", func(t *testing.T) {
		a, st := lifeAppSized(t, 100, 30)

		chord(a, 'n')
		chord(a, 'c')
		chord(a, 'v')

		if err := a.dispatch(core.SelectTab{Index: 0}); err != nil {
			t.Fatal(err)
		}

		chord(a, 'q')

		if a.confirm == nil {
			t.Fatal("no quit confirm")
		}

		a.Update(exitOf(t, a, "p4", st.proc(3)))

		if a.confirm == nil {
			t.Error("a confirm about no tab closed with a hidden pane")
		}
	})
}

func TestTabSwitchFocusReports(t *testing.T) {
	t.Parallel()
	a, st := lifeAppSized(t, 100, 30)

	feedAndWait(t, a.panes["p1"], st.proc(0), "\x1b[?1004h")
	chord(a, 'c')
	feedAndWait(t, a.panes["p2"], st.proc(1), "\x1b[?1004h")

	if got := settled(t, a, "p1", st.proc(0)); got != "\x1b[O" {
		t.Errorf("old pane got %q on the new tab, want focus-out", got)
	}

	press(a, keyLeader, keyTab)

	if got := settled(t, a, "p2", st.proc(1)); got != "\x1b[O" {
		t.Errorf("old pane got %q, want focus-out", got)
	}

	if got := settled(t, a, "p1", st.proc(0)); got != "\x1b[O\x1b[I" {
		t.Errorf("new pane got %q, want focus-in", got)
	}

	a.Update(tea.BlurMsg{})
	press(a, keyLeader, keyTab)

	if got := settled(t, a, "p1", st.proc(0)); got != "\x1b[O\x1b[I\x1b[O" {
		t.Errorf("blurred terminal still reported to the old pane: %q", got)
	}

	if got := settled(t, a, "p2", st.proc(1)); got != "\x1b[O" {
		t.Errorf("blurred terminal still reported to the new pane: %q", got)
	}
}

func TestDrawnWidth(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name, in string
		want     int
	}{
		{"ascii", "abc", 3},
		{"control character", "a\x01b", 2},
		{"escape sequence", "\x1b[31m", 4},
		{"wide rune", "a\u4e2d", 3},
		{"empty", "", 0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := drawnWidth(tt.in); got != tt.want {
				t.Errorf("drawnWidth(%q) = %d, want %d", tt.in, got, tt.want)
			}
		})
	}
}
