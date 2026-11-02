package md

import (
	"slices"
	"strings"
	"testing"
)

// pathological is an input at a size where a superlinear step in parse or
// layout would show. bench_test.go times these inputs and one more at the size
// limit.
type pathological struct {
	name     string
	src      string
	minLines int
	contains string // text some line must hold
}

func pathologicalInputs() []pathological {
	words := strings.Repeat("lorem ipsum dolor sit amet ", (4<<20)/27)

	var nested strings.Builder
	for range 3000 {
		nested.WriteString("<details open>\n<summary>s</summary>\n\n")
	}

	return []pathological{
		{"open brackets", strings.Repeat("[", 100_000), 1000, ""},
		{"quote markers", strings.Repeat(">", 10_000), 0, ""}, // empty nested quotes draw nothing
		{"quote markers with text", strings.Repeat(">", 10_000) + " deep", 1, "deep"},
		{"list items", strings.Repeat("- a\n", 100_000), 100_000, ""},
		{"one 4 MiB paragraph", words, 40_000, ""},
		{"3000 nested details", nested.String(), 3000, ""},
		{"one cluster across 32000 styled segments", "e" + strings.Repeat("**\u0301**~~\u0302~~", 16_000), 1, ""},
	}
}

// fourMiBCorpus repeats the committed documents to just under MaxBytes.
func fourMiBCorpus(t *testing.T) []byte {
	t.Helper()

	var b []byte

	docs := corpusDocs(t)
	docs = docs[:len(docs)-1] // the hostile document leaves a fold unclosed and would swallow the rest

	for i := 0; ; i++ {
		next := append(append([]byte{}, docs[i%len(docs)].src...), '\n', '\n')
		if len(b)+len(next) >= MaxBytes {
			break
		}

		b = append(b, next...)
	}

	return b
}

// parseUncut parses src and fails the test if a document within MaxBytes was
// cut.
func parseUncut(t *testing.T, src []byte) *Doc {
	t.Helper()

	d := Parse(src, ParseOptions{})
	if len(src) <= MaxBytes && d.Cut() {
		t.Error("a document within MaxBytes was cut")
	}

	return d
}

func layoutPathological(d *Doc) Layout {
	return d.Layout(LayoutOptions{Width: 100, Icons: true})
}

func TestPathological(t *testing.T) {
	t.Parallel()

	for _, tt := range pathologicalInputs() {
		t.Run(tt.name, func(t *testing.T) {
			l := layoutPathological(parseUncut(t, []byte(tt.src)))

			if len(l.Lines) < tt.minLines || (tt.minLines == 0 && len(l.Lines) != 0) {
				t.Errorf("layout has %d lines, want at least %d", len(l.Lines), tt.minLines)
			}

			if tt.contains != "" && !slices.ContainsFunc(l.Plain(), func(s string) bool { return strings.Contains(s, tt.contains) }) {
				t.Errorf("no line holds %q", tt.contains)
			}
		})
	}
}

func TestFourMiB(t *testing.T) {
	t.Parallel()

	l := layoutPathological(parseUncut(t, fourMiBCorpus(t)))

	if len(l.Lines) < 100_000 {
		t.Errorf("layout has %d lines, want at least 100000", len(l.Lines))
	}
}
