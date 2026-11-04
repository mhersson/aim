package ui

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/charmbracelet/x/exp/golden"

	"github.com/mhersson/aim/internal/core"
	"github.com/mhersson/aim/internal/ui/palette"
)

var (
	keyDown  = tea.KeyPressMsg{Code: tea.KeyDown}
	keyEnter = tea.KeyPressMsg{Code: tea.KeyEnter}
)

// run executes cmd and feeds every message it produces back into a, so a
// palette's follow-up messages (highlight, chosen, closed) take effect.
func run(a *App, cmd tea.Cmd) {
	if cmd == nil {
		return
	}

	switch msg := cmd().(type) {
	case nil:
	case tea.BatchMsg:
		for _, c := range msg {
			run(a, c)
		}
	case tea.QuitMsg:
	default:
		_, next := a.Update(msg)
		run(a, next)
	}
}

// pressRun sends keys one by one, running the resulting commands.
func pressRun(a *App, keys ...tea.KeyPressMsg) {
	for _, k := range keys {
		_, cmd := a.Update(k)
		run(a, cmd)
	}
}

func typeText(a *App, s string) {
	for _, r := range s {
		pressRun(a, keyRune(r))
	}
}

func TestPaletteListsEveryCommand(t *testing.T) {
	t.Parallel()
	a := newTestApp(t)
	sized(t, a, 100, 30)
	pressRun(a, keyLeader, keyRune(':'))

	if a.overlay == nil {
		t.Fatal("overlay not open after leader :")
	}

	// Scoped commands act on one view and are not listed.
	var (
		want   []string
		listed []command
	)

	for _, c := range a.reg.all() {
		if c.scope == "" {
			want = append(want, c.id)
			listed = append(listed, c)
		}
	}

	view := ansi.Strip(a.View().Content)
	// The list scrolls past one page; the first page shows in registration order.
	for _, c := range listed[:min(len(listed), palette.MaxRows)] {
		if !strings.Contains(view, c.title) {
			t.Errorf("palette view lacks command %q (%s)", c.title, c.id)
		}
	}

	var got []string
	for _, it := range a.overlay.items {
		got = append(got, it.ID)
	}

	if !slices.Equal(got, want) {
		t.Errorf("palette ids = %v, want %v", got, want)
	}

	if !strings.Contains(view, "ctrl+space n") {
		t.Errorf("palette lacks the new agent hint:\n%s", view)
	}
}

func TestThemePickerIcons(t *testing.T) {
	t.Parallel()

	a := newTestApp(t)
	a.icons = false
	sized(t, a, 100, 30)
	pressRun(a, keyLeader, keyRune('t'))

	view := ansi.Strip(a.View().Content)
	if strings.Contains(view, iconTheme) || !strings.Contains(view, " of 4 ") {
		t.Errorf("icons off: picker shows the theme glyph or lacks its count:\n%s", view)
	}
}

func TestPaletteRunsCommand(t *testing.T) {
	t.Parallel()
	a := newTestApp(t)
	sized(t, a, 100, 30)
	pressRun(a, keyLeader, keyRune(':'))
	typeText(a, "macchiato")
	pressRun(a, keyEnter)

	if a.overlay != nil {
		t.Error("overlay still open after enter")
	}

	if got := a.store.State().Theme; got != "catppuccin-macchiato" {
		t.Errorf("theme = %q, want catppuccin-macchiato", got)
	}
}

func TestPaletteOpensThemePicker(t *testing.T) {
	t.Parallel()
	a := newTestApp(t)
	sized(t, a, 100, 30)
	pressRun(a, keyLeader, keyRune(':'))
	typeText(a, "pick")
	pressRun(a, keyEnter)

	if a.overlay == nil || a.overlay.kind != overlayThemes {
		t.Fatalf("overlay = %+v, want the theme picker", a.overlay)
	}
}

func TestThemePickerCommits(t *testing.T) {
	t.Parallel()
	a := newTestApp(t)
	sized(t, a, 100, 30)
	pressRun(a, keyLeader, keyRune('t'), keyDown, keyEnter)

	latte := mustTheme(t, "catppuccin-latte")
	if got := a.store.State().Theme; got != latte.Name {
		t.Errorf("theme = %q, want %q", got, latte.Name)
	}

	if a.overlay != nil || a.preview != nil {
		t.Error("overlay or preview still set after enter")
	}

	if got := a.View().BackgroundColor; got != latte.UI.Bg {
		t.Errorf("background = %v, want %v", got, latte.UI.Bg)
	}
}

func TestThemePickerOffersUserThemes(t *testing.T) {
	t.Parallel()

	themes, dawn := withDawn(t)
	a := newOptionsApp(t, Options{Themes: themes, Store: core.NewStore(core.State{Theme: "catppuccin-mocha"})})
	sized(t, a, 100, 30)
	// The user's themes come after the built-ins, so down from Mocha is test-dawn.
	pressRun(a, keyLeader, keyRune('t'), keyDown)

	if a.preview == nil || a.preview.theme.Name != dawn.Name {
		t.Fatalf("preview = %+v, want %s", a.preview, dawn.Name)
	}

	pressRun(a, keyEnter)

	if got := a.store.State().Theme; got != dawn.Name {
		t.Errorf("theme = %q, want %s", got, dawn.Name)
	}
}

func TestThemePickerCtrlSSaves(t *testing.T) {
	t.Parallel()

	const latte = "catppuccin-latte"

	tests := []struct {
		name  string
		file  string // the config before; "" for none
		want  string // the config after
		level toastLevel
	}{
		{"saved", "", "theme = \"catppuccin-latte\"\n", levelSuccess},
		{"not valid TOML", "theme = \n", "theme = \n", levelError},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			home := t.TempDir()
			path := filepath.Join(home, ".config", "aim", "config.toml")

			if tt.file != "" {
				if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
					t.Fatal(err)
				}

				if err := os.WriteFile(path, []byte(tt.file), 0o600); err != nil {
					t.Fatal(err)
				}
			}

			a := newTestApp(t)
			a.configPath = path
			a.getenv = func(k string) string {
				if k == "HOME" {
					return home
				}

				return testGetenv(k)
			}

			sized(t, a, 100, 30)
			pressRun(a, keyLeader, keyRune('t'), keyDown, tea.KeyPressMsg{Code: 's', Mod: tea.ModCtrl})

			if got := a.store.State().Theme; got != latte {
				t.Errorf("theme = %q, want %q", got, latte)
			}

			if a.overlay != nil || a.preview != nil {
				t.Error("overlay or preview still set after ctrl+s")
			}

			got, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}

			if string(got) != tt.want {
				t.Errorf("config = %q, want %q", got, tt.want)
			}

			if !carries(latte, "~/.config/aim/config.toml").matches(a.notice) || a.noticeLevel() != tt.level {
				t.Errorf("notice = %q at level %v, want one carrying the theme and the config at level %v", a.notice, a.noticeLevel(), tt.level)
			}
		})
	}
}

// It compares highlighted colours, so it runs serially (docs/gotchas.md).
func TestThemePickerEscRestores(t *testing.T) {
	a := newTestApp(t)
	sized(t, a, 100, 30)
	mocha := mustTheme(t, "catppuccin-mocha")

	pressRun(a, keyLeader, keyRune('t'))

	for _, name := range []string{"catppuccin-latte", "catppuccin-frappe", "catppuccin-macchiato"} {
		pressRun(a, keyDown)

		th := mustTheme(t, name)
		if got := a.View().BackgroundColor; got != th.UI.Bg {
			t.Errorf("after moving to %s: background = %v, want %v", name, got, th.UI.Bg)
		}

		if got := a.store.State().Theme; got != mocha.Name {
			t.Errorf("after moving to %s: store theme = %q, want mocha", name, got)
		}
	}

	pressRun(a, keyEsc)

	if a.overlay != nil || a.preview != nil {
		t.Error("overlay or preview still set after esc")
	}

	v := a.View()
	if v.BackgroundColor != mocha.UI.Bg {
		t.Errorf("background after esc = %v, want %v", v.BackgroundColor, mocha.UI.Bg)
	}

	fresh := newTestApp(t)
	sized(t, fresh, 100, 30)

	if v.Content != fresh.View().Content {
		t.Error("view after esc differs from a never-previewed shell")
	}
}

func TestOverlayOwnsKeys(t *testing.T) {
	t.Parallel()
	a := newTestApp(t)
	sized(t, a, 100, 30)
	pressRun(a, keyLeader, keyRune(':'))

	cmd := press(a, keyLeader, keyRune('q'))
	if a.pending {
		t.Error("leader started while the overlay is open")
	}

	if cmd != nil {
		if _, ok := cmd().(tea.QuitMsg); ok {
			t.Error("q quit the app instead of typing into the query")
		}
	}

	if view := ansi.Strip(a.View().Content); !strings.Contains(view, "\u276f q") {
		t.Errorf("query does not show q:\n%s", view)
	}
}

func TestOverlayEndsLeaderPopup(t *testing.T) {
	t.Parallel()
	a := newTestApp(t)
	sized(t, a, 100, 30)
	press(a, keyLeader)
	seq := a.seq
	pressRun(a, keyRune(':'))
	a.Update(whichKeyMsg{seq: seq})

	if a.popup {
		t.Error("stale which-key tick opened the popup over the overlay")
	}
}

func TestOverlaySizes(t *testing.T) {
	t.Parallel()

	for _, key := range []rune{':', 't'} {
		for _, sz := range [][2]int{{100, 30}, {30, 8}, {5, 3}, {1, 1}, {2, 2}} {
			w, h := sz[0], sz[1]
			t.Run(fmt.Sprintf("%c/%dx%d", key, w, h), func(t *testing.T) {
				a := newTestApp(t)
				sized(t, a, w, h)
				pressRun(a, keyLeader, keyRune(key))

				lines := strings.Split(a.View().Content, "\n")
				if len(lines) != h {
					t.Fatalf("view has %d lines, want %d", len(lines), h)
				}

				for i, l := range lines {
					if got := lipgloss.Width(l); got != w {
						t.Errorf("line %d width = %d, want %d", i, got, w)
					}
				}
			})
		}
	}
}

// It compares highlighted colours, so it runs serially (docs/gotchas.md).
func TestPaletteOverlayNoIconsGolden(t *testing.T) {
	a := newTestApp(t)
	a.icons = false
	sized(t, a, 100, 30)
	pressRun(a, keyLeader, keyRune(':'))
	golden.RequireEqual(t, []byte(a.View().Content))
}

// It compares highlighted colours, so it runs serially (docs/gotchas.md).
func TestPaletteOverlayGolden(t *testing.T) {
	a := newTestApp(t)
	sized(t, a, 100, 30)
	pressRun(a, keyLeader, keyRune(':'))
	golden.RequireEqual(t, []byte(a.View().Content))
}

// It compares highlighted colours, so it runs serially (docs/gotchas.md).
func TestThemePickerGolden(t *testing.T) {
	a := newTestApp(t)
	sized(t, a, 100, 30)
	pressRun(a, keyLeader, keyRune('t'), keyDown)
	golden.RequireEqual(t, []byte(a.View().Content))
}

func TestLateHighlightIgnored(t *testing.T) {
	t.Parallel()

	latte := palette.HighlightMsg{ID: "catppuccin-latte"}
	mocha := mustTheme(t, "catppuccin-mocha")
	frappe := mustTheme(t, "catppuccin-frappe")

	t.Run("after esc", func(t *testing.T) {
		a := newTestApp(t)
		sized(t, a, 100, 30)
		pressRun(a, keyLeader, keyRune('t'), keyEsc)
		a.Update(latte)

		if got := a.View().BackgroundColor; got != mocha.UI.Bg {
			t.Errorf("background = %v, want mocha %v", got, mocha.UI.Bg)
		}
	})
	t.Run("after enter", func(t *testing.T) {
		a := newTestApp(t)
		sized(t, a, 100, 30)
		pressRun(a, keyLeader, keyRune('t'), keyDown, keyDown, keyEnter)
		a.Update(latte)

		if got := a.View().BackgroundColor; got != frappe.UI.Bg {
			t.Errorf("background = %v, want frappe %v", got, frappe.UI.Bg)
		}
	})
	t.Run("stale id while open", func(t *testing.T) {
		a := newTestApp(t)
		sized(t, a, 100, 30)
		pressRun(a, keyLeader, keyRune('t'), keyDown, keyDown)
		a.Update(latte)

		if got := a.View().BackgroundColor; got != frappe.UI.Bg {
			t.Errorf("background = %v, want frappe %v", got, frappe.UI.Bg)
		}
	})
}

func TestPasteReachesPalette(t *testing.T) {
	t.Parallel()
	a := newTestApp(t)
	sized(t, a, 100, 30)
	pressRun(a, keyLeader, keyRune(':'))
	_, cmd := a.Update(tea.PasteMsg{Content: "quit"})
	run(a, cmd)

	view := ansi.Strip(a.View().Content)
	if !strings.Contains(view, "\u276f quit") || strings.Contains(view, "Theme: Catppuccin") {
		t.Errorf("paste did not filter the palette:\n%s", view)
	}
}

func TestLateChosenIgnored(t *testing.T) {
	t.Parallel()
	a := newTestApp(t)
	sized(t, a, 100, 30)
	pressRun(a, keyLeader, keyRune(':'))

	chosen := palette.ChosenMsg{ID: "theme.pick"}
	_, cmd := a.Update(chosen)
	run(a, cmd)

	if a.overlay == nil || a.overlay.kind != overlayThemes {
		t.Fatalf("overlay = %+v, want the theme picker", a.overlay)
	}

	_, cmd = a.Update(chosen)
	run(a, cmd)

	if a.overlay == nil || a.overlay.kind != overlayThemes {
		t.Errorf("late chosen closed or replaced the picker: %+v", a.overlay)
	}

	if got := a.store.State().Theme; got != "catppuccin-mocha" {
		t.Errorf("store theme = %q, want catppuccin-mocha", got)
	}
}

func TestOverlayBeforeFirstSize(t *testing.T) {
	t.Parallel()
	a := newTestApp(t)
	press(a, keyLeader)
	a.Update(whichKeyMsg{seq: a.seq})
	pressRun(a, keyRune(':'))

	v := a.View()
	if v.Content != "" || !v.AltScreen {
		t.Errorf("unsized view content = %q, AltScreen = %v, want empty and true", v.Content, v.AltScreen)
	}

	sized(t, a, 100, 30)

	if a.overlay.kind != overlayCommands {
		t.Errorf("overlay kind = %v after sizing, want the commands", a.overlay.kind)
	}

	if view := ansi.Strip(a.View().Content); !strings.Contains(view, "❯") {
		t.Errorf("palette prompt not drawn after sizing:\n%s", view)
	}
}

func TestPaletteFooterPlacement(t *testing.T) {
	t.Parallel()

	items := make([]palette.Item, 12)
	for i := range items {
		items[i] = palette.Item{ID: fmt.Sprint(i), Title: fmt.Sprint("item ", i)}
	}

	pal := palette.New("Commands", items, mustTheme(t, "catppuccin-mocha"))
	// The box is 16 rows tall with ten results and starts at a quarter of the height.
	for _, tc := range []struct {
		height     int
		wantHint   bool
		wantBottom bool
	}{{22, true, true}, {21, true, false}, {20, false, false}, {19, false, false}} {
		base := strings.TrimSuffix(strings.Repeat(strings.Repeat(" ", 80)+"\n", tc.height), "\n")

		text := ansi.Strip(overlayOver(nil, base, 80, tc.height, pal))
		if got := strings.Contains(text, "esc"); got != tc.wantHint {
			t.Errorf("height %d: hint row visible = %v, want %v", tc.height, got, tc.wantHint)
		}

		if got := strings.Contains(text, "\u2570"); got != tc.wantBottom {
			t.Errorf("height %d: bottom border visible = %v, want %v", tc.height, got, tc.wantBottom)
		}
	}
}
