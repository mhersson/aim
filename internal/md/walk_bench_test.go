//go:build bench

package md

import (
	"context"
	"os"
	"runtime"
	"testing"
	"time"
)

// TestWalkNumbers walks / and the home folder read-only and logs what the
// finder's walk costs here. It asserts nothing.
func TestWalkNumbers(t *testing.T) {
	home, _ := os.UserHomeDir()

	for _, root := range []string{"/", home} {
		if root == "" {
			continue
		}

		var files, dirs int

		ctx, cancel := context.WithTimeout(context.Background(), WalkMaxTime)
		opts := WalkOptions{Root: root, SkipAbs: WalkSkips(runtime.GOOS, home)}
		start := time.Now() // wall-clock: reporting the walk's duration
		stop := Walk(ctx, opts, func(b []WalkEntry) {
			for _, e := range b {
				if e.Dir {
					dirs++
				} else {
					files++
				}
			}
		})
		took := time.Since(start) // wall-clock: reporting the walk's duration

		cancel()
		t.Logf("%s: %v, %d files, %d folders, stop %d", root, took, files, dirs, stop)
	}
}
