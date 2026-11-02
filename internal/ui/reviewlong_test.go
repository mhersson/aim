package ui

import (
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/charmbracelet/x/ansi"

	"github.com/mhersson/aim/internal/git"
)

// marks is n combining acute accents: they take no cell, and cleanText leaves them alone.
func marks(n int) string { return strings.Repeat("\xcc\x81", n) }

// longLinesResult has lines past the budget whose kept part is narrower than
// any column, one just inside the budget, one wider than a column and one past
// the budget that is wider than a column too.
func longLinesResult() git.Result {
	f := fileSpec("long.txt", git.Modified, hunkSpec(1, 1, "",
		"+Z"+marks(5000),
		"+short",
		"-Q"+marks(5000),
		"+Q"+marks(5000)+"!",
		"+K"+marks(lineBudget-2),
		"+"+strings.Repeat("w", 300),
		"+"+strings.Repeat("c", 1500),
	))

	return git.Result{Turn: 1, Files: []git.FileDiff{f}}
}

func longLinesFull() fullText {
	return newFullText(
		[]string{"head", "Q" + marks(5000), "tail"},
		[]string{"head", "Z" + marks(5000), "K" + marks(lineBudget-2), strings.Repeat("w", 300), strings.Repeat("c", 1500), "tail"},
		nil,
	)
}

// diffEnd is a drawn row without the drawer's right border and the padding.
func diffEnd(row string) string {
	return strings.TrimRight(strings.TrimSuffix(strings.TrimRight(row, " "), "│"), " ")
}

// A line past the column wraps and keeps every row but the last free of the cut
// marker. The marker ends the last row of a line past the budget, even when the
// part kept is narrower than the column, and ends no other row.
func TestDrawnLongLinesWrap(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		split bool
		full  bool
		// the rows to find, the cut markers each must hold
		rows map[string]int
	}{
		{"unified", false, false, map[string]int{"Z\xcc\x81": 1, "Q\xcc\x81": 1, "K\xcc\x81": 0, "short": 0, "wwwwww": 0}},
		{"split", true, false, map[string]int{"Z\xcc\x81": 1, "Q\xcc\x81": 2, "K\xcc\x81": 0, "short": 0, "wwwwww": 0}},
		{"full file", false, true, map[string]int{"Z\xcc\x81": 1, "Q\xcc\x81": 1, "K\xcc\x81": 0, "tail": 0, "wwwwww": 0}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := reviewScene{theme: "catppuccin-mocha", icons: true, w: 240, h: 40, res: longLinesResult(), turn: 1, turns: 1, focused: true}
			s.tune = func(t *testing.T, a *App, v *reviewView) {
				v.split = tt.split

				if tt.full {
					v.full = map[string]fullText{"long.txt": longLinesFull()}
					v.fullOn = allWhole(v.full)
					v.rebuild(false)
				}
			}
			a := s.build(t)

			st := a.store.State()
			r, _ := a.drawerRect(st)
			rows := a.drawerLines(st, r)

			v := a.reviewViewOf(st.Tab().ID)
			if v.rowsSplit != tt.split {
				t.Fatalf("rows are split = %v, want %v", v.rowsSplit, tt.split)
			}

			for start, want := range tt.rows {
				found := false

				for _, row := range rows {
					plain := ansi.Strip(row)
					if !strings.Contains(plain, start) {
						continue
					}

					found = true

					if got := strings.Count(diffEnd(plain), "…"); got != want {
						t.Errorf("the row of %q holds %d cut markers, want %d: %q", start, got, want, plain)
					}

					if want > 0 && !strings.HasSuffix(diffEnd(plain), "…") {
						t.Errorf("the row of %q does not end in the cut marker: %q", start, plain)
					}
				}

				if !found {
					t.Errorf("no row holds %q", start)
				}
			}

			var cut []bool

			for _, row := range rows {
				if plain := ansi.Strip(row); strings.Contains(plain, "cccccccc") {
					cut = append(cut, strings.HasSuffix(diffEnd(plain), "…"))
				}
			}

			if len(cut) < 2 {
				t.Fatalf("the line past the budget takes %d rows, want several", len(cut))
			}

			for i, got := range cut {
				if want := i == len(cut)-1; got != want {
					t.Errorf("row %d of %d of the line past the budget: ends in the cut marker = %v, want %v", i, len(cut), got, want)
				}
			}
		})
	}
}

// What the highlighter is handed is bounded, whatever the length of the line.
func TestChunkLinesAreBounded(t *testing.T) {
	t.Parallel()

	// An escape is cleaned into a tag of 8 runes, so a line of them just past the budget is
	// longer than 8*lineBudget runes unless it is cut first.
	long := longLines{plain: pastBudgetLines().plain, tags: strings.Repeat("\x1b", lineBudget+16)}
	f := fileSpec("long.go", git.Modified, hunkSpec(1, 1, "",
		"+"+long.plain,
		"+"+long.tags,
		"+"+long.plain+"\r",
		"-"+long.tags,
	))
	res := git.Result{Turn: 1, Files: []git.FileDiff{f}}

	for _, tt := range []struct {
		name string
		key  spanKey
		full fullText
	}{
		{"new side of a hunk", spanKey{file: 0, hunk: 0, side: sideNew}, fullText{}},
		{"old side of a hunk", spanKey{file: 0, hunk: 0, side: sideOld}, fullText{}},
		{"a whole file", spanKey{file: 0, hunk: -1, side: sideNew}, fullText{new: []string{long.plain, long.tags, long.plain + "\r"}}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			v := &reviewView{res: res, full: map[string]fullText{"long.go": tt.full}}

			path, lines := v.chunkLines(tt.key)
			if path != "long.go" || len(lines) == 0 {
				t.Fatalf("chunkLines = %q, %d lines", path, len(lines))
			}

			for i, l := range lines {
				if n := utf8.RuneCountInString(l); n > 8*lineBudget+1 {
					t.Errorf("line %d has %d runes, want at most %d", i, n, 8*lineBudget+1)
				}
			}

			if tt.key.side == sideNew && utf8.RuneCountInString(lines[0]) > lineBudget+1 {
				t.Errorf("a plain line has %d runes, want at most %d", utf8.RuneCountInString(lines[0]), lineBudget+1)
			}
		})
	}
}
