package ui

import (
	"slices"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/mhersson/aim/internal/github"
)

// convSelRig is the conversation overlay at 120x40 on the fixture, with a fake
// clipboard.
type convSelRig struct {
	a    *App
	clip *fakeRun
}

func newConvSelRig(t *testing.T) *convSelRig {
	t.Helper()

	a, _ := convApp(t, true, 120, 40)
	f := fakeClip(a)
	convOpen(t, a)

	return &convSelRig{a: a, clip: f}
}

// word is the first and last cell of the first view row holding row, where word
// starts; the search stays inside the view's cells.
func (r *convSelRig) word(t *testing.T, row, word string) (x0, x1, y int) {
	t.Helper()

	rect, ok := convDocRect(r.a.width, r.a.height)
	if !ok {
		t.Fatal("no doc rect")
	}

	lines := strings.Split(ansi.Strip(r.a.View().Content), "\n")

	for y := rect.Min.Y; y < rect.Max.Y; y++ {
		line := lines[y]
		if !strings.Contains(line, row) {
			continue
		}

		i := strings.Index(line, row) + strings.Index(row, word)
		x0 = ansi.StringWidth(line[:i])

		return x0, x0 + ansi.StringWidth(word) - 1, y
	}

	t.Fatalf("no view row holds %q", row)

	return 0, 0, 0
}

func (r *convSelRig) key(k tea.KeyPressMsg) { run(r.a, press(r.a, k)) }

func (r *convSelRig) selected() bool { return r.a.text != nil }

const noFrames = "│╭╰─"

func TestConversationDocRect(t *testing.T) {
	t.Parallel()

	r := newConvSelRig(t)

	rect, ok := convDocRect(r.a.width, r.a.height)
	if !ok {
		t.Fatal("no rect")
	}

	// The first view row is the first item's top border, in the cells the rect
	// names.
	lines := strings.Split(ansi.Strip(r.a.View().Content), "\n")
	row := lines[rect.Min.Y]

	if got := ansi.Cut(row, rect.Min.X, rect.Max.X); !strings.Contains(got, "╭─ @mhersson opened #7") {
		t.Errorf("first rect row = %q", got)
	}

	if _, ok := convDocRect(20, 40); ok {
		t.Error("a box too narrow to draw has a rect")
	}
}

func TestConversationDragCopies(t *testing.T) {
	t.Parallel()

	t.Run("hunk", func(t *testing.T) {
		t.Parallel()

		r := newConvSelRig(t)
		x0, _, y0 := r.word(t, "if k == keyTab", "if")
		_, _, y1 := r.word(t, "212 +", "212")

		dragRun(r.a, x0, y0, 1000, y1, 0)

		got := primaryWrites(r.clip)
		if len(got) != 1 {
			t.Fatalf("primary writes %q", got)
		}

		if strings.ContainsAny(got[0], noFrames+"+") || strings.Contains(got[0], "210") || strings.Contains(got[0], "212") {
			t.Errorf("copied %q, want no frames, numbers or signs", got[0])
		}

		if lines := strings.Split(got[0], "\n"); len(lines) != 3 {
			t.Errorf("copied %d lines, want 3: %q", len(lines), got[0])
		}
	})
}

func TestConversationYKey(t *testing.T) {
	t.Parallel()

	t.Run("selection", func(t *testing.T) {
		t.Parallel()

		r := newConvSelRig(t)
		x0, _, y0 := r.word(t, "Looks fine.", "Looks")
		_, x1, _ := r.word(t, "Looks fine.", "fine.")
		dragRun(r.a, x0, y0, x1, y0, 0)
		r.key(keyRune('y'))

		if got := clipboardWrites(r.clip); !slices.Equal(got, []string{"Looks fine."}) {
			t.Errorf("clipboard writes %q", got)
		}

		if !r.selected() {
			t.Error("selection cleared by y")
		}
	})

	t.Run("cursor unit", func(t *testing.T) {
		t.Parallel()

		r := newConvSelRig(t)
		convOnLine(t, r.a, "Looks fine.")

		r.key(keyRune('y'))

		if got, want := clipboardWrites(r.clip), []string{"Looks fine."}; !slices.Equal(got, want) {
			t.Errorf("clipboard writes %q, want %q", got, want)
		}
	})
}

func TestConversationKeysClear(t *testing.T) {
	t.Parallel()

	t.Run("search then esc", func(t *testing.T) {
		t.Parallel()

		r := newConvSelRig(t)
		convDragLooks(t, r)
		r.key(keyRune('/'))
		r.key(keyEsc)

		if r.selected() {
			t.Error("the search prompt left the selection")
		}
	})

	t.Run("search without a hint row", func(t *testing.T) {
		t.Parallel()

		a, _ := convApp(t, true, 120, 11)
		r := &convSelRig{a: a, clip: fakeClip(a)}
		convOpen(t, a)

		x0, _, y0 := r.word(t, "@mhersson opened", "@mhersson")
		_, x1, _ := r.word(t, "@mhersson opened", "opened")
		dragRun(a, x0, y0, x1, y0, 0)

		if !r.selected() {
			t.Fatal("no selection after the drag")
		}

		r.key(keyRune('/'))

		if !r.selected() || a.conv.searching {
			t.Errorf("selected = %v, searching = %v; want it kept and no prompt", r.selected(), a.conv.searching)
		}
	})

	t.Run("wheel", func(t *testing.T) {
		t.Parallel()

		r := newConvSelRig(t)
		convDragLooks(t, r)
		r.a.Update(tea.MouseWheelMsg{X: 60, Y: 10, Button: tea.MouseWheelDown})

		if !r.selected() {
			t.Error("the wheel cleared the selection")
		}
	})
}

// convDragLooks selects "Looks fine." by a drag.
func convDragLooks(t *testing.T, r *convSelRig) {
	t.Helper()

	x0, _, y0 := r.word(t, "Looks fine.", "Looks")
	_, x1, _ := r.word(t, "Looks fine.", "fine.")
	dragRun(r.a, x0, y0, x1, y0, 0)

	if !r.selected() {
		t.Fatal("no selection after the drag")
	}
}

func TestConversationRereadSelection(t *testing.T) {
	t.Parallel()

	same := convFor(7)

	changed := convFor(7)
	for i := range changed.Items {
		if changed.Items[i].ID == "PRR_4" {
			changed.Items[i].Body = convText("Looks fine!")
		}
	}

	tests := []struct {
		name string
		next github.Conversation
		keep bool
	}{
		{"same text", same, true},
		{"changed text", changed, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			a, reads := convApp(t, true, 120, 40)
			reads.convs = []github.Conversation{convFor(7), tt.next}
			r := &convSelRig{a: a, clip: fakeClip(a)}
			convOpen(t, a)
			convDragLooks(t, r)

			old := a.conv.doc
			ciDeliver(a, a.readConversation(a.conv, false))

			if a.conv.doc == old {
				t.Fatal("the re-read kept the old doc")
			}

			if got := r.selected(); got != tt.keep {
				t.Errorf("selected = %v, want %v", got, tt.keep)
			}
		})
	}
}

func TestConversationCloseClears(t *testing.T) {
	t.Parallel()

	r := newConvSelRig(t)
	convDragLooks(t, r)
	r.key(keyEsc)

	if r.a.conv != nil {
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

func TestConversationSelectionsAreTheirOverlays(t *testing.T) {
	t.Parallel()

	t.Run("plan selection survives closeConv", func(t *testing.T) {
		t.Parallel()

		r := newConvSelRig(t)
		pv := &planView{}
		r.a.plan = pv
		r.a.text = &textSelection{view: &pv.view}

		r.a.closeConv()

		if r.a.text == nil {
			t.Error("closing the conversation cleared the plan's selection")
		}
	})

	t.Run("conversation selection survives closePlan", func(t *testing.T) {
		t.Parallel()

		r := newConvSelRig(t)
		r.a.plan = &planView{}
		r.a.text = &textSelection{view: &r.a.conv.view}

		r.a.closePlan()

		if r.a.text == nil {
			t.Error("closing the plan cleared the conversation's selection")
		}
	})
}
