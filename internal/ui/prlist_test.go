package ui

import (
	"fmt"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/x/ansi"
	"github.com/charmbracelet/x/exp/golden"

	"github.com/mhersson/aim/internal/core"
	"github.com/mhersson/aim/internal/github"
)

// prAged is a tune that sets how long ago a pull request was updated.
func prAged(d time.Duration) func(p *core.PRInfo) {
	return func(p *core.PRInfo) { p.UpdatedAt = t0.Add(-d) }
}

func prTitled(title string) func(p *core.PRInfo) {
	return func(p *core.PRInfo) { p.Title = title }
}

// listAgents are agents on several repositories: two on one branch (one row,
// jumping to the one the rail lists first), a newer and an older pull request in
// one repository, a draft, a merged pull request and an agent with none.
func listAgents() []railAgent {
	return []railAgent{
		{newTab: true, project: "api", branch: "feat/a", state: core.AgentIdle, pr: new(prInfo("OPEN", prNumber(5), prTitled("Add the a endpoint"), checksPassed, prReview("APPROVED"), prAged(3*time.Hour)))},
		{newTab: true, project: "api", branch: "feat/a", state: core.AgentWorking, pr: new(prInfo("OPEN", prNumber(5), prTitled("Add the a endpoint"), checksPassed, prReview("APPROVED"), prAged(3*time.Hour)))},
		{project: "api", branch: "feat/c", state: core.AgentIdle, pr: new(prInfo("OPEN", prNumber(8), prTitled("Retry on timeout"), checksRunning, prAged(4*time.Minute)))},
		{newTab: true, project: "web", branch: "feat/b", state: core.AgentBlocked, pr: new(prInfo("OPEN", prNumber(9), prTitled("Login form"), prDraft, checksFailing, prAged(8*time.Second)))},
		{project: "web", branch: "feat/m", state: core.AgentIdle, pr: new(prInfo("MERGED", prNumber(3), prTitled("Already merged")))},
		{project: "web", branch: "main", state: core.AgentIdle},
		{newTab: true, project: "zeta", branch: "feat/z", state: core.AgentIdle, pr: new(prInfo("OPEN", prNumber(1), prTitled("Zeta change"), prReview("CHANGES_REQUESTED"), prAged(2*24*time.Hour)))},
	}
}

// listApp is drawApp with the pull request source and GitHub writes installed.
func listApp(t *testing.T, icons bool, w, h int, agents []railAgent) *App {
	t.Helper()

	a := drawApp(t, "catppuccin-mocha", icons, w, h, agents)
	a.prs.src = &refreshRec{fakePRs: newFakePRs()}
	a.actions = &fakeActions{}
	a.openLink = (&fakeOpen{}).open

	return a
}

func listKeys(r *prListRow) string { return fmt.Sprintf("%s#%d", r.key.repo, r.key.number) }

func TestPRListRows(t *testing.T) {
	t.Parallel()

	a := listApp(t, true, 120, 40, listAgents())
	rows := a.prListRows(a.store.State())

	var got []string
	for i := range rows {
		got = append(got, listKeys(&rows[i]))
	}

	want := []string{"github.com/acme/api#8", "github.com/acme/api#5", "github.com/acme/web#9", "github.com/acme/zeta#1"}
	if !slices.Equal(got, want) {
		t.Fatalf("rows = %v, want %v", got, want)
	}

	// Two agents share feat/a: the row's agent is the one the rail lists first
	// (working before idle), and it is in the second tab.
	if rows[1].pane != "p2" {
		t.Errorf("row for #5 jumps to %q, want p2 (the working agent)", rows[1].pane)
	}

	if rows[2].key.number != 9 || !rows[2].pr.Draft {
		t.Errorf("the draft is missing or not marked: %+v", rows[2])
	}

	if ti, _ := a.store.State().TabOf(rows[1].pane); ti != 1 {
		t.Errorf("p2 is in tab %d, want the second tab", ti)
	}

	if rows[0].root != "/w/api" {
		t.Errorf("root = %q, want the agent's directory", rows[0].root)
	}
}

func TestPRListRowsOtherCases(t *testing.T) {
	t.Parallel()

	t.Run("a shell's branch is not listed", func(t *testing.T) {
		t.Parallel()

		a := listApp(t, true, 120, 40, nil)
		openIn(t, a, core.NewTab{Kind: core.KindShell, Cwd: "/w/api"})
		seedPR(t, a, a.store.State().Tab().Focus, "/w/api", "feat/a", prInfo("OPEN"))

		if rows := a.prListRows(a.store.State()); len(rows) != 0 {
			t.Errorf("rows = %v, want none for a shell", rows)
		}
	})
}

func typedList(a *App, keys string) {
	for _, c := range keys {
		press(a, keyRune(c))
	}
}

func TestPRListKeysMove(t *testing.T) {
	t.Parallel()

	a := listApp(t, true, 120, 40, listAgents())
	press(a, keyLeader, keyRune('g'), keyRune('l'))

	if a.prList == nil {
		t.Fatalf("leader g l opened no list; notice %q", a.notice)
	}

	sel := func() string {
		rows := a.prListRows(a.store.State())

		return listKeys(&rows[a.prList.selected(rows)])
	}

	if got := sel(); got != "github.com/acme/api#8" {
		t.Errorf("selected %s first, want the newest of the first group", got)
	}

	typedList(a, "k")

	if got := sel(); got != "github.com/acme/api#8" {
		t.Errorf("k at the top moved to %s", got)
	}

	typedList(a, "jj")

	if got := sel(); got != "github.com/acme/web#9" {
		t.Errorf("jj reached %s, want the next group's row", got)
	}

	typedList(a, "jjjj")

	if got := sel(); got != "github.com/acme/zeta#1" {
		t.Errorf("j at the bottom reached %s, want the last row", got)
	}

	typedList(a, "k")

	if got := sel(); got != "github.com/acme/web#9" {
		t.Errorf("k reached %s", got)
	}

	// A key that is not the list's reaches nothing behind it.
	focus := a.store.State().Tab().Focus

	typedList(a, "x")
	press(a, keyLeader, keyRune('d'))

	if a.prList == nil || a.store.State().Tab().Focus != focus || a.pending || a.store.State().Tab().Review != nil {
		t.Error("a stray key reached past the list")
	}

	press(a, keyEsc)

	if a.prList != nil {
		t.Error("esc left the list open")
	}
}

func TestPRListKeysSideKeys(t *testing.T) {
	t.Parallel()

	t.Run("c stacks the checks over the list", func(t *testing.T) {
		t.Parallel()

		a := listApp(t, true, 120, 40, listAgents())
		press(a, keyLeader, keyRune('g'), keyRune('l'))
		typedList(a, "jj")
		typedList(a, "c")

		if a.checks == nil || a.checks.repo != "github.com/acme/web" || a.checks.number != 9 {
			t.Fatalf("checks = %+v, want #9 of acme/web", a.checks)
		}

		if a.prList == nil {
			t.Error("the list closed under the checks")
		}

		press(a, keyEsc)

		if a.checks != nil || a.prList == nil {
			t.Errorf("esc from the checks left checks %v, list %v; want the list back", a.checks, a.prList)
		}
	})

	t.Run("O opens the page and keeps the list", func(t *testing.T) {
		t.Parallel()

		a := listApp(t, true, 120, 40, listAgents())
		f := &fakeOpen{}
		a.openLink = f.open

		press(a, keyLeader, keyRune('g'), keyRune('l'))

		cmd := press(a, keyRune('O'))
		for _, m := range flatten(cmd) {
			a.Update(m)
		}

		if got := f.opened(t, 1); !slices.Equal(got, []string{"https://github.com/acme/api/pull/8"}) {
			t.Errorf("opened %v, want the selected pull request's page", got)
		}

		if a.prList == nil {
			t.Error("O closed the list")
		}
	})

	t.Run("O refuses a link that is not http", func(t *testing.T) {
		t.Parallel()

		agents := listAgents()
		agents[2].pr.URL = "javascript:alert(1)"
		a := listApp(t, true, 120, 40, agents)
		f := &fakeOpen{}
		a.openLink = f.open

		press(a, keyLeader, keyRune('g'), keyRune('l'))
		press(a, keyRune('O'))

		if len(f.urls()) != 0 || a.notice != noticeLinkRefused {
			t.Errorf("opened %v, notice %q; want a refusal", f.urls(), a.notice)
		}
	})

	t.Run("r refreshes each listed repository once", func(t *testing.T) {
		t.Parallel()

		a := listApp(t, true, 120, 40, listAgents())
		rec := a.prs.src.(*refreshRec)

		press(a, keyLeader, keyRune('g'), keyRune('l'))
		press(a, keyRune('r'))

		var got []string
		for _, r := range rec.refreshed() {
			got = append(got, r.String())
		}

		want := []string{"github.com/acme/api", "github.com/acme/web", "github.com/acme/zeta"}
		if !slices.Equal(got, want) {
			t.Errorf("refreshed %v, want %v", got, want)
		}
	})

	t.Run("a and M say pull requests are off without writes", func(t *testing.T) {
		t.Parallel()

		a := listApp(t, true, 120, 40, listAgents())
		a.actions = nil

		press(a, keyLeader, keyRune('g'), keyRune('l'))
		press(a, keyRune('a'))

		if a.notice != noticePRsOff || a.prList == nil {
			t.Errorf("a: notice %q, list %v; want %q with the list open", a.notice, a.prList, noticePRsOff)
		}

		a.notice = ""
		press(a, keyRune('M'))

		if a.notice != noticePRsOff || a.prList == nil {
			t.Errorf("M: notice %q, list %v; want %q with the list open", a.notice, a.prList, noticePRsOff)
		}
	})

	t.Run("a on your own pull request opens nothing", func(t *testing.T) {
		t.Parallel()

		agents := listAgents()
		agents[2].pr.ViewerAuthored = true
		a := listApp(t, true, 120, 40, agents)

		press(a, keyLeader, keyRune('g'), keyRune('l'))
		press(a, keyRune('a'))

		if a.notice != noticeApproveOwn || a.prList == nil || a.confirm != nil {
			t.Errorf("notice %q, list %v, confirm %v; want the refusal with the list open", a.notice, a.prList, a.confirm)
		}
	})

	t.Run("leader g l with no source says so", func(t *testing.T) {
		t.Parallel()

		a := listApp(t, true, 120, 40, listAgents())
		a.prs.src = nil

		press(a, keyLeader, keyRune('g'), keyRune('l'))

		if a.prList != nil || a.notice != noticePRsOff {
			t.Errorf("list %v, notice %q; want %q and no list", a.prList, a.notice, noticePRsOff)
		}
	})
}

// listRig is the PR drawer rig with an agent in a second tab on the branch of
// pull request 7, and the first tab active.
type listRig struct {
	*prDrawer
	agent core.PaneID
	open  *fakeOpen
	acts  *fakeActions
}

func newListRig(t *testing.T) *listRig {
	t.Helper()

	r := &listRig{prDrawer: newPRDrawer(t), open: &fakeOpen{}, acts: &fakeActions{}}
	r.openLink = r.open.open
	r.actions = r.acts

	r.agent = r.store.State().NewPaneID()
	openIn(t, r.App, core.NewTab{Kind: core.KindAgent, Cwd: r.dir})

	p := &r.railEntry(r.agent).probe
	p.known, p.repo, p.branch, p.headCwd, p.project = true, true, "feat/x", r.dir, "r"
	key := prKey{cwd: r.dir, branch: "feat/x"}
	r.prs.panes[r.agent] = &prPane{key: key, answered: key, repo: prRepo, ok: true}

	openIn(t, r.App, core.SelectTab{Index: 0})
	r.poll()

	return r
}

func TestPRListKeysThroughTheDrawer(t *testing.T) {
	t.Parallel()

	t.Run("enter shows the diff in the agent's tab", func(t *testing.T) {
		t.Parallel()

		r := newListRig(t)
		r.leader('g')
		r.typed("l")

		if r.prList == nil {
			t.Fatalf("no list; notice %q", r.notice)
		}

		r.keys(keyEnter)

		if r.prList != nil {
			t.Error("enter left the list open")
		}

		r.until(t, "the agent's drawer in PR mode", func() bool {
			st := r.store.State()

			return st.Active == 1 && st.Tab().Focus == r.agent && r.inPRMode(7)()
		})

		if rv := r.review(); rv == nil || rv.Pane != r.agent {
			t.Errorf("review = %+v, want the drawer on the agent %s", rv, r.agent)
		}
	})

	t.Run("a opens the approve confirm once the diff has loaded", func(t *testing.T) {
		t.Parallel()

		r := newListRig(t)
		r.leader('g')
		r.typed("l")
		r.keys(keyRune('a'))

		if r.prList != nil {
			t.Error("a left the list open")
		}

		r.until(t, "the approve confirm", func() bool { return r.confirm != nil && r.confirm.approve != nil })

		if r.confirm.approve.number != 7 || r.confirm.approve.sha != r.prHead {
			t.Errorf("approve target = %+v, want #7 at the loaded head %s", *r.confirm.approve, r.prHead)
		}

		if st := r.store.State(); st.Active != 1 || st.Tab().Focus != r.agent {
			t.Errorf("active tab %d, focus %s; want the agent's tab and pane", st.Active, st.Tab().Focus)
		}
	})

	t.Run("M opens the merge box once the diff has loaded", func(t *testing.T) {
		t.Parallel()

		r := newListRig(t)
		r.leader('g')
		r.typed("l")
		r.keys(keyRune('M'))

		r.until(t, "the merge box", func() bool { return r.merge != nil })

		if r.merge.pr.Number != 7 || r.merge.sha != r.prHead {
			t.Errorf("merge target = #%d at %s, want #7 at %s", r.merge.pr.Number, r.merge.sha, r.prHead)
		}

		if r.prList != nil {
			t.Error("M left the list open")
		}
	})
}

func TestPRListKeepsSelection(t *testing.T) {
	t.Parallel()

	a := listApp(t, true, 120, 40, listAgents())
	press(a, keyLeader, keyRune('g'), keyRune('l'))
	typedList(a, "j")

	selected := func() prListKey { return a.prList.sel[prTabAgents] }

	if want := (prListKey{"github.com/acme/api", 5}); selected() != want {
		t.Fatalf("selected %+v, want %+v", selected(), want)
	}

	// A poll makes #5 the newest of its repository: the rows reorder.
	api := "github.com/acme/api"
	st := a.store.State().PRs[api]
	open := slices.Clone(st.Open)

	for i := range open {
		if open[i].Number == 5 {
			open[i].UpdatedAt = t0
		}
	}

	openIn(t, a, core.SetRepoPRs{Repo: api, DefaultBranch: "main", Open: open})

	rows := a.prListRows(a.store.State())
	if rows[0].key.number != 5 {
		t.Fatalf("the poll left the rows as %v", rows)
	}

	_ = a.View()

	if i := a.prList.selected(rows); i != 0 || selected().number != 5 {
		t.Errorf("selection at %d on %+v after the reorder, want #5 at the top", i, selected())
	}

	// The selected pull request leaves the list: the selection stays at its place.
	openIn(t, a, core.SetRepoPRs{Repo: api, DefaultBranch: "main", Open: slices.DeleteFunc(slices.Clone(open), func(p core.PRInfo) bool { return p.Number == 5 })})

	rows = a.prListRows(a.store.State())
	i := a.prList.selected(rows)

	if i != 0 || rows[i].key.number != 8 {
		t.Errorf("after #5 left, selected row %d (%+v), want the row now at its place (#8)", i, rows[i].key)
	}

	// The last row leaves: the nearest is the new last.
	typedList(a, "jjj")

	openIn(t, a, core.SetRepoPRs{Repo: "github.com/acme/zeta", DefaultBranch: "main"})

	rows = a.prListRows(a.store.State())
	if i := a.prList.selected(rows); i != len(rows)-1 {
		t.Errorf("selected %d of %d after the last row left, want the new last", i, len(rows))
	}
}

func TestPRListCleansText(t *testing.T) {
	t.Parallel()

	t.Run("agent row", func(t *testing.T) {
		t.Parallel()

		const branch = "feat/\x1b[31mred\x07\u200bx"

		// The agent's branch is the pull request's head branch, which is what the row draws.
		tune := func(p *core.PRInfo) { p.HeadBranch = branch }
		agents := []railAgent{{
			newTab: true, project: "api", branch: branch, state: core.AgentIdle,
			pr: new(prInfo("OPEN", prNumber(4), tune, prTitled("Fix \x1b]0;pwned\x07the \u202eevil\u200b title\r\nsecond line"), prAged(time.Minute))),
		}}
		a := listApp(t, true, 120, 40, agents)

		if rows := a.prListRows(a.store.State()); len(rows) != 1 || rows[0].pr.HeadBranch != branch {
			t.Fatalf("rows = %+v, want one with the hostile head branch", rows)
		}

		press(a, keyLeader, keyRune('g'), keyRune('l'))

		view := a.View().Content
		text := ansi.Strip(view)

		for _, bad := range []string{"\x07", "\u200b", "\u202e", "pwned\x07", "\r", "\x1b[31mred"} {
			if strings.Contains(view, bad) {
				t.Errorf("the screen holds %q", bad)
			}
		}

		if !strings.Contains(text, "#4 Fix") || !strings.Contains(text, "title") {
			t.Errorf("the title was lost:\n%s", text)
		}

		if !strings.Contains(text, "feat/[31mredx") {
			t.Errorf("the branch was lost:\n%s", text)
		}

		// The title's second line stays on the row of its number.
		if !slices.ContainsFunc(strings.Split(text, "\n"), func(l string) bool {
			return strings.Contains(l, "#4 Fix") && strings.Contains(l, "second line")
		}) {
			t.Errorf("no row holds the whole title:\n%s", text)
		}
	})

	t.Run("search row", func(t *testing.T) {
		t.Parallel()

		mine := map[string]github.MyPRs{"github.com": {Requested: []github.SearchPR{
			searched("github.com/acme/web", 4, "Fix \x1b]0;pwned\x07the \u202eevil\u200b title", "ka\x1b[31mri\u200b", time.Minute),
		}}}
		a, _, _ := searchApp(t, listAgents(), 140, 40, mine, "github.com/acme/web")
		openList(a)
		typedList(a, "2")

		view := a.View().Content
		for _, bad := range []string{"\x07", "\u200b", "\u202e", "pwned\x07", "\x1b[31mri"} {
			if strings.Contains(view, bad) {
				t.Errorf("the screen holds %q", bad)
			}
		}

		if text := ansi.Strip(view); !strings.Contains(text, "#4 Fix") || !strings.Contains(text, "@ka[31mri") {
			t.Errorf("the row lost its text:\n%s", text)
		}
	})
}

func TestPRListEmpty(t *testing.T) {
	t.Parallel()

	a := listApp(t, true, 120, 40, []railAgent{{newTab: true, project: "api", branch: "main", state: core.AgentIdle}})
	press(a, keyLeader, keyRune('g'), keyRune('l'))

	if a.prList == nil {
		t.Fatalf("no list; notice %q", a.notice)
	}

	text := ansi.Strip(a.View().Content)
	if !strings.Contains(text, noticePRListNone) {
		t.Errorf("an empty list lacks %q:\n%s", noticePRListNone, text)
	}

	// No row, so the row keys do nothing and esc still closes.
	typedList(a, "jkcaMOr")
	press(a, keyEnter)

	if a.prList == nil || a.checks != nil || a.confirm != nil || a.merge != nil {
		t.Error("a row key did something on an empty list")
	}

	press(a, keyEsc)

	if a.prList != nil {
		t.Error("esc left the empty list open")
	}
}

func TestPRListBlocksTheMouse(t *testing.T) {
	t.Parallel()

	a := listApp(t, true, 120, 40, listAgents())
	press(a, keyLeader, keyRune('g'), keyRune('l'))

	if !a.mouseBlocked() {
		t.Error("the mouse reaches past the list")
	}
}

func TestPRListOnASmallTerminal(t *testing.T) {
	t.Parallel()

	for _, size := range [][2]int{{60, 16}, {40, 10}, {30, 6}} {
		a := listApp(t, true, size[0], size[1], listAgents())
		press(a, keyLeader, keyRune('g'), keyRune('l'))

		lines := strings.Split(a.View().Content, "\n")
		if len(lines) != size[1] {
			t.Errorf("%dx%d: %d lines", size[0], size[1], len(lines))
		}

		for i, l := range lines {
			if w := ansi.StringWidth(l); w != size[0] {
				t.Errorf("%dx%d: line %d is %d cells wide", size[0], size[1], i, w)
			}
		}
	}
}

// TestPRListMatchesTheConversation checks the list's box takes the
// conversation's columns, so a conversation drawn over it hides them.
func TestPRListMatchesTheConversation(t *testing.T) {
	t.Parallel()

	for _, c := range []struct{ width, want int }{{120, 110}, {80, 76}} {
		t.Run(strconv.Itoa(c.width), func(t *testing.T) {
			t.Parallel()

			const height = 40

			a := listApp(t, false, c.width, height, listAgents())
			press(a, keyLeader, keyRune('g'), keyRune('l'))

			top := ansi.Strip(strings.Split(a.View().Content, "\n")[height/4])
			start := strings.Index(top, "╭─ Pull requests ")
			end := strings.LastIndex(top, "╮")

			if start < 0 || end < start {
				t.Fatalf("no list top border in %q", top)
			}

			x, w := ansi.StringWidth(top[:start]), ansi.StringWidth(top[start:end])+1
			bx, _, bw, _, ok := planBox(c.width, height)

			if !ok {
				t.Fatalf("no conversation box at %d columns", c.width)
			}

			if w != c.want || x != bx || w != bw {
				t.Errorf("list at column %d, %d wide; want column %d, %d wide as the conversation", x, w, bx, c.want)
			}
		})
	}
}

func prListScene(t *testing.T, icons bool, w, h int) *App {
	t.Helper()

	agents := listAgents()
	agents = append(agents, railAgent{
		newTab: true, project: "multiplex", branch: "feat/a-branch-with-a-rather-long-name-that-has-to-be-cut", state: core.AgentIdle,
		pr: new(prInfo("OPEN", prNumber(112), prTitled("A pull request title that is far too long to fit in the list as it is drawn \x1b[31mhere\x07"), checksFailing, prReview("CHANGES_REQUESTED"), prAged(26*time.Hour))),
	})

	a := listApp(t, icons, w, h, agents)
	press(a, keyLeader, keyRune('g'), keyRune('l'))
	typedList(a, "jj")

	return a
}

func prListScenes() []struct {
	name string
	w, h int
} {
	return []struct {
		name string
		w, h int
	}{{"wide", 140, 44}, {"80x24", 80, 24}}
}

// TestPRListGolden draws the list once in colour; no drawer is open, so no
// highlighted code is drawn and it runs in parallel.
func TestPRListGolden(t *testing.T) {
	t.Parallel()

	for _, c := range prListScenes() {
		t.Run("catppuccin-mocha/icons=true/"+c.name, func(t *testing.T) {
			golden.RequireEqual(t, []byte(prListScene(t, true, c.w, c.h).View().Content))
		})
	}
}

// TestPRListText is the icons=false screens without styling; the colour golden
// of the icons=true ones holds the same text.
func TestPRListText(t *testing.T) {
	t.Parallel()

	for _, c := range prListScenes() {
		t.Run("icons=false/"+c.name, func(t *testing.T) {
			t.Parallel()
			golden.RequireEqual(t, []byte(ansi.Strip(prListScene(t, false, c.w, c.h).View().Content)))
		})
	}
}

// otherTabsRig is the PR drawer rig with searches that answer o/r#7 (base main)
// and acme/infra#88 on the Requested tab, and the rig's pane resolved to o/r.
func otherTabsRig(t *testing.T, opts ...func(*Options)) *prDrawer {
	t.Helper()

	r := newPRDrawer(t, opts...)
	r.openLink = (&fakeOpen{}).open
	r.actions = &fakeActions{}
	r.reads = &fakeReads{fail: map[string]error{}, mine: map[string]github.MyPRs{
		"github.com": {Requested: []github.SearchPR{
			searched("github.com/o/r", 7, "Add x", "kari-dev", time.Hour, func(p *github.PR) {
				p.BaseBranch, p.HeadBranch = "main", "feat/x"
			}),
			searched("github.com/acme/infra", 88, "Bump", "kari-dev", 2*time.Hour, func(p *github.PR) {
				p.Draft, p.ID = true, "PR_88"
				p.BaseBranch, p.HeadBranch, p.HeadOID = "main", "feat/infra", "listed-head"
			}),
		}},
		"ghe.example.com": {Requested: []github.SearchPR{
			searched("ghe.example.com/acme/infra", 4, "Svc", "li", time.Hour, func(p *github.PR) {
				p.BaseBranch, p.HeadBranch = "main", "feat/svc"
			}),
		}},
	}}

	watching(r.App, "github.com/o/r", "ghe.example.com/acme/infra")

	focus := r.store.State().Tab().Focus
	k := prKey{cwd: r.dir, branch: "main"}
	r.prs.panes[focus] = &prPane{key: k, answered: k, repo: prRepo, ok: true}

	r.leader('g')
	r.typed("l")
	r.typed("2")
	r.until(t, "both hosts' answers", func() bool {
		return len(r.prListRowsOf(r.store.State(), prTabRequested)) == 3
	})

	return r
}

func (p *prDrawer) selectRow(t *testing.T, repo string) {
	t.Helper()

	rows := p.prListRowsOf(p.store.State(), p.prList.tab)
	for i := range rows {
		if rows[i].key.repo == repo {
			p.prList.sel[p.prList.tab] = rows[i].key

			return
		}
	}

	t.Fatalf("no row of %s", repo)
}

func TestPRListCheckout(t *testing.T) {
	t.Parallel()

	t.Run("enter", func(t *testing.T) {
		t.Parallel()

		r := otherTabsRig(t)
		focus := r.store.State().Tab().Focus

		r.selectRow(t, "github.com/o/r")
		r.keys(keyEnter)

		if r.prList != nil {
			t.Error("enter left the list open")
		}

		r.until(t, "PR mode on #7", r.inPRMode(7))

		rv := r.review()
		if rv.Pane != focus || rv.PR.Root != r.dir {
			t.Errorf("review = %+v, want the drawer on %s with root %q", rv, focus, r.dir)
		}

		if v := r.reviewViewOf(r.store.State().Tab().ID); v.prBase != "main" {
			t.Errorf("base = %q, want main from the row", v.prBase)
		}
	})

	t.Run("a opens the approve confirm on the loaded head", func(t *testing.T) {
		t.Parallel()

		r := otherTabsRig(t)
		r.selectRow(t, "github.com/o/r")
		r.keys(keyRune('a'))

		r.until(t, "the approve confirm", func() bool { return r.confirm != nil && r.confirm.approve != nil })

		if r.confirm.approve.number != 7 || r.confirm.approve.sha != r.prHead {
			t.Errorf("approve target = %+v, want #7 at %s", *r.confirm.approve, r.prHead)
		}
	})
}

func TestPRCheckoutPrefersFocused(t *testing.T) {
	t.Parallel()

	a := newPRDrawer(t)
	focus := a.store.State().Tab().Focus

	if focus != "p1" {
		t.Fatalf("focus = %q, the test assumes p1", focus)
	}

	resolved := func(cwd string, repo github.Repo) *prPane {
		k := prKey{cwd: cwd}

		return &prPane{key: k, answered: k, repo: repo, ok: true}
	}
	elsewhere := github.Repo{Host: "github.com", Owner: "x", Name: "y"}

	// p0 has the lower id; the focused p1 wins anyway.
	a.prs.panes = map[core.PaneID]*prPane{
		"p0":  resolved("/w/lower", prRepo),
		focus: resolved("/w/focused", prRepo),
	}

	if dir, ok := a.prCheckout(prRepo.String()); !ok || dir != "/w/focused" {
		t.Errorf("checkout = %q, %v; want the focused pane's", dir, ok)
	}

	// A focused pane outside the repository: the lowest id of the others wins.
	a.prs.panes = map[core.PaneID]*prPane{
		"p9":  resolved("/w/p9", prRepo),
		"p2":  resolved("/w/p2", prRepo),
		focus: resolved("/w/elsewhere", elsewhere),
	}

	if dir, ok := a.prCheckout(prRepo.String()); !ok || dir != "/w/p2" {
		t.Errorf("checkout = %q, %v; want the lowest id's (/w/p2)", dir, ok)
	}
}

func TestPRCheckoutUsesAnsweredDir(t *testing.T) {
	t.Parallel()

	a := newPRDrawer(t)
	focus := a.store.State().Tab().Focus

	// The pane moved on; its repository answer has not caught up.
	a.prs.panes = map[core.PaneID]*prPane{
		focus: {key: prKey{cwd: "/w/moved"}, answered: prKey{cwd: "/w/answered"}, repo: prRepo, ok: true},
	}

	if dir, ok := a.prCheckout(prRepo.String()); !ok || dir != "/w/answered" {
		t.Errorf("checkout = %q, %v; want the answered directory", dir, ok)
	}
}

func TestPRListKeepsEscClose(t *testing.T) {
	t.Parallel()

	for _, w := range []int{60, 80, 100, 120, 140} {
		a := listApp(t, true, w, 30, listAgents())
		press(a, keyLeader, keyRune('g'), keyRune('l'))

		want := a.keys.prList.hint("prlist.close") + " close"
		if !strings.Contains(ansi.Strip(a.View().Content), want) {
			t.Errorf("width %d: the hint row lost %q", w, want)
		}
	}
}
