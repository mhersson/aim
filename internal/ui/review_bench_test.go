//go:build bench

package ui

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/mhersson/aim/internal/git"
	"github.com/mhersson/aim/internal/term"
)

// scrollHunks is how many hunks the long diff has; each is 40 lines of which 20
// are changed, so the file is 10,000 lines with half of them changed.
const scrollHunks = 250

// benchScrollResult is one file of scrollHunks hunks, each with 10 lines of
// context, 10 removed, 10 added and 10 of context.
func benchScrollResult() git.Result {
	hunks := make([]git.Hunk, scrollHunks)

	for k := range hunks {
		specs := make([]string, 0, 40)
		line := func(kind byte, tag string, i int) {
			specs = append(specs, fmt.Sprintf("%c\tv%d_%d := s.compute(ctx, %d, %q) // %s", kind, k, i, i*k, tag, tag))
		}

		for i := range 10 {
			line(' ', "before", i)
		}

		for i := range 10 {
			line('-', "old", i)
		}

		for i := range 10 {
			line('+', "new", i)
		}

		for i := range 10 {
			line(' ', "after", i)
		}

		start := 1 + k*50
		hunks[k] = hunkSpec(start, start, fmt.Sprintf("func (s *Server) step%d(ctx context.Context) error", k), specs...)
	}

	return git.Result{
		Old: "base", New: "tree", Live: true,
		Files: []git.FileDiff{fileSpec("internal/big/big.go", git.Modified, hunks...)},
	}
}

// TestBenchReviewScroll pages through a 10,000-line diff in the drawer with
// ctrl+d, one press per frame at 60 FPS on the virtual clock, and times update,
// view and present for each frame. Run it with:
//
//	CGO_ENABLED=1 go test -tags bench ./internal/ui -run 'TestBenchReviewScroll' -v
//
// It runs twice: over the bench's own small heap, and over a heap the size of a
// session. Run one with -run 'TestBenchReviewScroll/heap=session'.
func TestBenchReviewScroll(t *testing.T) {
	t.Run("heap=bench", func(t *testing.T) { benchScroll(t, nil) })
	t.Run("heap=session", func(t *testing.T) { benchScroll(t, sessionHeap) })
}

// sessionEmulators, sessionCols and sessionRows size sessionHeap: the panes of a busy session, one emulator each.
const (
	sessionEmulators = 6
	sessionCols      = 120
	sessionRows      = 40
)

// sessionHeap is six 120x40 emulators, each fed term.ScrollbackLines+40
// lines of about 100 columns with an SGR colour, so every scrollback is full.
func sessionHeap(t *testing.T) any {
	t.Helper()

	vts := make([]term.VT, 0, sessionEmulators)

	for e := range sessionEmulators {
		vt := term.NewVT(sessionCols, sessionRows, io.Discard)

		t.Cleanup(func() { _ = vt.Close() })

		for i := range term.ScrollbackLines + 40 {
			text := fmt.Sprintf("emulator %d line %d ", e, i)
			text += strings.Repeat("x", 100-len(text))

			if _, err := fmt.Fprintf(vt, "\x1b[3%dm%s\x1b[0m\r\n", 1+i%6, text); err != nil {
				t.Fatalf("feed emulator %d: %v", e, err)
			}
		}

		if n := vt.ScrollbackLen(); n != term.ScrollbackLines {
			t.Fatalf("emulator %d holds %d scrollback lines, want %d", e, n, term.ScrollbackLines)
		}

		vts = append(vts, vt)
	}

	return vts
}

// benchScroll is TestBenchReviewScroll's run; hold, when set, builds what
// the run keeps live (runtime.KeepAlive after the last frame).
func benchScroll(t *testing.T, hold func(t *testing.T) any) {
	t0 := time.Unix(1_000_000, 0)
	clk := &fakeClock{t: t0}

	app, _, err := newBenchApp(benchConfig{Panes: 1, Width: benchWidth, Height: benchHeight, Review: true}, clk)
	if err != nil {
		t.Fatal(err)
	}

	defer func() { _ = app.Close(context.Background()) }()

	// The long diff is a new source: it replaces the first result, at the top.
	app.Update(benchReviewMsg(app, benchScrollResult(), false))

	v := app.drawer.views[app.store.State().Tab().ID]
	if len(v.res.Files) != 1 || len(v.rows) < scrollHunks*40 || v.cursor != 0 {
		t.Fatalf("view holds %d files, %d rows, cursor %d; want the long diff at its top", len(v.res.Files), len(v.rows), v.cursor)
	}

	if !app.drawerHasKeys() {
		t.Fatal("the drawer does not have the keyboard")
	}

	hl := newHLRunner(app)
	hl.settle()

	pr := newPresenter(benchWidth, benchHeight)
	if err := pr.present(app.View().Content); err != nil {
		t.Fatalf("present: %v", err)
	}

	var held any
	if hold != nil {
		held = hold(t)
	}

	runtime.GC()

	var mem0, mem1 runtime.MemStats

	runtime.ReadMemStats(&mem0)

	var (
		answers, plainFrames, plainRun, plainMax int

		frames, render, present frameTimes
		press                   = tea.KeyPressMsg{Code: 'd', Mod: tea.ModCtrl}
		fi                      = time.Second / 60
	)

	for v.cursor < len(v.rows)-1 {
		if len(frames.ds) > len(v.rows) {
			t.Fatal("ctrl+d does not reach the last row")
		}

		clk.Advance(fi)

		before := v.cursor
		start := time.Now()

		answers += hl.deliver()

		app.Update(press)
		hl.start()

		frame := app.View().Content
		renderD := time.Since(start)
		err := pr.present(frame)

		frames.add(time.Since(start))
		render.add(renderD)
		present.add(time.Since(start) - renderD)

		if err != nil {
			t.Fatalf("present: %v", err)
		}

		if v.cursor <= before {
			t.Fatalf("ctrl+d moved the cursor from row %d to %d", before, v.cursor)
		}

		if plainRows(app, v) > 0 {
			plainFrames++
			plainRun++
			plainMax = max(plainMax, plainRun)
		} else {
			plainRun = 0
		}
	}

	runtime.ReadMemStats(&mem1)

	hl.settle()

	runtime.KeepAlive(held)

	s, r, p := frames.summary(), render.summary(), present.summary()
	t.Logf("paged %d rows in %d frames (%d rows a page): p50 %s, p95 %s, p99 %s (headroom %s), max %s (budget %s)",
		len(v.rows), s.Count, app.reviewBodyH(v)/2, fmtMS(s.P50), fmtMS(s.P95), fmtMS(s.P99), fmtMS(p99Headroom), fmtMS(s.Max), fmtMS(frameBudget))
	t.Logf("per-component p50/p99 (they do not sum to the frame's): update+view %s/%s, present %s/%s",
		fmtMS(r.P50), fmtMS(r.P99), fmtMS(p.P50), fmtMS(p.P99))

	t.Logf("highlight: %d answers between frames; %d of %d frames drew rows not yet coloured, at most %d in a row; live heap %.1f MB after setup, %d GCs in the run",
		answers, plainFrames, s.Count, plainMax, float64(mem0.HeapAlloc)/(1<<20), mem1.NumGC-mem0.NumGC)

	if app.drawer.fallbacks != 0 {
		t.Errorf("the compositor drew the drawer in %d frames, so the run did not measure the splice", app.drawer.fallbacks)
	}

	gateFrames(t, "paging", s.P99, s.Max)
}

// plainRows counts the visible rows with a chunk that has no spans yet.
func plainRows(app *App, v *reviewView) int {
	st := app.store.State()

	box, ok := app.drawerBox(st)
	if !ok {
		return 0
	}

	lay := app.drawerLayoutOf(box, v, drawerTree(st))
	top := v.viewTop(lay.diffH)
	c := v.derived()
	n := 0

	for i := top; i < min(top+lay.diffH, len(v.rows)); i++ {
		for _, ref := range v.rowSpans(i) {
			if _, ok := c.spans[ref.key]; !ok {
				n++

				break
			}
		}
	}

	return n
}

func hunkHolds(v *reviewView, path, text string) bool {
	for _, f := range v.res.Files {
		if f.Path != path {
			continue
		}

		for _, h := range f.Hunks {
			for _, l := range h.Lines {
				if l.Kind == git.Add && l.Text == text {
					return true
				}
			}
		}
	}

	return false
}

// gitIn runs git in dir, with an identity and a config that need nothing from the host.
func gitIn(t *testing.T, dir string, args ...string) {
	t.Helper()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Dir = dir

	cmd.Env = append(os.Environ(),
		"GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL="+filepath.Join(t.TempDir(), "gitconfig"),
		"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t", gitFileOnly)

	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}

// newRepoDir is an empty repository on main in a temp dir without symlinks.
func newRepoDir(t *testing.T) string {
	t.Helper()

	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}

	gitIn(t, dir, "init", "-q", "-b", "main")

	return dir
}

func commitEverything(t *testing.T, dir, msg string) {
	t.Helper()
	gitIn(t, dir, "add", "-A")
	gitIn(t, dir, "commit", "-q", "-m", msg)
}

func rootOf(t *testing.T, dir string) string {
	t.Helper()

	repo, err := git.Open(context.Background(), dir)
	if err != nil {
		t.Fatal(err)
	}

	return repo.Root
}

// saveToPaneCeiling bounds the median save-to-pane time the test asserts. The
// 300 ms gate is read from the logged median on a quiet machine.
const saveToPaneCeiling = time.Second

// A file saved in a 2,000-file repository reaches the open drawer. The test
// asserts a median ceiling of one second and logs the median and p95; the
// watcher has its default debounce, as in the app. Run it with:
//
//	CGO_ENABLED=1 go test -tags bench ./internal/ui -run TestReviewSaveToPane -v
func TestReviewSaveToPane(t *testing.T) {
	const (
		files = 2000
		saves = 20
	)

	set := git.NewWatchSet(context.Background(), git.WatchOptions{})

	t.Cleanup(func() { _ = set.Close() })

	r := startDrawerRig(t, func(o *Options) { o.Git, o.Watches = SystemGit{}, set })
	r.wait = 60 * time.Second

	dir := newRepoDir(t)
	for i := range files {
		write(t, filepath.Join(dir, fmt.Sprintf("d%02d/f%04d.txt", i%50, i)), numberedLines("line", 20))
	}

	commitEverything(t, dir, "base")
	gitIn(t, dir, "checkout", "-q", "-b", "feature")

	// Files written in the same second as the index are racily clean: git must
	// hash them again until the index is written once the second has passed.
	time.Sleep(1500 * time.Millisecond)
	gitIn(t, dir, "status", "--porcelain")

	root := rootOf(t, dir)
	id := r.shell(t, dir)

	v := r.open(t, id)
	r.until(t, "the worktree to be watched", func() bool {
		_, ok := set.Seq(root)

		return r.held(id) == root && ok
	})
	r.quiet(t, 400*time.Millisecond)

	if len(v.res.Files) != 0 {
		t.Fatalf("setup: %d changed files", len(v.res.Files))
	}

	took := make([]time.Duration, 0, saves)

	for i := range saves {
		n := (i*97 + 13) % files
		rel := fmt.Sprintf("d%02d/f%04d.txt", n%50, n)
		text := fmt.Sprintf("saved %d", i)

		r.quiet(t, 250*time.Millisecond)

		start := time.Now()

		write(t, filepath.Join(dir, rel), text+"\n")
		r.until(t, "save "+rel+" to reach the drawer", func() bool { return hunkHolds(v, rel, text) })

		took = append(took, time.Since(start))
	}

	slices.Sort(took)

	median, p95 := took[len(took)/2], took[len(took)*95/100]
	t.Logf("save to pane over %d saves: median %v, p95 %v, max %v", saves, median, p95, took[len(took)-1])

	if median > saveToPaneCeiling {
		t.Errorf("median save to pane is %v, want at most %v (p95 %v)", median, saveToPaneCeiling, p95)
	}
}

// stepRatio is how many times what a resize costs with 1 KiB lines a resize may
// cost with 1 MiB lines: what is drawn of a line must not depend on its length,
// which would show as a ratio in the hundreds. Both are timed in the same run,
// one after the other, so machine load moves them together.
const stepRatio = 10

// TestBenchDrawerResizeWithLongLines bounds what a drawer resize costs with
// 1 MiB lines against 1 KiB lines, summed over every width, for each icons
// setting and view variant. Run it with:
//
//	CGO_ENABLED=1 go test -tags bench ./internal/ui -run TestBenchDrawerResizeWithLongLines -v
func TestBenchDrawerResizeWithLongLines(t *testing.T) {
	for _, icons := range []bool{true, false} {
		for _, vr := range rowWidthVariants() {
			t.Run(fmt.Sprintf("icons=%v/%s", icons, vr.name), func(t *testing.T) {
				a, baseline := rowWidthApp(t, icons, vr, megabyteLines()), rowWidthApp(t, icons, vr, kilobyteLines())

				var slow, base time.Duration

				for _, w := range rowWidths(icons) {
					start := time.Now()
					_, _, _ = resizeDrawer(t, baseline, w)
					mid := time.Now()
					_, _, _ = resizeDrawer(t, a, w)

					base, slow = base+mid.Sub(start), slow+time.Since(mid)
				}

				t.Logf("resizes: %v with 1 MiB lines, %v with 1 KiB lines", slow, base)

				if slow > stepRatio*base {
					t.Errorf("the resizes took %v with 1 MiB lines and %v with 1 KiB lines, want at most %dx", slow, base, stepRatio)
				}
			})
		}
	}
}
