package ui

import (
	"context"
	"errors"
	"os"
	"runtime"
	"slices"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/mhersson/aim/internal/core"
	"github.com/mhersson/aim/internal/git"
	"github.com/mhersson/aim/internal/harness"
)

const watchWait = 5 * time.Second

// watchApp is a rail app on a fake git and its watch set, whose commands run on goroutines.
type watchApp struct {
	*railApp
	pm *pump
}

func newWatchApp(t *testing.T, stats bool) *watchApp {
	t.Helper()

	r := newRailApp(t, stats)
	r.watches.set = r.fake.watches

	w := &watchApp{railApp: r, pm: newPump(t)}
	w.pm.stopWith(t, r.App)
	w.pm.run(r.waitWatch())
	w.pm.run(r.settle())

	return w
}

// until feeds queued messages to Update until cond holds; seen sees each one first.
func (w *watchApp) until(t *testing.T, what string, seen func(tea.Msg), cond func() bool) {
	t.Helper()

	deadline := time.Now().Add(watchWait)

	for !cond() {
		select {
		case m := <-w.pm.msgs:
			if seen != nil {
				seen(m)
			}

			_, cmd := w.Update(m)
			w.pm.run(cmd)
		case <-time.After(10 * time.Millisecond):
			// A state change outside Update needs a pass of its own.
			w.pm.run(w.settle())
		}

		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
	}
}

func (w *watchApp) watched(root string) bool { return w.fake.watches.holds(root) > 0 }

func (w *watchApp) held(id core.PaneID) string {
	if pw := w.watches.panes[id]; pw != nil {
		return pw.root
	}

	return ""
}

func TestThreeAgentsShareOneWatch(t *testing.T) {
	t.Parallel()
	w := newWatchApp(t, false)
	dir := w.repo(t, "proj").Root()
	root := dir

	ids := []core.PaneID{w.addAgent(t, false, dir), w.addAgent(t, false, dir), w.addAgent(t, false, dir)}

	w.pm.run(w.settle())
	w.until(t, "all three to hold the watch", nil, func() bool {
		return w.held(ids[0]) == root && w.held(ids[1]) == root && w.held(ids[2]) == root
	})

	for _, id := range ids[:2] {
		w.pm.run(w.closePane(id))
	}

	w.until(t, "two panes to let go", nil, func() bool { return len(w.watches.panes) == 1 })
	w.closing.Wait()

	if !w.watched(root) {
		t.Fatal("closing two of three agents stopped the watch")
	}

	w.pm.run(w.closePane(ids[2]))
	w.until(t, "the last pane to let go", nil, func() bool { return len(w.watches.panes) == 0 })
	w.closing.Wait()

	if w.watched(root) {
		t.Error("the watch outlived its last agent")
	}
}

func TestShellDoesNotAcquire(t *testing.T) {
	t.Parallel()
	w := newWatchApp(t, false)
	dir := w.repo(t, "proj").Root()
	root := dir

	openIn(t, w.App, core.OpenPane{Kind: core.KindShell, Cwd: dir, Split: core.SplitVertical, Area: w.bodyArea()})

	// A focused shell is probed for its branch; it is not watched.
	if msgs := flatten(w.settle()); len(msgs) != 1 || countOf[railHeadMsg](msgs) != 1 {
		t.Errorf("a shell raised %v, want its head probe alone", msgs)
	}

	if len(w.watches.panes) != 0 || w.watched(root) {
		t.Error("a shell pane holds a watch")
	}
}

func TestCwdToAnotherRepoMovesTheWatch(t *testing.T) {
	t.Parallel()
	w := newWatchApp(t, false)
	a, b := w.repo(t, "a").Root(), w.repo(t, "b").Root()
	rootA, rootB := a, b
	id := w.addAgent(t, false, a)

	w.pm.run(w.settle())
	w.until(t, "a to be watched", nil, func() bool { return w.held(id) == rootA })

	openIn(t, w.App, core.SetPaneCwd{ID: id, Cwd: b})
	w.pm.run(w.settle())
	w.until(t, "b to be watched", nil, func() bool { return w.held(id) == rootB })
	w.until(t, "a to be released", nil, func() bool { return !w.watched(rootA) })

	// A cwd outside any repository drops the watch.
	openIn(t, w.App, core.SetPaneCwd{ID: id, Cwd: t.TempDir()})
	w.pm.run(w.settle())
	w.until(t, "b to be released", nil, func() bool { return !w.watched(rootB) })

	if _, ok := w.paneRoot(id); ok {
		t.Error("paneRoot() resolved a cwd outside a repository")
	}
}

func TestStaleResolutionIsDropped(t *testing.T) {
	t.Parallel()
	w := newWatchApp(t, false)
	a, b := w.repo(t, "a").Root(), w.repo(t, "b").Root()
	id := w.addAgent(t, false, b)

	w.pm.run(w.settle())
	w.until(t, "b to be held", nil, func() bool { return w.held(id) != "" })

	// An answer for a cwd the pane left must not start a watch.
	w.step(paneRepoMsg{id: id, cwd: a, repo: git.Repo{Root: a}})

	if w.watched(a) {
		t.Error("a stale resolution started a watch")
	}
}

func TestStaleAcquisitionIsReleased(t *testing.T) {
	t.Parallel()
	w := newWatchApp(t, false)
	repo := w.repo(t, "proj").Repo()
	root := repo.Root

	rel, err := w.fake.watches.Acquire(context.Background(), repo)
	if err != nil {
		t.Fatal(err)
	}

	// The pane is gone by the time the reference arrives.
	w.step(watchAcquiredMsg{id: "p99", repo: repo, release: w.watches.holds.add(rel)})
	w.closing.Wait()

	if w.watched(root) {
		t.Error("a reference for a closed pane was kept")
	}
}

func TestHoldsReleaseOnceAndAfterClose(t *testing.T) {
	t.Parallel()

	var h holds

	n := 0
	rel := h.add(func() { n++ })
	rel()
	rel()
	h.closeAll()

	if n != 1 {
		t.Errorf("released %d times, want 1", n)
	}

	m := 0

	h.add(func() { m++ })()

	if m != 1 {
		t.Errorf("a reference taken after closeAll was released %d times, want 1", m)
	}

	var g holds

	k := 0

	g.add(func() { k++ })
	g.closeAll()
	g.closeAll()

	if k != 1 {
		t.Errorf("closeAll released %d times, want 1", k)
	}
}

func TestRailSeesABranchSwitch(t *testing.T) {
	t.Parallel()
	w := newWatchApp(t, true)

	rp := w.fake.repo(t, "proj")
	dir, root := rp.Root(), rp.Root()
	write(t, dir+"/a.txt", "one\n")
	rp.commit("init")

	// A branch with one more file, so switching to it changes the totals.
	rp.branch("other")
	write(t, dir+"/b.txt", "b\n")
	rp.commit("b")
	rp.switchTo("main")

	if err := os.Remove(dir + "/b.txt"); err != nil {
		t.Fatal(err)
	}

	write(t, dir+"/a.txt", "one\ntwo\n")

	id := w.addAgent(t, false, dir)

	w.pm.run(w.settle())
	w.until(t, "the worktree to be watched", nil, func() bool { return w.held(id) == root })

	w.pm.run(w.railOpened())
	w.until(t, "the first probes", nil, func() bool {
		c, ok := w.card(id)

		return ok && c.branch == "main" && c.statsKnown
	})

	c, _ := w.card(id)
	files := c.stats.Files

	// No clock jump: the switch lands inside both intervals.
	rp.switchTo("other")
	write(t, dir+"/b.txt", "b\n")
	w.fake.watches.send(root, git.Batch{Git: true, Paths: []string{"b.txt"}})

	w.until(t, "the card to show other with its totals", nil, func() bool {
		c, _ := w.card(id)

		return c.branch == "other" && c.stats.Files != files
	})
}

func TestIndexOnlyChangeCostsNoStatsProbe(t *testing.T) {
	t.Parallel()
	w := newWatchApp(t, true)
	dir := w.repo(t, "proj").Root()
	root := dir
	id := w.addAgent(t, false, dir)

	w.pm.run(w.settle())
	w.until(t, "the worktree to be watched", nil, func() bool { return w.held(id) == root })

	w.pm.run(w.railOpened())
	w.until(t, "the first probes", nil, func() bool {
		c, ok := w.card(id)

		return ok && c.branch == "main" && c.statsKnown
	})

	stats := w.fake.count("Stats")

	// Staging rewrites the index and moves no head: the batch names no path. The
	// clock stands just inside the stats interval, so only a forced probe could
	// run. The batch and the head answer are delivered here, so what they raise is
	// all there is to see.
	w.clk.Advance(statsEvery - time.Second)

	msgs := flatten(w.onWatch(watchMsg{git.RootBatch{Root: root, Batch: git.Batch{Git: true}}}))
	if countOf[railHeadMsg](msgs) != 1 {
		t.Fatalf("the index change ran %v, want one head probe", msgs)
	}

	for _, m := range slices.Clone(msgs) {
		msgs = append(msgs, flatten(w.step(m))...)
	}

	if n := countOf[railStatsMsg](msgs); n != 0 || w.fake.count("Stats") != stats {
		t.Errorf("an index-only change ran %d stats probes inside the interval", n+w.fake.count("Stats")-stats)
	}
}

func TestFailedResolveNotifiesOnceAndRetriesOnAHook(t *testing.T) {
	t.Parallel()
	w := newWatchApp(t, false)
	dir := w.repo(t, "proj").Root()
	root := dir

	w.watches.open = func(context.Context, string) (git.Repo, error) { return git.Repo{}, errors.New("too slow") }

	first := w.addAgent(t, false, dir)
	w.addAgent(t, false, dir)

	w.pm.run(w.settle())

	failures := 0
	seen := func(m tea.Msg) {
		if r, ok := m.(paneRepoMsg); ok && r.err != nil {
			failures++
		}
	}

	w.until(t, "the first failure", seen, func() bool { return failures >= 1 && w.notice != "" })

	if want := carries("proj", "too slow"); !want.matches(w.notice) {
		t.Errorf("notice = %q, want %+v", w.notice, want)
	}

	w.notice = ""

	w.until(t, "the second failure", seen, func() bool { return failures >= 2 })

	if w.notice != "" {
		t.Errorf("the same project's second failure showed %q", w.notice)
	}

	if w.held(first) != "" || w.watched(root) {
		t.Fatal("a failed resolve left a watch held")
	}

	// A hook event retries, with no timer.
	w.watches.open = w.fake.Open

	w.pm.run(hook(w.App, harness.Event{Pane: first, Name: "Stop"}))
	w.until(t, "the retry to acquire", nil, func() bool { return w.held(first) == root })

	// A failure after a success is shown again, also for another project of the same name.
	w.watches.open = func(context.Context, string) (git.Repo, error) { return git.Repo{}, errors.New("too slow again") }
	w.notice = ""

	openIn(t, w.App, core.SetPaneCwd{ID: first, Cwd: w.repo(t, "proj").Root()})
	w.pm.run(w.settle())
	w.until(t, "the second notice", nil, func() bool { return w.notice != "" })

	if want := carries("proj", "too slow again"); !want.matches(w.notice) {
		t.Errorf("notice = %q, want %+v", w.notice, want)
	}
}

func TestRailStatsFollowAFileWrite(t *testing.T) {
	t.Parallel()
	w := newWatchApp(t, true)
	dir := w.repo(t, "proj").Root()
	root := dir
	id := w.addAgent(t, false, dir)

	w.pm.run(w.settle())
	w.until(t, "the worktree to be watched", nil, func() bool { return w.held(id) == root })

	w.pm.run(w.railOpened())
	w.until(t, "the first stats", nil, func() bool {
		c, ok := w.card(id)

		return ok && c.statsKnown && c.stats.Added == 1
	})

	w.clk.Advance(statsEvery + time.Second)
	write(t, dir+"/a.txt", "one\ntwo\nthree\n")
	w.fake.watches.send(root, git.Batch{Paths: []string{"a.txt"}})

	w.until(t, "the stats to follow the write", nil, func() bool {
		c, _ := w.card(id)

		return c.stats.Added == 2
	})
}

func TestFailedWatchNotifiesOnce(t *testing.T) {
	t.Parallel()
	w := newWatchApp(t, false)
	dir := w.repo(t, "proj").Root()

	// A closed set refuses every Acquire.
	w.fake.watches.close()

	w.addAgent(t, false, dir)
	w.addAgent(t, false, dir)

	w.pm.run(w.settle())

	failures := 0
	seen := func(m tea.Msg) {
		if a, ok := m.(watchAcquiredMsg); ok && errors.Is(a.err, git.ErrWatchSetClosed) {
			failures++
		}
	}

	w.until(t, "the first failure", seen, func() bool { return failures >= 1 })
	// Update ran after seen, so wait one more step for the notice.
	w.until(t, "the notice", seen, func() bool { return w.notice != "" })

	if want := carries("proj", "watch set closed"); !want.matches(w.notice) {
		t.Errorf("notice = %q, want %+v", w.notice, want)
	}

	w.notice = ""

	w.until(t, "the second failure", seen, func() bool { return failures >= 2 })

	if w.notice != "" {
		t.Errorf("the same root's second failure showed %q", w.notice)
	}
}

func TestWatchBatchErrorShowsNothing(t *testing.T) {
	t.Parallel()
	w := newWatchApp(t, false)
	w.step(watchMsg{Root: "/x", Err: errors.New("boom")})

	if w.notice != "" {
		t.Errorf("notice = %q", w.notice)
	}
}

// With the rail open the probes of an agent are answered, and with it closed
// the heads of an agent and of the focused shell are the only ones; once
// answered they cost nothing while the app idles.
func TestIdleAppDoesNoProbes(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		railOpen bool
	}{
		{"rail open", true},
		{"rail closed", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			w := newWatchApp(t, true)
			dir, shellDir := w.repo(t, "proj").Root(), w.repo(t, "shell").Root()
			id := w.addAgent(t, false, dir)

			openIn(t, w.App, core.OpenPane{Kind: core.KindShell, Cwd: shellDir, Split: core.SplitVertical, Area: w.bodyArea()})

			shell := w.store.State().Tab().Focus
			if shell == id {
				t.Fatal("setup: the agent has focus, want the shell")
			}

			w.pm.run(w.settle())
			w.until(t, "the worktree to be watched", nil, func() bool { return w.held(id) == dir })

			if tt.railOpen {
				w.pm.run(w.railOpened())
			}

			w.until(t, "the probes", nil, func() bool {
				for _, p := range []core.PaneID{id, shell} {
					rp := w.rail.panes[p]
					if rp == nil || !rp.probe.known || rp.head.busy || rp.stats.busy || rp.nameSlot.busy {
						return false
					}
				}

				c, ok := w.card(id)

				return ok && (!tt.railOpen || c.statsKnown)
			})

			calls, delays := w.gitCalls(), len(w.ticks.delays)

			// The update pass after the jump is the one that would start a probe.
			w.clk.Advance(time.Minute)

			if msgs := flatten(w.settle()); len(msgs) != 0 {
				t.Errorf("an idle app raised %v", msgs)
			}

			if got := w.gitCalls(); got != calls {
				t.Errorf("an idle app ran %d git calls", got-calls)
			}

			if got := len(w.ticks.delays); got != delays {
				t.Errorf("an idle app scheduled %d delayed probes", got-delays)
			}
		})
	}
}

func TestAppCloseNoLeaksWithWatches(t *testing.T) {
	base := runtime.NumGoroutine()
	f := newFakeGit(t)
	r := newScriptApp(t, gitOf(f), "sleep 30; :")
	root := f.repoAt(t, r.cwd).Root()

	pm := newPump(t)
	_, c := r.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
	pm.run(c)
	pm.run(r.waitWatch())
	pm.run(r.openPane(core.KindAgent, core.SplitVertical))
	_, c = r.Update(struct{}{})
	pm.run(c)

	pumped, stop := context.WithTimeout(context.Background(), watchWait)
	defer stop()

	for f.watches.holds(root) == 0 {
		if !pm.step(pumped, r.App) {
			t.Fatal("the agent's worktree was never watched")
		}
	}

	// A batch reaches the app and the wait for the next one is armed again.
	for range 2 {
		f.watches.send(root, git.Batch{Paths: []string{"a.txt"}})

		for seen := false; !seen; {
			select {
			case m := <-pm.msgs:
				_, isBatch := m.(watchMsg)
				seen = isBatch

				_, cmd := r.Update(m)
				pm.run(cmd)
			case <-pumped.Done():
				t.Fatal("a watch batch never reached the app")
			}
		}
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := r.Close(ctx); err != nil {
		t.Fatalf("Close() error = %v", err)
	}

	if n := f.watches.holds(root); n != 0 {
		t.Errorf("Close left %d references on %s", n, root)
	}

	if strings.Contains(r.notice, "failed") {
		t.Errorf("notice = %q", r.notice)
	}

	eventually2(t, 2*time.Second, "goroutines to return to baseline", func() bool { return runtime.NumGoroutine() <= base })
}
