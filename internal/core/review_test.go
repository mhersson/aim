package core

import (
	"errors"
	"reflect"
	"testing"
)

func reviewState(t *testing.T) State {
	t.Helper()

	// tab 0: p1, p2; tab 1: p3, p4
	return buildTabs(t, []LayoutMode{ModeBSP, ModeBSP}, []int{2, 2})
}

func TestReviewActions(t *testing.T) {
	s := reviewState(t)

	s = dispatch(t, s, OpenReview{Pane: "p1", Mode: ReviewTurn})
	if got, want := s.Tabs[0].Review, (&Review{Pane: "p1", Mode: ReviewTurn}); !reflect.DeepEqual(got, want) {
		t.Fatalf("after open: Review = %+v, want %+v", got, want)
	}

	s = dispatch(t, s, OpenReview{Pane: "p2", Mode: ReviewBranch})
	if got, want := s.Tabs[0].Review, (&Review{Pane: "p2", Mode: ReviewBranch}); !reflect.DeepEqual(got, want) {
		t.Fatalf("after retarget: Review = %+v, want %+v", got, want)
	}

	s = dispatch(t, s, SetReviewMode{Mode: ReviewTurn})
	if got, want := s.Tabs[0].Review, (&Review{Pane: "p2", Mode: ReviewTurn}); !reflect.DeepEqual(got, want) {
		t.Fatalf("after mode: Review = %+v, want %+v", got, want)
	}

	if s.Tabs[1].Review != nil {
		t.Errorf("other tab got a drawer: %+v", s.Tabs[1].Review)
	}

	s = dispatch(t, s, CloseReview{})
	if s.Tabs[0].Review != nil {
		t.Errorf("after close: Review = %+v, want nil", s.Tabs[0].Review)
	}

	// Closing with no drawer is not an error.
	dispatch(t, s, CloseReview{})
}

func TestReviewActionErrors(t *testing.T) {
	s := reviewState(t)

	tests := []struct {
		name   string
		action Action
	}{
		{"pane in another tab", OpenReview{Pane: "p3", Mode: ReviewTurn}},
		{"unknown pane", OpenReview{Pane: "p9", Mode: ReviewTurn}},
		{"unknown mode", OpenReview{Pane: "p1", Mode: "x"}},
		{"mode without a drawer", SetReviewMode{Mode: ReviewBranch}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if err := moveErr(s, tt.action); err == nil {
				t.Fatal("Dispatch() error = nil")
			}
		})
	}

	open := dispatch(t, s, OpenReview{Pane: "p1", Mode: ReviewTurn})
	if err := moveErr(open, SetReviewMode{Mode: "x"}); err == nil {
		t.Error("SetReviewMode with an unknown mode: error = nil")
	}
}

func TestSetReviewTree(t *testing.T) {
	s := dispatch(t, reviewState(t), OpenReview{Pane: "p1", Mode: ReviewTurn})
	if s.Tabs[0].Review.Tree {
		t.Fatal("a new drawer starts with the tree open")
	}

	s = dispatch(t, s, SetReviewTree{Open: true})
	if got, want := s.Tabs[0].Review, (&Review{Pane: "p1", Mode: ReviewTurn, Tree: true}); !reflect.DeepEqual(got, want) {
		t.Fatalf("after open: Review = %+v, want %+v", got, want)
	}

	s = dispatch(t, s, SetReviewTree{Open: true})
	if !s.Tabs[0].Review.Tree {
		t.Error("opening an open tree closed it")
	}

	s = dispatch(t, s, SetReviewTree{Open: false})
	if got, want := s.Tabs[0].Review, (&Review{Pane: "p1", Mode: ReviewTurn}); !reflect.DeepEqual(got, want) {
		t.Fatalf("after close: Review = %+v, want %+v", got, want)
	}
}

func TestSetReviewTreeWithoutDrawer(t *testing.T) {
	s := reviewState(t)

	if err := moveErr(s, SetReviewTree{Open: true}); !errors.Is(err, errNoReview) {
		t.Errorf("SetReviewTree without a drawer: error = %v, want errNoReview", err)
	}

	// A drawer in another tab does not count.
	s = dispatch(t, s, OpenReview{Pane: "p1", Mode: ReviewTurn})

	s = dispatch(t, s, SelectTab{Index: 1})
	if err := moveErr(s, SetReviewTree{Open: true}); !errors.Is(err, errNoReview) {
		t.Errorf("SetReviewTree on a tab without a drawer: error = %v, want errNoReview", err)
	}
}

func TestSetReviewTreeIsPerTab(t *testing.T) {
	s := reviewState(t)
	s = dispatch(t, s, OpenReview{Pane: "p1", Mode: ReviewTurn})
	s = dispatch(t, s, SelectTab{Index: 1})
	s = dispatch(t, s, OpenReview{Pane: "p3", Mode: ReviewBranch})
	s = dispatch(t, s, SetReviewTree{Open: true})

	if !s.Tabs[1].Review.Tree {
		t.Fatalf("active tab's tree is closed: %+v", s.Tabs[1].Review)
	}

	if got, want := s.Tabs[0].Review, (&Review{Pane: "p1", Mode: ReviewTurn}); !reflect.DeepEqual(got, want) {
		t.Errorf("other tab's drawer = %+v, want %+v", got, want)
	}

	s = dispatch(t, s, SelectTab{Index: 0})
	s = dispatch(t, s, SetReviewTree{Open: true})
	s = dispatch(t, s, SelectTab{Index: 1})
	s = dispatch(t, s, SetReviewTree{Open: false})

	if got, want := s.Tabs[0].Review, (&Review{Pane: "p1", Mode: ReviewTurn, Tree: true}); !reflect.DeepEqual(got, want) {
		t.Errorf("closing the other tab's tree changed this one: %+v, want %+v", got, want)
	}

	if s.Tabs[1].Review.Tree {
		t.Errorf("tab 1's tree is still open: %+v", s.Tabs[1].Review)
	}
}

func TestOpenReviewClosesTheTree(t *testing.T) {
	s := dispatch(t, reviewState(t), OpenReview{Pane: "p1", Mode: ReviewTurn})
	s = dispatch(t, s, SetReviewTree{Open: true})
	s = dispatch(t, s, OpenReview{Pane: "p1", Mode: ReviewTurn})

	if got, want := s.Tabs[0].Review, (&Review{Pane: "p1", Mode: ReviewTurn}); !reflect.DeepEqual(got, want) {
		t.Errorf("Review = %+v, want %+v", got, want)
	}
}

func TestSetReviewModeKeepsTheTree(t *testing.T) {
	s := dispatch(t, reviewState(t), OpenReview{Pane: "p2", Mode: ReviewTurn})
	s = dispatch(t, s, SetReviewTree{Open: true})
	s = dispatch(t, s, SetReviewMode{Mode: ReviewBranch})

	if got, want := s.Tabs[0].Review, (&Review{Pane: "p2", Mode: ReviewBranch, Tree: true}); !reflect.DeepEqual(got, want) {
		t.Errorf("Review = %+v, want %+v", got, want)
	}
}

func TestReviewClosePane(t *testing.T) {
	s := dispatch(t, reviewState(t), OpenReview{Pane: "p1", Mode: ReviewTurn})

	if got := dispatch(t, s, ClosePane{ID: "p2"}).Tabs[0].Review; got == nil || got.Pane != "p1" {
		t.Errorf("closing another pane: Review = %+v, want kept", got)
	}

	if got := dispatch(t, s, ClosePane{ID: "p1"}).Tabs[0].Review; got != nil {
		t.Errorf("closing the drawer's pane: Review = %+v, want nil", got)
	}
}

func TestReviewMovePane(t *testing.T) {
	tests := []struct {
		name   string
		action Action
	}{
		{"to another tab", MovePane{ID: "p1", Tab: 1, Area: modeInner}},
		{"break into a new tab", BreakPane{ID: "p1", Area: modeInner}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := dispatch(t, reviewState(t), OpenReview{Pane: "p1", Mode: ReviewTurn})

			got := dispatch(t, s, tt.action)
			if got.Tabs[0].Review != nil {
				t.Errorf("source tab Review = %+v, want nil", got.Tabs[0].Review)
			}
		})
	}

	t.Run("another pane keeps it", func(t *testing.T) {
		s := dispatch(t, reviewState(t), OpenReview{Pane: "p1", Mode: ReviewTurn})

		got := dispatch(t, s, MovePane{ID: "p2", Tab: 1, Area: modeInner})
		if got.Tabs[0].Review == nil {
			t.Error("source tab lost its drawer")
		}
	})
}

func TestStateCloneCopiesReview(t *testing.T) {
	s := reviewState(t)
	s = dispatch(t, s, OpenReview{Pane: "p1", Mode: ReviewTurn})

	c := s.clone()
	c.Tabs[0].Review.Mode = ReviewBranch

	if s.Tabs[0].Review.Mode != ReviewTurn {
		t.Error("State.clone shares Review")
	}
}

// A layout switch rebuilds the tab but keeps its drawer.
func TestLayoutSwitchKeepsTheReviewDrawer(t *testing.T) {
	want := &Review{Pane: "p2", Mode: ReviewBranch}

	t.Run("cycle", func(t *testing.T) {
		s := reviewState(t)
		s = dispatch(t, s, OpenReview{Pane: "p2", Mode: ReviewBranch})

		for range 3 {
			s = dispatch(t, s, CycleLayoutMode{})

			if got := s.Tabs[0].Review; !reflect.DeepEqual(got, want) {
				t.Fatalf("after cycling to %q Review = %+v, want %+v", s.Tabs[0].Mode, got, want)
			}
		}
	})
}

func TestPinReview(t *testing.T) {
	t.Parallel()

	open := func(t *testing.T) State {
		t.Helper()

		return dispatch(t, reviewState(t), OpenReview{Pane: "p1", Mode: ReviewTurn})
	}
	pinned := func(t *testing.T) State {
		t.Helper()

		return dispatch(t, open(t), PinReview{Root: "/w/b"})
	}
	want := &Pin{Pane: "p1", Root: "/w/b"}

	tests := []struct {
		name string
		do   func(t *testing.T) State
		want *Pin
	}{
		{"pin", pinned, want},
		{"unpin", func(t *testing.T) State { return dispatch(t, pinned(t), PinReview{}) }, nil},
		{"open on the same pane keeps it", func(t *testing.T) State {
			return dispatch(t, pinned(t), OpenReview{Pane: "p1", Mode: ReviewBranch})
		}, want},
		{"open on another pane drops it", func(t *testing.T) State {
			return dispatch(t, pinned(t), OpenReview{Pane: "p2", Mode: ReviewTurn})
		}, nil},
		{"close keeps it", func(t *testing.T) State { return dispatch(t, pinned(t), CloseReview{}) }, want},
		{"closing the pinned pane drops it", func(t *testing.T) State {
			return dispatch(t, pinned(t), ClosePane{ID: "p1"})
		}, nil},
		{"closing another pane keeps it", func(t *testing.T) State {
			return dispatch(t, pinned(t), ClosePane{ID: "p2"})
		}, want},
		{"moving the pinned pane out drops it", func(t *testing.T) State {
			return dispatch(t, pinned(t), MovePane{ID: "p1", Tab: 1, Area: modeInner})
		}, nil},
		{"a layout mode switch keeps it", func(t *testing.T) State {
			return dispatch(t, pinned(t), SetLayoutMode{Mode: ModeMaster, Area: modeInner})
		}, want},
		{"a layout cycle keeps it", func(t *testing.T) State {
			return dispatch(t, pinned(t), CycleLayoutMode{Area: modeInner})
		}, want},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got := tt.do(t).Tabs[0].Pin
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("Pin = %+v, want %+v", got, tt.want)
			}
		})
	}

	t.Run("no drawer is an error", func(t *testing.T) {
		t.Parallel()

		if err := moveErr(reviewState(t), PinReview{Root: "/w/b"}); !errors.Is(err, errNoReview) {
			t.Errorf("PinReview without a drawer: error = %v, want errNoReview", err)
		}
	})

	t.Run("a drawer in another tab does not count", func(t *testing.T) {
		t.Parallel()

		s := dispatch(t, open(t), SelectTab{Index: 1})
		if err := moveErr(s, PinReview{Root: "/w/b"}); !errors.Is(err, errNoReview) {
			t.Errorf("PinReview on a tab without a drawer: error = %v, want errNoReview", err)
		}
	})

	t.Run("a state taken before keeps its own pin", func(t *testing.T) {
		t.Parallel()

		st := NewStore(open(t))
		before := st.State()

		if err := st.Dispatch(PinReview{Root: "/w/b"}); err != nil {
			t.Fatal(err)
		}

		if before.Tabs[0].Pin != nil {
			t.Errorf("earlier state got the pin: %+v", before.Tabs[0].Pin)
		}

		got := st.State()
		got.Tabs[0].Pin.Root = "/elsewhere"

		if root := st.State().Tabs[0].Pin.Root; root != "/w/b" {
			t.Errorf("editing a copy changed the store's pin: %q", root)
		}
	})

	t.Run("the pin is per tab", func(t *testing.T) {
		t.Parallel()

		s := dispatch(t, pinned(t), SelectTab{Index: 1})
		s = dispatch(t, s, OpenReview{Pane: "p3", Mode: ReviewTurn})

		if s.Tabs[1].Pin != nil || !reflect.DeepEqual(s.Tabs[0].Pin, want) {
			t.Errorf("pins = %+v, %+v", s.Tabs[0].Pin, s.Tabs[1].Pin)
		}
	})
}

func TestShowReviewPR(t *testing.T) {
	t.Parallel()

	open := dispatch(t, reviewState(t), OpenReview{Pane: "p1", Mode: ReviewTurn})

	tests := []struct {
		name    string
		s       State
		act     ShowReviewPR
		wantErr bool
	}{
		{"no drawer", reviewState(t), ShowReviewPR{Repo: repoKey, Number: 3, Root: "/w"}, true},
		{"empty repo", open, ShowReviewPR{Number: 3, Root: "/w"}, true},
		{"empty root", open, ShowReviewPR{Repo: repoKey, Number: 3}, true},
		{"number zero", open, ShowReviewPR{Repo: repoKey, Root: "/w"}, true},
		{"ok", open, ShowReviewPR{Repo: repoKey, Number: 3, Root: "/w"}, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			st := NewStore(tt.s)

			err := st.Dispatch(tt.act)
			if (err != nil) != tt.wantErr {
				t.Fatalf("error = %v, want error %v", err, tt.wantErr)
			}

			if tt.wantErr {
				return
			}

			want := &ReviewPR{Repo: repoKey, Number: 3, Root: "/w"}
			if got := st.State().Tabs[0].Review.PR; !reflect.DeepEqual(got, want) {
				t.Errorf("PR = %+v, want %+v", got, want)
			}
		})
	}
}

func TestShowReviewPRKeepsCopy(t *testing.T) {
	t.Parallel()

	st := NewStore(dispatch(t, reviewState(t), OpenReview{Pane: "p1", Mode: ReviewTurn}))

	if err := st.Dispatch(ShowReviewPR{Repo: repoKey, Number: 3, Root: "/c", Copy: true}); err != nil {
		t.Fatal(err)
	}

	want := &ReviewPR{Repo: repoKey, Number: 3, Root: "/c", Copy: true}
	if got := st.State().Tabs[0].Review.PR; !reflect.DeepEqual(got, want) {
		t.Errorf("PR = %+v, want %+v", got, want)
	}
}

func TestReviewPRLeavesWithModeChangesAndPins(t *testing.T) {
	t.Parallel()

	shown := dispatch(t, reviewState(t), OpenReview{Pane: "p1", Mode: ReviewTurn})
	shown = dispatch(t, shown, ShowReviewPR{Repo: repoKey, Number: 3, Root: "/w"})

	tests := []struct {
		name string
		act  Action
	}{
		{"SetReviewMode", SetReviewMode{Mode: ReviewBranch}},
		{"PinReview", PinReview{Root: "/w"}},
		{"OpenReview", OpenReview{Pane: "p1", Mode: ReviewTurn}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if got := dispatch(t, shown, tt.act).Tabs[0].Review.PR; got != nil {
				t.Errorf("PR = %+v, want nil", got)
			}
		})
	}
}
