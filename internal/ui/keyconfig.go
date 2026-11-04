package ui

import (
	"fmt"
	"slices"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/mhersson/aim/internal/core"
	"github.com/mhersson/aim/internal/ui/theme"
)

// CheckKeys reports the first mistake in the [keys] config that the UI can
// find: a command that does not exist or does not belong to its context, or a
// key that is both a key and the start of a sequence. main calls it before the
// TUI starts, with the themes AIM offers, whose commands a key may name.
func CheckKeys(cfg core.KeysConfig, themes theme.Set) error {
	reg, err := newRegistry(commands(themes))
	if err != nil {
		return fmt.Errorf("build command registry: %w", err)
	}

	_, _, err = resolveKeys(cfg, reg)

	return err
}

// resolveLeader lays the config over the built-in leader keymap. A config key
// replaces a default binding of the same key, and goes right before its
// command's first remaining default binding, else at the end of the command's
// group, so the command's first key is the first the config lists.
func resolveLeader(cfg core.KeysConfig, reg *registry) (keymap, error) {
	defaults := defaultKeymap()
	km := defaults
	km.leader = cfg.LeaderName()
	km.delay = cfg.Delay()

	binds := cfg.Binds("leader")

	for _, b := range binds {
		if first, _, _ := strings.Cut(b.Key, " "); first == km.leader {
			return keymap{}, fmt.Errorf("keys.leader_key: %q is also bound under keys.leader", km.leader)
		}

		if b.Command == core.KeyNone {
			continue
		}

		c, ok := reg.get(b.Command)
		if !ok {
			return keymap{}, fmt.Errorf("keys.leader: %q = %q: no such command", b.Key, b.Command)
		}

		if c.scope != "" {
			return keymap{}, fmt.Errorf("keys.leader: %q = %q: a %s command", b.Key, b.Command, scopeNoun(c.scope))
		}
	}

	km.bindings = layBinds(defaults.bindings, binds, func(id string) string { return groupFor(defaults, id) })

	for _, b := range km.bindings {
		first, _, ok := strings.Cut(b.key, " ")
		if !ok {
			continue
		}

		if _, bound := km.lookup(first); bound {
			return keymap{}, fmt.Errorf("keys.leader: %q is bound and starts %q", first, b.key)
		}
	}

	return km, nil
}

// keyTable is a context's resolved keys, in order: the first key of a command
// is the one its hints show.
type keyTable struct{ binds []reviewKey }

// keyTables holds the resolved tables of the contexts other than the leader.
type keyTables struct {
	drawer  keyTable
	rail    keyTable
	prList  keyTable
	checks  keyTable
	reader  keyTable
	merge   keyTable
	compose keyTable
}

// lookup is the command a key runs.
func (t keyTable) lookup(key string) (string, bool) {
	if i := slices.IndexFunc(t.binds, func(b reviewKey) bool { return b.key == key }); i >= 0 {
		return t.binds[i].command, true
	}

	return "", false
}

// has reports whether key is bound in the table.
func (t keyTable) has(key string) bool {
	_, ok := t.lookup(key)

	return ok
}

// first is the first key bound to a command.
func (t keyTable) first(id string) (string, bool) {
	if i := slices.IndexFunc(t.binds, func(b reviewKey) bool { return b.command == id }); i >= 0 {
		return t.binds[i].key, true
	}

	return "", false
}

// keyMods splits key into its modifier prefix ("ctrl+alt+") and the rest, leaving
// at least one character in base.
func keyMods(key string) (mods, base string) {
	base = key

	for _, m := range []string{"ctrl+", "alt+", "shift+", "meta+", "hyper+", "super+"} {
		if rest, ok := strings.CutPrefix(base, m); ok && rest != "" {
			base = rest
		}
	}

	return key[:len(key)-len(base)], base
}

// hint joins the first keys of the commands with "/"; it is "" when one has no key.
// Keys that all carry the same modifiers share them (ctrl+d/u), and a key without
// modifiers after one with them follows a comma (ctrl+d, u).
func (t keyTable) hint(ids ...string) string {
	keys := make([]string, len(ids))

	for i, id := range ids {
		k, ok := t.first(id)
		if !ok {
			return ""
		}

		keys[i] = k
	}

	shared := true
	firstMods, _ := keyMods(keys[0])

	for _, k := range keys {
		if m, _ := keyMods(k); m == "" || m != firstMods {
			shared = false
		}
	}

	var b strings.Builder

	prev := ""

	for i, k := range keys {
		mods, base := keyMods(k)

		if i > 0 {
			if prev != "" && mods == "" {
				b.WriteString(", ")
			} else {
				b.WriteString("/")
			}

			if shared {
				k = base
			}
		}

		b.WriteString(k)

		prev = mods
	}

	return b.String()
}

// composeTypes reports whether the compose box's fields take key as text or
// cursor movement: a bare or shifted printable character or editing key.
func composeTypes(key string) bool {
	mods, base := keyMods(key)
	if mods != "" && mods != "shift+" {
		return false
	}

	switch base {
	case "space", "enter", "backspace", "delete", "up", "down", "left", "right", "home", "end", "pgup", "pgdown":
		return true
	}

	return utf8.RuneCountInString(base) == 1 && unicode.IsPrint([]rune(base)[0])
}

// resolveKeys lays the config over the leader keymap and the context tables.
func resolveKeys(cfg core.KeysConfig, reg *registry) (keymap, keyTables, error) {
	km, err := resolveLeader(cfg, reg)
	if err != nil {
		return keymap{}, keyTables{}, err
	}

	var tables keyTables

	contexts := []struct {
		context, noun string
		defaults      []reviewKey
		accept        func(id string) bool
		into          *keyTable
		// close is the command that ends a view the leader does not reach; "" when the leader closes it.
		close, closeNoun string
	}{
		{"drawer", "drawer", reviewKeys, func(id string) bool { return strings.HasPrefix(id, "review.") }, &tables.drawer, "", ""},
		{"rail", "rail", railKeys, scopedTo(reg, "rail"), &tables.rail, "", ""},
		{"pr_list", "PR list", prListKeys, scopedTo(reg, "pr_list"), &tables.prList, "prlist.close", "PR list"},
		{"checks", "checks", checksKeys, scopedTo(reg, "checks"), &tables.checks, "checks.close", "checks overlay"},
		{"reader", "reader", readerKeys, scopedTo(reg, "reader"), &tables.reader, "reader.close", "overlay"},
		{"merge", "merge", mergeKeys, scopedTo(reg, "merge"), &tables.merge, "merge.cancel", "merge box"},
		{"compose", "compose", composeKeys, scopedTo(reg, "compose"), &tables.compose, "compose.cancel", "compose box"},
	}

	for _, c := range contexts {
		// The leader is routed before the drawer and the rail, and runs in the compose box while a create is in flight.
		if c.context == "drawer" || c.context == "rail" || c.context == "compose" {
			for _, b := range cfg.Binds(c.context) {
				if b.Key == km.leader {
					return keymap{}, keyTables{}, fmt.Errorf("keys.%s: %q is the leader key", c.context, b.Key)
				}
			}
		}

		t, err := resolveTable(c.context, c.noun, c.defaults, cfg, c.accept, reg)
		if err != nil {
			return keymap{}, keyTables{}, err
		}

		if c.context == "compose" {
			for _, b := range cfg.Binds(c.context) {
				if b.Command != core.KeyNone && composeTypes(b.Key) {
					return keymap{}, keyTables{}, fmt.Errorf("keys.compose: %q is a key the title and body take", b.Key)
				}
			}
		}

		if c.close != "" {
			if _, ok := t.first(c.close); !ok {
				return keymap{}, keyTables{}, fmt.Errorf("keys.%s: nothing is bound to %s, so the %s could not close", c.context, c.close, c.closeNoun)
			}
		}

		*c.into = t
	}

	return km, tables, nil
}

// scopedTo accepts the commands of one view.
func scopedTo(reg *registry, scope string) func(id string) bool {
	return func(id string) bool {
		c, ok := reg.get(id)

		return ok && c.scope == scope
	}
}

// resolveTable lays a context's config over its default keys; accept says
// which commands the context takes.
func resolveTable(context, noun string, defaults []reviewKey, cfg core.KeysConfig, accept func(id string) bool, reg *registry) (keyTable, error) {
	binds := cfg.Binds(context)

	for _, b := range binds {
		if b.Command == core.KeyNone {
			continue
		}

		if _, ok := reg.get(b.Command); !ok {
			return keyTable{}, fmt.Errorf("keys.%s: %q = %q: no such command", context, b.Key, b.Command)
		}

		if !accept(b.Command) {
			return keyTable{}, fmt.Errorf("keys.%s: %q = %q: not a %s command", context, b.Key, b.Command, noun)
		}
	}

	base := make([]binding, len(defaults))
	for i, d := range defaults {
		base[i] = binding{key: d.key, command: d.command}
	}

	laid := layBinds(base, binds, func(string) string { return "" })
	out := make([]reviewKey, len(laid))

	for i, b := range laid {
		out[i] = reviewKey{b.key, b.command}
	}

	return keyTable{binds: out}, nil
}

// layBinds lays config entries over base: an entry replaces a base binding of
// the same key, and goes right before its command's first remaining base
// binding, else at the end of the command's group.
func layBinds(base []binding, binds []core.KeyBind, group func(id string) string) []binding {
	out := slices.DeleteFunc(slices.Clone(base), func(d binding) bool {
		return slices.ContainsFunc(binds, func(b core.KeyBind) bool { return b.Key == d.key })
	})
	added := make([]bool, len(out))

	for _, b := range binds {
		if b.Command == core.KeyNone {
			continue
		}

		nb := binding{key: b.Key, command: b.Command, group: group(b.Command)}
		at := insertAt(out, added, nb)
		out = slices.Insert(out, at, nb)
		added = slices.Insert(added, at, true)
	}

	return out
}

// insertAt is where a config binding goes in bindings; added marks the config's
// own entries. It goes before the command's first default binding, else after
// the config's earlier keys for the command, else after its group.
func insertAt(bindings []binding, added []bool, nb binding) int {
	for i, b := range bindings {
		if !added[i] && b.command == nb.command {
			return i
		}
	}

	if i := slices.IndexFunc(bindings, func(b binding) bool { return b.command == nb.command }); i >= 0 {
		for i < len(bindings) && bindings[i].command == nb.command {
			i++
		}

		return i
	}

	for i, binding := range slices.Backward(bindings) {
		if binding.group == nb.group {
			return i + 1
		}
	}

	return len(bindings)
}

// groupFor is the which-key group of a command: the group of its binding in
// km, else the group its id prefix names.
func groupFor(km keymap, id string) string {
	if g := km.groupOf(id); g != "" {
		return g
	}

	switch {
	case strings.HasPrefix(id, "layout."):
		return "Layout"
	case strings.HasPrefix(id, "tab."):
		return "Tabs"
	case strings.HasPrefix(id, "pr."), strings.HasPrefix(id, "git."), strings.HasPrefix(id, "worktree."):
		return "Git"
	}

	for _, p := range []string{"pane.", "agent.", "copy.", "rail.", "review.", "md.", "plan."} {
		if strings.HasPrefix(id, p) {
			return "Panes"
		}
	}

	return "App"
}

// keysOf returns every key a command is bound to after the leader, in order.
func (k keymap) keysOf(id string) []string {
	var keys []string

	for _, b := range k.bindings {
		if b.command == id {
			keys = append(keys, b.key)
		}
	}

	return keys
}

// preselectPrefix reports whether key opens a sequence of preselect commands,
// which work only in a BSP tab.
func (k keymap) preselectPrefix(key string) bool {
	found := false

	for _, b := range k.bindings {
		if !strings.HasPrefix(b.key, key+" ") {
			continue
		}

		if !strings.HasPrefix(b.command, "pane.preselect.") {
			return false
		}

		found = true
	}

	return found
}
