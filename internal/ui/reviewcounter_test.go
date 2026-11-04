package ui

import (
	"slices"
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"

	"github.com/mhersson/aim/internal/core"
	"github.com/mhersson/aim/internal/git"
)

const sceneRepo = "/repo"

// markedResult is three files of two hunks each, six hunks in all.
func markedResult() git.Result {
	file := func(path string) git.FileDiff {
		return fileSpec(path, git.Modified,
			hunkSpec(5, 5, "func one()", " a", "-b", "+c", " d"),
			hunkSpec(40, 40, "func two()", " e", "-f", "+g", " h"),
		)
	}

	return git.Result{Turn: 4, Live: true, Files: []git.FileDiff{file("a.go"), file("b.go"), file("c.go")}}
}

// withMarked is a scene tune that runs next, then marks the hunks pick selects
// reviewed in a repository the scene did not have.
func withMarked(pick func(file, hunk int) bool, next func(*testing.T, *App, *reviewView)) func(*testing.T, *App, *reviewView) {
	return func(t *testing.T, a *App, v *reviewView) {
		t.Helper()

		if next != nil {
			next(t, a, v)
		}

		v.common, v.commonRoot, v.ids = sceneRepo, v.root, hunkIDs(v.res)

		var ids []string

		for fi, f := range v.res.Files {
			for hi := range f.Hunks {
				if pick(fi, hi) {
					ids = append(ids, v.ids[fi][hi])
				}
			}
		}

		openIn(t, a,
			core.SetMarks{Repo: sceneRepo, Days: map[string]string{}},
			core.MarkHunks{Repo: sceneRepo, IDs: ids, Reviewed: true, Day: marksToday},
		)
		v.syncMarks(a.store.State())
		v.rebuild(false)
	}
}

func hunksUpTo(n int) func(file, hunk int) bool {
	return func(file, hunk int) bool { return file*2+hunk < n }
}

func markedScene(pick func(file, hunk int) bool) reviewScene {
	return reviewScene{
		theme: "catppuccin-mocha", icons: true, w: 100, h: 30, res: markedResult(), turn: 4, turns: 4, focused: true,
		tune: withMarked(pick, nil),
	}
}

func TestReviewCounter(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		scene func() reviewScene
		icons bool
		want  string // the counter's text; "" for none
		all   bool
	}{
		{"some", func() reviewScene { return markedScene(hunksUpTo(2)) }, true, " ✓ 2/6 ", false},
		{"all", func() reviewScene { return markedScene(hunksUpTo(6)) }, true, " ✓ 6/6 ", true},
		{"none", func() reviewScene { return markedScene(hunksUpTo(0)) }, true, " ✓ 0/6 ", false},
		{"icons off", func() reviewScene { return markedScene(hunksUpTo(2)) }, false, " ✓ 2/6 ", false},
		{"no hunks", func() reviewScene {
			s := markedScene(hunksUpTo(0))
			s.res = git.Result{Turn: 4, Files: []git.FileDiff{{Path: "bin", Binary: true, Status: git.Modified}}}

			return s
		}, true, "", false},
		{"a page shows", func() reviewScene {
			s := mdPageScene("catppuccin-mocha", true)
			s.tune = withMarked(hunksUpTo(6), s.tune)

			return s
		}, true, "", false},
		{"the cursor is on a rendered file", func() reviewScene {
			s := mdScene(false)("catppuccin-mocha", true)
			s.tune = withMarked(hunksUpTo(6), s.tune)

			return s
		}, true, "", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			s := tt.scene()
			s.icons = tt.icons
			a := s.build(t)

			line := topLine(t, a)
			plain := ansi.Strip(line)

			if tt.want == "" {
				if strings.Contains(plain, "✓") {
					t.Fatalf("top border %q holds a counter, want none", plain)
				}

				return
			}

			at := strings.Index(plain, tt.want)
			if at < 0 {
				t.Fatalf("top border %q lacks %q", plain, tt.want)
			}

			if mode := strings.Index(plain, "turn 4"); mode < at+len(tt.want) {
				t.Errorf("top border %q: the mode tag is not right of the counter", plain)
			}

			ui := a.current().theme.UI
			fg := ui.Accent

			if tt.all {
				fg = ui.Success
			}

			vt := lineVT(t, line, ansi.StringWidth(line))
			x := ansi.StringWidth(plain[:at]) + 1

			assertColours(t, vt, x, 0, "✓", fg, ui.Bg, true)
			assertColours(t, vt, x+2, 0, tt.want[len(" ✓ "):len(" ✓ ")+1], fg, ui.Bg, true)
		})
	}
}

// The split tag drops first when room runs short, then the pinned tag, then the
// counter; the mode tag stays.
func TestReviewCounterDropOrder(t *testing.T) {
	t.Parallel()

	s := markedScene(hunksUpTo(2))
	s.w, s.h, s.pinned = 200, 52, true
	a := s.build(t)

	lost := map[string]int{}
	kinds := []string{"split", "pinned", "2/6", "turn 4"}

	// Either side of each tag's drop point (split 51, pinned 41, counter 30), and the least
	// room the border is asked to fit.
	for _, width := range []int{52, 51, 42, 41, 31, 30, 18} {
		d, _, ok := a.drawCtxFor(a.store.State())
		if !ok {
			t.Fatal("no drawer to draw")
		}

		d.lay.split, d.lay.box.W, d.v.rowsSplit = true, width, true
		line := ansi.Strip(d.topBorder())

		shown := make([]bool, len(kinds))
		for i, k := range kinds {
			shown[i] = strings.Contains(line, k)

			if !shown[i] && lost[k] == 0 {
				lost[k] = width
			}
		}

		for i := 1; i < 3; i++ {
			if shown[i-1] && !shown[i] {
				t.Errorf("width %d: %q shows %s but not %s", width, line, kinds[i-1], kinds[i])
			}
		}

		if !shown[3] {
			t.Errorf("width %d: %q lost the mode tag", width, line)
		}

		if w := ansi.StringWidth(line); w != width {
			t.Errorf("width %d: the border is %d cells", width, w)
		}
	}

	for _, k := range kinds[:3] {
		if lost[k] == 0 {
			t.Fatalf("%s never dropped", k)
		}
	}

	if lost["split"] <= lost["pinned"] || lost["pinned"] <= lost["2/6"] {
		t.Errorf("tags dropped at widths split %d, pinned %d, counter %d; want in that order", lost["split"], lost["pinned"], lost["2/6"])
	}
}

func TestReviewTreeMarks(t *testing.T) {
	t.Parallel()

	// Every hunk of a.go is reviewed and one of b.go.
	pick := func(file, hunk int) bool { return file == 0 || file == 1 && hunk == 0 }
	s := markedScene(pick)
	s.w, s.h, s.tree = 200, 52, true
	tune := withMarked(pick, nil)
	s.tune = func(t *testing.T, a *App, v *reviewView) {
		tune(t, a, v)
		v.cursor = v.rowOfFileHeader(2)
	}
	a := s.build(t)

	st := a.store.State()
	lines := a.drawerLines(st, drawerRectOf(t, a, 0))
	ui := a.current().theme.UI

	row := func(name string) (string, int) {
		for _, l := range lines {
			plain := ansi.Strip(l)
			if at := strings.Index(plain, " "+name+" "); at >= 0 && at < 22 {
				return l, ansi.StringWidth(plain[:at]) + 1
			}
		}

		t.Fatalf("no tree row for %s", name)

		return "", 0
	}

	line, x := row("a.go")
	plain := ansi.Strip(line)
	vt := lineVT(t, line, ansi.StringWidth(line))

	assertColours(t, vt, x-2, 0, "M", ui.Muted, ui.Bg, true)
	assertColours(t, vt, x, 0, "a", ui.Muted, ui.Bg, false)

	if tree := ansi.Cut(plain, 0, 22); strings.Contains(tree, "+") {
		t.Errorf("reviewed row %q still holds counts", tree)
	}

	tick := strings.Index(plain, "✓")
	if tick < 0 || ansi.StringWidth(plain[:tick]) > 22 {
		t.Fatalf("reviewed row %q holds no tick in the tree", plain)
	}

	assertColours(t, vt, ansi.StringWidth(plain[:tick]), 0, "✓", ui.Success, ui.Bg, false)

	line, x = row("b.go")
	plain = ansi.Strip(line)
	vt = lineVT(t, line, ansi.StringWidth(line))

	assertColours(t, vt, x-2, 0, "M", ui.Info, ui.Bg, true)
	assertColours(t, vt, x, 0, "b", ui.Fg, ui.Bg, false)

	if tree := ansi.Cut(plain, 0, 22); !strings.Contains(tree, "+2 -2") {
		t.Errorf("partly reviewed row %q lost its counts", tree)
	}
}

// hintKeys are the keys of the drawer's hints, in order, as the hint row lists them.
func hintKeys(t *testing.T, a *App) []string {
	t.Helper()

	d, _, ok := a.drawCtxFor(a.store.State())
	if !ok {
		t.Fatal("no drawer to draw")
	}

	var b strings.Builder

	for _, run := range d.hintRuns(500) {
		b.WriteString(run.text)
	}

	var keys []string

	for entry := range strings.SplitSeq(b.String(), "   ") {
		key, _, _ := strings.Cut(strings.TrimSpace(entry), " ")
		keys = append(keys, key)
	}

	return keys
}

func TestReviewHintsReviewed(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		scene     func() reviewScene
		mark      string // the bound key of review.mark; none when empty
		show      string // the bound key of review.show.reviewed; none when empty
		showsRead bool   // v.showRead
	}{
		{"v on", func() reviewScene {
			s := markedScene(hunksUpTo(1))
			s.w, s.h = 312, 52
			s.tune = withMarked(hunksUpTo(1), func(t *testing.T, a *App, v *reviewView) { v.showRead = true })

			return s
		}, "space", "v", true},
		{"file shown whole", func() reviewScene {
			s := markedScene(hunksUpTo(1))
			s.w, s.h = 312, 52
			s.tune = withMarked(hunksUpTo(1), func(t *testing.T, a *App, v *reviewView) {
				v.full, v.fullOn = map[string]fullText{}, map[string]bool{}

				for _, f := range v.res.Files {
					v.full[f.Path], v.fullOn[f.Path] = fullText{}, true
				}
			})

			return s
		}, "", "", false},
		{"remapped", func() reviewScene {
			s := markedScene(hunksUpTo(1))
			s.w, s.h = 312, 52
			s.keys = core.KeysConfig{Drawer: map[string]string{"m": "review.mark", "space": "none"}}

			return s
		}, "m", "v", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			a := tt.scene().build(t)
			keys := hintKeys(t, a)
			d, _, _ := a.drawCtxFor(a.store.State())

			if got := d.bind.hint("review.mark"); tt.mark != "" && got != tt.mark {
				t.Fatalf("review.mark is bound to %q, want %q", got, tt.mark)
			}

			for id, want := range map[string]string{"review.mark": tt.mark, "review.show.reviewed": tt.show} {
				if present := slices.Contains(keys, d.bind.hint(id)); present != (want != "") {
					t.Errorf("hint keys %q: the key of %s present = %v, want %v", keys, id, present, want != "")
				}
			}

			if got := d.v.showRead; got != tt.showsRead {
				t.Errorf("showRead = %v, want %v", got, tt.showsRead)
			}
		})
	}
}

// Marks count for nothing in the tree, the page list and a rendered file, so
// their hints name no marks.
func TestReviewHintsWithoutMarks(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		scene func() reviewScene
	}{
		{"tree", func() reviewScene {
			s := markedScene(hunksUpTo(1))
			s.tune = withMarked(hunksUpTo(1), func(t *testing.T, a *App, v *reviewView) { a.drawer.tree = true })

			return s
		}},
		{"page", func() reviewScene { return mdPageScene("catppuccin-mocha", true) }},
		{"rendered", func() reviewScene { return mdScene(false)("catppuccin-mocha", true) }},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			s := tt.scene()
			s.w, s.h = 200, 52
			a := s.build(t)

			d, _, _ := a.drawCtxFor(a.store.State())
			if mark, keys := d.bind.hint("review.mark"), hintKeys(t, a); slices.Contains(keys, mark) {
				t.Errorf("hints %q name the mark key %q", keys, mark)
			}
		})
	}
}
