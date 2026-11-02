package ui

import (
	"fmt"
	"strconv"
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/mhersson/aim/internal/ui/theme"
)

// maxChecksWidth caps the checks overlay.
const maxChecksWidth = 70

// checkGlyph is the mark of a check; without icons plain symbols stand in.
func checkGlyph(k checkKind, icons bool) string {
	switch k {
	case checkFailed:
		if icons {
			return iconFailed
		}

		return "✗"
	case checkRunning:
		if icons {
			return iconRunning
		}

		return "…"
	}

	if icons {
		return iconPassed
	}

	return "✓"
}

// checkTone is the colour of a check's mark and whether its name is bold.
func checkTone(k checkKind, ui theme.UI) (fg theme.Hex, bold bool) {
	switch k {
	case checkFailed:
		return ui.Error, true
	case checkRunning:
		return ui.Warning, false
	case checkSkipped:
		return ui.Subtle, false
	}

	return ui.Success, false
}

// checksBox draws the checks overlay w cells wide, with room for at most room
// lines; it drops the blank rows and then the hint row before it clips a check.
func (a *App) checksBox(lk look, w, room int) []string {
	ui, st := lk.theme.UI, lk.styles
	panel := lipgloss.NewStyle().Background(ui.Panel).Foreground(ui.Fg)
	on := func(fg theme.Hex) lipgloss.Style { return panel.Foreground(fg) }

	state := a.store.State()
	v := a.checks
	checks := a.checksOf(state, v)
	rows := checkRows(checks, a.clock.Now())
	v.sel = min(max(v.sel, 0), max(len(rows)-1, 0))

	title := " #" + strconv.Itoa(v.number) + " checks "
	if a.icons {
		title = " " + iconPR + " #" + strconv.Itoa(v.number) + " checks "
	}

	top, row, bottom := boxFrame(w, title, boxStyle{border: st.popupBorder, title: st.pushTitle, cut: "…"}, st)
	cw := w - 5

	var body []string

	switch {
	case checks == nil:
		body = []string{row(on(ui.Muted).Render(noticeChecksLoading))}
	case checks.Err != "":
		for _, l := range wrapRows(fmt.Sprintf(noticeChecksUnread, checks.Err), cw, max(room-2, 1)) {
			body = append(body, row(on(ui.Muted).Render(l)))
		}
	case len(rows) == 0:
		head := checks.HeadOID[:min(shortOIDLen, len(checks.HeadOID))]
		body = []string{row(on(ui.Muted).Render(fmt.Sprintf(noticeChecksNone, printable(head))))}
	default:
		// Blank rows and hints go before a check does; a list taller than the box scrolls.
		count := len(rows)
		if count+2 > room {
			count = max(room-2, 1)
		}

		body = a.checkLines(rows, v.sel, count, cw, row, lk)
	}

	kt := a.keys.checks
	hints := popupHints(st, hintPairs([][2]string{
		{kt.hint("checks.next", "checks.prev"), "check"},
		{kt.hint("checks.log"), "log"},
		{kt.hint("checks.browser"), "browser"},
		{kt.hint("checks.refresh"), "refresh"},
		{kt.hint("checks.close"), "close"},
	}), cw)

	// The frame, two blank rows and the hints take five lines beyond the checks.
	full := len(body) + 5
	if hints == "" {
		full = len(body) + 4
	}

	switch {
	case full <= room:
		out := append([]string{top, row("")}, body...)
		out = append(out, row(""))

		if hints != "" {
			out = append(out, row(hints))
		}

		return append(out, bottom)
	case len(body)+2 <= room:
		return append(append([]string{top}, body...), bottom)
	}

	return append([]string{top}, body...)
}

// popupHints draws the key and label pairs a popup's hint row holds in cw cells,
// the pairs that do not fit dropped from the end.
func popupHints(st styles, pairs [][2]string, cw int) string {
	hints, used := "", 0

	for i, kv := range pairs {
		need := ansi.StringWidth(kv[0]) + 1 + ansi.StringWidth(kv[1])
		if i > 0 {
			need += 4
		}

		if used+need > cw {
			break
		}

		if i > 0 {
			hints += st.popup.Render("    ")
		}

		hints += st.popupKey.Render(kv[0]) + st.popupMuted.Render(" "+kv[1])
		used += need
	}

	return hints
}

// checkLines draws count check rows cw cells wide, scrolled so the selected one shows.
func (a *App) checkLines(rows []checkRow, sel, count, cw int, row func(string) string, lk look) []string {
	ui := lk.theme.UI
	panel := lipgloss.NewStyle().Background(ui.Panel).Foreground(ui.Fg)

	start := max(sel-count+1, 0)
	out := make([]string, 0, count)

	for i := start; i < min(start+count, len(rows)); i++ {
		r := rows[i]
		bg := ui.Panel

		if i == sel {
			bg = ui.Selection
		}

		with := func(fg theme.Hex, bold bool) lipgloss.Style { return panel.Background(bg).Foreground(fg).Bold(bold) }
		fg, bold := checkTone(r.kind, ui)

		marker := with(ui.Accent, true).Render(" ")
		if i == sel {
			marker = with(ui.Accent, true).Render("▎")
		}

		nameFg := ui.Fg
		if r.kind == checkSkipped {
			nameFg = ui.Muted
		}

		left := marker + with(fg, bold).Render(" "+checkGlyph(r.kind, a.icons)+" ")
		dur := r.duration
		nameW := cw - ansi.StringWidth(left) - ansi.StringWidth(dur) - 2

		if nameW < 4 {
			dur, nameW = "", cw-ansi.StringWidth(left)
		}

		name := ansi.Truncate(printable(r.name), max(nameW, 0), "…")
		gap := max(cw-ansi.StringWidth(left)-ansi.StringWidth(name)-ansi.StringWidth(dur), 0)

		line := left + with(nameFg, bold).Render(name) + with(ui.Fg, false).Render(strings.Repeat(" ", gap)) +
			with(ui.Subtle, false).Render(dur)

		out = append(out, row(line))
	}

	return out
}

// overlayChecks draws the checks overlay over base, centred with its top at a third
// of the height.
func (a *App) overlayChecks(f *frameLayers, base string, lk look) string {
	if a.checks.page != nil {
		return a.overlayCILog(f, base, lk)
	}

	w := min(maxChecksWidth, a.width-4)
	if w < 12 {
		return base
	}

	top := max(1, a.height/3)

	return overlayLines(f, base, a.width, a.height, top, a.checksBox(lk, w, max(a.height-1-top, 0)), w, nil)
}
