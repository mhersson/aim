package term

import (
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

const nativeLine = `export PATH="$HOME/.local/bin:$PATH"` + "\n"

func zshEnv(string) string {
	p, err := exec.LookPath("zsh")
	if err != nil {
		return "/bin/zsh"
	}

	return p
}

// requireAll fails for every line that does not end in suffix.
func requireAll(t *testing.T, lines []string, suffix string) {
	t.Helper()

	for _, l := range lines {
		if !strings.HasSuffix(l, suffix) {
			t.Errorf("line %q does not end in %q", l, suffix)
		}
	}
}

func TestZshIntegration(t *testing.T) {
	if _, err := exec.LookPath("zsh"); err != nil {
		t.Skip("zsh is not installed")
	}

	t.Run("native line and an alias", func(t *testing.T) {
		r := newShellRig(t)

		for _, f := range []string{".zshenv", ".zprofile", ".zlogin"} {
			r.write(t, f, r.record(f[1:]))
		}

		r.write(t, ".zshrc", r.record("zshrc")+nativeLine+"alias claude='claude --dangerously-skip-permissions'\n")

		ig := r.integrate(t)
		if !ig.Zsh {
			t.Fatal("Zsh = false with startup files present")
		}

		rt := r.start(t, ShellCommand(zshEnv, ig, r.Home, nil))
		rt.typeLine(t, "claude a 'b c'")
		rt.typeLine(t, "command claude c")

		wrap := r.records(t, "wrap", 2)
		if want := []string{"--dangerously-skip-permissions", "a", "b c"}; !slices.Equal(wrap[0], want) {
			t.Errorf("first wrapper run = %q, want %q", wrap[0], want)
		}

		if want := []string{"c"}; !slices.Equal(wrap[1], want) {
			t.Errorf("second wrapper run = %q, want %q", wrap[1], want)
		}

		runs := r.records(t, "real", 2)
		if want := []string{"--settings", rigSettings, "--dangerously-skip-permissions", "a", "b c"}; !slices.Equal(runs[0], want) {
			t.Errorf("first real run = %q, want %q", runs[0], want)
		}

		if want := []string{"--settings", rigSettings, "c"}; !slices.Equal(runs[1], want) {
			t.Errorf("second real run = %q, want %q", runs[1], want)
		}

		if got, want := r.lines("order"), []string{"zshenv", "zprofile", "zshrc", "zlogin"}; !slices.Equal(got, want) {
			t.Errorf("startup order = %q, want %q", got, want)
		}

		requireAll(t, r.lines("zdot"), "=unset")
	})

	t.Run("user ZDOTDIR in .zshenv and a user function", func(t *testing.T) {
		r := newShellRig(t)

		r.write(t, ".zshenv", `export ZDOTDIR="$HOME/.config/zsh"`+"\n")
		r.write(t, ".config/zsh/.zshrc", r.record("zshrc")+nativeLine+"claude() { command claude --user-fn \"$@\" }\n")

		ig := r.integrate(t)

		rt := r.start(t, ShellCommand(zshEnv, ig, r.Home, nil))
		rt.typeLine(t, "claude a")

		if want := []string{"--settings", rigSettings, "--user-fn", "a"}; !slices.Equal(r.args(t, "real"), want) {
			t.Errorf("real run differs from %q", want)
		}

		if want := []string{"zshrc=" + filepath.Join(r.Home, ".config/zsh")}; !slices.Equal(r.lines("zdot"), want) {
			t.Errorf("zdot = %q, want %q", r.lines("zdot"), want)
		}
	})

	t.Run("inherited ZDOTDIR", func(t *testing.T) {
		r := newShellRig(t)
		zd := filepath.Join(r.Home, "zd")
		t.Setenv("ZDOTDIR", zd)

		r.write(t, "zd/.zshrc", r.record("zshrc")+nativeLine)

		ig := r.integrate(t)

		rt := r.start(t, ShellCommand(zshEnv, ig, r.Home, nil))
		rt.typeLine(t, "claude x")
		rt.typeLine(t, "print -r -- $ZDOTDIR > $OUT_DIR/zd.tmp; mv $OUT_DIR/zd.tmp $OUT_DIR/zd")

		if want := []string{"--settings", rigSettings, "x"}; !slices.Equal(r.args(t, "real"), want) {
			t.Errorf("real run differs from %q", want)
		}

		if got := strings.TrimSpace(r.await(t, "zd")); got != zd {
			t.Errorf("shell ZDOTDIR = %q, want %q", got, zd)
		}

		if want := []string{"zshrc=" + zd}; !slices.Equal(r.lines("zdot"), want) {
			t.Errorf("zdot = %q, want %q", r.lines("zdot"), want)
		}
	})

	t.Run("a precmd that reorders PATH", func(t *testing.T) {
		r := newShellRig(t)

		r.fakeClaude(t, filepath.Join(r.Home, "mise"), "mise")
		r.write(t, ".zshrc", nativeLine+`_mise_hook() { path=("$HOME/mise" ${path:#$HOME/mise}) }`+"\nprecmd_functions+=(_mise_hook)\n")

		ig := r.integrate(t)

		rt := r.start(t, ShellCommand(zshEnv, ig, r.Home, nil))
		rt.typeLine(t, "claude m")

		if want := []string{"m"}; !slices.Equal(r.args(t, "wrap"), want) {
			t.Errorf("wrapper run differs from %q", want)
		}

		if want := []string{"--settings", rigSettings, "m"}; !slices.Equal(r.args(t, "mise"), want) {
			t.Errorf("mise run differs from %q", want)
		}
	})

	t.Run("no zsh startup files", func(t *testing.T) {
		r := newShellRig(t)

		ig := r.integrate(t)
		if ig.Zsh {
			t.Fatal("Zsh = true with no startup files")
		}

		c := ShellCommand(zshEnv, ig, r.Home, nil)
		for _, kv := range c.Env {
			if strings.HasPrefix(kv, "ZDOTDIR=") {
				t.Errorf("env has %q", kv)
			}
		}

		rt := r.start(t, c)
		// zsh offers its first-run setup here; q leaves it and does nothing.
		rt.typeLine(t, "q")
		rt.typeLine(t, "claude n")

		if want := []string{"n"}; !slices.Equal(r.args(t, "wrap"), want) {
			t.Errorf("wrapper run differs from %q", want)
		}
	})

	t.Run("the state of a plain login shell", func(t *testing.T) {
		r := newShellRig(t)

		r.write(t, ".zshenv", "export FROM_ZSHENV=1\n")
		r.write(t, ".zshrc", "setopt extendedglob rcquotes\nalias ll='ls -l'\nfpath=($HOME/fn $fpath)\n")

		dump := func(name string) string {
			return `{ setopt; print -l ${(ko)functions}; print -l $fpath; print ${+precmd_functions}; print ${+FROM_ZSHENV}; } > $OUT_DIR/` + name + `.tmp; mv $OUT_DIR/` + name + `.tmp $OUT_DIR/` + name
		}

		ig := r.integrate(t)

		rt := r.start(t, ShellCommand(zshEnv, ig, r.Home, nil))
		rt.typeLine(t, dump("with"))

		plain := r.start(t, ShellCommand(zshEnv, Integration{}, r.Home, nil))
		plain.typeLine(t, dump("plain"))

		// A plain shell defines no function, so its list is one empty line; drop empty lines on both sides.
		with := slices.DeleteFunc(strings.Split(r.await(t, "with"), "\n"), func(l string) bool { return l == "claude" || l == "" })
		without := slices.DeleteFunc(strings.Split(r.await(t, "plain"), "\n"), func(l string) bool { return l == "" })

		if !slices.Equal(with, without) {
			t.Errorf("state differs from a plain zsh -l:\nwith:  %q\nplain: %q", with, without)
		}

		if !slices.Contains(strings.Split(r.await(t, "with"), "\n"), "claude") {
			t.Error("the integration shell has no claude function")
		}
	})

	t.Run("the shell after an agent", func(t *testing.T) {
		r := newShellRig(t)

		r.write(t, ".zshrc", nativeLine)

		ig := r.integrate(t)

		rt := r.start(t, AgentCommand(zshEnv, ig, r.Home, []string{"true"}, nil))
		rt.typeLine(t, "claude t")

		if want := []string{"--settings", rigSettings, "t"}; !slices.Equal(r.args(t, "real"), want) {
			t.Errorf("real run differs from %q", want)
		}

		if _, err := os.Stat(filepath.Join(r.Out, "wrap")); err != nil {
			t.Errorf("the wrapper did not run: %v", err)
		}
	})
}
