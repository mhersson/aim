package ui

import (
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"

	"github.com/mhersson/aim/internal/core"
	"github.com/mhersson/aim/internal/ui/theme"
)

// contextKeys builds a [keys] config with the entries of one context, in order.
func contextKeys(context string, pairs ...string) core.KeysConfig {
	cfg := core.KeysConfig{Order: map[string][]string{}}
	table := map[string]string{}

	for i := 0; i+1 < len(pairs); i += 2 {
		table[pairs[i]] = pairs[i+1]
		cfg.Order[context] = append(cfg.Order[context], pairs[i])
	}

	switch context {
	case "rail":
		cfg.Rail = table
	case "pr_list":
		cfg.PRList = table
	case "checks":
		cfg.Checks = table
	case "reader":
		cfg.Reader = table
	case "images":
		cfg.Images = table
	case "merge":
		cfg.Merge = table
	case "compose":
		cfg.Compose = table
	}

	return cfg
}

// remapTables replaces the app's context tables with those the config resolves to.
func remapTables(t *testing.T, a *App, cfg core.KeysConfig) {
	t.Helper()

	_, tables, err := resolveKeys(cfg, a.reg)
	if err != nil {
		t.Fatal(err)
	}

	a.keys = tables
}

// sentScopedCommands is the viewer's commands less images.view, which opens it
// from anywhere and is in the palette.
func sentScopedCommands() []command {
	return slices.DeleteFunc(sentViewCommands(), func(c command) bool { return c.scope == "" })
}

func TestScopedCommands(t *testing.T) {
	t.Parallel()

	reg := testRegistry(t)

	var scoped []command

	for _, fn := range []func() []command{railCommands, prListCommands, checksCommands, readerCommands, mergeCommands, composeCommands, sentScopedCommands} {
		scoped = append(scoped, fn()...)
	}

	if len(scoped) == 0 {
		t.Fatal("no scoped commands")
	}

	a := newThemedApp(t, "catppuccin-mocha")
	a.openCommandPalette()

	var inPalette []string
	for _, it := range a.overlay.items {
		inPalette = append(inPalette, it.ID)
	}

	for _, c := range scoped {
		if _, ok := reg.get(c.id); !ok {
			t.Errorf("%s is not registered", c.id)
		}

		if c.scope == "" {
			t.Errorf("%s has no scope", c.id)
		}

		if slices.Contains(inPalette, c.id) {
			t.Errorf("%s is in the palette", c.id)
		}
	}

	// One command under the leader, then one command of another context in each
	// table; compose's mismatch is TestComposeKeyErrors'.
	tests := []struct {
		name                  string
		cfg                   core.KeysConfig
		section, key, command string
	}{
		{"rail command under the leader", leaderKeys("x", "rail.next"), "keys.leader", "x", "rail.next"},
		{"PR list command in the rail", contextKeys("rail", "x", "prlist.next"), "keys.rail", "x", "prlist.next"},
		{"rail command in the PR list", contextKeys("pr_list", "x", "rail.next"), "keys.pr_list", "x", "rail.next"},
		{"rail command in the checks", contextKeys("checks", "x", "rail.next"), "keys.checks", "x", "rail.next"},
		{"PR list command in the reader", contextKeys("reader", "x", "prlist.next"), "keys.reader", "x", "prlist.next"},
		{"reader command in the merge box", contextKeys("merge", "x", "reader.top"), "keys.merge", "x", "reader.top"},
		{"global command in the drawer", drawerKeys("x", "pr.list"), "keys.drawer", "x", "pr.list"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			err := CheckKeys(tt.cfg, theme.Builtin())
			if err == nil {
				t.Fatal("CheckKeys = nil, want an error")
			}

			for _, want := range []string{tt.section, strconv.Quote(tt.key), strconv.Quote(tt.command)} {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("CheckKeys = %v, want it to name %s", err, want)
				}
			}
		})
	}
}

func TestRailRemappedKeys(t *testing.T) {
	t.Parallel()

	t.Run("jump moves to the new key", func(t *testing.T) {
		t.Parallel()

		f := newFlowApp(t)
		f.twoTabs(t)
		remapTables(t, f.App, contextKeys("rail", "x", "rail.jump", "enter", "none"))
		f.open(t)
		press(f.App, keyRune('/'), keyRune('t'), keyEnter, keyRune('j'))

		if f.rail.sel != "p2" {
			t.Fatalf("setup: selected %s", f.rail.sel)
		}

		press(f.App, keyEnter)

		if !f.rail.focused {
			t.Fatal("enter jumped although it is unbound")
		}

		press(f.App, keyRune('x'))

		if f.rail.focused || f.store.State().Tab().Focus != "p2" {
			t.Errorf("x: focused = %v, focus = %s, want the jump to p2", f.rail.focused, f.store.State().Tab().Focus)
		}
	})

	t.Run("typing in the search stays fixed", func(t *testing.T) {
		t.Parallel()

		f := newFlowApp(t)
		f.twoTabs(t)
		remapTables(t, f.App, contextKeys("rail", "n", "rail.next", "j", "none"))
		f.open(t)
		press(f.App, keyRune('/'), keyRune('j'), keyRune('n'))

		if f.rail.query != "jn" {
			t.Errorf("query = %q, want jn", f.rail.query)
		}
	})
}

func TestPRListRemappedKeys(t *testing.T) {
	t.Parallel()

	t.Run("next and close move", func(t *testing.T) {
		t.Parallel()

		a := listApp(t, true, 120, 40, listAgents())
		remapTables(t, a, contextKeys("pr_list", "n", "prlist.next", "q", "prlist.close", "esc", "none"))

		press(a, keyLeader, keyRune('g'), keyRune('l'))

		sel := func() string {
			rows := a.prListRows(a.store.State())

			return listKeys(&rows[a.prList.selected(rows)])
		}
		first := sel()

		press(a, keyRune('n'))

		if sel() == first {
			t.Error("n did not move the selection")
		}

		press(a, keyEsc)

		if a.prList == nil {
			t.Fatal("esc closed the list although it is unbound")
		}

		press(a, keyRune('q'))

		if a.prList != nil {
			t.Error("q did not close the list")
		}
	})
}

func TestChecksRemappedKeys(t *testing.T) {
	t.Parallel()

	r, f := inChecks(t)
	remapTables(t, r.App, contextKeys("checks", "o", "checks.browser", "O", "none"))
	r.run(r.step(snapshotWithChecks("aaaaaaa", "aaaaaaa", 7, sampleChecks()[0])))

	r.typed("O")

	if got := f.urls(); len(got) != 0 {
		t.Fatalf("O opened %v although it is unbound", got)
	}

	r.keys(keyRune('o'))

	if got := f.opened(t, 1); !slices.Equal(got, []string{"https://ci.example.com/build"}) {
		t.Errorf("o opened %v, want the selected check's page", got)
	}

	r.keys(keyRune('x'))

	if r.checks == nil {
		t.Error("an unbound key closed the overlay")
	}

	r.keys(keyEsc)

	if r.checks != nil {
		t.Error("esc did not close the overlay")
	}
}

func TestContextHintsFollowBindings(t *testing.T) {
	t.Parallel()

	t.Run("rail", func(t *testing.T) {
		t.Parallel()

		tests := []struct {
			name      string
			cfg       core.KeysConfig
			want, not string
		}{
			{name: "defaults", want: "j/k move  enter jump  p plan  / search  esc back"},
			{name: "jump moved", cfg: contextKeys("rail", "x", "rail.jump"), want: "x jump"},
			{name: "plan moved", cfg: contextKeys("rail", "o", "rail.plan"), want: "o plan"},
			{name: "search unbound", cfg: contextKeys("rail", "/", "none"), want: "esc back", not: "search"},
		}
		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				t.Parallel()

				a := newThemedApp(t, "catppuccin-mocha")
				remapTables(t, a, tt.cfg)

				var b strings.Builder
				for _, run := range railHints(a.current().theme.UI, 80, a.keys.rail) {
					b.WriteString(run.text)
				}

				if got := b.String(); !strings.Contains(got, tt.want) || (tt.not != "" && strings.Contains(got, tt.not)) {
					t.Errorf("hints %q, want %q without %q", got, tt.want, tt.not)
				}
			})
		}
	})

	t.Run("PR list", func(t *testing.T) {
		t.Parallel()

		tests := []struct {
			name      string
			cfg       core.KeysConfig
			want, not string
		}{
			{name: "defaults", want: "enter diff    i conversation    c checks    a approve    M merge    O browser    r refresh    esc close"},
			{name: "browser unbound", cfg: contextKeys("pr_list", "O", "none"), want: "M merge    r refresh", not: "browser"},
			{name: "approve moved", cfg: contextKeys("pr_list", "A", "prlist.approve", "a", "none"), want: "A approve"},
		}
		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				t.Parallel()

				a := listApp(t, true, 160, 40, listAgents())
				remapTables(t, a, tt.cfg)
				press(a, keyLeader, keyRune('g'), keyRune('l'))

				got := ansi.Strip(a.View().Content)
				if !strings.Contains(got, tt.want) || (tt.not != "" && strings.Contains(got, tt.not)) {
					t.Errorf("the frame lacks %q or holds %q:\n%s", tt.want, tt.not, got)
				}
			})
		}
	})

	t.Run("checks", func(t *testing.T) {
		t.Parallel()

		tests := []struct {
			name      string
			cfg       core.KeysConfig
			want, not string
		}{
			{name: "browser moved", cfg: contextKeys("checks", "o", "checks.browser", "O", "none"), want: "o browser", not: "O browser    r"},
			{name: "refresh unbound", cfg: contextKeys("checks", "r", "none"), want: "O browser    esc close", not: "refresh"},
		}
		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				t.Parallel()

				r, _ := inChecks(t)
				remapTables(t, r.App, tt.cfg)
				r.run(r.step(snapshotWithChecks("aaaaaaa", "aaaaaaa", 7, sampleChecks()...)))

				got := ansi.Strip(r.View().Content)
				if !strings.Contains(got, tt.want) || (tt.not != "" && strings.Contains(got, tt.not)) {
					t.Errorf("the frame lacks %q or holds %q:\n%s", tt.want, tt.not, got)
				}
			})
		}
	})
}
