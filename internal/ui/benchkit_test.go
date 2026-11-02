package ui

import (
	"context"
	"fmt"
	"path"
	"reflect"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/x/ansi"

	"github.com/mhersson/aim/internal/core"
	"github.com/mhersson/aim/internal/term/rec"
)

func ms(n int) time.Duration { return time.Duration(n) * time.Millisecond }

func recs(cols, rows int, chunks ...rec.Record) []rec.Record {
	return append([]rec.Record{{Cols: cols, Rows: rows}}, chunks...)
}

func goroutineStacks() string {
	buf := make([]byte, 1<<20)

	return string(buf[:runtime.Stack(buf, true)])
}

func synthBenchConfig() benchConfig {
	return benchConfig{
		Panes: 2, Recordings: [][]rec.Record{rec.Synth(40, 16, 5*time.Second, 1)},
		Width: 82, Height: 20, Stagger: ms(500), Length: 3 * time.Second, FPS: 60,
	}
}

func holdBenchConfig(trace func(at time.Duration, drew bool)) benchConfig {
	r := recs(20, 5,
		rec.Record{At: ms(100), Data: []byte("\x1b[?2026hA")},
		rec.Record{At: ms(400), Data: []byte("\x1b[?2026l")},
		rec.Record{At: ms(600), Data: []byte("\x1b[?2026hB")},
		rec.Record{At: 3 * time.Second, Data: []byte("\x1b[0m")},
	)

	return benchConfig{
		Panes: 1, Recordings: [][]rec.Record{r}, Width: 40, Height: 10, Length: 3 * time.Second, FPS: 60,
		Trace: trace,
	}
}

func TestBenchKitOnSynth(t *testing.T) {
	res, err := runBench(synthBenchConfig())
	if err != nil {
		t.Fatal(err)
	}

	if res.Frames.Count == 0 || res.CPUPercent <= 0 || res.BytesFed == 0 {
		t.Fatalf("frames=%d cpu=%v bytes=%d, want all positive", res.Frames.Count, res.CPUPercent, res.BytesFed)
	}

	if res.Frames.Count > res.Steps {
		t.Errorf("frames=%d exceed steps=%d", res.Frames.Count, res.Steps)
	}

	if res.Width != 82 || res.Height != 20 || len(res.PaneSizes) != 2 {
		t.Errorf("result = %dx%d with %d pane sizes", res.Width, res.Height, len(res.PaneSizes))
	}

	// Pane read loops end with the bench's app.
	eventually(t, "pane read loops to end", func() bool { return !strings.Contains(goroutineStacks(), "(*pane).readLoop") })
}

func TestBenchKitNoFrameWithoutBytes(t *testing.T) {
	t.Parallel()

	r := recs(20, 5,
		rec.Record{At: ms(100), Data: []byte("x")},
		rec.Record{At: ms(1000), Data: []byte("y")},
		rec.Record{At: ms(2000), Data: []byte("z")},
	)

	res, err := runBench(benchConfig{
		Panes: 1, Recordings: [][]rec.Record{r}, Width: 40, Height: 10, Length: 2 * time.Second, FPS: 60,
	})
	if err != nil {
		t.Fatal(err)
	}

	if res.Updates != 2 {
		t.Fatalf("updates=%d, want 2 (idle panes must not be updated)", res.Updates)
	}

	if res.Steps != 120 || res.Frames.Count != 2 {
		t.Fatalf("steps=%d frames=%d, want 120 and 2", res.Steps, res.Frames.Count)
	}
}

func TestBenchKitHoldUsesVirtualClock(t *testing.T) {
	t.Parallel()

	var draws []time.Duration

	_, err := runBench(holdBenchConfig(func(at time.Duration, drew bool) {
		if drew {
			draws = append(draws, at)
		}
	}))
	if err != nil {
		t.Fatal(err)
	}

	if len(draws) != 2 {
		t.Fatalf("draws at %v, want exactly two", draws)
	}

	if draws[0] <= ms(400) || draws[0] > ms(417) {
		t.Errorf("first draw at %v, want in (400ms, 417ms]", draws[0])
	}

	if draws[1] < ms(1600) || draws[1] > ms(1650) {
		t.Errorf("second draw at %v, want in [1600ms, 1650ms]", draws[1])
	}
}

func TestBenchKitConfigErrors(t *testing.T) {
	t.Parallel()

	short := recs(20, 5, rec.Record{At: ms(500), Data: []byte("x")})

	tests := []struct {
		name string
		cfg  benchConfig
		want string // a value the error names; any error when empty
	}{
		{"no recordings", benchConfig{Panes: 1, Width: 40, Height: 10, Length: ms(100), FPS: 60}, ""},
		{"too many panes", benchConfig{Panes: 7, Recordings: [][]rec.Record{short}, Width: 40, Height: 10, Length: ms(100), FPS: 60}, ""},
		{"too short", benchConfig{
			Panes: 2, Recordings: [][]rec.Record{short}, Width: 40, Height: 10, Length: ms(400), Stagger: ms(200), FPS: 60,
		}, "pane 1"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := runBench(tt.cfg); err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("err = %v, want one naming %q", err, tt.want)
			}
		})
	}
}

func TestSummaryNearestRank(t *testing.T) {
	t.Parallel()

	var f frameTimes

	for i := 100; i >= 1; i-- {
		f.add(time.Duration(i) * time.Millisecond)
	}

	s := f.summary()
	if s.Count != 100 || s.P50 != ms(50) || s.P95 != ms(95) || s.P99 != ms(99) || s.Max != ms(100) {
		t.Fatalf("summary = %+v", s)
	}

	if (&frameTimes{}).summary() != (summary{}) {
		t.Fatal("empty summary is not the zero value")
	}
}

// A frame bench fails only on a frame over the budget; a p99 over the
// headroom is noted and never fails it.
func TestFrameGate(t *testing.T) {
	t.Parallel()

	us := func(n int) time.Duration { return time.Duration(n) * time.Microsecond }

	tests := []struct {
		name       string
		p99, worst time.Duration
		fail, note bool
	}{
		{"both within", ms(6), ms(12), false, false},
		{"p99 at the headroom, max at the budget", ms(8), us(16700), false, false},
		{"p99 over the headroom", us(8001), ms(14), false, true},
		{"p99 far over the headroom", us(13460), us(14400), false, true},
		{"max over the budget", ms(7), us(16701), true, false},
		{"both over", ms(13), ms(20), true, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			fail, note := frameGate("frames", tt.p99, tt.worst)
			if (fail != "") != tt.fail || (note != "") != tt.note {
				t.Fatalf("frameGate(p99 %v, max %v) = fail %q, note %q; want a failure %v, a note %v",
					tt.p99, tt.worst, fail, note, tt.fail, tt.note)
			}
		})
	}
}

// The six-pane bench gates the median of its runs' worst frames, so one run's
// spike does not decide it.
func TestMedianMax(t *testing.T) {
	t.Parallel()

	runs := func(maxes ...int) []benchResult {
		rs := make([]benchResult, len(maxes))
		for i, m := range maxes {
			rs[i].Frames.Max = ms(m)
		}

		return rs
	}

	tests := []struct {
		name string
		runs []benchResult
		want time.Duration
	}{
		{"one run", runs(14), ms(14)},
		{"one spike of three", runs(12, 30, 13), ms(13)},
		{"two over of three", runs(20, 13, 18), ms(18)},
		{"even count takes the upper middle", runs(15, 11, 12, 14), ms(14)},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if got := medianMax(tt.runs); got != tt.want {
				t.Fatalf("medianMax = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestBenchSelectionHighlightsEveryRowOfOnePane(t *testing.T) {
	t.Parallel()

	cfg := benchConfig{Panes: 2, Width: 82, Height: 20}

	app, ids, err := newBenchApp(cfg, &fakeClock{t: time.Unix(1_000_000, 0)})
	if err != nil {
		t.Fatal(err)
	}

	t.Cleanup(func() { _ = app.Close(context.Background()) })

	selectWholePane(app, ids[0])

	in, out := contentRectOf(app, ids[0]), contentRectOf(app, ids[1])

	for y := in.Y; y < in.Y+in.H; y++ {
		for _, x := range []int{in.X, in.X + in.W - 1} {
			if !selBgAt(app, x, y) {
				t.Errorf("selected pane cell (%d,%d) is not highlighted", x, y)
			}
		}
	}

	if selBgAt(app, out.X, out.Y) {
		t.Error("the other pane is highlighted")
	}
}

func TestBenchKitRunsWithASelection(t *testing.T) {
	t.Parallel()

	res, err := runBench(benchConfig{
		Panes: 2, Recordings: [][]rec.Record{rec.Synth(40, 16, 5*time.Second, 1)},
		Width: 82, Height: 20, Stagger: ms(500), Length: 2 * time.Second, FPS: 60, Selection: true,
	})
	if err != nil {
		t.Fatal(err)
	}

	if res.Frames.Count == 0 {
		t.Error("no frames were drawn with a selection")
	}
}

func TestParseBenchMode(t *testing.T) {
	t.Parallel()

	tests := []struct {
		in      string
		want    core.LayoutMode
		wantErr string
	}{
		{in: "", want: core.ModeBSP},
		{in: "bsp", want: core.ModeBSP},
		{in: "master-stack", want: core.ModeMaster},
		{in: "scrolling", want: core.ModeScrolling},
		{in: "tabs", wantErr: "bsp, master-stack, scrolling"},
		{in: "BSP", wantErr: "bsp, master-stack, scrolling"},
	}

	for _, tt := range tests {
		got, err := parseBenchMode(tt.in)
		if tt.wantErr != "" {
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Errorf("parseBenchMode(%q) error = %v, want one naming %q", tt.in, err, tt.wantErr)
			}

			continue
		}

		if err != nil || got != tt.want {
			t.Errorf("parseBenchMode(%q) = %q, %v; want %q", tt.in, got, err, tt.want)
		}
	}
}

func TestBenchAppLaysOutSixPanesInEveryMode(t *testing.T) {
	t.Parallel()

	for _, mode := range []core.LayoutMode{core.ModeBSP, core.ModeMaster, core.ModeScrolling} {
		t.Run(string(mode), func(t *testing.T) {
			app, ids, err := newBenchApp(benchConfig{Panes: 6, Width: 240, Height: 64, Mode: mode}, &fakeClock{t: time.Unix(1_000_000, 0)})
			if err != nil {
				t.Fatal(err)
			}

			defer func() { _ = app.Close(context.Background()) }()

			st := app.store.State()
			if got := st.Tab().Mode; got != mode {
				t.Errorf("tab mode = %q, want %q", got, mode)
			}

			a := core.Arrange(st.Tab(), app.bodyArea(), st.Layout)
			if len(ids) != 6 || len(a.Sizes) != 6 || a.HiddenLeft+a.HiddenRight != 0 {
				t.Errorf("panes %d, drawn %d, hidden %d+%d; want six drawn and none hidden", len(ids), len(a.Sizes), a.HiddenLeft, a.HiddenRight)
			}
		})
	}
}

func TestParseBenchRail(t *testing.T) {
	t.Parallel()

	tests := []struct {
		in      string
		want    bool
		wantErr string
	}{
		{in: "", want: false},
		{in: "0", want: false},
		{in: "1", want: true},
		{in: "true", wantErr: "0 or 1"},
		{in: "yes", wantErr: "0 or 1"},
	}

	for _, tt := range tests {
		got, err := parseBenchRail(tt.in)
		if tt.wantErr != "" {
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Errorf("parseBenchRail(%q) error = %v, want one naming %q", tt.in, err, tt.wantErr)
			}

			continue
		}

		if err != nil || got != tt.want {
			t.Errorf("parseBenchRail(%q) = %v, %v; want %v", tt.in, got, err, tt.want)
		}
	}
}

func TestParseBenchReview(t *testing.T) {
	t.Parallel()

	tests := []struct {
		in      string
		want    bool
		wantErr string
	}{
		{in: "", want: false},
		{in: "0", want: false},
		{in: "1", want: true},
		{in: "true", wantErr: "AIM_BENCH_REVIEW"},
		{in: "on", wantErr: "0 or 1"},
	}

	for _, tt := range tests {
		got, err := parseBenchReview(tt.in)
		if tt.wantErr != "" {
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Errorf("parseBenchReview(%q) error = %v, want one naming %q", tt.in, err, tt.wantErr)
			}

			continue
		}

		if err != nil || got != tt.want {
			t.Errorf("parseBenchReview(%q) = %v, %v; want %v", tt.in, got, err, tt.want)
		}
	}
}

func TestBenchAppOpensTheDrawer(t *testing.T) {
	t.Parallel()
	build := func(t *testing.T, review, rail bool) (*App, []core.PaneID) {
		t.Helper()

		app, ids, err := newBenchApp(benchConfig{Panes: 6, Width: 240, Height: 64, Agents: true, Rail: rail, Review: review}, &fakeClock{t: time.Unix(1_000_000, 0)})
		if err != nil {
			t.Fatal(err)
		}

		t.Cleanup(func() { _ = app.Close(context.Background()) })

		return app, ids
	}

	if app, _ := build(t, false, false); app.store.State().Tab().Review != nil {
		t.Error("a drawer is open without the knob")
	}

	for _, rail := range []bool{false, true} {
		t.Run(fmt.Sprintf("rail=%v", rail), func(t *testing.T) {
			app, ids := build(t, true, rail)
			st := app.store.State()

			rv := st.Tab().Review
			if rv == nil || rv.Pane != ids[0] || rv.Mode != core.ReviewBranch {
				t.Fatalf("drawer = %+v, want one open on pane %s in branch mode", rv, ids[0])
			}

			box, ok := app.drawerFull(st)
			if body := app.bodyAreaIn(st.Rail); !ok || box.W != body.W-body.W/2 || box.X+box.W != body.X+body.W {
				t.Errorf("drawer box = %+v (shown %v), want the right half of the body %+v", box, ok, body)
			}

			if app.railVisible() != rail {
				t.Errorf("rail visible = %v, want %v", app.railVisible(), rail)
			}

			v := app.drawer.views[st.Tab().ID]
			if v == nil || !v.loaded || v.loading || v.err != nil || len(v.res.Files) != benchReviewFiles {
				t.Fatalf("view = %+v, want a loaded result of %d files", v, benchReviewFiles)
			}

			if !app.drawerHasKeys() {
				t.Error("the drawer does not have the keyboard")
			}

			first := path.Base(v.res.Files[0].Path)
			if frame := ansi.Strip(app.View().Content); !strings.Contains(frame, first) {
				t.Errorf("the drawn frame does not show %q", first)
			}

			if app.drawer.fallbacks != 0 {
				t.Errorf("the compositor drew the drawer in %d frames, want the splice", app.drawer.fallbacks)
			}
		})
	}
}

func TestBenchReviewResultsDifferByOneHunk(t *testing.T) {
	t.Parallel()

	for rev := range 6 {
		prev, next := benchReviewResult(rev), benchReviewResult(rev+1)

		if len(next.Files) != benchReviewFiles {
			t.Fatalf("result %d has %d files, want %d", rev+1, len(next.Files), benchReviewFiles)
		}

		var changed []string

		for i, f := range next.Files {
			for j, h := range f.Hunks {
				if !reflect.DeepEqual(h, prev.Files[i].Hunks[j]) {
					changed = append(changed, fmt.Sprintf("file %d hunk %d", i, j))
				}
			}
		}

		if want := fmt.Sprintf("file 0 hunk %d", rev%benchReviewHunks); len(changed) != 1 || changed[0] != want {
			t.Errorf("result %d changed %v from the one before, want only %s", rev+1, changed, want)
		}
	}
}

// A run with the drawer draws a frame for every answer the fake engine gives,
// one each half second of virtual time, while the panes stay silent.
func TestBenchKitServesReviewAnswers(t *testing.T) {
	t.Parallel()

	quiet := recs(40, 16,
		rec.Record{At: ms(100), Data: []byte("x")},
		rec.Record{At: 4 * time.Second, Data: []byte("y")},
	)
	run := func(review bool) benchResult {
		res, err := runBench(benchConfig{
			Panes: 1, Recordings: [][]rec.Record{quiet}, Width: 120, Height: 30, Length: 1100 * time.Millisecond, FPS: 60, Review: review,
		})
		if err != nil {
			t.Fatal(err)
		}

		return res
	}

	off, on := run(false), run(true)

	if off.Reviews != 0 || off.Frames.Count != 1 {
		t.Errorf("without the drawer: %d answers and %d frames, want 0 and 1", off.Reviews, off.Frames.Count)
	}

	if on.Reviews != 2 {
		t.Errorf("answers = %d, want 2 (at 0.5 s and 1 s)", on.Reviews)
	}

	if on.Frames.Count < 1+on.Reviews {
		t.Errorf("frames = %d, want one for the write and one per answer, at least %d", on.Frames.Count, 1+on.Reviews)
	}

	if on.Fallbacks != 0 {
		t.Errorf("the compositor drew the drawer in %d frames, want the splice", on.Fallbacks)
	}

	if off.Batches != 0 {
		t.Errorf("without the drawer: %d highlight batches, want 0", off.Batches)
	}

	if on.Batches == 0 || on.Answers != on.Batches {
		t.Errorf("highlight batches = %d, answers = %d; want some, and every batch started answered", on.Batches, on.Answers)
	}
}

// A run with a picture places it in every frame it draws, beside the drawer's
// results and the rail too; a run without one places none.
func TestBenchKitPlacesThePictureInEveryFrame(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name                   string
		pictures, review, rail bool
	}{
		{name: "off"},
		{name: "with the drawer's results", pictures: true, review: true},
		{name: "with the rail", pictures: true, rail: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			cfg := benchConfig{
				Panes: 2, Recordings: [][]rec.Record{rec.Synth(40, 16, 5*time.Second, 1)},
				Width: 120, Height: 30, Stagger: ms(500), Length: ms(600), FPS: 60,
				Agents: true, Review: tt.review, Rail: tt.rail,
			}
			if tt.pictures {
				cfg.Pictures = t.TempDir()
			}

			res, err := runBench(cfg)
			if err != nil {
				t.Fatal(err)
			}

			if res.Frames.Count == 0 {
				t.Fatal("no frames were drawn")
			}

			want := 0
			if tt.pictures {
				want = res.Frames.Count
			}

			if res.Pictured != want {
				t.Errorf("the picture was placed in %d of %d frames, want %d", res.Pictured, res.Frames.Count, want)
			}

			if shown := res.Picture != [2]int{}; shown != tt.pictures {
				t.Errorf("picture size = %v, want one only with the knob", res.Picture)
			}

			if res.Fallbacks != 0 {
				t.Errorf("the compositor drew the drawer in %d frames, want the splice", res.Fallbacks)
			}

			if res.PictureFallbacks != 0 {
				t.Errorf("the compositor placed the picture in %d frames, want the splice", res.PictureFallbacks)
			}
		})
	}
}

// The bench counts a frame as placing the picture only when it shows every
// row of it where setup saw them.
func TestBenchPicturePlacedIn(t *testing.T) {
	t.Parallel()

	const w, h, id = 40, 10, 7

	base := strings.TrimSuffix(strings.Repeat(strings.Repeat(" ", w)+"\n", h), "\n")
	frame := func(id uint32, x, rows int) string {
		var pics []picRow
		for k := range rows {
			pics = append(pics, picRow{X: x, Y: 2 + k, ID: id, Row: k, Cols: 6})
		}

		out, _ := placePictures(base, w, h, pics)

		return out
	}
	pic := benchPicture{id: id, at: placeholders(frame(id, 3, 4), id)}

	tests := []struct {
		name  string
		frame string
		want  bool
	}{
		{"every row where setup saw it", frame(id, 3, 4), true},
		{"no picture", base, false},
		{"a row short", frame(id, 3, 3), false},
		{"a column over", frame(id, 4, 4), false},
		{"another picture", frame(id+1, 3, 4), false},
	}

	for _, tt := range tests {
		if got := pic.placedIn(tt.frame); got != tt.want {
			t.Errorf("%s: placedIn = %v, want %v", tt.name, got, tt.want)
		}
	}
}

func TestBenchAppOpensTheRail(t *testing.T) {
	t.Parallel()

	size := func(rail bool) (open, visible bool, cols int) {
		app, ids, err := newBenchApp(benchConfig{Panes: 6, Width: 240, Height: 64, Agents: true, Rail: rail}, &fakeClock{t: time.Unix(1_000_000, 0)})
		if err != nil {
			t.Fatal(err)
		}

		defer func() { _ = app.Close(context.Background()) }()

		cols, _ = app.panes[ids[0]].size()

		return app.store.State().Rail, app.railVisible(), cols
	}

	openOff, visibleOff, colsOff := size(false)
	openOn, visibleOn, colsOn := size(true)

	if openOff || visibleOff {
		t.Errorf("rail without the knob: open %v, visible %v; want closed", openOff, visibleOff)
	}

	if !openOn || !visibleOn {
		t.Errorf("rail with the knob: open %v, visible %v; want open and drawn", openOn, visibleOn)
	}

	if colsOn >= colsOff {
		t.Errorf("first pane is %d columns with the rail and %d without; want it narrower", colsOn, colsOff)
	}
}

func TestBenchAppFillsTheCards(t *testing.T) {
	t.Parallel()

	build := func(rail bool) *App {
		app, _, err := newBenchApp(benchConfig{Panes: 6, Width: 240, Height: 64, Agents: true, Rail: rail}, &fakeClock{t: time.Unix(1_000_000, 0)})
		if err != nil {
			t.Fatal(err)
		}

		t.Cleanup(func() { _ = app.Close(context.Background()) })

		return app
	}

	app := build(true)

	var cards []railCard

	for _, g := range app.railCards(app.store.State()) {
		cards = append(cards, g.cards...)
	}

	if len(cards) != 6 {
		t.Fatalf("%d cards, want 6", len(cards))
	}

	for _, c := range cards {
		switch {
		case c.model == "", !c.meterKnown:
			t.Errorf("card %s has no model or meter: %+v", c.pane, c)
		case c.branch == "", c.ahead == 0, !c.statsKnown, c.action == "":
			t.Errorf("card %s lacks its branch, commits ahead, action or stats: %+v", c.pane, c)
		case !c.hasPR, c.pr.ChecksTotal == 0:
			t.Errorf("card %s has no pull request with checks: %+v", c.pane, c)
		}
	}

	// The drawn rail carries the text, in each form of the checks.
	out := ansi.Strip(app.View().Content)
	for _, want := range []string{"Opus 5.5", "40%", "90%", "↑6", iconPR + " #101", iconRunning + " 3/5", iconPassed + " 5/5", iconFailed + " 2 failed", iconApproved, iconChanges} {
		if !strings.Contains(out, want) {
			t.Errorf("the rail lacks %q:\n%s", want, out)
		}
	}

	// Without the rail no card is drawn, so none is filled.
	if got := len(build(false).prs.panes); got != 0 {
		t.Errorf("the bench without the rail resolved %d panes' repositories, want none", got)
	}
}

func TestHLRunnerStartsOneBatchAndStopsWithTheApp(t *testing.T) {
	t.Parallel()

	app, _, err := newBenchApp(benchConfig{Panes: 1, Width: 240, Height: 64, Review: true}, &fakeClock{t: time.Unix(1_000_000, 0)})
	if err != nil {
		t.Fatal(err)
	}

	t.Cleanup(func() { _ = app.Close(context.Background()) })

	app.drawerAfterUpdate()

	// The command sees the ended context and answers nil; the drawer keeps its job.
	app.cancel()

	r := newHLRunner(app)
	r.start()
	r.start()

	if app.drawer.hlJob == nil || r.batches != 1 {
		t.Fatalf("job %v, batches %d; want a job and one batch from two starts", app.drawer.hlJob != nil, r.batches)
	}

	if n := r.settle(); n != 0 || r.answers != 0 {
		t.Errorf("settle fed %d answers after the app ended, want none", n)
	}

	done := make(chan struct{})

	go func() {
		defer close(done)

		r.settle()
		r.start()
		r.settle()
		r.deliver()
	}()

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("the runner blocked after the app ended")
	}

	if r.batches != 1 {
		t.Errorf("batches = %d after the app ended, want the one", r.batches)
	}
}
