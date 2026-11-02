package ui

import (
	"fmt"
	"reflect"
	"slices"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/mhersson/aim/internal/core"
	"github.com/mhersson/aim/internal/git"
)

// leaderKeys builds a [keys] config with the leader entries in the given order.
func leaderKeys(pairs ...string) core.KeysConfig {
	cfg := core.KeysConfig{Leader: map[string]string{}, Order: map[string][]string{}}
	for i := 0; i+1 < len(pairs); i += 2 {
		cfg.Leader[pairs[i]] = pairs[i+1]
		cfg.Order["leader"] = append(cfg.Order["leader"], pairs[i])
	}

	return cfg
}

func testRegistry(t *testing.T) *registry {
	t.Helper()

	reg, err := newRegistry(defaultCommands())
	if err != nil {
		t.Fatal(err)
	}

	return reg
}

func TestResolveLeader(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		cfg   core.KeysConfig
		check func(t *testing.T, km keymap)
	}{
		{
			"moved key", leaderKeys("w", "pr.list", "g l", "none"),
			func(t *testing.T, km keymap) {
				if got, _ := km.lookup("w"); got != "pr.list" {
					t.Errorf("lookup(w) = %q, want pr.list", got)
				}

				if got, ok := km.lookup("g l"); ok {
					t.Errorf("lookup(g l) = %q, want it unbound", got)
				}

				if got, _ := km.keyFor("pr.list"); got != "ctrl+space w" {
					t.Errorf("keyFor = %q, want ctrl+space w", got)
				}

				if got := km.groupOf("pr.list"); got != "Git" {
					t.Errorf("group = %q, want Git", got)
				}
			},
		},
		{
			"added key", leaderKeys("w", "pr.list"),
			func(t *testing.T, km keymap) {
				if got, _ := km.keyFor("pr.list"); got != "ctrl+space w" {
					t.Errorf("keyFor = %q, want ctrl+space w", got)
				}

				if want := []string{"w", "g l"}; !slices.Equal(km.keysOf("pr.list"), want) {
					t.Errorf("keysOf = %q, want %q", km.keysOf("pr.list"), want)
				}

				for _, key := range []string{"w", "g l"} {
					if got, _ := km.lookup(key); got != "pr.list" {
						t.Errorf("lookup(%s) = %q, want pr.list", key, got)
					}
				}
			},
		},
		{
			"key taken over", leaderKeys("z", "tab.new"),
			func(t *testing.T, km keymap) {
				if keys := km.keysOf("pane.zoom"); len(keys) != 0 {
					t.Errorf("pane.zoom keys = %q, want none", keys)
				}

				if got, _ := km.lookup("z"); got != "tab.new" {
					t.Errorf("lookup(z) = %q, want tab.new", got)
				}
			},
		},
		{
			"group by prefix", leaderKeys("b", "layout.set.bsp"),
			func(t *testing.T, km keymap) {
				if got := groupFor(km, "layout.set.bsp"); got != "Layout" {
					t.Errorf("group = %q, want Layout", got)
				}

				if got := km.groupOf("layout.set.bsp"); got != "Layout" {
					t.Errorf("binding group = %q, want Layout", got)
				}
			},
		},
		{
			"file order is kept", leaderKeys("w", "pr.list", "e", "pr.list"),
			func(t *testing.T, km keymap) {
				if want := []string{"w", "e", "g l"}; !slices.Equal(km.keysOf("pr.list"), want) {
					t.Errorf("keysOf = %q, want %q", km.keysOf("pr.list"), want)
				}
			},
		},
		{
			"leader key",
			core.KeysConfig{LeaderKey: "ctrl+a"},
			func(t *testing.T, km keymap) {
				if km.leader != "ctrl+a" {
					t.Errorf("leader = %q, want ctrl+a", km.leader)
				}
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			km, err := resolveLeader(tt.cfg, testRegistry(t))
			if err != nil {
				t.Fatal(err)
			}

			tt.check(t, km)
		})
	}
}

func TestCheckKeysErrors(t *testing.T) {
	t.Parallel()

	noGit := []string{"g l", "none", "g P", "none", "g o", "none", "g m", "none"}

	tests := []struct {
		name string
		cfg  core.KeysConfig
		want string
	}{
		{"unknown command", leaderKeys("w", "pr.lst"), `keys.leader: "w" = "pr.lst": no such command`},
		{"prefix over a key", leaderKeys("g", "pr.list"), `keys.leader: "g" is bound and starts "g l"`},
		{"key under a bound key", leaderKeys("q x", "pr.list"), `keys.leader: "q" is bound and starts "q x"`},
		{"prefix freed", leaderKeys(append([]string{"g", "pr.list"}, noGit...)...), ""},
		{
			"pr list without close", contextKeys("pr_list", "esc", "none"),
			"keys.pr_list: nothing is bound to prlist.close, so the PR list could not close",
		},
		{
			"checks without close", contextKeys("checks", "esc", "none"),
			"keys.checks: nothing is bound to checks.close, so the checks overlay could not close",
		},
		{
			"reader without close", contextKeys("reader", "esc", "none"),
			"keys.reader: nothing is bound to reader.close, so the overlay could not close",
		},
		{
			"merge without cancel", contextKeys("merge", "esc", "none"),
			"keys.merge: nothing is bound to merge.cancel, so the merge box could not close",
		},
		{"merge takes the leader key", contextKeys("merge", "ctrl+space", "merge.run"), ""},
		{"reader takes the leader key", contextKeys("reader", "ctrl+space", "reader.top"), ""},
		{"pr list close moved", contextKeys("pr_list", "esc", "none", "q", "prlist.close"), ""},
		{"rail needs no close", contextKeys("rail", "esc", "none"), ""},
		{"drawer needs no close", drawerKeys("esc", "none"), ""},
		{
			"leader under leader",
			func() core.KeysConfig {
				c := leaderKeys("ctrl+a", "pr.list")
				c.LeaderKey = "ctrl+a"

				return c
			}(),
			`keys.leader_key: "ctrl+a" is also bound under keys.leader`,
		},
		{
			"sequence starting with the leader",
			func() core.KeysConfig {
				c := leaderKeys("ctrl+a x", "pr.list")
				c.LeaderKey = "ctrl+a"

				return c
			}(),
			`keys.leader_key: "ctrl+a" is also bound under keys.leader`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			err := CheckKeys(tt.cfg)
			switch {
			case tt.want == "" && err != nil:
				t.Errorf("CheckKeys() = %v, want nil", err)
			case tt.want != "" && (err == nil || err.Error() != tt.want):
				t.Errorf("CheckKeys() = %v, want %q", err, tt.want)
			}
		})
	}
}

func TestComposeKeyErrors(t *testing.T) {
	t.Parallel()

	typing := func(k string) string {
		return fmt.Sprintf("keys.compose: %q is a key the title and body take", k)
	}

	tests := []struct {
		name string
		cfg  core.KeysConfig
		want string
	}{
		{"leader key", contextKeys("compose", "ctrl+space", "compose.create"), `keys.compose: "ctrl+space" is the leader key`},
		{"no cancel", contextKeys("compose", "esc", "none"), "keys.compose: nothing is bound to compose.cancel, so the compose box could not close"},
		{"typing key with another context's command", contextKeys("compose", "s", "merge.run"), `keys.compose: "s" = "merge.run": not a compose command`},
		{"typing key none", contextKeys("compose", "s", "none", "esc", "compose.cancel"), ""},
	}
	for _, k := range []string{"s", "H", "space", "enter", "left", "pgdown", "home", "shift+left", "shift+enter", "backspace", "delete"} {
		tests = append(tests, struct {
			name string
			cfg  core.KeysConfig
			want string
		}{"refuses " + k, contextKeys("compose", k, "compose.create"), typing(k)})
	}

	for _, k := range []string{"ctrl+o", "alt+s", "f5", "tab", "insert", "ctrl+left", "shift+tab"} {
		tests = append(tests, struct {
			name string
			cfg  core.KeysConfig
			want string
		}{"accepts " + k, contextKeys("compose", k, "compose.create"), ""})
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			err := CheckKeys(tt.cfg)
			switch {
			case tt.want == "" && err != nil:
				t.Errorf("CheckKeys() = %v, want nil", err)
			case tt.want != "" && (err == nil || err.Error() != tt.want):
				t.Errorf("CheckKeys() = %v, want %q", err, tt.want)
			}
		})
	}
}

func TestComposeTypes(t *testing.T) {
	t.Parallel()

	for key, want := range map[string]bool{
		"s": true, "H": true, "shift+h": true, "shift+left": true, "space": true, "enter": true, "pgup": true,
		"ctrl+k": false, "alt+b": false, "tab": false, "esc": false, "f1": false, "insert": false, "ctrl+shift+left": false,
	} {
		if got := composeTypes(key); got != want {
			t.Errorf("composeTypes(%q) = %v, want %v", key, got, want)
		}
	}
}

func TestDefaultKeysUnchanged(t *testing.T) {
	t.Parallel()

	km, err := resolveLeader(core.KeysConfig{}, testRegistry(t))
	if err != nil {
		t.Fatal(err)
	}

	if !reflect.DeepEqual(km, defaultKeymap()) {
		t.Error("an empty config changed the default keymap")
	}
}

var keyCtrlA = tea.KeyPressMsg{Code: 'a', Mod: tea.ModCtrl}

func TestLeaderRemapScreens(t *testing.T) {
	t.Parallel()

	cfg := leaderKeys("w", "pr.list", "g l", "none")
	cfg.LeaderKey = "ctrl+a"

	app := func(t *testing.T) *App {
		t.Helper()

		a := newKeyedApp(t, "catppuccin-mocha", cfg)
		sized(t, a, 120, 40)

		return a
	}
	text := func(a *App) string { return ansi.Strip(a.View().Content) }

	t.Run("which-key", func(t *testing.T) {
		t.Parallel()

		a := app(t)
		press(a, keyCtrlA)
		a.Update(whichKeyMsg{seq: a.seq})

		plain := text(a)
		if !strings.Contains(plain, " ctrl+a ") || strings.Contains(plain, "ctrl+space") {
			t.Errorf("title lacks ctrl+a:\n%s", plain)
		}

		var git []hint

		for _, h := range a.hints() {
			if h.group == "Git" {
				git = append(git, h)
			}
		}

		want := []hint{{"g", "Push and pull requests", "Git"}, {"w", "Pull requests", "Git"}}
		if !slices.Equal(git, want) {
			t.Errorf("Git hints = %v, want %v", git, want)
		}
	})

	t.Run("sequence popup", func(t *testing.T) {
		t.Parallel()

		a := app(t)
		press(a, keyCtrlA, keyRune('g'))

		var got []string
		for _, h := range a.sequenceRows("g") {
			got = append(got, h.key+"  "+h.title)
		}

		want := []string{"P  Push", "o  Open a pull request", "m  Merge pull request", "esc  Cancel"}
		if !slices.Equal(got, want) {
			t.Errorf("rows = %q, want %q", got, want)
		}

		if plain := text(a); !strings.Contains(plain, " ctrl+a g ") {
			t.Errorf("popup title lacks ctrl+a g:\n%s", plain)
		}
	})

	t.Run("moved key runs", func(t *testing.T) {
		t.Parallel()

		a := app(t)
		press(a, keyCtrlA, keyRune('w'))

		if a.notice != noticePRsOff {
			t.Errorf("notice = %q, want %q", a.notice, noticePRsOff)
		}
	})

	t.Run("old leader is a plain key", func(t *testing.T) {
		t.Parallel()

		a := app(t)
		press(a, keyLeader)

		if a.pending {
			t.Error("ctrl+space left a leader pending")
		}
	})

	t.Run("unbound key", func(t *testing.T) {
		t.Parallel()

		a := app(t)
		press(a, keyCtrlA, keyRune('Z'))

		if a.notice != "ctrl+a Z is not bound" {
			t.Errorf("notice = %q", a.notice)
		}
	})

	t.Run("freed sequence key", func(t *testing.T) {
		t.Parallel()

		a := app(t)
		press(a, keyCtrlA, keyRune('g'), keyRune('l'))

		if a.notice != "ctrl+a g l is not bound" || a.prList != nil {
			t.Errorf("notice = %q, prList = %v, want g l unbound", a.notice, a.prList)
		}
	})

	t.Run("palette", func(t *testing.T) {
		t.Parallel()

		a := app(t)
		a.openCommandPalette()

		for _, it := range a.overlay.items {
			if it.ID == "pr.list" && it.Hint != "ctrl+a w" {
				t.Errorf("Pull requests hint = %q, want ctrl+a w", it.Hint)
			}
		}
	})

	t.Run("welcome card", func(t *testing.T) {
		t.Parallel()

		if plain := text(app(t)); !strings.Contains(plain, "ctrl+a :") {
			t.Errorf("welcome card lacks ctrl+a ::\n%s", plain)
		}
	})
}

func TestSequenceFromBindings(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		cfg    core.KeysConfig
		prefix string
		want   []string
	}{
		{
			"preselect clear moved", leaderKeys("; x", "pane.preselect.clear", "; esc", "none"), ";",
			[]string{"h j k l  Preselect side", "x  Clear preselection", "esc  Cancel"},
		},
		{
			"one tab key freed", leaderKeys("> 1", "none"), ">",
			[]string{
				"2  Move pane to tab 2", "3  Move pane to tab 3", "4  Move pane to tab 4", "5  Move pane to tab 5",
				"6  Move pane to tab 6", "7  Move pane to tab 7", "8  Move pane to tab 8", "9  Move pane to tab 9", "esc  Cancel",
			},
		},
		{"defaults", core.KeysConfig{}, ">", []string{"1-9  Move pane to tab N", "esc  Cancel"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			a := newKeyedApp(t, "catppuccin-mocha", tt.cfg)

			var got []string
			for _, h := range a.sequenceRows(tt.prefix) {
				got = append(got, h.key+"  "+h.title)
			}

			if !slices.Equal(got, tt.want) {
				t.Errorf("rows = %q, want %q", got, tt.want)
			}
		})
	}

	t.Run("which-key stops collapsing", func(t *testing.T) {
		t.Parallel()

		a := newKeyedApp(t, "catppuccin-mocha", leaderKeys("> 1", "none"))
		for _, h := range a.hints() {
			if h.key == "> 1-9" {
				t.Errorf("hint %v collapses a family with an unbound key", h)
			}
		}
	})

	t.Run("preselect needs a BSP tab", func(t *testing.T) {
		t.Parallel()

		a := newKeyedApp(t, "catppuccin-mocha", leaderKeys("p", "pane.preselect.left", "; h", "none"))
		sized(t, a, 120, 40)

		if !a.keymap.preselectPrefix(";") || a.keymap.preselectPrefix(">") {
			t.Error("preselectPrefix wrong for ; and >")
		}

		if err := a.dispatch(core.SetLayoutMode{Mode: core.ModeMaster, Area: a.bodyArea()}); err != nil {
			t.Fatal(err)
		}

		press(a, keyLeader, keyRune(';'))

		if a.notice != noticePreselectBSP || a.prefix != "" {
			t.Errorf("notice = %q, prefix = %q, want the BSP notice and no prefix", a.notice, a.prefix)
		}
	})
}

func TestWhichKeyDelayConfig(t *testing.T) {
	t.Parallel()

	zero := 0

	if got := newKeyedApp(t, "catppuccin-mocha", core.KeysConfig{WhichKeyDelay: &zero}).keymap.delay; got != 0 {
		t.Errorf("delay = %v, want 0", got)
	}

	if got := newTestApp(t).keymap.delay.Milliseconds(); got != 300 {
		t.Errorf("default delay = %dms, want 300", got)
	}
}

func TestPrefixRowKeepsItsGroup(t *testing.T) {
	t.Parallel()

	a := newKeyedApp(t, "catppuccin-mocha", leaderKeys("g z", "tab.new"))

	for _, h := range a.hints() {
		if h.key == "g" && h.group != "Git" {
			t.Errorf("g row group = %q, want Git", h.group)
		}
	}
}

// drawerKeys builds a [keys] config with the drawer entries in the given order.
func drawerKeys(pairs ...string) core.KeysConfig {
	cfg := core.KeysConfig{Drawer: map[string]string{}, Order: map[string][]string{}}
	for i := 0; i+1 < len(pairs); i += 2 {
		cfg.Drawer[pairs[i]] = pairs[i+1]
		cfg.Order["drawer"] = append(cfg.Order["drawer"], pairs[i])
	}

	return cfg
}

func TestResolveDrawer(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		cfg     core.KeysConfig
		check   func(t *testing.T, tb keyTable)
		wantErr string
	}{
		{
			name: "added key", cfg: drawerKeys("n", "review.line.next"),
			check: func(t *testing.T, tb keyTable) {
				for _, key := range []string{"n", "j"} {
					if got, _ := tb.lookup(key); got != "review.line.next" {
						t.Errorf("lookup(%s) = %q, want review.line.next", key, got)
					}
				}

				if got, _ := tb.first("review.line.next"); got != "n" {
					t.Errorf("first = %q, want n", got)
				}
			},
		},
		{
			name: "dropped key", cfg: drawerKeys("j", "none"),
			check: func(t *testing.T, tb keyTable) {
				if got, ok := tb.lookup("j"); ok {
					t.Errorf("lookup(j) = %q, want it unbound", got)
				}

				if got, _ := tb.lookup("down"); got != "review.line.next" {
					t.Errorf("lookup(down) = %q, want review.line.next", got)
				}

				if got, _ := tb.first("review.line.next"); got != "down" {
					t.Errorf("first = %q, want down", got)
				}
			},
		},
		{
			name: "moved key", cfg: drawerKeys("o", "review.browser", "O", "none"),
			check: func(t *testing.T, tb keyTable) {
				if got, _ := tb.lookup("o"); got != "review.browser" {
					t.Errorf("lookup(o) = %q, want review.browser", got)
				}

				if _, ok := tb.lookup("O"); ok {
					t.Error("O is still bound")
				}
			},
		},
		{
			name: "key taken over", cfg: drawerKeys("f", "review.top"),
			check: func(t *testing.T, tb keyTable) {
				if got, _ := tb.lookup("f"); got != "review.top" {
					t.Errorf("lookup(f) = %q, want review.top", got)
				}

				if _, ok := tb.first("review.full"); ok {
					t.Error("review.full kept a key")
				}
			},
		},
		{
			name: "not a drawer command", cfg: drawerKeys("o", "pr.list"),
			wantErr: `keys.drawer: "o" = "pr.list": not a drawer command`,
		},
		{
			name: "no such command", cfg: drawerKeys("x", "review.x"),
			wantErr: `keys.drawer: "x" = "review.x": no such command`,
		},
		{
			name: "the leader key in the drawer", cfg: drawerKeys("ctrl+space", "review.line.next"),
			wantErr: `keys.drawer: "ctrl+space" is the leader key`,
		},
		{
			name: "a remapped leader key in the drawer",
			cfg: func() core.KeysConfig {
				c := drawerKeys("ctrl+a", "review.line.next")
				c.LeaderKey = "ctrl+a"

				return c
			}(),
			wantErr: `keys.drawer: "ctrl+a" is the leader key`,
		},
		{
			name: "the leader key in the rail",
			cfg: core.KeysConfig{
				Rail:  map[string]string{"ctrl+space": "rail.next"},
				Order: map[string][]string{"rail": {"ctrl+space"}},
			},
			wantErr: `keys.rail: "ctrl+space" is the leader key`,
		},
		{
			name: "the old leader key is free once it is remapped",
			cfg: func() core.KeysConfig {
				c := drawerKeys("ctrl+space", "review.line.next")
				c.LeaderKey = "ctrl+a"

				return c
			}(),
			check: func(t *testing.T, tb keyTable) {
				if got, _ := tb.lookup("ctrl+space"); got != "review.line.next" {
					t.Errorf("lookup(ctrl+space) = %q, want review.line.next", got)
				}
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			_, tables, err := resolveKeys(tt.cfg, testRegistry(t))
			if tt.wantErr != "" {
				if err == nil || err.Error() != tt.wantErr {
					t.Errorf("resolveKeys() = %v, want %q", err, tt.wantErr)
				}

				if cerr := CheckKeys(tt.cfg); cerr == nil || cerr.Error() != tt.wantErr {
					t.Errorf("CheckKeys() = %v, want %q", cerr, tt.wantErr)
				}

				return
			}

			if err != nil {
				t.Fatal(err)
			}

			tt.check(t, tables.drawer)
		})
	}
}

func TestDefaultDrawerKeysUnchanged(t *testing.T) {
	t.Parallel()

	_, tables, err := resolveKeys(core.KeysConfig{}, testRegistry(t))
	if err != nil {
		t.Fatal(err)
	}

	if !slices.Equal(tables.drawer.binds, reviewKeys) {
		t.Error("an empty config changed the drawer's keys")
	}
}

func TestKeyTableHint(t *testing.T) {
	t.Parallel()

	tb := keyTable{binds: []reviewKey{
		{"j", "a"},
		{"k", "b"},
		{"ctrl+d", "cd"},
		{"ctrl+u", "cu"},
		{"alt+j", "aj"},
		{"alt+k", "ak"},
		{"u", "u"},
		{"ctrl+k", "ck"},
		{"ctrl+alt+d", "cad"},
		{"ctrl++", "cplus"},
		{"ctrl+-", "cminus"},
	}}

	tests := []struct {
		name string
		ids  []string
		want string
	}{
		{"two commands", []string{"a", "b"}, "j/k"},
		{"one command", []string{"a"}, "j"},
		{"one unbound", []string{"a", "missing"}, ""},
		{"shared ctrl", []string{"cd", "cu"}, "ctrl+d/u"},
		{"shared alt", []string{"aj", "ak"}, "alt+j/k"},
		{"bare after mods", []string{"cd", "u"}, "ctrl+d, u"},
		{"mods after bare", []string{"a", "ck"}, "j/ctrl+k"},
		{"different mods", []string{"cad", "cu"}, "ctrl+alt+d/ctrl+u"},
		{"plus and minus", []string{"cplus", "cminus"}, "ctrl++/-"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if got := tb.hint(tt.ids...); got != tt.want {
				t.Errorf("hint(%q) = %q, want %q", tt.ids, got, tt.want)
			}
		})
	}
}

func TestPaletteHints(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		cfg  core.KeysConfig
		id   string
		want string
	}{
		{"leader sequence", core.KeysConfig{}, "pr.list", "ctrl+space g l"},
		{"drawer key", core.KeysConfig{}, "review.full", "drawer: f"},
		{"remapped drawer key", drawerKeys("F", "review.full"), "review.full", "drawer: F"},
		{"leader key wins", core.KeysConfig{}, "review.toggle", "ctrl+space d"},
		{"no key", drawerKeys("f", "none"), "review.full", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if got := newKeyedApp(t, "catppuccin-mocha", tt.cfg).paletteHint(tt.id); got != tt.want {
				t.Errorf("paletteHint(%s) = %q, want %q", tt.id, got, tt.want)
			}
		})
	}
}

func TestDrawerRemappedKeys(t *testing.T) {
	t.Parallel()

	r, _ := fakeKeysRig(t, func(o *Options) {
		o.Keys = drawerKeys("n", "review.line.next", "w", "none")
	})
	v := r.opened(t)

	r.keys(keyRune('n'))

	if v.cursor != 1 {
		t.Errorf("cursor after n = %d, want 1", v.cursor)
	}

	r.keys(keyRune('w'))

	if r.listFor(r.store.State().Tab()) != nil {
		t.Error("w opened the worktree list although it is unbound")
	}

	r.keys(keyTab)

	if !r.drawer.tree {
		t.Fatal("tab did not give the tree the keyboard")
	}

	before := v.treeSel

	r.keys(keyRune('n'))

	if v.treeSel == before {
		t.Errorf("n left the tree selection on file %d", before)
	}
}

func TestDrawerHintsFollowBindings(t *testing.T) {
	t.Parallel()

	const (
		diffHints = "j/k line   ]/[ hunk   }/{ file   z zoom   tab tree   w worktree   f full   u split   e edit   / search   space reviewed   R show reviewed   b/t mode   ,/. turn"
		treeHints = "j/k file   enter jump   z zoom   tab hide   esc back"
		prHints   = "j/k line   ]/[ hunk   z zoom   i conversation   c checks   a approve   M merge   O browser   space reviewed   R show reviewed   b/t mode"
	)

	tests := []struct {
		name    string
		cfg     core.KeysConfig
		pr, key bool // PR mode; the tree has the keyboard
		want    string
		not     string
	}{
		{name: "defaults", want: diffHints},
		{name: "defaults in a pull request", pr: true, want: prHints},
		{name: "defaults in the tree", key: true, want: treeHints},
		{name: "line next", cfg: drawerKeys("n", "review.line.next"), want: "n/k line   ]/[ hunk"},
		{
			name: "worktree unbound", cfg: drawerKeys("w", "none"),
			want: "tab tree   f full", not: "worktree",
		},
		{name: "checks moved", cfg: drawerKeys("x", "review.checks", "c", "none"), pr: true, want: "x checks   a approve"},
		{name: "tree moved", cfg: drawerKeys("x", "review.tree"), key: true, want: "j/k file   enter jump   z zoom   x hide   esc back"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			s := reviewScene{
				theme: "catppuccin-mocha", w: 330, h: 52, res: turn4Result(), turn: 4, turns: 4,
				focused: true, tree: tt.key, keys: tt.cfg,
			}
			s.tune = func(t *testing.T, a *App, v *reviewView) {
				a.drawer.tree = tt.key
				if tt.pr {
					prScene(t, a, v)
				}
			}

			got := ansi.Strip(s.build(t).View().Content)
			if !strings.Contains(got, tt.want) {
				t.Errorf("the frame lacks %q:\n%s", tt.want, got)
			}

			if tt.not != "" && strings.Contains(got, tt.not) {
				t.Errorf("the frame holds %q:\n%s", tt.not, got)
			}
		})
	}
}

func TestPRLoadedNoticeFollowsBindings(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		cfg  core.KeysConfig
		then prThen
		want string
	}{
		{"conversation", core.KeysConfig{}, thenConversation, "#7 loaded; press i for the conversation"},
		{"checks", core.KeysConfig{}, thenChecks, "#7 loaded; press c for checks"},
		{"browser", core.KeysConfig{}, thenBrowser, "#7 loaded; press O to open it"},
		{"approve", core.KeysConfig{}, thenApprove, "#7 loaded; press a to approve"},
		{"merge", core.KeysConfig{}, thenMerge, "#7 loaded; press M to merge"},
		{"approve moved", drawerKeys("x", "review.approve", "a", "none"), thenApprove, "#7 loaded; press x to approve"},
		{"merge moved", drawerKeys("m", "review.merge", "M", "none"), thenMerge, "#7 loaded; press m to merge"},
		{"checks unbound", drawerKeys("c", "none"), thenChecks, "#7 loaded"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if got := newKeyedApp(t, "catppuccin-mocha", tt.cfg).prLoadedNotice(7, tt.then); got != tt.want {
				t.Errorf("notice = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestDrawerTextFollowsBindings(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		cfg   core.KeysConfig
		scene func(s *reviewScene)
		want  []string
		not   []string
	}{
		{
			name:  "fold key",
			cfg:   drawerKeys("x", "review.fold", "enter", "none"),
			scene: ghostScene,
			want:  []string{"2 removed lines   x shows them"}, not: []string{"enter shows them"},
		},
		{
			name: "fold unbound", cfg: drawerKeys("enter", "none"), scene: ghostScene,
			want: []string{"2 removed lines"}, not: []string{"shows them"},
		},
		{
			name: "full file key", cfg: drawerKeys("F", "review.full", "f", "none"), scene: truncatedScene,
			want: []string{"too large, press F for the full file"}, not: []string{"press f "},
		},
		{
			name: "full file unbound", cfg: drawerKeys("f", "none"), scene: truncatedScene,
			want: []string{"too large"}, not: []string{"for the full file"},
		},
		{
			name: "no snapshot keys", cfg: drawerKeys("z", "review.mode.branch", "b", "none"), scene: noSnapScene,
			want: []string{", and . step through the turns; z shows the branch"},
		},
		{
			name: "no snapshot, turn keys unbound", cfg: drawerKeys(",", "none", ".", "none"), scene: noSnapScene,
			want: []string{"b shows the branch"}, not: []string{"step through"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			s := reviewScene{
				theme: "catppuccin-mocha", icons: true, w: 200, h: 52, res: turn4Result(), turn: 4, turns: 4,
				focused: true, keys: tt.cfg,
			}
			if tt.scene != nil {
				tt.scene(&s)
			}

			text := ansi.Strip(s.build(t).View().Content)

			for _, w := range tt.want {
				if !strings.Contains(text, w) {
					t.Errorf("the frame lacks %q:\n%s", w, text)
				}
			}

			for _, n := range tt.not {
				if strings.Contains(text, n) {
					t.Errorf("the frame holds %q:\n%s", n, text)
				}
			}
		})
	}
}

func ghostScene(s *reviewScene) {
	s.tune = func(t *testing.T, a *App, v *reviewView) {
		v.full = map[string]fullText{appGoPath: appGoFull()}
		v.fullOn = allWhole(v.full)
		v.folded = map[string]bool{foldKey(appGoPath, 1): true}
		v.rebuild(false)
	}
}

func truncatedScene(s *reviewScene) {
	s.res = edgeResult()
	s.tune = func(t *testing.T, a *App, v *reviewView) {
		i := slices.IndexFunc(v.rows, func(r reviewRow) bool { return r.kind == rowInfo && r.text == infoTruncated })
		if i < 0 {
			t.Fatal("no row holds the truncated note")
		}

		v.cursor = i
	}
}

func noSnapScene(s *reviewScene) { s.res, s.turn, s.noSnap = git.Result{}, 1, true }

func TestPRHeaderFollowsBindings(t *testing.T) {
	t.Parallel()

	moved := func(p *core.PRInfo) { p.HeadOID = "bbbbbbb0000000000000000000000000000000bb" }

	tests := []struct {
		name string
		cfg  core.KeysConfig
		tune func(*core.PRInfo)
		want string
		not  string
	}{
		{"refresh default", core.KeysConfig{}, moved, "since this diff; r fetches it", ""},
		{"refresh moved", drawerKeys("x", "review.refresh", "r", "none"), moved, "since this diff; x fetches it", "r fetches it"},
		{"refresh unbound", drawerKeys("r", "none"), moved, "the PR moved to bbbbbbb since this diff", "fetches it"},
		{"push default", core.KeysConfig{}, nil, "the PR shows the pushed head; ctrl+space g P pushes", ""},
		{"push moved", leaderKeys("g P", "none", "g u", "git.push"), nil, "pushed head; ctrl+space g u pushes", "g P pushes"},
		{"push unbound", leaderKeys("g P", "none"), nil, "↑2 not pushed: the PR shows the pushed head", "pushes"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			s := reviewScene{
				theme: "catppuccin-mocha", w: 200, h: 52, res: turn4Result(), turn: 4, turns: 4, focused: true, branch: true, keys: tt.cfg,
				tune: func(t *testing.T, a *App, v *reviewView) { prSceneWith(tt.tune, 2)(t, a, v) },
			}

			text := strings.Join(prHeaderLines(t, s.build(t)), "\n")
			if !strings.Contains(text, tt.want) {
				t.Errorf("drawer lacks %q:\n%s", tt.want, text)
			}

			if tt.not != "" && strings.Contains(text, tt.not) {
				t.Errorf("drawer holds %q:\n%s", tt.not, text)
			}
		})
	}
}
