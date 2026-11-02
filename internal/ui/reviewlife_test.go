package ui

import (
	"context"
	"errors"
	"maps"
	"path/filepath"
	"testing"

	"github.com/mhersson/aim/internal/core"
	"github.com/mhersson/aim/internal/git"
)

// holdCount is how many watch references the app holds.
func (r *drawerRig) holdCount() int {
	r.watches.holds.mu.Lock()
	defer r.watches.holds.mu.Unlock()

	return len(r.watches.holds.refs)
}

// A reload that changed nothing, such as a duplicate batch, leaves a running
// flash alone.
func TestNoOpReloadKeepsTheFlash(t *testing.T) {
	t.Parallel()
	r := newDrawerRig(t)
	dir := realEditedRepo(t)
	root := rootOf(t, dir)
	id := r.shell(t, dir)

	v := r.open(t, id)
	r.watching(t, id, root)

	write(t, filepath.Join(dir, "f.txt"), editedF(0)+"a brand new last line\n")
	r.run(r.batch(root, git.Batch{Paths: []string{"f.txt"}}))
	r.until(t, "the flash", func() bool { return len(v.fresh) > 0 })

	fresh, until, gen, ticks := maps.Clone(v.fresh), v.flashUntil, v.gen, len(r.ticks.delays)

	r.run(r.batch(root, git.Batch{Paths: []string{"f.txt"}}))
	r.until(t, "the reload", func() bool { return v.gen > gen && !v.loading })

	if !maps.Equal(v.fresh, fresh) || !v.flashUntil.Equal(until) || len(r.ticks.delays) != ticks {
		t.Errorf("a reload that changed nothing touched the flash: %d rows until %v, %d ticks; was %d rows until %v, %d ticks",
			len(v.fresh), v.flashUntil, len(r.ticks.delays), len(fresh), until, ticks)
	}
}

// The file the tree has selected stays selected when a live update moves the
// files around, and falls to the nearest index when it leaves the diff.
func TestTreeSelFollowsItsFile(t *testing.T) {
	t.Parallel()
	r := newDrawerRig(t)
	dir := newRepoDir(t)
	files := []string{"b.txt", "c.txt", "d.txt"}

	for _, f := range files {
		write(t, filepath.Join(dir, f), "one\n")
	}

	commitEverything(t, dir, "init")

	for _, f := range files {
		write(t, filepath.Join(dir, f), "two\n")
	}

	root := rootOf(t, dir)
	id := r.shell(t, dir)

	v := r.open(t, id)
	r.watching(t, id, root)

	v.treeSel = 1

	if got := v.res.Files[v.treeSel].Path; got != "c.txt" {
		t.Fatalf("setup: the selection is on %s, want c.txt", got)
	}

	reload := func(path string) {
		t.Helper()

		gen := v.gen

		r.run(r.batch(root, git.Batch{Paths: []string{path}}))
		r.until(t, "the reload", func() bool { return v.gen > gen && !v.loading })
	}

	// A file that sorts first moves every index.
	write(t, filepath.Join(dir, "a.txt"), "new\n")
	reload("a.txt")

	if got := v.res.Files[v.treeSel].Path; got != "c.txt" {
		t.Errorf("after a file was added the selection is on %s, want c.txt", got)
	}

	// Its file leaves the diff: the index it had, now d.txt's.
	write(t, filepath.Join(dir, "c.txt"), "one\n")
	reload("c.txt")

	if got := v.res.Files[v.treeSel].Path; got != "d.txt" {
		t.Errorf("after its file left the diff the selection is on %s, want the nearest, d.txt", got)
	}
}

// A full-file read ends with its view: a read of a drawer that was closed does
// not run on.
func TestFullFileReadsEndWithTheirView(t *testing.T) {
	t.Parallel()
	r := newDrawerRig(t)
	id := r.shell(t, realEditedRepo(t))

	v := r.open(t, id)
	v.cursor = rowWith(t, v, "f.txt")

	read := r.toggleReviewFull()

	openIn(t, r.App, core.CloseReview{})
	r.settle()

	var got *reviewFullMsg

	for _, m := range flatten(read) {
		if full, ok := m.(reviewFullMsg); ok {
			got = &full
		}
	}

	if got == nil {
		t.Fatal("the read gave no answer")
	}

	if !errors.Is(got.text.err, context.Canceled) {
		t.Errorf("the read of a closed drawer ended with %v, want it cancelled", got.text.err)
	}
}

// An answer that arrives after its drawer closed finds no view, and one that
// arrives after the drawer reopened names an older epoch.
func TestLateAnswersAfterCloseAreDropped(t *testing.T) {
	t.Parallel()
	r := newDrawerRig(t)
	id := r.shell(t, realEditedRepo(t))

	r.open(t, id)

	tab := r.store.State().Tab().ID
	late := flatten(r.setReviewMode(core.ReviewBranch))

	if len(late) != 1 {
		t.Fatalf("the load gave %d answers, want 1", len(late))
	}

	if m, ok := late[0].(reviewLoadedMsg); !ok || m.err != nil {
		t.Fatalf("setup: the late answer is %+v, want a finished load", late[0])
	}

	openIn(t, r.App, core.CloseReview{})
	r.settle()
	r.step(late[0])

	if r.drawer.views[tab] != nil {
		t.Fatal("an answer after the close made a view")
	}

	v := r.open(t, id)
	gen, epoch := v.gen, v.epoch

	r.step(late[0])

	if v.gen != gen || v.epoch != epoch || r.view() != v {
		t.Errorf("an answer from before the reopen was applied: generation %d -> %d", gen, v.gen)
	}
}

// Closing the drawer's pane, or moving it to another tab, releases the watch
// the drawer held; the root's watcher stops with its last holder.
func TestClosePaneAndMoveReleaseTheDrawersWatch(t *testing.T) {
	t.Parallel()
	setup := func(t *testing.T) (*drawerRig, string, core.PaneID, core.PaneID) {
		t.Helper()

		r := newDrawerRig(t)
		dir := realEditedRepo(t)
		root := rootOf(t, dir)
		agent := r.agent(t, dir, "")
		shell := r.shell(t, dir)

		r.open(t, shell)
		r.watching(t, shell, root)
		r.watching(t, agent, root)

		if got := r.holdCount(); got != 2 {
			t.Fatalf("setup: %d holds, want one for the agent and one for the drawer", got)
		}

		return r, root, shell, agent
	}

	t.Run("closing the pane", func(t *testing.T) {
		r, root, shell, agent := setup(t)

		openIn(t, r.App, core.ClosePane{ID: shell})
		r.settle()
		r.until(t, "one hold to go", func() bool { return r.holdCount() == 1 })

		if !r.watched(root) || r.store.State().Tab().Review != nil {
			t.Errorf("watched=%v review=%v after the drawer's pane closed, want the agent's watch and no drawer", r.watched(root), r.store.State().Tab().Review)
		}

		openIn(t, r.App, core.ClosePane{ID: agent})
		r.settle()
		r.until(t, "the last hold to go", func() bool { return r.holdCount() == 0 && !r.watched(root) })
	})

	t.Run("moving the pane to another tab", func(t *testing.T) {
		r, root, shell, _ := setup(t)

		openIn(t, r.App, core.MovePane{ID: shell, Tab: 1, Area: r.bodyArea()})
		r.settle()
		r.until(t, "the drawer's hold to go", func() bool { return r.holdCount() == 1 })

		if !r.watched(root) {
			t.Error("the agent's watch went with the drawer's")
		}

		if _, ok := r.store.State().TabOf(shell); !ok || len(r.drawer.views) != 0 {
			t.Errorf("after the move: pane in a tab %v, %d views, want the pane kept and no view", ok, len(r.drawer.views))
		}
	})
}

// A read that was cancelled is not applied when a newer read of the same blobs
// has taken its place, and its answer does not end the newer read.
func TestCancelledFullReadDoesNotShadowTheNewOne(t *testing.T) {
	t.Parallel()
	r := newDrawerRig(t)
	id := r.shell(t, realEditedRepo(t))

	v := r.open(t, id)
	v.cursor = rowWith(t, v, "f.txt")

	first := r.toggleReviewFull()

	r.toggleReviewFull()

	second := r.toggleReviewFull()

	// The cancelled read answers first.
	for _, m := range flatten(first) {
		r.step(m)
	}

	if len(v.full) != 0 || v.fullStop["f.txt"] == nil {
		t.Fatalf("the cancelled read was applied or ended the new one: %d texts, stop %v", len(v.full), v.fullStop["f.txt"] != nil)
	}

	for _, m := range flatten(second) {
		r.step(m)
	}

	if ft, ok := v.full["f.txt"]; !ok || ft.err != nil || len(ft.new) != 60 {
		t.Errorf("the new read was not applied: %+v", ft)
	}
}
