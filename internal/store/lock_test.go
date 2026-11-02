package store

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"golang.org/x/sys/unix"
)

func TestLock(t *testing.T) {
	dir := t.TempDir()
	unlock, err := Lock(dir)
	noErr(t, err)
	info, err := os.Stat(filepath.Join(dir, "state.lock"))
	noErr(t, err)
	eq(t, info.Mode().Perm(), os.FileMode(0o600))

	_, err = Lock(dir)
	if !errors.Is(err, ErrLocked) {
		t.Fatalf("second Lock err = %v, want ErrLocked", err)
	}

	noErr(t, unlock())

	unlock2, err := Lock(dir)
	noErr(t, err)
	noErr(t, unlock2())
	noErr(t, unlock2())
}

func TestLockMissingDir(t *testing.T) {
	_, err := Lock(filepath.Join(t.TempDir(), "missing"))
	wantErr(t, err)

	if errors.Is(err, ErrLocked) {
		t.Fatal("a missing dir is not a held lock")
	}
}

// lockFDCopy copies the descriptor that holds the lock on path, standing for a
// forked child that has not yet exec'd.
func lockFDCopy(t *testing.T, path string) int {
	t.Helper()

	var want unix.Stat_t
	noErr(t, unix.Stat(path, &want))

	var lim unix.Rlimit
	noErr(t, unix.Getrlimit(unix.RLIMIT_NOFILE, &lim))

	// Capped so a huge limit does not make the scan slow.
	for fd := 3; fd < int(min(lim.Cur, 1<<16)); fd++ {
		var st unix.Stat_t
		if unix.Fstat(fd, &st) != nil || st.Dev != want.Dev || st.Ino != want.Ino {
			continue
		}

		// Close-on-exec, so a parallel test's fork and exec cannot hand it to a child.
		dup, err := unix.FcntlInt(uintptr(fd), unix.F_DUPFD_CLOEXEC, 0)
		noErr(t, err)

		return dup
	}

	t.Fatal("lock descriptor not found")

	return -1
}

func TestLockUnlockReleasesWhileDescriptorCopyIsOpen(t *testing.T) {
	dir := t.TempDir()
	unlock, err := Lock(dir)
	noErr(t, err)

	cp := lockFDCopy(t, filepath.Join(dir, "state.lock"))
	t.Cleanup(func() { _ = unix.Close(cp) })

	noErr(t, unlock())

	unlock2, err := Lock(dir)
	if err != nil {
		t.Fatalf("Lock after unlock with a descriptor copy open: %v", err)
	}

	noErr(t, unlock2())
}
