package core

import (
	"math"
	"time"
)

// FloorPercent floors f and clamps it to 0-100; NaN is 0. The clamp comes
// before the conversion, which is undefined outside the int range.
func FloorPercent(f float64) int {
	if math.IsNaN(f) {
		return 0
	}

	return int(min(max(math.Floor(f), 0), 100))
}

// UsageWindow is one rate-limit window of the account. A zero ResetsAt means
// no reading.
type UsageWindow struct {
	Used     int // percent used, 0-100
	ResetsAt time.Time
}

// Shown reports whether w holds a reading whose reset is still after now.
func (w UsageWindow) Shown(now time.Time) bool {
	return w.ResetsAt.After(now)
}

// Usage is the account's Claude usage. Changed is the arrival time of the last
// reading that changed a figure.
type Usage struct {
	FiveHour, SevenDay UsageWindow
	Changed            time.Time
}

// NoteUsage offers the windows a status line carried (zero when it carried
// none) at time At.
type NoteUsage struct {
	FiveHour, SevenDay UsageWindow
	At                 time.Time
}

func (a NoteUsage) apply(s *State) error {
	five, fiveOK := mergeWindow(s.Usage.FiveHour, a.FiveHour, a.At)
	seven, sevenOK := mergeWindow(s.Usage.SevenDay, a.SevenDay, a.At)

	if fiveOK || sevenOK {
		s.Usage = Usage{FiveHour: five, SevenDay: seven, Changed: a.At}
	}

	return nil
}

// Changes reports whether a would change u.
func (a NoteUsage) Changes(u Usage) bool {
	_, fiveOK := mergeWindow(u.FiveHour, a.FiveHour, a.At)
	_, sevenOK := mergeWindow(u.SevenDay, a.SevenDay, a.At)

	return fiveOK || sevenOK
}

// mergeWindow applies one reading to the held window. A reading is ignored
// when its reset is not after its arrival (an idle or resumed agent reruns the
// status line with old limits), when its reset is earlier than the held one,
// or when it has the same reset and no higher percent.
func mergeWindow(held, got UsageWindow, at time.Time) (UsageWindow, bool) {
	switch {
	case !got.ResetsAt.After(at):
		return held, false
	case got.ResetsAt.After(held.ResetsAt):
		return got, true
	case got.ResetsAt.Equal(held.ResetsAt) && got.Used > held.Used:
		return got, true
	}

	return held, false
}
