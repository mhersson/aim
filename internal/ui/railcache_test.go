package ui

import (
	"strings"
	"testing"
	"time"

	"github.com/mhersson/aim/internal/core"
	"github.com/mhersson/aim/internal/ui/theme"
)

// railPicture is the rail's cached lines as one string.
func railPicture(a *App) string { return strings.Join(a.rail.cache.lines, "\n") }

// cacheApp is the fixture scene with one frame drawn, so the cache is warm.
func cacheApp(t *testing.T) *App {
	t.Helper()

	a := drawApp(t, "catppuccin-mocha", true, 100, 30, railFixtureAgents())
	a.rail.sel = "p2"
	a.View()

	return a
}

func (a *App) clockSet(d time.Duration) { a.clock.(*fakeClock).Set(t0.Add(d)) }

// rebuiltBy draws a frame after change and reports whether the rail's lines
// were rebuilt and whether they differ from before.
func rebuiltBy(a *App, change func()) (rebuilt, differs bool) {
	before, builds := railPicture(a), a.rail.cache.builds

	change()
	a.View()

	return a.rail.cache.builds > builds, railPicture(a) != before
}

func TestRailCacheReusesLinesWhileNothingChanges(t *testing.T) {
	t.Parallel()
	a := cacheApp(t)
	builds := a.rail.cache.builds

	if builds == 0 {
		t.Fatal("the first frame did not build the rail")
	}

	for range 3 {
		a.View()
	}

	if got := a.rail.cache.builds; got != builds {
		t.Errorf("builds after three more frames = %d, want %d", got, builds)
	}
}

func TestRailCacheKeyCoversEveryInput(t *testing.T) {
	t.Parallel()

	latte, err := theme.ByName("catppuccin-latte")
	if err != nil {
		t.Fatal(err)
	}

	latteLook, err := newLook(latte)
	if err != nil {
		t.Fatal(err)
	}

	probe := func(a *App, id core.PaneID) *railProbe { return &a.railEntry(id).probe }

	tests := []struct {
		name    string
		prep    func(t *testing.T, a *App)
		change  func(t *testing.T, a *App)
		visible bool
	}{
		{name: "terminal size", change: func(t *testing.T, a *App) { sized(t, a, 70, 30) }, visible: true},
		{name: "theme preview", change: func(t *testing.T, a *App) { a.preview = &latteLook }, visible: true},
		{name: "theme setting", change: func(t *testing.T, a *App) { openIn(t, a, core.SetTheme{Name: "catppuccin-latte"}) }, visible: true},
		{name: "icons", change: func(t *testing.T, a *App) { a.icons = false }, visible: true},
		{name: "keyboard flag", change: func(t *testing.T, a *App) { a.rail.focused = true }, visible: true},
		{name: "selection", change: func(t *testing.T, a *App) { a.rail.sel = "p3" }, visible: true},
		{name: "search open", change: func(t *testing.T, a *App) { a.rail.searching = true }, visible: true},
		{name: "search text", change: func(t *testing.T, a *App) { a.railSetSearch(true, "feat") }, visible: true},
		{name: "stored top", change: func(t *testing.T, a *App) { a.rail.top = 3 }, visible: true},
		{name: "spinner frame", change: func(t *testing.T, a *App) { a.spin++ }, visible: true},
		{name: "pulse phase", change: func(t *testing.T, a *App) { a.rail.pulseDim = true }, visible: true},
		{name: "state", change: func(t *testing.T, a *App) { openIn(t, a, core.SetAgentState{ID: "p2", State: core.AgentIdle}) }, visible: true},
		{name: "action", change: func(t *testing.T, a *App) { openIn(t, a, core.SetAgentAction{ID: "p2", Action: "Read go.mod"}) }, visible: true},
		{
			name: "model",
			change: func(t *testing.T, a *App) {
				openIn(t, a, core.SetAgentStatus{ID: "p2", Session: "s2", Model: "Opus 5.5"})
			},
			visible: true,
		},
		{
			name: "context meter",
			prep: func(t *testing.T, a *App) {
				openIn(t, a, core.SetAgentStatus{ID: "p2", Session: "s2", Model: "Opus 5.5", Context: 40, ContextKnown: true})
			},
			change: func(t *testing.T, a *App) {
				openIn(t, a, core.SetAgentStatus{ID: "p2", Session: "s2", Model: "Opus 5.5", Context: 41, ContextKnown: true})
			},
			visible: true,
		},
		{name: "project", change: func(t *testing.T, a *App) { probe(a, "p2").project = "other" }, visible: true},
		{name: "branch", change: func(t *testing.T, a *App) { probe(a, "p2").branch = "fix/other" }, visible: true},
		{name: "detached", change: func(t *testing.T, a *App) { probe(a, "p2").detached = true }},
		{name: "ahead", change: func(t *testing.T, a *App) { probe(a, "p2").ahead = 3 }, visible: true},
		{
			name: "pull request",
			change: func(t *testing.T, a *App) {
				seedPR(t, a, "p2", "/w/web", "feat/login", prInfo("OPEN", checksRunning))
			},
			visible: true,
		},
		{
			name: "pull request repository",
			prep: func(t *testing.T, a *App) {
				seedPR(t, a, "p2", "/w/web", "feat/login", prInfo("OPEN", checksRunning))
				a.prs.panes["p2"].ok = false
			},
			change:  func(t *testing.T, a *App) { a.prs.panes["p2"].ok = true },
			visible: true,
		},
		{
			name: "pull request branch resolved for",
			prep: func(t *testing.T, a *App) {
				seedPR(t, a, "p2", "/w/web", "feat/login", prInfo("OPEN", checksRunning))
				a.prs.panes["p2"].answered.branch = "feat/other"
			},
			change:  func(t *testing.T, a *App) { a.prs.panes["p2"].answered.branch = "feat/login" },
			visible: true,
		},
		{name: "stats", change: func(t *testing.T, a *App) { probe(a, "p2").stats.Added = 999 }, visible: true},
		{name: "stats known", change: func(t *testing.T, a *App) { probe(a, "p1").statsKnown = false }, visible: true},
		{name: "unseen mark", change: func(t *testing.T, a *App) { a.railEntry("p3").unseen = true }, visible: true},
		{
			name: "session name",
			prep: func(t *testing.T, a *App) {
				openIn(t, a, core.SetAgentSession{ID: "p2", Session: "s2", Transcript: "/w/web/p2.jsonl"})
				a.railEntry("p2").name = railName{text: "first", transcript: "/w/web/p2.jsonl"}
			},
			change:  func(t *testing.T, a *App) { a.railEntry("p2").name.text = "second" },
			visible: true,
		},
		{
			name: "tab name",
			change: func(t *testing.T, a *App) {
				openIn(t, a, core.RenameTab{ID: a.store.State().Tabs[0].ID, Name: "renamed"})
			},
		},
		{
			name: "tab name under a search it matches",
			prep: func(t *testing.T, a *App) { a.railSetSearch(false, "renamed") },
			change: func(t *testing.T, a *App) {
				openIn(t, a, core.RenameTab{ID: a.store.State().Tabs[0].ID, Name: "renamed"})
				a.railNormalize(a.store.State())
			},
			visible: true,
		},
		{
			name: "rename prompt under a search it matches",
			prep: func(t *testing.T, a *App) {
				a.railSetSearch(false, "zebra")
				a.openPrompt(a.store.State().Tab().ID, "", nil)
			},
			change:  func(t *testing.T, a *App) { a.prompt.input.SetValue("zebra") },
			visible: true,
		},
		{
			name: "tab and order",
			change: func(t *testing.T, a *App) {
				openIn(t, a, core.MovePane{ID: "p2", Tab: 2, Area: a.bodyArea()})
			},
			visible: true,
		},
		{
			name:    "a new agent",
			change:  func(t *testing.T, a *App) { openIn(t, a, core.NewTab{Kind: core.KindAgent, Cwd: "/w/new"}) },
			visible: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			a := cacheApp(t)

			if tt.prep != nil {
				tt.prep(t, a)
				a.View()
			}

			rebuilt, differs := rebuiltBy(a, func() { tt.change(t, a) })
			if !rebuilt {
				t.Error("the lines were not rebuilt")
			}

			if differs != tt.visible {
				t.Errorf("lines differ = %v, want %v", differs, tt.visible)
			}
		})
	}
}

func TestRailNameChangeRedrawsTheCard(t *testing.T) {
	t.Parallel()
	a := cacheApp(t)

	const transcript = "/w/web/p2.jsonl"

	openIn(t, a, core.SetAgentSession{ID: "p2", Session: "s2", Transcript: transcript})

	rp := a.railEntry("p2")
	rp.name = railName{text: "first", transcript: transcript}

	a.View()

	tests := []struct {
		name    string
		change  func()
		rebuilt bool
		differs bool
	}{
		{"a new name", func() { rp.name.text = "second" }, true, true},
		{"a name read from another transcript", func() { rp.name.transcript = "/w/web/earlier.jsonl" }, true, true},
		{"the same name read again", func() { rp.name = railName{text: "second", transcript: "/w/web/earlier.jsonl"} }, false, false},
		{"the name is back for the pane's transcript", func() { rp.name.transcript = transcript }, true, true},
	}

	for _, tt := range tests {
		rebuilt, differs := rebuiltBy(a, tt.change)
		if rebuilt != tt.rebuilt || differs != tt.differs {
			t.Errorf("%s: rebuilt = %v, differs = %v; want %v, %v", tt.name, rebuilt, differs, tt.rebuilt, tt.differs)
		}
	}
}

func TestRailCacheHiddenRailDropsLines(t *testing.T) {
	t.Parallel()
	a := cacheApp(t)

	sized(t, a, 63, 30)
	a.View()

	if len(a.rail.cache.lines) != 0 {
		t.Errorf("a hidden rail kept %d cached lines", len(a.rail.cache.lines))
	}

	sized(t, a, 100, 30)

	before := a.rail.cache.builds

	a.View()

	if a.rail.cache.builds == before || len(a.rail.cache.lines) == 0 {
		t.Error("showing the rail again did not rebuild it")
	}
}

func TestRailCacheAges(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		prep     func(a *App)
		advance  time.Duration
		rebuilt  bool
		differs  bool
		describe string
	}{
		{name: "an age that renders the same", advance: 500 * time.Millisecond},
		{name: "an age that changes", advance: time.Second, rebuilt: true, differs: true},
		{
			name:    "59s to 1m",
			prep:    func(a *App) { a.railEntry("p1").hookAt = t0.Add(-59 * time.Second) },
			advance: time.Second, rebuilt: true, differs: true,
		},
		{
			name:    "59m to 1h",
			prep:    func(a *App) { a.railEntry("p5").hookAt = t0.Add(-59 * time.Minute) },
			advance: time.Minute, rebuilt: true, differs: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			a := cacheApp(t)

			if tt.prep != nil {
				tt.prep(a)
				a.View()
			}

			rebuilt, differs := rebuiltBy(a, func() { a.clockSet(tt.advance) })
			if rebuilt != tt.rebuilt || differs != tt.differs {
				t.Errorf("rebuilt = %v, differs = %v, want %v, %v", rebuilt, differs, tt.rebuilt, tt.differs)
			}
		})
	}
}

func TestRailCacheAgeFollowsPaneOutput(t *testing.T) {
	t.Parallel()
	a := cacheApp(t)

	// A pane's output is newer than its hook: the age shown is 0s.
	a.panes["p2"] = &pane{id: "p2", lastOut: t0}

	t.Cleanup(func() { delete(a.panes, "p2") })

	if rebuilt, differs := rebuiltBy(a, func() {}); !rebuilt || !differs {
		t.Errorf("output newer than the hook: rebuilt = %v, differs = %v, want true, true", rebuilt, differs)
	}
}

func TestRailCacheKeepsLinesThroughPaneOutput(t *testing.T) {
	t.Parallel()

	f := newFlowApp(t)
	f.twoTabs(t)
	f.open(t)

	out := func(s string) {
		t.Helper()

		feedAndWait(t, f.panes["p4"], f.st.proc(3), s)
		f.Update(paneDirtyMsg{id: "p4", p: f.panes["p4"]})
	}

	out("one\r\n")
	f.View()

	builds := f.rail.cache.builds
	if builds == 0 {
		t.Fatal("the rail was not drawn")
	}

	f.clk.Advance(400 * time.Millisecond)
	out("two\r\n")
	f.View()

	if got := f.rail.cache.builds; got != builds {
		t.Errorf("pane output rebuilt the rail: builds %d, want %d", got, builds)
	}
}

func TestRailCacheIgnoresOtherChrome(t *testing.T) {
	t.Parallel()
	a := cacheApp(t)

	if rebuilt, _ := rebuiltBy(a, func() { a.notice = "layout: bsp" }); rebuilt {
		t.Error("a toast rebuilt the rail")
	}
}

func TestRailFitRunsOnlyWhenItsInputsChange(t *testing.T) {
	t.Parallel()
	a := cacheApp(t)
	a.railOpened()
	a.Update(struct{}{})

	fits := a.rail.cache.fits

	for range 3 {
		a.Update(struct{}{})
	}

	if got := a.rail.cache.fits; got != fits {
		t.Errorf("idle updates ran the fit %d times", got-fits)
	}

	for name, change := range map[string]func(){
		"selection": func() { a.rail.sel = "p3" },
		"state":     func() { openIn(t, a, core.SetAgentState{ID: "p2", State: core.AgentIdle}) },
		"resize":    func() { sized(t, a, 90, 30) },
		"search":    func() { a.railSetSearch(true, "") },
	} {
		before := a.rail.cache.fits

		change()
		a.Update(struct{}{})

		if a.rail.cache.fits == before {
			t.Errorf("%s: the fit did not run", name)
		}
	}

	// Clearing a kept filter changes nothing else the fit looks at.
	a.railSetSearch(false, "api")
	a.Update(struct{}{})

	before := a.rail.cache.fits
	a.rail.query = ""
	a.Update(struct{}{})

	if a.rail.cache.fits == before {
		t.Error("clearing the query did not refit")
	}

	// A search matches on probe results, so a probe change must refit.
	a.railSetSearch(true, "api")
	a.Update(struct{}{})

	before = a.rail.cache.fits
	a.railEntry("p6").probe = railProbe{known: true, project: "api"}
	a.Update(struct{}{})

	if a.rail.cache.fits == before {
		t.Error("a probe change under a search did not refit")
	}
}

func TestRailFitUnderAKeptFilterSkipsOutputOnlyUpdates(t *testing.T) {
	t.Parallel()
	a := cacheApp(t)
	a.railOpened()
	a.railSetSearch(false, "api")
	a.Update(struct{}{})

	fits := a.rail.cache.fits

	// Output and hook times that do not change what a card shows.
	rp := a.railEntry("p2")
	rp.hookAt = rp.hookAt.Add(-time.Second)

	for range 3 {
		a.Update(struct{}{})
	}

	if got := a.rail.cache.fits; got != fits {
		t.Errorf("updates with nothing new ran the fit %d times under a kept filter", got-fits)
	}

	before := a.rail.cache.fits
	a.clockSet(10 * time.Minute)
	a.Update(struct{}{})

	if a.rail.cache.fits == before {
		t.Error("a card's age changing did not refit under a kept filter")
	}
}
