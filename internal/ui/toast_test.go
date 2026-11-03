package ui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
	"github.com/charmbracelet/x/exp/golden"

	"github.com/mhersson/aim/internal/core"
	"github.com/mhersson/aim/internal/ui/theme"
)

// screenRows is the plain text of every row of the drawn frame.
func screenRows(a *App) []string {
	return strings.Split(ansi.Strip(a.View().Content), "\n")
}

// cells is the plain text of columns [from, to) of row y, one rune per cell
// (every glyph the chrome draws is one cell wide).
func cells(a *App, y, from, to int) string {
	return string([]rune(screenRows(a)[y])[from:to])
}

// toastApp is a w x h app with two side-by-side shells; the toast of a notice
// sits over p2.
func toastApp(t *testing.T, w, h int) (*App, map[core.PaneID]*fakeProc) {
	t.Helper()

	a, procs := framedApp(t, w, h, shellSpec("/w/a", ""), shellSpec("/w/b", ""))
	fakeClip(a)

	return a, procs
}

func TestToastBox(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		icons  bool
		notice string
		w      int // box width
		mid    string
	}{
		{"icon", true, "boom", 10, "│ " + iconInfo + " boom │"},
		{"icons off", false, "boom", 8, "│ boom │"},
		{"control characters", true, "a\nb\x1b[31m", 12, "│ " + iconInfo + " ab[31m │"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			a, _ := toastApp(t, 100, 30)
			a.icons = tt.icons
			a.notice = tt.notice

			x := 100 - 1 - tt.w
			top := "╭" + strings.Repeat("─", tt.w-2) + "╮"
			bottom := "╰" + strings.Repeat("─", tt.w-2) + "╯"

			for y, want := range map[int]string{27: top, 28: tt.mid, 29: bottom} {
				if got := cells(a, y, x, x+tt.w); got != want {
					t.Errorf("row %d cells %d..%d = %q, want %q", y, x, x+tt.w, got, want)
				}
			}

			// The cell at the right edge is the pane's own frame.
			if got := cells(a, 29, 99, 100); got != "╯" {
				t.Errorf("last cell = %q, want the pane's corner", got)
			}

			if got := cells(a, 28, x-1, x); got != " " {
				t.Errorf("cell left of the toast = %q, want the pane content", got)
			}
		})
	}
}

func TestToastGoesWithTheFirstKey(t *testing.T) {
	t.Parallel()
	a, procs := toastApp(t, 100, 30)
	a.notice = "boom"

	if !strings.Contains(screenRows(a)[28], "boom") {
		t.Fatal("no toast while a notice is set")
	}

	press(a, keyRune('x'))

	if a.notice != "" || strings.Contains(strings.Join(screenRows(a), "\n"), "boom") {
		t.Errorf("notice = %q, toast still drawn after a key", a.notice)
	}

	// Keys reach the process through the emulator's drain goroutine.
	eventually(t, "the key at the pane", func() bool { return procs["p2"].sent() == "x" })
}

func TestToastTakesNoClicks(t *testing.T) {
	t.Parallel()
	a, _ := toastApp(t, 100, 30)

	if err := a.dispatch(core.FocusPane{ID: "p1"}); err != nil {
		t.Fatal(err)
	}

	a.notice = "boom"

	// A cell of the toast's text row, over p2's content.
	click(a, 93, 28)

	if got := a.store.State().Tab().Focus; got != "p2" {
		t.Errorf("focus = %s after a click on the toast, want p2 under it", got)
	}

	if a.notice != "boom" {
		t.Errorf("notice = %q, want it kept by a click", a.notice)
	}
}

func TestToastSitsUnderWhichKey(t *testing.T) {
	t.Parallel()
	a, _ := toastApp(t, 140, 40)

	press(a, keyLeader)

	a.notice = "boom"
	a.Update(whichKeyMsg{seq: a.seq})

	if !a.popup {
		t.Fatal("which-key did not open")
	}

	if got := strings.Join(screenRows(a), "\n"); strings.Contains(got, "boom") {
		t.Errorf("the toast shows through the which-key popup:\n%s", got)
	}
}

func TestToastIsCutAtANarrowWidth(t *testing.T) {
	t.Parallel()
	a, _ := toastApp(t, 20, 12)
	a.notice = strings.Repeat("n", 40)

	// At most the body width minus 2: 18 cells, one cell from the right edge.
	want := "│ " + iconInfo + " " + strings.Repeat("n", 11) + "… │"
	if got := cells(a, 10, 1, 19); got != want {
		t.Errorf("toast row = %q, want %q", got, want)
	}

	for y, line := range screenRows(a) {
		if w := ansi.StringWidth(line); w != 20 {
			t.Errorf("row %d is %d cells, want 20", y, w)
		}
	}
}

func TestToastNeedsRoom(t *testing.T) {
	t.Parallel()

	for _, tt := range []struct{ w, h int }{{6, 12}, {40, 4}, {40, 2}, {3, 3}} {
		a, _ := toastApp(t, tt.w, tt.h)
		a.notice = "boom"

		for y, line := range screenRows(a) {
			if ansi.StringWidth(line) != tt.w || strings.Contains(line, "boom") {
				t.Errorf("%dx%d row %d = %q, want a full row without the toast", tt.w, tt.h, y, line)
			}
		}
	}
}

func TestToastGolden(t *testing.T) {
	t.Parallel()
	a, _ := toastApp(t, 60, 14)
	a.notice = "cannot start shell: no such file"

	golden.RequireEqual(t, []byte(a.View().Content))
}

// The tag and the prompt of copy mode live in the pane's own borders.

func TestCopyTag(t *testing.T) {
	t.Parallel()

	for _, tt := range []struct {
		name  string
		icons bool
		tag   string
	}{
		{"icon", true, " " + iconCopy + " COPY "},
		{"icons off", false, " COPY "},
	} {
		t.Run(tt.name, func(t *testing.T) {
			a, _, _ := copyApp(t)
			a.icons = tt.icons

			if strings.Contains(screenRows(a)[2], "COPY") {
				t.Fatal("COPY tag before copy mode")
			}

			enterCopy(a)

			if got := screenRows(a)[2]; !strings.HasSuffix(got, tt.tag+"─╮") {
				t.Errorf("top border = %q, want it to end with the COPY tag", got)
			}

			if bar := screenRows(a)[13]; strings.Contains(bar, "COPY") || strings.Contains(bar, "select") {
				t.Errorf("the bottom row = %q, want no legend", bar)
			}
		})
	}
}

func TestCopyTagColours(t *testing.T) {
	t.Parallel()
	ui := mochaUI(t)
	a, _, _ := copyApp(t)

	enterCopy(a)

	vt := lineVT(t, strings.Split(a.View().Content, "\n")[2], 60)

	// 60 wide: the tag ends before "─╮", so " <icon> COPY " is cells 49..57.
	assertColours(t, vt, 51, 0, iconCopy, ui.Info, ui.Bg, true)
	assertColours(t, vt, 53, 0, "C", ui.Info, ui.Bg, true)
}

func TestCopyTagIsLeftmostAndLastToGo(t *testing.T) {
	t.Parallel()
	a, _, _ := copyApp(t)
	a.icons = false

	enterCopy(a)

	for range 20 {
		keys(a, "k")
	}

	if got := screenRows(a)[2]; !strings.Contains(got, " COPY ─ ↑ ") || !strings.HasSuffix(got, " ─╮") {
		t.Errorf("top border = %q, want COPY left of the scroll tag", got)
	}

	// A narrow pane drops the scroll tag first.
	n, _ := framedApp(t, 12, 8, shellSpec("/w/a", manyLines(30)+"abcdef"))
	n.icons = false
	fakeClip(n)

	enterCopy(n)

	for range 20 {
		keys(n, "k")
	}

	if got := screenRows(n)[2]; !strings.Contains(got, " COPY ─╮") || strings.Contains(got, "↑") {
		t.Errorf("narrow top border = %q, want COPY kept and the scroll tag gone", got)
	}
}

func TestSearchPromptInTheBottomBorder(t *testing.T) {
	t.Parallel()

	for _, mark := range []string{"/", "?"} {
		t.Run(mark, func(t *testing.T) {
			ui := mochaUI(t)
			a, _, _ := copyApp(t)

			enterCopy(a)
			keys(a, mark)
			typeText(a, "ab")

			if got := screenRows(a)[13]; !strings.HasPrefix(got, "╰─ "+mark+"ab ─") || strings.Count(got, "─") < 50 {
				t.Errorf("bottom border = %q, want ╰─ %sab then the rule", got, mark)
			}

			if x, y, ok := cursorOf(a); !ok || x != 6 || y != 13 {
				t.Errorf("cursor = (%d,%d,%v), want after the query at (6,13)", x, y, ok)
			}

			vt := lineVT(t, strings.Split(a.View().Content, "\n")[13], 60)
			assertColours(t, vt, 3, 0, mark, ui.Info, ui.Bg, true)
			assertColours(t, vt, 4, 0, "a", ui.Fg, ui.Bg, false)

			keys(a, "esc")

			if got := screenRows(a)[13]; strings.Contains(got, mark+"ab") {
				t.Errorf("bottom border = %q after esc, want the prompt gone", got)
			}
		})
	}
}

func TestSearchPromptCap(t *testing.T) {
	t.Parallel()
	a, _, _ := copyApp(t)

	enterCopy(a)
	keys(a, "/")
	typeText(a, strings.Repeat("x", 40))

	want := "╰─ /" + strings.Repeat("x", 32) + " ─"
	if got := screenRows(a)[13]; !strings.HasPrefix(got, want) {
		t.Errorf("bottom border = %q, want it to start with %q", got, want)
	}

	if x, _, ok := cursorOf(a); !ok || x != 4+32 {
		t.Errorf("cursor x = %d (%v), want %d", x, ok, 4+32)
	}
}

func TestSearchPromptInANarrowPane(t *testing.T) {
	t.Parallel()
	a, _ := framedApp(t, 12, 8, shellSpec("/w/a", manyLines(30)))
	fakeClip(a)

	enterCopy(a)
	keys(a, "/")
	typeText(a, "abcdefghij")

	row := screenRows(a)[7]
	if ansi.StringWidth(row) != 12 || !strings.HasPrefix(row, "╰─ /abcde") || !strings.HasSuffix(row, "─╯") {
		t.Errorf("bottom border = %q, want the query cut inside the corners", row)
	}

	if x, y, ok := cursorOf(a); ok && (y != 7 || x >= 11) {
		t.Errorf("cursor = (%d,%d), want it inside the border row", x, y)
	}
}

// The sequence popup opens at once and lists the second keys.

func TestSequencePopup(t *testing.T) {
	t.Parallel()

	tests := []struct {
		first rune
		rows  []string // the bound keys, one per row
	}{
		{'>', []string{"1-9", "esc"}},
		{'.', []string{"1-9", "esc"}},
		{';', []string{"h j k l", "esc"}},
		{'g', []string{"l", "P", "o", "m", "esc"}},
	}

	for _, tt := range tests {
		t.Run(string(tt.first), func(t *testing.T) {
			a, _ := splitApp(t)

			press(a, keyLeader)

			if strings.Contains(strings.Join(screenRows(a), "\n"), "ctrl+space") {
				t.Fatal("the leader alone shows something before the delay")
			}

			press(a, keyRune(tt.first))

			rows := screenRows(a)
			n := len(rows)
			title := " " + iconLeader + " ctrl+space " + string(tt.first) + " "

			// Top border, one row per second key, bottom border on the last row.
			top := rows[n-2-len(tt.rows)]
			if !strings.Contains(top, "╭─"+title) {
				t.Errorf("popup top = %q, want the title %q", top, title)
			}

			for i, want := range tt.rows {
				if got := rows[n-1-len(tt.rows)+i]; !strings.Contains(got, "│ "+want+" ") {
					t.Errorf("popup row %d = %q, want the key %q", i, got, want)
				}
			}

			if got := cells(a, n-1, 138, 139); got != "╯" {
				t.Errorf("last row cell 138 = %q, want the popup's corner one cell from the edge", got)
			}

			// The delay passing keeps the sequence popup and adds no which-key.
			a.Update(whichKeyMsg{seq: a.seq})

			if a.popup || !a.sequenceOpen() {
				t.Errorf("popup = %v, sequence open = %v after the delay, want only the sequence popup", a.popup, a.sequenceOpen())
			}
		})
	}
}

func TestSequencePopupClosesOnTheSecondKey(t *testing.T) {
	t.Parallel()
	a, _ := splitApp(t)

	press(a, keyLeader, keyRune('>'))
	press(a, keyRune('2'))

	if a.sequenceOpen() {
		t.Error("the popup stays after the second key")
	}

	press(a, keyLeader, keyRune('>'), keyEsc)

	if a.sequenceOpen() {
		t.Error("the popup stays after esc")
	}
}

func TestSequencePopupGolden(t *testing.T) {
	t.Parallel()
	a, _ := splitApp(t)

	press(a, keyLeader, keyRune('>'))

	golden.RequireEqual(t, []byte(a.View().Content))
}

func TestCursorUnderToast(t *testing.T) {
	t.Parallel()
	// The toast ("boom", icon on) is 10 wide at columns 89-98, rows 27-29 of 100x30.
	// Row 26 col 42 of p2's content is screen (92, 28): under the toast's text row.
	a, _ := framedApp(t, 100, 30, shellSpec("/w/a", ""), shellSpec("/w/b", "\x1b[26;42H"))
	fakeClip(a)

	if cur := a.cursorView(); cur == nil || cur.X != 92 || cur.Y != 28 {
		t.Fatalf("cursor = %+v, want (92, 28) without a toast", cur)
	}

	a.notice = "boom"

	if cur := a.cursorView(); cur != nil {
		t.Errorf("cursor = %+v under the toast, want none", cur)
	}

	if a.View().Cursor != nil {
		t.Error("view carries a cursor under the toast")
	}

	press(a, keyRune('x'))

	if a.notice != "" {
		t.Fatalf("notice = %q after a key", a.notice)
	}

	if cur := a.cursorView(); cur == nil || cur.X != 92 || cur.Y != 28 {
		t.Errorf("cursor = %+v after the toast went, want (92, 28)", cur)
	}
}

func TestCursorOutsideToastStays(t *testing.T) {
	t.Parallel()
	a, _ := framedApp(t, 100, 30, shellSpec("/w/a", ""), shellSpec("/w/b", "\x1b[3;5Hx"))
	fakeClip(a)
	a.notice = "boom"

	if cur := a.cursorView(); cur == nil || cur.X != 56 || cur.Y != 5 {
		t.Errorf("cursor = %+v with a toast elsewhere, want (56, 5)", cur)
	}
}

func TestCopyCursorUnderToast(t *testing.T) {
	t.Parallel()
	a, _ := framedApp(t, 60, 14, shellSpec("/w/a", manyLines(30)+strings.Repeat("x", 55)))
	fakeClip(a)

	enterCopy(a)
	keys(a, "$")

	x, y, ok := cursorOf(a)
	if !ok || x < 39 || y < 11 {
		t.Fatalf("copy cursor = (%d,%d,%v), want one under the toast's box (columns 39-58, rows 11-13)", x, y, ok)
	}

	// A search miss raises the toast while copy mode stays on.
	keys(a, "/")
	typeText(a, "zzz")
	keys(a, "enter")

	if a.notice == "" {
		t.Fatal("no notice after a search miss")
	}

	if _, _, ok := cursorOf(a); ok {
		t.Error("copy cursor shows over the toast")
	}

	keys(a, "j")

	if a.notice != "" {
		t.Fatalf("notice = %q after a key", a.notice)
	}

	if _, _, ok := cursorOf(a); !ok {
		t.Error("copy cursor is not back after the toast went")
	}
}

func TestPromptCursorUnderToast(t *testing.T) {
	t.Parallel()
	a, _ := framedApp(t, 60, 14, shellSpec("/w/a", manyLines(30)))
	fakeClip(a)

	enterCopy(a)
	keys(a, "/")
	typeText(a, "ab")

	if _, _, ok := cursorOf(a); !ok {
		t.Fatal("no prompt cursor while a search is typed")
	}

	// The toast box covers the bottom border row and the prompt cursor sits on it.
	a.notice = strings.Repeat("n", 52)

	if _, _, ok := cursorOf(a); ok {
		t.Error("prompt cursor shows over the toast")
	}
}

func TestToastLevels(t *testing.T) {
	t.Parallel()
	ui := mochaUI(t)

	tests := []struct {
		name   string
		level  toastLevel
		colour theme.Hex
		icon   string
	}{
		{"notice", levelNotice, ui.Warning, iconInfo},
		{"success", levelSuccess, ui.Success, iconSuccess},
		{"error", levelError, ui.Error, iconAlert},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			a, _ := toastApp(t, 100, 30)
			a.toast(tt.level, "boom")

			vt := lineVT(t, strings.Split(a.View().Content, "\n")[28], 100)
			assertColours(t, vt, 89, 0, "│", tt.colour, ui.Panel, false)
			assertColours(t, vt, 91, 0, tt.icon, tt.colour, ui.Panel, false)
			assertColours(t, vt, 93, 0, "b", ui.Fg, ui.Panel, false)
			assertColours(t, vt, 98, 0, "│", tt.colour, ui.Panel, false)

			vt = lineVT(t, strings.Split(a.View().Content, "\n")[29], 100)
			assertColours(t, vt, 89, 0, "╰", tt.colour, ui.Panel, false)
			assertColours(t, vt, 94, 0, "─", tt.colour, ui.Panel, false)
		})
	}
}

func TestToastLevelBelongsToItsText(t *testing.T) {
	t.Parallel()
	a, _ := toastApp(t, 100, 30)

	a.toast(levelError, "boom")

	if got := a.noticeLevel(); got != levelError {
		t.Fatalf("level = %v right after an error toast, want error", got)
	}

	// A notice set the plain way, by any other code path, reads as a notice.
	a.notice = "later"

	if got := a.noticeLevel(); got != levelNotice {
		t.Errorf("level = %v for a notice assigned directly, want notice", got)
	}

	a.toast(levelSuccess, "done")
	press(a, keyRune('x'))

	if a.notice != "" || a.noticeLevel() != levelNotice {
		t.Errorf("notice = %q at level %v after a key, want none at the notice level", a.notice, a.noticeLevel())
	}
}

func TestGitSequencePopupGolden(t *testing.T) {
	t.Parallel()
	a, _ := splitApp(t)

	press(a, keyLeader, keyRune('g'))

	golden.RequireEqual(t, []byte(a.View().Content))
}

func TestGitSequencePopupText(t *testing.T) {
	t.Parallel()

	t.Run("icons=false", func(t *testing.T) {
		t.Parallel()
		a, _ := splitApp(t)
		a.icons = false

		press(a, keyLeader, keyRune('g'))

		golden.RequireEqual(t, []byte(ansi.Strip(a.View().Content)))
	})
}
