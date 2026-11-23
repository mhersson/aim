package harness

import (
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"slices"
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
		ev.Resume = h.Source == "resume"

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

	return fitCodexEvent(ev)
}

// The route added after parsing needs at most 20 bytes for p<decimal int>,
// 32 for the owner_pid field and one for the line terminator.
const codexRouteReserve = 53

func fitCodexEvent(ev Event) (Event, error) {
	// A raw string longer than the line cannot fit even before JSON escaping.
	if len(ev.Session)+len(ev.TurnID) > maxLine {
		return Event{}, errors.New("harness: codex identity too long")
	}

	if len(ev.Transcript) > maxLine {
		ev.Transcript = ""
	}

	if len(ev.Cwd) > maxLine {
		ev.Cwd = ""
	}

	fits := func() bool {
		encoded, err := json.Marshal(ev)

		return err == nil && len(encoded)+codexRouteReserve <= maxLine
	}
	if fits() {
		return ev, nil
	}

	paths := &ev.Changed
	if ev.Before {
		paths = &ev.BeforePaths
	}

	allPaths := *paths
	*paths = nil
	incomplete := ev.PathsIncomplete
	ev.PathsIncomplete = incomplete || len(allPaths) > 0

	// Drop optional metadata only when the envelope alone exceeds the limit.
	if !fits() {
		ev.Transcript = ""
	}

	if !fits() {
		ev.Cwd = ""
	}

	if !fits() {
		return Event{}, errors.New("harness: codex event identity exceeds socket limit")
	}

	*paths = allPaths

	ev.PathsIncomplete = incomplete
	for !fits() {
		ev.PathsIncomplete = true
		*paths = (*paths)[:len(*paths)-1]
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

	paths, incomplete := codexPatchPaths(jsonString(objectFields(h.Input)["command"]), ev.Cwd)

	if len(paths) > 0 {
		ev.Action = clip(h.Tool + " " + relativeTo(ev.Cwd, paths[0]))
	}

	ev.PathsIncomplete = incomplete
	if h.Name == "PreToolUse" {
		ev.BeforePaths, ev.Before = paths, len(paths) > 0 || incomplete
	} else {
		ev.Changed = paths
	}
}

func codexPatchPaths(patch, cwd string) (paths []string, incomplete bool) {
	size := 0
	updating, canMove := false, false

	lines := strings.SplitSeq(strings.TrimSpace(patch), "\n")
	for line := range lines {
		// In an update a leading space marks file content, including header-like text.
		if updating {
			line = strings.TrimRightFunc(line, unicode.IsSpace)
		} else {
			line = strings.TrimSpace(line)
		}

		if strings.HasPrefix(line, "*** Environment ID:") {
			// A named execution environment need not share this pane's filesystem.
			return nil, true
		}

		header, path, _ := strings.Cut(line, ": ")
		move := canMove
		canMove = false

		switch header {
		case "*** Add File", "*** Delete File":
			updating = false
		case "*** Update File":
			updating, canMove = true, true
		case "*** Move to":
			if !move {
				continue
			}
		default:
			continue
		}

		if path == "" {
			incomplete = true

			continue
		}

		if !filepath.IsAbs(path) && cwd != "" {
			path = filepath.Join(cwd, path)
		}

		path = absPath(path)
		if path == "" || len(path) > maxChangedPath {
			incomplete = true

			continue
		}

		if slices.Contains(paths, path) {
			continue
		}

		encoded, _ := json.Marshal(path)

		size += len(encoded) + 1
		if len(paths) == maxChanged || size > maxChangedBytes {
			return paths, true
		}

		paths = append(paths, path)
	}

	return paths, incomplete
}
