package git

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
)

var (
	// ErrExists means the worktree path is already taken.
	ErrExists = errors.New("git: worktree path exists")
	// ErrBadBranch means the branch name is not usable as a branch or a directory.
	ErrBadBranch = errors.New("git: invalid branch name")
	// ErrNoBase means there is no usable base branch.
	ErrNoBase = errors.New("git: no base branch")
)

const excludeLine = "/.worktrees/"

// Worktree is one entry of `git worktree list`.
type Worktree struct {
	Path, Head, Branch      string // Branch is without refs/heads/; "" when detached or bare
	Main, Bare, Detached    bool
	Locked, Prunable        bool
	LockReason, PruneReason string
}

// RootStyle says where new worktrees go.
type RootStyle int

const (
	RootInside  RootStyle = iota // <main checkout>/.worktrees/<name>
	RootSibling                  // <parent>/<main checkout name>.worktrees/<name>
)

// NewWorktree describes a worktree to create.
type NewWorktree struct {
	Branch string
	Base   string // "" = the default branch; ignored when Branch already exists
	Root   RootStyle
}

// Created is the result of CreateWorktree.
type Created struct {
	Worktree Worktree
	Excluded bool   // this call added /.worktrees/ to info/exclude
	Tools    []Tool // tools that need their own ignore entry (RootInside only)
	Copied   int    // files copied from the main checkout as its .worktreeinclude names them
	CopyErr  error  // the first copy failure; the worktree is made all the same
}

// ListWorktrees lists the repository's worktrees; the first is the main checkout.
func ListWorktrees(ctx context.Context, r Repo) ([]Worktree, error) {
	out, err := run(ctx, call{dir: r.Root, args: []string{"worktree", "list", "--porcelain", "-z"}, read: true})
	if err != nil {
		return nil, fmt.Errorf("list worktrees: %w", err)
	}

	return parseWorktrees(out), nil
}

// parseWorktrees reads NUL-separated fields; an empty field ends a record.
func parseWorktrees(out []byte) []Worktree {
	var (
		list []Worktree
		cur  *Worktree
	)

	for field := range bytes.SplitSeq(out, []byte{0}) {
		line := string(field)
		if line == "" {
			cur = nil

			continue
		}

		key, val, _ := strings.Cut(line, " ")

		if key == "worktree" {
			list = append(list, Worktree{Path: val, Main: len(list) == 0})
			cur = &list[len(list)-1]

			continue
		}

		if cur == nil {
			continue
		}

		switch key {
		case "HEAD":
			cur.Head = val
		case "branch":
			cur.Branch = strings.TrimPrefix(val, "refs/heads/")
		case "bare":
			cur.Bare = true
		case "detached":
			cur.Detached = true
		case "locked":
			cur.Locked, cur.LockReason = true, val
		case "prunable":
			cur.Prunable, cur.PruneReason = true, val
		}
	}

	return list
}

var unsafeDirChars = regexp.MustCompile(`[^A-Za-z0-9._-]+`)

// WorktreeDir turns a branch name into a directory name.
func WorktreeDir(branch string) (string, error) {
	name := strings.Trim(unsafeDirChars.ReplaceAllString(branch, "-"), "-.")
	if name == "" {
		return "", fmt.Errorf("directory name for %q: %w", branch, ErrBadBranch)
	}

	return name, nil
}

// DefaultBranch returns the start point for new branches: origin/<name> when
// origin/HEAD points at an existing branch, else main, else master.
func DefaultBranch(ctx context.Context, r Repo) (string, error) {
	out, err := run(ctx, call{dir: r.Root, args: []string{"symbolic-ref", "--quiet", "refs/remotes/origin/HEAD"}, read: true})

	switch {
	case err == nil:
		target := strings.TrimSpace(string(out))
		if name, ok := strings.CutPrefix(target, "refs/remotes/origin/"); ok {
			exists, err := refExists(ctx, r, target)
			if err != nil {
				return "", err
			}

			if exists {
				return "origin/" + name, nil
			}
		}
	case !isExitCode(err, 1):
		return "", fmt.Errorf("default branch: %w", err)
	}

	for _, name := range []string{"main", "master"} {
		exists, err := refExists(ctx, r, "refs/heads/"+name)
		if err != nil {
			return "", err
		}

		if exists {
			return name, nil
		}
	}

	return "", fmt.Errorf("default branch: %w", ErrNoBase)
}

func refExists(ctx context.Context, r Repo, ref string) (bool, error) {
	_, err := run(ctx, call{dir: r.Root, args: []string{"show-ref", "--verify", "--quiet", ref}, read: true})
	if err == nil {
		return true, nil
	}

	if isExitCode(err, 1) {
		return false, nil
	}

	return false, fmt.Errorf("check %s: %w", ref, err)
}

// isExitCode reports whether err wraps a git exit with the given status.
func isExitCode(err error, code int) bool {
	var exitErr *exec.ExitError

	return errors.As(err, &exitErr) && exitErr.ExitCode() == code
}

// CreateWorktree adds a worktree for nw.Branch under the chosen root, creating
// the branch from the base unless it exists (then the base is ignored). A
// non-empty Worktree.Path in the result means the worktree exists, even when
// the error is not nil. It also copies what the main checkout's
// .worktreeinclude names; a copy failure is Created.CopyErr, not the error.
func CreateWorktree(ctx context.Context, r Repo, nw NewWorktree) (Created, error) {
	if err := checkBranch(ctx, r, nw.Branch); err != nil {
		return Created{}, err
	}

	path, err := WorktreePath(r, nw.Root, nw.Branch)
	if err != nil {
		return Created{}, err
	}

	if _, err := os.Lstat(path); err == nil {
		return Created{}, fmt.Errorf("create worktree %s: %w", path, ErrExists)
	} else if !errors.Is(err, os.ErrNotExist) {
		return Created{}, fmt.Errorf("create worktree %s: %w", path, err)
	}

	created := Created{}

	if nw.Root == RootInside {
		if created.Excluded, err = ensureExcluded(r.CommonDir); err != nil {
			return Created{}, err
		}
	}

	args, err := addArgs(ctx, r, nw, path)
	if err != nil {
		return Created{}, err
	}

	if _, err := run(ctx, call{dir: r.Root, args: args}); err != nil {
		return Created{}, fmt.Errorf("create worktree %s: %w", path, err)
	}

	created.Worktree = Worktree{Path: path, Branch: nw.Branch}

	main, mainErr := r.MainRoot()
	if mainErr == nil {
		created.Copied, created.CopyErr = CopyIncluded(ctx, main, path)
	}

	if nw.Root == RootInside {
		created.Tools, _ = DetectTools(main) // advisory: keeps what it found; the inside root has a main checkout
	}

	list, err := ListWorktrees(ctx, r)
	if err != nil {
		return created, err
	}

	for _, w := range list {
		if filepath.Clean(w.Path) == path {
			created.Worktree = w

			return created, nil
		}
	}

	return created, fmt.Errorf("create worktree %s: not listed afterwards", path)
}

// WorktreePath is where CreateWorktree puts a worktree for branch under the
// root of style. It reads nothing from the disk, so a page can preview it on
// every frame. It fails with ErrBadBranch when the name has no directory form
// and with ErrBare when the inside root has no main checkout.
func WorktreePath(r Repo, style RootStyle, branch string) (string, error) {
	name, err := WorktreeDir(branch)
	if err != nil {
		return "", err
	}

	root, err := worktreeRoot(r, style)
	if err != nil {
		return "", err
	}

	return filepath.Join(root, name), nil
}

// checkBranch rejects names git refuses; the name must also survive
// check-ref-format unchanged, which rules out forms like @{-1}.
func checkBranch(ctx context.Context, r Repo, branch string) error {
	out, err := run(ctx, call{dir: r.Root, args: []string{"check-ref-format", "--branch", branch}, read: true})
	if err != nil {
		if isExitCode(err, 1) || isExitCode(err, 128) {
			return fmt.Errorf("branch %q: %w", branch, ErrBadBranch)
		}

		return fmt.Errorf("check branch %q: %w", branch, err)
	}

	if strings.TrimSpace(string(out)) != branch {
		return fmt.Errorf("branch %q: %w", branch, ErrBadBranch)
	}

	return nil
}

func worktreeRoot(r Repo, style RootStyle) (string, error) {
	main, err := r.MainRoot()
	if style == RootInside {
		if err != nil {
			return "", err
		}

		return filepath.Join(main, ".worktrees"), nil
	}

	if err == nil {
		return main + ".worktrees", nil
	}

	// A bare repository: /x/repo.git gives /x/repo.worktrees.
	base := strings.TrimSuffix(filepath.Base(r.CommonDir), ".git")
	if base == "" {
		return "", err
	}

	return filepath.Join(filepath.Dir(r.CommonDir), base+".worktrees"), nil
}

// addArgs is the `git worktree add` command line. --quiet drops git's progress
// line, which would otherwise be the first line of stderr and so the whole of
// the error text, ahead of the line that says why it failed.
func addArgs(ctx context.Context, r Repo, nw NewWorktree, path string) ([]string, error) {
	exists, err := refExists(ctx, r, "refs/heads/"+nw.Branch)
	if err != nil {
		return nil, err
	}

	if exists {
		return []string{"worktree", "add", "--quiet", "--", path, nw.Branch}, nil
	}

	base := nw.Base
	if base == "" {
		if base, err = DefaultBranch(ctx, r); err != nil {
			return nil, err
		}
	}

	if strings.HasPrefix(base, "-") {
		return nil, fmt.Errorf("base %q: %w", base, ErrNoBase)
	}

	return []string{"worktree", "add", "--quiet", "--no-track", "-b", nw.Branch, "--", path, base}, nil
}

// ensureExcluded appends /.worktrees/ to info/exclude unless a line already
// equals it, and reports whether it wrote.
func ensureExcluded(commonDir string) (bool, error) {
	path := filepath.Join(commonDir, "info", "exclude")

	content, err := os.ReadFile(path)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return false, fmt.Errorf("read %s: %w", path, err)
	}

	if hasLine(string(content), excludeLine) {
		return false, nil
	}

	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return false, fmt.Errorf("create %s: %w", filepath.Dir(path), err)
	}

	add := excludeLine + "\n"
	if len(content) > 0 && content[len(content)-1] != '\n' {
		add = "\n" + add
	}

	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return false, fmt.Errorf("open %s: %w", path, err)
	}

	if _, err := f.WriteString(add); err != nil {
		_ = f.Close()

		return false, fmt.Errorf("write %s: %w", path, err)
	}

	if err := f.Close(); err != nil {
		return false, fmt.Errorf("close %s: %w", path, err)
	}

	return true, nil
}
