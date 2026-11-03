package ui

import (
	"testing"

	uv "github.com/charmbracelet/ultraviolet"
)

type fakeGrid struct {
	cols    int
	cells   [][]uv.Cell
	wrapped map[int]bool
}

// newFakeGrid builds a grid from row strings padded with spaces to cols. Runes
// at or above U+2E80 are stored as wide cells followed by a continuation cell.
func newFakeGrid(cols int, wrapped []int, rows ...string) *fakeGrid {
	g := &fakeGrid{cols: cols, wrapped: map[int]bool{}}
	for _, r := range wrapped {
		g.wrapped[r] = true
	}

	for _, text := range rows {
		row := make([]uv.Cell, 0, cols)

		for _, r := range text {
			if r >= 0x2E80 {
				row = append(row, uv.Cell{Content: string(r), Width: 2}, uv.Cell{Width: 0})
			} else {
				row = append(row, uv.Cell{Content: string(r), Width: 1})
			}
		}

		for len(row) < cols {
			row = append(row, uv.Cell{Content: " ", Width: 1})
		}

		g.cells = append(g.cells, row)
	}

	return g
}

func (g *fakeGrid) Rows() int { return len(g.cells) }
func (g *fakeGrid) Cols() int { return g.cols }
func (g *fakeGrid) Cell(row, col int) *uv.Cell {
	if row < 0 || row >= len(g.cells) || col < 0 || col >= len(g.cells[row]) {
		return nil
	}

	return &g.cells[row][col]
}
func (g *fakeGrid) Wrapped(row int) bool { return g.wrapped[row] }

func TestExtract(t *testing.T) {
	t.Parallel()

	plain := newFakeGrid(10, nil, "alpha one", "beta  two", "gamma   ", "delta")
	wide := newFakeGrid(10, nil, "a中b文c")
	wrapped := newFakeGrid(6, []int{0, 1}, "hello ", "world ", "end", "next")
	holes := newFakeGrid(6, nil, "ab")
	holes.cells[0][1] = uv.Cell{}

	tests := []struct {
		name string
		g    grid
		sel  selection
		want string
	}{
		{"stream across three rows", plain, selection{selStream, point{0, 6}, point{2, 2}}, "one\nbeta  two\ngam"},
		{"stream single row", plain, selection{selStream, point{0, 0}, point{0, 4}}, "alpha"},
		{"reversed anchor and head", plain, selection{selStream, point{2, 2}, point{0, 6}}, "one\nbeta  two\ngam"},
		{"line mode", plain, selection{selLine, point{0, 3}, point{1, 1}}, "alpha one\nbeta  two"},
		{"block mode", plain, selection{selBlock, point{0, 6}, point{2, 8}}, "one\ntwo\n"},
		{"block mode reversed", plain, selection{selBlock, point{2, 8}, point{0, 6}}, "one\ntwo\n"},
		{"trailing blanks trimmed at hard line end", plain, selection{selStream, point{2, 0}, point{3, 4}}, "gamma\ndelta"},
		{"trailing blanks trimmed at selection end", plain, selection{selStream, point{0, 0}, point{0, 9}}, "alpha one"},
		{"wide char once from its first column", wide, selection{selStream, point{0, 0}, point{0, 3}}, "a中b"},
		{"wide char once when starting on continuation", wide, selection{selStream, point{0, 2}, point{0, 3}}, "中b"},
		{"wide char once when ending on first column", wide, selection{selStream, point{0, 0}, point{0, 1}}, "a中"},
		{"wide char both columns", wide, selection{selStream, point{0, 1}, point{0, 2}}, "中"},
		{"soft wraps join without newline or trim", wrapped, selection{selStream, point{0, 0}, point{2, 2}}, "hello world end"},
		{"hard row after wrapped line", wrapped, selection{selStream, point{0, 0}, point{3, 3}}, "hello world end\nnext"},
		{"block mode ignores soft wraps", wrapped, selection{selBlock, point{0, 0}, point{1, 4}}, "hello\nworld"},
		{"nil cell and empty content read as space", holes, selection{selStream, point{0, 0}, point{0, 3}}, "a"},
		{"selection past rows is clamped", plain, selection{selStream, point{2, 0}, point{99, 99}}, "gamma\ndelta"},
		{"selection before rows is clamped", plain, selection{selStream, point{-5, 0}, point{0, 4}}, "alpha"},
		{"columns past the grid are clamped", plain, selection{selStream, point{3, 0}, point{3, 99}}, "delta"},
		{"negative columns are clamped", plain, selection{selStream, point{3, -4}, point{3, 2}}, "del"},
		{"entirely outside yields nothing", plain, selection{selStream, point{40, 0}, point{50, 3}}, ""},
		{"empty grid", newFakeGrid(5, nil), selection{selStream, point{0, 0}, point{3, 3}}, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := extract(tt.g, tt.sel); got != tt.want {
				t.Errorf("extract = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestRowSpan(t *testing.T) {
	t.Parallel()

	const cols = 10

	tests := []struct {
		name     string
		sel      selection
		row      int
		from, to int
		ok       bool
	}{
		{"stream first row", selection{selStream, point{1, 4}, point{3, 2}}, 1, 4, 9, true},
		{"stream middle row", selection{selStream, point{1, 4}, point{3, 2}}, 2, 0, 9, true},
		{"stream last row", selection{selStream, point{1, 4}, point{3, 2}}, 3, 0, 2, true},
		{"stream single row", selection{selStream, point{1, 4}, point{1, 6}}, 1, 4, 6, true},
		{"stream reversed", selection{selStream, point{3, 2}, point{1, 4}}, 1, 4, 9, true},
		{"line mode", selection{selLine, point{1, 4}, point{2, 2}}, 2, 0, 9, true},
		{"block mode", selection{selBlock, point{1, 6}, point{3, 2}}, 2, 2, 6, true},
		{"above", selection{selStream, point{1, 4}, point{3, 2}}, 0, 0, 0, false},
		{"below", selection{selStream, point{1, 4}, point{3, 2}}, 4, 0, 0, false},
		{"start past the columns", selection{selStream, point{1, 40}, point{3, 2}}, 1, 0, 0, false},
		{"end clamped to the columns", selection{selStream, point{1, 4}, point{1, 40}}, 1, 4, 9, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			from, to, ok := tt.sel.rowSpan(tt.row, cols)
			if ok != tt.ok || (ok && (from != tt.from || to != tt.to)) {
				t.Fatalf("rowSpan = (%d, %d, %v), want (%d, %d, %v)", from, to, ok, tt.from, tt.to, tt.ok)
			}
		})
	}

	if _, _, ok := (selection{}).rowSpan(0, 0); ok {
		t.Error("rowSpan with zero columns reported ok")
	}
}

func TestWordAt(t *testing.T) {
	t.Parallel()

	g := newFakeGrid(20, nil, "see ~/src/a-b.go now", "", "", "", "", "中文 x", "")
	gw := newFakeGrid(13, []int{2}, "", "", "/home/user/pr", "oject/file.go", "")

	tests := []struct {
		name     string
		p        point
		wantFrom point
		wantTo   point
	}{
		{"path start", point{0, 4}, point{0, 4}, point{0, 15}},
		{"path middle", point{0, 9}, point{0, 4}, point{0, 15}},
		{"path end", point{0, 15}, point{0, 4}, point{0, 15}},
		{"plain word", point{0, 1}, point{0, 0}, point{0, 2}},
		{"blank selects one cell", point{0, 3}, point{0, 3}, point{0, 3}},
		{"wide word from continuation", point{5, 1}, point{5, 0}, point{5, 3}},
		{"outside is clamped", point{99, 99}, point{6, 19}, point{6, 19}},
		{"negative is clamped", point{-3, -3}, point{0, 0}, point{0, 2}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			sel := wordAt(g, tt.p)
			if sel.mode != selStream {
				t.Errorf("mode = %d, want selStream", sel.mode)
			}

			from, to := sel.ordered()
			if from != tt.wantFrom || to != tt.wantTo {
				t.Errorf("wordAt = %v..%v, want %v..%v", from, to, tt.wantFrom, tt.wantTo)
			}
		})
	}

	if got := extract(g, wordAt(g, point{0, 8})); got != "~/src/a-b.go" {
		t.Errorf("extract(wordAt) = %q", got)
	}

	for _, p := range []point{{2, 3}, {3, 1}} {
		sel := wordAt(gw, p)
		if sel.anchor != (point{2, 0}) || sel.head != (point{3, 12}) {
			t.Errorf("wordAt(%v) across wrap = %+v", p, sel)
		}

		if got := extract(gw, sel); got != "/home/user/project/file.go" {
			t.Errorf("extract(wordAt across wrap) = %q", got)
		}
	}

	if sel := wordAt(newFakeGrid(5, nil), point{3, 3}); sel.anchor != (point{0, 3}) || sel.head != (point{0, 3}) {
		t.Errorf("wordAt on empty grid = %+v", sel)
	}
}

func TestLineAt(t *testing.T) {
	t.Parallel()

	g := newFakeGrid(8, []int{1, 2}, "first", "wrapped1", "wrapped2", "end", "other")

	tests := []struct {
		name         string
		p            point
		wantR1, wrR2 int
	}{
		{"hard line", point{0, 3}, 0, 0},
		{"first row of a wrapped line", point{1, 0}, 1, 3},
		{"middle row", point{2, 5}, 1, 3},
		{"last row", point{3, 1}, 1, 3},
		{"after the wrapped line", point{4, 1}, 4, 4},
		{"outside is clamped", point{99, 99}, 4, 4},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			sel := lineAt(g, tt.p)

			want := selection{selLine, point{tt.wantR1, 0}, point{tt.wrR2, 7}}
			if sel != want {
				t.Errorf("lineAt = %+v, want %+v", sel, want)
			}
		})
	}

	if got := extract(g, lineAt(g, point{2, 0})); got != "wrapped1wrapped2end" {
		t.Errorf("extract(lineAt) = %q", got)
	}
}

func TestClampPointOnAnEmptyGrid(t *testing.T) {
	t.Parallel()

	if got := clampPoint(newFakeGrid(0, nil), point{4, 4}); got != (point{}) {
		t.Errorf("clampPoint on empty grid = %v", got)
	}
}

func TestCharClass(t *testing.T) {
	t.Parallel()

	tests := []struct {
		content string
		want    int
	}{
		{"", classBlank},
		{" ", classBlank},
		{"a", classWord},
		{"7", classWord},
		{"_", classWord},
		{"-", classWord},
		{".", classWord},
		{"/", classWord},
		{"~", classWord},
		{":", classWord},
		{"@", classWord},
		{"中", classWord},
		{"(", classOther},
		{"\t", classOther},
	}
	for _, tt := range tests {
		if got := charClass(tt.content); got != tt.want {
			t.Errorf("charClass(%q) = %d, want %d", tt.content, got, tt.want)
		}
	}
}

func TestSelectionBelow(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		sel  selection
		row  int
		want selection
		ok   bool
	}{
		{"stream start above", selection{selStream, point{2, 5}, point{6, 3}}, 4, selection{selStream, point{4, 0}, point{6, 3}}, true},
		{"stream reversed", selection{selStream, point{6, 3}, point{2, 5}}, 4, selection{selStream, point{4, 0}, point{6, 3}}, true},
		{"stream all below", selection{selStream, point{2, 5}, point{6, 3}}, 1, selection{selStream, point{2, 5}, point{6, 3}}, true},
		{"stream ends above", selection{selStream, point{2, 5}, point{6, 3}}, 7, selection{}, false},
		{"block keeps its column", selection{selBlock, point{2, 5}, point{6, 1}}, 4, selection{selBlock, point{4, 5}, point{6, 1}}, true},
		{"line", selection{selLine, point{2, 0}, point{6, 9}}, 4, selection{selLine, point{4, 0}, point{6, 9}}, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got, ok := tt.sel.below(tt.row)
			if ok != tt.ok || got != tt.want {
				t.Errorf("below(%d) = %+v, %v; want %+v, %v", tt.row, got, ok, tt.want, tt.ok)
			}
		})
	}
}
