package ui

import (
	"context"
	"crypto/sha1" //nolint:gosec // git's own object ids
	"encoding/hex"
	"errors"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"testing"
	"unicode"

	"github.com/mhersson/aim/internal/git"
	"github.com/mhersson/aim/internal/git/linediff"
)

var _ DiffEngine = (*fakeEngine)(nil)

// fakeTree maps a path to its content.
type fakeTree map[string]string

func blobID(content string) string {
	h := sha1.New() //nolint:gosec // git's own object ids
	fmt.Fprintf(h, "blob %d\x00%s", len(content), content)

	return hex.EncodeToString(h.Sum(nil))
}

func treeID(t fakeTree) string {
	h := sha1.New() //nolint:gosec // git's own object ids

	for _, p := range slices.Sorted(maps.Keys(t)) {
		fmt.Fprintf(h, "%s\x00%s\n", p, blobID(t[p]))
	}

	return hex.EncodeToString(h.Sum(nil))
}

const noNewlineKey = "\x00\x00nonl"

// lineKeys splits text into lines. A last line without its newline gets a
// suffix in the key, so a file that only gains or loses its final newline
// differs, as it does in git.
func lineKeys(text string) (keys, raw []string) {
	raw = linediff.Split(text)
	keys = slices.Clone(raw)

	if text != "" && !strings.HasSuffix(text, "\n") {
		keys[len(keys)-1] += noNewlineKey
	}

	return keys, raw
}

func isBinary(s string) bool {
	return strings.IndexByte(s[:min(len(s), 8000)], 0) >= 0
}

type diffLine struct {
	op        byte
	text      string
	noNewline bool
	old, nw   int
}

// buildHunks is git diff --histogram -U3 --inter-hunk-context=0 over two texts.
func buildHunks(oldText, newText string) (hunks []git.Hunk, added, removed int) {
	ok, oraw := lineKeys(oldText)
	nk, _ := lineKeys(newText)
	ops := linediff.Diff(ok, nk)
	ls := make([]diffLine, len(ops))
	o, n := 0, 0

	for i, op := range ops {
		l := diffLine{op: op.Op, noNewline: strings.HasSuffix(op.Line, noNewlineKey), text: strings.TrimSuffix(op.Line, noNewlineKey)}

		switch op.Op {
		case '-':
			o++
			l.old = o
			removed++
		case '+':
			n++
			l.nw = n
			added++
		default:
			o++
			n++
			l.old, l.nw = o, n
		}

		ls[i] = l
	}

	for i := 0; i < len(ls); {
		for i < len(ls) && ls[i].op == ' ' {
			i++
		}

		if i == len(ls) {
			break
		}

		start, j := max(i-3, 0), i

		for {
			for j < len(ls) && ls[j].op != ' ' {
				j++
			}

			gap := 0
			for j+gap < len(ls) && ls[j+gap].op == ' ' {
				gap++
			}

			if j+gap == len(ls) || gap > 6 {
				break
			}

			j += gap
		}

		hunks = append(hunks, makeHunk(ls[start:min(j+3, len(ls))], ls[:start], oraw))
		i = j
	}

	return hunks, added, removed
}

func makeHunk(ls, before []diffLine, oraw []string) git.Hunk {
	var h git.Hunk

	for _, l := range before {
		if l.op != '+' {
			h.OldStart++
		}

		if l.op != '-' {
			h.NewStart++
		}
	}

	oldBefore, newBefore := h.OldStart, h.NewStart

	for _, l := range ls {
		line := git.Line{Kind: git.LineKind(l.op), Old: l.old, New: l.nw, Text: l.text, NoNewline: l.noNewline}
		if l.op != '+' {
			h.OldLines++
		}

		if l.op != '-' {
			h.NewLines++
		}

		h.Lines = append(h.Lines, line)
	}

	h.OldStart, h.NewStart = oldBefore+1, newBefore+1

	if h.OldLines == 0 {
		h.OldStart = oldBefore
	}

	if h.NewLines == 0 {
		h.NewStart = newBefore
	}

	h.Section = funcLine(oraw, oldBefore)

	return h
}

func funcStart(line string) bool {
	if line == "" {
		return false
	}

	c := line[0]

	return c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c == '_' || c == '$'
}

// funcLine is git's default funcname: the nearest line at or above the
// 1-based line from that starts with a letter, _ or $.
func funcLine(raw []string, from int) string {
	for i := min(from, len(raw)) - 1; i >= 0; i-- {
		c := raw[i]
		if !funcStart(c) {
			continue
		}

		s := c + "\n"
		if len(s) > 80 {
			s = s[:80]
		}

		return strings.TrimRightFunc(s, unicode.IsSpace)
	}

	return ""
}

// fakeEngine is the fake's DiffEngine.
type fakeEngine struct {
	f      *fakeGit
	limits git.Limits
}

func (f *fakeGit) NewEngine(o git.EngineOptions) DiffEngine {
	l, d := o.Limits, git.DefaultLimits()
	if l.Files <= 0 {
		l.Files = d.Files
	}

	if l.Bytes <= 0 {
		l.Bytes = d.Bytes
	}

	if l.Lines <= 0 {
		l.Lines = d.Lines
	}

	return &fakeEngine{f: f, limits: l}
}

func srcRoot(src git.Source) string {
	switch s := src.(type) {
	case git.BranchSource:
		return s.Worktree
	case git.TurnSource:
		return s.Worktree
	case git.PRSource:
		return s.Worktree
	}

	return ""
}

func srcArgs(src git.Source) []string {
	switch s := src.(type) {
	case git.BranchSource:
		return []string{"branch", s.Base}
	case git.TurnSource:
		return []string{"turn", s.Session, strconv.Itoa(s.Turn)}
	case git.PRSource:
		return []string{"pr", strconv.Itoa(s.Number)}
	}

	return nil
}

func (e *fakeEngine) Diff(ctx context.Context, src git.Source) (git.Result, error) {
	end, err := e.f.begin(ctx, "Diff", srcRoot(src), srcArgs(src)...)
	if err != nil {
		return git.Result{}, err
	}
	defer end()

	return e.compute(src)
}

func (e *fakeEngine) Update(ctx context.Context, _ git.Result, src git.Source, b git.Batch) (git.Result, error) {
	end, err := e.f.begin(ctx, "Update", srcRoot(src), append(srcArgs(src), b.Paths...)...)
	if err != nil {
		return git.Result{}, err
	}
	defer end()

	return e.compute(src)
}

func (e *fakeEngine) Stats(ctx context.Context, src git.Source) (git.Stats, error) {
	end, err := e.f.begin(ctx, "Stats", srcRoot(src), srcArgs(src)...)
	if err != nil {
		return git.Stats{}, err
	}
	defer end()

	res, err := e.compute(src)
	if err != nil {
		return git.Stats{}, err
	}

	s := git.Stats{Files: len(res.Files)}
	for _, f := range res.Files {
		s.Added += f.Added
		s.Removed += f.Removed
	}

	return s, nil
}

func (e *fakeEngine) FileText(ctx context.Context, worktree string, f git.FileDiff) (old, nw []string, err error) {
	end, err := e.f.begin(ctx, "FileText", worktree, f.Path)
	if err != nil {
		return nil, nil, err
	}
	defer end()

	if err := ctx.Err(); err != nil {
		return nil, nil, err
	}

	for _, side := range []struct {
		oid string
		out *[]string
	}{{f.OldOID, &old}, {f.NewOID, &nw}} {
		if side.oid == "" {
			continue
		}

		text, ok := e.f.blobs[side.oid]
		if !ok {
			return nil, nil, fmt.Errorf("read %s: blob %s not in the fake", f.Path, side.oid)
		}

		*side.out = linediff.Split(text)
	}

	return old, nw, nil
}

func (e *fakeEngine) TurnList(ctx context.Context, worktree, session string) (git.TurnList, error) {
	return e.turnList(ctx, "TurnList", worktree, session, "")
}

func (e *fakeEngine) TurnListAt(ctx context.Context, worktree, session, workingTree string) (git.TurnList, error) {
	return e.turnList(ctx, "TurnListAt", worktree, session, workingTree)
}

func (e *fakeEngine) turnList(ctx context.Context, op, worktree, session, known string) (git.TurnList, error) {
	args := []string{session}
	if known != "" {
		args = append(args, known)
	}

	end, err := e.f.begin(ctx, op, worktree, args...)
	if err != nil {
		return git.TurnList{}, err
	}
	defer end()

	r, err := e.f.openL(worktree)
	if err != nil {
		return git.TurnList{}, err
	}

	list, _ := e.f.turnListL(r, session, known)

	return list, nil
}

// turnListL is internal/git's turnList over the fake's turns; it also returns
// the working tree it read, which is the disk.
func (f *fakeGit) turnListL(r *fakeRepo, session, known string) (git.TurnList, fakeTree) {
	var (
		list git.TurnList
		disk fakeTree
	)

	for _, t := range f.turnsOfL(r, session) {
		switch {
		case t.EndCommit != "":
			if !t.Missing && !t.EndMissing && t.EndTree == t.Tree {
				continue
			}
		case t.Current && !t.Missing:
			tree := known

			if tree == "" {
				disk = f.readDiskL(r)
				tree = treeID(disk)
			}

			if tree == t.Tree {
				continue
			}

			list.Live = true
		}

		list.Turns = append(list.Turns, t)
	}

	return list, disk
}

func (e *fakeEngine) compute(src git.Source) (git.Result, error) {
	f := e.f

	if res, ok := f.results[src]; ok {
		return res, nil
	}

	switch s := src.(type) {
	case git.BranchSource:
		return e.diffBranch(s)
	case git.TurnSource:
		return e.diffTurn(s)
	case git.PRSource:
		return e.diffPR(s)
	}

	return git.Result{}, fmt.Errorf("diff: unsupported source %T", src)
}

func (e *fakeEngine) diffBranch(s git.BranchSource) (git.Result, error) {
	f := e.f

	r, err := f.openL(s.Worktree)
	if err != nil {
		return git.Result{}, err
	}

	base := s.Base
	if base == "" {
		if base, err = f.defaultBranchL(r); err != nil {
			return git.Result{}, err
		}
	}

	if strings.HasPrefix(base, "-") {
		return git.Result{}, fmt.Errorf("diff: base %q looks like an option", base)
	}

	tip, ok := r.c.resolve(base)
	if !ok {
		return git.Result{}, fmt.Errorf("diff: merge base of %s and HEAD: no such ref", base)
	}

	mb, ok := f.mergeBase(tip, r.headOID())
	if !ok {
		return git.Result{}, fmt.Errorf("diff: merge base of %s and HEAD: none", base)
	}

	disk := f.readDiskL(r)
	res := e.compare(f.commits[mb].tree, disk, f.headTree(r))
	res.Live = true

	return res, nil
}

func (e *fakeEngine) diffPR(s git.PRSource) (git.Result, error) {
	f := e.f

	r, err := f.openL(s.Worktree)
	if err != nil {
		return git.Result{}, err
	}

	head, hok := r.c.aimRefs[fmt.Sprintf("refs/aim/pr/%d/head", s.Number)]
	base, bok := r.c.aimRefs[fmt.Sprintf("refs/aim/pr/%d/base", s.Number)]

	if !hok || !bok {
		return git.Result{}, fmt.Errorf("diff pull request %d: %w", s.Number, git.ErrNotFetched)
	}

	mb, ok := f.mergeBase(base, head)
	if !ok {
		return git.Result{}, fmt.Errorf("diff pull request %d: merge base: none", s.Number)
	}

	res := e.compare(f.commits[mb].tree, f.commits[head].tree, nil)
	res.Head = head

	return res, nil
}

func (e *fakeEngine) diffTurn(s git.TurnSource) (git.Result, error) {
	f := e.f

	r, err := f.openL(s.Worktree)
	if err != nil {
		return git.Result{}, err
	}

	list, disk := f.turnListL(r, s.Session, "")
	if len(list.Turns) == 0 {
		return git.Result{}, fmt.Errorf("diff session %s: %w", s.Session, git.ErrNoTurns)
	}

	pos := s.Turn
	if pos == 0 {
		pos = len(list.Turns)
	}

	if pos < 1 || pos > len(list.Turns) {
		return git.Result{}, fmt.Errorf("diff turn %d: %w", pos, git.ErrNoSnapshot)
	}

	t := list.Turns[pos-1]
	ft := f.fakeTurnOf(r, s.Session, t.N)

	var res git.Result

	switch {
	case t.Missing, t.EndCommit != "" && t.EndMissing, t.EndCommit == "" && !t.Current:
		return git.Result{}, fmt.Errorf("diff turn %d: %w", pos, git.ErrNoSnapshot)
	case t.EndCommit != "":
		res = e.compare(ft.tree, ft.endTree, nil)
	default:
		if disk == nil {
			disk = f.readDiskL(r)
		}

		res = e.compare(ft.tree, disk, f.headTree(r))
		res.Live = true
	}

	res.Turn, res.Turns = pos, len(list.Turns)

	return res, nil
}

// compare diffs two trees. headTree, when set, marks the added files it does
// not hold as Untracked.
func (e *fakeEngine) compare(oldT, newT, headTree fakeTree) git.Result {
	res := git.Result{Old: treeID(oldT), New: treeID(newT)}

	paths := map[string]bool{}
	for p := range oldT {
		paths[p] = true
	}

	for p := range newT {
		paths[p] = true
	}

	for _, p := range slices.Sorted(maps.Keys(paths)) {
		ov, oHas := oldT[p]
		nv, nHas := newT[p]

		if oHas && nHas && ov == nv {
			continue
		}

		fd := git.FileDiff{Path: p}

		switch {
		case !oHas:
			fd.Status = git.Added
		case !nHas:
			fd.Status = git.Deleted
		default:
			fd.Status = git.Modified
		}

		if oHas {
			fd.OldOID, fd.OldMode = blobID(ov), "100644"
			e.f.blobs[fd.OldOID] = ov
		}

		if nHas {
			fd.NewOID, fd.NewMode = blobID(nv), "100644"
			e.f.blobs[fd.NewOID] = nv
		}

		if headTree != nil && fd.Status == git.Added {
			_, held := headTree[p]
			fd.Untracked = !held
		}

		res.Files = append(res.Files, fd)
	}

	for i := range res.Files {
		fd := &res.Files[i]
		ov, nv := oldT[fd.Path], newT[fd.Path]

		if isBinary(ov) || isBinary(nv) {
			fd.Binary = true

			continue
		}

		hunks, added, removed := buildHunks(ov, nv)
		fd.Added, fd.Removed = added, removed

		switch {
		case added+removed == 0:
		case i >= e.limits.Files || added+removed > e.limits.Lines:
			fd.Truncated = true
			res.Truncated = true
		default:
			fd.Hunks = hunks
		}
	}

	return res
}

// hunkCtx are the unchanged lines "<prefix> from" to "<prefix> to" as hunk specs.
func hunkCtx(prefix string, from, to int) []string {
	var out []string

	for i := from; i <= to; i++ {
		out = append(out, fmt.Sprintf(" %s %d", prefix, i))
	}

	return out
}

// hunkEdited is numberedLines("line", n) with the lines at the given numbers
// written as "LINE <k>".
func hunkEdited(n int, at ...int) string {
	lines := strings.SplitAfter(numberedLines("line", n), "\n")
	for _, k := range at {
		lines[k-1] = fmt.Sprintf("LINE %d\n", k)
	}

	return strings.Join(lines, "")
}

// hunkChange is a deleted "line k" and its replacement as hunk specs.
func hunkChange(k int) []string {
	return []string{fmt.Sprintf("-line %d", k), fmt.Sprintf("+LINE %d", k)}
}

func hunkJoin(parts ...[]string) []string {
	var out []string
	for _, p := range parts {
		out = append(out, p...)
	}

	return out
}

func TestFakeHunks(t *testing.T) {
	t.Parallel()

	long := "func " + strings.Repeat("a", 70) + strings.Repeat(" ", 25)
	body := func(changed int) string {
		var b strings.Builder

		b.WriteString(long + "\n")

		for i := 1; i <= 8; i++ {
			if i == changed {
				b.WriteString("  BODY\n")

				continue
			}

			fmt.Fprintf(&b, "  body %d\n", i)
		}

		return b.String()
	}

	noNL := hunkSpec(1, 1, "", " a", "-b", "+b")
	noNL.Lines[2].NoNewline = true

	tests := []struct {
		name      string
		old, new  string
		noOld     bool // the file is not in the commit
		noNew     bool // the file is gone from the disk
		status    git.Status
		untracked bool
		binary    bool
		hunks     []git.Hunk
	}{
		{
			name: "one line changed", old: numberedLines("line", 60), new: hunkEdited(60, 5), status: git.Modified,
			hunks: []git.Hunk{hunkSpec(2, 2, "line 1", hunkJoin(hunkCtx("line", 2, 4), hunkChange(5), hunkCtx("line", 6, 8))...)},
		},
		{
			name: "two far changes make two hunks", old: numberedLines("line", 60), new: hunkEdited(60, 5, 50), status: git.Modified,
			hunks: []git.Hunk{
				hunkSpec(2, 2, "line 1", hunkJoin(hunkCtx("line", 2, 4), hunkChange(5), hunkCtx("line", 6, 8))...),
				hunkSpec(47, 47, "line 46", hunkJoin(hunkCtx("line", 47, 49), hunkChange(50), hunkCtx("line", 51, 53))...),
			},
		},
		{
			name: "six lines between share a hunk", old: numberedLines("line", 60), new: hunkEdited(60, 5, 12), status: git.Modified,
			hunks: []git.Hunk{hunkSpec(2, 2, "line 1", hunkJoin(
				hunkCtx("line", 2, 4), hunkChange(5), hunkCtx("line", 6, 11), hunkChange(12), hunkCtx("line", 13, 15))...)},
		},
		{
			name: "seven lines between split", old: numberedLines("line", 60), new: hunkEdited(60, 5, 13), status: git.Modified,
			hunks: []git.Hunk{
				hunkSpec(2, 2, "line 1", hunkJoin(hunkCtx("line", 2, 4), hunkChange(5), hunkCtx("line", 6, 8))...),
				hunkSpec(10, 10, "line 9", hunkJoin(hunkCtx("line", 10, 12), hunkChange(13), hunkCtx("line", 14, 16))...),
			},
		},
		{
			name: "new file", new: "a\nb\n", noOld: true, status: git.Added, untracked: true,
			hunks: []git.Hunk{hunkSpec(0, 1, "", "+a", "+b")},
		},
		{
			name: "deleted file", old: "a\nb\n", noNew: true, status: git.Deleted,
			hunks: []git.Hunk{hunkSpec(1, 0, "", "-a", "-b")},
		},
		{
			name: "last line without a newline on the new side", old: "a\nb\n", new: "a\nb", status: git.Modified,
			hunks: []git.Hunk{noNL},
		},
		{
			name: "hunk at the top has no section", old: numberedLines("line", 10), new: hunkEdited(10, 1), status: git.Modified,
			hunks: []git.Hunk{hunkSpec(1, 1, "", hunkJoin(hunkChange(1), hunkCtx("line", 2, 4))...)},
		},
		{
			name: "long section is cut and stripped", old: body(0), new: body(5), status: git.Modified,
			hunks: []git.Hunk{hunkSpec(3, 3, "func "+strings.Repeat("a", 70), hunkJoin(
				[]string{"   body 2", "   body 3", "   body 4", "-  body 5", "+  BODY", "   body 6", "   body 7", "   body 8"})...)},
		},
		{name: "a NUL byte makes it binary", old: "a\n", new: "a\x00b\n", status: git.Modified, binary: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			f := newFakeGit(t)
			r := f.repo(t, "")
			file := filepath.Join(r.Root(), "f.txt")

			write(t, filepath.Join(r.Root(), "keep.txt"), "keep\n")

			if !tt.noOld {
				write(t, file, tt.old)
			}

			r.commit("init")

			if tt.noNew {
				if err := os.Remove(file); err != nil {
					t.Fatal(err)
				}
			} else {
				write(t, file, tt.new)
			}

			res, err := f.NewEngine(git.EngineOptions{}).Diff(context.Background(), git.BranchSource{Worktree: r.Root()})
			if err != nil {
				t.Fatalf("Diff: %v", err)
			}

			if len(res.Files) != 1 || res.Files[0].Path != "f.txt" {
				t.Fatalf("files = %+v, want only f.txt", res.Files)
			}

			got := res.Files[0]
			if got.Status != tt.status || got.Untracked != tt.untracked || got.Binary != tt.binary || !res.Live {
				t.Errorf("status %q untracked %v binary %v live %v, want %q %v %v true",
					got.Status, got.Untracked, got.Binary, res.Live, tt.status, tt.untracked, tt.binary)
			}

			if !reflect.DeepEqual(got.Hunks, tt.hunks) {
				t.Errorf("hunks = %+v\nwant     %+v", got.Hunks, tt.hunks)
			}
		})
	}
}

func TestFakeTurnList(t *testing.T) {
	t.Parallel()

	const sess = "11111111-1111-4111-8111-111111111111"

	tests := []struct {
		name     string
		setup    func(t *testing.T, r *fakeRepo, file string)
		wantTurn []int // positions kept, as turn numbers
		wantLive bool
		diffPos  int   // the position Diff is asked for; 0 the latest
		diffErr  error // nil: Diff succeeds
		diffLive bool
	}{
		{
			name: "closed turn with equal trees is left out",
			setup: func(t *testing.T, r *fakeRepo, file string) {
				write(t, file, "a\n")
				r.turn(sess)
				r.turn(sess)
			},
			diffErr: git.ErrNoTurns,
		},
		{
			name: "closed turn that changed the tree is kept",
			setup: func(t *testing.T, r *fakeRepo, file string) {
				write(t, file, "a\n")
				r.turn(sess)
				write(t, file, "b\n")
				r.turn(sess)
			},
			wantTurn: []int{1},
		},
		{
			name: "a marker is kept",
			setup: func(t *testing.T, r *fakeRepo, file string) {
				write(t, file, "a\n")
				r.missingTurn(sess)
			},
			wantTurn: []int{1},
			diffErr:  git.ErrNoSnapshot,
		},
		{
			name: "open current turn is kept while the disk differs",
			setup: func(t *testing.T, r *fakeRepo, file string) {
				write(t, file, "a\n")
				r.turn(sess)
				write(t, file, "b\n")
			},
			wantTurn: []int{1}, wantLive: true, diffLive: true,
		},
		{
			name: "open current turn is left out once the disk matches",
			setup: func(t *testing.T, r *fakeRepo, file string) {
				write(t, file, "a\n")
				r.turn(sess)
				write(t, file, "b\n")
				write(t, file, "a\n")
			},
			diffErr: git.ErrNoTurns,
		},
		{
			name: "a position past the list has no snapshot",
			setup: func(t *testing.T, r *fakeRepo, file string) {
				write(t, file, "a\n")
				r.turn(sess)
				write(t, file, "b\n")
			},
			wantTurn: []int{1}, wantLive: true, diffPos: 5, diffErr: git.ErrNoSnapshot,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			f := newFakeGit(t)
			r := f.repo(t, "")
			file := filepath.Join(r.Root(), "f.txt")

			write(t, file, "base\n")
			r.commit("init")
			tt.setup(t, r, file)

			eng := f.NewEngine(git.EngineOptions{})
			ctx := context.Background()

			list, err := eng.TurnList(ctx, r.Root(), sess)
			if err != nil {
				t.Fatalf("TurnList: %v", err)
			}

			var got []int
			for _, turn := range list.Turns {
				got = append(got, turn.N)
			}

			if !slices.Equal(got, tt.wantTurn) || list.Live != tt.wantLive {
				t.Errorf("turns %v live %v, want %v %v", got, list.Live, tt.wantTurn, tt.wantLive)
			}

			res, err := eng.Diff(ctx, git.TurnSource{Worktree: r.Root(), Session: sess, Turn: tt.diffPos})
			if !errors.Is(err, tt.diffErr) {
				t.Fatalf("Diff error = %v, want %v", err, tt.diffErr)
			}

			if err == nil && (res.Live != tt.diffLive || res.Turns != len(got) || len(res.Files) != 1) {
				t.Errorf("Diff live %v turns %d files %d, want %v %d 1", res.Live, res.Turns, len(res.Files), tt.diffLive, len(got))
			}
		})
	}
}
