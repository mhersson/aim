package core

import (
	"errors"
	"fmt"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/pelletier/go-toml/v2/unstable"
)

// Defaults and sentinels of the [keys] section.
const (
	// KeyNone, as a command, unbinds the key.
	KeyNone = "none"
	// DefaultLeaderKey is the leader when the file sets none.
	DefaultLeaderKey = "ctrl+space"
	// DefaultWhichKeyDelay is the which-key delay in milliseconds.
	DefaultWhichKeyDelay = 300
)

// Bounds of keys.which_key_delay in milliseconds.
const (
	MinWhichKeyDelay = 0
	MaxWhichKeyDelay = 5000
)

// KeysConfig is the [keys] section. Validate checks the syntax of the key
// names; which commands a context takes is the UI's to check. Order keeps each
// context's keys in file order, since a map does not.
type KeysConfig struct {
	LeaderKey     string `toml:"leader_key"`
	WhichKeyDelay *int   `toml:"which_key_delay"` // milliseconds; nil means the default

	Leader  map[string]string `toml:"leader"`
	Drawer  map[string]string `toml:"drawer"`
	Rail    map[string]string `toml:"rail"`
	PRList  map[string]string `toml:"pr_list"`
	Checks  map[string]string `toml:"checks"`
	Reader  map[string]string `toml:"reader"`
	Images  map[string]string `toml:"images"`
	Merge   map[string]string `toml:"merge"`
	Compose map[string]string `toml:"compose"`

	Order map[string][]string `toml:"-"`
}

// KeyBind is one entry of a [keys] context.
type KeyBind struct{ Key, Command string }

// KeyContexts lists the [keys.<context>] tables.
func KeyContexts() []string {
	return []string{"leader", "drawer", "rail", "pr_list", "checks", "reader", "images", "merge", "compose"}
}

func (c KeysConfig) table(context string) map[string]string {
	switch context {
	case "leader":
		return c.Leader
	case "drawer":
		return c.Drawer
	case "rail":
		return c.Rail
	case "pr_list":
		return c.PRList
	case "checks":
		return c.Checks
	case "reader":
		return c.Reader
	case "images":
		return c.Images
	case "merge":
		return c.Merge
	case "compose":
		return c.Compose
	}

	return nil
}

// Binds lists a context's entries in file order. Keys Order lacks (a
// hand-built config) follow, sorted.
func (c KeysConfig) Binds(context string) []KeyBind {
	table := c.table(context)
	binds := make([]KeyBind, 0, len(table))
	seen := make(map[string]bool, len(table))

	for _, k := range c.Order[context] {
		if cmd, ok := table[k]; ok && !seen[k] {
			seen[k] = true

			binds = append(binds, KeyBind{k, cmd})
		}
	}

	var rest []string

	for k := range table {
		if !seen[k] {
			rest = append(rest, k)
		}
	}

	sort.Strings(rest)

	for _, k := range rest {
		binds = append(binds, KeyBind{k, table[k]})
	}

	return binds
}

// LeaderName is the leader key, read as the default when unset.
func (c KeysConfig) LeaderName() string {
	if c.LeaderKey == "" {
		return DefaultLeaderKey
	}

	return c.LeaderKey
}

func (c KeysConfig) delayMS() int {
	if c.WhichKeyDelay == nil {
		return DefaultWhichKeyDelay
	}

	return *c.WhichKeyDelay
}

// Delay is how long the leader waits before which-key opens.
func (c KeysConfig) Delay() time.Duration {
	return time.Duration(c.delayMS()) * time.Millisecond
}

// Validate names the first key that is malformed.
func (c KeysConfig) Validate() error {
	if c.LeaderKey != "" {
		mods, name, err := parseKeyName(c.LeaderKey)
		if err != nil {
			return fmt.Errorf("keys.leader_key: %w", err)
		}

		if !slices.ContainsFunc(mods, func(m string) bool { return m != "shift" }) && !isFunctionKey(name) {
			return fmt.Errorf("keys.leader_key: %q needs a modifier, such as ctrl+a, or a function key", c.LeaderKey)
		}
	}

	if d := c.delayMS(); d < MinWhichKeyDelay || d > MaxWhichKeyDelay {
		return fmt.Errorf("keys.which_key_delay: %d is out of range 0-%d", d, MaxWhichKeyDelay)
	}

	for _, ctx := range KeyContexts() {
		for _, b := range c.Binds(ctx) {
			if err := validateBind(ctx, b); err != nil {
				return err
			}
		}
	}

	return nil
}

func validateBind(ctx string, b KeyBind) error {
	parts := strings.Split(b.Key, " ")
	if len(parts) > 1 && ctx != "leader" {
		return fmt.Errorf("keys.%s: %q: only keys.leader takes two-key sequences", ctx, b.Key)
	}

	if slices.Contains(parts, "") {
		return fmt.Errorf("keys.%s: %q is not a key name", ctx, b.Key)
	}

	if len(parts) > 2 {
		return fmt.Errorf("keys.%s: %q: a sequence has at most two keys", ctx, b.Key)
	}

	for _, p := range parts {
		if _, _, err := parseKeyName(p); err != nil {
			if len(parts) > 1 && errors.Is(err, errNotKeyName) {
				return fmt.Errorf("keys.%s: %q is not a key name", ctx, b.Key)
			}

			return fmt.Errorf("keys.%s: %w", ctx, err)
		}
	}

	if b.Command == "" {
		return fmt.Errorf("keys.%s: %q has no command", ctx, b.Key)
	}

	return nil
}

// errNotKeyName marks a key part that names no key.
var errNotKeyName = errors.New("is not a key name")

// keyModifiers are Bubble Tea's modifier prefixes, in the order it writes them.
var keyModifiers = []string{"ctrl", "alt", "shift", "meta", "hyper", "super"}

var keyNames = []string{
	"enter", "tab", "backspace", "esc", "space", "up", "down", "left", "right", "begin", "find",
	"insert", "delete", "select", "pgup", "pgdown", "home", "end", "menu", "pause", "printscreen",
}

// parseKeyName splits a Bubble Tea key string into its modifiers and base key.
func parseKeyName(s string) (mods []string, name string, err error) {
	rest := s
	next := 0

	for next < len(keyModifiers) {
		m := keyModifiers[next]
		if strings.HasPrefix(rest, m+"+") && len(rest) > len(m)+1 {
			mods = append(mods, m)
			rest = rest[len(m)+1:]
		}

		next++
	}

	switch {
	case slices.Contains(keyNames, rest), isFunctionKey(rest):
	case utf8.RuneCountInString(rest) == 1 && unicode.IsPrint([]rune(rest)[0]) && rest != " ":
		if slices.Contains(mods, "shift") {
			if up := strings.ToUpper(rest); up != rest {
				return nil, "", fmt.Errorf("%q: write the shifted character, such as %q", s, up)
			}

			return nil, "", fmt.Errorf("%q: write the shifted character", s)
		}
	default:
		return nil, "", fmt.Errorf("%q %w", s, errNotKeyName)
	}

	return mods, rest, nil
}

func isFunctionKey(s string) bool {
	n, ok := strings.CutPrefix(s, "f")
	if !ok || n == "" || n[0] == '0' {
		return false
	}

	v, err := strconv.Atoi(n)

	return err == nil && v >= 1 && v <= 63 && strconv.Itoa(v) == n
}

// keyOrder reads each context's keys in file order from the config text.
// Table headers, dotted keys and inline tables all count.
func keyOrder(data []byte) map[string][]string {
	var order map[string][]string

	add := func(path []string) {
		if len(path) < 3 || path[0] != "keys" {
			return
		}

		if order == nil {
			order = map[string][]string{}
		}

		if !slices.Contains(order[path[1]], path[2]) {
			order[path[1]] = append(order[path[1]], path[2])
		}
	}

	var (
		p     unstable.Parser
		table []string
	)

	p.Reset(data)

	for p.NextExpression() {
		e := p.Expression()

		switch e.Kind {
		case unstable.Table, unstable.ArrayTable:
			table = keyParts(e.Key())
		case unstable.KeyValue:
			path := append(slices.Clone(table), keyParts(e.Key())...)

			if v := e.Value(); v != nil && v.Kind == unstable.InlineTable {
				for it := v.Children(); it.Next(); {
					kv := it.Node()
					add(append(slices.Clone(path), keyParts(kv.Key())...))
				}

				continue
			}

			add(path)
		}
	}

	return order
}

func keyParts(it unstable.Iterator) []string {
	var parts []string

	for it.Next() {
		parts = append(parts, string(it.Node().Data))
	}

	return parts
}
