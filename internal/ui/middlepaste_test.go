package ui

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/mhersson/aim/internal/core"
	"github.com/mhersson/aim/internal/term"
)

// middleRun presses and releases the middle button at (x, y), running the
// press's command so the read's answer reaches the app.
func middleRun(a *App, x, y int, mod tea.KeyMod) {
	_, cmd := a.Update(tea.MouseClickMsg{X: x, Y: y, Button: tea.MouseMiddle, Mod: mod})
	run(a, cmd)
	mup(a, x, y, tea.MouseMiddle)
}

// markOf is what sendPaste("MARK") writes to p in its current paste mode.
func markOf(p *pane) string { return string(term.EncodePaste("MARK", p.vt.BracketedPaste())) }

// sentAfterMark pastes a marker and returns everything sent once it arrived:
// the reply stream is ordered, so anything sent earlier is in it.
func sentAfterMark(t *testing.T, p *pane, fp *fakeProc) string {
	t.Helper()

	mark := markOf(p)
	p.sendPaste("MARK")

	eventually(t, "marker", func() bool { return strings.Contains(fp.sent(), mark) })

	return fp.sent()
}

func pasteCalls(f *fakeRun) int {
	f.mu.Lock()
	defer f.mu.Unlock()

	n := 0

	for _, c := range f.calls {
		if c.argv[0] == "wl-paste" {
			n++
		}
	}

	return n
}

func TestMiddleClickPastesIntoThePaneUnderThePointer(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		modes string
		out   string
		mod   tea.KeyMod
		want  string
	}{
		{"plain", "", "from primary", 0, "from primary"},
		{"escapes stripped, nothing added", "", "echo hi\x1b[2J", 0, "echo hi[2J"},
		{"shift held", "", "from primary", tea.ModShift, "from primary"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			a, procs, f := shellsWith(t, sampleText, "RIGHT")
			p1, p2 := a.panes["p1"], a.panes["p2"]

			if tt.modes != "" {
				feedAndWait(t, p1, procs["p1"], tt.modes)
			}

			f.out = tt.out
			c := contentRectOf(a, "p1")

			middleRun(a, c.X+1, c.Y+1, tt.mod)

			if a.store.State().Tab().Focus != "p1" {
				t.Fatalf("focus = %q, want p1", a.store.State().Tab().Focus)
			}

			if got, want := sentAfterMark(t, p1, procs["p1"]), tt.want+markOf(p1); !strings.Contains(got, want) {
				t.Fatalf("p1 got %q, want it to contain %q", got, want)
			}

			if got := sentAfterMark(t, p2, procs["p2"]); got != markOf(p2) {
				t.Fatalf("p2 got %q, want only the marker", got)
			}

			if n := pasteCalls(f); n != 1 {
				t.Fatalf("wl-paste ran %d times, want 1", n)
			}
		})
	}
}

func TestMiddleClickOnATrackingProgramIsForwarded(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		mod  tea.KeyMod
		want string
	}{
		{"plain", 0, "\x1b[<1;2;2M"},
		{"shift", tea.ModShift, "\x1b[<5;2;2M"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			a, procs, f := trackingShells(t)
			f.out = "from primary"
			c := contentRectOf(a, "p1")

			middleRun(a, c.X+1, c.Y+1, tt.mod)

			got := settle(t, a, procs["p1"], c)
			if !strings.Contains(got, tt.want) {
				t.Fatalf("sent %q, want it to contain %q", got, tt.want)
			}

			if !strings.Contains(got, "\x1b[<1;2;2m") {
				t.Fatalf("sent %q, want the release forwarded", got)
			}

			if strings.Contains(got, "from primary") {
				t.Fatalf("sent %q, want no paste", got)
			}

			if n := pasteCalls(f); n != 0 {
				t.Fatalf("wl-paste ran %d times, want 0", n)
			}
		})
	}
}

func TestMiddleClickPastesTheSelectionOverSSH(t *testing.T) {
	t.Parallel()

	a, procs, f := shellsWith(t, sampleText, "")
	a.clip.getenv = envFrom(map[string]string{"SSH_CONNECTION": "x", "WAYLAND_DISPLAY": "w"})
	a.copyOnSelect = false

	c := contentRectOf(a, "p1")
	dragRun(a, c.X, c.Y, c.X+4, c.Y, 0)

	c2 := contentRectOf(a, "p2")
	middleRun(a, c2.X+1, c2.Y+1, 0)

	eventually(t, "paste", func() bool { return strings.Contains(procs["p2"].sent(), "hello") })

	f.mu.Lock()
	defer f.mu.Unlock()

	if len(f.calls) != 0 {
		t.Fatalf("native calls %v, want none", f.calls)
	}
}

func TestMiddlePasteClearsOnlyItsPaneSelection(t *testing.T) {
	t.Parallel()

	a, procs, f := shellsWith(t, sampleText, "")
	f.out = "x"

	c1, c2 := contentRectOf(a, "p1"), contentRectOf(a, "p2")
	drag(a, c1.X, c1.Y, c1.X+4, c1.Y, 0)

	if a.sel == nil {
		t.Fatal("no selection after the drag")
	}

	middleRun(a, c2.X+1, c2.Y+1, 0)
	eventually(t, "paste", func() bool { return strings.Contains(procs["p2"].sent(), "x") })

	if a.sel == nil {
		t.Fatal("a paste into p2 cleared p1's selection")
	}

	middleRun(a, c1.X+1, c1.Y+1, 0)
	eventually(t, "paste", func() bool { return strings.Contains(procs["p1"].sent(), "x") })

	if a.sel != nil {
		t.Fatal("selection kept after a paste into its pane")
	}
}

func TestMiddlePasteArrival(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		prep func(a *App)
		msg  func(a *App) middlePasteMsg
		want map[core.PaneID]string
	}{
		{
			name: "delivered",
			msg:  func(a *App) middlePasteMsg { return middlePasteMsg{"p1", a.panes["p1"], "x"} },
			want: map[core.PaneID]string{"p1": "x"},
		},
		{
			name: "empty text",
			msg:  func(a *App) middlePasteMsg { return middlePasteMsg{"p1", a.panes["p1"], ""} },
		},
		{
			name: "replaced pane",
			msg:  func(a *App) middlePasteMsg { return middlePasteMsg{"p1", a.panes["p2"], "x"} },
		},
		{
			name: "closed pane",
			prep: func(a *App) { a.closePane("p1") },
			msg:  func(a *App) middlePasteMsg { return middlePasteMsg{"p1", a.panes["p1"], "x"} },
		},
		{
			name: "leader pending",
			prep: func(a *App) { a.pending = true },
			msg:  func(a *App) middlePasteMsg { return middlePasteMsg{"p1", a.panes["p1"], "x"} },
		},
		{
			name: "copy mode",
			prep: func(a *App) {
				_ = a.dispatch(core.FocusPane{ID: "p1"})
				run(a, chord(a, '['))
			},
			msg: func(a *App) middlePasteMsg { return middlePasteMsg{"p1", a.panes["p1"], "x"} },
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			a, procs, _ := shellsWith(t, sampleText, "RIGHT")
			old := a.panes["p1"]

			if tt.prep != nil {
				tt.prep(a)
			}

			msg := tt.msg(a)
			if msg.p == nil {
				msg.p = old
			}

			a.Update(msg)

			for _, id := range []core.PaneID{"p1", "p2"} {
				p := a.panes[id]
				if p == nil {
					continue
				}

				want := tt.want[id] + markOf(p)
				if got := sentAfterMark(t, p, procs[id]); got != want {
					t.Fatalf("%s got %q, want %q", id, got, want)
				}
			}
		})
	}
}
