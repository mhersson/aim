package main

import (
	"bytes"
	"context"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/mhersson/aim/internal/core"
	"github.com/mhersson/aim/internal/harness"
)

const sessionStart = `{"hook_event_name":"SessionStart","session_id":"s1","transcript_path":"/t.jsonl","source":"startup"}`

func env(m map[string]string) func(string) string {
	return func(k string) string { return m[k] }
}

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

func shortSock(t *testing.T) string {
	t.Helper()

	return filepath.Join(shortDir(t), "s.sock")
}

// silentSock returns a socket whose listener accepts and never answers.
func silentSock(t *testing.T) string {
	t.Helper()

	path := shortSock(t)

	ln, err := net.Listen("unix", path)
	if err != nil {
		t.Fatal(err)
	}

	var (
		mu       sync.Mutex
		conns    []net.Conn
		accepted = make(chan struct{})
	)

	go func() {
		defer close(accepted)

		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}

			mu.Lock()

			conns = append(conns, c)
			mu.Unlock()
		}
	}()

	t.Cleanup(func() {
		_ = ln.Close()

		<-accepted

		mu.Lock()
		defer mu.Unlock()

		for _, c := range conns {
			_ = c.Close()
		}
	})

	return path
}

// captureStdio fails the test if f writes to the process stdout or stderr.
func captureStdio(t *testing.T, f func()) {
	t.Helper()

	stdout, stderr := os.Stdout, os.Stderr

	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}

	os.Stdout, os.Stderr = w, w

	func() {
		defer func() { os.Stdout, os.Stderr = stdout, stderr }()

		f()
	}()

	_ = w.Close()

	got, _ := io.ReadAll(r)
	_ = r.Close()

	if len(got) != 0 {
		t.Fatalf("runHook wrote %q to stdout/stderr", got)
	}
}

func TestRunHookDelivers(t *testing.T) {
	path := shortSock(t)

	srv, err := harness.Listen(path)
	if err != nil {
		t.Fatal(err)
	}

	out := make(chan harness.Event, 1)
	done := make(chan error, 1)

	go func() { done <- srv.Serve(context.Background(), out) }()

	t.Cleanup(func() {
		_ = srv.Close()

		if err := <-done; err != nil {
			t.Errorf("Serve() error = %v", err)
		}
	})

	runHook(context.Background(), "claude-code", false, strings.NewReader(sessionStart), io.Discard,
		env(map[string]string{"AIM_SOCK": path, "AIM_PANE": "p7"}))

	select {
	case ev := <-out:
		if ev.Pane != core.PaneID("p7") || ev.Session != "s1" || !ev.Start {
			t.Fatalf("event = %+v, want pane p7 and a started session s1", ev)
		}
	default:
		t.Fatal("no event delivered")
	}
}

func TestRunHookIsSilentAndHonoursItsBudget(t *testing.T) {
	missing := filepath.Join(shortDir(t), "none.sock")

	silent := silentSock(t)

	tests := []struct {
		name  string
		id    string
		stdin string
		env   map[string]string
		// waitsOutBudget marks a payload that is delivered and never
		// answered: only the budget ends it.
		waitsOutBudget bool
	}{
		{"no socket", "claude-code", sessionStart, map[string]string{"AIM_PANE": "p"}, false},
		{"no pane", "claude-code", sessionStart, map[string]string{"AIM_SOCK": missing}, false},
		{"unknown harness", "nope", sessionStart, map[string]string{"AIM_SOCK": missing, "AIM_PANE": "p"}, false},
		{"invalid json", "claude-code", "{not json", map[string]string{"AIM_SOCK": missing, "AIM_PANE": "p"}, false},
		{"missing socket", "claude-code", sessionStart, map[string]string{"AIM_SOCK": missing, "AIM_PANE": "p"}, false},
		{"never answers", "claude-code", sessionStart, map[string]string{"AIM_SOCK": silent, "AIM_PANE": "p"}, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Only the budget case gets a context that can expire; the rest
			// must return on their own with theirs still live.
			var (
				ctx    context.Context
				cancel context.CancelFunc
			)

			if tt.waitsOutBudget {
				ctx, cancel = context.WithTimeout(context.Background(), injectedBudget)
			} else {
				ctx, cancel = context.WithCancel(context.Background())
			}

			defer cancel()

			captureStdio(t, func() {
				returns(t, "runHook", func() {
					runHook(ctx, tt.id, false, strings.NewReader(tt.stdin), io.Discard, env(tt.env))
				})
			})

			if tt.waitsOutBudget {
				// Send ends on the connection deadline, which can precede the
				// context's own timer, so wait for the context itself.
				awaitDone(ctx, t)
			} else if ctx.Err() != nil {
				t.Fatal("runHook needed the budget to return, want it to return on its own")
			}
		})
	}
}

func TestHookCommandHiddenAndQuiet(t *testing.T) {
	cmd := newRootCmd()

	var help bytes.Buffer

	cmd.SetOut(&help)
	cmd.SetErr(&help)
	cmd.SetArgs([]string{"--help"})

	if err := cmd.ExecuteContext(context.Background()); err != nil {
		t.Fatal(err)
	}

	if strings.Contains(help.String(), "hook") {
		t.Fatalf("help lists the hidden hook command:\n%s", help.String())
	}

	hook, _, err := newRootCmd().Find([]string{"hook", "claude-code"})
	if err != nil || hook.Name() != "hook" || !hook.Hidden {
		t.Fatalf("Find(hook) = %v, %v; want the hidden hook command", hook, err)
	}
}

func TestHookCommandReturnsNilWithoutEnvironment(t *testing.T) {
	t.Setenv("AIM_SOCK", "")
	t.Setenv("AIM_PANE", "")

	cmd := newRootCmd()

	var buf bytes.Buffer

	cmd.SetOut(&buf)
	cmd.SetErr(&buf)
	cmd.SetIn(strings.NewReader(sessionStart))
	cmd.SetArgs([]string{"hook", "claude-code"})

	if err := cmd.ExecuteContext(context.Background()); err != nil {
		t.Fatalf("Execute() error = %v, want nil", err)
	}

	if buf.Len() != 0 {
		t.Fatalf("output = %q, want none", buf.String())
	}
}

func TestRunHookPrintsTheNote(t *testing.T) {
	t.Parallel()

	stop := `{"hook_event_name":"Stop","session_id":"s1"}`

	tests := []struct {
		name      string
		note      bool
		stdin     string
		bin       string
		wantPrint bool
	}{
		{"session start with the note", true, sessionStart, "/run/aim", true},
		{"no AIM_BIN", true, sessionStart, "", false},
		{"no --note", false, sessionStart, "/run/aim", false},
		{"other event", true, stop, "/run/aim", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			path := shortSock(t)

			srv, err := harness.Listen(path)
			if err != nil {
				t.Fatal(err)
			}

			out := make(chan harness.Event, 1)
			done := make(chan error, 1)

			go func() { done <- srv.Serve(context.Background(), out) }()

			t.Cleanup(func() {
				_ = srv.Close()

				if err := <-done; err != nil {
					t.Errorf("Serve() error = %v", err)
				}
			})

			var stdout strings.Builder

			runHook(context.Background(), "claude-code", tt.note, strings.NewReader(tt.stdin), &stdout,
				env(map[string]string{"AIM_SOCK": path, "AIM_PANE": "p7", "AIM_BIN": tt.bin}))

			want := ""
			if tt.wantPrint {
				want = showNote + "\n"
			}

			if stdout.String() != want {
				t.Errorf("stdout = %q, want %q", stdout.String(), want)
			}

			select {
			case <-out:
			default:
				t.Error("no event delivered")
			}
		})
	}
}
