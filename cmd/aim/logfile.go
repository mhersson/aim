package main

import (
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// secondLogKeep is how long a second instance's log stays after its AIM ends,
// so a tester can still send the log of one that crashed.
const secondLogKeep = 7 * 24 * time.Hour

// openLog opens this run's log in dir. The lock holder (held) moves aim.log to
// aim.log.1 and starts a fresh one. Another instance never touches those: it
// appends to aim-<pid>.log, which keeps a dead instance's log under a reused pid.
func openLog(dir string, held bool, pid int) (*os.File, error) {
	path := filepath.Join(dir, "aim.log")
	if !held {
		return openSecondLog(dir, pid, path)
	}

	if err := os.Rename(path, path+".1"); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return nil, fmt.Errorf("rotate log: %w", err)
	}

	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		return nil, fmt.Errorf("open log: %w", err)
	}

	return f, nil
}

func openSecondLog(dir string, pid int, holderLog string) (*os.File, error) {
	f, err := os.OpenFile(filepath.Join(dir, secondLogName(pid)), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return nil, fmt.Errorf("open log: %w", err)
	}

	slog.New(slog.NewTextHandler(f, nil)).Warn("another AIM holds the state lock; this second instance logs here", "holder_log", holderLog)

	return f, nil
}

func secondLogName(pid int) string {
	return "aim-" + strconv.Itoa(pid) + ".log"
}

// sweepLogs removes the aim-<pid>.log files in dir whose pid is not alive and
// that were last written over secondLogKeep before now. A live pid's log stays
// at any age.
func sweepLogs(logger *slog.Logger, dir string, now time.Time, alive func(pid int) bool) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		logger.Warn("read log dir", "err", err)

		return
	}

	for _, e := range entries {
		pid, ok := secondLogPid(e.Name())
		if !ok || !e.Type().IsRegular() {
			continue
		}

		info, err := e.Info()
		if err != nil || now.Sub(info.ModTime()) <= secondLogKeep || alive(pid) {
			continue
		}

		if err := os.Remove(filepath.Join(dir, e.Name())); err != nil && !errors.Is(err, fs.ErrNotExist) {
			logger.Warn("remove old second instance log", "name", e.Name(), "err", err)
		}
	}
}

// secondLogPid extracts the pid from a name secondLogName makes, and from no
// other name.
func secondLogPid(name string) (int, bool) {
	digits, ok := strings.CutPrefix(name, "aim-")
	if !ok {
		return 0, false
	}

	digits, ok = strings.CutSuffix(digits, ".log")
	if !ok {
		return 0, false
	}

	pid, err := strconv.Atoi(digits)

	return pid, err == nil && pid > 0 && secondLogName(pid) == name
}
