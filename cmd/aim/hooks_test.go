package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
	"unicode"
	"unicode/utf8"

	tea "charm.land/bubbletea/v2"

	"github.com/mhersson/aim/internal/core"
	"github.com/mhersson/aim/internal/git"
	"github.com/mhersson/aim/internal/term"
)

func discardLogger() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

func TestRunServesHooks(t *testing.T) {
	t.Setenv("HOME", shortDir(t))
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	stateHome := shortDir(t)
	t.Setenv("XDG_STATE_HOME", stateHome)

	env, err := core.DefaultEnv()
	if err != nil {
		t.Fatal(err)
	}

	// No native clipboard tool may run from a test.
	cfg := filepath.Join(t.TempDir(), "config.toml")
	if err := os.WriteFile(cfg, []byte("[clipboard]\nnative = false\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	in, inW := io.Pipe()

	t.Cleanup(func() { _ = inW.Close() })

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()

	done := make(chan error, 1)

	go func() {
		done <- run(ctx, env, cfg, "", false, noClients, tea.WithInput(in), tea.WithOutput(io.Discard))
	}()

	runDir := filepath.Join(core.StateDir(env), "run")
	pid := strconv.Itoa(os.Getpid())
	sock := filepath.Join(runDir, pid+".sock")
	settings := filepath.Join(runDir, pid+"-claude.json")

	// Listen binds the socket before it chmods it, so the file can exist at the
	// umask's mode first: wait for the final mode, not for the file. A run that
	// returns first removes both files, so its error is the failure.
	waitUntil(t, "the hook socket never reached mode 0600, or the settings file never appeared", func() bool {
		select {
		case err := <-done:
			t.Fatalf("run() returned before it served hooks: %v", err)
		default:
		}

		fi, err := os.Lstat(sock)

		return err == nil && fi.Mode().Perm() == 0o600 && exists(settings)
	})

	if fi, err := os.Stat(runDir); err != nil || fi.Mode().Perm() != 0o700 {
		t.Errorf("run dir = %v, %v, want mode 0700", fi, err)
	}

	for _, p := range []string{sock, settings} {
		if fi, err := os.Stat(p); err != nil || fi.Mode().Perm() != 0o600 {
			t.Errorf("%s = %v, %v, want mode 0600", p, fi, err)
		}
	}

	data, err := os.ReadFile(settings)
	if err != nil || !strings.Contains(string(data), `"hook"`) {
		t.Errorf("settings = %q, %v, want the hook command", data, err)
	}

	cancel()

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("run() error = %v", err)
		}
	case <-time.After(hangGuard):
		t.Fatal("run did not return after its context ended")
	}

	if exists(sock) || exists(settings) {
		t.Errorf("socket exists = %v, settings exists = %v, want both removed", exists(sock), exists(settings))
	}
}

func TestStartHooksFailureTurnsHooksOff(t *testing.T) {
	stateDir := t.TempDir()
	// A file where the run dir belongs makes the setup fail without relying on permissions.
	if err := os.WriteFile(filepath.Join(stateDir, "run"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}

	h := startHooks(discardLogger(), stateDir, "", testWatches(t), testPlaces(), false)
	defer h.Stop()

	if h.Settings != "" || h.Sock != "" || h.Events != nil {
		t.Errorf("runtime = %+v, want no settings, socket or events", h)
	}

	if h.Notice == "" {
		t.Error("no notice, want one that says the hooks are off")
	}
}

func TestStartHooksNamesAnOverlongSocketPath(t *testing.T) {
	stateDir := filepath.Join(t.TempDir(), strings.Repeat("d", maxSockPath))

	h := startHooks(discardLogger(), stateDir, "", testWatches(t), testPlaces(), false)
	defer h.Stop()

	if h.Settings != "" || h.Sock != "" || h.Events != nil {
		t.Errorf("runtime = %+v, want no settings, socket or events", h)
	}

	sock := filepath.Join(stateDir, "run", strconv.Itoa(os.Getpid())+".sock")

	for _, n := range []int{len(sock), maxSockPath} {
		if !strings.Contains(h.Notice, strconv.Itoa(n)) {
			t.Errorf("notice = %q, want it to carry %d", h.Notice, n)
		}
	}
}

func TestWarnStatusLineOff(t *testing.T) {
	tests := []struct {
		name string
		exe  string
		warn bool
	}{
		{"quotable path", "/opt/aim/bin/aim", false},
		{"single quote", "/opt/a'im/aim", true},
		{"escape character", "/opt/a\x1b[31mim/aim", true},
		{"invalid UTF-8", "/opt/a\xffim/aim", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var buf bytes.Buffer

			warnStatusLineOff(slog.New(slog.NewTextHandler(&buf, nil)), tt.exe)

			got := buf.String()
			if !tt.warn {
				if got != "" {
					t.Fatalf("log = %q, want no warning", got)
				}

				return
			}

			if strings.Count(got, "\n") != 1 || !strings.HasSuffix(got, "\n") || !strings.Contains(got, "level=WARN") {
				t.Fatalf("log = %q, want one WARN record", got)
			}

			body := strings.TrimSuffix(got, "\n")
			if i := strings.IndexFunc(body, unicode.IsControl); i >= 0 || !utf8.ValidString(body) {
				t.Errorf("log = %q, want no raw control byte and valid UTF-8", got)
			}
		})
	}
}

func TestStartHooksSweepsDeadInstances(t *testing.T) {
	stateDir := shortDir(t)
	runDir := filepath.Join(stateDir, "run")

	if err := os.MkdirAll(runDir, 0o700); err != nil {
		t.Fatal(err)
	}

	dead := deadPid(t)

	// Another live process: the parent of the test binary.
	live := os.Getppid()

	files := map[string]bool{ // name -> kept
		strconv.Itoa(dead) + ".sock":            false,
		strconv.Itoa(dead) + "-claude.json":     false,
		strconv.Itoa(live) + ".sock":            true,
		strconv.Itoa(live) + "-claude.json":     true,
		"notes.txt":                             true,
		"abc.sock":                              true,
		"007.sock":                              true,
		strconv.Itoa(dead) + "-claude.json~":    true,
		strconv.Itoa(dead) + "-index-a1b2":      false,
		strconv.Itoa(dead) + "-index-a1b2.lock": false,
		strconv.Itoa(live) + "-index-a1b2":      true,
		strconv.Itoa(live) + "-index-a1b2.lock": true,
		"abc-index-a1b2":                        true,
		strconv.Itoa(dead) + "-indexed":         true,
		strconv.Itoa(dead) + "-aim":             false,
		strconv.Itoa(dead) + "-aim-0123":        false,
		strconv.Itoa(live) + "-aim":             true,
		strconv.Itoa(dead) + "-aimed":           true,
	}
	for name := range files {
		if err := os.WriteFile(filepath.Join(runDir, name), nil, 0o600); err != nil {
			t.Fatal(err)
		}
	}

	shellDirs := map[string]bool{ // a dir with a symlink in it -> kept
		strconv.Itoa(dead) + "-shell": false,
		strconv.Itoa(live) + "-shell": true,
	}
	for name := range shellDirs {
		if err := os.MkdirAll(filepath.Join(runDir, name, "bin"), 0o700); err != nil {
			t.Fatal(err)
		}

		if err := os.Symlink("/nonexistent/aim", filepath.Join(runDir, name, "bin", "claude")); err != nil {
			t.Fatal(err)
		}

		files[name] = shellDirs[name]
	}

	h := startHooks(discardLogger(), stateDir, "", testWatches(t), testPlaces(), false)
	defer h.Stop()

	if h.Notice != "" {
		t.Fatalf("notice = %q, want none", h.Notice)
	}

	for name, kept := range files {
		if got := exists(filepath.Join(runDir, name)); got != kept {
			t.Errorf("%s exists = %v, want %v", name, got, kept)
		}
	}

	if !exists(h.Sock) || !exists(h.Settings) {
		t.Error("this instance's files are missing")
	}

	h.Stop()

	if exists(h.Sock) || exists(h.Settings) {
		t.Error("Stop left this instance's files behind")
	}
}

func TestStartHooksWritesTheShellIntegration(t *testing.T) {
	t.Parallel()

	t.Run("on", func(t *testing.T) {
		stateDir := shortDir(t)

		h := startHooks(discardLogger(), stateDir, "", testWatches(t), testPlaces(), true)
		defer h.Stop()

		if h.Notice != "" {
			t.Fatalf("notice = %q, want none", h.Notice)
		}

		dir := filepath.Join(stateDir, "run", strconv.Itoa(os.Getpid())+"-shell")
		if h.Shell.Dir != dir {
			t.Fatalf("Shell.Dir = %q, want %q", h.Shell.Dir, dir)
		}

		for _, f := range []string{"zsh/.zshenv", "bash/init.bash", "fish/init.fish"} {
			if !exists(filepath.Join(dir, f)) {
				t.Errorf("%s is missing", f)
			}
		}

		got, err := os.Readlink(filepath.Join(dir, "bin", "claude"))
		if err != nil {
			t.Fatal(err)
		}

		if want := filepath.Join(stateDir, "run", strconv.Itoa(os.Getpid())+"-aim"); got != want {
			t.Errorf("bin/claude -> %q, want the instance's binary %q", got, want)
		}

		h.Stop()

		if exists(dir) {
			t.Error("Stop left the integration dir behind")
		}
	})

	t.Run("off", func(t *testing.T) {
		stateDir := shortDir(t)

		h := startHooks(discardLogger(), stateDir, "", testWatches(t), testPlaces(), false)
		defer h.Stop()

		if h.Shell != (term.Integration{}) {
			t.Errorf("Shell = %+v, want zero", h.Shell)
		}

		if exists(filepath.Join(stateDir, "run", strconv.Itoa(os.Getpid())+"-shell")) {
			t.Error("the integration dir exists with the switch off")
		}
	})

	t.Run("socket cannot be served", func(t *testing.T) {
		stateDir := shortDir(t)
		runDir := filepath.Join(stateDir, "run")
		pid := strconv.Itoa(os.Getpid())

		// A regular file at the socket path makes Listen refuse, after the dir was written.
		if err := os.MkdirAll(runDir, 0o700); err != nil {
			t.Fatal(err)
		}

		if err := os.WriteFile(filepath.Join(runDir, pid+".sock"), nil, 0o600); err != nil {
			t.Fatal(err)
		}

		h := startHooks(discardLogger(), stateDir, "", testWatches(t), testPlaces(), true)
		defer h.Stop()

		if h.Settings != "" || h.Notice == "" {
			t.Fatalf("runtime = %+v, want hooks off", h)
		}

		for _, name := range []string{pid + "-claude.json", pid + "-shell", pid + "-aim"} {
			if exists(filepath.Join(runDir, name)) {
				t.Errorf("a failed setup left %s behind", name)
			}
		}
	})

	t.Run("settings file cannot be written", func(t *testing.T) {
		stateDir := shortDir(t)
		runDir := filepath.Join(stateDir, "run")

		// A non-empty dir where the settings file belongs makes the rename fail.
		blocker := filepath.Join(runDir, strconv.Itoa(os.Getpid())+"-claude.json")
		if err := os.MkdirAll(filepath.Join(blocker, "x"), 0o700); err != nil {
			t.Fatal(err)
		}

		h := startHooks(discardLogger(), stateDir, "", testWatches(t), testPlaces(), true)
		defer h.Stop()

		if h.Settings != "" || h.Notice == "" {
			t.Fatalf("runtime = %+v, want hooks off", h)
		}

		if h.Shell != (term.Integration{}) || exists(filepath.Join(runDir, strconv.Itoa(os.Getpid())+"-shell")) {
			t.Error("the integration dir was written for a failed setup")
		}

		if exists(filepath.Join(runDir, strconv.Itoa(os.Getpid())+"-aim")) {
			t.Error("a failed setup left the instance's binary behind")
		}
	})
}

func TestStartHooksRunsHooksFromTheInstanceBinary(t *testing.T) {
	t.Parallel()

	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}

	stateDir := shortDir(t)

	h := startHooks(discardLogger(), stateDir, "", testWatches(t), testPlaces(), false)
	defer h.Stop()

	if h.Notice != "" {
		t.Fatalf("notice = %q, want none", h.Notice)
	}

	bin := filepath.Join(stateDir, "run", strconv.Itoa(os.Getpid())+"-aim")
	sameBinary(t, bin, exe)

	if h.Bin != bin {
		t.Errorf("Bin = %q, want %q", h.Bin, bin)
	}

	hooks, status := hookCommands(t, h.Settings)
	for _, c := range hooks {
		if c != bin {
			t.Errorf("hook command = %q, want %q", c, bin)
		}
	}

	if want := "'" + bin + "' statusline"; status != want {
		t.Errorf("status line = %q, want %q", status, want)
	}

	h.Stop()

	if exists(bin) {
		t.Error("Stop left the instance's binary behind")
	}
}

func TestStartHooksKeepsTheInstalledPathsWhenTheBinaryCannotBePlaced(t *testing.T) {
	t.Parallel()

	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}

	stateDir := shortDir(t)
	runDir := filepath.Join(stateDir, "run")
	pid := strconv.Itoa(os.Getpid())
	bin := filepath.Join(runDir, pid+"-aim")

	// A non-empty dir where the binary belongs fails both the link and the copy's rename.
	if err := os.MkdirAll(filepath.Join(bin, "x"), 0o700); err != nil {
		t.Fatal(err)
	}

	var log bytes.Buffer

	h := startHooks(slog.New(slog.NewTextHandler(&log, nil)), stateDir, "", testWatches(t), testPlaces(), true)
	defer h.Stop()

	if h.Notice != "" {
		t.Fatalf("notice = %q, want hooks on", h.Notice)
	}

	if h.Bin != exe {
		t.Errorf("Bin = %q, want the running binary %q", h.Bin, exe)
	}

	hooks, status := hookCommands(t, h.Settings)
	for _, c := range hooks {
		if c != exe {
			t.Errorf("hook command = %q, want the running binary %q", c, exe)
		}
	}

	if want := "'" + exe + "' statusline"; status != want {
		t.Errorf("status line = %q, want %q", status, want)
	}

	got, err := os.Readlink(filepath.Join(h.Shell.Dir, "bin", "claude"))
	if err != nil {
		t.Fatal(err)
	}

	if want := wrapperTarget(os.Args[0], exe, exec.LookPath); got != want {
		t.Errorf("bin/claude -> %q, want %q", got, want)
	}

	h.Stop()

	if s := log.String(); !strings.Contains(s, "level=WARN") || !strings.Contains(s, "path="+bin) {
		t.Errorf("log = %q, want a warning that names %s", s, bin)
	}

	temps, err := filepath.Glob(bin + "-*")
	if err != nil || len(temps) > 0 {
		t.Errorf("temporary copies left = %v, %v, want none", temps, err)
	}
}

func TestPlaceBinary(t *testing.T) {
	t.Parallel()

	refuse := func(string, string) error { return errors.New("invalid cross-device link") }

	tests := []struct {
		name    string
		link    func(oldname, newname string) error
		mode    os.FileMode // the binary's
		viaLink bool        // the binary is named by a relative symlink, as on macOS
		old     bool        // a file is already at the destination
		owned   bool        // the result must be a copy of its own with mode 0755
		fails   bool        // the placed file cannot run, so nothing stays placed
	}{
		{"link refused", refuse, 0o600, false, false, true, false},
		{"old file in the way", os.Link, 0o755, false, true, false, false},
		{"symlinked binary", os.Link, 0o755, true, false, false, false},
		{"placed file cannot run", os.Link, 0o644, false, false, false, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			dir := t.TempDir()
			file := filepath.Join(dir, "real", "aim")
			runDir := filepath.Join(dir, "state", "run")
			dst := filepath.Join(runDir, "4242-aim")

			for _, d := range []string{filepath.Dir(file), runDir} {
				if err := os.MkdirAll(d, 0o700); err != nil {
					t.Fatal(err)
				}
			}

			if err := os.WriteFile(file, []byte("the binary"), tt.mode); err != nil {
				t.Fatal(err)
			}

			src := file
			if tt.viaLink {
				src = filepath.Join(dir, "bin", "aim")
				if err := os.MkdirAll(filepath.Dir(src), 0o700); err != nil {
					t.Fatal(err)
				}

				if err := os.Symlink(filepath.Join("..", "real", "aim"), src); err != nil {
					t.Fatal(err)
				}
			}

			if tt.old {
				if err := os.WriteFile(dst, []byte("an older binary"), 0o600); err != nil {
					t.Fatal(err)
				}
			}

			err := placeBinary(src, dst, tt.link)

			entries, rerr := os.ReadDir(runDir)
			if want := map[bool]int{false: 1, true: 0}[tt.fails]; rerr != nil || len(entries) != want {
				t.Errorf("run dir = %v, %v, want %d entries", entries, rerr, want)
			}

			if tt.fails {
				if err == nil {
					t.Fatal("placeBinary = nil, want an error for a file that cannot run")
				}

				return
			}

			if err != nil {
				t.Fatalf("placeBinary: %v", err)
			}

			fi, err := os.Lstat(dst)
			if err != nil {
				t.Fatal(err)
			}

			if !fi.Mode().IsRegular() {
				t.Fatalf("destination mode = %v, want a regular file", fi.Mode())
			}

			if got, err := os.ReadFile(dst); err != nil || string(got) != "the binary" {
				t.Fatalf("destination = %q, %v, want the binary's content", got, err)
			}

			if tt.owned {
				a, errA := os.Stat(file)

				b, errB := os.Stat(dst)
				if errA != nil || errB != nil {
					t.Fatal(errA, errB)
				}

				if os.SameFile(a, b) || b.Mode().Perm() != 0o755 {
					t.Errorf("destination: same file = %v, mode %v, want a copy with mode 0755", os.SameFile(a, b), b.Mode().Perm())
				}
			}
		})
	}
}

// sameBinary fails the test unless path is exe or a byte-for-byte copy of it.
func sameBinary(t *testing.T, path, exe string) {
	t.Helper()

	a, errA := os.Stat(path)

	b, errB := os.Stat(exe)
	if errA != nil || errB != nil {
		t.Fatalf("stat: %v, %v", errA, errB)
	}

	if os.SameFile(a, b) {
		return
	}

	got, errA := os.ReadFile(path) //nolint:gosec // G304: a test's own temp file

	want, errB := os.ReadFile(exe)
	if errA != nil || errB != nil || !bytes.Equal(got, want) {
		t.Fatalf("%s is neither %s nor a copy of it (%v, %v)", path, exe, errA, errB)
	}
}

// hookCommands returns every hook's command in the settings file at path and
// the status line's command.
func hookCommands(t *testing.T, path string) (hooks []string, status string) {
	t.Helper()

	data, err := os.ReadFile(path) //nolint:gosec // G304: a test's own temp file
	if err != nil {
		t.Fatal(err)
	}

	type handler struct {
		Command string `json:"command"`
	}

	var s struct {
		Hooks map[string][]struct {
			Hooks []handler `json:"hooks"`
		} `json:"hooks"`
		StatusLine handler `json:"statusLine"`
	}

	if err := json.Unmarshal(data, &s); err != nil {
		t.Fatal(err)
	}

	for _, groups := range s.Hooks {
		for _, g := range groups {
			for _, h := range g.Hooks {
				hooks = append(hooks, h.Command)
			}
		}
	}

	if len(hooks) == 0 {
		t.Fatalf("settings = %s, want hooks", data)
	}

	return hooks, s.StatusLine.Command
}

func TestWrapperTarget(t *testing.T) {
	dir := t.TempDir()
	exe := filepath.Join(dir, "real", "aim")
	other := filepath.Join(dir, "other", "aim")

	for _, p := range []string{exe, other} {
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}

		if err := os.WriteFile(p, []byte(p), 0o755); err != nil { //nolint:gosec // test file
			t.Fatal(err)
		}
	}

	link := filepath.Join(dir, "brew", "aim")
	if err := os.MkdirAll(filepath.Dir(link), 0o755); err != nil {
		t.Fatal(err)
	}

	if err := os.Symlink(exe, link); err != nil {
		t.Fatal(err)
	}

	t.Chdir(dir)

	lookup := func(found string, err error) func(string) (string, error) {
		return func(string) (string, error) { return found, err }
	}

	tests := []struct {
		name string
		arg0 string
		look func(string) (string, error)
		want string
	}{
		{"found on PATH, same file", "aim", lookup(exe, nil), exe},
		{"relative, same file", "./real/aim", lookup("", errors.New("unused")), exe},
		{"another file", "aim", lookup(other, nil), exe},
		{"lookup error", "aim", lookup("", errors.New("not found")), exe},
		{"symlink to exe", "aim", lookup(link, nil), link},
		{"empty arg0", "", lookup(exe, nil), exe},
	}

	for _, tt := range tests {
		if got := wrapperTarget(tt.arg0, exe, tt.look); got != tt.want {
			t.Errorf("%s: wrapperTarget = %q, want %q", tt.name, got, tt.want)
		}
	}
}

// deadPid returns the pid of a child that has exited and been reaped.
func deadPid(t *testing.T) int {
	t.Helper()

	c := exec.Command("true")
	if err := c.Run(); err != nil {
		t.Fatal(err)
	}

	return c.Process.Pid
}

func exists(path string) bool {
	_, err := os.Lstat(path)

	return err == nil
}

func waitFor(t *testing.T, cond func() bool) {
	t.Helper()

	waitUntil(t, "condition not met in time", cond)
}

// waitUntil polls cond until it holds and fails the test with failure if it
// still does not within hangGuard.
func waitUntil(t *testing.T, failure string, cond func() bool) {
	t.Helper()

	deadline := time.Now().Add(hangGuard)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatal(failure)
		}

		time.Sleep(10 * time.Millisecond)
	}
}

func testPlaces() *git.PlaceCache { return git.NewPlaceCache(git.EditPlace, time.Now) }

func testWatches(t *testing.T) *git.WatchSet {
	t.Helper()

	w := git.NewWatchSet(context.Background(), git.WatchOptions{})

	t.Cleanup(func() { _ = w.Close() })

	return w
}

// It needs real git: the root comes from git rev-parse.
func TestWatchSeqResolvesTheRootFromASubdirectory(t *testing.T) {
	isolateGit(t)

	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}

	gitOut(t, dir, "init", "-q", "-b", "main")

	sub := filepath.Join(dir, "sub")

	if err := os.Mkdir(sub, 0o700); err != nil {
		t.Fatal(err)
	}

	ctx := context.Background()
	watches := testWatches(t)
	seq := watchSeq(watches)

	if root, _, ok := seq(ctx, sub); ok || root != dir {
		t.Fatalf("unwatched: root = %q, ok = %v", root, ok)
	}

	r, err := git.Open(ctx, dir)
	if err != nil {
		t.Fatal(err)
	}

	release, err := watches.Acquire(ctx, r)
	if err != nil {
		t.Fatal(err)
	}

	defer release()

	if root, _, ok := seq(ctx, sub); !ok || root != dir {
		t.Fatalf("watched: root = %q, ok = %v", root, ok)
	}

	if _, _, ok := seq(ctx, t.TempDir()); ok {
		t.Fatal("a directory outside any repository reported a watcher")
	}
}
