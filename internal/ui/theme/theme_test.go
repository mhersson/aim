package theme

import (
	"errors"
	"reflect"
	"slices"
	"testing"
)

func TestAllAndByName(t *testing.T) {
	names := []string{"catppuccin-latte", "catppuccin-frappe", "catppuccin-macchiato", "catppuccin-mocha"}
	if got := Names(); !slices.Equal(got, names) {
		t.Errorf("Names() = %q, want %q", got, names)
	}

	var allNames []string
	for _, th := range All() {
		allNames = append(allNames, th.Name)
	}

	if !slices.Equal(allNames, names) {
		t.Errorf("All() names = %q, want %q", allNames, names)
	}

	for _, name := range names {
		th, err := ByName(name)
		if err != nil {
			t.Errorf("ByName(%q): %v", name, err)

			continue
		}

		if th.Name != name {
			t.Errorf("ByName(%q).Name = %q", name, th.Name)
		}
	}

	for _, name := range []string{"nope", ""} {
		if _, err := ByName(name); !errors.Is(err, ErrUnknownTheme) {
			t.Errorf("ByName(%q) error = %v, want ErrUnknownTheme", name, err)
		}
	}
}

func TestUIRoles(t *testing.T) {
	for _, th := range All() {
		inPalette := map[Hex]bool{}
		for _, h := range paletteFields(th.Palette) {
			inPalette[h] = true
		}

		v := reflect.ValueOf(th.UI)
		for i := range v.NumField() {
			role, h := v.Type().Field(i).Name, Hex(v.Field(i).String())
			if h == "" {
				t.Errorf("%s: UI.%s is empty", th.Name, role)
			} else if !inPalette[h] {
				t.Errorf("%s: UI.%s = %q is not a palette colour", th.Name, role, h)
			}
		}
	}
}
