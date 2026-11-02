package git

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"

	"github.com/mhersson/aim/internal/md"
)

// maxMarkdownFiles bounds the listing so a huge tree cannot stall the picker.
const maxMarkdownFiles = 5000

// MarkdownFiles lists the markdown files under root as sorted, slash-separated
// paths relative to root, whether git tracks, ignores or has never seen them.
// It skips .git, node_modules, vendor and every folder below root that holds a
// .git entry (a nested checkout or worktree), and does not follow symlinked
// folders. At the cap the walk stops: the files found so far (the first in walk
// order) come back, sorted, without an error. So do the files found when ctx's
// deadline passes mid-walk. An unreadable folder below root is skipped; an
// unreadable root, a cancelled ctx and a deadline before any file are errors.
func MarkdownFiles(ctx context.Context, root string) ([]string, error) {
	return markdownFiles(ctx, root, maxMarkdownFiles)
}

func markdownFiles(ctx context.Context, root string, limit int) ([]string, error) {
	var files []string

	// WalkDir does not follow a root that is itself a symlink.
	if resolved, err := filepath.EvalSymlinks(root); err == nil {
		root = resolved
	}

	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			if path == root {
				return err
			}

			if d != nil && d.IsDir() {
				return fs.SkipDir
			}

			return nil
		}

		if d.IsDir() {
			if err := ctx.Err(); err != nil {
				return err
			}

			if path == root {
				return nil
			}

			switch d.Name() {
			case ".git", "node_modules", "vendor":
				return fs.SkipDir
			}

			if _, err := os.Lstat(filepath.Join(path, ".git")); err == nil {
				return fs.SkipDir
			}

			return nil
		}

		if !md.IsMarkdown(d.Name()) {
			return nil
		}

		rel, err := filepath.Rel(root, path)
		if err != nil {
			return nil
		}

		files = append(files, filepath.ToSlash(rel))
		if len(files) >= limit {
			return fs.SkipAll
		}

		return nil
	})
	// A deadline that cuts the walk short keeps what it found, as the cap does.
	if err != nil {
		if !errors.Is(err, context.DeadlineExceeded) || len(files) == 0 {
			return nil, fmt.Errorf("list %s: %w", root, err)
		}
	}

	slices.Sort(files)

	return files, nil
}
