package git

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestManagedWorktree(t *testing.T) {
	t.Parallel()

	dir, r := seededRepo(t)
	parent := filepath.Dir(dir)

	link := filepath.Join(t.TempDir(), "link")
	if err := os.Symlink(parent, link); err != nil {
		t.Fatal(err)
	}

	// A path under the sibling root reached through a symlink to the parent.
	siblingRoot := dir + ".worktrees"
	for _, d := range []string{filepath.Join(siblingRoot, "x"), filepath.Join(dir, ".worktrees", "x"), filepath.Join(dir, ".worktrees", "a", "b")} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}

	// An inside root that is itself a symlink to a directory elsewhere.
	other := t.TempDir()
	linkedRepo := newRepo(t)
	writeFile(t, linkedRepo, "a", "a\n")
	commitAll(t, linkedRepo, "init")

	lr, err := Open(t.Context(), linkedRepo)
	if err != nil {
		t.Fatal(err)
	}

	if err := os.Symlink(other, filepath.Join(linkedRepo, ".worktrees")); err != nil {
		t.Fatal(err)
	}

	if err := os.Mkdir(filepath.Join(other, "x"), 0o755); err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name string
		repo Repo
		path string
		want bool
	}{
		{"inside root", r, filepath.Join(dir, ".worktrees", "x"), true},
		{"sibling root", r, filepath.Join(siblingRoot, "x"), true},
		{"main checkout", r, dir, false},
		{"nested below the root", r, filepath.Join(dir, ".worktrees", "a", "b"), false},
		{"elsewhere", r, filepath.Join(dir, "..", "elsewhere"), false},
		{"root reached through a symlink", lr, filepath.Join(linkedRepo, ".worktrees", "x"), true},
		{"parent directory missing", r, filepath.Join(dir, ".worktrees", "gone", "x"), false},
		{"sibling reached through a symlinked parent", r, filepath.Join(link, filepath.Base(dir)+".worktrees", "x"), true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if got := ManagedWorktree(tt.repo, tt.path); got != tt.want {
				t.Errorf("ManagedWorktree(%q) = %v, want %v", tt.path, got, tt.want)
			}
		})
	}
}

func TestWorktreeChanges(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		setup func(t *testing.T, dir string)
		want  int
	}{
		{"clean", func(*testing.T, string) {}, 0},
		{"one staged rename", func(t *testing.T, dir string) {
			gitOut(t, dir, "mv", "a.txt", "b.txt")
		}, 1},
		{"a modified and two untracked", func(t *testing.T, dir string) {
			writeFile(t, dir, "a.txt", "changed\n")
			writeFile(t, dir, "u1.txt", "1\n")
			writeFile(t, dir, "u2.txt", "2\n")
		}, 3},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			dir, _ := seededRepo(t)
			tt.setup(t, dir)

			got, err := WorktreeChanges(t.Context(), dir)
			if err != nil {
				t.Fatal(err)
			}

			if got != tt.want {
				t.Errorf("WorktreeChanges = %d, want %d", got, tt.want)
			}
		})
	}
}

func addWorktree(t *testing.T, r Repo, branch string) string {
	t.Helper()

	c, err := CreateWorktree(t.Context(), r, NewWorktree{Branch: branch, Root: RootInside})
	if err != nil {
		t.Fatal(err)
	}

	return c.Worktree.Path
}

func TestRemoveWorktree(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		setup   func(t *testing.T, path string)
		wantErr error
		removed bool
	}{
		{name: "clean", setup: func(*testing.T, string) {}, removed: true},
		{name: "modified", setup: func(t *testing.T, path string) { writeFile(t, path, "a.txt", "changed\n") }, wantErr: ErrWorktreeDirty},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			_, r := seededRepo(t)
			path := addWorktree(t, r, "feat")
			tt.setup(t, path)

			ctx, log := wrapGit(t, "")

			err := RemoveWorktree(ctx, r, path)
			if tt.wantErr != nil && !errors.Is(err, tt.wantErr) {
				t.Fatalf("err = %v, want %v", err, tt.wantErr)
			}

			if tt.wantErr == nil && err != nil {
				t.Fatal(err)
			}

			_, statErr := os.Stat(path)
			if tt.removed != errors.Is(statErr, os.ErrNotExist) {
				t.Errorf("removed = %v, stat err = %v", tt.removed, statErr)
			}

			list, err := ListWorktrees(t.Context(), r)
			if err != nil {
				t.Fatal(err)
			}

			listed := false

			for _, w := range list {
				listed = listed || w.Path == path
			}

			if listed == tt.removed {
				t.Errorf("listed = %v, want %v", listed, !tt.removed)
			}

			assertNeverForced(t, log, tt.removed)
		})
	}

	t.Run("locked", func(t *testing.T) {
		t.Parallel()

		_, r := seededRepo(t)
		path := addWorktree(t, r, "feat")
		gitOut(t, r.Root, "worktree", "lock", path)

		err := RemoveWorktree(t.Context(), r, path)
		if err == nil {
			t.Fatal("removing a locked worktree succeeded")
		}

		if errors.Is(err, ErrWorktreeDirty) {
			t.Errorf("err = %v, want git's reason, not ErrWorktreeDirty", err)
		}

		if _, err := os.Stat(path); err != nil {
			t.Errorf("worktree gone: %v", err)
		}
	})
}

// assertNeverForced fails when a logged `worktree remove` carries --force or -f,
// or, with want set, when none was logged.
func assertNeverForced(t *testing.T, logPath string, want bool) {
	t.Helper()

	var seen bool

	for line := range strings.SplitSeq(wrappedCalls(t, logPath), "\n") {
		if !strings.Contains(line, "worktree remove") {
			continue
		}

		seen = true

		for f := range strings.FieldsSeq(line) {
			if f == "--force" || f == "-f" {
				t.Errorf("forced: %q", line)
			}
		}
	}

	if want && !seen {
		t.Error("no worktree remove call")
	}
}

func TestBranchAhead(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		setup     func(t *testing.T, dir string) (head string) // returns the merged head
		branch    string
		wantAhead int
		wantErr   error
		anyErr    bool
	}{
		{"tip at the head", func(t *testing.T, dir string) string {
			gitOut(t, dir, "branch", "feat")

			return gitOut(t, dir, "rev-parse", "feat")
		}, "feat", 0, nil, false},
		{"behind the head", func(t *testing.T, dir string) string {
			gitOut(t, dir, "branch", "feat")
			writeFile(t, dir, "b.txt", "b\n")
			commitAll(t, dir, "more")

			return gitOut(t, dir, "rev-parse", "HEAD")
		}, "feat", 0, nil, false},
		{"two commits past the head", func(t *testing.T, dir string) string {
			gitOut(t, dir, "branch", "feat")
			head := gitOut(t, dir, "rev-parse", "feat")
			gitOut(t, dir, "checkout", "-q", "feat")
			writeFile(t, dir, "b.txt", "b\n")
			commitAll(t, dir, "b")
			writeFile(t, dir, "c.txt", "c\n")
			commitAll(t, dir, "c")

			return head
		}, "feat", 2, nil, false},
		{"head unknown here", func(t *testing.T, dir string) string {
			gitOut(t, dir, "branch", "feat")
			writeFile(t, dir, "b.txt", "b\n")
			commitAll(t, dir, "more")
			gitOut(t, dir, "checkout", "-q", "feat")

			return strings.Repeat("ab", 20)
		}, "feat", 0, ErrNoCommit, true},
		{"missing branch", func(t *testing.T, dir string) string {
			return gitOut(t, dir, "rev-parse", "HEAD")
		}, "nope", 0, nil, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			dir, r := seededRepo(t)
			head := tt.setup(t, dir)

			tip, ahead, err := BranchAhead(t.Context(), r, tt.branch, head)

			switch {
			case tt.wantErr != nil:
				if !errors.Is(err, tt.wantErr) {
					t.Fatalf("err = %v, want %v", err, tt.wantErr)
				}

				return
			case tt.anyErr:
				if err == nil {
					t.Fatal("want an error")
				}

				return
			case err != nil:
				t.Fatal(err)
			}

			if ahead != tt.wantAhead {
				t.Errorf("ahead = %d, want %d", ahead, tt.wantAhead)
			}

			if want := gitOut(t, dir, "rev-parse", "refs/heads/"+tt.branch); tip != want {
				t.Errorf("tip = %q, want %q", tip, want)
			}
		})
	}
}

func TestDeleteBranch(t *testing.T) {
	t.Parallel()

	branchExists := func(t *testing.T, dir, name string) bool {
		t.Helper()

		return gitOut(t, dir, "branch", "--list", name) != ""
	}

	tests := []struct {
		name       string
		branch     string
		setup      func(t *testing.T, dir string, r Repo) (tip string)
		wantErr    error
		anyErr     bool
		wantExists bool
	}{
		{"deleted", "feat", func(t *testing.T, dir string, _ Repo) string {
			gitOut(t, dir, "branch", "feat")

			return gitOut(t, dir, "rev-parse", "feat")
		}, nil, false, false},
		{"tip moved", "feat", func(t *testing.T, dir string, _ Repo) string {
			gitOut(t, dir, "branch", "feat")
			old := gitOut(t, dir, "rev-parse", "feat")
			gitOut(t, dir, "checkout", "-q", "feat")
			writeFile(t, dir, "b.txt", "b\n")
			commitAll(t, dir, "b")
			gitOut(t, dir, "checkout", "-q", "main")

			return old
		}, ErrBranchMoved, true, true},
		{"checked out in another worktree", "feat", func(t *testing.T, _ string, r Repo) string {
			addWorktree(t, r, "feat")

			return gitOut(t, r.Root, "rev-parse", "feat")
		}, nil, true, true},
		{"name starting with a dash", "-D", func(t *testing.T, dir string, _ Repo) string {
			return gitOut(t, dir, "rev-parse", "HEAD")
		}, ErrBadBranch, true, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			dir, r := seededRepo(t)
			tip := tt.setup(t, dir, r)

			err := DeleteBranch(t.Context(), r, tt.branch, tip)

			switch {
			case tt.wantErr != nil && !errors.Is(err, tt.wantErr):
				t.Fatalf("err = %v, want %v", err, tt.wantErr)
			case tt.wantErr == nil && tt.anyErr && err == nil:
				t.Fatal("want an error")
			case !tt.anyErr && err != nil:
				t.Fatal(err)
			}

			if strings.HasPrefix(tt.branch, "-") {
				return
			}

			if got := branchExists(t, dir, tt.branch); got != tt.wantExists {
				t.Errorf("branch exists = %v, want %v", got, tt.wantExists)
			}
		})
	}
}
