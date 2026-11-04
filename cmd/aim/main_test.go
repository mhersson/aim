package main

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mhersson/aim/internal/core"
	"github.com/mhersson/aim/internal/ui/theme"
)

func TestRootRejectsBadConfig(t *testing.T) {
	tests := []struct {
		name    string
		content string
		missing bool
	}{
		{name: "unknown key", content: "them = 1\n"},
		{name: "layout ratio", content: "[layout]\nmaster_ratio = 95\n"},
		{name: "diff limit", content: "[diff]\nmax_files = 0\n"},
		{name: "worktree root", content: "[worktrees]\nroot = \"beside\"\n"},
		{name: "review days", content: "[pull_requests]\nreview_requested_days = -1\n"},
		{name: "unknown theme", content: "theme = \"nope\"\n"},
		{name: "explicit path missing", missing: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("HOME", shortDir(t))
			t.Setenv("XDG_CONFIG_HOME", t.TempDir())
			t.Setenv("XDG_CACHE_HOME", t.TempDir())
			stateHome := shortDir(t)
			t.Setenv("XDG_STATE_HOME", stateHome)

			path := filepath.Join(t.TempDir(), "config.toml")
			if !tt.missing {
				if err := os.WriteFile(path, []byte(tt.content), 0o600); err != nil {
					t.Fatal(err)
				}
			}

			cmd := newRootCmd()
			cmd.SetArgs([]string{"--config", path})
			cmd.SetOut(io.Discard)
			cmd.SetErr(io.Discard)
			// Cancelled up front, so a run that wrongly gets as far as the TUI
			// returns at once instead of taking over the test's terminal.
			ctx, cancel := context.WithCancel(t.Context())
			cancel()

			err := cmd.ExecuteContext(ctx)

			if err == nil || !strings.Contains(err.Error(), path) {
				t.Fatalf("Execute() error = %v, want one naming %s", err, path)
			}
			// The log is opened right before the TUI starts, so an empty state
			// dir means the TUI never started.
			if entries, _ := os.ReadDir(stateHome); len(entries) != 0 {
				t.Errorf("state dir has %d entries, want none", len(entries))
			}
		})
	}
}

func TestValidateConfigTakesThemes(t *testing.T) {
	t.Parallel()

	dawn := theme.Builtin()[0]
	dawn.Name = "test-dawn"

	cfg := core.DefaultConfig()
	cfg.Theme = dawn.Name
	cfg.Keys = core.KeysConfig{Leader: map[string]string{"x": "theme.set." + dawn.Name}, Order: map[string][]string{"leader": {"x"}}}

	if err := validateConfig(cfg, append(theme.Builtin(), dawn)); err != nil {
		t.Errorf("validateConfig() with %s loaded = %v, want nil", dawn.Name, err)
	}

	if err := validateConfig(cfg, theme.Builtin()); !errors.Is(err, theme.ErrUnknownTheme) {
		t.Errorf("validateConfig() with the built-ins = %v, want ErrUnknownTheme", err)
	}
}

func TestRecordFlagIsHidden(t *testing.T) {
	cmd := newRootCmd()

	f := cmd.Flags().Lookup("record")
	if f == nil || !f.Hidden {
		t.Fatalf("record flag = %+v, want a hidden flag", f)
	}

	var help strings.Builder

	cmd.SetOut(&help)
	cmd.SetErr(&help)
	cmd.SetArgs([]string{"--help"})

	if err := cmd.ExecuteContext(context.Background()); err != nil {
		t.Fatal(err)
	}

	if strings.Contains(help.String(), "--record") {
		t.Fatalf("help lists the hidden flag:\n%s", help.String())
	}
}
