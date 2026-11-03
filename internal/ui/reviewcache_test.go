package ui

import (
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/mhersson/aim/internal/core"
	"github.com/mhersson/aim/internal/git"
)

func cacheScene() reviewScene {
	return reviewScene{theme: "catppuccin-mocha", icons: true, w: 200, h: 52, res: turn4Result(), turn: 4, turns: 4, focused: true}
}

// An update that changes nothing the drawer reads must neither lay it out nor
// search its rows again, and drawing a frame must not either.
func TestDrawerIdleUpdatesDoNoWork(t *testing.T) {
	t.Parallel()
	a := cacheScene().build(t)

	layouts, scans := a.drawer.layouts, a.drawer.scans

	for range 3 {
		a.Update(struct{}{})
		a.Update(tea.WindowSizeMsg{Width: 200, Height: 52})
	}

	st := a.store.State()
	r, _ := a.drawerRect(st)
	a.drawerLines(st, r)
	a.View()

	if got := a.drawer.layouts - layouts; got != 0 {
		t.Errorf("%d layouts computed by updates and frames that changed nothing, want 0", got)
	}

	if got := a.drawer.scans - scans; got != 0 {
		t.Errorf("%d searches for chunks to tokenise by updates that changed nothing, want 0", got)
	}
}

// Every input of the layout must make it compute again, and the layout it then
// uses must be the one a cold cache gives.
func TestDrawerLayoutFollowsItsInputs(t *testing.T) {
	t.Parallel()

	app := appGoFull()
	longFull := newFullText(append(app.old, make([]string, 20000)...), append(app.new, make([]string, 20000)...), nil)

	tests := []struct {
		name   string
		scene  func(s *reviewScene)
		change func(t *testing.T, a *App, v *reviewView)
	}{
		{"a new generation with other files", nil, func(t *testing.T, a *App, v *reviewView) {
			v.res, v.gen = edgeResult(), v.gen+1
			v.rebuild(v.split)
		}},
		{"a narrower window", nil, func(t *testing.T, a *App, v *reviewView) { sized(t, a, 160, 48) }},
		{"a window under 80 inner columns", nil, func(t *testing.T, a *App, v *reviewView) { sized(t, a, 100, 30) }},
		{"a taller window", nil, func(t *testing.T, a *App, v *reviewView) { sized(t, a, 200, 60) }},
		{"the rail opens", nil, func(t *testing.T, a *App, v *reviewView) { openIn(t, a, core.SetRail{Open: true}) }},
		{"a wider mode row", nil, func(t *testing.T, a *App, v *reviewView) {
			v.src, v.base = git.BranchSource{Worktree: "/w/p1"}, "origin/a-base-branch-with-a-very-long-name"
		}},
		{"more turns", nil, func(t *testing.T, a *App, v *reviewView) { v.turns = 12345 }},
		{"the tree opens", nil, func(t *testing.T, a *App, v *reviewView) { openIn(t, a, core.SetReviewTree{Open: true}) }},
		{"the tree opens in a narrow drawer", func(s *reviewScene) { s.w = 100 }, func(t *testing.T, a *App, v *reviewView) {
			openIn(t, a, core.SetReviewTree{Open: true})
		}},
		{"the tree closes", func(s *reviewScene) { s.tree = true }, func(t *testing.T, a *App, v *reviewView) {
			openIn(t, a, core.SetReviewTree{Open: false})
		}},
		{"side by side is wanted", nil, func(t *testing.T, a *App, v *reviewView) { v.split = true }},
		{"a longer file shown whole", nil, func(t *testing.T, a *App, v *reviewView) {
			v.full = map[string]fullText{appGoPath: longFull}
			v.fullOn = allWhole(v.full)
			v.rebuild(v.split)
		}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			s := cacheScene()
			if tt.scene != nil {
				tt.scene(&s)
			}

			a := s.build(t)
			v := a.reviewViewOf(a.store.State().Tab().ID)

			before := a.drawer.layouts

			tt.change(t, a, v)
			settleDrawer(t, a)

			if a.drawer.layouts == before {
				t.Fatal("the layout was not computed again")
			}

			st := a.store.State()

			box, ok := a.drawerFull(st)
			if !ok {
				t.Fatal("no drawer")
			}

			got := a.drawerLayoutOf(box, v, drawerTree(st))

			v.cache = reviewCache{}

			if want := a.drawerLayoutOf(box, v, drawerTree(st)); got != want {
				t.Errorf("the cached layout is %+v, a cold cache gives %+v", got, want)
			}
		})
	}
}

// The keyboard moving into the tree draws it differently but lays nothing out
// again: the layout follows the tree being open, not who has the keyboard.
func TestDrawerLayoutIgnoresTheTreeKeyboard(t *testing.T) {
	t.Parallel()

	s := cacheScene()
	s.tree = true
	a := s.build(t)

	layouts := a.drawer.layouts

	a.drawer.tree = true

	settleDrawer(t, a)

	if got := a.drawer.layouts - layouts; got != 0 {
		t.Errorf("%d layouts computed after the keyboard moved into the tree, want 0", got)
	}
}

// The search for chunks to tokenise follows the rows in range: a view that
// moves or a new generation makes it run, a cursor that stays in view does not.
func TestDrawerScanFollowsTheView(t *testing.T) {
	t.Parallel()

	res, _ := bigResult(600)
	s := reviewScene{theme: "catppuccin-mocha", icons: true, w: 200, h: 52, res: res, turn: 1, turns: 1, focused: true}
	a := s.build(t)
	v := a.reviewViewOf(a.store.State().Tab().ID)

	scans := a.drawer.scans

	v.cursor++

	settleDrawer(t, a)

	if got := a.drawer.scans - scans; got != 0 {
		t.Errorf("%d scans after the cursor moved within the view, want 0", got)
	}

	v.cursor += hlChunk + 8 // into the second chunk

	a.drawerAfterUpdate()

	if got := a.drawer.scans - scans; got != 1 {
		t.Errorf("%d scans after the view moved, want 1", got)
	}

	if a.drawer.tokenised == 0 {
		t.Error("the rows the view moved to were not tokenised")
	}

	settleDrawer(t, a)

	scans = a.drawer.scans
	v.gen++

	if cmd := a.drawerAfterUpdate(); cmd != nil && a.drawer.tokenised == 0 {
		t.Error("a pass asked for a redraw and tokenised nothing")
	}

	if got := a.drawer.scans - scans; got != 1 {
		t.Errorf("%d scans after a new generation, want 1", got)
	}

	if a.drawer.tokenised == 0 {
		t.Error("a new generation dropped the spans but nothing was tokenised again")
	}
}
