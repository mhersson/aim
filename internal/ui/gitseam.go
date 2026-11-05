package ui

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"time"

	"github.com/mhersson/aim/internal/git"
)

// Git is every git call the app makes. cmd/aim passes SystemGit; the tests
// pass a fake, so no UI test starts git.
type Git interface {
	Open(ctx context.Context, dir string) (git.Repo, error)
	EditPlace(ctx context.Context, path string) (git.Place, error)
	Head(ctx context.Context, r git.Repo) (git.HeadInfo, error)
	HeadCommit(ctx context.Context, r git.Repo) (git.Commit, error)
	Ahead(ctx context.Context, r git.Repo) (n int, ok bool, err error)
	DefaultBranch(ctx context.Context, r git.Repo) (string, error)
	Log(ctx context.Context, r git.Repo, base string) ([]git.Commit, error)
	DiffStat(ctx context.Context, r git.Repo, base string) (string, error)

	ListWorktrees(ctx context.Context, r git.Repo) ([]git.Worktree, error)
	CreateWorktree(ctx context.Context, r git.Repo, nw git.NewWorktree) (git.Created, error)
	WorktreeChanges(ctx context.Context, dir string) (int, error)
	RemoveWorktree(ctx context.Context, r git.Repo, path string) error
	BranchAhead(ctx context.Context, r git.Repo, branch, head string) (tip string, ahead int, err error)
	DeleteBranch(ctx context.Context, r git.Repo, branch, tip string) error
	PruneWorktree(ctx context.Context, r git.Repo, worktree string) (int, error)

	PushRemote(ctx context.Context, r git.Repo, branch string) (string, error)
	PushURLs(ctx context.Context, r git.Repo, remote string) ([]string, error)
	RemoteURL(ctx context.Context, r git.Repo, remote string) (string, error)
	RemoteBranches(ctx context.Context, r git.Repo, remote string) ([]string, error)
	RemoteHasBranch(ctx context.Context, r git.Repo, remote, branch string) (bool, error)
	HasUpstream(ctx context.Context, r git.Repo, branch string) (bool, error)
	Unpushed(ctx context.Context, r git.Repo, remote string) (int, error)
	Push(ctx context.Context, r git.Repo, branch string) (git.PushResult, error)
	FetchPR(ctx context.Context, r git.Repo, remote string, number int, base string) (git.PRRefs, error)

	ListAimRefs(ctx context.Context, r git.Repo) (StaleRefs, error)
	DeleteRefs(ctx context.Context, r git.Repo, refs []git.Ref) (int, error)
	DeletePRRefs(ctx context.Context, r git.Repo, number int) error

	NewEngine(o git.EngineOptions) DiffEngine
}

// DiffEngine is the diff engine's surface the drawer, the rail and the
// worktree list use; *git.Engine is the real one.
type DiffEngine interface {
	Diff(ctx context.Context, src git.Source) (git.Result, error)
	Update(ctx context.Context, prev git.Result, src git.Source, b git.Batch) (git.Result, error)
	Stats(ctx context.Context, src git.Source) (git.Stats, error)
	FileText(ctx context.Context, worktree string, f git.FileDiff) (old, nw []string, err error)
	TurnList(ctx context.Context, worktree, session string) (git.TurnList, error)
	TurnListAt(ctx context.Context, worktree, session, workingTree string) (git.TurnList, error)
}

// StaleRefs is a listing of refs/aim; git.AimRefs is the real one.
type StaleRefs interface {
	Stale(now time.Time, held func(session string) bool, shown func(number int) bool) git.Sweep
}

// watchSet is the shared worktree watcher; *git.WatchSet is the real one.
type watchSet interface {
	Acquire(ctx context.Context, r git.Repo) (release func(), err error)
	Batches() <-chan git.RootBatch
}

// placeFinder answers which worktree an edited file belongs to;
// *git.PlaceCache is the real one.
type placeFinder interface {
	Place(ctx context.Context, path string) (git.Place, error)
}

// errNoBaseRef is SystemGit.Log's answer for a base ref that does not exist.
var errNoBaseRef = errors.New("base ref missing")

// baseRefMissing reports whether err is git saying the base ref does not
// exist: rev-parse --verify --quiet exits 1 and says nothing.
func baseRefMissing(err error) bool {
	var ee *exec.ExitError

	return errors.As(err, &ee) && ee.ExitCode() == 1
}

// SystemGit is Git over internal/git.
type SystemGit struct{}

func (SystemGit) Open(ctx context.Context, dir string) (git.Repo, error) { return git.Open(ctx, dir) }

func (SystemGit) EditPlace(ctx context.Context, path string) (git.Place, error) {
	return git.EditPlace(ctx, path)
}

func (SystemGit) Head(ctx context.Context, r git.Repo) (git.HeadInfo, error) {
	return git.Head(ctx, r)
}

func (SystemGit) HeadCommit(ctx context.Context, r git.Repo) (git.Commit, error) {
	return git.HeadCommit(ctx, r)
}

func (SystemGit) Ahead(ctx context.Context, r git.Repo) (int, bool, error) {
	return git.Ahead(ctx, r)
}

func (SystemGit) DefaultBranch(ctx context.Context, r git.Repo) (string, error) {
	return git.DefaultBranch(ctx, r)
}

// Log maps git's exit status 1 for a missing base to errNoBaseRef.
func (SystemGit) Log(ctx context.Context, r git.Repo, base string) ([]git.Commit, error) {
	cs, err := git.Log(ctx, r, base)
	if err != nil && baseRefMissing(err) {
		return nil, fmt.Errorf("%w: %w", errNoBaseRef, err)
	}

	return cs, err
}

// DiffStat maps a missing base to errNoBaseRef, as Log does.
func (SystemGit) DiffStat(ctx context.Context, r git.Repo, base string) (string, error) {
	stat, err := git.DiffStat(ctx, r, base)
	if err != nil && baseRefMissing(err) {
		return "", fmt.Errorf("%w: %w", errNoBaseRef, err)
	}

	return stat, err
}

func (SystemGit) ListWorktrees(ctx context.Context, r git.Repo) ([]git.Worktree, error) {
	return git.ListWorktrees(ctx, r)
}

func (SystemGit) CreateWorktree(ctx context.Context, r git.Repo, nw git.NewWorktree) (git.Created, error) {
	return git.CreateWorktree(ctx, r, nw)
}

func (SystemGit) WorktreeChanges(ctx context.Context, dir string) (int, error) {
	return git.WorktreeChanges(ctx, dir)
}

func (SystemGit) RemoveWorktree(ctx context.Context, r git.Repo, path string) error {
	return git.RemoveWorktree(ctx, r, path)
}

func (SystemGit) BranchAhead(ctx context.Context, r git.Repo, branch, head string) (string, int, error) {
	return git.BranchAhead(ctx, r, branch, head)
}

func (SystemGit) DeleteBranch(ctx context.Context, r git.Repo, branch, tip string) error {
	return git.DeleteBranch(ctx, r, branch, tip)
}

func (SystemGit) PruneWorktree(ctx context.Context, r git.Repo, worktree string) (int, error) {
	return git.PruneWorktree(ctx, r, worktree)
}

func (SystemGit) PushRemote(ctx context.Context, r git.Repo, branch string) (string, error) {
	return git.PushRemote(ctx, r, branch)
}

func (SystemGit) PushURLs(ctx context.Context, r git.Repo, remote string) ([]string, error) {
	return git.PushURLs(ctx, r, remote)
}

func (SystemGit) RemoteURL(ctx context.Context, r git.Repo, remote string) (string, error) {
	return git.RemoteURL(ctx, r, remote)
}

func (SystemGit) RemoteBranches(ctx context.Context, r git.Repo, remote string) ([]string, error) {
	return git.RemoteBranches(ctx, r, remote)
}

func (SystemGit) RemoteHasBranch(ctx context.Context, r git.Repo, remote, branch string) (bool, error) {
	return git.RemoteHasBranch(ctx, r, remote, branch)
}

func (SystemGit) HasUpstream(ctx context.Context, r git.Repo, branch string) (bool, error) {
	return git.HasUpstream(ctx, r, branch)
}

func (SystemGit) Unpushed(ctx context.Context, r git.Repo, remote string) (int, error) {
	return git.Unpushed(ctx, r, remote)
}

func (SystemGit) Push(ctx context.Context, r git.Repo, branch string) (git.PushResult, error) {
	return git.Push(ctx, r, branch)
}

func (SystemGit) FetchPR(ctx context.Context, r git.Repo, remote string, number int, base string) (git.PRRefs, error) {
	return git.FetchPR(ctx, r, remote, number, base)
}

func (SystemGit) ListAimRefs(ctx context.Context, r git.Repo) (StaleRefs, error) {
	return git.ListAimRefs(ctx, r)
}

func (SystemGit) DeleteRefs(ctx context.Context, r git.Repo, refs []git.Ref) (int, error) {
	return git.DeleteRefs(ctx, r, refs)
}

func (SystemGit) DeletePRRefs(ctx context.Context, r git.Repo, number int) error {
	return git.DeletePRRefs(ctx, r, number)
}

func (SystemGit) NewEngine(o git.EngineOptions) DiffEngine { return git.NewEngine(o) }
