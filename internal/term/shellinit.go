package term

import (
	"embed"
	"fmt"
	"os"
	"path/filepath"
)

//go:embed shellinit
var shellInitFS embed.FS

// scripts maps each embedded script to its place under the integration dir.
var scripts = []struct{ embedded, path string }{
	{"shellinit/zshenv", "zsh/.zshenv"},
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

	for _, s := range scripts {
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

// tail is the script's tail that leaves the pane in a login shell.
func (ig Integration) tail(sh string) string {
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

	var path, zdot string

	var hasZdot bool

	for _, kv := range base {
		switch k, v, _ := cutEnv(kv); k {
		case "PATH":
			path = v
		case "ZDOTDIR":
			zdot, hasZdot = v, true
		}
	}

	zsh := ig.Zsh && filepath.Base(sh) == "zsh"
	drop := []string{"SHELL"}

	if path != "" {
		drop = append(drop, "PATH")
	}

	if zsh && shellRoot {
		drop = append(drop, "ZDOTDIR")
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

	return append(out, extra...)
}
