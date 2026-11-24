package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"sync/atomic"
	"testing"
	"time"

	"github.com/mhersson/aim/internal/harness"
)

const (
	snapshotKey = "com.openai/codex-statusline-state"
	replyWait   = 10 * time.Second
)

func readFixture(t *testing.T, name string) string {
	t.Helper()

	b, err := os.ReadFile(filepath.Join("..", "..", "internal", "harness", "testdata", "codex", name))
	if err != nil {
		t.Fatal(err)
	}

	var buf bytes.Buffer
	if err := json.Compact(&buf, b); err != nil {
		t.Fatal(err)
	}

	return buf.String()
}

func readRequest(id int, snapshot string) string {
	return fmt.Sprintf(`{"jsonrpc":"2.0","id":%d,"method":"resources/read","params":{"uri":"ui://aim/statusline","_meta":{%q:%s}}}`,
		id, snapshotKey, snapshot)
}

// provider runs serveCodexStatus on a pipe and returns the writer for
// requests, a reader of reply lines and a func that closes stdin and waits.
func provider(t *testing.T, send func(harness.Event) bool) (io.Writer, func() map[string]any, func()) {
	t.Helper()

	inR, inW := io.Pipe()
	outR, outW := io.Pipe()
	done := make(chan error, 1)

	go func() {
		err := serveCodexStatus(context.Background(), inR, outW, send)
		_ = outW.Close()

		done <- err
	}()

	sc := bufio.NewScanner(outR)
	replies := make(chan map[string]any)

	go func() {
		defer close(replies)

		for sc.Scan() {
			var m map[string]any
			if err := json.Unmarshal(sc.Bytes(), &m); err != nil {
				t.Errorf("reply %q is not JSON: %v", sc.Text(), err)

				return
			}

			replies <- m
		}
	}()

	next := func() map[string]any {
		t.Helper()

		select {
		case m, ok := <-replies:
			if !ok {
				t.Fatal("replies ended early")
			}

			return m
		case <-time.After(replyWait):
			t.Fatal("no reply")
		}

		return nil
	}

	finish := func() {
		_ = inW.Close()

		select {
		case err := <-done:
			if err != nil {
				t.Errorf("serveCodexStatus() error = %v", err)
			}
		case <-time.After(replyWait):
			t.Error("serveCodexStatus did not return after stdin closed")
		}
	}

	return inW, next, finish
}

func writeLine(t *testing.T, w io.Writer, s string) {
	t.Helper()

	if _, err := io.WriteString(w, s+"\n"); err != nil {
		t.Fatal(err)
	}
}

func TestProviderProtocol(t *testing.T) {
	t.Parallel()

	in, next, finish := provider(t, func(harness.Event) bool { return true })

	writeLine(t, in, `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{}}`)

	caps := next()["result"].(map[string]any)["capabilities"].(map[string]any)
	for _, k := range []string{"tools", "resources"} {
		if _, ok := caps[k]; !ok {
			t.Errorf("capabilities = %v, want %q", caps, k)
		}
	}

	// A notification gets no reply, so the next reply is for id 2.
	writeLine(t, in, `{"jsonrpc":"2.0","method":"notifications/initialized"}`)
	writeLine(t, in, `{"jsonrpc":"2.0","id":2,"method":"tools/list"}`)

	r := next()
	if r["id"] != float64(2) {
		t.Fatalf("reply = %v, want id 2 (the notification must not be answered)", r)
	}

	tools := r["result"].(map[string]any)["tools"].([]any)
	if len(tools) != 1 {
		t.Fatalf("tools = %v, want one", tools)
	}

	tool := tools[0].(map[string]any)
	entry := tool["_meta"].(map[string]any)["openai/ui"].(map[string]any)["entrypoints"].([]any)[0]

	if tool["name"] != "statusline" || !reflect.DeepEqual(entry, map[string]any{"type": "statusline", "resourceUri": "ui://aim/statusline"}) {
		t.Errorf("tool = %v, want the statusline entrypoint", tool)
	}

	writeLine(t, in, readRequest(3, readFixture(t, "status-ready.json")))

	c := next()["result"].(map[string]any)["contents"].([]any)[0]
	if want := map[string]any{"uri": "ui://aim/statusline", "mimeType": "application/json", "text": "null"}; !reflect.DeepEqual(c, want) {
		t.Errorf("contents[0] = %v, want %v", c, want)
	}

	writeLine(t, in, `{"jsonrpc":"2.0","id":4,"method":"foo"}`)

	if code := next()["error"].(map[string]any)["code"]; code != float64(-32601) {
		t.Errorf("error code = %v, want -32601", code)
	}

	writeLine(t, in, `not json`)

	r = next()
	if code := r["error"].(map[string]any)["code"]; code != float64(-32700) || r["id"] != nil {
		t.Errorf("reply = %v, want -32700 with a null id", r)
	}

	writeLine(t, in, `{"jsonrpc":"2.0","id":5,"method":"ping"}`)

	if r := next(); r["id"] != float64(5) {
		t.Errorf("reply = %v, want the loop to carry on after a bad line", r)
	}

	finish()
}

func TestProviderSendsOnlyChanges(t *testing.T) {
	t.Parallel()

	ready := readFixture(t, "status-ready.json")
	working := readFixture(t, "status-working-named.json")

	var allow atomic.Bool

	allow.Store(true)

	events := make(chan harness.Event, 8)
	in, next, finish := provider(t, func(ev harness.Event) bool {
		// The answer is read before the event is published, so the test
		// changing it after receiving the event cannot affect this call.
		ok := allow.Load()

		events <- ev

		return ok
	})

	read := func(id int, snap string) {
		writeLine(t, in, readRequest(id, snap))
		next()
	}

	read(1, ready)

	readyEv := readEvent(t, events)

	// The duplicate is not sent: the next event is working, not ready.
	read(2, ready)
	read(3, working)

	workingEv := readEvent(t, events)
	if workingEv.RunState == readyEv.RunState && workingEv.ThreadName == readyEv.ThreadName {
		t.Fatalf("events = %+v then %+v, want ready then working, no duplicate", readyEv, workingEv)
	}

	// A failed send leaves the snapshot undelivered, so the same one is retried.
	allow.Store(false)
	read(4, ready)
	readEvent(t, events)

	allow.Store(true)
	read(5, ready)
	readEvent(t, events)

	read(6, ready)
	finish()

	select {
	case ev := <-events:
		t.Errorf("unexpected extra event %+v", ev)
	default:
	}
}

func readEvent(t *testing.T, events <-chan harness.Event) harness.Event {
	t.Helper()

	select {
	case ev := <-events:
		return ev
	case <-time.After(replyWait):
		t.Fatal("no event sent")
	}

	return harness.Event{}
}

func TestProviderRepliesWhenSendFails(t *testing.T) {
	t.Parallel()

	release := make(chan struct{})
	started := make(chan struct{}, 1)

	in, next, finish := provider(t, func(harness.Event) bool {
		select {
		case started <- struct{}{}:
		default:
		}

		<-release

		return false
	})

	snaps := []string{readFixture(t, "status-ready.json"), readFixture(t, "status-working-named.json"), `{"agent":` + "1}"}
	for i, snap := range snaps {
		writeLine(t, in, readRequest(i+1, snap))

		r := next()
		if r["id"] != float64(i+1) {
			t.Fatalf("reply = %v, want id %d", r, i+1)
		}

		c := r["result"].(map[string]any)["contents"].([]any)[0].(map[string]any)
		if c["text"] != "null" {
			t.Errorf("text = %v, want null", c["text"])
		}
	}

	select {
	case <-started:
	case <-time.After(replyWait):
		t.Fatal("send never started")
	}

	close(release)
	finish()
}

func TestCodexSendRefusals(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		env  map[string]string
	}{
		{"no socket", map[string]string{"AIM_PANE": "p1", "AIM_CODEX_OWNER": "1"}},
		{"no pane", map[string]string{"AIM_SOCK": "/nonexistent/sock", "AIM_CODEX_OWNER": "1"}},
		{"owner not a number", map[string]string{"AIM_SOCK": "/nonexistent/sock", "AIM_PANE": "p1", "AIM_CODEX_OWNER": "x"}},
		{"not the foreground owner", map[string]string{"AIM_SOCK": "/nonexistent/sock", "AIM_PANE": "p1", "AIM_CODEX_OWNER": "1", "AIM_TTY": "/dev/null"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if codexSender(context.Background(), env(tt.env))(harness.Event{}) {
				t.Error("send reported delivery, want a refusal")
			}
		})
	}
}
