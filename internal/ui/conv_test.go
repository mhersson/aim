package ui

import (
	"errors"
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/charmbracelet/x/exp/golden"

	"github.com/mhersson/aim/internal/core"
	"github.com/mhersson/aim/internal/github"
)

// convFor is the conversation fixture as pull request number n's, its times
// moved to the app clock's.
func convFor(n int) github.Conversation {
	c := convFixture()
	c.PR.Number = n

	d := t0.Sub(convNow)
	c.Created = c.Created.Add(d)

	for i := range c.Items {
		c.Items[i].At = c.Items[i].At.Add(d)
	}

	return c
}

// convApp is the PR drawer on #7 with reads that answer convFor(7).
func convApp(t *testing.T, icons bool, w, h int) (*App, *fakeReads) {
	t.Helper()

	s := reviewScene{
		theme: "catppuccin-mocha", icons: icons, w: w, h: h, res: turn4Result(), turn: 4, turns: 4, focused: true,
		branch: true, tune: prSceneWith(nil, 0),
	}

	a := s.build(t)
	reads := &fakeReads{fail: map[string]error{}, convs: []github.Conversation{convFor(7)}}
	a.reads = reads
	a.openLink = (&fakeOpen{}).open

	return a, reads
}

// convOpen presses i and delivers the answer.
func convOpen(t *testing.T, a *App) {
	t.Helper()

	ciDeliver(a, press(a, keyRune('i')))

	if a.conv == nil || a.conv.loading || a.conv.doc == nil {
		t.Fatalf("i opened no loaded conversation; notice %q", a.notice)
	}
}

func TestConversationFromTheList(t *testing.T) {
	t.Parallel()

	mine := map[string]github.MyPRs{"github.com": {Requested: []github.SearchPR{
		searched("github.com/acme/web", 311, "Fix the login form", "kari-dev", 2*time.Hour),
	}}}
	a, reads, _ := searchApp(t, listAgents(), 120, 40, mine, "github.com/acme/web")
	reads.convs = []github.Conversation{convFor(311)}

	openList(a)
	typedList(a, "2")
	ciDeliver(a, press(a, keyRune('i')))

	want := []convCall{{repo: github.Repo{Host: "github.com", Owner: "acme", Name: "web"}, number: 311}}
	if got := reads.convReads(); !slices.Equal(got, want) {
		t.Fatalf("reads = %v, want %v", got, want)
	}

	if a.conv == nil || a.conv.doc == nil {
		t.Fatalf("no conversation shown; notice %q", a.notice)
	}

	if a.conv.number != 311 {
		t.Errorf("the overlay is on #%d, want #311", a.conv.number)
	}

	press(a, keyEsc)

	if a.conv != nil || a.prList == nil {
		t.Fatalf("esc left conv = %v, list = %v; want the list alone", a.conv != nil, a.prList != nil)
	}
}

func TestConversationFromTheDrawer(t *testing.T) {
	t.Parallel()

	t.Run("in PR mode with the diff loaded", func(t *testing.T) {
		t.Parallel()

		r := newPRDrawer(t)
		reads := &fakeReads{fail: map[string]error{}, convs: []github.Conversation{convFor(7)}}
		r.reads = reads

		r.poll()
		r.opened(t)
		r.typed("p")
		r.until(t, "PR mode", r.inPRMode(7))
		r.typed("i")

		if r.conv == nil || r.conv.number != 7 {
			t.Fatalf("i opened %+v; notice %q", r.conv, r.notice)
		}

		r.until(t, "the conversation", func() bool { return r.conv != nil && r.conv.doc != nil })

		if got := reads.convReads(); len(got) != 1 || got[0].repo != prRepo || got[0].number != 7 {
			t.Errorf("reads = %v, want one read of o/r#7", got)
		}
	})

	t.Run("outside PR mode it shows the pull request first", func(t *testing.T) {
		t.Parallel()

		r := newPRDrawer(t)
		reads := &fakeReads{fail: map[string]error{}, convs: []github.Conversation{convFor(7)}}
		r.reads = reads

		r.poll()
		r.opened(t)
		r.typed("i")

		if r.conv != nil {
			t.Fatal("i opened the overlay before the pull request loaded")
		}

		r.until(t, "the conversation", func() bool { return r.conv != nil && r.conv.doc != nil })

		if rv := r.review(); rv == nil || rv.PR == nil || rv.PR.Number != 7 {
			t.Errorf("review = %+v, want the drawer in PR mode on #7", rv)
		}
	})

	t.Run("a modal opened meanwhile gets the notice", func(t *testing.T) {
		t.Parallel()

		r := newPRDrawer(t)
		r.reads = &fakeReads{fail: map[string]error{}, convs: []github.Conversation{convFor(7)}}

		r.poll()
		r.opened(t)
		r.typed("i")
		r.leader('h')

		r.until(t, "the PR to load", r.inPRMode(7))
		r.settle()

		if r.conv != nil {
			t.Error("the late answer opened the conversation")
		}

		if want := r.prLoadedNotice(7, thenConversation); r.notice != want {
			t.Errorf("notice = %q, want %q", r.notice, want)
		}
	})

	t.Run("PR mode's hints hold it before checks", func(t *testing.T) {
		t.Parallel()

		a, _ := convApp(t, true, 200, 48)
		text := shownText(a)

		i, c := strings.Index(text, "i conversation"), strings.Index(text, "c checks")
		if i < 0 || c < 0 || i > c {
			t.Errorf("hints hold i conversation at %d and c checks at %d:\n%s", i, c, text)
		}
	})
}

func TestConversationKeys(t *testing.T) {
	t.Parallel()

	t.Run("] and [ show the item from its top", func(t *testing.T) {
		t.Parallel()

		a, _ := convApp(t, true, 80, 24)
		convOpen(t, a)

		d, v := a.conv.doc, &a.conv.view
		n := len(v.layout().Lines)

		first, ok := d.itemLine(0, 1)
		if !ok {
			t.Fatal("the conversation has no second item")
		}

		press(a, keyRune(']'))

		if v.cursor != first || v.top != min(first, n-v.h) {
			t.Fatalf("] left cursor %d top %d, want both at %d", v.cursor, v.top, first)
		}

		press(a, keyRune('['))

		if v.cursor != 0 || v.top != 0 {
			t.Errorf("[ left cursor %d top %d, want 0 and 0", v.cursor, v.top)
		}

		for {
			before := v.cursor

			press(a, keyRune(']'))

			if v.cursor == before {
				break
			}

			if v.top != min(v.cursor, n-v.h) {
				t.Fatalf("] to %d left top %d, want %d", v.cursor, v.top, min(v.cursor, n-v.h))
			}
		}

		if v.top > n-v.h {
			t.Errorf("the top %d passes the last page %d", v.top, n-v.h)
		}
	})

	t.Run("enter opens the bot row", func(t *testing.T) {
		t.Parallel()

		a, _ := convApp(t, true, 160, 48)
		convOpen(t, a)

		if strings.Contains(shownText(a), "Coverage is 80%.") {
			t.Fatal("the bot comment shows before it was opened")
		}

		lay := a.conv.view.layout()
		if len(lay.Folds) == 0 {
			t.Fatal("the conversation has no fold")
		}

		v := &a.conv.view
		v.cursor = lay.Folds[0].Line
		v.clamp()

		press(a, keyEnter)

		if !strings.Contains(shownText(a), "Coverage is 80%.") {
			t.Errorf("enter did not open the bot row:\n%s", shownText(a))
		}
	})

	t.Run("every other key and the leader are dropped", func(t *testing.T) {
		t.Parallel()

		a, reads := convApp(t, true, 160, 48)
		convOpen(t, a)

		v := &a.conv.view
		v.cursor, v.top = 3, 0
		before, calls := *v, len(reads.convReads())

		for _, k := range []tea.KeyPressMsg{keyRune('x'), keyRune('a'), keyRune('c'), keyRune('y'), keyLeader, keyTab} {
			ciDeliver(a, press(a, k))
		}

		a.Update(tea.PasteMsg{Content: "pasted"})

		if v.cursor != before.cursor || v.top != before.top {
			t.Errorf("a dropped key moved the view to %d/%d", v.cursor, v.top)
		}

		if a.pending || a.popup || a.conv == nil || a.checks != nil || a.merge != nil || a.confirm != nil {
			t.Errorf("a dropped key reached beyond the overlay: pending %v, popup %v", a.pending, a.popup)
		}

		if got := len(reads.convReads()); got != calls {
			t.Errorf("a dropped key read again: %d reads, want %d", got, calls)
		}
	})
}

// convGrown is convFor(7) with a comment added above the review, so the review
// moves down by that comment's lines.
func convGrown() github.Conversation {
	c := convFor(7)
	extra := github.TimelineItem{Kind: github.ItemComment, ID: "IC_0", Author: "pat", At: t0.Add(-150 * time.Minute), Body: convText("One\n\ntwo\n\nthree.")}
	c.Items = append([]github.TimelineItem{extra}, c.Items...)

	return c
}

func TestConversationRereads(t *testing.T) {
	t.Parallel()

	t.Run("r reads again", func(t *testing.T) {
		t.Parallel()

		a, reads := convApp(t, true, 160, 48)
		convOpen(t, a)

		ciDeliver(a, press(a, keyRune('r')))

		if got := len(reads.convReads()); got != 2 {
			t.Errorf("reads = %d, want 2", got)
		}
	})

	t.Run("the poll's update time", func(t *testing.T) {
		t.Parallel()

		r := newPRDrawer(t)
		reads := &fakeReads{fail: map[string]error{}, convs: []github.Conversation{convFor(7)}}
		r.reads = reads

		at := t0.Add(-time.Hour)
		snap := func(at time.Time) {
			pr := prSeven()
			pr.UpdatedAt = at
			flatten(r.step(snapshotOf(pr)))
		}

		snap(at)
		r.opened(t)
		r.typed("p")
		r.until(t, "PR mode", r.inPRMode(7))
		r.typed("i")
		r.until(t, "the conversation", func() bool { return r.conv != nil && r.conv.doc != nil })

		snap(at)

		if got := len(reads.convReads()); got != 1 {
			t.Fatalf("a snapshot of the same update time read again: %d reads", got)
		}

		snap(at.Add(time.Minute))

		if got := len(reads.convReads()); got != 2 {
			t.Fatalf("a newer update time made %d reads, want 2", got)
		}

		snap(at.Add(time.Minute))

		if got := len(reads.convReads()); got != 2 {
			t.Errorf("the same newer time read again: %d reads", got)
		}
	})

	t.Run("an item that grew above keeps the place", func(t *testing.T) {
		t.Parallel()

		a, reads := convApp(t, true, 160, 30)
		reads.convs = []github.Conversation{convFor(7), convGrown()}

		convOpen(t, a)

		v := &a.conv.view
		review, _ := a.conv.doc.itemLine(0, 1)

		for range 3 {
			next, ok := a.conv.doc.itemLine(review, 1)
			if !ok {
				t.Fatal("too few items")
			}

			review = next
		}

		v.cursor, v.top = review+1, review-2
		v.clamp()

		id, off := a.conv.doc.itemAt(v.cursor)
		above := v.cursor - v.top

		ciDeliver(a, press(a, keyRune('r')))

		gotID, gotOff := a.conv.doc.itemAt(v.cursor)
		if gotID != id || gotOff != off {
			t.Errorf("the cursor is on %q+%d, want %q+%d", gotID, gotOff, id, off)
		}

		if v.cursor-v.top != above {
			t.Errorf("the top keeps %d rows above the cursor, want %d", v.cursor-v.top, above)
		}
	})
}

// convWithDetails is convFor(7) with a comment that folds some text and a bot
// comment that folds away.
func convWithDetails() github.Conversation {
	c := convFor(7)
	extra := github.TimelineItem{
		Kind: github.ItemComment, ID: "IC_9", Author: "pat", At: t0.Add(-150 * time.Minute),
		Body: convText("Intro.\n\n<details><summary>More</summary>\n\nhidden words\n\n</details>"),
	}
	c.Items = append([]github.TimelineItem{extra}, c.Items...)

	return c
}

func convShows(a *App, s string) bool {
	a.conv.view.layout()

	return slices.ContainsFunc(a.conv.doc.text(), func(l string) bool { return strings.Contains(l, s) })
}

func TestConversationRereadsKeepWhatWasOpened(t *testing.T) {
	t.Parallel()

	t.Run("an opened bot box", func(t *testing.T) {
		t.Parallel()

		a, reads := convApp(t, true, 160, 48)
		reads.convs = []github.Conversation{convWithDetails()}

		convOpen(t, a)

		if convShows(a, "Coverage is 80%.") {
			t.Fatal("the bot box starts open")
		}

		d := a.conv.doc
		d.toggleFold(d.botBase)

		if !convShows(a, "Coverage is 80%.") {
			t.Fatal("toggling the bot box did not open it")
		}

		v := &a.conv.view
		line := d.lineOf("IC_1", 1)
		v.cursor, v.top = line, max(line-2, 0)
		v.clamp()

		id, off := a.conv.doc.itemAt(v.cursor)

		ciDeliver(a, press(a, keyRune('r')))

		if !convShows(a, "Coverage is 80%.") {
			t.Error("a re-read folded the opened bot box")
		}

		if gotID, gotOff := a.conv.doc.itemAt(v.cursor); gotID != id || gotOff != off {
			t.Errorf("the cursor is on %q+%d, want %q+%d", gotID, gotOff, id, off)
		}
	})

	t.Run("an opened details", func(t *testing.T) {
		t.Parallel()

		a, reads := convApp(t, true, 160, 48)
		reads.convs = []github.Conversation{convWithDetails()}

		convOpen(t, a)

		if convShows(a, "hidden words") {
			t.Fatal("the details start open")
		}

		var folds int

		for _, b := range a.conv.doc.bodies {
			if b.folds() > 0 {
				a.conv.doc.toggleFold(b.foldBase)

				folds++
			}
		}

		if folds != 1 || !convShows(a, "hidden words") {
			t.Fatalf("toggled %d folds; shows hidden = %v", folds, convShows(a, "hidden words"))
		}

		ciDeliver(a, press(a, keyRune('r')))

		if !convShows(a, "hidden words") {
			t.Error("a re-read closed the opened details")
		}
	})
}

func TestConversationFailedReread(t *testing.T) {
	t.Parallel()

	t.Run("keeps the thread and says why", func(t *testing.T) {
		t.Parallel()

		a, reads := convApp(t, true, 160, 48)
		convOpen(t, a)

		doc, cursor := a.conv.doc, a.conv.view.cursor
		reads.convErr = errors.New("boom")

		ciDeliver(a, press(a, keyRune('r')))

		v := a.conv
		if v.doc != doc || v.view.cursor != cursor || v.err != "" {
			t.Errorf("a failed re-read changed the view: same doc %v, cursor %d (was %d), err %q", v.doc == doc, v.view.cursor, cursor, v.err)
		}

		if want := fmt.Sprintf(convReadFailed, "boom"); a.notice != want {
			t.Errorf("notice = %q, want %q", a.notice, want)
		}

		if !convShows(a, "Fixes the drawer.") {
			t.Error("the thread is not drawn")
		}
	})

	t.Run("a failed first read shows the error and r reads again", func(t *testing.T) {
		t.Parallel()

		a, reads := convApp(t, true, 160, 48)
		reads.convErr = errors.New("boom")

		ciDeliver(a, press(a, keyRune('i')))

		if a.conv == nil || a.conv.doc != nil || a.conv.err != "boom" {
			t.Fatalf("conv = %+v", a.conv)
		}

		reads.convErr = nil

		ciDeliver(a, press(a, keyRune('r')))

		if a.conv.doc == nil || a.conv.err != "" {
			t.Error("r did not read again")
		}
	})
}

// convReadsOf runs cmd and returns the conversation answers it raised.
func convReadsOf(cmd tea.Cmd) []convReadMsg {
	var out []convReadMsg

	for _, m := range flatten(cmd) {
		if c, ok := m.(convReadMsg); ok {
			out = append(out, c)
		}
	}

	return out
}

func TestConversationStale(t *testing.T) {
	t.Parallel()

	t.Run("after a newer read", func(t *testing.T) {
		t.Parallel()

		a, reads := convApp(t, true, 160, 48)
		reads.convs = []github.Conversation{convFor(7), convGrown()}

		first := convReadsOf(press(a, keyRune('i')))
		second := convReadsOf(press(a, keyRune('r')))

		if len(first) != 1 || len(second) != 1 {
			t.Fatalf("answers = %d and %d, want one each", len(first), len(second))
		}

		a.Update(first[0])

		if a.conv.doc != nil || !a.conv.loading {
			t.Fatal("the older answer was kept while a newer read is on its way")
		}

		a.Update(second[0])

		doc := a.conv.doc
		if doc == nil || a.conv.loading {
			t.Fatal("the newer answer was dropped")
		}

		a.Update(first[0])

		if a.conv.doc != doc {
			t.Error("the older answer replaced the newer one")
		}
	})

	t.Run("after the overlay reopened on another pull request", func(t *testing.T) {
		t.Parallel()

		a, reads := convApp(t, true, 160, 48)
		first := convReadsOf(press(a, keyRune('i')))
		repo, number := a.conv.repo, a.conv.number

		press(a, keyEsc)
		a.openConversation(repo, number+1, core.PRInfo{})

		if len(first) != 1 {
			t.Fatalf("answers = %d, want 1", len(first))
		}

		calls := len(reads.convReads())

		a.Update(first[0])

		if v := a.conv; v == nil || v.number != number+1 || v.doc != nil || !v.loading {
			t.Errorf("the first view's answer changed the reopened overlay: %+v", v)
		}

		if got := len(reads.convReads()); got != calls {
			t.Errorf("the stale answer issued reads: %d, want %d", got, calls)
		}
	})
}

func TestConversationReadClosesChooser(t *testing.T) {
	t.Parallel()

	a, _ := convApp(t, true, 160, 48)
	convOpen(t, a)

	a.conv.chooser = &linkChooser{}
	ciDeliver(a, a.readConversation(a.conv))

	if a.conv.chooser != nil {
		t.Error("a re-read left the link chooser open on the old document's links")
	}
}

func TestConversationWatch(t *testing.T) {
	t.Parallel()

	mine := map[string]github.MyPRs{"github.com": {Requested: []github.SearchPR{
		searched("github.com/acme/web", 311, "Fix the login form", "kari-dev", 2*time.Hour),
	}}}
	a, reads, _ := searchApp(t, listAgents(), 120, 40, mine, "github.com/acme/web")
	reads.convs = []github.Conversation{convFor(311)}

	web := github.Repo{Host: "github.com", Owner: "acme", Name: "web"}
	visible := func() []int {
		for _, w := range a.prWatches(a.store.State()) {
			if w.Repo == web {
				return w.Visible
			}
		}

		return nil
	}

	openList(a)
	typedList(a, "2")

	if got := visible(); slices.Contains(got, 311) {
		t.Fatalf("#311 is watched before the overlay opened: %v", got)
	}

	ciDeliver(a, press(a, keyRune('i')))

	if got := visible(); !slices.Contains(got, 311) {
		t.Errorf("watched = %v, want #311 visible while it shows", got)
	}

	press(a, keyEsc)

	if got := visible(); slices.Contains(got, 311) {
		t.Errorf("watched = %v after esc, want #311 gone", got)
	}
}

func TestConversationResize(t *testing.T) {
	t.Parallel()

	a, _ := convApp(t, true, 160, 48)
	convOpen(t, a)

	for range 3 {
		press(a, keyRune(']'))
	}

	press(a, keyRune('j'))

	id, off := a.conv.doc.itemAt(a.conv.view.cursor)
	before := a.conv.view.textWidth()

	a.Update(tea.WindowSizeMsg{Width: 90, Height: 48})

	if a.conv.view.textWidth() >= before {
		t.Fatalf("the text did not narrow: %d then %d", before, a.conv.view.textWidth())
	}

	gotID, gotOff := a.conv.doc.itemAt(a.conv.view.cursor)
	if gotID != id || gotOff != off {
		t.Errorf("the cursor is on %q+%d, want %q+%d", gotID, gotOff, id, off)
	}
}

func TestConversationOff(t *testing.T) {
	t.Parallel()

	a, _ := convApp(t, true, 160, 48)
	a.reads = nil

	ciDeliver(a, press(a, keyRune('i')))

	if a.conv != nil {
		t.Error("i opened the overlay without reads")
	}

	if a.notice != noticePRsOff {
		t.Errorf("notice = %q, want %q", a.notice, noticePRsOff)
	}
}

func TestConversationTooSmall(t *testing.T) {
	t.Parallel()

	a, _ := convApp(t, true, 160, 48)
	convOpen(t, a)

	a.Update(tea.WindowSizeMsg{Width: 20, Height: 8})

	if text := shownText(a); strings.Contains(text, "conversation") {
		t.Errorf("20x8 drew the box:\n%s", text)
	}
}

func TestConvRows(t *testing.T) {
	t.Parallel()

	tests := []struct {
		h                  int
		rows               int
		blanks, hint, head bool
	}{
		{12, 3, true, true, true},
		{11, 5, false, true, true},
		{9, 3, false, true, true},
		{8, 3, false, false, true},
		{7, 5, false, false, false},
		{3, 1, false, false, false},
	}

	for _, tc := range tests {
		rows, blanks, hint, head := convRows(tc.h)
		if rows != tc.rows || blanks != tc.blanks || hint != tc.hint || head != tc.head {
			t.Errorf("convRows(%d) = %d %v %v %v, want %d %v %v %v", tc.h, rows, blanks, hint, head, tc.rows, tc.blanks, tc.hint, tc.head)
		}
	}
}

// convScene opens the conversation at the given size. mode says what the read
// answers: "ok", "loading" (nothing delivered) or "error".
func convScene(t *testing.T, icons bool, w, h int, mode string) *App {
	t.Helper()

	a, reads := convApp(t, icons, w, h)
	if mode == "error" {
		reads.convErr = errors.New("HTTP 502 Bad Gateway")
	}

	cmd := press(a, keyRune('i'))
	if mode != "loading" {
		ciDeliver(a, cmd)
	}

	return a
}

type convSceneCase struct {
	name string
	w, h int
	mode string
}

func convScenes() []convSceneCase {
	return []convSceneCase{
		{"160x48", 160, 48, "ok"},
		{"120x40", 120, 40, "ok"},
		{"loading", 120, 30, "loading"},
		{"read-failed", 120, 30, "error"},
		{"80x24", 80, 24, "ok"},
		{"40x12", 40, 12, "ok"},
	}
}

// TestConversationGolden draws the overlay once in colour; it compares
// highlighted colours, so it runs serially.
func TestConversationGolden(t *testing.T) {
	for _, c := range convScenes() {
		t.Run("catppuccin-mocha/icons=true/"+c.name, func(t *testing.T) {
			golden.RequireEqual(t, []byte(convScene(t, true, c.w, c.h, c.mode).View().Content))
		})
	}
}

// TestConversationText is the icons=false screens without styling; the colour
// golden of the icons=true ones holds the same text.
func TestConversationText(t *testing.T) {
	t.Parallel()

	for _, c := range convScenes() {
		t.Run("icons=false/"+c.name, func(t *testing.T) {
			t.Parallel()
			golden.RequireEqual(t, []byte(ansi.Strip(convScene(t, false, c.w, c.h, c.mode).View().Content)))
		})
	}
}

// convLinkBody is a description with one link on a line, two on a line, a
// relative link and a hostile one.
const convLinkBody = "One [a](https://example.com/a) link.\n\n" +
	"Two [b](https://example.com/b) and [c](https://example.com/c) links.\n\n" +
	"Relative [r](/octo/repo/files) link.\n\n" +
	"Plain line with nothing."

// convReading is the PR drawer on #7 showing a conversation whose description
// is convLinkBody and whose first comment holds a javascript: link; opener
// records the links followed.
func convReading(t *testing.T, w, h int) (a *App, opener *fakeOpen) {
	t.Helper()

	a, reads := convApp(t, true, w, h)
	opener = &fakeOpen{}
	a.openLink = opener.open

	c := convFor(7)
	c.Body = github.Text{Markdown: convLinkBody}
	c.Items = append([]github.TimelineItem{{
		Kind: github.ItemComment, ID: "IC_9", Author: "kari-dev", At: c.Created.Add(time.Minute),
		Body: github.Text{Markdown: "Evil [x](javascript:alert(1)) link."},
	}}, c.Items...)
	reads.convs = []github.Conversation{c}

	convOpen(t, a)

	return a, opener
}

// convOnLine puts the cursor on the first line holding s.
func convOnLine(t *testing.T, a *App, s string) {
	t.Helper()

	v := &a.conv.view
	v.layout()

	for i, l := range v.doc.text() {
		if strings.Contains(l, s) {
			v.cursor = i
			v.clamp()

			return
		}
	}

	t.Fatalf("no line holds %q", s)
}

func TestConversationLinks(t *testing.T) {
	t.Parallel()

	t.Run("enter on one link opens it and the overlay stays", func(t *testing.T) {
		t.Parallel()

		a, opener := convReading(t, 160, 48)
		convOnLine(t, a, "One a link")
		ciDeliver(a, press(a, keyEnter))

		if got := opener.urls(); !slices.Equal(got, []string{"https://example.com/a"}) {
			t.Errorf("opened %q", got)
		}

		if a.conv == nil || a.conv.chooser != nil {
			t.Error("the overlay should stay open without a chooser")
		}
	})

	t.Run("two links open the chooser inside the box", func(t *testing.T) {
		t.Parallel()

		a, opener := convReading(t, 160, 48)
		convOnLine(t, a, "Two b and c")
		press(a, keyEnter)

		if a.conv.chooser == nil || len(a.conv.chooser.targets) != 2 {
			t.Fatalf("chooser = %+v, want two targets", a.conv.chooser)
		}

		press(a, keyRune('j'))
		ciDeliver(a, press(a, keyEnter))

		if got := opener.urls(); !slices.Equal(got, []string{"https://example.com/c"}) {
			t.Errorf("opened %q, want the selected link", got)
		}

		if a.conv == nil || a.conv.chooser != nil {
			t.Error("enter should close the chooser and keep the overlay")
		}
	})

	t.Run("esc closes the chooser, then the overlay", func(t *testing.T) {
		t.Parallel()

		a, _ := convReading(t, 160, 48)
		convOnLine(t, a, "Two b and c")
		press(a, keyEnter)
		press(a, keyEsc)

		if a.conv == nil || a.conv.chooser != nil {
			t.Fatal("the first esc should close the chooser alone")
		}

		press(a, keyEsc)

		if a.conv != nil {
			t.Error("the second esc should close the overlay")
		}
	})

	t.Run("a relative link resolves against the pull request's page", func(t *testing.T) {
		t.Parallel()

		a, opener := convReading(t, 160, 48)
		convOnLine(t, a, "Relative r link")
		ciDeliver(a, press(a, keyEnter))

		if got := opener.urls(); !slices.Equal(got, []string{"https://github.com/octo/repo/files"}) {
			t.Errorf("opened %q", got)
		}
	})

	t.Run("a javascript link has no target and never reaches the screen", func(t *testing.T) {
		t.Parallel()

		a, opener := convReading(t, 160, 48)
		convOnLine(t, a, "Evil x link")

		if got := a.conv.view.targets(); len(got) != 0 {
			t.Errorf("targets = %+v, want none", got)
		}

		ciDeliver(a, press(a, keyEnter))

		if got := opener.urls(); len(got) != 0 {
			t.Errorf("opened %q", got)
		}

		if raw := a.View().Content; strings.Contains(raw, "javascript:") {
			t.Error("a javascript: link reached the raw view")
		}
	})
}

func TestConversationSearch(t *testing.T) {
	t.Parallel()

	typed := func(a *App, s string) {
		for _, c := range s {
			a.Update(keyRune(c))
		}
	}
	cursorText := func(a *App) string { return a.conv.view.doc.text()[a.conv.view.cursor] }

	t.Run("the prompt takes the hint row", func(t *testing.T) {
		t.Parallel()

		a, _ := convReading(t, 160, 48)
		press(a, keyRune('/'))

		if !a.conv.searching {
			t.Fatal("/ did not open the prompt")
		}

		typed(a, "lin")
		a.Update(tea.PasteMsg{Content: "ks"})

		if a.conv.query != "links" {
			t.Errorf("typed and pasted: query = %q", a.conv.query)
		}

		a.Update(tea.KeyPressMsg{Code: tea.KeyBackspace})

		if a.conv.query != "link" {
			t.Errorf("after backspace: query = %q", a.conv.query)
		}
	})

	t.Run("enter finds, n and N repeat", func(t *testing.T) {
		t.Parallel()

		a, _ := convReading(t, 160, 48)
		press(a, keyRune('/'))
		typed(a, "link")
		press(a, keyEnter)

		if a.conv.searching || !strings.Contains(cursorText(a), "link") {
			t.Fatalf("after enter: searching %v, cursor on %q", a.conv.searching, cursorText(a))
		}

		first := a.conv.view.cursor

		press(a, keyRune('n'))

		if a.conv.view.cursor == first || !strings.Contains(strings.ToLower(cursorText(a)), "link") {
			t.Errorf("n: cursor %d on %q", a.conv.view.cursor, cursorText(a))
		}

		press(a, keyRune('N'))

		if a.conv.view.cursor != first {
			t.Errorf("N: cursor %d, want %d", a.conv.view.cursor, first)
		}
	})

	t.Run("a miss says so", func(t *testing.T) {
		t.Parallel()

		a, _ := convReading(t, 160, 48)
		press(a, keyRune('/'))
		typed(a, "zzzz")
		press(a, keyEnter)

		if a.notice != noticeNotFound+"zzzz" {
			t.Errorf("notice = %q", a.notice)
		}
	})

	t.Run("esc closes the prompt, then the overlay", func(t *testing.T) {
		t.Parallel()

		a, _ := convReading(t, 160, 48)
		press(a, keyRune('/'))
		typed(a, "ab")
		press(a, keyEsc)

		if a.conv == nil || a.conv.searching || a.conv.query != "" {
			t.Fatalf("the first esc should close the prompt alone: %+v", a.conv)
		}

		press(a, keyEsc)

		if a.conv != nil {
			t.Error("the second esc should close the overlay")
		}
	})

	t.Run("a paste outside the prompt is dropped", func(t *testing.T) {
		t.Parallel()

		a, _ := convReading(t, 160, 48)
		a.Update(tea.PasteMsg{Content: "abc"})

		if a.conv.query != "" || a.conv.searching {
			t.Errorf("a paste changed the query: %+v", a.conv)
		}
	})
}

func TestConversationPictures(t *testing.T) {
	t.Parallel()

	a, _ := convApp(t, true, 160, 48)
	a.pictures.getenv = func(string) string { return "" } // not the process's TMUX or AIM_PANE
	jobs := picturesOn(t, a)

	convOpen(t, a)
	a.View()

	got := jobs.take()
	if len(got) == 0 {
		t.Fatal("the registry was asked for nothing")
	}

	for _, j := range got {
		if j.key.src != convShotURL || j.host != "github.com" {
			t.Errorf("job for %q on host %q, want the alias URL on github.com", j.key.src, j.host)
		}
	}

	jobs.jobs = got

	if n := answerInfo(a, jobs, 5, 3, nil); n == 0 {
		t.Fatal("no info job")
	}

	a.View()

	if got := answerRender(t, a, jobs); len(got) != 1 {
		t.Fatalf("render answers = %q", got)
	}

	id := a.conv.doc.layout(a.conv.view.w, true).Images[0].ID
	if len(placeholders(a.View().Content, id)) == 0 {
		t.Fatal("the picture does not draw in the box")
	}

	a.conv.chooser = &linkChooser{targets: []mdTarget{
		{Text: "one", URL: "https://example.com/1"},
		{Text: "two", URL: "https://example.com/2"},
		{Text: "three", URL: "https://example.com/3"},
		{Text: "four", URL: "https://example.com/4"},
	}}

	if got := placeholders(a.View().Content, id); len(got) != 0 {
		t.Errorf("placeholders under the chooser: %+v", got)
	}
}

func TestConversationWheel(t *testing.T) {
	t.Parallel()

	t.Run("over the box", func(t *testing.T) {
		t.Parallel()

		a, _ := convApp(t, true, 120, 24)
		convOpen(t, a)

		v := &a.conv.view
		v.cursor = 5

		a.Update(tea.MouseWheelMsg{X: 60, Y: 10, Button: tea.MouseWheelDown})

		if v.top != 3 || v.cursor != 5 {
			t.Errorf("top, cursor = %d, %d; want 3, 5", v.top, v.cursor)
		}

		a.Update(tea.MouseWheelMsg{X: 60, Y: 10, Button: tea.MouseWheelDown})

		if v.top != 6 || v.cursor != 6 {
			t.Errorf("top, cursor = %d, %d; want 6, 6 (the cursor follows only when it would leave)", v.top, v.cursor)
		}

		a.Update(tea.MouseWheelMsg{X: 60, Y: 10, Button: tea.MouseWheelUp})

		if v.top != 3 {
			t.Errorf("top = %d after a notch up, want 3", v.top)
		}
	})

	t.Run("outside the box and clicks", func(t *testing.T) {
		t.Parallel()

		a, _ := convApp(t, true, 120, 24)
		convOpen(t, a)

		v := &a.conv.view

		a.Update(tea.MouseWheelMsg{X: 1, Y: 1, Button: tea.MouseWheelDown})
		a.Update(tea.MouseWheelMsg{X: 60, Y: 23, Button: tea.MouseWheelDown})
		a.Update(tea.MouseClickMsg{X: 60, Y: 10, Button: tea.MouseLeft})
		a.Update(tea.MouseClickMsg{X: 1, Y: 1, Button: tea.MouseLeft})

		if v.top != 0 || v.cursor != 0 || a.conv == nil {
			t.Errorf("top, cursor = %d, %d, open %v; want nothing changed", v.top, v.cursor, a.conv != nil)
		}
	})
}

// convReadingScene opens the link fixture and puts it in a reading state.
func convReadingScene(t *testing.T, icons bool, name string) *App {
	t.Helper()

	a, _ := convReading(t, 120, 40)
	a.icons = icons

	switch name {
	case "search":
		press(a, keyRune('/'))

		for _, c := range "link" {
			a.Update(keyRune(c))
		}
	case "chooser":
		convOnLine(t, a, "Two b and c")
		press(a, keyEnter)
	}

	return a
}

// TestConversationReadingGolden draws the search prompt and the chooser in
// colour; it compares highlighted colours, so it runs serially.
func TestConversationReadingGolden(t *testing.T) {
	for _, name := range []string{"search", "chooser"} {
		t.Run("catppuccin-mocha/icons=true/"+name, func(t *testing.T) {
			golden.RequireEqual(t, []byte(convReadingScene(t, true, name).View().Content))
		})
	}
}

func TestConversationReadingText(t *testing.T) {
	t.Parallel()

	for _, name := range []string{"search", "chooser"} {
		t.Run("icons=false/"+name, func(t *testing.T) {
			t.Parallel()
			golden.RequireEqual(t, []byte(shownText(convReadingScene(t, false, name))))
		})
	}
}
