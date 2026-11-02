package ui

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"testing"
)

// TestMain isolates git from the machine's config once for the binary, so tests
// that build repositories need no t.Setenv and can call t.Parallel. Its global
// config turns auto-maintenance off for every git the tests start; it is a file
// because the app's git calls and git's local transport drop GIT_CONFIG_COUNT.
func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "ui-test-")
	if err != nil {
		panic(err)
	}

	cfg := filepath.Join(dir, "gitconfig")
	if err := os.WriteFile(cfg, []byte("[maintenance]\n\tauto = false\n"), 0o600); err != nil {
		panic(err)
	}

	env := map[string]string{
		"GIT_CONFIG_NOSYSTEM": "1",
		"GIT_CONFIG_GLOBAL":   cfg,
		"GIT_AUTHOR_NAME":     "Test",
		"GIT_AUTHOR_EMAIL":    "test@example.com",
		"GIT_COMMITTER_NAME":  "Test",
		"GIT_COMMITTER_EMAIL": "test@example.com",
	}
	for k, v := range env {
		if err := os.Setenv(k, v); err != nil {
			panic(err)
		}
	}

	m.Run()

	_ = os.RemoveAll(dir)
}

// No git the test helpers run may start auto-maintenance: its detached run
// outlives the git that started it and races a copy or removal of the
// repository.
func TestGitHelpersStartNoAutoMaintenance(t *testing.T) {
	tests := []struct {
		name string
		git  func(t *testing.T)
		want string
	}{
		{"gitIn", func(t *testing.T) {
			dir := newRepoDir(t)
			write(t, filepath.Join(dir, "a.txt"), "one\n")
			commitEverything(t, dir, "init")
		}, "commit"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			trace := filepath.Join(t.TempDir(), "trace")
			t.Setenv("GIT_TRACE2_EVENT", trace)

			tt.git(t)

			names := gitCommands(t, trace)
			if !slices.Contains(names, tt.want) || slices.Contains(names, "maintenance") {
				t.Errorf("ran %v, want %s and no maintenance", names, tt.want)
			}
		})
	}
}

// gitCommands returns the name of every git command the trace2 event file at
// path records, children included.
func gitCommands(t *testing.T, path string) []string {
	t.Helper()

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}

	var names []string

	for line := range bytes.Lines(data) {
		var ev struct {
			Event string `json:"event"`
			Name  string `json:"name"`
		}

		if err := json.Unmarshal(line, &ev); err != nil {
			t.Fatalf("trace line %q: %v", line, err)
		}

		if ev.Event == "cmd_name" {
			names = append(names, ev.Name)
		}
	}

	return names
}
