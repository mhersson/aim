package ui

import (
	"context"
	"crypto/sha256"
	"errors"
	"path/filepath"

	tea "charm.land/bubbletea/v2"

	"github.com/mhersson/aim/internal/core"
)

// planPage is a markdown file followed from the plan overlay, shown over the
// plan; path is absolute.
type planPage struct {
	path string
	sum  [sha256.Size]byte // of the bytes the doc was parsed from
	view mdView
}

// planPageMsg answers the read and parse of a page. view is the overlay it was
// asked from and seq the serial of the ask.
type planPageMsg struct {
	view *planView
	seq  int
	path string
	sum  [sha256.Size]byte
	doc  *mdDoc
	err  error
}

// shown is the view that draws and takes the keys: the top page's, else the
// plan's own.
func (pv *planView) shown() *mdView {
	if n := len(pv.pages); n > 0 {
		return &pv.pages[n-1].view
	}

	return &pv.view
}

// shownPath is the path of the file the overlay shows: the top page's, else the
// markdown file's, else the plan's.
func (pv *planView) shownPath() string {
	switch {
	case len(pv.pages) > 0:
		return pv.pages[len(pv.pages)-1].path
	case pv.file != nil:
		return pv.file.path
	}

	return pv.plan.Path
}

// planEnter is enter on the shown view: a fold head toggles, one target is
// followed, several open the chooser, none does nothing.
func (a *App) planEnter() tea.Cmd {
	pv := a.plan
	v := pv.shown()

	if v.fold() {
		return nil
	}

	targets := v.targets()

	switch len(targets) {
	case 0:
		return nil
	case 1:
		return a.planFollow(targets[0])
	}

	pv.chooser = &linkChooser{targets: targets, root: pv.root}

	return nil
}

// planFollow follows a target inside the overlay's root. A file that is not
// markdown reports back with the overlay so that it closes only if the editor opens.
func (a *App) planFollow(t mdTarget) tea.Cmd {
	pv := a.plan

	cmd := a.followTarget(pv.root, t, a.planOpenPage)
	if cmd == nil {
		return nil
	}

	return func() tea.Msg {
		msg := cmd()
		if e, ok := msg.(linkEditMsg); ok {
			e.plan = pv

			return e
		}

		return msg
	}
}

// planChooserKey runs a key while the chooser has the keyboard.
func (a *App) planChooserKey(msg tea.KeyPressMsg) tea.Cmd {
	pv := a.plan

	pick, done := pv.chooser.key(bindingKey(msg))
	if !done {
		return nil
	}

	pv.chooser = nil

	if pick == nil {
		return nil
	}

	return a.planFollow(*pick)
}

// planOpenPage reads and parses the markdown file at path (absolute) under the
// overlay's root, off the update loop. Only the newest ask is answered.
func (a *App) planOpenPage(path string) tea.Cmd {
	pv := a.plan
	pv.pageSeq++

	msg := planPageMsg{view: pv, seq: pv.pageSeq, path: filepath.Clean(path)}
	root, parent, tok := pv.root, a.ctx, a.drawer.hl.HighlightLang

	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(parent, mdReadTimeout)
		defer cancel()

		src, err := readLocalMarkdown(ctx, root, msg.path)
		if err != nil {
			msg.err = err

			return msg
		}

		msg.sum = sha256.Sum256(src)
		msg.doc = parseMarkdown(src, filepath.Dir(msg.path), tok)

		return msg
	}
}

// onPlanPage pushes a page once it was read. An answer for an overlay that
// closed, or for an ask a newer one or a pop superseded, is dropped.
func (a *App) onPlanPage(msg planPageMsg) {
	pv := a.plan
	if pv == nil || msg.view != pv || msg.seq != pv.pageSeq {
		return
	}

	if msg.err != nil {
		if !errors.Is(msg.err, context.Canceled) {
			a.notice = markdownNotice(pv.root, msg.path, a.getenv("HOME"), msg.err)
		}

		return
	}

	msg.doc.withPictures(docPictures{reg: a.pictures, root: pv.root})
	pv.pages = append(pv.pages, &planPage{path: msg.path, sum: msg.sum, view: mdView{doc: msg.doc}})
	pv.chooser = nil

	a.sizePlan()
}

// planPop drops the shown page and any page read still on its way. In a file
// overlay it also checks the doc it uncovers, which may have changed meanwhile.
func (a *App) planPop() tea.Cmd {
	pv := a.plan
	pv.pages = pv.pages[:len(pv.pages)-1]
	pv.pageSeq++

	if pv.file == nil {
		return nil
	}

	return a.mdCheck(pv)
}

// onPlanLinkEdit opens the editor on a file followed from the overlay, closing
// the overlay once the pane opens, or says why not and keeps it. An answer for
// an overlay that closed meanwhile is dropped.
func (a *App) onPlanLinkEdit(msg linkEditMsg) tea.Cmd {
	if a.plan != msg.plan || errors.Is(msg.err, context.Canceled) {
		return nil
	}

	if msg.err != nil {
		a.notice = markdownNotice(msg.root, msg.path, a.getenv("HOME"), msg.err)

		return nil
	}

	a.closePlan()
	a.closeFinder()

	run := &paneRun{program: reviewEditor(a.getenv), args: []string{"+1", msg.path}}
	cmd, _ := a.openPaneAt(core.KindShell, core.SplitVertical, a.store.State().Tab().Focus, msg.root, run)

	return cmd
}
