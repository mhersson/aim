package ui

import (
	"context"
	"errors"
	"fmt"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/mhersson/aim/internal/core"
	"github.com/mhersson/aim/internal/github"
)

// readyTimeout bounds one mark ready.
const readyTimeout = 30 * time.Second

// The ready confirm's notices.
const (
	noticeReadyNotDraft = "#%d is not a draft"
	noticeReadyClosed   = "#%d is %s"
	noticeReadyUnknown  = "#%d: its draft state is unknown"
)

// readyTarget is the pull request a mark ready sends, frozen when its confirm
// opens; id is its node id.
type readyTarget struct {
	repo      github.Repo
	number    int
	id, title string
}

// readiedMsg answers a mark ready.
type readiedMsg struct {
	target readyTarget
	err    error
}

// reviewReady is `R`: it marks the drawer's pull request ready for review, the
// shown one, else the one of the drawer's branch. It needs no diff, so it loads
// none.
func (a *App) reviewReady() tea.Cmd {
	tab, v, ok := a.reviewActive()
	if !ok {
		return nil
	}

	a.drawerTake()

	if v.pr != (core.ReviewPR{}) {
		info, known := prInfoByNumber(a.store.State(), v.pr.Repo, v.pr.Number)
		a.openReady(v.pr.Repo, v.pr.Number, info, known)

		return nil
	}

	return a.showPR(tab.ID, thenReady)
}

// readyAtPlace opens the ready confirm on the pull request of the drawer's
// branch once its place has answered. A late answer never takes the keyboard
// from where it went since the key.
func (a *App) readyAtPlace(msg prPlaceMsg) {
	if a.modalOpen() || !a.drawerHasKeys() {
		a.notice = a.prLoadedNotice(msg.number, thenReady)

		return
	}

	repo := msg.place.repo.String()
	info, known := prInfoByNumber(a.store.State(), repo, msg.number)
	a.openReady(repo, msg.number, info, known)
}

// openReady opens the ready confirm on pull request number of repoKey, as
// info has it, or says why it cannot.
func (a *App) openReady(repoKey string, number int, info core.PRInfo, known bool) {
	// Not modalOpen: the PR list is a modal, and this confirm opens over it.
	if a.confirm != nil {
		return
	}

	repo, ok := repoOfKey(repoKey)
	if !ok || a.actions == nil {
		a.notice = noticePRsOff

		return
	}

	switch {
	case !known:
		a.notice = fmt.Sprintf(noticeReadyUnknown, number)

		return
	case info.State == "MERGED":
		a.notice = fmt.Sprintf(noticeReadyClosed, number, "merged")

		return
	case info.State == "CLOSED":
		a.notice = fmt.Sprintf(noticeReadyClosed, number, "closed")

		return
	case !info.Draft:
		a.notice = fmt.Sprintf(noticeReadyNotDraft, number)

		return
	}

	t := readyTarget{repo: repo, number: number, id: info.ID, title: info.Title}
	tab := a.store.State().Tab().ID

	a.openConfirm(confirm{tab: tab, ready: &t, yes: func(a *App) tea.Cmd { return a.readyRun(t) }})
}

// readyRun marks exactly t ready off the update loop, under a timeout derived
// from the app's context.
func (a *App) readyRun(t readyTarget) tea.Cmd {
	var (
		actions = a.actions
		appCtx  = a.ctx
	)

	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(appCtx, readyTimeout)
		defer cancel()

		return readiedMsg{target: t, err: actions.MarkReady(ctx, t.repo, t.id)}
	}
}

// onReadied says what a mark ready did and asks the poller for the repository.
// One cut short by the app closing says nothing.
func (a *App) onReadied(msg readiedMsg) {
	t := msg.target

	switch {
	case errors.Is(msg.err, context.Canceled):
		return
	case errors.Is(msg.err, context.DeadlineExceeded):
		a.toast(levelError, fmt.Sprintf("mark #%d ready: timed out", t.number))

		return
	case msg.err != nil:
		a.toast(levelError, fmt.Sprintf("mark #%d ready: %s", t.number, gitReason(msg.err)))

		return
	}

	a.toast(levelSuccess, fmt.Sprintf("marked #%d ready", t.number))

	if a.prs.src != nil {
		a.prs.src.Refresh(t.repo)
	}
}

// readyBox draws the ready confirm w cells wide: the pull request's title, what
// marking it ready does and the two answers. Text from GitHub is drawn without
// control and invisible characters.
func readyBox(t readyTarget, w int, icons bool, st styles) []string {
	hint := func(key, label string) string { return st.popupKey.Render(key) + st.popupMuted.Render(" "+label) }

	title := " Mark " + prNumberText(t.number) + " ready "
	if icons {
		title = " " + iconPR + " Mark " + prNumberText(t.number) + " ready "
	}

	top, row, bottom := boxFrame(w, title, boxStyle{border: st.popupBorder, title: st.approveTitle, cut: "…"}, st)

	return []string{
		top,
		row(""),
		row(st.confirmQuestion.Render(printable(t.title))),
		row(st.popupMuted.Render("ready for review: GitHub notifies its reviewers")),
		row(""),
		row(hint("enter", "mark ready") + st.popup.Render("    ") + hint("esc", "cancel")),
		bottom,
	}
}
