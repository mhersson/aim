package ui

import (
	"fmt"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
	"github.com/charmbracelet/x/exp/golden"

	"github.com/mhersson/aim/internal/core"
)

func TestPlanIndicator(t *testing.T) {
	t.Parallel()

	// The word of a state is not compared, only that each state has its own,
	// that it is not a percentage and that every case of a state gives it.
	words := []struct {
		state            string
		lines, rows, top int
	}{
		{"all", 10, 10, 0},
		{"all", 5, 10, 0},
		{"top", 100, 10, 0},
		{"end", 100, 10, 90},
		{"end", 100, 10, 95},
	}

	byState := map[string]string{}

	for _, w := range words {
		got := planIndicator(w.lines, w.rows, w.top)
		if strings.TrimSpace(got) == "" || strings.Contains(got, "%") {
			t.Errorf("planIndicator(%d, %d, %d) = %q, want a word", w.lines, w.rows, w.top, got)
		}

		if prev, ok := byState[w.state]; ok && prev != got {
			t.Errorf("%s: %q and %q", w.state, prev, got)
		}

		byState[w.state] = got
	}

	if len(byState) != 3 || byState["all"] == byState["top"] || byState["top"] == byState["end"] || byState["all"] == byState["end"] {
		t.Errorf("the states share a word: %q", byState)
	}

	percents := []struct {
		lines, rows, top int
		want             string
	}{
		{100, 10, 45, " 50% "},
		{101, 10, 1, " 1% "},
		{200, 31, 1, " 1% "},
		{200, 31, 168, " 99% "},
		{200, 31, 84, " 50% "},
	}

	for _, tt := range percents {
		if got := planIndicator(tt.lines, tt.rows, tt.top); got != tt.want {
			t.Errorf("planIndicator(%d, %d, %d) = %q, want %q", tt.lines, tt.rows, tt.top, got, tt.want)
		}
	}
}

func TestPlanHints(t *testing.T) {
	t.Parallel()

	const (
		line    = "j/k"
		half    = "ctrl+d/u"
		heading = "]/["
		search  = "/"
		open    = "enter"
		closeIt = "esc"
	)

	defaults := keyTable{binds: readerKeys}

	_, tables, err := resolveKeys(contextKeys("reader", "n", "reader.line.next", "p", "reader.line.prev", "ctrl+d", "none"), testRegistry(t))
	if err != nil {
		t.Fatal(err)
	}

	remapped := tables.reader

	// width is what the hints need, from planHints' own pairs and gap.
	full := planHints(defaults, 1000)
	width := func(drop ...string) int {
		n, kept := 0, 0

		for _, p := range full {
			if !slices.Contains(drop, p[1]) {
				n += ansi.StringWidth(p[0]) + 1 + ansi.StringWidth(p[1])
				kept++
			}
		}

		return n + planHintGap*(kept-1)
	}

	tests := []struct {
		width int
		kt    keyTable
		want  []string
	}{
		{100, remapped, []string{"n/p", heading, search, open, closeIt}},
		{34, remapped, []string{"n/p", closeIt}},
		{100, defaults, []string{line, half, heading, search, open, closeIt}},
		{width(), defaults, []string{line, half, heading, search, open, closeIt}},
		{width() - 1, defaults, []string{line, half, heading, open, closeIt}},
		{width("search"), defaults, []string{line, half, heading, open, closeIt}},
		{width("search") - 1, defaults, []string{line, half, open, closeIt}},
		{width("search", "heading"), defaults, []string{line, half, open, closeIt}},
		{width("search", "heading") - 1, defaults, []string{line, open, closeIt}},
		{width("search", "heading", "half page"), defaults, []string{line, open, closeIt}},
		{width("search", "heading", "half page") - 1, defaults, []string{line, closeIt}},
		{5, defaults, []string{line, closeIt}},
	}

	for _, tt := range tests {
		var got []string
		for _, p := range planHints(tt.kt, tt.width) {
			got = append(got, p[0])
		}

		if !slices.Equal(got, tt.want) {
			t.Errorf("planHints(%d) = %q, want %q", tt.width, got, tt.want)
		}
	}
}

func TestPlanRows(t *testing.T) {
	t.Parallel()

	tests := []struct {
		h                int
		rows             int
		blanks, hintsRow bool
	}{
		{36, 31, true, true},
		{8, 3, true, true},
		{7, 4, false, true},
		{6, 3, false, true},
		{5, 3, false, false},
		{4, 2, false, false},
		{3, 1, false, false},
	}

	for _, tt := range tests {
		rows, blanks, hint := planRows(tt.h)
		if rows != tt.rows || blanks != tt.blanks || hint != tt.hintsRow {
			t.Errorf("planRows(%d) = %d, %v, %v; want %d, %v, %v", tt.h, rows, blanks, hint, tt.rows, tt.blanks, tt.hintsRow)
		}
	}
}

func TestPlanBox(t *testing.T) {
	t.Parallel()

	tests := []struct {
		width, height int
		x, y, w, h    int
		ok            bool
	}{
		{120, 40, 5, 2, 110, 36, true},
		{80, 24, 2, 2, 76, 20, true},
		{28, 8, 2, 2, 24, 4, true},
		{27, 12, 0, 0, 0, 0, false},
		{120, 7, 5, 2, 110, 3, true},
		{120, 6, 0, 0, 0, 0, false},
	}

	for _, tt := range tests {
		x, y, w, h, ok := planBox(tt.width, tt.height)
		if ok != tt.ok || (ok && (x != tt.x || y != tt.y || w != tt.w || h != tt.h)) {
			t.Errorf("planBox(%d, %d) = %d, %d, %d, %d, %v; want %d, %d, %d, %d, %v", tt.width, tt.height, x, y, w, h, ok, tt.x, tt.y, tt.w, tt.h, tt.ok)
		}
	}
}

func TestPlanTitleCut(t *testing.T) {
	t.Parallel()

	topLine := func(content string, y int) string { return strings.Split(ansi.Strip(content), "\n")[y] }

	t.Run("a long path is cut from its start", func(t *testing.T) {
		t.Parallel()

		r := newPlanRig(t, 120, 40)
		rel := "docs/superpowers/plans/" + strings.Repeat("a-long-folder-name/", 6) + "p.md"
		r.filePlan(t, rel, "# x\n")
		r.open(t)

		line := topLine(r.a.View().Content, 2)
		if !strings.Contains(line, "…") || !strings.Contains(line, "p.md ") || strings.Contains(line, "docs/superpowers") {
			t.Errorf("top border = %q, want the path cut from its start and ending p.md", line)
		}

		if !strings.Contains(line, " "+planKindWord(core.PlanFile)+" · 4m ─╮") {
			t.Errorf("top border = %q, want the tag one dash before the corner", line)
		}
	})

	t.Run("the tag drops when the path would get under 12 cells", func(t *testing.T) {
		t.Parallel()

		r := newPlanRig(t, 30, 20)
		r.filePlan(t, "docs/superpowers/plans/p.md", "# x\n")
		r.open(t)

		line := topLine(r.a.View().Content, 2)
		if strings.Contains(line, planKindWord(core.PlanFile)) || !strings.Contains(line, "…") || !strings.Contains(line, "p.md ") {
			t.Errorf("top border = %q, want no tag and the cut path", line)
		}

		assertScreen(t, r.a, 30, 20)
	})

	t.Run("control and invisible characters in the path and the name", func(t *testing.T) {
		t.Parallel()

		r := newPlanRig(t, 120, 40)
		r.a.railEntry("p2").probe = railProbe{known: true, project: "ho\x1b[31mst\x07ile\u200bname"}
		r.filePlan(t, "docs/superpowers/plans/a\x1b[32mb\x07c\u200bd.md", "# x\n")
		r.open(t)

		if r.a.plan == nil {
			t.Fatal("the overlay did not open")
		}

		raw := assertScreen(t, r.a, 120, 40)

		for _, bad := range []string{"\x1b[31m", "\x1b[32m", "\x07", "\u200b"} {
			if strings.Contains(raw, bad) {
				t.Errorf("view holds %q", bad)
			}
		}

		if line := topLine(raw, 2); !strings.Contains(line, "ho[31mstilename") || !strings.Contains(line, "a[32mbcd.md") {
			t.Errorf("top border = %q, want the cleaned name and path", line)
		}
	})
}

func TestPlanOverlaySizes(t *testing.T) {
	t.Parallel()

	tests := []struct {
		w, h int
		rows int // 0 for a box that is not drawn
	}{
		{40, 12, 3},
		{20, 6, 0},
	}

	for _, tt := range tests {
		t.Run(fmt.Sprintf("%dx%d", tt.w, tt.h), func(t *testing.T) {
			t.Parallel()

			r := newPlanRig(t, tt.w, tt.h)
			r.filePlan(t, "docs/superpowers/plans/p.md", longPlan(t))
			r.open(t)

			if r.a.plan == nil {
				t.Fatal("the overlay did not open")
			}

			content := assertScreen(t, r.a, tt.w, tt.h)
			lines := strings.Split(ansi.Strip(content), "\n")

			if tt.rows == 0 {
				if _, _, _, _, ok := planBox(tt.w, tt.h); ok {
					t.Fatal("planBox is drawable")
				}

				if text := strings.Join(lines, "\n"); strings.Contains(text, "Title") || strings.Contains(text, "Section 1") {
					t.Errorf("a box that is too small drew:\n%s", text)
				}

				r.keys("j", "G")
				r.keys("esc")

				if r.a.plan != nil {
					t.Error("esc did not close an overlay that is not drawn")
				}

				return
			}

			x, y, w, h, _ := planBox(tt.w, tt.h)
			if got := r.a.plan.view.h; got != tt.rows {
				t.Errorf("doc rows = %d, want %d", got, tt.rows)
			}

			if !strings.HasPrefix(string([]rune(lines[y])[x:]), "╭─") || !strings.HasPrefix(string([]rune(lines[y+h-1])[x:]), "╰") {
				t.Errorf("box corners are not at rows %d and %d:\n%s", y, y+h-1, strings.Join(lines, "\n"))
			}

			if key := r.a.keys.reader.hint("reader.close"); !strings.Contains(lines[y+h-2], key) {
				t.Errorf("hint row %q lacks the close key %q", lines[y+h-2], key)
			}

			if row := string([]rune(lines[y+1])[x : x+w]); strings.Trim(row, "│ ") != "" {
				t.Errorf("row below the top border is not blank: %q", row)
			}

			if w != min(110, tt.w-4) || h != tt.h-4 {
				t.Errorf("box is %dx%d", w, h)
			}
		})
	}
}

type planScene struct {
	name string
	w, h int
	mode bool
	// rewrite has the agent rewrite the shown plan after it opened.
	rewrite bool
}

func planScenes() []planScene {
	return []planScene{{"plan-file", 120, 40, false, false}, {"plan-mode", 120, 40, true, false}, {"plan-80x24", 80, 24, false, false}, {"plan-rewritten", 120, 40, false, true}}
}

func planSceneApp(t *testing.T, icons bool, sc planScene) *App {
	t.Helper()

	r := newPlanRig(t, sc.w, sc.h)
	r.a.icons = icons
	r.a.plans = core.DefaultConfig().Plans

	if sc.mode {
		path := filepath.Join(r.home, ".claude", "plans", "x.md")
		write(t, path, "# Retry the push\n\n## Context\n\nA push that fails on a flaky network should try twice more before it reports an error.\n\n## Steps\n\n1. Wrap `push` in `retry`.\n2. Add a test with a fake remote.\n3. Update the docs.\n")
		r.notePlan(t, core.Plan{Path: path, Kind: core.PlanMode})
	} else {
		r.filePlan(t, "docs/superpowers/plans/p.md", demoPlan(t))
	}

	chord(r.a, 'a')
	r.open(t)

	if r.a.plan == nil {
		t.Fatal("the overlay did not open")
	}

	if sc.rewrite {
		path := r.a.plan.plan.Path
		text := strings.Replace(demoPlan(t), "# Session Caching Implementation Plan\n", "# Session Caching Implementation Plan\n\nRewritten by the agent after review.\n", 1)

		msgs := r.rewrite(t, "p2", path, text)
		if len(msgs) != 1 {
			t.Fatalf("the rewrite raised %d reads", len(msgs))
		}

		r.a.Update(msgs[0])
	}

	return r.a
}

// TestPlanOverlayGolden draws each scene once in colour; it compares
// highlighted colours, so it runs serially.
func TestPlanOverlayGolden(t *testing.T) {
	for _, sc := range planScenes() {
		t.Run("catppuccin-mocha/icons=true/"+sc.name, func(t *testing.T) {
			golden.RequireEqual(t, []byte(planSceneApp(t, true, sc).View().Content))
		})
	}
}

// TestPlanOverlayText is the same screens without styling, so the layout reads.
func TestPlanOverlayText(t *testing.T) {
	t.Parallel()

	for _, icons := range []bool{true, false} {
		for _, sc := range planScenes() {
			t.Run(fmt.Sprintf("icons=%v/%s", icons, sc.name), func(t *testing.T) {
				t.Parallel()
				golden.RequireEqual(t, []byte(ansi.Strip(planSceneApp(t, icons, sc).View().Content)))
			})
		}
	}
}
