package ui

import (
	"context"
	"crypto/sha256"
	"errors"
	"path/filepath"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/mhersson/aim/internal/core"
	"github.com/mhersson/aim/internal/git"
	"github.com/mhersson/aim/internal/ui/palette"
)

const (
	// mdListTimeout bounds the listing of a worktree's markdown files.
	mdListTimeout = 5 * time.Second

	noticeNoMarkdown = "no markdown files in this worktree"
)

// mdOpenFile is the markdown file the centred overlay shows: repo is the
// repository's printable name and path the file's absolute path.
type mdOpenFile struct {
	repo, path string
	sum        [sha256.Size]byte // of the bytes the doc was parsed from
}

// mdPick names the worktree a markdown list was made for: its root and the
// repository's printable name.
type mdPick struct {
	root, repo string
}

// mdListMsg answers the listing of a worktree's markdown files. dir is the
// directory md.open listed from and seq the serial of the ask.
type mdListMsg struct {
	seq             int
	dir, root, repo string
	files           []string
	err             error
}

// mdFileMsg answers the read and parse of a picked file, or with reload the
// re-read of the shown one: view is the overlay asked, seq its reloadSeq, and
// same says the bytes matched the known sum, so doc is nil.
type mdFileMsg struct {
	seq              int
	reload, same     bool
	view             *planView
	root, repo, path string
	sum              [sha256.Size]byte
	doc              *mdDoc
	err              error
}

// mdOpenDir is the directory md.open lists from: what the open drawer shows
// while it has the keyboard, else the focused pane's followed place.
func (a *App) mdOpenDir(st core.State) string {
	tab := st.Tab()
	if tab.Review != nil && a.drawerHasKeys() {
		return a.drawerShows(st, tab)
	}

	return st.Panes[tab.Focus].FollowDir()
}

// mdOpenCommand is md.open: it lists the markdown files of the repository the
// operator is looking at in the palette. It leaves any drawer as it is.
func (a *App) mdOpenCommand() tea.Cmd {
	dir := a.mdOpenDir(a.store.State())
	if dir == "" {
		a.notice = noticeNotRepo

		return nil
	}

	a.planSeq++

	seq, parent := a.planSeq, a.ctx

	return func() tea.Msg {
		msg := mdListMsg{seq: seq, dir: dir}

		ctx, cancel := context.WithTimeout(parent, reviewOpenTimeout)
		defer cancel()

		repo, err := a.git.Open(ctx, dir)
		if err != nil {
			msg.err = err
			if !usableDir(dir) {
				msg.err = errWorktreeRemoved{root: dir}
			}

			return msg
		}

		msg.root, msg.repo = repo.Root, printable(repo.Name())

		lctx, lcancel := context.WithTimeout(parent, mdListTimeout)
		defer lcancel()

		msg.files, msg.err = git.MarkdownFiles(lctx, repo.Root)

		return msg
	}
}

// onMDList opens the palette on the files listed. An answer after a newer ask,
// after the place md.open lists from changed, or under the compose box or
// another modal above the drawer is dropped.
func (a *App) onMDList(msg mdListMsg) {
	if msg.seq != a.planSeq || a.mdOpenDir(a.store.State()) != msg.dir || a.composeShown() || a.aboveDrawerModalOpen() {
		return
	}

	var gone errWorktreeRemoved

	switch {
	case errors.Is(msg.err, context.Canceled):
		return
	case errors.As(msg.err, &gone):
		a.notice = noticeRemoved + shortPath(gone.root, "", a.getenv("HOME"))

		return
	case errors.Is(msg.err, git.ErrNotRepo):
		a.notice = noticeNotRepo

		return
	case msg.err != nil:
		a.log.Warn("markdown files", "dir", msg.dir, "err", msg.err)
		a.notice = "markdown files: " + printable(msg.err.Error())

		return
	case len(msg.files) == 0:
		a.notice = noticeNoMarkdown

		return
	}

	items := make([]palette.Item, len(msg.files))
	for i, f := range msg.files {
		items[i] = palette.Item{ID: f, Title: printable(f)}
	}

	o := a.openOverlay(overlayMarkdown, a.titled(iconMarkdown, "Markdown files"), items)
	o.pal = o.pal.WithEnterLabel("open")
	o.pick = &mdPick{root: msg.root, repo: msg.repo}
}

// pickMarkdown reads the file picked from the list, off the update loop; the
// overlay shows when the answer lands.
func (a *App) pickMarkdown(pick *mdPick, rel string) tea.Cmd {
	if pick == nil {
		return nil
	}

	a.planSeq++

	path := filepath.Join(pick.root, filepath.FromSlash(rel))
	msg := mdFileMsg{seq: a.planSeq, root: pick.root, repo: pick.repo, path: path}
	parent, tok := a.ctx, a.drawer.hl.HighlightLang

	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(parent, mdReadTimeout)
		defer cancel()

		src, err := readLocalMarkdown(ctx, msg.root, msg.path)
		if err != nil {
			msg.err = err

			return msg
		}

		msg.sum = sha256.Sum256(src)
		msg.doc = parseMarkdown(src, filepath.Dir(msg.path), tok)

		return msg
	}
}

// onMDFile shows the file once it was read, or hands a re-read to onMDReload.
// An answer for an ask a newer one superseded, or one that lands under the
// compose box or another modal, is dropped.
func (a *App) onMDFile(msg mdFileMsg) {
	if msg.reload {
		a.onMDReload(msg)

		return
	}

	if msg.seq != a.planSeq || a.composeShown() || a.aboveDrawerModalOpen() {
		return
	}

	if msg.err != nil {
		if !errors.Is(msg.err, context.Canceled) {
			a.notice = markdownNotice(msg.root, msg.path, a.getenv("HOME"), msg.err)
		}

		return
	}

	msg.doc.withPictures(docPictures{reg: a.pictures, root: msg.root})
	a.endLeader()
	a.plan = &planView{
		file: &mdOpenFile{repo: msg.repo, path: msg.path, sum: msg.sum}, root: msg.root, base: filepath.Dir(msg.path),
		view: mdView{doc: msg.doc},
	}
	a.sizePlan()
}

// shownFile is the path and known sum of the doc the overlay shows: the top
// page's, else the file's.
func (pv *planView) shownFile() (path string, sum *[sha256.Size]byte) {
	if n := len(pv.pages); n > 0 {
		p := pv.pages[n-1]

		return p.path, &p.sum
	}

	return pv.file.path, &pv.file.sum
}

// mdCheck reads the shown doc again off the update loop. The answer parses it
// only when its bytes differ from the known sum; the newest check wins.
func (a *App) mdCheck(pv *planView) tea.Cmd {
	pv.reloadSeq++

	path, sum := pv.shownFile()
	msg := mdFileMsg{seq: pv.reloadSeq, reload: true, view: pv, root: pv.root, path: path}
	known, parent, tok := *sum, a.ctx, a.drawer.hl.HighlightLang

	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(parent, mdReadTimeout)
		defer cancel()

		src, err := readLocalMarkdown(ctx, msg.root, msg.path)
		if err != nil {
			msg.err = err

			return msg
		}

		msg.sum = sha256.Sum256(src)
		if msg.sum == known {
			msg.same = true

			return msg
		}

		msg.doc = parseMarkdown(src, filepath.Dir(msg.path), tok)

		return msg
	}
}

// mdBatch re-checks the shown doc when a file overlay is open on root.
func (a *App) mdBatch(root string) tea.Cmd {
	if pv := a.plan; pv != nil && pv.file != nil && pv.root == root {
		return tea.Batch(a.pictures.stale(docPictures{reg: a.pictures, root: pv.root}, pv.shown().layout()), a.mdCheck(pv))
	}

	return nil
}

// onMDReload swaps in a re-read doc, keeping the view's place. An answer for
// an overlay that closed, a check a newer one superseded or a doc no longer
// shown is dropped; a failed read keeps the old doc, and says why only when the operator asked.
func (a *App) onMDReload(msg mdFileMsg) {
	pv := a.plan
	if pv == nil || pv.file == nil && len(pv.pages) == 0 || msg.view != pv || msg.seq != pv.reloadSeq {
		return
	}

	path, sum := pv.shownFile()
	if msg.path != path {
		return
	}

	if msg.err != nil {
		if msg.seq == pv.askSeq && !errors.Is(msg.err, context.Canceled) {
			a.notice = markdownNotice(pv.root, path, a.getenv("HOME"), msg.err)
		}

		return
	}

	if msg.same {
		return
	}

	msg.doc.withPictures(docPictures{reg: a.pictures, root: pv.root})

	v := pv.shown()
	old := v.layout() // keeps the cursor in range, so top and cursor are read after it
	v.top, v.cursor = keepPlace(old, msg.doc.layout(v.textWidth(), v.icons), v.top, v.cursor)
	v.doc = msg.doc
	*sum = msg.sum

	a.sizePlan()
}
