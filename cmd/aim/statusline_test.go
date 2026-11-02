package main

import (
	"bytes"
	"context"
	"io"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/mhersson/aim/internal/core"
	"github.com/mhersson/aim/internal/harness"
)

const statusPayload = `{"hook_event_name":"Status","session_id":"s1","model":{"id":"claude-opus-4-5","display_name":"Opus 4.5"},` +
	`"context_window":{"total_input_tokens":84000,"context_window_size":200000,"used_percentage":42.7}}`

// statusServer serves a hook socket and returns its path and the events it
// receives.
func statusServer(t *testing.T) (string, <-chan harness.Event) {
	t.Helper()

	path := shortSock(t)

	srv, err := harness.Listen(path)
	if err != nil {
		t.Fatal(err)
	}

	out := make(chan harness.Event, 4)
	done := make(chan error, 1)

	go func() { done <- srv.Serve(context.Background(), out) }()

	t.Cleanup(func() {
		_ = srv.Close()

		if err := <-done; err != nil {
			t.Errorf("Serve() error = %v", err)
		}
	})

	return path, out
}

func TestRunStatuslineDelivers(t *testing.T) {
	path, out := statusServer(t)

	runStatusline(context.Background(), strings.NewReader(statusPayload),
		env(map[string]string{"AIM_SOCK": path, "AIM_PANE": "p7"}))

	select {
	case ev := <-out:
		if ev.Pane != core.PaneID("p7") || ev.Session != "s1" || !ev.Status || ev.Model != "Opus 4.5" {
			t.Fatalf("event = %+v, want a status event for pane p7, session s1 and model Opus 4.5", ev)
		}

		if !ev.ContextKnown || ev.Context != 42.7 || ev.Window != 200000 || ev.InputTokens != 84000 {
			t.Fatalf("event = %+v, want 42.7%% of a 200000 window with 84000 input tokens", ev)
		}
	default:
		t.Fatal("no event delivered")
	}
}

func TestRunStatuslineDeliversRateLimits(t *testing.T) {
	t.Parallel()

	const limits = `,"rate_limits":{"five_hour":{"used_percentage":23.5,"resets_at":1738425600},` +
		`"seven_day":{"used_percentage":41.2,"resets_at":1738857600}}}`

	path, out := statusServer(t)

	runStatusline(context.Background(), strings.NewReader(strings.TrimSuffix(statusPayload, "}")+limits),
		env(map[string]string{"AIM_SOCK": path, "AIM_PANE": "p7"}))

	select {
	case ev := <-out:
		if want := (&harness.RateWindow{Used: 23.5, ResetsAt: 1738425600}); !reflect.DeepEqual(ev.FiveHour, want) {
			t.Errorf("FiveHour = %+v, want %+v", ev.FiveHour, want)
		}

		if want := (&harness.RateWindow{Used: 41.2, ResetsAt: 1738857600}); !reflect.DeepEqual(ev.SevenDay, want) {
			t.Errorf("SevenDay = %+v, want %+v", ev.SevenDay, want)
		}
	default:
		t.Fatal("no event delivered")
	}
}

// hangGuard bounds a call that must return: it fails the test on a real hang
// instead of waiting for the go test timeout, and asserts no elapsed time.
const hangGuard = 30 * time.Second

// returns runs fn and fails the test if it does not return within hangGuard.
func returns(t *testing.T, what string, fn func()) {
	t.Helper()

	done := make(chan struct{})

	go func() {
		defer close(done)

		fn()
	}()

	select {
	case <-done:
	case <-time.After(hangGuard):
		t.Fatalf("%s did not return", what)
	}
}

// awaitDone waits for ctx to end on its own timer.
func awaitDone(ctx context.Context, t *testing.T) {
	t.Helper()

	select {
	case <-ctx.Done():
	case <-time.After(hangGuard):
		t.Fatal("the injected budget never ended")
	}
}

// injectedBudget is the budget the cases that must wait it out run under:
// short, because the tests assert that runStatusline honours it, not how long
// it took.
const injectedBudget = 50 * time.Millisecond

func TestRunStatuslineIsSilentAndHonoursItsBudget(t *testing.T) {
	missing := filepath.Join(shortDir(t), "none.sock")
	silent := silentSock(t)

	oversized := `{"session_id":"s1","pad":"` + strings.Repeat("x", maxHookPayload) + `"}`

	tests := []struct {
		name  string
		stdin string
		env   map[string]string
		// waitsOutBudget marks a payload that is delivered and never
		// answered: only the budget ends it.
		waitsOutBudget bool
	}{
		{"no socket", statusPayload, map[string]string{"AIM_PANE": "p"}, false},
		{"no pane", statusPayload, map[string]string{"AIM_SOCK": missing}, false},
		{"invalid json", "{not json", map[string]string{"AIM_SOCK": missing, "AIM_PANE": "p"}, false},
		{"truncated", statusPayload[:40], map[string]string{"AIM_SOCK": silent, "AIM_PANE": "p"}, false},
		{"oversized", oversized, map[string]string{"AIM_SOCK": silent, "AIM_PANE": "p"}, false},
		{"never answers", statusPayload, map[string]string{"AIM_SOCK": silent, "AIM_PANE": "p"}, true},
		{"missing socket", statusPayload, map[string]string{"AIM_SOCK": missing, "AIM_PANE": "p"}, false},
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
				returns(t, "runStatusline", func() {
					runStatusline(ctx, strings.NewReader(tt.stdin), env(tt.env))
				})
			})

			if tt.waitsOutBudget {
				// Send ends on the connection deadline, which can precede the
				// context's own timer, so wait for the context itself.
				awaitDone(ctx, t)
			} else if ctx.Err() != nil {
				t.Fatal("runStatusline needed the budget to return, want it to return on its own")
			}
		})
	}
}

func TestRunStatuslineStdinNeverClosesStaysInBudget(t *testing.T) {
	pr, pw := io.Pipe()
	defer func() { _ = pw.Close() }()

	ctx, cancel := context.WithTimeout(context.Background(), injectedBudget)
	defer cancel()

	captureStdio(t, func() {
		returns(t, "runStatusline", func() {
			runStatusline(ctx, pr, env(map[string]string{"AIM_SOCK": "/x", "AIM_PANE": "p"}))
		})
	})

	// Returning is only allowed once the budget ended; wait for its own timer.
	awaitDone(ctx, t)

	_ = pr.Close()
}

func TestStatuslineCommandHiddenAndQuiet(t *testing.T) {
	cmd := newRootCmd()

	var help bytes.Buffer

	cmd.SetOut(&help)
	cmd.SetErr(&help)
	cmd.SetArgs([]string{"--help"})

	if err := cmd.ExecuteContext(context.Background()); err != nil {
		t.Fatal(err)
	}

	if strings.Contains(help.String(), "statusline") {
		t.Fatalf("help lists the hidden statusline command:\n%s", help.String())
	}

	found, _, err := newRootCmd().Find([]string{"statusline"})
	if err != nil || found.Name() != "statusline" || !found.Hidden {
		t.Fatalf("Find(statusline) = %v, %v; want the hidden statusline command", found, err)
	}

	t.Run("without the environment", func(t *testing.T) {
		t.Setenv("AIM_SOCK", "")
		t.Setenv("AIM_PANE", "")

		var buf bytes.Buffer

		cmd := newRootCmd()
		cmd.SetOut(&buf)
		cmd.SetErr(&buf)
		cmd.SetIn(strings.NewReader(statusPayload))
		cmd.SetArgs([]string{"statusline"})

		captureStdio(t, func() {
			if err := cmd.ExecuteContext(context.Background()); err != nil {
				t.Errorf("Execute() error = %v, want nil", err)
			}
		})

		if buf.Len() != 0 {
			t.Fatalf("output = %q, want none", buf.String())
		}
	})

	t.Run("with the environment", func(t *testing.T) {
		path, out := statusServer(t)

		t.Setenv("AIM_SOCK", path)
		t.Setenv("AIM_PANE", "p3")

		var buf bytes.Buffer

		cmd := newRootCmd()
		cmd.SetOut(&buf)
		cmd.SetErr(&buf)
		cmd.SetIn(strings.NewReader(statusPayload))
		cmd.SetArgs([]string{"statusline"})

		captureStdio(t, func() {
			if err := cmd.ExecuteContext(context.Background()); err != nil {
				t.Errorf("Execute() error = %v, want nil", err)
			}
		})

		if buf.Len() != 0 {
			t.Fatalf("output = %q, want none", buf.String())
		}

		select {
		case ev := <-out:
			if ev.Pane != core.PaneID("p3") || !ev.Status || ev.Model != "Opus 4.5" {
				t.Fatalf("event = %+v, want a status event for pane p3", ev)
			}
		default:
			t.Fatal("no event delivered")
		}
	})
}

func TestRunStatuslineDrainsStdin(t *testing.T) {
	tests := []struct {
		name    string
		env     map[string]string
		payload io.Reader
	}{
		{"without the environment", nil, bytes.NewReader(bytes.Repeat([]byte("x"), 1<<20))},
		{
			"with the environment",
			map[string]string{"AIM_SOCK": "/x", "AIM_PANE": "p"},
			io.LimitReader(zeroReader{}, maxHookPayload+1<<20),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			pr, pw := io.Pipe()
			written := make(chan error, 1)

			go func() {
				_, err := io.Copy(pw, tt.payload)
				_ = pw.Close()

				written <- err
			}()

			captureStdio(t, func() {
				ctx, cancel := context.WithTimeout(context.Background(), statuslineBudget)
				defer cancel()

				runStatusline(ctx, pr, env(tt.env))
			})

			select {
			case err := <-written:
				if err != nil {
					t.Fatalf("writer error = %v, want every byte consumed", err)
				}
			case <-time.After(time.Second):
				t.Fatal("writer still blocked after runStatusline returned")
			}
		})
	}
}
