// Package harness adapts external coding-agent CLIs to AIM.
package harness

import (
	"context"
	"time"

	"github.com/mhersson/aim/internal/core"
)

// Harness adapts one agent CLI: how to launch it, how to hook it, and how to
// read what its hooks report.
type Harness interface {
	// ID is saved with each agent pane, e.g. "claude-code".
	ID() string
	// Program is the foreground process name of a running agent, e.g. "claude".
	Program() string
	// Launch returns the agent argv. An empty settings omits the hooks file; an
	// empty resume starts a fresh session.
	Launch(settings string, resume core.SessionID) []string
	// Settings returns the hooks file whose handlers run `exe hook <ID()>`.
	Settings(exe string) ([]byte, error)
	// ParseEvent reads one hook payload as received on stdin.
	ParseEvent(payload []byte) (Event, error)
	// CustomName reads the name the user gave the session from its transcript
	// file; empty when it has none. The name is untrusted text. It does file IO.
	CustomName(ctx context.Context, transcript string) (string, error)
}

// RateWindow is one usage window as the agent reports it: Used is the percent
// as sent and ResetsAt the reset time in Unix seconds.
type RateWindow struct {
	Used     float64 `json:"used"`
	ResetsAt int64   `json:"resets_at"`
}

// Event is one hook or statusline payload reduced to what the app needs.
type Event struct {
	Pane       core.PaneID     `json:"pane"`
	Name       string          `json:"name"` // the harness's hook event name, or "statusline", for logs
	Session    core.SessionID  `json:"session,omitempty"`
	Transcript string          `json:"transcript,omitempty"`
	State      core.AgentState `json:"state,omitempty"` // AgentUnknown leaves the state as it is
	Start      bool            `json:"start,omitempty"` // Session is now the pane's current session
	End        bool            `json:"end,omitempty"`   // the session ended
	Cwd        string          `json:"cwd,omitempty"`   // the agent's working directory

	// NewConversation marks a session start that begins a new conversation, a
	// /clear or a fresh start, rather than going on with one.
	NewConversation bool `json:"new_conversation,omitempty"`

	// Action is the agent's current action, short and printable. ActionSet true
	// replaces the pane's action with it (empty clears it); false leaves it.
	Action    string `json:"action,omitempty"`
	ActionSet bool   `json:"action_set,omitempty"`

	// Edit is the cleaned absolute path of the file an edit tool is about to
	// change or has changed; empty for every other event.
	Edit string `json:"edit,omitempty"`
	// Plan is the cleaned absolute path of the plan file an ExitPlanMode
	// PreToolUse names; empty for every other event. The text is not carried.
	Plan string `json:"plan,omitempty"`
	// Before marks a PreToolUse whose Edit names a file its tool has not
	// changed yet; the server holds its answer like a turn start's.
	Before bool `json:"before,omitempty"`
	// Subagent marks an event a subagent sent, not the agent's own session.
	Subagent bool `json:"subagent,omitempty"`
	// Changed holds the cleaned absolute paths of the files a finished Bash
	// command changed, as Claude Code lists them: one per directory, at most 16
	// and 32 KiB once encoded.
	Changed []string `json:"changed,omitempty"`

	// Status marks a status update from the agent's statusline, not a hook. It
	// carries Session and the fields below, and leaves state, action and cwd alone.
	Status bool `json:"status,omitempty"`
	// Model is the model's display name, printable and cut like an action; empty
	// when the agent did not report one.
	Model string `json:"model,omitempty"`
	// Context is the percentage of the context window in use, as the agent
	// reports it; it means something only when ContextKnown is true.
	Context      float64 `json:"context,omitempty"`
	ContextKnown bool    `json:"context_known,omitempty"`
	// Window is the context window size in tokens and InputTokens the input
	// tokens used so far; 0 when not reported.
	Window      int `json:"window,omitempty"`
	InputTokens int `json:"input_tokens,omitempty"`
	// FiveHour and SevenDay are the account's usage windows from the status
	// line's rate limits; nil when the payload did not carry that window.
	FiveHour *RateWindow `json:"five_hour,omitempty"`
	SevenDay *RateWindow `json:"seven_day,omitempty"`

	// TurnStart marks a prompt submission: the agent starts working once the
	// hook is answered, so the server holds the answer until Done (as it does
	// for Before).
	TurnStart bool `json:"turn_start,omitempty"`
	// Done releases the held answer. The server sets it on a held event; it is
	// safe to call more than once and concurrently. Nil when nothing is held.
	Done func() `json:"-"`
	// Deadline is when the turn snapshot must be done, stamped by the server
	// as it reads a held event (a turn start or an edit's before-event); zero
	// when unset.
	Deadline time.Time `json:"-"`
	// Snapped is closed when the turn's snapshot (or its marker) is finished,
	// whether or not it succeeded. Nil when no snapshot is taken for the event.
	Snapped <-chan struct{} `json:"-"`
}

// ByID returns the harness saved under id.
func ByID(id string) (Harness, bool) {
	if id == (Claude{}).ID() {
		return Claude{}, true
	}

	return nil, false
}
