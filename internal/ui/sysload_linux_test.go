package ui

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

const procStatFixture = `cpu  4705 150 1120 16250 520 30 45 10 0 0
cpu0 1200 40 300 4000 130 10 20 5 0 0
intr 12345
`

const meminfoFixture = `MemTotal:       16384000 kB
MemFree:         2048000 kB
MemAvailable:    6144000 kB
Buffers:          512000 kB
`

func TestParseCPUTimes(t *testing.T) {
	t.Parallel()

	got, err := parseCPUTimes(procStatFixture)
	if err != nil {
		t.Fatal(err)
	}

	// total = user+nice+system+idle+iowait+irq+softirq+steal; busy excludes idle and iowait.
	want := cpuTimes{busy: 4705 + 150 + 1120 + 30 + 45 + 10, total: 4705 + 150 + 1120 + 16250 + 520 + 30 + 45 + 10}
	if got != want {
		t.Errorf("parseCPUTimes = %+v, want %+v", got, want)
	}

	for _, bad := range []string{"", "intr 1\n", "cpu  1 2\n", "cpu  a b c d e f g h\n"} {
		if _, err := parseCPUTimes(bad); err == nil {
			t.Errorf("parseCPUTimes(%q) succeeded, want an error", bad)
		}
	}
}

func TestParseMemUsed(t *testing.T) {
	t.Parallel()

	got, err := parseMemUsed(meminfoFixture)
	if err != nil {
		t.Fatal(err)
	}

	if want := uint64(16384000-6144000) * 1024; got != want {
		t.Errorf("parseMemUsed = %d, want %d", got, want)
	}

	for _, bad := range []string{"", "MemTotal: 5 kB\n", "MemAvailable: 5 kB\n", "MemTotal: x kB\nMemAvailable: 1 kB\n"} {
		if _, err := parseMemUsed(bad); err == nil {
			t.Errorf("parseMemUsed(%q) succeeded, want an error", bad)
		}
	}
}

// The sampler reads two /proc snapshots and reports the share between them.
func TestSysLoadSample(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	write := func(name, text string) {
		t.Helper()

		if err := os.WriteFile(filepath.Join(root, name), []byte(text), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	write("stat", "cpu  100 0 100 800 0 0 0 0 0 0\n")
	write("meminfo", meminfoFixture)

	s := &sysLoad{root: root, warm: func(context.Context) error {
		write("stat", "cpu  150 0 150 900 0 0 0 0 0 0\n")

		return nil
	}}

	got, err := s.Sample(context.Background())
	if err != nil {
		t.Fatal(err)
	}

	// busy 100 of 200 ticks between the snapshots.
	if got.CPU != 50 || got.Mem != uint64(16384000-6144000)*1024 {
		t.Errorf("Sample = %+v", got)
	}

	// The next sample is measured against the previous one.
	write("stat", "cpu  150 0 150 1100 0 0 0 0 0 0\n")

	got, err = s.Sample(context.Background())
	if err != nil || got.CPU != 0 {
		t.Errorf("second Sample = %+v, %v, want 0%%", got, err)
	}

	write("stat", "garbage\n")

	if _, err := s.Sample(context.Background()); err == nil {
		t.Error("Sample on a broken /proc/stat succeeded")
	}
}
