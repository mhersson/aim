//go:build !unix

package md

// deviceOf has no device number off Unix, so the one-filesystem rule is off.
func deviceOf(string) (uint64, bool) { return 0, false }
