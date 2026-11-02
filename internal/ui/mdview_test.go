package ui

import (
	"fmt"
	"slices"
	"strings"
	"sync/atomic"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/charmbracelet/x/exp/golden"

	"github.com/mhersson/aim/internal/md"
	"github.com/mhersson/aim/internal/ui/diffview"
)

const fakeClass = 7

// countingTokeniser marks the first cell of every code line with fakeClass and
// counts its calls.
func countingTokeniser(calls *atomic.Int32) codeTokeniser {
	return func(_ string, lines []string) [][]diffview.Span {
		calls.Add(1)

		out := make([][]diffview.Span, len(lines))
		for i, l := range lines {
			if l != "" {
				out[i] = []diffview.Span{{Start: 0, End: 1, Class: fakeClass}}
			}
		}

		return out
	}
}

func parseNow(src string, tok codeTokeniser) *mdDoc {
	return parseMarkdown([]byte(src), "", tok)
}

// lineOf is the first layout line whose text holds s, or -1.
func lineOf(lay md.Layout, s string) int {
	for i, p := range lay.Plain() {
		if strings.Contains(p, s) {
			return i
		}
	}

	return -1
}

func hasClass(l md.Line) bool {
	for _, r := range l {
		if r.Role == md.RoleCodeText && r.Class == fakeClass {
			return true
		}
	}

	return false
}

func foldedBlocksDoc() string {
	return strings.Join([]string{
		"# Title", "",
		"```go", "outside_one", "```", "",
		"<details>", "<summary>First</summary>", "", "```go", "inside_one", "```", "", "</details>", "",
		"```go", "outside_two", "```", "",
		"<details>", "<summary>Second</summary>", "", "```py", "inside_two", "```", "", "</details>", "",
		"```sh", "outside_three", "```", "",
	}, "\n")
}

func TestParseMarkdownTokenisesOnce(t *testing.T) {
	t.Parallel()

	var calls atomic.Int32

	d := parseNow(foldedBlocksDoc(), countingTokeniser(&calls))
	if got := calls.Load(); got != 5 {
		t.Fatalf("parse called the tokeniser %d times, want 5", got)
	}

	for _, w := range []int{20, 60, 100} {
		d.layout(w, true)
	}

	d.toggleFold(0)
	d.toggleFold(1)

	lay := d.layout(100, true)

	if got := calls.Load(); got != 5 {
		t.Errorf("layouts called the tokeniser: %d calls, want 5", got)
	}

	for _, s := range []string{"outside_one", "inside_one", "outside_two", "inside_two", "outside_three"} {
		i := lineOf(lay, s)
		if i < 0 || !hasClass(lay.Lines[i]) {
			t.Errorf("%s has no spans (line %d)", s, i)
		}
	}
}

func TestParseMarkdownDefaultTokenCap(t *testing.T) {
	t.Parallel()

	if got := parseNow("x", nil).tokenMax; got != mdTokenMax {
		t.Fatalf("parseMarkdown caps at %d, want %d", got, mdTokenMax)
	}
}

// codeBlocks is one fenced block per size, its lines named b<block>_<line>.
func codeBlocks(sizes ...int) string {
	var sb strings.Builder

	for b, n := range sizes {
		sb.WriteString("```go\n")

		for l := range n {
			fmt.Fprintf(&sb, "b%d_%d\n", b, l)
		}

		sb.WriteString("```\n\n")
	}

	return sb.String()
}

func TestParseMarkdownTokenCap(t *testing.T) {
	t.Parallel()

	var calls atomic.Int32

	d := parseMarkdownCapped([]byte(codeBlocks(2, 2, 1)), "", countingTokeniser(&calls), 4)
	if got := calls.Load(); got != 2 {
		t.Fatalf("tokeniser called %d times, want 2", got)
	}

	lay := d.layout(100, false)

	for _, c := range []struct {
		line string
		want bool
	}{{"b0_1", true}, {"b1_1", true}, {"b2_0", false}} {
		i := lineOf(lay, c.line)
		if i < 0 {
			t.Fatalf("line %q not found", c.line)
		}

		if got := hasClass(lay.Lines[i]); got != c.want {
			t.Errorf("%q has spans = %v, want %v", c.line, got, c.want)
		}
	}
}

func TestParseMarkdownCapIsSticky(t *testing.T) {
	t.Parallel()

	var calls atomic.Int32

	d := parseMarkdownCapped([]byte(codeBlocks(3, 2, 1)), "", countingTokeniser(&calls), 4)
	if got := calls.Load(); got != 1 {
		t.Fatalf("tokeniser called %d times, want 1", got)
	}

	lay := d.layout(100, false)
	if i := lineOf(lay, "b2_0"); i < 0 || hasClass(lay.Lines[i]) {
		t.Errorf("a block after the cap was tokenised (line %d)", i)
	}
}

func TestMDLayoutCache(t *testing.T) {
	t.Parallel()

	d := parseNow(foldedBlocksDoc(), nil)

	steps := []struct {
		name string
		do   func()
		want int
	}{
		{"first", func() { d.layout(60, true) }, 1},
		{"same", func() { d.layout(60, true) }, 1},
		{"width", func() { d.layout(80, true) }, 2},
		{"icons", func() { d.layout(80, false) }, 3},
		{"again", func() { d.layout(80, false) }, 3},
		{"fold", func() { d.toggleFold(0) }, 3},
		{"after fold", func() { d.layout(80, false) }, 4},
	}

	for _, s := range steps {
		s.do()

		if d.layouts != s.want {
			t.Fatalf("%s: %d layouts, want %d", s.name, d.layouts, s.want)
		}
	}
}

const movesDoc = `# Alpha

one

## Beta is a heading long enough that it wraps over several lines here

two
three

### Gamma

four

` + "```go\ncode\n```\n" + `
## Delta

five
`

func newTestView(src string, w, h int) *mdView {
	v := &mdView{doc: parseNow(src, nil)}
	v.size(w, h, true)

	return v
}

func TestMDViewMoves(t *testing.T) {
	t.Parallel()

	v := newTestView(movesDoc, 40, 10)
	lay := v.layout()
	n := len(lay.Lines)
	alpha, beta, gamma, delta := lineOf(lay, "Alpha"), lineOf(lay, "Beta"), lineOf(lay, "Gamma"), lineOf(lay, "Delta")

	if lineOf(lay, "lines here") != beta+1 {
		t.Fatalf("the Beta heading does not wrap over two lines: %q", lay.Plain())
	}

	cases := []struct {
		name string
		do   func(*mdView)
		want int
	}{
		{"move down", func(v *mdView) { v.move(3) }, 3},
		{"move clamps top", func(v *mdView) { v.move(-5) }, 0},
		{"move clamps bottom", func(v *mdView) { v.move(n + 50) }, n - 1},
		{"page down", func(v *mdView) { v.page(1) }, 5},
		{"page up clamps", func(v *mdView) { v.page(-1) }, 0},
		{"end bottom", func(v *mdView) { v.end(true) }, n - 1},
		{"end top", func(v *mdView) { v.end(true); v.end(false) }, 0},
		{"heading from top", func(v *mdView) { v.heading(1) }, beta},
		{"heading twice", func(v *mdView) { v.heading(1); v.heading(1) }, gamma},
		{"heading from inside a wrapped one", func(v *mdView) { v.move(beta + 1); v.heading(-1) }, beta},
		{"heading back", func(v *mdView) { v.end(true); v.heading(-1) }, delta},
		{"heading stays at the last", func(v *mdView) { v.move(delta); v.heading(1) }, delta},
		{"heading stays at the first", func(v *mdView) { v.move(alpha); v.heading(-1) }, alpha},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()

			v := newTestView(movesDoc, 40, 10)
			c.do(v)

			if v.cursor != c.want {
				t.Errorf("cursor = %d, want %d", v.cursor, c.want)
			}

			if v.cursor < v.top || v.cursor >= v.top+10 {
				t.Errorf("cursor %d outside the window [%d, %d)", v.cursor, v.top, v.top+10)
			}

			if v.top < 0 || v.top > max(n-10, 0) {
				t.Errorf("top = %d out of range", v.top)
			}
		})
	}
}

func TestMDViewFold(t *testing.T) {
	t.Parallel()

	v := newTestView(foldedBlocksDoc(), 40, 10)
	head := lineOf(v.layout(), "First")
	before := len(v.layout().Lines)

	v.move(head)

	if !v.fold() {
		t.Fatal("fold on a head returned false")
	}

	if v.cursor != head {
		t.Errorf("cursor = %d, want the head %d", v.cursor, head)
	}

	if after := len(v.layout().Lines); after <= before {
		t.Errorf("opening grew the layout from %d to %d lines", before, after)
	}

	if !v.fold() || len(v.layout().Lines) != before {
		t.Errorf("a second fold did not close it")
	}

	v.move(-head)
	v.move(1)

	cur, lines := v.cursor, len(v.layout().Lines)
	if v.fold() {
		t.Error("fold on a plain line returned true")
	}

	if v.cursor != cur || len(v.layout().Lines) != lines {
		t.Error("fold on a plain line changed the view")
	}
}

func TestMDViewFind(t *testing.T) {
	t.Parallel()

	v := newTestView(movesDoc, 40, 10)
	lay := v.layout()
	gamma, delta := lineOf(lay, "Gamma"), lineOf(lay, "Delta")

	if !v.find("GAMMA") || v.cursor != gamma {
		t.Fatalf("find GAMMA: cursor %d, want %d", v.cursor, gamma)
	}

	if !v.find("gamma") || v.cursor != gamma {
		t.Errorf("find from its own line must wrap back to it, got %d", v.cursor)
	}

	v.move(delta - v.cursor)

	if !v.find("alpha") || v.cursor != lineOf(lay, "Alpha") {
		t.Errorf("find did not wrap, cursor %d", v.cursor)
	}

	cur := v.cursor
	if v.find("no such text") || v.find("") || v.cursor != cur {
		t.Error("a miss or an empty query must change nothing")
	}
}

func TestMDViewFindAfterDocSwap(t *testing.T) {
	t.Parallel()

	v := newTestView("first doc\n\nneedle_old\n", 40, 10)
	v.layout()
	v.find("needle")

	v.doc = parseNow("# New\n\nfiller\n\nneedle_new\n", nil)
	v.cursor, v.top = 0, 0
	v.size(40, 10, true)

	if !v.find("needle_new") || v.cursor != lineOf(v.layout(), "needle_new") {
		t.Errorf("find after a swap landed on %d", v.cursor)
	}
}

func TestLineTargets(t *testing.T) {
	t.Parallel()

	base := "/work"
	cases := []struct {
		name string
		src  string
		line string
		want []mdTarget
	}{
		{"one link", "see [docs](https://a.example/x) now", "see", []mdTarget{{Text: "docs", URL: "https://a.example/x"}}},
		{"links and a path", "[a](https://a.example) then [b](./b.md) then [c](https://c.example)", "then", []mdTarget{
			{Text: "a", URL: "https://a.example"}, {Text: "b", Path: "/work/b.md"}, {Text: "c", URL: "https://c.example"},
		}},
		{"mailto left out", "[me](mailto:a@b.example) and [x](https://x.example)", "and", []mdTarget{{Text: "x", URL: "https://x.example"}}},
		{"same url twice", "[a](https://a.example) and [b](https://a.example)", "and", []mdTarget{{Text: "a", URL: "https://a.example"}}},
		{"badge in a link", "[![build status](https://img.example/b.svg)](https://ci.example)", "build status", []mdTarget{{Text: "build status", URL: "https://ci.example"}}},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()

			lay := md.Parse([]byte(c.src), md.ParseOptions{Base: base}).Layout(md.LayoutOptions{Width: 80})
			i := lineOf(lay, c.line)

			if i < 0 {
				t.Fatalf("no line with %q in %q", c.line, lay.Plain())
			}

			got := lineTargets(lay, i)
			if !slices.Equal(got, c.want) {
				t.Errorf("targets = %+v, want %+v", got, c.want)
			}
		})
	}

	t.Run("wrapped link", func(t *testing.T) {
		t.Parallel()

		lay := md.Parse([]byte("[alpha beta gamma delta](https://a.example)"), md.ParseOptions{}).Layout(md.LayoutOptions{Width: 12})
		if len(lay.Lines) < 2 {
			t.Fatalf("the link did not wrap: %q", lay.Plain())
		}

		for i := range lay.Lines {
			if got := lineTargets(lay, i); len(got) != 1 || got[0].URL != "https://a.example" {
				t.Errorf("line %d targets = %+v", i, got)
			}
		}
	})
}

func TestMDViewGoSource(t *testing.T) {
	t.Parallel()

	src := "# Head\n\npara one\nstill one\n\n```go\ncode\n```\n\nlast\n"
	v := newTestView(src, 40, 20)
	lay := v.layout()
	last := len(lay.Lines) - 1

	cases := []struct {
		name string
		line int
		want int
	}{
		{"inside a block", 4, lineOf(lay, "para one")},
		{"inside a code block", 7, lineOf(lay, "╭")},
		{"blank between blocks", 5, lineOf(lay, "╭")},
		{"past the end", 99, last},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()

			v := newTestView(src, 40, 20)
			v.goSource(c.line)

			if v.cursor != c.want {
				t.Errorf("goSource(%d): cursor %d, want %d", c.line, v.cursor, c.want)
			}
		})
	}
}

func TestBlockEditLine(t *testing.T) {
	t.Parallel()

	none := md.SourceRange{}
	src := []md.SourceRange{none, {First: 3, Last: 4}, none, {First: 6, Last: 9}, none, none}

	tests := []struct {
		name string
		src  []md.SourceRange
		line int
		want int
	}{
		{"a block row", src, 1, 3},
		{"a blank before the first block", src, 0, 3},
		{"a blank between blocks", src, 2, 6},
		{"a blank after the last block", src, 5, 9},
		{"no block at all", []md.SourceRange{none, none}, 1, 1},
		{"past the layout", src, 99, 1},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if got := blockEditLine(tt.src, tt.line); got != tt.want {
				t.Errorf("blockEditLine = %d, want %d", got, tt.want)
			}
		})
	}
}

func drawTestView(t *testing.T, icons bool) (*mdView, mdPainter) {
	t.Helper()

	th := mochaTheme(t)
	v := newTestView(foldedBlocksDoc(), 40, 8)
	v.size(40, 8, icons)
	v.move(lineOf(v.layout(), "First"))

	return v, newMDPainter(th, th.UI.Bg)
}

func TestMDViewDraw(t *testing.T) {
	t.Parallel()

	v, p := drawTestView(t, true)
	golden.RequireEqual(t, []byte(strings.Join(v.draw(p, true), "\n")))
}

func TestMDViewDrawText(t *testing.T) {
	t.Parallel()

	for _, icons := range []bool{true, false} {
		t.Run(fmt.Sprintf("icons=%v", icons), func(t *testing.T) {
			t.Parallel()

			// An alert and an image carry icons, so the two goldens differ.
			src := "# Title\n\n> [!NOTE]\n> alert body\n\n![logo](logo.png)\n\n<details>\n<summary>First</summary>\n\nbody\n\n</details>\n"
			v := newTestView(src, 40, 14)
			v.size(40, 14, icons)
			v.move(lineOf(v.layout(), "First"))

			th := mochaTheme(t)
			golden.RequireEqual(t, []byte(ansi.Strip(strings.Join(v.draw(newMDPainter(th, th.UI.Bg), true), "\n"))))
		})
	}
}

func TestMDViewDrawWidths(t *testing.T) {
	t.Parallel()

	v, p := drawTestView(t, true)
	v.doc.toggleFold(0)

	lay := v.layout()

	inside := lineOf(lay, "inside_one")
	if inside < 0 {
		t.Fatal("the opened fold's code block is not drawn")
	}

	cursors := []int{0, inside, len(lay.Lines) - 1}

	for _, focused := range []bool{true, false} {
		for _, c := range cursors {
			v.end(false)
			v.move(c - v.cursor)

			rows := v.draw(p, focused)
			if len(rows) != 8 {
				t.Fatalf("%d rows, want 8", len(rows))
			}

			for i, r := range rows {
				if w := ansi.StringWidth(r); w != 40 {
					t.Fatalf("cursor %d row %d is %d cells", v.cursor, i, w)
				}
			}
		}
	}
}

func TestMDViewCursorColumn(t *testing.T) {
	t.Parallel()

	v, p := drawTestView(t, true)

	for i, r := range v.draw(p, false) {
		if strings.Contains(r, "▎") {
			t.Errorf("unfocused row %d has a cursor", i)
		}
	}
}

func TestMDViewHugeDoc(t *testing.T) {
	t.Parallel()

	// 64 levels is past md's maxDepth of 32.
	cmd := parseCmd([]byte(strings.Repeat(">", 64)+" deep\n"), "", nil, func(d *mdDoc) tea.Msg { return d })

	d, ok := cmd().(*mdDoc)
	if !ok || d == nil {
		t.Fatal("parseCmd did not answer with the doc")
	}

	th := mochaTheme(t)
	v := &mdView{doc: d}
	v.size(120, 40, true)

	for _, focused := range []bool{true, false} {
		rows := v.draw(newMDPainter(th, th.UI.Bg), focused)
		if len(rows) != 40 {
			t.Fatalf("%d rows, want 40", len(rows))
		}

		for i, r := range rows {
			if w := ansi.StringWidth(r); w != 120 {
				t.Fatalf("row %d is %d cells", i, w)
			}
		}

		v.end(true)
	}
}

func TestMDDocToggleTwice(t *testing.T) {
	t.Parallel()

	d := parseNow(foldedBlocksDoc(), nil)
	before := len(d.layout(80, false).Lines)

	d.toggleFold(0)
	d.toggleFold(0)

	if got := len(d.layout(80, false).Lines); got != before {
		t.Errorf("two toggles left %d lines, want %d", got, before)
	}
}

func TestMDViewScroll(t *testing.T) {
	t.Parallel()

	src := ""
	for i := range 60 {
		src += fmt.Sprintf("line %d\n\n", i)
	}

	// The window is 10 rows; n is the layout's line count.
	tests := []struct {
		name        string
		cursor, top int
		do          func(v *mdView)
		want        func(n int) (top, cursor int)
	}{
		{"down keeps a cursor in view", 5, 0, func(v *mdView) { v.scroll(3) }, func(int) (int, int) { return 3, 5 }},
		{"down pushes a cursor off the top", 1, 0, func(v *mdView) { v.scroll(3) }, func(int) (int, int) { return 3, 3 }},
		{"up pulls a cursor off the bottom", 14, 5, func(v *mdView) { v.scroll(-3) }, func(int) (int, int) { return 2, 11 }},
		{"up clamps at the first line", 2, 1, func(v *mdView) { v.scroll(-9) }, func(int) (int, int) { return 0, 2 }},
		{"down clamps at the last page", 0, 0, func(v *mdView) { v.scroll(999) }, func(n int) (int, int) { return n - 10, n - 10 }},
		{"page down moves both by the rows less one", 2, 1, func(v *mdView) { v.pageFull(1) }, func(int) (int, int) { return 10, 11 }},
		{"page up moves both back", 12, 10, func(v *mdView) { v.pageFull(-1) }, func(int) (int, int) { return 1, 3 }},
		{"page up clamps at the top", 3, 2, func(v *mdView) { v.pageFull(-1) }, func(int) (int, int) { return 0, 0 }},
		{"page down clamps at the end", 0, 0, func(v *mdView) {
			for range 99 {
				v.pageFull(1)
			}
		}, func(n int) (int, int) { return n - 10, n - 1 }},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			v := newTestView(src, 40, 10)
			n := len(v.layout().Lines)
			v.top, v.cursor = tt.top, tt.cursor
			tt.do(v)

			wantTop, wantCursor := tt.want(n)
			if v.top != wantTop || v.cursor != wantCursor {
				t.Errorf("top, cursor = %d, %d; want %d, %d (%d lines)", v.top, v.cursor, wantTop, wantCursor, n)
			}

			if v.cursor < v.top || v.cursor >= v.top+v.h {
				t.Errorf("cursor %d left the window [%d, %d)", v.cursor, v.top, v.top+v.h)
			}
		})
	}
}
