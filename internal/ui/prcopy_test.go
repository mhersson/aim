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
