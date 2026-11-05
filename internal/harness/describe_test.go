package harness

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/mhersson/aim/internal/core"
)

// hangGuard bounds a wait that ends only when the test is already failing.
const hangGuard = 30 * time.Second

// oneOffCall is what a fake one-off run was given.
type oneOffCall struct {
	dir   string
	args  []string
	stdin string
}

func TestDescribe(t *testing.T) {
	t.Parallel()

	req := DescribeRequest{
		Dir:    "/work/aim",
		Branch: "feat/lexer",
		Base:   "main",
		Commits: []Commit{
			{Subject: "Add the lexer", Body: "It reads tokens."},
			{Subject: "Fix the parser"},
		},
		Stat:     " lexer.go | 40 ++++\n 1 file changed, 40 insertions(+)",
		Template: "## Why\n\n## Testing\n",
	}
	noTools := []string{"--no-session-persistence", "--tools", "", "--strict-mcp-config"}

	tests := []struct {
		name     string
		session  core.SessionID
		wantArgs []string
	}{
		{
			name:     "a fork of the agent's session",
			session:  testSession,
			wantArgs: append([]string{"--print", "--model", "sonnet", "--resume", string(testSession), "--fork-session"}, noTools...),
		},
		{
			name:     "a fresh run without a session",
			wantArgs: append([]string{"--print", "--model", "sonnet"}, noTools...),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			var got oneOffCall

			c := Claude{oneOff: func(_ context.Context, dir string, args []string, stdin string) (string, error) {
				got = oneOffCall{dir: dir, args: args, stdin: stdin}

				return "\n## Why\n\nTokens.\n\n", nil
			}}

			r := req
			r.Session = tt.session

			body, err := c.Describe(context.Background(), r)
			if err != nil {
				t.Fatal(err)
			}

			if body != "## Why\n\nTokens." {
				t.Errorf("body = %q, want the output without its surrounding blank lines", body)
			}

			if got.dir != req.Dir {
				t.Errorf("ran in %q, want %q", got.dir, req.Dir)
			}

			if !slices.Equal(got.args, tt.wantArgs) {
				t.Errorf("args = %q, want %q", got.args, tt.wantArgs)
			}

			for _, want := range []string{req.Branch, req.Base, "Add the lexer", "It reads tokens.", "Fix the parser", req.Stat, req.Template} {
				if !strings.Contains(got.stdin, want) {
					t.Errorf("the prompt lacks %q:\n%s", want, got.stdin)
				}
			}
		})
	}
}

// writeExecutable writes an executable script; a concurrent fork could inherit
// the write descriptor and make the exec fail with ETXTBSY (go.dev/issue/22315).
func writeExecutable(t *testing.T, path, body string) {
	t.Helper()

	syscall.ForkLock.RLock()

	err := os.WriteFile(path, []byte(body), 0o755)

	syscall.ForkLock.RUnlock()

	if err != nil {
		t.Fatal(err)
	}
}

// fakeClaude writes a claude script into a new directory and returns the
// directory. The tests' PATH holds no system directory, so the script names
// its tools by path.
func fakeClaude(t *testing.T, body string) string {
	t.Helper()

	dir := t.TempDir()
	writeExecutable(t, filepath.Join(dir, "claude"), "#!/bin/sh\n"+body)

	return dir
}

func TestRunOneOff(t *testing.T) {
	t.Parallel()

	echo := fakeClaude(t, "pwd\nprintf '[%s]\\n' \"$@\"\n/bin/cat\n")
	failing := fakeClaude(t, "echo partial\necho 'Warning: noise' >&2\necho 'No conversation found' >&2\nexit 1\n")
	wrapper := fakeClaude(t, "echo wrapper\n")
	empty := t.TempDir()

	self, err := os.Stat(filepath.Join(wrapper, "claude"))
	if err != nil {
		t.Fatal(err)
	}

	work, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name    string
		path    []string
		self    os.FileInfo
		want    string
		wantErr string
		notFind bool
	}{
		{name: "runs the claude on PATH in dir", path: []string{empty, echo}, want: work + "\n[--print]\n[]\nthe prompt"},
		{name: "skips AIM's own binary", path: []string{wrapper, echo}, self: self, want: work + "\n[--print]\n[]\nthe prompt"},
		{name: "a failure carries the last line of stderr", path: []string{failing}, wantErr: "No conversation found"},
		{name: "no claude on PATH", path: []string{empty, wrapper}, self: self, notFind: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			env := []string{"PATH=" + strings.Join(tt.path, string(filepath.ListSeparator))}

			out, err := runOneOff(context.Background(), env, tt.self, work, []string{"--print", ""}, "the prompt")

			switch {
			case tt.notFind:
				if !errors.Is(err, exec.ErrNotFound) {
					t.Errorf("err = %v, want exec.ErrNotFound", err)
				}
			case tt.wantErr != "":
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) || strings.Contains(err.Error(), "noise") {
					t.Errorf("err = %v, want one carrying %q and no earlier line", err, tt.wantErr)
				}
			case err != nil:
				t.Fatal(err)
			case out != tt.want:
				t.Errorf("out = %q, want %q", out, tt.want)
			}
		})
	}
}

// A run that exits cleanly while a child it left holds its stdout keeps its
// output, and the child is stopped.
func TestRunOneOffKeepsTheOutputOfACleanExit(t *testing.T) {
	t.Parallel()

	pidFile := filepath.Join(t.TempDir(), "pid")
	dir := fakeClaude(t, "echo body\n/bin/sleep 300 &\necho $! > '"+pidFile+"'\n")

	out, err := runOneOff(context.Background(), []string{"PATH=" + dir}, nil, t.TempDir(), nil, "")
	if err != nil || out != "body\n" {
		t.Errorf("out = %q, err = %v, want the output and no error", out, err)
	}

	b, err := os.ReadFile(pidFile)
	if err != nil {
		t.Fatal(err)
	}

	pid, err := strconv.Atoi(strings.TrimSpace(string(b)))
	if err != nil {
		t.Fatal(err)
	}

	// The child may linger until it is reaped, so wait for it to go.
	for deadline := time.Now().Add(hangGuard); syscall.Kill(pid, 0) == nil; {
		if time.Now().After(deadline) {
			_ = syscall.Kill(pid, syscall.SIGKILL)

			t.Fatal("the child outlived the run")
		}

		time.Sleep(time.Millisecond)
	}
}

// A run that outlives its context is stopped with its whole process group.
func TestRunOneOffKillsTheGroup(t *testing.T) {
	t.Parallel()

	pidFile := filepath.Join(t.TempDir(), "pid")
	dir := fakeClaude(t, "/bin/sleep 300 &\necho $! > '"+pidFile+"'\nwait\n")

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)

	go func() {
		_, err := runOneOff(ctx, []string{"PATH=" + dir}, nil, t.TempDir(), nil, "")
		done <- err
	}()

	var pid int

	for deadline := time.Now().Add(hangGuard); pid == 0; {
		if time.Now().After(deadline) {
			t.Fatal("the fake claude never started its child")
		}

		if b, err := os.ReadFile(pidFile); err == nil && strings.HasSuffix(string(b), "\n") {
			pid, _ = strconv.Atoi(strings.TrimSpace(string(b)))
		}

		time.Sleep(time.Millisecond)
	}

	cancel()

	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Errorf("err = %v, want context.Canceled", err)
		}
	case <-time.After(hangGuard):
		t.Fatal("the run did not end with its context")
	}

	// The child may linger until it is reaped, so wait for it to go.
	for deadline := time.Now().Add(hangGuard); syscall.Kill(pid, 0) == nil; {
		if time.Now().After(deadline) {
			t.Fatal("the child outlived the run")
		}

		time.Sleep(time.Millisecond)
	}
}
