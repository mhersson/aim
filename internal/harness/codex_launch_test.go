package harness

import (
	"slices"
	"testing"

	"github.com/mhersson/aim/internal/core"
	"github.com/pelletier/go-toml/v2"
)

func TestCodexLaunch(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		settings string
		resume   core.SessionID
		want     []string
	}{
		{"", "saved-id", []string{"codex", "--no-daemon", "resume", "saved-id"}},
		{"{}", "", []string{"codex", "--no-daemon", "-c", "hooks={}"}},
		{"{}", "saved-id", []string{"codex", "--no-daemon", "-c", "hooks={}", "resume", "saved-id"}},
	} {
		if got := (Codex{}).Launch(tc.settings, tc.resume); !slices.Equal(got, tc.want) {
			t.Fatalf("argv = %q, want %q", got, tc.want)
		}
	}
}

func TestCodexHookTable(t *testing.T) {
	t.Parallel()

	for _, note := range []bool{false, true} {
		var config struct {
			Hooks map[string][]struct {
				Hooks []struct{ Type, Command string }
			}
		}
		if err := toml.Unmarshal([]byte("hooks="+(Codex{}).HookTable(note)), &config); err != nil {
			t.Fatal(err)
		}

		events := []string{"SessionStart", "UserPromptSubmit", "PreToolUse", "PermissionRequest", "PostToolUse", "Stop", "Interrupt"}
		for _, event := range events {
			rows := config.Hooks[event]
			if len(rows) != 1 || len(rows[0].Hooks) != 1 {
				t.Fatalf("%s handlers = %+v", event, rows)
			}

			want := `"$AIM_BIN" hook codex`
			if note && event == "SessionStart" {
				want += " --note"
			}

			handler := rows[0].Hooks[0]
			if handler.Type != "command" || handler.Command != want {
				t.Fatalf("%s handler = %+v, want %s", event, handler, want)
			}
		}
	}
}
