package ui

import (
	"bytes"
	"encoding/json"
	"errors"
	"log/slog"
	"maps"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mhersson/aim/internal/git"
)

// fakeMarks keeps marks in memory by common dir and records the saves. Two
// apps that share one stand for a restart.
type fakeMarks struct {
	mu    sync.Mutex
	files map[string]map[string]string
	saves []fakeSave
	loads int
	err   error
}

type fakeSave struct {
	dir  string
	days map[string]string
}

func (f *fakeMarks) Load(dir string, _ time.Time) (map[string]string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	f.loads++

	if f.err != nil {
		return nil, f.err
	}

	return maps.Clone(f.files[dir]), nil
}

func (f *fakeMarks) Save(dir string, days map[string]string) {
	f.mu.Lock()
	defer f.mu.Unlock()

	if f.files == nil {
		f.files = map[string]map[string]string{}
	}

	f.files[dir] = maps.Clone(days)
	f.saves = append(f.saves, fakeSave{dir: dir, days: maps.Clone(days)})
}

func (f *fakeMarks) saved() int {
	f.mu.Lock()
	defer f.mu.Unlock()

	return len(f.saves)
}

func (f *fakeMarks) daysOf(dir string) map[string]string {
	f.mu.Lock()
	defer f.mu.Unlock()

	return maps.Clone(f.files[dir])
}

func (f *fakeMarks) set(dir, id, day string) {
	f.mu.Lock()
	defer f.mu.Unlock()

	if f.files == nil {
		f.files = map[string]map[string]string{}
	}

	if f.files[dir] == nil {
		f.files[dir] = map[string]string{}
	}

	f.files[dir][id] = day
}

// withMarks hands the app the fake as its marks files.
func withMarks(f *fakeMarks) func(*Options) { return func(o *Options) { o.Marks = f } }

// marksToday is the day the test clock is at.
const marksToday = "2026-01-01"

// marksRepo has three files of 60 lines, each with two edited hunks.
func marksRepo(t *testing.T) *fakeRepo {
	t.Helper()

	r := newFakeGit(t).repo(t, "")
	dir := r.Root()

	for _, f := range []string{"a.txt", "b.txt", "c.txt"} {
		write(t, filepath.Join(dir, f), numberedLines("line", 60))
	}

	r.commit("init")

	for _, f := range []string{"a.txt", "b.txt", "c.txt"} {
		write(t, filepath.Join(dir, f), editedLines(60, 5, 50))
	}

	return r
}

// commonOf is the key the marks of dir's repository are kept under.
func (v *reviewView) countRows(kind reviewRowKind) int {
	n := 0

	for _, r := range v.rows {
		if r.kind == kind {
			n++
		}
	}

	return n
}

// onFirstChange puts the cursor on the first changed line of hunk h of file f.
func (r *drawerRig) onFirstChange(t *testing.T, f, h int) {
	t.Helper()

	v := r.view()

	hdr := v.rowOfHunkHeader(f, h)
	if hdr < 0 {
		t.Fatalf("no row for hunk %d of file %d", h, f)
	}

	r.reviewGo(v, v.firstChange(hdr))
}

// atFirstChange reports whether the cursor is on the first changed line of hunk h of file f.
func atFirstChange(v *reviewView, f, h int) bool {
	hdr := v.rowOfHunkHeader(f, h)

	return hdr >= 0 && v.cursor == v.firstChange(hdr) && v.cursor != hdr
}

func newMarksRig(t *testing.T, fr *fakeRepo, fm *fakeMarks, opts ...func(*Options)) (*drawerRig, *reviewView) {
	t.Helper()

	r := newKeysRig(t, fr.Root(), append([]func(*Options){gitOf(fr.f), withMarks(fm)}, opts...)...)

	return r, r.opened(t)
}

func TestReviewMark(t *testing.T) {
	t.Parallel()

	fr := marksRepo(t)
	fm := &fakeMarks{}
	common := fr.Repo().CommonDir
	r, v := newMarksRig(t, fr, fm)

	if got := v.countRows(rowHunk); got != 6 {
		t.Fatalf("hunk rows = %d, want 6", got)
	}

	// space on a hunk line hides the hunk and moves on to the next unread one.
	id := git.HunkID("a.txt", v.res.Files[0].Hunks[0])

	r.onFirstChange(t, 0, 0)
	r.keys(keySpace)

	if got := v.countRows(rowHunk); got != 5 {
		t.Fatalf("hunk rows after space = %d, want 5", got)
	}

	if got := fm.daysOf(common); len(got) != 1 || got[id] != marksToday {
		t.Fatalf("saved marks = %v, want %s on %s", got, id, marksToday)
	}

	if !atFirstChange(v, 0, 1) {
		t.Errorf("cursor on row %d (%+v), want the first changed line of the next hunk", v.cursor, v.rows[v.cursor])
	}

	// The last unread hunk of a file leaves the header with the flag and no counts.
	r.keys(keySpace)

	if v.countRows(rowHunk) != 4 || !v.rows[v.rowOfFileHeader(0)].read {
		t.Fatalf("file 0 after both hunks: %d hunk rows, read %v", v.countRows(rowHunk), v.rows[v.rowOfFileHeader(0)].read)
	}

	if !atFirstChange(v, 1, 0) {
		t.Errorf("cursor on row %d, want the first changed line of the next file's first hunk", v.cursor)
	}

	head := strings.Split(r.drawn(t), "\n")

	var a string

	for _, l := range head {
		if strings.Contains(l, "a.txt") && strings.Contains(l, "reviewed") {
			a = l
		}
	}

	if a == "" || strings.Contains(a, "+1") || strings.Contains(a, "-1") {
		t.Errorf("header of a.txt = %q, want the reviewed flag and no counts", a)
	}

	// space on a file header marks both hunks, and again unmarks both.
	v.cursor = v.rowOfFileHeader(1)

	r.keys(keySpace)

	if got := len(fm.daysOf(common)); got != 4 {
		t.Fatalf("marks after the file header = %d, want 4", got)
	}

	if !v.rows[v.rowOfFileHeader(1)].read || v.rowOfHunkHeader(1, 0) >= 0 {
		t.Errorf("b.txt is not reviewed after space on its header")
	}

	if v.rows[v.cursor].kind == rowFile && v.rows[v.cursor].file == 1 {
		t.Errorf("cursor stayed on the marked file, want the next unread hunk")
	}

	v.cursor = v.rowOfFileHeader(1)

	r.keys(keySpace)

	if got := len(fm.daysOf(common)); got != 2 {
		t.Fatalf("marks after space again = %d, want 2", got)
	}

	if v.rows[v.rowOfFileHeader(1)].read || v.rowOfHunkHeader(1, 1) < 0 || v.cursor != v.rowOfFileHeader(1) {
		t.Errorf("b.txt after unmarking: read %v, cursor %d", v.rows[v.rowOfFileHeader(1)].read, v.cursor)
	}
}

func TestReviewShowRead(t *testing.T) {
	t.Parallel()

	fr := marksRepo(t)
	fm := &fakeMarks{}
	r, v := newMarksRig(t, fr, fm)

	r.onFirstChange(t, 0, 0)
	r.keys(keySpace)
	r.keys(keyRune('R'))

	hdr := v.rowOfHunkHeader(0, 0)
	if hdr < 0 || !v.rows[hdr].read {
		t.Fatalf("R did not show the reviewed hunk as a header")
	}

	if next := v.rows[hdr+1]; next.kind == rowLine || next.kind == rowPair {
		t.Errorf("the reviewed hunk shows its lines, want it folded")
	}

	if !strings.Contains(r.drawn(t), "✓ reviewed") {
		t.Errorf("the folded header lacks the flag:\n%s", r.drawn(t))
	}

	// enter shows the lines and enter folds them again.
	v.cursor = hdr

	r.keys(keyEnter)

	if next := v.rows[hdr+1]; next.kind != rowLine && next.kind != rowPair {
		t.Errorf("enter on the reviewed hunk shows no lines")
	}

	if !v.rows[v.rowOfHunkHeader(0, 0)].read {
		t.Errorf("the open hunk lost its flag")
	}

	v.cursor = v.rowOfHunkHeader(0, 0)

	r.keys(keyEnter)

	if next := v.rows[hdr+1]; next.kind == rowLine || next.kind == rowPair {
		t.Errorf("enter on the open hunk did not fold it")
	}

	// space unmarks it and the cursor stays on its header.
	v.cursor = hdr

	r.keys(keySpace)

	if v.rows[v.rowOfHunkHeader(0, 0)].read || v.cursor != v.rowOfHunkHeader(0, 0) {
		t.Errorf("after unmarking: read %v, cursor %d, header %d", v.rows[v.rowOfHunkHeader(0, 0)].read, v.cursor, v.rowOfHunkHeader(0, 0))
	}

	if common := fr.Repo().CommonDir; len(fm.daysOf(common)) != 0 {
		t.Errorf("marks after unmarking = %v, want none", fm.daysOf(common))
	}

	// ] lands on a folded header.
	r.onFirstChange(t, 0, 0)
	r.keys(keySpace)
	r.reviewGo(v, 0)
	r.keys(keyRune(']'))

	if want := v.rowOfHunkHeader(0, 0); v.cursor != want || !v.rows[want].read {
		t.Errorf("] landed on row %d, want the folded header %d", v.cursor, want)
	}

	// R again hides it.
	r.keys(keyRune('R'))

	if v.rowOfHunkHeader(0, 0) >= 0 || v.showRead {
		t.Errorf("R again left the reviewed hunk shown")
	}
}

func TestReviewAllRead(t *testing.T) {
	t.Parallel()

	t.Run("six hunks", func(t *testing.T) {
		t.Parallel()

		r, v := newMarksRig(t, marksRepo(t), &fakeMarks{})

		for f := range 3 {
			v.cursor = v.rowOfFileHeader(f)

			r.keys(keySpace)
		}

		if v.countRows(rowHunk) != 0 {
			t.Fatalf("hunk rows = %d, want none", v.countRows(rowHunk))
		}

		n := len(v.rows)
		if v.rows[n-2].kind != rowInfo || v.rows[n-2].text != "" || v.rows[n-1].kind != rowInfo {
			t.Fatalf("the rows end in %+v, want a blank row and the note", v.rows[n-2:])
		}

		if !strings.Contains(r.drawn(t), "all 6 hunks reviewed; R shows them") {
			t.Errorf("no note:\n%s", r.drawn(t))
		}

		r.keys(keyRune('R'))

		if v.countRows(rowHunk) != 6 || strings.Contains(r.drawn(t), "reviewed; R shows") {
			t.Errorf("R left %d hunk rows or the note", v.countRows(rowHunk))
		}
	})
}

func TestReviewMarksIgnored(t *testing.T) {
	t.Parallel()

	t.Run("full file", func(t *testing.T) {
		t.Parallel()

		fm := &fakeMarks{}
		r, v := newMarksRig(t, marksRepo(t), fm)

		r.onFirstChange(t, 0, 0)
		r.keys(keySpace)

		saved := fm.saved()

		v.cursor = v.rowOfFileHeader(0)

		r.keys(keyRune('f'))
		r.until(t, "the full file", func() bool { return v.countRows(rowFull) > 0 })

		var line int

		for i, row := range v.rows {
			if row.kind == rowFull && row.file == 0 {
				line = i

				break
			}
		}

		v.cursor = line

		r.keys(keySpace)

		if fm.saved() != saved || v.countRows(rowFull) != 60 {
			t.Errorf("space on a full file row changed something: %d saves (was %d), %d full rows", fm.saved(), saved, v.countRows(rowFull))
		}
	})

	t.Run("rendered file", func(t *testing.T) {
		t.Parallel()

		m := newMDRig(t)
		m.onFile(t, planPath)
		m.press(keyRune('m'))

		if kindsOf(m.v, planPath)[rowMD] == 0 {
			t.Fatal("the plan is not rendered")
		}

		before := m.store.State().Marks
		m.press(keySpace)
		m.press(keyRune('R'))

		if m.v.showRead || len(m.store.State().Marks) != len(before) {
			t.Errorf("space or R acted on a rendered file: showRead %v, marks %v", m.v.showRead, m.store.State().Marks)
		}

		for _, row := range m.v.rows {
			if row.read {
				t.Errorf("row %+v is read", row)
			}
		}
	})

	t.Run("binary", func(t *testing.T) {
		t.Parallel()

		fr := newFakeGit(t).repo(t, "")
		dir := fr.Root()
		write(t, filepath.Join(dir, "bin.dat"), "a\x00b\x00")
		write(t, filepath.Join(dir, "f.txt"), numberedLines("line", 60))
		fr.commit("init")
		write(t, filepath.Join(dir, "bin.dat"), "c\x00d\x00")
		write(t, filepath.Join(dir, "f.txt"), editedLines(60, 5))

		fm := &fakeMarks{}
		r, v := newMarksRig(t, fr, fm)

		r.onFirstChange(t, 1, 0)
		r.keys(keySpace)

		if v.countRows(rowHunk) != 0 {
			t.Fatalf("the one text hunk still shows")
		}

		// The binary file shows its note as before and counts for nothing: the
		// all-reviewed rows follow the last file.
		var info []string

		for _, row := range v.rows {
			if row.kind == rowInfo && row.file == 0 {
				info = append(info, row.text)
			}
		}

		if len(info) != 1 || info[0] != infoBinary {
			t.Errorf("binary file rows = %q", info)
		}

		saved := fm.saved()
		v.cursor = v.rowOfFileHeader(0) + 1

		r.keys(keySpace)

		if fm.saved() != saved {
			t.Errorf("space on a binary file's note saved marks")
		}
	})
}

func TestReviewMarksAcrossWorktrees(t *testing.T) {
	t.Parallel()

	fr := marksRepo(t)
	dir := fr.Root()
	fm := &fakeMarks{}
	common := fr.Repo().CommonDir
	r, v := newMarksRig(t, fr, fm)

	id := git.HunkID("a.txt", v.res.Files[0].Hunks[0])

	r.onFirstChange(t, 0, 0)
	r.keys(keySpace)

	// A sibling worktree with the same edits.
	sib := filepath.Join(filepath.Dir(dir), filepath.Base(dir)+"-sib")
	sr := fr.worktree(t, sib, "sib")

	for _, f := range []string{"a.txt", "b.txt", "c.txt"} {
		write(t, filepath.Join(sib, f), editedLines(60, 5, 50))
	}

	if got := sr.Repo().CommonDir; got != common {
		t.Fatalf("the sibling's common dir is %s, want %s", got, common)
	}

	_, sv := newMarksRig(t, sr, fm)

	if got := sv.countRows(rowHunk); got != 5 {
		t.Errorf("sibling worktree shows %d hunks, want 5", got)
	}

	// A restart: a new app over the same files.
	_, rv := newMarksRig(t, fr, fm)

	if got := rv.countRows(rowHunk); got != 5 {
		t.Errorf("after a restart the drawer shows %d hunks, want 5", got)
	}

	// Another repository with an identical hunk shows it.
	_, ov := newMarksRig(t, marksRepo(t), fm)

	if got := ov.countRows(rowHunk); got != 6 {
		t.Errorf("another repository shows %d hunks, want 6", got)
	}

	if _, ok := fm.daysOf(common)[id]; !ok {
		t.Errorf("the mark %s is gone", id)
	}
}

// A hunk marked in another worktree of the repository is also hidden in PR
// mode, where the main checkout's drawer shows the pull request's diff.
func TestReviewMarksInPRMode(t *testing.T) {
	t.Parallel()

	f := newFakeGit(t)
	rp := prFeatureRepo(t, f)
	fm := &fakeMarks{}
	r := newPRDrawerOn(t, f, rp, withMarks(fm))

	// The mark is for the pull request's a.txt hunk, which the branch drawer does not show.
	a := git.Hunk{Lines: []git.Line{{Kind: git.Del, Text: "a"}, {Kind: git.Add, Text: "a changed"}}}
	fm.set(rp.Repo().CommonDir, git.HunkID("a.txt", a), marksToday)

	r.poll()
	r.opened(t)
	r.typed("p")
	r.until(t, "PR mode", r.inPRMode(7))

	pv := r.view()

	marked, other := slices.Index(viewPaths(pv), "a.txt"), slices.Index(viewPaths(pv), "new.txt")
	if marked < 0 || other < 0 {
		t.Fatalf("PR mode shows %v, want a.txt and new.txt", viewPaths(pv))
	}

	if pv.rowOfHunkHeader(marked, 0) >= 0 || !pv.rows[pv.rowOfFileHeader(marked)].read {
		t.Errorf("a.txt's marked hunk shows in PR mode")
	}

	if pv.rowOfHunkHeader(other, 0) < 0 {
		t.Errorf("new.txt's unmarked hunk is hidden in PR mode")
	}
}

func TestReviewMarksLive(t *testing.T) {
	t.Parallel()

	fr := marksRepo(t)
	dir, root := fr.Root(), fr.Root()
	fm := &fakeMarks{}
	r, v := newMarksRig(t, fr, fm)

	// Review a.txt's first hunk and b.txt's second.
	r.onFirstChange(t, 0, 0)
	r.keys(keySpace)
	r.onFirstChange(t, 1, 1)
	r.keys(keySpace)

	if v.countRows(rowHunk) != 4 {
		t.Fatalf("hunk rows = %d, want 4", v.countRows(rowHunk))
	}

	// The agent edits an added line of a.txt's reviewed hunk.
	write(t, filepath.Join(dir, "a.txt"), strings.Replace(editedLines(60, 5, 50), "edited 5", "edited five", 1))

	r.run(r.batch(root, git.Batch{Paths: []string{"a.txt"}}))
	r.until(t, "the hunk to show again", func() bool { return v.countRows(rowHunk) == 5 })

	if v.rowOfHunkHeader(1, 0) < 0 || v.rowOfHunkHeader(1, 1) >= 0 {
		t.Errorf("the untouched reviewed hunk of b.txt did not stay hidden")
	}
}

func TestReviewMarksFirstFrame(t *testing.T) {
	t.Parallel()

	fr := marksRepo(t)
	fm := &fakeMarks{}
	r, _ := newMarksRig(t, fr, fm)

	r.onFirstChange(t, 0, 0)
	r.keys(keySpace)
	r.onFirstChange(t, 1, 0)
	r.keys(keySpace)

	// A new app: no frame may show a reviewed hunk, and the first rows are already filtered.
	r2 := newKeysRig(t, fr.Root(), gitOf(fr.f), withMarks(fm))
	r2.leader('d')

	first := -1

	r2.until(t, "the first rows", func() bool {
		v2 := r2.view()
		if v2 != nil && len(v2.rows) > 0 {
			first = v2.countRows(rowHunk)

			return true
		}

		return false
	})

	if first != 4 {
		t.Errorf("the first rows hold %d hunks, want 4", first)
	}
}

func TestReviewMarksTouch(t *testing.T) {
	t.Parallel()

	fr := marksRepo(t)
	fm := &fakeMarks{}
	common := fr.Repo().CommonDir
	r, v := newMarksRig(t, fr, fm)

	id := git.HunkID("a.txt", v.res.Files[0].Hunks[0])

	r.onFirstChange(t, 0, 0)
	r.keys(keySpace)

	// The mark is 30 days old when the next app loads.
	fm.set(common, id, "2025-12-02")

	r2, _ := newMarksRig(t, fr, fm)

	if got := fm.daysOf(common)[id]; got != marksToday {
		t.Fatalf("the mark is dated %q after a load that showed it, want %s", got, marksToday)
	}

	saved := fm.saved()

	r2.keys(keyRune('r'))
	r2.idle(t)
	r2.settle()

	if fm.saved() != saved {
		t.Errorf("a second load saved again (%d saves, was %d)", fm.saved(), saved)
	}
}

// lockedBuffer is a bytes.Buffer safe for a logger's goroutines.
type lockedBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (l *lockedBuffer) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()

	return l.b.Write(p)
}

func (l *lockedBuffer) String() string {
	l.mu.Lock()
	defer l.mu.Unlock()

	return l.b.String()
}

func TestReviewMarksBadFile(t *testing.T) {
	t.Parallel()

	logs := &lockedBuffer{}
	fm := &fakeMarks{err: errors.New("marks file unreadable")}
	fr := marksRepo(t)

	r, v := newMarksRig(t, fr, fm, func(o *Options) { o.Logger = slog.New(slog.NewJSONHandler(logs, nil)) })

	if v.countRows(rowHunk) != 6 {
		t.Fatalf("the drawer shows %d hunks, want 6", v.countRows(rowHunk))
	}

	r.keys(keyRune('r'))
	r.idle(t)
	r.keys(keyRune('r'))
	r.idle(t)

	common := fr.Repo().CommonDir
	warned := 0

	for line := range strings.SplitSeq(strings.TrimSpace(logs.String()), "\n") {
		var rec struct {
			Level string `json:"level"`
			Repo  string `json:"repo"`
		}
		if err := json.Unmarshal([]byte(line), &rec); err != nil {
			t.Fatalf("log line %q: %v", line, err)
		}

		if rec.Level == slog.LevelWarn.String() && rec.Repo == common {
			warned++
		}
	}

	if warned != 1 {
		t.Errorf("the marks error was logged %d times, want once:\n%s", warned, logs.String())
	}

	// The drawer keeps working: a mark still hides its hunk.
	r.onFirstChange(t, 0, 0)
	r.keys(keySpace)

	if v.countRows(rowHunk) != 5 {
		t.Errorf("space after a bad file left %d hunks, want 5", v.countRows(rowHunk))
	}
}

func TestCountHunks(t *testing.T) {
	t.Parallel()

	r, v := newMarksRig(t, marksRepo(t), &fakeMarks{})

	if read, all := v.countHunks(v.hunkRead); read != 0 || all != 6 {
		t.Fatalf("counts = %d of %d, want 0 of 6", read, all)
	}

	v.cursor = v.rowOfFileHeader(0)

	r.keys(keySpace)

	if read, all := v.countHunks(v.hunkRead); read != 2 || all != 6 {
		t.Errorf("counts after a file = %d of %d, want 2 of 6", read, all)
	}

	// A file shown whole counts for nothing.
	v.cursor = v.rowOfFileHeader(1)

	r.keys(keyRune('f'))
	r.until(t, "the full file", func() bool { return v.countRows(rowFull) > 0 })

	if read, all := v.countHunks(v.hunkRead); read != 2 || all != 4 {
		t.Errorf("counts with a file shown whole = %d of %d, want 2 of 4", read, all)
	}
}

// R stays on when the drawer starts over, as it does across reloads; the hunks
// unfolded under it belong to the old view and are dropped.
func TestReviewResetKeepsShowRead(t *testing.T) {
	t.Parallel()

	m := newPageRig(t, nil)
	m.v.showRead, m.v.readOpen = true, map[string]bool{"h": true}

	v := m.reviewReset(m.tabID(), m.v.pane)

	if !v.showRead {
		t.Error("R is off after the drawer started over")
	}

	if len(v.readOpen) != 0 {
		t.Errorf("readOpen = %v, want none", v.readOpen)
	}
}
