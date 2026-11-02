package ui

import (
	"bytes"
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/charmbracelet/x/exp/teatest/v2"

	"github.com/mhersson/aim/internal/core"
	"github.com/mhersson/aim/internal/term"
)

func TestQuitFlow(t *testing.T) {
	t.Parallel()
	tm := teatest.NewTestModel(t, newTestApp(t), teatest.WithInitialTermSize(100, 30))
	tm.Send(keyLeader)
	tm.Send(keyRune('q'))
	tm.WaitFinished(t, teatest.WithFinalTimeout(3*time.Second))
}

// runFlow sends keys to a teatest program, waits until the output sets the
// wanted theme background, quits and returns the final app.
func runFlow(t *testing.T, app *App, want string, keys ...tea.KeyPressMsg) *App {
	t.Helper()

	tm := teatest.NewTestModel(t, app, teatest.WithInitialTermSize(100, 30))
	for _, k := range keys {
		tm.Send(k)
	}

	r, g, b, _ := mustTheme(t, want).UI.Bg.RGBA()
	// Bubble Tea reports View.BackgroundColor to the terminal as OSC 11.
	bg := fmt.Sprintf("]11;#%02x%02x%02x", r>>8, g>>8, b>>8)

	teatest.WaitFor(t, tm.Output(), func(out []byte) bool { return bytes.Contains(out, []byte(bg)) },
		teatest.WithDuration(3*time.Second))

	if err := tm.Quit(); err != nil {
		t.Fatalf("Quit() error = %v", err)
	}

	return tm.FinalModel(t, teatest.WithFinalTimeout(3*time.Second)).(*App)
}

func TestPaletteFlow(t *testing.T) {
	t.Parallel()

	keys := []tea.KeyPressMsg{keyLeader, keyRune(':')}
	for _, r := range "macchiato" {
		keys = append(keys, keyRune(r))
	}

	final := runFlow(t, newTestApp(t), "catppuccin-macchiato", append(keys, keyEnter)...)
	if got := final.store.State().Theme; got != "catppuccin-macchiato" {
		t.Errorf("theme = %q, want catppuccin-macchiato", got)
	}
}

// TestThemePickerFlow waits for a card row hidden under the open picker, so
// only the frame drawn after the commit can satisfy it (the preview already sets Latte's OSC 11).
func TestThemePickerFlow(t *testing.T) {
	t.Parallel()

	tm := teatest.NewTestModel(t, newTestApp(t), teatest.WithInitialTermSize(100, 30))
	for _, k := range []tea.KeyPressMsg{keyLeader, keyRune('t'), keyDown} {
		tm.Send(k)
	}

	r, g, b, _ := mustTheme(t, "catppuccin-latte").UI.Bg.RGBA()
	preview := fmt.Sprintf("]11;#%02x%02x%02x", r>>8, g>>8, b>>8)

	teatest.WaitFor(t, tm.Output(), func(out []byte) bool { return bytes.Contains(out, []byte(preview)) },
		teatest.WithDuration(3*time.Second))
	tm.Send(keyEnter)
	teatest.WaitFor(t, tm.Output(), func(out []byte) bool { return bytes.Contains(out, []byte("Open command palette")) },
		teatest.WithDuration(3*time.Second))

	if err := tm.Quit(); err != nil {
		t.Fatalf("Quit() error = %v", err)
	}

	final := tm.FinalModel(t, teatest.WithFinalTimeout(3*time.Second)).(*App)
	if got := final.store.State().Theme; got != "catppuccin-latte" {
		t.Errorf("theme = %q, want catppuccin-latte", got)
	}

	if got, want := final.View().BackgroundColor, mustTheme(t, "catppuccin-latte").UI.Bg; got != want {
		t.Errorf("background = %v, want %v", got, want)
	}
}

// TestShellFlow drives real /bin/sh panes: start, type, split, close, quit.
func TestShellFlow(t *testing.T) {
	// A login shell reads $HOME/.profile; keep it away from the user's.
	t.Setenv("HOME", t.TempDir())

	o := Options{
		Logger: testLogger(),
		Store:  core.NewStore(core.InitialState(core.Config{Theme: "catppuccin-mocha"})),
		Getenv: shellGetenv,
		Cwd:    t.TempDir(),
		Icons:  true,
	}
	gitOf(newFakeGit(t))(&o)

	o.caches = testDrawCaches

	app, err := New(o)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	// A copy must never reach the operator's real clipboard tools.
	app.clip.run = (&fakeRun{}).run
	app.openLink = (&fakeOpen{}).open

	var mu sync.Mutex

	var pids []int

	app.start = func(ctx context.Context, c term.Command, cols, rows int) (proc, error) {
		p, err := term.Start(ctx, c, cols, rows)
		if err != nil {
			return nil, err
		}

		mu.Lock()

		pids = append(pids, p.Pid())
		mu.Unlock()

		return p, nil
	}

	tm := teatest.NewTestModel(t, app, teatest.WithInitialTermSize(100, 30))
	waitFor := func(want string) {
		t.Helper()
		teatest.WaitFor(t, tm.Output(), func(out []byte) bool { return bytes.Contains(out, []byte(want)) },
			teatest.WithDuration(5*time.Second))
	}

	// The quotes keep the typed line from containing the output.
	for _, r := range "echo multi''plex" {
		tm.Send(keyRune(r))
	}

	tm.Send(keyEnter)
	waitFor("multiplex")

	tm.Send(keyLeader)
	tm.Send(keyRune('v'))
	waitFor("╮╭")

	tm.Send(keyLeader)
	tm.Send(keyRune('x'))
	tm.Send(keyLeader)
	tm.Send(keyRune('q'))
	tm.WaitFinished(t, teatest.WithFinalTimeout(3*time.Second))

	final := tm.FinalModel(t, teatest.WithFinalTimeout(3*time.Second)).(*App)
	if got := len(final.store.State().Panes); got != 1 {
		t.Errorf("panes after split and close = %d, want 1", got)
	}

	closeApp(t, final)

	mu.Lock()
	defer mu.Unlock()

	if len(pids) != 2 {
		t.Fatalf("started %d processes, want 2", len(pids))
	}

	for _, pid := range pids {
		eventually(t, fmt.Sprintf("process group %d to be gone after Close", pid), func() bool { return gone(pid) })
	}
}

// TestTabFlow drives real /bin/sh panes through tabs: new, type, cycle, close, quit.
func TestTabFlow(t *testing.T) {
	// A login shell reads $HOME/.profile; keep it away from the user's.
	t.Setenv("HOME", t.TempDir())

	o := Options{
		Logger: testLogger(),
		Store:  core.NewStore(core.InitialState(core.Config{Theme: "catppuccin-mocha"})),
		Getenv: shellGetenv,
		Cwd:    t.TempDir(),
		Icons:  true,
	}
	gitOf(newFakeGit(t))(&o)

	o.caches = testDrawCaches

	app, err := New(o)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	// A copy must never reach the operator's real clipboard tools.
	app.clip.run = (&fakeRun{}).run
	app.openLink = (&fakeOpen{}).open

	var mu sync.Mutex

	var pids []int

	app.start = func(ctx context.Context, c term.Command, cols, rows int) (proc, error) {
		p, err := term.Start(ctx, c, cols, rows)
		if err != nil {
			return nil, err
		}

		mu.Lock()

		pids = append(pids, p.Pid())
		mu.Unlock()

		return p, nil
	}

	tm := teatest.NewTestModel(t, app, teatest.WithInitialTermSize(100, 30))
	waitFor := func(want string) {
		t.Helper()
		teatest.WaitFor(t, tm.Output(), func(out []byte) bool { return bytes.Contains(out, []byte(want)) },
			teatest.WithDuration(5*time.Second))
	}

	tm.Send(keyLeader)
	tm.Send(keyRune('c'))
	waitFor(" 2 ")

	// The quotes keep the typed line from containing the output.
	for _, r := range "echo tw''o" {
		tm.Send(keyRune(r))
	}

	tm.Send(keyEnter)
	waitFor("two")

	tm.Send(keyLeader)
	tm.Send(keyShiftTab)
	tm.Send(keyLeader)
	tm.Send(keyTab)
	tm.Send(keyLeader)
	tm.Send(keyRune('X'))
	tm.Send(keyLeader)
	tm.Send(keyRune('q'))
	tm.WaitFinished(t, teatest.WithFinalTimeout(3*time.Second))

	final := tm.FinalModel(t, teatest.WithFinalTimeout(3*time.Second)).(*App)

	if s := final.store.State(); len(s.Tabs) != 1 || len(s.Panes) != 1 || s.Tab().ID != "t1" {
		t.Errorf("tabs = %d, panes = %d after closing the second tab, want 1 and 1", len(s.Tabs), len(s.Panes))
	}

	closeApp(t, final)

	mu.Lock()
	defer mu.Unlock()

	if len(pids) != 2 {
		t.Fatalf("started %d processes, want 2", len(pids))
	}

	for _, pid := range pids {
		eventually(t, fmt.Sprintf("process group %d to be gone after Close", pid), func() bool { return gone(pid) })
	}
}
