package md

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strings"
	"time"
)

const (
	// WalkMaxFiles is the default cap on listed files; folders do not count.
	WalkMaxFiles = 20000
	// WalkMaxTime is the time limit callers put on the walk's context.
	WalkMaxTime = 10 * time.Second

	// walkBatch and walkGap pace emit: a full batch at once, a partial one at
	// the end of a folder once walkGap has passed since the last emit.
	walkBatch = 4096
	walkGap   = 50 * time.Millisecond
)

// WalkStop says why a walk ended.
type WalkStop int

const (
	WalkDone WalkStop = iota
	WalkFileLimit
	WalkTimeLimit
	WalkCancelled
)

// WalkEntry is a listed file or folder; Rel is slash-separated from the root.
type WalkEntry struct {
	Rel string
	Dir bool
}

// WalkOptions configures Walk.
type WalkOptions struct {
	Root     string
	Hidden   bool
	SkipAbs  []string // absolute folders to skip
	MaxFiles int      // 0 means WalkMaxFiles

	// device is a test hook for the filesystem check; nil means deviceOf.
	device func(path string) (dev uint64, ok bool)
}

// WalkSkips lists the absolute folders a walk should skip on goos.
func WalkSkips(goos, home string) []string {
	skips := []string{"/proc", "/sys"}
	if goos == "darwin" && home != "" {
		skips = append(skips, filepath.Join(home, "Library"))
	}

	return skips
}

// skipName reports the folders skipped by name wherever they are.
func skipName(name string) bool {
	switch name {
	case ".git", "node_modules", "vendor", "target", "__pycache__":
		return true
	}

	return false
}

// classify says whether e is listed and whether it is a folder. A symlink is
// statted once: a folder link is a folder (the walk does not enter it), a link
// to a regular markdown file is a file, anything else is dropped. A
// non-regular file is dropped so nothing later opens a FIFO.
func classify(dir string, e fs.DirEntry) (listed, isDir bool) {
	typ := e.Type()

	if typ&fs.ModeSymlink != 0 {
		st, err := os.Stat(filepath.Join(dir, e.Name()))
		if err != nil {
			return false, false
		}

		typ = st.Mode().Type()
	}

	switch {
	case typ.IsDir():
		return true, true
	case typ.IsRegular() && IsMarkdown(e.Name()):
		return true, false
	}

	return false, false
}

type walkDir struct {
	abs string
	rel string // "" for the root
}

// Walk lists the folders and markdown files under opts.Root breadth first,
// each folder in name order, and passes them to emit in batches. It reads
// nothing on its own goroutine; emit runs on the caller's.
func Walk(ctx context.Context, opts WalkOptions, emit func([]WalkEntry)) WalkStop {
	maxFiles := cmp.Or(opts.MaxFiles, WalkMaxFiles)

	device := opts.device
	if device == nil {
		device = deviceOf
	}

	skipAbs := make(map[string]bool, len(opts.SkipAbs))

	for _, p := range opts.SkipAbs {
		skipAbs[filepath.Clean(p)] = true
	}

	rootDev, haveDev := device(opts.Root)

	var (
		pending []WalkEntry
		files   int
		last    = time.Now()
	)

	flush := func() {
		if len(pending) > 0 {
			emit(pending)
			pending = nil
		}

		last = time.Now()
	}
	stopOf := func() (WalkStop, bool) {
		err := ctx.Err()

		switch {
		case err == nil:
			return WalkDone, false
		case errors.Is(err, context.DeadlineExceeded):
			return WalkTimeLimit, true
		}

		return WalkCancelled, true
	}

	queue := []walkDir{{abs: opts.Root}}

	for len(queue) > 0 {
		if stop, done := stopOf(); done {
			return stop
		}

		cur := queue[0]
		queue = queue[1:]

		// An unreadable folder is skipped silently.
		entries, _ := os.ReadDir(cur.abs)
		parent := filepath.Base(cur.abs)

		for _, e := range entries {
			name := e.Name()
			if !opts.Hidden && strings.HasPrefix(name, ".") || name == ".git" {
				continue
			}

			listed, isDir := classify(cur.abs, e)
			if !listed {
				continue
			}

			rel := path.Join(cur.rel, name)
			abs := filepath.Join(cur.abs, name)

			if isDir {
				if skipName(name) || name == "mod" && parent == "pkg" || skipAbs[abs] {
					continue
				}

				enter := e.Type().IsDir()
				if enter && haveDev {
					if dev, ok := device(abs); ok && dev != rootDev {
						continue
					}
				}

				pending = append(pending, WalkEntry{Rel: rel, Dir: true})

				if enter {
					queue = append(queue, walkDir{abs: abs, rel: rel})
				}
			} else {
				if files == maxFiles {
					flush()

					return WalkFileLimit
				}

				files++

				pending = append(pending, WalkEntry{Rel: rel})
			}

			if len(pending) >= walkBatch {
				flush()
			}
		}

		if len(pending) > 0 && time.Since(last) >= walkGap {
			flush()
		}
	}

	flush()

	return WalkDone
}

// FolderEntry is one listed item of a folder.
type FolderEntry struct {
	Name string
	Dir  bool
}

// ReadFolder lists dir for browsing: folders and markdown files with the walk's
// classification, hidden ones and node_modules included, folders first, each
// group by case-folded name with the raw name breaking ties.
func ReadFolder(dir string) ([]FolderEntry, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, fmt.Errorf("read folder: %w", err)
	}

	out := make([]FolderEntry, 0, len(entries))

	for _, e := range entries {
		if listed, isDir := classify(dir, e); listed {
			out = append(out, FolderEntry{Name: e.Name(), Dir: isDir})
		}
	}

	slices.SortFunc(out, func(a, b FolderEntry) int {
		if a.Dir != b.Dir {
			if a.Dir {
				return -1
			}

			return 1
		}

		return cmp.Or(
			strings.Compare(strings.ToLower(a.Name), strings.ToLower(b.Name)),
			strings.Compare(a.Name, b.Name),
		)
	})

	return out, nil
}
