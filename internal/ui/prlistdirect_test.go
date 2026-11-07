package ui

import (
	"errors"
	"fmt"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/x/ansi"
	"github.com/charmbracelet/x/exp/golden"

	"github.com/mhersson/aim/internal/core"
	"github.com/mhersson/aim/internal/github"
)

// listHead is the head the list shows for #7 in the direct rig.
const listHead = "5f3a9c1d2e4b6a7081920a1b2c3d4e5f60718293"

// directRig is the list rig with #7 polled at listHead, a drawer holding the
// keyboard on the first tab and another drawer open on the agent's tab, and the
// list open on the agent's row. The agent is on a tab that is not active.
func directRig(t *testing.T) *listRig {
	t.Helper()

	r := newListRig(t)

	pr := prSeven()
	pr.HeadOID = listHead
	r.run(r.step(prSnapshotMsg{Repo: prRepo, Info: github.RepoInfo{DefaultBranch: "main", Methods: allMethods}, PRs: []github.PR{pr}}))

	r.opened(t)
	r.run(r.act(core.SelectTab{Index: 1}))
	r.opened(t)
	r.run(r.act(core.SelectTab{Index: 0}))
	r.drawerTake()

	if !r.drawerHasKeys() || r.store.State().Tabs[1].Review == nil {
		t.Fatalf("rig: drawer keys %v, agent tab drawer %v", r.drawerHasKeys(), r.store.State().Tabs[1].Review)
	}

	r.leader('g')
	r.typed("l")

	if r.prList == nil {
		t.Fatalf("rig: no list; notice %q", r.notice)
	}

	return r
}

// placement is where the tabs, the focus and the drawers are.
type placement struct {
	active  int
	focuses []core.PaneID
	reviews []core.Review
	keys    bool
}

func (r *listRig) placed() placement {
	st := r.store.State()
	p := placement{active: st.Active, keys: r.drawer.focused}

	for _, tab := range st.Tabs {
		p.focuses = append(p.focuses, tab.Focus)
		p.reviews = append(p.reviews, *tab.Review)
	}

	return p
}

func TestPRListActsInPlace(t *testing.T) {
	t.Parallel()

	t.Run("M opens the merge box over the list", func(t *testing.T) {
		t.Parallel()

		r := directRig(t)
		before := r.placed()

		r.keys(keyRune('M'))

		if r.merge == nil {
			t.Fatalf("no merge box; notice %q", r.notice)
		}

		if r.merge.sha != listHead || r.merge.pr.Number != 7 || r.merge.repo != prRepo || !r.merge.listed {
			t.Errorf("box = sha %s, #%d of %s, listed %v; want the listed head of #7 of %s", r.merge.sha, r.merge.pr.Number, r.merge.repo, r.merge.listed, prRepo)
		}

		if r.prList == nil {
			t.Error("the list closed")
		}

		if after := r.placed(); !reflect.DeepEqual(after, before) {
			t.Errorf("placement = %+v, want %+v", after, before)
		}
	})

	t.Run("a opens the approve confirm over the list", func(t *testing.T) {
		t.Parallel()

		r := directRig(t)
		before := r.placed()

		r.keys(keyRune('a'))

		if r.confirm == nil || r.confirm.approve == nil {
			t.Fatalf("no approve confirm; notice %q", r.notice)
		}

		if a := r.confirm.approve; a.sha != listHead || a.number != 7 || a.repo != prRepo || a.title != "Add x" || !a.listed {
			t.Errorf("target = %+v, want #7 of %s at the listed head", *a, prRepo)
		}

		if r.prList == nil {
			t.Error("the list closed")
		}

		if after := r.placed(); !reflect.DeepEqual(after, before) {
			t.Errorf("placement = %+v, want %+v", after, before)
		}
	})
}

func TestPRListBoxesSendTheListedHead(t *testing.T) {
	t.Parallel()

	t.Run("merge", func(t *testing.T) {
		t.Parallel()

		r := directRig(t)
		r.typed("M")
		r.keys(keyEnter)

		feedUntil[mergedMsg](t, r.drawerRig)

		got := r.acts.merged()
		if len(got) != 1 || got[0].opts.SHA != listHead || got[0].pr.Number != 7 {
			t.Errorf("merge calls = %+v, want #7 at %s", got, listHead)
		}

		if r.prList == nil || r.merge != nil {
			t.Errorf("list %v, box %v; want the list open and the box gone", r.prList, r.merge)
		}
	})

	t.Run("approve", func(t *testing.T) {
		t.Parallel()

		r := directRig(t)
		r.typed("a")
		r.keys(keyEnter)
		r.until(t, "the approval", func() bool { return len(r.acts.approved()) == 1 })

		if got := r.acts.approved(); !slices.Equal(got, []approveCall{{prRepo, 7, listHead}}) {
			t.Errorf("approved %v, want #7 at %s", got, listHead)
		}

		if r.prList == nil || r.confirm != nil {
			t.Errorf("list %v, confirm %v; want the list open and the confirm gone", r.prList, r.confirm)
		}
	})

	t.Run("esc goes back to the list", func(t *testing.T) {
		t.Parallel()

		r := directRig(t)
		r.typed("M")
		r.keys(keyEsc)

		if r.merge != nil || r.prList == nil {
			t.Errorf("box %v, list %v; want the box gone and the list open", r.merge, r.prList)
		}

		r.typed("a")
		r.keys(keyEsc)

		if r.confirm != nil || r.prList == nil || len(r.acts.approved()) != 0 {
			t.Errorf("confirm %v, list %v, approved %v; want the confirm gone, the list open, nothing sent", r.confirm, r.prList, r.acts.approved())
		}
	})

	t.Run("a on your own pull request is refused", func(t *testing.T) {
		t.Parallel()

		r := directRig(t)
		pr := prSeven()
		pr.HeadOID, pr.ViewerAuthored = listHead, true
		r.run(r.step(prSnapshotMsg{Repo: prRepo, Info: github.RepoInfo{DefaultBranch: "main"}, PRs: []github.PR{pr}}))
		r.typed("a")

		if r.notice != noticeApproveOwn || r.confirm != nil {
			t.Errorf("notice %q, confirm %v; want the refusal", r.notice, r.confirm)
		}
	})
}

// holdRig is the copy rig with the list on the Requested tab, #88 selected and
// no poll of its repository yet.
func holdRig(t *testing.T) *copyRig {
	t.Helper()

	c := newCopyRig(t)
	c.selectRow(t, "github.com/acme/infra")

	return c
}

func (c *copyRig) watchOf(repo github.Repo) (github.Watch, bool) {
	for _, w := range c.rec.last() {
		if w.Repo == repo {
			return w, true
		}
	}

	return github.Watch{}, false
}

func TestPRListMergeHoldsForThePoll(t *testing.T) {
	t.Parallel()

	t.Run("opens from the row after the poll answers", func(t *testing.T) {
		t.Parallel()

		c := holdRig(t)
		c.keys(keyRune('M'))
		c.noBox(t)

		if want := fmt.Sprintf(noticePRFetching, 88); c.notice != want {
			t.Errorf("notice = %q, want %q", c.notice, want)
		}

		if w, ok := c.watchOf(copyRepo); !ok || w.Focused != 88 || !slices.Contains(w.Visible, 88) {
			t.Errorf("watch = %+v (%v), want #88 visible and focused", w, ok)
		}

		c.poll(88, copyPR88())

		m := c.merge
		if m == nil {
			t.Fatalf("no merge box after the poll; notice %q", c.notice)
		}

		if m.sha != "listed-head" || m.pr.HeadBranch != copyBranch || !slices.Equal(m.info.Methods, []github.MergeMethod{github.Squash}) || !m.listed {
			t.Errorf("box = sha %s, branch %q, methods %v, listed %v; want the row's head, the branch and [squash]", m.sha, m.pr.HeadBranch, m.info.Methods, m.listed)
		}

		if c.prList == nil || c.notice == fmt.Sprintf(noticePRFetching, 88) {
			t.Errorf("list %v, notice %q; want the list open and the wait's notice gone", c.prList, c.notice)
		}
	})

	t.Run("a failed poll says why and opens nothing", func(t *testing.T) {
		t.Parallel()

		c := holdRig(t)
		c.keys(keyRune('M'))
		c.deliver(github.Snapshot{Repo: copyRepo, Asked: 88, Err: errors.New("boom")})

		if want := fmt.Sprintf(noticePRsFailed, "github.com/acme/infra", "boom"); c.notice != want {
			t.Errorf("notice = %q, want %q", c.notice, want)
		}

		c.noBox(t)
		c.poll(88, copyPR88())
		c.noBox(t)
	})

	t.Run("a standing error does not stop the hold", func(t *testing.T) {
		t.Parallel()

		c := holdRig(t)
		c.deliver(github.Snapshot{Repo: copyRepo, Err: errors.New("boom")})
		c.notice = ""
		c.keys(keyRune('M'))

		if want := fmt.Sprintf(noticePRFetching, 88); c.notice != want {
			t.Errorf("notice = %q, want %q", c.notice, want)
		}

		c.poll(88, copyPR88())

		if c.merge == nil {
			t.Error("no merge box after the poll")
		}
	})

	t.Run("a poll that does not list it opens nothing", func(t *testing.T) {
		t.Parallel()

		c := holdRig(t)
		c.keys(keyRune('M'))
		c.poll(88)

		if want := fmt.Sprintf(noticePRGone, 88); c.notice != want {
			t.Errorf("notice = %q, want %q", c.notice, want)
		}

		c.noBox(t)
	})

	t.Run("an older ask leaves the hold", func(t *testing.T) {
		t.Parallel()

		c := holdRig(t)
		c.keys(keyRune('M'))
		c.poll(5)
		c.noBox(t)
		c.poll(88, copyPR88())

		if c.merge == nil {
			t.Error("no merge box after the right answer")
		}
	})

	t.Run("a closed list drops it and the notice", func(t *testing.T) {
		t.Parallel()

		c := holdRig(t)
		c.keys(keyRune('M'))
		c.keys(keyEsc)

		if c.notice == fmt.Sprintf(noticePRFetching, 88) {
			t.Errorf("notice = %q, want the wait's notice gone", c.notice)
		}

		c.poll(88, copyPR88())
		c.noBox(t)
	})

	t.Run("another selected row drops it", func(t *testing.T) {
		t.Parallel()

		c := holdRig(t)
		c.keys(keyRune('M'))
		c.keys(keyRune('j'))

		if c.notice == fmt.Sprintf(noticePRFetching, 88) {
			t.Errorf("notice = %q, want the wait's notice gone", c.notice)
		}

		c.poll(88, copyPR88())
		c.noBox(t)
	})

	t.Run("something open above the list at the answer drops it silently", func(t *testing.T) {
		t.Parallel()

		c := holdRig(t)
		c.keys(keyRune('M'))

		c.confirm = &confirm{}
		c.poll(88, copyPR88())

		if c.merge != nil || c.confirm.approve != nil {
			t.Error("a box opened over the modal")
		}

		if c.notice != "" {
			t.Errorf("notice = %q, want none", c.notice)
		}
	})

	t.Run("a known repository opens at once", func(t *testing.T) {
		t.Parallel()

		c := holdRig(t)
		c.poll(88, copyPR88())
		c.keys(keyRune('M'))

		if c.merge == nil || c.merge.sha != "listed-head" {
			t.Errorf("merge = %+v, want the box at the row's head", c.merge)
		}
	})

	t.Run("a opens at once without a poll", func(t *testing.T) {
		t.Parallel()

		c := holdRig(t)
		c.keys(keyRune('a'))

		if c.confirm == nil || c.confirm.approve == nil {
			t.Fatalf("no approve confirm; notice %q", c.notice)
		}

		if a := c.confirm.approve; a.sha != "listed-head" || a.number != 88 || a.title != "Bump" {
			t.Errorf("target = %+v, want #88 Bump at the row's head", *a)
		}
	})
}

func TestMergeMovedToast(t *testing.T) {
	t.Parallel()

	moved := fmt.Errorf("%w: sha mismatch", github.ErrHeadMoved)

	t.Run("from the list it also reads the list again", func(t *testing.T) {
		t.Parallel()

		r := directRig(t)
		r.acts.mergeFail = moved
		r.typed("M")
		r.keys(keyEnter)

		feedUntil[mergedMsg](t, r.drawerRig)

		if want := fmt.Sprintf(noticeMergeMoved, 7); r.notice != want || r.noticeLevel() != levelError {
			t.Errorf("toast = %q at level %v, want %q at the error level", r.notice, r.noticeLevel(), want)
		}

		if got := r.rec.refreshed(); !slices.Contains(got, prRepo) {
			t.Errorf("refreshed %v, want %v among them", got, prRepo)
		}
	})

	t.Run("from the drawer", func(t *testing.T) {
		t.Parallel()

		r, f := inMergeDrawer(t, everyMethod, nil)
		f.mergeFail = moved

		r.typed("M")
		r.keys(keyEnter)

		feedUntil[mergedMsg](t, r.drawerRig)

		if want := fmt.Sprintf(noticeMergeMoved, 7); r.notice != want || r.noticeLevel() != levelError {
			t.Errorf("toast = %q at level %v, want %q at the error level", r.notice, r.noticeLevel(), want)
		}
	})
}

func TestMergedRowLeavesASearchedTab(t *testing.T) {
	t.Parallel()

	c := holdRig(t)
	c.poll(88, copyPR88())

	rows := func() int { return len(c.prListRowsOf(c.store.State(), prTabRequested)) }
	if rows() != 2 {
		t.Fatalf("rows = %d before the merge, want 2", rows())
	}

	// GitHub no longer lists the merged pull request.
	c.reads.mu.Lock()
	c.reads.mine["github.com"] = github.MyPRs{Requested: c.reads.mine["github.com"].Requested[:1]}
	c.reads.mu.Unlock()

	c.keys(keyRune('M'))
	c.keys(keyEnter)
	feedUntil[mergedMsg](t, c.drawerRig)
	c.until(t, "the row to leave", func() bool { return rows() == 1 })

	if c.prList == nil {
		t.Error("the list closed")
	}
}

func TestCleanupAnswerOverTheList(t *testing.T) {
	t.Parallel()

	t.Run("the list lets the merge's answer through", func(t *testing.T) {
		t.Parallel()

		r := newCleanupRig(t)
		answer := r.check(r.prHead)

		r.prList = &prListView{}
		r.run(r.step(answer))

		if r.confirm == nil || r.confirm.cleanup == nil {
			t.Errorf("confirm = %+v, want the clean-up confirm over the list", r.confirm)
		}
	})

	t.Run("another modal above it drops the answer", func(t *testing.T) {
		t.Parallel()

		r := newCleanupRig(t)
		answer := r.check(r.prHead)

		r.prList = &prListView{}
		r.confirm = &confirm{}
		r.run(r.step(answer))

		if r.confirm.cleanup != nil {
			t.Error("the clean-up confirm replaced the modal")
		}
	})
}

// listedBoxScene is the list at the 'jj' row with the box of key open over it.
func listedBoxScene(t *testing.T, icons bool, w, h int, key rune) *App {
	t.Helper()

	agents := listAgents()
	for i := range agents {
		if agents[i].pr != nil {
			agents[i].pr.HeadOID, agents[i].pr.BaseBranch = listHead, "main"
		}
	}

	a := listApp(t, icons, w, h, agents)
	press(a, keyLeader, keyRune('g'), keyRune('l'))
	typedList(a, "jj")

	rows := a.prListRowsOf(a.store.State(), a.prList.tab)
	repo, _ := repoOfKey(rows[a.prList.selected(rows)].key.repo)
	a.prs.polled = map[github.Repo]bool{repo: true}

	press(a, keyRune(key))

	return a
}

func listedBoxScenes() []struct {
	name string
	key  rune
	w, h int
} {
	return []struct {
		name string
		key  rune
		w, h int
	}{{"merge-wide", 'M', 140, 44}, {"merge-80x24", 'M', 80, 24}, {"approve-wide", 'a', 140, 44}, {"approve-80x24", 'a', 80, 24}}
}

func TestPRListBoxGolden(t *testing.T) {
	t.Parallel()

	for _, c := range listedBoxScenes() {
		t.Run("catppuccin-mocha/icons=true/"+c.name, func(t *testing.T) {
			golden.RequireEqual(t, []byte(listedBoxScene(t, true, c.w, c.h, c.key).View().Content))
		})
	}
}

func TestPRListBoxText(t *testing.T) {
	t.Parallel()

	for _, c := range listedBoxScenes() {
		t.Run("icons=false/"+c.name, func(t *testing.T) {
			golden.RequireEqual(t, []byte(ansi.Strip(listedBoxScene(t, false, c.w, c.h, c.key).View().Content)))
		})
	}
}

// A toast shows over a list that fills the terminal: the list ends on the
// toast's top border row, so the text row stays clear.
func TestToastShowsOverAFullList(t *testing.T) {
	t.Parallel()

	const (
		w, h = 80, 20
		text = "merged #8 (squash)"
	)

	a := prListScene(t, true, w, h)
	a.toast(levelSuccess, text)

	lines := strings.Split(ansi.Strip(a.View().Content), "\n")

	if !strings.Contains(lines[h-3], "╰") {
		t.Fatalf("the list does not end on row %d:\n%s", h-3, strings.Join(lines, "\n"))
	}

	if !strings.Contains(lines[h-2], text) {
		t.Errorf("the toast's text row is hidden:\n%s", strings.Join(lines, "\n"))
	}
}

// forkRig is the clean-up rig with the list on the Requested tab, holding a
// fork's #99 on feat/x, a branch name a worktree here has, that the poll does
// not list.
func forkRig(t *testing.T) *cleanupRig {
	t.Helper()

	r := newCleanupRig(t)
	r.reads = &fakeReads{fail: map[string]error{}, mine: map[string]github.MyPRs{
		"github.com": {Requested: []github.SearchPR{
			searched(prRepo.String(), 99, "Fork work", "stranger", time.Hour, func(p *github.PR) {
				p.BaseBranch, p.HeadBranch, p.HeadOID, p.CrossRepo = "main", "feat/x", "forkhead", true
			}),
		}},
	}}

	focus := r.store.State().Tab().Focus
	k := prKey{cwd: r.wt, branch: "feat/x"}
	r.prs.panes[focus] = &prPane{key: k, answered: k, repo: prRepo, ok: true}

	r.leader('g')
	r.typed("l")
	r.typed("2")
	r.until(t, "the row", func() bool { return len(r.prListRowsOf(r.store.State(), prTabRequested)) == 1 })

	return r
}

func TestListedForkMergeKeepsItsRepository(t *testing.T) {
	t.Parallel()

	r := forkRig(t)
	r.keys(keyRune('M'))

	if r.merge == nil || !r.merge.pr.CrossRepo {
		t.Fatalf("box = %+v, notice %q; want the fork's box", r.merge, r.notice)
	}

	r.keys(keyEnter)
	m := feedUntil[mergedMsg](t, r.drawerRig)

	if cmd := r.cleanupAfterMerge(m); cmd != nil {
		t.Error("a clean-up check started for a pull request from another repository")
	}

	if rec := r.store.State().PRs[prRepoKey].Branch["feat/x"]; rec.Number != 99 || !rec.CrossRepo {
		t.Errorf("record of feat/x = %+v, want #99 marked as from another repository", rec)
	}
}
