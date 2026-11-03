package ui

import (
	"fmt"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	uv "github.com/charmbracelet/ultraviolet"

	"github.com/mhersson/aim/internal/md"
)

// allOf selects every cell of every line of v.
func allOf(v *mdView) selection {
	return selection{mode: selStream, anchor: point{0, 0}, head: point{len(v.layout().Lines) - 1, v.w - 1}}
}

func TestTextGrid(t *testing.T) {
	t.Parallel()

	v := newTestView("one two three four five six", 16, 10)
	g := v.grid()

	if g.Rows() != 3 || g.Cols() != 16 {
		t.Fatalf("grid is %dx%d, want 3x16", g.Rows(), g.Cols())
	}

	if got, want := wordAt(g, point{1, 3}), (selection{selStream, point{1, 2}, point{1, 6}}); got != want {
		t.Errorf("wordAt = %+v, want %+v", got, want)
	}

	if got, want := lineAt(g, point{1, 3}), (selection{selLine, point{0, 0}, point{2, 15}}); got != want {
		t.Errorf("lineAt = %+v, want %+v", got, want)
	}

	if got := v.unitText(1); got != "one two three four five six" {
		t.Errorf("unitText = %q, want the whole paragraph", got)
	}

	code := newTestView("```go\nx := 1\n```", 30, 10)
	cg := code.grid()

	for col := range cg.Cols() {
		if cellClass(cg, 0, col) != classBlank {
			t.Errorf("frame cell %d reads %+v, want blank", col, cg.Cell(0, col))
		}
	}

	if got := extract(cg, selection{mode: selStream, anchor: point{1, 0}, head: point{1, 29}}); !strings.Contains(got, "x := 1") {
		t.Errorf("code row reads %q", got)
	}
}

func TestTextGridWhole(t *testing.T) {
	t.Parallel()

	v := newTestView("```\n\tx\n```", 30, 10)
	g := v.grid()
	lt := v.lineText(1)

	var tab, text int

	for _, p := range lt.Pieces {
		if p.Text == "\t" {
			tab = p.Col
		} else {
			text = p.Col
		}
	}

	if c := g.Cell(1, tab); c == nil || charClass(c.Content) != classBlank {
		t.Errorf("tab cell = %+v, want blank", c)
	}

	if c := g.Cell(1, text); c == nil || c.Content != "x" {
		t.Errorf("text cell = %+v, want x", c)
	}

	img := newTestView("![alt](a.png)", 30, 10)
	ic := img.grid().Cell(0, 2)

	if ic == nil || charClass(ic.Content) != classOther {
		t.Errorf("image cell = %+v, want one character of class other", ic)
	}
}

func TestMDViewSelectedText(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		src   string
		width int
		want  string
	}{
		{"wrap", "one two three four five six", 12, "one two three four five six"},
		{"hard break", "a  \nb", 20, "a\nb"},
		{"list", "- one two three four five", 14, "• one two three four five"},
		{"quote", "> one two three four", 12, "one two three four"},
		{"code tab", "```go\n\tx := 1\n```", 30, "\tx := 1"},
		{"cut code", "```\nabcdefghijklmnopqrstuvwxyz\n```", 20, "abcdefghijklmnopqrstuvwxyz"},
		{"table", "| h1 | h2 |\n| --- | --- |\n| one two three | x |", 20, "h1\th2\none two three\tx"},
		{"image line", "![alt](https://example.com/a.png)", 40, "![alt](https://example.com/a.png)"},
		{"two paragraphs", "one\n\ntwo", 20, "one\n\ntwo"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			v := newTestView(tt.src, tt.width+4, 20)

			if got := freshCopy(v.grid(), allOf(v)); got != tt.want {
				t.Errorf("copies %q, want %q", got, tt.want)
			}
		})
	}
}

func TestMDViewCopyLarge(t *testing.T) {
	t.Parallel()

	var b strings.Builder

	const n = 40

	for i := range n {
		fmt.Fprintf(&b, "- line %d\n", i)
	}

	v := newTestView(b.String(), 40, 20)
	lines := len(v.layout().Lines)

	if lines != n {
		t.Fatalf("doc has %d lines, want %d", lines, n)
	}

	calls := map[int]int{}
	got := textCopy(func(i int) md.LineText {
		calls[i]++

		return v.lineText(i)
	}, allOf(v), v.w)

	want := make([]string, n)
	for i := range want {
		want[i] = fmt.Sprintf("• line %d", i)
	}

	if got != strings.Join(want, "\n") {
		t.Errorf("copy is %d bytes, not the %d lines in order", len(got), n)
	}

	for i := range n {
		if calls[i] != 1 {
			t.Fatalf("lineText(%d) called %d times, want 1", i, calls[i])
		}
	}

	if len(calls) != n {
		t.Errorf("lineText called for %d lines, want %d", len(calls), n)
	}
}

// freshCopy is what s copies over g with every row read again, the reference
// for a copy of the held text.
func freshCopy(g textGrid, s selection) string {
	s.anchor, s.head = clampPoint(g, s.anchor), clampPoint(g, s.head)

	return textCopy(g.lines, s, g.cols)
}

func TestTextCopyReadsSelectedRowsOnly(t *testing.T) {
	t.Parallel()

	v := newTestView("a\n\nb\n\nc\n\nd", 20, 20)
	seen := map[int]bool{}
	s := selection{mode: selStream, anchor: point{2, 0}, head: point{4, 19}}

	got := textCopy(func(i int) md.LineText {
		seen[i] = true

		return v.lineText(i)
	}, s, v.w)

	if got != "b\n\nc" {
		t.Errorf("copies %q, want b, blank, c", got)
	}

	if len(seen) != 3 || !seen[2] || !seen[3] || !seen[4] {
		t.Errorf("read lines %v, want 2 to 4 only", seen)
	}
}

func TestTextPaint(t *testing.T) {
	t.Parallel()

	v := newTestView("hello world\n\n```\n\tx\n```\n\n![alt](a.png)", 40, 20)
	lay := v.layout()

	tests := []struct {
		name     string
		line     int
		from, to int
		want     [][2]int
	}{
		{"part of a word", 0, 3, 5, [][2]int{{3, 5}}},
		{"cells before the text", 0, 0, 3, [][2]int{{2, 3}}},
		{"all", 0, 0, 39, [][2]int{{2, 12}}},
		{"nothing picked", 0, 30, 39, nil},
		{"a tab is whole", lineOf(lay, "x"), 0, 39, [][2]int{{6, 10}}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got := textPaint(v.lineText(tt.line), tt.from, tt.to)
			if fmt.Sprint(got) != fmt.Sprint(tt.want) {
				t.Errorf("textPaint = %v, want %v", got, tt.want)
			}
		})
	}

	if got := textPaint(md.LineText{Picture: true, Pieces: []md.Piece{{Col: 2, Cells: 5, Text: "x", Whole: true}}}, 0, 9); got != nil {
		t.Errorf("picture row paints %v, want none", got)
	}

	whole := md.LineText{Pieces: []md.Piece{{Col: 4, Cells: 6, Text: "tag", Whole: true}}}
	if got := textPaint(whole, 7, 7); fmt.Sprint(got) != "[[4 9]]" {
		t.Errorf("a whole piece paints %v, want [[4 9]]", got)
	}
}

func TestMDViewSelectionPaint(t *testing.T) {
	t.Parallel()
	th := mochaTheme(t)
	p := newMDPainter(th, th.UI.Bg).withQuery("find")
	src := "> quoted\n\n```go\ncode_here\n```\n\nfind me"
	v := newTestView(src, 40, 12)
	lay := v.layout()

	quote, code, match := lineOf(lay, "quoted"), lineOf(lay, "code_here"), lineOf(lay, "find me")
	v.cursor = 0
	v.clamp()

	s := allOf(v)
	v.sel = &s

	rows := v.draw(p, true)
	buf := uv.NewScreenBuffer(40, len(rows))
	uv.NewStyledString(strings.Join(rows, "\n")).Draw(buf, buf.Bounds())

	on := func(x, y int) bool {
		c := buf.CellAt(x, y)

		return c != nil && c.Style.Bg != nil && sameColor(c.Style.Bg, th.UI.Selection)
	}

	for _, line := range []int{quote, code, match} {
		lt := v.lineText(line)
		if len(lt.Pieces) == 0 {
			t.Fatalf("line %d has no pieces", line)
		}

		for _, pc := range lt.Pieces {
			if !on(pc.Col, line) {
				t.Errorf("line %d text cell %d is not selected", line, pc.Col)
			}
		}
	}

	for y := range rows {
		for _, x := range []int{0, 1} {
			if on(x, y) {
				t.Errorf("cell %d of row %d is selected", x, y)
			}
		}
	}

	// The quote bar sits right of the lead, before the text.
	first := v.lineText(quote).Pieces[0].Col
	for x := 2; x < first; x++ {
		if on(x, quote) {
			t.Errorf("quote chrome cell %d is selected", x)
		}
	}

	// The frame above the code and its first cells stay off the selection.
	for x := range 40 {
		if on(x, code-1) {
			t.Errorf("code frame cell %d is selected", x)
		}
	}

	codeFirst := v.lineText(code).Pieces[0].Col
	for x := 2; x < codeFirst; x++ {
		if on(x, code) {
			t.Errorf("code row chrome cell %d is selected", x)
		}
	}

	// A match keeps its foreground on the selection ground.
	mc := buf.CellAt(v.lineText(match).Pieces[0].Col, match)
	if mc == nil || mc.Style.Fg == nil || !sameColor(mc.Style.Fg, th.UI.Match) {
		t.Errorf("match cell = %+v, want the Match foreground", mc)
	}

	// The cursor's mark stays.
	if c := buf.CellAt(0, 0); c == nil || c.Content != "▎" {
		t.Errorf("cursor mark = %+v", c)
	}
}

func TestMDViewSelectionFollowsPicture(t *testing.T) {
	t.Parallel()

	var b strings.Builder

	b.WriteString("intro\n\n![a](a.png)\n\n")

	for i := range 10 {
		fmt.Fprintf(&b, "para %d\n\n", i)
	}

	d, p, jobs, _ := picDoc(t, b.String())
	v := &mdView{doc: d}
	v.size(40, 10, false)

	from := lineOf(v.layout(), "para 2")
	to := lineOf(v.layout(), "para 4")
	v.sel = &selection{mode: selStream, anchor: point{from, 1}, head: point{to, 5}}

	answerDoc(t, p, jobs, 50, 160)

	if rows := v.layout().Images[0].Rows; rows != 8 {
		t.Fatalf("picture rows = %d, want 8", rows)
	}

	want := selection{mode: selStream, anchor: point{from + 7, 1}, head: point{to + 7, 5}}
	if *v.sel != want {
		t.Errorf("selection = %+v, want %+v", *v.sel, want)
	}

	if got := d.text()[v.sel.anchor.row]; got != "para 2" {
		t.Errorf("anchor on %q, want para 2", got)
	}
}

func TestOneSelection(t *testing.T) {
	t.Parallel()

	setup := func(t *testing.T) (*App, *mdView, selection) {
		t.Helper()

		a, _, _ := shellsWith(t, "left text", "right text")
		v := newTestView("one two three", 20, 10)

		return a, v, selection{mode: selStream, anchor: point{0, 2}, head: point{0, 4}}
	}

	t.Run("a text selection clears the pane's", func(t *testing.T) {
		t.Parallel()

		a, v, s := setup(t)
		a.sel = &paneSelection{pane: "p1"}
		a.setTextSel(&textSelection{view: v}, s)

		if a.sel != nil || a.text == nil || v.sel == nil || *v.sel != s {
			t.Errorf("sel = %v, text = %v, view sel = %v", a.sel, a.text, v.sel)
		}

		if got := a.text.held.text(); got != "one" {
			t.Errorf("held text = %q, want one", got)
		}
	})

	t.Run("a new text selection clears the old holder's", func(t *testing.T) {
		t.Parallel()

		a, v, s := setup(t)
		other := newTestView("alpha beta", 20, 10)

		a.setTextSel(&textSelection{view: v}, s)
		a.setTextSel(&textSelection{view: other}, s)

		if v.sel != nil || other.sel == nil || a.text.view != other {
			t.Errorf("old = %v, new = %v", v.sel, other.sel)
		}
	})

	t.Run("a pane press clears the text selection", func(t *testing.T) {
		t.Parallel()

		a, v, s := setup(t)
		a.setTextSel(&textSelection{view: v}, s)
		a.contentPress(hitResult{area: hitContent, pane: "p1", local: point{1, 1}}, tea.Mouse{X: 2, Y: 2, Button: tea.MouseLeft})

		if a.text != nil || v.sel != nil {
			t.Errorf("text = %v, view sel = %v", a.text, v.sel)
		}
	})

	t.Run("copy mode clears both", func(t *testing.T) {
		t.Parallel()

		a, v, s := setup(t)
		a.setTextSel(&textSelection{view: v}, s)
		a.sel = &paneSelection{pane: "p1"}
		a.enterCopyMode()

		if a.sel != nil || a.text != nil || v.sel != nil {
			t.Errorf("sel = %v, text = %v, view sel = %v", a.sel, a.text, v.sel)
		}
	})

	t.Run("a resize clears the text selection", func(t *testing.T) {
		t.Parallel()

		a, v, s := setup(t)
		a.setTextSel(&textSelection{view: v}, s)
		a.Update(tea.WindowSizeMsg{Width: 70, Height: 20})

		if a.text != nil || v.sel != nil {
			t.Errorf("text = %v, view sel = %v", a.text, v.sel)
		}
	})

	t.Run("leader y copies the text selection", func(t *testing.T) {
		t.Parallel()

		a, v, s := setup(t)
		f := fakeClip(a)
		a.setTextSel(&textSelection{view: v}, s)
		cmd := a.copySelection()

		for _, m := range flatten(cmd) {
			a.Update(m)
		}

		if got := lastCopied(f); got != "one" {
			t.Errorf("copied %q, want one", got)
		}
	})
}

func TestTextSelAfterUpdate(t *testing.T) {
	t.Parallel()

	const src = "# Title\n\nfirst line here\n\nsecond line there\n"

	tests := []struct {
		name string
		edit func(a *App)
		keep bool
	}{
		{"nothing changed", func(*App) {}, true},
		{"a doc swapped for the same text", func(a *App) { a.plan.view.doc = parseNow(src, nil) }, true},
		{"the width changed", func(a *App) { a.plan.view.size(a.plan.view.w-2, a.plan.view.h, a.plan.view.icons) }, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			r := newPlanRig(t, 100, 30)
			fakeClip(r.a)
			r.filePlan(t, "plan.md", src)
			r.open(t)

			v := r.a.plan.shown()
			line := lineOf(v.layout(), "first line")
			r.a.setTextSel(&textSelection{view: v}, selection{mode: selStream, anchor: point{line, 2}, head: point{line, 12}})

			if r.a.text == nil {
				t.Fatal("no text selection")
			}

			tt.edit(r.a)
			r.a.Update(tea.FocusMsg{})

			if kept := r.a.text != nil; kept != tt.keep {
				t.Errorf("kept = %v, want %v", kept, tt.keep)
			}

			if !tt.keep && v.sel != nil {
				t.Errorf("the view still holds %+v", *v.sel)
			}
		})
	}
}
