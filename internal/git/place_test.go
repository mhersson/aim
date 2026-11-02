package git

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

// resolvedTemp is a temp dir without symlinks in its path.
func resolvedTemp(t *testing.T) string {
	t.Helper()

	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}

	return dir
}

func TestEditPlace(t *testing.T) {
	t.Parallel()

	proj := newRepo(t)
	writeFile(t, proj, ".gitignore", "*.log\nbuild/\n")
	writeFile(t, proj, "tracked.go", "package p\n")
	writeFile(t, proj, "keep.log", "kept\n")
	gitOut(t, proj, "add", "-f", "keep.log")
	commitAll(t, proj, "init")

	ctx := context.Background()

	repo, err := Open(ctx, proj)
	if err != nil {
		t.Fatal(err)
	}

	created, err := CreateWorktree(ctx, repo, NewWorktree{Branch: "feat", Root: RootInside})
	if err != nil {
		t.Fatal(err)
	}

	inner := filepath.Join(proj, "inner")
	if err := os.MkdirAll(inner, 0o755); err != nil {
		t.Fatal(err)
	}

	gitOut(t, inner, "init", "-q", "-b", "main")

	spaced := filepath.Join(resolvedTemp(t), "wt ø 1")
	gitOut(t, proj, "worktree", "add", "-q", "-b", "spaced", spaced)

	plain := resolvedTemp(t)

	linkDir := t.TempDir()
	if err := os.Symlink(proj, filepath.Join(linkDir, "link")); err != nil {
		t.Fatal(err)
	}

	if err := os.Symlink(filepath.Join(proj, ".git"), filepath.Join(linkDir, "gitlink")); err != nil {
		t.Fatal(err)
	}

	projPlace := Place{Root: proj, Common: filepath.Join(proj, ".git"), Repo: filepath.Base(proj)}
	linkedPlace := func(root string) Place {
		return Place{Root: root, Common: projPlace.Common, Repo: projPlace.Repo}
	}

	tests := []struct {
		name    string
		path    string
		want    Place
		wantErr error
	}{
		{"tracked file", filepath.Join(proj, "tracked.go"), projPlace, nil},
		{"new file under new directories", filepath.Join(proj, "a", "b", "new.go"), projPlace, nil},
		{"linked worktree", filepath.Join(created.Worktree.Path, "x.go"), linkedPlace(created.Worktree.Path), nil},
		{"ignored by pattern", filepath.Join(proj, "debug.log"), Place{}, ErrNotCounted},
		{"tracked file matching a pattern", filepath.Join(proj, "keep.log"), projPlace, nil},
		{"ignored directory", filepath.Join(proj, "build", "out.txt"), Place{}, ErrNotCounted},
		{"inside the git dir", filepath.Join(proj, ".git", "info", "exclude"), Place{}, ErrNotCounted},
		{"inside the git dir in other case", filepath.Join(proj, ".GIT", "config"), Place{}, ErrNotCounted},
		{"git dir behind a symlink", filepath.Join(linkDir, "gitlink", "config"), Place{}, ErrNotCounted},
		{"outside any work tree", filepath.Join(plain, "a.go"), Place{}, ErrNotCounted},
		{"relative path", filepath.Join("rel", "a.go"), Place{}, ErrNotCounted},
		{"nested repository", filepath.Join(inner, "y.go"), Place{
			Root: inner, Common: filepath.Join(inner, ".git"), Repo: "inner",
		}, nil},
		{"symlinked parent", filepath.Join(linkDir, "link", "a.txt"), projPlace, nil},
		{"path with spaces and non-ASCII", filepath.Join(spaced, "f.go"), linkedPlace(spaced), nil},
		{"name that reads as pathspec magic", filepath.Join(proj, ":(top)build", "x.txt"), projPlace, nil},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got, err := EditPlace(ctx, tt.path)
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("EditPlace(%q) error = %v, want %v", tt.path, err, tt.wantErr)
			}

			if got != tt.want {
				t.Errorf("EditPlace(%q) = %+v, want %+v", tt.path, got, tt.want)
			}
		})
	}
}

func TestEditPlaceFailureIsNotRefusal(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	writeFile(t, dir, "a.go", "package p\n")

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := EditPlace(ctx, filepath.Join(dir, "a.go"))
	if err == nil || errors.Is(err, ErrNotCounted) {
		t.Fatalf("err = %v, want a failure that is not ErrNotCounted", err)
	}

	if !errors.Is(err, context.Canceled) {
		t.Errorf("err = %v, want it to wrap the context's error", err)
	}
}

func TestRepoName(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		commonDir string
		want      string
	}{
		{"main checkout", filepath.Join("work", "proj", ".git"), "proj"},
		{"bare repository", filepath.Join("srv", "proj.git"), "proj"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if got := (Repo{CommonDir: tt.commonDir}).Name(); got != tt.want {
				t.Errorf("Name = %q, want %q", got, tt.want)
			}
		})
	}

	// Open must give a worktree of a bare repository the bare common dir.
	main := newRepo(t)
	writeFile(t, main, "a.txt", "a\n")
	commitAll(t, main, "init")

	bare := filepath.Join(resolvedTemp(t), "proj.git")
	gitOut(t, main, "clone", "-q", "--bare", main, bare)

	bareLinked := filepath.Join(resolvedTemp(t), "bare-linked")
	gitOut(t, bare, "worktree", "add", "-q", "-b", "other", bareLinked)

	repo, err := Open(context.Background(), bareLinked)
	if err != nil {
		t.Fatal(err)
	}

	if got := repo.Name(); got != "proj" {
		t.Errorf("Name of a worktree of a bare repository = %q, want proj", got)
	}
}
