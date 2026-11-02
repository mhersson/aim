package ui

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/charmbracelet/x/exp/golden"

	"github.com/mhersson/aim/internal/core"
	"github.com/mhersson/aim/internal/git"
	"github.com/mhersson/aim/internal/github"
	"github.com/mhersson/aim/internal/harness"
	"github.com/mhersson/aim/internal/ui/palette"
)

// gitFileOnly limits git to local transports, so no push test can reach a network.
const gitFileOnly = "GIT_ALLOW_PROTOCOL=file"

// refreshRec records the repositories the poller was asked to refresh.
type refreshRec struct {
	*fakePRs

	rmu   sync.Mutex
	repos []github.Repo
}

func (r *refreshRec) Refresh(repo github.Repo) {
	r.rmu.Lock()
	defer r.rmu.Unlock()

	r.repos = append(r.repos, repo)
}

func (r *refreshRec) refreshed() []github.Repo {
	r.rmu.Lock()
	defer r.rmu.Unlock()

	return slices.Clone(r.repos)
}

// gitRead runs the real git in dir and returns its trimmed output.
func gitRead(t *testing.T, bin, dir string, args ...string) string {
	t.Helper()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, bin, args...)
	cmd.Dir = dir

	cmd.Env = append(os.Environ(), gitFileOnly)

	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("git %v: %v", args, err)
	}

	return strings.TrimSpace(string(out))
}

// churnMsgs is churn that also returns every message it fed back.
func (r *railApp) churnMsgs(t *testing.T, cmd tea.Cmd) []tea.Msg {
	t.Helper()

	var all []tea.Msg

	queue := []tea.Cmd{cmd}

	for rounds := 0; len(queue) > 0; rounds++ {
		if rounds > 200 {
			t.Fatal("commands keep raising commands")
		}

		next := queue[0]
		queue = queue[1:]

		for _, m := range flatten(next) {
			if m != nil {
				all = append(all, m)
				queue = append(queue, r.step(m))
			}
		}
	}

	return all
}

// pushRemoteURL is how the rig's repository reaches its remote. A relative URL
// shows the same in every run.
const pushRemoteURL = "../remote.git"

// pushHead is the id of the newest commit on feat/x, which the push golden shows.
const pushHead = "2388116000000000000000000000000000000000"

// pushScenario makes proj on feat/x two commits past main, with one edit left in
// the work tree, and origin at pushRemoteURL holding main.
func pushScenario(t *testing.T, f *fakeGit) *fakeRepo {
	t.Helper()

	r := f.repo(t, "proj")
	write(t, filepath.Join(r.Root(), "a.txt"), "one\n")

	main := r.commit("init")
	r.branch("feat/x")
	r.remote("origin", pushRemoteURL).setBranch("main", main)
	r.track("origin", "main", main)
	r.commit("add the lexer")
	r.commitAs(pushHead, "fix the parser")
	write(t, filepath.Join(r.Root(), "a.txt"), "one\ntwo\n")

	return r
}

// pushRig is a rail app with an agent in a repository named proj, on feat/x two
// commits past main. origin has main.
type pushRig struct {
	*prApp
	rec    *refreshRec
	repo   *fakeRepo
	origin *fakeRemote
	dir    string
	agent  core.PaneID
}

func newPushRig(t *testing.T) *pushRig {
	t.Helper()

	f := newFakeGit(t)
	repo := pushScenario(t, f)

	p := newPRApp(t, gitOf(f))
	rec := &refreshRec{fakePRs: p.src}
	p.prs.src = rec

	r := &pushRig{prApp: p, rec: rec, repo: repo, dir: repo.Root()}

	f.edit(func() { r.origin = repo.c.remotes["origin"] })

	r.agent = r.addAgent(t, false, r.dir)
	r.churn(t, r.settle())

	return r
}

// githubURL is a fetch URL the rig's pushes never reach: a rig that has it also has a push URL.
const githubURL = "https://github.com/o/r.git"

// splitURLs gives origin a fetch URL of its own while pushes still go to
// pushRemoteURL, through a push URL.
func (r *pushRig) splitURLs(t *testing.T, fetch string) {
	t.Helper()

	r.fake.edit(func() { r.origin.url = fetch })
	r.origin.setPushURLs(pushRemoteURL)
}

// branchTip is the tip of a branch of the rig's repository.
func (r *pushRig) branchTip(branch string) string {
	var oid string

	r.fake.edit(func() { oid = r.repo.c.branches[branch] })

	return oid
}

// publish has the remote hold the current branch at its tip, as after push -u.
func (r *pushRig) publish(t *testing.T) {
	t.Helper()

	var oid string

	r.fake.edit(func() { oid = r.repo.headOID() })

	r.origin.setBranch("feat/x", oid)
	r.repo.track("origin", "feat/x", oid)
	r.repo.setUpstream("feat/x", "origin")
}

// leaderP presses leader g P and returns the command it raised.
func (r *pushRig) leaderP() tea.Cmd {
	press(r.App, keyLeader)

	press(r.App, keyRune('g'))

	return press(r.App, keyRune('P'))
}

// ask presses leader g P and answers what follows.
func (r *pushRig) ask(t *testing.T) []tea.Msg {
	t.Helper()

	return r.churnMsgs(t, r.leaderP())
}

// askNotice asks and checks that the answer carried state, opened no confirm and
// raised a notice at the notice level; it returns the notice.
func (r *pushRig) askNotice(t *testing.T, state pushState) string {
	t.Helper()

	for _, m := range r.ask(t) {
		if res, ok := m.(pushResolvedMsg); ok {
			if res.state != state || res.err != nil {
				t.Errorf("resolved state %v (err %v), want %v", res.state, res.err, state)
			}

			if r.confirm != nil || r.noticeLevel() != levelNotice {
				t.Errorf("confirm %v, notice %q at level %v, want no confirm and a notice", r.confirm != nil, r.notice, r.noticeLevel())
			}

			return r.notice
		}
	}

	t.Fatal("no pushResolvedMsg after leader g P")

	return ""
}

// askOpen asks and fails unless the push confirm is open.
func (r *pushRig) askOpen(t *testing.T) {
	t.Helper()

	r.ask(t)

	if r.confirm == nil || r.confirm.push == nil {
		t.Fatalf("no push confirm after leader g P; notice = %q", r.notice)
	}
}

// remoteRefs is the remote's branches, one ref per line in name order.
func (r *pushRig) remoteRefs(t *testing.T) string {
	t.Helper()

	var refs []string

	r.fake.edit(func() {
		for b := range r.origin.branches {
			refs = append(refs, "refs/heads/"+b)
		}
	})

	slices.Sort(refs)

	return strings.Join(refs, "\n")
}

// pushCalls counts the pushes the app ran.
func (r *pushRig) pushCalls() int { return r.fake.count("Push") }

// pushes is the branch of every push the app ran, in order.
func (r *pushRig) pushes() []string {
	var out []string

	for _, c := range r.fake.calls("Push") {
		out = append(out, c.Args...)
	}

	return out
}

func (r *pushRig) overlayText() string { return ansi.Strip(r.View().Content) }

func TestPushConfirmNamesTheTarget(t *testing.T) {
	t.Parallel()

	r := newPushRig(t)
	r.askOpen(t)

	text := r.overlayText()

	for _, want := range []string{
		"Push feat/x", "proj", "feat/x", "origin  ../remote.git", "2 commits", "sets the upstream",
		pushHead[:7] + " fix the parser", "enter push", "esc cancel",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("the overlay lacks %q:\n%s", want, text)
		}
	}

	if got := r.remoteRefs(t); got != "refs/heads/main" {
		t.Errorf("the remote holds %q before the answer, want only main", got)
	}

	if n := r.pushCalls(); n != 0 {
		t.Errorf("git pushed %d times before the answer", n)
	}

	for _, icons := range []bool{true, false} {
		name := "icons"
		if !icons {
			name = "no icons"
		}

		t.Run(name, func(t *testing.T) {
			blank := strings.TrimSuffix(strings.Repeat(strings.Repeat(" ", 70)+"\n", 16), "\n")
			golden.RequireEqual(t, []byte(overlayConfirm(nil, blank, 70, 16, *r.confirm, icons, r.styles)))
		})
	}
}

func TestPushConfirmNamesAnUpstreamAlreadySet(t *testing.T) {
	t.Parallel()

	r := newPushRig(t)
	r.publish(t)
	r.repo.commit("one more")

	r.askOpen(t)

	text := r.overlayText()
	if !strings.Contains(text, "1 commit, upstream already set") || strings.Contains(text, "sets the upstream") {
		t.Errorf("the overlay lacks the upstream label:\n%s", text)
	}

	r.churn(t, press(r.App, keyEnter))

	if want := "pushed feat/x to origin: 1 commit"; r.notice != want {
		t.Errorf("toast = %q, want %q", r.notice, want)
	}
}

func TestPushFirstPush(t *testing.T) {
	t.Parallel()

	r := newPushRig(t)
	r.askOpen(t)

	msgs := r.churnMsgs(t, press(r.App, keyEnter))

	if r.confirm != nil {
		t.Error("the confirm is still open after enter")
	}

	if want := "pushed feat/x to origin: 2 commits"; r.notice != want || r.noticeLevel() != levelSuccess {
		t.Errorf("toast = %q at level %v, want %q at the success level", r.notice, r.noticeLevel(), want)
	}

	if got := r.remoteRefs(t); !strings.Contains(got, "refs/heads/feat/x") {
		t.Errorf("the remote holds %q, want feat/x", got)
	}

	if got := r.pushes(); !slices.Equal(got, []string{"feat/x"}) {
		t.Errorf("pushed %q, want only feat/x", got)
	}

	var upstream string

	r.fake.edit(func() { upstream = r.repo.c.upstream["feat/x"] })

	if upstream != "origin" {
		t.Errorf("upstream = %q, want origin", upstream)
	}

	if countOf[railHeadMsg](msgs) == 0 {
		t.Error("the pane's head was not probed again")
	}

	if got := r.rec.refreshed(); len(got) != 0 {
		t.Errorf("refreshed %v for a remote that is not on GitHub, want nothing", got)
	}
}

func TestPushRefreshesTheGitHubRepository(t *testing.T) {
	t.Parallel()

	r := newPushRig(t)
	r.splitURLs(t, githubURL)
	r.askOpen(t)

	r.churn(t, press(r.App, keyEnter))

	if want := "pushed feat/x to origin: 2 commits"; r.notice != want {
		t.Errorf("toast = %q, want %q", r.notice, want)
	}

	if got := r.rec.refreshed(); !slices.Equal(got, []github.Repo{prRepo}) {
		t.Errorf("refreshed %v, want %v", got, []github.Repo{prRepo})
	}
}

func TestPushConfirmNamesThePushURLs(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		setup func(t *testing.T, r *pushRig)
		urls  []string
	}{
		{"the fetch url when there is no push url", func(*testing.T, *pushRig) {}, []string{"../remote.git"}},
		{"two push urls", func(t *testing.T, r *pushRig) {
			r.splitURLs(t, githubURL)
			r.origin.setPushURLs(pushRemoteURL, "../mirror.git")
		}, []string{"../remote.git", "../mirror.git"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			r := newPushRig(t)
			tt.setup(t, r)
			r.askOpen(t)

			if got := r.confirm.push.urls; !slices.Equal(got, tt.urls) {
				t.Errorf("target urls = %q, want %q", got, tt.urls)
			}

			text := r.overlayText()
			if want := "origin  " + tt.urls[0]; !strings.Contains(text, want) {
				t.Errorf("the overlay lacks %q:\n%s", want, text)
			}

			// Each further url is a row of its own, under the first.
			lines := strings.Split(text, "\n")

			i := slices.IndexFunc(lines, func(l string) bool { return strings.Contains(l, "origin  "+tt.urls[0]) })
			if i < 0 {
				t.Fatalf("no remote row:\n%s", text)
			}

			col := strings.Index(lines[i], tt.urls[0])

			for k, u := range tt.urls[1:] {
				if row := lines[i+1+k]; strings.Index(row, u) != col {
					t.Errorf("row %q does not hold %q at column %d", row, u, col)
				}
			}

			if strings.Contains(text, "github.com") {
				t.Errorf("the overlay names the fetch url, not where the push goes:\n%s", text)
			}
		})
	}
}

func TestPushConfirmCapsThePushURLs(t *testing.T) {
	t.Parallel()

	a := newTestApp(t)

	tests := []struct {
		n    int
		more string
	}{
		{1, ""},
		{3, ""},
		{4, "and 1 more"},
		{6, "and 3 more"},
	}

	for _, tt := range tests {
		t.Run(commitCount(tt.n), func(t *testing.T) {
			tgt := pushTarget{branch: "feat/x", remote: "origin", commits: 1}
			for i := range tt.n {
				tgt.urls = append(tgt.urls, fmt.Sprintf("host/o/r%d", i))
			}

			text := ansi.Strip(strings.Join(pushBox(tgt, 60, true, a.styles), "\n"))

			shown := strings.Count(text, "host/o/r")
			if want := min(tt.n, 3); shown != want {
				t.Errorf("%d urls shown, want %d:\n%s", shown, want, text)
			}

			if got := strings.Contains(text, "and "); got != (tt.more != "") || tt.more != "" && !strings.Contains(text, tt.more) {
				t.Errorf("the box lacks %q (or has an extra line):\n%s", tt.more, text)
			}
		})
	}
}

func TestPushRefusesAChangedPushURL(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		changed func(t *testing.T, r *pushRig)
	}{
		{"the push url is replaced", func(t *testing.T, r *pushRig) {
			r.origin.setPushURLs("../elsewhere.git")
		}},
		{"a push url is added", func(t *testing.T, r *pushRig) {
			r.origin.setPushURLs(pushRemoteURL, "../mirror.git")
		}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			r := newPushRig(t)
			r.splitURLs(t, "../fetch-only.git")
			r.askOpen(t)

			tt.changed(t, r)
			r.churn(t, press(r.App, keyEnter))

			if want := "push: feat/x's push URL changed since you asked; nothing pushed"; r.notice != want || r.noticeLevel() != levelNotice {
				t.Errorf("toast = %q at level %v, want %q at the notice level", r.notice, r.noticeLevel(), want)
			}

			if got := r.remoteRefs(t); got != "refs/heads/main" {
				t.Errorf("the remote holds %q, want only main", got)
			}

			if n := r.pushCalls(); n != 0 {
				t.Errorf("git pushed %d times", n)
			}
		})
	}
}

func TestPushConfirmEscCancels(t *testing.T) {
	t.Parallel()

	r := newPushRig(t)
	r.askOpen(t)

	r.churn(t, press(r.App, keyEsc))

	if r.confirm != nil {
		t.Error("the confirm is still open after esc")
	}

	if got := r.remoteRefs(t); got != "refs/heads/main" {
		t.Errorf("the remote holds %q after esc, want only main", got)
	}

	if n := r.pushCalls(); n != 0 {
		t.Errorf("git pushed %d times after esc", n)
	}
}

func TestPushConfirmIgnoresYAndN(t *testing.T) {
	t.Parallel()

	r := newPushRig(t)
	r.askOpen(t)

	for _, k := range []rune{'y', 'n'} {
		r.churn(t, press(r.App, keyRune(k)))

		if r.confirm == nil || r.confirm.push == nil {
			t.Fatalf("%q closed the push confirm", k)
		}
	}

	if n := r.pushCalls(); n != 0 {
		t.Errorf("git pushed %d times for y and n", n)
	}
}

func TestPushConfirmTargetIsFrozen(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		move func(t *testing.T, r *pushRig, other string)
	}{
		{"focus moves to a pane on another branch", func(t *testing.T, r *pushRig, other string) {
			openIn(t, r.App, core.OpenPane{Kind: core.KindShell, Cwd: other, Split: core.SplitVertical, Area: r.bodyArea()})

			if r.store.State().Tab().Focus == r.agent {
				t.Fatal("focus did not move")
			}
		}},
		{"the agent changes directory", func(t *testing.T, r *pushRig, other string) {
			openIn(t, r.App, core.SetAgentCwd{ID: r.agent, Cwd: other})
		}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			r := newPushRig(t)
			other := pushOther(t, r.fake, "other", "elsewhere")

			r.askOpen(t)
			tt.move(t, r, other)

			if r.confirm == nil || r.confirm.push == nil {
				t.Fatal("the confirm closed when the focus moved")
			}

			r.churn(t, press(r.App, keyEnter))

			if want := "pushed feat/x to origin: 2 commits"; r.notice != want {
				t.Errorf("toast = %q, want %q", r.notice, want)
			}

			got := r.remoteRefs(t)
			if !strings.Contains(got, "refs/heads/feat/x") || strings.Contains(got, "elsewhere") {
				t.Errorf("the remote holds %q, want feat/x and not elsewhere", got)
			}

			if got := r.pushes(); !slices.Equal(got, []string{"feat/x"}) {
				t.Errorf("pushed %q, want only feat/x", got)
			}
		})
	}
}

// worktree adds a linked worktree of the rig's repository beside it, on a new
// branch cut from its head with one commit of its own, and returns its root.
func (r *pushRig) worktree(t *testing.T, name, branch string) string {
	t.Helper()

	wt := r.repo.worktree(t, filepath.Join(filepath.Dir(r.dir), name), branch)
	wt.commit("work on " + branch)

	return wt.Root()
}

// pushOther is a second repository, on branch, with one commit and an edit and no remote.
func pushOther(t *testing.T, f *fakeGit, name, branch string) string {
	t.Helper()

	r := f.repo(t, name)
	write(t, filepath.Join(r.Root(), "a.txt"), "one\n")
	r.commit("init")

	if branch != "main" {
		r.branch(branch)
	}

	write(t, filepath.Join(r.Root(), "a.txt"), "one\ntwo\n")

	return r.Root()
}

// agentOnMain moves the agent's own directory to a worktree on main, which the
// remote has up to date, and lets its head probe run.
func (r *pushRig) agentOnMain(t *testing.T) {
	t.Helper()

	// The fake makes a linked worktree on a new branch; main is switched to and the new branch dropped.
	wt := r.repo.worktree(t, filepath.Join(filepath.Dir(r.dir), "main"), "main-wt")
	wt.switchTo("main")
	r.fake.edit(func() { delete(r.repo.c.branches, "main-wt") })

	openIn(t, r.App, core.SetAgentCwd{ID: r.agent, Cwd: wt.Root()})

	r.clk.Advance(6 * time.Second)
	r.churn(t, r.settle())
}

// editIn has the agent edit a tracked file of the worktree at dir, as its hook
// reports it, after the head probe's throttle, and answers what that starts.
func (r *pushRig) editIn(t *testing.T, dir string) {
	t.Helper()

	r.clk.Advance(6 * time.Second)
	r.churn(t, hook(r.App, harness.Event{Pane: r.agent, Name: "PostToolUse", Edit: filepath.Join(dir, "a.txt")}))
}

// pushed presses enter on the open confirm and checks that the remote now holds
// main and exactly the branches in want.
func (r *pushRig) pushed(t *testing.T, want ...string) {
	t.Helper()

	r.churn(t, press(r.App, keyEnter))

	refs := []string{"refs/heads/main"}
	for _, b := range want {
		refs = append(refs, "refs/heads/"+b)
	}

	slices.Sort(refs)

	if got := r.remoteRefs(t); got != strings.Join(refs, "\n") {
		t.Errorf("the remote holds %q, want %q", got, strings.Join(refs, " "))
	}

	if got := r.pushes(); !slices.Equal(got, want) {
		t.Errorf("pushed %q, want %q", got, want)
	}
}

func TestPushTargetsTheDrawer(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		setup func(t *testing.T, r *pushRig, b string)
		want  string
	}{
		{"pinned to another worktree", func(t *testing.T, r *pushRig, b string) {
			openIn(t, r.App, core.PinReview{Root: b})
		}, "feat/b"},
		{"pull request mode held on another worktree", func(t *testing.T, r *pushRig, b string) {
			openIn(t, r.App, core.ShowReviewPR{Repo: "github.com/o/r", Number: 7, Root: b})
		}, "feat/b"},
		{"open and following the agent", func(*testing.T, *pushRig, string) {}, "feat/c"},
		{"open on an agent that is not focused", func(t *testing.T, r *pushRig, _ string) {
			r.addAgent(t, false, r.dir)

			if tab := r.store.State().Tab(); tab.Review == nil || tab.Review.Pane != r.agent || tab.Focus == r.agent {
				t.Fatalf("review %+v, focus %s, want the drawer on the agent and the focus elsewhere", tab.Review, tab.Focus)
			}
		}, "feat/c"},
		{"closed with the pin kept", func(t *testing.T, r *pushRig, b string) {
			openIn(t, r.App, core.PinReview{Root: b}, core.CloseReview{})
		}, "feat/c"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			r := newPushRig(t)
			b := r.worktree(t, "b", "feat/b")
			r.editIn(t, r.worktree(t, "c", "feat/c"))
			r.churn(t, r.openReview(r.agent, true))

			if r.store.State().Tab().Review == nil {
				t.Fatalf("the drawer did not open; notice = %q", r.notice)
			}

			tt.setup(t, r, b)
			r.askOpen(t)

			if got := r.confirm.push.branch; got != tt.want {
				t.Fatalf("the confirm names %q, want %q", got, tt.want)
			}

			r.pushed(t, tt.want)
		})
	}
}

func TestPushTargetsTheFollowedPlace(t *testing.T) {
	t.Parallel()

	r := newPushRig(t)
	r.agentOnMain(t)

	if got := r.askNotice(t, pushUpToDate); !strings.Contains(got, "main") || !strings.Contains(got, "origin") {
		t.Fatalf("toast %q before the edit, want main named up to date on origin", got)
	}

	r.editIn(t, r.dir)
	r.askOpen(t)

	if got, want := r.confirm.push.root, r.dir; got != want {
		t.Errorf("the confirm pushes from %q, want %q", got, want)
	}

	r.pushed(t, "feat/x")
}

func TestPushTargetFrozenWhileFollowing(t *testing.T) {
	t.Parallel()

	r := newPushRig(t)
	r.agentOnMain(t)
	r.editIn(t, r.dir)
	r.askOpen(t)

	r.editIn(t, r.worktree(t, "y", "feat/y"))

	if r.confirm == nil || r.confirm.push == nil || r.confirm.push.branch != "feat/x" {
		t.Fatalf("confirm = %+v after the agent followed feat/y, want the one for feat/x", r.confirm)
	}

	r.pushed(t, "feat/x")
}

func TestPushShellPane(t *testing.T) {
	t.Parallel()

	r := newPushRig(t)
	r.editIn(t, r.worktree(t, "b", "feat/b"))

	openIn(t, r.App, core.OpenPane{Kind: core.KindShell, Cwd: r.worktree(t, "c", "feat/c"), Split: core.SplitVertical, Area: r.bodyArea()})

	if r.store.State().Tab().Focus == r.agent {
		t.Fatal("focus did not move to the shell")
	}

	r.askOpen(t)

	if got := r.confirm.push.branch; got != "feat/c" {
		t.Fatalf("the confirm names %q, want the shell's feat/c and not the agent's feat/b", got)
	}

	r.pushed(t, "feat/c")
}

func TestPushConfirmHeadMoved(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		moved func(t *testing.T, r *pushRig)
	}{
		{"a commit lands", func(t *testing.T, r *pushRig) { r.repo.commit("late") }},
		{"another branch is checked out", func(t *testing.T, r *pushRig) { r.repo.branch("other") }},
		{"the head is detached", func(t *testing.T, r *pushRig) { r.repo.detach() }},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			r := newPushRig(t)
			r.askOpen(t)

			tt.moved(t, r)
			r.churn(t, press(r.App, keyEnter))

			if want := "push: feat/x changed since you asked; nothing pushed"; r.notice != want || r.noticeLevel() != levelNotice {
				t.Errorf("toast = %q at level %v, want %q at the notice level", r.notice, r.noticeLevel(), want)
			}

			if got := r.remoteRefs(t); got != "refs/heads/main" {
				t.Errorf("the remote holds %q, want only main", got)
			}

			if n := r.pushCalls(); n != 0 {
				t.Errorf("git pushed %d times", n)
			}
		})
	}
}

func TestPushInFlight(t *testing.T) {
	t.Parallel()

	r := newPushRig(t)
	r.askOpen(t)

	// The command is held, as a push still running is.
	held := press(r.App, keyEnter)

	if want := "pushing feat/x to origin…"; r.notice != want || r.noticeLevel() != levelNotice {
		t.Errorf("toast = %q at level %v, want %q at the notice level", r.notice, r.noticeLevel(), want)
	}

	if r.confirm != nil {
		t.Error("the confirm is still open after enter")
	}

	r.ask(t)

	if want := "push: feat/x is already being pushed"; r.notice != want || r.noticeLevel() != levelNotice {
		t.Errorf("toast = %q at level %v, want %q at the notice level", r.notice, r.noticeLevel(), want)
	}

	if r.confirm != nil {
		t.Error("a confirm opened for a worktree being pushed")
	}

	if n := r.pushCalls(); n != 0 {
		t.Errorf("git pushed %d times before the first push ran", n)
	}

	r.churn(t, held)

	if want := "pushed feat/x to origin: 2 commits"; r.notice != want || r.noticeLevel() != levelSuccess {
		t.Errorf("toast = %q at level %v, want %q at the success level", r.notice, r.noticeLevel(), want)
	}

	if len(r.pushing) != 0 {
		t.Errorf("%v still marked as being pushed after the result", r.pushing)
	}

	r.repo.commit("more")
	r.askOpen(t)
	r.churn(t, press(r.App, keyEnter))

	if want := "pushed feat/x to origin: 1 commit"; r.notice != want {
		t.Errorf("toast = %q, want %q", r.notice, want)
	}

	if n := r.pushCalls(); n != 2 {
		t.Errorf("git pushed %d times, want 2", n)
	}
}

func TestPushInFlightStopsAnotherAnswer(t *testing.T) {
	t.Parallel()

	r := newPushRig(t)
	r.askOpen(t)

	// A push of this worktree began after the confirm opened.
	r.pushing = map[string]string{r.confirm.push.root: "feat/x"}

	r.churn(t, press(r.App, keyEnter))

	if want := "push: feat/x is already being pushed"; r.notice != want {
		t.Errorf("toast = %q, want %q", r.notice, want)
	}

	if n := r.pushCalls(); n != 0 {
		t.Errorf("git pushed %d times for a worktree already being pushed", n)
	}

	if got := r.remoteRefs(t); got != "refs/heads/main" {
		t.Errorf("the remote holds %q, want only main", got)
	}
}

func TestPushNothingToSend(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		setup func(t *testing.T, r *pushRig)
		want  pushState
	}{
		{"the remote has the branch", func(t *testing.T, r *pushRig) { r.publish(t) }, pushUpToDate},
		{"the remote lacks the branch", func(t *testing.T, r *pushRig) {
			r.repo.reset(r.branchTip("main"))
		}, pushNothing},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			r := newPushRig(t)
			tt.setup(t, r)

			if got := r.askNotice(t, tt.want); !strings.Contains(got, "feat/x") || !strings.Contains(got, "origin") {
				t.Errorf("notice %q lacks the branch or the remote", got)
			}

			if n := r.pushCalls(); n != 0 {
				t.Errorf("git pushed %d times", n)
			}
		})
	}
}

func TestPushRejected(t *testing.T) {
	t.Parallel()

	r := newPushRig(t)

	// The remote's feat/x holds work this branch lacks.
	remoteWork := fakeOID("remote work")
	r.origin.setBranch("feat/x", remoteWork)

	r.askOpen(t)
	r.churn(t, press(r.App, keyEnter))

	if r.noticeLevel() != levelError || !strings.Contains(r.notice, git.ErrRejected.Error()) {
		t.Errorf("toast = %q at level %v, want the rejection at the error level", r.notice, r.noticeLevel())
	}

	for _, leak := range []string{pushRemoteURL, "remote.git", "github.com", "https://"} {
		if strings.Contains(r.notice, leak) {
			t.Errorf("toast %q holds the remote %q", r.notice, leak)
		}
	}

	if got := r.pushes(); !slices.Equal(got, []string{"feat/x"}) {
		t.Errorf("pushed %q, want one attempt at feat/x", got)
	}

	var tip string

	r.fake.edit(func() { tip = r.origin.branches["feat/x"] })

	if tip != remoteWork {
		t.Errorf("remote feat/x = %s after a rejected push, want %s", tip, remoteWork)
	}
}

func TestPushDetached(t *testing.T) {
	t.Parallel()

	r := newPushRig(t)
	r.repo.detach()

	if got := r.askNotice(t, pushDetached); got != noticePushDetached {
		t.Errorf("notice = %q, want %q", got, noticePushDetached)
	}

	if n := r.pushCalls(); n != 0 {
		t.Errorf("git pushed %d times", n)
	}
}

func TestPushNotRepo(t *testing.T) {
	t.Parallel()

	r := newPushRig(t)
	openIn(t, r.App, core.NewTab{Kind: core.KindShell, Cwd: t.TempDir()})

	if got := r.askNotice(t, pushNotRepo); got != noticePushNotRepo {
		t.Errorf("notice = %q, want %q", got, noticePushNotRepo)
	}
}

func TestPushUnborn(t *testing.T) {
	t.Parallel()

	r := newPushRig(t)

	empty := r.fake.repo(t, "empty")
	openIn(t, r.App, core.NewTab{Kind: core.KindShell, Cwd: empty.Root()})

	if got := r.askNotice(t, pushUnborn); !strings.Contains(got, "main") {
		t.Errorf("notice = %q, want the branch named", got)
	}
}

func TestPushNoRemote(t *testing.T) {
	t.Parallel()

	r := newPushRig(t)
	solo := pushOther(t, r.fake, "solo", "main")
	openIn(t, r.App, core.NewTab{Kind: core.KindShell, Cwd: solo})

	if got := r.askNotice(t, pushNoRemote); !strings.Contains(got, "main") {
		t.Errorf("notice = %q, want the branch named", got)
	}
}

func TestPushRunsOffTheLoop(t *testing.T) {
	t.Parallel()

	r := newPushRig(t)

	calls := r.gitCalls()
	cmd := r.leaderP()

	if got := r.gitCalls(); got != calls {
		t.Fatalf("leader g P ran git %d times on the update loop", got-calls)
	}

	msgs := flatten(cmd)
	if countOf[pushResolvedMsg](msgs) != 1 {
		t.Fatalf("messages = %v, want one pushResolvedMsg", msgs)
	}

	for _, m := range msgs {
		r.step(m)
	}

	if r.confirm == nil {
		t.Fatal("no confirm")
	}

	calls = r.gitCalls()
	cmd = press(r.App, keyEnter)

	if got := r.gitCalls(); got != calls || r.pushCalls() != 0 {
		t.Fatalf("enter ran git %d times on the update loop", got-calls)
	}

	if msgs = flatten(cmd); countOf[pushedMsg](msgs) != 1 {
		t.Fatalf("messages = %v, want one pushedMsg", msgs)
	}
}

func TestPushStopsWithTheApp(t *testing.T) {
	t.Parallel()

	t.Run("while resolving", func(t *testing.T) {
		t.Parallel()

		r := newPushRig(t)
		r.cancel()

		r.ask(t)

		if r.confirm != nil || r.notice != "" {
			t.Errorf("confirm %v, toast %q after the app's context ended, want nothing", r.confirm != nil, r.notice)
		}
	})

	t.Run("while pushing", func(t *testing.T) {
		t.Parallel()

		r := newPushRig(t)
		r.askOpen(t)
		r.cancel()

		r.churn(t, press(r.App, keyEnter))

		if r.notice != "" || r.pushCalls() != 0 {
			t.Errorf("toast %q, %d pushes after the app's context ended, want none", r.notice, r.pushCalls())
		}

		if len(r.pushing) != 0 {
			t.Errorf("%v still marked as being pushed after the app's context ended", r.pushing)
		}

		if got := r.remoteRefs(t); got != "refs/heads/main" {
			t.Errorf("the remote holds %q, want only main", got)
		}
	})
}

func TestPushAnswerIsDroppedWhenStale(t *testing.T) {
	t.Parallel()

	t.Run("a newer request", func(t *testing.T) {
		t.Parallel()

		r := newPushRig(t)

		first := flatten(r.leaderP())
		second := flatten(r.leaderP())

		for _, m := range first {
			r.step(m)
		}

		if r.confirm != nil {
			t.Fatal("the first answer opened a confirm after a newer request")
		}

		for _, m := range second {
			r.step(m)
		}

		if r.confirm == nil || r.confirm.push == nil {
			t.Fatal("the newer answer did not open the push confirm")
		}
	})

	t.Run("the focus moved", func(t *testing.T) {
		t.Parallel()

		r := newPushRig(t)
		msgs := flatten(r.leaderP())

		openIn(t, r.App, core.OpenPane{Kind: core.KindShell, Cwd: r.dir, Split: core.SplitVertical, Area: r.bodyArea()})

		for _, m := range msgs {
			r.step(m)
		}

		if r.confirm != nil {
			t.Error("an answer for a pane that lost focus opened a confirm")
		}
	})

	t.Run("another confirm is open", func(t *testing.T) {
		t.Parallel()

		r := newPushRig(t)
		msgs := flatten(r.leaderP())

		press(r.App, keyLeader, keyRune('x'))

		if r.confirm == nil {
			t.Fatal("no close confirm")
		}

		for _, m := range msgs {
			r.step(m)
		}

		if r.confirm == nil || r.confirm.push != nil {
			t.Error("the push answer replaced the confirm that was open")
		}
	})
}

func TestPushCommand(t *testing.T) {
	t.Parallel()

	a := newTestApp(t)

	c, ok := a.reg.get("git.push")
	if !ok || c.title != "Push branch" || c.icon != iconPush {
		t.Fatalf("git.push = %+v, %v; want Push branch with the push icon", c, ok)
	}

	if key, _ := a.keymap.keyOf("git.push"); key != "g P" || a.keymap.groupOf("git.push") != "Git" {
		t.Errorf("git.push bound to %q in group %q, want g P in Git", key, a.keymap.groupOf("git.push"))
	}

	if iconPush != "\uF403" {
		t.Errorf("iconPush = %q, want U+F403", iconPush)
	}
}

func TestPushKey(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		key     tea.KeyPressMsg
		confirm bool
		notice  string
	}{
		{"P", keyRune('P'), true, ""},
		{"shift+p without text", tea.KeyPressMsg{Code: 'p', ShiftedCode: 'P', Mod: tea.ModShift}, true, ""},
		{"p", keyRune('p'), false, "ctrl+space g p is not bound"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			r := newPushRig(t)

			press(r.App, keyLeader, keyRune('g'))
			r.churnMsgs(t, press(r.App, tt.key))

			if got := r.confirm != nil && r.confirm.push != nil; got != tt.confirm {
				t.Errorf("push confirm open = %v, want %v; notice = %q", got, tt.confirm, r.notice)
			}

			if r.notice != tt.notice {
				t.Errorf("notice = %q, want %q", r.notice, tt.notice)
			}

			if n := r.pushCalls(); n != 0 {
				t.Errorf("git pushed %d times without an answer", n)
			}
		})
	}
}

func TestCommitCount(t *testing.T) {
	t.Parallel()

	for n, want := range map[int]string{1: "1 commit", 2: "2 commits", 12: "12 commits"} {
		if got := commitCount(n); got != want {
			t.Errorf("commitCount(%d) = %q, want %q", n, got, want)
		}
	}
}

func TestPushConfirmFitsANarrowScreen(t *testing.T) {
	t.Parallel()

	r := newPushRig(t)
	r.askOpen(t)

	for _, w := range []int{30, 12, 5} {
		blank := strings.TrimSuffix(strings.Repeat(strings.Repeat(" ", w)+"\n", 16), "\n")

		for y, line := range strings.Split(ansi.Strip(overlayConfirm(nil, blank, w, 16, *r.confirm, true, r.styles)), "\n") {
			if got := ansi.StringWidth(line); got > w {
				t.Errorf("width %d row %d is %d cells, want at most %d", w, y, got, w)
			}
		}
	}
}

func TestRemoteDisplay(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name, raw, want string
	}{
		{"https", "https://github.com/o/r.git", "github.com/o/r"},
		{"scp-like", "git@github.com:o/r.git", "github.com/o/r"},
		{"ssh url", "ssh://git@ghe.example.com/o/r", "ghe.example.com/o/r"},
		{"a token in the userinfo", "https://x-access-token:ghp_secret@github.com/o/r.git", "github.com/o/r"},
		{"a token in the query", "https://github.com/o/r.git?token=abc", "github.com/o/r"},
		{"userinfo, query and fragment of a deeper path", "https://tok:secret@git.example.com/a/b/c.git?token=abc#frag", "https://git.example.com/a/b/c"},
		{"scp-like with a deeper path", "git@git.example.com:a/b/c.git", "git.example.com:a/b/c"},
		{"a local path in the home directory", "/home/me/code/remote.git", "~/code/remote.git"},
		{"a local path elsewhere", "/srv/git/remote.git", "/srv/git/remote.git"},
		{"a relative path", "../remote.git", "../remote.git"},
		{"a file URL", "file:///home/me/remote.git", "~/remote.git"},
		{"control characters", "/srv/a\x1b[31mb", "/srv/a[31mb"},
		{"an unreadable URL", "https://user:secret@host:notaport/x", "(unreadable url)"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := remoteDisplay(tt.raw, "/home/me")
			if got != tt.want {
				t.Errorf("remoteDisplay(%q) = %q, want %q", tt.raw, got, tt.want)
			}

			if strings.Contains(got, "secret") || strings.Contains(got, "ghp_") || strings.Contains(got, "token=") {
				t.Errorf("remoteDisplay(%q) = %q shows a credential", tt.raw, got)
			}
		})
	}
}

func TestPushConfirmDrawsRepositoryTextCleaned(t *testing.T) {
	t.Parallel()

	a := newTestApp(t)
	tgt := pushTarget{
		project: "pro\x1b[31mj", branch: "feat/\u200bx", remote: "ori\x07gin", urls: []string{"github.com/o/r", "host/\x1b[31mo/r"},
		commits: 1, short: "abc1234", subject: "fix\x1b[2Jthe\u200b parser",
	}

	text := strings.Join(pushBox(tgt, 60, true, a.styles), "\n")

	for _, bad := range []string{"\x1b[31m", "\x1b[2J", "\u200b", "\x07"} {
		if strings.Contains(ansi.Strip(text), bad) {
			t.Errorf("the box holds %q", bad)
		}
	}

	for _, want := range []string{"pro[31mj", "feat/x", "origin", "host/[31mo/r", "abc1234 fix[2Jthe parser", "1 commit,"} {
		if !strings.Contains(ansi.Strip(text), want) {
			t.Errorf("the box lacks %q:\n%s", want, ansi.Strip(text))
		}
	}
}

func TestPushFromThePalette(t *testing.T) {
	t.Parallel()

	r := newPushRig(t)

	press(r.App, keyLeader, keyRune(':'))

	if r.overlay == nil {
		t.Fatal("the palette did not open")
	}

	r.churn(t, r.step(palette.ChosenMsg{ID: "git.push"}))

	if r.confirm == nil || r.confirm.push == nil {
		t.Errorf("no push confirm after choosing Push branch; notice = %q", r.notice)
	}
}
