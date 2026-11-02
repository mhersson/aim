package ui

import (
	"bytes"
	"encoding/binary"
	"errors"
	"log/slog"
	"strings"
	"testing"
	"time"

	"golang.org/x/sys/unix"
)

func TestSysLoadMemory(t *testing.T) {
	t.Parallel()

	total, err := unix.SysctlUint64("hw.memsize")
	if err != nil {
		t.Fatal(err)
	}

	pages, err := readVMPages()
	if err != nil {
		t.Fatal(err)
	}

	if pages.free == 0 || pages.external == 0 {
		t.Errorf("readVMPages = %+v, want free and file-backed pages", pages)
	}

	got, err := newSysLoad(slog.New(slog.DiscardHandler)).Sample(t.Context())
	if err != nil {
		t.Fatal(err)
	}

	if got.Mem == 0 || got.Mem >= total {
		t.Errorf("Sample memory = %d, want between 0 and %d", got.Mem, total)
	}
}

func TestLoadShare(t *testing.T) {
	t.Parallel()

	loadavg := func(one uint32, scale uint64) []byte {
		b := make([]byte, loadavgSize)
		binary.NativeEndian.PutUint32(b[0:4], one)
		binary.NativeEndian.PutUint64(b[16:24], scale)

		return b
	}

	tests := []struct {
		name  string
		raw   []byte
		cores int
		want  int
		fail  bool
	}{
		{"idle", loadavg(0, 2048), 8, 0, false},
		{"half", loadavg(4*2048, 2048), 8, 50, false},
		{"capped", loadavg(20*2048, 2048), 8, 100, false},
		{"short reply", make([]byte, 8), 8, 0, true},
		{"zero scale", loadavg(1, 0), 8, 0, true},
		{"no cores", loadavg(1, 2048), 0, 0, true},
	}

	for _, tt := range tests {
		got, err := loadShare(tt.raw, tt.cores)
		if (err != nil) != tt.fail || got != tt.want {
			t.Errorf("%s: loadShare = %d, %v, want %d (fail %v)", tt.name, got, err, tt.want, tt.fail)
		}
	}
}

// The real host_statistics: the ticks are counted and move on between reads.
func TestHostTicks(t *testing.T) {
	t.Parallel()

	read, err := openHostTicks()
	if err != nil {
		t.Fatal(err)
	}

	first, err := read()
	if err != nil {
		t.Fatal(err)
	}

	if first[0] == 0 || first[1] == 0 || first[2] == 0 {
		t.Errorf("ticks = %v, want user, system and idle ticks", first)
	}

	deadline := time.After(10 * time.Second)

	for {
		cur, err := read()
		if err != nil {
			t.Fatal(err)
		}

		if cur.since(first).total > 0 {
			return
		}

		select {
		case <-deadline:
			t.Fatalf("ticks stayed at %v", first)
		case <-time.After(10 * time.Millisecond):
		}
	}
}

// The real sampler's CPU comes from the ticks and is a percentage.
func TestSysLoadTicks(t *testing.T) {
	t.Parallel()

	var logged bytes.Buffer

	s := newSysLoad(slog.New(slog.NewTextHandler(&logged, nil)))

	for range 2 {
		got, err := s.Sample(t.Context())
		if err != nil {
			t.Fatal(err)
		}

		if got.CPU < 0 || got.CPU > 100 {
			t.Errorf("Sample CPU = %d, want 0 to 100", got.CPU)
		}
	}

	if logged.Len() != 0 {
		t.Errorf("the sampler fell back to the load average: %s", logged.String())
	}
}

// Without the ticks the CPU is the load average share, and the failure is
// logged once.
func TestSysLoadFallback(t *testing.T) {
	t.Parallel()

	var logged bytes.Buffer

	s := &sysLoad{
		log:   slog.New(slog.NewTextHandler(&logged, nil)),
		warm:  warmWait,
		ticks: func() (hostTicks, error) { return hostTicks{}, errors.New("no ticks here") },
	}

	for range 2 {
		got, err := s.Sample(t.Context())
		if err != nil {
			t.Fatal(err)
		}

		if got.CPU < 0 || got.CPU > 100 {
			t.Errorf("Sample CPU = %d, want 0 to 100", got.CPU)
		}
	}

	if n := strings.Count(logged.String(), "no ticks here"); n != 1 {
		t.Errorf("the failure was logged %d times, want once: %s", n, logged.String())
	}
}
