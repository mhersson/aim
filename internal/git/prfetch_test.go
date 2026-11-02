package git

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// prFixture is a bare "github" repository whose pull request 7 head holds one
// commit beyond main, a main that moved after, and a clone with the bare as origin.
type prFixture struct {
	bare, author, work string
	repo               Repo
}

func newPRFixture(t *testing.T) prFixture {
	t.Helper()

	bare := newBare(t)

	author := newRepo(t)
	writeFile(t, author, "a.txt", "a\n")
	writeFile(t, author, "b.txt", "b\n")
	commitAll(t, author, "init")
	gitOut(t, author, "remote", "add", "origin", bare)
	gitOut(t, author, "push", "-q", "origin", "main")

	gitOut(t, author, "switch", "-q", "-c", "feat")
	writeFile(t, author, "a.txt", "a changed\n")
	writeFile(t, author, "new.txt", "new\n")
	commitAll(t, author, "feat work")
	gitOut(t, author, "push", "-q", "origin", "feat:refs/pull/7/head")

	gitOut(t, author, "switch", "-q", "main")
	writeFile(t, author, "later.txt", "later\n")
	commitAll(t, author, "main moves on")

	work := t.TempDir()
	gitOut(t, work, "clone", "-q", bare, ".")

	gitOut(t, author, "push", "-q", "origin", "main")

	// Tagged after the clone, so only a fetch that follows tags brings them in.
	gitOut(t, author, "tag", "v-main", "main")
	gitOut(t, author, "tag", "v-pr", "feat")
	gitOut(t, author, "push", "-q", "origin", "v-main", "v-pr")

	return prFixture{bare: bare, author: author, work: work, repo: openRepo(t, work)}
}

// lightRemoteRepo is a copied seed repository with an empty bare repository as
// origin.
func lightRemoteRepo(t *testing.T) string {
	t.Helper()

	dir := seedRepo(t)
	gitOut(t, dir, "remote", "add", "origin", newBare(t))

	return dir
}

// prRefsRepo is a copied seed repository holding the refs FetchPR writes for
// pull request 7: a head one commit beyond main and a base main that moved after.
func prRefsRepo(t *testing.T) (dir string, head string) {
	t.Helper()

	dir = seedRepo(t)
	gitOut(t, dir, "switch", "-q", "-c", "feat")
	writeFile(t, dir, "a.txt", "a changed\n")
	writeFile(t, dir, "new.txt", "new\n")
	commitAll(t, dir, "feat work")

	head = gitOut(t, dir, "rev-parse", "HEAD")

	gitOut(t, dir, "switch", "-q", "main")
	writeFile(t, dir, "later.txt", "later\n")
	commitAll(t, dir, "main moves on")

	gitOut(t, dir, "update-ref", prRef(7, "head"), head)
	gitOut(t, dir, "update-ref", prRef(7, "base"), gitOut(t, dir, "rev-parse", "HEAD"))

	return dir, head
}

func TestFetchPRWritesOnlyItsRefs(t *testing.T) {
	t.Parallel()
	f := newPRFixture(t)
	before := refs(t, f.work)
	remoteMain := gitOut(t, f.work, "rev-parse", "refs/remotes/origin/main")

	got, err := FetchPR(context.Background(), f.repo, "origin", 7, "main")
	if err != nil {
		t.Fatal(err)
	}

	after := refs(t, f.work)

	was := map[string]bool{}
	for line := range strings.SplitSeq(before, "\n") {
		was[line] = true
	}

	var added []string

	for line := range strings.SplitSeq(after, "\n") {
		if !was[line] {
			added = append(added, strings.Fields(line)[0])
		}
	}

	want := []string{"refs/aim/pr/7/base", "refs/aim/pr/7/head"}
	if strings.Join(added, " ") != strings.Join(want, " ") {
		t.Errorf("new refs = %v, want %v", added, want)
	}

	if now := gitOut(t, f.work, "rev-parse", "refs/remotes/origin/main"); now != remoteMain {
		t.Errorf("origin/main moved to %s, was %s", now, remoteMain)
	}

	if _, err := os.Stat(filepath.Join(f.repo.GitDir, "FETCH_HEAD")); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("FETCH_HEAD exists: %v", err)
	}

	if got.Head != gitOut(t, f.bare, "rev-parse", "refs/pull/7/head") {
		t.Errorf("Head = %s", got.Head)
	}

	if got.Base != gitOut(t, f.bare, "rev-parse", "refs/heads/main") {
		t.Errorf("Base = %s, want the moved main", got.Base)
	}

	gitOut(t, f.author, "switch", "-q", "feat")
	writeFile(t, f.author, "a.txt", "rewritten\n")
	gitOut(t, f.author, "commit", "-q", "--amend", "-a", "-m", "rewritten")
	gitOut(t, f.author, "push", "-q", "-f", "origin", "feat:refs/pull/7/head")

	second, err := FetchPR(context.Background(), f.repo, "origin", 7, "main")
	if err != nil {
		t.Fatal(err)
	}

	if second.Head == got.Head {
		t.Fatal("head did not move")
	}

	if ref := gitOut(t, f.work, "rev-parse", "refs/aim/pr/7/head"); ref != second.Head {
		t.Errorf("ref = %s, want %s", ref, second.Head)
	}
}

func TestFetchPRMissing(t *testing.T) {
	t.Parallel()

	_, err := FetchPR(context.Background(), openRepo(t, lightRemoteRepo(t)), "origin", 99, "main")
	if !errors.Is(err, ErrNoPR) {
		t.Errorf("err = %v, want ErrNoPR", err)
	}
}

func TestFetchPRRefusesBadInput(t *testing.T) {
	t.Parallel()

	work := lightRemoteRepo(t)
	repo := openRepo(t, work)
	before := refs(t, work)
	ctx, calls := wrapGit(t, "")
	credURL := "https://user:secret@example.invalid/o/r.git" //nolint:gosec // G101: fixture URL, no real credential

	tests := []struct {
		name, remote, base string
		number             int
		want               error // nil: any error
	}{
		{"zero", "origin", "main", 0, nil},
		{"base option", "origin", "-main", 7, nil},
		{"remote option", "--upload-pack=x", "main", 7, ErrNoRemote},
		{"unknown remote", "nope", "main", 7, ErrNoRemote},
		{"url as remote", credURL, "main", 7, ErrNoRemote},
		{"base refspec", "origin", "main:refs/heads/x", 7, ErrBadBranch},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := FetchPR(ctx, repo, tt.remote, tt.number, tt.base)
			if err == nil {
				t.Fatal("err = nil")
			}

			if tt.want != nil && !errors.Is(err, tt.want) {
				t.Errorf("err = %v, want %v", err, tt.want)
			}

			if strings.Contains(err.Error(), "secret") {
				t.Errorf("error carries the URL: %v", err)
			}
		})
	}

	if after := refs(t, work); after != before {
		t.Errorf("refs changed:\n%s\n%s", before, after)
	}

	log, err := os.ReadFile(calls)
	if err != nil {
		t.Fatal(err)
	}

	for line := range strings.SplitSeq(string(log), "\n") {
		if strings.Contains(line, "fetch") {
			t.Errorf("a fetch ran: %s", line)
		}
	}
}

func TestFetchPRErrorHidesTheURL(t *testing.T) {
	work := lightRemoteRepo(t)
	clearSSHEnv(t)
	gitOut(t, work, "remote", "add", "secret", "ssh://example.invalid/o/r.git")
	gitOut(t, work, "config", "core.sshCommand", writeScript(t,
		`echo "fatal: unable to access 'https://user:secret@example.invalid/o/r.git/'" >&2; exit 1`))

	_, err := FetchPR(context.Background(), openRepo(t, work), "secret", 7, "main")
	if err == nil {
		t.Fatal("err = nil")
	}

	for _, leak := range []string{"user:secret", "example.invalid/o/r"} {
		if strings.Contains(err.Error(), leak) {
			t.Errorf("error %q contains %q", err, leak)
		}
	}
}

func TestDiffPRSourceShowsOnlyThePR(t *testing.T) {
	t.Parallel()
	work, head := prRefsRepo(t)

	e := testEngine(t)
	src := PRSource{Worktree: work, Number: 7}
	res := diffOK(t, e, src)

	var paths []string
	for _, file := range res.Files {
		paths = append(paths, file.Path)
	}

	if strings.Join(paths, " ") != "a.txt new.txt" {
		t.Errorf("files = %v, want a.txt new.txt", paths)
	}

	if res.Head != head {
		t.Errorf("Head = %s, want %s", res.Head, head)
	}

	if res.Live {
		t.Error("Live = true")
	}

	got, err := e.Update(context.Background(), res, src, Batch{Paths: []string{"a.txt"}})
	if err != nil {
		t.Fatal(err)
	}

	if got.Head != res.Head || len(got.Files) != len(res.Files) {
		t.Errorf("Update = %d files, head %s; want %d files, head %s", len(got.Files), got.Head, len(res.Files), res.Head)
	}
}

func TestDiffPRSourceNotFetched(t *testing.T) {
	t.Parallel()

	_, err := testEngine(t).Diff(context.Background(), PRSource{Worktree: seedRepo(t), Number: 7})
	if !errors.Is(err, ErrNotFetched) {
		t.Errorf("err = %v, want ErrNotFetched", err)
	}
}

func TestDeletePRRefs(t *testing.T) {
	t.Parallel()
	work, _ := prRefsRepo(t)
	repo := openRepo(t, work)

	for range 2 {
		if err := DeletePRRefs(context.Background(), repo, 7); err != nil {
			t.Fatal(err)
		}

		if got := gitOut(t, work, "for-each-ref", "refs/aim/"); got != "" {
			t.Errorf("refs left: %s", got)
		}
	}
}

func TestDeletePRRefsSurvivesTheContextEnding(t *testing.T) {
	t.Parallel()
	work, _ := prRefsRepo(t)
	repo := openRepo(t, work)

	// The context ends while update-ref is blocked; the ref write must not be killed.
	ctx, started, release := blockRefWrite(t)
	ctx, cancel := context.WithCancel(ctx)

	defer cancel()

	err := endContextDuringRefWrite(t, cancel, started, release, func() error {
		return DeletePRRefs(ctx, repo, 7)
	})
	if err != nil {
		t.Fatalf("DeletePRRefs: %v", err)
	}

	if got := gitOut(t, work, "for-each-ref", "refs/aim/"); got != "" {
		t.Errorf("refs left: %s", got)
	}

	locks, err := filepath.Glob(filepath.Join(repo.CommonDir, "refs", "aim", "pr", "7", "*.lock"))
	if err != nil || len(locks) > 0 {
		t.Errorf("lock files %v (%v)", locks, err)
	}
}

// No other test reaches refWriteContext with a context that is already done.
func TestDeletePRRefsWithDoneContextWritesNothing(t *testing.T) {
	t.Parallel()
	work, _ := prRefsRepo(t)

	ctx, logPath := wrapGit(t, "")

	ctx, cancel := context.WithCancel(ctx)
	cancel()

	if err := DeletePRRefs(ctx, openRepo(t, work), 7); !errors.Is(err, context.Canceled) {
		t.Errorf("DeletePRRefs error = %v, want context.Canceled", err)
	}

	if data, err := os.ReadFile(logPath); err == nil && strings.Contains(string(data), "update-ref") {
		t.Errorf("update-ref ran:\n%s", data)
	}

	if got := gitOut(t, work, "for-each-ref", "refs/aim/"); got == "" {
		t.Error("the refs are gone")
	}
}
