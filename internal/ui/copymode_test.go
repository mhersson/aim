package ui

import (
	"fmt"
	"runtime"
	"strings"
	"testing"
)

func copyTestGrid() *fakeGrid {
	return newFakeGrid(12, nil,
		"foo bar_baz", // 0
		"  qux ((x)",  // 1
		"",            // 2
		"中文 a",        // 3
		"  tail text", // 4
		"foo again",   // 5
		"last",        // 6
		"",            // 7
	)
}

func pressKeys(t *testing.T, g grid, m copyMode, viewRows int, keys ...string) copyMode {
	t.Helper()

	for _, k := range keys {
		m, _ = m.key(g, k, viewRows)
	}

	return m
}

func TestCopyModeStart(t *testing.T) {
	t.Parallel()

	g := copyTestGrid()

	tests := []struct {
		name          string
		viewTop, rows int
		want          point
	}{
		{"bottom of view", 2, 3, point{4, 0}},
		{"view past the grid end", 6, 5, point{7, 0}},
		{"top of grid", 0, 1, point{0, 0}},
	}
	for _, tt := range tests {
		m := newCopyMode(g, tt.viewTop, tt.rows)
		if m.cursor != tt.want || m.top != tt.viewTop {
			t.Errorf("%s: cursor %v top %d, want %v top %d", tt.name, m.cursor, m.top, tt.want, tt.viewTop)
		}
	}
}

func TestCopyModeMotions(t *testing.T) {
	t.Parallel()

	g := copyTestGrid()

	tests := []struct {
		name  string
		start point
		keys  []string
		want  point
	}{
		{"h", point{0, 3}, []string{"h"}, point{0, 2}},
		{"h at left edge", point{0, 0}, []string{"h"}, point{0, 0}},
		{"h onto continuation lands on lead", point{3, 4}, []string{"h", "h"}, point{3, 0}},
		{"l", point{0, 0}, []string{"l"}, point{0, 1}},
		{"l over a wide char", point{3, 0}, []string{"l"}, point{3, 2}},
		{"l stays within the row", point{0, 11}, []string{"l"}, point{0, 11}},
		{"j keeps column", point{0, 5}, []string{"j"}, point{1, 5}},
		{"j onto wide continuation moves to lead", point{2, 1}, []string{"j"}, point{3, 0}},
		{"k keeps column", point{1, 5}, []string{"k"}, point{0, 5}},
		{"j at last row", point{7, 3}, []string{"j"}, point{7, 3}},
		{"k at first row", point{0, 3}, []string{"k"}, point{0, 3}},
		{"0", point{1, 6}, []string{"0"}, point{1, 0}},
		{"^", point{1, 6}, []string{"^"}, point{1, 2}},
		{"^ on blank row", point{2, 4}, []string{"^"}, point{2, 0}},
		{"$", point{1, 0}, []string{"$"}, point{1, 9}},
		{"$ on blank row", point{2, 4}, []string{"$"}, point{2, 0}},
		{"G lands on first non-blank", point{0, 0}, []string{"G"}, point{7, 0}},
		{"g needs two keys", point{4, 5}, []string{"g"}, point{4, 5}},
		{"g g", point{4, 5}, []string{"g", "g"}, point{0, 0}},
		{"g then other key is handled as itself", point{0, 0}, []string{"g", "j"}, point{1, 0}},
		{"g j g still needs two g", point{4, 5}, []string{"g", "j", "g"}, point{5, 5}},
		{"w within row", point{0, 0}, []string{"w"}, point{0, 4}},
		{"w word to other run", point{1, 2}, []string{"w"}, point{1, 6}},
		{"w other run to word", point{1, 6}, []string{"w"}, point{1, 8}},
		{"w crosses rows over a blank row", point{1, 9}, []string{"w"}, point{3, 0}},
		{"w over wide char", point{3, 0}, []string{"w"}, point{3, 5}},
		{"w from blank", point{0, 3}, []string{"w"}, point{0, 4}},
		{"w at end settles on the last run", point{6, 0}, []string{"w"}, point{6, 3}},
		{"e to end of current run", point{0, 0}, []string{"e"}, point{0, 2}},
		{"e from run end goes to next run end", point{0, 2}, []string{"e"}, point{0, 10}},
		{"e crosses rows", point{0, 10}, []string{"e"}, point{1, 4}},
		{"e at the end stays", point{6, 3}, []string{"e"}, point{6, 3}},
		{"b to start of current run", point{0, 6}, []string{"b"}, point{0, 4}},
		{"b from run start goes to previous", point{0, 4}, []string{"b"}, point{0, 0}},
		{"b crosses rows", point{1, 2}, []string{"b"}, point{0, 4}},
		{"b crosses a blank row to a wide char", point{4, 2}, []string{"b"}, point{3, 5}},
		{"b at the start stays", point{0, 0}, []string{"b"}, point{0, 0}},
		{"b lands on wide lead", point{3, 5}, []string{"b"}, point{3, 0}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := copyMode{cursor: tt.start}

			got := pressKeys(t, g, m, 8, tt.keys...)
			if got.cursor != tt.want {
				t.Errorf("cursor %v, want %v", got.cursor, tt.want)
			}
		})
	}
}

func TestCopyModeViewJumps(t *testing.T) {
	t.Parallel()

	g := newFakeGrid(8, nil,
		"r0", "r1", "r2", "r3", "r4", "r5", "r6", "r7", "r8", "r9",
		" r10", "r11",
	)

	tests := []struct {
		name     string
		top      int
		cursor   point
		viewRows int
		key      string
		want     point
		wantTop  int
	}{
		{"H", 2, point{5, 1}, 5, "H", point{2, 0}, 2},
		{"M", 2, point{5, 1}, 5, "M", point{4, 0}, 2},
		{"L", 2, point{3, 1}, 5, "L", point{6, 0}, 2},
		{"M even view", 0, point{0, 0}, 4, "M", point{1, 0}, 0},
		{"L clamped to grid", 9, point{9, 0}, 5, "L", point{11, 0}, 9},
		{"L lands on first non-blank", 7, point{7, 0}, 4, "L", point{10, 1}, 7},
		{"ctrl+d", 2, point{4, 1}, 6, "ctrl+d", point{7, 1}, 5},
		{"ctrl+u", 4, point{6, 1}, 6, "ctrl+u", point{3, 1}, 1},
		{"ctrl+f", 0, point{2, 1}, 4, "ctrl+f", point{6, 1}, 4},
		{"ctrl+b", 6, point{8, 1}, 4, "ctrl+b", point{4, 1}, 2},
		{"ctrl+u at top", 0, point{1, 1}, 6, "ctrl+u", point{0, 1}, 0},
		{"ctrl+b at top", 1, point{3, 1}, 4, "ctrl+b", point{0, 1}, 0},
		{"ctrl+d at bottom", 6, point{10, 1}, 6, "ctrl+d", point{11, 1}, 6},
		{"ctrl+f at bottom", 7, point{10, 1}, 5, "ctrl+f", point{11, 1}, 7},
		{"G", 0, point{0, 0}, 5, "G", point{11, 0}, 7},
		{"j scrolls minimally", 0, point{4, 0}, 5, "j", point{5, 0}, 1},
		{"k scrolls minimally", 3, point{3, 0}, 5, "k", point{2, 0}, 2},
		{"j inside view keeps top", 0, point{2, 0}, 5, "j", point{3, 0}, 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := copyMode{cursor: tt.cursor, top: tt.top}

			got, act := m.key(g, tt.key, tt.viewRows)
			if act != copyNone || got.cursor != tt.want || got.top != tt.wantTop {
				t.Errorf("cursor %v top %d act %v, want %v top %d", got.cursor, got.top, act, tt.want, tt.wantTop)
			}
		})
	}
}

func TestCopyModeTopStaysConsistent(t *testing.T) {
	t.Parallel()

	// A one-row grid in a one-row view, a view taller than the grid, and a grid
	// taller than the view by a lot and by a little.
	for _, size := range []struct{ rows, viewRows int }{{1, 1}, {3, 12}, {20, 1}, {20, 4}} {
		rows, viewRows := size.rows, size.viewRows

		g := newFakeGrid(6, nil)
		for i := range rows {
			g.cells = append(g.cells, newFakeGrid(6, nil, fmt.Sprintf("r%d", i)).cells[0])
		}

		for _, start := range []int{0, rows - 1} {
			for _, keys := range [][]string{
				{"ctrl+u"},
				{"ctrl+d"},
				{"ctrl+b"},
				{"ctrl+f"},
				{"G"},
				{"g", "g"},
			} {
				m := copyMode{cursor: point{start, 0}, top: min(start, max(0, rows-viewRows))}
				m = pressKeys(t, g, m, viewRows, keys...)
				maxTop := max(0, rows-viewRows)

				c := m.cursor
				switch {
				case c.row < 0 || c.row >= rows:
					t.Errorf("rows=%d view=%d start=%d %v: cursor %v out of grid", rows, viewRows, start, keys, c)
				case m.top < 0 || m.top > maxTop:
					t.Errorf("rows=%d view=%d start=%d %v: top %d out of [0,%d]", rows, viewRows, start, keys, m.top, maxTop)
				case c.row < m.top || c.row > m.top+viewRows-1:
					t.Errorf("rows=%d view=%d start=%d %v: cursor %v outside view top=%d", rows, viewRows, start, keys, c, m.top)
				}
			}
		}
	}
}

func TestCopyModeSelection(t *testing.T) {
	t.Parallel()

	g := copyTestGrid()

	tests := []struct {
		name string
		keys []string
		want *selection
	}{
		{"v follows the cursor", []string{"v", "l", "l"}, &selection{selStream, point{0, 0}, point{0, 2}}},
		{"V starts a line selection", []string{"V", "j"}, &selection{selLine, point{0, 0}, point{1, 0}}},
		{"ctrl+v starts a block selection", []string{"ctrl+v", "j", "l"}, &selection{selBlock, point{0, 0}, point{1, 1}}},
		{"same key clears", []string{"v", "l", "v"}, nil},
		{"other key switches mode and keeps anchor", []string{"l", "v", "j", "V"}, &selection{selLine, point{0, 1}, point{1, 1}}},
		{"word motions move the head", []string{"v", "w"}, &selection{selStream, point{0, 0}, point{0, 4}}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := pressKeys(t, g, copyMode{}, 8, tt.keys...)
			switch {
			case tt.want == nil && got.sel != nil:
				t.Errorf("sel %+v, want nil", *got.sel)
			case tt.want != nil && (got.sel == nil || *got.sel != *tt.want):
				t.Errorf("sel %v, want %+v", got.sel, *tt.want)
			}
		})
	}
}

func TestCopyModeUnknownKey(t *testing.T) {
	t.Parallel()

	g := copyTestGrid()

	m := copyMode{cursor: point{1, 3}, top: 1}

	got, act := m.key(g, "F13", 4)
	if act != copyNone || got != m {
		t.Errorf("unknown key: action %v state %+v, want none and unchanged", act, got)
	}
}

func TestCopyModeSearch(t *testing.T) {
	t.Parallel()

	g := newFakeGrid(12, nil, "foo bar", "中foo", "xx foo", "none")

	tests := []struct {
		name     string
		start    point
		query    string
		backward bool
		want     point
		found    bool
	}{
		{"forward same row", point{0, 0}, "ba", false, point{0, 4}, true},
		{"forward strictly after cursor", point{0, 0}, "foo", false, point{1, 2}, true},
		{"forward wide char column map", point{0, 0}, "中f", false, point{1, 0}, true},
		{"forward wraps", point{2, 3}, "foo", false, point{0, 0}, true},
		{"forward wraps onto itself", point{0, 0}, "bar", false, point{0, 4}, true},
		{"backward strictly before cursor", point{2, 3}, "foo", true, point{1, 2}, true},
		{"backward same row", point{0, 6}, "o", true, point{0, 2}, true},
		{"backward wraps", point{0, 0}, "foo", true, point{2, 3}, true},
		{"case sensitive", point{0, 0}, "FOO", false, point{0, 0}, false},
		{"no match leaves cursor", point{1, 1}, "zzz", false, point{1, 1}, false},
		{"empty query", point{1, 1}, "", false, point{1, 1}, false},
		{"match spanning a space", point{3, 0}, "o bar", false, point{0, 2}, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := copyMode{cursor: tt.start}

			got, ok := m.search(g, tt.query, tt.backward)
			if ok != tt.found || got.cursor != tt.want {
				t.Errorf("cursor %v found %v, want %v %v", got.cursor, ok, tt.want, tt.found)
			}

			if ok && (got.lastSearch != tt.query || got.backward != tt.backward) {
				t.Errorf("lastSearch %q backward %v", got.lastSearch, got.backward)
			}

			if !ok && got.lastSearch != "" {
				t.Errorf("failed search stored %q", got.lastSearch)
			}
		})
	}
}

func TestCopyModeRepeatSearch(t *testing.T) {
	t.Parallel()

	g := newFakeGrid(12, nil, "foo", "bar", "foo x", "baz", "foo")

	m, ok := (copyMode{}).search(g, "foo", false)
	if !ok || m.cursor != (point{2, 0}) {
		t.Fatalf("first search: %v %v", m.cursor, ok)
	}

	tests := []struct {
		key      string
		want     point
		backward bool
	}{
		{"n", point{4, 0}, false},
		{"n", point{0, 0}, false},
		{"N", point{4, 0}, false},
		{"N", point{2, 0}, false},
	}
	for i, tt := range tests {
		m, _ = m.key(g, tt.key, 2)
		if m.cursor != tt.want || m.backward != tt.backward {
			t.Fatalf("step %d %s: cursor %v backward %v, want %v", i, tt.key, m.cursor, m.backward, tt.want)
		}

		if m.cursor.row < m.top || m.cursor.row > m.top+1 {
			t.Fatalf("step %d: cursor %v outside view top %d", i, m.cursor, m.top)
		}
	}

	back, _ := (copyMode{cursor: point{4, 0}}).search(g, "foo", true)
	if back.cursor != (point{2, 0}) {
		t.Fatalf("backward search: %v", back.cursor)
	}

	n, _ := back.key(g, "n", 5)
	if n.cursor != (point{0, 0}) || !n.backward {
		t.Errorf("n after backward search: %v backward %v", n.cursor, n.backward)
	}

	nn, _ := back.key(g, "N", 5)
	if nn.cursor != (point{4, 0}) || !nn.backward {
		t.Errorf("N after backward search: %v backward %v", nn.cursor, nn.backward)
	}

	fresh := copyMode{cursor: point{1, 1}}
	if got, act := fresh.key(g, "n", 3); got != fresh || act != copyNone {
		t.Errorf("n without a search changed state")
	}
}

func TestCopyModeSearchMovesSelectionHead(t *testing.T) {
	t.Parallel()

	g := newFakeGrid(8, nil, "ab", "cd ab")
	m := pressKeys(t, g, copyMode{}, 4, "v")

	m, ok := m.search(g, "ab", false)
	if !ok || m.sel == nil || m.sel.head != (point{1, 3}) || m.sel.anchor != (point{0, 0}) {
		t.Errorf("sel %+v ok %v", m.sel, ok)
	}
}

// A search that finds nothing over a full history must not decode the history
// into memory: it blocks the update loop while it holds the pane lock.
func TestSearchOverCappedHistoryIsCheap(t *testing.T) {
	p := cappedHistoryPaneOf(t, 44, 30)

	var (
		found      bool
		allocBytes uint64
		rows       int
	)

	p.withGrid(func(g grid) {
		rows = g.Rows()

		var before, after runtime.MemStats

		runtime.GC()
		runtime.ReadMemStats(&before)

		_, found = copyMode{cursor: point{row: rows - 1}}.search(g, "zzz", false)

		runtime.ReadMemStats(&after)
		allocBytes = after.TotalAlloc - before.TotalAlloc
	})

	t.Logf("search over %d rows: %d KB allocated", rows, allocBytes>>10)

	if found {
		t.Fatal("found a match that is not there")
	}

	if rows < 10000 {
		t.Fatalf("setup: only %d rows", rows)
	}

	const repeats = 3

	var perSearch uint64

	p.withGrid(func(g grid) {
		var before, after runtime.MemStats

		runtime.GC()
		runtime.ReadMemStats(&before)

		for range repeats {
			copyMode{cursor: point{row: rows - 1}}.search(g, "zzz", false)
		}

		runtime.ReadMemStats(&after)
		perSearch = (after.TotalAlloc - before.TotalAlloc) / repeats
	})

	if perSearch > 1<<20 {
		t.Errorf("a repeated search allocated %d B, want under 1 MB", perSearch)
	}

	if allocBytes > 16<<20 {
		t.Errorf("search allocated %d MB over a capped history, want under 16", allocBytes>>20)
	}
}

// plainGrid hides a grid's text scanner, so the cell path runs.
type plainGrid struct{ grid }

// The text path over history gives the same matches and text as the cell path.
func TestHistoryTextPathMatchesCellPath(t *testing.T) {
	t.Parallel()
	a, procs := framedApp(t, 40, 8, shellSpec("/w/a", ""))
	p, fp := a.panes["p1"], procs["p1"]

	var sb strings.Builder

	for i := range 30 {
		switch i % 4 {
		case 0:
			sb.WriteString("中文 needle \x1b[1mbold\x1b[0m needle\r\n")
		case 1:
			sb.WriteString("\r\n")
		case 2:
			sb.WriteString("a中b needle" + strings.Repeat("x", 45) + "\r\n")
		default:
			sb.WriteString("plain " + lineName(i) + "\r\n")
		}
	}

	feedAndWait(t, p, fp, sb.String())
	eventually(t, "history", func() bool { return p.histLenLocked() > 20 })

	p.withGrid(func(g grid) {
		if _, ok := g.(textScanner); !ok {
			t.Fatal("pane grid is not a text scanner")
		}

		for _, backward := range []bool{false, true} {
			for _, cursor := range []point{{0, 0}, {2, 3}, {10, 8}, {g.Rows() - 1, 0}} {
				m := copyMode{cursor: cursor}
				got, gok := m.search(g, "needle", backward)
				want, wok := m.search(plainGrid{g}, "needle", backward)

				if !gok {
					t.Errorf("search from %v backward=%v found nothing", cursor, backward)
				}

				if gok != wok || got.cursor != want.cursor {
					t.Errorf("search from %v backward=%v: text %v %v, cells %v %v", cursor, backward, got.cursor, gok, want.cursor, wok)
				}
			}
		}

		sel := selection{mode: selStream, anchor: point{0, 1}, head: point{g.Rows() - 1, 5}}
		if got, want := extract(g, sel), extract(plainGrid{g}, sel); got != want {
			t.Errorf("extract text path = %q, cell path = %q", got, want)
		}
	})
}

// cappedHistoryPane is a 200-column pane whose history holds the 10000-line cap.
func cappedHistoryPane(t *testing.T) *pane { return cappedHistoryPaneOf(t, 202, 180) }

// cappedHistoryPaneOf is a pane of width columns plus its border, whose lines
// carry pad characters, with its history at the 10000-line cap.
func cappedHistoryPaneOf(t *testing.T, width, pad int) *pane {
	t.Helper()

	a, procs := framedApp(t, width, 14, shellSpec("/w/a", ""))
	p, fp := a.panes["p1"], procs["p1"]

	feedPaddedLines(t, p, fp, 0, 10100, strings.Repeat("x", pad))

	return p
}
