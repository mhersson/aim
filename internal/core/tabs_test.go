package core

import (
	"slices"
	"testing"
)

// tabsOf builds a state from per-tab layouts; tab i is "t<i+1>" and pane
// metadata is shell unless the ID is in agents.
func tabsOf(active int, agents []PaneID, layouts ...*Node) State {
	s := InitialState(DefaultConfig())
	s.Tabs = nil

	for i, l := range layouts {
		t := Tab{ID: TabID("t" + string(rune('1'+i)))}
		setLayout(&t, l)

		if l != nil {
			t.Focus = l.firstLeaf()
		}

		s.Tabs = append(s.Tabs, t)

		leaves(l, func(id PaneID) {
			k := KindShell
			if slices.Contains(agents, id) {
				k = KindAgent
			}

			s.Panes[id] = Pane{Kind: k}
			s.NextID++
		})
	}

	s.NextTabID = len(layouts)
	s.Active = active

	return s
}

func tabIDs(s State) []TabID {
	var ids []TabID
	for _, t := range s.Tabs {
		ids = append(ids, t.ID)
	}

	return ids
}

func TestNewTab(t *testing.T) {
	s := dispatch(t, stateOf(leaf("p1"), "p1"), NewTab{Kind: KindAgent, Cwd: "/w"})
	if !slices.Equal(tabIDs(s), []TabID{"t1", "t2"}) || s.Active != 1 {
		t.Fatalf("tabs = %v active %d", tabIDs(s), s.Active)
	}

	tab := s.Tab()
	if tab.ID != "t2" || tab.Name != "" || show(tab.Layout) != "p2" || tab.Focus != "p2" || tab.Zoomed {
		t.Errorf("tab = %+v", tab)
	}

	if p := s.Panes["p2"]; p.Kind != KindAgent || p.Cwd != "/w" {
		t.Errorf("pane = %+v", p)
	}

	if s.NextID != 2 || s.NextTabID != 2 {
		t.Errorf("NextID %d NextTabID %d", s.NextID, s.NextTabID)
	}

	st := NewStore(stateOf(leaf("p1"), "p1"))
	if err := st.Dispatch(NewTab{Kind: "bogus"}); err == nil {
		t.Error("unknown kind accepted")
	}
}

func TestTabIDsNeverReused(t *testing.T) {
	st := NewStore(stateOf(leaf("p1"), "p1"))
	for _, a := range []Action{NewTab{Kind: KindShell}, CloseTab{ID: "t2"}, NewTab{Kind: KindShell}} {
		if err := st.Dispatch(a); err != nil {
			t.Fatal(err)
		}
	}

	s := st.State()
	if !slices.Equal(tabIDs(s), []TabID{"t1", "t3"}) || s.Tab().Focus != "p3" {
		t.Errorf("tabs = %v focus %q", tabIDs(s), s.Tab().Focus)
	}
}

func TestNilTabsBehaveAsOneEmptyTab(t *testing.T) {
	s := State{Theme: "a"}
	if tab := s.Tab(); tab.ID != "t1" || !tab.Empty() {
		t.Errorf("Tab() = %+v", tab)
	}

	if _, ok := s.TabOf("p1"); ok || s.TabHasAgents(0) {
		t.Error("empty state reports panes")
	}

	got := dispatch(t, s, OpenPane{Kind: KindShell})
	if len(got.Tabs) != 1 || got.Tab().ID != "t1" || got.Tab().Focus != "p1" {
		t.Errorf("OpenPane on nil tabs: %+v", got)
	}

	got = dispatch(t, State{}, NewTab{Kind: KindShell})
	if !slices.Equal(tabIDs(got), []TabID{"t1"}) || got.Active != 0 || got.Tab().Focus != "p1" {
		t.Errorf("NewTab on nil tabs: %v active %d", tabIDs(got), got.Active)
	}
}

func TestNewTabFillsEmptyTab(t *testing.T) {
	empty := tabsOf(0, nil, nil)
	empty.Tabs[0].Name = "work"

	s := dispatch(t, empty, NewTab{Kind: KindAgent, Cwd: "/w"})

	tab := s.Tab()
	if !slices.Equal(tabIDs(s), []TabID{"t1"}) || s.Active != 0 {
		t.Fatalf("tabs = %v active %d", tabIDs(s), s.Active)
	}

	if tab.Name != "work" || show(tab.Layout) != "p1" || tab.Focus != "p1" || tab.Zoomed {
		t.Errorf("tab = %+v", tab)
	}

	if p := s.Panes["p1"]; p.Kind != KindAgent || p.Cwd != "/w" {
		t.Errorf("pane = %+v", p)
	}

	if s.NextTabID != 1 {
		t.Errorf("NextTabID = %d, want 1", s.NextTabID)
	}

	// The next new tab appends and IDs are not reused.
	s = dispatch(t, s, NewTab{Kind: KindShell})
	if !slices.Equal(tabIDs(s), []TabID{"t1", "t2"}) || s.Active != 1 {
		t.Errorf("tabs = %v active %d", tabIDs(s), s.Active)
	}
}

func TestCloseTab(t *testing.T) {
	three := func(active int) State {
		return tabsOf(active, nil, leaf("p1"), leaf("p2"), leaf("p3"))
	}

	tests := []struct {
		name       string
		from       State
		id         TabID
		wantTabs   []TabID
		wantActive TabID
		wantPanes  []PaneID
	}{
		{"active middle activates left", three(1), "t2", []TabID{"t1", "t3"}, "t1", []PaneID{"p1", "p3"}},
		{"active first activates new first", three(0), "t1", []TabID{"t2", "t3"}, "t2", []PaneID{"p2", "p3"}},
		{"active last activates left", three(2), "t3", []TabID{"t1", "t2"}, "t2", []PaneID{"p1", "p2"}},
		{"hidden left of active keeps active", three(2), "t1", []TabID{"t2", "t3"}, "t3", []PaneID{"p2", "p3"}},
		{"hidden right of active keeps active", three(0), "t3", []TabID{"t1", "t2"}, "t1", []PaneID{"p1", "p2"}},
		{"only tab stays empty", tabsOf(0, nil, vsplit(leaf("p1"), leaf("p2"))), "t1", []TabID{"t1"}, "t1", nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := dispatch(t, tt.from, CloseTab{ID: tt.id})
			if !slices.Equal(tabIDs(got), tt.wantTabs) || got.Tab().ID != tt.wantActive {
				t.Errorf("tabs = %v active %q, want %v %q", tabIDs(got), got.Tab().ID, tt.wantTabs, tt.wantActive)
			}

			var panes []PaneID
			for id := range got.Panes {
				panes = append(panes, id)
			}

			slices.Sort(panes)

			if !slices.Equal(panes, tt.wantPanes) {
				t.Errorf("panes = %v, want %v", panes, tt.wantPanes)
			}
		})
	}

	st := NewStore(three(0))
	if err := st.Dispatch(CloseTab{ID: "t9"}); err == nil {
		t.Error("unknown tab accepted")
	}
}

func TestClosePaneAcrossTabs(t *testing.T) {
	tests := []struct {
		name       string
		from       State
		id         PaneID
		wantTabs   []TabID
		wantActive TabID
		wantTree   string // of the active tab
		wantFocus  PaneID
	}{
		{"hidden tab's only pane removes the tab", tabsOf(1, nil, leaf("p1"), leaf("p2"), leaf("p3")), "p1", []TabID{"t2", "t3"}, "t2", "p2", "p2"},
		{"hidden tab right of active", tabsOf(0, nil, leaf("p1"), leaf("p2")), "p2", []TabID{"t1"}, "t1", "p1", "p1"},
		{"active tab's only pane removes it", tabsOf(1, nil, leaf("p1"), leaf("p2")), "p2", []TabID{"t1"}, "t1", "p1", "p1"},
		{"only tab's only pane leaves it empty", tabsOf(0, nil, leaf("p1")), "p1", []TabID{"t1"}, "t1", "-", ""},
		{"split pane in a hidden tab", tabsOf(0, nil, leaf("p1"), vsplit(leaf("p2"), leaf("p3"))), "p2", []TabID{"t1", "t2"}, "t1", "p1", "p1"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := dispatch(t, tt.from, ClosePane{ID: tt.id})
			if !slices.Equal(tabIDs(got), tt.wantTabs) || got.Tab().ID != tt.wantActive {
				t.Errorf("tabs = %v active %q", tabIDs(got), got.Tab().ID)
			}

			if show(got.Tab().Layout) != tt.wantTree || got.Tab().Focus != tt.wantFocus {
				t.Errorf("active tab = %s focus %q", show(got.Tab().Layout), got.Tab().Focus)
			}

			if _, ok := got.Panes[tt.id]; ok {
				t.Error("pane metadata kept")
			}
		})
	}
}

func TestClosePaneInHiddenTabKeepsOthers(t *testing.T) {
	from := tabsOf(0, nil, leaf("p1"), vsplit(leaf("p2"), leaf("p3")))
	from.Tabs[0].Zoomed = true
	from.Tabs[1].Focus, from.Tabs[1].Zoomed = "p3", true

	got := dispatch(t, from, ClosePane{ID: "p2"})
	if !got.Tabs[0].Zoomed || got.Tabs[0].Focus != "p1" {
		t.Errorf("active tab changed: %+v", got.Tabs[0])
	}

	if h := got.Tabs[1]; show(h.Layout) != "p3" || h.Focus != "p3" || h.Zoomed {
		t.Errorf("hidden tab = %+v", h)
	}

	from.Tabs[1].Focus = "p3"

	got = dispatch(t, from, ClosePane{ID: "p3"})
	if h := got.Tabs[1]; h.Focus != "p2" {
		t.Errorf("closing the focused pane: focus %q, want p2", h.Focus)
	}

	from.Tabs[1].Focus = "p3"

	got = dispatch(t, from, ClosePane{ID: "p2"})
	if got.Tabs[1].Focus != "p3" {
		t.Errorf("closing another pane moved focus to %q", got.Tabs[1].Focus)
	}
}

func TestRenameTab(t *testing.T) {
	s := dispatch(t, tabsOf(0, nil, leaf("p1"), leaf("p2")), RenameTab{ID: "t2", Name: "api"})
	if s.Tabs[1].Name != "api" || s.Tabs[0].Name != "" {
		t.Errorf("names = %q %q", s.Tabs[0].Name, s.Tabs[1].Name)
	}

	s = dispatch(t, s, RenameTab{ID: "t2"})
	if s.Tabs[1].Name != "" {
		t.Errorf("name = %q, want automatic", s.Tabs[1].Name)
	}

	st := NewStore(s)
	if err := st.Dispatch(RenameTab{ID: "t9", Name: "x"}); err == nil {
		t.Error("unknown tab accepted")
	}
}

func TestSelectAndCycleTab(t *testing.T) {
	from := func(active int) State { return tabsOf(active, nil, leaf("p1"), leaf("p2"), leaf("p3")) }

	tests := []struct {
		name   string
		active int
		action Action
		want   int
	}{
		{"select", 0, SelectTab{Index: 2}, 2},
		{"select out of range", 1, SelectTab{Index: 3}, 1},
		{"cycle next", 0, CycleTab{Delta: 1}, 1},
		{"cycle next wraps", 2, CycleTab{Delta: 1}, 0},
		{"cycle previous", 2, CycleTab{Delta: -1}, 1},
		{"cycle previous wraps", 0, CycleTab{Delta: -1}, 2},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := dispatch(t, from(tt.active), tt.action); got.Active != tt.want {
				t.Errorf("Active = %d, want %d", got.Active, tt.want)
			}
		})
	}
}

func TestTabSwitchKeepsPerTabState(t *testing.T) {
	s := tabsOf(0, nil, vsplit(leaf("p1"), leaf("p2")), leaf("p3"))
	s.Tabs[0].Focus, s.Tabs[0].Zoomed = "p2", true
	s = dispatch(t, s, SelectTab{Index: 1})

	s = dispatch(t, s, SelectTab{Index: 0})
	if tab := s.Tab(); tab.Focus != "p2" || !tab.Zoomed {
		t.Errorf("tab = %+v", tab)
	}
}

func TestLayoutActionsActOnTheActiveTab(t *testing.T) {
	s := tabsOf(1, nil, vsplit(leaf("p1"), leaf("p2")), leaf("p3"))

	s = dispatch(t, s, OpenPane{Kind: KindShell, Split: SplitHorizontal})
	if show(s.Tabs[1].Layout) != "h(p3,p4)" || show(s.Tabs[0].Layout) != "v(p1,p2)" {
		t.Errorf("trees = %s, %s", show(s.Tabs[0].Layout), show(s.Tabs[1].Layout))
	}

	s = dispatch(t, s, ToggleZoom{})
	if !s.Tabs[1].Zoomed || s.Tabs[0].Zoomed {
		t.Error("zoom not per tab")
	}
}

func TestTabOfAndHasAgents(t *testing.T) {
	s := tabsOf(0, []PaneID{"p3"}, leaf("p1"), vsplit(leaf("p2"), leaf("p3")))
	if i, ok := s.TabOf("p3"); !ok || i != 1 {
		t.Errorf("TabOf(p3) = %d, %v", i, ok)
	}

	if _, ok := s.TabOf("p9"); ok {
		t.Error("TabOf found a missing pane")
	}

	tests := []struct {
		i    int
		want bool
	}{{0, false}, {1, true}, {2, false}}
	for _, tt := range tests {
		if got := s.TabHasAgents(tt.i); got != tt.want {
			t.Errorf("TabHasAgents(%d) = %v, want %v", tt.i, got, tt.want)
		}
	}
}

func TestTabActionsDeepCopy(t *testing.T) {
	st := NewStore(tabsOf(0, nil, vsplit(leaf("p1"), leaf("p2")), leaf("p3")))

	before := st.State()
	for _, a := range []Action{NewTab{Kind: KindShell}, CloseTab{ID: "t1"}, RenameTab{ID: "t2", Name: "n"}, ClosePane{ID: "p3"}} {
		if err := st.Dispatch(a); err != nil {
			t.Fatal(err)
		}
	}

	if len(before.Tabs) != 2 || before.Tabs[0].Layout.Pane != "" || len(before.Panes) != 3 || before.Tabs[1].Name != "" {
		t.Errorf("earlier snapshot changed: %+v", before)
	}
}

func TestOnlyTabNameWhenEmptied(t *testing.T) {
	named := func() State {
		s := tabsOf(0, nil, leaf("p1"))
		s.Tabs[0].Name = "api"

		return s
	}

	tests := []struct {
		name   string
		action Action
		want   string
	}{
		{"closing its last pane keeps the name", ClosePane{ID: "p1"}, "api"},
		{"closing the tab clears the name", CloseTab{ID: "t1"}, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := dispatch(t, named(), tt.action)
			if len(got.Tabs) != 1 || !got.Tab().Empty() || got.Tab().Name != tt.want {
				t.Errorf("tab = %+v, want empty with name %q", got.Tab(), tt.want)
			}
		})
	}
}
