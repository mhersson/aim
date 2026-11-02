package git

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
)

// updateRepo commits a.txt, b.txt, c.txt and dir/d.txt on main, then branches.
func updateRepo(t *testing.T) string {
	t.Helper()

	return copyTemplate(t, &updateTemplate, func(dir string) error {
		files := map[string]string{}
		for _, n := range []string{"a.txt", "b.txt", "c.txt", "dir/d.txt", "dir/e.txt"} {
			files[n] = numbered(n, 30)
		}

		if err := buildRepo(dir, files); err != nil {
			return err
		}

		if err := gitRun(dir, "checkout", "-q", "-b", "feature"); err != nil {
			return err
		}

		return writeBuilt(dir, "a.txt", numbered("a.txt", 30)+"extra\n")
	})
}

func updateOK(t *testing.T, e *Engine, prev Result, src Source, b Batch) Result {
	t.Helper()

	return updateOKContext(context.Background(), t, e, prev, src, b)
}

// updateOKContext is updateOK with the context its git calls run under.
func updateOKContext(ctx context.Context, t *testing.T, e *Engine, prev Result, src Source, b Batch) Result {
	t.Helper()

	res, err := e.Update(ctx, prev, src, b)
	if err != nil {
		t.Fatalf("Update: %v", err)
	}

	assertEmptyDir(t, e.tempDir)
	checkInvariants(t, res)

	return res
}

// sameAsFull fails unless res equals a fresh Diff on a fresh engine.
func sameAsFull(t *testing.T, res Result, src Source) {
	t.Helper()

	want := diffOK(t, testEngine(t), src)
	if !reflect.DeepEqual(res, want) {
		t.Errorf("Update differs from a full Diff\n got: %s\nwant: %s", goldenJSON(t, res), goldenJSON(t, want))
	}
}

// listingCalls counts the listing passes in the fake git log, split by whether
// they were limited to paths.
func listingCalls(t *testing.T, logPath string) (restricted, full int) {
	t.Helper()

	data, err := os.ReadFile(logPath)
	if err != nil {
		return 0, 0
	}

	for line := range strings.SplitSeq(string(data), "\n") {
		if !strings.Contains(line, " --raw ") {
			continue
		}

		if strings.Contains(line, ":(literal)") {
			restricted++
		} else {
			full++
		}
	}

	return restricted, full
}

func TestUpdateTouchedFileEqualsFullDiff(t *testing.T) {
	t.Parallel()

	dir := updateRepo(t)
	src := BranchSource{Worktree: dir, Base: "main"}
	e := testEngine(t)
	prev := diffOK(t, e, src)

	ctx, logPath := wrapGit(t, "")

	writeFile(t, dir, "b.txt", numbered("b.txt", 30)+"new tail\n")

	before := hunkPasses(t, logPath)
	res := updateOKContext(ctx, t, e, prev, src, Batch{Paths: []string{"b.txt"}})

	got := hunkPasses(t, logPath) - before
	data, _ := os.ReadFile(logPath)

	sameAsFull(t, res, src)

	if got != 1 {
		t.Errorf("hunk passes = %d, want 1", got)
	}

	for line := range strings.SplitSeq(string(data), "\n") {
		if strings.Contains(line, " -U3 ") && (strings.Contains(line, "(literal)a.txt") || !strings.Contains(line, "(literal)b.txt")) {
			t.Errorf("hunk pass is not limited to b.txt: %s", line)
		}
	}
}

func TestUpdateShapesEqualFullDiff(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		setup   func(t *testing.T, dir string) // before prev is computed
		change  func(t *testing.T, dir string)
		touched []string
	}{
		{"revert a changed file", nil, func(t *testing.T, dir string) { writeFile(t, dir, "a.txt", numbered("a.txt", 30)) }, []string{"a.txt"}},
		{"delete", nil, func(t *testing.T, dir string) { must(t, os.Remove(filepath.Join(dir, "b.txt"))) }, []string{"b.txt"}},
		{"untracked file", nil, func(t *testing.T, dir string) { writeFile(t, dir, "new.txt", "fresh\nfile\n") }, []string{"new.txt"}},
		{"removed directory", nil, func(t *testing.T, dir string) { must(t, os.RemoveAll(filepath.Join(dir, "dir"))) }, []string{"dir"}},
		{"touched path that never existed", nil, func(t *testing.T, dir string) {}, []string{"ghost.txt"}},
		{"two files", nil, func(t *testing.T, dir string) {
			writeFile(t, dir, "b.txt", "x\n")
			writeFile(t, dir, "c.txt", "y\n")
		}, []string{"b.txt", "c.txt"}},
		{"identical content added while another file is deleted", func(t *testing.T, dir string) {
			must(t, os.Remove(filepath.Join(dir, "c.txt")))
		}, func(t *testing.T, dir string) {
			writeFile(t, dir, "moved.txt", numbered("c.txt", 30))
		}, []string{"moved.txt"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			dir := updateRepo(t)
			src := BranchSource{Worktree: dir, Base: "main"}
			e := testEngine(t)

			if tt.setup != nil {
				tt.setup(t, dir)
			}

			prev := diffOK(t, e, src)

			tt.change(t, dir)

			res := updateOK(t, e, prev, src, Batch{Paths: tt.touched})
			sameAsFull(t, res, src)
		})
	}
}

// A file the old side holds reads as a modification on the incremental path
// too, though the working tree does not track it: only a file the old side
// lacks is Untracked. Each case checks the update listed only the touched
// path, because a full listing would give the right answer for another reason
// (a retained added file next to a touched change makes Update recompute whole).
func TestUpdateJudgesAFileTheOldSideHoldsByTheComparison(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		// prepare returns the worktree, the source and the path of a file that
		// the old side holds and the working tree does not track.
		prepare func(t *testing.T) (dir string, src Source, path string)
		// first and second are its contents before the first result and after it.
		first, second string
	}{
		{
			name: "a turn that snapshotted an untracked file",
			prepare: func(t *testing.T) (string, Source, string) {
				dir := seedRepo(t)
				writeFile(t, dir, "x.go", numbered("x", 40))
				snap(t, dir, sessA, SnapshotOptions{})

				return dir, TurnSource{Worktree: dir, Session: sessA}, "x.go"
			},
			first:  strings.Replace(numbered("x", 40), "x 35\n", "x 35\nx 35a\nx 35b\n", 1),
			second: strings.Replace(numbered("x", 40), "x 35\n", "x 35\nx 35a\nx 35b\nx 35c\n", 1),
		},
		{
			name: "a branch whose base holds a file removed from the index",
			prepare: func(t *testing.T) (string, Source, string) {
				dir := updateRepo(t)
				gitOut(t, dir, "rm", "-q", "--cached", "b.txt")

				return dir, BranchSource{Worktree: dir, Base: "main"}, "b.txt"
			},
			first:  numbered("b.txt", 30) + "first\n",
			second: numbered("b.txt", 30) + "first\nsecond\n",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			dir, src, path := tt.prepare(t)
			writeFile(t, dir, path, tt.first)

			e := testEngine(t)
			prev := diffOK(t, e, src)

			// The starting point: a live result that holds the file as a modification.
			if f := fileByPath(t, prev, path); !prev.Live || f.Status != Modified || f.Untracked {
				t.Fatalf("setup: live = %v, %s = %+v, want a modification of the old side's file", prev.Live, path, f)
			}

			writeFile(t, dir, path, tt.second)

			ctx, logPath := wrapGit(t, "")
			upd := updateOKContext(ctx, t, e, prev, src, Batch{Paths: []string{path}})

			if restricted, full := listingCalls(t, logPath); restricted == 0 || full != 0 {
				t.Fatalf("listings: restricted=%d full=%d, want the incremental path (touched paths only)", restricted, full)
			}

			if f := fileByPath(t, upd, path); f.Status != Modified || f.Untracked || f.Added == 0 {
				t.Errorf("%s after update = %+v, want a modification that is not untracked", path, f)
			}

			sameAsFull(t, upd, src)
		})
	}
}

func TestUpdateFallsBackToFullDiff(t *testing.T) {
	t.Parallel()

	many := make([]string, 201)
	for i := range many {
		many[i] = fmt.Sprintf("many/f%03d.txt", i)
	}

	tests := []struct {
		name   string
		change func(t *testing.T, dir string)
		batch  Batch
		// listed: the rename is only found by listing the touched paths first.
		listed bool
	}{
		{"rename", func(t *testing.T, dir string) {
			must(t, os.Rename(filepath.Join(dir, "b.txt"), filepath.Join(dir, "b2.txt")))
		}, Batch{Paths: []string{"b.txt", "b2.txt"}}, true},
		{"git metadata", func(t *testing.T, dir string) { writeFile(t, dir, "b.txt", "x\n") }, Batch{Paths: []string{"b.txt"}, Git: true}, false},
		{"overflow", func(t *testing.T, dir string) { writeFile(t, dir, "b.txt", "x\n") }, Batch{Overflow: true}, false},
		{"ignore file", func(t *testing.T, dir string) { writeFile(t, dir, "dir/.gitignore", "*.log\n") }, Batch{Paths: []string{"dir/.gitignore"}}, false},
		{"too many paths", func(t *testing.T, dir string) { writeFile(t, dir, "b.txt", "x\n") }, Batch{Paths: many}, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			dir := updateRepo(t)
			src := BranchSource{Worktree: dir, Base: "main"}
			e := testEngine(t)
			prev := diffOK(t, e, src)

			tt.change(t, dir)

			ctx, logPath := wrapGit(t, "")
			res := updateOKContext(ctx, t, e, prev, src, tt.batch)
			restricted, full := listingCalls(t, logPath)

			sameAsFull(t, res, src)

			if full == 0 || (restricted != 0 && !tt.listed) {
				t.Errorf("listings: restricted=%d full=%d", restricted, full)
			}
		})
	}
}

func TestUpdateFallsBackWhenPrevHasRenames(t *testing.T) {
	t.Parallel()

	dir := updateRepo(t)
	must(t, os.Rename(filepath.Join(dir, "b.txt"), filepath.Join(dir, "b2.txt")))

	src := BranchSource{Worktree: dir, Base: "main"}
	e := testEngine(t)
	prev := diffOK(t, e, src)

	if fileByPath(t, prev, "b2.txt").Status != Renamed {
		t.Fatal("setup: b2.txt is not a rename")
	}

	writeFile(t, dir, "c.txt", "x\n")

	ctx, logPath := wrapGit(t, "")
	res := updateOKContext(ctx, t, e, prev, src, Batch{Paths: []string{"c.txt"}})

	wrappedCalls(t, logPath)

	restricted, _ := listingCalls(t, logPath)

	sameAsFull(t, res, src)

	if restricted != 0 {
		t.Error("a restricted listing ran next to a rename")
	}
}

func TestUpdatePastTurnFallsBack(t *testing.T) {
	t.Parallel()

	dir := turnRepo(t)
	writeFile(t, dir, "a.txt", "turn two\n")
	snap(t, dir, sessA, SnapshotOptions{})

	e := testEngine(t)
	src := TurnSource{Worktree: dir, Session: sessA, Turn: 1}
	prev := diffOK(t, e, src)

	if prev.Live {
		t.Fatal("setup: turn 1 is live")
	}

	ctx, logPath := wrapGit(t, "")
	res := updateOKContext(ctx, t, e, prev, src, Batch{Paths: []string{"a.txt"}})
	restricted, full := listingCalls(t, logPath)

	sameAsFull(t, res, src)

	if restricted != 0 || full == 0 {
		t.Errorf("listings: restricted=%d full=%d", restricted, full)
	}
}

func TestUpdateLiveTurn(t *testing.T) {
	t.Parallel()

	dir := turnRepo(t)
	writeFile(t, dir, "a.txt", "edited during the turn\n")

	e := testEngine(t)
	src := TurnSource{Worktree: dir, Session: sessA}
	prev := diffOK(t, e, src)

	writeFile(t, dir, "b.txt", "edited after the prompt\n")

	ctx, logPath := wrapGit(t, "")
	res := updateOKContext(ctx, t, e, prev, src, Batch{Paths: []string{"b.txt"}})
	restricted, _ := listingCalls(t, logPath)

	sameAsFull(t, res, src)

	if restricted != 1 {
		t.Errorf("restricted listings = %d, want 1", restricted)
	}

	// A newer turn changes the old side: no incremental update.
	snap(t, dir, sessA, SnapshotOptions{})
	writeFile(t, dir, "c.txt", "later\n")

	ctx, logPath = wrapGit(t, "")
	res = updateOKContext(ctx, t, e, res, src, Batch{Paths: []string{"c.txt"}})

	wrappedCalls(t, logPath)

	restricted, _ = listingCalls(t, logPath)

	sameAsFull(t, res, src)

	if restricted != 0 {
		t.Error("a restricted listing ran after a new turn")
	}
}

// A turn that ended is a start against an end now, so the live result is no
// base for an incremental update.
func TestUpdateEndedTurnFallsBack(t *testing.T) {
	t.Parallel()

	dir := turnRepo(t)
	writeFile(t, dir, "a.txt", "edited during the turn\n")

	e := testEngine(t)
	src := TurnSource{Worktree: dir, Session: sessA}
	prev := diffOK(t, e, src)

	writeFile(t, dir, "b.txt", "edited before the next prompt\n")

	end, err := EndTurn(context.Background(), dir, sessA, 1, SnapshotOptions{TempDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}

	writeFile(t, dir, "c.txt", "after the end\n")

	res := updateOK(t, e, prev, src, Batch{Paths: []string{"c.txt"}})

	if res.Live || res.New != end.EndTree {
		t.Errorf("Live=%v New=%s, want the closed turn ending at %s", res.Live, res.New, end.EndTree)
	}

	sameAsFull(t, res, src)
}

func TestUpdateFallsBackWhenTheLiveTurnStopsCounting(t *testing.T) {
	t.Parallel()

	dir := seedRepo(t)
	snap(t, dir, sessA, SnapshotOptions{})
	writeFile(t, dir, "a.txt", "one\n")
	snap(t, dir, sessA, SnapshotOptions{})
	writeFile(t, dir, "a.txt", "two\n")

	e := testEngine(t)
	src := TurnSource{Worktree: dir, Session: sessA}
	prev := diffOK(t, e, src)

	if !prev.Live || prev.Turn != 2 || prev.Turns != 2 {
		t.Fatalf("setup: Live=%v Turn=%d Turns=%d, want turn 2 of 2 live", prev.Live, prev.Turn, prev.Turns)
	}

	writeFile(t, dir, "a.txt", "one\n")

	res := updateOK(t, e, prev, src, Batch{Paths: []string{"a.txt"}})

	if res.Live || res.Turn != 1 || res.Turns != 1 {
		t.Errorf("Live=%v Turn=%d Turns=%d, want the previous turn, 1 of 1", res.Live, res.Turn, res.Turns)
	}

	sameAsFull(t, res, src)
}

func TestUpdateKeepsCaps(t *testing.T) {
	t.Parallel()
	dir := branchRepo(t, func(dir string) {
		for i := range 6 {
			writeFile(t, dir, fmt.Sprintf("f%d.txt", i), numbered("x", 10))
		}
	}, func(dir string) {
		for i := range 4 {
			writeFile(t, dir, fmt.Sprintf("f%d.txt", i), numbered("y", 10))
		}
	})
	src := BranchSource{Worktree: dir, Base: "main"}
	e := NewEngine(EngineOptions{Limits: Limits{Files: 5, Lines: 30}, TempDir: t.TempDir()})

	prev := diffOK(t, e, src)

	// The sixth changed file goes past the file cap, which a full Diff marks.
	writeFile(t, dir, "f4.txt", "z\n")
	writeFile(t, dir, "f5.txt", "z\n")

	res := updateOK(t, e, prev, src, Batch{Paths: []string{"f4.txt", "f5.txt"}})

	want := diffOK(t, NewEngine(EngineOptions{Limits: Limits{Files: 5, Lines: 30}, TempDir: t.TempDir()}), src)
	if !reflect.DeepEqual(res, want) {
		t.Errorf("capped Update differs from a full Diff\n got: %s\nwant: %s", goldenJSON(t, res), goldenJSON(t, want))
	}

	// A touched file over the line cap is marked like a full Diff marks it.
	writeFile(t, dir, "f0.txt", numbered("w", 40))

	res = updateOK(t, e, res, src, Batch{Paths: []string{"f0.txt"}})
	want = diffOK(t, NewEngine(EngineOptions{Limits: Limits{Files: 5, Lines: 30}, TempDir: t.TempDir()}), src)

	if !reflect.DeepEqual(res, want) || !res.Truncated {
		t.Errorf("line cap: got %s\nwant %s", goldenJSON(t, res), goldenJSON(t, want))
	}
}

func TestUpdateFallsBackPastTheByteCap(t *testing.T) {
	t.Parallel()

	dir := branchRepo(t, func(dir string) {
		for i := range 4 {
			writeFile(t, dir, fmt.Sprintf("f%d.txt", i), numbered("x", 100))
		}
	}, func(dir string) {
		for i := range 3 {
			writeFile(t, dir, fmt.Sprintf("f%d.txt", i), numbered("y", 100))
		}
	})
	src := BranchSource{Worktree: dir, Base: "main"}
	e := NewEngine(EngineOptions{Limits: Limits{Bytes: 5000}, TempDir: t.TempDir()})
	prev := diffOK(t, e, src)

	writeFile(t, dir, "f3.txt", numbered("y", 100))

	ctx, logPath := wrapGit(t, "")
	res := updateOKContext(ctx, t, e, prev, src, Batch{Paths: []string{"f3.txt"}})

	_, full := listingCalls(t, logPath)

	// Hunks cached from prev skip the byte budget, so compare on the same engine.
	if want := diffOK(t, e, src); !reflect.DeepEqual(res, want) {
		t.Errorf("Update differs from a full Diff\n got: %s\nwant: %s", goldenJSON(t, res), goldenJSON(t, want))
	}

	if full == 0 {
		t.Error("the merged files cross the byte cap but no full Diff ran")
	}
}

func TestUpdateNewFileBesideChangesRunsAFullListing(t *testing.T) {
	t.Parallel()

	dir := updateRepo(t) // a.txt is modified on the branch
	src := BranchSource{Worktree: dir, Base: "main"}
	e := testEngine(t)
	prev := diffOK(t, e, src)

	writeFile(t, dir, "fresh.txt", "a new file\n")

	ctx, logPath := wrapGit(t, "")
	res := updateOKContext(ctx, t, e, prev, src, Batch{Paths: []string{"fresh.txt"}})
	restricted, full := listingCalls(t, logPath)

	sameAsFull(t, res, src)

	// Git may pair a new file with a changed one as a rename or copy.
	if full != 1 || restricted != 1 {
		t.Errorf("listings: restricted=%d full=%d, want the touched listing and then a full one", restricted, full)
	}
}

// openCalls counts the rev-parse calls Open makes.
func openCalls(t *testing.T, logPath string) int {
	t.Helper()

	data, err := os.ReadFile(logPath)
	if err != nil {
		return 0
	}

	return strings.Count(string(data), "--show-toplevel")
}

func TestUpdateKeepsOneRepoPerWorktree(t *testing.T) {
	t.Parallel()

	dirA, dirB := updateRepo(t), updateRepo(t)
	srcA, srcB := BranchSource{Worktree: dirA, Base: "main"}, BranchSource{Worktree: dirB, Base: "main"}
	e := testEngine(t)
	prevA, prevB := diffOK(t, e, srcA), diffOK(t, e, srcB)

	ctx, logPath := wrapGit(t, "")

	// Each repo has its own edit, so a repo taken for the other's would show.
	writeFile(t, dirA, "b.txt", numbered("b.txt", 30)+"in a\n")
	writeFile(t, dirB, "c.txt", numbered("c.txt", 30)+"in b\n")

	resA := updateOKContext(ctx, t, e, prevA, srcA, Batch{Paths: []string{"b.txt"}})
	resB := updateOKContext(ctx, t, e, prevB, srcB, Batch{Paths: []string{"c.txt"}})

	writeFile(t, dirA, "dir/d.txt", numbered("dir/d.txt", 30)+"again in a\n")

	resA = updateOKContext(ctx, t, e, resA, srcA, Batch{Paths: []string{"dir/d.txt"}})
	if n := openCalls(t, logPath); n != 2 {
		t.Errorf("opens = %d, want 2: one per worktree", n)
	}

	sameAsFull(t, resA, srcA)
	sameAsFull(t, resB, srcB)
}

func TestUpdateReopensAWorktreeWhoseGitDirMoved(t *testing.T) {
	t.Parallel()

	dir := updateRepo(t)
	wt := filepath.Join(t.TempDir(), "wt")
	gitOut(t, dir, "worktree", "add", "-q", "-b", "other", wt, "main")

	src := BranchSource{Worktree: wt, Base: "main"}
	e := testEngine(t)
	prev := diffOK(t, e, src)

	ctx, logPath := wrapGit(t, "")

	writeFile(t, wt, "b.txt", numbered("b.txt", 30)+"first\n")

	res := updateOKContext(ctx, t, e, prev, src, Batch{Paths: []string{"b.txt"}})
	if n := openCalls(t, logPath); n != 1 {
		t.Fatalf("opens = %d, want 1", n)
	}

	// Rename the worktree's admin dir and point its .git file at the new name.
	common := gitOut(t, dir, "rev-parse", "--path-format=absolute", "--git-common-dir")
	oldAdmin := filepath.Join(common, "worktrees", "wt")
	newAdmin := filepath.Join(common, "worktrees", "moved")
	must(t, os.Rename(oldAdmin, newAdmin))
	writeFile(t, wt, ".git", "gitdir: "+newAdmin+"\n")

	writeFile(t, wt, "c.txt", numbered("c.txt", 30)+"second\n")

	res = updateOKContext(ctx, t, e, res, src, Batch{Paths: []string{"c.txt"}})
	if n := openCalls(t, logPath); n != 2 {
		t.Errorf("opens = %d, want 2: the moved git dir needs the repo opened again", n)
	}

	if r, ok := e.repos.get(wt); !ok || filepath.Base(r.GitDir) != "moved" {
		t.Errorf("cached repo = %+v, %v, want the moved git dir", r, ok)
	}

	sameAsFull(t, res, src)
}

// A linked worktree removed without a prune leaves its admin dir, so the cached
// git dirs still exist and the error comes from running git in the gone
// directory; the entry is dropped all the same.
func TestUpdateOfAGoneLinkedWorktreeIsAnError(t *testing.T) {
	t.Parallel()
	dir := updateRepo(t)
	wt := filepath.Join(t.TempDir(), "wt")
	gitOut(t, dir, "worktree", "add", "-q", "-b", "other", wt, "main")

	src := BranchSource{Worktree: wt, Base: "main"}
	e := testEngine(t)
	prev := diffOK(t, e, src)

	writeFile(t, wt, "b.txt", numbered("b.txt", 30)+"first\n")

	res := updateOK(t, e, prev, src, Batch{Paths: []string{"b.txt"}})

	cached, ok := e.repos.get(wt)
	if !ok {
		t.Fatal("setup: the update did not cache its repo")
	}

	must(t, os.RemoveAll(wt))

	if !gitDirsExist(cached) {
		t.Fatalf("setup: the admin dir %s went with the worktree", cached.GitDir)
	}

	if _, err := e.Update(context.Background(), res, src, Batch{Paths: []string{"b.txt"}}); err == nil {
		t.Error("Update of a removed linked worktree succeeded")
	}

	if _, ok := e.repos.get(wt); ok {
		t.Error("the removed worktree's repo is still cached")
	}
}

// A failed update drops its repo, which may be what failed. The fake git fails
// the next listing call once, when the test creates "$LOG.fail".
func TestUpdateOpensAgainAfterAFailedUpdate(t *testing.T) {
	t.Parallel()

	dir := updateRepo(t)
	src := BranchSource{Worktree: dir, Base: "main"}
	e := testEngine(t)
	prev := diffOK(t, e, src)

	ctx, logPath := wrapGit(t, `case "$*" in *--raw*)
	if [ -e "$LOG.fail" ]; then rm "$LOG.fail"; echo "fatal: injected" >&2; exit 1; fi;;
esac`)

	writeFile(t, dir, "b.txt", numbered("b.txt", 30)+"first\n")

	res := updateOKContext(ctx, t, e, prev, src, Batch{Paths: []string{"b.txt"}})

	must(t, os.WriteFile(logPath+".fail", nil, 0o644))

	if _, err := e.Update(ctx, res, src, Batch{Paths: []string{"b.txt"}}); err == nil {
		t.Fatal("Update with a failing git succeeded")
	}

	if _, ok := e.repos.get(dir); ok {
		t.Error("the failed update kept its repo")
	}

	res = updateOKContext(ctx, t, e, res, src, Batch{Paths: []string{"b.txt"}})
	if n := openCalls(t, logPath); n != 2 {
		t.Errorf("opens = %d, want 2: a failed update does not keep its repo", n)
	}

	sameAsFull(t, res, src)
}

// An update whose context ended says nothing about the repo: the drawer
// cancels its load whenever a newer one starts, and the next update reuses it.
func TestUpdateKeepsItsRepoWhenItsContextEnds(t *testing.T) {
	t.Parallel()

	dir := updateRepo(t)
	src := BranchSource{Worktree: dir, Base: "main"}
	e := testEngine(t)
	prev := diffOK(t, e, src)

	ctx, logPath := wrapGit(t, "")

	writeFile(t, dir, "b.txt", numbered("b.txt", 30)+"first\n")

	res := updateOKContext(ctx, t, e, prev, src, Batch{Paths: []string{"b.txt"}})

	ended, cancel := context.WithCancel(ctx)
	cancel()

	if _, err := e.Update(ended, res, src, Batch{Paths: []string{"b.txt"}}); err == nil {
		t.Fatal("Update with an ended context succeeded")
	}

	if _, ok := e.repos.get(dir); !ok {
		t.Error("the update whose context ended dropped its repo")
	}

	res = updateOKContext(ctx, t, e, res, src, Batch{Paths: []string{"b.txt"}})
	if n := openCalls(t, logPath); n != 1 {
		t.Errorf("opens = %d, want 1: the repo is reused after the context ended", n)
	}

	sameAsFull(t, res, src)
}

func TestUpdateRunsFromSeveralGoroutines(t *testing.T) {
	t.Parallel()

	var srcs []BranchSource

	prevs := map[string]Result{}
	e := testEngine(t)

	for range 2 {
		src := BranchSource{Worktree: updateRepo(t), Base: "main"}
		srcs = append(srcs, src)
		prevs[src.Worktree] = diffOK(t, e, src)
	}

	var wg sync.WaitGroup

	for i := range 4 {
		src := srcs[i%len(srcs)]

		wg.Go(func() {
			for range 2 {
				if _, err := e.Update(context.Background(), prevs[src.Worktree], src, Batch{Paths: []string{"b.txt"}}); err != nil {
					t.Errorf("Update: %v", err)

					return
				}
			}
		})
	}

	wg.Wait()
	assertEmptyDir(t, e.tempDir)
}
