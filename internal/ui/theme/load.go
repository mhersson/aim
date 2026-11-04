package theme

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/mhersson/aim/internal/core"
)

// themeFile is a theme file's keys; the pointers tell a missing key from a zero value.
type themeFile struct {
	Title   *string `toml:"title"`
	Dark    *bool   `toml:"dark"`
	Palette Palette `toml:"palette"`
}

// Load returns the built-in themes followed by the user's, one per
// <name>.toml file in dir, by name. A missing dir holds none. A file that
// does not load is an error naming it, as is a name that is not lowercase
// letters, digits and hyphens or that a built-in has.
func Load(dir string) (Set, error) {
	set := Builtin()

	entries, err := os.ReadDir(dir)
	if errors.Is(err, fs.ErrNotExist) {
		return set, nil
	}

	if err != nil {
		return nil, fmt.Errorf("themes: %w", err)
	}

	var user Set

	for _, e := range entries {
		name, ok := strings.CutSuffix(e.Name(), ".toml")
		if !ok || e.IsDir() {
			continue
		}

		th, err := loadFile(filepath.Join(dir, e.Name()), name, set)
		if err != nil {
			return nil, err
		}

		user = append(user, th)
	}

	slices.SortFunc(user, func(a, b Theme) int { return strings.Compare(a.Name, b.Name) })

	return append(set, user...), nil
}

// loadFile reads the theme called name from the file at path.
func loadFile(path, name string, builtin Set) (Theme, error) {
	if !validName(name) {
		return Theme{}, fmt.Errorf("theme %s: name %q: want lowercase letters, digits and hyphens", path, name)
	}

	if _, err := builtin.ByName(name); err == nil {
		return Theme{}, fmt.Errorf("theme %s: name %q is a built-in theme", path, name)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		return Theme{}, fmt.Errorf("theme: %w", err)
	}

	var f themeFile
	if err := core.DecodeStrict(data, &f); err != nil {
		return Theme{}, fmt.Errorf("theme %s: %w", path, err)
	}

	var missing []string

	if f.Title == nil {
		missing = append(missing, "title")
	}

	if f.Dark == nil {
		missing = append(missing, "dark")
	}

	for _, s := range f.Palette.slots() {
		if *s.hex == "" {
			missing = append(missing, "palette."+s.name)
		}
	}

	if len(missing) > 0 {
		return Theme{}, fmt.Errorf("theme %s: missing key %s", path, strings.Join(missing, ", "))
	}

	for _, s := range f.Palette.slots() {
		if !s.hex.valid() {
			return Theme{}, fmt.Errorf("theme %s: palette.%s: %q is not a #rrggbb colour", path, s.name, *s.hex)
		}
	}

	return newTheme(name, *f.Title, *f.Dark, f.Palette), nil
}

// validName reports whether s is a theme name: lowercase letters, digits and
// hyphens, the form theme.set.<name> commands and the config's theme take.
func validName(s string) bool {
	if s == "" {
		return false
	}

	for _, c := range s {
		if (c < 'a' || c > 'z') && (c < '0' || c > '9') && c != '-' {
			return false
		}
	}

	return true
}
