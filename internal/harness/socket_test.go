package harness

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mhersson/aim/internal/core"
)

// shortDir returns a fresh directory under /tmp: macOS's temp dir is too deep
// for a 104-byte socket path.
func shortDir(t *testing.T) string {
	t.Helper()

	dir, err := os.MkdirTemp("/tmp", "mr")
	if err != nil {
		t.Fatal(err)
	}

	t.Cleanup(func() { _ = os.RemoveAll(dir) })

	return dir
}

// socketPath returns a short socket path.
func socketPath(t *testing.T) string {
	t.Helper()

	return filepath.Join(shortDir(t), "s.sock")
}

type served struct {
	srv  *Server
	clk  *fakeClock
	out  chan Event
	path string
	done chan error
}

// fakeClock is a server clock that stands still: each wait the server asks for
// arrives on asks and ends only when the test ends it, so an answer that comes
// with no wait ended was not held. Every event asks for one wait, and a test
// that leaves more than asks holds unread blocks the server.
type fakeClock struct {
	at   time.Time
	asks chan ask
}

type ask struct {
	d    time.Duration
	fire chan time.Time
}

// runOnFakeClock puts srv on a fake clock; call it before Serve.
func runOnFakeClock(srv *Server) *fakeClock {
	c := &fakeClock{at: time.Date(2026, 10, 27, 9, 0, 0, 0, time.UTC), asks: make(chan ask, 64)}
	srv.now = func() time.Time { return c.at }
	srv.after = func(d time.Duration) <-chan time.Time {
		a := ask{d: d, fire: make(chan time.Time, 1)}
		c.asks <- a

		return a.fire
	}

	return c
}

// next returns the next wait the server asks for.
func (c *fakeClock) next(t *testing.T) ask {
	t.Helper()

	select {
	case a := <-c.asks:
		return a
	case <-time.After(3 * time.Second):
		t.Fatal("the server asked for no wait")
	}

	return ask{}
}

func (a ask) end() { a.fire <- time.Time{} }

func startServer(t *testing.T, buf int, log *slog.Logger) *served {
	t.Helper()

	path := socketPath(t)

	srv, err := Listen(path)
	if err != nil {
		t.Fatal(err)
	}

	srv.Log = log
	s := &served{srv: srv, clk: runOnFakeClock(srv), out: make(chan Event, buf), path: path, done: make(chan error, 1)}
	ctx, cancel := context.WithCancel(context.Background())

	go func() { s.done <- srv.Serve(ctx, s.out) }()

	t.Cleanup(func() {
		cancel()

		_ = srv.Close()

		if err := <-s.done; err != nil {
			t.Errorf("Serve() error = %v", err)
		}
	})

	return s
}

func recv(t *testing.T, ch <-chan Event) Event {
	t.Helper()

	select {
	case ev := <-ch:
		return ev
	case <-time.After(3 * time.Second):
		t.Fatal("no event received")

		return Event{}
	}
}

func TestSendRoundTrip(t *testing.T) {
	s := startServer(t, 1, nil)
	want := Event{
		Pane: "p1", Name: "SessionStart", Session: "abc", Transcript: "/t.jsonl", State: core.AgentIdle, Start: true, NewConversation: true,
		Cwd: "/r", Edit: "/r/a.go", Subagent: true, Plan: "/home/u/.claude/plans/x.md",
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	if err := Send(ctx, s.path, want); err != nil {
		t.Fatalf("Send() error = %v", err)
	}

	if got := recv(t, s.out); !reflect.DeepEqual(got, want) {
		t.Fatalf("event = %+v, want %+v", got, want)
	}
}

func TestServerDeliversHostileChanged(t *testing.T) {
	t.Parallel()

	// Sixteen paths of control characters: JSON writes each as 6 bytes, which
	// would take the line past maxLine, and the whole event would be dropped.
	var files []string

	for i := range maxChanged {
		dir := fmt.Sprintf("/h%02d/", i)
		files = append(files, dir+strings.Repeat("\x01", maxChangedPath-len(dir)))
	}

	list, err := json.Marshal(files)
	if err != nil {
		t.Fatal(err)
	}

	ev, err := Claude{}.ParseEvent(payload("PostToolUse",
		`"session_id":"s","cwd":"/r","tool_name":"Bash","tool_input":{"command":"x"},`+
			`"tool_response":{"bashEditDiff":{"changedFiles":`+string(list)+`}}`))
	if err != nil {
		t.Fatal(err)
	}

	if len(ev.Changed) == 0 {
		t.Fatal("ParseEvent kept no path")
	}

	ev.Pane = "p1"

	s := startServer(t, 1, nil)

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	if err := Send(ctx, s.path, ev); err != nil {
		t.Fatalf("Send() error = %v", err)
	}

	got := recv(t, s.out)
	if !reflect.DeepEqual(got, ev) || got.State != core.AgentWorking || got.Action == "" || got.Cwd != "/r" {
		t.Fatalf("event = %+v, want %+v with its state, action and cwd", got, ev)
	}
}

func TestSocketMode(t *testing.T) {
	s := startServer(t, 1, nil)

	fi, err := os.Stat(s.path)
	if err != nil {
		t.Fatal(err)
	}

	if perm := fi.Mode().Perm(); perm != 0o600 {
		t.Fatalf("socket mode = %o, want 600", perm)
	}
}

func TestSendParallel(t *testing.T) {
	s := startServer(t, 8, nil)

	var wg sync.WaitGroup

	errs := make(chan error, 2)

	for _, name := range []string{"a", "b"} {
		wg.Go(func() {
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()

			errs <- Send(ctx, s.path, Event{Pane: "p", Name: name})
		})
	}

	wg.Wait()
	close(errs)

	for err := range errs {
		if err != nil {
			t.Fatalf("Send() error = %v", err)
		}
	}

	got := map[string]bool{recv(t, s.out).Name: true, recv(t, s.out).Name: true}
	if !got["a"] || !got["b"] {
		t.Fatalf("events = %v, want a and b", got)
	}
}

func TestListenReplacesStaleSocket(t *testing.T) {
	path := socketPath(t)

	stale, err := net.Listen("unix", path)
	if err != nil {
		t.Fatal(err)
	}

	// Leave the file behind as a crashed process would.
	stale.(*net.UnixListener).SetUnlinkOnClose(false)
	_ = stale.Close()

	if _, err := os.Stat(path); err != nil {
		t.Fatalf("stale socket file missing: %v", err)
	}

	srv, err := Listen(path)
	if err != nil {
		t.Fatalf("Listen() error = %v", err)
	}

	if err := srv.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestOversizedLineDropped(t *testing.T) {
	s := startServer(t, 1, slog.New(slog.NewTextHandler(io.Discard, nil)))

	conn, err := net.Dial("unix", s.path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = conn.Close() }()

	big := append(bytes.Repeat([]byte("x"), maxLine+10), '\n')
	_, _ = conn.Write(big)

	// A dropped event gets no answer: the server closes the connection.
	_ = conn.SetReadDeadline(time.Now().Add(2 * time.Second))

	if reply, _ := io.ReadAll(conn); len(reply) != 0 {
		t.Fatalf("reply = %q, want none", reply)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	if err := Send(ctx, s.path, Event{Pane: "p", Name: "after"}); err != nil {
		t.Fatalf("Send() after oversized line error = %v", err)
	}

	if got := recv(t, s.out); got.Name != "after" {
		t.Fatalf("event = %+v, want the one sent after", got)
	}
}

func TestBadJSONDropped(t *testing.T) {
	s := startServer(t, 1, nil)

	conn, err := net.Dial("unix", s.path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = conn.Close() }()

	_, _ = conn.Write([]byte("not json\n"))
	_ = conn.SetReadDeadline(time.Now().Add(2 * time.Second))

	if reply, _ := io.ReadAll(conn); len(reply) != 0 {
		t.Fatalf("reply = %q, want none", reply)
	}
}

func TestFullChannelDropsAfterOneSecond(t *testing.T) {
	s := startServer(t, 0, nil)
	res := sendAsync(s.path, Event{Pane: "p", Name: "x"})

	a := s.clk.next(t)
	if a.d != time.Second {
		t.Fatalf("a full channel holds the event for %v, want 1s", a.d)
	}

	a.end()

	if err := <-res; err == nil {
		t.Fatal("Send() error = nil, want one for a dropped event")
	}
}

func TestCloseEndsServeAndRemovesFile(t *testing.T) {
	path := socketPath(t)

	srv, err := Listen(path)
	if err != nil {
		t.Fatal(err)
	}

	done := make(chan error, 1)

	go func() { done <- srv.Serve(context.Background(), make(chan Event)) }()

	if err := srv.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Serve() error = %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("Serve did not return after Close")
	}

	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("socket file after Close: err = %v, want not exist", err)
	}
}

func TestServeEndsWithContext(t *testing.T) {
	srv, err := Listen(socketPath(t))
	if err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)

	go func() { done <- srv.Serve(ctx, make(chan Event)) }()

	cancel()

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Serve() error = %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("Serve did not return after cancel")
	}

	_ = srv.Close()
}

// Without a deadline, a Send that waited on a missing socket would hang.
func TestSendMissingPathFails(t *testing.T) {
	err := Send(context.Background(), filepath.Join(shortDir(t), "none.sock"), Event{})
	if err == nil {
		t.Fatal("Send() error = nil, want one")
	}
}

func TestSendSilentServerHonoursCancel(t *testing.T) {
	path := socketPath(t)

	ln, err := net.Listen("unix", path)
	if err != nil {
		t.Fatal(err)
	}

	var accepted []net.Conn

	var mu sync.Mutex

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// The cancel comes once the server holds the connection, so Send is
	// waiting for an answer that never comes.
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}

			mu.Lock()

			accepted = append(accepted, c)
			mu.Unlock()
			cancel()
		}
	}()

	t.Cleanup(func() {
		_ = ln.Close()

		mu.Lock()
		defer mu.Unlock()

		for _, c := range accepted {
			_ = c.Close()
		}
	})

	if err := Send(ctx, path, Event{Pane: "p"}); err == nil {
		t.Fatal("Send() error = nil, want one")
	}
}

func TestListenKeepsNonSocketFile(t *testing.T) {
	path := socketPath(t)

	if err := os.WriteFile(path, []byte("precious"), 0o600); err != nil {
		t.Fatal(err)
	}

	if _, err := Listen(path); err == nil {
		t.Fatal("Listen() error = nil, want one for a regular file")
	}

	got, err := os.ReadFile(path)
	if err != nil || string(got) != "precious" {
		t.Fatalf("file = %q, %v; want it left untouched", got, err)
	}
}

func startHeldServer(t *testing.T, hold time.Duration) *served {
	t.Helper()

	return startBudgetServer(t, hold, 0)
}

func startBudgetServer(t *testing.T, hold, budget time.Duration) *served {
	t.Helper()

	path := socketPath(t)

	srv, err := Listen(path)
	if err != nil {
		t.Fatal(err)
	}

	srv.Hold = hold
	srv.Budget = budget
	s := &served{srv: srv, clk: runOnFakeClock(srv), out: make(chan Event, 4), path: path, done: make(chan error, 1)}
	ctx, cancel := context.WithCancel(context.Background())

	go func() { s.done <- srv.Serve(ctx, s.out) }()

	t.Cleanup(func() {
		cancel()

		_ = srv.Close()

		if err := <-s.done; err != nil {
			t.Errorf("Serve() error = %v", err)
		}
	})

	return s
}

// sendAsync sends ev and reports the Send error.
func sendAsync(path string, ev Event) <-chan error {
	res := make(chan error, 1)

	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()

		res <- Send(ctx, path, ev)
	}()

	return res
}

// awaitHold returns the hold the server asks for once ev is on the channel.
func awaitHold(t *testing.T, s *served) ask {
	t.Helper()

	s.clk.next(t) // the full channel's wait, which a buffered channel never needs

	return s.clk.next(t)
}

func TestTurnStartAnsweredAfterDone(t *testing.T) {
	s := startHeldServer(t, 300*time.Millisecond)
	res := sendAsync(s.path, Event{Pane: "p", Name: "UserPromptSubmit", TurnStart: true})

	ev := recv(t, s.out)
	if ev.Done == nil {
		t.Fatal("held event has no Done")
	}

	if a := awaitHold(t, s); a.d != 300*time.Millisecond {
		t.Fatalf("holds for %v, want the 300ms Hold", a.d)
	}

	select {
	case err := <-res:
		t.Fatalf("answered before Done: %v", err)
	default:
	}

	ev.Done()
	ev.Done() // more than once is fine

	if err := <-res; err != nil {
		t.Errorf("Send() = %v, want ok", err)
	}
}

func TestTurnStartAnsweredAfterHold(t *testing.T) {
	s := startHeldServer(t, 300*time.Millisecond)
	res := sendAsync(s.path, Event{Pane: "p", Name: "UserPromptSubmit", TurnStart: true})
	_ = recv(t, s.out)

	a := awaitHold(t, s)
	if a.d != 300*time.Millisecond {
		t.Fatalf("holds for %v, want the 300ms Hold", a.d)
	}

	a.end()

	if err := <-res; err != nil {
		t.Errorf("Send() = %v, want ok once the hold ends", err)
	}
}

func TestNonTurnEventAnsweredAtOnce(t *testing.T) {
	s := startHeldServer(t, 300*time.Millisecond)
	res := sendAsync(s.path, Event{Pane: "p", Name: "Stop"})

	if ev := recv(t, s.out); ev.Done != nil {
		t.Error("non-turn event has Done")
	}

	if err := <-res; err != nil {
		t.Errorf("Send() = %v, want ok with no hold", err)
	}
}

func TestZeroHoldAnswersAtOnce(t *testing.T) {
	s := startHeldServer(t, 0)
	res := sendAsync(s.path, Event{Pane: "p", Name: "UserPromptSubmit", TurnStart: true})

	if ev := recv(t, s.out); ev.Done != nil {
		t.Error("event has Done without a hold")
	}

	if err := <-res; err != nil {
		t.Errorf("Send() = %v, want ok with no hold", err)
	}
}

func TestCloseDuringHoldDoesNotWait(t *testing.T) {
	s := startHeldServer(t, 5*time.Second)
	res := sendAsync(s.path, Event{Pane: "p", Name: "UserPromptSubmit", TurnStart: true})
	_ = recv(t, s.out)
	_ = awaitHold(t, s)

	if err := s.srv.Close(); err != nil {
		t.Fatal(err)
	}

	if err := <-res; err == nil {
		t.Error("Send() succeeded though the server closed mid-hold")
	}
}

func TestServerStampsTheTurnDeadline(t *testing.T) {
	const budget = 1400 * time.Millisecond

	s := startBudgetServer(t, 300*time.Millisecond, budget)

	res := sendAsync(s.path, Event{Pane: "p", Name: "UserPromptSubmit", TurnStart: true})
	turn := recv(t, s.out)

	turn.Done()

	if err := <-res; err != nil {
		t.Errorf("Send() = %v, want ok", err)
	}

	if want := s.clk.at.Add(budget); !turn.Deadline.Equal(want) {
		t.Errorf("turn Deadline = %v, want the read time + %v, %v", turn.Deadline, budget, want)
	}

	res = sendAsync(s.path, Event{Pane: "p", Name: "Stop"})
	if stop := recv(t, s.out); !stop.Deadline.IsZero() {
		t.Errorf("non-turn Deadline = %v, want zero", stop.Deadline)
	}

	if err := <-res; err != nil {
		t.Errorf("Send() = %v, want ok", err)
	}
}

func TestServerHoldsAnEditBeforeItsTool(t *testing.T) {
	t.Parallel()

	const budget = 1400 * time.Millisecond

	s := startBudgetServer(t, 30*time.Second, budget)

	res := sendAsync(s.path, Event{Pane: "p", Name: "PreToolUse", Edit: "/r/a.go", Before: true})
	ev := recv(t, s.out)

	if ev.Done == nil || ev.Deadline.IsZero() {
		t.Fatalf("held edit has Done %v and Deadline %v", ev.Done != nil, ev.Deadline)
	}

	// The fake clock never ends the hold, so an answer now would not be the hold's.
	select {
	case err := <-res:
		t.Fatalf("answered before Done: %v", err)
	default:
	}

	ev.Done()

	select {
	case err := <-res:
		if err != nil {
			t.Errorf("Send() = %v, want ok", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("no answer after Done")
	}

	res = sendAsync(s.path, Event{Pane: "p", Name: "PostToolUse", Edit: "/r/a.go"})
	if post := recv(t, s.out); post.Done != nil {
		t.Error("PostToolUse edit has Done")
	}

	select {
	case err := <-res:
		if err != nil {
			t.Errorf("Send() = %v, want ok", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("PostToolUse edit was held")
	}
}
