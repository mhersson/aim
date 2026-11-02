package ui

import (
	"bytes"
	"context"
	"errors"
	"io"
	"log/slog"
	"os/exec"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
)

func envFrom(m map[string]string) func(string) string {
	return func(k string) string { return m[k] }
}

func TestNativeCommands(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		env  map[string]string
		goos string
		want [][]string
	}{
		{"darwin", nil, "darwin", [][]string{{"pbcopy"}}},
		{
			"wayland",
			map[string]string{"WAYLAND_DISPLAY": "wayland-0", "DISPLAY": ":0"},
			"linux",
			[][]string{{"wl-copy"}, {"wl-copy", "--primary"}},
		},
		{
			"x11",
			map[string]string{"DISPLAY": ":0"},
			"linux",
			[][]string{{"xclip", "-selection", "clipboard"}, {"xclip", "-selection", "primary"}},
		},
		{"no display", nil, "linux", nil},
		{"ssh connection", map[string]string{"SSH_CONNECTION": "a b c d", "DISPLAY": ":0"}, "linux", nil},
		{"ssh tty", map[string]string{"SSH_TTY": "/dev/pts/1", "WAYLAND_DISPLAY": "w"}, "linux", nil},
		{"ssh darwin", map[string]string{"SSH_CONNECTION": "a"}, "darwin", nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := nativeCommands(envFrom(tt.env), tt.goos)
			if !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("nativeCommands = %v, want %v", got, tt.want)
			}
		})
	}
}

// flatten runs a Cmd and expands a BatchMsg into the messages of its inner
// Cmds. Native write Cmds return nil and are executed for their side effect.
func flatten(cmd tea.Cmd) []tea.Msg {
	if cmd == nil {
		return nil
	}

	msg := cmd()
	if b, ok := msg.(tea.BatchMsg); ok {
		var out []tea.Msg
		for _, c := range b {
			out = append(out, flatten(c)...)
		}

		return out
	}

	return []tea.Msg{msg}
}

type runCall struct {
	argv  []string
	stdin string
}

type fakeRun struct {
	mu    sync.Mutex
	calls []runCall
	err   func(argv []string) error
	out   string
}

func (f *fakeRun) run(_ context.Context, argv []string, stdin string, stdout io.Writer) error {
	f.mu.Lock()
	f.calls = append(f.calls, runCall{argv, stdin})
	f.mu.Unlock()

	if stdout != nil {
		_, _ = io.WriteString(stdout, f.out)
	}

	if f.err != nil {
		return f.err(argv)
	}

	return nil
}

func newTestClipboard(cfg clipboardConfig, env map[string]string, goos string, run func(context.Context, []string, string, io.Writer) error) (*clipboard, *bytes.Buffer) {
	var buf bytes.Buffer

	log := slog.New(slog.NewTextHandler(&buf, nil))
	c := newClipboard(cfg, envFrom(env), goos, log)
	c.run = run

	return c, &buf
}

func TestClipboardCopy(t *testing.T) {
	t.Parallel()

	const text = "hello"

	osc := tea.SetClipboard(text)()
	prim := tea.SetPrimaryClipboard(text)()
	wayland := map[string]string{"WAYLAND_DISPLAY": "w"}

	tests := []struct {
		name      string
		cfg       clipboardConfig
		env       map[string]string
		goos      string
		text      string
		wantMsgs  []tea.Msg
		wantCalls []runCall
	}{
		{"empty text", clipboardConfig{true, true}, wayland, "linux", "", nil, nil},
		{
			"linux wayland",
			clipboardConfig{true, true},
			wayland, "linux", text,
			[]tea.Msg{osc, prim, nil},
			[]runCall{{[]string{"wl-copy"}, text}, {[]string{"wl-copy", "--primary"}, text}},
		},
		{
			"darwin",
			clipboardConfig{true, true},
			nil, "darwin", text,
			[]tea.Msg{osc, nil},
			[]runCall{{[]string{"pbcopy"}, text}},
		},
		{
			"ssh keeps osc52",
			clipboardConfig{true, true},
			map[string]string{"SSH_CONNECTION": "x", "DISPLAY": ":0"},
			"linux", text,
			[]tea.Msg{osc, prim},
			nil,
		},
		{
			"osc52 off",
			clipboardConfig{false, true},
			map[string]string{"DISPLAY": ":0"},
			"linux", text,
			[]tea.Msg{nil},
			[]runCall{{[]string{"xclip", "-selection", "clipboard"}, text}, {[]string{"xclip", "-selection", "primary"}, text}},
		},
		{"native off", clipboardConfig{true, false}, wayland, "linux", text, []tea.Msg{osc, prim}, nil},
		{"nothing to send", clipboardConfig{false, true}, nil, "linux", text, nil, nil},
		{"both off", clipboardConfig{false, false}, wayland, "linux", text, nil, nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fr := &fakeRun{}
			c, _ := newTestClipboard(tt.cfg, tt.env, tt.goos, fr.run)

			cmd := c.copy(tt.text)
			if len(tt.wantMsgs) == 0 {
				if cmd != nil {
					t.Fatalf("copy returned a Cmd, want nil")
				}

				return
			}

			got := flatten(cmd)
			if !reflect.DeepEqual(got, tt.wantMsgs) {
				t.Fatalf("msgs = %#v, want %#v", got, tt.wantMsgs)
			}

			if !reflect.DeepEqual(fr.calls, tt.wantCalls) {
				t.Fatalf("calls = %v, want %v", fr.calls, tt.wantCalls)
			}
		})
	}
}

func TestClipboardCopyPrimary(t *testing.T) {
	t.Parallel()

	const text = "hello"

	prim := tea.SetPrimaryClipboard(text)()
	wayland := map[string]string{"WAYLAND_DISPLAY": "w"}
	both := clipboardConfig{true, true}
	wlPrimary := []runCall{{[]string{"wl-copy", "--primary"}, text}}

	tests := []struct {
		name      string
		cfg       clipboardConfig
		env       map[string]string
		goos      string
		text      string
		wantMsgs  []tea.Msg
		wantCalls []runCall
		wantLast  string
	}{
		{"wayland", both, wayland, "linux", text, []tea.Msg{prim, nil}, wlPrimary, text},
		{
			"x11", both,
			map[string]string{"DISPLAY": ":0"},
			"linux", text,
			[]tea.Msg{prim, nil},
			[]runCall{{[]string{"xclip", "-selection", "primary"}, text}},
			text,
		},
		{"darwin", both, nil, "darwin", text, nil, nil, text},
		{"ssh on linux", both, map[string]string{"SSH_CONNECTION": "x", "WAYLAND_DISPLAY": "w"}, "linux", text, []tea.Msg{prim}, nil, text},
		{"native off", clipboardConfig{true, false}, wayland, "linux", text, []tea.Msg{prim}, nil, text},
		{"osc52 off", clipboardConfig{false, true}, wayland, "linux", text, []tea.Msg{nil}, wlPrimary, text},
		{"empty text", both, wayland, "linux", "", nil, nil, "old"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			fr := &fakeRun{}
			c, _ := newTestClipboard(tt.cfg, tt.env, tt.goos, fr.run)
			c.last = "old"

			cmd := c.copyPrimary(tt.text)
			if got := flatten(cmd); !reflect.DeepEqual(got, tt.wantMsgs) {
				t.Fatalf("msgs = %#v, want %#v", got, tt.wantMsgs)
			}

			if !reflect.DeepEqual(fr.calls, tt.wantCalls) {
				t.Fatalf("calls = %v, want %v", fr.calls, tt.wantCalls)
			}

			if c.last != tt.wantLast {
				t.Fatalf("last = %q, want %q", c.last, tt.wantLast)
			}
		})
	}
}

func TestClipboardNativeTimeout(t *testing.T) {
	t.Parallel()

	run := func(ctx context.Context, _ []string, _ string, _ io.Writer) error {
		<-ctx.Done()

		return ctx.Err()
	}
	c, buf := newTestClipboard(clipboardConfig{Native: true}, map[string]string{"WAYLAND_DISPLAY": "w"}, "linux", run)
	c.timeout = time.Nanosecond // expired at birth

	done := make(chan struct{})

	go func() {
		flatten(c.copy("x"))
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("native write Cmd did not return after the timeout")
	}

	if got := strings.Count(buf.String(), "context deadline exceeded"); got != 2 {
		t.Fatalf("timeouts logged %d times, want 2 (one per argv):\n%s", got, buf.String())
	}
}

func TestClipboardMissingToolLoggedOnce(t *testing.T) {
	t.Parallel()

	fr := &fakeRun{err: func(argv []string) error {
		if argv[0] == "wl-copy" {
			return &exec.Error{Name: "wl-copy", Err: exec.ErrNotFound}
		}

		return nil
	}}
	c, buf := newTestClipboard(clipboardConfig{OSC52: true, Native: true}, map[string]string{"WAYLAND_DISPLAY": "w"}, "linux", fr.run)

	for range 3 {
		msgs := flatten(c.copy("x"))
		if len(msgs) < 2 || msgs[0] != tea.SetClipboard("x")() {
			t.Fatalf("OSC 52 must still go out, got %#v", msgs)
		}
	}

	if got := strings.Count(buf.String(), "level=WARN"); got != 1 {
		t.Fatalf("missing tool logged %d times, want 1:\n%s", got, buf.String())
	}

	if len(fr.calls) != 6 {
		t.Fatalf("run called %d times, want 6 (every argv still attempted)", len(fr.calls))
	}
}

func TestClipboardOtherFailuresLoggedEachTime(t *testing.T) {
	t.Parallel()

	fr := &fakeRun{err: func([]string) error { return errors.New("exit status 1") }}

	c, buf := newTestClipboard(clipboardConfig{Native: true}, nil, "darwin", fr.run)
	for range 2 {
		flatten(c.copy("x"))
	}

	if got := strings.Count(buf.String(), "exit status 1"); got != 2 {
		t.Fatalf("failure logged %d times, want 2:\n%s", got, buf.String())
	}
}

// Only a binary that cannot exist runs, so no real clipboard tool is touched.
func TestRunNativeReportsAMissingTool(t *testing.T) {
	t.Parallel()

	err := runNative(context.Background(), []string{"aim-no-such-clipboard-tool", "-x"}, "text", nil)
	if !errors.Is(err, exec.ErrNotFound) {
		t.Errorf("runNative error = %v, want exec.ErrNotFound", err)
	}
}

func TestRunNativeWritesStdout(t *testing.T) {
	t.Parallel()

	var buf bytes.Buffer
	if err := runNative(context.Background(), []string{"printf", "%s", "out"}, "", &buf); err != nil {
		t.Fatalf("runNative: %v", err)
	}

	if buf.String() != "out" {
		t.Fatalf("stdout = %q, want %q", buf.String(), "out")
	}
}

func TestPasteCommand(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		env  map[string]string
		goos string
		want []string
	}{
		{"darwin", nil, "darwin", []string{"pbpaste"}},
		{
			"wayland",
			map[string]string{"WAYLAND_DISPLAY": "wayland-0", "DISPLAY": ":0"},
			"linux",
			[]string{"wl-paste", "--primary", "--no-newline", "--type", "text"},
		},
		{"x11", map[string]string{"DISPLAY": ":0"}, "linux", []string{"xclip", "-selection", "primary", "-o"}},
		{"no display", nil, "linux", nil},
		{"ssh connection", map[string]string{"SSH_CONNECTION": "a b c d", "DISPLAY": ":0"}, "linux", nil},
		{"ssh tty", map[string]string{"SSH_TTY": "/dev/pts/1", "WAYLAND_DISPLAY": "w"}, "linux", nil},
		{"ssh darwin", map[string]string{"SSH_CONNECTION": "a"}, "darwin", nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got := pasteCommand(envFrom(tt.env), tt.goos)
			if !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("pasteCommand = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestClipboardPaste(t *testing.T) {
	t.Parallel()

	wlPaste := []string{"wl-paste", "--primary", "--no-newline", "--type", "text"}
	xclipPaste := []string{"xclip", "-selection", "primary", "-o"}
	native := clipboardConfig{Native: true}
	wayland := map[string]string{"WAYLAND_DISPLAY": "w"}
	notFound := &exec.Error{Name: "wl-paste", Err: exec.ErrNotFound}

	tests := []struct {
		name      string
		cfg       clipboardConfig
		env       map[string]string
		goos      string
		last, out string
		err       error
		want      string
		wantReads [][]string
		wantWarn  int
	}{
		{"wayland", native, wayland, "linux", "old", "sel", nil, "sel", [][]string{wlPaste}, 0},
		{"x11", native, map[string]string{"DISPLAY": ":0"}, "linux", "old", "sel", nil, "sel", [][]string{xclipPaste}, 0},
		{"darwin", native, nil, "darwin", "old", "sel", nil, "sel", [][]string{{"pbpaste"}}, 0},
		{"ssh", native, map[string]string{"SSH_CONNECTION": "x", "DISPLAY": ":0"}, "linux", "old", "sel", nil, "old", nil, 0},
		{"native off", clipboardConfig{}, wayland, "linux", "old", "sel", nil, "old", nil, 0},
		{"missing tool", native, wayland, "linux", "old", "", notFound, "old", [][]string{wlPaste}, 1},
		{"empty selection", native, wayland, "linux", "old", "", nil, "", [][]string{wlPaste}, 0},
		{"failed read", native, wayland, "linux", "old", "", errors.New("exit status 1"), "", [][]string{wlPaste}, 1},
		{"nothing copied over ssh", native, map[string]string{"SSH_TTY": "x"}, "linux", "", "sel", nil, "", nil, 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			fr := &fakeRun{out: tt.out, err: func([]string) error { return tt.err }}
			c, buf := newTestClipboard(tt.cfg, tt.env, tt.goos, fr.run)
			c.last = tt.last

			got := c.paste(func(s string) tea.Msg { return s })()
			if got != tt.want {
				t.Fatalf("delivered %#v, want %q", got, tt.want)
			}

			var reads [][]string
			for _, call := range fr.calls {
				reads = append(reads, call.argv)
			}

			if !reflect.DeepEqual(reads, tt.wantReads) {
				t.Fatalf("reads = %v, want %v", reads, tt.wantReads)
			}

			if n := strings.Count(buf.String(), "level=WARN"); n != tt.wantWarn {
				t.Fatalf("warnings = %d, want %d:\n%s", n, tt.wantWarn, buf.String())
			}
		})
	}
}

func TestClipboardPasteHasATimeout(t *testing.T) {
	t.Parallel()

	var hasDeadline bool

	run := func(ctx context.Context, _ []string, _ string, _ io.Writer) error {
		_, hasDeadline = ctx.Deadline()

		return context.DeadlineExceeded
	}
	c, buf := newTestClipboard(clipboardConfig{Native: true}, map[string]string{"WAYLAND_DISPLAY": "w"}, "linux", run)
	c.last = "old"

	if got := c.paste(func(s string) tea.Msg { return s })(); got != "" {
		t.Fatalf("delivered %#v, want empty", got)
	}

	if !hasDeadline {
		t.Fatal("the read ran without a deadline")
	}

	if got := strings.Count(buf.String(), "context deadline exceeded"); got != 1 {
		t.Fatalf("timeout logged %d times, want 1:\n%s", got, buf.String())
	}
}
