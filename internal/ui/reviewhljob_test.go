package ui

import (
	"context"
	"fmt"
	"reflect"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/mhersson/aim/internal/core"
	"github.com/mhersson/aim/internal/git"
)

// applyLive hands the view res as a live result.
func applyLive(a *App, v *reviewView, res git.Result) {
	a.reviewApply(v, reviewLoadedMsg{tab: a.store.State().Tab().ID, res: res, live: true, info: reviewInfo{project: "multiplex", branch: "feat/review-pane", base: "origin/main", turns: 4, hidden: hiddenFlags(res.Files)}})
}

// Tokenising happens in a command: the drawer draws plain until its answer.
// It compares highlighted colours, so it runs serially (docs/gotchas.md).
func TestHighlightRunsOffTheLoop(t *testing.T) {
	a := cacheScene().build(t)
	v := a.reviewViewOf(a.store.State().Tab().ID)
	v.gen++
	v.rebuild(v.split)

	r := drawerRectOf(t, a, 0)
	lines := func() string { return strings.Join(a.drawerLines(a.store.State(), r), "\n") }
	plain := lines()

	cmd := a.drawerAfterUpdate()
	if cmd == nil || a.drawer.hlJob == nil {
		t.Fatalf("no batch was started: cmd %v, job %v", cmd != nil, a.drawer.hlJob != nil)
	}

	if lines() != plain {
		t.Error("the drawer changed before the batch answered")
	}

	scans := a.drawer.scans

	if again := a.drawerAfterUpdate(); again != nil || a.drawer.scans != scans {
		t.Errorf("a second settle with a batch running returned a command (%v) or scanned %d times", again != nil, a.drawer.scans-scans)
	}

	run(a, cmd)

	if a.drawer.hlJob != nil {
		t.Error("the batch is still running after its answer")
	}

	coloured := lines()
	if coloured == plain {
		t.Error("the answer did not colour the drawer")
	}

	v.cache, v.carry = reviewCache{}, nil

	settleDrawer(t, a)

	if lines() != coloured {
		t.Error("the answer coloured the drawer differently from a cold settle")
	}
}

// It compares highlighted spans, so it runs serially (docs/gotchas.md).
func TestHighlightAnswerFollowsItsView(t *testing.T) {
	noLexer := func() reviewScene {
		s := cacheScene()
		lines := []string{" \tone", "-\ttwo", "+\tthree", " \tfour"}
		s.res = git.Result{Old: "base", New: "tree", Live: true, Files: []git.FileDiff{fileSpec("notes.unknownext", git.Modified, hunkSpec(1, 1, "", lines...))}}
		s.turn, s.turns = 1, 1

		return s
	}

	tests := []struct {
		name   string
		scene  func() reviewScene
		change func(t *testing.T, a *App, v *reviewView)
		check  func(t *testing.T, a *App, v *reviewView)
	}{
		{"same text", cacheScene, func(t *testing.T, a *App, v *reviewView) {
			applyLive(a, v, turn4Result())
		}, func(t *testing.T, a *App, v *reviewView) {
			if got := settleCounting(t, a); got != 0 {
				t.Errorf("the same text was tokenised again: %d lines", got)
			}

			if len(spanTexts(v)) == 0 {
				t.Error("no chunk in view holds spans")
			}
		}},
		{"changed text", cacheScene, func(t *testing.T, a *App, v *reviewView) {
			before := map[spanKey][]string{}

			for i := range v.rows {
				for _, r := range v.rowSpans(i) {
					_, before[r.key] = v.chunkLines(r.key)
				}
			}

			res := turn4Result()
			res.Files[1].Hunks[0].Lines[3].Text = "\t// a comment where the code was"
			applyLive(a, v, res)

			differs := false

			for k, old := range before {
				path, lines := v.chunkLines(k)
				if !reflect.DeepEqual(a.drawer.hl.Highlight(path, old), a.drawer.hl.Highlight(path, lines)) {
					differs = true
				}
			}

			if !differs {
				t.Fatal("the edit leaves every chunk's spans as they were, so the case cannot fail")
			}
		}, func(t *testing.T, a *App, v *reviewView) {
			settleDrawer(t, a)

			spans := v.derived().spans
			if len(spans) == 0 {
				t.Fatal("no chunk holds spans")
			}

			for k, got := range spans {
				path, lines := v.chunkLines(k)
				if want := a.drawer.hl.Highlight(path, lines); !reflect.DeepEqual(got, want) {
					t.Errorf("chunk %v holds spans of other text", k)
				}
			}
		}},
		{"drawer closed", cacheScene, func(t *testing.T, a *App, v *reviewView) {
			openIn(t, a, core.CloseReview{})
			a.Update(struct{}{})
		}, func(t *testing.T, a *App, v *reviewView) {
			if n := len(v.derived().spans); n != 0 {
				t.Errorf("the dropped view's cache gained %d span entries from the answer", n)
			}

			tab := a.store.State().Tab().ID
			if a.drawer.views[tab] != nil {
				t.Fatal("the closed drawer still holds its view")
			}

			openIn(t, a, core.OpenReview{Pane: "p1", Mode: core.ReviewTurn})

			nv := a.reviewViewOf(tab)
			nv.pane, nv.mode, nv.loaded, nv.root = v.pane, v.mode, true, v.root
			nv.src, nv.res, nv.gen = v.src, v.res, 1
			nv.rebuild(false)

			if a.drawerAfterUpdate(); a.drawer.hlJob == nil || a.drawer.hlJob.view != nv {
				t.Error("the reopened drawer did not start its own batch")
			}
		}},
		{"no lexer", noLexer, func(t *testing.T, a *App, v *reviewView) {}, func(t *testing.T, a *App, v *reviewView) {
			var k spanKey

			for i := range v.rows {
				if refs := v.rowSpans(i); len(refs) > 0 {
					k = refs[0].key

					break
				}
			}

			got, ok := v.derived().spans[k]
			if !ok {
				t.Fatal("the chunk holds no entry, so it would be asked again")
			}

			path, lines := v.chunkLines(k)
			if want := a.drawer.hl.Highlight(path, lines); !reflect.DeepEqual(got, want) {
				t.Errorf("spans %v, want %v", got, want)
			}

			if cmd := a.drawerAfterUpdate(); cmd != nil || a.drawer.hlJob != nil || a.drawer.tokenised != 0 {
				t.Errorf("the first settle after the answer started work: cmd %v, job %v, tokenised %d", cmd != nil, a.drawer.hlJob != nil, a.drawer.tokenised)
			}

			scans := a.drawer.scans

			if cmd := a.drawerAfterUpdate(); cmd != nil || a.drawer.hlJob != nil {
				t.Error("a second settle started work")
			}

			if a.drawer.scans != scans {
				t.Errorf("a settled drawer scanned %d more times", a.drawer.scans-scans)
			}
		}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			a := tt.scene().build(t)
			v := a.reviewViewOf(a.store.State().Tab().ID)
			v.gen++
			v.rebuild(v.split)

			if a.drawerAfterUpdate() == nil || a.drawer.hlJob == nil {
				t.Fatal("no batch was started")
			}

			j := a.drawer.hlJob
			tt.change(t, a, v)

			msg, ok := j.run(context.Background(), a.drawer.hl).(drawerSpansMsg)
			if !ok {
				t.Fatal("the batch gave no answer")
			}

			a.onDrawerSpans(msg)

			if a.drawer.hlJob != nil {
				t.Error("the answer left a batch running")
			}

			tt.check(t, a, v)
		})
	}
}

func TestHighlightStopsWithTheApp(t *testing.T) {
	t.Parallel()

	a := cacheScene().build(t)
	v := a.reviewViewOf(a.store.State().Tab().ID)
	v.gen++
	v.rebuild(v.split)
	a.drawerAfterUpdate()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	if msg := a.drawer.hlJob.run(ctx, a.drawer.hl); msg != nil {
		t.Errorf("a cancelled batch answered %T", msg)
	}
}

// An answer that arrives while the view changes under it must leave every
// visible chunk with spans of its own text.
func TestHighlightJobRace(t *testing.T) {
	t.Parallel()

	a := cacheScene().build(t)
	v := a.reviewViewOf(a.store.State().Tab().ID)
	v.gen++
	v.rebuild(v.split)

	if a.drawerAfterUpdate() == nil {
		t.Fatal("no batch was started")
	}

	j, hl := a.drawer.hlJob, a.drawer.hl
	done := make(chan tea.Msg, 1)

	go func() { done <- j.run(context.Background(), hl) }()

	applyLive(a, v, edited())
	v.cursor += 3
	v.rebuild(!v.split)
	a.drawerLines(a.store.State(), drawerRectOf(t, a, 0))

	msg, ok := (<-done).(drawerSpansMsg)
	if !ok {
		t.Fatal("the batch gave no answer")
	}

	a.onDrawerSpans(msg)
	settleDrawer(t, a)

	spans := v.derived().spans
	top := v.viewTop(a.reviewBodyH(v))

	for i := top; i < min(top+a.reviewBodyH(v), len(v.rows)); i++ {
		for _, ref := range v.rowSpans(i) {
			if _, ok := spans[ref.key]; !ok {
				t.Errorf("visible row %d: chunk %v has no spans", i, ref.key)
			}
		}
	}
}

func TestHighlightWindowOrder(t *testing.T) {
	t.Parallel()

	span := func(from, to int) []int {
		var out []int

		step := 1
		if to < from {
			step = -1
		}

		for i := from; i != to+step; i += step {
			out = append(out, i)
		}

		return out
	}

	tests := []struct {
		name         string
		top, h, n    int
		up           bool
		visible, rst []int
	}{
		{"down", 100, 10, 1000, false, span(100, 109), append(span(110, 129), span(99, 90)...)},
		{"up", 100, 10, 1000, true, span(100, 109), append(span(99, 80), span(110, 119)...)},
		{"at the top", 0, 10, 25, false, span(0, 9), span(10, 24)},
		{"at the end", 20, 10, 25, false, span(20, 24), span(19, 10)},
		{"short rows", 0, 50, 20, false, span(0, 19), nil},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			visible, rest := hlWindow(tt.top, tt.h, tt.n, tt.up)
			if !reflect.DeepEqual(visible, tt.visible) || !reflect.DeepEqual(rest, tt.rst) {
				t.Errorf("hlWindow(%d, %d, %d, %v) = %v, %v; want %v, %v", tt.top, tt.h, tt.n, tt.up, visible, rest, tt.visible, tt.rst)
			}
		})
	}
}

// manyHunks is one added Go file of n small hunks, each a chunk of its own and
// shorter than a screen, so a batch holds many chunks and their order shows.
func manyHunks(n int) git.Result {
	hunks := make([]git.Hunk, n)

	for i := range hunks {
		specs := make([]string, 6)
		for j := range specs {
			specs[j] = fmt.Sprintf("+func f%d_%d() int { return %d } // chunk %d", i, j, i*j, i)
		}

		hunks[i] = hunkSpec(0, i*100+1, "", specs...)
	}

	return git.Result{Turn: 1, Files: []git.FileDiff{fileSpec("many.go", git.Added, hunks...)}}
}

func TestHighlightVisibleFirst(t *testing.T) {
	t.Parallel()

	s := reviewScene{theme: "catppuccin-mocha", icons: true, w: 200, h: 52, res: manyHunks(400), turn: 1, turns: 1, focused: true}
	a := s.build(t)
	v := a.reviewViewOf(a.store.State().Tab().ID)
	h := a.reviewBodyH(v)

	keysOf := func(from, to int) map[spanKey]bool {
		out := map[spanKey]bool{}

		for i := max(from, 0); i < min(to, len(v.rows)); i++ {
			for _, r := range v.rowSpans(i) {
				out[r.key] = true
			}
		}

		return out
	}
	jobKeys := func(j *hlJob) []spanKey {
		var out []spanKey

		for _, tx := range j.texts {
			out = append(out, tx.keys...)
		}

		return out
	}
	// ahead checks that job holds every key of want that held lacks, before any key outside want.
	ahead := func(t *testing.T, what string, job *hlJob, want, held map[spanKey]bool) {
		t.Helper()

		if job == nil {
			t.Fatalf("%s: no batch was started", what)
		}

		got := map[spanKey]bool{}
		other := false

		for _, k := range jobKeys(job) {
			got[k] = true

			switch {
			case want[k] && other:
				t.Errorf("%s: chunk %v comes after one outside the expected rows", what, k)
			case !want[k]:
				other = true
			}
		}

		for k := range want {
			if !held[k] && !got[k] {
				t.Errorf("%s: chunk %v of the expected rows is missing", what, k)
			}
		}
	}

	if len(v.rows) < 12*h {
		t.Fatalf("%d rows for a screen of %d: too few to look ahead in", len(v.rows), h)
	}

	at := func(row int) {
		v.top, v.cursor = row, row
		v.cache, v.carry = reviewCache{}, nil
	}

	at(len(v.rows) / 2)

	top := v.viewTop(h)

	a.drawerAfterUpdate()

	first := a.drawer.hlJob
	if first == nil {
		t.Fatal("no batch was started")
	}

	vis := keysOf(top, top+h)
	firstKeys := map[spanKey]bool{}

	for _, k := range jobKeys(first) {
		firstKeys[k] = true

		if !vis[k] {
			t.Errorf("the first batch holds %v, which no visible row reads", k)
		}
	}

	runHighlight(t, a)
	a.drawerAfterUpdate()
	ahead(t, "down", a.drawer.hlJob, keysOf(top+h, top+3*h), firstKeys)

	settleDrawer(t, a)

	inside := keysOf(top-h, top+3*h)
	for k := range inside {
		if _, ok := v.derived().spans[k]; !ok {
			t.Errorf("chunk %v inside the window holds no spans", k)
		}
	}

	for k := range v.derived().spans {
		if !inside[k] {
			t.Errorf("chunk %v outside the window holds spans", k)
		}
	}

	// A move up by 4h: the new window does not overlap the old one, so nothing it needs is held.
	v.top, v.cursor = top-4*h, top-4*h
	newTop := v.viewTop(h)

	a.drawerAfterUpdate()

	visible := a.drawer.hlJob
	if visible == nil {
		t.Fatal("no batch after moving up")
	}

	held := map[spanKey]bool{}
	for _, k := range jobKeys(visible) {
		held[k] = true
	}

	runHighlight(t, a)
	a.drawerAfterUpdate()
	ahead(t, "up", a.drawer.hlJob, keysOf(newTop-2*h, newTop), held)
}
