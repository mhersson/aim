package ui

import (
	"fmt"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/mhersson/aim/internal/core"
	"github.com/mhersson/aim/internal/git"
)

// keysRepo has four files in a nested tree, each with two edited hunks.
func keysRepo(t *testing.T) string {
	t.Helper()

	dir := newRepoDir(t)
	files := []string{"a.txt", "dir/b.txt", "dir/sub/c.txt", "z.txt"}

	for _, f := range files {
		write(t, filepath.Join(dir, f), numberedLines("line", 40))
	}

	commitEverything(t, dir, "init")

	for _, f := range files {
		write(t, filepath.Join(dir, f), editedLines(40, 3, 30))
	}

	return dir
}

// editedLines is n numbered lines with the ones named in edits changed.
func editedLines(n int, edits ...int) string {
	var b strings.Builder

	for i := 1; i <= n; i++ {
		if slices.Contains(edits, i) {
			fmt.Fprintf(&b, "edited %d\n", i)
		} else {
			fmt.Fprintf(&b, "line %d\n", i)
		}
	}

	return b.String()
}

// newKeysRig is a drawer rig whose first pane starts in dir.
func newKeysRig(t *testing.T, dir string, opts ...func(*Options)) *drawerRig {
	t.Helper()

	return newDrawerRig(t, append([]func(*Options){func(o *Options) { o.Cwd = dir }}, opts...)...)
}

// keys presses keys one at a time and runs the commands they return.
func (r *drawerRig) keys(ks ...tea.KeyPressMsg) {
	for _, k := range ks {
		r.run(r.step(k))
	}
}

func (r *drawerRig) leader(k rune) { r.keys(keyLeader, keyRune(k)) }

func (r *drawerRig) typed(s string) {
	for _, c := range s {
		r.keys(keyRune(c))
	}
}

// opened presses leader d, waits for the drawer's first diff and ends its slide.
func (r *drawerRig) opened(t *testing.T) *reviewView {
	t.Helper()

	r.leader('d')
	r.until(t, "the drawer to open", func() bool { return r.store.State().Tab().Review != nil })

	v := r.idle(t)
	r.finishSlide()

	return v
}

// fireSlide delivers the slide frames held so far and reports how many there were.
func (r *drawerRig) fireSlide() int {
	var rest []tea.Msg

	n := 0

	for _, m := range r.ticks.fire() {
		if _, ok := m.(drawerSlideMsg); ok {
			n++

			r.run(r.step(m))
		} else {
			rest = append(rest, m)
		}
	}

	r.ticks.msgs = append(rest, r.ticks.msgs...)

	return n
}

// finishSlide moves the clock past the slide and delivers its frame.
func (r *drawerRig) finishSlide() {
	r.clk.Advance(slideFor)
	r.fireSlide()
}

func (h *heldTicks) count(d time.Duration) int {
	n := 0

	for _, got := range h.delays {
		if got == d {
			n++
		}
	}

	return n
}

// sentTo is what pane id's process has been sent.
func (r *drawerRig) sentTo(id core.PaneID) string {
	if p := r.panes[id]; p != nil {
		return p.proc.(*fakeProc).sent() //nolint:forcetypeassert // test panes run fake processes
	}

	return ""
}

// marker is the key sentSoFar types; the tests that type every printable ASCII key never type it.
const marker = "\u00e9"

// sentSoFar is what pane id, the focused pane, has been sent once the drawer lets go of the keyboard and
// a marker typed into the pane has arrived, without the markers. Input reaches a pane's process
// asynchronously, so a test that checks that nothing arrived must wait behind something that does.
func (r *drawerRig) sentSoFar(t *testing.T, id core.PaneID) string {
	t.Helper()

	want := strings.Count(r.sentTo(id), marker) + 1

	r.drawerRelease()
	r.keys(keyRune([]rune(marker)[0]))
	eventually(t, "the marker to reach "+string(id), func() bool { return strings.Count(r.sentTo(id), marker) >= want })

	return strings.ReplaceAll(r.sentTo(id), marker, "")
}

func (r *drawerRig) focus() core.PaneID { return r.store.State().Tab().Focus }

// openShell opens a shell in dir with split, in the state only.
func (r *drawerRig) openShell(t *testing.T, dir string, split core.Split) core.PaneID {
	t.Helper()

	id := r.store.State().NewPaneID()
	openIn(t, r.App, core.OpenPane{Kind: core.KindShell, Cwd: dir, Split: split, Area: r.bodyArea()})

	return id
}

// fileAtCursor is the path of the file the cursor is in.
func fileAtCursor(v *reviewView) string { return v.res.Files[v.rows[v.cursor].file].Path }

func TestReviewKeysTable(t *testing.T) {
	t.Parallel()

	seen := map[string]bool{}
	reg := newTestApp(t).reg

	for _, k := range reviewKeys {
		if seen[k.key] {
			t.Errorf("key %q is bound twice", k.key)
		}

		seen[k.key] = true

		if _, ok := reg.get(k.command); !ok {
			t.Errorf("key %q names the unknown command %q", k.key, k.command)
		}
	}

	for _, want := range []string{"j", "k", "down", "up", "]", "[", "}", "{", "ctrl+d", "ctrl+u", "g", "G", "tab", "f", "u", "enter", "b", "t", ",", ".", "/", "z", "e", "esc"} {
		if !seen[want] {
			t.Errorf("key %q is not bound", want)
		}
	}
}

func TestDrawerKeysNeverReachPanes(t *testing.T) {
	r := newKeysRig(t, keysRepo(t))
	r.opened(t)

	var ks []tea.KeyPressMsg

	for c := ' '; c <= '~'; c++ {
		if c != 'e' {
			ks = append(ks, keyRune(c))
		}
	}

	for c := 'a'; c <= 'z'; c++ {
		ks = append(ks, tea.KeyPressMsg{Code: c, Mod: tea.ModCtrl})
	}

	for _, code := range []rune{tea.KeyEnter, tea.KeyTab, tea.KeyBackspace, tea.KeyUp, tea.KeyDown, tea.KeyLeft, tea.KeyRight, tea.KeyPgUp, tea.KeyPgDown} {
		ks = append(ks, tea.KeyPressMsg{Code: code})
	}

	for _, k := range ks {
		r.keys(k)

		// Put the drawer back as it was for the next key.
		if treeIsOpen(r.App) {
			openIn(t, r.App, core.SetReviewTree{Open: false})
		}

		r.drawer.focused, r.drawer.tree = true, false
		r.view().search = nil
	}

	r.quiet(t, 100*time.Millisecond)
	r.step(tea.PasteMsg{Content: "pasted"})

	r.drawer.focused = true
	r.view().search = nil

	if got := r.sentSoFar(t, "p1"); got != "" {
		t.Errorf("the pane was sent %q while the drawer had the keyboard, want nothing", got)
	}

	// e opens an editor pane; the old pane still hears nothing.
	r.drawer.focused = true
	r.keys(keyRune('e'))

	if r.store.State().Tab().Review != nil {
		t.Fatal("e left the drawer open")
	}

	openIn(t, r.App, core.FocusPane{ID: "p1"})

	if got := r.sentSoFar(t, "p1"); got != "" {
		t.Errorf("after e the pane was sent %q, want nothing", got)
	}
}

func TestSearchFindsAndMisses(t *testing.T) {
	t.Parallel()
	r := newKeysRig(t, editedRepo(t))
	v := r.opened(t)

	rowHolds := func(want string) bool {
		return slices.ContainsFunc(rowTexts(v.res, v.rows[v.cursor]), func(s string) bool { return strings.Contains(s, want) })
	}

	r.keys(keyRune('/'))
	r.typed("fifty")

	if !strings.Contains(r.drawn(t), "╰─ /fifty▏") {
		t.Errorf("the search prompt is not in the bottom border:\n%s", r.drawn(t))
	}

	r.keys(keyEnter)

	if v.search != nil || !rowHolds("fifty") {
		t.Errorf("enter: prompt open = %v, cursor row holds fifty = %v, want false and true", v.search != nil, rowHolds("fifty"))
	}

	at := v.cursor

	r.keys(keyRune('/'))
	r.typed("zzz")
	r.keys(keyEnter)

	if r.notice != "not found: zzz" || v.cursor != at {
		t.Errorf("a miss: notice = %q, cursor %d -> %d, want the notice and no move", r.notice, at, v.cursor)
	}

	// An empty query repeats the last one; with two matches it alternates.
	r.keys(keyRune('/'))
	r.typed("edited")
	r.keys(keyEnter)

	first := v.cursor

	r.keys(keyRune('/'), keyEnter)

	second := v.cursor

	r.keys(keyRune('/'), keyEnter)

	if first == second || v.cursor != first {
		t.Errorf("repeating the search went %d, %d, %d, want it to alternate between two rows", first, second, v.cursor)
	}

	// A paste goes to the open prompt and is dropped otherwise.
	r.step(tea.PasteMsg{Content: "ignored"})

	if v.search != nil {
		t.Error("a paste opened the search")
	}

	r.keys(keyRune('/'))
	r.step(tea.PasteMsg{Content: "pasted"})

	if v.search == nil || *v.search != "pasted" {
		t.Errorf("search after a paste = %v, want pasted", v.search)
	}
}

// matchFiles writes text to each of three files in dir.
func matchFiles(t *testing.T, dir, text string) {
	t.Helper()

	for _, f := range []string{"a.txt", "b.txt", "c.txt"} {
		write(t, filepath.Join(dir, f), text)
	}
}

func TestSearchMatchKeys(t *testing.T) {
	t.Parallel()

	for _, tt := range []struct {
		mode string
		open func(t *testing.T) (*drawerRig, *reviewView)
	}{
		{"branch", func(t *testing.T) (*drawerRig, *reviewView) {
			r := newKeysRig(t, keysRepo(t))

			return r, r.opened(t)
		}},
		{"turn", func(t *testing.T) (*drawerRig, *reviewView) {
			dir := newRepoDir(t)
			matchFiles(t, dir, numberedLines("line", 40))
			commitEverything(t, dir, "init")
			snapshotTurn(t, dir, testSession)
			matchFiles(t, dir, editedLines(40, 3, 30))

			r := newKeysRig(t, dir, turnFirst)
			openIn(t, r.App, core.FocusPane{ID: r.agent(t, dir, testSession)})

			v := r.opened(t)
			if _, ok := v.src.(git.TurnSource); !ok {
				t.Fatalf("source = %+v, want a turn", v.src)
			}

			return r, v
		}},
		{"pr", func(t *testing.T) (*drawerRig, *reviewView) {
			dir := newRepoDir(t)
			matchFiles(t, dir, numberedLines("line", 40))
			commitEverything(t, dir, "init")
			matchFiles(t, dir, editedLines(40, 3, 30))
			commitEverything(t, dir, "feature work")

			p := newPRDrawerOn(t, dir)
			p.poll()
			p.opened(t)
			p.typed("p")
			p.until(t, "PR mode", p.inPRMode(7))

			return p.drawerRig, p.view()
		}},
	} {
		t.Run(tt.mode, func(t *testing.T) {
			t.Parallel()

			r, v := tt.open(t)

			var hits []int

			for i, row := range v.rows {
				if slices.ContainsFunc(rowTexts(v.res, row), func(s string) bool { return strings.Contains(s, "edited") }) {
					hits = append(hits, i)
				}
			}

			if len(hits) < 3 {
				t.Fatalf("%d rows hold edited, want at least 3", len(hits))
			}

			// Before any search n and N do nothing, as in the reading overlays.
			r.keys(keyRune('g'))
			r.keys(keyRune('n'), keyRune('N'))

			if v.cursor != 0 || r.notice != "" {
				t.Fatalf("n and N before a search: cursor %d, notice %q, want 0 and none", v.cursor, r.notice)
			}

			r.keys(keyRune('/'))
			r.typed("EDITED")
			r.keys(keyEnter)

			if v.cursor != hits[0] {
				t.Fatalf("/EDITED from the top: cursor %d, want the first match %d", v.cursor, hits[0])
			}

			// n walks the matches forward and wraps; N walks them back and wraps.
			hit := func(k int) int { return hits[(k%len(hits)+len(hits))%len(hits)] }

			for _, dir := range []struct {
				key  rune
				sign int
			}{{'n', 1}, {'N', -1}} {
				for k := 1; k <= len(hits); k++ {
					r.keys(keyRune(dir.key))

					if want := hit(dir.sign * k); v.cursor != want {
						t.Fatalf("%c number %d: cursor %d, want match %d of %v", dir.key, k, v.cursor, want, hits)
					}
				}
			}

			r.keys(keyRune('/'))
			r.typed("zzz")
			r.keys(keyEnter)

			for _, k := range []rune{'n', 'N'} {
				r.notice = ""
				r.keys(keyRune(k))

				if r.notice != "not found: zzz" || v.cursor != hits[0] {
					t.Errorf("%c after a miss: notice %q, cursor %d, want not found: zzz and %d", k, r.notice, v.cursor, hits[0])
				}
			}
		})
	}
}

func TestTreeKeys(t *testing.T) {
	t.Parallel()

	for _, win := range treeWindows {
		t.Run(win.name, func(t *testing.T) {
			r, v := treeRig(t, win.w, win.h, treeStartHidden)

			r.keys(keyTab)

			if !r.drawer.tree || !treeIsOpen(r.App) || v.treeSel != 0 {
				t.Fatalf("tab: tree has the keyboard = %v, tree open = %v, selected file = %d, want true, true and 0", r.drawer.tree, treeIsOpen(r.App), v.treeSel)
			}

			// Tree order puts folders first: dir/sub/c.txt, dir/b.txt, a.txt, z.txt.
			for _, step := range []struct {
				key  rune
				want string
			}{{'j', "z.txt"}, {'j', "z.txt"}, {'k', "a.txt"}, {'k', "dir/b.txt"}, {'k', "dir/sub/c.txt"}, {'k', "dir/sub/c.txt"}} {
				r.keys(keyRune(step.key))

				if got := v.res.Files[v.treeSel].Path; got != step.want {
					t.Fatalf("after %c the tree selects %s, want %s", step.key, got, step.want)
				}
			}

			r.keys(keyRune('f'))

			if r.notice != "" || len(v.fullOn) != 0 {
				t.Error("a drawer key other than the tree's did something in the tree")
			}

			r.keys(keyEnter)

			if r.drawer.tree || !r.drawer.focused || !treeIsOpen(r.App) || fileAtCursor(v) != "dir/sub/c.txt" || v.rows[v.cursor].kind != rowFile {
				t.Errorf("enter: tree has the keyboard = %v, drawer = %v, tree open = %v, cursor on %s (kind %d), want the diff on dir/sub/c.txt's header and the tree open",
					r.drawer.tree, r.drawer.focused, treeIsOpen(r.App), fileAtCursor(v), v.rows[v.cursor].kind)
			}

			cursor := v.cursor

			r.keys(keyTab, keyRune('j'), keyTab)

			if r.drawer.tree || treeIsOpen(r.App) || v.cursor != cursor {
				t.Errorf("tab, j, tab: tree has the keyboard = %v, tree open = %v, cursor %d -> %d, want the tree hidden and the cursor kept",
					r.drawer.tree, treeIsOpen(r.App), cursor, v.cursor)
			}
		})
	}
}

func TestMovementKeys(t *testing.T) {
	t.Parallel()
	r := newKeysRig(t, keysRepo(t))
	v := r.opened(t)
	bodyH := r.layout(t).bodyH

	kindAt := func() reviewRowKind { return v.rows[v.cursor].kind }
	at := func(file int, kind reviewRowKind) bool { return v.rows[v.cursor].file == file && kindAt() == kind }

	r.keys(keyRune('j'))

	if v.cursor != 1 {
		t.Fatalf("j: cursor = %d, want 1", v.cursor)
	}

	r.keys(tea.KeyPressMsg{Code: tea.KeyUp})

	if v.cursor != 0 {
		t.Fatalf("up: cursor = %d, want 0", v.cursor)
	}

	r.keys(keyRune('k'))

	if v.cursor != 0 {
		t.Fatalf("k at the top: cursor = %d, want 0", v.cursor)
	}

	// Each hunk's first changed line is its first removed line.
	hunks := []struct {
		file int
		text string
	}{{0, "line 3"}, {0, "line 30"}, {1, "line 3"}}

	for i, want := range hunks {
		r.keys(keyRune(']'))

		if l, ok := cursorLine(v); !ok || v.rows[v.cursor].file != want.file || l.Kind != git.Del || l.Text != want.text {
			t.Fatalf("] #%d: cursor on file %d row kind %d, want file %d's removed line %q", i+1, v.rows[v.cursor].file, kindAt(), want.file, want.text)
		}
	}

	r.keys(keyRune('['))

	if l, ok := cursorLine(v); !ok || v.rows[v.cursor].file != 0 || l.Text != "line 30" {
		t.Errorf("[: cursor on file %d row kind %d, want file 0's removed line %q", v.rows[v.cursor].file, kindAt(), "line 30")
	}

	r.keys(keyRune('}'))

	if !at(1, rowFile) {
		t.Errorf("}: cursor on file %d kind %d, want file 1's header", v.rows[v.cursor].file, kindAt())
	}

	r.keys(keyRune('{'))

	if !at(0, rowFile) {
		t.Errorf("{ from a header: cursor on file %d kind %d, want file 0's header", v.rows[v.cursor].file, kindAt())
	}

	r.keys(keyRune('G'))

	if v.cursor != len(v.rows)-1 {
		t.Errorf("G: cursor = %d, want %d", v.cursor, len(v.rows)-1)
	}

	r.keys(keyRune('g'))

	if v.cursor != 0 {
		t.Errorf("g: cursor = %d, want 0", v.cursor)
	}

	r.keys(tea.KeyPressMsg{Code: 'd', Mod: tea.ModCtrl})

	if v.cursor != bodyH/2 {
		t.Errorf("ctrl+d: cursor = %d, want half a page, %d", v.cursor, bodyH/2)
	}

	r.keys(tea.KeyPressMsg{Code: 'u', Mod: tea.ModCtrl})

	if v.cursor != 0 {
		t.Errorf("ctrl+u: cursor = %d, want 0", v.cursor)
	}

	// The view scrolls only as far as the cursor needs, and stays put when the cursor moves back up.
	for range bodyH + 3 {
		r.keys(keyRune('j'))
	}

	top := v.top
	if top != 4 {
		t.Fatalf("after %d lines down the view starts at row %d, want 4", bodyH+3, top)
	}

	r.keys(keyRune('k'))

	if v.top != top || v.cursor != bodyH+2 {
		t.Errorf("k: view starts at %d, cursor %d, want %d and %d", v.top, v.cursor, top, bodyH+2)
	}
}

// cursorLine is the diff line under the cursor on a hunk row: the row's line,
// or the old side of a side by side pair.
func cursorLine(v *reviewView) (git.Line, bool) {
	r := v.rows[v.cursor]
	if r.kind != rowLine && r.kind != rowPair {
		return git.Line{}, false
	}

	return v.res.Files[r.file].Hunks[r.hunk].Lines[r.line], true
}

// changeRow reports whether a full-file row is an added or removed line.
func changeRow(r reviewRow) bool {
	return (r.kind == rowFull && r.other < 0) || r.kind == rowGhost || r.kind == rowFold
}

func TestHunkKeysLandOnTheFirstChange(t *testing.T) {
	t.Parallel()
	// editedRepo's hunks open with three context lines; line 5 and line 50 are
	// each hunk's first removed line.
	for _, split := range []bool{false, true} {
		t.Run(map[bool]string{false: "unified", true: "split"}[split], func(t *testing.T) {
			r := newKeysRig(t, editedRepo(t))
			v := r.opened(t)

			if split {
				r.keys(keyRune('u'))
				r.settle()
			}

			at := func(step, text string) {
				t.Helper()

				if l, ok := cursorLine(v); !ok || l.Kind != git.Del || l.Text != text {
					t.Fatalf("%s: cursor row %d is %+v (hunk row %v), want the removed line %q", step, v.cursor, v.rows[v.cursor], ok, text)
				}
			}

			r.keys(keyRune(']'))
			at("]", "line 5")

			r.keys(keyRune(']'))
			at("] again", "line 50")

			r.keys(keyRune(']'))
			at("] at the last hunk", "line 50")

			// From inside the hunk, [ goes back to its first change; again, to the hunk before.
			r.keys(keyRune('j'), keyRune('j'), keyRune('j'), keyRune('['))
			at("[ from inside a hunk", "line 50")

			r.keys(keyRune('['))
			at("[", "line 5")

			r.keys(keyRune('['))
			at("[ at the first hunk", "line 5")
		})
	}

	t.Run("full file", func(t *testing.T) {
		r := newKeysRig(t, editedRepo(t))
		v := r.opened(t)

		r.keys(keyRune('f'))
		r.until(t, "the full file", func() bool { return len(v.full) == 1 })
		r.keys(keyRune('g'))

		first := func(step string) int {
			t.Helper()

			i := v.cursor
			if !changeRow(v.rows[i]) || changeRow(v.rows[i-1]) {
				t.Fatalf("%s: row %d is not the first row of a change block (row %+v after %+v)", step, i, v.rows[i], v.rows[i-1])
			}

			return i
		}

		r.keys(keyRune(']'))

		a := first("]")

		r.keys(keyRune(']'))

		b := first("] again")

		if b <= a {
			t.Fatalf("] again: cursor row %d, want a later block than row %d", b, a)
		}

		r.keys(keyRune('j'), keyRune('['))

		if v.cursor != b {
			t.Errorf("[ from inside a block: cursor row %d, want the block's first row %d", v.cursor, b)
		}

		r.keys(keyRune('['))

		if v.cursor != a {
			t.Errorf("[: cursor row %d, want the first block's first row %d", v.cursor, a)
		}
	})
}

func TestViewKeys(t *testing.T) {
	t.Parallel()
	t.Run("full file and ghost lines", func(t *testing.T) {
		r := newKeysRig(t, editedRepo(t))
		v := r.opened(t)

		r.keys(keyRune('f'))
		r.until(t, "the full file", func() bool { return len(v.full) == 1 })

		ghost := slices.IndexFunc(v.rows, func(row reviewRow) bool { return row.kind == rowGhost })
		if ghost < 0 {
			t.Fatal("the full view has no ghost line")
		}

		v.cursor = ghost

		r.keys(keyEnter)

		if v.rows[v.cursor].kind != rowFold {
			t.Fatalf("enter on a ghost line: row is kind %d, want a fold", v.rows[v.cursor].kind)
		}

		r.keys(keyEnter)

		if v.rows[v.cursor].kind != rowGhost {
			t.Errorf("enter on a fold: row is kind %d, want the ghost line back", v.rows[v.cursor].kind)
		}

		r.keys(keyRune('f'))

		if len(v.full) != 0 {
			t.Error("f again did not return to the diff")
		}
	})

	t.Run("split", func(t *testing.T) {
		r := newKeysRig(t, editedRepo(t))
		v := r.opened(t)

		r.keys(keyRune('u'))
		r.settle()

		if !v.split || !v.rowsSplit || r.notice != "" {
			t.Errorf("u: split = %v, rows split = %v, notice = %q, want side by side", v.split, v.rowsSplit, r.notice)
		}

		r.keys(keyRune('u'))
		r.settle()

		if v.split || v.rowsSplit {
			t.Error("u again did not return to unified")
		}

		sized(t, r.App, 100, 30)
		r.keys(keyRune('u'))
		r.settle()

		if r.notice != "side by side needs a wider drawer" || v.rowsSplit {
			t.Errorf("u in a narrow drawer: notice = %q, rows split = %v, want the notice and unified rows", r.notice, v.rowsSplit)
		}
	})

	t.Run("modes and turns", func(t *testing.T) {
		dir := threeTurnRepo(t)
		r := newKeysRig(t, dir, turnFirst)
		id := r.agent(t, dir, testSession)
		openIn(t, r.App, core.FocusPane{ID: id})

		v := r.opened(t)
		turn := func() int {
			if ts, ok := v.src.(git.TurnSource); ok {
				return ts.Turn
			}

			return -1
		}

		if turn() != 0 {
			t.Fatalf("a new drawer shows turn %d, want the latest turn", turn())
		}

		for _, step := range []struct {
			key  rune
			want int
		}{{'b', -1}, {'t', 0}, {',', 2}, {',', 1}, {'.', 2}, {'t', 0}} {
			r.keys(keyRune(step.key))
			r.until(t, fmt.Sprintf("%c to load", step.key), func() bool { return v.loaded && !v.loading && turn() == step.want })
		}
	})
}

func TestDrawerCommandsWithoutADrawer(t *testing.T) {
	t.Parallel()
	r := newKeysRig(t, keysRepo(t))
	n := 0

	for _, c := range r.reg.all() {
		if !strings.HasPrefix(c.id, "review.") || c.id == "review.toggle" {
			continue
		}

		n++
		r.notice = ""
		c.run(r.App)

		if r.notice != "no review drawer is open" {
			t.Errorf("%s: notice = %q, want no review drawer is open", c.id, r.notice)
		}
	}

	if n < 20 {
		t.Errorf("only %d drawer commands are registered, want every key's", n)
	}

	if r.store.State().Tab().Review != nil || r.drawer.focused {
		t.Error("a drawer command opened a drawer or took the keyboard")
	}
}

func TestSlideOutSurvivesBackgroundActions(t *testing.T) {
	t.Parallel()
	r := newKeysRig(t, keysRepo(t))
	r.opened(t)
	r.leader('d')

	if !r.drawer.closing {
		t.Fatal("leader d did not start the slide out")
	}

	// Actions that come from outside the operator's hands must not cut the slide short.
	openIn(t, r.App, core.SetPaneCwd{ID: "p1", Cwd: r.store.State().Panes["p1"].Cwd}, core.SetTheme{Name: "catppuccin-latte"})

	if !r.drawer.closing || r.store.State().Tab().Review == nil {
		t.Fatalf("a background action ended the slide out: closing = %v, drawer open = %v", r.drawer.closing, r.store.State().Tab().Review != nil)
	}

	// An action that can change the active tab ends it at once, and the drawer of the tab it was in is gone.
	openIn(t, r.App, core.NewTab{Kind: core.KindShell, Cwd: t.TempDir()})

	if r.drawer.closing || r.store.State().Tabs[0].Review != nil {
		t.Errorf("after a new tab: closing = %v, first tab's drawer = %+v, want it closed", r.drawer.closing, r.store.State().Tabs[0].Review)
	}
}
