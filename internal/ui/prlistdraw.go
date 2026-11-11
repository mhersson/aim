package ui

import (
	"slices"
	"strconv"
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/mhersson/aim/internal/core"
	"github.com/mhersson/aim/internal/ui/theme"
)

// prListWidth is the list's width: the conversation box's (planBox), so a
// conversation drawn over the list hides its columns.
func prListWidth(width int) int {
	return min(planMaxBox, width-4)
}

// prListReview is the review column of a row: its words and colour.
func prListReview(pr core.PRInfo, ui theme.UI) (string, theme.Hex) {
	switch {
	case pr.Draft:
		return "draft", ui.Muted
	case pr.ReviewDecision == "APPROVED":
		return "approved", ui.Success
	case pr.ReviewDecision == "CHANGES_REQUESTED":
		return "changes", ui.Warning
	}

	return "no review yet", ui.Subtle
}

// prListBody draws the groups' headers and rows cw cells wide and gives the
// line the selected row is on.
func (a *App) prListBody(rows []prListRow, sel, cw int, byAuthor bool, lk look) (lines []string, selLine int) {
	ui := lk.theme.UI
	panel := lipgloss.NewStyle().Background(ui.Panel).Foreground(ui.Fg)
	prev := ""

	for i, r := range rows {
		if r.key.repo != prev {
			if prev != "" {
				lines = append(lines, "")
			}

			n := 0

			for _, o := range rows[i:] {
				if o.key.repo != r.key.repo {
					break
				}

				n++
			}

			lines = append(lines, prListHeader(prRepoName(r.key.repo), n, cw, panel, ui))
			prev = r.key.repo
		}

		if i == sel {
			selLine = len(lines)
		}

		lines = append(lines, a.prListRowLine(r, i == sel, byAuthor, cw, panel, ui))
	}

	return lines, selLine
}

// prListHeader is a repository's name, a rule and its count at the right.
func prListHeader(name string, count, cw int, panel lipgloss.Style, ui theme.UI) string {
	num := strconv.Itoa(count)
	name = ansi.Truncate(name, max(cw-ansi.StringWidth(num)-4, 1), "…")
	rule := max(cw-ansi.StringWidth(name)-ansi.StringWidth(num)-2, 0)

	return panel.Foreground(ui.Muted).Bold(true).Render(name) + panel.Render(" ") +
		panel.Foreground(ui.Border).Render(strings.Repeat("─", max(rule-1, 0))) + panel.Render(" ") +
		panel.Foreground(ui.Muted).Render(num)
}

// prListRowLine is one row: the number, title and branch at the left and the
// checks, review, author (when byAuthor) and age at the right, which give way
// to the title as the box narrows.
func (a *App) prListRowLine(r prListRow, selected, byAuthor bool, cw int, panel lipgloss.Style, ui theme.UI) string {
	bg := ui.Panel
	if selected {
		bg = ui.Selection
	}

	with := func(fg theme.Hex, bold bool) lipgloss.Style { return panel.Background(bg).Foreground(fg).Bold(bold) }

	marker := with(ui.Accent, true).Render(" ")
	if selected {
		marker = with(ui.Accent, true).Render("▎")
	}

	// The right end, widest first: each piece after the checks goes before the title is cut.
	type piece struct {
		text string
		fg   theme.Hex
		bold bool
	}

	var pieces []piece

	glyph, counts := prChecksOf(r.pr, a.icons, ui)
	if glyph.text != "" {
		pieces = append(pieces, piece{glyph.text + " " + counts.text, glyph.fg, glyph.bold})
	}

	words, fg := prListReview(r.pr, ui)
	pieces = append(pieces, piece{words, fg, false})

	if author := printable(r.pr.Author); byAuthor && author != "" {
		pieces = append(pieces, piece{"@" + author, ui.Subtle, false})
	}

	if age := railAge(r.pr.UpdatedAt, a.clock.Now()); age != "" {
		pieces = append(pieces, piece{age, ui.Subtle, false})
	}

	num := "#" + strconv.Itoa(r.key.number) + " "
	title := printable(r.pr.Title)
	branch := printable(r.pr.HeadBranch)

	// The title and its number keep at least 12 cells before a piece is dropped.
	right := func() int {
		w := 0

		for i, p := range pieces {
			w += ansi.StringWidth(p.text)
			if i > 0 {
				w += 2
			}
		}

		return w
	}

	for len(pieces) > 0 && cw-1-right()-2 < min(12, ansi.StringWidth(num+title)) {
		pieces = pieces[:len(pieces)-1]
	}

	leftW := cw - 1
	if len(pieces) > 0 {
		leftW -= right() + 2
	}

	leftW = max(leftW, 0)

	// The branch shows whole when both fit, else it takes at most a third of
	// the room, and goes when that is too little to read.
	branchW := ansi.StringWidth(branch)
	if ansi.StringWidth(num+title)+2+branchW > leftW {
		branchW = min(branchW, leftW/3)
	}

	if branchW < 4 && branchW < ansi.StringWidth(branch) {
		branchW = 0
	}

	titleW := leftW - ansi.StringWidth(num)
	if branchW > 0 {
		titleW -= branchW + 2
	}

	title = ansi.Truncate(title, max(titleW, 0), "…")

	left := with(ui.Fg, true).Render(num) + with(ui.Fg, false).Render(title)
	if branchW > 0 {
		left += with(ui.Fg, false).Render("  ") + with(ui.Dim, false).Render(ansi.Truncate(branch, branchW, "…"))
	}

	var tail strings.Builder

	for i, p := range pieces {
		if i > 0 {
			tail.WriteString(with(ui.Fg, false).Render("  "))
		}

		tail.WriteString(with(p.fg, p.bold).Render(p.text))
	}

	gap := max(cw-1-ansi.StringWidth(left)-ansi.StringWidth(tail.String()), 0)

	return marker + left + with(ui.Fg, false).Render(strings.Repeat(" ", gap)) + tail.String()
}

// prListBox draws the list w cells wide with room for at most room lines. A
// list taller than that scrolls; the blank rows and then the hint row go before
// a pull request does.
func (a *App) prListBox(lk look, w, room int) []string {
	ui, st := lk.theme.UI, lk.styles
	panel := lipgloss.NewStyle().Background(ui.Panel).Foreground(ui.Fg)
	on := func(fg theme.Hex) lipgloss.Style { return panel.Foreground(fg) }

	v := a.prList
	state := a.store.State()
	rows := a.prListRowsOf(state, v.tab)
	sel := v.selected(rows)

	title := " Pull requests "
	if a.icons {
		title = " " + iconPR + " Pull requests "
	}

	top, row, bottom := boxFrame(w, title, boxStyle{border: st.popupBorder, title: st.pushTitle, cut: "…"}, st)
	cw := w - 5

	hint := func(key, label string) string { return st.popupKey.Render(key) + st.popupMuted.Render(" "+label) }
	kt := a.keys.prList
	tabs := a.prListTabRow(state, cw, panel, hint, ui)

	var body []string

	selLine := 0

	if len(rows) > 0 {
		var lines []string

		lines, selLine = a.prListBody(rows, sel, cw, v.tab != prTabAgents, lk)
		for _, l := range lines {
			body = append(body, row(l))
		}
	}

	// cut maps a note row that continues on the next row to its text, so a
	// window that ends there can show an ellipsis.
	cut := map[int]string{}

	for _, t := range a.prListNotes(v.tab, len(rows)) {
		lines := strings.Split(ansi.Wrap(t, max(cw, 1), ""), "\n")
		for i, l := range lines {
			if i < len(lines)-1 {
				cut[len(body)] = l
			}

			body = append(body, row(on(ui.Subtle).Render(l)))
		}
	}

	ready := ""
	if sel >= 0 && rows[sel].pr.Draft {
		ready = kt.hint("prlist.ready")
	}

	pairs := hintPairs([][2]string{
		{kt.hint("prlist.show"), "diff"},
		{kt.hint("prlist.conversation"), "conversation"},
		{kt.hint("prlist.checks"), "checks"},
		{ready, "ready"},
		{kt.hint("prlist.approve"), "approve"},
		{kt.hint("prlist.merge"), "merge"},
		{kt.hint("prlist.browser"), "browser"},
		{kt.hint("prlist.refresh"), "refresh"},
		{kt.hint("prlist.close"), "close"},
	})

	// esc close always stays; the other pairs drop from the end.
	var closing [][2]string
	if n := len(pairs); n > 0 && pairs[n-1][1] == "close" {
		closing, pairs = pairs[n-1:], pairs[:n-1]
	}

	const gap = 4

	hroom := cw
	if len(closing) > 0 {
		hroom -= ansi.StringWidth(closing[0][0]) + 1 + ansi.StringWidth(closing[0][1]) + gap
	}

	hints := popupHints(st, pairs, hroom)
	if hints != "" && len(closing) > 0 {
		hints += st.popup.Render(strings.Repeat(" ", gap))
	}

	hints += popupHints(st, closing, cw)

	// The fullest frame the room holds around the whole list; else the list scrolls.
	frames := []struct{ airy, hints bool }{{true, true}, {false, true}, {false, false}}
	if hints == "" {
		frames = frames[2:]
	}

	pick := frames[len(frames)-1]
	chosen := false

	overhead := func(f struct{ airy, hints bool }) int {
		n := 4 // the frame, the tab row and the blank row under it
		if f.airy {
			n += 2 // the blank rows above the tabs and above the hints
		}

		if f.hints {
			n++
		}

		return n
	}

	for _, f := range frames {
		if overhead(f)+len(body) <= room {
			pick, chosen = f, true

			break
		}
	}

	if !chosen {
		for _, f := range frames {
			if room-overhead(f) >= 3 {
				pick = f

				break
			}
		}
	}

	if win := max(room-overhead(pick), 1); len(body) > win {
		start := min(max(selLine-win/2, 0), len(body)-win)
		if t, ok := cut[start+win-1]; ok {
			body[start+win-1] = row(on(ui.Subtle).Render(ellipsize(t, cw)))
		}

		body = body[start : start+win]
	}

	out := []string{top}
	if pick.airy {
		out = append(out, row(""))
	}

	out = append(out, row(tabs), row(""))
	out = append(out, body...)

	if pick.airy {
		out = append(out, row(""))
	}

	if pick.hints {
		out = append(out, row(hints))
	}

	return append(out, bottom)
}

// overlayPRList draws the list over base, centred with its top at a quarter of
// the height and ending at least two rows above the bottom.
func (a *App) overlayPRList(f *frameLayers, base string, lk look) string {
	w := prListWidth(a.width)
	if w < 12 {
		return base
	}

	top := a.height / 4
	lines := a.prListBox(lk, w, max(a.height-2-top, 0))

	return overlayLines(f, base, a.width, a.height, top, lines, w, nil)
}

// prListNotes are the lines under a tab's rows (or in place of them): the
// Agents tab's empty text, a searched tab's loading and empty texts, and a row
// for each host whose search failed.
func (a *App) prListNotes(tab prListTab, rows int) []string {
	if tab == prTabAgents {
		if rows == 0 {
			return []string{noticePRListNone}
		}

		return nil
	}

	if !a.prSearchLoaded() {
		return []string{noticePRListLoading}
	}

	notes := a.prSearchFailures()

	hosts := a.search.asked
	answered := len(hosts) == 0 || slices.ContainsFunc(hosts, func(h string) bool {
		s := a.search.hosts[h]

		return s != nil && s.done && s.err == nil
	})

	if rows == 0 && answered {
		none := prListNoneAsked(a.pullRequests.ReviewRequestedDays)
		if tab == prTabAll {
			none = noticePRListNoneMine
		}

		notes = append([]string{none}, notes...)
	}

	return notes
}

// prListNoneAsked is the empty Review requested tab's text; it names the age
// limit when there is one, as RequestedSince reads the days.
func prListNoneAsked(days int) string {
	switch {
	case days <= 0:
		return noticePRListNoneAsked
	case days == 1:
		return "no open pull requests updated in the last 1 day ask for your review"
	default:
		return "no open pull requests updated in the last " + strconv.Itoa(days) + " days ask for your review"
	}
}

// prListTabRow is the tab row cw cells wide: the tabs at the left, the shown
// one on the accent, and at the right the tab keys, which drop from the end
// when they do not fit.
func (a *App) prListTabRow(st core.State, cw int, panel lipgloss.Style, hint func(key, label string) string, ui theme.UI) string {
	var left strings.Builder

	for t := range prTabCount {
		n, ok := a.prListTabRows(st, t)
		label := prTabLabel(t, n, ok)

		style := panel.Foreground(ui.Subtle)
		if t == a.prList.tab {
			style = panel.Background(ui.Accent).Foreground(ui.OnAccent).Bold(true)
		}

		if t > 0 {
			left.WriteString(panel.Render(" "))
		}

		left.WriteString(style.Render(label))
	}

	kt := a.keys.prList

	jump := kt.hint("prlist.tab.agents", "prlist.tab.requested", "prlist.tab.all")
	if jump == "1/2/3" {
		jump = "1-3"
	}

	pairs := hintPairs([][2]string{
		{kt.hint("prlist.tab.next"), "next tab"},
		{jump, "jump"},
	})

	room := cw - ansi.StringWidth(left.String()) - 2
	right, used := "", 0

	for i, kv := range pairs {
		need := ansi.StringWidth(kv[0]) + 1 + ansi.StringWidth(kv[1])
		if i > 0 {
			need += 2
		}

		if used+need > room {
			break
		}

		if i > 0 {
			right += panel.Render("  ")
		}

		right += hint(kv[0], kv[1])
		used += need
	}

	if right == "" {
		return left.String()
	}

	return left.String() + panel.Render(strings.Repeat(" ", max(cw-ansi.StringWidth(left.String())-used, 0))) + right
}
