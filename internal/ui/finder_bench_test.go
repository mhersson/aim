//go:build bench

package ui

import (
	"fmt"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/mhersson/aim/internal/md"
)

// benchNames is n markdown paths shaped like a home folder's: a folder, a
// subfolder and a file named for one of a few topics.
func benchNames(n int) []md.WalkEntry {
	topics := []string{"architecture", "guide", "readme", "changelog", "notes", "design", "plan", "review"}
	out := make([]md.WalkEntry, n)

	for i := range out {
		out[i] = md.WalkEntry{Rel: fmt.Sprintf("project%03d/docs%02d/%s-%05d.md", i%250, i%40, topics[i%len(topics)], i)}
	}

	return out
}

// TestFinderTyping types and erases a query over about 100,000 names on the
// update loop and measures every frame: the key, the batch that lands while
// typing and each filter answer. The filter itself runs between frames, off the
// loop in the app, so it is outside the measure. Run it with:
//
//	go test -tags bench -run TestFinderTyping -v ./internal/ui
func TestFinderTyping(t *testing.T) {
	r := newFinderRig(t, "proj", finderTree)
	r.listed(t)
	r.typeKeys("x")
	r.press(keyBackspace)

	f := r.finder

	const seeded = 100_000

	names := benchNames(seeded + 40_000)
	for i := 0; i < seeded; i += 4096 {
		r.Update(finderWalkMsg{view: f, seq: f.walk.seq, batch: names[i:min(i+4096, seeded)]})
	}

	var (
		frames   frameTimes
		filterNs time.Duration
		filters  int
		fed      = seeded
	)

	frame := func(msg tea.Msg) tea.Cmd {
		start := time.Now()
		_, cmd := r.Update(msg)
		_ = r.View()

		frames.add(time.Since(start))

		return cmd
	}

	var land func(cmd tea.Cmd)

	land = func(cmd tea.Cmd) {
		start := time.Now()
		msgs := flatten(cmd)
		filterNs += time.Since(start)

		for _, m := range msgs {
			if fm, ok := m.(finderFilterMsg); ok {
				filters++

				land(frame(fm))
			}
		}
	}

	strike := func(k tea.KeyPressMsg) {
		land(frame(k))

		// A batch lands while the operator types, as the walk's do.
		if fed+1000 <= len(names) {
			land(frame(finderWalkMsg{view: f, seq: f.walk.seq, batch: names[fed : fed+1000]}))
			fed += 1000
		}
	}

	var keys []tea.KeyPressMsg
	for _, c := range "architecture 04" {
		keys = append(keys, keyRune(c))
	}

	for range 8 {
		for _, k := range keys {
			strike(k)
		}

		for range keys {
			strike(keyBackspace)
		}
	}

	s := frames.summary()
	t.Logf("%d names, %d frames over %d filters (%d rows at the end): p50 %s, p95 %s, p99 %s (headroom %s), max %s (budget %s)",
		fed, s.Count, filters, len(f.rows), fmtMS(s.P50), fmtMS(s.P95), fmtMS(s.P99), fmtMS(p99Headroom), fmtMS(s.Max), fmtMS(frameBudget))

	if filters > 0 {
		t.Logf("off the loop: %s a filter on average", fmtMS(filterNs/time.Duration(filters)))
	}

	gateFrames(t, "finder typing", s.P99, s.Max)
}
