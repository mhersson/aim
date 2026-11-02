package term

import (
	"bytes"
	"errors"
	"image/color"
	"io"
	"os"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	uv "github.com/charmbracelet/ultraviolet"
)

type safeBuf struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (s *safeBuf) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	return s.b.Write(p)
}

func (s *safeBuf) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()

	return s.b.String()
}

func waitFor(t *testing.T, cond func() bool) {
	t.Helper()

	deadline := time.Now().Add(2 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatal("condition not met before deadline")
		}

		time.Sleep(time.Millisecond)
	}
}

func newTestVT(t *testing.T, cols, rows int) (VT, *safeBuf) {
	t.Helper()

	reply := &safeBuf{}
	v := NewVT(cols, rows, reply)

	t.Cleanup(func() { _ = v.Close() })

	return v, reply
}

func TestWriteAndCells(t *testing.T) {
	v, _ := newTestVT(t, 20, 5)

	if _, err := v.Write([]byte("hi\x1b[38;2;1;2;3mX中")); err != nil {
		t.Fatal(err)
	}

	if c := v.CellAt(0, 0); c == nil || c.Content != "h" {
		t.Fatalf("cell 0 = %+v", c)
	}

	c := v.CellAt(2, 0)
	if c == nil || c.Content != "X" {
		t.Fatalf("cell 2 = %+v", c)
	}

	r, g, b, _ := c.Style.Fg.RGBA()
	if got := (color.RGBA{uint8(r >> 8), uint8(g >> 8), uint8(b >> 8), 0}); got.R != 1 || got.G != 2 || got.B != 3 {
		t.Fatalf("fg = %+v", got)
	}

	if w := v.CellAt(3, 0); w == nil || w.Content != "中" || w.Width != 2 {
		t.Fatalf("wide cell = %+v", w)
	}

	if v.CellAt(-1, 0) != nil || v.CellAt(0, 5) != nil || v.CellAt(20, 0) != nil {
		t.Fatal("cells outside the grid must be nil")
	}
}

func TestSyncState(t *testing.T) {
	tests := []struct {
		name   string
		in     string
		open   bool
		serial uint64
	}{
		{"open", "\x1b[?2026h", true, 1},
		{"open then close", "\x1b[?2026h\x1b[?2026l", false, 1},
		{"reopen", "\x1b[?2026h\x1b[?2026l\x1b[?2026h", true, 2},
		{"open twice", "\x1b[?2026h\x1b[?2026h", true, 1},
		{"other mode ignored", "\x1b[?25l", false, 0},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			v, _ := newTestVT(t, 10, 3)
			if _, err := v.Write([]byte(tc.in)); err != nil {
				t.Fatal(err)
			}

			open, serial := v.SyncState()
			if open != tc.open || serial != tc.serial {
				t.Fatalf("SyncState = %v, %d; want %v, %d", open, serial, tc.open, tc.serial)
			}
		})
	}
}

func TestTitleAndWorkingDir(t *testing.T) {
	v, _ := newTestVT(t, 10, 3)

	if v.Title() != "" || v.WorkingDir() != "" {
		t.Fatal("title and working dir must start empty")
	}

	if _, err := v.Write([]byte("\x1b]2;build\x07\x1b]7;file:///tmp/x%20y\x07")); err != nil {
		t.Fatal(err)
	}

	if got := v.Title(); got != "build" {
		t.Fatalf("Title = %q", got)
	}

	if got := v.WorkingDir(); got != "/tmp/x y" {
		t.Fatalf("WorkingDir = %q", got)
	}
}

func TestWorkingDirIgnoresRemoteHosts(t *testing.T) {
	local, err := os.Hostname()
	if err != nil {
		t.Skipf("no hostname: %v", err)
	}

	tests := []struct {
		name string
		osc  string
		want string
	}{
		{"empty host", "file:///a", "/a"},
		{"localhost", "file://localhost/b", "/b"},
		{"local hostname", "file://" + local + "/c", "/c"},
		{"remote host keeps the last path", "file://remote.example/d", "/c"},
		{"bare path", "/e", "/e"},
	}

	v, _ := newTestVT(t, 10, 3)

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := v.Write([]byte("\x1b]7;" + tt.osc + "\x07")); err != nil {
				t.Fatal(err)
			}

			if got := v.WorkingDir(); got != tt.want {
				t.Errorf("WorkingDir = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestRepliesOrdered(t *testing.T) {
	v, reply := newTestVT(t, 10, 3)

	if _, err := v.Write([]byte("\x1b[c")); err != nil {
		t.Fatal(err)
	}

	v.SendBytes([]byte("k"))

	waitFor(t, func() bool {
		s := reply.String()

		return len(s) > 1 && s[len(s)-1] == 'k'
	})

	s := reply.String()
	if s[:2] != "\x1b[" || bytes.Count([]byte(s), []byte("k")) != 1 {
		t.Fatalf("reply = %q; want DA1 reply followed by k", s)
	}
}

func TestFocusReportingAndModes(t *testing.T) {
	v, _ := newTestVT(t, 10, 3)

	if v.FocusReporting() || v.BracketedPaste() || v.KeyModes() != (KeyModes{}) {
		t.Fatal("modes must start off")
	}

	if _, err := v.Write([]byte("\x1b[?1004h\x1b[?2004h\x1b[>1u\x1b[?1h")); err != nil {
		t.Fatal(err)
	}

	if !v.FocusReporting() {
		t.Fatal("FocusReporting not set")
	}

	if !v.BracketedPaste() {
		t.Fatal("BracketedPaste not set")
	}

	km := v.KeyModes()
	if km.KittyFlags != 1 || !km.AppCursor {
		t.Fatalf("KeyModes = %+v", km)
	}

	if _, err := v.Write([]byte("\x1b=")); err != nil {
		t.Fatal(err)
	}

	if !v.KeyModes().AppKeypad {
		t.Fatal("AppKeypad not set")
	}
}

func TestResizeAndSize(t *testing.T) {
	v, _ := newTestVT(t, 20, 5)

	if c, r := v.Size(); c != 20 || r != 5 {
		t.Fatalf("Size = %d, %d", c, r)
	}

	v.Resize(30, 8)

	if c, r := v.Size(); c != 30 || r != 8 {
		t.Fatalf("Size after resize = %d, %d", c, r)
	}

	if v.CellAt(29, 7) == nil || v.CellAt(30, 0) != nil {
		t.Fatal("grid bounds did not follow the resize")
	}
}

func TestHistoryTrimmed(t *testing.T) {
	t.Run("ring overflow", func(t *testing.T) {
		v, _ := newTestVT(t, 10, 3)
		total := ScrollbackLines + 500

		for range total {
			if _, err := v.Write([]byte("x\r\n")); err != nil {
				t.Fatal(err)
			}
		}

		if got := v.ScrollbackLen(); got != ScrollbackLines {
			t.Fatalf("ScrollbackLen = %d, want the cap %d", got, ScrollbackLines)
		}

		// Every line that left the screen is in history or was trimmed from it.
		if got := int(v.HistoryTrimmed()) + v.ScrollbackLen(); got != total-2 {
			t.Fatalf("trimmed + len = %d, want %d", got, total-2)
		}

		if v.HistoryTrimmed() == 0 {
			t.Fatal("HistoryTrimmed did not move past the cap")
		}
	})

	t.Run("clear scrollback", func(t *testing.T) {
		v, _ := newTestVT(t, 10, 3)

		for range 20 {
			if _, err := v.Write([]byte("x\r\n")); err != nil {
				t.Fatal(err)
			}
		}

		n := v.ScrollbackLen()

		if _, err := v.Write([]byte("\x1b[3J")); err != nil {
			t.Fatal(err)
		}

		if v.ScrollbackLen() != 0 || int(v.HistoryTrimmed()) != n {
			t.Fatalf("after CSI 3J: len = %d, trimmed = %d, want 0 and %d", v.ScrollbackLen(), v.HistoryTrimmed(), n)
		}
	})

	t.Run("no trim", func(t *testing.T) {
		v, _ := newTestVT(t, 10, 3)

		if _, err := v.Write([]byte("a\r\nb\r\nc\r\nd\r\n")); err != nil {
			t.Fatal(err)
		}

		if v.HistoryTrimmed() != 0 {
			t.Fatalf("HistoryTrimmed = %d, want 0", v.HistoryTrimmed())
		}
	})
}

func TestCloseIdempotent(t *testing.T) {
	base := runtime.NumGoroutine()

	v := NewVT(10, 3, &safeBuf{})

	if err := v.Close(); err != nil {
		t.Fatal(err)
	}

	if err := v.Close(); err != nil {
		t.Fatalf("second Close = %v", err)
	}

	waitFor(t, func() bool { return runtime.NumGoroutine() <= base })
}

type failOnce struct {
	mu    sync.Mutex
	calls int
}

func (f *failOnce) Write(p []byte) (int, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	f.calls++
	if f.calls == 1 {
		return 0, errors.New("broken pipe")
	}

	return len(p), nil
}

// feedSrc is a reply source whose unread byte count a test can wait on.
type feedSrc struct {
	mu     sync.Mutex
	cond   *sync.Cond
	buf    []byte
	closed bool
}

func newFeedSrc() *feedSrc {
	f := &feedSrc{}
	f.cond = sync.NewCond(&f.mu)

	return f
}

func (f *feedSrc) put(b string) {
	f.mu.Lock()
	defer f.mu.Unlock()

	f.buf = append(f.buf, b...)
	f.cond.Broadcast()
}

func (f *feedSrc) shut() {
	f.mu.Lock()
	defer f.mu.Unlock()

	f.closed = true
	f.cond.Broadcast()
}

func (f *feedSrc) unread() int {
	f.mu.Lock()
	defer f.mu.Unlock()

	return len(f.buf)
}

func (f *feedSrc) Read(p []byte) (int, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	for len(f.buf) == 0 && !f.closed {
		f.cond.Wait()
	}

	if len(f.buf) == 0 {
		return 0, io.EOF
	}

	n := copy(p, f.buf)
	f.buf = f.buf[n:]

	return n, nil
}

// A stalled drain leaves the queued replies unread, so waitFor's bound is the
// hang guard; nothing reaches the writer again after its error.
func TestReplyWriteErrorKeepsDraining(t *testing.T) {
	const reply = "\x1b[?62;22c"

	src, w := newFeedSrc(), &failOnce{}
	d := startDrain(src, w)

	src.put(reply)
	waitFor(t, func() bool {
		w.mu.Lock()
		defer w.mu.Unlock()

		return w.calls == 1
	})

	for range 50 {
		src.put(reply)
	}

	waitFor(t, func() bool { return src.unread() == 0 })

	src.shut()

	select {
	case <-d.done:
	case <-time.After(5 * time.Second): // hang guard
		t.Fatal("drain did not stop after its source closed")
	}

	w.mu.Lock()
	defer w.mu.Unlock()

	if w.calls != 1 {
		t.Fatalf("writer called %d times; replies after its error must be dropped", w.calls)
	}
}

// replyAfterMarker returns everything on the reply stream up to a marker sent
// after the call under test; the stream is ordered, so nothing can follow it.
func replyAfterMarker(t *testing.T, v VT, reply *safeBuf) string {
	t.Helper()

	const marker = "\x00marker\x00"

	v.SendBytes([]byte(marker))
	waitFor(t, func() bool { return strings.HasSuffix(reply.String(), marker) })

	return strings.TrimSuffix(reply.String(), marker)
}

func lineText(cells []uv.Cell) string {
	var b strings.Builder

	for _, c := range cells {
		b.WriteString(c.Content)
	}

	return strings.TrimRight(b.String(), " ")
}

func TestScrollbackLine(t *testing.T) {
	v, _ := newTestVT(t, 10, 10)

	for i := range 30 {
		if _, err := v.Write([]byte("row" + string(rune('A'+i%26)) + "\r\n")); err != nil {
			t.Fatal(err)
		}
	}

	n := v.ScrollbackLen()
	if n == 0 {
		t.Fatal("no scrollback")
	}

	if got := lineText(v.ScrollbackLine(0)); got != "rowA" {
		t.Fatalf("oldest line = %q", got)
	}

	if got := lineText(v.ScrollbackLine(n - 1)); got != "row"+string(rune('A'+(n-1)%26)) {
		t.Fatalf("newest line = %q", got)
	}

	if v.ScrollbackLine(-1) != nil || v.ScrollbackLine(n) != nil {
		t.Fatal("out of range must be nil")
	}

	a := v.ScrollbackLine(0)
	a[0].Content = "Z"

	if got := lineText(v.ScrollbackLine(0)); got != "rowA" {
		t.Fatalf("mutating a returned line changed history: %q", got)
	}
}

func TestSoftWrapFlags(t *testing.T) {
	v, _ := newTestVT(t, 5, 3)

	// 7 chars wrap once on row 0; a hard newline follows.
	if _, err := v.Write([]byte("abcdefg\r\nxy")); err != nil {
		t.Fatal(err)
	}

	if !v.RowWrapped(0) || v.RowWrapped(1) || v.RowWrapped(2) {
		t.Fatalf("live wraps = %v %v %v", v.RowWrapped(0), v.RowWrapped(1), v.RowWrapped(2))
	}

	if v.RowWrapped(-1) || v.RowWrapped(3) {
		t.Fatal("out of range rows must not wrap")
	}

	if _, err := v.Write([]byte("\r\n\r\n\r\n")); err != nil {
		t.Fatal(err)
	}

	if v.ScrollbackLen() < 2 || !v.ScrollbackWrapped(0) || v.ScrollbackWrapped(1) {
		t.Fatalf("history wraps = %v %v (len %d)", v.ScrollbackWrapped(0), v.ScrollbackWrapped(1), v.ScrollbackLen())
	}

	if v.ScrollbackWrapped(-1) || v.ScrollbackWrapped(1000) {
		t.Fatal("out of range lines must not wrap")
	}
}

func TestAltScreen(t *testing.T) {
	v, _ := newTestVT(t, 10, 3)

	if v.AltScreen() {
		t.Fatal("alt screen must start off")
	}

	if _, err := v.Write([]byte("\x1b[?1049h")); err != nil {
		t.Fatal(err)
	}

	if !v.AltScreen() {
		t.Fatal("alt screen not set")
	}

	if _, err := v.Write([]byte("\x1b[?1049l")); err != nil {
		t.Fatal(err)
	}

	if v.AltScreen() {
		t.Fatal("alt screen not reset")
	}
}

func TestMouseTrackingAndSend(t *testing.T) {
	click := uv.MouseClickEvent(uv.Mouse{X: 2, Y: 3, Button: uv.MouseLeft})

	t.Run("tracking on", func(t *testing.T) {
		v, reply := newTestVT(t, 10, 5)

		if v.MouseTracking() {
			t.Fatal("tracking must start off")
		}

		if _, err := v.Write([]byte("\x1b[?1000h\x1b[?1006h")); err != nil {
			t.Fatal(err)
		}

		if !v.MouseTracking() {
			t.Fatal("tracking not set")
		}

		v.SendMouse(click)

		if got := replyAfterMarker(t, v, reply); got != "\x1b[<0;3;4M" {
			t.Fatalf("reply = %q", got)
		}
	})

	t.Run("tracking off", func(t *testing.T) {
		v, reply := newTestVT(t, 10, 5)

		v.SendMouse(click)

		if got := replyAfterMarker(t, v, reply); got != "" {
			t.Fatalf("reply = %q; want nothing", got)
		}
	})
}

func TestClipboardHandler(t *testing.T) {
	type call struct{ sel, content string }

	v, _ := newTestVT(t, 10, 3)

	var calls []call

	v.SetClipboardHandler(func(sel, content string) { calls = append(calls, call{sel, content}) })

	if _, err := v.Write([]byte("\x1b]52;c;aGk=\x07")); err != nil {
		t.Fatal(err)
	}

	if len(calls) != 1 || calls[0] != (call{"c", "hi"}) {
		t.Fatalf("calls = %+v", calls)
	}
}

func TestClipboardQueryRepliesEmpty(t *testing.T) {
	v, reply := newTestVT(t, 10, 3)
	v.SetClipboardHandler(func(string, string) {})

	if _, err := v.Write([]byte("\x1b]52;c;?\x07")); err != nil {
		t.Fatal(err)
	}

	if got := replyAfterMarker(t, v, reply); got != "\x1b]52;c;\x07" {
		t.Fatalf("reply = %q", got)
	}
}

func TestSetClipboardHandlerKeepsOtherCallbacks(t *testing.T) {
	v, reply := newTestVT(t, 10, 3)
	v.SetDefaultColors(color.RGBA{R: 0x11, G: 0x22, B: 0x33, A: 0xff}, color.RGBA{R: 0xaa, G: 0xbb, B: 0xcc, A: 0xff})
	v.SetClipboardHandler(func(string, string) {})

	in := "\x1b]2;ttl\x07\x1b]7;file:///tmp/x\x07\x1b[?2026h\x1b]10;?\x07\x1b]11;?\x07"
	if _, err := v.Write([]byte(in)); err != nil {
		t.Fatal(err)
	}

	if v.Title() != "ttl" || v.WorkingDir() != "/tmp/x" {
		t.Fatalf("title %q cwd %q", v.Title(), v.WorkingDir())
	}

	if open, serial := v.SyncState(); !open || serial != 1 {
		t.Fatalf("sync = %v, %d", open, serial)
	}

	got := replyAfterMarker(t, v, reply)
	if !strings.Contains(got, "]10;rgb:1111/2222/3333") || !strings.Contains(got, "]11;rgb:aaaa/bbbb/cccc") {
		t.Fatalf("colour replies = %q", got)
	}
}

func TestScrollbackTextMatchesScrollbackLine(t *testing.T) {
	v, _ := newTestVT(t, 10, 3)

	for _, s := range []string{"\x1b[31mred\x1b[0m\r\n", "a中b\r\n", "\r\n", "plain\r\n", "x\r\n", "y\r\n", "z\r\n"} {
		if _, err := v.Write([]byte(s)); err != nil {
			t.Fatal(err)
		}
	}

	n := v.ScrollbackLen()
	got := map[int]string{}

	v.ScrollbackText(0, n, func(i int, cells []TextCell) bool {
		var b strings.Builder

		for _, c := range cells {
			b.Write(c.Content)
		}

		got[i] = b.String()

		return true
	})

	for i := range n {
		want := lineText(v.ScrollbackLine(i))

		if text, ok := got[i]; ok && strings.TrimRight(text, " ") != want {
			t.Errorf("line %d = %q, want %q", i, text, want)
		} else if !ok && want != "" {
			t.Errorf("line %d (%q) was skipped", i, want)
		}
	}

	if len(got) == 0 {
		t.Fatal("no lines read")
	}

	calls := 0

	v.ScrollbackText(0, n, func(int, []TextCell) bool {
		calls++

		return false
	})

	if calls > 1 {
		t.Errorf("fn called %d times after returning false", calls)
	}
}
