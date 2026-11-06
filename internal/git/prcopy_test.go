package git

import (
	"context"
	"encoding/base64"
	"errors"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"
)

const testToken = "aim-test-token-4f9c1d"

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

	gitOut(t, f.author, "checkout", "-q", "-B", "pr"+strconv.Itoa(n), base)
	mutate()

	gitOut(t, f.author, "add", "-A")
	gitOut(t, f.author, "commit", "-q", "--allow-empty", "-m", "pr work")
	gitOut(t, f.author, "push", "-q", "-f", "origin", "HEAD:refs/pull/"+strconv.Itoa(n)+"/head")

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

	if _, err := FetchPRCopyHead(ctx, c, n, testToken); err != nil {
		t.Fatal(err)
	}

	if err := FetchPRCopyBase(ctx, c, n, base, testToken); err != nil {
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

	if _, err := FetchPRCopyHead(context.Background(), c, 99, testToken); !errors.Is(err, ErrNoPR) {
		t.Errorf("err = %v, want ErrNoPR", err)
	}
}

func TestPRCopyHeadMoved(t *testing.T) {
	t.Parallel()

	f := newPRRemote(t)
	ctx := context.Background()
	c := loadCopy(ctx, t, copyDir(t), f, 7, f.mergeBase)

	head := f.pushPR(t, 7, f.mergeBase, func() { writeFile(t, f.author, "changed.txt", "three\n") })

	if _, err := FetchPRCopyHead(ctx, c, 7, testToken); err != nil {
		t.Fatal(err)
	}

	if err := FetchPRCopyBase(ctx, c, 7, f.mergeBase, testToken); err != nil {
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

	if _, err := FetchPRCopyHead(ctx, c, 8, testToken); err != nil {
		t.Fatal(err)
	}

	if err := FetchPRCopyBase(ctx, c, 8, f.mainTip, testToken); err != nil {
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

	for _, tt := range []struct {
		name   string
		number int
		base   string // empty: a head fetch
	}{
		{"head number 0", 0, ""},
		{"base number 0", 0, f.mergeBase},
		{"option", 7, "-x"},
		{"short id", 7, f.mergeBase[:12]},
	} {
		var err error
		if tt.base == "" {
			_, err = FetchPRCopyHead(ctx, c, tt.number, testToken)
		} else {
			err = FetchPRCopyBase(ctx, c, tt.number, tt.base, testToken)
		}

		if err == nil {
			t.Errorf("%s was accepted", tt.name)
		}
	}

	if calls := wrappedCalls(t, logPath); strings.Contains(calls, "fetch") {
		t.Errorf("a refused call ran a fetch:\n%s", calls)
	}
}

func TestPRCopyFileNamedLikeARecord(t *testing.T) {
	t.Parallel()

	f := newPRRemote(t)
	f.pushPR(t, 8, f.mergeBase, func() { writeFile(t, f.author, ":foo", "colon\n") })

	c := loadCopy(context.Background(), t, copyDir(t), f, 8, f.mergeBase)

	if res := copyDiff(t, c, 8); len(res.Files) != 1 || res.Files[0].Path != ":foo" {
		t.Errorf("files = %+v", res.Files)
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

// authLog records the Authorization header of every request a test server gets.
type authLog struct {
	mu   sync.Mutex
	auth []string
}

func (l *authLog) add(r *http.Request) {
	l.mu.Lock()
	defer l.mu.Unlock()

	l.auth = append(l.auth, r.Header.Get("Authorization"))
}

func (l *authLog) seen() []string {
	l.mu.Lock()
	defer l.mu.Unlock()

	return append([]string(nil), l.auth...)
}

func basicToken(token string) string {
	return "Basic " + base64.StdEncoding.EncodeToString([]byte("x-access-token:"+token))
}

// httpCopy opens a copy whose origin is a pull request path on base.
func httpCopy(ctx context.Context, t *testing.T, base string) PRCopy {
	t.Helper()

	c, err := OpenPRCopy(ctx, copyDir(t), base+"/o/r.git")
	if err != nil {
		t.Fatal(err)
	}

	return c
}

func TestPRCopyTokenStaysOnItsHost(t *testing.T) {
	t.Setenv("NO_PROXY", "*")
	t.Setenv("no_proxy", "*")

	for _, tt := range []struct {
		name     string
		signedIn bool
	}{
		{"after sign-in", true},
		{"before sign-in", false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			var a, b authLog

			bSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				b.add(r)
				w.Header().Set("WWW-Authenticate", `Basic realm="b"`)
				w.WriteHeader(http.StatusUnauthorized)
			}))
			defer bSrv.Close()

			// A different host name makes B another credential scope.
			bURL := strings.Replace(bSrv.URL, "127.0.0.1", "localhost", 1)

			aSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				a.add(r)

				if tt.signedIn && r.Header.Get("Authorization") == "" {
					w.Header().Set("WWW-Authenticate", `Basic realm="a"`)
					w.WriteHeader(http.StatusUnauthorized)

					return
				}

				http.Redirect(w, r, bURL+r.URL.RequestURI(), http.StatusFound) //nolint:gosec // a test server on loopback
			}))
			defer aSrv.Close()

			ctx := context.Background()
			c := httpCopy(ctx, t, aSrv.URL)

			_, err := FetchPRCopyHead(ctx, c, 7, testToken)
			if err == nil {
				t.Fatal("the fetch through the redirect succeeded")
			}

			if strings.Contains(err.Error(), testToken) {
				t.Errorf("the error holds the token: %v", err)
			}

			if tt.signedIn && !slices.Contains(a.seen(), basicToken(testToken)) {
				t.Errorf("origin saw %q, want the token as Basic x-access-token", a.seen())
			}

			if len(b.seen()) == 0 {
				t.Error("the redirect target saw no request")
			}

			for _, h := range b.seen() {
				if h != "" {
					t.Errorf("the redirect target saw Authorization %q", h)
				}
			}
		})
	}
}

func TestPRCopyTokenNotInArgvOrFiles(t *testing.T) {
	t.Parallel()

	ctx, logPath := wrapGit(t, "")
	f := newPRRemote(t)
	dir := copyDir(t)

	// The full head and base fetches, including the blob fetch over stdin.
	loadCopy(ctx, t, dir, f, 7, f.mergeBase)

	// An unreachable origin, so the error carries git's stderr.
	missing := copyDir(t)

	c, err := OpenPRCopy(ctx, missing, "file://"+filepath.Join(t.TempDir(), "missing.git"))
	if err != nil {
		t.Fatal(err)
	}

	_, err = FetchPRCopyHead(ctx, c, 99, testToken)
	if err == nil {
		t.Fatal("fetching from a missing origin succeeded")
	}

	if errors.Is(err, ErrNoPR) || !strings.Contains(err.Error(), "missing.git") {
		t.Fatalf("the failure does not surface git's stderr: %v", err)
	}

	if strings.Contains(err.Error(), testToken) {
		t.Errorf("the error holds the token: %v", err)
	}

	calls := wrappedCalls(t, logPath)
	if !strings.Contains(calls, "--stdin") {
		t.Fatalf("the blob fetch did not run:\n%s", calls)
	}

	if strings.Contains(calls, testToken) {
		t.Errorf("the token is in an argument:\n%s", calls)
	}

	for _, d := range []string{dir, missing} {
		gitDir := filepath.Join(d, ".git")

		err = filepath.WalkDir(gitDir, func(path string, e fs.DirEntry, err error) error {
			if err != nil {
				return err
			}

			if e.IsDir() && path == filepath.Join(gitDir, "objects") {
				return filepath.SkipDir
			}

			if e.Type().IsRegular() {
				data, err := os.ReadFile(path) //nolint:gosec // a test temp dir
				if err != nil {
					return err
				}

				if strings.Contains(string(data), testToken) {
					t.Errorf("the token is in %s", path)
				}
			}

			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
}

func TestPRCopyIgnoresUserRewrite(t *testing.T) {
	cfg := filepath.Join(t.TempDir(), "gitconfig")
	body := "[maintenance]\n\tauto = false\n[url \"file:///nonexistent/\"]\n\tinsteadOf = file://\n"

	if err := os.WriteFile(cfg, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}

	t.Setenv("GIT_CONFIG_GLOBAL", cfg)

	f := newPRRemote(t)
	loadCopy(context.Background(), t, copyDir(t), f, 7, f.mergeBase)
}

func TestPRCopyIgnoresUserCredentialHelper(t *testing.T) {
	marker := filepath.Join(t.TempDir(), "helper-ran")
	cfg := filepath.Join(t.TempDir(), "gitconfig")
	body := "[maintenance]\n\tauto = false\n[credential]\n\thelper = \"!f() { : > '" + marker + "'; }; f\"\n"

	if err := os.WriteFile(cfg, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}

	t.Setenv("GIT_CONFIG_GLOBAL", cfg)
	t.Setenv("NO_PROXY", "*")
	t.Setenv("no_proxy", "*")

	var srv authLog

	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		srv.add(r)

		if r.Header.Get("Authorization") == "" {
			w.Header().Set("WWW-Authenticate", `Basic realm="a"`)
			w.WriteHeader(http.StatusUnauthorized)

			return
		}

		http.NotFound(w, r)
	}))
	defer s.Close()

	ctx := context.Background()
	c := httpCopy(ctx, t, s.URL)

	if _, err := FetchPRCopyHead(ctx, c, 7, testToken); err == nil {
		t.Fatal("the fetch from a server with no repository succeeded")
	}

	if _, err := os.Stat(marker); err == nil {
		t.Error("the user's credential helper ran")
	}

	if !slices.Contains(srv.seen(), basicToken(testToken)) {
		t.Errorf("server saw %q, want AIM's token", srv.seen())
	}
}

func TestPRCopyHidesTokenFromUserHooks(t *testing.T) {
	root := t.TempDir()
	hooks := filepath.Join(root, "hooks")
	marker := filepath.Join(root, "hook-ran")
	cfg := filepath.Join(root, "gitconfig")

	if err := os.Mkdir(hooks, 0o700); err != nil {
		t.Fatal(err)
	}

	script := "#!/bin/sh\nprintf '%s' \"$AIM_PR_TOKEN\" >> '" + marker + "'\n"

	// A concurrent fork can inherit the write fd and make exec fail with ETXTBSY (go.dev/issue/22315).
	syscall.ForkLock.RLock()

	err := os.WriteFile(filepath.Join(hooks, "reference-transaction"), []byte(script), 0o755)

	syscall.ForkLock.RUnlock()

	if err != nil {
		t.Fatal(err)
	}

	body := "[maintenance]\n\tauto = false\n[core]\n\thooksPath = " + hooks + "\n"
	if err := os.WriteFile(cfg, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}

	t.Setenv("GIT_CONFIG_GLOBAL", cfg)

	f := newPRRemote(t)
	ctx := context.Background()

	c, err := OpenPRCopy(ctx, copyDir(t), f.origin)
	if err != nil {
		t.Fatal(err)
	}

	// A plain ref write proves the hook fires in this configuration.
	mktree := exec.Command("git", "mktree")
	mktree.Dir = c.Repo.Root
	mktree.Stdin = strings.NewReader("")

	tree, err := mktree.Output()
	if err != nil {
		t.Fatal(err)
	}

	gitOut(t, c.Repo.Root, "update-ref", "refs/aim/probe", strings.TrimSpace(string(tree)))

	if _, err := os.Stat(marker); err != nil {
		t.Fatalf("the user's hook did not run for a plain ref write: %v", err)
	}

	if err := os.Remove(marker); err != nil {
		t.Fatal(err)
	}

	if _, err := FetchPRCopyHead(ctx, c, 7, testToken); err != nil {
		t.Fatal(err)
	}

	if _, err := os.Stat(marker); err == nil {
		t.Error("the user's hook ran during the fetch")
	}
}

func TestFetchPRCopyNeedsAToken(t *testing.T) {
	t.Parallel()

	f := newPRRemote(t)
	ctx, logPath := wrapGit(t, "")

	c, err := OpenPRCopy(ctx, copyDir(t), f.origin)
	if err != nil {
		t.Fatal(err)
	}

	if _, err := FetchPRCopyHead(ctx, c, 7, ""); !errors.Is(err, errNoToken) {
		t.Errorf("head err = %v, want errNoToken", err)
	}

	if err := FetchPRCopyBase(ctx, c, 7, f.mergeBase, ""); !errors.Is(err, errNoToken) {
		t.Errorf("base err = %v, want errNoToken", err)
	}

	if calls := wrappedCalls(t, logPath); strings.Contains(calls, "fetch") {
		t.Errorf("a refused call ran a fetch:\n%s", calls)
	}
}

func TestSweepPRCopies(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 11, 6, 12, 0, 0, 0, time.UTC)
	old := now.Add(-prCopyStaleAge - time.Hour)
	fresh := now.Add(-prCopyStaleAge + time.Hour)

	mkCopy := func(t *testing.T, root, rel string, used time.Time) string {
		t.Helper()

		dir := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Join(dir, ".git"), 0o700); err != nil {
			t.Fatal(err)
		}

		marker := filepath.Join(dir, ".git", prCopyMarker)
		if err := os.WriteFile(marker, nil, 0o600); err != nil {
			t.Fatal(err)
		}

		if err := os.Chtimes(marker, used, used); err != nil {
			t.Fatal(err)
		}

		return dir
	}

	exists := func(p string) bool {
		_, err := os.Lstat(p)

		return err == nil
	}

	tests := []struct {
		name    string
		setup   func(t *testing.T, root string)
		removed int
		gone    []string
		kept    []string
	}{
		{
			name:    "stale marker is removed with its empty owner and host",
			setup:   func(t *testing.T, root string) { mkCopy(t, root, "h/o/n", old) },
			removed: 1,
			gone:    []string{"h/o/n", "h/o", "h"},
		},
		{
			name: "fresh marker is kept",
			setup: func(t *testing.T, root string) {
				mkCopy(t, root, "h/o/n", fresh)
			},
			kept: []string{"h/o/n"},
		},
		{
			name: "owner with a kept copy stays",
			setup: func(t *testing.T, root string) {
				mkCopy(t, root, "h/o/old", old)
				mkCopy(t, root, "h/o/new", fresh)
			},
			removed: 1,
			gone:    []string{"h/o/old"},
			kept:    []string{"h/o/new", "h/o", "h"},
		},
		{
			name: "interrupted creation older than the limit is removed",
			setup: func(t *testing.T, root string) {
				dir := filepath.Join(root, "h", "o", "n")
				if err := os.MkdirAll(dir, 0o700); err != nil {
					t.Fatal(err)
				}

				if err := os.Chtimes(dir, old, old); err != nil {
					t.Fatal(err)
				}
			},
			removed: 1,
			gone:    []string{"h/o/n"},
		},
		{
			name: "interrupted creation newer than the limit is kept",
			setup: func(t *testing.T, root string) {
				dir := filepath.Join(root, "h", "o", "n")
				if err := os.MkdirAll(dir, 0o700); err != nil {
					t.Fatal(err)
				}

				if err := os.Chtimes(dir, fresh, fresh); err != nil {
					t.Fatal(err)
				}
			},
			kept: []string{"h/o/n"},
		},
		{
			name: "symlink in place of a copy is left and not followed",
			setup: func(t *testing.T, root string) {
				outside := mkCopy(t, t.TempDir(), "x", old)
				if err := os.MkdirAll(filepath.Join(root, "h", "o"), 0o700); err != nil {
					t.Fatal(err)
				}

				if err := os.Symlink(outside, filepath.Join(root, "h", "o", "n")); err != nil {
					t.Fatal(err)
				}
			},
			kept: []string{"h/o/n", "h/o/n/.git"},
		},
		{
			name: "regular file in place of a copy is left",
			setup: func(t *testing.T, root string) {
				if err := os.MkdirAll(filepath.Join(root, "h", "o"), 0o700); err != nil {
					t.Fatal(err)
				}

				p := filepath.Join(root, "h", "o", "n")
				if err := os.WriteFile(p, nil, 0o600); err != nil {
					t.Fatal(err)
				}

				if err := os.Chtimes(p, old, old); err != nil {
					t.Fatal(err)
				}
			},
			kept: []string{"h/o/n"},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			root := t.TempDir()
			tc.setup(t, root)

			n, err := SweepPRCopies(root, now)
			if err != nil {
				t.Fatal(err)
			}

			if n != tc.removed {
				t.Errorf("removed = %d, want %d", n, tc.removed)
			}

			for _, p := range tc.gone {
				if exists(filepath.Join(root, filepath.FromSlash(p))) {
					t.Errorf("%s should be gone", p)
				}
			}

			for _, p := range tc.kept {
				if !exists(filepath.Join(root, filepath.FromSlash(p))) {
					t.Errorf("%s should be kept", p)
				}
			}
		})
	}

	t.Run("missing root", func(t *testing.T) {
		t.Parallel()

		n, err := SweepPRCopies(filepath.Join(t.TempDir(), "none"), now)
		if n != 0 || err != nil {
			t.Errorf("got %d, %v; want 0, nil", n, err)
		}
	})

	t.Run("empty root", func(t *testing.T) {
		t.Parallel()

		if n, err := SweepPRCopies("", now); n != 0 || err != nil {
			t.Errorf("got %d, %v; want 0, nil", n, err)
		}
	})
}
