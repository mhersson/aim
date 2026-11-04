package theme

import (
	"reflect"
	"regexp"
	"testing"
)

// paletteFields returns every field of p keyed by field name.
func paletteFields(p Palette) map[string]Hex {
	v := reflect.ValueOf(p)

	fields := make(map[string]Hex, v.NumField())
	for i := range v.NumField() {
		fields[v.Type().Field(i).Name] = Hex(v.Field(i).String())
	}

	return fields
}

func TestPaletteHex(t *testing.T) {
	hexRE := regexp.MustCompile(`^#[0-9a-f]{6}$`)

	for _, th := range Builtin() {
		fields := paletteFields(th.Palette)
		for name, h := range fields {
			if !hexRE.MatchString(string(h)) {
				t.Errorf("%s: Palette.%s = %q, want #rrggbb", th.Name, name, h)
			}
		}
	}

	tests := []struct {
		hex        Hex
		r, g, b, a uint32
	}{
		{"#cba6f7", 0xcbcb, 0xa6a6, 0xf7f7, 0xffff},
		{"#CBA6F7", 0xcbcb, 0xa6a6, 0xf7f7, 0xffff},
		{"", 0, 0, 0, 0xffff},
		{"#cba6f", 0, 0, 0, 0xffff},
		{"#cba6fg", 0, 0, 0, 0xffff},
	}
	for _, tt := range tests {
		r, g, b, a := tt.hex.RGBA()
		if r != tt.r || g != tt.g || b != tt.b || a != tt.a {
			t.Errorf("Hex(%q).RGBA() = (%#x, %#x, %#x, %#x), want (%#x, %#x, %#x, %#x)",
				tt.hex, r, g, b, a, tt.r, tt.g, tt.b, tt.a)
		}
	}
}
