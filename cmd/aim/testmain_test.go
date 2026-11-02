package main

import (
	"os"
	"path/filepath"
	"testing"
)

// TestMain lets a copy of the test binary named claude run as the wrapper.
func TestMain(m *testing.M) {
	if filepath.Base(os.Args[0]) == shimName {
		os.Exit(runShim(os.Args[1:], os.Stderr))
	}

	os.Exit(m.Run())
}
