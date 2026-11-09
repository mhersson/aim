package git

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestCopyIncluded(t *testing.T) {
	t.Parallel()
	main, _ := seededRepo(t)
	dst := t.TempDir()

	writeFile(t, main, ".gitignore", ".env\n*.local\nconfig/\n")
	writeFile(t, main, IncludeFile, ".env\n*.local\nconfig/**\nnotignored.txt\n")
	writeFile(t, main, ".env", "SECRET=1\n")
	writeFile(t, main, "tracked.local", "tracked\n")
	writeFile(t, main, "notignored.txt", "x\n")
	writeFile(t, main, "config/deep/app.local", "nested\n")
	writeFile(t, main, "keep.local", "main\n")
	writeFile(t, dst, "keep.local", "dst\n")

	if err := os.Chmod(filepath.Join(main, ".env"), 0o666); err != nil {
		t.Fatal(err)
	}

	if err := os.Symlink(".env", filepath.Join(main, "link.local")); err != nil {
		t.Fatal(err)
	}

	gitOut(t, main, "add", "-f", "tracked.local")

	n, err := CopyIncluded(t.Context(), main, dst)
	if err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		path     string
		want     string // "" = absent
		wantMode os.FileMode
	}{
		{path: ".env", want: "SECRET=1\n", wantMode: 0o666},
		{path: "config/deep/app.local", want: "nested\n"},
		{path: "keep.local", want: "dst\n"},
		{path: "tracked.local"},
		{path: "notignored.txt"},
		{path: "link.local"},
	}

	for _, tt := range tests {
		t.Run(tt.path, func(t *testing.T) {
			got, err := os.ReadFile(filepath.Join(dst, tt.path))

			switch {
			case tt.want == "" && err == nil:
				t.Fatalf("%s was copied", tt.path)
			case tt.want == "":
				return
			case err != nil:
				t.Fatal(err)
			case string(got) != tt.want:
				t.Errorf("content = %q, want %q", got, tt.want)
			}

			if tt.wantMode != 0 {
				fi, err := os.Stat(filepath.Join(dst, tt.path))
				if err != nil {
					t.Fatal(err)
				}

				if fi.Mode().Perm() != tt.wantMode {
					t.Errorf("mode = %v, want %v", fi.Mode().Perm(), tt.wantMode)
				}
			}
		})
	}

	if n != 2 {
		t.Errorf("copied = %d, want 2", n)
	}
}

func TestCopyIncludedStaysInside(t *testing.T) {
	t.Parallel()
	main, _ := seededRepo(t)
	dst, outside := t.TempDir(), t.TempDir()

	writeFile(t, main, ".gitignore", "*.local\n")
	writeFile(t, main, IncludeFile, "*.local\n")
	writeFile(t, main, "conf/app.local", "x\n")

	if err := os.Symlink(outside, filepath.Join(dst, "conf")); err != nil {
		t.Fatal(err)
	}

	n, err := CopyIncluded(t.Context(), main, dst)

	var ce *CopyError
	if !errors.As(err, &ce) || ce.Path != "conf/app.local" {
		t.Fatalf("err = %v, want a CopyError for conf/app.local", err)
	}

	entries, rerr := os.ReadDir(outside)
	if rerr != nil {
		t.Fatal(rerr)
	}

	if n != 0 || len(entries) != 0 {
		t.Errorf("copied = %d, outside holds %d entries, want none", n, len(entries))
	}
}

func TestCopyIncludedCopiesNothing(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		include string // "" = no .worktreeinclude
	}{
		{name: "no include file"},
		{name: "only files git does not ignore", include: "plain.txt\n"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			main, _ := seededRepo(t)
			dst := t.TempDir()

			writeFile(t, main, ".gitignore", ".env\n")
			writeFile(t, main, ".env", "x\n")
			writeFile(t, main, "plain.txt", "x\n")

			if tt.include != "" {
				writeFile(t, main, IncludeFile, tt.include)
			}

			n, err := CopyIncluded(t.Context(), main, dst)
			if n != 0 || err != nil {
				t.Fatalf("CopyIncluded = %d, %v, want 0, nil", n, err)
			}

			entries, rerr := os.ReadDir(dst)
			if rerr != nil {
				t.Fatal(rerr)
			}

			if len(entries) != 0 {
				t.Errorf("dst holds %d entries, want none", len(entries))
			}
		})
	}
}

func TestCreateWorktreeCopiesIncluded(t *testing.T) {
	t.Parallel()
	dir, r := seededRepo(t)

	writeFile(t, dir, ".gitignore", ".env\n")
	writeFile(t, dir, IncludeFile, ".env\n")
	writeFile(t, dir, ".env", "SECRET=1\n")

	got, err := CreateWorktree(t.Context(), r, NewWorktree{Branch: "inc", Root: RootInside})
	if err != nil {
		t.Fatal(err)
	}

	if got.Copied != 1 || got.CopyErr != nil {
		t.Errorf("Copied = %d, CopyErr = %v, want 1, nil", got.Copied, got.CopyErr)
	}

	if s := readFile(t, filepath.Join(got.Worktree.Path, ".env")); s != "SECRET=1\n" {
		t.Errorf(".env = %q", s)
	}
}
