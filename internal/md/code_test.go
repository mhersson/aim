package md

import (
	"fmt"
	"slices"
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
)

func codeLayout(src string, width int, hl Highlighter) Layout {
	return layoutWith(src, LayoutOptions{Width: width, Highlight: hl}, "")
}

func bar(n int) string { return strings.Repeat("─", n) }

func TestCodeFrameAndLabel(t *testing.T) {
	t.Parallel()

	l := codeLayout("```go\nx := 1\n```\n", 20, nil)
	wantLines(t, l,
		"╭─ go "+bar(13)+"╮",
		"│ 1 x := 1         │",
		"╰"+bar(18)+"╯",
	)

	if r := run(t, l, 0, "go"); r.Role != RoleCodeLang || r.Text != "go" {
		t.Errorf("label run = %+v", r)
	}

	if r := l.Lines[0][0]; r.Role != RoleCodeFrame || r.Text != "╭─ " {
		t.Errorf("top left run = %+v", r)
	}

	row := l.Lines[1]
	if row[0].Role != RoleCodeFrame || row[0].Text != "│ " {
		t.Errorf("row left = %+v", row[0])
	}

	if row[1].Role != RoleLineNumber || row[1].Text != "1 " {
		t.Errorf("row number = %+v", row[1])
	}

	if row[2].Role != RoleCodeText || row[2].Text != "x := 1" {
		t.Errorf("row code = %+v", row[2])
	}

	if row[3].Role != RoleCodeText || row[3].Text != "        " || !row[3].chrome {
		t.Errorf("row pad = %+v", row[3])
	}

	if last := row[len(row)-1]; last.Role != RoleCodeFrame || last.Text != " │" {
		t.Errorf("row right = %+v", last)
	}

	for _, r := range l.Lines[2] {
		if r.Role != RoleCodeFrame {
			t.Errorf("bottom run = %+v", r)
		}
	}

	t.Run("no label without a language", func(t *testing.T) {
		l := codeLayout("```\nx\n```\n", 12, nil)
		wantLines(t, l, "╭"+bar(10)+"╮", "│ 1 x      │", "╰"+bar(10)+"╯")
	})

	t.Run("first word of the info string", func(t *testing.T) {
		for _, info := range []string{"go linenos", "go\tlinenos", "go {1,3}"} {
			l := codeLayout("```"+info+"\nx\n```\n", 20, nil)
			if got := l.Plain()[0]; got != "╭─ go "+bar(13)+"╮" {
				t.Errorf("info %q: top = %q", info, got)
			}
		}
	})

	t.Run("long language is cut to 20 cells", func(t *testing.T) {
		l := codeLayout("```"+strings.Repeat("a", 40)+"\nx\n```\n", 40, nil)

		want := "╭─ " + strings.Repeat("a", 19) + "… " + bar(15) + "╮"
		if got := l.Plain()[0]; got != want || ansi.StringWidth(got) != 40 {
			t.Errorf("top = %q (%d cells), want %q", got, ansi.StringWidth(got), want)
		}
	})

	t.Run("label never crowds the frame", func(t *testing.T) {
		// fw 12 leaves 6 cells for the label.
		l := codeLayout("```"+strings.Repeat("b", 30)+"\nx\n```\n", 12, nil)

		want := "╭─ bbbbb… " + bar(1) + "╮"
		if got := l.Plain()[0]; got != want {
			t.Errorf("top = %q, want %q", got, want)
		}
	})

	t.Run("label is cleaned like prose", func(t *testing.T) {
		l := codeLayout("```g\u200bo\x01\nx\n```\n", 20, nil)
		if got := l.Plain()[0]; got != "╭─ go "+bar(13)+"╮" {
			t.Errorf("top = %q", got)
		}

		assertSafe(t, l, 20)
	})
}

func TestCodeLineNumbers(t *testing.T) {
	t.Parallel()

	tests := []struct {
		n         int
		first     string
		last      string
		wantWidth int
	}{
		{9, "│ 1 a", "│ 9 a", 1},
		{10, "│  1 a", "│ 10 a", 2},
		{100, "│   1 a", "│ 100 a", 3},
	}

	for _, tt := range tests {
		t.Run(fmt.Sprint(tt.n), func(t *testing.T) {
			src := "```\n" + strings.Repeat("a\n", tt.n) + "```\n"
			l := codeLayout(src, 30, nil)

			if len(l.Lines) != tt.n+2 {
				t.Fatalf("%d lines, want %d", len(l.Lines), tt.n+2)
			}

			plain := l.Plain()
			if !strings.HasPrefix(plain[1], tt.first) || !strings.HasPrefix(plain[tt.n], tt.last) {
				t.Errorf("first %q, last %q; want prefixes %q, %q", plain[1], plain[tt.n], tt.first, tt.last)
			}

			for i, line := range l.Lines {
				if w := lineWidth(line); w != 30 {
					t.Errorf("line %d is %d cells, want 30", i, w)
				}
			}

			if r := l.Lines[tt.n][1]; r.Role != RoleLineNumber {
				t.Errorf("number run = %+v", r)
			}
		})
	}
}

func TestCodeCutNotWrapped(t *testing.T) {
	t.Parallel()

	l := codeLayout("```\n"+strings.Repeat("x", 10000)+"\n```\n", 20, nil)
	// room = 20 - 5 - 1 = 14: 13 cells of code, then the cut mark.
	wantLines(t, l, "╭"+bar(18)+"╮", "│ 1 "+strings.Repeat("x", 13)+"… │", "╰"+bar(18)+"╯")

	row := l.Lines[1]
	if r := row[len(row)-2]; r.Text != "…" || r.Role != RoleCodeText || r.Class != 0 || r.rest != strings.Repeat("x", 9987) {
		t.Errorf("cut mark run = %+v", r)
	}

	t.Run("fits exactly", func(t *testing.T) {
		l := codeLayout("```\n"+strings.Repeat("x", 14)+"\n```\n", 20, nil)
		if got := l.Plain()[1]; got != "│ 1 "+strings.Repeat("x", 14)+" │" {
			t.Errorf("row = %q", got)
		}
	})

	t.Run("one too many", func(t *testing.T) {
		l := codeLayout("```\n"+strings.Repeat("x", 15)+"\n```\n", 20, nil)
		if got := l.Plain()[1]; got != "│ 1 "+strings.Repeat("x", 13)+"… │" {
			t.Errorf("row = %q", got)
		}
	})

	t.Run("wide character straddling the cut", func(t *testing.T) {
		// A wide cell that would cross the room-1 cut leaves a space before the ….
		l := codeLayout("```\n"+strings.Repeat("x", 12)+"好好好\n```\n", 20, nil)
		if got := l.Plain()[1]; got != "│ 1 "+strings.Repeat("x", 12)+" … │" {
			t.Errorf("row = %q", got)
		}

		l = codeLayout("```\n"+strings.Repeat("x", 13)+"好好\n```\n", 20, nil)
		if got := l.Plain()[1]; got != "│ 1 "+strings.Repeat("x", 13)+"… │" {
			t.Errorf("row = %q", got)
		}

		l = codeLayout("```\n"+strings.Repeat("x", 12)+"好x好\n```\n", 20, nil)
		if got := l.Plain()[1]; got != "│ 1 "+strings.Repeat("x", 12)+" … │" {
			t.Errorf("row = %q", got)
		}

		l = codeLayout("```\n"+strings.Repeat("x", 11)+"好好好\n```\n", 20, nil)
		if got := l.Plain()[1]; got != "│ 1 "+strings.Repeat("x", 11)+"好… │" {
			t.Errorf("row = %q", got)
		}
	})

	t.Run("hidden tag straddling the cut", func(t *testing.T) {
		l := codeLayout("```\n"+strings.Repeat("x", 10)+"\u200b\u200b\n```\n", 20, nil)
		// the tag is 8 cells; it starts at 10, ends at 18 > 14.
		if got := l.Plain()[1]; got != "│ 1 "+strings.Repeat("x", 10)+"   … │" {
			t.Errorf("row = %q", got)
		}
	})
}

func TestCodeTabs(t *testing.T) {
	t.Parallel()

	l := codeLayout("```\na\tb\n\tc\nabcd\te\n好\tx\n```\n", 20, nil)
	plain := l.Plain()

	for i, want := range []string{
		"│ 1 a   b          │",
		"│ 2     c          │",
		"│ 3 abcd    e      │",
		"│ 4 好  x          │",
	} {
		if plain[i+1] != want {
			t.Errorf("line %d = %q, want %q", i+1, plain[i+1], want)
		}
	}

	t.Run("spaces keep the tab's class", func(t *testing.T) {
		hl := func(_ string, lines []string) [][]Span {
			return [][]Span{{{Start: 1, End: 2, Class: 4}}}
		}

		l := codeLayout("```\na\tb\n```\n", 20, hl)
		if r := run(t, l, 1, "   "); r.Class != 4 || r.Text != "   " {
			t.Errorf("tab run = %+v", r)
		}
	})

	t.Run("tab after a tag counts the tag's cells", func(t *testing.T) {
		l := codeLayout("```\n\x01\tx\n```\n", 30, nil)
		// the tag ⟨U+0001⟩ is 8 cells wide, so the tab goes 4 more.
		if got := l.Plain()[1]; got != "│ 1 ⟨U+0001⟩    x"+strings.Repeat(" ", 30-5-1-8-4-1)+" │" {
			t.Errorf("row = %q", got)
		}
	})
}

func TestCodeHidden(t *testing.T) {
	t.Parallel()

	l := codeLayout("```\na\x01b\u200bc\x7fd\u202ee\n```\n", 60, nil)
	wantLines(t, l,
		"╭"+bar(58)+"╮",
		"│ 1 a⟨U+0001⟩b⟨U+200B⟩c⟨U+007F⟩d⟨U+202E⟩e"+strings.Repeat(" ", 60-5-1-5-32)+" │",
		"╰"+bar(58)+"╯",
	)

	for _, tag := range []string{"⟨U+0001⟩", "⟨U+200B⟩", "⟨U+007F⟩", "⟨U+202E⟩"} {
		if r := run(t, l, 1, tag); r.Role != RoleHidden || !r.Bold || r.Class != 0 || r.Text != tag {
			t.Errorf("%s run = %+v", tag, r)
		}
	}

	assertSafe(t, l, 60)

	t.Run("invalid UTF-8", func(t *testing.T) {
		l := codeLayout("```\na\xffb\n```\n", 20, nil)
		if got := l.Plain()[1]; !strings.HasPrefix(got, "│ 1 a�b ") {
			t.Errorf("row = %q", got)
		}

		assertSafe(t, l, 20)
	})

	t.Run("carriage returns", func(t *testing.T) {
		l := codeLayout("```go\r\nx\r\ny\r\n```\r\n", 20, nil)
		for _, line := range l.Plain() {
			if strings.Contains(line, "U+000D") {
				t.Errorf("line %q tags a line ending", line)
			}
		}

		wantLines(t, l, "╭─ go "+bar(13)+"╮", "│ 1 x              │", "│ 2 y              │", "╰"+bar(18)+"╯")

		l = codeLayout("```\na\rb\n```\n", 20, nil)
		if got := l.Plain()[1]; !strings.Contains(got, "a⟨U+000D⟩b") {
			t.Errorf("row = %q, want a tagged inner CR", got)
		}
	})
}

func TestCodeHighlighter(t *testing.T) {
	t.Parallel()

	classes := func(l Layout, i int) string {
		var b strings.Builder

		for _, r := range l.Lines[i] {
			if r.Role == RoleCodeText && strings.TrimSpace(r.Text) != "" {
				fmt.Fprintf(&b, "%s=%d ", strings.TrimRight(r.Text, " "), r.Class)
			}
		}

		return strings.TrimSpace(b.String())
	}

	t.Run("classes land on the right runs", func(t *testing.T) {
		var gotLang string

		var gotLines []string

		calls := 0
		hl := func(lang string, lines []string) [][]Span {
			calls++
			gotLang, gotLines = lang, slices.Clone(lines)

			return [][]Span{
				{{Start: 0, End: 4, Class: 1}, {Start: 5, End: 6, Class: 2}},
				{{Start: 0, End: 1, Class: 3}},
			}
		}

		l := codeLayout("```go {1}\nfunc x\n\t}\n```\n", 30, hl)

		if calls != 1 || gotLang != "go" || !slices.Equal(gotLines, []string{"func x", "\t}"}) {
			t.Errorf("calls %d, lang %q, lines %q", calls, gotLang, gotLines)
		}

		if got := classes(l, 1); got != "func=1 x=2" {
			t.Errorf("line 1 classes = %q", got)
		}

		// bytes 0-1 are the tab: its spaces take class 3, the } stays plain.
		if r := run(t, l, 2, "   "); r.Class != 3 {
			t.Errorf("tab run = %+v", r)
		}

		if r := run(t, l, 2, "}"); r.Class != 0 {
			t.Errorf("} run = %+v", r)
		}
	})

	t.Run("nil highlighter gives plain runs", func(t *testing.T) {
		l := codeLayout("```go\nfunc x\n```\n", 30, nil)
		for _, r := range l.Lines[1] {
			if r.Class != 0 {
				t.Errorf("run %+v has a class", r)
			}
		}
	})

	t.Run("nil entries, extra entries and a short result", func(t *testing.T) {
		hl := func(string, []string) [][]Span {
			return [][]Span{{{Start: 0, End: 1, Class: 9}}, nil, {{Start: 0, End: 1, Class: 8}}, {{Start: 0, End: 1, Class: 7}}}
		}

		l := codeLayout("```\na\nb\nc\n```\n", 30, hl)
		for i, want := range []string{"a=9", "b=0", "c=8"} {
			if got := classes(l, i+1); got != want {
				t.Errorf("line %d classes = %q, want %q", i+1, got, want)
			}
		}

		assertSafe(t, l, 30)

		short := func(string, []string) [][]Span { return [][]Span{{{Start: 0, End: 1, Class: 5}}} }

		l = codeLayout("```\na\nb\nc\n```\n", 30, short)
		for i, want := range []string{"a=5", "b=0", "c=0"} {
			if got := classes(l, i+1); got != want {
				t.Errorf("short result, line %d classes = %q, want %q", i+1, got, want)
			}
		}
	})

	t.Run("out-of-range spans are clamped", func(t *testing.T) {
		hl := func(_ string, lines []string) [][]Span {
			return [][]Span{
				{{Start: -5, End: 2, Class: 1}, {Start: 3, End: 99, Class: 2}},
				{{Start: 5, End: 9, Class: 3}, {Start: 2, End: 1, Class: 4}, {Start: 1, End: 1, Class: 5}},
			}
		}

		l := codeLayout("```\nabcdef\nxy\n```\n", 30, hl)
		if got := classes(l, 1); got != "ab=1 c=0 def=2" {
			t.Errorf("line 1 classes = %q, want ab=1 c=0 def=2", got)
		}

		if got := classes(l, 2); got != "xy=0" {
			t.Errorf("line 2 classes = %q, want xy=0", got)
		}
	})

	t.Run("later spans override earlier ones", func(t *testing.T) {
		hl := func(_ string, lines []string) [][]Span {
			return [][]Span{{{Start: 0, End: 6, Class: 1}, {Start: 2, End: 4, Class: 2}}}
		}

		l := codeLayout("```\nabcdef\n```\n", 30, hl)
		if got := classes(l, 1); got != "ab=1 cd=2 ef=1" {
			t.Errorf("classes = %q", got)
		}
	})

	t.Run("hidden tags stay class 0", func(t *testing.T) {
		hl := func(_ string, lines []string) [][]Span {
			return [][]Span{{{Start: 0, End: 3, Class: 6}}}
		}

		l := codeLayout("```\na\x01b\n```\n", 30, hl)
		if r := run(t, l, 1, "⟨"); r.Class != 0 || r.Role != RoleHidden {
			t.Errorf("tag run = %+v", r)
		}

		if r := run(t, l, 1, "a"); r.Class != 6 {
			t.Errorf("a run = %+v", r)
		}
	})

	t.Run("not called for an empty block", func(t *testing.T) {
		called := false

		codeLayout("```go\n```\n", 30, func(string, []string) [][]Span {
			called = true

			return nil
		})

		if called {
			t.Error("highlighter called for a block with no lines")
		}
	})

	t.Run("language is passed as written", func(t *testing.T) {
		long := strings.Repeat("z", 40)

		var got string

		codeLayout("```"+long+"\nx\n```\n", 30, func(lang string, _ []string) [][]Span {
			got = lang

			return nil
		})

		if got != long {
			t.Errorf("lang = %q", got)
		}

		got = "unset"

		codeLayout("```\nx\n```\n", 30, func(lang string, _ []string) [][]Span {
			got = lang

			return nil
		})

		if got != "" {
			t.Errorf("lang = %q, want empty", got)
		}
	})

	t.Run("spans that split a multi-byte character", func(t *testing.T) {
		// 好 is bytes 1-3 of a好b.
		for _, tt := range []struct {
			span Span
			want string
		}{
			{Span{Start: 2, End: 4, Class: 7}, "a好b=0"},
			{Span{Start: 1, End: 2, Class: 7}, "a=0 好=7 b=0"},
			{Span{Start: 0, End: 2, Class: 7}, "a好=7 b=0"},
			{Span{Start: 3, End: 5, Class: 7}, "a好=0 b=7"},
		} {
			hl := func(string, []string) [][]Span { return [][]Span{{tt.span}} }
			l := codeLayout("```\na好b\n```\n", 30, hl)

			var parts []string

			for _, r := range l.Lines[1] {
				if r.Role == RoleCodeText && strings.TrimSpace(r.Text) != "" {
					parts = append(parts, fmt.Sprintf("%s=%d", strings.TrimRight(r.Text, " "), r.Class))
				}
			}

			assertSafe(t, l, 30)

			if got := strings.Join(parts, " "); got != tt.want {
				t.Errorf("span %+v: classes %q, want %q", tt.span, got, tt.want)
			}
		}
	})
}

func TestIndentedCode(t *testing.T) {
	t.Parallel()

	l := codeLayout("para\n\n    a\n    b\n\n\tc\n", 20, nil)
	wantLines(t, l,
		"para",
		"",
		"╭"+bar(18)+"╮",
		"│ 1 a              │",
		"│ 2 b              │",
		"│ 3                │",
		"│ 4 c              │",
		"╰"+bar(18)+"╯",
	)

	t.Run("padding from a partial tab", func(t *testing.T) {
		l := codeLayout("  \t a\n", 20, nil)
		if got := l.Plain()[1]; got != "│ 1  a             │" {
			t.Errorf("row = %q", got)
		}
	})
}

func TestCodeEmptyBlock(t *testing.T) {
	t.Parallel()

	wantLines(t, codeLayout("```\n```\n", 12, nil), "╭"+bar(10)+"╮", "╰"+bar(10)+"╯")
	wantLines(t, codeLayout("```go\n```\n", 12, nil), "╭─ go "+bar(5)+"╮", "╰"+bar(10)+"╯")
}

func TestCodeInContainers(t *testing.T) {
	t.Parallel()

	t.Run("list item", func(t *testing.T) {
		l := codeLayout("- item\n\n  ```go\n  x\n  ```\n", 20, nil)
		wantLines(t, l,
			"  • item",
			"    ╭─ go "+bar(9)+"╮",
			"    │ 1 x          │",
			"    ╰"+bar(14)+"╯",
		)
		assertSafe(t, l, 20)
	})

	t.Run("quote", func(t *testing.T) {
		l := codeLayout("> ```go\n> x\n> ```\n", 20, nil)
		wantLines(t, l,
			"▎ ╭─ go "+bar(11)+"╮",
			"▎ │ 1 x            │",
			"▎ ╰"+bar(16)+"╯",
		)
		assertSafe(t, l, 20)
	})

	t.Run("alert", func(t *testing.T) {
		l := codeLayout("> [!NOTE]\n> ```\n> x\n> ```\n", 20, nil)
		assertSafe(t, l, 20)

		if got := l.Plain()[1]; got != "▎ ╭"+bar(16)+"╮" {
			t.Errorf("top = %q", got)
		}
	})

	t.Run("open fold", func(t *testing.T) {
		src := "<details open>\n<summary>S</summary>\n\n```\nx\n```\n\n</details>\n"
		l := codeLayout(src, 20, nil)
		assertSafe(t, l, 20)

		if got := l.Plain(); len(got) < 4 || got[1] != "  ╭"+bar(16)+"╮" {
			t.Errorf("lines = %q", got)
		}
	})

	t.Run("spacing between blocks", func(t *testing.T) {
		l := codeLayout("a\n\n```\nx\n```\n\nb\n", 12, nil)
		wantLines(t, l, "a", "", "╭"+bar(10)+"╮", "│ 1 x      │", "╰"+bar(10)+"╯", "", "b")
	})
}

func TestCodeNarrowWidths(t *testing.T) {
	t.Parallel()

	srcs := []string{
		"```go\nfmt.Println(\"hello world\")\n\tindented\n```\n",
		"```\n好好好好好好好好好好\n😀😀😀😀😀😀\ne\u0301e\u0301\x01\u200b\n```\n",
		"```\n" + strings.Repeat("x", 10000) + "\n```\n",
		"```\n" + strings.Repeat("\t", 500) + "x\n```\n",
		"```" + strings.Repeat("l", 50) + "\n" + strings.Repeat("a\n", 120) + "```\n",
		"```\n```\n",
		"    indented\n",
		"```\n" + strings.Repeat("好\x01\t", 5000) + "\n```\n",
	}

	for _, w := range []int{1, 2, 3, 5, 10} {
		for i, src := range srcs {
			l := codeLayout(src, w, func(_ string, lines []string) [][]Span {
				out := make([][]Span, len(lines))
				for i := range out {
					out[i] = []Span{{Start: 1, End: 3, Class: 2}}
				}

				return out
			})

			assertSafe(t, l, w)

			if w == 1 {
				for j, line := range l.Plain() {
					if line != "…" {
						t.Errorf("src %d, width 1, line %d = %q, want …", i, j, line)
					}
				}
			}
		}
	}
}

func TestCodeGolden(t *testing.T) {
	t.Parallel()

	src := "Intro\n\n```go\npackage main\n\nfunc main() {\n\tprintln(\"hi\")\t// tab\n}\n```\n\n    indented\n\n```\nwith\x01hidden\u200btags and 好 wide\n```\n"

	for _, w := range []int{14, 40} {
		hl := func(_ string, lines []string) [][]Span {
			out := make([][]Span, len(lines))
			for i, ln := range lines {
				if strings.HasPrefix(ln, "func") || strings.HasPrefix(ln, "package") {
					out[i] = []Span{{Start: 0, End: 4, Class: 1}}
				}
			}

			return out
		}

		goldenText(t, fmt.Sprintf("code.%d", w), codeLayout(src, w, hl).debugClasses())
	}
}

// debugClasses is the debug form with each classed run's text followed by #class.
func (l Layout) debugClasses() string {
	out := Layout{Lines: make([]Line, len(l.Lines))}

	for i, line := range l.Lines {
		out.Lines[i] = slices.Clone(line)

		for j := range out.Lines[i] {
			if c := out.Lines[i][j].Class; c != 0 {
				out.Lines[i][j].Text += fmt.Sprintf("#%d", c)
			}
		}
	}

	return out.debug()
}
