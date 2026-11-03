package ui

import (
	"cmp"
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"path/filepath"
	"slices"
	"strings"

	tea "charm.land/bubbletea/v2"

	"github.com/mhersson/aim/internal/core"
	"github.com/mhersson/aim/internal/md"
)

// The plan overlay's notices.
const (
	noticeNoPlan   = "no plan from %s yet"
	noticeNotAgent = "the focused pane is not an agent"
)

// planView is the open plan overlay: what an agent's plan file read as, or,
// with file set, a markdown file picked by md.open. It is UI state and is never
// saved. root is where the plan reads and its links follow; base is what its
// relative links resolve against.
type planView struct {
	// file is the markdown file md.open opened, nil for an agent's plan.
	file       *mdOpenFile
	pane       core.PaneID
	agent      string
	plan       core.Plan
	root, base string
	view       mdView

	// sum is the hash of the plan bytes view.doc was parsed from; a file
	// overlay keeps its sum in file.
	sum [sha256.Size]byte

	// pages are the markdown files followed from the plan, the last one shown;
	// pageSeq is the serial of the newest page ask.
	pages   []*planPage
	pageSeq int
	chooser *linkChooser

	// reloadSeq is the serial of the newest reload ask; askSeq is the serial of
	// the newest ask the operator made of a page or file, the only one whose
	// failure speaks. rewritten is set by a reload and cleared by the next key.
	reloadSeq int
	askSeq    int
	rewritten bool

	// searching is the prompt being open, typing into query; last is the query
	// n and N repeat. query stays painted after enter.
	searching   bool
	query, last string
}

// planReadMsg answers the read and parse of a plan; seq is the serial of the
// ask. A reload is asked of the open overlay view, under its reloadSeq.
type planReadMsg struct {
	seq        int
	reload     bool
	again      bool // a read the operator asked for, not a rewrite
	known, sum [sha256.Size]byte
	same       bool // an asked read found the bytes of known
	view       *planView
	pane       core.PaneID
	plan       core.Plan
	root, base string
	doc        *mdDoc
	err        error
}

// planReadRoot is the folder a plan file is read through: a plan file's root,
// else the folder of a plan-mode plan, which lies outside any worktree.
func planReadRoot(p core.Plan) string {
	if p.Kind == core.PlanFile {
		return p.Root
	}

	return filepath.Dir(p.Path)
}

// planCommand is plan.open: the focused pane's plan.
func (a *App) planCommand() tea.Cmd { return a.openPlan(a.store.State().Tab().Focus) }

// openPlan reads the latest plan of agent id off the update loop; the overlay
// shows when the answer lands. A plan-mode plan's links resolve against and
// follow inside the agent's working directory, since Claude writes them
// relative to the project.
func (a *App) openPlan(id core.PaneID) tea.Cmd {
	a.endLeader()

	st := a.store.State()

	m, ok := st.Panes[id]
	switch {
	case !ok || m.Kind != core.KindAgent:
		a.notice = noticeNotAgent

		return nil
	case m.Plan.Path == "":
		a.notice = fmt.Sprintf(noticeNoPlan, a.cardName(st, id))

		return nil
	}

	root, base := m.Plan.Root, filepath.Dir(m.Plan.Path)
	if m.Plan.Kind == core.PlanMode {
		root = cmp.Or(m.WorkDir(), base)
		base = root
	}

	a.planSeq++

	return a.readPlanCmd(planReadMsg{seq: a.planSeq, pane: id, plan: m.Plan, root: root, base: base})
}

// readPlanCmd reads and parses the plan msg names and answers with msg filled in.
func (a *App) readPlanCmd(msg planReadMsg) tea.Cmd {
	parent, tok := a.ctx, a.drawer.hl.HighlightLang

	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(parent, mdReadTimeout)
		defer cancel()

		src, err := readLocalMarkdown(ctx, planReadRoot(msg.plan), msg.plan.Path)
		if err != nil {
			msg.err = err

			return msg
		}

		msg.sum = sha256.Sum256(src)
		if msg.again && msg.sum == msg.known {
			msg.same = true

			return msg
		}

		msg.doc = parseMarkdown(src, msg.base, tok)

		return msg
	}
}

// onPlanRead shows a plan once it was read. An answer for an ask a newer one or
// an esc superseded, one that lands under another modal, or one for a pane that
// is gone or no longer an agent is dropped.
func (a *App) onPlanRead(msg planReadMsg) {
	if msg.reload {
		a.onPlanReload(msg)

		return
	}

	m, ok := a.store.State().Panes[msg.pane]
	if msg.seq != a.planSeq || a.composeShown() || a.aboveDrawerModalOpen() || !ok || m.Kind != core.KindAgent {
		return
	}

	if msg.err != nil {
		if !errors.Is(msg.err, context.Canceled) {
			a.notice = markdownNotice(planReadRoot(msg.plan), msg.plan.Path, a.getenv("HOME"), msg.err)
		}

		return
	}

	msg.doc.withPictures(docPictures{reg: a.pictures, root: msg.root})
	a.endLeader()
	a.plan = &planView{
		pane: msg.pane, agent: printable(a.cardName(a.store.State(), msg.pane)),
		plan: msg.plan, root: msg.root, base: msg.base, view: mdView{doc: msg.doc}, sum: msg.sum,
	}
	a.sizePlan()

	// A newer plan noted meanwhile stays unread.
	if m.Plan.Path == msg.plan.Path {
		// dispatch logs its own failures.
		_ = a.dispatch(core.ReadPlan{ID: msg.pane})
	}
}

// planShowing reports whether the overlay is open on pane id's plan at path,
// pages above it included.
func (a *App) planShowing(id core.PaneID, path string) bool {
	return a.plan != nil && a.plan.pane == id && a.plan.plan.Path == path
}

// reloadPlan reads plan p again for the open overlay, off the update loop; again
// says the operator asked for it, so it is not marked rewritten.
func (a *App) reloadPlan(p core.Plan, again bool) tea.Cmd {
	pv := a.plan
	pv.reloadSeq++

	a.pictures.retry(docPictures{reg: a.pictures, root: pv.root}, pv.view.layout())

	return a.readPlanCmd(planReadMsg{seq: pv.reloadSeq, reload: true, again: again, known: pv.sum, view: pv, pane: pv.pane, plan: p, root: pv.root, base: pv.base})
}

// onPlanReload swaps in a rewritten plan, keeping the view's place. An answer
// for an overlay that closed, or for a reload a newer one superseded, is
// dropped; a failed read keeps the old doc and says why.
func (a *App) onPlanReload(msg planReadMsg) {
	pv := a.plan
	if pv == nil || msg.view != pv || msg.seq != pv.reloadSeq {
		return
	}

	if msg.err != nil {
		if !errors.Is(msg.err, context.Canceled) {
			a.notice = markdownNotice(planReadRoot(msg.plan), msg.plan.Path, a.getenv("HOME"), msg.err)
		}

		return
	}

	if msg.same {
		return
	}

	msg.doc.withPictures(docPictures{reg: a.pictures, root: pv.root})

	v := &pv.view
	old := v.layout() // keeps the cursor in range, so top and cursor are read after it
	v.top, v.cursor = keepPlace(old, msg.doc.layout(v.textWidth(), v.icons), v.top, v.cursor)
	v.doc = msg.doc
	pv.plan, pv.sum = msg.plan, msg.sum
	pv.rewritten = pv.rewritten || !msg.again

	a.sizePlan()
}

// keepPlace maps a view's top line and cursor from the old layout onto the
// new: the heading at or above the top is found again by its text and
// occurrence and the top keeps its offset below it; else the top keeps its
// source line. The cursor keeps its offset from the top.
func keepPlace(old, next md.Layout, top, cursor int) (newTop, newCursor int) {
	newTop = -1

	if text, nth, at, ok := headingAbove(old, top); ok {
		if i, found := findHeading(next, text, nth); found {
			newTop = i + top - at
		}
	}

	if newTop < 0 {
		newTop = sourceLineIndex(next.Source, blockEditLine(old.Source, top))
	}

	last := max(len(next.Lines)-1, 0)
	newTop = max(min(newTop, last), 0)

	return newTop, max(min(newTop+cursor-top, last), 0)
}

// headings lists the first line of each heading in lay with its text.
func headings(lay md.Layout) (at []int, text []string) {
	for i, l := range lay.Lines {
		if !isHeadingLine(l) || i > 0 && isHeadingLine(lay.Lines[i-1]) && lay.Source[i] == lay.Source[i-1] {
			continue
		}

		var b strings.Builder

		for j := i; j < len(lay.Lines) && isHeadingLine(lay.Lines[j]) && lay.Source[j] == lay.Source[i]; j++ {
			for _, r := range lay.Lines[j] {
				b.WriteString(r.Text)
			}
		}

		at, text = append(at, i), append(text, b.String())
	}

	return at, text
}

// headingAbove is the last heading starting at or above line top: its text,
// which occurrence of that text it is, and its line.
func headingAbove(lay md.Layout, top int) (text string, nth, at int, ok bool) {
	lines, texts := headings(lay)

	for k, line := range slices.Backward(lines) {
		if line <= top {
			return texts[k], countText(texts[:k], texts[k]), line, true
		}
	}

	return "", 0, 0, false
}

// findHeading is the line of the nth heading reading text.
func findHeading(lay md.Layout, text string, nth int) (int, bool) {
	lines, texts := headings(lay)

	for k, t := range texts {
		if t == text {
			if nth == 0 {
				return lines[k], true
			}

			nth--
		}
	}

	return 0, false
}

func countText(texts []string, text string) int {
	n := 0

	for _, t := range texts {
		if t == text {
			n++
		}
	}

	return n
}

// closePlan closes the overlay and drops any read still on its way.
func (a *App) closePlan() {
	if ts := a.text; ts != nil && ts.view != nil && ts.tab == "" && (a.conv == nil || ts.view != &a.conv.view) {
		a.clearTextSel()
	}

	a.plan = nil
	a.planSeq++
}

// sizePlan sets the cells the view draws in, keeping the cursor on its source
// line when the width changes. A box too small to draw leaves the view as it was.
func (a *App) sizePlan() {
	v := a.plan
	if v == nil {
		return
	}

	_, _, w, h, ok := planBox(a.width, a.height)
	if !ok {
		return
	}

	rows, _, _ := planRows(h)
	vw := min(w-4, planMaxView)

	views := []*mdView{&v.view}
	for _, p := range v.pages {
		views = append(views, &p.view)
	}

	for _, mv := range views {
		if mv.w != 0 && (mv.w != vw || mv.icons != a.icons) {
			line := mv.sourceLine()
			mv.size(vw, rows, a.icons)
			mv.goSource(line)

			continue
		}

		mv.size(vw, rows, a.icons)
	}
}

// handlePlanKey answers the open plan overlay. An open chooser or prompt
// closes on esc at any size; a box too small to draw answers reader.close alone.
func (a *App) handlePlanKey(msg tea.KeyPressMsg) tea.Cmd {
	a.notice = ""
	key := bindingKey(msg)

	pv := a.plan
	pv.rewritten = false

	if key == "esc" && (pv.chooser != nil || pv.searching) {
		a.clearTextSel()

		if pv.chooser != nil {
			pv.chooser = nil
		} else {
			pv.searching, pv.query = false, ""
		}

		return nil
	}

	if _, _, _, _, ok := planBox(a.width, a.height); !ok {
		return a.readerClose(key)
	}

	if pv.chooser != nil {
		switch key {
		case "j", "down", "k", "up", "enter":
			a.clearTextSel()
		}

		return a.planChooserKey(msg)
	}

	if pv.searching {
		if key == "enter" || key == "backspace" || msg.Text != "" {
			a.clearTextSel()
		}

		a.planSearchKey(msg)

		return nil
	}

	return a.readerKey(key)
}

// planCopy is y: the selection, kept, else the paragraph or item at the cursor.
func (a *App) planCopy() tea.Cmd {
	v := a.plan.shown()
	if ts := a.text; ts != nil && ts.view == v {
		return a.clip.copy(a.textSelected())
	}

	return a.clip.copy(v.unitText(v.cursor))
}

// planSearchKey types into the open search prompt.
func (a *App) planSearchKey(msg tea.KeyPressMsg) {
	pv := a.plan

	switch key := msg.String(); {
	case key == "enter":
		pv.searching = false
		a.planFind(cmp.Or(pv.query, pv.last), false)
	case key == "backspace":
		if r := []rune(pv.query); len(r) > 0 {
			pv.query = string(r[:len(r)-1])
		}
	case msg.Text != "":
		pv.query += printable(msg.Text)
	}
}

// planPaste extends the search query while the prompt is open; any other paste
// is dropped.
func (a *App) planPaste(text string) {
	if pv := a.plan; pv.searching {
		pv.query += printable(text)
	}
}

// planFind keeps q and moves to its next (or previous) match; a miss says so.
func (a *App) planFind(q string, back bool) {
	if q == "" {
		return
	}

	pv := a.plan

	pv.last, pv.query = q, q

	v := pv.shown()

	find := v.find
	if back {
		find = v.findPrev
	}

	if !find(q) {
		a.notice = noticeNotFound + q
	}
}
