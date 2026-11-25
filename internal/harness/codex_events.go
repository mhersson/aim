package harness

import (
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"unicode"

	"github.com/mhersson/aim/internal/core"
)

type codexHook struct {
	Name       string          `json:"hook_event_name"`
	Session    core.SessionID  `json:"session_id"`
	Turn       string          `json:"turn_id"`
	Agent      string          `json:"agent_id"`
	Transcript string          `json:"transcript_path"`
	Cwd        string          `json:"cwd"`
	Model      string          `json:"model"`
	Source     string          `json:"source"`
	Tool       string          `json:"tool_name"`
	Input      json.RawMessage `json:"tool_input"`
	Response   json.RawMessage `json:"tool_response"`
}

// Interrupt has no agent_id. The gate must match its session and turn to the
// foreground owner even when Subagent is false.
func (Codex) ParseEvent(payload []byte) (Event, error) {
	var h codexHook
	if err := json.Unmarshal(payload, &h); err != nil {
		return Event{}, fmt.Errorf("harness: parse codex payload: %w", err)
	}

	if h.Session == "" || (h.Name != "SessionStart" && h.Turn == "") {
		return Event{}, errors.New("harness: codex event missing session or turn identity")
	}

	ev := Event{
		Harness: "codex", Name: h.Name, Session: h.Session, TurnID: h.Turn,
		Subagent: h.Agent != "", Transcript: absPath(h.Transcript), Cwd: absPath(h.Cwd),
		Model: clip(h.Model), ActionSet: true,
	}
	switch h.Name {
	case "SessionStart":
		ev.Start = true

		ev.NewConversation = h.Source == "startup" || h.Source == "clear"
		if h.Source != "compact" {
			ev.State = core.AgentIdle
		}
	case "UserPromptSubmit":
		ev.State, ev.TurnStart, ev.Action = core.AgentWorking, true, "thinking"
	case "PermissionRequest":
		ev.State, ev.Action = core.AgentBlocked, permissionAction(h.Tool, ev.Cwd, h.Input)
	case "PreToolUse", "PostToolUse":
		ev.State = core.AgentWorking
		if h.Tool == "apply_patch" {
			h.patch(&ev)

			break
		}

		ev.Action = toolAction(h.Tool, ev.Cwd, h.Input)
		if h.Tool == "request_user_input" && h.Name == "PreToolUse" {
			ev.State = core.AgentBlocked
		}
	case "Stop", "Interrupt":
		ev.State, ev.TurnEnd = core.AgentIdle, h.Name == "Stop"
	default:
		return Event{}, fmt.Errorf("harness: unknown codex event %q", h.Name)
	}

	return ev, nil
}

func (h codexHook) patch(ev *Event) {
	ev.Action = h.Tool
	if h.Name == "PostToolUse" {
		status, _, _ := strings.Cut(jsonString(h.Response), "\n")
		if status != "Exit code: 0" {
			return
		}
	}

	paths := codexPatchPaths(jsonString(objectFields(h.Input)["command"]), ev.Cwd)

	if len(paths) > 0 {
		ev.Action = clip(h.Tool + " " + relativeTo(ev.Cwd, paths[0]))
	}

	if h.Name == "PreToolUse" {
		ev.BeforePaths, ev.Before = paths, len(paths) > 0
	} else {
		ev.Changed = paths
	}
}

// codexPatchPaths is the files a patch names, within Claude's caps: the first
// path of each directory, at most maxChanged.
func codexPatchPaths(patch, cwd string) []string {
	var paths []string

	dirs := map[string]bool{}
	size := 0

	for line := range strings.SplitSeq(patch, "\n") {
		line = strings.TrimRightFunc(line, unicode.IsSpace)

		path, ok := patchHeaderPath(line)
		if !ok {
			continue
		}

		if !filepath.IsAbs(path) && cwd != "" {
			path = filepath.Join(cwd, path)
		}

		path = absPath(path)
		if path == "" || len(path) > maxChangedPath || dirs[filepath.Dir(path)] {
			continue
		}

		encoded, _ := json.Marshal(path)
		if size += len(encoded) + 1; size > maxChangedBytes {
			break
		}

		dirs[filepath.Dir(path)] = true

		if paths = append(paths, path); len(paths) == maxChanged {
			break
		}
	}

	return paths
}

// patchHeaderPath reads a file header at column 0; content lines of an update
// start with a space, "-" or "+", so they never match.
func patchHeaderPath(line string) (string, bool) {
	for _, prefix := range []string{"*** Add File: ", "*** Delete File: ", "*** Update File: ", "*** Move to: "} {
		if path, ok := strings.CutPrefix(line, prefix); ok {
			return path, true
		}
	}

	return "", false
}
