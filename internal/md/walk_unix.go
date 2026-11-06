//go:build unix

package md

import (
	"os"
	"syscall"
)

// deviceOf returns the device of the folder at path; Dev is int32 on darwin.
func deviceOf(path string) (uint64, bool) {
	st, err := os.Lstat(path)
	if err != nil {
		return 0, false
	}

	sys, ok := st.Sys().(*syscall.Stat_t)
	if !ok {
		return 0, false
	}

	return uint64(sys.Dev), true //nolint:unconvert // int32 on darwin
}
