package ui

import (
	"cmp"
	"context"
	"crypto/sha1" //nolint:gosec // git's own object ids
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"maps"
	"os"
	"path"
	"path/filepath"
	"reflect"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mhersson/aim/internal/git"
)

var _ Git = (*fakeGit)(nil)

// gitCall is one call the app made to the fake. Args are the call's
// arguments after the repository: the branch for Push, the branch and the
// tip for DeleteBranch, the path for RemoveWorktree, the worktree for
// PruneWorktree, the number for DeletePRRefs and FetchPR, "name oid" per ref
// for DeleteRefs, the branch, base and root style ("inside" or "sibling")
// for CreateWorktree. The engine's calls carry the source: "branch" and the
// base, "turn", the session and the position, or "pr" and the number; Update
// adds the batch's paths.
type gitCall struct {
	Op, Root string
	Args     []string
}

type fakeHold struct {
	ch   chan struct{}
	once sync.Once
}

type fakeCommit struct {
	oid, subject, body, parent string
	tree                       fakeTree
}

type fakeTurn struct {
	root            string
	n               int
	tree, endTree   fakeTree
	missing, ended  bool
	startOID, endID string
}

// fakeCommon is what the worktrees of one repository share.
type fakeCommon struct {
	f        *fakeGit
	main     string
	trees    []*fakeRepo // the main checkout, then the linked ones in creation order
	branches map[string]string
	remotes  map[string]*fakeRemote
	tracking map[string]string // "<remote>/<branch>"
	upstream map[string]string // branch to remote
	deflt    string
	ignores  []string
	aimRefs  map[string]string
	turns    map[string][]*fakeTurn // by lower-case session
}

// fakeGit is an in-process model of git for the UI tests: commits and refs
// live in memory, the working tree is what is on disk.
type fakeGit struct {
	mu      sync.Mutex
	watches *fakeWatchSet

	roots   map[string]*fakeRepo
	commits map[string]*fakeCommit
	blobs   map[string]string
	seq     int
	results map[git.Source]git.Result

	failAll  map[string]error
	failRoot map[string]error // op + NUL + root
	holds    map[string]*fakeHold
	log      []gitCall
	running  map[string]int
	peaks    map[string]int
}

func newFakeGit(t *testing.T) *fakeGit {
	t.Helper()

	return &fakeGit{
		watches:  newFakeWatchSet(),
		roots:    map[string]*fakeRepo{},
		commits:  map[string]*fakeCommit{},
		blobs:    map[string]string{},
		results:  map[git.Source]git.Result{},
		failAll:  map[string]error{},
		failRoot: map[string]error{},
		holds:    map[string]*fakeHold{},
		running:  map[string]int{},
		peaks:    map[string]int{},
	}
}

func gitOf(f *fakeGit) func(*Options) {
	return func(o *Options) {
		o.Git = f
		o.Watches = f.watches
	}
}

// fakeRepo is one worktree of a fake repository.
type fakeRepo struct {
	f      *fakeGit
	t      *testing.T
	c      *fakeCommon
	root   string
	gitDir string
	linked bool

	cur      string // the checked-out branch
	detached bool
	detOID   string

	locked, isPrunable      bool
	lockReason, pruneReason string
}

// fakeRemote is a remote of a fake repository and the repository behind it.
type fakeRemote struct {
	f        *fakeGit
	c        *fakeCommon
	name     string
	url      string
	pushURLs []string
	branches map[string]string
	prs      map[int]string
}

func (r *fakeRepo) Root() string { return r.root }

func (r *fakeRepo) Repo() git.Repo {
	return git.Repo{Root: r.root, GitDir: r.gitDir, CommonDir: filepath.Join(r.c.main, ".git")}
}

func (r *fakeRepo) headOID() string {
	if r.detached {
		return r.detOID
	}

	return r.c.branches[r.cur]
}

func fakeOID(parts ...string) string {
	h := sha1.New() //nolint:gosec // git's own object ids
	for _, p := range parts {
		fmt.Fprintf(h, "%d:%s\n", len(p), p)
	}

	return hex.EncodeToString(h.Sum(nil))
}

// begin logs a call, waits out its hold, applies its scripted failure, and
// returns with the fake locked. The returned func unlocks and ends the call.
func (f *fakeGit) begin(ctx context.Context, op, root string, args ...string) (func(), error) {
	f.mu.Lock()
	f.log = append(f.log, gitCall{Op: op, Root: root, Args: slices.Clone(args)})
	f.running[op]++
	f.peaks[op] = max(f.peaks[op], f.running[op])
	h := f.holds[op]
	err := cmp.Or(f.failRoot[op+"\x00"+root], f.failAll[op])

	f.mu.Unlock()

	leave := func() {
		f.mu.Lock()
		f.running[op]--
		f.mu.Unlock()
	}

	if h != nil {
		select {
		case <-h.ch:
		case <-ctx.Done():
			leave()

			return nil, ctx.Err()
		}
	}

	if err != nil {
		leave()

		return nil, err
	}

	f.mu.Lock()

	return func() {
		f.mu.Unlock()
		leave()
	}, nil
}

func (f *fakeGit) enterRepo(ctx context.Context, op string, gr git.Repo, args ...string) (*fakeRepo, func(), error) {
	end, err := f.begin(ctx, op, gr.Root, args...)
	if err != nil {
		return nil, nil, err
	}

	r, ok := f.roots[gr.Root]
	if !ok {
		end()

		return nil, nil, fmt.Errorf("%s %s: %w", op, gr.Root, git.ErrNotRepo)
	}

	return r, end, nil
}

// Scripting.

func (f *fakeGit) edit(fn func()) {
	f.mu.Lock()
	defer f.mu.Unlock()

	fn()
}

func (f *fakeGit) fail(op string, err error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	if err == nil {
		delete(f.failAll, op)

		return
	}

	f.failAll[op] = err
}

func (f *fakeGit) failAt(op, root string, err error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	if err == nil {
		delete(f.failRoot, op+"\x00"+root)

		return
	}

	f.failRoot[op+"\x00"+root] = err
}

func (f *fakeGit) hold(op string) (release func()) {
	h := &fakeHold{ch: make(chan struct{})}

	f.mu.Lock()
	f.holds[op] = h
	f.mu.Unlock()

	return func() {
		h.once.Do(func() { close(h.ch) })

		f.mu.Lock()
		defer f.mu.Unlock()

		if f.holds[op] == h {
			delete(f.holds, op)
		}
	}
}

func (f *fakeGit) calls(op string) []gitCall {
	f.mu.Lock()
	defer f.mu.Unlock()

	var out []gitCall

	for _, c := range f.log {
		if c.Op == op {
			out = append(out, c)
		}
	}

	return out
}

func (f *fakeGit) count(op string) int { return len(f.calls(op)) }

func (f *fakeGit) inflight(op string) int {
	f.mu.Lock()
	defer f.mu.Unlock()

	return f.running[op]
}

func (f *fakeGit) peak(op string) int {
	f.mu.Lock()
	defer f.mu.Unlock()

	return f.peaks[op]
}

func (f *fakeGit) result(src git.Source, res git.Result) {
	f.mu.Lock()
	defer f.mu.Unlock()

	f.results[src] = res
}

// Repositories.

func (f *fakeGit) repo(t *testing.T, name string) *fakeRepo {
	t.Helper()

	if name == "" {
		name = "repo"
	}

	return f.repoAt(t, filepath.Join(t.TempDir(), name))
}

func (f *fakeGit) repoAt(t *testing.T, dir string) *fakeRepo {
	t.Helper()

	if err := os.MkdirAll(filepath.Join(dir, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}

	root, err := filepath.EvalSymlinks(dir)
	if err != nil {
		t.Fatal(err)
	}

	c := &fakeCommon{
		f: f, main: root, deflt: "main",
		branches: map[string]string{}, remotes: map[string]*fakeRemote{}, tracking: map[string]string{},
		upstream: map[string]string{}, aimRefs: map[string]string{}, turns: map[string][]*fakeTurn{},
	}
	r := &fakeRepo{f: f, t: t, c: c, root: root, gitDir: filepath.Join(root, ".git"), cur: "main"}
	c.trees = []*fakeRepo{r}

	f.mu.Lock()
	f.roots[root] = r
	f.mu.Unlock()

	return r
}

func (r *fakeRepo) worktree(t *testing.T, dir, branch string) *fakeRepo {
	t.Helper()

	r.f.mu.Lock()
	defer r.f.mu.Unlock()

	head := r.headOID()
	if head == "" {
		t.Fatalf("worktree %s: %s has no commit", dir, r.root)
	}

	nr, err := r.f.addWorktreeL(t, r.c, dir, branch, head)
	if err != nil {
		t.Fatal(err)
	}

	return nr
}

// addWorktreeL makes a linked worktree on branch at oid and writes its files.
func (f *fakeGit) addWorktreeL(t *testing.T, c *fakeCommon, dir, branch, oid string) (*fakeRepo, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}

	root, err := filepath.EvalSymlinks(dir)
	if err != nil {
		return nil, err
	}

	for p, content := range f.commits[oid].tree {
		full := filepath.Join(root, filepath.FromSlash(p))
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			return nil, err
		}

		if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
			return nil, err
		}
	}

	gitDir := filepath.Join(c.main, ".git", "worktrees", filepath.Base(root))
	if err := os.WriteFile(filepath.Join(root, ".git"), []byte("gitdir: "+gitDir+"\n"), 0o644); err != nil {
		return nil, err
	}

	c.branches[branch] = oid
	nr := &fakeRepo{f: f, t: t, c: c, root: root, gitDir: gitDir, linked: true, cur: branch}
	c.trees = append(c.trees, nr)
	f.roots[root] = nr

	return nr, nil
}

func (r *fakeRepo) commit(msg string) string {
	r.f.mu.Lock()
	defer r.f.mu.Unlock()

	r.f.seq++

	return r.commitL(fakeOID("commit", strconv.Itoa(r.f.seq), r.headOID(), msg), msg)
}

func (r *fakeRepo) commitAs(oid, msg string) {
	r.f.mu.Lock()
	defer r.f.mu.Unlock()

	r.commitL(oid, msg)
}

func (r *fakeRepo) commitL(oid, msg string) string {
	subject, rest, _ := strings.Cut(msg, "\n")
	tree := r.f.readDiskL(r)

	for _, content := range tree {
		r.f.blobs[blobID(content)] = content
	}

	r.f.commits[oid] = &fakeCommit{oid: oid, subject: subject, body: strings.TrimSpace(rest), parent: r.headOID(), tree: tree}

	if r.detached {
		r.detOID = oid
	} else {
		r.c.branches[r.cur] = oid
	}

	return oid
}

func (r *fakeRepo) branch(name string) {
	r.f.mu.Lock()
	defer r.f.mu.Unlock()

	if head := r.headOID(); head != "" {
		r.c.branches[name] = head
	}

	r.cur, r.detached = name, false
}

func (r *fakeRepo) switchTo(name string) {
	r.f.mu.Lock()
	defer r.f.mu.Unlock()

	if _, ok := r.c.branches[name]; !ok {
		r.t.Fatalf("switchTo %s: no such branch", name)
	}

	r.cur, r.detached = name, false
}

func (r *fakeRepo) detach() {
	r.f.mu.Lock()
	defer r.f.mu.Unlock()

	r.detOID, r.detached = r.headOID(), true
}

func (r *fakeRepo) reset(oid string) {
	r.f.mu.Lock()
	defer r.f.mu.Unlock()

	if _, ok := r.f.commits[oid]; !ok {
		r.t.Fatalf("reset to %s: no such commit", oid)
	}

	if r.detached {
		r.detOID = oid
	} else {
		r.c.branches[r.cur] = oid
	}
}

func (r *fakeRepo) ignore(patterns ...string) {
	r.f.mu.Lock()
	defer r.f.mu.Unlock()

	r.c.ignores = append(r.c.ignores, patterns...)
}

func (r *fakeRepo) setDefault(branch string) {
	r.f.mu.Lock()
	defer r.f.mu.Unlock()

	r.c.deflt = branch
}

func (r *fakeRepo) remote(name, url string) *fakeRemote {
	r.f.mu.Lock()
	defer r.f.mu.Unlock()

	rm := &fakeRemote{f: r.f, c: r.c, name: name, url: url, branches: map[string]string{}, prs: map[int]string{}}
	r.c.remotes[name] = rm

	return rm
}

func (rm *fakeRemote) setPushURLs(urls ...string) {
	rm.f.mu.Lock()
	defer rm.f.mu.Unlock()

	rm.pushURLs = urls
}

func (rm *fakeRemote) setBranch(name, oid string) {
	rm.f.mu.Lock()
	defer rm.f.mu.Unlock()

	if oid == "" {
		delete(rm.branches, name)

		return
	}

	rm.branches[name] = oid
}

func (rm *fakeRemote) pr(number int, head string) {
	rm.f.mu.Lock()
	defer rm.f.mu.Unlock()

	rm.prs[number] = head
}

func (r *fakeRepo) track(remote, branch, oid string) {
	r.f.mu.Lock()
	defer r.f.mu.Unlock()

	if oid == "" {
		delete(r.c.tracking, remote+"/"+branch)

		return
	}

	r.c.tracking[remote+"/"+branch] = oid
}

func (r *fakeRepo) setUpstream(branch, remote string) {
	r.f.mu.Lock()
	defer r.f.mu.Unlock()

	r.c.upstream[branch] = remote
}

func (r *fakeRepo) lock(reason string) {
	r.f.mu.Lock()
	defer r.f.mu.Unlock()

	r.locked, r.lockReason = true, reason
}

func (r *fakeRepo) prunable(reason string) {
	r.f.mu.Lock()
	defer r.f.mu.Unlock()

	r.isPrunable, r.pruneReason = true, reason
}

func (r *fakeRepo) aimRef(name, oid string) {
	r.f.mu.Lock()
	defer r.f.mu.Unlock()

	r.c.aimRefs[name] = oid
}

func turnKey(root string) string {
	sum := sha256.Sum256([]byte(root))

	return hex.EncodeToString(sum[:])[:16]
}

func fakeTurnRef(session, root string, n int, end bool) string {
	leaf := "start"
	if end {
		leaf = "end"
	}

	return fmt.Sprintf("refs/aim/turns/%s/%s/%d/%s", session, turnKey(root), n, leaf)
}

func (r *fakeRepo) turn(session string) {
	r.f.mu.Lock()
	defer r.f.mu.Unlock()

	session = strings.ToLower(session)
	disk := r.f.readDiskL(r)

	var last *fakeTurn

	for _, t := range r.c.turns[session] {
		if t.root == r.root {
			last = t
		}
	}

	if last != nil && !last.ended && !last.missing {
		last.endTree, last.ended = disk, true
		last.endID = fakeOID("turn end", session, r.root, strconv.Itoa(last.n))
		r.c.aimRefs[fakeTurnRef(session, r.root, last.n, true)] = last.endID
	}

	r.startTurnL(session, disk, false)
}

func (r *fakeRepo) missingTurn(session string) {
	r.f.mu.Lock()
	defer r.f.mu.Unlock()

	r.startTurnL(strings.ToLower(session), fakeTree{}, true)
}

func (r *fakeRepo) startTurnL(session string, tree fakeTree, missing bool) {
	n := 1

	for _, t := range r.c.turns[session] {
		n = max(n, t.n+1)
	}

	t := &fakeTurn{root: r.root, n: n, tree: tree, missing: missing, startOID: fakeOID("turn start", session, r.root, strconv.Itoa(n))}
	r.c.turns[session] = append(r.c.turns[session], t)
	r.c.aimRefs[fakeTurnRef(session, r.root, n, false)] = t.startOID
}

// turnsOfL is the session's turns in r's worktree as internal/git lists them.
func (f *fakeGit) turnsOfL(r *fakeRepo, session string) []git.Turn {
	session = strings.ToLower(session)
	highest := 0

	for _, t := range r.c.turns[session] {
		highest = max(highest, t.n)
	}

	var out []git.Turn

	for _, t := range r.c.turns[session] {
		if t.root != r.root {
			continue
		}

		gt := git.Turn{N: t.n, Commit: t.startOID, Tree: treeID(t.tree), Missing: t.missing, Current: t.n == highest}
		if t.ended {
			gt.EndCommit, gt.EndTree = t.endID, treeID(t.endTree)
		}

		out = append(out, gt)
	}

	return out
}

func (f *fakeGit) fakeTurnOf(r *fakeRepo, session string, n int) *fakeTurn {
	for _, t := range r.c.turns[strings.ToLower(session)] {
		if t.root == r.root && t.n == n {
			return t
		}
	}

	return nil
}

// Working tree and history.

func (f *fakeGit) readDiskL(r *fakeRepo) fakeTree {
	out := fakeTree{}

	_ = filepath.WalkDir(r.root, func(p string, d fs.DirEntry, err error) error {
		if err != nil || p == r.root {
			return nil
		}

		rel, _ := filepath.Rel(r.root, p)
		rel = filepath.ToSlash(rel)

		switch {
		case d.Name() == ".git":
			if d.IsDir() {
				return fs.SkipDir
			}

			return nil
		case d.IsDir():
			if _, other := f.roots[p]; other || ignored(r.c.ignores, rel, true) {
				return fs.SkipDir
			}

			return nil
		case !d.Type().IsRegular() || ignored(r.c.ignores, rel, false):
			return nil
		}

		if b, err := os.ReadFile(p); err == nil { //nolint:gosec // a test's own tree under t.TempDir()
			out[rel] = string(b)
		}

		return nil
	})

	return out
}

func (f *fakeGit) headTree(r *fakeRepo) fakeTree {
	if c, ok := f.commits[r.headOID()]; ok {
		return c.tree
	}

	return fakeTree{}
}

// ignored applies info/exclude lines to rel and its parent directories.
func ignored(patterns []string, rel string, isDir bool) bool {
	parts := strings.Split(rel, "/")

	for i := range parts {
		prefix := strings.Join(parts[:i+1], "/")
		dir := i < len(parts)-1 || isDir

		for _, p := range patterns {
			if ignoreMatch(p, prefix, dir) {
				return true
			}
		}
	}

	return false
}

func ignoreMatch(pattern, rel string, isDir bool) bool {
	if pattern == "" || strings.HasPrefix(pattern, "#") || strings.HasPrefix(pattern, "!") {
		return false
	}

	dirOnly := strings.HasSuffix(pattern, "/")
	pattern = strings.TrimSuffix(pattern, "/")

	if dirOnly && !isDir {
		return false
	}

	target := path.Base(rel)

	if strings.Contains(pattern, "/") {
		pattern, target = strings.TrimPrefix(pattern, "/"), rel
	}

	ok, _ := path.Match(pattern, target)

	return ok
}

func (c *fakeCommon) resolve(name string) (string, bool) {
	for _, n := range []string{name, strings.TrimPrefix(name, "refs/heads/")} {
		if oid, ok := c.branches[n]; ok {
			return oid, true
		}

		if oid, ok := c.tracking[n]; ok {
			return oid, true
		}
	}

	if _, ok := c.f.commits[name]; ok {
		return name, true
	}

	return "", false
}

// chain is oid and its first parents, newest first.
func (f *fakeGit) chain(oid string) []string {
	var out []string

	for c, ok := f.commits[oid]; ok; c, ok = f.commits[c.parent] {
		out = append(out, c.oid)
	}

	return out
}

func (f *fakeGit) ancestors(oid string) map[string]bool {
	set := map[string]bool{}
	for _, o := range f.chain(oid) {
		set[o] = true
	}

	return set
}

// mergeBase is the first commit on b's chain that a's chain holds.
func (f *fakeGit) mergeBase(a, b string) (string, bool) {
	set := f.ancestors(a)

	for _, o := range f.chain(b) {
		if set[o] {
			return o, true
		}
	}

	return "", false
}

func (f *fakeGit) countNotIn(tip string, exclude map[string]bool) int {
	n := 0

	for _, o := range f.chain(tip) {
		if !exclude[o] {
			n++
		}
	}

	return n
}

func (f *fakeGit) changesL(r *fakeRepo) int {
	if _, err := os.Stat(r.root); err != nil {
		return 0
	}

	head, disk := f.headTree(r), f.readDiskL(r)
	n := 0

	for p, v := range disk {
		if hv, ok := head[p]; !ok || hv != v {
			n++
		}
	}

	for p := range head {
		if _, ok := disk[p]; !ok {
			n++
		}
	}

	return n
}

// rootOfL is the deepest registered root that holds dir.
func (f *fakeGit) rootOfL(dir string) *fakeRepo {
	var best *fakeRepo

	for root, r := range f.roots {
		if (dir == root || strings.HasPrefix(dir, root+string(filepath.Separator))) && (best == nil || len(root) > len(best.root)) {
			best = r
		}
	}

	return best
}

func (f *fakeGit) openL(dir string) (*fakeRepo, error) {
	abs, err := filepath.Abs(dir)
	if err != nil {
		return nil, fmt.Errorf("open %s: %w", dir, err)
	}

	info, err := os.Stat(abs)
	if err != nil {
		return nil, fmt.Errorf("open %s: chdir %s: %w", dir, abs, err)
	}

	if !info.IsDir() {
		return nil, fmt.Errorf("open %s: chdir %s: not a directory", dir, abs)
	}

	resolved, err := filepath.EvalSymlinks(abs)
	if err != nil {
		return nil, fmt.Errorf("open %s: %w", dir, err)
	}

	r := f.rootOfL(resolved)
	if r == nil {
		return nil, fmt.Errorf("open %s: %w", dir, git.ErrNotRepo)
	}

	return r, nil
}

func (f *fakeGit) defaultBranchL(r *fakeRepo) (string, error) {
	if r.c.deflt == "" {
		return "", fmt.Errorf("default branch: %w", git.ErrNoBase)
	}

	return r.c.deflt, nil
}

// validBranch is git check-ref-format --branch for the forms the tests meet.
func validBranch(name string) bool {
	switch {
	case name == "", strings.HasPrefix(name, "-"), strings.HasPrefix(name, "/"), strings.HasSuffix(name, "/"),
		strings.HasSuffix(name, ".lock"), strings.HasSuffix(name, "."), strings.Contains(name, ".."),
		strings.Contains(name, "//"), strings.Contains(name, "@{"), name == "@":
		return false
	}

	for _, c := range name {
		if c <= ' ' || c == 0x7f || strings.ContainsRune("~^:?*[\\", c) {
			return false
		}
	}

	for part := range strings.SplitSeq(name, "/") {
		if strings.HasPrefix(part, ".") {
			return false
		}
	}

	return true
}

func badBranch(name string) error { return fmt.Errorf("branch %q: %w", name, git.ErrBadBranch) }

func noRemote(op string) error { return fmt.Errorf("%s: %w", op, git.ErrNoRemote) }

func (f *fakeGit) commitInfo(oid string, body bool) git.Commit {
	c := f.commits[oid]
	out := git.Commit{OID: oid, Subject: c.subject}

	if body {
		out.Body = c.body
	}

	return out
}

// The Git calls.

func (f *fakeGit) Open(ctx context.Context, dir string) (git.Repo, error) {
	end, err := f.begin(ctx, "Open", dir)
	if err != nil {
		return git.Repo{}, err
	}
	defer end()

	r, err := f.openL(dir)
	if err != nil {
		return git.Repo{}, err
	}

	return r.Repo(), nil
}

func (f *fakeGit) EditPlace(ctx context.Context, p string) (git.Place, error) {
	end, err := f.begin(ctx, "EditPlace", p)
	if err != nil {
		return git.Place{}, err
	}
	defer end()

	notCounted := func(why string) error { return fmt.Errorf("edit %s: %w: %s", p, git.ErrNotCounted, why) }

	if !filepath.IsAbs(p) {
		return git.Place{}, notCounted("not an absolute path")
	}

	p = filepath.Clean(p)

	for part := range strings.SplitSeq(p, string(filepath.Separator)) {
		if strings.EqualFold(part, ".git") {
			return git.Place{}, notCounted("inside a git dir")
		}
	}

	dir, tail := filepath.Dir(p), []string{filepath.Base(p)}

	for {
		if info, err := os.Stat(dir); err == nil && info.IsDir() {
			break
		}

		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}

		dir, tail = parent, append([]string{filepath.Base(dir)}, tail...)
	}

	resolved, err := filepath.EvalSymlinks(dir)
	if err != nil {
		return git.Place{}, fmt.Errorf("edit %s: %w", p, err)
	}

	r := f.rootOfL(resolved)
	if r == nil {
		return git.Place{}, notCounted("outside any work tree")
	}

	rel, err := filepath.Rel(r.root, filepath.Join(resolved, filepath.Join(tail...)))
	if err != nil {
		return git.Place{}, fmt.Errorf("edit %s: %w", p, err)
	}

	if ignored(r.c.ignores, filepath.ToSlash(rel), false) {
		return git.Place{}, notCounted("ignored")
	}

	return git.Place{Root: r.root, Common: r.Repo().CommonDir, Repo: r.Repo().Name()}, nil
}

func (f *fakeGit) Head(ctx context.Context, gr git.Repo) (git.HeadInfo, error) {
	r, end, err := f.enterRepo(ctx, "Head", gr)
	if err != nil {
		return git.HeadInfo{}, err
	}
	defer end()

	h := git.HeadInfo{Branch: r.cur, OID: r.headOID()}
	if r.detached {
		h.Branch, h.Detached = "", true
	}

	return h, nil
}

func (f *fakeGit) HeadCommit(ctx context.Context, gr git.Repo) (git.Commit, error) {
	r, end, err := f.enterRepo(ctx, "HeadCommit", gr)
	if err != nil {
		return git.Commit{}, err
	}
	defer end()

	oid := r.headOID()
	if _, ok := f.commits[oid]; !ok {
		return git.Commit{}, errors.New("head commit: unborn branch")
	}

	return f.commitInfo(oid, false), nil
}

func (f *fakeGit) Ahead(ctx context.Context, gr git.Repo) (int, bool, error) {
	r, end, err := f.enterRepo(ctx, "Ahead", gr)
	if err != nil {
		return 0, false, err
	}
	defer end()

	remote, ok := r.c.upstream[r.cur]
	if !ok || r.detached {
		return 0, false, nil
	}

	tip, ok := r.c.tracking[remote+"/"+r.cur]
	if !ok || r.headOID() == "" {
		return 0, false, nil
	}

	return f.countNotIn(r.headOID(), f.ancestors(tip)), true, nil
}

func (f *fakeGit) DefaultBranch(ctx context.Context, gr git.Repo) (string, error) {
	r, end, err := f.enterRepo(ctx, "DefaultBranch", gr)
	if err != nil {
		return "", err
	}
	defer end()

	return f.defaultBranchL(r)
}

func (f *fakeGit) Log(ctx context.Context, gr git.Repo, base string) ([]git.Commit, error) {
	r, end, err := f.enterRepo(ctx, "Log", gr, base)
	if err != nil {
		return nil, err
	}
	defer end()

	if strings.HasPrefix(base, "-") {
		return nil, fmt.Errorf("git log: invalid base %q", base)
	}

	tip, ok := r.c.resolve(base)
	if !ok {
		return nil, fmt.Errorf("resolve base %q: %w", base, errNoBaseRef)
	}

	if _, ok := f.commits[r.headOID()]; !ok {
		return nil, fmt.Errorf("list commits since %q: unborn branch", base)
	}

	excl := f.ancestors(tip)

	var out []git.Commit

	for _, oid := range f.chain(r.headOID()) {
		if excl[oid] {
			break
		}

		out = append(out, f.commitInfo(oid, true))
	}

	slices.Reverse(out)

	if len(out) > 250 {
		out = out[len(out)-250:]
	}

	return out, nil
}

func (f *fakeGit) wtEntry(r *fakeRepo) git.Worktree {
	head := r.headOID()
	if head == "" {
		head = strings.Repeat("0", 40)
	}

	w := git.Worktree{
		Path: r.root, Head: head, Main: !r.linked, Detached: r.detached,
		Locked: r.locked, LockReason: r.lockReason, Prunable: r.isPrunable, PruneReason: r.pruneReason,
	}
	if !r.detached {
		w.Branch = r.cur
	}

	return w
}

func (f *fakeGit) ListWorktrees(ctx context.Context, gr git.Repo) ([]git.Worktree, error) {
	r, end, err := f.enterRepo(ctx, "ListWorktrees", gr)
	if err != nil {
		return nil, err
	}
	defer end()

	out := make([]git.Worktree, 0, len(r.c.trees))
	for _, t := range r.c.trees {
		out = append(out, f.wtEntry(t))
	}

	return out, nil
}

func (f *fakeGit) CreateWorktree(ctx context.Context, gr git.Repo, nw git.NewWorktree) (git.Created, error) {
	style := "inside"
	if nw.Root == git.RootSibling {
		style = "sibling"
	}

	r, end, err := f.enterRepo(ctx, "CreateWorktree", gr, nw.Branch, nw.Base, style)
	if err != nil {
		return git.Created{}, err
	}
	defer end()

	if !validBranch(nw.Branch) {
		return git.Created{}, badBranch(nw.Branch)
	}

	target, err := git.WorktreePath(gr, nw.Root, nw.Branch)
	if err != nil {
		return git.Created{}, err
	}

	if _, err := os.Lstat(target); err == nil {
		return git.Created{}, fmt.Errorf("create worktree %s: %w", target, git.ErrExists)
	}

	var created git.Created

	if nw.Root == git.RootInside && !slices.Contains(r.c.ignores, "/.worktrees/") {
		r.c.ignores = append(r.c.ignores, "/.worktrees/")
		created.Excluded = true
	}

	oid, ok := r.c.branches[nw.Branch]
	if !ok {
		base := nw.Base
		if base == "" {
			if base, err = f.defaultBranchL(r); err != nil {
				return git.Created{}, err
			}
		}

		if oid, ok = r.c.resolve(base); !ok || strings.HasPrefix(base, "-") {
			return git.Created{}, fmt.Errorf("base %q: %w", base, git.ErrNoBase)
		}
	}

	nr, err := f.addWorktreeL(r.t, r.c, target, nw.Branch, oid)
	if err != nil {
		return git.Created{}, fmt.Errorf("create worktree %s: %w", target, err)
	}

	if nw.Root == git.RootInside {
		created.Tools, _ = git.DetectTools(r.c.main)
	}

	created.Worktree = f.wtEntry(nr)

	return created, nil
}

func (f *fakeGit) WorktreeChanges(ctx context.Context, dir string) (int, error) {
	end, err := f.begin(ctx, "WorktreeChanges", dir)
	if err != nil {
		return 0, err
	}
	defer end()

	r, err := f.openL(dir)
	if err != nil {
		return 0, fmt.Errorf("count changes in %s: %w", dir, err)
	}

	return f.changesL(r), nil
}

func (f *fakeGit) RemoveWorktree(ctx context.Context, gr git.Repo, p string) error {
	r, end, err := f.enterRepo(ctx, "RemoveWorktree", gr, p)
	if err != nil {
		return err
	}
	defer end()

	target := filepath.Clean(p)
	if resolved, err := filepath.EvalSymlinks(target); err == nil {
		target = resolved
	}

	idx := slices.IndexFunc(r.c.trees, func(t *fakeRepo) bool { return t.linked && t.root == target })
	if idx < 0 {
		return fmt.Errorf("remove worktree %s: not a working tree", p)
	}

	w := r.c.trees[idx]

	switch {
	case w.locked:
		return fmt.Errorf("remove worktree %s: cannot remove a locked working tree", p)
	case f.changesL(w) > 0:
		return fmt.Errorf("remove worktree %s: %w", p, git.ErrWorktreeDirty)
	}

	r.c.trees = slices.Delete(r.c.trees, idx, idx+1)

	delete(f.roots, w.root)

	if err := os.RemoveAll(w.root); err != nil {
		return fmt.Errorf("remove worktree %s: %w", p, err)
	}

	return nil
}

func (f *fakeGit) BranchAhead(ctx context.Context, gr git.Repo, branch, head string) (string, int, error) {
	r, end, err := f.enterRepo(ctx, "BranchAhead", gr, branch, head)
	if err != nil {
		return "", 0, err
	}
	defer end()

	tip, ok := r.c.branches[branch]
	if !ok {
		return "", 0, fmt.Errorf("tip of %s: no such branch", branch)
	}

	if tip == head {
		return tip, 0, nil
	}

	if _, ok := f.commits[head]; !ok {
		return "", 0, fmt.Errorf("commit %s: %w", head, git.ErrNoCommit)
	}

	return tip, f.countNotIn(tip, f.ancestors(head)), nil
}

func (f *fakeGit) DeleteBranch(ctx context.Context, gr git.Repo, branch, tip string) error {
	r, end, err := f.enterRepo(ctx, "DeleteBranch", gr, branch, tip)
	if err != nil {
		return err
	}
	defer end()

	if strings.HasPrefix(branch, "-") {
		return badBranch(branch)
	}

	cur, ok := r.c.branches[branch]
	if !ok {
		return fmt.Errorf("tip of %s: no such branch", branch)
	}

	if cur != tip {
		return fmt.Errorf("delete branch %s: %w", branch, git.ErrBranchMoved)
	}

	for _, t := range r.c.trees {
		if !t.detached && t.cur == branch {
			return fmt.Errorf("delete branch %s: checked out at %s", branch, t.root)
		}
	}

	delete(r.c.branches, branch)

	return nil
}

func (f *fakeGit) PruneWorktree(ctx context.Context, gr git.Repo, worktree string) (int, error) {
	r, end, err := f.enterRepo(ctx, "PruneWorktree", gr, worktree)
	if err != nil {
		return 0, err
	}
	defer end()

	want := filepath.Clean(worktree)
	key := turnKey(want)

	for session, ts := range r.c.turns {
		r.c.turns[session] = slices.DeleteFunc(ts, func(t *fakeTurn) bool { return t.root == want })
	}

	return deleteRefsWhere(r.c, func(name string) bool {
		m := turnRefRE.FindStringSubmatch(name)

		return m != nil && m[2] == key
	}), nil
}

var (
	turnRefRE = regexp.MustCompile(`^refs/aim/turns/([^/]+)/([0-9a-f]{16})/([0-9]+)/(start|end)$`)
	prRefRE   = regexp.MustCompile(`^refs/aim/pr/([1-9][0-9]*)/(head|base)$`)
)

func deleteRefsWhere(c *fakeCommon, match func(string) bool) int {
	n := 0

	for name := range c.aimRefs {
		if match(name) {
			delete(c.aimRefs, name)

			n++
		}
	}

	return n
}

func (f *fakeGit) pushRemoteL(r *fakeRepo, branch string) (*fakeRemote, error) {
	if !validBranch(branch) {
		return nil, badBranch(branch)
	}

	name := r.c.upstream[branch]
	if name == "" {
		name = "origin"
	}

	rm, ok := r.c.remotes[name]
	if !ok {
		return nil, noRemote("push remote is not a configured remote")
	}

	return rm, nil
}

func (f *fakeGit) PushRemote(ctx context.Context, gr git.Repo, branch string) (string, error) {
	r, end, err := f.enterRepo(ctx, "PushRemote", gr, branch)
	if err != nil {
		return "", err
	}
	defer end()

	rm, err := f.pushRemoteL(r, branch)
	if err != nil {
		return "", err
	}

	return rm.name, nil
}

func (f *fakeGit) remoteL(r *fakeRepo, name string) (*fakeRemote, error) {
	rm, ok := r.c.remotes[name]
	if name == "" || !ok {
		return nil, noRemote("push remote is not a configured remote")
	}

	return rm, nil
}

func (f *fakeGit) PushURLs(ctx context.Context, gr git.Repo, remote string) ([]string, error) {
	r, end, err := f.enterRepo(ctx, "PushURLs", gr, remote)
	if err != nil {
		return nil, err
	}
	defer end()

	rm, err := f.remoteL(r, remote)
	if err != nil {
		return nil, err
	}

	if len(rm.pushURLs) > 0 {
		return slices.Clone(rm.pushURLs), nil
	}

	return []string{rm.url}, nil
}

func (f *fakeGit) RemoteURL(ctx context.Context, gr git.Repo, remote string) (string, error) {
	r, end, err := f.enterRepo(ctx, "RemoteURL", gr, remote)
	if err != nil {
		return "", err
	}
	defer end()

	rm, err := f.remoteL(r, remote)
	if err != nil {
		return "", err
	}

	return rm.url, nil
}

// RemoteBranches lists the remote-tracking branches, as internal/git does:
// a remote that is not configured has none.
func (f *fakeGit) RemoteBranches(ctx context.Context, gr git.Repo, remote string) ([]string, error) {
	r, end, err := f.enterRepo(ctx, "RemoteBranches", gr, remote)
	if err != nil {
		return nil, err
	}
	defer end()

	if remote == "" || strings.HasPrefix(remote, "-") {
		return nil, noRemote("push remote is not a configured remote")
	}

	var names []string

	for key := range r.c.tracking {
		if name, ok := strings.CutPrefix(key, remote+"/"); ok && name != "HEAD" {
			names = append(names, name)
		}
	}

	sort.Strings(names)

	return names, nil
}

// RemoteHasBranch reads the tracking ref, as internal/git does.
func (f *fakeGit) RemoteHasBranch(ctx context.Context, gr git.Repo, remote, branch string) (bool, error) {
	r, end, err := f.enterRepo(ctx, "RemoteHasBranch", gr, remote, branch)
	if err != nil {
		return false, err
	}
	defer end()

	if remote == "" || strings.HasPrefix(remote, "-") {
		return false, noRemote("push remote is not a configured remote")
	}

	if !validBranch(branch) {
		return false, badBranch(branch)
	}

	_, ok := r.c.tracking[remote+"/"+branch]

	return ok, nil
}

func (f *fakeGit) HasUpstream(ctx context.Context, gr git.Repo, branch string) (bool, error) {
	r, end, err := f.enterRepo(ctx, "HasUpstream", gr, branch)
	if err != nil {
		return false, err
	}
	defer end()

	if !validBranch(branch) {
		return false, badBranch(branch)
	}

	_, ok := r.c.upstream[branch]

	return ok, nil
}

func (f *fakeGit) Unpushed(ctx context.Context, gr git.Repo, remote string) (int, error) {
	r, end, err := f.enterRepo(ctx, "Unpushed", gr, remote)
	if err != nil {
		return 0, err
	}
	defer end()

	if remote == "" || strings.HasPrefix(remote, "-") {
		return 0, noRemote("push remote is not a configured remote")
	}

	if _, ok := f.commits[r.headOID()]; !ok {
		return 0, errors.New("unpushed: unborn branch")
	}

	seen := map[string]bool{}

	for key, oid := range r.c.tracking {
		if strings.HasPrefix(key, remote+"/") {
			maps.Copy(seen, f.ancestors(oid))
		}
	}

	return f.countNotIn(r.headOID(), seen), nil
}

func (f *fakeGit) Push(ctx context.Context, gr git.Repo, branch string) (git.PushResult, error) {
	r, end, err := f.enterRepo(ctx, "Push", gr, branch)
	if err != nil {
		return git.PushResult{}, err
	}
	defer end()

	rm, err := f.pushRemoteL(r, branch)
	if err != nil {
		return git.PushResult{}, err
	}

	_, hasUp := r.c.upstream[branch]
	res := git.PushResult{Remote: rm.name, Branch: branch, Dst: "refs/heads/" + branch, SetUpstream: !hasUp}

	tip, ok := r.c.branches[branch]
	if !ok {
		return git.PushResult{}, fmt.Errorf("push %s to %s: src refspec refs/heads/%s does not match any", branch, rm.name, branch)
	}

	remoteTip, exists := rm.branches[branch]

	switch {
	case exists && remoteTip != tip && !slices.Contains(f.chain(tip), remoteTip):
		return git.PushResult{}, fmt.Errorf("push %s to %s: %w (non-fast-forward)", branch, rm.name, git.ErrRejected)
	case !exists:
		res.Created = true
	case remoteTip == tip:
		res.UpToDate = true
	}

	rm.branches[branch] = tip
	r.c.tracking[rm.name+"/"+branch] = tip

	if !hasUp {
		r.c.upstream[branch] = rm.name
	}

	return res, nil
}

func (f *fakeGit) FetchPR(ctx context.Context, gr git.Repo, remote string, number int, base string) (git.PRRefs, error) {
	r, end, err := f.enterRepo(ctx, "FetchPR", gr, remote, strconv.Itoa(number), base)
	if err != nil {
		return git.PRRefs{}, err
	}
	defer end()

	switch {
	case number < 1:
		return git.PRRefs{}, fmt.Errorf("fetch pull request: number %d is not positive", number)
	case strings.HasPrefix(base, "-"):
		return git.PRRefs{}, fmt.Errorf("fetch pull request: base %q looks like an option", base)
	}

	rm, err := f.remoteL(r, remote)
	if err != nil {
		return git.PRRefs{}, fmt.Errorf("fetch pull request: %w", err)
	}

	if !validBranch(base) {
		return git.PRRefs{}, fmt.Errorf("fetch pull request: %w", badBranch(base))
	}

	head, ok := rm.prs[number]
	if !ok {
		return git.PRRefs{}, fmt.Errorf("fetch pull request %d from %s: %w", number, remote, git.ErrNoPR)
	}

	baseOID, ok := rm.branches[base]
	if !ok {
		return git.PRRefs{}, fmt.Errorf("fetch pull request %d from %s: couldn't find remote ref refs/heads/%s", number, remote, base)
	}

	r.c.aimRefs[fmt.Sprintf("refs/aim/pr/%d/head", number)] = head
	r.c.aimRefs[fmt.Sprintf("refs/aim/pr/%d/base", number)] = baseOID

	return git.PRRefs{Head: head, Base: baseOID}, nil
}

// fakeStale is a listing of refs/aim.
type fakeStale struct{ refs []git.Ref }

// Stale picks the turn refs of the sessions held refuses and the pull request
// refs shown refuses; the age rule is git.AimRefs's.
func (s fakeStale) Stale(_ time.Time, held func(string) bool, shown func(int) bool) git.Sweep {
	var sw git.Sweep

	for _, ref := range s.refs {
		if m := turnRefRE.FindStringSubmatch(ref.Name); m != nil && !held(m[1]) {
			sw.Turns = append(sw.Turns, ref)
		}

		if m := prRefRE.FindStringSubmatch(ref.Name); m != nil {
			if n, err := strconv.Atoi(m[1]); err == nil && !shown(n) {
				sw.PRs = append(sw.PRs, ref)
			}
		}
	}

	return sw
}

func (f *fakeGit) ListAimRefs(ctx context.Context, gr git.Repo) (StaleRefs, error) {
	r, end, err := f.enterRepo(ctx, "ListAimRefs", gr)
	if err != nil {
		return nil, err
	}
	defer end()

	var refs []git.Ref
	for _, name := range slices.Sorted(maps.Keys(r.c.aimRefs)) {
		refs = append(refs, git.Ref{Name: name, OID: r.c.aimRefs[name]})
	}

	return fakeStale{refs: refs}, nil
}

func (f *fakeGit) DeleteRefs(ctx context.Context, gr git.Repo, refs []git.Ref) (int, error) {
	args := make([]string, len(refs))
	for i, ref := range refs {
		args[i] = ref.Name + " " + ref.OID
	}

	r, end, err := f.enterRepo(ctx, "DeleteRefs", gr, args...)
	if err != nil {
		return 0, err
	}
	defer end()

	for _, ref := range refs {
		if !strings.HasPrefix(ref.Name, "refs/aim/") || ref.OID == "" {
			return 0, fmt.Errorf("delete %q: %w", ref.Name, git.ErrNotAimRef)
		}
	}

	var (
		n    int
		errs []error
	)

	for _, ref := range refs {
		if cur, ok := r.c.aimRefs[ref.Name]; !ok || cur != ref.OID {
			errs = append(errs, fmt.Errorf("delete aim refs: %s is not at %s", ref.Name, ref.OID))

			continue
		}

		delete(r.c.aimRefs, ref.Name)
		f.dropTurnRefL(r.c, ref.Name)

		n++
	}

	return n, errors.Join(errs...)
}

// dropTurnRefL applies the deletion of a turn ref to the turns: a start takes
// the turn with it, an end reopens it.
func (f *fakeGit) dropTurnRefL(c *fakeCommon, name string) {
	m := turnRefRE.FindStringSubmatch(name)
	if m == nil {
		return
	}

	num, _ := strconv.Atoi(m[3])

	c.turns[m[1]] = slices.DeleteFunc(c.turns[m[1]], func(t *fakeTurn) bool {
		if turnKey(t.root) != m[2] || t.n != num {
			return false
		}

		if m[4] == "end" {
			t.ended = false
			t.endTree = nil

			return false
		}

		return true
	})
}

func (f *fakeGit) DeletePRRefs(ctx context.Context, gr git.Repo, number int) error {
	r, end, err := f.enterRepo(ctx, "DeletePRRefs", gr, strconv.Itoa(number))
	if err != nil {
		return err
	}
	defer end()

	prefix := fmt.Sprintf("refs/aim/pr/%d/", number)

	deleteRefsWhere(r.c, func(name string) bool { return strings.HasPrefix(name, prefix) })

	return nil
}

func TestFakeHistory(t *testing.T) {
	t.Parallel()

	f := newFakeGit(t)
	r := f.repo(t, "")
	ctx := context.Background()

	write(t, filepath.Join(r.Root(), "a.txt"), "a\n")
	base := r.commit("base")
	r.remote("origin", "https://example.com/o.git")
	r.track("origin", "main", base)
	r.branch("feat/x")
	write(t, filepath.Join(r.Root(), "b.txt"), "b\n")
	first := r.commit("first\n\nwhy")
	write(t, filepath.Join(r.Root(), "c.txt"), "c\n")
	second := r.commit("second")

	log, err := f.Log(ctx, r.Repo(), "main")
	if err != nil {
		t.Fatalf("Log: %v", err)
	}

	want := []git.Commit{{OID: first, Subject: "first", Body: "why"}, {OID: second, Subject: "second"}}
	if !reflect.DeepEqual(log, want) {
		t.Errorf("Log = %+v, want %+v", log, want)
	}

	if _, err := f.Log(ctx, r.Repo(), "nope"); !errors.Is(err, errNoBaseRef) {
		t.Errorf("Log of a missing base = %v, want errNoBaseRef", err)
	}

	unpushed := func() int {
		n, err := f.Unpushed(ctx, r.Repo(), "origin")
		if err != nil {
			t.Fatalf("Unpushed: %v", err)
		}

		return n
	}

	if n := unpushed(); n != 2 {
		t.Errorf("Unpushed before the push = %d, want 2", n)
	}

	res, err := f.Push(ctx, r.Repo(), "feat/x")
	if err != nil || !res.Created || !res.SetUpstream || res.UpToDate || res.Dst != "refs/heads/feat/x" {
		t.Fatalf("first Push = %+v, %v", res, err)
	}

	if n := unpushed(); n != 0 {
		t.Errorf("Unpushed after the push = %d, want 0", n)
	}

	if has, _ := f.HasUpstream(ctx, r.Repo(), "feat/x"); !has {
		t.Error("the push did not set the upstream")
	}

	if res, err = f.Push(ctx, r.Repo(), "feat/x"); err != nil || !res.UpToDate || res.SetUpstream {
		t.Errorf("second Push = %+v, %v, want up to date", res, err)
	}

	r.reset(first)

	if _, err = f.Push(ctx, r.Repo(), "feat/x"); !errors.Is(err, git.ErrRejected) {
		t.Errorf("Push behind the remote = %v, want ErrRejected", err)
	}

	if tip, ahead, err := f.BranchAhead(ctx, r.Repo(), "feat/x", first); err != nil || tip != first || ahead != 0 {
		t.Errorf("BranchAhead after the reset = %s, %d, %v", tip, ahead, err)
	}
}

func TestFakeHold(t *testing.T) {
	t.Parallel()

	f := newFakeGit(t)
	r := f.repo(t, "")
	ctx := context.Background()
	release := f.hold("Head")
	errs := make(chan error, 2)

	for range 2 {
		go func() {
			_, err := f.Head(ctx, r.Repo())
			errs <- err
		}()
	}

	eventually(t, "both Head calls to be in flight", func() bool { return f.inflight("Head") == 2 })

	if _, err := f.Open(ctx, r.Root()); err != nil {
		t.Errorf("Open while Head is held: %v", err)
	}

	release()

	for range 2 {
		if err := <-errs; err != nil {
			t.Errorf("released Head: %v", err)
		}
	}

	if n := f.inflight("Head"); n != 0 {
		t.Errorf("inflight after the release = %d, want 0", n)
	}

	release = f.hold("Head")
	defer release()

	cctx, cancel := context.WithCancel(ctx)
	done := make(chan error, 1)

	go func() {
		_, err := f.Head(cctx, r.Repo())
		done <- err
	}()

	eventually(t, "the held call to be in flight", func() bool { return f.inflight("Head") == 1 })
	cancel()

	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Errorf("cancelled held Head = %v, want context.Canceled", err)
	}

	if p := f.peak("Head"); p != 2 {
		t.Errorf("peak = %d, want 2", p)
	}
}

func TestFakeWorktrees(t *testing.T) {
	t.Parallel()

	f := newFakeGit(t)
	r := f.repo(t, "app")
	ctx := context.Background()

	write(t, filepath.Join(r.Root(), "a.txt"), "a\n")
	r.commit("base")

	created, err := f.CreateWorktree(ctx, r.Repo(), git.NewWorktree{Branch: "feat/x"})
	if err != nil {
		t.Fatalf("CreateWorktree: %v", err)
	}

	wantPath := filepath.Join(r.Root(), ".worktrees", "feat-x")
	if created.Worktree.Path != wantPath || created.Worktree.Branch != "feat/x" || !created.Excluded || created.Worktree.Main {
		t.Errorf("created = %+v, want %s on feat/x, excluded", created, wantPath)
	}

	if got, err := os.ReadFile(filepath.Join(wantPath, "a.txt")); err != nil || string(got) != "a\n" {
		t.Errorf("worktree file = %q, %v, want the commit's files", got, err)
	}

	if again, err := f.CreateWorktree(ctx, r.Repo(), git.NewWorktree{Branch: "other"}); err != nil || again.Excluded {
		t.Errorf("second create = %+v, %v, want no new exclude", again, err)
	}

	errTests := []struct {
		name string
		nw   git.NewWorktree
		want error
	}{
		{"taken path", git.NewWorktree{Branch: "feat/x"}, git.ErrExists},
		{"empty", git.NewWorktree{Branch: ""}, git.ErrBadBranch},
		{"space", git.NewWorktree{Branch: "a b"}, git.ErrBadBranch},
		{"dots", git.NewWorktree{Branch: "a..b"}, git.ErrBadBranch},
		{"dash", git.NewWorktree{Branch: "-x"}, git.ErrBadBranch},
		{"slash", git.NewWorktree{Branch: "x/"}, git.ErrBadBranch},
		{"lock", git.NewWorktree{Branch: "x.lock"}, git.ErrBadBranch},
		{"no base", git.NewWorktree{Branch: "y", Base: "nope"}, git.ErrNoBase},
	}

	for _, tt := range errTests {
		if _, err := f.CreateWorktree(ctx, r.Repo(), tt.nw); !errors.Is(err, tt.want) {
			t.Errorf("%s: CreateWorktree = %v, want %v", tt.name, err, tt.want)
		}
	}

	if calls := f.calls("CreateWorktree"); len(calls) != 2+len(errTests) || !slices.Equal(calls[0].Args, []string{"feat/x", "", "inside"}) {
		t.Errorf("call log = %+v", calls)
	}

	opened, err := f.Open(ctx, filepath.Join(wantPath, "sub"))
	if err == nil {
		t.Errorf("Open of a missing dir = %+v, want an error", opened)
	}

	opened, err = f.Open(ctx, wantPath)
	if err != nil || opened.Root != wantPath || opened.CommonDir != filepath.Join(r.Root(), ".git") ||
		opened.GitDir != filepath.Join(r.Root(), ".git", "worktrees", "feat-x") {
		t.Errorf("Open(worktree) = %+v, %v", opened, err)
	}

	if _, err := f.Open(ctx, t.TempDir()); !errors.Is(err, git.ErrNotRepo) {
		t.Errorf("Open outside = %v, want ErrNotRepo", err)
	}

	list, err := f.ListWorktrees(ctx, r.Repo())
	if err != nil || len(list) != 3 || !list[0].Main || list[1].Branch != "feat/x" || list[2].Branch != "other" {
		t.Fatalf("ListWorktrees = %+v, %v", list, err)
	}

	write(t, filepath.Join(wantPath, "new.txt"), "n\n")

	if n, err := f.WorktreeChanges(ctx, wantPath); err != nil || n != 1 {
		t.Errorf("WorktreeChanges = %d, %v, want 1", n, err)
	}

	if err := f.RemoveWorktree(ctx, r.Repo(), wantPath); !errors.Is(err, git.ErrWorktreeDirty) {
		t.Errorf("RemoveWorktree of a dirty worktree = %v, want ErrWorktreeDirty", err)
	}

	locked := r.worktree(t, filepath.Join(t.TempDir(), "locked"), "locked")
	locked.lock("busy")
	locked.prunable("gone")

	if err := f.RemoveWorktree(ctx, r.Repo(), locked.Root()); err == nil || errors.Is(err, git.ErrWorktreeDirty) {
		t.Errorf("RemoveWorktree of a locked worktree = %v, want a plain error", err)
	}

	list, _ = f.ListWorktrees(ctx, r.Repo())

	if w := list[len(list)-1]; !w.Locked || w.LockReason != "busy" || !w.Prunable || w.PruneReason != "gone" {
		t.Errorf("flags = %+v", w)
	}

	other := filepath.Join(r.Root(), ".worktrees", "other")
	if err := f.RemoveWorktree(ctx, r.Repo(), other); err != nil {
		t.Fatalf("RemoveWorktree: %v", err)
	}

	if _, err := os.Stat(other); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("removed worktree is still on disk: %v", err)
	}

	base, _ := f.HeadCommit(ctx, r.Repo())

	if err := f.DeleteBranch(ctx, r.Repo(), "other", "0000"); !errors.Is(err, git.ErrBranchMoved) {
		t.Errorf("DeleteBranch of a moved tip = %v, want ErrBranchMoved", err)
	}

	if err := f.DeleteBranch(ctx, r.Repo(), "other", base.OID); err != nil {
		t.Errorf("DeleteBranch: %v", err)
	}

	if _, _, err := f.BranchAhead(ctx, r.Repo(), "other", base.OID); err == nil {
		t.Error("BranchAhead of a deleted branch succeeded")
	}

	if _, _, err := f.BranchAhead(ctx, r.Repo(), "feat/x", "1234"); !errors.Is(err, git.ErrNoCommit) {
		t.Errorf("BranchAhead of an unknown head = %v, want ErrNoCommit", err)
	}

	if n, err := f.PruneWorktree(ctx, r.Repo(), wantPath); err != nil || n != 0 {
		t.Errorf("PruneWorktree without turns = %d, %v", n, err)
	}
}

func TestFakeRemotes(t *testing.T) {
	t.Parallel()

	f := newFakeGit(t)
	r := f.repo(t, "")
	ctx := context.Background()

	write(t, filepath.Join(r.Root(), "a.txt"), "a\n")
	base := r.commit("base")

	if _, err := f.PushRemote(ctx, r.Repo(), "main"); !errors.Is(err, git.ErrNoRemote) {
		t.Errorf("PushRemote without remotes = %v, want ErrNoRemote", err)
	}

	origin := r.remote("origin", "https://example.com/o.git")
	fork := r.remote("fork", "https://example.com/f.git")
	fork.setPushURLs("ssh://example.com/a", "ssh://example.com/b")
	origin.setBranch("main", base)
	origin.setBranch("gone", base)
	origin.setBranch("gone", "")

	write(t, filepath.Join(r.Root(), "b.txt"), "b\n")
	head := r.commit("head")
	origin.pr(7, head)
	origin.setBranch("topic", head)
	r.track("origin", "main", base)
	r.track("origin", "topic", head)
	r.track("origin", "old", base)
	r.track("origin", "old", "")
	r.setUpstream("main", "origin")

	checks := []struct {
		name string
		got  any
		want any
	}{
		{"PushRemote default", first(f.PushRemote(ctx, r.Repo(), "other")), "origin"},
		{"PushRemote upstream", first(f.PushRemote(ctx, r.Repo(), "main")), "origin"},
		{"RemoteURL", first(f.RemoteURL(ctx, r.Repo(), "fork")), "https://example.com/f.git"},
		{"PushURLs", first(f.PushURLs(ctx, r.Repo(), "fork")), []string{"ssh://example.com/a", "ssh://example.com/b"}},
		{"PushURLs fetch URL", first(f.PushURLs(ctx, r.Repo(), "origin")), []string{"https://example.com/o.git"}},
		{"RemoteBranches", first(f.RemoteBranches(ctx, r.Repo(), "origin")), []string{"main", "topic"}},
		{"RemoteHasBranch", first(f.RemoteHasBranch(ctx, r.Repo(), "origin", "topic")), true},
		{"RemoteHasBranch missing", first(f.RemoteHasBranch(ctx, r.Repo(), "origin", "old")), false},
		{"HasUpstream", first(f.HasUpstream(ctx, r.Repo(), "main")), true},
		{"HasUpstream none", first(f.HasUpstream(ctx, r.Repo(), "other")), false},
	}

	for _, c := range checks {
		if !reflect.DeepEqual(c.got, c.want) {
			t.Errorf("%s = %v, want %v", c.name, c.got, c.want)
		}
	}

	if _, err := f.RemoteURL(ctx, r.Repo(), "nope"); !errors.Is(err, git.ErrNoRemote) {
		t.Errorf("RemoteURL of an unknown remote = %v, want ErrNoRemote", err)
	}

	if n, ok, err := f.Ahead(ctx, r.Repo()); err != nil || !ok || n != 1 {
		t.Errorf("Ahead = %d, %v, %v, want 1", n, ok, err)
	}

	if _, ok, _ := f.Ahead(ctx, f.repo(t, "bare").Repo()); ok {
		t.Error("Ahead without an upstream reported ok")
	}

	refs, err := f.FetchPR(ctx, r.Repo(), "origin", 7, "main")
	if err != nil || refs != (git.PRRefs{Head: head, Base: base}) {
		t.Errorf("FetchPR = %+v, %v", refs, err)
	}

	for _, tt := range []struct {
		n    int
		base string
		want error
	}{{8, "main", git.ErrNoPR}, {7, "-x", nil}, {7, "nope", nil}} {
		if _, err := f.FetchPR(ctx, r.Repo(), "origin", tt.n, tt.base); err == nil || (tt.want != nil && !errors.Is(err, tt.want)) {
			t.Errorf("FetchPR(%d, %q) = %v, want %v", tt.n, tt.base, err, tt.want)
		}
	}

	res, err := f.NewEngine(git.EngineOptions{}).Diff(ctx, git.PRSource{Worktree: r.Root(), Number: 7})
	if err != nil || res.Head != head || len(res.Files) != 1 || res.Files[0].Path != "b.txt" {
		t.Errorf("PR diff = %+v, %v, want b.txt at the head", res, err)
	}

	if err := f.DeletePRRefs(ctx, r.Repo(), 7); err != nil {
		t.Fatal(err)
	}

	if _, err := f.NewEngine(git.EngineOptions{}).Diff(ctx, git.PRSource{Worktree: r.Root(), Number: 7}); !errors.Is(err, git.ErrNotFetched) {
		t.Errorf("PR diff after DeletePRRefs = %v, want ErrNotFetched", err)
	}
}

// first is the value of a two-result call.
func first[T any](v T, _ error) any { return v }

func TestFakeHeadAndPlaces(t *testing.T) {
	t.Parallel()

	f := newFakeGit(t)
	r := f.repo(t, "app")
	ctx := context.Background()

	if h, err := f.Head(ctx, r.Repo()); err != nil || h != (git.HeadInfo{Branch: "main"}) {
		t.Errorf("unborn Head = %+v, %v", h, err)
	}

	if _, err := f.HeadCommit(ctx, r.Repo()); err == nil {
		t.Error("HeadCommit of an unborn branch succeeded")
	}

	write(t, filepath.Join(r.Root(), "a.txt"), "a\n")
	r.commitAs("abc123", "base")
	r.branch("feat")
	r.switchTo("main")

	if h, _ := f.Head(ctx, r.Repo()); h != (git.HeadInfo{Branch: "main", OID: "abc123"}) {
		t.Errorf("Head = %+v", h)
	}

	r.detach()

	if h, _ := f.Head(ctx, r.Repo()); h != (git.HeadInfo{Detached: true, OID: "abc123"}) {
		t.Errorf("detached Head = %+v", h)
	}

	if _, ok, _ := f.Ahead(ctx, r.Repo()); ok {
		t.Error("Ahead on a detached HEAD reported ok")
	}

	if b, err := f.DefaultBranch(ctx, r.Repo()); err != nil || b != "main" {
		t.Errorf("DefaultBranch = %q, %v", b, err)
	}

	r.setDefault("")

	if _, err := f.DefaultBranch(ctx, r.Repo()); !errors.Is(err, git.ErrNoBase) {
		t.Errorf("DefaultBranch cleared = %v, want ErrNoBase", err)
	}

	wt := r.worktree(t, filepath.Join(r.Root(), ".worktrees", "w"), "w")
	r.ignore("*.log", "notes/b.md")

	write(t, filepath.Join(r.Root(), "x.log"), "x\n")
	write(t, filepath.Join(r.Root(), "notes", "b.md"), "b\n")

	for _, tt := range []struct {
		name string
		path string
		root string // "" when the edit does not count
	}{
		{"a file", filepath.Join(r.Root(), "pkg", "new.go"), r.Root()},
		{"in a linked worktree", filepath.Join(wt.Root(), "a.txt"), wt.Root()},
		{"ignored pattern", filepath.Join(r.Root(), "x.log"), ""},
		{"ignored path", filepath.Join(r.Root(), "notes", "b.md"), ""},
		{"git dir", filepath.Join(r.Root(), ".git", "HEAD"), ""},
		{"outside", filepath.Join(t.TempDir(), "x.go"), ""},
		{"relative", "x.go", ""},
	} {
		place, err := f.EditPlace(ctx, tt.path)

		switch {
		case tt.root == "" && !errors.Is(err, git.ErrNotCounted):
			t.Errorf("%s: EditPlace = %+v, %v, want ErrNotCounted", tt.name, place, err)
		case tt.root != "" && (err != nil || place.Root != tt.root || place.Repo != "app" || place.Common != filepath.Join(r.Root(), ".git")):
			t.Errorf("%s: EditPlace = %+v, %v, want root %s", tt.name, place, err, tt.root)
		}
	}

	write(t, filepath.Join(r.Root(), "u.txt"), "u\n")

	if n, _ := f.WorktreeChanges(ctx, r.Root()); n != 1 {
		t.Errorf("main changes = %d, want only u.txt: ignored files and linked worktrees do not count", n)
	}
}

func TestFakeScripting(t *testing.T) {
	t.Parallel()

	f := newFakeGit(t)
	r := f.repo(t, "")
	ctx := context.Background()

	var o Options

	gitOf(f)(&o)

	if o.Git != Git(f) || o.Watches != watchSet(f.watches) {
		t.Errorf("gitOf set %v and %v", o.Git, o.Watches)
	}

	boom := errors.New("boom")
	other := f.repo(t, "other")

	f.fail("Head", boom)
	f.failAt("Ahead", other.Root(), boom)

	if _, err := f.Head(ctx, r.Repo()); !errors.Is(err, boom) {
		t.Errorf("failed Head = %v", err)
	}

	if _, _, err := f.Ahead(ctx, r.Repo()); err != nil {
		t.Errorf("Ahead elsewhere = %v", err)
	}

	if _, _, err := f.Ahead(ctx, other.Repo()); !errors.Is(err, boom) {
		t.Errorf("Ahead at the failing root = %v", err)
	}

	f.fail("Head", nil)
	f.failAt("Ahead", other.Root(), nil)

	if _, err := f.Head(ctx, r.Repo()); err != nil {
		t.Errorf("cleared Head = %v", err)
	}

	if n := f.count("Head"); n != 2 || f.calls("Head")[0].Root != r.Root() {
		t.Errorf("Head calls = %+v", f.calls("Head"))
	}

	src := git.BranchSource{Worktree: r.Root()}
	want := git.Result{Old: "o", New: "n", Files: []git.FileDiff{{Path: "renamed", Status: git.Renamed, OldPath: "was"}}}
	f.result(src, want)

	eng := f.NewEngine(git.EngineOptions{})

	if got, err := eng.Diff(ctx, src); err != nil || !reflect.DeepEqual(got, want) {
		t.Errorf("scripted Diff = %+v, %v", got, err)
	}

	if s, err := eng.Stats(ctx, src); err != nil || s != (git.Stats{Files: 1}) {
		t.Errorf("Stats = %+v, %v", s, err)
	}

	if _, err := eng.Update(ctx, want, src, git.Batch{Paths: []string{"a.go"}}); err != nil {
		t.Fatal(err)
	}

	if c := f.calls("Update"); len(c) != 1 || !slices.Equal(c[0].Args, []string{"branch", "", "a.go"}) {
		t.Errorf("Update calls = %+v", c)
	}

	var ran bool

	f.edit(func() { ran = true })

	if !ran {
		t.Error("edit did not run its function")
	}
}

func TestFakeRefs(t *testing.T) {
	t.Parallel()

	const (
		sessA = "aaaaaaaa-0000-4000-8000-00000000000a"
		sessB = "bbbbbbbb-0000-4000-8000-00000000000b"
	)

	f := newFakeGit(t)
	r := f.repo(t, "")
	ctx := context.Background()

	write(t, filepath.Join(r.Root(), "a.txt"), "a\n")
	r.commit("base")
	r.turn(sessA)
	r.turn(sessB)

	turnRef := "refs/aim/turns/" + sessA + "/0123456789abcdef/5/start"
	r.aimRef(turnRef, "c0ffee")
	r.aimRef("refs/aim/pr/9/head", "beef")
	r.aimRef("refs/aim/pr/9/base", "feed")
	r.aimRef("refs/aim/pr/4/head", "d00d")

	listing, err := f.ListAimRefs(ctx, r.Repo())
	if err != nil {
		t.Fatal(err)
	}

	sw := listing.Stale(time.Now(), func(s string) bool { return s == sessA }, func(n int) bool { return n == 9 })

	var turns, prs []string
	for _, ref := range sw.Turns {
		turns = append(turns, ref.Name)
	}

	for _, ref := range sw.PRs {
		prs = append(prs, ref.Name)
	}

	if len(turns) != 1 || !strings.Contains(turns[0], sessB) || !slices.Equal(prs, []string{"refs/aim/pr/4/head"}) {
		t.Errorf("Stale = turns %v, prs %v", turns, prs)
	}

	if n, err := f.DeleteRefs(ctx, r.Repo(), []git.Ref{{Name: "refs/heads/main", OID: "x"}}); !errors.Is(err, git.ErrNotAimRef) || n != 0 {
		t.Errorf("DeleteRefs outside refs/aim = %d, %v", n, err)
	}

	n, err := f.DeleteRefs(ctx, r.Repo(), []git.Ref{{Name: turnRef, OID: "c0ffee"}, {Name: "refs/aim/pr/9/head", OID: "moved"}})
	if n != 1 || err == nil {
		t.Errorf("DeleteRefs with a moved ref = %d, %v, want 1 and an error", n, err)
	}

	if n, err := f.PruneWorktree(ctx, r.Repo(), r.Root()); err != nil || n != 2 {
		t.Errorf("PruneWorktree = %d, %v, want the two turn refs of this worktree", n, err)
	}

	if list, _ := f.NewEngine(git.EngineOptions{}).TurnList(ctx, r.Root(), sessA); len(list.Turns) != 0 {
		t.Errorf("turns after PruneWorktree = %+v", list.Turns)
	}
}
