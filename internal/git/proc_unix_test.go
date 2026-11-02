//go:build unix

package git

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
)

// writeHook writes an executable hook; the fork lock keeps a parallel test's
// fork from inheriting the write descriptor (ETXTBSY).
func writeHook(t *testing.T, path, body string) {
	t.Helper()

	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}

	syscall.ForkLock.RLock()

	err := os.WriteFile(path, []byte(body), 0o755)

	syscall.ForkLock.RUnlock()

	if err != nil {
		t.Fatal(err)
	}
}

// lockFiles lists the *.lock files under dir, relative to it.
func lockFiles(t *testing.T, dir string) []string {
	t.Helper()

	var locks []string

	err := filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}

		if !d.IsDir() && strings.HasSuffix(d.Name(), ".lock") {
			rel, _ := filepath.Rel(dir, path)
			locks = append(locks, rel)
		}

		return nil
	})
	if err != nil {
		t.Fatal(err)
	}

	return locks
}

// readPID reads the pid a wrapper wrote to path.
func readPID(t *testing.T, path string) int {
	t.Helper()

	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}

	pid, err := strconv.Atoi(strings.TrimSpace(string(raw)))
	if err != nil {
		t.Fatal(err)
	}

	return pid
}

// A ref write cancelled while git holds its locks leaves none behind: git
// removes them on SIGTERM but not on SIGKILL, and a packed-refs.lock left in
// the user's repository fails their own git until they delete it.
func TestCancelledRefWriteLeavesNoLock(t *testing.T) {
	t.Parallel()

	root := newRepo(t)
	writeFile(t, root, "f", "x\n")
	commitAll(t, root, "init")

	oid := gitOut(t, root, "rev-parse", "HEAD")
	gitOut(t, root, "update-ref", "refs/aim/a", oid)
	gitOut(t, root, "update-ref", "refs/aim/b", oid)
	gitOut(t, root, "pack-refs", "--all")

	// git runs the hook with "prepared" once it holds the transaction's locks.
	started := filepath.Join(t.TempDir(), "started")
	writeHook(t, filepath.Join(root, ".git", "hooks", "reference-transaction"),
		"#!/bin/sh\n[ \"$1\" = prepared ] || exit 0\n: > '"+started+"'\nexec sleep 30\n")

	_, err := run(cancelWhenWritten(t, started), call{
		dir:   root,
		args:  []string{"update-ref", "--stdin"},
		stdin: strings.NewReader("delete refs/aim/a " + oid + "\ndelete refs/aim/b " + oid + "\n"),
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want canceled", err)
	}

	if locks := lockFiles(t, filepath.Join(root, ".git")); len(locks) > 0 {
		t.Errorf("lock files left in the repository: %q", locks)
	}
}

// A write call whose processes ignore SIGTERM is still stopped: the kill
// follows once the grace is over.
func TestCancelKillsAGroupThatIgnoresTerm(t *testing.T) {
	t.Parallel()

	root := newRepo(t)
	script, logPath := writeGitWrapper(t, `trap '' TERM
sleep 30 &
echo $! > "$LOG.tmp" && mv "$LOG.tmp" "$LOG.pid"
wait`)
	ctx := context.WithValue(cancelWhenWritten(t, logPath+".pid"), gitPathKey{}, script)

	_, err := run(ctx, call{dir: root, args: []string{"update-ref", "--stdin"}, stdin: strings.NewReader("")})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want canceled", err)
	}

	pid := readPID(t, logPath+".pid")
	t.Cleanup(func() { _ = syscall.Kill(pid, syscall.SIGKILL) })

	eventually(t, "the child that ignores SIGTERM to die", func() bool { return !alive(pid) })
}

// A cancelled read holds no lock, so it is killed at once without SIGTERM's
// grace, and the re-kill still reaches children forked during the kill.
func TestCancelledReadGetsNoTerm(t *testing.T) {
	t.Parallel()

	root := newRepo(t)
	script, logPath := writeGitWrapper(t, `trap ': > "$LOG.term"' TERM
echo $$ > "$LOG.pgid"
i=0
while [ $i -lt 300 ]; do
	sh -c 'echo $$ >> "$1"; exec sleep 30' sh "$LOG.pids" &
	i=$((i+1))
done
wait`)

	t.Cleanup(func() {
		if raw, err := os.ReadFile(logPath + ".pgid"); err == nil {
			if pgid, err := strconv.Atoi(strings.TrimSpace(string(raw))); err == nil {
				_ = syscall.Kill(-pgid, syscall.SIGKILL)
			}
		}
	})

	// The first child's pid lands while the wrapper is still forking.
	ctx := context.WithValue(cancelWhenWritten(t, logPath+".pids"), gitPathKey{}, script)

	_, err := run(ctx, call{dir: root, args: []string{"status"}, read: true})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want canceled", err)
	}

	if _, err := os.Stat(logPath + ".term"); err == nil {
		t.Error("a cancelled read got SIGTERM")
	}

	raw, err := os.ReadFile(logPath + ".pids")
	if err != nil {
		t.Fatal(err)
	}

	for field := range strings.FieldsSeq(string(raw)) {
		pid, err := strconv.Atoi(field)
		if err != nil {
			t.Fatal(err)
		}

		eventually(t, "child "+field+" to die", func() bool { return !alive(pid) })
	}
}
