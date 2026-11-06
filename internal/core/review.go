package core

import (
	"errors"
	"fmt"
	"slices"
)

// ReviewMode is what a tab's review drawer shows.
type ReviewMode string

// Review modes.
const (
	ReviewTurn   ReviewMode = "turn"
	ReviewBranch ReviewMode = "branch"
)

// Valid reports whether m is a known mode.
func (m ReviewMode) Valid() bool { return slices.Contains(ReviewModes(), m) }

// ReviewModes lists the review modes.
func ReviewModes() []ReviewMode { return []ReviewMode{ReviewTurn, ReviewBranch} }

// Review is a tab's open review drawer: the pane it reviews, its mode and
// whether its file tree is open.
type Review struct {
	Pane PaneID
	Mode ReviewMode
	Tree bool
	PR   *ReviewPR // the pull request the drawer shows instead of a diff; never saved
}

// ReviewPR is the pull request a drawer in PR mode shows: its repository keyed
// "host/owner/name", its number and the worktree root it was opened from. Copy
// marks a root that is AIM's private copy of the repository, not a worktree.
type ReviewPR struct {
	Repo   string
	Number int
	Root   string
	Copy   bool
}

// Pin holds a tab's drawer on one worktree while it reviews Pane; not persisted.
type Pin struct {
	Pane PaneID
	Root string
}

// errNoReview is returned by SetReviewMode and SetReviewTree when the active
// tab has no drawer.
var errNoReview = errors.New("core: no review drawer is open")

// dropReview closes the drawer when it reviews id, which is leaving the tab,
// and releases a pin on id.
func (t *Tab) dropReview(id PaneID) {
	if t.Review != nil && t.Review.Pane == id {
		t.Review = nil
	}

	if t.Pin != nil && t.Pin.Pane == id {
		t.Pin = nil
	}
}

// OpenReview opens the active tab's drawer on a pane of that tab, replacing
// any open one. A pin on another pane ends.
type OpenReview struct {
	Pane PaneID
	Mode ReviewMode
}

func (a OpenReview) apply(s *State) error {
	t := s.cur()
	if !slices.Contains(t.Panes, a.Pane) {
		return fmt.Errorf("core: no pane %q in the active tab", a.Pane)
	}

	if !a.Mode.Valid() {
		return fmt.Errorf("core: unknown review mode %q", a.Mode)
	}

	t.Review = &Review{Pane: a.Pane, Mode: a.Mode}
	if t.Pin != nil && t.Pin.Pane != a.Pane {
		t.Pin = nil
	}

	return nil
}

// CloseReview closes the active tab's drawer, keeping its pin; with none open
// it does nothing.
type CloseReview struct{}

func (CloseReview) apply(s *State) error {
	s.cur().Review = nil

	return nil
}

// SetReviewMode changes the open drawer's mode.
type SetReviewMode struct {
	Mode ReviewMode
}

func (a SetReviewMode) apply(s *State) error {
	t := s.cur()
	if t.Review == nil {
		return errNoReview
	}

	if !a.Mode.Valid() {
		return fmt.Errorf("core: unknown review mode %q", a.Mode)
	}

	t.Review.Mode = a.Mode
	t.Review.PR = nil

	return nil
}

// ShowReviewPR puts the open drawer in PR mode on a pull request.
type ShowReviewPR struct {
	Repo   string
	Number int
	Root   string
	Copy   bool
}

func (a ShowReviewPR) apply(s *State) error {
	t := s.cur()
	if t.Review == nil {
		return errNoReview
	}

	if a.Repo == "" || a.Root == "" || a.Number < 1 {
		return errors.New("core: ShowReviewPR needs a repository, a root and a pull request number")
	}

	t.Review.PR = &ReviewPR{Repo: a.Repo, Number: a.Number, Root: a.Root, Copy: a.Copy}

	return nil
}

// SetReviewTree opens or closes the open drawer's file tree.
type SetReviewTree struct {
	Open bool
}

func (a SetReviewTree) apply(s *State) error {
	t := s.cur()
	if t.Review == nil {
		return errNoReview
	}

	t.Review.Tree = a.Open

	return nil
}

// PinReview holds the open drawer on the worktree at Root, or releases it when
// Root is empty.
type PinReview struct{ Root string }

func (a PinReview) apply(s *State) error {
	t := s.cur()
	if t.Review == nil {
		return errNoReview
	}

	t.Review.PR = nil

	t.Pin = nil
	if a.Root != "" {
		t.Pin = &Pin{Pane: t.Review.Pane, Root: a.Root}
	}

	return nil
}
