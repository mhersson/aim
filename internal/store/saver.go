package store

import (
	"bytes"
	"context"
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"sync"

	"github.com/mhersson/aim/internal/core"
)

// WriteAtomic replaces path with data: a temp file in the same directory is
// written, synced and renamed over it, so a reader sees the old or the new
// content and never a partial file.
func WriteAtomic(path string, data []byte) error { return writeAtomic(path, data, true, 0o600) }

// WriteAtomicNoSync is WriteAtomic without the sync, for a file that need not
// survive a crash. On macOS a sync is F_FULLFSYNC, which waits for the drive to
// flush every process's pending writes.
func WriteAtomicNoSync(path string, data []byte) error { return writeAtomic(path, data, false, 0o600) }

// writeAtomic replaces path with data in a file of mode perm.
func writeAtomic(path string, data []byte, durable bool, perm fs.FileMode) (err error) {
	f, err := os.CreateTemp(filepath.Dir(path), "."+filepath.Base(path)+".*.tmp") // 0600
	if err != nil {
		return fmt.Errorf("creating temp file: %w", err)
	}

	tmp := f.Name()

	defer func() {
		if err != nil {
			_ = os.Remove(tmp)
		}
	}()

	if perm != 0o600 {
		if err = f.Chmod(perm); err != nil {
			_ = f.Close()

			return fmt.Errorf("chmod %s: %w", tmp, err)
		}
	}

	if _, err = f.Write(data); err != nil {
		_ = f.Close()

		return fmt.Errorf("writing %s: %w", tmp, err)
	}

	if durable {
		if err = f.Sync(); err != nil {
			_ = f.Close()

			return fmt.Errorf("syncing %s: %w", tmp, err)
		}
	}

	if err = f.Close(); err != nil {
		return fmt.Errorf("closing %s: %w", tmp, err)
	}

	if err = os.Rename(tmp, path); err != nil {
		return fmt.Errorf("renaming %s: %w", tmp, err)
	}

	return nil
}

// Saver writes the state file from its own goroutine, which ends in Close.
type Saver struct {
	path string
	log  *slog.Logger

	mu      sync.Mutex
	pending []byte // newest encoding not yet written
	closed  bool

	wake chan struct{}
	stop chan struct{}
	done chan struct{}
}

// NewSaver starts a Saver that writes to path. A nil log discards.
func NewSaver(path string, log *slog.Logger) *Saver {
	if log == nil {
		log = slog.New(slog.DiscardHandler)
	}

	s := &Saver{
		path: path,
		log:  log,
		wake: make(chan struct{}, 1),
		stop: make(chan struct{}),
		done: make(chan struct{}),
	}
	go s.run()

	return s
}

// Save queues st for writing and returns at once; a newer state replaces one
// still waiting. Saves after Close are dropped.
func (s *Saver) Save(st core.State) {
	data, err := Encode(st)
	if err != nil {
		s.log.Error("encoding state", "err", err)

		return
	}

	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()

		return
	}

	s.pending = data
	s.mu.Unlock()

	select {
	case s.wake <- struct{}{}:
	default:
	}
}

// Close writes what is pending and stops the goroutine. It returns ctx's error
// if the goroutine does not finish first.
func (s *Saver) Close(ctx context.Context) error {
	s.mu.Lock()
	if !s.closed {
		s.closed = true
		close(s.stop)
	}
	s.mu.Unlock()

	select {
	case <-s.done:
		return nil
	case <-ctx.Done():
		return fmt.Errorf("closing saver: %w", ctx.Err())
	}
}

func (s *Saver) run() {
	defer close(s.done)

	var last []byte

	flush := func() {
		s.mu.Lock()
		data := s.pending
		s.pending = nil
		s.mu.Unlock()

		if data == nil || bytes.Equal(data, last) {
			return
		}

		if err := WriteAtomic(s.path, data); err != nil {
			s.log.Error("writing state", "path", s.path, "err", err)

			return
		}

		last = data
	}

	for {
		select {
		case <-s.wake:
			flush()
		case <-s.stop:
			flush()

			return
		}
	}
}
