package ui

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/mhersson/aim/internal/core"
	"github.com/mhersson/aim/internal/harness"
)

func envOf(m map[string]string) func(string) string {
	return func(k string) string { return m[k] }
}

func TestDetectNotifyProtocol(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		env  map[string]string
		want string
	}{
		{"kitty term", map[string]string{"TERM": "xterm-kitty"}, "osc99"},
		{"foot term", map[string]string{"TERM": "foot-direct"}, "osc777"},
		{"iterm program", map[string]string{"TERM": "xterm-256color", "TERM_PROGRAM": "iTerm.app"}, "osc9"},
		{"wezterm program", map[string]string{"TERM": "xterm-256color", "TERM_PROGRAM": "WezTerm"}, "osc777"},
		{"term wins over leftover ids", map[string]string{"TERM": "foot", "KITTY_WINDOW_ID": "1"}, "osc777"},
		{"kitty id inside tmux", map[string]string{"TERM": "tmux-256color", "TERM_PROGRAM": "tmux", "KITTY_WINDOW_ID": "3"}, "osc99"},
		{"ghostty id inside tmux", map[string]string{"TERM": "screen", "GHOSTTY_RESOURCES_DIR": "/x"}, "osc777"},
		{"unknown terminal", map[string]string{"TERM": "xterm-256color"}, "osc9"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if got := detectNotifyProtocol(envOf(tt.env)); got != tt.want {
				t.Errorf("protocol = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestNotifySequence(t *testing.T) {
	t.Parallel()

	const esc, st = "\x1b", "\x1b\\"

	tests := []struct {
		name string
		n    notice
		want string
	}{
		{
			"osc99",
			notice{proto: "osc99", id: "aim-1", name: "api", text: " needs you"},
			esc + "]99;i=aim-1:d=0;AIM" + st + esc + "]99;i=aim-1:p=body;api needs you" + st,
		},
		{
			"osc99 silent",
			notice{proto: "osc99", id: "aim-2", name: "api", text: " is done", silent: true},
			esc + "]99;i=aim-2:d=0:s=c2lsZW50;AIM" + st + esc + "]99;i=aim-2:p=body:s=c2lsZW50;api is done" + st,
		},
		{
			"osc777",
			notice{proto: "osc777", name: "api", text: " needs you"},
			esc + "]777;notify;AIM;api needs you" + st,
		},
		{
			"osc9 with bell",
			notice{proto: "osc9", name: "api", text: " is done", bell: true},
			esc + "]9;AIM: api is done" + st + "\x07",
		},
		{
			"tmux wraps with doubled escapes and the bell stays outside",
			notice{proto: "osc777", name: "api", text: " is done", bell: true, tmux: true},
			esc + "Ptmux;" + esc + esc + "]777;notify;AIM;api is done" + esc + st + esc + "\\" + "\x07",
		},
		{
			"a name cannot end the sequence",
			notice{proto: "osc777", name: "a\x1b\\\u202eb", text: " needs you"},
			esc + "]777;notify;AIM;a\\b needs you" + st,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if got := notifySeq(tt.n); got != tt.want {
				t.Errorf("seq = %q, want %q", got, tt.want)
			}
		})
	}
}

// notifyApp is agentApp with a notifications config and an environment.
func notifyApp(t *testing.T, cfg core.NotificationsConfig, env map[string]string) (*App, *fakeClock) {
	t.Helper()

	a, _, clk := kindApp(t, func(o *Options) {
		o.Notifications = cfg
		o.Getenv = func(k string) string {
			if v, ok := env[k]; ok {
				return v
			}

			return testGetenv(k)
		}
	})
	chord(a, 'n')
	a.tick = (&tickLog{}).tick

	if kindOf(a, "p2") != core.KindAgent {
		t.Fatal("p2 is not an agent")
	}

	return a, clk
}

// raws is the escape sequences a command sends.
func raws(cmd tea.Cmd) []string {
	var out []string

	for _, m := range flatten(cmd) {
		if r, ok := m.(tea.RawMsg); ok {
			s, _ := r.Msg.(string)
			out = append(out, s)
		}
	}

	return out
}

var osc777Env = map[string]string{"TERM": "foot"}

func blockedEv() harness.Event {
	return harness.Event{Pane: "p2", Name: "PermissionRequest", State: core.AgentBlocked}
}

func workingEv() harness.Event {
	return harness.Event{Pane: "p2", Name: "PostToolUse", State: core.AgentWorking}
}

func stopEv() harness.Event {
	return harness.Event{Pane: "p2", Name: "Stop", State: core.AgentIdle, TurnEnd: true}
}

func notice777(a *App, text string) string {
	return "\x1b]777;notify;AIM;" + a.cardName(a.store.State(), "p2") + text + "\x1b\\"
}

func TestNotifyBlockedOnlyWhenUnseen(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		setup func(*testing.T, *App)
		want  int
	}{
		{"visible and focused", func(*testing.T, *App) {}, 0},
		{"window unfocused", func(_ *testing.T, a *App) { a.Update(tea.BlurMsg{}) }, 1},
		{"on another tab", func(t *testing.T, a *App) {
			openIn(t, a, core.NewTab{Kind: core.KindShell, Cwd: "/w/b"})
		}, 1},
		{"behind a zoomed pane", func(t *testing.T, a *App) {
			openIn(t, a, core.FocusPane{ID: "p1"}, core.ToggleZoom{})
		}, 1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			a, _ := notifyApp(t, core.NotificationsConfig{}, osc777Env)
			tt.setup(t, a)

			got := raws(hook(a, blockedEv()))
			if len(got) != tt.want {
				t.Fatalf("sent %q, want %d notices", got, tt.want)
			}

			if tt.want == 1 && got[0] != notice777(a, " needs you") {
				t.Errorf("sent %q, want %q", got[0], notice777(a, " needs you"))
			}
		})
	}
}

func TestNotifyBlockedOncePerChange(t *testing.T) {
	t.Parallel()

	a, _ := notifyApp(t, core.NotificationsConfig{}, osc777Env)
	a.Update(tea.BlurMsg{})

	if got := raws(hook(a, blockedEv())); len(got) != 1 {
		t.Fatalf("first blocked sent %q", got)
	}

	if got := raws(hook(a, blockedEv())); len(got) != 0 {
		t.Errorf("a repeated blocked sent %q", got)
	}

	hook(a, workingEv())

	if got := raws(hook(a, blockedEv())); len(got) != 1 {
		t.Errorf("blocked again after working sent %q, want one", got)
	}
}

func TestNotifyDone(t *testing.T) {
	t.Parallel()

	a, clk := notifyApp(t, core.NotificationsConfig{}, osc777Env)
	a.Update(tea.BlurMsg{})
	hook(a, workingEv())

	got := raws(hook(a, stopEv()))
	if len(got) != 1 || got[0] != notice777(a, " is done") {
		t.Fatalf("stop sent %q, want %q", got, notice777(a, " is done"))
	}

	// The quiet rule idles a silent working pane; that is no finished turn.
	hook(a, workingEv())

	clk.Set(t0.Add(agentQuiet + spinEvery))

	_, cmd := a.Update(agentTickMsg{})
	if got := raws(cmd); len(got) != 0 || agentState(a, "p2") != core.AgentIdle {
		t.Errorf("quiet rule sent %q in state %v", got, agentState(a, "p2"))
	}

	// A stop that finds the pane already idle ends no turn.
	if got := raws(hook(a, stopEv())); len(got) != 0 {
		t.Errorf("stop of an idle pane sent %q", got)
	}
}

func TestNotifyMode(t *testing.T) {
	t.Parallel()

	tests := []struct {
		notify        string
		bell          bool
		blocked, done int
	}{
		{"all", false, 1, 1},
		{"blocked", false, 1, 0},
		{"off", true, 0, 0},
	}
	for _, tt := range tests {
		t.Run(tt.notify, func(t *testing.T) {
			t.Parallel()

			a, _ := notifyApp(t, core.NotificationsConfig{Notify: tt.notify, Bell: tt.bell}, osc777Env)
			a.Update(tea.BlurMsg{})

			if got := raws(hook(a, blockedEv())); len(got) != tt.blocked {
				t.Errorf("blocked sent %q, want %d", got, tt.blocked)
			}

			hook(a, workingEv())

			if got := raws(hook(a, stopEv())); len(got) != tt.done {
				t.Errorf("done sent %q, want %d", got, tt.done)
			}
		})
	}
}

func TestNotifyProtocolSettings(t *testing.T) {
	t.Parallel()

	const esc, st = "\x1b", "\x1b\\"

	kitty := map[string]string{"TERM": "xterm-kitty"}

	t.Run("kitty asks to be silent without the bell and ids are unique", func(t *testing.T) {
		t.Parallel()

		a, _ := notifyApp(t, core.NotificationsConfig{}, kitty)
		a.Update(tea.BlurMsg{})

		name := a.cardName(a.store.State(), "p2")
		want := esc + "]99;i=aim-1:d=0:s=c2lsZW50;AIM" + st + esc + "]99;i=aim-1:p=body:s=c2lsZW50;" + name + " needs you" + st

		if got := raws(hook(a, blockedEv())); len(got) != 1 || got[0] != want {
			t.Fatalf("sent %q, want %q", got, want)
		}

		hook(a, workingEv())

		if got := raws(hook(a, blockedEv())); len(got) != 1 || !strings.Contains(got[0], "i=aim-2:") {
			t.Errorf("second notice sent %q, want id aim-2", got)
		}
	})

	t.Run("the bell follows the notice and the override wins", func(t *testing.T) {
		t.Parallel()

		a, _ := notifyApp(t, core.NotificationsConfig{Bell: true, Protocol: "osc9"}, kitty)
		a.Update(tea.BlurMsg{})

		want := esc + "]9;AIM: " + a.cardName(a.store.State(), "p2") + " needs you" + st + "\x07"
		if got := raws(hook(a, blockedEv())); len(got) != 1 || got[0] != want {
			t.Errorf("sent %q, want %q", got, want)
		}
	})

	t.Run("inside tmux the notice is wrapped", func(t *testing.T) {
		t.Parallel()

		a, _ := notifyApp(t, core.NotificationsConfig{}, map[string]string{"TERM": "screen", "TMUX": "/tmp/t,1,0", "KITTY_WINDOW_ID": "1"})
		a.Update(tea.BlurMsg{})

		got := raws(hook(a, blockedEv()))
		if len(got) != 1 || !strings.HasPrefix(got[0], esc+"Ptmux;"+esc+esc+"]99;") || !strings.HasSuffix(got[0], esc+"\\") {
			t.Errorf("sent %q, want a tmux passthrough of an OSC 99 notice", got)
		}
	})
}
