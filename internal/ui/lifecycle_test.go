package ui

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"runtime"
	"slices"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/mhersson/aim/internal/core"
	"github.com/mhersson/aim/internal/term"
)

const testCwd = "/work"

func testGetenv(k string) string {
	if k == "SHELL" {
		return "/bin/testsh"
	}

	return ""
}

func shellGetenv(k string) string {
	if k == "SHELL" {
		return "/bin/sh"
	}

	return ""
}

// starts is a fake term.Start that records every request.
type starts struct {
	mu    sync.Mutex
	cmds  []term.Command
	sizes [][2]int
	procs []*fakeProc
	err   error
}

func (s *starts) start(_ context.Context, c term.Command, cols, rows int) (proc, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.err != nil {
		return nil, s.err
	}

	fp := newFakeProc()

	s.cmds = append(s.cmds, c)
	s.sizes = append(s.sizes, [2]int{cols, rows})
	s.procs = append(s.procs, fp)

	return fp, nil
}

func (s *starts) count() int {
	s.mu.Lock()
	defer s.mu.Unlock()

	return len(s.cmds)
}

func (s *starts) proc(i int) *fakeProc {
	s.mu.Lock()
	defer s.mu.Unlock()

	return s.procs[i]
}

func closeApp(t *testing.T, a *App) {
	t.Helper()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := a.Close(ctx); err != nil {
		t.Errorf("Close() error = %v", err)
	}
}

// lifeApp is an App that has not started yet and starts fake processes.
func lifeApp(t *testing.T) (*App, *starts) {
	t.Helper()

	return lifeAppWith(t, func(o *Options) { o.Git = newFakeGit(t) })
}

// lifeAppWith is lifeApp with its options tuned by opts.
func lifeAppWith(t *testing.T, opts ...func(*Options)) (*App, *starts) {
	t.Helper()

	o := Options{
		Logger: testLogger(),
		Store:  core.NewStore(core.InitialState(core.Config{Theme: "catppuccin-mocha"})),
		Getenv: testGetenv,
		Cwd:    testCwd,
		Icons:  true,
	}

	for _, f := range opts {
		f(&o)
	}

	o.caches = testDrawCaches

	a, err := New(o)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	// A copy must never reach the operator's real clipboard tools.
	a.clip.run = (&fakeRun{}).run
	a.openLink = (&fakeOpen{}).open
	a.tick = (&ticked{}).tick

	st := &starts{}
	a.start = st.start

	t.Cleanup(func() { closeApp(t, a) })

	return a, st
}

// lifeAppSized is lifeApp after the first size message opened the shell.
func lifeAppSized(t *testing.T, w, h int) (*App, *starts) {
	t.Helper()

	a, st := lifeApp(t)
	sized(t, a, w, h)

	return a, st
}

// chord presses the leader and then the key. The new agent request's repository
// check is answered at once, so the pane has opened when chord returns.
func chord(a *App, key rune) tea.Cmd {
	cmd := press(a, keyLeader, keyRune(key))
	if key != 'n' || cmd == nil {
		return cmd
	}

	_, cmd = a.Update(cmd())

	return cmd
}

func isQuit(cmd tea.Cmd) bool {
	if cmd == nil {
		return false
	}

	_, ok := cmd().(tea.QuitMsg)

	return ok
}

// collect runs the commands of cmd (a batch or one command) and returns the
// messages that arrive within a short wait; blocking pane waits are left running.
func collect(cmd tea.Cmd) []tea.Msg {
	if cmd == nil {
		return nil
	}

	first := cmd()

	cmds, ok := first.(tea.BatchMsg)
	if !ok {
		return []tea.Msg{first}
	}

	out := make(chan tea.Msg, len(cmds))

	for _, c := range cmds {
		go func() { out <- c() }()
	}

	var msgs []tea.Msg

	timeout := time.After(300 * time.Millisecond)

	for range cmds {
		select {
		case m := <-out:
			msgs = append(msgs, m)
		case <-timeout:
			return msgs
		}
	}

	return msgs
}

func TestStartupOpensShell(t *testing.T) {
	t.Parallel()
	a, st := lifeApp(t)

	sized(t, a, 100, 30)

	if st.count() != 1 {
		t.Fatalf("starts = %d, want 1", st.count())
	}

	c := st.cmds[0]
	want := term.ShellCommand(testGetenv, term.Integration{}, testCwd, nil)

	if !slices.Equal(c.Argv, want.Argv) || c.Dir != testCwd {
		t.Errorf("command = %v in %q, want %v in %q", c.Argv, c.Dir, want.Argv, testCwd)
	}

	if st.sizes[0] != [2]int{98, 26} {
		t.Errorf("start size = %v, want [98 26]", st.sizes[0])
	}

	s := a.store.State()
	if len(s.Panes) != 1 || s.Panes["p1"].Kind != core.KindShell || s.Panes["p1"].Cwd != testCwd || s.Tab().Focus != "p1" {
		t.Errorf("state = %+v, want one focused shell pane p1 in %s", s, testCwd)
	}

	if a.panes["p1"] == nil {
		t.Fatal("pane p1 is not in the registry")
	}

	sized(t, a, 120, 40)

	if st.count() != 1 {
		t.Errorf("second size message started a process (starts = %d)", st.count())
	}

	if got := st.proc(0).resized(); len(got) == 0 || got[len(got)-1] != [2]int{118, 36} {
		t.Errorf("resizes = %v, want the last to be [118 36]", got)
	}
}

func TestStartupSizeBeforeFirstMessage(t *testing.T) {
	t.Parallel()
	a, st := lifeApp(t)

	for _, k := range []rune{'v', 's', 'n'} {
		chord(a, k)
	}

	if st.count() != 0 || a.started || len(a.store.State().Panes) != 0 {
		t.Errorf("a pane opened before the first size (starts = %d)", st.count())
	}

	sized(t, a, 100, 30)

	if st.count() != 1 {
		t.Errorf("starts after the first size = %d, want the one startup shell", st.count())
	}
}

func TestStartFailure(t *testing.T) {
	t.Parallel()
	a, st := lifeApp(t)
	st.err = errors.New("no such shell")

	sized(t, a, 100, 30)

	if got := len(a.store.State().Panes); got != 0 || len(a.panes) != 0 {
		t.Errorf("failed start left %d panes in state and %d in the registry", got, len(a.panes))
	}

	if !strings.Contains(a.notice, "no such shell") {
		t.Errorf("notice = %q, want the start error", a.notice)
	}

	st.err = nil

	chord(a, 'v')

	if got := len(a.store.State().Panes); got != 1 {
		t.Errorf("panes after a later successful split = %d, want 1", got)
	}
}

func TestSplitCommands(t *testing.T) {
	t.Parallel()

	tests := []struct {
		key   rune
		split core.Split
		kind  core.PaneKind
	}{
		{'s', core.SplitHorizontal, core.KindShell},
		{'v', core.SplitVertical, core.KindShell},
		{'n', core.SplitVertical, core.KindAgent},
	}

	for _, tt := range tests {
		t.Run(string(tt.key), func(t *testing.T) {
			a, st := lifeAppSized(t, 100, 30)

			if err := a.dispatch(core.SetPaneCwd{ID: "p1", Cwd: "/elsewhere"}); err != nil {
				t.Fatal(err)
			}

			chord(a, tt.key)

			s := a.store.State()
			if s.Tab().Layout == nil || s.Tab().Layout.Split != tt.split || s.Tab().Layout.A.Pane != "p1" || s.Tab().Layout.B.Pane != "p2" {
				t.Fatalf("layout = %+v, want a %s split of p1 and p2", s.Tab().Layout, tt.split)
			}

			if s.Tab().Focus != "p2" || s.Panes["p2"].Kind != tt.kind || s.Panes["p2"].Cwd != "/elsewhere" {
				t.Errorf("state = %+v, want p2 (%s) focused in /elsewhere", s, tt.kind)
			}

			want := term.ShellCommand(testGetenv, term.Integration{}, "/elsewhere", nil)
			if tt.kind == core.KindAgent {
				want = term.AgentCommand(testGetenv, term.Integration{}, "/elsewhere", []string{"claude"}, nil)
			}

			if got := st.cmds[1]; !slices.Equal(got.Argv, want.Argv) || got.Dir != "/elsewhere" {
				t.Errorf("command = %v in %q, want %v in /elsewhere", got.Argv, got.Dir, want.Argv)
			}

			assertSizes(t, a)

			if got := st.sizes[1]; got != paneSize(t, a, "p2") {
				t.Errorf("start size = %v, want the content rect %v", got, paneSize(t, a, "p2"))
			}
		})
	}
}

// paneSize is the content rect of a pane as {cols, rows}.
func paneSize(t *testing.T, a *App, id core.PaneID) [2]int {
	t.Helper()

	r, ok := a.layoutRects()[id]
	if !ok {
		t.Fatalf("pane %s has no rect", id)
	}

	c := contentRect(r)

	return [2]int{max(c.W, 1), max(c.H, 1)}
}

// assertSizes checks that every pane with a rect has its content size and
// every pane without one is 1x1, unless zoom hides it.
func assertSizes(t *testing.T, a *App) {
	t.Helper()

	st := a.store.State()
	rects := a.layoutRects()

	for id, p := range a.panes {
		cols, rows := p.size()
		want := [2]int{1, 1}

		if r, ok := rects[id]; ok {
			c := contentRect(r)
			want = [2]int{max(c.W, 1), max(c.H, 1)}
		} else if st.Tab().Zoomed {
			continue
		}

		if got := [2]int{cols, rows}; got != want {
			t.Errorf("pane %s size = %v, want %v", id, got, want)
		}
	}
}

// Each PTY is its pane's rect minus the frame: two panes side by side in a
// 100x30 terminal are 50x28 rects, so 48x26 PTYs; scrolling's hidden column
// is sized as if shown alone.
func TestPaneSizesAreRectsMinusFrame(t *testing.T) {
	t.Parallel()
	a, _ := lifeAppSized(t, 100, 30)

	chord(a, 'v')

	for id, r := range a.layoutRects() {
		if r.W != 50 || r.H != 28 {
			t.Fatalf("%s rect = %+v, want 50x28", id, r)
		}

		if cols, rows := paneSizeOf(a, id); cols != 48 || rows != 26 {
			t.Errorf("%s PTY = %dx%d, want 48x26", id, cols, rows)
		}
	}

	chord(a, 'v')

	if err := a.dispatch(core.SetLayoutMode{Mode: core.ModeScrolling, Area: a.bodyArea()}); err != nil {
		t.Fatal(err)
	}

	run(a, a.relayout())

	if _, drawn := a.layoutRects()["p1"]; drawn {
		t.Fatal("p1 should be hidden")
	}

	for _, id := range []core.PaneID{"p1", "p2", "p3"} {
		if cols, rows := paneSizeOf(a, id); cols != 48 || rows != 26 {
			t.Errorf("%s PTY = %dx%d, want 48x26", id, cols, rows)
		}
	}
}

func TestFocusResizeZoom(t *testing.T) {
	t.Parallel()
	// 30 rows give an even body, so p2 and p3 overlap p1 equally.
	a, st := lifeAppSized(t, 100, 30)

	chord(a, 'v')
	chord(a, 's')

	// p1 | (p2 over p3), p3 focused.
	steps := []struct {
		name  string
		key   rune
		check func(s core.State) bool
	}{
		{"focus up", 'k', func(s core.State) bool { return s.Tab().Focus == "p2" }},
		{"focus down", 'j', func(s core.State) bool { return s.Tab().Focus == "p3" }},
		{"focus left", 'h', func(s core.State) bool { return s.Tab().Focus == "p1" }},
		{"focus right", 'l', func(s core.State) bool { return s.Tab().Focus == "p2" }},
		{"focus down again", 'j', func(s core.State) bool { return s.Tab().Focus == "p3" }},
		{"shrink up", 'K', func(s core.State) bool { return s.Tab().Layout.B.Ratio == 0.45 }},
		{"grow down", 'J', func(s core.State) bool { return s.Tab().Layout.B.Ratio == 0.5 }},
		{"shrink left", 'H', func(s core.State) bool { return s.Tab().Layout.Ratio == 0.45 }},
		{"grow right", 'L', func(s core.State) bool { return s.Tab().Layout.Ratio == 0.5 }},
		{"zoom", 'z', func(s core.State) bool { return s.Tab().Zoomed }},
		{"unzoom", 'z', func(s core.State) bool { return !s.Tab().Zoomed }},
	}

	for _, step := range steps {
		chord(a, step.key)

		if !step.check(a.store.State()) {
			t.Fatalf("%s: state after %c = %+v", step.name, step.key, a.store.State())
		}

		assertSizes(t, a)
	}

	// Resizes reach the process: p1 shrank to 43 columns and grew back to 48.
	var sawNarrow bool

	for _, r := range st.proc(0).resized() {
		if r[0] == 43 {
			sawNarrow = true
		}
	}

	if !sawNarrow {
		t.Errorf("p1 resizes = %v, want one with 43 columns", st.proc(0).resized())
	}
}

func TestZoomedPanesKeepTheirSize(t *testing.T) {
	t.Parallel()
	a, st := lifeAppSized(t, 100, 30)

	chord(a, 'v')

	before := len(st.proc(0).resized())

	chord(a, 'z')

	if got := paneSize(t, a, "p2"); got != [2]int{98, 26} {
		t.Errorf("zoomed pane size = %v, want [98 26]", got)
	}

	if got := len(st.proc(0).resized()); got != before {
		t.Errorf("hidden pane p1 was resized while zoomed (%d resizes, had %d)", got, before)
	}

	chord(a, 'z')
	assertSizes(t, a)
}

func TestCloseShellAndAgent(t *testing.T) {
	t.Parallel()
	t.Run("shell closes at once", func(t *testing.T) {
		a, st := lifeAppSized(t, 100, 30)
		chord(a, 'v')
		chord(a, 'x')

		s := a.store.State()
		if len(s.Panes) != 1 || s.Tab().Focus != "p1" || a.confirm != nil || a.panes["p2"] != nil {
			t.Fatalf("state = %+v, confirm = %v, want only p1 and no confirm", s, a.confirm)
		}

		eventually(t, "p2 process closed", func() bool { return isClosed(st.proc(1)) })
		assertSizes(t, a)
	})

	t.Run("agent asks", func(t *testing.T) {
		tests := []struct {
			name      string
			keys      []tea.KeyPressMsg
			wantPanes int
		}{
			{"n keeps", []tea.KeyPressMsg{keyRune('n')}, 2},
			{"esc keeps", []tea.KeyPressMsg{keyEsc}, 2},
			{"y closes", []tea.KeyPressMsg{keyRune('y')}, 1},
			{"enter closes", []tea.KeyPressMsg{keyEnter}, 1},
			{"other keys are swallowed", []tea.KeyPressMsg{keyRune('q'), keyLeader, keyRune('a')}, 2},
		}

		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				a, st := lifeAppSized(t, 100, 30)
				chord(a, 'n')
				chord(a, 'x')

				if a.confirm == nil {
					t.Fatal("no confirm for an agent pane")
				}

				if got := len(a.store.State().Panes); got != 2 {
					t.Fatalf("panes before the answer = %d, want 2", got)
				}

				if v := ansi.Strip(a.View().Content); !strings.Contains(v, "[y] Close pane") {
					t.Errorf("view lacks the confirm prompt:\n%s", v)
				}

				var cmd tea.Cmd
				for _, k := range tt.keys {
					cmd = press(a, k)
				}

				if isQuit(cmd) {
					t.Error("a key reached the app behind the confirm")
				}

				if got := len(a.store.State().Panes); got != tt.wantPanes {
					t.Errorf("panes = %d, want %d", got, tt.wantPanes)
				}

				open := tt.wantPanes == 2 && tt.name == "other keys are swallowed"
				if (a.confirm != nil) != open {
					t.Errorf("confirm open = %v, want %v", a.confirm != nil, open)
				}

				if got := st.proc(1).sent(); got != "" {
					t.Errorf("agent pane received %q behind the confirm", got)
				}

				if tt.wantPanes == 1 {
					eventually(t, "agent process closed", func() bool { return isClosed(st.proc(1)) })
				}
			})
		}
	})
}

func isClosed(f *fakeProc) bool {
	f.mu.Lock()
	defer f.mu.Unlock()

	return f.closed
}

func TestQuitConfirm(t *testing.T) {
	t.Parallel()
	t.Run("shells quit at once", func(t *testing.T) {
		a, _ := lifeAppSized(t, 100, 30)
		chord(a, 'v')

		if !isQuit(chord(a, 'q')) {
			t.Error("quit with only shells did not quit")
		}
	})

	t.Run("agents ask first", func(t *testing.T) {
		a, _ := lifeAppSized(t, 100, 30)
		chord(a, 'n')

		if cmd := chord(a, 'q'); cmd != nil || a.confirm == nil {
			t.Fatalf("quit with an agent: cmd = %v, confirm = %v, want a confirm", cmd, a.confirm)
		}

		if cmd := press(a, keyRune('n')); isQuit(cmd) || a.confirm != nil {
			t.Fatal("n quit or kept the confirm")
		}

		chord(a, 'q')

		if !isQuit(press(a, keyRune('y'))) {
			t.Error("y did not quit")
		}
	})
}

// exitOf makes the pane's process end and returns the exit message the pane reports.
func exitOf(t *testing.T, a *App, id core.PaneID, fp *fakeProc) tea.Msg {
	t.Helper()

	_ = fp.feed.Close()

	for {
		m := next(t, a.panes[id])
		if _, ok := m.(paneExitedMsg); ok {
			return m
		}

		a.Update(m)
	}
}

func TestPaneExit(t *testing.T) {
	t.Parallel()
	t.Run("during resizes", func(t *testing.T) {
		a, st := lifeAppSized(t, 100, 30)
		chord(a, 'v')
		chord(a, 'v')

		fp := st.proc(1)
		done := make(chan struct{})

		go func() {
			defer close(done)

			_ = fp.feed.Close()
		}()

		for i := range 50 {
			sized(t, a, 100+i%7, 30+i%5)
		}

		<-done

		msg := exitOf(t, a, "p2", fp)
		a.Update(msg)
		sized(t, a, 90, 25)

		s := a.store.State()
		if len(s.Panes) != 2 || s.Panes["p2"].Kind != "" || a.panes["p2"] != nil {
			t.Fatalf("panes after the exit = %+v", s.Panes)
		}

		if s.Tab().Focus == "p2" {
			t.Error("focus stayed on the exited pane")
		}

		assertSizes(t, a)
		eventually(t, "exited pane's process closed", func() bool { return isClosed(fp) })
	})

	t.Run("with its confirm open", func(t *testing.T) {
		a, st := lifeAppSized(t, 100, 30)
		chord(a, 'n')
		chord(a, 'x')

		if a.confirm == nil {
			t.Fatal("no confirm")
		}

		a.Update(exitOf(t, a, "p2", st.proc(1)))

		if a.confirm != nil {
			t.Error("the confirm for the exited pane stayed open")
		}

		if s := a.store.State(); len(s.Panes) != 1 || s.Tab().Focus != "p1" {
			t.Errorf("state = %+v, want only p1 focused", s)
		}

		// Late answers are harmless.
		press(a, keyRune('y'))
		a.Update(paneExitedMsg{id: "p2", p: a.panes["p2"]})
		a.Update(paneDirtyMsg{id: "p2", p: a.panes["p2"]})
		a.Update(holdExpiredMsg{id: "p2", p: a.panes["p2"]})
	})

	t.Run("quit confirm survives an agent exit", func(t *testing.T) {
		a, st := lifeAppSized(t, 100, 30)
		chord(a, 'n')
		chord(a, 'q')

		a.Update(exitOf(t, a, "p2", st.proc(1)))

		if a.confirm == nil {
			t.Error("the quit confirm closed with an unrelated pane")
		}
	})

	t.Run("last pane shows the welcome card", func(t *testing.T) {
		a, st := lifeAppSized(t, 100, 30)

		a.Update(exitOf(t, a, "p1", st.proc(0)))

		if s := a.store.State(); !s.Tab().Empty() || len(s.Panes) != 0 {
			t.Fatalf("state = %+v, want no panes", s)
		}

		if v := ansi.Strip(a.View().Content); !strings.Contains(v, "No panes yet.") {
			t.Errorf("view lacks the welcome card:\n%s", v)
		}

		sized(t, a, 100, 30)

		if st.count() != 1 {
			t.Errorf("a size message after the last pane closed started a process (starts = %d)", st.count())
		}

		chord(a, 'v')

		if st.count() != 2 || len(a.store.State().Panes) != 1 {
			t.Errorf("a split from the empty layout: starts = %d, panes = %d", st.count(), len(a.store.State().Panes))
		}
	})
}

func TestMessagesForUnknownPane(t *testing.T) {
	t.Parallel()
	a, _ := lifeAppSized(t, 100, 30)

	for _, m := range []tea.Msg{
		paneDirtyMsg{id: "p9", p: a.panes["p9"]},
		holdExpiredMsg{id: "p9"},
		paneExitedMsg{id: "p9"},
		paneCwdMsg{id: "p9", p: a.panes["p9"], cwd: "/x"},
		paneCwdDueMsg{id: "p9"},
	} {
		if _, cmd := a.Update(m); cmd != nil {
			t.Errorf("%T for an unknown pane returned a command", m)
		}
	}
}

func TestPaneMessagesRearmWait(t *testing.T) {
	t.Parallel()
	a, st := lifeAppSized(t, 100, 30)

	if _, err := st.proc(0).feed.Write([]byte("hello")); err != nil {
		t.Fatal(err)
	}

	m := next(t, a.panes["p1"])
	if _, ok := m.(paneDirtyMsg); !ok {
		t.Fatalf("message = %T, want paneDirtyMsg", m)
	}

	_, cmd := a.Update(m)
	if cmd == nil {
		t.Fatal("a dirty message returned no command, so the pane would never be waited on again")
	}

	if !strings.Contains(ansi.Strip(a.View().Content), "hello") {
		t.Errorf("the dirty message did not render the pane:\n%s", ansi.Strip(a.View().Content))
	}
}

func TestCwdTracking(t *testing.T) {
	t.Parallel()
	a, st := lifeAppSized(t, 100, 30)
	clk := &fakeClock{t: t0}
	a.clock = clk
	rec := &ticked{}
	a.tick = rec.tick
	st.proc(0).cwd = "/moved"

	p := a.panes["p1"]

	cmd := a.probeCwd(p)
	if cmd == nil {
		t.Fatal("first probe returned no command")
	}

	msg := cmd()
	if m, ok := msg.(paneCwdMsg); !ok || m.id != "p1" || m.cwd != "/moved" {
		t.Fatalf("probe message = %#v, want paneCwdMsg for p1 in /moved", msg)
	}

	// A probe is in flight: no second one.
	if cmd := a.probeCwd(p); cmd != nil {
		t.Error("a probe started while another is in flight")
	}

	a.Update(msg)

	if got := a.store.State().Panes["p1"].Cwd; got != "/moved" {
		t.Errorf("cwd = %q, want /moved", got)
	}

	// Within 500 ms the check waits for the window to end, once.
	clk.Advance(100 * time.Millisecond)

	cmd = a.probeCwd(p)
	if cmd == nil {
		t.Fatal("throttled probe scheduled no trailing check")
	}

	if want := []time.Duration{400 * time.Millisecond}; !slices.Equal(rec.delays, want) {
		t.Errorf("trailing check delays = %v, want %v", rec.delays, want)
	}

	if m, ok := rec.msgs[0].(paneCwdDueMsg); !ok || m.id != "p1" {
		t.Errorf("trailing check message = %#v, want paneCwdDueMsg for p1", rec.msgs[0])
	}

	if a.probeCwd(p) != nil {
		t.Error("a second trailing check was scheduled")
	}

	clk.Advance(400 * time.Millisecond)

	_, cmd = a.Update(paneCwdDueMsg{id: "p1", p: a.panes["p1"]})
	if cmd == nil {
		t.Fatal("the trailing check did not probe")
	}

	st.proc(0).cwd = "/again"

	a.Update(cmd())

	if got := a.store.State().Panes["p1"].Cwd; got != "/again" {
		t.Errorf("cwd = %q, want /again", got)
	}

	// An empty answer keeps the old value.
	a.Update(paneCwdMsg{id: "p1", p: a.panes["p1"], cwd: ""})

	if got := a.store.State().Panes["p1"].Cwd; got != "/again" {
		t.Errorf("cwd after an empty answer = %q, want /again", got)
	}
}

func TestDirtyMessageSchedulesCwdProbe(t *testing.T) {
	t.Parallel()
	a, st := lifeAppSized(t, 100, 30)
	st.proc(0).cwd = "/moved"

	_, cmd := a.Update(paneDirtyMsg{id: "p1", p: a.panes["p1"]})

	var got bool

	for _, m := range collect(cmd) {
		if c, ok := m.(paneCwdMsg); ok && c.cwd == "/moved" {
			got = true
		}
	}

	if !got {
		t.Error("a dirty message did not probe the pane's cwd")
	}
}

func TestTinyTerminals(t *testing.T) {
	t.Parallel()
	a, st := lifeAppSized(t, 100, 30)

	for _, k := range "vsvsv" {
		chord(a, k)
	}

	if got := len(a.panes); got != 6 {
		t.Fatalf("panes = %d, want 6", got)
	}

	for _, sz := range [][2]int{{20, 5}, {1, 1}, {2, 2}, {3, 1}, {100, 30}, {20, 5}} {
		sized(t, a, sz[0], sz[1])

		lines := strings.Split(a.View().Content, "\n")
		if len(lines) != sz[1] {
			t.Errorf("%dx%d: %d lines, want %d", sz[0], sz[1], len(lines), sz[1])
		}

		for i, l := range lines {
			if w := ansi.StringWidth(l); w != sz[0] {
				t.Errorf("%dx%d: line %d is %d cells wide", sz[0], sz[1], i, w)
			}
		}

		assertSizes(t, a)
	}

	for i := range st.count() {
		for _, r := range st.proc(i).resized() {
			if r[0] < 1 || r[1] < 1 {
				t.Errorf("process %d got resize %v", i, r)
			}
		}

		if s := st.sizes[i]; s[0] < 1 || s[1] < 1 {
			t.Errorf("process %d started at %v", i, s)
		}
	}
}

// realApp is an App that starts real processes running the given /bin/sh -c
// scripts in order (the last one repeats); it records every pid.
type realApp struct {
	*App
	mu   sync.Mutex
	pids []int
	n    int
}

func newRealApp(t *testing.T, scripts ...string) *realApp {
	t.Helper()

	return newScriptApp(t, func(o *Options) { o.Git = newFakeGit(t) }, scripts...)
}

// newScriptApp is newRealApp with git set by gitOpt.
func newScriptApp(t *testing.T, gitOpt func(*Options), scripts ...string) *realApp {
	t.Helper()

	o := Options{
		Logger: testLogger(),
		Store:  core.NewStore(core.InitialState(core.Config{Theme: "catppuccin-mocha"})),
		Getenv: shellGetenv,
		Cwd:    t.TempDir(),
	}
	gitOpt(&o)

	o.caches = testDrawCaches

	a, err := New(o)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	// A copy must never reach the operator's real clipboard tools.
	a.clip.run = (&fakeRun{}).run
	a.openLink = (&fakeOpen{}).open
	a.tick = cancellableTick(a.ctx)

	r := &realApp{App: a}

	t.Cleanup(func() { closeApp(t, a) })

	a.start = func(ctx context.Context, c term.Command, cols, rows int) (proc, error) {
		r.mu.Lock()
		script := scripts[min(r.n, len(scripts)-1)]
		r.n++
		r.mu.Unlock()

		c.Argv = []string{"/bin/sh", "-c", script}

		p, err := term.Start(ctx, c, cols, rows)
		if err != nil {
			return nil, err
		}

		r.mu.Lock()
		r.pids = append(r.pids, p.Pid())
		r.mu.Unlock()

		return p, nil
	}

	return r
}

func (r *realApp) pidList() []int {
	r.mu.Lock()
	defer r.mu.Unlock()

	return slices.Clone(r.pids)
}

// gone reports whether nothing is left in the process group of pid. Wait for it
// after Close: kill(2) still finds killed members until they are reaped, an
// orphan by init and, on macOS, the shadow proc an exec leaves with the
// leader's pid and group.
func gone(pid int) bool {
	return errors.Is(syscall.Kill(-pid, 0), syscall.ESRCH) && errors.Is(syscall.Kill(pid, 0), syscall.ESRCH)
}

func TestAppCloseNoLeaks(t *testing.T) {
	base := runtime.NumGoroutine()
	// The trailing ":" stops the shell from exec'ing sleep, so each group has a
	// member besides its leader.
	r := newRealApp(t, "sleep 30; :")

	sized(t, r.App, 100, 30)
	chord(r.App, 'v')
	chord(r.App, 's')

	pids := r.pidList()
	if len(pids) != 3 {
		t.Fatalf("started %d processes, want 3", len(pids))
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := r.Close(ctx); err != nil {
		t.Fatalf("Close() error = %v", err)
	}

	// Close reaps only each leader; the other killed members of its group stay
	// zombies until init reaps them.
	for _, pid := range pids {
		eventually(t, fmt.Sprintf("process group %d to be gone after Close", pid), func() bool { return gone(pid) })
	}

	if err := r.Close(ctx); err != nil {
		t.Errorf("second Close() error = %v", err)
	}

	eventually2(t, 2*time.Second, "goroutines to return to baseline", func() bool { return runtime.NumGoroutine() <= base })
}

// eventually2 is eventually with its own limit.
func eventually2(t *testing.T, limit time.Duration, what string, cond func() bool) {
	t.Helper()

	deadline := time.Now().Add(limit)

	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s (goroutines: %d)", what, runtime.NumGoroutine())
		}

		time.Sleep(5 * time.Millisecond)
	}
}

// cancellableTick is tea.Tick that also returns when ctx ends. Bubble Tea
// cannot cancel a tick, so one a pump runs would hold its goroutine for the
// whole delay after the app closed.
func cancellableTick(ctx context.Context) func(time.Duration, func(time.Time) tea.Msg) tea.Cmd {
	return func(d time.Duration, fn func(time.Time) tea.Msg) tea.Cmd {
		t := time.NewTimer(d)

		return func() tea.Msg {
			defer t.Stop()

			select {
			case ts := <-t.C:
				return fn(ts)
			case <-ctx.Done():
				return nil
			}
		}
	}
}

func TestCancellableTick(t *testing.T) {
	t.Parallel()

	type tickMsg struct{}

	tests := []struct {
		name   string
		delay  time.Duration
		cancel bool
		want   tea.Msg
	}{
		{"fires after the delay", time.Millisecond, false, tickMsg{}},
		{"gives up when the context ends", time.Hour, true, nil},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			t.Cleanup(cancel)

			cmd := cancellableTick(ctx)(tt.delay, func(time.Time) tea.Msg { return tickMsg{} })

			if tt.cancel {
				cancel()
			}

			got := make(chan tea.Msg, 1)

			go func() { got <- cmd() }()

			select {
			case m := <-got:
				if m != tt.want {
					t.Errorf("tick returned %v, want %v", m, tt.want)
				}
			case <-time.After(5 * time.Second):
				t.Fatal("the tick did not return")
			}
		})
	}
}

// pump stands in for the Bubble Tea runtime: it runs commands off the update
// loop and queues their messages for the test to feed to Update.
type pump struct {
	msgs chan tea.Msg
	done chan struct{}
	stop sync.Once
	cmds sync.WaitGroup
}

func newPump(t *testing.T) *pump {
	t.Helper()

	p := &pump{msgs: make(chan tea.Msg, 64), done: make(chan struct{})}

	t.Cleanup(p.halt)

	return p
}

// halt stops delivery, so no command waits on a full queue.
func (p *pump) halt() { p.stop.Do(func() { close(p.done) }) }

// stopWith registers the cleanup that closes a, which ends the commands the
// pump ran, and waits for them, so none still writes into a dir t.TempDir is
// removing. kindApp's cleanup closes a again later, which is harmless.
func (p *pump) stopWith(t *testing.T, a *App) {
	t.Helper()

	t.Cleanup(func() {
		p.halt()
		closeApp(t, a)

		ended := make(chan struct{})

		go func() {
			p.cmds.Wait()
			close(ended)
		}()

		select {
		case <-ended:
		case <-time.After(5 * time.Second):
			t.Error("the pump's commands still ran 5s after the app closed")
		}
	})
}

func (p *pump) run(cmd tea.Cmd) {
	if cmd == nil {
		return
	}

	p.cmds.Go(func() {
		m := cmd()
		if b, ok := m.(tea.BatchMsg); ok {
			for _, c := range b {
				p.run(c)
			}

			return
		}

		if m == nil {
			return
		}

		select {
		case p.msgs <- m:
		case <-p.done:
		}
	})
}

// step feeds the next queued message to Update, or reports false once ctx ends.
func (p *pump) step(ctx context.Context, a *App) bool {
	select {
	case m := <-p.msgs:
		_, cmd := a.Update(m)
		p.run(cmd)

		return true
	case <-ctx.Done():
		return false
	}
}

func TestFloodingPaneQuits(t *testing.T) {
	// The sleep ignores SIGHUP, so only Close's last SIGKILL reaches it, just
	// before Close returns.
	r := newScriptApp(t, gitOf(newFakeGit(t)), `(trap "" HUP; exec sleep 30) & while :; do echo xxxxxxxxxxxxxxxx; done`, `cat`)
	pm := newPump(t)

	_, cmd := r.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
	pm.run(cmd)
	pm.run(chord(r.App, 'v'))
	// p2 (cat) is focused; the flood runs in p1.

	pumped, stop := context.WithTimeout(context.Background(), 5*time.Second)
	defer stop()

	floods := 0

	// Let the flood run through Update before the key arrives.
	for floods < 50 {
		if !pm.step(pumped, r.App) {
			t.Fatal("the flood produced no messages")
		}

		floods++
	}

	pm.run(press(r.App, keyRune('z')))

	typed := func() bool {
		return strings.Contains(strings.Join(r.panes["p2"].lines(), "\n"), "z")
	}

	for after := 0; !typed(); after++ {
		if !pm.step(pumped, r.App) {
			t.Fatal("the key did not reach the second pane while the first flooded")
		}

		if after > 100000 {
			t.Fatal("too many messages without the key arriving")
		}
	}

	// The flood is still going.
	for range 20 {
		if !pm.step(pumped, r.App) {
			t.Fatal("the flood stopped")
		}
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := r.Close(ctx); err != nil {
		t.Fatalf("Close() error = %v", err)
	}

	for _, pid := range r.pidList() {
		eventually(t, fmt.Sprintf("process group %d to be gone after Close", pid), func() bool { return gone(pid) })
	}
}

func TestOrphanedExitClosesPane(t *testing.T) {
	r := newRealApp(t, `sleep 30 & exit 0`)

	sized(t, r.App, 100, 30)

	pid := r.pidList()[0]
	p := r.panes["p1"]

	deadline := time.After(2 * time.Second)
	done := make(chan tea.Msg, 1)

	go func() {
		for {
			m := p.wait()()
			if _, ok := m.(paneExitedMsg); ok {
				done <- m

				return
			}
		}
	}()

	select {
	case m := <-done:
		r.Update(m)
	case <-deadline:
		t.Fatal("the pane did not report its exit within 2s")
	}

	if got := len(r.store.State().Panes); got != 0 || len(r.panes) != 0 {
		t.Errorf("panes after the exit: state %d, registry %d", got, len(r.panes))
	}

	eventually2(t, 3*time.Second, "the orphan to be killed", func() bool { return gone(pid) })
}

// Bubble Tea reports shifted leader keys with their shifted character
// whether or not the terminal sends text.
func TestLeaderShiftedKeys(t *testing.T) {
	t.Parallel()

	shapes := []struct {
		name string
		key  tea.KeyPressMsg
	}{
		{"text", tea.KeyPressMsg{Code: 'h', ShiftedCode: 'H', Mod: tea.ModShift, Text: "H"}},
		{"no text", tea.KeyPressMsg{Code: 'h', ShiftedCode: 'H', Mod: tea.ModShift}},
	}

	for _, tt := range shapes {
		t.Run(tt.name, func(t *testing.T) {
			a, _ := lifeAppSized(t, 100, 30)

			chord(a, 'v')
			chord(a, 'h')
			press(a, keyLeader, tt.key)

			if got := a.store.State().Tab().Layout.Ratio; got != 0.45 {
				t.Errorf("ratio after ctrl+space H = %v, want 0.45 (notice %q)", got, a.notice)
			}
		})
	}
}

func TestSplitFallsBackToLaunchDir(t *testing.T) {
	t.Parallel()
	a, st := lifeAppSized(t, 100, 30)

	if err := a.dispatch(core.SetPaneCwd{ID: "p1", Cwd: "/gone"}); err != nil {
		t.Fatal(err)
	}

	a.start = func(ctx context.Context, c term.Command, cols, rows int) (proc, error) {
		if c.Dir == "/gone" {
			return nil, fmt.Errorf("start sh: %w", &fs.PathError{Op: "fork/exec", Path: "/bin/sh", Err: syscall.ENOENT})
		}

		return st.start(ctx, c, cols, rows)
	}

	chord(a, 'v')

	s := a.store.State()
	if len(s.Panes) != 2 {
		t.Fatalf("panes = %d, want 2", len(s.Panes))
	}

	if got := s.Panes[s.Tab().Focus].Cwd; got != testCwd {
		t.Errorf("new pane cwd = %q, want the launch dir %q", got, testCwd)
	}

	if cmds := st.cmds; cmds[len(cmds)-1].Dir != testCwd {
		t.Errorf("started in %q, want %q", cmds[len(cmds)-1].Dir, testCwd)
	}

	if !strings.Contains(a.notice, "/gone") {
		t.Errorf("notice = %q, want it to name /gone", a.notice)
	}
}

func TestSplitOtherStartErrorsAreNotRetried(t *testing.T) {
	t.Parallel()
	a, st := lifeAppSized(t, 100, 30)

	if err := a.dispatch(core.SetPaneCwd{ID: "p1", Cwd: "/gone"}); err != nil {
		t.Fatal(err)
	}

	st.err = errors.New("no ptys")

	chord(a, 'v')

	if len(a.store.State().Panes) != 1 || !strings.Contains(a.notice, "cannot start") {
		t.Errorf("panes = %d, notice = %q", len(a.store.State().Panes), a.notice)
	}
}

func TestSwapCommands(t *testing.T) {
	t.Parallel()

	ctrl := func(r rune) tea.KeyPressMsg { return tea.KeyPressMsg{Code: r, Mod: tea.ModCtrl} }

	t.Run("key strings", func(t *testing.T) {
		for r, want := range map[rune]string{'h': "ctrl+h", 'j': "ctrl+j", 'k': "ctrl+k", 'l': "ctrl+l"} {
			if got := bindingKey(ctrl(r)); got != want {
				t.Errorf("bindingKey(ctrl+%c) = %q, want %q", r, got, want)
			}
		}
	})

	t.Run("swap right", func(t *testing.T) {
		a, st := lifeAppSized(t, 100, 30)
		chord(a, 'v')
		chord(a, 'h')
		chord(a, 'L')
		chord(a, 'L')

		before1, before2 := len(st.proc(0).resized()), len(st.proc(1).resized())

		press(a, keyLeader, ctrl('l'))

		s := a.store.State()
		if l := s.Tab().Layout; l.A.Pane != "p2" || l.B.Pane != "p1" {
			t.Fatalf("layout = %s | %s, want p2 | p1 (notice %q)", l.A.Pane, l.B.Pane, a.notice)
		}

		if s.Tab().Focus != "p1" {
			t.Errorf("Focus = %q, want p1", s.Tab().Focus)
		}

		assertSizes(t, a)

		if got := st.proc(0).resized(); len(got) <= before1 || got[len(got)-1] != paneSize(t, a, "p1") {
			t.Errorf("p1 resizes = %v, want a new one with %v", got, paneSize(t, a, "p1"))
		}

		if got := st.proc(1).resized(); len(got) <= before2 || got[len(got)-1] != paneSize(t, a, "p2") {
			t.Errorf("p2 resizes = %v, want a new one with %v", got, paneSize(t, a, "p2"))
		}

		for i := range 2 {
			if got := st.proc(i).sent(); got != "" {
				t.Errorf("pane %d received %q, want nothing", i, got)
			}
		}
	})

	t.Run("every direction resolves", func(t *testing.T) {
		for r, cmd := range map[rune]string{'h': "pane.swap.left", 'j': "pane.swap.down", 'k': "pane.swap.up", 'l': "pane.swap.right"} {
			a := newTestApp(t)
			if got, ok := a.keymap.lookup(bindingKey(ctrl(r))); !ok || got != cmd {
				t.Errorf("lookup(ctrl+%c) = %q, %v, want %q", r, got, ok, cmd)
			}
		}
	})
}

// A pane the tiny body leaves out has no rect, so focus never steps onto it.
func TestFocusSkipsPanesLeftOutOfATinyBody(t *testing.T) {
	t.Parallel()
	a, _ := lifeAppSized(t, 12, 12)

	chord(a, 's')
	chord(a, 's')

	st := a.store.State()
	if len(st.Panes) != 3 {
		t.Fatalf("panes = %d, want 3", len(st.Panes))
	}

	if _, drawn := a.layoutRects()["p2"]; drawn {
		t.Fatalf("p2 is drawn in a 12x12 terminal: %v", a.layoutRects())
	}

	if err := a.dispatch(core.FocusPane{ID: "p1"}); err != nil {
		t.Fatal(err)
	}

	chord(a, 'j')

	if got := a.store.State().Tab().Focus; got != "p3" {
		t.Errorf("Focus = %q after focus down, want p3 (p2 has no rect)", got)
	}
}

func TestWindowResizeThroughTinyAndBack(t *testing.T) {
	t.Parallel()
	a, _ := lifeAppSized(t, 240, 64)

	for _, k := range "svsvs" {
		chord(a, k)
	}

	for _, sz := range [][2]int{{20, 6}, {1, 1}, {3, 3}, {240, 64}} {
		sized(t, a, sz[0], sz[1])
		assertSizes(t, a)

		inner := a.bodyArea()
		cells := map[[2]int]core.PaneID{}

		for id, r := range a.layoutRects() {
			if r.W < minFrame || r.H < minFrame || r.X < inner.X || r.Y < inner.Y ||
				r.X+r.W > inner.X+inner.W || r.Y+r.H > inner.Y+inner.H {
				t.Fatalf("%dx%d: %s = %+v outside %+v", sz[0], sz[1], id, r, inner)
			}

			for y := r.Y; y < r.Y+r.H; y++ {
				for x := r.X; x < r.X+r.W; x++ {
					if o, dup := cells[[2]int{x, y}]; dup {
						t.Fatalf("%dx%d: %s and %s overlap at %d,%d", sz[0], sz[1], id, o, x, y)
					}

					cells[[2]int{x, y}] = id
				}
			}
		}

		for id, p := range a.panes {
			if c, r := p.size(); c < 1 || r < 1 {
				t.Fatalf("%dx%d: pane %s is %dx%d", sz[0], sz[1], id, c, r)
			}
		}
	}
}

// blockFetcher holds every fetch until release closes, ignoring its context.
type blockFetcher struct {
	started chan struct{}
	release chan struct{}
}

func (f *blockFetcher) Fetch(context.Context, string, string, int64) ([]byte, error) {
	f.started <- struct{}{}

	<-f.release

	return nil, errors.New("released")
}

func TestCloseBoundsLoaderWait(t *testing.T) {
	t.Parallel()

	f := &blockFetcher{started: make(chan struct{}, 1), release: make(chan struct{})}

	a, err := New(Options{
		caches:   testDrawCaches,
		Git:      newFakeGit(t),
		Logger:   testLogger(),
		Store:    core.NewStore(core.InitialState(core.Config{Theme: "catppuccin-mocha"})),
		Getenv:   testGetenv,
		Cwd:      testCwd,
		Pictures: f,
	})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	a.tick = (&ticked{}).tick

	t.Cleanup(func() { close(f.release) })

	a.loader.add(pictureJob{key: pictureKey{src: "https://github.com/a.png"}})
	<-f.started

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	res := make(chan error, 1)

	go func() { res <- a.Close(ctx) }()

	select {
	case err := <-res:
		if !errors.Is(err, context.DeadlineExceeded) {
			t.Errorf("Close() error = %v, want the context's", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("Close did not return while a loader worker was stuck")
	}
}

func TestPanesGetTheShellIntegration(t *testing.T) {
	tests := []struct {
		name string
		ig   term.Integration
		want bool
	}{
		{"on", term.Integration{Dir: "/run/1-shell"}, true},
		{"off", term.Integration{}, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			a, st := lifeApp(t)
			a.shell = tt.ig

			sized(t, a, 100, 30)

			got := slices.Contains(st.cmds[0].Env, "AIM_SHELL_DIR=/run/1-shell")
			if got != tt.want {
				t.Errorf("env has AIM_SHELL_DIR = %v, want %v: %q", got, tt.want, st.cmds[0].Env)
			}
		})
	}
}
