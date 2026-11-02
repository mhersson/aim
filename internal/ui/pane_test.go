package ui

import (
	"bytes"
	"io"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	uv "github.com/charmbracelet/ultraviolet"
	"github.com/charmbracelet/x/ansi"

	"github.com/mhersson/aim/internal/term"
)

var _ proc = (*term.Process)(nil)

var t0 = time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

const testDeadline = 5 * time.Second

// fakeProc feeds the pane from an io.Pipe and records what the pane writes.
type fakeProc struct {
	out  *io.PipeReader
	feed *io.PipeWriter
	done chan struct{}

	mu       sync.Mutex
	written  bytes.Buffer
	resizes  [][2]int
	cwd      string
	fg       string // foreground process name; "" reads as an error
	closed   bool
	closeOne sync.Once

	// beforeBarrier, when set, runs before feedAndWait's empty write.
	beforeBarrier func()
}

func newFakeProc() *fakeProc {
	r, w := io.Pipe()

	return &fakeProc{out: r, feed: w, done: make(chan struct{})}
}

func (f *fakeProc) Read(b []byte) (int, error) { return f.out.Read(b) }

func (f *fakeProc) Write(b []byte) (int, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	return f.written.Write(b)
}

func (f *fakeProc) Resize(cols, rows int) error {
	f.mu.Lock()
	defer f.mu.Unlock()

	f.resizes = append(f.resizes, [2]int{cols, rows})

	return nil
}

func (f *fakeProc) Done() <-chan struct{} { return f.done }

func (f *fakeProc) setCwd(dir string) {
	f.mu.Lock()
	defer f.mu.Unlock()

	f.cwd = dir
}

func (f *fakeProc) Cwd() (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	if f.cwd == "" {
		return "", io.ErrUnexpectedEOF
	}

	return f.cwd, nil
}

func (f *fakeProc) Foreground() (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	if f.fg == "" {
		return "", io.ErrUnexpectedEOF
	}

	return f.fg, nil
}

func (f *fakeProc) setFg(name string) {
	f.mu.Lock()
	defer f.mu.Unlock()

	f.fg = name
}

func (f *fakeProc) Close() error {
	f.closeOne.Do(func() {
		f.mu.Lock()
		f.closed = true
		f.mu.Unlock()
		close(f.done)
	})

	return f.out.Close()
}

func (f *fakeProc) sent() string {
	f.mu.Lock()
	defer f.mu.Unlock()

	return f.written.String()
}

func (f *fakeProc) resized() [][2]int {
	f.mu.Lock()
	defer f.mu.Unlock()

	return append([][2]int(nil), f.resizes...)
}

func newTestPane(t *testing.T, cols, rows int, c clock) (*pane, *fakeProc) {
	t.Helper()

	fp := newFakeProc()
	p := newPane("p1", fp, cols, rows, c)

	t.Cleanup(func() { _ = p.close() })

	return p, fp
}

// next runs a wait command and returns its message, failing after the deadline.
func next(t *testing.T, p *pane) tea.Msg {
	t.Helper()

	ch := make(chan tea.Msg, 1)

	go func() { ch <- p.wait()() }()

	select {
	case m := <-ch:
		return m
	case <-time.After(testDeadline):
		t.Fatal("wait did not return")

		return nil
	}
}

// feedAndWait writes s as one chunk and waits for a dirty message. Its empty
// write returns only once the reader has fed the chunk, so s is in the emulator.
func feedAndWait(t *testing.T, p *pane, fp *fakeProc, s string) {
	t.Helper()

	if _, err := fp.feed.Write([]byte(s)); err != nil {
		t.Fatal(err)
	}

	if fp.beforeBarrier != nil {
		fp.beforeBarrier()
	}

	if _, err := fp.feed.Write(nil); err != nil {
		t.Fatal(err)
	}

	if m, ok := next(t, p).(paneDirtyMsg); !ok {
		t.Fatalf("message = %T, want paneDirtyMsg", m)
	}
}

func eventually(t *testing.T, what string, cond func() bool) {
	t.Helper()

	deadline := time.Now().Add(testDeadline)

	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}

		time.Sleep(time.Millisecond)
	}
}

// gatedProc holds each chunk back from the pane until gate is closed.
type gatedProc struct {
	*fakeProc
	gate chan struct{}
}

func (g *gatedProc) Read(b []byte) (int, error) {
	n, err := g.fakeProc.Read(b)
	if n > 0 {
		<-g.gate
	}

	return n, err
}

func TestFeedAndWaitIgnoresStaleDirty(t *testing.T) {
	t.Parallel()

	fp := newFakeProc()
	g := &gatedProc{fakeProc: fp, gate: make(chan struct{})}
	p := newPane("p1", g, 20, 3, realClock{})

	t.Cleanup(func() { _ = p.close() })

	// The gate opens at the barrier, so only a helper that waits for the feed
	// can return with the text on screen.
	var once sync.Once

	open := func() { once.Do(func() { close(g.gate) }) }
	fp.beforeBarrier = open

	t.Cleanup(open)

	// A notification from an earlier feed that nobody consumed.
	p.dirty <- struct{}{}

	feedAndWait(t, p, fp, "ZZZ")
	p.render()

	if !strings.HasPrefix(p.lines()[0], "ZZZ") {
		t.Fatalf("row 0 = %q, want the feed on screen", p.lines()[0])
	}
}

func TestPaneRendersWrites(t *testing.T) {
	t.Parallel()
	p, fp := newTestPane(t, 20, 3, realClock{})

	feedAndWait(t, p, fp, "hello")

	changed, held := p.render()
	if !changed || !held.IsZero() {
		t.Fatalf("render = %v, %v", changed, held)
	}

	if !strings.HasPrefix(p.lines()[0], "hello") {
		t.Fatalf("row 0 = %q", p.lines()[0])
	}

	if changed, _ := p.render(); changed {
		t.Fatal("second render reported a change")
	}
}

func TestCoalescedNotify(t *testing.T) {
	t.Parallel()
	p, fp := newTestPane(t, 20, 3, realClock{})

	for range 1000 {
		if _, err := fp.feed.Write([]byte("x")); err != nil {
			t.Fatal(err)
		}
	}

	if _, err := fp.feed.Write([]byte("\x1b[1;1HZ")); err != nil {
		t.Fatal(err)
	}

	// The last chunk is fed once the cache shows it.
	eventually(t, "last chunk", func() bool {
		p.render()

		return strings.HasPrefix(ansi.Strip(p.lines()[0]), "Z")
	})

	if m, ok := next(t, p).(paneDirtyMsg); !ok {
		t.Fatalf("message = %T, want paneDirtyMsg", m)
	}

	p.render()

	feedAndWait(t, p, fp, "y")
}

func TestSyncHold(t *testing.T) {
	t.Parallel()
	t.Run("close", func(t *testing.T) {
		clk := &fakeClock{t: t0}
		p, fp := newTestPane(t, 20, 3, clk)

		feedAndWait(t, p, fp, "\x1b[?2026hhello")

		changed, until := p.render()
		if changed || !until.Equal(t0.Add(syncMaxHold)) {
			t.Fatalf("held render = %v, %v; want false, %v", changed, until, t0.Add(syncMaxHold))
		}

		if strings.HasPrefix(p.lines()[0], "hello") {
			t.Fatal("rows drawn during hold")
		}

		clk.Advance(500 * time.Millisecond)

		if changed, until := p.render(); changed || until.IsZero() {
			t.Fatalf("still held render = %v, %v", changed, until)
		}

		feedAndWait(t, p, fp, "\x1b[?2026l")

		changed, until = p.render()
		if !changed || !until.IsZero() {
			t.Fatalf("released render = %v, %v", changed, until)
		}

		if !strings.HasPrefix(p.lines()[0], "hello") {
			t.Fatalf("row 0 = %q", p.lines()[0])
		}
	})

	t.Run("never closed", func(t *testing.T) {
		clk := &fakeClock{t: t0}
		p, fp := newTestPane(t, 20, 3, clk)

		feedAndWait(t, p, fp, "\x1b[?2026hhello")
		p.render()

		clk.Set(t0.Add(syncMaxHold - time.Millisecond))

		if changed, until := p.render(); changed || until.IsZero() {
			t.Fatalf("render just before the bound = %v, %v", changed, until)
		}

		clk.Set(t0.Add(syncMaxHold))

		changed, until := p.render()
		if !changed || !until.IsZero() {
			t.Fatalf("timed-out render = %v, %v", changed, until)
		}

		if !strings.HasPrefix(p.lines()[0], "hello") {
			t.Fatalf("row 0 = %q", p.lines()[0])
		}
	})

	t.Run("new serial restarts", func(t *testing.T) {
		clk := &fakeClock{t: t0}
		p, fp := newTestPane(t, 20, 3, clk)

		feedAndWait(t, p, fp, "\x1b[?2026h")
		clk.Advance(800 * time.Millisecond)
		feedAndWait(t, p, fp, "\x1b[?2026l\x1b[?2026h")

		_, until := p.render()
		if want := t0.Add(800*time.Millisecond + syncMaxHold); !until.Equal(want) {
			t.Fatalf("heldUntil = %v, want %v", until, want)
		}
	})
}

func TestHoldTick(t *testing.T) {
	t.Parallel()
	clk := &fakeClock{t: t0}
	p, _ := newTestPane(t, 20, 3, clk)

	msg := p.holdTick(t0.Add(-time.Second))()
	if m, ok := msg.(holdExpiredMsg); !ok || m.id != "p1" {
		t.Fatalf("message = %#v, want holdExpiredMsg for p1", msg)
	}
}

func TestResizeSameSizeSkips(t *testing.T) {
	t.Parallel()
	p, fp := newTestPane(t, 20, 3, realClock{})

	if p.resize(20, 3) {
		t.Fatal("same size reported a resize")
	}

	if len(fp.resized()) != 0 {
		t.Fatalf("process resized on same size: %v", fp.resized())
	}
}

func TestResizeRendersAtOnce(t *testing.T) {
	t.Parallel()
	p, fp := newTestPane(t, 20, 3, realClock{})

	feedAndWait(t, p, fp, "hello")
	p.render()

	if !p.resize(12, 5) {
		t.Fatal("resize reported no change")
	}

	if got := fp.resized(); len(got) != 1 || got[0] != [2]int{12, 5} {
		t.Fatalf("process resizes = %v", got)
	}

	if rows := p.lines(); len(rows) != 5 || ansi.StringWidth(rows[0]) != 12 {
		t.Fatalf("blank cache = %d rows, row 0 %q", len(rows), rows[0])
	}

	changed, held := p.render()
	if !changed || !held.IsZero() {
		t.Fatalf("render after resize = %v, %v", changed, held)
	}

	for y, row := range p.lines() {
		if w := ansi.StringWidth(row); w != 12 {
			t.Fatalf("row %d width = %d, want 12", y, w)
		}
	}

	if !strings.HasPrefix(p.lines()[0], "hello") {
		t.Fatalf("row 0 = %q", p.lines()[0])
	}
}

func TestExitNotifies(t *testing.T) {
	t.Parallel()
	p, fp := newTestPane(t, 20, 3, realClock{})

	feedAndWait(t, p, fp, "bye")

	if err := fp.feed.Close(); err != nil {
		t.Fatal(err)
	}

	if m, ok := next(t, p).(paneExitedMsg); !ok || m.id != "p1" {
		t.Fatalf("message = %#v, want paneExitedMsg for p1", m)
	}

	// The exit stays visible to every later wait.
	if _, ok := next(t, p).(paneExitedMsg); !ok {
		t.Fatal("second wait after exit did not report the exit")
	}
}

func TestExitReportsPendingOutputFirst(t *testing.T) {
	t.Parallel()
	p, fp := newTestPane(t, 20, 3, realClock{})

	if _, err := fp.feed.Write([]byte("last words")); err != nil {
		t.Fatal(err)
	}

	_ = fp.feed.Close()

	eventually(t, "exit", func() bool {
		select {
		case <-p.exited:
			return true
		default:
			return false
		}
	})

	if _, ok := next(t, p).(paneDirtyMsg); !ok {
		t.Fatal("pending output was not reported before the exit")
	}

	if _, ok := next(t, p).(paneExitedMsg); !ok {
		t.Fatal("exit not reported after the output")
	}
}

func TestSendKeyPasteFocus(t *testing.T) {
	t.Parallel()
	p, fp := newTestPane(t, 20, 3, realClock{})

	p.sendKey(uv.KeyPressEvent{Code: 'a', Text: "a"})
	eventually(t, "key", func() bool { return fp.sent() == "a" })

	p.sendPaste("one")
	eventually(t, "plain paste", func() bool { return fp.sent() == "aone" })

	feedAndWait(t, p, fp, "\x1b[?2004h")
	p.sendPaste("two")
	eventually(t, "bracketed paste", func() bool { return fp.sent() == "aone\x1b[200~two\x1b[201~" })

	before := fp.sent()

	p.sendFocus(true)
	p.sendFocus(false)

	// A later write proves the drain went past any focus bytes.
	p.sendKey(uv.KeyPressEvent{Code: 'b', Text: "b"})
	eventually(t, "marker key", func() bool { return fp.sent() == before+"b" })

	feedAndWait(t, p, fp, "\x1b[?1004h")
	p.sendFocus(true)
	p.sendFocus(false)
	eventually(t, "focus reports", func() bool { return fp.sent() == before+"b\x1b[I\x1b[O" })
}

func TestWorkingDirAndTitle(t *testing.T) {
	t.Parallel()
	p, fp := newTestPane(t, 20, 3, realClock{})

	if d := p.workingDir(); d != "" {
		t.Fatalf("workingDir = %q, want empty", d)
	}

	fp.cwd = "/from/proc"

	if d := p.workingDir(); d != "/from/proc" {
		t.Fatalf("workingDir = %q, want proc cwd", d)
	}

	feedAndWait(t, p, fp, "\x1b]7;file:///from/osc\x07\x1b]2;my title\x07")

	if d := p.workingDir(); d != "/from/osc" {
		t.Fatalf("workingDir = %q, want OSC 7 path", d)
	}

	if title := p.title(); title != "my title" {
		t.Fatalf("title = %q", title)
	}
}

func TestCloseStopsLoop(t *testing.T) {
	base := runtime.NumGoroutine()

	fp := newFakeProc()
	p := newPane("p1", fp, 20, 3, realClock{})

	if err := p.close(); err != nil {
		t.Fatalf("close = %v", err)
	}

	if err := p.close(); err != nil {
		t.Fatalf("second close = %v", err)
	}

	select {
	case <-p.exited:
	default:
		t.Fatal("read loop still running after close")
	}

	if _, ok := next(t, p).(paneExitedMsg); !ok {
		t.Fatal("wait after close did not report the exit")
	}

	// Key input after close is dropped without panicking.
	p.sendKey(uv.KeyPressEvent{Code: 'a', Text: "a"})

	eventually(t, "goroutines back to baseline", func() bool { return runtime.NumGoroutine() <= base })
}

func TestConcurrentFeedRender(t *testing.T) {
	t.Parallel()
	p, fp := newTestPane(t, 40, 10, realClock{})

	var chunk strings.Builder
	for i := range 20 {
		chunk.WriteString("\x1b[3")
		chunk.WriteByte(byte('0' + i%8))
		chunk.WriteString("mline ")
		chunk.WriteString(strings.Repeat("x", i))
		chunk.WriteString("\x1b[0m\r\n")
	}

	done := make(chan struct{})

	go func() {
		defer close(done)

		for range 200 {
			if _, err := fp.feed.Write([]byte(chunk.String())); err != nil {
				return
			}
		}

		_ = fp.feed.Close()
	}()

	for running := true; running; {
		select {
		case <-done:
			running = false
		default:
			p.render()
			_ = p.lines()
			p.cursor()
		}
	}

	eventually(t, "loop exit", func() bool {
		select {
		case <-p.exited:
			return true
		default:
			return false
		}
	})

	p.render()

	for y, row := range p.lines() {
		if w := ansi.StringWidth(row); w != 40 {
			t.Fatalf("row %d width = %d, want 40", y, w)
		}
	}
}
