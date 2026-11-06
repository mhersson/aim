package ui

import (
	"fmt"
	"image"
	"slices"
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/mhersson/aim/internal/core"
	"github.com/mhersson/aim/internal/store"
	"github.com/mhersson/aim/internal/ui/palette"
	"github.com/mhersson/aim/internal/ui/theme"
)

// maxOverlayWidth caps the palette overlay width.
const maxOverlayWidth = 64

type overlayKind int

const (
	overlayCommands overlayKind = iota
	overlayThemes
)

// overlay is an open palette. It owns every key until it closes.
type overlay struct {
	kind  overlayKind
	items []palette.Item
	pal   palette.Model
}

// look is the theme with its derived styles and highlighted sample. The app
// draws with the committed look, or a preview look while the theme picker moves.
type look struct {
	theme  theme.Theme
	styles styles
	sample []string
}

func newLook(th theme.Theme) (look, error) {
	sample, err := highlight(welcomeSample, "go", th)
	if err != nil {
		return look{}, fmt.Errorf("highlight welcome sample: %w", err)
	}

	return look{theme: th, styles: newStyles(th), sample: sample}, nil
}

// current is the look to draw with.
func (a *App) current() look {
	if a.preview != nil {
		return *a.preview
	}

	return look{theme: a.theme, styles: a.styles, sample: a.sample}
}

// openOverlay replaces any open overlay and ends a pending leader sequence.
func (a *App) openOverlay(kind overlayKind, title string, items []palette.Item) *overlay {
	a.endLeader()
	a.preview = nil
	a.overlay = &overlay{kind: kind, items: items, pal: palette.New(title, items, a.theme)}

	return a.overlay
}

func (a *App) closeOverlay() {
	a.overlay, a.preview = nil, nil
}

func (a *App) openCommandPalette() {
	cmds := a.reg.all()

	items := make([]palette.Item, 0, len(cmds))

	for _, c := range cmds {
		if c.scope == "" {
			items = append(items, palette.Item{ID: c.id, Title: c.title, Hint: a.paletteHint(c.id), Icon: a.glyph(c.icon)})
		}
	}

	a.openOverlay(overlayCommands, a.titled(iconPalette, "Commands"), items)
}

// paletteHint is a command's leader sequence, else its first drawer key as
// "drawer: <key>", else "".
func (a *App) paletteHint(id string) string {
	if key, ok := a.keymap.keyFor(id); ok {
		return key
	}

	if key, ok := a.keys.drawer.first(id); ok {
		return "drawer: " + key
	}

	return ""
}

func (a *App) openThemePicker() {
	items := make([]palette.Item, len(a.themes))
	for i, th := range a.themes {
		items[i] = palette.Item{ID: th.Name, Title: th.Title}
	}

	o := a.openOverlay(overlayThemes, a.titled(iconTheme, "Theme"), items)
	o.pal = o.pal.WithEnterLabel("apply").WithHint("ctrl+s", "save")
	o.pal.Select(a.theme.Name)
}

// themeSavedMsg reports the theme picker's save of name to the config at path.
type themeSavedMsg struct {
	name, path string
	err        error
}

// saveTheme applies the theme picker's selection as enter does, and saves it
// as the config's theme off the update loop.
func (a *App) saveTheme() tea.Cmd {
	it, ok := a.overlay.pal.Selected()
	if !ok {
		return nil
	}

	a.closeOverlay()
	// dispatch logs its own failures.
	_ = a.dispatch(core.SetTheme{Name: it.ID})

	path := a.configPath

	return func() tea.Msg {
		return themeSavedMsg{name: it.ID, path: path, err: store.SaveTheme(path, it.ID)}
	}
}

// themeSaved toasts how the theme picker's save went; the theme applies either way.
func (a *App) themeSaved(msg themeSavedMsg) {
	where := shortPath(msg.path, "", a.getenv("HOME"))

	if msg.err != nil {
		a.log.Warn("save theme", "path", msg.path, "err", msg.err)
		a.toast(levelError, "could not save "+msg.name+" to "+where+": "+msg.err.Error())

		return
	}

	a.toast(levelSuccess, "saved "+msg.name+" to "+where)
}

// glyph returns icon, or "" when icons are off.
func (a *App) glyph(icon string) string {
	if !a.icons {
		return ""
	}

	return icon
}

// titled prefixes name with icon and a space, or is name alone with icons off.
func (a *App) titled(icon, name string) string {
	if !a.icons {
		return name
	}

	return icon + " " + name
}

// updateOverlay forwards a message to the open overlay's palette.
func (a *App) updateOverlay(msg tea.Msg) tea.Cmd {
	pal, cmd := a.overlay.pal.Update(msg)
	a.overlay.pal = pal

	return cmd
}

// chosen closes the overlay and applies the confirmed item.
func (a *App) chosen(id string) tea.Cmd {
	// Chosen messages arrive from goroutines; one meant for an earlier
	// overlay must not act on this one.
	if a.overlay == nil || !slices.ContainsFunc(a.overlay.items, func(it palette.Item) bool { return it.ID == id }) {
		return nil
	}

	kind := a.overlay.kind
	a.closeOverlay()

	switch kind {
	case overlayCommands:
		c, ok := a.reg.get(id)
		if !ok {
			a.log.Error("chosen command missing", "command", id)

			return nil
		}

		return c.run(a)
	case overlayThemes:
		// dispatch logs its own failures.
		_ = a.dispatch(core.SetTheme{Name: id})
	}

	return nil
}

// highlighted previews the theme picker's selection without touching the store.
// Messages arrive from goroutines, so a late one for another item is dropped.
func (a *App) highlighted(id string) {
	if a.overlay == nil || a.overlay.kind != overlayThemes {
		return
	}

	if it, ok := a.overlay.pal.Selected(); !ok || it.ID != id {
		return
	}

	th, err := a.themes.ByName(id)
	if err != nil {
		a.log.Warn("preview theme", "name", id, "err", err)

		return
	}

	lk, err := newLook(th)
	if err != nil {
		a.log.Error("preview theme", "name", id, "err", err)

		return
	}

	a.preview = &lk
	a.overlay.pal.SetTheme(th)
}

// overlayOver draws the palette over base, centred horizontally with its top
// at a quarter of the height. It is clipped to the body rows, never wrapped.
func overlayOver(f *frameLayers, base string, width, height int, pal palette.Model) string {
	pw := min(maxOverlayWidth, width-4)
	if pw < 2 || height < 2 {
		return base
	}

	return overlayLines(f, base, width, height, height/4, strings.Split(pal.View(pw), "\n"), pw, nil)
}

// overlayLines draws lines pw cells wide over base, centred horizontally with
// the top at row y, clipped to the body rows.
func overlayLines(f *frameLayers, base string, width, height, y int, lines []string, pw int, pics []picRow) string {
	if room := height - 1 - y; len(lines) > room {
		lines = lines[:max(room, 0)]
	}

	for i, l := range lines {
		lines[i] = ansi.Truncate(l, pw, "")
	}

	return overlayAt(f, base, width, height, (width-pw)/2, y, lines, pics)
}

// drawerReset ends every style and link a base row leaves open, so the
// drawer's cells start from nothing.
const drawerReset = "\x1b[m\x1b]8;;\x1b\\"

// overlayDrawer draws the drawer's lines over base. The drawer always reaches
// the right edge and spans the body's height, so each line replaces the end of
// one base row and the compositor's parse of the whole screen is not needed.
// Anything that shape does not cover goes through the compositor; spliced says
// which drew it.
func overlayDrawer(f *frameLayers, base string, width, height int, r core.Rect, lines []string, pics []picRow, fits bool) (out string, spliced bool) {
	if out, ok := spliceDrawer(base, width, height, r, lines, fits); ok {
		// The splice needs fits, so every line is r.W cells.
		recordBox(f, width, height, r.X, r.Y, r.W, len(lines), pics)

		return out, true
	}

	return overlayAt(f, base, width, height, r.X, r.Y, lines, pics), false
}

// drawDrawer draws the drawer over the frame and counts the frames the
// compositor drew instead of the splice, each about 3 ms dearer.
func (a *App) drawDrawer(f *frameLayers, base string, r core.Rect, lines []string, pics []picRow, fits bool) string {
	out, spliced := overlayDrawer(f, base, a.width, a.height, r, lines, pics, fits)
	if !spliced {
		a.drawer.fallbacks++
	}

	return out
}

// spliceDrawer replaces the end of each covered base row with the drawer's line.
// It reports false, drawing nothing, unless the cells it leaves are the
// compositor's: the lines must fit (see drawerDrawn), the drawer must reach the
// right edge inside the screen, and each covered row must end with nothing open
// and hold nothing uv carries from one cell to another (see carriesAcrossCells).
// It has no counterpart to overlayAt's bounds guard, which leaves a base wider
// than the screen undrawn: that differs only for a frame that is already broken.
func spliceDrawer(base string, width, height int, r core.Rect, lines []string, fits bool) (string, bool) {
	if !fits || len(lines) == 0 || width < 2 || height < 2 || r.X < 0 || r.X+r.W != width || r.Y < 0 || r.Y+len(lines) > height {
		return "", false
	}

	rows := strings.Split(base, "\n")
	if len(rows) != height {
		return "", false
	}

	for i, l := range lines {
		row := rows[r.Y+i]
		if !endsClean(row) || carriesAcrossCells(row) {
			return "", false
		}

		rows[r.Y+i] = cutRow(row, r.X) + l
	}

	return strings.Join(rows, "\n"), true
}

// carriesAcrossCells reports whether s holds something uv carries from one cell
// to another, which a cut row would change: an APC, DCS, SOS or PM string rides
// on the next cell drawn, and a carriage return moves the column back.
func carriesAcrossCells(s string) bool {
	return strings.Contains(s, "\r") || strings.Contains(s, "\x1b_") || strings.Contains(s, "\x1bP") ||
		strings.Contains(s, "\x1bX") || strings.Contains(s, "\x1b^")
}

// endsClean reports whether nothing is left open at the end of s, judged by its
// last style sequence (a reset) and its last link sequence (a close). A string
// with neither is clean.
func endsClean(s string) bool {
	if i := strings.LastIndex(s, "\x1b["); i >= 0 && !strings.HasPrefix(s[i:], "\x1b[m") && !strings.HasPrefix(s[i:], "\x1b[0m") {
		return false
	}

	if i := strings.LastIndex(s, "\x1b]8;"); i >= 0 && !strings.HasPrefix(s[i:], "\x1b]8;;\x1b\\") && !strings.HasPrefix(s[i:], "\x1b]8;;\a") {
		return false
	}

	return true
}

// cutRow is the first x cells of row, then what ends every style and link it
// left open. A wide character that crosses x leaves a blank in its own style
// and a row shorter than x ends in plain blanks, as the cell buffer leaves
// them. Everything after x is dropped, escape sequences too, so nothing from
// under the drawer reaches the terminal.
func cutRow(row string, x int) string {
	p := ansi.GetParser()
	defer ansi.PutParser(p)

	var state byte

	w := 0

	for i := 0; i < len(row); {
		_, width, n, next := ansi.DecodeSequence(row[i:], state, p)
		if width > 0 && w+width > x {
			return row[:i] + strings.Repeat(" ", x-w) + drawerReset
		}

		w += width
		i += n
		state = next
	}

	return row + drawerReset + strings.Repeat(" ", x-w)
}

// overlayAt draws lines over base with the top-left corner at x, y and records
// the box and pics (relative to x, y) in f, clipped to the lines and the screen.
func overlayAt(f *frameLayers, base string, width, height, x, y int, lines []string, pics []picRow) string {
	if width < 2 || height < 2 || len(lines) == 0 {
		return base
	}

	lines = lines[:max(min(len(lines), height-y), 0)]
	if len(lines) == 0 {
		return base
	}

	layer := lipgloss.NewLayer(strings.Join(lines, "\n")).X(x).Y(y).Z(1)

	comp := lipgloss.NewCompositor(lipgloss.NewLayer(base), layer)
	if comp.Bounds() != image.Rect(0, 0, width, height) {
		return base
	}

	recordLayer(f, width, height, x, y, lines, pics)

	return comp.Render()
}
