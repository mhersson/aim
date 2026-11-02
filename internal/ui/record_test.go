package ui

import (
	"context"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/mhersson/aim/internal/core"
	"github.com/mhersson/aim/internal/term"
	"github.com/mhersson/aim/internal/term/rec"
)

// shCommand runs script instead of the pane's login shell.
func shCommand(script string) func(context.Context, term.Command, int, int) (proc, error) {
	return func(ctx context.Context, c term.Command, cols, rows int) (proc, error) {
		c.Argv = []string{"/bin/sh", "-c", script}

		return termStart(ctx, c, cols, rows)
	}
}

func TestRecordFlag(t *testing.T) {
	t.Parallel()
	dir := filepath.Join(t.TempDir(), "rec")

	app, err := New(Options{
		caches: testDrawCaches,
		Git:    newFakeGit(t),
		Logger: testLogger(), Store: core.NewStore(core.InitialState(core.DefaultConfig())),
		Cwd: t.TempDir(), RecordDir: dir,
	})
	if err != nil {
		t.Fatal(err)
	}

	// A copy must never reach the operator's real clipboard tools.
	app.clip.run = (&fakeRun{}).run
	app.openLink = (&fakeOpen{}).open
	app.tick = (&ticked{}).tick

	app.start = shCommand("printf hi")

	t.Cleanup(func() { _ = app.Close(context.Background()) })

	app.Update(tea.WindowSizeMsg{Width: 40, Height: 11})

	pn := app.panes["p1"]
	if pn == nil {
		t.Fatal("no pane opened")
	}

	// The read loop ends once the child's output is drained.
	select {
	case <-pn.exited:
	case <-time.After(testDeadline):
		t.Fatal("pane output did not end")
	}

	ctx, cancel := context.WithTimeout(context.Background(), testDeadline)
	defer cancel()

	if err := app.Close(ctx); err != nil {
		t.Fatal(err)
	}

	st, err := os.Stat(dir)
	if err != nil || st.Mode().Perm() != 0o700 {
		t.Fatalf("dir = %v, %v; want mode 0700", st, err)
	}

	path := filepath.Join(dir, "p1.rec")

	fi, err := os.Stat(path)
	if err != nil || fi.Mode().Perm() != 0o600 {
		t.Fatalf("file = %v, %v; want mode 0600", fi, err)
	}

	recs, err := rec.Load(path)
	if err != nil {
		t.Fatal(err)
	}

	if recs[0].Data != nil || recs[0].Cols != 38 || recs[0].Rows != 7 {
		t.Fatalf("initial record = %+v, want a 38x7 resize", recs[0])
	}

	vt := term.NewVT(recs[0].Cols, recs[0].Rows, io.Discard)
	defer func() { _ = vt.Close() }()

	for _, r := range recs[1:] {
		if r.Data != nil {
			_, _ = vt.Write(r.Data)
		}
	}

	var row strings.Builder

	for x := range 2 {
		row.WriteString(vt.CellAt(x, 0).String())
	}

	if row.String() != "hi" {
		t.Fatalf("replayed row 0 = %q, want hi", row.String())
	}
}

func TestRecordTapRecordsResizes(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	clk := &fakeClock{t: t0}

	tp, err := openTap(testLogger(), dir, "p9", 20, 5, clk.Now)
	if err != nil {
		t.Fatal(err)
	}

	fp := newFakeProc()
	p := newPane("p9", fp, 20, 5, clk, withTap(tp))

	clk.Advance(time.Millisecond)
	p.resize(30, 6)

	if err := p.close(); err != nil {
		t.Fatal(err)
	}

	recs, err := rec.Load(filepath.Join(dir, "p9.rec"))
	if err != nil {
		t.Fatal(err)
	}

	if len(recs) != 2 || recs[1].Cols != 30 || recs[1].Rows != 6 {
		t.Fatalf("records = %+v, want the initial size and the resize", recs)
	}
}

// failAfter accepts n writes, then fails every write and counts the attempts.
type failAfter struct{ ok, calls int }

func (f *failAfter) Write(b []byte) (int, error) {
	f.calls++

	if f.calls > f.ok {
		return 0, io.ErrClosedPipe
	}

	return len(b), nil
}

type nopCloser struct{}

func (nopCloser) Close() error { return nil }

func TestRecordTapFailureStopsRecording(t *testing.T) {
	t.Parallel()

	var logs strings.Builder

	log := slog.New(slog.NewTextHandler(&logs, nil))
	// The header and the initial size succeed; the next write fails.
	fw := &failAfter{ok: 2}

	w, err := rec.NewWriter(fw, 20, 5, time.Now)
	if err != nil {
		t.Fatal(err)
	}

	tp := startTap(log, "p1", nopCloser{}, w, time.Now, 8)

	tp.data([]byte("x"))
	tp.data([]byte("y"))
	tp.resize(10, 10)

	if err := tp.close(); err != nil {
		t.Fatal(err)
	}

	if !tp.failed {
		t.Fatal("tap kept recording after a write error")
	}

	if fw.calls != 3 {
		t.Errorf("writes = %d, want the header, the size and the one failing record", fw.calls)
	}

	if n := strings.Count(logs.String(), "recording stopped"); n != 1 {
		t.Errorf("failure logged %d times, want once:\n%s", n, logs.String())
	}
}

// blockedWriter passes the first pass writes, then holds every write until release closes.
type blockedWriter struct {
	pass    int
	release chan struct{}
	blocked chan struct{}
	calls   int
	once    sync.Once
}

func (b *blockedWriter) Write(p []byte) (int, error) {
	b.calls++
	if b.calls <= b.pass {
		return len(p), nil
	}

	b.once.Do(func() { close(b.blocked) })
	<-b.release

	return len(p), nil
}

func TestRecordTapDropsWhenBehind(t *testing.T) {
	t.Parallel()

	var logs strings.Builder

	log := slog.New(slog.NewTextHandler(&logs, nil))
	// The header and the initial size pass; the first record blocks.
	bw := &blockedWriter{pass: 2, release: make(chan struct{}), blocked: make(chan struct{})}

	w, err := rec.NewWriter(bw, 20, 5, time.Now)
	if err != nil {
		t.Fatal(err)
	}

	tp := startTap(log, "p1", nopCloser{}, w, time.Now, 2)

	tp.data([]byte("a"))
	<-bw.blocked

	done := make(chan struct{})

	go func() {
		defer close(done)

		for range 50 {
			tp.data([]byte("b"))
			tp.resize(10, 10)
		}
	}()

	select {
	case <-done:
	case <-time.After(testDeadline):
		close(bw.release)
		t.Fatal("enqueue blocked behind a stalled writer")
	}

	close(bw.release)

	if err := tp.close(); err != nil {
		t.Fatal(err)
	}

	if n := strings.Count(logs.String(), "falling behind"); n != 1 {
		t.Errorf("overflow logged %d times, want once:\n%s", n, logs.String())
	}
}

func TestRecordDirCollisionSetsNotice(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()

	if err := os.WriteFile(filepath.Join(dir, "p1.rec"), nil, 0o600); err != nil {
		t.Fatal(err)
	}

	app, err := New(Options{
		caches: testDrawCaches,
		Git:    newFakeGit(t),
		Logger: testLogger(), Store: core.NewStore(core.InitialState(core.DefaultConfig())),
		Cwd: t.TempDir(), RecordDir: dir,
	})
	if err != nil {
		t.Fatal(err)
	}

	// A copy must never reach the operator's real clipboard tools.
	app.clip.run = (&fakeRun{}).run
	app.openLink = (&fakeOpen{}).open
	app.tick = (&ticked{}).tick

	app.start = func(context.Context, term.Command, int, int) (proc, error) { return newFakeProc(), nil }

	t.Cleanup(func() { _ = app.Close(context.Background()) })

	app.Update(tea.WindowSizeMsg{Width: 40, Height: 11})

	if app.panes["p1"] == nil || !strings.Contains(app.notice, "recording is off for p1") {
		t.Fatalf("pane = %v, notice = %q; want the pane running and a recording notice", app.panes["p1"], app.notice)
	}
}

func TestOpenTapRefusesExistingFile(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()

	tp, err := openTap(testLogger(), dir, "p1", 20, 5, time.Now)
	if err != nil {
		t.Fatal(err)
	}

	defer func() { _ = tp.close() }()

	if _, err := openTap(testLogger(), dir, "p1", 20, 5, time.Now); err == nil {
		t.Fatal("second recording overwrote the first")
	}
}
