package ui

import (
	"slices"
	"strings"
	"testing"
)

func TestEveryBindingResolves(t *testing.T) {
	t.Parallel()

	reg, err := newRegistry(defaultCommands())
	if err != nil {
		t.Fatal(err)
	}

	km := defaultKeymap()
	seen := map[string]bool{}

	for _, b := range km.bindings {
		if _, ok := reg.get(b.command); !ok {
			t.Errorf("binding %q names unknown command %q", b.key, b.command)
		}

		if seen[b.key] {
			t.Errorf("key %q is bound twice", b.key)
		}

		seen[b.key] = true
		if !slices.Contains([]string{"Panes", "Layout", "Tabs", "Git", "App"}, b.group) {
			t.Errorf("binding %q has group %q, want Panes, Layout, Tabs, Git or App", b.key, b.group)
		}
	}
}

func TestDefaultKeymap(t *testing.T) {
	t.Parallel()

	km := defaultKeymap()
	if km.leader != "ctrl+space" {
		t.Errorf("leader = %q, want ctrl+space", km.leader)
	}

	if km.delay.Milliseconds() != 300 {
		t.Errorf("delay = %v, want 300ms", km.delay)
	}
}

func TestKeymapLookup(t *testing.T) {
	t.Parallel()

	km := defaultKeymap()

	tests := []struct {
		key     string
		wantCmd string
		wantOK  bool
	}{
		{"q", "app.quit", true},
		{":", "palette.open", true},
		{"t", "theme.pick", true},
		{"x", "pane.close", true},
		{"H", "pane.resize.left", true},
		{"w", "", false},
		{"", "", false},
	}
	for _, tt := range tests {
		t.Run(tt.key, func(t *testing.T) {
			got, ok := km.lookup(tt.key)
			if got != tt.wantCmd || ok != tt.wantOK {
				t.Errorf("lookup(%q) = %q, %v, want %q, %v", tt.key, got, ok, tt.wantCmd, tt.wantOK)
			}
		})
	}
}

func TestKeymapKeyFor(t *testing.T) {
	t.Parallel()

	km := defaultKeymap()

	tests := []struct {
		command string
		want    string
		wantOK  bool
	}{
		{"app.quit", "ctrl+space q", true},
		{"palette.open", "ctrl+space :", true},
		{"theme.pick", "ctrl+space t", true},
		{"pane.split.right", "ctrl+space v", true},
		{"theme.set.catppuccin-latte", "", false},
	}
	for _, tt := range tests {
		t.Run(tt.command, func(t *testing.T) {
			got, ok := km.keyFor(tt.command)
			if got != tt.want || ok != tt.wantOK {
				t.Errorf("keyFor(%q) = %q, %v, want %q, %v", tt.command, got, ok, tt.want, tt.wantOK)
			}
		})
	}
}

func TestKeymapSequences(t *testing.T) {
	t.Parallel()

	km := defaultKeymap()

	for key, want := range map[string]string{
		"> 1": "pane.move.tab.1", "> 9": "pane.move.tab.9", ". 3": "pane.send.tab.3",
		"; h": "pane.preselect.left", "; esc": "pane.preselect.clear",
		"g l": "pr.list", "g P": "git.push", "g o": "pr.open", "g m": "pr.merge",
		"!": "pane.break", "space": "layout.cycle", "r": "layout.rotate", "=": "layout.equalize", "enter": "layout.promote",
	} {
		if got, ok := km.lookup(key); !ok || got != want {
			t.Errorf("lookup(%q) = %q, %v, want %q", key, got, ok, want)
		}
	}

	// Push takes a capital P, so a slip off g l's pull request list pushes nothing.
	if got, ok := km.lookup("g p"); ok {
		t.Errorf("lookup(%q) = %q, want unbound", "g p", got)
	}

	for _, key := range []string{">", ".", ";", "g"} {
		if !km.isPrefix(key) {
			t.Errorf("isPrefix(%q) = false", key)
		}
	}

	for _, key := range []string{"q", "1", "h", "!", "p", "o", "m", ""} {
		if km.isPrefix(key) {
			t.Errorf("isPrefix(%q) = true", key)
		}
	}
}

func TestNoKeyIsBothSingleAndPrefix(t *testing.T) {
	t.Parallel()

	km := defaultKeymap()

	for _, b := range km.bindings {
		first, _, seq := strings.Cut(b.key, " ")
		if !seq {
			if km.isPrefix(b.key) {
				t.Errorf("%q is both a binding and a prefix", b.key)
			}

			continue
		}

		if strings.Contains(strings.TrimPrefix(b.key, first+" "), " ") {
			t.Errorf("binding %q has more than two keys", b.key)
		}

		if _, ok := km.lookup(first); ok {
			t.Errorf("prefix %q is also bound as a single key", first)
		}
	}
}
