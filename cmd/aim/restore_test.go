package main

import (
	"bytes"
	"context"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/mhersson/aim/internal/core"
	"github.com/mhersson/aim/internal/harness"
	"github.com/mhersson/aim/internal/store"
)

const (
	sessionA = "7c0b6f1e-3a52-4d8e-9b41-2f5e8a1d0c37"
	sessionB = "0a1b2c3d-4e5f-4a6b-8c7d-9e0f1a2b3c4d"
	sessionC = "11111111-2222-4333-8444-555555555555"
)

var testCfg = core.Config{Theme: "tokyo-night"}

func writeFile(t *testing.T, path, data string) {
	t.Helper()

	if err := os.WriteFile(path, []byte(data), 0o600); err != nil {
		t.Fatal(err)
	}
}

func readFile(t *testing.T, path string) string {
	t.Helper()

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}

	return string(data)
}

// layoutFn builds a tab's layout from the quoted ids of its two panes.
type layoutFn func(a, b string) string

func goodLayout(a, b string) string {
	return `{"split":"vertical","ratio":0.3,"children":[{"pane":` + a + `},{"pane":` + b + `}]}`
}

// brokenLayout lists one of the tab's two panes only.
func brokenLayout(a, _ string) string { return `{"pane":` + a + `}` }

// stateDoc is a file with one tab per layout, holding the panes p1 and p2, p3
// and p4, and so on.
func stateDoc(version string, layouts ...layoutFn) string {
	var tabs, panes []string

	for i, l := range layouts {
		a, b := strconv.Quote("p"+strconv.Itoa(2*i+1)), strconv.Quote("p"+strconv.Itoa(2*i+2))
		tabs = append(tabs, `{"id":"t`+strconv.Itoa(i+1)+`","mode":"bsp","focused":`+a+`,"pane_ids":[`+a+`,`+b+`],"layout":`+l(a, b)+`}`)
		panes = append(panes, a+`:{"kind":"shell","cwd":"/work"}`, b+`:{"kind":"shell"}`)
	}

	return `{"version":` + version + `,"active":0,"tabs":[` + strings.Join(tabs, ",") + `],"panes":{` + strings.Join(panes, ",") + `}}`
}

// carriesNumber reports whether s holds n as a whole number, not as a digit
// inside a longer one.
func carriesNumber(s, n string) bool {
	return regexp.MustCompile(`(^|\D)` + regexp.QuoteMeta(n) + `(\D|$)`).MatchString(s)
}

func TestLoadState(t *testing.T) {
	good, broken := goodLayout, brokenLayout

	t.Run("missing file is a fresh start", func(t *testing.T) {
		st, notices := loadState(filepath.Join(t.TempDir(), "state.json"), false, testCfg)

		if !reflect.DeepEqual(st, core.InitialState(testCfg)) || len(notices) != 0 {
			t.Errorf("state = %+v, notices = %q, want the initial state and none", st, notices)
		}
	})

	t.Run("fresh ignores a valid file and leaves it", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "state.json")
		doc := stateDoc("2", good)
		writeFile(t, path, doc)

		st, notices := loadState(path, true, testCfg)

		if len(st.Panes) != 0 || len(notices) != 0 {
			t.Errorf("state = %+v, notices = %q, want a fresh start", st, notices)
		}

		if readFile(t, path) != doc {
			t.Error("--fresh changed the saved file")
		}
	})

	t.Run("an invalid file becomes state.json.bad, replacing an older one", func(t *testing.T) {
		dir := t.TempDir()
		path := filepath.Join(dir, "state.json")
		writeFile(t, path, `{"version":1,`)
		writeFile(t, path+".bad", "older")

		st, notices := loadState(path, false, testCfg)

		if len(st.Panes) != 0 || !slices.Equal(notices, []string{noticeBadFile}) {
			t.Errorf("state = %+v, notices = %q, want a fresh start and the bad-file notice", st, notices)
		}

		if got := readFile(t, path+".bad"); got != `{"version":1,` {
			t.Errorf("state.json.bad = %q, want the invalid file", got)
		}

		if exists(path) {
			t.Error("state.json is still in place")
		}
	})

	t.Run("a valid file is restored with the theme and layout settings from the config", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "state.json")
		writeFile(t, path, stateDoc("2", good))

		cfg := testCfg
		cfg.Layout.DefaultMode, cfg.Layout.MasterCount = "scrolling", 3

		st, notices := loadState(path, false, cfg)

		if len(notices) != 0 || st.Theme != cfg.Theme || st.Layout != cfg.Layout || len(st.Panes) != 2 || st.Tabs[0].Layout.Ratio != 0.3 {
			t.Errorf("state = %+v, notices = %q, want the saved tab, the config's theme and layout and no notice", st, notices)
		}
	})

	t.Run("an older or newer version restores default layouts, stays in place and is replaced by the current one", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "state.json")
		doc := stateDoc("1", good)
		writeFile(t, path, doc)

		st, notices := loadState(path, false, testCfg)

		if len(notices) != 1 || !carriesNumber(notices[0], "1") || len(st.Panes) != 2 || st.Tabs[0].Layout.Ratio != 0.5 {
			t.Errorf("state = %+v, notices = %q, want one notice carrying the version and a default layout", st, notices)
		}

		if readFile(t, path) != doc {
			t.Error("loading changed the version 1 file")
		}

		data, err := store.Encode(st)
		if err != nil {
			t.Fatal(err)
		}

		if err := store.WriteAtomic(path, data); err != nil {
			t.Fatal(err)
		}

		if _, rep, err := store.Load(path, core.LayoutConfig{}); err != nil || rep.Version != "" {
			t.Errorf("saved file: report %+v, err %v, want the current version", rep, err)
		}
	})

	t.Run("tab layouts that could not be read", func(t *testing.T) {
		tests := []struct {
			name    string
			layouts []layoutFn
			count   string // the number the one notice carries; empty for no notice
		}{
			{"none", []layoutFn{good}, ""},
			{"two", []layoutFn{broken, broken, good}, "2"},
		}
		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				path := filepath.Join(t.TempDir(), "state.json")
				writeFile(t, path, stateDoc("2", tt.layouts...))

				_, notices := loadState(path, false, testCfg)

				switch {
				case tt.count == "" && len(notices) != 0:
					t.Errorf("notices = %q, want none", notices)
				case tt.count != "" && (len(notices) != 1 || !carriesNumber(notices[0], tt.count)):
					t.Errorf("notices = %q, want one carrying %s", notices, tt.count)
				}
			})
		}
	})
}

func TestCheckResume(t *testing.T) {
	agent := func(cwd, session, transcript string) core.Pane {
		return core.Pane{Kind: core.KindAgent, Cwd: cwd, Harness: "claude-code", Session: core.SessionID(session), Transcript: transcript}
	}
	exists := func(p string) bool { return p == "/t/ok.jsonl" }
	stateOf := func(panes map[core.PaneID]core.Pane) core.State { return core.State{Panes: panes} }

	t.Run("a resumable agent keeps its session", func(t *testing.T) {
		in := stateOf(map[core.PaneID]core.Pane{"p1": agent("/w/api", sessionA, "/t/ok.jsonl"), "p2": {Kind: core.KindShell, Cwd: "/w"}})
		out, notices := checkResume(in, exists)

		if len(notices) != 0 || out.Panes["p1"].Session != sessionA || out.Panes["p1"].Transcript != "/t/ok.jsonl" {
			t.Errorf("panes = %+v, notices = %q, want the session kept and no notice", out.Panes, notices)
		}
	})

	t.Run("no session and a missing transcript give the single notice", func(t *testing.T) {
		for name, p := range map[string]core.Pane{
			"no session":         agent("/w/api", "", ""),
			"missing transcript": agent("/w/api", sessionA, "/t/gone.jsonl"),
			"no transcript path": agent("/w/api", sessionA, ""),
		} {
			t.Run(name, func(t *testing.T) {
				in := stateOf(map[core.PaneID]core.Pane{"p1": p})
				out, notices := checkResume(in, exists)

				if len(notices) != 1 || !strings.Contains(notices[0], "api") {
					t.Errorf("notices = %q, want one naming the pane's directory", notices)
				}

				if got := out.Panes["p1"]; got.Session != "" || got.Transcript != "" || got.Kind != core.KindAgent || got.Cwd != "/w/api" {
					t.Errorf("pane = %+v, want an agent with the session cleared", got)
				}

				if in.Panes["p1"].Session != p.Session {
					t.Error("checkResume changed its input")
				}
			})
		}
	})

	t.Run("several give the plural notice", func(t *testing.T) {
		in := stateOf(map[core.PaneID]core.Pane{
			"p1": agent("/w/a", "", ""), "p2": agent("/w/b", sessionB, "/t/gone.jsonl"), "p3": agent("/w/c", sessionA, "/t/ok.jsonl"),
		})
		out, notices := checkResume(in, exists)

		if len(notices) != 1 || !carriesNumber(notices[0], "2") {
			t.Errorf("notices = %q, want one carrying the count 2", notices)
		}

		if out.Panes["p3"].Session != sessionA || out.Panes["p1"].Session != "" || out.Panes["p2"].Session != "" {
			t.Errorf("panes = %+v, want only p3 resumable", out.Panes)
		}
	})
}

func TestFreshFlag(t *testing.T) {
	if f := newRootCmd().Flags().Lookup("fresh"); f == nil || f.Hidden || f.DefValue != "false" {
		t.Fatalf("fresh flag = %+v, want a visible boolean flag", f)
	}
}

// lockedBuffer is an output the program writes while the test reads it.
type lockedBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *lockedBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()

	return b.buf.Write(p)
}

func (b *lockedBuffer) len() int {
	b.mu.Lock()
	defer b.mu.Unlock()

	return b.buf.Len()
}

// testEnv isolates the config and state dirs and returns the run environment
// and a config that keeps the clipboard tools away.
func testEnv(t *testing.T) (env core.Env, cfg string) {
	t.Helper()

	t.Setenv("HOME", shortDir(t))
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	t.Setenv("XDG_STATE_HOME", shortDir(t))
	t.Setenv("SHELL", "/bin/sh")

	// run launches in the cwd; inside a repository leader n opens the worktree picker.
	t.Chdir(t.TempDir())

	env, err := core.DefaultEnv()
	if err != nil {
		t.Fatal(err)
	}

	cfg = filepath.Join(t.TempDir(), "config.toml")
	writeFile(t, cfg, "[clipboard]\nnative = false\n")

	return env, cfg
}

// session is one run of the program with a pipe for its keyboard.
type session struct {
	in     *io.PipeWriter
	out    *lockedBuffer
	cancel context.CancelFunc
	done   chan error
}

func startSession(t *testing.T, env core.Env, cfg string, fresh bool) *session {
	t.Helper()

	in, inW := io.Pipe()
	out := &lockedBuffer{}
	ctx, cancel := context.WithCancel(t.Context())
	s := &session{in: inW, out: out, cancel: cancel, done: make(chan error, 1)}

	go func() {
		s.done <- run(ctx, env, cfg, "", fresh, noClients, tea.WithInput(in), tea.WithOutput(out), tea.WithWindowSize(160, 50))
	}()

	t.Cleanup(func() {
		cancel()

		_ = inW.Close()
	})

	return s
}

func (s *session) keys(t *testing.T, keys string) {
	t.Helper()

	if _, err := s.in.Write([]byte(keys)); err != nil {
		t.Fatal(err)
	}
}

// stop ends the run as a signal would and waits for it to return.
func (s *session) stop(t *testing.T) {
	t.Helper()

	s.cancel()

	select {
	case err := <-s.done:
		if err != nil {
			t.Fatalf("run() error = %v", err)
		}
	case <-time.After(15 * time.Second):
		t.Fatal("run did not return after its context ended")
	}
}

func TestRunSecondInstanceStartsFresh(t *testing.T) {
	env, cfg := testEnv(t)
	dir := core.StateDir(env)

	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}

	unlock, err := store.Lock(dir)
	if err != nil {
		t.Fatal(err)
	}

	defer func() { _ = unlock() }()

	path := filepath.Join(dir, "state.json")
	doc := stateDoc("2", goodLayout)
	writeFile(t, path, doc)

	holderLog := filepath.Join(dir, "aim.log")
	writeFile(t, holderLog, "the holder's run")
	writeFile(t, holderLog+".1", "the holder's previous run")

	s := startSession(t, env, cfg, false)

	ownLog := filepath.Join(dir, "aim-"+strconv.Itoa(os.Getpid())+".log")

	waitFor(t, func() bool { return exists(ownLog) && s.out.len() > 0 })
	// A split repaints the screen, so new output shows the key was handled (and would have been saved).
	shown := s.out.len()
	s.keys(t, "\x00v")
	waitFor(t, func() bool { return s.out.len() > shown })
	s.stop(t)

	if readFile(t, path) != doc {
		t.Error("the second instance wrote the state file")
	}

	if exists(path + ".bad") {
		t.Error("the second instance set the state file aside")
	}

	if got := readFile(t, holderLog); got != "the holder's run" {
		t.Errorf("aim.log = %q, want the holder's log untouched", got)
	}

	if got := readFile(t, holderLog+".1"); got != "the holder's previous run" {
		t.Errorf("aim.log.1 = %q, want the holder's previous log untouched", got)
	}

	if !exists(ownLog) {
		t.Error("the second instance wrote no log of its own")
	}
}

func TestRunSweepsDeadSecondInstanceLogs(t *testing.T) {
	env, cfg := testEnv(t)
	dir := core.StateDir(env)

	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}

	old := time.Now().Add(-secondLogKeep - time.Hour)
	stale := filepath.Join(dir, "aim-"+strconv.Itoa(deadPid(t))+".log")
	writeFile(t, stale, "a crashed second instance")

	if err := os.Chtimes(stale, old, old); err != nil {
		t.Fatal(err)
	}

	s := startSession(t, env, cfg, false)

	waitFor(t, func() bool { return s.out.len() > 0 })
	s.stop(t)

	if exists(stale) {
		t.Error("the lock holder kept a dead second instance's old log")
	}
}

// fakeClaude installs a claude script that logs "<pane> <args>" and waits, and
// a $SHELL that finds it first whatever the login profile does to PATH.
func fakeClaude(t *testing.T) (logPath string) {
	t.Helper()

	dir := t.TempDir()
	logPath = filepath.Join(dir, "claude.log")
	bin := filepath.Join(dir, "bin")

	if err := os.Mkdir(bin, 0o700); err != nil {
		t.Fatal(err)
	}

	script := "#!/bin/sh\necho \"$AIM_PANE $*\" >> '" + logPath + "'\nsleep 3600\n"
	if err := os.WriteFile(filepath.Join(bin, "claude"), []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}

	// Agents run `$SHELL -l -i -c '<script>' sh <argv...>` and then `exec $SHELL -l`;
	// after shift 3, "$@" hands /bin/sh the script, its $0 and the agent's argv.
	shell := "#!/bin/sh\nPATH='" + bin + ":'\"$PATH\"\nexport PATH\n" +
		"if [ \"$3\" = -c ]; then shift 3; exec /bin/sh -c \"$@\"; fi\nexec /bin/sh\n"
	shellPath := filepath.Join(dir, "sh")

	if err := os.WriteFile(shellPath, []byte(shell), 0o700); err != nil {
		t.Fatal(err)
	}

	t.Setenv("SHELL", shellPath)

	return logPath
}

func loadSaved(t *testing.T, path string) (core.State, bool) {
	t.Helper()

	st, _, err := store.Load(path, core.LayoutConfig{})

	return st, err == nil
}

func TestRunRestoresTheSession(t *testing.T) {
	env, cfg := testEnv(t)
	logPath := fakeClaude(t)
	statePath := filepath.Join(core.StateDir(env), "state.json")
	sock := filepath.Join(core.StateDir(env), "run", strconv.Itoa(os.Getpid())+".sock")
	transcripts := t.TempDir()

	report := func(pane core.PaneID, session string, state core.AgentState) {
		t.Helper()

		transcript := filepath.Join(transcripts, session+".jsonl")
		writeFile(t, transcript, "{}\n")

		ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
		defer cancel()

		ev := harness.Event{Pane: pane, Name: "SessionStart", Session: core.SessionID(session), Transcript: transcript, Start: true, State: core.AgentIdle}
		if err := harness.Send(ctx, sock, ev); err != nil {
			t.Fatal(err)
		}

		if state != core.AgentIdle {
			ev = harness.Event{Pane: pane, Name: "UserPromptSubmit", State: state}
			if err := harness.Send(ctx, sock, ev); err != nil {
				t.Fatal(err)
			}
		}
	}

	panesAre := func(n int) func() bool {
		return func() bool {
			st, ok := loadSaved(t, statePath)

			return ok && len(st.Panes) == n
		}
	}

	// First run: a shell, two agents and a split shell.
	first := startSession(t, env, cfg, false)

	waitFor(t, panesAre(1))
	first.keys(t, "\x00n")
	waitFor(t, panesAre(2))
	first.keys(t, "\x00n")
	waitFor(t, panesAre(3))
	first.keys(t, "\x00v")
	waitFor(t, panesAre(4))

	saved, _ := loadSaved(t, statePath)

	agents := saved.AgentPanes()
	if len(agents) != 2 {
		t.Fatalf("agents = %v, want two", agents)
	}

	report(agents[0], sessionA, core.AgentWorking)
	report(agents[1], sessionB, core.AgentWorking)

	waitFor(t, func() bool {
		st, ok := loadSaved(t, statePath)

		return ok && st.Panes[agents[0]].Session == sessionA && st.Panes[agents[1]].Session == sessionB
	})

	before := readFile(t, statePath)

	first.stop(t)

	// The agents died on quit; the file still lists them as agents with their sessions.
	if after := readFile(t, statePath); after != before {
		t.Errorf("the state file changed while shutting down:\nbefore:\n%s\nafter:\n%s", before, after)
	}

	want, _ := loadSaved(t, statePath)
	if got := want.AgentPanes(); !slices.Equal(got, agents) {
		t.Fatalf("saved agents = %v, want %v", got, agents)
	}

	// Second run: the same tabs and layout, agents resumed.
	second := startSession(t, env, cfg, false)

	// The log does not exist until some agent has started.
	waitFor(t, func() bool {
		data, err := os.ReadFile(logPath)

		return err == nil && strings.Count(string(data), "--resume") == 2
	})

	log := readFile(t, logPath)
	for id, session := range map[core.PaneID]string{agents[0]: sessionA, agents[1]: sessionB} {
		if !strings.Contains(log, string(id)+" --settings ") || !strings.Contains(log, "--resume "+session) {
			t.Errorf("claude log lacks a resume of %s for %s:\n%s", session, id, log)
		}
	}

	// A change saves the restored state as it is.
	report(agents[0], sessionC, core.AgentIdle)

	waitFor(t, func() bool {
		st, ok := loadSaved(t, statePath)

		return ok && st.Panes[agents[0]].Session == sessionC
	})

	got, _ := loadSaved(t, statePath)

	if !reflect.DeepEqual(got.Tabs, want.Tabs) || got.Active != want.Active {
		t.Errorf("tabs after the restore = %+v (active %d), want %+v (active %d)", got.Tabs, got.Active, want.Tabs, want.Active)
	}

	for id, m := range want.Panes {
		g := got.Panes[id]
		if id == agents[0] {
			m.Session = sessionC
			m.Transcript = g.Transcript
		}

		if !reflect.DeepEqual(g, m) {
			t.Errorf("pane %s = %+v, want %+v", id, g, m)
		}
	}

	second.stop(t)
}
