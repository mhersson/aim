package git

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

const (
	waitDelay   = 2 * time.Second
	stderrLimit = 64 << 10
)

// errLimit reports that git wrote more than call.limit bytes and was killed.
var errLimit = errors.New("git: output over limit")

// gitPathKey is a context key whose value is the path of a git executable.
// Only tests set it, to wrap git without changing PATH.
type gitPathKey struct{}

// gitPath is the git executable ctx asks for, else git on PATH.
func gitPath(ctx context.Context) string {
	if path, ok := ctx.Value(gitPathKey{}).(string); ok {
		return path
	}

	return "git"
}

// call describes one git invocation. All git in this package goes through run
// or stream.
type call struct {
	dir   string
	args  []string
	env   []string // added after the cleaned environment, e.g. GIT_INDEX_FILE
	stdin io.Reader
	read  bool  // adds GIT_OPTIONAL_LOCKS=0
	limit int64 // stdout bytes kept; 0 = unlimited
	net   bool  // network command: new session, no terminal prompts, URLs hidden in errors
}

// run executes git and returns its stdout. A non-zero exit carries the first
// line of stderr in the error and also returns the stdout written so far.
func run(ctx context.Context, c call) ([]byte, error) {
	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	cmd := c.command(runCtx)
	out := &cappedBuffer{limit: c.limit, onFull: cancel}
	stderr := &cappedBuffer{limit: stderrLimit}
	cmd.Stdout = out
	cmd.Stderr = stderr

	err := cmd.Run()

	switch {
	case out.full:
		return nil, fmt.Errorf("git %s: %w", c.name(), errLimit)
	case ctx.Err() != nil:
		return nil, fmt.Errorf("git %s: %w", c.name(), ctx.Err())
	case err != nil:
		return out.Bytes(), fmt.Errorf("git %s: %w%s", c.name(), err, c.stderrSuffix(stderr.Bytes()))
	}

	return out.Bytes(), nil
}

// stream starts git and returns its stdout. Close kills the process group
// unless stdout was read to the end, waits for it and reports its exit.
func stream(ctx context.Context, c call) (io.ReadCloser, error) {
	runCtx, cancel := context.WithCancel(ctx)
	cmd := c.command(runCtx)
	stderr := &cappedBuffer{limit: stderrLimit}
	cmd.Stderr = stderr

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		cancel()

		return nil, fmt.Errorf("git %s: %w", c.name(), err)
	}

	if err := cmd.Start(); err != nil {
		cancel()

		return nil, fmt.Errorf("git %s: %w", c.name(), err)
	}

	return &streamReader{r: stdout, cmd: cmd, cancel: cancel, stderr: stderr, name: c.name()}, nil
}

func (c call) name() string {
	for _, a := range c.args {
		if !strings.HasPrefix(a, "-") {
			return a
		}
	}

	return "command"
}

// stderrSuffix is firstLine of stderr. A network call also has every URL in it
// replaced by "<url>", since a remote URL may carry credentials.
func (c call) stderrSuffix(stderr []byte) string {
	if c.net {
		return hideURLs(firstLine(stderr))
	}

	return firstLine(stderr)
}

// hideURLs replaces each <scheme>://... token, up to whitespace or a quote.
func hideURLs(s string) string {
	var b strings.Builder

	for {
		i := strings.Index(s, "://")
		if i < 0 {
			b.WriteString(s)

			return b.String()
		}

		start := i
		for start > 0 && isSchemeByte(s[start-1]) {
			start--
		}

		end := i + len("://")
		for end < len(s) && !strings.ContainsRune(" \t\r\n'\"", rune(s[end])) {
			end++
		}

		b.WriteString(s[:start])
		b.WriteString("<url>")

		s = s[end:]
	}
}

func isSchemeByte(c byte) bool {
	return c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '+' || c == '-' || c == '.'
}

// command builds the process: config as -c arguments before the subcommand,
// the cleaned environment, its own process group (a network call: its own
// session, so no controlling terminal) and a bounded wait.
func (c call) command(ctx context.Context) *exec.Cmd {
	args := append([]string{"-c", "core.fsmonitor=false", "-c", "core.quotePath=false"}, c.args...)
	cmd := exec.CommandContext(ctx, gitPath(ctx), args...)
	cmd.Dir = c.dir
	cmd.Stdin = c.stdin
	cmd.Env = append(cleanEnv(os.Environ()), c.env...)

	if c.read {
		cmd.Env = append(cmd.Env, "GIT_OPTIONAL_LOCKS=0")
	}

	if c.net {
		cmd.Env = append(cmd.Env, "GIT_TERMINAL_PROMPT=0", "GCM_INTERACTIVE=never")
		setSession(cmd)
	} else {
		setProcessGroup(cmd)
	}

	// A read takes no locks (GIT_OPTIONAL_LOCKS), so it skips SIGTERM's grace.
	stop := stopProcessGroup
	if c.read {
		stop = killProcessGroup
	}

	cmd.Cancel = func() error { return stop(cmd.Process) }
	cmd.WaitDelay = waitDelay

	return cmd
}

// cleanEnv drops git's repository-local variables from inherited, so a call
// reads only the repository it was pointed at. The list is from
// `git rev-parse --local-env-vars` (git 2.56); GIT_CONFIG_COUNT also removes its
// KEY_<n> and VALUE_<n> pairs. GIT_DIFF_OPTS goes too: it would override -U3.
func cleanEnv(inherited []string) []string {
	env := make([]string, 0, len(inherited))

	for _, kv := range inherited {
		name, _, _ := strings.Cut(kv, "=")
		if !repoLocalVar(name) {
			env = append(env, kv)
		}
	}

	return env
}

func repoLocalVar(name string) bool {
	switch name {
	case "GIT_ALTERNATE_OBJECT_DIRECTORIES", "GIT_CONFIG", "GIT_CONFIG_PARAMETERS",
		"GIT_CONFIG_COUNT", "GIT_OBJECT_DIRECTORY", "GIT_DIR", "GIT_WORK_TREE",
		"GIT_IMPLICIT_WORK_TREE", "GIT_GRAFT_FILE", "GIT_INDEX_FILE",
		"GIT_NO_REPLACE_OBJECTS", "GIT_REPLACE_REF_BASE", "GIT_PREFIX",
		"GIT_SHALLOW_FILE", "GIT_COMMON_DIR", "GIT_OPTIONAL_LOCKS":
		return true
	}

	return name == "GIT_DIFF_OPTS" || strings.HasPrefix(name, "GIT_CONFIG_KEY_") || strings.HasPrefix(name, "GIT_CONFIG_VALUE_")
}

// firstLine formats stderr's first line as a ": ..." suffix, or "" when empty.
func firstLine(b []byte) string {
	line, _, _ := strings.Cut(strings.TrimSpace(string(b)), "\n")
	if line == "" {
		return ""
	}

	return ": " + strings.TrimSpace(line)
}

// cappedBuffer keeps at most limit bytes (0 = unlimited). Past the limit it
// drops the rest, sets full and calls onFull once. It does not embed
// bytes.Buffer, whose ReadFrom would bypass Write.
type cappedBuffer struct {
	buf    bytes.Buffer
	limit  int64
	onFull func()
	full   bool
}

func (b *cappedBuffer) Write(p []byte) (int, error) {
	if b.full {
		return len(p), nil
	}

	if b.limit > 0 && int64(b.buf.Len()+len(p)) > b.limit {
		b.buf.Write(p[:b.limit-int64(b.buf.Len())])
		b.full = true

		if b.onFull != nil {
			b.onFull()
		}

		return len(p), nil
	}

	return b.buf.Write(p)
}

func (b *cappedBuffer) Bytes() []byte { return b.buf.Bytes() }

type streamReader struct {
	r      io.Reader
	cmd    *exec.Cmd
	cancel context.CancelFunc
	stderr *cappedBuffer
	name   string

	eof  atomic.Bool
	once sync.Once
	err  error
}

func (s *streamReader) Read(p []byte) (int, error) {
	n, err := s.r.Read(p)
	if errors.Is(err, io.EOF) {
		s.eof.Store(true)
	}

	return n, err
}

func (s *streamReader) Close() error {
	s.once.Do(func() {
		if !s.eof.Load() {
			s.cancel()
		}

		werr := s.cmd.Wait()
		s.cancel()

		if s.eof.Load() && werr != nil {
			s.err = fmt.Errorf("git %s: %w%s", s.name, werr, firstLine(s.stderr.Bytes()))
		}
	})

	return s.err
}
