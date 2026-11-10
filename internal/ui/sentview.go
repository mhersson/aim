package ui

import (
	"context"
	"path/filepath"
	"slices"
	"strconv"
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/mhersson/aim/internal/core"
	"github.com/mhersson/aim/internal/ui/theme"
)

const (
	// sentSideBySide is the box width from which a batch of two shows both.
	sentSideBySide = 80
	// sentChrome is the rows of the viewer's box that are not the picture area:
	// the borders, two blank rows, the caption and the hint row.
	sentChrome = 6
	// sentMinArea is the area rows a picture needs; fewer show the name instead.
	sentMinArea = 3
	// sentGap is the cells between two images.
	sentGap = 2

	noticeNoImages = "no agent has sent an image yet"
)

// sentView is the open image viewer: one agent's pages, on one of them.
type sentView struct {
	pane  core.PaneID
	batch int // seq of the shown page's batch
	part  int // which page of that batch
}

// sentPage is what one screen of the viewer shows: images of one batch, as
// indices into its images.
type sentPage struct {
	batch  sentBatch
	images []int
}

// sentPages lays batches (oldest first) out as pages, newest batch first. A
// batch of two, when wide, has a page with both and then one for each; any
// other image has a page of its own.
func sentPages(batches []sentBatch, wide bool) []sentPage {
	var pages []sentPage

	for _, b := range slices.Backward(batches) {
		if wide && len(b.images) == 2 {
			pages = append(pages, sentPage{b, []int{0, 1}})
		}

		for i := range b.images {
			pages = append(pages, sentPage{b, []int{i}})
		}
	}

	return pages
}

// sentIndicator says which images page at shows, among the images of all the
// batches. Pages of one batch repeat its images, so it counts batches, not pages.
func sentIndicator(pages []sentPage, at int) string {
	before, total := 0, 0

	for i, p := range pages {
		if i > 0 && pages[i-1].batch.seq == p.batch.seq {
			continue
		}

		if i < at && p.batch.seq != pages[at].batch.seq {
			before += len(p.batch.images)
		}

		total += len(p.batch.images)
	}

	first := before + pages[at].images[0] + 1
	n := strconv.Itoa(total)

	if k := len(pages[at].images); k > 1 {
		return " " + strconv.Itoa(first) + "-" + strconv.Itoa(first+k-1) + " of " + n + " "
	}

	return " " + strconv.Itoa(first) + " of " + n + " "
}

// sentBox is the viewer's box: the reading box, when it has room for the
// picture area.
func (a *App) sentBox() (x, y, w, h int, ok bool) {
	x, y, w, h, ok = planBox(a.width, a.height)

	return x, y, w, h, ok && h >= sentChrome+1
}

// sentPos is the viewer's pages and the one on show. A page the width no
// longer has gives the batch's last.
func (a *App) sentPos() (pages []sentPage, at int) {
	v := a.sent.view

	_, _, w, _, ok := a.sentBox()
	pages = sentPages(a.sentBatches(v.pane), ok && w >= sentSideBySide)
	part := 0

	for i, p := range pages {
		if p.batch.seq != v.batch {
			part = 0

			continue
		}

		at = i

		if part == v.part {
			break
		}

		part++
	}

	return pages, at
}

// sentRemember records page at as the one on show.
func (a *App) sentRemember(pages []sentPage, at int) {
	part := 0

	for _, p := range pages[:at] {
		if p.batch.seq == pages[at].batch.seq {
			part++
		}
	}

	a.sent.view.batch, a.sent.view.part = pages[at].batch.seq, part
}

// openSentView is images.view: the images of the agent on the card, else the
// focused agent's, else those of the agent that sent the newest.
func (a *App) openSentView() tea.Cmd {
	a.endLeader()

	id, newest := a.store.State().Tab().Focus, 0
	if b, ok := a.sentCardBatch(); ok {
		id = b.pane
	} else if len(a.sentBatches(id)) == 0 {
		id = ""

		for pid, sp := range a.sent.panes {
			if n := len(sp.batches); n > 0 && sp.batches[n-1].seq > newest {
				id, newest = pid, sp.batches[n-1].seq
			}
		}
	}

	if id == "" {
		a.notice = noticeNoImages

		return nil
	}

	a.showSentView(id)

	return nil
}

// showSentView opens the viewer on the newest page of pane id, which must have
// a batch.
func (a *App) showSentView(id core.PaneID) {
	b := a.sentBatches(id)
	if len(b) == 0 {
		return
	}

	a.sent.card = nil
	a.sent.view = &sentView{pane: id, batch: b[len(b)-1].seq}
}

// sentStep moves the viewer by d pages, towards older ones when d is positive.
func (a *App) sentStep(d int) {
	if _, _, _, _, ok := a.sentBox(); !ok || a.sent.view == nil {
		return
	}

	pages, at := a.sentPos()
	a.sentRemember(pages, min(max(at+d, 0), len(pages)-1))
}

// sentOpenedMsg answers the system opener: err is why it failed.
type sentOpenedMsg struct{ err error }

func noticeNotOpening(name, reason string) string {
	return "not opening " + name + ": " + reason
}

// openSentImages is images.open: AIM's copy of each image on the shown page
// goes to the system opener, which never sees a file AIM did not decode.
func (a *App) openSentImages() tea.Cmd {
	if _, _, _, _, ok := a.sentBox(); !ok || a.sent.view == nil {
		return nil
	}

	pages, at := a.sentPos()
	page := pages[at]

	var copies []string

	for _, i := range page.images {
		im := page.batch.images[i]
		if !im.decoded() {
			a.notice = noticeNotOpening(printable(filepath.Base(im.path)), printable(im.reason))

			return nil
		}

		copies = append(copies, im.copy)
	}

	open, ctx := a.openLink, a.ctx

	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(ctx, openTimeout)
		defer cancel()

		for _, c := range copies {
			// A broken opener fails every file; one toast says why.
			if err := open(ctx, c); err != nil {
				return sentOpenedMsg{err: err}
			}
		}

		return nil
	}
}

func (a *App) onSentOpened(msg sentOpenedMsg) {
	a.toast(levelError, "image viewer: "+gitReason(msg.err))
}

func sentViewCommands() []command {
	cmd := func(id, title string, run func(*App)) command {
		return command{id: id, title: "Image viewer: " + title, run: func(a *App) tea.Cmd {
			if a.sent.view != nil {
				run(a)
			}

			return nil
		}}
	}

	return append([]command{{
		id:    "images.view",
		icon:  iconImage,
		title: "Show images an agent sent",
		run:   (*App).openSentView,
	}}, scoped("images", []command{
		cmd("images.prev", "newer images", func(a *App) { a.sentStep(-1) }),
		cmd("images.next", "older images", func(a *App) { a.sentStep(1) }),
		cmd("images.close", "close", func(a *App) { a.sent.view = nil }),
		{id: "images.open", title: "Image viewer: open in your image viewer", run: (*App).openSentImages},
	})...)
}

// handleSentKey runs the command the viewer's keys bind to key; other keys are
// dropped.
func (a *App) handleSentKey(msg tea.KeyPressMsg) tea.Cmd {
	return a.runKey(a.keys.images, msg.String())
}

// sentHints are the viewer's hints as kt binds them; a pair with no key is left out.
func sentHints(kt keyTable) [][2]string {
	var pairs [][2]string

	for _, d := range []struct {
		label string
		ids   []string
	}{
		{"step", []string{"images.prev", "images.next"}},
		{"open", []string{"images.open"}},
		{"close", []string{"images.close"}},
	} {
		if k := kt.hint(d.ids...); k != "" {
			pairs = append(pairs, [2]string{k, d.label})
		}
	}

	return pairs
}

// sentSlot is one image's cells in the area: its rows, and the picture over
// them when pictures show it.
type sentSlot struct {
	rows []string
	pic  *sentPic
}

type sentPic struct {
	id             uint32
	cols, rows, dx int
	dy             int
}

// sentOpenHint says which key opens an image, or "" when none does.
func (a *App) sentOpenHint() string {
	if k := a.keys.images.hint("images.open"); k != "" {
		return k + " opens it in your image viewer"
	}

	return ""
}

// slotText is one centred line of a slot.
type slotText struct {
	s  string
	fg theme.Hex
}

// sentSlotCore lays im out in slotW x area cells: its picture centred when the
// registry answers one and area is at least minArea, else the lines text gives,
// centred. text is told whether the picture was tried and still loads.
func (a *App) sentSlotCore(im sentImage, slotW, area, minArea int, ui theme.UI, text func(tried, loading bool) []slotText) sentSlot {
	on := func(fg theme.Hex) lipgloss.Style { return lipgloss.NewStyle().Foreground(fg).Background(ui.Panel) }
	pad := func(n int) string { return on(ui.Fg).Render(strings.Repeat(" ", max(n, 0))) }

	rows := make([]string, area)
	for i := range rows {
		rows[i] = pad(slotW)
	}

	tried, loading := im.decoded() && area >= minArea, false

	if tried {
		sz := a.pictures.sizeIn(docPictures{reg: a.pictures, root: a.sent.dir}, im.copy, slotW, area)

		if sz.Cols > 0 && sz.Rows > 0 {
			return sentSlot{rows: rows, pic: &sentPic{id: sz.ID, cols: sz.Cols, rows: sz.Rows, dx: (slotW - sz.Cols) / 2, dy: (area - sz.Rows) / 2}}
		}

		loading = sz.Loading
	}

	lines := text(tried, loading)
	top := max((area-len(lines))/2, 0)

	for i, l := range lines {
		if top+i >= area {
			break
		}

		s := ansi.Truncate(l.s, slotW, "…")
		sw := ansi.StringWidth(s)
		rows[top+i] = pad((slotW-sw)/2) + on(l.fg).Render(s) + pad(slotW-sw-(slotW-sw)/2)
	}

	return sentSlot{rows: rows}
}

// sentSlotOf is a viewer slot: the picture, else the name with the open hint
// and the reason.
func (a *App) sentSlotOf(im sentImage, slotW, area int, ui theme.UI) sentSlot {
	return a.sentSlotCore(im, slotW, area, sentMinArea, ui, func(tried, loading bool) []slotText {
		lines := []slotText{{printable(filepath.Base(im.path)), ui.Fg}}
		hint := a.sentOpenHint()

		switch {
		case tried && loading:
			lines = append(lines, slotText{"loading…", ui.Muted})
		case hint != "" && im.decoded():
			lines = append(lines, slotText{hint, ui.Muted})
		}

		if !tried && im.reason != "" {
			lines = append(lines, slotText{printable(im.reason), ui.Muted})
		}

		return lines
	})
}

// sentLines draws the box w x h and the picture rows in the box's cells.
func (a *App) sentLines(lk look, w, h int) (out []string, pics []picRow) {
	ui := lk.theme.UI
	on := func(fg theme.Hex) lipgloss.Style { return lipgloss.NewStyle().Foreground(fg).Background(ui.Panel) }
	border := on(ui.BorderFocus)
	pad := func(n int) string { return on(ui.Fg).Render(strings.Repeat(" ", max(n, 0))) }

	pages, at := a.sentPos()
	page := pages[at]
	inner, area := w-2, h-sentChrome
	content := w - 4

	slotW := content
	if len(page.images) == 2 {
		slotW = (w - 6) / 2
	}

	used := len(page.images)*slotW + (len(page.images)-1)*sentGap
	lead := 1 + (content-used)/2

	row := func(body string, bw int) string {
		return border.Render("│") + body + pad(inner-bw) + border.Render("│")
	}

	var names []string
	for _, i := range page.images {
		names = append(names, printable(filepath.Base(page.batch.images[i].path)))
	}

	title := " " + printable(page.batch.name) + ": " + strings.Join(names, ", ") + " "
	if a.icons {
		title = " " + iconImage + title
	}

	title = ansi.Truncate(title, w-4, "…")
	out = append(out, titledTop(border, on(ui.Info).Bold(true), title, w), row("", 0))

	slots := make([]sentSlot, len(page.images))
	for k, i := range page.images {
		slots[k] = a.sentSlotOf(page.batch.images[i], slotW, area, ui)
	}

	for r := range area {
		var b strings.Builder

		b.WriteString(pad(lead))

		for k, s := range slots {
			if k > 0 {
				b.WriteString(pad(sentGap))
			}

			b.WriteString(s.rows[r])
		}

		out = append(out, row(b.String(), lead+used))
	}

	for k, s := range slots {
		if s.pic == nil {
			continue
		}

		x := 1 + lead + k*(slotW+sentGap) + s.pic.dx

		for r := range s.pic.rows {
			pics = append(pics, picRow{X: x, Y: 2 + s.pic.dy + r, ID: s.pic.id, Row: r, Cols: s.pic.cols, Ground: ui.Panel})
		}
	}

	caption := ansi.Truncate(printable(page.batch.caption), content, "…")
	cw := ansi.StringWidth(caption)

	hints := hintRow(ui, sentHints(a.keys.images), content-2)

	out = append(out,
		row("", 0),
		row(pad(1+(content-cw)/2)+on(ui.Muted).Render(caption), 1+(content-cw)/2+cw),
		row(pad(2)+hints, 2+ansi.StringWidth(hints)),
	)

	ind := sentIndicator(pages, at)

	return append(out, labelledBottom(border, on(ui.Accent).Bold(true), ind, w)), pics
}

// overlaySentView draws the viewer over base; a terminal too small for the box
// draws nothing, and the viewer stays open unseen.
func (a *App) overlaySentView(f *frameLayers, base string, lk look) string {
	x, y, w, h, ok := a.sentBox()
	if !ok {
		return base
	}

	lines, pics := a.sentLines(lk, w, h)

	return overlayAt(f, base, a.width, a.height, x, y, lines, pics)
}
