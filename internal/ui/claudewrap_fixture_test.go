package ui

import (
	"fmt"
	"strings"
	"testing"

	"github.com/mhersson/aim/internal/core"
)

// claudeDump is the screen text of a Claude Code session, oldest line first: the
// first len(lines)-rows lines end up in the history.
type claudeDump struct {
	cols, rows int
	lines      []string
}

// bytes draws every line with cursor positioning, as Claude Code does: the
// lines past the screen's height scroll the top row into the history with a
// line feed on the bottom row. Nothing is printed while the cursor waits at the
// right margin, so no row carries a soft-wrap flag.
func (d claudeDump) bytes() []byte {
	var b strings.Builder

	for i, line := range d.lines {
		if i < d.rows {
			fmt.Fprintf(&b, "\x1b[%d;1H%s", i+1, line)
		} else {
			fmt.Fprintf(&b, "\x1b[%d;1H\n%s", d.rows, line)
		}
	}

	return []byte(b.String())
}

// hist is the number of history rows, so screen row sNNNN is absolute row
// hist()+NNNN.
func (d claudeDump) hist() int { return len(d.lines) - d.rows }

// claudeGridPane is a pane that has drawn d, and fails the test unless the
// grid holds every line and no row reports a soft wrap.
func claudeGridPane(t *testing.T, d claudeDump) (*pane, *fakeProc) {
	t.Helper()

	p, fp := newTestPane(t, d.cols, d.rows, realClock{})
	feedAndWait(t, p, fp, string(d.bytes()))

	p.withGrid(func(g grid) {
		if g.Rows() != len(d.lines) {
			t.Fatalf("rows = %d, want %d", g.Rows(), len(d.lines))
		}

		for row := range g.Rows() {
			if g.Wrapped(row) {
				t.Fatalf("row %d reports a soft wrap", row)
			}
		}
	})

	return p, fp
}

// claudeApp is a framed app whose one pane, p1, has drawn d. The body starts on
// row 2 and the frame insets the content by one cell, so a window of
// d.cols+2 x d.rows+4 gives the pane exactly d's size.
func claudeApp(t *testing.T, d claudeDump, kind core.PaneKind, harnessID string) (*App, *fakeProc, *fakeRun) {
	t.Helper()

	a, procs := framedApp(t, d.cols+2, d.rows+4, paneSpec{
		kind: kind, cwd: "/w/a", split: core.SplitVertical, text: string(d.bytes()), harness: harnessID,
	})

	if c := contentRectOf(a, "p1"); c.W != d.cols || c.H != d.rows {
		t.Fatalf("content = %dx%d, want %dx%d", c.W, c.H, d.cols, d.rows)
	}

	return a, procs["p1"], fakeClip(a)
}

// claudePath is 112 characters, longer than a row at either width.
var claudePath = "/" + strings.Join([]string{
	"home", "user", "projects", "aim", "internal", "ui", "testdata",
	"TestPlanOverlayGolden", "catppuccin-mocha", "icons=true", "plan-80x24.golden",
}, "/")

const claudeWantPrompt = `❯ Please summarise the copy change for me in a reply that has a long paragraph, a list, a quote, a long URL and a code block, so I can check how it wraps.`

var claudeWantReply = strings.Join([]string{
	`● Here is a summary of the change. The copy path joins rows only where the terminal itself wrapped a line at the right edge, so text that an application wraps on its own reaches the clipboard with a newline at every visual row, which is not what anyone selecting a paragraph expects to paste.`,
	``,
	`  A second paragraph mentions internal/ui/selection.go in inline code and has bold words in the middle of a sentence that is long enough to wrap.`,
	`  This line follows a single newline in the source.`,
	``,
	`  - The first bullet is long enough that it wraps onto a second row, and maybe a third one when the pane is narrow.`,
	`  - A short bullet.`,
	`  - A bullet with a long path: ` + claudePath,
	`    - A nested bullet that is also long enough to wrap onto the next row of the pane.`,
	``,
	`  1. A numbered item whose text runs past the edge of the pane so that the renderer has to wrap it.`,
	`  2. Second item.`,
	``,
	`  ▎ A quoted paragraph that is long enough to wrap, so the quote's gutter shows on each of its rows when it wraps.`,
	``,
	`  See https://github.com/example/repository/blob/main/internal/ui/selection.go?highlight=extract&view=plain&line=243 for the code.`,
	``,
	`  func extract(g grid, s selection) string {`,
	`          return strings.Join(rowsOf(g, s), "\n") + someVeryLongTrailingExpression(g, s, "that runs past the edge")`,
	`  }`,
	``,
	`  Done.`,
}, "\n")

// claudeDefault80 is Claude Code's default renderer at 80 columns: the prompt
// and the tool rows are in the history, the reply starts in the history and
// ends on the screen.
var claudeDefault80 = claudeDump{cols: 80, rows: 40, lines: []string{
	``,
	` ▐▛███▜▌   Claude Code v2.1.322`,
	`▝▜█████▛▘  Opus 5.5 · Claude Max`,
	`  ▘▘ ▝▝    aim`,
	``,
	`❯ Please summarise the copy change for me in a reply that has a long paragraph,`,
	`  a list, a quote, a long URL and a code block, so I can check how it wraps.`,
	``,
	`● Read(internal/ui/selection.go)`,
	`  ⎿  Read 318 lines`,
	``,
	`● Bash(grep -n "Wrapped(" internal/ui/selection.go internal/ui/scrollview.go`,
	`      internal/ui/mouse.go)`,
	`  ⎿  internal/ui/selection.go:20:  // Wrapped reports whether row soft-wraps`,
	`     … +6 lines (ctrl+o to expand)`,
	``,
	`● Here is a summary of the change. The copy path joins rows only where the`,
	`  terminal itself wrapped a line at the right edge, so text that an application`,
	`  wraps on its own reaches the clipboard with a newline at every visual row,`,
	`  which is not what anyone selecting a paragraph expects to paste.`,
	``,
	`  A second paragraph mentions internal/ui/selection.go in inline code and has`,
	`  bold words in the middle of a sentence that is long enough to wrap.`,
	`  This line follows a single newline in the source.`,
	``,
	`  - The first bullet is long enough that it wraps onto a second row, and maybe a`,
	`    third one when the pane is narrow.`,
	`  - A short bullet.`,
	`  - A bullet with a long path: ` + claudePath[:49],
	`    ` + claudePath[49:],
	`    - A nested bullet that is also long enough to wrap onto the next row of the`,
	`      pane.`,
	``,
	`  1. A numbered item whose text runs past the edge of the pane so that the`,
	`     renderer has to wrap it.`,
	`  2. Second item.`,
	``,
	`  ▎ A quoted paragraph that is long enough to wrap, so the quote's gutter shows`,
	`  ▎ on each of its rows when it wraps.`,
	``,
	`  See https://github.com/example/repository/blob/main/internal/ui/selection.go?h`,
	`  ighlight=extract&view=plain&line=243 for the code.`,
	``,
	`  func extract(g grid, s selection) string {`,
	`          return strings.Join(rowsOf(g, s), "\n") +`,
	`  someVeryLongTrailingExpression(g, s, "that runs past the edge")`,
	`  }`,
	``,
	`  Done.`,
	``,
	strings.Repeat("─", 80),
	`❯`,
	strings.Repeat("─", 80),
	`  ? for shortcuts`,
	``,
	``,
	``,
}}

// claudeFullscreen117 is what Claude Code's fullscreen renderer draws at 117
// columns; its 40 lines fill a 40-row screen, so it has no history.
var claudeFullscreen117 = claudeDump{cols: 117, rows: 40, lines: []string{
	``,
	``,
	``,
	``,
	``,
	``,
	`❯ Please summarise the copy change for me in a reply that has a long paragraph, a list, a quote, a long URL and a`,
	`  code block, so I can check how it wraps.`,
	``,
	`● Read 1 file, searched for 1 pattern (ctrl+o to expand)`,
	``,
	`● Here is a summary of the change. The copy path joins rows only where the terminal itself wrapped a line at the`,
	`  right edge, so text that an application wraps on its own reaches the clipboard with a newline at every visual row,`,
	`  which is not what anyone selecting a paragraph expects to paste.`,
	``,
	`  A second paragraph mentions internal/ui/selection.go in inline code and has bold words in the middle of a sentence`,
	`  that is long enough to wrap.`,
	`  This line follows a single newline in the source.`,
	``,
	`  - The first bullet is long enough that it wraps onto a second row, and maybe a third one when the pane is narrow.`,
	`  - A short bullet.`,
	`  - A bullet with a long path:`,
	`    ` + claudePath,
	`    - A nested bullet that is also long enough to wrap onto the next row of the pane.`,
	``,
	`  1. A numbered item whose text runs past the edge of the pane so that the renderer has to wrap it.`,
	`  2. Second item.`,
	``,
	`  ▎ A quoted paragraph that is long enough to wrap, so the quote's gutter shows on each of its rows when it wraps.`,
	``,
	`  See https://github.com/example/repository/blob/main/internal/ui/selection.go?highlight=extract&view=plain&line=243`,
	`  for the code.`,
	``,
	`  func extract(g grid, s selection) string {`,
	`          return strings.Join(rowsOf(g, s), "\n") + someVeryLongTrailingExpression(g, s, "that runs past the edge")`,
	`  }`,
	``,
	`  Done.`,
	strings.Repeat("─", 117),
	`❯`,
}}
