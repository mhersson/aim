package ui

import (
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/x/ansi"

	"github.com/mhersson/aim/internal/core"
	"github.com/mhersson/aim/internal/git"
	"github.com/mhersson/aim/internal/harness"
)

// pinTo holds the open drawer on a worktree root.
func (r *followRig) pinTo(t *testing.T, root string) {
	t.Helper()
	openIn(t, r.App, core.PinReview{Root: root})
	r.settle()
}

// unpin lets the drawer follow its pane again.
func (r *followRig) unpin(t *testing.T) { r.pinTo(t, "") }

func (r *followRig) tabID() core.TabID { return r.store.State().Tab().ID }

// pinHeld reports whether the active tab holds a watch reference on root for its pin.
func (r *followRig) pinHeld(root string) bool {
	pw := r.watches.pins[r.tabID()]

	return pw != nil && pw.root == root && pw.release != nil && r.watched(root)
}

// showing is a condition: the open drawer shows root, loaded and idle.
func (r *followRig) showing(root string) func() bool {
	return r.viewHolds(func(v *reviewView) bool { return v.root == root && v.loaded && !v.loading })
}

func TestPinnedDrawerStaysPut(t *testing.T) {
	t.Parallel()
	r := newFollowRig(t, "")
	r.addLines(t, filepath.Join(r.dirB, "a.txt"))
	r.addLines(t, filepath.Join(r.dirC, "c.txt"))
	r.open(t, r.id)
	r.openRail(t, "main")

	r.pinTo(t, r.rootB)
	r.until(t, "the drawer to show b", r.showing(r.rootB))

	r.clk.Advance(11 * time.Second)
	r.edit(filepath.Join(r.dirC, "c.txt"))
	r.until(t, "the rail to follow the edit", r.cardOn("other", -1))
	r.until(t, "the agent's watch to move to C", func() bool { return r.held(r.id) == r.rootC })
	r.until(t, "the edit's probes to land", r.landed)

	v := r.view()
	if v.root != r.rootB || !slices.Equal(viewPaths(v), []string{"a.txt"}) || v.branch != "b" {
		t.Errorf("drawer shows %v of %q on %q, want a.txt on b", viewPaths(v), v.branch, v.root)
	}

	if got := r.pane().FollowDir(); got != r.dirC {
		t.Errorf("FollowDir() = %q, want %q", got, r.dirC)
	}

	if drawn := r.drawn(t); !strings.Contains(drawn, filepath.Base(r.dirA)+" · b") || !strings.Contains(drawn, "pinned") {
		t.Errorf("drawer reads\n%s\nwant the title %q and the pinned tag", drawn, filepath.Base(r.dirA)+" · b")
	}
}

func TestPinSurvivesReopenOnSamePane(t *testing.T) {
	t.Parallel()
	r := newFollowRig(t, "")
	r.addLines(t, filepath.Join(r.dirB, "a.txt"))
	r.open(t, r.id)

	r.pinTo(t, r.rootB)
	r.until(t, "the drawer to show b", r.showing(r.rootB))
	r.until(t, "the pin's watch", func() bool { return r.pinHeld(r.rootB) })

	tab := r.tabID()

	openIn(t, r.App, core.CloseReview{})
	r.settle()
	r.until(t, "the pin's watch to go with the drawer", func() bool { return r.watches.pins[tab] == nil && !r.watched(r.rootB) })

	if pin := r.store.State().Tab().Pin; pin == nil || pin.Root != r.rootB {
		t.Fatalf("Pin = %+v after closing the drawer, want it kept on %q", pin, r.rootB)
	}

	v := r.open(t, r.id)
	if v.root != r.rootB || !slices.Equal(viewPaths(v), []string{"a.txt"}) {
		t.Errorf("reopened drawer shows %v on %q, want a.txt on %q", viewPaths(v), v.root, r.rootB)
	}

	r.until(t, "the pin's watch again", func() bool { return r.pinHeld(r.rootB) })

	if !strings.Contains(r.drawn(t), "pinned") {
		t.Error("the reopened drawer lacks the pinned tag")
	}
}

func TestPinEndsOnAnotherPane(t *testing.T) {
	t.Parallel()
	r := newFollowRig(t, "")

	// The first pane lies left of the drawer, so the keyboard can stay with the panes.
	const other = core.PaneID("p1")

	openIn(t, r.App, core.SetPaneCwd{ID: other, Cwd: r.dirC})

	r.open(t, r.id)
	r.pinTo(t, r.rootB)
	r.until(t, "the drawer to show b", r.showing(r.rootB))
	r.until(t, "the pin's watch", func() bool { return r.pinHeld(r.rootB) })

	r.drawerRelease()
	openIn(t, r.App, core.FocusPane{ID: other})
	r.settle()

	if r.drawerHasKeys() {
		t.Fatal("setup: the drawer took the keyboard back")
	}

	r.leader('d')
	r.until(t, "the drawer to follow the other pane", func() bool {
		rv := r.store.State().Tab().Review

		return rv != nil && rv.Pane == other && r.showing(r.rootC)()
	})

	if pin := r.store.State().Tab().Pin; pin != nil {
		t.Errorf("Pin = %+v, want none on another pane", pin)
	}

	r.until(t, "the pin's watch to be released", func() bool { return r.watches.pins[r.tabID()] == nil && !r.watched(r.rootB) })

	if strings.Contains(r.drawn(t), "pinned") {
		t.Error("the drawer on the other pane has the pinned tag")
	}
}

func TestPinSurvivesNewSession(t *testing.T) {
	t.Parallel()
	r := newFollowRig(t, testSession)
	r.addLines(t, filepath.Join(r.dirB, "a.txt"))
	r.addLines(t, filepath.Join(r.dirC, "c.txt"))
	r.open(t, r.id)
	r.openRail(t, "main")

	r.clk.Advance(11 * time.Second)
	r.edit(filepath.Join(r.dirC, "c.txt"))
	r.until(t, "the rail to follow the edit", r.cardOn("other", -1))

	r.pinTo(t, r.rootB)
	r.until(t, "the drawer to show b", r.showing(r.rootB))

	r.clk.Advance(11 * time.Second)
	r.hookNow(harness.Event{Pane: r.id, Name: "SessionStart", Start: true, Session: otherSession, Cwd: r.dirA})
	r.until(t, "the rail to return to A", r.cardOn("main", -1))
	r.until(t, "the agent's watch to return to A", func() bool { return r.held(r.id) == r.rootA })
	r.until(t, "the session start's probes to land", r.landed)

	if p := r.pane(); len(p.Edits) != 0 || p.FollowDir() != r.dirA {
		t.Errorf("pane = %d edits, FollowDir %q, want none and %q", len(p.Edits), p.FollowDir(), r.dirA)
	}

	if pin := r.store.State().Tab().Pin; pin == nil || pin.Root != r.rootB {
		t.Errorf("Pin = %+v after a new session, want it kept on %q", pin, r.rootB)
	}

	v := r.view()
	if v.root != r.rootB || !slices.Equal(viewPaths(v), []string{"a.txt"}) {
		t.Errorf("drawer shows %v on %q, want a.txt on %q", viewPaths(v), v.root, r.rootB)
	}
}

func TestPinnedDrawerWatchesItsRoot(t *testing.T) {
	t.Parallel()
	r := newFollowRig(t, "")
	r.open(t, r.id)

	r.edit(filepath.Join(r.dirC, "c.txt"))
	r.until(t, "the agent's watch to move to C", func() bool { return r.held(r.id) == r.rootC })

	r.pinTo(t, r.rootB)
	r.until(t, "the drawer to show b", r.showing(r.rootB))
	r.until(t, "the pin's watch", func() bool { return r.pinHeld(r.rootB) })

	if got := r.held(r.id); got != r.rootC {
		t.Fatalf("the agent's watch holds %q, want %q", got, r.rootC)
	}

	// A hook adds nothing the pin's watch does not report.
	if cmd := r.reviewHook(r.id, false, nil); cmd != nil {
		t.Error("a hook recomputed a drawer whose root is watched")
	}

	r.addLines(t, filepath.Join(r.dirB, "a.txt"))
	r.fake.watches.send(r.rootB, git.Batch{Paths: []string{"a.txt"}})
	r.until(t, "the pinned drawer to show b's change", r.viewHolds(func(v *reviewView) bool {
		return v.root == r.rootB && slices.Equal(viewPaths(v), []string{"a.txt"})
	}))

	r.unpin(t)
	r.until(t, "the drawer to follow C again", r.showing(r.rootC))
	r.until(t, "b's pin reference to be released", func() bool { return r.watches.pins[r.tabID()] == nil && !r.watched(r.rootB) })
}

func TestPinnedHookRefreshUsesItsRoot(t *testing.T) {
	t.Parallel()
	r := newFollowRig(t, "")

	// The watch of b cannot start.
	r.fake.watches.failAcquire(r.rootB, errors.New("no watch for b"))

	r.open(t, r.id)
	r.pinTo(t, r.rootB)
	r.until(t, "the drawer to show b", r.showing(r.rootB))
	r.until(t, "the failed watch to be noticed", func() bool { return strings.Contains(r.notice, "failed") })

	if r.pinHeld(r.rootB) || r.rootWatched(r.rootB) {
		t.Fatal("b's watch is held although it failed")
	}

	// No watcher reports the write, so nothing reaches the app that could reload the drawer.
	r.addLines(t, filepath.Join(r.dirB, "a.txt"))
	r.until(t, "the drawer to be idle", r.landed)

	if got := viewPaths(r.view()); len(got) != 0 {
		t.Fatalf("drawer shows %v with no watch on b and no hook", got)
	}

	// A's watch reports a batch, and it does not stand in for b's.
	r.until(t, "the agent's watch on A", func() bool { return r.held(r.id) == r.rootA })
	heads := r.fake.count("Head")
	r.fake.watches.send(r.rootA, git.Batch{Git: true, Paths: []string{"a.txt"}})
	r.until(t, "the batch for A to land", func() bool { return r.fake.count("Head") > heads && r.landed() })

	if got := viewPaths(r.view()); len(got) != 0 {
		t.Fatalf("drawer shows %v after a batch for A with no watch on b", got)
	}

	// The agent's own watch on A is held, and does not stand in for b's.
	r.hookNow(idleHook(r.id))
	r.until(t, "the hook to recompute the pinned drawer", r.viewHolds(func(v *reviewView) bool {
		return v.root == r.rootB && slices.Equal(viewPaths(v), []string{"a.txt"})
	}))
}

func TestPinnedWorktreeRemoved(t *testing.T) {
	t.Parallel()
	r := newFollowRig(t, "")
	home := filepath.Dir(r.dirA)
	r.getenv = func(string) string { return home }

	r.addLines(t, filepath.Join(r.dirB, "a.txt"))
	r.open(t, r.id)
	r.pinTo(t, r.rootB)
	r.until(t, "the drawer to show b", r.showing(r.rootB))
	r.until(t, "the pin's watch", func() bool { return r.pinHeld(r.rootB) })

	if err := os.RemoveAll(r.dirB); err != nil {
		t.Fatal(err)
	}

	// The watcher reports the files that went.
	r.fake.watches.send(r.rootB, git.Batch{Paths: []string{"a.txt"}})
	r.hookNow(idleHook(r.id))
	r.until(t, "the drawer to find the worktree gone", r.viewHolds(func(v *reviewView) bool {
		var gone errWorktreeRemoved

		return v.loaded && !v.loading && errors.As(v.err, &gone) && gone.root == r.rootB
	}))
	r.until(t, "the hook's probes to land", r.landed)

	if pin := r.store.State().Tab().Pin; pin == nil || pin.Root != r.rootB {
		t.Errorf("Pin = %+v, want it kept on the removed worktree", pin)
	}

	if r.notice != "" {
		t.Errorf("notice = %q, want none", r.notice)
	}

	drawn := r.drawn(t)
	if want := "worktree removed: ~/" + filepath.Base(r.dirA) + "/.worktrees/b"; !strings.Contains(drawn, want) {
		t.Errorf("drawer reads\n%s\nwant a row %q", drawn, want)
	}

	if want := filepath.Base(r.dirA) + " · b"; !strings.Contains(drawn, want) {
		t.Errorf("drawer reads\n%s\nwant the title %q", drawn, want)
	}

	// It never moves on by itself, and picking again does.
	r.hookNow(idleHook(r.id))
	r.until(t, "the hook's probes to land", r.landed)

	if v := r.view(); v.root != r.rootB {
		t.Errorf("drawer moved to %q", v.root)
	}

	r.pinTo(t, r.rootA)
	r.until(t, "the drawer to show A", r.showing(r.rootA))

	if v := r.view(); v.err != nil {
		t.Errorf("drawer error = %v after picking A", v.err)
	}
}

// A pinned worktree that went while the drawer was closed still opens a
// drawer, which says so and from which another worktree can be picked.
func TestReopenOnRemovedPinnedWorktree(t *testing.T) {
	t.Parallel()
	r := newFollowRig(t, "")
	r.open(t, r.id)
	r.pinTo(t, r.rootB)
	r.until(t, "the drawer to show b", r.showing(r.rootB))

	openIn(t, r.App, core.CloseReview{})
	r.settle()
	r.until(t, "the pin's watch to go", func() bool { return r.watches.pins[r.tabID()] == nil })

	if err := os.RemoveAll(r.dirB); err != nil {
		t.Fatal(err)
	}

	r.run(r.openReview(r.id, true))
	r.until(t, "the drawer to open", func() bool { return r.store.State().Tab().Review != nil })
	r.until(t, "the drawer to find the worktree gone", r.viewHolds(func(v *reviewView) bool {
		return v.loaded && !v.loading && errors.Is(v.err, errWorktreeRemoved{root: r.rootB})
	}))

	if pin := r.store.State().Tab().Pin; pin == nil || pin.Root != r.rootB {
		t.Errorf("Pin = %+v, want it kept", pin)
	}

	if strings.Contains(r.notice, "git repository") {
		t.Errorf("notice = %q", r.notice)
	}

	if _, ok := r.paneRoot(r.id); !ok {
		t.Error("the agent's own watch lost its root")
	}

	if got := ansi.Strip(r.drawn(t)); !strings.Contains(got, "worktree removed: ") {
		t.Errorf("drawer reads\n%s\nwant the removed row", got)
	}
}

// A pin chosen again while the first one's watch is still being taken ends up
// with one reference, on the root that is pinned.
func TestPinRetargetWhileAcquiring(t *testing.T) {
	t.Parallel()
	r := newFollowRig(t, "")
	r.open(t, r.id)

	acquired := map[string]pinAcquiredMsg{}

	feed := func(m any) {
		if a, ok := m.(pinAcquiredMsg); ok {
			acquired[a.root] = a

			return
		}

		r.feed(t, m)
	}

	// Hold the answers of the watch acquisitions back.
	hold := func(cond func() bool) {
		t.Helper()

		deadline := time.Now().Add(r.wait)

		for !cond() {
			select {
			case m := <-r.pm.msgs:
				feed(m)
			case <-time.After(10 * time.Millisecond):
				r.settle()
			}

			if time.Now().After(deadline) {
				t.Fatal("timed out holding the pin's answers")
			}
		}
	}

	// b's acquisition is still running when the pin moves to C.
	release := r.fake.watches.holdAcquire()
	t.Cleanup(release)

	for _, root := range []string{r.rootB, r.rootC} {
		r.pinTo(t, root)

		v := r.view()
		r.epochRoot[v.epoch] = v.root
	}

	release()
	hold(func() bool { return len(acquired) == 2 })

	// The answer for b comes last, after the pin moved to C.
	r.feed(t, acquired[r.rootC])
	r.feed(t, acquired[r.rootB])

	r.until(t, "b's reference to be released", func() bool { return !r.watched(r.rootB) })

	if pw := r.watches.pins[r.tabID()]; pw == nil || pw.root != r.rootC || pw.release == nil || !r.watched(r.rootC) {
		t.Errorf("pin watch = %+v, want one reference on %q", pw, r.rootC)
	}
}
