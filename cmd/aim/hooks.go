package main

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"golang.org/x/sys/unix"

	"github.com/mhersson/aim/internal/git"
	"github.com/mhersson/aim/internal/harness"
	"github.com/mhersson/aim/internal/store"
	"github.com/mhersson/aim/internal/term"
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
	Shell          term.Integration // the zero value when the integration dir was not written

	stop func()
}

// Stop ends the socket server and removes this instance's files and
// integration dir; it is safe on a runtime whose setup failed.
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

// startHooks prepares <stateDir>/run: it sweeps files of dead instances, places
// this instance's binary, writes its settings file and serves its socket. A
// failure leaves nothing behind and yields a runtime with a notice, so agents
// start without hooks. With shell it also writes the shell integration dir.
func startHooks(logger *slog.Logger, stateDir, indexCache string, watches *git.WatchSet, places *git.PlaceCache, shell bool) hookRuntime {
	h, err := setupHooks(logger, stateDir, indexCache, watches, places, shell, defaultHookTimes())
	if err != nil {
		logger.Warn("agent hooks are off", "err", err)

		return hookRuntime{Notice: "agent hooks are off: " + err.Error()}
	}

	return h
}

// hookTimes are the hook server's hold and the turn snapshot's budget.
type hookTimes struct{ hold, budget time.Duration }

func defaultHookTimes() hookTimes { return hookTimes{hold: turnHold, budget: turnBudget} }

func setupHooks(logger *slog.Logger, stateDir, indexCache string, watches *git.WatchSet, places *git.PlaceCache, shell bool, times hookTimes) (_ hookRuntime, err error) {
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

	// Hooks, the status line and the wrapper run this instance's own binary, so an
	// upgrade can neither delete the binary under them nor mix versions.
	bin := filepath.Join(runDir, strconv.Itoa(pid)+"-aim")
	if err := placeBinary(exe, bin, os.Link); err != nil {
		logger.Warn("hooks run from the installed binary", "path", bin, "err", err)

		bin = ""
	}

	defer func() {
		if err != nil && bin != "" {
			_ = os.Remove(bin)
		}
	}()

	hookExe := cmp.Or(bin, exe)

	settings, err := harness.Claude{}.Settings(hookExe)
	if err != nil {
		return hookRuntime{}, err
	}

	warnStatusLineOff(logger, hookExe)

	// A per-instance file needs no sync: a crash leaves it to the next sweep.
	if err := store.WriteAtomicNoSync(settingsPath, settings); err != nil {
		return hookRuntime{}, fmt.Errorf("settings file: %w", err)
	}

	var (
		shellDir string
		ig       term.Integration
	)

	if shell {
		target := bin
		if target == "" {
			target = wrapperTarget(os.Args[0], exe, exec.LookPath)
		}

		shellDir = filepath.Join(runDir, strconv.Itoa(pid)+"-shell")
		if err := term.WriteIntegration(shellDir, target); err != nil {
			logger.Warn("shell integration is off", "err", err)

			shellDir = ""
		} else {
			ig = term.NewIntegration(shellDir, os.Getenv)
		}
	}

	srv, err := harness.Listen(sockPath)
	if err != nil {
		_ = os.Remove(settingsPath)

		if shellDir != "" {
			_ = os.RemoveAll(shellDir)
		}

		return hookRuntime{}, err
	}

	srv.Log = logger
	srv.Hold = times.hold
	srv.Budget = times.budget

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

		if shellDir != "" {
			if err := os.RemoveAll(shellDir); err != nil {
				logger.Warn("remove shell integration", "err", err)
			}
		}

		if bin != "" {
			if err := os.Remove(bin); err != nil && !errors.Is(err, os.ErrNotExist) {
				logger.Warn("remove instance binary", "err", err)
			}
		}
	})

	return hookRuntime{Settings: settingsPath, Sock: sockPath, RunDir: runDir, Events: events, Shell: ig, stop: stop}, nil
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

// wrapperTarget is the path bin/claude links to: the path AIM was started by
// (arg0, looked up with lookPath when it has no slash, made absolute) when it
// names the running binary exe, else exe. That path survives a package
// manager relinking bin/aim or an install over the binary.
func wrapperTarget(arg0, exe string, lookPath func(string) (string, error)) string {
	p := arg0
	if p == "" {
		return exe
	}

	if !strings.Contains(p, "/") {
		found, err := lookPath(p)
		if err != nil {
			return exe
		}

		p = found
	}

	abs, err := filepath.Abs(p)
	if err != nil {
		return exe
	}

	a, errA := os.Stat(abs) //nolint:gosec // G703: abs is AIM's own argv[0] or a PATH lookup, only stat-ed
	b, errB := os.Stat(exe)

	if errA != nil || errB != nil || !os.SameFile(a, b) {
		return exe
	}

	return abs
}

// placeBinary puts the binary src at dst: a hard link when link succeeds, else
// a copy renamed into place. A link fails across filesystems, on an old file at
// dst and, on Linux with fs.protected_hardlinks, for a file the user does not own.
func placeBinary(src, dst string, link func(oldname, newname string) error) error {
	linkErr := link(src, dst)
	if linkErr == nil {
		return nil
	}

	if err := copyBinary(src, dst); err != nil {
		return fmt.Errorf("%w; copy: %w", linkErr, err)
	}

	return nil
}

// copyBinary copies src to dst through "<dst>-*", a name sweepRunDir removes
// once the instance is gone.
func copyBinary(src, dst string) (err error) {
	in, err := os.Open(src) //nolint:gosec // G304: src is AIM's own executable
	if err != nil {
		return err
	}

	defer func() { _ = in.Close() }()

	f, err := os.CreateTemp(filepath.Dir(dst), filepath.Base(dst)+"-*")
	if err != nil {
		return err
	}

	tmp := f.Name()

	defer func() {
		if err != nil {
			_ = os.Remove(tmp)
		}
	}()

	if _, err = io.Copy(f, in); err == nil {
		err = f.Chmod(0o755)
	}

	if cerr := f.Close(); err == nil {
		err = cerr
	}

	if err != nil {
		return err
	}

	return os.Rename(tmp, dst)
}

// sweepRunDir removes the socket, settings file, integration dir, binary and
// temporary indexes of instances that are gone.
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

		if err := os.RemoveAll(filepath.Join(dir, e.Name())); err != nil && !errors.Is(err, os.ErrNotExist) {
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

// runFilePid extracts the pid from "<pid>.sock", "<pid>-claude.json",
// "<pid>-aim" and "<pid>-aim-<anything>" (the binary and its temporary copy),
// "<pid>-index-<anything>" (a snapshot's temporary index or its lock) and the
// integration dir "<pid>-shell".
func runFilePid(name string) (int, bool) {
	digits, ok := strings.CutSuffix(name, ".sock")
	if !ok {
		digits, ok = strings.CutSuffix(name, "-claude.json")
	}

	if !ok {
		digits, ok = strings.CutSuffix(name, "-shell")
	}

	if !ok {
		digits, ok = strings.CutSuffix(name, "-aim")
	}

	if !ok {
		digits, _, ok = strings.Cut(name, "-index-")
	}

	if !ok {
		digits, _, ok = strings.Cut(name, "-aim-")
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
