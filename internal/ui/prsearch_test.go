package ui

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/charmbracelet/x/exp/golden"

	"github.com/mhersson/aim/internal/core"
	"github.com/mhersson/aim/internal/github"
)

// fakeReads answers MyPRs per host and records the calls.
type fakeReads struct {
	mu     sync.Mutex
	calls  []string
	sinces []time.Time // each MyPRs call's since, in call order
	mine   map[string]github.MyPRs
	fail   map[string]error

	logData  []byte // what JobLog returns
	logCut   bool
	logErr   error
	logCalls []jobLogCall

	runOut   github.CheckRunOutput // what CheckRun returns
	runErr   error
	runCalls []jobLogCall

	// Conversation answers convs[n] for the nth call, the last one once they run
	// out, or convErr.
	convs     []github.Conversation
	convErr   error
	convCalls []convCall

	gitToken   string
	tokenErr   error
	mergeBases map[string]string // by head
	mergeErr   error
}

// convCall is one Conversation read.
type convCall struct {
	repo   github.Repo
	number int
}

func (f *fakeReads) Conversation(_ context.Context, r github.Repo, number int) (github.Conversation, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	n := len(f.convCalls)
	f.convCalls = append(f.convCalls, convCall{repo: r, number: number})

	if f.convErr != nil || len(f.convs) == 0 {
		return github.Conversation{}, f.convErr
	}

	return f.convs[min(n, len(f.convs)-1)], nil
}

func (f *fakeReads) MergeBase(_ context.Context, r github.Repo, base, head string) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	f.calls = append(f.calls, "mergebase "+r.String()+" "+base+"..."+head)

	if f.mergeErr != nil {
		return "", f.mergeErr
	}

	return f.mergeBases[head], nil
}

func (f *fakeReads) GitToken(_ context.Context, host string) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	f.calls = append(f.calls, "gittoken "+host)

	return f.gitToken, f.tokenErr
}

func (f *fakeReads) convReads() []convCall {
	f.mu.Lock()
	defer f.mu.Unlock()

	return slices.Clone(f.convCalls)
}

// jobLogCall is one JobLog read.
type jobLogCall struct {
	repo github.Repo
	id   int64
}

func (f *fakeReads) JobLog(_ context.Context, r github.Repo, id int64) (data []byte, cut bool, err error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	f.logCalls = append(f.logCalls, jobLogCall{repo: r, id: id})

	return f.logData, f.logCut, f.logErr
}

func (f *fakeReads) CheckRun(_ context.Context, r github.Repo, id int64) (github.CheckRunOutput, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	f.runCalls = append(f.runCalls, jobLogCall{repo: r, id: id})

	return f.runOut, f.runErr
}

func (f *fakeReads) runReads() []jobLogCall {
	f.mu.Lock()
	defer f.mu.Unlock()

	return slices.Clone(f.runCalls)
}

func (f *fakeReads) logReads() []jobLogCall {
	f.mu.Lock()
	defer f.mu.Unlock()

	return slices.Clone(f.logCalls)
}

func (f *fakeReads) MyPRs(_ context.Context, host string, since time.Time) (github.MyPRs, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	f.calls = append(f.calls, host)
	f.sinces = append(f.sinces, since)

	if err := f.fail[host]; err != nil {
		return github.MyPRs{}, err
	}

	return f.mine[host], nil
}

func (f *fakeReads) since() []time.Time {
	f.mu.Lock()
	defer f.mu.Unlock()

	return slices.Clone(f.sinces)
}

func (f *fakeReads) called() []string {
	f.mu.Lock()
	defer f.mu.Unlock()

	out := slices.Clone(f.calls)
	slices.Sort(out)

	return out
}

func searched(repo string, number int, title, author string, age time.Duration, tune ...func(*github.PR)) github.SearchPR {
	r, ok := repoOfKey(repo)
	if !ok {
		panic("bad repo " + repo)
	}

	pr := github.PR{
		Number: number, Title: title, Author: author, State: "OPEN",
		URL:       fmt.Sprintf("https://%s/pull/%d", repo, number),
		UpdatedAt: t0.Add(-age), HeadBranch: fmt.Sprintf("branch-%d", number),
	}
	for _, f := range tune {
		f(&pr)
	}

	return github.SearchPR{Repo: r, PR: pr}
}

func watching(a *App, repos ...string) {
	a.prs.set = nil

	for _, key := range repos {
		r, _ := repoOfKey(key)
		a.prs.set = append(a.prs.set, github.Watch{Repo: r})
	}

	a.prs.sent, a.prs.ver = true, a.storeVer
}

// searchApp is a list app with fake reads, the watch set on repos and a tick
// log; the list is not opened.
func searchApp(t *testing.T, agents []railAgent, w, h int, mine map[string]github.MyPRs, repos ...string) (*App, *fakeReads, *tickLog) {
	t.Helper()

	a := listApp(t, true, w, h, agents)
	reads := &fakeReads{mine: mine, fail: map[string]error{}}
	ticks := &tickLog{}
	a.reads, a.tick = reads, ticks.tick

	watching(a, repos...)

	return a, reads, ticks
}

// answered delivers the answers a command produced and returns the other messages.
func answered(a *App, cmd tea.Cmd) []tea.Msg {
	var rest []tea.Msg

	for _, m := range flatten(cmd) {
		if _, ok := m.(prSearchMsg); ok {
			a.Update(m)
		} else {
			rest = append(rest, m)
		}
	}

	return rest
}

func openList(a *App) {
	answered(a, press(a, keyLeader, keyRune('g'), keyRune('l')))
}

func (t *tickLog) searchTicks() int {
	n := 0

	for _, m := range t.msgs {
		if _, ok := m.(prSearchTickMsg); ok {
			n++
		}
	}

	return n
}

func tabbedRows(a *App, tab prListTab) []string {
	var got []string

	rows := a.prListRowsOf(a.store.State(), tab)
	for i := range rows {
		got = append(got, listKeys(&rows[i]))
	}

	return got
}

func TestPRListTabKeys(t *testing.T) {
	t.Parallel()

	mine := map[string]github.MyPRs{"github.com": {
		Requested: []github.SearchPR{
			searched("github.com/acme/web", 311, "Fix", "kari-dev", 2*time.Hour),
			searched("github.com/acme/web", 305, "Older", "kari-dev", 24*time.Hour),
		},
		Authored: []github.SearchPR{searched("github.com/acme/web", 400, "Mine", "me", time.Hour)},
	}}
	a, _, _ := searchApp(t, listAgents(), 120, 40, mine, "github.com/acme/web")
	openList(a)

	shown := func() prListTab { return a.prList.tab }

	want := []prListTab{prTabRequested, prTabAll, prTabAgents}
	for _, w := range want {
		press(a, keyTab)

		if shown() != w {
			t.Fatalf("tab showed %d, want %d", shown(), w)
		}
	}

	press(a, keyShiftTab)

	if shown() != prTabAll {
		t.Errorf("shift+tab showed %d, want All", shown())
	}

	typedList(a, "2")

	if shown() != prTabRequested {
		t.Errorf("2 showed %d", shown())
	}

	typedList(a, "3")

	if shown() != prTabAll {
		t.Errorf("3 showed %d", shown())
	}

	typedList(a, "1")

	if shown() != prTabAgents {
		t.Errorf("1 showed %d", shown())
	}

	// Each tab keeps its selection.
	typedList(a, "2j")

	if got := a.prList.sel[prTabRequested]; got != (prListKey{"github.com/acme/web", 305}) {
		t.Fatalf("Requested selected %+v after j", got)
	}

	typedList(a, "3")
	typedList(a, "2")

	if rows := a.prListRowsOf(a.store.State(), prTabRequested); a.prList.selected(rows) != 1 {
		t.Error("the Requested selection was lost over a tab switch")
	}

	// Closing on All and reopening opens on Agents.
	typedList(a, "3")
	press(a, keyEsc)
	openList(a)

	if shown() != prTabAgents {
		t.Errorf("reopened on tab %d, want Agents", shown())
	}
}

func TestPRListTabsWithoutReads(t *testing.T) {
	t.Parallel()

	a := listApp(t, true, 120, 40, listAgents())
	openList(a)
	typedList(a, "2")

	if a.prList.tab != prTabAgents || a.notice != noticePRsOff {
		t.Errorf("tab %d, notice %q; want Agents and %q", a.prList.tab, a.notice, noticePRsOff)
	}
}

func TestPRListSearchWhen(t *testing.T) {
	t.Parallel()

	mine := map[string]github.MyPRs{
		"github.com":      {Requested: []github.SearchPR{searched("github.com/acme/web", 311, "Fix", "kari-dev", time.Hour)}},
		"ghe.example.com": {Requested: []github.SearchPR{searched("ghe.example.com/team/svc", 4, "Svc", "li", time.Hour)}},
	}
	a, reads, ticks := searchApp(t, listAgents(), 120, 40, mine, "github.com/acme/web", "ghe.example.com/team/svc")
	hosts := []string{"ghe.example.com", "github.com"}

	openList(a)

	if got := reads.called(); !slices.Equal(got, hosts) {
		t.Fatalf("opening read %v, want one per host %v", got, hosts)
	}

	if ticks.searchTicks() != 1 {
		t.Fatalf("opening scheduled %d ticks, want 1", ticks.searchTicks())
	}

	// r on Requested reads each host again and asks the poller for each listed repository.
	typedList(a, "2")

	rec := a.prs.src.(*refreshRec)
	answered(a, press(a, keyRune('r')))

	if got := reads.called(); len(got) != 4 {
		t.Errorf("after r the reads are %v, want two per host", got)
	}

	var refreshed []string
	for _, r := range rec.refreshed() {
		refreshed = append(refreshed, r.String())
	}

	if want := []string{"github.com/acme/web", "ghe.example.com/team/svc"}; !slices.Equal(refreshed, want) {
		t.Errorf("refreshed %v, want %v", refreshed, want)
	}

	// The tick of this opening reads again and schedules the next.
	_, cmd := a.Update(prSearchTickMsg{open: a.search.open})
	answered(a, cmd)

	if got := reads.called(); len(got) != 6 || ticks.searchTicks() != 2 {
		t.Errorf("after the tick: %d reads, %d ticks; want 6 and 2", len(got), ticks.searchTicks())
	}

	// An older opening's tick, or one after esc, does nothing.
	old := a.search.open
	press(a, keyEsc)

	_, cmd = a.Update(prSearchTickMsg{open: old})
	if cmd != nil {
		t.Error("a tick after esc returned a command")
	}

	openList(a)

	before, beforeTicks := len(reads.called()), ticks.searchTicks()

	_, cmd = a.Update(prSearchTickMsg{open: old})
	if cmd != nil || len(reads.called()) != before || ticks.searchTicks() != beforeTicks {
		t.Error("a tick of an older opening read or rescheduled")
	}
}

// The Review requested search goes back the configured days from the app's
// clock at each ask.
func TestPRListSearchSince(t *testing.T) {
	t.Parallel()

	week := 7 * 24 * time.Hour

	tests := []struct {
		days int
		want []time.Time
	}{
		{days: 0, want: []time.Time{{}, {}}},
		{days: 7, want: []time.Time{t0.Add(-week), t0.Add(time.Hour - week)}},
	}
	for _, tt := range tests {
		t.Run(strconv.Itoa(tt.days), func(t *testing.T) {
			t.Parallel()

			a, reads, _ := searchApp(t, listAgents(), 120, 40, nil, "github.com/acme/web")
			clk := &fakeClock{t: t0}
			a.clock, a.pullRequests = clk, core.PullRequestsConfig{ReviewRequestedDays: tt.days}

			openList(a)
			clk.Advance(time.Hour)

			_, cmd := a.Update(prSearchTickMsg{open: a.search.open})
			answered(a, cmd)

			if got := reads.since(); !slices.EqualFunc(got, tt.want, time.Time.Equal) {
				t.Errorf("since = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestPRListSearchStale(t *testing.T) {
	t.Parallel()

	mine := map[string]github.MyPRs{"github.com": {Requested: []github.SearchPR{searched("github.com/acme/web", 311, "Fix", "kari-dev", time.Hour)}}}

	t.Run("an older ask", func(t *testing.T) {
		t.Parallel()

		a, _, _ := searchApp(t, listAgents(), 120, 40, mine, "github.com/acme/web")
		openList(a)
		typedList(a, "2")

		// r asks again; the answer to the ask before it lands afterwards.
		press(a, keyRune('r'))

		late := prSearchMsg{seq: a.search.seq - 1, host: "github.com", mine: github.MyPRs{
			Requested: []github.SearchPR{searched("github.com/acme/web", 999, "Late", "x", time.Hour)},
		}}
		a.Update(late)

		if want, got := []string{"github.com/acme/web#311"}, tabbedRows(a, prTabRequested); !slices.Equal(got, want) {
			t.Errorf("a stale answer changed the rows to %v, want %v", got, want)
		}
	})

	t.Run("after esc", func(t *testing.T) {
		t.Parallel()

		a, _, _ := searchApp(t, listAgents(), 120, 40, mine, "github.com/acme/web")
		msgs := flatten(press(a, keyLeader, keyRune('g'), keyRune('l')))

		press(a, keyEsc)

		for _, m := range msgs {
			a.Update(m)
		}

		if len(a.search.hosts) != 0 {
			t.Errorf("an answer after esc was kept: %v", a.search.hosts)
		}
	})

	t.Run("after a reopen", func(t *testing.T) {
		t.Parallel()

		a, _, _ := searchApp(t, listAgents(), 120, 40, mine, "github.com/acme/web")
		msgs := flatten(press(a, keyLeader, keyRune('g'), keyRune('l')))

		press(a, keyEsc)
		press(a, keyLeader, keyRune('g'), keyRune('l'))

		for _, m := range msgs {
			a.Update(m)
		}

		if len(a.search.hosts) != 0 {
			t.Errorf("the previous opening's answer was kept: %v", a.search.hosts)
		}
	})

	t.Run("for a host no longer watched", func(t *testing.T) {
		t.Parallel()

		a, _, _ := searchApp(t, listAgents(), 120, 40, mine, "github.com/acme/web")
		openList(a)

		a.Update(prSearchMsg{seq: a.search.seq, host: "other.example.com", mine: mine["github.com"]})

		if a.search.hosts["other.example.com"] != nil {
			t.Error("an answer for an unwatched host was kept")
		}
	})
}

func TestPRListSearchTexts(t *testing.T) {
	t.Parallel()

	// The empty Review requested tab names the age limit; All open has none. A
	// limit of 7 days is TestPRListSearchGolden's requested-none.
	if prListNoneAsked(0) == prListNoneAsked(1) || prListNoneAsked(1) == prListNoneAsked(7) {
		t.Fatal("the empty texts of 0, 1 and 7 days do not differ")
	}

	for _, days := range []int{0, 1} {
		t.Run(fmt.Sprintf("no rows, %d days", days), func(t *testing.T) {
			t.Parallel()

			a, _, _ := searchApp(t, listAgents(), 120, 40, nil, "github.com/acme/web")
			a.pullRequests = core.PullRequestsConfig{ReviewRequestedDays: days}
			openList(a)
			typedList(a, "2")

			if text := ansi.Strip(a.View().Content); !strings.Contains(text, prListNoneAsked(days)) || !strings.Contains(text, " Review requested 0 ") {
				t.Errorf("no empty text %q:\n%s", prListNoneAsked(days), text)
			}

			typedList(a, "3")

			if text := ansi.Strip(a.View().Content); !strings.Contains(text, noticePRListNoneMine) {
				t.Errorf("no %q:\n%s", noticePRListNoneMine, text)
			}
		})
	}

	t.Run("a failed host", func(t *testing.T) {
		t.Parallel()

		mine := map[string]github.MyPRs{"github.com": {Requested: []github.SearchPR{searched("github.com/acme/web", 311, "Fix", "kari-dev", time.Hour)}}}
		a, reads, _ := searchApp(t, listAgents(), 120, 40, mine, "github.com/acme/web", "ghe.example.com/team/svc")
		reads.fail["ghe.example.com"] = errors.New("HTTP 502\x1b]0;pwned\x07 Bad\u200b Gateway")

		openList(a)
		typedList(a, "2")

		text := ansi.Strip(a.View().Content)
		if !strings.Contains(text, "#311") {
			t.Errorf("the other host's rows are missing:\n%s", text)
		}

		if i := slices.IndexFunc(strings.Split(text, "\n"), func(l string) bool { return strings.Contains(l, "HTTP 502") }); i < 0 ||
			!strings.Contains(strings.Split(text, "\n")[i], "ghe.example.com") {
			t.Errorf("no failure row naming the host and HTTP 502:\n%s", text)
		}

		view := a.View().Content
		for _, bad := range []string{"\x07", "\u200b", "pwned\x07", "\x1b]0;"} {
			if strings.Contains(view, bad) {
				t.Errorf("the screen holds %q", bad)
			}
		}
	})
}

func TestPRListOtherTabKeys(t *testing.T) {
	t.Parallel()

	mine := map[string]github.MyPRs{"github.com": {Requested: []github.SearchPR{
		searched("github.com/acme/web", 311, "Fix", "kari-dev", time.Hour),
		searched("github.com/acme/web", 305, "Own", "me", 2*time.Hour, func(p *github.PR) { p.ViewerAuthored = true }),
	}}}
	setup := func(t *testing.T) (*App, *fakeOpen) {
		t.Helper()

		a, _, _ := searchApp(t, listAgents(), 120, 40, mine, "github.com/acme/web")
		f := &fakeOpen{}
		a.openLink = f.open

		openList(a)
		typedList(a, "2")

		return a, f
	}

	t.Run("c opens the checks and focuses the pull request", func(t *testing.T) {
		t.Parallel()

		a, _ := setup(t)
		typedList(a, "c")

		if a.checks == nil || a.checks.repo != "github.com/acme/web" || a.checks.number != 311 || a.prList == nil {
			t.Fatalf("checks = %+v, list %v", a.checks, a.prList)
		}

		a.prSync()

		i := slices.IndexFunc(a.prs.set, func(w github.Watch) bool { return w.Repo.String() == "github.com/acme/web" })
		if i < 0 || a.prs.set[i].Focused != 311 {
			t.Errorf("watch set = %+v, want acme/web with 311 focused", a.prs.set)
		}
	})
}

func TestPRListOtherTabRemap(t *testing.T) {
	t.Parallel()

	a, _, _ := searchApp(t, listAgents(), 160, 40, nil, "github.com/acme/web")
	remapTables(t, a, contextKeys("pr_list", "]", "prlist.tab.next"))
	openList(a)
	press(a, keyRune(']'))

	if a.prList.tab != prTabRequested {
		t.Errorf("] showed tab %d", a.prList.tab)
	}

	key := a.keys.prList.hint("prlist.tab.next")
	if key != "]" {
		t.Errorf("the tab hint names %q, want the bound ]", key)
	}

	if text := ansi.Strip(a.View().Content); !strings.Contains(text, key+" next tab") {
		t.Errorf("the tab row lacks the hint %q:\n%s", key+" next tab", text)
	}
}

// tabScene opens the list on tab with the answers in mine and the hosts failing in fail.
func tabScene(t *testing.T, icons bool, w, h int, tab prListTab, answered bool, mine map[string]github.MyPRs, fail map[string]error, repos ...string) *App {
	t.Helper()

	a, reads, _ := searchApp(t, listAgents(), w, h, mine, repos...)
	a.icons = icons

	maps.Copy(reads.fail, fail)

	if answered {
		openList(a)
	} else {
		press(a, keyLeader, keyRune('g'), keyRune('l'))
	}

	typedList(a, fmt.Sprint(int(tab)+1))

	return a
}

func tabScenes(t *testing.T, icons bool) map[string]*App {
	t.Helper()

	full := map[string]github.MyPRs{"github.com": {
		Requested: []github.SearchPR{
			searched("github.com/acme/web", 311, "Fix the login form on narrow screens", "kari-dev", 2*time.Hour, func(p *github.PR) { p.ReviewDecision = "APPROVED"; p.Checks = github.Checks{Total: 3, Done: 3} }),
			searched("github.com/acme/web", 305, "Retry on timeout", "li", 24*time.Hour, func(p *github.PR) { p.Draft = true }),
			searched("github.com/acme/infra", 88, "Bump the chart", "kari-dev", 5*time.Hour, func(p *github.PR) { p.ReviewDecision = "CHANGES_REQUESTED" }),
		},
		Authored: []github.SearchPR{
			searched("github.com/mhersson/aim", 24, "The PR list tabs", "mhersson", time.Hour, func(p *github.PR) { p.Checks = github.Checks{Total: 4, Done: 2, Failed: 1} }),
		},
	}}
	fail := map[string]error{"ghe.example.com": errors.New("HTTP 502 Bad Gateway")}
	both := []string{"github.com/acme/web", "ghe.example.com/team/svc"}

	// The empty tab under the default age limit.
	none := tabScene(t, icons, 120, 40, prTabRequested, true, nil, nil, "github.com/acme/web")
	none.pullRequests = core.DefaultConfig().PullRequests

	return map[string]*App{
		"requested":         tabScene(t, icons, 120, 40, prTabRequested, true, full, nil, "github.com/acme/web"),
		"all":               tabScene(t, icons, 120, 40, prTabAll, true, full, nil, "github.com/acme/web"),
		"requested-loading": tabScene(t, icons, 120, 40, prTabRequested, false, full, nil, "github.com/acme/web"),
		"requested-none":    none,
		"requested-failed":  tabScene(t, icons, 120, 40, prTabRequested, true, full, fail, both...),
		"requested-80x24":   tabScene(t, icons, 80, 24, prTabRequested, true, full, nil, "github.com/acme/web"),
	}
}

var tabSceneNames = []string{"requested", "all", "requested-loading", "requested-none", "requested-failed", "requested-80x24"}

// TestPRListSearchGolden draws the searched tabs once in colour; no drawer is
// open, so no highlighted code is drawn and it runs in parallel.
func TestPRListSearchGolden(t *testing.T) {
	t.Parallel()

	scenes := tabScenes(t, true)

	for _, name := range tabSceneNames {
		t.Run("catppuccin-mocha/icons=true/"+name, func(t *testing.T) {
			golden.RequireEqual(t, []byte(scenes[name].View().Content))
		})
	}
}

// TestPRListSearchText is the icons=false screens without styling; the colour
// golden of the icons=true ones holds the same text.
func TestPRListSearchText(t *testing.T) {
	t.Parallel()

	scenes := tabScenes(t, false)

	for _, name := range tabSceneNames {
		t.Run("icons=false/"+name, func(t *testing.T) {
			t.Parallel()
			golden.RequireEqual(t, []byte(ansi.Strip(scenes[name].View().Content)))
		})
	}
}
