package ui

import (
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/exp/golden"

	"github.com/mhersson/aim/internal/core"
	"github.com/mhersson/aim/internal/harness"
)

// cardTicks records the ticks asked for and never fires them.
type cardTicks struct {
	durs []time.Duration
	msgs []tea.Msg
}

func (k *cardTicks) tick(d time.Duration, fn func(time.Time) tea.Msg) tea.Cmd {
	k.durs = append(k.durs, d)
	k.msgs = append(k.msgs, fn(t0))

	return nil
}

// dues are the card timers asked for so far.
func (k *cardTicks) dues() []sentCardDueMsg {
	var out []sentCardDueMsg

	for _, m := range k.msgs {
		if d, ok := m.(sentCardDueMsg); ok {
			out = append(out, d)
		}
	}

	return out
}

func cardApp(t *testing.T) (*App, *cardTicks) {
	t.Helper()

	a, _ := sentApp(t)
	k := &cardTicks{}
	a.tick = k.tick

	return a, k
}

func cardEvent(t *testing.T, id core.PaneID, n int) harness.Event {
	t.Helper()

	dir := t.TempDir()

	var paths []string

	for i := range n {
		p := filepath.Join(dir, fmt.Sprintf("shot%d.png", i))
		writePNG(t, p, 40, 20)

		paths = append(paths, p)
	}

	return harness.Event{Pane: id, Name: "show", Show: true, Images: paths}
}

func TestSentCardWaitsUntilSeen(t *testing.T) {
	t.Parallel()

	a, k := cardApp(t)

	a.Update(tea.BlurMsg{})
	sentSend(t, a, "p2", 1, "")

	if a.sent.card == nil {
		t.Fatal("no card after a batch")
	}

	if n := len(k.dues()); n != 0 {
		t.Fatalf("timers while unfocused = %d, want 0", n)
	}

	a.Update(tea.FocusMsg{})

	dues := k.dues()
	if len(dues) != 1 {
		t.Fatalf("timers after focus = %d, want 1", len(dues))
	}

	for i, m := range k.msgs {
		if _, ok := m.(sentCardDueMsg); ok && k.durs[i] != sentCardFor {
			t.Fatalf("timer = %v, want %v", k.durs[i], sentCardFor)
		}
	}

	a.Update(tea.FocusMsg{})

	if n := len(k.dues()); n != 1 {
		t.Fatalf("timers after a second focus = %d, want still 1", n)
	}

	a.Update(sentCardDueMsg{seq: dues[0].seq - 1})

	if a.sent.card == nil {
		t.Fatal("a stale due message cleared the card")
	}

	a.Update(dues[0])

	if a.sent.card != nil {
		t.Fatal("its due message left the card")
	}
}

func TestSentCardWaitsUnderAModal(t *testing.T) {
	t.Parallel()

	a, k := cardApp(t)

	pressRun(a, keyLeader, keyRune(':'))

	if a.overlay == nil {
		t.Fatal("palette did not open")
	}

	sentSend(t, a, "p2", 1, "")

	if n := len(k.dues()); n != 0 {
		t.Fatalf("timers under the palette = %d, want 0", n)
	}

	if a.sentCardShows() {
		t.Fatal("card shows under the palette")
	}

	pressRun(a, keyEsc)

	if a.overlay != nil {
		t.Fatal("palette did not close")
	}

	if n := len(k.dues()); n != 1 {
		t.Fatalf("timers after the palette closed = %d, want 1", n)
	}
}

func TestSentCardReplacedByANewBatch(t *testing.T) {
	t.Parallel()

	a, k := cardApp(t)

	sentSend(t, a, "p2", 1, "")

	first := *a.sent.card

	sentSend(t, a, "p2", 1, "")

	c := a.sent.card
	if c == nil || c.batch == first.batch || c.seq == first.seq || !c.armed {
		t.Fatalf("card = %+v after a second batch, first %+v", c, first)
	}

	if n := len(k.dues()); n != 2 {
		t.Fatalf("timers = %d, want 2", n)
	}

	a.Update(sentCardDueMsg{seq: first.seq})

	if a.sent.card == nil {
		t.Fatal("the first batch's timer cleared the replacement")
	}

	// A copy that finishes after a newer one does not take the card back.
	c1 := hook(a, cardEvent(t, "p2", 1))
	c2 := hook(a, cardEvent(t, "p2", 1))

	run(a, c2)

	newest := a.sent.card.batch

	run(a, c1)

	if a.sent.card.batch != newest {
		t.Fatalf("card = %+v, want the batch that arrived last (%d)", a.sent.card, newest)
	}
}

func TestSentCardTakesNoKeys(t *testing.T) {
	t.Parallel()

	a, st, _ := kindApp(t)
	chord(a, 'n')

	a.sent.dir = t.TempDir()
	a.tick = (&cardTicks{}).tick

	sentSend(t, a, "p2", 1, "")

	if !a.sentCardShows() {
		t.Fatal("card not on show")
	}

	pressRun(a, keyRune('h'), keyRune('i'), keyRune('l'))

	sent := func() string {
		var b strings.Builder
		for i := range st.count() {
			b.WriteString(st.proc(i).sent())
		}

		return b.String()
	}

	eventually(t, "the keys to reach the pane", func() bool { return sent() == "hil" })

	if a.sent.card == nil || a.sent.view != nil {
		t.Fatalf("card = %+v, view = %+v, want the card kept and no viewer", a.sent.card, a.sent.view)
	}
}

func TestSentCardClickOpensTheViewer(t *testing.T) {
	t.Parallel()

	a, _ := cardApp(t)
	sentSend(t, a, "p2", 1, "")

	x, y, w, h, _, ok := sentCardLayout(a.width, a.height, a.pictures.on())
	if !ok {
		t.Fatal("no card layout")
	}

	click := func(cx, cy int) {
		a.Update(tea.MouseClickMsg{X: cx, Y: cy, Button: tea.MouseLeft})
	}

	click(2, 5)

	if a.sent.view != nil || a.sent.card == nil {
		t.Fatalf("a click outside: view = %+v, card = %+v", a.sent.view, a.sent.card)
	}

	click(x-1, y+1)
	click(x+w, y+1)
	click(x+1, y-1)
	click(x+1, y+h)

	if a.sent.view != nil {
		t.Fatal("a click next to the card opened the viewer")
	}

	click(x+w/2, y+h/2)

	if a.sent.view == nil || a.sent.view.pane != "p2" {
		t.Fatalf("view = %+v after a click on the card, want p2's", a.sent.view)
	}

	if a.sent.card != nil {
		t.Fatal("card stayed after the viewer opened")
	}
}

func TestSentViewOpensTheCardsAgent(t *testing.T) {
	t.Parallel()

	a, _ := cardApp(t)
	chord(a, 'n')

	if kindOf(a, "p3") != core.KindAgent {
		t.Fatal("p3 is not an agent")
	}

	sentSend(t, a, "p2", 1, "")
	sentSend(t, a, "p3", 1, "")

	if err := a.dispatch(core.FocusPane{ID: "p2"}); err != nil {
		t.Fatal(err)
	}

	pressRun(a, keyLeader, keyRune('i'))

	if a.sent.view == nil || a.sent.view.pane != "p3" {
		t.Fatalf("view = %+v, want the card's agent p3", a.sent.view)
	}

	if a.sent.card != nil {
		t.Fatal("card stayed after the viewer opened")
	}
}

func TestSentCardTooSmall(t *testing.T) {
	t.Parallel()

	a, _ := cardApp(t)
	sized(t, a, 30, 10)

	sentSend(t, a, "p2", 1, "")

	if a.sentCardShows() {
		t.Fatal("card shows at 30x10")
	}

	key, _ := a.keymap.keyFor("images.view")
	if key != "ctrl+space i" {
		t.Fatalf("images.view key = %q", key)
	}

	want := noticeSent(sentCardTitle(a.cardName(a.store.State(), "p2"), 0, false, 1), "ctrl+space i")
	if a.notice != want || a.noticeLevel() != levelNotice {
		t.Fatalf("notice = %q at level %v, want %q at levelNotice", a.notice, a.noticeLevel(), want)
	}

	pressRun(a, keyLeader, keyRune('i'))

	if a.sent.view == nil || a.sent.view.pane != "p2" {
		t.Fatalf("view = %+v, want p2's", a.sent.view)
	}
}

func TestSentCardTitle(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		tab      int
		otherTab bool
		n        int
		want     string
	}{
		{"api", 0, false, 1, "api sent an image"},
		{"api", 1, true, 1, "api (tab 2) sent an image"},
		{"api", 0, false, 3, "api sent 3 images"},
		{"api", 2, true, 3, "api (tab 3) sent 3 images"},
	}

	for _, tt := range tests {
		if got := sentCardTitle(tt.name, tt.tab, tt.otherTab, tt.n); got != tt.want {
			t.Errorf("sentCardTitle(%q, %d, %v, %d) = %q, want %q", tt.name, tt.tab, tt.otherTab, tt.n, got, tt.want)
		}
	}
}

func TestSentCardHidesTheCursor(t *testing.T) {
	t.Parallel()

	// The card is at columns 63-98, rows 22-26 of 100x30; row 20 col 42 of p2's
	// content is screen (92, 22), on its top border.
	a, _ := framedApp(t, 100, 30, shellSpec("/w/a", ""), shellSpec("/w/b", "\x1b[20;42H"))
	fakeClip(a)

	if cur := a.cursorView(); cur == nil || cur.X != 92 || cur.Y != 22 {
		t.Fatalf("cursor = %+v, want (92, 22) without a card", cur)
	}

	a.sent.card = &sentCard{batch: 1, seq: 1}

	if cur := a.cursorView(); cur != nil {
		t.Fatalf("cursor = %+v under the card, want none", cur)
	}
}

// sentCardScene shows a card for a batch of n images at 100x30.
func sentCardScene(t *testing.T, n int, caption string, pictures bool) *App {
	t.Helper()

	a, _ := cardApp(t)
	sized(t, a, 100, 30)

	var jobs *recJobs
	if pictures {
		jobs = picturesOn(t, a)
	}

	sentSend(t, a, "p2", n, caption)

	if !a.sentCardShows() {
		t.Fatal("card not on show")
	}

	// The toast of the missing worktree is not what the goldens are about.
	a.notice = ""

	if pictures {
		a.View()
		answerInfo(a, jobs, 400, 200, nil)
		a.View()
		answerRender(t, a, jobs)
	}

	return a
}

func TestSentCardGolden(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		n        int
		caption  string
		pictures bool
	}{
		{"two-pictures", 2, "before and after the fix", true},
		{"three-images", 3, "", true},
		{"no-pictures", 2, "before and after the fix", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			a := sentCardScene(t, tt.n, tt.caption, tt.pictures)
			golden.RequireEqual(t, []byte(plain(a.View().Content)))
		})
	}
}
