package git

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"
)

const (
	sessA = "11111111-2222-4333-8444-555555555555"
	sessB = "aaaaaaaa-bbbb-4ccc-8ddd-eeeeeeeeeeee"
)

// seedRepo makes a repository with one commit holding a.txt, b.txt and
// dir/c.txt.
func seedRepo(t *testing.T) string {
	t.Helper()

	return copyTemplate(t, &seedTemplate, func(dir string) error {
		return buildRepo(dir, map[string]string{"a.txt": "a\n", "b.txt": "b\n", "dir/c.txt": "c\n"})
	})
}

func openRepo(t *testing.T, dir string) Repo {
	t.Helper()

	r, err := Open(context.Background(), dir)
	if err != nil {
		t.Fatal(err)
	}

	return r
}

// snap takes a snapshot and fails when it leaves anything in its temp dir. Like
// the next prompt, it ends the worktree's latest turn when that is open.
func snap(t *testing.T, dir, session string, opts SnapshotOptions) Turn {
	t.Helper()

	return snapContext(context.Background(), t, dir, session, opts)
}

// snapContext is snap with the context its git calls run under.
func snapContext(ctx context.Context, t *testing.T, dir, session string, opts SnapshotOptions) Turn {
	t.Helper()

	tmp := t.TempDir()
	opts.TempDir = tmp

	if opts.Ends == 0 {
		if turns, err := Turns(ctx, openRepo(t, dir), session); err == nil && len(turns) > 0 {
			if last := turns[len(turns)-1]; last.EndCommit == "" && !last.Missing {
				opts.Ends = last.N
			}
		}
	}

	turn, err := Snapshot(ctx, dir, session, opts)
	if err != nil {
		t.Fatalf("Snapshot: %v", err)
	}

	assertEmptyDir(t, tmp)

	return turn
}

func assertEmptyDir(t *testing.T, dir string) {
	t.Helper()

	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}

	for _, e := range entries {
		t.Errorf("temporary file left behind: %s", e.Name())
	}
}

// gitEnvOut runs git with extra environment and returns trimmed stdout.
func gitEnvOut(t *testing.T, dir string, env []string, args ...string) string {
	t.Helper()

	cmd := exec.Command("git", args...)
	cmd.Dir = dir

	cmd.Env = append(os.Environ(), env...)

	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("git %s: %v", strings.Join(args, " "), err)
	}

	return strings.TrimSpace(string(out))
}

// scratchTree builds the tree of the files on disk the plain way: an empty
// index and `git add -A`.
func scratchTree(t *testing.T, dir string) string {
	t.Helper()

	env := []string{"GIT_INDEX_FILE=" + filepath.Join(t.TempDir(), "scratch-index")}
	gitEnvOut(t, dir, env, "add", "-A")

	return gitEnvOut(t, dir, env, "write-tree")
}

// treeEntries lists a tree as path -> "mode sha".
func treeEntries(t *testing.T, dir, tree string) map[string]string {
	t.Helper()

	cmd := exec.Command("git", "ls-tree", "-r", "-z", tree)
	cmd.Dir = dir

	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("ls-tree: %v", err)
	}

	entries := map[string]string{}

	for rec := range bytes.SplitSeq(out, []byte{0}) {
		meta, path, ok := strings.Cut(string(rec), "\t")
		if !ok {
			continue
		}

		f := strings.Fields(meta)
		entries[path] = f[0] + " " + f[2]
	}

	return entries
}

func paths(m map[string]string) []string {
	out := make([]string, 0, len(m))
	for p := range m {
		out = append(out, p)
	}

	sort.Strings(out)

	return out
}

func unsetEnv(t *testing.T, keys ...string) {
	t.Helper()

	for _, k := range keys {
		t.Setenv(k, "")

		if err := os.Unsetenv(k); err != nil {
			t.Fatal(err)
		}
	}
}

func TestSnapshotRoundTrip(t *testing.T) {
	dir := seedRepo(t)
	ctx := context.Background()
	before := time.Now().Truncate(time.Second)

	var turns []Turn

	writeFile(t, dir, "a.txt", "a1\n")
	turns = append(turns, snap(t, dir, sessA, SnapshotOptions{}))
	want1 := scratchTree(t, dir)

	writeFile(t, dir, "new.txt", "n\n")
	turns = append(turns, snap(t, dir, sessA, SnapshotOptions{}))
	want2 := scratchTree(t, dir)

	if err := os.Remove(filepath.Join(dir, "b.txt")); err != nil {
		t.Fatal(err)
	}

	turns = append(turns, snap(t, dir, sessA, SnapshotOptions{}))
	want3 := scratchTree(t, dir)

	for i, want := range []string{want1, want2, want3} {
		if turns[i].N != i+1 || turns[i].Tree != want {
			t.Errorf("turn %d = %+v, want tree %s", i+1, turns[i], want)
		}
	}

	got, err := Turns(ctx, openRepo(t, dir), sessA)
	if err != nil {
		t.Fatal(err)
	}

	after := time.Now()

	if len(got) != 3 {
		t.Fatalf("Turns = %+v, want 3", got)
	}

	head := gitOut(t, dir, "rev-parse", "HEAD")

	for i, g := range got {
		if g.N != i+1 || g.Commit != turns[i].Commit || g.Tree != turns[i].Tree || !g.Time.Equal(turns[i].Time) {
			t.Errorf("Turns[%d] = %+v, want %+v", i, g, turns[i])
		}

		if p := gitOut(t, dir, "rev-parse", g.Commit+"^"); p != head {
			t.Errorf("turn %d parent = %s, want HEAD %s", g.N, p, head)
		}

		if g.Time.Before(before) || g.Time.After(after) {
			t.Errorf("turn %d time %v is outside the test's run, %v to %v", g.N, g.Time, before, after)
		}
	}

	msg := gitOut(t, dir, "log", "-1", "--format=%B", turns[0].Commit)
	for _, line := range []string{"aim turn 1", "Session: " + sessA, "Worktree: " + dir} {
		if !strings.Contains(msg, line) {
			t.Errorf("commit message lacks %q:\n%s", line, msg)
		}
	}

	if who := gitOut(t, dir, "log", "-1", "--format=%an <%ae> %cn <%ce>", turns[0].Commit); who !=
		"AIM <aim@localhost> AIM <aim@localhost>" {
		t.Errorf("identity = %q", who)
	}
}

func TestTurnsSortNumerically(t *testing.T) {
	t.Parallel()
	dir := seedRepo(t)

	for range 11 {
		snap(t, dir, sessA, SnapshotOptions{})
	}

	turns, err := Turns(context.Background(), openRepo(t, dir), sessA)
	if err != nil {
		t.Fatal(err)
	}

	for i, tn := range turns {
		if tn.N != i+1 {
			t.Fatalf("turn at index %d is %d; turns = %v", i, tn.N, turns)
		}
	}

	if len(turns) != 11 {
		t.Fatalf("got %d turns, want 11", len(turns))
	}
}

func TestSnapshotTreeContent(t *testing.T) {
	t.Parallel()
	dir := seedRepo(t)
	writeFile(t, dir, ".gitignore", "ignored.log\nbuild/\n")
	writeFile(t, dir, "mod.txt", "old\n")
	writeFile(t, dir, "gone.txt", "bye\n")
	writeFile(t, dir, "bin.dat", "\x00\x01\x02")
	commitAll(t, dir, "more")

	writeFile(t, dir, "mod.txt", "new\n")
	writeFile(t, dir, "bin.dat", "\x00\x01\x02\x03\xff")
	writeFile(t, dir, "crlf.txt", "one\r\ntwo\r\n")
	writeFile(t, dir, "untracked.txt", "u\n")
	writeFile(t, dir, "staged.txt", "s\n")
	gitOut(t, dir, "add", "staged.txt")
	writeFile(t, dir, "ignored.log", "x\n")
	writeFile(t, dir, "build/out.o", "x\n")
	writeFile(t, dir, "with space/file name.txt", "x\n")
	writeFile(t, dir, "new\nline.txt", "x\n")

	invalid := "bad\xffname.txt"
	invalidOK := fsAcceptsName(t, invalid)

	if invalidOK {
		writeFile(t, dir, invalid, "x\n")
	}

	if err := os.Symlink("a.txt", filepath.Join(dir, "link")); err != nil {
		t.Fatal(err)
	}

	if err := os.Remove(filepath.Join(dir, "gone.txt")); err != nil {
		t.Fatal(err)
	}

	// A nested repository that is not a submodule is left out.
	writeFile(t, dir, "nested/f.txt", "x\n")
	gitOut(t, filepath.Join(dir, "nested"), "init", "-q", "-b", "main")
	gitOut(t, filepath.Join(dir, "nested"), "add", "-A")
	gitOut(t, filepath.Join(dir, "nested"), "commit", "-q", "-m", "n")

	turn := snap(t, dir, sessA, SnapshotOptions{})
	got := treeEntries(t, dir, turn.Tree)

	wantPaths := []string{
		"a.txt", "b.txt", "dir/c.txt", "mod.txt", "bin.dat", "crlf.txt", "untracked.txt",
		"staged.txt", "link", "with space/file name.txt", "new\nline.txt", ".gitignore",
	}
	if invalidOK {
		wantPaths = append(wantPaths, invalid)
	}

	for _, p := range wantPaths {
		if _, ok := got[p]; !ok {
			t.Errorf("tree lacks %q; has %q", p, paths(got))
		}
	}

	for _, p := range []string{"gone.txt", "ignored.log", "build/out.o", "nested", "nested/f.txt"} {
		if _, ok := got[p]; ok {
			t.Errorf("tree has %q", p)
		}
	}

	if !strings.HasPrefix(got["link"], "120000 ") {
		t.Errorf("link = %q, want a symlink", got["link"])
	}

	for _, w := range []string{"mod.txt", "bin.dat", "crlf.txt"} {
		want := gitOut(t, dir, "hash-object", w)
		if !strings.HasSuffix(got[w], " "+want) {
			t.Errorf("%s = %q, want blob %s", w, got[w], want)
		}
	}
}

func TestSnapshotSubmoduleStaysAtRecordedCommit(t *testing.T) {
	t.Parallel()
	dir := seedRepo(t)

	sub := newRepo(t)
	writeFile(t, sub, "s.txt", "s\n")
	commitAll(t, sub, "sub")

	gitOut(t, dir, "-c", "protocol.file.allow=always", "submodule", "add", "-q", sub, "sm")
	gitOut(t, dir, "commit", "-q", "-m", "add submodule")

	recorded := gitOut(t, dir, "rev-parse", "HEAD:sm")

	// The submodule moves on; the superproject has not recorded it.
	writeFile(t, filepath.Join(dir, "sm"), "s.txt", "moved\n")
	gitOut(t, filepath.Join(dir, "sm"), "commit", "-q", "-am", "moved")
	writeFile(t, dir, "x.txt", "x\n")

	got := treeEntries(t, dir, snap(t, dir, sessA, SnapshotOptions{}).Tree)

	if got["sm"] != "160000 "+recorded {
		t.Errorf("sm = %q, want gitlink at %s", got["sm"], recorded)
	}

	if _, ok := got["x.txt"]; !ok {
		t.Error("untracked file missing")
	}
}

func TestSnapshotUntrackedSizeCap(t *testing.T) {
	t.Parallel()
	dir := seedRepo(t)

	big := filepath.Join(dir, "big\nfile.bin")

	f, err := os.Create(big)
	if err != nil {
		t.Fatal(err)
	}

	if err := f.Truncate(60 << 20); err != nil {
		t.Fatal(err)
	}

	if err := f.Close(); err != nil {
		t.Fatal(err)
	}

	writeFile(t, dir, "small.txt", "s\n")

	capped := snap(t, dir, sessA, SnapshotOptions{})
	if want := []string{"big\nfile.bin"}; !slices.Equal(capped.Skipped, want) {
		t.Errorf("Skipped = %q, want %q", capped.Skipped, want)
	}

	entries := treeEntries(t, dir, capped.Tree)
	if _, ok := entries["big\nfile.bin"]; ok {
		t.Error("big file in a capped tree")
	}

	if _, ok := entries["small.txt"]; !ok {
		t.Error("small file missing")
	}

	msg := gitOut(t, dir, "log", "-1", "--format=%B", capped.Commit)
	if !strings.Contains(msg, "Skipped-Count: 1") || !strings.Contains(msg, `Skipped: "big\nfile.bin"`) {
		t.Errorf("trailers do not name the skipped file:\n%s", msg)
	}

	turns, err := Turns(context.Background(), openRepo(t, dir), sessA)
	if err != nil {
		t.Fatal(err)
	}

	if !slices.Equal(turns[0].Skipped, capped.Skipped) {
		t.Errorf("Turns Skipped = %q, want %q", turns[0].Skipped, capped.Skipped)
	}

	all := snap(t, dir, sessA, SnapshotOptions{MaxUntracked: -1})
	if len(all.Skipped) != 0 {
		t.Errorf("Skipped = %q with no cap", all.Skipped)
	}

	if _, ok := treeEntries(t, dir, all.Tree)["big\nfile.bin"]; !ok {
		t.Error("big file missing with no cap")
	}
}

// userState captures everything a snapshot must leave alone.
type userState struct {
	index      []byte
	indexMtime time.Time
	stash      string
	refs       string
	head       string
	status     string
	mtimes     map[string]time.Time
	gitDirTop  []string
}

func readOrEmpty(path string) string {
	b, _ := os.ReadFile(path)

	return string(b)
}

func captureState(t *testing.T, dir string) userState {
	t.Helper()

	gitDir := filepath.Join(dir, ".git")
	s := userState{
		stash:  gitOut(t, dir, "for-each-ref", "--format=%(objectname)", "refs/stash") + readOrEmpty(filepath.Join(gitDir, "logs", "refs", "stash")),
		refs:   gitOut(t, dir, "for-each-ref", "refs/heads", "refs/tags"),
		head:   gitOut(t, dir, "symbolic-ref", "-q", "HEAD"),
		status: gitEnvOut(t, dir, []string{"GIT_OPTIONAL_LOCKS=0"}, "status", "--porcelain=v2", "--ignored"),
		mtimes: map[string]time.Time{},
	}

	var err error

	if s.index, err = os.ReadFile(filepath.Join(gitDir, "index")); err != nil {
		t.Fatal(err)
	}

	info, err := os.Stat(filepath.Join(gitDir, "index"))
	if err != nil {
		t.Fatal(err)
	}

	s.indexMtime = info.ModTime()

	err = filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}

		rel, _ := filepath.Rel(dir, p)

		if rel == ".git" {
			return filepath.SkipDir
		}

		info, err := d.Info()
		if err != nil {
			return err
		}

		s.mtimes[rel] = info.ModTime()

		return nil
	})
	if err != nil {
		t.Fatal(err)
	}

	err = filepath.WalkDir(gitDir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}

		rel, _ := filepath.Rel(gitDir, p)
		if d.IsDir() && (rel == "objects" || rel == "refs") {
			return filepath.SkipDir
		}

		s.gitDirTop = append(s.gitDirTop, rel)

		return nil
	})
	if err != nil {
		t.Fatal(err)
	}

	return s
}

func assertSameState(t *testing.T, before, after userState) {
	t.Helper()

	if !bytes.Equal(before.index, after.index) {
		t.Error("index bytes changed")
	}

	if !before.indexMtime.Equal(after.indexMtime) {
		t.Errorf("index mtime changed: %v -> %v", before.indexMtime, after.indexMtime)
	}

	for name, want := range map[string][2]string{
		"stash":  {before.stash, after.stash},
		"refs":   {before.refs, after.refs},
		"HEAD":   {before.head, after.head},
		"status": {before.status, after.status},
	} {
		if want[0] != want[1] {
			t.Errorf("%s changed:\n%s\n---\n%s", name, want[0], want[1])
		}
	}

	for p, mt := range before.mtimes {
		if got, ok := after.mtimes[p]; !ok || !got.Equal(mt) {
			t.Errorf("mtime of %q changed: %v -> %v", p, mt, got)
		}
	}

	if len(after.mtimes) != len(before.mtimes) {
		t.Errorf("worktree entries changed: %d -> %d", len(before.mtimes), len(after.mtimes))
	}

	if !slices.Equal(before.gitDirTop, after.gitDirTop) {
		t.Errorf("git dir files changed:\n%q\n%q", before.gitDirTop, after.gitDirTop)
	}
}

func TestSnapshotLeavesUserStateAlone(t *testing.T) {
	dir := seedRepo(t)

	// A stash entry seeded by hand, so no test runs git stash.
	stashed := gitOut(t, dir, "commit-tree", "-p", "HEAD", "-m", "mine", "HEAD^{tree}")
	gitOut(t, dir, "update-ref", "--create-reflog", "-m", "mine", "refs/stash", stashed)
	marks := t.TempDir()
	hooks := filepath.Join(t.TempDir(), "hooks")

	hookScript := "#!/bin/sh\necho \"$0\" >> '" + filepath.Join(marks, "mark") + "'\nexit 1\n"
	for _, h := range []string{"pre-commit", "post-index-change", "reference-transaction", "post-commit", "commit-msg"} {
		for _, d := range []string{hooks, filepath.Join(dir, ".git", "hooks")} {
			if err := os.MkdirAll(d, 0o755); err != nil {
				t.Fatal(err)
			}

			if err := os.WriteFile(filepath.Join(d, h), []byte(hookScript), 0o755); err != nil {
				t.Fatal(err)
			}
		}
	}

	fsmon := filepath.Join(t.TempDir(), "fsmon")
	if err := os.WriteFile(fsmon, []byte("#!/bin/sh\necho fsmonitor >> '"+filepath.Join(marks, "mark")+"'\n"), 0o755); err != nil {
		t.Fatal(err)
	}

	for k, v := range map[string]string{
		"commit.gpgsign":     "true",
		"gpg.program":        "/nonexistent/gpg",
		"core.hooksPath":     hooks,
		"core.fsmonitor":     fsmon,
		"user.useConfigOnly": "true",
	} {
		gitOut(t, dir, "config", k, v)
	}

	// Staged, intent-to-add, assume-unchanged and skip-worktree entries.
	writeFile(t, dir, "staged.txt", "s\n")
	gitOut(t, dir, "add", "staged.txt")
	writeFile(t, dir, "intent.txt", "i\n")
	gitOut(t, dir, "add", "-N", "intent.txt")
	gitOut(t, dir, "update-index", "--assume-unchanged", "a.txt")
	gitOut(t, dir, "update-index", "--skip-worktree", "b.txt")
	writeFile(t, dir, "a.txt", "assumed\n")
	writeFile(t, dir, "dir/c.txt", "modified\n")
	writeFile(t, dir, "untracked.txt", "u\n")

	unsetEnv(t, "GIT_AUTHOR_NAME", "GIT_AUTHOR_EMAIL", "GIT_COMMITTER_NAME", "GIT_COMMITTER_EMAIL",
		"EMAIL", "GIT_CONFIG_GLOBAL")
	t.Setenv("GIT_CONFIG_GLOBAL", os.DevNull)
	t.Setenv("HOME", t.TempDir())

	// Let the clock move so a rewritten file would show a new mtime.
	time.Sleep(20 * time.Millisecond)

	before := captureState(t, dir)
	_ = os.Remove(filepath.Join(marks, "mark"))
	turn := snap(t, dir, sessA, SnapshotOptions{})
	mark := snap(t, dir, sessA, SnapshotOptions{Unchanged: true})

	missing, err := MarkMissing(context.Background(), dir, sessA, 0, false)
	if err != nil {
		t.Fatal(err)
	}

	if b, err := os.ReadFile(filepath.Join(marks, "mark")); err == nil {
		t.Errorf("a hook or the fsmonitor ran:\n%s", b)
	}

	assertSameState(t, before, captureState(t, dir))

	if turn.N != 1 || mark.N != 2 || missing.N != 3 {
		t.Errorf("turns = %d, %d, %d", turn.N, mark.N, missing.N)
	}

	for _, c := range []string{turn.Commit, missing.Commit} {
		if raw := gitOut(t, dir, "cat-file", "commit", c); strings.Contains(raw, "gpgsig") {
			t.Errorf("commit %s is signed", c)
		}
	}

	got := treeEntries(t, dir, turn.Tree)
	if _, ok := got["untracked.txt"]; !ok {
		t.Error("untracked file missing")
	}
}

func TestSnapshotUnbornHead(t *testing.T) {
	t.Parallel()
	dir := newRepo(t)
	writeFile(t, dir, "f.txt", "f\n")
	gitOut(t, dir, "add", "f.txt")
	writeFile(t, dir, "g.txt", "g\n")

	turn := snap(t, dir, sessA, SnapshotOptions{})

	if got := gitOut(t, dir, "rev-list", "--parents", "-n1", turn.Commit); got != turn.Commit {
		t.Errorf("rev-list --parents = %q, want a commit without parents", got)
	}

	if want := scratchTree(t, dir); turn.Tree != want {
		t.Errorf("tree = %s, want %s", turn.Tree, want)
	}

	missing, err := MarkMissing(context.Background(), dir, sessB, 0, false)
	if err != nil {
		t.Fatal(err)
	}

	if got := gitOut(t, dir, "rev-list", "--parents", "-n1", missing.Commit); got != missing.Commit {
		t.Errorf("marker has parents: %q", got)
	}
}

func TestSnapshotDetachedHead(t *testing.T) {
	t.Parallel()
	dir := seedRepo(t)
	gitOut(t, dir, "checkout", "-q", "--detach")
	writeFile(t, dir, "a.txt", "changed\n")

	turn := snap(t, dir, sessA, SnapshotOptions{})

	if got := gitOut(t, dir, "rev-parse", turn.Commit+"^"); got != gitOut(t, dir, "rev-parse", "HEAD") {
		t.Errorf("parent = %s", got)
	}

	if want := scratchTree(t, dir); turn.Tree != want {
		t.Errorf("tree = %s, want %s", turn.Tree, want)
	}
}

func TestSnapshotLinkedWorktree(t *testing.T) {
	t.Parallel()
	dir := seedRepo(t)
	wt := filepath.Join(filepath.Dir(dir), filepath.Base(dir)+"-linked")
	gitOut(t, dir, "worktree", "add", "-q", "-b", "feat", wt)

	wt, err := filepath.EvalSymlinks(wt)
	if err != nil {
		t.Fatal(err)
	}

	writeFile(t, wt, "only-here.txt", "x\n")
	writeFile(t, wt, "a.txt", "linked edit\n")

	turn := snap(t, wt, sessA, SnapshotOptions{})

	if want := scratchTree(t, wt); turn.Tree != want {
		t.Errorf("tree = %s, want %s", turn.Tree, want)
	}

	want := "refs/aim/turns/" + sessA + "/" + turnKey(openRepo(t, wt).Root) + "/1/start"
	if got := gitOut(t, dir, "for-each-ref", "--format=%(refname)", "refs/aim"); got != want {
		t.Errorf("refs seen from the main checkout = %q, want %q", got, want)
	}

	if msg := gitOut(t, wt, "log", "-1", "--format=%B", turn.Commit); !strings.Contains(msg, "Worktree: "+wt) {
		t.Errorf("trailer does not name the linked worktree:\n%s", msg)
	}
}

func TestSnapshotLinkedWorktreeOfBareRepo(t *testing.T) {
	t.Parallel()
	src := seedRepo(t)
	bare := filepath.Join(t.TempDir(), "bare.git")
	gitOut(t, src, "clone", "-q", "--bare", src, bare)

	wt := filepath.Join(t.TempDir(), "wt")
	gitOut(t, bare, "worktree", "add", "-q", "-b", "work", wt)

	wt, err := filepath.EvalSymlinks(wt)
	if err != nil {
		t.Fatal(err)
	}

	writeFile(t, wt, "new.txt", "n\n")

	turn := snap(t, wt, sessA, SnapshotOptions{})

	if want := scratchTree(t, wt); turn.Tree != want {
		t.Errorf("tree = %s, want %s", turn.Tree, want)
	}

	want := "refs/aim/turns/" + sessA + "/" + turnKey(openRepo(t, wt).Root) + "/1/start"
	if got := gitOut(t, bare, "for-each-ref", "--format=%(refname)", "refs/aim"); got != want {
		t.Errorf("refs in the bare repository = %q, want %q", got, want)
	}
}

func TestSnapshotSplitIndex(t *testing.T) {
	t.Parallel()

	dir := seedRepo(t)
	gitOut(t, dir, "config", "core.splitIndex", "true")
	gitOut(t, dir, "update-index", "--split-index")
	writeFile(t, dir, "dir/c.txt", "touched\n")
	gitOut(t, dir, "add", "dir/c.txt") // a second index write leaves a delta over the shared file
	writeFile(t, dir, "a.txt", "edited\n")
	writeFile(t, dir, "u.txt", "u\n")

	shared, _ := filepath.Glob(filepath.Join(dir, ".git", "sharedindex.*"))
	if len(shared) == 0 {
		t.Fatal("setup did not produce a split index")
	}

	before := captureState(t, dir)
	ctx, logPath := wrapGit(t, "")
	turn := snapContext(ctx, t, dir, sessA, SnapshotOptions{})
	assertSameState(t, before, captureState(t, dir))

	if want := scratchTree(t, dir); turn.Tree != want {
		t.Errorf("tree = %s, want %s", turn.Tree, want)
	}

	if calls, _ := os.ReadFile(logPath); !strings.Contains(string(calls), "read-tree HEAD") {
		t.Errorf("a split index did not fall back to HEAD:\n%s", calls)
	}
}

func TestSnapshotUnusableIndex(t *testing.T) {
	t.Parallel()

	for name, content := range map[string][]byte{
		"garbage": []byte("this is not an index file at all, not even close"),
		"empty":   nil,
		"missing": nil,
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			dir := seedRepo(t)
			writeFile(t, dir, "u.txt", "u\n")

			indexPath := filepath.Join(dir, ".git", "index")
			if name == "missing" {
				if err := os.Remove(indexPath); err != nil {
					t.Fatal(err)
				}
			} else if err := os.WriteFile(indexPath, content, 0o644); err != nil {
				t.Fatal(err)
			}

			turn := snap(t, dir, sessA, SnapshotOptions{})
			if want := scratchTree(t, dir); turn.Tree != want {
				t.Errorf("tree = %s, want %s", turn.Tree, want)
			}
		})
	}
}

func TestSnapshotPathShapeChanges(t *testing.T) {
	t.Parallel()
	dir := seedRepo(t)
	writeFile(t, dir, "d/inner.txt", "i\n")
	commitAll(t, dir, "d")

	// A tracked file becomes a directory and a tracked directory becomes a file.
	if err := os.Remove(filepath.Join(dir, "a.txt")); err != nil {
		t.Fatal(err)
	}

	writeFile(t, dir, "a.txt/b", "now a dir\n")

	if err := os.RemoveAll(filepath.Join(dir, "d")); err != nil {
		t.Fatal(err)
	}

	writeFile(t, dir, "d", "now a file\n")

	turn := snap(t, dir, sessA, SnapshotOptions{})
	got := treeEntries(t, dir, turn.Tree)

	if want := scratchTree(t, dir); turn.Tree != want {
		t.Errorf("tree = %s, want %s; has %q", turn.Tree, want, paths(got))
	}

	// An emptied directory in place of a tracked file removes the file.
	dir2 := seedRepo(t)
	if err := os.Remove(filepath.Join(dir2, "a.txt")); err != nil {
		t.Fatal(err)
	}

	if err := os.Mkdir(filepath.Join(dir2, "a.txt"), 0o755); err != nil {
		t.Fatal(err)
	}

	if _, ok := treeEntries(t, dir2, snap(t, dir2, sessA, SnapshotOptions{}).Tree)["a.txt"]; ok {
		t.Error("a.txt survived as a file")
	}
}

func TestSnapshotUnchangedReusesPreviousTree(t *testing.T) {
	t.Parallel()

	dir := seedRepo(t)

	if first := snap(t, dir, sessA, SnapshotOptions{Unchanged: true}); first.N != 1 || first.Tree != scratchTree(t, dir) {
		t.Fatalf("Unchanged on the first turn must take a full snapshot, got %+v", first)
	}

	writeFile(t, dir, "big.bin", "")

	if err := os.Truncate(filepath.Join(dir, "big.bin"), 60<<20); err != nil {
		t.Fatal(err)
	}

	second := snap(t, dir, sessA, SnapshotOptions{})
	ctx, logPath := wrapGit(t, "")

	// Marker in between: the reuse skips it and takes the real snapshot's tree.
	if _, err := MarkMissing(ctx, dir, sessA, 0, false); err != nil {
		t.Fatal(err)
	}

	logBefore, _ := os.ReadFile(logPath)

	writeFile(t, dir, "ignored-by-reuse.txt", "x\n")

	fourth := snapContext(ctx, t, dir, sessA, SnapshotOptions{Unchanged: true})

	if fourth.N != 4 || fourth.Tree != second.Tree || !slices.Equal(fourth.Skipped, second.Skipped) {
		t.Errorf("fourth = %+v, want turn 4 with %s and skipped %q", fourth, second.Tree, second.Skipped)
	}

	logAfter, _ := os.ReadFile(logPath)

	calls := string(logAfter[len(logBefore):])
	if calls == "" {
		t.Fatal("the git wrapper saw no call of the unchanged snapshot")
	}

	for _, sub := range []string{"update-index", "write-tree", "ls-files"} {
		if strings.Contains(calls, sub) {
			t.Errorf("Unchanged ran %s:\n%s", sub, calls)
		}
	}
}

// A turn ended by a marker leaves no known tree, so Unchanged takes a full
// snapshot instead of reusing the turn's stale start.
func TestSnapshotUnchangedSkipsAnEndMarker(t *testing.T) {
	t.Parallel()

	dir := seedRepo(t)
	snap(t, dir, sessA, SnapshotOptions{Turn: 1})
	writeFile(t, dir, "a.txt", "changed after the start\n")

	if _, err := MarkMissing(context.Background(), dir, sessA, 1, true); err != nil {
		t.Fatal(err)
	}

	got := snap(t, dir, sessA, SnapshotOptions{Unchanged: true})
	if want := scratchTree(t, dir); got.Tree != want {
		t.Errorf("tree = %s, want a full snapshot %s", got.Tree, want)
	}
}

func TestMarkMissing(t *testing.T) {
	t.Parallel()
	dir := seedRepo(t)
	ctx := context.Background()

	snap(t, dir, sessA, SnapshotOptions{})

	m, err := MarkMissing(ctx, dir, sessA, 0, false)
	if err != nil {
		t.Fatal(err)
	}

	if m.N != 2 || !m.Missing || m.Tree != gitOut(t, dir, "mktree") {
		t.Errorf("marker = %+v", m)
	}

	msg := gitOut(t, dir, "log", "-1", "--format=%B", m.Commit)
	for _, line := range []string{"aim turn 2", "Session: " + sessA, "Worktree: " + dir, "Snapshot: none"} {
		if !strings.Contains(msg, line) {
			t.Errorf("marker message lacks %q:\n%s", line, msg)
		}
	}

	if after := snap(t, dir, sessA, SnapshotOptions{}); after.N != 3 || after.Missing {
		t.Errorf("snapshot after a marker = %+v, want turn 3", after)
	}

	turns, err := Turns(ctx, openRepo(t, dir), sessA)
	if err != nil {
		t.Fatal(err)
	}

	if len(turns) != 3 || turns[0].Missing || !turns[1].Missing || turns[2].Missing {
		t.Errorf("turns = %+v", turns)
	}
}

func TestBadSession(t *testing.T) {
	t.Parallel()
	dir := seedRepo(t)
	ctx := context.Background()
	tmp := t.TempDir()

	for _, s := range []string{"", "not-a-uuid", sessA + "x", "../../heads/main", "11111111-2222-4333-8444-55555555555*"} {
		if _, err := Snapshot(ctx, dir, s, SnapshotOptions{TempDir: tmp}); !errors.Is(err, ErrBadSession) {
			t.Errorf("Snapshot(%q) error = %v, want ErrBadSession", s, err)
		}

		if _, err := MarkMissing(ctx, dir, s, 0, false); !errors.Is(err, ErrBadSession) {
			t.Errorf("MarkMissing(%q) error = %v, want ErrBadSession", s, err)
		}

		if _, err := Turns(ctx, openRepo(t, dir), s); !errors.Is(err, ErrBadSession) {
			t.Errorf("Turns(%q) error = %v, want ErrBadSession", s, err)
		}

		if _, err := PruneSession(ctx, openRepo(t, dir), s); !errors.Is(err, ErrBadSession) {
			t.Errorf("PruneSession(%q) error = %v, want ErrBadSession", s, err)
		}
	}

	assertEmptyDir(t, tmp)

	if refs := gitOut(t, dir, "for-each-ref", "refs/aim"); refs != "" {
		t.Errorf("refs written: %s", refs)
	}
}

func TestSnapshotNotInRepo(t *testing.T) {
	t.Parallel()

	_, err := Snapshot(context.Background(), t.TempDir(), sessA, SnapshotOptions{TempDir: t.TempDir()})
	if !errors.Is(err, ErrNotRepo) {
		t.Errorf("error = %v, want ErrNotRepo", err)
	}
}

func TestSnapshotRetriesWhenTheRefExists(t *testing.T) {
	t.Parallel()

	dir := seedRepo(t)

	realGit, err := exec.LookPath("git")
	if err != nil {
		t.Skip("git not found")
	}

	// A racing writer takes number 1 right before our update-ref, once.
	racing := "refs/aim/turns/" + sessA + "/" + turnKey(openRepo(t, dir).Root) + "/1/start"
	ctx, _ := wrapGit(t, `case "$*" in
*update-ref*--stdin*)
	if [ ! -e "$LOG.raced" ]; then
		: > "$LOG.raced"
		'`+realGit+`' update-ref `+racing+` "$('`+realGit+`' rev-parse HEAD)"
	fi;;
esac`)

	turn := snapContext(ctx, t, dir, sessA, SnapshotOptions{})
	if turn.N != 2 {
		t.Errorf("turn = %d, want 2 after the retry", turn.N)
	}

	turns, err := Turns(context.Background(), openRepo(t, dir), sessA)
	if err != nil {
		t.Fatal(err)
	}

	if len(turns) != 2 || turns[1].Commit != turn.Commit {
		t.Errorf("turns = %+v", turns)
	}
}

// legacyTurnRef writes a turn ref of the earlier layout, a commit of HEAD's tree
// carrying the trailers it had, and returns the ref's name.
func legacyTurnRef(t *testing.T, dir, root, session string, n int) string {
	t.Helper()

	msg := fmt.Sprintf("aim turn %d\n\nSession: %s\nWorktree: %s\n", n, session, root)
	commit := gitInput(t, dir, msg, "commit-tree", gitOut(t, dir, "rev-parse", "HEAD^{tree}"))
	ref := fmt.Sprintf("refs/aim/%s/%d", session, n)
	gitOut(t, dir, "update-ref", ref, commit)

	return ref
}

// legacyRefs lists the refs under refs/aim in the earlier layout.
func legacyRefs(t *testing.T, dir string) []string {
	t.Helper()

	var out []string

	for ref := range strings.FieldsSeq(gitOut(t, dir, "for-each-ref", "--format=%(refname)", "refs/aim")) {
		if legacyRefRE.MatchString(ref) {
			out = append(out, ref)
		}
	}

	return out
}

func TestLegacyTurnRefsAreIgnored(t *testing.T) {
	t.Parallel()
	dir := seedRepo(t)
	r := openRepo(t, dir)
	legacyTurnRef(t, dir, r.Root, sessA, 1)

	turns, err := Turns(context.Background(), r, sessA)
	if err != nil || len(turns) != 0 {
		t.Fatalf("Turns = %+v, %v; want none", turns, err)
	}

	// The number after it ignores the earlier layout too.
	if turn := snap(t, dir, sessA, SnapshotOptions{}); turn.N != 1 {
		t.Errorf("first turn = %d, want 1", turn.N)
	}

	if turns, err := Turns(context.Background(), r, sessA); err != nil || len(turns) != 1 {
		t.Errorf("Turns = %+v, %v; want the one turn", turns, err)
	}
}

func TestPrune(t *testing.T) {
	t.Parallel()
	dir := seedRepo(t)
	ctx := context.Background()

	wt := linkedWorktree(t, dir, "other")
	r := openRepo(t, dir)

	// Session A in the main checkout: turn 1 with its end, turn 2 open. Session
	// B in the other worktree: turn 1 open and a marker. Both have a turn of the
	// earlier layout as well.
	snap(t, dir, sessA, SnapshotOptions{})
	snap(t, dir, sessA, SnapshotOptions{})
	snap(t, wt, sessB, SnapshotOptions{})

	if _, err := MarkMissing(ctx, wt, sessB, 0, false); err != nil {
		t.Fatal(err)
	}

	legacyTurnRef(t, dir, r.Root, sessA, 7)
	legacyTurnRef(t, dir, openRepo(t, wt).Root, sessB, 7)

	n, err := PruneWorktree(ctx, r, openRepo(t, wt).Root)
	if err != nil || n != 3 {
		t.Fatalf("PruneWorktree = %d, %v; want 3", n, err)
	}

	if got, _ := Turns(ctx, openRepo(t, wt), sessB); len(got) != 0 {
		t.Errorf("session B turns left: %+v", got)
	}

	if got, _ := Turns(ctx, r, sessA); len(got) != 2 {
		t.Errorf("session A turns = %+v, want both kept", got)
	}

	if got := legacyRefs(t, dir); len(got) != 1 {
		t.Errorf("earlier-layout refs = %v, want session A's kept", got)
	}

	snap(t, wt, sessB, SnapshotOptions{})

	n, err = PruneSession(ctx, r, sessA)
	if err != nil || n != 4 {
		t.Fatalf("PruneSession = %d, %v; want 4", n, err)
	}

	if got, _ := Turns(ctx, openRepo(t, wt), sessB); len(got) != 1 {
		t.Errorf("session B turns = %+v, want 1 kept", got)
	}

	if n, err := PruneSession(ctx, r, sessA); err != nil || n != 0 {
		t.Errorf("second PruneSession = %d, %v", n, err)
	}
}

func TestSnapshotCancelledLeavesNoTempFileOrRef(t *testing.T) {
	t.Parallel()
	dir := seedRepo(t)
	tmp := t.TempDir()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	if _, err := Snapshot(ctx, dir, sessA, SnapshotOptions{TempDir: tmp}); err == nil {
		t.Fatal("cancelled context did not fail the snapshot")
	}

	assertEmptyDir(t, tmp)

	if refs := gitOut(t, dir, "for-each-ref", "refs/aim"); refs != "" {
		t.Errorf("refs written: %s", refs)
	}
}

func TestTempIndexNameAndLockCleanup(t *testing.T) {
	t.Parallel()
	tmp := t.TempDir()

	idx, err := newTempIndex(tmp)
	if err != nil {
		t.Fatal(err)
	}

	base := filepath.Base(idx.path)
	if !strings.HasPrefix(base, strconv.Itoa(os.Getpid())+"-index-") {
		t.Errorf("name %q does not match <pid>-index-*", base)
	}

	if err := os.WriteFile(idx.path+".lock", nil, 0o600); err != nil {
		t.Fatal(err)
	}

	idx.remove()
	assertEmptyDir(t, tmp)
}

func TestSnapshotRefWriteSurvivesTheContextEnding(t *testing.T) {
	t.Parallel()

	dir := seedRepo(t)
	r := openRepo(t, dir)

	// update-ref outlasts the deadline; the ref write must not be killed.
	ctx, logPath := wrapGit(t, `case "$*" in
*update-ref*) sleep 1.5;;
esac`)

	ctx, cancel := context.WithTimeout(ctx, time.Second)
	defer cancel()

	turn, err := Snapshot(ctx, dir, sessA, SnapshotOptions{TempDir: t.TempDir()})
	if err != nil {
		t.Fatalf("Snapshot: %v", err)
	}

	if turn.N != 1 {
		t.Errorf("turn = %d, want 1", turn.N)
	}

	if calls := wrappedCalls(t, logPath); !strings.Contains(calls, "update-ref") {
		t.Errorf("the ref write never ran through the wrapper:\n%s", calls)
	}

	locks, err := filepath.Glob(filepath.Join(r.CommonDir, "refs", "aim", "*", "*.lock"))
	if err != nil || len(locks) > 0 {
		t.Errorf("lock files %v (%v)", locks, err)
	}

	if next := snap(t, dir, sessA, SnapshotOptions{}); next.N != 2 {
		t.Errorf("second turn = %d, want 2", next.N)
	}
}

func TestWriteStartWithDoneContextWritesNothing(t *testing.T) {
	t.Parallel()
	dir := seedRepo(t)
	r := openRepo(t, dir)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	tree := gitOut(t, dir, "rev-parse", "HEAD^{tree}")

	if _, err := writeStart(ctx, r, sessA, 0, 0, tree, nil, false); !errors.Is(err, context.Canceled) {
		t.Errorf("writeStart error = %v, want context.Canceled", err)
	}

	if _, err := MarkMissing(ctx, dir, sessA, 0, false); err == nil {
		t.Error("MarkMissing with a done context succeeded")
	}

	if refs := gitOut(t, dir, "for-each-ref", "refs/aim"); refs != "" {
		t.Errorf("refs written: %s", refs)
	}
}

// linkedWorktree adds a linked worktree on a new branch next to dir.
func linkedWorktree(t *testing.T, dir, branch string) string {
	t.Helper()

	wt := filepath.Join(t.TempDir(), branch)
	gitOut(t, dir, "worktree", "add", "-q", "-b", branch, wt)

	wt, err := filepath.EvalSymlinks(wt)
	if err != nil {
		t.Fatal(err)
	}

	return wt
}

func TestTurnsListOneWorktree(t *testing.T) {
	t.Parallel()
	dir := seedRepo(t)
	b := linkedWorktree(t, dir, "b")

	writeFile(t, dir, "a.txt", "in main\n")
	writeFile(t, b, "a.txt", "in b\n")
	snap(t, dir, sessA, SnapshotOptions{})
	snap(t, b, sessA, SnapshotOptions{})

	for name, d := range map[string]string{"main": dir, "b": b} {
		turns, err := Turns(context.Background(), openRepo(t, d), sessA)
		if err != nil {
			t.Fatal(err)
		}

		if len(turns) != 1 {
			t.Errorf("%s lists %d turns, want 1", name, len(turns))
		}
	}
}

func TestTurnRefsLayout(t *testing.T) {
	t.Parallel()

	main := seedRepo(t)
	linked := linkedWorktree(t, main, "b")

	odd := filepath.Join(t.TempDir(), "wt \u00f8 1")
	gitOut(t, main, "worktree", "add", "-q", "-b", "odd", odd)

	tests := []struct {
		name string
		dir  string
	}{
		{"main checkout", main},
		{"linked worktree", linked},
		{"worktree with a space and a non-ASCII name", odd},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			r := openRepo(t, tt.dir)
			sum := sha256.Sum256([]byte(r.Root))
			key := hex.EncodeToString(sum[:8])

			// The earlier turn of this worktree is closed by the next one.
			snap(t, tt.dir, sessA, SnapshotOptions{Turn: 1})
			snap(t, tt.dir, sessA, SnapshotOptions{Turn: 2})

			base := "refs/aim/turns/" + sessA + "/" + key + "/"
			want := []string{base + "1/end", base + "1/start", base + "2/start"}

			got := strings.Fields(gitOut(t, tt.dir, "for-each-ref", "--format=%(refname)", base))
			if !slices.Equal(got, want) {
				t.Errorf("refs = %v, want %v", got, want)
			}

			if turnRef(sessA, key, 1, true) != want[0] || turnRef(sessA, key, 1, false) != want[1] {
				t.Errorf("turnRef disagrees with the layout")
			}
		})
	}
}

func TestSnapshotEndsTheOpenTurn(t *testing.T) {
	t.Parallel()

	ctx := context.Background()

	t.Run("ends the open turn with its own tree", func(t *testing.T) {
		t.Parallel()

		dir := seedRepo(t)
		writeFile(t, dir, "a.txt", "one\n")
		first := snap(t, dir, sessA, SnapshotOptions{Turn: 1})

		writeFile(t, dir, "a.txt", "two\n")
		second := snap(t, dir, sessA, SnapshotOptions{Turn: 2, Ends: 1})

		turns, err := Turns(ctx, openRepo(t, dir), sessA)
		if err != nil || len(turns) != 2 {
			t.Fatalf("Turns = %+v, %v", turns, err)
		}

		if turns[0].Commit != first.Commit || turns[0].EndTree != second.Tree || turns[0].EndCommit == "" || turns[0].EndMissing {
			t.Errorf("turn 1 = %+v, want it ended with turn 2's tree %s", turns[0], second.Tree)
		}

		if turns[1].EndCommit != "" || !turns[1].Current || turns[0].Current {
			t.Errorf("turn 2 = %+v, want it open and current", turns[1])
		}
	})

	t.Run("changes nothing otherwise", func(t *testing.T) {
		t.Parallel()

		dir := seedRepo(t)
		other := linkedWorktree(t, dir, "b")

		snap(t, other, sessA, SnapshotOptions{Turn: 1})
		snap(t, dir, sessA, SnapshotOptions{Turn: 2})
		snap(t, dir, sessA, SnapshotOptions{Turn: 3, Ends: 2})

		if _, err := MarkMissing(ctx, dir, sessA, 4, false); err != nil {
			t.Fatal(err)
		}

		for i, ends := range []int{2, 1, 4, 99} {
			before := gitOut(t, dir, "for-each-ref", "--format=%(refname) %(objectname)", "refs/aim")

			next := 10 + i
			if _, err := Snapshot(ctx, dir, sessA, SnapshotOptions{TempDir: t.TempDir(), Turn: next, Ends: ends}); err != nil {
				t.Fatalf("Ends %d: %v", ends, err)
			}

			after := gitOut(t, dir, "for-each-ref", "--format=%(refname) %(objectname)", "refs/aim")
			if strings.Count(after, "\n") != strings.Count(before, "\n")+1 {
				t.Errorf("Ends %d changed more than the new start:\n%s\n--\n%s", ends, before, after)
			}
		}
	})
}

func TestEndTurn(t *testing.T) {
	t.Parallel()

	ctx := context.Background()

	tests := []struct {
		name      string
		setup     func(t *testing.T, dir string)
		turn      int
		unchanged bool
		wantErr   error
		wantTree  func(t *testing.T, dir string, start Turn) string
	}{
		{
			name: "ends an open turn with the working tree",
			setup: func(t *testing.T, dir string) {
				snap(t, dir, sessA, SnapshotOptions{Turn: 1})
				writeFile(t, dir, "a.txt", "changed after the start\n")
			},
			turn:     1,
			wantTree: func(t *testing.T, dir string, _ Turn) string { return scratchTree(t, dir) },
		},
		{
			name: "unchanged reuses the start's tree",
			setup: func(t *testing.T, dir string) {
				snap(t, dir, sessA, SnapshotOptions{Turn: 1})
				writeFile(t, dir, "a.txt", "x\n")
			},
			turn:      1,
			unchanged: true,
			wantTree:  func(_ *testing.T, _ string, start Turn) string { return start.Tree },
		},
		{
			name:    "refuses a turn with no start here",
			setup:   func(t *testing.T, dir string) { snap(t, dir, sessA, SnapshotOptions{Turn: 1}) },
			turn:    2,
			wantErr: ErrTurnNotOpen,
		},
		{
			name: "refuses a turn that has an end",
			setup: func(t *testing.T, dir string) {
				snap(t, dir, sessA, SnapshotOptions{Turn: 1})
				snap(t, dir, sessA, SnapshotOptions{Turn: 2, Ends: 1})
			},
			turn:    1,
			wantErr: ErrTurnNotOpen,
		},
		{
			name: "refuses a start marker",
			setup: func(t *testing.T, dir string) {
				if _, err := MarkMissing(ctx, dir, sessA, 1, false); err != nil {
					t.Fatal(err)
				}
			},
			turn:    1,
			wantErr: ErrTurnNotOpen,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			dir := seedRepo(t)
			tt.setup(t, dir)

			r := openRepo(t, dir)

			var start Turn

			if turns, _ := Turns(ctx, r, sessA); len(turns) > 0 {
				start = turns[0]
			}

			got, err := EndTurn(ctx, dir, sessA, tt.turn, SnapshotOptions{TempDir: t.TempDir(), Unchanged: tt.unchanged})
			if tt.wantErr != nil {
				if !errors.Is(err, tt.wantErr) {
					t.Fatalf("EndTurn error = %v, want %v", err, tt.wantErr)
				}

				return
			}

			if err != nil {
				t.Fatal(err)
			}

			if want := tt.wantTree(t, dir, start); got.EndTree != want || got.EndCommit == "" {
				t.Errorf("EndTurn = %+v, want end tree %s", got, want)
			}

			turns, err := Turns(ctx, r, sessA)
			if err != nil || turns[0].EndCommit != got.EndCommit || turns[0].EndTree != got.EndTree {
				t.Errorf("listed turn = %+v, %v; want the end EndTurn wrote", turns, err)
			}
		})
	}
}

func TestMarkMissingStartAndEnd(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	dir := seedRepo(t)

	snap(t, dir, sessA, SnapshotOptions{Turn: 1})

	start, err := MarkMissing(ctx, dir, sessA, 0, false)
	if err != nil || start.N != 2 || !start.Missing {
		t.Fatalf("start marker = %+v, %v; want turn 2 as a marker", start, err)
	}

	if _, err := MarkMissing(ctx, dir, sessA, 1, true); err != nil {
		t.Fatalf("end marker on the open turn: %v", err)
	}

	turns, err := Turns(ctx, openRepo(t, dir), sessA)
	if err != nil || len(turns) != 2 {
		t.Fatalf("Turns = %+v, %v", turns, err)
	}

	if !turns[0].EndMissing || turns[0].EndCommit == "" || !turns[1].Missing {
		t.Errorf("turns = %+v, want turn 1 ended by a marker and turn 2 a start marker", turns)
	}

	if _, err := MarkMissing(ctx, dir, sessA, 1, true); !errors.Is(err, ErrTurnNotOpen) {
		t.Errorf("end marker on a closed turn: error = %v, want ErrTurnNotOpen", err)
	}

	if _, err := MarkMissing(ctx, dir, sessA, 5, true); !errors.Is(err, ErrTurnNotOpen) {
		t.Errorf("end marker on a turn with no start: error = %v, want ErrTurnNotOpen", err)
	}
}

func TestOpenTurnBehindALaterTurnHasNoSnapshot(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	dir := seedRepo(t)
	b := linkedWorktree(t, dir, "b")

	writeFile(t, b, "a.txt", "in b\n")
	snap(t, b, sessA, SnapshotOptions{})
	writeFile(t, dir, "a.txt", "in main\n")
	snap(t, dir, sessA, SnapshotOptions{})

	turns, err := Turns(ctx, openRepo(t, b), sessA)
	if err != nil || len(turns) != 1 {
		t.Fatalf("Turns = %+v, %v", turns, err)
	}

	if turns[0].Current || turns[0].EndCommit != "" {
		t.Errorf("turn = %+v, want it open and not current", turns[0])
	}

	_, err = testEngine(t).Diff(ctx, TurnSource{Worktree: b, Session: sessA})
	if !errors.Is(err, ErrNoSnapshot) {
		t.Errorf("Diff error = %v, want ErrNoSnapshot", err)
	}
}

func TestOpenTurns(t *testing.T) {
	t.Parallel()

	ctx := context.Background()

	tests := []struct {
		name  string
		setup func(t *testing.T, a, b, c string)
		want  func(a, b, c string) []string
	}{
		{
			name: "open turns at the highest number in two worktrees",
			setup: func(t *testing.T, a, b, _ string) {
				snap(t, a, sessA, SnapshotOptions{Turn: 5})
				snap(t, b, sessA, SnapshotOptions{Turn: 5})
			},
			want: func(a, b, _ string) []string { return []string{a, b} },
		},
		{
			name: "a closed turn is left out",
			setup: func(t *testing.T, a, b, _ string) {
				snap(t, a, sessA, SnapshotOptions{Turn: 5})
				snap(t, b, sessA, SnapshotOptions{Turn: 5})

				if _, err := EndTurn(ctx, b, sessA, 5, SnapshotOptions{}); err != nil {
					t.Fatal(err)
				}
			},
			want: func(a, _, _ string) []string { return []string{a} },
		},
		{
			name: "an open turn behind a later one is left out",
			setup: func(t *testing.T, a, b, _ string) {
				snap(t, a, sessA, SnapshotOptions{Turn: 4})
				snap(t, b, sessA, SnapshotOptions{Turn: 5})
			},
			want: func(_, b, _ string) []string { return []string{b} },
		},
		{
			name: "a start marker is left out",
			setup: func(t *testing.T, a, _, c string) {
				snap(t, a, sessA, SnapshotOptions{Turn: 5})

				if _, err := MarkMissing(ctx, c, sessA, 5, false); err != nil {
					t.Fatal(err)
				}
			},
			want: func(a, _, _ string) []string { return []string{a} },
		},
		{
			name: "another session's turn is left out",
			setup: func(t *testing.T, a, b, _ string) {
				snap(t, a, sessA, SnapshotOptions{Turn: 5})
				snap(t, b, sessB, SnapshotOptions{Turn: 9})
			},
			want: func(a, _, _ string) []string { return []string{a} },
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			a := seedRepo(t)
			b := linkedWorktree(t, a, "b")
			c := linkedWorktree(t, a, "c")
			a = openRepo(t, a).Root

			tt.setup(t, a, b, c)

			got, err := OpenTurns(ctx, openRepo(t, a), sessA)
			if err != nil {
				t.Fatal(err)
			}

			var roots []string
			for _, o := range got {
				roots = append(roots, o.Root)
			}

			want := tt.want(a, b, c)
			slices.Sort(want)

			if !slices.Equal(roots, want) {
				t.Errorf("OpenTurns roots = %v, want %v", roots, want)
			}
		})
	}
}
