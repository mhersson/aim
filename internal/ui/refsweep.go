package ui

import (
	"context"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/mhersson/aim/internal/git"
)

// refSweepTimeout bounds the listing of refs/aim, and the two deletes together;
// each delete is also bounded by the ref-write budget.
const refSweepTimeout = 10 * time.Second

// refsListedMsg is the answer of listing a repository's refs/aim.
type refsListedMsg struct {
	repo git.Repo
	refs StaleRefs
	err  error
}

// refSweep starts the once-per-run sweep of r's refs/aim by listing them off
// the loop. It is nil when sweeping is off or the repository was already swept.
func (a *App) refSweep(r git.Repo) tea.Cmd {
	if !a.sweepRefs || a.swept[r.CommonDir] {
		return nil
	}

	if a.swept == nil {
		a.swept = map[string]bool{}
	}

	a.swept[r.CommonDir] = true

	ctx := a.ctx

	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(ctx, refSweepTimeout)
		defer cancel()

		refs, err := a.git.ListAimRefs(ctx, r)

		return refsListedMsg{repo: r, refs: refs, err: err}
	}
}

// onRefsListed filters the listing against the state as it is now, so a session
// or pull request taken since the listing stays, and deletes the rest off the
// loop. Its result is only logged.
func (a *App) onRefsListed(msg refsListedMsg) tea.Cmd {
	if msg.err != nil {
		a.log.Warn("sweep refs", "repo", msg.repo.CommonDir, "err", msg.err)

		return nil
	}

	st := a.store.State()
	sessions := map[string]bool{}

	for _, p := range st.Panes {
		if p.Session != "" {
			sessions[strings.ToLower(string(p.Session))] = true
		}
	}

	prs := map[int]bool{}

	for _, t := range st.Tabs {
		if t.Review != nil && t.Review.PR != nil {
			prs[t.Review.PR.Number] = true
		}
	}

	for n := range a.drawer.fetching {
		prs[n] = true
	}

	sweep := msg.refs.Stale(a.clock.Now(),
		func(session string) bool { return sessions[session] },
		func(number int) bool { return prs[number] })
	if len(sweep.Turns) == 0 && len(sweep.PRs) == 0 {
		return nil
	}

	var (
		appCtx = a.ctx
		log    = a.log
		repo   = msg.repo
	)

	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(appCtx, refSweepTimeout)
		defer cancel()

		turnCount, err := a.git.DeleteRefs(ctx, repo, sweep.Turns)
		if err != nil {
			log.Warn("sweep turn refs", "repo", repo.CommonDir, "err", err)
		}

		prCount, err := a.git.DeleteRefs(ctx, repo, sweep.PRs)
		if err != nil {
			log.Warn("sweep pull request refs", "repo", repo.CommonDir, "err", err)
		}

		log.Info("swept refs", "repo", repo.CommonDir, "turns", turnCount, "prs", prCount)

		return nil
	}
}
