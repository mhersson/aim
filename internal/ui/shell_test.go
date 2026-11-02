package ui

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/charmbracelet/x/exp/golden"

	"github.com/mhersson/aim/internal/core"
	"github.com/mhersson/aim/internal/ui/theme"
)

func mustTheme(t *testing.T, name string) theme.Theme {
	t.Helper()

	th, err := theme.ByName(name)
	if err != nil {
		t.Fatal(err)
	}

	return th
}

func sized(t *testing.T, a *App, w, h int) {
	t.Helper()
	a.Update(tea.WindowSizeMsg{Width: w, Height: h})
}

func TestNewRejectsUnknownTheme(t *testing.T) {
	t.Parallel()

	_, err := New(Options{Logger: testLogger(), Git: SystemGit{}, Store: core.NewStore(core.State{Theme: "nope"})})
	if !errors.Is(err, theme.ErrUnknownTheme) {
		t.Errorf("New() error = %v, want one wrapping ErrUnknownTheme", err)
	}
}

// It compares highlighted colours, so it runs serially (docs/gotchas.md).
func TestShellGolden(t *testing.T) {
	t.Run("catppuccin-mocha", func(t *testing.T) {
		a := newThemedApp(t, "catppuccin-mocha")
		sized(t, a, 100, 30)
		golden.RequireEqual(t, []byte(a.View().Content))
	})
}

// TestShellText is the same screen without styling, so the layout reads.
func TestShellText(t *testing.T) {
	t.Parallel()

	for _, icons := range []bool{true, false} {
		t.Run(fmt.Sprintf("icons=%v", icons), func(t *testing.T) {
			t.Parallel()

			a := newThemedApp(t, "catppuccin-mocha")
			a.icons = icons
			sized(t, a, 100, 30)
			golden.RequireEqual(t, []byte(ansi.Strip(a.View().Content)))
		})
	}
}

func TestViewColours(t *testing.T) {
	t.Parallel()

	for _, name := range theme.Names() {
		t.Run(name, func(t *testing.T) {
			a := newThemedApp(t, name)
			sized(t, a, 100, 30)
			v := a.View()

			th := mustTheme(t, name)
			if v.BackgroundColor != th.UI.Bg {
				t.Errorf("BackgroundColor = %v, want %v", v.BackgroundColor, th.UI.Bg)
			}

			if v.ForegroundColor != th.UI.Fg {
				t.Errorf("ForegroundColor = %v, want %v", v.ForegroundColor, th.UI.Fg)
			}
		})
	}
}

func TestThemeFollowsStore(t *testing.T) {
	t.Parallel()
	a := newThemedApp(t, "catppuccin-mocha")
	sized(t, a, 100, 30)

	if err := a.dispatch(core.SetTheme{Name: "catppuccin-latte"}); err != nil {
		t.Fatal(err)
	}

	v := a.View()

	latte := mustTheme(t, "catppuccin-latte")
	if v.BackgroundColor != latte.UI.Bg {
		t.Errorf("BackgroundColor = %v, want %v", v.BackgroundColor, latte.UI.Bg)
	}
}

func TestDispatchUnknownThemeKeepsPrevious(t *testing.T) {
	t.Parallel()
	a := newThemedApp(t, "catppuccin-mocha")
	sized(t, a, 100, 30)
	before := a.View()

	err := a.dispatch(core.SetTheme{Name: "nope"})
	if !errors.Is(err, theme.ErrUnknownTheme) {
		t.Fatalf("dispatch() error = %v, want one wrapping ErrUnknownTheme", err)
	}

	if got := a.store.State().Theme; got != "catppuccin-mocha" {
		t.Errorf("store theme = %q, want catppuccin-mocha", got)
	}

	if after := a.View(); after.Content != before.Content || after.BackgroundColor != before.BackgroundColor {
		t.Error("view changed after a rejected theme")
	}
}

func TestShellSizes(t *testing.T) {
	t.Parallel()

	for _, name := range theme.Names() {
		for _, sz := range [][2]int{{100, 30}, {40, 12}, {20, 5}, {1, 1}, {3, 1}, {200, 3}} {
			t.Run(fmt.Sprintf("%s/%dx%d", name, sz[0], sz[1]), func(t *testing.T) {
				a := newThemedApp(t, name)
				sized(t, a, sz[0], sz[1])
				content := a.View().Content

				lines := strings.Split(content, "\n")
				if len(lines) != sz[1] {
					t.Fatalf("view has %d lines, want %d", len(lines), sz[1])
				}

				for i, line := range lines {
					if got := lipgloss.Width(line); got != sz[0] {
						t.Errorf("line %d width = %d, want %d: %q", i, got, sz[0], line)
					}
				}

				if strings.Contains(content, "\t") {
					t.Error("view contains a raw tab")
				}
			})
		}
	}
}

func TestShellContent(t *testing.T) {
	t.Parallel()

	tests := []struct {
		w, h     int
		wantCard bool
	}{
		{100, 30, true},
		{40, 12, false},
		{5, 3, false},
	}
	for _, tt := range tests {
		t.Run(fmt.Sprintf("%dx%d", tt.w, tt.h), func(t *testing.T) {
			a := newThemedApp(t, "catppuccin-mocha")
			sized(t, a, tt.w, tt.h)

			plain := ansi.Strip(a.View().Content)
			if got := strings.Contains(plain, "No panes yet."); got != tt.wantCard {
				t.Errorf("card present = %v, want %v:\n%s", got, tt.wantCard, plain)
			}
		})
	}
}
