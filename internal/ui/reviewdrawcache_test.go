package ui

import (
	"errors"
	"math/rand/v2"
	"reflect"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/mhersson/aim/internal/core"
	"github.com/mhersson/aim/internal/git"
	"github.com/mhersson/aim/internal/ui/theme"
)

// Frames and updates that change nothing the drawer shows must not draw it
// again.
func TestDrawerIdleFramesReuseTheirLines(t *testing.T) {
	t.Parallel()
	a := cacheScene().build(t)
	a.View()

	renders := a.drawer.renders

	for range 3 {
		a.Update(struct{}{})
		a.Update(tea.WindowSizeMsg{Width: 200, Height: 52})
		a.View()
	}

	if got := a.drawer.renders - renders; got != 0 {
		t.Errorf("%d drawers drawn for frames that changed nothing, want 0", got)
	}
}

// drawerRectOf is the drawer's rect on screen, narrowed by shrink columns as a
// slide narrows it.
func drawerRectOf(t *testing.T, a *App, shrink int) core.Rect {
	t.Helper()

	r, ok := a.drawerRect(a.store.State())
	if !ok {
		t.Fatal("no drawer on screen")
	}

	r.W -= shrink

	return r
}

// coldLines draws the drawer for r without the cache.
func coldLines(t *testing.T, a *App, r core.Rect) string {
	t.Helper()

	d, _, ok := a.drawCtxFor(a.store.State())
	if !ok {
		t.Fatal("no drawer to draw")
	}

	return strings.Join(d.lines(r), "\n")
}

func advanceClock(a *App, d time.Duration) { a.clock.(*fakeClock).Advance(d) }

// flashRow makes the row holding sub flash until 600 ms after t0.
func flashRow(t *testing.T, v *reviewView, sub string) {
	t.Helper()

	v.fresh = map[int]bool{rowWith(t, v, sub): true}
	v.flashUntil = t0.Add(600 * time.Millisecond)
}

// edited is turn4Result with one added line changed, as a live update brings it.
func edited() git.Result {
	res := turn4Result()
	res.Files[1].Hunks[0].Lines[3].Text = "\tif a.store.Pane(agent).Kind == core.KindShell {"

	return res
}

// Each input of the drawing must make it draw again, and what it then shows
// must be what a cold draw gives; with nothing more changed the next frame
// reuses it.
func TestDrawerLinesFollowTheirInputs(t *testing.T) {
	t.Parallel()

	big, _ := bigResult(200)

	small := func(s *reviewScene) { s.tree = true }
	noTree := func(s *reviewScene) {}
	treeFocus := func(s *reviewScene) {
		s.tree = true
		s.tune = func(t *testing.T, a *App, v *reviewView) { a.drawer.tree = true }
	}
	whole := func(t *testing.T, a *App, v *reviewView) {
		v.full = map[string]fullText{appGoPath: appGoFull()}
		v.fullOn = allWhole(v.full)
		v.gen++
		v.rebuild(v.split)
	}
	searching := func(t *testing.T, a *App, v *reviewView) { press(a, keyRune('/'), keyRune('t')) }

	tests := []struct {
		name    string
		scene   func(s *reviewScene)
		setup   func(t *testing.T, a *App, v *reviewView) // before the first frame
		change  func(t *testing.T, a *App, v *reviewView)
		shrink  int  // columns the slide takes off the drawer after the change
		settle  bool // run the update's drawer work after the change
		differs bool // what is drawn must change
	}{
		{name: "the cursor moves", scene: small, change: func(t *testing.T, a *App, v *reviewView) { v.cursor++ }, differs: true},
		{name: "the view scrolls", scene: func(s *reviewScene) {
			s.res, s.turn = big, 1
			s.tune = func(t *testing.T, a *App, v *reviewView) { v.cursor, v.top = 50, 40 }
		}, change: func(t *testing.T, a *App, v *reviewView) { v.top = 45 }, differs: true},
		{name: "the window narrows", scene: small, change: func(t *testing.T, a *App, v *reviewView) { sized(t, a, 160, 48) }, settle: true, differs: true},
		{name: "the window is under 80 inner columns", scene: small, change: func(t *testing.T, a *App, v *reviewView) { sized(t, a, 100, 30) }, settle: true, differs: true},
		{name: "the window grows taller", scene: small, change: func(t *testing.T, a *App, v *reviewView) { sized(t, a, 200, 60) }, settle: true, differs: true},
		{name: "the rail opens", scene: small, change: func(t *testing.T, a *App, v *reviewView) { openIn(t, a, core.SetRail{Open: true}) }, settle: true, differs: true},
		{name: "the drawer slides", scene: small, change: func(t *testing.T, a *App, v *reviewView) {}, shrink: 9, differs: true},
		{name: "the keyboard leaves the drawer", scene: small, change: func(t *testing.T, a *App, v *reviewView) { a.drawer.focused = false }, differs: true},
		{name: "the tree opens", scene: noTree, change: func(t *testing.T, a *App, v *reviewView) {
			openIn(t, a, core.SetReviewTree{Open: true})
		}, settle: true, differs: true},
		{name: "the tree closes", scene: small, change: func(t *testing.T, a *App, v *reviewView) {
			openIn(t, a, core.SetReviewTree{Open: false})
		}, settle: true, differs: true},
		{name: "the tree takes the keyboard", scene: small, change: func(t *testing.T, a *App, v *reviewView) { a.drawer.tree = true }, differs: true},
		{name: "the tree's selection moves", scene: treeFocus, change: func(t *testing.T, a *App, v *reviewView) { v.treeSel = 2 }, differs: true},
		{name: "a flash starts", scene: small, change: func(t *testing.T, a *App, v *reviewView) { flashRow(t, v, "review.toggle") }, differs: true},
		{name: "a flash ends", scene: func(s *reviewScene) {
			s.tune = func(t *testing.T, a *App, v *reviewView) { flashRow(t, v, "review.toggle") }
		}, change: func(t *testing.T, a *App, v *reviewView) { advanceClock(a, 600*time.Millisecond) }, differs: true},
		{name: "a highlight answer adds spans", scene: small, setup: func(t *testing.T, a *App, v *reviewView) {
			v.gen++
			v.rebuild(v.split)
		}, change: func(t *testing.T, a *App, v *reviewView) {
			a.drawerAfterUpdate()

			if !runHighlight(t, a) {
				t.Fatal("no batch was started")
			}
		}, differs: true},
		{name: "a live result arrives", scene: small, change: func(t *testing.T, a *App, v *reviewView) {
			res := edited()
			a.reviewApply(v, reviewLoadedMsg{tab: a.store.State().Tab().ID, res: res, live: true, info: reviewInfo{project: "multiplex", branch: "feat/review-pane", base: "origin/main", turns: 4, hidden: hiddenFlags(res.Files)}})
		}, differs: true},
		{name: "a new generation", scene: small, change: func(t *testing.T, a *App, v *reviewView) {
			v.gen++
			v.rebuildFor(edgeResult(), v.split)
		}, differs: true},
		{name: "the search prompt opens", scene: small, change: func(t *testing.T, a *App, v *reviewView) { press(a, keyRune('/')) }, differs: true},
		{name: "a letter is typed in the search", scene: small, setup: func(t *testing.T, a *App, v *reviewView) { press(a, keyRune('/')) }, change: searching, differs: true},
		{name: "the search is confirmed", scene: small, setup: searching, change: func(t *testing.T, a *App, v *reviewView) {
			press(a, tea.KeyPressMsg{Code: tea.KeyEnter})
		}, differs: true},
		{name: "side by side is wanted", scene: small, change: func(t *testing.T, a *App, v *reviewView) { v.split = true }, settle: true, differs: true},
		{name: "a file is shown whole", scene: small, change: whole, differs: true},
		{name: "a block is folded", scene: small, setup: whole, change: func(t *testing.T, a *App, v *reviewView) {
			v.folded = map[string]bool{foldKey(appGoPath, 0): true}
			v.rebuild(v.split)
		}, differs: true},
		{name: "the theme changes", scene: small, change: func(t *testing.T, a *App, v *reviewView) {
			openIn(t, a, core.SetTheme{Name: "catppuccin-latte"})
		}, differs: true},
		{name: "a theme is previewed", scene: small, change: func(t *testing.T, a *App, v *reviewView) {
			lk := a.current()
			lk.theme = mustTheme(t, "catppuccin-frappe")
			a.preview = &lk
		}, differs: true},
		{name: "the icons go off", scene: small, change: func(t *testing.T, a *App, v *reviewView) { a.icons = false }, differs: true},
		{name: "the turn count changes", scene: small, change: func(t *testing.T, a *App, v *reviewView) { v.turns = 9 }, differs: true},
		{name: "the source changes", scene: small, change: func(t *testing.T, a *App, v *reviewView) {
			v.src, v.base = git.BranchSource{Worktree: "/w/p1"}, "origin/dev"
		}, differs: true},
		{name: "the project and branch change", scene: small, change: func(t *testing.T, a *App, v *reviewView) { v.project, v.branch = "other", "fix/it" }, differs: true},
		{name: "the base changes", scene: func(s *reviewScene) { s.branch, s.tree = true, true }, change: func(t *testing.T, a *App, v *reviewView) { v.base = "origin/dev" }, differs: true},
		{name: "a branch drawer starts to show a turn", scene: func(s *reviewScene) { s.branch, s.tree = true, true }, change: func(t *testing.T, a *App, v *reviewView) {
			v.src = git.TurnSource{Worktree: "/w/p1", Session: "s", Turn: 4}
		}, differs: true},
		{name: "the turn has no snapshot", scene: func(s *reviewScene) { s.res, s.turn, s.tree = git.Result{Turn: 3}, 3, true }, change: func(t *testing.T, a *App, v *reviewView) { v.noSnapshot = true }, differs: true},
		{name: "the load fails", scene: func(s *reviewScene) { s.res, s.branch = git.Result{}, true }, change: func(t *testing.T, a *App, v *reviewView) { v.err = errors.New("git: boom") }, differs: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			s := cacheScene()
			tt.scene(&s)

			a := s.build(t)
			v := a.reviewViewOf(a.store.State().Tab().ID)

			if tt.setup != nil {
				tt.setup(t, a, v)
			}

			before := strings.Join(a.drawerLines(a.store.State(), drawerRectOf(t, a, 0)), "\n")
			renders := a.drawer.renders

			tt.change(t, a, v)

			if tt.settle {
				settleDrawer(t, a)
			}

			r := drawerRectOf(t, a, tt.shrink)
			got := strings.Join(a.drawerLines(a.store.State(), r), "\n")

			if a.drawer.renders != renders+1 {
				t.Fatalf("%d drawers drawn after the change, want 1", a.drawer.renders-renders)
			}

			if want := coldLines(t, a, r); got != want {
				t.Error("the lines drawn after the change are not what a cold draw gives")
			}

			if tt.differs && got == before {
				t.Error("the change did not show: the test does not exercise its input")
			}

			again := strings.Join(a.drawerLines(a.store.State(), r), "\n")
			if a.drawer.renders != renders+1 || again != got {
				t.Errorf("a frame after nothing changed drew the drawer again (%d) or differently", a.drawer.renders-renders-1)
			}
		})
	}
}

// Whatever order the inputs change in, the lines a frame shows are the ones a
// cold draw gives, on the frame that draws them and on the next.
// It compares highlighted colours, so it runs serially (docs/gotchas.md).
func TestDrawerLinesStayTrueToAColdDraw(t *testing.T) {
	a := cacheScene().build(t)
	v := a.reviewViewOf(a.store.State().Tab().ID)
	rng := rand.New(rand.NewPCG(7, 11))

	sizes := [][2]int{{200, 52}, {160, 48}, {100, 30}}
	themes := theme.Names()
	results := []git.Result{turn4Result(), edited(), edgeResult()}

	searchKeys := []tea.KeyPressMsg{keyRune('t'), keyRune('r'), {Code: tea.KeyBackspace}, {Code: tea.KeyEnter}, keyEsc}

	ops := []struct {
		name string
		do   func()
	}{
		{"cursor", func() { v.cursor = rng.IntN(max(len(v.rows), 1)) }},
		{"scroll", func() { v.top = rng.IntN(max(len(v.rows), 1)) }},
		{"line keys", func() {
			if a.drawer.focused && !a.drawer.tree && v.search == nil {
				press(a, keyRune([]rune("jk")[rng.IntN(2)]))
			}
		}},
		{"resize", func() { sz := sizes[rng.IntN(len(sizes))]; sized(t, a, sz[0], sz[1]) }},
		{"rail", func() { openIn(t, a, core.SetRail{Open: !a.store.State().Rail}) }},
		{"keyboard", func() { a.drawer.focused = !a.drawer.focused }},
		{"tree open", func() {
			openIn(t, a, core.SetReviewTree{Open: !treeIsOpen(a)})

			if !treeIsOpen(a) {
				a.drawer.tree = false
			}
		}},
		{"tree keyboard", func() { a.drawer.tree = treeIsOpen(a) && !a.drawer.tree }},
		{"tree selection", func() { v.treeSel = rng.IntN(max(len(v.res.Files), 1)) }},
		{"split", func() { v.split = !v.split }},
		{"whole file", func() {
			v.full = map[string]fullText{appGoPath: appGoFull()}
			v.fullOn = allWhole(v.full)

			if rng.IntN(2) == 0 {
				v.full = nil
			}

			v.gen++
			v.rebuild(v.split)
		}},
		{"theme", func() { openIn(t, a, core.SetTheme{Name: themes[rng.IntN(len(themes))]}) }},
		{"icons", func() { a.icons = !a.icons }},
		{"flash", func() {
			if len(v.rows) > 0 {
				v.fresh = map[int]bool{rng.IntN(len(v.rows)): true}
				v.flashUntil = a.clock.Now().Add(600 * time.Millisecond)
			}
		}},
		{"time", func() { advanceClock(a, 300*time.Millisecond) }},
		{"search", func() {
			a.drawer.focused, a.drawer.tree = true, false

			if v.search == nil {
				press(a, keyRune('/'))

				return
			}

			press(a, searchKeys[rng.IntN(len(searchKeys))])
		}},
		{"live result", func() {
			res := results[rng.IntN(len(results))]
			a.reviewApply(v, reviewLoadedMsg{tab: a.store.State().Tab().ID, res: res, live: true, info: reviewInfo{project: "multiplex", branch: "feat/review-pane", base: "origin/main", turns: 4, hidden: hiddenFlags(res.Files)}})
		}},
		{"new result", func() {
			v.gen++
			v.rebuildFor(results[rng.IntN(len(results))], v.split)

			v.treeSel = 0
		}},
		{"highlight pass", func() {
			v.gen++
			v.rebuild(v.split)
		}},
	}

	for step := range 100 {
		op := ops[rng.IntN(len(ops))]
		op.do()
		settleDrawer(t, a)

		r := drawerRectOf(t, a, []int{0, 0, 0, 7, 25}[rng.IntN(5)])
		st := a.store.State()
		got := strings.Join(a.drawerLines(st, r), "\n")

		if want := coldLines(t, a, r); got != want {
			t.Fatalf("step %d (%s): the frame is not what a cold draw gives", step, op.name)
		}

		if again := strings.Join(a.drawerLines(st, r), "\n"); again != got {
			t.Fatalf("step %d (%s): the next frame differs from the one before it", step, op.name)
		}

		// Spans a live result kept must be what tokenising the chunk again gives.
		if op.name != "live result" && op.name != "new result" {
			continue
		}

		for k, held := range v.derived().spans {
			path, lines := v.chunkLines(k)
			if !reflect.DeepEqual(held, a.drawer.hl.Highlight(path, lines)) {
				t.Fatalf("step %d (%s): the spans held for %+v are not what tokenising it gives", step, op.name, k)
			}
		}
	}
}
