package term

import (
	"bytes"
	"fmt"
	"os"
	"strconv"
	"strings"

	"golang.org/x/sys/unix"
)

// ForegroundProcess returns the terminal's foreground leader name and PID.
func (p *Process) ForegroundProcess() (string, int, error) {
	pgrp, err := foregroundPgrp(p.pty)
	if err != nil {
		return "", 0, err
	}

	dir := "/proc/" + strconv.Itoa(pgrp)

	b, err := os.ReadFile(dir + "/comm")
	if err != nil {
		return "", 0, fmt.Errorf("read comm: %w", err)
	}

	name := strings.TrimSpace(string(b))
	if name == "codex" || name == "node" || name == "bun" {
		if cmd, err := os.ReadFile(dir + "/cmdline"); err == nil {
			argv := bytes.Split(bytes.TrimSuffix(cmd, []byte{0}), []byte{0})
			if n, ok := foregroundName(argv); ok {
				name = n
			}
		}
	}

	return name, pgrp, nil
}

func matchesForeground(pid int, tty unix.Stat_t) bool {
	stat, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/stat")
	if err != nil {
		return false
	}
	// comm is parenthesized and can itself contain spaces or parentheses.
	_, after, ok := bytes.CutLast(stat, []byte{')'})
	if !ok {
		return false
	}

	fields := strings.Fields(string(after))
	if len(fields) < 6 {
		return false
	}

	device, err := strconv.ParseInt(fields[4], 10, 64)
	if err != nil || device == 0 {
		return false
	}

	group, err := strconv.Atoi(fields[5])

	return err == nil && group == pid && device >= 0 && uint64(device) == tty.Rdev
}
