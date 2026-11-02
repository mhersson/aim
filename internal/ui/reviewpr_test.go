package ui

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/mhersson/aim/internal/core"
	"github.com/mhersson/aim/internal/git"
	"github.com/mhersson/aim/internal/github"
)

// prDrawer is a drawer rig on a worktree of feat/x whose origin holds main and
// pull request 7. The worktree has a commit of its own that the pull request
// lacks. The fake rig fills repo and origin; the real rig (newRealPRDrawerOn)
// fills bare instead.
type prDrawer struct {
	*drawerRig
	rec     *refreshRec
	dir     string
	root    string
	repo    *fakeRepo
	origin  *fakeRemote
	bare    string
	prHead  string // the commit pull request 7 holds on the remote
	localOK string // the worktree's HEAD

	notGitHub atomic.Bool

	mu      sync.Mutex
	lookups []string
	found   map[string]github.PR
}

const prOriginURL = "https://github.com/o/r.git"

// prTwoCommits is a repository on main whose first commit holds a.txt and
// b.txt and whose second changes a.txt and adds new.txt.
func prTwoCommits(t *testing.T, f *fakeGit) *fakeRepo {
	t.Helper()

	r := f.repo(t, "")
	write(t, filepath.Join(r.Root(), "a.txt"), "a\n")
	write(t, filepath.Join(r.Root(), "b.txt"), "b\n")
	r.commit("init")
	write(t, filepath.Join(r.Root(), "a.txt"), "a changed\n")
	write(t, filepath.Join(r.Root(), "new.txt"), "new\n")
	r.commit("feature work")

	return r
}

// prScaffold gives r, two commits on main, an origin that holds main at the
// first commit and pull request 7 at the second, then moves r to feat/x with a
// commit of its own.
func prScaffold(t *testing.T, r *fakeRepo) *fakeRepo {
	t.Helper()

	var chain []string

	r.f.edit(func() { chain = r.f.chain(r.headOID()) })

	if len(chain) != 2 {
		t.Fatalf("prScaffold: %d commits, want 2", len(chain))
	}

	head, base := chain[0], chain[1]
	rm := r.remote("origin", prOriginURL)
	rm.setBranch("main", base)
	r.track("origin", "main", base)
	rm.pr(7, head)
	r.branch("feat/x")
	write(t, filepath.Join(r.Root(), "local.txt"), "not pushed\n")
	r.commit("local only")

	return r
}

// prFeatureRepo is prTwoCommits on feat/x, scaffolded by prScaffold.
func prFeatureRepo(t *testing.T, f *fakeGit) *fakeRepo {
	t.Helper()

	return prScaffold(t, prTwoCommits(t, f))
}

// newPRDrawerOn is a PR drawer rig on r, a scaffolded repository of f.
func newPRDrawerOn(t *testing.T, f *fakeGit, r *fakeRepo, opts ...func(*Options)) *prDrawer {
	t.Helper()

	p := &prDrawer{
		rec: &refreshRec{fakePRs: newFakePRs()}, dir: r.Root(), root: r.Root(), repo: r, found: map[string]github.PR{},
	}

	f.edit(func() {
		p.origin = r.c.remotes["origin"]
		p.prHead, p.localOK = p.origin.prs[7], r.headOID()
	})

	opts = append([]func(*Options){gitOf(f), func(o *Options) { o.PRs, o.PRLookup = p.rec, p.lookup }}, opts...)
	p.drawerRig = newKeysRig(t, p.dir, opts...)
	p.prs.resolve = p.resolve

	return p
}

func newPRDrawer(t *testing.T, opts ...func(*Options)) *prDrawer {
	t.Helper()

	f := newFakeGit(t)

	return newPRDrawerOn(t, f, prFeatureRepo(t, f), opts...)
}

// realPRWorktree makes the repository dir (on feat/x, two commits past main)
// with a bare origin that has main and refs/pull/7/head, and a commit past the
// pull request in dir. It returns the bare repository, the pull request's head
// and the worktree's.
func realPRWorktree(t *testing.T, dir string) (bare, prHead, local string) {
	t.Helper()

	bare, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}

	gitIn(t, "git", bare, "init", "-q", "--bare", "-b", "main")
	gitIn(t, "git", dir, "remote", "add", "origin", bare)

	base := gitRead(t, "git", dir, "rev-list", "--max-parents=0", "HEAD")
	gitIn(t, "git", dir, "push", "-q", "origin", base+":refs/heads/main")
	gitIn(t, "git", dir, "switch", "-q", "-c", "feat/x")
	gitIn(t, "git", dir, "push", "-q", "origin", "HEAD:refs/pull/7/head")

	prHead = gitRead(t, "git", dir, "rev-parse", "HEAD")

	write(t, filepath.Join(dir, "local.txt"), "not pushed\n")
	commitEverything(t, dir, "local only")

	return bare, prHead, gitRead(t, "git", dir, "rev-parse", "HEAD")
}

// realPRFeatureRepo is prTwoCommits on the real git.
func realPRFeatureRepo(t *testing.T) string {
	t.Helper()

	dir := newRepoDir(t)
	write(t, filepath.Join(dir, "a.txt"), "a\n")
	write(t, filepath.Join(dir, "b.txt"), "b\n")
	commitEverything(t, dir, "init")
	write(t, filepath.Join(dir, "a.txt"), "a changed\n")
	write(t, filepath.Join(dir, "new.txt"), "new\n")
	commitEverything(t, dir, "feature work")

	return dir
}

// newRealPRDrawerOn is the PR drawer rig on the real git and a bare origin.
func newRealPRDrawerOn(t *testing.T, dir string, opts ...func(*Options)) *prDrawer {
	t.Helper()

	bare, prHead, local := realPRWorktree(t, dir)
	p := &prDrawer{
		rec: &refreshRec{fakePRs: newFakePRs()}, dir: dir, root: rootOf(t, dir),
		bare: bare, prHead: prHead, localOK: local, found: map[string]github.PR{},
	}

	opts = append([]func(*Options){func(o *Options) { o.PRs, o.PRLookup = p.rec, p.lookup }}, opts...)
	p.drawerRig = newKeysRig(t, dir, opts...)
	p.prs.resolve = p.resolve

	return p
}

func (p *prDrawer) resolve(_ context.Context, _, _ string) (github.Repo, error) {
	if p.notGitHub.Load() {
		return github.Repo{}, fmt.Errorf("%w: elsewhere", github.ErrNotGitHub)
	}

	return prRepo, nil
}

func (p *prDrawer) lookup(_ context.Context, r github.Repo, branch string) (github.PR, error) {
	p.mu.Lock()
	defer p.mu.Unlock()

	p.lookups = append(p.lookups, r.String()+" "+branch)

	if pr, ok := p.found[branch]; ok {
		return pr, nil
	}

	return github.PR{}, fmt.Errorf("lookup %s: %w", branch, github.ErrNoPR)
}

func (p *prDrawer) lookedUp() []string {
	p.mu.Lock()
	defer p.mu.Unlock()

	return slices.Clone(p.lookups)
}

// poll hands the app a poll that lists pull request 7 of feat/x.
func (p *prDrawer) poll() {
	p.run(p.step(snapshotOf(prSeven())))
}

func prSeven() github.PR {
	return github.PR{Number: 7, Title: "Add x", HeadBranch: "feat/x", BaseBranch: "main", State: prOpenState}
}

func (p *prDrawer) review() *core.Review { return p.store.State().Tab().Review }

// inPRMode is a condition: the drawer shows pull request n and has loaded it.
func (p *prDrawer) inPRMode(n int) func() bool {
	return p.viewHolds(func(v *reviewView) bool {
		rv := p.review()

		return rv != nil && rv.PR != nil && rv.PR.Number == n && v.pr.Number == n && v.loaded && !v.loading
	})
}

// pressP presses p and returns the command that asks; its messages are fed to
// the app one at a time by the caller.
func (p *prDrawer) pressP() tea.Cmd { return p.step(keyRune('p')) }

func plainTop(t *testing.T, r *prDrawer) string { return ansi.Strip(topLine(t, r.App)) }

func TestPRModeShowsThePRDiff(t *testing.T) {
	t.Parallel()

	r := newPRDrawer(t)
	r.poll()
	r.opened(t)
	r.typed("p")
	r.until(t, "PR mode", r.inPRMode(7))

	v := r.view()
	if want := (git.PRSource{Worktree: r.root, Number: 7}); v.src != want {
		t.Errorf("source = %+v, want %+v", v.src, want)
	}

	if v.res.Head != r.prHead || v.res.Head == r.localOK {
		t.Errorf("head = %s, want the remote's %s, not the worktree's %s", v.res.Head, r.prHead, r.localOK)
	}

	if got := viewPaths(v); !slices.Equal(got, []string{"a.txt", "new.txt"}) {
		t.Errorf("files = %v, want a.txt and new.txt", got)
	}

	if top := plainTop(t, r); !strings.Contains(top, " "+iconPR+" #7 ") {
		t.Errorf("top border %q, want the #7 tag", top)
	}

	r.icons = false

	if top := plainTop(t, r); !strings.Contains(top, " #7 ") {
		t.Errorf("top border %q without icons, want the #7 tag", top)
	}
}

func TestPRModeNotices(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		setup func(t *testing.T, r *prDrawer)
		want  string
	}{
		{"no pull request for the branch", func(t *testing.T, r *prDrawer) {}, fmt.Sprintf(noticePRNone, "feat/x")},
		{"a remote not on GitHub", func(t *testing.T, r *prDrawer) { r.notGitHub.Store(true) }, fmt.Sprintf(noticePRNotGitHub, "feat/x")},
		{"a detached head", func(t *testing.T, r *prDrawer) { r.repo.detach() }, noticePRDetached},
		{"the pull request is not on the remote", func(t *testing.T, r *prDrawer) {
			pr := prSeven()
			pr.Number = 8
			r.run(r.step(snapshotOf(pr)))
		}, fmt.Sprintf(noticePRNotOnRemote, 8, "origin")},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			r := newPRDrawer(t)
			tc.setup(t, r)
			r.opened(t)

			was := r.view().src
			r.typed("p")
			r.until(t, "the notice", func() bool { return r.notice == tc.want })

			if rv := r.review(); rv == nil || rv.PR != nil {
				t.Errorf("review = %+v, want a drawer with no PR", rv)
			}

			if r.view().src != was {
				t.Errorf("source = %+v, want it left as %+v", r.view().src, was)
			}
		})
	}
}

func TestPRModeLooksUpOnce(t *testing.T) {
	t.Parallel()

	r := newPRDrawer(t)
	r.found["feat/x"] = prSeven()
	r.opened(t)
	r.typed("p")
	r.until(t, "PR mode", r.inPRMode(7))

	if got := r.lookedUp(); !slices.Equal(got, []string{prRepoKey + " feat/x"}) {
		t.Errorf("lookups = %v, want one for feat/x", got)
	}
}

func TestPRModeHoldsItsPR(t *testing.T) {
	t.Parallel()

	r := newPRDrawer(t)
	r.poll()

	other := r.fake.repo(t, "other")
	write(t, filepath.Join(other.Root(), "only-in-other.txt"), "other\n")
	other.commit("other")
	write(t, filepath.Join(other.Root(), "dirty.txt"), "dirty\n")

	r.opened(t)
	r.typed("p")
	r.until(t, "PR mode", r.inPRMode(7))

	openIn(t, r.App, core.SetPaneCwd{ID: r.focus(), Cwd: other.Root()})
	r.settle()
	r.settled(t)

	if rv := r.review(); rv.PR == nil || rv.PR.Number != 7 || r.view().pr.Number != 7 || r.view().root != r.root {
		t.Fatalf("review %+v, view root %q: want #7 held on %q", rv, r.view().root, r.root)
	}

	r.typed("b")
	r.until(t, "the followed place", r.viewHolds(func(v *reviewView) bool {
		return v.root == other.Root() && v.loaded && !v.loading && v.pr == (core.ReviewPR{})
	}))

	if rv := r.review(); rv.PR != nil {
		t.Errorf("review = %+v, want PR mode left", rv)
	}

	if _, ok := r.view().src.(git.BranchSource); !ok {
		t.Errorf("source = %+v, want the branch", r.view().src)
	}
}

func TestPRModeLeaves(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		do   func(t *testing.T, r *prDrawer)
	}{
		{"b", func(t *testing.T, r *prDrawer) { r.typed("b") }},
		{"t", func(t *testing.T, r *prDrawer) { r.typed("t") }},
		{",", func(t *testing.T, r *prDrawer) { r.typed(",") }},
		{"a pick in the worktree list", func(t *testing.T, r *prDrawer) {
			openIn(t, r.App, core.PinReview{Root: r.root})
		}},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			f := newFakeGit(t)
			rp := prTwoCommits(t, f)
			rp.turn(string(testSession))
			write(t, filepath.Join(rp.Root(), "turn.txt"), "turn\n")
			rp.turn(string(testSession))

			r := newPRDrawerOn(t, f, prScaffold(t, rp))
			r.poll()
			id := r.agent(t, r.dir, testSession)
			r.run(r.openReview(id, true))
			r.until(t, "the drawer", func() bool { return r.review() != nil })
			r.idle(t)
			r.typed("p")
			r.until(t, "PR mode", r.inPRMode(7))

			tc.do(t, r)
			r.settle()
			r.until(t, "PR mode to end", r.viewHolds(func(v *reviewView) bool {
				return v.pr == (core.ReviewPR{}) && v.loaded && !v.loading
			}))

			if rv := r.review(); rv == nil || rv.PR != nil {
				t.Errorf("review = %+v, want no PR", rv)
			}

			if _, ok := r.view().src.(git.PRSource); ok {
				t.Errorf("source = %+v, want it left the pull request", r.view().src)
			}
		})
	}
}

func TestPRModeLateAnswers(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		move   func(t *testing.T, r *prDrawer)
		stages []string
	}{
		{"the drawer closed", func(t *testing.T, r *prDrawer) {
			openIn(t, r.App, core.CloseReview{})
			r.settle()
		}, []string{"the place answered", "the fetch answered"}},
		{"b", func(t *testing.T, r *prDrawer) { r.typed("b") }, []string{"the place answered", "the fetch answered"}},
		{"a second p", func(t *testing.T, r *prDrawer) { r.pressP() }, []string{"the fetch answered"}},
	}

	for _, tc := range tests {
		for _, stage := range tc.stages {
			t.Run(tc.name+" before "+stage, func(t *testing.T) {
				t.Parallel()

				r := newPRDrawer(t)
				r.poll()
				r.opened(t)

				msgs := flatten(r.pressP())
				if len(msgs) != 1 {
					t.Fatalf("p raised %d messages, want the place's answer", len(msgs))
				}

				late := msgs[0]

				if stage == "the fetch answered" {
					fetch := r.step(msgs[0])

					fm := flatten(fetch)
					if len(fm) != 1 {
						t.Fatalf("the place raised %d messages, want the fetch's answer", len(fm))
					}

					late = fm[0]
				}

				tc.move(t, r)

				r.settle()

				notice, rv := r.notice, r.review()

				if cmd := r.step(late); cmd != nil {
					for _, m := range flatten(cmd) {
						if _, ok := m.(prFetchedMsg); ok {
							t.Errorf("a late answer started a fetch")
						}
					}
				}

				r.settle()

				if got := r.review(); (got == nil) != (rv == nil) || got != nil && got.PR != nil {
					t.Errorf("review = %+v after the late answer, want no PR (was %+v)", got, rv)
				}

				if r.notice != notice {
					t.Errorf("notice = %q after the late answer, was %q", r.notice, notice)
				}
			})
		}
	}
}

func TestPRModeIgnoresBatches(t *testing.T) {
	t.Parallel()

	r := newPRDrawer(t)
	r.poll()
	r.opened(t)
	r.typed("p")
	r.until(t, "PR mode", r.inPRMode(7))

	v := r.view()
	gen := v.gen

	write(t, filepath.Join(r.dir, "a.txt"), "edited after the fetch\n")
	r.run(r.batch(r.root, git.Batch{Paths: []string{"a.txt"}}))
	r.run(r.batch(r.root, git.Batch{Git: true}))
	r.run(r.reviewRefresh(r.focus()))
	r.settle()

	if v.loading || v.queued != nil || v.gen != gen {
		t.Errorf("loading %v, queued %v, gen %d (was %d): a batch reloaded a pull request diff", v.loading, v.queued, v.gen, gen)
	}
}

func TestPRModeRefresh(t *testing.T) {
	t.Parallel()

	t.Run("pull request", func(t *testing.T) {
		t.Parallel()

		r := newPRDrawer(t)
		r.poll()
		r.opened(t)
		r.typed("p")
		r.until(t, "PR mode", r.inPRMode(7))

		write(t, filepath.Join(r.dir, "pushed-later.txt"), "later\n")
		newHead := r.repo.commit("pushed later")
		r.origin.pr(7, newHead)

		r.typed("r")
		r.until(t, "the new head", func() bool { return r.view().res.Head == newHead && !r.view().loading })

		if got := r.rec.refreshed(); !slices.Equal(got, []github.Repo{prRepo}) {
			t.Errorf("refreshed %v, want the pull request's repository", got)
		}

		if !slices.Contains(viewPaths(r.view()), "pushed-later.txt") {
			t.Errorf("files = %v, want the new commit's file", viewPaths(r.view()))
		}
	})

	t.Run("branch", func(t *testing.T) {
		t.Parallel()

		r := newPRDrawer(t)
		r.poll()
		r.opened(t)

		write(t, filepath.Join(r.dir, "fresh.txt"), "fresh\n")
		r.typed("r")
		r.until(t, "the reload", func() bool { return slices.Contains(viewPaths(r.view()), "fresh.txt") })

		if got := r.rec.refreshed(); len(got) != 0 {
			t.Errorf("refreshed %v in branch mode, want none", got)
		}
	})
}

func TestPRModeWatchesTheFocusedPR(t *testing.T) {
	t.Parallel()

	r := newPRDrawer(t)
	r.poll()
	r.opened(t)
	r.typed("p")
	r.until(t, "PR mode", r.inPRMode(7))

	want := []github.Watch{{Repo: prRepo, Visible: []int{7}, Focused: 7}}
	if got := r.rec.last(); !slices.EqualFunc(got, want, func(x, y github.Watch) bool {
		return x.Repo == y.Repo && x.Focused == y.Focused && slices.Equal(x.Visible, y.Visible)
	}) {
		t.Errorf("watches = %+v, want %+v for a repository no agent follows", got, want)
	}

	r.typed("b")
	r.until(t, "branch mode", r.viewHolds(func(v *reviewView) bool { return v.pr == (core.ReviewPR{}) && !v.loading }))

	if got := r.rec.last(); len(got) != 0 {
		t.Errorf("watches = %+v after leaving PR mode, want none", got)
	}
}

func TestPRModeHints(t *testing.T) {
	t.Parallel()

	r := newPRDrawer(t)
	sized(t, r.App, 300, 40)
	r.poll()
	r.opened(t)

	branch := r.drawn(t)
	if !strings.Contains(branch, "j/k line   ]/[ hunk   }/{ file   z zoom   tab tree") {
		t.Errorf("branch mode hints changed:\n%s", branch)
	}

	r.typed("p")
	r.until(t, "PR mode", r.inPRMode(7))

	text := r.drawn(t)
	if !strings.Contains(text, "j/k line   ]/[ hunk   z zoom   i conversation   c checks   a approve   M merge   O browser   space reviewed   R show reviewed   b/t mode") {
		t.Errorf("PR mode hints missing j/k line, ]/[ hunk, the mark pairs, c checks, a approve, M merge, O browser, b/t mode:\n%s", text)
	}

	for _, gone := range []string{"}/{ file", "w worktree", "e edit", "/ search", "tab tree"} {
		if strings.Contains(text, gone) {
			t.Errorf("PR mode hints hold %q:\n%s", gone, text)
		}
	}
}

func TestPRModeShowsTheModeRow(t *testing.T) {
	t.Parallel()

	r := newPRDrawer(t)
	r.poll()
	r.opened(t)
	r.showTree(t)
	r.typed("p")
	r.until(t, "PR mode", r.inPRMode(7))

	if text := r.drawn(t); !strings.Contains(text, iconPR+" #7 vs main") {
		t.Errorf("drawer lacks the mode row:\n%s", text)
	}
}

func TestPRThenIsConsumedWhenTheResultLands(t *testing.T) {
	t.Parallel()

	r := newPRDrawer(t)
	r.poll()
	r.opened(t)

	r.run(r.showPR(r.store.State().Tab().ID, thenChecks))
	r.until(t, "PR mode", func() bool { return r.review().PR != nil && r.view().then == thenChecks })
	r.until(t, "the result", r.inPRMode(7))

	if got := r.view().then; got != thenNothing {
		t.Errorf("then = %v after the result landed, want it consumed", got)
	}
}

func TestOpenWithPR(t *testing.T) {
	t.Parallel()

	t.Run("the drawer closed", func(t *testing.T) {
		t.Parallel()

		r := newPRDrawer(t)
		r.poll()

		pane := r.focus()
		r.run(r.openWithPR(pane, &prWant{repo: prRepoKey, number: 7, root: r.root}, thenNothing))
		r.until(t, "PR mode", r.inPRMode(7))

		if rv := r.review(); rv.Pane != pane {
			t.Errorf("drawer on %s, want %s", rv.Pane, pane)
		}
	})

	t.Run("open on another pane", func(t *testing.T) {
		t.Parallel()

		r := newPRDrawer(t)
		r.poll()
		r.opened(t)

		second := r.shell(t, r.dir)
		r.run(r.openWithPR(second, &prWant{repo: prRepoKey, number: 7, root: r.root}, thenNothing))
		r.until(t, "PR mode", r.inPRMode(7))

		if rv := r.review(); rv.Pane != second {
			t.Errorf("drawer on %s, want %s", rv.Pane, second)
		}
	})

	t.Run("a leader d before the open answers", func(t *testing.T) {
		t.Parallel()

		r := newPRDrawer(t)
		r.poll()

		pane := r.focus()
		stale := r.openWithPR(pane, &prWant{repo: prRepoKey, number: 7, root: r.root}, thenNothing)

		r.leader('d')
		r.until(t, "the drawer", func() bool { return r.review() != nil })
		r.idle(t)

		for _, m := range flatten(stale) {
			r.run(r.step(m))
		}

		r.settle()
		r.settled(t)

		if rv := r.review(); rv == nil || rv.PR != nil {
			t.Errorf("review = %+v, want a drawer that shows no pull request", rv)
		}
	})
}

func TestOpenWithPROutsideARepository(t *testing.T) {
	t.Parallel()

	r := newPRDrawer(t)
	r.poll()

	pane := r.shell(t, t.TempDir())
	r.run(r.openWithPR(pane, &prWant{repo: prRepoKey, number: 7, root: r.root}, thenChecks))
	r.until(t, "the notice", func() bool { return r.notice == noticeNotRepo })

	openIn(t, r.App, core.SetPaneCwd{ID: pane, Cwd: r.dir})
	r.settle()

	if rv := r.review(); rv != nil {
		t.Errorf("review = %+v, want no drawer", rv)
	}

	if r.drawer.prNext != nil {
		t.Errorf("prNext = %+v, want the wait gone", r.drawer.prNext)
	}
}

func TestOpenWithPRDropsAWaitOnAViewWithoutASource(t *testing.T) {
	t.Parallel()

	r := newPRDrawer(t)
	r.opened(t)

	tab := r.store.State().Tab()
	v := r.view()
	v.src, v.err = nil, errNotRepo
	r.drawer.prNext = &prNext{tab: tab.ID, pane: tab.Review.Pane, then: thenNothing, open: true}

	if cmd := r.prNextAfterSync(); cmd != nil || r.drawer.prNext != nil {
		t.Errorf("cmd %v, prNext %+v: want the wait dropped and nothing asked", cmd != nil, r.drawer.prNext)
	}
}

func TestPRPlaceErrors(t *testing.T) {
	t.Parallel()

	resolve := func(context.Context, string, string) (github.Repo, error) { return prRepo, nil }
	f := newFakeGit(t)

	if _, err := resolvePRPlace(context.Background(), f, resolve, t.TempDir()); !errors.Is(err, errPRNoRepo) {
		t.Errorf("outside a repository: err = %v, want %v", err, errPRNoRepo)
	}

	r := prTwoCommits(t, f)
	r.branch("feat/y")

	if _, err := resolvePRPlace(context.Background(), f, resolve, r.Root()); !errors.Is(err, errPRNoRemote) {
		t.Errorf("with no remote: err = %v, want %v", err, errPRNoRemote)
	}
}

// prHeaderLines are the drawer's lines of a scene in PR mode, without styling.
func prHeaderLines(t *testing.T, a *App) []string {
	t.Helper()

	st := a.store.State()

	r, ok := a.drawerRect(st)
	if !ok {
		t.Fatal("no drawer on screen")
	}

	return strings.Split(ansi.Strip(strings.Join(a.drawerLines(st, r), "\n")), "\n")
}

func TestPRHeader(t *testing.T) {
	t.Parallel()

	const moved = "the PR moved to bbbbbbb since this diff; r fetches it"

	tests := []struct {
		name     string
		tune     func(*core.PRInfo)
		unpushed int
		icons    bool
		want     []string
		absent   []string
		rows     int
	}{
		{
			name: "passed and approved", icons: true, rows: 3,
			want:   []string{"#7 Show the pull request over its diff", iconGit + " feat/review-pane → main", iconPassed + " 4/4", "approved", "@mhersson", "3h"},
			absent: []string{"not pushed", "moved", "no review yet"},
		},
		{
			name: "running", icons: true, rows: 3,
			tune: func(p *core.PRInfo) { p.ChecksDone = 1 },
			want: []string{iconRunning + " 1/4"},
		},
		{
			name: "failed", icons: true, rows: 3,
			tune: func(p *core.PRInfo) { p.ChecksFailed = 2 },
			want: []string{iconFailed + " 2 failed"},
		},
		{
			name: "changes requested", icons: true, rows: 3,
			tune: func(p *core.PRInfo) { p.ReviewDecision = "CHANGES_REQUESTED" },
			want: []string{"changes requested"},
		},
		{
			name: "no review yet", icons: true, rows: 3,
			tune:   func(p *core.PRInfo) { p.ReviewDecision = "" },
			want:   []string{"no review yet"},
			absent: []string{"approved"},
		},
		{
			name: "no checks", icons: true, rows: 3,
			tune:   func(p *core.PRInfo) { p.ChecksTotal, p.ChecksDone = 0, 0 },
			absent: []string{iconPassed, "0/0"},
		},
		{
			name: "unpushed commits", icons: true, rows: 4, unpushed: 2,
			want: []string{"↑2 not pushed: the PR shows the pushed head; ctrl+space g P pushes"},
		},
		{
			name: "the poll's head differs", icons: true, rows: 4, unpushed: 2,
			tune:   func(p *core.PRInfo) { p.HeadOID = "bbbbbbb0000000000000000000000000000000bb" },
			want:   []string{moved},
			absent: []string{"not pushed"},
		},
		{
			name: "no icons", rows: 3,
			want:   []string{"feat/review-pane → main", "ci 4/4", "approved"},
			absent: []string{iconGit, iconPassed},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			s := reviewScene{
				theme: "catppuccin-mocha", icons: tc.icons, w: 200, h: 52, res: turn4Result(), turn: 4, turns: 4, focused: true,
				branch: true, tune: func(t *testing.T, a *App, v *reviewView) { prSceneWith(tc.tune, tc.unpushed)(t, a, v) },
			}
			a := s.build(t)

			text := strings.Join(prHeaderLines(t, a), "\n")
			for _, w := range tc.want {
				if !strings.Contains(text, w) {
					t.Errorf("drawer lacks %q:\n%s", w, text)
				}
			}

			for _, w := range tc.absent {
				if strings.Contains(text, w) {
					t.Errorf("drawer holds %q:\n%s", w, text)
				}
			}

			v := a.drawer.views[a.store.State().Tab().ID]
			info, _ := prInfoByNumber(a.store.State(), prRepoKey, 7)

			if got := prHeaderRows(v, info); got != tc.rows {
				t.Errorf("prHeaderRows = %d, want %d", got, tc.rows)
			}
		})
	}
}

func TestPRHeaderKeepsTheCursorInView(t *testing.T) {
	t.Parallel()

	s := reviewScene{
		theme: "catppuccin-mocha", icons: true, w: 200, h: 24, res: turn4Result(), turn: 4, turns: 4, focused: true,
		branch: true, tune: func(t *testing.T, a *App, v *reviewView) { prSceneWith(nil, 1)(t, a, v); v.cursor = 0 },
	}
	a := s.build(t)
	st := a.store.State()
	v := a.drawer.views[st.Tab().ID]

	box, _ := a.drawerFull(st)
	lay := a.drawerLayoutOf(box, v, drawerTree(st))

	info, _ := prInfoByNumber(st, prRepoKey, 7)
	head := prHeaderRows(v, info)

	if head != 4 || lay.bodyH-lay.diffH != head {
		t.Fatalf("header %d rows, bodyH %d, diffH %d: want the diff column to lose 4", head, lay.bodyH, lay.diffH)
	}

	if got := a.reviewBodyH(v); got != lay.diffH {
		t.Fatalf("reviewBodyH = %d, want %d", got, lay.diffH)
	}

	a.Update(keyRune('G'))

	if v.cursor != len(v.rows)-1 || v.top != len(v.rows)-lay.diffH {
		t.Errorf("G: cursor %d top %d, want %d and %d", v.cursor, v.top, len(v.rows)-1, len(v.rows)-lay.diffH)
	}

	// The last row is drawn on the last row of the diff column.
	last := strings.Join(rowTexts(v.res, v.rows[len(v.rows)-1]), "")
	lines := prHeaderLines(t, a)

	if row := lines[1+lay.bodyH-1]; !strings.Contains(row, strings.TrimSpace(last)) {
		t.Errorf("last diff row of the drawer %q does not hold the last line %q", row, last)
	}

	a.Update(keyRune('g'))
	a.Update(tea.KeyPressMsg{Code: 'd', Mod: tea.ModCtrl})

	if v.cursor != lay.diffH/2 {
		t.Errorf("ctrl+d: cursor = %d, want half the diff column, %d", v.cursor, lay.diffH/2)
	}

	a.Update(keyRune('g'))

	r, _ := a.drawerRect(st)
	k := 3 // the diff column's fourth row

	click(a, r.X+1+lay.treeW+3, r.Y+1+head+k)

	if v.cursor != k {
		t.Errorf("a click on diff row %d put the cursor on %d", k, v.cursor)
	}

	a.Update(keyRune('g'))
	mdown(a, r.X+1+lay.treeW+3, r.Y+1+head-1, tea.MouseLeft, 0)

	if v.cursor != 0 {
		t.Errorf("a click on the header moved the cursor to %d", v.cursor)
	}
}

func TestPRHeaderNarrow(t *testing.T) {
	t.Parallel()

	long := strings.Repeat("a long title ", 12)
	s := reviewScene{
		theme: "catppuccin-mocha", icons: true, w: 60, h: 40, res: turn4Result(), turn: 4, turns: 4, focused: true,
		branch: true, tune: func(t *testing.T, a *App, v *reviewView) {
			prSceneWith(func(p *core.PRInfo) {
				p.Title, p.HeadBranch, p.Author = long, "feature/"+long, "someone-with-a-long-name"
				p.HeadOID = "bbbbbbb0000000000000000000000000000000bb"
			}, 3)(t, a, v)
		},
	}
	a := s.build(t)
	st := a.store.State()

	r, _ := a.drawerRect(st)
	lines, _, fits := a.drawerDrawn(st, r)

	if len(lines) != r.H || !fits {
		t.Fatalf("drawer drew %d lines (want %d), fits %v: a header row wrapped or overran", len(lines), r.H, fits)
	}

	plain := prHeaderLines(t, a)
	for _, i := range []int{1, 2} {
		if !strings.Contains(plain[i], "…") {
			t.Errorf("header row %d is not cut with …: %q", i, plain[i])
		}
	}
}

func TestPRFetchCounter(t *testing.T) {
	t.Parallel()

	for _, overtaken := range []bool{false, true} {
		t.Run(fmt.Sprintf("overtaken=%v", overtaken), func(t *testing.T) {
			t.Parallel()

			r := newPRDrawer(t)
			r.poll()
			r.opened(t)

			msgs := flatten(r.pressP())
			if len(msgs) != 1 {
				t.Fatalf("p raised %d messages, want the place's answer", len(msgs))
			}

			fetch := r.step(msgs[0])

			if got := r.drawer.fetching[7]; got != 1 {
				t.Fatalf("fetching[7] = %d once the fetch started, want 1", got)
			}

			fm := flatten(fetch)
			if len(fm) != 1 {
				t.Fatalf("the fetch raised %d messages, want its answer", len(fm))
			}

			if overtaken {
				r.drawer.prSeq++
			}

			r.step(fm[0])

			if n, ok := r.drawer.fetching[7]; ok {
				t.Errorf("fetching[7] = %d after the answer, want no entry", n)
			}
		})
	}
}
