package git

import (
	"context"
	"fmt"
	"path/filepath"
	"sync"

	"github.com/mhersson/aim/internal/git/fsnotify"
)

// FileWatcher signals when one file changes. It watches the file's folder, not
// the file, so a rename into place and a delete then recreate are seen. It needs
// no repository.
type FileWatcher struct {
	fsw     *fsnotify.Watcher
	changes chan struct{}
	stop    chan struct{}
	done    chan struct{}
	once    sync.Once

	// observe sees each event that names the file; tests only.
	observe func(fsnotify.Event)
}

// WatchFile starts watching path. It blocks on the watch syscalls, so callers
// keep it off the update loop. The watcher ends with ctx or Close.
func WatchFile(ctx context.Context, path string) (*FileWatcher, error) {
	return watchFile(ctx, path, nil)
}

func watchFile(ctx context.Context, path string, observe func(fsnotify.Event)) (*FileWatcher, error) {
	name := filepath.Clean(path)

	fsw, err := fsnotify.NewWatcher()
	if err != nil {
		return nil, fmt.Errorf("watch %s: %w", name, err)
	}

	if err := fsw.Add(filepath.Dir(name)); err != nil {
		_ = fsw.Close()

		return nil, fmt.Errorf("watch %s: %w", name, err)
	}

	w := &FileWatcher{
		fsw: fsw, changes: make(chan struct{}, 1), stop: make(chan struct{}), done: make(chan struct{}),
		observe: observe,
	}

	go w.loop(ctx, name)

	return w, nil
}

// Changes is a one-slot signal that the file changed since it was last read: a
// burst of events leaves one signal. It closes when the watcher ends.
func (w *FileWatcher) Changes() <-chan struct{} { return w.changes }

// Close stops the watcher and waits for its goroutine. It is safe to call more
// than once.
func (w *FileWatcher) Close() error {
	w.once.Do(func() { close(w.stop) })
	<-w.done

	return nil
}

func (w *FileWatcher) loop(ctx context.Context, name string) {
	defer func() {
		_ = w.fsw.Close()

		close(w.changes)
		close(w.done)
	}()

	signal := func() {
		select {
		case w.changes <- struct{}{}:
		default:
		}
	}

	for {
		select {
		case <-ctx.Done():
			return
		case <-w.stop:
			return
		case ev, ok := <-w.fsw.Events:
			if !ok {
				return
			}

			// kqueue sends Chmod-only events no write caused; the file's own
			// content is what a reader cares about.
			if ev.Name != name || ev.Op == fsnotify.Chmod {
				continue
			}

			if w.observe != nil {
				w.observe(ev)
			}

			signal()
		case _, ok := <-w.fsw.Errors:
			if !ok {
				return
			}

			// An overflow lost events: say the file may have changed.
			signal()
		}
	}
}
