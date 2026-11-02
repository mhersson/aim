package core

import (
	"errors"
	"maps"
	"testing"
)

func leaf(id PaneID) *Node { return &Node{Pane: id} }

func vsplit(a, b *Node) *Node {
	return &Node{Split: SplitVertical, Ratio: 0.5, A: a, B: b}
}

func hsplit(a, b *Node) *Node {
	return &Node{Split: SplitHorizontal, Ratio: 0.5, A: a, B: b}
}

// show renders a tree compactly: p1, v(p1,p2), h(p1,v(p2,p3)).
func show(n *Node) string {
	switch {
	case n == nil:
		return "-"
	case n.leaf():
		return string(n.Pane)
	case n.Split == SplitVertical:
		return "v(" + show(n.A) + "," + show(n.B) + ")"
	}

	return "h(" + show(n.A) + "," + show(n.B) + ")"
}

func leaves(n *Node, f func(PaneID)) {
	if n == nil {
		return
	}

	if n.leaf() {
		f(n.Pane)

		return
	}

	leaves(n.A, f)
	leaves(n.B, f)
}

// setLayout installs a BSP tree on a tab the way core does: Panes in leaf order.
func setLayout(t *Tab, n *Node) {
	if n == nil {
		t.Layout, t.Panes = nil, nil

		return
	}

	t.setTree(n)
}

func stateOf(layout *Node, focus PaneID) State {
	s := InitialState(DefaultConfig())
	setLayout(&s.Tabs[0], layout)
	s.Tabs[0].Focus = focus

	leaves(layout, func(id PaneID) {
		s.Panes[id] = Pane{Kind: KindShell}
		s.NextID++
	})

	return s
}

func dispatch(t *testing.T, s State, a Action) State {
	t.Helper()

	st := NewStore(s)
	if err := st.Dispatch(a); err != nil {
		t.Fatalf("Dispatch(%T) error = %v", a, err)
	}

	return st.State()
}

func TestInitialState(t *testing.T) {
	s := InitialState(Config{Theme: "catppuccin-latte"})
	if s.Theme != "catppuccin-latte" {
		t.Errorf("Theme = %q", s.Theme)
	}

	if tab := s.Tab(); len(s.Tabs) != 1 || s.Active != 0 || tab.ID != "t1" || !tab.Empty() || tab.Layout != nil || tab.Focus != "" || len(s.Panes) != 0 || tab.Zoomed {
		t.Errorf("state not empty: %+v", s)
	}
}

func TestOpenPane(t *testing.T) {
	tests := []struct {
		name      string
		from      State
		action    OpenPane
		wantTree  string
		wantFocus PaneID
	}{
		{"empty layout", stateOf(nil, ""), OpenPane{Kind: KindShell, Cwd: "/w"}, "p1", "p1"},
		{"side by side", stateOf(leaf("p1"), "p1"), OpenPane{Kind: KindShell, Split: SplitVertical}, "v(p1,p2)", "p2"},
		{"stacked", stateOf(leaf("p1"), "p1"), OpenPane{Kind: KindAgent, Split: SplitHorizontal}, "h(p1,p2)", "p2"},
		{"splits the focused pane", stateOf(vsplit(leaf("p1"), leaf("p2")), "p2"), OpenPane{Kind: KindShell, Split: SplitHorizontal}, "v(p1,h(p2,p3))", "p3"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tt.from.Tabs[0].Zoomed = tt.from.Tabs[0].Layout != nil

			got := dispatch(t, tt.from, tt.action)
			if show(got.Tab().Layout) != tt.wantTree {
				t.Errorf("tree = %s, want %s", show(got.Tab().Layout), tt.wantTree)
			}

			if got.Tab().Focus != tt.wantFocus {
				t.Errorf("Focus = %q, want %q", got.Tab().Focus, tt.wantFocus)
			}

			if got.Tab().Zoomed {
				t.Error("zoom not cleared")
			}

			if p := got.Panes[tt.wantFocus]; p.Kind != tt.action.Kind || p.Cwd != tt.action.Cwd {
				t.Errorf("pane = %+v", p)
			}

			if got.NextID != tt.from.NextID+1 {
				t.Errorf("NextID = %d, want %d", got.NextID, tt.from.NextID+1)
			}

			if n := got.Tab().Layout.path(tt.wantFocus); len(n) > 1 {
				if r := n[len(n)-2].Ratio; r != 0.5 {
					t.Errorf("ratio = %v, want 0.5", r)
				}
			}
		})
	}
}

func TestOpenPaneHarness(t *testing.T) {
	tests := []struct {
		name   string
		action OpenPane
		want   string
	}{
		{"agent keeps harness", OpenPane{Kind: KindAgent, Harness: "claude-code"}, "claude-code"},
		{"shell drops harness", OpenPane{Kind: KindShell, Harness: "claude-code"}, ""},
		{"agent without harness", OpenPane{Kind: KindAgent}, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := dispatch(t, stateOf(nil, ""), tt.action)
			if h := got.Panes["p1"].Harness; h != tt.want {
				t.Errorf("Harness = %q, want %q", h, tt.want)
			}
		})
	}
}

func TestOpenPaneErrors(t *testing.T) {
	tests := []struct {
		name   string
		from   State
		action OpenPane
	}{
		{"missing split", stateOf(leaf("p1"), "p1"), OpenPane{Kind: KindShell}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if err := NewStore(tt.from).Dispatch(tt.action); err == nil {
				t.Error("want error")
			}
		})
	}
}

func TestNewPaneIDNeverReused(t *testing.T) {
	s := dispatch(t, stateOf(nil, ""), OpenPane{Kind: KindShell})

	s = dispatch(t, s, ClosePane{ID: "p1"})
	if got := s.NewPaneID(); got != "p2" {
		t.Errorf("NewPaneID() = %q, want p2", got)
	}
}

func TestClosePane(t *testing.T) {
	tree := func() *Node { return vsplit(leaf("p1"), hsplit(leaf("p2"), leaf("p3"))) }

	tests := []struct {
		name      string
		from      State
		id        PaneID
		wantTree  string
		wantFocus PaneID
	}{
		{"focused pane focuses the sibling", stateOf(tree(), "p2"), "p2", "v(p1,p3)", "p3"},
		{"sibling subtree focuses its first leaf", stateOf(tree(), "p1"), "p1", "h(p2,p3)", "p2"},
		{"unfocused pane keeps focus", stateOf(tree(), "p1"), "p3", "v(p1,p2)", "p1"},
		{"unfocused pane keeps focus in the sibling", stateOf(tree(), "p3"), "p1", "h(p2,p3)", "p3"},
		{"last pane", stateOf(leaf("p1"), "p1"), "p1", "-", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tt.from.Tabs[0].Zoomed = true

			got := dispatch(t, tt.from, ClosePane{ID: tt.id})
			if show(got.Tab().Layout) != tt.wantTree {
				t.Errorf("tree = %s, want %s", show(got.Tab().Layout), tt.wantTree)
			}

			if got.Tab().Focus != tt.wantFocus {
				t.Errorf("Focus = %q, want %q", got.Tab().Focus, tt.wantFocus)
			}

			if _, ok := got.Panes[tt.id]; ok {
				t.Error("pane metadata not removed")
			}

			if got.Tab().Zoomed {
				t.Error("zoom not cleared")
			}
		})
	}
}

func TestDispatchKeepsStateOnError(t *testing.T) {
	st := NewStore(stateOf(vsplit(leaf("p1"), leaf("p2")), "p1"))
	for _, a := range []Action{
		ClosePane{ID: "p9"}, FocusPane{ID: "p9"}, SetPaneCwd{ID: "p9"}, OpenPane{Kind: "x"},
	} {
		if err := st.Dispatch(a); err == nil {
			t.Errorf("Dispatch(%T) want error", a)
		}
	}

	got := st.State()
	if show(got.Tab().Layout) != "v(p1,p2)" || got.Tab().Focus != "p1" || len(got.Panes) != 2 || got.NextID != 2 {
		t.Errorf("state changed: %s %+v", show(got.Tab().Layout), got)
	}
}

func TestStateIsCopied(t *testing.T) {
	st := NewStore(stateOf(vsplit(leaf("p1"), leaf("p2")), "p1"))
	got := st.State()
	got.Panes["p1"] = Pane{Kind: KindAgent, Cwd: "/x"}
	delete(got.Panes, "p2")
	got.Tab().Layout.Ratio = 0.9
	got.Tab().Layout.A.Pane = "zz"

	again := st.State()
	if len(again.Panes) != 2 || again.Panes["p1"].Kind != KindShell {
		t.Errorf("Panes leaked: %+v", again.Panes)
	}

	if again.Tab().Layout.Ratio != 0.5 || again.Tab().Layout.A.Pane != "p1" {
		t.Errorf("Layout leaked: %s", show(again.Tab().Layout))
	}
}

func TestFocusDir(t *testing.T) {
	tree := func() *Node { return vsplit(leaf("p1"), hsplit(leaf("p2"), leaf("p3"))) }

	tests := []struct {
		name  string
		focus PaneID
		dir   Dir
		want  PaneID
	}{
		{"right takes the top-most tie", "p1", Right, "p2"},
		{"left", "p3", Left, "p1"},
		{"up", "p3", Up, "p2"},
		{"right from the edge", "p3", Right, "p3"},
		{"down", "p2", Down, "p3"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := dispatch(t, stateOf(tree(), tt.focus), FocusDir{Dir: tt.dir})
			if got.Tab().Focus != tt.want {
				t.Errorf("Focus = %q, want %q", got.Tab().Focus, tt.want)
			}
		})
	}
}

func TestFocusDirPrefersOverlap(t *testing.T) {
	// p1 is wide on top; below sit p2 (narrow, left) and p3 (wide, right).
	tree := hsplit(leaf("p1"), &Node{Split: SplitVertical, Ratio: 0.2, A: leaf("p2"), B: leaf("p3")})
	tree.Ratio = 0.5

	s := stateOf(tree, "p1")
	if got := dispatch(t, s, FocusDir{Dir: Down}); got.Tab().Focus != "p3" {
		t.Errorf("Focus = %q, want p3 (most overlap)", got.Tab().Focus)
	}
}

func TestResizeDir(t *testing.T) {
	tests := []struct {
		name      string
		tree      *Node
		focus     PaneID
		dirs      []Dir
		wantRatio float64
	}{
		{"right grows the first child", vsplit(leaf("p1"), leaf("p2")), "p1", []Dir{Right}, 0.55},
		{"right from the second child", vsplit(leaf("p1"), leaf("p2")), "p2", []Dir{Right}, 0.55},
		{"left clamps", vsplit(leaf("p1"), leaf("p2")), "p1", []Dir{Left, Left, Left, Left, Left, Left, Left, Left, Left}, 0.1},
		{"right clamps", vsplit(leaf("p1"), leaf("p2")), "p1", []Dir{Right, Right, Right, Right, Right, Right, Right, Right, Right}, 0.9},
		{"down without a horizontal ancestor", vsplit(leaf("p1"), leaf("p2")), "p1", []Dir{Down}, 0.5},
		{"down on a stack", hsplit(leaf("p1"), leaf("p2")), "p1", []Dir{Down, Down}, 0.6},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := stateOf(tt.tree, tt.focus)
			for _, d := range tt.dirs {
				s = dispatch(t, s, ResizeDir{Dir: d})
			}

			if s.Tab().Layout.Ratio != tt.wantRatio {
				t.Errorf("Ratio = %v, want %v", s.Tab().Layout.Ratio, tt.wantRatio)
			}
		})
	}
}

func TestResizeDirUsesNearestAncestor(t *testing.T) {
	tree := vsplit(leaf("p1"), vsplit(leaf("p2"), leaf("p3")))

	s := dispatch(t, stateOf(tree, "p3"), ResizeDir{Dir: Right})
	if s.Tab().Layout.Ratio != 0.5 || s.Tab().Layout.B.Ratio != 0.55 {
		t.Errorf("ratios = %v, %v", s.Tab().Layout.Ratio, s.Tab().Layout.B.Ratio)
	}
}

func TestToggleZoom(t *testing.T) {
	s := stateOf(vsplit(leaf("p1"), leaf("p2")), "p1")

	s = dispatch(t, s, ToggleZoom{})
	if !s.Tab().Zoomed {
		t.Fatal("not zoomed")
	}

	if s = dispatch(t, s, ToggleZoom{}); s.Tab().Zoomed {
		t.Error("still zoomed")
	}

	if s = dispatch(t, stateOf(nil, ""), ToggleZoom{}); s.Tab().Zoomed {
		t.Error("zoomed with no pane")
	}
}

func TestZoomClearedBy(t *testing.T) {
	zoomed := func(focus PaneID) State {
		s := stateOf(vsplit(leaf("p1"), hsplit(leaf("p2"), leaf("p3"))), focus)
		s.Tabs[0].Zoomed = true

		return s
	}

	tests := []struct {
		name   string
		from   State
		action Action
		want   bool
	}{
		{"open", zoomed("p1"), OpenPane{Kind: KindShell, Split: SplitVertical}, false},
		{"close", zoomed("p1"), ClosePane{ID: "p3"}, false},
		{"focus pane", zoomed("p1"), FocusPane{ID: "p2"}, false},
		{"focus pane on the same pane", zoomed("p1"), FocusPane{ID: "p1"}, true},
		{"focus dir that moves", zoomed("p1"), FocusDir{Dir: Right}, false},
		{"focus dir at an edge", zoomed("p1"), FocusDir{Dir: Left}, true},
		{"resize", zoomed("p1"), ResizeDir{Dir: Right}, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := dispatch(t, tt.from, tt.action); got.Tab().Zoomed != tt.want {
				t.Errorf("Zoomed = %v, want %v", got.Tab().Zoomed, tt.want)
			}
		})
	}
}

func TestSetPaneCwd(t *testing.T) {
	s := dispatch(t, stateOf(leaf("p1"), "p1"), SetPaneCwd{ID: "p1", Cwd: "/tmp"})
	if s.Panes["p1"].Cwd != "/tmp" || s.Panes["p1"].Kind != KindShell {
		t.Errorf("pane = %+v", s.Panes["p1"])
	}
}

func TestSwapDir(t *testing.T) {
	ratioed := func() *Node {
		return &Node{Split: SplitVertical, Ratio: 0.7, A: leaf("p1"), B: leaf("p2")}
	}
	tree := func() *Node { return vsplit(leaf("p1"), hsplit(leaf("p2"), leaf("p3"))) }

	tests := []struct {
		name      string
		layout    *Node
		focus     PaneID
		dir       Dir
		want      string
		wantRatio float64
		zoomed    bool
		wantZoom  bool
	}{
		{"keeps the ratio of the position", ratioed(), "p2", Left, "v(p2,p1)", 0.7, false, false},
		{"up", tree(), "p3", Up, "v(p1,h(p3,p2))", 0.5, false, false},
		{"no neighbour keeps the tree and zoom", tree(), "p1", Left, "v(p1,h(p2,p3))", 0.5, true, true},
		{"right takes the top-most tie and clears zoom", tree(), "p1", Right, "v(p2,h(p1,p3))", 0.5, true, false},
		{"single pane", leaf("p1"), "p1", Right, "p1", 0, true, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := stateOf(tt.layout, tt.focus)
			s.Tabs[0].Zoomed = tt.zoomed
			before := maps.Clone(s.Panes)

			got := dispatch(t, s, SwapDir{Dir: tt.dir})
			if g := show(got.Tab().Layout); g != tt.want {
				t.Errorf("layout = %s, want %s", g, tt.want)
			}

			if got.Tab().Focus != tt.focus {
				t.Errorf("Focus = %q, want %q", got.Tab().Focus, tt.focus)
			}

			if got.Tab().Zoomed != tt.wantZoom {
				t.Errorf("Zoomed = %v, want %v", got.Tab().Zoomed, tt.wantZoom)
			}

			if tt.wantRatio != 0 && got.Tab().Layout.Ratio != tt.wantRatio {
				t.Errorf("root ratio = %v, want %v", got.Tab().Layout.Ratio, tt.wantRatio)
			}

			if !maps.EqualFunc(got.Panes, before, samePane) {
				t.Errorf("Panes = %v, want %v", got.Panes, before)
			}
		})
	}
}

func TestPaneLimit(t *testing.T) {
	st := NewStore(InitialState(Config{}))
	for len(st.State().Panes) < MaxPanes-1 {
		if err := st.Dispatch(OpenPane{Kind: KindShell, Split: SplitVertical}); err != nil {
			t.Fatalf("OpenPane at %d panes: %v", len(st.State().Panes), err)
		}
	}

	if err := st.Dispatch(NewTab{Kind: KindShell}); err != nil {
		t.Fatalf("NewTab at %d panes: %v", MaxPanes-1, err)
	}

	if got := len(st.State().Panes); got != MaxPanes {
		t.Fatalf("panes = %d, want %d", got, MaxPanes)
	}

	for _, a := range []Action{OpenPane{Kind: KindShell, Split: SplitVertical}, NewTab{Kind: KindShell}} {
		if err := st.Dispatch(a); !errors.Is(err, ErrTooManyPanes) {
			t.Errorf("Dispatch(%T) at %d panes: error = %v, want ErrTooManyPanes", a, MaxPanes, err)
		}

		if got := len(st.State().Panes); got != MaxPanes {
			t.Errorf("panes = %d after %T, want %d unchanged", got, a, MaxPanes)
		}
	}
}
