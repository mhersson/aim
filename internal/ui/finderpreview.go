package ui

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/mhersson/aim/internal/md"
	"github.com/mhersson/aim/internal/ui/palette"
)

const finderUnreadableFile = "can't read this file: "

// finderPreview is what the preview column shows for the row asked: a rendered
// file, a folder's contents or one line of text. It is built off the update
// loop and read-only to it except for the scroll.
type finderPreview struct {
	path string // the row's path, which the ask was for
	head string // the path above the preview; a README's own for a folder

	file bool // a file was read: size and mod are set
	size int64
	mod  time.Time

	doc  *mdDoc
	view mdView // the doc's scroll; the cursor is never drawn

	list []md.FolderEntry // a folder without a README: its visible entries
	top  int              // the list's scroll

	text  string
	isErr bool
}

// finderPreviewMsg answers the ask for a preview.
type finderPreviewMsg struct {
	view *finderView
	seq  int
	pv   *finderPreview
}

// finderPreviewOpts is what a preview is made from besides the path.
type finderPreviewOpts struct {
	home  string
	width int // the doc's text width, as the view draws it
	icons bool
	tok   codeTokeniser
}

// finderReadme is the folder's README.md, else README.markdown, whatever its
// case.
func finderReadme(es []md.FolderEntry) (string, bool) {
	for _, want := range []string{"readme.md", "readme.markdown"} {
		for _, e := range es {
			if !e.Dir && strings.EqualFold(e.Name, want) {
				return e.Name, true
			}
		}
	}

	return "", false
}

// readFinderPreview makes the preview of path, a folder when dir. It runs off
// the update loop and stops early when ctx ends.
func readFinderPreview(ctx context.Context, path string, dir bool, o finderPreviewOpts) *finderPreview {
	p := &finderPreview{path: path, head: finderTitle(path, o.home)}

	if !dir {
		p.loadFile(ctx, path, o)

		return p
	}

	es, err := md.ReadFolder(path)
	if err != nil {
		p.text, p.isErr = finderUnreadable+pathErrReason(err), true

		return p
	}

	if name, ok := finderReadme(es); ok {
		p.loadFile(ctx, filepath.Join(path, name), o)

		return p
	}

	for _, e := range es {
		if !strings.HasPrefix(e.Name, ".") {
			p.list = append(p.list, e)
		}
	}

	if len(p.list) == 0 {
		p.text = finderEmpty
	}

	return p
}

// loadFile reads and parses the markdown file at path into p, and lays it out at
// the width the loop will draw it, so the first draw hits the cache.
func (p *finderPreview) loadFile(ctx context.Context, path string, o finderPreviewOpts) {
	p.head = finderTitle(path, o.home)

	fail := func(err error) {
		p.text, p.isErr = finderUnreadableFile+finderFileReason(err), true
	}

	resolved, err := filepath.EvalSymlinks(path)
	if err != nil {
		fail(err)

		return
	}

	src, err := readLocalMarkdown(ctx, filepath.Dir(resolved), resolved)
	if err != nil {
		fail(err)

		return
	}

	info, err := os.Stat(resolved)
	if err != nil {
		fail(err)

		return
	}

	if err := ctx.Err(); err != nil {
		fail(err)

		return
	}

	p.file, p.size, p.mod = true, info.Size(), info.ModTime()
	p.doc = parseMarkdown(src, filepath.Dir(resolved), o.tok)
	p.doc.layout(o.width, o.icons)
	p.view = mdView{doc: p.doc}
}

// finderFileReason is why a file did not read, as the reading box words it.
func finderFileReason(err error) string {
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return "no such file"
	case errors.Is(err, errIsFolder):
		return errIsFolder.Error()
	case errors.Is(err, errOutsideWorktree):
		return errOutsideWorktree.Error()
	}

	return pathErrReason(err)
}

// finderSize is a byte count as the row above the preview writes it.
func finderSize(n int64) string {
	const kb, mb = 1 << 10, 1 << 20

	if n < kb {
		return strconv.FormatInt(n, 10) + " B"
	}

	// Tenths, rounded, so 1048575 B reads 1.0 MB and not 1024.0 KB.
	tenths, unit := (n*10+kb/2)/kb, " KB"
	if tenths >= 10*kb {
		tenths, unit = (n*10+mb/2)/mb, " MB"
	}

	return strconv.FormatInt(tenths/10, 10) + "." + strconv.FormatInt(tenths%10, 10) + unit
}

// finderPreviewHead is the row above the preview in w cells: the path, cut on
// the left, and for a file its size and age.
func finderPreviewHead(p *finderPreview, now time.Time, w int) string {
	tail := ""

	if p.file {
		tail = " · " + finderSize(p.size)
		if age := railAge(p.mod, now); age != "" {
			tail += " · " + age
		}
	}

	return planCutStart(p.head, max(w-ansi.StringWidth(tail), 0)) + tail
}

// finderListWidth is the list's cells of the box's inner width inner, with the
// preview column on.
func finderListWidth(inner int) int {
	return max(finderListMin, (inner*finderListShare+50)/100)
}

// finderPreviewGeom is the preview column's width and the body's rows, and
// whether there is a column: the terminal is wide enough and the box has a list
// row.
func (a *App) finderPreviewGeom() (colW, body int, ok bool) {
	if a.width < finderPreviewMin {
		return 0, 0, false
	}

	_, _, w, h, ok := planBox(a.width, a.height)
	if !ok || h < finderMinH {
		return 0, 0, false
	}

	inner := max(w-2, 0)

	return inner - finderListWidth(inner) - 1, h - 6, true
}

// finderPreviewRows is the rows left for the content under the path and a blank
// row.
func finderPreviewRows(body int, head bool) int {
	if head && body > 4 {
		return body - 2
	}

	return body
}

// finderPreviewWidth is the doc's text width in a column colW wide. The view
// draws one cell wider and the lead cell is cut, so the text starts one cell
// from the bar as the list's does.
func finderPreviewWidth(colW int) int { return (&mdView{w: colW + 1}).textWidth() }

// drop ends the ask in flight and forgets what was asked, so the next sync asks
// again. The preview on show stays until another lands.
func (f *finderView) drop() {
	if f.pvCancel != nil {
		f.pvCancel()
		f.pvCancel = nil
	}

	f.pvSeq++
	f.pvAsked = ""
}

// finderPreviewSync asks for the preview of the row under the cursor when its
// path changed. It runs after every update, so no place that moves the cursor
// has to remember to. Nothing is asked below finderPreviewMin columns, while the
// reading box is over the finder or before the folder was listed.
func (a *App) finderPreviewSync() tea.Cmd {
	f := a.finder
	if f == nil {
		return nil
	}

	colW, _, ok := a.finderPreviewGeom()
	if !ok {
		if f.pvAsked != "" {
			f.drop()
		}

		return nil
	}

	if a.modalOverFinder() || !f.loaded {
		return nil
	}

	r, has := f.selected()

	target := ""
	if has {
		target = f.target(r, a.getenv("HOME"))
	}

	if target == "" {
		f.drop()
		f.preview = nil

		return nil
	}

	if target == f.pvAsked {
		return nil
	}

	f.drop()

	ctx, cancel := context.WithCancel(a.ctx)
	f.pvCancel, f.pvAsked = cancel, target
	seq, dir := f.pvSeq, r.dir || r.kind != finderEntry
	o := finderPreviewOpts{
		home: a.getenv("HOME"), width: finderPreviewWidth(colW), icons: a.icons, tok: a.drawer.hl.HighlightLang,
	}

	return func() tea.Msg {
		tctx, tcancel := context.WithTimeout(ctx, mdReadTimeout)
		defer tcancel()

		return finderPreviewMsg{view: f, seq: seq, pv: readFinderPreview(tctx, target, dir, o)}
	}
}

// onFinderPreview puts a preview on show. An answer for a closed finder or for
// an ask a newer one, a folder change or a resize below the column replaced is
// dropped.
func (a *App) onFinderPreview(msg finderPreviewMsg) {
	f := a.finder
	if f == nil || msg.view != f || msg.seq != f.pvSeq {
		return
	}

	f.preview = msg.pv
}

// finderPreviewScroll moves the preview half its rows, down for dir > 0.
func (a *App) finderPreviewScroll(f *finderView, dir int) {
	colW, body, ok := a.finderPreviewGeom()
	p := f.preview

	if !ok || p == nil {
		return
	}

	rows := finderPreviewRows(body, p.head != "")
	delta := dir * max(rows/2, 1)

	if p.doc != nil {
		p.view.size(colW+1, rows, a.icons)
		p.view.scroll(delta)

		return
	}

	p.top = min(max(p.top+delta, 0), max(len(p.list)-rows, 0))
}

// finderPreviewLines is the preview column: h lines of colW cells.
func (a *App) finderPreviewLines(lk look, box palette.Box, f *finderView, colW, h int) []string {
	ui := box.UI()
	blank := box.On(ui.Fg).Render(strings.Repeat(" ", colW))
	lines := make([]string, 0, h)

	p := f.preview
	if p == nil {
		for range h {
			lines = append(lines, blank)
		}

		return lines
	}

	// A line is a cell of margin, the styled text and padding to the column.
	row := func(styled string, width int) string {
		return box.On(ui.Fg).Render(" ") + styled + box.On(ui.Fg).Render(strings.Repeat(" ", max(colW-1-width, 0)))
	}

	rows := finderPreviewRows(h, p.head != "")
	if rows < h {
		head := ansi.Truncate(finderPreviewHead(p, a.clock.Now(), colW-2), max(colW-2, 0), "…")
		lines = append(lines, row(box.On(ui.Muted).Render(head), ansi.StringWidth(head)), blank)
	}

	switch {
	case p.doc != nil:
		p.view.size(colW+1, rows, a.icons)

		for _, l := range p.view.draw(newMDPainter(lk.theme, ui.Panel), false) {
			lines = append(lines, ansi.TruncateLeft(l, 1, ""))
		}
	case p.text != "":
		fg := ui.Subtle
		if p.isErr {
			fg = ui.Error
		}

		text := ansi.Truncate(p.text, max(colW-2, 0), "…")
		lines = append(lines, row(box.On(fg).Render(text), ansi.StringWidth(text)))
	default:
		p.top = min(max(p.top, 0), max(len(p.list)-rows, 0))

		for _, e := range p.list[p.top:min(len(p.list), p.top+rows)] {
			lines = append(lines, a.finderPreviewEntry(box, e, colW, row))
		}
	}

	for len(lines) < h {
		lines = append(lines, blank)
	}

	return lines[:h]
}

// finderPreviewEntry is one entry of a folder's contents: its icon, as the list
// draws them, and its name.
func (a *App) finderPreviewEntry(box palette.Box, e md.FolderEntry, colW int, row func(string, int) string) string {
	ui := box.UI()
	name, lead, used := printable(e.Name), "", 0

	if e.Dir {
		name += "/"
	}

	if a.icons {
		glyph := iconMarkdown
		if e.Dir {
			glyph = iconFolder
		}

		lead, used = box.On(ui.Muted).Render(glyph)+box.On(ui.Fg).Render(" "), 2
	}

	name = ansi.Truncate(name, max(colW-2-used, 0), "…")

	return row(lead+box.On(ui.Fg).Render(name), used+ansi.StringWidth(name))
}
