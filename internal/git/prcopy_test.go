package git

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

// prRemote is a bare "GitHub" with partial clone allowed: a merge base on main,
// main moved past it, and pull request 7 one commit past the merge base that
// changes changed.txt and leaves same.txt alone.
type prRemote struct {
	bare, author, origin string
	mergeBase, mainTip   string
	sameOID              string
}

func newPRRemote(t *testing.T) prRemote {
	t.Helper()

	bare := newBare(t)
	gitOut(t, bare, "config", "uploadpack.allowFilter", "true")
	gitOut(t, bare, "config", "uploadpack.allowAnySHA1InWant", "true")

	author := newRepo(t)
	writeFile(t, author, "changed.txt", "one\n")
	writeFile(t, author, "same.txt", "never touched\n")
	writeFile(t, author, "move.txt", "to be moved, long enough to be an exact rename\n")
	writeFile(t, author, "mode.txt", "mode only\n")
	commitAll(t, author, "base")
	gitOut(t, author, "remote", "add", "origin", bare)
	gitOut(t, author, "push", "-q", "origin", "main")

	f := prRemote{
		bare:      bare,
		author:    author,
		origin:    "file://" + bare,
		mergeBase: gitOut(t, author, "rev-parse", "HEAD"),
		sameOID:   gitOut(t, author, "rev-parse", "HEAD:same.txt"),
	}

	f.pushPR(t, 7, f.mergeBase, func() { writeFile(t, author, "changed.txt", "two\n") })

	gitOut(t, author, "checkout", "-q", "main")
	writeFile(t, author, "later.txt", "later\n")
	commitAll(t, author, "main moves on")
	gitOut(t, author, "push", "-q", "origin", "main")

	f.mainTip = gitOut(t, author, "rev-parse", "HEAD")

	return f
}

// pushPR commits mutate's change on top of base and force-pushes it as the head
// of pull request n.
func (f prRemote) pushPR(t *testing.T, n int, base string, mutate func()) string {
	t.Helper()

	gitOut(t, f.author, "checkout", "-q", "-B", "pr"+string(rune('0'+n)), base)
	mutate()

	gitOut(t, f.author, "add", "-A")
	gitOut(t, f.author, "commit", "-q", "--allow-empty", "-m", "pr work")
	gitOut(t, f.author, "push", "-q", "-f", "origin", "HEAD:refs/pull/"+string(rune('0'+n))+"/head")

	return gitOut(t, f.author, "rev-parse", "HEAD")
}

func copyDir(t *testing.T) string {
	t.Helper()

	return filepath.Join(t.TempDir(), "prs", "github.com", "o", "name")
}

// loadCopy opens a copy and fetches pull request n over base.
func loadCopy(ctx context.Context, t *testing.T, dir string, f prRemote, n int, base string) PRCopy {
	t.Helper()

	c, err := OpenPRCopy(ctx, dir, f.origin)
	if err != nil {
		t.Fatal(err)
	}

	if _, err := FetchPRCopyHead(ctx, c, n); err != nil {
		t.Fatal(err)
	}

	if err := FetchPRCopyBase(ctx, c, n, base); err != nil {
		t.Fatal(err)
	}

	return c
}

func copyDiff(t *testing.T, c PRCopy, n int) Result {
	t.Helper()

	res, err := NewEngine(EngineOptions{}).Diff(context.Background(), PRSource{Worktree: c.Repo.Root, Number: n, Copy: true})
	if err != nil {
		t.Fatal(err)
	}

	return res
}

func blobPresent(dir, oid string) bool {
	cmd := exec.Command("git", "cat-file", "-e", oid)
	cmd.Dir = dir

	return cmd.Run() == nil
}

func TestOpenPRCopyMakesAnEmptyRepo(t *testing.T) {
	t.Parallel()

	f := newPRRemote(t)
	dir := copyDir(t)

	c, err := OpenPRCopy(context.Background(), dir, f.origin)
	if err != nil {
		t.Fatal(err)
	}

	if got := c.Repo.Name(); got != "name" {
		t.Errorf("Name = %q", got)
	}

	for _, sub := range []string{"hooks", "info"} {
		if _, err := os.Stat(filepath.Join(c.Repo.GitDir, sub)); err == nil {
			t.Errorf(".git/%s exists", sub)
		}
	}

	for key, want := range map[string]string{
		"remote.origin.url": f.origin,
		"gc.auto":           "0",
		"maintenance.auto":  "false",
	} {
		if got := gitOut(t, dir, "config", "--get", key); got != want {
			t.Errorf("%s = %q, want %q", key, got, want)
		}
	}

	check := exec.Command("git", "rev-parse", "--verify", "--quiet", "HEAD")
	check.Dir = dir

	if check.Run() == nil {
		t.Error("HEAD resolves in an empty copy")
	}

	again, err := OpenPRCopy(context.Background(), dir, f.origin)
	if err != nil {
		t.Fatal(err)
	}

	if again != c {
		t.Errorf("second open = %+v, want %+v", again, c)
	}
}

func TestOpenPRCopyInsideAnotherRepo(t *testing.T) {
	t.Parallel()

	f := newPRRemote(t)
	parent := newRepo(t)
	writeFile(t, parent, "p.txt", "p\n")
	commitAll(t, parent, "parent")

	refsBefore := refs(t, parent)

	cfgBefore, err := os.ReadFile(filepath.Join(parent, ".git", "config"))
	if err != nil {
		t.Fatal(err)
	}

	dir := filepath.Join(parent, "sub", "copy")
	c := loadCopy(context.Background(), t, dir, f, 7, f.mergeBase)

	if c.Repo.Root != dir {
		t.Errorf("Root = %q, want %q", c.Repo.Root, dir)
	}

	if got := refs(t, parent); got != refsBefore {
		t.Errorf("the parent's refs changed:\n%s", got)
	}

	cfgAfter, err := os.ReadFile(filepath.Join(parent, ".git", "config"))
	if err != nil {
		t.Fatal(err)
	}

	if string(cfgAfter) != string(cfgBefore) {
		t.Errorf("the parent's config changed:\n%s", cfgAfter)
	}
}

func TestOpenPRCopyMarksUse(t *testing.T) {
	t.Parallel()

	f := newPRRemote(t)
	dir := copyDir(t)

	c, err := OpenPRCopy(context.Background(), dir, f.origin)
	if err != nil {
		t.Fatal(err)
	}

	marker := filepath.Join(c.Repo.GitDir, prCopyMarker)
	old := time.Unix(1_000_000_000, 0)

	if err := os.Chtimes(marker, old, old); err != nil {
		t.Fatal(err)
	}

	if _, err := OpenPRCopy(context.Background(), dir, f.origin); err != nil {
		t.Fatal(err)
	}

	info, err := os.Stat(marker)
	if err != nil {
		t.Fatal(err)
	}

	if !info.ModTime().After(old) {
		t.Errorf("marker mtime = %v, not moved past %v", info.ModTime(), old)
	}
}

func TestPRCopyDirRefuses(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name              string
		root              string
		host, owner, repo string
		want              string
		ok                bool
	}{
		{"normal", "/r", "github.com", "o", "n", "/r/github.com/o/n", true},
		{"dotdot", "/r", "github.com", "..", "n", "", false},
		{"dot", "/r", ".", "o", "n", "", false},
		{"separator", "/r", "github.com", "o", "a/b", "", false},
		{"empty segment", "/r", "github.com", "", "n", "", false},
		{"empty root", "", "github.com", "o", "n", "", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got, ok := PRCopyDir(tt.root, tt.host, tt.owner, tt.repo)
			if ok != tt.ok || got != tt.want {
				t.Errorf("PRCopyDir = %q, %v; want %q, %v", got, ok, tt.want, tt.ok)
			}
		})
	}
}

func TestPRCopyDiffMatchesFullFetch(t *testing.T) {
	t.Parallel()

	f := newPRRemote(t)
	ctx := context.Background()

	full := t.TempDir()
	gitOut(t, full, "clone", "-q", f.bare, ".")

	if _, err := FetchPR(ctx, openRepo(t, full), "origin", 7, "main"); err != nil {
		t.Fatal(err)
	}

	e := NewEngine(EngineOptions{})

	want, err := e.Diff(ctx, PRSource{Worktree: full, Number: 7})
	if err != nil {
		t.Fatal(err)
	}

	got := copyDiff(t, loadCopy(ctx, t, copyDir(t), f, 7, f.mergeBase), 7)

	if got.Old != want.Old || got.New != want.New || got.Head != want.Head {
		t.Errorf("trees/head = %s %s %s, want %s %s %s", got.Old, got.New, got.Head, want.Old, want.New, want.Head)
	}

	if len(got.Files) == 0 || len(got.Files) != len(want.Files) {
		t.Fatalf("%d files, want %d", len(got.Files), len(want.Files))
	}

	type view struct {
		Path          string
		Status        Status
		Added, Remove int
		Hunks         []Hunk
	}

	project := func(fs []FileDiff) []view {
		var vs []view
		for _, fd := range fs {
			vs = append(vs, view{fd.Path, fd.Status, fd.Added, fd.Removed, fd.Hunks})
		}

		return vs
	}

	if g, w := project(got.Files), project(want.Files); !reflect.DeepEqual(g, w) {
		t.Errorf("files differ:\n got %+v\nwant %+v", g, w)
	}
}

func TestPRCopyFetchesOnlyChangedBlobs(t *testing.T) {
	t.Parallel()

	f := newPRRemote(t)
	c := loadCopy(context.Background(), t, copyDir(t), f, 7, f.mergeBase)
	res := copyDiff(t, c, 7)

	if len(res.Files) != 1 {
		t.Fatalf("%d files", len(res.Files))
	}

	for _, oid := range []string{res.Files[0].OldOID, res.Files[0].NewOID} {
		if !blobPresent(c.Repo.Root, oid) {
			t.Errorf("blob %s is missing", oid)
		}
	}

	if blobPresent(c.Repo.Root, f.sameOID) {
		t.Error("the unchanged file's blob was fetched")
	}
}

func TestPRCopyPureRenameAndModeHaveText(t *testing.T) {
	t.Parallel()

	f := newPRRemote(t)
	f.pushPR(t, 8, f.mergeBase, func() {
		gitOut(t, f.author, "mv", "move.txt", "moved.txt")

		if err := os.Chmod(filepath.Join(f.author, "mode.txt"), 0o755); err != nil {
			t.Fatal(err)
		}
	})

	c := loadCopy(context.Background(), t, copyDir(t), f, 8, f.mergeBase)
	res := copyDiff(t, c, 8)

	if len(res.Files) != 2 {
		t.Fatalf("%d files, want the rename and the mode change", len(res.Files))
	}

	e := NewEngine(EngineOptions{})

	for _, fd := range res.Files {
		old, nw, err := e.FileText(context.Background(), c.Repo.Root, fd)
		if err != nil {
			t.Fatalf("%s: %v", fd.Path, err)
		}

		if len(old) == 0 || len(nw) == 0 {
			t.Errorf("%s: old %d lines, new %d lines", fd.Path, len(old), len(nw))
		}
	}
}

func TestPRCopyReadsNeverFetch(t *testing.T) {
	t.Parallel()

	f := newPRRemote(t)
	c := loadCopy(context.Background(), t, copyDir(t), f, 7, f.mergeBase)

	_, _, err := NewEngine(EngineOptions{}).FileText(context.Background(), c.Repo.Root, FileDiff{Path: "same.txt", OldOID: f.sameOID})
	if err == nil {
		t.Error("FileText of a blob the copy lacks succeeded")
	}

	if blobPresent(c.Repo.Root, f.sameOID) {
		t.Error("a read fetched the blob")
	}

	cfg, err := os.ReadFile(filepath.Join(c.Repo.GitDir, "config"))
	if err != nil {
		t.Fatal(err)
	}

	for _, key := range []string{"promisor", "partialclonefilter"} {
		if strings.Contains(string(cfg), key) {
			t.Errorf("config holds %s:\n%s", key, cfg)
		}
	}
}

func TestFetchPRCopyHeadNoSuchPR(t *testing.T) {
	t.Parallel()

	f := newPRRemote(t)

	c, err := OpenPRCopy(context.Background(), copyDir(t), f.origin)
	if err != nil {
		t.Fatal(err)
	}

	if _, err := FetchPRCopyHead(context.Background(), c, 99); !errors.Is(err, ErrNoPR) {
		t.Errorf("err = %v, want ErrNoPR", err)
	}
}

func TestPRCopyHeadMoved(t *testing.T) {
	t.Parallel()

	f := newPRRemote(t)
	ctx := context.Background()
	c := loadCopy(ctx, t, copyDir(t), f, 7, f.mergeBase)

	head := f.pushPR(t, 7, f.mergeBase, func() { writeFile(t, f.author, "changed.txt", "three\n") })

	if _, err := FetchPRCopyHead(ctx, c, 7); err != nil {
		t.Fatal(err)
	}

	if err := FetchPRCopyBase(ctx, c, 7, f.mergeBase); err != nil {
		t.Fatal(err)
	}

	if got := copyDiff(t, c, 7).Head; got != head {
		t.Errorf("Head = %s, want %s", got, head)
	}
}

func TestPRCopySecondPullRequest(t *testing.T) {
	t.Parallel()

	f := newPRRemote(t)
	ctx := context.Background()
	c := loadCopy(ctx, t, copyDir(t), f, 7, f.mergeBase)

	f.pushPR(t, 8, f.mainTip, func() { writeFile(t, f.author, "later.txt", "later, changed\n") })

	if _, err := FetchPRCopyHead(ctx, c, 8); err != nil {
		t.Fatal(err)
	}

	if err := FetchPRCopyBase(ctx, c, 8, f.mainTip); err != nil {
		t.Fatal(err)
	}

	if res := copyDiff(t, c, 8); len(res.Files) != 1 || res.Files[0].Path != "later.txt" {
		t.Errorf("second pull request files = %+v", res.Files)
	}

	if res := copyDiff(t, c, 7); len(res.Files) != 1 || res.Files[0].Path != "changed.txt" {
		t.Errorf("first pull request files = %+v", res.Files)
	}
}

func TestFetchPRCopyRefuses(t *testing.T) {
	t.Parallel()

	f := newPRRemote(t)
	ctx, logPath := wrapGit(t, "")

	c, err := OpenPRCopy(ctx, copyDir(t), f.origin)
	if err != nil {
		t.Fatal(err)
	}

	if _, err := FetchPRCopyHead(ctx, c, 0); err == nil {
		t.Error("number 0 was accepted")
	}

	for _, tt := range []struct {
		name   string
		number int
		base   string
	}{
		{"number 0", 0, f.mergeBase},
		{"option", 7, "-x"},
		{"short id", 7, f.mergeBase[:12]},
	} {
		if err := FetchPRCopyBase(ctx, c, tt.number, tt.base); err == nil {
			t.Errorf("%s was accepted", tt.name)
		}
	}

	if calls := wrappedCalls(t, logPath); strings.Contains(calls, "fetch") {
		t.Errorf("a refused call ran a fetch:\n%s", calls)
	}
}

func TestPRCopyNoTreeChange(t *testing.T) {
	t.Parallel()

	f := newPRRemote(t)
	f.pushPR(t, 8, f.mergeBase, func() {})
	ctx, logPath := wrapGit(t, "")

	c := loadCopy(ctx, t, copyDir(t), f, 8, f.mergeBase)

	if calls := wrappedCalls(t, logPath); strings.Contains(calls, "--stdin") {
		t.Errorf("a fetch --stdin ran for an empty diff:\n%s", calls)
	}

	if res := copyDiff(t, c, 8); len(res.Files) != 0 {
		t.Errorf("files = %+v, want none", res.Files)
	}
}

func TestPRCopyListWorktrees(t *testing.T) {
	t.Parallel()

	f := newPRRemote(t)
	c := loadCopy(context.Background(), t, copyDir(t), f, 7, f.mergeBase)

	list, err := ListWorktrees(context.Background(), c.Repo)
	if err != nil {
		t.Fatal(err)
	}

	if len(list) != 1 || !list[0].Main || list[0].Branch != prCopyBranch {
		t.Errorf("worktrees = %+v", list)
	}
}
