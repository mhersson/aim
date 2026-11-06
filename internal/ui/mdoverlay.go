package ui

import (
	"context"
	"crypto/sha256"
	"errors"
	"path/filepath"

	tea "charm.land/bubbletea/v2"

	"github.com/mhersson/aim/internal/core"
	"github.com/mhersson/aim/internal/git"
)

// mdOpenFile is the markdown file the centred overlay shows: repo is the
// repository's printable name, empty outside any, and path the file's absolute
// path.
type mdOpenFile struct {
	repo, path string
	sum        [sha256.Size]byte // of the bytes the doc was parsed from
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

// mdOpenDir is the directory md.open starts in: what the open drawer shows
// while it has the keyboard, else the focused pane's followed place.
func (a *App) mdOpenDir(st core.State) string {
	tab := st.Tab()
	if tab.Review != nil && a.drawerHasKeys() {
		return a.drawerShows(st, tab)
	}

	return st.Panes[tab.Focus].FollowDir()
}

// openFinderFile reads the file picked in the finder off the update loop; the
// reading box shows when the answer lands. The file's root is the repository it
// is in, else its own folder, found after resolving symlinks in the path.
func (a *App) openFinderFile(path string) tea.Cmd {
	a.planSeq++

	seq, parent, tok := a.planSeq, a.ctx, a.drawer.hl.HighlightLang

	return func() tea.Msg {
		msg := mdFileMsg{seq: seq, root: filepath.Dir(path), path: path}

		ctx, cancel := context.WithTimeout(parent, mdReadTimeout)
		defer cancel()

		resolved, err := filepath.EvalSymlinks(path)
		if err != nil {
			msg.err = err

			return msg
		}

		msg.path, msg.root = resolved, filepath.Dir(resolved)
		switch repo, err := a.git.Open(ctx, msg.root); {
		case err == nil:
			msg.root, msg.repo = repo.Root, printable(repo.Name())
		case !errors.Is(err, git.ErrNotRepo) && !errors.Is(err, context.Canceled):
			a.log.Warn("find the repository of a markdown file", "path", msg.path, "err", err)
		}

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
// An answer for an ask a newer one or an esc superseded, one for a finder that
// closed, or one that lands under the compose box or another modal, is dropped.
func (a *App) onMDFile(msg mdFileMsg) {
	if msg.reload {
		a.onMDReload(msg)

		return
	}

	if msg.seq != a.planSeq || a.finder == nil || a.composeShown() || a.modalOverFinder() {
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
