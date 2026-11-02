package main

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/mhersson/aim/internal/harness"
	"github.com/mhersson/aim/internal/term"
)

// typedRig is a scratch HOME, a fake real claude and the integration with a
// copy of this test binary as the wrapper (TestMain makes a copy named claude
// run as one). It mirrors the rig of internal/term's shell tests.
type typedRig struct {
	home, out, settings string
	ig                  term.Integration
	copy, sockEnv       string
}

func newTypedRig(t *testing.T, startupFiles map[string]string) *typedRig {
	t.Helper()

	tmp := t.TempDir()
	r := &typedRig{home: filepath.Join(tmp, "home"), out: filepath.Join(tmp, "out")}
	runDir := filepath.Join(tmp, "run dir") // a space, as under macOS's Application Support

	for _, d := range []string{r.home, r.out, runDir, filepath.Join(r.home, ".local", "bin"), filepath.Join(tmp, "bin")} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}

	t.Setenv("HOME", r.home)
	t.Setenv("OUT_DIR", r.out)
	// Ubuntu's /etc/zsh/zshrc runs compinit, which can prompt and eat typed input.
	t.Setenv("skip_global_compinit", "1")

	for _, k := range []string{"XDG_CONFIG_HOME", "XDG_DATA_HOME", "XDG_CACHE_HOME", "XDG_STATE_HOME", "XDG_RUNTIME_DIR"} {
		d := filepath.Join(tmp, k)
		if err := os.MkdirAll(d, 0o700); err != nil {
			t.Fatal(err)
		}

		t.Setenv(k, d)
	}

	for _, k := range []string{"ZDOTDIR", "ENV", "CLAUDECODE", "AIM_SHELL_DIR", "AIM_ZDOTDIR", "AIM_ENV"} {
		t.Setenv(k, "")

		if err := os.Unsetenv(k); err != nil {
			t.Fatal(err)
		}
	}

	// The operator's real claude must never be reachable from a rig.
	var keep []string

	for _, d := range filepath.SplitList(os.Getenv("PATH")) {
		if _, err := os.Stat(filepath.Join(d, "claude")); err != nil { //nolint:gosec // test PATH entries
			keep = append(keep, d)
		}
	}

	t.Setenv("PATH", strings.Join(keep, ":"))

	for rel, content := range startupFiles {
		p := filepath.Join(r.home, rel)
		if rel == "fish/config.fish" {
			p = filepath.Join(os.Getenv("XDG_CONFIG_HOME"), rel)
		}

		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil { //nolint:gosec // test temp dir
			t.Fatal(err)
		}

		if err := os.WriteFile(p, []byte(content), 0o644); err != nil { //nolint:gosec // test temp dir
			t.Fatal(err)
		}
	}

	writeTestExec(t, filepath.Join(r.home, ".local", "bin", "claude"), []byte(`#!/bin/sh
{ for a in "$@"; do printf '%s\0' "$a"; done; printf '\0'; } >> "$OUT_DIR/real"
`))

	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}

	bin, err := os.ReadFile(exe)
	if err != nil {
		t.Fatal(err)
	}

	r.copy = filepath.Join(tmp, "bin", "aim")
	writeTestExec(t, r.copy, bin)

	sock := filepath.Join(runDir, strconv.Itoa(os.Getpid())+".sock")
	r.settings, _ = settingsForSock(sock)

	if err := os.WriteFile(r.settings, []byte("{}"), 0o600); err != nil {
		t.Fatal(err)
	}

	r.sockEnv = "AIM_SOCK=" + sock // PaneEnv strips it; the app passes it per pane

	dir := filepath.Join(runDir, strconv.Itoa(os.Getpid())+"-shell")
	if err := term.WriteIntegration(dir, r.copy); err != nil {
		t.Fatal(err)
	}

	r.ig = term.NewIntegration(dir, os.Getenv)

	return r
}

// writeTestExec writes an executable, holding the fork lock against ETXTBSY.
func writeTestExec(t *testing.T, path string, content []byte) {
	t.Helper()

	syscall.ForkLock.RLock()
	defer syscall.ForkLock.RUnlock()

	if err := os.WriteFile(path, content, 0o755); err != nil { //nolint:gosec // an executable by design
		t.Fatal(err)
	}
}

// typedTerm is a started pane whose output is drained into a buffer.
type typedTerm struct {
	p   *term.Process
	mu  sync.Mutex
	buf bytes.Buffer
}

func (r *typedRig) start(t *testing.T, c term.Command) *typedTerm {
	t.Helper()

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)

	p, err := term.Start(ctx, c, 120, 30)
	if err != nil {
		cancel()
		t.Fatalf("Start: %v", err)
	}

	tt := &typedTerm{p: p}
	drained := make(chan struct{})

	go func() {
		defer close(drained)

		b := make([]byte, 4096)

		var carry []byte

		for {
			n, err := p.Read(b)
			if n > 0 {
				// fish waits up to 10 s for this reply; see daQueries in internal/term.
				var replies int

				replies, carry = daQueries(carry, b[:n])
				for range replies {
					_, _ = p.Write([]byte("\x1b[?62;22c"))
				}

				tt.mu.Lock()
				tt.buf.Write(b[:n])
				tt.mu.Unlock()
			}

			if err != nil {
				return
			}
		}
	}()

	t.Cleanup(func() {
		cancel()

		_ = p.Close()

		<-drained

		if t.Failed() {
			t.Logf("terminal output:\n%q", tt.output())
		}
	})

	return tt
}

// daQueries counts the Primary Device Attributes queries in chunk, which may
// continue carry, and returns the bytes to carry on (as in internal/term's rig).
func daQueries(carry, chunk []byte) (int, []byte) {
	data := append(slices.Clone(carry), chunk...)
	n := bytes.Count(data, []byte("\x1b[c")) + bytes.Count(data, []byte("\x1b[0c"))

	i := bytes.LastIndexByte(data, 0x1b)
	if i < 0 || len(data)-i > 3 || bytes.HasSuffix(data, []byte("c")) {
		return n, nil
	}

	return n, slices.Clone(data[i:])
}

func (tt *typedTerm) output() string {
	tt.mu.Lock()
	defer tt.mu.Unlock()

	return tt.buf.String()
}

func (tt *typedTerm) typeLine(t *testing.T, s string) {
	t.Helper()

	if _, err := tt.p.Write([]byte(s + "\r")); err != nil {
		t.Fatalf("type %q: %v", s, err)
	}
}

// realArgs waits for one complete run of the fake real claude, removes its
// record and returns the run's arguments.
func (r *typedRig) realArgs(t *testing.T) []string {
	t.Helper()

	path := filepath.Join(r.out, "real")
	deadline := time.Now().Add(30 * time.Second)

	for {
		b, _ := os.ReadFile(path)
		if s := string(b); strings.HasSuffix(s, "\x00\x00") || s == "\x00" {
			_ = os.Remove(path)

			s = strings.TrimSuffix(s, "\x00\x00")
			if s == "\x00" {
				return nil
			}

			return strings.Split(s, "\x00")
		}

		if time.Now().After(deadline) {
			t.Fatalf("the real claude was not run: %q", b)
		}

		time.Sleep(20 * time.Millisecond)
	}
}

func TestTypedClaudeGetsAIMSettings(t *testing.T) {
	const nativeLine = `export PATH="$HOME/.local/bin:$PATH"` + "\n"

	shells := []struct {
		name  string
		files map[string]string
	}{
		{"zsh", map[string]string{".zshrc": nativeLine}},
		{"bash", map[string]string{".bash_profile": ". ~/.bashrc\n", ".bashrc": nativeLine}},
		{"fish", map[string]string{"fish/config.fish": "fish_add_path --prepend --move $HOME/.local/bin\n"}},
	}

	for _, sh := range shells {
		t.Run(sh.name, func(t *testing.T) {
			path, err := exec.LookPath(sh.name)
			if err != nil {
				t.Skipf("%s is not installed", sh.name)
			}

			// Apple's /bin/bash gets no ENV route (bashReadsENV), so its .bashrc PATH line wins.
			if sh.name == "bash" && runtime.GOOS == "darwin" && path == "/bin/bash" {
				t.Skip("this bash does not read ENV")
			}

			r := newTypedRig(t, sh.files)
			t.Setenv("SHELL", path)

			env := func(k string) string {
				if k == "SHELL" {
					return path
				}

				return os.Getenv(k)
			}

			pane := r.start(t, term.ShellCommand(env, r.ig, r.home, []string{r.sockEnv}))
			pane.typeLine(t, "claude a 'b c'")

			if got, want := r.realArgs(t), []string{"--settings", r.settings, "a", "b c"}; !slices.Equal(got, want) {
				t.Errorf("typed claude ran with %q, want %q", got, want)
			}

			r.start(t, term.AgentCommand(env, r.ig, r.home, harness.Claude{}.Launch(r.settings, ""), []string{r.sockEnv}))

			if got, want := r.realArgs(t), []string{"--settings", r.settings}; !slices.Equal(got, want) {
				t.Errorf("launched claude ran with %q, want exactly one --settings: %q", got, want)
			}

			// The binary removed under a running AIM: claude still runs, unchanged.
			if err := os.Remove(r.copy); err != nil {
				t.Fatal(err)
			}

			pane.typeLine(t, "claude d")

			if got, want := r.realArgs(t), []string{"d"}; !slices.Equal(got, want) {
				t.Errorf("claude after the binary was removed ran with %q, want %q", got, want)
			}

			for _, bad := range []string{"No such file", "not found", "Permission denied", "claude:"} {
				if strings.Contains(pane.output(), bad) {
					t.Errorf("terminal output has %q", bad)
				}
			}
		})
	}
}
