package ui

import (
	"path/filepath"
	"strconv"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/mhersson/aim/internal/ui/theme"
)

const (
	// sentCardFor is how long the card stays once it could be seen.
	sentCardFor = 15 * time.Second
	// sentCardMinW is the narrowest card, so its title fits.
	sentCardMinW = 36
	// sentCardPicRows is the most picture rows.
	sentCardPicRows = 8
	// sentCardTextRows is the card's height without pictures: borders, names,
	// caption and hint.
	sentCardTextRows = 5
)

// sentCard is the corner card for the newest batch.
type sentCard struct {
	batch int  // seq of the batch it shows
	seq   int  // timer counter; a due message with another seq is stale
	armed bool // its countdown has started
}

// sentCardDueMsg ends the card whose countdown has seq.
type sentCardDueMsg struct{ seq int }

// sentCardLayout is the card's box: above the toast's rows at the bottom right,
// with picRows rows for pictures, or none when pictures are off or the room is
// under 3. ok is false when the box does not fit.
func sentCardLayout(width, height int, pictures bool) (x, y, w, h, picRows int, ok bool) {
	w = max(width/3, sentCardMinW)
	bottom := height - toastRows - 1
	// Rows above the bottom border: not the tab row, the top border, the text
	// line and the hint line.
	room := bottom - 4

	if w > width-2 || room < 1 {
		return 0, 0, 0, 0, 0, false
	}

	h = sentCardTextRows

	if pictures && room >= 3 {
		picRows = min(sentCardPicRows, room)
		h = picRows + 4
	}

	return width - 1 - w, bottom - h + 1, w, h, picRows, true
}

func sentCardTitle(name string, tab int, otherTab bool, n int) string {
	title := name
	if otherTab {
		title += " (tab " + strconv.Itoa(tab+1) + ")"
	}

	if n == 1 {
		return title + " sent an image"
	}

	return title + " sent " + strconv.Itoa(n) + " images"
}

func noticeSent(title, key string) string {
	if key == "" {
		return title
	}

	return title + "; " + key + " views it"
}

// sentCardBatch is the batch the card shows, or false when none holds it now.
func (a *App) sentCardBatch() (sentBatch, bool) {
	if a.sent.card == nil {
		return sentBatch{}, false
	}

	for _, sp := range a.sent.panes {
		for _, b := range sp.batches {
			if b.seq == a.sent.card.batch {
				return b, true
			}
		}
	}

	return sentBatch{}, false
}

func (a *App) sentCardTitleOf(b sentBatch) string {
	return sentCardTitle(printable(b.name), b.tab, b.tab != a.store.State().Active, len(b.images))
}

// sentCardShows reports whether the card is drawn: nothing hides it and it fits.
func (a *App) sentCardShows() bool {
	if a.sent.card == nil || a.sent.view != nil || a.modalOpen() {
		return false
	}

	_, _, _, _, _, ok := sentCardLayout(a.width, a.height, a.pictures.on())

	return ok
}

// sentCardCovers reports whether the drawn card covers cell (cx, cy).
func (a *App) sentCardCovers(cx, cy int) bool {
	if !a.sentCardShows() {
		return false
	}

	x, y, w, h, _, _ := sentCardLayout(a.width, a.height, a.pictures.on())

	return cx >= x && cx < x+w && cy >= y && cy < y+h
}

// sentCardSet makes the batch just filed the card, and says so in a notice when
// the terminal has no room to draw it.
func (a *App) sentCardSet(b sentBatch) {
	if a.sent.card != nil && a.sent.card.batch > b.seq {
		return
	}

	a.sent.cardSeq++
	a.sent.card = &sentCard{batch: b.seq, seq: a.sent.cardSeq}

	if _, _, _, _, _, ok := sentCardLayout(a.width, a.height, a.pictures.on()); !ok {
		key, _ := a.keymap.keyFor("images.view")
		a.notice = noticeSent(a.sentCardTitleOf(b), key)
	}
}

// sentCardArm starts the countdown of a card that can be seen with the window
// focused; the due message is the only thing that ends it.
func (a *App) sentCardArm() tea.Cmd {
	c := a.sent.card
	if c == nil || c.armed || !a.outerFocused || !a.sentCardShows() {
		return nil
	}

	c.armed = true
	seq := c.seq

	return a.tick(sentCardFor, func(time.Time) tea.Msg { return sentCardDueMsg{seq: seq} })
}

func (a *App) onSentCardDue(msg sentCardDueMsg) {
	if a.sent.card != nil && a.sent.card.seq == msg.seq {
		a.sent.card = nil
	}
}

// sentCardClick opens the viewer when the press lands on the card.
func (a *App) sentCardClick(m tea.Mouse) bool {
	if m.Button != tea.MouseLeft || !a.sentCardCovers(m.X, m.Y) {
		return false
	}

	if b, ok := a.sentCardBatch(); ok {
		a.showSentView(b.pane)
	} else {
		a.sent.card = nil
	}

	return true
}

// sentCardLines draws the card w wide with picRows picture rows (none in the
// text form), and the picture rows in the card's cells.
func (a *App) sentCardLines(lk look, b sentBatch, w, picRows int) (out []string, pics []picRow) {
	ui, st := lk.theme.UI, lk.styles
	tone := st.toastTone[levelNotice]
	pad := func(n int) string { return st.toastText.Render(strings.Repeat(" ", max(n, 0))) }
	content := w - 4

	row := func(body string, bw int) string {
		return tone.Render("│") + pad(1) + body + pad(content-bw+1) + tone.Render("│")
	}
	centred := func(s string, fg theme.Hex) string {
		s = ansi.Truncate(s, content, "…")
		sw := ansi.StringWidth(s)

		return row(pad((content-sw)/2)+lipgloss.NewStyle().Foreground(fg).Background(ui.Panel).Render(s), (content-sw)/2+sw)
	}

	title := " " + a.sentCardTitleOf(b) + " "
	if a.icons {
		title = " " + iconImage + title
	}

	title = ansi.Truncate(title, w-4, "…")
	out = append(out, tone.Render("╭─")+tone.Bold(true).Render(title)+tone.Render(strings.Repeat("─", w-3-ansi.StringWidth(title))+"╮"))

	var names []string
	for _, im := range b.images {
		names = append(names, printable(filepath.Base(im.path)))
	}

	caption := printable(b.caption)

	if picRows > 0 {
		var slots []sentSlot

		n := min(len(b.images), 2)
		slotW := content

		if n == 2 {
			slotW = (w - 6) / 2
		}

		used := n*slotW + (n-1)*sentGap
		lead := (content - used) / 2

		for _, im := range b.images[:n] {
			slots = append(slots, a.sentCardSlot(im, slotW, picRows, ui))
		}

		for r := range picRows {
			var sb strings.Builder

			sb.WriteString(pad(lead))

			for k, s := range slots {
				if k > 0 {
					sb.WriteString(pad(sentGap))
				}

				sb.WriteString(s.rows[r])
			}

			out = append(out, row(sb.String(), lead+used))
		}

		for k, s := range slots {
			if s.pic == nil {
				continue
			}

			for r := range s.pic.rows {
				pics = append(pics, picRow{X: 2 + lead + k*(slotW+sentGap) + s.pic.dx, Y: 1 + s.pic.dy + r, ID: s.pic.id, Row: r, Cols: s.pic.cols, Ground: ui.Panel})
			}
		}

		text := caption
		if text == "" {
			text = strings.Join(names[:n], ", ")
			if more := len(names) - n; more > 0 {
				text += "  +" + strconv.Itoa(more) + " more"
			}
		}

		out = append(out, centred(text, ui.Fg))
	} else {
		out = append(out, centred(strings.Join(names, ", "), ui.Fg), centred(caption, ui.Muted))
	}

	hint := ""
	if k, ok := a.keymap.keyFor("images.view"); ok {
		hint = hintRow(ui, [][2]string{{k, "view"}}, content)
	}

	out = append(out, row(hint, ansi.StringWidth(hint)))

	return append(out, tone.Render("╰"+strings.Repeat("─", w-2)+"╯")), pics
}

// sentCardSlot lays im out in slotW x rows cells: its picture centred, else its
// name with the reason or "loading…".
func (a *App) sentCardSlot(im sentImage, slotW, rows int, ui theme.UI) sentSlot {
	on := func(fg theme.Hex) lipgloss.Style { return lipgloss.NewStyle().Foreground(fg).Background(ui.Panel) }
	pad := func(n int) string { return on(ui.Fg).Render(strings.Repeat(" ", max(n, 0))) }

	out := make([]string, rows)
	for i := range out {
		out[i] = pad(slotW)
	}

	var text string

	if im.decoded() {
		sz := a.pictures.sizeIn(docPictures{reg: a.pictures, root: a.sent.dir}, im.copy, slotW, rows)
		if sz.Cols > 0 && sz.Rows > 0 {
			return sentSlot{rows: out, pic: &sentPic{id: sz.ID, cols: sz.Cols, rows: sz.Rows, dx: (slotW - sz.Cols) / 2, dy: (rows - sz.Rows) / 2}}
		}

		text = printable(filepath.Base(im.path))
		if sz.Loading {
			text = "loading…"
		}
	} else {
		text = printable(filepath.Base(im.path)) + ": " + printable(im.reason)
	}

	text = ansi.Truncate(text, slotW, "…")
	sw := ansi.StringWidth(text)
	out[(rows-1)/2] = pad((slotW-sw)/2) + on(ui.Fg).Render(text) + pad(slotW-sw-(slotW-sw)/2)

	return sentSlot{rows: out}
}

// overlaySentCard draws the card over base; it takes no keys of its own.
func (a *App) overlaySentCard(f *frameLayers, base string, lk look) string {
	if !a.sentCardShows() {
		return base
	}

	b, ok := a.sentCardBatch()
	if !ok {
		return base
	}

	x, y, w, _, picRows, _ := sentCardLayout(a.width, a.height, a.pictures.on())
	lines, pics := a.sentCardLines(lk, b, w, picRows)

	return overlayAt(f, base, a.width, a.height, x, y, lines, pics)
}
