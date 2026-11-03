package ui

import (
	"errors"
	"fmt"
	"slices"
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
	"github.com/charmbracelet/x/exp/golden"

	"github.com/mhersson/aim/internal/core"
	"github.com/mhersson/aim/internal/github"
	"github.com/mhersson/aim/internal/ui/theme"
)

var allMethods = []github.MergeMethod{github.MergeCommit, github.Squash, github.Rebase}

// inMergeDrawer opens the drawer in PR mode on pull request 7 of a repository
// with the given settings, with fake GitHub writes installed.
func inMergeDrawer(t *testing.T, info github.RepoInfo, tune func(*github.PR)) (*prDrawer, *fakeActions) {
	t.Helper()

	r := newPRDrawer(t)
	f := &fakeActions{}
	r.actions = f

	pr := prSeven()
	if tune != nil {
		tune(&pr)
	}

	if info.DefaultBranch == "" {
		info.DefaultBranch = "main"
	}

	r.run(r.step(prSnapshotMsg{Repo: prRepo, Info: info, PRs: []github.PR{pr}}))
	r.opened(t)
	r.typed("p")
	r.until(t, "PR mode", r.inPRMode(7))

	return r, f
}

var everyMethod = github.RepoInfo{Methods: allMethods, DeleteBranchOnMerge: true}

func TestDefaultMethod(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		allowed []string
		want    github.MergeMethod
	}{
		{"all allowed", []string{"merge", "squash", "rebase"}, github.MergeCommit},
		{"squash and rebase", []string{"squash", "rebase"}, github.Squash},
		{"rebase only", []string{"rebase"}, github.Rebase},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			if got := defaultMethod(tc.allowed); got != tc.want {
				t.Errorf("defaultMethod(%v) = %q, want %q", tc.allowed, got, tc.want)
			}
		})
	}
}

func TestMergeWarning(t *testing.T) {
	t.Parallel()

	checks := func(names ...string) *core.PRChecks {
		c := &core.PRChecks{Number: 7}
		for _, n := range names {
			c.Checks = append(c.Checks, core.CheckInfo{Name: n, State: "failure"})
		}

		return c
	}

	approved := core.PRInfo{ReviewDecision: "APPROVED", ChecksTotal: 4, ChecksDone: 4}
	failing := func(n int) core.PRInfo {
		pr := approved
		pr.ChecksFailed = n

		return pr
	}
	running := func(n int) core.PRInfo {
		pr := approved
		pr.ChecksDone -= n

		return pr
	}
	reviewed := func(decision string) core.PRInfo {
		pr := approved
		pr.ReviewDecision = decision

		return pr
	}

	// Each cause is a part of its own; none of them is a warning.
	t.Run("nothing wrong", func(t *testing.T) {
		t.Parallel()

		if got := mergeWarning(approved, nil, 0); got != "" {
			t.Errorf("mergeWarning = %q, want none", got)
		}
	})

	t.Run("the review part", func(t *testing.T) {
		t.Parallel()

		changes, none := mergeWarning(reviewed("CHANGES_REQUESTED"), nil, 0), mergeWarning(reviewed(""), nil, 0)
		if changes == "" || none == "" || changes == none {
			t.Errorf("changes requested %q and no approval %q, want two different warnings", changes, none)
		}
	})

	t.Run("failed checks are named, cut at two with the count left", func(t *testing.T) {
		t.Parallel()

		got := mergeWarning(failing(3), checks("alpha", "beta", "gamma"), 0)
		if !strings.Contains(got, "alpha") || !strings.Contains(got, "beta") || strings.Contains(got, "gamma") || !strings.Contains(got, "1") {
			t.Errorf("mergeWarning = %q, want alpha and beta and the 1 left out", got)
		}
	})

	t.Run("two failed checks are both named with no count", func(t *testing.T) {
		t.Parallel()

		got := mergeWarning(failing(2), checks("alpha", "beta"), 0)
		if !strings.Contains(got, "alpha") || !strings.Contains(got, "beta") || strings.Contains(got, "more") {
			t.Errorf("mergeWarning = %q, want alpha and beta and no count left out", got)
		}
	})

	t.Run("failed and running checks are counted", func(t *testing.T) {
		t.Parallel()

		failed, waiting := mergeWarning(failing(2), nil, 0), mergeWarning(running(2), nil, 0)
		if !strings.Contains(failed, "2") || !strings.Contains(waiting, "2") || failed == waiting {
			t.Errorf("failed %q and running %q, want two different warnings counting 2", failed, waiting)
		}
	})

	t.Run("causes combine, failed then running then review", func(t *testing.T) {
		t.Parallel()

		pr := core.PRInfo{ChecksTotal: 4, ChecksDone: 3, ChecksFailed: 1}
		want := strings.Join([]string{
			mergeWarning(failing(1), checks("lint"), 0),
			mergeWarning(running(1), nil, 0),
			mergeWarning(reviewed(""), nil, 0),
		}, ", ")

		if got := mergeWarning(pr, checks("lint"), 0); got != want {
			t.Errorf("mergeWarning = %q, want %q", got, want)
		}
	})

	t.Run("names give way to a count when the line is short", func(t *testing.T) {
		t.Parallel()

		long := checks("lint-everything", "test-everything")

		if got, want := mergeWarning(failing(2), long, 20), mergeWarning(failing(2), nil, 0); got != want {
			t.Errorf("a short line: %q, want the count %q", got, want)
		}

		if got := mergeWarning(failing(1), checks("lint"), 20); !strings.Contains(got, "lint") {
			t.Errorf("a name that fits: %q, want it named", got)
		}
	})
}

func TestMergeStateWord(t *testing.T) {
	t.Parallel()

	mocha, err := theme.ByName("catppuccin-mocha")
	if err != nil {
		t.Fatal(err)
	}

	ui := mocha.UI

	// Each state has its own non-empty word and tone; HAS_HOOKS reads as CLEAN,
	// an unknown state as UNKNOWN, and BEHIND and DIRTY name the base.
	tests := []struct {
		state string
		fg    theme.Hex
	}{
		{"CLEAN", ui.Success},
		{"UNSTABLE", ui.Warning},
		{"BLOCKED", ui.Warning},
		{"BEHIND", ui.Warning},
		{"DIRTY", ui.Error},
		{"DRAFT", ui.Muted},
		{"UNKNOWN", ui.Subtle},
	}

	seen := map[string]string{}

	for _, tc := range tests {
		got := mergeStateWord(tc.state, "main", ui)
		if got.text == "" || got.fg != tc.fg {
			t.Errorf("mergeStateWord(%q) = %q in %v, want a word in %v", tc.state, got.text, got.fg, tc.fg)
		}

		if other, dup := seen[got.text]; dup {
			t.Errorf("%s and %s read the same: %q", tc.state, other, got.text)
		}

		seen[got.text] = tc.state

		if want, has := tc.state == "BEHIND" || tc.state == "DIRTY", strings.Contains(got.text, "main"); has != want {
			t.Errorf("mergeStateWord(%q) = %q, names the base = %v, want %v", tc.state, got.text, has, want)
		}
	}

	if hooks, clean := mergeStateWord("HAS_HOOKS", "main", ui), mergeStateWord("CLEAN", "main", ui); hooks != clean {
		t.Errorf("HAS_HOOKS = %+v, want CLEAN's %+v", hooks, clean)
	}
}

func TestMergeAtTheShownHead(t *testing.T) {
	t.Parallel()

	r, f := inMergeDrawer(t, everyMethod, nil)
	r.typed("M")

	v := r.merge
	if v == nil {
		t.Fatalf("M opened no merge box; notice %q", r.notice)
	}

	if v.sha != r.prHead || v.pr.Number != 7 || v.repo != prRepo || v.method != github.MergeCommit || !v.del {
		t.Errorf("box = %+v, want #7 of %s at %s, merge, delete on", *v, prRepo, r.prHead)
	}

	r.typed("s")

	if v.method != github.Squash {
		t.Errorf("s chose %q, want squash", v.method)
	}

	if got := f.merged(); len(got) != 0 {
		t.Fatalf("merged %v before enter", got)
	}

	f.mu.Lock()
	f.mergeRes = github.Merged{SHA: "m1", BranchDeleted: true}
	f.mu.Unlock()

	r.keys(keyEnter)

	answer := feedUntil[mergedMsg](t, r.drawerRig)

	got := f.merged()[0]
	if got.repo != prRepo || got.pr.Number != 7 || got.pr.HeadBranch != "feat/x" || got.info.DefaultBranch != "main" ||
		got.opts != (github.MergeOptions{Method: github.Squash, SHA: r.prHead, DeleteBranch: true}) {
		t.Errorf("merge call = %+v, want #7 squashed at %s with the branch deleted", got, r.prHead)
	}

	if r.merge != nil {
		t.Error("the box is still open after enter")
	}

	if answer.err != nil || !answer.res.BranchDeleted || answer.method != github.Squash {
		t.Errorf("merge answer = %+v, want a squash that deleted the branch", answer)
	}

	if !strings.Contains(r.notice, prNumberText(7)) || !strings.Contains(r.notice, string(github.Squash)) || r.noticeLevel() != levelSuccess {
		t.Errorf("toast = %q at level %v, want one naming #7 and the squash method at the success level", r.notice, r.noticeLevel())
	}

	if got := r.rec.refreshed(); !slices.Equal(got, []github.Repo{prRepo}) {
		t.Errorf("refreshed %v, want %v", got, []github.Repo{prRepo})
	}
}

func TestMergeKeys(t *testing.T) {
	t.Parallel()

	t.Run("a forbidden method does nothing", func(t *testing.T) {
		t.Parallel()

		r, f := inMergeDrawer(t, github.RepoInfo{Methods: []github.MergeMethod{github.MergeCommit, github.Squash}}, nil)
		r.typed("M")
		r.typed("r")

		if r.merge == nil || r.merge.method != github.MergeCommit {
			t.Fatalf("after r the box = %+v, want it open on merge", r.merge)
		}

		r.keys(keyEnter)
		r.until(t, "the merge", func() bool { return len(f.merged()) == 1 })

		if got := f.merged()[0].opts.Method; got != github.MergeCommit {
			t.Errorf("merged by %q, want merge", got)
		}
	})

	t.Run("d toggles the branch delete", func(t *testing.T) {
		t.Parallel()

		r, f := inMergeDrawer(t, github.RepoInfo{Methods: allMethods}, nil)
		r.typed("M")

		if r.merge.del {
			t.Fatal("delete starts on though the repository does not delete head branches")
		}

		r.typed("d")

		if !r.merge.del {
			t.Fatal("d left delete off")
		}

		r.keys(keyEnter)
		r.until(t, "the merge", func() bool { return len(f.merged()) == 1 })

		if !f.merged()[0].opts.DeleteBranch {
			t.Error("merge sent no branch delete after d")
		}
	})

	t.Run("no delete row for the default branch", func(t *testing.T) {
		t.Parallel()

		info := everyMethod
		info.DefaultBranch = "feat/x"

		r, f := inMergeDrawer(t, info, nil)
		r.typed("M")
		r.typed("d")

		if r.merge.del || r.merge.deleteShown() {
			t.Fatalf("the default branch offers a delete: %+v", *r.merge)
		}

		r.keys(keyEnter)
		r.until(t, "the merge", func() bool { return len(f.merged()) == 1 })

		if f.merged()[0].opts.DeleteBranch {
			t.Error("merge sent a branch delete for the default branch")
		}
	})

	t.Run("no delete row for another repository's branch", func(t *testing.T) {
		t.Parallel()

		r, f := inMergeDrawer(t, everyMethod, nil)

		info, _ := prInfoByNumber(r.store.State(), prRepoKey, 7)
		info.CrossRepo = true

		openIn(t, r.App,
			core.SetRepoPRs{Repo: prRepoKey, DefaultBranch: "main", Methods: []string{"merge", "squash", "rebase"}},
			core.SetBranchPR{Repo: prRepoKey, Branch: "feat/x", PR: info, Found: true},
		)

		r.typed("M")

		if r.merge.del || r.merge.deleteShown() {
			t.Fatalf("a fork's pull request offers a delete: %+v", *r.merge)
		}

		r.keys(keyEnter)
		r.until(t, "the merge", func() bool { return len(f.merged()) == 1 })

		if f.merged()[0].opts.DeleteBranch {
			t.Error("merge sent a branch delete for a fork")
		}
	})

	t.Run("esc and other keys send nothing", func(t *testing.T) {
		t.Parallel()

		r, f := inMergeDrawer(t, everyMethod, nil)
		r.typed("M")
		r.typed("y")
		r.typed("a")

		if r.merge == nil || r.confirm != nil {
			t.Fatalf("other keys changed the modal: box %v, confirm %v", r.merge, r.confirm)
		}

		r.keys(keyEsc)

		if r.merge != nil {
			t.Error("esc left the box open")
		}

		r.settle()

		if got := f.merged(); len(got) != 0 {
			t.Errorf("merged %v, want nothing", got)
		}
	})
}

func TestMergeFromLeaderM(t *testing.T) {
	t.Parallel()

	r := newPRDrawer(t)
	f := &fakeActions{}
	r.actions = f

	r.poll()
	r.leader('g')
	r.typed("m")

	if r.merge != nil {
		t.Fatal("leader g m opened the box before the pull request loaded")
	}

	r.until(t, "the box", func() bool { return r.merge != nil })

	if rv := r.review(); rv == nil || rv.PR == nil || rv.PR.Number != 7 {
		t.Errorf("review = %+v, want the drawer in PR mode on #7", rv)
	}

	if r.merge.sha != r.prHead {
		t.Errorf("box head = %s, want the loaded head %s", r.merge.sha, r.prHead)
	}

	if got := f.merged(); len(got) != 0 {
		t.Errorf("merged %v before enter", got)
	}
}

func TestMergeWithoutActions(t *testing.T) {
	t.Parallel()

	r, _ := inMergeDrawer(t, everyMethod, nil)
	r.actions = nil

	r.typed("M")

	if r.merge != nil || r.notice != noticePRsOff {
		t.Errorf("box %v, notice %q, want no box and %q", r.merge, r.notice, noticePRsOff)
	}

	r.leader('h')
	r.leader('g')
	r.typed("m")

	if r.merge != nil || r.notice != noticePRsOff {
		t.Errorf("leader g m: box %v, notice %q, want no box and %q", r.merge, r.notice, noticePRsOff)
	}
}

func TestMergeWithoutAFocusedPane(t *testing.T) {
	t.Parallel()

	r := newPRDrawer(t)
	r.actions = &fakeActions{}

	st := r.store.State()
	openIn(t, r.App, core.ClosePane{ID: st.Tab().Focus})

	if st = r.store.State(); len(st.Tab().Panes) != 0 {
		t.Fatalf("the tab still has panes: %v", st.Tab().Panes)
	}

	r.leader('g')
	r.typed("m")

	if r.merge != nil || r.notice != noticeMergeNoPane {
		t.Errorf("box %v, notice %q, want no box and %q", r.merge, r.notice, noticeMergeNoPane)
	}
}

func TestMergeNotOpen(t *testing.T) {
	t.Parallel()

	for _, state := range []string{"MERGED", "CLOSED"} {
		t.Run(state, func(t *testing.T) {
			t.Parallel()

			r, _ := inMergeDrawer(t, everyMethod, nil)

			info, _ := prInfoByNumber(r.store.State(), prRepoKey, 7)
			info.State = state

			openIn(t, r.App,
				core.SetRepoPRs{Repo: prRepoKey, DefaultBranch: "main"},
				core.SetBranchPR{Repo: prRepoKey, Branch: "feat/x", PR: info, Found: true},
			)

			r.typed("M")

			if want := fmt.Sprintf(noticeMergeClosed, 7, strings.ToLower(state)); r.merge != nil || r.notice != want {
				t.Errorf("box %v, notice %q, want no box and %q", r.merge, r.notice, want)
			}
		})
	}
}

func TestMergeErrors(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		fail  error
		res   github.Merged
		is    error    // what the answer's error is, by errors.Is
		has   []string // what the toast carries
		lacks []string // what tells it from the generic failure toast
		level toastLevel
	}{
		{
			"head moved", fmt.Errorf("%w: sha mismatch", github.ErrHeadMoved),
			github.Merged{},
			github.ErrHeadMoved,
			[]string{prNumberText(7)},
			[]string{"sha mismatch", github.ErrHeadMoved.Error()},
			levelError,
		},
		{
			"not mergeable", fmt.Errorf("%w: base branch policy", github.ErrNotMergeable),
			github.Merged{},
			github.ErrNotMergeable,
			[]string{prNumberText(7), github.ErrNotMergeable.Error(), "base branch policy"},
			nil, levelError,
		},
		{
			"delete failed", nil,
			github.Merged{SHA: "m1", DeleteErr: errors.New("protected branch")},
			nil,
			[]string{prNumberText(7), string(github.MergeCommit), "feat/x", "protected branch"},
			nil, levelSuccess,
		},
		{"plain success", nil, github.Merged{SHA: "m1"}, nil, []string{prNumberText(7), string(github.MergeCommit)}, []string{"protected branch"}, levelSuccess},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			r, f := inMergeDrawer(t, everyMethod, nil)
			f.mergeFail, f.mergeRes = tc.fail, tc.res

			r.typed("M")
			r.keys(keyEnter)

			m := feedUntil[mergedMsg](t, r.drawerRig)

			if tc.is != nil && !errors.Is(m.err, tc.is) || tc.is == nil && m.err != nil {
				t.Errorf("answer error = %v, want %v", m.err, tc.is)
			}

			for _, w := range tc.has {
				if !strings.Contains(r.notice, w) {
					t.Errorf("toast = %q, want it to carry %q", r.notice, w)
				}
			}

			for _, w := range tc.lacks {
				if strings.Contains(r.notice, w) {
					t.Errorf("toast = %q, want it without %q", r.notice, w)
				}
			}

			if r.noticeLevel() != tc.level {
				t.Errorf("toast = %q at level %v, want level %v", r.notice, r.noticeLevel(), tc.level)
			}

			refreshed := len(r.rec.refreshed()) > 0
			if refreshed != (tc.fail == nil) {
				t.Errorf("refreshed = %v after %q", refreshed, tc.name)
			}
		})
	}
}

func TestMergeOnce(t *testing.T) {
	t.Parallel()

	r, f := inMergeDrawer(t, everyMethod, nil)
	f.hold = true

	r.typed("M")
	r.keys(keyEnter)
	r.until(t, "the call", func() bool { return len(f.merged()) == 1 })

	r.typed("M")

	if r.merge != nil || r.notice != fmt.Sprintf(noticeMergeBusy, 7) {
		t.Errorf("box %v, notice %q, want no box and the busy notice", r.merge, r.notice)
	}

	r.leader('g')
	r.typed("m")

	if r.merge != nil || r.notice != fmt.Sprintf(noticeMergeBusy, 7) {
		t.Errorf("leader g m: box %v, notice %q, want no box and the busy notice", r.merge, r.notice)
	}

	r.cancel()
	r.settle()
	r.until(t, "the cancelled merge to answer", func() bool { return len(r.merging) == 0 })
	r.settled(t)

	if got := len(f.merged()); got != 1 {
		t.Errorf("merge called %d times, want once", got)
	}

	if r.notice != "" && strings.HasPrefix(r.notice, "merge") {
		t.Errorf("notice = %q after the app closed, want none", r.notice)
	}
}

func TestMergeTargetIsFrozen(t *testing.T) {
	t.Parallel()

	r, f := inMergeDrawer(t, everyMethod, nil)
	r.typed("M")

	moved := prSeven()
	moved.HeadOID = strings.Repeat("e", 40)
	moved.HeadBranch = "other"

	r.run(r.step(prSnapshotMsg{
		Repo: prRepo, Info: github.RepoInfo{DefaultBranch: "main", Methods: []github.MergeMethod{github.Rebase}}, PRs: []github.PR{moved},
	}))

	r.keys(keyEnter)
	r.until(t, "the merge", func() bool { return len(f.merged()) == 1 })

	got := f.merged()[0]
	if got.opts.SHA != r.prHead || got.opts.Method != github.MergeCommit || got.pr.HeadBranch != "feat/x" {
		t.Errorf("merge call = %+v, want the frozen head %s, merge and feat/x", got, r.prHead)
	}
}

// mergeScenes are the box over the PR drawer, with a warning and with none, at
// the usual size and at 80x24.
func mergeScenes() []struct {
	name string
	w, h int
	warn bool
} {
	return []struct {
		name string
		w, h int
		warn bool
	}{
		{"warning-wide", 200, 52, true},
		{"clean-wide", 200, 52, false},
		{"warning-80x24", 80, 24, true},
		{"clean-80x24", 80, 24, false},
	}
}

func mergeScene(t *testing.T, icons bool, w, h int, warn bool) *App {
	t.Helper()

	s := reviewScene{
		theme: "catppuccin-mocha", icons: icons, w: w, h: h, res: turn4Result(), turn: 4, turns: 4, focused: true,
		branch: true, tune: func(t *testing.T, a *App, v *reviewView) {
			t.Helper()

			prSceneWith(func(info *core.PRInfo) {
				info.MergeState = "CLEAN"

				if warn {
					info.ChecksTotal, info.ChecksDone, info.ChecksFailed = 4, 3, 1
					info.ReviewDecision, info.MergeState = "", "BLOCKED"
				}
			}, 0)(t, a, v)

			rp := a.store.State().PRs[prRepoKey]
			pr, _ := prInfoByNumber(a.store.State(), prRepoKey, 7)
			pr.HeadBranch = "feat/review-pane"

			a.merge = &mergeView{
				repo: prRepo, pr: pr, sha: prHeadOID, method: github.Squash, del: true,
				info: github.RepoInfo{DefaultBranch: rp.DefaultBranch, Methods: allMethods, DeleteBranchOnMerge: true},
			}
		},
	}

	return s.build(t)
}

func TestMergeRemappedKeys(t *testing.T) {
	t.Parallel()

	r, f := inMergeDrawer(t, everyMethod, nil)
	r.typed("M")
	remapTables(t, r.App, contextKeys("merge", "1", "merge.method.squash", "s", "none", "q", "merge.cancel"))

	r.typed("1")

	if r.merge.method != github.Squash {
		t.Fatalf("1 chose %q, want squash", r.merge.method)
	}

	r.typed("m")
	r.typed("s")

	if r.merge.method != github.MergeCommit {
		t.Errorf("s moved the method to %q", r.merge.method)
	}

	got := ansi.Strip(r.View().Content)
	if !strings.Contains(got, "1/m/r method") {
		t.Errorf("the hints do not show 1/m/r:\n%s", got)
	}

	if !strings.Contains(got, " 1 squash ") || strings.Contains(got, " s squash ") {
		t.Errorf("the chips do not show 1 for squash:\n%s", got)
	}

	r.typed("q")

	if r.merge != nil || len(f.merged()) != 0 {
		t.Fatalf("q left the box %+v open or merged %v", r.merge, f.merged())
	}

	r.typed("M")
	r.keys(keyEsc)

	if r.merge != nil {
		t.Error("esc did not close the reopened box")
	}
}

// The chips and the reminder name the keys as bound.
func TestMergeBoxNamesKeysAsBound(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		pairs []string
		has   []string
		not   []string
	}{
		{
			"merge.run moved",
			[]string{"ctrl+m", "merge.run", "enter", "none"},
			[]string{"ctrl+m still merges if GitHub allows it"},
			[]string{"enter still merges"},
		},
		{
			"merge.run unbound",
			[]string{"enter", "none"},
			nil,
			[]string{"still merges"},
		},
		{
			"method unbound",
			[]string{"s", "none"},
			[]string{" squash ", " m merge "},
			[]string{" s squash "},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			r, _ := inMergeDrawer(t, everyMethod, nil)
			r.typed("M")
			remapTables(t, r.App, contextKeys("merge", tc.pairs...))

			got := ansi.Strip(r.View().Content)
			for _, w := range tc.has {
				if !strings.Contains(got, w) {
					t.Errorf("the box lacks %q:\n%s", w, got)
				}
			}

			for _, n := range tc.not {
				if strings.Contains(got, n) {
					t.Errorf("the box holds %q:\n%s", n, got)
				}
			}
		})
	}
}

// TestMergeGolden draws the box once in colour; it compares highlighted
// colours, so it runs serially.
func TestMergeGolden(t *testing.T) {
	for _, c := range mergeScenes() {
		t.Run("catppuccin-mocha/icons=true/"+c.name, func(t *testing.T) {
			golden.RequireEqual(t, []byte(mergeScene(t, true, c.w, c.h, c.warn).View().Content))
		})
	}
}

// TestMergeText is the icons=false screens without styling; the colour golden
// of the icons=true ones holds the same text.
func TestMergeText(t *testing.T) {
	t.Parallel()

	for _, c := range mergeScenes() {
		t.Run("icons=false/"+c.name, func(t *testing.T) {
			t.Parallel()

			got := ansi.Strip(mergeScene(t, false, c.w, c.h, c.warn).View().Content)
			golden.RequireEqual(t, []byte(got))
		})
	}
}

// A box taller than the terminal gives up its blank rows and then its hint row
// whole, and keeps its frame and its delete row.
func TestMergeBoxFitsAShortTerminal(t *testing.T) {
	t.Parallel()

	a := mergeScene(t, true, 100, 18, true)
	got := ansi.Strip(a.View().Content)
	lines := strings.Split(got, "\n")

	// The delete row names the branch with its box checked.
	del := slices.IndexFunc(lines, func(l string) bool { return strings.Contains(l, "feat/review-pane") && strings.Contains(l, "[x]") })
	if del < 0 {
		t.Fatalf("the box lost the delete row:\n%s", got)
	}

	if reminder := mergeReminder(a.keys.merge); reminder == "" || !strings.Contains(got, reminder) {
		t.Errorf("the box lost the reminder %q:\n%s", reminder, got)
	}

	if hint := a.keys.merge.hint("merge.cancel") + " cancel"; strings.Contains(got, hint) {
		t.Errorf("a hint row is left (%q):\n%s", hint, got)
	}

	// The box's bottom border is the one under the delete row.
	if del+1 >= len(lines) || !strings.Contains(lines[del+1], "╯") {
		t.Errorf("the row under the delete row is not the box's bottom border:\n%s", got)
	}
}

func TestMergedPRShowsMergedInTheDrawer(t *testing.T) {
	t.Parallel()

	r, f := inMergeDrawer(t, everyMethod, func(pr *github.PR) {
		pr.Checks = github.Checks{Total: 5, Done: 5}
		pr.ReviewDecision = "APPROVED"
	})

	header := func() string { return strings.Join(prHeaderLines(t, r.App)[:3], "\n") }

	if h := header(); !strings.Contains(h, "5/5") {
		t.Fatalf("header before the merge has no checks:\n%s", h)
	}

	r.typed("M")
	r.keys(keyEnter)
	r.until(t, "the merge", func() bool { return len(f.merged()) == 1 })
	r.until(t, "the merged state", func() bool {
		info, _ := prInfoByNumber(r.store.State(), prRepoKey, 7)

		return info.State == prMergedState
	})

	check := func(when string) {
		t.Helper()

		h := header()
		if !strings.Contains(h, "merged") || strings.Contains(h, "5/5") || strings.Contains(h, "approved") {
			t.Errorf("%s: header\n%s\nwant merged and no checks or review words", when, h)
		}

		for _, key := range []string{"a", "M"} {
			r.notice = ""
			r.typed(key)

			if want := fmt.Sprintf(noticeMergeClosed, 7, "merged"); r.merge != nil || r.notice != want {
				t.Errorf("%s: %s gave box %v, notice %q, want no box and %q", when, key, r.merge, r.notice, want)
			}
		}

		if rows := r.prListRows(r.store.State()); len(rows) != 0 {
			t.Errorf("%s: the PR list still holds %d rows", when, len(rows))
		}
	}

	check("before a poll")

	r.run(r.step(snapshotOf()))
	check("after a poll that drops it")
}

// The delete row keeps its hint only whole: when it does not fit beside the
// checkbox text, the row ends after "after merging".
func TestMergeDeleteHintDropsWhole(t *testing.T) {
	t.Parallel()

	const hint = "(the repository deletes head branches)"

	tests := []struct {
		name   string
		branch string
		w      int
		want   bool
	}{
		{"a short branch in a wide box shows it", "fix/a", 200, true},
		{"a long branch drops it", "feat/review-pane-with-a-long-name", 200, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			a := mergeScene(t, true, tt.w, 40, false)
			a.merge.pr.HeadBranch = tt.branch

			var row string

			for l := range strings.SplitSeq(ansi.Strip(a.View().Content), "\n") {
				if strings.Contains(l, "after merging") || strings.Contains(l, "(the") {
					row = l
				}
			}

			if row == "" {
				t.Fatal("no delete row")
			}

			if got := strings.Contains(row, hint); got != tt.want {
				t.Errorf("hint shown = %v, want %v:\n%s", got, tt.want, row)
			}

			if !tt.want && (strings.Contains(row, "(the") || strings.Contains(row, "…")) {
				t.Errorf("a partial hint or cut is left:\n%s", row)
			}
		})
	}
}

func TestPRInfoByNumberMergedRecord(t *testing.T) {
	t.Parallel()

	const repo = "github.com/o/r"

	merged := core.PRInfo{Number: 3, State: prMergedState, HeadBranch: "feat"}
	store := core.NewStore(core.InitialState(core.DefaultConfig()))

	for _, act := range []core.Action{
		core.SetRepoPRs{Repo: repo, Open: []core.PRInfo{
			{Number: 3, State: prOpenState, HeadBranch: "feat"},
			{Number: 9, State: prOpenState, HeadBranch: "feat"},
		}},
		core.SetBranchPR{Repo: repo, Branch: "feat", PR: merged, Found: true},
	} {
		if err := store.Dispatch(act); err != nil {
			t.Fatal(err)
		}
	}

	st := store.State()

	for number, want := range map[int]string{3: prMergedState, 9: prOpenState} {
		info, ok := prInfoByNumber(st, repo, number)
		if !ok || info.Number != number || info.State != want {
			t.Errorf("#%d = %+v, %v, want state %s", number, info, ok, want)
		}
	}
}
