package term

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/creack/pty"
	"golang.org/x/sys/unix"
)

const killAfter = time.Second

// Command is a program to run on a PTY. Env is the complete child environment.
type Command struct {
	Argv []string
	Dir  string
	Env  []string
}

// Process is a child running on a pseudo terminal in its own session.
type Process struct {
	pty  *os.File
	cmd  *exec.Cmd
	done chan struct{}

	closeOnce sync.Once
	closeErr  error
}

// Start runs c without a shell. The size is applied before the child runs.
func Start(ctx context.Context, c Command, cols, rows int) (*Process, error) {
	if len(c.Argv) == 0 {
		return nil, errors.New("start pty: empty command")
	}

	cmd := exec.CommandContext(ctx, c.Argv[0], c.Argv[1:]...)
	cmd.Dir = c.Dir
	cmd.Env = c.Env

	// The cancel hook can run before Start returns, so it reads the master through an atomic.
	var master atomic.Pointer[os.File]

	cmd.Cancel = func() error {
		signalGroups(processGroups(cmd.Process.Pid, master.Load()), syscall.SIGKILL)

		return nil
	}

	f, err := startOnPTY(cmd, winsize(cols, rows))
	if err != nil {
		return nil, fmt.Errorf("start %s: %w", c.Argv[0], err)
	}

	master.Store(f)

	p := &Process{pty: f, cmd: cmd, done: make(chan struct{})}

	go func() {
		_ = cmd.Wait()

		close(p.done)
	}()

	return p, nil
}

// startOnPTY does what pty.StartWithSize does, and also puts the slave's path in
// the child's AIM_TTY so a wrapper can tell the pane's terminal from a nested one.
func startOnPTY(cmd *exec.Cmd, ws *pty.Winsize) (*os.File, error) {
	master, slave, err := pty.Open()
	if err != nil {
		return nil, fmt.Errorf("open pty: %w", err)
	}

	defer func() { _ = slave.Close() }()

	if err := pty.Setsize(master, ws); err != nil {
		_ = master.Close()

		return nil, fmt.Errorf("size pty: %w", err)
	}

	cmd.Stdin, cmd.Stdout, cmd.Stderr = slave, slave, slave
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true, Setctty: true}

	env := cmd.Env
	if env == nil {
		env = os.Environ()
	}

	cmd.Env = append(dropKeys(env, "AIM_TTY"), "AIM_TTY="+slave.Name())

	if err := cmd.Start(); err != nil {
		_ = master.Close()

		return nil, err
	}

	return master, nil
}

// Read returns io.EOF when the child closes the terminal; EIO is the normal
// end of stream on Linux.
func (p *Process) Read(b []byte) (int, error) {
	n, err := p.pty.Read(b)
	if err != nil && (errors.Is(err, syscall.EIO) || errors.Is(err, io.EOF) || errors.Is(err, os.ErrClosed)) {
		return n, io.EOF
	}

	return n, err
}

func (p *Process) Write(b []byte) (int, error) { return p.pty.Write(b) }

// Resize clamps to at least 1x1 so a collapsed pane never sends a zero size.
func (p *Process) Resize(cols, rows int) error {
	if err := pty.Setsize(p.pty, winsize(cols, rows)); err != nil {
		return fmt.Errorf("resize pty: %w", err)
	}

	return nil
}

func (p *Process) Pid() int { return p.cmd.Process.Pid }

// Done is closed after the child has been waited for.
func (p *Process) Done() <-chan struct{} { return p.done }

// Close sends SIGHUP to the leader's process group and to the terminal's
// foreground group (a job-control shell runs its commands in their own group),
// waits for the child and closes the master. After a second it escalates to
// SIGKILL and closes the master before waiting. It is idempotent.
func (p *Process) Close() error {
	p.closeOnce.Do(func() {
		// Read before any signal: once the leader dies the terminal loses its foreground group.
		groups := processGroups(p.Pid(), p.pty)

		signalGroups(groups, syscall.SIGHUP)

		select {
		case <-p.done:
		case <-time.After(killAfter):
			signalGroups(groups, syscall.SIGKILL)

			// macOS can hold a killed session leader in exit while its master is
			// open and unread. A Read in flight defers the real close, but its
			// reads keep the master drained, so the leader exits anyway.
			if err := p.pty.Close(); err != nil {
				p.closeErr = fmt.Errorf("close pty: %w", err)
			}

			<-p.done
		}

		// A descendant that ignored SIGHUP may outlive a leader that exited on it.
		signalGroups(groups, syscall.SIGKILL)

		if err := p.pty.Close(); err != nil && !errors.Is(err, os.ErrClosed) && p.closeErr == nil {
			p.closeErr = fmt.Errorf("close pty: %w", err)
		}
	})

	return p.closeErr
}

// processGroups lists the leader's group and the foreground group of f. Setsid
// makes the child a group leader, so -pgid reaches its descendants.
func processGroups(pgid int, f *os.File) []int {
	groups := []int{pgid}

	if f == nil {
		return groups
	}

	if fg, err := foregroundPgrp(f); err == nil && fg > 0 && fg != pgid {
		groups = append(groups, fg)
	}

	return groups
}

func signalGroups(groups []int, sig syscall.Signal) {
	for _, g := range groups {
		_ = syscall.Kill(-g, sig)
	}
}

// foregroundPgrp reads through SyscallConn because File.Fd would put the
// master into blocking mode.
func foregroundPgrp(f *os.File) (int, error) {
	rc, err := f.SyscallConn()
	if err != nil {
		return 0, fmt.Errorf("pty conn: %w", err)
	}

	var (
		pgrp     int
		ioctlErr error
	)

	if err := rc.Control(func(fd uintptr) {
		pgrp, ioctlErr = unix.IoctlGetInt(int(fd), unix.TIOCGPGRP)
	}); err != nil {
		return 0, fmt.Errorf("pty control: %w", err)
	}

	if ioctlErr != nil {
		return 0, fmt.Errorf("foreground process group: %w", ioctlErr)
	}

	return pgrp, nil
}

func winsize(cols, rows int) *pty.Winsize {
	return &pty.Winsize{Rows: uint16(clampSize(rows)), Cols: uint16(clampSize(cols))}
}

func clampSize(n int) int {
	return min(max(n, 1), 1<<16-1)
}
