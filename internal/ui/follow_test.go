package ui

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/mhersson/aim/internal/core"
	"github.com/mhersson/aim/internal/git"
	"github.com/mhersson/aim/internal/harness"
)

// followRig is the drawer rig with an agent launched in repository A, which has
// a linked worktree b on branch b under .worktrees, and another repository C on
// branch other. A hides .worktrees and *.log through its own exclude file.
type followRig struct {
	*drawerRig
	id                  core.PaneID
	dirA, dirB, dirC    string
	rootA, rootB, rootC string
	repoA, repoB, repoC *fakeRepo // nil on the real git

	mu       sync.Mutex
	resolved []string // the paths the edit resolver ran for

	epochRoot map[int]string // the root each drawer epoch loads

	holdLoads bool              // feed keeps the drawer's load answers in parked instead of delivering them
	parked    []reviewLoadedMsg // the load answers feed kept
}

func newFollowRig(t *testing.T, session core.SessionID, opts ...func(*Options)) *followRig {
	t.Helper()

	f := newFakeGit(t)
	r := &followRig{drawerRig: newDrawerRig(t, append([]func(*Options){gitOf(f)}, opts...)...), epochRoot: map[int]string{}}

	// The two repositories have different names, so a project read from the wrong one shows.
	a := f.repo(t, "projA")
	write(t, filepath.Join(a.Root(), "a.txt"), "one\n")
	a.commit("init")
	a.ignore(".worktrees/", "*.log")

	b := a.worktree(t, filepath.Join(a.Root(), ".worktrees", "b"), "b")

	c := f.repo(t, "projC")
	write(t, filepath.Join(c.Root(), "c.txt"), "one\n")
	c.commit("init")
	c.branch("other")

	r.repoA, r.repoB, r.repoC = a, b, c
	r.dirA, r.dirB, r.dirC = a.Root(), b.Root(), c.Root()
	r.rootA, r.rootB, r.rootC = r.dirA, r.dirB, r.dirC

	// The cache is the app's own, over a resolver that counts what reaches the fake.
	places := git.NewPlaceCache(func(ctx context.Context, path string) (git.Place, error) {
		r.mu.Lock()
		r.resolved = append(r.resolved, path)
		r.mu.Unlock()

		return f.EditPlace(ctx, path)
	}, time.Now)
	r.edits.resolve = places.Place

	r.id = r.agent(t, r.dirA, session)
	r.settle()
	r.watching(t, r.id, r.rootA)

	return r
}

func (r *followRig) resolverCalls() int {
	r.mu.Lock()
	defer r.mu.Unlock()

	return len(r.resolved)
}

func (r *followRig) pane() core.Pane { return r.store.State().Panes[r.id] }

// addLines leaves five uncommitted lines in the file at path.
func (r *followRig) addLines(t *testing.T, path string) {
	t.Helper()
	write(t, path, "one\n"+numberedLines("added", 5))
}

// hookNow delivers a hook and then every answer of the commands it raised, in
// order, so an edit is recorded when it returns. What those answers start runs
// off the test's goroutine, and a test waits for it with until.
func (r *followRig) hookNow(ev harness.Event) {
	for _, m := range flatten(hook(r.App, ev)) {
		if m != nil {
			r.run(r.step(m))
		}
	}
}

func (r *followRig) editHook(path string) harness.Event {
	return harness.Event{Pane: r.id, Name: "PostToolUse", Edit: path}
}

func (r *followRig) edit(path string) { r.hookNow(r.editHook(path)) }

// openRail opens the rail and waits until the agent's card knows its branch and changes.
func (r *followRig) openRail(t *testing.T, branch string) {
	t.Helper()

	r.run(r.railOpened())
	r.until(t, "the card to know "+branch, func() bool {
		c, ok := r.card()

		return ok && c.branch == branch && c.statsKnown
	})
}

func (r *followRig) card() (railCard, bool) { return railCardOf(r.App, r.id) }

// cardOn is a condition on the agent's card: it shows branch, and when added is
// not negative its changes have that many added lines.
func (r *followRig) cardOn(branch string, added int) func() bool {
	return func() bool {
		c, ok := r.card()

		return ok && c.branch == branch && (added < 0 || c.statsKnown && c.stats.Added == added)
	}
}

// landed is a condition: every probe of the agent has answered and the drawer is
// idle. The step that starts a probe or a load marks it busy before it returns,
// so once this holds what the step started has been delivered.
func (r *followRig) landed() bool {
	if rp := r.rail.panes[r.id]; rp != nil && (rp.head.busy || rp.stats.busy || rp.nameSlot.busy) {
		return false
	}

	v := r.view()

	return v == nil || !v.loading && v.queued == nil
}

// onRoot reports whether the watch and the open drawer settled on root.
func (r *followRig) onRoot(root string) func() bool {
	return func() bool {
		if r.held(r.id) != root || r.watched(r.rootA) != (root == r.rootA) {
			return false
		}

		v := r.view()

		return v == nil || v.root == root && v.loaded && !v.loading
	}
}

// feed delivers one message and checks that the drawer applied a load only for
// the root that load was started for. While holdLoads is set it keeps the
// drawer's load answers back instead.
func (r *followRig) feed(t *testing.T, m tea.Msg) {
	t.Helper()

	if loaded, ok := m.(reviewLoadedMsg); ok && r.holdLoads {
		r.parked = append(r.parked, loaded)

		return
	}

	before := r.view()
	gen := 0

	if before != nil {
		gen = before.gen
	}

	r.run(r.step(m))

	after := r.view()
	if after == nil {
		return
	}

	if loaded, ok := m.(reviewLoadedMsg); ok && after == before && after.gen != gen {
		if want, known := r.epochRoot[loaded.gen]; !known || want != after.root {
			t.Errorf("the drawer applied a load started for %q while it shows %q", want, after.root)
		}
	}

	r.epochRoot[after.epoch] = after.root
}

// drive feeds what the commands answer, through feed, until cond holds.
func (r *followRig) drive(t *testing.T, what string, cond func() bool) {
	t.Helper()

	deadline := time.Now().Add(r.wait)

	for !cond() {
		select {
		case m := <-r.pm.msgs:
			r.feed(t, m)
		case <-time.After(10 * time.Millisecond):
			r.feed(t, struct{}{})
		}

		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
	}
}

// editNow delivers an edit hook and every answer of the commands it raised, through feed.
func (r *followRig) editNow(t *testing.T, path string) {
	t.Helper()

	for _, m := range flatten(hook(r.App, r.editHook(path))) {
		if m != nil {
			r.feed(t, m)
		}
	}
}

func TestFollowWatchesTheFollowedPlace(t *testing.T) {
	t.Parallel()
	r := newFollowRig(t, "")

	r.edit(filepath.Join(r.dirB, "a.txt"))
	r.until(t, "the worktree b to be watched and A's reference released", r.onRoot(r.rootB))

	if got, ok := r.paneRoot(r.id); !ok || got != r.rootB {
		t.Errorf("paneRoot() = %q, %v, want %q", got, ok, r.rootB)
	}
}

func TestDrawerFollowsEdits(t *testing.T) {
	t.Parallel()
	r := newFollowRig(t, "")
	r.addLines(t, filepath.Join(r.dirB, "a.txt"))

	if v := r.open(t, r.id); v.root != r.rootA || len(viewPaths(v)) != 0 {
		t.Fatalf("drawer shows %v on %q before the edit, want nothing on A", viewPaths(v), v.root)
	}

	r.edit(filepath.Join(r.dirB, "a.txt"))
	r.until(t, "the drawer to follow the edit", r.viewHolds(func(v *reviewView) bool {
		return v.root == r.rootB && v.loaded && !v.loading
	}))

	v := r.view()
	if got := viewPaths(v); !slices.Equal(got, []string{"a.txt"}) || v.branch != "b" {
		t.Errorf("drawer shows %v on %q, want a.txt on b", got, v.branch)
	}

	if title := r.drawn(t); !strings.Contains(title, filepath.Base(r.dirA)+" · b") {
		t.Errorf("drawer reads\n%s\nwant a title %q", title, filepath.Base(r.dirA)+" · b")
	}
}

func TestCwdChangeAfterEditKeepsFollowedPlace(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		cwd  func(r *followRig) string
	}{
		{"the agent's repository", func(r *followRig) string { return r.dirA }},
		{"another repository", func(r *followRig) string { return r.dirC }},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			r := newFollowRig(t, "")
			r.addLines(t, filepath.Join(r.dirB, "a.txt"))
			r.open(t, r.id)
			r.openRail(t, "main")

			r.clk.Advance(11 * time.Second)
			r.edit(filepath.Join(r.dirB, "a.txt"))
			r.until(t, "the watch and the drawer to follow", r.onRoot(r.rootB))
			r.until(t, "the card to follow", r.cardOn("b", 5))

			r.clk.Advance(11 * time.Second)
			r.hookNow(cwdHook(r.id, tt.cwd(r)))
			r.until(t, "the hook's probes to land", r.landed)

			if got := r.pane().FollowDir(); got != r.dirB {
				t.Fatalf("FollowDir() = %q after a hook in %q, want %q", got, tt.cwd(r), r.dirB)
			}

			if c, _ := r.card(); c.branch != "b" || c.stats.Added != 5 {
				t.Errorf("card = %+v, want branch b with its 5 added lines", c)
			}

			if got := r.held(r.id); got != r.rootB {
				t.Errorf("the watch holds %q, want %q", got, r.rootB)
			}

			if v := r.view(); v.root != r.rootB {
				t.Errorf("drawer root = %q, want %q", v.root, r.rootB)
			}
		})
	}
}

func TestCwdChangeBeforeEditMoves(t *testing.T) {
	t.Parallel()
	r := newFollowRig(t, "")
	r.addLines(t, filepath.Join(r.dirB, "a.txt"))
	r.open(t, r.id)
	r.openRail(t, "main")

	r.clk.Advance(11 * time.Second)
	r.hookNow(cwdHook(r.id, r.dirB))
	r.until(t, "the watch and the drawer to move", r.onRoot(r.rootB))
	r.until(t, "the card to move", r.cardOn("b", 5))

	if got := r.pane().FollowDir(); got != r.dirB {
		t.Errorf("FollowDir() = %q, want %q", got, r.dirB)
	}
}

func TestReviewerSubagentKeepsImplementersPlace(t *testing.T) {
	t.Parallel()
	r := newFollowRig(t, "")
	r.addLines(t, filepath.Join(r.dirB, "a.txt"))
	r.open(t, r.id)
	r.openRail(t, "main")

	sub := func(name, cwd, edit string) harness.Event {
		return harness.Event{Pane: r.id, Name: name, Subagent: true, Cwd: cwd, Edit: edit}
	}

	// The implementer works in b and edits there.
	r.clk.Advance(11 * time.Second)
	r.hookNow(sub("PostToolUse", r.dirB, filepath.Join(r.dirB, "a.txt")))
	r.until(t, "the watch and the drawer to follow", r.onRoot(r.rootB))
	r.until(t, "the card to follow", r.cardOn("b", -1))

	steps := []struct {
		name string
		ev   harness.Event
	}{
		// The reviewer reads in b and diffs it, from A.
		{"reviewer pre-read", sub("PreToolUse", r.dirA, "")},
		{"reviewer post-read", sub("PostToolUse", r.dirA, "")},
		{"reviewer git -C b diff", sub("PostToolUse", r.dirA, "")},
		// The agent itself reports from A and writes a file git ignores.
		{"agent's own hook", harness.Event{Pane: r.id, Name: "PostToolUse", Cwd: r.dirA}},
		{"agent writes an ignored file", harness.Event{Pane: r.id, Name: "PostToolUse", Cwd: r.dirA, Edit: filepath.Join(r.dirA, "debug.log")}},
	}

	for _, s := range steps {
		r.clk.Advance(11 * time.Second)
		r.hookNow(s.ev)
		r.until(t, "the event's probes to land", r.landed)

		if got := r.pane().FollowDir(); got != r.dirB {
			t.Fatalf("after %s: FollowDir() = %q, want %q", s.name, got, r.dirB)
		}

		if c, _ := r.card(); c.branch != "b" {
			t.Errorf("after %s: card branch = %q, want b", s.name, c.branch)
		}

		if got, v := r.held(r.id), r.view(); got != r.rootB || v.root != r.rootB {
			t.Errorf("after %s: the watch holds %q and the drawer shows %q, want %q", s.name, got, v.root, r.rootB)
		}
	}
}

func TestEditInOwnDirectoryMovesBack(t *testing.T) {
	t.Parallel()
	r := newFollowRig(t, "")
	r.open(t, r.id)
	r.openRail(t, "main")

	r.clk.Advance(11 * time.Second)
	r.edit(filepath.Join(r.dirB, "a.txt"))
	r.until(t, "the watch and the drawer to follow", r.onRoot(r.rootB))
	r.until(t, "the card to follow", r.cardOn("b", -1))

	r.clk.Advance(11 * time.Second)
	r.edit(filepath.Join(r.dirA, "a.txt"))
	r.until(t, "the watch and the drawer to move back", r.onRoot(r.rootA))
	r.until(t, "the card to move back", r.cardOn("main", -1))

	if got := r.pane().FollowDir(); got != r.dirA {
		t.Errorf("FollowDir() = %q, want %q", got, r.dirA)
	}
}

func TestTabNameAndSplitsKeepOwnDirectory(t *testing.T) {
	t.Parallel()
	r := newFollowRig(t, "")

	r.edit(filepath.Join(r.dirC, "c.txt"))

	if got := r.pane().FollowDir(); got != r.dirC {
		t.Fatalf("FollowDir() = %q, want %q", got, r.dirC)
	}

	if got := r.tabInfos()[0].name; got != filepath.Base(r.dirA) {
		t.Errorf("tab name = %q, want %q", got, filepath.Base(r.dirA))
	}

	if got := r.focusedCwd(r.store.State()); got != r.dirA {
		t.Errorf("focusedCwd = %q, want %q", got, r.dirA)
	}

	next := r.store.State().NewPaneID()
	r.openPane(core.KindShell, core.SplitVertical)

	if m, ok := r.store.State().Panes[next]; !ok || m.Cwd != r.dirA {
		t.Errorf("new pane = %+v, want a shell in %q", m, r.dirA)
	}
}

func TestRemovedFollowedWorktreeIsForgotten(t *testing.T) {
	t.Parallel()
	r := newFollowRig(t, "")
	r.open(t, r.id)
	r.openRail(t, "main")

	path := filepath.Join(r.dirB, "a.txt")

	r.clk.Advance(11 * time.Second)
	r.edit(path)
	r.until(t, "the watch and the drawer to follow", r.onRoot(r.rootB))
	r.until(t, "the card to follow", r.cardOn("b", -1))

	if err := os.RemoveAll(r.dirB); err != nil {
		t.Fatal(err)
	}

	r.clk.Advance(11 * time.Second)
	r.hookNow(idleHook(r.id))
	r.until(t, "the watch and the drawer to fall back to A", r.onRoot(r.rootA))
	r.until(t, "the card to fall back to A", r.cardOn("main", -1))

	if p := r.pane(); len(p.Edits) != 0 || p.FollowDir() != r.dirA {
		t.Errorf("pane = %d edits, FollowDir %q, want none and %q", len(p.Edits), p.FollowDir(), r.dirA)
	}

	// The same path again resolves afresh and no longer moves the pane to b.
	calls := r.resolverCalls()

	r.edit(path)
	r.until(t, "the edit's probes to land", r.landed)

	if got := r.resolverCalls(); got != calls+1 {
		t.Errorf("the resolver ran %d times for the repeated path, want 1", got-calls)
	}

	if got := r.pane().FollowDir(); got != r.dirA {
		t.Errorf("FollowDir() = %q after an edit in the removed worktree, want %q", got, r.dirA)
	}
}

func TestAlternatingEditsEndOnLatest(t *testing.T) {
	t.Parallel()
	// Answers that resolve out of order never reorder the edits.
	t.Run("answers in reverse order", func(t *testing.T) {
		r := newFollowRig(t, "")
		r.addLines(t, filepath.Join(r.dirB, "a.txt"))
		r.addLines(t, filepath.Join(r.dirC, "c.txt"))
		r.open(t, r.id)

		// Each edit is a new path, so every one resolves through git.
		const edits = 20

		answers := make([][]tea.Msg, edits)

		for i := range edits {
			dir := r.dirB
			if i%2 == 1 {
				dir = r.dirC
			}

			answers[i] = flatten(hook(r.App, r.editHook(filepath.Join(dir, fmt.Sprintf("f%d.go", i)))))
		}

		for i := edits - 1; i >= 0; i-- {
			for _, m := range answers[i] {
				if m != nil {
					r.feed(t, m)
				}
			}
		}

		last, ok := r.pane().Followed()
		if !ok || last.Root != r.dirC || last.Seq != edits || r.pane().FollowDir() != r.dirC {
			t.Fatalf("Followed() = %+v, %v, want the last edit's root %q with Seq %d", last, ok, r.dirC, edits)
		}

		r.drive(t, "the drawer to settle on C", func() bool {
			v := r.view()

			return v != nil && v.root == r.rootC && v.loaded && !v.loading && r.held(r.id) == r.rootC
		})

		if got := viewPaths(r.view()); !slices.Equal(got, []string{"c.txt"}) {
			t.Errorf("drawer shows %v, want C's change", got)
		}
	})

	// The drawer moves between the worktrees for real, and the loads it started
	// for the roots it left come back last.
	t.Run("loads for a root the drawer left", func(t *testing.T) {
		r := newFollowRig(t, "")
		r.addLines(t, filepath.Join(r.dirB, "a.txt"))
		r.addLines(t, filepath.Join(r.dirC, "c.txt"))
		r.open(t, r.id)

		r.holdLoads = true

		moves := []struct{ dir, root string }{
			{r.dirB, r.rootB}, {r.dirC, r.rootC}, {r.dirB, r.rootB}, {r.dirC, r.rootC},
		}

		for i, mv := range moves {
			prev := r.view().epoch

			r.editNow(t, filepath.Join(mv.dir, fmt.Sprintf("g%d.go", i)))
			r.drive(t, "the drawer to move to "+mv.root, func() bool {
				v := r.view()

				return v != nil && v.root == mv.root && v.epoch != prev
			})
		}

		r.drive(t, "a load answer for every move", func() bool { return len(r.parked) >= len(moves) })

		r.holdLoads = false

		// Loads of the roots the drawer left are in flight, or the test shows nothing.
		if stale := slices.IndexFunc(r.parked, func(l reviewLoadedMsg) bool { return l.gen != r.view().epoch }); stale < 0 {
			t.Fatal("no load for a root the drawer left was in flight")
		}

		for _, v := range slices.Backward(r.parked) {
			r.feed(t, v)
		}

		v := r.view()
		if v.root != r.rootC || !v.loaded || v.loading || !slices.Equal(viewPaths(v), []string{"c.txt"}) {
			t.Errorf("drawer shows %v on %q (loaded %v, loading %v), want C's change", viewPaths(v), v.root, v.loaded, v.loading)
		}
	})
}

func TestNewSessionReturnsToOwnDirectory(t *testing.T) {
	t.Parallel()
	r := newFollowRig(t, testSession)
	r.addLines(t, filepath.Join(r.dirC, "c.txt"))
	r.open(t, r.id)
	r.openRail(t, "main")

	r.clk.Advance(11 * time.Second)
	r.edit(filepath.Join(r.dirC, "c.txt"))
	r.until(t, "the watch and the drawer to follow", r.onRoot(r.rootC))
	r.until(t, "the card to follow", r.cardOn("other", -1))

	r.clk.Advance(11 * time.Second)
	r.hookNow(harness.Event{Pane: r.id, Name: "SessionStart", Start: true, Session: otherSession, Cwd: r.dirA})
	r.until(t, "the watch and the drawer to return", r.onRoot(r.rootA))
	r.until(t, "the card to return", r.cardOn("main", -1))

	if p := r.pane(); len(p.Edits) != 0 || p.FollowDir() != r.dirA {
		t.Errorf("pane = %d edits, FollowDir %q, want none and %q", len(p.Edits), p.FollowDir(), r.dirA)
	}
}

func TestProbeHeadOf(t *testing.T) {
	t.Parallel()
	r := newFollowRig(t, "")
	scratch := t.TempDir()
	gone := filepath.Join(t.TempDir(), "gone")
	nameA := filepath.Base(r.dirA)

	tests := []struct {
		name         string
		dir, own     string
		project      string
		branch       string
		repo, isGone bool
	}{
		{"the same directory", r.dirA, r.dirA, nameA, "main", true, false},
		{"a worktree of the own repository", r.dirB, r.dirA, nameA, "b", true, false},
		{"another repository, named from the own directory", r.dirC, r.dirA, nameA, "other", true, false},
		{"own directory in a linked worktree", r.dirC, r.dirB, nameA, "other", true, false},
		{"own directory outside any repository", r.dirC, scratch, filepath.Base(scratch), "other", true, false},
		{"own directory gone", r.dirA, gone, "gone", "main", true, false},
		{"followed directory outside any repository", scratch, r.dirA, nameA, "", false, false},
		{"followed directory gone", gone, r.dirA, nameA, "", false, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := probeHeadOf(context.Background(), r.fake, r.log, tt.dir, tt.own)
			if err != nil {
				t.Fatalf("probeHeadOf: %v", err)
			}

			if got.project != tt.project || got.branch != tt.branch || got.repo != tt.repo || got.gone != tt.isGone ||
				!got.known || got.headCwd != tt.dir {
				t.Errorf("probeHeadOf(%q, %q) = %+v, want project %q, branch %q, repo %v, gone %v for %q",
					tt.dir, tt.own, got, tt.project, tt.branch, tt.repo, tt.isGone, tt.dir)
			}
		})
	}
}

// deliverAll is deliver that also runs the commands the answers raise, until none is left.
func (r *railApp) deliverAll(cmd tea.Cmd) []tea.Msg {
	var all []tea.Msg

	for range 10 {
		var cmds []tea.Cmd

		for _, m := range flatten(cmd) {
			if m != nil {
				all = append(all, m)
				cmds = append(cmds, r.step(m))
			}
		}

		if cmd = tea.Batch(cmds...); cmd == nil {
			break
		}
	}

	return all
}

func TestHeadProbeTargetIsFollowedAndOwnDirectory(t *testing.T) {
	t.Parallel()

	r := newRailApp(t, false)
	tr := newAgentTrees(t, r.fake)
	other := r.repo(t, "other").Root()
	id := r.addAgent(t, false, tr.main)
	r.openRail()

	r.clk.Advance(6 * time.Second)
	r.deliverAll(hook(r.App, harness.Event{Pane: id, Name: "PostToolUse", Edit: filepath.Join(tr.linked, "a.txt")}))

	if c, _ := r.card(id); c.branch != "feat" || c.project != filepath.Base(tr.main) {
		t.Fatalf("card = %+v, want branch feat of project %s", c, filepath.Base(tr.main))
	}

	// A move of the own directory alone probes the followed place again, and the
	// project follows the own directory.
	r.clk.Advance(6 * time.Second)

	heads, _ := probedCwds(r.deliverAll(hook(r.App, cwdHook(id, other))))
	if want := []string{tr.linked}; !slices.Equal(heads, want) {
		t.Fatalf("head probes for %v after the own directory moved, want %v", heads, want)
	}

	if c, _ := r.card(id); c.branch != "feat" || c.project != "other" {
		t.Fatalf("card = %+v, want branch feat of project other", c)
	}

	// An answer is stale when either directory changed since it started.
	for _, msg := range []railHeadMsg{
		{pane: id, cwd: tr.linked, own: tr.main, res: railProbe{project: "stale", branch: "stale", known: true, headCwd: tr.linked}},
		{pane: id, cwd: tr.main, own: other, res: railProbe{project: "stale", branch: "stale", known: true, headCwd: tr.main}},
	} {
		r.step(msg)

		if c, _ := r.card(id); c.project != "other" || c.branch != "feat" {
			t.Errorf("card = %+v after a stale answer for (%s, %s), want it unchanged", c, msg.cwd, msg.own)
		}
	}
}

// pump delivers cmd's messages and those of the commands they raise, except the
// probes' answers and due messages: those come back undelivered, so a test
// decides in which order they arrive.
func (r *railApp) pump(cmd tea.Cmd) []tea.Msg {
	var held []tea.Msg

	for range 10 {
		var cmds []tea.Cmd

		for _, m := range flatten(cmd) {
			switch m.(type) {
			case nil:
			case railHeadMsg, railStatsMsg, railDueMsg:
				held = append(held, m)
			default:
				cmds = append(cmds, r.step(m))
			}
		}

		if cmd = tea.Batch(cmds...); cmd == nil {
			break
		}
	}

	return held
}

// release steps the held messages and returns what they raise, still held.
func (r *railApp) release(held []tea.Msg) []tea.Msg {
	var next []tea.Msg

	for _, m := range held {
		next = append(next, r.pump(r.step(m))...)
	}

	return next
}

// movedFollow is an agent launched in a repository's main checkout, whose card
// shows branch main with one added line, followed to a linked worktree (branch
// feat, three added lines) by an edit a second later: the probes of the new
// place wait out their intervals, the head's for 4 s more and the stats' for 9 s.
type movedFollow struct {
	*railApp
	id       core.PaneID
	linked   string
	dueHead  railDueMsg
	dueStats railDueMsg
}

func newMovedFollow(t *testing.T) *movedFollow {
	t.Helper()

	r := newRailApp(t, true)
	tr := newAgentTrees(t, r.fake)
	write(t, filepath.Join(tr.main, "a.txt"), "one\ntwo\n")
	write(t, filepath.Join(tr.linked, "a.txt"), "one\ntwo\nthree\nfour\n")

	id := r.addAgent(t, false, tr.main)
	r.openRail()

	if c, _ := r.card(id); c.branch != "main" || !c.statsKnown || c.stats.Added != 1 {
		t.Fatalf("setup: card = %+v, want main with one added line", c)
	}

	r.clk.Advance(time.Second)

	held := r.pump(hook(r.App, harness.Event{Pane: id, Name: "PostToolUse", Edit: filepath.Join(tr.linked, "a.txt")}))
	if countOf[railDueMsg](held) != 2 || len(held) != 2 {
		t.Fatalf("an edit inside both intervals ran %v, want one delayed head probe and one delayed stats probe", held)
	}

	return &movedFollow{
		railApp:  r,
		id:       id,
		linked:   tr.linked,
		dueHead:  railDueMsg{pane: id, kind: probeHead},
		dueStats: railDueMsg{pane: id, kind: probeStats, gen: r.rail.gen},
	}
}

// onePlace fails the test when the card shows one place's branch with the
// other's counts.
func (m *movedFollow) onePlace(t *testing.T, when string) {
	t.Helper()

	c, ok := m.card(m.id)
	if !ok {
		t.Fatalf("%s: no card", when)
	}

	if c.statsKnown && (c.branch != "main" || c.stats.Added != 1) && (c.branch != "feat" || c.stats.Added != 3) {
		t.Errorf("%s: the card shows branch %q with %d added lines", when, c.branch, c.stats.Added)
	}
}

// answers delivers the held messages one at a time, the first that early accepts
// before the others, and does the same with what each raises. It checks the card
// after every delivery.
func (m *movedFollow) answers(t *testing.T, held []tea.Msg, early func(tea.Msg) bool) {
	t.Helper()

	for range 10 {
		if len(held) == 0 {
			return
		}

		i := max(slices.IndexFunc(held, early), 0)
		msg := held[i]
		held = append(slices.Delete(held, i, i+1), m.pump(m.step(msg))...)

		m.onePlace(t, "after an answer")
	}
}

func isMsg[T any](msg tea.Msg) bool {
	_, ok := msg.(T)

	return ok
}

func TestCardCountsStayOnThePlaceItNames(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		run  func(t *testing.T, m *movedFollow)
	}{
		{"the head answers first", func(t *testing.T, m *movedFollow) {
			m.clk.Advance(4 * time.Second)
			m.answers(t, m.pump(m.step(m.dueHead)), isMsg[railHeadMsg])

			m.clk.Advance(5 * time.Second)
			m.answers(t, m.pump(m.step(m.dueStats)), isMsg[railHeadMsg])
		}},
		// The next update starts the head probe as soon as its interval is over, so
		// the stats probe's due message starts both.
		{"the stats answer first", func(t *testing.T, m *movedFollow) {
			m.clk.Advance(9 * time.Second)
			m.answers(t, m.pump(m.step(m.dueStats)), isMsg[railStatsMsg])
		}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			m := newMovedFollow(t)
			m.onePlace(t, "after the edit")

			tt.run(t, m)

			if c, _ := m.card(m.id); c.branch != "feat" || !c.statsKnown || c.stats.Added != 3 {
				t.Errorf("card = %+v, want feat with its three added lines", c)
			}
		})
	}
}

func TestPlaceChangeProbesItsStatsAtOnce(t *testing.T) {
	t.Parallel()

	m := newMovedFollow(t)

	// The head's answer comes with 5 s left of the stats interval.
	m.clk.Advance(4 * time.Second)

	held := m.release(m.pump(m.step(m.dueHead)))
	if len(held) != 1 {
		t.Fatalf("the head answer for the new place raised %v, want one stats probe", held)
	}

	if sm, ok := held[0].(railStatsMsg); !ok || sm.cwd != m.linked {
		t.Fatalf("the head answer raised %+v, want a stats probe of %s", held[0], m.linked)
	}

	m.release(held)

	if c, _ := m.card(m.id); c.branch != "feat" || !c.statsKnown || c.stats.Added != 3 {
		t.Errorf("card = %+v, want feat with its three added lines", c)
	}
}

func TestPlaceChangeWithTheRailClosedCountsWhenItOpens(t *testing.T) {
	t.Parallel()

	m := newMovedFollow(t)
	m.railClosed()

	m.clk.Advance(4 * time.Second)

	if held := m.release(m.pump(m.step(m.dueHead))); len(held) != 0 {
		t.Fatalf("a head answer with the rail closed raised %v, want nothing", held)
	}

	// The stats interval has 5 s left; the move's own force outlasts the closed rail.
	held := m.pump(m.railOpened())
	if len(held) != 1 {
		t.Fatalf("opening the rail raised %v, want one stats probe", held)
	}

	if sm, ok := held[0].(railStatsMsg); !ok || sm.cwd != m.linked {
		t.Fatalf("opening the rail raised %+v, want a stats probe of %s", held[0], m.linked)
	}
}
