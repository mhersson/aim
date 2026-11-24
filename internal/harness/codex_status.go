package harness

import (
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"

	"github.com/mhersson/aim/internal/core"
)

var codexThreadIDRE = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)

// codexStatusPayload holds the only fields read from a Codex status snapshot.
// Every usage field is optional, so the percentage is a pointer.
type codexStatusPayload struct {
	Thread struct {
		ID    string  `json:"id"`
		Name  *string `json:"name"`
		Agent struct {
			State string `json:"state"`
		} `json:"agent"`
		Model struct {
			DisplayName string `json:"display_name"`
		} `json:"model"`
		Usage struct {
			Context struct {
				Size int      `json:"context_window_size"`
				Used int      `json:"used_tokens"`
				Pct  *float64 `json:"used_percentage"`
			} `json:"context"`
		} `json:"usage"`
	} `json:"thread"`
}

// ParseStatus reads one status snapshot Codex sends to its status line
// provider. Rate limits are not read.
func (Codex) ParseStatus(snapshot []byte) (Event, error) {
	var p codexStatusPayload
	if err := json.Unmarshal(snapshot, &p); err != nil {
		return Event{}, fmt.Errorf("harness: parse codex status snapshot: %w", err)
	}

	t := p.Thread
	if !codexThreadIDRE.MatchString(t.ID) {
		return Event{}, errors.New("harness: codex status snapshot has no valid thread id")
	}

	ev := Event{
		Harness:     "codex",
		Name:        "statusline",
		Status:      true,
		Session:     core.SessionID(t.ID),
		Model:       clip(t.Model.DisplayName),
		Window:      t.Usage.Context.Size,
		InputTokens: t.Usage.Context.Used,
	}
	if t.Name != nil {
		ev.ThreadName = strings.TrimSpace(capRunes(printable(*t.Name), maxNameRunes))
	}

	switch t.Agent.State {
	case "starting", "ready", "working", "waiting", "thinking":
		ev.RunState = t.Agent.State
	}

	if t.Usage.Context.Pct != nil {
		ev.Context, ev.ContextKnown = *t.Usage.Context.Pct, true
	}

	return ev, nil
}
