package term

import (
	"bytes"
	"fmt"

	"golang.org/x/sys/unix"
)

// ForegroundProcess identifies the terminal's foreground process group leader by
// the base name of its argv[0] (of its script for node or bun), else its
// p_comm. p_comm names a symlink's target, and Claude's native install runs as
// a file named after its version.
func (p *Process) ForegroundProcess() (string, int, error) {
	pgrp, err := foregroundPgrp(p.pty)
	if err != nil {
		return "", 0, err
	}

	// Fails for another user's process (sudo) or one that has exited.
	if b, err := unix.SysctlRaw("kern.procargs2", pgrp); err == nil {
		if name, ok := procargsName(b); ok {
			return name, pgrp, nil
		}
	}

	name, err := kinfoComm(pgrp)
	return name, pgrp, err
}

// kinfoComm is the kinfo_proc p_comm of pid.
func kinfoComm(pid int) (string, error) {
	kp, err := unix.SysctlKinfoProc("kern.proc.pid", pid)
	if err != nil {
		return "", fmt.Errorf("read process name: %w", err)
	}

	comm := kp.Proc.P_comm[:]
	if i := bytes.IndexByte(comm, 0); i >= 0 {
		comm = comm[:i]
	}

	return string(comm), nil
}

func matchesForeground(pid int, tty unix.Stat_t) bool {
	kp, err := unix.SysctlKinfoProc("kern.proc.pid", pid)
	if err != nil {
		return false
	}
	return kp.Eproc.Tdev == tty.Rdev && int(kp.Eproc.Tpgid) == pid
}
