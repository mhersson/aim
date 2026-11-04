package store

import (
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"testing"
)

func TestSaveTheme(t *testing.T) {
	t.Parallel()

	const tables = `# my config
icons = false

[layout]
# BSP everywhere
default_mode = "bsp"
`

	tests := []struct {
		name     string
		file     string // the config's content; "" for no file
		link     bool   // the config is a symlink to a file in another directory
		mode     fs.FileMode
		want     string
		wantErr  bool
		wantMode fs.FileMode
	}{
		{
			name:     "no file",
			want:     "theme = \"catppuccin-latte\"\n",
			wantMode: 0o600,
		},
		{
			name:     "theme line",
			file:     "# my config\ntheme = 'catppuccin-mocha' # dark\nicons = false\n\n[keys]\n",
			mode:     0o640,
			want:     "# my config\ntheme = \"catppuccin-latte\" # dark\nicons = false\n\n[keys]\n",
			wantMode: 0o640,
		},
		{
			name:     "no theme line",
			file:     tables,
			mode:     0o644,
			want:     "theme = \"catppuccin-latte\"\n" + tables,
			wantMode: 0o644,
		},
		{
			name:     "commented theme line",
			file:     "## The color theme at startup.\n# theme = \"catppuccin-mocha\"\n\n[layout]\n",
			mode:     0o600,
			want:     "theme = \"catppuccin-latte\"\n## The color theme at startup.\n# theme = \"catppuccin-mocha\"\n\n[layout]\n",
			wantMode: 0o600,
		},
		{
			name:     "symlinked file",
			file:     "theme = \"catppuccin-mocha\"\n",
			link:     true,
			mode:     0o644,
			want:     "theme = \"catppuccin-latte\"\n",
			wantMode: 0o644,
		},
		{
			name:     "not TOML",
			file:     "theme = \n[keys\n",
			mode:     0o644,
			want:     "theme = \n[keys\n",
			wantErr:  true,
			wantMode: 0o644,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			root := t.TempDir()
			dir := filepath.Join(root, "aim")
			path := filepath.Join(dir, "config.toml")
			target := path

			if tt.file != "" {
				if tt.link {
					target = filepath.Join(root, "dotfiles", "aim.toml")
				}

				for _, d := range []string{dir, filepath.Dir(target)} {
					if err := os.MkdirAll(d, 0o700); err != nil {
						t.Fatal(err)
					}
				}

				if err := os.WriteFile(target, []byte(tt.file), tt.mode); err != nil {
					t.Fatal(err)
				}

				if err := os.Chmod(target, tt.mode); err != nil {
					t.Fatal(err)
				}

				if tt.link {
					if err := os.Symlink(target, path); err != nil {
						t.Fatal(err)
					}
				}
			}

			err := SaveTheme(path, "catppuccin-latte")
			if (err != nil) != tt.wantErr {
				t.Fatalf("SaveTheme() error = %v, want error %v", err, tt.wantErr)
			}

			got, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}

			if string(got) != tt.want {
				t.Errorf("config =\n%q\nwant\n%q", got, tt.want)
			}

			info, err := os.Stat(target)
			if err != nil {
				t.Fatal(err)
			}

			if info.Mode().Perm() != tt.wantMode {
				t.Errorf("mode = %v, want %v", info.Mode().Perm(), tt.wantMode)
			}

			if tt.link {
				if l, err := os.Lstat(path); err != nil || l.Mode()&fs.ModeSymlink == 0 {
					t.Errorf("config is no longer a symlink (err %v)", err)
				}
			}

			if d, err := os.Stat(dir); err != nil || d.Mode().Perm() != 0o700 {
				t.Errorf("config dir = %v (err %v), want mode 0700", d, err)
			}

			// A temporary file left beside the config is a broken atomic write.
			entries, err := os.ReadDir(filepath.Dir(target))
			if err != nil {
				t.Fatal(err)
			}

			names := make([]string, len(entries))
			for i, e := range entries {
				names[i] = e.Name()
			}

			if want := []string{filepath.Base(target)}; !slices.Equal(names, want) {
				t.Errorf("config's dir holds %v, want %v", names, want)
			}
		})
	}
}
