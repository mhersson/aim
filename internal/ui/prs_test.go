package ui

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/mhersson/aim/internal/core"
	"github.com/mhersson/aim/internal/github"
	"github.com/mhersson/aim/internal/harness"
)

const prRepoKey = "github.com/o/r"

var prRepo = github.Repo{Host: "github.com", Owner: "o", Name: "r"}

// fakePRs stands in for the poller. Its updates channel is closed, so the
// command that waits on it returns at once and a churn never blocks.
type fakePRs struct {
	mu   sync.Mutex
	sets [][]github.Watch
	ch   chan github.Snapshot
}

func newFakePRs() *fakePRs {
	f := &fakePRs{ch: make(chan github.Snapshot)}
	close(f.ch)

	return f
}

func (f *fakePRs) Set(ws []github.Watch) {
	f.mu.Lock()
	defer f.mu.Unlock()

	f.sets = append(f.sets, slices.Clone(ws))
}

func (f *fakePRs) Updates() <-chan github.Snapshot { return f.ch }

func (f *fakePRs) Refresh(github.Repo) {}

func (f *fakePRs) setCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()

	return len(f.sets)
}

func (f *fakePRs) last() []github.Watch {
	f.mu.Lock()
	defer f.mu.Unlock()

	if len(f.sets) == 0 {
		return nil
	}

	return f.sets[len(f.sets)-1]
}

// textResolver resolves remote URLs by their text, so a test reads neither ssh
// nor gh's configuration. Only github.com is a GitHub host.
type textResolver struct{ calls *atomic.Int32 }

func (r textResolver) Resolve(_ context.Context, raw string) (github.Repo, error) {
	r.calls.Add(1)

	repo, err := github.ParseRemoteURL(raw)
	if err != nil {
		return github.Repo{}, err
	}

	if repo.Host != "github.com" {
		return github.Repo{}, fmt.Errorf("%w: %s", github.ErrNotGitHub, repo)
	}

	return repo, nil
}

// prApp is a rail app polling pull requests through a fake source, with a fake
// branch lookup and a resolver that reads remote URLs as text.
type prApp struct {
	*railApp
	src      *fakePRs
	resolves atomic.Int32

	mu      sync.Mutex
	lookups []string
	found   map[string]github.PR // what a lookup finds, by branch
}

// newPRApp builds on the fake rail rig when opts carry a fake git (gitOf), else on the real one.
func newPRApp(t *testing.T, opts ...func(*Options)) *prApp {
	t.Helper()

	p := &prApp{src: newFakePRs(), found: map[string]github.PR{}}

	var probe Options
	for _, f := range opts {
		f(&probe)
	}

	if f, ok := probe.Git.(*fakeGit); ok {
		// No watch set after the options: the PR tests count probes, as the real rig does.
		p.railApp = newRailApp(t, false, append(opts, func(o *Options) { o.Watches = nil })...)
		p.fake = f
	} else {
		p.railApp = newRealRailApp(t, false)
	}

	p.prs = newPRState(p.src, p.lookup, p.git)
	p.prs.resolve = newPRResolver(p.git, textResolver{calls: &p.resolves}).resolve

	return p
}

func (p *prApp) lookup(_ context.Context, r github.Repo, branch string) (github.PR, error) {
	p.mu.Lock()
	defer p.mu.Unlock()

	p.lookups = append(p.lookups, r.String()+" "+branch)

	if pr, ok := p.found[branch]; ok {
		return pr, nil
	}

	return github.PR{}, fmt.Errorf("lookup %s: %w", branch, github.ErrNoPR)
}

func (p *prApp) lookedUp() []string {
	p.mu.Lock()
	defer p.mu.Unlock()

	return slices.Clone(p.lookups)
}

// prRepo makes a repository on branch, with an origin at url unless that is empty.
func (r *railApp) prRepo(t *testing.T, name, branch, url string) string {
	t.Helper()

	dir := r.realRepo(t, name)
	gitIn(t, r.real, dir, "checkout", "-q", "-b", branch)

	if url != "" {
		gitIn(t, r.real, dir, "remote", "add", "origin", url)
	}

	_ = os.Remove(r.log)

	return dir
}

// churn runs cmd and feeds every message back to the app, then the commands
// those raise, until none is left.
func (r *railApp) churn(t *testing.T, cmd tea.Cmd) {
	t.Helper()

	queue := []tea.Cmd{cmd}

	for rounds := 0; len(queue) > 0; rounds++ {
		if rounds > 200 {
			t.Fatal("commands keep raising commands")
		}

		next := queue[0]
		queue = queue[1:]

		for _, m := range flatten(next) {
			if m != nil {
				queue = append(queue, r.step(m))
			}
		}
	}
}

func snapshotOf(prs ...github.PR) prSnapshotMsg { return snapshotOfRepo(prRepo, prs...) }

func snapshotOfRepo(repo github.Repo, prs ...github.PR) prSnapshotMsg {
	return prSnapshotMsg{Repo: repo, Info: github.RepoInfo{DefaultBranch: "main"}, PRs: prs}
}

func openPRIn(n int, branch string) github.PR {
	return github.PR{Number: n, HeadBranch: branch, State: "OPEN"}
}

func (p *prApp) prFor(branch string) (core.PRInfo, bool) {
	return p.store.State().PRFor(prRepoKey, branch)
}

func TestTwoAgentsInOneRepositoryShareOneWatch(t *testing.T) {
	p := newPRApp(t)
	dir := p.prRepo(t, "proj", "feat-a", "https://github.com/o/r.git")
	wt := filepath.Join(t.TempDir(), "wt")
	gitIn(t, p.real, dir, "worktree", "add", "-q", "-b", "feat-b", wt)

	p.addAgent(t, false, dir)
	p.addAgent(t, false, wt)
	p.churn(t, p.settle())

	want := []github.Watch{{Repo: prRepo}}
	if got := p.src.last(); !reflect.DeepEqual(got, want) {
		t.Fatalf("watches = %+v, want one for %s with nothing visible yet", got, prRepo)
	}

	p.churn(t, p.step(snapshotOf(openPRIn(7, "feat-a"), openPRIn(8, "feat-b"), openPRIn(9, "elsewhere"))))

	want[0].Visible = []int{7, 8}
	if got := p.src.last(); !reflect.DeepEqual(got, want) {
		t.Errorf("watches = %+v, want one with PRs 7 and 8 visible", got)
	}

	if pr, ok := p.prFor("feat-b"); !ok || pr.Number != 8 {
		t.Errorf("PR of feat-b = #%d, %v, want #8", pr.Number, ok)
	}
}

func TestClosingAnAgentDropsItsRepository(t *testing.T) {
	p := newPRApp(t)
	one := p.prRepo(t, "one", "feat", "https://github.com/o/r.git")
	two := p.prRepo(t, "two", "feat", "git@github.com:o/s.git")

	first := p.addAgent(t, false, one)
	second := p.addAgent(t, false, two)
	p.churn(t, p.settle())

	repos := func() []string {
		var out []string

		for _, w := range p.src.last() {
			out = append(out, w.Repo.String())
		}

		return out
	}

	if got := repos(); !slices.Equal(got, []string{"github.com/o/r", "github.com/o/s"}) {
		t.Fatalf("watched = %v, want both repositories in order", got)
	}

	openIn(t, p.App, core.ClosePane{ID: second})
	p.churn(t, p.settle())

	if got := repos(); !slices.Equal(got, []string{"github.com/o/r"}) {
		t.Fatalf("watched = %v after closing the second agent, want the first alone", got)
	}

	sets := p.src.setCount()

	openIn(t, p.App, core.ClosePane{ID: first})
	p.churn(t, p.settle())

	if got := p.src.last(); len(got) != 0 || p.src.setCount() != sets+1 {
		t.Errorf("last set = %+v after %d sets, want an empty one sent", got, p.src.setCount())
	}
}

func TestFocusedShellRepositoryIsWatched(t *testing.T) {
	p := newPRApp(t)
	dir := p.prRepo(t, "proj", "feat", "https://github.com/o/r.git")

	openIn(t, p.App, core.NewTab{Kind: core.KindShell, Cwd: dir})
	p.churn(t, p.settle())

	if got := p.src.last(); len(got) != 1 || got[0].Repo != prRepo {
		t.Fatalf("watches = %+v, want the focused shell's repository", got)
	}

	// The shell's own branch counts as visible.
	p.churn(t, p.step(snapshotOf(openPRIn(4, "feat"))))

	if got := p.src.last(); len(got) != 1 || !slices.Equal(got[0].Visible, []int{4}) {
		t.Errorf("watches = %+v, want PR 4 visible", got)
	}

	openIn(t, p.App, core.SelectTab{Index: 0})
	p.churn(t, p.settle())

	if got := p.src.last(); len(got) != 0 {
		t.Errorf("watches = %+v after the focus moved off, want none", got)
	}
}

// repoAt is a GitHub repository of the owner o.
func repoAt(name string) github.Repo { return github.Repo{Host: "github.com", Owner: "o", Name: name} }

// followRepos is an agent in repository a, on feat-a with remote o/a, and
// repository b, on feat-b with remote o/b, where the agent has not been yet.
func (p *prApp) followRepos(t *testing.T) (agent core.PaneID, dirB string) {
	t.Helper()

	dirA := p.prRepo(t, "a", "feat-a", "https://github.com/o/a.git")
	dirB = p.prRepo(t, "b", "feat-b", "https://github.com/o/b.git")

	agent = p.addAgent(t, false, dirA)
	p.churn(t, p.settle())

	return agent, dirB
}

// editRepo has the agent edit a file of the repository at dir after the head
// probe's throttle, and answers what that starts.
func (p *prApp) editRepo(t *testing.T, agent core.PaneID, dir string) {
	t.Helper()

	p.clk.Advance(6 * time.Second)
	p.churn(t, hook(p.App, harness.Event{Pane: agent, Name: "PostToolUse", Edit: filepath.Join(dir, "a.txt")}))
}

func TestPollerWatchesFollowedRepository(t *testing.T) {
	p := newPRApp(t)
	agent, dirB := p.followRepos(t)

	if got, want := p.src.last(), []github.Watch{{Repo: repoAt("a")}}; !reflect.DeepEqual(got, want) {
		t.Fatalf("watches = %+v before the edit, want %+v", got, want)
	}

	sets := p.src.setCount()

	p.editRepo(t, agent, dirB)

	if p.src.setCount() == sets {
		t.Fatal("the poller was given no new watch set after the edit")
	}

	if got, want := p.src.last(), []github.Watch{{Repo: repoAt("b")}}; !reflect.DeepEqual(got, want) {
		t.Errorf("watches = %+v after the edit, want only %+v", got, want)
	}
}

func TestSnapshotDispatchesTheRepositoryPRs(t *testing.T) {
	p := newPRApp(t)
	updated := time.Date(2026, 10, 29, 12, 0, 0, 0, time.UTC)

	pr := github.PR{
		Number: 12, Title: "Add the thing", URL: "https://github.com/o/r/pull/12", Author: "ada", State: "OPEN",
		Draft: true, CrossRepo: false, HeadBranch: "feat", HeadOID: "abc", BaseBranch: "main",
		ReviewDecision: "APPROVED", MergeState: "CLEAN",
		Checks:    github.Checks{State: github.CheckPending, Total: 5, Done: 3, Failed: 1},
		UpdatedAt: updated,
	}

	p.step(snapshotOf(pr))

	want := core.RepoPRs{
		DefaultBranch: "main",
		Open: []core.PRInfo{{
			Number: 12, Title: "Add the thing", URL: "https://github.com/o/r/pull/12", Author: "ada", State: "OPEN",
			HeadBranch: "feat", HeadOID: "abc", BaseBranch: "main", ReviewDecision: "APPROVED", MergeState: "CLEAN",
			Draft: true, ChecksTotal: 5, ChecksDone: 3, ChecksFailed: 1, UpdatedAt: updated,
		}},
	}

	if got := p.store.State().PRs[prRepoKey]; !reflect.DeepEqual(got, want) {
		t.Errorf("PRs = %+v, want %+v", got, want)
	}
}

func TestIdenticalPollsDispatchNothing(t *testing.T) {
	p := newPRApp(t)

	p.step(snapshotOf(openPRIn(1, "a")))

	ver := p.storeVer

	p.step(snapshotOf(openPRIn(1, "a")))

	if p.storeVer != ver {
		t.Errorf("an unchanged poll dispatched %d times, want none", p.storeVer-ver)
	}
}

func errSnapshot(err error) prSnapshotMsg {
	return prSnapshotMsg{Repo: prRepo, Err: err}
}

func TestPollErrorIsShownOnce(t *testing.T) {
	p := newPRApp(t)
	p.step(snapshotOf(openPRIn(1, "a")))

	boom := errors.New("boom")

	p.step(errSnapshot(boom))

	if want := "pull requests for github.com/o/r: boom"; p.notice != want {
		t.Fatalf("notice = %q, want %q", p.notice, want)
	}

	got := p.store.State().PRs[prRepoKey]
	if got.Err != "boom" || len(got.Open) != 1 || got.DefaultBranch != "main" {
		t.Errorf("repository = %+v, want the last good poll kept with the error set", got)
	}

	// The same error again is not shown again.
	p.notice = ""
	p.step(errSnapshot(boom))

	if p.notice != "" {
		t.Errorf("notice = %q for the same error, want none", p.notice)
	}

	// Another error is.
	p.step(errSnapshot(errors.New("bang")))

	if want := "pull requests for github.com/o/r: bang"; p.notice != want {
		t.Errorf("notice = %q, want %q", p.notice, want)
	}

	// A recovery clears the error, and a later failure is shown again.
	p.step(snapshotOf(openPRIn(1, "a")))

	if got := p.store.State().PRs[prRepoKey]; got.Err != "" {
		t.Errorf("Err = %q after a good poll, want none", got.Err)
	}

	p.notice = ""
	p.step(errSnapshot(boom))

	if p.notice == "" {
		t.Error("an error after a recovery was not shown")
	}
}

func TestMissingTokenShowsTheBackendsReason(t *testing.T) {
	p := newPRApp(t)

	noToken := fmt.Errorf("github: client for github.com: %w",
		fmt.Errorf("%w for github.com: run gh auth login --hostname github.com", github.ErrNoToken))

	p.step(errSnapshot(noToken))

	if want := "pull requests for github.com/o/r: run gh auth login --hostname github.com"; p.notice != want {
		t.Errorf("notice = %q, want %q", p.notice, want)
	}
}

func TestPollErrorReasonIsOneCleanLine(t *testing.T) {
	p := newPRApp(t)

	p.step(errSnapshot(errors.New("bad\x1b[31m\nreply")))

	if want := "pull requests for github.com/o/r: bad[31mreply"; p.notice != want {
		t.Errorf("notice = %q, want %q", p.notice, want)
	}
}

func TestRepositoriesThatAreNotOnGitHubAreSilent(t *testing.T) {
	tests := []struct {
		name string
		url  string // "" for no remote
		mk   func(t *testing.T, p *prApp) string
	}{
		{"another host", "https://gitlab.example.com/o/r.git", nil},
		{"no remote", "", nil},
		{"not a repository", "", func(t *testing.T, _ *prApp) string { return t.TempDir() }},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := newPRApp(t)

			var dir string
			if tt.mk != nil {
				dir = tt.mk(t, p)
			} else {
				dir = p.prRepo(t, "proj", "feat", tt.url)
			}

			p.addAgent(t, false, dir)
			p.churn(t, p.settle())

			if n := p.src.setCount(); n != 0 {
				t.Errorf("the source was set %d times (last %+v), want never", n, p.src.last())
			}

			if got := p.lookedUp(); len(got) != 0 {
				t.Errorf("lookups = %v, want none", got)
			}

			if p.notice != "" {
				t.Errorf("notice = %q, want none", p.notice)
			}
		})
	}
}

func TestBranchLookupFollowsTheOpenPRLeavingTheList(t *testing.T) {
	p := newPRApp(t)
	dir := p.prRepo(t, "proj", "feat", "https://github.com/o/r.git")
	p.addAgent(t, false, dir)
	p.churn(t, p.settle())

	// A branch with no open PR is looked up once.
	p.churn(t, p.step(snapshotOf(openPRIn(1, "other"))))

	if got := p.lookedUp(); !slices.Equal(got, []string{prRepoKey + " feat"}) {
		t.Fatalf("lookups = %v, want one for feat", got)
	}

	p.churn(t, p.step(snapshotOf(openPRIn(1, "other"))))

	if got := p.lookedUp(); len(got) != 1 {
		t.Fatalf("lookups = %v after another poll, want still one", got)
	}

	// An open PR needs no lookup.
	p.churn(t, p.step(snapshotOf(openPRIn(7, "feat"))))

	if got := p.lookedUp(); len(got) != 1 {
		t.Fatalf("lookups = %v while the PR is open, want still one", got)
	}

	if pr, ok := p.prFor("feat"); !ok || pr.Number != 7 || pr.State != "OPEN" {
		t.Fatalf("PR of feat = %+v, %v, want #7 open", pr, ok)
	}

	// It merges: the poll's list loses it and one lookup records how it ended.
	p.found["feat"] = github.PR{Number: 7, State: "MERGED", HeadBranch: "feat"}

	p.churn(t, p.step(snapshotOf(openPRIn(1, "other"))))

	if got := p.lookedUp(); len(got) != 2 {
		t.Fatalf("lookups = %v after the PR left the list, want exactly one more", got)
	}

	if pr, ok := p.prFor("feat"); !ok || pr.Number != 7 || pr.State != "MERGED" {
		t.Errorf("PR of feat = %+v, %v, want #7 merged", pr, ok)
	}

	p.churn(t, p.step(snapshotOf(openPRIn(1, "other"))))

	if got := p.lookedUp(); len(got) != 2 {
		t.Errorf("lookups = %v after a further poll, want still two", got)
	}
}

func TestBranchWithNoPRRecordsNothing(t *testing.T) {
	p := newPRApp(t)
	dir := p.prRepo(t, "proj", "feat", "https://github.com/o/r.git")
	p.addAgent(t, false, dir)
	p.churn(t, p.settle())

	p.churn(t, p.step(snapshotOf()))

	if _, ok := p.prFor("feat"); ok {
		t.Error("a branch the lookup found nothing for has a PR")
	}

	if got := p.store.State().PRs[prRepoKey].Branch; len(got) != 0 {
		t.Errorf("Branch = %+v, want none", got)
	}
}

// A lookup that reads open is no poll: open PRs come only from the poll's list.
func TestLookupFindingAnOpenPRRecordsNothing(t *testing.T) {
	p := newPRApp(t)
	dir := p.prRepo(t, "proj", "feat", "https://github.com/o/r.git")
	p.addAgent(t, false, dir)
	p.churn(t, p.settle())

	// The PR is open but not among the poll's PRs, as when it is older than the
	// poll's page or was opened since the poll.
	p.found["feat"] = github.PR{Number: 11, State: "OPEN", HeadBranch: "feat"}

	p.churn(t, p.step(snapshotOf()))

	if got := p.lookedUp(); len(got) != 1 {
		t.Fatalf("lookups = %v, want one", got)
	}

	if pr, ok := p.prFor("feat"); ok {
		t.Errorf("PR of feat = #%d from a lookup that read open, want none", pr.Number)
	}

	if got := p.store.State().PRs[prRepoKey].Branch; len(got) != 0 {
		t.Errorf("Branch = %+v, want nothing recorded", got)
	}

	if got := p.src.last(); len(got) != 1 || len(got[0].Visible) != 0 {
		t.Errorf("watches = %+v, want nothing visible", got)
	}

	// It counts as looked up while the poll does not list it.
	p.churn(t, p.step(snapshotOf()))

	if got := p.lookedUp(); len(got) != 1 {
		t.Fatalf("lookups = %v after another poll, want still one", got)
	}

	// Once the poll lists it, it is visible; when it leaves the list, the next
	// lookup records how it ended.
	p.churn(t, p.step(snapshotOf(openPRIn(11, "feat"))))

	if got := p.src.last(); len(got) != 1 || !slices.Equal(got[0].Visible, []int{11}) {
		t.Fatalf("watches = %+v, want PR 11 visible once the poll lists it", got)
	}

	p.found["feat"] = github.PR{Number: 11, State: "MERGED", HeadBranch: "feat"}

	p.churn(t, p.step(snapshotOf()))

	if got := p.lookedUp(); len(got) != 2 {
		t.Fatalf("lookups = %v after the PR left the list, want exactly one more", got)
	}

	if pr, ok := p.prFor("feat"); !ok || pr.Number != 11 || pr.State != "MERGED" {
		t.Errorf("PR of feat = %+v, %v, want #11 merged", pr, ok)
	}
}

func TestNewBranchOfAPolledRepositoryIsLookedUpWhenSeen(t *testing.T) {
	p := newPRApp(t)
	dir := p.prRepo(t, "proj", "feat-a", "https://github.com/o/r.git")
	wt := filepath.Join(t.TempDir(), "wt")
	gitIn(t, p.real, dir, "worktree", "add", "-q", "-b", "feat-b", wt)

	p.addAgent(t, false, dir)
	p.churn(t, p.settle())
	p.churn(t, p.step(snapshotOf()))

	if got := p.lookedUp(); !slices.Equal(got, []string{prRepoKey + " feat-a"}) {
		t.Fatalf("lookups = %v, want one for feat-a", got)
	}

	// The repository is polled already, so the next snapshot is not waited for.
	p.addAgent(t, false, wt)
	p.churn(t, p.settle())

	if got := p.lookedUp(); !slices.Equal(got, []string{prRepoKey + " feat-a", prRepoKey + " feat-b"}) {
		t.Errorf("lookups = %v, want feat-b looked up as soon as it was seen", got)
	}
}

func TestLookupFailureIsAskedAgainAtTheNextPoll(t *testing.T) {
	p := newPRApp(t)
	dir := p.prRepo(t, "proj", "feat", "https://github.com/o/r.git")
	p.addAgent(t, false, dir)
	p.churn(t, p.settle())

	var calls atomic.Int32

	p.prs.lookup = func(context.Context, github.Repo, string) (github.PR, error) {
		if calls.Add(1) == 1 {
			return github.PR{}, errors.New("flaky")
		}

		return github.PR{Number: 3, State: "CLOSED", HeadBranch: "feat"}, nil
	}

	p.churn(t, p.step(snapshotOf()))

	if p.notice != "" {
		t.Errorf("notice = %q for a failed lookup, want none", p.notice)
	}

	if _, ok := p.prFor("feat"); ok {
		t.Fatal("a failed lookup recorded a PR")
	}

	p.churn(t, p.step(snapshotOf()))

	if pr, ok := p.prFor("feat"); !ok || pr.Number != 3 || pr.State != "CLOSED" {
		t.Errorf("PR of feat = %+v, %v, want #3 closed after the retry", pr, ok)
	}
}

func TestResolutionRunsOncePerBranchAndRemote(t *testing.T) {
	p := newPRApp(t)
	dir := p.prRepo(t, "proj", "feat", "https://github.com/o/r.git")

	// Two panes in one worktree resolve the same remote once between them.
	p.addAgent(t, false, dir)
	p.addAgent(t, false, dir)
	p.churn(t, p.settle())

	if n := p.resolves.Load(); n != 1 {
		t.Errorf("Resolve ran %d times for one worktree and remote, want 1", n)
	}

	if got := p.src.last(); len(got) != 1 {
		t.Fatalf("watches = %+v, want one", got)
	}

	// Idle updates ask git for nothing more.
	calls := p.gitCalls()

	for range 5 {
		p.churn(t, p.settle())
	}

	if got := p.gitCalls(); got != calls {
		t.Errorf("git ran %d more times over idle updates, want none", got-calls)
	}
}

func TestResolutionFollowsABranchSwitch(t *testing.T) {
	p := newPRApp(t)
	dir := p.prRepo(t, "proj", "feat", "https://github.com/o/r.git")
	id := p.addAgent(t, false, dir)
	p.churn(t, p.settle())
	p.churn(t, p.step(snapshotOf(openPRIn(5, "feat"), openPRIn(6, "next"))))

	if got := p.src.last(); len(got) != 1 || !slices.Equal(got[0].Visible, []int{5}) {
		t.Fatalf("watches = %+v, want PR 5 visible", got)
	}

	gitIn(t, p.real, dir, "checkout", "-q", "-b", "next")
	p.rail.panes[id].head.force = true
	p.churn(t, p.railFire(p.store.State(), id, probeHead))

	if got := p.src.last(); len(got) != 1 || !slices.Equal(got[0].Visible, []int{6}) {
		t.Errorf("watches = %+v after the switch, want PR 6 visible", got)
	}
}

func TestFailedResolutionIsTriedAgainAtTheNextHeadProbe(t *testing.T) {
	p := newPRApp(t)
	dir := p.prRepo(t, "proj", "feat", "https://github.com/o/r.git")

	var calls atomic.Int32

	p.prs.resolve = func(context.Context, string, string) (github.Repo, error) {
		if calls.Add(1) == 1 {
			return github.Repo{}, errors.New("timed out")
		}

		return prRepo, nil
	}

	id := p.addAgent(t, false, dir)
	p.churn(t, p.settle())

	if n := p.src.setCount(); n != 0 || p.notice != "" {
		t.Fatalf("after a failed resolution: %d sets, notice %q, want neither", n, p.notice)
	}

	// Nothing but a head result asks again.
	for range 3 {
		p.churn(t, p.settle())
	}

	if calls.Load() != 1 {
		t.Fatalf("resolution ran %d times before a new head result, want 1", calls.Load())
	}

	p.rail.panes[id].head.force = true
	p.churn(t, p.railFire(p.store.State(), id, probeHead))

	if got := p.src.last(); len(got) != 1 || got[0].Repo != prRepo {
		t.Errorf("watches = %+v after the retry, want the repository", got)
	}
}

// An answer that ended with the app's context is neither news nor a failure to retry.
func TestCancelledAnswersAreNoFailure(t *testing.T) {
	p := newPRApp(t)
	dir := p.prRepo(t, "proj", "feat", "https://github.com/o/r.git")
	id := p.addAgent(t, false, dir)
	p.churn(t, p.settle())

	pp := p.prs.panes[id]
	if pp == nil || !pp.ok {
		t.Fatalf("setup: the pane's repository is not resolved: %+v", pp)
	}

	key, resolves := pp.key, p.resolves.Load()

	p.step(prResolvedMsg{id: id, key: key, err: context.Canceled})

	if pp.failed || !pp.ok || pp.key != key {
		t.Errorf("pane = %+v after a cancelled resolution, want it as it was", pp)
	}

	if p.notice != "" {
		t.Errorf("notice = %q after a cancelled resolution, want none", p.notice)
	}

	// Nothing but a head result retries, and a cancelled answer asks for none.
	p.churn(t, p.settle())

	if p.resolves.Load() != resolves {
		t.Error("a cancelled resolution was tried again")
	}

	p.step(errSnapshot(fmt.Errorf("github: query PullRequests: %w", context.Canceled)))

	if p.notice != "" {
		t.Errorf("notice = %q after a cancelled poll, want none", p.notice)
	}

	if _, ok := p.store.State().PRs[prRepoKey]; ok {
		t.Error("a cancelled poll left a repository in the state")
	}
}

func TestAnswerForAnotherBranchIsDropped(t *testing.T) {
	p := newPRApp(t)
	dir := p.prRepo(t, "proj", "feat", "https://github.com/o/r.git")
	id := p.addAgent(t, false, dir)
	p.churn(t, p.settle())

	other := github.Repo{Host: "github.com", Owner: "x", Name: "y"}
	sets := p.src.setCount()

	p.step(prResolvedMsg{id: id, key: prKey{cwd: dir, branch: "old"}, repo: other})
	p.settle()

	if p.src.setCount() != sets {
		t.Errorf("a stale answer changed the watches to %+v", p.src.last())
	}
}

func TestPollerAndResolverEndWithTheApp(t *testing.T) {
	t.Run("waiting for a snapshot", func(t *testing.T) {
		p := newPRApp(t)
		p.src.ch = make(chan github.Snapshot)

		done := make(chan tea.Msg, 1)

		go func() { done <- p.waitPRs()() }()

		select {
		case m := <-done:
			t.Fatalf("waitPRs returned %v before anything arrived", m)
		case <-time.After(50 * time.Millisecond):
		}

		closeApp(t, p.App)

		select {
		case m := <-done:
			if m != nil {
				t.Errorf("waitPRs returned %v after the app closed, want nothing", m)
			}
		case <-time.After(5 * time.Second):
			t.Fatal("waitPRs still waits after the app closed")
		}
	})

	t.Run("a snapshot is delivered", func(t *testing.T) {
		p := newPRApp(t)
		p.src.ch = make(chan github.Snapshot, 1)

		p.src.ch <- github.Snapshot{Repo: prRepo}

		if m, ok := p.waitPRs()().(prSnapshotMsg); !ok || m.Repo != prRepo {
			t.Errorf("waitPRs returned %v, want the snapshot", m)
		}
	})

	t.Run("a closed channel ends the wait", func(t *testing.T) {
		p := newPRApp(t)

		if m := p.waitPRs()(); m != nil {
			t.Errorf("waitPRs returned %v from a closed channel, want nothing", m)
		}
	})

	t.Run("resolving", func(t *testing.T) {
		p := newPRApp(t)
		p.prs.resolve = func(ctx context.Context, _, _ string) (github.Repo, error) {
			<-ctx.Done()

			return github.Repo{}, ctx.Err()
		}

		done := make(chan tea.Msg, 1)
		cmd := p.prResolveCmd("p1", prKey{cwd: "/w", branch: "b"})

		go func() { done <- cmd() }()

		select {
		case m := <-done:
			t.Fatalf("the resolution returned %v before the app closed", m)
		case <-time.After(50 * time.Millisecond):
		}

		closeApp(t, p.App)

		select {
		case m := <-done:
			if got, ok := m.(prResolvedMsg); !ok || !errors.Is(got.err, context.Canceled) {
				t.Errorf("the resolution returned %+v, want a cancelled one", m)
			}
		case <-time.After(5 * time.Second):
			t.Fatal("the resolution outlived the app's context")
		}
	})

	t.Run("looking up", func(t *testing.T) {
		p := newPRApp(t)
		p.prs.lookup = func(ctx context.Context, _ github.Repo, _ string) (github.PR, error) {
			<-ctx.Done()

			return github.PR{}, ctx.Err()
		}

		done := make(chan tea.Msg, 1)
		cmd := p.prLookupCmd(core.State{}, prRepo, "b")

		go func() { done <- cmd() }()

		closeApp(t, p.App)

		select {
		case m := <-done:
			if got, ok := m.(prBranchMsg); !ok || !errors.Is(got.err, context.Canceled) {
				t.Errorf("the lookup returned %+v, want a cancelled one", m)
			}
		case <-time.After(5 * time.Second):
			t.Fatal("the lookup outlived the app's context")
		}
	})
}

func TestNoSourceMeansNoPRWork(t *testing.T) {
	r := newRealRailApp(t, false)
	dir := r.prRepo(t, "proj", "feat", "https://github.com/o/r.git")
	r.addAgent(t, false, dir)
	r.churn(t, r.settle())

	if cmd := r.waitPRs(); cmd != nil {
		t.Error("waitPRs returned a command with no source")
	}

	if len(r.prs.panes) != 0 {
		t.Errorf("resolutions = %v, want none", r.prs.panes)
	}
}

func TestSnapshotKeepsTheRepositoryDetails(t *testing.T) {
	p := newPRApp(t)
	done := time.Date(2026, 10, 29, 12, 0, 0, 0, time.UTC)

	snap := snapshotOf(github.PR{Number: 12, HeadBranch: "feat", State: "OPEN", ViewerAuthored: true})
	snap.Info.Methods = []github.MergeMethod{github.MergeCommit, github.Rebase}
	snap.Info.DeleteBranchOnMerge = true
	snap.Focused = &github.FocusedChecks{Number: 12, HeadOID: "abc", Checks: []github.Check{
		{Name: "ci", URL: "https://x/1", State: github.CheckFailure, CompletedAt: done},
	}}

	p.step(snap)

	want := core.RepoPRs{
		DefaultBranch:       "main",
		Open:                []core.PRInfo{{Number: 12, HeadBranch: "feat", State: "OPEN", ViewerAuthored: true}},
		Methods:             []string{"merge", "rebase"},
		DeleteBranchOnMerge: true,
		Focused: &core.PRChecks{Number: 12, HeadOID: "abc", Checks: []core.CheckInfo{
			{Name: "ci", URL: "https://x/1", State: "failure", CompletedAt: done},
		}},
	}

	if got := p.store.State().PRs[prRepoKey]; !reflect.DeepEqual(got, want) {
		t.Errorf("PRs = %+v, want %+v", got, want)
	}

	failed := snapshotOf()
	failed.Err = errors.New("boom")
	p.step(failed)

	got := p.store.State().PRs[prRepoKey]
	if !reflect.DeepEqual(got.Methods, want.Methods) || !got.DeleteBranchOnMerge || !reflect.DeepEqual(got.Focused, want.Focused) {
		t.Errorf("a failed poll lost the details: %+v", got)
	}
}
