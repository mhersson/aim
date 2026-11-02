//go:build !linux && !darwin

package ui

import "log/slog"

// newSysLoad has no sampler on this platform, so the tray shows no load module.
func newSysLoad(*slog.Logger) loadSampler { return nil }
