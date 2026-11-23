//go:build !linux && !darwin

package term

import "errors"

// Foreground is not implemented on this platform.
func (p *Process) ForegroundProcess() (string, int, error) { return "", 0, errors.ErrUnsupported }
