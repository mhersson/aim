//go:build unix

package harness

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"syscall"
	"time"

	"golang.org/x/sys/unix"
)

// oneOffWaitDelay bounds the wait for the run's output once its group is killed.
const oneOffWaitDelay = time.Second

// runOneOff runs the program that the PATH in env names, skipping self, in dir
// with stdin, and returns its stdout. It starts in a new session, which has no
// controlling terminal, so the agent never reaches AIM's; ending ctx kills the
// session's process group.
func runOneOff(ctx context.Context, program string, env []string, self os.FileInfo, dir string, args []string, stdin string) (string, error) {
	path, err := findProgram(program, pathOf(env), self)
	if err != nil {
		return "", err
	}

	cmd := exec.CommandContext(ctx, path, args...)
	cmd.Dir, cmd.Env, cmd.Stdin = dir, env, strings.NewReader(stdin)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	cmd.Cancel = func() error { return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL) }
	cmd.WaitDelay = oneOffWaitDelay

	out, err := cmd.Output()

	var exit *exec.ExitError

	switch {
	case err == nil:
		return string(out), nil
	case errors.Is(err, exec.ErrWaitDelay):
		// The agent exited 0 but something it left holds stdout; that holder is
		// alive, so the group id is still its own.
		_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)

		return string(out), nil
	case ctx.Err() != nil:
		return "", fmt.Errorf("%s: %w", program, ctx.Err())
	case errors.As(err, &exit):
		if line := lastLine(string(exit.Stderr)); line != "" {
			return "", fmt.Errorf("%s failed (%w): %s", program, err, line)
		}

		return "", fmt.Errorf("%s failed (%w)", program, err)
	default:
		return "", fmt.Errorf("run %s: %w", program, err)
	}
}

// findProgram returns the first matching executable in an absolute directory of
// pathEnv that is not self; a relative entry would run whatever the cwd holds.
func findProgram(program, pathEnv string, self os.FileInfo) (string, error) {
	for _, dir := range filepath.SplitList(pathEnv) {
		if !filepath.IsAbs(dir) {
			continue
		}

		candidate := filepath.Join(dir, program)

		info, err := os.Stat(candidate)
		if err != nil || info.IsDir() || unix.Access(candidate, unix.X_OK) != nil {
			continue
		}

		if self != nil && os.SameFile(info, self) {
			continue
		}

		return candidate, nil
	}

	return "", fmt.Errorf("no %s on PATH: %w", program, exec.ErrNotFound)
}

// pathOf is the last PATH in env, the one exec passes on.
func pathOf(env []string) string {
	for _, e := range slices.Backward(env) {
		if v, ok := strings.CutPrefix(e, "PATH="); ok {
			return v
		}
	}

	return ""
}

// lastLine is the last non-blank line of s, printable and cut short.
func lastLine(s string) string {
	lines := strings.Split(strings.TrimSpace(s), "\n")

	return clip(strings.TrimSpace(lines[len(lines)-1]))
}
