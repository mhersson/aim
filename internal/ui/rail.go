package ui

import (
	"context"
	"fmt"
	"path/filepath"
	"slices"
	"strings"
	"time"
	"unicode"

	tea "charm.land/bubbletea/v2"

	"github.com/mhersson/aim/internal/core"
	"github.com/mhersson/aim/internal/git"
)

const (
	// railPulseEvery is the blocked bar's blink half-period.
	railPulseEvery = 500 * time.Millisecond
	// railFlashFor is how long a jump flashes the pane's frame.
	railFlashFor = 600 * time.Millisecond
)

// idleAction is the third card line of an idle agent.
const idleAction = "waiting for a prompt"

// railCard is one agent on the rail.
type railCard struct {
	harness    string // the program's name; empty unless the rail lists more than one harness
	pane       core.PaneID
	tab        int // 0-based
	tabName    string
	state      core.AgentState
	project    string
	name       string // the session's custom name, cleaned; empty shows the project
	branch     string
	place      string // the followed repository's name when it is not the agent's own; empty otherwise
	action     string // the card's third line; empty for an agent without hooks
	detached   bool   // branch holds the first 7 characters of the commit
	stats      git.Stats
	statsKnown bool
	active     time.Time // the later of its last output and last hook event
	unseen     bool
	model      string // the model the status line reported, cleaned; empty shows none
	meter      int    // the context percentage as core holds it; the drawing clamps it
	meterKnown bool
	ahead      int         // commits past the branch's upstream; 0 with none or no upstream
	pr         core.PRInfo // the pull request of the card's branch, when hasPR
	hasPR      bool
	plan       bool // the agent's latest plan is unread
}

// railGroup is the shown cards of one tab, by state and then pane order.
type railGroup struct {
	tab   int    // 0-based
	name  string // the tab's name as the workspace bar shows it
	cards []railCard
}

// railStateOrder is the order of the cards within a tab, most urgent first.
var railStateOrder = [...]core.AgentState{core.AgentBlocked, core.AgentWorking, core.AgentIdle, core.AgentUnknown}

// railStateRank is a state's place in railStateOrder; a state not in it comes last.
func railStateRank(s core.AgentState) int {
	if i := slices.Index(railStateOrder[:], s); i >= 0 {
		return i
	}

	return len(railStateOrder)
}

// railField names where a search matched.
type railField int

const (
	railFieldProject railField = iota
	railFieldName
	railFieldPlace
	railFieldBranch
	railFieldAction
	railFieldTabNumber
	railFieldTabName
)

// railHit is a match in one field, as rune offsets [start, end).
type railHit struct {
	field      railField
	start, end int
}

// railProbe is the result of one pane's probes.
type railProbe struct {
	project, branch string
	place           string // the followed place's repository name when it differs from the own directory's; "" otherwise
	oid             string // the full commit id; "" on an unborn branch
	detached        bool
	ahead           int    // commits past the branch's upstream; 0 with none or no upstream
	repo            bool   // the followed place is inside a repository
	known           bool   // a head probe has succeeded
	headCwd         string // the followed directory the head was probed in
	gone            bool   // headCwd does not exist
	stats           git.Stats
	statsKnown      bool
	statsCwd        string
}

// railName is a session's custom name and the transcript it was read from. It
// shows only while that is still the pane's transcript.
type railName struct {
	text       string
	transcript string
}

// railPane is the rail's per-pane memory; it lives until the pane closes.
type railPane struct {
	hookAt   time.Time
	unseen   bool
	probe    railProbe
	name     railName
	head     probeSlot
	stats    probeSlot
	nameSlot probeSlot
}

// railState is the rail's UI state; core.State only says whether it is open.
type railState struct {
	focused   bool        // the rail has the keyboard
	sel       core.PaneID // the selected agent
	selIdx    int         // its index in the shown list when last seen
	searching bool        // the search row is open
	query     string
	top       int  // first list row shown; the drawing clamps it
	pulseDim  bool // the blocked bar shows Border instead of Blocked

	flashPane  core.PaneID // the pane whose frame shows the jump flash
	flashUntil time.Time   // the flash ends when the clock reaches this
	flashSeq   int         // a flash tick of an earlier jump does not clear a later flash
	pulsing    bool        // a railPulseMsg is outstanding

	panes  map[core.PaneID]*railPane
	focus  core.PaneID     // the active tab's focused pane at the last update, so a shell that takes focus is probed
	ctx    context.Context //nolint:containedctx // the rail's lifetime, set while the rail is open; stats and name probes derive from it
	cancel context.CancelFunc
	cache  railCache
	gen    int // bumps on every open, so late messages of an earlier open are dropped
}

func (a *App) railIsOpen() bool { return a.rail.ctx != nil }

// railEntry returns the pane's memory, creating it.
func (a *App) railEntry(id core.PaneID) *railPane {
	if a.rail.panes == nil {
		a.rail.panes = map[core.PaneID]*railPane{}
	}

	rp, ok := a.rail.panes[id]
	if !ok {
		rp = &railPane{}
		a.rail.panes[id] = rp
	}

	return rp
}

// railCards lists the agents matching the search, one group per tab in tab
// order, so a card changes group only when its pane changes tab.
func (a *App) railCards(st core.State) []railGroup {
	tabs := a.tabInfos()
	mixed := a.railMixed(st)
	now := a.clock.Now()

	var out []railGroup

	// AgentPanes lists by tab, so a tab's cards are adjacent.
	for _, id := range st.AgentPanes() {
		m := st.Panes[id]
		ti, _ := st.TabOf(id)

		c := railCard{
			pane: id, tab: ti, state: m.State, action: cardAction(m),
			model: cleanModel(m.Model), meter: m.Context, meterKnown: m.ContextKnown,
			plan: m.Plan.Path != "" && m.Plan.Unread,
		}
		if mixed {
			c.harness = a.harnessOf(m).Program()
		}

		if p := a.panes[id]; p != nil && p.codex.checkHooks(now, p.hooksSeen) {
			c.action = "check /hooks"
		}

		if ti < len(tabs) {
			c.tabName = tabs[ti].name
		}

		rp := a.rail.panes[id]
		c.project, c.name = cardLabels(m, rp)

		if rp != nil {
			c.unseen, c.active = rp.unseen, rp.hookAt
			if rp.probe.known {
				c.branch, c.place, c.detached, c.ahead = rp.probe.branch, rp.probe.place, rp.probe.detached, rp.probe.ahead
			}

			// The counts belong to the place the branch line names.
			if rp.probe.statsKnown && rp.probe.statsCwd == rp.probe.headCwd {
				c.stats, c.statsKnown = rp.probe.stats, true
			}
		}

		c.pr, c.hasPR = a.prOfPane(st, id)

		if p, ok := a.panes[id]; ok && p.lastOut.After(c.active) {
			c.active = p.lastOut
		}

		// Printable once, so the matcher and the drawing see the same runes.
		c.branch, c.place, c.action = printable(c.branch), printable(c.place), printable(c.action)

		if _, ok := c.matches(a.rail.query); !ok {
			continue
		}

		if n := len(out); n == 0 || out[n-1].tab != c.tab {
			out = append(out, railGroup{tab: c.tab, name: c.tabName})
		}

		out[len(out)-1].cards = append(out[len(out)-1].cards, c)
	}

	// Stable, so the cards of one state keep the tab's pane order.
	for _, g := range out {
		slices.SortStableFunc(g.cards, func(x, y railCard) int { return railStateRank(x.state) - railStateRank(y.state) })
	}

	return out
}

// railMixed reports whether the agent panes span two or more harnesses. It
// counts every agent, not only the cards a search leaves.
func (a *App) railMixed(st core.State) bool {
	var first string

	for _, id := range st.AgentPanes() {
		hid := a.harnessOf(st.Panes[id]).ID()
		if first == "" {
			first = hid
		} else if hid != first {
			return true
		}
	}

	return false
}

// cardLabels are the project and the session's name card line 1 can show, both
// cleaned. The name is a file's text; a blank one, or the project's own name,
// leaves the project alone on the line.
func cardLabels(m core.Pane, rp *railPane) (project, name string) {
	project = filepath.Base(m.WorkDir())

	if rp != nil {
		if rp.probe.known {
			project = rp.probe.project
		}

		// A name belongs to the transcript it was read from, not to the pane.
		if m.Transcript != "" && rp.name.transcript == m.Transcript {
			name = rp.name.text
		}
	}

	project = printable(project)

	if name = strings.TrimSpace(printable(name)); name == project {
		name = ""
	}

	return project, name
}

// cardName is what card line 1 calls the agent: its session's name, else its project.
func (a *App) cardName(st core.State, id core.PaneID) string {
	project, name := cardLabels(st.Panes[id], a.rail.panes[id])
	if name != "" {
		return name
	}

	return project
}

func cardAction(m core.Pane) string {
	switch m.State {
	case core.AgentBlocked, core.AgentWorking:
		return m.Action
	case core.AgentIdle:
		return idleAction
	}

	return ""
}

// matches reports where query occurs in the card, case-insensitively: project,
// session name, the other repository's name, branch, action, "tab N" and the
// tab's name. An empty query matches with no hits.
func (c railCard) matches(query string) ([]railHit, bool) {
	if query == "" {
		return nil, true
	}

	q := foldRunes(query)

	var hits []railHit

	for _, f := range [...]struct {
		field railField
		text  string
	}{
		{railFieldProject, c.project},
		{railFieldName, c.name},
		{railFieldPlace, c.place},
		{railFieldBranch, c.branch},
		{railFieldAction, c.action},
		{railFieldTabNumber, fmt.Sprintf("tab %d", c.tab+1)},
		{railFieldTabName, c.tabName},
	} {
		if i := indexRunes(foldRunes(f.text), q); i >= 0 {
			hits = append(hits, railHit{field: f.field, start: i, end: i + len(q)})
		}
	}

	return hits, len(hits) > 0
}

// foldRunes lowercases rune by rune, so offsets in the result are offsets in s.
func foldRunes(s string) []rune {
	r := []rune(s)
	for i, c := range r {
		r[i] = unicode.ToLower(c)
	}

	return r
}

func indexRunes(s, sub []rune) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if slices.Equal(s[i:i+len(sub)], sub) {
			return i
		}
	}

	return -1
}

// railShown flattens the groups into the order the list shows.
func (a *App) railShown(st core.State) []railCard {
	var out []railCard
	for _, g := range a.railCards(st) {
		out = append(out, g.cards...)
	}

	return out
}

// railNormalize keeps the selection on a shown card: it follows its agent, else
// takes the card now at its old index, else the last.
func (a *App) railNormalize(st core.State) { a.railNormalizeIn(a.railShown(st)) }

func (a *App) railNormalizeIn(shown []railCard) {
	if len(shown) == 0 {
		a.rail.sel, a.rail.selIdx = "", 0

		return
	}

	if i := slices.IndexFunc(shown, func(c railCard) bool { return c.pane == a.rail.sel }); i >= 0 {
		a.rail.selIdx = i

		return
	}

	i := min(max(a.rail.selIdx, 0), len(shown)-1)
	a.rail.sel, a.rail.selIdx = shown[i].pane, i
}

// railMove moves the selection by delta cards, stopping at the ends.
func (a *App) railMove(delta int) {
	st := a.store.State()
	a.railNormalize(st)

	shown := a.railShown(st)
	if len(shown) == 0 {
		return
	}

	i := min(max(a.rail.selIdx+delta, 0), len(shown)-1)
	a.rail.sel, a.rail.selIdx = shown[i].pane, i
}

// railSelect selects an agent if it is shown.
func (a *App) railSelect(id core.PaneID) {
	for i, c := range a.railShown(a.store.State()) {
		if c.pane == id {
			a.rail.sel, a.rail.selIdx = id, i

			return
		}
	}
}

// railSetSearch opens the search row (or closes it with open false) and sets its text.
func (a *App) railSetSearch(open bool, text string) {
	a.rail.searching, a.rail.query = open, text
	a.railNormalize(a.store.State())
}

// markUnseen flags an agent that changed while it is not the focused pane of the active tab.
func (a *App) markUnseen(id core.PaneID) {
	if a.store.State().Tab().Focus != id {
		a.railEntry(id).unseen = true
	}
}

// changeAgentState dispatches a state and marks the pane unseen when it changed.
func (a *App) changeAgentState(id core.PaneID, s core.AgentState) error {
	prev := a.store.State().Panes[id].State
	if err := a.dispatch(core.SetAgentState{ID: id, State: s}); err != nil {
		return err
	}

	if prev != s {
		a.markUnseen(id)
	}

	return nil
}

// railAfterUpdate runs after every update: it clears the mark of the focused
// pane, forgets closed panes, starts the head probes that new agents, a focused
// shell or a new working directory need and, while the rail is open, keeps the
// selection valid and starts its other probes.
func (a *App) railAfterUpdate() {
	// A rail the width rule hides cannot hold the keyboard.
	if a.rail.focused && !a.railVisible() {
		a.railRelease()
	}

	st := a.store.State()

	if rp, ok := a.rail.panes[st.Tab().Focus]; ok {
		rp.unseen = false
	}

	for id := range a.rail.panes {
		if _, ok := st.Panes[id]; !ok {
			delete(a.rail.panes, id)
		}
	}

	a.railHeadSync(st)

	if !a.railIsOpen() {
		return
	}

	a.railFit(st)

	if cmd := a.railPulseCmd(st); cmd != nil {
		a.later = append(a.later, cmd)
	}

	for _, id := range st.AgentPanes() {
		if cmd := a.railSync(st, id, st.Panes[id].FollowDir()); cmd != nil {
			a.later = append(a.later, cmd)
		}
	}
}

// railFit keeps the selection on a shown card and stores the list's first row
// clamped to keep that card whole, so the drawing never has to.
func (a *App) railFit(st core.State) {
	c := &a.rail.cache

	// A kept filter matches on the cards' text, so what the line cache keys on
	// counts too; without one only which agents exist matters.
	var agents []railAgentKey

	if a.rail.query != "" {
		c.fitSpare = a.railAgentKeys(c.fitSpare[:0], st.AgentPanes(), a.clock.Now())
		agents = c.fitSpare
	}

	if c.fitValid && c.fit == a.railFitKeyOf() && slices.Equal(agents, c.fitAgents) {
		return
	}

	c.fits++
	defer func() {
		c.fit, c.fitValid = a.railFitKeyOf(), true
		c.fitAgents = append(c.fitAgents[:0], agents...)
	}()

	v, ok := a.railViewOf(st)
	if !ok {
		a.railNormalize(st)

		return
	}

	a.railNormalizeIn(v.shown)

	sel := -1
	if len(v.shown) > 0 {
		sel = a.rail.selIdx
	}

	a.rail.top = railClampTop(v.rows, v.geom.list.H, a.rail.top, sel)
}

func (a *App) railFitKeyOf() railFitKey {
	return railFitKey{
		storeVer: a.storeVer, w: a.width, h: a.height, searching: a.rail.searching,
		query: a.rail.query,
		sel:   a.rail.sel, selIdx: a.rail.selIdx, top: a.rail.top,
	}
}

// railToggle is leader a: it opens the rail, or gives it the keyboard, or closes it.
func (a *App) railToggle() tea.Cmd {
	var cmds []tea.Cmd

	switch {
	case !a.store.State().Rail:
		// dispatch logs its own failures.
		_ = a.dispatch(core.SetRail{Open: true})
		cmds = append(cmds, a.railOpened())

		if a.railVisible() {
			a.railTake()
		}
	case !a.railVisible() || a.rail.focused:
		a.railClose()
	default:
		a.railTake()
	}

	cmds = append(cmds, a.relayout())

	return tea.Batch(cmds...)
}

// railTake gives the rail the keyboard; the review drawer, which shares it,
// gives it up.
func (a *App) railTake() {
	a.rail.focused = true
	a.drawerRelease()
}

// railRelease gives the keyboard back to the panes and ends search typing; the
// filter text stays.
func (a *App) railRelease() {
	a.rail.focused, a.rail.searching = false, false
}

// railClose closes the rail and forgets its search; the keyboard returns to the focused pane.
func (a *App) railClose() {
	_ = a.dispatch(core.SetRail{Open: false})
	a.railClosed()
	a.rail.focused, a.rail.searching, a.rail.query = false, false, ""
}
