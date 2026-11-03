package ui

import (
	"strings"
	"testing"

	"github.com/mhersson/aim/internal/core"
)

func TestRailBranchLineAhead(t *testing.T) {
	t.Parallel()
	a := newTestApp(t)
	ui := a.current().theme.UI

	prefix := "  " + iconGit + " "
	noIcons := "  "

	tests := []struct {
		name   string
		cw     int
		icons  bool
		branch string
		ahead  int
		want   string
	}{
		{"at the right end", 30, true, "feat/login", 2, prefix + "feat/login" + strings.Repeat(" ", 14) + "↑2"},
		{"more digits", 30, true, "feat/login", 123, prefix + "feat/login" + strings.Repeat(" ", 12) + "↑123"},
		{"fits exactly", 17, true, "feat/login", 2, prefix + "feat/login ↑2"},
		{"the branch is cut first", 16, true, "feat/login", 2, prefix + "feat/log… ↑2"},
		{"down to 8 cells", 15, true, "feat/login", 2, prefix + "feat/lo… ↑2"},
		{"then the count goes", 14, true, "feat/login", 2, prefix + "feat/login"},
		{"and the branch is cut to the rail", 11, true, "feat/login", 2, prefix + "feat/l…"},
		{"a short branch is not cut for the count", 11, true, "main", 2, prefix + "main" + " " + "↑2"},
		{"a short branch, then the count goes", 10, true, "main", 2, prefix + "main"},
		{"nothing ahead", 30, true, "feat/login", 0, prefix + "feat/login"},
		{"no icon", 30, false, "feat/login", 2, noIcons + "feat/login" + strings.Repeat(" ", 16) + "↑2"},
		{"no icon, the count goes at the same place", 12, false, "feat/login", 2, noIcons + "feat/login"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			a.icons = tt.icons

			c := railCard{pane: "p1", state: core.AgentIdle, project: "api", branch: tt.branch, ahead: tt.ahead}
			runs := a.railCardLine(ui, c, nil, 1, tt.cw)

			if got := runsText(runs); got != tt.want {
				t.Errorf("line = %q, want %q", got, tt.want)
			}

			if w := runsWidth(runs); w > tt.cw {
				t.Errorf("line is %d cells wide, want at most %d", w, tt.cw)
			}

			if tt.ahead > 0 && strings.Contains(tt.want, "↑") {
				last := runs[len(runs)-1]
				if last.fg != ui.Info || !last.bold {
					t.Errorf("the count is %v bold %v, want Info bold", last.fg, last.bold)
				}
			}
		})
	}
}
