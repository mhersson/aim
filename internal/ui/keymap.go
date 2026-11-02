package ui

import (
	"strconv"
	"strings"
	"time"
)

// binding maps a key pressed after the leader to a command id; group names
// the which-key section it is listed under. A sequence is two key names
// joined by one space, as in "> 1"; no key name contains a space.
type binding struct{ key, command, group string }

// keymap binds keys pressed after the leader key to commands.
type keymap struct {
	leader   string
	delay    time.Duration // leader wait before the which-key popup opens
	bindings []binding
}

// defaultKeymap returns the built-in bindings.
func defaultKeymap() keymap {
	km := keymap{
		leader: "ctrl+space",
		delay:  300 * time.Millisecond,
		bindings: []binding{
			{key: ":", command: "palette.open", group: "App"},
			{key: "t", command: "theme.pick", group: "App"},
			{key: "u", command: "usage.toggle", group: "App"},
			{key: "n", command: "agent.new", group: "Panes"},
			{key: "a", command: "rail.toggle", group: "Panes"},
			{key: "d", command: "review.toggle", group: "Panes"},
			{key: "p", command: "plan.open", group: "Panes"},
			{key: "s", command: "pane.split.down", group: "Panes"},
			{key: "v", command: "pane.split.right", group: "Panes"},
			{key: "h", command: "pane.focus.left", group: "Panes"},
			{key: "j", command: "pane.focus.down", group: "Panes"},
			{key: "k", command: "pane.focus.up", group: "Panes"},
			{key: "l", command: "pane.focus.right", group: "Panes"},
			{key: "H", command: "pane.resize.left", group: "Panes"},
			{key: "J", command: "pane.resize.down", group: "Panes"},
			{key: "K", command: "pane.resize.up", group: "Panes"},
			{key: "L", command: "pane.resize.right", group: "Panes"},
			{key: "ctrl+h", command: "pane.swap.left", group: "Panes"},
			{key: "ctrl+j", command: "pane.swap.down", group: "Panes"},
			{key: "ctrl+k", command: "pane.swap.up", group: "Panes"},
			{key: "ctrl+l", command: "pane.swap.right", group: "Panes"},
			{key: "z", command: "pane.zoom", group: "Panes"},
			{key: "x", command: "pane.close", group: "Panes"},
			{key: "y", command: "copy.selection", group: "Panes"},
			{key: "[", command: "copy.mode", group: "Panes"},
			{key: "space", command: "layout.cycle", group: "Layout"},
			{key: "; h", command: "pane.preselect.left", group: "Layout"},
			{key: "; j", command: "pane.preselect.down", group: "Layout"},
			{key: "; k", command: "pane.preselect.up", group: "Layout"},
			{key: "; l", command: "pane.preselect.right", group: "Layout"},
			{key: "; esc", command: "pane.preselect.clear", group: "Layout"},
			{key: "r", command: "layout.rotate", group: "Layout"},
			{key: "=", command: "layout.equalize", group: "Layout"},
			{key: "enter", command: "layout.promote", group: "Layout"},
			{key: "c", command: "tab.new", group: "Tabs"},
			{key: ",", command: "tab.rename", group: "Tabs"},
			{key: "X", command: "tab.close", group: "Tabs"},
			{key: "tab", command: "tab.next", group: "Tabs"},
			{key: "shift+tab", command: "tab.prev", group: "Tabs"},
			{key: "1", command: "tab.jump.1", group: "Tabs"},
			{key: "2", command: "tab.jump.2", group: "Tabs"},
			{key: "3", command: "tab.jump.3", group: "Tabs"},
			{key: "4", command: "tab.jump.4", group: "Tabs"},
			{key: "5", command: "tab.jump.5", group: "Tabs"},
			{key: "6", command: "tab.jump.6", group: "Tabs"},
			{key: "7", command: "tab.jump.7", group: "Tabs"},
			{key: "8", command: "tab.jump.8", group: "Tabs"},
			{key: "9", command: "tab.jump.9", group: "Tabs"},
			{key: "!", command: "pane.break", group: "Tabs"},
			{key: "g l", command: "pr.list", group: "Git"},
			{key: "g P", command: "git.push", group: "Git"},
			{key: "g o", command: "pr.open", group: "Git"},
			{key: "g m", command: "pr.merge", group: "Git"},
			{key: "q", command: "app.quit", group: "App"},
		},
	}
	for n := 1; n <= 9; n++ {
		km.bindings = append(km.bindings,
			binding{key: "> " + strconv.Itoa(n), command: "pane.move.tab." + strconv.Itoa(n), group: "Tabs"},
			binding{key: ". " + strconv.Itoa(n), command: "pane.send.tab." + strconv.Itoa(n), group: "Tabs"})
	}

	return km
}

// isPrefix reports whether key starts a two-key sequence such as "> 1".
func (k keymap) isPrefix(key string) bool {
	if key == "" {
		return false
	}

	for _, b := range k.bindings {
		if strings.HasPrefix(b.key, key+" ") {
			return true
		}
	}

	return false
}

func (k keymap) lookup(key string) (string, bool) {
	for _, b := range k.bindings {
		if b.key == key {
			return b.command, true
		}
	}

	return "", false
}

// keyOf returns the key a command is bound to after the leader, e.g. "q".
func (k keymap) keyOf(command string) (string, bool) {
	for _, b := range k.bindings {
		if b.command == command {
			return b.key, true
		}
	}

	return "", false
}

// keyFor returns the full key sequence for a command, e.g. "ctrl+space q".
func (k keymap) keyFor(command string) (string, bool) {
	key, ok := k.keyOf(command)
	if !ok {
		return "", false
	}

	return k.leader + " " + key, true
}

// groupOf returns the which-key group of the binding for a command.
func (k keymap) groupOf(command string) string {
	for _, b := range k.bindings {
		if b.command == command {
			return b.group
		}
	}

	return ""
}
