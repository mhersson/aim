package theme

import "image/color"

// Hex is a colour written as "#rrggbb".
type Hex string

var _ color.Color = Hex("")

// RGBA implements color.Color. A value that is not "#rrggbb" is opaque black.
func (h Hex) RGBA() (r, g, b, a uint32) {
	rgb, ok := h.rgb()
	if !ok {
		return 0, 0, 0, 0xffff
	}

	return rgb[0], rgb[1], rgb[2], 0xffff
}

// valid reports whether h is "#rrggbb", in either case.
func (h Hex) valid() bool {
	_, ok := h.rgb()

	return ok
}

func (h Hex) rgb() ([3]uint32, bool) {
	var rgb [3]uint32
	if len(h) != 7 || h[0] != '#' {
		return rgb, false
	}

	for i := range rgb {
		hi, okHi := hexDigit(h[1+2*i])

		lo, okLo := hexDigit(h[2+2*i])
		if !okHi || !okLo {
			return rgb, false
		}

		v := hi<<4 | lo
		rgb[i] = v<<8 | v
	}

	return rgb, true
}

func hexDigit(c byte) (uint32, bool) {
	switch {
	case '0' <= c && c <= '9':
		return uint32(c - '0'), true
	case 'a' <= c && c <= 'f':
		return uint32(c-'a') + 10, true
	case 'A' <= c && c <= 'F':
		return uint32(c-'A') + 10, true
	}

	return 0, false
}

// Palette holds a theme's 26 colours, in Catppuccin's slots. The flavor
// values are the official Catppuccin palette (MIT, see THIRD_PARTY.md).
type Palette struct {
	Rosewater, Flamingo, Pink, Mauve, Red, Maroon, Peach, Yellow, Green, Teal,
	Sky, Sapphire, Blue, Lavender, Text, Subtext1, Subtext0, Overlay2, Overlay1,
	Overlay0, Surface2, Surface1, Surface0, Base, Mantle, Crust Hex
}

// slot is one of a palette's colours under its theme-file key.
type slot struct {
	name string
	hex  *Hex
}

// slots returns p's colours in Catppuccin's order. Each name is the field's
// name in lowercase, the key go-toml decodes into that field.
func (p *Palette) slots() []slot {
	return []slot{
		{"rosewater", &p.Rosewater},
		{"flamingo", &p.Flamingo},
		{"pink", &p.Pink},
		{"mauve", &p.Mauve},
		{"red", &p.Red},
		{"maroon", &p.Maroon},
		{"peach", &p.Peach},
		{"yellow", &p.Yellow},
		{"green", &p.Green},
		{"teal", &p.Teal},
		{"sky", &p.Sky},
		{"sapphire", &p.Sapphire},
		{"blue", &p.Blue},
		{"lavender", &p.Lavender},
		{"text", &p.Text},
		{"subtext1", &p.Subtext1},
		{"subtext0", &p.Subtext0},
		{"overlay2", &p.Overlay2},
		{"overlay1", &p.Overlay1},
		{"overlay0", &p.Overlay0},
		{"surface2", &p.Surface2},
		{"surface1", &p.Surface1},
		{"surface0", &p.Surface0},
		{"base", &p.Base},
		{"mantle", &p.Mantle},
		{"crust", &p.Crust},
	}
}

func lattePalette() Palette {
	return Palette{
		Rosewater: "#dc8a78",
		Flamingo:  "#dd7878",
		Pink:      "#ea76cb",
		Mauve:     "#8839ef",
		Red:       "#d20f39",
		Maroon:    "#e64553",
		Peach:     "#fe640b",
		Yellow:    "#df8e1d",
		Green:     "#40a02b",
		Teal:      "#179299",
		Sky:       "#04a5e5",
		Sapphire:  "#209fb5",
		Blue:      "#1e66f5",
		Lavender:  "#7287fd",
		Text:      "#4c4f69",
		Subtext1:  "#5c5f77",
		Subtext0:  "#6c6f85",
		Overlay2:  "#7c7f93",
		Overlay1:  "#8c8fa1",
		Overlay0:  "#9ca0b0",
		Surface2:  "#acb0be",
		Surface1:  "#bcc0cc",
		Surface0:  "#ccd0da",
		Base:      "#eff1f5",
		Mantle:    "#e6e9ef",
		Crust:     "#dce0e8",
	}
}

func frappePalette() Palette {
	return Palette{
		Rosewater: "#f2d5cf",
		Flamingo:  "#eebebe",
		Pink:      "#f4b8e4",
		Mauve:     "#ca9ee6",
		Red:       "#e78284",
		Maroon:    "#ea999c",
		Peach:     "#ef9f76",
		Yellow:    "#e5c890",
		Green:     "#a6d189",
		Teal:      "#81c8be",
		Sky:       "#99d1db",
		Sapphire:  "#85c1dc",
		Blue:      "#8caaee",
		Lavender:  "#babbf1",
		Text:      "#c6d0f5",
		Subtext1:  "#b5bfe2",
		Subtext0:  "#a5adce",
		Overlay2:  "#949cbb",
		Overlay1:  "#838ba7",
		Overlay0:  "#737994",
		Surface2:  "#626880",
		Surface1:  "#51576d",
		Surface0:  "#414559",
		Base:      "#303446",
		Mantle:    "#292c3c",
		Crust:     "#232634",
	}
}

func macchiatoPalette() Palette {
	return Palette{
		Rosewater: "#f4dbd6",
		Flamingo:  "#f0c6c6",
		Pink:      "#f5bde6",
		Mauve:     "#c6a0f6",
		Red:       "#ed8796",
		Maroon:    "#ee99a0",
		Peach:     "#f5a97f",
		Yellow:    "#eed49f",
		Green:     "#a6da95",
		Teal:      "#8bd5ca",
		Sky:       "#91d7e3",
		Sapphire:  "#7dc4e4",
		Blue:      "#8aadf4",
		Lavender:  "#b7bdf8",
		Text:      "#cad3f5",
		Subtext1:  "#b8c0e0",
		Subtext0:  "#a5adcb",
		Overlay2:  "#939ab7",
		Overlay1:  "#8087a2",
		Overlay0:  "#6e738d",
		Surface2:  "#5b6078",
		Surface1:  "#494d64",
		Surface0:  "#363a4f",
		Base:      "#24273a",
		Mantle:    "#1e2030",
		Crust:     "#181926",
	}
}

func mochaPalette() Palette {
	return Palette{
		Rosewater: "#f5e0dc",
		Flamingo:  "#f2cdcd",
		Pink:      "#f5c2e7",
		Mauve:     "#cba6f7",
		Red:       "#f38ba8",
		Maroon:    "#eba0ac",
		Peach:     "#fab387",
		Yellow:    "#f9e2af",
		Green:     "#a6e3a1",
		Teal:      "#94e2d5",
		Sky:       "#89dceb",
		Sapphire:  "#74c7ec",
		Blue:      "#89b4fa",
		Lavender:  "#b4befe",
		Text:      "#cdd6f4",
		Subtext1:  "#bac2de",
		Subtext0:  "#a6adc8",
		Overlay2:  "#9399b2",
		Overlay1:  "#7f849c",
		Overlay0:  "#6c7086",
		Surface2:  "#585b70",
		Surface1:  "#45475a",
		Surface0:  "#313244",
		Base:      "#1e1e2e",
		Mantle:    "#181825",
		Crust:     "#11111b",
	}
}
