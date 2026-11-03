package ui

import (
	"fmt"
	"io"
	"slices"
	"strings"
	"testing"

	uv "github.com/charmbracelet/ultraviolet"

	"github.com/mhersson/aim/internal/term"
)

// themedScreen draws one golden case; draw returns the frame and its size.
type themedScreen struct {
	name string
	draw func(t *testing.T, th string, icons bool) (content string, w, h int)
}

func appScreen(name string, build func(t *testing.T, th string, icons bool) *App) themedScreen {
	return themedScreen{name, func(t *testing.T, th string, icons bool) (string, int, int) {
		a := build(t, th, icons)

		return a.View().Content, a.width, a.height
	}}
}

func themedScreens() []themedScreen {
	var screens []themedScreen

	for _, c := range railCases() {
		screens = append(screens, appScreen("rail/"+c.name, c.build))
	}

	for _, c := range reviewCases() {
		screens = append(screens, appScreen("review/"+c.name, func(t *testing.T, th string, icons bool) *App {
			return c.scene(th, icons).build(t)
		}))
	}

	for _, c := range layoutChromeCases() {
		screens = append(screens, appScreen("chrome/"+c.name, c.build))
	}

	for _, c := range layoutTrayCases() {
		screens = append(screens, themedScreen{"tray/" + c.name, func(t *testing.T, th string, icons bool) (string, int, int) {
			return c.build(t, th, icons).workspaceBar(c.width), c.width, 1
		}})
	}

	for _, c := range pickerGoldenCases() {
		screens = append(screens, appScreen("picker/"+c.name, func(t *testing.T, th string, icons bool) *App {
			return pickerScene(t, th, icons, c.page)
		}))
	}

	for _, c := range modelFrameCases() {
		screens = append(screens, appScreen("frame-model/"+c.name, c.build))
	}

	screens = append(screens, appScreen("shell", func(t *testing.T, th string, icons bool) *App {
		a := newThemedApp(t, th)
		a.icons = icons
		sized(t, a, 100, 30)

		return a
	}))

	return screens
}

// contentVT replays a frame into a virtual terminal to read cell colours.
func contentVT(t *testing.T, content string, w, h int) term.VT {
	t.Helper()

	vt := term.NewVT(w, h, io.Discard)

	t.Cleanup(func() { _ = vt.Close() })

	if _, err := vt.Write([]byte(strings.ReplaceAll(content, "\n", "\r\n"))); err != nil {
		t.Fatal(err)
	}

	return vt
}

// sharedColours lists the visible colours that a and b draw the same. A blank
// cell has no visible foreground, and a cell without an underline has no
// visible underline colour.
func sharedColours(a, b term.VT, w, h int) []string {
	var shared []string

	for y := range h {
		for x := range w {
			ca, cb := a.CellAt(x, y), b.CellAt(x, y)
			if ca == nil || cb == nil {
				continue
			}

			check := func(which string, colA, colB interface{ RGBA() (r, g, b, a uint32) }) {
				if colA == nil || colB == nil {
					return
				}

				if hexOf(colA) == hexOf(colB) {
					shared = append(shared, fmt.Sprintf("cell (%d,%d) %q %s %s", x, y, ca.Content, which, hexOf(colA)))
				}
			}

			if ca.Style.Bg != nil && cb.Style.Bg != nil {
				check("bg", ca.Style.Bg, cb.Style.Bg)
			}

			if strings.TrimSpace(ca.Content) != "" && ca.Style.Fg != nil && cb.Style.Fg != nil {
				check("fg", ca.Style.Fg, cb.Style.Fg)
			}

			if ca.Style.Underline != uv.UnderlineNone && ca.Style.UnderlineColor != nil && cb.Style.UnderlineColor != nil {
				check("ul", ca.Style.UnderlineColor, cb.Style.UnderlineColor)
			}
		}
	}

	return shared
}

// colourScreens are the screens the colour check draws: icons on, every style
// source of each area; icons off, one per area, since a no-icons fallback could
// pick its own colour. Cases that draw the same styles at another size, pin or
// width are left out.
func colourScreens(icons bool) []string {
	if !icons {
		return []string{
			"rail/cards", "review/unified", "chrome/scrolling-both-sides", "tray/bsp/pr-wide",
			"picker/list", "frame-model/agent-model-hot", "shell",
		}
	}

	var names []string

	for _, n := range []string{
		"cards", "focused-blocked-selected", "pulse-dim", "search-typing", "search-no-match", "toast-over-rail",
		"no-hooks-selected", "palette", "overflow-70", "no-agents", "named", "metered", "prs", "ahead", "place", "plan",
	} {
		names = append(names, "rail/"+n)
	}

	for _, n := range []string{
		"unified", "split", "full-file", "hidden-characters", "binary-and-truncated", "no-snapshot", "turn-past",
		"empty", "sticky-header", "search-prompt", "pinned", "worktree-removed", "worktree-list", "pr", "pr-unpushed",
		"pr-tree", "rendered", "md-page", "links", "reviewed", "wrapped", "unfocused", "narrow-tree-open",
	} {
		names = append(names, "review/"+n)
	}

	return append(names,
		"chrome/scrolling-both-sides", "chrome/scrolling-search-prompt", "chrome/preselect-all-tags",
		"tray/bsp/wide", "tray/bsp/pr-wide", "tray/bsp/pr-failed",
		"picker/list", "picker/new-page", "frame-model/agent-model", "frame-model/agent-model-hot", "shell")
}

// TestScreensTakeTheirColoursFromTheTheme: a colour that does not change with
// the theme does not come from it, and this check is why the goldens draw one theme.
func TestScreensTakeTheirColoursFromTheTheme(t *testing.T) {
	t.Parallel()

	all := themedScreens()

	for _, icons := range []bool{true, false} {
		for _, name := range colourScreens(icons) {
			i := slices.IndexFunc(all, func(s themedScreen) bool { return s.name == name })
			if i < 0 {
				t.Fatalf("no screen %q", name)
			}

			s := all[i]

			t.Run(fmt.Sprintf("icons=%v/%s", icons, s.name), func(t *testing.T) {
				t.Parallel()

				mocha, w, h := s.draw(t, "catppuccin-mocha", icons)
				latte, _, _ := s.draw(t, "catppuccin-latte", icons)

				shared := sharedColours(contentVT(t, mocha, w, h), contentVT(t, latte, w, h), w, h)
				for _, c := range shared[:min(len(shared), 5)] {
					t.Errorf("%s is the same in mocha and latte, so it is not a theme colour", c)
				}

				if len(shared) > 5 {
					t.Errorf("%d cells in all", len(shared))
				}
			})
		}
	}
}
