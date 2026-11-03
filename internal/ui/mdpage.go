package ui

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/mhersson/aim/internal/core"
	"github.com/mhersson/aim/internal/md"
)

// mdReadTimeout bounds the read and parse of one page.
const mdReadTimeout = 10 * time.Second

var (
	errOutsideWorktree = errors.New("outside the worktree")
	errIsFolder        = errors.New("it is a folder")
)

// mdPage is a markdown file shown over the drawer's diff column. path is
// absolute and rel is path relative to the drawer's root, with slashes.
type mdPage struct {
	path, rel string
	view      mdView
	rev       int  // bumps whenever what the page draws changes, for the drawing's cache key
	stale     bool // the file may have changed while another page covered it
	sum       [sha256.Size]byte
}

// mdPageMsg answers the read and parse of a page. gen is the view's epoch and
// seq the serial of the ask; reload says it re-reads the page that shows.
type mdPageMsg struct {
	tab    core.TabID
	gen    int
	seq    int
	path   string
	doc    *mdDoc
	err    error
	reload bool
	sum    [sha256.Size]byte
	same   bool // a check found the file as the page has it, and parsed nothing
}

// page is the page that shows, nil for none.
func (v *reviewView) page() *mdPage {
	if len(v.pages) == 0 {
		return nil
	}

	return v.pages[len(v.pages)-1]
}

// resize sets the cells the page draws in.
func (p *mdPage) resize(w, h int, icons bool) {
	if p.view.w == w && p.view.h == h && p.view.icons == icons {
		return
	}

	p.view.size(w, h, icons)
	p.rev++
}

// scroll moves the window n lines; the cursor follows when it would leave it.
func (p *mdPage) scroll(n int) {
	p.view.scroll(n)
	p.rev++
}

// anchorLine is the source line to keep on screen across a reload: the cursor
// line's, else the nearest line above it that has one.
func (p *mdPage) anchorLine() int {
	src := p.view.layout().Source

	for i := min(p.view.cursor, len(src)-1); i >= 0; i-- {
		if src[i].First != 0 {
			return src[i].First
		}
	}

	return 1
}

// readLocalMarkdown reads a file of the worktree root, at most md.MaxBytes+1
// bytes so that Parse can tell it was cut. A relative path is taken from root.
// A path that leaves root lexically is errOutsideWorktree, and a symlink that
// leaves it is an error: the read goes through os.Root, which resolves nothing
// outside it.
func readLocalMarkdown(ctx context.Context, root, path string) ([]byte, error) {
	if !filepath.IsAbs(path) {
		path = filepath.Join(root, path)
	}

	rel, err := filepath.Rel(root, path)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return nil, errOutsideWorktree
	}

	if err := ctx.Err(); err != nil {
		return nil, fmt.Errorf("read markdown: %w", err)
	}

	r, err := os.OpenRoot(root)
	if err != nil {
		return nil, fmt.Errorf("open the worktree: %w", err)
	}
	defer r.Close()

	info, err := r.Stat(rel)
	if err != nil {
		return nil, err //nolint:wrapcheck // the *PathError names the path and reason markdownNotice shows
	}

	switch {
	case info.IsDir():
		return nil, errIsFolder
	case !info.Mode().IsRegular():
		return nil, errors.New("not a regular file")
	}

	f, err := r.Open(rel)
	if err != nil {
		return nil, err //nolint:wrapcheck // as above
	}
	defer f.Close()

	data, err := io.ReadAll(io.LimitReader(f, md.MaxBytes+1))
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", rel, err)
	}

	return data, nil
}

// markdownNotice is what a page that did not open says: the path relative to
// root, or with ~ for the home directory when it lies outside, and why.
func markdownNotice(root, path, home string, err error) string {
	rel := path
	if r, ok := within(root, path); ok {
		rel = r
	}

	if errors.Is(err, errOutsideWorktree) {
		return "not opening " + shortPath(path, "", home) + ": " + errOutsideWorktree.Error()
	}

	reason := ""

	var pe *fs.PathError

	switch {
	case errors.Is(err, fs.ErrNotExist):
		reason = "no such file"
	case errors.Is(err, errIsFolder):
		reason = errIsFolder.Error()
	case errors.As(err, &pe):
		reason = pe.Err.Error()
	default:
		reason = err.Error()
	}

	return "not opening " + printable(rel) + ": " + printable(reason)
}

// relSlash is path relative to root with slashes; path itself when it is not under root.
func relSlash(root, path string) string {
	if rel, ok := within(root, path); ok {
		return filepath.ToSlash(rel)
	}

	return filepath.ToSlash(path)
}

// ---- opening ----

// openMarkdownPage reads and parses the file at path, off the update loop, and
// pushes it as a page on top of the drawer's stack. Only the newest ask is
// answered; the drawer's other pages are left alone.
func (a *App) openMarkdownPage(tab core.TabID, path string) tea.Cmd {
	v := a.drawer.views[tab]
	if v == nil || v.root == "" {
		return nil
	}

	if !filepath.IsAbs(path) {
		path = filepath.Join(v.root, path)
	}

	path = filepath.Clean(path)
	v.pageSeq++

	return a.readPageCmd(tab, v, path, v.pageSeq, false, nil)
}

// readPageCmd reads path (absolute) under the view's root and parses it. With
// known set, a file whose bytes hash to it is not parsed.
func (a *App) readPageCmd(tab core.TabID, v *reviewView, path string, seq int, reload bool, known *[sha256.Size]byte) tea.Cmd {
	root, epoch, parent, tok, fold := v.root, v.epoch, a.ctx, a.drawer.hl.HighlightLang, a.foldKey()

	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(parent, mdReadTimeout)
		defer cancel()

		msg := mdPageMsg{tab: tab, gen: epoch, seq: seq, path: path, reload: reload}

		src, err := readLocalMarkdown(ctx, root, path)
		if err != nil {
			msg.err = err

			return msg
		}

		msg.sum = sha256.Sum256(src)
		if known != nil && *known == msg.sum {
			msg.same = true

			return msg
		}

		msg.doc = parseMarkdown(src, filepath.Dir(path), tok).withFoldKey(fold)

		return msg
	}
}

// onMDPage shows a page once it was read. An answer for a source that was
// replaced, or for an ask a newer one or a dropped stack superseded, is dropped.
func (a *App) onMDPage(msg mdPageMsg) {
	v := a.drawer.views[msg.tab]
	if v == nil || msg.gen != v.epoch {
		return
	}

	st := a.store.State()
	active := st.Tab().ID == msg.tab

	if msg.reload {
		a.onPageReload(v, msg, active)

		return
	}

	if t, ok := tabByID(st, msg.tab); msg.seq != v.pageSeq || !ok || t.Review == nil {
		return
	}

	// The operator moved on during the read: a page must not take the keyboard back.
	if !active || a.modalOpen() || !a.drawer.focused {
		return
	}

	if msg.err != nil {
		if active && !errors.Is(msg.err, context.Canceled) {
			a.notice = markdownNotice(v.root, msg.path, a.getenv("HOME"), msg.err)
		}

		return
	}

	msg.doc.withPictures(docPictures{reg: a.pictures, root: v.root})

	p := &mdPage{path: msg.path, rel: relSlash(v.root, msg.path), view: mdView{doc: msg.doc}, sum: msg.sum}
	v.pages = append(v.pages, p)

	v.search = nil

	if active {
		a.closeWorktrees()
		a.closeLinks()
		a.drawerTake()
		a.sizePage(v, p)
	}
}

// onPageReload swaps the shown page's doc for a new read of its file and keeps
// the cursor on its source line. A file that cannot be read leaves the old doc.
func (a *App) onPageReload(v *reviewView, msg mdPageMsg, active bool) {
	p := v.page()
	if p == nil || p.path != msg.path || msg.seq != v.reloadSeq {
		return
	}

	if msg.err != nil {
		if active && v.reloadNotice && !errors.Is(msg.err, context.Canceled) {
			a.notice = markdownNotice(v.root, msg.path, a.getenv("HOME"), msg.err)
		}

		return
	}

	p.stale = false

	if msg.same {
		return
	}

	msg.doc.withPictures(docPictures{reg: a.pictures, root: v.root})

	line, old := p.anchorLine(), p.view
	p.view = mdView{doc: msg.doc, w: old.w, h: old.h, icons: old.icons, top: old.top, sel: old.sel}
	p.view.goSource(line)
	p.sum = msg.sum
	p.rev++
}

// reloadPage reads the shown page's file again; notice says a failure shows.
func (a *App) reloadPage(tab core.TabID, v *reviewView, notice bool) tea.Cmd {
	p := v.page()
	if p == nil {
		return nil
	}

	v.reloadSeq++
	v.reloadNotice = notice

	a.pictures.retry(docPictures{reg: a.pictures, root: v.root}, p.view.layout())

	return a.readPageCmd(tab, v, p.path, v.reloadSeq, true, nil)
}

// checkPage reads the shown page's file and parses it again only when its
// bytes changed.
func (a *App) checkPage(tab core.TabID, v *reviewView) tea.Cmd {
	p := v.page()
	if p == nil {
		return nil
	}

	v.reloadSeq++
	v.reloadNotice = false
	sum := p.sum

	return a.readPageCmd(tab, v, p.path, v.reloadSeq, true, &sum)
}

// pageBatch checks the pages of a watcher batch's worktree: the shown one now,
// the ones under it when they are shown again. The watcher never names an
// ignored file, so every batch counts, not only one that names the page.
func (a *App) pageBatch(tab core.TabID, v *reviewView) tea.Cmd {
	if len(v.pages) == 0 {
		return nil
	}

	for _, p := range v.pages[:len(v.pages)-1] {
		p.stale = true
	}

	return a.checkPage(tab, v)
}

// dropPages ends the page stack and any read for it.
func (a *App) dropPages(v *reviewView) {
	if a.drawer.linksOf.view == v {
		a.closeLinks()
	}

	v.pages = nil
	v.pageSeq++
	v.reloadSeq++
}

// popPage shows the page under the shown one, or the diff after the last.
func (a *App) popPage(tab core.TabID, v *reviewView) tea.Cmd {
	v.pages = v.pages[:len(v.pages)-1]
	v.pageSeq++

	if p := v.page(); p != nil && p.stale {
		return a.checkPage(tab, v)
	}

	return nil
}

// sizePage sets the cells the page draws in from the drawer's layout.
func (a *App) sizePage(v *reviewView, p *mdPage) {
	st := a.store.State()

	box, ok := a.drawerBox(st)
	if !ok {
		return
	}

	lay := a.drawerLayoutOf(box, v, false)
	p.resize(lay.diffW, lay.diffH, a.icons)
}

// activePage is the active tab's drawer view and the page it shows, sized; nil
// for no page.
func (a *App) activePage() (*reviewView, *mdPage) {
	tab := a.store.State().Tab()
	if tab.Review == nil {
		return nil, nil
	}

	v := a.drawer.views[tab.ID]
	if v == nil || v.page() == nil {
		return nil, nil
	}

	p := v.page()
	a.sizePage(v, p)

	return v, p
}

// onPage runs f on the page the drawer shows and reports whether there is one.
func (a *App) onPage(f func(v *reviewView, p *mdPage)) bool {
	v, p := a.activePage()
	if p == nil {
		return false
	}

	f(v, p)
	p.rev++

	return true
}

// pageShown reports whether the active tab's drawer shows a page.
func (a *App) pageShown() bool {
	tab := a.store.State().Tab()
	if tab.Review == nil {
		return false
	}

	v := a.drawer.views[tab.ID]

	return v != nil && v.page() != nil
}

// pageCommand reports whether a drawer command works while a page shows.
func pageCommand(id string) bool {
	switch id {
	case "review.line.next", "review.line.prev", "review.hunk.next", "review.hunk.prev", "review.page.down", "review.page.up",
		"review.top", "review.bottom", "review.fold", "review.back", "review.edit", "review.refresh", "review.search",
		"review.match.next", "review.match.prev",
		"review.mode.branch", "review.mode.turn", "review.mode.pr", "review.copy", "review.zoom":
		return true
	}

	return false
}

// openEditor opens path at line in the editor, in a new shell pane beside the
// drawer's pane, and closes the drawer so the pane shows.
func (a *App) openEditor(tab core.Tab, v *reviewView, path string, line int) tea.Cmd {
	run := &paneRun{program: reviewEditor(a.getenv), args: []string{"+" + strconv.Itoa(line), path}}

	cmd, opened := a.openPaneAt(core.KindShell, core.SplitVertical, tab.Review.Pane, v.root, run)
	if opened {
		a.closeDrawer(false)
	}

	return cmd
}

// foldKey is the key a closed fold names in the drawer's markdown: the first
// one bound to review.fold, "" when it has none.
func (a *App) foldKey() *string {
	key, _ := a.keys.drawer.first("review.fold")

	return &key
}
