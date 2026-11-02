//go:build !bench

package ui

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// guardProbe is the argument TestGitGuardStopsGit starts git with; the guard's
// report leaves that line out.
const guardProbe = "aim-guard-probe"

// installGitGuard puts a failing git first on PATH. check returns the git
// starts the tests made, prefixed by a header, or "", and removes the guard.
// It writes the script before any test runs, so no test races its exec.
func installGitGuard() (check func() string) {
	dir, err := os.MkdirTemp("", "ui-gitguard-")
	if err != nil {
		panic(err)
	}

	bin, logPath := filepath.Join(dir, "bin"), filepath.Join(dir, "log")
	if err := os.Mkdir(bin, 0o700); err != nil {
		panic(err)
	}

	script := "#!/bin/sh\nprintf '%s\\t%s\\n' \"$PWD\" \"$*\" >> '" + logPath + "'\n" +
		"echo 'internal/ui tests must not start git; use the fake (fakegit_test.go)' >&2\nexit 97\n"
	if err := os.WriteFile(filepath.Join(bin, "git"), []byte(script), 0o700); err != nil {
		panic(err)
	}

	if err := os.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH")); err != nil {
		panic(err)
	}

	return func() string {
		defer func() { _ = os.RemoveAll(dir) }()

		data, err := os.ReadFile(logPath)
		if err != nil {
			return ""
		}

		var lines []string

		for line := range strings.SplitSeq(strings.TrimRight(string(data), "\n"), "\n") {
			if !strings.HasSuffix(line, "\t"+guardProbe) {
				lines = append(lines, line)
			}
		}

		if len(lines) == 0 {
			return ""
		}

		return "internal/ui tests started git:\n" + strings.Join(lines, "\n")
	}
}

func TestGitGuardStopsGit(t *testing.T) {
	t.Parallel()

	err := exec.Command("git", guardProbe).Run()

	var ee *exec.ExitError
	if !errors.As(err, &ee) || ee.ExitCode() != 97 {
		t.Fatalf("git %s error = %v, want exit code 97", guardProbe, err)
	}
}
