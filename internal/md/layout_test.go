package md

import (
	"fmt"
	"slices"
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
)

const (
	iconNote  = "\U000F02FD"
	iconTip   = "\U000F0336"
	iconImpt  = "\U000F0A04"
	iconWarn  = "\U000F002A"
	iconCaut  = "\U000F0CE6"
	iconImage = "\U000F0976"
)

func layoutWith(src string, o LayoutOptions, base string) Layout {
	return Parse([]byte(src), ParseOptions{Base: base}).Layout(o)
}

func wantLines(t *testing.T, l Layout, want ...string) {
	t.Helper()

	got := l.Plain()
	if fmt.Sprintf("%q", got) != fmt.Sprintf("%q", want) {
		t.Errorf("lines =\n%q\nwant\n%q", got, want)
	}
}

// run returns the first run of line i whose text contains s.
func run(t *testing.T, l Layout, i int, s string) Run {
	t.Helper()

	for _, r := range l.Lines[i] {
		if strings.Contains(r.Text, s) {
			return r
		}
	}

	t.Fatalf("line %d %q has no run with %q", i, plainLine(l.Lines[i]), s)

	return Run{}
}

func TestHeadings(t *testing.T) {
	t.Parallel()

	src := "# Title\n\n## Two\n\n### Three\n\n###### Six\n\nSetext\n======\n\nSub\n---\n"
	l := layoutOf(src, 20, "")
	rule := strings.Repeat("─", 20)
	wantLines(t, l, "Title", rule, "", "Two", "", "Three", "", "Six", "", "Setext", rule, "", "Sub")

	for i, role := range map[int]Role{0: RoleHeading1, 3: RoleHeading2, 5: RoleHeading3, 7: RoleHeading3, 9: RoleHeading1, 12: RoleHeading2} {
		if r := l.Lines[i][0]; r.Role != role || !r.Bold {
			t.Errorf("line %d run = %+v, want bold role %d", i, r, role)
		}
	}

	if r := l.Lines[1][0]; r.Role != RoleRule || r.Bold {
		t.Errorf("rule run = %+v", r)
	}

	goldenText(t, "headings", l.debug())

	wrapped := layoutOf("# one two three four five", 10, "")
	wantLines(t, wrapped, "one two", "three four", "five", strings.Repeat("─", 10))

	rich := layoutOf("## See [docs](https://e.com) and `code` *now*", 40, "")
	if r := run(t, rich, 0, "docs"); r.Role != RoleLink || !r.Bold || !r.Underline || r.Link != 0 {
		t.Errorf("link in heading = %+v", r)
	}

	if r := run(t, rich, 0, "code"); r.Role != RoleCode || !r.Bold {
		t.Errorf("code in heading = %+v", r)
	}

	if r := run(t, rich, 0, "now"); r.Role != RoleHeading2 || !r.Italic || !r.Bold {
		t.Errorf("emphasis in heading = %+v", r)
	}

	wantLines(t, layoutOf("#\n\ntext", 20, ""), "text")
}

func TestSpacing(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name, src string
		want      []string
	}{
		{"blocks", "para one\n\n- a\n- b\n\npara two\n\n> q\n\n---\n", []string{"para one", "", "  • a", "  • b", "", "para two", "", "▎ q", "", strings.Repeat("─", 30)}},
		{"loose list has no blanks", "- a\n\n- b\n\n  more\n\n  - sub\n", []string{"  • a", "  • b", "    more", "    ◦ sub"}},
		{"quote blocks", "> a\n>\n> - x\n> - y\n>\n> b\n", []string{"▎ a", "▎ ", "▎   • x", "▎   • y", "▎ ", "▎ b"}},
		{"block in item", "- a\n\n  > q\n\n  text\n", []string{"  • a", "    ▎ q", "    text"}},
		{"open fold body", "<details open>\n<summary>S</summary>\n\none\n\ntwo\n\n</details>\n", []string{"▾ S", "  one", "", "  two"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			l := layoutOf(tt.src, 30, "")
			wantLines(t, l, tt.want...)
			goldenText(t, "spacing_"+strings.ReplaceAll(tt.name, " ", "_"), l.debug())
		})
	}
}

func TestBulletsByDepth(t *testing.T) {
	t.Parallel()

	src := "- a\n  - b\n    - c\n      - d\n- long item one two three four\n"
	l := layoutOf(src, 18, "")
	wantLines(t, l, "  • a", "    ◦ b", "      ▪ c", "        ▪ d", "  • long item one", "    two three four")

	for i, want := range []string{"•", "◦", "▪", "▪"} {
		var found bool

		for _, r := range l.Lines[i] {
			if r.Role == RoleBullet && strings.HasPrefix(r.Text, want+" ") {
				found = true
			}
		}

		if !found {
			t.Errorf("line %d has no bullet %q: %q", i, want, plainLine(l.Lines[i]))
		}
	}

	goldenText(t, "bullets", l.debug())
}

func TestOrderedNumbersAlign(t *testing.T) {
	t.Parallel()

	l := layoutOf("9. nine\n10. ten\n11. eleven is long enough to wrap around\n", 24, "")
	wantLines(t, l, "   9. nine", "  10. ten", "  11. eleven is long", "      enough to wrap", "      around")

	if r := l.Lines[0][1]; r.Role != RoleBullet || r.Text != " " || !r.chrome {
		t.Errorf("number pad = %+v", r)
	}

	if r := l.Lines[0][2]; r.Role != RoleBullet || r.Text != "9. " || r.chrome {
		t.Errorf("number run = %+v", r)
	}

	goldenText(t, "ordered", l.debug())

	nested := layoutOf("1. a\n   1. b\n   2. c\n", 20, "")
	wantLines(t, nested, "  1. a", "     1. b", "     2. c")
}

func TestTaskList(t *testing.T) {
	t.Parallel()

	src := "- [x] done [link](https://e.com) and `code`\n- [ ] todo\n- plain\n"
	l := layoutOf(src, 40, "")
	wantLines(t, l, "  ✓ done link and  code ", "  ○ todo", "  • plain")

	if r := l.Lines[0][1]; r.Role != RoleTaskDone || !r.Bold || r.Text != "✓ " {
		t.Errorf("done mark = %+v", r)
	}

	if r := run(t, l, 0, "done"); r.Role != RoleTaskDoneText {
		t.Errorf("done text = %+v", r)
	}

	if r := run(t, l, 0, "link"); r.Role != RoleLink {
		t.Errorf("link in done item = %+v", r)
	}

	if r := run(t, l, 0, "code"); r.Role != RoleCode {
		t.Errorf("code in done item = %+v", r)
	}

	if r := l.Lines[1][1]; r.Role != RoleTaskOpen || r.Text != "○ " {
		t.Errorf("open mark = %+v", r)
	}

	if r := run(t, l, 1, "todo"); r.Role != RoleText {
		t.Errorf("open text = %+v", r)
	}

	goldenText(t, "tasks", l.debug())

	icons := layoutWith(src, LayoutOptions{Width: 40, Icons: true}, "")
	if icons.Plain()[0] != l.Plain()[0] {
		t.Errorf("icons change the task marks: %q", icons.Plain()[0])
	}
}

func TestQuote(t *testing.T) {
	t.Parallel()

	l := layoutOf("> a [l](https://e.com) `c`\n>\n> b\n\n> > nested\n> back\n", 20, "")
	wantLines(t, l, "▎ a l  c ", "▎ ", "▎ b", "", "▎ ▎ nested back")

	nested := layoutOf("> outer\n>\n> > inner text that wraps here\n", 16, "")
	wantLines(t, nested, "▎ outer", "▎ ", "▎ ▎ inner text", "▎ ▎ that wraps", "▎ ▎ here")

	for i := range l.Lines {
		if i == 3 {
			continue
		}

		if r := l.Lines[i][0]; r.Role != RoleQuoteBar || !strings.HasPrefix(r.Text, "▎ ") {
			t.Errorf("line %d first run = %+v", i, r)
		}
	}

	if r := run(t, l, 0, "a"); r.Role != RoleQuoteText {
		t.Errorf("quote text = %+v", r)
	}

	if r := run(t, l, 0, "l"); r.Role != RoleLink {
		t.Errorf("link in quote = %+v", r)
	}

	goldenText(t, "quote", l.debug()+nested.debug())
}

func TestAlerts(t *testing.T) {
	t.Parallel()

	kinds := []struct {
		marker, title, icon string
		role                Role
	}{
		{"NOTE", "Note", iconNote, RoleAlertNote},
		{"TIP", "Tip", iconTip, RoleAlertTip},
		{"IMPORTANT", "Important", iconImpt, RoleAlertImportant},
		{"WARNING", "Warning", iconWarn, RoleAlertWarning},
		{"CAUTION", "Caution", iconCaut, RoleAlertCaution},
	}

	var all strings.Builder

	for _, k := range kinds {
		src := "> [!" + k.marker + "]\n> body text\n>\n> second *para*\n"

		on := layoutWith(src, LayoutOptions{Width: 30, Icons: true}, "")
		wantLines(t, on, "▎ "+k.icon+" "+k.title, "▎ body text", "▎ ", "▎ second para")

		off := layoutWith(src, LayoutOptions{Width: 30}, "")
		wantLines(t, off, "▎ "+k.title, "▎ body text", "▎ ", "▎ second para")

		for i := range on.Lines {
			if r := on.Lines[i][0]; r.Role != k.role || r.Text != "▎ " || r.Bold {
				t.Errorf("%s line %d bar = %+v", k.marker, i, r)
			}
		}

		if r := on.Lines[0][1]; r.Role != k.role || !r.Bold || r.Text != k.icon+" " || !r.chrome {
			t.Errorf("%s icon = %+v", k.marker, r)
		}

		if r := on.Lines[0][2]; r.Role != k.role || !r.Bold || r.Text != k.title || r.chrome {
			t.Errorf("%s title = %+v", k.marker, r)
		}

		if r := on.Lines[1][1]; r.Role != RoleText {
			t.Errorf("%s body = %+v", k.marker, r)
		}

		all.WriteString(on.debug() + off.debug())
	}

	goldenText(t, "alerts", all.String())
}

func TestDetailsFold(t *testing.T) {
	t.Parallel()

	src := "intro\n\n<details>\n<summary>Why <b>so</b></summary>\n\nbody one\n\n<details>\n<summary>Inner</summary>\n\ndeep\n\n</details>\n\n</details>\n\nafter\n"

	folded := layoutOf(src, 40, "")
	wantLines(t, folded, "intro", "", "▸ Why so   enter opens", "", "after")
	wantFolds(t, folded, Fold{ID: 0, Line: 2, Open: false})

	if r := folded.Lines[2][0]; r.Role != RoleSummaryMark || !r.Bold || r.Text != "▸ " {
		t.Errorf("mark = %+v", r)
	}

	if r := folded.Lines[2][1]; r.Role != RoleSummary || !r.Bold {
		t.Errorf("summary = %+v", r)
	}

	if r := folded.Lines[2][2]; r.Role != RoleFoldHint || r.Text != "   enter opens" {
		t.Errorf("hint = %+v", r)
	}

	open := layoutWith(src, LayoutOptions{Width: 40, Open: map[int]bool{0: true}}, "")
	wantLines(t, open, "intro", "", "▾ Why so", "  body one", "", "  ▸ Inner   enter opens", "", "after")
	wantFolds(t, open, Fold{ID: 0, Line: 2, Open: true}, Fold{ID: 1, Line: 5, Open: false})

	both := layoutWith(src, LayoutOptions{Width: 40, Open: map[int]bool{0: true, 1: true}}, "")
	wantLines(t, both, "intro", "", "▾ Why so", "  body one", "", "  ▾ Inner", "    deep", "", "after")
	wantFolds(t, both, Fold{ID: 0, Line: 2, Open: true}, Fold{ID: 1, Line: 5, Open: true})

	goldenText(t, "details", folded.debug()+open.debug()+both.debug())

	byDefault := layoutOf("<details open>\n<summary>S</summary>\n\nx\n\n</details>\n", 30, "")
	wantLines(t, byDefault, "▾ S", "  x")
	wantFolds(t, byDefault, Fold{ID: 0, Line: 0, Open: true})

	closed := layoutWith("<details open>\n<summary>S</summary>\n\nx\n\n</details>\n", LayoutOptions{Width: 30, Open: map[int]bool{0: false}}, "")
	wantLines(t, closed, "▸ S   enter opens")
	wantFolds(t, closed, Fold{ID: 0, Line: 0, Open: false})
}

func TestDetailsTagsInParagraphs(t *testing.T) {
	t.Parallel()

	// The closer glued to the image ends the fold, so the rest of the document
	// shows below it instead of hiding inside it.
	glued := "<details>\n<summary>Screenshot</summary>\n\n![shot](https://e.com/s.png)</details>\n\n## After\n\nMore text.\n"

	folded := layoutOf(glued, 40, "")
	wantLines(t, folded, "▸ Screenshot   enter opens", "", "After", "", "More text.")
	wantFolds(t, folded, Fold{ID: 0, Line: 0, Open: false})

	open := layoutWith(glued, LayoutOptions{Width: 40, Open: map[int]bool{0: true}}, "")
	wantLines(t, open, "▾ Screenshot", "  image: shot  e.com", "", "After", "", "More text.")

	inline := layoutWith("before <details open><summary>s</summary>body</details> after\n", LayoutOptions{Width: 40}, "")
	wantLines(t, inline, "before", "", "▾ s", "  body", "", "after")

	attr := layoutOf("<details title=\"a>b\" open>\n<summary>S</summary>\n\nbody\n\n</details>\n", 40, "")
	wantLines(t, attr, "▾ S", "  body")
}

func TestDetailsCloserNestedInParagraph(t *testing.T) {
	t.Parallel()

	for _, tt := range []struct{ name, para, text string }{
		{"strong", "**log</details>**", "log"},
		{"emphasis", "*log</details>*", "log"},
		{"link text", "[log</details>](https://e.com)", "log"},
		{"strong around a link", "**a [b](https://e.com)</details>**", "a b"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			src := "<details>\n<summary>S</summary>\n\n" + tt.para + "\n\n## After\n\nMore text.\n"

			// The closer ends the fold after its paragraph, so the rest of the
			// document shows below the fold, not inside it.
			folded := layoutOf(src, 40, "")
			wantLines(t, folded, "▸ S   enter opens", "", "After", "", "More text.")

			open := layoutWith(src, LayoutOptions{Width: 40, Open: map[int]bool{0: true}}, "")
			wantLines(t, open, "▾ S", "  "+strings.Join(strings.Fields(tt.text), " "), "", "After", "", "More text.")

			for _, line := range open.Plain() {
				if strings.Contains(line, "details") || strings.Contains(line, "<") {
					t.Errorf("the tag shows as text: %q", line)
				}
			}
		})
	}

	// With no fold open the nested closer is a stray and drops out.
	wantLines(t, layoutOf("plain **text</details>** more\n\nafter\n", 40, ""), "plain text more", "", "after")
}

func TestDetailsSummaryWraps(t *testing.T) {
	t.Parallel()

	src := "<details>\n<summary>a long summary that wraps over lines</summary>\n\nx\n\n</details>\n"

	wide := layoutOf(src, 30, "")
	wantLines(t, wide, "▸ a long summary that wraps", "  over lines   enter opens")

	narrow := layoutOf(src, 20, "")
	wantLines(t, narrow, "▸ a long summary", "  that wraps over", "  lines", "  enter opens")
	wantFolds(t, narrow, Fold{ID: 0, Line: 0, Open: false})

	open := layoutWith(src, LayoutOptions{Width: 20, Open: map[int]bool{0: true}}, "")
	wantLines(t, open, "▾ a long summary", "  that wraps over", "  lines", "  x")

	goldenText(t, "details_wrap", wide.debug()+narrow.debug()+open.debug())
}

func wantFolds(t *testing.T, l Layout, want ...Fold) {
	t.Helper()

	if fmt.Sprint(l.Folds) != fmt.Sprint(want) {
		t.Errorf("folds = %v, want %v", l.Folds, want)
	}
}

func TestFootnotesReachedThroughNotes(t *testing.T) {
	t.Parallel()

	rule := strings.Repeat("─", 16)

	tests := []struct {
		name string
		src  string
		want []string
	}{
		{
			name: "a note referenced only from another note",
			src:  "one[^1] two[^2]\n\n[^1]: see [^3]\n\n[^2]: second\n\n[^3]: third\n",
			want: []string{"one[1] two[2]", "", rule, " 1. see [3]", " 2. second", " 3. third"},
		},
		{
			name: "a chain of notes",
			src:  "x[^a]\n\n[^a]: first [^b]\n\n[^b]: middle [^c]\n\n[^c]: last\n",
			want: []string{"x[1]", "", rule, " 1. first [2]", " 2. middle [3]", " 3. last"},
		},
		{
			// goldmark numbers a note by its first reference in the source.
			name: "a chain of notes defined in reverse",
			src:  "x[^a]\n\n[^c]: last\n\n[^b]: middle [^c]\n\n[^a]: first [^b]\n",
			want: []string{"x[1]", "", rule, " 1. first [3]", " 2. last", " 3. middle [2]"},
		},
		{
			name: "notes that refer to each other end",
			src:  "x[^1]\n\n[^1]: one [^2]\n\n[^2]: two [^1]\n",
			want: []string{"x[1]", "", rule, " 1. one [2]", " 2. two [1]"},
		},
		{
			name: "a note that refers to itself",
			src:  "x[^1]\n\n[^1]: me [^1]\n",
			want: []string{"x[1]", "", rule, " 1. me [1]"},
		},
		{
			name: "the first definition of a label wins",
			src:  "x[^1]\n\n[^1]: first\n\n[^1]: second\n",
			want: []string{"x[1]", "", rule, " 1. first"},
		},
		{
			name: "a note that nothing shown refers to stays out",
			src:  "x[^1]\n\n[^1]: one\n\n[^2]: two [^3]\n\n[^3]: three\n",
			want: []string{"x[1]", "", rule, " 1. one"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			wantLines(t, layoutOf(tt.src, 24, ""), tt.want...)
		})
	}
}

func TestThematicBreak(t *testing.T) {
	t.Parallel()

	l := layoutOf("a\n\n---\n\nb\n\n***\n", 10, "")
	rule := strings.Repeat("─", 10)
	wantLines(t, l, "a", "", rule, "", "b", "", rule)

	if r := l.Lines[2][0]; r.Role != RoleRule {
		t.Errorf("rule = %+v", r)
	}

	goldenText(t, "break", l.debug())
}

func TestBreakAfterImage(t *testing.T) {
	t.Parallel()

	const a, b = "https://e.com/a.png", "https://e.com/b.png"

	tests := []struct {
		name string
		src  string
		want []string
	}{
		{"br", "![Logo](" + a + ")<br>tagline\n", []string{"image: Logo  e.com", "tagline"}},
		{"backslash break", "![Logo](" + a + ")\\\ntagline\n", []string{"image: Logo  e.com", "tagline"}},
		{"two-space break", "![Logo](" + a + ")  \ntagline\n", []string{"image: Logo  e.com", "tagline"}},
		{"spaces around the br", "![Logo](" + a + ") <br> tagline\n", []string{"image: Logo  e.com", "tagline"}},
		{"a second break still makes a blank line", "![Logo](" + a + ")<br><br>tagline\n", []string{"image: Logo  e.com", "", "tagline"}},
		{"text before the image", "see ![a](" + a + ")<br>after\n", []string{"see", "image: a  e.com", "after"}},
		{"two images", "![a](" + a + ")<br>![b](" + b + ")\n", []string{"image: a  e.com", "image: b  e.com"}},
		{
			"html block",
			"<p align=\"center\">\n  <img src=\"" + a + "\" alt=\"L\">\n  <br>\n  <b>G</b> tagline\n</p>\n",
			[]string{"image: L  e.com", "G tagline"},
		},
		{"a break before the image is kept", "x<br>![a](" + a + ")\n", []string{"x", "image: a  e.com"}},
		{
			"a paragraph tag after the image still separates",
			"<p><img src=\"" + a + "\" alt=\"L\"></p>\n<p>after</p>\n",
			[]string{"image: L  e.com", "", "after"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			wantLines(t, layoutOf(tt.src, 40, ""), tt.want...)
		})
	}
}

func TestFootnotesSection(t *testing.T) {
	t.Parallel()

	src := "Text[^b] and[^a].\n\n[^a]: First note.\n[^b]: Second note that wraps\n[^c]: unused\n"
	l := layoutOf(src, 24, "")
	wantLines(t, l, "Text[1] and[2].", "", strings.Repeat("─", 16), " 1. Second note that", "    wraps", " 2. First note.")

	if r := l.Lines[2][0]; r.Role != RoleFootnoteRule {
		t.Errorf("rule = %+v", r)
	}

	if r := l.Lines[3][0]; r.Role != RoleFootnoteNumber || r.Text != " " || !r.chrome {
		t.Errorf("number pad = %+v", r)
	}

	if r := l.Lines[3][1]; r.Role != RoleFootnoteNumber || r.Text != "1. " || r.chrome {
		t.Errorf("number = %+v", r)
	}

	if r := run(t, l, 3, "Second"); r.Role != RoleFootnoteText {
		t.Errorf("text = %+v", r)
	}

	goldenText(t, "footnotes", l.debug())

	wantLines(t, layoutOf("[^a]: lonely\n", 20, ""))
	wantLines(t, layoutOf("x\n\n[^a]: lonely\n", 20, ""), "x")

	narrow := layoutOf(src, 8, "")
	assertSafe(t, narrow, 8)

	if !slices.Contains(narrow.Plain(), strings.Repeat("─", 8)) {
		t.Errorf("no 8-cell footnote rule in %q", narrow.Plain())
	}

	rich := layoutOf("A[^1].\n\n[^1]: has [a link](https://e.com) and `code`\n\n    second para\n", 40, "")
	if r := run(t, rich, 3, "a link"); r.Role != RoleLink {
		t.Errorf("link in footnote = %+v", r)
	}
}

func TestImages(t *testing.T) {
	t.Parallel()

	t.Run("inline image takes its own line", func(t *testing.T) {
		l := layoutWith("before ![logo](https://example.com/a.png) after", LayoutOptions{Width: 40, Icons: true}, "")
		wantLines(t, l, "before", iconImage+" logo  example.com", "after")

		if fmt.Sprint(l.Images) != fmt.Sprint([]Image{{Line: 1, Alt: "logo", Src: "https://example.com/a.png", Dest: "https://example.com/a.png", Link: -1}}) {
			t.Errorf("images = %+v", l.Images)
		}

		if r := l.Lines[1][0]; r.Role != RoleImage || r.Text != iconImage+" logo  " {
			t.Errorf("image run = %+v", r)
		}

		if r := l.Lines[1][1]; r.Role != RoleImageHost || r.Text != "example.com" {
			t.Errorf("host run = %+v", r)
		}

		goldenText(t, "image_inline", l.debug())
	})

	t.Run("html image", func(t *testing.T) {
		l := layoutWith("<img src=\"https://x.io/b.png\" alt=\"B &amp; W\">\n", LayoutOptions{Width: 40, Icons: true}, "")
		wantLines(t, l, iconImage+" B & W  x.io")

		if len(l.Images) != 1 || l.Images[0].Alt != "B & W" || l.Images[0].Src != "https://x.io/b.png" {
			t.Errorf("images = %+v", l.Images)
		}

		inline := layoutWith("see <img src=\"https://x.io/c.png\" alt=\"C\"/> here", LayoutOptions{Width: 40, Icons: true}, "")
		wantLines(t, inline, "see", iconImage+" C  x.io", "here")
	})

	t.Run("badge in a markdown link", func(t *testing.T) {
		l := layoutWith("[![build](https://img.shields.io/b.svg)](https://ci.example.com/run)\n", LayoutOptions{Width: 50, Icons: true}, "")
		wantLines(t, l, iconImage+" build  img.shields.io")

		if len(l.Links) != 1 || l.Links[0].URL != "https://ci.example.com/run" {
			t.Errorf("links = %+v", l.Links)
		}

		if len(l.Images) != 1 || l.Images[0].Link != 0 {
			t.Errorf("images = %+v", l.Images)
		}

		for _, r := range l.Lines[0] {
			if r.Link != 0 || !r.Underline {
				t.Errorf("run = %+v, want link 0 and underline", r)
			}
		}

		goldenText(t, "image_badge", l.debug())
	})

	t.Run("badge in an html link", func(t *testing.T) {
		l := layoutWith("<a href=\"https://ci.example.com/\"><img src=\"https://img.shields.io/b.svg\" alt=\"ci\"></a>\n", LayoutOptions{Width: 50}, "")
		wantLines(t, l, "image: ci  img.shields.io")

		if len(l.Links) != 1 || len(l.Images) != 1 || l.Images[0].Link != 0 {
			t.Errorf("links = %+v images = %+v", l.Links, l.Images)
		}

		for _, r := range l.Lines[0] {
			if r.Link != 0 || !r.Underline {
				t.Errorf("run = %+v", r)
			}
		}
	})

	t.Run("badge rows drop the whitespace between images", func(t *testing.T) {
		l := layoutWith("![a](https://h.io/a) ![b](https://h.io/b)\n![c](https://h.io/c)\n", LayoutOptions{Width: 40}, "")
		wantLines(t, l, "image: a  h.io", "image: b  h.io", "image: c  h.io")

		html := layoutWith("<p>\n  <img src=\"https://h.io/a\" alt=\"a\">\n  <img src=\"https://h.io/b\" alt=\"b\">\n</p>\n", LayoutOptions{Width: 40}, "")
		wantLines(t, html, "image: a  h.io", "image: b  h.io")
		goldenText(t, "image_row", l.debug()+html.debug())
	})

	t.Run("local source shows its file name", func(t *testing.T) {
		l := layoutWith("![diagram](docs/img/arch.png)\n", LayoutOptions{Width: 40}, "/repo")
		wantLines(t, l, "image: diagram  arch.png")

		if len(l.Images) != 1 || l.Images[0].Src != "/repo/docs/img/arch.png" {
			t.Errorf("images = %+v", l.Images)
		}

		url := layoutWith("![d](img/a.png)\n", LayoutOptions{Width: 40}, "https://example.com/docs/")
		wantLines(t, url, "image: d  example.com")

		if url.Images[0].Src != "https://example.com/docs/img/a.png" {
			t.Errorf("src = %q", url.Images[0].Src)
		}

		goldenText(t, "image_local", l.debug()+url.debug())
	})

	t.Run("icons off and empty alt", func(t *testing.T) {
		off := layoutWith("![](https://example.com/a.png)\n", LayoutOptions{Width: 40}, "")
		wantLines(t, off, "image: example.com")

		on := layoutWith("![](https://example.com/a.png)\n", LayoutOptions{Width: 40, Icons: true}, "")
		wantLines(t, on, iconImage+" example.com")

		goldenText(t, "image_icons", off.debug()+on.debug())
	})

	t.Run("unusable sources keep the line without a host", func(t *testing.T) {
		for _, src := range []string{
			"![x](ftp://h.io/a.png)", "![x](data:image/png;base64,AAAA)", "![x](mailto:a@b.io)",
			"![x](https://h.io/\u202e.png)", "![x](a.png)",
		} {
			l := layoutOf(src, 40, "")
			wantLines(t, l, "image: x")

			if len(l.Images) != 1 || l.Images[0].Src != "" {
				t.Errorf("%q: images = %+v", src, l.Images)
			}
		}
	})

	t.Run("cut at the width", func(t *testing.T) {
		l := layoutOf("![a long alt text here](https://example.com/a.png)\n", 16, "")
		wantLines(t, l, "image: a long a…")
		assertSafe(t, l, 16)
	})

	t.Run("empty links add none", func(t *testing.T) {
		l := layoutOf("[](https://e.com) and <a href=\"https://f.com\"></a> x\n", 40, "")
		wantLines(t, l, "and x")

		if len(l.Links) != 0 {
			t.Errorf("links = %+v", l.Links)
		}
	})

	t.Run("links renumber after unused ones drop", func(t *testing.T) {
		l := layoutOf("[](https://e.com) [real](https://real.com)\n", 40, "")
		if len(l.Links) != 1 || l.Links[0].URL != "https://real.com" || run(t, l, 0, "real").Link != 0 {
			t.Errorf("links = %+v", l.Links)
		}
	})
}

func TestHTMLBlocks(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name, src string
		want      []string
	}{
		{"centered paragraph", "<p align=\"center\">\n  <b>Hi</b> there\n  <br>\n  <a href=\"https://e.com\">link</a>\n</p>\n", []string{"Hi there", "link"}},
		{"div breaks", "<div>one</div><div>two</div>\n", []string{"one", "two"}},
		{"list rows and headings", "<ul><li>a</li><li>b</li></ul>\n<h2>H</h2>\n", []string{"a", "b", "H"}},
		{"source newlines are spaces", "<div>\nsome\ntext\n</div>\n", []string{"some text"}},
		{"script is dropped", "<script>\nalert(1)\n</script>\n\nafter\n", []string{"after"}},
		{"style is dropped", "<style>\np { color: red }\n</style>\n\nafter\n", []string{"after"}},
		{"comment is dropped", "<!-- note -->\n\nafter\n", []string{"after"}},
		{"kbd and em", "<p><kbd>Esc</kbd> <em>soft</em></p>\n", []string{"Esc soft"}},
		{"entities decode", "<p>a &amp; b &lt;c&gt;</p>\n", []string{"a & b <c>"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			wantLines(t, layoutOf(tt.src, 40, ""), tt.want...)
		})
	}

	l := layoutOf("<p><kbd>Esc</kbd> <em>soft</em> <a href=\"https://e.com\">go</a></p>\n", 40, "")
	if r := run(t, l, 0, "Esc"); r.Role != RoleKey || !r.Bold {
		t.Errorf("kbd = %+v", r)
	}

	if r := run(t, l, 0, "soft"); !r.Italic {
		t.Errorf("em = %+v", r)
	}

	if r := run(t, l, 0, "go"); r.Link != 0 || r.Role != RoleLink {
		t.Errorf("anchor = %+v", r)
	}

	goldenText(t, "html_block", l.debug())
}

func TestFragmentsInFolds(t *testing.T) {
	t.Parallel()

	l := layoutWith("<details><summary>S</summary>text <b>bold</b><br>more</details>\n", LayoutOptions{Width: 30, Open: map[int]bool{0: true}}, "")
	wantLines(t, l, "▾ S", "  text bold", "  more")

	if r := run(t, l, 1, "bold"); !r.Bold {
		t.Errorf("fragment tag styling = %+v", r)
	}
}

func TestHostileText(t *testing.T) {
	t.Parallel()

	const bad = "x\x1b[31m\u202ey\x1b]8;;evil\x07z"

	srcs := map[string]string{
		"summary":  "<details><summary>" + bad + "</summary>body</details>\n",
		"fragment": "<details><summary>S</summary>" + bad + "</details>\n",
		"alt":      "![" + bad + "](https://h.io/a.png)\n",
		"html alt": "<img src=\"https://h.io/a.png\" alt=\"" + bad + "\">\n",
		"heading":  "# " + bad + "\n",
		"quote":    "> " + bad + "\n",
		"item":     "- " + bad + "\n",
		"html":     "<div>" + bad + "</div>\n",
	}
	for name, src := range srcs {
		t.Run(name, func(t *testing.T) {
			for _, open := range []bool{false, true} {
				l := layoutWith(src, LayoutOptions{Width: 30, Icons: true, Open: map[int]bool{0: open}}, "")
				assertSafe(t, l, 30)

				for i, im := range l.Images {
					if strings.ContainsFunc(im.Alt, func(r rune) bool { return r < 0x20 || r == 0x202e }) {
						t.Errorf("image %d alt %q", i, im.Alt)
					}
				}

				if p := strings.Join(l.Plain(), "\n"); strings.ContainsAny(p, "\x1b\x07\u202e") {
					t.Errorf("plain text holds a hostile character: %q", p)
				}
			}
		})
	}
}

func TestNestingCap(t *testing.T) {
	t.Parallel()

	var lists strings.Builder
	for i := 1; i <= 30; i++ {
		lists.WriteString(strings.Repeat("  ", i-1) + fmt.Sprintf("- L%d\n", i))
	}

	var quotes strings.Builder
	for i := 1; i <= 30; i++ {
		quotes.WriteString(strings.Repeat(">", i) + fmt.Sprintf(" Q%d\n", i))
		quotes.WriteString(strings.Repeat(">", i) + "\n")
	}

	for name, src := range map[string]string{"lists": lists.String(), "quotes": quotes.String()} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			for _, w := range []int{1, 2, 5, 10} {
				assertSafe(t, layoutOf(src, w, ""), w)
			}

			l := layoutOf(src, 16, "")
			assertSafe(t, l, 16)

			wide := layoutOf(src, 100, "")
			assertSafe(t, wide, 100)

			text := strings.Join(wide.Plain(), "\n")
			for i := 1; i <= 30; i++ {
				if !strings.Contains(text, fmt.Sprintf("L%d", i)) && !strings.Contains(text, fmt.Sprintf("Q%d", i)) {
					t.Errorf("level %d is missing from the layout", i)
				}
			}

			goldenText(t, "nesting_"+name, wide.debug())
		})
	}
}

func TestBlocksWidths(t *testing.T) {
	t.Parallel()

	src := "# Head [link](https://e.com)\n\n- one `code` two\n  - [x] nested task\n    1. deep\n\n> [!WARNING]\n> careful *now*\n\n<details open>\n<summary>A long summary</summary>\n\n- in\n\n![alt](https://example.com/a.png)\n\n</details>\n\nText[^1] 世界 wide\n\n---\n\n[^1]: note\n"
	for _, w := range []int{1, 2, 3, 5, 10, 20, 40} {
		l := layoutWith(src, LayoutOptions{Width: w, Icons: true}, "")
		assertSafe(t, l, w)

		for _, line := range l.Lines {
			if ansi.StringWidth(plainLine(line)) > w {
				t.Fatalf("width %d: %q", w, plainLine(line))
			}
		}
	}
}

func TestBlocksGolden(t *testing.T) {
	t.Parallel()

	src := "# Review drawer\n\nIntro with [a link](https://e.com) and `code`.\n\n## Checklist\n\n- [x] tests\n- [ ] docs\n  - nested\n\n> [!NOTE]\n> Mind the gap.\n\n<details>\n<summary>Details</summary>\n\nhidden\n\n</details>\n\n![badge](https://img.shields.io/b.svg)\n\nDone[^1].\n\n[^1]: The note.\n"

	for _, w := range []int{30, 60} {
		goldenText(t, fmt.Sprintf("blocks.%d", w), layoutWith(src, LayoutOptions{Width: w, Icons: true}, "").debug())
	}
}

func TestNestedFoldsStopIndenting(t *testing.T) {
	t.Parallel()

	var b strings.Builder

	const n = 30

	for i := 1; i <= n; i++ {
		fmt.Fprintf(&b, "<details open>\n<summary>F%d</summary>\n\n", i)
	}

	b.WriteString("deep\n\n" + strings.Repeat("</details>\n\n", n))

	open := map[int]bool{}
	for i := range n {
		open[i] = true
	}

	src := b.String()

	for _, w := range []int{1, 2, 10} {
		assertSafe(t, layoutWith(src, LayoutOptions{Width: w, Open: open}, ""), w)
	}

	l := layoutWith(src, LayoutOptions{Width: 100, Open: open}, "")
	assertSafe(t, l, 100)

	if len(l.Folds) != n {
		t.Fatalf("folds = %d, want %d", len(l.Folds), n)
	}

	indent := func(i int) int {
		line := plainLine(l.Lines[l.Folds[i].Line])

		return len(line) - len(strings.TrimLeft(line, " "))
	}

	if indent(15) != 30 || indent(16) != 32 || indent(29) != 32 {
		t.Errorf("indents = %d, %d, %d; want 30, 32, 32", indent(15), indent(16), indent(29))
	}

	if text := strings.Join(l.Plain(), "\n"); !strings.Contains(text, "deep") || !strings.Contains(text, "F30") {
		t.Error("content missing")
	}
}

// nestedItem lays out one list item under the given number of quotes. Quotes
// past maxNest draw no bar, so under maxNest or more quotes the bars take
// 2*maxNest cells and what is left of width is the item's room.
func nestedItem(item string, quotes, width int, icons bool) Layout {
	lines := strings.Split(item, "\n")
	for range quotes {
		lines = nestLines("quote", 0, lines)
	}

	return layoutWith(strings.Join(lines, "\n")+"\n", LayoutOptions{Width: width, Icons: icons}, "")
}

// TestCappedListKeepsMarksBelowItsFirstLine pins what a capped list item leaves
// to the lines under its first: a capped item draws its marker on the first line
// only, so only that line is narrower than the room its quotes leave.
func TestCappedListKeepsMarksBelowItsFirstLine(t *testing.T) {
	t.Parallel()

	bars := strings.Repeat("▎ ", maxNest)

	tests := []struct {
		name  string
		item  string
		width int
		icons bool
		want  []string
	}{
		{
			// 3 cells of room below the bars: the fold line keeps its mark and cuts its text
			name:  "a fold under the first line",
			item:  "- 0<details><summary>S</summary>x</details>",
			width: 2*maxNest + 3,
			icons: true,
			want:  []string{bars + "• 0", bars + "▸ S", bars + "  …"},
		},
		{
			// 8 cells of room: the image line keeps its 7-cell mark and cuts the rest
			name:  "an image under the first line",
			item:  "- ab ![i](https://e.com/i.png)",
			width: 2*maxNest + 8,
			icons: false,
			want:  []string{bars + "• ab", bars + "image: …"},
		},
		{
			// 2 cells of room, a 3-cell marker: it reserves nothing and is dropped, and the image keeps its icon
			name:  "a marker wider than the room",
			item:  "1. ![i](https://e.com/i.png)",
			width: 2*maxNest + 2,
			icons: true,
			want:  []string{bars + iconImage + "…"},
		},
		{
			name:  "text wraps in the room the marker leaves, then in the whole room",
			item:  "- aa bb cc dd ee ff",
			width: 2*maxNest + 8,
			icons: false,
			want:  []string{bars + "• aa bb", bars + "cc dd ee", bars + "ff"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			l := nestedItem(tt.item, maxNest, tt.width, tt.icons)
			assertSafe(t, l, tt.width)
			assertReferences(t, l, tt.icons, tt.width)
			wantLines(t, l, tt.want...)
		})
	}
}

// TestFirstBlockOfACappedItem pins how a block that starts a capped item takes
// the room its marker leaves, 12 cells under the bars. A block that is one unit
// (a fold head, a code box, a table, a rule) is laid out whole in the room left
// after the marker; the marker stays on its first line. The recursive layout
// (maxNest quotes) and the flat walk (maxDepth quotes) must agree.
func TestFirstBlockOfACappedItem(t *testing.T) {
	t.Parallel()

	const room = 12

	bars := strings.Repeat("▎ ", maxNest)

	tests := []struct {
		name string
		item string
		want []string
	}{
		{"fold head", "- <details><summary>Summary</summary>body</details>", []string{"• ▸ Summary", "  enter o…"}},
		{"image", "- ![alt](https://e.com/a.png)", []string{"• image: al…"}},
		{"code box", "- ```go\n  x := 1\n  ```", []string{"• ╭─ go ───╮", "│ 1 x :… │", "╰────────╯"}},
		{
			"table", "- | aaaa | bbbb |\n  |---|---|\n  | 1 | 2 |",
			[]string{"• ╭──────┬─…", "│ aaaa │ …", "├──────┼─…", "│ 1    │ …", "╰──────┴─…"},
		},
		{"rule", "- ***", []string{"• ──────────"}},
		{"empty item", "-", []string{"• "}},
	}

	for _, quotes := range []int{maxNest, maxDepth} {
		for _, tt := range tests {
			t.Run(fmt.Sprintf("%d quotes/%s", quotes, tt.name), func(t *testing.T) {
				l := nestedItem(tt.item, quotes, 2*maxNest+room, false)
				assertSafe(t, l, 2*maxNest+room)
				assertReferences(t, l, false, 2*maxNest+room)

				want := make([]string, len(tt.want))
				for i, w := range tt.want {
					want[i] = bars + w
				}

				wantLines(t, l, want...)
			})
		}
	}
}

func TestCappedListKeepsMarkers(t *testing.T) {
	t.Parallel()

	var b strings.Builder
	for i := 1; i <= 20; i++ {
		b.WriteString(strings.Repeat("   ", i-1) + fmt.Sprintf("%d. L%d\n", 1, i))
	}

	b.WriteString(strings.Repeat("   ", 20) + "- [x] done\n")

	plain := layoutOf(b.String(), 100, "").Plain()
	if got := plain[19]; !strings.HasSuffix(got, "1. L20") || strings.Index(got, "1.") != strings.Index(plain[16], "1.") {
		t.Errorf("line 19 = %q, line 16 = %q", got, plain[16])
	}

	if got := plain[20]; !strings.HasSuffix(got, "✓ done") {
		t.Errorf("task line = %q", got)
	}
}

func TestFoldHintKey(t *testing.T) {
	t.Parallel()

	const src = "<details>\n<summary>Why</summary>\n\nbody\n\n</details>\n"

	tests := []struct {
		name string
		key  *string
		want string
	}{
		{"default", nil, "▸ Why   enter opens"},
		{"another key", new("o"), "▸ Why   o opens"},
		{"no key", new(""), "▸ Why"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			wantLines(t, layoutWith(src, LayoutOptions{Width: 40, FoldKey: tt.key}, ""), tt.want)
		})
	}
}
