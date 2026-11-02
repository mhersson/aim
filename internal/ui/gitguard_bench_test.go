//go:build bench

package ui

// installGitGuard installs nothing: the benchmarks run on real git.
func installGitGuard() (check func() string) {
	return func() string { return "" }
}
