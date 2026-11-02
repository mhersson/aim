package git

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/mhersson/aim/internal/git/linediff"
)

// maxFileText bounds one side of Engine.FileText.
const maxFileText = 8 << 20

var (
	// ErrNoSnapshot means the requested turn has no snapshot.
	ErrNoSnapshot = errors.New("git: turn has no snapshot")
	// ErrNoTurns means the session has no recorded turns.
	ErrNoTurns = errors.New("git: session has no turns")
	// ErrNotFetched means the pull request refs are not in the repository.
	ErrNotFetched = errors.New("git: pull request refs not fetched")
	// ErrTooLarge means a file is too large to read whole.
	ErrTooLarge = errors.New("git: file too large")
)

// Source selects what Engine.Diff compares.
type Source interface{ source() }

// BranchSource compares the merge base of Base and HEAD with the working tree.
// An empty Base means the default branch.
type BranchSource struct{ Worktree, Base string }

// TurnSource compares one turn of a worktree with what followed it. Turn is a
// position in the worktree's TurnList: 1 the oldest, 0 the latest.
type TurnSource struct {
	Worktree, Session string
	Turn              int
}

// PRSource compares the merge base of a pull request's base and head, as FetchPR
// stored them, with its head.
type PRSource struct {
	Worktree string
	Number   int
}

func (BranchSource) source() {}
func (TurnSource) source()   {}
func (PRSource) source()     {}

// Status is how a file differs between the two sides.
type Status string

// File statuses. ModeChanged is a mode change with identical content.
const (
	Added       Status = "added"
	Deleted     Status = "deleted"
	Modified    Status = "modified"
	Renamed     Status = "renamed"
	Copied      Status = "copied"
	ModeChanged Status = "mode"
)

// LineKind is the diff marker of a line.
type LineKind byte

// Line kinds, the characters git prints.
const (
	Context LineKind = ' '
	Add     LineKind = '+'
	Del     LineKind = '-'
)

// Line is one line of a hunk.
type Line struct {
	Kind      LineKind
	Old, New  int // line numbers; 0 on the side the line is not on
	Text      string
	NoNewline bool
}

// Hunk is one block of changes with its context.
type Hunk struct {
	OldStart, OldLines, NewStart, NewLines int
	Section                                string
	Lines                                  []Line
}

// FileDiff is one changed file. OldPath is set for renames and copies only.
// Untracked marks a file whose path git does not track and the old side lacks,
// also when git paired it with another file as a rename or copy.
// Hunks stay empty for binary files, files without line changes and files
// marked Truncated; cached hunks are shared, so treat them as read-only.
type FileDiff struct {
	Path, OldPath                string
	Status                       Status
	OldOID, NewOID               string // full ids; "" on the side a file does not exist
	OldMode, NewMode             string
	Binary, Untracked, Truncated bool
	Added, Removed               int
	Hunks                        []Hunk
}

// Result is a diff between two trees.
type Result struct {
	Old, New  string // tree ids
	Live      bool   // New is the working tree
	Turn      int    // turn sources: the position shown in the worktree's list
	Turns     int    // turn sources: how many turns the worktree's list held
	Head      string // PR sources: the head commit shown; a merge passes it
	Files     []FileDiff
	Truncated bool
}

// Limits bound the work and memory of one diff.
type Limits struct{ Files, Bytes, Lines int }

// DefaultLimits returns 400 files with hunks, 2 MiB of diff text and 5000
// changed lines per file.
func DefaultLimits() Limits {
	return Limits{Files: 400, Bytes: 2 << 20, Lines: 5000}
}

// Engine turns sources into structured diffs. It is safe for concurrent use.
type Engine struct {
	limits     Limits
	tempDir    string
	indexCache string
	cache      *hunkCache
	repos      repoCache // the repos Update opened, by worktree path
	maxText    int       // bytes FileText reads per side
}

// EngineOptions configure NewEngine.
type EngineOptions struct {
	Limits     Limits // zero fields take the defaults
	TempDir    string // where working-tree snapshots keep their temporary index
	IndexCache string // keeps a refreshed index copy per worktree; empty = none
}

// NewEngine returns an engine for the given options.
func NewEngine(o EngineOptions) *Engine {
	l := o.Limits

	d := DefaultLimits()
	if l.Files <= 0 {
		l.Files = d.Files
	}

	if l.Bytes <= 0 {
		l.Bytes = d.Bytes
	}

	if l.Lines <= 0 {
		l.Lines = d.Lines
	}

	return &Engine{limits: l, tempDir: o.TempDir, indexCache: o.IndexCache, cache: newHunkCache(cacheBytes), maxText: maxFileText}
}

func (e *Engine) workingTree(ctx context.Context, r Repo) (WorkTree, error) {
	return WorkingTree(ctx, r, TreeOptions{TempDir: e.tempDir, IndexCache: e.indexCache})
}

// Diff computes the diff for src.
func (e *Engine) Diff(ctx context.Context, src Source) (Result, error) {
	switch s := src.(type) {
	case BranchSource:
		return e.diffBranch(ctx, s)
	case TurnSource:
		return e.diffTurn(ctx, s)
	case PRSource:
		return e.diffPR(ctx, s)
	default:
		return Result{}, fmt.Errorf("diff: unsupported source %T", src)
	}
}

func (e *Engine) diffBranch(ctx context.Context, s BranchSource) (Result, error) {
	r, oldTree, wt, err := e.branchTrees(ctx, s)
	if err != nil {
		return Result{}, err
	}

	res, err := e.compare(ctx, r, oldTree, wt)
	if err != nil {
		return Result{}, err
	}

	res.Live = true

	return res, nil
}

// branchTrees resolves the two sides of a branch source: the merge base's tree
// and a snapshot of the working tree.
func (e *Engine) branchTrees(ctx context.Context, s BranchSource) (Repo, string, WorkTree, error) {
	r, err := Open(ctx, s.Worktree)
	if err != nil {
		return Repo{}, "", WorkTree{}, err
	}

	base := s.Base
	if base == "" {
		if base, err = DefaultBranch(ctx, r); err != nil {
			return Repo{}, "", WorkTree{}, err
		}
	}

	if strings.HasPrefix(base, "-") {
		return Repo{}, "", WorkTree{}, fmt.Errorf("diff: base %q looks like an option", base)
	}

	oldTree, err := mergeBaseTree(ctx, r, base)
	if err != nil {
		return Repo{}, "", WorkTree{}, err
	}

	wt, err := e.workingTree(ctx, r)
	if err != nil {
		return Repo{}, "", WorkTree{}, fmt.Errorf("diff: %w", err)
	}

	return r, oldTree, wt, nil
}

func (e *Engine) diffPR(ctx context.Context, s PRSource) (Result, error) {
	r, err := Open(ctx, s.Worktree)
	if err != nil {
		return Result{}, err
	}

	pr, err := resolvePR(ctx, r, s.Number)
	if err != nil {
		return Result{}, err
	}

	res, err := e.compare(ctx, r, pr.baseTree, WorkTree{Tree: pr.headTree})
	if err != nil {
		return Result{}, err
	}

	res.Head = pr.head

	return res, nil
}

func (e *Engine) diffTurn(ctx context.Context, s TurnSource) (Result, error) {
	r, err := Open(ctx, s.Worktree)
	if err != nil {
		return Result{}, err
	}

	list, wt, err := e.turnList(ctx, r, s.Session, "")
	if err != nil {
		return Result{}, err
	}

	turns := list.Turns

	if len(turns) == 0 {
		return Result{}, fmt.Errorf("diff session %s: %w", s.Session, ErrNoTurns)
	}

	pos := s.Turn
	if pos == 0 {
		pos = len(turns)
	}

	if pos < 1 || pos > len(turns) {
		return Result{}, fmt.Errorf("diff turn %d: %w", pos, ErrNoSnapshot)
	}

	t := turns[pos-1]

	var res Result

	switch {
	case t.Missing, t.EndCommit != "" && t.EndMissing, t.EndCommit == "" && !t.Current:
		return Result{}, fmt.Errorf("diff turn %d: %w", pos, ErrNoSnapshot)
	case t.EndCommit != "":
		res, err = e.compare(ctx, r, t.Tree, WorkTree{Tree: t.EndTree})
		if err != nil {
			return Result{}, err
		}
	default:
		res, err = e.compare(ctx, r, t.Tree, *wt)
		if err != nil {
			return Result{}, err
		}

		res.Live = true
	}

	res.Turn, res.Turns = pos, len(turns)

	return res, nil
}

// mergeBaseTree returns the tree of the merge base of base and HEAD.
func mergeBaseTree(ctx context.Context, r Repo, base string) (string, error) {
	out, err := run(ctx, call{dir: r.Root, args: []string{"merge-base", base, "HEAD"}, read: true})
	if err != nil {
		return "", fmt.Errorf("diff: merge base of %s and HEAD: %w", base, err)
	}

	commit := strings.TrimSpace(string(out))

	out, err = run(ctx, call{dir: r.Root, args: []string{"rev-parse", "--verify", commit + "^{tree}"}, read: true})
	if err != nil {
		return "", fmt.Errorf("diff: tree of %s: %w", commit, err)
	}

	return strings.TrimSpace(string(out)), nil
}

// compare diffs oldTree against the new side. Files the old side lacks and the
// working tree does not track are marked Untracked.
func (e *Engine) compare(ctx context.Context, r Repo, oldTree string, newSide WorkTree) (Result, error) {
	res := Result{Old: oldTree, New: newSide.Tree}

	files, err := listFiles(ctx, r, oldTree, newSide.Tree)
	if err != nil {
		return Result{}, err
	}

	markUntracked(files, newSide.Untracked)

	if err := e.addHunks(ctx, r, oldTree, newSide.Tree, files); err != nil {
		return Result{}, err
	}

	for _, f := range files {
		res.Truncated = res.Truncated || f.Truncated
	}

	res.Files = files

	return res, nil
}

// markUntracked marks the files whose path git does not track. A path the old
// side holds (modified, mode changed) is judged by the comparison alone.
func markUntracked(files []FileDiff, untracked []string) {
	set := make(map[string]bool, len(untracked))
	for _, p := range untracked {
		set[p] = true
	}

	for i := range files {
		f := &files[i]

		switch f.Status {
		case Added, Renamed, Copied:
			f.Untracked = set[f.Path]
		default:
			f.Untracked = false
		}
	}
}

// FileText returns the lines of both sides of f, read from the object database.
// An empty id gives an empty side. A side over 8 MiB gives ErrTooLarge.
func (e *Engine) FileText(ctx context.Context, worktree string, f FileDiff) (old, nw []string, err error) {
	if old, err = blobLines(ctx, worktree, f.OldOID, e.maxText); err != nil {
		return nil, nil, fmt.Errorf("read old side of %s: %w", f.Path, err)
	}

	if nw, err = blobLines(ctx, worktree, f.NewOID, e.maxText); err != nil {
		return nil, nil, fmt.Errorf("read new side of %s: %w", f.Path, err)
	}

	return old, nw, nil
}

func blobLines(ctx context.Context, worktree, oid string, limit int) ([]string, error) {
	if oid == "" {
		return nil, nil
	}

	out, err := run(ctx, call{
		dir:   worktree,
		args:  []string{"cat-file", "blob", oid},
		read:  true,
		limit: int64(limit),
	})
	if errors.Is(err, errLimit) {
		return nil, ErrTooLarge
	}

	if err != nil {
		return nil, err
	}

	return linediff.Split(string(out)), nil
}
