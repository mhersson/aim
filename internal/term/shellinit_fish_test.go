package term

import (
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func fishEnv(string) string {
	p, err := exec.LookPath("fish")
	if err != nil {
		return "/usr/bin/fish"
	}

	return p
}

// writeFish puts content in fish's config dir, which the rig keeps under XDG_CONFIG_HOME.
func writeFish(t *testing.T, rel, content string) {
	t.Helper()

	p := filepath.Join(os.Getenv("XDG_CONFIG_HOME"), "fish", rel)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil { //nolint:gosec // test temp dir
		t.Fatal(err)
	}

	if err := os.WriteFile(p, []byte(content), 0o644); err != nil { //nolint:gosec // test temp dir
		t.Fatal(err)
	}
}

// fishRecord is the fish-syntax startup line that appends name to $OUT_DIR/order.
func fishRecord(name string) string {
	return "echo " + name + ` >> "$OUT_DIR/order"` + "\n"
}

// fishToFile is a typed fish command whose output lands in $OUT_DIR/name.
func fishToFile(name, cmd string) string {
	return `begin; ` + cmd + `; end > "$OUT_DIR/` + name + `.tmp" 2>&1; mv "$OUT_DIR/` + name + `.tmp" "$OUT_DIR/` + name + `"`
}

const fishAddPath = "fish_add_path --prepend --move $HOME/.local/bin\n"

func TestFishIntegration(t *testing.T) {
	if _, err := exec.LookPath("fish"); err != nil {
		t.Skip("fish is not installed")
	}

	t.Run("fish_add_path and an alias", func(t *testing.T) {
		r := newShellRig(t)
		t.Setenv("XDG_DATA_DIRS", "/rig/data:/usr/share")

		writeFish(t, "conf.d/00.fish", fishRecord("conf.d")+`echo "$XDG_DATA_DIRS" > "$OUT_DIR/xdg"`+"\n")
		writeFish(t, "config.fish", fishRecord("config")+fishAddPath+"alias claude 'claude --dangerously-skip-permissions'\n")

		ig := r.integrate(t)

		rt := r.start(t, ShellCommand(fishEnv, ig, r.Home, nil))
		rt.typeLine(t, "claude a 'b c'")
		rt.typeLine(t, "command claude c")
		rt.typeLine(t, fishToFile("login", "status is-login; and echo login"))

		runs := r.records(t, "real", 2)
		if want := []string{"--settings", rigSettings, "--dangerously-skip-permissions", "a", "b c"}; !slices.Equal(runs[0], want) {
			t.Errorf("first real run = %q, want %q", runs[0], want)
		}

		if want := []string{"--settings", rigSettings, "c"}; !slices.Equal(runs[1], want) {
			t.Errorf("second real run = %q, want %q", runs[1], want)
		}

		if got, want := r.lines("order"), []string{"conf.d", "config"}; !slices.Equal(got, want) {
			t.Errorf("startup order = %q, want %q", got, want)
		}

		if got, want := r.lines("xdg"), []string{"/rig/data:/usr/share"}; !slices.Equal(got, want) {
			t.Errorf("conf.d saw XDG_DATA_DIRS %q, want %q", got, want)
		}

		if got := strings.TrimSpace(r.await(t, "login")); got != "login" {
			t.Errorf("login check = %q, want login", got)
		}
	})

	t.Run("set -gx PATH", func(t *testing.T) {
		r := newShellRig(t)

		writeFish(t, "config.fish", "set -gx PATH ~/.local/bin $PATH\n")

		ig := r.integrate(t)

		rt := r.start(t, ShellCommand(fishEnv, ig, r.Home, nil))
		rt.typeLine(t, "claude s")

		if want := []string{"--settings", rigSettings, "s"}; !slices.Equal(r.args(t, "real"), want) {
			t.Errorf("real run differs from %q", want)
		}
	})

	t.Run("an autoloaded user function", func(t *testing.T) {
		r := newShellRig(t)

		writeFish(t, "config.fish", fishAddPath)
		writeFish(t, "functions/claude.fish", "function claude\n    command claude --autoload $argv\nend\n")

		ig := r.integrate(t)

		rt := r.start(t, ShellCommand(fishEnv, ig, r.Home, nil))
		rt.typeLine(t, "claude z")

		if want := []string{"--settings", rigSettings, "--autoload", "z"}; !slices.Equal(r.args(t, "real"), want) {
			t.Errorf("real run differs from %q", want)
		}
	})

	t.Run("nothing leaks", func(t *testing.T) {
		r := newShellRig(t)
		ig := r.integrate(t)

		rt := r.start(t, ShellCommand(fishEnv, ig, r.Home, nil))
		rt.typeLine(t, fishToFile("leak", "set -q __aim_dir __aim_bin __aim_path; or echo clean"))

		if got := strings.TrimSpace(r.await(t, "leak")); got != "clean" {
			t.Errorf("leak check = %q, want clean", got)
		}
	})

	t.Run("the shell after an agent", func(t *testing.T) {
		r := newShellRig(t)

		writeFish(t, "config.fish", fishAddPath)

		ig := r.integrate(t)

		rt := r.start(t, AgentCommand(fishEnv, ig, r.Home, []string{"true"}, nil))
		rt.typeLine(t, "claude t")

		if want := []string{"--settings", rigSettings, "t"}; !slices.Equal(r.args(t, "real"), want) {
			t.Errorf("real run differs from %q", want)
		}
	})

	t.Run("the wrapper gone", func(t *testing.T) {
		r := newShellRig(t)

		writeFish(t, "config.fish", fishAddPath)

		ig := r.integrate(t)

		rt := r.start(t, ShellCommand(fishEnv, ig, r.Home, nil))
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

		for _, bad := range []string{"No such file", "not found", "Permission denied", "Unknown command"} {
			if strings.Contains(rt.output(), bad) {
				t.Errorf("terminal output has %q", bad)
			}
		}
	})
}
