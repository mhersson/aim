package core

import (
	"errors"
	"slices"
	"testing"
)

// buildTabs makes one tab per entry of modes, tab i holding counts[i] shell
// panes in that mode (p1.. in creation order), tab 0 active.
func buildTabs(t *testing.T, modes []LayoutMode, counts []int) State {
	t.Helper()

	cfg := DefaultConfig()
	cfg.Layout.DefaultMode = string(modes[0])

	st := NewStore(InitialState(cfg))
	for i, m := range modes {
		s := st.State()
		s.Layout.DefaultMode = string(m)

		st = NewStore(s)
		if i > 0 {
			if err := st.Dispatch(NewTab{Kind: KindShell}); err != nil {
				t.Fatal(err)
			}
		}
		// Tab 0 starts empty, so its first pane comes from OpenPane too; other
		// tabs get theirs from NewTab.
		opens := counts[i] - 1
		if i == 0 {
			opens = counts[i]
		}

		for range opens {
			if err := st.Dispatch(OpenPane{Kind: KindShell, Split: SplitVertical, Area: modeInner}); err != nil {
				t.Fatal(err)
			}
		}
	}

	s := st.State()
	s.Layout.DefaultMode = string(modes[0])
	s.Active = 0

	return s
}

func moveErr(s State, a Action) error { return NewStore(s).Dispatch(a) }

func TestMovePaneLandsPerTargetMode(t *testing.T) {
	for _, mode := range allModes {
		t.Run(string(mode), func(t *testing.T) {
			s := buildTabs(t, []LayoutMode{ModeBSP, mode}, []int{2, 2})
			// tab 0: p1,p2; tab 1: p3,p4 (focus p4)
			s.Panes["p2"] = Pane{Kind: KindAgent, Cwd: "/w", Harness: "claude-code", Session: "s1", Transcript: "/t.jsonl", State: AgentBlocked}
			want := s.Panes["p2"]
			s.Tabs[1].Zoomed = true
			s.Tabs[0].Focus = "p2"
			s.Tabs[0].Zoomed = true

			got := dispatch(t, s, MovePane{ID: "p2", Tab: 1, Area: modeInner})
			if !samePane(got.Panes["p2"], want) {
				t.Errorf("pane = %+v, want %+v", got.Panes["p2"], want)
			}

			if len(got.Panes) != 4 {
				t.Errorf("panes = %d", len(got.Panes))
			}

			src, dst := got.Tabs[0], got.Tabs[1]
			if !slices.Equal(src.Panes, []PaneID{"p1"}) || src.Focus != "p1" || src.Zoomed {
				t.Errorf("source = %+v", src)
			}

			if len(dst.Panes) != 3 || !slices.Contains(dst.Panes, "p2") || dst.Focus != "p2" || dst.Zoomed {
				t.Errorf("target = %+v", dst)
			}

			switch mode {
			case ModeMaster:
				if !slices.Equal(dst.Panes, []PaneID{"p3", "p4", "p2"}) {
					t.Errorf("master order = %v", dst.Panes)
				}
			case ModeScrolling:
				if !slices.Equal(dst.Panes, []PaneID{"p3", "p4", "p2"}) || len(dst.Columns) != 3 {
					t.Errorf("columns = %v", columnsOf(dst))
				}
			default:
				if show(dst.Layout) != "v(p3,h(p4,p2))" {
					t.Errorf("layout = %s", show(dst.Layout))
				}
			}

			if got.Active != 0 {
				t.Errorf("Active = %d", got.Active)
			}
		})
	}
}

func TestMovePaneBSPPreselect(t *testing.T) {
	s := buildTabs(t, []LayoutMode{ModeBSP, ModeBSP}, []int{2, 2})
	s.Tabs[1].Preselect = Left

	got := dispatch(t, s, MovePane{ID: "p1", Tab: 1, Area: modeInner})
	if show(got.Tabs[1].Layout) != "v(p3,v(p1,p4))" || got.Tabs[1].Preselect != NoDir {
		t.Errorf("layout = %s preselect %v", show(got.Tabs[1].Layout), got.Tabs[1].Preselect)
	}

	if got.Tabs[0].Preselect != NoDir {
		t.Error("source preselect set")
	}
}

func TestMovePaneSourceFocusPerMode(t *testing.T) {
	// tab 0 holds p1,p2,p3; the focused p2 moves out.
	want := map[LayoutMode]PaneID{
		ModeBSP:       "p3", // the sibling's first leaf: v(p1,v(p2,p3))
		ModeMaster:    "p3", // the pane that took its place in the order
		ModeScrolling: "p1", // its column is gone, so the previous column
	}

	for _, mode := range allModes {
		t.Run(string(mode), func(t *testing.T) {
			s := buildTabs(t, []LayoutMode{mode, ModeBSP}, []int{3, 1})
			s.Tabs[0].Focus = "p2"

			got := dispatch(t, s, MovePane{ID: "p2", Tab: 1, Area: modeInner})
			if f := got.Tabs[0].Focus; f != want[mode] {
				t.Errorf("focus = %q, want %q", f, want[mode])
			}
		})
	}
}

func TestMovePaneUnfocusedFromZoomedSource(t *testing.T) {
	s := buildTabs(t, []LayoutMode{ModeBSP, ModeBSP}, []int{3, 1})
	s.Tabs[0].Focus = "p3"
	s.Tabs[0].Zoomed = true

	got := dispatch(t, s, MovePane{ID: "p1", Tab: 1, Area: modeInner})
	if src := got.Tabs[0]; !src.Zoomed || src.Focus != "p3" || slices.Contains(src.Panes, "p1") {
		t.Errorf("source = %+v", src)
	}
}

func TestMovePaneFromLoneBackgroundTab(t *testing.T) {
	// tabs t1(p1,p2) t2(p3) t3(p4); t3 is active, lone background t2 gives p3 to t1.
	s := buildTabs(t, []LayoutMode{ModeBSP, ModeBSP, ModeBSP}, []int{2, 1, 1})
	s.Active = 2

	got := dispatch(t, s, MovePane{ID: "p3", Tab: 0, Area: modeInner})
	if !slices.Equal(tabIDs(got), []TabID{"t1", "t3"}) || got.Active != 1 {
		t.Errorf("tabs %v active %d", tabIDs(got), got.Active)
	}
	// Active left of the removed tab keeps its index.
	s.Active = 0

	got = dispatch(t, s, MovePane{ID: "p3", Tab: 2, Area: modeInner})
	if !slices.Equal(tabIDs(got), []TabID{"t1", "t3"}) || got.Active != 0 {
		t.Errorf("tabs %v active %d", tabIDs(got), got.Active)
	}
}

func TestMovePaneEmptiedSource(t *testing.T) {
	cases := []struct {
		name       string
		tabs       int // tabs, each with one pane except as noted; pane of tab i is p<i+1>
		active     int
		src        int
		dst        int
		follow     bool
		wantActive int
		wantIDs    []TabID
	}{
		{"follow right", 3, 0, 0, 2, true, 1, []TabID{"t2", "t3"}},
		{"follow left", 3, 2, 2, 0, true, 0, []TabID{"t1", "t2"}},
		{"stay, source active, target right", 3, 0, 0, 2, false, 1, []TabID{"t2", "t3"}},
		{"stay, source active, target left", 3, 2, 2, 0, false, 0, []TabID{"t1", "t2"}},
		{"stay, target left of source, source active", 3, 1, 1, 0, false, 0, []TabID{"t1", "t3"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			modes := []LayoutMode{ModeBSP, ModeBSP, ModeBSP}
			s := buildTabs(t, modes[:c.tabs], []int{1, 1, 1}[:c.tabs])
			s.Active = c.active
			id := PaneID("p" + string(rune('1'+c.src)))

			got := dispatch(t, s, MovePane{ID: id, Tab: c.dst, Follow: c.follow, Area: modeInner})
			if !slices.Equal(tabIDs(got), c.wantIDs) || got.Active != c.wantActive {
				t.Fatalf("tabs %v active %d", tabIDs(got), got.Active)
			}

			if _, ok := got.Panes[id]; !ok || len(got.Panes) != 3 {
				t.Errorf("panes = %v", got.Panes)
			}

			if got.Tab().Focus != id {
				t.Errorf("active focus = %q", got.Tab().Focus)
			}
		})
	}
}

func TestMovePaneActiveFollow(t *testing.T) {
	s := buildTabs(t, []LayoutMode{ModeBSP, ModeBSP, ModeBSP}, []int{2, 1, 1})

	got := dispatch(t, s, MovePane{ID: "p2", Tab: 2, Follow: true, Area: modeInner})
	if got.Active != 2 || len(got.Tabs) != 3 {
		t.Errorf("active %d tabs %d", got.Active, len(got.Tabs))
	}

	got = dispatch(t, s, MovePane{ID: "p2", Tab: 2, Area: modeInner})
	if got.Active != 0 {
		t.Errorf("active = %d", got.Active)
	}
}

func TestMovePaneNewTab(t *testing.T) {
	for _, def := range allModes {
		t.Run(string(def), func(t *testing.T) {
			s := buildTabs(t, []LayoutMode{ModeBSP, ModeBSP}, []int{3, 1})
			s.Layout.DefaultMode = string(def)
			s.Tabs[0].Focus = "p2"
			s.Tabs[0].Zoomed = true
			next := s.NextTabID

			got := dispatch(t, s, MovePane{ID: "p2", Tab: 2, Area: modeInner})
			if len(got.Tabs) != 3 || got.Active != 0 {
				t.Fatalf("tabs %d active %d", len(got.Tabs), got.Active)
			}

			nt := got.Tabs[2]
			if nt.ID != TabID("t"+string(rune('0'+next+1))) || got.NextTabID != next+1 {
				t.Errorf("tab id %q next %d", nt.ID, got.NextTabID)
			}

			if nt.Mode != def || !slices.Equal(nt.Panes, []PaneID{"p2"}) || nt.Focus != "p2" || nt.Zoomed || nt.Name != "" {
				t.Errorf("new tab = %+v", nt)
			}

			if got.Tabs[0].Zoomed {
				t.Error("source still zoomed")
			}
		})
	}
}

func TestMovePaneNineTabs(t *testing.T) {
	modes := make([]LayoutMode, 9)

	counts := make([]int, 9)
	for i := range modes {
		modes[i], counts[i] = ModeBSP, 1
	}

	counts[0] = 2
	s := buildTabs(t, modes, counts)

	got := dispatch(t, s, MovePane{ID: "p2", Tab: 8, Area: modeInner})
	if len(got.Tabs[8].Panes) != 2 || len(got.Tabs) != 9 {
		t.Errorf("tabs = %d, last = %v", len(got.Tabs), got.Tabs[8].Panes)
	}

	if err := moveErr(s, MovePane{ID: "p2", Tab: 9}); !errors.Is(err, ErrNoTab) {
		t.Errorf("Tab 9 error = %v", err)
	}
}

func TestMovePaneZoomedHiddenTarget(t *testing.T) {
	s := buildTabs(t, []LayoutMode{ModeBSP, ModeMaster, ModeBSP}, []int{2, 2, 1})
	s.Tabs[1].Zoomed = true

	got := dispatch(t, s, MovePane{ID: "p1", Tab: 1, Area: modeInner})
	if got.Tabs[1].Zoomed || got.Tabs[1].Focus != "p1" || got.Active != 0 {
		t.Errorf("target = %+v active %d", got.Tabs[1], got.Active)
	}
}

func TestMovePaneKeepsMetadata(t *testing.T) {
	s := buildTabs(t, []LayoutMode{ModeBSP, ModeBSP}, []int{2, 1})
	p := Pane{Kind: KindAgent, Cwd: "/x", Harness: "claude-code", Session: "sess", Transcript: "/tr", State: AgentWorking}
	s.Panes["p2"] = p

	got := dispatch(t, s, BreakPane{ID: "p2", Area: modeInner})
	if !samePane(got.Panes["p2"], p) {
		t.Errorf("after break pane = %+v", got.Panes["p2"])
	}
}

func TestMovePaneNoMaxPanesCheck(t *testing.T) {
	s := buildTabs(t, []LayoutMode{ModeBSP, ModeBSP}, []int{2, 1})
	for i := len(s.Panes); i < MaxPanes; i++ {
		s.Panes[PaneID("x"+string(rune('a'+i%26))+string(rune('a'+i/26)))] = Pane{}
	}

	if len(s.Panes) != MaxPanes {
		t.Fatalf("panes = %d", len(s.Panes))
	}

	dispatch(t, s, MovePane{ID: "p1", Tab: 1, Area: modeInner})
	dispatch(t, s, MovePane{ID: "p1", Tab: 2, Area: modeInner})
}

func TestMovePaneErrors(t *testing.T) {
	s := buildTabs(t, []LayoutMode{ModeBSP, ModeBSP}, []int{2, 1})

	cases := []struct {
		name string
		a    Action
		want error
	}{
		{"unknown pane", MovePane{ID: "p99", Tab: 1}, nil},
		{"negative tab", MovePane{ID: "p1", Tab: -1}, ErrNoTab},
		{"beyond count", MovePane{ID: "p1", Tab: 3}, ErrNoTab},
		{"same tab", MovePane{ID: "p1", Tab: 0}, ErrSameTab},
		{"same tab, lone", MovePane{ID: "p3", Tab: 1}, ErrSameTab},
		{"lone to new tab", MovePane{ID: "p3", Tab: 2}, ErrAlone},
		{"break lone", BreakPane{ID: "p3"}, ErrAlone},
		{"break unknown", BreakPane{ID: "p99"}, nil},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			st := NewStore(s)

			err := st.Dispatch(c.a)
			if err == nil {
				t.Fatal("no error")
			}

			if c.want != nil && !errors.Is(err, c.want) {
				t.Errorf("error = %v, want %v", err, c.want)
			}

			if c.want == nil && (errors.Is(err, ErrNoTab) || errors.Is(err, ErrSameTab) || errors.Is(err, ErrAlone)) {
				t.Errorf("unexpected sentinel %v", err)
			}

			if got := st.State(); !slices.Equal(tabIDs(got), tabIDs(s)) || got.Active != s.Active {
				t.Error("state changed")
			}
		})
	}
}

func TestBreakPane(t *testing.T) {
	for _, mode := range allModes {
		t.Run(string(mode), func(t *testing.T) {
			s := buildTabs(t, []LayoutMode{mode, mode}, []int{3, 1})
			s.Tabs[0].Focus = "p2"

			got := dispatch(t, s, BreakPane{ID: "p2", Area: modeInner})
			if len(got.Tabs) != 3 || got.Active != 2 {
				t.Fatalf("tabs %d active %d", len(got.Tabs), got.Active)
			}

			if nt := got.Tabs[2]; !slices.Equal(nt.Panes, []PaneID{"p2"}) || nt.Focus != "p2" || nt.Mode != mode {
				t.Errorf("new tab = %+v", nt)
			}

			if slices.Contains(got.Tabs[0].Panes, "p2") || len(got.Tabs[0].Panes) != 2 {
				t.Errorf("source = %v", got.Tabs[0].Panes)
			}
		})
	}
}

func TestBreakPaneWithManyTabs(t *testing.T) {
	n := 10
	modes := make([]LayoutMode, n)

	counts := make([]int, n)
	for i := range modes {
		modes[i], counts[i] = ModeBSP, 1
	}

	counts[0] = 2
	s := buildTabs(t, modes, counts)

	got := dispatch(t, s, BreakPane{ID: "p2", Area: modeInner})
	if len(got.Tabs) != n+1 || got.Active != n || !slices.Equal(got.Tabs[n].Panes, []PaneID{"p2"}) {
		t.Errorf("%d tabs: tabs %d active %d", n, len(got.Tabs), got.Active)
	}

	if err := moveErr(s, MovePane{ID: "p2", Tab: n}); !errors.Is(err, ErrNoTab) {
		t.Errorf("%d tabs: explicit new tab error = %v", n, err)
	}
}
