package ui

import (
	"slices"

	tea "charm.land/bubbletea/v2"
)

// The default keys of the agents rail, the PR list and the checks overlay;
// [keys.rail], [keys.pr_list] and [keys.checks] are laid over them.
var (
	railKeys = []reviewKey{
		{"j", "rail.next"},
		{"down", "rail.next"},
		{"k", "rail.prev"},
		{"up", "rail.prev"},
		{"/", "rail.search"},
		{"enter", "rail.jump"},
		{"p", "rail.plan"},
		{"esc", "rail.back"},
	}
	prListKeys = []reviewKey{
		{"j", "prlist.next"},
		{"down", "prlist.next"},
		{"k", "prlist.prev"},
		{"up", "prlist.prev"},
		{"tab", "prlist.tab.next"},
		{"shift+tab", "prlist.tab.prev"},
		{"1", "prlist.tab.agents"},
		{"2", "prlist.tab.requested"},
		{"3", "prlist.tab.all"},
		{"enter", "prlist.show"},
		{"i", "prlist.conversation"},
		{"c", "prlist.checks"},
		{"R", "prlist.ready"},
		{"a", "prlist.approve"},
		{"M", "prlist.merge"},
		{"O", "prlist.browser"},
		{"r", "prlist.refresh"},
		{"esc", "prlist.close"},
	}
	imagesKeys = []reviewKey{
		{"left", "images.prev"},
		{"h", "images.prev"},
		{"right", "images.next"},
		{"l", "images.next"},
		{"o", "images.open"},
		{"z", "images.zoom"},
		{"esc", "images.close"},
	}
	checksKeys = []reviewKey{
		{"j", "checks.next"},
		{"down", "checks.next"},
		{"k", "checks.prev"},
		{"up", "checks.prev"},
		{"l", "checks.log"},
		{"O", "checks.browser"},
		{"enter", "checks.fold"},
		{"ctrl+d", "checks.page.down"},
		{"ctrl+u", "checks.page.up"},
		{"g", "checks.top"},
		{"G", "checks.bottom"},
		{"]", "checks.error.next"},
		{"[", "checks.error.prev"},
		{"r", "checks.refresh"},
		{"esc", "checks.close"},
	}
)

// scopeNoun is how an error names the view a scoped command belongs to.
func scopeNoun(scope string) string {
	switch scope {
	case "pr_list":
		return "PR list"
	case "images":
		return "viewer"
	}

	return scope
}

// scoped makes the commands of a view: registered so its [keys] table can name
// them, but never in the palette or under [keys.leader].
func scoped(scope string, cmds []command) []command {
	for i := range cmds {
		cmds[i].scope = scope
	}

	return cmds
}

// runKey runs the command a context's table binds to key; a key without one is dropped.
func (a *App) runKey(t keyTable, key string) tea.Cmd {
	id, ok := t.lookup(key)
	if !ok {
		return nil
	}

	c, ok := a.reg.get(id)
	if !ok {
		a.log.Error("key names a missing command", "key", key, "command", id)

		return nil
	}

	return c.run(a)
}

// railOpen reports whether the rail has the keyboard and is not taking search text.
func (a *App) railOpen() bool { return a.rail.focused && !a.rail.searching }

func railCommands() []command {
	cmd := func(id, title string, run func(*App) tea.Cmd) command {
		return command{id: id, title: "Agents rail: " + title, run: func(a *App) tea.Cmd {
			if !a.railOpen() {
				return nil
			}

			return run(a)
		}}
	}

	return scoped("rail", []command{
		cmd("rail.next", "next card", func(a *App) tea.Cmd {
			a.railMove(1)

			return nil
		}),
		cmd("rail.prev", "previous card", func(a *App) tea.Cmd {
			a.railMove(-1)

			return nil
		}),
		cmd("rail.search", "search", func(a *App) tea.Cmd {
			a.railSetSearch(true, a.rail.query)

			return nil
		}),
		cmd("rail.jump", "jump to the agent", func(a *App) tea.Cmd { return a.railJump(a.rail.sel) }),
		cmd("rail.plan", "open the agent's plan", func(a *App) tea.Cmd {
			if a.rail.sel == "" {
				return nil
			}

			return a.openPlan(a.rail.sel)
		}),
		cmd("rail.back", "end the search or go back to the panes", func(a *App) tea.Cmd {
			if a.rail.query != "" {
				a.railSetSearch(false, "")
			} else {
				a.railRelease()
			}

			return nil
		}),
	})
}

func prListCommands() []command {
	// cmd runs on the open list; row says the command needs the selected row.
	cmd := func(id, title string, row bool, run func(a *App, rows []prListRow, i int) tea.Cmd) command {
		return command{id: id, title: "Pull requests: " + title, run: func(a *App) tea.Cmd {
			if a.prList == nil {
				return nil
			}

			rows := a.prListRowsOf(a.store.State(), a.prList.tab)

			i := a.prList.selected(rows)
			if row && i < 0 {
				return nil
			}

			return run(a, rows, i)
		}}
	}

	return scoped("pr_list", []command{
		cmd("prlist.tab.next", "next tab", false, func(a *App, _ []prListRow, _ int) tea.Cmd {
			a.prListStep(true)

			return nil
		}),
		cmd("prlist.tab.prev", "previous tab", false, func(a *App, _ []prListRow, _ int) tea.Cmd {
			a.prListStep(false)

			return nil
		}),
		cmd("prlist.tab.agents", "agents tab", false, func(a *App, _ []prListRow, _ int) tea.Cmd {
			a.prListShow(prTabAgents)

			return nil
		}),
		cmd("prlist.tab.requested", "review requested tab", false, func(a *App, _ []prListRow, _ int) tea.Cmd {
			a.prListShow(prTabRequested)

			return nil
		}),
		cmd("prlist.tab.all", "all open tab", false, func(a *App, _ []prListRow, _ int) tea.Cmd {
			a.prListShow(prTabAll)

			return nil
		}),
		cmd("prlist.next", "next", false, func(a *App, rows []prListRow, _ int) tea.Cmd {
			a.prList.move(rows, 1)

			return nil
		}),
		cmd("prlist.prev", "previous", false, func(a *App, rows []prListRow, _ int) tea.Cmd {
			a.prList.move(rows, -1)

			return nil
		}),
		cmd("prlist.show", "show the diff", true, func(a *App, rows []prListRow, i int) tea.Cmd {
			return a.prListOpen(rows[i])
		}),
		// The conversation and the checks stack over the list, which stays under them.
		cmd("prlist.conversation", "show the conversation", true, func(a *App, rows []prListRow, i int) tea.Cmd {
			return a.openConversation(rows[i].key.repo, rows[i].key.number, rows[i].pr)
		}),
		cmd("prlist.checks", "show the checks", true, func(a *App, rows []prListRow, i int) tea.Cmd {
			return a.openChecks(rows[i].key.repo, rows[i].key.number)
		}),
		// Marking ready needs no diff, so it works on any row, checked out or not.
		cmd("prlist.ready", "mark ready", true, func(a *App, rows []prListRow, i int) tea.Cmd {
			a.openReady(rows[i].key.repo, rows[i].key.number, rows[i].pr, true)

			return nil
		}),
		cmd("prlist.approve", "approve", true, func(a *App, rows []prListRow, i int) tea.Cmd {
			if a.actions == nil {
				a.notice = noticePRsOff

				return nil
			}

			if rows[i].pr.ViewerAuthored {
				a.notice = noticeApproveOwn

				return nil
			}

			// The row has all the confirm needs, so it opens over the list at once.
			r := rows[i]
			a.openApproveConfirm(r.key.repo, r.key.number, r.pr.Title, r.pr.HeadOID, true)

			return nil
		}),
		cmd("prlist.merge", "merge", true, func(a *App, rows []prListRow, i int) tea.Cmd {
			if a.actions == nil {
				a.notice = noticePRsOff

				return nil
			}

			a.prListMerge(rows[i])

			return nil
		}),
		cmd("prlist.browser", "open in the browser", true, func(a *App, rows []prListRow, i int) tea.Cmd {
			return a.openURL(a.prListRowURL(rows[i]))
		}),
		cmd("prlist.refresh", "refresh", false, func(a *App, _ []prListRow, _ int) tea.Cmd {
			return a.prListReread()
		}),
		cmd("prlist.close", "close", false, func(a *App, _ []prListRow, _ int) tea.Cmd {
			a.prList = nil

			return nil
		}),
	})
}

func checksCommands() []command {
	// cmd runs on the open overlay with its rows and the clamped selection.
	cmd := func(id, title string, run func(a *App, v *checksView, rows []checkRow) tea.Cmd) command {
		return command{id: id, title: "Checks: " + title, run: func(a *App) tea.Cmd {
			v := a.checks
			if v == nil {
				return nil
			}

			rows := checkRows(a.checksOf(a.store.State(), v), a.clock.Now())
			v.sel = min(max(v.sel, 0), max(len(rows)-1, 0))

			return run(a, v, rows)
		}}
	}

	// page runs on the open log page, and does nothing in the list. n is the
	// log rows the page shows.
	page := func(id, title string, run func(p *checkPage, n int)) command {
		return cmd(id, title, func(a *App, v *checksView, _ []checkRow) tea.Cmd {
			if v.page != nil {
				run(v.page, a.logPageRows())
			}

			return nil
		})
	}

	return scoped("checks", []command{
		cmd("checks.next", "next check or line", func(a *App, v *checksView, rows []checkRow) tea.Cmd {
			if p := v.page; p != nil {
				if p.sum {
					p.moveSummary(1)

					return nil
				}

				p.move(p.cursor+1, a.logPageRows())

				return nil
			}

			v.sel = min(v.sel+1, max(len(rows)-1, 0))

			return nil
		}),
		cmd("checks.prev", "previous check or line", func(a *App, v *checksView, _ []checkRow) tea.Cmd {
			if p := v.page; p != nil {
				if p.sum {
					p.moveSummary(-1)

					return nil
				}

				p.move(p.cursor-1, a.logPageRows())

				return nil
			}

			v.sel = max(v.sel-1, 0)

			return nil
		}),
		cmd("checks.log", "show the job's log", func(a *App, v *checksView, rows []checkRow) tea.Cmd {
			if v.page != nil || v.sel >= len(rows) {
				return nil
			}

			return a.checksLog(v, rows[v.sel])
		}),
		cmd("checks.browser", "open in the browser", func(a *App, v *checksView, rows []checkRow) tea.Cmd {
			var c checkRow

			switch {
			case v.page != nil:
				c = v.page.check
			case v.sel < len(rows):
				c = rows[v.sel]
			default:
				return nil
			}

			if c.url == "" {
				a.notice = noticeCheckNoPage

				return nil
			}

			return a.openURL(c.url)
		}),
		cmd("checks.fold", "open or close the group, or open the link", func(a *App, v *checksView, _ []checkRow) tea.Cmd {
			switch p := v.page; {
			case p == nil:
			case p.sum:
				return a.summaryEnter(p)
			default:
				p.fold()
			}

			return nil
		}),
		page("checks.page.down", "half a page down", func(p *checkPage, n int) {
			if p.sum {
				p.pageSummary(1)

				return
			}

			p.move(p.cursor+max(n/2, 1), n)
		}),
		page("checks.page.up", "half a page up", func(p *checkPage, n int) {
			if p.sum {
				p.pageSummary(-1)

				return
			}

			p.move(p.cursor-max(n/2, 1), n)
		}),
		page("checks.top", "first line", func(p *checkPage, n int) {
			if p.sum {
				p.endSummary(false)

				return
			}

			p.move(0, n)
		}),
		page("checks.bottom", "last line", func(p *checkPage, n int) {
			if p.sum {
				p.endSummary(true)

				return
			}

			p.move(len(p.rows())-1, n)
		}),
		page("checks.error.next", "next error", func(p *checkPage, n int) {
			if i, ok := p.errorRow(1); ok {
				p.move(i, n)
			}
		}),
		page("checks.error.prev", "previous error", func(p *checkPage, n int) {
			if i, ok := p.errorRow(-1); ok {
				p.move(i, n)
			}
		}),
		cmd("checks.refresh", "refresh", func(a *App, v *checksView, _ []checkRow) tea.Cmd {
			if v.page != nil {
				if v.page.sum {
					return a.readCheckRun(v)
				}

				return a.readJobLog(v)
			}

			if repo, ok := repoOfKey(v.repo); ok && a.prs.src != nil {
				a.prs.src.Refresh(repo)
			}

			return nil
		}),
		cmd("checks.close", "close", func(a *App, v *checksView, _ []checkRow) tea.Cmd {
			if v.page != nil {
				v.page = nil

				return nil
			}

			a.closeChecks()

			return nil
		}),
	})
}

// hintPairs drops the pairs whose command has no key.
func hintPairs(pairs [][2]string) [][2]string {
	return slices.DeleteFunc(pairs, func(p [2]string) bool { return p[0] == "" })
}
