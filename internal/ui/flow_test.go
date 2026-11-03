package ui

import (
	"bytes"
	"context"
	"fmt"
	"sync"
	"testing"

	"github.com/charmbracelet/x/exp/teatest/v2"

	"github.com/mhersson/aim/internal/core"
	"github.com/mhersson/aim/internal/term"
)

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
			teatest.WithDuration(hangGuard))
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
	tm.WaitFinished(t, teatest.WithFinalTimeout(hangGuard))

	final := tm.FinalModel(t, teatest.WithFinalTimeout(hangGuard)).(*App)
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
			teatest.WithDuration(hangGuard))
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
	tm.WaitFinished(t, teatest.WithFinalTimeout(hangGuard))

	final := tm.FinalModel(t, teatest.WithFinalTimeout(hangGuard)).(*App)

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
