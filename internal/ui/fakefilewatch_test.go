package ui

import (
	"context"
	"slices"
	"sync"
)

// fakeFileWatches is the app's seam for watching one file. A quiet one hands out
// watches whose signal channel is closed, so a driver that runs the wait command
// inline never blocks; a live one holds the channel open for fire.
type fakeFileWatches struct {
	live bool

	mu   sync.Mutex
	err  error
	ws   []*fakeFileWatch
	pend []string
}

type fakeFileWatch struct {
	path string
	ch   chan struct{}

	mu     sync.Mutex
	closed bool // Close was called
	shut   bool // ch is closed
}

func (f *fakeFileWatches) open(_ context.Context, path string) (fileWatch, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	f.pend = append(f.pend, path)

	if f.err != nil {
		return nil, f.err
	}

	w := &fakeFileWatch{path: path, ch: make(chan struct{}, 1)}
	if !f.live {
		close(w.ch)

		w.shut = true
	}

	f.ws = append(f.ws, w)

	return w, nil
}

func (f *fakeFileWatches) fail(err error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	f.err = err
}

// opened are the paths asked for, in order.
func (f *fakeFileWatches) opened() []string {
	f.mu.Lock()
	defer f.mu.Unlock()

	return slices.Clone(f.pend)
}

// openWatches are the watches not closed yet.
func (f *fakeFileWatches) openWatches() []*fakeFileWatch {
	f.mu.Lock()
	defer f.mu.Unlock()

	var out []*fakeFileWatch

	for _, w := range f.ws {
		if !w.isClosed() {
			out = append(out, w)
		}
	}

	return out
}

func (w *fakeFileWatch) Changes() <-chan struct{} { return w.ch }

func (w *fakeFileWatch) Close() error {
	w.mu.Lock()
	defer w.mu.Unlock()

	w.closed = true

	if !w.shut {
		w.shut = true

		close(w.ch)
	}

	return nil
}

func (w *fakeFileWatch) isClosed() bool {
	w.mu.Lock()
	defer w.mu.Unlock()

	return w.closed
}

// fire signals a change.
func (w *fakeFileWatch) fire() {
	w.mu.Lock()
	defer w.mu.Unlock()

	if w.shut {
		return
	}

	select {
	case w.ch <- struct{}{}:
	default:
	}
}
