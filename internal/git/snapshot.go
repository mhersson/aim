package git

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"
)

const (
	refPrefix      = "refs/aim/"
	turnsPrefix    = refPrefix + "turns/"
	maxSkippedTags = 20

	// refWriteBudget bounds writing a ref and its commit once the tree exists, so the
	// caller's deadline cannot stop update-ref halfway and risk leaving its locks.
	refWriteBudget = 2 * time.Second
)

// ErrBadSession means a session id is not a UUID.
var ErrBadSession = errors.New("git: session id is not a UUID")

var (
	sessionRE = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)
	turnRefRE = regexp.MustCompile(`^refs/aim/turns/([0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12})/([0-9a-f]{16})/([0-9]+)/(start|end)$`)
	// legacyRefRE is the earlier layout, which is pruned but no longer read.
	legacyRefRE = regexp.MustCompile(`^refs/aim/([0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12})/([0-9]+)$`)
)

// refWriteContext returns the context for a ref write: the caller's error when
// it is already done, else one that survives its cancellation for refWriteBudget.
func refWriteContext(ctx context.Context) (context.Context, context.CancelFunc, error) {
	if err := ctx.Err(); err != nil {
		return nil, nil, err
	}

	c, cancel := context.WithTimeout(context.WithoutCancel(ctx), refWriteBudget)

	return c, cancel, nil
}

// SnapshotOptions tune one snapshot.
type SnapshotOptions struct {
	TempDir      string // where the temporary index is created
	IndexCache   string // keeps a refreshed index copy per worktree; empty = none
	MaxUntracked int64  // bytes; 0 = 50 MiB, negative = no cap
	Unchanged    bool   // reuse the worktree's latest tree in the session without staging
	Turn         int    // the turn's number; 0 takes one more than the session's highest in the repository
	Ends         int    // a turn of this worktree to end with the same tree when it is open; 0 for none
}

// Turn is one turn of a session in one worktree.
type Turn struct {
	N          int
	Commit     string // the start
	Tree       string
	Missing    bool   // the start is a "no snapshot" marker
	EndCommit  string // "" while the turn is open
	EndTree    string
	EndMissing bool // the end is a marker
	Current    bool // N is the session's highest turn number in the repository
	Skipped    []string
	Time       time.Time // the start's committer time
}

// ErrTurnNotOpen means the turn to end has no start in this worktree, or has an
// end already.
var ErrTurnNotOpen = errors.New("git: the turn is not open in this worktree")

// turnKey names a worktree in turn refs: the first 16 hex digits of the SHA-256
// of its root.
func turnKey(root string) string {
	sum := sha256.Sum256([]byte(root))

	return hex.EncodeToString(sum[:])[:16]
}

func turnRef(session, key string, n int, end bool) string {
	leaf := "start"
	if end {
		leaf = "end"
	}

	return fmt.Sprintf("%s%s/%s/%d/%s", turnsPrefix, session, key, n, leaf)
}

// Snapshot records the working tree of the worktree containing dir as the start
// of turn opts.Turn of the session. The user's index, refs, stash and files are
// only read. The returned Turn is the start alone: only Turns fills Current and
// the end fields.
func Snapshot(ctx context.Context, dir, session string, opts SnapshotOptions) (Turn, error) {
	session, err := normalizeSession(session)
	if err != nil {
		return Turn{}, err
	}

	r, err := Open(ctx, dir)
	if err != nil {
		return Turn{}, err
	}

	mine, _, err := sessionTurns(ctx, r, session)
	if err != nil {
		return Turn{}, err
	}

	var wt WorkTree

	reused := false

	if opts.Unchanged {
		for _, t := range slices.Backward(mine) {
			if tree, ok := latestTree(t); ok {
				wt = WorkTree{Tree: tree, Skipped: t.Skipped}
				reused = true

				break
			}
		}
	}

	if !reused {
		wt, err = WorkingTree(ctx, r, TreeOptions{TempDir: opts.TempDir, IndexCache: opts.IndexCache, MaxUntracked: opts.MaxUntracked})
		if err != nil {
			return Turn{}, fmt.Errorf("snapshot %s: %w", r.Root, err)
		}
	}

	return writeStart(ctx, r, session, opts.Turn, opts.Ends, wt.Tree, wt.Skipped, false)
}

// latestTree is the tree a turn left the worktree in: its end, else its start.
// A turn with a marker for its start or end says nothing reliable, since the
// start of an ended turn is stale by then.
func latestTree(t Turn) (string, bool) {
	switch {
	case t.Missing, t.EndMissing:
	case t.EndCommit != "":
		return t.EndTree, true
	default:
		return t.Tree, true
	}

	return "", false
}

// EndTurn records the working tree as the end of turn n of the worktree
// containing dir. A turn with no start there, a start marker or an end is
// ErrTurnNotOpen.
func EndTurn(ctx context.Context, dir, session string, n int, opts SnapshotOptions) (Turn, error) {
	session, err := normalizeSession(session)
	if err != nil {
		return Turn{}, err
	}

	r, err := Open(ctx, dir)
	if err != nil {
		return Turn{}, err
	}

	mine, _, err := sessionTurns(ctx, r, session)
	if err != nil {
		return Turn{}, err
	}

	t, ok := openTurn(mine, n)
	if !ok || t.Missing {
		return Turn{}, fmt.Errorf("end turn %d: %w", n, ErrTurnNotOpen)
	}

	tree := t.Tree

	if !opts.Unchanged {
		wt, err := WorkingTree(ctx, r, TreeOptions{TempDir: opts.TempDir, IndexCache: opts.IndexCache, MaxUntracked: opts.MaxUntracked})
		if err != nil {
			return Turn{}, fmt.Errorf("snapshot %s: %w", r.Root, err)
		}

		tree = wt.Tree
	}

	return writeEnd(ctx, r, session, t, tree, false)
}

func openTurn(turns []Turn, n int) (Turn, bool) {
	for _, t := range turns {
		if t.N == n && t.EndCommit == "" {
			return t, true
		}
	}

	return Turn{}, false
}

// MarkMissing records that a turn has no snapshot: a commit of the empty tree
// with the trailer "Snapshot: none". As a start it is turn n (0 for the next),
// so later turns keep their numbers; as an end (end true) it closes the open
// turn n.
func MarkMissing(ctx context.Context, dir, session string, n int, end bool) (Turn, error) {
	session, err := normalizeSession(session)
	if err != nil {
		return Turn{}, err
	}

	r, err := Open(ctx, dir)
	if err != nil {
		return Turn{}, err
	}

	var t Turn

	if end {
		mine, _, err := sessionTurns(ctx, r, session)
		if err != nil {
			return Turn{}, err
		}

		var ok bool
		if t, ok = openTurn(mine, n); !ok {
			return Turn{}, fmt.Errorf("mark turn %d: %w", n, ErrTurnNotOpen)
		}
	}

	wctx, cancel, err := refWriteContext(ctx)
	if err != nil {
		return Turn{}, err
	}
	defer cancel()

	// mktree writes the empty tree in whatever hash the repository uses.
	out, err := run(wctx, call{dir: r.Root, args: []string{"mktree"}, stdin: bytes.NewReader(nil)})
	if err != nil {
		return Turn{}, fmt.Errorf("mark missing: %w", err)
	}

	tree := strings.TrimSpace(string(out))

	if end {
		return writeEnd(ctx, r, session, t, tree, true)
	}

	return writeStart(ctx, r, session, n, 0, tree, nil, true)
}

// turnWrite is one ref to create: the start or end of turn n.
type turnWrite struct {
	n       int
	end     bool
	tree    string
	skipped []string
	none    bool
}

// writeStart commits tree as the start of turn n and creates its ref; n 0
// takes one more than the session's highest in the repository, and a ref that
// already exists (another writer took the number) is then retried once. When
// ends names an open turn of this worktree, the same tree ends it, in the same
// transaction.
func writeStart(ctx context.Context, r Repo, session string, n, ends int, tree string, skipped []string, none bool) (Turn, error) {
	computed := n == 0

	for attempt := 0; ; attempt++ {
		mine, highest, err := sessionTurns(ctx, r, session)
		if err != nil {
			return Turn{}, err
		}

		num := n
		if computed {
			num = highest + 1
		}

		writes := []turnWrite{{n: num, tree: tree, skipped: skipped, none: none}}

		if prev, ok := openTurn(mine, ends); ok && ends != 0 && !prev.Missing && prev.N != num {
			writes = append(writes, turnWrite{n: ends, end: true, tree: tree, skipped: skipped})
		}

		now := time.Now().Truncate(time.Second)

		commits, err := writeTurns(ctx, r, session, writes, now)
		if err == nil {
			return Turn{N: num, Commit: commits[0], Tree: tree, Missing: none, Skipped: skipped, Time: now}, nil
		}

		if !computed || attempt > 0 || ctx.Err() != nil {
			return Turn{}, err
		}

		updateErr := err

		_, again, err := sessionTurns(ctx, r, session)
		if err != nil {
			return Turn{}, fmt.Errorf("%w (then %w)", updateErr, err)
		}

		if again+1 == num {
			return Turn{}, updateErr
		}
	}
}

// writeEnd commits tree as the end of the open turn t and creates its ref.
func writeEnd(ctx context.Context, r Repo, session string, t Turn, tree string, none bool) (Turn, error) {
	now := time.Now().Truncate(time.Second)

	commits, err := writeTurns(ctx, r, session, []turnWrite{{n: t.N, end: true, tree: tree, skipped: t.Skipped, none: none}}, now)
	if err != nil {
		return Turn{}, err
	}

	t.EndCommit, t.EndTree, t.EndMissing = commits[0], tree, none

	return t, nil
}

// writeTurns commits every write and creates all their refs in one transaction.
// It returns the commits in order.
func writeTurns(ctx context.Context, r Repo, session string, writes []turnWrite, at time.Time) ([]string, error) {
	parent, err := headCommit(ctx, r.Root)
	if err != nil && !errors.Is(err, errUnborn) {
		return nil, err
	}

	wctx, cancel, err := refWriteContext(ctx)
	if err != nil {
		return nil, err
	}
	defer cancel()

	key := turnKey(r.Root)
	commits := make([]string, len(writes))

	var stdin bytes.Buffer

	for i, w := range writes {
		commits[i], err = commitTurn(wctx, r, session, w, parent, at)
		if err != nil {
			return nil, err
		}

		fmt.Fprintf(&stdin, "create %s %s\n", turnRef(session, key, w.n, w.end), commits[i])
	}

	_, err = run(wctx, call{
		dir:   r.Root,
		args:  []string{"update-ref", "-m", "aim turn", "--stdin"},
		env:   isolatedEnv(),
		stdin: &stdin,
	})
	if err != nil {
		return nil, fmt.Errorf("write turn refs: %w", err)
	}

	return commits, nil
}

func commitTurn(ctx context.Context, r Repo, session string, w turnWrite, parent string, at time.Time) (string, error) {
	var msg strings.Builder

	fmt.Fprintf(&msg, "aim turn %d", w.n)

	if w.end {
		msg.WriteString(" end")
	}

	fmt.Fprintf(&msg, "\n\nSession: %s\nWorktree: %s\n", session, quoteValue(r.Root))

	if w.none {
		msg.WriteString("Snapshot: none\n")
	}

	if len(w.skipped) > 0 {
		fmt.Fprintf(&msg, "Skipped-Count: %d\n", len(w.skipped))

		for _, p := range w.skipped[:min(len(w.skipped), maxSkippedTags)] {
			fmt.Fprintf(&msg, "Skipped: %s\n", quoteValue(p))
		}
	}

	date := strconv.FormatInt(at.Unix(), 10) + " +0000"
	env := append(isolatedEnv(),
		"GIT_AUTHOR_NAME=AIM", "GIT_COMMITTER_NAME=AIM",
		"GIT_AUTHOR_EMAIL=aim@localhost", "GIT_COMMITTER_EMAIL=aim@localhost",
		"GIT_AUTHOR_DATE="+date, "GIT_COMMITTER_DATE="+date,
	)

	args := []string{"commit-tree", "--no-gpg-sign", w.tree}
	if parent != "" {
		args = append(args, "-p", parent)
	}

	out, err := run(ctx, call{dir: r.Root, args: args, env: env, stdin: strings.NewReader(msg.String())})
	if err != nil {
		return "", fmt.Errorf("commit turn %d: %w", w.n, err)
	}

	return strings.TrimSpace(string(out)), nil
}

func normalizeSession(session string) (string, error) {
	if !sessionRE.MatchString(session) {
		return "", fmt.Errorf("session %q: %w", session, ErrBadSession)
	}

	return strings.ToLower(session), nil
}

// quoteValue makes s safe as a one-line trailer value: Go-quoted when it has
// characters a trailer would mangle (newlines, edge spaces, quotes, bad UTF-8).
func quoteValue(s string) string {
	q := strconv.Quote(s)
	if s != "" && q == `"`+s+`"` && strings.TrimSpace(s) == s {
		return s
	}

	return q
}

func unquoteValue(s string) string {
	if strings.HasPrefix(s, `"`) {
		if u, err := strconv.Unquote(s); err == nil {
			return u
		}
	}

	return s
}

const turnFormat = "%(refname)%00%(objectname)%00%(tree)%00%(committerdate:unix)%00" +
	"%(trailers:key=Snapshot,valueonly,separator=%x1f)%00" +
	"%(trailers:key=Skipped,valueonly,separator=%x1f)%00" +
	"%(trailers:key=Worktree,valueonly,separator=%x1f)"

type turnRecord struct {
	Turn

	ref      string
	session  string
	key      string // "" for the earlier layout
	worktree string
	end      bool
}

// listRefs reads every turn ref under the patterns in one for-each-ref call,
// both layouts.
func listRefs(ctx context.Context, r Repo, patterns ...string) ([]turnRecord, error) {
	out, err := run(ctx, call{
		dir:  r.CommonDir,
		args: append([]string{"for-each-ref", "--format=" + turnFormat}, patterns...),
		read: true,
	})
	if err != nil {
		return nil, fmt.Errorf("list turns: %w", err)
	}

	var recs []turnRecord

	for line := range strings.SplitSeq(string(out), "\n") {
		f := strings.Split(line, "\x00")
		if len(f) != 7 {
			continue
		}

		rec, ok := parseTurnRef(f[0])
		if !ok {
			continue
		}

		secs, _ := strconv.ParseInt(f[3], 10, 64)
		rec.Commit, rec.Tree, rec.Time = f[1], f[2], time.Unix(secs, 0)
		rec.Missing = strings.TrimSpace(f[4]) == "none"
		rec.worktree = unquoteValue(strings.TrimSpace(f[6]))

		if f[5] != "" {
			for p := range strings.SplitSeq(f[5], "\x1f") {
				rec.Skipped = append(rec.Skipped, unquoteValue(p))
			}
		}

		recs = append(recs, rec)
	}

	return recs, nil
}

func parseTurnRef(ref string) (turnRecord, bool) {
	if m := turnRefRE.FindStringSubmatch(ref); m != nil {
		n, err := strconv.Atoi(m[3])
		if err != nil {
			return turnRecord{}, false
		}

		return turnRecord{N: n, ref: ref, session: m[1], key: m[2], end: m[4] == "end"}, true
	}

	if m := legacyRefRE.FindStringSubmatch(ref); m != nil {
		n, err := strconv.Atoi(m[2])
		if err != nil {
			return turnRecord{}, false
		}

		return turnRecord{N: n, ref: ref, session: m[1]}, true
	}

	return turnRecord{}, false
}

// sessionTurns lists the session's turns in r's worktree in ascending order,
// and the session's highest turn number in the repository. The earlier layout
// is not read.
func sessionTurns(ctx context.Context, r Repo, session string) ([]Turn, int, error) {
	recs, err := listRefs(ctx, r, turnsPrefix+session+"/")
	if err != nil {
		return nil, 0, err
	}

	key := turnKey(r.Root)
	highest := 0
	byN := map[int]*Turn{}

	var ends []turnRecord

	for _, rec := range recs {
		highest = max(highest, rec.N)

		switch {
		case rec.key != key:
		case rec.end:
			ends = append(ends, rec)
		default:
			t := rec.Turn
			byN[rec.N] = &t
		}
	}

	for _, e := range ends {
		if t := byN[e.N]; t != nil {
			t.EndCommit, t.EndTree, t.EndMissing = e.Commit, e.Tree, e.Missing
		}
	}

	turns := make([]Turn, 0, len(byN))
	for _, t := range byN {
		t.Current = t.N == highest
		turns = append(turns, *t)
	}

	slices.SortFunc(turns, func(a, b Turn) int { return a.N - b.N })

	return turns, highest, nil
}

// Turns lists the session's turns in r's worktree in ascending order.
func Turns(ctx context.Context, r Repo, session string) ([]Turn, error) {
	session, err := normalizeSession(session)
	if err != nil {
		return nil, err
	}

	turns, _, err := sessionTurns(ctx, r, session)

	return turns, err
}

// OpenTurn is a worktree whose turn has no end yet.
type OpenTurn struct {
	Root string // from the start's Worktree trailer
	N    int
}

// OpenTurns lists, in every worktree of r's repository, the session's open
// turns at its highest number there; start markers are left out.
func OpenTurns(ctx context.Context, r Repo, session string) ([]OpenTurn, error) {
	session, err := normalizeSession(session)
	if err != nil {
		return nil, err
	}

	recs, err := listRefs(ctx, r, turnsPrefix+session+"/")
	if err != nil {
		return nil, err
	}

	highest := 0
	for _, rec := range recs {
		highest = max(highest, rec.N)
	}

	ended := map[string]bool{}

	for _, rec := range recs {
		if rec.end && rec.N == highest {
			ended[rec.key] = true
		}
	}

	var open []OpenTurn

	for _, rec := range recs {
		if rec.N == highest && !rec.end && !rec.Missing && !ended[rec.key] && rec.worktree != "" {
			open = append(open, OpenTurn{Root: rec.worktree, N: rec.N})
		}
	}

	slices.SortFunc(open, func(a, b OpenTurn) int { return strings.Compare(a.Root, b.Root) })

	return open, nil
}

// PruneWorktree deletes every turn ref of worktree, in both layouts, and
// returns how many it removed. worktree is a worktree root as Repo.Root gives
// it, the form the commits record; a symlinked spelling does not match.
func PruneWorktree(ctx context.Context, r Repo, worktree string) (int, error) {
	recs, err := listRefs(ctx, r, refPrefix)
	if err != nil {
		return 0, err
	}

	want := filepath.Clean(worktree)
	key := turnKey(want)

	return deleteRefs(ctx, r, recs, func(rec turnRecord) bool {
		return rec.key == key || filepath.Clean(rec.worktree) == want
	})
}

// PruneSession deletes every turn ref of the session, in both layouts, and
// returns how many.
func PruneSession(ctx context.Context, r Repo, session string) (int, error) {
	session, err := normalizeSession(session)
	if err != nil {
		return 0, err
	}

	recs, err := listRefs(ctx, r, refPrefix+session+"/", turnsPrefix+session+"/")
	if err != nil {
		return 0, err
	}

	return deleteRefs(ctx, r, recs, func(turnRecord) bool { return true })
}

// deleteRefs removes the matching refs through DeleteRefs, each only if it
// still points at the commit that was read.
func deleteRefs(ctx context.Context, r Repo, recs []turnRecord, match func(turnRecord) bool) (int, error) {
	var refs []Ref

	for _, rec := range recs {
		if match(rec) {
			refs = append(refs, Ref{Name: rec.ref, OID: rec.Commit})
		}
	}

	n, err := DeleteRefs(ctx, r, refs)
	if err != nil {
		return n, fmt.Errorf("prune turns: %w", err)
	}

	return n, nil
}
