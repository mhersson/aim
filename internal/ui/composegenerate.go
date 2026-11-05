package ui

import (
	"cmp"
	"context"
	"errors"
	"strings"

	tea "charm.land/bubbletea/v2"

	"github.com/mhersson/aim/internal/core"
	"github.com/mhersson/aim/internal/harness"
)

// composeGenerate is ctrl+g: off the update loop it reads the commits and the
// diff summary and asks the pane's harness for a body. When the box is for the
// worktree the pane's agent follows, the run forks the agent's session.
func (a *App) composeGenerate(v *composeView) tea.Cmd {
	if v.generate != nil {
		return nil
	}

	m := a.store.State().Panes[v.t.pane]
	h := a.harnessOf(m)
	req := harness.DescribeRequest{Dir: v.t.root, Branch: v.t.branch, Base: v.t.bases[v.base], Template: v.template}

	if m.Kind == core.KindAgent && m.Session != "" && m.FollowDir() == v.t.dir {
		req.Session, req.Dir = m.Session, cmp.Or(m.Cwd, v.t.root)
	}

	ctx, cancel := context.WithTimeout(a.ctx, describeTimeout)
	v.generate = cancel
	v.genSeq++

	var (
		seq  = v.genSeq
		g    = a.git
		root = v.t.root
		base = v.t.remote + "/" + req.Base
	)

	return func() tea.Msg {
		defer cancel()

		text, err := describeBranch(ctx, g, h, root, base, req)

		return composeGeneratedMsg{v: v, seq: seq, text: text, err: err}
	}
}

// describeBranch puts the commits and the diff summary since base in req and
// asks h for the body. A base whose remote-tracking ref is gone has neither.
func describeBranch(ctx context.Context, g Git, h harness.Harness, root, base string, req harness.DescribeRequest) (string, error) {
	r, err := g.Open(ctx, root)
	if err != nil {
		return "", err
	}

	cs, err := g.Log(ctx, r, base)
	if err != nil && !errors.Is(err, errNoBaseRef) {
		return "", err
	}

	for _, c := range cs {
		req.Commits = append(req.Commits, harness.Commit{Subject: c.Subject, Body: c.Body})
	}

	if req.Stat, err = g.DiffStat(ctx, r, base); err != nil && !errors.Is(err, errNoBaseRef) {
		return "", err
	}

	// The run costs tokens, so a box closed during the reads never starts it.
	if err := ctx.Err(); err != nil {
		return "", err
	}

	return h.Describe(ctx, req)
}

// stopGenerating cancels the description being generated; its answer is then
// dropped.
func (v *composeView) stopGenerating() {
	if v.generate != nil {
		v.generate()
		v.generate = nil
		v.genSeq++
	}
}

// onComposeGenerated puts the description in a body the user has not changed
// and asks before replacing one they have, or one out in the editor. An
// answer for a closed box or a stopped run is dropped.
func (a *App) onComposeGenerated(msg composeGeneratedMsg) tea.Cmd {
	v := msg.v
	if a.compose != v || msg.seq != v.genSeq {
		return nil
	}

	v.generate = nil
	text := strings.TrimSpace(msg.text)

	switch {
	case errors.Is(msg.err, context.Canceled):
		return nil
	case errors.Is(msg.err, context.DeadlineExceeded):
		v.status, v.statusErr = statusComposeGenerateErr+reasonGenerateTimeout, true

		return nil
	case msg.err != nil:
		v.status, v.statusErr = statusComposeGenerateErr+gitReason(msg.err), true

		return nil
	case text == "":
		v.status, v.statusErr = statusComposeGenerateErr+reasonGenerateEmpty, true

		return nil
	}

	if v.editing == "" && v.untouched() {
		return a.fillBody(v, text)
	}

	v.asking = text

	return nil
}

// untouched reports whether the body is empty or still the text the box put in.
func (v *composeView) untouched() bool {
	body := v.body.Value()

	return strings.TrimSpace(body) == "" || body == v.filled
}

// fillBody puts a generated description in the body.
func (a *App) fillBody(v *composeView, text string) tea.Cmd {
	cmd := a.setBody(v, text)
	v.filled = v.body.Value()

	return cmd
}

// answerGenerated answers the confirm: enter puts the generated description
// in the body, esc keeps the body, and every other key is dropped.
func (a *App) answerGenerated(v *composeView, key string) tea.Cmd {
	switch key {
	case "enter":
		text := v.asking
		v.asking = ""

		return a.fillBody(v, text)
	case "esc":
		v.asking = ""
	}

	return nil
}

// statusRow is the box's status row and whether it is an error: the confirm
// while it waits, else the status, else the note that a description is being
// generated.
func (v *composeView) statusRow() (string, bool) {
	switch {
	case v.asking != "":
		return statusComposeGenerateAsk, false
	case v.status == "" && v.generate != nil:
		return statusComposeGenerating, false
	}

	return v.status, v.statusErr
}
