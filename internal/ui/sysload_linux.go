package ui

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// sysLoad samples /proc. Its Sample calls must not overlap; the tray re-arms
// the next one only after a result arrived.
type sysLoad struct {
	root string
	prev cpuTimes
	have bool
	warm func(context.Context) error // the wait before a first sample's second read
}

func newSysLoad(*slog.Logger) loadSampler {
	return &sysLoad{root: "/proc", warm: warmWait}
}

// Sample reports the CPU share since the previous call and the memory in use.
func (s *sysLoad) Sample(ctx context.Context) (loadSample, error) {
	cur, err := s.readCPU()
	if err != nil {
		return loadSample{}, err
	}

	if !s.have {
		s.prev, s.have = cur, true

		if err := s.warm(ctx); err != nil {
			return loadSample{}, err
		}

		if cur, err = s.readCPU(); err != nil {
			return loadSample{}, err
		}
	}

	share := cpuShare(s.prev, cur)
	s.prev = cur

	meminfo, err := os.ReadFile(filepath.Join(s.root, "meminfo"))
	if err != nil {
		return loadSample{}, fmt.Errorf("read meminfo: %w", err)
	}

	mem, err := parseMemUsed(string(meminfo))
	if err != nil {
		return loadSample{}, err
	}

	return loadSample{CPU: share, Mem: mem}, nil
}

func (s *sysLoad) readCPU() (cpuTimes, error) {
	b, err := os.ReadFile(filepath.Join(s.root, "stat"))
	if err != nil {
		return cpuTimes{}, fmt.Errorf("read stat: %w", err)
	}

	return parseCPUTimes(string(b))
}

// parseCPUTimes reads the aggregate "cpu" line: user nice system idle iowait
// irq softirq steal. Guest ticks are already part of user and nice.
func parseCPUTimes(stat string) (cpuTimes, error) {
	line, _, _ := strings.Cut(stat, "\n")

	f := strings.Fields(line)
	if len(f) < 9 || f[0] != "cpu" {
		return cpuTimes{}, errors.New("parse stat: no aggregate cpu line")
	}

	var v [8]uint64

	for i := range v {
		n, err := strconv.ParseUint(f[i+1], 10, 64)
		if err != nil {
			return cpuTimes{}, fmt.Errorf("parse stat: %w", err)
		}

		v[i] = n
	}

	var total uint64
	for _, n := range v {
		total += n
	}

	return cpuTimes{busy: total - v[3] - v[4], total: total}, nil
}

// parseMemUsed is MemTotal minus MemAvailable, in bytes.
func parseMemUsed(meminfo string) (uint64, error) {
	var total, avail uint64

	var haveTotal, haveAvail bool

	for line := range strings.SplitSeq(meminfo, "\n") {
		key, rest, ok := strings.Cut(line, ":")
		if !ok || (key != "MemTotal" && key != "MemAvailable") {
			continue
		}

		f := strings.Fields(rest)
		if len(f) == 0 {
			return 0, fmt.Errorf("parse meminfo: %s has no value", key)
		}

		kb, err := strconv.ParseUint(f[0], 10, 64)
		if err != nil {
			return 0, fmt.Errorf("parse meminfo: %w", err)
		}

		if key == "MemTotal" {
			total, haveTotal = kb*1024, true
		} else {
			avail, haveAvail = kb*1024, true
		}
	}

	if !haveTotal || !haveAvail || avail > total {
		return 0, errors.New("parse meminfo: MemTotal or MemAvailable missing")
	}

	return total - avail, nil
}
