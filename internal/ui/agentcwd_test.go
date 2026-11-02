package ui

import (
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/mhersson/aim/internal/core"
	"github.com/mhersson/aim/internal/git"
	"github.com/mhersson/aim/internal/harness"
)

// agentTrees is one repository: its main checkout, where an agent is
// launched, a linked worktree of it on branch feat, and a subdirectory of that
// worktree. Both a.txt and b.txt are committed with one line.
type agentTrees struct{ main, linked, sub string }

func newAgentTrees(t *testing.T, f *fakeGit) agentTrees {
	t.Helper()

	main := f.repo(t, "")
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

// cwdHook is a hook event that carries only a working directory.
func cwdHook(id core.PaneID, cwd string) harness.Event {
	return harness.Event{Pane: id, Name: "Notification", Cwd: cwd}
}

// probedCwds lists the directories the head and stats probes in msgs ran for.
func probedCwds(msgs []tea.Msg) (heads, stats []string) {
	for _, m := range msgs {
		switch m := m.(type) {
		case railHeadMsg:
			heads = append(heads, m.cwd)
		case railStatsMsg:
			stats = append(stats, m.cwd)
		}
	}

	return heads, stats
}

func TestHookRecordsTheAgentCwd(t *testing.T) {
	t.Parallel()

	r := newRailApp(t, false)
	tr := newAgentTrees(t, r.fake)
	id := r.addAgent(t, false, tr.main)
	elsewhere := t.TempDir()

	steps := []struct{ name, cwd, want string }{
		{"a linked worktree", tr.linked, tr.linked},
		{"the same directory again", tr.linked, tr.linked},
		{"an empty cwd leaves it", "", tr.linked},
		{"a relative cwd leaves it", "pkg", tr.linked},
		{"a subdirectory", tr.sub, tr.sub},
		{"a directory outside any repository", elsewhere, elsewhere},
	}

	for _, s := range steps {
		t.Run(s.name, func(t *testing.T) {
			hook(r.App, cwdHook(id, s.cwd))

			m := r.store.State().Panes[id]
			if m.AgentCwd != s.want || m.WorkDir() != s.want || m.Cwd != tr.main {
				t.Errorf("pane = Cwd %q AgentCwd %q, want Cwd %q AgentCwd %q", m.Cwd, m.AgentCwd, tr.main, s.want)
			}
		})
	}
}

func TestHookCwdOfAPaneThatIsNotAnAgentChangesNothing(t *testing.T) {
	t.Parallel()

	r := newRailApp(t, false)
	tr := newAgentTrees(t, r.fake)

	closed := r.addAgent(t, false, tr.main)
	demoted := r.addAgent(t, false, tr.main)

	hook(r.App, cwdHook(demoted, tr.linked))
	openIn(t, r.App, core.SetPaneKind{ID: demoted, Kind: core.KindShell})
	r.closePane(closed)

	before := r.store.State()

	for _, id := range []core.PaneID{"p1", "p9", closed, demoted} {
		hook(r.App, cwdHook(id, tr.sub))
	}

	after := r.store.State()
	if len(after.Panes) != len(before.Panes) {
		t.Fatalf("panes = %d, want %d: an event opened one", len(after.Panes), len(before.Panes))
	}

	for id, m := range before.Panes {
		if !reflect.DeepEqual(after.Panes[id], m) {
			t.Errorf("pane %s = %+v, want %+v", id, after.Panes[id], m)
		}
	}
}

func TestFocusedCwdAndTabNameFollowTheAgent(t *testing.T) {
	t.Parallel()

	r := newRailApp(t, false)
	tr := newAgentTrees(t, r.fake)
	id := r.addAgent(t, false, tr.main)

	if got := r.store.State().Tab().Focus; got != id {
		t.Fatalf("focus = %s, want %s", got, id)
	}

	tabName := func() string { return r.tabInfos()[0].name }

	if got := r.focusedCwd(r.store.State()); got != tr.main || tabName() != filepath.Base(tr.main) {
		t.Fatalf("before a hook: focusedCwd %q, tab %q, want the launch directory", got, tabName())
	}

	hook(r.App, cwdHook(id, tr.linked))

	if got := r.focusedCwd(r.store.State()); got != tr.linked || tabName() != "feat-wt" {
		t.Errorf("focusedCwd %q, tab %q, want %q, feat-wt", got, tabName(), tr.linked)
	}

	hook(r.App, cwdHook(id, tr.sub))

	if got := tabName(); got != "pkg" {
		t.Errorf("tab = %q after a hook in a subdirectory, want pkg", got)
	}

	// A pane opened beside the agent starts where the agent works.
	next := r.store.State().NewPaneID()
	r.openPane(core.KindShell, core.SplitVertical)

	if m, ok := r.store.State().Panes[next]; !ok || m.Cwd != tr.sub {
		t.Errorf("new pane = %+v, want a shell in %q", m, tr.sub)
	}
}

func TestRailFollowsTheAgentCwd(t *testing.T) {
	t.Parallel()

	r := newRailApp(t, true)
	tr := newAgentTrees(t, r.fake)
	write(t, filepath.Join(tr.linked, "a.txt"), "one\ntwo\n")

	id := r.addAgent(t, false, tr.main)
	r.openRail()

	if c, _ := r.card(id); c.project != filepath.Base(tr.main) || c.branch != "main" || !c.statsKnown || c.stats.Files != 0 {
		t.Fatalf("card at the launch directory = %+v, want main with no changes", c)
	}

	for _, dir := range []string{tr.linked, tr.sub} {
		r.clk.Advance(11 * time.Second)

		heads, stats := probedCwds(r.deliver(hook(r.App, cwdHook(id, dir))))
		if want := []string{dir}; !slices.Equal(heads, want) || !slices.Equal(stats, want) {
			t.Fatalf("hook in %s: head probes for %v, stats probes for %v, want one each for %s", dir, heads, stats, dir)
		}

		// The worktrees share the project; the branch and the changes are the worktree's.
		c, _ := r.card(id)
		if c.project != filepath.Base(tr.main) || c.branch != "feat" || !c.statsKnown || c.stats.Files != 1 || c.stats.Added != 1 {
			t.Errorf("card in %s = %+v, want project %s, branch feat, one file with one added line", dir, c, filepath.Base(tr.main))
		}
	}
}

func TestRailShowsAnAgentDirectoryOutsideARepository(t *testing.T) {
	t.Parallel()

	r := newRailApp(t, true)
	tr := newAgentTrees(t, r.fake)
	elsewhere := filepath.Join(t.TempDir(), "scratch")

	if err := os.Mkdir(elsewhere, 0o755); err != nil {
		t.Fatal(err)
	}

	id := r.addAgent(t, false, tr.main)
	r.openRail()
	r.clk.Advance(11 * time.Second)
	r.deliver(hook(r.App, cwdHook(id, elsewhere)))

	if c, _ := r.card(id); c.project != "scratch" || c.branch != "" || c.statsKnown {
		t.Errorf("card = %+v, want scratch with no branch and no stats", c)
	}
}

func TestRailCwdChangesAreThrottled(t *testing.T) {
	t.Parallel()

	r := newRailApp(t, true)
	tr := newAgentTrees(t, r.fake)
	id := r.addAgent(t, false, tr.main)
	r.openRail()
	r.clk.Advance(11 * time.Second)

	dirs := make([]string, 6)
	for i := range dirs {
		dirs[i] = filepath.Join(tr.linked, fmt.Sprintf("d%d", i))

		if err := os.Mkdir(dirs[i], 0o755); err != nil {
			t.Fatal(err)
		}
	}

	// The first move probes at once.
	msgs := r.deliver(hook(r.App, cwdHook(id, dirs[0])))
	if countOf[railHeadMsg](msgs) != 1 || countOf[railStatsMsg](msgs) != 1 {
		t.Fatalf("the first move ran %v, want one head and one stats probe", msgs)
	}

	calls, delays := r.gitCalls(), len(r.ticks.delays)

	// A directory that changes on every event waits for the intervals.
	for _, dir := range dirs[1:] {
		r.clk.Advance(100 * time.Millisecond)

		if heads, stats := probedCwds(flatten(hook(r.App, cwdHook(id, dir)))); len(heads)+len(stats) != 0 {
			t.Fatalf("a move to %s probed again inside the intervals: heads %v, stats %v", dir, heads, stats)
		}
	}

	if got := r.gitCalls(); got != calls {
		t.Errorf("git ran %d times inside the intervals, want none", got-calls)
	}

	if got := len(r.ticks.delays) - delays; got != 2 {
		t.Errorf("scheduled %d delayed probes, want one head and one stats", got)
	}

	// The delayed head probe asks for the directory the agent is in by then.
	r.clk.Advance(5 * time.Second)

	heads, _ := probedCwds(flatten(r.step(railDueMsg{pane: id, kind: probeHead})))
	if want := []string{dirs[len(dirs)-1]}; !slices.Equal(heads, want) {
		t.Errorf("delayed head probes for %v, want %v", heads, want)
	}
}

func TestWatchFollowsTheAgentCwd(t *testing.T) {
	t.Parallel()

	w := newWatchApp(t, false)
	tr := newAgentTrees(t, w.fake)
	rootMain, rootLinked := tr.main, tr.linked
	id := w.addAgent(t, false, tr.main)

	w.pm.run(w.settle())
	w.until(t, "the main checkout to be watched", nil, func() bool { return w.held(id) == rootMain })

	// A subdirectory of the worktree resolves to the worktree's root.
	w.pm.run(hook(w.App, cwdHook(id, tr.sub)))
	w.until(t, "the worktree to be watched", nil, func() bool { return w.held(id) == rootLinked })
	w.until(t, "the main checkout to be released", nil, func() bool { return !w.watched(rootMain) })

	if got, ok := w.paneRoot(id); !ok || got != rootLinked {
		t.Errorf("paneRoot() = %q, %v, want %q", got, ok, rootLinked)
	}
}

func TestWatchSettlesWhenTheAgentCwdFlips(t *testing.T) {
	t.Parallel()

	w := newWatchApp(t, false)
	tr := newAgentTrees(t, w.fake)
	rootMain, rootLinked := tr.main, tr.linked
	id := w.addAgent(t, false, tr.main)

	w.pm.run(w.settle())
	w.until(t, "the main checkout to be watched", nil, func() bool { return w.held(id) == rootMain })

	// Answers for the directories the agent already left are dropped.
	for _, dir := range []string{tr.sub, tr.main, tr.sub, tr.main, tr.sub} {
		w.pm.run(hook(w.App, cwdHook(id, dir)))
	}

	w.until(t, "the worktree to be watched", nil, func() bool { return w.held(id) == rootLinked })
	w.until(t, "the main checkout to be released", nil, func() bool { return !w.watched(rootMain) })

	// No answer left a reference behind: closing the pane frees the last one.
	w.pm.run(w.closePane(id))
	w.until(t, "the worktree to be released", nil, func() bool { return !w.watched(rootLinked) })
	w.closing.Wait()
}

func TestDrawerOpensOnTheAgentCwd(t *testing.T) {
	t.Parallel()
	f := newFakeGit(t)
	r := newDrawerRig(t, gitOf(f))
	tr := newAgentTrees(t, f)
	write(t, filepath.Join(tr.main, "b.txt"), "one\nmain\n")
	write(t, filepath.Join(tr.linked, "a.txt"), "one\nlinked\n")

	id := r.agent(t, tr.main, "")
	r.run(hook(r.App, cwdHook(id, tr.sub)))
	r.settle()

	v := r.open(t, id)

	if want := (git.BranchSource{Worktree: tr.linked}); v.src != want {
		t.Errorf("source = %+v, want %+v", v.src, want)
	}

	if got := viewPaths(v); !slices.Equal(got, []string{"a.txt"}) || v.branch != "feat" {
		t.Errorf("drawer shows %v on %q, want a.txt on feat", got, v.branch)
	}
}

func TestOpenAnswerForTheDirectoryTheAgentLeftIsDropped(t *testing.T) {
	t.Parallel()
	f := newFakeGit(t)
	r := newDrawerRig(t, gitOf(f))
	tr := newAgentTrees(t, f)
	id := r.agent(t, tr.main, "")

	msg := r.openReview(id, true)()

	r.run(hook(r.App, cwdHook(id, tr.linked)))
	r.step(msg)

	if r.store.State().Tab().Review != nil {
		t.Error("a drawer opened for the directory the agent had left")
	}

	if v := r.open(t, id); v.src != (git.BranchSource{Worktree: tr.linked}) {
		t.Errorf("source = %+v, want the worktree", v.src)
	}
}
