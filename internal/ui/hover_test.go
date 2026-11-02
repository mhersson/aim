package ui

import (
	"testing"

	tea "charm.land/bubbletea/v2"
)

// holder names who has the keyboard.
func holder(r *drawerRig) string {
	switch {
	case r.rail.focused && r.drawer.focused:
		return "both"
	case r.rail.focused:
		return "rail"
	case r.drawer.focused:
		return "drawer"
	default:
		return "pane"
	}
}

type hoverCells struct{ rail, railBorder, drawer, pane, border [2]int }

func cellsOf(t *testing.T, r *drawerRig) hoverCells {
	t.Helper()

	rail := r.railRect()
	box := mustRect(t, r)
	p := r.geometry().rects["p1"]
	c := contentRect(p)

	return hoverCells{
		rail:       [2]int{rail.X + 2, rail.Y + 2},
		railBorder: [2]int{rail.X, rail.Y + 3},
		drawer:     [2]int{box.X + box.W/2, box.Y + 4},
		pane:       [2]int{c.X + 1, c.Y + 1},
		border:     [2]int{c.X + 1, p.Y},
	}
}

func hoverRig(t *testing.T, w, h int) *drawerRig {
	t.Helper()

	r, _ := fakeKeysRig(t)
	r.opened(t)
	zoomLayout(t, r, w, h)
	r.focusFollows = true
	r.railRelease()
	r.drawerRelease()

	return r
}

func TestHoverFocus(t *testing.T) {
	t.Parallel()

	at := func(r *drawerRig, c [2]int) { hover(r.App, c[0], c[1]) }

	tests := []struct {
		name string
		w, h int
		run  func(t *testing.T, r *drawerRig, c hoverCells)
	}{
		{"rail takes it", 270, 75, func(t *testing.T, r *drawerRig, c hoverCells) {
			at(r, c.rail)
			wantHolder(t, r, "rail")
			at(r, c.railBorder)
			wantHolder(t, r, "rail")
		}},
		{"drawer takes it", 270, 75, func(t *testing.T, r *drawerRig, c hoverCells) {
			r.drawer.tree = true
			at(r, c.drawer)
			wantHolder(t, r, "drawer")

			if r.drawer.tree {
				t.Error("tree focused, want the diff")
			}
		}},
		{"tree keeps it", 270, 75, func(t *testing.T, r *drawerRig, c hoverCells) {
			r.drawerTake()
			r.drawer.tree = true
			at(r, c.drawer)
			wantHolder(t, r, "drawer")

			if !r.drawer.tree {
				t.Error("tree lost the keyboard to the diff")
			}
		}},
		{"focused pane takes it back", 270, 75, func(t *testing.T, r *drawerRig, c hoverCells) {
			for _, cell := range [][2]int{c.pane, c.border} {
				r.drawerTake()
				at(r, cell)
				wantHolder(t, r, "pane")

				if r.store.State().Tab().Review == nil {
					t.Error("drawer closed")
				}

				r.railTake()
				at(r, cell)
				wantHolder(t, r, "pane")
			}
		}},
		{"rail to drawer", 270, 75, func(t *testing.T, r *drawerRig, c hoverCells) {
			at(r, c.rail)
			at(r, c.drawer)
			wantHolder(t, r, "drawer")
		}},
		{"zoomed holds", 270, 75, func(t *testing.T, r *drawerRig, c hoverCells) {
			r.drawerTake()
			r.keys(keyRune('z'))
			r.finishSlide()
			at(r, c.rail)
			wantHolder(t, r, "drawer")

			if !r.zoomed() {
				t.Error("drawer un-zoomed")
			}
		}},
		{"zoomed at 80x24", 80, 24, func(t *testing.T, r *drawerRig, c hoverCells) {
			r.drawerTake()
			r.keys(keyRune('z'))
			r.finishSlide()
			at(r, c.rail)
			wantHolder(t, r, "drawer")
		}},
		{"half covering every pane trades", 100, 30, func(t *testing.T, r *drawerRig, c hoverCells) {
			at(r, c.rail)
			wantHolder(t, r, "rail")
			at(r, c.drawer)
			wantHolder(t, r, "drawer")
		}},
		{"rail search typed holds", 270, 75, func(t *testing.T, r *drawerRig, c hoverCells) {
			r.railTake()
			r.keys(keyRune('/'))
			at(r, c.drawer)
			wantHolder(t, r, "rail")

			if !r.rail.searching {
				t.Error("search ended")
			}
		}},
		{"drawer search open holds", 270, 75, func(t *testing.T, r *drawerRig, c hoverCells) {
			r.drawerTake()
			r.keys(keyRune('/'))
			r.typed("ab")
			at(r, c.pane)
			wantHolder(t, r, "drawer")

			if v := r.view(); v.search == nil || *v.search != "ab" {
				t.Errorf("search = %v, want ab", v.search)
			}
		}},
		{"closing drawer", 270, 75, func(t *testing.T, r *drawerRig, c hoverCells) {
			r.drawerTake()
			r.leader('d')

			if !r.drawer.closing {
				t.Fatal("setup: no close slide")
			}

			zoom := r.view().zoom
			at(r, c.drawer)
			wantHolder(t, r, "pane")

			if !r.drawer.closing || r.view().zoom != zoom {
				t.Errorf("closing = %v, zoom = %v, want closing and zoom %v", r.drawer.closing, r.view().zoom, zoom)
			}
		}},
		{"drawer text press holds", 270, 75, func(t *testing.T, r *drawerRig, c hoverCells) {
			r.drawerTake()
			r.mouse.text = &textPress{}
			at(r, c.pane)
			wantHolder(t, r, "drawer")
		}},
		{"modal", 270, 75, func(t *testing.T, r *drawerRig, c hoverCells) {
			r.openCommandPalette()
			at(r, c.rail)
			wantHolder(t, r, "pane")
		}},
		{"button held", 270, 75, func(t *testing.T, r *drawerRig, c hoverCells) {
			r.Update(tea.MouseClickMsg{X: c.pane[0], Y: c.pane[1], Button: tea.MouseLeft})
			at(r, c.rail)
			wantHolder(t, r, "pane")
		}},
		{"setting off", 270, 75, func(t *testing.T, r *drawerRig, c hoverCells) {
			r.focusFollows = false
			at(r, c.rail)
			wantHolder(t, r, "pane")
		}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			r := hoverRig(t, tt.w, tt.h)
			tt.run(t, r, cellsOf(t, r))
		})
	}
}

func wantHolder(t *testing.T, r *drawerRig, want string) {
	t.Helper()

	if got := holder(r); got != want {
		t.Errorf("keyboard holder = %s, want %s", got, want)
	}
}
