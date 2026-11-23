//go:build live

package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/mhersson/aim/internal/core"
	"github.com/mhersson/aim/internal/harness"
)

const codexComplete = "data: {\"type\":\"response.completed\",\"response\":{\"id\":\"fixture\",\"output\":[]}}\n\n"

type codexTerminal struct {
	dir    string
	env    []string
	socket string
	pid    string
}

func (ui *codexTerminal) tmux(ctx context.Context, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, "tmux", append([]string{"-S", ui.socket, "-f", "/dev/null"}, args...)...)
	cmd.Dir, cmd.Env = ui.dir, ui.env
	b, err := cmd.CombinedOutput()

	return string(b), err
}

func launchLiveCodex(t *testing.T, dir, pane string, args, env []string) *codexTerminal {
	t.Helper()

	ui := &codexTerminal{dir: dir, env: append(slices.Clone(env), "AIM_PANE="+pane), socket: filepath.Join(dir, pane+".sock")}

	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()

		if _, err := os.Stat(ui.socket); err == nil {
			_, _ = ui.tmux(ctx, "kill-server")
		}
	})

	if out, err := ui.tmux(t.Context(), append([]string{"new-session", "-d", "-P", "-F", "#{pane_pid}", "-x", "110", "-y", "40"}, args...)...); err != nil {
		t.Fatalf("tmux: %v %s", err, out)
	} else {
		ui.pid = strings.TrimSpace(out)
	}

	return ui
}

func (ui *codexTerminal) stop(t *testing.T) {
	t.Helper()

	ctx, cancel := context.WithTimeout(t.Context(), 6*time.Second)
	defer cancel()

	if out, err := ui.tmux(ctx, "kill-server"); err != nil {
		t.Fatalf("stopping private server: %v %s", err, out)
	}

	for {
		out, err := exec.CommandContext(ctx, "ps", "-p", ui.pid, "-o", "stat=").Output()

		var exit *exec.ExitError
		if (errors.As(err, &exit) && exit.ExitCode() == 1) || strings.HasPrefix(strings.TrimSpace(string(out)), "Z") {
			return
		}

		if err != nil {
			t.Fatal(err)
		}

		select {
		case <-ctx.Done():
			t.Fatalf("private Codex pid %s is still running", ui.pid)
		case <-time.After(50 * time.Millisecond):
		}
	}
}

func (ui *codexTerminal) wait(t *testing.T, needle string) {
	t.Helper()

	ctx, cancel := context.WithTimeout(t.Context(), 6*time.Second)
	defer cancel()

	var screen string

	for {
		out, err := ui.tmux(ctx, "capture-pane", "-p")
		if err == nil {
			screen = out
		}

		if strings.Contains(screen, needle) {
			return
		}

		select {
		case <-ctx.Done():
			t.Fatalf("waiting for %q:\n%s", needle, screen)
		case <-time.After(50 * time.Millisecond):
		}
	}
}

func (ui *codexTerminal) send(t *testing.T, keys ...string) {
	t.Helper()

	if out, err := ui.tmux(t.Context(), append([]string{"send-keys"}, keys...)...); err != nil {
		t.Fatalf("tmux keys: %v %s", err, out)
	}
}

func (ui *codexTerminal) submit(t *testing.T, prompt string) {
	t.Helper()
	ui.send(t, prompt)
	ui.wait(t, prompt)
	ui.send(t, "Enter")
}

type codexDelivery struct {
	Req     *http.Request
	Body    []byte
	Reply   chan string
	Turn    string
	Session string
}

type codexPayload struct {
	Name     string `json:"hook_event_name"`
	Session  string `json:"session_id"`
	Turn     string `json:"turn_id"`
	Cwd      string
	Prompt   string
	Source   string
	Tool     string                   `json:"tool_name"`
	Input    struct{ Command string } `json:"tool_input"`
	Response string                   `json:"tool_response"`
}

type codexFixture struct {
	dir, url  string
	received  chan codexDelivery
	released  sync.Map
	reviewed  atomic.Bool
	userHooks sync.Map
}

func newCodexFixture(t *testing.T, root string) *codexFixture {
	t.Helper()

	fixture := &codexFixture{dir: root, received: make(chan codexDelivery)}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, err := io.ReadAll(r.Body)
		if err != nil {
			return
		}

		var meta struct {
			Session string `json:"session_id"`
			Source  string `json:"thread_source"`
			Turn    string `json:"turn_id"`
		}
		if r.URL.Path == "/v1/responses" {
			if err := json.Unmarshal([]byte(r.Header.Get("X-Codex-Turn-Metadata")), &meta); err != nil {
				t.Error(err)

				return
			}

			if meta.Source == "thread_title" {
				w.Header().Set("Content-Type", "text/event-stream")
				_, _ = io.WriteString(w, codexComplete)

				return
			}

			if _, ok := fixture.released.Load(meta.Turn); fixture.reviewed.Load() && !ok {
				t.Errorf("model request before prompt release for turn %q", meta.Turn)
			}
		}

		if strings.HasSuffix(r.URL.Path, "/user") {
			var h codexPayload
			if err := json.Unmarshal(b, &h); err != nil {
				t.Error(err)

				return
			}

			if !fixture.reviewed.Load() {
				t.Error("unreviewed user hook ran")
			}

			fixture.userHooks.Store(h.Turn, r.URL.Path)

			return
		}

		reply := make(chan string, 1)
		select {
		case fixture.received <- codexDelivery{r, b, reply, meta.Turn, meta.Session}:
		case <-r.Context().Done():
			return
		case <-t.Context().Done():
			return
		}

		select {
		case data := <-reply:
			if strings.HasPrefix(r.URL.Path, "/v1") {
				w.Header().Set("Content-Type", "text/event-stream")
			}

			_, _ = io.WriteString(w, data)
		case <-r.Context().Done():
		case <-t.Context().Done():
		}
	}))
	t.Cleanup(server.Close)
	fixture.url = server.URL

	return fixture
}

func (f *codexFixture) next(t *testing.T) codexDelivery {
	t.Helper()

	select {
	case d := <-f.received:
		return d
	case <-time.After(8 * time.Second):
		t.Fatal("missing fixture request")

		return codexDelivery{}
	}
}

func (f *codexFixture) readHook(t *testing.T, d codexDelivery, pane string) codexPayload {
	t.Helper()

	if d.Req.URL.Path != "/hook/"+pane+"/hook" {
		t.Fatalf("unexpected delivery to %s", d.Req.URL.Path)
	}

	var h codexPayload
	if err := json.Unmarshal(d.Body, &h); err != nil {
		t.Fatal(err)
	}

	if h.Session == "" || h.Cwd != f.dir {
		t.Fatalf("bad session or cwd: %+v", h)
	}

	return h
}

func (f *codexFixture) firstPrompt(t *testing.T, pane, msg string) (codexPayload, codexPayload, chan string) {
	t.Helper()

	var start codexPayload

	for {
		d := f.next(t)

		h := f.readHook(t, d, pane)
		if h.Name == "SessionStart" {
			start = h

			d.Reply <- ""

			continue
		}

		if h.Name != "UserPromptSubmit" || h.Prompt != msg || h.Turn == "" {
			t.Fatalf("unexpected prompt: %+v", h)
		}

		return start, h, d.Reply
	}
}

func (f *codexFixture) release(prompt codexPayload, ack chan string) {
	f.released.Store(prompt.Turn, true)

	ack <- ""
}

func (f *codexFixture) model(t *testing.T, pane string, prompt codexPayload) codexDelivery {
	t.Helper()

	d := f.next(t)
	if d.Req.URL.Path != "/v1/responses" || d.Turn != prompt.Turn || d.Session != prompt.Session {
		t.Fatalf("expected model request for %s/%s, got %s %s/%s", prompt.Session, prompt.Turn, d.Req.URL.Path, d.Session, d.Turn)
	}

	if user, _ := f.userHooks.Load(prompt.Turn); user != "/hook/"+pane+"/user" {
		t.Fatalf("missing independent user hook for %s/%s", pane, prompt.Turn)
	}

	return d
}

func (ui *codexTerminal) skipHookReview(t *testing.T) {
	t.Helper()
	ui.wait(t, "Review hooks")
	foreground, err := ui.tmux(t.Context(), "display-message", "-p", "#{pane_pid} #{pane_current_command}")

	fields := strings.Fields(foreground)
	if err != nil || len(fields) != 2 || fields[1] != "codex" {
		t.Fatalf("foreground %q: %v", foreground, err)
	}

	ps := exec.CommandContext(t.Context(), "ps", "-ww", "-p", fields[0], "-o", "args=")

	out, err := ps.Output()
	if err != nil || !strings.Contains(string(out), "codex --no-daemon -c hooks=") {
		t.Fatalf("foreground argv %q: %v", out, err)
	}

	for ctx, cancel := context.WithTimeout(t.Context(), 6*time.Second); ; {
		ui.send(t, "Down", "Down")

		select {
		case <-time.After(100 * time.Millisecond):
		case <-ctx.Done():
			cancel()
			t.Fatal("selecting continue without trusting")
		}

		out, _ := ui.tmux(ctx, "capture-pane", "-p")
		if strings.Contains(out, "› 3.") {
			cancel()

			break
		}
	}

	ui.send(t, "Enter")
	ui.wait(t, "Ask Codex")
}

func TestLiveCodexHooks(t *testing.T) {
	t.Parallel()

	root, err := os.MkdirTemp("/tmp", "ah")
	if err != nil {
		t.Fatal(err)
	}

	t.Cleanup(func() { _ = os.RemoveAll(root) })

	root, err = filepath.EvalSymlinks(root)
	if err != nil {
		t.Fatal(err)
	}

	env := []string{"PATH=/opt/homebrew/bin:/usr/local/bin:/usr/bin:/bin", "TERM=xterm-256color", "SHELL=/bin/sh", "LANG=C.UTF-8"}

	for _, key := range []string{"HOME", "XDG_CONFIG_HOME", "XDG_STATE_HOME", "XDG_CACHE_HOME", "XDG_RUNTIME_DIR", "XDG_DATA_HOME", "TMPDIR", "CODEX_HOME"} {
		path := filepath.Join(root, key)
		if err = os.Mkdir(path, 0o700); err != nil {
			t.Fatal(err)
		}

		env = append(env, key+"="+path)
	}

	fixture := newCodexFixture(t, root)

	cfg := fmt.Sprintf("approval_policy = 'on-request'\nmodel = 'gpt-6.2-sol'\nmodel_provider = 'fixture'\n[model_providers.fixture]\nname = 'fixture'\nbase_url = '%s/v1'\nwire_api = 'responses'\n", fixture.url)
	if err = os.WriteFile(filepath.Join(root, "CODEX_HOME", "config.toml"), []byte(cfg), 0o600); err != nil {
		t.Fatal(err)
	}

	userHooks := `{"hooks":{"UserPromptSubmit":[{"hooks":[{"type":"command","command":"\"$AIM_BIN\" user"}]}]}}`

	if err = os.WriteFile(filepath.Join(root, "CODEX_HOME", "hooks.json"), []byte(userHooks), 0o600); err != nil {
		t.Fatal(err)
	}

	sock, tracked := statusServer(t)

	bin, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}

	exe := filepath.Join(root, "record")

	recorder := `#!/bin/sh
if [ "$1" = hook ] && [ "$AIM_PANE" = first ] && mkdir "$TMPDIR/captured" 2>/dev/null; then
    cat > "$TMPDIR/captured/input"
    "$AIM_PROBE" hook codex < "$TMPDIR/captured/input" >/dev/null
    exec < "$TMPDIR/captured/input"
fi
exec curl --silent --show-error --max-time 30 --data-binary @- ` + fixture.url + `/hook/"$AIM_PANE"/"$1"
`
	if err = os.WriteFile(exe, []byte(recorder), 0o700); err != nil {
		t.Fatal(err)
	}

	env = append(env, "AIM_BIN="+exe, "AIM_PROBE="+bin)
	a := harness.Codex{}
	launch := []string{"/bin/sh", "-c", `AIM_TTY=$(tty); export AIM_TTY; exec "$@"`, "aim", bin, "exec-owned-codex"}

	initialEnv := append(slices.Clone(env), "AIM_SOCK="+sock, "AIM_CODEX_HOOKS="+a.HookTable(false))
	ui := launchLiveCodex(t, root, "first", launch, initialEnv)
	ui.skipHookReview(t)
	ui.submit(t, "unreviewed-fixture")

	unreviewed := fixture.next(t)
	if unreviewed.Req.URL.Path != "/v1/responses" {
		t.Fatalf("unreviewed handler executed at %s", unreviewed.Req.URL.Path)
	}

	unreviewed.Reply <- codexComplete

	ui.wait(t, "Worked for")
	ui.send(t, "/hooks")
	ui.wait(t, "manage lifecycle hooks")
	ui.send(t, "Enter")
	ui.wait(t, "t trust all")
	ui.send(t, "t")
	ui.wait(t, "enter details")
	ui.send(t, "Escape")
	ui.wait(t, "Ask Codex")
	fixture.reviewed.Store(true)
	ui.submit(t, "fixture-message")

	start, prompt, held := fixture.firstPrompt(t, "first", "fixture-message")
	if start.Name != "" && (start.Source != "startup" || start.Session != prompt.Session) {
		t.Fatalf("start %+v, prompt %+v", start, prompt)
	}

	for _, want := range []string{harness.CodexOwnerStart, "UserPromptSubmit"} {
		select {
		case ev := <-tracked:
			if ev.Name != want || ev.Pane != "first" || strconv.Itoa(ev.OwnerPID) != ui.pid || (ev.Name != harness.CodexOwnerStart && string(ev.Session) != prompt.Session) {
				t.Fatalf("production hook route %+v, foreground pid %s", ev, ui.pid)
			}
		default:
			t.Fatalf("missing production %s", want)
		}
	}

	second := launchLiveCodex(t, root, "second", []string{"/bin/sh", "-i"}, append(slices.Clone(env), "AIM_TEST_HOOKS=hooks="+a.HookTable(false)))
	second.send(t, `codex --no-daemon -c "$AIM_TEST_HOOKS"`, "Enter")
	second.wait(t, "Ask Codex")
	second.submit(t, "parallel-fixture")

	secondStart, parallelPrompt, ack := fixture.firstPrompt(t, "second", "parallel-fixture")
	if secondStart.Source != "startup" || secondStart.Session == prompt.Session || secondStart.Session != parallelPrompt.Session {
		t.Fatalf("second session: %+v", secondStart)
	}

	fixture.release(parallelPrompt, ack)

	fixture.model(t, "second", parallelPrompt).Reply <- codexComplete

	stopped := fixture.next(t)

	stop := fixture.readHook(t, stopped, "second")
	if stop.Name != "Stop" || stop.Session != parallelPrompt.Session || stop.Turn != parallelPrompt.Turn {
		t.Fatalf("second completion: %+v", stop)
	}

	stopped.Reply <- ""

	fixture.release(prompt, held)
	d := fixture.model(t, "first", prompt)

	for _, f := range []string{"edit.txt", "move.txt", "delete.txt"} {
		if err := os.WriteFile(filepath.Join(root, f), []byte("before\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	patch := "*** Begin Patch\n*** Add File: added.txt\n+new\n*** Update File: edit.txt\n@@\n-before\n+after\n*** Update File: move.txt\n*** Move to: moved.txt\n@@\n-before\n+moved\n*** Delete File: delete.txt\n*** End Patch"
	item := map[string]any{"type": "custom_tool_call", "id": "patch_1", "call_id": "call_1", "name": "apply_patch", "input": patch}
	event := func(v any) string {
		b, err := json.Marshal(v)
		if err != nil {
			t.Fatal(err)
		}

		return "data: " + string(b) + "\n\n"
	}

	done := codexComplete
	d.Reply <- event(map[string]any{"type": "response.output_item.done", "item": item, "output_index": 0}) + done

	var hooks []string

	for {
		d = fixture.next(t)
		if d.Req.URL.Path == "/v1/responses" {
			d.Reply <- done

			continue
		}

		h := fixture.readHook(t, d, "first")
		if h.Session != prompt.Session || h.Turn != prompt.Turn {
			t.Fatalf("unexpected identity: %+v", h)
		}

		d.Reply <- ""

		hooks = append(hooks, h.Name)
		if h.Name == "Stop" {
			break
		}

		if h.Tool != "apply_patch" || h.Input.Command != patch {
			t.Fatalf("tool payload: %+v", h)
		}

		if h.Name == "PostToolUse" && !strings.Contains(h.Response, "Exit code: 0") {
			t.Fatalf("patch response: %q", h.Response)
		}
	}

	if strings.Join(hooks, ",") != "PreToolUse,PostToolUse,Stop" {
		t.Fatalf("hooks: %v", hooks)
	}

	second.submit(t, "interrupt-fixture")
	_, secondPrompt, ack := fixture.firstPrompt(t, "second", "interrupt-fixture")
	fixture.release(secondPrompt, ack)
	pending := fixture.model(t, "second", secondPrompt)
	second.send(t, "Escape")
	interrupted := fixture.next(t)

	h := fixture.readHook(t, interrupted, "second")
	interrupted.Reply <- ""

	if h.Name != "Interrupt" || h.Session != secondPrompt.Session || h.Turn != secondPrompt.Turn {
		t.Fatalf("interrupt: %+v", h)
	}

	pending.Reply <- done

	ui.stop(t)
	resumed := launchLiveCodex(t, root, "resumed", a.Launch(a.HookTable(false), core.SessionID(prompt.Session)), env)
	resumed.wait(t, "fixture-message")
	resumed.submit(t, "resume-fixture")

	resumedStart, resumedPrompt, resumeAck := fixture.firstPrompt(t, "resumed", "resume-fixture")
	if resumedStart.Source != "resume" || resumedStart.Session != resumedPrompt.Session || resumedPrompt.Session != prompt.Session || resumedPrompt.Turn == prompt.Turn {
		t.Fatalf("resume: %+v %+v", resumedStart, resumedPrompt)
	}

	approval := map[string]any{"type": "function_call", "name": "exec_command", "call_id": "approve_1", "arguments": `{"cmd":"pwd","sandbox_permissions":"require_escalated","justification":"Print fixture directory?"}`}

	fixture.release(resumedPrompt, resumeAck)

	fixture.model(t, "resumed", resumedPrompt).Reply <- event(map[string]any{"type": "response.output_item.done", "item": approval, "output_index": 0}) + done

	for {
		d = fixture.next(t)
		if d.Req.URL.Path == "/v1/responses" {
			t.Fatal("model continued before its permission prompt")
		}

		h := fixture.readHook(t, d, "resumed")
		d.Reply <- ""

		if h.Name == "PermissionRequest" {
			if h.Tool != "Bash" || h.Turn != resumedPrompt.Turn || h.Session != resumedPrompt.Session {
				t.Fatalf("permission: %+v", h)
			}

			break
		}

		if h.Name != "PreToolUse" {
			t.Fatalf("unexpected hook before permission: %+v", h)
		}
	}

	resumed.wait(t, "Print fixture directory?")
	resumed.send(t, "Escape")
}
