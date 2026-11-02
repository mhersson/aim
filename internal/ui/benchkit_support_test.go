package ui

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"image"
	"image/png"
	"io"
	"log/slog"
	"math"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"sync"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/colorprofile"
	uv "github.com/charmbracelet/ultraviolet"

	"github.com/mhersson/aim/internal/core"
	"github.com/mhersson/aim/internal/git"
	"github.com/mhersson/aim/internal/github"
	"github.com/mhersson/aim/internal/term"
	"github.com/mhersson/aim/internal/term/rec"
)

// This file is the replay bench: the real App with panes fed from recordings
// on a virtual clock. It builds in every test run; the gate run itself is in
// bench_test.go behind the bench tag.

// maxBenchPanes is the largest grid runBench lays out (3x2).
const maxBenchPanes = 6

// fakeClock is a settable clock.
type fakeClock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *fakeClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()

	return c.t
}

func (c *fakeClock) Set(t time.Time) {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.t = t
}

func (c *fakeClock) Advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.t = c.t.Add(d)
}

var errNoCwd = errors.New("replay pane has no cwd")

// replayProc is a pane process whose output the bench feeds itself: Read blocks
// until Close, so the pane's read loop ends with the pane.
type replayProc struct {
	done chan struct{}
	once sync.Once
}

func newReplayProc() *replayProc { return &replayProc{done: make(chan struct{})} }

func (r *replayProc) Read([]byte) (int, error) {
	<-r.done

	return 0, io.EOF
}

func (r *replayProc) Write(b []byte) (int, error) { return len(b), nil }
func (r *replayProc) Resize(int, int) error       { return nil }
func (r *replayProc) Done() <-chan struct{}       { return r.done }
func (r *replayProc) Cwd() (string, error)        { return "", errNoCwd }
func (r *replayProc) Foreground() (string, error) { return "", errNoCwd }

func (r *replayProc) Close() error {
	r.once.Do(func() { close(r.done) })

	return nil
}

// summary is the nearest-rank percentiles of a set of durations.
type summary struct {
	Count                     int
	P50, P95, P99, Max, Total time.Duration
}

type frameTimes struct{ ds []time.Duration }

func (f *frameTimes) add(d time.Duration) { f.ds = append(f.ds, d) }

func (f *frameTimes) summary() summary {
	n := len(f.ds)
	if n == 0 {
		return summary{}
	}

	sorted := slices.Clone(f.ds)
	slices.Sort(sorted)

	var total time.Duration
	for _, d := range sorted {
		total += d
	}

	rank := func(p float64) time.Duration {
		return sorted[max(int(math.Ceil(p*float64(n)))-1, 0)]
	}

	return summary{Count: n, P50: rank(0.50), P95: rank(0.95), P99: rank(0.99), Max: sorted[n-1], Total: total}
}

// Every frame bench, on every machine: a frame over frameBudget, one 60 Hz
// frame, fails it. p99Headroom, half that frame, leaves the rest to the
// terminal and the machine; a p99 over it is noted, not failed.
const (
	frameBudget = 16700 * time.Microsecond
	p99Headroom = 8 * time.Millisecond
)

// frameGate judges a bench's frames: fail says why the bench fails, empty when
// the worst frame is within frameBudget; note reports a p99 over p99Headroom,
// empty when it is within.
func frameGate(name string, p99, worst time.Duration) (fail, note string) {
	if worst > frameBudget {
		fail = fmt.Sprintf("%s max %s over the %s frame budget", name, fmtMS(worst), fmtMS(frameBudget))
	}

	if p99 > p99Headroom {
		note = fmt.Sprintf("%s p99 %s over the %s headroom", name, fmtMS(p99), fmtMS(p99Headroom))
	}

	return fail, note
}

// medianMax returns the median of the runs' worst frames (the upper middle for
// an even count). A max is one frame, so one preemption in one run sets it;
// the median takes a frame over the budget in most runs to fail a gate.
func medianMax(runs []benchResult) time.Duration {
	maxes := make([]time.Duration, len(runs))
	for i, r := range runs {
		maxes[i] = r.Frames.Max
	}

	slices.Sort(maxes)

	return maxes[len(maxes)/2]
}

func fmtMS(d time.Duration) string {
	return fmt.Sprintf("%.2fms", float64(d)/float64(time.Millisecond))
}

// benchConfig describes one run. Pane i replays Recordings[i%len] from
// Start + i*Stagger for Length.
type benchConfig struct {
	Panes                  int
	Recordings             [][]rec.Record
	Width, Height          int
	Start, Length, Stagger time.Duration
	FPS                    int
	// Selection highlights every content row of the first pane for the whole run.
	Selection bool
	// Agents makes every pane a working agent whose badge spinner advances every spinEvery.
	Agents bool
	// Mode is the bench tab's layout mode; "" is bsp.
	Mode core.LayoutMode
	// Rail opens the agents rail before the run, and with Agents gives every card all its text (see fillBenchCards).
	Rail bool
	// Review opens the review drawer on the first pane over a 40-file result; a new result arrives every benchReviewEvery.
	Review bool
	// Pictures, when set, is a directory the run writes a page with one picture
	// into; the drawer opens on the first pane, over the fake engine's first
	// result as with Review, and shows the page with the picture placed (see
	// showBenchPicture).
	Pictures string
	Trace    func(at time.Duration, drew bool) // tests only; nil in production
}

// benchScrollingColumnWidth is the column width (percent) that keeps all six
// bench panes visible in scrolling mode.
const benchScrollingColumnWidth = 16

// parseBenchMode maps AIM_BENCH_MODE to a layout mode; "" is bsp.
func parseBenchMode(s string) (core.LayoutMode, error) {
	switch s {
	case "", string(core.ModeBSP):
		return core.ModeBSP, nil
	case string(core.ModeMaster):
		return core.ModeMaster, nil
	case string(core.ModeScrolling):
		return core.ModeScrolling, nil
	}

	return "", fmt.Errorf("AIM_BENCH_MODE %q is not one of bsp, master-stack, scrolling", s)
}

// parseBenchRail maps AIM_BENCH_RAIL to whether the rail is open; "" is closed.
func parseBenchRail(s string) (bool, error) { return parseBenchSwitch("AIM_BENCH_RAIL", s) }

// parseBenchReview maps AIM_BENCH_REVIEW to whether the drawer is open; "" is closed.
func parseBenchReview(s string) (bool, error) { return parseBenchSwitch("AIM_BENCH_REVIEW", s) }

// parseBenchSwitch reads an on/off knob: "" and "0" are off, "1" is on.
func parseBenchSwitch(name, s string) (bool, error) {
	switch s {
	case "", "0":
		return false, nil
	case "1":
		return true, nil
	}

	return false, fmt.Errorf("%s %q is not 0 or 1", name, s)
}

type benchResult struct {
	Panes, Width, Height  int
	PaneSizes             [][2]int // guest grid per pane, in open order
	Frames                summary  // feed + update + view + present, per drawn frame
	Feed, Render, Present summary  // components per drawn frame; Render = update + view
	CPUPercent            float64  // all step work / Length * 100
	BytesFed              int64
	Steps                 int
	Updates               int // pane Update calls in timed steps
	Reviews               int // results the fake engine served in timed steps
	Fallbacks             int // frames whose drawer the compositor drew because the splice declined
	PictureFallbacks      int // frames whose pictures the compositor placed because the splice declined
	GCs                   uint32
	GCPause               time.Duration
	Batches               int    // drawer highlight batches started
	Answers               int    // their answers fed to the app, the final settle's included
	Picture               [2]int // the picture's cols and rows; zero without Pictures
	Pictured              int    // drawn frames that placed the picture
}

// hlRunner runs the drawer's highlight batches on their own goroutine, as
// Bubble Tea runs a command, and feeds their answers back to the app.
type hlRunner struct {
	app     *App
	started *hlJob
	done    chan tea.Msg // holds the one running batch's answer, so its send never blocks
	batches int
	answers int
	closed  bool // a nil answer came: the app's context ended, so nothing starts or waits again
}

func newHLRunner(app *App) *hlRunner {
	return &hlRunner{app: app, done: make(chan tea.Msg, 1)}
}

// start runs the batch the drawer waits on, if the runner has not started it.
// Call it after every Update.
func (r *hlRunner) start() {
	j := r.app.drawer.hlJob
	if r.closed || j == nil || j == r.started {
		return
	}

	r.started = j
	r.batches++

	ctx, hl := r.app.ctx, r.app.drawer.hl

	go func() { r.done <- j.run(ctx, hl) }()
}

// deliver feeds an answer that has arrived to the app without blocking, then
// starts the next batch. It returns 1 for an answer fed, else 0.
func (r *hlRunner) deliver() int {
	if r.closed {
		return 0
	}

	select {
	case msg := <-r.done:
		return r.feed(msg)
	default:
		return 0
	}
}

// settle feeds answers, waiting for each, until no batch waits or an answer is
// nil (the app's context ended). It returns how many it fed.
func (r *hlRunner) settle() int {
	n := 0

	for r.start(); !r.closed && r.app.drawer.hlJob != nil && r.app.drawer.hlJob == r.started; {
		k := r.feed(<-r.done)
		if k == 0 {
			break
		}

		n += k
	}

	return n
}

func (r *hlRunner) feed(msg tea.Msg) int {
	if msg == nil {
		r.closed = true

		return 0
	}

	r.app.Update(msg)
	r.answers++
	r.start()

	return 1
}

// replayFeed walks one pane's records; they are sorted by At.
type replayFeed struct {
	pane  *pane
	recs  []rec.Record
	start time.Duration
	next  int
}

// feedBefore feeds every data record with At < limit and returns the bytes fed.
// Recorded resizes are skipped: the layout sets the pane size.
func (f *replayFeed) feedBefore(limit time.Duration) int64 {
	var n int64

	for f.next < len(f.recs) && f.recs[f.next].At < limit {
		if d := f.recs[f.next].Data; d != nil {
			f.pane.feed(d)

			n += int64(len(d))
		}

		f.next++
	}

	return n
}

// presenter mirrors Bubble Tea v2.1.0's cursedRenderer.flush in alt screen
// mode, writing to io.Discard.
type presenter struct {
	scr *uv.TerminalRenderer
	buf uv.ScreenBuffer
}

func newPresenter(w, h int) *presenter {
	scr := uv.NewTerminalRenderer(io.Discard, []string{"TERM=xterm-ghostty"})
	scr.SetColorProfile(colorprofile.TrueColor)
	scr.SetTabStops(w)
	scr.SetBackspace(true)
	scr.SetScrollOptim(runtime.GOOS != "windows")
	scr.Resize(w, h)
	scr.SetFullscreen(true)
	scr.SetRelativeCursor(false)
	scr.Erase()

	return &presenter{scr: scr, buf: uv.NewScreenBuffer(w, h)}
}

func (p *presenter) present(frame string) error {
	content := uv.NewStyledString(frame)

	p.buf.Clear()
	content.Draw(p.buf, p.buf.Bounds())
	p.scr.Render(p.buf.RenderBuffer)

	if err := p.scr.Flush(); err != nil {
		return fmt.Errorf("flush: %w", err)
	}

	return nil
}

func (c benchConfig) validate() error {
	if c.Panes <= 0 || c.Panes > maxBenchPanes {
		return fmt.Errorf("bench needs 1 to %d panes, got %d", maxBenchPanes, c.Panes)
	}

	if len(c.Recordings) == 0 || c.FPS <= 0 || c.Length <= 0 || c.Width <= 0 || c.Height <= 1 {
		return fmt.Errorf("bench needs a recording, fps, length and size (got %d recordings, %d fps, %v, %dx%d)",
			len(c.Recordings), c.FPS, c.Length, c.Width, c.Height)
	}

	for i := range c.Panes {
		need := c.Start + time.Duration(i)*c.Stagger + c.Length
		if have := rec.Duration(c.Recordings[i%len(c.Recordings)]); have < need {
			return fmt.Errorf("pane %d needs a recording of %v, have %v", i, need, have)
		}
	}

	return nil
}

// benchGrid is the columns and rows runBench lays n panes out in.
func benchGrid(n int) (cols, rows int) {
	if n <= 2 {
		return n, 1
	}

	return (n + 1) / 2, 2
}

// newBenchApp builds an App of replay panes in a grid, sized width x height,
// and returns the pane ids in open order. Columns come first (side by side),
// then each top pane is split to open a row below it.
func newBenchApp(cfg benchConfig, clk clock) (*App, []core.PaneID, error) {
	conf := core.DefaultConfig()
	if cfg.Mode != "" {
		conf.Layout.DefaultMode = string(cfg.Mode)
	}

	if cfg.Mode == core.ModeScrolling {
		conf.Layout.ColumnWidth = benchScrollingColumnWidth
	}

	app, err := New(Options{
		caches: testDrawCaches,
		Git:    &fakeGit{}, // the bench runs no git cmd
		Logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
		Store:  core.NewStore(core.InitialState(conf)),
		Getenv: func(string) string { return "" },
		Cwd:    "/",
		Icons:  true,
	})
	if err != nil {
		return nil, nil, err
	}

	// A copy must never reach the operator's real clipboard tools.
	app.clip.run = (&fakeRun{}).run
	app.openLink = (&fakeOpen{}).open

	app.clock = clk
	app.start = func(context.Context, term.Command, int, int) (proc, error) { return newReplayProc(), nil }

	// The pane commands run on the update loop; their tea.Cmds are never run, so no wall time passes.
	app.Update(tea.WindowSizeMsg{Width: cfg.Width, Height: cfg.Height})

	ids := []core.PaneID{app.store.State().Tab().Focus}
	open := func(split core.Split) {
		app.openPane(core.KindShell, split)
		ids = append(ids, app.store.State().Tab().Focus)
	}

	cols, _ := benchGrid(cfg.Panes)
	for range cols - 1 {
		open(core.SplitVertical)
	}

	// Three columns: the first takes about a third instead of half. The
	// other modes keep their own default sizes.
	if cols == 3 && (cfg.Mode == "" || cfg.Mode == core.ModeBSP) {
		_ = app.dispatch(core.FocusPane{ID: ids[0]})

		for range 3 {
			_ = app.act(core.ResizeDir{Dir: core.Left})
		}
	}

	for i := range cfg.Panes - cols {
		_ = app.dispatch(core.FocusPane{ID: ids[i]})

		open(core.SplitHorizontal)
	}

	if len(ids) != cfg.Panes || len(app.panes) != cfg.Panes {
		_ = app.Close(context.Background())

		return nil, nil, fmt.Errorf("opened %d of %d panes", len(app.panes), cfg.Panes)
	}

	if cfg.Agents {
		for _, id := range ids {
			for _, act := range []core.Action{
				core.SetPaneKind{ID: id, Kind: core.KindAgent, Harness: "claude-code"},
				core.SetAgentState{ID: id, State: core.AgentWorking},
			} {
				if err := app.dispatch(act); err != nil {
					_ = app.Close(context.Background())

					return nil, nil, fmt.Errorf("make pane %s a working agent: %w", id, err)
				}
			}
		}
	}

	if cfg.Rail {
		// The probe and relayout cmds are not run: the bench has no wall time.
		_ = app.railToggle()
		app.rail.focused = false

		if cfg.Agents {
			if err := fillBenchCards(app, ids); err != nil {
				_ = app.Close(context.Background())

				return nil, nil, err
			}
		}
	}

	if cfg.Review || cfg.Pictures != "" {
		root := benchReviewRoot
		if cfg.Pictures != "" {
			root = cfg.Pictures
		}

		if err := openBenchDrawer(app, ids[0], root); err != nil {
			_ = app.Close(context.Background())

			return nil, nil, err
		}
	}

	return app, ids, nil
}

// fillBenchCards gives every agent's card all its text, as probes, hooks and
// polls leave it in a real run: a model, a meter, a branch ahead of its upstream,
// a pull request with checks and a review, an action and diff stats.
func fillBenchCards(app *App, ids []core.PaneID) error {
	forms := []core.PRInfo{
		prInfo("OPEN", checksRunning, prReview("APPROVED")),
		prInfo("OPEN", checksPassed, prReview("CHANGES_REQUESTED")),
		prInfo("OPEN", checksFailing),
	}
	open := map[string][]core.PRInfo{}
	now := app.clock.Now()

	app.prs.panes = map[core.PaneID]*prPane{}

	for i, id := range ids {
		n := i + 1
		project, branch := fmt.Sprintf("bench%d", n), fmt.Sprintf("feat/bench-card-%d", n)
		cwd := app.store.State().Panes[id].WorkDir()

		for _, act := range []core.Action{
			core.SetAgentAction{ID: id, Action: "Edit internal/ui/rail.go"},
			core.SetAgentStatus{ID: id, Session: core.SessionID(fmt.Sprintf("bench-%d", n)), Model: "Opus 5.5", Context: float64(30 + 10*n), ContextKnown: true},
		} {
			if err := app.dispatch(act); err != nil {
				return fmt.Errorf("fill the card of pane %s: %w", id, err)
			}
		}

		rp := app.railEntry(id)
		rp.head.target, rp.head.own, rp.hookAt = cwd, cwd, now.Add(-time.Duration(n)*time.Minute)
		rp.probe = railProbe{
			known: true, repo: true, project: project, branch: branch, ahead: n, headCwd: cwd,
			stats: git.Stats{Files: 3 * n, Added: 40 * n, Removed: 5 * n}, statsKnown: true, statsCwd: cwd,
		}

		repo := github.Repo{Host: "github.com", Owner: "acme", Name: project}
		key := prKey{cwd: cwd, branch: branch}
		app.prs.panes[id] = &prPane{key: key, answered: key, repo: repo, ok: true}

		pr := forms[i%len(forms)]
		pr.Number, pr.HeadBranch = 100+n, branch
		open[repo.String()] = append(open[repo.String()], pr)
	}

	for repo, prs := range open {
		if err := app.dispatch(core.SetRepoPRs{Repo: repo, DefaultBranch: "main", Open: prs}); err != nil {
			return fmt.Errorf("fill the pull requests of %s: %w", repo, err)
		}
	}

	return nil
}

const (
	// benchReviewFiles is the size of the result the fake engine serves.
	benchReviewFiles = 40
	// benchReviewHunks is how many hunks each of its files has.
	benchReviewHunks = 2
	// benchReviewEvery is the virtual time between two results.
	benchReviewEvery = 500 * time.Millisecond
	// benchReviewRoot is the worktree the reviewed pane resolves to without Pictures.
	benchReviewRoot = "/bench/repo"
)

// benchReviewHunk is one hunk of a file of the fake engine's result; seq is the
// number the hunk's last stamp carries, 0 before it has one.
func benchReviewHunk(file, hunk, seq int) git.Hunk {
	start := 10 + hunk*40

	return hunkSpec(start, start, fmt.Sprintf("func (s *Server) handle%d_%d(w http.ResponseWriter, r *http.Request)", file, hunk),
		" \tctx := r.Context()",
		" \tlog := s.log.With(\"handler\", \"handle\")",
		"-\tid := r.URL.Query().Get(\"id\")",
		"+\tid := mux.Vars(r)[\"id\"]",
		" \tif id == \"\" {",
		" \t\thttp.Error(w, \"missing id\", http.StatusBadRequest)",
		" \t\treturn",
		" \t}",
		"-\titem, err := s.store.Get(id)",
		"+\titem, err := s.store.Get(ctx, id)",
		fmt.Sprintf("+\tstamp := %d", seq),
		" \tif err != nil {",
		" \t\tlog.Error(\"get\", \"id\", id, \"err\", err)",
		" \t\treturn",
		" \t}",
	)
}

// benchReviewResult is the fake engine's result number rev: 40 files of two
// hunks. Each result after the first changes one hunk of the first file, the
// one at the top of the drawer, so every result shows.
func benchReviewResult(rev int) git.Result {
	files := make([]git.FileDiff, benchReviewFiles)

	for i := range files {
		hunks := make([]git.Hunk, benchReviewHunks)

		for j := range hunks {
			seq := 0
			if i == 0 && rev-1 >= j {
				seq = rev - (rev-1-j)%benchReviewHunks
			}

			hunks[j] = benchReviewHunk(i, j, seq)
		}

		files[i] = fileSpec(fmt.Sprintf("internal/pkg%02d/file%02d.go", i/4, i), git.Modified, hunks...)
	}

	return git.Result{Old: "base", New: fmt.Sprintf("tree%d", rev), Live: true, Files: files}
}

// benchReviewMsg is the answer the engine's goroutine would send for the active
// tab's load of res; live is an answer to a watcher batch, not to a new source.
func benchReviewMsg(app *App, res git.Result, live bool) reviewLoadedMsg {
	tab := app.store.State().Tab().ID

	return reviewLoadedMsg{
		tab: tab, gen: app.drawer.views[tab].epoch, res: res, live: live,
		info: reviewInfo{project: "bench", branch: "feat/bench", base: "main", hidden: hiddenFlags(res.Files)},
	}
}

// openBenchDrawer opens the drawer on pane, whose worktree is root, in branch
// mode, with its tree open and the keyboard, and answers its first load with
// the fake engine's first result. The app has no engine: an answer is the one
// message a load ends in, so the sync that starts the load and the apply of its
// answer run as in the app. The load's command is not run, so its context is
// ended here.
func openBenchDrawer(app *App, pane core.PaneID, root string) error {
	if err := app.dispatch(core.OpenReview{Pane: pane, Mode: core.ReviewBranch}); err != nil {
		return fmt.Errorf("open the drawer: %w", err)
	}

	if err := app.dispatch(core.SetReviewTree{Open: true}); err != nil {
		return fmt.Errorf("open the drawer's tree: %w", err)
	}

	app.watches.panes[pane] = &paneWatch{cwd: app.store.State().Panes[pane].Cwd, want: root}
	_ = app.reviewSync()
	app.drawerTake()

	tab := app.store.State().Tab().ID
	if v := app.drawer.views[tab]; v == nil || !v.loading {
		return errors.New("the drawer did not start a load")
	}

	app.drawer.views[tab].cancel()
	app.Update(benchReviewMsg(app, benchReviewResult(0), false))

	switch v := app.drawer.views[tab]; {
	case v.err != nil:
		return fmt.Errorf("apply the first result: %w", v.err)
	case !v.loaded:
		return errors.New("the drawer did not apply the first result")
	}

	return nil
}

const (
	// benchPicturePage and benchPictureFile are the page and the picture a run writes.
	benchPicturePage = "bench.md"
	benchPictureFile = "picture.png"
	// At 10x20 pixel cells the picture is 80x20 cells, as tall as a picture shows.
	benchPictureW, benchPictureH = 800, 400
)

// benchPicture is the picture a run keeps on screen, and the placeholder runs
// setup saw it drawn as.
type benchPicture struct {
	id         uint32
	cols, rows int
	at         []phRow
}

// placedIn reports whether frame shows every row of the picture where setup saw them.
func (p benchPicture) placedIn(frame string) bool {
	return len(p.at) > 0 && slices.Equal(placeholders(frame, p.id), p.at)
}

// showBenchPicture puts a picture on screen as a terminal with kitty graphics
// shows one: it turns pictures on with 10x20 pixel cells, writes a page with
// one picture into dir, opens it in the drawer, which must be open on dir, and
// answers the picture's jobs with the app's loader, which decodes and encodes it.
func showBenchPicture(app *App, dir string) (benchPicture, error) {
	if err := writeBenchPicture(dir); err != nil {
		return benchPicture{}, err
	}

	jobs := &recJobs{}
	app.pictures.jobs = jobs
	app.pictures.start()

	for _, m := range []tea.Msg{truecol, gfxOK, cell10x2, da1} {
		app.Update(m)
	}

	if !app.pictures.on() {
		return benchPicture{}, errors.New("pictures are off")
	}

	tab := app.store.State().Tab().ID

	read := app.openMarkdownPage(tab, filepath.Join(dir, benchPicturePage))
	if read == nil {
		return benchPicture{}, errors.New("the drawer is not open on a worktree")
	}

	app.Update(read())

	p := app.drawer.views[tab].page()
	if p == nil {
		return benchPicture{}, fmt.Errorf("the page did not open: %s", app.notice)
	}

	// A frame asks for the picture's size and the next for its render; each answer is fed before the next frame.
	for range 2 {
		app.View()

		for _, j := range jobs.take() {
			msg := app.loader.run(app.ctx, j)
			if msg.err != nil {
				return benchPicture{}, fmt.Errorf("load the picture: %w", msg.err)
			}

			app.Update(msg)
		}
	}

	lay := p.view.layout()
	if len(lay.Images) != 1 || lay.Images[0].ID == 0 {
		return benchPicture{}, fmt.Errorf("the page lays out images %+v, want one picture", lay.Images)
	}

	im := lay.Images[0]
	pic := benchPicture{id: im.ID, cols: im.Cols, rows: im.Rows, at: placeholders(app.View().Content, im.ID)}

	if len(pic.at) != im.Rows || len(jobs.jobs) != 0 {
		return benchPicture{}, fmt.Errorf("the frame places %d of the picture's %d rows, with %d jobs left", len(pic.at), im.Rows, len(jobs.jobs))
	}

	return pic, nil
}

// writeBenchPicture writes the page and its picture into dir.
func writeBenchPicture(dir string) error {
	var b bytes.Buffer
	if err := png.Encode(&b, image.NewRGBA(image.Rect(0, 0, benchPictureW, benchPictureH))); err != nil {
		return fmt.Errorf("encode the picture: %w", err)
	}

	if err := os.WriteFile(filepath.Join(dir, benchPictureFile), b.Bytes(), 0o600); err != nil {
		return fmt.Errorf("write the picture: %w", err)
	}

	page := "# Bench\n\n![bench](" + benchPictureFile + ")\n"
	if err := os.WriteFile(filepath.Join(dir, benchPicturePage), []byte(page), 0o600); err != nil {
		return fmt.Errorf("write the page: %w", err)
	}

	return nil
}

// selectWholePane selects the pane's whole history and more, so every content
// row stays highlighted in every frame while the replay scrolls.
func selectWholePane(app *App, id core.PaneID) {
	cols, _ := app.panes[id].size()

	app.sel = &paneSelection{
		pane:   id,
		mode:   selStream,
		anchor: stablePoint{},
		head:   stablePoint{row: 1 << 40, col: cols - 1},
	}
}

// holdEnd is when the pane's current sync hold ends.
func (p *pane) holdEnd() time.Time {
	p.mu.Lock()
	defer p.mu.Unlock()

	return p.openedAt.Add(syncMaxHold)
}

// runBench replays the recordings through the real App on a virtual clock and
// times feed + update + view + present for every drawn frame.
//
// Fairness: every pane replays the same recording window (offset by Stagger),
// history before the window is fed untimed, the GC runs before timing, steps
// where nothing was fed or released are not counted, and the caller takes the
// median of three runs.
func runBench(cfg benchConfig) (benchResult, error) {
	if err := cfg.validate(); err != nil {
		return benchResult{}, err
	}

	t0 := time.Unix(1_000_000, 0)
	clk := &fakeClock{t: t0}

	app, ids, err := newBenchApp(cfg, clk)
	if err != nil {
		return benchResult{}, err
	}

	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()

		_ = app.Close(ctx)
	}()

	var pic benchPicture

	if cfg.Pictures != "" {
		if pic, err = showBenchPicture(app, cfg.Pictures); err != nil {
			return benchResult{}, fmt.Errorf("show the picture: %w", err)
		}
	}

	hl := newHLRunner(app)
	hl.start()

	feeds := make([]*replayFeed, cfg.Panes)
	res := benchResult{Panes: cfg.Panes, Width: cfg.Width, Height: cfg.Height, Picture: [2]int{pic.cols, pic.rows}}
	tab := app.store.State().Tab().ID
	rev := 0

	for i, id := range ids {
		pn := app.panes[id]
		cols, rows := pn.size()
		res.PaneSizes = append(res.PaneSizes, [2]int{cols, rows})
		feeds[i] = &replayFeed{pane: pn, recs: cfg.Recordings[i%len(cfg.Recordings)], start: cfg.Start + time.Duration(i)*cfg.Stagger}
	}

	// A hold's tick would fire on a timer in the app; the bench delivers it when the virtual clock passes it.
	armed := map[core.PaneID]time.Time{}
	send := func(id core.PaneID, msg tea.Msg) {
		app.Update(msg)
		hl.start()

		if p := app.panes[id]; p != nil && p.holdArmed {
			if _, ok := armed[id]; !ok {
				armed[id] = p.holdEnd()
			}
		}
	}

	// Untimed warm-up: history before each pane's window, then one full frame.
	for i, f := range feeds {
		f.feedBefore(f.start)
		send(ids[i], paneDirtyMsg{id: ids[i], p: app.panes[ids[i]]})
	}

	if cfg.Selection {
		selectWholePane(app, ids[0])
	}

	hl.settle()

	pr := newPresenter(cfg.Width, cfg.Height)
	last := app.View().Content

	if cfg.Pictures != "" && !pic.placedIn(last) {
		return benchResult{}, errors.New("the picture is not placed before timing")
	}

	if err := pr.present(last); err != nil {
		return benchResult{}, fmt.Errorf("present: %w", err)
	}

	runtime.GC()

	var mem0, mem1 runtime.MemStats

	runtime.ReadMemStats(&mem0)

	fi := time.Second / time.Duration(cfg.FPS)
	res.Steps = int((cfg.Length*time.Duration(cfg.FPS) + time.Second - 1) / time.Second)

	var frames, feedS, renderS, presentS frameTimes

	var total time.Duration

	fed := make([]bool, cfg.Panes)

	for k := 1; k <= res.Steps; k++ {
		at := time.Duration(k) * fi
		now := t0.Add(at)
		clk.Set(now)

		// The engine builds its answer off the update loop, so it is built before timing.
		var answer *reviewLoadedMsg

		if cfg.Review && at/benchReviewEvery > (at-fi)/benchReviewEvery {
			rev++
			msg := benchReviewMsg(app, benchReviewResult(rev), true)
			answer = &msg
		}

		t := time.Now()

		for i, f := range feeds {
			n := f.feedBefore(f.start + at)
			fed[i] = n > 0
			res.BytesFed += n
		}

		feedD := time.Since(t) // wall-clock: measured for the bench report, never asserted
		total += feedD

		t = time.Now()
		updates := hl.deliver()

		for i, id := range ids {
			if fed[i] {
				send(id, paneDirtyMsg{id: id, p: app.panes[id]})

				updates++
			}

			if end, ok := armed[id]; ok && !now.Before(end) {
				delete(armed, id)
				send(id, holdExpiredMsg{id: id, p: app.panes[id]})

				updates++
			}
		}

		// The agent tick advances the badge spinner every spinEvery while an agent works.
		if cfg.Agents && at/spinEvery > (at-fi)/spinEvery {
			app.spin++

			updates++
		}

		if answer != nil {
			app.Update(*answer)
			hl.start()

			res.Reviews++
			updates++
		}

		// The flash of rows an answer added ends on a tick in the app; a later answer moves its end.
		if cfg.Review {
			if v := app.drawer.views[tab]; v != nil && !v.flashUntil.IsZero() && !now.Before(v.flashUntil) {
				app.Update(reviewFlashMsg{tab: tab})
				hl.start()

				updates++
			}
		}

		res.Updates += updates
		drew := false

		if updates > 0 {
			frame := app.View().Content
			renderD := time.Since(t) // wall-clock: measured for the bench report, never asserted
			total += renderD

			if frame != last {
				last = frame
				drew = true

				t = time.Now()
				err := pr.present(frame)
				presentD := time.Since(t) // wall-clock: measured for the bench report, never asserted
				total += presentD

				if err != nil {
					return benchResult{}, fmt.Errorf("present: %w", err)
				}

				frames.add(feedD + renderD + presentD)
				feedS.add(feedD)
				renderS.add(renderD)
				presentS.add(presentD)

				// Outside the timed sections; its garbage is a small share of a frame's.
				if cfg.Pictures != "" && pic.placedIn(frame) {
					res.Pictured++
				}
			}
		}

		if cfg.Trace != nil {
			cfg.Trace(at, drew)
		}
	}

	runtime.ReadMemStats(&mem1)
	hl.settle()

	res.Batches, res.Answers = hl.batches, hl.answers
	res.GCs, res.GCPause = mem1.NumGC-mem0.NumGC, time.Duration(mem1.PauseTotalNs-mem0.PauseTotalNs)
	res.Fallbacks, res.PictureFallbacks = app.drawer.fallbacks, app.pictures.fallbacks
	res.Frames, res.Feed, res.Render, res.Present = frames.summary(), feedS.summary(), renderS.summary(), presentS.summary()
	res.CPUPercent = float64(total) / float64(cfg.Length) * 100

	return res, nil
}
