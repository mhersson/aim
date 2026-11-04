package theme

import (
	"errors"
	"reflect"
	"slices"
	"testing"
)

func TestBuiltinAndByName(t *testing.T) {
	names := []string{"catppuccin-latte", "catppuccin-frappe", "catppuccin-macchiato", "catppuccin-mocha"}
	if got := Builtin().Names(); !slices.Equal(got, names) {
		t.Errorf("Builtin().Names() = %q, want %q", got, names)
	}

	user := Builtin()[0]
	user.Name = "test-dawn"
	set := append(Builtin(), user)

	for _, name := range append(names, "test-dawn") {
		th, err := set.ByName(name)
		if err != nil {
			t.Errorf("ByName(%q): %v", name, err)

			continue
		}

		if th.Name != name {
			t.Errorf("ByName(%q).Name = %q", name, th.Name)
		}
	}

	for _, name := range []string{"nope", ""} {
		if _, err := set.ByName(name); !errors.Is(err, ErrUnknownTheme) {
			t.Errorf("ByName(%q) error = %v, want ErrUnknownTheme", name, err)
		}
	}

	if _, err := Builtin().ByName("test-dawn"); !errors.Is(err, ErrUnknownTheme) {
		t.Errorf("Builtin().ByName(test-dawn) error = %v, want ErrUnknownTheme", err)
	}
}

func TestUIRoles(t *testing.T) {
	for _, th := range Builtin() {
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
