package ui

import (
	"fmt"
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
)

const scrollCols = 10

// scrollPane is a scrollCols x rows pane that has printed lines "L00000" to
// L(n-1) and a final "END<n>" line, rendered; its last row is END<n>.
func scrollPane(t *testing.T, rows, n int) (*pane, *fakeProc) {
	t.Helper()

	p, fp := newTestPane(t, scrollCols, rows, realClock{})
	feedLines(t, p, fp, 0, n)

	return p, fp
}

func lineName(i int) string { return fmt.Sprintf("L%05d", i) }

// feedLines prints lines [from, to) and waits until the pane has them.
func feedLines(t *testing.T, p *pane, fp *fakeProc, from, to int) {
	t.Helper()

	feedPaddedLines(t, p, fp, from, to, "")
}

// feedPaddedLines is feedLines with pad after each line's name.
func feedPaddedLines(t *testing.T, p *pane, fp *fakeProc, from, to int, pad string) {
	t.Helper()

	var sb strings.Builder

	// A later call overwrites the previous call's END line.
	sb.WriteString("\r\x1b[2K")

	for i := from; i < to; i++ {
		sb.WriteString(lineName(i) + pad + "\r\n")
	}

	end := fmt.Sprintf("END%d", to)
	sb.WriteString(end)

	if _, err := fp.feed.Write([]byte(sb.String())); err != nil {
		t.Fatal(err)
	}

	eventually(t, "END on screen", func() bool {
		p.mu.Lock()
		defer p.mu.Unlock()

		for y := range p.rows {
			if strings.HasPrefix(ansi.Strip(renderRow(p.vt, y, scrollCols)), end) {
				return true
			}
		}

		return false
	})

	p.render()
}

// text returns the stripped, right-trimmed rows of a view.
func text(rows []string) []string {
	out := make([]string, len(rows))
	for i, r := range rows {
		out[i] = strings.TrimRight(ansi.Strip(r), " ")
	}

	return out
}

func TestScrollViewShowsHistory(t *testing.T) {
	t.Parallel()
	p, _ := scrollPane(t, 5, 20)

	// 20 lines + END on 5 rows: the screen holds L17 L18 L19 END and the
	// row above it, history holds L00000 to L00016.
	if got, want := text(p.lines()), []string{"L00016", "L00017", "L00018", "L00019", "END20"}; strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("live rows = %v, want %v", got, want)
	}

	if !p.scrollBy(3) {
		t.Fatal("scrollBy(3) did not move")
	}

	rows := p.lines()
	if len(rows) != 5 {
		t.Fatalf("view has %d rows, want 5", len(rows))
	}

	for i, r := range rows {
		if w := ansi.StringWidth(r); w != scrollCols {
			t.Errorf("row %d width = %d, want %d", i, w, scrollCols)
		}
	}

	if got, want := text(rows), []string{"L00013", "L00014", "L00015", "L00016", "L00017"}; strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("scrolled rows = %v, want %v", got, want)
	}

	if got := p.linesAbove(); got != 3 {
		t.Fatalf("linesAbove = %d, want 3", got)
	}
}

func TestScrollClampsAndReturnsLive(t *testing.T) {
	t.Parallel()
	p, _ := scrollPane(t, 5, 20)
	hist := p.viewTop()

	p.scrollBy(1000)

	if got := p.linesAbove(); got != hist {
		t.Fatalf("linesAbove after a huge scroll = %d, want the history length %d", got, hist)
	}

	if got := text(p.lines())[0]; got != "L00000" {
		t.Fatalf("top row = %q, want the oldest line", got)
	}

	if p.scrollBy(1) {
		t.Error("scrolling past the oldest line moved")
	}

	p.scrollBy(-1000)

	if got := p.linesAbove(); got != 0 {
		t.Fatalf("linesAbove after scrolling down = %d, want 0 (live)", got)
	}

	if p.scrollBy(-1) {
		t.Error("scrolling down while live moved")
	}

	if got := text(p.lines())[4]; got != "END20" {
		t.Fatalf("live bottom row = %q", got)
	}
}

// Output keeps a scrolled view on the same text while the tag grows.
func TestScrollViewPinnedOnOutput(t *testing.T) {
	t.Parallel()
	p, fp := scrollPane(t, 5, 20)
	p.scrollBy(6)

	before := text(p.lines())

	feedLines(t, p, fp, 20, 27)

	if got := text(p.lines()); strings.Join(got, ",") != strings.Join(before, ",") {
		t.Fatalf("view moved with output: %v, was %v", got, before)
	}

	if got := p.linesAbove(); got != 6+7 {
		t.Fatalf("linesAbove = %d, want 13", got)
	}
}

// Past the 10000-line cap the view stays on its text until that text is
// trimmed, then clamps to the oldest line.
func TestScrollViewAcrossHistoryCap(t *testing.T) {
	t.Parallel()
	p, fp := scrollPane(t, 3, 10100)

	p.scrollBy(100)

	pinned := text(p.lines())[0]

	feedLines(t, p, fp, 10100, 10150)

	if got := text(p.lines())[0]; got != pinned {
		t.Fatalf("top row = %q after 50 trimmed lines, want %q", got, pinned)
	}

	if got := p.linesAbove(); got < 100 {
		t.Fatalf("linesAbove = %d, want at least 100", got)
	}

	feedLines(t, p, fp, 10150, 20200)

	rows := text(p.lines())

	var oldest string

	p.withGrid(func(g grid) {
		var sb strings.Builder

		for c := range g.Cols() {
			if cell := g.Cell(0, c); cell != nil {
				sb.WriteString(cell.Content)
			}
		}

		oldest = strings.TrimSpace(sb.String())
	})

	if rows[0] != oldest {
		t.Fatalf("top row = %q after the anchor was trimmed, want the oldest line %q", rows[0], oldest)
	}

	if p.viewTop() != 0 {
		t.Fatalf("view top = %d, want the view clamped to row 0", p.viewTop())
	}
}

func TestPasteReturnsToLive(t *testing.T) {
	t.Parallel()
	p, _ := scrollPane(t, 5, 20)
	p.scrollBy(4)

	p.sendPaste("x")

	if got := p.linesAbove(); got != 0 {
		t.Fatalf("linesAbove = %d after a paste, want 0", got)
	}
}

func TestScrollReturnsLiveOnResizeAndAltScreen(t *testing.T) {
	t.Parallel()
	t.Run("resize", func(t *testing.T) {
		p, _ := scrollPane(t, 5, 20)
		p.scrollBy(4)

		if !p.resize(scrollCols, 4) {
			t.Fatal("resize did not change the size")
		}

		if got := p.linesAbove(); got != 0 {
			t.Fatalf("linesAbove = %d after a resize, want 0", got)
		}
	})

	t.Run("alt screen", func(t *testing.T) {
		p, fp := scrollPane(t, 5, 20)
		p.scrollBy(4)

		feedAndWait(t, p, fp, "\x1b[?1049h")
		p.render()

		if got := p.linesAbove(); got != 0 {
			t.Fatalf("linesAbove = %d on the alt screen, want 0", got)
		}

		if p.scrollBy(3) {
			t.Error("the alt screen scrolled")
		}

		feedAndWait(t, p, fp, "\x1b[?1049l")
		p.render()

		if got := p.linesAbove(); got != 0 {
			t.Fatalf("linesAbove = %d back on the main screen, want 0", got)
		}
	})
}

func TestScrolledViewRowsFitAfterResize(t *testing.T) {
	t.Parallel()

	for _, cols := range []int{scrollCols + 7, scrollCols - 4} {
		t.Run(fmt.Sprint(cols), func(t *testing.T) {
			p, _ := scrollPane(t, 5, 20)
			p.scrollBy(4)
			_ = p.lines()

			if !p.resize(cols, 5) {
				t.Fatal("resize did not change the size")
			}

			p.scrollBy(4)

			for i, r := range p.lines() {
				if w := ansi.StringWidth(r); w != cols {
					t.Errorf("row %d width = %d, want %d", i, w, cols)
				}
			}
		})
	}
}

func TestCursorHiddenWhileScrolled(t *testing.T) {
	t.Parallel()
	p, _ := scrollPane(t, 5, 20)

	if _, visible := p.cursor(); !visible {
		t.Fatal("live cursor hidden")
	}

	p.scrollBy(2)

	if _, visible := p.cursor(); visible {
		t.Fatal("cursor visible while scrolled")
	}
}

func TestScrollViewCache(t *testing.T) {
	t.Parallel()
	p, fp := scrollPane(t, 5, 20)
	p.scrollBy(3)

	a := p.lines()

	if b := p.lines(); &a[0] != &b[0] {
		t.Error("an unchanged view was rebuilt")
	}

	p.scrollBy(1)

	if c := p.lines(); &a[0] == &c[0] {
		t.Error("a scrolled view was reused")
	}

	// A screen change below a pinned view is picked up.
	p.scrollBy(-1)
	before := strings.Join(text(p.lines()), ",")

	feedAndWait(t, p, fp, "\x1b[1;1HZZZ")
	p.render()

	if after := strings.Join(text(p.lines()), ","); after == before {
		t.Error("view ignored a change of the screen rows it shows")
	}
}

func TestScrollLive(t *testing.T) {
	t.Parallel()
	p, _ := scrollPane(t, 5, 20)

	if p.scrollLive() {
		t.Error("scrollLive reported a scrolled view on a live pane")
	}

	p.scrollBy(4)

	if !p.scrollLive() || p.linesAbove() != 0 {
		t.Errorf("scrollLive did not return to live (linesAbove = %d)", p.linesAbove())
	}
}

func TestSetViewTop(t *testing.T) {
	t.Parallel()
	p, _ := scrollPane(t, 5, 20)
	hist := p.viewTop()

	p.setViewTop(2)

	if got := p.viewTop(); got != 2 {
		t.Fatalf("viewTop = %d, want 2", got)
	}

	if got := text(p.lines())[0]; got != "L00002" {
		t.Fatalf("top row = %q", got)
	}

	p.setViewTop(hist + 50)

	if p.linesAbove() != 0 || p.viewTop() != hist {
		t.Fatalf("a top past the history is live: linesAbove = %d, viewTop = %d", p.linesAbove(), p.viewTop())
	}
}

func TestPaneGrid(t *testing.T) {
	t.Parallel()
	p, fp := newTestPane(t, scrollCols, 3, realClock{})

	// Row 0 wraps into row 1 (12 > 10 columns); a wide rune follows.
	feedAndWait(t, p, fp, "ABCDEFGHIJKL\r\nx中y\r\nm\r\nn\r\no")
	p.render()

	p.withGrid(func(g grid) {
		hist := g.Rows() - 3
		if hist < 2 {
			t.Fatalf("history = %d rows, want at least 2", hist)
		}

		if g.Cols() != scrollCols {
			t.Errorf("Cols = %d", g.Cols())
		}

		if c := g.Cell(0, 0); c == nil || c.Content != "A" {
			t.Errorf("Cell(0,0) = %v, want A", c)
		}

		if !g.Wrapped(0) || g.Wrapped(1) {
			t.Errorf("Wrapped(0,1) = %v %v, want true false", g.Wrapped(0), g.Wrapped(1))
		}

		if c := g.Cell(2, 0); c == nil || c.Content != "x" {
			t.Fatalf("Cell(2,0) = %v", c)
		}

		if c := g.Cell(2, 1); c == nil || c.Content != "中" || c.Width != 2 {
			t.Errorf("wide Cell(2,1) = %v", c)
		}

		if c := g.Cell(2, 2); c == nil || c.Width != 0 || c.Content != "" {
			t.Errorf("continuation Cell(2,2) = %v, want width 0", c)
		}

		if c := g.Cell(g.Rows()-1, 0); c == nil || c.Content != "o" {
			t.Errorf("last screen row cell = %v, want o", c)
		}

		for _, rc := range [][2]int{{-1, 0}, {0, -1}, {0, scrollCols}, {g.Rows(), 0}} {
			if c := g.Cell(rc[0], rc[1]); c != nil {
				t.Errorf("Cell(%d,%d) = %v, want nil", rc[0], rc[1], c)
			}
		}

		if g.Wrapped(-1) || g.Wrapped(g.Rows()) {
			t.Error("Wrapped outside the grid")
		}
	})
}

func TestPaneGridAltScreenIsScreenOnly(t *testing.T) {
	t.Parallel()
	p, fp := scrollPane(t, 4, 20)

	feedAndWait(t, p, fp, "\x1b[?1049h")
	p.render()

	p.withGrid(func(g grid) {
		if g.Rows() != 4 {
			t.Errorf("alt screen grid has %d rows, want 4", g.Rows())
		}
	})
}

func TestWithGridOnClosedPaneRunsNothing(t *testing.T) {
	t.Parallel()
	p, _ := scrollPane(t, 4, 5)
	_ = p.close()

	p.withGrid(func(grid) { t.Error("withGrid ran on a closed pane") })
}
