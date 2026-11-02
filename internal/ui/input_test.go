package ui

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	uv "github.com/charmbracelet/ultraviolet"
	"github.com/charmbracelet/x/ansi"

	"github.com/mhersson/aim/internal/core"
)

// twoPanes returns an app with p1 and p2 open; p2 is focused.
func twoPanes(t *testing.T) (*App, *fakeProc, *fakeProc) {
	t.Helper()

	a, procs := framedApp(t, 100, 30, shellSpec("/a", ""), shellSpec("/b", ""))

	return a, procs["p1"], procs["p2"]
}

// settled sends a marker key straight to the pane and returns everything the
// process received so far, without markers, so earlier async writes are accounted for.
func settled(t *testing.T, a *App, id core.PaneID, fp *fakeProc) string {
	t.Helper()

	// A pane in Kitty mode encodes the marker as CSI u.
	markers := func() int { return strings.Count(fp.sent(), "\x01") + strings.Count(fp.sent(), "\x1b[97;5u") }
	want := markers() + 1

	a.panes[id].sendKey(uv.KeyPressEvent{Code: 'a', Mod: uv.ModCtrl})
	eventually(t, "marker", func() bool { return markers() == want })

	return strings.NewReplacer("\x01", "", "\x1b[97;5u", "").Replace(fp.sent())
}

func TestKeysReachFocusedPane(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		setup string
		key   tea.KeyPressMsg
		want  string
	}{
		{"rune", "", keyRune('a'), "a"},
		{"q", "", keyRune('q'), "q"},
		{"ctrl+c", "", tea.KeyPressMsg{Code: 'c', Mod: tea.ModCtrl}, "\x03"},
		{"up", "", tea.KeyPressMsg{Code: tea.KeyUp}, "\x1b[A"},
		{"up application cursor", "\x1b[?1h", tea.KeyPressMsg{Code: tea.KeyUp}, "\x1bOA"},
		{"shift+enter kitty", "\x1b[>1u", tea.KeyPressMsg{Code: tea.KeyEnter, Mod: tea.ModShift}, "\x1b[13;2u"},
		{"repeat", "", tea.KeyPressMsg{Code: tea.KeyUp, IsRepeat: true}, "\x1b[A"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			a, fp1, fp2 := twoPanes(t)
			if tt.setup != "" {
				feedAndWait(t, a.panes["p2"], fp2, tt.setup)
			}

			press(a, tt.key)

			if got := settled(t, a, "p2", fp2); got != tt.want {
				t.Errorf("focused pane got %q, want %q", got, tt.want)
			}

			if got := settled(t, a, "p1", fp1); got != "" {
				t.Errorf("other pane got %q, want nothing", got)
			}
		})
	}
}

func TestKeyReleaseIgnored(t *testing.T) {
	t.Parallel()
	a, _, fp2 := twoPanes(t)

	a.Update(tea.KeyReleaseMsg{Code: 'a', Text: "a"})

	if got := settled(t, a, "p2", fp2); got != "" {
		t.Errorf("release reached the pane: %q", got)
	}
}

func TestLeaderTwiceSendsCtrlSpace(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		setup string
		want  string
	}{
		{"legacy", "", "\x00"},
		{"kitty", "\x1b[>1u", "\x1b[32;5u"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			a, _, fp2 := twoPanes(t)
			if tt.setup != "" {
				feedAndWait(t, a.panes["p2"], fp2, tt.setup)
			}

			if cmd := press(a, keyLeader, keyLeader); cmd != nil {
				t.Error("leader twice returned a command")
			}

			if a.pending {
				t.Error("leader still pending")
			}

			if got := settled(t, a, "p2", fp2); got != tt.want {
				t.Errorf("pane got %q, want %q", got, tt.want)
			}
		})
	}
}

func TestLeaderStillRuns(t *testing.T) {
	t.Parallel()
	a, fp1, fp2 := twoPanes(t)

	press(a, keyLeader, keyRune('t'))

	if a.overlay == nil || a.overlay.kind != overlayThemes {
		t.Fatal("ctrl+space t did not open the theme picker")
	}

	a.closeOverlay()

	press(a, keyLeader, keyEsc)

	for id, fp := range map[core.PaneID]*fakeProc{"p1": fp1, "p2": fp2} {
		if got := settled(t, a, id, fp); got != "" {
			t.Errorf("%s got %q, want nothing", id, got)
		}
	}
}

func TestOverlayFirst(t *testing.T) {
	t.Parallel()
	a, fp1, fp2 := twoPanes(t)
	a.keymap.delay = 0
	pressRun(a, keyLeader, keyRune(':'), keyRune('a'))
	_, cmd := a.Update(tea.PasteMsg{Content: "quit"})
	run(a, cmd)

	if view := ansi.Strip(a.View().Content); !strings.Contains(view, "\u276f aquit") {
		t.Errorf("query lacks the typed and pasted text:\n%s", view)
	}

	for id, fp := range map[core.PaneID]*fakeProc{"p1": fp1, "p2": fp2} {
		if got := settled(t, a, id, fp); got != "" {
			t.Errorf("%s got %q, want nothing", id, got)
		}
	}
}

func TestPasteToPane(t *testing.T) {
	t.Parallel()
	a, _, fp2 := twoPanes(t)

	a.Update(tea.PasteMsg{Content: "one"})

	if got := settled(t, a, "p2", fp2); got != "one" {
		t.Fatalf("plain paste = %q, want %q", got, "one")
	}

	feedAndWait(t, a.panes["p2"], fp2, "\x1b[?2004h")
	a.Update(tea.PasteMsg{Content: "two"})

	if got := settled(t, a, "p2", fp2); got != "one\x1b[200~two\x1b[201~" {
		t.Errorf("bracketed paste = %q", got)
	}
}

func TestFocusReports(t *testing.T) {
	t.Parallel()
	a, fp1, fp2 := twoPanes(t)
	feedAndWait(t, a.panes["p1"], fp1, "\x1b[?1004h")
	feedAndWait(t, a.panes["p2"], fp2, "\x1b[?1004h")

	a.Update(tea.BlurMsg{})
	a.Update(tea.FocusMsg{})

	if got := settled(t, a, "p2", fp2); got != "\x1b[O\x1b[I" {
		t.Errorf("outer focus reports = %q", got)
	}

	if err := a.dispatch(core.FocusPane{ID: "p1"}); err != nil {
		t.Fatal(err)
	}

	if got := settled(t, a, "p2", fp2); got != "\x1b[O\x1b[I\x1b[O" {
		t.Errorf("old pane got %q, want focus-out", got)
	}

	if got := settled(t, a, "p1", fp1); got != "\x1b[I" {
		t.Errorf("new pane got %q, want focus-in", got)
	}

	a.Update(tea.BlurMsg{})

	if err := a.dispatch(core.FocusPane{ID: "p2"}); err != nil {
		t.Fatal(err)
	}

	if got := settled(t, a, "p2", fp2); got != "\x1b[O\x1b[I\x1b[O" {
		t.Errorf("blurred terminal still got a report: %q", got)
	}
}

func TestViewRequestsFocusReports(t *testing.T) {
	t.Parallel()
	a := newTestApp(t)
	v := a.View()

	if !v.ReportFocus {
		t.Error("ReportFocus = false")
	}

	if !v.KeyboardEnhancements.ReportAlternateKeys {
		t.Error("ReportAlternateKeys = false")
	}

	if v.KeyboardEnhancements.ReportEventTypes {
		t.Error("key releases requested")
	}
}

func TestPasteEndsPendingLeader(t *testing.T) {
	t.Parallel()
	a, _, fp2 := twoPanes(t)
	press(a, keyLeader)
	a.Update(whichKeyMsg{seq: a.seq})

	if !a.pending || !a.popup {
		t.Fatalf("pending = %v, popup = %v, want both set", a.pending, a.popup)
	}

	a.Update(tea.PasteMsg{Content: "one"})

	if a.pending || a.popup {
		t.Errorf("pending = %v, popup = %v after paste", a.pending, a.popup)
	}

	if got := settled(t, a, "p2", fp2); got != "one" {
		t.Errorf("paste = %q, want %q", got, "one")
	}

	press(a, keyRune('x'))

	if len(a.panes) != 2 {
		t.Errorf("x after the paste closed a pane; %d left", len(a.panes))
	}
}

func TestPanesAnswerThemeColours(t *testing.T) {
	t.Parallel()

	const (
		queryFg = "\x1b]10;?\x07"
		queryBg = "\x1b]11;?\x07"
	)

	a, _, fp2 := twoPanes(t)
	a.keymap.delay = 0
	p2 := a.panes["p2"]
	// The harness injects its panes after the first message, so forget what was reported.
	a.reportedFg, a.reportedBg = "", ""
	a.Update(nil)

	// answers asks both questions and returns what the process got back.
	answers := func() string {
		before := len(fp2.sent())
		feedAndWait(t, p2, fp2, queryFg+queryBg)
		eventually(t, "both answers", func() bool {
			return strings.Count(fp2.sent()[before:], "rgb:") == 2
		})

		return fp2.sent()[before:]
	}
	hasColours := func(got, fg, bg string) bool {
		return strings.Contains(got, "]10;"+fg) && strings.Contains(got, "]11;"+bg)
	}

	if got := answers(); !hasColours(got, "rgb:cdcd/d6d6/f4f4", "rgb:1e1e/1e1e/2e2e") {
		t.Errorf("mocha answers = %q", got)
	}

	// The picker's live preview already reports the highlighted theme.
	pressRun(a, keyLeader, keyRune('t'), keyDown)

	if got := answers(); !hasColours(got, "rgb:4c4c/4f4f/6969", "rgb:efef/f1f1/f5f5") {
		t.Errorf("latte preview answers = %q", got)
	}

	pressRun(a, keyEsc)

	if got := answers(); !hasColours(got, "rgb:cdcd/d6d6/f4f4", "rgb:1e1e/1e1e/2e2e") {
		t.Errorf("answers after cancelling the preview = %q", got)
	}

	pressRun(a, keyLeader, keyRune('t'), keyDown, keyEnter)

	if got := answers(); !hasColours(got, "rgb:4c4c/4f4f/6969", "rgb:efef/f1f1/f5f5") {
		t.Errorf("latte answers = %q", got)
	}
}

func TestNewPaneAnswersThemeColours(t *testing.T) {
	t.Parallel()
	a, st := lifeAppSized(t, 100, 30)

	chord(a, 'v')

	fp := st.proc(1)
	feedAndWait(t, a.panes["p2"], fp, "\x1b]11;?\x07")
	eventually(t, "answer", func() bool { return strings.Contains(fp.sent(), "]11;rgb:1e1e/1e1e/2e2e") })
}
