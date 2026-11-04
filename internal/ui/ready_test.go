package ui

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
	"github.com/charmbracelet/x/exp/golden"

	"github.com/mhersson/aim/internal/core"
	"github.com/mhersson/aim/internal/github"
)

type readyCall struct {
	repo github.Repo
	id   string
}

func (f *fakeActions) MarkReady(ctx context.Context, r github.Repo, id string) error {
	f.mu.Lock()
	f.readies = append(f.readies, readyCall{r, id})
	fail, hold := f.readyFail, f.hold
	f.mu.Unlock()

	if hold {
		<-ctx.Done()

		return ctx.Err()
	}

	return fail
}

func (f *fakeActions) readied() []readyCall {
	f.mu.Lock()
	defer f.mu.Unlock()

	return slices.Clone(f.readies)
}

// draftSeven makes pull request 7 a draft with the node id a ready call takes.
func draftSeven(pr *github.PR) { pr.Draft, pr.ID = true, "PR_7" }

func TestReadyAtTheShownPR(t *testing.T) {
	t.Parallel()

	r, f := inPRDrawer(t, draftSeven)
	r.typed("R")

	c := r.confirm
	if c == nil || c.ready == nil {
		t.Fatalf("R opened no ready confirm; notice %q", r.notice)
	}

	if want := (readyTarget{repo: prRepo, number: 7, id: "PR_7", title: "Add x"}); *c.ready != want {
		t.Errorf("target = %+v, want %+v", *c.ready, want)
	}

	if got := f.readied(); len(got) != 0 {
		t.Fatalf("marked %v ready before enter", got)
	}

	r.keys(keyEnter)
	r.until(t, "the call", func() bool { return len(f.readied()) == 1 && r.notice != "" })

	if got := f.readied(); !slices.Equal(got, []readyCall{{prRepo, "PR_7"}}) {
		t.Errorf("marked %v ready, want #7 by its id", got)
	}

	if !strings.Contains(r.notice, prNumberText(7)) || r.noticeLevel() != levelSuccess {
		t.Errorf("toast = %q at level %v, want one naming #7 at the success level", r.notice, r.noticeLevel())
	}

	if got := r.rec.refreshed(); !slices.Equal(got, []github.Repo{prRepo}) {
		t.Errorf("refreshed %v, want %v", got, []github.Repo{prRepo})
	}
}

func TestReadyFromBranchMode(t *testing.T) {
	t.Parallel()

	r := newPRDrawer(t)
	r.actions = &fakeActions{}

	pr := prSeven()
	draftSeven(&pr)
	r.run(r.step(snapshotOf(pr)))
	r.opened(t)
	r.typed("R")

	r.until(t, "the confirm", func() bool { return r.confirm != nil })

	if got := r.confirm.ready; got == nil || got.number != 7 || got.id != "PR_7" {
		t.Errorf("target = %+v, want #7 by its id", got)
	}

	if rv := r.review(); rv.PR != nil {
		t.Errorf("the drawer shows %+v; R loads no pull request", *rv.PR)
	}
}

func TestReadyLateAnswerNeedsTheKeyboard(t *testing.T) {
	t.Parallel()

	r := newPRDrawer(t)
	r.actions = &fakeActions{}

	pr := prSeven()
	draftSeven(&pr)
	r.run(r.step(snapshotOf(pr)))
	r.opened(t)
	r.typed("R")
	r.leader('h')

	r.until(t, "the notice", func() bool { return r.notice != "" })

	if r.confirm != nil {
		t.Errorf("the late answer opened a confirm: %+v", *r.confirm)
	}

	if want := r.prLoadedNotice(7, thenReady); r.notice != want {
		t.Errorf("notice = %q, want %q", r.notice, want)
	}
}

func TestReadyFromTheList(t *testing.T) {
	t.Parallel()

	r := otherTabsRig(t)
	f, _ := r.actions.(*fakeActions)
	infra := github.Repo{Host: "github.com", Owner: "acme", Name: "infra"}

	// acme/infra has no checkout here.
	r.selectRow(t, infra.String())
	r.keys(keyRune('R'))

	c := r.confirm
	if c == nil || c.ready == nil {
		t.Fatalf("R opened no ready confirm; notice %q", r.notice)
	}

	if want := (readyTarget{repo: infra, number: 88, id: "PR_88", title: "Bump"}); *c.ready != want {
		t.Errorf("target = %+v, want %+v", *c.ready, want)
	}

	r.keys(keyEnter)
	r.until(t, "the call", func() bool { return len(f.readied()) == 1 && r.notice != "" })

	if got := f.readied(); !slices.Equal(got, []readyCall{{infra, "PR_88"}}) {
		t.Errorf("marked %v ready, want #88 by its id", got)
	}

	if r.prList == nil || r.review() != nil {
		t.Errorf("list open %v, drawer %+v; want the list still open and no drawer", r.prList != nil, r.review())
	}
}

func TestReadyRefused(t *testing.T) {
	t.Parallel()

	// with sets the state's pull request 7 after the drawer has shown the draft.
	with := func(state string) func(*prDrawer) {
		return func(r *prDrawer) {
			info, _ := prInfoByNumber(r.store.State(), prRepoKey, 7)
			info.State = state

			openIn(t, r.App,
				core.SetRepoPRs{Repo: prRepoKey, DefaultBranch: "main"},
				core.SetBranchPR{Repo: prRepoKey, Branch: "feat/x", PR: info, Found: true},
			)
		}
	}

	tests := []struct {
		name  string
		tune  func(*github.PR)
		after func(*prDrawer)
		want  string
	}{
		{"not a draft", nil, nil, fmt.Sprintf(noticeReadyNotDraft, 7)},
		{"merged", draftSeven, with("MERGED"), fmt.Sprintf(noticeReadyClosed, 7, "merged")},
		{"closed", draftSeven, with("CLOSED"), fmt.Sprintf(noticeReadyClosed, 7, "closed")},
		{"not polled", draftSeven, func(r *prDrawer) {
			openIn(t, r.App, core.SetRepoPRs{Repo: prRepoKey, DefaultBranch: "main"})
		}, fmt.Sprintf(noticeReadyUnknown, 7)},
		{"actions off", draftSeven, func(r *prDrawer) { r.actions = nil }, noticePRsOff},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			r, f := inPRDrawer(t, tc.tune)
			if tc.after != nil {
				tc.after(r)
			}

			r.typed("R")
			r.settle()

			if r.confirm != nil || r.notice != tc.want {
				t.Errorf("confirm %+v, notice %q; want no confirm and %q", r.confirm, r.notice, tc.want)
			}

			if got := f.readied(); len(got) != 0 {
				t.Errorf("marked %v ready", got)
			}
		})
	}
}

func TestReadyEscSendsNothing(t *testing.T) {
	t.Parallel()

	r, f := inPRDrawer(t, draftSeven)
	r.typed("R")
	r.keys(keyEsc)

	if r.confirm != nil {
		t.Error("esc left the confirm open")
	}

	r.typed("R")
	r.typed("y")

	if r.confirm == nil {
		t.Error("y answered the ready confirm; only enter and esc do")
	}

	r.settle()

	if got := f.readied(); len(got) != 0 {
		t.Errorf("marked %v ready, want nothing", got)
	}
}

func TestReadyError(t *testing.T) {
	t.Parallel()

	r, f := inPRDrawer(t, draftSeven)
	f.readyFail = errors.New("github: mutation MarkReady: Resource not accessible by personal access token")

	r.typed("R")
	r.keys(keyEnter)
	r.until(t, "the toast", func() bool { return r.notice != "" })

	if !strings.Contains(r.notice, prNumberText(7)) || !strings.Contains(r.notice, f.readyFail.Error()) || r.noticeLevel() != levelError {
		t.Errorf("toast = %q at level %v, want one naming #7 and %q at the error level", r.notice, r.noticeLevel(), f.readyFail)
	}

	if got := r.rec.refreshed(); len(got) != 0 {
		t.Errorf("refreshed %v after a failed ready", got)
	}
}

func TestReadyEndsAtClose(t *testing.T) {
	t.Parallel()

	r, f := inPRDrawer(t, draftSeven)
	f.hold = true

	r.typed("R")
	r.keys(keyEnter)
	r.until(t, "the call", func() bool { return len(f.readied()) == 1 })

	r.cancel()
	r.settle()
	feedUntil[readiedMsg](t, r.drawerRig)
	r.settled(t)

	if r.notice != "" {
		t.Errorf("notice = %q after the app closed, want none", r.notice)
	}
}

func TestReadyHint(t *testing.T) {
	t.Parallel()

	for _, draft := range []bool{true, false} {
		t.Run(fmt.Sprintf("drawer/draft=%v", draft), func(t *testing.T) {
			t.Parallel()

			s := reviewScene{
				theme: "catppuccin-mocha", icons: true, w: 312, h: 52, res: turn4Result(), turn: 4, turns: 4, focused: true,
				branch: true, tune: prSceneWith(func(p *core.PRInfo) { p.Draft = draft }, 0),
			}
			a := s.build(t)
			d, _, _ := a.drawCtxFor(a.store.State())

			if got := slices.Contains(hintKeys(t, a), d.bind.hint("review.ready")); got != draft {
				t.Errorf("the ready hint shows = %v, want %v", got, draft)
			}
		})

		t.Run(fmt.Sprintf("list/draft=%v", draft), func(t *testing.T) {
			t.Parallel()

			a := listApp(t, true, 160, 40, listAgents())
			press(a, keyLeader, keyRune('g'), keyRune('l'))

			// #9 is the agents' draft, #8 an open pull request.
			number := map[bool]int{true: 9, false: 8}[draft]
			repo := map[bool]string{true: "github.com/acme/web", false: "github.com/acme/api"}[draft]
			a.prList.sel[a.prList.tab] = prListKey{repo: repo, number: number}

			hint := a.keys.prList.hint("prlist.ready") + " ready"
			if got := strings.Contains(ansi.Strip(a.View().Content), hint); got != draft {
				t.Errorf("the hint row holds %q = %v, want %v", hint, got, draft)
			}
		})
	}
}

// readyScene is the ready confirm over the PR drawer.
func readyScene(t *testing.T, icons bool, w, h int, title string) *App {
	t.Helper()

	s := reviewScene{
		theme: "catppuccin-mocha", icons: icons, w: w, h: h, res: turn4Result(), turn: 4, turns: 4, focused: true,
		branch: true, tune: func(t *testing.T, a *App, v *reviewView) {
			t.Helper()

			prSceneWith(func(p *core.PRInfo) { p.Draft = true }, 0)(t, a, v)

			a.confirm = &confirm{ready: &readyTarget{repo: prRepo, number: 7, id: "PR_7", title: title}}
		},
	}

	return s.build(t)
}

// TestReadyGolden draws the confirm once in colour; it compares highlighted
// colours, so it runs serially.
func TestReadyGolden(t *testing.T) {
	golden.RequireEqual(t, []byte(readyScene(t, true, 200, 52, "Show the pull request over its diff").View().Content))
}

// TestReadyText is the icons=false confirm without styling at 80x24, its title
// too long for the box.
func TestReadyText(t *testing.T) {
	t.Parallel()

	title := "A very long title that cannot fit in the box at all, not even at 80 columns of width"
	golden.RequireEqual(t, []byte(ansi.Strip(readyScene(t, false, 80, 24, title).View().Content)))
}
