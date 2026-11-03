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
		clock   string
	}{
		{"same day", t0.Add(2 * time.Hour), t0, "(02:00)"},
		{"other day", t0.Add(52 * time.Hour), t0, "(Sat 04:00)"},
		{"in now's zone", t0.Add(2 * time.Hour), t0.In(zone), "(05:00)"},
	}

	for _, tt := range tests {
		got := resetText(tt.at, tt.now)
		if !strings.Contains(got, untilText(tt.at.Sub(tt.now))) || !strings.Contains(got, tt.clock) {
			t.Errorf("%s: resetText = %q, want the wait %q and %s", tt.name, got, untilText(tt.at.Sub(tt.now)), tt.clock)
		}
	}
}

func TestUpdatedText(t *testing.T) {
	t.Parallel()

	tests := []struct {
		ago  time.Duration
		want string
	}{
		{3 * time.Minute, "3 min"},
		{59 * time.Minute, "59 min"},
		{2*time.Hour + 10*time.Minute, "2h"},
		{25 * time.Hour, "1d"},
	}

	for _, tt := range tests {
		if got := updatedText(t0.Add(-tt.ago), t0); !strings.Contains(got, tt.want) {
			t.Errorf("updatedText(%v ago) = %q, want it to carry %q", tt.ago, got, tt.want)
		}
	}

	// Under a minute, and a clock gone back, read alike and carry no number.
	just := updatedText(t0, t0)
	for _, ago := range []time.Duration{59 * time.Second, -time.Minute} {
		if got := updatedText(t0.Add(-ago), t0); got != just || strings.ContainsAny(got, "0123456789") {
			t.Errorf("updatedText(%v ago) = %q, want %q and no number", ago, got, just)
		}
	}
}
