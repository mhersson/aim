package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"

	"golang.org/x/sys/unix"

	"github.com/mhersson/aim/internal/git"
	"github.com/mhersson/aim/internal/harness"
	"github.com/mhersson/aim/internal/store"
)

// eventBuffer is how many hook events may wait for the update loop.
const eventBuffer = 256

// hookRuntime is what agents need to report through hooks. Settings and Sock
// are empty, and Notice says why, when the setup failed.
type hookRuntime struct {
	Settings, Sock string
	RunDir         string // where temporary git indexes go; empty when setup failed
	Notice         string
	Events         <-chan harness.Event

	stop func()
}

// Stop ends the socket server and removes this instance's files; it is safe on
// a runtime whose setup failed.
func (h hookRuntime) Stop() {
	if h.stop != nil {
		h.stop()
	}
}

// indexCacheDir returns <user cache dir>/aim, created 0700. A failure
// is logged and returns "", which turns the index cache off.
func indexCacheDir(logger *slog.Logger) string {
	base, err := os.UserCacheDir()
	if err == nil {
		dir := filepath.Join(base, "aim")
		if err = os.MkdirAll(dir, 0o700); err == nil {
			return dir
		}
	}

	logger.Warn("index cache is off", "err", err)

	return ""
}

// imageCacheDir is where fetched images are cached under the index cache; empty
// when that is off.
func imageCacheDir(indexCache string) string {
	if indexCache == "" {
		return ""
	}

	return filepath.Join(indexCache, "images")
}

// startHooks prepares <stateDir>/run: it sweeps files of dead instances, writes
// this instance's settings file and serves its socket. A failure leaves nothing
// behind and yields a runtime with a notice, so agents start without hooks.
func startHooks(logger *slog.Logger, stateDir, indexCache string, watches *git.WatchSet, places *git.PlaceCache) hookRuntime {
	h, err := setupHooks(logger, stateDir, indexCache, watches, places)
	if err != nil {
		logger.Warn("agent hooks are off", "err", err)

		return hookRuntime{Notice: "agent hooks are off: " + err.Error()}
	}

	return h
}

func setupHooks(logger *slog.Logger, stateDir, indexCache string, watches *git.WatchSet, places *git.PlaceCache) (hookRuntime, error) {
	runDir := filepath.Join(stateDir, "run")
	if err := os.MkdirAll(runDir, 0o700); err != nil {
		return hookRuntime{}, fmt.Errorf("run dir: %w", err)
	}

	if err := os.Chmod(runDir, 0o700); err != nil {
		return hookRuntime{}, fmt.Errorf("run dir mode: %w", err)
	}

	sweepRunDir(logger, runDir, pidAlive)

	exe, err := os.Executable()
	if err != nil {
		return hookRuntime{}, fmt.Errorf("executable path: %w", err)
	}

	pid := os.Getpid()
	sockPath := filepath.Join(runDir, strconv.Itoa(pid)+".sock")
	settingsPath, _ := settingsForSock(sockPath) // sockPath ends in .sock, so the bool is true

	if len(sockPath) > maxSockPath {
		return hookRuntime{}, fmt.Errorf("socket path too long (%d bytes, at most %d)", len(sockPath), maxSockPath)
	}

	settings, err := harness.Claude{}.Settings(exe)
	if err != nil {
		return hookRuntime{}, err
	}

	warnStatusLineOff(logger, exe)

	// A per-instance file needs no sync: a crash leaves it to the next sweep.
	if err := store.WriteAtomicNoSync(settingsPath, settings); err != nil {
		return hookRuntime{}, fmt.Errorf("settings file: %w", err)
	}

	srv, err := harness.Listen(sockPath)
	if err != nil {
		_ = os.Remove(settingsPath)

		return hookRuntime{}, err
	}

	srv.Log = logger
	srv.Hold = turnHold
	srv.Budget = turnBudget

	raw := make(chan harness.Event, eventBuffer)
	events := make(chan harness.Event, eventBuffer)
	ctx, cancel := context.WithCancel(context.Background())

	var wg sync.WaitGroup

	wg.Go(func() {
		if err := srv.Serve(ctx, raw); err != nil {
			logger.Error("hook server", "err", err)
		}
	})

	wg.Go(func() {
		runTurnGate(ctx, raw, events, gitTurnWriter{tempDir: runDir, indexCache: indexCache}, places.Place, watchSeq(watches), logger)
	})

	stop := sync.OnceFunc(func() {
		cancel()

		if err := srv.Close(); err != nil {
			logger.Warn("close hook server", "err", err)
		}

		wg.Wait()

		if err := os.Remove(settingsPath); err != nil && !errors.Is(err, os.ErrNotExist) {
			logger.Warn("remove settings file", "err", err)
		}
	})

	return hookRuntime{Settings: settingsPath, Sock: sockPath, RunDir: runDir, Events: events, stop: stop}, nil
}

// warnStatusLineOff logs why Settings leaves out the status line for exe. The
// error holds the path only %q-escaped, so it is safe to log.
func warnStatusLineOff(logger *slog.Logger, exe string) {
	if _, err := harness.QuotePath(exe); err != nil {
		logger.Warn("status line is off", "err", err)
	}
}

// watchSeq reads the change counter of the worktree containing dir. The root is
// resolved with git, never by comparing cwd strings.
func watchSeq(watches *git.WatchSet) seqFunc {
	return func(ctx context.Context, dir string) (string, uint64, bool) {
		r, err := git.Open(ctx, dir)
		if err != nil {
			return "", 0, false
		}

		n, ok := watches.Seq(r.Root)

		return r.Root, n, ok
	}
}

// sweepRunDir removes the socket, settings file and temporary indexes of
// instances that are gone.
// Other names, and files of live instances, stay.
func sweepRunDir(logger *slog.Logger, dir string, alive func(pid int) bool) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		logger.Warn("read run dir", "err", err)

		return
	}

	for _, e := range entries {
		pid, ok := runFilePid(e.Name())
		if !ok || alive(pid) {
			continue
		}

		if err := os.Remove(filepath.Join(dir, e.Name())); err != nil && !errors.Is(err, os.ErrNotExist) {
			logger.Warn("remove stale run file", "name", e.Name(), "err", err)
		}
	}
}

// settingsForSock names the settings file that sits next to the socket
// "<dir>/<pid>.sock", so the writer and the claude wrapper agree on it.
func settingsForSock(sock string) (string, bool) {
	base, ok := strings.CutSuffix(sock, ".sock")
	if !ok {
		return "", false
	}

	return base + "-claude.json", true
}

// runFilePid extracts the pid from "<pid>.sock", "<pid>-claude.json" and
// "<pid>-index-<anything>" (a snapshot's temporary index or its lock).
func runFilePid(name string) (int, bool) {
	digits, ok := strings.CutSuffix(name, ".sock")
	if !ok {
		digits, ok = strings.CutSuffix(name, "-claude.json")
	}

	if !ok {
		digits, _, ok = strings.Cut(name, "-index-")
	}

	if !ok {
		return 0, false
	}

	pid, err := strconv.Atoi(digits)

	return pid, err == nil && pid > 0 && digits == strconv.Itoa(pid)
}

// pidAlive reports whether a process with pid exists; a process of another
// user answers EPERM and counts as alive.
func pidAlive(pid int) bool {
	return !errors.Is(unix.Kill(pid, 0), unix.ESRCH)
}
