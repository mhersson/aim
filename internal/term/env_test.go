package term

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

func TestPaneEnv(t *testing.T) {
	tests := []struct {
		name string
		base []string
		want []string
	}{
		{"empty", nil, []string{"TERM=xterm-256color", "COLORTERM=truecolor"}},
		{
			"strips and keeps",
			[]string{"TERM=dumb", "HOME=/h", "TMUX=/t,1,0", "COLORTERM=24bit", "TMUX_PANE=%1", "PATH=/bin"},
			[]string{"HOME=/h", "PATH=/bin", "TERM=xterm-256color", "COLORTERM=truecolor"},
		},
		{
			"duplicates collapse",
			[]string{"TERM=a", "X=1", "TERM=b"},
			[]string{"X=1", "TERM=xterm-256color", "COLORTERM=truecolor"},
		},
		{
			"pane identity dropped",
			[]string{"AIM_PANE=p1", "AIM_SOCK=/s", "AIM_OTHER=1"},
			[]string{"AIM_OTHER=1", "TERM=xterm-256color", "COLORTERM=truecolor"},
		},
		{
			"integration variables dropped",
			[]string{"AIM_SHELL_DIR=/d", "AIM_ZDOTDIR=/z", "AIM_ENV=/e", "AIM_KEEP=1"},
			[]string{"AIM_KEEP=1", "TERM=xterm-256color", "COLORTERM=truecolor"},
		},
		{
			"similar names kept",
			[]string{"TERMINFO=/x", "TMUX_FOO=1"},
			[]string{"TERMINFO=/x", "TMUX_FOO=1", "TERM=xterm-256color", "COLORTERM=truecolor"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := PaneEnv(tt.base); !slices.Equal(got, tt.want) {
				t.Errorf("PaneEnv = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestCommands(t *testing.T) {
	lookup := func(shell string) func(string) string {
		return func(k string) string {
			if k == "SHELL" {
				return shell
			}

			return ""
		}
	}

	tests := []struct {
		name  string
		shell string
		shArg []string
		agent []string
	}{
		{
			"fish", "/usr/bin/fish",
			[]string{"/usr/bin/fish", "-l"},
			[]string{"/usr/bin/fish", "-l", "-i", "-c", `status job-control full; $argv; exec "$SHELL" -l`, "claude"},
		},
		{
			"fallback", "",
			[]string{"/bin/sh", "-l"},
			[]string{"/bin/sh", "-l", "-i", "-c", `"$@"; exec "$SHELL" -l`, "sh", "claude"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			sh := ShellCommand(lookup(tt.shell), Integration{}, "/work", nil)
			if !slices.Equal(sh.Argv, tt.shArg) || sh.Dir != "/work" {
				t.Errorf("ShellCommand = %+v", sh)
			}

			ag := AgentCommand(lookup(tt.shell), Integration{}, "/work", []string{"claude"}, nil)
			if !slices.Equal(ag.Argv, tt.agent) || ag.Dir != "/work" {
				t.Errorf("AgentCommand = %+v", ag)
			}

			wantShell := tt.shArg[0]

			for _, c := range []Command{sh, ag} {
				var shells []string

				for _, kv := range c.Env {
					if strings.HasPrefix(kv, "SHELL=") {
						shells = append(shells, kv)
					}
				}

				if !slices.Equal(shells, []string{"SHELL=" + wantShell}) {
					t.Errorf("SHELL entries = %q, want only SHELL=%s", shells, wantShell)
				}

				if !slices.Contains(c.Env, "TERM=xterm-256color") {
					t.Errorf("Env lacks TERM: %q", c.Env)
				}
			}
		})
	}
}

func TestCommandsExtraEnv(t *testing.T) {
	t.Setenv("AIM_PANE", "inherited")

	env := func(string) string { return "/bin/sh" }
	extra := []string{"AIM_PANE=p2", "AIM_SOCK=/run/1.sock"}

	for name, c := range map[string]Command{
		"shell": ShellCommand(env, Integration{}, "/work", extra),
		"agent": AgentCommand(env, Integration{}, "/work", []string{"claude"}, extra),
	} {
		var panes []string

		for _, kv := range c.Env {
			if strings.HasPrefix(kv, "AIM_PANE=") {
				panes = append(panes, kv)
			}
		}

		if !slices.Equal(panes, []string{"AIM_PANE=p2"}) {
			t.Errorf("%s: AIM_PANE entries = %q, want only p2", name, panes)
		}

		if !slices.Contains(c.Env, "AIM_SOCK=/run/1.sock") {
			t.Errorf("%s: Env lacks extra entry: %q", name, c.Env)
		}
	}
}

func TestAgentCommandArgumentsAreNotInTheScript(t *testing.T) {
	env := func(string) string { return "/bin/sh" }
	argv := []string{"claude", "--settings", "/tmp/a b/it's.json", "$(x)"}
	c := AgentCommand(env, Integration{}, "/work", argv, nil)

	want := append([]string{"/bin/sh", "-l", "-i", "-c", `"$@"; exec "$SHELL" -l`, "sh"}, argv...)
	if !slices.Equal(c.Argv, want) {
		t.Errorf("Argv = %q, want %q", c.Argv, want)
	}
}

func TestEditorCommand(t *testing.T) {
	sh := func(string) string { return "/bin/sh" }
	fish := func(string) string { return "/usr/bin/fish" }

	const tail = `; exec "$SHELL" -l`

	tests := []struct {
		name   string
		env    func(string) string
		editor string
		args   []string
		want   []string // the arguments after -l -i -c
	}{
		{"a plain editor", sh, "vi", []string{"+7", "a.go"}, []string{`vi "$@"` + tail, "sh", "+7", "a.go"}},
		{"an editor with arguments is read by the shell", sh, "code -w", []string{"+7", "a.go"}, []string{`code -w "$@"` + tail, "sh", "+7", "a.go"}},
		{"a quote and a space stay one argument", sh, "vi", []string{"+7", "it's a file.go"}, []string{`vi "$@"` + tail, "sh", "+7", "it's a file.go"}},
		{"shell syntax in an argument is not in the script", sh, "vi", []string{"$(x)", "a;b"}, []string{`vi "$@"` + tail, "sh", "$(x)", "a;b"}},
		{"fish gets job control first and reads argv", fish, "vi", []string{"a.go"}, []string{`status job-control full; vi $argv` + tail, "a.go"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := EditorCommand(tt.env, Integration{}, "/work", tt.editor, tt.args, []string{"AIM_PANE=p2"})

			wantSh := tt.env("SHELL")
			if want := append([]string{wantSh, "-l", "-i", "-c"}, tt.want...); !slices.Equal(c.Argv, want) {
				t.Errorf("Argv = %q, want %q", c.Argv, want)
			}

			if c.Dir != "/work" {
				t.Errorf("Dir = %q, want /work", c.Dir)
			}

			if !slices.Contains(c.Env, "SHELL="+wantSh) || !slices.Contains(c.Env, "AIM_PANE=p2") {
				t.Errorf("Env lacks SHELL or the extra entry: %q", c.Env)
			}
		})
	}
}

func TestEditorOnlyCommand(t *testing.T) {
	tests := []struct {
		name string
		sh   string
		want []string // the arguments after -l -i -c
	}{
		{"sh", "/bin/sh", []string{`vi "$@"`, "sh", "/run/body.md"}},
		{"bash", "/usr/bin/bash", []string{`vi "$@"`, "bash", "/run/body.md"}},
		{"zsh", "/usr/bin/zsh", []string{`vi "$@"`, "zsh", "/run/body.md"}},
		{"fish", "/usr/bin/fish", []string{`status job-control full; vi $argv`, "/run/body.md"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := EditorOnlyCommand(func(string) string { return tt.sh }, Integration{}, "/work", "vi", []string{"/run/body.md"}, nil)

			if want := append([]string{tt.sh, "-l", "-i", "-c"}, tt.want...); !slices.Equal(c.Argv, want) {
				t.Errorf("Argv = %q, want %q", c.Argv, want)
			}

			if strings.Contains(strings.Join(c.Argv, " "), "exec") {
				t.Errorf("Argv = %q, want no trailing shell", c.Argv)
			}
		})
	}
}

// TestAgentCommandPinned pins, byte for byte, the Command AgentCommand returns.
// It runs nothing, so unlike the tests that start a shell it holds on every platform.
func TestAgentCommandPinned(t *testing.T) {
	t.Setenv("SHELL", "/inherited/shell")
	t.Setenv("AIM_PANE", "p9")
	t.Setenv("AIM_SOCK", "/inherited.sock")
	t.Setenv("TERM", "dumb")
	t.Setenv("COLORTERM", "16color")

	const tail = `; exec "$SHELL" -l`

	argv := []string{"claude", "--settings", "/tmp/a b/it's.json", "$(x)"}
	posix := `"$@"` + tail
	fishScript := `status job-control full; $argv` + tail

	tests := []struct {
		name  string
		shell string // what $SHELL says; "" for unset
		argv  []string
		sh    string   // the shell that runs
		want  []string // the arguments after -l -i -c
	}{
		{"sh", "/bin/sh", argv, "/bin/sh", append([]string{posix, "sh"}, argv...)},
		{"bash", "/bin/bash", argv, "/bin/bash", append([]string{posix, "bash"}, argv...)},
		{"zsh", "/bin/zsh", argv, "/bin/zsh", append([]string{posix, "zsh"}, argv...)},
		{"fish gets job control first", "/usr/bin/fish", argv, "/usr/bin/fish", append([]string{fishScript}, argv...)},
		{"fish at another path", "/opt/homebrew/bin/fish", argv, "/opt/homebrew/bin/fish", append([]string{fishScript}, argv...)},
		{"an unset SHELL runs sh", "", argv, "/bin/sh", append([]string{posix, "sh"}, argv...)},
		{"one word", "/bin/zsh", []string{"claude"}, "/bin/zsh", []string{posix, "zsh", "claude"}},
	}

	extra := []string{"AIM_PANE=p2", "AIM_SOCK=/run/1.sock"}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			env := func(k string) string {
				if k == "SHELL" {
					return tt.shell
				}

				return ""
			}

			c := AgentCommand(env, Integration{}, "/work", tt.argv, extra)

			if want := append([]string{tt.sh, "-l", "-i", "-c"}, tt.want...); !slices.Equal(c.Argv, want) {
				t.Errorf("Argv = %q, want %q", c.Argv, want)
			}

			if c.Dir != "/work" {
				t.Errorf("Dir = %q, want /work", c.Dir)
			}

			// The inherited identity, shell and terminal values are dropped; the pane's
			// terminal values, the shell that runs and the extra entries end the list, in this order.
			wantTail := []string{"TERM=xterm-256color", "COLORTERM=truecolor", "SHELL=" + tt.sh, "AIM_PANE=p2", "AIM_SOCK=/run/1.sock"}
			if len(c.Env) < len(wantTail) || !slices.Equal(c.Env[len(c.Env)-len(wantTail):], wantTail) {
				t.Fatalf("Env ends with %q, want %q", c.Env[max(len(c.Env)-len(wantTail), 0):], wantTail)
			}

			for _, kv := range c.Env[:len(c.Env)-len(wantTail)] {
				for _, key := range []string{"SHELL=", "AIM_PANE=", "AIM_SOCK=", "TERM=", "COLORTERM="} {
					if strings.HasPrefix(kv, key) {
						t.Errorf("Env keeps the inherited %q", kv)
					}
				}
			}
		})
	}
}

// testShell returns a $SHELL for the named shell that reads no user or system
// startup files, and skips the test when the shell is not installed.
func testShell(t *testing.T, name string) string {
	t.Helper()

	path, err := exec.LookPath(name)
	if err != nil {
		t.Skipf("%s not installed", name)
	}

	flags := map[string]string{"fish": "--no-config", "zsh": "-f", "bash": "--noprofile --norc"}[name]
	if flags == "" {
		return path
	}

	wrapper := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(wrapper, []byte("#!/bin/sh\nexec '"+path+"' "+flags+" \"$@\"\n"), 0o755); err != nil {
		t.Fatal(err)
	}

	return wrapper
}

// runToExit runs c without a terminal and waits for it, under a deadline. The
// command's closing `exec "$SHELL" -l` reads EOF from the null device and exits.
func runToExit(t *testing.T, c Command) string {
	t.Helper()

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, c.Argv[0], c.Argv[1:]...)
	cmd.Dir, cmd.Env, cmd.WaitDelay = c.Dir, c.Env, time.Second

	out, _ := cmd.CombinedOutput()
	if ctx.Err() != nil {
		t.Fatalf("%q did not finish: %v\n%s", c.Argv, ctx.Err(), out)
	}

	return string(out)
}

// TestCommandsPassArgumentsUntouched runs the editor and agent commands under
// each shell, with arguments the shell would act on if it parsed them. They must
// reach the program byte for byte and nothing may run.
func TestCommandsPassArgumentsUntouched(t *testing.T) {
	for _, name := range []string{"sh", "bash", "zsh", "fish"} {
		t.Run(name, func(t *testing.T) {
			shell := testShell(t, name)

			for _, kind := range []string{"editor", "agent"} {
				for _, hostile := range []string{"escaped quote", "every metacharacter", "trailing backslash"} {
					t.Run(kind+"/"+hostile, func(t *testing.T) {
						out := shellTestEnv(t)
						dir, marker := t.TempDir(), filepath.Join(t.TempDir(), "injected")

						value := map[string]string{
							"escaped quote":       `x\';touch ` + marker + `;#`,
							"every metacharacter": "a\\'b'c$(touch pwned)`touch pwned`;touch pwned;# d\ne",
							"trailing backslash":  "it's a\\",
						}[hostile]

						env := func(k string) string {
							if k == "SHELL" {
								return shell
							}

							return ""
						}

						program := filepath.Join(filepath.Dir(out), "bin", "claude")

						var (
							c    Command
							want []string
						)

						switch kind {
						case "editor":
							want = []string{"+3", value}
							c = EditorCommand(env, Integration{}, dir, program, want, nil)
						default:
							want = []string{"--settings", value, "--resume", value}
							c = AgentCommand(env, Integration{}, dir, append([]string{program}, want...), nil)
						}

						shellOut := runToExit(t, c)

						raw, err := os.ReadFile(filepath.Join(out, "args"))
						if err != nil {
							t.Fatalf("the program did not run: %v\n%s", err, shellOut)
						}

						if got := strings.Split(strings.TrimSuffix(string(raw), "\x00"), "\x00"); !slices.Equal(got, want) {
							t.Errorf("args = %q, want %q\n%s", got, want, shellOut)
						}

						if _, err := os.Stat(marker); err == nil {
							t.Error("an injected command ran: the marker exists")
						}

						if left, _ := os.ReadDir(dir); len(left) != 0 {
							t.Errorf("a command in an argument ran: %d files in the working directory", len(left))
						}
					})
				}
			}
		})
	}
}
