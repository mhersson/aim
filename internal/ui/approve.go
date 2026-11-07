package ui

import (
	"context"
	"errors"
	"fmt"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/mhersson/aim/internal/github"
)

// approveTimeout bounds one approval.
const approveTimeout = 30 * time.Second

// maxApproveWidth caps the approve and ready confirms.
const maxApproveWidth = 66

// The approve confirm's notices.
const (
	noticeApproveOwn    = "you can't approve your own pull request"
	noticeApproveClosed = "#%d is %s"
)

// approveTarget is what an approval sends, frozen when its confirm opens: the
// pull request and the commit whose diff the drawer showed, or the one the pull
// request list showed when listed is set.
type approveTarget struct {
	repo       github.Repo
	number     int
	title, sha string
	listed     bool // opened from the pull request list, not from the drawer
}

// approvedMsg answers an approval.
type approvedMsg struct {
	target approveTarget
	err    error
}

// reviewApprove is `a`: it approves the drawer's pull request at the head its
// diff showed, after showing the pull request first when it is not shown.
func (a *App) reviewApprove() tea.Cmd { return a.prAct(thenApprove) }

// openApprove opens the approve confirm on the pull request the drawer has
// loaded, or says why it cannot.
func (a *App) openApprove(repoKey string, number int, sha string) {
	if a.modalOpen() {
		return
	}

	a.openApproveConfirm(repoKey, number, "", sha, false)
}

// openApproveConfirm opens the approve confirm on pull request number of
// repoKey at sha, or says why it cannot. The title is the poll's, else title. It
// checks no modal: the drawer's route does, and the list's opens over the list.
func (a *App) openApproveConfirm(repoKey string, number int, title, sha string, listed bool) {
	repo, ok := repoOfKey(repoKey)
	if !ok || a.actions == nil {
		a.notice = noticePRsOff

		return
	}

	t := approveTarget{repo: repo, number: number, title: title, sha: sha, listed: listed}

	if info, ok := prInfoByNumber(a.store.State(), repoKey, number); ok {
		switch {
		case info.State == "MERGED":
			a.notice = fmt.Sprintf(noticeApproveClosed, number, "merged")

			return
		case info.State == "CLOSED":
			a.notice = fmt.Sprintf(noticeApproveClosed, number, "closed")

			return
		case info.ViewerAuthored:
			a.notice = noticeApproveOwn

			return
		}

		t.title = info.Title
	}

	tab := a.store.State().Tab().ID

	a.openConfirm(confirm{tab: tab, approve: &t, yes: func(a *App) tea.Cmd { return a.approveRun(t) }})
}

// approveRun approves exactly t off the update loop, under a timeout derived
// from the app's context.
func (a *App) approveRun(t approveTarget) tea.Cmd {
	var (
		actions = a.actions
		appCtx  = a.ctx
	)

	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(appCtx, approveTimeout)
		defer cancel()

		return approvedMsg{target: t, err: actions.Approve(ctx, t.repo, t.number, t.sha)}
	}
}

// onApproved says what an approval did and asks the poller for the repository.
// An approval cut short by the app closing says nothing.
func (a *App) onApproved(msg approvedMsg) {
	t := msg.target

	switch {
	case errors.Is(msg.err, context.Canceled):
		return
	case errors.Is(msg.err, context.DeadlineExceeded):
		a.toast(levelError, fmt.Sprintf("approve #%d: timed out", t.number))

		return
	case msg.err != nil:
		a.toast(levelError, fmt.Sprintf("approve #%d: %s", t.number, gitReason(msg.err)))

		return
	}

	a.toast(levelSuccess, fmt.Sprintf("approved #%d", t.number))

	if a.prs.src != nil {
		a.prs.src.Refresh(t.repo)
	}
}

// approveBox draws the approve confirm w cells wide: the pull request's title,
// the commit it approves and the two answers. Text from GitHub is drawn without
// control and invisible characters.
func approveBox(t approveTarget, w int, icons bool, st styles) []string {
	hint := func(key, label string) string { return st.popupKey.Render(key) + st.popupMuted.Render(" "+label) }

	title := " Approve " + prNumberText(t.number) + " "
	if icons {
		title = " " + iconApproved + " Approve " + prNumberText(t.number) + " "
	}

	top, row, bottom := boxFrame(w, title, boxStyle{border: st.popupBorder, title: st.approveTitle, cut: "…"}, st)

	which := ", the commit you reviewed"
	if t.listed {
		which = ", the commit listed"
	}

	return []string{
		top,
		row(""),
		row(st.confirmQuestion.Render(printable(t.title))),
		row(st.popupMuted.Render("at head ") + st.confirmQuestion.Render(t.sha[:min(shortOIDLen, len(t.sha))]) +
			st.popupMuted.Render(which)),
		row(""),
		row(hint("enter", "approve") + st.popup.Render("    ") + hint("esc", "cancel")),
		bottom,
	}
}
