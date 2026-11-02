package git

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"testing"
)

// mdEntry is one file of a temp tree; a non-empty symlink makes it a link.
type mdEntry struct {
	path    string
	symlink string
}

func buildMDTree(t *testing.T, entries []mdEntry) string {
	t.Helper()

	root := t.TempDir()

	for _, e := range entries {
		p := filepath.Join(root, filepath.FromSlash(e.path))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}

		if e.symlink != "" {
			if err := os.Symlink(filepath.Join(root, filepath.FromSlash(e.symlink)), p); err != nil {
				t.Fatal(err)
			}

			continue
		}

		if err := os.WriteFile(p, []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	return root
}

func TestMarkdownFiles(t *testing.T) {
	t.Parallel()

	if maxMarkdownFiles != 5000 {
		t.Errorf("maxMarkdownFiles = %d, want 5000", maxMarkdownFiles)
	}

	tests := []struct {
		name    string
		entries []mdEntry
		want    []string
	}{
		{
			name: "tracked untracked and ignored all listed, sorted",
			entries: []mdEntry{
				{path: "z.md"},
				{path: "docs/a.md"},
				{path: "docs/b.txt"},
				{path: ".gitignore"},
				{path: "ignored/c.md"},
				{path: "a-b/d.md"},
			},
			want: []string{"a-b/d.md", "docs/a.md", "ignored/c.md", "z.md"},
		},
		{
			name:    "extensions",
			entries: []mdEntry{{path: "README.MD"}, {path: "x.markdown"}, {path: "x.mdx"}, {path: "md"}},
			want:    []string{"README.MD", "x.markdown"},
		},
		{
			name: "skipped folders at any depth",
			entries: []mdEntry{
				{path: "node_modules/b.md"},
				{path: "pkg/vendor/e.md"},
				{path: "pkg/.git/f.md"},
				{path: "keep.md"},
			},
			want: []string{"keep.md"},
		},
		{
			name: "nested checkout and worktree skipped",
			entries: []mdEntry{
				{path: "sub/.git/HEAD"},
				{path: "sub/a.md"},
				{path: "sub/deep/b.md"},
				{path: "wt/.git"},
				{path: "wt/c.md"},
				{path: "ok/d.md"},
			},
			want: []string{"ok/d.md"},
		},
		{
			name:    "root holding a .git folder is still listed",
			entries: []mdEntry{{path: ".git/HEAD"}, {path: "a.md"}, {path: "docs/b.md"}},
			want:    []string{"a.md", "docs/b.md"},
		},
		{
			name: "symlinked folder not followed, symlinked file listed",
			entries: []mdEntry{
				{path: "real/a.md"},
				{path: "link", symlink: "real"},
				{path: "alias.md", symlink: "real/a.md"},
			},
			want: []string{"alias.md", "real/a.md"},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got, err := MarkdownFiles(context.Background(), buildMDTree(t, tc.entries))
			if err != nil {
				t.Fatal(err)
			}

			if !slices.Equal(got, tc.want) {
				t.Errorf("got %v, want %v", got, tc.want)
			}
		})
	}
}

func TestMarkdownFilesSymlinkedRoot(t *testing.T) {
	t.Parallel()

	target := buildMDTree(t, []mdEntry{{path: "a.md"}, {path: "docs/b.md"}})
	link := filepath.Join(t.TempDir(), "link")

	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}

	got, err := MarkdownFiles(context.Background(), link)
	if err != nil {
		t.Fatal(err)
	}

	if want := []string{"a.md", "docs/b.md"}; !slices.Equal(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

func TestMarkdownFilesCap(t *testing.T) {
	t.Parallel()

	entries := make([]mdEntry, 0, 15)
	for i := range 15 {
		entries = append(entries, mdEntry{path: fmt.Sprintf("d%d/f%02d.md", i%3, i)})
	}

	got, err := markdownFiles(context.Background(), buildMDTree(t, entries), 5)
	if err != nil {
		t.Fatalf("the cap is not an error: %v", err)
	}

	if len(got) != 5 {
		t.Fatalf("len = %d, want 5", len(got))
	}

	if !slices.IsSorted(got) {
		t.Error("capped result is not sorted")
	}
}

// expiresAfter is a context whose deadline passes at its n+1th check.
type expiresAfter struct {
	context.Context //nolint:containedctx // a test double that overrides Err
	left            int
}

func (c *expiresAfter) Err() error {
	if c.left <= 0 {
		return context.DeadlineExceeded
	}

	c.left--

	return nil
}

func TestMarkdownFilesDeadline(t *testing.T) {
	t.Parallel()

	root := buildMDTree(t, []mdEntry{{path: "a/x.md"}, {path: "b/y.md"}, {path: "c/z.md"}})

	// The walk checks the root and then each folder it enters.
	got, err := MarkdownFiles(&expiresAfter{Context: context.Background(), left: 3}, root)
	if err != nil {
		t.Fatalf("a deadline after some files is not an error: %v", err)
	}

	if want := []string{"a/x.md", "b/y.md"}; !slices.Equal(got, want) {
		t.Errorf("got %v, want the files found before the deadline %v", got, want)
	}

	if _, err := MarkdownFiles(&expiresAfter{Context: context.Background()}, root); !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("a deadline before any file: err = %v, want context.DeadlineExceeded", err)
	}
}

func TestMarkdownFilesErrors(t *testing.T) {
	t.Parallel()

	t.Run("cancelled context", func(t *testing.T) {
		t.Parallel()

		ctx, cancel := context.WithCancel(context.Background())
		cancel()

		_, err := MarkdownFiles(ctx, buildMDTree(t, []mdEntry{{path: "a.md"}}))
		if !errors.Is(err, context.Canceled) {
			t.Errorf("err = %v, want context.Canceled", err)
		}
	})

	t.Run("unreadable root", func(t *testing.T) {
		t.Parallel()

		if _, err := MarkdownFiles(context.Background(), filepath.Join(t.TempDir(), "missing")); err == nil {
			t.Error("want an error for a missing root")
		}
	})
}
