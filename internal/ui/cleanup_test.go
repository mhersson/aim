package ui

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/charmbracelet/x/exp/golden"

	"github.com/mhersson/aim/internal/core"
	"github.com/mhersson/aim/internal/git"
	"github.com/mhersson/aim/internal/github"
)

const mergedToast = "merged #7 (merge)"

// cleanupRig is the PR drawer on .worktrees/feat-x, a linked worktree of a
// repository whose main checkout is on main. The drawer's pane is an idle agent
// started in the main checkout whose hooks report the worktree, and a shell works
// in the main checkout; neither keeps the worktree.
type cleanupRig struct {
	*prDrawer
	f        *fakeActions
	main     string
	wt       string
	agentID  core.PaneID       // the drawer's agent
	other    core.PaneID       // the shell in the main checkout
	ids      []core.PaneID     // every pane the rig opened, in tab order
	keepers  []core.PaneID     // the panes started in the worktree, in tab order
	turnKeys map[string]string // seedTurns: the turn key of each root
}

// newCleanupRig builds the rig; before opens more panes ahead of the drawer.
func newCleanupRig(t *testing.T, before ...func(*testing.T, *cleanupRig)) *cleanupRig {
	t.Helper()

	main := realPRFeatureRepo(t)
	wt := filepath.Join(main, ".worktrees", "feat-x")

	// The branch is made inside the worktree, so the main checkout stays on main.
	gitIn(t, "git", main, "worktree", "add", "-q", "--detach", wt)

	wt = rootOf(t, wt)
	r := &cleanupRig{prDrawer: newRealPRDrawerOn(t, wt), main: main, wt: wt, f: &fakeActions{}}
	r.actions = r.f

	// The branch sits at the pull request's head, as it does once the work is pushed and merged.
	gitIn(t, "git", wt, "reset", "-q", "--hard", r.prHead)

	// The app's first pane opens in the worktree; it moves to the main checkout.
	r.other = r.store.State().Tab().Focus
	openIn(t, r.App, core.SetPaneCwd{ID: r.other, Cwd: main})

	r.agentID = r.agent(t, main, "")
	openIn(t, r.App, core.SetAgentCwd{ID: r.agentID, Cwd: wt}, core.SetAgentState{ID: r.agentID, State: core.AgentIdle})
	r.ids = []core.PaneID{r.other, r.agentID}

	for _, f := range before {
		f(t, r)
	}

	openIn(t, r.App, core.FocusPane{ID: r.agentID})

	info := github.RepoInfo{DefaultBranch: "main", Methods: allMethods, DeleteBranchOnMerge: false}
	r.run(r.step(prSnapshotMsg{Repo: prRepo, Info: info, PRs: []github.PR{prSeven()}}))
	r.opened(t)
	r.typed("p")
	r.until(t, "PR mode", r.inPRMode(7))

	return r
}

// keeper opens a pane started in the worktree: a shell, or an idle agent whose
// hooks report the worktree.
func (r *cleanupRig) keeper(t *testing.T, kind core.PaneKind) core.PaneID {
	t.Helper()

	var id core.PaneID

	switch kind {
	case core.KindAgent:
		id = r.agent(t, r.wt, "")
		openIn(t, r.App, core.SetAgentCwd{ID: id, Cwd: r.wt}, core.SetAgentState{ID: id, State: core.AgentIdle})
	default:
		id = r.shell(t, r.wt)
	}

	r.ids = append(r.ids, id)
	r.keepers = append(r.keepers, id)

	return id
}

// named gives agent id a session name, so the cards tell agents apart.
func (r *cleanupRig) named(t *testing.T, id core.PaneID, name string) {
	t.Helper()

	transcript := filepath.Join(t.TempDir(), name+".jsonl")
	openIn(t, r.App, core.SetAgentSession{ID: id, Session: core.SessionID(name), Transcript: transcript})

	rp := r.railEntry(id)
	rp.name.text, rp.name.transcript = name, transcript
}

// started opens a pane started in the worktree for each of kinds, in order.
func started(kinds ...core.PaneKind) func(*testing.T, *cleanupRig) {
	return func(t *testing.T, r *cleanupRig) {
		t.Helper()

		for _, k := range kinds {
			r.keeper(t, k)
		}
	}
}

// mergeToBox presses M and enter and waits for the cleanup box.
func (r *cleanupRig) mergeToBox(t *testing.T) {
	t.Helper()

	r.typed("M")
	r.keys(keyEnter)
	r.until(t, "the cleanup box", func() bool { return r.confirm != nil && r.confirm.cleanup != nil })
}

// check runs the cleanup check for pull request 7 as a merge leaves it and
// returns its answer without feeding it to the app.
func (r *cleanupRig) check(head string) cleanupFoundMsg {
	pr, _ := prInfoByNumber(r.store.State(), prRepoKey, 7)
	pr.State = prMergedState

	r.toast(levelSuccess, mergedToast)

	return r.cleanupCheck(fromMerge, prRepo, pr, r.root, head)().(cleanupFoundMsg)
}

// offer is check followed by feeding the answer to the app.
func (r *cleanupRig) offer(head string) {
	r.run(r.step(r.check(head)))
}

func (r *cleanupRig) name() string { return r.nameOf(r.agentID) }

func (r *cleanupRig) nameOf(id core.PaneID) string { return r.cardName(r.store.State(), id) }

func (r *cleanupRig) text() string { return ansi.Strip(r.View().Content) }

func (r *cleanupRig) branchExists(t *testing.T) bool {
	t.Helper()

	return gitRead(t, "git", r.main, "branch", "--list", "feat/x") != ""
}

func (r *cleanupRig) worktreeExists() bool {
	_, err := os.Stat(r.wt)

	return err == nil
}

// done waits for the toast a run ends with.
func (r *cleanupRig) done(t *testing.T) {
	t.Helper()

	r.until(t, "the answer", func() bool { return r.notice != "" })
}

// kept is the notice for panes that keep the worktree.
func kept(panes string) string { return "kept .worktrees/feat-x: " + panes }

func TestCleanupOffer(t *testing.T) {
	t.Parallel()

	r := newCleanupRig(t)
	r.mergeToBox(t)

	if r.notice != mergedToast {
		t.Errorf("toast = %q, want %q", r.notice, mergedToast)
	}

	sha := r.prHead[:7]
	got := r.text()

	for _, want := range []string{
		" " + iconCleanup + " Clean up after #7 ",
		"#7 Add x",
		"is merged at " + sha + ". Clean up its worktree?",
		"[x] remove the worktree  .worktrees/feat-x",
		"    clean ",
		"[x] delete the local branch  feat/x",
		"at " + sha + ", the merged head",
		"enter clean up",
		"w worktree",
		"b branch",
		"esc keep",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("the box lacks %q:\n%s", want, got)
		}
	}
}

func TestCleanupRun(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		keys    []string
		toast   string
		level   toastLevel
		removed bool
		branch  bool
	}{
		{"both", nil, "removed .worktrees/feat-x; deleted feat/x", levelSuccess, true, false},
		{"branch off", []string{"b"}, "removed .worktrees/feat-x", levelSuccess, true, true},
		{"worktree off", []string{"w"}, "nothing to clean up", levelNotice, false, true},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			r := newCleanupRig(t)
			r.mergeToBox(t)

			r.typed(strings.Join(tc.keys, ""))
			r.notice = ""
			r.keys(keyEnter)
			r.done(t)

			if r.notice != tc.toast || r.noticeLevel() != tc.level {
				t.Errorf("toast = %q at level %v, want %q at level %v", r.notice, r.noticeLevel(), tc.toast, tc.level)
			}

			if r.confirm != nil {
				t.Error("the box is still open")
			}

			r.assertEnd(t, tc.removed, tc.branch)
		})
	}

	t.Run("esc keeps everything", func(t *testing.T) {
		t.Parallel()

		r := newCleanupRig(t)
		r.mergeToBox(t)
		r.keys(keyEsc)

		if r.confirm != nil {
			t.Error("esc left the box open")
		}

		r.assertEnd(t, false, true)
	})

	t.Run("worktree off reads the branch as unavailable", func(t *testing.T) {
		t.Parallel()

		r := newCleanupRig(t)
		r.mergeToBox(t)
		r.typed("w")

		got := r.text()
		for _, want := range []string{"[ ] remove the worktree", "[ ] delete the local branch", "needs the worktree removed"} {
			if !strings.Contains(got, want) {
				t.Errorf("with w off the box lacks %q:\n%s", want, got)
			}
		}

		before := r.confirm.cleanup.del
		r.typed("b")

		if r.confirm.cleanup.del != before || r.confirm.cleanup.remove {
			t.Errorf("b changed the target with w off: %+v", *r.confirm.cleanup)
		}
	})

	t.Run("other keys are dropped", func(t *testing.T) {
		t.Parallel()

		r := newCleanupRig(t)
		r.mergeToBox(t)

		r.typed("yn q")

		if r.confirm == nil || r.confirm.cleanup == nil || !r.confirm.cleanup.remove || !r.confirm.cleanup.del {
			t.Errorf("a stray key changed the box: %+v", r.confirm)
		}
	})
}

// A shell in the worktree, or an agent started there, keeps it; an agent that
// only cd'd in does not. No pane closes either way.
func TestCleanupInUse(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		before func(*testing.T, *cleanupRig)
		kept   func(r *cleanupRig) string // the panes the row under the worktree's names; nil when it can go
	}{
		{name: "only an agent that cd'd in"},
		{
			name: "an agent elsewhere",
			before: func(t *testing.T, r *cleanupRig) {
				t.Helper()
				r.ids = append(r.ids, r.agent(t, r.main, ""))
			},
		},
		{
			name:   "a shell",
			before: started(core.KindShell),
			kept:   func(*cleanupRig) string { return "1 shell is in it" },
		},
		{
			name:   "two shells",
			before: started(core.KindShell, core.KindShell),
			kept:   func(*cleanupRig) string { return "2 shells are in it" },
		},
		{
			name:   "an agent started in it",
			before: started(core.KindAgent),
			kept:   func(r *cleanupRig) string { return r.nameOf(r.keepers[0]) + " is in it" },
		},
		{
			name: "an agent started in it that cd'd out",
			before: func(t *testing.T, r *cleanupRig) {
				t.Helper()
				openIn(t, r.App, core.SetAgentCwd{ID: r.keeper(t, core.KindAgent), Cwd: r.main})
			},
			kept: func(r *cleanupRig) string { return r.nameOf(r.keepers[0]) + " is in it" },
		},
		{
			name:   "a shell and an agent",
			before: started(core.KindShell, core.KindAgent),
			kept:   func(r *cleanupRig) string { return r.nameOf(r.keepers[1]) + " and 1 shell are in it" },
		},
		{
			name: "two agents and a shell",
			before: func(t *testing.T, r *cleanupRig) {
				t.Helper()
				started(core.KindAgent, core.KindShell, core.KindAgent)(t, r)
				r.named(t, r.keepers[0], "worker-a")
				r.named(t, r.keepers[2], "worker-b")
			},
			kept: func(*cleanupRig) string { return "worker-a, worker-b and 1 shell are in it" },
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			var before []func(*testing.T, *cleanupRig)
			if tc.before != nil {
				before = append(before, tc.before)
			}

			r := newCleanupRig(t, before...)
			r.mergeToBox(t)

			got := r.text()

			if tc.kept == nil {
				for _, want := range []string{"[x] remove the worktree  .worktrees/feat-x", "[x] delete the local branch  feat/x", "enter clean up"} {
					if !strings.Contains(got, want) {
						t.Errorf("the box lacks %q:\n%s", want, got)
					}
				}

				r.notice = ""
				r.keys(keyEnter)
				r.done(t)

				if want := "removed .worktrees/feat-x; deleted feat/x"; r.notice != want || r.noticeLevel() != levelSuccess {
					t.Errorf("toast = %q at level %v, want %q as a success", r.notice, r.noticeLevel(), want)
				}

				r.assertEnd(t, true, false)

				return
			}

			for _, want := range []string{
				"[-] remove the worktree  .worktrees/feat-x",
				"    " + tc.kept(r),
				"[-] delete the local branch  feat/x",
				"needs the worktree removed",
				"esc keep",
			} {
				if !strings.Contains(got, want) {
					t.Errorf("the box lacks %q:\n%s", want, got)
				}
			}

			for _, hint := range []string{"enter clean up", "w worktree", "b branch"} {
				if strings.Contains(got, hint) {
					t.Errorf("a kept worktree's box offers %q:\n%s", hint, got)
				}
			}

			r.typed("wb")

			if c := r.confirm; c == nil || c.cleanup == nil || c.cleanup.remove || c.cleanup.del {
				t.Fatalf("w or b changed a kept worktree's box: %+v", c)
			}

			r.notice = ""
			r.keys(keyEnter)
			r.done(t)

			if want := noticeNothingToClean; r.notice != want || r.noticeLevel() != levelNotice {
				t.Errorf("after enter: %q at level %v, want %q as a notice", r.notice, r.noticeLevel(), want)
			}

			if r.confirm != nil {
				t.Error("enter left the box open")
			}

			r.assertEnd(t, false, true)
		})
	}
}

func TestCleanupGuards(t *testing.T) {
	t.Parallel()

	const unknownHead = "1234567890abcdef1234567890abcdef12345678"

	moveOut := func(t *testing.T, r *cleanupRig) {
		t.Helper()
		gitIn(t, "git", r.wt, "switch", "-q", "--detach")
	}

	commit := func(t *testing.T, r *cleanupRig, n int) {
		t.Helper()

		for i := range n {
			gitIn(t, "git", r.wt, "commit", "-q", "--allow-empty", "-m", fmt.Sprintf("extra %d", i))
		}
	}

	working := func(t *testing.T, r *cleanupRig, id core.PaneID) {
		t.Helper()
		openIn(t, r.App, core.SetAgentState{ID: id, State: core.AgentWorking})
	}

	tests := []struct {
		name    string
		setup   func(t *testing.T, r *cleanupRig)
		head    string
		toast   func(r *cleanupRig) string // the toast after the check
		box     []string                   // text the box holds; none means no box
		after   func(t *testing.T, r *cleanupRig)
		final   func(r *cleanupRig) string // the notice after enter
		level   toastLevel
		removed bool // the worktree is gone at the end
		branch  bool // the branch is left at the end
	}{
		{
			name: "modified file",
			setup: func(t *testing.T, r *cleanupRig) {
				write(t, filepath.Join(r.wt, "a.txt"), "changed\n")
			},
			toast: func(*cleanupRig) string {
				return mergedToast + "; .worktrees/feat-x has 1 uncommitted change, so it stays"
			},
			branch: true,
		},
		{
			name: "untracked files",
			setup: func(t *testing.T, r *cleanupRig) {
				write(t, filepath.Join(r.wt, "u1.txt"), "1\n")
				write(t, filepath.Join(r.wt, "u2.txt"), "2\n")
			},
			toast: func(*cleanupRig) string {
				return mergedToast + "; .worktrees/feat-x has 2 uncommitted changes, so it stays"
			},
			branch: true,
		},
		{
			name:  "locked worktree",
			setup: func(t *testing.T, r *cleanupRig) { gitIn(t, "git", r.main, "worktree", "lock", r.wt) },
			toast: func(*cleanupRig) string {
				return mergedToast + "; .worktrees/feat-x is locked, so it stays"
			},
			branch: true,
		},
		{
			name:  "working agent that cd'd in",
			setup: func(t *testing.T, r *cleanupRig) { working(t, r, r.agentID) },
			toast: func(r *cleanupRig) string {
				return mergedToast + "; " + r.name() + " is still working in .worktrees/feat-x"
			},
			branch: true,
		},
		{
			name: "working agent started in it that cd'd out",
			setup: func(t *testing.T, r *cleanupRig) {
				id := r.keeper(t, core.KindAgent)
				openIn(t, r.App, core.SetAgentCwd{ID: id, Cwd: r.main})
				working(t, r, id)
			},
			toast: func(r *cleanupRig) string {
				return mergedToast + "; " + r.nameOf(r.keepers[0]) + " is still working in .worktrees/feat-x"
			},
			branch: true,
		},
		{
			name:  "branch two commits past the head",
			setup: func(t *testing.T, r *cleanupRig) { commit(t, r, 2) },
			toast: func(*cleanupRig) string { return mergedToast },
			box:   []string{"[-] delete the local branch  feat/x", "kept feat/x: it has 2 commits the PR did not merge"},
			final: func(*cleanupRig) string {
				return "removed .worktrees/feat-x; kept feat/x: it has 2 commits the PR did not merge"
			},
			level:   levelSuccess,
			removed: true, branch: true,
		},
		{
			name: "branch past the head with a shell in the worktree",
			setup: func(t *testing.T, r *cleanupRig) {
				commit(t, r, 2)
				r.keeper(t, core.KindShell)
			},
			toast: func(*cleanupRig) string { return mergedToast },
			box: []string{
				"[-] remove the worktree  .worktrees/feat-x", "    1 shell is in it",
				"[-] delete the local branch  feat/x", "kept feat/x: it has 2 commits the PR did not merge",
			},
			final:  func(*cleanupRig) string { return noticeNothingToClean },
			branch: true,
		},
		{
			name:  "head unknown here",
			head:  unknownHead,
			toast: func(*cleanupRig) string { return mergedToast },
			box:   []string{"[-] delete the local branch  feat/x", "kept feat/x: #7's head 1234567 is not in this repository"},
			final: func(*cleanupRig) string {
				return "removed .worktrees/feat-x; kept feat/x: #7's head 1234567 is not in this repository"
			},
			level:   levelSuccess,
			removed: true, branch: true,
		},
		{
			name: "main checkout on the branch",
			setup: func(t *testing.T, r *cleanupRig) {
				moveOut(t, r)
				gitIn(t, "git", r.main, "switch", "-q", "feat/x")
			},
			toast: func(r *cleanupRig) string {
				return mergedToast + "; " + r.main + " is outside AIM's worktrees, so it stays"
			},
			branch: true,
		},
		{
			name: "worktree outside both roots",
			setup: func(t *testing.T, r *cleanupRig) {
				moveOut(t, r)
				gitIn(t, "git", r.main, "worktree", "add", "-q", filepath.Join(filepath.Dir(r.main), "elsewhere"), "feat/x")
			},
			toast: func(r *cleanupRig) string {
				return mergedToast + "; " + filepath.Join(filepath.Dir(r.main), "elsewhere") + " is outside AIM's worktrees, so it stays"
			},
			branch: true,
		},
		{
			name:   "no worktree on the branch",
			setup:  moveOut,
			toast:  func(*cleanupRig) string { return mergedToast },
			branch: true,
		},
		{
			name:   "agent starts working after the box opened",
			toast:  func(*cleanupRig) string { return mergedToast },
			box:    []string{"[x] remove the worktree  .worktrees/feat-x"},
			after:  func(t *testing.T, r *cleanupRig) { working(t, r, r.agentID) },
			final:  func(r *cleanupRig) string { return r.name() + " is still working in .worktrees/feat-x" },
			branch: true,
		},
		{
			name:  "shell opened in it after the box opened",
			toast: func(*cleanupRig) string { return mergedToast },
			box:   []string{"[x] remove the worktree  .worktrees/feat-x"},
			after: func(t *testing.T, r *cleanupRig) {
				t.Helper()
				r.keeper(t, core.KindShell)
			},
			final:  func(*cleanupRig) string { return kept("1 shell is in it") },
			branch: true,
		},
		{
			name:  "agent started in it and working after the box opened",
			toast: func(*cleanupRig) string { return mergedToast },
			box:   []string{"[x] remove the worktree  .worktrees/feat-x"},
			after: func(t *testing.T, r *cleanupRig) {
				t.Helper()
				working(t, r, r.keeper(t, core.KindAgent))
			},
			final:  func(r *cleanupRig) string { return r.nameOf(r.keepers[0]) + " is still working in .worktrees/feat-x" },
			branch: true,
		},
		{
			name:  "agent left the worktree and started working after the box opened",
			toast: func(*cleanupRig) string { return mergedToast },
			box:   []string{"at "},
			after: func(t *testing.T, r *cleanupRig) {
				openIn(t, r.App, core.SetAgentCwd{ID: r.agentID, Cwd: r.main})
				working(t, r, r.agentID)
			},
			final:   func(*cleanupRig) string { return "removed .worktrees/feat-x; deleted feat/x" },
			level:   levelSuccess,
			removed: true,
		},
		{
			name:  "branch moved after the box opened",
			toast: func(*cleanupRig) string { return mergedToast },
			box:   []string{"at "},
			after: func(t *testing.T, r *cleanupRig) { commit(t, r, 1) },
			final: func(*cleanupRig) string {
				return "removed .worktrees/feat-x; kept feat/x: it moved since you asked"
			},
			level:   levelSuccess,
			removed: true, branch: true,
		},
		{
			name:  "worktree switched to another branch after the box opened",
			toast: func(*cleanupRig) string { return mergedToast },
			box:   []string{"at "},
			after: func(t *testing.T, r *cleanupRig) {
				gitIn(t, "git", r.wt, "switch", "-q", "-c", "other")
				commit(t, r, 1)
			},
			final:  func(*cleanupRig) string { return "kept .worktrees/feat-x: it is no longer on feat/x" },
			level:  levelError,
			branch: true,
		},
		{
			name:  "worktree detached with a new commit after the box opened",
			toast: func(*cleanupRig) string { return mergedToast },
			box:   []string{"at "},
			after: func(t *testing.T, r *cleanupRig) {
				moveOut(t, r)
				commit(t, r, 1)
			},
			final:  func(*cleanupRig) string { return "kept .worktrees/feat-x: it is no longer on feat/x" },
			level:  levelError,
			branch: true,
		},
		{
			name:   "file modified after the box opened",
			toast:  func(*cleanupRig) string { return mergedToast },
			box:    []string{"at "},
			after:  func(t *testing.T, r *cleanupRig) { write(t, filepath.Join(r.wt, "a.txt"), "changed\n") },
			final:  func(*cleanupRig) string { return "kept .worktrees/feat-x: it has uncommitted changes" },
			level:  levelError,
			branch: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			r := newCleanupRig(t)
			if tc.setup != nil {
				tc.setup(t, r)
			}

			head := tc.head
			if head == "" {
				head = r.prHead
			}

			r.offer(head)

			if want := tc.toast(r); r.notice != want {
				t.Errorf("toast = %q, want %q", r.notice, want)
			}

			if len(tc.box) == 0 {
				if r.confirm != nil {
					t.Fatalf("a box opened:\n%s", r.text())
				}

				r.assertEnd(t, tc.removed, tc.branch)

				return
			}

			if r.confirm == nil || r.confirm.cleanup == nil {
				t.Fatalf("no cleanup box opened; toast %q", r.notice)
			}

			got := r.text()
			for _, want := range tc.box {
				if !strings.Contains(got, want) {
					t.Errorf("the box lacks %q:\n%s", want, got)
				}
			}

			if tc.after != nil {
				tc.after(t, r)
			}

			r.notice = ""
			r.keys(keyEnter)
			r.done(t)

			if want := tc.final(r); r.notice != want || (tc.level != levelNotice && r.noticeLevel() != tc.level) {
				t.Errorf("after enter: toast %q at level %v, want %q at level %v", r.notice, r.noticeLevel(), want, tc.level)
			}

			r.assertEnd(t, tc.removed, tc.branch)
		})
	}
}

// assertEnd checks what is left of the worktree and the branch, and that no
// pane closed.
func (r *cleanupRig) assertEnd(t *testing.T, removed, branch bool) {
	t.Helper()

	// A worktree outside the roots is never removed; only the rig's own is looked at.
	if got := !r.worktreeExists(); got != removed {
		t.Errorf("worktree removed = %v, want %v", got, removed)
	}

	if got := r.branchExists(t); got != branch {
		t.Errorf("branch left = %v, want %v", got, branch)
	}

	for _, id := range r.ids {
		if _, ok := r.store.State().Panes[id]; !ok {
			t.Errorf("pane %s was closed", id)
		}
	}
}

const cleanupSession = "11111111-2222-4333-8444-555555555555"

// seedTurns writes one turn ref pair in the worktree and one in the main
// checkout, and keeps their roots' keys: the worktree's root cannot be read once removed.
func (r *cleanupRig) seedTurns(t *testing.T) {
	t.Helper()

	r.turnKeys = map[string]string{}

	for _, dir := range []string{r.wt, r.main} {
		sum := sha256.Sum256([]byte(rootOf(t, dir)))
		r.turnKeys[dir] = hex.EncodeToString(sum[:])[:16]
	}

	for _, dir := range []string{r.wt, r.main} {
		if _, err := git.Snapshot(context.Background(), dir, cleanupSession, git.SnapshotOptions{TempDir: t.TempDir()}); err != nil {
			t.Fatalf("snapshot in %s: %v", dir, err)
		}
	}
}

// prRefs counts the refs fetched for pull request 7.
func (r *cleanupRig) prRefs(t *testing.T) int {
	t.Helper()

	return len(strings.Fields(gitRead(t, "git", r.main, "for-each-ref", "--format=%(refname)", "refs/aim/pr/7/")))
}

// turnRefs counts the turn refs recorded for the worktree root.
func (r *cleanupRig) turnRefs(t *testing.T, root string) int {
	t.Helper()

	key := r.turnKeys[root]
	n := 0

	for ref := range strings.FieldsSeq(gitRead(t, "git", r.main, "for-each-ref", "--format=%(refname)", "refs/aim/turns/"+cleanupSession+"/")) {
		if strings.Contains(ref, "/"+key+"/") {
			n++
		}
	}

	return n
}

func openShellThere(t *testing.T, r *cleanupRig) { t.Helper(); r.keeper(t, core.KindShell) }

func dirtyWorktree(t *testing.T, r *cleanupRig) {
	t.Helper()
	write(t, filepath.Join(r.wt, "late.txt"), "late\n")
}

// enter drops #7's refs whatever happens to the worktree; its turn refs go only
// with it; the main checkout's always stay.
func TestCleanupPrunesRefs(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name                string
		toggles             string
		before              func(*testing.T, *cleanupRig)
		key                 tea.KeyPressMsg
		toast               string
		prGone, wtTurnsGone bool
		early               func(*testing.T, *cleanupRig) // runs before the box opens
	}{
		{"both", "", nil, keyEnter, "removed .worktrees/feat-x; deleted feat/x", true, true, nil},
		{"branch off", "b", nil, keyEnter, "removed .worktrees/feat-x", true, true, nil},
		{"worktree off", "w", nil, keyEnter, "nothing to clean up", true, false, nil},
		{"refused: a shell opened in the worktree", "", openShellThere, keyEnter, kept("1 shell is in it"), true, false, nil},
		{"removal failed: a change after the box", "", dirtyWorktree, keyEnter, "kept .worktrees/feat-x: it has uncommitted changes", true, false, nil},
		{"esc", "", nil, keyEsc, "", false, false, nil},
		{"in-use box: enter says nothing to clean up", "", nil, keyEnter, noticeNothingToClean, true, false, openShellThere},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			var early []func(*testing.T, *cleanupRig)
			if tc.early != nil {
				early = append(early, tc.early)
			}

			r := newCleanupRig(t, early...)
			r.seedTurns(t)
			r.mergeToBox(t)

			if tc.before != nil {
				tc.before(t, r)
			}

			if tc.toggles != "" {
				r.typed(tc.toggles)
			}

			if got := r.prRefs(t); got != 2 {
				t.Fatalf("PR refs before = %d, want 2", got)
			}

			r.notice = ""
			r.keys(tc.key)

			if tc.key == keyEnter {
				r.done(t)
			}

			if tc.prGone {
				r.until(t, "the PR refs", func() bool { return r.prRefs(t) == 0 })
			} else if got := r.prRefs(t); got != 2 {
				t.Errorf("PR refs = %d, want 2", got)
			}

			if tc.toast != "" && r.notice != tc.toast {
				t.Errorf("toast = %q, want %q", r.notice, tc.toast)
			}

			if got := r.turnRefs(t, r.wt) == 0; got != tc.wtTurnsGone {
				t.Errorf("worktree turn refs gone = %v, want %v", got, tc.wtTurnsGone)
			}

			if got := r.turnRefs(t, r.main); got != 1 {
				t.Errorf("main checkout turn refs = %d, want 1", got)
			}
		})
	}
}

// A refs/aim/pr/7/head.lock file makes the delete fail; the clean-up still reports success.
func TestCleanupSucceedsWhenThePruneFails(t *testing.T) {
	t.Parallel()

	r := newCleanupRig(t)
	r.seedTurns(t)
	r.mergeToBox(t)

	common := gitRead(t, "git", r.main, "rev-parse", "--path-format=absolute", "--git-common-dir")
	write(t, filepath.Join(strings.TrimSpace(common), "refs", "aim", "pr", "7", "head.lock"), "")

	r.notice = ""
	r.keys(keyEnter)
	r.done(t)

	if want := "removed .worktrees/feat-x; deleted feat/x"; r.notice != want || r.noticeLevel() != levelSuccess {
		t.Errorf("toast = %q at level %v, want %q as a success", r.notice, r.noticeLevel(), want)
	}

	if got := r.turnRefs(t, r.wt); got != 0 {
		t.Errorf("worktree turn refs = %d, want 0", got)
	}

	if got := r.prRefs(t); got != 2 {
		t.Errorf("PR refs = %d, want 2", got)
	}
}

func TestCleanupOfferLate(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		run   func(t *testing.T, r *cleanupRig)
		box   bool
		toast func(r *cleanupRig) string
	}{
		{
			name: "palette opened",
			run: func(t *testing.T, r *cleanupRig) {
				held := r.check(r.prHead)

				r.openCommandPalette()
				r.run(r.step(held))
			},
			toast: func(*cleanupRig) string { return mergedToast },
		},
		{
			name: "a key cleared the toast",
			run: func(t *testing.T, r *cleanupRig) {
				held := r.check(r.prHead)

				r.notice = ""
				r.run(r.step(held))
			},
			box:   true,
			toast: func(*cleanupRig) string { return "" },
		},
		{
			name: "the agent started working",
			run: func(t *testing.T, r *cleanupRig) {
				held := r.check(r.prHead)

				openIn(t, r.App, core.SetAgentState{ID: r.agentID, State: core.AgentWorking})
				r.run(r.step(held))
			},
			toast: func(r *cleanupRig) string {
				return mergedToast + "; " + r.name() + " is still working in .worktrees/feat-x"
			},
		},
		{
			name: "a second merge started",
			run: func(t *testing.T, r *cleanupRig) {
				first := r.check(r.prHead)
				second := r.check(r.prHead)

				r.run(r.step(first))

				if r.confirm != nil {
					t.Fatal("the older check opened a box")
				}

				r.run(r.step(second))
			},
			box:   true,
			toast: func(*cleanupRig) string { return mergedToast },
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			r := newCleanupRig(t)
			tc.run(t, r)

			if got := r.confirm != nil && r.confirm.cleanup != nil; got != tc.box {
				t.Errorf("cleanup box open = %v, want %v", got, tc.box)
			}

			if want := tc.toast(r); r.notice != want {
				t.Errorf("toast = %q, want %q", r.notice, want)
			}
		})
	}
}

// cleanupScene draws the cleanup box over the review scene.
func cleanupScene(t *testing.T, icons bool, w, h int, tune func(*cleanupTarget)) *App {
	t.Helper()

	target := cleanupTarget{
		repo: prRepo, number: 7, title: "Show the pull request over its diff", head: prHeadOID, home: "/home/u",
		found: cleanupFound{
			main: "/w/repo", path: "/w/repo/.worktrees/feat-review-pane", branch: "feat/review-pane", tip: prHeadOID, managed: true,
		},
		remove: true, del: true,
	}
	if tune != nil {
		tune(&target)
	}

	s := reviewScene{
		theme: "catppuccin-mocha", icons: icons, w: w, h: h, res: turn4Result(), turn: 4, turns: 4, focused: true,
		branch: true, tune: func(t *testing.T, a *App, v *reviewView) {
			t.Helper()

			prSceneWith(nil, 0)(t, a, v)
			a.confirm = &confirm{cleanup: &target}
		},
	}

	return s.build(t)
}

// keptScene makes the scene's worktree kept by an agent and a shell, as the check
// leaves a target with panes in it.
func keptScene(t *cleanupTarget) {
	t.inUse = []cleanupPane{{name: "muxr-worker-2", agent: true}, {}}
	t.remove, t.del = false, false
}

func cleanupScenes() []struct {
	name string
	w, h int
	tune func(*cleanupTarget)
} {
	return []struct {
		name string
		w, h int
		tune func(*cleanupTarget)
	}{
		{"offer", 120, 40, nil},
		{"branch-past-head", 120, 40, func(t *cleanupTarget) {
			t.found.ahead, t.del = 2, false
		}},
		{"head-missing", 120, 40, func(t *cleanupTarget) {
			t.found.headErr, t.found.tip, t.del = git.ErrNoCommit, "", false
		}},
		{"worktree-off", 120, 40, func(t *cleanupTarget) { t.remove = false }},
		{"branch-off", 120, 40, func(t *cleanupTarget) { t.del = false }},
		{"branch-behind-head", 120, 40, func(t *cleanupTarget) { t.found.tip = "0123456789abcdef0123456789abcdef01234567" }},
		{"kept-by-panes", 120, 40, keptScene},
		{"kept-by-panes-branch-past-head", 120, 40, func(t *cleanupTarget) {
			keptScene(t)
			t.found.ahead = 2
		}},
	}
}

// TestCleanupGolden draws the box once in colour; it compares highlighted
// colours, so it runs serially.
func TestCleanupGolden(t *testing.T) {
	for _, c := range cleanupScenes() {
		t.Run("catppuccin-mocha/icons=true/"+c.name, func(t *testing.T) {
			golden.RequireEqual(t, []byte(cleanupScene(t, true, c.w, c.h, c.tune).View().Content))
		})
	}
}

// TestCleanupText is the same screens without styling, so the layout reads.
func TestCleanupText(t *testing.T) {
	t.Parallel()

	for _, icons := range []bool{true, false} {
		for _, c := range cleanupScenes() {
			t.Run(fmt.Sprintf("icons=%v/%s", icons, c.name), func(t *testing.T) {
				t.Parallel()

				got := ansi.Strip(cleanupScene(t, icons, c.w, c.h, c.tune).View().Content)
				golden.RequireEqual(t, []byte(got))
			})
		}
	}
}

func TestCleanupBoxFits(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name         string
		w, h         int
		blank, hints bool // the blank rows and the hint row the box keeps
	}{
		{"80x24", 80, 24, true, true},
		{"60x16", 60, 16, false, true},
		{"60x14", 60, 14, false, false},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got := ansi.Strip(cleanupScene(t, true, tc.w, tc.h, nil).View().Content)
			lines := strings.Split(got, "\n")

			for i, l := range lines {
				if w := ansi.StringWidth(l); w != tc.w {
					t.Errorf("line %d is %d cells wide, want %d: %q", i, w, tc.w, l)
				}
			}

			if has := strings.Contains(got, "enter clean up"); has != tc.hints {
				t.Errorf("hint row shown = %v, want %v:\n%s", has, tc.hints, got)
			}

			pw := min(maxCleanupWidth, tc.w-2)
			x := (tc.w - pw) / 2
			blanks, in := 0, false

			for _, l := range lines {
				switch {
				case strings.Contains(l, "Clean up after"):
					in = true

					continue
				case strings.Contains(l, "╰"):
					in = false
				}

				if in && strings.TrimSpace(ansi.Cut(l, x+1, x+pw-1)) == "" {
					blanks++
				}
			}

			if has := blanks > 1; has != tc.blank {
				t.Errorf("box keeps %d blank rows, want blank rows kept = %v:\n%s", blanks, tc.blank, got)
			}

			if !strings.Contains(got, "[x] remove the worktree") {
				t.Errorf("the worktree row is gone:\n%s", got)
			}
		})
	}
}

// merged makes the state read pull request number of branch merged at the
// rig's head, as the poll or a lookup leaves it after the merge.
func (r *cleanupRig) merged(t *testing.T, branch string, number int, tune func(*core.PRInfo)) {
	t.Helper()

	pr := core.PRInfo{
		Number: number, Title: "Add x", State: prMergedState, HeadBranch: branch, HeadOID: r.prHead, BaseBranch: "main",
	}
	if tune != nil {
		tune(&pr)
	}

	// A lookup the rail makes for the branch must find the same record, or it clears this one.
	r.know(branch, github.PR{
		Number: pr.Number, Title: pr.Title, State: pr.State, HeadBranch: pr.HeadBranch, HeadOID: pr.HeadOID,
		BaseBranch: pr.BaseBranch, CrossRepo: pr.CrossRepo,
	})
	openIn(t, r.App, core.SetBranchPR{Repo: prRepoKey, Branch: branch, PR: pr, Found: true})
}

// know makes the lookup of branch find pr.
func (r *cleanupRig) know(branch string, pr github.PR) {
	r.mu.Lock()
	defer r.mu.Unlock()

	r.found[branch] = pr
}

// command starts worktree.cleanup and returns the command it makes.
func (r *cleanupRig) command(t *testing.T) tea.Cmd {
	t.Helper()

	c, ok := r.reg.get("worktree.cleanup")
	if !ok {
		t.Fatal("worktree.cleanup is not registered")
	}

	return c.run(r.App)
}

// held runs the command's work and returns its answer without feeding it to the app.
func (r *cleanupRig) held(t *testing.T) cleanupPlaceMsg {
	t.Helper()

	cmd := r.command(t)
	if cmd == nil {
		t.Fatalf("worktree.cleanup made no command; notice %q", r.notice)
	}

	return cmd().(cleanupPlaceMsg)
}

// said runs the command and waits for the notice it ends in; no box may open.
func (r *cleanupRig) said(t *testing.T) string {
	t.Helper()

	r.notice = ""
	r.run(r.command(t))
	r.until(t, "the notice", func() bool { return r.notice != "" })

	if r.confirm != nil {
		t.Fatalf("a box opened:\n%s", r.text())
	}

	return r.notice
}

func TestCleanupCommand(t *testing.T) {
	t.Parallel()

	r := newCleanupRig(t)
	r.merged(t, "feat/x", 7, nil)

	r.run(r.command(t))
	r.until(t, "the cleanup box", func() bool { return r.confirm != nil && r.confirm.cleanup != nil })

	got := r.text()
	for _, want := range []string{
		"Clean up after #7 ", "#7 Add x", "is merged at " + r.prHead[:7] + ". Clean up its worktree?",
		"[x] remove the worktree  .worktrees/feat-x", "[x] delete the local branch  feat/x",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("the box lacks %q:\n%s", want, got)
		}
	}

	r.notice = ""
	r.keys(keyEnter)
	r.done(t)

	if want := "removed .worktrees/feat-x; deleted feat/x"; r.notice != want {
		t.Errorf("toast = %q, want %q", r.notice, want)
	}

	r.assertEnd(t, true, false)
}

func TestCleanupCommandNotices(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		setup func(t *testing.T, r *cleanupRig)
		want  func(r *cleanupRig) string
	}{
		{
			name:  "no pull request source",
			setup: func(_ *testing.T, r *cleanupRig) { r.prs.src = nil },
			want:  func(*cleanupRig) string { return "pull requests are off" },
		},
		{
			name: "outside a repository",
			setup: func(t *testing.T, r *cleanupRig) {
				openIn(t, r.App, core.SetAgentCwd{ID: r.agentID, Cwd: t.TempDir()})
			},
			want: func(*cleanupRig) string { return "not a git repository" },
		},
		{
			name:  "detached head",
			setup: func(t *testing.T, r *cleanupRig) { gitIn(t, "git", r.wt, "switch", "-q", "--detach") },
			want:  func(*cleanupRig) string { return "clean up: HEAD is detached" },
		},
		{
			name:  "no pull request",
			setup: func(t *testing.T, r *cleanupRig) { gitIn(t, "git", r.wt, "switch", "-q", "-c", "nopr") },
			want:  func(*cleanupRig) string { return "no pull request for nopr" },
		},
		{
			name: "pull request open",
			setup: func(t *testing.T, r *cleanupRig) {
				openIn(t, r.App, core.SetRepoPRs{Repo: prRepoKey, DefaultBranch: "main", Open: []core.PRInfo{{Number: 7, HeadBranch: "feat/x", State: prOpenState}}})
			},
			want: func(*cleanupRig) string { return "#7 is not merged" },
		},
		{
			name: "pull request closed",
			setup: func(t *testing.T, r *cleanupRig) {
				openIn(t, r.App, core.SetRepoPRs{Repo: prRepoKey, DefaultBranch: "main"})
				r.merged(t, "feat/x", 7, func(p *core.PRInfo) { p.State = "CLOSED" })
			},
			want: func(*cleanupRig) string { return "#7 is not merged" },
		},
		{
			name: "dirty worktree",
			setup: func(t *testing.T, r *cleanupRig) {
				r.merged(t, "feat/x", 7, nil)
				write(t, filepath.Join(r.wt, "a.txt"), "changed\n")
			},
			want: func(*cleanupRig) string { return ".worktrees/feat-x has 1 uncommitted change, so it stays" },
		},
		{
			name: "working agent",
			setup: func(t *testing.T, r *cleanupRig) {
				r.merged(t, "feat/x", 7, nil)
				openIn(t, r.App, core.SetAgentState{ID: r.agentID, State: core.AgentWorking})
			},
			want: func(r *cleanupRig) string { return r.name() + " is still working in .worktrees/feat-x" },
		},
		{
			name: "main checkout",
			setup: func(t *testing.T, r *cleanupRig) {
				r.merged(t, "feat/x", 7, nil)
				gitIn(t, "git", r.wt, "switch", "-q", "--detach")
				gitIn(t, "git", r.main, "switch", "-q", "feat/x")
				openIn(t, r.App, core.FocusPane{ID: r.other})
			},
			want: func(r *cleanupRig) string { return r.main + " is outside AIM's worktrees, so it stays" },
		},
		{
			name: "pull request from another repository",
			setup: func(t *testing.T, r *cleanupRig) {
				// The poll's record is the fork's branch; the lookup of the local branch finds the merged pull request.
				openIn(t, r.App, core.SetRepoPRs{Repo: prRepoKey, DefaultBranch: "main"})
				r.merged(t, "fork-branch", 7, func(p *core.PRInfo) { p.CrossRepo = true; p.HeadBranch = "feat/x" })
				r.know("feat/x", github.PR{Number: 7, HeadBranch: "feat/x", BaseBranch: "main", State: prMergedState})
			},
			want: func(*cleanupRig) string { return "#7's branch is in another repository" },
		},
		{
			name: "default branch",
			setup: func(t *testing.T, r *cleanupRig) {
				r.merged(t, "main", 9, nil)
				openIn(t, r.App, core.FocusPane{ID: r.other})
			},
			want: func(*cleanupRig) string { return "main is the default branch, so it stays" },
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			r := newCleanupRig(t)
			tc.setup(t, r)

			if got, want := r.said(t), tc.want(r); got != want {
				t.Errorf("notice = %q, want %q", got, want)
			}

			if r.noticeLevel() != levelNotice {
				t.Errorf("notice level = %v, want a plain notice", r.noticeLevel())
			}

			r.assertEnd(t, false, true)
		})
	}
}

func TestCleanupCommandLate(t *testing.T) {
	t.Parallel()

	// feed gives the app the place answer and returns the check it starts.
	feed := func(r *cleanupRig, msg cleanupPlaceMsg) tea.Cmd { return r.step(msg) }

	tests := []struct {
		name string
		run  func(t *testing.T, r *cleanupRig)
	}{
		{"focus moved before the place answer", func(t *testing.T, r *cleanupRig) {
			held := r.held(t)

			openIn(t, r.App, core.FocusPane{ID: r.other})
			r.run(feed(r, held))
		}},
		{"palette opened before the place answer", func(t *testing.T, r *cleanupRig) {
			held := r.held(t)

			r.openCommandPalette()
			r.run(feed(r, held))
		}},
		{"confirm opened before the place answer", func(t *testing.T, r *cleanupRig) {
			held := r.held(t)

			r.openConfirm(confirm{tab: r.store.State().Tab().ID, yes: func(*App) tea.Cmd { return nil }})
			r.run(feed(r, held))
		}},
		{"newer command before the place answer", func(t *testing.T, r *cleanupRig) {
			older := r.held(t)
			_ = r.held(t)

			r.run(feed(r, older))
		}},
		{"focus moved before the check answer", func(t *testing.T, r *cleanupRig) {
			check := feed(r, r.held(t))
			if check == nil {
				t.Fatal("the place answer started no check")
			}

			held := check()

			openIn(t, r.App, core.FocusPane{ID: r.other})
			r.run(r.step(held))
		}},
		{"palette opened before the check answer", func(t *testing.T, r *cleanupRig) {
			check := feed(r, r.held(t))
			if check == nil {
				t.Fatal("the place answer started no check")
			}

			held := check()

			r.openCommandPalette()
			r.run(r.step(held))
		}},
		{"newer command before the check answer", func(t *testing.T, r *cleanupRig) {
			check := feed(r, r.held(t))
			if check == nil {
				t.Fatal("the place answer started no check")
			}

			held := check()
			_ = r.held(t)

			r.run(r.step(held))
		}},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			r := newCleanupRig(t)
			r.merged(t, "feat/x", 7, nil)
			r.notice = ""

			tc.run(t, r)

			if r.confirm != nil && r.confirm.cleanup != nil {
				t.Errorf("a cleanup box opened:\n%s", r.text())
			}

			if r.notice != "" {
				t.Errorf("notice = %q, want none", r.notice)
			}

			r.assertEnd(t, false, true)
		})
	}
}

// An early notice supersedes a check still in flight, so its answer opens no box.
func TestCleanupCommandEarlyNoticeLate(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		// held runs the first command as far as the stage whose answer arrives late.
		held func(t *testing.T, r *cleanupRig) tea.Msg
	}{
		{"place answer", func(t *testing.T, r *cleanupRig) tea.Msg { return r.held(t) }},
		{"check answer", func(t *testing.T, r *cleanupRig) tea.Msg {
			check := r.step(r.held(t))
			if check == nil {
				t.Fatal("the place answer started no check")
			}

			return check()
		}},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			r := newCleanupRig(t)
			r.merged(t, "feat/x", 7, nil)

			held := tc.held(t, r)

			r.prs.src = nil
			r.notice = ""

			if cmd := r.command(t); cmd != nil || r.notice != noticePRsOff {
				t.Fatalf("early command = %v, notice %q, want a notice only", cmd != nil, r.notice)
			}

			r.run(r.step(held))

			if r.confirm != nil {
				t.Errorf("a box opened:\n%s", r.text())
			}
		})
	}
}

func TestCleanupCommandRegistered(t *testing.T) {
	t.Parallel()

	reg := testRegistry(t)

	c, ok := reg.get("worktree.cleanup")
	if !ok || c.title != "Clean up a merged worktree" || c.icon != iconCleanup {
		t.Errorf("worktree.cleanup = %+v, %v; want the broom and its title", c, ok)
	}

	km, err := resolveLeader(leaderKeys("W", "worktree.cleanup"), reg)
	if err != nil {
		t.Fatal(err)
	}

	if got, _ := km.lookup("W"); got != "worktree.cleanup" {
		t.Errorf("lookup(W) = %q, want worktree.cleanup", got)
	}

	if g := groupFor(km, "worktree.cleanup"); g != "Git" {
		t.Errorf("group = %q, want Git", g)
	}
}
