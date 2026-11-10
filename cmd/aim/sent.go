package main

import (
	"errors"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"strconv"
)

// sentDir is where one run keeps the copies of the images its agents sent, under
// the index cache; empty when that is off.
func sentDir(indexCache string, pid int) string {
	if indexCache == "" {
		return ""
	}

	return filepath.Join(indexCache, "sent", strconv.Itoa(pid))
}

// sweepSent removes the per-pid folders of base whose instance is gone; a name
// that is not a pid stays.
func sweepSent(logger *slog.Logger, base string, alive func(pid int) bool) {
	entries, err := os.ReadDir(base)
	if err != nil {
		if !errors.Is(err, fs.ErrNotExist) {
			logger.Warn("read sent images dir", "err", err)
		}

		return
	}

	for _, e := range entries {
		pid, err := strconv.Atoi(e.Name())
		if err != nil || pid <= 0 || strconv.Itoa(pid) != e.Name() || !e.IsDir() || alive(pid) {
			continue
		}

		if err := os.RemoveAll(filepath.Join(base, e.Name())); err != nil {
			logger.Warn("remove sent images of a dead instance", "name", e.Name(), "err", err)
		}
	}
}
