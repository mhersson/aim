package git

import (
	"os"
	"path/filepath"
	"testing"
)

func TestDetectTools(t *testing.T) {
	t.Parallel()

	const (
		vscodeNoKey  = `{"editor.tabSize": 2}`
		vscodeOther  = `{"files.watcherExclude": {"**/node_modules/**": true}}`
		vscodeCovers = `{"files.watcherExclude": {"**/.worktrees/**": true}}`
	)

	tests := []struct {
		name  string
		files map[string]string // a trailing "/" makes a directory
		want  string            // "" = nothing detected, else Name:Path
	}{
		{name: "nothing", files: map[string]string{"main.go": "x"}},

		{name: "jetbrains yes", files: map[string]string{".idea/": ""}, want: "JetBrains:.idea"},
		{name: "jetbrains file is not a dir", files: map[string]string{".idea": "x"}},

		{name: "vscode no key", files: map[string]string{".vscode/settings.json": vscodeNoKey}, want: "VS Code:.vscode/settings.json"},
		{name: "vscode other keys", files: map[string]string{".vscode/settings.json": vscodeOther}, want: "VS Code:.vscode/settings.json"},
		{name: "vscode unparsable", files: map[string]string{".vscode/settings.json": "{// c\n"}, want: "VS Code:.vscode/settings.json"},
		{name: "vscode covered", files: map[string]string{".vscode/settings.json": vscodeCovers}},

		{name: "jest config", files: map[string]string{"jest.config.ts": ""}, want: "Jest:jest.config.ts"},
		{name: "vitest config", files: map[string]string{"vitest.config.mjs": ""}, want: "Vitest:vitest.config.mjs"},
		{name: "jest key", files: map[string]string{"package.json": `{"jest": {}}`}, want: "Jest:package.json"},
		{name: "package.json unparsable", files: map[string]string{"package.json": "{"}},
		{name: "package.json without jest", files: map[string]string{"package.json": `{"name": "x"}`}},

		{name: "pytest.ini", files: map[string]string{"pytest.ini": ""}, want: "pytest:pytest.ini"},
		{name: "pyproject pytest", files: map[string]string{"pyproject.toml": "[project]\n[tool.pytest.ini_options]\nx=1\n"}, want: "pytest:pyproject.toml"},
		{name: "pyproject without pytest", files: map[string]string{"pyproject.toml": "[project]\nname='x'\n"}},
		{name: "setup.cfg pytest", files: map[string]string{"setup.cfg": "[tool:pytest]\n"}, want: "pytest:setup.cfg"},
		{name: "tox pytest", files: map[string]string{"tox.ini": "[tox]\n[pytest]\n"}, want: "pytest:tox.ini"},

		{name: "tsconfig no include", files: map[string]string{"tsconfig.json": `{"compilerOptions": {}}`}, want: "TypeScript:tsconfig.json"},
		{name: "tsconfig glob include", files: map[string]string{"tsconfig.json": `{"include": ["**/*"]}`}, want: "TypeScript:tsconfig.json"},
		{name: "tsconfig unparsable", files: map[string]string{"tsconfig.json": "{/* c */}"}, want: "TypeScript:tsconfig.json"},
		{name: "tsconfig src include", files: map[string]string{"tsconfig.json": `{"include": ["src"]}`}},

		{name: "eslintrc", files: map[string]string{".eslintrc.json": "{}"}, want: "ESLint:.eslintrc.json"},
		{name: "eslint flat config", files: map[string]string{"eslint.config.js": ""}, want: "ESLint:eslint.config.js"},

		{name: "docker no ignore", files: map[string]string{"Dockerfile": "FROM x"}, want: "Docker:Dockerfile"},
		{name: "docker ignore without", files: map[string]string{"Dockerfile": "FROM x", ".dockerignore": "node_modules\n"}, want: "Docker:.dockerignore"},
		{name: "docker ignore covers", files: map[string]string{"Dockerfile": "FROM x", ".dockerignore": "node_modules\n.worktrees\n"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			root := t.TempDir()

			for rel, content := range tt.files {
				if rel[len(rel)-1] == '/' {
					if err := os.MkdirAll(filepath.Join(root, rel), 0o755); err != nil {
						t.Fatal(err)
					}

					continue
				}

				writeFile(t, root, rel, content)
			}

			got, err := DetectTools(root)
			if err != nil {
				t.Fatal(err)
			}

			if tt.want == "" {
				if len(got) != 0 {
					t.Fatalf("tools = %+v, want none", got)
				}

				return
			}

			if len(got) != 1 || got[0].Name+":"+got[0].Path != tt.want || got[0].Hint == "" {
				t.Fatalf("tools = %+v, want %s", got, tt.want)
			}
		})
	}
}

func TestDetectToolsOrder(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	writeFile(t, root, "Dockerfile", "FROM x")
	writeFile(t, root, ".eslintrc", "{}")
	writeFile(t, root, "pytest.ini", "")

	got, err := DetectTools(root)
	if err != nil {
		t.Fatal(err)
	}

	var names []string
	for _, tool := range got {
		names = append(names, tool.Name)
	}

	if want := "pytest ESLint Docker"; len(names) != 3 || names[0]+" "+names[1]+" "+names[2] != want {
		t.Errorf("names = %v, want %s", names, want)
	}
}
