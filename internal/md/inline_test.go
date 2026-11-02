package md

import (
	"fmt"
	"os"
	"strings"
	"testing"
	"unicode"
	"unicode/utf8"

	"github.com/charmbracelet/x/ansi"

	"github.com/mhersson/aim/internal/git/invisible"
)

func layoutOf(src string, width int, base string) Layout {
	return Parse([]byte(src), ParseOptions{Base: base}).Layout(LayoutOptions{Width: width})
}

// assertSafe fails on a control character or invisible rune in any run, on a
// line wider than max, and on a Link or Path that is not safe to emit.
func assertSafe(t *testing.T, l Layout, maxWidth int) {
	t.Helper()

	for i, line := range l.Lines {
		if w := ansi.StringWidth(plainLine(line)); w > maxWidth {
			t.Errorf("line %d is %d cells wide, max %d: %q", i, w, maxWidth, plainLine(line))
		}

		for _, r := range line {
			if !utf8.ValidString(r.Text) {
				t.Errorf("line %d holds invalid UTF-8: %q", i, r.Text)
			}

			for _, c := range r.Text {
				if unicode.IsControl(c) || (invisible.Rune(c) && c != 0x200d) {
					t.Errorf("line %d holds U+%04X in %q", i, c, r.Text)
				}
			}

			if r.Link < -1 || r.Link >= len(l.Links) {
				t.Errorf("line %d has link index %d of %d", i, r.Link, len(l.Links))
			}

			assertCleanTarget(t, r.Path)
		}
	}

	for _, k := range l.Links {
		assertCleanTarget(t, k.URL)

		if !strings.HasPrefix(k.URL, "http://") && !strings.HasPrefix(k.URL, "https://") && !strings.HasPrefix(k.URL, "mailto:") {
			t.Errorf("link %q is outside the allowed schemes", k.URL)
		}
	}
}

func assertCleanTarget(t *testing.T, s string) {
	t.Helper()

	for _, c := range s {
		if c < 0x20 || c == 0x7f || (c >= 0x80 && c <= 0x9f) || invisible.Rune(c) {
			t.Errorf("target %q holds U+%04X", s, c)
		}
	}
}

func plainLine(line Line) string {
	var b strings.Builder
	for _, r := range line {
		b.WriteString(r.Text)
	}

	return b.String()
}

func TestInlineStyles(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		src  string
		want string
	}{
		{"emphasis strong strike", "*a* **b** ~~c~~ ***d***", "‹Text,i:a› ‹Text,b:b› ‹Text,s:c› ‹Text,b,i:d›\n"},
		{"code gets a space each side", "x `d` y", "x ‹Code: d › y\n"},
		{"kbd", "press <kbd>Ctrl</kbd>+<kbd>C</kbd>", "press ‹Key,b:Ctrl›+‹Key,b:C›\n"},
		{"br breaks the line", "a<br>b<br/>c<br />d", "a\nb\nc\nd\n"},
		{"two trailing spaces break", "a  \nb", "a\nb\n"},
		{"backslash breaks", "a\\\nb", "a\nb\n"},
		{"soft break is a space", "a\nb", "a b\n"},
		{"other tags keep their text", "<sub>x</sub> <sup>y</sup> <span class=\"z\">w</span>", "x y w\n"},
		{"b strong i em", "<b>a</b> <strong>b</strong> <i>c</i> <em>d</em>", "‹Text,b:a› ‹Text,b:b› ‹Text,i:c› ‹Text,i:d›\n"},
		{"comments are dropped", "a <!-- gone --> b", "a b\n"},
		{"entities decode", "a &amp; b &#x27;c&#x27; &lt;d&gt;", "a & b 'c' <d>\n"},
		{"backslash escapes drop the backslash", `\*a\* \\ \#`, "*a* \\ #\n"},
		{"code keeps entities and escapes", "`&amp; \\*`", "‹Code: &amp; \\* ›\n"},
		{"unclosed tag does not leak into the next paragraph", "<b>a\n\nb", "‹Text,b:a›\n\nb\n"},
		{"stray close tag is harmless", "a</b></kbd> b", "a b\n"},
		{"self-closing style tags do not leak", "<b/>a <i/>b <kbd/>c <br/>d", "a b c\nd\n"},
		{"collapsed spaces", "a    b", "a b\n"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			l := layoutOf(tt.src, 80, "")
			if got := l.debug(); got != tt.want {
				t.Errorf("layout of %q:\n got %q\nwant %q", tt.src, got, tt.want)
			}

			assertSafe(t, l, 80)
		})
	}
}

func TestLinkSchemes(t *testing.T) {
	t.Parallel()

	tests := []struct {
		target string
		linked bool
	}{
		{"http://example.com/a", true},
		{"https://example.com/a?b=c#d", true},
		{"HTTPS://example.com/a", true},
		{"mailto:me@example.com", true},
		{"javascript:alert(1)", false},
		{"JavaScript:alert(1)", false},
		{"data:text/html,x", false},
		{"http:///nohost", false},
		{"http://example.com/\u202e", false},
	}
	for _, tt := range tests {
		t.Run(tt.target, func(t *testing.T) {
			l := layoutOf("[text]("+"<"+tt.target+">"+")", 80, "")
			if len(l.Lines) != 1 || len(l.Lines[0]) != 1 {
				t.Fatalf("layout = %q", l.debug())
			}

			r := l.Lines[0][0]
			if r.Role != RoleLink || !r.Underline || r.Text != "text" {
				t.Errorf("run = %+v, want underlined link text", r)
			}

			if tt.linked {
				if r.Link != 0 || len(l.Links) != 1 {
					t.Fatalf("Link = %d of %d, want a link", r.Link, len(l.Links))
				}
			} else if r.Link != -1 || len(l.Links) != 0 {
				t.Errorf("Link = %d of %d, want none", r.Link, len(l.Links))
			}

			assertSafe(t, l, 80)
		})
	}
}

func TestLinkSchemesInHTMLAndAutolinks(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		src  string
		want []string
	}{
		{"anchor", `<a href="https://e.com/a">x</a>`, []string{"https://e.com/a"}},
		{"anchor javascript", `<a href="javascript:alert(1)">x</a>`, nil},
		{"anchor single quotes", `<a href='http://e.com/'>x</a>`, []string{"http://e.com/"}},
		{"anchor unquoted", `<a href=http://e.com/>x</a>`, []string{"http://e.com/"}},
		{"anchor without href", `<a name="n">x</a>`, nil},
		{"self-closing anchor stays open like a browser", `<a href="https://e.com"/>x`, []string{"https://e.com"}},
		{"anchor closed", `<a href="http://e.com/">x</a> y`, []string{"http://e.com/"}},
		{"autolink", `<https://e.com/x>`, []string{"https://e.com/x"}},
		{"email autolink", `<me@e.com>`, []string{"mailto:me@e.com"}},
		{"bare url", `see https://e.com/y now`, []string{"https://e.com/y"}},
		{"bare www", `see www.e.com now`, []string{"http://www.e.com"}},
		{"javascript autolink", `<javascript:alert(1)>`, nil},
		{"two links to one url", `[a](http://e.com) [b](http://e.com)`, []string{"http://e.com", "http://e.com"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			l := layoutOf(tt.src, 80, "")

			var got []string
			for _, k := range l.Links {
				got = append(got, k.URL)
			}

			if fmt.Sprint(got) != fmt.Sprint(tt.want) {
				t.Errorf("links = %v, want %v", got, tt.want)
			}

			assertSafe(t, l, 80)
		})
	}
}

func TestLinkRunsUnderlined(t *testing.T) {
	t.Parallel()

	l := layoutOf("a [b *c*](http://e.com) d", 80, "")

	want := "a ‹Link,u:b ›‹Link,i,u:c› d\n"
	if got := l.debug(); got != want {
		t.Errorf("got %q, want %q", got, want)
	}

	for _, r := range l.Lines[0] {
		wantLink := -1
		if r.Role == RoleLink {
			wantLink = 0
		}

		if r.Link != wantLink {
			t.Errorf("run %q has Link %d, want %d", r.Text, r.Link, wantLink)
		}
	}
}

func TestLinkSpansLinesWithOneIndex(t *testing.T) {
	t.Parallel()

	l := layoutOf("[aaa bbb ccc ddd](http://e.com/x) end", 7, "")
	if len(l.Links) != 1 {
		t.Fatalf("links = %v, want one", l.Links)
	}

	var n int

	for _, line := range l.Lines {
		for _, r := range line {
			if r.Link == 0 {
				n++
			}
		}
	}

	if n < 2 {
		t.Errorf("link covers %d runs, want a run on each of several lines:\n%s", n, l.debug())
	}
}

func TestLinkResolve(t *testing.T) {
	t.Parallel()

	const page = "https://example.com/a/b/c.md"

	tests := []struct {
		name, base, target string
		wantURL, wantPath  string
	}{
		{"url base dotdot", page, "../x.md", "https://example.com/a/x.md", ""},
		{"url base sibling", page, "d/e.md", "https://example.com/a/b/d/e.md", ""},
		{"url base fragment", page, "#x", page + "#x", ""},
		{"url base query", page, "?q=1", "https://example.com/a/b/c.md?q=1", ""},
		{"url base absolute path", page, "/top", "https://example.com/top", ""},
		{"url base protocol relative", page, "//cdn.example.com/y", "https://cdn.example.com/y", ""},
		{"url base absolute target wins", page, "https://other.org/z", "https://other.org/z", ""},
		{"url base javascript", page, "javascript:alert(1)", "", ""},
		{"local base sibling", "/repo/docs", "guide.md", "", "/repo/docs/guide.md"},
		{"local base dotdot", "/repo/docs", "../README.md", "", "/repo/README.md"},
		{"local base dot slash", "/repo/docs", "./img/a.png", "", "/repo/docs/img/a.png"},
		{"local base decodes and drops query and fragment", "/repo/docs", "a%20b.md?x=1#f", "", "/repo/docs/a b.md"},
		{"local base fragment only", "/repo/docs", "#x", "", ""},
		{"local base protocol relative", "/repo/docs", "//host/x", "", ""},
		{"local base absolute http still links", "/repo/docs", "http://e.com/", "http://e.com/", ""},
		{"local base escaped control", "/repo/docs", "a%1bb.md", "", ""},
		{"local base escaped bidi override", "/repo/docs", "a%E2%80%AEb.md", "", ""},
		{"no base relative", "", "x.md", "", ""},
		{"no base fragment", "", "#x", "", ""},
		{"no base protocol relative", "", "//host/x", "", ""},
		{"no base absolute", "", "https://e.com/", "https://e.com/", ""},
		{"relative base is no base", "docs", "x.md", "", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			l := layoutOf("[t](<"+tt.target+">)", 80, tt.base)
			if len(l.Lines) != 1 || len(l.Lines[0]) != 1 {
				t.Fatalf("layout = %q", l.debug())
			}

			r := l.Lines[0][0]
			if r.Path != tt.wantPath {
				t.Errorf("Path = %q, want %q", r.Path, tt.wantPath)
			}

			switch {
			case tt.wantURL == "" && (r.Link != -1 || len(l.Links) != 0):
				t.Errorf("Link = %d (%v), want none", r.Link, l.Links)
			case tt.wantURL != "" && (r.Link != 0 || len(l.Links) != 1 || l.Links[0].URL != tt.wantURL):
				t.Errorf("Link = %d (%v), want %q", r.Link, l.Links, tt.wantURL)
			}

			assertSafe(t, l, 80)
		})
	}
}

func TestLinkTargetSafety(t *testing.T) {
	t.Parallel()

	targets := []string{
		"http://e.com/\x1b[31m",
		"http://e.com/\x7f",
		"http://e.com/\u0085x",
		"http://e.com/\u202ex",
		"http://e.com/\u200bx",
		"http://e.com/\ufe0f",
		"http://e.com/%1b%5d8;;evil%07",
		"x\x1by.md",
	}

	for _, target := range targets {
		for _, base := range []string{"", "/repo"} {
			for _, form := range []string{"[t](<%s>)", `<a href="%s">t</a>`} {
				l := layoutOf(fmt.Sprintf(form, target), 80, base)
				assertSafe(t, l, 80)
			}
		}
	}
}

func TestTextSafety(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		src  string
		want string
	}{
		{"esc and csi dropped in prose", "a\x1b[31mred\x1b[0m b", "a[31mred[0m b\n"},
		{"osc introducer dropped", "a\x1b]8;;http://evil\x07b", "a]8;;http://evilb\n"},
		{"bidi override and zero width dropped", "a\u202eb\u200bc", "abc\n"},
		{"line separator becomes a space", "a\u2028b\u2029c", "a b c\n"},
		{"tab becomes a space", "a\tb", "a b\n"},
		{"c1 controls dropped", "a\u0085b\u009bc", "abc\n"},
		{"invalid utf-8 becomes U+FFFD", "a\xffb\xc3(c", "a\ufffdb\ufffd(c\n"},
		{"code tags hidden characters", "`a\u202eb\u200bc`", "‹Code: a›‹Hidden,b:⟨U+202E⟩›‹Code:b›‹Hidden,b:⟨U+200B⟩›‹Code:c ›\n"},
		{"code tags del and c1", "`\x7f\u009b`", "‹Code: ›‹Hidden,b:⟨U+007F⟩⟨U+009B⟩›‹Code: ›\n"},
		{"code tags astral hidden", "`a\U000e0041b`", "‹Code: a›‹Hidden,b:⟨U+E0041⟩›‹Code:b ›\n"},
		{"code tab is a space", "`a\tb`", "‹Code: a b ›\n"},
		{"code invalid utf-8", "`a\xffb`", "‹Code: a\ufffdb ›\n"},
		{"link text is cleaned", "[a\x1b\u202eb](http://e.com)", "‹Link,u:ab›\n"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			l := layoutOf(tt.src, 80, "")
			if got := l.debug(); got != tt.want {
				t.Errorf("layout of %q:\n got %q\nwant %q", tt.src, got, tt.want)
			}

			assertSafe(t, l, 80)
		})
	}
}

func TestWrapLines(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		src   string
		width int
		want  []string
	}{
		{"breaks at spaces", "aaa bbb ccc", 7, []string{"aaa bbb", "ccc"}},
		{"exact fit", "aaa bbb", 7, []string{"aaa bbb"}},
		{"long word is cut by cells", "abcdefghij", 4, []string{"abcd", "efgh", "ij"}},
		{"long word after text starts a line", "ab abcdefg", 4, []string{"ab", "abcd", "efg"}},
		{"cjk wide cells", "日本語", 5, []string{"日本", "語"}},
		{"cjk exact", "日本", 4, []string{"日本"}},
		{"wide char at width 1 becomes an ellipsis", "日a", 1, []string{"…", "a"}},
		{"emoji is wide", "😀😀😀", 4, []string{"😀😀", "😀"}},
		{"width below 1 lays out at 1", "ab", 0, []string{"a", "b"}},
		{"combining mark stays with its base", "e\u0301e\u0301", 1, []string{"e\u0301", "e\u0301"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			l := layoutOf(tt.src, tt.width, "")
			if got := l.Plain(); fmt.Sprintf("%q", got) != fmt.Sprintf("%q", tt.want) {
				t.Errorf("lines = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestWrapWidths(t *testing.T) {
	t.Parallel()

	const src = "the quick brown fox jumps over 日本語のテキスト and 😀😀 emoji supercalifragilisticexpialidocious `inline code here` end"

	for _, width := range []int{1, 2, 3, 5, 10} {
		t.Run(fmt.Sprint(width), func(t *testing.T) {
			t.Parallel()

			l := layoutOf(src, width, "")
			assertSafe(t, l, width)

			if width < 2 {
				return
			}

			// Nothing is lost: only spaces differ from the source text.
			want := strings.NewReplacer(" ", "", "`", "").Replace(src)
			got := strings.ReplaceAll(strings.Join(l.Plain(), ""), " ", "")

			if got != want {
				t.Errorf("text changed:\n got %q\nwant %q", got, want)
			}
		})
	}
}

func TestWrapLongInput(t *testing.T) {
	t.Parallel()

	l := layoutOf(strings.Repeat("x", 10000)+" "+strings.Repeat("日", 5000), 40, "")
	assertSafe(t, l, 40)

	if len(l.Lines) < 250 {
		t.Errorf("only %d lines for 20000 cells at width 40", len(l.Lines))
	}
}

func TestMaxWidth100(t *testing.T) {
	t.Parallel()

	l := layoutOf(strings.Repeat("word ", 300), 130, "")
	assertSafe(t, l, 100)

	longest := 0
	for _, s := range l.Plain() {
		longest = max(longest, ansi.StringWidth(s))
	}

	if longest < 95 {
		t.Errorf("longest line is %d cells, want the width 100 used", longest)
	}
}

func TestRunsMerge(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		src  string
		runs int
		want string
	}{
		{"split text pieces merge", "a [b c] d", 1, "a [b c] d\n"},
		{"same style neighbours merge", "*a*_b_", 1, "‹Text,i:ab›\n"},
		{"different styles stay apart", "**a** **b**", 3, "‹Text,b:a› ‹Text,b:b›\n"},
		{"link pieces merge", "[a [b] c](http://e.com)", 1, "‹Link,u:a [b] c›\n"},
		{"two links stay apart", "[a](http://e.com)[b](http://e.com)", 2, "‹Link,u:a›‹Link,u:b›\n"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			l := layoutOf(tt.src, 80, "")
			if len(l.Lines) != 1 || len(l.Lines[0]) != tt.runs {
				t.Errorf("runs = %d, want %d: %q", len(l.Lines[0]), tt.runs, l.debug())
			}

			if got := l.debug(); got != tt.want {
				t.Errorf("got %q, want %q", got, tt.want)
			}
		})
	}
}

func TestFootnoteRef(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name, src, want string
	}{
		{"defined", "x[^1] y\n\n[^1]: note", "x‹FootnoteRef:[1]› y\n\n‹FootnoteRule:────────────────›\n‹FootnoteNumber: 1. ›‹FootnoteText:note›\n"},
		{"numbered by first reference", "a[^b] c[^a]\n\n[^a]: one\n\n[^b]: two", "a‹FootnoteRef:[1]› c‹FootnoteRef:[2]›\n\n‹FootnoteRule:────────────────›\n‹FootnoteNumber: 1. ›‹FootnoteText:two›\n‹FootnoteNumber: 2. ›‹FootnoteText:one›\n"},
		{"undefined stays as written", "z [^2]", "z [^2]\n"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := layoutOf(tt.src, 80, "").debug(); got != tt.want {
				t.Errorf("got %q, want %q", got, tt.want)
			}
		})
	}
}

func TestParagraphs(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name, src string
		want      []string
	}{
		{"one blank line between paragraphs", "a\n\nb\n\n\n\nc", []string{"a", "", "b", "", "c"}},
		{"empty paragraph lays out nothing", "<!-- x -->\n\na", []string{"a"}},
		{"empty document", "", nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := layoutOf(tt.src, 80, "").Plain()
			if fmt.Sprintf("%q", got) != fmt.Sprintf("%q", tt.want) {
				t.Errorf("lines = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestLayoutDoesNotMutateDoc(t *testing.T) {
	t.Parallel()

	d := Parse([]byte(strings.Repeat("some *words* and [a link](http://e.com) here\n\n", 20)), ParseOptions{})
	first := d.Layout(LayoutOptions{Width: 30}).debug()
	d.Layout(LayoutOptions{Width: 7})

	if again := d.Layout(LayoutOptions{Width: 30}).debug(); again != first {
		t.Errorf("layout changed after another layout:\n%s\nvs\n%s", first, again)
	}
}

func TestInlineGolden(t *testing.T) {
	t.Parallel()

	src, err := os.ReadFile("testdata/inline.md")
	if err != nil {
		t.Fatal(err)
	}

	d := Parse(src, ParseOptions{Base: "https://example.com/repo/docs/"})

	for _, width := range []int{20, 60} {
		t.Run(fmt.Sprint(width), func(t *testing.T) {
			l := d.Layout(LayoutOptions{Width: width})
			assertSafe(t, l, width)
			goldenText(t, fmt.Sprintf("inline.%d", width), l.debug())
		})
	}
}

func TestDeepInlineNestingKeepsText(t *testing.T) {
	t.Parallel()

	src := strings.Repeat("[", 200) + "deep" + strings.Repeat("](http://e.com)", 200)
	l := layoutOf(src, 40, "")
	assertSafe(t, l, 40)

	src = strings.Repeat("*a ", 300) + "end"
	l = layoutOf(src, 40, "")
	assertSafe(t, l, 40)

	if got := strings.Join(l.Plain(), " "); !strings.Contains(got, "end") {
		t.Errorf("text lost under deep emphasis: %q", got)
	}

	// Past the depth cap, soft breaks stay word breaks and images stay hidden.
	src = strings.Repeat("*", 400) + "one\ntwo ![alt](http://e.com/i.png) three" + strings.Repeat("*", 400)
	deep := strings.Join(layoutOf(src, 80, "").Plain(), " ")

	if !strings.Contains(deep, "one two") || strings.Contains(deep, "alt") {
		t.Errorf("deep nesting laid out %q", deep)
	}
}
