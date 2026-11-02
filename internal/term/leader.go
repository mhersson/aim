//go:build linux || darwin

package term

import (
	"fmt"
	"os"

	"golang.org/x/sys/unix"
)

// OwnsForeground reports whether this process leads the foreground process group
// of both its /dev/tty and the terminal at paneTTY. A program on a pty nested in
// the pane leads its own pty's group, but the pane's terminal is led by whoever
// started that pty. /dev/tty is a node of its own (not the pty's device), so
// the terminals are compared by their groups, not by device numbers.
func OwnsForeground(paneTTY string) (bool, error) {
	self := os.Getpid()

	for _, path := range []string{"/dev/tty", paneTTY} {
		pgrp, err := foregroundOf(path)
		if err != nil {
			return false, err
		}

		if pgrp != self {
			return false, nil
		}
	}

	return true, nil
}

func foregroundOf(path string) (int, error) {
	fd, err := unix.Open(path, unix.O_RDONLY|unix.O_NOCTTY|unix.O_CLOEXEC, 0)
	if err != nil {
		return 0, fmt.Errorf("open %s: %w", path, err)
	}

	defer func() { _ = unix.Close(fd) }()

	pgrp, err := unix.IoctlGetInt(fd, unix.TIOCGPGRP)
	if err != nil {
		return 0, fmt.Errorf("foreground process group of %s: %w", path, err)
	}

	return pgrp, nil
}
