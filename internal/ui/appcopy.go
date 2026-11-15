package ui

import (
	tea "charm.land/bubbletea/v2"

	"github.com/mhersson/aim/internal/core"
)

// copyState is copy mode on one pane. Its rows are absolute as of trimmed, the
// lines trimmed off the pane's history when they were last shifted.
type copyState struct {
	pane    core.PaneID
	m       copyMode
	trimmed uint64
}

// shifted moves the cursor, view top and selection up by the history trimmed
// since cs.trimmed, so they stay on the same text, and clamps them to a grid of
// rows rows. A selection whose both ends fell off the history is dropped.
func (cs copyState) shifted(trimmed uint64, rows int) copyState {
	delta := int(trimmed - cs.trimmed)
	cs.trimmed = trimmed

	if delta == 0 {
		return cs
	}

	clampRow := func(row int) int { return max(min(row, rows-1), 0) }
	m := cs.m

	if m.sel != nil {
		s := *m.sel

		if s.anchor.row-delta < 0 && s.head.row-delta < 0 {
			m.sel = nil
		} else {
			s.anchor = shiftEnd(s.anchor, delta, s.mode, clampRow)
			s.head = shiftEnd(s.head, delta, s.mode, clampRow)
			m.sel = &s
		}
	}

	m.cursor.row = clampRow(m.cursor.row - delta)
	m.top = clampRow(m.top - delta)
	cs.m = m

	return cs
}

// shiftEnd moves a selection end up by delta rows. An end that fell off the
// history takes the start of the oldest line, except in block mode.
func shiftEnd(p point, delta int, mode selMode, clampRow func(int) int) point {
	if p.row-delta < 0 && mode != selBlock {
		p.col = 0
	}

	p.row = clampRow(p.row - delta)

	return p
}

// enterCopyMode implements copy.mode: vim-style navigation of the focused
// pane's text, starting at the bottom of its current view.
func (a *App) enterCopyMode() tea.Cmd {
	// A pane the drawer covers cannot show copy mode, so the drawer closes first.
	a.uncoverFocus()

	p := a.panes[a.store.State().Tab().Focus]
	if p == nil {
		return nil
	}

	_, rows := p.size()

	var cs copyState

	p.withView(func(g grid, top int, trimmed uint64) {
		cs = copyState{pane: p.id, m: newCopyMode(g, top, rows), trimmed: trimmed}
	})

	if cs.pane == "" {
		return nil
	}

	a.clearSelections()
	a.mouse.press, a.mouse.fwd = nil, nil
	a.copy = &cs

	return nil
}

// handleCopyKey runs one key of copy mode; the view follows the cursor.
func (a *App) handleCopyKey(msg tea.KeyPressMsg) tea.Cmd {
	a.notice = ""
	cs := a.copy

	p := a.panes[cs.pane]
	if p == nil {
		a.copy = nil

		return nil
	}

	_, rows := p.size()
	key := bindingKey(msg)
	act, text := copyNone, ""

	claude := isClaudePane(a.store.State().Panes[cs.pane])

	p.withView(func(g grid, top int, trimmed uint64) {
		next := cs.shifted(trimmed, g.Rows())
		next.m.top = top
		next.m, act = next.m.key(g, key, rows)

		if act == copyYank && next.m.sel != nil {
			text = extractFor(g, *next.m.sel, claude)
		}

		*cs = next
	})

	switch act {
	case copyYank:
		a.endCopy()

		return a.clip.copy(text)
	case copyExit:
		a.endCopy()

		return nil
	case copySearch, copySearchBack:
		backward := act == copySearchBack

		a.showPrompt(&prompt{kind: promptSearch, pane: cs.pane, backward: backward, done: func(a *App, query string) tea.Cmd {
			a.runSearch(query, backward)

			return nil
		}}, "")
	}

	p.setViewTop(cs.m.top)

	return nil
}

// runSearch moves copy mode's cursor to the next match; an empty query repeats
// the last one. A miss leaves the cursor and says so.
func (a *App) runSearch(query string, backward bool) {
	cs := a.copy
	if cs == nil {
		return
	}

	p := a.panes[cs.pane]
	if p == nil {
		return
	}

	if query == "" {
		query = cs.m.lastSearch
	}

	if query == "" {
		return
	}

	_, rows := p.size()
	found := false

	p.withView(func(g grid, top int, trimmed uint64) {
		next := cs.shifted(trimmed, g.Rows())
		next.m.top = top

		m, ok := next.m.search(g, query, backward)
		if ok {
			m.top = m.viewTop(rows, m.top)
			next.m = m
		}

		found = ok
		*cs = next
	})

	if !found {
		a.notice = "not found: " + query

		return
	}

	p.setViewTop(cs.m.top)
}

// endCopy leaves copy mode and returns the pane to its live screen.
func (a *App) endCopy() {
	if a.copy == nil {
		return
	}

	id := a.copy.pane
	a.endCopyOf(id)

	if p := a.panes[id]; p != nil {
		p.scrollLive()
	}
}

// endCopyOf ends copy mode and any search prompt when they belong to pane id.
func (a *App) endCopyOf(id core.PaneID) {
	if a.copy != nil && a.copy.pane == id {
		a.copy = nil
	}

	if a.prompt != nil && a.prompt.kind == promptSearch && a.prompt.pane == id {
		a.prompt = nil
	}
}

// syncCopy re-bases copy mode's rows after output that trimmed p's history and
// holds the view on the cursor's text while output arrives.
func (a *App) syncCopy(p *pane) {
	cs := a.copy
	if cs == nil || cs.pane != p.id {
		return
	}

	p.withView(func(g grid, _ int, trimmed uint64) { *cs = cs.shifted(trimmed, g.Rows()) })
	p.setViewTop(cs.m.top)
}

// copyCursor is copy mode's block cursor on screen; nil when its pane is not
// drawn or its row is outside the view. It is placed on the rows as drawn, so a
// held sync update cannot move it off its text.
func (a *App) copyCursor(g geometry) *tea.Cursor {
	cs := a.copy

	r, ok := g.rects[cs.pane]
	p := a.panes[cs.pane]

	if !ok || p == nil {
		return nil
	}

	var cur *tea.Cursor

	c := contentRect(r)
	_, base := p.linesAt()

	p.withView(func(grid grid, _ int, trimmed uint64) {
		at := cs.shifted(trimmed, grid.Rows()).m.cursor
		top := int(int64(base) - int64(trimmed))
		x, y := at.col, at.row-top

		if x < 0 || y < 0 || x >= c.W || y >= c.H {
			return
		}

		cur = tea.NewCursor(c.X+x, c.Y+y)
		cur.Shape = tea.CursorBlock
		cur.Blink = false
	})

	if cur != nil && a.inDrawer(cur.X, cur.Y) {
		return nil
	}

	return cur
}
