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
	"runtime"
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
		// Cleanup begins after the test context is canceled.
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()

		ui.stop(ctx, t)
	})

	if out, err := ui.tmux(t.Context(), append([]string{"new-session", "-d", "-P", "-F", "#{pane_pid}", "-x", "110", "-y", "40"}, args...)...); err != nil {
		t.Fatalf("tmux: %v %s", err, out)
	} else {
		ui.pid = strings.TrimSpace(out)
	}

	return ui
}

func (ui *codexTerminal) stop(ctx context.Context, t *testing.T) {
	t.Helper()

	if out, err := ui.tmux(ctx, "kill-server"); err != nil {
		// An exited server can leave its private socket behind.
		var exit *exec.ExitError

		gone := errors.As(err, &exit) && exit.ExitCode() == 1 && (strings.Contains(out, "no server running on ") || strings.Contains(out, "(No such file or directory)") || strings.Contains(out, "(Connection refused)"))
		if !gone {
			t.Errorf("stopping private tmux server %s: %v %s", ui.socket, err, out)
		}
	}

	ui.waitExit(ctx, t)
}

func (ui *codexTerminal) waitExit(ctx context.Context, t *testing.T) {
	t.Helper()

	if ui.pid == "" {
		return
	}

	for {
		out, err := exec.CommandContext(ctx, "ps", "-p", ui.pid, "-o", "stat=").Output()

		var exit *exec.ExitError
		if (errors.As(err, &exit) && exit.ExitCode() == 1) || strings.HasPrefix(strings.TrimSpace(string(out)), "Z") {
			ui.pid = ""

			return
		}

		if err != nil {
			t.Fatalf("checking private pane pid %s: %v", ui.pid, err)
		}

		select {
		case <-ctx.Done():
			t.Fatalf("private pane pid %s is still running", ui.pid)
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

func (d codexDelivery) replyTool(t *testing.T, item map[string]any) {
	t.Helper()

	data, err := json.Marshal(map[string]any{"type": "response.output_item.done", "item": item, "output_index": 0})
	if err != nil {
		t.Fatal(err)
	}

	d.Reply <- "data: " + string(data) + "\n\n" + codexComplete
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

func (f *codexFixture) config() string {
	return fmt.Sprintf("approval_policy = 'on-request'\nmodel = 'gpt-6.2-sol'\nmodel_provider = 'fixture'\n[model_providers.fixture]\nname = 'fixture'\nbase_url = '%s/v1'\nwire_api = 'responses'\n", f.url)
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

func liveEnvironment(t *testing.T) (string, []string) {
	t.Helper()

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

	return root, env
}

func TestLiveCodexHooks(t *testing.T) {
	t.Parallel()
	root, env := liveEnvironment(t)
	fixture := newCodexFixture(t, root)

	cfg := fixture.config()
	if err := os.WriteFile(filepath.Join(root, "CODEX_HOME", "config.toml"), []byte(cfg), 0o600); err != nil {
		t.Fatal(err)
	}

	userHooks := `{"hooks":{"UserPromptSubmit":[{"hooks":[{"type":"command","command":"\"$AIM_BIN\" user"}]}]}}`

	if err := os.WriteFile(filepath.Join(root, "CODEX_HOME", "hooks.json"), []byte(userHooks), 0o600); err != nil {
		t.Fatal(err)
	}

	sock, tracked := statusServer(t)

	bin, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}

	exe := filepath.Join(root, "record")

	// Codex passes the provider only the variables in env_vars, so the
	// recorder names the test binary itself.
	recorder := `#!/bin/sh
if [ "$1" = codex-statusline ]; then
    exec '` + strings.ReplaceAll(bin, "'", `'\''`) + `' codex-statusline
fi
if [ "$1" = hook ] && [ "$3" = --note ]; then
    input=$(mktemp)
    cat > "$input"
    curl --silent --show-error --max-time 30 --data-binary @"$input" ` + fixture.url + `/hook/"$AIM_PANE"/hook >/dev/null
    exec "$AIM_PROBE" hook codex --note < "$input"
fi
if [ "$1" = hook ] && [ "$AIM_PANE" = first ] && mkdir "$TMPDIR/captured" 2>/dev/null; then
    cat > "$TMPDIR/captured/input"
    "$AIM_PROBE" hook codex < "$TMPDIR/captured/input" >/dev/null
    exec < "$TMPDIR/captured/input"
fi
exec curl --silent --show-error --max-time 30 --data-binary @- ` + fixture.url + `/hook/"$AIM_PANE"/"$1"
`
	if err := os.WriteFile(exe, []byte(recorder), 0o700); err != nil {
		t.Fatal(err)
	}

	env = append(env, "AIM_BIN="+exe, "AIM_PROBE="+bin)
	a := harness.Codex{}
	launch := []string{"/bin/sh", "-c", `AIM_TTY=$(tty); export AIM_TTY; exec "$@"`, "aim", bin, "exec-owned-codex"}

	initialEnv := append(slices.Clone(env), "AIM_SOCK="+sock, "AIM_CODEX_HOOKS="+a.HookTable(false))
	ui := launchLiveCodex(t, root, "first", launch, initialEnv)
	hookEvents, statuses := splitTracked(t, tracked)
	ui.skipHookReview(t)
	logStatus(t, "before the first prompt", statuses.wait(t, "no ready status before the first prompt", func(ev harness.Event) bool {
		return ev.Pane == "first" && ev.RunState == "ready" && strconv.Itoa(ev.OwnerPID) == ui.pid
	}))
	ui.noFooter(t)
	ui.submit(t, "unreviewed-fixture")

	unreviewed := fixture.next(t)
	if unreviewed.Req.URL.Path != "/v1/responses" {
		t.Fatalf("unreviewed handler executed at %s", unreviewed.Req.URL.Path)
	}

	unreviewed.Reply <- codexComplete

	ui.wait(t, "Worked for")
	ui.submit(t, "/rename status-name")
	logStatus(t, "after /rename", statuses.wait(t, "no status with the new thread name", func(ev harness.Event) bool {
		return ev.Pane == "first" && ev.ThreadName == "status-name" && strconv.Itoa(ev.OwnerPID) == ui.pid
	}))
	ui.noFooter(t)
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
		case ev := <-hookEvents:
			if ev.Name != want || ev.Pane != "first" || strconv.Itoa(ev.OwnerPID) != ui.pid || (ev.Name != harness.CodexOwnerStart && string(ev.Session) != prompt.Session) {
				t.Fatalf("production hook route %+v, foreground pid %s", ev, ui.pid)
			}
		case <-time.After(5 * time.Second):
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
	// Without the provider, Codex's own footer shows once a turn is done.
	second.wait(t, codexFooter)
	t.Logf("footer check: the pane without the provider shows %q, the provider's pane does not", codexFooter)

	fixture.release(prompt, held)
	d := fixture.model(t, "first", prompt)

	for _, f := range []string{"edit.txt", "move.txt", "delete.txt"} {
		if err := os.WriteFile(filepath.Join(root, f), []byte("before\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	patch := "*** Begin Patch\n*** Add File: added.txt\n+new\n*** Update File: edit.txt\n@@\n-before\n+after\n*** Update File: move.txt\n*** Move to: moved.txt\n@@\n-before\n+moved\n*** Delete File: delete.txt\n*** End Patch"
	item := map[string]any{"type": "custom_tool_call", "id": "patch_1", "call_id": "call_1", "name": "apply_patch", "input": patch}
	done := codexComplete

	d.replyTool(t, item)

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

	stopCtx, stopCancel := context.WithTimeout(t.Context(), 10*time.Second)
	second.stop(stopCtx, t)
	stopCancel()

	for _, thread := range []struct{ search, history, session string }{
		{"parallel-fixture", "interrupt-fixture", parallelPrompt.Session},
		{"unreviewed-fixture", "fixture-message", prompt.Session},
	} {
		ui.submit(t, "/resume")
		ui.wait(t, "Type to search")
		ui.send(t, thread.search)
		ui.wait(t, "1 / 1")
		ui.send(t, "Enter")
		ui.wait(t, thread.history)
		ui.wait(t, "Ask Codex")

		msg := "picked-" + thread.search
		ui.submit(t, msg)

		start, picked, pickedAck := fixture.firstPrompt(t, "first", msg)
		if start.Source != "resume" || start.Session != thread.session || picked.Session != thread.session {
			t.Fatalf("picker resume: %+v %+v, want %s", start, picked, thread.session)
		}

		fixture.release(picked, pickedAck)

		fixture.model(t, "first", picked).Reply <- codexComplete

		last := fixture.next(t)
		if h := fixture.readHook(t, last, "first"); h.Name != "Stop" || h.Turn != picked.Turn {
			t.Fatalf("unexpected picker completion: %+v", h)
		}

		last.Reply <- ""

		ui.wait(t, "Worked for")
	}

	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	ui.stop(ctx, t)
	cancel()

	noted := launchLiveCodex(t, root, "noted", a.Launch(a.HookTable(true), ""), notedEnv(t, root, env))
	noted.wait(t, "Review hooks")
	noted.send(t, "2")
	noted.wait(t, "› 2. Trust all")
	noted.send(t, "Enter")
	noted.wait(t, "Ask Codex")
	noted.submit(t, "noted-fixture")

	notedStart, notedPrompt, notedAck := fixture.firstPrompt(t, "noted", "noted-fixture")
	if notedStart.Name != "SessionStart" || notedStart.Session != notedPrompt.Session {
		t.Fatalf("noted start %+v, prompt %+v", notedStart, notedPrompt)
	}

	fixture.release(notedPrompt, notedAck)

	notedModel := fixture.model(t, "noted", notedPrompt)
	if !strings.Contains(string(notedModel.Body), showNote[:40]) {
		t.Fatalf("the SessionStart note did not reach the model: %s", notedModel.Body)
	}

	notedModel.Reply <- codexComplete

	notedStop := fixture.next(t)
	if h := fixture.readHook(t, notedStop, "noted"); h.Name != "Stop" || h.Turn != notedPrompt.Turn {
		t.Fatalf("noted completion: %+v", h)
	}

	notedStop.Reply <- ""

	noted.wait(t, "Worked for")

	// Codex reports a failing hook in the transcript as "Hook failed".
	if screen, err := noted.tmux(t.Context(), "capture-pane", "-p"); err != nil || strings.Contains(screen, "Hook failed") {
		t.Fatalf("SessionStart hook with the note: %v\n%s", err, screen)
	}

	resumed := launchLiveCodex(t, root, "resumed", a.Launch(a.HookTable(false), core.SessionID(prompt.Session)), env)
	resumed.wait(t, "picked-unreviewed-fixture")
	resumed.submit(t, "resume-fixture")

	resumedStart, resumedPrompt, resumeAck := fixture.firstPrompt(t, "resumed", "resume-fixture")
	if resumedStart.Source != "resume" || resumedStart.Session != resumedPrompt.Session || resumedPrompt.Session != prompt.Session || resumedPrompt.Turn == prompt.Turn {
		t.Fatalf("resume: %+v %+v", resumedStart, resumedPrompt)
	}

	approval := map[string]any{"type": "function_call", "name": "exec_command", "call_id": "approve_1", "arguments": `{"cmd":"pwd","sandbox_permissions":"require_escalated","justification":"Print fixture directory?"}`}

	fixture.release(resumedPrompt, resumeAck)

	fixture.model(t, "resumed", resumedPrompt).replyTool(t, approval)

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

const (
	// codexFooter is the start of Codex's own footer for the fixture model.
	codexFooter = "GPT-6.2-Sol default ·"
	// codexHint ends the key hint Codex 0.172.0 keeps below its composer.
	codexHint = "? for shortcuts"
)

// noFooter checks that only Codex's key hint shows below its composer, so the
// provider's null tree left the footer blank.
func (ui *codexTerminal) noFooter(t *testing.T) {
	t.Helper()

	screen, err := ui.tmux(t.Context(), "capture-pane", "-p")
	lines := strings.Split(screen, "\n")

	composer := -1

	for i, l := range lines {
		if strings.Contains(l, "› Ask Codex") {
			composer = i
		}
	}

	if err != nil || composer < 0 || strings.Contains(screen, codexFooter) {
		t.Fatalf("Codex footer with the provider: %v\n%s", err, screen)
	}

	for _, l := range lines[composer+1:] {
		if l = strings.TrimSpace(l); l != "" && !strings.HasSuffix(l, codexHint) {
			t.Fatalf("footer line %q below the composer with the provider:\n%s", l, screen)
		}
	}
}

// notedEnv gives the note launch its own CODEX_HOME: trusting its changed
// SessionStart hook would otherwise send later launches back to hook review.
func notedEnv(t *testing.T, root string, env []string) []string {
	t.Helper()

	home := filepath.Join(root, "noted-home")
	if err := os.Mkdir(home, 0o700); err != nil {
		t.Fatal(err)
	}

	for _, f := range []string{"config.toml", "hooks.json"} {
		writeFile(t, filepath.Join(home, f), readFile(t, filepath.Join(root, "CODEX_HOME", f)))
	}

	return append(slices.DeleteFunc(slices.Clone(env), func(kv string) bool { return strings.HasPrefix(kv, "CODEX_HOME=") }), "CODEX_HOME="+home)
}

func logStatus(t *testing.T, when string, ev harness.Event) {
	t.Helper()
	t.Logf("status %s: pane=%s owner=%d session=%s state=%s name=%q model=%q", when, ev.Pane, ev.OwnerPID, ev.Session, ev.RunState, ev.ThreadName, ev.Model)
}

// statusLog keeps every status event, so waiting for one never blocks the
// socket's channel and loses a hook event behind it.
type statusLog struct {
	mu     sync.Mutex
	events []harness.Event
}

func (l *statusLog) wait(t *testing.T, failure string, match func(harness.Event) bool) harness.Event {
	t.Helper()

	var found harness.Event

	waitUntil(t, failure, func() bool {
		l.mu.Lock()
		defer l.mu.Unlock()

		i := slices.IndexFunc(l.events, match)
		if i >= 0 {
			found = l.events[i]
		}

		return i >= 0
	})

	return found
}

// splitTracked drains in until the test ends, keeping status events apart
// from the hook events whose order the test checks.
func splitTracked(t *testing.T, in <-chan harness.Event) (<-chan harness.Event, *statusLog) {
	t.Helper()

	hooks, statuses := make(chan harness.Event, 16), &statusLog{}

	go func() {
		for {
			select {
			case ev := <-in:
				if ev.Status {
					statuses.mu.Lock()
					statuses.events = append(statuses.events, ev)
					statuses.mu.Unlock()

					continue
				}

				select {
				case hooks <- ev:
				case <-t.Context().Done():
					return
				}
			case <-t.Context().Done():
				return
			}
		}
	}()

	return hooks, statuses
}

func liveCommand(t *testing.T, dir string, env []string, program string, args ...string) string {
	t.Helper()
	c := exec.CommandContext(t.Context(), program, args...)
	c.Dir, c.Env = dir, env

	out, err := c.CombinedOutput()
	if err != nil {
		t.Fatalf("%s %v: %v %s", program, args, err, out)
	}

	return strings.TrimSpace(string(out))
}

func TestLiveMixedAgentRestore(t *testing.T) {
	t.Parallel()
	root, env := liveEnvironment(t)
	bin := filepath.Join(root, "bin")
	repo := filepath.Join(root, "repo")
	wt := filepath.Join(root, "codex-tree")

	for _, dir := range []string{bin, repo} {
		if err := os.Mkdir(dir, 0o700); err != nil {
			t.Fatal(err)
		}
	}

	aim := filepath.Join(bin, "aim")
	liveCommand(t, ".", nil, "go", "build", "-o", aim, ".")
	liveCommand(t, ".", nil, "go", "build", "-o", filepath.Join(bin, "claude"), "./testdata/live-claude")

	shell, profile := "/bin/bash", ".bash_profile"
	if runtime.GOOS == "darwin" {
		shell, profile = "/bin/zsh", ".zprofile"
	}

	writeFile(t, filepath.Join(root, "HOME", profile), "export PATH="+bin+":$PATH\n")
	writeFile(t, filepath.Join(root, "HOME", ".zshrc"), "")

	for i, v := range env {
		if path, ok := strings.CutPrefix(v, "PATH="); ok {
			env[i] = "PATH=" + bin + ":" + path
		}

		if strings.HasPrefix(v, "SHELL=") {
			env[i] = "SHELL=" + shell
		}
	}

	env = append(env, "GIT_CONFIG_NOSYSTEM=1", "GIT_TERMINAL_PROMPT=0")
	t.Log(liveCommand(t, repo, env, "codex", "--version"), "with scratch AIM and compiled Claude fixture")
	liveCommand(t, repo, env, "git", "init", "-b", "main")
	liveCommand(t, repo, env, "git", "-c", "user.name=fixture", "-c", "user.email=test@example.invalid", "commit", "--allow-empty", "-m", "fixture")
	liveCommand(t, repo, env, "git", "worktree", "add", "-b", "codex-tree", wt)
	fixture := newCodexFixture(t, wt)
	cfg := fixture.config() + fmt.Sprintf("[projects.'%s']\ntrust_level='trusted'\n", wt)
	writeFile(t, filepath.Join(root, "CODEX_HOME", "config.toml"), cfg)
	config := filepath.Join(root, "config.toml")
	writeFile(t, config, "icons=false\n[diff]\nmode='turn'\n")

	start := func(name string) *codexTerminal {
		return launchLiveCodex(t, repo, name, []string{aim, "--config", config}, env)
	}
	ui := start("first")
	leader := func(k string) { ui.send(t, "C-Space", k) }
	closed := func(label string) {
		waitUntil(t, "still showing "+label, func() bool {
			out, err := ui.tmux(t.Context(), "capture-pane", "-p")

			return err == nil && !strings.Contains(out, label)
		})
	}
	open := func(codex bool) {
		leader("n")
		ui.wait(t, "New agent · Claude Code")

		if codex {
			ui.send(t, "Tab")
			ui.wait(t, "New agent · Codex")
			ui.send(t, "/", "codex-tree")
			ui.wait(t, "1 of 2")
			ui.send(t, "Enter")
		}

		ui.send(t, "Enter")
	}

	ui.wait(t, "layout")
	open(false)
	ui.wait(t, "Claude fixture ready")
	ui.submit(t, "claude-conversation")
	ui.wait(t, "Claude fixture finished")
	leader("d")
	ui.wait(t, "A claude-edit.txt")
	leader("d")
	closed("A claude-edit.txt")
	leader("c")
	ui.wait(t, "2 ")
	open(true)
	leader("z")
	ui.wait(t, "Review hooks")
	waitFor(t, func() bool {
		out, _ := ui.tmux(t.Context(), "capture-pane", "-p")
		if strings.Contains(out, "› 2. Trust all") {
			return true
		}

		ui.send(t, "2")

		return false
	})
	ui.send(t, "Enter")
	ui.wait(t, "Ask Codex")
	writeFile(t, filepath.Join(wt, "before-prompt.txt"), "unrelated\n")
	ui.submit(t, "codex-conversation")

	model := fixture.next(t)
	if model.Req.URL.Path != "/v1/responses" || model.Session == "" {
		t.Fatalf("unexpected model delivery %s", model.Req.URL.Path)
	}

	model.replyTool(t, map[string]any{"type": "custom_tool_call", "id": "patch_1", "call_id": "call_1", "name": "apply_patch", "input": "*** Begin Patch\n*** Add File: codex-edit.txt\n+known edit\n*** End Patch"})

	fixture.next(t).Reply <- codexComplete

	ui.wait(t, "Worked for")
	leader("d")
	ui.wait(t, "turn 1/1")
	ui.wait(t, "A codex-edit.txt")

	screen, err := ui.tmux(t.Context(), "capture-pane", "-p")
	if err != nil || strings.Contains(screen, "before-prompt.txt") {
		t.Fatalf("turn diff: %v\n%s", err, screen)
	}

	leader("d")
	closed("turn 1/1")
	ui.submit(t, "approval-conversation")
	fixture.next(t).replyTool(t, map[string]any{"type": "function_call", "name": "exec_command", "call_id": "approve_1", "arguments": `{"cmd":"pwd","sandbox_permissions":"require_escalated","justification":"Print fixture directory?"}`})
	ui.wait(t, "Print fixture directory?")
	leader("a")
	ui.wait(t, "waiting for a prompt")
	ui.wait(t, "blocked 1 idle 1")
	ui.wait(t, "claude waiting for a prompt")
	ui.wait(t, "codex Allow Bash pwd?")
	leader("a")
	ui.send(t, "Enter")

	fixture.next(t).Reply <- codexComplete

	leader("a")
	ui.wait(t, "waiting for a prompt")
	waitFor(t, func() bool {
		out, _ := ui.tmux(t.Context(), "capture-pane", "-p")

		return strings.Contains(out, "claude waiting for a prompt") && strings.Contains(out, "codex waiting for a prompt") && !strings.Contains(out, "blocked 1")
	})
	leader("a")

	statePath := filepath.Join(core.StateDir(core.Env{Home: filepath.Join(root, "HOME"), GOOS: runtime.GOOS, Getenv: func(k string) string { return filepath.Join(root, k) }}), "state.json")
	assertSessions := func() {
		waitFor(t, func() bool {
			st, ok := loadSaved(t, statePath)
			if !ok {
				return false
			}

			found := map[string]bool{}

			for _, p := range st.Panes {
				switch p.Harness {
				case "codex":
					found[p.Harness] = found[p.Harness] || (string(p.Session) == model.Session && p.Cwd == wt)
				case "claude-code":
					found[p.Harness] = string(p.Session) == "12345678-1234-4234-8234-123456789abc" && p.Cwd == repo
				}
			}

			return found["codex"] && found["claude-code"]
		})
	}
	assertSessions()

	quit := func() {
		leader("q")
		ui.wait(t, "Quit AIM?")
		ui.send(t, "y")

		ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
		defer cancel()

		ui.waitExit(ctx, t)
	}

	leader("c")
	ui.wait(t, "3 ")
	ui.submit(t, "cd "+wt)
	ui.submit(t, "codex")
	ui.wait(t, "Ask Codex")
	ui.submit(t, "typed-conversation")

	typed := fixture.next(t)
	if typed.Session == "" || typed.Session == model.Session {
		t.Fatalf("hand-typed session %q", typed.Session)
	}

	typed.Reply <- codexComplete

	ui.wait(t, "Worked for")

	var typedID core.PaneID

	waitFor(t, func() bool {
		st, ok := loadSaved(t, statePath)
		for id, p := range st.Panes {
			if ok && p.Harness == "codex" && string(p.Session) == typed.Session {
				typedID = id

				return true
			}
		}

		return false
	})

	quit()
	// Only the hand-typed conversation is unavailable on the second launch.
	rollouts, err := filepath.Glob(filepath.Join(root, "CODEX_HOME", "sessions", "*", "*", "*", "*-"+typed.Session+".jsonl"))
	if err != nil || len(rollouts) != 1 {
		t.Fatalf("typed rollout: paths=%v err=%v", rollouts, err)
	}

	if err := os.Remove(rollouts[0]); err != nil {
		t.Fatal(err)
	}

	ui = start("second")

	ctx, cancel := context.WithTimeout(t.Context(), 25*time.Second)
	defer cancel()

	for {
		st, ok := loadSaved(t, statePath)
		if ok && st.Panes[typedID].Kind == core.KindAgent && string(st.Panes[typedID].Session) != typed.Session {
			break
		}

		select {
		case <-ctx.Done():
			t.Fatalf("refused resume did not retry: %+v", st.Panes[typedID])
		case <-time.After(50 * time.Millisecond):
		}
	}

	ui.wait(t, "Ask Codex")
	ui.submit(t, "fresh-after-refusal")

	fresh := fixture.next(t)
	if fresh.Session == typed.Session || fresh.Session == model.Session {
		t.Fatalf("fresh session %s", fresh.Session)
	}

	fresh.Reply <- codexComplete

	ui.wait(t, "Worked for")
	waitFor(t, func() bool {
		st, ok := loadSaved(t, statePath)

		return ok && string(st.Panes[typedID].Session) == fresh.Session
	})
	ui.submit(t, "/quit")
	waitFor(t, func() bool {
		st, ok := loadSaved(t, statePath)

		return ok && st.Panes[typedID].Kind == core.KindShell
	})
	ui.submit(t, "printf '%s%s\\n' shell- ready")
	ui.wait(t, "shell-ready")
	leader("2")
	ui.wait(t, "codex-conversation")
	assertSessions()
	ui.submit(t, "continued-codex")

	resume := fixture.next(t)
	if resume.Session != model.Session {
		t.Fatalf("resumed session %s, want %s", resume.Session, model.Session)
	}

	resume.replyTool(t, map[string]any{"type": "function_call", "name": "exec_command", "call_id": "resumed_pwd", "arguments": `{"cmd":"pwd"}`})
	result := fixture.next(t)

	type toolResult struct {
		CallID string `json:"call_id"`
		Output string
	}

	var request struct{ Input []toolResult }
	if err := json.Unmarshal(result.Body, &request); err != nil {
		t.Fatal(err)
	}

	found := slices.ContainsFunc(request.Input, func(item toolResult) bool {
		return item.CallID == "resumed_pwd" && strings.HasSuffix(strings.TrimSpace(item.Output), "\n"+wt)
	})
	if !found {
		t.Fatalf("resumed pwd did not report %s: %s", wt, result.Body)
	}

	result.Reply <- codexComplete

	ui.wait(t, "Worked for")
	leader("1")
	ui.wait(t, "continued claude-conversation")

	if got := readFile(t, filepath.Join(root, "HOME", "claude-resumed-cwd")); got != repo {
		t.Fatalf("resumed Claude cwd %q want %q", got, repo)
	}

	leader("d")
	ui.wait(t, "A claude-edit.txt")
	quit()
}
