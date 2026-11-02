package harness

import (
	"context"
	"encoding/json"
	"fmt"
	"maps"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/mhersson/aim/internal/core"
)

const (
	testSession    = core.SessionID("11111111-2222-4333-8444-555555555555")
	testTranscript = "/home/user/.claude/projects/-work/11111111-2222-4333-8444-555555555555.jsonl"
)

func readFixture(t *testing.T, name string) []byte {
	t.Helper()

	b, err := os.ReadFile(filepath.Join("testdata", name+".json"))
	if err != nil {
		t.Fatal(err)
	}

	return b
}

func TestParseEvent(t *testing.T) {
	tests := []struct {
		fixture string
		name    string
		state   core.AgentState
		start   bool
		end     bool
		action  string
		set     bool
	}{
		{"session_start_startup", "SessionStart", core.AgentIdle, true, false, "", true},
		{"session_start_resume", "SessionStart", core.AgentIdle, true, false, "", true},
		{"session_start_clear", "SessionStart", core.AgentIdle, true, false, "", true},
		{"session_start_fork", "SessionStart", core.AgentIdle, true, false, "", true},
		{"session_start_compact", "SessionStart", core.AgentUnknown, true, false, "", true},
		{"session_end", "SessionEnd", core.AgentUnknown, false, true, "", true},
		{"user_prompt_submit", "UserPromptSubmit", core.AgentWorking, false, false, "thinking", true},
		{"post_tool_use", "PostToolUse", core.AgentWorking, false, false, "Bash ls", true},
		{"post_tool_use_failure", "PostToolUseFailure", core.AgentWorking, false, false, "Bash false", true},
		{"elicitation_result", "ElicitationResult", core.AgentWorking, false, false, "", false},
		{"pre_tool_use_bash", "PreToolUse", core.AgentWorking, false, false, "Bash ls", true},
		{"pre_tool_use_ask", "PreToolUse", core.AgentBlocked, false, false, "AskUserQuestion", true},
		{"permission_request", "PermissionRequest", core.AgentBlocked, false, false, "Allow Bash rm x?", true},
		{"elicitation", "Elicitation", core.AgentBlocked, false, false, "", true},
		{"notification_permission_prompt", "Notification", core.AgentBlocked, false, false, "m", true},
		{"notification_elicitation_dialog", "Notification", core.AgentBlocked, false, false, "m", true},
		{"notification_elicitation_url_dialog", "Notification", core.AgentBlocked, false, false, "m", true},
		{"notification_agent_needs_input", "Notification", core.AgentBlocked, false, false, "m", true},
		{"notification_idle_prompt", "Notification", core.AgentIdle, false, false, "", false},
		{"notification_auth_success", "Notification", core.AgentUnknown, false, false, "", false},
		{"stop", "Stop", core.AgentIdle, false, false, "", true},
		{"stop_failure", "StopFailure", core.AgentIdle, false, false, "", true},
	}
	for _, tt := range tests {
		t.Run(tt.fixture, func(t *testing.T) {
			ev, err := Claude{}.ParseEvent(readFixture(t, tt.fixture))
			if err != nil {
				t.Fatal(err)
			}

			want := Event{
				Name:       tt.name,
				Session:    testSession,
				Transcript: testTranscript,
				State:      tt.state,
				Start:      tt.start,
				End:        tt.end,
				Cwd:        "/work",
				TurnStart:  tt.name == "UserPromptSubmit",
				Action:     tt.action,
				ActionSet:  tt.set,

				NewConversation: tt.fixture == "session_start_startup" || tt.fixture == "session_start_clear",
			}
			if !reflect.DeepEqual(ev, want) {
				t.Errorf("event = %+v, want %+v", ev, want)
			}
		})
	}
}

// payload builds a hook payload from raw JSON members after the event name.
func payload(event, rest string) []byte {
	if rest != "" {
		rest = "," + rest
	}

	return []byte(`{"hook_event_name":"` + event + `"` + rest + `}`)
}

func TestParseEventAction(t *testing.T) {
	long := strings.Repeat("a", 200)
	cjk := strings.Repeat("\u6f22", 200) // 200 CJK runes of 3 bytes each
	emoji := strings.Repeat("\U0001F600", 200)

	tests := []struct {
		name    string
		event   string
		rest    string
		state   core.AgentState
		action  string
		set     bool
		wantLen int // wanted rune count when > 0
	}{
		{"file_path", "PreToolUse", `"tool_name":"Edit","tool_input":{"file_path":"internal/core/modes.go","command":"x"}`, core.AgentWorking, "Edit internal/core/modes.go", true, 0},
		{"notebook_path", "PreToolUse", `"tool_name":"NotebookEdit","tool_input":{"notebook_path":"a.ipynb","pattern":"p"}`, core.AgentWorking, "NotebookEdit a.ipynb", true, 0},
		{"command", "PreToolUse", `"tool_name":"Bash","tool_input":{"command":"go test ./...","description":"run"}`, core.AgentWorking, "Bash go test ./...", true, 0},
		{"pattern", "PreToolUse", `"tool_name":"Grep","tool_input":{"pattern":"foo","url":"u"}`, core.AgentWorking, "Grep foo", true, 0},
		{"url", "PreToolUse", `"tool_name":"WebFetch","tool_input":{"url":"https://x.dev","query":"q"}`, core.AgentWorking, "WebFetch https://x.dev", true, 0},
		{"query", "PreToolUse", `"tool_name":"WebSearch","tool_input":{"query":"go vt","description":"d"}`, core.AgentWorking, "WebSearch go vt", true, 0},
		{"description", "PreToolUse", `"tool_name":"Task","tool_input":{"description":"explore"}`, core.AgentWorking, "Task explore", true, 0},
		{"no target", "PreToolUse", `"tool_name":"TodoWrite","tool_input":{"todos":[]}`, core.AgentWorking, "TodoWrite", true, 0},
		{"unknown tool generic rule", "PreToolUse", `"tool_name":"mcp__x__y","tool_input":{"url":"u"}`, core.AgentWorking, "mcp__x__y u", true, 0},
		{"unknown tool no target", "PreToolUse", `"tool_name":"mcp__x__y","tool_input":{"a":1}`, core.AgentWorking, "mcp__x__y", true, 0},
		{"empty first target falls through", "PreToolUse", `"tool_name":"Bash","tool_input":{"command":"","description":"d"}`, core.AgentWorking, "Bash d", true, 0},
		{"non-string target skipped", "PreToolUse", `"tool_name":"Bash","tool_input":{"file_path":5,"command":"ls"}`, core.AgentWorking, "Bash ls", true, 0},
		{"missing tool_input", "PreToolUse", `"tool_name":"Bash"`, core.AgentWorking, "Bash", true, 0},
		{"tool_input string", "PreToolUse", `"tool_name":"Bash","tool_input":"ls"`, core.AgentWorking, "Bash", true, 0},
		{"tool_input array", "PreToolUse", `"tool_name":"Bash","tool_input":["ls"]`, core.AgentWorking, "Bash", true, 0},
		{"tool_input null", "PreToolUse", `"tool_name":"Bash","tool_input":null`, core.AgentWorking, "Bash", true, 0},
		{"tool_input number", "PreToolUse", `"tool_name":"Bash","tool_input":7`, core.AgentWorking, "Bash", true, 0},
		{"multi-line command keeps first line", "PreToolUse", `"tool_name":"Bash","tool_input":{"command":"echo a\necho b"}`, core.AgentWorking, "Bash echo a", true, 0},
		{"crlf command keeps first line", "PreToolUse", `"tool_name":"Bash","tool_input":{"command":"echo a\r\necho b"}`, core.AgentWorking, "Bash echo a", true, 0},
		{"escape sequences removed", "PreToolUse", `"tool_name":"Bash","tool_input":{"command":"\u001b[31mred\u001b[0m \u009b2J\u0007x"}`, core.AgentWorking, "Bash [31mred[0m 2Jx", true, 0},
		{"format characters removed", "PreToolUse", `"tool_name":"Bash","tool_input":{"command":"a\u200bb\u202ec\ufeffd"}`, core.AgentWorking, "Bash abcd", true, 0},
		{"tab removed", "PreToolUse", `"tool_name":"Bash","tool_input":{"command":"a\tb"}`, core.AgentWorking, "Bash ab", true, 0},
		{"control characters in the tool name", "PreToolUse", `"tool_name":"Ba\u001bsh\n","tool_input":{"command":"ls"}`, core.AgentWorking, "Bash ls", true, 0},
		{"60 KiB command", "PreToolUse", `"tool_name":"Bash","tool_input":{"command":"` + strings.Repeat("x", 60<<10) + `"}`, core.AgentWorking, "", true, 120},
		{"long target cut", "PreToolUse", `"tool_name":"Bash","tool_input":{"command":"` + long + `"}`, core.AgentWorking, "", true, 120},
		{"CJK cut at 120 runes", "PreToolUse", `"tool_name":"Read","tool_input":{"file_path":"` + cjk + `"}`, core.AgentWorking, "Read " + strings.Repeat("\u6f22", 115), true, 120},
		{"emoji cut at 120 runes", "PreToolUse", `"tool_name":"Read","tool_input":{"file_path":"` + emoji + `"}`, core.AgentWorking, "Read " + strings.Repeat("\U0001F600", 115), true, 120},
		{"ask first question", "PreToolUse", `"tool_name":"AskUserQuestion","tool_input":{"questions":[{"question":"Which db?","header":"DB"},{"question":"Other"}]}`, core.AgentBlocked, "Which db?", true, 0},
		{"ask question with newline", "PreToolUse", `"tool_name":"AskUserQuestion","tool_input":{"questions":[{"question":"Which\ndb?"}]}`, core.AgentBlocked, "Which", true, 0},
		{"ask empty questions", "PreToolUse", `"tool_name":"AskUserQuestion","tool_input":{"questions":[]}`, core.AgentBlocked, "AskUserQuestion", true, 0},
		{"ask no questions", "PreToolUse", `"tool_name":"AskUserQuestion","tool_input":{}`, core.AgentBlocked, "AskUserQuestion", true, 0},
		{"ask questions not a list", "PreToolUse", `"tool_name":"AskUserQuestion","tool_input":{"questions":"why"}`, core.AgentBlocked, "AskUserQuestion", true, 0},
		{"ask question not a string", "PreToolUse", `"tool_name":"AskUserQuestion","tool_input":{"questions":[{"question":5}]}`, core.AgentBlocked, "AskUserQuestion", true, 0},
		{"ask question entry is a number", "PreToolUse", `"tool_name":"AskUserQuestion","tool_input":{"questions":[3]}`, core.AgentBlocked, "AskUserQuestion", true, 0},
		{"ask tool_input string", "PreToolUse", `"tool_name":"AskUserQuestion","tool_input":"x"`, core.AgentBlocked, "AskUserQuestion", true, 0},
		{"permission with target", "PermissionRequest", `"tool_name":"Bash","tool_input":{"command":"rm x"}`, core.AgentBlocked, "Allow Bash rm x?", true, 0},
		{"permission without target", "PermissionRequest", `"tool_name":"Bash"`, core.AgentBlocked, "Allow Bash?", true, 0},
		{"permission tool_input array", "PermissionRequest", `"tool_name":"Bash","tool_input":[1]`, core.AgentBlocked, "Allow Bash?", true, 0},
		{"permission long command", "PermissionRequest", `"tool_name":"Bash","tool_input":{"command":"` + strings.Repeat("y", 60<<10) + `"}`, core.AgentBlocked, "", true, 120},
		{"permission prompt message", "Notification", `"notification_type":"permission_prompt","message":"Claude needs permission"`, core.AgentBlocked, "Claude needs permission", true, 0},
		{"agent needs input message", "Notification", `"notification_type":"agent_needs_input","message":"a\u001b[2Jb\nc"`, core.AgentBlocked, "a[2Jb", true, 0},
		{"blocking notification without message clears", "Notification", `"notification_type":"elicitation_dialog"`, core.AgentBlocked, "", true, 0},
		{"blocking notification message not a string", "Notification", `"notification_type":"permission_prompt","message":{"a":1}`, core.AgentBlocked, "", true, 0},
		{"idle notification leaves the action", "Notification", `"notification_type":"idle_prompt","message":"waiting"`, core.AgentIdle, "", false, 0},
		{"other notification leaves the action", "Notification", `"notification_type":"auth_success","message":"ok"`, core.AgentUnknown, "", false, 0},
		{"elicitation message", "Elicitation", `"message":"Enter token"`, core.AgentBlocked, "Enter token", true, 0},
		{"elicitation long message", "Elicitation", `"message":"` + long + `"`, core.AgentBlocked, "", true, 120},
		{"prompt", "UserPromptSubmit", `"prompt":"do the thing"`, core.AgentWorking, "thinking", true, 0},
		{"stop clears", "Stop", ``, core.AgentIdle, "", true, 0},
		{"stop failure clears", "StopFailure", ``, core.AgentIdle, "", true, 0},
		{"session start clears", "SessionStart", `"session_id":"s","source":"startup"`, core.AgentIdle, "", true, 0},
		{"compact session start clears", "SessionStart", `"session_id":"s","source":"compact"`, core.AgentUnknown, "", true, 0},
		{"session end clears", "SessionEnd", ``, core.AgentUnknown, "", true, 0},
		{"post tool use replaces the action", "PostToolUse", `"tool_name":"Bash","tool_input":{"command":"ls"}`, core.AgentWorking, "Bash ls", true, 0},
		{"post tool use failure replaces the action", "PostToolUseFailure", `"tool_name":"Bash"`, core.AgentWorking, "Bash", true, 0},
		{"post tool use relative file", "PostToolUse", `"cwd":"/a/b","tool_name":"Edit","tool_input":{"file_path":"/a/b/x/y.go"}`, core.AgentWorking, "Edit x/y.go", true, 0},
		{"post tool use failure relative file", "PostToolUseFailure", `"cwd":"/a/b","tool_name":"Read","tool_input":{"file_path":"/a/b/y.go"}`, core.AgentWorking, "Read y.go", true, 0},
		{"pre inside the cwd", "PreToolUse", `"cwd":"/a/b","tool_name":"Edit","tool_input":{"file_path":"/a/b/x/y.go"}`, core.AgentWorking, "Edit x/y.go", true, 0},
		{"pre notebook inside the cwd", "PreToolUse", `"cwd":"/a/b","tool_name":"NotebookEdit","tool_input":{"notebook_path":"/a/b/n.ipynb"}`, core.AgentWorking, "NotebookEdit n.ipynb", true, 0},
		{"pre unclean path inside the cwd", "PreToolUse", `"cwd":"/a/b/","tool_name":"Edit","tool_input":{"file_path":"/a/b/./x//y.go"}`, core.AgentWorking, "Edit x/y.go", true, 0},
		{"pre outside the cwd", "PreToolUse", `"cwd":"/a/b","tool_name":"Edit","tool_input":{"file_path":"/a/c/y.go"}`, core.AgentWorking, "Edit /a/c/y.go", true, 0},
		{"pre parent of the cwd", "PreToolUse", `"cwd":"/a/b","tool_name":"Edit","tool_input":{"file_path":"/a/y.go"}`, core.AgentWorking, "Edit /a/y.go", true, 0},
		{"pre equal to the cwd", "PreToolUse", `"cwd":"/a/b","tool_name":"Read","tool_input":{"file_path":"/a/b"}`, core.AgentWorking, "Read /a/b", true, 0},
		{"pre relative target", "PreToolUse", `"cwd":"/a/b","tool_name":"Edit","tool_input":{"file_path":"x/y.go"}`, core.AgentWorking, "Edit x/y.go", true, 0},
		{"pre sibling sharing the cwd prefix", "PreToolUse", `"cwd":"/a/b","tool_name":"Edit","tool_input":{"file_path":"/a/bc/x"}`, core.AgentWorking, "Edit /a/bc/x", true, 0},
		{"pre empty cwd", "PreToolUse", `"tool_name":"Edit","tool_input":{"file_path":"/a/b/x"}`, core.AgentWorking, "Edit /a/b/x", true, 0},
		{"pre relative cwd", "PreToolUse", `"cwd":"a/b","tool_name":"Edit","tool_input":{"file_path":"/a/b/x"}`, core.AgentWorking, "Edit /a/b/x", true, 0},
		{"pre command is not relativised", "PreToolUse", `"cwd":"/a/b","tool_name":"Bash","tool_input":{"command":"/a/b/x"}`, core.AgentWorking, "Bash /a/b/x", true, 0},
		{"pre pattern is not relativised", "PreToolUse", `"cwd":"/a/b","tool_name":"Grep","tool_input":{"pattern":"/a/b/x"}`, core.AgentWorking, "Grep /a/b/x", true, 0},
		{"pre url is not relativised", "PreToolUse", `"cwd":"/a/b","tool_name":"WebFetch","tool_input":{"url":"/a/b/x"}`, core.AgentWorking, "WebFetch /a/b/x", true, 0},
		{"permission inside the cwd", "PermissionRequest", `"cwd":"/a/b","tool_name":"Edit","tool_input":{"file_path":"/a/b/x/y.go"}`, core.AgentBlocked, "Allow Edit x/y.go?", true, 0},
		{"permission outside the cwd", "PermissionRequest", `"cwd":"/a/b","tool_name":"Edit","tool_input":{"file_path":"/a/c/y.go"}`, core.AgentBlocked, "Allow Edit /a/c/y.go?", true, 0},
		{"permission equal to the cwd", "PermissionRequest", `"cwd":"/a/b","tool_name":"Read","tool_input":{"file_path":"/a/b"}`, core.AgentBlocked, "Allow Read /a/b?", true, 0},
		{"permission relative target", "PermissionRequest", `"cwd":"/a/b","tool_name":"Edit","tool_input":{"file_path":"x"}`, core.AgentBlocked, "Allow Edit x?", true, 0},
		{"permission sibling sharing the cwd prefix", "PermissionRequest", `"cwd":"/a/b","tool_name":"Edit","tool_input":{"file_path":"/a/bc/x"}`, core.AgentBlocked, "Allow Edit /a/bc/x?", true, 0},
		{"permission empty cwd", "PermissionRequest", `"tool_name":"Edit","tool_input":{"file_path":"/a/b/x"}`, core.AgentBlocked, "Allow Edit /a/b/x?", true, 0},
		{"elicitation result leaves", "ElicitationResult", ``, core.AgentWorking, "", false, 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ev, err := Claude{}.ParseEvent(payload(tt.event, tt.rest))
			if err != nil {
				t.Fatal(err)
			}

			if ev.State != tt.state || ev.ActionSet != tt.set {
				t.Errorf("state, set = %v, %v, want %v, %v", ev.State, ev.ActionSet, tt.state, tt.set)
			}

			if tt.wantLen == 0 && ev.Action != tt.action {
				t.Errorf("action = %q, want %q", ev.Action, tt.action)
			}

			if n := utf8.RuneCountInString(ev.Action); n > 120 || (tt.wantLen > 0 && n != tt.wantLen) {
				t.Errorf("action has %d runes, want %d", n, max(tt.wantLen, 120))
			}

			if !utf8.ValidString(ev.Action) {
				t.Errorf("action %q is not valid UTF-8", ev.Action)
			}

			for _, r := range ev.Action {
				if !unicode.IsPrint(r) {
					t.Errorf("action %q holds non-printable %U", ev.Action, r)
				}
			}

			if !ev.ActionSet && ev.Action != "" {
				t.Errorf("action %q on an event that leaves it", ev.Action)
			}
		})
	}
}

func TestParseEventEdit(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		event string
		rest  string
		want  string
	}{
		{"pre edit", "PreToolUse", `"tool_name":"Edit","tool_input":{"file_path":"/r/a.go"}`, "/r/a.go"},
		{"post write", "PostToolUse", `"tool_name":"Write","tool_input":{"file_path":"/r/a.go","content":"x"}`, "/r/a.go"},
		{"pre notebook edit", "PreToolUse", `"tool_name":"NotebookEdit","tool_input":{"notebook_path":"/r/n.ipynb"}`, "/r/n.ipynb"},
		{"pre multi edit", "PreToolUse", `"tool_name":"MultiEdit","tool_input":{"file_path":"/r/a.go"}`, "/r/a.go"},
		{"post notebook edit", "PostToolUse", `"tool_name":"NotebookEdit","tool_input":{"notebook_path":"/r/n.ipynb"}`, "/r/n.ipynb"},
		{"path is cleaned", "PreToolUse", `"tool_name":"Edit","tool_input":{"file_path":"/r/x/../a.go"}`, "/r/a.go"},
		{"path with spaces and non-ASCII", "PostToolUse", `"tool_name":"Edit","tool_input":{"file_path":"/r/my dir/å.go"}`, "/r/my dir/å.go"},
		{"notebook edit ignores file_path", "PreToolUse", `"tool_name":"NotebookEdit","tool_input":{"file_path":"/r/a.go"}`, ""},
		{"edit ignores notebook_path", "PreToolUse", `"tool_name":"Edit","tool_input":{"notebook_path":"/r/n.ipynb"}`, ""},
		{"failed edit", "PostToolUseFailure", `"tool_name":"Edit","tool_input":{"file_path":"/r/a.go"}`, ""},
		{"edit permission request", "PermissionRequest", `"tool_name":"Edit","tool_input":{"file_path":"/r/a.go"}`, ""},
		{"read", "PreToolUse", `"tool_name":"Read","tool_input":{"file_path":"/r/a.go"}`, ""},
		{"bash", "PreToolUse", `"tool_name":"Bash","tool_input":{"command":"sed -i x /r/a.go"}`, ""},
		{"relative path", "PreToolUse", `"tool_name":"Edit","tool_input":{"file_path":"src/a.go"}`, ""},
		{"relative path that climbs", "PostToolUse", `"cwd":"/r","tool_name":"Edit","tool_input":{"file_path":"../a.go"}`, ""},
		{"empty path", "PreToolUse", `"tool_name":"Edit","tool_input":{"file_path":""}`, ""},
		{"path is not a string", "PreToolUse", `"tool_name":"Edit","tool_input":{"file_path":5}`, ""},
		{"tool_input is not an object", "PreToolUse", `"tool_name":"Edit","tool_input":"/r/a.go"`, ""},
		{"no tool_input", "PreToolUse", `"tool_name":"Edit"`, ""},
		{"path holds NUL", "PreToolUse", `"tool_name":"Edit","tool_input":{"file_path":"/r/a\u0000.go"}`, ""},
		{"session event", "SessionStart", `"session_id":"s","source":"startup"`, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			ev, err := Claude{}.ParseEvent(payload(tt.event, tt.rest))
			if err != nil {
				t.Fatal(err)
			}

			if ev.Edit != tt.want {
				t.Errorf("Edit = %q, want %q", ev.Edit, tt.want)
			}
		})
	}
}

func TestParseEventPlan(t *testing.T) {
	t.Parallel()

	const input = `"tool_name":"ExitPlanMode","tool_input":{"planFilePath":"/home/u/.claude/plans/x.md"}`

	tests := []struct {
		name  string
		event string
		rest  string
		want  string
	}{
		{"path", "PreToolUse", input, "/home/u/.claude/plans/x.md"},
		{"path is cleaned", "PreToolUse", `"tool_name":"ExitPlanMode","tool_input":{"planFilePath":"/home/u/.claude/plans/../plans/x.md"}`, "/home/u/.claude/plans/x.md"},
		{"relative path", "PreToolUse", `"tool_name":"ExitPlanMode","tool_input":{"planFilePath":"plans/x.md"}`, ""},
		{"path holds NUL", "PreToolUse", `"tool_name":"ExitPlanMode","tool_input":{"planFilePath":"/p/a\u0000.md"}`, ""},
		{"path is a number", "PreToolUse", `"tool_name":"ExitPlanMode","tool_input":{"planFilePath":5}`, ""},
		{"no path", "PreToolUse", `"tool_name":"ExitPlanMode","tool_input":{"plan":"# x"}`, ""},
		{"tool_input is a string", "PreToolUse", `"tool_name":"ExitPlanMode","tool_input":"/p/x.md"`, ""},
		{"permission request", "PermissionRequest", input, ""},
		{"post tool use", "PostToolUse", input, ""},
		{"other tool", "PreToolUse", `"tool_name":"Write","tool_input":{"planFilePath":"/p/x.md"}`, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			ev, err := Claude{}.ParseEvent(payload(tt.event, tt.rest))
			if err != nil {
				t.Fatal(err)
			}

			if ev.Plan != tt.want {
				t.Errorf("Plan = %q, want %q", ev.Plan, tt.want)
			}
		})
	}

	ev, err := Claude{}.ParseEvent(readFixture(t, "pre_tool_use_exit_plan_mode"))
	if err != nil {
		t.Fatal(err)
	}

	if ev.Plan != "/home/u/.claude/plans/lexical-hugging-muffin.md" || ev.State != core.AgentWorking || ev.Action != "ExitPlanMode" {
		t.Errorf("fixture event = %+v", ev)
	}
}

func TestParseEventBefore(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		event string
		rest  string
		want  bool
	}{
		{"pre edit", "PreToolUse", `"tool_name":"Edit","tool_input":{"file_path":"/r/a.go"}`, true},
		{"pre write", "PreToolUse", `"tool_name":"Write","tool_input":{"file_path":"/r/a.go"}`, true},
		{"pre multi edit", "PreToolUse", `"tool_name":"MultiEdit","tool_input":{"file_path":"/r/a.go"}`, true},
		{"pre notebook edit", "PreToolUse", `"tool_name":"NotebookEdit","tool_input":{"notebook_path":"/r/n.ipynb"}`, true},
		{"subagent pre edit", "PreToolUse", `"agent_id":"a1","tool_name":"Edit","tool_input":{"file_path":"/r/a.go"}`, true},
		{"post edit", "PostToolUse", `"tool_name":"Edit","tool_input":{"file_path":"/r/a.go"}`, false},
		{"pre read", "PreToolUse", `"tool_name":"Read","tool_input":{"file_path":"/r/a.go"}`, false},
		{"pre bash", "PreToolUse", `"tool_name":"Bash","tool_input":{"command":"sed -i x /r/a.go"}`, false},
		{"pre edit with a relative path", "PreToolUse", `"tool_name":"Edit","tool_input":{"file_path":"src/a.go"}`, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			ev, err := Claude{}.ParseEvent(payload(tt.event, tt.rest))
			if err != nil {
				t.Fatal(err)
			}

			if ev.Before != tt.want {
				t.Errorf("Before = %v, want %v", ev.Before, tt.want)
			}
		})
	}
}

func TestParseEventChanged(t *testing.T) {
	t.Parallel()

	files := func(paths ...string) string {
		b, err := json.Marshal(paths)
		if err != nil {
			t.Fatal(err)
		}

		return `{"bashEditDiff":{"changedFiles":` + string(b) + `}}`
	}

	var dirs, firstDirs []string

	for i := range 40 {
		dirs = append(dirs, fmt.Sprintf("/r/d%02d/a.go", i))
		if i < maxChanged {
			firstDirs = append(firstDirs, dirs[i])
		}
	}

	long := "/" + strings.Repeat("a", maxChangedPath)

	// Each path of a hostile list is 1024 bytes that JSON writes as 6 bytes each,
	// so the 16 of them would encode to about 96 KiB.
	hostile := func(c string) (paths, kept []string) {
		for i := range maxChanged {
			dir := fmt.Sprintf("/h%02d/", i)

			paths = append(paths, dir+strings.Repeat(c, maxChangedPath-len(dir)))
		}

		// Each encodes to 6121 bytes and a comma: 5 fit in the 32 KiB budget, 6 do not.
		return paths, paths[:5]
	}
	ctlPaths, ctlKept := hostile("\x01")
	htmlPaths, htmlKept := hostile("<")

	tests := []struct {
		name     string
		event    string
		tool     string
		response string
		want     []string
	}{
		{"two directories", "PostToolUse", "Bash", files("/r/a/x.go", "/r/b/y.go"), []string{"/r/a/x.go", "/r/b/y.go"}},
		{"one directory twice", "PostToolUse", "Bash", files("/r/a/x.go", "/r/a/y.go"), []string{"/r/a/x.go"}},
		{"a nested directory is its own", "PostToolUse", "Bash", files("/r/a/x.go", "/r/a/sub/y.go"), []string{"/r/a/x.go", "/r/a/sub/y.go"}},
		{"the path is cleaned", "PostToolUse", "Bash", files("/r/x/../a.go"), []string{"/r/a.go"}},
		{"paths that clean to one directory", "PostToolUse", "Bash", files("/r/a/x.go", "/r/b/../a/y.go"), []string{"/r/a/x.go"}},
		{"path with spaces and non-ASCII", "PostToolUse", "Bash", files("/r/my dir/\u00e5.go"), []string{"/r/my dir/\u00e5.go"}},
		{"forty directories", "PostToolUse", "Bash", files(dirs...), firstDirs},
		{"a path of the limit is kept", "PostToolUse", "Bash", files(long[:maxChangedPath]), []string{long[:maxChangedPath]}},
		{
			"unusable entries are left out", "PostToolUse", "Bash",
			`{"bashEditDiff":{"changedFiles":["src/a.go",5,null,{"p":"/r/o.go"},"/r/n\u0000.go","` + long + `","/r/ok/a.go"]}}`,
			[]string{"/r/ok/a.go"},
		},
		{"control characters fill the encoded budget", "PostToolUse", "Bash", files(ctlPaths...), ctlKept},
		{"escaped HTML characters fill the encoded budget", "PostToolUse", "Bash", files(htmlPaths...), htmlKept},
		{"a long path does not claim its directory", "PostToolUse", "Bash", files("/r/x/"+long, "/r/x/b.go"), []string{"/r/x/b.go"}},
		{"the list is not an array", "PostToolUse", "Bash", `{"bashEditDiff":{"changedFiles":"/r/a.go"}}`, nil},
		{"the list is an object", "PostToolUse", "Bash", `{"bashEditDiff":{"changedFiles":{"0":"/r/a.go"}}}`, nil},
		{"the list is null", "PostToolUse", "Bash", `{"bashEditDiff":{"changedFiles":null}}`, nil},
		{"the list is empty", "PostToolUse", "Bash", `{"bashEditDiff":{"changedFiles":[]}}`, nil},
		{"no list", "PostToolUse", "Bash", `{"bashEditDiff":{}}`, nil},
		{"no bashEditDiff", "PostToolUse", "Bash", `{"stdout":"x"}`, nil},
		{"bashEditDiff is a string", "PostToolUse", "Bash", `{"bashEditDiff":"/r/a.go"}`, nil},
		{"tool_response is a string", "PostToolUse", "Bash", `"/r/a.go"`, nil},
		{"tool_response is an array", "PostToolUse", "Bash", `[{"bashEditDiff":{"changedFiles":["/r/a.go"]}}]`, nil},
		{"tool_response is null", "PostToolUse", "Bash", `null`, nil},
		{"skipped with no list", "PostToolUse", "Bash", `{"bashEditDiff":{"skipped":true}}`, nil},
		{"a failed Bash", "PostToolUseFailure", "Bash", files("/r/a.go"), nil},
		{"a Bash before it runs", "PreToolUse", "Bash", files("/r/a.go"), nil},
		{"an Edit carrying a list", "PostToolUse", "Edit", files("/r/a.go"), nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			rest := `"cwd":"/r","tool_name":"` + tt.tool + `","tool_input":{"file_path":"/r/e.go","command":"x"}`

			ev, err := Claude{}.ParseEvent(payload(tt.event, rest+`,"tool_response":`+tt.response))
			if err != nil {
				t.Fatal(err)
			}

			if !slices.Equal(ev.Changed, tt.want) {
				t.Errorf("Changed = %q, want %q", ev.Changed, tt.want)
			}

			// The list never changes what the event says otherwise.
			plain, err := Claude{}.ParseEvent(payload(tt.event, rest))
			if err != nil {
				t.Fatal(err)
			}

			// The compact event travels as one line, as the hook command sends it.
			if line, err := json.Marshal(ev); err != nil || len(line) > maxLine*3/4 {
				t.Errorf("encoded event = %d bytes, %v, want at most %d", len(line), err, maxLine*3/4)
			}

			ev.Changed = nil
			if !reflect.DeepEqual(ev, plain) {
				t.Errorf("event = %+v, want %+v as without the list", ev, plain)
			}
		})
	}
}

func TestParseEventSubagent(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		rest string
		want bool
	}{
		{"agent_id", `"tool_name":"Edit","tool_input":{"file_path":"/r/a.go"},"agent_id":"agent-1","agent_type":"Explore"`, true},
		{"agent_id on a tool that is not an edit", `"tool_name":"Bash","agent_id":"agent-1"`, true},
		{"no agent_id", `"tool_name":"Edit","tool_input":{"file_path":"/r/a.go"}`, false},
		{"agent_type alone", `"tool_name":"Edit","tool_input":{"file_path":"/r/a.go"},"agent_type":"reviewer"`, false},
		{"empty agent_id", `"tool_name":"Edit","agent_id":""`, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			ev, err := Claude{}.ParseEvent(payload("PreToolUse", tt.rest))
			if err != nil {
				t.Fatal(err)
			}

			if ev.Subagent != tt.want {
				t.Errorf("Subagent = %v, want %v", ev.Subagent, tt.want)
			}
		})
	}
}

func TestParseEventNewConversation(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name, event, rest string
		want              bool
	}{
		{"clear", "SessionStart", `"session_id":"s","source":"clear"`, true},
		{"startup", "SessionStart", `"session_id":"s","source":"startup"`, true},
		{"resume", "SessionStart", `"session_id":"s","source":"resume"`, false},
		{"fork", "SessionStart", `"session_id":"s","source":"fork"`, false},
		{"compact", "SessionStart", `"session_id":"s","source":"compact"`, false},
		{"no source", "SessionStart", `"session_id":"s"`, false},
		{"unknown source", "SessionStart", `"session_id":"s","source":"teleport"`, false},
		{"another event", "UserPromptSubmit", `"session_id":"s","source":"clear"`, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			ev, err := Claude{}.ParseEvent(payload(tt.event, tt.rest))
			if err != nil {
				t.Fatal(err)
			}

			if ev.NewConversation != tt.want {
				t.Errorf("NewConversation = %v, want %v", ev.NewConversation, tt.want)
			}
		})
	}
}

func TestParseEventSessionStartWithoutSession(t *testing.T) {
	ev, err := Claude{}.ParseEvent([]byte(`{"hook_event_name":"SessionStart","source":"startup"}`))
	if err != nil {
		t.Fatal(err)
	}

	if ev.Start || ev.Session != "" || ev.State != core.AgentIdle {
		t.Errorf("event = %+v, want no Start, no session, idle", ev)
	}
}

func TestParseEventErrors(t *testing.T) {
	for _, fixture := range []string{"invalid", "unknown_event", "missing_event"} {
		t.Run(fixture, func(t *testing.T) {
			if _, err := (Claude{}).ParseEvent(readFixture(t, fixture)); err == nil {
				t.Error("want an error")
			}
		})
	}

	if _, err := (Claude{}).ParseEvent(nil); err == nil {
		t.Error("empty payload: want an error")
	}
}

func TestParseStatus(t *testing.T) {
	const (
		full = `{"hook_event_name":"Status","session_id":"11111111-2222-4333-8444-555555555555",` +
			`"transcript_path":"/t","cwd":"/work","model":{"id":"claude-opus-4-1","display_name":"Opus 4.1"},` +
			`"workspace":{"current_dir":"/work","project_dir":"/work"},"version":"2.1.311",` +
			`"cost":{"total_cost_usd":0.0123,"total_duration_ms":4500},` +
			`"context_window":{"total_input_tokens":15234,"total_output_tokens":4521,"context_window_size":200000,` +
			`"used_percentage":8,"remaining_percentage":92,"current_usage":null}}`
		session = `"session_id":"11111111-2222-4333-8444-555555555555"`
	)

	status := func(mod func(*Event)) Event {
		ev := Event{
			Name: "statusline", Status: true, Session: testSession, Model: "Opus 4.1",
			Context: 8, ContextKnown: true, Window: 200000, InputTokens: 15234,
		}
		mod(&ev)

		return ev
	}

	tests := []struct {
		name    string
		payload string
		want    Event
		wantErr bool
	}{
		{"full payload", full, status(func(*Event) {}), false},
		{
			"null used_percentage",
			`{` + session + `,"model":{"display_name":"Opus"},"context_window":{"total_input_tokens":0,"context_window_size":200000,"used_percentage":null}}`,
			Event{Name: "statusline", Status: true, Session: testSession, Model: "Opus", Window: 200000},
			false,
		},
		{
			"missing used_percentage",
			`{` + session + `,"model":{"display_name":"Opus"},"context_window":{"context_window_size":200000}}`,
			Event{Name: "statusline", Status: true, Session: testSession, Model: "Opus", Window: 200000},
			false,
		},
		{
			"fractional used_percentage",
			`{` + session + `,"model":{"display_name":"Opus"},"context_window":{"total_input_tokens":47000,"context_window_size":200000,"used_percentage":23.5}}`,
			Event{
				Name: "statusline", Status: true, Session: testSession, Model: "Opus",
				Context: 23.5, ContextKnown: true, Window: 200000, InputTokens: 47000,
			},
			false,
		},
		{
			"zero used_percentage is known",
			`{` + session + `,"model":{"display_name":"Opus"},"context_window":{"used_percentage":0}}`,
			Event{Name: "statusline", Status: true, Session: testSession, Model: "Opus", ContextKnown: true},
			false,
		},
		{
			"missing context_window",
			`{` + session + `,"model":{"display_name":"Opus"}}`,
			Event{Name: "statusline", Status: true, Session: testSession, Model: "Opus"},
			false,
		},
		{
			"null context_window",
			`{` + session + `,"model":{"display_name":"Opus"},"context_window":null}`,
			Event{Name: "statusline", Status: true, Session: testSession, Model: "Opus"},
			false,
		},
		{
			"missing model",
			`{` + session + `,"context_window":{"total_input_tokens":15234,"context_window_size":200000,"used_percentage":8}}`,
			status(func(ev *Event) { ev.Model = "" }),
			false,
		},
		{
			"null model",
			`{` + session + `,"model":null,"context_window":{"total_input_tokens":15234,"context_window_size":200000,"used_percentage":8}}`,
			status(func(ev *Event) { ev.Model = "" }),
			false,
		},
		{
			"missing session",
			`{"model":{"display_name":"Opus"},"context_window":{"used_percentage":8}}`,
			Event{Name: "statusline", Status: true, Model: "Opus", Context: 8, ContextKnown: true},
			false,
		},
		{
			"hook_event_name does not name the event",
			`{"hook_event_name":"PostToolUse",` + session + `,"model":{"display_name":"Opus"}}`,
			Event{Name: "statusline", Status: true, Session: testSession, Model: "Opus"},
			false,
		},
		{
			"model display name keeps printable runes",
			`{` + session + `,"model":{"display_name":"Op\u001b[2Jus\n\u200b 4"}}`,
			Event{Name: "statusline", Status: true, Session: testSession, Model: "Op[2Jus 4"},
			false,
		},
		{"missing rate_limits", full, status(func(*Event) {}), false},
		{"invalid JSON", `{"model":`, Event{}, true},
		{"empty payload", ``, Event{}, true},
		{"not an object", `[1]`, Event{}, true},
		{"percentage is a string", `{"context_window":{"used_percentage":"8"}}`, Event{}, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ev, err := Claude{}.ParseStatus([]byte(tt.payload))
			if tt.wantErr {
				if err == nil {
					t.Errorf("ParseStatus = %+v, want an error", ev)
				}

				return
			}

			if err != nil {
				t.Fatal(err)
			}

			if !reflect.DeepEqual(ev, tt.want) {
				t.Errorf("event = %+v, want %+v", ev, tt.want)
			}
		})
	}
}

func TestParseStatusRateLimitsFixture(t *testing.T) {
	t.Parallel()

	payload, err := os.ReadFile(filepath.Join("testdata", "statusline_rate_limits.json"))
	if err != nil {
		t.Fatal(err)
	}

	ev, err := Claude{}.ParseStatus(payload)
	if err != nil {
		t.Fatal(err)
	}

	want := Event{
		Name: "statusline", Status: true, Session: testSession, Model: "Opus 4.1",
		Context: 8, ContextKnown: true, Window: 200000, InputTokens: 15234,
		FiveHour: &RateWindow{23.5, 1738425600}, SevenDay: &RateWindow{41.2, 1738857600},
	}
	if !reflect.DeepEqual(ev, want) {
		t.Errorf("event = %+v, want %+v", ev, want)
	}
}

func TestParseStatusRateLimits(t *testing.T) {
	t.Parallel()

	const head = `{"session_id":"11111111-2222-4333-8444-555555555555","model":{"display_name":"Opus"},"rate_limits":`

	tests := []struct {
		name       string
		limits     string
		five, week *RateWindow
	}{
		{
			"both",
			`{"five_hour":{"used_percentage":23.5,"resets_at":1738425600},"seven_day":{"used_percentage":41.2,"resets_at":1738857600}}`,
			&RateWindow{23.5, 1738425600}, &RateWindow{41.2, 1738857600},
		},
		{
			"weekly only",
			`{"seven_day":{"used_percentage":41.2,"resets_at":1738857600}}`,
			nil, &RateWindow{41.2, 1738857600},
		},
		{
			"null window",
			`{"five_hour":null,"seven_day":{"used_percentage":0,"resets_at":1738857600}}`,
			nil, &RateWindow{0, 1738857600},
		},
		{"null percent", `{"five_hour":{"used_percentage":null,"resets_at":1738425600}}`, nil, nil},
		{"no reset", `{"five_hour":{"used_percentage":23.5}}`, nil, nil},
		{"zero reset", `{"five_hour":{"used_percentage":23.5,"resets_at":0}}`, nil, nil},
		{
			"fractional reset",
			`{"five_hour":{"used_percentage":23.5,"resets_at":1738425600.0}}`,
			&RateWindow{23.5, 1738425600}, nil,
		},
		{"huge reset", `{"five_hour":{"used_percentage":23.5,"resets_at":1e30}}`, nil, nil},
		{"empty", `{}`, nil, nil},
		{"not an object", `"soon"`, nil, nil},
		{"string percent", `{"five_hour":{"used_percentage":"23%","resets_at":1738425600}}`, nil, nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			ev, err := Claude{}.ParseStatus([]byte(head + tt.limits + `}`))
			if err != nil {
				t.Fatal(err)
			}

			if ev.Model != "Opus" {
				t.Errorf("Model = %q, want Opus", ev.Model)
			}

			if !reflect.DeepEqual(ev.FiveHour, tt.five) || !reflect.DeepEqual(ev.SevenDay, tt.week) {
				t.Errorf("windows = %+v, %+v, want %+v, %+v", ev.FiveHour, ev.SevenDay, tt.five, tt.week)
			}
		})
	}
}

func TestParseEventIgnoresLargeFields(t *testing.T) {
	payload := `{"session_id":"s","transcript_path":"/t","hook_event_name":"PostToolUse","tool_name":"Bash",` +
		`"tool_response":{"stdout":"` + strings.Repeat("x", 5<<20) + `"}}`

	ev, err := Claude{}.ParseEvent([]byte(payload))
	if err != nil {
		t.Fatal(err)
	}

	if ev.State != core.AgentWorking || ev.Session != "s" || ev.Transcript != "/t" {
		t.Errorf("event = %+v", ev)
	}

	enc, err := json.Marshal(ev)
	if err != nil {
		t.Fatal(err)
	}

	if len(enc) > 256 {
		t.Errorf("encoded event is %d bytes", len(enc))
	}
}

func TestLaunch(t *testing.T) {
	tests := []struct {
		settings string
		resume   core.SessionID
		want     []string
	}{
		{"", "", []string{"claude"}},
		{"/run/1-claude.json", "", []string{"claude", "--settings", "/run/1-claude.json"}},
		{"", testSession, []string{"claude", "--resume", string(testSession)}},
		{"/run/1-claude.json", testSession, []string{"claude", "--settings", "/run/1-claude.json", "--resume", string(testSession)}},
	}
	for _, tt := range tests {
		if got := (Claude{}).Launch(tt.settings, tt.resume); !slices.Equal(got, tt.want) {
			t.Errorf("Launch(%q, %q) = %q, want %q", tt.settings, tt.resume, got, tt.want)
		}
	}
}

func TestIdentity(t *testing.T) {
	if got := (Claude{}).ID(); got != "claude-code" {
		t.Errorf("ID = %q", got)
	}

	if got := (Claude{}).Program(); got != "claude" {
		t.Errorf("Program = %q", got)
	}
}

type settingsFile struct {
	Hooks map[string][]struct {
		Matcher *string `json:"matcher"`
		Hooks   []struct {
			Type    string   `json:"type"`
			Command string   `json:"command"`
			Args    []string `json:"args"`
			Timeout int      `json:"timeout"`
		} `json:"hooks"`
	} `json:"hooks"`
}

func TestSettings(t *testing.T) {
	const exe = "/opt/my tools/aim"

	raw, err := Claude{}.Settings(exe)
	if err != nil {
		t.Fatal(err)
	}

	var s settingsFile
	if err := json.Unmarshal(raw, &s); err != nil {
		t.Fatal(err)
	}

	events := []string{
		"SessionStart", "SessionEnd", "UserPromptSubmit", "PostToolUse", "PostToolUseFailure",
		"ElicitationResult", "PreToolUse", "PermissionRequest", "Elicitation", "Notification",
		"Stop", "StopFailure",
	}
	if len(s.Hooks) != len(events) {
		t.Errorf("%d events registered, want %d", len(s.Hooks), len(events))
	}

	for _, name := range events {
		groups := s.Hooks[name]
		if len(groups) != 1 || len(groups[0].Hooks) != 1 {
			t.Errorf("%s: groups = %+v, want one group with one handler", name, groups)

			continue
		}

		if groups[0].Matcher != nil {
			t.Errorf("%s: has a matcher", name)
		}

		h := groups[0].Hooks[0]
		if h.Type != "command" || h.Command != exe || !slices.Equal(h.Args, []string{"hook", "claude-code"}) || h.Timeout != 5 {
			t.Errorf("%s: handler = %+v", name, h)
		}
		// Every registered event must be one ParseEvent accepts.
		payload := `{"hook_event_name":"` + name + `"}`
		if _, err := (Claude{}).ParseEvent([]byte(payload)); err != nil {
			t.Errorf("%s: registered but not parsed: %v", name, err)
		}
	}
}

func TestSettingsStatusLine(t *testing.T) {
	tests := []struct {
		name    string
		exe     string
		command string
	}{
		{"plain", "/usr/local/bin/aim", "'/usr/local/bin/aim' statusline"},
		{"space", "/Users/a b/bin/mr", "'/Users/a b/bin/mr' statusline"},
		{"non-ASCII", "/Users/jørgen/漢/mr", "'/Users/jørgen/漢/mr' statusline"},
		{"shell metacharacters", "/opt/$HOME/`x` \"q\" *?~;&|<>(){}/mr", "'/opt/$HOME/`x` \"q\" *?~;&|<>(){}/mr' statusline"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			raw, err := Claude{}.Settings(tt.exe)
			if err != nil {
				t.Fatal(err)
			}

			var s struct {
				StatusLine *struct {
					Type    string `json:"type"`
					Command string `json:"command"`
				} `json:"statusLine"`
			}
			if err := json.Unmarshal(raw, &s); err != nil {
				t.Fatal(err)
			}

			if s.StatusLine == nil || s.StatusLine.Type != "command" || s.StatusLine.Command != tt.command {
				t.Errorf("statusLine = %+v, want a command %q", s.StatusLine, tt.command)
			}
		})
	}

	refused := []struct {
		name string
		exe  string
	}{
		{"single quote", "/opt/it's/mr"},
		{"backslash", `/opt/a\b/mr`},
		{"newline", "/opt/a\nb/mr"},
		{"invalid UTF-8", "/opt/a\xffb/mr"},
	}
	for _, tt := range refused {
		t.Run("refused "+tt.name, func(t *testing.T) {
			raw, err := Claude{}.Settings(tt.exe)
			if err != nil {
				t.Fatalf("Settings: %v, want the hooks without a status line", err)
			}

			var keys map[string]json.RawMessage
			if err := json.Unmarshal(raw, &keys); err != nil {
				t.Fatal(err)
			}

			if _, ok := keys["statusLine"]; ok || len(keys) != 1 {
				t.Errorf("settings keys = %v, want only hooks", slices.Collect(maps.Keys(keys)))
			}

			var s settingsFile
			if err := json.Unmarshal(raw, &s); err != nil {
				t.Fatal(err)
			}

			if len(s.Hooks) != len(claudeEvents()) {
				t.Errorf("%d events registered, want %d", len(s.Hooks), len(claudeEvents()))
			}

			for name, groups := range s.Hooks {
				if len(groups) != 1 || len(groups[0].Hooks) != 1 {
					t.Errorf("%s: groups = %+v, want one group with one handler", name, groups)

					continue
				}

				// JSON rewrites invalid UTF-8 in the hook command as well; only the
				// status line is guarded against that.
				h := groups[0].Hooks[0]
				if h.Type != "command" || (utf8.ValidString(tt.exe) && h.Command != tt.exe) ||
					!slices.Equal(h.Args, []string{"hook", "claude-code"}) || h.Timeout != 5 {
					t.Errorf("%s: handler = %+v", name, h)
				}
			}
		})
	}
}

func TestStatusLineCommandInShells(t *testing.T) {
	shells := []struct {
		name string
		args []string
	}{
		{"sh", []string{"-c"}},
		{"zsh", []string{"-f", "-c"}},
		{"fish", []string{"--no-config", "-c"}},
	}
	dirs := []string{"a b", "jørgen 漢", "$HOME `x` \"q\" *?~;&|<>(){}#!"}

	for _, sh := range shells {
		path, err := exec.LookPath(sh.name)
		if err != nil {
			t.Logf("%s not installed", sh.name)

			continue
		}

		for _, dir := range dirs {
			t.Run(sh.name+"/"+dir, func(t *testing.T) {
				bin := filepath.Join(t.TempDir(), dir)
				if err := os.MkdirAll(bin, 0o755); err != nil {
					t.Fatal(err)
				}

				exe := filepath.Join(bin, "mr")
				if err := os.WriteFile(exe, []byte("#!/bin/sh\nprintf '%s:%s\\n' \"$#\" \"$1\"\n"), 0o755); err != nil {
					t.Fatal(err)
				}

				raw, err := Claude{}.Settings(exe)
				if err != nil {
					t.Fatal(err)
				}

				var s struct {
					StatusLine struct {
						Command string `json:"command"`
					} `json:"statusLine"`
				}
				if err := json.Unmarshal(raw, &s); err != nil {
					t.Fatal(err)
				}

				ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
				defer cancel()

				out, err := exec.CommandContext(ctx, path, append(sh.args, s.StatusLine.Command)...).Output()
				if err != nil || string(out) != "1:statusline\n" {
					t.Errorf("%s -c %q = %q, %v, want one argument, statusline", sh.name, s.StatusLine.Command, out, err)
				}
			})
		}
	}
}

func TestQuotePath(t *testing.T) {
	tests := []struct {
		name    string
		path    string
		want    string
		wantErr string // a fragment of the error, naming the refused character
	}{
		{"plain", "/usr/bin/mr", "'/usr/bin/mr'", ""},
		{"space", "/a b/mr", "'/a b/mr'", ""},
		{"non-ASCII", "/jørgen/漢/\U0001F600", "'/jørgen/漢/\U0001F600'", ""},
		{"double quote and dollar", `/a"b/$c`, `'/a"b/$c'`, ""},
		{"single quote", "/it's/mr", "", "single quote"},
		{"backslash", `/a\b`, "", "backslash"},
		{"newline", "/a\nb", "", "U+000A"},
		{"carriage return", "/a\rb", "", "U+000D"},
		{"tab", "/a\tb", "", "U+0009"},
		{"nul", "/a\x00b", "", "U+0000"},
		{"escape", "/a\x1bb", "", "U+001B"},
		{"delete", "/a\x7fb", "", "U+007F"},
		{"C1 control", "/a\u0085b", "", "U+0085"},
		{"invalid UTF-8", "/a\xffb", "", "UTF-8"},
		{"truncated UTF-8", "/a\xe6\xbc", "", "UTF-8"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := QuotePath(tt.path)
			if tt.wantErr == "" {
				if err != nil || got != tt.want {
					t.Errorf("QuotePath(%q) = %q, %v, want %q", tt.path, got, err, tt.want)
				}

				return
			}

			if err == nil || got != "" || !strings.Contains(err.Error(), tt.wantErr) {
				t.Errorf("QuotePath(%q) = %q, %v, want an error naming %q", tt.path, got, err, tt.wantErr)
			}
		})
	}
}

func TestSettingsRejectsBadExe(t *testing.T) {
	for _, exe := range []string{"", "aim", "./aim", "bin/aim"} {
		if _, err := (Claude{}).Settings(exe); err == nil {
			t.Errorf("Settings(%q): want an error", exe)
		}
	}
}

func TestByID(t *testing.T) {
	h, ok := ByID("claude-code")
	if !ok || h.ID() != "claude-code" {
		t.Errorf("ByID(claude-code) = %v, %v", h, ok)
	}

	if h, ok := ByID("codex"); ok || h != nil {
		t.Errorf("ByID(codex) = %v, %v", h, ok)
	}
}
