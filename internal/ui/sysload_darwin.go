package ui

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"log/slog"
	"runtime"

	"github.com/ebitengine/purego"
	"golang.org/x/sys/unix"
)

// loadavgSize is the size of the kernel's struct loadavg: three uint32
// averages, 4 bytes of padding and an int64 scale.
const loadavgSize = 24

const (
	hostCPULoadInfo = 3 // HOST_CPU_LOAD_INFO
	kernSuccess     = 0 // KERN_SUCCESS
)

// sysLoad takes the CPU share from host_statistics' tick counters, as Activity
// Monitor does, and the memory from sysctl. Its Sample calls must not overlap;
// the tray re-arms the next one only after a result arrived.
type sysLoad struct {
	log    *slog.Logger
	ticks  func() (hostTicks, error)
	warm   func(context.Context) error // the wait before a first sample's second read
	prev   hostTicks
	have   bool
	failed bool // a tick failure was logged
}

func newSysLoad(log *slog.Logger) loadSampler {
	ticks, err := openHostTicks()
	if err != nil {
		ticks = func() (hostTicks, error) { return hostTicks{}, err }
	}

	return &sysLoad{log: log, ticks: ticks, warm: warmWait}
}

// openHostTicks loads host_statistics from libSystem through purego, since
// release builds have no cgo. mach_host_self hands out a send right on every
// call, so the reader keeps the one port it took.
func openHostTicks() (func() (hostTicks, error), error) {
	lib, err := purego.Dlopen("/usr/lib/libSystem.B.dylib", purego.RTLD_NOW|purego.RTLD_LOCAL)
	if err != nil {
		return nil, fmt.Errorf("load libSystem: %w", err)
	}

	var (
		hostSelf       func() uint32
		hostStatistics func(host uint32, flavor int32, info *hostTicks, count *uint32) int32
	)

	// Dlsym rather than RegisterLibFunc, which panics on a missing symbol.
	for _, f := range []struct {
		fptr any
		name string
	}{{&hostSelf, "mach_host_self"}, {&hostStatistics, "host_statistics"}} {
		sym, err := purego.Dlsym(lib, f.name)
		if err != nil {
			return nil, fmt.Errorf("load %s: %w", f.name, err)
		}

		purego.RegisterFunc(f.fptr, sym)
	}

	host := hostSelf()

	return func() (hostTicks, error) {
		var t hostTicks

		count := uint32(len(t))
		if kr := hostStatistics(host, hostCPULoadInfo, &t, &count); kr != kernSuccess {
			return hostTicks{}, fmt.Errorf("host_statistics: kern_return_t %d", kr)
		}

		if count != uint32(len(t)) {
			return hostTicks{}, fmt.Errorf("host_statistics: %d counters, want %d", count, len(t))
		}

		return t, nil
	}, nil
}

func (s *sysLoad) Sample(ctx context.Context) (loadSample, error) {
	cpu, err := s.cpu(ctx)
	if err != nil {
		return loadSample{}, err
	}

	total, err := unix.SysctlUint64("hw.memsize")
	if err != nil {
		return loadSample{}, fmt.Errorf("sysctl hw.memsize: %w", err)
	}

	pages, err := readVMPages()
	if err != nil {
		return loadSample{}, err
	}

	page, err := unix.SysctlUint32("hw.pagesize")
	if err != nil {
		return loadSample{}, fmt.Errorf("sysctl hw.pagesize: %w", err)
	}

	return loadSample{CPU: cpu, Mem: pages.used(total, uint64(page))}, nil
}

// cpu is the busy share since the previous sample. When the ticks cannot be
// read it is the load average share, so the tray keeps a CPU figure.
func (s *sysLoad) cpu(ctx context.Context) (int, error) {
	share, err := s.tickShare(ctx)
	if err == nil || ctx.Err() != nil {
		return share, err
	}

	if !s.failed {
		s.failed = true
		s.log.Warn("cpu ticks unreadable, the tray shows the load average", "err", err)
	}

	raw, err := unix.SysctlRaw("vm.loadavg")
	if err != nil {
		return 0, fmt.Errorf("sysctl vm.loadavg: %w", err)
	}

	return loadShare(raw, runtime.NumCPU())
}

// tickShare is the busy share of the ticks since the previous sample; a first
// sample reads twice, warmUp apart.
func (s *sysLoad) tickShare(ctx context.Context) (int, error) {
	cur, err := s.ticks()
	if err != nil {
		return 0, err
	}

	if !s.have {
		s.prev, s.have = cur, true

		if err := s.warm(ctx); err != nil {
			return 0, err
		}

		if cur, err = s.ticks(); err != nil {
			return 0, err
		}
	}

	share := cpuShare(cpuTimes{}, cur.since(s.prev))
	s.prev = cur

	return share, nil
}

func readVMPages() (vmPages, error) {
	var p vmPages

	for _, c := range []struct {
		name string
		dst  *uint64
	}{
		{"vm.page_free_count", &p.free},
		{"vm.page_pageable_external_count", &p.external},
		{"vm.page_purgeable_count", &p.purgeable},
	} {
		raw, err := unix.SysctlRaw(c.name)
		if err != nil {
			return vmPages{}, fmt.Errorf("sysctl %s: %w", c.name, err)
		}

		if *c.dst, err = vmCount(c.name, raw); err != nil {
			return vmPages{}, err
		}
	}

	return p, nil
}

// loadShare turns a struct loadavg into a percentage of cores, at most 100.
func loadShare(raw []byte, cores int) (int, error) {
	if len(raw) < loadavgSize || cores < 1 {
		return 0, errors.New("sysctl vm.loadavg: short reply")
	}

	scale := binary.NativeEndian.Uint64(raw[16:24])
	if scale == 0 {
		return 0, errors.New("sysctl vm.loadavg: zero scale")
	}

	one := float64(binary.NativeEndian.Uint32(raw[0:4])) / float64(scale)

	return min(int(one*100/float64(cores)+0.5), 100), nil
}
