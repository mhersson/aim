package ui

import "testing"

func TestCPUShare(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		prev, cur cpuTimes
		want      int
	}{
		{"idle", cpuTimes{busy: 10, total: 100}, cpuTimes{busy: 10, total: 200}, 0},
		{"quarter", cpuTimes{busy: 0, total: 0}, cpuTimes{busy: 25, total: 100}, 25},
		{"rounds", cpuTimes{busy: 0, total: 0}, cpuTimes{busy: 1, total: 3}, 33},
		{"no time passed", cpuTimes{busy: 5, total: 10}, cpuTimes{busy: 5, total: 10}, 0},
		{"counter went back", cpuTimes{busy: 50, total: 100}, cpuTimes{busy: 10, total: 90}, 0},
	}

	for _, tt := range tests {
		if got := cpuShare(tt.prev, tt.cur); got != tt.want {
			t.Errorf("%s: cpuShare = %d, want %d", tt.name, got, tt.want)
		}
	}
}

func TestHostTicksSince(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		prev, cur hostTicks // user, system, idle, nice
		want      cpuTimes
	}{
		{"nothing passed", hostTicks{5, 6, 7, 8}, hostTicks{5, 6, 7, 8}, cpuTimes{}},
		{"user", hostTicks{}, hostTicks{10, 0, 0, 0}, cpuTimes{busy: 10, total: 10}},
		{"system", hostTicks{}, hostTicks{0, 20, 0, 0}, cpuTimes{busy: 20, total: 20}},
		{"idle", hostTicks{}, hostTicks{0, 0, 30, 0}, cpuTimes{busy: 0, total: 30}},
		{"nice", hostTicks{}, hostTicks{0, 0, 0, 40}, cpuTimes{busy: 40, total: 40}},
		{"all four", hostTicks{100, 50, 800, 0}, hostTicks{110, 55, 880, 5}, cpuTimes{busy: 20, total: 100}},
		// natural_t counters wrap at 2^32; 0x20 idle ticks passed across the wrap.
		{"idle wrapped", hostTicks{0, 0, 0xFFFFFFF0, 0}, hostTicks{0, 0, 0x10, 0}, cpuTimes{busy: 0, total: 0x20}},
		{"user wrapped", hostTicks{0xFFFFFFFF, 0, 0, 0}, hostTicks{4, 0, 4, 0}, cpuTimes{busy: 5, total: 9}},
	}

	for _, tt := range tests {
		if got := tt.cur.since(tt.prev); got != tt.want {
			t.Errorf("%s: since = %+v, want %+v", tt.name, got, tt.want)
		}
	}
}
