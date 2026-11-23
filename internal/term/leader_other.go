//go:build !linux && !darwin

package term

import "errors"

// OwnsForeground is not implemented on this platform.
func OwnsForeground(string) (bool, error) { return false, errors.ErrUnsupported }

func ForegroundOwner(string, int) bool { return false }
