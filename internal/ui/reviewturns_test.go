package ui

import (
	"fmt"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/mhersson/aim/internal/core"
	"github.com/mhersson/aim/internal/git"
	"github.com/mhersson/aim/internal/harness"
)

func (r *drawerRig) coreMode() core.ReviewMode { return r.store.State().Tab().Review.Mode }

func isBranchSource(v *reviewView) bool {
	_, ok := v.src.(git.BranchSource)

	return ok
}

// A session that ends and another that starts, as /clear sends them, leave the
// drawer's turn mode alone: it shows the branch until the new session has a
// turn, then that turn.
func TestSessionEndAndStartKeepTurnMode(t *testing.T) {
	t.Parallel()
	f := newFakeGit(t)
	r := newDrawerRig(t, gitOf(f), turnFirst)
	rp := threeTurnRepo(t, f)
	dir := rp.Root()
	root := dir
	id := r.agent(t, dir, testSession)

	v := r.open(t, id)
	r.watching(t, id, root)

	// The mode row is the tree's first row.
	r.showTree(t)

	if _, ok := v.src.(git.TurnSource); !ok {
		t.Fatalf("setup: source = %T, want the turn source", v.src)
	}

	branchShown := func(what string) {
		t.Helper()

		r.until(t, what, r.viewHolds(func(v *reviewView) bool { return isBranchSource(v) && v.loaded && !v.loading }))

		v := r.view()

		if r.coreMode() != core.ReviewTurn || v.err != nil {
			t.Errorf("%s: core mode %q err %v, want turn mode kept and no error", what, r.coreMode(), v.err)
		}

		if got := r.drawn(t); !strings.Contains(got, "branch vs main") {
			t.Errorf("%s: the mode row does not read branch:\n%s", what, got)
		}
	}

	r.run(r.step(hookMsg{ev: harness.Event{Pane: id, Name: "SessionEnd", End: true}}))
	branchShown("the branch after the session ended")

	r.run(r.step(hookMsg{ev: harness.Event{Pane: id, Name: "SessionStart", Session: otherSession, Start: true}}))
	r.until(t, "the new session", r.viewHolds(func(v *reviewView) bool { return v.session == string(otherSession) && v.loaded && !v.loading }))
	branchShown("the branch while the new session has no turns")

	// The new session's first prompt reaches the app before its snapshot exists.
	snapshot := r.turnStartFirst(t, id)

	rp.turn(string(otherSession))
	write(t, filepath.Join(dir, "x.txt"), "after the first prompt\n")
	snapshot()
	r.until(t, "the new session's turn", r.viewHolds(func(v *reviewView) bool {
		ts, ok := v.src.(git.TurnSource)

		return ok && ts.Session == string(otherSession) && v.loaded && !v.loading
	}))

	v = r.view()
	if got := viewPaths(v); !slices.Equal(got, []string{"x.txt"}) || v.turns != 1 || r.coreMode() != core.ReviewTurn {
		t.Errorf("shows %v of %d turns in %q mode, want x.txt of 1 in turn mode", got, v.turns, r.coreMode())
	}
}

// A turn drawer whose session has no turns yet shows the branch, and the turn
// keys say why they do nothing.
func TestNoTurnsYetNotices(t *testing.T) {
	t.Parallel()
	f := newFakeGit(t)
	r := newDrawerRig(t, gitOf(f))
	dir := editedRepo(t, f).Root()
	id := r.agent(t, dir, testSession)

	// A restored drawer in turn mode, on a session with no turns.
	openIn(t, r.App, core.OpenReview{Pane: id, Mode: core.ReviewTurn})
	r.settle()

	v := r.idle(t)

	if !isBranchSource(v) || v.err != nil || r.coreMode() != core.ReviewTurn {
		t.Fatalf("source %T err %v core mode %q, want the branch shown and no error in turn mode", v.src, v.err, r.coreMode())
	}

	keys := map[string]func() tea.Cmd{
		"t": func() tea.Cmd { return r.setReviewMode(core.ReviewTurn) },
		",": func() tea.Cmd { return r.stepReviewTurn(-1) },
		".": func() tea.Cmd { return r.stepReviewTurn(1) },
	}

	for key, press := range keys {
		r.notice = ""
		r.run(press())

		if want := noticeNoTurnsYet; r.notice != want {
			t.Errorf("%s: notice = %q, want %q", key, r.notice, want)
		}

		if v.loading {
			t.Errorf("%s started a load", key)
		}
	}

	// b still works, and a pane with no session keeps its own notice.
	r.run(r.setReviewMode(core.ReviewBranch))
	r.idle(t)

	if r.coreMode() != core.ReviewBranch {
		t.Errorf("b left core mode %q", r.coreMode())
	}

	openIn(t, r.App, core.SetAgentSession{ID: id})
	r.settle()

	r.notice = ""
	r.run(r.setReviewMode(core.ReviewTurn))

	if want := noticeNoTurns; r.notice != want {
		t.Errorf("t without a session: notice = %q, want %q", r.notice, want)
	}
}

// turnStartFirst sends a turn start as the gate orders it in production: before
// the turn's snapshot exists. The drawer must not load until the returned
// function has closed the event's Snapped channel.
func (r *drawerRig) turnStartFirst(t *testing.T, id core.PaneID) (snapshotDone func()) {
	t.Helper()

	snapped := make(chan struct{})
	v := r.view()
	epoch := v.epoch

	r.run(r.step(hookMsg{ev: harness.Event{Pane: id, Name: "UserPromptSubmit", TurnStart: true, State: core.AgentWorking, Snapped: snapped}}))

	if v.loading || v.epoch != epoch {
		t.Fatalf("loading = %v, epoch %d -> %d: the drawer loaded before the turn's snapshot existed", v.loading, epoch, v.epoch)
	}

	return func() { close(snapped) }
}

// A turn's snapshot ref never wakes the watcher, so the turn's start event
// recomputes the drawer, once the turn's snapshot exists. A turn that has
// changed files moves the count and the mode row; a past turn only learns the
// new count.
func TestTurnStartRefreshesTheDrawer(t *testing.T) {
	t.Parallel()
	t.Run("a turn that changes files", func(t *testing.T) {
		t.Parallel()
		f := newFakeGit(t)
		r := newDrawerRig(t, gitOf(f), turnFirst)
		rp := threeTurnRepo(t, f)
		dir := rp.Root()
		id := r.agent(t, dir, testSession)

		r.open(t, id)
		r.watching(t, id, dir)

		// The mode row is the tree's first row.
		r.showTree(t)

		snapshotDone := r.turnStartFirst(t, id)

		rp.turn(string(testSession))
		write(t, filepath.Join(dir, "four.txt"), "turn four\n")
		snapshotDone()
		r.until(t, "turn 4", r.viewHolds(func(v *reviewView) bool { return v.turns == 4 && v.res.Turn == 4 && !v.loading }))

		got := r.drawn(t)
		for _, want := range []string{"turn 4 of 4, live", "four.txt"} {
			if !strings.Contains(got, want) {
				t.Errorf("the drawer lacks %q:\n%s", want, got)
			}
		}
	})

	t.Run("a turn that changes nothing yet", func(t *testing.T) {
		t.Parallel()
		f := newFakeGit(t)
		r := newDrawerRig(t, gitOf(f), turnFirst)
		rp := threeTurnRepo(t, f)
		id := r.agent(t, rp.Root(), testSession)

		r.open(t, id)
		r.watching(t, id, rp.Root())

		snapshotDone := r.turnStartFirst(t, id)

		rp.turn(string(testSession))
		snapshotDone()
		r.until(t, "turn 3 closed", r.viewHolds(func(v *reviewView) bool { return v.turns == 3 && v.res.Turn == 3 && !v.res.Live && !v.loading }))
	})

	t.Run("a past turn", func(t *testing.T) {
		t.Parallel()
		f := newFakeGit(t)
		r := newDrawerRig(t, gitOf(f), turnFirst)
		rp := threeTurnRepo(t, f)
		dir := rp.Root()
		id := r.agent(t, dir, testSession)

		r.open(t, id)
		r.watching(t, id, dir)
		r.showTree(t)
		r.run(r.stepReviewTurn(-1))

		v := r.idle(t)
		gen := v.gen

		snapshotDone := r.turnStartFirst(t, id)

		rp.turn(string(testSession))
		write(t, filepath.Join(dir, "four.txt"), "turn four\n")
		snapshotDone()
		r.until(t, "the new count", func() bool { return v.turns == 4 && !v.loading })

		if v.res.Turn != 2 || v.gen != gen {
			t.Errorf("a past turn reloaded: turn %d, generation %d -> %d", v.res.Turn, gen, v.gen)
		}

		if got := r.drawn(t); !strings.Contains(got, "turn 2 of 4") {
			t.Errorf("the mode row does not read turn 2 of 4:\n%s", got)
		}
	})

	// The wait for the snapshot ends with the app, so no command outlives it.
	t.Run("the app ending ends the wait", func(t *testing.T) {
		t.Parallel()
		f := newFakeGit(t)
		r := newDrawerRig(t, gitOf(f))
		id := r.agent(t, threeTurnRepo(t, f).Root(), testSession)
		r.open(t, id)

		cmd := r.reviewHook(id, true, make(chan struct{}))
		if cmd == nil {
			t.Fatal("a turn start with a snapshot to wait for returned no command")
		}

		answered := make(chan tea.Msg, 1)

		go func() { answered <- cmd() }()

		r.cancel()

		select {
		case m := <-answered:
			if m != nil {
				t.Errorf("the wait answered %T after the app ended, want nothing", m)
			}
		case <-time.After(5 * time.Second):
			t.Fatal("the wait outlived the app")
		}
	})
}

// A drawer on a pane that moved to another repository whose watch cannot start
// holds nothing for the old repository, and hook events refresh it as they do
// any drawer without a watch.
func TestFailedWatchOfANewRepositoryFallsBackToHooks(t *testing.T) {
	t.Parallel()
	t.Run("a failed acquire releases the old watch", func(t *testing.T) {
		t.Parallel()
		f := newFakeGit(t)
		r := newDrawerRig(t, gitOf(f))
		first, second := editedRepo(t, f).Root(), editedRepo(t, f).Root()
		id := r.agent(t, first, "")

		r.open(t, id)
		r.watching(t, id, first)

		// A closed set refuses every Acquire.
		f.watches.close()

		openIn(t, r.App, core.SetPaneCwd{ID: id, Cwd: second})
		r.settle()
		r.until(t, "the old reference to be released", func() bool { return r.held(id) == "" && r.holdCount() == 0 })

		if w := r.watches.panes[id]; w.want != second {
			t.Errorf("wants %q, want the new repository %q", w.want, second)
		}

		v := r.idle(t)

		r.run(r.step(hookMsg{ev: harness.Event{Pane: id, Name: "Stop", State: core.AgentIdle}}))

		if !v.loading {
			t.Error("a hook event did not refresh the drawer of the repository with no watch")
		}
	})

	t.Run("the old root's watch does not count while another root is wanted", func(t *testing.T) {
		t.Parallel()
		f := newFakeGit(t)
		r := newDrawerRig(t, gitOf(f))
		dir := editedRepo(t, f).Root()
		id := r.agent(t, dir, "")

		v := r.open(t, id)
		r.watching(t, id, dir)

		// The pane moved on, so the drawer is on the new root, whose watch has not answered yet.
		r.watches.panes[id].want = "/another/root"
		v.root = "/another/root"

		r.run(r.reviewHook(id, false, nil))

		if !v.loading {
			t.Error("a hook event did not refresh while the held watch is for another root")
		}
	})
}

// A hook event for a watch-less drawer in a hidden tab marks it stale, as a
// batch would, so it recomputes when its tab shows.
func TestHookEventMarksAHiddenDrawerStale(t *testing.T) {
	t.Parallel()
	f := newFakeGit(t)
	r := newDrawerRig(t, gitOf(f), func(o *Options) { o.Watches = nil })
	dir := editedRepo(t, f).Root()
	id := r.agent(t, dir, "")

	v := r.open(t, id)
	gen := v.gen

	openIn(t, r.App, core.NewTab{Kind: core.KindShell, Cwd: t.TempDir()})
	r.settle()

	write(t, filepath.Join(dir, "f.txt"), editedF(2))
	r.run(r.step(hookMsg{ev: harness.Event{Pane: id, Name: "Stop", State: core.AgentIdle}}))

	if v.loading || v.queued == nil || v.gen != gen {
		t.Fatalf("hidden drawer: loading=%v queued=%v gen %d -> %d, want it marked stale and left alone", v.loading, v.queued, gen, v.gen)
	}

	openIn(t, r.App, core.SelectTab{Index: 0})
	r.settle()
	r.until(t, "the drawer to recompute", func() bool { return v.gen > gen && !v.loading })

	if got := v.res.Files[0].Hunks[0].NewStart; got != 1 {
		t.Errorf("the first hunk starts at line %d, want 1 (the saved file)", got)
	}
}

// A new session makes the turn count the ended session's: the turn keys wait
// for the new session's count instead of trusting it. With no turns they say
// so, and core stays in branch mode.
func TestTurnKeysDoNotTrustTheEndedSessionsCount(t *testing.T) {
	t.Parallel()
	f := newFakeGit(t)
	r := newDrawerRig(t, gitOf(f))
	dir := threeTurnRepo(t, f).Root()
	root := dir
	id := r.agent(t, dir, testSession)

	r.open(t, id)
	r.watching(t, id, root)
	r.run(r.setReviewMode(core.ReviewBranch))

	if v := r.idle(t); v.turns != 3 {
		t.Fatalf("setup: %d turns, want the ended session's 3", v.turns)
	}

	// The /clear order, with the key pressed before anything refreshed the count.
	r.run(r.step(hookMsg{ev: harness.Event{Pane: id, Name: "SessionEnd", End: true}}))
	r.run(r.step(hookMsg{ev: harness.Event{Pane: id, Name: "SessionStart", Session: otherSession, Start: true}}))

	r.notice = ""
	r.run(r.setReviewMode(core.ReviewTurn))
	r.until(t, "the notice", func() bool { return r.notice != "" })

	if want := noticeNoTurnsYet; r.notice != want {
		t.Errorf("notice = %q, want %q", r.notice, want)
	}

	if r.coreMode() != core.ReviewBranch {
		t.Errorf("core mode = %q, want branch kept", r.coreMode())
	}
}

// The same wait ends in the switch when the new session has turns.
func TestTurnKeysSwitchToANewSessionThatHasTurns(t *testing.T) {
	t.Parallel()
	f := newFakeGit(t)
	r := newDrawerRig(t, gitOf(f))
	rp := threeTurnRepo(t, f)
	dir := rp.Root()
	root := dir

	rp.turn(string(otherSession))
	write(t, filepath.Join(dir, "other.txt"), "the other session's change\n")
	id := r.agent(t, dir, testSession)

	r.open(t, id)
	r.watching(t, id, root)
	r.run(r.setReviewMode(core.ReviewBranch))
	r.idle(t)

	r.run(r.step(hookMsg{ev: harness.Event{Pane: id, Name: "SessionEnd", End: true}}))
	r.run(r.step(hookMsg{ev: harness.Event{Pane: id, Name: "SessionStart", Session: otherSession, Start: true}}))

	r.notice = ""
	r.run(r.setReviewMode(core.ReviewTurn))
	r.until(t, "the new session's turn", r.viewHolds(func(v *reviewView) bool {
		ts, ok := v.src.(git.TurnSource)

		return ok && ts.Session == string(otherSession) && v.loaded && !v.loading
	}))

	if r.notice != "" || r.coreMode() != core.ReviewTurn || r.view().turns != 1 {
		t.Errorf("notice %q core mode %q turns %d, want no notice, turn mode and 1 turn", r.notice, r.coreMode(), r.view().turns)
	}
}

// An answer whose probe ran for another session does not set the count.
func TestAnswerForAnotherSessionDoesNotSetTheCount(t *testing.T) {
	t.Parallel()
	f := newFakeGit(t)
	r := newDrawerRig(t, gitOf(f))
	id := r.agent(t, threeTurnRepo(t, f).Root(), testSession)

	v := r.open(t, id)
	tab := r.store.State().Tab().ID

	r.run(r.step(hookMsg{ev: harness.Event{Pane: id, Name: "SessionEnd", End: true}}))
	r.run(r.step(hookMsg{ev: harness.Event{Pane: id, Name: "SessionStart", Session: otherSession, Start: true}}))
	r.step(reviewLoadedMsg{tab: tab, gen: v.epoch, session: string(testSession), info: reviewInfo{turns: 7}})

	if v.turns == 7 {
		t.Error("the count of the ended session was adopted for the new one")
	}
}

// A turn key held for the turn count gives way to the user's next key: t then
// b, inside the one probe, ends in branch mode.
func TestLaterKeyReplacesTheHeldTurnKey(t *testing.T) {
	t.Parallel()
	f := newFakeGit(t)
	r := newDrawerRig(t, gitOf(f))
	rp := threeTurnRepo(t, f)
	dir := rp.Root()
	root := dir

	rp.turn(string(otherSession))

	id := r.agent(t, dir, testSession)

	r.open(t, id)
	r.watching(t, id, root)
	r.run(r.setReviewMode(core.ReviewBranch))
	r.idle(t)

	r.run(r.step(hookMsg{ev: harness.Event{Pane: id, Name: "SessionEnd", End: true}}))
	r.run(r.step(hookMsg{ev: harness.Event{Pane: id, Name: "SessionStart", Session: otherSession, Start: true}}))

	// Both keys come before any answer is delivered.
	r.run(r.setReviewMode(core.ReviewTurn))
	r.run(r.setReviewMode(core.ReviewBranch))
	r.until(t, "the probe and the diff to settle", r.viewHolds(func(v *reviewView) bool { return !v.loading && v.turnsFor == string(otherSession) }))

	if v := r.view(); r.coreMode() != core.ReviewBranch || !isBranchSource(v) || v.after != nil {
		t.Errorf("core mode %q source %T held key %v, want branch with no key held", r.coreMode(), v.src, v.after != nil)
	}
}

// A turn key is held only while a probe can answer it. Outside a repository
// there is none, so the key gets its notice at once.
func TestTurnKeyOutsideARepositoryAnswersAtOnce(t *testing.T) {
	t.Parallel()
	r := newDrawerRig(t, gitOf(newFakeGit(t)))
	id := r.agent(t, t.TempDir(), testSession)

	openIn(t, r.App, core.OpenReview{Pane: id, Mode: core.ReviewTurn})
	r.settle()
	r.until(t, "the error", r.viewHolds(func(v *reviewView) bool { return v.err != nil }))

	r.notice = ""

	cmd := r.setReviewMode(core.ReviewTurn)

	if want := noticeNoTurnsYet; r.notice != want || cmd != nil || r.view().after != nil {
		t.Errorf("notice %q command %v held key %v, want the notice at once and nothing held", r.notice, cmd != nil, r.view().after != nil)
	}
}

// A new session drops a held turn key: it was pressed for the old one.
func TestNewSessionDropsAHeldTurnKey(t *testing.T) {
	t.Parallel()
	f := newFakeGit(t)
	r := newDrawerRig(t, gitOf(f))
	rp := threeTurnRepo(t, f)
	dir := rp.Root()
	root := dir

	rp.turn(string(otherSession))

	id := r.agent(t, dir, testSession)

	r.open(t, id)
	r.watching(t, id, root)
	r.run(r.setReviewMode(core.ReviewBranch))
	r.idle(t)

	r.run(r.step(hookMsg{ev: harness.Event{Pane: id, Name: "SessionEnd", End: true}}))
	r.run(r.step(hookMsg{ev: harness.Event{Pane: id, Name: "SessionStart", Session: otherSession, Start: true}}))
	r.run(r.setReviewMode(core.ReviewTurn))

	if r.view().after == nil {
		t.Fatal("setup: the key was not held")
	}

	// The session ends before the probe answers.
	r.notice = ""
	r.run(r.step(hookMsg{ev: harness.Event{Pane: id, Name: "SessionEnd", End: true}}))

	if r.view().after != nil {
		t.Error("the held key outlived the session it was pressed for")
	}

	r.until(t, "the loads to settle", r.viewHolds(func(v *reviewView) bool { return !v.loading }))

	if r.notice != "" || r.coreMode() != core.ReviewBranch {
		t.Errorf("notice %q core mode %q, want the dropped key to do nothing", r.notice, r.coreMode())
	}
}

// newTurnRig is a follow rig whose agent has one turn of its session, taken in
// A's main checkout (the agent's own directory) and followed there by a new
// file, and five uncommitted lines in b that only the branch shows.
func newTurnRig(t *testing.T, opts ...func(*Options)) *followRig {
	t.Helper()

	r := newFollowRig(t, testSession, opts...)
	r.repoA.turn(string(testSession))
	write(t, filepath.Join(r.dirA, "x.txt"), "after the turn\n")
	r.addLines(t, filepath.Join(r.dirB, "a.txt"))

	return r
}

// settledOn is a condition: the drawer shows a loaded turn, or the branch, of root.
func (r *followRig) settledOn(root string, turn bool) func() bool {
	return r.viewHolds(func(v *reviewView) bool {
		_, isTurn := v.src.(git.TurnSource)

		return v.root == root && v.loaded && !v.loading && isTurn == turn
	})
}

func TestNoTurnsNoticeCleansNames(t *testing.T) {
	t.Parallel()
	r := newDrawerRig(t)
	tab := r.store.State().Tab().ID
	v := &reviewView{loaded: true, foreign: true, turnsFor: string(testSession), project: "my\x07repo", branch: "feat\u200b/x"}

	if ok, cmd := r.turnsGate(tab, v, string(testSession), nil); ok || cmd != nil {
		t.Fatalf("turnsGate() = %v, %v, want the key stopped", ok, cmd != nil)
	}

	if want := fmt.Sprintf(noticeNoTurnsIn, "myrepo", "feat/x"); r.notice != want {
		t.Errorf("notice = %q, want %q", r.notice, want)
	}

	// A pane with no session keeps its own notice.
	r.notice = ""
	r.turnsGate(tab, v, "", nil)

	if want := noticeNoTurns; r.notice != want {
		t.Errorf("notice = %q, want %q", r.notice, want)
	}
}

// A move of the own directory inside the shown repository neither reloads the
// diff nor changes what it shows.
func TestOwnDirectoryMovingInsideTheRepositoryKeepsTheDiff(t *testing.T) {
	t.Parallel()
	r := newTurnRig(t, turnFirst)
	sub := filepath.Join(r.dirA, "pkg")
	write(t, filepath.Join(sub, "p.txt"), "p\n")

	v := r.open(t, r.id)
	epoch, gen := v.epoch, v.gen

	r.hookNow(cwdHook(r.id, sub))
	r.until(t, "the own directory to be seen", func() bool { return v.own == sub && !v.loading })
	r.until(t, "every answer the move raised to land", r.landed)

	if v != r.view() || v.epoch != epoch || v.gen != gen || v.foreign || !isTurnSource(v) {
		t.Errorf("the drawer reloaded: epoch %d -> %d, generation %d -> %d, foreign %v, source %T", epoch, v.epoch, gen, v.gen, v.foreign, v.src)
	}
}

func isTurnSource(v *reviewView) bool {
	_, ok := v.src.(git.TurnSource)

	return ok
}

// A turn is listed in the worktree it was taken in: after the agent's own
// directory moved into b, A's turn is not a turn of b.
func TestOwnDirectoryMovedListsOnlyItsWorktreesTurns(t *testing.T) {
	t.Parallel()
	r := newTurnRig(t, turnFirst)
	r.open(t, r.id)

	r.hookNow(cwdHook(r.id, r.dirB))
	r.until(t, "the drawer to show b", r.viewHolds(func(v *reviewView) bool { return v.root == r.rootB && v.loaded && !v.loading }))

	if v := r.view(); v.turns != 0 || isTurnSource(v) || r.coreMode() != core.ReviewTurn {
		t.Errorf("b has %d turns, source %T, core mode %q, want none, the branch and turn mode kept", v.turns, v.src, r.coreMode())
	}

	r.notice = ""
	r.run(r.stepReviewTurn(-1))
	r.idle(t)

	if r.notice != noticeNoTurnsYet {
		t.Errorf("notice %q, want %q", r.notice, noticeNoTurnsYet)
	}
}

// A session that moved worktrees keeps each turn with the worktree it began in.
func TestPastTurnOfAMovedSessionStaysInItsWorktree(t *testing.T) {
	t.Parallel()
	r := newFollowRig(t, testSession, turnFirst)

	const lineOfA = "only in worktree A"

	r.repoA.turn(string(testSession))
	write(t, filepath.Join(r.dirA, "a.txt"), "one\n"+lineOfA+"\n")
	r.open(t, r.id)

	r.hookNow(cwdHook(r.id, r.dirB))
	r.until(t, "the drawer to show b", r.viewHolds(func(v *reviewView) bool { return v.root == r.rootB && v.loaded && !v.loading }))

	write(t, filepath.Join(r.dirB, "b.txt"), "changed in b\n")

	done := r.turnStartFirst(t, r.id)

	r.repoB.turn(string(testSession))
	done()

	write(t, filepath.Join(r.dirB, "after.txt"), "after the turn\n")
	r.until(t, "b's turn", r.viewHolds(func(v *reviewView) bool { return v.turns > 0 && isTurnSource(v) && !v.loading }))

	got := r.drawn(t)
	if !strings.Contains(got, "turn 1/1") || strings.Contains(got, lineOfA) {
		t.Errorf("the drawer should read turn 1/1 without A's line:\n%s", got)
	}

	r.notice = ""
	r.run(r.stepReviewTurn(-1))
	r.idle(t)

	if want := noticeFirstTurn; r.notice != want {
		t.Errorf("notice %q, want %q", r.notice, want)
	}

	if got := r.drawn(t); strings.Contains(got, lineOfA) {
		t.Errorf("a row holds A's line:\n%s", got)
	}
}

// A turn that changed nothing is not one of the worktree's turns: the drawer
// shows the one before it until a file changes.
func TestQuestionTurnIsNotListed(t *testing.T) {
	t.Parallel()
	f := newFakeGit(t)
	r := newDrawerRig(t, gitOf(f), turnFirst)
	rp := f.repo(t, "")
	dir := rp.Root()
	write(t, filepath.Join(dir, "a.txt"), "a\n")
	rp.commit("init")

	rp.turn(string(testSession))
	write(t, filepath.Join(dir, "a.txt"), "turn one\n")
	rp.turn(string(testSession))

	root := dir
	id := r.agent(t, dir, testSession)

	v := r.open(t, id)
	r.watching(t, id, root)

	if v.turns != 1 || v.res.Turn != 1 || v.res.Live {
		t.Fatalf("turns %d shown %d live %v, want turn 1 of 1, not live", v.turns, v.res.Turn, v.res.Live)
	}

	if got, want := modeRow(v), "turn 1 of 1 in this worktree"; got != want {
		t.Errorf("mode row = %q, want %q", got, want)
	}

	if got := r.drawn(t); !strings.Contains(got, "turn 1/1") {
		t.Errorf("the tag does not read turn 1/1:\n%s", got)
	}

	write(t, filepath.Join(dir, "b.txt"), "turn two\n")
	r.run(r.batch(root, git.Batch{Paths: []string{"b.txt"}}))
	r.until(t, "turn 2", r.viewHolds(func(v *reviewView) bool { return v.turns == 2 && v.res.Turn == 2 && v.res.Live && !v.loading }))

	if got := r.drawn(t); !strings.Contains(got, "turn 2/2") {
		t.Errorf("the tag does not read turn 2/2:\n%s", got)
	}

	if got, want := modeRow(r.view()), "turn 2 of 2 in this worktree, live"; got != want {
		t.Errorf("mode row = %q, want %q", got, want)
	}
}

// A drawer on a past turn recounts on a batch of plain paths: the open turn may
// have started to differ from the working tree. The turn shown stays.
func TestPastTurnViewRecountsOnPathBatches(t *testing.T) {
	t.Parallel()
	f := newFakeGit(t)
	r := newDrawerRig(t, gitOf(f), turnFirst)
	rp := threeTurnRepo(t, f)
	dir := rp.Root()
	root := dir
	id := r.agent(t, dir, testSession)

	r.open(t, id)
	r.watching(t, id, root)
	r.run(r.stepReviewTurn(-1))

	v := r.idle(t)
	gen := v.gen

	rp.turn(string(testSession))
	write(t, filepath.Join(dir, "four.txt"), "turn four\n")
	r.run(r.batch(root, git.Batch{Paths: []string{"four.txt"}}))
	r.until(t, "the new count", func() bool { return v.turns == 4 && !v.loading })

	if v.res.Turn != 2 || v.gen != gen {
		t.Errorf("the shown turn moved: turn %d, generation %d -> %d", v.res.Turn, gen, v.gen)
	}

	if got := r.drawn(t); !strings.Contains(got, "turn 2/4") {
		t.Errorf("the tag does not read turn 2/4:\n%s", got)
	}
}

// endTurn ends the session's open turn in r's worktree at the disk, as the end
// of a turn does, without starting the next.
func endTurn(r *fakeRepo, session core.SessionID) {
	r.f.edit(func() {
		var last *fakeTurn

		for _, t := range r.c.turns[string(session)] {
			if t.root == r.root {
				last = t
			}
		}

		last.endTree, last.ended = r.f.readDiskL(r), true
		last.endID = fakeOID("turn end", string(session), r.root, strconv.Itoa(last.n))
		r.c.aimRefs[fakeTurnRef(string(session), r.root, last.n, true)] = last.endID
	})
}

// newTwoTurnRig is a follow rig whose session has turn 1 in A, ended after a
// new x.txt, and turn 2 in b, open with five uncommitted lines in a.txt.
func newTwoTurnRig(t *testing.T, opts ...func(*Options)) *followRig {
	t.Helper()

	r := newFollowRig(t, testSession, opts...)

	r.repoA.turn(string(testSession))
	write(t, filepath.Join(r.dirA, "x.txt"), "turn one\n")
	endTurn(r.repoA, testSession)

	r.repoB.turn(string(testSession))
	r.addLines(t, filepath.Join(r.dirB, "a.txt"))

	return r
}

// modeRow is the mode row's text for the view as it loaded.
func modeRow(v *reviewView) string {
	_, word, rest := modeParts(v, false, false)

	return word + rest
}

func TestTurnModeInAFollowedWorktree(t *testing.T) {
	t.Parallel()
	r := newTwoTurnRig(t, turnFirst)

	v := r.open(t, r.id)
	if got := viewPaths(v); !slices.Equal(got, []string{"x.txt"}) || v.turns != 1 || v.res.Live || v.foreign {
		t.Fatalf("A shows %v as turn %d of %d (live %v), want x.txt in its only turn, not live", got, v.res.Turn, v.turns, v.res.Live)
	}

	if got := r.drawn(t); !strings.Contains(got, "turn 1/1") {
		t.Errorf("A's tag does not read turn 1/1:\n%s", got)
	}

	if got, want := modeRow(v), "turn 1 of 1 in this worktree"; got != want {
		t.Errorf("A's mode row = %q, want %q", got, want)
	}

	r.edit(filepath.Join(r.dirB, "a.txt"))
	r.until(t, "b's turn", r.settledOn(r.rootB, true))

	v = r.view()
	if got := viewPaths(v); !slices.Equal(got, []string{"a.txt"}) || v.res.Turn != 1 || !v.res.Live || v.turns != 1 || !v.foreign {
		t.Errorf("b shows %v as turn %d (live %v) of %d turns, want a.txt as its live turn 1 of 1", got, v.res.Turn, v.res.Live, v.turns)
	}

	if got, want := modeRow(v), "turn 1 of 1 in this worktree, live"; got != want {
		t.Errorf("b's mode row = %q, want %q", got, want)
	}

	r.showTree(t)

	if got := r.drawn(t); !strings.Contains(got, "turn 1/1") || !strings.Contains(got, "turn 1 of 1, live") {
		t.Errorf("b's drawer does not read turn 1/1, live:\n%s", got)
	}
}

func TestTurnModeInAPinnedWorktree(t *testing.T) {
	t.Parallel()
	r := newTwoTurnRig(t, turnFirst)

	r.open(t, r.id)
	r.followC(t)
	r.until(t, "C's branch", r.settledOn(r.rootC, false))

	r.pinTo(t, r.rootB)
	r.until(t, "b's turn", r.settledOn(r.rootB, true))

	v := r.view()
	if got := viewPaths(v); !slices.Equal(got, []string{"a.txt"}) || v.turns != 1 || r.coreMode() != core.ReviewTurn {
		t.Errorf("pinned b shows %v of %d turns in %q mode, want its turn with a.txt", got, v.turns, r.coreMode())
	}
}

func TestTurnKeysInAWorktreeWithoutTurns(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		rig  func(t *testing.T) (*followRig, string)
	}{
		{"another worktree", func(t *testing.T) (*followRig, string) {
			r := newTwoTurnRig(t, turnFirst)
			r.open(t, r.id)
			r.followC(t)
			r.until(t, "C's branch", r.settledOn(r.rootC, false))

			return r, "no turns in " + filepath.Base(r.dirC) + " · other yet"
		}},
		{"the agent's own worktree", func(t *testing.T) (*followRig, string) {
			r := newFollowRig(t, testSession, turnFirst)
			r.open(t, r.id)

			return r, "no turns yet"
		}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			r, want := tt.rig(t)
			v := r.view()

			keys := []struct {
				name  string
				press func() tea.Cmd
			}{
				{"t", func() tea.Cmd { return r.setReviewMode(core.ReviewTurn) }},
				{",", func() tea.Cmd { return r.stepReviewTurn(-1) }},
				{".", func() tea.Cmd { return r.stepReviewTurn(1) }},
			}

			for _, k := range keys {
				r.notice = ""
				epoch, gen, mode := v.epoch, v.gen, r.coreMode()

				cmd := k.press()

				if r.notice != want {
					t.Errorf("%s: notice = %q, want %q", k.name, r.notice, want)
				}

				if cmd != nil || v.loading || v.epoch != epoch || v.gen != gen || r.coreMode() != mode || !isBranchSource(v) {
					t.Errorf("%s changed something: command %v, loading %v, epoch %d -> %d, core mode %q, source %T", k.name, cmd != nil, v.loading, epoch, v.epoch, r.coreMode(), v.src)
				}
			}
		})
	}
}

// A move of the own directory changes only whether b is the agent's own
// worktree, so it recounts and keeps the diff.
func TestOwnDirectoryMoveRecountsOnly(t *testing.T) {
	t.Parallel()
	r := newTurnRig(t, turnFirst)
	r.open(t, r.id)
	r.edit(filepath.Join(r.dirB, "a.txt"))
	r.until(t, "the drawer to show b", r.settledOn(r.rootB, false))

	v := r.view()
	epoch, gen := v.epoch, v.gen
	notice := func(own string) string {
		r.hookNow(cwdHook(r.id, own))
		r.until(t, "the own directory to be seen", func() bool { return v.own == own && !v.loading })

		r.notice = ""
		r.run(r.setReviewMode(core.ReviewTurn))

		return r.notice
	}

	if got := notice(r.dirB); got != noticeNoTurnsYet {
		t.Errorf("b as the own worktree: notice %q, want %q", got, noticeNoTurnsYet)
	}

	if got, want := notice(r.dirA), fmt.Sprintf(noticeNoTurnsIn, filepath.Base(r.dirA), "b"); got != want {
		t.Errorf("b as another worktree: notice %q, want %q", got, want)
	}

	if v != r.view() || v.epoch != epoch || v.gen != gen {
		t.Errorf("the diff reloaded: epoch %d -> %d, generation %d -> %d", epoch, v.epoch, gen, v.gen)
	}
}

// A drawer opened on a followed worktree that has no turns shows the branch.
func TestOpenDrawerWhereTheFollowedWorktreeHasNoTurns(t *testing.T) {
	t.Parallel()
	r := newTwoTurnRig(t, turnFirst)
	r.followC(t)

	v := r.open(t, r.id)

	if got := r.coreMode(); got != core.ReviewBranch || isTurnSource(v) {
		t.Errorf("opened in %q mode with source %T, want branch mode on the branch source", got, v.src)
	}
}

func TestStepThroughAWorktreesTurns(t *testing.T) {
	t.Parallel()
	r := newFollowRig(t, testSession, turnFirst)

	r.repoB.turn(string(testSession))
	write(t, filepath.Join(r.dirB, "b1.txt"), "one\n")
	endTurn(r.repoB, testSession)

	r.repoA.turn(string(testSession))
	write(t, filepath.Join(r.dirA, "x.txt"), "A's turn\n")
	endTurn(r.repoA, testSession)

	r.repoB.turn(string(testSession))
	write(t, filepath.Join(r.dirB, "b2.txt"), "two\n")
	endTurn(r.repoB, testSession)

	r.repoB.turn(string(testSession))
	write(t, filepath.Join(r.dirB, "b3.txt"), "three\n")

	r.open(t, r.id)
	r.pinTo(t, r.rootB)
	r.until(t, "b's turns", r.settledOn(r.rootB, true))

	step := func(delta int, wantFiles []string, wantNotice string) {
		t.Helper()

		r.notice = ""
		r.run(r.stepReviewTurn(delta))
		r.idle(t)

		v := r.view()
		if got := viewPaths(v); !slices.Equal(got, wantFiles) || r.notice != wantNotice {
			t.Errorf("after %+d: %v as turn %d of %d, notice %q, want %v and notice %q", delta, got, v.res.Turn, v.turns, r.notice, wantFiles, wantNotice)
		}
	}

	if v := r.view(); v.turns != 3 || v.res.Turn != 3 || !slices.Equal(viewPaths(v), []string{"b3.txt"}) {
		t.Fatalf("setup: turn %d of %d showing %v, want b's live turn 3 of 3", v.res.Turn, v.turns, viewPaths(v))
	}

	step(-1, []string{"b2.txt"}, "")
	step(-1, []string{"b1.txt"}, "")
	step(-1, []string{"b1.txt"}, "turn 1 is the first turn")
	step(1, []string{"b2.txt"}, "")
	step(1, []string{"b3.txt"}, "")
	step(1, []string{"b3.txt"}, "turn 3 is the latest turn")
}

// Without an own repository the drawer still shows the turns of the worktree it
// follows.
func TestTurnModeWithoutAnOwnRepository(t *testing.T) {
	t.Parallel()
	r := newTwoTurnRig(t)

	r.edit(filepath.Join(r.dirB, "a.txt"))
	r.hookNow(cwdHook(r.id, t.TempDir()))

	openIn(t, r.App, core.OpenReview{Pane: r.id, Mode: core.ReviewTurn})
	r.settle()
	r.until(t, "b's turn", r.settledOn(r.rootB, true))

	v := r.view()
	if got := viewPaths(v); !slices.Equal(got, []string{"a.txt"}) || v.turns != 1 || !v.res.Live || r.coreMode() != core.ReviewTurn {
		t.Errorf("shows %v as turn %d of %d (live %v) in %q mode, want b's live turn with a.txt", got, v.res.Turn, v.turns, v.res.Live, r.coreMode())
	}
}

// A live load after the own directory moved off the shown root keeps updating
// the turn.
func TestLiveLoadAfterOwnDirectoryMovedKeepsTheTurn(t *testing.T) {
	t.Parallel()
	r := newFollowRig(t, testSession, turnFirst)

	// The turn's start holds m.txt changed. Since then m.txt is back to its
	// committed text and n.txt is changed, so the turn shows both files.
	write(t, filepath.Join(r.dirA, "m.txt"), "m\n")
	write(t, filepath.Join(r.dirA, "n.txt"), "n\n")
	r.repoA.commit("more files")
	write(t, filepath.Join(r.dirA, "m.txt"), "m in the turn\n")
	r.repoA.turn(string(testSession))
	write(t, filepath.Join(r.dirA, "m.txt"), "m\n")
	write(t, filepath.Join(r.dirA, "n.txt"), "n changed\n")

	v := r.open(t, r.id)
	if !isTurnSource(v) || !slices.Equal(viewPaths(v), []string{"m.txt", "n.txt"}) {
		t.Fatalf("setup: source %T shows %v, want the turn with m.txt and n.txt", v.src, viewPaths(v))
	}

	// An edit keeps the followed place on A whatever the own directory does.
	r.edit(filepath.Join(r.dirA, "n.txt"))
	r.until(t, "the edit's answers to land", r.landed)

	// The own directory moves while a load is in flight and a second batch
	// waits behind it, so the load that follows starts for the moved directory.
	r.run(r.batch(r.rootA, git.Batch{Paths: []string{"n.txt"}}))

	if !v.loading {
		t.Fatal("setup: no load in flight")
	}

	r.hookNow(cwdHook(r.id, r.dirB))
	r.run(r.batch(r.rootA, git.Batch{Paths: []string{"n.txt"}}))
	r.until(t, "the queued load", func() bool {
		v := r.view()

		return v.own == r.dirB && !v.loading && v.queued == nil
	})

	v = r.view()
	if got := viewPaths(v); !isTurnSource(v) || !v.foreign || !slices.Equal(got, []string{"m.txt", "n.txt"}) || !v.res.Live {
		t.Errorf("source %T foreign %v shows %v (live %v), want A's live turn with m.txt and n.txt", v.src, v.foreign, got, v.res.Live)
	}
}
