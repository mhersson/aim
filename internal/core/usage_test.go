package core

import (
	"math"
	"reflect"
	"testing"
	"time"
)

func TestFloorPercent(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		in   float64
		want int
	}{
		{"NaN", math.NaN(), 0},
		{"negative", -3, 0},
		{"zero", 0, 0},
		{"fraction floors", 41.9, 41},
		{"hundred", 100, 100},
		{"over hundred", 100.4, 100},
		{"huge", 1e300, 100},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if got := FloorPercent(tt.in); got != tt.want {
				t.Errorf("FloorPercent(%v) = %d, want %d", tt.in, got, tt.want)
			}
		})
	}
}

func TestUsageWindowShown(t *testing.T) {
	t.Parallel()

	at := time.Date(2026, 1, 1, 10, 0, 0, 0, time.UTC)
	r1 := at.Add(2 * time.Hour)

	tests := []struct {
		name string
		w    UsageWindow
		now  time.Time
		want bool
	}{
		{"zero window", UsageWindow{}, at, false},
		{"reset ahead", UsageWindow{Used: 5, ResetsAt: r1}, at, true},
		{"at the reset", UsageWindow{Used: 5, ResetsAt: r1}, r1, false},
		{"after the reset", UsageWindow{Used: 5, ResetsAt: r1}, r1.Add(time.Second), false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if got := tt.w.Shown(tt.now); got != tt.want {
				t.Errorf("Shown() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestNoteUsage(t *testing.T) {
	t.Parallel()

	at := time.Date(2026, 1, 1, 10, 0, 0, 0, time.UTC)
	c0 := at.Add(-time.Hour)
	r1 := at.Add(2 * time.Hour)
	r2 := at.Add(7 * time.Hour)
	w := func(p int, r time.Time) UsageWindow { return UsageWindow{Used: p, ResetsAt: r} }

	tests := []struct {
		name string
		held Usage
		act  NoteUsage
		want Usage
	}{
		{
			"first reading",
			Usage{},
			NoteUsage{FiveHour: w(23, r1), SevenDay: w(41, r2), At: at},
			Usage{FiveHour: w(23, r1), SevenDay: w(41, r2), Changed: at},
		},
		{
			"absent window keeps the held one",
			Usage{FiveHour: w(23, r1)},
			NoteUsage{SevenDay: w(41, r2), At: at},
			Usage{FiveHour: w(23, r1), SevenDay: w(41, r2), Changed: at},
		},
		{
			"same reset, higher",
			Usage{FiveHour: w(23, r1)},
			NoteUsage{FiveHour: w(30, r1), At: at},
			Usage{FiveHour: w(30, r1), Changed: at},
		},
		{
			"same reset, lower",
			Usage{FiveHour: w(30, r1), Changed: c0},
			NoteUsage{FiveHour: w(23, r1), At: at},
			Usage{FiveHour: w(30, r1), Changed: c0},
		},
		{
			"same reset, equal",
			Usage{FiveHour: w(30, r1), Changed: c0},
			NoteUsage{FiveHour: w(30, r1), At: at},
			Usage{FiveHour: w(30, r1), Changed: c0},
		},
		{
			"later reset, lower",
			Usage{FiveHour: w(90, r1)},
			NoteUsage{FiveHour: w(4, r2), At: at},
			Usage{FiveHour: w(4, r2), Changed: at},
		},
		{
			"earlier reset",
			Usage{FiveHour: w(4, r2), Changed: c0},
			NoteUsage{FiveHour: w(90, r1), At: at},
			Usage{FiveHour: w(4, r2), Changed: c0},
		},
		{"reset already past", Usage{}, NoteUsage{FiveHour: w(50, at.Add(-time.Minute)), At: at}, Usage{}},
		{"reset at arrival", Usage{}, NoteUsage{FiveHour: w(50, at), At: at}, Usage{}},
		{
			"one changes, one ignored",
			Usage{FiveHour: w(30, r1), SevenDay: w(41, r2)},
			NoteUsage{FiveHour: w(20, r1), SevenDay: w(45, r2), At: at},
			Usage{FiveHour: w(30, r1), SevenDay: w(45, r2), Changed: at},
		},
		{
			"nothing carried",
			Usage{FiveHour: w(30, r1), Changed: c0},
			NoteUsage{At: at},
			Usage{FiveHour: w(30, r1), Changed: c0},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if got, want := tt.act.Changes(tt.held), !reflect.DeepEqual(tt.want, tt.held); got != want {
				t.Errorf("Changes() = %v, want %v", got, want)
			}

			st := NewStore(State{Usage: tt.held})
			if err := st.Dispatch(tt.act); err != nil {
				t.Fatalf("Dispatch() error = %v", err)
			}

			if got := st.State().Usage; !reflect.DeepEqual(got, tt.want) {
				t.Errorf("Usage = %+v, want %+v", got, tt.want)
			}
		})
	}
}
