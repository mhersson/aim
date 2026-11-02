package term

import (
	"embed"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

//go:embed shellinit
var shellInitFS embed.FS

type script struct{ embedded, path string }

// scripts maps each embedded script to its place under the integration dir.
func scripts() []script {
	return []script{
		{"shellinit/zshenv", "zsh/.zshenv"},
		{"shellinit/init.bash", "bash/init.bash"},
		{"shellinit/init.fish", "fish/init.fish"},
	}
}

// Integration is the per-instance shell integration. The zero value is off.
type Integration struct {
	// Dir is the instance's integration dir; empty means off.
	Dir string
	// Zsh means zsh panes take AIM's ZDOTDIR; with no user startup files zsh
	// gets the wrapper on PATH only.
	Zsh bool
}

// NewIntegration is the integration for dir. getenv reads the environment the
// panes inherit. An empty dir gives the zero Integration.
func NewIntegration(dir string, getenv func(string) string) Integration {
	if dir == "" {
		return Integration{}
	}

	zdot := getenv("ZDOTDIR")
	if zdot == "" {
		zdot = getenv("HOME")
	}

	ig := Integration{Dir: dir}
	if zdot == "" {
		return ig
	}

	for _, f := range []string{".zshenv", ".zprofile", ".zshrc", ".zlogin"} {
		if _, err := os.Stat(filepath.Join(zdot, f)); err == nil {
			ig.Zsh = true

			break
		}
	}

	return ig
}

// WriteIntegration replaces dir with a fresh integration dir: every script
// (0600) in 0700 subdirs, and bin/claude as a symlink to wrapper. On error it
// removes dir again.
func WriteIntegration(dir, wrapper string) (err error) {
	if dir == "" {
		return errors.New("write integration: empty dir")
	}

	defer func() {
		if err != nil {
			_ = os.RemoveAll(dir)
		}
	}()

	if err := os.RemoveAll(dir); err != nil {
		return fmt.Errorf("clear integration dir: %w", err)
	}

	if err := os.MkdirAll(filepath.Join(dir, "bin"), 0o700); err != nil {
		return fmt.Errorf("create integration dir: %w", err)
	}

	for _, s := range scripts() {
		data, err := shellInitFS.ReadFile(s.embedded)
		if err != nil {
			return fmt.Errorf("read %s: %w", s.embedded, err)
		}

		p := filepath.Join(dir, s.path)
		if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
			return fmt.Errorf("create %s: %w", filepath.Dir(p), err)
		}

		if err := os.WriteFile(p, data, 0o600); err != nil {
			return fmt.Errorf("write %s: %w", p, err)
		}
	}

	if err := os.Symlink(wrapper, filepath.Join(dir, "bin", "claude")); err != nil {
		return fmt.Errorf("link wrapper: %w", err)
	}

	return nil
}

// zshTail is the agent and editor script's tail for a zsh with the integration.
const zshTail = `; if [ -r "$AIM_SHELL_DIR/zsh/.zshenv" ]; then export ZDOTDIR="$AIM_SHELL_DIR/zsh"; fi; exec "$SHELL" -l`

// bashTail is the tail for a bash that reads ENV: the next shell is a posix-mode
// one that reads AIM's init.bash.
const bashTail = `; if [ -r "$AIM_SHELL_DIR/bash/init.bash" ]; then export ENV="$AIM_SHELL_DIR/bash/init.bash"; exec "$SHELL" --posix -l; fi; exec "$SHELL" -l`

// fishInit is what fish's -C runs: it sources AIM's init.fish when it is there.
const fishInit = `test -n "$AIM_SHELL_DIR"; and test -r "$AIM_SHELL_DIR/fish/init.fish"; and source "$AIM_SHELL_DIR/fish/init.fish"`

// fishTail is the tail for a fish with the integration: the next shell runs
// init.fish through -C, after the user's conf.d and config.fish.
const fishTail = `; exec "$SHELL" -l -C '` + fishInit + `'`

// tail is the script's tail that leaves the pane in a login shell.
func (ig Integration) tail(sh string) string {
	if ig.fish(sh) {
		return fishTail
	}

	if ig.bash(sh) {
		return bashTail
	}

	if ig.Dir != "" && ig.Zsh && filepath.Base(sh) == "zsh" {
		return zshTail
	}

	return `; exec "$SHELL" -l`
}

// env is the pane environment for sh: base with the integration's entries.
// shellRoot is true for the plain login shell, where zsh takes AIM's ZDOTDIR.
func (ig Integration) env(base []string, sh string, shellRoot bool, extra []string) []string {
	if ig.Dir == "" {
		return append(append(dropKeys(base, "SHELL"), "SHELL="+sh), extra...)
	}

	var path, zdot, inheritedEnv string

	var hasZdot, hasInhEnv bool

	for _, kv := range base {
		switch k, v, _ := strings.Cut(kv, "="); k {
		case "PATH":
			path = v
		case "ZDOTDIR":
			zdot, hasZdot = v, true
		case "ENV":
			inheritedEnv, hasInhEnv = v, true
		}
	}

	zsh := ig.Zsh && filepath.Base(sh) == "zsh"
	bash := ig.bash(sh)
	drop := []string{"SHELL"}

	if path != "" {
		drop = append(drop, "PATH")
	}

	if zsh && shellRoot {
		drop = append(drop, "ZDOTDIR")
	}

	if bash && shellRoot {
		drop = append(drop, "ENV")
	}

	out := append(dropKeys(base, drop...), "SHELL="+sh)

	if path != "" {
		out = append(out, "PATH="+filepath.Join(ig.Dir, "bin")+":"+path)
	}

	out = append(out, "AIM_SHELL_DIR="+ig.Dir)

	if zsh && hasZdot {
		out = append(out, "AIM_ZDOTDIR="+zdot)
	}

	if zsh && shellRoot {
		out = append(out, "ZDOTDIR="+filepath.Join(ig.Dir, "zsh"))
	}

	if bash && hasInhEnv {
		out = append(out, "AIM_ENV="+inheritedEnv)
	}

	if bash && shellRoot {
		out = append(out, "ENV="+filepath.Join(ig.Dir, "bash", "init.bash"))
	}

	return append(out, extra...)
}

// bashReadsENV is false for Apple's /bin/bash 3.2, which ignores ENV.
func bashReadsENV(goos, sh string) bool { return goos != "darwin" || sh != "/bin/bash" }

// bash is whether sh is a bash that takes the integration through ENV.
func (ig Integration) bash(sh string) bool {
	return ig.Dir != "" && filepath.Base(sh) == "bash" && bashReadsENV(runtime.GOOS, sh)
}

// fish is whether sh is a fish that takes the integration through -C.
func (ig Integration) fish(sh string) bool {
	return ig.Dir != "" && filepath.Base(sh) == "fish"
}
