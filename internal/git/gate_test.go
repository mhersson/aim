package git

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"
)

// bigRepo commits n files in 50 directories on main and branches to feature.
func bigRepo(t *testing.T, n int) string {
	t.Helper()

	dir := newRepo(t)
	for i := range n {
		writeFile(t, dir, fmt.Sprintf("d%02d/f%04d.txt", i%50, i), numbered("line", 20))
	}

	commitAll(t, dir, "base")
	gitOut(t, dir, "checkout", "-q", "-b", "feature")

	return dir
}

// saveToDiff writes content to rel, waits for the watcher batch that names it
// and returns the Update of prev with that batch.
func saveToDiff(t *testing.T, w *Watcher, e *Engine, prev Result, src Source, rel, content string) Result {
	t.Helper()

	dir := src.(BranchSource).Worktree

	if err := os.WriteFile(filepath.Join(dir, rel), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	var batch Batch

	for !slices.Contains(batch.Paths, rel) {
		select {
		case b, ok := <-w.Batches():
			if !ok {
				t.Fatal("batches closed")
			}

			batch = mergeBatches(batch, b)
		case err := <-w.Errors():
			t.Fatalf("watch error: %v", err)
		case <-time.After(watchWait):
			t.Fatalf("no batch named %s; saw %+v", rel, batch)
		}
	}

	res, err := e.Update(context.Background(), prev, src, batch)
	if err != nil {
		t.Fatalf("Update: %v", err)
	}

	return res
}

func TestSavedFileReachesTheDiff(t *testing.T) {
	t.Parallel()
	dir := bigRepo(t, 20)
	src := BranchSource{Worktree: dir, Base: "main"}
	e := testEngine(t)
	ctx := context.Background()

	w := startWatch(t, dir, WatchOptions{})
	barrier(t, w, dir, "barrier.txt")

	prev, err := e.Diff(ctx, src)
	if err != nil {
		t.Fatal(err)
	}

	rel := "d07/f0007.txt"

	res := saveToDiff(t, w, e, prev, src, rel, "saved\n")

	if f := fileByPath(t, res, rel); f.Status != Modified || len(f.Hunks) == 0 {
		t.Errorf("%s: %+v", rel, f)
	}

	sameAsFull(t, res, src)
}
