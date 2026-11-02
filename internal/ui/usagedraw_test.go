package ui

import (
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/x/ansi"
)

func TestUsageBar(t *testing.T) {
	t.Parallel()

	full := "\u2588"
	tests := []struct {
		used int
		want string
	}{
		{0, strings.Repeat(" ", 20)},
		{1, "\u258E" + strings.Repeat(" ", 19)},
		{23, strings.Repeat(full, 4) + "\u258B" + strings.Repeat(" ", 15)},
		{41, strings.Repeat(full, 8) + "\u258E" + strings.Repeat(" ", 11)},
		{50, strings.Repeat(full, 10) + strings.Repeat(" ", 10)},
		{100, strings.Repeat(full, 20)},
	}

	ui := mochaUI(t)

	for _, tt := range tests {
		t.Run(tt.want, func(t *testing.T) {
			t.Parallel()

			bar := usageBar(tt.used, 20, ui.Warning, ui)
			if got := ansi.Strip(bar); got != tt.want {
				t.Errorf("bar(%d) = %q, want %q", tt.used, got, tt.want)
			}

			vt := lineVT(t, bar, 20)
			cells := []rune(tt.want)

			for _, x := range []int{0, 19} {
				assertColours(t, vt, x, 0, string(cells[x]), ui.Warning, ui.Border, false)
			}
		})
	}
}

func TestUntilText(t *testing.T) {
	t.Parallel()

	tests := []struct {
		d    time.Duration
		want string
	}{
		{30 * time.Second, "under a minute"},
		{time.Minute, "1 min"},
		{59 * time.Minute, "59 min"},
		{2 * time.Hour, "2h 0m"},
		{2*time.Hour + 5*time.Minute, "2h 5m"},
		{52 * time.Hour, "2d 4h"},
		{52*time.Hour + 59*time.Minute, "2d 4h"},
	}

	for _, tt := range tests {
		if got := untilText(tt.d); got != tt.want {
			t.Errorf("untilText(%v) = %q, want %q", tt.d, got, tt.want)
		}
	}
}

func TestResetText(t *testing.T) {
	t.Parallel()

	zone := time.FixedZone("east", 3*3600)
	tests := []struct {
		name    string
		at, now time.Time
		want    string
	}{
		{"same day", t0.Add(2 * time.Hour), t0, "resets in 2h 0m (02:00)"},
		{"other day", t0.Add(52 * time.Hour), t0, "resets in 2d 4h (Sat 04:00)"},
		{"in now's zone", t0.Add(2 * time.Hour), t0.In(zone), "resets in 2h 0m (05:00)"},
	}

	for _, tt := range tests {
		if got := resetText(tt.at, tt.now); got != tt.want {
			t.Errorf("%s: resetText = %q, want %q", tt.name, got, tt.want)
		}
	}
}

func TestUpdatedText(t *testing.T) {
	t.Parallel()

	tests := []struct {
		ago  time.Duration
		want string
	}{
		{0, "updated just now"},
		{59 * time.Second, "updated just now"},
		{3 * time.Minute, "updated 3 min ago"},
		{59 * time.Minute, "updated 59 min ago"},
		{2*time.Hour + 10*time.Minute, "updated 2h ago"},
		{25 * time.Hour, "updated 1d ago"},
		{-time.Minute, "updated just now"},
	}

	for _, tt := range tests {
		if got := updatedText(t0.Add(-tt.ago), t0); got != tt.want {
			t.Errorf("updatedText(%v ago) = %q, want %q", tt.ago, got, tt.want)
		}
	}
}
