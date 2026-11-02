package ui

import (
	"context"
	"errors"
	"log/slog"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/mhersson/aim/internal/core"
	"github.com/mhersson/aim/internal/git"
)

// editState is what the hooks' edit handling keeps between events.
type editState struct {
	seq     uint64                                           // counts the edit hooks in the order they reach the update loop
	resolve func(context.Context, string) (git.Place, error) // the shared PlaceCache's Place; tests replace it
}

// editPlaceMsg answers noteEdit: the place of path, or why it has none. The
// pane's session is the one when the hook arrived, so an answer that comes after
// a session change records nothing.
type editPlaceMsg struct {
	pane    core.PaneID
	session core.SessionID
	path    string
	seq     uint64
	at      time.Time
	place   git.Place
	err     error
}

// noteEdit records the worktree the file an agent just edited belongs to. The
// order is the one the hooks arrive in, whenever git answers. A path resolves
// off the update loop, under the probe limit.
func (a *App) noteEdit(m core.Pane, id core.PaneID, path string) tea.Cmd {
	a.edits.seq++

	msg := editPlaceMsg{pane: id, session: m.Session, path: path, seq: a.edits.seq, at: a.clock.Now()}

	ctx, sem, budget, resolve := a.ctx, a.probeSem, a.probeBudget, a.edits.resolve

	return func() tea.Msg {
		ctx, done, err := admit(ctx, sem, budget)
		if err != nil {
			msg.err = err

			return msg
		}
		defer done()

		msg.place, msg.err = resolve(ctx, path)

		return msg
	}
}

// editPlacesMsg answers noteChanged: the places of the files a Bash command
// changed that resolved, each with the number of the path that found it.
type editPlacesMsg struct {
	pane    core.PaneID
	session core.SessionID
	at      time.Time
	places  []git.Place
	seqs    []uint64
}

// noteChanged records the worktrees the files a Bash command changed belong to.
// Each path gets its number now, in the list's order, and the list resolves off
// the update loop under one probe slot, so a command costs one slot.
func (a *App) noteChanged(m core.Pane, id core.PaneID, paths []string) tea.Cmd {
	seqs := make([]uint64, len(paths))
	for i := range seqs {
		a.edits.seq++
		seqs[i] = a.edits.seq
	}

	at := a.clock.Now()
	ctx, sem, budget, resolve, log := a.ctx, a.probeSem, a.probeBudget, a.edits.resolve, a.log

	return func() tea.Msg {
		msg := editPlacesMsg{pane: id, session: m.Session, at: at}

		ctx, done, err := admit(ctx, sem, budget)
		if err != nil {
			logEditError(log, id, err)

			return msg
		}
		defer done()

		for i, path := range paths {
			place, err := resolve(ctx, path)
			if errors.Is(err, git.ErrNotCounted) {
				continue
			}

			if err != nil {
				logEditError(log, id, err)

				break
			}

			msg.places, msg.seqs = append(msg.places, place), append(msg.seqs, seqs[i])
		}

		return msg
	}
}

// logEditError logs why an edit's path has no place. Not counted is the normal
// case, and its error names the edited path: not logged.
func logEditError(log *slog.Logger, pane core.PaneID, err error) {
	switch {
	case errors.Is(err, git.ErrNotCounted):
	case errors.Is(err, context.Canceled):
		log.Debug("resolve edit place", "pane", pane, "err", err)
	default:
		log.Warn("resolve edit place", "pane", pane, "err", err)
	}
}

// onEditPlace hands a resolved place to the core. An edit that
// does not count, or fails to resolve, changes nothing and raises no notice.
func (a *App) onEditPlace(msg editPlaceMsg) {
	if msg.err != nil {
		logEditError(a.log, msg.pane, msg.err)

		return
	}

	if m, ok := a.store.State().Panes[msg.pane]; !ok || m.Kind != core.KindAgent {
		return
	}

	a.recordEdit(msg.pane, msg.session, msg.place, msg.seq, msg.at)
}

// onEditPlaces hands the places a Bash command's files resolved to the core,
// the last one the list reaches being the followed one.
func (a *App) onEditPlaces(msg editPlacesMsg) {
	if m, ok := a.store.State().Panes[msg.pane]; !ok || m.Kind != core.KindAgent {
		return
	}

	for i, place := range msg.places {
		a.recordEdit(msg.pane, msg.session, place, msg.seqs[i], msg.at)
	}
}

// recordEdit dispatches an edit of place; the core drops it when the session changed.
func (a *App) recordEdit(pane core.PaneID, session core.SessionID, place git.Place, seq uint64, at time.Time) {
	// dispatch logs its own failures.
	_ = a.dispatch(core.NoteEdit{ID: pane, Session: session, Edit: core.Edit{
		Root: place.Root, Common: place.Common, Repo: place.Repo, Seq: seq, At: at,
	}})
}

// forgetEdit forgets the followed place of a pane when it is root, which is gone.
// It reports whether the pane followed root.
func (a *App) forgetEdit(pane core.PaneID, root string) bool {
	if e, ok := a.store.State().Panes[pane].Followed(); !ok || e.Root != root {
		return false
	}

	// dispatch logs its own failures.
	_ = a.dispatch(core.ForgetEdit{ID: pane, Root: root})

	return true
}
