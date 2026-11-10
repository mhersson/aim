package ui

import (
	"slices"
	"strings"
	"unicode/utf8"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
)

// whichKeyMsg fires after the leader delay; seq ties it to one leader press.
type whichKeyMsg struct{ seq int }

// hint is one key and the title of the command it runs, listed under group.
type hint struct{ key, title, group string }

// hintFamily is a set of commands that share one which-key row when every
// member is bound.
type hintFamily struct {
	prefix  string
	members []string // command id suffixes, in the order their keys show
	title   string
	sep     string // between keys; "-" shows consecutive keys as first-last
	// seqTitle titles the family's row in the sequence popup, whose keys are the
	// second ones; "" means each command's own title.
	seqTitle string
}

func hintFamilies() []hintFamily {
	return []hintFamily{
		{"pane.split.", []string{"down", "right"}, "Split below/right", " ", ""},
		{"pane.focus.", []string{"left", "down", "up", "right"}, "Focus pane", " ", ""},
		{"pane.resize.", []string{"left", "down", "up", "right"}, "Resize pane", " ", ""},
		{"pane.swap.", []string{"left", "down", "up", "right"}, "Swap pane", "", ""},
		{"tab.jump.", []string{"1", "2", "3", "4", "5", "6", "7", "8", "9"}, "Jump to tab", "-", ""},
		{"pane.move.tab.", []string{"1", "2", "3", "4", "5", "6", "7", "8", "9"}, "Move pane to tab", "-", "Move pane to tab N"},
		{"pane.send.tab.", []string{"1", "2", "3", "4", "5", "6", "7", "8", "9"}, "Send pane to tab", "-", "Send pane to tab N"},
		{"pane.preselect.", []string{"left", "down", "up", "right"}, "Preselect", "", "Preselect side"},
	}
}

// familyOf returns the family a command is a member of, or nil.
func familyOf(families []hintFamily, id string) *hintFamily {
	for i := range families {
		if rest, ok := strings.CutPrefix(id, families[i].prefix); ok && slices.Contains(families[i].members, rest) {
			return &families[i]
		}
	}

	return nil
}

// groupIcon is the glyph of a which-key group head, or "".
func groupIcon(group string) string {
	switch group {
	case "Panes":
		return iconPanes
	case "Layout":
		return iconLayoutBSP
	case "Tabs":
		return iconTabs
	case "Git":
		return iconGit
	case "App":
		return iconCog
	}

	return ""
}

// hintKey names a key for display: ctrl+h becomes ^h.
func hintKey(key string) string {
	if rest, ok := strings.CutPrefix(key, "ctrl+"); ok {
		return "^" + rest
	}

	return key
}

// keys returns the display keys of the family's members, each member's first
// key, and the lead their sequences share ("> " for "> 1"), or false when a
// member is unbound or the sequences differ in their first key.
func (f hintFamily) keys(a *App) (lead string, keys []string, group string, ok bool) {
	keys = make([]string, len(f.members))

	for i, m := range f.members {
		key, ok := a.keymap.keyOf(f.prefix + m)
		if !ok {
			return "", nil, "", false
		}

		keys[i] = hintKey(key)
		if i == 0 {
			group = a.keymap.groupOf(f.prefix + m)
		}
	}
	// Sequence keys such as "> 1" share their first key, which leads the row.
	if first, _, ok := strings.Cut(keys[0], " "); ok {
		lead = first + " "
		for i, k := range keys {
			rest, ok := strings.CutPrefix(k, lead)
			if !ok || strings.Contains(rest, " ") {
				return "", nil, "", false
			}

			keys[i] = rest
		}
	}

	return lead, keys, group, true
}

// collapse returns the one row for f, or false when a member is unbound.
func (f hintFamily) collapse(a *App) (hint, bool) {
	lead, keys, group, ok := f.keys(a)
	if !ok {
		return hint{}, false
	}

	if f.sep == "-" && consecutive(keys) {
		return hint{key: lead + keys[0] + "-" + keys[len(keys)-1], title: f.title, group: group}, true
	}

	sep := f.sep
	if sep == "-" {
		sep = " "
	}

	return hint{key: lead + strings.Join(keys, sep), title: f.title, group: group}, true
}

// consecutive reports whether keys are single characters in ascending order.
func consecutive(keys []string) bool {
	for i, k := range keys {
		r, n := utf8.DecodeRuneInString(k)
		if n != len(k) {
			return false
		}

		if i > 0 {
			prev, _ := utf8.DecodeRuneInString(keys[i-1])
			if r != prev+1 {
				return false
			}
		}
	}

	return len(keys) > 0
}

// hints lists the bound keys with their commands' titles, a row per key. A
// family whose commands are all bound shares one row on their first keys;
// otherwise its keys get a row each.
func (a *App) hints() []hint {
	hints := make([]hint, 0, len(a.keymap.bindings))
	done := map[string]bool{}
	families := hintFamilies()

	for _, b := range a.keymap.bindings {
		c, ok := a.reg.get(b.command)
		if !ok {
			continue
		}

		first, _ := a.keymap.keyOf(b.command)

		if fam := familyOf(families, b.command); fam != nil && first == b.key {
			if row, ok := fam.collapse(a); ok {
				if !done[fam.prefix] {
					hints = append(hints, row)
				}

				done[fam.prefix] = true

				continue
			}
		}

		// A group prefix shows one row, whatever its second keys are.
		if first, _, ok := strings.Cut(b.key, " "); ok {
			if title, group, ok := groupPrefixTitle(first); ok {
				if !done["prefix "+first] {
					hints = append(hints, hint{key: first, title: title, group: group})
				}

				done["prefix "+first] = true

				continue
			}
		}

		hints = append(hints, hint{key: b.key, title: c.title, group: b.group})
	}

	return hints
}

// groupPrefixTitle is the which-key row title and group of a prefix that opens
// a sequence popup of its group's commands; the group does not follow whatever
// binding under the prefix comes first.
func groupPrefixTitle(prefix string) (title, group string, ok bool) {
	if prefix == "g" {
		return "Push and pull requests", "Git", true
	}

	return "", "", false
}

// groupOrder lists the which-key groups left to right and top to bottom;
// any other group follows in the order it first appears.
func groupOrder() []string { return []string{"Panes", "Layout", "Tabs", "Git", "App"} }

// groupHints splits hints by group, in groupOrder.
func groupHints(hints []hint) [][]hint {
	var groups [][]hint

	index := map[string]int{}
	for _, h := range hints {
		i, ok := index[h.group]
		if !ok {
			i = len(groups)
			index[h.group] = i

			groups = append(groups, nil)
		}

		groups[i] = append(groups[i], h)
	}

	order := groupOrder()
	rank := func(g []hint) int {
		if i := slices.Index(order, g[0].group); i >= 0 {
			return i
		}

		return len(order)
	}

	slices.SortStableFunc(groups, func(a, b []hint) int { return rank(a) - rank(b) })

	return groups
}

// column is one which-key column: its rows, drawn at width cells including
// the one-cell margin on each side.
type column struct {
	rows  []string
	width int
}

// whichKeyColumn lays out the groups of hints one under another, a blank row
// between them when gaps is set. Each row is exactly the column's width.
func whichKeyColumn(groups [][]hint, gaps, icons bool, st styles) column {
	keyW, contentW := 0, 0

	headW := func(name string) int {
		if icons && groupIcon(name) != "" {
			return lipgloss.Width(groupIcon(name)) + 1 + lipgloss.Width(name)
		}

		return lipgloss.Width(name)
	}
	for _, g := range groups {
		contentW = max(contentW, headW(g[0].group))
		for _, h := range g {
			keyW = max(keyW, lipgloss.Width(h.key))
		}
	}

	for _, g := range groups {
		for _, h := range g {
			contentW = max(contentW, keyW+2+lipgloss.Width(h.title))
		}
	}

	margin := st.popup.Render(" ")

	var rows []string

	for i, g := range groups {
		if i > 0 && gaps {
			rows = append(rows, st.popup.Render(strings.Repeat(" ", contentW+2)))
		}

		name := g[0].group

		head := name
		if icons && groupIcon(name) != "" {
			head = groupIcon(name) + " " + name
		}

		rows = append(rows, margin+st.popupHead.Render(head)+st.popup.Render(" ")+
			st.popupRule.Render(strings.Repeat("─", max(contentW-headW(name)-1, 0)))+margin)

		for _, h := range g {
			pad := keyW - lipgloss.Width(h.key) + 2
			rest := contentW - keyW - 2 - lipgloss.Width(h.title)
			rows = append(rows, margin+st.popupKey.Render(h.key)+st.popup.Render(strings.Repeat(" ", pad)+h.title+
				strings.Repeat(" ", rest))+margin)
		}
	}

	return column{rows: rows, width: contentW + 2}
}

// sequenceRows are the second keys a sequence prefix waits for, as key and
// title, in binding order, with a cancel row last unless esc is bound.
func (a *App) sequenceRows(prefix string) []hint {
	lead := prefix + " "
	families := hintFamilies()
	done := map[string]bool{}

	var rows []hint

	for _, b := range a.keymap.bindings {
		second, ok := strings.CutPrefix(b.key, lead)
		if !ok {
			continue
		}

		c, ok := a.reg.get(b.command)
		if !ok {
			continue
		}

		first, _ := a.keymap.keyOf(b.command)

		if fam := familyOf(families, b.command); fam != nil && first == b.key && fam.seqTitle != "" {
			if l, keys, _, ok := fam.keys(a); ok && l == lead {
				if !done[fam.prefix] {
					rows = append(rows, hint{key: joinSecond(keys, fam.sep), title: fam.seqTitle})
				}

				done[fam.prefix] = true

				continue
			}
		}

		title := c.short
		if title == "" {
			title = c.title
		}

		rows = append(rows, hint{key: second, title: title})
	}

	if len(rows) == 0 {
		return nil
	}

	if _, bound := a.keymap.lookup(lead + "esc"); !bound {
		rows = append(rows, hint{key: "esc", title: "Cancel"})
	}

	return rows
}

// joinSecond shows a family's second keys as first-last when they run on, else
// separated by spaces.
func joinSecond(keys []string, sep string) string {
	if sep == "-" && consecutive(keys) {
		return keys[0] + "-" + keys[len(keys)-1]
	}

	return strings.Join(keys, " ")
}

// sequencePopup draws the box a pending sequence shows: the leader's name and
// the first key as its title, then one row per second key.
func sequencePopup(leader, prefix string, rows []hint, icons bool, st styles) []string {
	if len(rows) == 0 {
		return nil
	}

	title := " " + leader + " " + prefix + " "
	if icons {
		title = " " + iconLeader + " " + leader + " " + prefix + " "
	}

	keyW, contentW := 0, 0
	for _, h := range rows {
		keyW = max(keyW, lipgloss.Width(h.key))
	}

	for _, h := range rows {
		contentW = max(contentW, keyW+2+lipgloss.Width(h.title))
	}
	// The title sits after the corner and one dash, with a dash left before the corner.
	contentW = max(contentW, lipgloss.Width(title))
	width := contentW + 2

	border := func(s string) string { return st.popupBorder.Render(s) }
	margin := st.popup.Render(" ")

	lines := []string{border("╭─") + st.popupHead.Render(title) +
		border(strings.Repeat("─", width-1-lipgloss.Width(title))+"╮")}
	for _, h := range rows {
		pad := keyW - lipgloss.Width(h.key) + 2
		rest := contentW - keyW - 2 - lipgloss.Width(h.title)
		lines = append(lines, border("│")+margin+st.popupKey.Render(h.key)+
			st.popup.Render(strings.Repeat(" ", pad)+h.title+strings.Repeat(" ", rest))+margin+border("│"))
	}

	return append(lines, border("╰"+strings.Repeat("─", width)+"╯"))
}

// whichKeyPopup draws the grouped hint list titled with the leader key: Panes
// and Layout in a left column, the others in a right one.
func whichKeyPopup(leader string, hints []hint, icons bool, st styles) []string {
	groups := groupHints(hints)
	if len(groups) == 0 {
		return nil
	}
	// Panes and Layout share the left column; the rest go right.
	nLeft := 1
	if len(groups) > 1 && groups[0][0].group == "Panes" && groups[1][0].group == "Layout" {
		nLeft = 2
	}

	left, right := groups[:nLeft], groups[nLeft:]

	title := " " + leader + " "
	if icons {
		title = " " + iconLeader + " " + leader + " "
	}

	l := whichKeyColumn(left, false, icons, st)
	// The title sits after the corner and one dash, with a dash left before the column rule.
	if need := lipgloss.Width(title) + 2; l.width < need {
		l = padColumn(l, need, st)
	}

	cols := []column{l}

	if len(right) > 0 {
		// The gaps go when they would make the right column taller than the left: the popup must fit an 80x24 terminal.
		r := whichKeyColumn(right, true, icons, st)
		if len(r.rows) > len(l.rows) {
			r = whichKeyColumn(right, false, icons, st)
		}

		cols = append(cols, r)
	}

	height := 0
	for _, c := range cols {
		height = max(height, len(c.rows))
	}

	for i, c := range cols {
		for len(c.rows) < height {
			c.rows = append(c.rows, st.popup.Render(strings.Repeat(" ", c.width)))
		}

		cols[i] = c
	}

	border := func(s string) string { return st.popupBorder.Render(s) }
	rule := func(s string) string { return st.popupRule.Render(s) }
	split := len(cols) > 1

	top := border("╭─") + st.popupHead.Render(title)
	topFill := l.width - 1 - lipgloss.Width(title)
	top += border(strings.Repeat("─", topFill))

	bottom := border("╰" + strings.Repeat("─", l.width))
	if split {
		top += rule("┬") + border(strings.Repeat("─", cols[1].width)+"╮")
		bottom += rule("┴") + border(strings.Repeat("─", cols[1].width)+"╯")
	} else {
		top += border("╮")
		bottom += border("╯")
	}

	lines := []string{top}

	for i := range height {
		line := border("│") + cols[0].rows[i]
		if split {
			line += rule("│") + cols[1].rows[i]
		}

		lines = append(lines, line+border("│"))
	}

	return append(lines, bottom)
}

// padColumn widens c to width with blank cells on the right of every row.
func padColumn(c column, width int, st styles) column {
	extra := st.popup.Render(strings.Repeat(" ", width-c.width))
	for i, r := range c.rows {
		c.rows[i] = r + extra
	}

	c.width = width

	return c
}

// overlayWhichKey draws the popup over base at the bottom-right, one cell
// from the right edge, its bottom border on the last row. It is clipped to the
// body rows and the screen, never wrapped.
func overlayWhichKey(f *frameLayers, base string, width, height int, leader string, hints []hint, icons bool, st styles) string {
	if len(hints) == 0 {
		return base
	}

	return overlayCorner(f, base, width, height, whichKeyPopup(leader, hints, icons, st))
}

// overlayCorner draws popup lines over base at the bottom-right of the body,
// one cell from the right edge and ending on the last row, clipped to the body
// rows and the screen.
func overlayCorner(f *frameLayers, base string, width, height int, lines []string) string {
	if width <= 0 || height < 3 || len(lines) == 0 {
		return base
	}

	y := height - len(lines)
	if y < 2 {
		lines, y = lines[:height-2], 2
	}

	w := 0
	for _, l := range lines {
		w = max(w, lipgloss.Width(l))
	}

	x := max(width-1-w, 0)
	for i, l := range lines {
		lines[i] = ansi.Truncate(l, width-x, "")
	}

	return overlayAt(f, base, width, height, x, y, lines, nil)
}
