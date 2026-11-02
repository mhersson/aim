package store

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"

	"golang.org/x/sys/unix"
)

// ErrLocked reports that another AIM holds the state lock.
var ErrLocked = errors.New("state is locked by another AIM")

// Lock takes an exclusive, non-blocking flock on dir/state.lock. The lock lasts
// until unlock is called or the process exits; the caller keeps unlock reachable
// until then, because a dropped unlock lets the finalizer close the file and
// release the lock.
func Lock(dir string) (unlock func() error, err error) {
	path := filepath.Join(dir, "state.lock")

	f, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE, 0o600)
	if err != nil {
		return nil, fmt.Errorf("opening lock file: %w", err)
	}

	for {
		err = unix.Flock(int(f.Fd()), unix.LOCK_EX|unix.LOCK_NB)
		if !errors.Is(err, unix.EINTR) {
			break
		}
	}

	if err != nil {
		_ = f.Close()

		if errors.Is(err, unix.EWOULDBLOCK) {
			return nil, ErrLocked
		}

		return nil, fmt.Errorf("locking %s: %w", path, err)
	}

	var once sync.Once

	return func() error {
		var uerr error

		once.Do(func() {
			// Close alone keeps the lock while a forked child still holds a copy
			// of the descriptor; LOCK_UN releases the shared description.
			for {
				err := unix.Flock(int(f.Fd()), unix.LOCK_UN)
				if !errors.Is(err, unix.EINTR) {
					if err != nil {
						uerr = fmt.Errorf("unlocking %s: %w", path, err)
					}

					break
				}
			}

			if cerr := f.Close(); uerr == nil && cerr != nil {
				uerr = cerr
			}
		})

		return uerr
	}, nil
}
