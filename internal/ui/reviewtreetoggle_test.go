package ui

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/mhersson/aim/internal/core"
)

// treeStart is the state a toggle test begins in.
type treeStart int

const (
	treeStartHidden treeStart = iota // the tree is hidden
	treeStartIdle                    // the tree is shown, the diff has the keyboard
	treeStartKeyed                   // the tree is shown and has the keyboard
)

// treeWindows are windows whose drawer, half the window wide, has 120 and 58
// inner columns.
var treeWindows = []struct {
	name string
	w, h int
	want int // the drawer's inner width
}{
	{"120 inner columns", 244, 40, 120},
	{"58 inner columns", 100, 30, 58},
}

// treeRig opens the drawer on the keys repository's shell in a w x h window with
// the tree in state from. A shell pane has branch mode only, so the mode is the
// same whatever the configured default.
func treeRig(t *testing.T, w, h int, from treeStart) (*drawerRig, *reviewView) {
	t.Helper()

	r, _ := fakeKeysRig(t)

	return treeRigOn(t, r, w, h, from)
}

func treeRigOn(t *testing.T, r *drawerRig, w, h int, from treeStart) (*drawerRig, *reviewView) {
	t.Helper()

	sized(t, r.App, w, h)

	v := r.opened(t)

	if m := r.store.State().Tab().Review.Mode; m != core.ReviewBranch {
		t.Fatalf("setup: the drawer is in %s mode, want branch", m)
	}

	if from == treeStartHidden && r.layout(t).treeW != 0 {
		t.Fatalf("setup: a newly opened drawer shows its tree (width %d)", r.layout(t).treeW)
	}

	// Start inside a file, so that a jump to a file's header moves the cursor.
	r.keys(keyRune(']'))

	switch from {
	case treeStartIdle:
		r.showTree(t)
	case treeStartKeyed:
		// The tree lists dir/sub/c.txt, dir/b.txt, a.txt, z.txt; the cursor is in
		// dir/sub/c.txt, so one j selects dir/b.txt, a file the cursor is not in.
		r.keys(keyTab, keyRune('j'))
	}

	return r, v
}

func TestReviewTreeToggle(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		narrow   bool // the 58 inner column window, else the 120 one
		from     treeStart
		key      tea.KeyPressMsg
		wantOpen bool // the tree is open in core afterwards
		wantKeys bool // and the tree has the keyboard
		jumps    bool // the cursor goes to the selected file's header
	}{
		{"tab in the diff shows a hidden tree and gives it the keyboard", false, treeStartHidden, keyTab, true, true, false},
		{"tab in the diff shows a hidden tree in a narrow drawer", true, treeStartHidden, keyTab, true, true, false},
		{"tab in the diff gives a shown tree the keyboard", false, treeStartIdle, keyTab, true, true, false},
		{"tab in the tree hides it and keeps the cursor", false, treeStartKeyed, keyTab, false, false, false},
		{"enter in the tree jumps to the file and leaves it shown", false, treeStartKeyed, keyEnter, true, false, true},
		{"esc in the tree keeps the cursor and leaves it shown", false, treeStartKeyed, keyEsc, true, false, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			win := treeWindows[0]
			if tt.narrow {
				win = treeWindows[1]
			}

			r, v := treeRig(t, win.w, win.h, tt.from)

			lay := r.layout(t)
			if lay.innerW != win.want {
				t.Fatalf("setup: inner width %d, want %d", lay.innerW, win.want)
			}

			// A newly opened drawer draws no tree, and its diff takes the whole inner width.
			if tt.from == treeStartHidden {
				if lay.treeW != 0 || lay.diffW != lay.innerW {
					t.Fatalf("tree width %d and diff width %d in %d inner columns, want no tree and the whole width", lay.treeW, lay.diffW, lay.innerW)
				}

				if got := r.drawn(t); strings.Contains(got, "4 files") {
					t.Fatalf("the tree's totals row is drawn:\n%s", got)
				}
			}

			cursor, selected := v.cursor, v.treeSel

			r.keys(tt.key)

			if got := treeIsOpen(r.App); got != tt.wantOpen {
				t.Errorf("tree open in core = %v, want %v", got, tt.wantOpen)
			}

			if got := r.layout(t).treeW > 0; got != tt.wantOpen {
				t.Errorf("tree drawn = %v, want %v", got, tt.wantOpen)
			}

			if r.drawer.tree != tt.wantKeys || !r.drawer.focused {
				t.Errorf("tree has the keyboard = %v, drawer has it = %v, want %v and true", r.drawer.tree, r.drawer.focused, tt.wantKeys)
			}

			switch {
			case tt.jumps && (fileAtCursor(v) != "dir/b.txt" || v.rows[v.cursor].kind != rowFile):
				t.Errorf("cursor on %s (kind %d), want dir/b.txt's header", fileAtCursor(v), v.rows[v.cursor].kind)
			case !tt.jumps && v.cursor != cursor:
				t.Errorf("cursor moved from %d to %d, want it where it was", cursor, v.cursor)
			}

			if tt.from == treeStartHidden && v.treeSel != v.rows[v.cursor].file {
				t.Errorf("tree selects file %d, want the cursor's file %d", v.treeSel, v.rows[v.cursor].file)
			}

			if tt.from == treeStartKeyed && v.treeSel != selected {
				t.Errorf("selection moved from file %d to %d", selected, v.treeSel)
			}
		})
	}
}

// A drawer reopened, on the same pane or on another, starts with the tree closed.
func TestReopenedDrawerStartsWithTheTreeClosed(t *testing.T) {
	t.Parallel()
	t.Run("another pane", func(t *testing.T) {
		r, dir := fakeKeysRig(t)

		// The drawer reviews the right pane, which it covers; p1 stays in view.
		right := r.openShell(t, dir, core.SplitVertical)
		openIn(t, r.App, core.FocusPane{ID: right})
		r.opened(t)
		r.keys(keyTab)

		if !treeIsOpen(r.App) {
			t.Fatal("setup: tab did not open the tree")
		}

		openIn(t, r.App, core.FocusPane{ID: "p1"})
		r.drawerRelease()
		r.leader('d')
		r.until(t, "the drawer to retarget", func() bool { return r.store.State().Tab().Review.Pane == "p1" })
		r.idle(t)

		if treeIsOpen(r.App) || r.drawer.tree || r.layout(t).treeW != 0 {
			t.Errorf("after retargeting: tree open = %v, tree has the keyboard = %v, tree drawn = %v, want a closed tree",
				treeIsOpen(r.App), r.drawer.tree, r.layout(t).treeW != 0)
		}
	})

	t.Run("same pane", func(t *testing.T) {
		r, _ := treeRig(t, 200, 50, treeStartIdle)

		// The first leader d closes the drawer and the second opens it again.
		r.leader('d')
		r.finishSlide()

		if r.store.State().Tab().Review != nil {
			t.Fatal("setup: leader d left the drawer open")
		}

		r.opened(t)

		if treeIsOpen(r.App) || r.layout(t).treeW != 0 {
			t.Errorf("after reopening: tree open = %v, tree drawn = %v, want a closed tree", treeIsOpen(r.App), r.layout(t).treeW != 0)
		}
	})
}

// Each tab's drawer keeps its own tree, and the keyboard never stays in a tree
// that its tab's drawer does not show.
func TestReviewTreeIsPerTab(t *testing.T) {
	t.Parallel()
	r, dir := fakeKeysRig(t)
	r.opened(t)
	r.keys(keyTab)

	if !r.drawer.tree || !treeIsOpen(r.App) {
		t.Fatal("setup: tab did not open the tree in the first tab")
	}

	// A new tab by the operator takes the keyboard from the drawer.
	r.leader('c')
	r.openShell(t, dir, core.SplitVertical)

	if r.drawer.tree || r.drawer.focused {
		t.Errorf("in the new tab: tree has the keyboard = %v, drawer has it = %v, want neither", r.drawer.tree, r.drawer.focused)
	}

	r.opened(t)

	if st := r.store.State(); st.Active != 1 || st.Tabs[0].Review == nil || !st.Tabs[0].Review.Tree || st.Tabs[1].Review == nil || st.Tabs[1].Review.Tree {
		t.Fatalf("tab %d, trees open = %v, want the second tab active, its tree closed and the first tab's open", st.Active, treesOf(st))
	}

	// Back in the first tab, whose tree is open, with the keyboard in it. The drawer
	// still has the keyboard from the second tab.
	selectTab := func(i int) {
		t.Helper()
		openIn(t, r.App, core.SelectTab{Index: i})
		r.settle()
	}

	selectTab(0)

	if r.drawer.tree || !treeIsOpen(r.App) {
		t.Fatalf("setup: first tab: tree has the keyboard = %v, tree open = %v, want its tree open and the keyboard not in it", r.drawer.tree, treeIsOpen(r.App))
	}

	r.keys(keyTab)

	if !r.drawer.tree || !treeIsOpen(r.App) {
		t.Fatalf("setup: first tab: tree has the keyboard = %v, tree open = %v, want both", r.drawer.tree, treeIsOpen(r.App))
	}

	// Switching tabs by anything but a key leaves the keyboard flag where it was,
	// so the tab whose tree is closed must not inherit it.
	selectTab(1)

	if r.drawer.tree || treeIsOpen(r.App) || r.layout(t).treeW != 0 {
		t.Errorf("second tab: tree has the keyboard = %v, tree open = %v, tree drawn = %v, want a closed tree",
			r.drawer.tree, treeIsOpen(r.App), r.layout(t).treeW != 0)
	}

	selectTab(0)

	if r.drawer.tree || !treeIsOpen(r.App) || r.layout(t).treeW == 0 {
		t.Errorf("first tab again: tree has the keyboard = %v, tree open = %v, tree drawn = %v, want its tree open and the keyboard not in it",
			r.drawer.tree, treeIsOpen(r.App), r.layout(t).treeW != 0)
	}
}

// treesOf is each tab's tree state for a failure message; "none" is no drawer.
func treesOf(st core.State) []string {
	var out []string

	for _, tab := range st.Tabs {
		switch {
		case tab.Review == nil:
			out = append(out, "none")
		case tab.Review.Tree:
			out = append(out, "open")
		default:
			out = append(out, "closed")
		}
	}

	return out
}

// A drawer narrowed by a resize keeps its open tree, in the tree's width rule,
// whether the tree or the diff has the keyboard.
func TestResizeKeepsTheTreeOpen(t *testing.T) {
	t.Parallel()

	for _, tt := range []struct {
		name     string
		keys     []tea.KeyPressMsg
		wantKeys bool // the tree has the keyboard
	}{
		{"the tree has the keyboard", []tea.KeyPressMsg{keyTab}, true},
		{"the diff has the keyboard", []tea.KeyPressMsg{keyTab, keyEsc}, false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			r, _ := treeRig(t, 244, 40, treeStartHidden)
			r.keys(tt.keys...)

			wide := r.layout(t)
			if wide.treeW == 0 || r.drawer.tree != tt.wantKeys {
				t.Fatalf("setup: tree width %d, tree has the keyboard = %v, want a tree and %v", wide.treeW, r.drawer.tree, tt.wantKeys)
			}

			sized(t, r.App, 100, 30)

			lay := r.layout(t)
			if !treeIsOpen(r.App) || r.drawer.tree != tt.wantKeys {
				t.Errorf("after the resize: tree open = %v, tree has the keyboard = %v, want true and %v", treeIsOpen(r.App), r.drawer.tree, tt.wantKeys)
			}

			if lay.innerW != 58 {
				t.Fatalf("setup: inner width %d after the resize, want 58", lay.innerW)
			}

			// At least 20 columns, at most 35 % of the inner width unless 20 is more.
			if limit := max(treeMinWidth, lay.innerW*treeMaxShare/100); lay.treeW < treeMinWidth || lay.treeW > limit || lay.diffW != lay.innerW-lay.treeW-1 {
				t.Errorf("tree width %d and diff width %d in %d inner columns, want a tree of %d to %d columns and the diff beside it",
					lay.treeW, lay.diffW, lay.innerW, treeMinWidth, limit)
			}

			sized(t, r.App, 244, 40)

			if got := r.layout(t); got != wide {
				t.Errorf("after growing back the layout is %+v, want %+v", got, wide)
			}
		})
	}
}

// Where a hidden tree would be, a click lands in the diff.
func TestClickWhereAHiddenTreeWouldBe(t *testing.T) {
	t.Parallel()
	r, v := treeRig(t, 200, 50, treeStartHidden)
	r.leader('h')

	// Row 8 of the drawer is where a tree lists z.txt, after the mode and totals rows and a blank.
	x, y := r.drawerCell(t, 2, 1+3+5)

	click(r.App, x, y)

	if v.cursor != 3+5 || fileAtCursor(v) == "z.txt" || treeIsOpen(r.App) {
		t.Errorf("a click on row 8 of a hidden tree: cursor = %d in %s, tree open = %v, want the diff's row 8 and a hidden tree",
			v.cursor, fileAtCursor(v), treeIsOpen(r.App))
	}

	if !r.drawer.focused || r.drawer.tree {
		t.Errorf("after the click: drawer has the keyboard = %v, tree has it = %v, want the diff to have it", r.drawer.focused, r.drawer.tree)
	}
}

// A state saved with the tree open draws it, at any width.
func TestRestoredTreeIsDrawn(t *testing.T) {
	t.Parallel()

	st := hiddenDrawerState()
	st.Active = 0
	st.Tabs[0].Review.Tree = true

	r := restoreApp(t, st)
	r.size(100, 30)

	a := r.a
	state := a.store.State()

	box, ok := a.drawerFull(state)
	if !ok {
		t.Fatal("no drawer")
	}

	v := a.drawer.views[state.Tab().ID]
	if v == nil {
		t.Fatal("the restored drawer has no view")
	}

	lay := a.drawerLayoutOf(box, v, drawerTree(state))
	if lay.treeW == 0 || lay.innerW >= 80 {
		t.Fatalf("tree width %d in %d inner columns, want a tree in a drawer under 80", lay.treeW, lay.innerW)
	}

	rect, _ := a.drawerRect(state)
	rows := a.drawerLines(state, rect)

	if row := []rune(ansi.Strip(rows[1])); row[1+lay.treeW] != '│' {
		t.Errorf("the tree's rule is missing from column %d: %q", 1+lay.treeW, string(row))
	}
}
