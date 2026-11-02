//go:build bench

package main

import (
	"fmt"
	"path/filepath"
	"slices"
	"testing"
	"time"
)

// p95 returns the 95th percentile of ds.
func p95(ds []time.Duration) time.Duration {
	slices.Sort(ds)

	return ds[(len(ds)*95+99)/100-1]
}

// TestBenchEditHold times the hold of an edit's before-event in a worktree the
// turn already started: the same path again, and new paths.
func TestBenchEditHold(t *testing.T) {
	repo := seededRepo(t)
	wt := linkedWorktree(t, repo)

	h, err := setupHooks(discardLogger(), shortDir(t), "", testWatches(t), testPlaces(), false)
	if err != nil {
		t.Fatal(err)
	}

	t.Cleanup(h.Stop)

	sendTurn(t, h.Sock, repo, gateSession)

	timed := func(path string) time.Duration {
		start := time.Now()

		if err := sendEdit(t.Context(), h.Sock, repo, path); err != nil {
			t.Fatalf("Send(edit) error = %v", err)
		}

		return time.Since(start)
	}

	// The first edit takes the start; it is the warm-up.
	timed(filepath.Join(wt, "warm.go"))

	same, fresh := make([]time.Duration, 50), make([]time.Duration, 50)

	for i := range same {
		same[i] = timed(filepath.Join(wt, "warm.go"))
	}

	for i := range fresh {
		fresh[i] = timed(filepath.Join(wt, fmt.Sprintf("new%d.go", i)))
	}

	t.Logf("p95 same path %v, new paths %v", p95(same), p95(fresh))

	if got := p95(same); got >= 10*time.Millisecond {
		t.Errorf("p95 of a cached path = %v, want under 10ms", got)
	}

	if got := p95(fresh); got >= 100*time.Millisecond {
		t.Errorf("p95 of new paths = %v, want under 100ms", got)
	}
}
