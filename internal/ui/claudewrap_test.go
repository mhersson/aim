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
