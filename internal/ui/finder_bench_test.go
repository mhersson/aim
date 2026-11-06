//go:build bench

package ui

import (
	"fmt"
	"strings"
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

// bigMarkdown is a document just under md.MaxBytes: headings, prose with
// inline marks and links, lists and code, the shapes that cost to parse.
func bigMarkdown() string {
	const block = "## Section\n\nSome *prose* with **bold**, `code` and a [link](https://example.com/page) that wraps over " +
		"more than one line of a narrow column, so the layout has work to do.\n\n- one\n- two\n- three\n\n" +
		"```go\nfunc main() {\n\tfmt.Println(\"hello\")\n}\n```\n\n"

	return strings.Repeat(block, (md.MaxBytes-1024)/len(block))
}

// TestFinderPreviewMove moves the cursor down a list of files that holds a
// document near md.MaxBytes and measures every frame on the loop: the key, which
// keeps the old preview on show, and the frame the new preview lands in. The
// read, parse and layout run between frames, off the loop in the app, so they
// are outside the measure and are timed on their own. Run it with:
//
//	go test -tags bench -run TestFinderPreviewMove -v ./internal/ui
func TestFinderPreviewMove(t *testing.T) {
	tree := map[string]string{"proj/big.md": bigMarkdown()}
	for i := range 40 {
		tree[fmt.Sprintf("proj/f%02d.md", i)] = fmt.Sprintf("# File %d\n\n%s", i, strings.Repeat("A line of text in the file.\n\n", 40))
	}

	r := newFinderRig(t, "proj", tree)
	r.listed(t)

	f := r.finder

	var (
		frames  frameTimes
		offLoop time.Duration
		bigAt   time.Duration
		reads   int
	)

	frame := func(msg tea.Msg) tea.Cmd {
		start := time.Now()
		_, cmd := r.Update(msg)
		_ = r.View()

		frames.add(time.Since(start))

		return cmd
	}

	for range 3 * len(f.rows) {
		cmd := frame(keyDown)

		big := f.rows[f.sel].rel == "big.md"
		start := time.Now()
		msgs := flatten(cmd)
		took := time.Since(start)

		for _, m := range msgs {
			if pm, ok := m.(finderPreviewMsg); ok {
				reads++
				offLoop += took

				if big {
					bigAt = max(bigAt, took)
				}

				frame(pm)
			}
		}
	}

	s := frames.summary()
	t.Logf("%d frames over %d previews: p50 %s, p95 %s, p99 %s (headroom %s), max %s (budget %s)",
		s.Count, reads, fmtMS(s.P50), fmtMS(s.P95), fmtMS(s.P99), fmtMS(p99Headroom), fmtMS(s.Max), fmtMS(frameBudget))
	t.Logf("off the loop: %s a preview on average, %s for the %d KiB document (read, parse and layout)",
		fmtMS(offLoop/time.Duration(max(reads, 1))), fmtMS(bigAt), len(tree["proj/big.md"])>>10)

	gateFrames(t, "finder preview move", s.P99, s.Max)
}

// TestFinderOpenBig opens a document near md.MaxBytes from the finder and
// reloads it after a change, and measures the frame each answer lands in on the
// loop. The read, parse and layout run in the command, outside the measure.
// Run it with:
//
//	go test -tags bench -run TestFinderOpenBig -v ./internal/ui
func TestFinderOpenBig(t *testing.T) {
	big := bigMarkdown()
	r := newFinderRig(t, "proj", map[string]string{"proj/big.md": big})
	r.listed(t)
	r.finderSelect(t, "big.md")

	var (
		frames  frameTimes
		offLoop time.Duration
		landed  int
	)

	land := func(what string, msg tea.Msg) {
		start := time.Now()

		r.Update(msg)

		upd := time.Since(start)
		_ = r.View()

		frames.add(time.Since(start))
		t.Logf("%s: Update %s, with View %s", what, fmtMS(upd), fmtMS(time.Since(start)))

		landed++
	}

	for i := range 10 {
		r.press(keyEsc) // the reading box, if any, then the finder
		r.mdOpen()
		r.finderSelect(t, "big.md")

		start := time.Now()
		msg := r.openFinderFile(r.at("proj/big.md"))()
		offLoop += time.Since(start)

		land("open", msg)

		if r.plan == nil {
			t.Fatalf("round %d: no reading box (notice %q)", i, r.notice)
		}

		write(t, r.at("proj/big.md"), strings.Replace(big, "Section", fmt.Sprintf("Part%d", i), 1))

		start = time.Now()
		msg = r.mdCheck(r.plan)()
		offLoop += time.Since(start)

		land("reload", msg)
	}

	s := frames.summary()
	t.Logf("%d KiB document, %d answers: p50 %s, p95 %s, p99 %s (headroom %s), max %s (budget %s)",
		len(big)>>10, landed, fmtMS(s.P50), fmtMS(s.P95), fmtMS(s.P99), fmtMS(p99Headroom), fmtMS(s.Max), fmtMS(frameBudget))
	t.Logf("off the loop: %s an answer on average (read, parse and layout)", fmtMS(offLoop/time.Duration(landed)))

	gateFrames(t, "finder open big", s.P99, s.Max)
}
