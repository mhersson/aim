package md

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"unicode/utf8"

	"github.com/charmbracelet/x/ansi"

	"github.com/mhersson/aim/internal/git/invisible"
)

// corpusWidths are the widths of the goldens; 130 lays out as 100, which the
// caps-at-100 subtest checks.
var corpusWidths = []int{20, 40, 80, 100}

type corpusDoc struct {
	name string
	src  []byte
}

// corpusDocs returns the committed documents and the hostile one.
func corpusDocs(t testing.TB) []corpusDoc {
	t.Helper()

	paths, err := filepath.Glob(filepath.Join("testdata", "corpus", "*.md"))
	if err != nil || len(paths) == 0 {
		t.Fatalf("no corpus documents: %v", err)
	}

	docs := make([]corpusDoc, 0, len(paths)+1)

	for _, p := range paths {
		src, err := os.ReadFile(p)
		if err != nil {
			t.Fatal(err)
		}

		docs = append(docs, corpusDoc{strings.TrimSuffix(filepath.Base(p), ".md"), src})
	}

	return append(docs, corpusDoc{"hostile", hostileDoc()})
}

// hostileDoc builds the adversarial document in Go so the repository holds no
// raw control or bidi bytes.
func hostileDoc() []byte {
	const (
		esc  = "\x1b"
		csi  = "\x1b[31;1m"
		osc8 = "\x1b]8;;https://evil.example/\x07"
		rlo  = "\u202e"
		zwsp = "\u200b"
		c1   = "\u0085\u009b\u009d"
		del  = "\x7f"
		bad  = "\xff\xfe\xc3"
	)

	nasty := esc + csi + osc8 + rlo + zwsp + c1 + del + bad
	big := strings.Repeat("W", 150)

	var b strings.Builder

	w := func(format string, args ...any) { fmt.Fprintf(&b, format, args...) }

	w("# Hostile %s heading\n\n", nasty)
	w("Prose %s end and `code %s end` and **bold %s** and *it %s*.\n\n", nasty, nasty, nasty, nasty)
	w("```go%s\nfmt.Println(\"%s\")\n\tindented %s\n```\n\n", esc, nasty, nasty)
	w("[link %s](https://example.com/%s) and [safe](https://example.com/ok)\n\n", nasty, esc)
	w("![alt %s](https://example.com/%s.png) and <img src=\"https://example.com/i%s.png\" alt=\"html %s\">\n\n", nasty, esc, esc, nasty)
	w("| head %s | other |\n| --- | --- |\n| cell %s | [x %s](https://example.com/c) |\n| `code %s` | ![img %s](https://example.com/t.png) |\n\n", nasty, nasty, nasty, nasty, nasty)

	w("[js](javascript:alert(1)) [data](data:text/html;base64,AAAA) [file](file:///etc/passwd) [vsc](vscode://file/etc/passwd) [mail](mailto:a@example.com)\n\n")
	w("![js](javascript:alert(1)) ![data](data:image/png;base64,AAAA) ![file](file:///etc/passwd) ![vsc](vscode://x)\n\n")
	w("<a href=\"javascript:alert(1)\">html js</a> <a href=\"data:text/html,x\">html data</a> <https://example.com/auto> <javascript:alert(1)>\n\n")
	w("[ref js][r1] [ref ok][r2]\n\n[r1]: javascript:alert(1)\n[r2]: https://example.com/ref\n\n")

	for i := range 10 {
		w("<details open>\n<summary>level %d</summary>\n\ntext at level %d\n\n", i, i)
	}

	for range 10 {
		w("</details>\n\n")
	}

	w("日本語の文章はとても長くなることがあります。折り返しの確認をします。😀😀😀 family 👨\u200d👩\u200d👧\u200d👦 flag 🇳🇴 and é combining\n\n")
	w("%s\n\n", big)
	w("```\n%s\n```\n\n", strings.Repeat("x", 10000))
	w("```\n%s\n```\n\n", strings.Repeat("日", 3000))

	for d := range 30 {
		w("%s- level %d\n", strings.Repeat("  ", d), d)
	}

	b.WriteString("\n")

	for d := range 30 {
		w("%s quote %d\n", strings.Repeat(">", d+1), d)
	}

	b.WriteString("\n")
	b.WriteString(wideTable(30))
	b.WriteString("\n")
	b.WriteString(wideTable(65))
	b.WriteString("\n- [x] done " + nasty + "\n- [ ] open " + nasty + "\n\ntext with footnote[^n]\n\n[^n]: note " + nasty + "\n\n")
	// Last, so everything above still shows at the default folds.
	w("<details>\n<summary>never closed %s</summary>\n\nbody of the unclosed fold\n", nasty)

	return []byte(b.String())
}

func wideTable(cols int) string {
	row := func(cell func(i int) string) string {
		parts := make([]string, cols)
		for i := range parts {
			parts[i] = cell(i)
		}

		return "| " + strings.Join(parts, " | ") + " |\n"
	}

	return row(func(i int) string { return fmt.Sprintf("h%d", i) }) +
		row(func(int) string { return "---" }) +
		row(func(i int) string { return fmt.Sprintf("cell number %d", i) }) +
		row(func(i int) string { return fmt.Sprintf("`c%d` \u202e\x1b[1m", i) })
}

// everyOtherByte is a highlighter that gives every other byte a class.
func everyOtherByte(_ string, lines []string) [][]Span {
	out := make([][]Span, len(lines))

	for i, l := range lines {
		for j := 0; j < len(l); j += 2 {
			out[i] = append(out[i], Span{Start: j, End: j + 1, Class: uint8(1 + j%5)})
		}
	}

	return out
}

// layoutAllOpen lays d out with every fold open, reopening until no new fold
// appears, because opening one reveals the ones inside it.
func layoutAllOpen(d *Doc, o LayoutOptions) Layout {
	open := map[int]bool{}
	o.Open = open

	for {
		l := d.Layout(o)
		grew := false

		for _, f := range l.Folds {
			if !open[f.ID] {
				open[f.ID] = true
				grew = true
			}
		}

		if !grew {
			return l
		}
	}
}

// assertSource checks the source ranges of l against a source of srcLines
// lines: one per layout line, in bounds, and consecutive lines for one range.
// Lines between may have no source or an overlapping range: an inline
// <details> starts several blocks on one source line.
func assertSource(t testing.TB, l Layout, srcLines int) {
	t.Helper()

	if len(l.Source) != len(l.Lines) {
		t.Fatalf("%d source ranges for %d lines", len(l.Source), len(l.Lines))
	}

	last := map[SourceRange]int{}

	for i, r := range l.Source {
		if r == (SourceRange{}) {
			continue
		}

		if r.First < 1 || r.First > r.Last || r.Last > srcLines {
			t.Errorf("line %d has source range %v outside 1..%d", i, r, srcLines)
		}

		if prev, ok := last[r]; ok {
			for j := prev + 1; j < i; j++ {
				if b := l.Source[j]; b != (SourceRange{}) && (b.First > r.Last || b.Last < r.First) {
					t.Errorf("source range %v is on lines %d and %d but not between them", r, prev, i)

					break
				}
			}
		}

		last[r] = i
	}
}

// assertReferences checks the image and fold lines and the link indexes of l.
func assertReferences(t testing.TB, l Layout, icons bool, limit int) {
	t.Helper()

	mark := "image: "
	if icons {
		mark = "\U000F0976"
	}

	for _, im := range l.Images {
		if im.Line < 0 || im.Line >= len(l.Lines) {
			t.Errorf("image line %d outside %d lines", im.Line, len(l.Lines))

			continue
		}

		found := false

		for _, r := range l.Lines[im.Line] {
			if r.Role == RoleImage && strings.Contains(r.Text, mark) {
				found = true
			}
		}

		if !found && !markLost(l.Lines[im.Line], ansi.StringWidth(mark), limit) {
			t.Errorf("image line %d has no %q in RoleImage: %q", im.Line, mark, plainLine(l.Lines[im.Line]))
		}

		if im.Link < -1 || im.Link >= len(l.Links) {
			t.Errorf("image link index %d of %d", im.Link, len(l.Links))
		}
	}

	for _, f := range l.Folds {
		if f.Line < 0 || f.Line >= len(l.Lines) {
			t.Errorf("fold line %d outside %d lines", f.Line, len(l.Lines))

			continue
		}

		found := false

		for _, r := range l.Lines[f.Line] {
			if r.Role == RoleSummaryMark && (strings.Contains(r.Text, "▸") || strings.Contains(r.Text, "▾")) {
				found = true
			}
		}

		if !found && !markLost(l.Lines[f.Line], 2, limit) {
			t.Errorf("fold line %d has no marker in RoleSummaryMark: %q", f.Line, plainLine(l.Lines[f.Line]))
		}
	}
}

// markLost reports whether a line ends in a cut and its leading structure
// (indent, bars, bullets, task marks) leaves no room for the whole mark before
// the cut, so the mark may be missing or partial.
func markLost(line Line, markWidth, limit int) bool {
	if len(line) == 0 || !strings.HasSuffix(plainLine(line), "…") {
		return false
	}

	prefix := 0

	for i, r := range line {
		text := r.Text
		if i == len(line)-1 {
			text = strings.TrimSuffix(text, "…")
		}

		if !structural(r.Role, text) {
			break
		}

		prefix += ansi.StringWidth(text)
	}

	return prefix+markWidth > limit-1
}

// structural reports whether a run only builds the indentation before a block's content.
func structural(role Role, text string) bool {
	switch role {
	case RoleBullet, RoleTaskDone, RoleTaskOpen, RoleQuoteBar,
		RoleAlertNote, RoleAlertTip, RoleAlertImportant, RoleAlertWarning, RoleAlertCaution:
		return true
	case RoleText, RoleQuoteText:
		return strings.Trim(text, " ") == ""
	}

	return false
}

func TestMarkLost(t *testing.T) {
	t.Parallel()

	spaces := func(n int) Run { return Run{Text: strings.Repeat(" ", n), Link: -1} }

	tests := []struct {
		name      string
		line      Line
		markWidth int
		limit     int
		want      bool
	}{
		{"partial image mark under a nested bullet", Line{spaces(4), {Text: "\u25e6 ", Role: RoleBullet}, {Text: "ima\u2026", Role: RoleImage}}, 7, 10, true},
		{"indent fills the line", Line{spaces(8), {Text: "\u2026", Role: RoleText}}, 2, 10, true},
		{"cut at width 80 with room for the mark", Line{spaces(2), {Text: "\u2022 ", Role: RoleBullet}, {Text: "\u2026", Role: RoleImage}}, 7, 80, false},
		{"no cut", Line{spaces(4), {Text: "x", Role: RoleText}}, 2, 3, false},
		{"mark exactly fits before the cut", Line{spaces(1), {Text: "\u2026", Role: RoleText}}, 8, 10, false},
		{"mark one cell too wide", Line{spaces(1), {Text: "\u2026", Role: RoleText}}, 9, 10, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := markLost(tt.line, tt.markWidth, tt.limit); got != tt.want {
				t.Errorf("markLost = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestCorpusGoldens(t *testing.T) {
	t.Parallel()

	if *update {
		if err := os.MkdirAll(filepath.Join("testdata", "corpus"), 0o755); err != nil {
			t.Fatal(err)
		}
	}

	for _, doc := range corpusDocs(t) {
		d := Parse(doc.src, ParseOptions{Base: "https://github.com/example/repo/blob/main/"})

		for _, w := range corpusWidths {
			t.Run(fmt.Sprintf("%s.%d", doc.name, w), func(t *testing.T) {
				t.Parallel()

				l := d.Layout(LayoutOptions{Width: w, Icons: true})
				goldenText(t, fmt.Sprintf("corpus/%s.%d", doc.name, w), l.debug())
			})
		}

		t.Run(doc.name+" caps at 100", func(t *testing.T) {
			t.Parallel()

			at100 := d.Layout(LayoutOptions{Width: 100, Icons: true}).debug()
			at130 := d.Layout(LayoutOptions{Width: 130, Icons: true}).debug()

			if at100 != at130 {
				t.Error("width 130 does not lay out as width 100")
			}
		})
	}
}

func TestCorpusWidths(t *testing.T) {
	t.Parallel()

	widths := []int{1, 2, 3, 5, 10, 20, 80, 130}

	for _, doc := range corpusDocs(t) {
		d := Parse(doc.src, ParseOptions{Base: "https://github.com/example/repo/blob/main/"})

		for _, w := range widths {
			for _, icons := range []bool{true, false} {
				for _, hl := range []Highlighter{nil, everyOtherByte} {
					o := LayoutOptions{Width: w, Icons: icons, Highlight: hl}
					limit := min(w, maxWidth)

					for _, name := range []string{"default", "open"} {
						label := fmt.Sprintf("%s w=%d icons=%v hl=%v %s", doc.name, w, icons, hl != nil, name)

						t.Run(label, func(t *testing.T) {
							t.Parallel()

							l := d.Layout(o)
							if name == "open" {
								l = layoutAllOpen(d, o)
							}

							assertSafe(t, l, limit)
							assertReferences(t, l, icons, limit)
							assertSource(t, l, len(d.starts))
						})
					}
				}
			}
		}
	}
}

func allowedScheme(u string) bool {
	return strings.HasPrefix(u, "http://") || strings.HasPrefix(u, "https://") || strings.HasPrefix(u, "mailto:")
}

// assertImageSrcs checks that every image source is empty, http(s) or an absolute path, and clean.
func assertImageSrcs(t *testing.T, l Layout) {
	t.Helper()

	for _, im := range l.Images {
		assertCleanTarget(t, im.Src)

		abs := filepath.IsAbs(im.Src) && !strings.HasPrefix(im.Src, "//")
		if im.Src != "" && !strings.HasPrefix(im.Src, "http://") && !strings.HasPrefix(im.Src, "https://") && !abs {
			t.Errorf("image source %q is not empty, http(s) or an absolute path", im.Src)
		}
	}
}

func TestCorpusLinks(t *testing.T) {
	t.Parallel()

	counts := map[string]int{}

	for _, doc := range corpusDocs(t) {
		d := Parse(doc.src, ParseOptions{Base: "https://github.com/example/repo/blob/main/"})

		for _, l := range []Layout{d.Layout(LayoutOptions{Width: 80, Icons: true}), layoutAllOpen(d, LayoutOptions{Width: 80})} {
			assertImageSrcs(t, l)

			for _, k := range l.Links {
				counts[doc.name]++

				if !allowedScheme(k.URL) {
					t.Errorf("%s: link %q is outside the allowed schemes", doc.name, k.URL)
				}

				if !utf8.ValidString(k.URL) {
					t.Errorf("%s: link %q is not UTF-8", doc.name, k.URL)
				}

				for _, c := range k.URL {
					if c < 0x20 || c == 0x7f || (c >= 0x80 && c <= 0x9f) || invisible.Rune(c) {
						t.Errorf("%s: link %q holds U+%04X", doc.name, k.URL, c)
					}
				}
			}
		}
	}

	for _, name := range []string{"readme", "pr-body"} {
		if counts[name] == 0 {
			t.Errorf("%s has no links, so the scheme check proves nothing", name)
		}
	}
}

// TestConcurrentLayout lays one Doc out from several goroutines at several
// widths, with folds open and a highlighter, and checks each result against a
// sequential layout. The race detector catches a layout that writes to the Doc.
func TestConcurrentLayout(t *testing.T) {
	t.Parallel()

	widths := []int{1, 20, 80, 130}
	opts := func(w int) LayoutOptions {
		return LayoutOptions{Width: w, Icons: true, Highlight: everyOtherByte}
	}

	for _, doc := range corpusDocs(t) {
		t.Run(doc.name, func(t *testing.T) {
			t.Parallel()

			d := Parse(doc.src, ParseOptions{Base: "https://example.test/docs/"})

			want := make([]string, len(widths))
			for i, w := range widths {
				want[i] = strings.Join(layoutAllOpen(d, opts(w)).Plain(), "\n")
			}

			const perWidth = 2

			got := make([][perWidth]string, len(widths))

			var wg sync.WaitGroup

			for i, w := range widths {
				for g := range perWidth {
					wg.Go(func() {
						got[i][g] = strings.Join(layoutAllOpen(d, opts(w)).Plain(), "\n")
					})
				}
			}

			wg.Wait()

			for i, w := range widths {
				for g := range perWidth {
					if got[i][g] != want[i] {
						t.Errorf("width %d, goroutine %d: the layout differs from the sequential one", w, g)
					}
				}
			}
		})
	}
}
