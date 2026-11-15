package ui

import (
	"strings"
	"testing"

	"github.com/mhersson/aim/internal/core"
	"github.com/mhersson/aim/internal/harness"
)

// claudeSel is the selection in mode from column 0 of row from to the last column
// of row to.
func claudeSel(mode selMode, from, to, cols int) selection {
	return selection{mode, point{from, 0}, point{to, cols - 1}}
}

// replyLine is the line of claudeWantReply that starts with prefix.
func replyLine(t *testing.T, prefix string) string {
	t.Helper()

	for line := range strings.SplitSeq(claudeWantReply, "\n") {
		if strings.HasPrefix(line, prefix) {
			return line
		}
	}

	t.Fatalf("no line of the reply starts with %q", prefix)

	return ""
}

type claudeExtractCase struct {
	name string
	// pane or fake is the grid the case reads.
	pane   *pane
	fake   *fakeGrid
	sel    selection
	claude bool
	// want is the copy, unless asExtract says it is extract's.
	want      string
	asExtract bool
}

func TestClaudeExtract(t *testing.T) {
	t.Parallel()

	d80, d117 := claudeDefault80, claudeFullscreen117
	h := d80.hist()
	p80, _ := claudeGridPane(t, d80)
	p117, _ := claudeGridPane(t, d117)

	// Rows 0-48 all move into the history, so the scanner skips the blank ones.
	pHist, fpHist := claudeGridPane(t, d80)
	feedAndWait(t, pHist, fpHist, "\x1b[40;1H"+strings.Repeat("\n", 32))

	pHist.withGrid(func(g grid) {
		ts, ok := g.(textScanner)
		if !ok || ts.historyRows() < h+32 {
			t.Fatalf("rows 0-%d are not all in the history", h+31)
		}
	})

	stream := func(from, to, cols int) selection { return claudeSel(selStream, from, to, cols) }
	alpha := "alpha beta gamma del"

	cases := []claudeExtractCase{
		{name: "80 prompt", pane: p80, sel: stream(5, 6, 80), claude: true, want: claudeWantPrompt},
		{name: "80 reply across history and screen", pane: p80, sel: stream(16, h+31, 80), claude: true, want: claudeWantReply},
		{name: "117 prompt", pane: p117, sel: stream(6, 7, 117), claude: true, want: claudeWantPrompt},
		{name: "117 reply", pane: p117, sel: stream(11, 37, 117), claude: true, want: claudeWantReply},

		{
			name: "short row then next paragraph row keeps newline",
			pane: p80, sel: stream(h+5, h+6, 80), claude: true, asExtract: true,
		},
		{
			name: "break inside a path joins with nothing",
			pane: p80, sel: stream(h+11, h+12, 80), claude: true,
			want: replyLine(t, "  - A bullet with a long path:"),
		},
		{
			name: "break inside a URL joins with nothing",
			pane: p80, sel: stream(h+23, h+24, 80), claude: true,
			want: replyLine(t, "  See https://"),
		},
		{
			name: "break inside a quote drops the gutter",
			pane: p80, sel: stream(h+20, h+21, 80), claude: true,
			want: replyLine(t, "  ▎ A quoted paragraph"),
		},
		{
			name: "break after a code line joins with one space",
			pane: p80, sel: stream(h+27, h+28, 80), claude: true,
			want: replyLine(t, "          return"),
		},
		{
			name: "closing brace keeps its newline",
			pane: p80, sel: stream(h+28, h+29, 80), claude: true, asExtract: true,
		},

		{name: "80 tool rows as today", pane: p80, sel: stream(11, 13, 80), claude: true, asExtract: true},
		{name: "117 code line keeps newline before brace", pane: p117, sel: stream(34, 35, 117), claude: true, asExtract: true},

		{
			name: "word lookups read whole rows",
			pane: p80, sel: selection{selStream, point{h + 23, 78}, point{h + 24, 37}}, claude: true,
			want: "?highlight=extract&view=plain&line=243",
		},
		{
			name: "last row inside indent adds nothing",
			pane: p80, sel: selection{selStream, point{h + 8, 0}, point{h + 9, 1}}, claude: true,
			want: d80.lines[h+8],
		},
		{
			name: "first row keeps its indent",
			pane: p80, sel: stream(h+0, h+1, 80), claude: true,
			want: "  terminal itself wrapped a line at the right edge, so text that an application wraps on its own reaches the clipboard with a newline at every visual row,",
		},

		{
			name: "soft wrap keeps the next row's bullet",
			fake: newFakeGrid(20, []int{0}, "abcdefghijklmnopqrst", "- uvw"),
			sel:  stream(0, 1, 20), claude: true, want: "abcdefghijklmnopqrst- uvw",
		},
		{
			name: "soft wrap keeps trailing blanks",
			fake: newFakeGrid(20, []int{0}, "abc"+strings.Repeat(" ", 17), "  def"),
			sel:  stream(0, 1, 20), claude: true, want: "abc" + strings.Repeat(" ", 17) + "  def",
		},

		{
			name: "blank row keeps both newlines",
			fake: newFakeGrid(20, nil, alpha, "", "abcdefghijklmnopqrst"),
			sel:  stream(0, 2, 20), claude: true, want: alpha + "\n\nabcdefghijklmnopqrst",
		},
		{
			name: "wide characters measured in cells",
			fake: newFakeGrid(10, nil, "漢字漢字漢", "字 b"),
			sel:  stream(0, 1, 10), claude: true, want: "漢字漢字漢字 b",
		},
		{
			name: "dash without a blank is no marker",
			fake: newFakeGrid(20, nil, alpha, "-uvw"),
			sel:  stream(0, 1, 20), claude: true, want: alpha + " -uvw",
		},
		{
			name: "blank history rows skipped by the scanner read as blank",
			pane: pHist, sel: stream(16, h+31, 80), claude: true, want: claudeWantReply,
		},
	}

	for _, row1 := range []string{"- uvw", "* uvw", "• uvw", "1. uvw", "2) uvw", "⎿ uvw", "╭───"} {
		cases = append(cases, claudeExtractCase{
			name: "marker " + row1 + " keeps newline",
			fake: newFakeGrid(20, nil, alpha, row1),
			sel:  stream(0, 1, 20), claude: true, want: alpha + "\n" + row1,
		})
	}

	selections := []struct {
		name         string
		pane         *pane
		from, to, wd int
	}{
		{"80 prompt", p80, 5, 6, 80},
		{"80 reply", p80, 16, h + 31, 80},
		{"117 prompt", p117, 6, 7, 117},
		{"117 reply", p117, 11, 37, 117},
	}

	for _, s := range selections {
		for _, m := range []struct {
			name string
			mode selMode
		}{{"stream", selStream}, {"line", selLine}, {"block", selBlock}} {
			cases = append(cases, claudeExtractCase{
				name: "flag false " + m.name + " " + s.name,
				pane: s.pane, sel: claudeSel(m.mode, s.from, s.to, s.wd), asExtract: true,
			})
		}

		cases = append(cases, claudeExtractCase{
			name: "flag true block " + s.name,
			pane: s.pane, sel: claudeSel(selBlock, s.from, s.to, s.wd), claude: true, asExtract: true,
		})
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			read := false
			check := func(g grid) {
				read = true

				want := tc.want
				if tc.asExtract {
					want = extract(g, tc.sel)
				}

				if tc.asExtract && want == "" {
					t.Fatal("extract returned an empty copy")
				}

				if got := extractFor(g, tc.sel, tc.claude); got != want {
					t.Errorf("extractFor = %q, want %q", got, want)
				}
			}

			if tc.pane != nil {
				tc.pane.withGrid(check)
			} else {
				check(tc.fake)
			}

			if !read {
				t.Fatal("the grid was not read")
			}
		})
	}
}

func TestClaudeLine(t *testing.T) {
	t.Parallel()

	d := claudeDefault80
	h := d.hist()
	p, _ := claudeGridPane(t, d)

	tests := []struct {
		name         string
		row          int
		first, start int
	}{
		{"paragraph row walks up across the history break", h + 2, 16, 2},
		{"bullet continuation", h + 9, h + 8, 4},
		{"path continuation", h + 12, h + 11, 4},
		{"URL continuation", h + 24, h + 23, 2},
		{"code continuation", h + 28, h + 27, 10},
		{"closing brace starts its own line", h + 29, h + 29, 2},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			read := false

			p.withGrid(func(g grid) {
				read = true

				first, start := claudeLine(g, tc.row)
				if first != tc.first || start != tc.start {
					t.Errorf("claudeLine(%d) = (%d, %d), want (%d, %d)", tc.row, first, start, tc.first, tc.start)
				}
			})

			if !read {
				t.Fatal("the grid was not read")
			}
		})
	}
}

func TestIsClaudePane(t *testing.T) {
	t.Parallel()

	claudeID := (harness.Claude{}).ID()

	tests := []struct {
		name string
		pane core.Pane
		want bool
	}{
		{"claude code agent", core.Pane{Kind: core.KindAgent, Harness: claudeID}, true},
		{"agent without a harness", core.Pane{Kind: core.KindAgent}, false},
		{"agent of another harness", core.Pane{Kind: core.KindAgent, Harness: "codex"}, false},
		{"shell", core.Pane{Kind: core.KindShell}, false},
		{"shell naming claude code", core.Pane{Kind: core.KindShell, Harness: claudeID}, false},
		{"zero pane", core.Pane{}, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if got := isClaudePane(tt.pane); got != tt.want {
				t.Errorf("isClaudePane(%+v) = %v, want %v", tt.pane, got, tt.want)
			}
		})
	}
}

// claudeURL is the URL on the fixture's "See" line.
const claudeURL = "https://github.com/example/repository/blob/main/internal/ui/selection.go?highlight=extract&view=plain&line=243"

// claudeClickCase is one double- or triple-click on a grid.
type claudeClickCase struct {
	name string
	// pane or fake is the grid the case reads.
	pane   *pane
	fake   *fakeGrid
	at     point
	claude bool
	want   selection
	// text is what extractFor copies for want, unless it is empty.
	text string
	// today means wordAt or lineAt, and the copy of its selection, give the same.
	today bool
	// notWord lists columns of at's row that cellClass does not call classWord.
	notWord []int
}

// runClaudeClicks runs every case against forClaude, with today as the form that
// ignores Claude Code.
func runClaudeClicks(
	t *testing.T, cases []claudeClickCase,
	forClaude func(grid, point, bool) selection, today func(grid, point) selection,
) {
	t.Helper()

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			read := false
			check := func(g grid) {
				read = true

				got := forClaude(g, tc.at, tc.claude)
				if got != tc.want {
					t.Errorf("selection = %+v, want %+v", got, tc.want)
				}

				if tc.text != "" {
					if text := extractFor(g, got, tc.claude); text != tc.text {
						t.Errorf("extractFor = %q, want %q", text, tc.text)
					}
				}

				if tc.today {
					old := today(g, tc.at)
					if got != old {
						t.Errorf("selection = %+v, today's is %+v", got, old)
					}

					if text, want := extractFor(g, got, tc.claude), extract(g, old); text != want {
						t.Errorf("extractFor = %q, extract of today's selection is %q", text, want)
					}
				}

				for _, col := range tc.notWord {
					if cellClass(g, tc.at.row, col) == classWord {
						t.Errorf("cellClass(%d, %d) is classWord", tc.at.row, col)
					}
				}
			}

			if tc.pane != nil {
				tc.pane.withGrid(check)
			} else {
				check(tc.fake)
			}

			if !read {
				t.Fatal("the grid was not read")
			}
		})
	}
}

func TestClaudeWordAt(t *testing.T) {
	t.Parallel()

	p80, _ := claudeGridPane(t, claudeDefault80)
	p117, _ := claudeGridPane(t, claudeFullscreen117)

	stream := func(r1, c1, r2, c2 int) selection {
		return selection{selStream, point{r1, c1}, point{r2, c2}}
	}
	tight := newFakeGrid(10, nil, "aaaaaaaaaa", "bbbbbbbbbb", "cc")
	marks := newFakeGrid(20, nil, "a?b=c&d%e#f+g")
	wide := newFakeGrid(10, nil, "漢字漢字漢", "字 b")
	soft := newFakeGrid(20, []int{0}, "abcdefghijklmnopqrst", "- uvw")

	cases := []claudeClickCase{
		{name: "broken URL", pane: p80, at: point{41, 4}, claude: true, want: stream(40, 6, 41, 37), text: claudeURL},
		{name: "broken path from Test", pane: p80, at: point{28, 77}, claude: true, want: stream(28, 31, 29, 66), text: claudePath},
		{name: "broken path from the lower row", pane: p80, at: point{29, 10}, claude: true, want: stream(28, 31, 29, 66), text: claudePath},

		{name: "space join stops the word", pane: p80, at: point{25, 79}, claude: true, want: stream(25, 79, 25, 79)},
		{name: "word on the lower row of a space join", pane: p80, at: point{26, 5}, claude: true, want: stream(26, 4, 26, 8)},
		{
			name: "two tight joins in a row from the middle", fake: tight, at: point{1, 5}, claude: true,
			want: stream(0, 0, 2, 1), text: "aaaaaaaaaabbbbbbbbbbcc",
		},
		{
			name: "two tight joins in a row from the last row", fake: tight, at: point{2, 0}, claude: true,
			want: stream(0, 0, 2, 1), text: "aaaaaaaaaabbbbbbbbbbcc",
		},

		{name: "word crosses ? = and & from the first cell", fake: marks, at: point{0, 0}, claude: true, want: stream(0, 0, 0, 6)},
		{name: "word crosses ? = and & from the question mark", fake: marks, at: point{0, 1}, claude: true, want: stream(0, 0, 0, 6)},
		{name: "hash and plus still end a word", fake: marks, at: point{0, 10}, claude: true, want: stream(0, 10, 0, 10)},
		{name: "percent is no word character", fake: marks, at: point{0, 7}, claude: true, want: stream(0, 7, 0, 7)},

		{name: "117 URL on one row", pane: p117, at: point{30, 82}, claude: true, want: stream(30, 6, 30, 115), text: claudeURL},
		{name: "117 path on one row", pane: p117, at: point{22, 95}, claude: true, want: stream(22, 4, 22, 115), text: claudePath},

		{
			name: "wide characters measured in cells from the upper row", fake: wide, at: point{0, 4}, claude: true,
			want: stream(0, 0, 1, 1), text: "漢字漢字漢字",
		},
		{
			name: "wide characters measured in cells from the lower row", fake: wide, at: point{1, 0}, claude: true,
			want: stream(0, 0, 1, 1), text: "漢字漢字漢字",
		},

		{name: "soft wrap right", fake: soft, at: point{0, 19}, claude: true, want: stream(0, 0, 1, 0), today: true},
		{name: "soft wrap left", fake: soft, at: point{1, 0}, claude: true, want: stream(0, 0, 1, 0), today: true},

		{name: "blank after a word", pane: p80, at: point{41, 38}, claude: true, want: stream(41, 38, 41, 38)},
		{name: "blank row", pane: p80, at: point{20, 5}, claude: true, want: stream(20, 5, 20, 5)},
		{
			name: "single full-width row", fake: newFakeGrid(20, nil, "abcdefghijklmnopqrst"),
			at: point{0, 5}, claude: true, want: stream(0, 0, 0, 19),
		},
		{name: "no rows", fake: newFakeGrid(20, nil), at: point{0, 0}, claude: true, want: stream(0, 0, 0, 0), today: true},

		{name: "flag false URL part", pane: p80, at: point{41, 4}, want: stream(41, 2, 41, 9), today: true},
		{name: "flag false path from Test", pane: p80, at: point{28, 77}, want: stream(28, 31, 28, 79), today: true},
		{name: "flag false path from the lower row", pane: p80, at: point{29, 10}, want: stream(29, 4, 29, 43), today: true},
		{
			name: "flag false keeps ? = and & out of the word", fake: marks, at: point{0, 0},
			want: stream(0, 0, 0, 0), today: true, notWord: []int{1, 3, 5},
		},
	}

	runClaudeClicks(t, cases, wordAtFor, wordAt)
}

func TestClaudeLineAt(t *testing.T) {
	t.Parallel()

	p80, _ := claudeGridPane(t, claudeDefault80)

	soft := newFakeGrid(20, []int{0}, "abcdefghijklmnopqrst", "- uvw")
	bullet := replyLine(t, "  - The first bullet")
	first := replyLine(t, "● Here is")

	cases := []claudeClickCase{
		{
			name: "wrapped bullet from the lower row", pane: p80, at: point{26, 10}, claude: true,
			want: claudeSel(selLine, 25, 26, 80), text: bullet,
		},
		{
			name: "wrapped bullet from the upper row", pane: p80, at: point{25, 10}, claude: true,
			want: claudeSel(selLine, 25, 26, 80), text: bullet,
		},

		{
			name: "up across history", pane: p80, at: point{19, 5}, claude: true,
			want: claudeSel(selLine, 16, 19, 80), text: first,
		},
		{
			name: "down across history", pane: p80, at: point{16, 10}, claude: true,
			want: claudeSel(selLine, 16, 19, 80), text: first,
		},
		{
			name: "stops before brace", pane: p80, at: point{45, 10}, claude: true,
			want: claudeSel(selLine, 44, 45, 80), text: replyLine(t, "          return"),
		},

		{name: "soft wrap up", fake: soft, at: point{1, 2}, claude: true, want: claudeSel(selLine, 0, 1, 20), today: true},
		{name: "soft wrap down", fake: soft, at: point{0, 5}, claude: true, want: claudeSel(selLine, 0, 1, 20), today: true},

		{name: "blank row", pane: p80, at: point{20, 5}, claude: true, want: claudeSel(selLine, 20, 20, 80)},
		{
			name: "single full-width row", fake: newFakeGrid(20, nil, "abcdefghijklmnopqrst"),
			at: point{0, 5}, claude: true, want: claudeSel(selLine, 0, 0, 20),
		},
		{name: "no rows", fake: newFakeGrid(20, nil), at: point{0, 0}, claude: true, want: claudeSel(selLine, 0, 0, 20), today: true},

		{name: "flag false bullet", pane: p80, at: point{26, 10}, want: claudeSel(selLine, 26, 26, 80), today: true},
		{name: "flag false history break", pane: p80, at: point{19, 5}, want: claudeSel(selLine, 19, 19, 80), today: true},
	}

	runClaudeClicks(t, cases, lineAtFor, lineAt)
}
