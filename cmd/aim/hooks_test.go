package main

import (
	"bytes"
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
	"testing"
	"time"
	"unicode"
	"unicode/utf8"

	tea "charm.land/bubbletea/v2"

	"github.com/mhersson/aim/internal/core"
	"github.com/mhersson/aim/internal/git"
	"github.com/mhersson/aim/internal/harness"
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
	case <-time.After(10 * time.Second):
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

	if !strings.HasPrefix(h.Notice, "agent hooks are off: ") {
		t.Errorf("notice = %q, want the hooks-off notice", h.Notice)
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
	want := fmt.Sprintf("agent hooks are off: socket path too long (%d bytes, at most %d)", len(sock), maxSockPath)

	if h.Notice != want {
		t.Errorf("notice = %q, want %q", h.Notice, want)
	}
}

func TestWarnStatusLineOff(t *testing.T) {
	tests := []struct {
		name string
		exe  string
		want string // what the warning names; empty for no warning
	}{
		{"quotable path", "/opt/aim/bin/aim", ""},
		{"single quote", "/opt/a'im/aim", "single quote"},
		{"backslash", `/opt/a\im/aim`, "backslash"},
		{"escape character", "/opt/a\x1b[31mim/aim", "U+001B"},
		{"newline", "/opt/a\nim/aim", "U+000A"},
		{"invalid UTF-8", "/opt/a\xffim/aim", "UTF-8"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var buf bytes.Buffer

			warnStatusLineOff(slog.New(slog.NewTextHandler(&buf, nil)), tt.exe)

			got := buf.String()
			if tt.want == "" {
				if got != "" {
					t.Fatalf("log = %q, want no warning", got)
				}

				return
			}

			if strings.Count(got, "\n") != 1 || !strings.HasSuffix(got, "\n") {
				t.Fatalf("log = %q, want one record", got)
			}

			for _, want := range []string{"level=WARN", "status line is off", tt.want} {
				if !strings.Contains(got, want) {
					t.Errorf("log = %q, want it to contain %q", got, want)
				}
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

	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}

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

		if want := wrapperTarget(os.Args[0], exe, exec.LookPath); got != want {
			t.Errorf("bin/claude -> %q, want %q", got, want)
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

		if h.Settings != "" || !strings.HasPrefix(h.Notice, "agent hooks are off: ") {
			t.Fatalf("runtime = %+v, want hooks off", h)
		}

		if exists(filepath.Join(runDir, pid+"-claude.json")) || exists(filepath.Join(runDir, pid+"-shell")) {
			t.Error("a failed setup left the settings file or the integration dir behind")
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

		if h.Settings != "" || !strings.HasPrefix(h.Notice, "agent hooks are off: ") {
			t.Fatalf("runtime = %+v, want hooks off", h)
		}

		if h.Shell != (term.Integration{}) || exists(filepath.Join(runDir, strconv.Itoa(os.Getpid())+"-shell")) {
			t.Error("the integration dir was written for a failed setup")
		}
	})
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

func TestPidAlive(t *testing.T) {
	if !pidAlive(os.Getpid()) {
		t.Error("the running process is not alive")
	}

	if pidAlive(deadPid(t)) {
		t.Error("a reaped process is alive")
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
// still does not after 10 seconds.
func waitUntil(t *testing.T, failure string, cond func() bool) {
	t.Helper()

	deadline := time.Now().Add(10 * time.Second)
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

func TestWatchSeqResolvesTheRootFromASubdirectory(t *testing.T) {
	dir := seededRepo(t)
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

// A turn ends at the pane's next prompt, so it keeps what changed after Stop.
func TestHookNextPromptWritesTheEnd(t *testing.T) {
	repo := seededRepo(t)

	h, err := setupHooks(discardLogger(), shortDir(t), "", testWatches(t), testPlaces(), false)
	if err != nil {
		t.Fatal(err)
	}

	t.Cleanup(h.Stop)

	sendTurn(t, h.Sock, repo, gateSession)

	if err := os.WriteFile(filepath.Join(repo, "during.txt"), []byte("during\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	stop := harness.Event{Name: "Stop", Session: gateSession, Cwd: repo, Pane: "p"}
	if err := harness.Send(t.Context(), h.Sock, stop); err != nil {
		t.Fatalf("Send(Stop) error = %v", err)
	}

	if turns := gitTurns(t, repo); len(turns) != 1 || turns[0].EndCommit != "" {
		t.Fatalf("turns after Stop = %+v, want one open turn", turns)
	}

	if err := os.WriteFile(filepath.Join(repo, "after.txt"), []byte("after\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	sendTurn(t, h.Sock, repo, gateSession)

	turns := gitTurns(t, repo)
	if len(turns) != 2 || turns[0].EndCommit == "" || turns[0].EndMissing || turns[1].EndCommit != "" {
		t.Fatalf("turns = %+v, want the first ended and the second open", turns)
	}

	for _, name := range []string{"during.txt", "after.txt"} {
		if out := gitOut(t, repo, "ls-tree", "--name-only", turns[0].EndTree, name); out != name {
			t.Errorf("first turn's end tree lacks %s", name)
		}
	}
}
