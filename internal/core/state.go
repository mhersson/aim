package core

import (
	"errors"
	"maps"
)

// State is the application state; only a Store changes it. A State with no
// tabs behaves as one empty tab.
type State struct {
	Theme     string
	Tabs      []Tab // at least one once an action has run
	Active    int   // index into Tabs
	Panes     map[PaneID]Pane
	NextID    int          // last pane number handed out
	NextTabID int          // last tab number handed out
	Layout    LayoutConfig // settings Arrange and the placement helpers read
	Rail      bool         // the agents rail is open; it narrows every pane's body

	// PRs holds each GitHub repository's pull requests, keyed "host/owner/name".
	// It is not saved. Copies of the state share it, so actions replace the map
	// and its values and never edit them in place.
	PRs map[string]RepoPRs

	// Marks holds each repository's reviewed-hunk marks, keyed by its common
	// dir. It is not saved in state.json (the marks have their own files) and
	// copies share it, so actions replace the map and its values.
	Marks map[string]RepoMarks

	// Usage is the account's Claude usage. It is not saved: it comes back from
	// the agents' status lines.
	Usage Usage
}

// InitialState returns the state of a fresh session: one empty tab.
func InitialState(cfg Config) State {
	return State{
		Theme:     cfg.Theme,
		Tabs:      []Tab{{ID: "t1"}},
		Panes:     map[PaneID]Pane{},
		NextTabID: 1,
		Layout:    cfg.Layout,
	}
}

func (s State) clone() State {
	c := s

	c.Tabs = make([]Tab, len(s.Tabs))
	for i, t := range s.Tabs {
		c.Tabs[i] = t.clone()
	}

	c.Panes = make(map[PaneID]Pane, len(s.Panes))
	maps.Copy(c.Panes, s.Panes)

	return c
}

// MaxPanes is the most panes a state may hold across all tabs; the saved
// layout file is rejected above it.
const MaxPanes = 64

// ErrTooManyPanes is returned by OpenPane and NewTab at MaxPanes panes.
var ErrTooManyPanes = errors.New("core: too many panes")

// Action is a state change applied through Store.Dispatch.
type Action interface {
	apply(*State) error
}

// SetTheme switches the theme by name. Whether the name is a known theme is
// the caller's check, since core cannot import the theme package.
type SetTheme struct {
	Name string
}

func (a SetTheme) apply(s *State) error {
	if a.Name == "" {
		return errors.New("core: SetTheme with an empty name")
	}

	s.Theme = a.Name

	return nil
}

// ToggleRail opens the agents rail when it is closed and closes it when open.
type ToggleRail struct{}

func (ToggleRail) apply(s *State) error {
	s.Rail = !s.Rail

	return nil
}

// SetRail opens or closes the agents rail.
type SetRail struct {
	Open bool
}

func (a SetRail) apply(s *State) error {
	s.Rail = a.Open

	return nil
}

// Store holds the State. It is used only on the Bubble Tea goroutine, so it
// has no lock.
type Store struct {
	state State
}

// NewStore returns a Store holding initial.
func NewStore(initial State) *Store {
	return &Store{state: initial.clone()}
}

// Dispatch applies a. On error the state is unchanged.
func (s *Store) Dispatch(a Action) error {
	next := s.state.clone()
	if err := a.apply(&next); err != nil {
		return err
	}

	s.state = next

	return nil
}

// State returns a copy of the current state; tabs and panes are deep copies,
// and PRs is shared (see State.PRs).
func (s *Store) State() State {
	return s.state.clone()
}
