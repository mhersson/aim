package git

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestOpen(t *testing.T) {
	t.Parallel()
	root := newRepo(t)
	writeFile(t, root, "sub/a.txt", "a\n")
	commitAll(t, root, "init")

	linked := filepath.Join(t.TempDir(), "linked")
	if resolved, err := filepath.EvalSymlinks(filepath.Dir(linked)); err == nil {
		linked = filepath.Join(resolved, "linked")
	}

	gitOut(t, root, "worktree", "add", "-q", "-b", "feat", linked)

	tests := []struct {
		name string
		dir  string
		want Repo
	}{
		{"root", root, Repo{Root: root, GitDir: filepath.Join(root, ".git"), CommonDir: filepath.Join(root, ".git")}},
		{"subdirectory", filepath.Join(root, "sub"), Repo{Root: root, GitDir: filepath.Join(root, ".git"), CommonDir: filepath.Join(root, ".git")}},
		{"linked worktree", linked, Repo{
			Root:      linked,
			GitDir:    filepath.Join(root, ".git", "worktrees", "linked"),
			CommonDir: filepath.Join(root, ".git"),
		}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got, err := Open(context.Background(), tt.dir)
			if err != nil {
				t.Fatal(err)
			}

			if got != tt.want {
				t.Errorf("Open = %+v, want %+v", got, tt.want)
			}
		})
	}
}

func TestOpenOutsideRepo(t *testing.T) {
	t.Parallel()

	_, err := Open(context.Background(), t.TempDir())
	if !errors.Is(err, ErrNotRepo) {
		t.Fatalf("err = %v, want ErrNotRepo", err)
	}
}

func TestOpenBareIsNotAWorkTree(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	gitOut(t, dir, "init", "-q", "--bare")

	if _, err := Open(context.Background(), dir); !errors.Is(err, ErrNotRepo) {
		t.Fatalf("err = %v, want ErrNotRepo", err)
	}
}

func TestOpenFailureIsNotErrNotRepo(t *testing.T) {
	root := newRepo(t)
	cfg := filepath.Join(t.TempDir(), "badconfig")

	if err := os.WriteFile(cfg, []byte("[core\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	t.Setenv("GIT_CONFIG_GLOBAL", cfg)

	_, err := Open(context.Background(), root)
	if err == nil || errors.Is(err, ErrNotRepo) {
		t.Fatalf("err = %v, want a failure that is not ErrNotRepo", err)
	}

	if !strings.Contains(err.Error(), "bad config") {
		t.Errorf("err = %q lacks git's stderr line", err)
	}
}

func TestMainRoot(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		repo    Repo
		want    string
		wantErr error
	}{
		{"main checkout", Repo{CommonDir: "/r/proj/.git"}, "/r/proj", nil},
		{"bare", Repo{CommonDir: "/r/proj.git"}, "", ErrBare},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := tt.repo.MainRoot()
			if !errors.Is(err, tt.wantErr) || got != tt.want {
				t.Errorf("MainRoot = %q, %v; want %q, %v", got, err, tt.want, tt.wantErr)
			}
		})
	}
}

func TestOpenThroughSymlinkedDir(t *testing.T) {
	t.Parallel()
	root := newRepo(t)
	writeFile(t, root, "sub/a.txt", "a\n")
	commitAll(t, root, "init")

	link := filepath.Join(resolvedTemp(t), "link")
	if err := os.Symlink(root, link); err != nil {
		t.Fatal(err)
	}

	for _, dir := range []string{link, filepath.Join(link, "sub")} {
		got, err := Open(t.Context(), dir)
		if err != nil {
			t.Fatal(err)
		}

		if want := (Repo{Root: root, GitDir: filepath.Join(root, ".git"), CommonDir: filepath.Join(root, ".git")}); got != want {
			t.Errorf("Open(%s) = %+v, want %+v", dir, got, want)
		}
	}
}
