package ui

import (
	"fmt"
	"slices"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/mhersson/aim/internal/core"
)

// The checks overlay's notices.
const (
	noticeChecksLoading = "loading checks…"
	noticeChecksNone    = "no checks on %s"
	noticeChecksUnread  = "could not read the checks: %s"
	noticeCheckNoPage   = "no page for that check"
)

// checksView is the open checks overlay: the pull request whose checks it lists,
// as a repository keyed "host/owner/name" and a number, and the selected row.
type checksView struct {
	repo   string
	number int
	sel    int
	page   *checkPage // a job's log shown in place of the list; nil for the list
}

// checkKind is how a check is told apart and ordered.
type checkKind uint8

// The kinds in the order they are listed.
const (
	checkFailed checkKind = iota
	checkRunning
	checkPassed
	checkSkipped
)

// checkRow is one listed check.
type checkRow struct {
	kind     checkKind
	name     string
	url      string
	duration string
	id       int64  // a run's job id; 0 for a commit status, which has no log
	app      string // the app that made the run
}

// checkKindOf maps a check's state; one not known counts as running.
func checkKindOf(state string) checkKind {
	switch state {
	case "failure":
		return checkFailed
	case "success":
		return checkPassed
	case "skipped":
		return checkSkipped
	}

	return checkRunning
}

// checkRows lists the checks: failed, running, passed, then skipped, each group
// in the order the checks came.
func checkRows(c *core.PRChecks, now time.Time) []checkRow {
	if c == nil {
		return nil
	}

	rows := make([]checkRow, len(c.Checks))
	for i, ch := range c.Checks {
		rows[i] = checkRow{
			kind: checkKindOf(ch.State), name: ch.Name, url: ch.URL, duration: checkDuration(ch, now),
			id: ch.ID, app: ch.App,
		}
	}

	slices.SortStableFunc(rows, func(x, y checkRow) int { return int(x.kind) - int(y.kind) })

	return rows
}

// checkDuration is how long a check took, or for a running one how long it has
// run; empty when its times are not known.
func checkDuration(c core.CheckInfo, now time.Time) string {
	if c.StartedAt.IsZero() {
		return ""
	}

	if checkKindOf(c.State) == checkRunning {
		return "running " + spanText(now.Sub(c.StartedAt))
	}

	if c.CompletedAt.IsZero() {
		return ""
	}

	return spanText(c.CompletedAt.Sub(c.StartedAt))
}

// spanText writes d as 12s, 3m 04s or 1h 02m.
func spanText(d time.Duration) string {
	s := int(max(d, 0) / time.Second)

	switch {
	case s < 60:
		return fmt.Sprintf("%ds", s)
	case s < 3600:
		return fmt.Sprintf("%dm %02ds", s/60, s%60)
	}

	return fmt.Sprintf("%dh %02dm", s/3600, s%3600/60)
}

// openChecks shows the checks of pull request number of repo.
func (a *App) openChecks(repo string, number int) tea.Cmd {
	a.endLeader()
	a.checks = &checksView{repo: repo, number: number}
	a.prs.dirty = true

	return nil
}

// closeChecks closes the overlay and lets the poller go back to the drawer's pull request.
func (a *App) closeChecks() {
	a.checks = nil
	a.prs.dirty = true
}

// checksOf is the focused checks the overlay shows: those of its pull request at
// the head the poll lists; nil until they arrive.
func (a *App) checksOf(st core.State, v *checksView) *core.PRChecks {
	f := st.PRs[v.repo].Focused
	if f == nil || f.Number != v.number {
		return nil
	}

	if info, ok := prInfoByNumber(st, v.repo, v.number); ok && info.HeadOID != "" && info.HeadOID != f.HeadOID {
		return nil
	}

	return f
}

// handleChecksKey answers the open checks overlay; any other key is swallowed.
func (a *App) handleChecksKey(msg tea.KeyPressMsg) tea.Cmd {
	if p := a.checks.page; p != nil && p.chooser != nil {
		return a.summaryChooserKey(p, bindingKey(msg))
	}

	return a.runKey(a.keys.checks, bindingKey(msg))
}
