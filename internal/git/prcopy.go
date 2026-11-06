package git

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

const (
	prCopyBranch = "aim-pr-copy"
	prCopyMarker = "aim-used"
)

var errNoToken = errors.New("fetch pull request: no token")

// PRCopy is AIM's private repository for reading a repository's pull requests.
// It is shallow and fetches file contents only when asked, so no clone is needed.
type PRCopy struct {
	Repo   Repo
	Origin string
}

// PRCopyDir is the copy's directory for host/owner/name under root. It is not ok
// when root is empty or a segment is not a single local path element.
func PRCopyDir(root, host, owner, name string) (dir string, ok bool) {
	if root == "" {
		return "", false
	}

	for _, seg := range []string{host, owner, name} {
		if seg == "." || !filepath.IsLocal(seg) || strings.ContainsAny(seg, `/\`) {
			return "", false
		}
	}

	return filepath.Join(root, host, owner, name), true
}

// OpenPRCopy creates the copy at dir if needed, points it at origin and marks it
// used. It never calls Open to decide whether the copy exists: from a folder
// with no .git, Open finds a parent repository.
func OpenPRCopy(ctx context.Context, dir, origin string) (PRCopy, error) {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return PRCopy{}, fmt.Errorf("open pull request copy: %w", err)
	}

	marker := filepath.Join(dir, ".git", prCopyMarker)

	if _, err := os.Stat(marker); err != nil {
		if err := initPRCopy(ctx, dir, origin); err != nil {
			return PRCopy{}, err
		}
	}

	r, err := Open(ctx, dir)
	if err != nil {
		return PRCopy{}, fmt.Errorf("open pull request copy: %w", err)
	}

	want, err := filepath.EvalSymlinks(dir)
	if err != nil {
		return PRCopy{}, fmt.Errorf("open pull request copy: %w", err)
	}

	if r.Root != want {
		return PRCopy{}, fmt.Errorf("open pull request copy: %s is not a repository of its own", dir)
	}

	if err := touch(filepath.Join(r.GitDir, prCopyMarker)); err != nil {
		return PRCopy{}, fmt.Errorf("open pull request copy: %w", err)
	}

	return PRCopy{Repo: r, Origin: origin}, nil
}

// initPRCopy is idempotent, and writes the marker last so an interrupted
// creation repeats.
func initPRCopy(ctx context.Context, dir, origin string) error {
	steps := [][]string{
		{"init", "--quiet", "--template=", "--initial-branch=" + prCopyBranch},
		{"config", "remote.origin.url", origin},
		{"config", "gc.auto", "0"},
		{"config", "maintenance.auto", "false"},
	}

	for _, args := range steps {
		if _, err := run(ctx, call{dir: dir, args: args}); err != nil {
			return fmt.Errorf("create pull request copy: %w", err)
		}
	}

	if err := touch(filepath.Join(dir, ".git", prCopyMarker)); err != nil {
		return fmt.Errorf("create pull request copy: %w", err)
	}

	return nil
}

func touch(path string) error {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}

	if err := f.Close(); err != nil {
		return err
	}

	now := time.Now()

	return os.Chtimes(path, now, now)
}

// helperScript answers git's credential "get" with the token from the environment.
const helperScript = `!f() { test "$1" = get && printf "username=x-access-token\npassword=%s\n" "$AIM_PR_TOKEN"; }; f`

// netEnv applies the promisor settings through the environment only: --filter
// would write them to the config, and a later read would then fetch. The token
// lives only in AIM_PR_TOKEN, read by a helper scoped to the origin's host, so it
// reaches no other host after a redirect, no argv, no file and no user hook.
func (c PRCopy) netEnv(token string, extra ...string) []string {
	kv := [][2]string{{"credential.helper", ""}}

	if u, err := url.Parse(c.Origin); err == nil && u.Host != "" {
		kv = append(kv, [2]string{"credential." + u.Scheme + "://" + u.Host + ".helper", helperScript})
	}

	kv = append(kv,
		[2]string{"url." + c.Origin + ".insteadOf", c.Origin},
		[2]string{"core.hooksPath", "/dev/null"},
		[2]string{"remote.origin.promisor", "true"},
		[2]string{"remote.origin.partialclonefilter", "blob:none"},
	)

	for _, e := range extra {
		k, v, _ := strings.Cut(e, "=")
		kv = append(kv, [2]string{k, v})
	}

	env := []string{"LC_ALL=C", "AIM_PR_TOKEN=" + token, "GIT_CONFIG_COUNT=" + strconv.Itoa(len(kv))}

	for i, p := range kv {
		n := strconv.Itoa(i)
		env = append(env, "GIT_CONFIG_KEY_"+n+"="+p[0], "GIT_CONFIG_VALUE_"+n+"="+p[1])
	}

	return env
}

// FetchPRCopyHead fetches pull request number's head commit, depth 1, into
// refs/aim/pr/<number>/head and returns it.
func FetchPRCopyHead(ctx context.Context, c PRCopy, number int, token string) (head string, err error) {
	switch {
	case number < 1:
		return "", fmt.Errorf("fetch pull request: number %d is not positive", number)
	case token == "":
		return "", errNoToken
	}

	n := strconv.Itoa(number)
	args := append(prFetchFlags(), "--depth=1", "origin", "+refs/pull/"+n+"/head:"+prRef(number, "head"))

	if _, err := run(ctx, call{dir: c.Repo.Root, args: args, env: c.netEnv(token), net: true}); err != nil {
		if strings.Contains(err.Error(), "couldn't find remote ref refs/pull/"+n+"/head") {
			return "", fmt.Errorf("fetch pull request %d: %w", number, ErrNoPR)
		}

		return "", fmt.Errorf("fetch pull request %d: %w", number, err)
	}

	return revParse(ctx, c.Repo, prRef(number, "head")+"^{commit}")
}

// FetchPRCopyBase fetches the merge base commit, depth 1, into
// refs/aim/pr/<number>/base, then the blobs of every file the pull request
// changes, so the diff and both sides of each file can be read offline.
func FetchPRCopyBase(ctx context.Context, c PRCopy, number int, mergeBase, token string) error {
	switch {
	case token == "":
		return errNoToken
	case number < 1:
		return fmt.Errorf("fetch pull request: number %d is not positive", number)
	case !isFullOID(mergeBase):
		return fmt.Errorf("fetch pull request: merge base %q is not a full object id", mergeBase)
	}

	args := append(prFetchFlags(), "--depth=1", "origin", "+"+mergeBase+":"+prRef(number, "base"))
	if _, err := run(ctx, call{dir: c.Repo.Root, args: args, env: c.netEnv(token), net: true}); err != nil {
		return fmt.Errorf("fetch pull request %d merge base: %w", number, err)
	}

	oldTree, err := revParse(ctx, c.Repo, prRef(number, "base")+"^{tree}")
	if err != nil {
		return err
	}

	newTree, err := revParse(ctx, c.Repo, prRef(number, "head")+"^{tree}")
	if err != nil {
		return err
	}

	ids, err := changedBlobs(ctx, c.Repo, oldTree, newTree)
	if err != nil {
		return err
	}

	if len(ids) == 0 {
		return nil
	}

	// Without noop, a have that reaches a blob stops git sending it.
	args = append(prFetchFlags(), "origin", "--stdin")
	env := c.netEnv(token, "fetch.negotiationAlgorithm=noop")

	in := strings.NewReader(strings.Join(ids, "\n") + "\n")
	if _, err := run(ctx, call{dir: c.Repo.Root, args: args, env: env, stdin: in, net: true}); err != nil {
		return fmt.Errorf("fetch pull request %d files: %w", number, err)
	}

	return nil
}

// changedBlobs lists the blob ids on both sides of every changed file. With
// --raw and no rename detection git reads no blob.
func changedBlobs(ctx context.Context, r Repo, oldTree, newTree string) ([]string, error) {
	out, err := run(ctx, call{
		dir: r.Root,
		args: []string{
			"diff", "--raw", "--no-renames", "-z", "--no-abbrev", "--no-color", "--no-ext-diff", "--no-textconv",
			"--ignore-submodules=none", "--submodule=short", "-O/dev/null", oldTree, newTree,
		},
		read:  true,
		limit: listLimit,
	})
	if err != nil {
		return nil, fmt.Errorf("list changed files: %w", err)
	}

	var (
		ids  []string
		seen = map[string]bool{}
	)

	for tok := range strings.SplitSeq(string(out), "\x00") {
		if !strings.HasPrefix(tok, ":") {
			continue
		}

		f := strings.Fields(tok[1:])
		if len(f) < 5 {
			return nil, fmt.Errorf("list changed files: bad record %q", tok)
		}

		for i := range 2 {
			mode, oid := f[i], f[2+i]
			if mode == "160000" || strings.Trim(oid, "0") == "" || seen[oid] {
				continue
			}

			seen[oid] = true
			ids = append(ids, oid)
		}
	}

	return ids, nil
}

func isFullOID(s string) bool {
	if len(s) != 40 && len(s) != 64 {
		return false
	}

	for _, c := range []byte(s) {
		if c < '0' || c > '9' && c < 'a' || c > 'f' {
			return false
		}
	}

	return true
}
