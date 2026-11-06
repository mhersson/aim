package ui

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/mhersson/aim/internal/core"
	"github.com/mhersson/aim/internal/git"
	"github.com/mhersson/aim/internal/github"
)

const (
	copyToken = "copy-token"
	// copyBranch is the head branch of acme/infra#88 in the list.
	copyBranch = "feat/infra"
)

// copyRig is the other tabs' rig with a folder for copies, a remote that holds
// the pull requests of acme/infra on both hosts and the merge base GitHub gives.
type copyRig struct {
	*prDrawer
	reads  *fakeReads
	copies string
	head   string // the head the remote holds for the copy's pull requests
	base   string // their merge base
}

func copyOrigin(host string) string { return "https://" + host + "/acme/infra.git" }

func newCopyRig(t *testing.T) *copyRig { return newCopyRigWith(t) }

func newCopyRigWith(t *testing.T, opts ...func(*Options)) *copyRig {
	t.Helper()

	copies := t.TempDir()
	r := otherTabsRig(t, append([]func(*Options){func(o *Options) { o.PRCopies = copies }}, opts...)...)
	c := &copyRig{prDrawer: r, reads: r.reads.(*fakeReads), copies: copies}

	var chain []string

	r.repo.f.edit(func() { chain = r.repo.f.chain(r.prHead) })

	c.head, c.base = chain[0], chain[1]

	c.reads.mu.Lock()
	c.reads.gitToken, c.reads.mergeBases = copyToken, map[string]string{c.head: c.base}
	c.reads.mu.Unlock()

	c.remoteHolds(c.head)

	return c
}

// remoteHolds makes the remotes hold head for every pull request of the copies.
func (c *copyRig) remoteHolds(head string) {
	c.repo.f.edit(func() {
		for host, n := range map[string]int{"github.com": 88, "ghe.example.com": 4} {
			c.repo.f.copyPRs[copyOrigin(host)] = map[int]string{n: head}
		}
	})
}

// mergeBaseOf makes GitHub answer base for head.
func (c *copyRig) mergeBaseOf(head, base string) {
	c.reads.mu.Lock()
	defer c.reads.mu.Unlock()

	c.reads.mergeBases[head] = base
}

// rootOf is the copy's root for the repository, as the fake resolves it.
func (c *copyRig) rootOf(t *testing.T, repo string) string {
	t.Helper()

	dir, ok := c.prCopyRoot(repo)
	if !ok {
		t.Fatalf("no copy root for %s", repo)
	}

	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}

	root, err := filepath.EvalSymlinks(dir)
	if err != nil {
		t.Fatal(err)
	}

	return root
}

// open selects the row of repo and presses enter.
func (c *copyRig) open(t *testing.T, repo string) {
	t.Helper()

	c.selectRow(t, repo)
	c.keys(keyEnter)
}

// opened88 opens acme/infra#88 and waits for its diff.
func (c *copyRig) opened88(t *testing.T) {
	t.Helper()

	c.open(t, "github.com/acme/infra")
	c.until(t, "PR mode on #88", c.inPRMode(88))
	c.finishSlide()
}

func (c *copyRig) want88() *prWant {
	dir, _ := c.prCopyRoot("github.com/acme/infra")

	return &prWant{repo: "github.com/acme/infra", number: 88, root: dir, base: "main", inCopy: true, branch: copyBranch}
}

func (c *copyRig) fetchHeld(n int) func() bool {
	return func() bool { return c.drawer.fetching[n] == 0 }
}

func TestPRListCopyOpens(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name, host, repo string
		number           int
		key              rune
	}{
		{"github enter", "github.com", "github.com/acme/infra", 88, '\r'},
		{"enterprise enter", "ghe.example.com", "ghe.example.com/acme/infra", 4, '\r'},
		{"github a", "github.com", "github.com/acme/infra", 88, 'a'},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			c := newCopyRig(t)
			root := c.rootOf(t, tc.repo)

			c.selectRow(t, tc.repo)

			if tc.key == '\r' {
				c.keys(keyEnter)
			} else {
				c.keys(keyRune(tc.key))
			}

			if c.prList != nil {
				t.Error("the list stayed open")
			}

			c.until(t, "the fetching notice", func() bool { return c.notice == fmt.Sprintf(noticePRFetching, tc.number) })
			c.until(t, "PR mode", c.inPRMode(tc.number))

			if want := (git.PRSource{Worktree: root, Number: tc.number, Copy: true}); c.view().src != want {
				t.Errorf("source = %+v, want %+v", c.view().src, want)
			}

			if pr := c.review().PR; pr == nil || !pr.Copy || pr.Root != root {
				t.Errorf("review PR = %+v, want a copy rooted at %s", pr, root)
			}

			opens := c.repo.f.calls("OpenPRCopy")
			if len(opens) != 1 || opens[0].Root != filepath.Join(c.copies, tc.host, "acme", "infra") || opens[0].Args[0] != copyOrigin(tc.host) {
				t.Errorf("OpenPRCopy calls = %+v, want one on the copy of %s", opens, copyOrigin(tc.host))
			}

			n := fmt.Sprint(tc.number)

			if got := c.repo.f.calls("FetchPRCopyHead"); len(got) != 1 || !slices.Equal(got[0].Args, []string{n, copyToken}) {
				t.Errorf("FetchPRCopyHead calls = %+v, want #%s with the token", got, n)
			}

			if got := c.repo.f.calls("FetchPRCopyBase"); len(got) != 1 || !slices.Equal(got[0].Args, []string{n, c.base, copyToken}) {
				t.Errorf("FetchPRCopyBase calls = %+v, want #%s at the merge base with the token", got, n)
			}
		})
	}
}

func TestPRListCopyUsesTheFetchedHead(t *testing.T) {
	t.Parallel()

	c := newCopyRig(t)

	// The remote moved on since the list read the pull request.
	newer := c.repo.commit("moved on")
	c.remoteHolds(newer)
	c.mergeBaseOf(newer, c.base)

	c.open(t, "github.com/acme/infra")
	c.until(t, "PR mode on #88", c.inPRMode(88))

	if got := c.view().res.Head; got != newer {
		t.Errorf("head = %s, want the fetched %s", got, newer)
	}

	c.reads.mu.Lock()
	defer c.reads.mu.Unlock()

	if want := "mergebase github.com/acme/infra main..." + newer; !slices.Contains(c.reads.calls, want) {
		t.Errorf("reads = %v, want %q", c.reads.calls, want)
	}
}

func TestPRListCopySkipsTheBranchLookups(t *testing.T) {
	t.Parallel()

	c := newCopyRig(t)
	root := c.rootOf(t, "github.com/acme/infra")

	c.opened88(t)

	for _, op := range []string{"Head", "PushRemote", "FetchPR", "Unpushed"} {
		for _, call := range c.repo.f.calls(op) {
			if call.Root == root {
				t.Errorf("%s ran on the copy: %+v", op, call)
			}
		}
	}

	if got := c.lookedUp(); len(got) != 0 {
		t.Errorf("lookups = %v, want none", got)
	}
}

func TestPRListNoPane(t *testing.T) {
	t.Parallel()

	c := newCopyRig(t)

	if err := c.dispatch(core.ClosePane{ID: c.focus()}); err != nil {
		t.Fatal(err)
	}

	c.selectRow(t, "github.com/acme/infra")
	c.keys(keyEnter)

	if c.notice != noticePRNoPane {
		t.Errorf("notice = %q, want %q", c.notice, noticePRNoPane)
	}

	if c.prList == nil {
		t.Error("the list closed")
	}

	if c.review() != nil {
		t.Error("a drawer opened")
	}
}

func TestPRListCopyOff(t *testing.T) {
	t.Parallel()

	r := otherTabsRig(t)
	r.selectRow(t, "github.com/acme/infra")
	r.keys(keyEnter)

	if want := fmt.Sprintf(noticeCopyOff, 88); r.notice != want {
		t.Errorf("notice = %q, want %q", r.notice, want)
	}

	if r.prList == nil {
		t.Error("the list closed")
	}

	if r.review() != nil {
		t.Error("a drawer opened")
	}
}

func TestPRListCheckoutSkipsTheCopy(t *testing.T) {
	t.Parallel()

	c := newCopyRig(t)
	c.selectRow(t, "github.com/o/r")
	c.keys(keyEnter)
	c.until(t, "PR mode on #7", c.inPRMode(7))

	if n := c.repo.f.count("OpenPRCopy"); n != 0 {
		t.Errorf("OpenPRCopy ran %d times for a repository with a pane", n)
	}

	if pr := c.review().PR; pr == nil || pr.Copy {
		t.Errorf("review PR = %+v, want the checkout's", pr)
	}
}

func TestCopyFetchFailure(t *testing.T) {
	t.Parallel()

	boom := errors.New("boom")

	for _, tc := range []struct {
		name string
		fail func(c *copyRig)
	}{
		{"open", func(c *copyRig) { c.repo.f.fail("OpenPRCopy", boom) }},
		{"head", func(c *copyRig) { c.repo.f.fail("FetchPRCopyHead", boom) }},
		{"merge base", func(c *copyRig) { c.reads.mu.Lock(); c.reads.mergeErr = boom; c.reads.mu.Unlock() }},
		{"base", func(c *copyRig) { c.repo.f.fail("FetchPRCopyBase", boom) }},
		{"token", func(c *copyRig) { c.reads.mu.Lock(); c.reads.tokenErr = boom; c.reads.mu.Unlock() }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			c := newCopyRig(t)
			tc.fail(c)

			c.open(t, "github.com/acme/infra")
			c.until(t, "the failure", func() bool { return c.noticeLevel() == levelError })

			if !strings.Contains(c.notice, prNumberText(88)) || !strings.Contains(c.notice, boom.Error()) {
				t.Errorf("notice = %q, want #88 and %q", c.notice, boom)
			}

			if v := c.view(); v != nil && v.pr != (core.ReviewPR{}) {
				t.Errorf("the drawer shows %+v after a failed fetch", v.pr)
			}
		})
	}
}

func TestCopyNoSuchPR(t *testing.T) {
	t.Parallel()

	c := newCopyRig(t)
	c.repo.f.edit(func() { c.repo.f.copyPRs[copyOrigin("github.com")] = nil })

	c.open(t, "github.com/acme/infra")
	c.until(t, "the notice", func() bool { return c.notice == fmt.Sprintf(noticePRNotOnRemote, 88, "origin") })
}

func TestCopyLateFetchDropped(t *testing.T) {
	t.Parallel()

	c := newCopyRig(t)
	release := c.repo.f.hold("FetchPRCopyHead")

	defer release()

	c.open(t, "github.com/acme/infra")
	c.until(t, "the held fetch", func() bool { return c.repo.f.inflight("FetchPRCopyHead") == 1 })

	// A newer ask overtakes it.
	c.drawer.prSeq++

	release()
	c.until(t, "the fetch to answer", c.fetchHeld(88))
	c.settle()

	if pr := c.review().PR; pr != nil {
		t.Errorf("review PR = %+v, want the late answer dropped", pr)
	}
}

func TestCopyFetchesOneAtATime(t *testing.T) {
	t.Parallel()

	c := newCopyRig(t)
	release := c.repo.f.hold("FetchPRCopyHead")

	defer release()

	c.open(t, "github.com/acme/infra")
	c.until(t, "the held fetch", func() bool { return c.repo.f.inflight("FetchPRCopyHead") == 1 })

	c.run(c.askPR(c.store.State().Tab().ID, c.want88(), "", thenNothing))
	c.until(t, "both fetches asked", func() bool { return c.drawer.fetching[88] == 2 })

	release()
	c.until(t, "both fetches to answer", c.fetchHeld(88))

	if got := c.repo.f.peak("FetchPRCopyHead"); got != 1 {
		t.Errorf("peak FetchPRCopyHead = %d, want 1", got)
	}

	if got := c.repo.f.count("FetchPRCopyHead"); got != 2 {
		t.Errorf("FetchPRCopyHead ran %d times, want both asks to complete", got)
	}
}

func TestCopyTitleIsTheHeadBranch(t *testing.T) {
	t.Parallel()

	c := newCopyRig(t)
	root := c.rootOf(t, "github.com/acme/infra")

	c.opened88(t)

	if top := plainTop(t, c.prDrawer); !strings.Contains(top, "infra · "+copyBranch) {
		t.Errorf("top border %q, want the repository and the head branch", top)
	}

	for _, call := range c.repo.f.calls("Head") {
		if call.Root == root {
			t.Errorf("Head ran on the copy: %+v", call)
		}
	}
}

func TestCopyPShowsTheHeldPR(t *testing.T) {
	t.Parallel()

	c := newCopyRig(t)

	// Another pull request on branches of the names a copy's HEAD could have.
	other := github.PR{Number: 5, HeadBranch: "main", BaseBranch: "main", State: prOpenState}
	c.found["main"], c.found["aim-pr-copy"] = other, other

	c.opened88(t)
	c.typed("p")
	c.until(t, "the second fetch", func() bool { return c.repo.f.count("FetchPRCopyHead") == 2 })
	c.until(t, "PR mode on #88", c.inPRMode(88))

	if got := c.view().pr.Number; got != 88 {
		t.Errorf("shown pull request = #%d, want #88", got)
	}

	if got := c.lookedUp(); len(got) != 0 {
		t.Errorf("lookups = %v, want none", got)
	}
}

func TestCopyReloadFetchesAgain(t *testing.T) {
	t.Parallel()

	c := newCopyRig(t)
	c.opened88(t)

	moved := c.repo.commit("moved")
	c.remoteHolds(moved)
	c.mergeBaseOf(moved, c.base)

	c.typed("r")
	c.until(t, "the new head", c.viewHolds(func(v *reviewView) bool { return v.res.Head == moved && !v.loading }))
}

func TestCopyReloadAfterRemoval(t *testing.T) {
	t.Parallel()

	c := newCopyRig(t)
	root := c.rootOf(t, "github.com/acme/infra")

	c.opened88(t)

	if err := os.RemoveAll(root); err != nil {
		t.Fatal(err)
	}

	c.typed("r")
	c.until(t, "the copy to open again", func() bool { return c.repo.f.count("OpenPRCopy") == 2 })
	c.until(t, "the diff", c.viewHolds(func(v *reviewView) bool { return v.loaded && !v.loading && v.err == nil && len(v.res.Files) > 0 }))
}

func TestCopyNoTurnCount(t *testing.T) {
	t.Parallel()

	c := newCopyRig(t)
	root := c.rootOf(t, "github.com/acme/infra")

	pane := c.agent(t, c.dir, testSession)
	c.run(c.openWithPR(pane, c.want88(), thenNothing))
	c.until(t, "PR mode on #88", c.inPRMode(88))

	for _, op := range []string{"TurnList", "TurnListAt"} {
		for _, call := range c.repo.f.calls(op) {
			if call.Root == root {
				t.Errorf("%s ran on the copy: %+v", op, call)
			}
		}
	}

	if got := c.view().turns; got != 0 {
		t.Errorf("turns = %d, want 0", got)
	}
}

func TestCopyMarksAHunk(t *testing.T) {
	t.Parallel()

	fm := &fakeMarks{}
	c := newCopyRigWith(t, withMarks(fm))
	root := c.rootOf(t, "github.com/acme/infra")

	c.opened88(t)
	c.onFirstChange(t, 0, 0)

	id := git.HunkID(c.view().res.Files[0].Path, c.view().res.Files[0].Hunks[0])

	c.keys(keySpace)

	common := filepath.Join(root, ".git")
	if _, ok := fm.daysOf(common)[id]; !ok {
		t.Errorf("marks under %s = %v, want the hunk %s", common, fm.daysOf(common), id)
	}
}

func TestCopyGateServesOneRootAtATime(t *testing.T) {
	t.Parallel()

	var g prCopyGate

	release, err := g.acquire(t.Context(), "/c/a")
	if err != nil {
		t.Fatal(err)
	}

	// A wait ends with its context, not with the turn.
	gone, cancel := context.WithCancel(t.Context())
	cancel()

	if _, err := g.acquire(gone, "/c/a"); !errors.Is(err, context.Canceled) {
		t.Errorf("acquire on a held root = %v, want the context's error", err)
	}

	other, err := g.acquire(t.Context(), "/c/b")
	if err != nil {
		t.Fatalf("acquire on another root: %v", err)
	}

	other()
	release()

	again, err := g.acquire(t.Context(), "/c/a")
	if err != nil {
		t.Fatalf("acquire after release: %v", err)
	}

	again()
}

// noRepoPane opens an agent pane that is in no repository: dir "" leaves its
// follow directory empty.
func (c *copyRig) noRepoPane(t *testing.T, dir string, session core.SessionID) core.PaneID {
	t.Helper()

	id := c.agent(t, dir, session)
	if got := c.store.State().Tab().Focus; got != id {
		t.Fatalf("focus = %s, want the new pane %s", got, id)
	}

	return id
}

// noRepoDirs are the two ways a pane is in no repository.
var noRepoDirs = []struct {
	name string
	dir  func(t *testing.T) string
}{
	{"outside any repository", func(t *testing.T) string { return t.TempDir() }},
	{"empty directory", func(*testing.T) string { return "" }},
}

func TestCopyOpensOnPaneInNoRepo(t *testing.T) {
	t.Parallel()

	for _, tc := range noRepoDirs {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			c := newCopyRig(t)
			root := c.rootOf(t, "github.com/acme/infra")
			c.noRepoPane(t, tc.dir(t), "")

			c.opened88(t)

			v := c.view()
			src, ok := v.src.(git.PRSource)

			if !ok || !src.Copy || src.Worktree != root || src.Number != 88 {
				t.Errorf("src = %+v, want the copy's pull request 88", v.src)
			}

			if len(v.res.Files) == 0 {
				t.Error("the diff has no files")
			}
		})
	}
}

func TestCopyLeavingLandsOnNotRepo(t *testing.T) {
	t.Parallel()

	c := newCopyRig(t)
	c.noRepoPane(t, t.TempDir(), "")
	c.opened88(t)

	c.keys(keyRune('b'))
	c.until(t, "the not-a-repository state", c.viewHolds(func(v *reviewView) bool { return errors.Is(v.err, errNotRepo) }))

	if c.review().PR != nil {
		t.Errorf("review PR = %+v, want none", c.review().PR)
	}
}

func TestCopyTurnKeyLeavesForThePane(t *testing.T) {
	t.Parallel()

	c := newCopyRig(t)
	c.agent(t, c.dir, testSession)
	c.opened88(t)

	c.keys(keyRune('t'))
	c.until(t, "the pane's turns", c.viewHolds(func(v *reviewView) bool { return c.review().PR == nil && v.root == c.root }))

	if got := c.review().Mode; got != core.ReviewTurn {
		t.Errorf("mode = %v, want turn", got)
	}
}

func TestCopyTurnKeyNoSession(t *testing.T) {
	t.Parallel()

	c := newCopyRig(t)
	c.noRepoPane(t, t.TempDir(), "")
	c.opened88(t)

	c.keys(keyRune('t'))

	if c.notice != noticeNoTurns {
		t.Errorf("notice = %q, want %q", c.notice, noticeNoTurns)
	}

	if pr := c.review().PR; pr == nil || pr.Number != 88 {
		t.Errorf("review PR = %+v, want #88 kept", pr)
	}
}

func TestCopyFetchFailsOnPaneInNoRepo(t *testing.T) {
	t.Parallel()

	boom := errors.New("boom")
	c := newCopyRig(t)
	c.repo.f.fail("OpenPRCopy", boom)
	c.noRepoPane(t, t.TempDir(), "")

	c.open(t, "github.com/acme/infra")
	c.until(t, "the failure", func() bool { return c.noticeLevel() == levelError })

	if !strings.Contains(c.notice, prNumberText(88)) {
		t.Errorf("notice = %q, want #88", c.notice)
	}

	c.until(t, "the not-a-repository state", c.viewHolds(func(v *reviewView) bool { return errors.Is(v.err, errNotRepo) }))
}

func TestCopyOpenErrorLeavesTheWatch(t *testing.T) {
	t.Parallel()

	c := newCopyRig(t)
	c.rootOf(t, "github.com/acme/infra")

	pane, tab := c.focus(), c.store.State().Tab().ID
	c.watches.panes[pane] = &paneWatch{cwd: c.dir, want: c.root}
	c.prList = nil
	c.drawer.prNext = &prNext{tab: tab, pane: pane, want: c.want88(), then: thenNothing}

	// An open that failed for a reason other than "not a repository".
	c.run(c.step(reviewOpenMsg{tab: tab, pane: pane, cwd: c.dir, focus: true, anyDir: true, err: errors.New("boom")}))
	c.until(t, "PR mode on #88", c.inPRMode(88))

	if w := c.watches.panes[pane]; w == nil || w.notRepo || w.want != c.root {
		t.Fatalf("watch = %+v, want it untouched", w)
	}

	c.keys(keyRune('b'))
	c.until(t, "the pane's branch", c.viewHolds(func(v *reviewView) bool { return c.review().PR == nil && v.root == c.root }))

	if errors.Is(c.view().err, errNotRepo) {
		t.Error("leaving the pull request shows not a git repository on a healthy pane")
	}
}

func TestCopyTurnKeyOnPaneInNoRepoWithSession(t *testing.T) {
	t.Parallel()

	c := newCopyRig(t)
	c.noRepoPane(t, t.TempDir(), testSession)
	c.opened88(t)

	c.keys(keyRune('t'))
	c.until(t, "the not-a-repository state", c.viewHolds(func(v *reviewView) bool { return errors.Is(v.err, errNotRepo) }))

	if rv := c.review(); rv.PR != nil || rv.Mode != core.ReviewTurn {
		t.Errorf("review = %+v, want no pull request in turn mode", rv)
	}
}

var copyRepo = github.Repo{Host: "github.com", Owner: "acme", Name: "infra"}

func (c *copyRig) deliver(s github.Snapshot) { c.run(c.step(prSnapshotMsg{s})) }

// poll delivers a poll of the copy's repository, asked for pull request asked,
// that lists prs.
func (c *copyRig) poll(asked int, prs ...github.PR) {
	c.deliver(github.Snapshot{
		Repo: copyRepo, Asked: asked, PRs: prs,
		Info: github.RepoInfo{DefaultBranch: "main", Methods: []github.MergeMethod{github.Squash}},
	})
}

func copyPR88() github.PR {
	return github.PR{Number: 88, HeadBranch: copyBranch, BaseBranch: "main", State: prOpenState}
}

// pressOnRow presses key on the row of #88 and waits for the diff to load.
func (c *copyRig) pressOnRow(t *testing.T, key rune) {
	t.Helper()

	c.selectRow(t, "github.com/acme/infra")
	c.keys(keyRune(key))
	c.until(t, "PR mode on #88", c.inPRMode(88))
}

func (c *copyRig) noBox(t *testing.T) {
	t.Helper()

	if c.merge != nil || c.confirm != nil {
		t.Errorf("a box is open: merge %v, confirm %v", c.merge != nil, c.confirm != nil)
	}
}

func TestCopyActionWaitsForThePoll(t *testing.T) {
	t.Parallel()

	for _, key := range []rune{'M', 'a'} {
		t.Run(string(key), func(t *testing.T) {
			t.Parallel()

			c := newCopyRig(t)
			c.pressOnRow(t, key)
			c.noBox(t)

			if want := fmt.Sprintf(noticePRFetching, 88); c.notice != want {
				t.Errorf("notice = %q, want %q", c.notice, want)
			}

			c.rec.rmu.Lock()
			refreshed := slices.Contains(c.rec.repos, copyRepo)
			c.rec.rmu.Unlock()

			if !refreshed {
				t.Error("the poll was not asked to refresh the repository")
			}

			c.poll(88, copyPR88())

			head := c.view().res.Head

			if key == 'a' {
				if c.confirm == nil || c.confirm.approve == nil || c.confirm.approve.sha != head {
					t.Fatalf("confirm = %+v, want the approve of the loaded head %s", c.confirm, head)
				}

				return
			}

			m := c.merge
			if m == nil {
				t.Fatal("no merge box after the poll")
			}

			if m.sha != head || m.pr.HeadBranch != copyBranch || !slices.Equal(m.info.Methods, []github.MergeMethod{github.Squash}) {
				t.Errorf("merge box = sha %s, branch %q, methods %v; want %s, %q, [squash]", m.sha, m.pr.HeadBranch, m.info.Methods, head, copyBranch)
			}
		})
	}
}

func TestCopyEnterDoesNotWait(t *testing.T) {
	t.Parallel()

	c := newCopyRig(t)
	c.opened88(t)

	if c.view().pollThen != thenNothing {
		t.Error("enter held for the poll")
	}
}

func TestCopyDrawerKeyWaitsToo(t *testing.T) {
	t.Parallel()

	c := newCopyRig(t)
	c.opened88(t)
	c.keys(keyRune('M'))
	c.noBox(t)

	if c.view().pollThen != thenMerge {
		t.Fatalf("pollThen = %v, want the merge held", c.view().pollThen)
	}

	c.poll(88, copyPR88())

	if c.merge == nil {
		t.Error("no merge box after the poll")
	}
}

func TestCopyKnownPRDoesNotWait(t *testing.T) {
	t.Parallel()

	c := newCopyRig(t)
	c.poll(88, copyPR88())
	c.pressOnRow(t, 'M')

	if c.merge == nil {
		t.Error("the merge box did not open at once")
	}
}

func TestCopyWaitIgnoresAnOlderAsk(t *testing.T) {
	t.Parallel()

	c := newCopyRig(t)
	c.pressOnRow(t, 'M')
	c.poll(5)

	if c.view().pollThen != thenMerge {
		t.Error("an answer for another pull request ended the hold")
	}

	c.noBox(t)
}

func TestCopyWaitFailedPoll(t *testing.T) {
	t.Parallel()

	c := newCopyRig(t)
	c.pressOnRow(t, 'M')
	c.deliver(github.Snapshot{Repo: copyRepo, Asked: 88, Err: errors.New("boom")})

	if want := fmt.Sprintf(noticePRsFailed, "github.com/acme/infra", "boom"); c.notice != want {
		t.Errorf("notice = %q, want %q", c.notice, want)
	}

	if c.view().pollThen != thenNothing {
		t.Error("the hold stayed after a failed poll")
	}

	c.noBox(t)
}

func TestCopyWaitStandingError(t *testing.T) {
	t.Parallel()

	c := newCopyRig(t)
	c.deliver(github.Snapshot{Repo: copyRepo, Err: errors.New("boom")})
	c.notice = ""
	c.pressOnRow(t, 'M')

	if want := fmt.Sprintf(noticePRsFailed, "github.com/acme/infra", "boom"); c.notice != want {
		t.Errorf("notice = %q, want %q", c.notice, want)
	}

	if c.view().pollThen != thenNothing {
		t.Error("it held on a standing error")
	}

	c.noBox(t)
}

func TestCopyWaitAnswerWithoutIt(t *testing.T) {
	t.Parallel()

	c := newCopyRig(t)
	c.pressOnRow(t, 'M')
	c.poll(88)

	if want := fmt.Sprintf(noticePRGone, 88); c.notice != want {
		t.Errorf("notice = %q, want %q", c.notice, want)
	}

	c.noBox(t)
}

func TestCopyWaitFocusedQueryFailed(t *testing.T) {
	t.Parallel()

	c := newCopyRig(t)
	c.pressOnRow(t, 'M')
	c.deliver(github.Snapshot{
		Repo: copyRepo, Asked: 88, Focused: &github.FocusedChecks{Number: 88, Err: errors.New("boom")},
	})

	if want := fmt.Sprintf(noticePRsFailed, "github.com/acme/infra", "boom"); c.notice != want {
		t.Errorf("notice = %q, want %q", c.notice, want)
	}

	c.noBox(t)
}

func TestCopyWaitDroppedByReload(t *testing.T) {
	t.Parallel()

	c := newCopyRig(t)
	c.pressOnRow(t, 'M')
	c.keys(keyRune('r'))
	c.until(t, "PR mode on #88", c.inPRMode(88))
	c.poll(88, copyPR88())
	c.noBox(t)
}

func TestCopyWaitHiddenTab(t *testing.T) {
	t.Parallel()

	c := newCopyRig(t)
	first := c.store.State().Active

	c.pressOnRow(t, 'M')

	if err := c.dispatch(core.NewTab{Kind: core.KindShell, Cwd: c.dir}); err != nil {
		t.Fatal(err)
	}

	c.poll(88, copyPR88())
	c.noBox(t)

	c.run(c.act(core.SelectTab{Index: first}))
	c.settle()
	c.noBox(t)
}

func TestCopyWaitSwitchedAwayAndBack(t *testing.T) {
	t.Parallel()

	c := newCopyRig(t)
	first := c.store.State().Active

	c.pressOnRow(t, 'M')

	if err := c.dispatch(core.NewTab{Kind: core.KindShell, Cwd: c.dir}); err != nil {
		t.Fatal(err)
	}

	c.settle()
	c.run(c.act(core.SelectTab{Index: first}))
	c.settle()

	if c.view().pollThen != thenNothing {
		t.Error("the hold outlived the tab switch")
	}

	c.poll(88, copyPR88())
	c.noBox(t)
}

func TestCopyWaitLateModal(t *testing.T) {
	t.Parallel()

	c := newCopyRig(t)
	c.pressOnRow(t, 'M')

	c.confirm = &confirm{}
	c.poll(88, copyPR88())

	if want := c.prLoadedNotice(88, thenMerge); c.notice != want {
		t.Errorf("notice = %q, want %q", c.notice, want)
	}

	if c.merge != nil {
		t.Error("a merge box opened over the modal")
	}
}

// callsOn is the fake git's calls on root made after the first from calls.
func (c *copyRig) callsOn(root string, from int) []gitCall {
	f := c.repo.f

	f.mu.Lock()
	defer f.mu.Unlock()

	var out []gitCall

	for _, call := range f.log[from:] {
		if call.Root == root {
			out = append(out, call)
		}
	}

	return out
}

func (c *copyRig) logLen() int {
	c.repo.f.mu.Lock()
	defer c.repo.f.mu.Unlock()

	return len(c.repo.f.log)
}

// copyOnShow opens #88 as a copy and gives the drawer the keyboard.
func (c *copyRig) copyOnShow(t *testing.T) (copyRoot string) {
	t.Helper()

	copyRoot = c.rootOf(t, "github.com/acme/infra")
	c.opened88(t)

	if !c.drawerHasKeys() {
		t.Fatal("the drawer does not have the keyboard")
	}

	return copyRoot
}

func TestCopyEditRefused(t *testing.T) {
	t.Parallel()

	c := newCopyRig(t)
	c.copyOnShow(t)

	if v := c.view(); v.cursor < 0 || v.cursor >= len(v.rows) {
		t.Fatalf("cursor %d outside %d rows", v.cursor, len(v.rows))
	}

	panes := len(c.store.State().Tab().Panes)

	c.keys(keyRune('e'))

	if c.notice != noticeCopyNoEdit {
		t.Errorf("notice = %q, want %q", c.notice, noticeCopyNoEdit)
	}

	if got := len(c.store.State().Tab().Panes); got != panes {
		t.Errorf("panes = %d, want %d: the editor opened", got, panes)
	}
}

func TestDrawerShowsSkipsACopy(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		pane     func(t *testing.T, c *copyRig)
		wantShow func(c *copyRig) string // drawerShows and mdOpenDir
		wantPush func(c *copyRig) string
	}{
		{
			"pane in a repository", func(*testing.T, *copyRig) {},
			func(c *copyRig) string { return c.dir }, func(c *copyRig) string { return c.dir },
		},
		{
			"pane with no directory", func(t *testing.T, c *copyRig) { c.noRepoPane(t, "", "") },
			func(*copyRig) string { return "" }, func(c *copyRig) string { return c.cwd },
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			c := newCopyRig(t)
			tc.pane(t, c)
			root := c.copyOnShow(t)

			st := c.store.State()
			got := map[string]string{
				"drawerShows": c.drawerShows(st, st.Tab()),
				"mdOpenDir":   c.mdOpenDir(st),
				"pushDir":     c.pushDir(st),
			}
			want := map[string]string{
				"drawerShows": tc.wantShow(c), "mdOpenDir": tc.wantShow(c), "pushDir": tc.wantPush(c),
			}

			for name, dir := range got {
				if dir == root || dir != want[name] {
					t.Errorf("%s = %q, want %q (the copy is %q)", name, dir, want[name], root)
				}
			}
		})
	}

	t.Run("a checkout keeps its root", func(t *testing.T) {
		t.Parallel()

		c := newCopyRig(t)
		c.selectRow(t, "github.com/o/r")
		c.keys(keyEnter)
		c.until(t, "PR mode on #7", c.inPRMode(7))

		st := c.store.State()
		pr := c.review().PR

		if pr == nil || pr.Copy || pr.Root == "" {
			t.Fatalf("review PR = %+v, want a checkout's", pr)
		}

		if got := c.drawerShows(st, st.Tab()); got != pr.Root {
			t.Errorf("drawerShows = %q, want %q", got, pr.Root)
		}
	})
}

func TestCopyPushActsOnThePane(t *testing.T) {
	t.Parallel()

	c := newCopyRig(t)
	root := c.copyOnShow(t)
	from := c.logLen()

	c.run(c.push())
	c.until(t, "the push answer", func() bool { return c.confirm != nil || c.notice != "" })

	if c.confirm == nil || c.confirm.push == nil {
		t.Fatalf("no push confirm; notice = %q", c.notice)
	}

	if got := c.confirm.push.root; got == root || got != c.root {
		t.Errorf("the confirm pushes from %q, want the pane's %q", got, c.root)
	}

	if calls := c.callsOn(root, from); len(calls) != 0 {
		t.Errorf("git ran on the copy: %+v", calls)
	}
}

func TestCopyComposeActsOnThePane(t *testing.T) {
	t.Parallel()

	c := newCopyRig(t)
	root := c.copyOnShow(t)
	from := c.logLen()

	c.run(c.composeCommand())
	c.until(t, "the compose answer", func() bool { return c.compose != nil || c.notice != "" })

	if calls := c.callsOn(root, from); len(calls) != 0 {
		t.Errorf("git ran on the copy: %+v", calls)
	}

	if c.compose != nil && c.compose.t.dir == root {
		t.Errorf("compose resolves from the copy %q", root)
	}
}

func TestCopyPushOnPaneInNoRepo(t *testing.T) {
	t.Parallel()

	c := newCopyRig(t)
	c.noRepoPane(t, t.TempDir(), "")
	root := c.copyOnShow(t)
	from := c.logLen()

	c.run(c.push())
	c.until(t, "the notice", func() bool { return c.notice != "" })

	if c.notice != noticePushNotRepo || c.confirm != nil {
		t.Errorf("notice = %q, confirm = %v, want %q and no confirm", c.notice, c.confirm, noticePushNotRepo)
	}

	if calls := c.callsOn(root, from); len(calls) != 0 {
		t.Errorf("git ran on the copy: %+v", calls)
	}
}

func TestCopyCleanUpSaysNoWorktree(t *testing.T) {
	t.Parallel()

	c := newCopyRig(t)
	root := c.copyOnShow(t)
	from := c.logLen()

	// The pane's branch reads a merged pull request whose head branch has no worktree, as a copy's #88 does.
	merged := github.PR{
		Number: 88, Title: "Bump", State: prMergedState, HeadBranch: copyBranch, HeadOID: c.prHead, BaseBranch: "main",
	}
	c.mu.Lock()
	c.found["feat/x"] = merged
	c.mu.Unlock()

	openIn(t, c.App, core.SetBranchPR{
		Repo: prRepoKey, Branch: "feat/x", Found: true,
		PR: core.PRInfo{
			Number: 88, Title: "Bump", State: prMergedState, HeadBranch: copyBranch, HeadOID: c.prHead, BaseBranch: "main",
		},
	})

	cmd, ok := c.reg.get("worktree.cleanup")
	if !ok {
		t.Fatal("worktree.cleanup is not registered")
	}

	c.notice = ""
	c.run(cmd.run(c.App))
	c.until(t, "the notice", func() bool { return c.notice != "" || c.confirm != nil })

	if want := noticeNoCleanupWorktree(copyBranch); c.notice != want || c.confirm != nil {
		t.Errorf("notice = %q, confirm = %v, want %q", c.notice, c.confirm, want)
	}

	if calls := c.callsOn(root, from); len(calls) != 0 {
		t.Errorf("git ran on the copy: %+v", calls)
	}
}

func TestCopyMergeFindsNoWorktree(t *testing.T) {
	t.Parallel()

	c := newCopyRig(t)
	root := c.rootOf(t, "github.com/acme/infra")
	c.pressOnRow(t, 'M')
	c.poll(88, copyPR88())

	if c.merge == nil {
		t.Fatal("no merge box after the poll")
	}

	from := c.logLen()
	c.keys(keyEnter)
	c.until(t, "the clean-up check on the copy", func() bool { return len(c.callsOn(root, from)) > 0 })
	c.until(t, "the merged state", func() bool {
		info, _ := prInfoByNumber(c.store.State(), "github.com/acme/infra", 88)

		return info.State == prMergedState
	})

	// A merge finding no worktree is silent, as for a checkout with none: no clean-up box, and no notice but the merge's own.
	if c.confirm != nil || c.notice == noticeNoCleanupWorktree(copyBranch) {
		t.Errorf("confirm = %v, notice = %q, want no clean-up offer or notice", c.confirm, c.notice)
	}

	reads := []string{"Open", "ListWorktrees"}

	for _, call := range c.callsOn(root, from) {
		if !slices.Contains(reads, call.Op) {
			t.Errorf("%s ran on the copy; want only reads %v", call.Op, reads)
		}
	}
}
