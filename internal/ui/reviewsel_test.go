package ui

import (
	"fmt"
	"image/color"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"unicode/utf8"

	tea "charm.land/bubbletea/v2"
	uv "github.com/charmbracelet/ultraviolet"
	"github.com/charmbracelet/x/ansi"

	"github.com/mhersson/aim/internal/core"
	"github.com/mhersson/aim/internal/git"
	"github.com/mhersson/aim/internal/md"
	"github.com/mhersson/aim/internal/ui/diffview"
)

// selPage is a page rig with docs/a.md shown, a fake clipboard and the drawer
// holding the keyboard.
type selPage struct {
	*pageRig
	p    *mdPage
	clip *fakeRun
}

func newSelPage(t *testing.T, opts ...func(*Options)) *selPage {
	t.Helper()

	m := newPageRig(t, nil, opts...)
	f := fakeClip(m.App)
	p := m.openPage(t, "docs/a.md")

	m.drawerTake()
	m.drawn(t)

	return &selPage{pageRig: m, p: p, clip: f}
}

// word is the screen cells of the first drawer row holding row: the cells of
// word on it, first and last, and the row.
func (m *selPage) word(t *testing.T, row, word string) (x0, x1, y int) {
	t.Helper()

	rect := mustRect(t, m.drawerRig)

	for y, line := range strings.Split(m.drawn(t), "\n") {
		if !strings.Contains(line, row) {
			continue
		}

		i := strings.Index(line, word)
		x0 = rect.X + utf8.RuneCountInString(line[:i])

		return x0, x0 + utf8.RuneCountInString(word) - 1, rect.Y + y
	}

	t.Fatalf("no drawer row holds %q", row)

	return 0, 0, 0
}

func (m *selPage) selected() bool { return m.text != nil && m.p.view.sel != nil }

// dragIntro selects "intro text".
func (m *selPage) dragIntro(t *testing.T) {
	t.Helper()

	x0, x1, y := m.word(t, "intro text", "intro text")
	dragRun(m.App, x0, y, x1, y, 0)
}

func (m *selPage) key(k tea.KeyPressMsg) { run(m.App, press(m.App, k)) }

func TestPageDragCopies(t *testing.T) {
	t.Parallel()

	m := newSelPage(t)
	m.dragIntro(t)

	if got := primaryWrites(m.clip); !slices.Equal(got, []string{"intro text"}) {
		t.Errorf("primary writes %q, want [intro text]", got)
	}

	if !m.selected() || !m.drawer.focused {
		t.Errorf("selected = %v, drawer has the keyboard = %v, want both", m.selected(), m.drawer.focused)
	}
}

func TestPageClickMovesCursorOnRelease(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		dy          int // rows the pointer moves down before the release
		wantPress   bool
		wantRelease bool
	}{
		{"click", 0, false, true},
		{"drag two rows", 2, false, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			m := newSelPage(t)
			m.p.view.cursor = 0
			m.p.view.clamp()

			want := pageLine(t, m.p, "item two")
			x, _, y := m.word(t, "item two", "item two")

			mdown(m.App, x, y, tea.MouseLeft, 0)

			if got := m.p.view.cursor == want; got != tt.wantPress {
				t.Errorf("cursor on the press = %d, at the row = %v, want %v", m.p.view.cursor, got, tt.wantPress)
			}

			if tt.dy > 0 {
				mmove(m.App, x, y+tt.dy, tea.MouseLeft)
			}

			release(m.App, x, y+tt.dy)

			if got := m.p.view.cursor == want; got != tt.wantRelease {
				t.Errorf("cursor after the release = %d, at the row = %v, want %v", m.p.view.cursor, got, tt.wantRelease)
			}
		})
	}
}

func TestPageClickOffTheText(t *testing.T) {
	t.Parallel()

	m := newSelPage(t)
	m.p.view.cursor = 2
	rect := mustRect(t, m.drawerRig)

	// Below the last line the press takes the keyboard and moves nothing.
	m.drawerRelease()

	y := rect.Y + 1 + len(m.p.view.layout().Lines) - m.p.view.top
	if y >= rect.Y+1+m.layout(t).diffH {
		t.Skip("the page fills the drawer")
	}

	clickRun(m.App, rect.X+6, y)

	if m.p.view.cursor != 2 || !m.drawer.focused {
		t.Errorf("cursor = %d, drawer has the keyboard = %v, want 2 and true", m.p.view.cursor, m.drawer.focused)
	}
}

func TestReviewCopy(t *testing.T) {
	t.Parallel()

	t.Run("selection", func(t *testing.T) {
		t.Parallel()

		m := newSelPage(t)
		m.dragIntro(t)
		m.key(keyRune('y'))

		if got := clipboardWrites(m.clip); !slices.Equal(got, []string{"intro text"}) {
			t.Errorf("clipboard writes %q", got)
		}

		if !m.selected() {
			t.Error("y cleared the selection")
		}
	})

	t.Run("cursor item", func(t *testing.T) {
		t.Parallel()

		m := newSelPage(t)
		m.p.view.cursor = pageLine(t, m.p, "item one")
		m.key(keyRune('y'))

		if got := clipboardWrites(m.clip); !slices.Equal(got, []string{"• item one"}) {
			t.Errorf("clipboard writes %q, want [• item one]", got)
		}
	})

	t.Run("palette hint", func(t *testing.T) {
		t.Parallel()

		m := newSelPage(t)

		if got := m.paletteHint("review.copy"); got != "drawer: y" {
			t.Errorf("hint = %q, want drawer: y", got)
		}
	})

	t.Run("remapped", func(t *testing.T) {
		t.Parallel()

		m := newSelPage(t, func(o *Options) { o.Keys = drawerKeys("Y", "review.copy", "y", "none") })
		m.p.view.cursor = pageLine(t, m.p, "item one")

		m.key(keyRune('y'))

		if got := clipboardWrites(m.clip); len(got) != 0 {
			t.Errorf("y wrote %q, want nothing", got)
		}

		m.key(keyRune('Y'))

		if got := clipboardWrites(m.clip); !slices.Equal(got, []string{"• item one"}) {
			t.Errorf("Y wrote %q, want [• item one]", got)
		}
	})

	t.Run("tree focused", func(t *testing.T) {
		t.Parallel()

		m := newPageRig(t, nil)
		f := fakeClip(m.App)

		m.keys(keyTab)

		if !m.drawer.tree {
			t.Fatal("tab did not give the tree the keyboard")
		}

		m.keys(keyRune('y'))

		if got := clipboardWrites(f); len(got) != 0 {
			t.Errorf("y with the tree focused wrote %q, want nothing", got)
		}
	})
}

func TestDrawerKeysClear(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		keys []tea.KeyPressMsg
		want bool // the selection is kept
	}{
		{"move", []tea.KeyPressMsg{keyRune('j')}, false},
		{"search then a letter", []tea.KeyPressMsg{keyRune('/'), keyRune('z')}, false},
		{"back", []tea.KeyPressMsg{keyEsc}, false},
		{"copy", []tea.KeyPressMsg{keyRune('y')}, true},
		{"full file, which a page drops", []tea.KeyPressMsg{keyRune('f')}, true},
		{"split, which a page drops", []tea.KeyPressMsg{keyRune('u')}, true},
		{"file next, which a page drops", []tea.KeyPressMsg{keyRune('}')}, true},
		{"dropped key", []tea.KeyPressMsg{keyRune('x')}, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			m := newSelPage(t)
			m.dragIntro(t)

			for _, k := range tt.keys {
				m.key(k)
			}

			if got := m.text != nil; got != tt.want {
				t.Errorf("selection kept = %v, want %v", got, tt.want)
			}
		})
	}

	t.Run("dropped key in the search prompt", func(t *testing.T) {
		t.Parallel()

		m := newSelPage(t)
		m.key(keyRune('/'))
		m.dragIntro(t)

		if m.v.search == nil || m.text == nil {
			t.Fatal("setup: the prompt is closed or nothing is selected")
		}

		m.key(tea.KeyPressMsg{Code: tea.KeyUp})

		if m.text == nil {
			t.Error("a key the prompt drops cleared the selection")
		}

		m.key(keyRune('z'))

		if m.text != nil {
			t.Error("a key the prompt takes kept the selection")
		}
	})

	t.Run("wheel", func(t *testing.T) {
		t.Parallel()

		m := newSelPage(t)
		m.dragIntro(t)

		rect := mustRect(t, m.drawerRig)
		m.Update(tea.MouseWheelMsg{X: rect.X + 6, Y: rect.Y + 5, Button: tea.MouseWheelDown})

		if m.text == nil {
			t.Error("the wheel cleared the selection")
		}
	})
}

func TestOneSelectionAcrossHosts(t *testing.T) {
	t.Parallel()

	t.Run("pane then page", func(t *testing.T) {
		t.Parallel()

		m := newSelPage(t)
		m.agent(t, m.dir, "")

		// The first pane is left of the drawer.
		drag(m.App, 5, 10, 20, 10, 0)

		if m.sel == nil {
			t.Fatal("setup: the pane drag selected nothing")
		}

		m.dragIntro(t)

		if m.sel != nil || m.text == nil {
			t.Errorf("pane selection = %v, text selection = %v, want only the page's", m.sel, m.text)
		}
	})

	t.Run("tab switch keeps it", func(t *testing.T) {
		t.Parallel()

		m := newSelPage(t)
		m.dragIntro(t)

		first := m.tabID()
		chord(m.App, 'c')

		if m.tabID() == first {
			t.Fatal("no second tab")
		}

		if m.text == nil || m.p.view.sel == nil {
			t.Error("a tab switch cleared the drawer's selection")
		}

		m.run(m.openReview(m.focus(), true))
		m.until(t, "the other tab's drawer to open", func() bool { return m.store.State().Tab().Review != nil })

		other := m.drawer.views[m.tabID()]
		if other == nil {
			t.Fatal("the other tab has no drawer view")
		}

		if other.page() != nil && other.page().view.sel != nil {
			t.Error("the other tab's drawer holds the selection")
		}

		openIn(t, m.App, core.SelectTab{Index: 0})

		if m.text == nil || m.p.view.sel == nil || m.view() != m.drawer.views[first] {
			t.Error("the selection is gone after switching back")
		}

		if !strings.Contains(m.drawn(t), "intro text") {
			t.Error("the page is not drawn")
		}
	})

	t.Run("rail click keeps it", func(t *testing.T) {
		t.Parallel()

		m := newSelPage(t)
		chord(m.App, 'a')

		rail := m.railRect()
		if rail.W == 0 {
			t.Fatal("no rail")
		}

		m.dragIntro(t)

		clickRun(m.App, rail.X+1, rail.Y+1)

		if m.text == nil {
			t.Error("a rail click cleared the selection")
		}
	})

	t.Run("hover keeps it", func(t *testing.T) {
		t.Parallel()

		m := newSelPage(t)
		m.agent(t, m.dir, "")
		m.drawn(t)
		m.dragIntro(t)

		before := m.focus()
		m.focusFollows = true
		hover(m.App, 10, 10)

		if m.focus() == before {
			t.Fatalf("setup: hovering a pane left the focus on %s", before)
		}

		if m.text == nil {
			t.Error("hovering a pane cleared the selection")
		}
	})

	t.Run("overlay", func(t *testing.T) {
		t.Parallel()

		m := newSelPage(t)
		m.dragIntro(t)

		path := filepath.Join(m.dir, "plan.md")
		write(t, path, selPlan)

		id := m.agent(t, m.dir, "")
		openIn(t, m.App, core.NotePlan{ID: id, Plan: core.Plan{Path: path, Root: m.dir, Kind: core.PlanFile, At: t0}})

		msgs := planMsgs(m.openPlan(id))
		if len(msgs) != 1 {
			t.Fatalf("the plan ask raised %d plan reads, want 1", len(msgs))
		}

		m.Update(msgs[0])

		rect, ok := planDocRect(m.width, m.height)
		if !ok {
			t.Fatal("no plan doc")
		}

		dragRun(m.App, rect.Min.X, rect.Min.Y+2, rect.Min.X+8, rect.Min.Y+2, 0)

		if m.p.view.sel != nil {
			t.Error("the page kept its selection under an overlay drag")
		}

		m.keys(keyEsc)
		m.keys(keyLeader, keyRune('y'))

		if got := clipboardWrites(m.clip); len(got) != 0 {
			t.Errorf("leader y wrote %q, want nothing", got)
		}
	})
}

func TestPageSelectionCleared(t *testing.T) {
	t.Parallel()

	t.Run("closing the drawer", func(t *testing.T) {
		t.Parallel()

		m := newSelPage(t)
		m.dragIntro(t)
		m.closeDrawer(false)

		if m.text != nil {
			t.Error("closing the drawer kept the selection")
		}
	})

	t.Run("a key", func(t *testing.T) {
		t.Parallel()

		m := newSelPage(t)
		m.dragIntro(t)
		m.key(keyRune('b'))

		if m.text != nil {
			t.Error("a command key kept the selection")
		}
	})

	reload := func(t *testing.T, from, to string) bool {
		t.Helper()

		m := newSelPage(t)
		m.dragIntro(t)

		write(t, filepath.Join(m.dir, "docs/a.md"), strings.Replace(pageDoc, from, to, 1))

		for _, msg := range flatten(m.batch(m.root, git.Batch{Paths: []string{"docs/a.md"}})) {
			if pm, ok := msg.(mdPageMsg); ok && pm.reload {
				m.flow(m.step(msg), nil)
			}
		}

		return m.text != nil
	}

	t.Run("the selected line changed", func(t *testing.T) {
		t.Parallel()

		if reload(t, "intro text", "intro words") {
			t.Error("a re-read with the selected line changed kept the selection")
		}
	})

	t.Run("another line changed", func(t *testing.T) {
		t.Parallel()

		if !reload(t, "last words", "final words") {
			t.Error("a re-read with only another line changed cleared the selection")
		}
	})
}

func TestDiffPartPieces(t *testing.T) {
	t.Parallel()

	thirty := "0123456789abcdefghijklmnopqrst"
	long := strings.Repeat("x", 1100)
	seventy := strings.Repeat("0123456789", 7)
	big := strings.Repeat("y", 1500)

	tests := []struct {
		name  string
		raw   string
		tagCR bool
		width int
		part  int
		want  []md.Piece
	}{
		{"plain", "abc", false, 10, 0, []md.Piece{{Col: 0, Cells: 3, Text: "abc"}}},
		{"no such part", "abc", false, 10, 1, nil},
		{"no width", "abc", false, 0, 0, nil},
		{"tab", "\tx", false, 10, 0, []md.Piece{{Col: 0, Cells: 4, Text: "\t", Whole: true}, {Col: 4, Cells: 1, Text: "x"}}},
		{"tab stop", "ab\tx", false, 10, 0, []md.Piece{{Col: 0, Cells: 2, Text: "ab"}, {Col: 2, Cells: 2, Text: "\t", Whole: true}, {Col: 4, Cells: 1, Text: "x"}}},
		{"hidden character", "a\u200bb", false, 20, 0, []md.Piece{
			{Col: 0, Cells: 1, Text: "a"}, {Col: 1, Cells: 8, Text: "⟨U+200B⟩", Whole: true}, {Col: 9, Cells: 1, Text: "b"},
		}},
		{"first of three", seventy, false, 30, 0, []md.Piece{{Col: 0, Cells: 30, Text: seventy[:30]}}},
		{"second of three", seventy, false, 30, 1, []md.Piece{{Col: 0, Cells: 30, Text: seventy[30:60]}}},
		{"third of three", seventy, false, 30, 2, []md.Piece{{Col: 0, Cells: 10, Text: seventy[60:]}}},
		{"fourth of three", seventy, false, 30, 3, nil},
		{"wide characters", "日本語", false, 10, 0, []md.Piece{{Col: 0, Cells: 6, Text: "日本語"}}},
		{"wide character moves down", "a日本", false, 4, 1, []md.Piece{{Col: 0, Cells: 2, Text: "本"}}},
		{"tab kept whole in the next part", "abcd\tx", false, 6, 1, []md.Piece{{Col: 0, Cells: 4, Text: "\t", Whole: true}, {Col: 4, Cells: 1, Text: "x"}}},
		{"text before a tab stays behind", "abcd\tx", false, 6, 0, []md.Piece{{Col: 0, Cells: 4, Text: "abcd"}}},
		{"ending tag", "ab", true, 20, 0, []md.Piece{{Col: 0, Cells: 2, Text: "ab"}, {Col: 2, Cells: 8, Text: "⟨U+000D⟩", Whole: true}}},
		{"ending tag on the last part only", thirty, true, 30, 0, []md.Piece{{Col: 0, Cells: 30, Text: thirty}}},
		{"ending tag alone", thirty, true, 30, 1, []md.Piece{{Col: 0, Cells: 8, Text: "⟨U+000D⟩", Whole: true}}},
		{"cut by the line budget", long, false, 2000, 0, []md.Piece{
			{Col: 0, Cells: lineBudget, Text: long[:lineBudget]}, {Col: lineBudget, Cells: 1, Text: long[lineBudget:], Whole: true},
		}},
		{"the cut ends the last part", big, false, 1000, 1, []md.Piece{
			{Col: 0, Cells: 24, Text: big[1000:1024]}, {Col: 24, Cells: 1, Text: big[1024:], Whole: true},
		}},
		{"the cut and the ending tag", big, true, 1100, 0, []md.Piece{
			{Col: 0, Cells: lineBudget, Text: big[:lineBudget]},
			{Col: lineBudget, Cells: 1, Text: big[lineBudget:] + endingTag, Whole: true},
			{Col: lineBudget + 1, Cells: 8, Whole: true},
		}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if got := diffPartPieces(tt.raw, tt.tagCR, tt.width, tt.part); !slices.Equal(got, tt.want) {
				t.Errorf("diffPartPieces = %+v, want %+v", got, tt.want)
			}
		})
	}
}

// diffSel is a drawer rig on realKeysRepo with the drawer open, a fake clipboard
// and the unified diff showing.
type diffSel struct {
	*drawerRig
	v    *reviewView
	clip *fakeRun
	dir  string
	root string
}

func newDiffSel(t *testing.T) *diffSel {
	t.Helper()

	dir := realKeysRepo(t)
	r := newKeysRig(t, dir)
	clip := fakeClip(r.App)
	v := r.opened(t)

	if r.layout(t).split {
		t.Fatal("setup: the diff is drawn side by side")
	}

	return &diffSel{drawerRig: r, v: v, clip: clip, dir: dir, root: rootOf(t, dir)}
}

// rowCell is the screen cell of diff-column cell col on row row of the diff.
func rowCell(t *testing.T, r *drawerRig, row, col int) (x, y int) {
	t.Helper()

	h, ok := r.rowsHost()
	if !ok {
		t.Fatal("the diff rows are not a text host")
	}

	y = h.rect.Min.Y + row - h.top
	if y < h.rect.Min.Y || y >= h.rect.Max.Y {
		t.Fatalf("row %d is not in view (top %d)", row, h.top)
	}

	return h.rect.Min.X + col, y
}

// code is the diff-column cell where row i's code starts.
func (d *diffSel) code(t *testing.T, i int) int {
	t.Helper()

	lay := d.layout(t)

	switch d.v.rows[i].kind {
	case rowLine:
		return markW + 2*lay.gw + diffview.SignWidth
	case rowFull, rowGhost:
		return markW + lay.gw + diffview.SignWidth
	}

	t.Fatalf("row %d is not a code row", i)

	return 0
}

// dragRows drags from cell c0 of row r0 to cell c1 of row r1 and releases.
func (d *diffSel) dragRows(t *testing.T, r0, c0, r1, c1 int) {
	t.Helper()

	x0, y0 := rowCell(t, d.drawerRig, r0, c0)
	x1, y1 := rowCell(t, d.drawerRig, r1, c1)
	dragRun(d.App, x0, y0, x1, y1, 0)
}

func TestDiffDragCopiesCodeOnly(t *testing.T) {
	t.Parallel()

	d := newDiffSel(t)
	del, add := rowWith(t, d.v, "line 3"), rowWith(t, d.v, "edited 3")

	d.dragRows(t, del, d.code(t, del), add, d.code(t, add)+len("edited 3")-1)

	if got := primaryWrites(d.clip); !slices.Equal(got, []string{"line 3\nedited 3"}) {
		t.Errorf("primary writes %q, want [line 3\\nedited 3]", got)
	}

	if d.text == nil || d.v.sel == nil || !d.drawer.focused {
		t.Errorf("selection = %v, view selection = %v, drawer has the keyboard = %v, want all set", d.text, d.v.sel, d.drawer.focused)
	}
}

func TestDiffHeadersCopyWhole(t *testing.T) {
	t.Parallel()

	d := newDiffSel(t)
	line := rowWith(t, d.v, "line 1")
	hunk := d.v.res.Files[0].Hunks[0]

	d.dragRows(t, 0, 6, line, d.code(t, line)+len("line 1")-1)

	want := "a.txt\n" + hunkHeader(hunk) + "\nline 1"
	if got := primaryWrites(d.clip); !slices.Equal(got, []string{want}) {
		t.Errorf("primary writes %q, want %q", got, want)
	}
}

func TestDiffRowsLeftOut(t *testing.T) {
	t.Parallel()

	d := newDiffSel(t)
	d.keys(keyRune('f'))
	d.until(t, "the full file", func() bool { return len(d.v.full) == 1 })

	ghost := slices.IndexFunc(d.v.rows, func(r reviewRow) bool { return r.kind == rowGhost })
	if ghost < 0 {
		t.Fatal("the full view has no ghost line")
	}

	d.v.cursor = ghost
	d.keys(keyEnter)

	fold := slices.IndexFunc(d.v.rows, func(r reviewRow) bool { return r.kind == rowFold })
	if fold < 1 || fold+1 >= len(d.v.rows) {
		t.Fatalf("no fold row in the middle of the rows (at %d)", fold)
	}

	d.v.top = 0
	before, after := d.v.rows[fold-1], d.v.rows[fold+1]

	d.dragRows(t, fold-1, d.code(t, fold-1), fold+1, d.code(t, fold+1)+len(after.text)-1)

	want := before.text + "\n" + after.text
	if got := primaryWrites(d.clip); !slices.Equal(got, []string{want}) {
		t.Errorf("primary writes %q, want %q", got, want)
	}
}

// cellBg is the background of screen cell x of a drawn line.
func cellBg(line string, x int) color.Color {
	buf := uv.NewScreenBuffer(ansi.StringWidth(line), 1)
	uv.NewStyledString(line).Draw(buf, buf.Bounds())

	return buf.CellAt(x, 0).Style.Bg
}

// bgIs reports whether a cell's background is want.
func bgIs(c, want color.Color) bool { return c != nil && sameColor(c, want) }

// Not parallel: the drawer highlights code, which takes the highlighter's time.
func TestDiffSelectionPaint(t *testing.T) {
	d := newDiffSel(t)
	add := rowWith(t, d.v, "edited 3")
	del := rowWith(t, d.v, "line 3")
	d.v.cursor = add

	d.dragRows(t, del, d.code(t, del), add, d.code(t, add)+len("edited 3")-1)

	st := d.store.State()
	rect := mustRect(t, d.drawerRig)
	lay := d.layout(t)
	lines := d.drawerLines(st, rect)
	sel := d.current().theme.UI.Selection
	off := 1
	top := d.v.viewTop(lay.diffH)

	if lay.treeW > 0 {
		off += lay.treeW + 1
	}

	at := func(row, col int) (string, int) { return lines[1+lay.headH+row-top], off + col }

	for _, c := range []struct {
		name     string
		row, col int
		want     bool
	}{
		{"first code cell of the removed line", del, d.code(t, del), true},
		{"last code cell of the added line", add, d.code(t, add) + len("edited 3") - 1, true},
		{"line number of the removed line", del, markW + 1, false},
		{"sign of the removed line", del, markW + 2*lay.gw + 1, false},
		{"line number of the added line", add, markW + lay.gw + 1, false},
		{"mark of the added line", add, 0, false},
	} {
		line, x := at(c.row, c.col)
		if got := bgIs(cellBg(line, x), sel); got != c.want {
			t.Errorf("%s: on the selection ground = %v, want %v", c.name, got, c.want)
		}
	}

	line, x := at(add, 0)
	if got := ansi.Strip(line); !strings.Contains(got, markGlyph) {
		t.Errorf("the cursor's mark is gone: %q", got)
	}

	if bgIs(cellBg(line, x), sel) {
		t.Error("the cursor's mark cell is on the selection ground")
	}
}

func TestDiffDragReleasedOverPane(t *testing.T) {
	t.Parallel()

	d := newDiffSel(t)
	row := rowWith(t, d.v, "line 3")
	x0, y := rowCell(t, d.drawerRig, row, d.code(t, row)+3)

	px := 10
	if d.hit(px, y).area != hitContent || px >= mustRect(t, d.drawerRig).X {
		t.Fatalf("setup: (%d, %d) is not a pane's content left of the drawer", px, y)
	}

	dragRun(d.App, x0, y, px, y, 0)

	if d.v.sel == nil || d.v.sel.head.col != 0 {
		t.Errorf("selection = %+v, want it ending at the column's left edge", d.v.sel)
	}

	if got := primaryWrites(d.clip); !slices.Equal(got, []string{"line"}) {
		t.Errorf("primary writes %q, want [line]", got)
	}

	if got := d.sentSoFar(t, "p1"); got != "" {
		t.Errorf("the pane's program was sent %q, want nothing", got)
	}
}

func TestDiffPressOutsideText(t *testing.T) {
	t.Parallel()

	d := newDiffSel(t)
	d.showTree(t)

	lay := d.layout(t)
	if lay.treeW == 0 || !lay.hints {
		t.Fatalf("setup: tree width %d, hints %v", lay.treeW, lay.hints)
	}

	z := slices.IndexFunc(d.v.derived().tree, func(it treeItem) bool { return !it.dir && d.v.res.Files[it.file].Path == "z.txt" })
	treeX, treeY := d.drawerCell(t, 2, 1+3+z)
	hintX, hintY := d.drawerCell(t, 10, 1+lay.bodyH)
	row := rowWith(t, d.v, "line 3")
	rx, ry := rowCell(t, d.drawerRig, row, d.code(t, row))
	dragRun(d.App, rx, ry, rx+4, ry, 0)

	if d.text == nil {
		t.Fatal("setup: no selection")
	}

	cursor := d.v.cursor

	dragRun(d.App, hintX, hintY, hintX+5, hintY, 0)

	if d.v.cursor != cursor || !d.drawer.focused || d.mouse.text != nil {
		t.Errorf("a drag on the hint row: cursor %d -> %d, drawer has the keyboard = %v, held press = %v", cursor, d.v.cursor, d.drawer.focused, d.mouse.text != nil)
	}

	clickRun(d.App, treeX, treeY)

	if fileAtCursor(d.v) != "z.txt" || d.mouse.text != nil {
		t.Errorf("a click on z.txt in the tree: cursor on %s, held press = %v, want z.txt's header and none", fileAtCursor(d.v), d.mouse.text != nil)
	}

	if got := primaryWrites(d.clip); len(got) != 1 {
		t.Errorf("primary writes %q, want only the first drag's", got)
	}
}

func TestStickyHeaderSelects(t *testing.T) {
	t.Parallel()

	d := newDiffSel(t)
	d.v.top, d.v.cursor = 8, 11

	h, ok := d.rowsHost()
	if !ok || h.sticky != 0 {
		t.Fatalf("setup: host ok = %v, sticky row = %d, want the first file's header on the first row", ok, h.sticky)
	}

	x, y := h.rect.Min.X+5, h.rect.Min.Y

	clickRun(d.App, x, y)
	clickRun(d.App, x, y)

	if got := primaryWrites(d.clip); !slices.Equal(got, []string{"a.txt"}) {
		t.Errorf("primary writes %q, want [a.txt]", got)
	}
}

func TestDiffSelectionReload(t *testing.T) {
	t.Parallel()

	edits := func(n ...int) func(*testing.T, string) {
		return func(t *testing.T, dir string) { write(t, filepath.Join(dir, "a.txt"), editedLines(40, n...)) }
	}

	tests := []struct {
		name     string
		from, to string // the rows the selection runs over
		edit     func(*testing.T, string)
		hidden   bool // the drawer's tab is hidden while the file changes
		held     bool // the button is held while the file changes
		reloads  bool
		want     bool // the selection is kept
	}{
		{"rewritten with the same content", "line 28", "line 29", edits(3, 30), false, false, false, true},
		{"selected line edited", "line 28", "line 29", edits(3, 29), false, false, true, false},
		{"a line above edited", "line 28", "line 29", edits(4, 30), false, false, true, true},
		{"edited while the tab is hidden", "line 28", "line 29", edits(3, 29), true, false, true, false},
		{"untouched while the tab is hidden", "line 28", "line 29", edits(4, 30), true, false, true, true},
		{"edited during a held drag", "line 28", "line 29", edits(3, 29), false, true, true, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			d := newDiffSel(t)
			v := d.v
			from, to := rowWith(t, v, tt.from), rowWith(t, v, tt.to)
			x0, y0 := rowCell(t, d.drawerRig, from, d.code(t, from))
			x1, y1 := rowCell(t, d.drawerRig, to, d.code(t, to)+len(tt.to)-1)

			if tt.held {
				mdown(d.App, x0, y0, tea.MouseLeft, 0)
				mmove(d.App, x1, y1, tea.MouseLeft)
			} else {
				dragRun(d.App, x0, y0, x1, y1, 0)
			}

			if d.text == nil || v.sel == nil {
				t.Fatal("setup: no selection")
			}

			first := d.tabID()
			gen := v.gen

			if tt.hidden {
				d.watching(t, "p1", d.root)
				openIn(t, d.App, core.NewTab{Kind: core.KindShell, Cwd: t.TempDir()})
				d.settle()
			}

			tt.edit(t, d.dir)

			if tt.hidden {
				d.batch(d.root, git.Batch{Paths: []string{"a.txt"}})
				d.until(t, "the watcher to report a.txt", d.reportedAll(filepath.Join(d.root, "a.txt")))

				openIn(t, d.App, core.SelectTab{Index: 0})

				if d.tabID() != first {
					t.Fatalf("active tab = %s, want %s", d.tabID(), first)
				}

				d.settle()
			} else {
				d.run(d.batch(d.root, git.Batch{Paths: []string{"a.txt"}}))
			}

			if tt.reloads {
				d.until(t, "the drawer to reload", func() bool { return v.gen > gen && !v.loading && v.queued == nil })
			} else {
				d.settle()
			}

			if tt.held {
				mmove(d.App, x1, y1, tea.MouseLeft)
				release(d.App, x1, y1)
			}

			if got := d.text != nil && v.sel != nil; got != tt.want {
				t.Errorf("selection kept = %v, want %v", got, tt.want)
			}

			if got := d.text == nil; got == (v.sel != nil) {
				t.Errorf("the app's selection (%v) and the view's (%v) disagree", d.text, v.sel)
			}

			if tt.held {
				if got := primaryWrites(d.clip); len(got) != 0 {
					t.Errorf("the release wrote %q, want nothing", got)
				}
			}
		})
	}
}

func TestRenderedRowsCopy(t *testing.T) {
	t.Parallel()

	para := strings.TrimSpace(strings.Repeat("alpha beta gamma delta ", 12))
	m := newMDRigOf(t, planWith(map[int]string{3: para}), planWith(map[int]string{3: para + " omega", 8: "- 2nd step"}))
	clip := fakeClip(m.App)

	m.onFile(t, planPath)
	m.press(keyRune('m'))

	var rows []int

	for i, r := range m.v.rows {
		if r.kind == rowMD && (strings.Contains(r.text, "alpha") || strings.Contains(r.text, "omega")) {
			rows = append(rows, i)
		}
	}

	if len(rows) < 2 {
		t.Fatalf("the paragraph is on %d rendered rows, want it wrapped", len(rows))
	}

	m.v.cursor = rows[0]
	m.v.top = max(rows[0]-1, 0)

	last := rows[len(rows)-1]
	x0, y0 := rowCell(t, m.drawerRig, rows[0], 2)
	x1, y1 := rowCell(t, m.drawerRig, last, m.layout(t).diffW-1)

	dragRun(m.App, x0, y0, x1, y1, 0)

	want := para + " omega"
	if got := primaryWrites(clip); !slices.Equal(got, []string{want}) {
		t.Errorf("primary writes %q, want the paragraph as one line %q", got, want)
	}

	m.clearSelections()
	m.v.cursor = rows[1]
	m.drawerTake()
	run(m.App, press(m.App, keyRune('y')))

	if got := clipboardWrites(clip); !slices.Equal(got, []string{want}) {
		t.Errorf("y on the second row wrote %q, want the whole paragraph %q", got, want)
	}
}

func TestDiffCopyKey(t *testing.T) {
	t.Parallel()

	d := newDiffSel(t)

	tests := []struct {
		name string
		row  func() int
		want []string
	}{
		{"a diff line", func() int { return rowWith(t, d.v, "edited 3") }, []string{"edited 3"}},
		{"a file header", func() int { return 0 }, []string{"a.txt"}},
		{"a hunk header", func() int { return 1 }, []string{hunkHeader(d.v.res.Files[0].Hunks[0])}},
	}

	for _, tt := range tests {
		d.clip.calls = nil
		d.v.cursor = tt.row()
		d.drawerTake()
		run(d.App, press(d.App, keyRune('y')))

		if got := clipboardWrites(d.clip); !slices.Equal(got, tt.want) {
			t.Errorf("%s: y wrote %q, want %q", tt.name, got, tt.want)
		}
	}

	d.clip.calls = nil
	d.v.rows = append(d.v.rows, reviewRow{kind: rowInfo, file: 0, hunk: -1, line: -1, other: -1, text: "note"})
	d.v.cursor = len(d.v.rows) - 1
	run(d.App, press(d.App, keyRune('y')))

	if got := clipboardWrites(d.clip); len(got) != 0 || d.notice != "" {
		t.Errorf("y on an info row wrote %q with the notice %q, want nothing", got, d.notice)
	}
}

func TestDiffPiecesCopyEdges(t *testing.T) {
	t.Parallel()

	wide := diffPartPieces("日本", false, 10, 0)
	tagged := diffPartPieces("a\u200bb", false, 20, 0)

	tests := []struct {
		name      string
		pieces    []md.Piece
		from, to  int
		want      string
		wantPaint [][2]int
	}{
		{"ends on a wide right half", wide, 0, 1, "日", [][2]int{{0, 1}}},
		{"starts on a wide right half", wide, 1, 1, "日", [][2]int{{0, 1}}},
		{"spans two wide characters", wide, 1, 2, "日本", [][2]int{{0, 3}}},
		{"ends inside a tag", tagged, 0, 4, "a⟨U+200B⟩", [][2]int{{0, 8}}},
		{"starts inside a tag", tagged, 3, 9, "⟨U+200B⟩b", [][2]int{{1, 9}}},
		{"only inside a tag", tagged, 3, 3, "⟨U+200B⟩", [][2]int{{1, 8}}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			lt := md.LineText{Pieces: tt.pieces}

			if got := md.CopyLines([]md.LineText{lt}, []md.CellRange{{From: tt.from, To: tt.to}}, false); got != tt.want {
				t.Errorf("copy = %q, want %q", got, tt.want)
			}

			if got := textPaint(lt, tt.from, tt.to); !slices.Equal(got, tt.wantPaint) {
				t.Errorf("paint = %v, want %v", got, tt.wantPaint)
			}
		})
	}
}

// newSplitSel is newDiffSel with the diff side by side.
func newSplitSel(t *testing.T, dir string) *diffSel {
	t.Helper()

	r := newKeysRig(t, dir)
	clip := fakeClip(r.App)
	v := r.opened(t)

	r.keys(keyRune('u'))
	r.settle()

	if !v.rowsSplit || !r.layout(t).split {
		t.Fatal("setup: the diff is not side by side")
	}

	return &diffSel{drawerRig: r, v: v, clip: clip, dir: dir, root: rootOf(t, dir)}
}

// splitCols are the diff-column cells of a side by side row: where each side's
// code starts, and the "│".
func (d *diffSel) splitCols(t *testing.T) (leftCode, sep, rightCode int) {
	t.Helper()

	lay := d.layout(t)
	leftCode = markW + lay.gw + diffview.SignWidth
	sep = leftCode + lay.splitCode()
	rightCode = sep + 1 + lay.gw + diffview.SignWidth

	return leftCode, sep, rightCode
}

// insertedRepo is a file with two lines added between two kept ones, so the
// two side by side rows of the added lines have a blank left side.
func insertedRepo(t *testing.T) string {
	t.Helper()

	dir := newRepoDir(t)

	write(t, filepath.Join(dir, "f.txt"), "alpha\nbravo\ncharlie\ndelta\n")
	commitEverything(t, dir, "init")
	write(t, filepath.Join(dir, "f.txt"), "alpha\nbravo\nnew one\nnew two\ncharlie\ndelta\n")

	return dir
}

// rightHalfOffset is how far into the right side's code a drag ends to be "in
// the right half"; it is past the left side's columns, so the drag clamps.
const rightHalfOffset = 3

// removedRepo is a file with two lines removed between two kept ones, so the
// two side by side rows of the removed lines have a blank right side.
func removedRepo(t *testing.T) string {
	t.Helper()

	dir := newRepoDir(t)

	write(t, filepath.Join(dir, "f.txt"), "alpha\nbravo\nold one\nold two\ncharlie\ndelta\n")
	commitEverything(t, dir, "init")
	write(t, filepath.Join(dir, "f.txt"), "alpha\nbravo\ncharlie\ndelta\n")

	return dir
}

func TestSplitCopyKey(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		repo func(*testing.T) string
		row  string
		want string
	}{
		{"context row", insertedRepo, "bravo", "bravo"},
		{"added-only row", insertedRepo, "new one", "new one"},
		{"removed-only row", removedRepo, "old one", "old one"},
	}

	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			d := newSplitSel(t, tt.repo(t))
			d.v.cursor = rowWith(t, d.v, tt.row)
			d.drawerTake()
			run(d.App, press(d.App, keyRune('y')))

			if got := clipboardWrites(d.clip); !slices.Equal(got, []string{tt.want}) {
				t.Errorf("y wrote %q, want %q", got, tt.want)
			}
		})
	}
}

func TestSplitSelectionSide(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		repo func(*testing.T) string
		// The rows are named by their text; the columns come from where the
		// layout puts the left code, the divider and the right code.
		row0, row1 string
		col0, col1 func(left, sep, right int) int
		want       string
		wantSide   int
	}{
		{
			name: "left code to the right half of the next row",
			repo: realKeysRepo, row0: "line 3", row1: "line 4",
			col0: func(l, _, _ int) int { return l },
			col1: func(_, _, r int) int { return r + rightHalfOffset },
			want: "line 3\nline 4", wantSide: 0,
		},
		{
			name: "right code",
			repo: realKeysRepo, row0: "line 3", row1: "line 3",
			col0: func(_, _, r int) int { return r },
			col1: func(_, _, r int) int { return r + len("edited 3") - 1 },
			want: "edited 3", wantSide: 1,
		},
		{
			name: "press on the left number column",
			repo: realKeysRepo, row0: "line 3", row1: "line 3",
			col0: func(l, _, _ int) int { return markW + 1 },
			col1: func(l, _, _ int) int { return l + len("line 3") - 1 },
			want: "line 3", wantSide: 0,
		},
		{
			name: "press on the right number column",
			repo: realKeysRepo, row0: "line 3", row1: "line 3",
			col0: func(_, _, r int) int { return r - 2 },
			col1: func(_, _, r int) int { return r + len("edited 3") - 1 },
			want: "edited 3", wantSide: 1,
		},
		{
			name: "press on the divider",
			repo: realKeysRepo, row0: "line 3", row1: "line 4",
			col0: func(_, sep, _ int) int { return sep },
			col1: func(l, _, _ int) int { return l + len("line 4") - 1 },
			want: "\nline 4", wantSide: 0,
		},
		{
			name: "rows where the left side is blank add no line",
			repo: insertedRepo, row0: "alpha", row1: "delta",
			col0: func(l, _, _ int) int { return l },
			col1: func(l, _, _ int) int { return l + len("delta") - 1 },
			want: "alpha\nbravo\ncharlie\ndelta", wantSide: 0,
		},
		{
			name: "the right side takes the added lines",
			repo: insertedRepo, row0: "alpha", row1: "delta",
			col0: func(_, _, r int) int { return r },
			col1: func(_, _, r int) int { return r + len("delta") - 1 },
			want: "alpha\nbravo\nnew one\nnew two\ncharlie\ndelta", wantSide: 1,
		},
	}

	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			d := newSplitSel(t, tt.repo(t))
			l, sep, r := d.splitCols(t)
			r0, r1 := rowWith(t, d.v, tt.row0), rowWith(t, d.v, tt.row1)

			d.dragRows(t, r0, tt.col0(l, sep, r), r1, tt.col1(l, sep, r))

			if got := primaryWrites(d.clip); !slices.Equal(got, []string{tt.want}) {
				t.Errorf("primary writes %q, want %q", got, tt.want)
			}

			if d.v.selSide != tt.wantSide {
				t.Errorf("selection side = %d, want %d", d.v.selSide, tt.wantSide)
			}
		})
	}
}

// Not parallel: the drawer highlights code, which takes the highlighter's time.
func TestSplitSelectionPaint(t *testing.T) {
	for _, side := range []int{0, 1} {
		d := newSplitSel(t, realKeysRepo(t))
		pair := rowWith(t, d.v, "line 3")
		l, sep, r := d.splitCols(t)
		lay := d.layout(t)

		from, to := l, sep-1
		if side == 1 {
			from, to = r, r+len("edited 3")-1
		}

		d.dragRows(t, pair, from, pair, to)

		lines := d.drawerLines(d.store.State(), mustRect(t, d.drawerRig))
		sel := d.current().theme.UI.Selection
		off := 1
		top := d.v.viewTop(lay.diffH)

		if lay.treeW > 0 {
			off += lay.treeW + 1
		}

		line := lines[1+lay.headH+pair-top]
		on := func(col int) bool { return bgIs(cellBg(line, off+col), sel) }

		// The cells of each side's code that hold text, and the cells that never paint.
		left, right := on(l), on(r)
		if left != (side == 0) || right != (side == 1) {
			t.Errorf("side %d: first code cell painted left = %v, right = %v", side, left, right)
		}

		for name, col := range map[string]int{"left number": markW + 1, "left sign": l - 1, "divider": sep, "right number": sep + 2, "right sign": r - 1} {
			if on(col) {
				t.Errorf("side %d: %s cell is on the selection ground", side, name)
			}
		}
	}
}

// textSelHead is the head of the held text selection.
func textSelHead(t *testing.T, a *App) point {
	t.Helper()

	switch {
	case a.text == nil:
		t.Fatal("no selection")
	case a.text.view != nil && a.text.view.sel != nil:
		return a.text.view.sel.head
	case a.text.rows != nil && a.text.rows.sel != nil:
		return a.text.rows.sel.head
	}

	t.Fatal("the selection holder has no selection")

	return point{}
}

// dragTick sends msg to a and returns the command it answers with.
func dragTick(a *App, msg tea.Msg) tea.Cmd {
	_, cmd := a.Update(msg)

	return cmd
}

// edgeCase is a text host with a way to scroll it.
type edgeCase struct {
	a      *App
	host   func() textHost
	scroll func(n int)
}

func edgeOverlay(t *testing.T) edgeCase {
	t.Helper()

	r := newPlanRig(t, 100, 40)
	fakeClip(r.a)
	r.filePlan(t, "plan.md", numberedMD(200))
	r.open(t)

	return edgeCase{
		a: r.a,
		host: func() textHost {
			h, ok := r.a.planHost()
			if !ok {
				t.Fatal("the plan overlay is not a text host")
			}

			return h
		},
		scroll: func(n int) { r.a.plan.shown().scroll(n) },
	}
}

func edgeDiff(t *testing.T) edgeCase {
	t.Helper()

	r := newKeysRig(t, realKeysRepo(t))
	fakeClip(r.App)
	v := r.opened(t)

	c := edgeCase{
		a: r.App,
		host: func() textHost {
			h, ok := r.rowsHost()
			if !ok {
				t.Fatal("the diff rows are not a text host")
			}

			return h
		},
		scroll: func(n int) { r.scrollRows(v, n) },
	}

	if h := c.host(); len(v.rows) <= h.rect.Dy()+12 {
		t.Fatalf("setup: %d rows fit the %d-row view", len(v.rows), h.rect.Dy())
	}

	return c
}

func edgePage(t *testing.T) edgeCase {
	t.Helper()

	m := newPageRig(t, map[string]string{"docs/long.md": numberedMD(300)})
	fakeClip(m.App)
	p := m.openPage(t, "docs/long.md")
	m.drawerTake()
	m.drawn(t)

	return edgeCase{
		a: m.App,
		host: func() textHost {
			h, ok := m.pageHost()
			if !ok {
				t.Fatal("the page is not a text host")
			}

			return h
		},
		scroll: p.scroll,
	}
}

// numberedMD is n list lines, one row each.
func numberedMD(n int) string {
	var b strings.Builder

	for i := range n {
		fmt.Fprintf(&b, "- line %d\n", i)
	}

	return b.String()
}

func TestEdgeScroll(t *testing.T) {
	t.Parallel()

	hosts := []struct {
		name string
		make func(*testing.T) edgeCase
	}{
		{"plan overlay", edgeOverlay},
		{"diff rows", edgeDiff},
		{"drawer page", edgePage},
	}

	for _, hs := range hosts {
		for _, dir := range []int{1, -1} {
			name := hs.name + " down"
			if dir < 0 {
				name = hs.name + " up"
			}

			t.Run(name, func(t *testing.T) {
				t.Parallel()

				c := hs.make(t)
				edgeScrollOne(t, c, dir)
			})
		}
	}
}

// edgeScrollOne drags past the text's bottom (dir 1) or top (dir -1) and checks
// the tick scrolls a row, the head follows to the edge row, and the tick stops.
func edgeScrollOne(t *testing.T, c edgeCase, dir int) {
	t.Helper()

	a := c.a

	if dir < 0 {
		c.scroll(10)
	}

	h0 := c.host()
	x := h0.rect.Min.X + 5
	pressY, outY, overY := h0.rect.Min.Y+1, h0.rect.Max.Y, h0.rect.Min.Y+2

	if dir < 0 {
		pressY, outY, overY = h0.rect.Max.Y-2, h0.rect.Min.Y-1, h0.rect.Max.Y-3
	}

	edgeRow := func(h textHost) int {
		if dir < 0 {
			return h.top
		}

		return h.top + h.rect.Dy() - 1
	}
	motion := func(y int) tea.Cmd {
		return dragTick(a, tea.MouseMotionMsg{X: x, Y: y, Button: tea.MouseLeft})
	}

	mdown(a, h0.rect.Min.X+2, pressY, tea.MouseLeft, 0)

	if motion(overY) != nil {
		t.Fatal("a motion over the text returned a command")
	}

	if motion(outY) == nil {
		t.Fatal("a drag held past the text returned no command")
	}

	if motion(outY+dir) != nil {
		t.Error("a motion while a tick runs returned a command")
	}

	seq := a.mouse.text.seq
	top := c.host().top

	if got, want := textSelHead(t, a).row, edgeRow(c.host()); got != want {
		t.Errorf("head row after the drag = %d, want the edge row %d", got, want)
	}

	if dragTick(a, dragScrollMsg{seq: seq}) == nil {
		t.Error("a tick returned no next tick")
	}

	h := c.host()
	if h.top != top+dir {
		t.Errorf("top = %d, want %d: one row", h.top, top+dir)
	}

	if got, want := textSelHead(t, a).row, edgeRow(h); got != want {
		t.Errorf("head row = %d, want the edge row %d", got, want)
	}

	if motion(overY) != nil {
		t.Error("a motion back over the text returned a command")
	}

	head := textSelHead(t, a)

	if dragTick(a, dragScrollMsg{seq: seq}) != nil || c.host().top != top+dir || textSelHead(t, a) != head {
		t.Error("a tick after the pointer came back changed something")
	}

	if motion(outY) == nil {
		t.Fatal("outside again returned no command")
	}

	stale := dragScrollMsg{seq: a.mouse.text.seq}
	if stale.seq == seq {
		t.Error("a new tick reused the old seq")
	}

	mup(a, x, outY, tea.MouseLeft)

	top = c.host().top

	if dragTick(a, stale) != nil || c.host().top != top {
		t.Error("a tick after the release changed something")
	}
}

func TestEdgeScrollStopsAtTop(t *testing.T) {
	t.Parallel()

	for _, tt := range []struct {
		name string
		make func(*testing.T) edgeCase
	}{
		{"plan overlay", edgeOverlay},
		{"diff rows", edgeDiff},
		{"drawer page", edgePage},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			c := tt.make(t)
			a, h := c.a, c.host()

			if h.top != 0 {
				t.Fatalf("setup: top = %d", h.top)
			}

			mdown(a, h.rect.Min.X+2, h.rect.Max.Y-2, tea.MouseLeft, 0)

			if dragTick(a, tea.MouseMotionMsg{X: h.rect.Min.X + 5, Y: h.rect.Min.Y - 1, Button: tea.MouseLeft}) == nil {
				t.Fatal("a drag held above the text returned no command")
			}

			head := textSelHead(t, a)

			if dragTick(a, dragScrollMsg{seq: a.mouse.text.seq}) != nil {
				t.Error("a tick at the top returned a next tick")
			}

			if c.host().top != 0 || textSelHead(t, a) != head {
				t.Error("a tick at the top changed something")
			}
		})
	}
}

func TestEdgeScrollUpPastStickyHeader(t *testing.T) {
	t.Parallel()

	d := newDiffSel(t)
	d.v.top, d.v.cursor = 8, 11

	h, ok := d.rowsHost()
	if !ok || h.sticky < 0 {
		t.Fatalf("setup: host ok = %v, sticky = %d, want a pinned header", ok, h.sticky)
	}

	x := h.rect.Min.X + 5

	mdown(d.App, x, h.rect.Max.Y-2, tea.MouseLeft, 0)

	if dragTick(d.App, tea.MouseMotionMsg{X: x, Y: h.rect.Min.Y - 1, Button: tea.MouseLeft}) == nil {
		t.Fatal("a drag above the text returned no command")
	}

	if got := textSelHead(t, d.App).row; got != h.top {
		t.Errorf("head row after the drag = %d, want the first text row %d, not the header %d", got, h.top, h.sticky)
	}

	dragTick(d.App, dragScrollMsg{seq: d.mouse.text.seq})

	h, _ = d.rowsHost()

	if got := textSelHead(t, d.App).row; got != h.top || h.top != 7 {
		t.Errorf("head row = %d, top = %d, want the head on the new first row 7", got, h.top)
	}
}

func TestDiffDragThroughReload(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		path string
		body string
	}{
		{"another file reloads while the drag is held", "z.txt", editedLines(40, 3, 30, 12)},
		{"rows above shift while the drag is held", "a.txt", editedLines(40, 3, 5, 30)},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			d := newDiffSel(t)
			v := d.v
			from := rowWith(t, v, "line 28")
			x0, y0 := rowCell(t, d.drawerRig, from, d.code(t, from))
			xm, ym := rowCell(t, d.drawerRig, from, d.code(t, from)+len("line 28")-1)

			mdown(d.App, x0, y0, tea.MouseLeft, 0)
			mmove(d.App, xm, ym, tea.MouseLeft)

			gen := v.gen

			write(t, filepath.Join(d.dir, tt.path), tt.body)
			d.run(d.batch(d.root, git.Batch{Paths: []string{tt.path}}))
			d.until(t, "the drawer to reload", func() bool { return v.gen > gen && !v.loading && v.queued == nil })

			if tt.path == "a.txt" && rowWith(t, v, "line 28") == from {
				t.Fatal("setup: the rows did not shift")
			}

			if d.text == nil || v.sel == nil {
				t.Fatal("the selection went on a reload that left its text alone")
			}

			to := rowWith(t, v, "line 29")
			x1, y1 := rowCell(t, d.drawerRig, to, d.code(t, to)+len("line 29")-1)

			mmove(d.App, x1, y1, tea.MouseLeft)
			release(d.App, x1, y1)

			if got := primaryWrites(d.clip); !slices.Equal(got, []string{"line 28\nline 29"}) {
				t.Errorf("primary writes %q, want the lines from line 28 to line 29", got)
			}
		})
	}
}

func TestDiffDragReadsEachRowOnce(t *testing.T) {
	t.Parallel()

	d := newDiffSel(t)
	v := d.v
	h, _ := d.rowsHost()
	x := h.rect.Min.X + 12
	reads := v.rowTexts

	mdown(d.App, x, h.rect.Min.Y+1, tea.MouseLeft, 0)

	for y := h.rect.Min.Y + 2; y <= h.rect.Max.Y; y++ {
		mmove(d.App, x, y, tea.MouseLeft)
	}

	for range 8 {
		if dragTick(d.App, dragScrollMsg{seq: d.mouse.text.seq}) == nil {
			t.Fatal("the edge scroll stopped")
		}
	}

	release(d.App, x, h.rect.Max.Y)

	if d.text == nil || v.sel == nil {
		t.Fatal("the drag left no selection")
	}

	reads = v.rowTexts - reads
	first, last := v.sel.rowBounds()

	if rows := last - first + 1; reads > rows {
		t.Errorf("a drag over %d rows read rows' copy text %d times, want each row once", rows, reads)
	}

	if got, want := primaryWrites(d.clip), freshCopy(v.selGrid(), *v.sel); !slices.Equal(got, []string{want}) {
		t.Errorf("primary writes %q, want %q", got, want)
	}
}

// rewritten is res with the text of the lines that match changed.
func rewritten(res git.Result, match func(text string) bool) git.Result {
	res.Files = slices.Clone(res.Files)

	for i := range res.Files {
		f := &res.Files[i]
		f.Hunks = slices.Clone(f.Hunks)

		for j := range f.Hunks {
			h := &f.Hunks[j]
			h.Lines = slices.Clone(h.Lines)

			for k := range h.Lines {
				if match(h.Lines[k].Text) {
					h.Lines[k].Text = "re " + h.Lines[k].Text
				}
			}
		}
	}

	return res
}

func TestDiffDragCopiesWhatItCovers(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		alt  bool
		path []int // the rows the pointer moves to, as offsets from the press's row
		// rebuild is how many moves pass before the rows are laid out again with
		// new text and no reload compare in between; -1 for none.
		rebuild int
	}{
		{"down", false, []int{1, 2, 3, 4, 5}, -1},
		{"down and back", false, []int{1, 2, 3, 4, 5, 3, 1}, -1},
		{"down then above the press", false, []int{1, 2, 3, -1, -3, -4}, -1},
		{"up then below the press", false, []int{-1, -2, -4, 1, 2}, -1},
		{"jumps past the held rows", false, []int{2, -5, 5}, -1},
		{"block, down and back", true, []int{1, 3, 5, 2}, -1},
		{"rows laid out again between two moves", false, []int{1, 2, 3, 4, 5}, 3},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			d := newDiffSel(t)
			v := d.v
			h, _ := d.rowsHost()

			if h.rect.Dy() < 12 {
				t.Fatalf("setup: the view has %d rows, want 12 or more", h.rect.Dy())
			}

			x, y := h.rect.Min.X+12, h.rect.Min.Y+h.rect.Dy()/2

			var mod tea.KeyMod
			if tt.alt {
				mod = tea.ModAlt
			}

			mdown(d.App, x, y, tea.MouseLeft, mod)

			for i, dy := range tt.path {
				if i == tt.rebuild {
					v.rebuildFor(rewritten(v.res, func(string) bool { return true }), v.rowsSplit)
				}

				mmove(d.App, x+4, y+dy, tea.MouseLeft)
			}

			release(d.App, x+4, y+tt.path[len(tt.path)-1])

			if d.text == nil || v.sel == nil {
				t.Fatal("the drag left no selection")
			}

			want := freshCopy(v.selGrid(), *v.sel)

			if tt.rebuild >= 0 && !strings.Contains(want, "re line") {
				t.Fatalf("setup: the selection %q holds no rewritten line", want)
			}

			if got := primaryWrites(d.clip); !slices.Equal(got, []string{want}) {
				t.Errorf("primary writes %q, want %q", got, want)
			}
		})
	}
}

func TestDiffCopyBeforeReloadCompare(t *testing.T) {
	t.Parallel()

	d := newDiffSel(t)
	v := d.v
	from, to := rowWith(t, v, "line 27"), rowWith(t, v, "line 29")
	x0, y0 := rowCell(t, d.drawerRig, from, d.code(t, from))
	x1, y1 := rowCell(t, d.drawerRig, to, d.code(t, to)+len("line 29")-1)

	dragRun(d.App, x0, y0, x1, y1, 0)

	if d.text == nil || v.sel == nil {
		t.Fatal("setup: no selection")
	}

	// Rows laid out again after the update's compare, as drawerAfterUpdate does;
	// the selection's ends keep their lines.
	v.rebuildFor(rewritten(v.res, func(s string) bool { return s == "line 28" }), v.rowsSplit)
	run(d.App, press(d.App, keyRune('y')))

	if got := clipboardWrites(d.clip); !slices.Equal(got, []string{"line 27\nre line 28\nline 29"}) {
		t.Errorf("y wrote %q, want the rows' new text", got)
	}

	if d.text != nil || v.sel != nil {
		t.Error("the selection outlived a reload that changed its text, because the copy read it first")
	}
}

func TestDiffSelectionUnderPage(t *testing.T) {
	t.Parallel()

	m := newPageRig(t, nil)
	clip := fakeClip(m.App)
	v := m.v

	row := rowWith(t, v, "edited 3")
	lay := m.layout(t)
	c := markW + 2*lay.gw + diffview.SignWidth
	x0, y0 := rowCell(t, m.drawerRig, row, c)
	x1, y1 := rowCell(t, m.drawerRig, row, c+len("edited 3")-1)
	dragRun(m.App, x0, y0, x1, y1, 0)

	if m.text == nil {
		t.Fatal("setup: no selection")
	}

	// A page opens over the diff without a drawer key.
	m.flow(m.openMarkdownPage(m.tabID(), filepath.Join(m.root, "README.md")), nil)

	if v.page() == nil {
		t.Fatal("no page opened")
	}

	if m.text != nil || v.sel != nil {
		t.Error("the diff selection outlived the page opened over it")
	}

	m.drawerTake()
	run(m.App, press(m.App, keyRune('y')))

	if got := clipboardWrites(clip); len(got) != 1 || got[0] == "edited 3" {
		t.Errorf("y wrote %q, want the page's cursor unit", got)
	}

	run(m.App, press(m.App, keyLeader, keyRune('y')))

	if got := clipboardWrites(clip); slices.Contains(got, "edited 3") {
		t.Errorf("leader y copied the hidden diff text %q", got)
	}
}

// counted is n characters of 4-digit counters, so a copy shows which cells it took.
func counted(n int) string {
	var b strings.Builder

	for i := 0; b.Len() < n; i++ {
		fmt.Fprintf(&b, "%04d", i)
	}

	return b.String()[:n]
}

// wrapRepo is a file with the line add inserted after its second line.
func wrapRepo(t *testing.T, add string) string {
	t.Helper()

	dir := newRepoDir(t)

	write(t, filepath.Join(dir, "f.txt"), "alpha\nbravo\ncharlie\ndelta\n")
	commitEverything(t, dir, "init")
	write(t, filepath.Join(dir, "f.txt"), "alpha\nbravo\n"+add+"\ncharlie\ndelta\n")

	return dir
}

// newWrapSel is a unified diff rig on wrapRepo and the row of the line's first part.
func newWrapSel(t *testing.T, line string) (*diffSel, int) {
	t.Helper()

	dir := wrapRepo(t, line)
	r := newKeysRig(t, dir)
	clip := fakeClip(r.App)
	v := r.opened(t)

	if r.layout(t).split {
		t.Fatal("setup: the diff is drawn side by side")
	}

	d := &diffSel{drawerRig: r, v: v, clip: clip, dir: dir, root: rootOf(t, dir)}
	row := rowWith(t, v, line[:8])

	if v.wrapW.line <= 0 || v.wrapW.line*2 >= len(line) {
		t.Fatalf("setup: the %d-character line does not take three rows at %d cells", len(line), v.wrapW.line)
	}

	return d, row
}

func TestWrappedLineCopiesAsOneLine(t *testing.T) {
	t.Parallel()

	line := counted(600)
	d, row := newWrapSel(t, line)
	w := d.v.wrapW.line

	d.dragRows(t, row, d.code(t, row)+4, row+2, d.code(t, row+2)+2)

	if got, want := primaryWrites(d.clip), []string{line[4 : 2*w+3]}; !slices.Equal(got, want) {
		t.Errorf("primary writes %q, want %q", got, want)
	}

	d.clip.calls = nil
	next := rowWith(t, d.v, "charlie")
	d.dragRows(t, row, d.code(t, row)+4, next, d.code(t, next)+2)

	if got, want := primaryWrites(d.clip), []string{line[4:] + "\ncha"}; !slices.Equal(got, want) {
		t.Errorf("a drag on to the next line wrote %q, want %q", got, want)
	}
}

func TestWrappedTripleClickTakesLine(t *testing.T) {
	t.Parallel()

	line := counted(600)
	d, row := newWrapSel(t, line)
	x, y := rowCell(t, d.drawerRig, row+1, d.code(t, row+1)+3)

	for range 3 {
		clickRun(d.App, x, y)
	}

	got := primaryWrites(d.clip)
	if len(got) == 0 || got[len(got)-1] != line {
		t.Errorf("a triple-click on the second row wrote %q, want the line", got)
	}
}

func TestWrappedYCopiesLine(t *testing.T) {
	t.Parallel()

	line := counted(600)
	d, row := newWrapSel(t, line)
	d.v.cursor = row
	d.drawerTake()
	run(d.App, press(d.App, keyRune('y')))

	if got := clipboardWrites(d.clip); !slices.Equal(got, []string{line}) {
		t.Errorf("y wrote %q, want the line", got)
	}
}

func TestWrappedDoubleClick(t *testing.T) {
	t.Parallel()

	line := "head " + strings.Repeat("b", 595)
	d, row := newWrapSel(t, line)
	w := d.v.wrapW.line
	x, y := rowCell(t, d.drawerRig, row+1, d.code(t, row+1)+3)

	clickRun(d.App, x, y)
	clickRun(d.App, x, y)

	got := primaryWrites(d.clip)
	if want := strings.Repeat("b", w); len(got) == 0 || got[len(got)-1] != want {
		t.Errorf("a double-click on the second row wrote %q, want its %d cells", got, w)
	}
}

func TestWrappedSplitCopy(t *testing.T) {
	t.Parallel()

	dir := newRepoDir(t)
	long := counted(100)

	write(t, filepath.Join(dir, "f.txt"), "alpha\nold one\ncharlie\n")
	commitEverything(t, dir, "init")
	write(t, filepath.Join(dir, "f.txt"), "alpha\n"+long+"\ncharlie\n")

	d := newSplitSel(t, dir)
	row := rowWith(t, d.v, "old one")
	l, _, r := d.splitCols(t)

	if d.v.wrapW.left <= 0 || d.v.rows[row+2].line != d.v.rows[row].line || d.v.rows[row+3].line == d.v.rows[row].line {
		t.Fatalf("setup: the pair does not take three rows (wrap %+v)", d.v.wrapW)
	}

	d.dragRows(t, row, r, row+2, r+d.v.wrapW.right-1)

	if got, want := primaryWrites(d.clip), []string{long}; !slices.Equal(got, want) {
		t.Errorf("a drag on the right side wrote %q, want %q", got, want)
	}

	d.clip.calls = nil
	d.dragRows(t, row, l, row+2, l+3)

	if got := primaryWrites(d.clip); !slices.Equal(got, []string{"old one"}) {
		t.Errorf("a drag on the left side wrote %q, want the left line alone", got)
	}
}

// Not parallel: the drawer highlights code, which takes the highlighter's time.
func TestWrappedSelectionPaint(t *testing.T) {
	line := counted(600)
	d, row := newWrapSel(t, line)
	w := d.v.wrapW.line

	d.dragRows(t, row, d.code(t, row)+4, row+2, d.code(t, row+2)+2)

	lay := d.layout(t)
	lines := d.drawerLines(d.store.State(), mustRect(t, d.drawerRig))
	sel := d.current().theme.UI.Selection
	off := 1
	top := d.v.viewTop(lay.diffH)

	if lay.treeW > 0 {
		off += lay.treeW + 1
	}

	on := func(r, col int) bool { return bgIs(cellBg(lines[1+lay.headH+r-top], off+col), sel) }
	code := d.code(t, row)

	for _, c := range []struct {
		name     string
		row, col int
		want     bool
	}{
		{"before the start", row, code + 3, false},
		{"the start", row, code + 4, true},
		{"the end of the first row", row, code + w - 1, true},
		{"the start of the middle row", row + 1, code, true},
		{"the end of the middle row", row + 1, code + w - 1, true},
		{"the end", row + 2, code + 2, true},
		{"after the end", row + 2, code + 3, false},
		{"the wrap mark", row + 1, code - diffview.SignWidth, false},
		{"the second number", row + 1, code - diffview.SignWidth - 1, false},
		{"the first number", row + 1, markW + 1, false},
	} {
		if got := on(c.row, c.col); got != c.want {
			t.Errorf("%s: on the selection ground = %v, want %v", c.name, got, c.want)
		}
	}
}

func TestWrappedFullViewWhileSplit(t *testing.T) {
	t.Parallel()

	line := counted(600)
	d := newSplitSel(t, wrapRepo(t, line))

	d.keys(keyRune('f'))
	d.until(t, "the full file", func() bool { return len(d.v.full) == 1 })

	row := slices.IndexFunc(d.v.rows, func(r reviewRow) bool { return r.kind == rowFull && r.text == line })
	if row < 0 {
		t.Fatal("the full view has no row for the long line")
	}

	w := d.v.wrapW.full
	if w <= 0 || d.v.rows[row+2].text != line || d.v.rows[row+2].part != 2 {
		t.Fatalf("setup: the line does not take rows from %d at %d cells", row, w)
	}

	lay := d.layout(t)
	lines := d.drawerLines(d.store.State(), mustRect(t, d.drawerRig))
	top := d.v.viewTop(lay.diffH)

	if got := ansi.Strip(lines[1+lay.headH+row+1-top]); !strings.Contains(got, wrapGlyph) {
		t.Errorf("the continuation row is drawn without %q: %q", wrapGlyph, got)
	}

	d.dragRows(t, row, d.code(t, row)+4, row+2, d.code(t, row+2)+2)

	if got, want := primaryWrites(d.clip), []string{line[4 : 2*w+3]}; !slices.Equal(got, want) {
		t.Errorf("primary writes %q, want %q", got, want)
	}
}
