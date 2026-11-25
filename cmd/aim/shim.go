package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"syscall"
	"unicode"
	"unicode/utf8"

	"golang.org/x/sys/unix"

	"github.com/mhersson/aim/internal/core"
	"github.com/mhersson/aim/internal/harness"
	"github.com/mhersson/aim/internal/term"
)

// shimName is the name AIM's binary runs under as the claude wrapper.
const shimName = "claude"

// shimPlan is the exec the wrapper performs.
type shimPlan struct {
	owner     bool
	path      string
	argv, env []string
}

// The first executable after removing this instance's wrappers is resolved on
// the pane's PATH, including relative entries after a shell changes directory.
func planShim(name string, args, environ []string, self os.FileInfo, own func(paneTTY string) bool) (shimPlan, bool) {
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
		candidate := filepath.Join(dir, name) // an empty entry is the cwd, as in a shell

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

	owner := false

	if name == "codex" {
		settings, _ := getenv("AIM_CODEX_HOOKS")
		tty, _ := getenv("AIM_TTY")
		parent, _ := getenv("AIM_CODEX_OWNER")
		sock, _ := getenv("AIM_SOCK")
		pane, _ := getenv("AIM_PANE")
		interactive, private := term.CodexOptions(args)
		owner = settings != "" && sock != "" && pane != "" && tty != "" && parent == "" && interactive && own(tty)
		env = slices.DeleteFunc(env, func(kv string) bool { return strings.HasPrefix(kv, "AIM_CODEX_OWNER=") })

		if owner {
			prefix := (harness.Codex{}).Launch(settings, "")[1:]
			if private {
				prefix = prefix[1:]
			}

			if bin, _ := getenv("AIM_BIN"); bin != "" {
				if status, ok := codexStatusArgs(bin); ok {
					prefix = append(prefix, status...)
				}
			}

			args = append(prefix, args...)
			env = append(env, "AIM_CODEX_OWNER="+strconv.Itoa(os.Getpid()))
		}
	} else {
		args = shimArgs(args, getenv, own)
	}

	argv := append([]string{name}, args...)

	return shimPlan{owner: owner, path: found, argv: argv, env: env}, true
}

// codexStatusArgs returns the -c overrides that start AIM's status line
// provider. It refuses a path a TOML basic string cannot carry unchanged.
func codexStatusArgs(bin string) ([]string, bool) {
	if !utf8.ValidString(bin) || strings.ContainsFunc(bin, unicode.IsControl) {
		return nil, false
	}

	quoted := strings.ReplaceAll(strings.ReplaceAll(bin, `\`, `\\`), `"`, `\"`)

	return []string{
		"-c", "features.tui_extensions_statusline=true",
		"-c", `mcp_servers.aim={command="` + quoted + `",args=["codex-statusline"],env_vars=["AIM_SOCK","AIM_PANE","AIM_CODEX_OWNER","AIM_TTY"]}`,
		"-c", `tui.status_line_provider={server="aim",tool="statusline"}`,
	}, true
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

	if info, err := os.Lstat(settings); err != nil || !info.Mode().IsRegular() {
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
func runShim(name string, args []string, stderr io.Writer) int {
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

	plan, ok := planShim(name, args, os.Environ(), self, own)
	if !ok {
		_, _ = fmt.Fprintln(stderr, name+": command not found")

		return 127
	}

	if plan.owner {
		ctx, cancel := context.WithTimeout(context.Background(), hookBudget)
		_ = harness.Send(ctx, os.Getenv("AIM_SOCK"), harness.Event{Harness: "codex", Pane: core.PaneID(os.Getenv("AIM_PANE")), Name: harness.CodexOwnerStart, OwnerPID: os.Getpid(), OwnerTTY: os.Getenv("AIM_TTY")})

		cancel()
	}

	err := syscall.Exec(plan.path, plan.argv, plan.env) //nolint:gosec // G204: the exec is the wrapper's purpose
	_, _ = fmt.Fprintf(stderr, "%s: %v\n", name, err)

	return 126
}
