package term

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
	"time"
)

// waitForeground polls Foreground until it reports want or the deadline passes.
func waitForeground(t *testing.T, p *Process, want string) {
	t.Helper()

	deadline := time.Now().Add(testDeadline)

	for {
		got, err := p.Foreground()
		if err == nil && got == want {
			return
		}

		if time.Now().After(deadline) {
			t.Fatalf("Foreground = %q, %v; want %q", got, err, want)
		}

		time.Sleep(20 * time.Millisecond)
	}
}

func startCommand(t *testing.T, c Command) *Process {
	t.Helper()

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	t.Cleanup(cancel)

	p, err := Start(ctx, c, 80, 24)
	if err != nil {
		t.Fatalf("Start: %v", err)
	}

	t.Cleanup(func() { _ = p.Close() })

	return p
}

func TestForeground(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("foreground lookup is tested on Linux")
	}

	// The script is a shebang file, so its comm is the file name.
	script := filepath.Join(t.TempDir(), "claude")
	if err := os.WriteFile(script, []byte("#!/bin/sh\nsleep 30; exit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name string
		argv []string
		want string
	}{
		{"script named claude", []string{script}, "claude"},
		{"plain sh", []string{"/bin/sh"}, "sh"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := startCommand(t, Command{Argv: tt.argv, Dir: t.TempDir(), Env: PaneEnv(nil)})

			waitForeground(t, p, tt.want)
		})
	}
}

// shellTestEnv points every startup and state location of the shells at
// t.TempDir() and puts a fake claude first on PATH. The fake records its
// arguments and AIM_PANE under out.
func shellTestEnv(t *testing.T) (out string) {
	t.Helper()

	root := t.TempDir()
	out = filepath.Join(root, "out")
	bin := filepath.Join(root, "bin")

	for _, d := range []string{out, bin} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}

	fake := "#!/bin/sh\nprintf '%s\\0' \"$@\" > \"$OUT_DIR/args.tmp\"\n" +
		"printf '%s' \"$AIM_PANE\" > \"$OUT_DIR/pane\"\n" +
		"mv \"$OUT_DIR/args.tmp\" \"$OUT_DIR/args\"\n"
	if err := os.WriteFile(filepath.Join(bin, "claude"), []byte(fake), 0o755); err != nil {
		t.Fatal(err)
	}

	for k, v := range map[string]string{
		"HOME":            filepath.Join(root, "home"),
		"ZDOTDIR":         filepath.Join(root, "zdot"),
		"XDG_CONFIG_HOME": filepath.Join(root, "xdg-config"),
		"XDG_DATA_HOME":   filepath.Join(root, "xdg-data"),
		"XDG_CACHE_HOME":  filepath.Join(root, "xdg-cache"),
		"XDG_STATE_HOME":  filepath.Join(root, "xdg-state"),
		"OUT_DIR":         out,
		"AIM_PANE":        "inherited",
		// Ubuntu's /etc/zsh/zshrc runs compinit, which can prompt and block the shell.
		"skip_global_compinit": "1",
	} {
		if strings.HasPrefix(k, "XDG_") || k == "HOME" || k == "ZDOTDIR" {
			if err := os.MkdirAll(v, 0o755); err != nil {
				t.Fatal(err)
			}
		}

		t.Setenv(k, v)
	}

	// An empty .zshrc keeps zsh from starting its interactive first-run setup.
	if err := os.WriteFile(filepath.Join(root, "zdot", ".zshrc"), nil, 0o644); err != nil {
		t.Fatal(err)
	}

	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))

	return out
}

func TestAgentCommandRunsArgvVerbatim(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("shell invocation is tested on Linux")
	}

	hostile := "$(touch pwned)"
	args := []string{"--settings", "/tmp/a b/it's.json", "--resume", hostile, "*", `back\slash "dq"`}

	for _, name := range []string{"sh", "zsh", "fish"} {
		t.Run(name, func(t *testing.T) {
			sh, err := exec.LookPath(name)
			if err != nil {
				t.Skipf("%s not installed", name)
			}

			out := shellTestEnv(t)
			dir := t.TempDir()
			t.Setenv("SHELL", sh)

			// Absolute path: a login profile may reset PATH.
			fake := filepath.Join(filepath.Dir(out), "bin", "claude")

			c := AgentCommand(os.Getenv, Integration{}, dir, append([]string{fake}, args...), []string{"AIM_PANE=p7"})
			startCommand(t, c)

			raw := waitFile(t, filepath.Join(out, "args"))

			got := strings.Split(strings.TrimSuffix(string(raw), "\x00"), "\x00")
			if !slices.Equal(got, args) {
				t.Errorf("args = %q, want %q", got, args)
			}

			if _, err := os.Stat(filepath.Join(dir, "pwned")); err == nil {
				t.Error("session id was executed: pwned exists")
			}

			if pane := waitFile(t, filepath.Join(out, "pane")); string(pane) != "p7" {
				t.Errorf("AIM_PANE = %q, want p7", pane)
			}
		})
	}
}

func TestAgentCommandLeadsForeground(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("shell invocation is tested on Linux")
	}

	for _, name := range []string{"sh", "bash", "zsh", "fish"} {
		t.Run(name, func(t *testing.T) {
			sh, err := exec.LookPath(name)
			if err != nil {
				t.Skipf("%s not installed", name)
			}

			shellTestEnv(t)
			t.Setenv("SHELL", sh)

			fake := filepath.Join(t.TempDir(), "claude")
			if err := os.WriteFile(fake, []byte("#!/bin/sh\nsleep 30\nexit 0\n"), 0o755); err != nil {
				t.Fatal(err)
			}

			p := startCommand(t, AgentCommand(os.Getenv, Integration{}, t.TempDir(), []string{fake}, nil))

			waitForeground(t, p, "claude")
		})
	}
}

func waitFile(t *testing.T, path string) []byte {
	t.Helper()

	deadline := time.Now().Add(5 * time.Second)

	for {
		if b, err := os.ReadFile(path); err == nil {
			return b
		}

		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", path)
		}

		time.Sleep(20 * time.Millisecond)
	}
}
