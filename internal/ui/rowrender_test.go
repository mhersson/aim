package ui

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"

	"github.com/mhersson/aim/internal/term"
)

type discard struct{}

func (discard) Write(p []byte) (int, error) { return len(p), nil }

func newTestVT(t *testing.T, cols, rows int) term.VT {
	t.Helper()

	v := term.NewVT(cols, rows, discard{})

	t.Cleanup(func() { _ = v.Close() })

	return v
}

func TestRenderRow(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		input  string
		resize int
		w      int
		text   string
		styled bool
		link   bool
	}{
		{name: "plain", input: "hello", w: 10, text: "hello"},
		{name: "colour", input: "\x1b[31mred\x1b[0m", w: 10, text: "red", styled: true},
		{name: "link", input: "\x1b]8;;http://example.com\x07link\x1b]8;;\x07", w: 10, text: "link", link: true},
		{name: "wide at edge", input: "0123456789中", resize: 11, w: 11, text: "0123456789"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			cols := tc.w
			if tc.resize > 0 {
				cols = 12
			}

			v := newTestVT(t, cols, 3)

			if _, err := v.Write([]byte(tc.input)); err != nil {
				t.Fatal(err)
			}

			if tc.resize > 0 {
				v.Resize(tc.resize, 3)
			}

			row := renderRow(v, 0, tc.w)

			if got := ansi.StringWidth(row); got != tc.w {
				t.Fatalf("width = %d, want %d: %q", got, tc.w, row)
			}

			want := tc.text + strings.Repeat(" ", tc.w-len(tc.text))
			if got := ansi.Strip(row); got != want {
				t.Fatalf("text = %q, want %q", got, want)
			}

			// Padding follows the reset; only unstyled blanks come after it.
			body := strings.TrimRight(row, " ")

			if tc.styled && !strings.HasSuffix(body, ansi.ResetStyle) {
				t.Fatalf("styled row not reset: %q", row)
			}

			if tc.link && !strings.HasSuffix(body, ansi.ResetHyperlink()) {
				t.Fatalf("link row not closed: %q", row)
			}

			if tc.link && !strings.Contains(row, "http://example.com") {
				t.Fatalf("link missing: %q", row)
			}
		})
	}
}
