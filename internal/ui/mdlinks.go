package ui

import (
	"context"
	"errors"
	"fmt"
	"image"
	"os"
	"path/filepath"
	"strings"

	"github.com/charmbracelet/x/ansi"

	tea "charm.land/bubbletea/v2"

	"github.com/mhersson/aim/internal/core"
	"github.com/mhersson/aim/internal/md"
	"github.com/mhersson/aim/internal/ui/palette"
	"github.com/mhersson/aim/internal/ui/theme"
)

// linkChooser lists the targets of a rendered line when it has several. root is
// the shown worktree's root, which paths are shown relative to and followed
// within.
type linkChooser struct {
	targets []mdTarget
	sel     int
	root    string
}

// linksOwner names the drawer a chooser was opened over.
type linksOwner struct {
	tab  core.TabID
	pane core.PaneID
	view *reviewView
}

// key moves the selection, stopping at the ends. enter picks the selected
// target and ends the chooser, as esc does without one; every other key is
// dropped.
func (c *linkChooser) key(k string) (pick *mdTarget, done bool) {
	switch k {
	case "j", "down":
		c.sel = min(c.sel+1, len(c.targets)-1)
	case "k", "up":
		c.sel = max(c.sel-1, 0)
	case "enter":
		if c.sel >= 0 && c.sel < len(c.targets) {
			t := c.targets[c.sel]

			return &t, true
		}

		return nil, true
	case "esc":
		return nil, true
	}

	return nil, false
}

// lines draws the box w cells wide and at most maxH tall; nil when not one row
// fits.
func (c *linkChooser) lines(ui theme.UI, w, maxH int, icons bool, home string) []string {
	vis := min(len(c.targets), maxH-wtChrome)
	if w < wtMinBoxW || vis < 1 {
		return nil
	}

	pt := palette.NewBox(theme.Theme{UI: ui}, w)
	top := min(max(c.sel-vis+1, 0), len(c.targets)-vis)

	title := "Links"
	if icons {
		title = iconLinks + " " + title
	}

	out := []string{pt.Top(title)}

	for i := top; i < top+vis; i++ {
		out = append(out, pt.Row(c.row(ui, c.targets[i], i == c.sel, pt.Inner(), home), pt.On(ui.Fg)))
	}

	hints := pt.Hints([][2]string{{"j/k", "move"}, {"enter", "open"}, {"esc", "close"}})

	return append(out, pt.Row("", pt.On(ui.Fg)), pt.Row(hints, pt.On(ui.Fg)), pt.Bottom(""))
}

// row draws one target, inner cells wide: the mark, its text and where it leads.
func (c *linkChooser) row(ui theme.UI, t mdTarget, selected bool, inner int, home string) string {
	bg := ui.Panel
	mark := railRun{text: " ", fg: ui.Fg}

	if selected {
		bg = ui.Selection
		mark = railRun{text: markGlyph, fg: ui.Accent}
	}

	text, _ := cleanText(t.Text)
	dest := printable(t.URL)

	if t.Path != "" {
		dest = shortPath(t.Path, c.root, home)
	}

	// The mark, a blank, the text and a blank margin.
	avail := max(inner-3, 0)

	if text != "" && dest != "" {
		room := avail - 2
		textW := min(ansi.StringWidth(text), max(room/2, room-ansi.StringWidth(dest)))
		text = ansi.Truncate(text, max(textW, 0), "…")
		dest = ansi.Truncate(dest, max(room-ansi.StringWidth(text), 0), "…")
	}

	runs := []railRun{mark, {text: " ", fg: ui.Fg}}

	if text != "" {
		runs = append(runs, railRun{text: text, fg: ui.Fg})
	}

	if text != "" && dest != "" {
		runs = append(runs, railRun{text: "  ", fg: ui.Fg})
	}

	if dest != "" {
		runs = append(runs, railRun{text: dest, fg: ui.Subtle})
	}

	return railPainter{bg: bg}.line(cutRuns(runs, inner-1), inner)
}

// ---- following ----

// followTarget follows a target of a rendered view: a URL goes to the browser,
// a markdown file of root to openMD, any other file of root to the editor at
// line 1. A path that leaves root is refused.
func (a *App) followTarget(root string, t mdTarget, openMD func(path string) tea.Cmd) tea.Cmd {
	if t.Path == "" {
		if t.URL == "" {
			return nil
		}

		return a.openURL(t.URL)
	}

	path := filepath.Clean(t.Path)

	if rel, err := filepath.Rel(root, path); err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		a.notice = markdownNotice(root, path, a.getenv("HOME"), errOutsideWorktree)

		return nil
	}

	if md.IsMarkdown(path) {
		return openMD(path)
	}

	tab := a.store.State().Tab()
	msg := linkEditMsg{tab: tab.ID, root: root, path: path}

	if tab.Review != nil {
		msg.pane = tab.Review.Pane
		if v := a.drawer.views[tab.ID]; v != nil {
			msg.view, msg.epoch = v, v.epoch
		}
	}

	parent := a.ctx

	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(parent, mdReadTimeout)
		defer cancel()

		msg.err = insideResolved(ctx, root, path)

		return msg
	}
}

// linkEditMsg answers the resolution of a followed file that is not markdown.
// pane, view and epoch name the drawer it was followed from, if any.
type linkEditMsg struct {
	tab        core.TabID
	pane       core.PaneID
	view       *reviewView
	epoch      int
	root, path string
	err        error
	plan       *planView // the plan overlay it was followed from, if any
}

// insideResolved follows the symlinks of root and path and reports errOutsideWorktree
// when the path leaves the root and errIsFolder when it is a folder.
func insideResolved(ctx context.Context, root, path string) error {
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("resolve %s: %w", path, err)
	}

	r, err := filepath.EvalSymlinks(root)
	if err != nil {
		return fmt.Errorf("resolve the worktree: %w", err)
	}

	p, err := filepath.EvalSymlinks(path)
	if err != nil {
		return err //nolint:wrapcheck // the *PathError names the reason markdownNotice shows
	}

	if rel, err := filepath.Rel(r, p); err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return errOutsideWorktree
	}

	info, err := os.Stat(p)
	if err != nil {
		return err //nolint:wrapcheck // the *PathError names the reason markdownNotice shows
	}

	if info.IsDir() {
		return errIsFolder
	}

	return nil
}

// onLinkEdit opens the editor on a followed file that resolved inside the root,
// or says why not. An answer for a drawer that closed, retargeted, lost the
// keyboard or has a new source since, or one that a modal opened over, is dropped.
func (a *App) onLinkEdit(msg linkEditMsg) tea.Cmd {
	if msg.plan != nil {
		return a.onPlanLinkEdit(msg)
	}

	tab := a.store.State().Tab()
	if tab.ID != msg.tab || (tab.Review == nil) != (msg.pane == "") || (tab.Review != nil && tab.Review.Pane != msg.pane) {
		return nil
	}

	if msg.view != nil && (a.drawer.views[tab.ID] != msg.view || msg.view.epoch != msg.epoch || !a.drawer.focused) {
		return nil
	}

	if a.modalOpen() || errors.Is(msg.err, context.Canceled) {
		return nil
	}

	if msg.err != nil {
		a.notice = markdownNotice(msg.root, msg.path, a.getenv("HOME"), msg.err)

		return nil
	}

	anchor := tab.Focus
	if tab.Review != nil {
		anchor = tab.Review.Pane
	}

	run := &paneRun{program: reviewEditor(a.getenv), args: []string{"+1", msg.path}}

	cmd, opened := a.openPaneAt(core.SplitVertical, anchor, msg.root, run)
	if opened && tab.Review != nil {
		a.closeDrawer(false)
	}

	return cmd
}

// followLine follows what a rendered line leads to: one target is followed,
// several open the chooser, none does nothing.
func (a *App) followLine(tab core.TabID, v *reviewView, targets []mdTarget) tea.Cmd {
	switch len(targets) {
	case 0:
		return nil
	case 1:
		return a.followTarget(v.root, targets[0], a.openLinkedPage(tab))
	}

	rv := a.store.State().Tab().Review
	if rv == nil {
		return nil
	}

	pane := rv.Pane
	a.drawer.links = &linkChooser{targets: targets, root: v.root}
	a.drawer.linksOf = linksOwner{tab: tab, pane: pane, view: v}

	return nil
}

// openLinkedPage opens a followed markdown file as a page over the drawer's
// stack.
func (a *App) openLinkedPage(tab core.TabID) func(string) tea.Cmd {
	return func(path string) tea.Cmd { return a.openMarkdownPage(tab, path) }
}

// ---- the chooser's place in the drawer ----

// linksFor is the open chooser when it belongs to the active tab's drawer.
func (a *App) linksFor(tab core.Tab) *linkChooser {
	o := a.drawer.linksOf
	if a.drawer.links == nil || tab.Review == nil || o.tab != tab.ID || o.pane != tab.Review.Pane || a.drawer.views[tab.ID] != o.view {
		return nil
	}

	return a.drawer.links
}

// closeLinks closes the chooser.
func (a *App) closeLinks() {
	a.drawer.links, a.drawer.linksOf = nil, linksOwner{}
}

// linksSync closes a chooser whose drawer left it behind.
func (a *App) linksSync() {
	if a.drawer.links != nil && a.linksFor(a.store.State().Tab()) == nil {
		a.closeLinks()
	}
}

// handleLinksKey runs a key while the chooser has the keyboard.
func (a *App) handleLinksKey(c *linkChooser, msg tea.KeyPressMsg) tea.Cmd {
	pick, done := c.key(bindingKey(msg))
	if !done {
		return nil
	}

	a.closeLinks()

	if pick == nil {
		return nil
	}

	return a.followTarget(c.root, *pick, a.openLinkedPage(a.store.State().Tab().ID))
}

// ---- drawing ----

// linksKey is what drawing the chooser reads, for the drawer's draw key.
type linksKey struct {
	c   *linkChooser
	sel int
}

func (d *drawCtx) linksKey() linksKey {
	if d.links == nil {
		return linksKey{}
	}

	return linksKey{c: d.links, sel: d.links.sel}
}

// overlayLinks draws the chooser over the drawer's lines, inside its borders,
// where the worktree list draws.
func (d *drawCtx) overlayLinks(lines []string) {
	c := d.links
	if c == nil {
		return
	}

	box := d.lay.box

	box1 := c.lines(d.ui, box.W-2*wtInsetX, box.H-1-wtTopY, d.icons, d.home)
	if box1 == nil || len(lines) != box.H {
		return
	}

	gap := railPainter{bg: d.ui.Bg}.line(nil, 1)
	side := paint(d.edge(), d.ui.Bg, false).Render("│")

	for i, line := range box1 {
		y := wtTopY + i
		lines[y] = cutRow(lines[y], wtInsetX) + line + gap + side
	}

	d.covers = append(d.covers, image.Rect(wtInsetX, wtTopY, box.W, wtTopY+len(box1)))
}
