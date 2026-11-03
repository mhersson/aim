package ui

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/mhersson/aim/internal/core"
	"github.com/mhersson/aim/internal/git"
	"github.com/mhersson/aim/internal/harness"
)

func TestDrawerOpensInTurnMode(t *testing.T) {
	t.Parallel()
	f := newFakeGit(t)
	r := newDrawerRig(t, gitOf(f), turnFirst)
	dir := threeTurnRepo(t, f).Root()
	id := r.agent(t, dir, testSession)

	v := r.open(t, id)
	root := dir

	if rv := r.store.State().Tab().Review; rv == nil || rv.Pane != id || rv.Mode != core.ReviewTurn {
		t.Fatalf("Tab.Review = %+v, want turn mode on %s", rv, id)
	}

	if want := (git.TurnSource{Worktree: root, Session: string(testSession)}); v.src != want {
		t.Errorf("source = %+v, want %+v", v.src, want)
	}

	if got := viewPaths(v); !slices.Equal(got, []string{"dir/c.txt"}) || !v.res.Live || v.turns != 3 {
		t.Errorf("turn mode shows %v live=%v of %d turns, want the last turn's dir/c.txt of 3", got, v.res.Live, v.turns)
	}

	if v.project != filepath.Base(dir) || v.branch != "main" {
		t.Errorf("title = %q on %q, want %q on main", v.project, v.branch, filepath.Base(dir))
	}

	if !r.drawer.focused {
		t.Error("the opened drawer does not have the keyboard")
	}
}

func TestDrawerOpensInBranchForAShell(t *testing.T) {
	t.Parallel()
	f := newFakeGit(t)
	r := newDrawerRig(t, gitOf(f))
	rp := f.repo(t, "")
	dir := rp.Root()
	write(t, filepath.Join(dir, "a.txt"), "one\n")
	rp.commit("init")
	write(t, filepath.Join(dir, "a.txt"), "one\ntwo\n")

	id := r.shell(t, dir)
	v := r.open(t, id)

	if rv := r.store.State().Tab().Review; rv == nil || rv.Mode != core.ReviewBranch {
		t.Fatalf("Tab.Review = %+v, want branch mode", rv)
	}

	if want := (git.BranchSource{Worktree: dir}); v.src != want {
		t.Errorf("source = %+v, want %+v", v.src, want)
	}

	if got := viewPaths(v); !slices.Equal(got, []string{"a.txt"}) || v.base != "main" {
		t.Errorf("branch mode shows %v against %q, want a.txt against main", got, v.base)
	}

	r.run(r.setReviewMode(core.ReviewTurn))

	if r.notice != noticeNoTurns {
		t.Errorf("notice = %q, want %q", r.notice, noticeNoTurns)
	}

	if rv := r.store.State().Tab().Review; rv.Mode != core.ReviewBranch {
		t.Errorf("mode = %q after t on a shell, want branch", rv.Mode)
	}
}

func TestDrawerOpenModeFollowsTurnsAndConfig(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		session core.SessionID
		cfg     core.DiffConfig
		want    core.ReviewMode
	}{
		{"an agent with turns", testSession, core.DiffConfig{Mode: "turn"}, core.ReviewTurn},
		{"an agent without a session", "", core.DiffConfig{Mode: "turn"}, core.ReviewBranch},
		{"a session without turns", otherSession, core.DiffConfig{Mode: "turn"}, core.ReviewBranch},
		{"the config asks for branch", testSession, core.DiffConfig{Mode: "branch"}, core.ReviewBranch},
		{"the config leaves the mode unset", testSession, core.DiffConfig{}, core.ReviewBranch},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			f := newFakeGit(t)
			r := newDrawerRig(t, gitOf(f), func(o *Options) { o.Diff = tt.cfg })
			id := r.agent(t, threeTurnRepo(t, f).Root(), tt.session)

			r.open(t, id)

			if got := r.store.State().Tab().Review.Mode; got != tt.want {
				t.Errorf("opened in %q, want %q", got, tt.want)
			}
		})
	}
}

func TestNewDrawerOpensInBranchMode(t *testing.T) {
	t.Parallel()

	f := newFakeGit(t)
	r := newDrawerRig(t, gitOf(f), func(o *Options) { o.Diff = core.DefaultConfig().Diff })
	id := r.agent(t, threeTurnRepo(t, f).Root(), testSession)

	v := r.open(t, id)

	if got := r.store.State().Tab().Review.Mode; got != core.ReviewBranch {
		t.Fatalf("opened in %q, want branch", got)
	}

	if _, isTurn := v.src.(git.TurnSource); isTurn {
		t.Errorf("source = %+v in branch mode", v.src)
	}

	r.run(r.setReviewMode(core.ReviewTurn))

	if v := r.idle(t); r.store.State().Tab().Review.Mode != core.ReviewTurn || v.res.Turn != 3 {
		t.Errorf("t gives mode %q turn %d, want turn mode on the latest turn", r.store.State().Tab().Review.Mode, v.res.Turn)
	}
}

func TestDrawerNeedsARepository(t *testing.T) {
	t.Parallel()
	r := newDrawerRig(t, gitOf(newFakeGit(t)))
	id := r.shell(t, t.TempDir())

	r.run(r.openReview(id, true))
	r.until(t, "the notice", func() bool { return r.notice != "" })

	if r.notice != noticeNotRepo {
		t.Errorf("notice = %q, want %q", r.notice, noticeNotRepo)
	}

	if r.store.State().Tab().Review != nil {
		t.Error("a drawer opened outside a repository")
	}
}

// The check at opening is newer than an earlier finding that the cwd is in no
// repository: a repository made since is found.
func TestOpenFindsARepositoryMadeAfterTheCwdWasFound(t *testing.T) {
	t.Parallel()
	f := newFakeGit(t)
	r := newDrawerRig(t, gitOf(f))

	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}

	id := r.agent(t, dir, "")
	r.settle()
	r.until(t, "the cwd to be found outside a repository", func() bool { return r.watches.panes[id] != nil && r.watches.panes[id].notRepo })

	rp := f.repoAt(t, dir)
	write(t, filepath.Join(dir, "a.txt"), "one\n")
	rp.commit("init")
	write(t, filepath.Join(dir, "a.txt"), "one\ntwo\n")

	v := r.open(t, id)

	if got := viewPaths(v); !slices.Equal(got, []string{"a.txt"}) || v.err != nil {
		t.Errorf("shows %v with err %v, want a.txt", got, v.err)
	}
}

func TestDrawerWithoutAnEngineShowsTheError(t *testing.T) {
	t.Parallel()
	f := newFakeGit(t)
	r := newDrawerRig(t, gitOf(f), func(o *Options) { o.GitTempDir = "" })
	dir := editedRepo(t, f).Root()

	v := r.open(t, r.shell(t, dir))

	if !errors.Is(v.err, errDiffsOff) || len(v.res.Files) != 0 {
		t.Fatalf("err = %v with %d files, want the diffs-off error and no files", v.err, len(v.res.Files))
	}

	if got := r.drawn(t); !strings.Contains(got, errDiffsOff.Error()) {
		t.Errorf("the drawer does not say why it is empty:\n%s", got)
	}
}

func TestTurnStepping(t *testing.T) {
	t.Parallel()
	f := newFakeGit(t)
	r := newDrawerRig(t, gitOf(f), turnFirst)
	rp := threeTurnRepo(t, f)
	rp.missingTurn(string(testSession))

	id := r.agent(t, rp.Root(), testSession)
	v := r.open(t, id)

	// The mode row is the tree's first row.
	r.showTree(t)

	// The latest turn took no snapshot: it reads as such, with its two info rows.
	// Turn 3 stayed open behind it, so it has no snapshot either.
	if !v.noSnapshot || v.turns != 4 || v.err != nil || len(v.res.Files) != 0 {
		t.Fatalf("latest: noSnapshot=%v turns=%d err=%v files=%d, want a turn 4 of 4 without a snapshot",
			v.noSnapshot, v.turns, v.err, len(v.res.Files))
	}

	got := r.drawn(t)
	for _, want := range []string{"turn 4 of 4, no snapshot", "turn 4 has no snapshot", ", and . step through the turns; b shows the branch"} {
		if !strings.Contains(got, want) {
			t.Errorf("the drawer lacks %q:\n%s", want, got)
		}
	}

	step := func(delta, turn int, noSnap bool, files ...string) {
		t.Helper()

		r.run(r.stepReviewTurn(delta))
		v := r.idle(t)

		if v.shownTurn() != turn || v.noSnapshot != noSnap || (!noSnap && v.res.Turn != turn) || !slices.Equal(viewPaths(v), files) {
			t.Errorf("after stepping %d: turn %d (res %d) no snapshot %v files %v, want turn %d no snapshot %v files %v",
				delta, v.shownTurn(), v.res.Turn, v.noSnapshot, viewPaths(v), turn, noSnap, files)
		}
	}

	step(-1, 3, true)
	step(-1, 2, false, "b.txt", "d.txt", "new.txt")
	step(-1, 1, false, "a.txt")

	r.notice = ""
	r.run(r.stepReviewTurn(-1))

	if r.notice != noticeFirstTurn {
		t.Errorf("notice = %q, want %q", r.notice, noticeFirstTurn)
	}

	if v := r.view(); v.res.Turn != 1 || v.loading {
		t.Errorf("the first turn's end reloaded: turn %d loading=%v", v.res.Turn, v.loading)
	}

	step(1, 2, false, "b.txt", "d.txt", "new.txt")
	step(1, 3, true)

	// The latest turn is the source's turn 0 again, here without a snapshot.
	r.run(r.stepReviewTurn(1))

	if v := r.idle(t); !v.noSnapshot || v.src.(git.TurnSource).Turn != 0 {
		t.Errorf("stepping to the latest turn: noSnapshot=%v source %+v", v.noSnapshot, v.src)
	}

	r.notice = ""
	r.run(r.stepReviewTurn(1))

	if !strings.Contains(r.notice, "4") {
		t.Errorf("notice = %q, want one naming turn 4, the latest", r.notice)
	}
}

func TestStepInBranchModeEntersTurnMode(t *testing.T) {
	t.Parallel()
	f := newFakeGit(t)
	r := newDrawerRig(t, gitOf(f), func(o *Options) { o.Diff = core.DiffConfig{Mode: "branch"} })
	id := r.agent(t, threeTurnRepo(t, f).Root(), testSession)

	r.open(t, id)

	if r.store.State().Tab().Review.Mode != core.ReviewBranch {
		t.Fatal("setup: the drawer did not open in branch mode")
	}

	for _, delta := range []int{-1, 1} {
		r.run(r.setReviewMode(core.ReviewBranch))
		r.idle(t)
		r.run(r.stepReviewTurn(delta))

		v := r.idle(t)

		if r.store.State().Tab().Review.Mode != core.ReviewTurn || v.res.Turn != 3 || !slices.Equal(viewPaths(v), []string{"dir/c.txt"}) {
			t.Errorf("stepping %d from branch mode: mode %q turn %d files %v, want turn mode on the latest turn",
				delta, r.store.State().Tab().Review.Mode, v.res.Turn, viewPaths(v))
		}
	}
}

func TestTurnModeReturnsToTheLatestTurn(t *testing.T) {
	t.Parallel()
	f := newFakeGit(t)
	r := newDrawerRig(t, gitOf(f), turnFirst)
	id := r.agent(t, threeTurnRepo(t, f).Root(), testSession)

	r.open(t, id)
	r.run(r.stepReviewTurn(-1))

	if v := r.idle(t); v.res.Turn != 2 {
		t.Fatalf("setup: turn %d, want 2", v.res.Turn)
	}

	r.run(r.setReviewMode(core.ReviewTurn))

	if v := r.idle(t); v.res.Turn != 3 || !v.res.Live {
		t.Errorf("t shows turn %d live=%v, want the live latest turn 3", v.res.Turn, v.res.Live)
	}
}

func TestStaleLoadIsDropped(t *testing.T) {
	t.Parallel()
	f := newFakeGit(t)
	r := newDrawerRig(t, gitOf(f), turnFirst)
	id := r.agent(t, threeTurnRepo(t, f).Root(), testSession)

	v := r.open(t, id)
	tab := r.store.State().Tab().ID
	before, gen := v.res, v.gen

	// A load that finished, but whose source was replaced before its answer was
	// delivered, changes nothing.
	first := r.setReviewMode(core.ReviewBranch)
	finished := flatten(first)
	second := r.setReviewMode(core.ReviewTurn)

	if m, ok := finished[0].(reviewLoadedMsg); !ok || m.err != nil || len(m.res.Files) == len(before.Files) {
		t.Fatalf("setup: the held-back answer is %+v, want a finished load that differs from what is shown", finished[0])
	}

	for _, m := range finished {
		r.step(m)
	}

	if v.gen != gen || v.err != nil || len(v.res.Files) != len(before.Files) {
		t.Fatalf("a finished stale load was applied: gen %d -> %d, err %v, files %d", gen, v.gen, v.err, len(v.res.Files))
	}

	if _, ok := v.src.(git.TurnSource); !ok {
		t.Errorf("source = %T after the stale answer, want the turn source", v.src)
	}

	for _, m := range flatten(second) {
		r.step(m)
	}

	if v.gen == gen {
		t.Error("the load for the current source was not applied")
	}

	// So does one that was cancelled when its source was replaced.
	gen = v.gen
	third := r.setReviewMode(core.ReviewBranch)
	fourth := r.setReviewMode(core.ReviewTurn)

	for _, m := range flatten(third) {
		r.step(m)
	}

	if v.gen != gen || v.err != nil {
		t.Fatalf("a cancelled load was applied: gen %d -> %d, err %v", gen, v.gen, v.err)
	}

	for _, m := range flatten(fourth) {
		r.step(m)
	}

	// Answers that name an older generation are dropped, whatever they hold.
	gen = v.gen
	bogus := git.Result{Files: []git.FileDiff{{Path: "bogus.txt", Status: git.Added}}}

	r.step(reviewLoadedMsg{tab: tab, gen: v.epoch - 1, res: bogus})
	r.step(reviewFullMsg{tab: tab, gen: v.epoch - 1, path: "dir/c.txt", text: fullText{new: []string{"bogus"}}})

	if v.gen != gen || viewPaths(v)[0] == "bogus.txt" || len(v.full) != 0 {
		t.Errorf("a stale answer was applied: gen %d -> %d, files %v, full %d", gen, v.gen, viewPaths(v), len(v.full))
	}

	// An answer for a tab that has no drawer is dropped too.
	r.step(reviewLoadedMsg{tab: "t99", gen: v.epoch, res: bogus})

	if r.drawer.views["t99"] != nil {
		t.Error("an answer for an unknown tab created a view")
	}
}

func TestLiveUpdateKeepsTheHunk(t *testing.T) {
	t.Parallel()

	f := newFakeGit(t)
	r := newDrawerRig(t, gitOf(f))
	dir := editedRepo(t, f).Root()
	root := dir
	id := r.shell(t, dir)

	v := r.open(t, id)
	r.watching(t, id, root)

	if len(v.res.Files) != 1 || len(v.res.Files[0].Hunks) != 2 {
		t.Fatalf("setup: %d files, want f.txt with two hunks", len(v.res.Files))
	}

	v.cursor = rowWith(t, v, "fifty edited")
	gen := v.gen

	lastStart := func() int {
		if len(v.res.Files) == 0 || len(v.res.Files[0].Hunks) == 0 {
			return -1
		}

		hs := v.res.Files[0].Hunks

		return hs[len(hs)-1].NewStart
	}

	// Three lines above the cursor shift it down; the watcher reports the saved file.
	write(t, filepath.Join(dir, "f.txt"), editedF(3))
	f.watches.send(root, git.Batch{Paths: []string{"f.txt"}})
	r.until(t, "the saved file to reach the drawer", func() bool { return v.gen > gen && lastStart() == 50 })

	if got := diffLineAt(v, v.cursor); got != "fifty edited" {
		t.Errorf("the cursor is on %q after the update, want the line it was on", got)
	}

	row := v.rows[v.cursor]

	if h := v.res.Files[row.file].Hunks[row.hunk]; h.NewStart != 50 {
		t.Errorf("the cursor is in the hunk at line %d, want the one at line 50", h.NewStart)
	}
}

func TestLiveUpdateFlashes(t *testing.T) {
	t.Parallel()
	f := newFakeGit(t)
	r := newDrawerRig(t, gitOf(f))
	dir := editedRepo(t, f).Root()
	root := dir
	id := r.shell(t, dir)

	v := r.open(t, id)
	r.watching(t, id, root)

	if len(v.fresh) != 0 {
		t.Fatalf("the first load flashes %d rows", len(v.fresh))
	}

	write(t, filepath.Join(dir, "f.txt"), editedF(0)+"a brand new last line\n")
	r.run(r.batch(root, git.Batch{Paths: []string{"f.txt"}}))
	r.until(t, "the flash", func() bool { return len(v.fresh) > 0 })

	// The new line flashes, and so do the context lines the new hunk shows for
	// the first time; lines that were on screen before do not.
	flashing := map[string]bool{}
	for i := range v.fresh {
		flashing[diffLineAt(v, i)] = true
	}

	if !flashing["a brand new last line"] || flashing["five edited"] || flashing["fifty edited"] || flashing["line 4"] {
		t.Errorf("flashing lines %v, want the new line and not the ones shown before", flashing)
	}

	const flash = 600 * time.Millisecond

	if want := r.clk.Now().Add(flash); !v.flashUntil.Equal(want) || !r.ticks.scheduled(flash) {
		t.Errorf("flash until %v with ticks %v, want %v and a %v tick", v.flashUntil, r.ticks.delays, want, flash)
	}

	during := strings.Join(r.drawerLines(r.store.State(), mustRect(t, r)), "\n")

	// A tick that fires early, while a later flash runs, leaves the flash alone.
	for _, m := range r.ticks.fire() {
		r.step(m)
	}

	if len(v.fresh) == 0 {
		t.Error("a tick before the flash ended cleared it")
	}

	r.clk.Advance(flash)
	r.step(reviewFlashMsg{tab: r.store.State().Tab().ID})

	if len(v.fresh) != 0 {
		t.Errorf("%d rows still flash after the tick", len(v.fresh))
	}

	if after := strings.Join(r.drawerLines(r.store.State(), mustRect(t, r)), "\n"); after == during {
		t.Error("the drawer looks the same after the flash ended")
	}
}

func TestHiddenTabDrawerRecomputesWhenShown(t *testing.T) {
	t.Parallel()
	f := newFakeGit(t)
	r := newDrawerRig(t, gitOf(f))
	dir := editedRepo(t, f).Root()
	root := dir
	id := r.shell(t, dir)

	v := r.open(t, id)
	r.watching(t, id, root)

	first := r.store.State().Tab().ID

	openIn(t, r.App, core.NewTab{Kind: core.KindShell, Cwd: t.TempDir()})
	r.settle()

	gen := v.gen

	write(t, filepath.Join(dir, "f.txt"), editedF(2))

	if cmd := r.batch(root, git.Batch{Paths: []string{"f.txt"}}); cmd != nil {
		t.Error("a batch for a hidden tab's drawer started work")
	}

	if v.loading || v.queued == nil || v.gen != gen {
		t.Fatalf("hidden drawer: loading=%v queued=%v gen %d -> %d, want it marked stale and left alone", v.loading, v.queued, gen, v.gen)
	}

	openIn(t, r.App, core.SelectTab{Index: 0})

	if got := r.store.State().Tab().ID; got != first {
		t.Fatalf("active tab = %s, want %s", got, first)
	}

	r.settle()
	r.until(t, "the drawer to recompute", func() bool { return v.gen > gen && !v.loading })

	if v.queued != nil || v.res.Files[0].Hunks[0].NewStart != 1 {
		t.Errorf("after showing: queued=%v first hunk at %d, want the new diff", v.queued, v.res.Files[0].Hunks[0].NewStart)
	}
}

func TestSessionChangeRetargets(t *testing.T) {
	t.Parallel()
	f := newFakeGit(t)
	r := newDrawerRig(t, gitOf(f), turnFirst)
	rp := threeTurnRepo(t, f)
	dir := rp.Root()
	rp.turn(string(otherSession))
	write(t, filepath.Join(dir, "x.txt"), "after the other session began\n")

	id := r.agent(t, dir, testSession)
	v := r.open(t, id)

	// The working tree holds x.txt, which the other session's turn has not seen yet.
	if v.session != string(testSession) || !slices.Equal(viewPaths(v), []string{"dir/c.txt", "x.txt"}) {
		t.Fatalf("setup: session %q files %v", v.session, viewPaths(v))
	}

	openIn(t, r.App, core.SetAgentSession{ID: id, Session: otherSession})
	r.settle()
	r.until(t, "the other session's turn", r.viewHolds(func(v *reviewView) bool {
		return v.session == string(otherSession) && v.loaded && !v.loading
	}))

	v = r.view()
	if ts, ok := v.src.(git.TurnSource); !ok || ts.Session != string(otherSession) || ts.Turn != 0 {
		t.Errorf("source = %+v, want the other session's latest turn", v.src)
	}

	if got := viewPaths(v); !slices.Equal(got, []string{"x.txt"}) || v.turns != 1 {
		t.Errorf("shows %v of %d turns, want x.txt of 1", got, v.turns)
	}

	// In branch mode a new session only changes what t would show: the diff
	// stays, and the turn count is probed for the new session.
	r.run(r.setReviewMode(core.ReviewBranch))
	r.idle(t)

	gen := r.view().gen

	openIn(t, r.App, core.SetAgentSession{ID: id, Session: testSession})
	r.settle()
	r.until(t, "the count for the new session", r.viewHolds(func(v *reviewView) bool { return v.turnsFor == string(testSession) && !v.loading }))

	if v := r.view(); v.gen != gen || v.session != string(testSession) || v.turns != 3 {
		t.Errorf("branch mode: gen %d -> %d session %q turns %d, want the diff kept, the new session and its 3 turns", gen, v.gen, v.session, v.turns)
	}

	r.run(r.setReviewMode(core.ReviewTurn))

	if v := r.idle(t); !slices.Equal(viewPaths(v), []string{"dir/c.txt", "x.txt"}) {
		t.Errorf("t shows %v, want the new session's latest turn", viewPaths(v))
	}

	// A session that ends takes the turns with it: the drawer shows the branch
	// and core keeps turn mode.
	r.run(r.setReviewMode(core.ReviewTurn))
	r.idle(t)
	openIn(t, r.App, core.SetAgentSession{ID: id})
	r.settle()
	r.until(t, "the branch", r.viewHolds(func(v *reviewView) bool {
		_, ok := v.src.(git.BranchSource)

		return ok && !v.loading
	}))

	if got := r.store.State().Tab().Review.Mode; got != core.ReviewTurn {
		t.Errorf("mode = %q after the session ended, want turn kept", got)
	}
}

// Batches reach only the drawers that review their root.
func TestBatchesRouteToTheirRoot(t *testing.T) {
	t.Parallel()
	f := newFakeGit(t)
	r := newDrawerRig(t, gitOf(f), turnFirst)
	dir := threeTurnRepo(t, f).Root()
	id := r.agent(t, dir, testSession)

	v := r.open(t, id)

	if cmd := r.batch(filepath.Join(dir, "elsewhere"), git.Batch{Overflow: true}); cmd != nil || v.queued != nil || v.loading {
		t.Fatalf("a batch for another root reached the drawer: queued=%v loading=%v", v.queued, v.loading)
	}
}

// A drawer opened without the keyboard leaves it where it is.
func TestOpenWithoutTheKeyboard(t *testing.T) {
	t.Parallel()
	f := newFakeGit(t)
	r := newDrawerRig(t, gitOf(f))
	id := r.shell(t, editedRepo(t, f).Root())

	// The new shell lies under the drawer, so the first pane keeps the keyboard.
	openIn(t, r.App, core.FocusPane{ID: "p1"})
	r.run(r.openReview(id, false))
	r.until(t, "the drawer to open", func() bool { return r.store.State().Tab().Review != nil })

	if r.drawer.focused {
		t.Error("the drawer took the keyboard")
	}
}

// With no watch held a hook event stands in for the file changes.
func TestHookEventRecomputesAnUnwatchedDrawer(t *testing.T) {
	t.Parallel()
	f := newFakeGit(t)
	r := newDrawerRig(t, gitOf(f), func(o *Options) { o.Watches = nil })
	dir := editedRepo(t, f).Root()
	id := r.agent(t, dir, "")

	v := r.open(t, id)
	gen := v.gen

	if got := r.held(id); got != "" {
		t.Fatalf("setup: the pane holds a watch on %q", got)
	}

	write(t, filepath.Join(dir, "f.txt"), editedF(2))
	r.run(r.step(hookMsg{ev: harness.Event{Pane: id, Name: "Stop", State: core.AgentIdle}}))
	r.until(t, "the drawer to recompute", func() bool { return v.gen > gen && !v.loading })

	if got := v.res.Files[0].Hunks[0].NewStart; got != 1 {
		t.Errorf("the first hunk starts at line %d, want 1 (the saved file)", got)
	}
}

func TestBranchSwitchRecomputes(t *testing.T) {
	t.Parallel()
	f := newFakeGit(t)
	r := newDrawerRig(t, gitOf(f))
	rp := f.repo(t, "")
	dir := rp.Root()
	write(t, filepath.Join(dir, "base.txt"), "base\n")
	rp.commit("init")
	rp.branch("feature")
	write(t, filepath.Join(dir, "feat.txt"), "feature\n")
	rp.commit("feature work")

	root := dir
	id := r.shell(t, dir)
	v := r.open(t, id)

	if !slices.Equal(viewPaths(v), []string{"feat.txt"}) || v.branch != "feature" {
		t.Fatalf("setup: %v on %q, want feat.txt on feature", viewPaths(v), v.branch)
	}

	switchTo := func(branch string, want ...string) {
		t.Helper()

		// A checkout also rewrites the working tree.
		rp.switchTo(branch)

		if branch == "main" {
			if err := os.Remove(filepath.Join(dir, "feat.txt")); err != nil {
				t.Fatal(err)
			}
		} else {
			write(t, filepath.Join(dir, "feat.txt"), "feature\n")
		}

		gen := r.view().gen
		r.run(r.batch(root, git.Batch{Git: true}))
		r.until(t, "the new branch's diff", func() bool { return r.view().gen > gen && !r.view().loading })

		if v := r.view(); !slices.Equal(viewPaths(v), want) || v.branch != branch {
			t.Errorf("on %s: %v titled %q, want %v", branch, viewPaths(v), v.branch, want)
		}
	}

	switchTo("main")
	switchTo("feature", "feat.txt")
}

func TestPaneCwdChangeRetargetsTheDrawer(t *testing.T) {
	t.Parallel()
	f := newFakeGit(t)
	r := newDrawerRig(t, gitOf(f))
	a, rb := editedRepo(t, f).Root(), f.repo(t, "")
	b := rb.Root()
	write(t, filepath.Join(b, "base.txt"), "base\n")
	rb.commit("init")
	write(t, filepath.Join(b, "only-in-b.txt"), "b\n")

	rootA, rootB := a, b
	id := r.shell(t, a)

	r.open(t, id)
	r.watching(t, id, rootA)

	openIn(t, r.App, core.SetPaneCwd{ID: id, Cwd: b})
	r.settle()
	r.until(t, "the drawer to show b", r.viewHolds(func(v *reviewView) bool { return v.root == rootB && v.loaded && !v.loading }))

	v := r.view()
	if v.project != filepath.Base(b) || !slices.Equal(viewPaths(v), []string{"only-in-b.txt"}) {
		t.Errorf("shows %v of %q, want only-in-b.txt of %q", viewPaths(v), v.project, filepath.Base(b))
	}

	r.until(t, "the watch to move", func() bool { return r.held(id) == rootB && !r.watched(rootA) })

	// Outside any repository the drawer says so and keeps nothing.
	openIn(t, r.App, core.SetPaneCwd{ID: id, Cwd: t.TempDir()})
	r.settle()
	r.until(t, "the drawer to notice", r.viewHolds(func(v *reviewView) bool { return v.root == "" && v.err != nil }))

	if v := r.view(); len(v.res.Files) != 0 || !errors.Is(v.err, errNotRepo) {
		t.Errorf("outside a repository: %d files, err %v", len(v.res.Files), v.err)
	}
}

func TestWorktreeDeletedShowsTheError(t *testing.T) {
	t.Parallel()
	f := newFakeGit(t)
	r := newDrawerRig(t, gitOf(f))
	dir := editedRepo(t, f).Root()
	root := dir

	v := r.open(t, r.shell(t, dir))

	if len(v.res.Files) == 0 {
		t.Fatal("setup: nothing to show")
	}

	if err := os.RemoveAll(dir); err != nil {
		t.Fatal(err)
	}

	r.run(r.batch(root, git.Batch{Overflow: true}))
	r.until(t, "the error", func() bool { return v.err != nil && !v.loading })

	if len(v.res.Files) != 0 || len(v.rows) != 0 {
		t.Errorf("a deleted worktree still shows %d files and %d rows", len(v.res.Files), len(v.rows))
	}

	var gone errWorktreeRemoved
	if !errors.As(v.err, &gone) || gone.root != root {
		t.Errorf("error = %v, want the worktree %q reported removed", v.err, root)
	}

	if got := r.drawn(t); !strings.Contains(got, noticeRemoved) {
		t.Errorf("the drawer does not show the error %q:\n%s", v.err, got)
	}
}

func TestFullFileLoadsOnF(t *testing.T) {
	t.Parallel()
	f := newFakeGit(t)
	r := newDrawerRig(t, gitOf(f))
	dir := editedRepo(t, f).Root()
	write(t, filepath.Join(dir, "blob.bin"), "a\x00b\x00")
	root := dir
	id := r.shell(t, dir)

	v := r.open(t, id)
	r.watching(t, id, root)

	v.cursor = rowWith(t, v, "f.txt")
	gen := v.gen

	r.run(r.toggleReviewFull())
	r.until(t, "the full text", func() bool { return len(v.full) == 1 })

	ft := v.full["f.txt"]
	if len(ft.new) != 60 || len(ft.old) != 60 || ft.err != nil || v.gen <= gen {
		t.Fatalf("full text: %d new, %d old lines, err %v, gen %d -> %d, want 60 and 60 and a new generation", len(ft.new), len(ft.old), ft.err, gen, v.gen)
	}

	if n := countRows(v, rowFull); n < 60 {
		t.Errorf("%d full rows, want the 60 lines of the new file", n)
	}

	// A save while the file is shown whole reloads its text.
	gen = v.gen

	write(t, filepath.Join(dir, "f.txt"), editedF(2))
	f.watches.send(root, git.Batch{Paths: []string{"f.txt"}})
	r.until(t, "the full text to follow", func() bool { return v.gen > gen && len(v.full["f.txt"].new) == 62 })

	// f toggles it off again, and the other file has no full view.
	v.cursor = rowWith(t, v, "f.txt")

	r.run(r.toggleReviewFull())

	if len(v.full) != 0 || countRows(v, rowFull) != 0 {
		t.Errorf("f did not turn the full view off: %d texts, %d rows", len(v.full), countRows(v, rowFull))
	}

	v.cursor = rowWith(t, v, "blob.bin")
	r.notice = ""

	r.run(r.toggleReviewFull())

	if r.notice != noticeBinaryFull {
		t.Errorf("notice = %q, want %q", r.notice, noticeBinaryFull)
	}
}

// bigTwoEnded is a 3000-line file and its edit with changes near both ends:
// line 2 and line 2999 edited, a line inserted after line 10 and line 2990
// removed.
func bigTwoEnded() (old, edited string) {
	var b strings.Builder

	for i := 1; i <= 3000; i++ {
		switch i {
		case 2:
			b.WriteString("two edited\n")
		case 2990:
		case 2999:
			b.WriteString("end edited\n")
		default:
			fmt.Fprintf(&b, "line %d\n", i)
		}

		if i == 10 {
			b.WriteString("inserted\n")
		}
	}

	return numberedLines("line", 3000), b.String()
}

// fullChanges are the texts of the added and the ghost rows of file fi.
func fullChanges(v *reviewView, fi int) (added, ghosts []string) {
	for _, row := range v.rows {
		switch {
		case row.file != fi || row.part > 0:
		case row.kind == rowFull && row.other < 0:
			added = append(added, row.text)
		case row.kind == rowGhost:
			ghosts = append(ghosts, row.text)
		}
	}

	return added, ghosts
}

// brief is ss for a failure message: its length and at most its first five.
func brief(ss []string) string {
	return fmt.Sprintf("%d lines %q", len(ss), ss[:min(len(ss), 5)])
}

// hunkChanges are the texts of the added and the removed lines of f's hunks.
func hunkChanges(f git.FileDiff) (added, removed []string) {
	for _, h := range f.Hunks {
		for _, l := range h.Lines {
			switch l.Kind {
			case git.Add:
				added = append(added, l.Text)
			case git.Del:
				removed = append(removed, l.Text)
			}
		}
	}

	return added, removed
}

func TestFullFileOfABigFileMarksOnlyItsChanges(t *testing.T) {
	t.Parallel()
	f := newFakeGit(t)
	r := newDrawerRig(t, gitOf(f))
	rp := f.repo(t, "")
	dir := rp.Root()
	old, edited := bigTwoEnded()
	write(t, filepath.Join(dir, "big.txt"), old)
	rp.commit("init")
	write(t, filepath.Join(dir, "big.txt"), edited)
	id := r.shell(t, dir)

	v := r.open(t, id)
	r.watching(t, id, dir)

	v.cursor = rowWith(t, v, "big.txt")

	r.run(r.toggleReviewFull())
	r.until(t, "the full text", func() bool { return len(v.full) == 1 })

	fi := v.rows[rowWith(t, v, "big.txt")].file
	added, ghosts := fullChanges(v, fi)

	if want := []string{"two edited", "inserted", "end edited"}; !slices.Equal(added, want) {
		t.Errorf("added rows = %s, want %q", brief(added), want)
	}

	if want := []string{"line 2", "line 2990", "line 2999"}; !slices.Equal(ghosts, want) {
		t.Errorf("ghost rows = %s, want %q", brief(ghosts), want)
	}

	hunkAdded, hunkRemoved := hunkChanges(v.res.Files[fi])
	if !slices.Equal(added, hunkAdded) || !slices.Equal(ghosts, hunkRemoved) {
		t.Errorf("the full view marks +%s -%s, unified +%s -%s", brief(added), brief(ghosts), brief(hunkAdded), brief(hunkRemoved))
	}

	if n := countRows(v, rowFull) - len(added); n != 2997 {
		t.Errorf("%d kept rows, want the 2997 unchanged lines", n)
	}
}

func countRows(v *reviewView, kind reviewRowKind) int {
	n := 0

	for _, row := range v.rows {
		if row.kind == kind {
			n++
		}
	}

	return n
}

func TestRestoredDrawerLoadsWhenItsTabShows(t *testing.T) {
	t.Parallel()
	f := newFakeGit(t)
	r := newDrawerRig(t, gitOf(f))
	dir := editedRepo(t, f).Root()
	id := r.shell(t, dir)

	// A restored state has the drawer open and no view; the focused pane is not under it.
	openIn(t, r.App, core.FocusPane{ID: "p1"}, core.OpenReview{Pane: id, Mode: core.ReviewBranch})
	r.settle()
	r.idle(t)

	if r.drawer.focused {
		t.Error("a restored drawer took the keyboard")
	}

	first := r.store.State().Tab().ID
	openIn(t, r.App, core.NewTab{Kind: core.KindShell, Cwd: t.TempDir()})

	delete(r.drawer.views, first)
	r.settle()

	if len(r.drawer.views) != 0 {
		t.Fatalf("a hidden tab's drawer was loaded: %d views", len(r.drawer.views))
	}

	openIn(t, r.App, core.SelectTab{Index: 0})
	r.settle()

	if v := r.idle(t); !slices.Equal(viewPaths(v), []string{"f.txt"}) {
		t.Errorf("the restored drawer shows %v, want f.txt", viewPaths(v))
	}
}

// A restored drawer whose pane is no longer in a repository says so.
func TestRestoredDrawerOutsideARepositoryShowsTheError(t *testing.T) {
	t.Parallel()
	r := newDrawerRig(t, gitOf(newFakeGit(t)))
	id := r.shell(t, t.TempDir())

	openIn(t, r.App, core.OpenReview{Pane: id, Mode: core.ReviewBranch})
	r.settle()
	r.until(t, "the error", r.viewHolds(func(v *reviewView) bool { return v.err != nil }))

	if v := r.view(); !errors.Is(v.err, errNotRepo) || !v.loaded || len(v.rows) != 0 {
		t.Errorf("err = %v loaded = %v rows = %d, want the not-a-repository error and no rows", v.err, v.loaded, len(v.rows))
	}

	if got := r.drawn(t); !strings.Contains(got, noticeNotRepo) {
		t.Errorf("the drawer does not say why it is empty:\n%s", got)
	}
}

func TestDrawerHoldsAWatchForAShell(t *testing.T) {
	t.Parallel()
	f := newFakeGit(t)
	r := newDrawerRig(t, gitOf(f))
	dir := editedRepo(t, f).Root()
	root := dir
	id := r.shell(t, dir)

	r.open(t, id)
	r.watching(t, id, root)

	openIn(t, r.App, core.CloseReview{})
	r.settle()
	r.until(t, "the watch to be released", func() bool { return !r.watched(root) })

	if len(r.drawer.views) != 0 || r.watches.panes[id] != nil {
		t.Errorf("closing the drawer left %d views and a watch entry %v", len(r.drawer.views), r.watches.panes[id])
	}
}

func TestEngineLimitsAndLayoutFollowTheConfig(t *testing.T) {
	t.Parallel()
	f := newFakeGit(t)
	rp := f.repo(t, "")
	dir := rp.Root()
	write(t, filepath.Join(dir, "a.txt"), "a\n")
	write(t, filepath.Join(dir, "b.txt"), "b\n")
	rp.commit("init")
	write(t, filepath.Join(dir, "a.txt"), "a2\n")
	write(t, filepath.Join(dir, "b.txt"), "b2\n")

	t.Run("defaults", func(t *testing.T) {
		r := newDrawerRig(t, gitOf(f))
		v := r.open(t, r.shell(t, dir))

		if v.res.Truncated || v.split {
			t.Errorf("truncated=%v split=%v with the defaults, want neither", v.res.Truncated, v.split)
		}
	})

	t.Run("configured", func(t *testing.T) {
		r := newDrawerRig(t, gitOf(f), func(o *Options) {
			o.Diff = core.DiffConfig{Layout: "split", MaxFiles: 1}
		})
		v := r.open(t, r.shell(t, dir))

		if !v.res.Truncated || !v.split {
			t.Errorf("truncated=%v split=%v with max_files 1 and layout split, want both", v.res.Truncated, v.split)
		}
	})
}

func TestAppCloseNoLeaksWithADrawer(t *testing.T) {
	base := runtime.NumGoroutine()
	f := newFakeGit(t)
	r := newDrawerRig(t, gitOf(f))
	dir := editedRepo(t, f).Root()
	root := dir
	id := r.shell(t, dir)

	r.open(t, id)
	r.watching(t, id, root)

	// A load is in flight when the app closes.
	write(t, filepath.Join(dir, "f.txt"), editedF(1))
	r.run(r.batch(root, git.Batch{Paths: []string{"f.txt"}}))

	if !r.view().loading {
		t.Fatal("setup: no load is in flight")
	}

	ctx, cancel := context.WithTimeout(context.Background(), hangGuard)
	defer cancel()

	if err := r.Close(ctx); err != nil {
		t.Fatalf("Close() error = %v", err)
	}

	if n := f.watches.holds(root); n != 0 {
		t.Errorf("%d watch references still held after Close", n)
	}

	eventually2(t, hangGuard, "goroutines to return to baseline", func() bool { return runtime.NumGoroutine() <= base })
}
