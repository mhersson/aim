package main

import (
	"bufio"
	"errors"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mhersson/aim/internal/store"
)

func TestLockState(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		setup   func(t *testing.T, dir string)
		wantErr bool
	}{
		{name: "missing dir"},
		{
			name:  "existing dir",
			setup: func(t *testing.T, dir string) { mkdir(t, dir) },
		},
		{
			name: "dir is a regular file",
			setup: func(t *testing.T, dir string) {
				mkdir(t, filepath.Dir(dir))
				writeFile(t, dir, "")
			},
			wantErr: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			dir := filepath.Join(t.TempDir(), "state", "aim")
			if tt.setup != nil {
				tt.setup(t, dir)
			}

			held, unlock, err := lockState(dir)

			if tt.wantErr {
				if err == nil {
					_ = unlock()

					t.Fatal("lockState() succeeded, want an error")
				}

				return
			}

			if err != nil {
				t.Fatalf("lockState() error = %v", err)
			}

			if !held {
				t.Fatal("lockState() did not take a free lock")
			}

			assertMode(t, dir, 0o700)

			if _, err := store.Lock(dir); !errors.Is(err, store.ErrLocked) {
				t.Fatalf("store.Lock() after lockState err = %v, want ErrLocked", err)
			}

			if err := unlock(); err != nil {
				t.Fatal(err)
			}

			again, err := store.Lock(dir)
			if err != nil {
				t.Fatalf("store.Lock() after unlock err = %v", err)
			}

			if err := again(); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestLockStateHeldElsewhere(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()

	other, err := store.Lock(dir)
	if err != nil {
		t.Fatal(err)
	}

	t.Cleanup(func() { _ = other() })

	held, unlock, err := lockState(dir)
	if err != nil {
		t.Fatalf("lockState() error = %v", err)
	}

	if held {
		t.Fatal("lockState() took a lock another AIM holds")
	}

	if err := unlock(); err != nil {
		t.Errorf("unlock of a lock not held = %v, want nil", err)
	}

	if _, err := store.Lock(dir); !errors.Is(err, store.ErrLocked) {
		t.Errorf("the other AIM lost its lock: store.Lock() err = %v", err)
	}
}

func TestOpenLog(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		setup    func(t *testing.T, dir string)
		wantPrev string // content of aim.log.1; "" means it must not exist
	}{
		{name: "no previous log"},
		{
			name:     "rotates the previous log",
			setup:    func(t *testing.T, dir string) { writeFile(t, filepath.Join(dir, "aim.log"), "old") },
			wantPrev: "old",
		},
		{
			name: "replaces an older rotation",
			setup: func(t *testing.T, dir string) {
				writeFile(t, filepath.Join(dir, "aim.log"), "old")
				writeFile(t, filepath.Join(dir, "aim.log.1"), "older")
			},
			wantPrev: "old",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			dir := t.TempDir()
			if tt.setup != nil {
				tt.setup(t, dir)
			}

			f, err := openLog(dir, true, 4242)
			if err != nil {
				t.Fatalf("openLog() error = %v", err)
			}

			t.Cleanup(func() {
				if err := f.Close(); err != nil {
					t.Error(err)
				}
			})

			logPath := filepath.Join(dir, "aim.log")
			if f.Name() != logPath {
				t.Errorf("log file = %q, want %q", f.Name(), logPath)
			}

			assertMode(t, logPath, 0o600)

			if got, err := os.ReadFile(logPath); err != nil || len(got) != 0 {
				t.Errorf("new log = %q, %v; want empty", got, err)
			}

			prev, err := os.ReadFile(logPath + ".1")
			switch {
			case tt.wantPrev == "" && !os.IsNotExist(err):
				t.Errorf("aim.log.1 exists (%q, %v), want none", prev, err)
			case tt.wantPrev != "" && string(prev) != tt.wantPrev:
				t.Errorf("aim.log.1 = %q, %v; want %q", prev, err, tt.wantPrev)
			}

			if exists(filepath.Join(dir, "aim-4242.log")) {
				t.Error("the lock holder wrote a second instance's log")
			}
		})
	}
}

func TestOpenLogSecondInstance(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	holderLog := filepath.Join(dir, "aim.log")
	prevLog := holderLog + ".1"

	writeFile(t, holderLog, "the holder's run")
	writeFile(t, prevLog, "the holder's previous run")

	before := []os.FileInfo{stat(t, holderLog), stat(t, prevLog)}

	other, err := store.Lock(dir)
	if err != nil {
		t.Fatal(err)
	}

	t.Cleanup(func() { _ = other() })

	held, unlock, err := lockState(dir)
	if err != nil {
		t.Fatal(err)
	}

	t.Cleanup(func() { _ = unlock() })

	f, err := openLog(dir, held, 4242)
	if err != nil {
		t.Fatalf("openLog() error = %v", err)
	}

	t.Cleanup(func() {
		if err := f.Close(); err != nil {
			t.Error(err)
		}
	})

	ownLog := filepath.Join(dir, "aim-4242.log")
	if f.Name() != ownLog {
		t.Errorf("log file = %q, want %q", f.Name(), ownLog)
	}

	if _, err := io.WriteString(f, "the second instance's run\n"); err != nil {
		t.Fatal(err)
	}

	assertMode(t, ownLog, 0o600)

	for i, path := range []string{holderLog, prevLog} {
		if !os.SameFile(before[i], stat(t, path)) {
			t.Errorf("%s is another file now", filepath.Base(path))
		}
	}

	if got := readFile(t, holderLog); got != "the holder's run" {
		t.Errorf("aim.log = %q, want it untouched", got)
	}

	if got := readFile(t, prevLog); got != "the holder's previous run" {
		t.Errorf("aim.log.1 = %q, want it untouched", got)
	}

	first := firstLine(t, ownLog)
	if !strings.Contains(first, "level=WARN") || !strings.Contains(first, holderLog) {
		t.Errorf("first line of aim-4242.log = %q, want a warning naming %s", first, holderLog)
	}
}

func TestOpenLogSecondInstanceKeepsAnEarlierLog(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	ownLog := filepath.Join(dir, "aim-4242.log")

	writeFile(t, ownLog, "an earlier run with the same pid\n")

	f, err := openLog(dir, false, 4242)
	if err != nil {
		t.Fatalf("openLog() error = %v", err)
	}

	if err := f.Close(); err != nil {
		t.Fatal(err)
	}

	if first := firstLine(t, ownLog); first != "an earlier run with the same pid" {
		t.Errorf("first line of aim-4242.log = %q, want the earlier run kept", first)
	}
}

func TestSweepLogs(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 11, 2, 12, 0, 0, 0, time.UTC)
	day := 24 * time.Hour
	live := map[int]bool{102: true}

	tests := []struct {
		name string
		age  time.Duration
		dir  bool
		keep bool
	}{
		{name: "aim-100.log", age: 8 * day},
		{name: "aim-101.log", age: 6 * day, keep: true},
		{name: "aim-102.log", age: 30 * day, keep: true},
		{name: "aim-104.log", age: secondLogKeep, keep: true},
		{name: "aim-105.log", age: secondLogKeep + time.Second},
		{name: "aim-103.log", age: 30 * day, dir: true, keep: true},
		{name: "aim.log", age: 30 * day, keep: true},
		{name: "aim.log.1", age: 30 * day, keep: true},
		{name: "aim-007.log", age: 30 * day, keep: true},
		{name: "aim-0.log", age: 30 * day, keep: true},
		{name: "aim--5.log", age: 30 * day, keep: true},
		{name: "aim-abc.log", age: 30 * day, keep: true},
		{name: "aim-106.log.1", age: 30 * day, keep: true},
		{name: "state.json", age: 30 * day, keep: true},
	}

	dir := t.TempDir()

	for _, tt := range tests {
		path := filepath.Join(dir, tt.name)
		if tt.dir {
			mkdir(t, path)
		} else {
			writeFile(t, path, "log")
		}

		mtime := now.Add(-tt.age)
		if err := os.Chtimes(path, mtime, mtime); err != nil {
			t.Fatal(err)
		}
	}

	var asked []int

	sweepLogs(slog.New(slog.DiscardHandler), dir, now, func(pid int) bool {
		asked = append(asked, pid)

		return live[pid]
	})

	for _, tt := range tests {
		if got := exists(filepath.Join(dir, tt.name)); got != tt.keep {
			t.Errorf("%s exists = %v, want %v", tt.name, got, tt.keep)
		}
	}

	for _, pid := range asked {
		if pid <= 0 {
			t.Errorf("asked whether pid %d is alive", pid)
		}
	}
}

func mkdir(t *testing.T, path string) {
	t.Helper()

	if err := os.MkdirAll(path, 0o700); err != nil {
		t.Fatal(err)
	}
}

func stat(t *testing.T, path string) os.FileInfo {
	t.Helper()

	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}

	return info
}

func firstLine(t *testing.T, path string) string {
	t.Helper()

	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}

	defer func() { _ = f.Close() }()

	s := bufio.NewScanner(f)
	if !s.Scan() {
		t.Fatalf("%s is empty (%v)", path, s.Err())
	}

	return s.Text()
}

func assertMode(t *testing.T, path string, want os.FileMode) {
	t.Helper()

	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}

	if got := info.Mode().Perm(); got != want {
		t.Errorf("mode of %s = %o, want %o", path, got, want)
	}
}
