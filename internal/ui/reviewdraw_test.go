package ui

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/x/ansi"
	"github.com/charmbracelet/x/exp/golden"

	"github.com/mhersson/aim/internal/core"
	"github.com/mhersson/aim/internal/git"
	"github.com/mhersson/aim/internal/git/invisible"
)

type reviewCase struct {
	name  string
	scene func(th string, icons bool) reviewScene
}

func reviewCases() []reviewCase {
	base := func(th string, icons bool) reviewScene {
		return reviewScene{theme: th, icons: icons, w: 200, h: 52, res: turn4Result(), turn: 4, turns: 4, focused: true}
	}
	with := func(tune func(s *reviewScene)) func(th string, icons bool) reviewScene {
		return func(th string, icons bool) reviewScene {
			s := base(th, icons)
			tune(&s)

			return s
		}
	}

	return []reviewCase{
		{"unified", with(func(s *reviewScene) {
			s.tune = func(t *testing.T, a *App, v *reviewView) {
				v.cursor = rowWith(t, v, "a review pane needs an agent pane")
			}
		})},
		{"split", with(func(s *reviewScene) {
			s.tune = func(t *testing.T, a *App, v *reviewView) {
				v.split = true
				v.rebuild(true)
				v.cursor = rowWith(t, v, "p.Kind != core.KindAgent")
			}
		})},
		{"full-file", with(func(s *reviewScene) {
			s.tune = func(t *testing.T, a *App, v *reviewView) {
				v.full = map[string]fullText{appGoPath: appGoFull()}
				v.fullOn = allWhole(v.full)
				v.folded = map[string]bool{foldKey(appGoPath, 1): true}
				v.rebuild(false)
				v.cursor = rowWith(t, v, "notice  string")
			}
		})},
		{"narrow-tree-hidden", with(func(s *reviewScene) { s.w, s.h, s.rail = 160, 48, true })},
		{"narrow-tree-open", with(func(s *reviewScene) {
			s.w, s.h, s.rail, s.tree = 160, 48, true, true
			s.tune = func(t *testing.T, a *App, v *reviewView) {
				v.treeSel = 1
				a.drawer.tree = true
			}
		})},
		{"hidden-characters", with(func(s *reviewScene) {
			s.res, s.turn = hostileResult(), 2
			s.tune = func(t *testing.T, a *App, v *reviewView) { v.cursor = rowWith(t, v, "bidi") }
		})},
		{"binary-and-truncated", with(func(s *reviewScene) { s.res, s.turn = edgeResult(), 3 })},
		{"no-snapshot", with(func(s *reviewScene) { s.res, s.turn, s.noSnap = git.Result{}, 1, true })},
		{"turn-past", with(func(s *reviewScene) {
			s.turn, s.turns, s.tree = 2, 3, true
			s.res.Live = false
		})},
		{"empty", with(func(s *reviewScene) { s.res, s.branch = git.Result{}, true })},
		{"sticky-header", with(func(s *reviewScene) {
			s.h = 24
			s.tune = func(t *testing.T, a *App, v *reviewView) {
				v.top = rowWith(t, v, "ReviewSource is the agent")
				v.cursor = v.top + 4
			}
		})},
		{"search-prompt", with(func(s *reviewScene) {
			s.tune = func(t *testing.T, a *App, v *reviewView) {
				v.search = new("review")
				v.cursor = rowWith(t, v, "review.toggle")
			}
		})},
		{"pinned", with(func(s *reviewScene) { s.pinned = true })},
		{"pinned-split", with(func(s *reviewScene) {
			s.pinned = true
			s.tune = func(t *testing.T, a *App, v *reviewView) {
				v.split = true
				v.rebuild(true)
			}
		})},
		{"pinned-narrow", with(func(s *reviewScene) { s.pinned, s.w, s.h = true, 40, 40 })},
		{"pinned-narrower", with(func(s *reviewScene) { s.pinned, s.w, s.h = true, 28, 40 })},
		{"worktree-removed", with(func(s *reviewScene) {
			s.pinned, s.branch, s.res = true, true, git.Result{}
			s.tune = func(t *testing.T, a *App, v *reviewView) {
				a.getenv = func(string) string { return "/home/me" }
				v.err = errWorktreeRemoved{root: "/home/me/code/multiplex/.worktrees/b"}
				v.rebuild(false)
			}
		})},
		{"worktree-list", with(func(s *reviewScene) {
			s.tune = func(t *testing.T, a *App, v *reviewView) { showList(a, wtDemoRows(), "other · main", 0) }
		})},
		{"worktree-list-pinned", with(func(s *reviewScene) {
			s.pinned = true
			s.tune = func(t *testing.T, a *App, v *reviewView) { showList(a, wtDemoRows(), "other · main", 4) }
		})},
		{"worktree-list-narrow", with(func(s *reviewScene) {
			s.w, s.h = 40, 40
			s.tune = func(t *testing.T, a *App, v *reviewView) {
				rows := wtDemoRows()
				rows[2].branch = "feature/follow-the-agents-edits"
				rows[3].branch = "feature/worktree-list"

				showList(a, rows, "other · feature/follow-the-agents-edits", 3)
			}
		})},
		{"pr", with(func(s *reviewScene) {
			s.branch, s.tune = true, prScene
		})},
		{"pr-unpushed", with(func(s *reviewScene) {
			s.branch, s.tune = true, prSceneWith(func(p *core.PRInfo) {
				p.ChecksDone, p.ReviewDecision = 2, "CHANGES_REQUESTED"
			}, 2)
		})},
		{"pr-tree", with(func(s *reviewScene) {
			s.branch, s.tree, s.tune = true, true, prScene
		})},
		{"rendered", mdScene(false)},
		{"rendered-narrow", mdScene(true)},
		{"md-page", mdPageScene},
		{"links", linksScene},
		{"reviewed", with(func(s *reviewScene) {
			s.res, s.tree = markedResult(), true
			s.tune = withMarked(hunksUpTo(3), func(t *testing.T, a *App, v *reviewView) {
				v.showRead = true
				v.readOpen = map[string]bool{git.HunkID("b.go", v.res.Files[1].Hunks[0]): true}
			})
		})},
		{"reviewed-all", with(func(s *reviewScene) {
			s.res, s.tree = markedResult(), true
			s.tune = withMarked(hunksUpTo(6), nil)
		})},
		{"wrapped", with(func(s *reviewScene) {
			s.res = wrappedResult()
			s.tune = func(t *testing.T, a *App, v *reviewView) { v.cursor = rowWith(t, v, "ansi.Truncate") }
		})},
		{"wrapped-split", with(func(s *reviewScene) {
			s.res = wrappedResult()
			s.tune = func(t *testing.T, a *App, v *reviewView) {
				v.split = true
				v.rebuild(true)
				v.cursor = rowWith(t, v, "ansi.Truncate")
			}
		})},
		{"wrapped-80x24", with(func(s *reviewScene) {
			s.res, s.w, s.h = wrappedResult(), 80, 24
			s.tune = func(t *testing.T, a *App, v *reviewView) { v.cursor = rowWith(t, v, "ansi.Truncate") }
		})},
		{"zoomed", with(func(s *reviewScene) {
			s.res, s.w, s.h, s.rail = wrappedResult(), 270, 75, true
			s.tune = func(t *testing.T, a *App, v *reviewView) {
				v.zoom = true
				v.cursor = rowWith(t, v, "ansi.Truncate")
			}
		})},
		{"unfocused", with(func(s *reviewScene) {
			s.focused = false
			s.tune = func(t *testing.T, a *App, v *reviewView) { v.cursor = rowWith(t, v, "review.toggle") }
		})},
	}
}

// TestReviewGolden draws each case once in colour, in the default theme with icons.
// TestReviewText draws icons off and TestScreensTakeTheirColoursFromTheTheme the other theme.
// It compares highlighted colours, so it runs serially (docs/gotchas.md).
func TestReviewGolden(t *testing.T) {
	for _, c := range reviewCases() {
		t.Run("catppuccin-mocha/icons=true/"+c.name, func(t *testing.T) {
			a := c.scene("catppuccin-mocha", true).build(t)
			golden.RequireEqual(t, []byte(a.View().Content))
		})
	}
}

// TestReviewText is the same frames without styling and without icons, so the layout reads.
func TestReviewText(t *testing.T) {
	t.Parallel()

	for _, c := range reviewCases() {
		t.Run("icons=false/"+c.name, func(t *testing.T) {
			t.Parallel()
			a := c.scene("catppuccin-mocha", false).build(t)
			golden.RequireEqual(t, []byte(ansi.Strip(a.View().Content)))
		})
	}
}

func TestDrawerCoversPanes(t *testing.T) {
	t.Parallel()

	s := reviewScene{theme: "catppuccin-mocha", icons: true, w: 200, h: 52, res: turn4Result(), turn: 4, turns: 4, focused: true}
	a := s.build(t)

	st := a.store.State()

	r, ok := a.drawerRect(st)
	if !ok {
		t.Fatal("no drawer rect")
	}

	with := strings.Split(ansi.Strip(a.View().Content), "\n")
	drawer := a.drawerLines(st, r)

	openIn(t, a, core.CloseReview{})

	without := strings.Split(ansi.Strip(a.View().Content), "\n")

	if len(with) != len(without) {
		t.Fatalf("frame heights differ: %d with the drawer, %d without", len(with), len(without))
	}

	rects := a.layoutRects()
	master := rects["p1"]

	if master.X+master.W <= r.X {
		t.Fatalf("the master %+v lies clear of the drawer %+v; the scene must put it partly under", master, r)
	}

	if rects["p2"].X < r.X || rects["p3"].X < r.X {
		t.Fatalf("workers %+v, %+v are not under the drawer %+v", rects["p2"], rects["p3"], r)
	}

	for y := range with {
		inDrawer := y >= r.Y && y < r.Y+r.H

		left := ansi.Cut(with[y], 0, r.X)
		if inDrawer && left != ansi.Cut(without[y], 0, r.X) {
			t.Errorf("row %d: cells left of the drawer changed:\n got %q\nwant %q", y, left, ansi.Cut(without[y], 0, r.X))
		}

		if !inDrawer {
			if with[y] != without[y] {
				t.Errorf("row %d outside the drawer changed", y)
			}

			continue
		}

		if got, want := ansi.Cut(with[y], r.X, r.X+r.W), ansi.Strip(drawer[y-r.Y]); got != want {
			t.Errorf("row %d: the drawer's cells hold pane text:\n got %q\nwant %q", y, got, want)
		}
	}
}

func TestDrawerRect(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		w, h int
		rail bool
		want core.Rect
		ok   bool
	}{
		{"half of the body", 200, 52, false, core.Rect{X: 100, Y: 2, W: 100, H: 50}, true},
		{"odd body takes the larger half", 121, 30, false, core.Rect{X: 60, Y: 2, W: 61, H: 28}, true},
		{"at least 60 columns", 100, 30, false, core.Rect{X: 40, Y: 2, W: 60, H: 28}, true},
		{"a body under 60 is covered whole", 50, 30, false, core.Rect{X: 0, Y: 2, W: 50, H: 28}, true},
		{"no room for a frame", 2, 30, false, core.Rect{}, false},
		{"no rows for a frame", 100, 4, false, core.Rect{}, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			a := newThemedApp(t, "catppuccin-mocha")
			sized(t, a, tt.w, tt.h)
			openIn(t, a, core.OpenPane{Kind: core.KindShell, Cwd: "/w/p1", Split: core.SplitVertical})

			if tt.ok {
				openIn(t, a, core.OpenReview{Pane: "p1", Mode: core.ReviewTurn})
			}

			got, ok := a.drawerRect(a.store.State())
			if ok != tt.ok || got != tt.want {
				t.Errorf("drawerRect = %+v, %v; want %+v, %v", got, ok, tt.want, tt.ok)
			}
		})
	}
}

func TestDrawerRectWithRail(t *testing.T) {
	t.Parallel()
	a := drawApp(t, "catppuccin-mocha", true, 200, 40, railFixtureAgents())
	openIn(t, a, core.FocusPane{ID: "p5"}, core.OpenReview{Pane: "p5", Mode: core.ReviewBranch})

	st := a.store.State()
	body := a.bodyAreaIn(st.Rail)

	r, ok := a.drawerRect(st)
	if !ok {
		t.Fatal("no drawer rect")
	}

	if r.X < body.X || r.X+r.W != body.X+body.W || r.W != body.W-body.W/2 {
		t.Errorf("drawerRect = %+v in a body %+v, want the right half of it", r, body)
	}
}

func TestDrawerRectSlides(t *testing.T) {
	t.Parallel()

	s := reviewScene{theme: "catppuccin-mocha", icons: true, w: 200, h: 52, res: turn4Result(), turn: 4, turns: 4}
	a := s.build(t)
	clk := a.clock.(*fakeClock)
	st := a.store.State()

	a.drawer.slideFrom = t0

	width := func(d time.Duration) int {
		clk.Set(t0.Add(d))

		r, ok := a.drawerRect(st)
		if !ok {
			return 0
		}

		if r.X+r.W != 200 {
			t.Errorf("at %v the drawer %+v does not end at the right edge", d, r)
		}

		return r.W
	}

	if got := width(0); got != 0 {
		t.Errorf("at 0 ms the drawer is %d wide, want it out of sight", got)
	}

	// ease-out cubic: 1 - (1 - 0.5)^3 = 0.875 of 100 columns
	if got := width(60 * time.Millisecond); got != 88 {
		t.Errorf("at 60 ms the drawer is %d wide, want 88", got)
	}

	if got := width(120 * time.Millisecond); got != 100 {
		t.Errorf("at 120 ms the drawer is %d wide, want 100", got)
	}

	if got := width(time.Second); got != 100 {
		t.Errorf("after the slide the drawer is %d wide, want 100", got)
	}

	a.drawer.closing = true

	if got := width(60 * time.Millisecond); got != 13 {
		t.Errorf("closing at 60 ms the drawer is %d wide, want 13", got)
	}

	if got := width(120 * time.Millisecond); got != 0 {
		t.Errorf("closing at 120 ms the drawer is %d wide, want 0", got)
	}

	// A slide frame draws the left part of the full-width drawer: the content slides, it does not squeeze.
	a.drawer.closing = false

	clk.Set(t0.Add(60 * time.Millisecond))

	r, _ := a.drawerRect(st)
	full, _ := a.drawerFull(st)
	got := a.drawerLines(st, r)

	if len(got) != r.H {
		t.Fatalf("slide frame has %d rows, want %d", len(got), r.H)
	}

	fullLines := a.drawerLines(st, full)
	if ansi.Strip(got[1]) != ansi.Strip(ansi.Truncate(fullLines[1], r.W, "")) {
		t.Errorf("slide row = %q, want the full row cut to %d cells", ansi.Strip(got[1]), r.W)
	}
}

type rowWidthVariant struct {
	name string
	tune func(t *testing.T, a *App, v *reviewView, long longLines)
}

func rowWidthVariants() []rowWidthVariant {
	return []rowWidthVariant{
		{"unified", func(t *testing.T, a *App, v *reviewView, long longLines) {}},
		{"split", func(t *testing.T, a *App, v *reviewView, long longLines) { v.split = true }},
		{"full file", func(t *testing.T, a *App, v *reviewView, long longLines) {
			v.full = map[string]fullText{}
			for _, f := range v.res.Files {
				v.full[f.Path] = hostileFullOf(long)
			}

			v.fullOn = allWhole(v.full)

			v.rebuild(v.split)
		}},
		{"tree open", func(t *testing.T, a *App, v *reviewView, long longLines) {
			openIn(t, a, core.SetReviewTree{Open: true})
			a.drawer.tree = true
		}},
		{"search", func(t *testing.T, a *App, v *reviewView, long longLines) { v.search = new("e\x1bv") }},
	}
}

func rowWidths(icons bool) []int {
	step := 3
	if !icons {
		step = 11
	}

	var widths []int
	for w := 40; w <= 120; w += step {
		widths = append(widths, w)
	}

	return widths
}

// rowWidthApp builds the scene once at the first width; the views are then
// drawn at every width, so a timing measures the resizes and frames, not the setup.
func rowWidthApp(t *testing.T, icons bool, vr rowWidthVariant, long longLines) *App {
	t.Helper()

	return rowWidthAppAt(t, icons, rowWidths(icons)[0], vr, long)
}

func rowWidthAppAt(t *testing.T, icons bool, w int, vr rowWidthVariant, long longLines) *App {
	t.Helper()

	s := reviewScene{theme: "catppuccin-mocha", icons: icons, w: w, h: 30, res: hostileResultOf(long), turn: 2, turns: 3, focused: true}
	s.tune = func(t *testing.T, a *App, v *reviewView) {
		vr.tune(t, a, v, long)
		v.cursor = min(4, len(v.rows)-1)
	}

	return s.build(t)
}

// resizeDrawer is one step of the drawer: the update and both frames.
func resizeDrawer(t *testing.T, a *App, w int) (r core.Rect, lines []string, frame string) {
	t.Helper()

	sized(t, a, w, 30)
	settleDrawer(t, a)

	st := a.store.State()

	r, ok := a.drawerRect(st)
	if !ok {
		t.Fatal("no drawer")
	}

	lines = a.drawerLines(st, r)
	frame = a.View().Content

	return r, lines, frame
}

func TestDrawerRowsAreExactWidth(t *testing.T) {
	t.Parallel()

	// A drawer that covers the body whole (40 to 59), the 60-column floor and one past it,
	// and the half of a body of 120.
	exactWidths := []int{40, 47, 53, 59, 60, 61, 120}

	for _, icons := range []bool{true, false} {
		for _, vr := range rowWidthVariants() {
			t.Run(fmt.Sprintf("icons=%v/%s", icons, vr.name), func(t *testing.T) {
				t.Parallel()

				a := rowWidthAppAt(t, icons, exactWidths[0], vr, pastBudgetLines())

				for _, w := range exactWidths {
					t.Run(fmt.Sprintf("width=%d", w), func(t *testing.T) {
						r, lines, frame := resizeDrawer(t, a, w)

						if len(lines) != r.H {
							t.Fatalf("%d lines, want %d", len(lines), r.H)
						}

						for i, l := range lines {
							if got := ansi.StringWidth(l); got != r.W {
								t.Errorf("row %d is %d cells, want %d: %q", i, got, r.W, ansi.Strip(l))
							}

							for _, c := range ansi.Strip(l) {
								if c < 0x20 || (c >= 0x7f && c <= 0x9f) || invisible.Rune(c) {
									t.Errorf("row %d carries %U: %q", i, c, ansi.Strip(l))
								}
							}
						}

						for i, l := range strings.Split(frame, "\n") {
							if got := ansi.StringWidth(l); got != w {
								t.Errorf("frame row %d is %d cells, want %d", i, got, w)
							}
						}
					})
				}
			})
		}
	}
}

func TestHighlightBatchCap(t *testing.T) {
	t.Parallel()

	res, full := bigResult(1100)

	for _, whole := range []bool{false, true} {
		t.Run(fmt.Sprintf("full=%v", whole), func(t *testing.T) {
			s := reviewScene{theme: "catppuccin-mocha", icons: true, w: 200, h: 52, res: res, turn: 1, turns: 1, focused: true}
			s.tune = func(t *testing.T, a *App, v *reviewView) {
				if whole {
					v.full = map[string]fullText{"big.go": full}
					v.fullOn = allWhole(v.full)
					v.rebuild(false)
				}
			}

			a := newThemedApp(t, s.theme)
			a.icons = s.icons
			a.clock = &fakeClock{t: t0}

			sized(t, a, s.w, s.h)
			openIn(t, a, core.OpenPane{Kind: core.KindShell, Cwd: "/w/p1", Split: core.SplitVertical}, core.OpenReview{Pane: "p1", Mode: core.ReviewTurn})

			v := a.reviewViewOf(a.store.State().Tab().ID)
			v.res, v.gen, v.turns, v.src = res, 1, 1, git.TurnSource{Turn: 1}
			v.rebuild(false)
			s.tune(t, a, v)

			// The screen sits across the boundary of the first and second chunk (hlChunk lines).
			v.top, v.cursor = 490, 490

			passes, total := 0, 0

			for passes < 100 {
				a.drawerAfterUpdate()

				if a.drawer.hlJob == nil {
					break
				}

				passes++

				if got := a.drawer.tokenised; got > hlBatch {
					t.Fatalf("pass %d tokenised %d lines, want at most %d", passes, got, hlBatch)
				}

				total += a.drawer.tokenised

				runHighlight(t, a)
			}

			if passes < 2 {
				t.Errorf("the whole range was tokenised in %d pass; the cap never applied", passes)
			}

			if total < hlBatch || total > len(full.new) {
				t.Errorf("tokenised %d lines in all", total)
			}

			if a.drawer.hlJob != nil {
				t.Fatal("highlighting still runs batches after 100 passes")
			}

			// Nothing is left: the next update must not tokenise or ask again.
			if cmd := a.drawerAfterUpdate(); cmd != nil || a.drawer.tokenised != 0 {
				t.Errorf("an idle pass tokenised %d lines and asked for a redraw: %v", a.drawer.tokenised, cmd != nil)
			}
		})
	}
}

func TestFreshRowsFlash(t *testing.T) {
	t.Parallel()

	build := func(until time.Duration) string {
		s := reviewScene{theme: "catppuccin-mocha", icons: true, w: 200, h: 52, res: turn4Result(), turn: 4, turns: 4, focused: true}
		s.tune = func(t *testing.T, a *App, v *reviewView) {
			v.fresh = map[int]bool{rowWith(t, v, "review.toggle"): true}
			v.flashUntil = t0.Add(until)
		}

		return s.build(t).View().Content
	}

	flashing, quiet := build(600*time.Millisecond), build(-time.Millisecond)
	if flashing == quiet {
		t.Error("a fresh row inside its flash window draws like any other")
	}
}

// The mode row drops " in this worktree" when the tree cannot grow to hold it,
// and is cut only when even the short form does not fit.
func TestModeRowForms(t *testing.T) {
	t.Parallel()

	noSnap := func(s *reviewScene) { s.res, s.turn, s.noSnap = git.Result{}, 1, true }
	past := func(s *reviewScene) { s.turn, s.res.Live = 2, false }

	tests := []struct {
		name  string
		w     int
		scene func(s *reviewScene)
		short bool // the row drops " in this worktree"
		cut   bool // and is cut to the tree's width
	}{
		{"live fits whole", 300, func(s *reviewScene) {}, false, false},
		{"live drops", 200, func(s *reviewScene) {}, true, false},
		{"past fits whole", 300, past, false, false},
		{"past drops", 160, past, true, false},
		{"no snapshot fits whole", 300, noSnap, false, false},
		{"no snapshot drops", 200, noSnap, true, false},
		{"even the short form is cut", 120, noSnap, true, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			s := reviewScene{theme: "catppuccin-mocha", w: tt.w, h: 52, res: turn4Result(), turn: 4, turns: 4, focused: true, tree: true}
			tt.scene(&s)

			a := s.build(t)
			text := ansi.Strip(a.View().Content)

			d, _, ok := a.drawCtxFor(a.store.State())
			if !ok {
				t.Fatal("no drawer to draw")
			}

			if d.lay.modeShort != tt.short {
				t.Errorf("modeShort = %v, want %v", d.lay.modeShort, tt.short)
			}

			_, word, rest := modeParts(d.v, false, tt.short)
			row := " " + word + rest

			if tt.cut {
				row = ansi.Truncate(row, d.lay.treeW, "…")
			}

			if !strings.Contains(text, row) {
				t.Errorf("the frame lacks the mode row %q:\n%s", row, text)
			}

			if _, wordLong, restLong := modeParts(d.v, false, false); tt.short && strings.Contains(text, wordLong+restLong) {
				t.Errorf("the frame holds the long form of the mode row:\n%s", text)
			}
		})
	}
}

func TestDrawerTitleAndTags(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		icons bool
		scene func(s *reviewScene)
		want  []string
		not   []string
	}{
		{"branch", false, func(s *reviewScene) { s.branch = true }, []string{" branch ─╮", "branch vs origin/main"}, []string{"turn 4/4"}},
		{"branch icon", true, func(s *reviewScene) { s.branch = true }, []string{"\U000F062C branch ", "\U000F062C branch vs origin/main"}, nil},
		{"empty branch", false, func(s *reviewScene) { s.res, s.branch = git.Result{}, true }, []string{"origin/main", "0 files"}, nil},
		{"empty turn", false, func(s *reviewScene) { s.res, s.turn = git.Result{Turn: 3}, 3 }, []string{"turn 3"}, []string{"no snapshot"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			s := reviewScene{theme: "catppuccin-mocha", icons: tt.icons, w: 200, h: 52, res: turn4Result(), turn: 4, turns: 4, focused: true, tree: true}
			tt.scene(&s)

			a := s.build(t)
			text := ansi.Strip(a.View().Content)

			for _, w := range tt.want {
				if !strings.Contains(text, w) {
					t.Errorf("the frame lacks %q:\n%s", w, text)
				}
			}

			for _, n := range tt.not {
				if strings.Contains(text, n) {
					t.Errorf("the frame holds %q", n)
				}
			}
		})
	}
}

// Without the tree the mode shows only in the tag, and the diff takes the whole width.
func TestDrawerModeShowsOnlyInTheTagWithoutTheTree(t *testing.T) {
	t.Parallel()

	s := reviewScene{theme: "catppuccin-mocha", icons: false, w: 200, h: 52, res: turn4Result(), turn: 4, turns: 4, focused: true}
	text := ansi.Strip(s.build(t).View().Content)

	for _, want := range []string{" turn 4/4 ─╮"} {
		if !strings.Contains(text, want) {
			t.Errorf("the frame lacks %q:\n%s", want, text)
		}
	}

	for _, not := range []string{"turn 4 of 4", "4 files"} {
		if strings.Contains(text, not) {
			t.Errorf("the frame holds %q although the tree is hidden:\n%s", not, text)
		}
	}
}

func TestDrawerKeyHints(t *testing.T) {
	t.Parallel()

	// build draws the drawer h rows high with the keyboard in the diff and returns the frame
	// and the bound keys of the diff hints.
	build := func(h int, tree bool) (text string, keys []string) {
		s := reviewScene{theme: "catppuccin-mocha", icons: false, w: 330, h: h, res: turn4Result(), turn: 4, turns: 4, focused: true, tree: tree}
		s.tune = func(t *testing.T, a *App, v *reviewView) { a.drawer.tree = false }
		a := s.build(t)

		d, _, ok := a.drawCtxFor(a.store.State())
		if !ok {
			t.Fatal("no drawer to draw")
		}

		keys = []string{
			d.bind.hint("review.line.next", "review.line.prev"),
			d.bind.hint("review.hunk.next", "review.hunk.prev"),
			d.bind.hint("review.mode.branch", "review.mode.turn"),
		}

		return ansi.Strip(a.View().Content), keys
	}

	if got, keys := build(52, true); !holdsAll(got, keys) {
		t.Errorf("diff hints %q missing with the tree shown and the keyboard in the diff:\n%s", keys, got)
	}

	// An inner height under 12 shows no hints: 12 rows of body plus the frame is 14, plus the bar and the rule 16.
	if got, keys := build(15, false); strings.Contains(got, keys[0]) {
		t.Errorf("hints shown with an inner height of 11:\n%s", got)
	}

	if got, keys := build(16, false); !holdsAll(got, keys) {
		t.Errorf("hints %q missing with an inner height of 12:\n%s", keys, got)
	}
}

func holdsAll(text string, parts []string) bool {
	for _, p := range parts {
		if !strings.Contains(text, p) {
			return false
		}
	}

	return true
}

// The turn hint shows only while the drawer shows a turn, as the mode tag does:
// a drawer that shows the branch leaves it out so the row holds the hints that
// work, whatever mode core keeps.
func TestDrawerTurnHintShowsOnlyForTurns(t *testing.T) {
	t.Parallel()

	// A turn-mode drawer that shows the branch while core keeps turn mode.
	branchInTurnMode := func(session string, turns int, foreign bool) func(s *reviewScene) {
		return func(s *reviewScene) {
			s.tune = func(t *testing.T, a *App, v *reviewView) {
				v.src, v.base, v.session, v.turns, v.foreign = git.BranchSource{Worktree: "/w/p1"}, "origin/main", session, turns, foreign
			}
		}
	}

	tests := []struct {
		name  string
		scene func(s *reviewScene)
		turn  bool // the row holds the turn hint
	}{
		{"branch mode", func(s *reviewScene) { s.branch = true }, false},
		{"turn mode on the agent's worktree", func(s *reviewScene) {}, true},
		{"turn mode on another worktree with no turns", branchInTurnMode("s", 0, true), false},
		{"turn mode with no session", branchInTurnMode("", 0, false), false},
		{"turn mode with no turns yet", branchInTurnMode("s", 0, false), false},
	}

	hintRow := func(t *testing.T, w int, scene func(s *reviewScene)) (row string, d *drawCtx) {
		t.Helper()

		s := reviewScene{theme: "catppuccin-mocha", icons: false, w: w, h: 52, res: turn4Result(), turn: 4, turns: 4, focused: true}
		scene(&s)

		a := s.build(t)

		for l := range strings.SplitSeq(ansi.Strip(a.View().Content), "\n") {
			if strings.Contains(l, "j/k") {
				row = strings.TrimRight(strings.TrimRight(l, " │"), " ")
			}
		}

		d, _, ok := a.drawCtxFor(a.store.State())
		if !ok {
			t.Fatal("no drawer to draw")
		}

		return row, d
	}

	for _, tt := range tests {
		t.Run(tt.name+", wide", func(t *testing.T) {
			t.Parallel()

			row, d := hintRow(t, 330, tt.scene)
			mode := d.bind.hint("review.mode.branch", "review.mode.turn")
			turn := d.bind.hint("review.turn.prev", "review.turn.next")

			// The turn keys end the row, after the mode keys.
			if has := strings.Contains(row, turn); has != tt.turn {
				t.Errorf("the hint row %q holds the turn keys %q = %v, want %v", row, turn, has, tt.turn)
			}

			if tt.turn && strings.Index(row, turn) < strings.Index(row, mode) {
				t.Errorf("the turn keys %q come before the mode keys %q in %q", turn, mode, row)
			}

			if !tt.turn && !strings.Contains(row, mode) {
				t.Errorf("the hint row %q lacks the mode keys %q", row, mode)
			}
		})
	}

	// At a usual width the hints drop whole from the end, the mode and turn hints first.
	t.Run("usual width", func(t *testing.T) {
		t.Parallel()

		row, d := hintRow(t, 270, func(s *reviewScene) {})
		mark := d.bind.hint("review.mark")

		for _, id := range [][]string{{"review.mode.branch", "review.mode.turn"}, {"review.turn.prev", "review.turn.next"}} {
			if keys := d.bind.hint(id...); strings.Contains(row, keys) {
				t.Errorf("the hint row %q holds %q, want it dropped", row, keys)
			}
		}

		at := strings.LastIndex(row, mark)
		if at < 0 || len(strings.Fields(row[at+len(mark):])) != 1 {
			t.Errorf("the hint row %q does not end with the hint of %q", row, mark)
		}
	})
}

func TestDrawerSplitFallsBackWhenNarrow(t *testing.T) {
	t.Parallel()

	s := reviewScene{theme: "catppuccin-mocha", icons: false, w: 124, h: 40, res: turn4Result(), turn: 4, turns: 4, focused: true}
	s.tune = func(t *testing.T, a *App, v *reviewView) { v.split = true }
	a := s.build(t)

	v := a.reviewViewOf(a.store.State().Tab().ID)
	if v.rowsSplit {
		t.Error("rows are side by side in a drawer too narrow for 24 columns of code per side")
	}

	if strings.Contains(ansi.Strip(a.View().Content), "split ─") {
		t.Error("the split tag shows although unified is drawn")
	}

	if !v.split {
		t.Error("the choice of split was dropped; a wider window must bring it back")
	}

	sized(t, a, 200, 40)
	settleDrawer(t, a)

	if !v.rowsSplit {
		t.Error("rows stayed unified after the window widened")
	}
}

func TestDrawerFocusedPaneDrawsUnfocused(t *testing.T) {
	t.Parallel()

	s := reviewScene{theme: "catppuccin-mocha", icons: true, w: 200, h: 52, res: turn4Result(), turn: 4, turns: 4}
	s.focused = false

	keysOnPane := s.build(t)

	s.focused = true
	keysOnDrawer := s.build(t)

	rects := keysOnPane.layoutRects()
	row := func(a *App) string {
		lines := strings.Split(a.View().Content, "\n")

		return ansi.Cut(lines[rects["p1"].Y], 0, rects["p1"].X+rects["p1"].W/2)
	}

	if row(keysOnPane) == row(keysOnDrawer) {
		t.Error("the master's top border draws the same with the keyboard on it and on the drawer")
	}
}

func TestDrawerHidesPaneCursor(t *testing.T) {
	t.Parallel()

	s := reviewScene{theme: "catppuccin-mocha", icons: true, w: 200, h: 52, res: turn4Result(), turn: 4, turns: 4, focused: true}
	a := s.build(t)

	if cur := a.View().Cursor; cur != nil {
		t.Errorf("cursor %+v shown while the drawer has the keyboard", cur)
	}
}

func TestDrawerGhostLinesCarryNoNumber(t *testing.T) {
	t.Parallel()

	s := reviewScene{theme: "catppuccin-mocha", icons: true, w: 200, h: 52, res: turn4Result(), turn: 4, turns: 4, focused: true}
	s.tune = func(t *testing.T, a *App, v *reviewView) {
		v.full = map[string]fullText{appGoPath: appGoFull()}
		v.fullOn = allWhole(v.full)
		v.rebuild(false)
	}

	lines := strings.Split(ansi.Strip(s.build(t).View().Content), "\n")

	// before is the screen row that holds code, up to where the code starts.
	before := func(code string) string {
		t.Helper()

		var found []string

		for _, l := range lines {
			if before, _, ok := strings.Cut(l, code); ok {
				found = append(found, before)
			}
		}

		if len(found) != 1 {
			t.Fatalf("%d screen rows hold %q, want 1:\n%s", len(found), code, strings.Join(lines, "\n"))
		}

		return found[0]
	}

	isNumber := func(s string) bool { return s != "" && strings.Trim(s, "0123456789") == "" }

	// The removed line "notice  string" is old line 6; the kept line "rail    rail" is new line 8.
	ghost, kept := before("notice  string"), before("rail    rail")

	fields := strings.Fields(ghost)
	if n := len(fields); n < 2 || fields[n-1] != "-" || isNumber(fields[n-2]) {
		t.Errorf("a ghost row reads %q before its code, want no number before the sign", fields[max(len(fields)-2, 0):])
	}

	if gw, kw := ansi.StringWidth(ghost), ansi.StringWidth(kept); gw != kw {
		t.Errorf("a ghost row's code starts at column %d, a kept row's at %d, want them aligned", gw, kw)
	}

	if fields := strings.Fields(kept); len(fields) == 0 || fields[len(fields)-1] != "8" {
		t.Errorf("the line after the removed one reads %q before its code, want its new number 8", fields[max(len(fields)-1, 0):])
	}
}

func crResult(specs ...string) git.Result {
	f := fileSpec("eol.txt", git.Modified, hunkSpec(1, 1, "", specs...))

	return git.Result{Turn: 4, Files: []git.FileDiff{f}}
}

func TestLineEndingCR(t *testing.T) {
	t.Parallel()

	const tag = "\u27e8U+000D\u27e9"

	tests := []struct {
		name    string
		specs   []string
		split   bool
		tags    int
		flagged bool
	}{
		{"crlf content change shows nothing", []string{"-old\r", "+new\r", " same\r"}, false, 0, false},
		{"crlf content change in split", []string{"-old\r", "+new\r"}, true, 0, false},
		{"lf to crlf is tagged", []string{"-same", "+same\r"}, false, 1, false},
		{"crlf to lf is tagged", []string{"-same\r", "+same"}, false, 1, false},
		{"lf to crlf in split", []string{"-same", "+same\r"}, true, 1, false},
		{"mid-line cr is tagged and flags", []string{"+a\rb"}, false, 1, true},
		{"cr and an edit is not an ending change", []string{"-a\r", "+b"}, false, 0, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			res := crResult(tt.specs...)
			s := reviewScene{theme: "catppuccin-mocha", w: 200, h: 52, res: res, turn: 4, turns: 4, focused: true}
			s.tune = func(t *testing.T, a *App, v *reviewView) { v.split = tt.split }

			text := ansi.Strip(s.build(t).View().Content)
			if got := strings.Count(text, tag); got != tt.tags {
				t.Errorf("%d CR tags drawn, want %d:\n%s", got, tt.tags, text)
			}

			if got := fileHidden(res.Files[0]); got != tt.flagged {
				t.Errorf("fileHidden = %v, want %v", got, tt.flagged)
			}
		})
	}
}

// prHeadOID is the head the scene's diff was loaded at.
const prHeadOID = "a1b2c3d4e5f60718293a4b5c6d7e8f9012345678"

// prScene puts the scene's drawer in PR mode on pull request 7, as the sync
// after a fetch leaves it.
func prScene(t *testing.T, a *App, v *reviewView) { prSceneWith(nil, 0)(t, a, v) }

// prSceneWith is prScene with the poll's pull request changed by tune and the
// worktree's unpushed commits given.
func prSceneWith(tune func(*core.PRInfo), unpushed int) func(t *testing.T, a *App, v *reviewView) {
	return func(t *testing.T, a *App, v *reviewView) {
		t.Helper()

		pr := core.ReviewPR{Repo: prRepoKey, Number: 7, Root: "/w/p1"}
		openIn(t, a, core.ShowReviewPR(pr))

		v.pr, v.base, v.unpushed = pr, "main", unpushed
		v.res.Head = prHeadOID
		v.src = git.PRSource{Worktree: pr.Root, Number: pr.Number}
		v.cursor = rowWith(t, v, "a review pane needs an agent pane")

		info := core.PRInfo{
			Number: 7, Title: "Show the pull request over its diff", Author: "mhersson", State: prOpenState,
			HeadBranch: "feat/review-pane", HeadOID: prHeadOID, BaseBranch: "main",
			ChecksTotal: 4, ChecksDone: 4, ReviewDecision: "APPROVED", UpdatedAt: t0.Add(-3 * time.Hour),
		}
		if tune != nil {
			tune(&info)
		}

		openIn(t, a, core.SetRepoPRs{Repo: prRepoKey, DefaultBranch: "main", Open: []core.PRInfo{info}})
	}
}

// wrappedResult is turn4Result under a file of long Go lines, so the first
// screen shows them: a call that wraps
// at a space, a word past the reach, a tab indented line and one with a
// hidden character.
func wrappedResult() git.Result {
	res := turn4Result()
	f := fileSpec("internal/ui/wrapdemo.go", git.Added, hunkSpec(0, 1, "",
		"+package ui",
		"+",
		"+func wrapDemo(s string, width int) string {",
		"+\treturn ansi.Truncate(strings.TrimSpace(strings.ReplaceAll(s, \"\\n\", \" \")), width-len(ellipsis)-padding, ellipsis) + strings.Repeat(\" \", max(width-ansi.StringWidth(s), 0))",
		"+\t// averyveryveryverylongcommentwordwithoutanyspacesatallsothatitcannotbebrokenatawordboundaryandwrapsattheedgeofthecolumnwhereverthatis",
		"+\tconst hidden = \"zero\u200bwidth \u202eand a bidi mark, then enough plain text after them to pass the column and wrap onto a second row\"",
		"+\t"+strings.Repeat("budget ", 160),
		"+\treturn s",
		"+}",
	))
	res.Files = append([]git.FileDiff{f}, res.Files...)

	return res
}
