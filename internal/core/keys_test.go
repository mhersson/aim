package core

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

func loadKeys(t *testing.T, content string) (Config, error) {
	t.Helper()

	path := filepath.Join(t.TempDir(), "config.toml")
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}

	return LoadConfig(path)
}

func TestLoadKeys(t *testing.T) {
	t.Parallel()

	type bind = KeyBind

	tests := []struct {
		name    string
		content string
		leader  string
		delay   time.Duration
		binds   map[string][]bind
		errHas  []string
	}{
		{name: "no keys section", leader: "ctrl+space", delay: 300 * time.Millisecond},
		{
			name: "scalars", content: "[keys]\nleader_key = \"ctrl+a\"\nwhich_key_delay = 0\n",
			leader: "ctrl+a", delay: 0,
		},
		{
			name:    "leader table keeps file order",
			content: "[keys.leader]\n\"w\" = \"pr.list\"\n\"g l\" = \"none\"\n\"a\" = \"rail.toggle\"\n",
			leader:  "ctrl+space", delay: 300 * time.Millisecond,
			binds: map[string][]bind{"leader": {{"w", "pr.list"}, {"g l", "none"}, {"a", "rail.toggle"}}},
		},
		{
			name:    "dotted key under keys",
			content: "[keys]\nleader.\"w\" = \"pr.list\"\nleader.\"g l\" = \"none\"\nleader.a = \"rail.toggle\"\n",
			leader:  "ctrl+space", delay: 300 * time.Millisecond,
			binds: map[string][]bind{"leader": {{"w", "pr.list"}, {"g l", "none"}, {"a", "rail.toggle"}}},
		},
		{
			name:    "inline table keeps file order",
			content: "[keys]\ndrawer = { o = \"review.browser\", O = \"none\", a = \"review.x\" }\n",
			leader:  "ctrl+space", delay: 300 * time.Millisecond,
			binds: map[string][]bind{"drawer": {{"o", "review.browser"}, {"O", "none"}, {"a", "review.x"}}},
		},
		{
			name:    "reader table keeps file order",
			content: "[keys.reader]\nq = \"reader.close\"\nesc = \"none\"\n",
			leader:  "ctrl+space", delay: 300 * time.Millisecond,
			binds: map[string][]bind{"reader": {{"q", "reader.close"}, {"esc", "none"}}},
		},
		{name: "unknown context", content: "[keys.drawr]\no = \"x\"\n", errHas: []string{"keys.drawr"}},
		{name: "context is a string", content: "[keys]\nleader = \"ctrl+a\"\n", errHas: []string{"keys.leader"}},
		{name: "command is a number", content: "[keys.leader]\nw = 3\n", errHas: []string{"keys.leader.w"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			cfg, err := loadKeys(t, tt.content)
			if len(tt.errHas) > 0 {
				if err == nil {
					t.Fatalf("LoadConfig = nil error, want one with %q", tt.errHas)
				}

				for _, s := range tt.errHas {
					if !strings.Contains(err.Error(), s) {
						t.Errorf("error %q lacks %q", err, s)
					}
				}

				return
			}

			if err != nil {
				t.Fatal(err)
			}

			if got := cfg.Keys.LeaderName(); got != tt.leader {
				t.Errorf("LeaderName = %q, want %q", got, tt.leader)
			}

			if got := cfg.Keys.Delay(); got != tt.delay {
				t.Errorf("Delay = %v, want %v", got, tt.delay)
			}

			for _, ctx := range KeyContexts() {
				want := tt.binds[ctx]
				if got := cfg.Keys.Binds(ctx); !reflect.DeepEqual(got, want) && (len(got) > 0 || len(want) > 0) {
					t.Errorf("Binds(%q) = %v, want %v", ctx, got, want)
				}
			}
		})
	}
}

func TestKeysBindsHandBuilt(t *testing.T) {
	t.Parallel()

	c := KeysConfig{
		Leader: map[string]string{"z": "a", "b": "c", "m": "d"},
		Order:  map[string][]string{"leader": {"m", "gone"}},
	}

	want := []KeyBind{{"m", "d"}, {"b", "c"}, {"z", "a"}}
	if got := c.Binds("leader"); !reflect.DeepEqual(got, want) {
		t.Errorf("Binds = %v, want %v", got, want)
	}
}

func TestKeysConfigValidate(t *testing.T) {
	t.Parallel()

	ptrInt := func(n int) *int { return &n }

	tests := []struct {
		name string
		cfg  KeysConfig
		want []string // empty means valid; the field and the offending key
	}{
		{name: "defaults"},
		{name: "ctrl leader", cfg: KeysConfig{LeaderKey: "ctrl+a"}},
		{name: "alt space leader", cfg: KeysConfig{LeaderKey: "alt+space"}},
		{name: "function key leader", cfg: KeysConfig{LeaderKey: "f12"}},
		{name: "plain leader", cfg: KeysConfig{LeaderKey: "a"}, want: []string{"keys.leader_key", `"a"`}},
		{name: "shifted leader", cfg: KeysConfig{LeaderKey: "shift+a"}, want: []string{"keys.leader_key", `"shift+a"`}},
		{name: "shifted digit", cfg: KeysConfig{Drawer: map[string]string{"shift+1": "review.x"}}, want: []string{"keys.drawer", `"shift+1"`}},
		{name: "dangling modifier", cfg: KeysConfig{LeaderKey: "ctrl+"}, want: []string{"keys.leader_key", `"ctrl+"`}},
		{name: "modifiers out of order", cfg: KeysConfig{LeaderKey: "alt+ctrl+a"}, want: []string{"keys.leader_key", `"alt+ctrl+a"`}},
		{name: "delay below", cfg: KeysConfig{WhichKeyDelay: ptrInt(-1)}, want: []string{"keys.which_key_delay", "-1"}},
		{name: "delay above", cfg: KeysConfig{WhichKeyDelay: ptrInt(5001)}, want: []string{"keys.which_key_delay", "5001"}},
		{name: "delay top", cfg: KeysConfig{WhichKeyDelay: ptrInt(5000)}},
		{
			name: "shift on a character", cfg: KeysConfig{Drawer: map[string]string{"shift+h": "review.x"}},
			want: []string{"keys.drawer", `"shift+h"`},
		},
		{name: "sequence in drawer", cfg: KeysConfig{Drawer: map[string]string{"g g": "review.x"}}, want: []string{"keys.drawer", `"g g"`}},
		{name: "three keys in leader", cfg: KeysConfig{Leader: map[string]string{"g l x": "pr.list"}}, want: []string{"keys.leader", `"g l x"`}},
		{name: "empty command", cfg: KeysConfig{Rail: map[string]string{"j": ""}}, want: []string{"keys.rail", `"j"`}},
		{name: "unknown name", cfg: KeysConfig{Checks: map[string]string{"ctrl+nope": "none"}}, want: []string{"keys.checks", `"ctrl+nope"`}},
		{name: "unknown name in images", cfg: KeysConfig{Images: map[string]string{"ctrl+nope": "none"}}, want: []string{"keys.images", `"ctrl+nope"`}},
		{name: "bad key in sequence", cfg: KeysConfig{Leader: map[string]string{"g nope": "none"}}, want: []string{"keys.leader", `"g nope"`}},
		{name: "double space in sequence", cfg: KeysConfig{Leader: map[string]string{"g  l": "none"}}, want: []string{"keys.leader", `"g  l"`}},
		{name: "ctrl shift left", cfg: KeysConfig{Drawer: map[string]string{"ctrl+shift+left": "review.x"}}},
		{name: "bracket", cfg: KeysConfig{PRList: map[string]string{"]": "prlist.x"}}},
		{name: "capital", cfg: KeysConfig{Drawer: map[string]string{"H": "review.x"}}},
		{name: "space name", cfg: KeysConfig{Drawer: map[string]string{"space": "review.x"}}},
		{name: "plus key", cfg: KeysConfig{Drawer: map[string]string{"ctrl++": "review.x"}}},
		{name: "semicolon esc", cfg: KeysConfig{Leader: map[string]string{"; esc": "none"}}},
		{name: "capital in sequence", cfg: KeysConfig{Leader: map[string]string{"g P": "git.push"}}},
		{name: "none command", cfg: KeysConfig{Leader: map[string]string{"g l": KeyNone}}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			err := tt.cfg.Validate()
			if len(tt.want) == 0 {
				if err != nil {
					t.Errorf("Validate = %v, want nil", err)
				}

				return
			}

			if err == nil {
				t.Fatalf("Validate = nil, want an error naming %q", tt.want)
			}

			for _, s := range tt.want {
				if !strings.Contains(err.Error(), s) {
					t.Errorf("error %q lacks %q", err, s)
				}
			}
		})
	}
}
