package git

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

func TestRunIgnoresInheritedRepoVariables(t *testing.T) {
	root := newRepo(t)

	t.Setenv("GIT_DIR", "/nonexistent")
	t.Setenv("GIT_INDEX_FILE", "/nonexistent")
	t.Setenv("GIT_WORK_TREE", "/nonexistent")

	envLog := fakeGit(t, `env > "$LOG.env"`)

	out, err := run(context.Background(), call{dir: root, args: []string{"rev-parse", "--show-toplevel"}, read: true})
	if err != nil {
		t.Fatal(err)
	}

	if got := strings.TrimSpace(string(out)); got != root {
		t.Errorf("toplevel = %q, want %q", got, root)
	}

	env, err := os.ReadFile(envLog + ".env")
	if err != nil {
		t.Fatal(err)
	}

	for _, name := range []string{"GIT_DIR=", "GIT_INDEX_FILE=", "GIT_WORK_TREE="} {
		if strings.Contains(string(env), name) {
			t.Errorf("environment still has %s", name)
		}
	}

	if !strings.Contains(string(env), "GIT_OPTIONAL_LOCKS=0") {
		t.Error("read call lacks GIT_OPTIONAL_LOCKS=0")
	}
}

func TestRunEnvAndLocks(t *testing.T) {
	t.Parallel()

	root := newRepo(t)
	ctx, envLog := wrapGit(t, `env > "$LOG.env"`)
	idx := filepath.Join(t.TempDir(), "index")

	if _, err := run(ctx, call{dir: root, args: []string{"status", "-z"}, env: []string{"GIT_INDEX_FILE=" + idx}}); err != nil {
		t.Fatal(err)
	}

	argv, err := os.ReadFile(envLog)
	if err != nil {
		t.Fatal(err)
	}

	if want := "-c core.fsmonitor=false -c core.quotePath=false status -z"; !strings.Contains(string(argv), want) {
		t.Errorf("log %q lacks %q", argv, want)
	}

	env, err := os.ReadFile(envLog + ".env")
	if err != nil {
		t.Fatal(err)
	}

	if !strings.Contains(string(env), "GIT_INDEX_FILE="+idx) {
		t.Error("call env not passed on")
	}

	if strings.Contains(string(env), "GIT_OPTIONAL_LOCKS") {
		t.Error("write call must not set GIT_OPTIONAL_LOCKS")
	}
}

func TestRunStdin(t *testing.T) {
	t.Parallel()
	root := t.TempDir()

	out, err := run(context.Background(), call{dir: root, args: []string{"hash-object", "--stdin"}, stdin: strings.NewReader("hello\n")})
	if err != nil {
		t.Fatal(err)
	}

	if got := strings.TrimSpace(string(out)); got != "ce013625030ba8dba906f756967f9e9ca394464a" {
		t.Errorf("hash = %s", got)
	}
}

func TestRunErrorCarriesStderrFirstLine(t *testing.T) {
	t.Parallel()
	root := t.TempDir()

	_, err := run(context.Background(), call{dir: root, args: []string{"rev-parse", "--verify", "nope"}, read: true})
	if err == nil {
		t.Fatal("want an error")
	}

	if !strings.Contains(err.Error(), "rev-parse") || !strings.Contains(err.Error(), "fatal:") {
		t.Errorf("err = %q", err)
	}
}

// alive reports whether pid is a running process (a zombie is not).
func alive(pid int) bool {
	if syscall.Kill(pid, 0) != nil {
		return false
	}

	stat, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/stat")
	if err != nil {
		return true
	}

	_, rest, _ := strings.Cut(string(stat), ") ")

	return !strings.HasPrefix(rest, "Z")
}

// cancelWhenWritten returns a context that is cancelled once path exists, so a
// test cancels a call at a known point rather than after a guessed delay.
func cancelWhenWritten(t *testing.T, path string) context.Context {
	t.Helper()

	return cancelWhenWrittenFrom(context.Background(), t, path)
}

// cancelWhenWrittenFrom is cancelWhenWritten for a context derived from parent.
func cancelWhenWrittenFrom(parent context.Context, t *testing.T, path string) context.Context {
	t.Helper()

	ctx, cancel := context.WithCancel(parent)
	t.Cleanup(cancel)

	go func() {
		for ctx.Err() == nil {
			if _, err := os.Stat(path); err == nil {
				cancel()

				return
			}

			time.Sleep(time.Millisecond)
		}
	}()

	return ctx
}

// The wrapper forks without pause, so the cancel lands mid-fork: a child that
// misses the group kill outlives the call.
func TestRunCancelKillsChildrenForkedDuringTheKill(t *testing.T) {
	root := newRepo(t)
	ctx, logPath := wrapGit(t, `echo $$ > "$LOG.pgid"
i=0
while [ $i -lt 300 ]; do
	sh -c 'echo $$ >> "$1"; exec sleep 30' sh "$LOG.pids" &
	i=$((i+1))
done
wait`)

	t.Cleanup(func() {
		if raw, err := os.ReadFile(logPath + ".pgid"); err == nil {
			if pgid, err := strconv.Atoi(strings.TrimSpace(string(raw))); err == nil {
				_ = syscall.Kill(-pgid, syscall.SIGKILL)
			}
		}
	})

	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	go func() {
		for ctx.Err() == nil {
			if raw, err := os.ReadFile(logPath + ".pids"); err == nil && strings.Count(string(raw), "\n") >= 5 {
				cancel()

				return
			}

			time.Sleep(time.Millisecond)
		}
	}()

	_, err := run(ctx, call{dir: root, args: []string{"status"}})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want canceled", err)
	}

	raw, err := os.ReadFile(logPath + ".pids")
	if err != nil {
		t.Fatal(err)
	}

	deadline := time.Now().Add(time.Second)

	for field := range strings.FieldsSeq(string(raw)) {
		pid, err := strconv.Atoi(field)
		if err != nil {
			t.Fatal(err)
		}

		for alive(pid) && time.Now().Before(deadline) {
			time.Sleep(10 * time.Millisecond)
		}

		if alive(pid) {
			t.Errorf("child %d survived cancel", pid)
		}
	}
}

func TestRunLimit(t *testing.T) {
	t.Parallel()

	root := newRepo(t)
	ctx, _ := wrapGit(t, "exec yes")

	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	_, err := run(ctx, call{dir: root, args: []string{"status"}, limit: 1000})
	if !errors.Is(err, errLimit) {
		t.Fatalf("err = %v, want errLimit", err)
	}
}

func TestRunLimitNotReached(t *testing.T) {
	t.Parallel()
	root := newRepo(t)

	out, err := run(context.Background(), call{dir: root, args: []string{"rev-parse", "--git-dir"}, limit: 1000})
	if err != nil || strings.TrimSpace(string(out)) != ".git" {
		t.Fatalf("out = %q, err = %v", out, err)
	}
}

func TestStream(t *testing.T) {
	t.Parallel()
	root := newRepo(t)

	rc, err := stream(context.Background(), call{dir: root, args: []string{"rev-parse", "--git-dir"}, read: true})
	if err != nil {
		t.Fatal(err)
	}

	data, err := io.ReadAll(rc)
	if err != nil {
		t.Fatal(err)
	}

	if strings.TrimSpace(string(data)) != ".git" {
		t.Errorf("data = %q", data)
	}

	if err := rc.Close(); err != nil {
		t.Errorf("Close = %v", err)
	}
}

func TestStreamCloseKillsEarly(t *testing.T) {
	t.Parallel()
	root := newRepo(t)
	ctx, _ := wrapGit(t, "exec yes")

	rc, err := stream(ctx, call{dir: root, args: []string{"status"}})
	if err != nil {
		t.Fatal(err)
	}

	buf := make([]byte, 16)
	if _, err := io.ReadFull(rc, buf); err != nil {
		t.Fatal(err)
	}

	done := make(chan error, 1)
	go func() { done <- rc.Close() }()

	select {
	case err := <-done:
		if err != nil {
			t.Errorf("Close = %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("Close did not return")
	}
}

func TestNetCallHasNoTerminal(t *testing.T) {
	for _, name := range []string{"GIT_TERMINAL_PROMPT", "GCM_INTERACTIVE"} {
		t.Setenv(name, "")
		os.Unsetenv(name)
	}

	netCmd := call{args: []string{"fetch"}, net: true}.command(context.Background())
	if !netCmd.SysProcAttr.Setsid || netCmd.SysProcAttr.Setpgid {
		t.Errorf("net call attr = %+v, want Setsid without Setpgid", netCmd.SysProcAttr)
	}

	for _, kv := range []string{"GIT_TERMINAL_PROMPT=0", "GCM_INTERACTIVE=never"} {
		if !slices.Contains(netCmd.Env, kv) {
			t.Errorf("net call lacks %s", kv)
		}
	}

	plain := call{args: []string{"status"}}.command(context.Background())
	if !plain.SysProcAttr.Setpgid || plain.SysProcAttr.Setsid {
		t.Errorf("plain call attr = %+v, want Setpgid without Setsid", plain.SysProcAttr)
	}

	for _, kv := range []string{"GIT_TERMINAL_PROMPT=0", "GCM_INTERACTIVE=never"} {
		if slices.Contains(plain.Env, kv) {
			t.Errorf("plain call has %s", kv)
		}
	}
}

func TestHideURLs(t *testing.T) {
	t.Parallel()

	tests := []struct{ in, want string }{
		{"", ""},
		{"fatal: no url", "fatal: no url"},
		{"fatal: unable to access 'https://user:secret@example.invalid/o/r.git/'", "fatal: unable to access '<url>'"},
		{": ssh://host:22/o/r and git://h/x done", ": <url> and <url> done"},
	}
	for _, tt := range tests {
		if got := hideURLs(tt.in); got != tt.want {
			t.Errorf("hideURLs(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}
