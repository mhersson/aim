package ui

import (
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	uv "github.com/charmbracelet/ultraviolet"

	"github.com/mhersson/aim/internal/core"
	"github.com/mhersson/aim/internal/ui/palette"
)

const escByte = "\x1b"

// escApp is a sized app on a fake clock whose focused pane is a new agent.
func escApp(t *testing.T) (*App, *fakeClock) {
	t.Helper()

	a, _, clk := kindApp(t)
	chord(a, 'n')

	return a, clk
}

// sentSince writes a marker straight to pane id, waits for it and returns what
// the pane was sent since the previous marker.
func sentSince(t *testing.T, a *App, id core.PaneID) string {
	t.Helper()

	p := a.panes[id]

	fp, ok := p.proc.(*fakeProc)
	if !ok {
		t.Fatalf("pane %s runs %T, want a fake process", id, p.proc)
	}

	n := strings.Count(fp.sent(), "~")

	p.sendKey(uv.KeyPressEvent(keyRune('~')))

	eventually(t, "the marker to reach "+string(id), func() bool { return strings.Count(fp.sent(), "~") > n })

	got := strings.TrimSuffix(fp.sent(), "~")

	return got[strings.LastIndex(got, "~")+1:]
}

// escStep is a key pressed after a wait on the fake clock.
type escStep struct {
	after time.Duration
	key   tea.KeyPressMsg
}

func escSteps(steps []escStep, a *App, clk *fakeClock) {
	for _, s := range steps {
		clk.Advance(s.after)
		press(a, s.key)
	}
}

// heldEsc is esc held down after the press that closed something: the first
// repeat after delay, then 25 more every 40 ms.
func heldEsc(delay time.Duration) []escStep {
	steps := []escStep{{delay, keyEsc}}
	for range 25 {
		steps = append(steps, escStep{40 * time.Millisecond, keyEsc})
	}

	return steps
}

func TestEscGuardTiming(t *testing.T) {
	t.Parallel()

	altEsc := tea.KeyPressMsg{Code: tea.KeyEscape, Mod: tea.ModAlt}
	dropped := escStep{30 * time.Millisecond, keyEsc}

	tests := []struct {
		name  string
		steps []escStep
		want  string
	}{
		{"a doubled esc is dropped", []escStep{{40 * time.Millisecond, keyEsc}}, ""},
		{"a held esc with a 225 ms repeat delay never reaches the pane", heldEsc(225 * time.Millisecond), ""},
		{"a held esc with a 600 ms repeat delay never reaches the pane", heldEsc(600 * time.Millisecond), ""},
		{"an esc 750 ms after the close reaches the pane", []escStep{{750 * time.Millisecond, keyEsc}}, escByte},
		{"an esc just short of 750 ms after the close is dropped", []escStep{{749 * time.Millisecond, keyEsc}}, ""},
		{"an esc 500 ms after a dropped one reaches the pane", []escStep{dropped, {500 * time.Millisecond, keyEsc}}, escByte},
		{"an esc just short of 500 ms after a dropped one is dropped", []escStep{dropped, {499 * time.Millisecond, keyEsc}}, ""},
		{"each dropped esc restarts the wait", []escStep{{300 * time.Millisecond, keyEsc}, {300 * time.Millisecond, keyEsc}, {300 * time.Millisecond, keyEsc}}, ""},
		{"a released esc reaches the pane after the wait", append(heldEsc(600*time.Millisecond), escStep{500 * time.Millisecond, keyEsc}), escByte},
		{"another key passes at once and ends the guard", []escStep{{10 * time.Millisecond, keyRune('x')}, {10 * time.Millisecond, keyEsc}}, "x" + escByte},
		{"a doubled esc read as alt+esc is dropped", []escStep{{0, altEsc}}, ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			a, clk := escApp(t)
			id := a.store.State().Tab().Focus
			_ = a.openChecks("github.com/acme/api", 7)

			press(a, keyEsc)

			if a.checks != nil {
				t.Fatal("setup: esc did not close the checks overlay")
			}

			escSteps(tt.steps, a, clk)

			if got := sentSince(t, a, id); got != tt.want {
				t.Errorf("the pane was sent %q, want %q", got, tt.want)
			}
		})
	}
}

// TestEscGuardCloses checks that an esc closing each of these and handing the
// keyboard to the pane drops an esc 30 ms later and passes one after 500 ms.
func TestEscGuardCloses(t *testing.T) {
	t.Parallel()

	plan := func(t *testing.T, a *App) {
		t.Helper()

		id := a.store.State().Tab().Focus
		a.Update(planReadMsg{
			seq: a.planSeq, pane: id, plan: core.Plan{Path: "/w/plan.md", Kind: core.PlanFile},
			root: "/w", base: "/w", doc: parseNow("# Plan\n\nStep one.\n", nil),
		})
	}

	tests := []struct {
		name   string
		open   func(t *testing.T) (*App, *fakeClock)
		closed func(a *App) bool
	}{
		{"review drawer", func(t *testing.T) (*App, *fakeClock) {
			t.Helper()

			r := newKeysRig(t, keysRepo(t))
			r.opened(t)

			return r.App, r.clk
		}, func(a *App) bool { return a.drawer.closing || a.store.State().Tab().Review == nil }},
		{"plan", func(t *testing.T) (*App, *fakeClock) {
			t.Helper()

			a, clk := escApp(t)
			plan(t, a)

			return a, clk
		}, func(a *App) bool { return a.plan == nil }},
		{"markdown file", func(t *testing.T) (*App, *fakeClock) {
			t.Helper()

			a, clk := escApp(t)
			a.Update(mdFileMsg{seq: a.planSeq, root: "/w", repo: "/w", path: "/w/a.md", doc: parseNow("# A\n\nText.\n", nil)})

			return a, clk
		}, func(a *App) bool { return a.plan == nil }},
		{"conversation", func(t *testing.T) (*App, *fakeClock) {
			t.Helper()

			a, clk := escApp(t)
			a.reads = &fakeReads{fail: map[string]error{}}
			_ = a.openConversation("github.com/acme/api", 7, core.PRInfo{Number: 7})

			return a, clk
		}, func(a *App) bool { return a.conv == nil }},
		{"PR list", func(t *testing.T) (*App, *fakeClock) {
			t.Helper()

			a, clk := escApp(t)
			a.prs.src = &refreshRec{fakePRs: newFakePRs()}
			press(a, keyLeader, keyRune('g'), keyRune('l'))

			return a, clk
		}, func(a *App) bool { return a.prList == nil }},
		{"checks", func(t *testing.T) (*App, *fakeClock) {
			t.Helper()

			a, clk := escApp(t)
			_ = a.openChecks("github.com/acme/api", 7)

			return a, clk
		}, func(a *App) bool { return a.checks == nil }},
		{"merge box", func(t *testing.T) (*App, *fakeClock) {
			t.Helper()

			a, clk := escApp(t)
			a.actions = &fakeActions{}
			a.openMerge("github.com/acme/api", 7, "aaaaaaa", "/w")

			return a, clk
		}, func(a *App) bool { return a.merge == nil }},
		{"compose box", func(t *testing.T) (*App, *fakeClock) {
			t.Helper()

			a, clk := escApp(t)
			a.compose = &composeView{}

			return a, clk
		}, func(a *App) bool { return a.compose == nil }},
		{"command palette", func(t *testing.T) (*App, *fakeClock) {
			t.Helper()

			a, clk := escApp(t)
			a.openCommandPalette()

			return a, clk
		}, func(a *App) bool { return a.overlay == nil }},
		{"theme picker", func(t *testing.T) (*App, *fakeClock) {
			t.Helper()

			a, clk := escApp(t)
			a.openThemePicker()

			return a, clk
		}, func(a *App) bool { return a.overlay == nil }},
		{"markdown picker", func(t *testing.T) (*App, *fakeClock) {
			t.Helper()

			a, clk := escApp(t)
			a.openOverlay(overlayMarkdown, "Markdown", []palette.Item{{ID: "a.md", Title: "a.md"}})

			return a, clk
		}, func(a *App) bool { return a.overlay == nil }},
		{"confirm", func(t *testing.T) (*App, *fakeClock) {
			t.Helper()

			a, clk := escApp(t)
			a.openConfirm(confirm{action: "Test", prompt: "Sure?", yes: func(*App) tea.Cmd { return nil }})

			return a, clk
		}, func(a *App) bool { return a.confirm == nil }},
		{"prompt", func(t *testing.T) (*App, *fakeClock) {
			t.Helper()

			a, clk := escApp(t)
			a.openPrompt(a.store.State().Tab().ID, "x", func(*App, string) tea.Cmd { return nil })

			return a, clk
		}, func(a *App) bool { return a.prompt == nil }},
		{"pending leader", func(t *testing.T) (*App, *fakeClock) {
			t.Helper()

			a, clk := escApp(t)
			press(a, keyLeader)

			return a, clk
		}, func(a *App) bool { return !a.pending }},
		{"worktree picker", func(t *testing.T) (*App, *fakeClock) {
			t.Helper()

			a, clk := escApp(t)
			a.picker.open = &picker{query: newPickerInput()}

			return a, clk
		}, func(a *App) bool { return a.picker.open == nil }},
		{"agents rail", func(t *testing.T) (*App, *fakeClock) {
			t.Helper()

			a, clk := escApp(t)
			chord(a, 'a')

			return a, clk
		}, func(a *App) bool { return !a.rail.focused }},
		{"usage popover", func(t *testing.T) (*App, *fakeClock) {
			t.Helper()

			a, clk := escApp(t)
			seedUsage(t, a, usageFive, usageWeek)
			chord(a, 'u')

			return a, clk
		}, func(a *App) bool { return !a.usageShows() }},
		{"copy mode", func(t *testing.T) (*App, *fakeClock) {
			t.Helper()

			a, clk := escApp(t)
			chord(a, '[')

			return a, clk
		}, func(a *App) bool { return a.copy == nil }},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			a, clk := tt.open(t)
			id := a.store.State().Tab().Focus

			if tt.closed(a) || a.paneHasKeys() {
				t.Fatal("setup: it did not open with the keyboard")
			}

			// What the esc's command sends lands before the next key, as a quick
			// command's message does in Bubble Tea.
			for _, m := range flatten(press(a, keyEsc)) {
				a.Update(m)
			}

			if !tt.closed(a) || !a.paneHasKeys() {
				t.Fatal("esc did not close it and hand the keyboard to the pane")
			}

			escSteps([]escStep{{30 * time.Millisecond, keyEsc}}, a, clk)

			if got := sentSince(t, a, id); got != "" {
				t.Errorf("an esc 30 ms after the close sent %q to the pane, want nothing", got)
			}

			escSteps([]escStep{{500 * time.Millisecond, keyEsc}}, a, clk)

			if got := sentSince(t, a, id); got != escByte {
				t.Errorf("an esc after 500 ms of quiet sent %q to the pane, want %q", got, escByte)
			}
		})
	}
}

func TestEscGuardSparesUI(t *testing.T) {
	t.Parallel()

	t.Run("an esc the drawer takes after an overlay over it closes", func(t *testing.T) {
		t.Parallel()

		r := newKeysRig(t, keysRepo(t))
		r.opened(t)
		id := r.store.State().Tab().Focus
		_ = r.openChecks("github.com/acme/api", 7)

		r.keys(keyEsc)

		if r.checks != nil || !r.drawerHasKeys() {
			t.Fatalf("setup: checks open %v, drawer has the keyboard %v, want the overlay closed onto the drawer", r.checks != nil, r.drawerHasKeys())
		}

		r.clk.Advance(30 * time.Millisecond)
		r.keys(keyEsc)

		if !r.drawer.closing {
			t.Error("the drawer did not take the second esc and close")
		}

		r.clk.Advance(30 * time.Millisecond)
		r.keys(keyEsc)

		if got := sentSince(t, r.App, id); got != "" {
			t.Errorf("the pane was sent %q, want nothing", got)
		}
	})

	t.Run("a close by another key starts no guard", func(t *testing.T) {
		t.Parallel()

		a, clk := escApp(t)
		id := a.store.State().Tab().Focus
		a.prs.src = &refreshRec{fakePRs: newFakePRs()}
		remapTables(t, a, contextKeys("pr_list", "q", "prlist.close"))
		press(a, keyLeader, keyRune('g'), keyRune('l'))
		press(a, keyRune('q'))

		if a.prList != nil {
			t.Fatal("setup: q did not close the list")
		}

		escSteps([]escStep{{30 * time.Millisecond, keyEsc}}, a, clk)

		if got := sentSince(t, a, id); got != escByte {
			t.Errorf("the pane was sent %q, want %q", got, escByte)
		}
	})
}

// TestEscGuardDroppedEscIsAKey checks that an esc the guard drops still does
// what any esc does before routing: it ends the toast and cancels a plan read
// that has not answered.
func TestEscGuardDroppedEscIsAKey(t *testing.T) {
	t.Parallel()

	a, clk := escApp(t)
	id := a.store.State().Tab().Focus
	_ = a.openChecks("github.com/acme/api", 7)

	press(a, keyEsc)

	a.notice = "pushed"
	asked := a.planSeq

	escSteps([]escStep{{30 * time.Millisecond, keyEsc}}, a, clk)

	if got := sentSince(t, a, id); got != "" {
		t.Fatalf("setup: the esc was not dropped; the pane was sent %q", got)
	}

	if a.notice != "" {
		t.Errorf("the toast reads %q after the dropped esc, want it gone", a.notice)
	}

	a.Update(planReadMsg{
		seq: asked, pane: id, plan: core.Plan{Path: "/w/plan.md", Kind: core.PlanFile},
		root: "/w", base: "/w", doc: parseNow("# Plan\n", nil),
	})

	if a.plan != nil {
		t.Error("a plan read asked before the dropped esc opened the overlay")
	}
}
