package ui

import (
	"context"
	"fmt"
	"time"
)

// warmUp is the gap between the two tick reads of a sampler's first sample,
// so the first share is not an average since boot.
const warmUp = 250 * time.Millisecond

// warmWait is a sampler's wait before a first sample's second read.
func warmWait(ctx context.Context) error {
	if !sleepCtx(ctx, warmUp) {
		return fmt.Errorf("warm up: %w", ctx.Err())
	}

	return nil
}

// cpuTimes are cumulative CPU ticks: those spent busy and all of them.
type cpuTimes struct{ busy, total uint64 }

// cpuShare is the busy percentage between two readings, rounded; a counter
// that went back or did not move reads as 0.
func cpuShare(prev, cur cpuTimes) int {
	if cur.total <= prev.total || cur.busy < prev.busy {
		return 0
	}

	dTotal, dBusy := cur.total-prev.total, cur.busy-prev.busy

	return int((dBusy*100 + dTotal/2) / dTotal)
}

// hostTicks are macOS's HOST_CPU_LOAD_INFO counters summed over the cores, in
// cpu_ticks order: user, system, idle, nice. They build on every platform so
// their test runs on Linux too.
type hostTicks [4]uint32

// since is the ticks spent between two readings. Each counter is a natural_t
// that wraps at 2^32, so the deltas are taken modulo 2^32.
func (t hostTicks) since(prev hostTicks) cpuTimes {
	user, system, idle, nice := uint64(t[0]-prev[0]), uint64(t[1]-prev[1]), uint64(t[2]-prev[2]), uint64(t[3]-prev[3])

	return cpuTimes{busy: user + system + nice, total: user + system + idle + nice}
}
