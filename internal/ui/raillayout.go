package ui

import (
	"strconv"
	"time"

	"github.com/charmbracelet/x/ansi"

	"github.com/mhersson/aim/internal/core"
)

// The rail's width rule: a wide body gets railFullWidth; narrower ones leave
// railPanesMin columns to the panes; under railBodyMin it is hidden.
const (
	railFullWidth = 44
	railWideBody  = 84
	railBodyMin   = 64
	railPanesMin  = 40
	// railHintsMinInner is the inner height from which the hint row shows.
	railHintsMinInner = 10
	// railCardLines is the lines of one card.
	railCardLines = 4
)

// railWidthFor is the rail's width in a body, 0 when it does not fit.
func railWidthFor(body core.Rect) int {
	switch {
	case body.H < minFrame || body.W < railBodyMin:
		return 0
	case body.W >= railWideBody:
		return railFullWidth
	}

	return body.W - railPanesMin
}

// railVisible reports whether the rail is drawn: open and wide enough.
func (a *App) railVisible() bool { return a.railShows(a.store.State().Rail) }

func (a *App) railShows(open bool) bool {
	return open && railWidthFor(bodyRect(a.width, a.height)) > 0
}

// railRect is the rail's box; it is the zero rect while the rail is hidden.
func (a *App) railRect() core.Rect {
	if !a.railVisible() {
		return core.Rect{}
	}

	body := bodyRect(a.width, a.height)
	body.W = railWidthFor(body)

	return body
}

// railRowKind is what one list row holds.
type railRowKind uint8

const (
	railRowBlank railRowKind = iota
	railRowHeader
	railRowCard
)

// railRow is one row of the whole list: a group header, a card line or a blank.
type railRow struct {
	kind  railRowKind
	group int // the group index, for a header
	card  int // the card's index in the flat shown list, for a card line
	line  int // 0 to 3, for a card line
}

// railLayout lists every row of the list: each group's header, then for each
// card a blank row and its four lines, then a blank row closing the group. A
// blank row so lies above and below every card, where the outline goes.
func railLayout(groups []railGroup) []railRow {
	var rows []railRow

	card := 0

	for gi, g := range groups {
		rows = append(rows, railRow{kind: railRowHeader, group: gi})

		for range g.cards {
			rows = append(rows, railRow{kind: railRowBlank})

			for l := range railCardLines {
				rows = append(rows, railRow{kind: railRowCard, card: card, line: l})
			}

			card++
		}

		rows = append(rows, railRow{kind: railRowBlank})
	}

	return rows
}

// railClampTop is the first row shown in h rows: top moved the least that shows
// the selected card with its blank rows and, when it fits, its group's header
// (the blank rows win over the header), then snapped to a group header or a
// card's blank row so the list never starts mid-card. When the card and its
// blank rows do not fit, the card's first line wins. card is the selected
// card's index, -1 for none.
func railClampTop(rows []railRow, h, top, card int) int {
	maxTop := max(len(rows)-h, 0)
	top = min(max(top, 0), maxTop)

	if h <= 0 {
		return top
	}

	f := -1

	if card >= 0 {
		for i, r := range rows {
			if r.kind == railRowCard && r.card == card && r.line == 0 {
				f = i

				break
			}
		}
	}

	if f < 0 {
		return railSnapTop(rows, top, 0, maxTop)
	}

	lo, hi := f-1, f+railCardLines

	if f >= 2 && rows[f-2].kind == railRowHeader {
		lo = f - 2
	}

	// A header that makes the span overflow is given up before the blank rows.
	if hi-lo+1 > h && lo < f-1 {
		lo = f - 1
	}

	if hi-lo+1 > h {
		return f
	}

	if top > lo {
		top = lo
	}

	if top+h-1 < hi {
		top = hi - h + 1
	}

	// lo is a boundary within reach, so a snap that keeps the blank rows around
	// the card always exists, even when it leaves empty rows below the list end.
	return railSnapTop(rows, top, max(hi-h+1, 0), lo)
}

// railSnapTop is top moved to a boundary in [from, to], or top itself when
// there is none.
func railSnapTop(rows []railRow, top, from, to int) int {
	if t, ok := railSnapIn(rows, top, from, to); ok {
		return t
	}

	return top
}

// railSnapIn is the last boundary row in [from, to] at or above top, else the
// first one below it. A boundary is a group header or a card's blank row.
func railSnapIn(rows []railRow, top, from, to int) (int, bool) {
	best := -1

	for i := max(from, 0); i <= to && i < len(rows); i++ {
		if !railBoundary(rows, i) {
			continue
		}

		if i <= top {
			best = i

			continue
		}

		if best < 0 {
			best = i
		}

		break
	}

	return best, best >= 0
}

// railBoundary reports whether row i is where the list may start: a group
// header or the blank row above a card.
func railBoundary(rows []railRow, i int) bool {
	switch rows[i].kind {
	case railRowHeader:
		return true
	case railRowBlank:
		return i+1 < len(rows) && rows[i+1].kind == railRowCard
	}

	return false
}

// railGeom is where the parts of the rail's box sit; a y of -1 means the part
// is not drawn.
type railGeom struct {
	box     core.Rect
	searchY int
	list    core.Rect
	hintY   int
}

// railGeometry places the search row, the list and the hint row inside box.
func railGeometry(box core.Rect, search bool) railGeom {
	g := railGeom{box: box, searchY: -1, hintY: -1}
	in := core.Rect{X: box.X + 1, Y: box.Y + 1, W: max(box.W-2, 0), H: max(box.H-2, 0)}
	y, h := in.Y, in.H

	if search {
		g.searchY = y
		y, h = y+2, h-2
	}

	if in.H >= railHintsMinInner {
		g.hintY = in.Y + in.H - 1
		h -= 2
	}

	g.list = core.Rect{X: in.X, Y: y, W: in.W, H: max(h, 0)}

	return g
}

// railView is everything the drawing and the mouse need to know about the
// list: the geometry, the cards, their rows and the first row shown.
type railView struct {
	geom   railGeom
	groups []railGroup
	shown  []railCard
	rows   []railRow
	top    int        // clamped
	sel    int        // the selected card's index in shown, -1 for none
	marks  []markSpan // per shown card; w is 0 without a plan mark
}

// markSpan is the plan mark's place on a card's first line: x from the
// content's left edge, w cells.
type markSpan struct{ x, w int }

// railViewOf lays the list out for the current state; ok is false while the
// rail is hidden.
func (a *App) railViewOf(st core.State) (railView, bool) {
	if !a.railShows(st.Rail) {
		return railView{}, false
	}

	box := bodyRect(a.width, a.height)
	box.W = railWidthFor(box)

	v := railView{
		geom:   railGeometry(box, a.rail.searching || a.rail.query != ""),
		groups: a.railCards(st),
		sel:    -1,
	}

	for _, g := range v.groups {
		v.shown = append(v.shown, g.cards...)
	}

	for i, c := range v.shown {
		if c.pane == a.rail.sel {
			v.sel = i
		}
	}

	v.marks = a.railMarks(v.shown, max(box.W-4, 0))
	v.rows = railLayout(v.groups)
	v.top = railClampTop(v.rows, v.geom.list.H, a.rail.top, v.sel)

	return v, true
}

// cardAt is the index of the card whose four lines include the cell (x, y) of
// the list; blank rows and headers belong to no card.
func (v railView) cardAt(x, y int) (int, bool) {
	l := v.geom.list
	if x < l.X || x >= l.X+l.W || y < l.Y || y >= l.Y+l.H {
		return 0, false
	}

	i := v.top + y - l.Y
	if i < 0 || i >= len(v.rows) {
		return 0, false
	}

	if r := v.rows[i]; r.kind == railRowCard {
		return r.card, true
	}

	return 0, false
}

// railAgeOf is the age as shown: its count and unit byte (s, m, h, d). The
// count is -1 when there is no time.
func railAgeOf(active, now time.Time) (n int, unit byte) {
	if active.IsZero() {
		return -1, 0
	}

	d := max(now.Sub(active), 0)

	switch {
	case d < time.Minute:
		return int(d / time.Second), 's'
	case d < time.Hour:
		return int(d / time.Minute), 'm'
	case d < 24*time.Hour:
		return int(d / time.Hour), 'h'
	}

	return int(d / (24 * time.Hour)), 'd'
}

// railAge is the time since active, floored: 8s, 4m, 2h, 3d; empty for no time.
func railAge(active, now time.Time) string {
	n, unit := railAgeOf(active, now)
	if n < 0 {
		return ""
	}

	return strconv.Itoa(n) + string(unit)
}

// railHiddenSide is the cards hidden on one side of the rows shown.
type railHiddenSide struct {
	n       int
	blocked bool // at least one of them is blocked
}

// railHidden counts the cards of shown not wholly inside the h rows shown from
// top: a card is shown only when all four of its lines are.
func railHidden(rows []railRow, shown []railCard, h, top int) (above, below railHiddenSide) {
	for i, r := range rows {
		if r.kind != railRowCard || r.line != 0 {
			continue
		}

		var side *railHiddenSide

		switch {
		case i < top:
			side = &above
		case i+railCardLines-1 >= top+h:
			side = &below
		default:
			continue
		}

		side.n++
		side.blocked = side.blocked || shown[r.card].state == core.AgentBlocked
	}

	return above, below
}

// railMarks finds the plan mark of each card that shows one.
func (a *App) railMarks(shown []railCard, cw int) []markSpan {
	marks := make([]markSpan, len(shown))
	ui := a.current().theme.UI

	for i, c := range shown {
		if !c.plan {
			continue
		}

		x := 0

		for _, r := range a.railTitleLine(ui, c, nil, cw) {
			w := ansi.StringWidth(r.text)
			if r.mark {
				marks[i] = markSpan{x: x, w: w}

				break
			}

			x += w
		}
	}

	return marks
}

// markAt is the agent whose plan mark covers the cell (x, y).
func (v railView) markAt(x, y int) (core.PaneID, bool) {
	l := v.geom.list
	if y < l.Y || y >= l.Y+l.H {
		return "", false
	}

	i := v.top + y - l.Y
	if i < 0 || i >= len(v.rows) {
		return "", false
	}

	r := v.rows[i]
	if r.kind != railRowCard || r.line != 0 {
		return "", false
	}

	m := v.marks[r.card]
	// The content starts after the box's border and the edge column.
	if left := v.geom.box.X + 2 + m.x; m.w == 0 || x < left || x >= left+m.w {
		return "", false
	}

	return v.shown[r.card].pane, true
}
