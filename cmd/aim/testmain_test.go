package main

import (
	"os"
	"path/filepath"
	"testing"
)

// TestMain lets a copy of the test binary named claude run as the wrapper, and
// the binary itself run AIM's hook and Codex status line subcommands.
func TestMain(m *testing.M) {
	if name := filepath.Base(os.Args[0]); name == shimName || name == "codex" {
		os.Exit(runShim(name, os.Args[1:], os.Stderr))
	}

	if len(os.Args) > 1 && os.Args[1] == "exec-owned-codex" {
		os.Exit(runShim("codex", os.Args[2:], os.Stderr))
	}

	if len(os.Args) > 1 && (os.Args[1] == "hook" || os.Args[1] == "codex-statusline") {
		main()

		return
	}

	os.Exit(m.Run())
}
