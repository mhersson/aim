package term

import (
	"bytes"
	"context"
	"errors"
	"io"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/creack/pty"
)

const testDeadline = 2 * time.Second

func startSh(t *testing.T, script string, env []string, cols, rows int) *Process {
	t.Helper()

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	t.Cleanup(cancel)

	p, err := Start(ctx, Command{Argv: []string{"/bin/sh", "-c", script}, Dir: t.TempDir(), Env: env}, cols, rows)
	if err != nil {
		t.Fatalf("Start: %v", err)
	}

	t.Cleanup(func() { _ = p.Close() })

	return p
}

// readUntil reads until the output contains want or the deadline passes.
func readUntil(t *testing.T, p *Process, want string) string {
	t.Helper()

	type result struct {
		out string
		err error
	}

	done := make(chan result, 1)

	go func() {
		var out bytes.Buffer

		buf := make([]byte, 4096)

		for !strings.Contains(out.String(), want) {
			n, err := p.Read(buf)
			out.Write(buf[:n])

			if err != nil {
				done <- result{out.String(), err}

				return
			}
		}

		done <- result{out.String(), nil}
	}()

	select {
	case r := <-done:
		if !strings.Contains(r.out, want) {
			t.Fatalf("output %q lacks %q (err %v)", r.out, want, r.err)
		}

		return r.out
	case <-time.After(testDeadline):
		t.Fatalf("timed out waiting for %q", want)
	}

	return ""
}

func TestStartReadExit(t *testing.T) {
	p := startSh(t, "printf hello", PaneEnv(nil), 80, 24)

	var out bytes.Buffer

	buf := make([]byte, 64)

	for {
		n, err := p.Read(buf)
		out.Write(buf[:n])

		if err != nil {
			if !errors.Is(err, io.EOF) {
				t.Fatalf("Read err = %v, want io.EOF", err)
			}

			break
		}
	}

	if out.String() != "hello" {
		t.Errorf("output = %q", out.String())
	}

	select {
	case <-p.Done():
	case <-time.After(testDeadline):
		t.Fatal("Done not closed")
	}
}

func TestStartSizeAndResize(t *testing.T) {
	p := startSh(t, "stty size; sleep 5", PaneEnv(nil), 80, 24)

	readUntil(t, p, "24 80")

	if err := p.Resize(100, 30); err != nil {
		t.Fatalf("Resize: %v", err)
	}

	ws, err := pty.GetsizeFull(p.pty)
	if err != nil {
		t.Fatal(err)
	}

	if ws.Rows != 30 || ws.Cols != 100 {
		t.Errorf("size = %dx%d, want 100x30", ws.Cols, ws.Rows)
	}

	if err := p.Resize(0, 0); err != nil {
		t.Fatalf("Resize(0,0): %v", err)
	}

	if ws, _ = pty.GetsizeFull(p.pty); ws.Rows != 1 || ws.Cols != 1 {
		t.Errorf("clamped size = %dx%d, want 1x1", ws.Cols, ws.Rows)
	}
}

func TestStartSetsPaneTTY(t *testing.T) {
	p := startSh(t, `echo "A=$AIM_TTY"; echo "B=$(tty)"; sleep 5`, []string{"AIM_TTY=/dev/bogus"}, 80, 24)

	out := readUntil(t, p, "B=/")
	got := map[string]string{}

	for line := range strings.SplitSeq(out, "\n") {
		if k, v, ok := strings.Cut(strings.TrimSpace(line), "="); ok {
			got[k] = v
		}
	}

	if got["A"] == "" || got["A"] != got["B"] {
		t.Errorf("AIM_TTY = %q, tty = %q; want the same slave path", got["A"], got["B"])
	}
}

func TestEnvReachesChild(t *testing.T) {
	env := PaneEnv([]string{"TMUX=x", "TERM=dumb"})
	p := startSh(t, "echo $TERM $COLORTERM ${TMUX:-none}", env, 80, 24)

	readUntil(t, p, "xterm-256color truecolor none")
}

func TestCwd(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("cwd lookup is Linux-only")
	}

	p := startSh(t, "cd /tmp && sleep 5", PaneEnv(nil), 80, 24)

	deadline := time.Now().Add(testDeadline)

	for {
		got, err := p.Cwd()
		if err == nil && got == "/tmp" {
			return
		}

		if time.Now().After(deadline) {
			t.Fatalf("Cwd = %q, %v; want /tmp", got, err)
		}

		time.Sleep(20 * time.Millisecond)
	}
}

func TestCloseKillsGroup(t *testing.T) {
	baseline := runtime.NumGoroutine()

	p := startSh(t, `trap "" HUP; sleep 30 & echo $!; wait`, PaneEnv(nil), 80, 24)

	out := readUntil(t, p, "\n")
	childPid := 0

	for _, c := range strings.TrimSpace(out) {
		if c < '0' || c > '9' {
			t.Fatalf("unexpected pid line %q", out)
		}

		childPid = childPid*10 + int(c-'0')
	}

	pid := p.Pid()

	if err := p.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	if err := p.Close(); err != nil {
		t.Fatalf("second Close: %v", err)
	}

	deadline := time.Now().Add(testDeadline)

	for _, target := range []int{pid, childPid} {
		for syscall.Kill(target, 0) == nil {
			if time.Now().After(deadline) {
				t.Fatalf("pid %d still alive", target)
			}

			time.Sleep(20 * time.Millisecond)
		}
	}

	for runtime.NumGoroutine() > baseline {
		if time.Now().After(deadline) {
			t.Fatalf("goroutines = %d, baseline %d", runtime.NumGoroutine(), baseline)
		}

		time.Sleep(20 * time.Millisecond)
	}
}

// macOS can hold a killed session leader in exit while its master is open and
// unread, so Close must close the master before it waits for the leader.
func TestCloseReturnsForManyKilledGroups(t *testing.T) {
	const n = 24

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	t.Cleanup(cancel)

	procs := make([]*Process, n)
	pids := make([]int, 0, 2*n)

	for i := range procs {
		cmd := Command{Argv: []string{"/bin/sh", "-c", `trap "" HUP; sleep 30 & echo $!; wait`}, Dir: t.TempDir(), Env: PaneEnv(nil)}

		// Not startSh: its cleanup Close would block on a Close that hangs here.
		p, err := Start(ctx, cmd, 80, 24)
		if err != nil {
			t.Fatalf("Start: %v", err)
		}

		procs[i] = p

		child, err := strconv.Atoi(strings.TrimSpace(readUntil(t, p, "\n")))
		if err != nil {
			t.Fatalf("child pid: %v", err)
		}

		pids = append(pids, p.Pid(), child)
	}

	var wg sync.WaitGroup

	for _, p := range procs {
		wg.Go(func() { _ = p.Close() })
	}

	done := make(chan struct{})

	go func() {
		wg.Wait()
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("Close still blocked after 10 s")
	}

	deadline := time.Now().Add(testDeadline)

	for _, pid := range pids {
		for syscall.Kill(pid, 0) == nil {
			if time.Now().After(deadline) {
				t.Fatalf("pid %d still alive", pid)
			}

			time.Sleep(20 * time.Millisecond)
		}
	}
}

func TestCloseKillsForegroundGroup(t *testing.T) {
	// set -m puts sleep in its own foreground group, as an interactive shell does.
	// The leader dies on SIGHUP while the foreground sleep ignores it.
	p := startSh(t, `set -m; sh -c 'trap "" HUP; exec sleep 30'`, PaneEnv(nil), 80, 24)

	var fg int

	deadline := time.Now().Add(testDeadline)

	for {
		got, err := foregroundPgrp(p.pty)
		if err == nil && got > 0 && got != p.Pid() {
			fg = got

			break
		}

		if time.Now().After(deadline) {
			t.Fatalf("no separate foreground group (fg %d, err %v)", got, err)
		}

		time.Sleep(20 * time.Millisecond)
	}

	if err := p.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	deadline = time.Now().Add(testDeadline)

	for syscall.Kill(fg, 0) == nil {
		if time.Now().After(deadline) {
			t.Fatalf("foreground process %d still alive", fg)
		}

		time.Sleep(20 * time.Millisecond)
	}
}

func TestCancelKillsGroup(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	p, err := Start(ctx, Command{Argv: []string{"/bin/sh", "-c", `trap "" HUP; sleep 30 & echo $!; wait`}, Dir: t.TempDir(), Env: PaneEnv(nil)}, 80, 24)
	if err != nil {
		t.Fatal(err)
	}

	t.Cleanup(func() { _ = p.Close() })

	childPid := 0

	for _, c := range strings.TrimSpace(readUntil(t, p, "\n")) {
		childPid = childPid*10 + int(c-'0')
	}

	cancel()

	deadline := time.Now().Add(testDeadline)

	for syscall.Kill(childPid, 0) == nil {
		if time.Now().After(deadline) {
			t.Fatalf("pid %d survived cancel", childPid)
		}

		time.Sleep(20 * time.Millisecond)
	}
}
