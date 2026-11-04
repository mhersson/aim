package theme

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// latteFile is a valid theme file: Catppuccin Latte's palette under title, with
// the slot lines edit changes (a line is dropped when edit maps it to "").
func latteFile(title string, edit map[string]string) string {
	var b strings.Builder

	b.WriteString("title = \"" + title + "\"\ndark = false\n\n[palette]\n")

	p := lattePalette()
	for _, s := range p.slots() {
		line := s.name + " = \"" + string(*s.hex) + "\""
		if v, ok := edit[s.name]; ok {
			line = v
		}

		if line != "" {
			b.WriteString(line + "\n")
		}
	}

	return b.String()
}

func TestLoad(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		files   map[string]string // file name -> content
		wantErr []string          // the values the error must carry; nil for none
	}{
		{
			name: "good files",
			files: map[string]string{
				"zeta.toml":       latteFile("Zeta", nil),
				"alpha-dawn.toml": latteFile("Alpha Dawn", map[string]string{"mauve": `mauve = "#ABCDEF"`}),
				"notes.md":        "not a theme",
			},
		},
		{
			name:    "missing slot",
			files:   map[string]string{"dawn.toml": latteFile("Dawn", map[string]string{"red": ""})},
			wantErr: []string{"dawn.toml", "palette.red"},
		},
		{
			name:    "missing title and dark",
			files:   map[string]string{"dawn.toml": strings.Replace(latteFile("Dawn", nil), "title = \"Dawn\"\ndark = false\n", "", 1)},
			wantErr: []string{"dawn.toml", "title", "dark"},
		},
		{
			name:    "bad colour",
			files:   map[string]string{"dawn.toml": latteFile("Dawn", map[string]string{"red": `red = "#d20f3"`})},
			wantErr: []string{"dawn.toml", "palette.red", `"#d20f3"`},
		},
		{
			name:    "unknown key",
			files:   map[string]string{"dawn.toml": latteFile("Dawn", map[string]string{"red": `redd = "#d20f39"`})},
			wantErr: []string{"dawn.toml", "palette.redd"},
		},
		{
			name:    "bad name",
			files:   map[string]string{"Dawn.toml": latteFile("Dawn", nil)},
			wantErr: []string{"Dawn.toml", `"Dawn"`},
		},
		{
			name:    "built-in name",
			files:   map[string]string{"catppuccin-mocha.toml": latteFile("Mine", nil)},
			wantErr: []string{"catppuccin-mocha.toml", `"catppuccin-mocha"`},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			dir := t.TempDir()
			for name, content := range tt.files {
				if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o600); err != nil {
					t.Fatal(err)
				}
			}

			set, err := Load(dir)
			if tt.wantErr != nil {
				if err == nil {
					t.Fatalf("Load() = %q, want an error", set.Names())
				}

				// The temporary dir holds the test's name, so it is dropped before matching.
				msg := strings.ReplaceAll(err.Error(), dir, "")
				for _, want := range tt.wantErr {
					if !strings.Contains(msg, want) {
						t.Errorf("Load() error = %q, want it to carry %s", err, want)
					}
				}

				return
			}

			if err != nil {
				t.Fatal(err)
			}

			want := append(Builtin().Names(), "alpha-dawn", "zeta")
			if got := set.Names(); !slices.Equal(got, want) {
				t.Fatalf("Load() names = %q, want %q", got, want)
			}

			// Every slot reaches its own field: one that missed would leave Latte's colour out.
			wantPal := lattePalette()
			wantPal.Mauve = "#ABCDEF"

			dawn := set[len(Builtin())]
			if dawn.Title != "Alpha Dawn" || dawn.Dark || dawn.Palette != wantPal || dawn.UI != uiRoles(wantPal) {
				t.Errorf("alpha-dawn = %+v, want Alpha Dawn, light, Latte's palette with mauve #ABCDEF and its UI roles", dawn)
			}
		})
	}
}

func TestLoadWithoutFolder(t *testing.T) {
	t.Parallel()

	set, err := Load(filepath.Join(t.TempDir(), "themes"))
	if err != nil {
		t.Fatal(err)
	}

	if got, want := set.Names(), Builtin().Names(); !slices.Equal(got, want) {
		t.Errorf("Load() names = %q, want the built-ins %q", got, want)
	}
}

func TestExampleThemes(t *testing.T) {
	t.Parallel()

	set, err := Load(filepath.Join("..", "..", "..", "themes"))
	if err != nil {
		t.Fatal(err)
	}

	if len(set) == len(Builtin()) {
		t.Fatal("the repository's themes folder holds no example themes")
	}
}
