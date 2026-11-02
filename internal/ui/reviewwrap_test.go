package ui

import (
	"fmt"
	"math/rand/v2"
	"reflect"
	"slices"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	uv "github.com/charmbracelet/ultraviolet"
	"github.com/charmbracelet/x/ansi"

	"github.com/mhersson/aim/internal/core"
	"github.com/mhersson/aim/internal/git"
	"github.com/mhersson/aim/internal/ui/diffview"
)

func TestWrapLine(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		text  string
		units []diffUnit
		width int
		want  [][2]int
	}{
		{"exact fit", "abcdefghij", nil, 10, [][2]int{{0, 10}}},
		{"one over", "abcdefghijk", nil, 10, [][2]int{{0, 10}, {10, 11}}},
		{"space in reach", strings.Repeat("a", 21) + " " + strings.Repeat("b", 8), nil, 25, [][2]int{{0, 22}, {22, 30}}},
		{"space out of reach", strings.Repeat("a", 7) + " " + strings.Repeat("b", 22), nil, 25, [][2]int{{0, 25}, {25, 30}}},
		{"tab moves whole", "abcdefghi\tx", nil, 10, [][2]int{{0, 9}, {9, 13}}},
		{"tag moves whole", "abcdefgh\u200bxy", nil, 10, [][2]int{{0, 8}, {8, 18}}},
		{"tag wider than the column", "\u200b", nil, 5, [][2]int{{0, 8}}},
		{"empty", "", nil, 10, [][2]int{{0, 0}}},
		{"ending tag wraps alone", "", lineUnits("abcdefghij", true), 10, [][2]int{{0, 10}, {10, 18}}},
		{"ending tag skips the reach space", "", lineUnits("foo barxyz", true), 10, [][2]int{{0, 10}, {10, 18}}},
		{"budget ellipsis wraps alone", "", lineUnits(strings.Repeat("a", 1500), false), 1024, [][2]int{{0, 1024}, {1024, 1025}}},
		{"budget ellipsis skips the reach space", "", lineUnits(strings.Repeat("a", 1010)+" "+strings.Repeat("b", 100), false), 1024, [][2]int{{0, 1024}, {1024, 1025}}},
		{"no width", "abc def", nil, 0, [][2]int{{0, 7}}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			units := tt.units
			if units == nil {
				units = diffUnits(tt.text)
			}

			parts := wrapLine(units, tt.width)

			var got [][2]int
			for _, p := range parts {
				got = append(got, [2]int{p.c0, p.c1})
			}

			if !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("parts = %v, want %v", got, tt.want)
			}

			if tt.width <= 0 && (len(parts) != 1 || parts[0].u0 != 0 || parts[0].u1 != len(units)) {
				t.Fatalf("width %d: part %+v does not hold all %d units", tt.width, parts, len(units))
			}
		})
	}
}

func TestLineUnits(t *testing.T) {
	t.Parallel()

	long := lineUnits(strings.Repeat("x", 1500), false)
	if len(long) != lineBudget+1 {
		t.Fatalf("units = %d, want %d", len(long), lineBudget+1)
	}

	if last := long[len(long)-1]; !last.whole || last.cells != 1 {
		t.Fatalf("last unit = %+v, want a whole 1-cell ellipsis", last)
	}

	tagged := lineUnits("a", true)
	if len(tagged) != 2 || tagged[1].text != endingTag {
		t.Fatalf("tagged units = %+v, want the ending tag last", tagged)
	}

	if got := lineUnits("a\r", false); len(got) != 1 {
		t.Fatalf("a CR without the tag gave %d units, want 1", len(got))
	}
}

func TestLinePartsFastPath(t *testing.T) {
	t.Parallel()

	rng := rand.New(rand.NewPCG(1, 2))
	alphabet := []rune("ab c\t\u200b\r")

	for range 200 {
		var b strings.Builder
		for range rng.IntN(60) {
			b.WriteRune(alphabet[rng.IntN(len(alphabet))])
		}

		raw := b.String()
		width := 1 + rng.IntN(40)

		for _, tagCR := range []bool{false, true} {
			got := lineParts(raw, tagCR, width)
			want := wrapLine(lineUnits(raw, tagCR), width)

			if !reflect.DeepEqual(got, want) {
				t.Fatalf("lineParts(%q, %v, %d) = %v, want %v", raw, tagCR, width, got, want)
			}
		}
	}

	long := strings.Repeat("a", 1500)
	if got, want := lineParts(long, false, 2000), wrapLine(lineUnits(long, false), 2000); !reflect.DeepEqual(got, want) {
		t.Fatalf("long line: lineParts = %v, want %v", got, want)
	}
}

func TestWholePart(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		raw   string
		tagCR bool
		width int
		whole bool
	}{
		{"plain ASCII", "foo bar", false, 10, true},
		{"empty", "", false, 10, true},
		{"tab at column 0", "\tfoo", false, 20, true},
		{"tab after one cell", "a\tb", false, 20, true},
		{"tab after two cells", "ab\tc", false, 20, true},
		{"tab after three cells", "abc\td", false, 20, true},
		{"tab mid-word", "fo\too", false, 20, true},
		{"two tabs", "\t\tx", false, 20, true},
		{"exactly the width", "\tabcd", false, 8, true},
		{"one cell over", "\tabcde", false, 8, false},
		{"a tab takes it over", "ab\tc", false, 4, false},
		{"at the budget", "\t" + strings.Repeat("a", lineBudget-1), false, 2000, true},
		{"one over the budget", "\t" + strings.Repeat("a", lineBudget), false, 2000, false},
		{"final CR", "\tfoo\r", false, 20, true},
		{"final CR tagged", "\tfoo\r", true, 20, false},
		{"CR mid-line", "\tfo\ro", false, 20, false},
		{"non-ASCII byte", "\tfoé", false, 20, false},
		{"wide cells", "\t世界", false, 20, false},
		{"wide cells that wrap", "\t世界世界", false, 6, false},
		{"control byte", "\tfo\x01o", false, 20, false},
		{"DEL", "\tfo\x7fo", false, 20, false},
		{"no width", "\tfoo", false, 0, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if _, ok := wholePart(tt.raw, tt.tagCR, tt.width); ok != tt.whole {
				t.Fatalf("wholePart at width %d: ok = %v, want %v", tt.width, ok, tt.whole)
			}

			for _, width := range append([]int{tt.width}, 1, 2, 3, 4, 5, 7, 8, 9, 12) {
				want := wrapLine(lineUnits(tt.raw, tt.tagCR), width)

				if p, ok := wholePart(tt.raw, tt.tagCR, width); ok && !reflect.DeepEqual([]wrapPart{p}, want) {
					t.Fatalf("width %d: wholePart = %v, want %v", width, p, want)
				}

				if got := lineParts(tt.raw, tt.tagCR, width); !reflect.DeepEqual(got, want) {
					t.Fatalf("width %d: lineParts = %v, want %v", width, got, want)
				}
			}
		})
	}
}

const wrapPath = "wrap.go"

// longLine is 70 characters with no space, so a 30-cell edge breaks it at
// cells 30 and 60.
var longLine = strings.Repeat("0123456789", 7)

// wrapResult is one file whose hunk holds a long line, a short one, a tab
// indented long one and a removed/added pair of different lengths.
func wrapResult() git.Result {
	f := fileSpec(wrapPath, git.Modified, hunkSpec(1, 1, "",
		" "+longLine,
		" short",
		" \t"+strings.Repeat("x", 70),
		"-"+strings.Repeat("abcdefghi.", 7),
		"+"+strings.Repeat("abcdefghi.", 16),
	))

	return git.Result{Turn: 1, Files: []git.FileDiff{f}}
}

// partsOf counts the rows of line li of hunk 0 and checks their parts run 0, 1, ...
func partsOf(t *testing.T, rows []reviewRow, li int) int {
	t.Helper()

	n := 0

	for _, r := range rows {
		if (r.kind != rowLine && r.kind != rowPair) || r.line != li {
			continue
		}

		if r.part != n {
			t.Fatalf("line %d: row %d has part %d, want %d", li, n, r.part, n)
		}

		n++
	}

	return n
}

func TestWrappedRows(t *testing.T) {
	t.Parallel()

	res := wrapResult()
	tests := []struct {
		name  string
		split bool
		wrap  codeWidths
		want  map[int]int // line index -> rows
	}{
		{"no wrap", false, codeWidths{}, map[int]int{0: 1, 1: 1, 2: 1, 3: 1, 4: 1}},
		{"unified 30", false, codeWidths{line: 30}, map[int]int{0: 3, 1: 1, 2: 3, 3: 3, 4: 6}},
		{"unified 70 fits", false, codeWidths{line: 70}, map[int]int{0: 1, 1: 1, 3: 1, 4: 3}},
		{"split takes the longer side", true, codeWidths{left: 30, right: 50}, map[int]int{0: 3, 1: 1, 3: 4}},
		{"split without a width", true, codeWidths{line: 30}, map[int]int{0: 1, 3: 1}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			rows := buildRows(res, rowOpts{split: tt.split, wrap: tt.wrap})

			for li, want := range tt.want {
				if got := partsOf(t, rows, li); got != want {
					t.Errorf("line %d: %d rows, want %d", li, got, want)
				}
			}

			for _, r := range rows {
				if r.kind == rowLine || r.kind == rowPair {
					if r.hunk != 0 || r.file != 0 {
						t.Errorf("row %+v is off its hunk", r)
					}
				} else if r.part != 0 {
					t.Errorf("row of kind %d has part %d", r.kind, r.part)
				}
			}
		})
	}
}

func TestWrappedFullRows(t *testing.T) {
	t.Parallel()

	res := wrapResult()
	ghost, added := strings.Repeat("g", 70), strings.Repeat("n", 70)
	ft := newFullText([]string{"a", ghost, "b"}, []string{"a", added, "b"}, nil)

	for _, folded := range []bool{false, true} {
		rows := buildRows(res, rowOpts{
			full: map[string]fullText{wrapPath: ft}, whole: map[string]bool{wrapPath: true},
			ghostsStartFolded: folded, wrap: codeWidths{full: 30, line: 7},
		})

		counts := map[reviewRowKind]int{}

		var fullParts []int

		for _, r := range rows {
			counts[r.kind]++

			if r.kind == rowFull {
				fullParts = append(fullParts, r.part)
			}
		}

		if want := []int{0, 0, 1, 2, 0}; !slices.Equal(fullParts, want) {
			t.Errorf("folded=%v: full row parts %v, want %v", folded, fullParts, want)
		}

		if folded && (counts[rowFold] != 1 || counts[rowGhost] != 0) {
			t.Errorf("folded: counts %v, want one fold row and no ghost", counts)
		}

		if !folded && counts[rowGhost] != 3 {
			t.Errorf("%d ghost rows, want 3", counts[rowGhost])
		}

		if counts[rowFile] != 1 {
			t.Errorf("%d file rows, want 1", counts[rowFile])
		}
	}
}

// wrapScene is a scene over res whose unified rows wrap at w.line, at the
// window width that gives the drawer that code column.
func wrapScene(t *testing.T, res git.Result, w codeWidths, h int) (*App, *reviewView) {
	t.Helper()

	build := func(win int) (*App, *reviewView) {
		s := reviewScene{
			theme: "catppuccin-mocha", w: win, h: h, res: res, turn: 1, turns: 1, focused: true,
			tune: func(_ *testing.T, _ *App, v *reviewView) {
				v.wrapW = codeWidths{line: w.line}
				v.rebuild(false)
			},
		}
		a := s.build(t)

		return a, a.drawer.views[a.store.State().Tab().ID]
	}

	const probe = 50 // under 60, so the drawer takes the whole window

	a, v := build(probe)
	r, _ := a.drawerRect(a.store.State())

	if d := w.line - a.drawerLayoutOf(r, v, false).unifiedCode(2); d != 0 {
		a, v = build(probe + d)
	}

	return a, v
}

// padded is a file of n short context lines, then lines built from the rest.
func padded(n int, rest ...string) git.Result {
	specs := make([]string, 0, n+len(rest))
	for i := range n {
		specs = append(specs, fmt.Sprintf(" pad %d", i))
	}

	f := fileSpec(wrapPath, git.Modified, hunkSpec(1, 1, "", append(specs, rest...)...))

	return git.Result{Turn: 1, Files: []git.FileDiff{f}}
}

func TestWrappedCursorMoves(t *testing.T) {
	t.Parallel()

	a, v := wrapScene(t, padded(1, " "+longLine), codeWidths{line: 30}, 30)
	// Rows: file 0, hunk 1, pad 2, the long line 3-5.
	at := func(want int, what string) {
		t.Helper()

		if v.cursor != want {
			t.Fatalf("%s: cursor %d, want %d", what, v.cursor, want)
		}
	}

	press(a, keyRune('j'), keyRune('j'), keyRune('j'))
	at(3, "j onto the long line")

	press(a, keyRune('j'))
	at(3, "j from the last line stays")

	press(a, keyRune('k'))
	at(2, "k back")

	press(a, keyRune('j'), keyRune('k'))
	at(2, "k from the first row of the long line")

	press(a, keyRune('G'))
	at(3, "G lands on the last line's first row")

	press(a, keyRune('g'))
	at(0, "g")

	// A half page that lands on a continuation row puts the cursor on its first row.
	half := max(a.reviewBodyH(v)/2, 1)
	a, v = wrapScene(t, padded(half-1, " "+longLine), codeWidths{line: 30}, 30)
	press(a, keyRune('j'), keyRune('j'))
	press(a, tea.KeyPressMsg{Code: 'd', Mod: tea.ModCtrl})

	if v.cursor != half+1 || v.rows[v.cursor].part != 0 {
		// Rows: file, hunk, half-1 pads, so the long line starts at half+1.
		t.Errorf("ctrl+d: cursor %d part %d, want row %d part 0", v.cursor, v.rows[v.cursor].part, half+1)
	}
}

func TestWrappedClickLandsOnFirstRow(t *testing.T) {
	t.Parallel()

	a, v := wrapScene(t, padded(1, " "+longLine), codeWidths{line: 30}, 30)

	h, ok := a.rowsHost()
	if !ok {
		t.Fatal("no rows host")
	}

	clickRun(a, h.rect.Min.X+15, h.rect.Min.Y+5-h.top)

	if v.cursor != 3 {
		t.Errorf("a click on part 2: cursor %d, want 3", v.cursor)
	}
}

func TestWrappedTallLine(t *testing.T) {
	t.Parallel()

	tall := strings.Repeat("t", 1024)
	res := padded(0, append(append(manyPads(3), " "+tall), manyShort(25)...)...)

	var (
		a *App
		v *reviewView
	)

	// The drawer's chrome sets diffH from the scene height; find the height that gives 20.
	for sh := 20; sh <= 40; sh++ {
		a, v = wrapScene(t, res, codeWidths{line: 40}, sh)
		if a.reviewBodyH(v) == 20 {
			break
		}
	}

	h := a.reviewBodyH(v)
	if h != 20 {
		t.Fatalf("diffH = %d, want 20", h)
	}

	// Rows: file 0, hunk 1, pads 2-4, the tall line 5-30 (26 rows), short lines 31-.
	const first, end = 5, 31

	if v.lineStart(first+4) != first || v.lineEnd(first+4) != end {
		t.Fatalf("the tall line spans %d-%d, want %d-%d", v.lineStart(first+4), v.lineEnd(first+4), first, end)
	}

	shows := func() bool { return v.lineEnd(v.cursor) > v.top && v.lineStart(v.cursor) < v.top+h }

	for range 5 {
		press(a, keyRune('j'))
	}

	if v.cursor != first || v.top != first {
		t.Fatalf("j onto the tall line: cursor %d top %d, want both %d", v.cursor, v.top, first)
	}

	x, y := wheelCell(t, a)

	for n := 0; ; n++ {
		if n > 30 {
			t.Fatal("the wheel never moved the cursor off the tall line")
		}

		wheelAt(a, x, y, tea.MouseWheelDown)

		if v.cursor == first {
			if v.top >= end {
				t.Fatalf("notch %d: cursor on the tall line with top %d past it", n, v.top)
			}

			continue
		}

		if v.cursor != v.top || v.rows[v.cursor].part != 0 {
			t.Fatalf("notch %d: cursor %d, want the first line starting in the view (top %d)", n, v.cursor, v.top)
		}

		break
	}

	// j and k step one line at a time and keep the cursor's line in view.
	press(a, keyRune('g'))

	for v.cursor != len(v.rows)-1 {
		prev := v.cursor

		press(a, keyRune('j'))

		if v.cursor <= prev || !shows() {
			t.Fatalf("j from %d: cursor %d, top %d", prev, v.cursor, v.top)
		}
	}

	for v.cursor != 0 {
		prev := v.cursor

		press(a, keyRune('k'))

		if v.cursor >= prev || !shows() {
			t.Fatalf("k from %d: cursor %d, top %d", prev, v.cursor, v.top)
		}
	}

	// A half page moves on too.
	press(a, keyRune('j'), keyRune('j'), keyRune('j'), keyRune('j'), keyRune('j'))

	for prev := v.cursor; v.cursor < len(v.rows)-1; prev = v.cursor {
		press(a, tea.KeyPressMsg{Code: 'd', Mod: tea.ModCtrl})

		if v.cursor <= prev || !shows() {
			t.Fatalf("ctrl+d from %d: cursor %d", prev, v.cursor)
		}
	}
}

func manyPads(n int) []string {
	out := make([]string, n)
	for i := range out {
		out[i] = fmt.Sprintf(" pad %d", i)
	}

	return out
}

func manyShort(n int) []string {
	out := make([]string, n)
	for i := range out {
		out[i] = fmt.Sprintf(" short %d", i)
	}

	return out
}

// wheelCell is a cell of the diff rows to scroll over.
func wheelCell(t *testing.T, a *App) (x, y int) {
	t.Helper()

	h, ok := a.rowsHost()
	if !ok {
		t.Fatal("no rows host")
	}

	return h.rect.Min.X + 5, h.rect.Min.Y + 3
}

func TestWrappedSearchOncePerLine(t *testing.T) {
	t.Parallel()

	long := "needle" + strings.Repeat("-", 64)
	a, v := wrapScene(t, padded(0, " "+long, " mid", " a needle here"), codeWidths{line: 30}, 30)

	typed := func(s string) {
		press(a, keyRune('/'))

		for _, r := range s {
			press(a, keyRune(r))
		}

		press(a, tea.KeyPressMsg{Code: tea.KeyEnter})
	}

	typed("needle")

	if r := v.rows[v.cursor]; r.line != 0 || r.part != 0 {
		t.Fatalf("first match: row %+v, want line 0 part 0", r)
	}

	typed("")

	if r := v.rows[v.cursor]; r.line != 2 || r.part != 0 {
		t.Fatalf("second match: row %+v, want line 2 part 0", r)
	}

	typed("")

	if r := v.rows[v.cursor]; r.line != 0 || r.part != 0 {
		t.Fatalf("wrapped match: row %+v, want line 0 part 0", r)
	}

	// N goes back by lines too, landing on a line's first row.
	for _, want := range []int{2, 0} {
		press(a, keyRune('N'))

		if r := v.rows[v.cursor]; r.line != want || r.part != 0 {
			t.Fatalf("N: row %+v, want line %d part 0", r, want)
		}
	}
}

func TestWrappedFreshRows(t *testing.T) {
	t.Parallel()

	prev := padded(0, " "+longLine, " mid")
	next := padded(0, " "+longLine, " mid", "+"+strings.Repeat("z", 70))
	rows := buildRows(next, rowOpts{wrap: codeWidths{line: 30}})
	fresh := freshRows(prev, next, rows)

	for i, r := range rows {
		want := r.kind == rowLine && r.line == 2

		if fresh[i] != want {
			t.Errorf("row %d (line %d part %d): fresh = %v, want %v", i, r.line, r.part, fresh[i], want)
		}
	}

	if len(fresh) != 3 {
		t.Errorf("%d fresh rows, want the 3 of the added line", len(fresh))
	}
}

func TestWrappedAnchorPart(t *testing.T) {
	t.Parallel()

	res := wrapResult()
	rows := buildRows(res, rowOpts{wrap: codeWidths{line: 30}})
	third := slices.IndexFunc(rows, func(r reviewRow) bool { return r.kind == rowLine && r.line == 0 && r.part == 2 })

	if third < 0 {
		t.Fatal("no third part")
	}

	anchor := anchorAt(res, rows, third)
	if anchor.part != 2 {
		t.Fatalf("anchor part = %d, want 2", anchor.part)
	}

	// A reload that keeps the line, with a line added before it, and a view
	// whose width wraps the line in two rows only.
	later := wrapResult()
	later.Files[0].Hunks[0].Lines = slices.Insert(later.Files[0].Hunks[0].Lines, 0, git.Line{Kind: git.Context, Old: 1, New: 1, Text: "first"})

	tests := []struct {
		name string
		wrap codeWidths
		part int
	}{
		{"same width", codeWidths{line: 30}, 2},
		{"narrower rows", codeWidths{line: 40}, 1},
		{"no wrap", codeWidths{}, 0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			rows2 := buildRows(later, rowOpts{wrap: tt.wrap})
			r := rows2[restoreAnchor(later, rows2, anchor)]

			if r.kind != rowLine || r.line != 1 || r.part != tt.part {
				t.Errorf("restored row %+v, want line 1 part %d", r, tt.part)
			}
		})
	}

	plain := anchorAt(res, rows, third)
	plain.part = 0

	if got := restoreAnchor(res, rows, plain); rows[got].part != 0 {
		t.Errorf("a part-0 anchor restored to part %d", rows[got].part)
	}
}

func TestWrappedViewKeepsSelectionPart(t *testing.T) {
	t.Parallel()

	_, v := wrapScene(t, wrapResult(), codeWidths{line: 30}, 30)
	third := slices.IndexFunc(v.rows, func(r reviewRow) bool { return r.kind == rowLine && r.line == 0 && r.part == 2 })

	v.cursor = third
	v.sel = &selection{anchor: point{row: third - 2}, head: point{row: third}}
	v.rebuild(false)

	if v.rows[v.cursor].part != 0 || v.cursor != third-2 {
		t.Errorf("cursor row %d part %d, want row %d part 0", v.cursor, v.rows[v.cursor].part, third-2)
	}

	if v.sel.head.row != third || v.sel.anchor.row != third-2 {
		t.Errorf("selection ends %d and %d, want %d and %d", v.sel.anchor.row, v.sel.head.row, third-2, third)
	}
}

func TestWrappedRestoreFallbackFirstRow(t *testing.T) {
	t.Parallel()

	res := padded(0, " "+longLine, " mid")
	rows := buildRows(res, rowOpts{wrap: codeWidths{line: 30}})
	// Row 0 file, 1 hunk, 2-4 the long line, 5 mid. No file matches, so the
	// nearest index (row 4, a continuation row) is the answer.
	gone := rowAnchor{path: "gone.go", row: rowLine, index: 4}

	if got := restoreAnchor(res, rows, gone); got != 2 {
		t.Errorf("restored row %d, want 2 (the long line's first row)", got)
	}
}

// altText is 70 cells of text no two 30-cell windows of which are alike, and
// none like dtext's.
const altText = "abcdefghijklmnopqrstuvwxyz0123456789ABCDEFGHIJKLMNOPQRSTUVWXYZ!@#$%^&*"

// dtext is n cells of text no two 30-cell windows of which are alike.
func dtext(n int) string {
	var b strings.Builder
	for i := 0; b.Len() < n; i++ {
		fmt.Fprintf(&b, "%02d", i)
	}

	return b.String()[:n]
}

// drawnScene draws a scene over res in a window of win columns, the rows
// wrapped at wrap and laid out side by side or not, and hands back what the
// frame needs to be read.
type drawnScene struct {
	a     *App
	v     *reviewView
	d     *drawCtx
	lay   drawerLayout
	lines []string
}

func newDrawnScene(t *testing.T, res git.Result, win, h int, wrap codeWidths, split bool, tune func(*reviewView)) drawnScene {
	t.Helper()

	s := reviewScene{
		theme: "catppuccin-mocha", w: win, h: h, res: res, turn: 1, turns: 1, focused: true,
		tune: func(_ *testing.T, _ *App, v *reviewView) {
			v.split = split

			if tune != nil {
				tune(v)
			}

			v.rebuild(split)
		},
	}
	a := s.build(t)
	st := a.store.State()

	// The drawer wraps at its own widths once it settles; the test wraps at its own.
	a.drawer.views[st.Tab().ID].wrapW = wrap
	a.drawer.views[st.Tab().ID].rebuild(split)

	r, _ := a.drawerRect(st)
	d, _, ok := a.drawCtxFor(st)

	if !ok {
		t.Fatal("no drawer context")
	}

	v := a.drawer.views[st.Tab().ID]

	return drawnScene{a: a, v: v, d: d, lay: d.lay, lines: a.drawerLines(st, r)}
}

// find is the index of the one drawn line whose text holds sub.
func (s drawnScene) find(t *testing.T, sub string) int {
	t.Helper()

	at := -1

	for i, l := range s.lines {
		if strings.Contains(ansi.Strip(l), sub) {
			if at >= 0 {
				t.Fatalf("%q is on lines %d and %d", sub, at, i)
			}

			at = i
		}
	}

	if at < 0 {
		t.Fatalf("no line holds %q", sub)
	}

	return at
}

func styleAt(line string, x int) uv.Style {
	buf := uv.NewScreenBuffer(ansi.StringWidth(line), 1)
	uv.NewStyledString(line).Draw(buf, buf.Bounds())

	return buf.CellAt(x, 0).Style
}

func TestWrappedRowLook(t *testing.T) {
	t.Parallel()

	texts := []string{dtext(70), altText}
	res := padded(0, " "+texts[0], "+"+texts[1])
	kinds := []diffview.Kind{diffview.Context, diffview.Add}

	for li, kind := range kinds {
		text := texts[li]
		parts := []string{text[:30], text[30:60], text[60:]}

		for _, cursor := range []bool{false, true} {
			t.Run(fmt.Sprintf("line %d cursor %v", li, cursor), func(t *testing.T) {
				t.Parallel()

				s := newDrawnScene(t, res, 100, 30, codeWidths{line: 30}, false, nil)
				s.v.cursor = 2 + 3*li

				if !cursor {
					s.v.cursor = 0
				}

				st := s.a.store.State()
				r, _ := s.a.drawerRect(st)
				s.lines = s.a.drawerLines(st, r)
				s.d, _, _ = s.a.drawCtxFor(st)

				gw, ui, th := s.lay.gw, s.d.ui, s.d.dv
				codeX := 1 + markW + 2*gw + diffview.SignWidth

				for k, part := range parts {
					line := s.lines[s.find(t, part)]
					plain := ansi.Strip(line)

					if got := ansi.Cut(plain, 1, 2); got != map[bool]string{false: " ", true: markGlyph}[cursor] {
						t.Errorf("part %d: cell 0 = %q, cursor %v", k, got, cursor)
					}

					if got := ansi.Cut(plain, codeX, codeX+len(part)); got != part {
						t.Errorf("part %d: code = %q, want %q", k, got, part)
					}

					if strings.Contains(plain, "…") {
						t.Errorf("part %d ends in a cut marker: %q", k, plain)
					}

					if bg := styleAt(line, 2).Bg; bg == nil || !sameColor(bg, th.GutterBg(kind, cursor)) {
						t.Errorf("part %d: number cell ground %v, want %v", k, bg, th.GutterBg(kind, cursor))
					}

					if bg := styleAt(line, codeX).Bg; bg == nil || !sameColor(bg, th.CodeBg(kind, cursor)) {
						t.Errorf("part %d: code ground %v, want %v", k, bg, th.CodeBg(kind, cursor))
					}

					if k == 0 {
						continue
					}

					if got := ansi.Cut(plain, 2, 2+2*gw); strings.TrimSpace(got) != "" {
						t.Errorf("part %d: number cells %q, want blank", k, got)
					}

					if got := ansi.Cut(plain, codeX-diffview.SignWidth, codeX); got != wrapGlyph+" " {
						t.Errorf("part %d: sign = %q, want %q", k, got, wrapGlyph+" ")
					}

					st := styleAt(line, codeX-diffview.SignWidth)
					if st.Fg == nil || !sameColor(st.Fg, ui.Subtle) || st.Bg == nil || !sameColor(st.Bg, th.CodeBg(kind, cursor)) {
						t.Errorf("part %d: %s fg %v bg %v, want Subtle on the code ground", k, wrapGlyph, st.Fg, st.Bg)
					}
				}
			})
		}
	}
}

func TestWrappedBudgetLine(t *testing.T) {
	t.Parallel()

	s := newDrawnScene(t, padded(0, " "+strings.Repeat("a", 1500)), 100, 60, codeWidths{line: 40}, false, nil)

	var rows []string

	for _, l := range s.lines {
		if p := ansi.Strip(l); strings.Contains(p, "aaaaaaaa") {
			rows = append(rows, diffEnd(p))
		}
	}

	if want := (lineBudget + 1 + 39) / 40; len(rows) != want {
		t.Fatalf("%d rows, want %d", len(rows), want)
	}

	for i, r := range rows {
		if got := strings.HasSuffix(r, "…"); got != (i == len(rows)-1) {
			t.Errorf("row %d ends in a cut marker = %v: %q", i, got, r)
		}
	}
}

func TestWrappedMatchAcrossRows(t *testing.T) {
	t.Parallel()

	text := dtext(70) // "1415" runs over the cut at cell 30
	s := newDrawnScene(t, padded(0, " "+text), 100, 30, codeWidths{line: 30}, false, func(v *reviewView) { v.search = new("1415") })
	codeX := 1 + markW + 2*s.lay.gw + diffview.SignWidth
	first, second := s.lines[s.find(t, text[:30])], s.lines[s.find(t, text[30:60])]

	for _, tt := range []struct {
		line string
		off  int
		want bool
	}{
		{first, 27, false},
		{first, 28, true},
		{first, 29, true},
		{second, 0, true},
		{second, 1, true},
		{second, 2, false},
	} {
		st := styleAt(tt.line, codeX+tt.off)
		// A number's own colour is Match's, so the bold tells a match.
		if got := st.Fg != nil && sameColor(st.Fg, s.d.ui.Match) && st.Attrs&uv.AttrBold != 0; got != tt.want {
			t.Errorf("cell %d of %q: Match = %v, want %v", tt.off, ansi.Strip(tt.line)[codeX:codeX+30], got, tt.want)
		}
	}
}

func TestWrappedSplitSides(t *testing.T) {
	t.Parallel()

	right := dtext(70)
	lone := altText
	res := git.Result{Turn: 1, Files: []git.FileDiff{fileSpec(wrapPath, git.Modified, hunkSpec(1, 1, "",
		"-"+strings.Repeat("L", 65), "+"+right, "+"+lone))}}
	res.Files[0].Hunks[0].Lines[0].Text = strings.Repeat("L", 30)

	probe := newDrawnScene(t, res, 160, 30, codeWidths{}, true, nil)
	cw := probe.lay.codeWidths()
	s := newDrawnScene(t, res, 160, 30, cw, true, nil)

	if !s.lay.split || cw.left < 30 || len(lineParts(right, false, cw.right)) != 3 {
		t.Fatalf("widths %+v: want a split drawer, a left column of 30 or more and the right line in 3 rows", cw)
	}

	th := s.d.dv
	sep := 1 + s.lay.splitSep()
	rcode, _ := s.lay.splitCodeAt(1)
	lcode, lw := s.lay.splitCodeAt(0)
	rsign := 1 + rcode - diffview.SignWidth

	tests := []struct {
		name    string
		text    string
		missing bool
	}{{"paired", right, false}, {"left missing", lone, true}}

	for _, tt := range tests {
		for k, wp := range lineParts(tt.text, false, cw.right) {
			part := tt.text[wp.c0:wp.c1]
			line := s.lines[s.find(t, part)]
			plain := ansi.Strip(line)
			left := ansi.Cut(plain, 1, sep)
			kind, lkind := diffview.Add, diffview.Delete

			if tt.missing {
				lkind = diffview.Missing
			}

			if k == 0 {
				continue
			}

			if strings.Contains(left, wrapGlyph) {
				t.Errorf("%s part %d: the left side shows %s", tt.name, k, wrapGlyph)
			}

			if strings.TrimSpace(ansi.Cut(left, 1, ansi.StringWidth(left))) != "" {
				t.Errorf("%s part %d: the left side is not blank: %q", tt.name, k, left)
			}

			if got := strings.Count(plain, wrapGlyph); got != 1 || ansi.Cut(plain, rsign, rsign+1) != wrapGlyph {
				t.Errorf("%s part %d: %d %s, the right one at %d: %q", tt.name, k, got, wrapGlyph, rsign, plain)
			}

			if bg := styleAt(line, 2).Bg; bg == nil || !sameColor(bg, th.GutterBg(lkind, false)) {
				t.Errorf("%s part %d: left number ground %v, want %v", tt.name, k, bg, th.GutterBg(lkind, false))
			}

			for _, x := range []int{1 + lcode, 1 + lcode + lw - 1} {
				if bg := styleAt(line, x).Bg; bg == nil || !sameColor(bg, th.CodeBg(lkind, false)) {
					t.Errorf("%s part %d: left code ground at %d is %v, want %v", tt.name, k, x, bg, th.CodeBg(lkind, false))
				}
			}

			if fg := styleAt(line, rsign).Fg; fg == nil || !sameColor(fg, s.d.ui.Subtle) {
				t.Errorf("%s part %d: %s fg %v, want Subtle", tt.name, k, wrapGlyph, fg)
			}

			if bg := styleAt(line, rsign).Bg; bg == nil || !sameColor(bg, th.CodeBg(kind, false)) {
				t.Errorf("%s part %d: %s ground %v, want the added ground", tt.name, k, wrapGlyph, bg)
			}
		}
	}
}

func TestWrappedGhostRows(t *testing.T) {
	t.Parallel()

	ghost := dtext(70)
	ft := newFullText([]string{"a", ghost, "b"}, []string{"a", "b"}, nil)
	s := newDrawnScene(t, wrapResult(), 100, 30, codeWidths{full: 30}, false, func(v *reviewView) {
		v.full = map[string]fullText{wrapPath: ft}
		v.fullOn = allWhole(v.full)
	})
	codeX := 1 + markW + s.lay.gw + diffview.SignWidth

	for k := range 3 {
		part := ghost[30*k : min(30*k+30, 70)]
		line := s.lines[s.find(t, part)]
		plain := ansi.Strip(line)

		want := wrapGlyph + " "
		if k == 0 {
			want = "- "
		}

		if got := ansi.Cut(plain, codeX-diffview.SignWidth, codeX); got != want {
			t.Errorf("part %d: sign %q, want %q", k, got, want)
		}

		if k > 0 {
			if fg := styleAt(line, codeX-diffview.SignWidth).Fg; fg == nil || !sameColor(fg, s.d.ui.Subtle) {
				t.Errorf("part %d: %s fg %v, want Subtle", k, wrapGlyph, fg)
			}
		}

		if fg := styleAt(line, codeX).Fg; fg == nil || !sameColor(fg, s.d.ui.Muted) {
			t.Errorf("part %d: text fg %v, want Muted", k, fg)
		}

		if strings.Contains(plain, "…") {
			t.Errorf("part %d ends in a cut marker", k)
		}
	}
}

func TestWrappedRowsFollowWidth(t *testing.T) {
	t.Parallel()

	s := reviewScene{theme: "catppuccin-mocha", w: 200, h: 52, res: padded(1, " "+dtext(70)), turn: 1, turns: 1, focused: true}
	a := s.build(t)
	v := a.drawer.views[a.store.State().Tab().ID]

	sized(t, a, 200, 52)

	if v.wrapW.line < 70 {
		t.Fatalf("wrapW %+v at 200 columns wraps a 70-cell line", v.wrapW)
	}

	v.cursor = 3 // rows: file, hunk, pad, the long line

	rev, wide := v.rowsRev, v.wrapW

	sized(t, a, 200, 52)

	if v.rowsRev != rev {
		t.Errorf("a same-size resize rebuilt the rows")
	}

	sized(t, a, 120, 52)

	if v.wrapW == wide || v.rowsRev == rev {
		t.Fatalf("a narrower window kept wrapW %+v and rowsRev %d", v.wrapW, v.rowsRev)
	}

	if parts := len(lineParts(dtext(70), false, v.wrapW.line)); parts < 2 {
		t.Fatalf("the narrow column %d does not wrap the line", v.wrapW.line)
	}

	if v.cursor != 3 || v.rows[v.cursor].part != 0 {
		t.Errorf("cursor %d part %d, want the line's first row 3", v.cursor, v.rows[v.cursor].part)
	}

	if want := v.lineEnd(3) - 3; want < 2 {
		t.Errorf("the line takes %d rows after narrowing", want)
	}

	rev, narrow := v.rowsRev, v.wrapW

	openIn(t, a, core.SetReviewTree{Open: true})
	sized(t, a, 120, 52)

	if v.wrapW == narrow || v.rowsRev == rev {
		t.Errorf("opening the tree kept wrapW %+v and rowsRev %d", v.wrapW, v.rowsRev)
	}
}

func TestCodeWidths(t *testing.T) {
	t.Parallel()

	l := drawerLayout{diffW: 100, gw: 4}

	if got, want := l.codeWidths(), (codeWidths{line: l.unifiedCode(2), full: l.unifiedCode(1)}); got != want {
		t.Errorf("unified widths %+v, want %+v", got, want)
	}

	l.split = true

	if got, want := l.codeWidths(), (codeWidths{line: l.unifiedCode(2), full: l.unifiedCode(1), left: l.splitCode(), right: l.splitRightCode()}); got != want {
		t.Errorf("split widths %+v, want %+v", got, want)
	}
}

// fitScene is a wrapScene whose diff column is 20 rows tall and 40 cells wide.
func fitScene(t *testing.T, res git.Result) (*App, *reviewView) {
	t.Helper()

	for sh := 20; sh <= 40; sh++ {
		a, v := wrapScene(t, res, codeWidths{line: 40}, sh)
		if a.reviewBodyH(v) == 20 {
			return a, v
		}
	}

	t.Fatal("no scene height gives a diff column of 20 rows")

	return nil, nil
}

// wheelWalk turns the wheel until the drawn top stops at an end. Every notch
// moves the drawn top by 3 rows (less at an end) and leaves the cursor on a
// line with a row in view.
func wheelWalk(t *testing.T, a *App, v *reviewView, dir int) {
	t.Helper()

	x, y := wheelCell(t, a)
	btn := tea.MouseWheelDown
	end := max(len(v.rows)-a.reviewBodyH(v), 0)

	if dir < 0 {
		btn, end = tea.MouseWheelUp, 0
	}

	h := a.reviewBodyH(v)

	for n := 0; v.viewTop(h) != end; n++ {
		if n > len(v.rows) {
			t.Fatalf("the drawn top stuck at %d, want %d", v.viewTop(h), end)
		}

		prev := v.viewTop(h)
		want := min(max(prev+dir*wheelRows, 0), max(len(v.rows)-h, 0))

		wheelAt(a, x, y, btn)

		if got := v.viewTop(h); got != want {
			t.Fatalf("notch %d from %d: drawn top %d, want %d", n, prev, got, want)
		}

		top := v.viewTop(h)
		if v.lineEnd(v.cursor) <= top || v.lineStart(v.cursor) >= top+h {
			t.Fatalf("notch %d: cursor %d has no row in view (top %d)", n, v.cursor, top)
		}
	}
}

func repeated(n, width int) []string {
	out := make([]string, n)
	for i := range out {
		out[i] = " " + strings.Repeat(string(rune('a'+i%26)), width)
	}

	return out
}

func TestWrappedWheelPastFittingLines(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name  string
		width int
		toEnd bool
	}{
		{"4 rows", 130, false},
		{"5 rows", 190, false},
		{"4 rows after G", 130, true},
		{"5 rows after G", 190, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			a, v := fitScene(t, padded(0, repeated(12, tc.width)...))

			if tc.toEnd {
				press(a, keyRune('G'))
			} else {
				wheelWalk(t, a, v, 1)
			}

			wheelWalk(t, a, v, -1)

			if tc.toEnd {
				wheelWalk(t, a, v, 1)
			}
		})
	}
}

func TestWrappedEdgeScrollSteps(t *testing.T) {
	t.Parallel()

	a, v := fitScene(t, padded(0, repeated(20, 70)...))
	h := a.reviewBodyH(v)
	maxTop := len(v.rows) - h

	for _, dir := range []int{1, -1} {
		if dir < 0 {
			a.scrollRows(v, maxTop)
		}

		goal := map[int]int{1: maxTop, -1: 0}[dir]

		for n := 0; v.viewTop(h) != goal; n++ {
			if n > 2*len(v.rows) {
				t.Fatalf("step %d: drawn top stuck at %d, want %d", dir, v.viewTop(h), goal)
			}

			prev := v.viewTop(h)

			a.scrollRows(v, dir)

			if got := v.viewTop(h); got != prev+dir {
				t.Fatalf("step %d from %d: drawn top %d", dir, prev, got)
			}
		}
	}
}

func TestWrappedKeyMoveShowsWholeLine(t *testing.T) {
	t.Parallel()

	a, v := fitScene(t, padded(0, repeated(12, 130)...))
	h := a.reviewBodyH(v)

	for v.lineEnd(v.cursor) < len(v.rows) {
		press(a, keyRune('j'))

		top := v.viewTop(h)
		if v.lineStart(v.cursor) < top || v.lineEnd(v.cursor) > top+h {
			t.Fatalf("j to %d: line %d-%d not whole in view (top %d)",
				v.cursor, v.lineStart(v.cursor), v.lineEnd(v.cursor), top)
		}
	}
}

func TestWrappedWheelOverTallLine(t *testing.T) {
	t.Parallel()

	tall := " " + strings.Repeat("t", 1024)

	for _, tc := range []struct {
		name string
		rows []string
	}{
		{"in the middle", append(append(manyPads(3), tall), manyShort(25)...)},
		{"last line", append(manyPads(3), tall)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			a, v := fitScene(t, padded(0, tc.rows...))

			press(a, keyRune('G'))
			wheelWalk(t, a, v, -1)
			wheelWalk(t, a, v, 1)
			wheelWalk(t, a, v, -1)
		})
	}
}

func TestStickyHeaderStandsDownOnTheCursorsLine(t *testing.T) {
	t.Parallel()

	tall := " " + strings.Repeat("t", 1024)
	a, v := fitScene(t, padded(0, append(append(manyPads(3), tall), manyShort(25)...)...))

	// Rows: file 0, hunk 1, pads 2-4, the tall line 5-30, short lines 31-.
	for _, tc := range []struct {
		name      string
		cursor    int
		wantStick bool
	}{
		{"cursor on the line of the top row", 5, false},
		{"cursor on another line", 31, true},
	} {
		v.cursor, v.top = tc.cursor, 15

		d, _, ok := a.drawCtxFor(a.store.State())
		if !ok || d.top != 15 {
			t.Fatalf("%s: setup: ok %v, top %d, want 15", tc.name, ok, d.top)
		}

		if _, got := d.stickyHeader(); got != tc.wantStick {
			t.Errorf("%s: sticky header = %v, want %v", tc.name, got, tc.wantStick)
		}
	}
}
