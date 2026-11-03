package ui

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"slices"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/mhersson/aim/internal/core"
)

// convReadTimeout bounds one read of a pull request's conversation.
const convReadTimeout = 30 * time.Second

// The conversation overlay's texts.
const (
	convLoading    = "loading the conversation…"
	convReadFailed = "could not read the conversation: %s"
)

// convView is the open conversation overlay: one pull request's description and
// timeline as a markdown view. It is UI state and is never saved. seq numbers
// the read whose answer it waits for; readAt is the update time of the pull
// request the shown conversation is as of, which a newer poll reads again.
type convView struct {
	repo    string
	number  int
	pr      core.PRInfo
	view    mdView
	doc     *convDoc
	seq     int
	loading bool
	err     string
	readAt  time.Time

	// chooser holds a line's several links. searching is the prompt being open,
	// typing into query; last is the query n and N repeat, and query stays
	// painted after enter.
	chooser     *linkChooser
	searching   bool
	query, last string
}

// convReadMsg answers one read of a conversation.
type convReadMsg struct {
	view *convView
	seq  int
	doc  *convDoc
	pr   core.PRInfo
	err  error
}

// openConversation opens the overlay on pull request number of repo (keyed
// "host/owner/name"), which shows at once and fills when the read answers. pr
// is what the caller knows of the pull request.
func (a *App) openConversation(repo string, number int, pr core.PRInfo) tea.Cmd {
	if a.reads == nil {
		a.notice = noticePRsOff

		return nil
	}

	a.endLeader()
	a.conv = &convView{repo: repo, number: number, pr: pr}
	a.prs.dirty = true
	a.sizeConv()

	return a.readConversation(a.conv)
}

// closeConv closes the overlay and drops any read still on its way.
func (a *App) closeConv() {
	if ts := a.text; ts != nil && a.conv != nil && ts.view == &a.conv.view {
		a.clearTextSel()
	}

	a.conv = nil
	a.prs.dirty = true
}

// readConversation reads and parses the conversation of v off the update loop,
// under the timeout and the app's context. An answer of an older read is stale.
func (a *App) readConversation(v *convView) tea.Cmd {
	v.seq++
	v.loading, v.err = true, ""

	repo, ok := repoOfKey(v.repo)
	if !ok || a.reads == nil {
		v.loading = false
		a.convFailed(v, "not available")

		return nil
	}

	if info, ok := prInfoByNumber(a.store.State(), v.repo, v.number); ok && info.UpdatedAt.After(v.readAt) {
		v.readAt = info.UpdatedAt
	}

	if v.doc != nil {
		a.pictures.retry(docPictures{reg: a.pictures, host: repo.Host}, v.view.layout())
	}

	var (
		seq, number = v.seq, v.number
		parent      = a.ctx
		reads       = a.reads
		now         = a.clock.Now()
		base        = a.prLink(core.ReviewPR{Repo: v.repo, Number: v.number})
		tok, hl     = a.drawer.hl.HighlightLang, a.drawer.hl.Highlight
	)

	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(parent, convReadTimeout)
		defer cancel()

		c, err := reads.Conversation(ctx, repo, number)
		if err != nil {
			return convReadMsg{view: v, seq: seq, err: err}
		}

		return convReadMsg{view: v, seq: seq, doc: parseConversation(c, base, tok, hl, now), pr: prInfoOf(c.PR)}
	}
}

// convFailed reports a failed read: in the notice when a conversation is on
// screen, else in place of the items.
func (a *App) convFailed(v *convView, reason string) {
	reason = printable(reason)
	if v.doc != nil {
		a.notice = fmt.Sprintf(convReadFailed, reason)

		return
	}

	v.err = reason
}

// onConvRead keeps a conversation's answer. One for an overlay that closed or
// a read a newer one superseded is dropped; a failed read leaves the
// conversation drawn and says why.
func (a *App) onConvRead(msg convReadMsg) {
	v := msg.view
	if a.conv != v || v == nil || v.seq != msg.seq {
		return
	}

	v.loading = false

	if msg.err != nil {
		// A cancelled read is the app shutting down: nothing to show.
		if !errors.Is(msg.err, context.Canceled) {
			a.convFailed(v, msg.err.Error())
		}

		return
	}

	v.err = ""
	// The chooser's targets are the old document's links.
	v.chooser = nil

	if msg.pr.Number != 0 {
		v.pr = msg.pr
		if msg.pr.UpdatedAt.After(v.readAt) {
			v.readAt = msg.pr.UpdatedAt
		}
	}

	host := ""
	if r, ok := repoOfKey(v.repo); ok {
		host = r.Host
	}

	msg.doc.withPictures(docPictures{reg: a.pictures, host: host})

	// The cursor keeps its item and its offset in it, and the top its distance
	// from the cursor.
	mv := &v.view

	var (
		id         string
		off, above int
	)

	if v.doc != nil {
		mv.layout()
		id, off = v.doc.itemAt(mv.cursor)
		above = mv.cursor - mv.top
	}

	msg.doc.carryOpen(v.doc)

	v.doc, mv.doc = msg.doc, msg.doc
	mv.layout()

	if line := msg.doc.lineOf(id, off); id != "" && line >= 0 {
		mv.cursor, mv.top = line, line-above
	}

	a.sizeConv()
	mv.clamp()
}

// convPoll reads the open conversation again when the poll of its repository
// lists the pull request updated since the conversation was read.
func (a *App) convPoll(st core.State, repo string) tea.Cmd {
	v := a.conv
	if v == nil || v.repo != repo {
		return nil
	}

	if info, ok := prInfoByNumber(st, repo, v.number); ok && info.UpdatedAt.After(v.readAt) {
		return a.readConversation(v)
	}

	return nil
}

// sizeConv sets the cells the view draws in, keeping the cursor on its item
// when the width changes. A box too small to draw leaves the view as it was.
func (a *App) sizeConv() {
	v := a.conv
	if v == nil {
		return
	}

	_, _, w, h, ok := planBox(a.width, a.height)
	if !ok {
		return
	}

	rows, _, _, _ := convRows(h)
	vw := min(w-4, planMaxView)
	mv := &v.view

	if mv.w != 0 && (mv.w != vw || mv.icons != a.icons) && v.doc != nil {
		mv.layout()
		id, off := v.doc.itemAt(mv.cursor)
		above := mv.cursor - mv.top

		mv.size(vw, rows, a.icons)
		mv.layout()

		if line := v.doc.lineOf(id, off); id != "" && line >= 0 {
			mv.cursor, mv.top = line, line-above
		}

		mv.clamp()

		return
	}

	mv.size(vw, rows, a.icons)
}

// handleConvKey answers the open conversation overlay. An open chooser or
// prompt closes on esc at any size; a box too small to draw answers
// reader.close alone.
func (a *App) handleConvKey(msg tea.KeyPressMsg) tea.Cmd {
	a.notice = ""
	key := bindingKey(msg)
	v := a.conv

	if key == "esc" && (v.chooser != nil || v.searching) {
		a.clearTextSel()

		if v.chooser != nil {
			v.chooser = nil
		} else {
			v.searching, v.query = false, ""
		}

		return nil
	}

	if _, _, _, _, ok := planBox(a.width, a.height); !ok {
		return a.readerClose(key)
	}

	a.sizeConv()

	switch {
	case v.chooser != nil:
		switch key {
		case "j", "down", "k", "up", "enter":
			a.clearTextSel()
		}

		return a.convChooserKey(key)
	case v.searching:
		if key == "enter" || key == "backspace" || msg.Text != "" {
			a.clearTextSel()
		}

		a.convSearchKey(msg)

		return nil
	}

	return a.readerKey(key)
}

// convCopy is y: the selection, kept, else the paragraph or item at the cursor.
func (a *App) convCopy() tea.Cmd {
	v := &a.conv.view
	if ts := a.text; ts != nil && ts.view == v {
		return a.clip.copy(a.textSelected())
	}

	return a.clip.copy(v.unitText(v.cursor))
}

// convEnter is enter on the view: a fold head toggles, one link opens in the
// browser, several open the chooser. Only web links are targets; the
// conversation has no checkout to resolve a path against.
func (a *App) convEnter() tea.Cmd {
	v := a.conv
	if v.view.fold() {
		return nil
	}

	targets := slices.DeleteFunc(v.view.targets(), func(t mdTarget) bool { return t.Path != "" || t.URL == "" })

	switch len(targets) {
	case 0:
	case 1:
		return a.openURL(targets[0].URL)
	default:
		v.chooser = &linkChooser{targets: targets}
	}

	return nil
}

// convChooserKey runs a key while the link chooser has the keyboard.
func (a *App) convChooserKey(key string) tea.Cmd {
	v := a.conv

	pick, done := v.chooser.key(key)
	if !done {
		return nil
	}

	v.chooser = nil

	if pick == nil {
		return nil
	}

	return a.openURL(pick.URL)
}

// convSearchKey types into the open search prompt.
func (a *App) convSearchKey(msg tea.KeyPressMsg) {
	v := a.conv

	switch key := msg.String(); {
	case key == "enter":
		v.searching = false
		a.convFind(cmp.Or(v.query, v.last), false)
	case key == "backspace":
		if r := []rune(v.query); len(r) > 0 {
			v.query = string(r[:len(r)-1])
		}
	case msg.Text != "":
		v.query += printable(msg.Text)
	}
}

// convPaste extends the search query while the prompt is open; any other paste
// is dropped.
func (a *App) convPaste(text string) {
	if v := a.conv; v.searching {
		v.query += printable(text)
	}
}

// convFind keeps q and moves to its next (or previous) match; a miss says so.
func (a *App) convFind(q string, back bool) {
	if q == "" {
		return
	}

	v := a.conv
	v.last, v.query = q, q

	find := v.view.find
	if back {
		find = v.view.findPrev
	}

	if v.doc != nil && !find(q) {
		a.notice = noticeNotFound + q
	}
}

// convItem puts the cursor on the first line of the next (dir 1) or the current
// item or the previous one (dir -1) and scrolls it to the top of the view.
func (a *App) convItem(dir int) {
	v := a.conv
	if v.doc == nil {
		return
	}

	mv := &v.view
	mv.layout()

	if line, ok := v.doc.itemLine(mv.cursor, dir); ok {
		mv.cursor, mv.top = line, line
		mv.clamp()
	}
}
