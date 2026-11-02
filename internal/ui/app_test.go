package ui

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"regexp"
	"slices"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/charmbracelet/x/exp/golden"

	"github.com/mhersson/aim/internal/core"
	"github.com/mhersson/aim/internal/term"
)

func testLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func newThemedApp(t *testing.T, name string) *App {
	t.Helper()

	return newKeyedApp(t, name, core.KeysConfig{})
}

// newKeyedApp is a test app with the [keys] config keys.
func newKeyedApp(t *testing.T, name string, keys core.KeysConfig) *App {
	t.Helper()

	app, err := New(Options{Logger: testLogger(), Git: newFakeGit(t), Icons: true, Keys: keys, Store: core.NewStore(core.State{Theme: name})})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	// The shell tests assert the empty layout, so the first size message must not open a pane.
	app.started = true
	app.start = func(context.Context, term.Command, int, int) (proc, error) {
		return nil, errors.New("test app does not start processes")
	}
	// A copy must never reach the operator's real clipboard tools.
	app.clip.run = (&fakeRun{}).run
	app.openLink = (&fakeOpen{}).open
	app.tick = (&ticked{}).tick

	return app
}

func TestLeaderWaitsOnTheAppTick(t *testing.T) {
	t.Parallel()

	a := newTestApp(t)
	rec := &ticked{}
	a.tick = rec.tick

	press(a, keyLeader)

	if want := []time.Duration{a.keymap.delay}; !slices.Equal(rec.delays, want) {
		t.Fatalf("tick delays = %v, want %v", rec.delays, want)
	}

	if got := rec.msgs[0]; got != (whichKeyMsg{seq: a.seq}) {
		t.Errorf("tick message = %#v, want whichKeyMsg for seq %d", got, a.seq)
	}
}

func newTestApp(t *testing.T) *App {
	t.Helper()

	return newThemedApp(t, "catppuccin-mocha")
}

func TestNewRequiresLogger(t *testing.T) {
	t.Parallel()

	store := core.NewStore(core.State{Theme: "catppuccin-mocha"})
	if _, err := New(Options{Store: store}); err == nil {
		t.Error("New() without a logger succeeded, want an error")
	}
}

func TestNewRequiresStore(t *testing.T) {
	t.Parallel()

	if _, err := New(Options{Logger: testLogger()}); err == nil {
		t.Error("New() without a store succeeded, want an error")
	}
}

func TestNewRequiresGit(t *testing.T) {
	t.Parallel()

	store := core.NewStore(core.State{Theme: "catppuccin-mocha"})
	if _, err := New(Options{Logger: testLogger(), Store: store}); err == nil {
		t.Error("New() without a git succeeded, want an error")
	}
}

func TestAppViewSizes(t *testing.T) {
	t.Parallel()
	t.Run("before any size", func(t *testing.T) {
		v := newTestApp(t).View()
		if !v.AltScreen {
			t.Error("AltScreen = false, want true")
		}
	})

	tests := []struct {
		w, h     int
		wantWord bool
	}{{100, 30, true}, {20, 5, false}, {1, 1, false}}
	for _, tt := range tests {
		t.Run(fmt.Sprintf("%dx%d", tt.w, tt.h), func(t *testing.T) {
			app := newTestApp(t)
			app.Update(tea.WindowSizeMsg{Width: tt.w, Height: tt.h})

			v := app.View()
			if !v.AltScreen {
				t.Error("AltScreen = false, want true")
			}

			if got := strings.Contains(v.Content, "AIM"); got != tt.wantWord {
				t.Errorf("view contains AIM = %v, want %v", got, tt.wantWord)
			}

			lines := strings.Split(v.Content, "\n")
			if len(lines) != tt.h {
				t.Fatalf("view has %d lines, want %d:\n%s", len(lines), tt.h, v.Content)
			}

			for i, line := range lines {
				if got := lipgloss.Width(line); got != tt.w {
					t.Errorf("line %d width = %d, want %d: %q", i, got, tt.w, line)
				}
			}
		})
	}
}

var (
	keyLeader = tea.KeyPressMsg{Code: tea.KeySpace, Mod: tea.ModCtrl}
	keyEsc    = tea.KeyPressMsg{Code: tea.KeyEscape}
)

func keyRune(r rune) tea.KeyPressMsg {
	return tea.KeyPressMsg{Code: r, Text: string(r)}
}

func press(a *App, keys ...tea.KeyPressMsg) tea.Cmd {
	var cmd tea.Cmd
	for _, k := range keys {
		_, cmd = a.Update(k)
	}

	return cmd
}

func TestLeader(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		keys        []tea.KeyPressMsg
		wantQuit    bool
		wantCmd     bool
		wantPending bool
		wantNotice  string
	}{
		{"leader then q quits", []tea.KeyPressMsg{keyLeader, keyRune('q')}, true, true, false, ""},
		{"leader then esc", []tea.KeyPressMsg{keyLeader, keyEsc}, false, false, false, ""},
		{"leader twice runs no command", []tea.KeyPressMsg{keyLeader, keyLeader}, false, false, false, ""},
		{"leader then unbound", []tea.KeyPressMsg{keyLeader, keyRune('w')}, false, false, false, "ctrl+space w is not bound"},
		{"leader alone", []tea.KeyPressMsg{keyLeader}, false, true, true, ""},
		{"idle ctrl+c is not an app command", []tea.KeyPressMsg{{Code: 'c', Mod: tea.ModCtrl}}, false, false, false, ""},
		{"ctrl+c while pending", []tea.KeyPressMsg{keyLeader, {Code: 'c', Mod: tea.ModCtrl}}, false, false, false, "ctrl+space ctrl+c is not bound"},
		{"idle q is not an app command", []tea.KeyPressMsg{keyRune('q')}, false, false, false, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			a := newTestApp(t)
			sized(t, a, 140, 30)

			cmd := press(a, tt.keys...)
			if (cmd != nil) != tt.wantCmd {
				t.Fatalf("last cmd non-nil = %v, want %v", cmd != nil, tt.wantCmd)
			}

			if tt.wantQuit {
				if _, ok := cmd().(tea.QuitMsg); !ok {
					t.Errorf("cmd() is not tea.QuitMsg")
				}
			}

			if a.pending != tt.wantPending {
				t.Errorf("pending = %v, want %v", a.pending, tt.wantPending)
			}

			if a.notice != tt.wantNotice {
				t.Errorf("notice = %q, want %q", a.notice, tt.wantNotice)
			}

			plain := ansi.Strip(a.View().Content)
			if got := strings.Contains(plain, tt.wantNotice); tt.wantNotice != "" && !got {
				t.Errorf("view lacks notice %q:\n%s", tt.wantNotice, plain)
			}
		})
	}
}

func TestNoticeClearsOnNextKey(t *testing.T) {
	t.Parallel()
	a := newTestApp(t)
	sized(t, a, 100, 30)
	press(a, keyLeader, keyRune('w'))

	if a.notice == "" {
		t.Fatal("no notice after an unbound key")
	}

	press(a, keyRune('y'))

	if a.notice != "" {
		t.Errorf("notice = %q after the next key, want empty", a.notice)
	}

	if strings.Contains(ansi.Strip(a.View().Content), "is not bound") {
		t.Error("view still shows the notice")
	}
}

func hintCount(a *App) int {
	return strings.Count(ansi.Strip(a.View().Content), "Jump to tab")
}

func TestWhichKey(t *testing.T) {
	t.Parallel()
	a := newTestApp(t)
	sized(t, a, 100, 30)
	before := hintCount(a)
	press(a, keyLeader)

	if got := hintCount(a); got != before {
		t.Errorf("popup visible before the delay: %d hints, want %d", got, before)
	}

	a.Update(whichKeyMsg{seq: a.seq})

	plain := ansi.Strip(a.View().Content)
	if got := hintCount(a); got != before+1 {
		t.Errorf("hints = %d, want %d:\n%s", got, before+1, plain)
	}

	for _, want := range []string{
		"\u256d\u2500 " + iconLeader + " ctrl+space ",
		iconPanes + " Panes", iconTabs + " Tabs", iconCog + " App",
		"\u252c", "\u2534",
	} {
		if !strings.Contains(plain, want) {
			t.Errorf("popup lacks %q:\n%s", want, plain)
		}
	}
	// Keys pad to the column's widest key, so the gap after a short key varies.
	for _, row := range []string{`h j k l +Focus pane`, `H J K L +Resize pane`, `\^h\^j\^k\^l +Swap pane`, `1-9 +Jump to tab`} {
		if !regexp.MustCompile(row).MatchString(plain) {
			t.Errorf("popup lacks the row %q:\n%s", row, plain)
		}
	}

	press(a, keyEsc)

	if hintCount(a) != before {
		t.Error("popup still visible after esc")
	}
}

func TestWhichKeyNoIcons(t *testing.T) {
	t.Parallel()
	a := newTestApp(t)
	a.icons = false
	sized(t, a, 100, 30)
	press(a, keyLeader)
	a.Update(whichKeyMsg{seq: a.seq})

	plain := ansi.Strip(a.View().Content)
	for _, want := range []string{"\u256d\u2500 ctrl+space \u2500", "Panes", "Tabs", "App"} {
		if !strings.Contains(plain, want) {
			t.Errorf("popup lacks %q:\n%s", want, plain)
		}
	}

	for _, ic := range []string{iconLeader, iconPanes, iconTabs, iconCog} {
		if strings.Contains(plain, ic) {
			t.Errorf("popup draws glyph %q with icons off", ic)
		}
	}
}

// cellOf finds the first cell of needle in the stripped screen rows.
func cellOf(t *testing.T, rows []string, needle string) (x, y int) {
	t.Helper()

	for y, row := range rows {
		if before, _, ok := strings.Cut(row, needle); ok {
			return lipgloss.Width(before), y
		}
	}

	t.Fatalf("screen lacks %q:\n%s", needle, strings.Join(rows, "\n"))

	return 0, 0
}

func TestWhichKeyColours(t *testing.T) {
	t.Parallel()
	ui := mochaUI(t)
	a := newTestApp(t)
	sized(t, a, 100, 30)
	press(a, keyLeader)
	a.Update(whichKeyMsg{seq: a.seq})
	content := a.View().Content
	vt := term.NewVT(100, 30, io.Discard)

	t.Cleanup(func() { _ = vt.Close() })

	if _, err := vt.Write([]byte(strings.ReplaceAll(content, "\n", "\r\n"))); err != nil {
		t.Fatal(err)
	}

	rows := strings.Split(ansi.Strip(content), "\n")

	x, y := cellOf(t, rows, "\u256d\u2500 "+iconLeader)
	assertColours(t, vt, x, y, "\u256d", ui.BorderFocus, ui.Panel, false)
	assertColours(t, vt, x+2, y, " ", ui.Accent, ui.Panel, true)
	assertColours(t, vt, x+3, y, iconLeader, ui.Accent, ui.Panel, true)
	assertColours(t, vt, x+5, y, "c", ui.Accent, ui.Panel, true)

	jx, jy := cellOf(t, rows, "\u252c")
	assertColours(t, vt, jx, jy, "\u252c", ui.Border, ui.Panel, false)
	bx, by := cellOf(t, rows, "\u2534")
	assertColours(t, vt, bx, by, "\u2534", ui.Border, ui.Panel, false)

	hx, hy := cellOf(t, rows, iconTabs+" Tabs")
	assertColours(t, vt, hx, hy, iconTabs, ui.Accent, ui.Panel, true)
	assertColours(t, vt, hx+2, hy, "T", ui.Accent, ui.Panel, true)
	assertColours(t, vt, hx+7, hy, "\u2500", ui.Border, ui.Panel, false)

	kx, ky := cellOf(t, rows, "\u2502 q ")
	assertColours(t, vt, kx+2, ky, "q", ui.Key, ui.Panel, true)
	tx, ty := cellOf(t, rows, "Quit AIM")
	assertColours(t, vt, tx, ty, "Q", ui.Fg, ui.Panel, false)
}

// bindingsWithout returns the default keymap minus the given keys.
func bindingsWithout(keys ...string) keymap {
	km := defaultKeymap()
	km.bindings = slices.DeleteFunc(km.bindings, func(b binding) bool { return slices.Contains(keys, b.key) })

	return km
}

func TestHintsDeriveFromBindings(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		unbind  []string
		want    []string
		notWant []string
	}{
		{
			"all bound", nil,
			[]string{"h j k l|Focus pane", "H J K L|Resize pane", "^h^j^k^l|Swap pane", "1-9|Jump to tab"},
			[]string{"h|Focus left"},
		},
		{
			"partial focus",
			[]string{"j"},
			[]string{"h|Focus left", "k|Focus up", "l|Focus right"},
			[]string{"h j k l|Focus pane"},
		},
		{
			"partial jump",
			[]string{"5"},
			[]string{"1|Jump to tab 1", "9|Jump to tab 9"},
			[]string{"1-9|Jump to tab"},
		},
		{
			"no swap",
			[]string{"ctrl+h", "ctrl+j", "ctrl+k", "ctrl+l"},
			nil,
			[]string{"Swap pane", "Swap left"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			a := newTestApp(t)
			a.keymap = bindingsWithout(tt.unbind...)

			var got []string
			for _, h := range a.hints() {
				got = append(got, h.key+"|"+h.title)
			}

			for _, w := range tt.want {
				if !slices.Contains(got, w) {
					t.Errorf("hints lack %q: %q", w, got)
				}
			}

			for _, w := range tt.notWant {
				if slices.ContainsFunc(got, func(g string) bool { return strings.Contains(g, w) }) {
					t.Errorf("hints contain %q: %q", w, got)
				}
			}
		})
	}
}

func TestHintGroups(t *testing.T) {
	t.Parallel()
	a := newTestApp(t)
	groups := map[string]string{}

	for _, h := range a.hints() {
		if h.group == "" {
			t.Errorf("hint %q has no group", h.title)
		}

		groups[h.title] = h.group
	}

	for title, want := range map[string]string{
		"Focus pane": "Panes", "Zoom pane": "Panes", "New tab": "Tabs",
		"Jump to tab": "Tabs", "Quit AIM": "App", "Pick theme": "App",
	} {
		if groups[title] != want {
			t.Errorf("group of %q = %q, want %q", title, groups[title], want)
		}
	}
}

func TestStaleWhichKey(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		keys []tea.KeyPressMsg
	}{
		{"esc", []tea.KeyPressMsg{keyEsc}},
		{"bound key", []tea.KeyPressMsg{keyRune('q')}},
		{"second leader press after esc", []tea.KeyPressMsg{keyEsc, keyLeader}},
		{"second leader press", []tea.KeyPressMsg{keyLeader}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			a := newTestApp(t)
			sized(t, a, 100, 30)
			press(a, keyLeader)
			first := a.seq
			before := hintCount(a)
			press(a, tt.keys...)
			a.Update(whichKeyMsg{seq: first})

			if got := hintCount(a); got != before {
				t.Errorf("stale tick opened the popup: %d hints, want %d", got, before)
			}

			if a.popup {
				t.Error("popup = true after a stale tick")
			}
		})
	}
}

func TestWhichKeySizes(t *testing.T) {
	t.Parallel()

	for _, sz := range [][2]int{{100, 30}, {80, 24}, {40, 12}, {20, 5}, {1, 1}} {
		t.Run(fmt.Sprintf("%dx%d", sz[0], sz[1]), func(t *testing.T) {
			a := newTestApp(t)
			sized(t, a, sz[0], sz[1])
			press(a, keyLeader)
			a.Update(whichKeyMsg{seq: a.seq})
			content := a.View().Content

			lines := strings.Split(content, "\n")
			if len(lines) != sz[1] {
				t.Fatalf("view has %d lines, want %d", len(lines), sz[1])
			}

			for i, line := range lines {
				if got := lipgloss.Width(line); got != sz[0] {
					t.Errorf("line %d width = %d, want %d: %q", i, got, sz[0], line)
				}
			}

			plain := ansi.Strip(content)

			switch sz[0] {
			case 100, 80:
				// Fits whole: the bottom border sits on the last row, one cell from the edge.
				bottom := ansi.Strip(lines[sz[1]-1])
				if !strings.HasSuffix(bottom, "\u256f ") || !strings.Contains(bottom, "\u2534") ||
					!strings.Contains(plain, "Quit AIM") || !strings.Contains(plain, "Jump to tab") {
					t.Errorf("popup not drawn whole:\n%s", plain)
				}
			case 40:
				// The popup is taller than the body: its top rows show, the rest is clipped.
				if !strings.Contains(plain, "\u256d\u2500 "+iconLeader+" ctrl+space") || !strings.Contains(plain, "Panes") {
					t.Errorf("popup top not drawn:\n%s", plain)
				}
			case 20:
				if !strings.Contains(plain, "\u256d\u2500 "+iconLeader+" ctrl") || strings.Contains(plain, "\u256e") {
					t.Errorf("popup not clipped on the right:\n%s", plain)
				}
			}
		})
	}
}

// It compares highlighted colours, so it runs serially (docs/gotchas.md).
func TestWhichKeyGolden(t *testing.T) {
	a := newTestApp(t)
	sized(t, a, 100, 30)
	press(a, keyLeader)
	a.Update(whichKeyMsg{seq: a.seq})
	golden.RequireEqual(t, []byte(a.View().Content))
}

// It compares highlighted colours, so it runs serially (docs/gotchas.md).
func TestWhichKeyNoIconsGolden(t *testing.T) {
	a := newTestApp(t)
	a.icons = false
	sized(t, a, 100, 30)
	press(a, keyLeader)
	a.Update(whichKeyMsg{seq: a.seq})
	golden.RequireEqual(t, []byte(a.View().Content))
}

// It compares highlighted colours, so it runs serially (docs/gotchas.md).
func TestLeaderPendingGolden(t *testing.T) {
	a := newTestApp(t)
	sized(t, a, 100, 30)
	press(a, keyLeader)
	golden.RequireEqual(t, []byte(a.View().Content))
}

func TestDispatchSyncsOnlyThemeChanges(t *testing.T) {
	t.Parallel()
	a := newTestApp(t)
	sized(t, a, 100, 30)

	chord(a, 'v')
	a.sample = []string{"sentinel"}

	chord(a, 'h')

	if len(a.sample) != 1 || a.sample[0] != "sentinel" {
		t.Fatal("a focus move re-highlighted the sample")
	}

	if err := a.dispatch(core.SetTheme{Name: "catppuccin-latte"}); err != nil {
		t.Fatal(err)
	}

	if len(a.sample) == 1 && a.sample[0] == "sentinel" {
		t.Error("a theme change did not rebuild the sample")
	}
}
