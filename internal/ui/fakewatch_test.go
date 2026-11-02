package ui

import (
	"context"
	"errors"
	"sync"
	"testing"

	"github.com/mhersson/aim/internal/git"
)

var _ watchSet = (*fakeWatchSet)(nil)

// fakeWatchSet is a watch set whose batches the test sends.
type fakeWatchSet struct {
	out chan git.RootBatch

	mu       sync.Mutex
	refs     map[string]int
	failures map[string]error
	hold     chan struct{}
	closed   bool
}

func newFakeWatchSet() *fakeWatchSet {
	return &fakeWatchSet{out: make(chan git.RootBatch, 256), refs: map[string]int{}, failures: map[string]error{}}
}

func (w *fakeWatchSet) Batches() <-chan git.RootBatch { return w.out }

func (w *fakeWatchSet) Acquire(ctx context.Context, r git.Repo) (func(), error) {
	w.mu.Lock()
	closed, hold := w.closed, w.hold
	w.mu.Unlock()

	if closed {
		return nil, git.ErrWatchSetClosed
	}

	if hold != nil {
		select {
		case <-hold:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}

	w.mu.Lock()
	defer w.mu.Unlock()

	if err := w.failures[r.Root]; err != nil {
		return nil, err
	}

	if w.closed {
		return nil, git.ErrWatchSetClosed
	}

	w.refs[r.Root]++

	var once sync.Once

	return func() {
		once.Do(func() {
			w.mu.Lock()
			defer w.mu.Unlock()

			w.refs[r.Root]--
		})
	}, nil
}

func (w *fakeWatchSet) send(root string, b git.Batch) {
	w.out <- git.RootBatch{Root: root, Batch: b}
}

func (w *fakeWatchSet) failAcquire(root string, err error) {
	w.mu.Lock()
	defer w.mu.Unlock()

	if err == nil {
		delete(w.failures, root)

		return
	}

	w.failures[root] = err
}

func (w *fakeWatchSet) holdAcquire() (release func()) {
	ch := make(chan struct{})

	w.mu.Lock()
	w.hold = ch
	w.mu.Unlock()

	var once sync.Once

	return func() {
		once.Do(func() { close(ch) })

		w.mu.Lock()
		defer w.mu.Unlock()

		if w.hold == ch {
			w.hold = nil
		}
	}
}

func (w *fakeWatchSet) holds(root string) int {
	w.mu.Lock()
	defer w.mu.Unlock()

	return w.refs[root]
}

func (w *fakeWatchSet) close() {
	w.mu.Lock()
	defer w.mu.Unlock()

	w.closed = true
}

func TestFakeWatchSet(t *testing.T) {
	t.Parallel()

	w := newFakeWatchSet()
	ctx := context.Background()
	repo := git.Repo{Root: "/r"}

	w.send("/r", git.Batch{Paths: []string{"a.go"}})

	if got := <-w.Batches(); got.Root != "/r" || len(got.Batch.Paths) != 1 {
		t.Errorf("batch = %+v", got)
	}

	release, err := w.Acquire(ctx, repo)
	if err != nil {
		t.Fatal(err)
	}

	if _, err := w.Acquire(ctx, repo); err != nil {
		t.Fatal(err)
	}

	release()
	release()

	if n := w.holds("/r"); n != 1 {
		t.Errorf("holds = %d, want 1: a release counts once", n)
	}

	boom := errors.New("boom")
	w.failAcquire("/r", boom)

	if _, err := w.Acquire(ctx, repo); !errors.Is(err, boom) {
		t.Errorf("failed Acquire = %v", err)
	}

	w.failAcquire("/r", nil)

	unhold := w.holdAcquire()
	done := make(chan error, 1)

	go func() {
		_, err := w.Acquire(ctx, repo)
		done <- err
	}()

	cctx, cancel := context.WithCancel(ctx)
	cancel()

	if _, err := w.Acquire(cctx, repo); !errors.Is(err, context.Canceled) {
		t.Errorf("held Acquire with a done context = %v", err)
	}

	unhold()

	if err := <-done; err != nil {
		t.Errorf("released Acquire = %v", err)
	}

	w.close()

	if _, err := w.Acquire(ctx, repo); !errors.Is(err, git.ErrWatchSetClosed) {
		t.Errorf("Acquire after close = %v", err)
	}
}
