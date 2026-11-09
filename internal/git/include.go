package git

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// IncludeFile, in the main checkout's root, names the ignored files a new worktree gets copies of.
const IncludeFile = ".worktreeinclude"

// CopyError is the first file CopyIncluded could not copy.
type CopyError struct {
	Path string // relative to the checkout root, as git lists it
	Err  error
}

func (e *CopyError) Error() string { return fmt.Sprintf("copy %s: %v", e.Path, e.Err) }

func (e *CopyError) Unwrap() error { return e.Err }

// CopyIncluded copies into dst the files in the checkout main that main's
// .worktreeinclude names and git ignores, and returns how many it copied.
// Tracked files, symlinks and other special files are skipped, as is a file
// dst already has. A file that fails comes back as a *CopyError while the rest
// are still copied; a failure to list the files or to open either checkout is
// a plain error and copies nothing.
func CopyIncluded(ctx context.Context, main, dst string) (int, error) {
	if _, err := os.Lstat(filepath.Join(main, IncludeFile)); err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return 0, nil
		}

		return 0, fmt.Errorf("read %s: %w", IncludeFile, err)
	}

	paths, err := includedPaths(ctx, main)
	if err != nil || len(paths) == 0 {
		return 0, err
	}

	src, err := os.OpenRoot(main)
	if err != nil {
		return 0, fmt.Errorf("open %s: %w", main, err)
	}
	defer src.Close()

	out, err := os.OpenRoot(dst)
	if err != nil {
		return 0, fmt.Errorf("open %s: %w", dst, err)
	}
	defer out.Close()

	var (
		copied int
		first  error
	)

	fail := func(path string, err error) {
		if first == nil {
			first = &CopyError{Path: path, Err: err}
		}
	}

	for _, path := range paths {
		if err := ctx.Err(); err != nil {
			fail(path, err)

			break
		}

		ok, err := copyOne(src, out, filepath.FromSlash(path))
		if err != nil {
			fail(path, err)

			continue
		}

		if ok {
			copied++
		}
	}

	return copied, first
}

// includedPaths lists the untracked files .worktreeinclude matches that the
// repository's own rules also ignore.
func includedPaths(ctx context.Context, main string) ([]string, error) {
	out, err := run(ctx, call{
		dir:  main,
		args: []string{"ls-files", "-z", "--others", "--ignored", "--exclude-from=" + IncludeFile},
		read: true,
	})
	if err != nil {
		return nil, fmt.Errorf("match %s: %w", IncludeFile, err)
	}

	if len(out) == 0 {
		return nil, nil
	}

	out, err = run(ctx, call{
		dir:   main,
		args:  []string{"check-ignore", "-z", "--stdin"},
		stdin: strings.NewReader(string(out)),
		read:  true,
	})
	if err != nil {
		if isExitCode(err, 1) {
			return nil, nil // none of them is ignored
		}

		return nil, fmt.Errorf("check ignored paths: %w", err)
	}

	var paths []string

	for p := range strings.SplitSeq(string(out), "\x00") {
		if p != "" {
			paths = append(paths, p)
		}
	}

	return paths, nil
}

// copyOne copies the regular file rel from src to out and reports whether it
// did; a file that is not regular, or already exists in out, is skipped.
func copyOne(src, out *os.Root, rel string) (bool, error) {
	fi, err := src.Lstat(rel)
	if err != nil {
		return false, err
	}

	if !fi.Mode().IsRegular() {
		return false, nil
	}

	in, err := src.Open(rel)
	if err != nil {
		return false, err
	}
	defer in.Close()

	if dir := filepath.Dir(rel); dir != "." {
		if err := out.MkdirAll(dir, 0o755); err != nil {
			return false, err
		}
	}

	perm := fi.Mode().Perm()

	f, err := out.OpenFile(rel, os.O_WRONLY|os.O_CREATE|os.O_EXCL, perm)
	if err != nil {
		if errors.Is(err, fs.ErrExist) {
			return false, nil
		}

		return false, err
	}

	if _, err = io.Copy(f, in); err == nil {
		err = f.Chmod(perm) // the umask must not change the bits
	}

	if cerr := f.Close(); err == nil {
		err = cerr
	}

	if err != nil {
		_ = out.Remove(rel)

		return false, err
	}

	return true, nil
}
