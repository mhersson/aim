package git

import (
	"context"
	"fmt"
	"os"
	"path"
	"slices"
	"strings"
	"sync"
)

// maxIncrementalPaths is the most touched paths Update re-lists; a bigger
// batch is cheaper to recompute whole.
const maxIncrementalPaths = 200

// repoCache remembers the Repo that Open found for a worktree path. A Repo is
// only paths, so a hit hands out a copy; two misses that race both Open and the
// later store wins.
type repoCache struct {
	mu sync.Mutex
	m  map[string]Repo
}

// get returns the Repo cached for dir while its git dirs are still there. A
// worktree whose git dir moved or went away must be opened again.
func (c *repoCache) get(dir string) (Repo, bool) {
	c.mu.Lock()
	r, ok := c.m[dir]
	c.mu.Unlock()

	if ok && !gitDirsExist(r) {
		c.forget(dir)

		return Repo{}, false
	}

	return r, ok
}

func (c *repoCache) put(dir string, r Repo) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.m == nil {
		c.m = make(map[string]Repo)
	}

	c.m[dir] = r
}

func (c *repoCache) forget(dir string) {
	c.mu.Lock()
	defer c.mu.Unlock()

	delete(c.m, dir)
}

func gitDirsExist(r Repo) bool {
	for _, d := range []string{r.GitDir, r.CommonDir} {
		if info, err := os.Stat(d); err != nil || !info.IsDir() {
			return false
		}
	}

	return true
}

// Update brings prev, the result of Diff for src, up to date after the watcher
// batch b. It rebuilds the working tree and re-lists only the touched paths;
// the result equals a fresh Diff. Anything that could change which files pair
// up, the old side, or which files fall inside the caps runs a full Diff. The
// Repo opened for a worktree is reused until its git dirs move or an update
// fails; an update whose context ended keeps it.
func (e *Engine) Update(ctx context.Context, prev Result, src Source, b Batch) (Result, error) {
	if !e.canUpdate(prev, src, b) {
		return e.Diff(ctx, src)
	}

	dir := worktreeOf(src)

	r, cached := e.repos.get(dir)
	if !cached {
		var err error

		if r, err = Open(ctx, dir); err != nil {
			return Result{}, err
		}
	}

	res, err := e.update(ctx, r, prev, src, b)
	if err != nil {
		// The repo may be what failed (moved, replaced); open it again next time.
		// A context that ended proves nothing about it.
		if ctx.Err() == nil {
			e.repos.forget(dir)
		}

		return Result{}, err
	}

	if !cached {
		e.repos.put(dir, r)
	}

	return res, nil
}

// update is Update on the opened repo r.
func (e *Engine) update(ctx context.Context, r Repo, prev Result, src Source, b Batch) (Result, error) {
	var (
		wt    WorkTree
		count int
	)

	if ts, ok := src.(TurnSource); ok {
		list, w, err := e.turnList(ctx, r, ts.Session, "")
		if err != nil {
			return Result{}, err
		}

		last := len(list.Turns)
		if !list.Live || w == nil || prev.Turn != last || !stillLive(list.Turns[last-1], prev.Old) {
			return e.Diff(ctx, src)
		}

		wt, count = *w, last
	} else {
		var err error
		if wt, err = e.workingTree(ctx, r); err != nil {
			return Result{}, fmt.Errorf("update: %w", err)
		}
	}

	touched, err := listFiles(ctx, r, prev.Old, wt.Tree, b.Paths...)
	if err != nil {
		return Result{}, err
	}

	retained := retainedFiles(prev.Files, b.Paths)

	if pairingMayChange(touched, retained) {
		return e.Diff(ctx, src)
	}

	markUntracked(touched, wt.Untracked)

	// A scratch cache keeps hunks of a result that falls back out of the real
	// one: cached hunks skip the byte budget, which would skew the full Diff.
	scratch := &Engine{limits: e.limits, maxText: e.maxText, tempDir: e.tempDir, indexCache: e.indexCache, cache: newHunkCache(cacheBytes)}
	if err := scratch.addHunks(ctx, r, prev.Old, wt.Tree, touched); err != nil {
		return Result{}, err
	}

	files := mergeFiles(retained, touched)
	if !e.withinCaps(files) {
		return e.Diff(ctx, src)
	}

	for _, f := range touched {
		if len(f.Hunks) > 0 {
			e.cache.put(hunkKey{f.OldOID, f.NewOID}, f.Hunks)
		}
	}

	res := Result{Old: prev.Old, New: wt.Tree, Live: true, Turn: prev.Turn, Turns: count, Files: files}
	for _, f := range files {
		res.Truncated = res.Truncated || f.Truncated
	}

	return res, nil
}

// stillLive reports whether t is the open current turn that started at tree.
func stillLive(t Turn, tree string) bool {
	return !t.Missing && t.EndCommit == "" && t.Current && t.Tree == tree
}

// worktreeOf returns the worktree of a branch or turn source.
func worktreeOf(src Source) string {
	switch s := src.(type) {
	case BranchSource:
		return s.Worktree
	case TurnSource:
		return s.Worktree
	}

	return ""
}

// canUpdate reports whether prev and the batch allow an incremental update.
func (e *Engine) canUpdate(prev Result, src Source, b Batch) bool {
	switch s := src.(type) {
	case BranchSource:
	case TurnSource:
		if s.Turn != 0 && s.Turn != prev.Turn {
			return false
		}
	default:
		return false
	}

	// A .gitignore change can add or drop untracked files nobody touched.
	if !prev.Live || b.Git || b.Overflow || len(b.Paths) > maxIncrementalPaths ||
		slices.ContainsFunc(b.Paths, func(p string) bool { return path.Base(p) == ".gitignore" }) {
		return false
	}

	for _, f := range prev.Files {
		if f.OldPath != "" || f.Truncated {
			return false
		}
	}

	return true
}

// retainedFiles returns prev's files that lie outside every touched path. A
// touched path stands for everything under it.
func retainedFiles(files []FileDiff, touched []string) []FileDiff {
	var out []FileDiff

	for _, f := range files {
		if !underAny(f.Path, touched) {
			out = append(out, f)
		}
	}

	return out
}

func underAny(p string, roots []string) bool {
	for _, root := range roots {
		if p == root || strings.HasPrefix(p, root+"/") {
			return true
		}
	}

	return false
}

// pairingMayChange reports whether a full listing could pair files differently
// than the restricted one: a rename or copy among the touched files, or an
// added file on one side next to any other changed file on the other.
func pairingMayChange(touched, retained []FileDiff) bool {
	var touchedAdded, touchedOther, retainedAdded, retainedOther bool

	for _, f := range touched {
		switch f.Status {
		case Renamed, Copied:
			return true
		case Added:
			touchedAdded = true
		default:
			touchedOther = true
		}
	}

	for _, f := range retained {
		if f.Status == Added {
			retainedAdded = true
		} else {
			retainedOther = true
		}
	}

	return (touchedAdded && retainedOther) || (retainedAdded && touchedOther)
}

func mergeFiles(retained, touched []FileDiff) []FileDiff {
	files := make([]FileDiff, 0, len(retained)+len(touched))
	files = append(files, retained...)
	files = append(files, touched...)
	slices.SortStableFunc(files, func(a, b FileDiff) int { return strings.Compare(a.Path, b.Path) })

	if len(files) == 0 {
		return nil
	}

	return files
}

// withinCaps reports whether a full Diff would give every file its hunks, so
// the merged files need no cap bookkeeping: no file past the file cap, none cut
// by the byte cap, and a diff text that stays under it, estimated from above.
func (e *Engine) withinCaps(files []FileDiff) bool {
	if len(files) > e.limits.Files {
		return false
	}

	// Per-file header, per-hunk header and per-line framing, each rounded up so
	// the sum bounds what git prints.
	size := 0

	for _, f := range files {
		// Past the line cap is a per-file rule; any other cut came from the byte cap.
		if f.Truncated && f.Added+f.Removed <= e.limits.Lines {
			return false
		}

		if len(f.Hunks) == 0 {
			continue
		}

		size += 512 + 4*(len(f.Path)+len(f.OldPath))

		for _, h := range f.Hunks {
			size += 64 + len(h.Section)

			for _, l := range h.Lines {
				size += len(l.Text) + 2
				if l.NoNewline {
					size += 32
				}
			}
		}
	}

	return size <= e.limits.Bytes
}
