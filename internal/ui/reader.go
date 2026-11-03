package ui

import (
	"strconv"

	tea "charm.land/bubbletea/v2"

	"github.com/mhersson/aim/internal/core"
)

// readerKeys are the default keys of the reading overlays: the plan, the
// markdown file and the conversation. [keys.reader] is laid over them.
var readerKeys = []reviewKey{
	{"j", "reader.line.next"},
	{"down", "reader.line.next"},
	{"k", "reader.line.prev"},
	{"up", "reader.line.prev"},
	{"ctrl+d", "reader.page.down"},
	{"ctrl+u", "reader.page.up"},
	{"ctrl+f", "reader.screen.down"},
	{"pgdown", "reader.screen.down"},
	{"ctrl+b", "reader.screen.up"},
	{"pgup", "reader.screen.up"},
	{"g", "reader.top"},
	{"G", "reader.bottom"},
	{"]", "reader.section.next"},
	{"[", "reader.section.prev"},
	{"enter", "reader.open"},
	{"/", "reader.search"},
	{"n", "reader.match.next"},
	{"N", "reader.match.prev"},
	{"y", "reader.copy"},
	{"e", "reader.edit"},
	{"r", "reader.refresh"},
	{"esc", "reader.close"},
}

// readerView is the shown view of the open plan overlay, else the
// conversation's; nil when neither is open.
func (a *App) readerView() *mdView {
	switch {
	case a.plan != nil:
		return a.plan.shown()
	case a.conv != nil:
		return &a.conv.view
	}

	return nil
}

// readerHint reports whether the open box has the hint row the search prompt
// draws in.
func (a *App) readerHint() bool {
	_, _, _, h, ok := planBox(a.width, a.height)
	if !ok {
		return false
	}

	if a.plan != nil {
		_, _, hint := planRows(h)

		return hint
	}

	_, _, hint, _ := convRows(h)

	return hint
}

// readerActs reports whether a reader command does anything in the open
// overlay; one that does not keeps the text selection.
func (a *App) readerActs(id string) bool {
	switch id {
	case "reader.search":
		return a.readerHint()
	case "reader.edit":
		return a.plan != nil
	}

	return true
}

// readerKey runs the command the reader table binds to key; a key without one
// is dropped.
func (a *App) readerKey(key string) tea.Cmd {
	id, ok := a.keys.reader.lookup(key)
	if !ok {
		return nil
	}

	return a.readerRun(id, key)
}

func (a *App) readerRun(id, key string) tea.Cmd {
	if id != "reader.copy" && a.readerActs(id) {
		a.clearTextSel()
	}

	return a.runKey(a.keys.reader, key)
}

// readerClose answers a key in a box too small to draw: only reader.close runs.
func (a *App) readerClose(key string) tea.Cmd {
	if id, ok := a.keys.reader.lookup(key); ok && id == "reader.close" {
		return a.readerRun(id, key)
	}

	return nil
}

// noticeConvNoFile answers e in the conversation, which is not a file.
const noticeConvNoFile = "the conversation has no file to open"

// planEdit opens the shown file in the editor at the cursor's source line in a
// new shell pane and closes the overlay.
func (a *App) planEdit() tea.Cmd {
	pv := a.plan
	line, path, root := pv.shown().sourceLine(), pv.shownPath(), pv.root

	a.closePlan()

	run := &paneRun{program: reviewEditor(a.getenv), args: []string{"+" + strconv.Itoa(line), path}}
	cmd, _ := a.openPaneAt(core.KindShell, core.SplitVertical, a.store.State().Tab().Focus, root, run)

	return cmd
}

// planRefresh reads the shown file again, quietly; a page or file keeps its own sum, so mdCheck skips an unchanged parse.
func (a *App) planRefresh() tea.Cmd {
	pv := a.plan
	if len(pv.pages) > 0 || pv.file != nil {
		pics := a.pictures.reloadAll(docPictures{reg: a.pictures, root: pv.root}, pv.shown().layout())
		cmd := a.mdCheck(pv)
		pv.askSeq = pv.reloadSeq

		return tea.Batch(pics, cmd)
	}

	return a.reloadPlan(pv.plan, true)
}

func readerCommands() []command {
	// cmd runs on the open overlay's view; conv says it is the conversation.
	cmd := func(id, title string, run func(a *App, v *mdView, conv bool) tea.Cmd) command {
		return command{id: id, title: "Reader: " + title, run: func(a *App) tea.Cmd {
			v := a.readerView()
			if v == nil {
				return nil
			}

			return run(a, v, a.plan == nil)
		}}
	}

	// move runs a plain view movement.
	move := func(id, title string, run func(v *mdView)) command {
		return cmd(id, title, func(_ *App, v *mdView, _ bool) tea.Cmd {
			run(v)

			return nil
		})
	}

	// section moves by heading in the plan, by item in the conversation.
	section := func(id, title string, dir int) command {
		return cmd(id, title, func(a *App, v *mdView, conv bool) tea.Cmd {
			if conv {
				a.convItem(dir)
			} else {
				v.heading(dir)
			}

			return nil
		})
	}

	match := func(id, title string, back bool) command {
		return cmd(id, title, func(a *App, _ *mdView, conv bool) tea.Cmd {
			if conv {
				a.convFind(a.conv.last, back)
			} else {
				a.planFind(a.plan.last, back)
			}

			return nil
		})
	}

	return scoped("reader", []command{
		move("reader.line.next", "next line", func(v *mdView) { v.move(1) }),
		move("reader.line.prev", "previous line", func(v *mdView) { v.move(-1) }),
		move("reader.page.down", "half a page down", func(v *mdView) { v.page(1) }),
		move("reader.page.up", "half a page up", func(v *mdView) { v.page(-1) }),
		move("reader.screen.down", "a screen down", func(v *mdView) { v.pageFull(1) }),
		move("reader.screen.up", "a screen up", func(v *mdView) { v.pageFull(-1) }),
		move("reader.top", "first line", func(v *mdView) { v.end(false) }),
		move("reader.bottom", "last line", func(v *mdView) { v.end(true) }),
		section("reader.section.next", "next heading or item", 1),
		section("reader.section.prev", "previous heading or item", -1),
		cmd("reader.open", "open the fold or follow the link", func(a *App, _ *mdView, conv bool) tea.Cmd {
			if conv {
				return a.convEnter()
			}

			return a.planEnter()
		}),
		cmd("reader.search", "search", func(a *App, _ *mdView, conv bool) tea.Cmd {
			// The prompt draws in the hint row, so without one there is nowhere to type.
			if !a.readerHint() {
				return nil
			}

			if conv {
				a.conv.searching, a.conv.query = true, ""
			} else {
				a.plan.searching, a.plan.query = true, ""
			}

			return nil
		}),
		match("reader.match.next", "next match", false),
		match("reader.match.prev", "previous match", true),
		cmd("reader.copy", "copy the selection", func(a *App, _ *mdView, conv bool) tea.Cmd {
			if conv {
				return a.convCopy()
			}

			return a.planCopy()
		}),
		cmd("reader.edit", "open in the editor", func(a *App, _ *mdView, conv bool) tea.Cmd {
			if conv {
				a.notice = noticeConvNoFile

				return nil
			}

			return a.planEdit()
		}),
		cmd("reader.refresh", "read again", func(a *App, _ *mdView, conv bool) tea.Cmd {
			if conv {
				return a.readConversation(a.conv, true)
			}

			return a.planRefresh()
		}),
		cmd("reader.close", "close the page or the overlay", func(a *App, _ *mdView, conv bool) tea.Cmd {
			switch {
			case conv:
				a.closeConv()
			case len(a.plan.pages) > 0:
				return a.planPop()
			default:
				a.closePlan()
			}

			return nil
		}),
	})
}
