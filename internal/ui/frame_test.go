package ui

import (
	"fmt"
	"io"
	"slices"
	"strings"
	"testing"

	"charm.land/lipgloss/v2"
	uv "github.com/charmbracelet/ultraviolet"
	"github.com/charmbracelet/x/ansi"
	"github.com/charmbracelet/x/exp/golden"

	"github.com/mhersson/aim/internal/core"
	"github.com/mhersson/aim/internal/term"
	"github.com/mhersson/aim/internal/ui/theme"
)

type paneSpec struct {
	kind  core.PaneKind
	cwd   string
	split core.Split
	text  string // fed to the guest after the pane is sized
}

// framedApp opens the specs through the store, starts a fake-process pane for
// each sized to its content rect, and feeds the text.
func framedApp(t *testing.T, w, h int, specs ...paneSpec) (*App, map[core.PaneID]*fakeProc) {
	t.Helper()

	return framedAppOn(t, newTestApp(t), w, h, specs...)
}

// framedAppOn is framedApp on an app the caller made, to set it up first: the
// panes read a.clock for their sync hold.
func framedAppOn(t *testing.T, a *App, w, h int, specs ...paneSpec) (*App, map[core.PaneID]*fakeProc) {
	t.Helper()

	sized(t, a, w, h)

	texts := map[core.PaneID]string{}

	for i, s := range specs {
		if err := a.dispatch(core.OpenPane{Kind: s.kind, Cwd: s.cwd, Split: s.split}); err != nil {
			t.Fatal(err)
		}

		texts[core.PaneID(fmt.Sprintf("p%d", i+1))] = s.text
	}

	return a, attachPanes(t, a, texts)
}

// attachPanes gives every pane in the state a fake process sized to its
// content rect and feeds it its text. The panes read the app's clock.
func attachPanes(t *testing.T, a *App, texts map[core.PaneID]string) map[core.PaneID]*fakeProc {
	t.Helper()

	rects := a.layoutRects()
	procs := map[core.PaneID]*fakeProc{}

	for id := range a.store.State().Panes {
		c := contentRect(rects[id])
		fp := newFakeProc()
		p := newPane(id, fp, max(c.W, 1), max(c.H, 1), a.clock)

		t.Cleanup(func() { _ = p.close() })

		a.panes[id] = p
		procs[id] = fp

		if texts[id] != "" {
			feedAndWait(t, p, fp, texts[id])
			p.render()
		}
	}

	return procs
}

func shellSpec(cwd, text string) paneSpec {
	return paneSpec{kind: core.KindShell, cwd: cwd, split: core.SplitVertical, text: text}
}

// frameCases are layouts at 40x12, each focused on its last-opened pane unless
// the builder moves focus.
func frameCases() []struct {
	name  string
	build func(t *testing.T) *App
} {
	open := func(t *testing.T, a *App, kind core.PaneKind, cwd string, split core.Split) {
		t.Helper()

		if err := a.dispatch(core.OpenPane{Kind: kind, Cwd: cwd, Split: split}); err != nil {
			t.Fatal(err)
		}
	}
	focus := func(t *testing.T, a *App, id core.PaneID) {
		t.Helper()

		if err := a.dispatch(core.FocusPane{ID: id}); err != nil {
			t.Fatal(err)
		}
	}
	started := func(t *testing.T, build func(a *App)) *App {
		t.Helper()

		a := newTestApp(t)
		sized(t, a, 40, 12)
		build(a)
		attachPanes(t, a, map[core.PaneID]string{"p1": "left", "p2": "right"})

		return a
	}

	cases := []struct {
		name  string
		build func(t *testing.T) *App
	}{
		{"one", func(t *testing.T) *App {
			return started(t, func(a *App) { open(t, a, core.KindShell, "/home/u/proj", core.SplitVertical) })
		}},
		{"agent", func(t *testing.T) *App {
			return started(t, func(a *App) { open(t, a, core.KindAgent, "/home/u/proj", core.SplitVertical) })
		}},
		{"vsplit", func(t *testing.T) *App {
			return started(t, func(a *App) {
				open(t, a, core.KindShell, "/home/u/proj", core.SplitVertical)
				open(t, a, core.KindAgent, "/home/u/other", core.SplitVertical)
			})
		}},
		{"hsplit", func(t *testing.T) *App {
			return started(t, func(a *App) {
				open(t, a, core.KindShell, "/home/u/proj", core.SplitVertical)
				open(t, a, core.KindShell, "/home/u/other", core.SplitHorizontal)
			})
		}},
		{"tee", func(t *testing.T) *App {
			return started(t, func(a *App) {
				open(t, a, core.KindShell, "/home/u/proj", core.SplitVertical)
				open(t, a, core.KindShell, "/home/u/other", core.SplitVertical)
				open(t, a, core.KindAgent, "/home/u/third", core.SplitHorizontal)
			})
		}},
		{"tee-inner-focus", func(t *testing.T) *App {
			return started(t, func(a *App) {
				open(t, a, core.KindShell, "/home/u/proj", core.SplitVertical)
				open(t, a, core.KindShell, "/home/u/other", core.SplitVertical)
				open(t, a, core.KindAgent, "/home/u/third", core.SplitHorizontal)
				focus(t, a, "p2")
			})
		}},
		{"tee-down", func(t *testing.T) *App {
			return started(t, func(a *App) {
				open(t, a, core.KindShell, "/home/u/proj", core.SplitVertical)
				open(t, a, core.KindShell, "/home/u/other", core.SplitHorizontal)
				open(t, a, core.KindAgent, "/home/u/third", core.SplitVertical)
			})
		}},
		{"cross", func(t *testing.T) *App {
			return started(t, func(a *App) {
				open(t, a, core.KindShell, "/home/u/proj", core.SplitVertical)
				open(t, a, core.KindShell, "/home/u/other", core.SplitVertical)
				open(t, a, core.KindShell, "/home/u/third", core.SplitHorizontal)
				focus(t, a, "p1")
				open(t, a, core.KindAgent, "/home/u/fourth", core.SplitHorizontal)
			})
		}},
		{"zoomed", func(t *testing.T) *App {
			a := started(t, func(a *App) {
				open(t, a, core.KindShell, "/home/u/proj", core.SplitVertical)
				open(t, a, core.KindShell, "/home/u/other", core.SplitVertical)

				if err := a.dispatch(core.ToggleZoom{}); err != nil {
					t.Fatal(err)
				}
			})

			return a
		}},
		{"empty", func(t *testing.T) *App { return started(t, func(*App) {}) }},
	}

	for _, c := range modelFrameCases() {
		cases = append(cases, struct {
			name  string
			build func(t *testing.T) *App
		}{c.name, func(t *testing.T) *App { return c.build(t, "catppuccin-mocha", true) }})
	}

	return cases
}

// modelFrameCases are agent frames whose status lines reported a model, with and
// without a meter, among tags, and in panes too narrow for the tag beside the
// title: zoomed-model has room for the title, the zoom tag and the badge but not
// for the model tag as well.
func modelFrameCases() []struct {
	name  string
	build func(t *testing.T, th string, icons bool) *App
} {
	started := func(t *testing.T, th string, icons bool, w int, build func(a *App)) *App {
		t.Helper()

		a := newThemedApp(t, th)
		a.icons = icons
		sized(t, a, w, 12)
		build(a)
		attachPanes(t, a, map[core.PaneID]string{"p1": "left", "p2": "right"})

		return a
	}
	// agent opens an agent pane in state st whose status line reported model and, unless meter is nil, a percentage.
	agent := func(t *testing.T, a *App, id core.PaneID, st core.AgentState, model string, meter *int) {
		t.Helper()

		status := core.SetAgentStatus{ID: id, Session: "s-" + core.SessionID(id), Model: model}
		if meter != nil {
			status.Context, status.ContextKnown = float64(*meter), true
		}

		openIn(t, a,
			core.OpenPane{Kind: core.KindAgent, Cwd: "/home/u/proj", Split: core.SplitVertical},
			core.SetAgentState{ID: id, State: st},
			status,
		)
	}

	return []struct {
		name  string
		build func(t *testing.T, th string, icons bool) *App
	}{
		{"agent-model", func(t *testing.T, th string, icons bool) *App {
			return started(t, th, icons, 40, func(a *App) { agent(t, a, "p1", core.AgentWorking, "Opus 5.5", new(62)) })
		}},
		{"agent-model-hot", func(t *testing.T, th string, icons bool) *App {
			return started(t, th, icons, 40, func(a *App) { agent(t, a, "p1", core.AgentBlocked, "Opus 5.5", new(95)) })
		}},
		{"agent-model-only", func(t *testing.T, th string, icons bool) *App {
			return started(t, th, icons, 40, func(a *App) { agent(t, a, "p1", core.AgentIdle, "Opus 5.5", nil) })
		}},
		{"zoomed-model", func(t *testing.T, th string, icons bool) *App {
			return started(t, th, icons, 40, func(a *App) {
				openIn(t, a, core.OpenPane{Kind: core.KindShell, Cwd: "/home/u/other", Split: core.SplitVertical})
				agent(t, a, "p2", core.AgentWorking, "Opus 5.5", new(78))
				openIn(t, a, core.ToggleZoom{})
			})
		}},
		{"zoomed-model-wide", func(t *testing.T, th string, icons bool) *App {
			return started(t, th, icons, 50, func(a *App) {
				openIn(t, a, core.OpenPane{Kind: core.KindShell, Cwd: "/home/u/other", Split: core.SplitVertical})
				agent(t, a, "p2", core.AgentWorking, "Opus 5.5", new(78))
				openIn(t, a, core.ToggleZoom{})
			})
		}},
		{"split-model-dropped", func(t *testing.T, th string, icons bool) *App {
			return started(t, th, icons, 40, func(a *App) {
				openIn(t, a, core.OpenPane{Kind: core.KindShell, Cwd: "/home/u/other", Split: core.SplitVertical})
				agent(t, a, "p2", core.AgentWorking, "Opus 5.5", new(62))
			})
		}},
	}
}

// TestFrameModelText draws the model frames without styling and with icons
// off; TestFrameGolden draws them with icons.
func TestFrameModelText(t *testing.T) {
	t.Parallel()

	for _, c := range modelFrameCases() {
		t.Run("icons=false/"+c.name, func(t *testing.T) {
			t.Parallel()
			golden.RequireEqual(t, []byte(ansi.Strip(c.build(t, "catppuccin-mocha", false).View().Content)))
		})
	}
}

func TestFrameGolden(t *testing.T) {
	t.Parallel()

	for _, tt := range frameCases() {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			golden.RequireEqual(t, []byte(tt.build(t).View().Content))
		})
	}
}

// Every line is exactly the terminal width, whatever the size, layout or zoom:
// six panes with a wide-glyph title at the sizes where something gives way.
func TestFrameSizes(t *testing.T) {
	t.Parallel()

	build := func(w, h int) *App {
		specs := make([]paneSpec, 6)
		for i := range specs {
			split := core.SplitVertical
			if i%2 == 1 {
				split = core.SplitHorizontal
			}

			specs[i] = paneSpec{kind: core.KindShell, cwd: "/a/b", split: split, text: "\x1b]2;日本語のタイトル\x07hello world"}
		}

		a, _ := framedApp(t, w, h, specs...)

		return a
	}

	const rows = 12

	// Smallest sizes: the bar alone, the bar and rule, the first body (minFrame
	// below the bar and rule).
	sizes := [][2]int{{1, 1}, {2, 2}, {minFrame, 2 + minFrame}}

	// Widths: where the bar's plan changes, the zoom tag's edge (it needs its
	// own cells and three more for corners and a dash) and the zoomed wide-glyph
	// title's: it returns at 20, is cut exactly at 21 and one cell short, padded
	// by a dash, at 22.
	zoomW := lipgloss.Width(" " + iconZoom + " zoom ")
	bar := build(40, rows)
	widths := planEdges(bar.tabInfos(), 0, bar.trayModules(), bar.icons, 40)
	widths = append(widths, zoomW+2, zoomW+3, 19, 20, 21, 22)

	slices.Sort(widths)

	for _, w := range slices.Compact(widths) {
		sizes = append(sizes, [2]int{w, rows})
	}

	// Heights at width 40: one under the body minimum, the minimum, a tile
	// dropped by height, the full layout.
	for _, h := range []int{1 + minFrame, 2 + minFrame, 8, rows} {
		sizes = append(sizes, [2]int{40, h})
	}

	for _, sz := range sizes {
		w, h := sz[0], sz[1]
		a := build(w, h)

		for _, zoomed := range []bool{false, true} {
			if zoomed {
				if err := a.dispatch(core.ToggleZoom{}); err != nil {
					t.Fatal(err)
				}
			}

			lines := strings.Split(a.View().Content, "\n")
			if len(lines) != h {
				t.Fatalf("%dx%d zoomed=%v: %d lines, want %d", w, h, zoomed, len(lines), h)
			}

			for i, l := range lines {
				if got := lipgloss.Width(l); got != w {
					t.Errorf("%dx%d zoomed=%v line %d: width %d, want %d: %q", w, h, zoomed, i, got, w, ansi.Strip(l))
				}
			}
		}
	}
}

// A 3x3 pane is a box around one content cell; a tile of 2x2 is left out.
func TestSmallestPanes(t *testing.T) {
	t.Parallel()
	t.Run("3x3 draws a box with one content cell", func(t *testing.T) {
		a, _ := framedApp(t, 3, 5, shellSpec("/a", "x"))

		if got := a.layoutRects()["p1"]; got != (core.Rect{X: 0, Y: 2, W: 3, H: 3}) {
			t.Fatalf("rect = %+v, want 3x3 at the body", got)
		}

		if cols, rows := paneSizeOf(a, "p1"); cols != 1 || rows != 1 {
			t.Errorf("PTY = %dx%d, want 1x1", cols, rows)
		}

		got := strings.Split(ansi.Strip(a.View().Content), "\n")[2:5]
		if want := []string{"╭─╮", "│x│", "╰─╯"}; !slices.Equal(got, want) {
			t.Errorf("box = %q, want %q", got, want)
		}
	})

	t.Run("a 2x2 tile is left out", func(t *testing.T) {
		// A 5x3 body split into 2 and 3 columns: the 2-wide tile is too small.
		a, _ := framedApp(t, 5, 5, shellSpec("/a", "x"), shellSpec("/b", "y"))

		rects := a.layoutRects()
		if _, drawn := rects["p1"]; drawn || len(rects) != 1 {
			t.Fatalf("rects = %v, want only p2", rects)
		}

		if cols, rows := paneSizeOf(a, "p1"); cols != 1 || rows != 1 {
			t.Errorf("left-out PTY = %dx%d, want 1x1", cols, rows)
		}

		got := strings.Split(ansi.Strip(a.View().Content), "\n")[2:5]
		if want := []string{"  ╭─╮", "  │y│", "  ╰─╯"}; !slices.Equal(got, want) {
			t.Errorf("body = %q, want %q", got, want)
		}
	})
}

// topBorder is the top row of the frame, under the workspace bar, without styling.
func topBorder(a *App) string {
	return ansi.Strip(strings.Split(a.View().Content, "\n")[2])
}

// fill pads prefix with border cells to w cells.
func fill(prefix string, w int) string {
	return prefix + strings.Repeat("─", w-lipgloss.Width(prefix))
}

// dashes pads prefix with border cells to w cells and closes it with a corner.
func dashes(prefix string, w int) string { return fill(prefix, w-1) + "╮" }

func TestTitle(t *testing.T) {
	t.Parallel()

	zoom := " " + iconZoom + " zoom "
	tests := []struct {
		name  string
		kind  core.PaneKind
		cwd   string
		osc   string
		width int
		icons bool
		zoom  bool
		want  string
	}{
		{"shell", core.KindShell, "/home/u/proj", "", 40, true, false, dashes("╭─ "+iconShell+" shell ", 40)},
		{"agent", core.KindAgent, "/home/u/proj", "", 40, true, false, dashes("╭─ "+iconAgent+" claude ", 40)},
		{"osc title", core.KindShell, "/home/u/proj", "\x1b]2;vim main.go\x07", 40, true, false, dashes("╭─ "+iconShell+" vim main.go ", 40)},
		{"no icons", core.KindAgent, "/home/u/proj", "", 40, false, false, dashes("╭─ claude ", 40)},
		{"no cwd in the title", core.KindShell, "", "", 40, true, false, dashes("╭─ "+iconShell+" shell ", 40)},
		{"title fits exactly", core.KindAgent, "/home/u/proj", "", 13, true, false, dashes("╭─ "+iconAgent+" claude ", 13)},
		{"title shrinks first", core.KindAgent, "/home/u/proj", "", 12, true, false, dashes("╭─ "+iconAgent+" clau… ", 12)},
		{"title shrinks", core.KindShell, "/home/u/proj", "\x1b]2;abcdefghij\x07", 14, true, false, dashes("╭─ "+iconShell+" abcdef… ", 14)},
		{"title shrinks to one", core.KindShell, "/home/u/proj", "\x1b]2;abcdefghij\x07", 11, true, false, dashes("╭─ "+iconShell+" abc… ", 11)},
		{"title goes", core.KindShell, "/home/u/proj", "\x1b]2;abcdefghij\x07", 10, true, false, dashes("╭", 10)},
		{"zoom tag", core.KindShell, "/home/u/proj", "", 40, true, true, fill("╭─ "+iconShell+" shell ", 30) + zoom + "─╮"},
		{"zoom tag no icons", core.KindShell, "/home/u/proj", "", 40, false, true, fill("╭─ shell ", 32) + " zoom ─╮"},
		{"zoom tag takes the title room", core.KindShell, "/home/u/proj", "", 21, true, true, "╭─ " + iconShell + " shell " + zoom + "─╮"},
		{"zoom tag alone", core.KindShell, "/home/u/proj", "", 11, true, true, "╭" + zoom + "─╮"},
		{"zoom tag does not fit", core.KindShell, "/home/u/proj", "", 10, true, true, dashes("╭", 10)},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			a, _ := framedApp(t, tt.width, 6, paneSpec{kind: tt.kind, cwd: tt.cwd, split: core.SplitVertical, text: tt.osc})
			a.icons = tt.icons

			if tt.zoom {
				if err := a.dispatch(core.ToggleZoom{}); err != nil {
					t.Fatal(err)
				}
			}

			if got := topBorder(a); got != tt.want {
				t.Errorf("top border = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestTitleControlCharacters(t *testing.T) {
	t.Parallel()
	a, _ := framedApp(t, 40, 6, paneSpec{kind: core.KindShell, cwd: "/home/u/a", split: core.SplitVertical, text: "\x1b]2;a\x01b\x7fc\x07"})

	if got, want := topBorder(a), dashes("╭─ "+iconShell+" abc ", 40); got != want {
		t.Errorf("top border = %q, want %q", got, want)
	}
}

func TestTitleInvisibleCharacters(t *testing.T) {
	t.Parallel()
	// U+202E reorders what follows, U+200B is a zero-width space.
	a, _ := framedApp(t, 40, 6, paneSpec{kind: core.KindShell, cwd: "/home/u/a", split: core.SplitVertical, text: "\x1b]2;a\u202Eb\u200Bc\x07"})

	if got, want := topBorder(a), dashes("╭─ "+iconShell+" abc ", 40); got != want {
		t.Errorf("top border = %q, want %q", got, want)
	}
}

func TestPrintable(t *testing.T) {
	t.Parallel()

	const family = "\U0001F468\u200D\U0001F469\u200D\U0001F467"

	tests := []struct {
		name, in, want string
	}{
		{"plain", "main", "main"},
		{"controls", "a\x1b\x01b\x7fc\u0085d", "abcd"},
		{"bidi override", "a\u202Eb\u2066c", "abc"},
		{"zero-width space and word joiner", "a\u200Bb\u2060c", "abc"},
		{"separators leave no line break", "a\u2028b\u2029c", "abc"},
		{"emoji presentation selector", "\u2764\uFE0F", "\u2764"},
		{"keycap keeps its enclosing mark", "1\uFE0F\u20E3", "1\u20E3"},
		{"a flag stays whole", "\U0001F1F3\U0001F1F4", "\U0001F1F3\U0001F1F4"},
		{"joiner between emoji stays", family, family},
		{"joiner elsewhere goes", "a\u200Db", "ab"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := printable(tt.in); got != tt.want {
				t.Errorf("printable(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}

// An unfocused pane's title is Muted; the focused pane's title is bold in its
// kind colour. The icon is in the kind colour either way.
func TestTitleColours(t *testing.T) {
	t.Parallel()
	ui := mochaUI(t)
	tests := []struct {
		name  string
		kind  core.PaneKind
		kindC theme.Hex
	}{
		{"shell", core.KindShell, ui.Shell},
		{"agent", core.KindAgent, ui.Agent},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			a, _ := framedApp(t, 60, 8,
				paneSpec{kind: tt.kind, cwd: "/w/first", split: core.SplitVertical},
				paneSpec{kind: tt.kind, cwd: "/w/second", split: core.SplitVertical})

			if err := a.dispatch(core.FocusPane{ID: "p1"}); err != nil {
				t.Fatal(err)
			}

			vt := viewVT(t, a)
			p1, p2 := a.layoutRects()["p1"], a.layoutRects()["p2"]
			start := func(r core.Rect) int { return r.X + 5 } // after "╭─ icon "

			// Focused p1: icon and title bold in the kind colour.
			assertCell(t, vt, start(p1)-2, 2, tt.kindC, true)
			assertCell(t, vt, start(p1), 2, tt.kindC, true)
			// Unfocused p2: Muted title, the icon still in the kind colour.
			assertCell(t, vt, start(p2)-2, 2, tt.kindC, true)
			assertCell(t, vt, start(p2), 2, ui.Muted, false)
		})
	}
}

// The focused pane's whole box is in its kind colour; every other border cell
// is Border.
func TestBorderColours(t *testing.T) {
	t.Parallel()
	ui := mochaUI(t)

	for _, tt := range frameCases() {
		if !slices.Contains([]string{"one", "agent", "vsplit", "tee-inner-focus", "cross"}, tt.name) {
			continue
		}

		t.Run(tt.name, func(t *testing.T) {
			a := tt.build(t)
			vt := viewVT(t, a)
			st := a.store.State()
			focus := st.Tab().Focus
			r := a.layoutRects()[focus]

			kindC := ui.Shell
			if st.Panes[focus].Kind == core.KindAgent {
				kindC = ui.Agent
			}

			onFocus := func(x, y int) bool {
				return (x == r.X || x == r.X+r.W-1) && y >= r.Y && y < r.Y+r.H ||
					(y == r.Y || y == r.Y+r.H-1) && x >= r.X && x < r.X+r.W
			}

			for y := range a.height - 1 {
				for x := range a.width {
					c := vt.CellAt(x, y)
					if c == nil || !strings.Contains("─│╭╮╰╯", c.Content) || c.Content == "" {
						continue
					}

					want := ui.Border
					if onFocus(x, y) {
						want = kindC
					}

					assertCell(t, vt, x, y, want, false)
				}
			}
		})
	}
}

func TestIconsAreOneCell(t *testing.T) {
	t.Parallel()

	for name, g := range map[string]string{"agent": iconAgent, "shell": iconShell, "zoom": iconZoom} {
		if w := lipgloss.Width(g); w != 1 {
			t.Errorf("%s icon is %d cells wide, want 1", name, w)
		}
	}
}

func TestNoIconsNoGlyphs(t *testing.T) {
	t.Parallel()

	for _, tt := range frameCases() {
		if !slices.Contains([]string{"one", "agent", "zoomed"}, tt.name) {
			continue
		}

		t.Run(tt.name, func(t *testing.T) {
			a := tt.build(t)
			a.icons = false

			for _, r := range a.View().Content {
				if r >= 0xE000 && r <= 0xF8FF || r >= 0xF0000 {
					t.Fatalf("frame has the private-use glyph %U with icons off", r)
				}
			}
		})
	}
}

// Every pane is its own rounded box: the corners sit on the rect's corners,
// neighbours touch, and no junction glyph or shared cell exists.
func TestFramesAreOneBoxPerPane(t *testing.T) {
	t.Parallel()

	for _, w := range []int{8, 9, 40} {
		a, _ := framedApp(t, w, 12,
			paneSpec{kind: core.KindShell, cwd: "/w/top", split: core.SplitVertical},
			paneSpec{kind: core.KindShell, cwd: "/w/bottom", split: core.SplitHorizontal},
			paneSpec{kind: core.KindShell, cwd: "/w/right", split: core.SplitVertical})

		lines := strings.Split(ansi.Strip(a.View().Content), "\n")
		for _, l := range lines {
			if strings.ContainsAny(l, "┬┴├┤┼") {
				t.Fatalf("width %d: junction glyph in\n%s", w, strings.Join(lines, "\n"))
			}
		}

		cell := func(x, y int) string { return string([]rune(lines[y])[x]) }

		for id, r := range a.layoutRects() {
			for _, c := range []struct {
				x, y int
				want string
			}{
				{r.X, r.Y, "╭"}, {r.X + r.W - 1, r.Y, "╮"}, {r.X, r.Y + r.H - 1, "╰"}, {r.X + r.W - 1, r.Y + r.H - 1, "╯"},
			} {
				if got := cell(c.x, c.y); got != c.want {
					t.Errorf("width %d: %s corner (%d, %d) = %q, want %q", w, id, c.x, c.y, got, c.want)
				}
			}
		}
	}
}

func TestCursorView(t *testing.T) {
	t.Parallel()
	a, procs := framedApp(t, 100, 30, shellSpec("/a", ""), shellSpec("/b", "\x1b[3;5Hx"))

	// p2 is focused and sits right of p1: p1 takes floor(100*0.5) = 50 columns, so p2 starts at x=50 and its content at x=51.
	cur := a.cursorView()
	if cur == nil {
		t.Fatal("cursorView() = nil, want the focused pane's cursor")
	}

	if cur.X != 51+5 || cur.Y != 3+2 {
		t.Errorf("cursor = (%d, %d), want (56, 5)", cur.X, cur.Y)
	}

	if err := a.dispatch(core.ToggleZoom{}); err != nil {
		t.Fatal(err)
	}

	// Zoomed, p2 fills the body, so the cursor is offset by the left frame column and the top border.
	if cur := a.cursorView(); cur == nil || cur.X != 6 || cur.Y != 5 {
		t.Errorf("zoomed cursor = %+v, want (6, 5)", cur)
	}

	a.overlay = &overlay{}
	if a.cursorView() != nil {
		t.Error("cursorView() with an overlay open, want nil")
	}

	a.overlay = nil

	feedAndWait(t, a.panes["p2"], procs["p2"], "\x1b[?25l")

	if a.cursorView() != nil {
		t.Error("cursorView() with the cursor hidden, want nil")
	}
}

func TestCursorViewNoRect(t *testing.T) {
	t.Parallel()

	a, _ := framedApp(t, 1, 1, shellSpec("/a", ""))
	if a.cursorView() != nil {
		t.Error("cursorView() with no rect for the focused pane, want nil")
	}

	if empty := newTestApp(t); empty.cursorView() != nil {
		t.Error("cursorView() with no panes, want nil")
	}
}

func TestCursorViewOutsideContent(t *testing.T) {
	t.Parallel()
	a, procs := framedApp(t, 100, 30, shellSpec("/a", ""))
	// The pane is still 100 wide when the layout shrinks to 40: the cursor is clipped.
	feedAndWait(t, a.panes["p1"], procs["p1"], "\x1b[3;60Hx")
	a.width = 40

	if cur := a.cursorView(); cur != nil {
		t.Errorf("cursorView() = %+v for a cursor outside the content rect, want nil", cur)
	}
}

func TestCursorViewWhichKeyPopup(t *testing.T) {
	t.Parallel()
	a, _ := framedApp(t, 100, 30, shellSpec("/a", ""))
	a.popup = true

	if a.cursorView() != nil {
		t.Error("cursorView() with the which-key popup open, want nil")
	}
}

func TestViewSetsCursor(t *testing.T) {
	t.Parallel()
	a, _ := framedApp(t, 100, 30, shellSpec("/a", "ab"))

	want := a.cursorView()
	got := a.View().Cursor

	if want == nil || got == nil || got.Position != want.Position {
		t.Errorf("View().Cursor = %+v, want %+v", got, want)
	}
}

func mochaUI(t *testing.T) theme.UI {
	t.Helper()

	th, err := theme.Builtin().ByName("catppuccin-mocha")
	if err != nil {
		t.Fatal(err)
	}

	return th.UI
}

// viewVT replays the rendered frame into a virtual terminal to read cell colours.
func viewVT(t *testing.T, a *App) term.VT {
	t.Helper()

	vt := term.NewVT(a.width, a.height, io.Discard)

	t.Cleanup(func() { _ = vt.Close() })

	if _, err := vt.Write([]byte(strings.ReplaceAll(a.View().Content, "\n", "\r\n"))); err != nil {
		t.Fatal(err)
	}

	return vt
}

func assertCell(t *testing.T, vt term.VT, x, y int, want theme.Hex, bold bool) {
	t.Helper()

	c := vt.CellAt(x, y)
	if c == nil || c.Style.Fg == nil {
		t.Errorf("cell (%d, %d) has no foreground", x, y)

		return
	}

	r, g, b, _ := c.Style.Fg.RGBA()
	got := fmt.Sprintf("#%02x%02x%02x", r>>8, g>>8, b>>8)

	if got != string(want) {
		t.Errorf("cell (%d, %d) %q foreground = %s, want %s", x, y, c.Content, got, want)
	}

	if isBold := c.Style.Attrs&uv.AttrBold != 0; isBold != bold {
		t.Errorf("cell (%d, %d) %q bold = %v, want %v", x, y, c.Content, isBold, bold)
	}
}

// Row 1 is a full-width rule in Border; the body starts under it and runs to
// the last row. The rule takes no clicks.
func TestRuleUnderWorkspaceBar(t *testing.T) {
	t.Parallel()
	ui := mochaUI(t)
	a, _ := framedApp(t, 40, 12, shellSpec("/a", "x"), shellSpec("/b", "y"))

	if got, want := bodyRect(40, 12), (core.Rect{Y: 2, W: 40, H: 10}); got != want {
		t.Fatalf("bodyRect = %+v, want %+v", got, want)
	}

	if got := a.layoutRects()["p1"]; got.Y != 2 {
		t.Errorf("first pane starts on row %d, want 2", got.Y)
	}

	if got, want := screenText(a, 1), strings.Repeat("─", 40); got != want {
		t.Errorf("row 1 = %q, want the rule", got)
	}

	vt := viewVT(t, a)
	for x := range 40 {
		assertCell(t, vt, x, 1, ui.Border, false)
	}

	for x := range 40 {
		if h := a.hit(x, 1); h.area != hitNone {
			t.Errorf("click on the rule at column %d hit %v", x, h.area)
		}
	}

	if h := a.hit(5, 2); h.area != hitHeader {
		t.Errorf("click on the top border = %v, want hitHeader", h.area)
	}
}

// Small heights: the workspace bar alone, then the rule too; the body is what
// is left.
func TestRuleSmallHeights(t *testing.T) {
	t.Parallel()

	for _, tt := range []struct {
		h    int
		rule bool
		body int
	}{{1, false, 0}, {2, true, 0}, {3, true, 1}, {4, true, 2}, {6, true, 4}} {
		a, _ := framedApp(t, 20, tt.h, shellSpec("/a", "x"))
		lines := strings.Split(ansi.Strip(a.View().Content), "\n")

		if len(lines) != tt.h {
			t.Fatalf("height %d: %d lines", tt.h, len(lines))
		}

		if got := tt.h >= 2 && lines[1] == strings.Repeat("─", 20); got != tt.rule {
			t.Errorf("height %d: rule = %v, want %v", tt.h, got, tt.rule)
		}

		if got := bodyRect(20, tt.h).H; got != tt.body {
			t.Errorf("height %d: body rows = %d, want %d", tt.h, got, tt.body)
		}
	}
}
