package main

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"syscall"

	"golang.org/x/sys/unix"

	"github.com/mhersson/aim/internal/harness"
	"github.com/mhersson/aim/internal/term"
)

// shimName is the name AIM's binary runs under as the claude wrapper.
const shimName = "claude"

// shimPlan is the exec the wrapper performs.
type shimPlan struct {
	path      string
	argv, env []string
}

// planShim finds the first claude on PATH that is not the wrapper itself and
// plans its exec, adding this instance's settings when the claude is a typed
// one that leads the pane's own terminal (own is given AIM_TTY). With self nil (its file is unknown) the wrapper
// is told apart by AIM_SHELL_DIR/bin instead.
func planShim(args, environ []string, self os.FileInfo, own func(paneTTY string) bool) (shimPlan, bool) {
	getenv := func(key string) (string, bool) {
		for _, e := range slices.Backward(environ) {
			if v, ok := strings.CutPrefix(e, key+"="); ok {
				return v, true
			}
		}

		return "", false
	}

	pathEnv, ok := getenv("PATH")
	if !ok {
		return shimPlan{}, false
	}

	shellBin := ""
	if dir, _ := getenv("AIM_SHELL_DIR"); dir != "" {
		shellBin = filepath.Join(dir, "bin")
	}

	if self == nil && shellBin == "" {
		return shimPlan{}, false
	}

	var (
		found string
		kept  []string
	)

	for _, dir := range filepath.SplitList(pathEnv) {
		candidate := filepath.Join(dir, shimName) // an empty entry is the cwd, as in a shell

		if self == nil && dir != "" && filepath.Clean(dir) == shellBin {
			continue
		}

		info, err := os.Stat(candidate)
		if err != nil || info.IsDir() || unix.Access(candidate, unix.X_OK) != nil {
			kept = append(kept, dir)

			continue
		}

		if self != nil && os.SameFile(info, self) {
			continue
		}

		kept = append(kept, dir)

		if found == "" {
			found = candidate
		}
	}

	if found == "" {
		return shimPlan{}, false
	}

	env := make([]string, len(environ))
	for i, e := range environ {
		if strings.HasPrefix(e, "PATH=") {
			e = "PATH=" + strings.Join(kept, string(filepath.ListSeparator))
		}

		env[i] = e
	}

	argv := append([]string{shimName}, shimArgs(args, getenv, own)...)

	return shimPlan{path: found, argv: argv, env: env}, true
}

// shimArgs returns args with this instance's settings in front when all the
// conditions hold; the cheap checks come first and the terminal probe last.
func shimArgs(args []string, getenv func(string) (string, bool), own func(paneTTY string) bool) []string {
	sock, _ := getenv("AIM_SOCK")

	settings, ok := settingsForSock(sock)
	if !ok {
		return args
	}

	paneTTY, _ := getenv("AIM_TTY")
	if paneTTY == "" {
		return args
	}

	if v, _ := getenv("CLAUDECODE"); v != "" {
		return args
	}

	if _, err := os.Stat(settings); err != nil {
		return args
	}

	with := harness.Claude{}.WithSettings(args, settings)
	if len(with) == len(args) || !own(paneTTY) {
		return args
	}

	return with
}

// runShim runs as claude: it execs the real claude and returns only when that
// fails, with the exit status a shell gives.
func runShim(args []string, stderr io.Writer) int {
	var self os.FileInfo

	if exe, err := os.Executable(); err == nil {
		if info, err := os.Stat(exe); err == nil {
			self = info
		}
	}

	own := func(paneTTY string) bool {
		ok, err := term.OwnsForeground(paneTTY)

		return err == nil && ok
	}

	plan, ok := planShim(args, os.Environ(), self, own)
	if !ok {
		_, _ = fmt.Fprintln(stderr, shimName+": command not found")

		return 127
	}

	err := syscall.Exec(plan.path, plan.argv, plan.env) //nolint:gosec // G204: the exec is the wrapper's purpose
	_, _ = fmt.Fprintf(stderr, "%s: %v\n", shimName, err)

	return 126
}
