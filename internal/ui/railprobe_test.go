package ui

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/mhersson/aim/internal/core"
	"github.com/mhersson/aim/internal/git"
	"github.com/mhersson/aim/internal/harness"
)

// gitIn runs the git at bin in dir, with a config that needs nothing from the host.
func gitIn(t *testing.T, bin, dir string, args ...string) {
	t.Helper()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, bin, args...)
	cmd.Dir = dir

	cmd.Env = append(os.Environ(),
		"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t", gitFileOnly)

	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}

// repo makes a fake repository named name on branch main with one commit and one edit.
func (r *railApp) repo(t *testing.T, name string) *fakeRepo {
	t.Helper()

	rp := r.fake.repo(t, name)
	write(t, filepath.Join(rp.Root(), "a.txt"), "one\n")
	rp.commit("init")
	write(t, filepath.Join(rp.Root(), "a.txt"), "one\ntwo\n")

	return rp
}

// trees is a fake repository with a linked worktree on branch feat and a
// subdirectory of that worktree; a.txt and b.txt are committed with one line.
func (r *railApp) trees(t *testing.T) agentTrees {
	t.Helper()

	main := r.fake.repo(t, "")
	write(t, filepath.Join(main.Root(), "a.txt"), "one\n")
	write(t, filepath.Join(main.Root(), "b.txt"), "one\n")
	main.commit("init")

	parent, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}

	linked := main.worktree(t, filepath.Join(parent, "feat-wt"), "feat")

	sub := filepath.Join(linked.Root(), "pkg")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}

	return agentTrees{main: main.Root(), linked: linked.Root(), sub: sub}
}

// realRepo makes a repository named name on the real git with one commit and one edit.
func (r *railApp) realRepo(t *testing.T, name string) string {
	t.Helper()

	dir := filepath.Join(t.TempDir(), name)
	if err := os.Mkdir(dir, 0o755); err != nil {
		t.Fatal(err)
	}

	gitIn(t, r.real, dir, "init", "-q", "-b", "main")
	write(t, filepath.Join(dir, "a.txt"), "one\n")
	gitIn(t, r.real, dir, "add", ".")
	gitIn(t, r.real, dir, "commit", "-q", "-m", "init")
	write(t, filepath.Join(dir, "a.txt"), "one\ntwo\n")

	// The setup's own git calls are not the probes' business.
	_ = os.Remove(r.log)

	return dir
}

func write(t *testing.T, path, s string) {
	t.Helper()

	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}

	if err := os.WriteFile(path, []byte(s), 0o644); err != nil {
		t.Fatal(err)
	}
}

func idleHook(id core.PaneID) harness.Event {
	return harness.Event{Pane: id, Name: "Stop", State: core.AgentIdle}
}

func TestProbeBudgetDefault(t *testing.T) {
	t.Parallel()

	if got := newTestApp(t).probeBudget; got != railProbeTimeout {
		t.Errorf("probeBudget = %v, want %v", got, railProbeTimeout)
	}
}

func TestRailProbesOnOpen(t *testing.T) {
	t.Parallel()

	r := newRailApp(t, true)
	dir := r.repo(t, "proj").Root()
	id := r.addAgent(t, false, dir)

	// With the rail closed the head is probed and nothing else.
	if msgs := r.deliver(r.settle()); countOf[railHeadMsg](msgs) != 1 || len(msgs) != 1 {
		t.Fatalf("settling with the rail closed ran %v, want one head probe", msgs)
	}

	// Opening adds the stats; the head is known already.
	msgs := r.deliver(r.railOpened())
	if countOf[railHeadMsg](msgs) != 0 || countOf[railStatsMsg](msgs) != 1 {
		t.Fatalf("open ran %v", msgs)
	}

	c, ok := r.card(id)
	if !ok || c.project != "proj" || c.branch != "main" || c.detached {
		t.Errorf("card = %+v", c)
	}

	if !c.statsKnown || c.stats.Files != 1 || c.stats.Added != 1 || c.stats.Removed != 0 {
		t.Errorf("stats = %+v known %v", c.stats, c.statsKnown)
	}

	// Opening again changes nothing.
	if r.railOpened() != nil {
		t.Error("a second open probed again")
	}
}

func TestRailDetachedHead(t *testing.T) {
	t.Parallel()

	r := newRailApp(t, false)
	rp := r.repo(t, "proj")
	rp.detach()
	id := r.addAgent(t, false, rp.Root())
	r.openRail()

	c, _ := r.card(id)
	if !c.detached || len(c.branch) != 7 {
		t.Errorf("card = %+v, want a detached 7-character id", c)
	}
}

func TestRailProbeIntervals(t *testing.T) {
	t.Parallel()

	r := newRailApp(t, true)
	id := r.addAgent(t, false, r.repo(t, "proj").Root())
	r.openRail()

	r.clk.Advance(time.Second)

	// Both triggers fall inside their intervals: one delayed probe each.
	msgs := flatten(hook(r.App, idleHook(id)))
	if countOf[railDueMsg](msgs) != 2 || countOf[railHeadMsg](msgs)+countOf[railStatsMsg](msgs) != 0 {
		t.Fatalf("idle hook inside the intervals ran %v", msgs)
	}

	if want := []time.Duration{4 * time.Second, 9 * time.Second}; len(r.ticks.delays) != 2 || r.ticks.delays[0] != want[0] || r.ticks.delays[1] != want[1] {
		t.Errorf("delays = %v, want %v", r.ticks.delays, want)
	}

	// More triggers while the delayed probes wait add nothing.
	r.ticks.delays = nil

	if msgs := flatten(hook(r.App, idleHook(id))); len(msgs) != 0 || len(r.ticks.delays) != 0 {
		t.Errorf("a second trigger scheduled %v %v", msgs, r.ticks.delays)
	}

	r.clk.Set(t0.Add(5 * time.Second))

	head := railDueMsg{pane: id, kind: probeHead}
	if msgs := flatten(r.step(head)); countOf[railHeadMsg](msgs) != 1 {
		t.Errorf("the head due message ran %v", msgs)
	}

	r.clk.Set(t0.Add(10 * time.Second))

	stats := railDueMsg{pane: id, kind: probeStats, gen: r.rail.gen}
	if msgs := flatten(r.step(stats)); countOf[railStatsMsg](msgs) != 1 {
		t.Errorf("the stats due message ran %v", msgs)
	}

	// Nothing pending: a late due message does nothing.
	if msgs := flatten(r.step(head)); len(msgs) != 0 {
		t.Errorf("a due message with nothing pending ran %v", msgs)
	}
}

func TestRailTriggerDuringProbeRunsOneAfter(t *testing.T) {
	t.Parallel()

	r := newRailApp(t, false)
	id := r.addAgent(t, false, r.repo(t, "proj").Root())
	cmd := r.settle()
	r.clk.Advance(6 * time.Second)

	if msgs := flatten(hook(r.App, harness.Event{Pane: id, Name: "SessionStart", Start: true, State: core.AgentIdle})); len(msgs) != 0 {
		t.Fatalf("a trigger during a probe ran %v", msgs)
	}

	// The first probe's answer starts the one that waited.
	if n := countOf[railHeadMsg](flatten(r.step(flatten(cmd)[0]))); n != 1 {
		t.Errorf("follow-up head probes = %d, want 1", n)
	}
}

func TestRailDropsStaleResults(t *testing.T) {
	t.Parallel()

	t.Run("pane closed", func(t *testing.T) {
		r := newRailApp(t, false)
		id := r.addAgent(t, false, r.repo(t, "proj").Root())
		msgs := flatten(r.settle())
		openIn(t, r.App, core.ClosePane{ID: id})
		r.settle()
		r.step(msgs[0])

		if _, ok := r.rail.panes[id]; ok {
			t.Error("a late answer revived the closed pane's entry")
		}
	})

	t.Run("cwd changed", func(t *testing.T) {
		r := newRailApp(t, false)
		first, second := r.repo(t, "first").Root(), r.repo(t, "second").Root()
		id := r.addAgent(t, false, first)
		msgs := flatten(r.settle())

		openIn(t, r.App, core.SetPaneCwd{ID: id, Cwd: second})
		r.clk.Advance(6 * time.Second)

		next := r.step(msgs[0])
		if c, _ := r.card(id); c.project == "first" {
			t.Error("the answer for the old cwd was applied")
		}

		// The new cwd gets its own probe.
		follow := flatten(next)
		if len(follow) != 1 {
			t.Fatalf("follow-up = %v", follow)
		}

		if hm, ok := follow[0].(railHeadMsg); !ok || hm.cwd != second {
			t.Fatalf("follow-up = %+v", follow[0])
		}

		r.step(follow[0])

		if c, _ := r.card(id); c.project != "second" {
			t.Errorf("project = %q, want second", c.project)
		}
	})

	t.Run("rail closed", func(t *testing.T) {
		r := newRailApp(t, true)
		id := r.addAgent(t, false, r.repo(t, "proj").Root())
		heads, stats := r.settle(), r.railOpened()
		r.railClosed()

		// The close cancels the rail's probes, not the head's.
		for _, m := range flatten(tea.Batch(heads, stats)) {
			switch m := m.(type) {
			case railStatsMsg:
				if !errors.Is(m.err, context.Canceled) {
					t.Errorf("a stats probe of a closed rail ended with %v, want a cancel", m.err)
				}
			case railHeadMsg:
				if m.err != nil {
					t.Errorf("a head probe ended with %v when the rail closed, want an answer", m.err)
				}
			}

			r.step(m)
		}

		rp := r.rail.panes[id]
		if rp == nil || rp.probe.statsKnown {
			t.Error("a stats answer after the rail closed was applied")
		}

		if !rp.probe.known || rp.probe.branch != "main" {
			t.Errorf("probe = %+v, want the head answer applied", rp.probe)
		}

		if r.rail.ctx != nil {
			t.Error("rail context kept")
		}
	})
}

func TestRailTriggersNeedAnOpenRail(t *testing.T) {
	t.Parallel()

	r := newRailApp(t, true)
	id := r.addAgent(t, false, r.repo(t, "proj").Root())
	r.deliver(r.settle())
	r.clk.Advance(time.Minute)

	// With the rail closed a hook probes the head and nothing else.
	if msgs := r.deliver(hook(r.App, idleHook(id))); len(msgs) != 1 || countOf[railHeadMsg](msgs) != 1 {
		t.Errorf("a hook with the rail closed ran %v, want one head probe", msgs)
	}

	r.openRail()
	r.railClosed()
	r.clk.Advance(time.Minute)
	r.ticks.delays = nil

	if msgs := r.deliver(hook(r.App, idleHook(id))); len(msgs) != 1 || countOf[railHeadMsg](msgs) != 1 || len(r.ticks.delays) != 0 {
		t.Errorf("a hook after the rail closed ran %v, want one head probe and nothing delayed", msgs)
	}
}

func TestRailSlowGitIsCancelledAndKeepsTheLastResult(t *testing.T) {
	t.Parallel()

	r := newRailApp(t, false)
	id := r.addAgent(t, false, r.repo(t, "proj").Root())
	r.openRail()

	// The slow call waits for its context, which the spent budget has ended.
	release := r.fake.hold("Open")
	defer release()

	r.probeBudget = time.Nanosecond
	r.clk.Advance(6 * time.Second)

	msgs := flatten(hook(r.App, idleHook(id)))
	if len(msgs) != 1 {
		t.Fatalf("msgs = %v", msgs)
	}

	hm, ok := msgs[0].(railHeadMsg)
	if !ok || !errors.Is(hm.err, context.DeadlineExceeded) {
		t.Fatalf("msg = %+v, want a deadline error", msgs[0])
	}

	r.step(hm)

	if c, _ := r.card(id); c.project != "proj" || c.branch != "main" {
		t.Errorf("card = %+v, want the previous result", c)
	}
}

// commonGit names one repository's common dir as given, as a bare repository's worktree has it.
type commonGit struct {
	*fakeGit
	root, common string
}

func (g commonGit) Open(ctx context.Context, dir string) (git.Repo, error) {
	repo, err := g.fakeGit.Open(ctx, dir)
	if err == nil && repo.Root == g.root {
		repo.CommonDir = g.common
	}

	return repo, err
}

func TestRailProjectNames(t *testing.T) {
	t.Parallel()

	r := newRailApp(t, true)
	plain := t.TempDir()
	gone := filepath.Join(t.TempDir(), "vanished")

	src := r.repo(t, "src")
	wtsrc := src.worktree(t, filepath.Join(t.TempDir(), "linked"), "side").Root()
	bareWT := r.repo(t, "wt")
	r.git = commonGit{fakeGit: r.fake, root: bareWT.Root(), common: filepath.Join(t.TempDir(), "b.git")}

	cases := []struct {
		name, dir, project, branch string
		stats                      bool
	}{
		{"not a repo", plain, filepath.Base(plain), "", false},
		{"deleted cwd", gone, "vanished", "", false},
		{"bare repo worktree", bareWT.Root(), "b", "main", true},
		{"linked worktree", wtsrc, "src", "side", true},
	}

	for _, c := range cases {
		id := r.addAgent(t, true, c.dir)
		r.openRail()
		r.railClosed()

		got, _ := r.card(id)
		if got.project != c.project || got.branch != c.branch || got.statsKnown != c.stats {
			t.Errorf("%s: card = project %q branch %q stats %v; want %q %q %v", c.name, got.project, got.branch, got.statsKnown, c.project, c.branch, c.stats)
		}
	}
}

func TestRailIdleShellsCostNothing(t *testing.T) {
	t.Parallel()

	r := newRailApp(t, true)

	if cmd := r.railOpened(); cmd != nil {
		t.Error("opening over shells probed")
	}

	// The focused shell's head is the one probe.
	r.deliver(r.settle())

	if msgs := flatten(r.settle()); len(msgs) != 0 || len(r.ticks.delays) != 0 || r.gitCalls() != 0 {
		t.Errorf("work with nothing to do: %v %v %d", msgs, r.ticks.delays, r.gitCalls())
	}
}

func TestRailStatsOffWithoutTempDir(t *testing.T) {
	t.Parallel()

	r := newRailApp(t, false)
	r.addAgent(t, false, r.repo(t, "proj").Root())

	if n := countOf[railStatsMsg](r.deliver(r.railOpened())); n != 0 {
		t.Errorf("stats probes = %d, want 0", n)
	}
}

func TestRailReopenProbesAgain(t *testing.T) {
	t.Parallel()

	r := newRailApp(t, true)
	id := r.addAgent(t, false, r.repo(t, "proj").Root())
	r.openRail()
	r.railClosed()

	// Inside the interval the reopen schedules one delayed stats probe; the head is not the rail's.
	r.ticks.delays = nil

	if msgs := flatten(r.railOpened()); countOf[railDueMsg](msgs) != 1 || len(msgs) != 1 {
		t.Fatalf("reopen inside the intervals ran %v", msgs)
	}

	r.railClosed()
	r.clk.Advance(time.Minute)

	msgs := r.deliver(r.railOpened())
	if countOf[railStatsMsg](msgs) != 1 || countOf[railHeadMsg](msgs) != 0 {
		t.Fatalf("reopen with the same cwd ran %v", msgs)
	}

	if c, _ := r.card(id); !c.statsKnown {
		t.Error("no stats after the reopen")
	}
}

func TestRailAnswerOfAnEarlierOpenIsDropped(t *testing.T) {
	t.Parallel()

	r := newRailApp(t, true)
	dir := r.repo(t, "proj").Root()
	id := r.addAgent(t, false, dir)

	// Heads are known when the rail opens; a card shows counts only of the place its head names.
	r.deliver(r.settle())
	r.railOpened()
	oldGen := r.rail.gen
	r.railClosed()
	r.clk.Advance(time.Minute)

	newCmd := r.railOpened()
	if r.rail.gen == oldGen || !r.rail.panes[id].stats.busy {
		t.Fatal("the reopen did not start a probe")
	}

	r.step(railStatsMsg{pane: id, cwd: dir, gen: oldGen, stats: git.Stats{Files: 9}})

	if rp := r.rail.panes[id]; rp.probe.statsKnown {
		t.Error("a successful answer of an earlier open was applied")
	}

	if !r.rail.panes[id].stats.busy {
		t.Error("the stale answer freed the new open's probe")
	}

	r.deliver(newCmd)

	if c, _ := r.card(id); !c.statsKnown || c.stats.Files != 1 {
		t.Errorf("card = %+v after the new answer", c)
	}

	// With the rail closed and no reopen, a successful answer is dropped too.
	r.railClosed()
	r.step(railStatsMsg{pane: id, cwd: dir, gen: r.rail.gen, stats: git.Stats{Files: 7}})

	if c, _ := r.card(id); c.stats.Files == 7 {
		t.Error("an answer after the close was applied")
	}
}

func TestRailCardKeepsItsResultUntilTheNewCwdAnswers(t *testing.T) {
	t.Parallel()

	r := newRailApp(t, true)
	first, second := r.repo(t, "first").Root(), r.repo(t, "second").Root()
	id := r.addAgent(t, false, first)
	r.openRail()

	openIn(t, r.App, core.SetPaneCwd{ID: id, Cwd: second})
	r.clk.Advance(time.Minute)
	r.settle()

	if c, _ := r.card(id); c.project != "first" || c.branch != "main" || !c.statsKnown {
		t.Fatalf("card = %+v, want the previous result until the new cwd answers", c)
	}

	// A failed probe of the new cwd clears it.
	r.step(railHeadMsg{pane: id, cwd: second, own: second, err: context.DeadlineExceeded})
	r.step(railStatsMsg{pane: id, cwd: second, gen: r.rail.gen, err: context.DeadlineExceeded})

	c, _ := r.card(id)
	if c.project != "second" || c.branch != "" || c.statsKnown {
		t.Errorf("card = %+v, want the cwd's base name with no branch and no stats", c)
	}
}

func TestRailFailedProbeOfTheSameCwdKeepsTheResult(t *testing.T) {
	t.Parallel()

	r := newRailApp(t, true)
	id := r.addAgent(t, false, r.repo(t, "proj").Root())
	r.openRail()

	cwd := r.store.State().Panes[id].Cwd
	r.step(railHeadMsg{pane: id, cwd: cwd, own: cwd, err: context.DeadlineExceeded})
	r.step(railStatsMsg{pane: id, cwd: cwd, gen: r.rail.gen, err: context.DeadlineExceeded})

	if c, _ := r.card(id); c.branch != "main" || !c.statsKnown {
		t.Errorf("card = %+v, want the previous result", c)
	}
}

// runAll runs the commands of the batches at once and sends each message on the channel.
func runAll(cmds ...tea.Cmd) <-chan tea.Msg {
	out := make(chan tea.Msg, 64)

	var leaves []tea.Cmd

	for _, cmd := range cmds {
		if b, ok := cmd().(tea.BatchMsg); ok {
			leaves = append(leaves, b...)
		}
	}

	var wg sync.WaitGroup

	for _, c := range leaves {
		wg.Go(func() {
			out <- c()
		})
	}

	go func() {
		wg.Wait()
		close(out)
	}()

	return out
}

// drain counts the messages on ch until it closes, failing the test when the
// next is later than within.
func drain(t *testing.T, ch <-chan tea.Msg, within time.Duration) int {
	t.Helper()

	n := 0

	for {
		select {
		case _, ok := <-ch:
			if !ok {
				return n
			}

			n++
		case <-time.After(within):
			t.Fatalf("%d probes returned, then none within %v", n, within)
		}
	}
}

func agentsInOneRepo(t *testing.T, r *railApp, n int) {
	t.Helper()

	dir := r.repo(t, "proj").Root()
	for i := range n {
		r.addAgent(t, i%2 == 0, dir)
	}
}

func TestRailProbesRunFourAtATime(t *testing.T) {
	t.Parallel()

	r := newRailApp(t, true)
	agentsInOneRepo(t, r, 10)

	// The heads are known: the ten stats probes compete for the four places.
	r.deliver(r.settle())

	release := r.fake.hold("Stats")
	defer release()

	msgs := runAll(r.railOpened())

	eventually(t, "four stats probes running", func() bool { return r.fake.inflight("Stats") == railProbeMax })

	if got := len(r.probeSem); got != railProbeMax {
		t.Errorf("held slots = %d while the probes are held, want %d", got, railProbeMax)
	}

	release()

	if got := drain(t, msgs, 20*time.Second); got != 10 {
		t.Fatalf("%d of 10 probes finished after the release", got)
	}

	if got := r.fake.count("Stats"); got != 10 {
		t.Errorf("stats calls = %d, want 10", got)
	}

	if got := r.fake.peak("Stats"); got != railProbeMax {
		t.Errorf("peak concurrent probes = %d, want %d", got, railProbeMax)
	}
}

func TestRailClosingFreesWaitingProbes(t *testing.T) {
	t.Parallel()

	r := newRailApp(t, true)
	agentsInOneRepo(t, r, 10)

	// The heads are answered first: closing the rail does not cancel them.
	r.deliver(r.settle())

	release := r.fake.hold("Stats")
	defer release()

	msgs := runAll(r.railOpened())

	eventually(t, "four stats probes running", func() bool { return r.fake.inflight("Stats") == railProbeMax })
	r.railClosed()

	if got := drain(t, msgs, 5*time.Second); got != 10 {
		t.Fatalf("%d of 10 probes returned after the rail closed; waiting probes were not cancelled", got)
	}

	if n := r.fake.count("Stats"); n != railProbeMax {
		t.Errorf("stats calls = %d, want only the %d that held a slot", n, railProbeMax)
	}
}

func TestRailNonRepoAgentDoesNotRefireItsStatsProbe(t *testing.T) {
	t.Parallel()

	r := newRailApp(t, true)
	first, second := t.TempDir(), t.TempDir()
	id := r.addAgent(t, false, first)
	r.openRail()

	if rp := r.rail.panes[id]; !rp.probe.known || rp.probe.repo {
		t.Fatalf("setup: probe = %+v, want a known non-repo", rp.probe)
	}

	// Past the head interval but inside the stats one: the head probe answers
	// before the delayed stats probe comes due.
	r.clk.Advance(6 * time.Second)
	openIn(t, r.App, core.SetPaneCwd{ID: id, Cwd: second})
	r.deliver(r.settle())

	// The head probe of the new cwd answered; the stats probe has nothing to count.
	if got := r.rail.panes[id].stats.target; got != second {
		t.Errorf("stats slot cwd = %q, want %q so that no update asks again", got, second)
	}
}

func TestRailStatsProbeRunsWhenAnAgentMovesIntoARepo(t *testing.T) {
	t.Parallel()

	r := newRailApp(t, true)
	plain := t.TempDir()
	repo := r.repo(t, "proj").Root()
	id := r.addAgent(t, false, plain)
	r.openRail()
	r.clk.Advance(11 * time.Second)

	openIn(t, r.App, core.SetPaneCwd{ID: id, Cwd: repo})

	msgs := r.deliver(r.settle())
	if countOf[railStatsMsg](msgs) != 1 {
		t.Fatalf("moving into a repo ran %v, want one stats probe", msgs)
	}

	if c, _ := r.card(id); !c.statsKnown || c.stats.Files != 1 {
		t.Errorf("card = %+v, want the repo's stats", c)
	}
}

func TestProbeHeadPlace(t *testing.T) {
	t.Parallel()

	r := newRailApp(t, false)
	tr := r.trees(t)
	other := r.repo(t, "other").Root()
	scratch := t.TempDir()
	gone := filepath.Join(t.TempDir(), "gone")

	tests := []struct {
		name     string
		dir, own string
		place    string
	}{
		{"the same directory", tr.main, tr.main, ""},
		{"a worktree of the own repository", tr.linked, tr.main, ""},
		{"the main checkout, own directory in a worktree", tr.main, tr.linked, ""},
		{"another repository", other, tr.main, "other"},
		{"another repository, own directory in a worktree", other, tr.linked, "other"},
		{"own directory in no repository", other, scratch, "other"},
		{"own directory gone", other, gone, "other"},
		{"followed directory in no repository", scratch, tr.main, ""},
		{"followed directory gone", gone, tr.main, ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := probeHeadOf(context.Background(), r.fake, r.App.log, tt.dir, tt.own)
			if err != nil {
				t.Fatalf("probeHeadOf: %v", err)
			}

			if got.place != tt.place {
				t.Errorf("probeHeadOf(%q, %q).place = %q, want %q", tt.dir, tt.own, got.place, tt.place)
			}
		})
	}
}

func TestRailCardNamesTheOtherRepository(t *testing.T) {
	t.Parallel()

	r := newRailApp(t, false)
	tr := r.trees(t)
	other := r.repo(t, "other").Root()
	id := r.addAgent(t, false, tr.main)
	r.openRail()

	steps := []struct {
		name, edit, branch, place string
	}{
		{"an edit in another repository", filepath.Join(other, "a.txt"), "main", "other"},
		{"an edit in a worktree of the own repository", filepath.Join(tr.linked, "a.txt"), "feat", ""},
		{"an edit back in the other repository", filepath.Join(other, "a.txt"), "main", "other"},
		{"an edit in the own directory", filepath.Join(tr.main, "a.txt"), "main", ""},
	}

	for _, s := range steps {
		r.clk.Advance(6 * time.Second)
		r.deliverAll(hook(r.App, harness.Event{Pane: id, Name: "PostToolUse", Edit: s.edit}))

		c, _ := r.card(id)
		if c.branch != s.branch || c.place != s.place || c.project != filepath.Base(tr.main) {
			t.Errorf("%s: card = project %q, place %q, branch %q; want project %q, place %q, branch %q",
				s.name, c.project, c.place, c.branch, filepath.Base(tr.main), s.place, s.branch)
		}
	}
}

// linkGit answers as a git that does not canonicalize the paths it prints:
// the common dir of a repository under target is named through link.
type linkGit struct {
	*fakeGit
	target, link string
}

func (g linkGit) Open(ctx context.Context, dir string) (git.Repo, error) {
	repo, err := g.fakeGit.Open(ctx, dir)
	if err == nil && (repo.Root == g.target || strings.HasPrefix(repo.Root, g.target+"/")) {
		repo.CommonDir = g.link + strings.TrimPrefix(repo.CommonDir, g.target)
	}

	return repo, err
}

func TestCardPlaceThroughSymlink(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		asReached bool
	}{
		{"git answers with canonical paths", false},
		{"git answers with the path it was reached by", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			r := newRailApp(t, false)
			tr := r.trees(t)

			link := filepath.Join(t.TempDir(), "link")
			if err := os.Symlink(tr.main, link); err != nil {
				t.Fatal(err)
			}

			if tt.asReached {
				r.git = linkGit{fakeGit: r.fake, target: tr.main, link: link}

				if repo, err := r.git.Open(context.Background(), link); err != nil || !strings.HasPrefix(repo.CommonDir, link+"/") {
					t.Fatalf("Open(%q) = %+v, %v; want a common dir under the link", link, repo, err)
				}
			}

			id := r.addAgent(t, false, link)
			r.openRail()
			r.clk.Advance(6 * time.Second)
			r.deliverAll(hook(r.App, harness.Event{Pane: id, Name: "PostToolUse", Edit: filepath.Join(tr.linked, "a.txt")}))

			c, _ := r.card(id)
			if c.branch != "feat" || c.place != "" {
				t.Errorf("card = branch %q, place %q; want branch feat and no place: the own directory is the same repository", c.branch, c.place)
			}
		})
	}
}
