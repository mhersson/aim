package ui

import (
	"slices"
	"time"

	"github.com/mhersson/aim/internal/core"
	"github.com/mhersson/aim/internal/github"
)

// railKey is the rail picture's inputs that are plain fields. The core state
// is covered by its dispatch counter instead of by value, and the agents by
// railAgentKey: a frame is keyed without cloning the state or listing cards.
type railKey struct {
	storeVer  uint64
	w, h      int
	theme     string
	icons     bool
	focused   bool
	searching bool
	query     string
	sel       core.PaneID
	top       int
	spin      int
	pulseDim  bool
	renaming  bool
	rename    string
}

// railAgentKey is what an agent's card takes from outside the core state: the
// unseen mark, the probe results, the repository its branch's pull request is
// read from, the session name, a Codex's thread name and the age as displayed
// (its unit and count, so an age that renders the same does not rebuild).
type railAgentKey struct {
	checkHooks bool
	unseen     bool
	probe      railProbe
	prRepo     github.Repo
	prOK       bool
	name       railName
	codexName  string
	ageN       int
	ageUnit    byte
}

// railFitKey is the inputs of railFit. Without a search the cards' text does
// not matter to it, only which agents exist; with one, railFit adds the agents'
// keys.
type railFitKey struct {
	storeVer  uint64
	w, h      int
	searching bool
	query     string
	sel       core.PaneID
	selIdx    int
	top       int
}

// railCache holds the last rail picture with the key it was drawn for, so a
// frame in which nothing in the rail changed reuses its lines.
type railCache struct {
	valid  bool
	key    railKey
	agents []railAgentKey
	spare  []railAgentKey
	ids    []core.PaneID // the agents of the build, valid while key.storeVer holds
	lines  []string
	width  int

	fit       railFitKey
	fitValid  bool
	fitAgents []railAgentKey
	fitSpare  []railAgentKey

	builds int // rebuilds of the lines, for tests
	fits   int // runs of railFit, for tests
}

func (a *App) railKeyOf(lk look) railKey {
	k := railKey{
		storeVer:  a.storeVer,
		w:         a.width,
		h:         a.height,
		theme:     lk.theme.Name,
		icons:     a.icons,
		focused:   a.rail.focused,
		searching: a.rail.searching,
		query:     a.rail.query,
		sel:       a.rail.sel,
		top:       a.rail.top,
		spin:      a.spin,
		pulseDim:  a.rail.pulseDim,
	}

	if a.prompt != nil && a.prompt.kind == promptRename {
		k.renaming, k.rename = true, a.prompt.input.Value()
	}

	return k
}

// railAgentKeys appends the keys of the agents ids, reading the memory the
// cards are built from.
func (a *App) railAgentKeys(dst []railAgentKey, ids []core.PaneID, now time.Time) []railAgentKey {
	for _, id := range ids {
		var k railAgentKey

		var active time.Time

		if rp := a.rail.panes[id]; rp != nil {
			k.unseen, k.probe, k.name, active = rp.unseen, rp.probe, rp.name, rp.hookAt
		}

		if p := a.panes[id]; p != nil {
			k.checkHooks, k.codexName = p.codex.checkHooks(now), p.codex.last.ThreadName
			if p.lastOut.After(active) {
				active = p.lastOut
			}
		}

		k.prRepo, k.prOK = a.prRepoOf(id)
		k.ageN, k.ageUnit = railAgeOf(active, now)
		dst = append(dst, k)
	}

	return dst
}

// railFrame is the rail's lines and width for this frame; (nil, 0) while the
// rail is hidden. The lines are rebuilt only when the key or an agent's key
// differs from the last build's; the cache is a memo of a pure function.
func (a *App) railFrame(st core.State, lk look) ([]string, int) {
	c := &a.rail.cache

	if !a.railShows(st.Rail) {
		*c = railCache{builds: c.builds, fits: c.fits, fit: c.fit, fitValid: c.fitValid, fitAgents: c.fitAgents, fitSpare: c.fitSpare}

		return nil, 0
	}

	now := a.clock.Now()
	key := a.railKeyOf(lk)

	if c.valid && key == c.key {
		c.spare = a.railAgentKeys(c.spare[:0], c.ids, now)
		if slices.Equal(c.spare, c.agents) {
			return c.lines, c.width
		}
	}

	v, ok := a.railViewOf(st)
	if !ok {
		return nil, 0
	}

	c.builds++
	c.valid, c.key, c.ids = true, key, st.AgentPanes()
	c.agents = a.railAgentKeys(c.agents[:0], c.ids, now)
	c.lines, c.width = a.railLines(st, lk, v), v.geom.box.W

	return c.lines, c.width
}
