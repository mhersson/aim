package git

import (
	"context"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/mhersson/aim/internal/git/fsnotify"
)

const fileWatchGuard = 30 * time.Second

func watchedFile(ctx context.Context, t *testing.T, observe func(fsnotify.Event)) (*FileWatcher, string) {
	t.Helper()

	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}

	path := filepath.Join(dir, "doc.md")
	if err := os.WriteFile(path, []byte("one"), 0o600); err != nil {
		t.Fatal(err)
	}

	w, err := watchFile(ctx, path, observe)
	if err != nil {
		t.Fatal(err)
	}

	t.Cleanup(func() { _ = w.Close() })

	return w, path
}

func awaitChange(t *testing.T, w *FileWatcher) {
	t.Helper()

	select {
	case _, ok := <-w.Changes():
		if !ok {
			t.Fatal("the watcher ended before it signalled")
		}
	case <-time.After(fileWatchGuard):
		t.Fatal("no change signalled")
	}
}

func TestFileWatcherSignals(t *testing.T) {
	t.Parallel()

	for _, tt := range []struct {
		name   string
		change func(t *testing.T, path string)
	}{
		{"a write", func(t *testing.T, path string) {
			t.Helper()

			if err := os.WriteFile(path, []byte("two"), 0o600); err != nil {
				t.Fatal(err)
			}
		}},
		{"a rename into place", func(t *testing.T, path string) {
			t.Helper()

			tmp := path + ".tmp"
			if err := os.WriteFile(tmp, []byte("two"), 0o600); err != nil {
				t.Fatal(err)
			}

			if err := os.Rename(tmp, path); err != nil {
				t.Fatal(err)
			}
		}},
		{"a delete then recreate", func(t *testing.T, path string) {
			t.Helper()

			if err := os.Remove(path); err != nil {
				t.Fatal(err)
			}

			if err := os.WriteFile(path, []byte("two"), 0o600); err != nil {
				t.Fatal(err)
			}
		}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			w, path := watchedFile(context.Background(), t, nil)
			tt.change(t, path)
			awaitChange(t, w)
		})
	}
}

func TestFileWatcherIgnoresANeighbour(t *testing.T) {
	t.Parallel()

	var (
		mu   sync.Mutex
		seen []string
	)

	w, path := watchedFile(context.Background(), t, func(ev fsnotify.Event) {
		mu.Lock()
		defer mu.Unlock()

		seen = append(seen, ev.Name)
	})

	// The watcher handles events in order, so by the time the file's signal
	// arrives, the neighbour's events were handled and none counted.
	if err := os.WriteFile(filepath.Join(filepath.Dir(path), "other.md"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}

	if err := os.WriteFile(path, []byte("two"), 0o600); err != nil {
		t.Fatal(err)
	}

	awaitChange(t, w)

	mu.Lock()
	defer mu.Unlock()

	if len(seen) == 0 {
		t.Fatal("the file's own write was not counted")
	}

	for _, n := range seen {
		if n != path {
			t.Errorf("counted an event for %q, want only %q", n, path)
		}
	}
}

func TestFileWatcherEnds(t *testing.T) {
	t.Parallel()

	for _, tt := range []struct {
		name string
		end  func(w *FileWatcher, cancel context.CancelFunc)
	}{
		{"Close", func(w *FileWatcher, _ context.CancelFunc) { _ = w.Close() }},
		{"a cancelled context", func(_ *FileWatcher, cancel context.CancelFunc) { cancel() }},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()

			w, _ := watchedFile(ctx, t, nil)
			tt.end(w, cancel)

			select {
			case _, ok := <-w.Changes():
				if ok {
					t.Error("a signal arrived after the watcher ended")
				}
			case <-time.After(fileWatchGuard):
				t.Fatal("Changes did not close")
			}

			// Close after the end is safe and waits for nothing more.
			_ = w.Close()
		})
	}
}

func TestWatchFileMissingFolder(t *testing.T) {
	t.Parallel()

	if w, err := WatchFile(context.Background(), filepath.Join(t.TempDir(), "gone", "doc.md")); err == nil {
		_ = w.Close()

		t.Fatal("watching a file in a missing folder did not fail")
	}
}
