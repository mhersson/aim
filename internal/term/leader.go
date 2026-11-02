//go:build linux || darwin

package term

import (
	"fmt"
	"os"

	"golang.org/x/sys/unix"
)

// LeadsForeground reports whether this process leads its terminal's foreground
// process group, as the first command of a shell job does.
func LeadsForeground() (bool, error) {
	fd, err := unix.Open("/dev/tty", unix.O_RDONLY|unix.O_NOCTTY|unix.O_CLOEXEC, 0)
	if err != nil {
		return false, fmt.Errorf("open /dev/tty: %w", err)
	}

	pgrp, err := unix.IoctlGetInt(fd, unix.TIOCGPGRP)
	_ = unix.Close(fd)

	if err != nil {
		return false, fmt.Errorf("foreground process group: %w", err)
	}

	return pgrp == os.Getpid(), nil
}
