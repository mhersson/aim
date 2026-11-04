package ui

import (
	"fmt"
	"os"
	"strings"
	"testing"
	"unicode"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/charmbracelet/x/exp/golden"

	"github.com/mhersson/aim/internal/md"
	"github.com/mhersson/aim/internal/ui/diffview"
	"github.com/mhersson/aim/internal/ui/theme"
)

func mochaTheme(t *testing.T) theme.Theme {
	t.Helper()

	th, err := theme.Builtin().ByName("catppuccin-mocha")
	if err != nil {
		t.Fatal(err)
	}

	return th
}

// fakeHighlight classes code by word, so goldens need no lexer.
func fakeHighlight(_ string, lines []string) [][]md.Span {
	out := make([][]md.Span, len(lines))

	for i, ln := range lines {
		start := -1

		flush := func(end int) {
			if start < 0 {
				return
			}

			var class diffview.Class

			switch w := ln[start:end]; {
			case w == "func" || w == "return":
				class = diffview.Keyword
			case w == "main":
				class = diffview.Func
			case w[0] >= '0' && w[0] <= '9':
				class = diffview.Number
			default:
				start = -1

				return
			}

			out[i] = append(out[i], md.Span{Start: start, End: end, Class: uint8(class)})
			start = -1
		}

		for j := 0; j < len(ln); j++ {
			if c := ln[j]; unicode.IsLetter(rune(c)) || unicode.IsDigit(rune(c)) {
				if start < 0 {
					start = j
				}

				continue
			}

			flush(j)
		}

		flush(len(ln))
	}

	return out
}

func rolesLayout(t *testing.T, width int, icons bool) md.Layout {
	t.Helper()

	src, err := os.ReadFile("testdata/md-roles.md")
	if err != nil {
		t.Fatal(err)
	}

	return md.Parse(src, md.ParseOptions{}).Layout(md.LayoutOptions{
		Width: width, Icons: icons, Highlight: fakeHighlight,
		Open: map[int]bool{},
	})
}

func paintLayout(p mdPainter, l md.Layout, w int) []string {
	out := make([]string, len(l.Lines))
	for i, ln := range l.Lines {
		out[i] = p.line(ln, l.Links, w)
	}

	return out
}

func TestMDPainterGolden(t *testing.T) {
	t.Parallel()

	th := mochaTheme(t)
	l := rolesLayout(t, 60, true)
	p := newMDPainter(th, th.UI.Bg)

	golden.RequireEqual(t, []byte(strings.Join(paintLayout(p, l, 60), "\n")))
}

// TestMDPainterText is the same screen without styling, so the layout reads.
func TestMDPainterText(t *testing.T) {
	t.Parallel()

	th := mochaTheme(t)
	p := newMDPainter(th, th.UI.Bg)

	t.Run("icons=false", func(t *testing.T) {
		t.Parallel()

		l := rolesLayout(t, 60, false)
		golden.RequireEqual(t, []byte(ansi.Strip(strings.Join(paintLayout(p, l, 60), "\n"))))
	})
}

func TestMDPainterRoles(t *testing.T) {
	t.Parallel()

	th := mochaTheme(t)
	ui := th.UI
	ground := ui.Bg
	st := func(fg, bg theme.Hex) lipgloss.Style { return lipgloss.NewStyle().Foreground(fg).Background(bg) }

	cases := []struct {
		name string
		run  md.Run
		want lipgloss.Style
	}{
		{"code text unknown class", md.Run{Text: "x", Role: md.RoleCodeText, Class: 250, Link: -1}, st(ui.Fg, ground)},
		{"all attributes", md.Run{Text: "t", Bold: true, Italic: true, Strike: true, Link: -1}, st(ui.Fg, ground).Bold(true).Italic(true).Strikethrough(true)},
		{"underline attribute", md.Run{Text: "t", Underline: true, Link: -1}, st(ui.Fg, ground).Underline(true)},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()

			got := newMDPainter(th, ground).line(md.Line{c.run}, nil, ansi.StringWidth(c.run.Text))
			if want := c.want.Render(c.run.Text); got != want {
				t.Errorf("painted %q, want %q", got, want)
			}
		})
	}
}

func TestMDPainterLinks(t *testing.T) {
	t.Parallel()

	th := mochaTheme(t)
	p := newMDPainter(th, th.UI.Bg)
	plain := lipgloss.NewStyle().Foreground(th.UI.Info).Background(th.UI.Bg).Underline(true)

	for _, url := range []string{"https://example.com/a?b=c", "mailto:me@example.com"} {
		got := p.line(md.Line{{Text: "go", Role: md.RoleLink, Link: 0}}, []md.Link{{URL: url}}, 2)
		want := ansi.SetHyperlink(url) + plain.Render("go") + ansi.ResetHyperlink()

		if got != want {
			t.Errorf("%s: painted %q, want %q", url, got, want)
		}

		if !endsClean(got) {
			t.Errorf("%s: %q does not end clean", url, got)
		}
	}

	t.Run("path gets no link", func(t *testing.T) {
		t.Parallel()

		got := p.line(md.Line{{Text: "go", Role: md.RoleLink, Link: -1, Path: "/x/y.md"}}, nil, 2)
		if strings.Contains(got, "\x1b]8") || got != plain.Render("go") {
			t.Errorf("painted %q", got)
		}
	})

	t.Run("closed when the line is cut", func(t *testing.T) {
		t.Parallel()

		got := p.line(md.Line{{Text: "gopher", Role: md.RoleLink, Link: 0}, {Text: "tail"}}, []md.Link{{URL: "https://e.x"}}, 3)
		if w := ansi.StringWidth(got); w != 3 || !endsClean(got) {
			t.Errorf("painted %q: width %d, clean %v", got, w, endsClean(got))
		}
	})

	t.Run("link index out of range", func(t *testing.T) {
		t.Parallel()

		got := p.line(md.Line{{Text: "go", Role: md.RoleLink, Link: 3}}, nil, 2)
		if strings.Contains(got, "\x1b]8") {
			t.Errorf("painted %q", got)
		}
	})
}

func TestMDPainterHostileLinks(t *testing.T) {
	t.Parallel()

	th := mochaTheme(t)
	p := newMDPainter(th, th.UI.Bg)

	for name, url := range map[string]string{
		"osc8":         "https://e.x/\x1b]8;;https://evil.x\x1b\\",
		"del":          "https://e.x/\x7f",
		"c1 csi":       "https://e.x/\u009b31m",
		"bidi":         "https://e.x/\u202e",
		"bad utf8":     "https://e.x/\xff",
		"other scheme": "javascript:alert(1)",
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			got := p.line(md.Line{{Text: "link", Role: md.RoleLink, Link: 0}}, []md.Link{{URL: url}}, 10)

			if strings.Contains(got, "\x1b]") || strings.Contains(got, "\a") || strings.Contains(got, "\u009b") {
				t.Errorf("painted %q holds an OSC or control", got)
			}

			if rest := strings.ReplaceAll(ansi.Strip(got), "link", ""); strings.TrimSpace(rest) != "" || ansi.StringWidth(got) != 10 {
				t.Errorf("painted %q: stripped %q", got, ansi.Strip(got))
			}

			if strings.Count(got, "\x1b") != strings.Count(got, "\x1b[") {
				t.Errorf("painted %q holds an escape that is not SGR", got)
			}

			if u := strings.TrimPrefix(url, "https://e.x/"); u != "" && u != url && strings.Contains(got, u) {
				t.Errorf("painted %q holds the url's bytes %q", got, u)
			}
		})
	}
}

func TestLinkSafe(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		url  string
		want bool
	}{
		{"https with space query and fragment", "https://example.com/a b?c=d#e", true},
		{"scheme in capitals", "HTTP://example.com", true},
		{"non-ascii path", "https://e.x/é中", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if got := linkSafe(tt.url); got != tt.want {
				t.Errorf("linkSafe(%q) = %v, want %v", tt.url, got, tt.want)
			}
		})
	}
}

func TestMDPainterWidth(t *testing.T) {
	t.Parallel()

	th := mochaTheme(t)
	l := rolesLayout(t, 60, true)

	for _, w := range []int{1, 7, 80} {
		t.Run(fmt.Sprintf("w=%d", w), func(t *testing.T) {
			t.Parallel()

			p := newMDPainter(th, th.UI.Bg)

			for i, s := range paintLayout(p, l, w) {
				if got := ansi.StringWidth(s); got != w {
					t.Fatalf("line %d is %d cells, want %d: %q", i, got, w, s)
				}

				if !endsClean(s) {
					t.Fatalf("line %d does not end clean: %q", i, s)
				}
			}
		})
	}

	t.Run("padding is on the ground", func(t *testing.T) {
		t.Parallel()

		p := newMDPainter(th, th.UI.Panel)
		got := p.line(md.Line{{Text: "ab", Link: -1}}, nil, 6)

		want := lipgloss.NewStyle().Foreground(th.UI.Fg).Background(th.UI.Panel).Render("ab") +
			lipgloss.NewStyle().Background(th.UI.Panel).Render("    ")
		if got != want {
			t.Errorf("painted %q, want %q", got, want)
		}

		if g := newMDPainter(th, th.UI.Bg).withGround(th.UI.Panel).line(md.Line{{Text: "ab", Link: -1}}, nil, 6); g != want {
			t.Errorf("withGround painted %q, want %q", g, want)
		}
	})

	t.Run("a link with a non-ASCII URL", func(t *testing.T) {
		t.Parallel()

		p := newMDPainter(th, th.UI.Bg)
		line := md.Line{{Text: "caf\u00e9 ", Link: -1}, {Text: "see the caf\u00e9 page", Role: md.RoleLink, Link: 0}, {Text: " tail"}}
		links := []md.Link{{URL: "https://example.com/caf\u00e9"}}

		// The line is 27 cells: the link spans 5 to 22, so 4 stops before it, 6 and 17
		// inside it, 22 at its end and 40 pads past the line.
		for _, w := range []int{1, 4, 6, 17, 22, 27, 40} {
			got := p.line(line, links, w)

			if sw := ansi.StringWidth(got); sw != w {
				t.Errorf("width %d: painted %d cells: %q", w, sw, got)
			}

			if !endsClean(got) {
				t.Errorf("width %d does not end clean: %q", w, got)
			}
		}
	})

	t.Run("cut at a wide character", func(t *testing.T) {
		t.Parallel()

		tests := []struct {
			width int
			want  string
		}{
			{4, "ab\u2026 "},
			{5, "ab\u4e2d\u2026"},
		}

		p := newMDPainter(th, th.UI.Bg)

		for _, tt := range tests {
			got := p.line(md.Line{{Text: "ab\u4e2d\u6587", Link: -1}}, nil, tt.width)

			if w := ansi.StringWidth(got); w != tt.width {
				t.Errorf("width %d: painted %d cells: %q", tt.width, w, got)
			}

			if plain := ansi.Strip(got); plain != tt.want {
				t.Errorf("width %d: text %q, want %q", tt.width, plain, tt.want)
			}

			if !endsClean(got) {
				t.Errorf("width %d does not end clean: %q", tt.width, got)
			}
		}
	})

	t.Run("empty line", func(t *testing.T) {
		t.Parallel()

		p := newMDPainter(th, th.UI.Bg)
		if got := p.line(nil, nil, 5); ansi.StringWidth(got) != 5 {
			t.Errorf("painted %q", got)
		}

		if got := p.line(nil, nil, 0); got != "" {
			t.Errorf("zero width painted %q", got)
		}
	})
}

func TestMDPainterPictureRow(t *testing.T) {
	t.Parallel()

	th := mochaTheme(t)
	p := newMDPainter(th, th.UI.Bg)
	row := md.Line{{Text: "│", Role: md.RoleQuoteBar, Link: -1}, {Text: "   ", Role: md.RolePicture, Link: 0}}
	links := []md.Link{{URL: "https://example.com/x"}}

	want := p.line(row, nil, 5)
	got := p.withQuery(" ").line(row, links, 5)

	if got != want || strings.Contains(got, "\x1b]8") {
		t.Errorf("picture row painted %q, want %q: blank cells, no link, no match", got, want)
	}
}
