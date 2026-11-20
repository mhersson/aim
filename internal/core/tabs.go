package core

import (
	"fmt"
	"slices"
	"strconv"
)

// TabID names a tab for the session: "t1", "t2", ... and never reused.
type TabID string

// LayoutMode is how a tab arranges its panes.
type LayoutMode string

// Layout modes.
const (
	ModeBSP       LayoutMode = "bsp"
	ModeMaster    LayoutMode = "master-stack"
	ModeScrolling LayoutMode = "scrolling"
)

// Column is one scrolling column.
type Column struct {
	Panes []PaneID
	Width float64 // share of the content width, 0.1-1.0
}

// Tab is one workspace: its own layout, focus and zoom, kept as plain values
// so a state copy is a value copy.
type Tab struct {
	ID        TabID
	Name      string     // "" for the automatic name
	Focus     PaneID     // "" when the tab has no pane
	Zoomed    bool       // the focused pane fills the body
	Mode      LayoutMode // never "" once a pane exists
	Panes     []PaneID   // layout order; empty for an empty tab
	Layout    *Node      // the BSP tree; nil outside BSP and for an empty tab
	Master    float64    // master-stack ratio, 0.1-0.9
	Columns   []Column   // scrolling columns, left to right
	First     int        // first visible scrolling column
	Preselect Dir        // NoDir when none; BSP only, not saved
	Review    *Review    // the open review drawer; nil when closed
	Pin       *Pin       // the worktree the drawer is held on; not saved
}

// Empty reports whether the tab has no pane.
func (t Tab) Empty() bool { return len(t.Panes) == 0 }

func (t Tab) clone() Tab {
	t.Panes = slices.Clone(t.Panes)

	t.Layout = t.Layout.clone()

	if t.Review != nil {
		r := *t.Review
		t.Review = &r
	}

	if t.Pin != nil {
		p := *t.Pin
		t.Pin = &p
	}

	if t.Columns != nil {
		cols := make([]Column, len(t.Columns))
		for i, c := range t.Columns {
			c.Panes = slices.Clone(c.Panes)
			cols[i] = c
		}

		t.Columns = cols
	}

	return t
}

// normalize gives a State with no tabs its one empty tab.
func (s *State) normalize() {
	if len(s.Tabs) == 0 {
		s.Tabs = []Tab{{ID: "t1"}}
		s.Active = 0
		s.NextTabID = max(s.NextTabID, 1)
	}

	if s.Panes == nil {
		s.Panes = map[PaneID]Pane{}
	}
}

// cur returns the active tab for in-place change.
func (s *State) cur() *Tab {
	s.normalize()

	return &s.Tabs[s.Active]
}

// Tab returns the active tab.
func (s State) Tab() Tab {
	if len(s.Tabs) == 0 {
		return Tab{ID: "t1"}
	}

	return s.Tabs[s.Active]
}

// TabOf returns the index of the tab holding a pane.
func (s State) TabOf(id PaneID) (int, bool) {
	for i, t := range s.Tabs {
		if slices.Contains(t.Panes, id) {
			return i, true
		}
	}

	return 0, false
}

// TabHasAgents reports whether tab i holds an agent pane.
func (s State) TabHasAgents(i int) bool {
	if i < 0 || i >= len(s.Tabs) {
		return false
	}

	return slices.ContainsFunc(s.Tabs[i].Panes, func(id PaneID) bool {
		return s.Panes[id].Kind == KindAgent
	})
}

func (s State) tabIndex(id TabID) (int, bool) {
	for i, t := range s.Tabs {
		if t.ID == id {
			return i, true
		}
	}

	return 0, false
}

// removeTab drops tab i and its panes' metadata. The only tab stays, emptied;
// keepName keeps its custom name (a closed pane, not a closed tab).
// The active tab stays the same one, or moves left when it was removed.
func (s *State) removeTab(i int, keepName bool) {
	for _, id := range s.Tabs[i].Panes {
		delete(s.Panes, id)
	}

	name := s.Tabs[i].Name
	if len(s.Tabs) == 1 {
		s.Tabs[0] = Tab{ID: s.Tabs[0].ID}
		if keepName {
			s.Tabs[0].Name = name
		}

		return
	}

	s.Tabs = append(s.Tabs[:i], s.Tabs[i+1:]...)
	if i <= s.Active && s.Active > 0 {
		s.Active--
	}
}

// NewTab appends a tab whose first pane is a new pane and activates it. An
// empty active tab (only possible as the only tab) is filled instead, keeping
// its ID and name.
type NewTab struct {
	Kind PaneKind
	Cwd  string
}

func (a NewTab) apply(s *State) error {
	if len(s.Panes) >= MaxPanes {
		return ErrTooManyPanes
	}

	if a.Kind != KindShell && a.Kind != KindAgent {
		return fmt.Errorf("core: NewTab with unknown kind %q", a.Kind)
	}

	s.normalize()
	id := s.NewPaneID()

	s.NextID++
	if t := s.cur(); t.Empty() {
		mode := t.Mode
		if mode == "" {
			mode = s.Layout.Mode()
		}

		t.start(id, mode, s.Layout)
		t.Focus = id
	} else {
		s.NextTabID++
		nt := Tab{ID: TabID("t" + strconv.Itoa(s.NextTabID))}
		nt.start(id, s.Layout.Mode(), s.Layout)
		nt.Focus = id
		s.Tabs = append(s.Tabs, nt)
		s.Active = len(s.Tabs) - 1
	}

	s.Panes[id] = Pane{Kind: a.Kind, Cwd: a.Cwd}

	return nil
}

// CloseTab removes a tab and its panes' metadata. Closing the active tab
// activates the tab to its left, or the new first tab. The only tab stays,
// emptied.
type CloseTab struct {
	ID TabID
}

func (a CloseTab) apply(s *State) error {
	s.normalize()

	i, ok := s.tabIndex(a.ID)
	if !ok {
		return fmt.Errorf("core: no tab %q", a.ID)
	}

	s.removeTab(i, false)

	return nil
}

// RenameTab sets a tab's name; "" restores the automatic name.
type RenameTab struct {
	ID   TabID
	Name string
}

func (a RenameTab) apply(s *State) error {
	s.normalize()

	i, ok := s.tabIndex(a.ID)
	if !ok {
		return fmt.Errorf("core: no tab %q", a.ID)
	}

	s.Tabs[i].Name = a.Name

	return nil
}

// SelectTab activates the tab at a 0-based index; out of range does nothing.
type SelectTab struct {
	Index int
}

func (a SelectTab) apply(s *State) error {
	s.normalize()

	if a.Index >= 0 && a.Index < len(s.Tabs) {
		s.Active = a.Index
	}

	return nil
}

// CycleTab activates the next (+1) or previous (-1) tab, wrapping.
type CycleTab struct {
	Delta int
}

func (a CycleTab) apply(s *State) error {
	s.normalize()
	n := len(s.Tabs)
	s.Active = ((s.Active+a.Delta)%n + n) % n

	return nil
}

// MoveTab moves a tab Delta places along the bar, -1 left and +1 right,
// keeping the active tab active; a move past either end does nothing.
type MoveTab struct {
	ID    TabID
	Delta int
}

func (a MoveTab) apply(s *State) error {
	s.normalize()

	i, ok := s.tabIndex(a.ID)
	if !ok {
		return fmt.Errorf("core: no tab %q", a.ID)
	}

	j := i + a.Delta
	if j == i || j < 0 || j >= len(s.Tabs) {
		return nil
	}

	active := s.Tabs[s.Active].ID
	t := s.Tabs[i]
	s.Tabs = slices.Insert(slices.Delete(s.Tabs, i, i+1), j, t)
	s.Active, _ = s.tabIndex(active)

	return nil
}
