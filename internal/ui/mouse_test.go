package ui

import (
	"encoding/base64"
	"fmt"
	"image/color"
	"slices"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	uv "github.com/charmbracelet/ultraviolet"

	"github.com/mhersson/aim/internal/core"
)

// fakeClip routes the app's native clipboard writes to a recorder on a
// deterministic Linux/Wayland environment, so no test runs a real tool.
func fakeClip(a *App) *fakeRun {
	f := &fakeRun{}
	a.clip.run = f.run
	a.openLink = (&fakeOpen{}).open
	a.clip.getenv = envFrom(map[string]string{"WAYLAND_DISPLAY": "wayland-0"})
	a.clip.goos = "linux"

	return f
}

// isPrimaryOnly reports whether argv writes the primary selection alone.
func isPrimaryOnly(argv []string) bool {
	return slices.Equal(argv, []string{"wl-copy", "--primary"}) ||
		slices.Equal(argv, []string{"xclip", "-selection", "primary"})
}

// clipboardWrites are the stdins of the native writes to the clipboard, in order.
func clipboardWrites(f *fakeRun) []string {
	f.mu.Lock()
	defer f.mu.Unlock()

	var out []string

	for _, c := range f.calls {
		if !isPrimaryOnly(c.argv) {
			out = append(out, c.stdin)
		}
	}

	return out
}

// primaryWrites are the stdins of the primary-only native writes, in order.
func primaryWrites(f *fakeRun) []string {
	f.mu.Lock()
	defer f.mu.Unlock()

	var out []string

	for _, c := range f.calls {
		if isPrimaryOnly(c.argv) {
			out = append(out, c.stdin)
		}
	}

	return out
}

// copiedText is the text of the first native clipboard write (primary-only
// writes skipped), "" when none happened.
func copiedText(f *fakeRun) string {
	if w := clipboardWrites(f); len(w) > 0 {
		return w[0]
	}

	return ""
}

func mdown(a *App, x, y int, b tea.MouseButton, mod tea.KeyMod) {
	a.Update(tea.MouseClickMsg{X: x, Y: y, Button: b, Mod: mod})
}

func mmove(a *App, x, y int, b tea.MouseButton) {
	a.Update(tea.MouseMotionMsg{X: x, Y: y, Button: b})
}

func mup(a *App, x, y int, b tea.MouseButton) {
	a.Update(tea.MouseReleaseMsg{X: x, Y: y, Button: b})
}

// drag presses the left button at (x0, y0), moves to (x1, y1) and releases.
func drag(a *App, x0, y0, x1, y1 int, mod tea.KeyMod) {
	mdown(a, x0, y0, tea.MouseLeft, mod)
	mmove(a, x1, y1, tea.MouseLeft)
	mup(a, x1, y1, tea.MouseLeft)
}

func click(a *App, x, y int) {
	mdown(a, x, y, tea.MouseLeft, 0)
	mup(a, x, y, tea.MouseLeft)
}

// shellsWith is a 60x14 app with two side-by-side shells holding the given
// text; p2 is focused.
func shellsWith(t *testing.T, left, right string) (*App, map[core.PaneID]*fakeProc, *fakeRun) {
	t.Helper()

	a, procs := framedApp(t, 60, 14, shellSpec("/w/a", left), shellSpec("/w/b", right))

	return a, procs, fakeClip(a)
}

const sampleText = "hello world\r\nsecond line\r\nthird"

// copyNow runs the copy command and returns the copied text.
func copyNow(a *App, f *fakeRun) string {
	run(a, chord(a, 'y'))

	return copiedText(f)
}

func selBgAt(a *App, x, y int) bool { return selBgIn(a, drawnBuffer(a), x, y) }

// drawnBuffer is the frame View draws, as cells.
func drawnBuffer(a *App) *uv.ScreenBuffer {
	buf := uv.NewScreenBuffer(a.width, a.height)
	uv.NewStyledString(a.View().Content).Draw(buf, buf.Bounds())

	return &buf
}

// selBgIn reports whether cell (x, y) of an already drawn frame has the
// selection background.
func selBgIn(a *App, buf *uv.ScreenBuffer, x, y int) bool {
	c := buf.CellAt(x, y)
	if c == nil || c.Style.Bg == nil {
		return false
	}

	return sameColor(c.Style.Bg, a.current().theme.UI.Selection)
}

func sameColor(p, q color.Color) bool {
	pr, pg, pb, pa := p.RGBA()
	qr, qg, qb, qa := q.RGBA()

	return pr == qr && pg == qg && pb == qb && pa == qa
}

func TestDragSelectsAndHighlightsExactlyThoseCells(t *testing.T) {
	t.Parallel()
	a, _, f := shellsWith(t, sampleText, "RIGHTSIDE")
	c := contentRect(a.layoutRects()["p1"])

	drag(a, c.X, c.Y, c.X+4, c.Y, 0)

	if a.sel == nil || a.sel.pane != "p1" {
		t.Fatalf("sel = %+v, want a selection in p1", a.sel)
	}

	for x := c.X; x <= c.X+4; x++ {
		if !selBgAt(a, x, c.Y) {
			t.Errorf("cell (%d,%d) is not highlighted", x, c.Y)
		}
	}

	for _, cell := range [][2]int{{c.X + 5, c.Y}, {c.X, c.Y + 1}, {c.X + 4, c.Y + 1}} {
		if selBgAt(a, cell[0], cell[1]) {
			t.Errorf("cell %v is highlighted, want plain", cell)
		}
	}

	if got := copyNow(a, f); got != "hello" {
		t.Errorf("copied %q, want %q", got, "hello")
	}

	if got := a.store.State().Tab().Focus; got != "p1" {
		t.Errorf("focus = %q, want p1 (a press focuses the pane)", got)
	}
}

func TestDragThatLeavesItsPaneStaysClamped(t *testing.T) {
	t.Parallel()

	r := contentRectOf
	tests := []struct {
		name string
		to   func(a *App) (x, y int)
		want string
		pad  bool // the rows below the text are blank lines up to the pane's last row
	}{
		{"into the neighbour", func(a *App) (int, int) {
			c := r(a, "p2")

			return c.X + 3, r(a, "p1").Y + 1
		}, "hello world\nsecond line", false},
		{"onto the header", func(a *App) (int, int) {
			c := r(a, "p2")

			return c.X + 1, c.Y - 1
		}, "hello world", false},
		{"onto its right border", func(a *App) (int, int) {
			c := r(a, "p1")

			return c.X + c.W, c.Y
		}, "hello world", false},
		{"onto its bottom border", func(a *App) (int, int) {
			c := r(a, "p1")

			return c.X + 2, c.Y + c.H
		}, "hello world\nsecond line\nthird", true},
		{"onto the tab bar", func(a *App) (int, int) {
			return r(a, "p1").X + 2, 0
		}, "hel", false},
		{"off screen", func(a *App) (int, int) {
			return -9, -9
		}, "h", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			a, _, f := shellsWith(t, sampleText, "RIGHTSIDE")
			c := r(a, "p1")
			x, y := tt.to(a)

			drag(a, c.X, c.Y, x, y, 0)

			got := copyNow(a, f)
			if strings.Contains(got, "RIGHT") {
				t.Fatalf("copied %q, includes the neighbour", got)
			}

			want := tt.want
			if tt.pad {
				want += strings.Repeat("\n", c.H-3)
			}

			if got != want {
				t.Errorf("copied %q, want %q", got, want)
			}

			n := r(a, "p2")
			buf := drawnBuffer(a)

			for y := n.Y; y < n.Y+n.H; y++ {
				for x := n.X; x < n.X+n.W; x++ {
					if selBgIn(a, buf, x, y) {
						t.Fatalf("neighbour cell (%d,%d) is highlighted", x, y)
					}
				}
			}
		})
	}
}

func contentRectOf(a *App, id core.PaneID) core.Rect { return contentRect(a.layoutRects()[id]) }

func trackingShells(t *testing.T) (*App, map[core.PaneID]*fakeProc, *fakeRun) {
	t.Helper()

	a, procs, f := shellsWith(t, sampleText, "")
	feedAndWait(t, a.panes["p1"], procs["p1"], "\x1b[?1000h\x1b[?1006h")

	return a, procs, f
}

// settle sends a wheel notch and waits for its bytes: everything forwarded
// before it has reached the process by then.
func settle(t *testing.T, a *App, fp *fakeProc, c core.Rect) string {
	t.Helper()

	wheelAt(a, c.X, c.Y, tea.MouseWheelUp)

	want := "\x1b[<64;1;1M"

	eventually(t, "probe wheel", func() bool { return strings.HasSuffix(fp.sent(), want) })

	return strings.TrimSuffix(fp.sent(), want)
}

func TestClickInTrackingPaneIsForwardedOnRelease(t *testing.T) {
	t.Parallel()
	a, procs, _ := trackingShells(t)
	c := contentRectOf(a, "p1")

	mdown(a, c.X+4, c.Y+2, tea.MouseLeft, 0)

	if got := settle(t, a, procs["p1"], c); got != "" {
		t.Fatalf("sent %q on press, want nothing until release", got)
	}

	mup(a, c.X+4, c.Y+2, tea.MouseLeft)

	want := "\x1b[<0;5;3M\x1b[<0;5;3m"

	eventually(t, "click", func() bool { return strings.Contains(procs["p1"].sent(), want) })

	if a.sel != nil {
		t.Errorf("a plain click made a selection %+v", a.sel)
	}

	if got := a.store.State().Tab().Focus; got != "p1" {
		t.Errorf("focus = %q, want p1", got)
	}
}

func TestDragInTrackingPaneSelectsAndSendsNothing(t *testing.T) {
	t.Parallel()
	a, procs, f := trackingShells(t)
	c := contentRectOf(a, "p1")

	drag(a, c.X, c.Y, c.X+4, c.Y, 0)

	if got := settle(t, a, procs["p1"], c); got != "" {
		t.Errorf("sent %q for a drag, want nothing", got)
	}

	if got := copyNow(a, f); got != "hello" {
		t.Errorf("copied %q, want hello", got)
	}
}

func TestRightButtonIsForwardedAndNeverSelects(t *testing.T) {
	t.Parallel()
	a, procs, _ := trackingShells(t)
	c := contentRectOf(a, "p1")

	mdown(a, c.X+1, c.Y+1, tea.MouseRight, 0)
	mmove(a, c.X+5, c.Y+1, tea.MouseRight)
	mup(a, c.X+5, c.Y+1, tea.MouseRight)

	eventually(t, "right press", func() bool { return strings.Contains(procs["p1"].sent(), "\x1b[<2;2;2M") })
	eventually(t, "right release", func() bool { return strings.Contains(procs["p1"].sent(), "\x1b[<2;6;2m") })

	if a.sel != nil {
		t.Errorf("a right-button drag selected %+v", a.sel)
	}
}

func TestNewPressReleasesAHeldForwardedButton(t *testing.T) {
	t.Parallel()
	a, procs, _ := trackingShells(t)
	c := contentRectOf(a, "p1")

	mdown(a, c.X+1, c.Y+1, tea.MouseMiddle, 0)
	mdown(a, c.X+3, c.Y+2, tea.MouseLeft, 0)

	eventually(t, "middle release", func() bool { return strings.Contains(procs["p1"].sent(), "\x1b[<1;2;2m") })

	if a.mouse.fwd != nil {
		t.Errorf("forwarded button still held: %+v", *a.mouse.fwd)
	}
}

func TestModalOpeningReleasesAHeldForwardedButton(t *testing.T) {
	t.Parallel()
	a, procs, _ := trackingShells(t)
	c := contentRectOf(a, "p1")

	mdown(a, c.X+1, c.Y+1, tea.MouseRight, 0)
	a.pending = true
	mmove(a, c.X+4, c.Y+1, tea.MouseRight)

	eventually(t, "right release", func() bool { return strings.Contains(procs["p1"].sent(), "\x1b[<2;2;2m") })

	if a.mouse.fwd != nil {
		t.Errorf("forwarded button still held: %+v", *a.mouse.fwd)
	}
}

func TestDoubleAndTripleClickSelectWordAndLine(t *testing.T) {
	t.Parallel()
	a, _, f := shellsWith(t, sampleText, "")
	clk := &fakeClock{t: time.Unix(1000, 0)}
	a.clock = clk
	c := contentRectOf(a, "p1")
	x, y := c.X+8, c.Y

	click(a, x, y)

	if a.sel != nil {
		t.Fatalf("one click selected %+v", a.sel)
	}

	clk.Advance(100 * time.Millisecond)
	click(a, x, y)

	if got := copyNow(a, f); got != "world" {
		t.Errorf("double click copied %q, want world", got)
	}

	clk.Advance(100 * time.Millisecond)
	click(a, x, y)

	f.mu.Lock()
	f.calls = nil
	f.mu.Unlock()

	if got := copyNow(a, f); got != "hello world" {
		t.Errorf("triple click copied %q, want the line", got)
	}

	clk.Advance(100 * time.Millisecond)
	click(a, x, y)

	if a.sel != nil {
		t.Errorf("a fourth click starts again at one and clears; sel = %+v", a.sel)
	}
}

func TestSlowOrMovedClicksDoNotCount(t *testing.T) {
	t.Parallel()
	a, _, _ := shellsWith(t, sampleText, "")
	clk := &fakeClock{t: time.Unix(1000, 0)}
	a.clock = clk
	c := contentRectOf(a, "p1")

	click(a, c.X+8, c.Y)
	clk.Advance(500 * time.Millisecond)
	click(a, c.X+8, c.Y)

	if a.sel != nil {
		t.Errorf("clicks 500 ms apart selected %+v", a.sel)
	}

	clk.Advance(100 * time.Millisecond)
	click(a, c.X+9, c.Y)

	if a.sel != nil {
		t.Errorf("clicks on different cells selected %+v", a.sel)
	}
}

func TestAltDragSelectsABlock(t *testing.T) {
	t.Parallel()
	a, _, f := shellsWith(t, "abcdef\r\nghijkl\r\nmnopqr", "")
	c := contentRectOf(a, "p1")

	drag(a, c.X+1, c.Y, c.X+3, c.Y+2, tea.ModAlt)

	if got := copyNow(a, f); got != "bcd\nhij\nnop" {
		t.Errorf("copied %q, want the block", got)
	}
}

func TestTabClickSwitchesTab(t *testing.T) {
	t.Parallel()
	a, _, _ := shellsWith(t, sampleText, "")

	if err := a.dispatch(core.NewTab{Kind: core.KindShell, Cwd: "/w/c"}); err != nil {
		t.Fatal(err)
	}

	if got := a.store.State().Active; got != 1 {
		t.Fatalf("active = %d, want 1", got)
	}

	plan := planBar(a.tabInfos(), 1, a.trayModules(), a.width, a.icons)
	x := segWidth(a.tabInfos()[plan.tabs[0].idx], plan.tabs[0].level, a.icons) + 1 - 2

	click(a, x, 0)

	if got := a.store.State().Active; got != 0 {
		t.Errorf("active = %d after clicking tab 1, want 0", got)
	}

	// The edge cell after the segment is not a tab.
	edge := segWidth(a.tabInfos()[0], plan.tabs[0].level, a.icons)
	click(a, edge, 0)

	if got := a.store.State().Active; got != 0 {
		t.Errorf("active = %d after clicking the edge, want 0", got)
	}
}

func TestCopyWithoutSelectionDoesNothing(t *testing.T) {
	t.Parallel()
	a, _, f := shellsWith(t, sampleText, "")

	if got := copyNow(a, f); got != "" {
		t.Errorf("copied %q with no selection", got)
	}
}

func TestGuestOSC52ReachesTheClipboard(t *testing.T) {
	t.Parallel()
	a, procs, f := shellsWith(t, sampleText, "")
	pm := newPump(t)

	osc := "\x1b]52;c;" + base64.StdEncoding.EncodeToString([]byte("first")) + "\x07" +
		"\x1b]52;c;" + base64.StdEncoding.EncodeToString([]byte("from the guest")) + "\x07"
	feedAndWait(t, a.panes["p1"], procs["p1"], osc)

	_, cmd := a.Update(paneDirtyMsg{id: "p1", p: a.panes["p1"]})
	pm.run(cmd)

	eventually(t, "native write", func() bool { return copiedText(f) != "" })

	if got := copiedText(f); got != "from the guest" {
		t.Errorf("copied %q, want the last write of the chunk", got)
	}
}

func TestGuestOSC52FromAHiddenTabReachesTheClipboard(t *testing.T) {
	t.Parallel()
	a, procs, f := shellsWith(t, sampleText, "")
	pm := newPump(t)

	if err := a.dispatch(core.NewTab{Kind: core.KindShell, Cwd: "/w/c"}); err != nil {
		t.Fatal(err)
	}

	osc := "\x1b]52;c;" + base64.StdEncoding.EncodeToString([]byte("hidden")) + "\x07"
	feedAndWait(t, a.panes["p1"], procs["p1"], osc)

	_, cmd := a.Update(paneDirtyMsg{id: "p1", p: a.panes["p1"]})
	pm.run(cmd)

	eventually(t, "native write", func() bool { return copiedText(f) != "" })
}

// modalCases opens each kind of UI that owns the pointer.
func modalCases() []struct {
	name string
	open func(a *App)
} {
	return []struct {
		name string
		open func(a *App)
	}{
		{"confirm", func(a *App) {
			a.openConfirm(confirm{action: "x", prompt: "x?", yes: func(*App) tea.Cmd { return nil }})
		}},
		{"prompt", func(a *App) { a.renameTab() }},
		{"checks", func(a *App) { a.openChecks(prRepoKey, 7) }},
		{"palette", func(a *App) { a.openCommandPalette() }},
		{"leader pending", func(a *App) { press(a, keyLeader) }},
		{"which-key popup", func(a *App) {
			press(a, keyLeader)
			a.popup = true
		}},
	}
}

func TestMouseIsIgnoredUnderModalUI(t *testing.T) {
	t.Parallel()

	for _, m := range modalCases() {
		t.Run(m.name, func(t *testing.T) {
			a, procs, _ := trackingShells(t)
			c := contentRectOf(a, "p1")

			if err := a.dispatch(core.NewTab{Kind: core.KindShell, Cwd: "/w/c"}); err != nil {
				t.Fatal(err)
			}

			if err := a.dispatch(core.SelectTab{Index: 0}); err != nil {
				t.Fatal(err)
			}

			if err := a.dispatch(core.FocusPane{ID: "p2"}); err != nil {
				t.Fatal(err)
			}

			m.open(a)

			drag(a, c.X, c.Y, c.X+4, c.Y, 0)
			click(a, c.X+1, c.Y+1)
			wheelAt(a, c.X+1, c.Y+1, tea.MouseWheelUp)
			click(a, 1, 0)

			if a.sel != nil {
				t.Errorf("a selection appeared: %+v", a.sel)
			}

			if got := a.store.State().Tab().Focus; got != "p2" {
				t.Errorf("focus = %q, want p2", got)
			}

			if got := a.store.State().Active; got != 0 {
				t.Errorf("active tab = %d, want 0", got)
			}

			// Drop the modal so the probe is not swallowed, then check nothing was sent.
			a.pending, a.popup, a.confirm, a.prompt, a.overlay, a.checks = false, false, nil, nil, nil, nil
			if got := settle(t, a, procs["p1"], c); got != "" {
				t.Errorf("sent %q to the guest", got)
			}
		})
	}
}

func TestKeyToTheSelectionPaneClearsIt(t *testing.T) {
	t.Parallel()
	a, _, _ := shellsWith(t, sampleText, "")
	c := contentRectOf(a, "p1")

	drag(a, c.X, c.Y, c.X+4, c.Y, 0)
	press(a, keyLeader, keyRune('y'))
	press(a, keyLeader, keyRune('w'))

	if a.sel == nil {
		t.Fatal("leader commands cleared the selection")
	}

	press(a, keyRune('x'))

	if a.sel != nil {
		t.Errorf("sel = %+v after a key to its pane, want nil", a.sel)
	}
}

func TestKeyToAnotherPaneKeepsTheSelection(t *testing.T) {
	t.Parallel()
	a, _, _ := shellsWith(t, sampleText, "")
	c := contentRectOf(a, "p1")

	drag(a, c.X, c.Y, c.X+4, c.Y, 0)

	if err := a.dispatch(core.FocusPane{ID: "p2"}); err != nil {
		t.Fatal(err)
	}

	press(a, keyRune('x'))

	if a.sel == nil {
		t.Error("a key to p2 cleared p1's selection")
	}
}

func TestPasteToTheSelectionPaneClearsIt(t *testing.T) {
	t.Parallel()
	a, _, _ := shellsWith(t, sampleText, "")
	c := contentRectOf(a, "p1")

	drag(a, c.X, c.Y, c.X+4, c.Y, 0)
	a.Update(tea.PasteMsg{Content: "pasted"})

	if a.sel != nil {
		t.Errorf("sel = %+v after a paste, want nil", a.sel)
	}
}

func TestClickClearsAndDragReplaces(t *testing.T) {
	t.Parallel()
	a, _, f := shellsWith(t, sampleText, "RIGHTSIDE")
	c := contentRectOf(a, "p1")

	drag(a, c.X, c.Y, c.X+4, c.Y, 0)
	click(a, contentRectOf(a, "p2").X+1, c.Y)

	if a.sel != nil {
		t.Errorf("a click in another pane left %+v", a.sel)
	}

	drag(a, c.X, c.Y, c.X+4, c.Y, 0)
	drag(a, c.X+6, c.Y, c.X+10, c.Y, 0)

	if got := copyNow(a, f); got != "world" {
		t.Errorf("copied %q, want the second drag's text", got)
	}
}

func TestSelectionSurvivesTabSwitchAndIsHiddenMeanwhile(t *testing.T) {
	t.Parallel()
	a, _, _ := shellsWith(t, sampleText, "")
	c := contentRectOf(a, "p1")

	drag(a, c.X, c.Y, c.X+4, c.Y, 0)

	if err := a.dispatch(core.NewTab{Kind: core.KindShell, Cwd: "/w/c"}); err != nil {
		t.Fatal(err)
	}

	if a.sel == nil {
		t.Fatal("a tab switch cleared the selection")
	}

	if err := a.dispatch(core.SelectTab{Index: 0}); err != nil {
		t.Fatal(err)
	}

	if !selBgAt(a, c.X, c.Y) {
		t.Error("selection is not drawn after returning to its tab")
	}
}

func TestClosingThePaneClearsItsSelection(t *testing.T) {
	t.Parallel()
	a, _, _ := shellsWith(t, sampleText, "")
	c := contentRectOf(a, "p1")

	drag(a, c.X, c.Y, c.X+4, c.Y, 0)
	a.closePane("p1")

	if a.sel != nil {
		t.Errorf("sel = %+v after its pane closed", a.sel)
	}
}

func TestResizeAndAltScreenClearTheSelection(t *testing.T) {
	t.Parallel()
	t.Run("resize", func(t *testing.T) {
		a, _, _ := shellsWith(t, sampleText, "")
		c := contentRectOf(a, "p1")

		drag(a, c.X, c.Y, c.X+4, c.Y, 0)
		a.Update(tea.WindowSizeMsg{Width: 70, Height: 16})

		if a.sel != nil {
			t.Errorf("sel = %+v after a resize", a.sel)
		}
	})

	t.Run("alt screen", func(t *testing.T) {
		a, procs, _ := shellsWith(t, sampleText, "")
		c := contentRectOf(a, "p1")

		drag(a, c.X, c.Y, c.X+4, c.Y, 0)
		feedAndWait(t, a.panes["p1"], procs["p1"], "\x1b[?1049h")
		a.renderPane(a.panes["p1"])

		if a.sel != nil {
			t.Errorf("sel = %+v after the alt screen opened", a.sel)
		}
	})
}

// A click on a pane's top border focuses it, as its header did; its side and
// bottom borders do nothing of their own, and no click there reaches the guest.
func TestPaneBorderClicks(t *testing.T) {
	t.Parallel()
	a, _, _ := trackingShells(t)
	r := a.layoutRects()["p1"]
	c := contentRect(r)

	tests := []struct {
		name     string
		x, y     int
		wantPane core.PaneID
	}{
		{"left border", r.X, c.Y + 1, "p2"},
		{"right border", r.X + r.W - 1, c.Y + 1, "p2"},
		{"bottom border", c.X + 2, r.Y + r.H - 1, "p2"},
		{"bottom corner", r.X, r.Y + r.H - 1, "p2"},
		{"top border", c.X + 2, r.Y, "p1"},
		{"top corner", r.X + r.W - 1, r.Y, "p1"},
		{"content", c.X + 2, c.Y + 1, "p1"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			a, procs, _ := trackingShells(t)
			if err := a.dispatch(core.FocusPane{ID: "p2"}); err != nil {
				t.Fatal(err)
			}

			click(a, tt.x, tt.y)

			if got := a.store.State().Tab().Focus; got != tt.wantPane {
				t.Errorf("focus = %q, want %q", got, tt.wantPane)
			}

			if a.sel != nil {
				t.Errorf("click selected %+v", a.sel)
			}

			if tt.name != "content" {
				if got := settle(t, a, procs["p1"], c); got != "" {
					t.Errorf("border click reached the guest: %q", got)
				}
			}
		})
	}
}

func TestHitAreas(t *testing.T) {
	t.Parallel()
	a, _, _ := shellsWith(t, sampleText, "")
	r := a.layoutRects()["p1"]
	c := contentRect(r)

	tests := []struct {
		name string
		x, y int
		area hitArea
		pane core.PaneID
	}{
		{"content", c.X + 2, c.Y + 1, hitContent, "p1"},
		{"first content cell", c.X, c.Y, hitContent, "p1"},
		{"last content cell", c.X + c.W - 1, c.Y + c.H - 1, hitContent, "p1"},
		{"top border", r.X + 1, r.Y, hitHeader, "p1"},
		{"top corner", r.X, r.Y, hitHeader, "p1"},
		{"left border", r.X, c.Y, hitNone, ""},
		{"right border", r.X + r.W - 1, c.Y, hitNone, ""},
		{"bottom border", c.X, r.Y + r.H - 1, hitNone, ""},
		{"outside the screen on the left", r.X - 1, r.Y, hitNone, ""},
		{"tab", 2, 0, hitTab, ""},
		{"off screen", -1, -1, hitNone, ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h := a.hit(tt.x, tt.y)
			if h.area != tt.area || h.pane != tt.pane {
				t.Errorf("hit(%d,%d) = %+v, want area %v pane %q", tt.x, tt.y, h, tt.area, tt.pane)
			}
		})
	}

	if h := a.hit(c.X+2, c.Y+1); h.local != (point{row: 1, col: 2}) {
		t.Errorf("local = %+v, want row 1 col 2", h.local)
	}
}

func TestModalOpeningMidDragDropsTheHeldButton(t *testing.T) {
	t.Parallel()
	a, _, _ := shellsWith(t, sampleText, "")
	c := contentRectOf(a, "p1")

	mdown(a, c.X, c.Y, tea.MouseLeft, 0)
	a.openConfirm(confirm{action: "x", prompt: "x?", yes: func(*App) tea.Cmd { return nil }})
	mup(a, c.X, c.Y, tea.MouseLeft)

	if a.mouse.press != nil || a.mouse.fwd != nil {
		t.Errorf("held button survived a modal: %+v %+v", a.mouse.press, a.mouse.fwd)
	}
}

// drawnRow is the text on screen row y across the content rect c.
func drawnRow(a *App, c core.Rect, y int) string {
	buf := uv.NewScreenBuffer(a.width, a.height)
	uv.NewStyledString(a.View().Content).Draw(buf, buf.Bounds())

	var sb strings.Builder

	for x := c.X; x < c.X+c.W; x++ {
		if cell := buf.CellAt(x, y); cell != nil && cell.Content != "" {
			sb.WriteString(cell.Content)
		} else {
			sb.WriteByte(' ')
		}
	}

	return strings.TrimRight(sb.String(), " ")
}

func TestHighlightFollowsDrawnRowsDuringSyncHold(t *testing.T) {
	t.Parallel()

	// A fake clock fixed at the hold's start keeps the hold open.
	a := newTestApp(t)
	a.clock = &fakeClock{t: t0}

	a, procs := framedAppOn(t, a, 60, 14, shellSpec("/w/a", sampleText), shellSpec("/w/b", ""))
	fakeClip(a)
	p := a.panes["p1"]
	c := contentRectOf(a, "p1")

	drag(a, c.X, c.Y, c.X+4, c.Y, 0)
	feedAndWait(t, p, procs["p1"], "\x1b[?2026h\x1b[1;1HXXXXXXXXXXX")
	a.renderPane(p)

	if got := drawnRow(a, c, c.Y); got != "hello world" {
		t.Fatalf("selected row drawn as %q, want the held frame %q", got, "hello world")
	}

	for x := c.X; x <= c.X+4; x++ {
		if !selBgAt(a, x, c.Y) {
			t.Errorf("cell (%d,%d) lost its highlight", x, c.Y)
		}
	}
}

func TestHighlightStaysOnDrawnTextWhenOutputIsNotRenderedYet(t *testing.T) {
	t.Parallel()
	a, procs, _ := shellsWith(t, manyLines(30), "")
	p := a.panes["p1"]
	c := contentRectOf(a, "p1")

	want := drawnRow(a, c, c.Y+2)

	drag(a, c.X, c.Y+2, c.X+5, c.Y+2, 0)
	feedAndWait(t, p, procs["p1"], manyLines(5))

	// No render: the rows drawn are the old ones, and so must be the highlight.
	if got := drawnRow(a, c, c.Y+2); got != want {
		t.Fatalf("row drawn as %q, want %q", got, want)
	}

	for x := c.X; x <= c.X+5; x++ {
		if !selBgAt(a, x, c.Y+2) {
			t.Errorf("cell (%d,%d) lost its highlight", x, c.Y+2)
		}
	}

	if selBgAt(a, c.X, c.Y+3) || selBgAt(a, c.X, c.Y+1) {
		t.Error("the highlight spilled onto a neighbouring row")
	}
}

func TestMultiClickInTrackingPaneSelectsAndForwardsOnlyTheFirstClick(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		clicks int
		want   string
	}{
		{"double", 2, "world"},
		{"triple", 3, "hello world"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			a, procs, f := trackingShells(t)
			a.clock = &fakeClock{t: time.Unix(1000, 0)}
			c := contentRectOf(a, "p1")

			for range tt.clicks {
				click(a, c.X+8, c.Y)
				a.clock.(*fakeClock).Advance(100 * time.Millisecond)
			}

			if got := copyNow(a, f); got != tt.want {
				t.Errorf("copied %q, want %q", got, tt.want)
			}

			one := "\x1b[<0;9;1M\x1b[<0;9;1m"
			if got := settle(t, a, procs["p1"], c); got != one {
				t.Errorf("guest got %q, want exactly the first click %q", got, one)
			}
		})
	}
}

func TestSelectionClearsWhenItsTextIsRedrawn(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		pane core.PaneID
		out  string
		kept bool
	}{
		{"rewrite inside", "p1", "\x1b[1;1Hjello", false},
		{"same text in bold", "p1", "\x1b[1;1H\x1b[1mhello\x1b[0m", true},
		{"rest of the row", "p1", "\x1b[1;7HWORLD", true},
		{"another row", "p1", "\x1b[3;1HTHIRD", true},
		{"another pane", "p2", "\x1b[1;1Hjello", true},
		{"output scrolls", "p1", "\r\n" + manyLines(20), true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			a, procs, f := shellsWith(t, sampleText, "RIGHT")
			c := contentRectOf(a, "p1")

			drag(a, c.X, c.Y, c.X+4, c.Y, 0)
			feedAndWait(t, a.panes[tt.pane], procs[tt.pane], tt.out)
			a.Update(paneDirtyMsg{id: tt.pane, p: a.panes[tt.pane]})

			if !tt.kept {
				if a.sel != nil {
					t.Error("selection kept, want cleared")
				}

				return
			}

			if a.sel == nil {
				t.Fatal("selection cleared, want kept")
			}

			if got := copyNow(a, f); got != "hello" {
				t.Errorf("copied %q, want hello", got)
			}
		})
	}
}

func TestSelectionAcrossHistoryAndScreen(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		out  func(endRow int) string
		kept bool
	}{
		{"output scrolls", func(int) string { return "\r\n" + manyLines(5) }, true},
		{"live part rewritten", func(endRow int) string { return fmt.Sprintf("\x1b[%d;1HXXXXX", endRow) }, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			a, fp, f := copyApp(t)
			c := contentRectOf(a, "p1")
			p := a.panes["p1"]

			wheelAt(a, c.X, c.Y, tea.MouseWheelUp)
			drag(a, c.X, c.Y, c.X+4, c.Y+c.H-1, 0)

			want := copyNow(a, f)

			// The drag ends on a live-screen row; CUP rows are 1-based.
			endRow := int(a.sel.head.row-a.sel.watchFrom) + 1

			feedAndWait(t, p, fp, tt.out(endRow))
			a.Update(paneDirtyMsg{id: "p1", p: p})

			if kept := a.sel != nil; kept != tt.kept {
				t.Fatalf("selection kept = %v, want %v", kept, tt.kept)
			}

			if !tt.kept {
				return
			}

			f.mu.Lock()
			f.calls = nil
			f.mu.Unlock()

			if got := copyNow(a, f); got != want {
				t.Errorf("after output copied %q, want %q", got, want)
			}
		})
	}
}

func TestSelectionClearsWhenItsScreenIsReprintedUnchanged(t *testing.T) {
	t.Parallel()
	a, fp, f := copyApp(t)
	c := contentRectOf(a, "p1")
	y := c.Y + c.H/2

	drag(a, c.X, y, c.X+5, y, 0)

	if got := copyNow(a, f); !strings.HasPrefix(got, "L000") {
		t.Fatalf("copied %q, want a row's L000xx text", got)
	}

	f.mu.Lock()
	f.calls = nil
	f.mu.Unlock()

	before, _ := a.panes["p1"].linesAt()
	before = append([]string(nil), before...)

	feedAndWait(t, a.panes["p1"], fp, "\x1b[2J\x1b[3J\x1b[H"+manyLines(30)+"abcdef")
	a.Update(paneDirtyMsg{id: "p1", p: a.panes["p1"]})

	after, _ := a.panes["p1"].linesAt()
	if !slices.Equal(before, after) {
		t.Fatalf("rows changed by the reprint:\n%q\nwant\n%q", after, before)
	}

	if a.sel != nil {
		t.Error("selection kept after its screen was reprinted, want cleared")
	}
}

func TestSelectionInHistorySurvivesOutput(t *testing.T) {
	t.Parallel()
	a, fp, f := copyApp(t)
	c := contentRectOf(a, "p1")

	wheelAt(a, c.X, c.Y, tea.MouseWheelUp)
	wheelAt(a, c.X, c.Y, tea.MouseWheelUp)
	drag(a, c.X, c.Y, c.X+4, c.Y, 0)

	want := copyNow(a, f)

	feedAndWait(t, a.panes["p1"], fp, "\x1b[2J\x1b[Hnew screen")
	a.Update(paneDirtyMsg{id: "p1", p: a.panes["p1"]})

	if a.sel == nil {
		t.Fatal("selection cleared, want kept")
	}

	f.mu.Lock()
	f.calls = nil
	f.mu.Unlock()

	if got := copyNow(a, f); got != want {
		t.Errorf("after output copied %q, want %q", got, want)
	}
}
