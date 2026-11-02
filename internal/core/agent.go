package core

import (
	"fmt"
	"slices"
	"time"
)

// SessionID names a harness session, e.g. a Claude Code session UUID.
type SessionID string

// AgentState is what an agent pane is doing, as its harness reports it.
type AgentState int

// Agent states, in increasing urgency; AgentUnknown shows no badge.
const (
	AgentUnknown AgentState = iota
	AgentIdle
	AgentWorking
	AgentBlocked
)

func (s AgentState) String() string {
	switch s {
	case AgentIdle:
		return "idle"
	case AgentWorking:
		return "working"
	case AgentBlocked:
		return "blocked"
	}

	return ""
}

// SetPaneKind changes what a pane is. An agent names its harness; becoming a
// shell clears Harness, Session, Transcript, State, Action, AgentCwd, the
// reported status and the edited places.
type SetPaneKind struct {
	ID      PaneID
	Kind    PaneKind
	Harness string
}

func (a SetPaneKind) apply(s *State) error {
	p, ok := s.Panes[a.ID]
	if !ok {
		return fmt.Errorf("core: no pane %q", a.ID)
	}

	switch a.Kind {
	case KindShell:
		p = Pane{Kind: KindShell, Cwd: p.Cwd}
	case KindAgent:
		if a.Harness == "" {
			return fmt.Errorf("core: SetPaneKind agent for pane %q without a harness", a.ID)
		}

		p.Kind, p.Harness = KindAgent, a.Harness
	default:
		return fmt.Errorf("core: SetPaneKind with unknown kind %q", a.Kind)
	}

	s.Panes[a.ID] = p

	return nil
}

// agentPane returns the agent pane for id, or an error for an unknown or shell pane.
func (s *State) agentPane(id PaneID) (Pane, error) {
	p, ok := s.Panes[id]
	if !ok {
		return Pane{}, fmt.Errorf("core: no pane %q", id)
	}

	if p.Kind != KindAgent {
		return Pane{}, fmt.Errorf("core: pane %q is not an agent", id)
	}

	return p, nil
}

// SetAgentSession records the session an agent reported and its transcript
// file. An empty Session clears both. An end or a change of session also clears
// the edited places, and the reported status unless that status already belongs
// to the new session: a status line can report before its SessionStart hook.
type SetAgentSession struct {
	ID         PaneID
	Session    SessionID
	Transcript string
	// NewConversation drops the plan: the session does not go on with the old
	// conversation.
	NewConversation bool
}

func (a SetAgentSession) apply(s *State) error {
	p, err := s.agentPane(a.ID)
	if err != nil {
		return err
	}

	if a.Session == "" || (a.Session != p.Session && a.Session != p.StatusSession) {
		p.clearStatus()
	}

	if a.Session == "" || a.Session != p.Session {
		p.Edits = nil
	}

	if a.NewConversation {
		p.Plan = Plan{}
	}

	p.Session, p.Transcript = a.Session, a.Transcript
	if a.Session == "" {
		p.Transcript = ""
	}

	s.Panes[a.ID] = p

	return nil
}

// SetAgentStatus records the model and context an agent's status line
// reported for Session. A false ContextKnown clears the meter and keeps the
// model; Context is floored and clamped to 0-100.
type SetAgentStatus struct {
	ID           PaneID
	Session      SessionID
	Model        string
	Context      float64
	ContextKnown bool
}

func (a SetAgentStatus) apply(s *State) error {
	p, err := s.agentPane(a.ID)
	if err != nil {
		return err
	}

	p.Model, p.StatusSession = a.Model, a.Session
	p.Context, p.ContextKnown = a.percent(), a.ContextKnown
	s.Panes[a.ID] = p

	return nil
}

// Matches reports whether p already holds the status a would record.
func (a SetAgentStatus) Matches(p Pane) bool {
	return p.Model == a.Model && p.StatusSession == a.Session &&
		p.ContextKnown == a.ContextKnown && p.Context == a.percent()
}

// percent is the floored context clamped to 0-100, 0 when none was reported.
func (a SetAgentStatus) percent() int {
	if !a.ContextKnown {
		return 0
	}

	return FloorPercent(a.Context)
}

// SetAgentState records what an agent is doing; AgentUnknown clears the badge.
type SetAgentState struct {
	ID    PaneID
	State AgentState
}

func (a SetAgentState) apply(s *State) error {
	if a.State < AgentUnknown || a.State > AgentBlocked {
		return fmt.Errorf("core: SetAgentState with unknown state %d", int(a.State))
	}

	p, err := s.agentPane(a.ID)
	if err != nil {
		return err
	}

	p.State = a.State
	s.Panes[a.ID] = p

	return nil
}

// SetAgentAction records what an agent is doing right now; empty clears it.
type SetAgentAction struct {
	ID     PaneID
	Action string
}

func (a SetAgentAction) apply(s *State) error {
	p, err := s.agentPane(a.ID)
	if err != nil {
		return err
	}

	p.Action = a.Action
	s.Panes[a.ID] = p

	return nil
}

// SetAgentCwd records the directory an agent reported in a hook; empty clears it.
type SetAgentCwd struct {
	ID  PaneID
	Cwd string
}

func (a SetAgentCwd) apply(s *State) error {
	p, err := s.agentPane(a.ID)
	if err != nil {
		return err
	}

	p.AgentCwd = a.Cwd
	s.Panes[a.ID] = p

	return nil
}

// NoteEdit records a worktree Session's agent edited. It changes nothing, and
// is no error, for a pane that is closed, a shell, or in another session. An
// entry for the same Root keeps the higher Seq and the later At; past MaxEdits
// the entry with the lowest Seq goes.
type NoteEdit struct {
	ID      PaneID
	Session SessionID
	Edit    Edit
}

func (a NoteEdit) apply(s *State) error {
	p, ok := s.Panes[a.ID]
	if !ok || p.Kind != KindAgent || p.Session != a.Session {
		return nil
	}

	// A fresh slice: State.clone shares the old one with earlier states.
	edits := slices.Clone(p.Edits)

	if i := slices.IndexFunc(edits, func(e Edit) bool { return e.Root == a.Edit.Root }); i >= 0 {
		kept := edits[i]
		if a.Edit.Seq > kept.Seq {
			kept = a.Edit
		}

		kept.At = laterOf(edits[i].At, a.Edit.At)
		edits[i] = kept
	} else {
		edits = append(edits, a.Edit)
	}

	if len(edits) > MaxEdits {
		lowest := 0

		for i, e := range edits {
			if e.Seq < edits[lowest].Seq {
				lowest = i
			}
		}

		edits = slices.Delete(edits, lowest, lowest+1)
	}

	p.Edits = edits
	s.Panes[a.ID] = p

	return nil
}

// ForgetEdit drops the entry for Root from an agent's edited places; it
// changes nothing when the pane has none.
type ForgetEdit struct {
	ID   PaneID
	Root string
}

func (a ForgetEdit) apply(s *State) error {
	p, ok := s.Panes[a.ID]
	if !ok {
		return nil
	}

	i := slices.IndexFunc(p.Edits, func(e Edit) bool { return e.Root == a.Root })
	if i < 0 {
		return nil
	}

	p.Edits = slices.Delete(slices.Clone(p.Edits), i, i+1)
	s.Panes[a.ID] = p

	return nil
}

func laterOf(a, b time.Time) time.Time {
	if b.After(a) {
		return b
	}

	return a
}

// TabAgentState is the most urgent state among tab i's agent panes: blocked,
// working, idle, else AgentUnknown.
func (s State) TabAgentState(i int) AgentState {
	if i < 0 || i >= len(s.Tabs) {
		return AgentUnknown
	}

	best := AgentUnknown
	for _, id := range s.tabLeaves(i) {
		if p := s.Panes[id]; p.Kind == KindAgent && p.State > best {
			best = p.State
		}
	}

	return best
}

// AgentPanes lists the agent panes by tab, then layout order.
func (s State) AgentPanes() []PaneID {
	var out []PaneID

	for i := range s.Tabs {
		for _, id := range s.tabLeaves(i) {
			if s.Panes[id].Kind == KindAgent {
				out = append(out, id)
			}
		}
	}

	return out
}

// tabLeaves lists tab i's panes in layout order. The copy keeps callers from
// editing the tab.
func (s State) tabLeaves(i int) []PaneID {
	return slices.Clone(s.Tabs[i].Panes)
}
