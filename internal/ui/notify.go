package ui

import (
	"strconv"
	"strings"

	tea "charm.land/bubbletea/v2"

	"github.com/mhersson/aim/internal/core"
	"github.com/mhersson/aim/internal/harness"
)

const (
	protoOSC9   = "osc9"
	protoOSC99  = "osc99"
	protoOSC777 = "osc777"

	// silentB64 is base64("silent"): kitty's sound-off value for OSC 99's s key.
	silentB64 = "c2lsZW50"
)

// notifier sends desktop notifications to the outer terminal.
type notifier struct {
	cfg   core.NotificationsConfig
	proto string
	tmux  bool
	n     int // notices sent; numbers the ids
}

// newNotifier reads the environment once: the escape the terminal takes (the
// protocol setting overrides detection) and whether tmux sits between.
func newNotifier(cfg core.NotificationsConfig, getenv func(string) string) notifier {
	n := notifier{cfg: cfg, proto: detectNotifyProtocol(getenv), tmux: getenv("TMUX") != ""}
	if p := cfg.Proto(); p != "auto" {
		n.proto = p
	}

	return n
}

// detectNotifyProtocol picks the escape from the environment. TERM and
// TERM_PROGRAM name the terminal AIM runs in directly; the ids a terminal
// leaves in the environment survive into tmux, where those two are tmux's own.
func detectNotifyProtocol(getenv func(string) string) string {
	term := getenv("TERM")

	switch {
	case term == "xterm-kitty":
		return protoOSC99
	case term == "xterm-ghostty", term == "foot", strings.HasPrefix(term, "foot-"), strings.HasPrefix(term, "rxvt-unicode"):
		return protoOSC777
	}

	switch getenv("TERM_PROGRAM") {
	case "iTerm.app":
		return protoOSC9
	case "ghostty", "WezTerm":
		return protoOSC777
	}

	switch {
	case getenv("KITTY_WINDOW_ID") != "":
		return protoOSC99
	case getenv("GHOSTTY_RESOURCES_DIR") != "", getenv("WEZTERM_PANE") != "":
		return protoOSC777
	}

	// OSC 9 is the escape most terminals read, so it is the fallback.
	return protoOSC9
}

// notice is one notification: the agent's name and what it says after it.
type notice struct {
	proto, id  string
	name, text string
	silent     bool // ask for no sound (OSC 99 only)
	bell       bool
	tmux       bool
}

// notifySeq builds the escape sequence for n. The name is cleaned here, so
// nothing in it can end or break the sequence.
func notifySeq(n notice) string {
	const esc, st = "\x1b", "\x1b\\"

	body := printable(n.name) + n.text

	var s string

	switch n.proto {
	case protoOSC99:
		meta := ""
		if n.silent {
			meta = ":s=" + silentB64
		}

		s = esc + "]99;i=" + n.id + ":d=0" + meta + ";AIM" + st +
			esc + "]99;i=" + n.id + ":p=body" + meta + ";" + body + st
	case protoOSC777:
		s = esc + "]777;notify;AIM;" + body + st
	default:
		s = esc + "]9;AIM: " + body + st
	}

	if n.tmux {
		s = esc + "Ptmux;" + strings.ReplaceAll(s, esc, esc+esc) + st
	}

	if n.bell {
		s += "\x07"
	}

	return s
}

// notifyHook is the notice for the state change a hook just made, or nil: an
// agent that enters blocked, or whose turn ended while it worked, and that the
// user cannot see.
func (a *App) notifyHook(ev harness.Event, prev core.AgentState) tea.Cmd {
	st := a.store.State()
	now := st.Panes[ev.Pane].State

	var text string

	switch {
	case now == core.AgentBlocked && prev != core.AgentBlocked:
		text = " needs you"
	case ev.TurnEnd && prev == core.AgentWorking && now == core.AgentIdle && a.notify.cfg.Mode() == "all":
		text = " is done"
	default:
		return nil
	}

	if a.notify.cfg.Mode() == "off" || a.paneSeen(st, ev.Pane) {
		return nil
	}

	a.notify.n++

	seq := notifySeq(notice{
		proto: a.notify.proto, id: "aim-" + strconv.Itoa(a.notify.n),
		name: a.cardName(st, ev.Pane), text: text,
		silent: !a.notify.cfg.Bell, bell: a.notify.cfg.Bell, tmux: a.notify.tmux,
	})

	return tea.Raw(seq)
}

// paneSeen reports whether the user can see the pane: the window has focus and
// the pane is drawn. An overlay over a drawn pane does not hide it.
func (a *App) paneSeen(st core.State, id core.PaneID) bool {
	if !a.outerFocused {
		return false
	}

	if i, ok := st.TabOf(id); !ok || i != st.Active {
		return false
	}

	if _, ok := a.arrangeIn(st, st.Active).Rects[id]; !ok {
		return false
	}

	if st.Tab().Review != nil {
		if v := a.drawer.views[st.Tab().ID]; v != nil && v.zoom {
			return false
		}
	}

	return true
}
