package ui

import (
	"fmt"
	"os"
	"testing"
)

// TestMain fails the run when a test starts git: the UI's tests use the fake
// in fakegit_test.go, and real git is tested in internal/git.
func TestMain(m *testing.M) {
	check := installGitGuard()

	code := m.Run()

	if report := check(); report != "" {
		fmt.Fprintln(os.Stderr, report)

		if code == 0 {
			code = 1
		}
	}

	os.Exit(code)
}
