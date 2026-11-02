package md

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
)

// widthSamples are clusters whose width differs from the sum of their runes.
var widthSamples = map[string]string{
	"flag":    "\U0001F1F3\U0001F1F4",
	"keycap":  "1\uFE0F\u20E3",
	"zwj":     "\U0001F469\u200D\U0001F4BB",
	"cjk":     "\u4E2D",
	"combine": "e\u0301",
}

// assertBordersAligned checks that the last │ of every framed row sits in one
// cell column, measured by grapheme cluster.
func assertBordersAligned(t *testing.T, l Layout, rows []string) {
	t.Helper()

	want := -1

	for i, p := range rows {
		before, _, ok := strings.CutLast(p, "│")
		if !ok {
			continue
		}

		col := ansi.StringWidth(before)
		if want < 0 {
			want = col
		} else if col != want {
			t.Errorf("row %d: right border at cell %d, want %d: %q", i, col, want, p)
		}
	}
}

// clusterSamples are the widthSamples whose width comes from joining runes;
// TestCutCells covers the other two.
var clusterSamples = map[string]string{
	"flag":   widthSamples["flag"],
	"keycap": widthSamples["keycap"],
	"zwj":    widthSamples["zwj"],
}

func TestCodeWidthByCluster(t *testing.T) {
	t.Parallel()

	for name, sample := range clusterSamples {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			src := "```\nplain line\nab" + sample + "cd\n" + sample + sample + "\nplain\n```\n"

			for _, w := range []int{14, 30} {
				l := codeLayout(src, w, nil)
				assertSafe(t, l, w)
				assertBordersAligned(t, l, l.Plain()[1:len(l.Lines)-1])
			}
		})
	}
}

func TestTableWidthByCluster(t *testing.T) {
	t.Parallel()

	for name, sample := range clusterSamples {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			src := "| a | b |\n|---|---|\n| plain | x |\n| ab" + sample + "cd | " + sample + " |\n| zz | yyyy |\n"

			for _, w := range []int{14, 30} {
				l := layoutOf(src, w, "")
				assertSafe(t, l, w)
				assertBordersAligned(t, l, l.Plain())

				for i, p := range l.Plain() {
					if got := ansi.StringWidth(p); got != ansi.StringWidth(l.Plain()[0]) {
						t.Errorf("width %d row %d is %d cells, frame is %d: %q", w, i, got, ansi.StringWidth(l.Plain()[0]), p)
					}
				}
			}
		})
	}
}

func TestTextWidthMatchesStringWidth(t *testing.T) {
	t.Parallel()

	for name, s := range widthSamples {
		if got, want := textWidth("a"+s+"b"), ansi.StringWidth("a"+s+"b"); got != want {
			t.Errorf("%s: textWidth = %d, StringWidth = %d", name, got, want)
		}
	}
}

// splitSamples are clusters cut in two by a style change.
var splitSamples = map[string]string{
	"flag":   "**\U0001F1F3**\U0001F1F4",
	"keycap": "**1**\uFE0F\u20E3",
}

func TestTableSplitClusterAligned(t *testing.T) {
	t.Parallel()

	for name, cell := range splitSamples {
		t.Run(name, func(t *testing.T) {
			src := "| a | b |\n|---|---|\n| plain | x |\n| ab " + cell + " cd | " + cell + " |\n| zz | yyyy |\n"

			for _, w := range []int{14, 30, 60} {
				l := layoutOf(src, w, "")
				assertSafe(t, l, w)
				assertBordersAligned(t, l, l.Plain())

				for i, p := range l.Plain() {
					if got, want := ansi.StringWidth(p), ansi.StringWidth(l.Plain()[0]); got != want {
						t.Errorf("width %d row %d is %d cells, frame is %d: %q", w, i, got, want, p)
					}
				}
			}
		})
	}
}

func TestProseSplitClusterFits(t *testing.T) {
	t.Parallel()

	for name, cell := range splitSamples {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			for _, w := range []int{1, 2, 3, 5, 20} {
				l := layoutOf("ab "+cell+" cd "+cell+" ef", w, "")
				assertSafe(t, l, w)
			}
		})
	}
}

func TestSplitClusterTakesFirstStyle(t *testing.T) {
	t.Parallel()

	for name, cell := range splitSamples {
		t.Run(name, func(t *testing.T) {
			l := layoutOf("x "+cell+" y", 40, "")

			if len(l.Lines) != 1 {
				t.Fatalf("got %d lines: %q", len(l.Lines), l.Plain())
			}

			var cluster *Run

			for i, r := range l.Lines[0] {
				if r.Bold && r.Text != "" {
					cluster = &l.Lines[0][i]
				}
			}

			if cluster == nil {
				t.Fatalf("no bold run in %+v", l.Lines[0])
			}

			// Prose drops the variation selector, so the keycap is "1" plus U+20E3.
			want := map[string]string{"flag": widthSamples["flag"], "keycap": "1\u20E3"}[name]
			if cluster.Text != want {
				t.Errorf("bold run = %q, want the whole cluster %q", cluster.Text, want)
			}

			if got := ansi.StringWidth(l.Plain()[0]); got != lineWidth(l.Lines[0]) {
				t.Errorf("lineWidth = %d, text is %d cells", lineWidth(l.Lines[0]), got)
			}
		})
	}
}

func TestCodeLabelCutByCluster(t *testing.T) {
	t.Parallel()

	for name, sample := range clusterSamples {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			src := "```" + strings.Repeat(sample+"x", 30) + "\ncode\n```\n"

			for _, w := range []int{14, 30} {
				l := codeLayout(src, w, nil)
				assertSafe(t, l, w)
				assertBordersAligned(t, l, l.Plain())
			}
		})
	}
}

func TestCutCells(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		s    string
		n    int
		want string
	}{
		{"fits", "abc", 3, "abc"},
		{"ascii", "abcdef", 4, "abcd"},
		{"zero cells", "abc", 0, ""},
		{"wide character at the cut", "a\u4E2Db", 2, "a"},
		{"wide character fits", "a\u4E2Db", 3, "a\u4E2D"},
		{"keycap at the cut", "a1\uFE0F\u20E3b", 2, "a"},
		{"keycap fits", "a1\uFE0F\u20E3b", 3, "a1\uFE0F\u20E3"},
		{"keycap alone in one cell", "1\uFE0F\u20E3x", 1, ""},
		{"flag at the cut", "a\U0001F1F3\U0001F1F4", 2, "a"},
		{"combining mark stays with its base", "e\u0301e\u0301", 1, "e\u0301"},
		{"zero-width join stays in its cluster", "\U0001F469\u200D\U0001F4BBx", 1, ""},
		{"empty", "", 5, ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := cutCells(tt.s, tt.n); got != tt.want {
				t.Errorf("cutCells(%q, %d) = %q, want %q", tt.s, tt.n, got, tt.want)
			}
		})
	}

	// For every sample and every n the cut fits and is the longest prefix that does.
	for name, sample := range widthSamples {
		s := "ab" + sample + "cd" + sample
		for n := 0; n <= textWidth(s)+1; n++ {
			cut := cutCells(s, n)
			if got := textWidth(cut); got > n {
				t.Errorf("%s: cutCells(%q, %d) is %d cells wide", name, s, n, got)
			}

			if rest := s[len(cut):]; rest != "" {
				if _, w := nextCluster(rest); textWidth(cut)+w <= n {
					t.Errorf("%s: cutCells(%q, %d) = %q stops before a cluster that fits", name, s, n, cut)
				}
			}
		}
	}
}

func TestFitMergesTheEllipsis(t *testing.T) {
	t.Parallel()

	st := Run{Role: RoleTableFrame, Link: -1}

	tests := []struct {
		name string
		line Line
		w    int
		want string
	}{
		{"cut inside a run", Line{{Text: "abcdef", Role: st.Role, Link: -1}}, 4, "[abc…]"},
		{"cut between runs of one style", Line{{Text: "ab", Role: st.Role, Link: -1}, {Text: "cdef", Role: RoleText, Link: -1}}, 3, "[ab][…]"},
		{"cut run of another style", Line{{Text: "abc", Role: st.Role, Link: -1}, {Text: "defg", Role: RoleText, Link: -1}}, 6, "[abc][de…]"},
		{"nothing of the run fits", Line{{Text: "ab", Role: st.Role, Link: -1}, {Text: "\u4E2D", Role: st.Role, Link: -1}}, 3, "[ab…]"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var b strings.Builder
			for _, r := range fit(tt.line, tt.w) {
				b.WriteString("[" + r.Text + "]")
			}

			if b.String() != tt.want {
				t.Errorf("fit = %s, want %s", b.String(), tt.want)
			}
		})
	}
}
