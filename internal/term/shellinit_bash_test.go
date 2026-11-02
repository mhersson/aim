package term

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
)

func TestBashReadsENV(t *testing.T) {
	t.Parallel()

	tests := []struct {
		goos, sh string
		want     bool
	}{
		{"darwin", "/bin/bash", false},
		{"darwin", "/opt/homebrew/bin/bash", true},
		{"linux", "/bin/bash", true},
	}

	for _, tt := range tests {
		if got := bashReadsENV(tt.goos, tt.sh); got != tt.want {
			t.Errorf("bashReadsENV(%q, %q) = %v, want %v", tt.goos, tt.sh, got, tt.want)
		}
	}
}

func TestBashCommands(t *testing.T) {
	const (
		dir      = "/run dir/1-shell"
		bashTail = `; if [ -r "$AIM_SHELL_DIR/bash/init.bash" ]; then export ENV="$AIM_SHELL_DIR/bash/init.bash"; exec "$SHELL" --posix -l; fi; exec "$SHELL" -l`
	)

	if runtime.GOOS == "darwin" {
		t.Skip("/bin/bash is the Apple case on darwin")
	}

	tests := []struct {
		name   string
		env    string // inherited ENV; "" means unset
		setEnv bool
	}{
		{"ENV unset", "", false},
		{"ENV set", "/home/u/env.sh", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("PATH", "/usr/bin:/bin")
			t.Setenv("ENV", tt.env)

			if !tt.setEnv {
				if err := os.Unsetenv("ENV"); err != nil {
					t.Fatal(err)
				}
			}

			env := func(string) string { return "/bin/bash" }
			ig := Integration{Dir: dir}
			extra := []string{"AIM_PANE=p1"}

			sh := ShellCommand(env, ig, "/w", extra)
			if want := []string{"/bin/bash", "--posix", "-l"}; !slices.Equal(sh.Argv, want) {
				t.Errorf("shell argv = %q, want %q", sh.Argv, want)
			}

			if n := countKey(sh.Env, "ENV"); n != 1 || !hasEnv(sh.Env, "ENV="+dir+"/bash/init.bash") {
				t.Errorf("shell env ENV entries = %d, want only AIM's: %q", n, sh.Env)
			}

			wantAim := ""
			if tt.setEnv {
				wantAim = "AIM_ENV=" + tt.env
			}

			checkOpt(t, sh.Env, "AIM_ENV", wantAim)

			ag := AgentCommand(env, ig, "/w", []string{"claude"}, extra)
			if !strings.HasSuffix(ag.Argv[4], bashTail) {
				t.Errorf("agent script = %q, want tail %q", ag.Argv[4], bashTail)
			}

			checkOpt(t, ag.Env, "AIM_ENV", wantAim)

			if tt.setEnv && !hasEnv(ag.Env, "ENV="+tt.env) {
				t.Errorf("agent env lost the inherited ENV: %q", ag.Env)
			}

			if !tt.setEnv && hasKey(ag.Env, "ENV") {
				t.Errorf("agent env sets ENV: %q", ag.Env)
			}

			ed := EditorCommand(env, ig, "/w", "vi", []string{"f"}, extra)
			if !strings.HasSuffix(ed.Argv[4], bashTail) {
				t.Errorf("editor script = %q, want tail %q", ed.Argv[4], bashTail)
			}

			eo := EditorOnlyCommand(env, ig, "/w", "vi", []string{"f"}, extra)
			if hasKey(eo.Env, "AIM_ENV") != tt.setEnv {
				t.Errorf("editor-only AIM_ENV presence differs from %v: %q", tt.setEnv, eo.Env)
			}
		})
	}
}

func bashEnv(string) string {
	p, err := exec.LookPath("bash")
	if err != nil {
		return "/bin/bash"
	}

	return p
}

// skipUnlessBash skips when bash is missing or is Apple's /bin/bash.
func skipUnlessBash(t *testing.T) {
	t.Helper()

	p, err := exec.LookPath("bash")
	if err != nil {
		t.Skip("bash is not installed")
	}

	if !bashReadsENV(runtime.GOOS, p) {
		t.Skip("this bash does not read ENV")
	}
}

// toFile is a typed command that writes cmd's output to $OUT_DIR/name atomically.
func toFile(name, cmd string) string {
	return `{ ` + cmd + `; } > "$OUT_DIR/` + name + `.tmp" 2>&1; mv "$OUT_DIR/` + name + `.tmp" "$OUT_DIR/` + name + `"`
}

const probePosix = `case :$SHELLOPTS: in *:posix:*) echo posix=on;; *) echo posix=off;; esac`

func TestBashIntegration(t *testing.T) {
	skipUnlessBash(t)

	t.Run("bash_profile sources bashrc, native line and an alias", func(t *testing.T) {
		r := newShellRig(t)

		r.write(t, ".bash_profile", r.record("bash_profile")+`{ shopt -q login_shell && echo login=on || echo login=off; `+probePosix+`; echo ENV=${ENV-unset}; } >> "$OUT_DIR/bp"`+"\n"+`[ -f ~/.bashrc ] && . ~/.bashrc`+"\n")
		r.write(t, ".bashrc", r.record("bashrc")+nativeLine+"alias claude='claude --dangerously-skip-permissions'\n")

		ig := r.integrate(t)

		rt := r.start(t, ShellCommand(bashEnv, ig, r.Home, nil))
		rt.typeLine(t, "claude a 'b c'")
		rt.typeLine(t, "command claude c")

		runs := r.records(t, "real", 2)
		if want := []string{"--settings", rigSettings, "--dangerously-skip-permissions", "a", "b c"}; !slices.Equal(runs[0], want) {
			t.Errorf("first real run = %q, want %q", runs[0], want)
		}

		if want := []string{"--settings", rigSettings, "c"}; !slices.Equal(runs[1], want) {
			t.Errorf("second real run = %q, want %q", runs[1], want)
		}

		if got, want := r.lines("order"), []string{"bash_profile", "bashrc"}; !slices.Equal(got, want) {
			t.Errorf("startup order = %q, want %q", got, want)
		}

		if got, want := r.lines("bp"), []string{"login=on", "posix=off", "ENV=unset"}; !slices.Equal(got, want) {
			t.Errorf(".bash_profile saw %q, want %q", got, want)
		}

		for _, bad := range []string{"not found", "syntax error", "No such file", "bash:"} {
			if strings.Contains(rt.output(), bad) {
				t.Errorf("terminal output has %q", bad)
			}
		}
	})

	t.Run("only .profile and an inherited ENV", func(t *testing.T) {
		r := newShellRig(t)
		envFile := filepath.Join(r.Home, "env.sh")
		t.Setenv("ENV", envFile)

		r.write(t, ".profile", `echo "ENV=$ENV" >> "$OUT_DIR/profile_env"`+"\n"+`if [ -d "$HOME/.local/bin" ] ; then`+"\n"+`    PATH="$HOME/.local/bin:$PATH"`+"\n"+"fi\n")

		ig := r.integrate(t)

		rt := r.start(t, ShellCommand(bashEnv, ig, r.Home, nil))
		rt.typeLine(t, "claude p")
		rt.typeLine(t, toFile("shell_env", "echo $ENV"))

		if want := []string{"--settings", rigSettings, "p"}; !slices.Equal(r.args(t, "real"), want) {
			t.Errorf("real run differs from %q", want)
		}

		if got, want := r.lines("profile_env"), []string{"ENV=" + envFile}; !slices.Equal(got, want) {
			t.Errorf(".profile saw %q, want %q", got, want)
		}

		if got := strings.TrimSpace(r.await(t, "shell_env")); got != envFile {
			t.Errorf("shell ENV = %q, want %q", got, envFile)
		}
	})

	t.Run("a user function", func(t *testing.T) {
		r := newShellRig(t)

		r.write(t, ".bash_profile", ". ~/.bashrc\n")
		r.write(t, ".bashrc", nativeLine+"function claude { command claude --user-fn \"$@\"; }\n")

		ig := r.integrate(t)

		rt := r.start(t, ShellCommand(bashEnv, ig, r.Home, nil))
		rt.typeLine(t, "claude u")

		if want := []string{"--settings", rigSettings, "--user-fn", "u"}; !slices.Equal(r.args(t, "real"), want) {
			t.Errorf("real run differs from %q", want)
		}
	})

	t.Run("the state of a plain login shell", func(t *testing.T) {
		r := newShellRig(t)

		r.write(t, ".bash_profile", ". ~/.bashrc\n")
		r.write(t, ".bashrc", nativeLine)

		dump := func(name string) string {
			return toFile(name, `shopt -p; set -o; declare -F; declare -p HISTFILE HISTSIZE HISTFILESIZE MAILCHECK PS1`)
		}

		ig := r.integrate(t)

		rt := r.start(t, ShellCommand(bashEnv, ig, r.Home, nil))
		rt.typeLine(t, dump("with"))

		plain := r.start(t, ShellCommand(bashEnv, Integration{}, r.Home, nil))
		plain.typeLine(t, dump("plain"))

		with := strings.Split(r.await(t, "with"), "\n")
		without := strings.Split(r.await(t, "plain"), "\n")

		const fn = "declare -f claude"

		if !slices.Contains(with, fn) {
			t.Error("the integration shell has no claude function")
		}

		with = slices.DeleteFunc(with, func(l string) bool { return l == fn })

		if !slices.Equal(with, without) {
			t.Errorf("state differs from a plain bash -l:\nwith:  %q\nplain: %q", with, without)
		}
	})

	t.Run("history and logout", func(t *testing.T) {
		r := newShellRig(t)

		r.write(t, ".bash_history", "echo FROM_BASH_HISTORY\n")
		r.write(t, ".bash_logout", `echo bye > "$OUT_DIR/logout"`+"\n")

		ig := r.integrate(t)

		rt := r.start(t, ShellCommand(bashEnv, ig, r.Home, nil))
		rt.typeLine(t, toFile("hist", "history"))

		if got := r.await(t, "hist"); !strings.Contains(got, "echo FROM_BASH_HISTORY") {
			t.Errorf("history = %q, want the history file's line", got)
		}

		rt.typeLine(t, "exit")
		r.await(t, "logout")
	})

	t.Run("an exported HISTFILE", func(t *testing.T) {
		r := newShellRig(t)
		hf := filepath.Join(r.Home, "h")
		t.Setenv("HISTFILE", hf)

		ig := r.integrate(t)

		rt := r.start(t, ShellCommand(bashEnv, ig, r.Home, nil))
		rt.typeLine(t, toFile("hf", "echo $HISTFILE"))

		if got := strings.TrimSpace(r.await(t, "hf")); got != hf {
			t.Errorf("HISTFILE = %q, want %q", got, hf)
		}
	})

	t.Run("the shell after an agent", func(t *testing.T) {
		r := newShellRig(t)

		r.write(t, ".bash_profile", nativeLine)

		ig := r.integrate(t)

		rt := r.start(t, AgentCommand(bashEnv, ig, r.Home, []string{"true"}, nil))
		rt.typeLine(t, "claude t")
		rt.typeLine(t, toFile("posix", probePosix))

		if want := []string{"--settings", rigSettings, "t"}; !slices.Equal(r.args(t, "real"), want) {
			t.Errorf("real run differs from %q", want)
		}

		if got := strings.TrimSpace(r.await(t, "posix")); got != "posix=off" {
			t.Errorf("shell after the agent: %s", got)
		}
	})

	t.Run("the wrapper gone after a hash", func(t *testing.T) {
		r := newShellRig(t)

		r.write(t, ".bash_profile", ". ~/.bashrc\n")
		r.write(t, ".bashrc", nativeLine)

		ig := r.integrate(t)

		rt := r.start(t, ShellCommand(bashEnv, ig, r.Home, nil))
		rt.typeLine(t, "command claude h")

		if want := []string{"--settings", rigSettings, "h"}; !slices.Equal(r.args(t, "real"), want) {
			t.Errorf("first real run differs from %q", want)
		}

		if err := os.Remove(r.wrapper); err != nil {
			t.Fatal(err)
		}

		rt.typeLine(t, "claude g")

		if want := []string{"g"}; !slices.Equal(r.args(t, "real"), want) {
			t.Errorf("second real run differs from %q", want)
		}

		for _, bad := range []string{"No such file", "not found", "Permission denied"} {
			if strings.Contains(rt.output(), bad) {
				t.Errorf("terminal output has %q", bad)
			}
		}
	})
}

func TestAppleBashGetsTheWrapperOnly(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("Apple's /bin/bash exists on darwin only")
	}

	if _, err := os.Stat("/bin/bash"); err != nil {
		t.Skip("no /bin/bash")
	}

	// /etc/profile's path_helper moves these dirs ahead of the inherited PATH.
	files, _ := filepath.Glob("/etc/paths.d/*")
	for _, f := range append(files, "/etc/paths") {
		b, err := os.ReadFile(f)
		if err != nil {
			continue
		}

		for d := range strings.FieldsSeq(string(b)) {
			if _, err := os.Stat(filepath.Join(d, "claude")); err == nil { //nolint:gosec // system PATH dirs
				t.Skipf("%s holds a claude, which path_helper would put first", d)
			}
		}
	}

	r := newShellRig(t)
	t.Setenv("PATH", filepath.Join(r.Home, ".local", "bin")+":"+os.Getenv("PATH"))

	r.write(t, ".bash_profile", r.record("bash_profile"))

	ig := r.integrate(t)
	env := func(string) string { return "/bin/bash" }

	c := ShellCommand(env, ig, r.Home, nil)
	if want := []string{"/bin/bash", "-l"}; !slices.Equal(c.Argv, want) {
		t.Errorf("argv = %q, want %q", c.Argv, want)
	}

	if hasKey(c.Env, "ENV") || hasKey(c.Env, "AIM_ENV") {
		t.Errorf("env names ENV: %q", c.Env)
	}

	rt := r.start(t, c)
	rt.typeLine(t, "claude a")

	if want := []string{"--settings", rigSettings, "a"}; !slices.Equal(r.args(t, "real"), want) {
		t.Errorf("real run differs from %q", want)
	}
}
