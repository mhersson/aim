package ui

import (
	"context"
	"sync"

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
