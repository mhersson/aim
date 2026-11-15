package ui

import (
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/mhersson/aim/internal/core"
	"github.com/mhersson/aim/internal/harness"
)

// claudeScrolledApp is claudeApp for claudeDefault80, scrolled up two wheel
// notches: the view top is absolute row 11, so the reply's first row (16) is
// content row 5 and the last (48) is content row 37.
func claudeScrolledApp(t *testing.T, kind core.PaneKind, harnessID string) (*App, *fakeRun, core.Rect) {
	t.Helper()

	a, _, f := claudeApp(t, claudeDefault80, kind, harnessID)
	c := contentRectOf(a, "p1")

	for range 2 {
		wheelAt(a, c.X, c.Y, tea.MouseWheelUp)
	}

	if top := a.panes["p1"].viewTop(); top != 11 {
		t.Fatalf("view top = %d, want 11", top)
	}

	return a, f, c
}

// dragReply drags over the whole reply, h0016 col 0 to the last cell of s0031,
// with gesture (drag, or dragRun to run the release's command) and mod.
func dragReply(a *App, c core.Rect, gesture func(a *App, x0, y0, x1, y1 int, mod tea.KeyMod), mod tea.KeyMod) {
	gesture(a, c.X, c.Y+5, c.X+79, c.Y+37, mod)
}

// pressN presses the copy-mode key n times.
func pressN(a *App, key string, n int) {
	for range n {
		keys(a, key)
	}
}

// copyModeAtReply enters copy mode on the scrolled-up pane with the cursor on
// the reply's first cell, absolute (16, 0).
func copyModeAtReply(a *App) {
	enterCopy(a)
	keys(a, "H")
	pressN(a, "j", 5)
	keys(a, "0")
}

// copyModeStream selects with v from the reply's first cell to the end of
// "Done.", absolute (48, 6).
func copyModeStream(a *App) {
	copyModeAtReply(a)
	keys(a, "v")
	pressN(a, "j", 32)
	keys(a, "$")
}

// dragThenCopy drags over the reply, runs change and copies with ctrl+space y.
func dragThenCopy(a *App, f *fakeRun, c core.Rect, change func()) string {
	dragReply(a, c, drag, 0)
	change()

	return copyNow(a, f)
}

// copyModeThenYank selects with v to the end of "Done.", runs change and yanks.
func copyModeThenYank(a *App, f *fakeRun, _ core.Rect, change func()) string {
	copyModeStream(a)
	change()
	keys(a, "y")

	return copiedText(f)
}

// copyModeLineThenYank selects the reply's rows with V, runs change and yanks.
func copyModeLineThenYank(a *App, f *fakeRun, _ core.Rect, change func()) string {
	copyModeAtReply(a)
	keys(a, "V")
	pressN(a, "j", 32)
	change()
	keys(a, "y")

	return copiedText(f)
}

// extractOf is extract over the selection, read from p1's grid.
func extractOf(t *testing.T, a *App, sel selection) string {
	t.Helper()

	var got string

	read := false

	a.panes["p1"].withGrid(func(g grid) {
		read = true
		got = extract(g, sel)
	})

	if !read {
		t.Fatal("the grid was not read")
	}

	if got == "" {
		t.Fatal("extract returned an empty copy")
	}

	return got
}

// lastPrimaryWrite is the latest write to the primary selection alone.
func lastPrimaryWrite(f *fakeRun) string {
	if w := primaryWrites(f); len(w) > 0 {
		return w[len(w)-1]
	}

	return ""
}

func TestClaudeCopyPaths(t *testing.T) {
	t.Parallel()

	claudeID := (harness.Claude{}).ID()

	block := claudeSel(selBlock, 16, 48, 80)

	tests := []struct {
		name string
		run  func(a *App, f *fakeRun, c core.Rect, change func()) string
		// want is the copy, unless sel is set: then it is extract of sel.
		want string
		sel  *selection
	}{
		{
			name: "drag writes the primary selection joined", want: claudeWantReply,
			run: func(a *App, f *fakeRun, c core.Rect, _ func()) string {
				dragReply(a, c, dragRun, 0)

				return lastPrimaryWrite(f)
			},
		},
		{name: "drag then ctrl+space y", want: claudeWantReply, run: dragThenCopy},
		{
			name: "copy on select", want: claudeWantReply,
			run: func(a *App, f *fakeRun, c core.Rect, _ func()) string {
				a.copyOnSelect = true
				dragReply(a, c, dragRun, 0)

				return lastCopied(f)
			},
		},
		{name: "copy mode v then y", want: claudeWantReply, run: copyModeThenYank},
		{name: "copy mode V then y", want: claudeWantReply, run: copyModeLineThenYank},

		{
			name: "alt+drag rectangle copies each row as drawn", sel: &block,
			run: func(a *App, f *fakeRun, c core.Rect, _ func()) string {
				dragReply(a, c, drag, tea.ModAlt)

				return copyNow(a, f)
			},
		},
		{
			name: "copy mode ctrl+v rectangle then y", sel: &block,
			run: func(a *App, f *fakeRun, _ core.Rect, _ func()) string {
				copyModeAtReply(a)
				pressRun(a, tea.KeyPressMsg{Code: 'v', Mod: tea.ModCtrl})
				pressN(a, "j", 32)
				pressN(a, "l", 79)
				keys(a, "y")

				return copiedText(f)
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			a, f, c := claudeScrolledApp(t, core.KindAgent, claudeID)

			want := tt.want
			if tt.sel != nil {
				want = extractOf(t, a, *tt.sel)
			}

			if got := tt.run(a, f, c, func() {}); got != want {
				t.Errorf("copied %q, want %q", got, want)
			}
		})
	}
}

func TestClaudePaneKindAtCopy(t *testing.T) {
	t.Parallel()

	claudeID := (harness.Claude{}).ID()
	toShell := core.SetPaneKind{ID: "p1", Kind: core.KindShell}
	toClaude := core.SetPaneKind{ID: "p1", Kind: core.KindAgent, Harness: claudeID}

	tests := []struct {
		name    string
		kind    core.PaneKind
		harness string
		become  core.SetPaneKind
		// run makes the selection, then calls change, then copies.
		run func(a *App, f *fakeRun, c core.Rect, change func()) string
		// want is the copy, unless sel is set: then it is extract of sel.
		want string
		sel  *selection
	}{
		{
			name: "agent turned shell copies raw rows after a drag",
			kind: core.KindAgent, harness: claudeID, become: toShell,
			sel: &selection{selStream, point{16, 0}, point{48, 79}},
			run: dragThenCopy,
		},
		{
			name: "agent turned shell copies raw rows after copy mode v",
			kind: core.KindAgent, harness: claudeID, become: toShell,
			sel: &selection{selStream, point{16, 0}, point{48, 6}},
			run: copyModeThenYank,
		},
		{
			name: "shell turned agent joins rows after a drag",
			kind: core.KindShell, become: toClaude, want: claudeWantReply,
			run: dragThenCopy,
		},
		{
			name: "shell turned agent joins rows after copy mode v",
			kind: core.KindShell, become: toClaude, want: claudeWantReply,
			run: copyModeThenYank,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			a, f, c := claudeScrolledApp(t, tt.kind, tt.harness)

			want := tt.want
			if tt.sel != nil {
				want = extractOf(t, a, *tt.sel)
			}

			change := func() {
				t.Helper()

				if err := a.dispatch(tt.become); err != nil {
					t.Fatal(err)
				}
			}

			if got := tt.run(a, f, c, change); got != want {
				t.Errorf("copied %q, want %q", got, want)
			}
		})
	}
}

func TestClaudeRedrawRawRows(t *testing.T) {
	t.Parallel()

	claudeID := (harness.Claude{}).ID()
	d := claudeDefault80

	tests := []struct {
		name string
		out  string
		kept bool
		// want is the copy of a kept selection.
		want string
	}{
		{
			name: "indent of the second row changes",
			out:  "\x1b[29;1H\x1b[2K" + "    " + `someVeryLongTrailingExpression(g, s, "that runs past the edge")`,
		},
		{
			name: "second row drawn again as it was",
			out:  "\x1b[29;1H" + d.lines[d.hist()+28],
			kept: true, want: replyLine(t, "          return"),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			a, fp, f := claudeApp(t, d, core.KindAgent, claudeID)
			c := contentRectOf(a, "p1")

			drag(a, c.X, c.Y+27, c.X+79, c.Y+28, 0)
			feedAndWait(t, a.panes["p1"], fp, tt.out)
			a.Update(paneDirtyMsg{id: "p1", p: a.panes["p1"]})

			if !tt.kept {
				if a.sel != nil {
					t.Error("selection kept, want cleared")
				}

				return
			}

			if a.sel == nil {
				t.Fatal("selection cleared, want kept")
			}

			if got := copyNow(a, f); got != tt.want {
				t.Errorf("copied %q, want %q", got, tt.want)
			}
		})
	}
}

func TestClaudeMultiClick(t *testing.T) {
	t.Parallel()

	claudeID := (harness.Claude{}).ID()
	toShell := core.SetPaneKind{ID: "p1", Kind: core.KindShell}
	toClaude := core.SetPaneKind{ID: "p1", Kind: core.KindAgent, Harness: claudeID}
	bullet := replyLine(t, "  - The first bullet")

	// lit is a highlight check: whether the cell at content (row, col) has the
	// selection background.
	type lit struct {
		row, col int
		on       bool
	}

	tests := []struct {
		name    string
		kind    core.PaneKind
		harness string
		// before changes the pane's kind before the click; after, between the click and the copy.
		before, after *core.SetPaneKind
		// at is the content cell pressed: twice for a double-click, three times for a triple.
		at     point
		clicks int
		lit    []lit
		// want is the copy, unless sel is set: then it is extract of sel.
		want string
		sel  *selection
	}{
		{
			name: "agent double-click on the broken URL", kind: core.KindAgent, harness: claudeID,
			at: point{24, 4}, clicks: 2, want: claudeURL,
			lit: []lit{{23, 6, true}, {24, 0, true}, {24, 37, true}, {23, 5, false}, {24, 38, false}},
		},
		{
			name: "agent triple-click on the wrapped bullet", kind: core.KindAgent, harness: claudeID,
			at: point{9, 10}, clicks: 3, want: bullet,
			lit: []lit{{8, 0, true}, {9, 79, true}, {10, 0, false}},
		},
		{
			name: "shell turned agent before the click", kind: core.KindShell, before: &toClaude,
			at: point{24, 4}, clicks: 2, want: claudeURL,
		},
		{
			name: "agent turned shell before the click", kind: core.KindAgent, harness: claudeID, before: &toShell,
			at: point{24, 4}, clicks: 2, want: "ighlight",
		},
		{
			name: "agent turned shell between the click and the copy", kind: core.KindAgent, harness: claudeID, after: &toShell,
			at: point{24, 4}, clicks: 2, sel: &selection{selStream, point{40, 6}, point{41, 37}},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			a, _, f := claudeApp(t, claudeDefault80, tt.kind, tt.harness)
			clk := &fakeClock{t: time.Unix(1000, 0)}
			a.clock = clk
			r := contentRectOf(a, "p1")

			dispatch := func(act *core.SetPaneKind) {
				t.Helper()

				if act == nil {
					return
				}

				if err := a.dispatch(*act); err != nil {
					t.Fatal(err)
				}
			}

			dispatch(tt.before)

			for i := range tt.clicks {
				if i > 0 {
					clk.Advance(100 * time.Millisecond)
				}

				click(a, r.X+tt.at.col, r.Y+tt.at.row)
			}

			buf := drawnBuffer(a)
			for _, l := range tt.lit {
				if got := selBgIn(a, buf, r.X+l.col, r.Y+l.row); got != l.on {
					t.Errorf("selection background at content (%d,%d) = %v, want %v", l.row, l.col, got, l.on)
				}
			}

			dispatch(tt.after)

			want := tt.want
			if tt.sel != nil {
				want = extractOf(t, a, *tt.sel)
			}

			if got := copyNow(a, f); got != want {
				t.Errorf("copied %q, want %q", got, want)
			}
		})
	}
}
