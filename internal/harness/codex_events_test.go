package harness

import (
	"fmt"
	"os"
	"slices"
	"strings"
	"testing"

	"github.com/mhersson/aim/internal/core"
)

func codexFixture(t *testing.T, name string) []byte {
	t.Helper()

	raw, err := os.ReadFile("testdata/codex/" + name + ".json")
	if err != nil {
		t.Fatal(err)
	}

	return raw
}

func TestCodexEvents(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		fixture                    string
		state                      core.AgentState
		start, fresh, prompt, done bool
	}{
		{"startup", core.AgentIdle, true, true, false, false},
		{"resume", core.AgentIdle, true, false, false, false},
		{"prompt", core.AgentWorking, false, false, true, false},
		{"permission", core.AgentBlocked, false, false, false, false},
		{"stop", core.AgentIdle, false, false, false, true},
		{"interrupt", core.AgentIdle, false, false, false, false},
	} {
		ev, err := (Codex{}).ParseEvent(codexFixture(t, tc.fixture))
		if err != nil {
			t.Fatalf("%s: %v", tc.fixture, err)
		}

		if ev.State != tc.state || ev.Start != tc.start || ev.NewConversation != tc.fresh ||
			ev.TurnStart != tc.prompt || ev.TurnEnd != tc.done || !ev.ActionSet {
			t.Errorf("%s state: %+v", tc.fixture, ev)
		}

		if ev.Session == "" || ev.Harness != "codex" || ev.Cwd != "/work/project" ||
			ev.Model != "gpt-6.2-sol" || ev.TurnID == "" && !ev.Start || ev.Subagent {
			t.Fatalf("%s: %+v, %v", tc.fixture, ev, err)
		}
	}

	for _, raw := range []string{"{", "{}", `{"hook_event_name":"unknown","session_id":"s","turn_id":"t"}`, `{"hook_event_name":"SessionStart"}`, `{"hook_event_name":"Stop","session_id":"main"}`} {
		if _, err := (Codex{}).ParseEvent([]byte(raw)); err == nil {
			t.Errorf("accepted %q", raw)
		}
	}
}

func TestCodexChildPrompt(t *testing.T) {
	t.Parallel()

	ev, err := (Codex{}).ParseEvent([]byte(`{"hook_event_name":"UserPromptSubmit","session_id":"child","turn_id":"child-turn","agent_id":"worker","agent_type":"explorer"}`))
	if err != nil || !ev.Subagent || ev.Session != "child" || ev.TurnID != "child-turn" {
		t.Fatalf("%+v, %v", ev, err)
	}
}

func TestCodexCompact(t *testing.T) {
	t.Parallel()

	ev, err := (Codex{}).ParseEvent([]byte(`{"hook_event_name":"SessionStart","session_id":"main","source":"compact"}`))
	if err != nil || ev.Session != "main" || ev.NewConversation || ev.State != core.AgentUnknown {
		t.Fatalf("%+v, %v", ev, err)
	}
}

func TestCodexRequestInput(t *testing.T) {
	t.Parallel()

	ev, err := (Codex{}).ParseEvent([]byte(`{"hook_event_name":"PreToolUse","session_id":"s","turn_id":"t","tool_name":"request_user_input"}`))
	if err != nil || ev.State != core.AgentBlocked || ev.Action == "" {
		t.Fatalf("%+v, %v", ev, err)
	}
}

func TestCodexPatchPaths(t *testing.T) {
	t.Parallel()

	// The fixture holds five files in one directory; one path per directory is kept.
	want := []string{"/work/project/added.txt"}

	for _, name := range []string{"pre_patch", "post_patch"} {
		ev, err := (Codex{}).ParseEvent(codexFixture(t, name))
		if err != nil || ev.Before != (name == "pre_patch") || ev.State != core.AgentWorking {
			t.Fatalf("%s: %+v, %v", name, ev, err)
		}

		paths := ev.Changed
		if ev.Before {
			paths = ev.BeforePaths
		}

		if !slices.Equal(paths, want) {
			t.Errorf("%s paths %q", name, paths)
		}
	}

	raw := string(codexFixture(t, "post_patch"))
	for _, input := range []string{strings.Replace(raw, "Exit code: 0", "Exit code: 1", 1), strings.Replace(raw, "apply_patch", "Bash", 1)} {
		ev, err := (Codex{}).ParseEvent([]byte(input))
		if err != nil || len(ev.Changed) > 0 {
			t.Errorf("unreported edits %+v, %v", ev, err)
		}
	}
}

func TestCodexPatchHeaders(t *testing.T) {
	t.Parallel()

	patch := "*** Begin Patch\n*** Update File: foo\n@@\n *** Update File: context\n-*** Delete File: removed\n+*** Move to: added\n*** Delete File: ./foo\n*** End Patch"

	paths := codexPatchPaths(patch, "/work")
	if !slices.Equal(paths, []string{"/work/foo"}) {
		t.Fatal(paths)
	}
}

func TestCodexPatchCoverage(t *testing.T) {
	t.Parallel()

	var patch strings.Builder
	patch.WriteString("*** Begin Patch\n*** Add File: same/a\n*** Add File: same/b\n")

	for i := range maxChanged + 4 {
		fmt.Fprintf(&patch, "*** Delete File: d%d/f\n", i)
	}

	patch.WriteString("*** End Patch")

	paths := codexPatchPaths(patch.String(), "/work")
	if len(paths) != maxChanged || paths[0] != "/work/same/a" || paths[1] != "/work/d0/f" {
		t.Fatal(paths)
	}
}
