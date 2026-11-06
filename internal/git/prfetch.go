package git

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
)

// ErrNoPR means the remote has no such pull request.
var ErrNoPR = errors.New("git: no such pull request on the remote")

// PRRefs are the commits FetchPR stored: the pull request's head and the tip of
// its base branch.
type PRRefs struct{ Head, Base string }

func prRef(number int, name string) string {
	return "refs/aim/pr/" + strconv.Itoa(number) + "/" + name
}

// prFetchFlags make a fetch write only the refs it was asked for.
func prFetchFlags() []string {
	return []string{"fetch", "--quiet", "--no-tags", "--no-write-fetch-head", "--no-recurse-submodules", "--no-prune", "--refmap="}
}

// FetchPR fetches pull request number's head and its base branch into
// refs/aim/pr/<number>/. It writes nothing else: no remote-tracking ref,
// tag, FETCH_HEAD or submodule.
func FetchPR(ctx context.Context, r Repo, remote string, number int, base string) (PRRefs, error) {
	switch {
	case number < 1:
		return PRRefs{}, fmt.Errorf("fetch pull request: number %d is not positive", number)
	case strings.HasPrefix(base, "-"):
		return PRRefs{}, fmt.Errorf("fetch pull request: base %q looks like an option", base)
	}

	if _, err := configuredRemote(ctx, r, remote); err != nil {
		return PRRefs{}, fmt.Errorf("fetch pull request: %w", err)
	}

	if err := checkBranch(ctx, r, base); err != nil {
		return PRRefs{}, fmt.Errorf("fetch pull request: %w", err)
	}

	n := strconv.Itoa(number)
	args := append(prFetchFlags(),
		remote,
		"+refs/pull/"+n+"/head:"+prRef(number, "head"),
		"+refs/heads/"+base+":"+prRef(number, "base"),
	)

	if _, err := run(ctx, call{dir: r.Root, args: args, env: []string{"LC_ALL=C"}, net: true}); err != nil {
		if strings.Contains(err.Error(), "couldn't find remote ref refs/pull/"+n+"/head") {
			return PRRefs{}, fmt.Errorf("fetch pull request %d from %s: %w", number, remote, ErrNoPR)
		}

		return PRRefs{}, fmt.Errorf("fetch pull request %d from %s: %w", number, remote, err)
	}

	head, err := revParse(ctx, r, prRef(number, "head")+"^{commit}")
	if err != nil {
		return PRRefs{}, err
	}

	baseOID, err := revParse(ctx, r, prRef(number, "base")+"^{commit}")
	if err != nil {
		return PRRefs{}, err
	}

	return PRRefs{Head: head, Base: baseOID}, nil
}

// DeletePRRefs removes the refs FetchPR wrote in one transaction. Missing refs
// are not an error. It runs under the ref-write budget, so a deadline cannot
// kill update-ref and leave a lock file.
func DeletePRRefs(ctx context.Context, r Repo, number int) error {
	wctx, cancel, err := refWriteContext(ctx)
	if err != nil {
		return err
	}
	defer cancel()

	cmds := "delete " + prRef(number, "head") + "\ndelete " + prRef(number, "base") + "\n"

	_, err = run(wctx, call{dir: r.Root, args: []string{"update-ref", "--stdin"}, stdin: strings.NewReader(cmds)})
	if err != nil {
		return fmt.Errorf("delete pull request %d refs: %w", number, err)
	}

	return nil
}

// revParse resolves spec to an object id; exit 1 means it does not exist.
func revParse(ctx context.Context, r Repo, spec string) (string, error) {
	out, err := run(ctx, call{dir: r.Root, args: []string{"rev-parse", "--verify", "--quiet", spec}, read: true})
	if err != nil {
		return "", fmt.Errorf("resolve %s: %w", spec, err)
	}

	return strings.TrimSpace(string(out)), nil
}

type prResolved struct{ head, headTree, baseTree string }

// resolvePR reads the fetched refs: the head commit, its tree and the tree of
// the merge base with the base branch.
func resolvePR(ctx context.Context, r Repo, number int) (prResolved, error) {
	head, err := resolvePRRef(ctx, r, number, "head")
	if err != nil {
		return prResolved{}, err
	}

	base, err := resolvePRRef(ctx, r, number, "base")
	if err != nil {
		return prResolved{}, err
	}

	out, err := run(ctx, call{dir: r.Root, args: []string{"merge-base", base, head}, read: true})
	if err != nil {
		return prResolved{}, fmt.Errorf("diff pull request %d: merge base: %w", number, err)
	}

	baseTree, err := revParse(ctx, r, strings.TrimSpace(string(out))+"^{tree}")
	if err != nil {
		return prResolved{}, err
	}

	headTree, err := revParse(ctx, r, head+"^{tree}")
	if err != nil {
		return prResolved{}, err
	}

	return prResolved{head: head, headTree: headTree, baseTree: baseTree}, nil
}

// resolvePRCopy reads a copy's fetched refs: the base ref is the merge base
// itself, so no merge-base runs.
func resolvePRCopy(ctx context.Context, r Repo, number int) (prResolved, error) {
	head, err := resolvePRRef(ctx, r, number, "head")
	if err != nil {
		return prResolved{}, err
	}

	if _, err := resolvePRRef(ctx, r, number, "base"); err != nil {
		return prResolved{}, err
	}

	baseTree, err := revParse(ctx, r, prRef(number, "base")+"^{tree}")
	if err != nil {
		return prResolved{}, err
	}

	headTree, err := revParse(ctx, r, head+"^{tree}")
	if err != nil {
		return prResolved{}, err
	}

	return prResolved{head: head, headTree: headTree, baseTree: baseTree}, nil
}

func resolvePRRef(ctx context.Context, r Repo, number int, name string) (string, error) {
	oid, err := revParse(ctx, r, prRef(number, name)+"^{commit}")
	if isExitCode(err, 1) {
		return "", fmt.Errorf("diff pull request %d: %w", number, ErrNotFetched)
	}

	return oid, err
}
