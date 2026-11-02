package ui

import (
	"sync"

	"github.com/mhersson/aim/internal/ui/diffview"
)

// drawCaches is what drawing builds once and only reads after: the lexers
// with their per-name matches, and the diff themes of each theme. New makes
// one per app; the UI's tests share one, since building it is most of a
// small test's cost.
type drawCaches struct {
	hl     diffview.Highlighter
	mu     sync.Mutex
	themes map[string]themePair // by theme name
}

type themePair struct{ dv, flash *diffview.Theme }

// diffThemes returns the diff view's theme and its flash twin for the look,
// building them once per theme name.
func (c *drawCaches) diffThemes(lk look) (dv, flash *diffview.Theme) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if p, ok := c.themes[lk.theme.Name]; ok {
		return p.dv, p.flash
	}

	pal := reviewPalette(lk.theme)
	dv = diffview.NewTheme(pal)
	pal.Ground = diffview.MixColors(pal.Ground, lk.theme.UI.Match, tintFlash)
	flash = diffview.NewTheme(pal)

	if c.themes == nil {
		c.themes = map[string]themePair{}
	}

	c.themes[lk.theme.Name] = themePair{dv, flash}

	return dv, flash
}
