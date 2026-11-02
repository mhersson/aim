package github

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeTemplate(t *testing.T, root, rel, body string) {
	t.Helper()

	path := filepath.Join(root, rel)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}

	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestTemplatesLocations(t *testing.T) {
	root := t.TempDir()
	writeTemplate(t, root, "docs/pull_request_template.md", "docs body")
	writeTemplate(t, root, ".github/PULL_REQUEST_TEMPLATE/b.md", "b body")
	writeTemplate(t, root, ".github/PULL_REQUEST_TEMPLATE.md", "github body")
	writeTemplate(t, root, ".github/PULL_REQUEST_TEMPLATE/a.md", "a body")
	writeTemplate(t, root, ".github/PULL_REQUEST_TEMPLATE/notes.txt", "ignored")
	writeTemplate(t, root, "pull_request_template.md", "root body")

	got, err := Templates(root)
	if err != nil {
		t.Fatal(err)
	}

	want := []Template{
		{Name: "pull_request_template.md", Path: "pull_request_template.md", Body: "root body"},
		{Name: "PULL_REQUEST_TEMPLATE.md", Path: ".github/PULL_REQUEST_TEMPLATE.md", Body: "github body"},
		{Name: "pull_request_template.md", Path: "docs/pull_request_template.md", Body: "docs body"},
		{Name: "a.md", Path: ".github/PULL_REQUEST_TEMPLATE/a.md", Body: "a body"},
		{Name: "b.md", Path: ".github/PULL_REQUEST_TEMPLATE/b.md", Body: "b body"},
	}

	if len(got) != len(want) {
		t.Fatalf("got %+v, want %+v", got, want)
	}

	for i := range want {
		if got[i] != want[i] {
			t.Errorf("template %d = %+v, want %+v", i, got[i], want[i])
		}
	}
}

func TestTemplatesMatchNamesCaseInsensitively(t *testing.T) {
	root := t.TempDir()
	writeTemplate(t, root, "Pull_Request_Template.MD", "root body")
	writeTemplate(t, root, ".GitHub/pull_request_template/x.md", "x body")

	got, err := Templates(root)
	if err != nil {
		t.Fatal(err)
	}

	if len(got) != 2 || got[0].Body != "root body" || got[1].Body != "x body" {
		t.Fatalf("got %+v", got)
	}
}

func TestTemplatesSymlinkOutsideIsSkipped(t *testing.T) {
	outside := t.TempDir()
	writeTemplate(t, outside, "secret.md", "secret")
	writeTemplate(t, outside, "dir/c.md", "c body")

	root := t.TempDir()
	writeTemplate(t, root, "docs/pull_request_template.md", "docs body")

	links := map[string]string{
		"pull_request_template.md":              filepath.Join(outside, "secret.md"),
		".github/PULL_REQUEST_TEMPLATE":         filepath.Join(outside, "dir"),
		"docs/PULL_REQUEST_TEMPLATE/escape.md":  filepath.Join(outside, "secret.md"),
		"docs/PULL_REQUEST_TEMPLATE/missing.md": filepath.Join(outside, "gone.md"),
	}
	for rel, target := range links {
		if err := os.MkdirAll(filepath.Dir(filepath.Join(root, rel)), 0o755); err != nil {
			t.Fatal(err)
		}

		if err := os.Symlink(target, filepath.Join(root, rel)); err != nil {
			t.Fatal(err)
		}
	}

	got, err := Templates(root)
	if err != nil {
		t.Fatal(err)
	}

	if len(got) != 1 || got[0].Body != "docs body" {
		t.Fatalf("got %+v, want only the docs template", got)
	}
}

func TestTemplatesSymlinkInsideIsFollowed(t *testing.T) {
	root := t.TempDir()
	writeTemplate(t, root, "real.md", "real body")

	if err := os.Symlink("real.md", filepath.Join(root, "pull_request_template.md")); err != nil {
		t.Fatal(err)
	}

	got, err := Templates(root)
	if err != nil {
		t.Fatal(err)
	}

	if len(got) != 1 || got[0].Body != "real body" {
		t.Fatalf("got %+v", got)
	}
}

func TestTemplatesTooLargeIsSkipped(t *testing.T) {
	root := t.TempDir()
	writeTemplate(t, root, "pull_request_template.md", strings.Repeat("x", 64<<10+1))
	writeTemplate(t, root, "docs/pull_request_template.md", strings.Repeat("y", 64<<10))

	got, err := Templates(root)
	if err != nil {
		t.Fatal(err)
	}

	if len(got) != 1 || got[0].Path != "docs/pull_request_template.md" || len(got[0].Body) != 64<<10 {
		t.Fatalf("got %+v templates, want only the 64 KiB docs one", len(got))
	}
}

func TestTemplatesNone(t *testing.T) {
	root := t.TempDir()
	writeTemplate(t, root, "README.md", "readme")

	got, err := Templates(root)
	if err != nil {
		t.Fatal(err)
	}

	if len(got) != 0 {
		t.Fatalf("got %+v, want none", got)
	}
}

func TestTemplatesKeepsReadableWhenOneIsNot(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root reads any file")
	}

	root := t.TempDir()
	writeTemplate(t, root, "pull_request_template.md", "root body")
	writeTemplate(t, root, "docs/pull_request_template.md", "locked")

	locked := filepath.Join(root, "docs", "pull_request_template.md")
	if err := os.Chmod(locked, 0); err != nil {
		t.Fatal(err)
	}

	got, err := Templates(root)
	if err == nil {
		t.Fatal("an unreadable template gave no error")
	}

	if len(got) != 1 || got[0].Body != "root body" {
		t.Fatalf("got %+v, want only the root template", got)
	}
}

func TestFill(t *testing.T) {
	one := []Commit{{Subject: "add rail", Body: "why it exists"}}
	three := []Commit{{Subject: "a", Body: "ignored"}, {Subject: "b"}, {Subject: "c"}}

	tests := []struct {
		name     string
		branch   string
		commits  []Commit
		template string
		want     Draft
	}{
		{"one commit", "feat-x", one, "", Draft{"add rail", "why it exists"}},
		{"several commits", "feat-agents_rail", three, "", Draft{"Feat agents rail", "- a\n- b\n- c\n"}},
		{"no commits", "fix-bug", nil, "", Draft{"Fix bug", ""}},
		{"unicode first letter", "étude-un", nil, "", Draft{"Étude un", ""}},
		{"empty branch", "", nil, "", Draft{}},
		{"template beside one commit", "feat-x", one, "## Summary\n", Draft{"add rail", "## Summary\n"}},
		{"template beside several commits", "feat-x", three, "tpl", Draft{"Feat x", "tpl"}},
		{"blank template is none", "feat-x", one, " \n\t\n", Draft{"add rail", "why it exists"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := Fill(tt.branch, tt.commits, tt.template); got != tt.want {
				t.Errorf("Fill = %+v, want %+v", got, tt.want)
			}
		})
	}
}
