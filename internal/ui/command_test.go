package ui

import (
	"fmt"
	"slices"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/mhersson/aim/internal/ui/theme"
)

func TestRegistry(t *testing.T) {
	t.Parallel()

	noop := func(*App) tea.Cmd { return nil }

	tests := []struct {
		name    string
		cmds    []command
		wantErr bool
	}{
		{"ok", []command{{id: "a", title: "A", run: noop}, {id: "b", title: "B", run: noop}}, false},
		{"empty", nil, false},
		{"empty id", []command{{id: "", title: "A", run: noop}}, true},
		{"duplicate id", []command{{id: "a", run: noop}, {id: "a", run: noop}}, true},
		{"nil run", []command{{id: "a"}}, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r, err := newRegistry(tt.cmds)
			if (err != nil) != tt.wantErr {
				t.Fatalf("newRegistry() error = %v, wantErr %v", err, tt.wantErr)
			}

			if err != nil {
				return
			}

			got := r.all()
			if len(got) != len(tt.cmds) {
				t.Fatalf("all() has %d commands, want %d", len(got), len(tt.cmds))
			}

			for i, c := range tt.cmds {
				if got[i].id != c.id {
					t.Errorf("all()[%d].id = %q, want %q", i, got[i].id, c.id)
				}

				if byID, ok := r.get(c.id); !ok || byID.title != c.title {
					t.Errorf("get(%q) = %+v, %v", c.id, byID, ok)
				}
			}

			if _, ok := r.get("missing"); ok {
				t.Error("get(missing) found a command")
			}
		})
	}
}

func TestDefaultCommands(t *testing.T) {
	t.Parallel()

	// The scoped commands have their own test.
	cmds := slices.DeleteFunc(defaultCommands(), func(c command) bool { return c.scope != "" })

	type want struct{ id, title, icon string }

	fixed := []want{
		{"palette.open", "Open command palette", iconPalette},
		{"theme.pick", "Pick theme", iconTheme},
		{"agent.new", "New agent pane", iconAgent},
		{"rail.toggle", "Agents rail", iconAgent},
		{"usage.toggle", "Claude usage", iconUsage},
		{"pane.split.down", "Split: shell below", iconSplitDown},
		{"pane.split.right", "Split: shell right", iconPanes},
	}
	for _, d := range []string{"left", "down", "up", "right"} {
		fixed = append(fixed, want{"pane.focus." + d, "Focus " + d, iconFocus})
	}

	for _, d := range []string{"left", "down", "up", "right"} {
		fixed = append(fixed, want{"pane.resize." + d, "Resize " + d, iconResize})
	}

	for _, d := range []string{"left", "down", "up", "right"} {
		fixed = append(fixed, want{"pane.swap." + d, "Swap " + d, iconSwap})
	}

	fixed = append(fixed,
		want{"pane.zoom", "Zoom pane", iconZoom},
		want{"pane.close", "Close pane", iconClose},
		want{"copy.selection", "Copy selection", iconCopy},
		want{"copy.mode", "Copy mode", iconCopy},
		want{"tab.new", "New tab", iconTabNew},
		want{"tab.rename", "Rename tab", iconRename},
		want{"tab.close", "Close tab", iconClose},
		want{"tab.next", "Next tab", iconTabs},
		want{"tab.prev", "Previous tab", iconTabs},
	)
	for n := 1; n <= 9; n++ {
		fixed = append(fixed, want{fmt.Sprintf("tab.jump.%d", n), fmt.Sprintf("Jump to tab %d", n), iconTabs})
	}

	for n := 1; n <= 9; n++ {
		fixed = append(fixed, want{fmt.Sprintf("pane.move.tab.%d", n), fmt.Sprintf("Move pane to tab %d", n), iconMove})
	}

	for n := 1; n <= 9; n++ {
		fixed = append(fixed, want{fmt.Sprintf("pane.send.tab.%d", n), fmt.Sprintf("Send pane to tab %d", n), iconMove})
	}

	fixed = append(fixed,
		want{"pane.break", "Break pane into new tab", iconBreak},
		want{"layout.cycle", "Cycle layout", iconLayoutBSP},
		want{"layout.set.bsp", "Layout: BSP", iconLayoutBSP},
		want{"layout.set.master-stack", "Layout: master-stack", iconLayoutMaster},
		want{"layout.set.scrolling", "Layout: scrolling", iconLayoutScrolling},
		want{"pane.preselect.left", "Preselect left", iconArrowLeft},
		want{"pane.preselect.down", "Preselect down", iconArrowDown},
		want{"pane.preselect.up", "Preselect up", iconArrowUp},
		want{"pane.preselect.right", "Preselect right", iconArrowRight},
		want{"pane.preselect.clear", "Clear preselection", iconClose},
		want{"layout.rotate", "Rotate split", iconRotate},
		want{"layout.equalize", "Equalize", iconEqualize},
		want{"layout.promote", "Promote to master", iconPromote},
		want{"review.toggle", "Review drawer", iconReview},
		want{"review.line.next", "Review: next line", iconReview},
		want{"review.line.prev", "Review: previous line", iconReview},
		want{"review.hunk.next", "Review: next hunk", iconReview},
		want{"review.hunk.prev", "Review: previous hunk", iconReview},
		want{"review.file.next", "Review: next file", iconReview},
		want{"review.file.prev", "Review: previous file", iconReview},
		want{"review.page.down", "Review: half a page down", iconReview},
		want{"review.page.up", "Review: half a page up", iconReview},
		want{"review.top", "Review: top", iconReview},
		want{"review.bottom", "Review: bottom", iconReview},
		want{"review.tree", "Review: file tree", iconReview},
		want{"review.full", "Review: full file", iconReview},
		want{"review.markdown", "Review: rendered or source", iconReview},
		want{"review.split", "Review: unified or side by side", iconReview},
		want{"review.fold", "Review: fold or show removed lines", iconReview},
		want{"review.mark", "Review: mark the hunk reviewed", iconReview},
		want{"review.show.reviewed", "Review: show reviewed hunks too", iconReview},
		want{"review.mode.branch", "Review: branch mode", iconReview},
		want{"review.mode.turn", "Review: turn mode", iconReview},
		want{"review.mode.pr", "Review: pull request", iconReview},
		want{"review.conversation", "Review: conversation", iconReview},
		want{"review.checks", "Review: checks", iconReview},
		want{"review.approve", "Review: approve", iconReview},
		want{"review.merge", "Review: merge", iconReview},
		want{"review.browser", "Review: open in browser", iconReview},
		want{"review.turn.prev", "Review: previous turn", iconReview},
		want{"review.turn.next", "Review: next turn", iconReview},
		want{"review.search", "Review: search", iconReview},
		want{"review.copy", "Review: copy the selection", iconReview},
		want{"review.zoom", "Review: zoom", iconReview},
		want{"review.worktrees", "Review: worktrees", iconReview},
		want{"review.edit", "Review: open in the editor", iconReview},
		want{"review.refresh", "Review: refresh", iconReview},
		want{"review.back", "Review: back, else close the drawer", iconReview},
		want{"plan.open", "Open the agent's plan", iconPlan},
		want{"md.open", "Open a markdown file", iconMarkdown},
		want{"pr.list", "Pull requests", iconPR},
		want{"git.push", "Push branch", iconPush},
		want{"pr.open", "Open a pull request", iconPR},
		want{"pr.merge", "Merge pull request", iconMerge},
		want{"worktree.cleanup", "Clean up a merged worktree", iconCleanup},
		want{"app.quit", "Quit AIM", iconQuit},
	)
	if len(cmds) != len(fixed)+len(theme.All()) {
		t.Fatalf("defaultCommands() has %d commands, want %d", len(cmds), len(fixed)+len(theme.All()))
	}

	for i, f := range fixed {
		if cmds[i].id != f.id || cmds[i].title != f.title || cmds[i].icon != f.icon {
			t.Errorf("command %d = %q %q %q, want %s %s %q", i, cmds[i].id, cmds[i].title, cmds[i].icon, f.id, f.title, f.icon)
		}
	}

	for i, th := range theme.All() {
		c := cmds[len(fixed)+i]
		if c.id != "theme.set."+th.Name || c.title != "Theme: "+th.Title || c.icon != iconTheme {
			t.Errorf("command %d = %q %q %q, want theme.set.%s Theme: %s %q", len(fixed)+i, c.id, c.title, c.icon, th.Name, th.Title, iconTheme)
		}
	}
}

func TestThemeCommands(t *testing.T) {
	t.Parallel()
	a := newTestApp(t)
	sized(t, a, 100, 30)

	c, ok := a.reg.get("theme.set.catppuccin-latte")
	if !ok {
		t.Fatal("theme.set.catppuccin-latte is not registered")
	}

	if cmd := c.run(a); cmd != nil {
		t.Errorf("run() cmd = %v, want nil", cmd)
	}

	if got := a.store.State().Theme; got != "catppuccin-latte" {
		t.Errorf("store theme = %q, want catppuccin-latte", got)
	}

	v := a.View()
	if want := mustTheme(t, "catppuccin-latte").UI.Bg; v.BackgroundColor != want {
		t.Errorf("BackgroundColor = %v, want %v", v.BackgroundColor, want)
	}
}
