package ui

import (
	"errors"
	"fmt"
	"strconv"

	tea "charm.land/bubbletea/v2"

	"github.com/mhersson/aim/internal/core"
	"github.com/mhersson/aim/internal/ui/theme"
)

// command is a named user action. Every action is a command so the keymap and
// the command palette reach it the same way.
type command struct {
	id, title, icon string
	short           string // title in a sequence popup, where the group names the rest; "" means title
	scope           string // "", or the view whose keys run it: "rail", "pr_list" or "checks"
	run             func(a *App) tea.Cmd
}

// registry holds commands by id and in registration order.
type registry struct {
	byID  map[string]command
	order []command
}

// newRegistry fails on an empty or duplicate id, or a command without run.
func newRegistry(cmds []command) (*registry, error) {
	r := &registry{byID: make(map[string]command, len(cmds)), order: make([]command, 0, len(cmds))}
	for _, c := range cmds {
		if c.id == "" {
			return nil, errors.New("ui: command with empty id")
		}

		if c.run == nil {
			return nil, fmt.Errorf("ui: command %q has no run function", c.id)
		}

		if _, dup := r.byID[c.id]; dup {
			return nil, fmt.Errorf("ui: duplicate command id %q", c.id)
		}

		r.byID[c.id] = c
		r.order = append(r.order, c)
	}

	return r, nil
}

func (r *registry) get(id string) (command, bool) {
	c, ok := r.byID[id]

	return c, ok
}

// all returns the commands in registration order.
func (r *registry) all() []command {
	return append([]command(nil), r.order...)
}

func defaultCommands() []command {
	// In the order of the h j k l keys.
	dirs := []struct {
		name string
		dir  core.Dir
	}{{"left", core.Left}, {"down", core.Down}, {"up", core.Up}, {"right", core.Right}}

	cmds := []command{{
		id:    "palette.open",
		icon:  iconPalette,
		title: "Open command palette",
		run: func(a *App) tea.Cmd {
			a.openCommandPalette()

			return nil
		},
	}, {
		id:    "theme.pick",
		icon:  iconTheme,
		title: "Pick theme",
		run: func(a *App) tea.Cmd {
			a.openThemePicker()

			return nil
		},
	}, {
		id:    "agent.new",
		icon:  iconAgent,
		title: "New agent pane",
		run:   (*App).newAgent,
	}, {
		id:    "rail.toggle",
		icon:  iconAgent,
		title: "Agents rail",
		run:   (*App).railToggle,
	}, {
		id:    "usage.toggle",
		icon:  iconUsage,
		title: "Claude usage",
		run:   (*App).usageToggle,
	}, {
		id:    "pane.split.down",
		icon:  iconSplitDown,
		title: "Split: shell below",
		run:   func(a *App) tea.Cmd { return a.openPane(core.KindShell, core.SplitHorizontal) },
	}, {
		id:    "pane.split.right",
		icon:  iconPanes,
		title: "Split: shell right",
		run:   func(a *App) tea.Cmd { return a.openPane(core.KindShell, core.SplitVertical) },
	}}
	for _, d := range dirs {
		cmds = append(cmds, command{
			id:    "pane.focus." + d.name,
			icon:  iconFocus,
			title: "Focus " + d.name,
			run:   func(a *App) tea.Cmd { return a.focusDir(d.dir) },
		})
	}

	for _, d := range dirs {
		cmds = append(cmds, command{
			id:    "pane.resize." + d.name,
			icon:  iconResize,
			title: "Resize " + d.name,
			run:   func(a *App) tea.Cmd { return a.act(core.ResizeDir{Dir: d.dir}) },
		})
	}

	for _, d := range dirs {
		cmds = append(cmds, command{
			id:    "pane.swap." + d.name,
			icon:  iconSwap,
			title: "Swap " + d.name,
			run:   func(a *App) tea.Cmd { return a.act(core.SwapDir{Dir: d.dir, Area: a.bodyArea()}) },
		})
	}

	cmds = append(cmds, command{
		id:    "pane.zoom",
		icon:  iconZoom,
		title: "Zoom pane",
		run:   (*App).zoomPane,
	}, command{
		id:    "pane.close",
		icon:  iconClose,
		title: "Close pane",
		run:   (*App).closeFocused,
	}, command{
		id:    "copy.selection",
		icon:  iconCopy,
		title: "Copy selection",
		run:   func(a *App) tea.Cmd { return a.copySelection() },
	}, command{
		id:    "copy.mode",
		icon:  iconCopy,
		title: "Copy mode",
		run:   (*App).enterCopyMode,
	}, command{
		id:    "tab.new",
		icon:  iconTabNew,
		title: "New tab",
		run:   (*App).newTab,
	}, command{
		id:    "tab.rename",
		icon:  iconRename,
		title: "Rename tab",
		run:   (*App).renameTab,
	}, command{
		id:    "tab.close",
		icon:  iconClose,
		title: "Close tab",
		run:   (*App).closeTab,
	}, command{
		id:    "tab.next",
		icon:  iconTabs,
		title: "Next tab",
		run:   func(a *App) tea.Cmd { return a.act(core.CycleTab{Delta: 1}) },
	}, command{
		id:    "tab.prev",
		icon:  iconTabs,
		title: "Previous tab",
		run:   func(a *App) tea.Cmd { return a.act(core.CycleTab{Delta: -1}) },
	})
	for n := 1; n <= 9; n++ {
		cmds = append(cmds, command{
			id:    "tab.jump." + strconv.Itoa(n),
			icon:  iconTabs,
			title: "Jump to tab " + strconv.Itoa(n),
			run:   func(a *App) tea.Cmd { return a.act(core.SelectTab{Index: n - 1}) },
		})
	}

	cmds = append(cmds, layoutCommands()...)
	cmds = append(cmds, reviewCommands()...)

	cmds = append(cmds, command{
		id:    "plan.open",
		icon:  iconPlan,
		title: "Open the agent's plan",
		run:   (*App).planCommand,
	}, command{
		id:    "md.open",
		icon:  iconMarkdown,
		title: "Open a markdown file",
		run:   (*App).mdOpenCommand,
	}, command{
		id:    "pr.list",
		icon:  iconPR,
		title: "Pull requests",
		run:   (*App).prListCommand,
	}, command{
		id:    "git.push",
		icon:  iconPush,
		title: "Push branch",
		short: "Push",
		run:   (*App).push,
	}, command{
		id:    "pr.open",
		icon:  iconPR,
		title: "Open a pull request",
		run:   (*App).composeCommand,
	}, command{
		id:    "pr.merge",
		icon:  iconMerge,
		title: "Merge pull request",
		run:   (*App).mergeCommand,
	}, command{
		id:    "worktree.cleanup",
		icon:  iconCleanup,
		title: "Clean up a merged worktree",
		run:   (*App).cleanupCommand,
	}, command{
		id:    "app.quit",
		icon:  iconQuit,
		title: "Quit AIM",
		run:   (*App).quit,
	})
	for _, th := range theme.All() {
		cmds = append(cmds, command{
			id:    "theme.set." + th.Name,
			icon:  iconTheme,
			title: "Theme: " + th.Title,
			run: func(a *App) tea.Cmd {
				// dispatch logs its own failures.
				_ = a.dispatch(core.SetTheme{Name: th.Name})

				return nil
			},
		})
	}

	cmds = append(cmds, railCommands()...)
	cmds = append(cmds, prListCommands()...)

	cmds = append(cmds, checksCommands()...)

	cmds = append(cmds, readerCommands()...)

	cmds = append(cmds, mergeCommands()...)

	return append(cmds, composeCommands()...)
}

// zoomPane is pane.zoom: it zooms the focused pane, or the review drawer while
// the drawer has the keyboard.
func (a *App) zoomPane() tea.Cmd {
	if a.drawerHasKeys() {
		return a.reviewZoom()
	}

	return a.act(core.ToggleZoom{})
}
