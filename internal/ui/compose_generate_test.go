package ui

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/mhersson/aim/internal/core"
	"github.com/mhersson/aim/internal/harness"
)

var keyCtrlG = tea.KeyPressMsg{Code: 'g', Mod: tea.ModCtrl}

// describer is a harness whose Describe answers text and err and records what
// it was asked.
type describer struct {
	harness.Claude

	text string
	err  error

	mu   sync.Mutex
	reqs []harness.DescribeRequest
}

func (d *describer) Describe(_ context.Context, req harness.DescribeRequest) (string, error) {
	d.mu.Lock()
	defer d.mu.Unlock()

	d.reqs = append(d.reqs, req)

	return d.text, d.err
}

func (d *describer) asked() []harness.DescribeRequest {
	d.mu.Lock()
	defer d.mu.Unlock()

	return append([]harness.DescribeRequest(nil), d.reqs...)
}

const generated = "## Why\n\nThe lexer reads tokens."

// newGenerateRig is the compose rig whose agent has a session, its harness
// answering text and err, with the box open.
func newGenerateRig(t *testing.T, text string, err error) (*composeRig, *describer) {
	t.Helper()

	r := newComposeRig(t)
	d := &describer{text: text, err: err}
	r.harness = d

	openIn(t, r.App, core.SetAgentSession{ID: r.agent, Session: "s-agent", Transcript: "/x/t.jsonl"})
	r.mustOpen(t)

	return r, d
}

// typeInBody types s at the start of the body.
func (r *composeRig) typeInBody(t *testing.T, s string) {
	t.Helper()

	r.compose.focus(composeBody)
	r.typed(t, s)
}

func TestComposeGenerateFillsAnUntouchedBody(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		setup func(r *composeRig)
	}{
		{name: "the body it started from", setup: func(*composeRig) {}},
		{name: "an emptied body", setup: func(r *composeRig) { r.compose.body = newTextArea("", maxComposeBody) }},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			r, d := newGenerateRig(t, generated, nil)
			tt.setup(r)

			r.keys(t, keyCtrlG)

			if got := r.compose.body.Value(); got != generated {
				t.Errorf("body = %q, want the generated description", got)
			}

			if r.compose.asking != "" {
				t.Error("the box asks before filling an untouched body")
			}

			reqs := d.asked()
			if len(reqs) != 1 {
				t.Fatalf("Describe ran %d times, want once", len(reqs))
			}

			req := reqs[0]
			if req.Branch != "feat/x" || req.Base != "main" || len(req.Commits) != 2 || !strings.Contains(req.Stat, "files changed") {
				t.Errorf("request = %+v, want feat/x into main with its two commits and the diff summary", req)
			}
		})
	}
}

// The agent's session is forked only when the box is for the worktree the
// agent works in.
func TestComposeGenerateForksTheBranchAgentsSession(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		session     bool
		elsewhere   bool
		wantSession core.SessionID
	}{
		{name: "the agent on the branch", session: true, wantSession: "s-agent"},
		{name: "an agent without a session"},
		{name: "a box for another worktree", session: true, elsewhere: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			r := newComposeRig(t)
			d := &describer{text: generated}
			r.harness = d

			if tt.session {
				openIn(t, r.App, core.SetAgentSession{ID: r.agent, Session: "s-agent", Transcript: "/x/t.jsonl"})
			}

			if tt.elsewhere {
				b := r.worktree(t, "b", "feat/b")
				r.churn(t, r.openReview(r.agent, true))
				openIn(t, r.App, core.ShowReviewPR{Repo: composeRepo, Number: 7, Root: b})
			}

			v := r.mustOpen(t)
			r.keys(t, keyCtrlG)

			reqs := d.asked()
			if len(reqs) != 1 {
				t.Fatalf("Describe ran %d times, want once", len(reqs))
			}

			want := v.t.root
			if tt.wantSession != "" {
				want = r.store.State().Panes[r.agent].Cwd
			}

			if got := reqs[0]; got.Session != tt.wantSession || got.Dir != want {
				t.Errorf("session %q in %q, want %q in %q", got.Session, got.Dir, tt.wantSession, want)
			}
		})
	}
}

func TestComposeGenerateAsksBeforeReplacingTypedText(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		answer     tea.KeyPressMsg
		wantBody   func(typed string) string
		wantAsking bool
	}{
		{name: "enter replaces the body", answer: keyEnter, wantBody: func(string) string { return generated }},
		{name: "esc keeps the body", answer: keyEsc, wantBody: func(typed string) string { return typed }},
		{name: "another key answers nothing", answer: keyRune('y'), wantBody: func(typed string) string { return typed }, wantAsking: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			r, _ := newGenerateRig(t, generated, nil)
			r.typeInBody(t, "mine ")
			typed := r.compose.body.Value()

			r.keys(t, keyCtrlG)

			if r.compose.body.Value() != typed {
				t.Fatal("the generated description replaced typed text without asking")
			}

			if status, isErr := r.compose.statusRow(); status != statusComposeGenerateAsk || isErr {
				t.Errorf("status = %q (error %v), want the confirm", status, isErr)
			}

			if text := r.text(); !strings.Contains(text, "enter replace") || !strings.Contains(text, "esc keep") {
				t.Errorf("the hints lack the confirm's keys:\n%s", text)
			}

			r.keys(t, tt.answer)

			if r.compose == nil {
				t.Fatal("answering the confirm closed the box")
			}

			if got, want := r.compose.body.Value(), tt.wantBody(typed); got != want {
				t.Errorf("body = %q, want %q", got, want)
			}

			if asking := r.compose.asking != ""; asking != tt.wantAsking {
				t.Errorf("asking = %v, want %v", asking, tt.wantAsking)
			}
		})
	}
}

func TestComposeGenerateFailureKeepsTheBody(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		text       string
		err        error
		wantReason string
	}{
		{name: "a failed run", err: errors.New("claude failed (exit status 1): No conversation found"), wantReason: "No conversation found"},
		{name: "a run out of time", err: context.DeadlineExceeded, wantReason: "timed out"},
		{name: "an empty answer", text: " \n", wantReason: reasonGenerateEmpty},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			r, _ := newGenerateRig(t, tt.text, tt.err)
			before := r.compose.body.Value()

			r.keys(t, keyCtrlG)

			if got := r.compose.body.Value(); got != before {
				t.Errorf("body = %q, want it kept as %q", got, before)
			}

			if status, isErr := r.compose.statusRow(); !isErr || !strings.Contains(status, tt.wantReason) {
				t.Errorf("status = %q (error %v), want an error with %q", status, isErr, tt.wantReason)
			}
		})
	}
}

// An answer that arrives while the body is out in the editor asks, since the
// edit comes back over the body.
func TestComposeGenerateAsksWhileTheEditorHasTheBody(t *testing.T) {
	t.Parallel()

	r, _ := newGenerateRig(t, generated, nil)
	before := r.compose.body.Value()

	cmd := press(r.App, keyCtrlG)
	if cmd == nil {
		t.Fatal("ctrl+g started no run")
	}

	r.compose.editing = "p9"
	r.churn(t, cmd)

	if r.compose.body.Value() != before || r.compose.asking != generated {
		t.Errorf("body = %q, asking = %q: want the body kept and the description waiting", r.compose.body.Value(), r.compose.asking)
	}
}

// An answer for a box that closed, was opened again, or is creating its pull
// request is dropped; closing or creating cancels the run.
func TestComposeGenerateDropsALateAnswer(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		after func(t *testing.T, r *composeRig)
	}{
		{name: "a closed box", after: func(t *testing.T, r *composeRig) { t.Helper(); r.keys(t, keyEsc) }},
		{name: "a box opened again", after: func(t *testing.T, r *composeRig) { t.Helper(); r.keys(t, keyEsc); r.mustOpen(t) }},
		{name: "a box creating its pull request", after: func(t *testing.T, r *composeRig) { t.Helper(); press(r.App, keyCtrlS) }},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			r, d := newGenerateRig(t, generated, nil)

			cmd := press(r.App, keyCtrlG)
			if cmd == nil {
				t.Fatal("ctrl+g started no run")
			}

			v, seq := r.compose, r.compose.genSeq

			tt.after(t, r)

			if gen, ok := cmd().(composeGeneratedMsg); !ok || !errors.Is(gen.err, context.Canceled) {
				t.Errorf("the run answered %+v, want it cancelled", gen)
			}

			if len(d.asked()) != 0 {
				t.Error("the cancelled run still asked the harness")
			}

			// An answer that was on its way before the stop.
			r.churn(t, func() tea.Msg { return composeGeneratedMsg{v: v, seq: seq, text: generated} })

			if r.compose != nil && (r.compose.body.Value() == generated || r.compose.asking != "") {
				t.Error("the late answer reached the box")
			}
		})
	}
}

// While the run is out the box stays editable, says it is generating, and a
// second ctrl+g starts nothing.
func TestComposeGenerateLeavesTheBoxEditable(t *testing.T) {
	t.Parallel()

	r, d := newGenerateRig(t, generated, nil)
	first := press(r.App, keyCtrlG)

	if status, isErr := r.compose.statusRow(); status != statusComposeGenerating || isErr {
		t.Errorf("status = %q (error %v), want %q", status, isErr, statusComposeGenerating)
	}

	r.compose.title.SetValue("")
	r.typed(t, "Lexer")

	if got := r.compose.title.Value(); got != "Lexer" {
		t.Errorf("title = %q, want the typed one", got)
	}

	if status, _ := r.compose.statusRow(); status != statusComposeGenerating {
		t.Errorf("status after typing = %q, want %q", status, statusComposeGenerating)
	}

	if second := press(r.App, keyCtrlG); second != nil {
		r.churn(t, second)
	}

	r.churn(t, first)

	if n := len(d.asked()); n != 1 {
		t.Errorf("Describe ran %d times, want once", n)
	}
}
