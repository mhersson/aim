package ui

import (
	"strings"
	"testing"

	"github.com/mhersson/aim/internal/md"
)

func splitLines(s string) []string {
	return strings.Split(strings.TrimSuffix(s, "\n"), "\n")
}

func TestChangeMarks(t *testing.T) {
	t.Parallel()

	const (
		add = markAdded
		rem = markRemoved
	)

	// want maps a piece of a layout line's text to its mark; every other line
	// must carry none.
	tests := []struct {
		name     string
		old, new string
		want     map[string]changeMark
	}{
		{
			"an added paragraph", "# T\n\nfirst\n", "# T\n\nfirst\n\nsecond one\nsecond two\n",
			map[string]changeMark{"second one": add, "second two": add},
		},
		{
			"a changed list item", "- one\n- two\n- three\n", "- one\n- 2\n- three\n",
			map[string]changeMark{"2": add},
		},
		{
			"a removed paragraph", "A\n\nB\n\nC\n", "A\n\nC\n",
			map[string]changeMark{"C": rem},
		},
		{
			"lines removed inside a paragraph", "A\n\nalpha beta gamma delta\nepsilon\nzeta eta\n\nC\n", "A\n\nalpha beta gamma delta\nzeta eta\n\nC\n",
			map[string]changeMark{"alpha beta": rem},
		},
		{"removal after the last block", "A\n\nB\n", "A\n", nil},
		{
			"added and removed in one block", "alpha beta gamma delta\nepsilon\n", "alpha beta gamma delta\nnewline here\n",
			map[string]changeMark{"alpha beta": add, "gamma delta": add, "newline here": add},
		},
		{"a blank line added between paragraphs", "A\nB\n", "A\n\nB\n", nil},
		{"a blank line removed between paragraphs", "A\n\n\nB\n", "A\n\nB\n", nil},
		{
			"a blank line and a line removed", "A\n\n\nB\n\nC\n", "A\n\nC\n",
			map[string]changeMark{"C": rem},
		},
		{
			"a blank line removed inside a code block", "```\nx\n\ny\n```\n\nafter\n", "```\nx\ny\n```\n\nafter\n",
			map[string]changeMark{"╭": rem},
		},
		{"nothing changed", "A\n\nB\n", "A\n\nB\n", nil},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			lay := md.Parse([]byte(tc.new), md.ParseOptions{}).Layout(md.LayoutOptions{Width: 12})
			marks := changeMarks(lay.Source, newFullText(splitLines(tc.old), splitLines(tc.new), nil))

			if len(marks) != len(lay.Lines) {
				t.Fatalf("%d marks for %d layout lines", len(marks), len(lay.Lines))
			}

			for i, text := range lay.Plain() {
				want := markNone

				for piece, m := range tc.want {
					if strings.Contains(text, piece) {
						want = m
					}
				}

				if marks[i] != want {
					t.Errorf("line %d %q: mark %d, want %d (layout %q)", i, text, marks[i], want, lay.Plain())
				}
			}
		})
	}
}
