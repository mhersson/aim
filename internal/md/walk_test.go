package md

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"syscall"
	"testing"
	"time"
)

// makeTree creates files (content-less) and folders (trailing slash) under a
// fresh temp dir and returns it.
func makeTree(t *testing.T, paths ...string) string {
	t.Helper()

	root := t.TempDir()

	for _, p := range paths {
		full := filepath.Join(root, filepath.FromSlash(p))
		if strings.HasSuffix(p, "/") {
			if err := os.MkdirAll(full, 0o755); err != nil {
				t.Fatal(err)
			}

			continue
		}

		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}

		if err := os.WriteFile(full, nil, 0o644); err != nil {
			t.Fatal(err)
		}
	}

	return root
}

func collect(ctx context.Context, opts WalkOptions) ([]WalkEntry, WalkStop) {
	var got []WalkEntry

	stop := Walk(ctx, opts, func(b []WalkEntry) { got = append(got, b...) })

	return got, stop
}

func rels(es []WalkEntry) []string {
	out := make([]string, 0, len(es))

	for _, e := range es {
		if e.Dir {
			e.Rel += "/"
		}

		out = append(out, e.Rel)
	}

	return out
}

func TestWalkSkips(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		paths []string
		opts  WalkOptions
		want  []string
	}{
		{
			name:  "hidden entries are skipped by default",
			paths: []string{".h.md", ".hd/in.md", "a.md"},
			want:  []string{"a.md"},
		},
		{
			name:  "hidden entries are listed with Hidden",
			paths: []string{".h.md", ".hd/in.md", "a.md"},
			opts:  WalkOptions{Hidden: true},
			want:  []string{".h.md", ".hd/", "a.md", ".hd/in.md"},
		},
		{
			name:  ".git is skipped even with Hidden",
			paths: []string{".git/x.md", ".github/y.md", "a.md"},
			opts:  WalkOptions{Hidden: true},
			want:  []string{".github/", "a.md", ".github/y.md"},
		},
		{
			name:  "named folders",
			paths: []string{"node_modules/a.md", "vendor/a.md", "target/a.md", "__pycache__/a.md", "src/a.md"},
			want:  []string{"src/", "src/a.md"},
		},
		{
			name:  "pkg/mod but not mod elsewhere",
			paths: []string{"pkg/mod/a.md", "pkg/keep.md", "other/mod/b.md"},
			want:  []string{"other/", "pkg/", "other/mod/", "pkg/keep.md", "other/mod/b.md"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			root := makeTree(t, tt.paths...)
			tt.opts.Root = root

			got, stop := collect(t.Context(), tt.opts)
			if stop != WalkDone {
				t.Fatalf("stop = %v, want done", stop)
			}

			if !slices.Equal(rels(got), tt.want) {
				t.Errorf("got %v, want %v", rels(got), tt.want)
			}
		})
	}
}

func TestWalkRootNamedNodeModules(t *testing.T) {
	t.Parallel()

	root := filepath.Join(makeTree(t, "node_modules/a.md"), "node_modules")

	got, _ := collect(t.Context(), WalkOptions{Root: root})

	if want := []string{"a.md"}; !slices.Equal(rels(got), want) {
		t.Errorf("got %v, want %v", rels(got), want)
	}
}

func TestWalkSkipAbs(t *testing.T) {
	t.Parallel()

	root := makeTree(t, "a/x.md", "b/y.md")

	got, _ := collect(t.Context(), WalkOptions{Root: root, SkipAbs: []string{filepath.Join(root, "a")}})

	if want := []string{"b/", "b/y.md"}; !slices.Equal(rels(got), want) {
		t.Errorf("got %v, want %v", rels(got), want)
	}
}

func TestWalkOtherFilesystem(t *testing.T) {
	t.Parallel()

	root := makeTree(t, "mnt/x.md", "src/y.md")
	mnt := filepath.Join(root, "mnt")

	dev := func(p string) (uint64, bool) {
		if p == mnt {
			return 2, true
		}

		return 1, true
	}

	got, _ := collect(t.Context(), WalkOptions{Root: root, device: dev})

	if want := []string{"src/", "src/y.md"}; !slices.Equal(rels(got), want) {
		t.Errorf("got %v, want %v", rels(got), want)
	}
}

func TestWalkSymlinkedRoot(t *testing.T) {
	t.Parallel()

	target := makeTree(t, "sub/x.md")
	link := filepath.Join(t.TempDir(), "link")

	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}

	// Lstat of the link gives the link's device; the folders below it live on
	// the target's.
	dev := func(p string) (uint64, bool) {
		if p == link {
			return 1, true
		}

		return 2, true
	}

	got, _ := collect(t.Context(), WalkOptions{Root: link, device: dev})

	if want := []string{"sub/", "sub/x.md"}; !slices.Equal(rels(got), want) {
		t.Errorf("got %v, want %v", rels(got), want)
	}
}

// expiring is a context whose deadline passes when expire is called.
type expiring struct {
	context.Context //nolint:containedctx // a test double that overrides Err

	expired bool
}

func (c *expiring) Err() error {
	if c.expired {
		return context.DeadlineExceeded
	}

	return nil
}

func TestWalkTimeLimitKeepsFound(t *testing.T) {
	t.Parallel()

	root := makeTree(t, "a.md", "sub/b.md")
	ctx := &expiring{Context: t.Context()}

	// The deadline passes while the root's entries are handled, before sub is read.

	var got []WalkEntry

	stop := Walk(ctx, WalkOptions{Root: root, device: func(p string) (uint64, bool) {
		// By name: the root's own read comes resolved, and on macOS t.TempDir() is under a symlink.
		if filepath.Base(p) == "sub" {
			ctx.expired = true
		}

		return 1, true
	}}, func(b []WalkEntry) { got = append(got, b...) })
	if stop != WalkTimeLimit {
		t.Fatalf("stop = %v, want time limit", stop)
	}

	if want := []string{"a.md", "sub/"}; !slices.Equal(rels(got), want) {
		t.Errorf("got %v, want %v", rels(got), want)
	}
}

func TestWalkClassification(t *testing.T) {
	t.Parallel()

	root := makeTree(t, "real/in.md", "doc.md", "other.txt")

	links := map[string]string{
		"linkdir":     "real",
		"linkfile.md": "doc.md",
		"broken.md":   "missing.md",
	}
	for name, target := range links {
		if err := os.Symlink(target, filepath.Join(root, name)); err != nil {
			t.Fatal(err)
		}
	}

	if err := syscall.Mkfifo(filepath.Join(root, "fifo.md"), 0o644); err != nil {
		t.Skipf("mkfifo: %v", err)
	}

	got, _ := collect(t.Context(), WalkOptions{Root: root})

	// linkdir is listed as a folder and not entered: no linkdir/in.md.
	want := []string{"doc.md", "linkdir/", "linkfile.md", "real/", "real/in.md"}
	if !slices.Equal(rels(got), want) {
		t.Errorf("got %v, want %v", rels(got), want)
	}
}

func TestWalkBreadthFirst(t *testing.T) {
	t.Parallel()

	root := makeTree(t, "a/deep/z.md", "a/m.md", "b/n.md", "top.md")

	got, _ := collect(t.Context(), WalkOptions{Root: root})

	want := []string{"a/", "b/", "top.md", "a/deep/", "a/m.md", "b/n.md", "a/deep/z.md"}
	if !slices.Equal(rels(got), want) {
		t.Errorf("got %v, want %v", rels(got), want)
	}
}

func TestWalkFileLimit(t *testing.T) {
	t.Parallel()

	root := makeTree(t, "a/deep/z.md", "a/m.md", "b/n.md", "top1.md", "top2.md")

	got, stop := collect(t.Context(), WalkOptions{Root: root, MaxFiles: 3})
	if stop != WalkFileLimit {
		t.Fatalf("stop = %v, want file limit", stop)
	}

	// Folders are not counted, and the shallow files are the ones kept.
	want := []string{"a/", "b/", "top1.md", "top2.md", "a/deep/", "a/m.md"}
	if !slices.Equal(rels(got), want) {
		t.Errorf("got %v, want %v", rels(got), want)
	}
}

func TestWalkExactLimitIsDone(t *testing.T) {
	t.Parallel()

	root := makeTree(t, "a.md", "b.md")

	if _, stop := collect(t.Context(), WalkOptions{Root: root, MaxFiles: 2}); stop != WalkDone {
		t.Errorf("stop = %v, want done", stop)
	}
}

func TestWalkStops(t *testing.T) {
	t.Parallel()

	root := makeTree(t, "a.md")

	t.Run("past deadline", func(t *testing.T) {
		t.Parallel()

		ctx, cancel := context.WithDeadline(t.Context(), time.Unix(1, 0))
		defer cancel()

		got, stop := collect(ctx, WalkOptions{Root: root})
		if stop != WalkTimeLimit || len(got) != 0 {
			t.Errorf("got %v, %v; want nothing, time limit", rels(got), stop)
		}
	})

	t.Run("cancelled", func(t *testing.T) {
		t.Parallel()

		ctx, cancel := context.WithCancel(t.Context())
		cancel()

		if _, stop := collect(ctx, WalkOptions{Root: root}); stop != WalkCancelled {
			t.Errorf("stop = %v, want cancelled", stop)
		}
	})
}

func TestWalkCancelInEmit(t *testing.T) {
	t.Parallel()

	// More than one batch of files in the root, so emit runs before the end;
	// sub is the next folder the walk must not read.
	paths := []string{"sub/late.md"}
	for i := range walkBatch {
		paths = append(paths, fmt.Sprintf("f%05d.md", i))
	}

	root := makeTree(t, paths...)

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()

	var got []WalkEntry

	stop := Walk(ctx, WalkOptions{Root: root}, func(b []WalkEntry) {
		got = append(got, b...)

		cancel()
	})
	if stop != WalkCancelled {
		t.Fatalf("stop = %v, want cancelled", stop)
	}

	if slices.Contains(rels(got), "sub/late.md") {
		t.Error("the walk read the next folder after the cancel")
	}
}

func TestWalkUnreadableFolder(t *testing.T) {
	t.Parallel()

	if os.Geteuid() == 0 {
		t.Skip("root reads every folder")
	}

	root := makeTree(t, "locked/x.md", "ok/y.md")
	locked := filepath.Join(root, "locked")

	if err := os.Chmod(locked, 0); err != nil {
		t.Fatal(err)
	}

	t.Cleanup(func() { _ = os.Chmod(locked, 0o755) })

	got, stop := collect(t.Context(), WalkOptions{Root: root})
	if stop != WalkDone {
		t.Fatalf("stop = %v, want done", stop)
	}

	if want := []string{"locked/", "ok/", "ok/y.md"}; !slices.Equal(rels(got), want) {
		t.Errorf("got %v, want %v", rels(got), want)
	}
}

func TestWalkSkipsList(t *testing.T) {
	t.Parallel()

	tests := []struct {
		goos string
		want []string
	}{
		{"linux", []string{"/proc", "/sys"}},
		{"darwin", []string{"/proc", "/sys", "/Users/me/Library"}},
	}

	for _, tt := range tests {
		t.Run(tt.goos, func(t *testing.T) {
			t.Parallel()

			if got := WalkSkips(tt.goos, "/Users/me"); !slices.Equal(got, tt.want) {
				t.Errorf("got %v, want %v", got, tt.want)
			}
		})
	}
}

func TestReadFolder(t *testing.T) {
	t.Parallel()

	root := makeTree(t, "B.md", "a.md", "Zed/", "alpha/", ".hidden.md", ".dot/",
		"node_modules/", "note.txt", "real/")

	if err := os.Symlink("real", filepath.Join(root, "link")); err != nil {
		t.Fatal(err)
	}

	got, err := ReadFolder(root)
	if err != nil {
		t.Fatal(err)
	}

	var names []string

	for _, e := range got {
		if e.Dir {
			e.Name += "/"
		}

		names = append(names, e.Name)
	}

	// Folders first, then files; each case-folded with the raw name as the tie
	// break; hidden and node_modules included; the symlinked folder is a folder.
	want := []string{".dot/", "alpha/", "link/", "node_modules/", "real/", "Zed/", ".hidden.md", "a.md", "B.md"}
	if !slices.Equal(names, want) {
		t.Errorf("got %v, want %v", names, want)
	}
}

func TestSortFolder(t *testing.T) {
	t.Parallel()

	es := []FolderEntry{{"b.md", false}, {"B.md", false}, {"Z", true}, {"a", true}}
	sortFolder(es)

	want := []FolderEntry{{"a", true}, {"Z", true}, {"B.md", false}, {"b.md", false}}
	if !slices.Equal(es, want) {
		t.Errorf("got %v, want %v", es, want)
	}
}

func TestReadFolderError(t *testing.T) {
	t.Parallel()

	_, err := ReadFolder(filepath.Join(t.TempDir(), "missing"))

	if _, ok := errors.AsType[*fs.PathError](err); !ok {
		t.Fatalf("err = %v, want a *fs.PathError inside", err)
	}
}
