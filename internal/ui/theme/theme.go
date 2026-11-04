// Package theme holds the themes AIM offers, the Catppuccin flavors it ships
// and the user's theme files: each theme's palette, the semantic UI roles
// widgets colour from, and a matching Chroma syntax style.
package theme

import (
	"errors"
	"fmt"
	"strings"
)

// UI maps semantic roles to palette colours.
type UI struct {
	Bg, Fg, Muted, Subtle, Panel, Surface, Border, BorderFocus, Accent, OnAccent,
	Key, Selection, Match, Error, Warning, Success, Info,
	Agent, Shell, Bar, Dim, Clock, Git, Load, Working, Blocked, Idle, Review, Code, Meter Hex
}

// Theme is one theme AIM offers.
type Theme struct {
	Name    string // ByName key, e.g. "catppuccin-mocha"
	Title   string // display name, e.g. "Catppuccin Mocha"
	Dark    bool
	Palette Palette
	UI      UI
}

// ErrUnknownTheme is returned by ByName for a name no theme has.
var ErrUnknownTheme = errors.New("unknown theme")

// Set is the themes AIM offers, in order: the built-ins, then the user's by
// name. It is built once at start and handed to everything that lists themes.
type Set []Theme

// Builtin returns the Catppuccin flavors, light first: latte, frappe,
// macchiato, mocha.
func Builtin() Set {
	return Set{
		newTheme("catppuccin-latte", "Catppuccin Latte", false, lattePalette()),
		newTheme("catppuccin-frappe", "Catppuccin Frappé", true, frappePalette()),
		newTheme("catppuccin-macchiato", "Catppuccin Macchiato", true, macchiatoPalette()),
		newTheme("catppuccin-mocha", "Catppuccin Mocha", true, mochaPalette()),
	}
}

// Names returns the theme names in order.
func (s Set) Names() []string {
	names := make([]string, len(s))
	for i, t := range s {
		names[i] = t.Name
	}

	return names
}

// ByName returns the theme called name.
func (s Set) ByName(name string) (Theme, error) {
	for _, t := range s {
		if t.Name == name {
			return t, nil
		}
	}

	return Theme{}, fmt.Errorf("%w %q (known: %s)", ErrUnknownTheme, name, strings.Join(s.Names(), ", "))
}

func newTheme(name, title string, dark bool, p Palette) Theme {
	return Theme{Name: name, Title: title, Dark: dark, Palette: p, UI: uiRoles(p)}
}

func uiRoles(p Palette) UI {
	return UI{
		Bg:          p.Base,
		Fg:          p.Text,
		Muted:       p.Overlay1,
		Subtle:      p.Overlay0,
		Panel:       p.Mantle,
		Surface:     p.Surface0,
		Border:      p.Surface1,
		BorderFocus: p.Lavender,
		Accent:      p.Mauve,
		OnAccent:    p.Crust,
		Key:         p.Blue,
		Selection:   p.Surface1,
		Match:       p.Peach,
		Error:       p.Red,
		Warning:     p.Yellow,
		Success:     p.Green,
		Info:        p.Blue,
		Agent:       p.Mauve,
		Shell:       p.Blue,
		Bar:         p.Base,
		Dim:         p.Subtext0,
		Clock:       p.Lavender,
		Git:         p.Flamingo,
		Load:        p.Teal,
		Working:     p.Peach,
		Blocked:     p.Red,
		Idle:        p.Green,
		Review:      p.Teal,
		Code:        p.Peach,
		Meter:       p.Teal,
	}
}
