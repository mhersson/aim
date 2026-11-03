package ui

import (
	"strings"

	"github.com/charmbracelet/x/ansi"

	"github.com/mhersson/aim/internal/ui/theme"
)

// toastRows is the height of the toast box: a border, one text row, a border.
const toastRows = 3

// toastLevel is how a toast reads, and so its colour: a notice, a success or an error.
type toastLevel uint8

const (
	levelNotice toastLevel = iota
	levelSuccess
	levelError
	toastLevels
)

// colour is the level's border and icon colour.
func (l toastLevel) colour(ui theme.UI) theme.Hex {
	switch l {
	case levelSuccess:
		return ui.Success
	case levelError:
		return ui.Error
	}

	return ui.Warning
}

// icon is the level's glyph, drawn when icons are on.
func (l toastLevel) icon() string {
	switch l {
	case levelSuccess:
		return iconSuccess
	case levelError:
		return iconAlert
	}

	return iconInfo
}

// toastMark is the level a notice text was raised at.
type toastMark struct {
	text  string
	level toastLevel
}

// toast shows text as the toast at level, until the next key press.
func (a *App) toast(level toastLevel, text string) {
	a.notice, a.marked = text, toastMark{text: text, level: level}
}

// noticeLevel is the level of the notice on show. A notice assigned to a.notice
// directly is a plain notice, whatever level the one before it had.
func (a *App) noticeLevel() toastLevel {
	if a.notice != "" && a.notice == a.marked.text {
		return a.marked.level
	}

	return levelNotice
}

// toastLayout is the notice text as it is drawn and the box's left column,
// top row and width; ok is false when no box is drawn.
func toastLayout(width, height int, notice string, icons bool) (text string, x, y, w int, ok bool) {
	text = printable(notice)
	if text == "" || height-2 < toastRows {
		return "", 0, 0, 0, false
	}

	// Blanks around the text, and the icon with its own blank.
	chrome := 2
	if icons {
		chrome += 2
	}

	room := width - 2 - 2 - chrome
	if room < 1 {
		return "", 0, 0, 0, false
	}

	text = ansi.Truncate(text, room, "…")
	w = chrome + ansi.StringWidth(text) + 2

	return text, width - 1 - w, height - toastRows, w, true
}

// toastCovers reports whether the toast box for notice covers cell (cx, cy).
func toastCovers(width, height int, notice string, icons bool, cx, cy int) bool {
	_, x, y, w, ok := toastLayout(width, height, notice, icons)

	return ok && cx >= x && cx < x+w && cy >= y && cy < y+toastRows
}

// overlayToast draws the notice as a rounded box at the bottom right of the
// body, one cell from the right edge, its bottom border on the last row. It is
// drawn into the frame only, so it takes no keys and no mouse events. Nothing
// is drawn when the box does not fit the body.
func overlayToast(f *frameLayers, base string, width, height int, notice string, level toastLevel, icons bool, st styles) string {
	text, x, y, w, ok := toastLayout(width, height, notice, icons)
	if !ok {
		return base
	}

	inner := w - 2

	tone := st.toastTone[level]

	mid := tone.Render("│") + st.toastText.Render(" ")
	if icons {
		mid += tone.Render(level.icon()) + st.toastText.Render(" ")
	}

	mid += st.toastText.Render(text+" ") + tone.Render("│")

	lines := []string{
		tone.Render("╭" + strings.Repeat("─", inner) + "╮"),
		mid,
		tone.Render("╰" + strings.Repeat("─", inner) + "╯"),
	}

	return overlayAt(f, base, width, height, x, y, lines, nil)
}
