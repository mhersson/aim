package git

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"testing"
)

func TestLog(t *testing.T) {
	t.Parallel()
	dir := newRepo(t)
	writeFile(t, dir, "a.txt", "a\n")
	commitAll(t, dir, "init")
	gitOut(t, dir, "switch", "-q", "-c", "feat")

	msgs := []string{
		"first 100% done\n\nbody one\nline two",
		"second\n\n%H literal",
		"third",
		"fourth\n\np1\n\np2",
	}

	var stream strings.Builder

	for i, m := range msgs {
		fmt.Fprintf(&stream, "commit refs/heads/feat\ncommitter Test <test@example.com> %d +0000\ndata %d\n%s\n", 1700000000+i, len(m), m)

		if i == 0 {
			stream.WriteString("from refs/heads/feat^0\n")
		}

		stream.WriteString("\n")
	}

	gitInput(t, dir, stream.String(), "fast-import", "--quiet")

	got, err := Log(context.Background(), openRepo(t, dir), "main")
	if err != nil {
		t.Fatal(err)
	}

	want := []Commit{
		{Subject: "first 100% done", Body: "body one\nline two"},
		{Subject: "second", Body: "%H literal"},
		{Subject: "third", Body: ""},
		{Subject: "fourth", Body: "p1\n\np2"},
	}
	if len(got) != len(want) {
		t.Fatalf("got %d commits, want %d: %+v", len(got), len(want), got)
	}

	for i, w := range want {
		if got[i].Subject != w.Subject || got[i].Body != w.Body {
			t.Errorf("commit %d = %q / %q, want %q / %q", i, got[i].Subject, got[i].Body, w.Subject, w.Body)
		}

		if len(got[i].OID) != 40 {
			t.Errorf("commit %d OID = %q", i, got[i].OID)
		}
	}

	if got[0].OID != gitOut(t, dir, "rev-parse", "HEAD~3") {
		t.Errorf("first OID = %s, not the oldest commit", got[0].OID)
	}
}

func TestLogCaps(t *testing.T) {
	t.Parallel()
	dir := newRepo(t)
	writeFile(t, dir, "a.txt", "a\n")
	commitAll(t, dir, "init")
	gitOut(t, dir, "switch", "-q", "-c", "feat")

	var stream strings.Builder

	for i := range 260 {
		fmt.Fprintf(&stream, "commit refs/heads/feat\ncommitter Test <test@example.com> %d +0000\ndata 5\nc%03d\n", 1700000000+i, i)

		if i == 0 {
			stream.WriteString("from refs/heads/feat^0\n")
		}

		stream.WriteString("\n")
	}

	gitInput(t, dir, stream.String(), "fast-import", "--quiet")

	got, err := Log(context.Background(), openRepo(t, dir), "main")
	if err != nil {
		t.Fatal(err)
	}

	if len(got) != 250 {
		t.Fatalf("got %d commits, want 250", len(got))
	}

	if got[0].Subject != "c010" || got[249].Subject != "c259" {
		t.Errorf("range = %q..%q, want c010..c259", got[0].Subject, got[249].Subject)
	}
}

func TestLogRefusesOptionBase(t *testing.T) {
	t.Parallel()

	dir := newRepo(t)
	writeFile(t, dir, "a.txt", "a\n")
	commitAll(t, dir, "init")

	ctx, logPath := wrapGit(t, "")

	for _, base := range []string{"--all", "-n1"} {
		if _, err := Log(ctx, openRepo(t, dir), base); err == nil {
			t.Errorf("Log(%q) succeeded", base)
		}
	}

	// A missing base is a git failure, not a refusal: its error keeps git's exit status.
	_, err := Log(context.Background(), openRepo(t, dir), "nope")

	var exitErr *exec.ExitError
	if !errors.As(err, &exitErr) || exitErr.ExitCode() != 1 {
		t.Errorf("Log with an unknown base: err = %v, want an exit status 1", err)
	}

	calls, err := os.ReadFile(logPath)
	for _, opt := range []string{"--all", "-n1"} {
		if err == nil && strings.Contains(string(calls), opt) {
			t.Errorf("git ran with the option base %q: %s", opt, calls)
		}
	}
}

func TestDiffStat(t *testing.T) {
	t.Parallel()

	dir := newRepo(t)
	writeFile(t, dir, "a.txt", "a\n")
	commitAll(t, dir, "init")
	gitOut(t, dir, "switch", "-q", "-c", "feat")
	writeFile(t, dir, "lexer.go", "package lexer\n")
	commitAll(t, dir, "add the lexer")
	gitOut(t, dir, "switch", "-q", "main")
	writeFile(t, dir, "main-only.txt", "m\n")
	commitAll(t, dir, "main moves on")
	gitOut(t, dir, "switch", "-q", "feat")
	writeFile(t, dir, "uncommitted.txt", "u\n")

	got, err := DiffStat(context.Background(), openRepo(t, dir), "main")
	if err != nil {
		t.Fatal(err)
	}

	if !strings.Contains(got, "lexer.go") || !strings.Contains(got, "1 file changed") {
		t.Errorf("stat = %q, want the branch's one file", got)
	}

	for _, other := range []string{"main-only.txt", "uncommitted.txt"} {
		if strings.Contains(got, other) {
			t.Errorf("stat = %q, names %s, which the branch's commits do not change", got, other)
		}
	}

	// A missing base is a git failure: its error keeps git's exit status.
	_, err = DiffStat(context.Background(), openRepo(t, dir), "nope")

	var exitErr *exec.ExitError
	if !errors.As(err, &exitErr) || exitErr.ExitCode() != 1 {
		t.Errorf("DiffStat with an unknown base: err = %v, want an exit status 1", err)
	}

	if _, err := DiffStat(context.Background(), openRepo(t, dir), "--all"); err == nil {
		t.Error("DiffStat with an option for a base succeeded")
	}
}
