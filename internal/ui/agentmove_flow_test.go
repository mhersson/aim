package ui

import (
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/mhersson/aim/internal/core"
	"github.com/mhersson/aim/internal/harness"
)

// fileRows are the paths of the file header rows the drawer lists, in order.
func fileRows(v *reviewView) []string {
	var out []string

	for _, r := range v.rows {
		if r.kind == rowFile {
			out = append(out, v.res.Files[r.file].Path)
		}
	}

	return out
}

// An agent launched in the main checkout that moves into a linked worktree
// takes the rail, the drawer in both modes and the tab's name with it, and
// brings them back when it returns. A turn it started in the main checkout is
// not a turn of the worktree: turn mode there has none until the worktree has
// a turn of its own.
func TestAgentMovingIntoAWorktreeTakesTheRailAndDrawer(t *testing.T) {
	t.Parallel()
	f := newFakeGit(t)
	r := newDrawerRig(t, gitOf(f))

	main := f.repo(t, "")
	mainDir := main.Root()
	write(t, filepath.Join(mainDir, "a.txt"), "one\n")
	write(t, filepath.Join(mainDir, "b.txt"), "one\n")
	main.commit("init")

	parent, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}

	wt := main.worktree(t, filepath.Join(parent, "wt-x"), "feat/x")
	linked := wt.Root()

	// The main checkout has a change of its own, so that the two directories differ.
	write(t, filepath.Join(mainDir, "b.txt"), "one\nmain\n")

	rootMain, rootLinked := mainDir, linked
	id := r.agent(t, mainDir, "")

	r.keys(keyLeader, keyRune('a'))

	if !r.store.State().Rail {
		t.Fatal("leader a did not open the rail")
	}

	send := func(ev harness.Event) {
		ev.Pane = id
		r.run(hook(r.App, ev))
	}

	// A turn starts before its snapshot exists; the drawer waits for it.
	turnStart := func(dir string, repo *fakeRepo) {
		snapped := make(chan struct{})

		send(harness.Event{Name: "UserPromptSubmit", TurnStart: true, State: core.AgentWorking, Cwd: dir, Snapped: snapped})
		repo.turn(string(testSession))
		close(snapped)
	}

	cardIs := func(branch string, added int) func() bool {
		return func() bool {
			c, ok := railCardOf(r.App, id)

			return ok && c.branch == branch && c.statsKnown && c.stats.Added == added
		}
	}

	tabName := func() string { return r.tabInfos()[0].name }

	send(harness.Event{Name: "SessionStart", Session: testSession, Start: true, State: core.AgentIdle, Cwd: mainDir})
	r.until(t, "the card to show the main checkout", cardIs("main", 1))
	turnStart(mainDir, main)

	r.open(t, id)

	r.keys(keyRune('b'))
	r.until(t, "branch mode", r.viewHolds(func(v *reviewView) bool { return isBranchSource(v) && v.loaded && !v.loading }))

	if v := r.view(); v.root != rootMain || !slices.Equal(fileRows(v), []string{"b.txt"}) || v.branch != "main" {
		t.Fatalf("before the move the drawer shows %v of %q on %q, want b.txt of %q on main", fileRows(v), v.root, v.branch, rootMain)
	}

	if got := tabName(); got != filepath.Base(mainDir) {
		t.Fatalf("tab = %q before the move, want %q", got, filepath.Base(mainDir))
	}

	// The agent edits in the worktree and its next event reports it.
	write(t, filepath.Join(linked, "a.txt"), "one\ntwo\nthree\n")
	r.clk.Advance(11 * time.Second)
	send(harness.Event{Name: "PreToolUse", State: core.AgentWorking, Action: "Edit a.txt", ActionSet: true, Cwd: linked})

	r.until(t, "the card to show the worktree", cardIs("feat/x", 2))
	r.until(t, "the drawer to follow the agent", r.viewHolds(func(v *reviewView) bool { return v.root == rootLinked && v.loaded && !v.loading }))

	c, ok := railCardOf(r.App, id)
	if !ok {
		t.Fatal("the agent has no card on the rail")
	}

	if c.project != filepath.Base(mainDir) || c.stats.Files != 1 || c.stats.Removed != 0 {
		t.Errorf("card = %+v, want project %s with one file of two added lines", c, filepath.Base(mainDir))
	}

	if got := tabName(); got != "wt-x" {
		t.Errorf("tab = %q, want the worktree's directory name wt-x", got)
	}

	if v := r.view(); !isBranchSource(v) || v.branch != "feat/x" || !slices.Equal(fileRows(v), []string{"a.txt"}) {
		t.Errorf("branch mode shows %v on %q, want a.txt on feat/x", fileRows(v), v.branch)
	}

	// The turn started in the main checkout is not a turn of the worktree.
	r.notice = ""
	r.keys(keyRune('t'))
	r.idle(t)

	if v := r.view(); !isBranchSource(v) || r.notice != noticeNoTurnsYet {
		t.Errorf("t in the worktree gives source %T and notice %q, want the branch and %q", v.src, r.notice, noticeNoTurnsYet)
	}

	// The worktree's own turn starts, and the agent changes a file after it.
	turnStart(linked, wt)
	write(t, filepath.Join(linked, "a.txt"), "one\ntwo\nthree\nfour\n")
	r.until(t, "the worktree's turn to be counted", r.viewHolds(func(v *reviewView) bool { return v.turns == 1 && v.loaded && !v.loading }))

	r.notice = ""
	r.keys(keyRune('t'))
	r.until(t, "turn mode", r.viewHolds(func(v *reviewView) bool { return !isBranchSource(v) && v.loaded && !v.loading }))

	if v := r.view(); !v.res.Live || v.res.Turn != 1 || v.turns != 1 || v.err != nil || !slices.Contains(fileRows(v), "a.txt") {
		t.Errorf("turn mode shows %v of turn %d of %d (live %v, err %v), want the live turn 1 of 1 with a.txt", fileRows(v), v.res.Turn, v.turns, v.res.Live, v.err)
	}

	r.keys(keyRune(','))

	if want := noticeFirstTurn; r.notice != want {
		t.Errorf("notice %q after ',', want %q", r.notice, want)
	}

	// Back in the main checkout the rail and the drawer return to it.
	r.clk.Advance(11 * time.Second)
	send(harness.Event{Name: "Stop", State: core.AgentIdle, Cwd: mainDir})

	r.until(t, "the card to return to the main checkout", cardIs("main", 1))
	r.until(t, "the drawer to return", r.viewHolds(func(v *reviewView) bool { return v.root == rootMain && v.loaded && !v.loading }))

	if got := tabName(); got != filepath.Base(mainDir) {
		t.Errorf("tab = %q after the return, want %q", got, filepath.Base(mainDir))
	}

	if v := r.view(); v.err != nil || v.branch != "main" {
		t.Errorf("drawer after the return: err %v on %q, want no error on main", v.err, v.branch)
	}

	r.keys(keyRune('b'))
	r.until(t, "branch mode", r.viewHolds(func(v *reviewView) bool { return isBranchSource(v) && v.loaded && !v.loading }))

	if v := r.view(); v.root != rootMain || !slices.Equal(fileRows(v), []string{"b.txt"}) {
		t.Errorf("branch mode after the return shows %v of %q, want b.txt of %q", fileRows(v), v.root, rootMain)
	}
}
