//go:build !linux && !darwin

package term

import "errors"

// LeadsForeground is not implemented on this platform.
func LeadsForeground() (bool, error) { return false, errors.ErrUnsupported }
