package ui

import (
	"fmt"
	"testing"

	"github.com/mhersson/aim/internal/ui/theme"
)

func TestBorderRunCache(t *testing.T) {
	t.Parallel()

	th, err := theme.Builtin().ByName("catppuccin-mocha")
	if err != nil {
		t.Fatal(err)
	}

	st := newStyles(th)
	if got, want := st.borderRun(ownAgent, "│"), st.borderAgent.Render("│"); got != want {
		t.Errorf("run = %q, want %q", got, want)
	}

	if st.borderRun(ownAgent, "│") != st.borderAgent.Render("│") || len(st.runs.m) != 1 {
		t.Errorf("repeat run not served from the cache: %d entries", len(st.runs.m))
	}

	for i := range 2 * runCacheMax {
		st.borderRun(0, fmt.Sprint(i))
	}

	if n := len(st.runs.m); n > runCacheMax {
		t.Errorf("cache holds %d entries, want at most %d", n, runCacheMax)
	}
}
