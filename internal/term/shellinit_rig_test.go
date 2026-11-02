package term

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"
)

// rigSettings is the --settings value the stand-in wrapper adds.
const rigSettings = "/rig/settings.json"

// shellRig is a temporary HOME, a fake claude and a stand-in wrapper for the
// shell integration tests. Home is the temporary HOME, Out the dir the fakes
// record into ($OUT_DIR), RunDir the parent of the integration dir.
type shellRig struct {
	Home, Out, RunDir string
	wrapper           string
}

// newShellRig points HOME, every XDG_* dir and OUT_DIR at temporary dirs, unsets
// ZDOTDIR and ENV, drops any dir holding a claude from PATH, and installs
// $HOME/.local/bin/claude (a fake recording to "real"). It uses t.Setenv, so the
// test is serial. The integration dir sits under a path with a space.
func newShellRig(t *testing.T) *shellRig {
	t.Helper()

	tmp := t.TempDir()
	r := &shellRig{Home: filepath.Join(tmp, "home"), Out: filepath.Join(tmp, "out"), RunDir: filepath.Join(tmp, "run dir")}

	for _, d := range []string{r.Home, r.Out, filepath.Join(r.Home, ".local", "bin")} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}

	t.Setenv("HOME", r.Home)
	t.Setenv("OUT_DIR", r.Out)
	// Ubuntu's /etc/zsh/zshrc runs compinit, which can prompt and eat typed input.
	t.Setenv("skip_global_compinit", "1")

	for _, k := range []string{"XDG_CONFIG_HOME", "XDG_DATA_HOME", "XDG_CACHE_HOME", "XDG_STATE_HOME", "XDG_RUNTIME_DIR"} {
		d := filepath.Join(tmp, k)
		if err := os.MkdirAll(d, 0o700); err != nil {
			t.Fatal(err)
		}

		t.Setenv(k, d)
	}

	for _, k := range []string{"ZDOTDIR", "ENV", "AIM_SHELL_DIR", "AIM_ZDOTDIR", "AIM_ENV"} {
		unsetenv(t, k)
	}

	// The operator's real claude must never be reachable from a rig.
	var keep []string

	for _, d := range filepath.SplitList(os.Getenv("PATH")) {
		if _, err := os.Stat(filepath.Join(d, "claude")); err != nil { //nolint:gosec // test PATH entries
			keep = append(keep, d)
		}
	}

	t.Setenv("PATH", strings.Join(keep, ":"))

	r.fakeClaude(t, filepath.Join(r.Home, ".local", "bin"), "real")

	r.wrapper = filepath.Join(tmp, "wrapper.sh")
	writeExec(t, r.wrapper, `#!/bin/sh
{ for a in "$@"; do printf '%s\0' "$a"; done; printf '\0'; } >> "$OUT_DIR/wrap"
set -f
IFS=:
for d in $PATH; do
  [ "$d" = "$AIM_SHELL_DIR/bin" ] && continue
  if [ -x "$d/claude" ]; then exec "$d/claude" --settings `+rigSettings+` "$@"; fi
done
exit 127
`)

	return r
}

// unsetenv unsets k for the rest of the test and restores it afterwards.
func unsetenv(t *testing.T, k string) {
	t.Helper()

	t.Setenv(k, "")

	if err := os.Unsetenv(k); err != nil {
		t.Fatal(err)
	}
}

// writeExec writes an executable file, holding the fork lock against ETXTBSY.
func writeExec(t *testing.T, path, content string) {
	t.Helper()

	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}

	syscall.ForkLock.RLock()
	defer syscall.ForkLock.RUnlock()

	if err := os.WriteFile(path, []byte(content), 0o755); err != nil {
		t.Fatal(err)
	}
}

// fakeClaude installs dir/claude, which records its arguments to $OUT_DIR/name.
func (r *shellRig) fakeClaude(t *testing.T, dir, name string) {
	t.Helper()

	writeExec(t, filepath.Join(dir, "claude"), `#!/bin/sh
{ for a in "$@"; do printf '%s\0' "$a"; done; printf '\0'; } >> "$OUT_DIR/`+name+`"
`)
}

// write puts content in the file rel under Home, making its dirs.
func (r *shellRig) write(t *testing.T, rel, content string) {
	t.Helper()

	p := filepath.Join(r.Home, rel)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}

	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// record is the startup-file line that appends name to $OUT_DIR/order and the
// ZDOTDIR the file sees to $OUT_DIR/zdot. It is valid in zsh, bash and sh.
func (r *shellRig) record(name string) string {
	return "printf '%s\\n' " + name + ` >> "$OUT_DIR/order"; printf '%s=%s\n' ` + name + ` "${ZDOTDIR-unset}" >> "$OUT_DIR/zdot"` + "\n"
}

// integrate writes the integration under RunDir/1-shell, with the stand-in
// wrapper as bin/claude, and returns NewIntegration for the current environment.
// Call it after the startup files exist, since NewIntegration looks at them.
func (r *shellRig) integrate(t *testing.T) Integration {
	t.Helper()

	dir := filepath.Join(r.RunDir, "1-shell")
	if err := WriteIntegration(dir, r.wrapper); err != nil {
		t.Fatal(err)
	}

	return NewIntegration(dir, os.Getenv)
}

// rigTerm is a started Command whose output is drained into a buffer.
type rigTerm struct {
	p    *Process
	mu   sync.Mutex
	buf  bytes.Buffer
	done chan struct{}
}

// start runs c on a pty and drains its output; the test closes it on cleanup
// and logs the output when it failed.
func (r *shellRig) start(t *testing.T, c Command) *rigTerm {
	t.Helper()

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)

	p, err := Start(ctx, c, 120, 30)
	if err != nil {
		cancel()
		t.Fatalf("Start: %v", err)
	}

	rt := &rigTerm{p: p, done: make(chan struct{})}

	go func() {
		defer close(rt.done)

		b := make([]byte, 4096)

		var carry []byte

		for {
			n, err := p.Read(b)
			if n > 0 {
				var replies int

				replies, carry = daQueries(carry, b[:n])
				for range replies {
					_, _ = p.Write([]byte(daReply))
				}

				rt.mu.Lock()
				rt.buf.Write(b[:n])
				rt.mu.Unlock()
			}

			if err != nil {
				return
			}
		}
	}()

	t.Cleanup(func() {
		cancel()

		_ = p.Close()

		<-rt.done

		if t.Failed() {
			t.Logf("terminal output:\n%q", rt.output())
		}
	})

	return rt
}

// daReply answers a Primary Device Attributes query.
const daReply = "\x1b[?62;22c"

// daQueries counts the Primary Device Attributes queries (ESC [ c, ESC [ 0 c)
// in chunk, which may continue carry, and returns the bytes to carry into the
// next chunk. fish waits up to 10 s for the reply before it starts, so a rig
// that never answers makes every fish test slow; zsh and bash never ask.
func daQueries(carry, chunk []byte) (int, []byte) {
	data := append(slices.Clone(carry), chunk...)
	n := bytes.Count(data, []byte("\x1b[c")) + bytes.Count(data, []byte("\x1b[0c"))

	// Keep an unfinished query only; a finished one must not count twice.
	i := bytes.LastIndexByte(data, 0x1b)
	if i < 0 || len(data)-i > 3 || bytes.HasSuffix(data, []byte("c")) {
		return n, nil
	}

	return n, slices.Clone(data[i:])
}

// typeLine types s and Enter.
func (rt *rigTerm) typeLine(t *testing.T, s string) {
	t.Helper()

	if _, err := rt.p.Write([]byte(s + "\r")); err != nil {
		t.Fatalf("type %q: %v", s, err)
	}
}

// output is what the terminal has printed so far.
func (rt *rigTerm) output() string {
	rt.mu.Lock()
	defer rt.mu.Unlock()

	return rt.buf.String()
}

// await waits for the file name in Out and returns its content.
func (r *shellRig) await(t *testing.T, name string) string {
	t.Helper()

	deadline := time.Now().Add(30 * time.Second)

	for {
		b, err := os.ReadFile(filepath.Join(r.Out, name))
		if err == nil && len(b) > 0 {
			return string(b)
		}

		if time.Now().After(deadline) {
			t.Fatalf("%s never appeared in %s", name, r.Out)
		}

		time.Sleep(20 * time.Millisecond)
	}
}

// records waits until the fake that records to name was run n times, returns
// each run's arguments and removes the file.
func (r *shellRig) records(t *testing.T, name string, n int) [][]string {
	t.Helper()

	deadline := time.Now().Add(30 * time.Second)

	for {
		b, _ := os.ReadFile(filepath.Join(r.Out, name))

		var (
			out [][]string
			cur []string
		)

		for _, f := range strings.SplitAfter(string(b), "\x00") {
			switch f {
			case "":
			case "\x00":
				out, cur = append(out, cur), nil
			default:
				cur = append(cur, strings.TrimSuffix(f, "\x00"))
			}
		}

		if len(out) >= n {
			_ = os.Remove(filepath.Join(r.Out, name))

			return out
		}

		if time.Now().After(deadline) {
			t.Fatalf("%s has %d of %d runs: %q", name, len(out), n, b)
		}

		time.Sleep(20 * time.Millisecond)
	}
}

// args is the arguments of the single run recorded to name.
func (r *shellRig) args(t *testing.T, name string) []string {
	t.Helper()

	return r.records(t, name, 1)[0]
}

// lines is the lines of the file name in Out, none when it is missing.
func (r *shellRig) lines(name string) []string {
	b, err := os.ReadFile(filepath.Join(r.Out, name))
	if err != nil {
		return nil
	}

	return strings.Fields(string(b))
}
