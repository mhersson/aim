package ui

import (
	"strings"

	"github.com/charmbracelet/x/ansi"

	"github.com/mhersson/aim/internal/core"
	"github.com/mhersson/aim/internal/github"
	"github.com/mhersson/aim/internal/ui/theme"
)

// mergeLine is one row of the merge box; a blank one goes first when the box
// is too tall, then the hint row.
type mergeLine struct {
	text  string
	blank bool
	hint  bool
}

// repoLabel names a repository as owner/name, with its host off github.com.
func repoLabel(r github.Repo) string {
	if r.Host == "github.com" {
		return r.Owner + "/" + r.Name
	}

	return r.String()
}

// mergeBox draws the merge box w cells wide with room for at most room lines. It
// drops the blank rows and then the hint row before it clips anything. The state
// of the pull request is the poll's latest; what enter sends is the box's own.
func (a *App) mergeBox(lk look, w, room int) []string {
	ui, st := lk.theme.UI, lk.styles
	v := a.merge
	state := a.store.State()

	pr := v.pr
	if live, ok := prInfoByNumber(state, v.repo.String(), pr.Number); ok {
		pr = live
	}

	title := " Merge " + prNumberText(pr.Number) + " "
	if a.icons {
		title = " " + iconMerge + " Merge " + prNumberText(pr.Number) + " "
	}

	top, row, bottom := boxFrame(w, title, boxStyle{border: st.popupBorder, title: st.mergeTitle, cut: "…"}, st)
	cw := w - 4
	run := func(text string, fg theme.Hex, bold bool) string { return paint(fg, ui.Panel, bold).Render(text) }

	branch := v.pr.HeadBranch
	if branch == "" {
		branch = pr.HeadBranch
	}

	base := pr.BaseBranch
	if base == "" {
		base = v.pr.BaseBranch
	}

	where := run(printable(repoLabel(v.repo)), ui.Muted, false) + run("  ", ui.Fg, false)
	if a.icons {
		where += run(iconGit+" ", ui.Git, false)
	}

	where += run(printable(branch), ui.Dim, false) + run(" → ", ui.Muted, false) + run(printable(base), ui.Dim, false)

	sha := v.sha[:min(shortOIDLen, len(v.sha))]

	headNote := ", the commit you reviewed; GitHub refuses if the PR moved since"
	if v.listed {
		headNote = ", the commit listed; GitHub refuses if the PR moved since"
	}

	head := run("head "+sha, ui.Fg, true) + run(headNote, ui.Muted, false)

	status := []railRun{}
	if glyph, counts := prChecksOf(pr, a.icons, ui); glyph.text != "" {
		status = append(status, glyph, railRun{text: " "}, counts, railRun{text: "   "})
	}

	status = append(status, prReviewWords(pr, ui), railRun{text: "   "}, mergeStateWord(pr.MergeState, base, ui))

	lines := []mergeLine{
		{blank: true},
		{text: run(printable(pr.Title), ui.Fg, true)},
		{text: where},
		{blank: true},
		{text: head},
		{text: railPainter{bg: ui.Panel}.line(status, cw)},
	}

	mark := ""
	if a.icons {
		mark = iconAlert + " "
	}

	if warn := mergeWarning(pr, a.mergeChecks(state, v), cw-ansi.StringWidth(mark)); warn != "" {
		lines = append(lines, mergeLine{text: run(mark+printable(warn), ui.Warning, false)})

		if reminder := mergeReminder(a.keys.merge); reminder != "" {
			lines = append(lines, mergeLine{text: run(reminder, ui.Warning, false)})
		}
	}

	lines = append(lines, mergeLine{blank: true}, mergeLine{text: a.mergeChips(v, lk)})

	if v.deleteShown() {
		lines = append(lines, mergeLine{blank: true}, mergeLine{text: a.mergeDelete(v, lk, cw)})
	}

	hints := mergeHints(v, a.keys.merge, cw, lk)
	if hints != "" {
		lines = append(lines, mergeLine{blank: true}, mergeLine{text: hints, hint: true})
	}

	// The frame takes two lines beyond these.
	for over := len(lines) + 2 - room; over > 0; {
		i := lastBlank(lines)
		if i < 0 {
			break
		}

		lines = append(lines[:i], lines[i+1:]...)
		over--
	}

	// The hint row goes whole after the blank rows.
	if over := len(lines) + 2 - room; over > 0 && len(lines) > 0 && lines[len(lines)-1].hint {
		lines = lines[:len(lines)-1]
	}

	out := []string{top}

	for _, l := range lines {
		out = append(out, row(l.text))
	}

	return append(out, bottom)
}

// lastBlank is the last blank row of lines, or -1: the box gives up the
// ones nearest its hints first and keeps its leading blank.
func lastBlank(lines []mergeLine) int {
	for i := len(lines) - 1; i > 0; i-- {
		if lines[i].blank {
			return i
		}
	}

	return -1
}

// mergeChips draws the method row: a chip per allowed method, the chosen one on
// Accent.
func (a *App) mergeChips(v *mergeView, lk look) string {
	ui := lk.theme.UI

	var sb strings.Builder

	sb.WriteString(paint(ui.Muted, ui.Panel, false).Render("method  "))

	first := true

	for _, m := range mergeMethods() {
		if !v.allows(m) {
			continue
		}

		if !first {
			sb.WriteString(paint(ui.Fg, ui.Panel, false).Render(" "))
		}

		first = false

		chip := " " + string(m) + " "
		if k := a.keys.merge.hint(mergeMethodCommands[m]); k != "" {
			chip = " " + k + chip
		}

		if m == v.method {
			sb.WriteString(paint(ui.OnAccent, ui.Accent, true).Render(chip))
		} else {
			sb.WriteString(paint(ui.Muted, ui.Panel, false).Render(chip))
		}
	}

	return sb.String()
}

// mergeDelete draws the delete-branch row in cw cells; the hint after it shows
// only whole.
func (a *App) mergeDelete(v *mergeView, lk look, cw int) string {
	ui := lk.theme.UI
	box, fg := "[ ]", ui.Subtle

	if v.del {
		box, fg = "[x]", ui.Accent
	}

	text := " delete " + printable(v.pr.HeadBranch) + " after merging"
	out := paint(fg, ui.Panel, v.del).Render(box) + paint(ui.Fg, ui.Panel, false).Render(text)

	const hint = "   (the repository deletes head branches)"
	if v.info.DeleteBranchOnMerge && ansi.StringWidth(box+text+hint) <= cw {
		out += paint(ui.Subtle, ui.Panel, false).Render(hint)
	}

	return out
}

// mergeHints are the key hints that fit cw cells; ones that do not are dropped
// from the end, and none that fit leaves "".
func mergeHints(v *mergeView, kt keyTable, cw int, lk look) string {
	pairs := [][2]string{{kt.hint("merge.method.squash", "merge.method.merge", "merge.method.rebase"), "method"}}

	if v.deleteShown() {
		pairs = append(pairs, [2]string{kt.hint("merge.delete"), "delete branch"})
	}

	pairs = append(pairs, [2]string{kt.hint("merge.run"), "merge"}, [2]string{kt.hint("merge.cancel"), "cancel"})

	return hintLine(hintPairs(pairs), cw, lk.styles)
}

// hintLine draws key and label pairs four cells apart; pairs that do not fit
// cw cells are dropped from the end.
func hintLine(pairs [][2]string, cw int, st styles) string {
	var sb strings.Builder

	used := 0

	for i, kv := range pairs {
		need := ansi.StringWidth(kv[0]) + 1 + ansi.StringWidth(kv[1])
		if i > 0 {
			need += 4
		}

		if used+need > cw {
			break
		}

		if i > 0 {
			sb.WriteString(st.popup.Render("    "))
		}

		sb.WriteString(st.popupKey.Render(kv[0]) + st.popupMuted.Render(" "+kv[1]))

		used += need
	}

	return sb.String()
}

// overlayMerge draws the merge box over base, centred with its top at a third of
// the height.
func (a *App) overlayMerge(f *frameLayers, base string, lk look) string {
	w := min(maxMergeWidth, a.width-2)
	if w < 4 {
		return base
	}

	top := max(1, a.height/3)

	return overlayLines(f, base, a.width, a.height, top, a.mergeBox(lk, w, max(a.height-1-top, 0)), w, nil)
}

// prReviewWords is a pull request's review in words, as the header writes it.
func prReviewWords(pr core.PRInfo, ui theme.UI) railRun {
	switch pr.ReviewDecision {
	case "APPROVED":
		return railRun{text: "approved", fg: ui.Success}
	case "CHANGES_REQUESTED":
		return railRun{text: "changes requested", fg: ui.Warning}
	}

	return railRun{text: "no review yet", fg: ui.Subtle}
}
