package main

import (
	"os"
	"path/filepath"
	"strconv"
	"testing"
)

func TestSweepSent(t *testing.T) {
	t.Parallel()

	base := t.TempDir()
	names := []string{"100", "200", "notapid"}

	for _, n := range names {
		if err := os.MkdirAll(filepath.Join(base, n), 0o700); err != nil {
			t.Fatal(err)
		}
	}

	sweepSent(discardLogger(), base, func(pid int) bool { return pid == 200 })

	want := map[string]bool{"100": false, "200": true, "notapid": true}
	for n, keep := range want {
		_, err := os.Stat(filepath.Join(base, n))
		if (err == nil) != keep {
			t.Errorf("%s kept = %v, want %v", n, err == nil, keep)
		}
	}
}

func TestSentDir(t *testing.T) {
	t.Parallel()

	if got := sentDir("", 7); got != "" {
		t.Errorf("sentDir(\"\") = %q, want empty", got)
	}

	if got, want := sentDir("/c", 7), filepath.Join("/c", "sent", strconv.Itoa(7)); got != want {
		t.Errorf("sentDir = %q, want %q", got, want)
	}
}
