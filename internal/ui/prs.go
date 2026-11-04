package ui

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"reflect"
	"slices"
	"strings"
	"sync"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/mhersson/aim/internal/core"
	"github.com/mhersson/aim/internal/git"
	"github.com/mhersson/aim/internal/github"
)

const (
	// prResolveTimeout bounds finding the repository of a branch's push remote;
	// it may run ssh.
	prResolveTimeout = 10 * time.Second
	prLookupTimeout  = 15 * time.Second
	prOpenState      = "OPEN"
)

// prSource is the poller as the app uses it.
type prSource interface {
	Set([]github.Watch)
	Updates() <-chan github.Snapshot
	Refresh(github.Repo)
}

// PRLookup finds the pull request of a branch: the newest open one, else the
// newest of any state. A branch with none is github.ErrNoPR.
type PRLookup func(ctx context.Context, r github.Repo, branch string) (github.PR, error)

// remoteResolver turns a remote URL into a GitHub repository.
type remoteResolver interface {
	Resolve(ctx context.Context, rawURL string) (github.Repo, error)
}

// prSnapshotMsg carries one poll of one repository.
type prSnapshotMsg struct{ github.Snapshot }

// prResolvedMsg is the answer of resolving the repository of a pane's branch.
type prResolvedMsg struct {
	id   core.PaneID
	key  prKey
	repo github.Repo
	err  error
}

// prBranchMsg is the answer of a branch lookup.
type prBranchMsg struct {
	repo   github.Repo
	branch string
	pr     github.PR
	err    error
}

// prKey is what a pane's repository was resolved for.
type prKey struct{ cwd, branch string }

// prPane is one pane's repository.
type prPane struct {
	key      prKey       // the latest cwd and branch, answered or not
	repo     github.Repo // the repository of the latest answer
	answered prKey       // the cwd and branch the latest answer was for
	ok       bool        // repo is set; a remote that is not on GitHub clears it
	failed   bool        // the latest resolution failed; a head result retries it
}

// prBranch is a branch of a repository.
type prBranch struct {
	repo   github.Repo
	branch string
}

type prState struct {
	src     prSource // nil turns every pull request feature off
	lookup  PRLookup // nil means no lookups
	resolve func(ctx context.Context, cwd, branch string) (github.Repo, error)

	panes  map[core.PaneID]*prPane
	polled map[github.Repo]bool // repositories with a successful poll
	looked map[prBranch]bool    // branches whose lookup ran and found them no open PR since
	shown  map[string]string    // the poll error last shown, per repository
	set    []github.Watch       // what the source was last given
	sent   bool                 // the source was given a set
	ver    uint64               // the store version the last sync read
	dirty  bool                 // a head result arrived since the last sync
}

func newPRState(src prSource, lookup PRLookup, g Git) prState {
	return prState{
		src: src, lookup: lookup,
		resolve: newPRResolver(g, github.NewResolver(github.ResolverOptions{})).resolve,
		panes:   map[core.PaneID]*prPane{},
		polled:  map[github.Repo]bool{},
		looked:  map[prBranch]bool{},
		shown:   map[string]string{},
	}
}

// prRemote is a worktree's root and the name of one of its remotes.
type prRemote struct{ root, remote string }

// prResolver finds the GitHub repository of a branch's push remote. It is safe
// for concurrent use.
type prResolver struct {
	git Git
	gh  remoteResolver

	mu    sync.Mutex
	known map[prRemote]resolvedRemote
}

// resolvedRemote is a remote's URL and the repository it resolved to; a changed
// URL is resolved again.
type resolvedRemote struct {
	url  string
	repo github.Repo
}

func newPRResolver(g Git, gh remoteResolver) *prResolver {
	return &prResolver{git: g, gh: gh, known: map[prRemote]resolvedRemote{}}
}

// resolve returns the repository the push remote of branch in cwd's worktree
// points at. A remote that is not on GitHub is github.ErrNotGitHub, and one that
// does not exist is git.ErrNoRemote.
func (r *prResolver) resolve(ctx context.Context, cwd, branch string) (github.Repo, error) {
	wt, err := r.git.Open(ctx, cwd)
	if err != nil {
		return github.Repo{}, err
	}

	remote, err := r.git.PushRemote(ctx, wt, branch)
	if err != nil {
		return github.Repo{}, fmt.Errorf("push remote of %s: %w", branch, err)
	}

	url, err := r.git.RemoteURL(ctx, wt, remote)
	if err != nil {
		return github.Repo{}, fmt.Errorf("remote %s: %w", remote, err)
	}

	k := prRemote{root: wt.Root, remote: remote}

	r.mu.Lock()
	c, ok := r.known[k]
	r.mu.Unlock()

	if ok && c.url == url {
		return c.repo, nil
	}

	repo, err := r.gh.Resolve(ctx, url)
	if err != nil {
		return github.Repo{}, fmt.Errorf("resolve remote %s: %w", remote, err)
	}

	r.mu.Lock()
	r.known[k] = resolvedRemote{url: url, repo: repo}
	r.mu.Unlock()

	return repo, nil
}

// waitPRs reads the next poll, or returns nothing once the source's channel
// closes or the app's context ends. The update loop re-arms it.
func (a *App) waitPRs() tea.Cmd {
	if a.prs.src == nil {
		return nil
	}

	ch := a.prs.src.Updates()

	return func() tea.Msg {
		select {
		case s, ok := <-ch:
			if !ok {
				return nil
			}

			return prSnapshotMsg{Snapshot: s}
		case <-a.ctx.Done():
			return nil
		}
	}
}

// prProbed notes a head result for a pane: the next sync reads its branch, and a
// resolution that failed is tried again.
func (a *App) prProbed(id core.PaneID) {
	if a.prs.src == nil {
		return
	}

	a.prs.dirty = true

	if pp := a.prs.panes[id]; pp != nil && pp.failed {
		pp.key = prKey{}
	}
}

// prSync resolves the repository of every agent and of the focused pane whose
// branch changed, and keeps the source's watch set current. It reads the head
// probe's results and runs only after a head result or a store change.
func (a *App) prSync() tea.Cmd {
	p := &a.prs
	if p.src == nil || !p.dirty && p.ver == a.storeVer {
		return nil
	}

	p.dirty, p.ver = false, a.storeVer

	st := a.store.State()

	ids := st.AgentPanes()
	if f := st.Tab().Focus; f != "" && !slices.Contains(ids, f) {
		ids = append(ids, f)
	}

	for id := range p.panes {
		if !slices.Contains(ids, id) {
			delete(p.panes, id)
		}
	}

	var cmds []tea.Cmd

	for _, id := range ids {
		m, ok := st.Panes[id]
		rp := a.rail.panes[id]

		// Nothing is known of a pane whose head was not probed for its followed directory.
		if !ok || rp == nil || !rp.probe.known || rp.probe.headCwd != m.FollowDir() {
			continue
		}

		switch pr := rp.probe; {
		case !pr.repo:
			delete(p.panes, id)
		case pr.detached || pr.branch == "":
			// With no branch the repository stays as it was.
		default:
			key := prKey{cwd: pr.headCwd, branch: pr.branch}

			pp := p.panes[id]
			if pp == nil {
				pp = &prPane{}
				p.panes[id] = pp
			}

			if pp.key != key {
				pp.key, pp.failed = key, false
				cmds = append(cmds, a.prResolveCmd(id, key))
			}
		}
	}

	a.prSetWatches(st)

	return tea.Batch(cmds...)
}

// prWatches is every watched agent's repository and the focused pane's, with
// the open pull request of each pane's branch visible, and the repository of the
// pull request the active tab's drawer shows, with that pull request visible and
// focused.
func (a *App) prWatches(st core.State) []github.Watch {
	visible := map[github.Repo][]int{}
	focused := map[github.Repo]int{}

	for _, pp := range a.prs.panes {
		if !pp.ok {
			continue
		}

		nums := visible[pp.repo]
		if pr, ok := st.OpenPRFor(pp.repo.String(), pp.key.branch); ok {
			nums = append(nums, pr.Number)
		}

		visible[pp.repo] = nums
	}

	if rv := st.Tab().Review; rv != nil && rv.PR != nil {
		if repo, ok := repoOfKey(rv.PR.Repo); ok {
			visible[repo] = append(visible[repo], rv.PR.Number)
			focused[repo] = rv.PR.Number
		}
	}

	// The merge box's pull request is the one whose checks it warns with.
	if m := a.merge; m != nil {
		visible[m.repo] = append(visible[m.repo], m.pr.Number)
		focused[m.repo] = m.pr.Number
	}

	// The open checks overlay's pull request is the one whose checks are read.
	if c := a.checks; c != nil {
		if repo, ok := repoOfKey(c.repo); ok {
			visible[repo] = append(visible[repo], c.number)
			focused[repo] = c.number
		}
	}

	// The open conversation's pull request is held in the watch set, so its
	// update time keeps coming.
	if c := a.conv; c != nil {
		if repo, ok := repoOfKey(c.repo); ok {
			visible[repo] = append(visible[repo], c.number)
		}
	}

	ws := make([]github.Watch, 0, len(visible))

	for repo, nums := range visible {
		slices.Sort(nums)
		ws = append(ws, github.Watch{Repo: repo, Visible: slices.Compact(nums), Focused: focused[repo]})
	}

	slices.SortFunc(ws, func(x, y github.Watch) int { return strings.Compare(x.Repo.String(), y.Repo.String()) })

	return ws
}

// prSetWatches gives the source the current watch set when it differs from the
// last one it was given.
func (a *App) prSetWatches(st core.State) {
	p := &a.prs
	ws := a.prWatches(st)

	same := slices.EqualFunc(ws, p.set, func(x, y github.Watch) bool {
		return x.Repo == y.Repo && x.Focused == y.Focused && slices.Equal(x.Visible, y.Visible)
	})
	if same && (p.sent || len(ws) == 0) {
		return
	}

	p.set, p.sent = ws, true
	p.src.Set(slices.Clone(ws))
}

// prResolveCmd resolves the repository of a pane's branch off the loop, under
// the app's context.
func (a *App) prResolveCmd(id core.PaneID, key prKey) tea.Cmd {
	resolve := a.prs.resolve

	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(a.ctx, prResolveTimeout)
		defer cancel()

		repo, err := resolve(ctx, key.cwd, key.branch)

		return prResolvedMsg{id: id, key: key, repo: repo, err: err}
	}
}

// onPRResolved keeps the repository a pane's branch resolved to. An answer for a
// branch the pane has left is dropped, a repository off GitHub is left alone
// without a word, and a failure is retried at the pane's next head result.
func (a *App) onPRResolved(msg prResolvedMsg) tea.Cmd {
	pp := a.prs.panes[msg.id]
	if pp == nil || pp.key != msg.key {
		return nil
	}

	a.prs.dirty = true

	switch {
	case msg.err == nil:
		pp.repo, pp.answered, pp.ok, pp.failed = msg.repo, msg.key, true, false
	case errors.Is(msg.err, context.Canceled):
		return nil
	case errors.Is(msg.err, github.ErrNotGitHub), errors.Is(msg.err, git.ErrNoRemote), errors.Is(msg.err, git.ErrNotRepo), errors.Is(msg.err, git.ErrBadBranch):
		pp.ok, pp.failed = false, false

		return nil
	default:
		a.log.Debug("resolve pull request repository", "pane", msg.id, "err", msg.err)

		pp.failed = true

		return nil
	}

	// A branch of a repository polled already is looked up when first seen,
	// without waiting for the next poll.
	if a.prs.polled[pp.repo] {
		return a.prLookupCmd(a.store.State(), pp.repo, pp.key.branch)
	}

	return nil
}

// onPRSnapshot puts a poll into the store, shows a new error once and looks up
// the branches whose pull request is not in the poll.
func (a *App) onPRSnapshot(s github.Snapshot) tea.Cmd {
	key := s.Repo.String()
	cur := a.store.State().PRs[key]

	if s.Err != nil {
		if errors.Is(s.Err, context.Canceled) {
			return nil
		}

		// The last good poll stays on show while the repository fails.
		reason := prReason(s.Err)
		a.setRepoPRs(cur, core.SetRepoPRs{
			Repo: key, DefaultBranch: cur.DefaultBranch, Open: cur.Open, Err: reason,
			Methods: cur.Methods, DeleteBranchOnMerge: cur.DeleteBranchOnMerge, Focused: cur.Focused,
		})

		if a.prs.shown[key] != reason {
			a.prs.shown[key] = reason
			a.notice = fmt.Sprintf("pull requests for %s: %s", key, reason)
		}

		return nil
	}

	open := make([]core.PRInfo, len(s.PRs))
	for i, pr := range s.PRs {
		open[i] = prInfoOf(pr)
	}

	var methods []string
	for _, m := range s.Info.Methods {
		methods = append(methods, string(m))
	}

	a.setRepoPRs(cur, core.SetRepoPRs{
		Repo: key, DefaultBranch: s.Info.DefaultBranch, Open: open,
		Methods: methods, DeleteBranchOnMerge: s.Info.DeleteBranchOnMerge, Focused: focusedChecksOf(s.Focused),
	})
	delete(a.prs.shown, key)
	a.prs.polled[s.Repo] = true

	st := a.store.State()

	var (
		cmds []tea.Cmd
		done = map[string]bool{}
	)

	for _, id := range slices.Sorted(maps.Keys(a.prs.panes)) {
		pp := a.prs.panes[id]
		if !pp.ok || pp.repo != s.Repo || done[pp.key.branch] {
			continue
		}

		done[pp.key.branch] = true
		cmds = append(cmds, a.prLookupCmd(st, s.Repo, pp.key.branch))
	}

	if cmd := a.convPoll(st, key); cmd != nil {
		cmds = append(cmds, cmd)
	}

	return tea.Batch(cmds...)
}

// setRepoPRs dispatches a poll's result unless the store holds it already.
func (a *App) setRepoPRs(cur core.RepoPRs, set core.SetRepoPRs) {
	if cur.DefaultBranch == set.DefaultBranch && cur.Err == set.Err && slices.Equal(cur.Open, set.Open) &&
		slices.Equal(cur.Methods, set.Methods) && cur.DeleteBranchOnMerge == set.DeleteBranchOnMerge &&
		reflect.DeepEqual(cur.Focused, set.Focused) {
		return
	}

	_ = a.dispatch(set)
}

// focusedChecksOf maps the poll's checks of the focused pull request.
func focusedChecksOf(f *github.FocusedChecks) *core.PRChecks {
	if f == nil {
		return nil
	}

	checks := make([]core.CheckInfo, len(f.Checks))
	for i, c := range f.Checks {
		checks[i] = core.CheckInfo{
			Name: c.Name, URL: c.URL, State: string(c.State),
			StartedAt: c.StartedAt, CompletedAt: c.CompletedAt, ID: c.ID, App: c.App,
		}
	}

	out := &core.PRChecks{Number: f.Number, HeadOID: f.HeadOID, Checks: checks}
	if f.Err != nil {
		out.Err = prReason(f.Err)
	}

	return out
}

// prLookupCmd looks up the pull request of a branch that the poll in st lists no
// open one for, unless it was looked up since it last had one: a branch the poll
// lists is forgotten, so the poll that loses it asks again. It returns nil when
// no lookup is due.
func (a *App) prLookupCmd(st core.State, repo github.Repo, branch string) tea.Cmd {
	p := &a.prs
	if p.lookup == nil {
		return nil
	}

	k := prBranch{repo: repo, branch: branch}

	if _, open := st.OpenPRFor(repo.String(), branch); open {
		delete(p.looked, k)

		return nil
	}

	if p.looked[k] {
		return nil
	}

	p.looked[k] = true
	lookup := p.lookup

	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(a.ctx, prLookupTimeout)
		defer cancel()

		pr, err := lookup(ctx, repo, branch)

		return prBranchMsg{repo: repo, branch: branch, pr: pr, err: err}
	}
}

// onPRBranch records what a lookup found. A failed lookup is asked again at the
// next poll.
func (a *App) onPRBranch(msg prBranchMsg) {
	repo := msg.repo.String()

	switch {
	case msg.err == nil && msg.pr.State == prOpenState:
		// Open PRs come from the poll alone; the branch stays looked up until the
		// poll lists one and later loses it.
	case msg.err == nil:
		_ = a.dispatch(core.SetBranchPR{Repo: repo, Branch: msg.branch, PR: prInfoOf(msg.pr), Found: true})
	case errors.Is(msg.err, github.ErrNoPR):
		if _, ok := a.store.State().PRs[repo].Branch[msg.branch]; ok {
			_ = a.dispatch(core.SetBranchPR{Repo: repo, Branch: msg.branch})
		}
	default:
		a.log.Debug("pull request lookup", "repo", repo, "branch", msg.branch, "err", msg.err)

		delete(a.prs.looked, prBranch{repo: msg.repo, branch: msg.branch})
	}
}

// prReason is a poll error as one line of text. A missing token gives gh's own
// instruction.
func prReason(err error) string {
	msg := err.Error()

	if errors.Is(err, github.ErrNoToken) {
		if _, rest, ok := strings.Cut(msg, "run gh auth login"); ok {
			msg = "run gh auth login" + rest
		}
	}

	return printable(msg)
}

func prInfoOf(pr github.PR) core.PRInfo {
	return core.PRInfo{
		ID: pr.ID, Number: pr.Number, Title: pr.Title, URL: pr.URL, Author: pr.Author, State: pr.State,
		HeadBranch: pr.HeadBranch, HeadOID: pr.HeadOID, BaseBranch: pr.BaseBranch,
		ReviewDecision: pr.ReviewDecision, MergeState: pr.MergeState,
		Draft: pr.Draft, CrossRepo: pr.CrossRepo, ViewerAuthored: pr.ViewerAuthored,
		ChecksTotal: pr.Checks.Total, ChecksDone: pr.Checks.Done, ChecksFailed: pr.Checks.Failed,
		UpdatedAt: pr.UpdatedAt,
	}
}
