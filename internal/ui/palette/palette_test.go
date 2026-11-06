package palette

import (
	"fmt"
	"slices"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/charmbracelet/x/exp/golden"

	"github.com/mhersson/aim/internal/ui/theme"
)

func mustTheme(t *testing.T, name string) theme.Theme {
	t.Helper()

	th, err := theme.Builtin().ByName(name)
	if err != nil {
		t.Fatal(err)
	}

	return th
}

func testItems() []Item {
	return []Item{
		{ID: "a", Title: "Theme: Catppuccin Latte", Hint: "ctrl+t"},
		{ID: "b", Title: "Theme: Catppuccin Mocha"},
		{ID: "c", Title: "Quit AIM", Hint: "ctrl+q"},
		{ID: "d", Title: "Pick theme"},
	}
}

func newModel(t *testing.T) Model {
	t.Helper()

	return New("Commands", testItems(), mustTheme(t, "catppuccin-mocha"))
}

// collect runs cmd and returns every message it yields, unwrapping batches.
func collect(cmd tea.Cmd) []tea.Msg {
	if cmd == nil {
		return nil
	}

	msg := cmd()
	if b, ok := msg.(tea.BatchMsg); ok {
		var out []tea.Msg
		for _, c := range b {
			out = append(out, collect(c)...)
		}

		return out
	}

	if msg == nil {
		return nil
	}

	return []tea.Msg{msg}
}

func press(m Model, k tea.KeyPressMsg) (Model, []tea.Msg) {
	m, cmd := m.Update(k)

	return m, collect(cmd)
}

func typeText(m Model, s string) (Model, []tea.Msg) {
	var all []tea.Msg

	for _, r := range s {
		var msgs []tea.Msg

		m, msgs = press(m, tea.KeyPressMsg{Code: r, Text: string(r)})
		all = append(all, msgs...)
	}

	return m, all
}

var (
	keyDown  = tea.KeyPressMsg{Code: tea.KeyDown}
	keyEnter = tea.KeyPressMsg{Code: tea.KeyEnter}
	keyEsc   = tea.KeyPressMsg{Code: tea.KeyEscape}
	keyBksp  = tea.KeyPressMsg{Code: tea.KeyBackspace}
)

func highlights(msgs []tea.Msg) []string {
	var ids []string

	for _, m := range msgs {
		if h, ok := m.(HighlightMsg); ok {
			ids = append(ids, h.ID)
		}
	}

	return ids
}

func selectedID(t *testing.T, m Model) string {
	t.Helper()

	it, ok := m.Selected()
	if !ok {
		return ""
	}

	return it.ID
}

func TestFilter(t *testing.T) {
	tests := []struct {
		query string
		want  []string
	}{
		{"", []string{"a", "b", "c", "d"}},
		{"latte", []string{"a"}},
		{"zzz", nil},
	}
	for _, tt := range tests {
		t.Run(tt.query, func(t *testing.T) {
			m, _ := typeText(newModel(t), tt.query)
			if got := m.visibleIDs(); !slices.Equal(got, tt.want) {
				t.Fatalf("visible = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestNavigationKeys(t *testing.T) {
	tests := []struct {
		name string
		key  tea.KeyPressMsg
		want string
	}{
		{"down", tea.KeyPressMsg{Code: tea.KeyDown}, "b"},
		{"ctrl+n", tea.KeyPressMsg{Code: 'n', Mod: tea.ModCtrl}, "b"},
		{"tab", tea.KeyPressMsg{Code: tea.KeyTab}, "b"},
		{"up", tea.KeyPressMsg{Code: tea.KeyUp}, "d"},
		{"ctrl+p", tea.KeyPressMsg{Code: 'p', Mod: tea.ModCtrl}, "d"},
		{"shift+tab", tea.KeyPressMsg{Code: tea.KeyTab, Mod: tea.ModShift}, "d"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m, msgs := press(newModel(t), tt.key)
			if got := selectedID(t, m); got != tt.want {
				t.Fatalf("selected = %q, want %q", got, tt.want)
			}

			if got := highlights(msgs); !slices.Equal(got, []string{tt.want}) {
				t.Fatalf("highlights = %v, want [%s]", got, tt.want)
			}
		})
	}
}

func TestEnterAndEsc(t *testing.T) {
	m := newModel(t)

	_, msgs := press(m, keyEnter)
	if len(msgs) != 1 || msgs[0] != (ChosenMsg{ID: "a"}) {
		t.Fatalf("enter msgs = %v, want [ChosenMsg{a}]", msgs)
	}

	_, msgs = press(m, keyEsc)
	if len(msgs) != 1 || msgs[0] != (ClosedMsg{}) {
		t.Fatalf("esc msgs = %v, want [ClosedMsg{}]", msgs)
	}
}

func TestNoMatches(t *testing.T) {
	m, msgs := typeText(newModel(t), "zzz")
	if got := highlights(msgs); len(got) != 0 {
		t.Fatalf("typing to zero matches emitted highlights %v", got)
	}

	if _, ok := m.Selected(); ok {
		t.Fatal("something is selected with zero matches")
	}

	m2, cmd := m.Update(keyEnter)
	if cmd != nil {
		t.Fatalf("enter with no matches returned a cmd: %v", collect(cmd))
	}

	_, msgs = press(m2, keyDown)
	if len(msgs) != 0 {
		t.Fatalf("down with no matches emitted %v", msgs)
	}

	// Backspacing to an empty query restores the list with the first item
	// selected and highlighted.
	for range 3 {
		m, msgs = press(m, keyBksp)
	}

	if got := m.visibleIDs(); !slices.Equal(got, []string{"a", "b", "c", "d"}) {
		t.Fatalf("visible after clear = %v", got)
	}

	if got := selectedID(t, m); got != "a" {
		t.Fatalf("selected after clear = %q, want a", got)
	}

	if got := highlights(msgs); !slices.Equal(got, []string{"a"}) {
		t.Fatalf("highlights on clear = %v, want [a]", got)
	}
}

func TestNoHighlightWhenUnchanged(t *testing.T) {
	m, _ := typeText(newModel(t), "q")
	if got := selectedID(t, m); got != "c" {
		t.Fatalf("selected after q = %q, want c", got)
	}
	// Narrowing the query keeps c first; the selection does not change.
	m, msgs := typeText(m, "ui")
	if got := highlights(msgs); len(got) != 0 {
		t.Fatalf("highlights = %v, want none", got)
	}

	if got := selectedID(t, m); got != "c" {
		t.Fatalf("selected = %q, want c", got)
	}
}

func TestQueryResetsSelection(t *testing.T) {
	m, _ := press(newModel(t), keyDown)
	if got := selectedID(t, m); got != "b" {
		t.Fatalf("selected after down = %q, want b", got)
	}

	m, msgs := typeText(m, "l")
	if first := m.visibleIDs()[0]; selectedID(t, m) != first {
		t.Fatalf("selected = %q, want first match %q", selectedID(t, m), first)
	}

	if got := highlights(msgs); len(got) != 1 || got[0] != m.visibleIDs()[0] {
		t.Fatalf("highlights = %v, want first match", got)
	}

	m, _ = press(m, keyBksp)
	if got := m.visibleIDs(); !slices.Equal(got, []string{"a", "b", "c", "d"}) {
		t.Fatalf("visible after backspace = %v", got)
	}

	if got := selectedID(t, m); got != "a" {
		t.Fatalf("selected after backspace = %q, want a", got)
	}
}

func TestSelect(t *testing.T) {
	m := newModel(t)
	m.Select("b")

	if got := selectedID(t, m); got != "b" {
		t.Fatalf("selected = %q, want b", got)
	}

	m.Select("zz")

	if got := selectedID(t, m); got != "b" {
		t.Fatalf("selected after unknown id = %q, want b", got)
	}
}

func TestScroll(t *testing.T) {
	items := make([]Item, 15)
	for i := range items {
		items[i] = Item{ID: fmt.Sprintf("id%02d", i), Title: fmt.Sprintf("Item %02d", i)}
	}

	m := New("Many", items, mustTheme(t, "catppuccin-mocha"))
	for range 12 {
		m, _ = press(m, keyDown)
	}

	view := ansi.Strip(m.View(60))
	if !strings.Contains(view, "Item 12") {
		t.Fatalf("selected item not visible:\n%s", view)
	}

	if strings.Contains(view, "Item 00") {
		t.Fatalf("view did not scroll:\n%s", view)
	}

	if rows := strings.Count(view, "Item "); rows != MaxRows {
		t.Fatalf("visible item rows = %d, want %d", rows, MaxRows)
	}
}

func TestViewWidth(t *testing.T) {
	queries := []string{"", "zzz"}
	for _, w := range []int{60, 12, 2, 1, 0} {
		for _, q := range queries {
			t.Run(fmt.Sprintf("w%d/%q", w, q), func(t *testing.T) {
				m, _ := typeText(newModel(t), q)

				view := m.View(w)
				for i, line := range strings.Split(view, "\n") {
					if got := lipgloss.Width(line); got > w {
						t.Fatalf("line %d width %d > %d: %q", i, got, w, line)
					}
				}

				if w >= 2 {
					for i, line := range strings.Split(view, "\n") {
						if got := lipgloss.Width(line); got != w {
							t.Fatalf("line %d width %d, want exactly %d", i, got, w)
						}
					}
				}
			})
		}
	}
}

func TestSetTheme(t *testing.T) {
	m := newModel(t)
	before := m.View(40)
	m.SetTheme(mustTheme(t, "catppuccin-latte"))

	if after := m.View(40); after == before {
		t.Fatal("view did not change with the theme")
	}
}

func TestPaletteGolden(t *testing.T) {
	m, _ := typeText(newModel(t), "th")
	golden.RequireEqual(t, []byte(m.View(60)))
}

func TestPaletteIconsGolden(t *testing.T) {
	m := New("\uF4B5 Commands", iconItems(), mustTheme(t, "catppuccin-mocha"))
	m, _ = press(m, keyDown)
	golden.RequireEqual(t, []byte(m.View(48)))
}

func iconItems() []Item {
	return []Item{
		{ID: "a", Title: "Zoom pane", Hint: "ctrl+space z", Icon: "\U000F0293"},
		{ID: "b", Title: "Close pane", Hint: "ctrl+space x", Icon: "\U000F0156"},
		{ID: "c", Title: "Quit", Icon: "\U000F0206"},
	}
}

func TestFooterWithNoMatches(t *testing.T) {
	m, _ := typeText(newModel(t), "zzz")

	if _, ok := m.Selected(); ok || len(m.matches) != 0 {
		t.Errorf("Selected ok = %v, matches = %d, want none", ok, len(m.matches))
	}

	if !strings.Contains(ansi.Strip(m.View(40)), " 0 of 0 ") {
		t.Errorf("footer lacks the count 0 of 0:\n%s", ansi.Strip(m.View(40)))
	}

	if n := len(strings.Split(ansi.Strip(m.View(40)), "\n")); n != 3+1+3 {
		t.Errorf("no-match view has %d lines, want 7", n)
	}
}

func TestMultibyteTitle(t *testing.T) {
	th := mustTheme(t, "catppuccin-mocha")

	m := New("Commands", []Item{
		{ID: "a", Title: "Theme: Catppuccin Latte"},
		{ID: "b", Title: "Theme: Catppuccin Frappé"},
	}, th)
	for _, r := range "frap" {
		m, _ = press(m, tea.KeyPressMsg{Code: r, Text: string(r)})
	}

	const width = 40

	view := m.View(width)
	if !strings.Contains(ansi.Strip(view), "Frappé") {
		t.Errorf("stripped view lacks Frappé:\n%s", ansi.Strip(view))
	}

	for i, l := range strings.Split(view, "\n") {
		if got := lipgloss.Width(l); got > width {
			t.Errorf("line %d width = %d, want at most %d", i, got, width)
		}
	}

	hit := lipgloss.NewStyle().Background(th.UI.Selection).Foreground(th.UI.Match).Bold(true)
	if want := hit.Render("Frap"); !strings.Contains(view, want) {
		t.Errorf("view lacks the highlighted run %q:\n%q", want, view)
	}
}

func TestFooterEnterLabel(t *testing.T) {
	if view := ansi.Strip(newModel(t).View(60)); strings.Contains(view, "apply") {
		t.Errorf("default footer names apply:\n%s", view)
	}

	if view := ansi.Strip(newModel(t).WithEnterLabel("apply").View(60)); !strings.Contains(view, "apply") {
		t.Errorf("footer lacks apply:\n%s", view)
	}
}

func TestBox(t *testing.T) {
	th := mustTheme(t, "catppuccin-mocha")

	for _, w := range []int{2, 3, 5, 40} {
		box := NewBox(th, w)

		pieces := map[string]string{
			"row":            box.Row("some content that is longer than a small box", box.On(th.UI.Fg)),
			"short row":      box.Row("", box.On(th.UI.Fg)),
			"top":            box.Top("A long title for a narrow box"),
			"divider":        box.Divider(),
			"bottom":         box.Bottom(" 3 of 9 "),
			"bottom no tail": box.Bottom(""),
		}
		for name, p := range pieces {
			if got := lipgloss.Width(p); got != w {
				t.Errorf("width %d: %s is %d cells wide: %q", w, name, got, ansi.Strip(p))
			}
		}
	}

	// A count that does not fit is left out, never cut.
	if got := ansi.Strip(NewBox(th, 8).Bottom(" 3 of 9 ")); got != "\u2570"+strings.Repeat("\u2500", 6)+"\u256f" {
		t.Errorf("bottom in a narrow box = %q, want no count", got)
	}
}

func TestDividerJoin(t *testing.T) {
	t.Parallel()

	box := NewBox(mustTheme(t, "catppuccin-mocha"), 10)

	tests := []struct {
		name string
		col  int
		want string
	}{
		{"a junction in the row", 4, "├───┬────┤"},
		{"the first cell after the corner", 1, "├┬───────┤"},
		{"the last cell before the corner", 8, "├───────┬┤"},
		{"a column outside the row is a plain divider", 9, "├────────┤"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if got := ansi.Strip(box.DividerJoin(tt.col, "┬")); got != tt.want {
				t.Errorf("DividerJoin(%d) = %q, want %q", tt.col, got, tt.want)
			}

			if got := lipgloss.Width(box.DividerJoin(tt.col, "┬")); got != 10 {
				t.Errorf("width = %d, want 10", got)
			}
		})
	}

	if got := ansi.Strip(box.Bar()); got != "│" {
		t.Errorf("Bar = %q, want one vertical line", got)
	}
}
