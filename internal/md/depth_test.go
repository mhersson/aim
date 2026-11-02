package md

import (
	"fmt"
	"runtime/debug"
	"strings"
	"testing"

	"github.com/yuin/goldmark/v2/ast"
)

// nestLines wraps inner in one container of the given kind.
func nestLines(kind string, id int, inner []string) []string {
	prefixed := func(first, rest string) []string {
		out := make([]string, len(inner))

		for i, line := range inner {
			p := rest
			if i == 0 {
				p = first
			}

			if line == "" {
				out[i] = strings.TrimRight(p, " ")
			} else {
				out[i] = p + line
			}
		}

		return out
	}

	switch kind {
	case "quote":
		return prefixed("> ", "> ")
	case "alert":
		return append([]string{"> [!NOTE]"}, prefixed("> ", "> ")...)
	case "list":
		return prefixed("- ", "  ")
	case "ordered":
		return prefixed("7. ", "   ")
	case "task":
		return append([]string{"- [x] done", ""}, prefixed("  ", "  ")...)
	default:
		out := []string{"<details open>", fmt.Sprintf("<summary>F%d</summary>", id), ""}
		out = append(out, inner...)

		return append(out, "", "</details>")
	}
}

// mixedNesting is a document of depth containers cycling through every kind,
// around a body of blocks.
func mixedNesting(depth int) string {
	kinds := []string{"quote", "list", "alert", "fold", "ordered", "task"}
	lines := []string{
		"first paragraph", "", "second `code` and [link](https://e.com/x)", "",
		"```go", "x := 1", "```", "", "| a | b |", "|---|---|", "| 1 | 2 |", "", "---",
	}

	for i := depth - 1; i >= 0; i-- {
		lines = nestLines(kinds[i%len(kinds)], i, lines)
	}

	return strings.Join(lines, "\n") + "\n"
}

// TestNestingMixedPastCap pins the layout of every container kind nested
// beyond the depth where bars and indentation stop.
func TestNestingMixedPastCap(t *testing.T) {
	t.Parallel()

	for _, depth := range []int{12, 24, 36} {
		src := mixedNesting(depth)

		for _, w := range []int{100, 40} {
			l := layoutAllOpen(Parse([]byte(src), ParseOptions{}), LayoutOptions{Width: w, Icons: true})
			assertSafe(t, l, w)

			text := strings.Join(l.Plain(), "\n")
			for _, want := range []string{"first paragraph", "second", "link"} {
				if w == 100 && !strings.Contains(text, want) {
					t.Errorf("depth %d width %d: %q is missing", depth, w, want)
				}
			}

			goldenText(t, fmt.Sprintf("nesting_mixed.%d.%d", depth, w), l.debug())
		}
	}
}

// TestDeepNestingKeepsStack parses and lays out input nested far past the depth
// cap under a small stack limit. An overflow kills the process, so the limit is
// the assertion. The stack limits sit well below what the unbounded recursion
// needed for these inputs, and above what goldmark's own parse takes.
func TestDeepNestingKeepsStack(t *testing.T) {
	const folds, foldStack = 20_000, 8 // folds cost the most stack per level

	const noteLevels = 1000

	var notes strings.Builder
	for i := 1; i <= noteLevels; i++ {
		notes.WriteString(strings.Repeat(">", i) + " [!NOTE]\n")
	}

	tests := []struct {
		name  string
		src   string
		stack int // MiB
		want  func(t *testing.T, l Layout)
	}{
		{"quotes", strings.Repeat(">", 200_000) + " deep", 32, wantLine("deep")},
		// goldmark parses a list this deep in about 0.2 s; its parse time grows with the square of the depth.
		{"list items", strings.Repeat("- ", 10_000) + "deep", 4, wantLine("deep")},
		{"details", strings.Repeat("<details>\n", folds) + "deep", foldStack, func(t *testing.T, l Layout) {
			t.Helper()
			wantLine("deep")(t, l)

			if len(l.Folds) != folds {
				t.Errorf("laid out %d folds, want %d", len(l.Folds), folds)
			}
		}},
		{"alerts", notes.String(), 32, func(t *testing.T, l Layout) {
			t.Helper()

			n := 0

			for _, p := range l.Plain() {
				if strings.Contains(p, "Note") {
					n++
				}
			}

			if n != noteLevels {
				t.Errorf("%d title lines, want %d", n, noteLevels)
			}
		}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			defer debug.SetMaxStack(debug.SetMaxStack(tt.stack << 20))

			d := Parse([]byte(tt.src), ParseOptions{})
			if d.Cut() {
				t.Fatal("the input was cut")
			}

			open := map[int]bool{}
			for i := range d.folds {
				open[i] = true
			}

			l := d.Layout(LayoutOptions{Width: 100, Open: open})
			assertSafe(t, l, 100)
			tt.want(t, l)
		})
	}
}

func wantLine(s string) func(t *testing.T, l Layout) {
	return func(t *testing.T, l Layout) {
		t.Helper()

		for _, p := range l.Plain() {
			if strings.Contains(p, s) {
				return
			}
		}

		t.Errorf("no line holds %q", s)
	}
}

// TestFlatNesting pins what the layout draws past the depth it stops recursing
// at: every level's text, a list item's marker on its own line, and the bars
// and indentation of the capped levels only.
func TestFlatNesting(t *testing.T) {
	t.Parallel()

	const levels = maxDepth + 8

	var lists, quotes strings.Builder
	for i := 1; i <= levels; i++ {
		fmt.Fprintf(&lists, "%s- L%d\n", strings.Repeat("  ", i-1), i)
		fmt.Fprintf(&quotes, "%s Q%d\n%s\n", strings.Repeat(">", i), i, strings.Repeat(">", i))
	}

	t.Run("lists", func(t *testing.T) {
		l := layoutOf(lists.String(), 100, "")
		assertSafe(t, l, 100)

		plain := l.Plain()
		for i := 1; i <= levels; i++ {
			at := slicesIndexLine(plain, fmt.Sprintf("L%d", i))
			if at < 0 {
				t.Fatalf("L%d is missing", i)
			}

			if i > maxDepth && strings.TrimSpace(plain[at]) != fmt.Sprintf("▪ L%d", i) {
				t.Errorf("line of L%d = %q, want its marker and text", i, plain[at])
			}
		}
	})

	t.Run("quotes", func(t *testing.T) {
		l := layoutOf(quotes.String(), 100, "")
		assertSafe(t, l, 100)

		plain := l.Plain()
		for i := 1; i <= levels; i++ {
			at := slicesIndexLine(plain, fmt.Sprintf("Q%d", i))
			if at < 0 {
				t.Fatalf("Q%d is missing", i)
			}

			if bars := strings.Count(plain[at], "▎"); bars != min(i, maxNest) {
				t.Errorf("line of Q%d has %d bars, want %d", i, bars, min(i, maxNest))
			}
		}
	})

	t.Run("items sharing a line", func(t *testing.T) {
		tests := []struct {
			name, src string
			mark      string // the marker the line of the innermost item keeps
		}{
			{"bullets", strings.Repeat("- ", levels) + "deep", "▪"},
			{"task", strings.Repeat("- ", levels) + "[x] deep", "✓"},
			{"numbers", strings.Repeat("7. ", levels) + "deep", "7."},
		}

		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				for w := 1; w <= 10; w++ {
					assertSafe(t, layoutOf(tt.src, w, ""), w)
				}

				l := layoutOf(tt.src, 100, "")
				assertSafe(t, l, 100)

				plain := l.Plain()
				if !strings.Contains(plain[0], tt.mark) {
					t.Errorf("first line has no marker: %q", plain[0])
				}

				if slicesIndexLine(plain, "deep") < 0 {
					t.Errorf("deep is missing from %q", plain)
				}
			})
		}
	})

	t.Run("a closed fold stays closed", func(t *testing.T) {
		inner := []string{"<details>", "<summary>S</summary>", "", "hidden body", "", "</details>"}
		for range levels {
			inner = nestLines("quote", 0, inner)
		}

		d := Parse([]byte(strings.Join(inner, "\n")+"\n"), ParseOptions{})

		closed := strings.Join(d.Layout(LayoutOptions{Width: 100}).Plain(), "\n")
		if !strings.Contains(closed, "▸ S") || strings.Contains(closed, "hidden body") {
			t.Errorf("closed fold laid out as:\n%s", closed)
		}

		open := strings.Join(d.Layout(LayoutOptions{Width: 100, Open: map[int]bool{0: true}}).Plain(), "\n")
		if !strings.Contains(open, "▾ S") || !strings.Contains(open, "hidden body") {
			t.Errorf("open fold laid out as:\n%s", open)
		}
	})
}

func slicesIndexLine(lines []string, s string) int {
	for i, line := range lines {
		if strings.Contains(line, s) {
			return i
		}
	}

	return -1
}

// TestWalkTree checks the iterative walk against goldmark's recursive one.
func TestWalkTree(t *testing.T) {
	t.Parallel()

	for _, doc := range corpusDocs(t) {
		d := Parse(doc.src, ParseOptions{})

		for _, skipParagraphs := range []bool{false, true} {
			var want, got []ast.Node

			_ = ast.Walk(d.root, func(n ast.Node, entering bool) (ast.WalkStatus, error) {
				if !entering {
					return ast.WalkContinue, nil
				}

				want = append(want, n)

				if _, ok := n.(*ast.Paragraph); ok && skipParagraphs {
					return ast.WalkSkipChildren, nil
				}

				return ast.WalkContinue, nil
			})

			walkTree(d.root, func(n ast.Node) bool {
				got = append(got, n)

				_, ok := n.(*ast.Paragraph)

				return !ok || !skipParagraphs
			})

			if len(got) != len(want) {
				t.Fatalf("%s: visited %d nodes, want %d", doc.name, len(got), len(want))
			}

			for i := range got {
				if got[i] != want[i] {
					t.Fatalf("%s: node %d differs", doc.name, i)
				}
			}
		}
	}
}

// TestWalkTreeSubtree stays inside the subtree it is given.
func TestWalkTreeSubtree(t *testing.T) {
	t.Parallel()

	d := Parse([]byte("- a\n- b *c*\n- d\n"), ParseOptions{})

	var item ast.Node

	walkTree(d.root, func(n ast.Node) bool {
		if li, ok := n.(*ast.ListItem); ok && item == nil && li.NextSibling() != nil {
			item = li.NextSibling()
		}

		return true
	})

	var texts []string

	walkTree(item, func(n ast.Node) bool {
		if tx, ok := n.(*ast.Text); ok {
			texts = append(texts, tx.Value.Value(d.src))
		}

		return true
	})

	if got := strings.Join(texts, "|"); got != "b |c" {
		t.Errorf("texts under the second item = %q, want %q", got, "b |c")
	}
}

// The range pass finds which blocks sit in a quote with work linear in the
// tree: a walk up the ancestors of every node would cost the square of the depth.
func TestSourceRangesLinearInDepth(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		src  string
	}{
		{"details", strings.Repeat("<details>\n", 2_000) + "deep"},
		{"list items", strings.Repeat("- ", 2_000) + "deep"},
		{"quotes", strings.Repeat(">", 2_000) + " deep"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			d := Parse([]byte(tt.src), ParseOptions{})

			nodes := len(d.ranges)
			if nodes < 1_000 {
				t.Fatalf("only %d blocks, the input did not nest", nodes)
			}

			if d.quoteSteps > 4*nodes {
				t.Errorf("%d steps for %d blocks, want linear", d.quoteSteps, nodes)
			}
		})
	}
}
