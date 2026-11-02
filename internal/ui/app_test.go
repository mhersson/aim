package ui

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
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

	app, err := New(Options{Logger: testLogger(), Git: newFakeGit(t), Icons: true, Keys: keys, caches: testDrawCaches, Store: core.NewStore(core.State{Theme: name})})
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

func TestNewMakesItsOwnDrawCaches(t *testing.T) {
	t.Parallel()

	opts := func() Options {
		return Options{Logger: testLogger(), Git: newFakeGit(t), Store: core.NewStore(core.State{Theme: "catppuccin-mocha"})}
	}

	a, err := New(opts())
	if err != nil {
		t.Fatal(err)
	}

	b, err := New(opts())
	if err != nil {
		t.Fatal(err)
	}

	if a.drawer.hl == nil || a.drawer.caches == nil || b.drawer.hl == nil || b.drawer.caches == nil {
		t.Fatal("an app without Options.caches has no highlighter or draw caches")
	}

	if a.drawer.hl == b.drawer.hl || a.drawer.caches == b.drawer.caches {
		t.Error("two apps share a highlighter or draw caches without being given them")
	}

	o := opts()
	o.caches = testDrawCaches

	c, err := New(o)
	if err != nil {
		t.Fatal(err)
	}

	if c.drawer.caches != testDrawCaches || c.drawer.hl != &testDrawCaches.hl {
		t.Error("an app given Options.caches does not use them")
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

// testDrawCaches is the one lexer and theme cache every test app shares.
var testDrawCaches = &drawCaches{}

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
		wantNotice  []string
	}{
		{"leader then q quits", []tea.KeyPressMsg{keyLeader, keyRune('q')}, true, true, false, nil},
		{"leader then esc", []tea.KeyPressMsg{keyLeader, keyEsc}, false, false, false, nil},
		{"leader then unbound", []tea.KeyPressMsg{keyLeader, keyRune('w')}, false, false, false, []string{"ctrl+space w"}},
		{"leader alone", []tea.KeyPressMsg{keyLeader}, false, true, true, nil},
		{"idle ctrl+c is not an app command", []tea.KeyPressMsg{{Code: 'c', Mod: tea.ModCtrl}}, false, false, false, nil},
		{"ctrl+c while pending", []tea.KeyPressMsg{keyLeader, {Code: 'c', Mod: tea.ModCtrl}}, false, false, false, []string{"ctrl+space ctrl+c"}},
		{"idle q is not an app command", []tea.KeyPressMsg{keyRune('q')}, false, false, false, nil},
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

			if want := carries(tt.wantNotice...); !want.matches(a.notice) || (tt.wantNotice != nil && a.noticeLevel() != levelNotice) {
				t.Errorf("notice = %q at level %v, want one carrying %q at notice level", a.notice, a.noticeLevel(), tt.wantNotice)
			}

			if plain := ansi.Strip(a.View().Content); a.notice != "" && !strings.Contains(plain, a.notice) {
				t.Errorf("view lacks notice %q:\n%s", a.notice, plain)
			}
		})
	}
}

func TestNoticeClearsOnNextKey(t *testing.T) {
	t.Parallel()
	a := newTestApp(t)
	sized(t, a, 100, 30)
	press(a, keyLeader, keyRune('w'))

	shown := a.notice
	if shown == "" {
		t.Fatal("no notice after an unbound key")
	}

	press(a, keyRune('y'))

	if a.notice != "" {
		t.Errorf("notice = %q after the next key, want empty", a.notice)
	}

	if strings.Contains(ansi.Strip(a.View().Content), shown) {
		t.Error("view still shows the notice")
	}
}

func TestWhichKey(t *testing.T) {
	t.Parallel()
	a := newTestApp(t)
	sized(t, a, 100, 30)
	before := a.View().Content
	press(a, keyLeader)

	if a.popup || a.View().Content != before {
		t.Error("popup drawn before the delay")
	}

	a.Update(whichKeyMsg{seq: a.seq})

	if !a.popup || a.View().Content == before {
		t.Error("popup not drawn after the delay")
	}

	press(a, keyEsc)

	if a.popup || a.View().Content != before {
		t.Error("popup still visible after esc")
	}
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
			"partial focus",
			[]string{"j"},
			[]string{"h", "k", "l"},
			[]string{"h j k l"},
		},
		{
			"partial jump",
			[]string{"5"},
			[]string{"1", "9"},
			[]string{"1-9"},
		},
		{
			"no swap",
			[]string{"ctrl+h", "ctrl+j", "ctrl+k", "ctrl+l"},
			nil,
			[]string{"^h^j^k^l", "ctrl+h"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			a := newTestApp(t)
			a.keymap = bindingsWithout(tt.unbind...)

			var got []string
			for _, h := range a.hints() {
				got = append(got, h.key)
			}

			for _, w := range tt.want {
				if !slices.Contains(got, w) {
					t.Errorf("hint keys lack %q: %q", w, got)
				}
			}

			for _, w := range tt.notWant {
				if slices.Contains(got, w) {
					t.Errorf("hint keys contain %q: %q", w, got)
				}
			}
		})
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
			press(a, tt.keys...)
			a.Update(whichKeyMsg{seq: first})

			if a.popup {
				t.Error("popup = true after a stale tick")
			}
		})
	}
}

func TestWhichKeySizes(t *testing.T) {
	t.Parallel()

	for _, sz := range [][2]int{{80, 24}, {40, 12}, {20, 5}, {1, 1}} {
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
			case 80:
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
