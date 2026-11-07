package ui

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"path/filepath"
	"slices"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/mhersson/aim/internal/core"
	"github.com/mhersson/aim/internal/git"
	"github.com/mhersson/aim/internal/github"
)

const (
	// cleanupCheckTimeout bounds finding what a cleanup would remove.
	cleanupCheckTimeout = 10 * time.Second
	// cleanupRunTimeout bounds a cleanup.
	cleanupRunTimeout = 30 * time.Second
	// maxCleanupWidth caps the cleanup box.
	maxCleanupWidth = 78
)

const noticeNothingToClean = "nothing to clean up"

// noticeNoCleanupWorktree answers a clean-up command when no worktree has branch.
func noticeNoCleanupWorktree(branch string) string {
	return "no worktree has " + printable(branch)
}

// cleanupFound is the worktree that has a merged branch checked out and what a
// cleanup would remove with it. managed is false for the main checkout and for a
// worktree outside AIM's roots: those are found but never removed, nor is
// a locked one.
type cleanupFound struct {
	main, path, branch, tip string
	managed, locked         bool
	changes, ahead          int
	headErr                 error // git.ErrNoCommit: the merged head is not in this repository
}

// cleanupPane is a pane that works in the worktree. keeps is set when its cwd is
// inside: a shell there, or an agent started there. An agent that only cd'd in
// does not keep the worktree.
type cleanupPane struct {
	name  string // the agent's card name; "" for a shell
	agent bool
	state core.AgentState
	keeps bool
}

// cleanupTarget is what the cleanup confirm names and enter acts on. The
// worktree, the panes that keep it and the branch tip are fixed when the box
// opens; remove and del are the two boxes, which w and b toggle.
type cleanupTarget struct {
	repo        github.Repo
	number      int
	title, head string
	home        string
	found       cleanupFound
	inUse       []cleanupPane // the panes that keep the worktree; with any, nothing can go
	remove, del bool
}

// removable reports whether no pane keeps the worktree.
func (t cleanupTarget) removable() bool { return len(t.inUse) == 0 }

// canDelete reports whether the branch holds nothing the merged head does not:
// its tip is the head or behind it.
func (t cleanupTarget) canDelete() bool {
	return t.found.headErr == nil && t.found.ahead == 0 && t.found.tip != ""
}

// toggle handles w and b, and reports whether key was one. The worktree box
// only changes while no pane keeps the worktree, and the branch box only while
// the branch can go and the worktree box is on.
func (t *cleanupTarget) toggle(key string) bool {
	switch key {
	case "w":
		if t.removable() {
			t.remove = !t.remove
		}
	case "b":
		if t.remove && t.canDelete() {
			t.del = !t.del
		}
	default:
		return false
	}

	return true
}

// path is the worktree as the box and the toasts write it.
func (t cleanupTarget) path() string { return shortPath(t.found.path, t.found.main, t.home) }

// keptReason is why the branch is not offered.
func (t cleanupTarget) keptReason() string {
	if t.found.headErr != nil {
		return fmt.Sprintf("%s's head %s is not in this repository", prNumberText(t.number), shortOID(t.head))
	}

	return fmt.Sprintf("it has %s the PR did not merge", plural(t.found.ahead, "commit"))
}

// keptBy says that panes keep the worktree at path.
func keptBy(path string, panes []cleanupPane) string { return "kept " + path + ": " + inIt(panes) }

// inIt says that panes are in the worktree; the box's row under the worktree's
// leaves out the path that row already shows.
func inIt(panes []cleanupPane) string {
	verb := "are"
	if len(panes) == 1 {
		verb = "is"
	}

	return paneList(panes) + " " + verb + " in it"
}

// paneList names panes: agents by card name in tab order, then the shells counted.
func paneList(panes []cleanupPane) string {
	var parts []string

	shells := 0

	for _, p := range panes {
		if !p.agent {
			shells++

			continue
		}

		parts = append(parts, printable(p.name))
	}

	if shells > 0 {
		parts = append(parts, plural(shells, "shell"))
	}

	switch len(parts) {
	case 0:
		return ""
	case 1:
		return parts[0]
	}

	return strings.Join(parts[:len(parts)-1], ", ") + " and " + parts[len(parts)-1]
}

// shortOID abbreviates a commit id.
func shortOID(oid string) string { return oid[:min(shortOIDLen, len(oid))] }

// plural writes n units, with an s unless n is 1.
func plural(n int, unit string) string {
	if n == 1 {
		return "1 " + unit
	}

	return fmt.Sprintf("%d %ss", n, unit)
}

// cleanupFrom says what asked for a cleanup.
type cleanupFrom uint8

const (
	fromMerge cleanupFrom = iota
	fromCommand
)

// cleanupFoundMsg answers a cleanup check.
type cleanupFoundMsg struct {
	seq   int
	from  cleanupFrom
	pane  core.PaneID // the focused pane when asked
	repo  github.Repo
	pr    core.PRInfo
	head  string
	found cleanupFound
	ok    bool
	err   error
}

// cleanedMsg answers a cleanup run. kept is only the reason found while running
// (the branch moved); the reasons the box already showed come from keptReason.
type cleanedMsg struct {
	target           cleanupTarget
	removed, deleted bool
	kept             string
	err, delErr      error
	pruneErr         error // refs that could not be deleted; logged, never shown
}

// findCleanup finds the worktree of root's repository that has branch checked
// out, with what a cleanup of it would remove measured against head, the merged
// commit. ok is false when no worktree has the branch.
func findCleanup(ctx context.Context, g Git, root, branch, head string) (found cleanupFound, ok bool, err error) {
	if branch == "" {
		return cleanupFound{}, false, nil
	}

	repo, err := g.Open(ctx, root)
	if err != nil {
		return cleanupFound{}, false, err
	}

	list, err := g.ListWorktrees(ctx, repo)
	if err != nil {
		return cleanupFound{}, false, err
	}

	i := slices.IndexFunc(list, func(w git.Worktree) bool { return w.Branch == branch && !w.Bare && !w.Prunable })
	if i < 0 {
		return cleanupFound{}, false, nil
	}

	wt := list[i]

	main, merr := repo.MainRoot()
	if merr != nil {
		main = repo.CommonDir
	}

	found = cleanupFound{main: main, path: wt.Path, branch: branch}
	if wt.Main || !git.ManagedWorktree(repo, wt.Path) {
		return found, true, nil
	}

	found.managed, found.locked = true, wt.Locked
	if found.locked {
		return found, true, nil
	}

	if found.changes, err = g.WorktreeChanges(ctx, wt.Path); err != nil {
		return cleanupFound{}, false, err
	}

	found.tip, found.ahead, err = g.BranchAhead(ctx, repo, branch, head)

	switch {
	case errors.Is(err, git.ErrNoCommit):
		found.headErr = err
	case err != nil:
		return cleanupFound{}, false, err
	}

	return found, true, nil
}

// cleanupCheck finds, off the update loop, the worktree of root with pr's head
// branch checked out. Only the newest check's answer counts.
func (a *App) cleanupCheck(from cleanupFrom, repo github.Repo, pr core.PRInfo, root, head string) tea.Cmd {
	a.cleanupSeq++

	var (
		seq    = a.cleanupSeq
		pane   = a.store.State().Tab().Focus
		appCtx = a.ctx
		g      = a.git
	)

	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(appCtx, cleanupCheckTimeout)
		defer cancel()

		found, ok, err := findCleanup(ctx, g, root, pr.HeadBranch, head)

		return cleanupFoundMsg{seq: seq, from: from, pane: pane, repo: repo, pr: pr, head: head, found: found, ok: ok, err: err}
	}
}

// panesIn lists the panes that work in path or below it, in tab order: those
// whose cwd is there and those whose agent reported it.
func (a *App) panesIn(st core.State, path string) []cleanupPane {
	var out []cleanupPane

	for _, tab := range st.Tabs {
		for _, id := range tab.Panes {
			p, ok := st.Panes[id]
			if !ok || (!inDir(path, p.Cwd) && !inDir(path, p.WorkDir())) {
				continue
			}

			// The cwd is where an agent was started: the hooks' directory never changes it.
			cp := cleanupPane{agent: p.Kind == core.KindAgent, state: p.State, keeps: inDir(path, p.Cwd)}
			if cp.agent {
				cp.name = a.cardName(st, id)
			}

			out = append(out, cp)
		}
	}

	return out
}

// keeping is the panes among panes that keep the worktree.
func keeping(panes []cleanupPane) []cleanupPane {
	var out []cleanupPane

	for _, p := range panes {
		if p.keeps {
			out = append(out, p)
		}
	}

	return out
}

// inDir reports whether p is dir or lies inside it.
func inDir(dir, p string) bool {
	if p == "" {
		return false
	}

	p = filepath.Clean(p)
	_, inside := within(dir, p)

	return p == dir || inside
}

// workingAgent is the first agent among panes that is working or blocked.
func workingAgent(panes []cleanupPane) (cleanupPane, bool) {
	i := slices.IndexFunc(panes, func(p cleanupPane) bool {
		return p.agent && (p.state == core.AgentWorking || p.state == core.AgentBlocked)
	})
	if i < 0 {
		return cleanupPane{}, false
	}

	return panes[i], true
}

// onCleanupFound says why a worktree stays, or opens the cleanup confirm. An
// answer that is not the newest check's, or that arrives under a modal, is
// dropped; a merge's answer is not dropped for the PR list, which the confirm
// opens over.
func (a *App) onCleanupFound(msg cleanupFoundMsg) tea.Cmd { //nolint:unparam // every handler of the update loop's messages has this shape
	modal := a.modalOpen()
	if msg.from == fromMerge && a.prList != nil {
		modal = a.modalAboveList()
	}

	if msg.seq != a.cleanupSeq || modal || (msg.from == fromCommand && msg.pane != a.store.State().Tab().Focus) {
		return nil
	}

	switch {
	case errors.Is(msg.err, context.Canceled):
		return nil
	case msg.err != nil:
		a.log.Warn("clean up check", "pr", msg.pr.Number, "err", msg.err)

		if msg.from == fromCommand {
			a.toast(levelError, "clean up: "+gitReason(msg.err))
		}

		return nil
	case !msg.ok:
		switch {
		case msg.from != fromCommand:
		case msg.pr.HeadBranch == "":
			a.notice = noticeNothingToClean
		default:
			a.notice = noticeNoCleanupWorktree(msg.pr.HeadBranch)
		}

		return nil
	}

	var (
		f    = msg.found
		st   = a.store.State()
		home = a.getenv("HOME")
		path = shortPath(f.path, f.main, home)
	)

	panes := a.panesIn(st, f.path)
	busy, working := workingAgent(panes)

	switch {
	case !f.managed:
		a.cleanupSay(msg, fmt.Sprintf("%s is outside AIM's worktrees, so it stays", path))
	case f.locked:
		a.cleanupSay(msg, fmt.Sprintf("%s is locked, so it stays", path))
	case f.changes > 0:
		a.cleanupSay(msg, fmt.Sprintf("%s has %s, so it stays", path, plural(f.changes, "uncommitted change")))
	case working:
		a.cleanupSay(msg, fmt.Sprintf("%s is still working in %s", busy.name, path))
	default:
		t := cleanupTarget{
			repo: msg.repo, number: msg.pr.Number, title: msg.pr.Title, head: msg.head, home: home,
			found: f, inUse: keeping(panes),
		}
		t.remove = t.removable()
		t.del = t.remove && t.canDelete()

		a.openConfirm(confirm{tab: st.Tab().ID, cleanup: &t, yes: func(a *App) tea.Cmd { return a.cleanupRun(t) }})
	}

	return nil
}

// cleanupSay tells why a worktree stays: a merge's toast gains it while that
// toast still shows, a command's answer is a notice of its own.
func (a *App) cleanupSay(msg cleanupFoundMsg, why string) {
	if msg.from == fromCommand {
		a.notice = why

		return
	}

	if a.noticeLevel() == levelSuccess && strings.HasPrefix(a.notice, fmt.Sprintf("merged %s (", prNumberText(msg.pr.Number))) {
		a.toast(levelSuccess, a.notice+"; "+why)
	}
}

// cleanupRun cleans up exactly what t named and closes no pane. On the loop it
// reads the panes again, since an agent may have started working or a pane
// that keeps the worktree may have opened; off the loop it removes the worktree
// and, when asked, deletes the branch. Every outcome deletes the pull request's
// refs.
func (a *App) cleanupRun(t cleanupTarget) tea.Cmd {
	if !t.remove {
		a.notice = noticeNothingToClean

		return a.prunePRRefs(t)
	}

	panes := a.panesIn(a.store.State(), t.found.path)

	if busy, ok := workingAgent(panes); ok {
		a.notice = fmt.Sprintf("%s is still working in %s", busy.name, t.path())

		return a.prunePRRefs(t)
	}

	if in := keeping(panes); len(in) > 0 {
		a.notice = keptBy(t.path(), in)

		return a.prunePRRefs(t)
	}

	appCtx, g := a.ctx, a.git

	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(appCtx, cleanupRunTimeout)
		defer cancel()

		return cleanupExec(ctx, g, t)
	}
}

// prunePRRefs deletes t's pull request refs off the loop when the clean-up
// itself does nothing. A failure is logged and says nothing to the user.
func (a *App) prunePRRefs(t cleanupTarget) tea.Cmd {
	appCtx, log, g := a.ctx, a.log, a.git

	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(appCtx, cleanupRunTimeout)
		defer cancel()

		repo, err := openMain(ctx, g, t.found.main)
		if err == nil {
			err = g.DeletePRRefs(ctx, repo, t.number)
		}

		if err != nil {
			warnPrune(log, t.number, err)
		}

		return nil
	}
}

// errLeftBranch means the worktree is no longer on the branch the box named.
var errLeftBranch = errors.New("ui: worktree left the branch")

// stillOnBranch checks, before anything is removed, that the worktree is still
// on the branch the box named: commits made after a switch or a detach would be
// lost with it.
func stillOnBranch(ctx context.Context, g Git, repo git.Repo, f cleanupFound) error {
	list, err := g.ListWorktrees(ctx, repo)
	if err != nil {
		return err
	}

	if !slices.ContainsFunc(list, func(w git.Worktree) bool { return w.Path == f.path && w.Branch == f.branch }) {
		return errLeftBranch
	}

	return nil
}

// cleanupExec removes the worktree and deletes the branch, never forced. The
// branch goes only when its tip is still the one the box showed and is not past
// the merged head. The pull request's refs go on every path, and the
// worktree's turn refs after a removal; neither changes what is reported.
func cleanupExec(ctx context.Context, g Git, t cleanupTarget) cleanedMsg {
	msg := cleanedMsg{target: t}

	repo, err := openMain(ctx, g, t.found.main)
	if err != nil {
		msg.err = withContext(ctx, err)

		return msg
	}

	// Turn refs record the root git reports, which the listed path may not spell.
	root := t.found.path
	if wt, werr := g.Open(ctx, t.found.path); werr == nil {
		root = wt.Root
	}

	removed := cleanupRemove(ctx, g, repo, t, &msg)
	msg.pruneErr = pruneRefs(ctx, g, repo, t.number, root, removed)

	return msg
}

// pruneRefs deletes pull request number's refs and, when removed, the turn
// refs of the worktree at root.
func pruneRefs(ctx context.Context, g Git, repo git.Repo, number int, root string, removed bool) error {
	err := g.DeletePRRefs(ctx, repo, number)

	if removed {
		_, perr := g.PruneWorktree(ctx, repo, root)
		err = errors.Join(err, perr)
	}

	return err
}

// cleanupRemove removes the worktree and deletes the branch, and fills msg. It
// reports whether the worktree went.
func cleanupRemove(ctx context.Context, g Git, repo git.Repo, t cleanupTarget, msg *cleanedMsg) bool {
	err := stillOnBranch(ctx, g, repo, t.found)
	if err == nil {
		err = g.RemoveWorktree(ctx, repo, t.found.path)
	}

	if err != nil {
		msg.err = withContext(ctx, err)

		return false
	}

	msg.removed = true

	if !t.del || !t.canDelete() {
		return true
	}

	branch := t.found.branch

	tip, ahead, err := g.BranchAhead(ctx, repo, branch, t.head)

	switch {
	case err != nil:
		msg.delErr = withContext(ctx, err)
	case tip != t.found.tip || ahead > 0:
		msg.kept = movedReason
	default:
		switch err := g.DeleteBranch(ctx, repo, branch, tip); {
		case errors.Is(err, git.ErrBranchMoved):
			msg.kept = movedReason
		case err != nil:
			msg.delErr = withContext(ctx, err)
		default:
			msg.deleted = true
		}
	}

	return true
}

const movedReason = "it moved since you asked"

// openMain opens the repository from its main checkout; a bare repository has
// no work tree to open, so its git dir stands in.
func openMain(ctx context.Context, g Git, main string) (git.Repo, error) {
	repo, err := g.Open(ctx, main)
	if errors.Is(err, git.ErrNotRepo) {
		return git.Repo{Root: main, GitDir: main, CommonDir: main}, nil
	}

	return repo, err
}

// withContext reports a context that ended as the reason, since git only says
// it was killed.
func withContext(ctx context.Context, err error) error {
	if cerr := ctx.Err(); cerr != nil && !errors.Is(err, cerr) {
		return fmt.Errorf("%w: %w", cerr, err)
	}

	return err
}

func warnPrune(log *slog.Logger, pr int, err error) {
	log.Warn("prune refs after clean up", "pr", pr, "err", err)
}

// onCleaned says what a cleanup did. A cleanup cut short by the app closing says nothing.
func (a *App) onCleaned(msg cleanedMsg) {
	t := msg.target
	path, branch := t.path(), printable(t.found.branch)

	if msg.pruneErr != nil {
		warnPrune(a.log, t.number, msg.pruneErr)
	}

	switch {
	case errors.Is(msg.err, context.Canceled):
		return
	case errors.Is(msg.err, context.DeadlineExceeded):
		a.toast(levelError, fmt.Sprintf("clean up %s: timed out", path))

		return
	case errors.Is(msg.err, errLeftBranch):
		a.toast(levelError, fmt.Sprintf("kept %s: it is no longer on %s", path, branch))

		return
	case errors.Is(msg.err, git.ErrWorktreeDirty):
		a.toast(levelError, fmt.Sprintf("kept %s: it has uncommitted changes", path))

		return
	case msg.err != nil:
		a.toast(levelError, fmt.Sprintf("kept %s: %s", path, gitReason(msg.err)))

		return
	}

	text := "removed " + path

	switch {
	case msg.deleted:
		text += "; deleted " + branch
	case errors.Is(msg.delErr, context.DeadlineExceeded):
		text += fmt.Sprintf("; deleting %s failed: timed out", branch)
	case msg.delErr != nil:
		text += fmt.Sprintf("; deleting %s failed: %s", branch, gitReason(msg.delErr))
	case msg.kept != "":
		text += fmt.Sprintf("; kept %s: %s", branch, msg.kept)
	case !t.canDelete():
		text += fmt.Sprintf("; kept %s: %s", branch, t.keptReason())
	}

	a.toast(levelSuccess, text)
}

const (
	noticeCleanupDetached  = "clean up: HEAD is detached"
	noticeCleanupNotMerged = "%s is not merged"
	noticeCleanupFork      = "%s's branch is in another repository"
	noticeCleanupDefault   = "%s is the default branch, so it stays"
	noticeCleanupNoHead    = "%s: its merged head is unknown"
)

// cleanupPlaceMsg answers finding the place and pull request of the focused pane.
type cleanupPlaceMsg struct {
	seq    int
	pane   core.PaneID
	place  prPlace
	number int
	repo   github.Repo
	err    error
}

// cleanupCommand is worktree.cleanup: it finds, off the update loop, the
// focused pane's followed place and its pull request, and cleans up after the
// pull request when the state reads it merged.
func (a *App) cleanupCommand() tea.Cmd {
	a.cleanupSeq++

	if a.prs.src == nil || a.prs.resolve == nil {
		a.notice = noticePRsOff

		return nil
	}

	st := a.store.State()
	pane := st.Tab().Focus

	dir := st.Panes[pane].FollowDir()
	if dir == "" {
		a.notice = noticeNotRepo

		return nil
	}

	var (
		seq                     = a.cleanupSeq
		resolve, lookup, appCtx = a.prs.resolve, a.prs.lookup, a.ctx
		g                       = a.git
	)

	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(appCtx, prShowTimeout)
		defer cancel()

		msg := cleanupPlaceMsg{seq: seq, pane: pane}
		if msg.place, msg.err = resolvePRPlace(ctx, g, resolve, dir); msg.err != nil {
			return msg
		}

		msg.repo = msg.place.repo
		msg.number, _, msg.err = prOfPlace(ctx, st, lookup, msg.place)

		return msg
	}
}

// onCleanupPlace checks that the pull request is merged and starts the
// worktree check, or says why there is nothing to clean up. An answer that is
// not the newest request's, or whose pane lost the focus, is dropped.
func (a *App) onCleanupPlace(msg cleanupPlaceMsg) tea.Cmd {
	st := a.store.State()
	if msg.seq != a.cleanupSeq || a.modalOpen() || msg.pane != st.Tab().Focus {
		return nil
	}

	branch := printable(msg.place.branch)

	switch err := msg.err; {
	case err == nil:
	case errors.Is(err, context.Canceled):
		return nil
	case errors.Is(err, errPRNoRepo):
		a.notice = noticeNotRepo

		return nil
	case errors.Is(err, errPRDetached):
		a.notice = noticeCleanupDetached

		return nil
	case errors.Is(err, errPRNoRemote):
		a.notice = fmt.Sprintf(noticePRNoRemote, branch)

		return nil
	case errors.Is(err, errPRNotGitHub):
		a.notice = fmt.Sprintf(noticePRNotGitHub, branch)

		return nil
	case errors.Is(err, errPRNone):
		a.notice = fmt.Sprintf(noticePRNone, branch)

		return nil
	case errors.Is(err, context.DeadlineExceeded):
		a.toast(levelError, "clean up: timed out")

		return nil
	default:
		a.toast(levelError, gitReason(err))

		return nil
	}

	pr, ok := prInfoByNumber(st, msg.repo.String(), msg.number)
	if pr.HeadBranch == "" {
		pr.HeadBranch = msg.place.branch
	}

	num := prNumberText(msg.number)

	switch {
	case !ok || pr.State != prMergedState:
		a.notice = fmt.Sprintf(noticeCleanupNotMerged, num)
	case pr.CrossRepo:
		a.notice = fmt.Sprintf(noticeCleanupFork, num)
	case pr.HeadBranch == st.PRs[msg.repo.String()].DefaultBranch:
		a.notice = fmt.Sprintf(noticeCleanupDefault, printable(pr.HeadBranch))
	case pr.HeadOID == "":
		a.notice = fmt.Sprintf(noticeCleanupNoHead, num)
	default:
		return a.cleanupCheck(fromCommand, msg.repo, pr, msg.place.root, pr.HeadOID)
	}

	return nil
}
