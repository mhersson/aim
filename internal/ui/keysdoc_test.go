package ui

import (
	"flag"
	"fmt"
	"os"
	"slices"
	"strings"
	"testing"

	"github.com/mhersson/aim/internal/core"
)

const keysPagePath = "../../docs/guide/keys.md"

// keysContexts are the tables the page has a section for, in page order.
var keysContexts = []struct {
	context, title, applies string
	table                   func(keyTables) keyTable
}{
	{"drawer", "Review drawer", "These keys apply while the review drawer has the keyboard.", func(t keyTables) keyTable { return t.drawer }},
	{"rail", "Agents rail", "These keys apply while the agents rail has the keyboard.", func(t keyTables) keyTable { return t.rail }},
	{"pr_list", "PR list", "These keys apply while the pull request list is open.", func(t keyTables) keyTable { return t.prList }},
	{"checks", "Checks overlay", "These keys apply while the checks overlay is open.", func(t keyTables) keyTable { return t.checks }},
	{"reader", "Reading overlays", "These keys apply in the plan overlay, a markdown file overlay and the conversation.", func(t keyTables) keyTable { return t.reader }},
	{"merge", "Merge box", "These keys apply while the merge box is open.", func(t keyTables) keyTable { return t.merge }},
	{"compose", "Compose box", "These keys apply while the compose box is open.", func(t keyTables) keyTable { return t.compose }},
}

// mdCell writes s as a code span for a table cell, each | escaped.
func mdCell(s string) string {
	return "`" + strings.ReplaceAll(s, "|", `\|`) + "`"
}

// keysWith is a config holding one entry in one [keys.<context>] table.
func keysWith(context, key, command string) core.KeysConfig {
	m := map[string]string{key: command}
	cfg := core.KeysConfig{Order: map[string][]string{context: {key}}}

	switch context {
	case "leader":
		cfg.Leader = m
	case "drawer":
		cfg.Drawer = m
	case "rail":
		cfg.Rail = m
	case "pr_list":
		cfg.PRList = m
	case "checks":
		cfg.Checks = m
	case "reader":
		cfg.Reader = m
	case "merge":
		cfg.Merge = m
	case "compose":
		cfg.Compose = m
	}

	return cfg
}

// bindableIn lists the contexts whose table takes the command, as the real
// resolver decides; no default binds f12.
func bindableIn(reg *registry, id string) []string {
	var out []string

	for _, c := range core.KeyContexts() {
		if _, _, err := resolveKeys(keysWith(c, "f12", id), reg); err == nil {
			out = append(out, c)
		}
	}

	return out
}

func titleCell(s string) string {
	return strings.ReplaceAll(s, "|", `\|`)
}

// keysPage is the keys page's Markdown.
func keysPage() (string, error) {
	reg, err := newRegistry(defaultCommands())
	if err != nil {
		return "", fmt.Errorf("build command registry: %w", err)
	}

	km, tables, err := resolveKeys(core.DefaultConfig().Keys, reg)
	if err != nil {
		return "", fmt.Errorf("resolve default keys: %w", err)
	}

	title := func(id string) string {
		if c, ok := reg.get(id); ok {
			return c.title
		}

		return id
	}

	var b strings.Builder

	w := func(format string, args ...any) { fmt.Fprintf(&b, format, args...) }

	w("# Keys\n\n")
	w("This page is generated from AIM's keymap by `TestKeysPage` in `internal/ui`. Do not edit it by hand; regenerate it with `go test ./internal/ui -run TestKeysPage -update`.\n\n")
	w("These are the defaults. `[keys]` in the config changes them (see [Remapping keys](config.md#remapping-keys)). The Command column is what a `[keys.*]` entry names.\n\n")

	w("## The leader\n\n")
	w("The leader key is %s (`leader_key`). Pressing it and waiting %d ms (`which_key_delay`) opens the which-key popup. ", mdCell(km.leader), km.delay.Milliseconds())
	w("A key written as two keys, such as `g l`, is two keys pressed in turn after the leader.\n\n")
	w("On macOS, once a second input source is on, the system takes `ctrl+space` for \"Select the previous input source\", so AIM never sees it. ")
	w("Turn that shortcut off in System Settings, Keyboard, Keyboard Shortcuts, Input Sources, or set another leader, such as `leader_key = \"ctrl+a\"` under `[keys]` (see [The leader key](config.md#the-leader-key)).\n\n")

	w("## After the leader\n\n")
	w("Keys pressed after the leader key. Remap them under `[keys.leader]`.\n\n")

	groups := groupOrder()

	for _, bd := range km.bindings {
		if !slices.Contains(groups, bd.group) {
			groups = append(groups, bd.group)
		}
	}

	for _, g := range groups {
		w("### %s\n\n| Key | Command | Title |\n| --- | --- | --- |\n", g)

		for _, bd := range km.bindings {
			if bd.group == g {
				w("| %s | %s | %s |\n", mdCell(bd.key), mdCell(bd.command), titleCell(title(bd.command)))
			}
		}

		w("\n")
	}

	bound := map[string]bool{}

	for _, bd := range km.bindings {
		bound[bd.command] = true
	}

	for _, c := range keysContexts {
		binds := c.table(tables).binds

		w("## %s\n\n", c.title)
		w("The table is `[keys.%s]`. %s\n\n", c.context, c.applies)
		w("| Key | Command | Title |\n| --- | --- | --- |\n")

		for _, bd := range binds {
			bound[bd.command] = true

			w("| %s | %s | %s |\n", mdCell(bd.key), mdCell(bd.command), titleCell(title(bd.command)))
		}

		w("\n")

		if c.context != "drawer" {
			continue
		}

		var tree, cells []string

		for _, bd := range binds {
			if treeCommand(bd.command) && !slices.Contains(tree, bd.command) {
				tree = append(tree, bd.command)
				cells = append(cells, "`"+bd.command+"`")
			}
		}

		w("While the file tree has the keyboard, only these commands work: %s.\n\n", strings.Join(cells, ", "))
	}

	w("## Commands without a default key\n\n")
	w("These commands are in the command palette. Bind one under a table that takes it.\n\n")
	w("| Command | Title | Bind under |\n| --- | --- | --- |\n")

	for _, c := range reg.all() {
		if bound[c.id] {
			continue
		}

		var under []string

		for _, ctx := range bindableIn(reg, c.id) {
			under = append(under, mdCell("[keys."+ctx+"]"))
		}

		w("| %s | %s | %s |\n", mdCell(c.id), titleCell(c.title), strings.Join(under, ", "))
	}

	w("\n## Keys you cannot remap\n\n")
	w("These keys are fixed. Each is described where it is used.\n\n")
	w("- Copy mode's keys: [copy mode](copy-paste-mouse.md#copy-mode).\n")
	w("- The leader pressed twice, which sends it to the pane, and `esc` in a two-key sequence: [the leader key](agents.md#the-leader-key).\n")
	w("- Renaming a tab: [tabs](agents.md#tabs).\n")
	w("- The command palette and the theme picker: [the command palette and themes](agents.md#the-command-palette-and-themes).\n")
	w("- The worktree picker: [start an agent](agents.md#start-an-agent).\n")
	w("- Typing in the rail's search: [the agents rail](agents.md#the-agents-rail).\n")
	w("- Confirm prompts: [close panes and tabs](agents.md#close-panes-and-tabs) and [push](pull-requests.md#push).\n")
	w("- The drawer's search and worktree list: [search](review.md#search) and [the worktree list](review.md#the-worktree-list).\n")
	w("- The reading overlays' search and link chooser: [search](reading.md#search) and [links and pictures](reading.md#links-and-pictures).\n")
	w("- The compose box's fields: [open a pull request](pull-requests.md#open-a-pull-request).\n")
	w("- The Claude usage details: [Claude usage](agents.md#claude-usage).\n")
	w("- The mouse: [the mouse](copy-paste-mouse.md#the-mouse).\n")

	return b.String(), nil
}

// Not parallel: with -update it writes the page.
func TestKeysPage(t *testing.T) {
	if flag.Lookup("update") == nil {
		t.Fatal("the update flag is not defined; the golden package must stay linked")
	}

	got, err := keysPage()
	if err != nil {
		t.Fatal(err)
	}

	if flag.Lookup("update").Value.String() == "true" {
		if err := os.WriteFile(keysPagePath, []byte(got), 0o644); err != nil { //nolint:gosec // a doc page
			t.Fatal(err)
		}

		return
	}

	want, err := os.ReadFile(keysPagePath)
	if err != nil {
		t.Fatal(err)
	}

	if string(want) == got {
		return
	}

	wl, gl := strings.Split(string(want), "\n"), strings.Split(got, "\n")

	for i := 0; i < max(len(wl), len(gl)); i++ {
		var a, b string
		if i < len(wl) {
			a = wl[i]
		}

		if i < len(gl) {
			b = gl[i]
		}

		if a != b {
			t.Fatalf("docs/guide/keys.md is stale; run go test ./internal/ui -run TestKeysPage -update\nline %d:\n  file: %q\n  want: %q", i+1, a, b)
		}
	}

	t.Fatal("docs/guide/keys.md is stale; run go test ./internal/ui -run TestKeysPage -update\nit differs only in its trailing newlines")
}

func TestKeysPageListsEveryCommand(t *testing.T) {
	t.Parallel()

	reg, err := newRegistry(defaultCommands())
	if err != nil {
		t.Fatal(err)
	}

	page, err := keysPage()
	if err != nil {
		t.Fatal(err)
	}

	for _, c := range reg.all() {
		if !strings.Contains(page, "`"+c.id+"`") {
			t.Errorf("command %s is not on the keys page", c.id)
		}
	}
}

func TestKeysPageKeysLoad(t *testing.T) {
	t.Parallel()

	reg, err := newRegistry(defaultCommands())
	if err != nil {
		t.Fatal(err)
	}

	km, tables, err := resolveKeys(core.DefaultConfig().Keys, reg)
	if err != nil {
		t.Fatal(err)
	}

	check := func(context, key, command string) {
		cfg := keysWith(context, key, command)

		if err := cfg.Validate(); err != nil {
			t.Errorf("keys.%s %q = %q: Validate: %v", context, key, command, err)
		}

		if err := CheckKeys(cfg); err != nil {
			t.Errorf("keys.%s %q = %q: CheckKeys: %v", context, key, command, err)
		}
	}

	for _, bd := range km.bindings {
		check("leader", bd.key, bd.command)
	}

	for _, c := range keysContexts {
		for _, bd := range c.table(tables).binds {
			check(c.context, bd.key, bd.command)
		}
	}
}

func TestMDCell(t *testing.T) {
	t.Parallel()

	for in, want := range map[string]string{"g l": "`g l`", "a|b": "`a\\|b`"} {
		if got := mdCell(in); got != want {
			t.Errorf("mdCell(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestBindableIn(t *testing.T) {
	t.Parallel()

	reg, err := newRegistry(defaultCommands())
	if err != nil {
		t.Fatal(err)
	}

	for id, want := range map[string][]string{
		"layout.set.bsp":   {"leader"},
		"review.line.next": {"leader", "drawer"},
		"rail.next":        {"rail"},
		"compose.create":   {"compose"},
	} {
		if got := bindableIn(reg, id); !slices.Equal(got, want) {
			t.Errorf("bindableIn(%s) = %v, want %v", id, got, want)
		}
	}
}
