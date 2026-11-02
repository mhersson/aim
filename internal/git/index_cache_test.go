package git

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"syscall"
	"testing"
	"time"
)

// cacheRepo is a repository with two tracked files, a cache dir and a temp dir.
func cacheRepo(t *testing.T) (dir, cache, tmp string, r Repo) {
	t.Helper()

	dir = newRepo(t)
	writeFile(t, dir, "a.txt", "alpha\n")
	writeFile(t, dir, "b.txt", "beta\n")
	commitAll(t, dir, "init")

	r, err := Open(context.Background(), dir)
	if err != nil {
		t.Fatal(err)
	}

	return dir, t.TempDir(), t.TempDir(), r
}

func treeOf(t *testing.T, r Repo, cache, tmp string) WorkTree {
	t.Helper()

	wt, err := WorkingTree(context.Background(), r, TreeOptions{TempDir: tmp, IndexCache: cache})
	if err != nil {
		t.Fatal(err)
	}

	return wt
}

func cacheFiles(t *testing.T, cache string) []string {
	t.Helper()

	files, err := filepath.Glob(filepath.Join(cache, "index", "*.idx"))
	if err != nil {
		t.Fatal(err)
	}

	return files
}

// wantCacheName is the cache file name for r's real index as it is now.
func wantCacheName(t *testing.T, r Repo) string {
	t.Helper()

	info, err := os.Stat(filepath.Join(r.GitDir, "index"))
	if err != nil {
		t.Fatal(err)
	}

	sum := sha256.Sum256([]byte(r.GitDir))

	return fmt.Sprintf("%s-%d-%d-%d.idx", hex.EncodeToString(sum[:])[:16],
		info.Size(), info.ModTime().UnixNano(), info.Sys().(*syscall.Stat_t).Ino)
}

// A hit with nothing left to stage leaves the cache file untouched: same inode
// and mtime, where a miss would have written a new one.
func TestWorkingTreeReusesTheCache(t *testing.T) {
	t.Parallel()
	dir, cache, tmp, r := cacheRepo(t)
	writeFile(t, dir, "a.txt", "changed\n")
	treeOf(t, r, cache, tmp)

	files := cacheFiles(t, cache)
	if len(files) != 1 || filepath.Base(files[0]) != wantCacheName(t, r) {
		t.Fatalf("cache files = %v, want one named %s", files, wantCacheName(t, r))
	}

	assertEmptyDir(t, tmp)

	file := files[0]

	before, err := os.Stat(file)
	if err != nil {
		t.Fatal(err)
	}

	treeOf(t, r, cache, tmp)

	after, err := os.Stat(file)
	if err != nil {
		t.Fatal(err)
	}

	if !os.SameFile(before, after) || !before.ModTime().Equal(after.ModTime()) {
		t.Fatal("cache file was rewritten on a hit")
	}
}

func TestWorkingTreeIgnoresAStaleCache(t *testing.T) {
	t.Parallel()
	dir, cache, tmp, r := cacheRepo(t)
	treeOf(t, r, cache, tmp)

	old := cacheFiles(t, cache)
	if len(old) != 1 {
		t.Fatalf("cache files = %v", old)
	}

	writeFile(t, dir, "c.txt", "gamma\n")
	gitOut(t, dir, "add", "c.txt")

	want := gitOut(t, dir, "write-tree") // may rewrite the index, so before the call under test

	if wt := treeOf(t, r, cache, tmp); wt.Tree != want {
		t.Fatalf("tree = %s, want %s", wt.Tree, want)
	}

	files := cacheFiles(t, cache)
	if len(files) != 1 || filepath.Base(files[0]) != wantCacheName(t, r) || files[0] == old[0] {
		t.Fatalf("cache files = %v, want only %s", files, wantCacheName(t, r))
	}
}

func TestWorkingTreeNeverWritesTheUserIndex(t *testing.T) {
	t.Parallel()
	dir, cache, tmp, r := cacheRepo(t)
	writeFile(t, dir, "a.txt", "changed\n")
	writeFile(t, dir, "new.txt", "new\n")

	path := filepath.Join(r.GitDir, "index")
	data, _ := os.ReadFile(path)
	before, _ := os.Stat(path)

	for range 2 {
		treeOf(t, r, cache, tmp)
	}

	got, _ := os.ReadFile(path)
	after, _ := os.Stat(path)

	if !bytes.Equal(data, got) || !before.ModTime().Equal(after.ModTime()) {
		t.Fatal("the user's index changed")
	}
}

func TestTwoWorktreesKeepTwoCaches(t *testing.T) {
	t.Parallel()
	dir, cache, tmp, r := cacheRepo(t)
	other := filepath.Join(filepath.Dir(dir), filepath.Base(dir)+"-wt")
	gitOut(t, dir, "worktree", "add", "-q", "-b", "side", other)

	r2, err := Open(context.Background(), other)
	if err != nil {
		t.Fatal(err)
	}

	treeOf(t, r, cache, tmp)
	treeOf(t, r2, cache, tmp)

	if files := cacheFiles(t, cache); len(files) != 2 {
		t.Fatalf("cache files = %v, want two", files)
	}

	if names := []string{wantCacheName(t, r), wantCacheName(t, r2)}; names[0] == names[1] {
		t.Fatal("two worktrees share a cache name")
	}
}

func TestCacheWriteIsAtomic(t *testing.T) {
	t.Parallel()

	dir, cache, tmp, r := cacheRepo(t)
	writeFile(t, dir, "f.txt", "x\n")

	treeOf(t, r, cache, tmp)

	file := cacheFiles(t, cache)[0]

	want, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}

	c := openIndexCache(cache, r.GitDir, filepath.Join(r.GitDir, "index"))
	next := filepath.Join(t.TempDir(), "next")

	body := append(bytes.Clone(want), make([]byte, 1<<20)...)

	//nolint:gosec // path under t.TempDir
	if err := os.WriteFile(next, body, 0o600); err != nil {
		t.Fatal(err)
	}

	var wg sync.WaitGroup

	done := make(chan struct{})

	wg.Go(func() {
		for range 20 {
			c.store(next)
		}

		close(done)
	})

	for {
		got, err := os.ReadFile(file)
		if err == nil && !bytes.Equal(got, body) && !bytes.Equal(got, want) {
			t.Fatalf("partial cache file of %d bytes", len(got))
		}

		select {
		case <-done:
			wg.Wait()

			return
		default:
		}
	}
}

// The cache file's mtime is set equal to the cached entry's mtime, so git
// must treat the entry as racy and compare content. A copy stamped with the
// copy time instead would trust the entry's size and mtime and miss the edit.
func TestWorkingTreeSeesAnEditAfterTheCache(t *testing.T) {
	t.Parallel()
	dir, cache, tmp, r := cacheRepo(t)
	// The rewrite changes ctime, which would mask the mtime check.
	gitOut(t, dir, "config", "core.trustctime", "false")

	past := time.Now().Add(-time.Hour)
	file := filepath.Join(dir, "a.txt")

	writeFile(t, dir, "a.txt", "aaaa\n")

	if err := os.Chtimes(file, past, past); err != nil {
		t.Fatal(err)
	}

	treeOf(t, r, cache, tmp)

	cached := cacheFiles(t, cache)[0]
	if err := os.Chtimes(cached, past, past); err != nil {
		t.Fatal(err)
	}

	writeFile(t, dir, "a.txt", "bbbb\n")

	if err := os.Chtimes(file, past, past); err != nil {
		t.Fatal(err)
	}

	wt := treeOf(t, r, cache, tmp)
	if got := gitOut(t, dir, "rev-parse", wt.Tree+":a.txt"); got != gitOut(t, dir, "hash-object", "a.txt") {
		t.Fatal("tree has the stale a.txt")
	}
}

func TestWorkingTreeKeepsADeletedTrackedFileTracked(t *testing.T) {
	t.Parallel()
	dir, cache, tmp, r := cacheRepo(t)

	if err := os.Remove(filepath.Join(dir, "a.txt")); err != nil {
		t.Fatal(err)
	}

	if wt := treeOf(t, r, cache, tmp); gitOut(t, dir, "ls-tree", "--name-only", wt.Tree) != "b.txt" {
		t.Fatal("deleted file is still in the tree")
	}

	writeFile(t, dir, "a.txt", "alpha\n")

	wt := treeOf(t, r, cache, tmp)
	if len(wt.Untracked) != 0 {
		t.Fatalf("untracked = %v, want none", wt.Untracked)
	}

	if got, want := gitOut(t, dir, "rev-parse", wt.Tree+":a.txt"), gitOut(t, dir, "rev-parse", "HEAD:a.txt"); got != want {
		t.Fatalf("a.txt = %s, want %s", got, want)
	}
}

func TestWorkingTreeKeepsUntrackedOutOfTheCache(t *testing.T) {
	t.Parallel()
	dir, cache, tmp, r := cacheRepo(t)
	writeFile(t, dir, "new.txt", "new\n")

	for range 2 {
		if wt := treeOf(t, r, cache, tmp); len(wt.Untracked) != 1 || wt.Untracked[0] != "new.txt" {
			t.Fatalf("untracked = %v", wt.Untracked)
		}
	}
}

func TestSweepIndexCache(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 11, 2, 12, 0, 0, 0, time.UTC)

	const day = 24 * time.Hour

	files := []struct {
		name string
		at   time.Time
		gone bool
	}{
		{"aaaa-1-2-3.idx", now.Add(-15 * day), true},
		{"bbbb-1-2-3.idx", now.Add(-13 * day), false},
		{"cccc-1-2-3.idx", now.Add(-14 * day), false},
		{".tmp-old", now.Add(-2 * time.Hour), true},
		{".tmp-new", now.Add(-30 * time.Minute), false},
		{"notes.txt", now.Add(-30 * day), false},
	}

	root := t.TempDir()
	dir := filepath.Join(root, "index")

	if err := os.MkdirAll(filepath.Join(dir, "old.idx"), 0o700); err != nil {
		t.Fatal(err)
	}

	if err := os.Chtimes(filepath.Join(dir, "old.idx"), now.Add(-30*day), now.Add(-30*day)); err != nil {
		t.Fatal(err)
	}

	for _, f := range files {
		p := filepath.Join(dir, f.name)
		if err := os.WriteFile(p, []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}

		if err := os.Chtimes(p, f.at, f.at); err != nil {
			t.Fatal(err)
		}
	}

	// A symlink named *.idx is not a regular file, whatever its target's age.
	if err := os.WriteFile(filepath.Join(dir, "target.dat"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}

	if err := os.Chtimes(filepath.Join(dir, "target.dat"), now.Add(-30*day), now.Add(-30*day)); err != nil {
		t.Fatal(err)
	}

	if err := os.Symlink("target.dat", filepath.Join(dir, "link.idx")); err != nil {
		t.Fatal(err)
	}

	n, err := SweepIndexCache(root, now)
	if err != nil || n != 2 {
		t.Fatalf("SweepIndexCache = %d, %v; want 2, nil", n, err)
	}

	for _, f := range files {
		_, err := os.Stat(filepath.Join(dir, f.name))
		if gone := os.IsNotExist(err); gone != f.gone {
			t.Errorf("%s: gone = %v, want %v", f.name, gone, f.gone)
		}
	}

	if _, err := os.Stat(filepath.Join(dir, "old.idx")); err != nil {
		t.Errorf("directory old.idx: %v", err)
	}

	if _, err := os.Lstat(filepath.Join(dir, "link.idx")); err != nil {
		t.Errorf("symlink link.idx: %v", err)
	}

	if n, err := SweepIndexCache("", now); n != 0 || err != nil {
		t.Errorf("empty root = %d, %v; want 0, nil", n, err)
	}

	if n, err := SweepIndexCache(t.TempDir(), now); n != 0 || err != nil {
		t.Errorf("root without index = %d, %v; want 0, nil", n, err)
	}
}
