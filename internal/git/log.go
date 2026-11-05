package git

import (
	"context"
	"fmt"
	"strings"
)

const logLimit = 250

// Commit is one entry of a branch's history.
type Commit struct{ OID, Subject, Body string }

// Log returns the commits in base..HEAD, oldest first. When there are more than
// 250 it keeps the newest 250.
func Log(ctx context.Context, r Repo, base string) ([]Commit, error) {
	if err := verifyBase(ctx, r, "git log", base); err != nil {
		return nil, err
	}

	// -n applies before --reverse, so the newest commits are the ones kept.
	out, err := run(ctx, call{
		dir:  r.Root,
		args: []string{"log", "-z", "--reverse", "--format=%H%n%s%n%b", "-n", fmt.Sprint(logLimit), base + "..HEAD", "--"},
		read: true,
	})
	if err != nil {
		return nil, fmt.Errorf("list commits since %q: %w", base, err)
	}

	var commits []Commit

	for rec := range strings.SplitSeq(string(out), "\x00") {
		rec = strings.TrimLeft(rec, "\n")
		if rec == "" {
			continue
		}

		oid, rest, _ := strings.Cut(rec, "\n")
		subject, body, _ := strings.Cut(rest, "\n")
		commits = append(commits, Commit{OID: oid, Subject: subject, Body: strings.TrimRight(body, "\n")})
	}

	return commits, nil
}

// statFiles caps the files DiffStat lists; git sums up the rest in one line.
const statFiles = 200

// DiffStat returns git diff --stat of what HEAD changes since its merge base
// with base, at most statFiles files.
func DiffStat(ctx context.Context, r Repo, base string) (string, error) {
	if err := verifyBase(ctx, r, "git diff", base); err != nil {
		return "", err
	}

	out, err := run(ctx, call{
		dir:  r.Root,
		args: []string{"diff", "--no-color", "--stat=200", fmt.Sprintf("--stat-count=%d", statFiles), base + "...HEAD", "--"},
		read: true,
	})
	if err != nil {
		return "", fmt.Errorf("diff summary since %q: %w", base, err)
	}

	return string(out), nil
}

// verifyBase refuses a base that git would read as an option and resolves it
// to a commit; a missing base fails with rev-parse's exit status 1.
func verifyBase(ctx context.Context, r Repo, what, base string) error {
	if strings.HasPrefix(base, "-") {
		return fmt.Errorf("%s: invalid base %q", what, base)
	}

	if _, err := run(ctx, call{dir: r.Root, args: []string{"rev-parse", "--verify", "--quiet", base + "^{commit}"}, read: true}); err != nil {
		return fmt.Errorf("resolve base %q: %w", base, err)
	}

	return nil
}
