package git

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"testing"
	"time"

	"github.com/mhersson/aim/internal/git/fsnotify"
)

func newWatchSet(t *testing.T) *WatchSet {
	t.Helper()

	return newWatchSetWith(t, fastWatch)
}

func newWatchSetWith(t *testing.T, opts WatchOptions) *WatchSet {
	t.Helper()

	s := NewWatchSet(context.Background(), opts)

	t.Cleanup(func() { _ = s.Close() })

	return s
}

func nextRootBatch(t *testing.T, s *WatchSet) RootBatch {
	t.Helper()

	select {
	case b, ok := <-s.Batches():
		if !ok {
			t.Fatal("batches closed")
		}

		return b
	case <-time.After(watchWait):
		t.Fatal("no batch")
	}

	return RootBatch{}
}

func TestWatchSetSharesARoot(t *testing.T) {
	t.Parallel()
	dir := newRepo(t)
	s := newWatchSet(t)
	r := openRepo(t, dir)

	rel1, err := s.Acquire(context.Background(), r)
	if err != nil {
		t.Fatal(err)
	}

	rel2, err := s.Acquire(context.Background(), r)
	if err != nil {
		t.Fatal(err)
	}

	// One watcher serves both holders, so one change makes one batch.
	s.mu.Lock()
	entries, refs := len(s.entries), 0

	if e, ok := s.entries[r.Root]; ok {
		refs = e.refs
	}

	s.mu.Unlock()

	if entries != 1 || refs != 2 {
		t.Fatalf("entries = %d, references = %d; want 1 entry with 2", entries, refs)
	}

	writeFile(t, dir, "a.txt", "x")

	b := nextRootBatch(t, s)
	if b.Root != r.Root || len(b.Batch.Paths) != 1 || b.Batch.Paths[0] != "a.txt" {
		t.Fatalf("batch = %+v", b)
	}

	rel1()
	// One reference left: still watched.
	if _, ok := s.Seq(r.Root); !ok {
		t.Fatal("watcher stopped while a reference was held")
	}

	rel2()
	rel2() // idempotent
}

func TestWatchSetReacquireNeverRepeatsSeq(t *testing.T) {
	t.Parallel()
	dir := newRepo(t)
	s := newWatchSet(t)
	r := openRepo(t, dir)
	seen := map[uint64]bool{}

	for i := range 4 {
		rel, err := s.Acquire(context.Background(), r)
		if err != nil {
			t.Fatal(err)
		}

		n, ok := s.Seq(r.Root)
		if !ok {
			t.Fatal("not watched")
		}

		if seen[n] {
			t.Fatalf("round %d: seq %d repeated", i, n)
		}

		seen[n] = true

		if i%2 == 0 {
			writeFile(t, dir, "f.txt", string(rune('a'+i)))
			nextRootBatch(t, s)

			n, _ = s.Seq(r.Root)
			if seen[n] {
				t.Fatalf("round %d: seq %d repeated after a change", i, n)
			}

			seen[n] = true
		}

		rel()
	}
}

func TestWatchSetCloseStopsEverything(t *testing.T) {
	dir1, dir2 := newRepo(t), newRepo(t)
	base := runtime.NumGoroutine()
	s := NewWatchSet(context.Background(), fastWatch)

	for _, d := range []string{dir1, dir2} {
		if _, err := s.Acquire(context.Background(), openRepo(t, d)); err != nil {
			t.Fatal(err)
		}
	}

	if err := s.Close(); err != nil {
		t.Fatal(err)
	}

	if _, ok := s.Seq(openRepo(t, dir1).Root); ok {
		t.Fatal("Seq reports a watcher after Close")
	}

	if _, ok := <-s.Batches(); ok {
		t.Fatal("batches not closed")
	}

	if _, err := s.Acquire(context.Background(), openRepo(t, dir1)); err == nil {
		t.Fatal("Acquire after Close succeeded")
	}

	deadline := time.Now().Add(2 * time.Second)
	for runtime.NumGoroutine() > base && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}

	if got := runtime.NumGoroutine(); got > base {
		t.Fatalf("goroutines = %d, baseline %d", got, base)
	}
}

func TestWatchSetReleaseAfterCloseIsSafe(t *testing.T) {
	t.Parallel()

	s := NewWatchSet(context.Background(), fastWatch)

	rel, err := s.Acquire(context.Background(), openRepo(t, newRepo(t)))
	if err != nil {
		t.Fatal(err)
	}

	_ = s.Close()

	rel()
	rel()
}

func TestWatchSetFailedAcquireNeverBlocksWithoutAReader(t *testing.T) {
	t.Parallel()
	r := openRepo(t, newRepo(t))
	ctx, cancel := context.WithCancel(context.Background())
	s := NewWatchSet(ctx, fastWatch)

	t.Cleanup(func() { _ = s.Close() })
	cancel()

	done := make(chan struct{})

	go func() {
		defer close(done)

		for range rootBatchBuffer + 1 {
			_, _ = s.Acquire(context.Background(), r)
		}
	}()

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("failed Acquire blocked with nobody reading Batches")
	}
}

func TestWatchSetConcurrentAcquireRelease(t *testing.T) {
	t.Parallel()
	dir := newRepo(t)
	s := newWatchSet(t)
	r := openRepo(t, dir)

	var wg sync.WaitGroup

	for range 8 {
		wg.Go(func() {
			for range 5 {
				rel, err := s.Acquire(context.Background(), r)
				if err != nil {
					t.Error(err)

					return
				}

				_, _ = s.Seq(r.Root)

				rel()
			}
		})
	}

	wg.Wait()

	if _, ok := s.Seq(r.Root); ok {
		t.Fatal("still watched after every release")
	}
}

func TestWatchSetReportsARealStartFailure(t *testing.T) {
	t.Parallel()
	bad := newRepo(t)
	writeFile(t, bad, "sub/a.txt", "x")

	badRepo := openRepo(t, bad)

	// A broken config makes git check-ignore fail while the walk runs.
	if err := os.WriteFile(filepath.Join(bad, ".git", "config"), []byte("[core\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	s := newWatchSet(t)

	rel, err := s.Acquire(context.Background(), badRepo)
	if err == nil {
		rel()
		t.Fatal("Acquire succeeded on a repository whose walk fails")
	}

	// Acquire sends the report before it returns, so it is already queued.
	if got := nextRootBatch(t, s); got.Root != badRepo.Root || got.Err == nil {
		t.Fatalf("batch = %+v", got)
	}

	if n := len(s.Batches()); n != 0 {
		t.Fatalf("%d more batches queued for one failure", n)
	}

	if _, ok := s.Seq(badRepo.Root); ok {
		t.Fatal("Seq reports a watcher that failed to start")
	}

	good := openRepo(t, newRepo(t))

	rel, err = s.Acquire(context.Background(), good)
	if err != nil {
		t.Fatalf("healthy root after a failure: %v", err)
	}

	defer rel()

	if _, ok := s.Seq(good.Root); !ok {
		t.Fatal("healthy root not watched")
	}

	writeFile(t, good.Root, "a.txt", "x")

	if got := nextRootBatch(t, s); got.Root != good.Root || got.Err != nil {
		t.Fatalf("next batch = %+v, want the healthy root's change", got)
	}
}

// A watcher that reported an error may be missing events from then on, so its
// counter must not allow a skip until it is restarted.
func TestWatchSetSeqIsOffAfterAWatcherError(t *testing.T) {
	t.Parallel()

	for name, injected := range map[string]error{
		"read error": errors.New("read failed"),
		"overflow":   fsnotify.ErrEventOverflow,
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			dir := newRepo(t)
			injectErrs := make(chan error, 1)
			opts := fastWatch
			opts.injectErrs = injectErrs
			s := newWatchSetWith(t, opts)
			r := openRepo(t, dir)

			rel, err := s.Acquire(context.Background(), r)
			if err != nil {
				t.Fatal(err)
			}

			if _, ok := s.Seq(r.Root); !ok {
				t.Fatal("healthy watcher not reported")
			}

			injectErrs <- injected

			deadline := time.Now().Add(watchWait)
			for time.Now().Before(deadline) {
				if _, ok := s.Seq(r.Root); !ok {
					break
				}

				time.Sleep(5 * time.Millisecond)
			}

			if _, ok := s.Seq(r.Root); ok {
				t.Fatal("Seq still reported after the watcher reported an error")
			}

			// A restarted watcher starts clean.
			rel()

			rel, err = s.Acquire(context.Background(), r)
			if err != nil {
				t.Fatal(err)
			}

			defer rel()

			if _, ok := s.Seq(r.Root); !ok {
				t.Fatal("restarted watcher still degraded")
			}
		})
	}
}
