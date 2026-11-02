package ui

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/mhersson/aim/internal/core"
	"github.com/mhersson/aim/internal/git"
	"github.com/mhersson/aim/internal/harness"
)

// repoAhead is a fake repository on main with an upstream and n commits past it,
// and one uncommitted edit.
func (r *railApp) repoAhead(t *testing.T, name string, n int) *fakeRepo {
	t.Helper()

	rp := r.fake.repo(t, name)
	write(t, filepath.Join(rp.Root(), "a.txt"), "one\n")
	oid := rp.commit("init")

	rp.remote("origin", "https://example.invalid/o/r.git").setBranch("main", oid)
	rp.track("origin", "main", oid)
	rp.setUpstream("main", "origin")

	for i := range n {
		rp.commit(fmt.Sprintf("c%d", i))
	}

	write(t, filepath.Join(rp.Root(), "a.txt"), "one\ntwo\n")

	return rp
}

// openRail opens the rail as the app does and answers what follows: the head
// probes an update raises for the agents, then the rail's own probes.
func (r *railApp) openRail() []tea.Msg { return r.deliver(tea.Batch(r.settle(), r.railOpened())) }

// gitBatch hands the app a watcher batch for the pane's worktree (dir, a fake root), as the watch
// set would, and answers the probes it raises. An update that follows drops
// the pane's watch, since no watch set holds one.
func (r *railApp) gitBatch(t *testing.T, id core.PaneID, dir string, b git.Batch) []tea.Msg {
	t.Helper()

	r.watches.panes[id] = &paneWatch{want: dir, root: dir}

	return r.deliver(r.onWatch(watchMsg{Root: dir, Batch: b}))
}

func TestHeadProbeRunsWithTheRailClosed(t *testing.T) {
	t.Parallel()

	r := newRailApp(t, true)
	rp := r.repoAhead(t, "proj", 2)
	dir := rp.Root()
	id := r.addAgent(t, false, dir)

	if r.railIsOpen() {
		t.Fatal("setup: the rail is open")
	}

	// A pane that becomes an agent is probed with no rail to show it.
	msgs := r.deliver(r.settle())
	if heads, _ := probedCwds(msgs); len(heads) != 1 || heads[0] != dir {
		t.Fatalf("settle probed %v, want the agent's cwd", heads)
	}

	c, ok := r.card(id)
	if !ok || c.branch != "main" || c.ahead != 2 {
		t.Fatalf("card = %+v, want branch main, 2 ahead", c)
	}

	// A watcher batch with a git change probes again at once, inside the interval.
	rp.commit("another")
	r.clk.Advance(time.Second)

	msgs = r.gitBatch(t, id, dir, git.Batch{Git: true})
	if heads, _ := probedCwds(msgs); len(heads) != 1 || heads[0] != dir {
		t.Fatalf("a git batch probed %v, want the agent's cwd", heads)
	}

	if c, _ := r.card(id); c.ahead != 3 {
		t.Errorf("ahead = %d after a commit, want 3", c.ahead)
	}

	// The branch a checkout lands on shows too.
	rp.branch("side")
	r.clk.Advance(time.Second)
	r.gitBatch(t, id, dir, git.Batch{Git: true})

	if c, _ := r.card(id); c.branch != "side" || c.ahead != 0 {
		t.Errorf("card = branch %q ahead %d, want side with no upstream", c.branch, c.ahead)
	}
}

func TestHeadProbeOutlivesTheRail(t *testing.T) {
	t.Parallel()

	r := newRailApp(t, true)
	rp := r.repoAhead(t, "proj", 1)
	id := r.addAgent(t, false, rp.Root())
	r.openRail()

	if c, _ := r.card(id); c.ahead != 1 {
		t.Fatalf("ahead = %d, want 1", c.ahead)
	}

	// A head probe running when the rail closes is not cancelled by it.
	r.clk.Advance(time.Minute)
	rp.commit("another")

	running := r.railFire(r.store.State(), id, probeHead)
	r.railClosed()

	hm, ok := running().(railHeadMsg)
	if !ok || hm.err != nil {
		t.Fatalf("the running probe ended with %+v, want an answer", hm)
	}

	r.step(hm)

	if c, _ := r.card(id); c.ahead != 2 {
		t.Errorf("ahead = %d after the rail closed, want 2", c.ahead)
	}

	// A trigger inside the interval waits for it, as it does with the rail open.
	r.ticks.delays = nil
	r.clk.Advance(time.Second)

	if msgs := flatten(hook(r.App, idleHook(id))); countOf[railDueMsg](msgs) != 1 || countOf[railHeadMsg](msgs) != 0 {
		t.Fatalf("an idle hook inside the interval ran %v", msgs)
	}

	if len(r.ticks.delays) != 1 || r.ticks.delays[0] != 4*time.Second {
		t.Errorf("delays = %v, want one of 4s", r.ticks.delays)
	}

	r.clk.Advance(4 * time.Second)

	if msgs := flatten(r.step(railDueMsg{pane: id, kind: probeHead})); countOf[railHeadMsg](msgs) != 1 {
		t.Errorf("the due message ran %v, want one head probe", msgs)
	}
}

func TestHeadProbeEndsWithTheApp(t *testing.T) {
	t.Parallel()

	r := newRailApp(t, false)
	id := r.addAgent(t, false, r.repo(t, "proj").Root())

	run := r.railFire(r.store.State(), id, probeHead)
	r.cancel()

	hm, ok := run().(railHeadMsg)
	if !ok || !errors.Is(hm.err, context.Canceled) {
		t.Errorf("a probe of a stopped app ended with %+v, want a cancel", hm)
	}
}

func TestStatsAndNameProbesStayOffWithTheRailClosed(t *testing.T) {
	t.Parallel()

	r := newRailApp(t, true)
	dir := r.repoAhead(t, "proj", 1).Root()
	id := r.addAgent(t, false, dir)
	transcript := filepath.Join(dir, "t.jsonl")
	openIn(t, r.App, core.SetAgentSession{ID: id, Session: "s1", Transcript: transcript})

	var msgs []tea.Msg

	msgs = append(msgs, r.deliver(r.settle())...)
	msgs = append(msgs, flatten(hook(r.App, harness.Event{Pane: id, Name: "SessionStart", Start: true, State: core.AgentIdle, Session: "s1", Transcript: transcript}))...)
	msgs = append(msgs, r.gitBatch(t, id, dir, git.Batch{Git: true, Paths: []string{"a.txt"}, Overflow: true})...)
	r.clk.Advance(time.Minute)
	msgs = append(msgs, r.deliver(hook(r.App, idleHook(id)))...)

	if n := countOf[railStatsMsg](msgs) + countOf[railNameMsg](msgs); n != 0 {
		t.Errorf("%d stats or name probes ran with the rail closed: %v", n, msgs)
	}

	for _, m := range msgs {
		if due, ok := m.(railDueMsg); ok && due.kind != probeHead {
			t.Errorf("a delayed %v probe was scheduled with the rail closed", due.kind)
		}
	}

	rp := r.rail.panes[id]
	if rp.stats.target != "" || rp.nameSlot.target != "" || rp.stats.busy || rp.nameSlot.busy {
		t.Errorf("stats slot %+v, name slot %+v: want both untouched", rp.stats, rp.nameSlot)
	}

	// Opening the rail starts them.
	opened := r.deliver(r.railOpened())
	if countOf[railStatsMsg](opened) != 1 || countOf[railNameMsg](opened) != 1 || countOf[railHeadMsg](opened) != 0 {
		t.Errorf("open ran %v, want one stats and one name probe", opened)
	}
}

func TestFocusedShellHeadIsProbed(t *testing.T) {
	t.Parallel()

	r := newRailApp(t, true)
	first, second := r.repoAhead(t, "first", 1).Root(), r.repoAhead(t, "second", 2).Root()

	openIn(t, r.App, core.NewTab{Kind: core.KindShell, Cwd: first})

	firstID := r.store.State().Tab().Focus

	// A shell that takes focus is probed, with the rail closed and with no agent.
	msgs := r.deliver(r.settle())
	if heads, stats := probedCwds(msgs); len(heads) != 1 || heads[0] != first || len(stats) != 0 {
		t.Fatalf("focusing the shell probed heads %v stats %v, want its head alone", heads, stats)
	}

	if p := r.rail.panes[firstID].probe; p.branch != "main" || p.ahead != 1 || !p.repo {
		t.Errorf("probe = %+v, want branch main, 1 ahead", p)
	}

	// A shell that is not focused is not probed.
	openIn(t, r.App, core.NewTab{Kind: core.KindShell, Cwd: second})

	secondID := r.store.State().Tab().Focus

	openIn(t, r.App, core.SelectTab{Index: 1})

	if msgs := flatten(r.settle()); len(msgs) != 0 {
		t.Errorf("settling with the first shell focused again ran %v", msgs)
	}

	if _, ok := r.rail.panes[secondID]; ok {
		t.Error("a shell that never had focus has probe memory")
	}

	// It is probed when it takes focus.
	r.clk.Advance(6 * time.Second)
	openIn(t, r.App, core.SelectTab{Index: 2})

	msgs = r.deliver(r.settle())
	if heads, _ := probedCwds(msgs); len(heads) != 1 || heads[0] != second {
		t.Fatalf("focusing the second shell probed %v, want %s", heads, second)
	}

	if p := r.rail.panes[secondID].probe; p.branch != "main" || p.ahead != 2 {
		t.Errorf("probe = %+v, want branch main, 2 ahead", p)
	}

	// Taking focus again probes again, once the interval has passed.
	r.clk.Advance(6 * time.Second)
	openIn(t, r.App, core.SelectTab{Index: 1})

	if heads, _ := probedCwds(r.deliver(r.settle())); len(heads) != 1 || heads[0] != first {
		t.Errorf("refocusing the first shell probed %v, want %s", heads, first)
	}

	// Inside the interval a focus change waits for it.
	openIn(t, r.App, core.SelectTab{Index: 2})
	r.deliver(r.settle())
	openIn(t, r.App, core.SelectTab{Index: 1})

	if msgs := flatten(r.settle()); countOf[railHeadMsg](msgs) != 0 || countOf[railDueMsg](msgs) != 1 {
		t.Errorf("a focus inside the interval ran %v, want one delayed probe", msgs)
	}

	// A change of working directory probes the new one.
	r.clk.Advance(6 * time.Second)
	openIn(t, r.App, core.SetPaneCwd{ID: firstID, Cwd: second})

	if heads, _ := probedCwds(flatten(r.settle())); len(heads) != 1 || heads[0] != second {
		t.Errorf("a cwd change probed %v, want %s", heads, second)
	}
}

// brokenUpstream drops the tracking ref its branch's upstream names: the
// branch still has an upstream, but the commits past it cannot be counted.
func brokenUpstream(rp *fakeRepo) { rp.track("origin", "main", "") }

func TestHeadProbeKeepsTheBranchWhenTheCountFails(t *testing.T) {
	t.Parallel()

	t.Run("same cwd", func(t *testing.T) {
		r := newRailApp(t, false)
		rp := r.repoAhead(t, "proj", 2)
		dir := rp.Root()
		id := r.addAgent(t, false, dir)
		r.deliver(r.settle())

		if c, _ := r.card(id); c.branch != "main" || c.ahead != 2 {
			t.Fatalf("card = branch %q ahead %d, want main, 2", c.branch, c.ahead)
		}

		// A checkout onto a branch whose upstream cannot be counted still shows.
		rp.branch("feat2")
		rp.setUpstream("feat2", "origin")
		r.clk.Advance(time.Second)
		r.gitBatch(t, id, dir, git.Batch{Git: true})

		if c, _ := r.card(id); c.branch != "feat2" || c.ahead != 0 {
			t.Errorf("card = branch %q ahead %d, want feat2 with no count", c.branch, c.ahead)
		}
	})

	t.Run("new cwd", func(t *testing.T) {
		r := newRailApp(t, false)
		first, secondRepo := r.repoAhead(t, "first", 2).Root(), r.repoAhead(t, "second", 1)
		second := secondRepo.Root()
		brokenUpstream(secondRepo)

		id := r.addAgent(t, false, first)
		r.deliver(r.settle())

		openIn(t, r.App, core.SetPaneCwd{ID: id, Cwd: second})
		r.clk.Advance(6 * time.Second)
		r.deliver(r.settle())

		c, _ := r.card(id)
		if c.project != "second" || c.branch != "main" || c.ahead != 0 {
			t.Errorf("card = project %q branch %q ahead %d, want second on main with no count", c.project, c.branch, c.ahead)
		}
	})
}

// countTimeoutGit lets the head probe reach the count, where the spent budget
// ends it: only Ahead sees the probe's own context.
type countTimeoutGit struct{ *fakeGit }

func (g countTimeoutGit) Open(ctx context.Context, dir string) (git.Repo, error) {
	return g.fakeGit.Open(context.WithoutCancel(ctx), dir)
}

func (g countTimeoutGit) Head(ctx context.Context, r git.Repo) (git.HeadInfo, error) {
	return g.fakeGit.Head(context.WithoutCancel(ctx), r)
}

func TestHeadProbeCountTimeoutIsNoAnswer(t *testing.T) {
	t.Parallel()

	r := newRailApp(t, false)
	id := r.addAgent(t, false, r.repoAhead(t, "proj", 1).Root())
	r.deliver(r.settle())

	// Only the count runs out of budget: the probe has no answer to give.
	r.git = countTimeoutGit{r.fake}
	r.probeBudget = time.Nanosecond
	release := r.fake.hold("Ahead")

	defer release()

	r.clk.Advance(time.Minute)

	msgs := flatten(hook(r.App, idleHook(id)))
	if len(msgs) != 1 {
		t.Fatalf("msgs = %v", msgs)
	}

	hm, ok := msgs[0].(railHeadMsg)
	if !ok || !errors.Is(hm.err, context.DeadlineExceeded) {
		t.Fatalf("msg = %+v, want a deadline error", msgs[0])
	}

	if r.fake.count("Ahead") != 2 {
		t.Errorf("Ahead calls = %d, want the first probe's and the one that ran out of budget", r.fake.count("Ahead"))
	}

	r.step(hm)

	if c, _ := r.card(id); c.branch != "main" || c.ahead != 1 {
		t.Errorf("card = branch %q ahead %d, want the previous result", c.branch, c.ahead)
	}
}
