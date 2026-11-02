package git

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"path"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode"
)

// staleAge is how long a session's newest turn must be past for its refs to go.
const staleAge = 14 * 24 * time.Hour

// deleteChunk caps the refs of one update-ref, so each call stays well inside
// refWriteBudget: one killed after SIGTERM's grace leaves lock files behind.
const deleteChunk = 500

// ErrNotAimRef is returned by DeleteRefs for a name outside refs/aim/, a ref
// listed without its value, or a name or value with whitespace.
var ErrNotAimRef = errors.New("git: ref is not under refs/aim/, has no old value or holds whitespace")

var prRefRE = regexp.MustCompile(`^refs/aim/pr/([1-9][0-9]*)/(head|base)$`)

// Ref is a ref name and the object it pointed at when it was listed.
type Ref struct{ Name, OID string }

type turnEntry struct {
	session string
	n       int
	ref     Ref
}

type prEntry struct {
	number int
	ref    Ref
}

// AimRefs is the turn and pull request refs under refs/aim/, as listed.
type AimRefs struct {
	turns []turnEntry
	prs   []prEntry
}

// Sweep is the refs Stale picked.
type Sweep struct{ Turns, PRs []Ref }

// ListAimRefs reads the names and values of refs/aim/ in one call, without
// reading any commit. Names AIM does not own are dropped.
func ListAimRefs(ctx context.Context, r Repo) (AimRefs, error) {
	out, err := run(ctx, call{
		dir:  r.CommonDir,
		args: []string{"for-each-ref", "--format=%(refname)%00%(objectname)", refPrefix},
		read: true,
	})
	if err != nil {
		return AimRefs{}, fmt.Errorf("list aim refs: %w", err)
	}

	var l AimRefs

	for line := range strings.SplitSeq(string(out), "\n") {
		name, oid, ok := strings.Cut(line, "\x00")
		if !ok {
			continue
		}

		ref := Ref{Name: name, OID: oid}

		if rec, ok := parseTurnRef(name); ok {
			l.turns = append(l.turns, turnEntry{session: rec.session, n: rec.N, ref: ref})

			continue
		}

		if m := prRefRE.FindStringSubmatch(name); m != nil {
			if n, err := strconv.Atoi(m[1]); err == nil {
				l.prs = append(l.prs, prEntry{number: n, ref: ref})
			}
		}
	}

	return l, nil
}

// Stale picks the turn refs of sessions that are not held and whose newest turn
// in the repository, across worktrees and both layouts, is over staleAge old,
// and the pull request refs that are not shown. held gets the session as the
// ref spells it, in lower case.
func (l AimRefs) Stale(now time.Time, held func(session string) bool, shown func(number int) bool) Sweep {
	newest := make(map[string]time.Time)

	// The earlier layout's <n> is a turn counter, not milliseconds, so such a session reads as old.
	for _, t := range l.turns {
		at := time.UnixMilli(int64(t.n))
		if at.After(newest[t.session]) {
			newest[t.session] = at
		}
	}

	var s Sweep

	for _, t := range l.turns {
		if !held(t.session) && now.Sub(newest[t.session]) > staleAge {
			s.Turns = append(s.Turns, t.ref)
		}
	}

	for _, p := range l.prs {
		if !shown(p.number) {
			s.PRs = append(s.PRs, p.ref)
		}
	}

	byName := func(a, b Ref) int { return strings.Compare(a.Name, b.Name) }
	slices.SortFunc(s.Turns, byName)
	slices.SortFunc(s.PRs, byName)

	return s
}

// hasSpace reports whitespace or NUL, which would split a line of the
// update-ref script.
func hasSpace(s string) bool {
	return strings.ContainsFunc(s, func(r rune) bool { return unicode.IsSpace(r) || r == 0 })
}

// DeleteRefs removes refs in chunks of about deleteChunk, one update-ref
// transaction each and in input order, every ref only while it still holds the
// listed value. Refs of one parent directory (a PR's head and base) share a
// chunk. It refuses, and deletes nothing, when a name is outside refs/aim/, a
// value is empty, or either holds whitespace or NUL. A failed chunk does not
// stop the next; it returns the refs deleted and the chunks' errors joined.
func DeleteRefs(ctx context.Context, r Repo, refs []Ref) (int, error) {
	for _, ref := range refs {
		if !strings.HasPrefix(ref.Name, refPrefix) || ref.OID == "" || hasSpace(ref.Name) || hasSpace(ref.OID) {
			return 0, fmt.Errorf("delete %q: %w", ref.Name, ErrNotAimRef)
		}
	}

	var (
		deleted int
		errs    []error
	)

	for len(refs) > 0 {
		end := min(deleteChunk, len(refs))
		for end < len(refs) && path.Dir(refs[end].Name) == path.Dir(refs[end-1].Name) {
			end++
		}

		if err := deleteChunkOf(ctx, r, refs[:end]); err != nil {
			errs = append(errs, err)
		} else {
			deleted += end
		}

		refs = refs[end:]
	}

	return deleted, errors.Join(errs...)
}

// deleteChunkOf runs one update-ref over validated refs, under its own write budget.
func deleteChunkOf(ctx context.Context, r Repo, refs []Ref) error {
	var stdin bytes.Buffer

	for _, ref := range refs {
		fmt.Fprintf(&stdin, "delete %s %s\n", ref.Name, ref.OID)
	}

	wctx, cancel, err := refWriteContext(ctx)
	if err != nil {
		return err
	}
	defer cancel()

	if _, err := run(wctx, call{dir: r.CommonDir, args: []string{"update-ref", "--stdin"}, env: isolatedEnv(), stdin: &stdin}); err != nil {
		return fmt.Errorf("delete aim refs: %w", err)
	}

	return nil
}
