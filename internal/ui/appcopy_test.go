package ui

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
)

// copyApp is a 60x14 app with one shell holding 30 numbered lines and the
// last row "abcdef", plus the recorder of native clipboard writes.
func copyApp(t *testing.T) (*App, *fakeProc, *fakeRun) {
	t.Helper()

	a, procs := framedApp(t, 60, 14, shellSpec("/w/a", manyLines(30)+"abcdef"))

	return a, procs["p1"], fakeClip(a)
}

func keys(a *App, ks ...string) {
	for _, k := range ks {
		switch k {
		case "esc":
			pressRun(a, keyEsc)
		case "enter":
			pressRun(a, tea.KeyPressMsg{Code: tea.KeyEnter})
		case "ctrl+u":
			pressRun(a, tea.KeyPressMsg{Code: 'u', Mod: tea.ModCtrl})
		default:
			pressRun(a, keyRune([]rune(k)[0]))
		}
	}
}

func enterCopy(a *App) { pressRun(a, keyLeader, keyRune('[')) }

func cursorOf(a *App) (x, y int, ok bool) {
	c := a.View().Cursor
	if c == nil {
		return 0, 0, false
	}

	return c.X, c.Y, true
}

// screenText is the plain text of row y of the drawn frame.
func screenText(a *App, y int) string {
	lines := strings.Split(ansi.Strip(a.View().Content), "\n")

	return lines[y]
}

func TestCopyModeStartsAtBottomOfView(t *testing.T) {
	t.Parallel()
	a, _, _ := copyApp(t)
	c := contentRect(a.layoutRects()["p1"])

	enterCopy(a)

	if a.copy == nil {
		t.Fatal("leader [ did not enter copy mode")
	}

	v := a.View()
	if v.Cursor == nil || v.Cursor.X != c.X || v.Cursor.Y != c.Y+c.H-1 {
		t.Fatalf("cursor = %+v, want the bottom-left content cell (%d,%d)", v.Cursor, c.X, c.Y+c.H-1)
	}

	if v.Cursor.Shape != tea.CursorBlock {
		t.Errorf("cursor shape = %v, want block", v.Cursor.Shape)
	}

	if got := a.panes["p1"].linesAbove(); got != 0 {
		t.Errorf("entering scrolled the view by %d", got)
	}
}

func TestCopyModeMotionsMoveCursorAndScrollView(t *testing.T) {
	t.Parallel()
	a, _, _ := copyApp(t)
	c := contentRect(a.layoutRects()["p1"])

	enterCopy(a)
	keys(a, "k", "k")

	if _, y, _ := cursorOf(a); y != c.Y+c.H-3 {
		t.Fatalf("cursor row = %d after k k, want %d", y, c.Y+c.H-3)
	}

	if got := a.panes["p1"].linesAbove(); got != 0 {
		t.Fatalf("view scrolled by %d while the cursor is inside it", got)
	}

	for range c.H {
		keys(a, "k")
	}

	// 2+H presses from the bottom row cross the top after H-1 and scroll the other 3.
	if got := a.panes["p1"].linesAbove(); got != 3 {
		t.Errorf("linesAbove = %d after walking above the view, want 3", got)
	}

	if _, y, _ := cursorOf(a); y != c.Y {
		t.Errorf("cursor row = %d, want the top content row %d", y, c.Y)
	}

	keys(a, "G")

	if got := a.panes["p1"].linesAbove(); got != 0 {
		t.Errorf("G left the view %d lines above live", got)
	}
}

func TestCopyModeSelectHighlightsAndYankCopies(t *testing.T) {
	t.Parallel()
	a, _, f := copyApp(t)
	c := contentRect(a.layoutRects()["p1"])
	y := c.Y + c.H - 1

	enterCopy(a)
	keys(a, "v", "l", "l")

	for x, want := range []bool{true, true, true, false} {
		if got := selBgAt(a, c.X+x, y); got != want {
			t.Errorf("selection background at column %d = %v, want %v", x, got, want)
		}
	}

	if got := selBgAt(a, c.X, y-1); got {
		t.Error("selection painted a row it does not touch")
	}

	keys(a, "y")

	if a.copy != nil {
		t.Error("y did not leave copy mode")
	}

	if got := copiedText(f); got != "abc" {
		t.Errorf("copied %q, want abc", got)
	}

	if selBgAt(a, c.X, y) {
		t.Error("highlight remains after leaving copy mode")
	}
}

func TestCopyModeYankWithoutSelectionCopiesNothing(t *testing.T) {
	t.Parallel()
	a, _, f := copyApp(t)

	enterCopy(a)
	keys(a, "y")

	if a.copy != nil {
		t.Error("y did not leave copy mode")
	}

	if got := copiedText(f); got != "" {
		t.Errorf("copied %q with no selection", got)
	}
}

func TestCopyModeQuitReturnsToLive(t *testing.T) {
	t.Parallel()

	for _, k := range []string{"q", "esc"} {
		a, _, f := copyApp(t)

		enterCopy(a)
		keys(a, "g", "g")

		if a.panes["p1"].linesAbove() == 0 {
			t.Fatalf("%s: gg did not scroll to the top", k)
		}

		keys(a, k)

		if a.copy != nil || a.panes["p1"].linesAbove() != 0 {
			t.Errorf("%s: copy = %v, linesAbove = %d, want live", k, a.copy, a.panes["p1"].linesAbove())
		}

		if got := copiedText(f); got != "" {
			t.Errorf("%s copied %q", k, got)
		}
	}
}

func TestCopyModeKeysDoNotReachTheProgram(t *testing.T) {
	t.Parallel()
	a, fp, _ := copyApp(t)

	enterCopy(a)
	keys(a, "j", "k", "q")

	// The marker's own bytes are the only thing the program may have received.
	if got := sentAfterMark(t, a.panes["p1"], fp); got != markOf(a.panes["p1"]) {
		t.Errorf("program received %q from copy mode keys, want only the marker", got)
	}
}

func TestCopyModeSearchJumpsToMatch(t *testing.T) {
	t.Parallel()
	a, _, _ := copyApp(t)

	enterCopy(a)
	keys(a, "/")

	if a.prompt == nil {
		t.Fatal("/ did not open the search prompt")
	}

	typeText(a, "L00003")
	keys(a, "enter")

	if a.prompt != nil || a.copy == nil {
		t.Fatalf("prompt = %v, copy = %v, want the prompt closed and copy mode kept", a.prompt, a.copy)
	}

	var line string

	a.panes["p1"].withGrid(func(g grid) {
		line = extract(g, selection{mode: selLine, anchor: a.copy.m.cursor, head: a.copy.m.cursor})
	})

	if line != "L00003" {
		t.Errorf("cursor is on %q, want L00003", line)
	}

	_, y, ok := cursorOf(a)
	if !ok || !strings.Contains(screenText(a, y), "L00003") {
		t.Errorf("drawn cursor row %d shows %q, want L00003", y, screenText(a, y))
	}
}

func TestCopyModeSearchBackwardAndRepeat(t *testing.T) {
	t.Parallel()
	a, _, _ := copyApp(t)

	enterCopy(a)
	keys(a, "?")
	typeText(a, "L0002")
	keys(a, "enter")

	row := a.copy.m.cursor.row
	if row != 29 {
		t.Fatalf("backward search from the bottom stopped at row %d, want 29", row)
	}

	keys(a, "n")

	if got := a.copy.m.cursor.row; got != 28 {
		t.Errorf("n repeated to row %d, want 28", got)
	}

	keys(a, "N")

	if got := a.copy.m.cursor.row; got != 29 {
		t.Errorf("N repeated to row %d, want 29", got)
	}

	keys(a, "?", "enter")

	if got := a.copy.m.cursor.row; got != 28 {
		t.Errorf("an empty backward search moved to row %d, want 28", got)
	}
}

func TestCopyModeSearchNotFoundSetsNotice(t *testing.T) {
	t.Parallel()
	a, _, _ := copyApp(t)

	enterCopy(a)
	before := a.copy.m.cursor

	keys(a, "/")
	typeText(a, "zzz")
	keys(a, "enter")

	if a.copy.m.cursor != before {
		t.Errorf("cursor moved to %v on a miss", a.copy.m.cursor)
	}

	if !strings.Contains(a.notice, "zzz") || a.noticeLevel() != levelNotice {
		t.Errorf("notice = %q at level %v, want one carrying the query at the notice level", a.notice, a.noticeLevel())
	}
}

func TestCopyModeSearchEscKeepsCopyMode(t *testing.T) {
	t.Parallel()
	a, _, _ := copyApp(t)

	enterCopy(a)
	keys(a, "/", "esc")

	if a.prompt != nil || a.copy == nil {
		t.Errorf("prompt = %v, copy = %v, want a closed prompt and copy mode", a.prompt, a.copy)
	}
}

func TestCopyModeIgnoresMouse(t *testing.T) {
	t.Parallel()
	a, _, _ := shellsWith(t, manyLines(30), manyLines(30))
	x1, y1 := centre(a, "p1")
	x2, y2 := centre(a, "p2")

	enterCopy(a)
	before := a.copy.m

	drag(a, x2, y2, x2+3, y2, 0)
	click(a, x1, y1)
	wheelAt(a, x2, y2, tea.MouseWheelUp)
	mdown(a, 3, 0, tea.MouseLeft, 0)

	if a.sel != nil {
		t.Error("a drag selected during copy mode")
	}

	if got := a.store.State().Tab().Focus; got != "p2" {
		t.Errorf("a click changed focus to %q", got)
	}

	if got := a.panes["p2"].linesAbove(); got != 0 {
		t.Errorf("the wheel scrolled the pane by %d", got)
	}

	if a.copy == nil || a.copy.m.cursor != before.cursor {
		t.Error("the mouse moved the copy mode cursor or ended it")
	}
}

func TestCopyModeEndsWhenPaneResizes(t *testing.T) {
	t.Parallel()
	a, _, _ := copyApp(t)

	enterCopy(a)
	sized(t, a, 50, 14)

	if a.copy != nil {
		t.Error("copy mode survived a resize")
	}
}

func TestCopyModeEndsWhenPaneCloses(t *testing.T) {
	t.Parallel()
	a, _, _ := shellsWith(t, manyLines(30), manyLines(30))

	enterCopy(a)
	keys(a, "/")
	a.closePane("p2")

	if a.copy != nil || a.prompt != nil {
		t.Errorf("copy = %v, prompt = %v after the pane closed, want both gone", a.copy, a.prompt)
	}
}

func TestCopyModeEndsOnAltScreenSwitch(t *testing.T) {
	t.Parallel()
	a, fp, _ := copyApp(t)

	enterCopy(a)
	feedAndWait(t, a.panes["p1"], fp, "\x1b[?1049h")
	a.Update(paneDirtyMsg{id: "p1", p: a.panes["p1"]})

	if a.copy != nil {
		t.Error("copy mode survived a switch to the alt screen")
	}
}

func TestCopyModeWithoutPaneDoesNothing(t *testing.T) {
	t.Parallel()
	a := newTestApp(t)
	sized(t, a, 60, 14)

	enterCopy(a)

	if a.copy != nil {
		t.Error("copy mode entered with no pane")
	}
}

func TestCopyModeShiftsRowsByTrimmedHistory(t *testing.T) {
	t.Parallel()

	cs := copyState{
		m: copyMode{
			cursor: point{row: 12, col: 2}, top: 8,
			sel: &selection{mode: selStream, anchor: point{row: 4, col: 1}, head: point{row: 12, col: 2}},
		},
		trimmed: 100,
	}

	got := cs.shifted(105, 40)

	if got.m.cursor.row != 7 || got.m.top != 3 || got.m.sel.anchor.row != 0 || got.m.sel.head.row != 7 || got.trimmed != 105 {
		t.Errorf("shifted = %+v sel %+v", got.m, *got.m.sel)
	}

	if cs.m.cursor.row != 12 {
		t.Error("shifted mutated its receiver")
	}

	gone := cs.shifted(150, 40)
	if gone.m.sel != nil || gone.m.cursor.row != 0 || gone.m.top != 0 {
		t.Errorf("a fully trimmed selection survived: %+v", gone.m)
	}
}

func TestCopyModeCursorStaysOnItsTextWhileLiveOutputScrolls(t *testing.T) {
	t.Parallel()
	a, fp, _ := copyApp(t)

	enterCopy(a)
	keys(a, "k", "k")

	_, y, _ := cursorOf(a)
	before := screenText(a, y)

	feedAndWait(t, a.panes["p1"], fp, strings.Repeat("\r\nx", 40))
	a.Update(paneDirtyMsg{id: "p1", p: a.panes["p1"]})

	_, y2, ok := cursorOf(a)
	if !ok {
		t.Fatal("cursor vanished after output")
	}

	if got := screenText(a, y2); got != before {
		t.Errorf("cursor row shows %q, want %q", got, before)
	}
}

// Past the 10000-line cap the cursor and selection stay on their text until it
// is trimmed, then clamp to the oldest line.
func TestCopyModeAcrossHistoryCap(t *testing.T) {
	t.Parallel()
	a, fp, _ := copyApp(t)
	p := a.panes["p1"]

	feedLines(t, p, fp, 0, 10100)
	a.Update(paneDirtyMsg{id: "p1", p: a.panes["p1"]})

	enterCopy(a)
	keys(a, "k", "k", "v", "k")

	_, y, ok := cursorOf(a)
	if !ok {
		t.Fatal("setup: no cursor")
	}

	before := screenText(a, y)

	feedLines(t, p, fp, 10100, 10150)
	a.Update(paneDirtyMsg{id: "p1", p: a.panes["p1"]})

	_, y2, ok := cursorOf(a)
	if !ok || screenText(a, y2) != before {
		t.Fatalf("cursor row = %q (drawn %v) after a small trim, want %q", screenText(a, y2), ok, before)
	}

	feedLines(t, p, fp, 10150, 20300)
	a.Update(paneDirtyMsg{id: "p1", p: a.panes["p1"]})

	var rows int

	p.withGrid(func(g grid) { rows = g.Rows() })

	cs := a.copy
	if cs == nil {
		t.Fatal("copy mode ended")
	}

	if cs.m.cursor.row != 0 || cs.m.top != 0 {
		t.Errorf("cursor row %d, top %d after the text was trimmed, want both 0", cs.m.cursor.row, cs.m.top)
	}

	if s := cs.m.sel; s != nil && (s.anchor.row < 0 || s.head.row < 0 || s.anchor.row >= rows || s.head.row >= rows) {
		t.Errorf("selection %+v outside %d rows", *s, rows)
	}

	if _, _, ok := cursorOf(a); !ok {
		t.Error("no cursor drawn after the clamp")
	}
}

func TestCopyModeTrimmedSelectionStartTakesColumnZero(t *testing.T) {
	t.Parallel()

	g := newFakeGrid(10, nil, "L00001", "L00002")
	cs := copyState{m: copyMode{sel: &selection{mode: selStream, anchor: point{row: 0, col: 3}, head: point{row: 2, col: 3}}}}

	got := cs.shifted(1, g.Rows())

	if text := extract(g, *got.m.sel); text != "L00001\nL000" {
		t.Errorf("extract = %q, want %q", text, "L00001\nL000")
	}

	cs.m.sel.mode = selBlock
	if s := cs.shifted(1, g.Rows()).m.sel; s.anchor.col != 3 {
		t.Errorf("block mode anchor column = %d, want 3", s.anchor.col)
	}
}

func TestCopyModeCursorFollowsDrawnRowsDuringSyncHold(t *testing.T) {
	t.Parallel()
	// A fake clock fixed at the hold's start keeps the hold open.
	a := newTestApp(t)
	a.clock = &fakeClock{t: t0}

	a, procs := framedAppOn(t, a, 60, 14, shellSpec("/w/a", manyLines(30)+"abcdef"))
	fp := procs["p1"]

	fakeClip(a)

	enterCopy(a)

	_, y, ok := cursorOf(a)
	if !ok {
		t.Fatal("setup: no cursor")
	}

	before := screenText(a, y)

	feedAndWait(t, a.panes["p1"], fp, "\x1b[?2026h"+strings.Repeat("\r\nheld", 5))
	a.renderPane(a.panes["p1"])

	_, y2, ok := cursorOf(a)
	if !ok {
		t.Fatal("cursor vanished during the hold")
	}

	if got := screenText(a, y2); got != before {
		t.Errorf("cursor row shows %q during the hold, want %q", got, before)
	}

	if strings.Contains(ansi.Strip(a.View().Content), "held") {
		t.Error("the held output was drawn before the hold ended")
	}
}
