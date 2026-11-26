package harness

import (
	"strings"

	"github.com/mhersson/aim/internal/core"
)

// Codex adapts the interactive Codex CLI.
type Codex struct{}

func (Codex) Launch(settings string, resume core.SessionID) []string {
	argv := []string{"codex", "--no-daemon"}
	if settings != "" {
		argv = append(argv, "-c", "hooks="+settings)
	}

	if resume != "" {
		argv = append(argv, "resume", string(resume))
	}

	return argv
}

// HookTable is an inline TOML table for the CLI's launch-scoped configuration.
func (Codex) HookTable(note bool) string {
	var rows []string

	for _, event := range []string{"SessionStart", "UserPromptSubmit", "PreToolUse", "PermissionRequest", "PostToolUse", "Stop", "Interrupt"} {
		cmd := `"$AIM_BIN" hook codex`
		if event == "SessionStart" && note {
			cmd += " --note"
		}

		rows = append(rows, event+`=[{hooks=[{type="command",command='`+cmd+`'}]}]`)
	}

	return "{" + strings.Join(rows, ",") + "}"
}
