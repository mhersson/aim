package git

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

var update = flag.Bool("update", false, "regenerate the golden files in testdata/")

// goldenJSON renders a Result as indented JSON with line kinds as characters.
func goldenJSON(t *testing.T, res Result) []byte {
	t.Helper()

	raw, err := json.Marshal(res)
	if err != nil {
		t.Fatal(err)
	}

	var v any
	if err := json.Unmarshal(raw, &v); err != nil {
		t.Fatal(err)
	}

	var walk func(any)

	walk = func(n any) {
		switch n := n.(type) {
		case map[string]any:
			for k, c := range n {
				if num, ok := c.(float64); ok && k == "Kind" {
					n[k] = string(rune(int(num)))

					continue
				}

				walk(c)
			}
		case []any:
			for _, c := range n {
				walk(c)
			}
		}
	}
	walk(v)

	out, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		t.Fatal(err)
	}

	return append(out, '\n')
}

func golden(t *testing.T, name string, res Result) {
	t.Helper()

	got := goldenJSON(t, res)
	path := filepath.Join("testdata", name+".golden.json")

	if *update {
		if err := os.MkdirAll("testdata", 0o755); err != nil {
			t.Fatal(err)
		}

		if err := os.WriteFile(path, got, 0o644); err != nil {
			t.Fatal(err)
		}
	}

	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("%v (run with -update to create it)", err)
	}

	if !bytes.Equal(got, want) {
		t.Errorf("%s differs from the result (run with -update to accept):\n%s", path, got)
	}
}

// checkInvariants asserts that every file's hunks agree with its counts and
// that line numbers advance consistently.
func checkInvariants(t *testing.T, res Result) {
	t.Helper()

	for _, f := range res.Files {
		if f.Truncated || f.Binary {
			if len(f.Hunks) != 0 {
				t.Errorf("%q: truncated or binary file has %d hunks", f.Path, len(f.Hunks))
			}

			continue
		}

		var added, removed int

		for _, h := range f.Hunks {
			oldN, newN := h.OldStart, h.NewStart

			for _, l := range h.Lines {
				switch l.Kind {
				case Add:
					added++

					if l.New != newN || l.Old != 0 {
						t.Fatalf("%q: add line numbers %d/%d, want 0/%d", f.Path, l.Old, l.New, newN)
					}

					newN++
				case Del:
					removed++

					if l.Old != oldN || l.New != 0 {
						t.Fatalf("%q: del line numbers %d/%d, want %d/0", f.Path, l.Old, l.New, oldN)
					}

					oldN++
				default:
					if l.Old != oldN || l.New != newN {
						t.Fatalf("%q: context line numbers %d/%d, want %d/%d", f.Path, l.Old, l.New, oldN, newN)
					}

					oldN++
					newN++
				}
			}
		}

		if added != f.Added || removed != f.Removed {
			t.Errorf("%q: hunks hold +%d -%d, counts say +%d -%d", f.Path, added, removed, f.Added, f.Removed)
		}
	}
}

func testEngine(t *testing.T) *Engine {
	t.Helper()

	return NewEngine(EngineOptions{TempDir: t.TempDir()})
}

// diffOK runs Diff, then checks the engine left nothing in its temp dir.
func diffOK(t *testing.T, e *Engine, src Source) Result {
	t.Helper()

	return diffOKContext(context.Background(), t, e, src)
}

// diffOKContext is diffOK with the context its git calls run under.
func diffOKContext(ctx context.Context, t *testing.T, e *Engine, src Source) Result {
	t.Helper()

	res, err := e.Diff(ctx, src)
	if err != nil {
		t.Fatalf("Diff: %v", err)
	}

	assertEmptyDir(t, e.tempDir)
	checkInvariants(t, res)

	return res
}

func fileByPath(t *testing.T, res Result, path string) FileDiff {
	t.Helper()

	for _, f := range res.Files {
		if f.Path == path {
			return f
		}
	}

	t.Fatalf("no file %q in %v", path, pathsOf(res))

	return FileDiff{}
}

func pathsOf(res Result) []string {
	out := make([]string, len(res.Files))
	for i, f := range res.Files {
		out[i] = f.Path
	}

	return out
}

// numbered returns n lines "<tag> <i>".
func numbered(tag string, n int) string {
	var b strings.Builder

	for i := 1; i <= n; i++ {
		fmt.Fprintf(&b, "%s %d\n", tag, i)
	}

	return b.String()
}

// branchRepo commits base on main, then switches to feature and applies work.
func branchRepo(t *testing.T, base, work func(dir string)) string {
	t.Helper()

	dir := newRepo(t)
	base(dir)
	commitAll(t, dir, "base")
	gitOut(t, dir, "checkout", "-q", "-b", "feature")

	if work != nil {
		work(dir)
	}

	return dir
}

func branchDiff(t *testing.T, dir string) Result {
	t.Helper()

	return branchDiffContext(context.Background(), t, dir)
}

// branchDiffContext is branchDiff with the context its git calls run under.
func branchDiffContext(ctx context.Context, t *testing.T, dir string) Result {
	t.Helper()

	return diffOKContext(ctx, t, testEngine(t), BranchSource{Worktree: dir, Base: "main"})
}

func TestDiffStatuses(t *testing.T) {
	t.Parallel()
	dir := branchRepo(t, func(dir string) {
		for _, n := range []string{"mod", "del", "ren", "renedit", "src"} {
			writeFile(t, dir, n+".txt", numbered(n, 20))
		}

		writeFile(t, dir, "mode.sh", "echo hi\n")
		writeFile(t, dir, "bin.dat", "\x00\x01\x02\x03")
	}, func(dir string) {
		writeFile(t, dir, "mod.txt", strings.Replace(strings.Replace(numbered("mod", 20), "mod 5\n", "mod five\n", 1), "mod 15\n", "mod fifteen\n", 1))
		must(t, os.Remove(filepath.Join(dir, "del.txt")))
		must(t, os.Rename(filepath.Join(dir, "ren.txt"), filepath.Join(dir, "moved.txt")))
		must(t, os.Remove(filepath.Join(dir, "renedit.txt")))
		writeFile(t, dir, "renamed-edit.txt", strings.Replace(numbered("renedit", 20), "renedit 3\n", "renedit three\n", 1))
		writeFile(t, dir, "copy.txt", numbered("src", 20))
		writeFile(t, dir, "src.txt", numbered("src", 20)+"src appended\n")
		must(t, os.Chmod(filepath.Join(dir, "mode.sh"), 0o755))
		writeFile(t, dir, "bin.dat", "\x00\x01\x02\x04")
		writeFile(t, dir, "untracked.txt", "u1\nu2\n")
		writeFile(t, dir, "staged.txt", "s1\n")
		gitOut(t, dir, "add", "staged.txt")
	})

	res := branchDiff(t, dir)
	golden(t, "diff_statuses", res)

	want := map[string]Status{
		"mod.txt": Modified, "del.txt": Deleted, "moved.txt": Renamed, "renamed-edit.txt": Renamed,
		"copy.txt": Copied, "src.txt": Modified, "mode.sh": ModeChanged, "bin.dat": Modified,
		"untracked.txt": Added, "staged.txt": Added,
	}
	for path, status := range want {
		if got := fileByPath(t, res, path).Status; got != status {
			t.Errorf("%s status = %s, want %s", path, got, status)
		}
	}

	if !fileByPath(t, res, "untracked.txt").Untracked || fileByPath(t, res, "staged.txt").Untracked {
		t.Error("only the untracked file is marked Untracked")
	}

	if !fileByPath(t, res, "bin.dat").Binary {
		t.Error("bin.dat is not marked Binary")
	}

	if !res.Live || res.Truncated {
		t.Errorf("Live = %v, Truncated = %v", res.Live, res.Truncated)
	}
}

func must(t *testing.T, err error) {
	t.Helper()

	if err != nil {
		t.Fatal(err)
	}
}

func TestDiffSymlinksAndTypeChanges(t *testing.T) {
	t.Parallel()
	dir := branchRepo(t, func(dir string) {
		writeFile(t, dir, "target.txt", "t\n")
		writeFile(t, dir, "tolink.txt", "was a file\n")
		must(t, os.Symlink("target.txt", filepath.Join(dir, "link")))
		must(t, os.Symlink("target.txt", filepath.Join(dir, "tofile")))
	}, func(dir string) {
		must(t, os.Remove(filepath.Join(dir, "link")))
		must(t, os.Symlink("elsewhere.txt", filepath.Join(dir, "link")))
		must(t, os.Remove(filepath.Join(dir, "tolink.txt")))
		must(t, os.Symlink("target.txt", filepath.Join(dir, "tolink.txt")))
		must(t, os.Remove(filepath.Join(dir, "tofile")))
		writeFile(t, dir, "tofile", "now a file\n")
		must(t, os.Symlink("target.txt", filepath.Join(dir, "newlink")))
	})

	res := branchDiff(t, dir)
	golden(t, "diff_symlinks", res)

	for _, p := range []string{"tolink.txt", "tofile"} {
		f := fileByPath(t, res, p)
		if len(f.Hunks) != 2 || f.Added != 1 || f.Removed != 1 {
			t.Errorf("%s: type change has %d hunks, +%d -%d", p, len(f.Hunks), f.Added, f.Removed)
		}
	}
}

func TestDiffSubmoduleAndNestedRepo(t *testing.T) {
	t.Setenv("GIT_AUTHOR_DATE", "2026-01-01T00:00:00Z")
	t.Setenv("GIT_COMMITTER_DATE", "2026-01-01T00:00:00Z")

	sub := newRepo(t)
	writeFile(t, sub, "s.txt", "s\n")
	commitAll(t, sub, "sub")

	dir := branchRepo(t, func(dir string) {
		writeFile(t, dir, "a.txt", "a\n")
		gitOut(t, dir, "-c", "protocol.file.allow=always", "submodule", "add", "-q", sub, "sm")
	}, func(dir string) {
		writeFile(t, filepath.Join(dir, "sm"), "s.txt", "moved\n")
		gitOut(t, filepath.Join(dir, "sm"), "commit", "-q", "-am", "moved")
		gitOut(t, dir, "add", "sm")
		gitOut(t, dir, "commit", "-q", "-m", "bump submodule")

		nested := filepath.Join(dir, "nested")
		must(t, os.MkdirAll(nested, 0o755))
		gitOut(t, nested, "init", "-q", "-b", "main")
		writeFile(t, nested, "n.txt", "n\n")
		writeFile(t, dir, "after.txt", "x\n")
	})

	res := branchDiff(t, dir)

	// .gitmodules holds the temporary path of the submodule, so the tree ids vary.
	golden(t, "diff_submodule", Result{Live: res.Live, Files: res.Files})

	if f := fileByPath(t, res, "sm"); f.NewMode != "160000" || f.Status != Modified {
		t.Errorf("sm = %+v", f)
	}

	for _, f := range res.Files {
		if strings.HasPrefix(f.Path, "nested") {
			t.Errorf("nested repository leaked into the diff: %q", f.Path)
		}
	}
}

func TestDiffLineEndings(t *testing.T) {
	t.Parallel()
	dir := branchRepo(t, func(dir string) {
		writeFile(t, dir, "crlf.txt", "a\r\nb\r\nc\r\n")
		writeFile(t, dir, "noeol.txt", "x\ny")
		writeFile(t, dir, "gains.txt", "p\nq")
	}, func(dir string) {
		writeFile(t, dir, "crlf.txt", "a\r\nB\r\nc\r\n")
		writeFile(t, dir, "noeol.txt", "x\ny\nz")
		writeFile(t, dir, "gains.txt", "p\nq\n")
		writeFile(t, dir, "new-noeol.txt", "only line")
	})

	res := branchDiff(t, dir)
	golden(t, "diff_line_endings", res)

	var sawCR bool

	for _, h := range fileByPath(t, res, "crlf.txt").Hunks {
		for _, l := range h.Lines {
			sawCR = sawCR || strings.HasSuffix(l.Text, "\r")
		}
	}

	if !sawCR {
		t.Error("CRLF lines lost their carriage return")
	}
}

func TestDiffOddPaths(t *testing.T) {
	t.Parallel()

	names := []string{"sp ace.txt", "uni/ünï-日本.txt", "new\nline.txt", "quo\"te.txt", "tab\there.txt", "back\\slash.txt"}

	dir := branchRepo(t, func(dir string) {
		writeFile(t, dir, "keep.txt", "k\n")
		writeFile(t, dir, names[0], "old\n")
	}, func(dir string) {
		for _, n := range names {
			writeFile(t, dir, n, "content of "+strconv.Quote(n)+"\n")
		}
	})

	res := branchDiff(t, dir)
	golden(t, "diff_odd_paths", res)

	for _, n := range names {
		if f := fileByPath(t, res, n); len(f.Hunks) == 0 {
			t.Errorf("%q: no hunks", n)
		}
	}
}

func TestDiffInvalidUTF8Path(t *testing.T) {
	t.Parallel()

	name := "inv\xffalid.txt"
	if !fsAcceptsName(t, name) {
		t.Skip("the filesystem refuses names that are not valid UTF-8")
	}

	dir := branchRepo(t, func(dir string) {
		writeFile(t, dir, "keep.txt", "k\n")
	}, func(dir string) {
		writeFile(t, dir, name, "content of "+strconv.Quote(name)+"\n")
	})

	f := fileByPath(t, branchDiff(t, dir), name)
	if f.Status != Added || !f.Untracked || len(f.Hunks) != 1 || len(f.Hunks[0].Lines) != 1 {
		t.Fatalf("%q: status %v, untracked %v, hunks %+v, want one untracked added hunk with one line", name, f.Status, f.Untracked, f.Hunks)
	}

	if l := f.Hunks[0].Lines[0]; l.Kind != Add || l.Text != "content of "+strconv.Quote(name) {
		t.Errorf("%q: line %c %q, want %c %q", name, l.Kind, l.Text, Add, "content of "+strconv.Quote(name))
	}
}

func TestDiffPathShapeChanges(t *testing.T) {
	t.Parallel()
	dir := branchRepo(t, func(dir string) {
		writeFile(t, dir, "x", "was a file\n")
		writeFile(t, dir, "d/inner.txt", "inner\n")
	}, func(dir string) {
		must(t, os.Remove(filepath.Join(dir, "x")))
		writeFile(t, dir, "x/new.txt", "now inside\n")
		must(t, os.RemoveAll(filepath.Join(dir, "d")))
		writeFile(t, dir, "d", "now a file\n")
	})

	golden(t, "diff_path_shapes", branchDiff(t, dir))
}

func TestDiffBranchUsesMergeBase(t *testing.T) {
	t.Parallel()
	dir := newRepo(t)
	writeFile(t, dir, "shared.txt", numbered("shared", 10))
	writeFile(t, dir, "feature.txt", numbered("feature", 5))
	commitAll(t, dir, "c0")
	gitOut(t, dir, "checkout", "-q", "-b", "feature")
	writeFile(t, dir, "feature.txt", numbered("feature", 5)+"f1\n")
	commitAll(t, dir, "f1")
	writeFile(t, dir, "committed-new.txt", "c\n")
	commitAll(t, dir, "f2")
	gitOut(t, dir, "checkout", "-q", "main")
	writeFile(t, dir, "main-only.txt", "m\n")
	commitAll(t, dir, "m1")
	gitOut(t, dir, "checkout", "-q", "feature")
	writeFile(t, dir, "shared.txt", numbered("shared", 10)+"dirty\n")
	writeFile(t, dir, "untracked.txt", "u\n")

	res := diffOK(t, testEngine(t), BranchSource{Worktree: dir, Base: "main"})
	golden(t, "diff_branch_merge_base", res)

	if want := gitOut(t, dir, "rev-parse", gitOut(t, dir, "merge-base", "main", "HEAD")+"^{tree}"); res.Old != want {
		t.Errorf("Old = %s, want the merge-base tree %s", res.Old, want)
	}

	for _, f := range res.Files {
		if f.Path == "main-only.txt" {
			t.Error("a change that only main has is in the diff")
		}
	}

	// An empty base resolves to the default branch.
	def := diffOK(t, testEngine(t), BranchSource{Worktree: dir})
	if def.Old != res.Old || def.New != res.New {
		t.Errorf("default base diff = %s..%s, want %s..%s", def.Old, def.New, res.Old, res.New)
	}
}

func TestDiffBranchErrors(t *testing.T) {
	t.Parallel()
	e := testEngine(t)
	ctx := context.Background()

	t.Run("no default branch", func(t *testing.T) {
		dir := newRepo(t)
		writeFile(t, dir, "a", "a\n")
		commitAll(t, dir, "c")
		gitOut(t, dir, "branch", "-m", "trunk")

		if _, err := e.Diff(ctx, BranchSource{Worktree: dir}); !errors.Is(err, ErrNoBase) {
			t.Errorf("err = %v, want ErrNoBase", err)
		}
	})

	t.Run("unborn HEAD", func(t *testing.T) {
		dir := newRepo(t)
		writeFile(t, dir, "a", "a\n")

		if _, err := e.Diff(ctx, BranchSource{Worktree: dir, Base: "main"}); err == nil {
			t.Error("a branch diff without commits succeeded")
		}
	})

	t.Run("option as base", func(t *testing.T) {
		dir := seedRepo(t)

		if _, err := e.Diff(ctx, BranchSource{Worktree: dir, Base: "--all"}); err == nil {
			t.Error("accepted a base that looks like an option")
		}
	})

	t.Run("unknown base", func(t *testing.T) {
		dir := seedRepo(t)

		if _, err := e.Diff(ctx, BranchSource{Worktree: dir, Base: "nope"}); err == nil {
			t.Error("accepted an unknown base")
		}
	})

	t.Run("unrelated histories", func(t *testing.T) {
		dir := seedRepo(t)
		gitOut(t, dir, "checkout", "-q", "--orphan", "other")
		writeFile(t, dir, "o.txt", "o\n")
		commitAll(t, dir, "orphan")

		if _, err := e.Diff(ctx, BranchSource{Worktree: dir, Base: "main"}); err == nil {
			t.Error("a diff without a merge base succeeded")
		}
	})

	t.Run("not a repository", func(t *testing.T) {
		if _, err := e.Diff(ctx, BranchSource{Worktree: t.TempDir(), Base: "main"}); !errors.Is(err, ErrNotRepo) {
			t.Errorf("err = %v, want ErrNotRepo", err)
		}
	})

	t.Run("cancelled", func(t *testing.T) {
		dir := seedRepo(t)
		cctx, cancel := context.WithCancel(ctx)
		cancel()

		if _, err := e.Diff(cctx, BranchSource{Worktree: dir, Base: "main"}); !errors.Is(err, context.Canceled) {
			t.Errorf("err = %v, want context.Canceled", err)
		}

		assertEmptyDir(t, e.tempDir)
	})

	t.Run("unsupported source", func(t *testing.T) {
		if _, err := e.Diff(ctx, nil); err == nil {
			t.Error("accepted a nil source")
		}
	})
}

func TestDiffOddRepositories(t *testing.T) {
	t.Parallel()
	t.Run("detached HEAD", func(t *testing.T) {
		t.Parallel()
		dir := seedRepo(t)
		writeFile(t, dir, "a.txt", "changed\n")
		commitAll(t, dir, "second")
		gitOut(t, dir, "checkout", "-q", "--detach", "HEAD")
		writeFile(t, dir, "d.txt", "d\n")

		res := diffOK(t, testEngine(t), BranchSource{Worktree: dir, Base: "main"})
		if len(res.Files) != 1 || res.Files[0].Path != "d.txt" {
			t.Errorf("files = %v", pathsOf(res))
		}
	})

	t.Run("linked worktree of a bare repository", func(t *testing.T) {
		t.Parallel()
		src := seedRepo(t)
		bare := filepath.Join(t.TempDir(), "bare.git")
		gitOut(t, src, "clone", "-q", "--bare", src, bare)

		wt := filepath.Join(t.TempDir(), "wt")
		gitOut(t, bare, "worktree", "add", "-q", "-b", "work", wt)

		wt, err := filepath.EvalSymlinks(wt)
		must(t, err)
		writeFile(t, wt, "new.txt", "n\n")

		res := diffOK(t, testEngine(t), BranchSource{Worktree: wt, Base: "main"})
		if len(res.Files) != 1 || res.Files[0].Path != "new.txt" || !res.Files[0].Untracked {
			t.Errorf("files = %+v", res.Files)
		}
	})

	t.Run("split index", func(t *testing.T) {
		t.Parallel()
		dir := seedRepo(t)
		gitOut(t, dir, "config", "core.splitIndex", "true")
		gitOut(t, dir, "update-index", "--split-index")
		writeFile(t, dir, "dir/c.txt", "touched\n")
		gitOut(t, dir, "add", "dir/c.txt")
		writeFile(t, dir, "u.txt", "u\n")

		res := diffOK(t, testEngine(t), BranchSource{Worktree: dir, Base: "main"})
		if got := pathsOf(res); len(got) != 2 || got[0] != "dir/c.txt" || got[1] != "u.txt" {
			t.Errorf("files = %v", got)
		}
	})

	t.Run("untracked file over the snapshot cap", func(t *testing.T) {
		t.Parallel()
		dir := seedRepo(t)

		f, err := os.Create(filepath.Join(dir, "huge.bin"))
		must(t, err)
		must(t, f.Truncate(60<<20)) // sparse
		must(t, f.Close())
		writeFile(t, dir, "small.txt", "s\n")

		res := diffOK(t, testEngine(t), BranchSource{Worktree: dir, Base: "main"})
		if got := pathsOf(res); len(got) != 1 || got[0] != "small.txt" {
			t.Errorf("files = %v, want only small.txt", got)
		}
	})
}

// Turn sources.

// turnRepo returns a repository with turn 1 snapshotted.
func turnRepo(t *testing.T) string {
	t.Helper()

	dir := seedRepo(t)
	writeFile(t, dir, "a.txt", "turn one\n")
	snap(t, dir, sessA, SnapshotOptions{})

	return dir
}

func TestDiffTurnLiveAndPast(t *testing.T) {
	t.Parallel()
	dir := turnRepo(t)
	writeFile(t, dir, "a.txt", "turn two\n")
	writeFile(t, dir, "two.txt", "two\n")
	must(t, os.Remove(filepath.Join(dir, "b.txt")))
	snap(t, dir, sessA, SnapshotOptions{})
	writeFile(t, dir, "a.txt", "after two\n")
	writeFile(t, dir, "live.txt", "live\n")

	e := testEngine(t)
	src := func(turn int) Source { return TurnSource{Worktree: dir, Session: sessA, Turn: turn} }

	latest := diffOK(t, e, src(0))
	golden(t, "diff_turn_live", latest)

	if !latest.Live || latest.Turn != 2 {
		t.Errorf("latest: Live=%v Turn=%d", latest.Live, latest.Turn)
	}

	if !fileByPath(t, latest, "live.txt").Untracked {
		t.Error("live.txt is not marked Untracked")
	}

	if explicit := diffOK(t, e, src(2)); explicit.Old != latest.Old || explicit.New != latest.New {
		t.Error("turn 2 and turn 0 differ")
	}

	past := diffOK(t, e, src(1))
	golden(t, "diff_turn_past", past)

	if past.Live || past.Turn != 1 {
		t.Errorf("past: Live=%v Turn=%d", past.Live, past.Turn)
	}

	if f := fileByPath(t, past, "b.txt"); f.Status != Deleted {
		t.Errorf("b.txt status = %q in the past turn, want deleted", f.Status)
	}

	for _, f := range past.Files {
		if f.Untracked {
			t.Errorf("%s: a turn pair has no untracked files", f.Path)
		}
	}
}

// A file untracked when a turn started is in that turn's snapshot, so a later
// edit is a modification of it, not an added file.
func TestDiffTurnJudgesAFileTheSnapshotHoldsByTheComparison(t *testing.T) {
	t.Parallel()
	dir := seedRepo(t)
	writeFile(t, dir, "x.go", numbered("x", 40))
	snap(t, dir, sessA, SnapshotOptions{})

	edited := strings.Replace(numbered("x", 40), "x 35\n", "x 35\nx 35a\nx 35b\n", 1)
	writeFile(t, dir, "x.go", edited)
	writeFile(t, dir, "fresh.go", "fresh\n")

	src := TurnSource{Worktree: dir, Session: sessA}
	e := testEngine(t)
	res := diffOK(t, e, src)

	x := fileByPath(t, res, "x.go")
	if x.Status != Modified || x.Untracked || len(x.Hunks) != 1 || x.Added != 2 || x.Removed != 0 {
		t.Errorf("x.go = %+v, want a modification with one hunk and +2 -0", x)
	}

	if fresh := fileByPath(t, res, "fresh.go"); fresh.Status != Added || !fresh.Untracked {
		t.Errorf("fresh.go = %+v, want an untracked added file", fresh)
	}

	t.Run("update", func(t *testing.T) {
		writeFile(t, dir, "x.go", edited+"tail\n")

		upd := updateOK(t, e, res, src, Batch{Paths: []string{"x.go"}})
		if x := fileByPath(t, upd, "x.go"); x.Status != Modified || x.Untracked {
			t.Errorf("x.go after update = %+v, want a modification", x)
		}

		sameAsFull(t, upd, src)
	})
}

func TestMarkUntracked(t *testing.T) {
	t.Parallel()

	tests := []struct {
		status Status
		want   bool
	}{
		{Added, true},
		{Renamed, true},
		{Copied, true},
		{Modified, false},
		{ModeChanged, false},
		{Deleted, false},
	}

	for _, tt := range tests {
		files := []FileDiff{{Path: "p", Status: tt.status}, {Path: "other", Status: tt.status}}

		markUntracked(files, []string{"p"})

		if files[0].Untracked != tt.want || files[1].Untracked {
			t.Errorf("%s: Untracked = %v, other = %v, want %v and false", tt.status, files[0].Untracked, files[1].Untracked, tt.want)
		}
	}
}

// A turn whose start or end is a marker, or that stayed open behind a later
// turn, has no snapshot; it is never shown against a later state.
func TestDiffTurnMissingSnapshots(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	dir := turnRepo(t)
	writeFile(t, dir, "a.txt", "between\n")

	_, err := MarkMissing(ctx, dir, sessA, 0, false)
	must(t, err)

	writeFile(t, dir, "a.txt", "turn three\n")
	snap(t, dir, sessA, SnapshotOptions{})
	writeFile(t, dir, "a.txt", "live\n")

	e := testEngine(t)

	for _, turn := range []int{1, 2, 9} {
		_, err := e.Diff(ctx, TurnSource{Worktree: dir, Session: sessA, Turn: turn})
		if !errors.Is(err, ErrNoSnapshot) {
			t.Errorf("turn %d: err = %v, want ErrNoSnapshot", turn, err)
		}
	}

	live := diffOK(t, e, TurnSource{Worktree: dir, Session: sessA, Turn: 3})
	if !live.Live || live.Turn != 3 {
		t.Errorf("Live=%v Turn=%d, want turn 3 live", live.Live, live.Turn)
	}

	// A marker after it makes turn 3 a past turn with no end.
	if _, err := MarkMissing(ctx, dir, sessA, 0, false); err != nil {
		t.Fatal(err)
	}

	for _, turn := range []int{0, 3} {
		if _, err := e.Diff(ctx, TurnSource{Worktree: dir, Session: sessA, Turn: turn}); !errors.Is(err, ErrNoSnapshot) {
			t.Errorf("turn %d: err = %v, want ErrNoSnapshot", turn, err)
		}
	}

	// An end marker leaves the turn without a snapshot too.
	dir = turnRepo(t)
	if _, err := MarkMissing(ctx, dir, sessA, 1, true); err != nil {
		t.Fatal(err)
	}

	if _, err := e.Diff(ctx, TurnSource{Worktree: dir, Session: sessA, Turn: 1}); !errors.Is(err, ErrNoSnapshot) {
		t.Errorf("end marker: err = %v, want ErrNoSnapshot", err)
	}
}

func TestDiffTurnErrors(t *testing.T) {
	t.Parallel()
	dir := seedRepo(t)
	e := testEngine(t)
	ctx := context.Background()

	if _, err := e.Diff(ctx, TurnSource{Worktree: dir, Session: sessA}); !errors.Is(err, ErrNoTurns) {
		t.Errorf("no turns: err = %v, want ErrNoTurns", err)
	}

	if _, err := e.Diff(ctx, TurnSource{Worktree: dir, Session: "not-a-uuid"}); !errors.Is(err, ErrBadSession) {
		t.Errorf("bad session: err = %v, want ErrBadSession", err)
	}

	snap(t, dir, sessA, SnapshotOptions{})

	if _, err := e.Diff(ctx, TurnSource{Worktree: dir, Session: sessB}); !errors.Is(err, ErrNoTurns) {
		t.Errorf("other session: err = %v, want ErrNoTurns", err)
	}
}

func TestDiffTurnAgentCommitChangesNothing(t *testing.T) {
	t.Parallel()

	build := func(commit bool) Result {
		dir := turnRepo(t)
		writeFile(t, dir, "a.txt", "agent edit\n")
		writeFile(t, dir, "new.txt", "agent file\n")

		if commit {
			commitAll(t, dir, "agent commit")
		}

		writeFile(t, dir, "b.txt", "after commit\n")

		return diffOK(t, testEngine(t), TurnSource{Worktree: dir, Session: sessA})
	}

	with, without := build(true), build(false)

	if with.Old != without.Old || with.New != without.New {
		t.Errorf("trees differ: %s..%s vs %s..%s", with.Old, with.New, without.Old, without.New)
	}

	golden(t, "diff_turn_agent_commit", with)

	if len(with.Files) != 3 {
		t.Errorf("files = %v, want a.txt, b.txt and new.txt", pathsOf(with))
	}
}

func TestDiffTurnUnbornHead(t *testing.T) {
	t.Parallel()
	dir := newRepo(t)
	writeFile(t, dir, "a.txt", "a\n")
	snap(t, dir, sessA, SnapshotOptions{})
	writeFile(t, dir, "b.txt", "b\n")

	res := diffOK(t, testEngine(t), TurnSource{Worktree: dir, Session: sessA})
	if got := pathsOf(res); len(got) != 1 || got[0] != "b.txt" {
		t.Errorf("files = %v", got)
	}
}

// Caps and hostile content.

func hunkPasses(t *testing.T, logPath string) int {
	t.Helper()

	data, err := os.ReadFile(logPath)
	if err != nil {
		return 0
	}

	return strings.Count(string(data), " -U3 ")
}

// recordPid makes the wrapped git write its pid for hunk passes. The wrapper
// execs git, so the pid is git's.
func recordPid(t *testing.T) (ctx context.Context, logPath, pidFile string) {
	t.Helper()

	pidFile = filepath.Join(t.TempDir(), "pid")
	ctx, logPath = wrapGit(t, "case \"$*\" in *-U3*) echo $$ > '"+pidFile+"';; esac")

	return ctx, logPath, pidFile
}

func assertProcessGone(t *testing.T, pidFile string) {
	t.Helper()

	data, err := os.ReadFile(pidFile)
	if err != nil {
		t.Fatalf("the hunk pass never started: %v", err)
	}

	pid, err := strconv.Atoi(strings.TrimSpace(string(data)))
	if err != nil {
		t.Fatal(err)
	}

	if err := syscall.Kill(pid, 0); !errors.Is(err, syscall.ESRCH) {
		t.Errorf("git (pid %d) is still running: kill -0 = %v", pid, err)
	}
}

func TestDiffFileCap(t *testing.T) {
	t.Parallel()

	// The cap is lowered to keep the fixture small; TestNewEngineDefaults pins the default.
	const n = 22

	dir := branchRepo(t, func(dir string) { writeFile(t, dir, "base.txt", "b\n") }, func(dir string) {
		for i := range n {
			writeFile(t, dir, fmt.Sprintf("d%02d/f%05d.txt", i%5, i), fmt.Sprintf("file %d\nsecond\nthird\n", i))
		}
	})

	ctx, logPath := wrapGit(t, "")
	res := diffOKContext(ctx, t, NewEngine(EngineOptions{Limits: Limits{Files: 20}, TempDir: t.TempDir()}), BranchSource{Worktree: dir, Base: "main"})

	if len(res.Files) != n || !res.Truncated {
		t.Fatalf("files = %d, Truncated = %v, want %d and true", len(res.Files), res.Truncated, n)
	}

	for i, f := range res.Files {
		wantTrunc := i >= 20
		if f.Truncated != wantTrunc || f.Added != 3 || (len(f.Hunks) > 0) == wantTrunc {
			t.Fatalf("file %d %q: Truncated=%v Added=%d hunks=%d", i, f.Path, f.Truncated, f.Added, len(f.Hunks))
		}
	}

	if got := hunkPasses(t, logPath); got != 1 {
		t.Errorf("hunk passes = %d, want 1", got)
	}
}

func TestDiffListingLargerThanAPipeBuffer(t *testing.T) {
	t.Parallel()

	const files = 70

	long := strings.Repeat("long", 50)
	path := func(i int) string { return fmt.Sprintf("%s/%s/%s/f%03d.txt", long, long, long, i) }

	// The listing carries each path twice; it must outgrow the 64 KB pipe buffer.
	if 2*files*len(path(0)) <= 64<<10 {
		t.Fatalf("%d paths of %d bytes do not exceed a pipe buffer", files, len(path(0)))
	}

	dir := branchRepo(t, func(dir string) { writeFile(t, dir, "base.txt", "b\n") }, func(dir string) {
		for i := range files {
			writeFile(t, dir, path(i), fmt.Sprintf("file %d\n", i))
		}
	})

	res := branchDiff(t, dir)

	if res.Truncated || len(res.Files) != files {
		t.Fatalf("files = %d, Truncated = %v, want %d and false", len(res.Files), res.Truncated, files)
	}

	for i, f := range res.Files {
		if f.Path != path(i) || f.Added != 1 || f.Truncated || len(f.Hunks) != 1 {
			t.Fatalf("file %d %q: Added=%d Truncated=%v hunks=%d, want %q, 1, false, 1", i, f.Path, f.Added, f.Truncated, len(f.Hunks), path(i))
		}
	}
}

func TestDiffLineCap(t *testing.T) {
	t.Parallel()

	t.Run("60 changed lines", func(t *testing.T) {
		t.Parallel()

		dir := branchRepo(t, func(dir string) {
			writeFile(t, dir, "big.txt", numbered("base", 10))
			writeFile(t, dir, "small.txt", "s\n")
		}, func(dir string) {
			writeFile(t, dir, "big.txt", numbered("base", 10)+numbered("added", 60))
			writeFile(t, dir, "small.txt", "s changed\n")
		})

		res := diffOK(t, lineCapEngine(t), BranchSource{Worktree: dir, Base: "main"})
		golden(t, "diff_line_cap", Result{Old: res.Old, New: res.New, Live: res.Live, Truncated: res.Truncated, Files: []FileDiff{
			withoutHunks(fileByPath(t, res, "big.txt")), fileByPath(t, res, "small.txt"),
		}})

		big := fileByPath(t, res, "big.txt")
		if !big.Truncated || big.Added != 60 || big.Removed != 0 || len(big.Hunks) != 0 || !res.Truncated {
			t.Errorf("big.txt = Truncated:%v +%d -%d hunks:%d", big.Truncated, big.Added, big.Removed, len(big.Hunks))
		}

		if small := fileByPath(t, res, "small.txt"); small.Truncated || len(small.Hunks) != 1 {
			t.Errorf("small.txt = %+v", small)
		}
	})

	t.Run("no hunk pass for a file over the cap", func(t *testing.T) {
		t.Parallel()

		dir := branchRepo(t, func(dir string) { writeFile(t, dir, "a.txt", "a\n") }, func(dir string) {
			writeFile(t, dir, "huge.txt", numbered("l", 60))
		})

		ctx, logPath := wrapGit(t, "")
		res := diffOKContext(ctx, t, lineCapEngine(t), BranchSource{Worktree: dir, Base: "main"})

		f := fileByPath(t, res, "huge.txt")
		if !f.Truncated || f.Added != 60 || len(f.Hunks) != 0 || !res.Truncated {
			t.Errorf("huge.txt = Truncated:%v +%d hunks:%d", f.Truncated, f.Added, len(f.Hunks))
		}

		wrappedCalls(t, logPath)

		if got := hunkPasses(t, logPath); got != 0 {
			t.Errorf("hunk passes = %d, want none for a file over the line cap", got)
		}
	})
}

func lineCapEngine(t *testing.T) *Engine {
	t.Helper()

	return NewEngine(EngineOptions{Limits: Limits{Lines: 50}, TempDir: t.TempDir()})
}

func withoutHunks(f FileDiff) FileDiff {
	f.Hunks = nil

	return f
}

func TestDiffByteCapStopsGit(t *testing.T) {
	t.Parallel()

	const files = 12

	line := strings.Repeat("x", 40) + "\n"
	body := strings.Repeat(line, 400) // 17 KB per file, 200 KB in all

	dir := branchRepo(t, func(dir string) { writeFile(t, dir, "base.txt", "b\n") }, func(dir string) {
		for i := range files {
			writeFile(t, dir, fmt.Sprintf("f%02d.txt", i), body)
		}
	})

	ctx, logPath, pidFile := recordPid(t)
	res := diffOKContext(ctx, t, NewEngine(EngineOptions{Limits: Limits{Bytes: 50 << 10}, TempDir: t.TempDir()}), BranchSource{Worktree: dir, Base: "main"})

	assertProcessGone(t, pidFile)

	if got := hunkPasses(t, logPath); got != 1 {
		t.Errorf("hunk passes = %d, want 1", got)
	}

	if !res.Truncated || len(res.Files) != files {
		t.Fatalf("Truncated = %v, files = %d", res.Truncated, len(res.Files))
	}

	firstCut := -1

	for i, f := range res.Files {
		if f.Added != 400 {
			t.Errorf("%s: Added = %d, counts must survive truncation", f.Path, f.Added)
		}

		switch {
		case f.Truncated && firstCut < 0:
			firstCut = i
		case !f.Truncated && firstCut >= 0:
			t.Errorf("%s has hunks after the cut at file %d", f.Path, firstCut)
		}
	}

	// 50 KiB holds 3 whole files of 17 KB; the 4th is cut. The 200 KB total
	// outgrows the pipe buffer plus the cap, so git is blocked when it is killed.
	if firstCut != 3 {
		t.Errorf("first truncated file = %d, want 3", firstCut)
	}
}

func TestDiffBinaryFiles(t *testing.T) {
	t.Parallel()

	dir := branchRepo(t, func(dir string) {
		writeFile(t, dir, "old.bin", "\x00old")
		writeFile(t, dir, "gone.bin", "\x00gone")
	}, func(dir string) {
		writeFile(t, dir, "old.bin", "\x00new")
		must(t, os.Remove(filepath.Join(dir, "gone.bin")))
		writeFile(t, dir, "added.bin", "\x00"+strings.Repeat("z", 64))
	})

	ctx, logPath := wrapGit(t, "")
	res := branchDiffContext(ctx, t, dir)
	golden(t, "diff_binary", res)

	for _, f := range res.Files {
		if !f.Binary || f.Truncated || len(f.Hunks) != 0 {
			t.Errorf("%s: Binary=%v Truncated=%v hunks=%d", f.Path, f.Binary, f.Truncated, len(f.Hunks))
		}
	}

	if res.Truncated {
		t.Error("binary files do not truncate a result")
	}

	wrappedCalls(t, logPath)

	if got := hunkPasses(t, logPath); got != 0 {
		t.Errorf("hunk passes = %d, want none", got)
	}
}

// Cache.

func TestDiffCacheSkipsTheHunkPass(t *testing.T) {
	t.Parallel()

	dir := branchRepo(t, func(dir string) { writeFile(t, dir, "a.txt", numbered("a", 10)) }, func(dir string) {
		writeFile(t, dir, "a.txt", numbered("a", 10)+"more\n")
		writeFile(t, dir, "b.txt", "new\n")
	})

	ctx, logPath := wrapGit(t, "")
	e := testEngine(t)
	src := BranchSource{Worktree: dir, Base: "main"}

	first := diffOKContext(ctx, t, e, src)
	second := diffOKContext(ctx, t, e, src)

	if got := hunkPasses(t, logPath); got != 1 {
		t.Errorf("hunk passes after two diffs = %d, want 1", got)
	}

	if !bytes.Equal(goldenJSON(t, first), goldenJSON(t, second)) {
		t.Error("a cached diff differs from the first")
	}

	writeFile(t, dir, "b.txt", "changed\n")
	diffOKContext(ctx, t, e, src)

	if got := hunkPasses(t, logPath); got != 2 {
		t.Errorf("hunk passes after a changed file = %d, want 2", got)
	}
}

func TestHunkCacheEvictsLeastRecentlyUsed(t *testing.T) {
	t.Parallel()

	c := newHunkCache(cacheBytes)
	hunks := []Hunk{{Lines: []Line{{Text: "x"}}}}
	key := func(i int) hunkKey { return hunkKey{old: strconv.Itoa(i), new: "n"} }

	for i := range cacheEntries {
		c.put(key(i), hunks)
	}

	if _, ok := c.get(key(0)); !ok { // key 0 becomes the most recent
		t.Fatal("key 0 missing before the cache is full")
	}

	c.put(key(cacheEntries), hunks) // 2049th distinct pair

	if _, ok := c.get(key(1)); ok {
		t.Error("the least recently used pair survived")
	}

	for _, i := range []int{0, 2, cacheEntries} {
		if _, ok := c.get(key(i)); !ok {
			t.Errorf("pair %d was evicted", i)
		}
	}

	if c.order.Len() != cacheEntries || len(c.items) != cacheEntries {
		t.Errorf("entries = %d/%d, want %d", c.order.Len(), len(c.items), cacheEntries)
	}
}

func TestHunkCacheBoundsTheTextItHolds(t *testing.T) {
	t.Parallel()

	c := newHunkCache(1000)
	big := []Hunk{{Lines: []Line{{Text: strings.Repeat("x", 400)}}}}

	for i := range 10 {
		c.put(hunkKey{old: strconv.Itoa(i)}, big)
	}

	if c.bytes > 1000 || c.order.Len() == 0 {
		t.Errorf("bytes = %d, entries = %d", c.bytes, c.order.Len())
	}

	c.put(hunkKey{old: "huge"}, []Hunk{{Lines: []Line{{Text: strings.Repeat("x", 5000)}}}})

	if _, ok := c.get(hunkKey{old: "huge"}); ok {
		t.Error("an entry over the whole budget was kept")
	}
}

func TestDiffConcurrent(t *testing.T) {
	t.Parallel()
	dir := branchRepo(t, func(dir string) { writeFile(t, dir, "a.txt", numbered("a", 10)) }, func(dir string) {
		writeFile(t, dir, "a.txt", numbered("a", 10)+"more\n")
		writeFile(t, dir, "b.txt", "new\n")
	})

	e := testEngine(t)
	src := BranchSource{Worktree: dir, Base: "main"}
	first, err := json.Marshal(diffOK(t, e, src))
	must(t, err)

	errs := make(chan error, 8)

	for range cap(errs) {
		go func() {
			res, err := e.Diff(context.Background(), src)
			if err == nil {
				var got []byte
				if got, err = json.Marshal(res); err == nil && !bytes.Equal(got, first) {
					err = errors.New("result differs")
				}
			}

			errs <- err
		}()
	}

	deadline := time.After(time.Minute)

	for range cap(errs) {
		select {
		case err := <-errs:
			if err != nil {
				t.Error(err)
			}
		case <-deadline:
			t.Fatal("concurrent diffs timed out")
		}
	}

	assertEmptyDir(t, e.tempDir)
}

func TestNewEngineDefaults(t *testing.T) {
	t.Parallel()

	e := NewEngine(EngineOptions{Limits: Limits{Files: 7}})

	if e.maxText != 8<<20 {
		t.Errorf("maxText = %d, want %d", e.maxText, 8<<20)
	}

	if want := (Limits{Files: 7, Bytes: 2 << 20, Lines: 5000}); e.limits != want {
		t.Errorf("limits = %+v, want %+v", e.limits, want)
	}

	if got, want := DefaultLimits(), (Limits{Files: 400, Bytes: 2 << 20, Lines: 5000}); got != want {
		t.Errorf("DefaultLimits = %+v, want %+v", got, want)
	}
}

func TestDiffByteCapInsideTheFirstFile(t *testing.T) {
	t.Parallel()
	dir := branchRepo(t, func(dir string) { writeFile(t, dir, "base.txt", "b\n") }, func(dir string) {
		writeFile(t, dir, "a.txt", numbered("first", 200))
		writeFile(t, dir, "b.txt", "tiny\n")
	})

	res := diffOK(t, NewEngine(EngineOptions{Limits: Limits{Bytes: 500}, TempDir: t.TempDir()}), BranchSource{Worktree: dir, Base: "main"})

	for _, f := range res.Files {
		if !f.Truncated || len(f.Hunks) != 0 {
			t.Errorf("%s: Truncated=%v hunks=%d, want every file cut", f.Path, f.Truncated, len(f.Hunks))
		}
	}

	if fileByPath(t, res, "a.txt").Added != 200 || !res.Truncated {
		t.Error("counts were lost or the result is not marked Truncated")
	}
}

func TestWholeFiles(t *testing.T) {
	t.Parallel()

	tests := []struct{ in, want string }{
		{"", ""},
		{"diff --git a/x b/x\n+one\n", ""},
		{"diff --git a/x b/x\n+one\ndiff --git a/y b/y\n+tw", "diff --git a/x b/x\n+one\n"},
		{"diff --git a/x b/x\n+diff --git inside content\ndiff --git a/y b/y\n", "diff --git a/x b/x\n+diff --git inside content\n"},
	}

	for _, tt := range tests {
		if got := string(wholeFiles([]byte(tt.in))); got != tt.want {
			t.Errorf("wholeFiles(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

func TestDiffCancelledDuringTheHunkPass(t *testing.T) {
	dir := branchRepo(t, func(dir string) { writeFile(t, dir, "base.txt", "b\n") }, func(dir string) {
		writeFile(t, dir, "a.txt", "a\n")
	})

	// The hunk pass hangs; it marks its end only if the cancel failed to kill it.
	logPath := fakeGit(t, `case "$*" in *-U3*) : > "$LOG.hunks"; sleep 30; : > "$LOG.finished"; exit 1;; esac`)

	ctx := cancelWhenWritten(t, logPath+".hunks")
	_, err := testEngine(t).Diff(ctx, BranchSource{Worktree: dir, Base: "main"})

	if !errors.Is(err, context.Canceled) {
		t.Errorf("err = %v, want context.Canceled", err)
	}

	if _, err := os.Stat(logPath + ".finished"); err == nil {
		t.Error("Diff waited for the hunk pass to finish")
	}
}

// hostileDiffConfig is user diff configuration that changes git's patch text;
// the engine must read the same diff under it as under a clean config.
func hostileDiffConfig(t *testing.T, dir string) (mark string) {
	t.Helper()

	mark = filepath.Join(t.TempDir(), "external-ran")
	script := filepath.Join(t.TempDir(), "ext.sh")
	writeFile(t, filepath.Dir(script), filepath.Base(script), "#!/bin/sh\necho ran >> '"+mark+"'\n")
	must(t, os.Chmod(script, 0o755))

	// Puts the last paths of the listing first.
	order := filepath.Join(t.TempDir(), "order")
	writeFile(t, filepath.Dir(order), filepath.Base(order), "sm\nmoved*\n")

	for k, v := range map[string]string{
		"diff.submodule": "log", "diff.suppressBlankEmpty": "true", "diff.noprefix": "true",
		"diff.mnemonicPrefix": "true", "color.diff": "always", "color.ui": "always",
		"diff.relative": "true", "diff.renames": "false", "diff.algorithm": "minimal",
		"diff.context": "10", "diff.interHunkContext": "20", "diff.ignoreSubmodules": "all",
		"diff.external": script, "diff.orderFile": order,
	} {
		gitOut(t, dir, "config", k, v)
	}

	t.Setenv("GIT_DIFF_OPTS", "--unified=0")

	return mark
}

func TestDiffIgnoresUserDiffConfig(t *testing.T) {
	t.Setenv("GIT_AUTHOR_DATE", "2026-01-01T00:00:00Z")
	t.Setenv("GIT_COMMITTER_DATE", "2026-01-01T00:00:00Z")

	sub := newRepo(t)
	writeFile(t, sub, "s.txt", "s\n")
	commitAll(t, sub, "sub")

	blank := "one\n\ntwo\n\nthree\n\nfour\n\nfive\n\nsix\n\nseven\n"

	dir := branchRepo(t, func(dir string) {
		writeFile(t, dir, "blank.txt", blank)
		writeFile(t, dir, "moved-from.txt", numbered("move", 20))
		gitOut(t, dir, "-c", "protocol.file.allow=always", "submodule", "add", "-q", sub, "sm")
	}, func(dir string) {
		writeFile(t, filepath.Join(dir, "sm"), "s.txt", "moved\n")
		gitOut(t, filepath.Join(dir, "sm"), "commit", "-q", "-am", "moved")
		gitOut(t, dir, "add", "sm")
		writeFile(t, dir, "blank.txt", strings.Replace(blank, "four", "FOUR", 1))
		must(t, os.Rename(filepath.Join(dir, "moved-from.txt"), filepath.Join(dir, "moved-to.txt")))
	})

	snap(t, dir, sessA, SnapshotOptions{})
	writeFile(t, dir, "blank.txt", strings.Replace(blank, "four", "FOUR again", 1))
	writeFile(t, dir, "new.txt", "n\n")

	sources := map[string]Source{
		"branch": BranchSource{Worktree: dir, Base: "main"},
		"turn":   TurnSource{Worktree: dir, Session: sessA},
	}
	clean := map[string][]byte{}

	for name, src := range sources {
		res := diffOK(t, testEngine(t), src)
		if name == "branch" && fileByPath(t, res, "sm").Status != Modified {
			t.Fatalf("setup: sm = %+v", fileByPath(t, res, "sm"))
		}

		b, err := json.Marshal(res)
		must(t, err)

		clean[name] = b
	}

	mark := hostileDiffConfig(t, dir)

	for name, src := range sources {
		b, err := json.Marshal(diffOK(t, testEngine(t), src))
		must(t, err)

		if !bytes.Equal(b, clean[name]) {
			t.Errorf("%s diff changed under user diff config:\n%s\nwant\n%s", name, b, clean[name])
		}
	}

	if _, err := os.Stat(mark); err == nil {
		t.Error("diff.external ran")
	}
}

func TestPastTurnStaysInItsWorktree(t *testing.T) {
	t.Parallel()
	dir := seedRepo(t)
	b := linkedWorktree(t, dir, "b")

	writeFile(t, dir, "a.txt", "a\nonly in main\n")
	snap(t, dir, sessA, SnapshotOptions{})
	snap(t, b, sessA, SnapshotOptions{})
	writeFile(t, b, "b-only.txt", "b\n")

	e := testEngine(t)

	for name, wt := range map[string]string{"b": b, "main": dir} {
		res, err := e.Diff(context.Background(), TurnSource{Worktree: wt, Session: sessA, Turn: 1})
		if err != nil {
			if !errors.Is(err, ErrNoSnapshot) {
				t.Fatalf("%s: %v", name, err)
			}

			continue
		}

		for _, f := range res.Files {
			if f.Path == "a.txt" {
				t.Errorf("%s: turn 1 shows a.txt, which belongs to the other worktree's state", name)
			}
		}
	}
}
