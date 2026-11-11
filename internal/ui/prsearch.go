package ui

import (
	"context"
	"slices"
	"strconv"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/mhersson/aim/internal/core"
	"github.com/mhersson/aim/internal/github"
)

const (
	// prSearchEvery is how often the open list reads its searches again.
	prSearchEvery = 60 * time.Second
	// prSearchTimeout bounds one host's search.
	prSearchTimeout = 15 * time.Second
)

// prListTab is a tab of the PR list.
type prListTab uint8

const (
	prTabAgents prListTab = iota
	prTabRequested
	prTabAll
	prTabCount
)

// prTabNames are the tabs' labels.
var prTabNames = [prTabCount]string{"Agents", "Review requested", "All open"}

// Texts of the searched tabs.
const (
	noticePRListLoading   = "loading pull requests…"
	noticePRListNoneAsked = "no open pull requests ask for your review"
	noticePRListNoneMine  = "you have no open pull requests"
)

// prSearchState is the searches behind the list's other tabs. seq numbers the
// asks, so an answer to an older one changes nothing; open numbers the openings
// of the list, which the tick belongs to. asked is the hosts of the latest ask
// whose host list came back, and listed says one has in this opening; the rows,
// failures and loaded test go over them.
type prSearchState struct {
	seq, open int
	asked     []string
	listed    bool
	hosts     map[string]*prSearchHost
}

// prSearchHostsMsg is an ask's hosts to search, read off the update loop.
type prSearchHostsMsg struct {
	seq   int
	hosts []string
	since time.Time
}

// prSearchHost is one host's last answer.
type prSearchHost struct {
	done bool // some answer arrived
	mine github.MyPRs
	err  error // the last ask's failure; nil after a success
}

// prSearchMsg answers one host's search.
type prSearchMsg struct {
	seq  int
	host string
	mine github.MyPRs
	err  error
}

// prSearchTickMsg is the open list's next periodic read; open is the opening it
// belongs to.
type prSearchTickMsg struct{ open int }

// prWatchedHosts are the hosts of the watched repositories.
func (a *App) prWatchedHosts() []string {
	var hosts []string

	for _, w := range a.prs.set {
		hosts = append(hosts, w.Repo.Host)
	}

	return hosts
}

// prSearchCmd starts an ask: off the update loop, since it reads gh's config,
// it lists the hosts to search, every host gh is signed in to and the watched
// ones, sorted. The review requests go back the configured days from now.
func (a *App) prSearchCmd() tea.Cmd {
	if a.reads == nil {
		return nil
	}

	a.search.seq++

	seq, parent, reads, watched := a.search.seq, a.ctx, a.reads, a.prWatchedHosts()
	since := a.pullRequests.RequestedSince(a.clock.Now())

	return func() tea.Msg {
		hosts := slices.Concat(reads.SignedInHosts(parent), watched)
		slices.Sort(hosts)

		return prSearchHostsMsg{seq: seq, hosts: slices.Compact(hosts), since: since}
	}
}

// onPRSearchHosts asks each host of a current ask for the viewer's pull
// requests, each on its own command under the app's context.
func (a *App) onPRSearchHosts(msg prSearchHostsMsg) tea.Cmd {
	if a.prList == nil || msg.seq != a.search.seq {
		return nil
	}

	a.search.asked, a.search.listed = msg.hosts, true

	seq, parent, reads := msg.seq, a.ctx, a.reads

	var cmds []tea.Cmd

	for _, host := range msg.hosts {
		cmds = append(cmds, func() tea.Msg {
			ctx, cancel := context.WithTimeout(parent, prSearchTimeout)
			defer cancel()

			mine, err := reads.MyPRs(ctx, host, msg.since)

			return prSearchMsg{seq: seq, host: host, mine: mine, err: err}
		})
	}

	return tea.Batch(cmds...)
}

// prSearchTick schedules the next periodic read of the opening.
func (a *App) prSearchTick() tea.Cmd {
	open := a.search.open

	return a.tick(prSearchEvery, func(time.Time) tea.Msg { return prSearchTickMsg{open: open} })
}

// onPRSearch keeps a host's answer. One for a list that closed, an ask a newer
// one superseded or a host its ask did not list is dropped.
func (a *App) onPRSearch(msg prSearchMsg) {
	if a.prList == nil || msg.seq != a.search.seq || !slices.Contains(a.search.asked, msg.host) {
		return
	}

	if a.search.hosts == nil {
		a.search.hosts = map[string]*prSearchHost{}
	}

	h := a.search.hosts[msg.host]
	if h == nil {
		h = &prSearchHost{}
		a.search.hosts[msg.host] = h
	}

	h.done, h.err = true, msg.err
	if msg.err == nil {
		h.mine = msg.mine
	}
}

// onPRSearchTick reads the searches again and schedules the next tick, while the
// list of that opening is open.
func (a *App) onPRSearchTick(msg prSearchTickMsg) tea.Cmd {
	if a.prList == nil || msg.open != a.search.open {
		return nil
	}

	return tea.Batch(a.prSearchCmd(), a.prSearchTick())
}

// prSearchOpen starts a new opening of the list: the old answers go, and the
// first read and tick are scheduled.
func (a *App) prSearchOpen() tea.Cmd {
	a.search.open++
	a.search.seq++ // an answer to the previous opening's ask is stale
	a.search.asked, a.search.listed = nil, false
	a.search.hosts = map[string]*prSearchHost{}

	if a.reads == nil {
		return nil
	}

	return tea.Batch(a.prSearchCmd(), a.prSearchTick())
}

// prSearchLoaded reports whether the other tabs have an answer to show. With
// no host to search there is nothing to wait for.
func (a *App) prSearchLoaded() bool {
	if !a.search.listed {
		return false
	}

	hosts := a.search.asked
	if len(hosts) == 0 {
		return true
	}

	return slices.ContainsFunc(hosts, func(h string) bool {
		s := a.search.hosts[h]

		return s != nil && s.done
	})
}

// prSearchFailures are the rows for the hosts whose last search failed, in host order.
func (a *App) prSearchFailures() []string {
	var out []string

	for _, h := range a.search.asked {
		if s := a.search.hosts[h]; s != nil && s.err != nil {
			out = append(out, "pull requests on "+printable(h)+": "+printable(s.err.Error()))
		}
	}

	return out
}

// prSearchRows are the listed pull requests of a searched tab: repositories by
// name and each one's rows newest update first. All open is the ones you opened
// and the ones on your repositories, a pull request in both once.
func (a *App) prSearchRows(tab prListTab) []prListRow {
	var rows []prListRow

	seen := map[prListKey]bool{}

	for _, h := range a.search.asked {
		s := a.search.hosts[h]
		if s == nil {
			continue
		}

		found := s.mine.Requested
		if tab == prTabAll {
			found = slices.Concat(s.mine.Authored, s.mine.Owned)
		}

		for _, f := range found {
			key := prListKey{repo: f.Repo.String(), number: f.PR.Number}
			if seen[key] {
				continue
			}

			seen[key] = true
			rows = append(rows, prListRow{key: key, pr: prInfoOf(f.PR)})
		}
	}

	sortPRListRows(rows)

	return rows
}

// prListRowsOf are the rows of a tab.
func (a *App) prListRowsOf(st core.State, tab prListTab) []prListRow {
	if tab == prTabAgents {
		return a.prListRows(st)
	}

	return a.prSearchRows(tab)
}

// prListTabRows is the tab's row count for the tab row; ok is false before the
// first answer of a searched tab.
func (a *App) prListTabRows(st core.State, tab prListTab) (n int, ok bool) {
	if tab != prTabAgents && !a.prSearchLoaded() {
		return 0, false
	}

	return len(a.prListRowsOf(st, tab)), true
}

// prListRowURL is the page of a listed pull request.
func (a *App) prListRowURL(r prListRow) string {
	if r.pr.URL != "" {
		return r.pr.URL
	}

	return a.prLink(core.ReviewPR{Repo: r.key.repo, Number: r.key.number})
}

// prTabLabel is a tab's text in the tab row.
func prTabLabel(tab prListTab, n int, ok bool) string {
	label := " " + prTabNames[tab] + " "
	if ok {
		label = " " + prTabNames[tab] + " " + strconv.Itoa(n) + " "
	}

	return label
}
