package ui

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/charmbracelet/x/ansi"
	"github.com/charmbracelet/x/exp/golden"

	"github.com/mhersson/aim/internal/core"
	"github.com/mhersson/aim/internal/github"
)

type approveCall struct {
	repo   github.Repo
	number int
	sha    string
}

// fakeActions records the GitHub writes the app asks for, so no test reaches GitHub.
type fakeActions struct {
	mu        sync.Mutex
	approvals []approveCall
	merges    []mergeCall
	fail      error
	hold      bool // block until the context ends

	mergeRes  github.Merged
	mergeFail error

	creates    []createCall
	createRes  github.PR
	createFail error
}

type createCall struct {
	repo github.Repo
	pr   github.NewPR
}

type mergeCall struct {
	repo github.Repo
	pr   github.PR
	info github.RepoInfo
	opts github.MergeOptions
}

func (f *fakeActions) CreatePR(ctx context.Context, r github.Repo, n github.NewPR) (github.PR, error) {
	f.mu.Lock()
	f.creates = append(f.creates, createCall{r, n})
	res, fail, hold := f.createRes, f.createFail, f.hold
	f.mu.Unlock()

	if hold {
		<-ctx.Done()

		return github.PR{}, ctx.Err()
	}

	return res, fail
}

func (f *fakeActions) created() []createCall {
	f.mu.Lock()
	defer f.mu.Unlock()

	return slices.Clone(f.creates)
}

func (f *fakeActions) Merge(ctx context.Context, r github.Repo, pr github.PR, info github.RepoInfo, opts github.MergeOptions) (github.Merged, error) {
	f.mu.Lock()
	f.merges = append(f.merges, mergeCall{r, pr, info, opts})
	res, fail, hold := f.mergeRes, f.mergeFail, f.hold
	f.mu.Unlock()

	if hold {
		<-ctx.Done()

		return github.Merged{}, ctx.Err()
	}

	return res, fail
}

func (f *fakeActions) merged() []mergeCall {
	f.mu.Lock()
	defer f.mu.Unlock()

	return slices.Clone(f.merges)
}

func (f *fakeActions) Approve(ctx context.Context, r github.Repo, number int, sha string) error {
	f.mu.Lock()
	f.approvals = append(f.approvals, approveCall{r, number, sha})
	fail, hold := f.fail, f.hold
	f.mu.Unlock()

	if hold {
		<-ctx.Done()

		return ctx.Err()
	}

	return fail
}

func (f *fakeActions) approved() []approveCall {
	f.mu.Lock()
	defer f.mu.Unlock()

	return slices.Clone(f.approvals)
}

// inPRDrawer opens the drawer in PR mode on pull request 7, as tune changes it
// in the poll, with fake GitHub writes installed.
func inPRDrawer(t *testing.T, tune func(*github.PR)) (*prDrawer, *fakeActions) {
	t.Helper()

	r := newPRDrawer(t)
	f := &fakeActions{}
	r.actions = f

	pr := prSeven()
	if tune != nil {
		tune(&pr)
	}

	r.run(r.step(snapshotOf(pr)))
	r.opened(t)
	r.typed("p")
	r.until(t, "PR mode", r.inPRMode(7))

	return r, f
}

func TestApproveAtTheShownHead(t *testing.T) {
	t.Parallel()

	r, f := inPRDrawer(t, nil)
	r.typed("a")

	c := r.confirm
	if c == nil || c.approve == nil {
		t.Fatalf("a opened no approve confirm; notice %q", r.notice)
	}

	if c.approve.sha != r.prHead || c.approve.number != 7 || c.approve.repo != prRepo {
		t.Errorf("target = %+v, want #7 of %s at %s", *c.approve, prRepo, r.prHead)
	}

	if got := f.approved(); len(got) != 0 {
		t.Fatalf("approved %v before enter", got)
	}

	r.keys(keyEnter)
	r.until(t, "the approval", func() bool { return len(f.approved()) == 1 && r.notice != "" })

	if got := f.approved(); !slices.Equal(got, []approveCall{{prRepo, 7, r.prHead}}) {
		t.Errorf("approved %v, want #7 at %s", got, r.prHead)
	}

	if r.confirm != nil {
		t.Error("the confirm is still open after enter")
	}

	if !strings.Contains(r.notice, prNumberText(7)) || r.noticeLevel() != levelSuccess {
		t.Errorf("toast = %q at level %v, want one naming #7 at the success level", r.notice, r.noticeLevel())
	}

	if got := r.rec.refreshed(); !slices.Equal(got, []github.Repo{prRepo}) {
		t.Errorf("refreshed %v, want %v", got, []github.Repo{prRepo})
	}
}

func TestApproveFromBranchMode(t *testing.T) {
	t.Parallel()

	t.Run("shows the pull request first", func(t *testing.T) {
		t.Parallel()

		r := newPRDrawer(t)
		f := &fakeActions{}
		r.actions = f

		r.poll()
		r.opened(t)
		r.typed("a")

		if r.confirm != nil {
			t.Fatal("a opened the confirm before the pull request loaded")
		}

		r.until(t, "the confirm", func() bool { return r.confirm != nil })

		if got := r.confirm.approve; got == nil || got.sha != r.prHead {
			t.Errorf("target = %+v, want the loaded head %s", got, r.prHead)
		}

		if got := f.approved(); len(got) != 0 {
			t.Errorf("approved %v before enter", got)
		}
	})

	t.Run("b before the load opens nothing", func(t *testing.T) {
		t.Parallel()

		r := newPRDrawer(t)
		r.actions = &fakeActions{}

		r.poll()
		r.opened(t)

		r.typed("a")
		r.typed("b")
		r.settle()
		r.settled(t)

		if r.confirm != nil {
			t.Errorf("a b before the load left a confirm open: %+v", *r.confirm)
		}
	})
}

func TestApproveOwnPR(t *testing.T) {
	t.Parallel()

	for _, mode := range []string{"PR mode", "branch mode"} {
		t.Run(mode, func(t *testing.T) {
			t.Parallel()

			own := func(pr *github.PR) { pr.ViewerAuthored = true }

			var r *prDrawer

			if mode == "PR mode" {
				r, _ = inPRDrawer(t, own)
			} else {
				r = newPRDrawer(t)
				r.actions = &fakeActions{}
				pr := prSeven()
				own(&pr)
				r.run(r.step(snapshotOf(pr)))
				r.opened(t)
			}

			r.typed("a")

			r.until(t, "the notice", func() bool { return r.notice == noticeApproveOwn })

			if r.confirm != nil {
				t.Error("a confirm opened for your own pull request")
			}
		})
	}
}

func TestApproveNotOpen(t *testing.T) {
	t.Parallel()

	tests := []struct{ state, want string }{
		{"MERGED", fmt.Sprintf(noticeApproveClosed, 7, "merged")},
		{"CLOSED", fmt.Sprintf(noticeApproveClosed, 7, "closed")},
	}

	for _, tc := range tests {
		t.Run(tc.state, func(t *testing.T) {
			t.Parallel()

			r, _ := inPRDrawer(t, nil)

			info, _ := prInfoByNumber(r.store.State(), prRepoKey, 7)
			info.State = tc.state

			openIn(t, r.App,
				core.SetRepoPRs{Repo: prRepoKey, DefaultBranch: "main"},
				core.SetBranchPR{Repo: prRepoKey, Branch: "feat/x", PR: info, Found: true},
			)

			r.typed("a")

			if r.confirm != nil {
				t.Error("a confirm opened for a pull request that is not open")
			}

			if r.notice != tc.want {
				t.Errorf("notice = %q, want %q", r.notice, tc.want)
			}
		})
	}
}

func TestApproveEscSendsNothing(t *testing.T) {
	t.Parallel()

	r, f := inPRDrawer(t, nil)
	r.typed("a")
	r.keys(keyEsc)

	if r.confirm != nil {
		t.Error("esc left the confirm open")
	}

	r.typed("a")
	r.typed("y")

	if r.confirm == nil {
		t.Error("y answered the approve confirm; only enter and esc do")
	}

	r.settle()

	if got := f.approved(); len(got) != 0 {
		t.Errorf("approved %v, want nothing", got)
	}
}

func TestApproveError(t *testing.T) {
	t.Parallel()

	r, f := inPRDrawer(t, nil)
	f.fail = errors.New("github: Can not approve your own pull request")

	r.typed("a")
	r.keys(keyEnter)
	r.until(t, "the toast", func() bool { return r.notice != "" })

	if !strings.Contains(r.notice, prNumberText(7)) || !strings.Contains(r.notice, f.fail.Error()) || r.noticeLevel() != levelError {
		t.Errorf("toast = %q at level %v, want one naming #7 and %q at the error level", r.notice, r.noticeLevel(), f.fail)
	}

	if got := r.rec.refreshed(); len(got) != 0 {
		t.Errorf("refreshed %v after a failed approve", got)
	}
}

func TestApproveTargetIsFrozen(t *testing.T) {
	t.Parallel()

	r, f := inPRDrawer(t, nil)
	r.typed("a")

	moved := prSeven()
	moved.HeadOID = strings.Repeat("e", 40)
	moved.Title = "A different title"
	r.run(r.step(snapshotOf(moved)))

	r.keys(keyEnter)
	r.until(t, "the approval", func() bool { return len(f.approved()) == 1 })

	if got := f.approved(); !slices.Equal(got, []approveCall{{prRepo, 7, r.prHead}}) {
		t.Errorf("approved %v, want the shown head %s", got, r.prHead)
	}
}

func TestApproveWithoutActions(t *testing.T) {
	t.Parallel()

	r, _ := inPRDrawer(t, nil)
	r.actions = nil

	r.typed("a")

	if r.confirm != nil || r.notice != noticePRsOff {
		t.Errorf("confirm %v, notice %q, want no confirm and %q", r.confirm, r.notice, noticePRsOff)
	}
}

func TestApproveEndsAtClose(t *testing.T) {
	t.Parallel()

	r, f := inPRDrawer(t, nil)
	f.hold = true

	r.typed("a")
	r.keys(keyEnter)
	r.until(t, "the call", func() bool { return len(f.approved()) == 1 })

	r.cancel()
	r.settle()
	r.settled(t)

	if r.notice != "" {
		t.Errorf("notice = %q after the app closed, want none", r.notice)
	}
}

// approveScenes are the confirm over the PR drawer, at the usual size and at 80x24.
func approveScenes() []struct {
	name  string
	w, h  int
	title string
} {
	return []struct {
		name  string
		w, h  int
		title string
	}{
		{"wide", 200, 52, "Show the pull request over its diff"},
		{"80x24", 80, 24, "A very long title that cannot fit in the box at all, not even at 80 columns of width"},
		{"hostile", 80, 24, "bad \x1b[31mtitle\x07 here"},
	}
}

func approveScene(t *testing.T, icons bool, w, h int, title string) *App {
	t.Helper()

	s := reviewScene{
		theme: "catppuccin-mocha", icons: icons, w: w, h: h, res: turn4Result(), turn: 4, turns: 4, focused: true,
		branch: true, tune: func(t *testing.T, a *App, v *reviewView) {
			t.Helper()

			prSceneWith(nil, 0)(t, a, v)

			a.confirm = &confirm{approve: &approveTarget{repo: prRepo, number: 7, title: title, sha: prHeadOID}}
		},
	}

	return s.build(t)
}

// TestApproveGolden draws the confirm once in colour; it compares highlighted
// colours, so it runs serially.
func TestApproveGolden(t *testing.T) {
	for _, c := range approveScenes() {
		t.Run("catppuccin-mocha/icons=true/"+c.name, func(t *testing.T) {
			golden.RequireEqual(t, []byte(approveScene(t, true, c.w, c.h, c.title).View().Content))
		})
	}
}

// TestApproveText is the same screens without styling, so the layout reads.
func TestApproveText(t *testing.T) {
	t.Parallel()

	for _, icons := range []bool{true, false} {
		for _, c := range approveScenes() {
			t.Run(fmt.Sprintf("icons=%v/%s", icons, c.name), func(t *testing.T) {
				t.Parallel()

				got := ansi.Strip(approveScene(t, icons, c.w, c.h, c.title).View().Content)
				golden.RequireEqual(t, []byte(got))
			})
		}
	}
}
