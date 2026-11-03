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

	fixed := []string{
		"palette.open",
		"theme.pick",
		"agent.new",
		"rail.toggle",
		"usage.toggle",
		"pane.split.down",
		"pane.split.right",
	}
	for _, d := range []string{"left", "down", "up", "right"} {
		fixed = append(fixed, "pane.focus."+d)
	}

	for _, d := range []string{"left", "down", "up", "right"} {
		fixed = append(fixed, "pane.resize."+d)
	}

	for _, d := range []string{"left", "down", "up", "right"} {
		fixed = append(fixed, "pane.swap."+d)
	}

	fixed = append(fixed,
		"pane.zoom",
		"pane.close",
		"copy.selection",
		"copy.mode",
		"tab.new",
		"tab.rename",
		"tab.close",
		"tab.next",
		"tab.prev",
	)
	for n := 1; n <= 9; n++ {
		fixed = append(fixed, fmt.Sprintf("tab.jump.%d", n))
	}

	for n := 1; n <= 9; n++ {
		fixed = append(fixed, fmt.Sprintf("pane.move.tab.%d", n))
	}

	for n := 1; n <= 9; n++ {
		fixed = append(fixed, fmt.Sprintf("pane.send.tab.%d", n))
	}

	fixed = append(fixed,
		"pane.break",
		"layout.cycle",
		"layout.set.bsp",
		"layout.set.master-stack",
		"layout.set.scrolling",
		"pane.preselect.left",
		"pane.preselect.down",
		"pane.preselect.up",
		"pane.preselect.right",
		"pane.preselect.clear",
		"layout.rotate",
		"layout.equalize",
		"layout.promote",
		"review.toggle",
		"review.line.next",
		"review.line.prev",
		"review.hunk.next",
		"review.hunk.prev",
		"review.file.next",
		"review.file.prev",
		"review.page.down",
		"review.page.up",
		"review.top",
		"review.bottom",
		"review.tree",
		"review.full",
		"review.markdown",
		"review.split",
		"review.fold",
		"review.mark",
		"review.show.reviewed",
		"review.mode.branch",
		"review.mode.turn",
		"review.mode.pr",
		"review.conversation",
		"review.checks",
		"review.approve",
		"review.merge",
		"review.browser",
		"review.turn.prev",
		"review.turn.next",
		"review.search",
		"review.match.next",
		"review.match.prev",
		"review.copy",
		"review.zoom",
		"review.worktrees",
		"review.edit",
		"review.refresh",
		"review.back",
		"plan.open",
		"md.open",
		"pr.list",
		"git.push",
		"pr.open",
		"pr.merge",
		"worktree.cleanup",
		"app.quit",
	)
	if len(cmds) != len(fixed)+len(theme.All()) {
		t.Fatalf("defaultCommands() has %d commands, want %d", len(cmds), len(fixed)+len(theme.All()))
	}

	for i, f := range fixed {
		if cmds[i].id != f {
			t.Errorf("command %d = %q, want %s", i, cmds[i].id, f)
		}
	}

	for i, th := range theme.All() {
		c := cmds[len(fixed)+i]
		if c.id != "theme.set."+th.Name {
			t.Errorf("command %d = %q, want theme.set.%s", len(fixed)+i, c.id, th.Name)
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
