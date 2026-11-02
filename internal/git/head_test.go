package git

import (
	"context"
	"fmt"
	"os"
	"strings"
	"testing"
)

func TestHead(t *testing.T) {
	t.Parallel()
	t.Run("branch", func(t *testing.T) {
		t.Parallel()
		dir := newRepo(t)
		writeFile(t, dir, "a.txt", "a\n")
		commitAll(t, dir, "one")
		gitOut(t, dir, "checkout", "-q", "-b", "feat/x")

		got := headOf(t, dir)
		want := HeadInfo{Branch: "feat/x", OID: gitOut(t, dir, "rev-parse", "HEAD")}

		if got != want {
			t.Errorf("Head = %+v, want %+v", got, want)
		}
	})

	t.Run("detached", func(t *testing.T) {
		t.Parallel()
		dir := newRepo(t)
		writeFile(t, dir, "a.txt", "a\n")
		commitAll(t, dir, "one")
		gitOut(t, dir, "checkout", "-q", "--detach")

		got := headOf(t, dir)
		want := HeadInfo{Detached: true, OID: gitOut(t, dir, "rev-parse", "HEAD")}

		if got != want {
			t.Errorf("Head = %+v, want %+v", got, want)
		}

		if len(got.OID) < 40 {
			t.Errorf("OID %q is not a full id", got.OID)
		}
	})

	t.Run("unborn", func(t *testing.T) {
		t.Parallel()
		dir := newRepo(t)

		got := headOf(t, dir)
		if want := (HeadInfo{Branch: "main"}); got != want {
			t.Errorf("Head = %+v, want %+v", got, want)
		}
	})
}

func headOf(t *testing.T, dir string) HeadInfo {
	t.Helper()

	r, err := Open(context.Background(), dir)
	if err != nil {
		t.Fatal(err)
	}

	h, err := Head(context.Background(), r)
	if err != nil {
		t.Fatalf("Head: %v", err)
	}

	return h
}

func TestHeadReportsFailures(t *testing.T) {
	t.Parallel()
	dir := newRepo(t)
	writeFile(t, dir, "a.txt", "a\n")
	commitAll(t, dir, "one")

	r, err := Open(context.Background(), dir)
	if err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	if h, err := Head(ctx, r); err == nil {
		t.Errorf("cancelled context: Head = %+v, want an error", h)
	}
}

func TestHeadCommit(t *testing.T) {
	t.Parallel()

	dir := newRepo(t)

	steps := []struct {
		name        string
		message     string
		wantSubject string
	}{
		{"first commit", "first", "first"},
		{"newest of several", "second", "second"},
		{"subject without its body", "subject\n\nthe body\nsecond line", "subject"},
		{"wrapped subject is one line", "line one\nline two\n\nbody", "line one line two"},
		{"control and invisible characters stay as git has them", "a\x1b[31mb\u200bc\td", "a\x1b[31mb\u200bc\td"},
	}

	for i, st := range steps {
		writeFile(t, dir, "a.txt", fmt.Sprintf("%d\n", i))
		commitAll(t, dir, st.message)

		got, err := HeadCommit(context.Background(), openRepo(t, dir))
		if err != nil {
			t.Fatalf("%s: %v", st.name, err)
		}

		want := Commit{OID: gitOut(t, dir, "rev-parse", "HEAD"), Subject: st.wantSubject}
		if got != want {
			t.Errorf("%s: HeadCommit = %+v, want %+v", st.name, got, want)
		}
	}
}

func TestHeadCommitUnbornBranchIsAnError(t *testing.T) {
	dir := newRepo(t)

	if c, err := HeadCommit(context.Background(), openRepo(t, dir)); err == nil {
		t.Errorf("HeadCommit = %+v on an unborn branch, want an error", c)
	}
}

func TestHeadCommitIsARead(t *testing.T) {
	t.Parallel()
	dir := newRepo(t)
	writeFile(t, dir, "a.txt", "a\n")
	commitAll(t, dir, "first")

	r := openRepo(t, dir)
	ctx, log := wrapGit(t, `echo "locks=$GIT_OPTIONAL_LOCKS" >> "$LOG.locks"`)

	if _, err := HeadCommit(ctx, r); err != nil {
		t.Fatal(err)
	}

	calls, err := os.ReadFile(log)
	if err != nil {
		t.Fatal(err)
	}

	if n := strings.Count(string(calls), "\n"); n != 1 {
		t.Errorf("git ran %d times, want 1:\n%s", n, calls)
	}

	locks, err := os.ReadFile(log + ".locks")
	if err != nil {
		t.Fatal(err)
	}

	if got := strings.TrimSpace(string(locks)); got != "locks=0" {
		t.Errorf("GIT_OPTIONAL_LOCKS = %q, want 0", got)
	}
}
