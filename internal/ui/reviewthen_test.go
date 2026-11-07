package ui

import (
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/mhersson/aim/internal/core"
)

func TestAfterLoadActionNeedsTheKeyboard(t *testing.T) {
	t.Parallel()

	t.Run("the drawer lost the keyboard before the load", func(t *testing.T) {
		t.Parallel()

		r := newPRDrawer(t)
		r.actions = &fakeActions{}

		r.poll()
		r.opened(t)
		r.typed("a")
		r.leader('h')

		if r.drawer.focused {
			t.Fatal("leader h left the drawer focused")
		}

		r.until(t, "the PR to load", r.inPRMode(7))
		r.settle()

		if r.confirm != nil {
			t.Errorf("the late approve opened a confirm: %+v", *r.confirm)
		}

		if want := r.prLoadedNotice(7, thenApprove); r.notice != want {
			t.Errorf("notice = %q, want %q", r.notice, want)
		}
	})

	t.Run("another confirm is open at the load", func(t *testing.T) {
		t.Parallel()

		r := newPRDrawer(t)
		r.actions = &fakeActions{}

		r.poll()
		r.opened(t)
		r.typed("a")

		other := &confirm{prompt: "Quit AIM?", yes: func(*App) tea.Cmd { return nil }}
		r.openConfirm(*other)

		r.until(t, "the PR to load", r.inPRMode(7))
		r.settle()

		if r.confirm == nil || r.confirm.approve != nil || r.confirm.prompt != other.prompt {
			t.Errorf("confirm = %+v, want the quit confirm left alone", r.confirm)
		}

		if want := r.prLoadedNotice(7, thenApprove); r.notice != want {
			t.Errorf("notice = %q, want %q", r.notice, want)
		}
	})

	t.Run("a merge asked for under a modal is dropped with its notice", func(t *testing.T) {
		t.Parallel()

		r := newPRDrawer(t)
		r.actions = &fakeActions{}

		r.poll()
		r.opened(t)
		r.typed("M")
		r.leader('h')

		r.until(t, "the PR to load", r.inPRMode(7))
		r.settle()

		if r.merge != nil {
			t.Error("the late merge opened the box")
		}

		if want := r.prLoadedNotice(7, thenMerge); r.notice != want {
			t.Errorf("notice = %q, want %q", r.notice, want)
		}
	})

	t.Run("the merge command takes an unfocused drawer's keyboard", func(t *testing.T) {
		t.Parallel()

		r := newPRDrawer(t)
		r.actions = &fakeActions{}

		r.poll()
		r.opened(t)
		r.leader('h')

		if r.drawer.focused {
			t.Fatal("leader h left the drawer focused")
		}

		r.run(r.mergeCommand())
		r.until(t, "the box", func() bool { return r.merge != nil })

		if !r.drawer.focused {
			t.Error("the merge left the keyboard off the drawer")
		}
	})
}

func TestAfterLoadActionDroppedByANewerAsk(t *testing.T) {
	t.Parallel()

	// a's place and fetch answers are fed by hand, so r lands between them where
	// the case says.
	for _, tt := range []struct {
		name       string
		placeFirst bool // the place answer arrives before r
	}{
		{"r before the place answer", false},
		{"r before the fetch answer", true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			r := newPRDrawer(t)
			r.actions = &fakeActions{}

			r.poll()
			r.opened(t)

			place := r.step(keyRune('a'))
			if place == nil {
				t.Fatalf("a asked for nothing; notice = %q", r.notice)
			}

			var fetch tea.Cmd

			if tt.placeFirst {
				if fetch = r.step(place()); fetch == nil {
					t.Fatalf("the place answer started no fetch; notice = %q", r.notice)
				}

				r.typed("r")
			} else {
				r.typed("r")

				fetch = r.step(place())
			}

			if fetch != nil {
				r.run(r.step(fetch()))
			}

			r.settled(t)

			if r.confirm != nil {
				t.Errorf("a's answers after r opened a confirm: %+v", *r.confirm)
			}
		})
	}

	t.Run("r while the drawer opens on another pane for it", func(t *testing.T) {
		t.Parallel()

		r := newPRDrawer(t)
		r.actions = &fakeActions{}

		r.poll()
		r.opened(t)

		// The drawer stays on the first pane, with the keyboard, until the open answers.
		open := r.openWithPR(r.shell(t, r.dir), &prWant{repo: prRepoKey, number: 7, root: r.root}, thenApprove)

		r.typed("r")
		r.run(open)
		r.until(t, "PR mode", r.inPRMode(7))
		r.settled(t)

		if r.confirm != nil {
			t.Errorf("an r while the drawer opened left a confirm open: %+v", *r.confirm)
		}
	})

	t.Run("r while the pull request's diff loads", func(t *testing.T) {
		t.Parallel()

		r := newPRDrawer(t)
		r.actions = &fakeActions{}

		r.poll()
		r.opened(t)
		r.run(r.showPR(r.store.State().Tab().ID, thenApprove))
		r.until(t, "the wait", func() bool { return r.review().PR != nil && r.view().then == thenApprove })

		r.typed("r")
		r.settle()
		r.settled(t)

		if r.confirm != nil {
			t.Errorf("an r while loading left a confirm open: %+v", *r.confirm)
		}
	})
}

func TestPinnedDrawerKeepsTheUnpushedCountInPRMode(t *testing.T) {
	t.Parallel()

	r := newPRDrawer(t)
	r.poll()
	r.opened(t)

	openIn(t, r.App, core.PinReview{Root: r.fake.repo(t, "other").Root()})
	r.settle()

	r.run(r.openWithPR(r.focus(), &prWant{repo: prRepoKey, number: 7, root: r.root}, thenNothing))
	r.until(t, "PR mode", r.inPRMode(7))

	// The worktree has two commits that origin's branches lack.
	if got := r.view().unpushed; got != 2 {
		t.Errorf("unpushed = %d, want 2", got)
	}
}

func TestActOnAHeldPRAsksForThatPR(t *testing.T) {
	t.Parallel()

	r := newPRDrawer(t)
	r.actions = &fakeActions{}

	r.poll()
	r.opened(t)
	r.typed("p")
	r.until(t, "PR mode", r.inPRMode(7))

	r.view().err = errNotRepo

	cmd := r.prAct(thenNothing)
	if cmd == nil {
		t.Fatalf("prAct asked for nothing; notice = %q", r.notice)
	}

	msg, ok := cmd().(prPlaceMsg)
	if !ok || msg.want == nil || msg.want.number != 7 || msg.want.root != r.root {
		t.Errorf("ask = %+v, want the held pull request 7 at %s", msg.want, r.root)
	}
}
