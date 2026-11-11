package github

import (
	"slices"

	"github.com/cli/go-gh/v2/pkg/auth"
	"github.com/cli/go-gh/v2/pkg/config"
)

// SignedInHosts are the hosts gh is signed in to, from gh's config and the
// token variables, normalised and sorted. It reads the config file again,
// since go-gh keeps its first read for the life of the process; a failed read
// leaves the earlier one.
func SignedInHosts() []string {
	_ = config.Reload(nil)

	return normalHosts(auth.KnownHosts())
}

func normalHosts(hosts []string) []string {
	out := make([]string, 0, len(hosts))
	for _, h := range hosts {
		out = append(out, auth.NormalizeHostname(h))
	}

	slices.Sort(out)

	return slices.Compact(out)
}
