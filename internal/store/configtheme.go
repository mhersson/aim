package store

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/mhersson/aim/internal/core"
)

// SaveTheme sets the theme in the config file at path to name, keeping every
// other byte (core.ConfigWithTheme). The file is read fresh and replaced
// atomically with its mode kept; a missing one is created 0600 in a 0700 dir.
// A file that is not valid TOML is left as it is.
func SaveTheme(path, name string) error {
	// A dotfiles symlink stays a link: the file it names is replaced.
	if target, err := filepath.EvalSymlinks(path); err == nil {
		path = target
	}

	perm := fs.FileMode(0o600)

	data, err := os.ReadFile(path)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			return fmt.Errorf("creating config dir: %w", err)
		}
	case err != nil:
		return fmt.Errorf("reading config: %w", err)
	default:
		info, err := os.Stat(path)
		if err != nil {
			return fmt.Errorf("reading config: %w", err)
		}

		perm = info.Mode().Perm()
	}

	out, err := core.ConfigWithTheme(data, name)
	if err != nil {
		return fmt.Errorf("config: %w", err)
	}

	if err := writeAtomic(path, out, true, perm); err != nil {
		return fmt.Errorf("writing config: %w", err)
	}

	return nil
}
