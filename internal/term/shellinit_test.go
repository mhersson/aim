package term

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func hasEnv(env []string, kv string) bool { return slices.Contains(env, kv) }

func hasKey(env []string, key string) bool {
	return slices.ContainsFunc(env, func(kv string) bool { return strings.HasPrefix(kv, key+"=") })
}

func TestIntegrationCommands(t *testing.T) {
	const (
		dir         = "/run/1-shell"
		zshTailText = `; if [ -r "$AIM_SHELL_DIR/zsh/.zshenv" ]; then export ZDOTDIR="$AIM_SHELL_DIR/zsh"; fi; exec "$SHELL" -l`
		plain       = `; exec "$SHELL" -l`
		fishTail    = `; exec "$SHELL" -l -C '` + fishInit + `'`
	)

	tests := []struct {
		name      string
		shell     string
		zsh       bool
		zdotdir   string // inherited ZDOTDIR; "" means unset
		wantArgv  []string
		wantTail  string
		wantZdot  string // AIM's ZDOTDIR entry of the shell command; "" means none
		wantAimZd string // AIM_ZDOTDIR entry; "" means none
		emptyZdot bool   // ZDOTDIR is inherited set but empty; zdotdir is then ""
	}{
		{"zsh on, ZDOTDIR set", "/bin/zsh", true, "/home/u/zd", []string{"/bin/zsh", "-l"}, zshTailText, "ZDOTDIR=" + dir + "/zsh", "AIM_ZDOTDIR=/home/u/zd", false},
		{"zsh on, ZDOTDIR unset", "/bin/zsh", true, "", []string{"/bin/zsh", "-l"}, zshTailText, "ZDOTDIR=" + dir + "/zsh", "", false},
		{"zsh on, ZDOTDIR set but empty", "/bin/zsh", true, "", []string{"/bin/zsh", "-l"}, zshTailText, "ZDOTDIR=" + dir + "/zsh", "AIM_ZDOTDIR=", true},
		{"zsh off", "/bin/zsh", false, "/home/u/zd", []string{"/bin/zsh", "-l"}, plain, "", "", false},
		{"fish", "/usr/bin/fish", false, "", []string{"/usr/bin/fish", "-l", "-C", fishInit}, fishTail, "", "", false},
		{"sh", "/bin/sh", true, "/home/u/zd", []string{"/bin/sh", "-l"}, plain, "", "", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("PATH", "/usr/bin:/bin")

			// Setenv registers the restore; the Unsetenv then leaves it unset.
			t.Setenv("ZDOTDIR", tt.zdotdir)

			if tt.zdotdir == "" && !tt.emptyZdot {
				if err := os.Unsetenv("ZDOTDIR"); err != nil {
					t.Fatal(err)
				}
			}

			env := func(string) string { return tt.shell }
			ig := Integration{Dir: dir, Zsh: tt.zsh}
			extra := []string{"AIM_PANE=p1"}

			sh := ShellCommand(env, ig, "/w", extra)
			if !slices.Equal(sh.Argv, tt.wantArgv) {
				t.Errorf("shell argv = %q, want %q", sh.Argv, tt.wantArgv)
			}

			for _, kv := range []string{"PATH=" + dir + "/bin:/usr/bin:/bin", "AIM_SHELL_DIR=" + dir, "SHELL=" + tt.shell} {
				if !hasEnv(sh.Env, kv) {
					t.Errorf("shell env lacks %q: %q", kv, sh.Env)
				}
			}

			if n := countKey(sh.Env, "PATH"); n != 1 {
				t.Errorf("shell env has %d PATH entries, want 1", n)
			}

			wantZ := tt.wantZdot
			if wantZ == "" && (tt.zdotdir != "" || tt.emptyZdot) {
				wantZ = "ZDOTDIR=" + tt.zdotdir // not AIM's: left as inherited
			}

			checkOpt(t, sh.Env, "ZDOTDIR", wantZ)
			checkOpt(t, sh.Env, "AIM_ZDOTDIR", tt.wantAimZd)

			if sh.Env[len(sh.Env)-1] != "AIM_PANE=p1" {
				t.Errorf("extraEnv is not last: %q", sh.Env)
			}

			ag := AgentCommand(env, ig, "/w", []string{"claude"}, extra)
			if !strings.HasSuffix(ag.Argv[4], tt.wantTail) {
				t.Errorf("agent script = %q, want tail %q", ag.Argv[4], tt.wantTail)
			}

			if tt.zdotdir == "" && !tt.emptyZdot && hasKey(ag.Env, "ZDOTDIR") {
				t.Errorf("agent env sets ZDOTDIR: %q", ag.Env)
			}

			if (tt.zdotdir != "" || tt.emptyZdot) && !hasEnv(ag.Env, "ZDOTDIR="+tt.zdotdir) {
				t.Errorf("agent env lost the inherited ZDOTDIR: %q", ag.Env)
			}

			checkOpt(t, ag.Env, "AIM_ZDOTDIR", tt.wantAimZd)

			ed := EditorCommand(env, ig, "/w", "vi", []string{"f"}, extra)
			if !strings.HasSuffix(ed.Argv[4], tt.wantTail) {
				t.Errorf("editor script = %q, want tail %q", ed.Argv[4], tt.wantTail)
			}

			eo := EditorOnlyCommand(env, ig, "/w", "vi", []string{"f"}, extra)
			if strings.Contains(eo.Argv[4], "exec") {
				t.Errorf("editor-only script has a tail: %q", eo.Argv[4])
			}

			if !hasEnv(eo.Env, "PATH="+dir+"/bin:/usr/bin:/bin") || !hasEnv(eo.Env, "AIM_SHELL_DIR="+dir) {
				t.Errorf("editor-only env lacks the integration: %q", eo.Env)
			}
		})
	}
}

// fixedEnv makes os.Environ exactly kvs, in order, until the test ends.
func fixedEnv(t *testing.T, kvs ...string) {
	t.Helper()

	for _, kv := range os.Environ() {
		k, _, _ := strings.Cut(kv, "=")

		// Setenv registers the restore; the Unsetenv then leaves it unset.
		t.Setenv(k, "")

		if err := os.Unsetenv(k); err != nil {
			t.Fatal(err)
		}
	}

	for _, kv := range kvs {
		k, v, _ := strings.Cut(kv, "=")
		t.Setenv(k, v)
	}
}

func TestIntegrationEnvOrder(t *testing.T) {
	const dir = "/run/1-shell"

	fixedEnv(t, "HOME=/home/u", "SHELL=/bin/old", "PATH=/usr/bin:/bin", "ZDOTDIR=/home/u/zd", "TERM=dumb")

	env := func(string) string { return "/bin/zsh" }
	ig := Integration{Dir: dir, Zsh: true}
	extra := []string{"AIM_PANE=p1"}

	base := []string{"HOME=/home/u", "TERM=xterm-256color", "COLORTERM=truecolor"}
	common := []string{"SHELL=/bin/zsh", "PATH=" + dir + "/bin:/usr/bin:/bin", "AIM_SHELL_DIR=" + dir, "AIM_ZDOTDIR=/home/u/zd"}

	tests := []struct {
		name string
		got  []string
		want []string
	}{
		// The shell root takes AIM's ZDOTDIR; an agent keeps the inherited one in place.
		{"shell", ShellCommand(env, ig, "/w", extra).Env, slices.Concat(base, common, []string{"ZDOTDIR=" + dir + "/zsh"}, extra)},
		{"agent", AgentCommand(env, ig, "/w", []string{"claude"}, extra).Env, slices.Concat(
			[]string{"HOME=/home/u", "ZDOTDIR=/home/u/zd", "TERM=xterm-256color", "COLORTERM=truecolor"}, common, extra)},
	}

	for _, tt := range tests {
		if !slices.Equal(tt.got, tt.want) {
			t.Errorf("%s env = %q, want %q", tt.name, tt.got, tt.want)
		}
	}
}

func TestIntegrationOff(t *testing.T) {
	fixedEnv(t, "HOME=/home/u", "SHELL=/bin/old", "PATH=/usr/bin", "TERM=dumb")

	env := func(string) string { return "/bin/zsh" }
	extra := []string{"AIM_PANE=p1"}

	want := []string{"HOME=/home/u", "PATH=/usr/bin", "TERM=xterm-256color", "COLORTERM=truecolor", "SHELL=/bin/zsh", "AIM_PANE=p1"}
	tail := `; exec "$SHELL" -l`

	tests := []struct {
		name string
		c    Command
		argv []string
	}{
		{"shell", ShellCommand(env, Integration{}, "/w", extra), []string{"/bin/zsh", "-l"}},
		{"agent", AgentCommand(env, Integration{}, "/w", []string{"claude"}, extra), []string{"/bin/zsh", "-l", "-i", "-c", `"$@"` + tail, "zsh", "claude"}},
		{"editor", EditorCommand(env, Integration{}, "/w", "vi", nil, extra), []string{"/bin/zsh", "-l", "-i", "-c", `vi "$@"` + tail, "zsh"}},
	}

	for _, tt := range tests {
		if !slices.Equal(tt.c.Env, want) {
			t.Errorf("%s env = %q, want %q", tt.name, tt.c.Env, want)
		}

		if !slices.Equal(tt.c.Argv, tt.argv) {
			t.Errorf("%s argv = %q, want %q", tt.name, tt.c.Argv, tt.argv)
		}
	}

	if c := AgentCommand(env, Integration{Zsh: true}, "/w", []string{"claude"}, nil); !strings.HasSuffix(c.Argv[4], tail) || hasKey(c.Env, "AIM_SHELL_DIR") {
		t.Errorf("a Zsh flag without a Dir changed the command: %q", c.Argv)
	}
}

func TestEmptyPathLeftAsIs(t *testing.T) {
	t.Setenv("PATH", "")

	c := ShellCommand(func(string) string { return "/bin/sh" }, Integration{Dir: "/d"}, "/w", nil)

	if !hasEnv(c.Env, "PATH=") || !hasEnv(c.Env, "AIM_SHELL_DIR=/d") {
		t.Errorf("env = %q, want the empty PATH kept and AIM_SHELL_DIR set", c.Env)
	}
}

func countKey(env []string, key string) int {
	n := 0

	for _, kv := range env {
		if strings.HasPrefix(kv, key+"=") {
			n++
		}
	}

	return n
}

func checkOpt(t *testing.T, env []string, key, want string) {
	t.Helper()

	switch {
	case want == "" && hasKey(env, key):
		t.Errorf("env has %s, want none: %q", key, env)
	case want != "" && (!hasEnv(env, want) || countKey(env, key) != 1):
		t.Errorf("env lacks a single %q: %q", want, env)
	}
}

func TestNewIntegration(t *testing.T) {
	tests := []struct {
		name  string
		files []string
		want  bool
	}{
		{"none", nil, false},
		{".zshenv", []string{".zshenv"}, true},
		{".zprofile", []string{".zprofile"}, true},
		{".zshrc", []string{".zshrc"}, true},
		{".zlogin", []string{".zlogin"}, true},
		{"other files", []string{".bashrc", ".profile"}, false},
	}

	for _, tt := range tests {
		for _, viaZdot := range []bool{false, true} {
			name := tt.name
			if viaZdot {
				name += " via ZDOTDIR"
			}

			t.Run(name, func(t *testing.T) {
				t.Parallel()

				home, zd := t.TempDir(), t.TempDir()
				place := home

				if viaZdot {
					place = zd
				}

				for _, f := range tt.files {
					if err := os.WriteFile(filepath.Join(place, f), nil, 0o600); err != nil {
						t.Fatal(err)
					}
				}

				getenv := func(k string) string {
					switch {
					case k == "HOME":
						return home
					case k == "ZDOTDIR" && viaZdot:
						return zd
					}

					return ""
				}

				ig := NewIntegration("/run/1-shell", getenv)
				if ig.Dir != "/run/1-shell" || ig.Zsh != tt.want {
					t.Errorf("NewIntegration = %+v, want Zsh %v", ig, tt.want)
				}
			})
		}
	}

	t.Run("ZDOTDIR replaces HOME", func(t *testing.T) {
		t.Parallel()

		home := t.TempDir()
		if err := os.WriteFile(filepath.Join(home, ".zshrc"), nil, 0o600); err != nil {
			t.Fatal(err)
		}

		getenv := func(k string) string {
			if k == "ZDOTDIR" {
				return t.TempDir()
			}

			return home
		}

		if NewIntegration("/d", getenv).Zsh {
			t.Error("Zsh = true from HOME although ZDOTDIR is set")
		}
	})

	t.Run("no HOME and no ZDOTDIR", func(t *testing.T) {
		t.Chdir(t.TempDir())

		if err := os.WriteFile(".zshrc", nil, 0o600); err != nil {
			t.Fatal(err)
		}

		if NewIntegration("/d", func(string) string { return "" }).Zsh {
			t.Error("Zsh = true from a .zshrc in the working directory")
		}
	})

	t.Run("empty dir is off", func(t *testing.T) {
		t.Parallel()

		if ig := NewIntegration("", func(string) string { return "" }); ig != (Integration{}) {
			t.Errorf("NewIntegration(\"\") = %+v, want zero", ig)
		}
	})
}

func TestWriteIntegration(t *testing.T) {
	t.Parallel()

	t.Run("files and modes", func(t *testing.T) {
		t.Parallel()

		dir := filepath.Join(t.TempDir(), "run dir", "1-shell")

		if err := WriteIntegration(dir, "/the/wrapper"); err != nil {
			t.Fatal(err)
		}

		for _, s := range scripts() {
			want, err := shellInitFS.ReadFile(s.embedded)
			if err != nil {
				t.Fatal(err)
			}

			p := filepath.Join(dir, s.path)

			got, err := os.ReadFile(p)
			if err != nil || string(got) != string(want) {
				t.Errorf("%s = %q, %v; want the embedded text", s.path, got, err)
			}

			if fi, err := os.Stat(p); err != nil || fi.Mode().Perm() != 0o600 {
				t.Errorf("%s mode = %v, %v; want 0600", s.path, fi.Mode(), err)
			}

			if fi, err := os.Stat(filepath.Dir(p)); err != nil || fi.Mode().Perm() != 0o700 {
				t.Errorf("%s dir mode = %v, %v; want 0700", s.path, fi.Mode(), err)
			}
		}

		for _, d := range []string{dir, filepath.Join(dir, "bin")} {
			if fi, err := os.Stat(d); err != nil || fi.Mode().Perm() != 0o700 {
				t.Errorf("%s mode = %v, %v; want 0700", d, fi.Mode(), err)
			}
		}

		if got, err := os.Readlink(filepath.Join(dir, "bin", "claude")); err != nil || got != "/the/wrapper" {
			t.Errorf("bin/claude -> %q, %v; want /the/wrapper", got, err)
		}
	})

	t.Run("replaces an existing dir", func(t *testing.T) {
		t.Parallel()

		dir := filepath.Join(t.TempDir(), "1-shell")
		stray := filepath.Join(dir, "stray")

		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatal(err)
		}

		if err := os.WriteFile(stray, []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}

		if err := WriteIntegration(dir, "/w"); err != nil {
			t.Fatal(err)
		}

		if _, err := os.Stat(stray); !os.IsNotExist(err) {
			t.Errorf("stray file survived: %v", err)
		}
	})

	t.Run("empty dir fails", func(t *testing.T) {
		t.Parallel()

		if err := WriteIntegration("", "/w"); err == nil {
			t.Error("WriteIntegration(\"\") succeeded")
		}
	})

	t.Run("failure leaves nothing", func(t *testing.T) {
		t.Parallel()

		file := filepath.Join(t.TempDir(), "file")
		if err := os.WriteFile(file, nil, 0o600); err != nil {
			t.Fatal(err)
		}

		dir := filepath.Join(file, "1-shell")
		if err := WriteIntegration(dir, "/w"); err == nil {
			t.Fatal("WriteIntegration under a regular file succeeded")
		}

		if _, err := os.Lstat(dir); err == nil {
			t.Error("dir exists after a failed write")
		}
	})
}
