package harness

import (
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"path/filepath"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/mhersson/aim/internal/core"
)

// Claude is the Claude Code adapter.
type Claude struct{}

const hookTimeout = 5 // seconds; the hook command answers in milliseconds

// claudeEvents returns the hook events AIM registers.
func claudeEvents() []string {
	return []string{
		"SessionStart", "SessionEnd", "UserPromptSubmit", "PostToolUse", "PostToolUseFailure",
		"ElicitationResult", "PreToolUse", "PermissionRequest", "Elicitation", "Notification",
		"Stop", "StopFailure",
	}
}

func (Claude) ID() string      { return "claude-code" }
func (Claude) Program() string { return "claude" }

func (Claude) Launch(settings string, resume core.SessionID) []string {
	argv := []string{"claude"}
	if settings != "" {
		argv = append(argv, "--settings", settings)
	}

	if resume != "" {
		argv = append(argv, "--resume", string(resume))
	}

	return argv
}

// Settings registers the hooks in the exec form, so no shell parses exe. The
// status line has no exec form: it runs through a shell, so exe goes through
// QuotePath, and a refused path leaves the hooks without a status line.
func (c Claude) Settings(exe string) ([]byte, error) {
	if !filepath.IsAbs(exe) {
		return nil, fmt.Errorf("harness: hook executable %q is not an absolute path", exe)
	}

	type handler struct {
		Type    string   `json:"type"`
		Command string   `json:"command"`
		Args    []string `json:"args"`
		Timeout int      `json:"timeout"`
	}

	type group struct {
		Hooks []handler `json:"hooks"`
	}

	type statusLine struct {
		Type    string `json:"type"`
		Command string `json:"command"`
	}

	h := handler{Type: "command", Command: exe, Args: []string{"hook", c.ID()}, Timeout: hookTimeout}
	events := claudeEvents()

	hooks := make(map[string][]group, len(events))
	for _, name := range events {
		hooks[name] = []group{{Hooks: []handler{h}}}
	}

	settings := struct {
		Hooks      map[string][]group `json:"hooks"`
		StatusLine *statusLine        `json:"statusLine,omitempty"`
	}{Hooks: hooks}

	if quoted, err := QuotePath(exe); err == nil {
		settings.StatusLine = &statusLine{Type: "command", Command: quoted + " statusline"}
	}

	out, err := json.Marshal(settings)
	if err != nil {
		return nil, fmt.Errorf("harness: encode settings: %w", err)
	}

	return out, nil
}

// QuotePath returns path in single quotes for a shell command line. Inside
// single quotes every shell reads characters literally except the closing ',
// and fish also reads \ as an escape; a control character could end the line.
// A path with any of them is refused, as is one that is not valid UTF-8, since
// the JSON settings file would rewrite its bytes.
func QuotePath(path string) (string, error) {
	if !utf8.ValidString(path) {
		return "", fmt.Errorf("harness: cannot quote %q for a shell: it is not valid UTF-8", path)
	}

	for _, r := range path {
		var what string

		switch {
		case r == '\'':
			what = "a single quote"
		case r == '\\':
			what = "a backslash"
		case unicode.IsControl(r):
			what = fmt.Sprintf("the control character %U", r)
		default:
			continue
		}

		return "", fmt.Errorf("harness: cannot quote %q for a shell: it holds %s", path, what)
	}

	return "'" + path + "'", nil
}

// claudePayload holds the only fields read; decoding skips the rest. Only Bash's
// tool_response is read, for the files it lists, so no large one reaches the Event.
type claudePayload struct {
	Name             string `json:"hook_event_name"`
	SessionID        string `json:"session_id"`
	TranscriptPath   string `json:"transcript_path"`
	Cwd              string `json:"cwd"`
	Source           string `json:"source"`
	ToolName         string `json:"tool_name"`
	NotificationType string `json:"notification_type"`
	AgentID          string `json:"agent_id"`
	// Lenient: a payload with an odd shape here must not drop the state.
	ToolInput    json.RawMessage `json:"tool_input"`
	ToolResponse json.RawMessage `json:"tool_response"`
	Message      json.RawMessage `json:"message"`
}

// maxActionRunes bounds the action shown on a card.
const maxActionRunes = 120

// targetKeys are the tool_input fields that name what a tool works on, in order.
var targetKeys = []string{"file_path", "notebook_path", "command", "pattern", "url", "query", "description"}

// printable keeps the printable runes of s (no control, format or other
// non-printing runes) and trims surrounding spaces.
func printable(s string) string {
	return strings.TrimSpace(strings.Map(func(r rune) rune {
		if unicode.IsPrint(r) {
			return r
		}

		return -1
	}, s))
}

// clip makes s an action: printable runes only, at most maxActionRunes.
func clip(s string) string { return capRunes(printable(s), maxActionRunes) }

// jsonString returns raw as a string, or "" when it is not a JSON string.
func jsonString(raw json.RawMessage) string {
	var s string
	if json.Unmarshal(raw, &s) != nil {
		return ""
	}

	return s
}

// pathKeys are the targetKeys that hold file paths; only these are shown
// relative to the working directory.
var pathKeys = map[string]bool{"file_path": true, "notebook_path": true}

// relativeTo shows an absolute path strictly inside the absolute directory cwd
// relative to it; any other path stays as sent, never in a "../" form.
func relativeTo(cwd, path string) string {
	if !filepath.IsAbs(cwd) || !filepath.IsAbs(path) {
		return path
	}

	rel, err := filepath.Rel(filepath.Clean(cwd), filepath.Clean(path))
	if err != nil || rel == "." || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return path
	}

	return rel
}

// toolTarget is "<tool> <target>"; the target is the first non-empty target
// field, reduced to its first line, with file paths relative to cwd. A
// tool_input that is not an object leaves the tool name alone.
func toolTarget(tool, cwd string, fields map[string]json.RawMessage) string {
	for _, k := range targetKeys {
		t := printable(firstLine(jsonString(fields[k])))
		if t == "" {
			continue
		}

		if pathKeys[k] {
			t = relativeTo(cwd, t)
		}

		return tool + " " + t
	}

	return tool
}

const (
	// maxChanged bounds the paths one Bash command contributes, so one command
	// costs few resolutions.
	maxChanged = 16
	// maxChangedPath leaves out a path no real file has.
	maxChangedPath = 1024
	// maxChangedBytes bounds the JSON-encoded size of the kept paths: JSON writes
	// a control character or <, > or & as 6 bytes, so the count and length caps
	// alone could take the event past the socket's line, and the server would
	// drop the event whole. The budget leaves the rest of the event room.
	maxChangedBytes = 32 << 10
)

// editTools maps each tool that changes a file to the tool_input key naming
// it. MultiEdit is no longer documented and stays for older Claude Code.
func editTools() map[string]string {
	return map[string]string{
		"Edit": "file_path", "Write": "file_path", "MultiEdit": "file_path",
		"NotebookEdit": "notebook_path",
	}
}

// editPath is the cleaned absolute path an edit tool's input names, or "" when
// the tool is not an edit tool or the path is missing, relative or holds a NUL.
func editPath(tool string, input json.RawMessage) string {
	key, ok := editTools()[tool]
	if !ok {
		return ""
	}

	return absPath(jsonString(objectFields(input)[key]))
}

// absPath is path cleaned, or "" when it is relative or holds a NUL.
func absPath(path string) string {
	if !filepath.IsAbs(path) || strings.ContainsRune(path, 0) {
		return ""
	}

	return filepath.Clean(path)
}

// changedPaths is the list of files a finished Bash command changed, read
// leniently from its tool_response: any other shape, and any other tool, gives
// none. It keeps the first path of each directory, within the three caps above.
func changedPaths(tool string, response json.RawMessage) []string {
	if tool != "Bash" {
		return nil
	}

	var files []json.RawMessage
	if json.Unmarshal(objectFields(objectFields(response)["bashEditDiff"])["changedFiles"], &files) != nil {
		return nil
	}

	var out []string

	dirs := map[string]bool{}
	size := 0

	for _, raw := range files {
		path := absPath(jsonString(raw))
		if path == "" || len(path) > maxChangedPath || dirs[filepath.Dir(path)] {
			continue
		}

		enc, err := json.Marshal(path)
		if err != nil {
			continue
		}

		// The comma between entries counts too.
		if size += len(enc) + 1; size > maxChangedBytes {
			break
		}

		dirs[filepath.Dir(path)] = true

		if out = append(out, path); len(out) == maxChanged {
			break
		}
	}

	return out
}

// objectFields reads a tool_input or tool_response leniently; anything but an object gives nil.
func objectFields(input json.RawMessage) map[string]json.RawMessage {
	var fields map[string]json.RawMessage
	if json.Unmarshal(input, &fields) != nil {
		return nil
	}

	return fields
}

// toolAction is the PreToolUse action. AskUserQuestion shows its first
// question when it has one.
func toolAction(tool, cwd string, input json.RawMessage) string {
	fields := objectFields(input)
	if tool == "AskUserQuestion" {
		if q := firstQuestion(fields["questions"]); q != "" {
			return clip(firstLine(q))
		}

		return clip(tool)
	}

	return clip(toolTarget(tool, cwd, fields))
}

// firstQuestion returns the text of the first entry of a questions list: a
// string, or an object with a string "question".
func firstQuestion(raw json.RawMessage) string {
	var list []json.RawMessage
	if json.Unmarshal(raw, &list) != nil || len(list) == 0 {
		return ""
	}

	if q := jsonString(list[0]); q != "" {
		return q
	}

	var obj map[string]json.RawMessage
	if json.Unmarshal(list[0], &obj) != nil {
		return ""
	}

	return jsonString(obj["question"])
}

func firstLine(s string) string {
	line, _, _ := strings.Cut(s, "\n")
	line, _, _ = strings.Cut(line, "\r")

	return line
}

func (Claude) ParseEvent(payload []byte) (Event, error) {
	var p claudePayload
	if err := json.Unmarshal(payload, &p); err != nil {
		return Event{}, fmt.Errorf("harness: parse claude-code payload: %w", err)
	}

	if p.Name == "" {
		return Event{}, errors.New("harness: claude-code payload has no hook_event_name")
	}

	ev := Event{
		Name:       p.Name,
		Session:    core.SessionID(p.SessionID),
		Transcript: p.TranscriptPath,
		Cwd:        p.Cwd,
		TurnStart:  p.Name == "UserPromptSubmit",
		Subagent:   p.AgentID != "",
	}
	switch p.Name {
	case "SessionStart":
		ev.ActionSet = true

		ev.Start = p.SessionID != ""
		// A missing or unknown source cannot tell a resume from a new conversation.
		ev.NewConversation = p.Source == "clear" || p.Source == "startup"

		if p.Source != "compact" {
			ev.State = core.AgentIdle
		}
	case "SessionEnd":
		ev.End, ev.ActionSet = true, true
	case "UserPromptSubmit":
		ev.State = core.AgentWorking
		ev.Action, ev.ActionSet = "thinking", true
	case "PostToolUse", "PostToolUseFailure":
		ev.State, ev.ActionSet = core.AgentWorking, true
		ev.Action = clip(toolTarget(printable(p.ToolName), p.Cwd, objectFields(p.ToolInput)))

		if p.Name == "PostToolUse" {
			ev.Edit = editPath(p.ToolName, p.ToolInput)
			ev.Changed = changedPaths(p.ToolName, p.ToolResponse)
		}
	case "ElicitationResult":
		ev.State = core.AgentWorking
	case "PreToolUse":
		ev.State, ev.ActionSet = core.AgentWorking, true
		if p.ToolName == "AskUserQuestion" {
			ev.State = core.AgentBlocked
		}

		ev.Action = toolAction(p.ToolName, p.Cwd, p.ToolInput)
		ev.Edit = editPath(p.ToolName, p.ToolInput)
		ev.Before = ev.Edit != ""

		if p.ToolName == "ExitPlanMode" {
			ev.Plan = absPath(jsonString(objectFields(p.ToolInput)["planFilePath"]))
		}
	case "PermissionRequest":
		ev.State, ev.ActionSet = core.AgentBlocked, true
		ev.Action = permissionAction(p.ToolName, p.Cwd, p.ToolInput)
	case "Elicitation":
		ev.State, ev.ActionSet = core.AgentBlocked, true
		ev.Action = clip(firstLine(jsonString(p.Message)))
	case "Notification":
		switch p.NotificationType {
		case "permission_prompt", "elicitation_dialog", "elicitation_url_dialog", "agent_needs_input":
			ev.State, ev.ActionSet = core.AgentBlocked, true
			ev.Action = clip(firstLine(jsonString(p.Message)))
		case "idle_prompt":
			ev.State = core.AgentIdle
		}
	case "Stop", "StopFailure":
		ev.State, ev.ActionSet = core.AgentIdle, true
	default:
		return Event{}, fmt.Errorf("harness: unknown claude-code event %q", p.Name)
	}

	return ev, nil
}

// permissionAction is "Allow <tool> <target>?".
func permissionAction(tool, cwd string, input json.RawMessage) string {
	return clip("Allow " + toolTarget(printable(tool), cwd, objectFields(input)) + "?")
}

// statusPayload holds the only fields read from the statusline JSON. The
// percentage is a pointer because Claude sends null before its first request;
// rate_limits stays raw so a malformed one drops only the windows.
type statusPayload struct {
	SessionID string `json:"session_id"`
	Model     struct {
		DisplayName string `json:"display_name"`
	} `json:"model"`
	ContextWindow struct {
		Size        int      `json:"context_window_size"`
		InputTokens int      `json:"total_input_tokens"`
		Used        *float64 `json:"used_percentage"`
	} `json:"context_window"`
	RateLimits json.RawMessage `json:"rate_limits"`
}

// rateLimitPayload is one rate-limit window; resets_at is read as a number of
// either form.
type rateLimitPayload struct {
	UsedPercentage *float64 `json:"used_percentage"`
	ResetsAt       *float64 `json:"resets_at"`
}

// rateWindow is the one place that decides whether a window counts: it needs a
// percent and a reset above zero and below 2^63. A recorded Ultra payload would change it.
func rateWindow(w *rateLimitPayload) *RateWindow {
	if w == nil || w.UsedPercentage == nil || w.ResetsAt == nil || *w.ResetsAt <= 0 || *w.ResetsAt >= math.MaxInt64 {
		return nil
	}

	return &RateWindow{Used: *w.UsedPercentage, ResetsAt: int64(math.Floor(*w.ResetsAt))}
}

// ParseStatus reads one statusline payload as received on stdin. The event is
// named "statusline" whatever hook_event_name the payload carries.
func (Claude) ParseStatus(payload []byte) (Event, error) {
	var p statusPayload
	if err := json.Unmarshal(payload, &p); err != nil {
		return Event{}, fmt.Errorf("harness: parse claude-code statusline payload: %w", err)
	}

	ev := Event{
		Name:        "statusline",
		Status:      true,
		Session:     core.SessionID(p.SessionID),
		Model:       clip(p.Model.DisplayName),
		Window:      p.ContextWindow.Size,
		InputTokens: p.ContextWindow.InputTokens,
	}
	if p.ContextWindow.Used != nil {
		ev.Context, ev.ContextKnown = *p.ContextWindow.Used, true
	}

	if len(p.RateLimits) > 0 {
		var limits struct {
			FiveHour *rateLimitPayload `json:"five_hour"`
			SevenDay *rateLimitPayload `json:"seven_day"`
		}

		// A bad rate_limits drops only the windows, not the rest of the status.
		if json.Unmarshal(p.RateLimits, &limits) == nil {
			ev.FiveHour, ev.SevenDay = rateWindow(limits.FiveHour), rateWindow(limits.SevenDay)
		}
	}

	return ev, nil
}
