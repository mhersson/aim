package ui

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
)

func taKey(s string) tea.KeyPressMsg {
	switch s {
	case "enter":
		return tea.KeyPressMsg{Code: tea.KeyEnter}
	case "backspace":
		return tea.KeyPressMsg{Code: tea.KeyBackspace}
	case "delete":
		return tea.KeyPressMsg{Code: tea.KeyDelete}
	case "left":
		return tea.KeyPressMsg{Code: tea.KeyLeft}
	case "right":
		return tea.KeyPressMsg{Code: tea.KeyRight}
	case "up":
		return tea.KeyPressMsg{Code: tea.KeyUp}
	case "down":
		return tea.KeyPressMsg{Code: tea.KeyDown}
	case "home":
		return tea.KeyPressMsg{Code: tea.KeyHome}
	case "end":
		return tea.KeyPressMsg{Code: tea.KeyEnd}
	case "pgup":
		return tea.KeyPressMsg{Code: tea.KeyPgUp}
	case "pgdown":
		return tea.KeyPressMsg{Code: tea.KeyPgDown}
	}

	r := []rune(s)[0]

	return tea.KeyPressMsg{Code: r, Text: s}
}

func TestTextAreaKeys(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		start       string
		keys        []string
		want        string
		row, col    int
		wantChanged bool // the last key
	}{
		{"typing", "", []string{"a", "b"}, "ab", 0, 2, true},
		{"enter splits", "abcd", []string{"left", "left", "enter"}, "ab\ncd", 1, 0, true},
		{"backspace joins", "ab\ncd", []string{"up", "down", "home", "backspace"}, "abcd", 0, 2, true},
		{"backspace at start", "ab", []string{"home", "backspace"}, "ab", 0, 0, false},
		{"delete joins", "ab\ncd", []string{"up", "end", "delete"}, "abcd", 0, 2, true},
		{"delete at end", "ab", []string{"delete"}, "ab", 0, 2, false},
		{"up clamps the column", "abcdef\nab", []string{"up"}, "abcdef\nab", 0, 2, true},
		{"down clamps the column", "abcdef\nab", []string{"up", "end", "down"}, "abcdef\nab", 1, 2, true},
		{"left wraps", "ab\ncd", []string{"home", "left"}, "ab\ncd", 0, 2, true},
		{"right wraps", "ab\ncd", []string{"up", "end", "right"}, "ab\ncd", 1, 0, true},
		{"home and end", "abc", []string{"home"}, "abc", 0, 0, true},
		{"end", "abc", []string{"home", "end"}, "abc", 0, 3, true},
		{"up at top", "abc", []string{"up"}, "abc", 0, 3, false},
		{"pgup", strings.Repeat("x\n", 25) + "x", []string{"pgup"}, strings.Repeat("x\n", 25) + "x", 15, 1, true},
		{"pgdown stops at the end", "x\ny\nz", []string{"up", "up", "pgdown"}, "x\ny\nz", 2, 1, true},
		{"text that names a key is text", "", []string{"text:end"}, "end", 0, 3, true},
		{"unknown key", "ab", []string{"ctrl"}, "ab", 0, 2, false},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			ta := newTextArea(tc.start, 0)
			changed := false

			for _, k := range tc.keys {
				msg := taKey(k)
				if k == "text:end" {
					msg = tea.KeyPressMsg{Code: 'e', Text: "end"}
				}

				if k == "ctrl" {
					msg = tea.KeyPressMsg{Code: 'a', Mod: tea.ModCtrl}
				}

				changed = ta.Key(msg)
			}

			if got := ta.Value(); got != tc.want {
				t.Errorf("Value = %q, want %q", got, tc.want)
			}

			if ta.row != tc.row || ta.col != tc.col {
				t.Errorf("cursor = (%d, %d), want (%d, %d)", ta.row, ta.col, tc.row, tc.col)
			}

			if changed != tc.wantChanged {
				t.Errorf("last Key changed = %v, want %v", changed, tc.wantChanged)
			}
		})
	}
}

func TestTextAreaPaste(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name, start, paste, want string
		max                      int
	}{
		{"keeps newlines", "", "a\nb", "a\nb", 0},
		{"CRLF and CR become LF", "", "a\r\nb\rc", "a\nb\nc", 0},
		{"inserts at the cursor", "ad", "bc", "adbc", 0},
		{"cut at the cap", "ab", "cdef", "abcd", 4},
		{"newlines count", "", "a\nb\nc", "a\nb\n", 4},
		{"full takes nothing", "abcd", "e", "abcd", 4},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			ta := newTextArea(tc.start, tc.max)
			ta.Paste(tc.paste)

			if got := ta.Value(); got != tc.want {
				t.Errorf("Value = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestTextAreaCapOnTypingAndStart(t *testing.T) {
	t.Parallel()

	ta := newTextArea("abc", 3)

	if ta.Key(taKey("d")) || ta.Key(taKey("enter")) {
		t.Error("a key at the cap reported a change")
	}

	if got := newTextArea("éééé", 3); len([]rune(got.Value())) != 3 {
		t.Errorf("start text holds %d characters, want 3", len([]rune(got.Value())))
	}
}

func TestTextAreaView(t *testing.T) {
	t.Parallel()

	plain := lipgloss.NewStyle()

	t.Run("scrolls to keep the cursor row", func(t *testing.T) {
		t.Parallel()

		ta := newTextArea("0\n1\n2\n3\n4", 0)
		lines, row, _ := ta.View(10, 3, plain)

		if got := strings.Join(lines, ","); got != "2,3,4" || row != 2 {
			t.Errorf("lines = %q, row = %d; want 2,3,4 and 2", got, row)
		}

		for range 4 {
			ta.Key(taKey("up"))
		}

		lines, row, _ = ta.View(10, 3, plain)

		if got := strings.Join(lines, ","); got != "0,1,2" || row != 0 {
			t.Errorf("lines = %q, row = %d; want 0,1,2 and 0", got, row)
		}
	})

	t.Run("cuts other lines and scrolls the cursor's", func(t *testing.T) {
		t.Parallel()

		ta := newTextArea("abcdefghij\nklmnopqrst", 0)
		ta.Key(taKey("up"))
		ta.Key(taKey("end"))
		lines, row, col := ta.View(5, 2, plain)

		if row != 0 || col != 4 {
			t.Errorf("cursor = (%d, %d), want (0, 4)", row, col)
		}

		if got := ansi.Strip(lines[0]); got != "ghij" {
			t.Errorf("cursor line = %q", got)
		}

		if got := ansi.Strip(lines[1]); got != "klmn…" {
			t.Errorf("other line = %q, want klmn…", got)
		}

		for _, l := range lines {
			if ansi.StringWidth(l) > 5 {
				t.Errorf("line %q wider than 5", l)
			}
		}
	})

	t.Run("cleans invisible characters and counts visible cells", func(t *testing.T) {
		t.Parallel()

		ta := newTextArea("a\x1b\u200bb世c", 0)
		ta.Key(taKey("left"))
		lines, _, col := ta.View(20, 1, plain)

		if got := ansi.Strip(lines[0]); got != "ab世c" {
			t.Errorf("line = %q, want ab世c", got)
		}

		if col != 4 {
			t.Errorf("col = %d, want 4 (a, b, the wide one)", col)
		}
	})

	t.Run("a very long line keeps the cursor at its end", func(t *testing.T) {
		t.Parallel()

		ta := newTextArea(strings.Repeat("x", 65536), 65536)
		lines, row, col := ta.View(10, 1, plain)

		if row != 0 || col != 9 || ansi.Strip(lines[0]) != strings.Repeat("x", 9) {
			t.Errorf("row, col, line = %d, %d, %q; want 0, 9, nine x", row, col, ansi.Strip(lines[0]))
		}
	})
}
