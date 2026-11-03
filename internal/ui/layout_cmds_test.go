package ui

import (
	"fmt"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/mhersson/aim/internal/core"
	"github.com/mhersson/aim/internal/harness"
)

// seq presses the leader and then each key of keys, one rune per key; spaces
// only group the keys for reading.
func seq(a *App, keys string) {
	press(a, keyLeader)

	for _, r := range strings.ReplaceAll(keys, " ", "") {
		press(a, keyRune(r))
	}
}

// toastLine is the text row of the toast: the row above the last.
func toastLine(a *App) string {
	lines := strings.Split(ansi.Strip(a.View().Content), "\n")

	return lines[len(lines)-2]
}

func tabIDs(a *App, i int) []core.PaneID { return a.store.State().Tabs[i].Panes }

// wideTabs is a sized app with n tabs of one shell each, wide enough for notices.
func wideTabs(t *testing.T, n int) (*App, *starts) {
	t.Helper()

	a, st := lifeAppSized(t, 140, 30)
	for range n - 1 {
		chord(a, 'c')
	}

	return a, st
}

// splitApp is tab 1 with p1 and p2 (p2 focused) and tab 2 with p3; tab 1 is active.
func splitApp(t *testing.T) (*App, *starts) {
	t.Helper()

	a, st := lifeAppSized(t, 140, 30)
	chord(a, 'v')
	chord(a, 'c')
	press(a, keyLeader, keyRune('1'))

	if activeIndex(a) != 0 || a.store.State().Tab().Focus != "p2" {
		t.Fatalf("setup: active %d focus %s", activeIndex(a), a.store.State().Tab().Focus)
	}

	return a, st
}

func TestMoveFollowsAndKeepsThePane(t *testing.T) {
	t.Parallel()
	a, st := wideTabs(t, 3)
	p3 := a.panes["p3"]
	fp := st.proc(2)

	var sb strings.Builder
	for i := range 80 {
		fmt.Fprintf(&sb, "line %d\r\n", i)
	}

	if _, err := fp.feed.Write([]byte(sb.String())); err != nil {
		t.Fatal(err)
	}

	hist := func() int {
		p3.mu.Lock()
		defer p3.mu.Unlock()

		return p3.histLenLocked()
	}

	eventually(t, "history", func() bool { return hist() > 20 })

	before, resizes := hist(), len(fp.resized())

	seq(a, "> 1")

	s := a.store.State()
	if len(s.Tabs) != 2 || s.Active != 0 {
		t.Fatalf("tabs %d active %d, want 2 tabs and tab 1 active", len(s.Tabs), s.Active)
	}

	if got := tabIDs(a, 0); len(got) != 2 || got[1] != "p3" || s.Tab().Focus != "p3" {
		t.Errorf("tab 1 = %v focus %s, want p1 p3 and p3 focused", got, s.Tab().Focus)
	}

	if a.panes["p3"] != p3 {
		t.Error("the pane value changed")
	}

	if isClosed(fp) {
		t.Error("the process was closed")
	}

	if len(fp.resized()) <= resizes {
		t.Error("the PTY was not resized")
	}

	if got := hist(); got < before {
		t.Errorf("history = %d lines, had %d", got, before)
	}

	assertTabSizes(t, a)

	if cols, rows := p3.size(); [2]int{cols, rows} != paneSize(t, a, "p3") {
		t.Errorf("size = %dx%d, want %v", cols, rows, paneSize(t, a, "p3"))
	}
}

func TestSendStaysAndBreakFollows(t *testing.T) {
	t.Parallel()
	t.Run("send", func(t *testing.T) {
		a, _ := splitApp(t)
		seq(a, ". 2")

		if activeIndex(a) != 0 {
			t.Errorf("active = %d, want tab 1", activeIndex(a))
		}

		if got := tabIDs(a, 1); len(got) != 2 || got[1] != "p2" {
			t.Errorf("tab 2 = %v, want p3 p2", got)
		}

		if got := tabIDs(a, 0); len(got) != 1 || got[0] != "p1" {
			t.Errorf("tab 1 = %v, want p1", got)
		}

		assertTabSizes(t, a)
	})

	t.Run("send from the emptied tab", func(t *testing.T) {
		a, _ := wideTabs(t, 2)
		seq(a, ". 1")

		if len(a.store.State().Tabs) != 1 || activeIndex(a) != 0 {
			t.Errorf("tabs %d active %d, want one tab, active", len(a.store.State().Tabs), activeIndex(a))
		}
	})

	t.Run("break", func(t *testing.T) {
		a, _ := splitApp(t)
		press(a, keyLeader, keyRune('!'))

		s := a.store.State()
		if len(s.Tabs) != 3 || s.Active != 2 || s.Tab().Focus != "p2" || len(s.Tab().Panes) != 1 {
			t.Errorf("tabs %d active %d focus %s", len(s.Tabs), s.Active, s.Tab().Focus)
		}

		assertTabSizes(t, a)
	})

	t.Run("a new tab", func(t *testing.T) {
		a, _ := splitApp(t)
		seq(a, "> 3")

		s := a.store.State()
		if len(s.Tabs) != 3 || s.Active != 2 || s.Tab().Focus != "p2" {
			t.Errorf("tabs %d active %d focus %s", len(s.Tabs), s.Active, s.Tab().Focus)
		}
	})
}

func TestMoveNotices(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		keys string
		want string
	}{
		{"same tab", "> 1", moveNotice(core.ErrSameTab, 1)},
		{"same tab send", ". 1", moveNotice(core.ErrSameTab, 1)},
		{"no tab", "> 3", moveNotice(core.ErrNoTab, 3)},
		{"no tab send", ". 9", moveNotice(core.ErrNoTab, 9)},
		{"alone move", "> 2", moveNotice(core.ErrAlone, 2)},
		{"alone send", ". 2", moveNotice(core.ErrAlone, 2)},
		{"alone break", "!", moveNotice(core.ErrAlone, 0)},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			a, _ := wideTabs(t, 1)
			seq(a, tt.keys)

			if a.notice != tt.want {
				t.Errorf("notice = %q, want %q", a.notice, tt.want)
			}

			if !strings.Contains(toastLine(a), tt.want) {
				t.Errorf("toast lacks %q: %q", tt.want, toastLine(a))
			}

			if a.pending {
				t.Error("leader still pending")
			}
		})
	}
}

func TestBreakWithNineTabs(t *testing.T) {
	t.Parallel()
	a, _ := wideTabs(t, 9)
	chord(a, 'v')
	seq(a, "!")

	if a.notice != "" {
		t.Errorf("notice = %q", a.notice)
	}

	if got := len(a.store.State().Tabs); got != 10 || activeIndex(a) != 9 {
		t.Errorf("tabs %d active %d", got, activeIndex(a))
	}
}

func TestMoveToTabNineAndLeft(t *testing.T) {
	t.Parallel()
	t.Run("nine tabs", func(t *testing.T) {
		a, _ := wideTabs(t, 9)
		seq(a, "> 9")

		if a.notice != moveNotice(core.ErrSameTab, 9) {
			t.Errorf("notice = %q", a.notice)
		}

		press(a, keyLeader, keyRune('1'))
		seq(a, "> 9")

		s := a.store.State()
		if len(s.Tabs) != 8 || s.Active != 7 || s.Tab().Focus != "p1" {
			t.Errorf("tabs %d active %d focus %s, want 8 tabs, tab 8 active holding p1", len(s.Tabs), s.Active, s.Tab().Focus)
		}
	})

	t.Run("send left of the source", func(t *testing.T) {
		a, _ := wideTabs(t, 3)
		seq(a, ". 1")

		s := a.store.State()
		if len(s.Tabs) != 2 || s.Active != 0 {
			t.Errorf("tabs %d active %d, want 2 tabs, the target active", len(s.Tabs), s.Active)
		}

		assertTabSizes(t, a)
	})
}

// scrollingMoveApp has tab 1 with p1 and p2 (p2 focused) and tab 2 with p3 and p4,
// both scrolling with two visible columns, so p2 keeps its size when it moves to tab 2.
func scrollingMoveApp(t *testing.T) (*App, *starts) {
	t.Helper()

	a, st := lifeAppSized(t, 140, 30)
	chord(a, 'v')
	c, _ := a.reg.get("layout.set.scrolling")
	c.run(a)
	chord(a, 'c')
	c.run(a)
	chord(a, 'v')
	press(a, keyLeader, keyRune('1'))

	if got := tabIDs(a, 1); len(got) != 2 {
		t.Fatalf("setup: tab 2 = %v", got)
	}

	return a, st
}

func TestMoveEndsCopyModeAndSelection(t *testing.T) {
	t.Parallel()
	// p2 keeps its size on the move, so only the explicit calls can end copy
	// mode and the selection.
	a, st := scrollingMoveApp(t)
	fp := st.proc(1)
	before := len(fp.resized())

	a.enterCopyMode()

	if a.copy == nil {
		t.Fatal("copy mode did not start")
	}

	a.sel = &paneSelection{pane: "p2"}

	c, _ := a.reg.get("pane.send.tab.2")
	run(a, c.run(a))

	if i, _ := a.store.State().TabOf("p2"); i != 1 {
		t.Fatalf("p2 is in tab %d, want 2", i+1)
	}

	if len(fp.resized()) != before {
		t.Fatal("the moved pane was resized, so relayout could end copy mode on its own")
	}

	if a.copy != nil || a.sel != nil {
		t.Errorf("copy = %v selection = %v after the move, want both gone", a.copy, a.sel)
	}
}

func TestMoveOfZoomedPane(t *testing.T) {
	t.Parallel()
	a, _ := splitApp(t)
	p2 := a.panes["p2"]

	chord(a, 'z')

	if !a.store.State().Tab().Zoomed {
		t.Fatal("setup: not zoomed")
	}

	seq(a, "> 2")

	s := a.store.State()
	if s.Active != 1 || s.Tab().Focus != "p2" || s.Tab().Zoomed || s.Tabs[0].Zoomed {
		t.Errorf("active %d focus %s zoom target %v source %v", s.Active, s.Tab().Focus, s.Tab().Zoomed, s.Tabs[0].Zoomed)
	}

	if a.panes["p2"] != p2 {
		t.Error("the pane value changed")
	}

	assertTabSizes(t, a)

	if cols, rows := p2.size(); [2]int{cols, rows} != paneSize(t, a, "p2") || cols < 2 || rows < 2 {
		t.Errorf("p2 size = %dx%d, want %v", cols, rows, paneSize(t, a, "p2"))
	}

	// p1 grew back into the freed source tab.
	if cols, rows := a.panes["p1"].size(); cols < 2 || rows < 2 {
		t.Errorf("p1 size = %dx%d", cols, rows)
	}
}

func TestSendIntoZoomedHiddenTab(t *testing.T) {
	t.Parallel()
	a, _ := splitApp(t)
	p2 := a.panes["p2"]

	press(a, keyLeader, keyRune('2'))
	chord(a, 'z')
	press(a, keyLeader, keyRune('1'))

	if !a.store.State().Tabs[1].Zoomed || activeIndex(a) != 0 {
		t.Fatal("setup: tab 2 is not zoomed or tab 1 is not active")
	}

	seq(a, ". 2")

	s := a.store.State()
	if s.Active != 0 || s.Tabs[1].Zoomed || s.Tabs[1].Focus != "p2" || len(s.Tabs[1].Panes) != 2 {
		t.Errorf("active %d target zoom %v focus %s panes %v", s.Active, s.Tabs[1].Zoomed, s.Tabs[1].Focus, s.Tabs[1].Panes)
	}

	if a.panes["p2"] != p2 {
		t.Error("the pane value changed")
	}

	assertTabSizes(t, a)

	r, ok := a.tabRects(1)["p2"]
	if !ok {
		t.Fatal("p2 has no rect in tab 2")
	}

	c := contentRect(r)
	if cols, rows := p2.size(); cols != c.W || rows != c.H || cols < 2 || rows < 2 {
		t.Errorf("p2 size = %dx%d, want %dx%d", cols, rows, c.W, c.H)
	}

	if cols, rows := a.panes["p3"].size(); cols < 2 || rows < 2 {
		t.Errorf("p3 size = %dx%d, still shrunk by the zoom", cols, rows)
	}
}

func TestMoveKeepsOtherSelection(t *testing.T) {
	t.Parallel()
	a, _ := splitApp(t)
	a.sel = &paneSelection{pane: "p3"}

	seq(a, ". 3")

	if a.sel == nil {
		t.Error("the selection of another pane was cleared")
	}
}

func TestMoveKeepsAgentState(t *testing.T) {
	t.Parallel()
	a, _, _ := agentApp(t)
	hook(a, harness.Event{Pane: "p2", Name: "SessionStart", Session: "s1", Transcript: "/t/1.jsonl", Start: true, State: core.AgentIdle})
	hook(a, harness.Event{Pane: "p2", Name: "UserPromptSubmit", State: core.AgentWorking})

	p2 := a.panes["p2"]

	seq(a, "> 2")

	m := a.store.State().Panes["p2"]
	if m.State != core.AgentWorking || m.Session != "s1" || m.Kind != core.KindAgent {
		t.Errorf("pane = %+v, want a working agent with its session", m)
	}

	if a.panes["p2"] != p2 {
		t.Error("the pane value changed")
	}

	if s := a.store.State(); len(s.Tabs) != 2 || s.Active != 1 {
		t.Errorf("tabs %d active %d, want a new tab 2 active", len(s.Tabs), s.Active)
	}

	assertTabSizes(t, a)
}

func TestLayoutKeys(t *testing.T) {
	t.Parallel()
	t.Run("cycle", func(t *testing.T) {
		a, _ := splitApp(t)

		for _, want := range []core.LayoutMode{core.ModeMaster, core.ModeScrolling, core.ModeBSP} {
			press(a, keyLeader, keyRune(' '))

			if got := a.store.State().Tab().Mode; got != want || !strings.Contains(a.notice, string(want)) {
				t.Errorf("mode = %s, notice = %q, want the mode %s", got, a.notice, want)
			}
		}

		assertTabSizes(t, a)
	})

	t.Run("set to the current mode", func(t *testing.T) {
		a, _ := splitApp(t)
		c, _ := a.reg.get("layout.set.bsp")
		c.run(a)

		if !strings.Contains(a.notice, string(core.ModeBSP)) {
			t.Errorf("notice = %q, want the current mode", a.notice)
		}

		c, _ = a.reg.get("layout.set.scrolling")
		c.run(a)

		if !strings.Contains(a.notice, string(core.ModeScrolling)) || a.store.State().Tab().Mode != core.ModeScrolling {
			t.Errorf("notice = %q mode = %s", a.notice, a.store.State().Tab().Mode)
		}
	})

	t.Run("preselect then split", func(t *testing.T) {
		a, _ := lifeAppSized(t, 140, 30)
		seq(a, ";h")

		if a.pending || a.store.State().Tab().Preselect != core.Left {
			t.Fatalf("pending %v preselect %v", a.pending, a.store.State().Tab().Preselect)
		}

		chord(a, 'v')

		r := a.layoutRects()
		if r["p2"].X >= r["p1"].X {
			t.Errorf("p2 at x=%d, p1 at x=%d, want p2 on the left", r["p2"].X, r["p1"].X)
		}

		if a.store.State().Tab().Preselect != core.NoDir {
			t.Error("preselection not cleared by the split")
		}
	})

	t.Run("preselect esc clears", func(t *testing.T) {
		a, _ := lifeAppSized(t, 140, 30)
		seq(a, ";l")
		press(a, keyLeader, keyRune(';'), keyEsc)

		if a.store.State().Tab().Preselect != core.NoDir || a.pending || a.notice != "" {
			t.Errorf("preselect %v pending %v notice %q", a.store.State().Tab().Preselect, a.pending, a.notice)
		}
	})

	t.Run("preselect outside bsp", func(t *testing.T) {
		a, _ := splitApp(t)
		press(a, keyLeader, keyRune(' '))
		a.notice = ""
		press(a, keyLeader, keyRune(';'))

		if a.notice != noticePreselectBSP || a.pending || a.prefix != "" {
			t.Errorf("notice %q pending %v prefix %q", a.notice, a.pending, a.prefix)
		}

		a.notice = ""
		c, _ := a.reg.get("pane.preselect.up")
		c.run(a)

		if a.notice != noticePreselectBSP {
			t.Errorf("palette notice = %q", a.notice)
		}

		a.notice = ""
		c, _ = a.reg.get("pane.preselect.clear")
		c.run(a)

		if a.notice != noticePreselectBSP {
			t.Errorf("clear notice = %q", a.notice)
		}
	})
}

func TestRotateEqualizePromote(t *testing.T) {
	t.Parallel()
	t.Run("rotate", func(t *testing.T) {
		a, _ := lifeAppSized(t, 140, 30)
		chord(a, 'r')

		if a.notice != layoutNotice(core.ErrNothingToRotate, "rotate") {
			t.Errorf("notice = %q", a.notice)
		}

		chord(a, 'v')

		r := a.layoutRects()
		if r["p2"].X <= r["p1"].X || r["p2"].Y != r["p1"].Y {
			t.Fatalf("rects %v", r)
		}

		chord(a, 'r')

		r = a.layoutRects()
		if r["p2"].Y <= r["p1"].Y {
			t.Errorf("p2 not below p1 after rotate: %v", r)
		}

		assertSizes(t, a)
	})

	t.Run("equalize", func(t *testing.T) {
		a, _ := lifeAppSized(t, 140, 30)
		chord(a, 'v')

		even := a.layoutRects()

		press(a, keyLeader, keyRune('H'))

		if a.layoutRects()["p1"].W == even["p1"].W {
			t.Fatal("resize changed nothing")
		}

		chord(a, '=')

		if got := a.layoutRects()["p1"].W; got != even["p1"].W {
			t.Errorf("p1 width = %d, want %d", got, even["p1"].W)
		}
	})

	t.Run("promote", func(t *testing.T) {
		a, _ := lifeAppSized(t, 140, 30)
		chord(a, 'v')
		press(a, keyLeader, keyEnter)

		if a.notice != layoutNotice(core.ErrNeedsMaster, "promote") {
			t.Errorf("notice = %q", a.notice)
		}

		c, _ := a.reg.get("layout.set.master-stack")
		c.run(a)
		press(a, keyLeader, keyEnter)

		if got := tabIDs(a, 0)[0]; got != a.store.State().Tab().Focus {
			t.Errorf("first pane %s, focus %s, want the focused pane first", got, a.store.State().Tab().Focus)
		}

		assertSizes(t, a)
	})

	t.Run("rotate outside bsp", func(t *testing.T) {
		a, _ := splitApp(t)
		c, _ := a.reg.get("layout.set.master-stack")
		c.run(a)
		chord(a, 'r')

		if a.notice != layoutNotice(core.ErrNeedsBSP, "rotate") {
			t.Errorf("notice = %q", a.notice)
		}
	})
}

func TestPrefixSequences(t *testing.T) {
	t.Parallel()
	t.Run("escape cancels", func(t *testing.T) {
		a, _ := splitApp(t)
		press(a, keyLeader, keyRune('>'))

		if !a.pending || a.popup || a.prefix != ">" {
			t.Fatalf("pending %v popup %v prefix %q", a.pending, a.popup, a.prefix)
		}

		press(a, keyEsc)

		if a.pending || a.prefix != "" || a.notice != "" || len(tabIDs(a, 1)) != 1 {
			t.Errorf("pending %v prefix %q notice %q", a.pending, a.prefix, a.notice)
		}
	})

	t.Run("unbound second key", func(t *testing.T) {
		a, _ := splitApp(t)
		seq(a, ">x")

		if !strings.Contains(a.notice, "ctrl+space > x") || a.pending {
			t.Errorf("notice %q pending %v", a.notice, a.pending)
		}

		seq(a, ";x")

		if !strings.Contains(a.notice, "ctrl+space ; x") {
			t.Errorf("notice %q", a.notice)
		}
	})

	t.Run("leader key goes to the pane", func(t *testing.T) {
		a, st := splitApp(t)
		press(a, keyLeader, keyRune('.'), keyLeader)

		if a.pending || a.notice != "" {
			t.Errorf("pending %v notice %q", a.pending, a.notice)
		}

		if got := settled(t, a, "p2", st.proc(1)); got == "" {
			t.Error("the leader key did not reach the pane")
		}
	})

	t.Run("paste ends it", func(t *testing.T) {
		a, st := splitApp(t)
		press(a, keyLeader, keyRune('>'))
		a.Update(tea.PasteMsg{Content: "one"})

		if a.pending || a.prefix != "" {
			t.Fatalf("pending %v prefix %q after paste", a.pending, a.prefix)
		}

		press(a, keyRune('2'))

		if got := settled(t, a, "p2", st.proc(1)); got != "one2" {
			t.Errorf("pane got %q, want %q", got, "one2")
		}

		if len(tabIDs(a, 1)) != 1 {
			t.Error("the digit moved a pane")
		}
	})

	t.Run("popup delay after the first key", func(t *testing.T) {
		a, _ := splitApp(t)
		press(a, keyLeader)

		seqNo := a.seq
		press(a, keyRune('>'))
		a.Update(whichKeyMsg{seq: seqNo})

		if a.popup {
			t.Error("the popup reopened")
		}
	})

	t.Run("overlay ends it", func(t *testing.T) {
		a, _ := splitApp(t)
		press(a, keyLeader, keyRune('>'))
		a.openCommandPalette()

		if a.pending || a.prefix != "" {
			t.Errorf("pending %v prefix %q", a.pending, a.prefix)
		}
	})

	t.Run("a fresh leader starts clean", func(t *testing.T) {
		a, _ := splitApp(t)
		press(a, keyLeader, keyRune('>'), keyEsc, keyLeader)

		if !a.pending || a.prefix != "" {
			t.Errorf("pending %v prefix %q", a.pending, a.prefix)
		}
	})
}
