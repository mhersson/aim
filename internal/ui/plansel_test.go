package ui

import (
	"fmt"
	"slices"
	"strings"
	"testing"
	"unicode/utf8"

	tea "charm.land/bubbletea/v2"

	"github.com/mhersson/aim/internal/core"
)

const selPlan = `# Plan

Alpha beta gamma delta epsilon zeta eta theta iota kappa lambda mu nu xi omicron pi rho sigma tau.

- first item
- second item
`

const selPara = "Alpha beta gamma delta epsilon zeta eta theta iota kappa lambda mu nu xi omicron pi rho sigma tau."

// selRig is a plan rig with the plan open, a fake clipboard and every pane's
// guest asking for mouse reports.
type selRig struct {
	*planRig
	clip *fakeRun
	path string
	pv   *planView
}

func newSelRig(t *testing.T) *selRig {
	t.Helper()

	r := newPlanRig(t, 100, 40)
	f := fakeClip(r.a)
	path := r.filePlan(t, "plan.md", selPlan)
	r.open(t)

	for i, id := range []core.PaneID{"p1", "p2"} {
		feedAndWait(t, r.a.panes[id], r.st.procs[i], "\x1b[?1000h\x1b[?1006h")
	}

	return &selRig{planRig: r, clip: f, path: path, pv: r.a.plan}
}

// word is the cells of the first row holding word: its first and last cell.
func (r *selRig) word(t *testing.T, row, word string) (x0, x1, y int) {
	t.Helper()

	for y, line := range strings.Split(r.shown(t), "\n") {
		if !strings.Contains(line, row) {
			continue
		}

		i := strings.Index(line, word)
		x0 = utf8.RuneCountInString(line[:i])

		return x0, x0 + utf8.RuneCountInString(word) - 1, y
	}

	t.Fatalf("no screen row holds %q", row)

	return 0, 0, 0
}

func (r *selRig) selected() bool { return r.a.text != nil }

func (r *selRig) dragBeta(t *testing.T) {
	t.Helper()

	b0, _, y := r.word(t, "Alpha", "beta")
	_, d1, _ := r.word(t, "Alpha", "delta")

	dragRun(r.a, b0, y, d1, y, 0)
}

func (r *selRig) key(k tea.KeyPressMsg) { run(r.a, press(r.a, k)) }

func TestPlanDocRect(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name          string
		width, height int
		want          [4]int
		ok            bool
	}{
		{"with blank rows", 100, 40, [4]int{4, 4, 92, 31}, true},
		{"no blank rows", 100, 10, [4]int{4, 3, 92, 3}, true},
		{"too narrow", 20, 40, [4]int{}, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			r, ok := planDocRect(tt.width, tt.height)
			if ok != tt.ok {
				t.Fatalf("ok = %v, want %v", ok, tt.ok)
			}

			if got := [4]int{r.Min.X, r.Min.Y, r.Dx(), r.Dy()}; ok && got != tt.want {
				t.Errorf("rect = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestPlanDragCopiesToPrimary(t *testing.T) {
	t.Parallel()

	r := newSelRig(t)
	r.dragBeta(t)

	if got := primaryWrites(r.clip); !slices.Equal(got, []string{"beta gamma delta"}) {
		t.Errorf("primary writes %q", got)
	}

	if got := clipboardWrites(r.clip); len(got) != 0 {
		t.Errorf("clipboard writes %q, want none", got)
	}

	if !r.selected() || r.a.textSelected() != "beta gamma delta" {
		t.Errorf("selection %q, want it kept", r.a.textSelected())
	}
}

func TestPlanCopyOnSelect(t *testing.T) {
	t.Parallel()

	r := newSelRig(t)
	r.a.copyOnSelect = true
	r.dragBeta(t)

	if got := clipboardWrites(r.clip); !slices.Equal(got, []string{"beta gamma delta"}) {
		t.Errorf("clipboard writes %q", got)
	}

	if r.selected() || r.pv.view.sel != nil {
		t.Error("selection kept, want it cleared")
	}
}

func TestPlanMultiClick(t *testing.T) {
	t.Parallel()

	r := newSelRig(t)

	_, _, y0 := r.word(t, "Alpha", "Alpha")
	_, _, y1 := r.word(t, "tau.", "tau.")

	if y0 == y1 {
		t.Fatalf("the paragraph is one row (%d), want it wrapped", y0)
	}

	g0, _, gy := r.word(t, "Alpha", "gamma")
	clickRun(r.a, g0, gy)
	clickRun(r.a, g0, gy)

	if got := primaryWrites(r.clip); !slices.Equal(got, []string{"gamma"}) {
		t.Errorf("double click wrote %q, want [gamma]", got)
	}

	tx, _, ty := r.word(t, "tau.", "tau.")
	clickRun(r.a, tx, ty)
	clickRun(r.a, tx, ty)
	clickRun(r.a, tx, ty)

	got := primaryWrites(r.clip)
	if n := len(got); n != 3 || got[2] != selPara {
		t.Errorf("triple click wrote %q, want the paragraph as one line", got)
	}
}

func TestPlanClickClears(t *testing.T) {
	t.Parallel()

	r := newSelRig(t)
	r.dragBeta(t)

	cursor := r.pv.view.cursor
	x, _, y := r.word(t, "first item", "item")

	clickRun(r.a, x, y)

	if r.selected() || r.pv.view.sel != nil {
		t.Error("selection kept after a click")
	}

	if r.pv.view.cursor != cursor {
		t.Errorf("cursor moved to %d, want %d", r.pv.view.cursor, cursor)
	}

	if got := primaryWrites(r.clip); len(got) != 1 {
		t.Errorf("primary writes %q, want only the drag's", got)
	}
}

func TestPlanYKey(t *testing.T) {
	t.Parallel()

	t.Run("selection", func(t *testing.T) {
		t.Parallel()

		r := newSelRig(t)
		r.dragBeta(t)
		r.key(keyRune('y'))

		if got := clipboardWrites(r.clip); !slices.Equal(got, []string{"beta gamma delta"}) {
			t.Errorf("clipboard writes %q", got)
		}

		if !r.selected() {
			t.Error("selection cleared by y")
		}
	})

	t.Run("cursor item", func(t *testing.T) {
		t.Parallel()

		r := newSelRig(t)
		v := &r.pv.view

		for !strings.Contains(layoutLine(v.layout(), v.cursor), "second item") {
			r.key(keyRune('j'))
		}

		r.key(keyRune('y'))

		if got := clipboardWrites(r.clip); !slices.Equal(got, []string{"• second item"}) {
			t.Errorf("clipboard writes %q", got)
		}
	})
}

func TestPlanYCopiesCursorUnitWhenAnotherViewHoldsTheSelection(t *testing.T) {
	t.Parallel()

	r := newSelRig(t)
	r.a.text = &textSelection{view: &mdView{}}

	v := &r.pv.view
	for !strings.Contains(layoutLine(v.layout(), v.cursor), "second item") {
		r.key(keyRune('j'))
	}

	r.key(keyRune('y'))

	if got := clipboardWrites(r.clip); !slices.Equal(got, []string{"• second item"}) {
		t.Errorf("clipboard writes %q", got)
	}
}

func TestPlanOtherButtonsKeepTheDrag(t *testing.T) {
	t.Parallel()

	for _, b := range []tea.MouseButton{tea.MouseMiddle, tea.MouseRight} {
		r := newSelRig(t)
		b0, _, y := r.word(t, "Alpha", "beta")
		_, d1, _ := r.word(t, "Alpha", "delta")

		mdown(r.a, b0, y, tea.MouseLeft, 0)
		mmove(r.a, d1, y, tea.MouseLeft)
		mdown(r.a, b0+2, y, b, 0)
		mup(r.a, b0+2, y, b)
		release(r.a, d1, y)

		if got := primaryWrites(r.clip); !slices.Equal(got, []string{"beta gamma delta"}) {
			t.Errorf("button %v: primary writes %q", b, got)
		}
	}
}

func TestPlanKeysClear(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		keys []tea.KeyPressMsg
		want bool // the selection is kept
	}{
		{"move", []tea.KeyPressMsg{keyRune('j')}, false},
		{"heading", []tea.KeyPressMsg{keyRune(']')}, false},
		{"search then esc", []tea.KeyPressMsg{keyRune('/'), keyEsc}, false},
		{"enter without a target", []tea.KeyPressMsg{{Code: tea.KeyEnter}}, false},
		{"dropped key", []tea.KeyPressMsg{keyRune('x')}, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			r := newSelRig(t)
			r.dragBeta(t)

			for _, k := range tt.keys {
				r.key(k)
			}

			if got := r.selected(); got != tt.want {
				t.Errorf("selected = %v, want %v", got, tt.want)
			}
		})
	}

	t.Run("wheel", func(t *testing.T) {
		t.Parallel()

		r := newSelRig(t)
		r.dragBeta(t)
		r.a.Update(tea.MouseWheelMsg{X: 20, Y: 10, Button: tea.MouseWheelDown})

		if !r.selected() {
			t.Error("the wheel cleared the selection")
		}
	})
}

func TestPlanDragEndsAtEdge(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		x, y int
	}{
		{"box border", 2, 12},
		{"outside the box", 0, 20},
		{"last screen row", 50, 39},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			r := newSelRig(t)
			b0, _, y := r.word(t, "Alpha", "beta")

			dragRun(r.a, b0, y, tt.x, tt.y, 0)

			rect, _ := planDocRect(r.a.width, r.a.height)
			sel := r.pv.view.sel

			if sel == nil {
				t.Fatal("no selection")
			}

			lines := len(r.pv.view.layout().Lines)
			for _, p := range []point{sel.anchor, sel.head} {
				if p.row < 0 || p.row >= lines || p.col < 0 || p.col >= rect.Dx() {
					t.Errorf("selection end %v lies outside the doc", p)
				}
			}

			if got := primaryWrites(r.clip); len(got) != 1 || got[0] == "" {
				t.Errorf("primary writes %q, want one", got)
			}

			for i, p := range r.st.procs {
				if got := p.sent(); got != "" {
					t.Errorf("pane %d received %q", i, got)
				}
			}
		})
	}
}

func TestPlanRewriteSelection(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		text      string
		held      bool
		wantSel   bool
		wantWrite int
	}{
		{"other line changes", strings.Replace(selPlan, "second item", "second thing", 1), false, true, 1},
		{"selected line changes", strings.Replace(selPlan, "gamma", "gamma2", 1), false, false, 1},
		{"changes while held", strings.Replace(selPlan, "gamma", "gamma2", 1), true, false, 0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			r := newSelRig(t)
			b0, _, y := r.word(t, "Alpha", "beta")
			_, d1, _ := r.word(t, "Alpha", "delta")

			rewrite := func() {
				write(t, r.path, tt.text)

				for _, m := range planMsgs(r.a.reloadPlan(r.pv.plan, false)) {
					r.a.Update(m)
				}
			}

			if tt.held {
				mdown(r.a, b0, y, tea.MouseLeft, 0)
				mmove(r.a, d1, y, tea.MouseLeft)
				rewrite()
				release(r.a, d1, y)
			} else {
				dragRun(r.a, b0, y, d1, y, 0)
				rewrite()
			}

			if got := r.selected(); got != tt.wantSel {
				t.Errorf("selected = %v, want %v", got, tt.wantSel)
			}

			if got := len(primaryWrites(r.clip)); got != tt.wantWrite {
				t.Errorf("%d primary writes, want %d", got, tt.wantWrite)
			}
		})
	}
}

func TestPlanDragThroughRewrite(t *testing.T) {
	t.Parallel()

	r := newSelRig(t)
	b0, _, y := r.word(t, "Alpha", "beta")
	_, g1, _ := r.word(t, "Alpha", "gamma")
	_, d1, _ := r.word(t, "Alpha", "delta")

	mdown(r.a, b0, y, tea.MouseLeft, 0)
	mmove(r.a, g1, y, tea.MouseLeft)
	write(t, r.path, strings.Replace(selPlan, "second item", "second thing", 1))

	for _, m := range planMsgs(r.a.reloadPlan(r.pv.plan, false)) {
		r.a.Update(m)
	}

	mmove(r.a, d1, y, tea.MouseLeft)
	release(r.a, d1, y)

	if !r.selected() {
		t.Error("a rewrite of another line during the drag dropped the selection")
	}

	if got := len(primaryWrites(r.clip)); got != 1 {
		t.Errorf("%d primary writes, want 1", got)
	}
}

func TestPlanClearedMidDrag(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		act  func(*selRig)
	}{
		{"resize", func(r *selRig) { r.a.Update(tea.WindowSizeMsg{Width: 70, Height: 40}) }},
		{"acting key", func(r *selRig) { r.key(keyRune('j')) }},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			r := newSelRig(t)
			b0, _, y := r.word(t, "Alpha", "beta")
			_, d1, _ := r.word(t, "Alpha", "delta")

			mdown(r.a, b0, y, tea.MouseLeft, 0)
			mmove(r.a, d1, y, tea.MouseLeft)

			if !r.selected() {
				t.Fatal("setup: no selection")
			}

			tt.act(r)
			mmove(r.a, d1, y, tea.MouseLeft)
			release(r.a, d1, y)

			if r.selected() {
				t.Error("the held drag brought the selection back")
			}

			if got := primaryWrites(r.clip); len(got) != 0 {
				t.Errorf("the release wrote %q, want nothing", got)
			}
		})
	}
}

func TestPlanCloseClears(t *testing.T) {
	t.Parallel()

	r := newSelRig(t)
	r.dragBeta(t)
	r.key(keyEsc)

	if r.a.plan != nil {
		t.Fatal("the overlay is still open")
	}

	if r.selected() {
		t.Error("selection kept after the overlay closed")
	}

	run(r.a, press(r.a, keyLeader, keyRune('y')))

	if got := clipboardWrites(r.clip); len(got) != 0 {
		t.Errorf("leader y wrote %q after close", got)
	}
}

func TestEdgeScrollToEnd(t *testing.T) {
	t.Parallel()

	// A few rows past the 31-row view, so the drag has to scroll.
	const n = 40

	r := newPlanRig(t, 100, 40)
	fakeClip(r.a)
	r.filePlan(t, "plan.md", numberedMD(n))
	r.open(t)

	h, _ := r.a.planHost()
	mdown(r.a, h.rect.Min.X+2, h.rect.Min.Y, tea.MouseLeft, 0)

	cmd := dragTick(r.a, tea.MouseMotionMsg{X: h.rect.Min.X + 20, Y: h.rect.Max.Y, Button: tea.MouseLeft})
	seq := r.a.mouse.text.seq

	for ticks := 0; cmd != nil; ticks++ {
		if ticks > n {
			t.Fatal("the ticks never stopped")
		}

		cmd = dragTick(r.a, dragScrollMsg{seq: seq})
	}

	if head := textSelHead(t, r.a); head.row != n-1 {
		t.Errorf("head row = %d, want %d", head.row, n-1)
	}

	lines := strings.Split(r.a.textSelected(), "\n")
	if len(lines) != n {
		t.Fatalf("copy has %d lines, want %d", len(lines), n)
	}

	// The press is on the list indent (the bullet is at cell 4), so row 0 copies
	// whole; a head on the indent would copy nothing of the last row, hence the
	// pointer further in.
	if lines[0] != "• line 0" {
		t.Errorf("first copied line = %q", lines[0])
	}

	if lines[n-1] != fmt.Sprintf("• line %d", n-1) {
		t.Errorf("last copied line = %q", lines[n-1])
	}
}
