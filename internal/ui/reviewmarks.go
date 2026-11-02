package ui

import (
	"context"
	"path/filepath"
	"slices"

	tea "charm.land/bubbletea/v2"

	"github.com/mhersson/aim/internal/core"
	"github.com/mhersson/aim/internal/git"
)

// markDay is the layout of the day a mark carries.
const markDay = "2006-01-02"

// loadedMarks is the marks file of one repository as the load job read it.
type loadedMarks struct {
	repo string
	days map[string]string
	err  error
}

// hunkIDs are the ids of every hunk of res, per file and hunk.
func hunkIDs(res git.Result) [][]string {
	out := make([][]string, len(res.Files))

	for i, f := range res.Files {
		out[i] = make([]string, len(f.Hunks))

		for j, h := range f.Hunks {
			out[i][j] = git.HunkID(f.Path, h)
		}
	}

	return out
}

// marked finds the hunk ids of the loaded result and its repository's marks. It
// reads the marks file only for a repository core holds none of.
func (j reviewJob) marked(ctx context.Context, msg *reviewLoadedMsg) {
	msg.ids = hunkIDs(msg.res)

	common := j.common
	if j.commonRoot != j.root || common == "" {
		r, err := j.git.Open(ctx, j.root)
		if err != nil {
			j.log.Debug("review: marks", "root", j.root, "err", err)

			return
		}

		if common, err = filepath.EvalSymlinks(r.CommonDir); err != nil {
			common = filepath.Clean(r.CommonDir)
		}
	}

	msg.common = common

	if _, ok := j.known[common]; ok {
		return
	}

	m := &loadedMarks{repo: common}

	if j.files != nil {
		m.days, m.err = j.files.Load(common, j.now)
	}

	msg.marks = m
}

// adoptMarks takes the ids and marks of a load: it records a repository's marks
// the first time they load, dates the marks the load shows today and saves what
// that changed.
func (a *App) adoptMarks(v *reviewView, msg reviewLoadedMsg) {
	v.ids, v.common = msg.ids, msg.common

	if msg.common == "" {
		v.marks, v.marksRev = nil, 0

		return
	}

	v.commonRoot = v.root

	if m := msg.marks; m != nil {
		if _, ok := a.store.State().Marks[m.repo]; !ok {
			if m.err != nil {
				a.log.Warn("reviewed marks: load", "repo", m.repo, "err", m.err)
			}

			// dispatch logs its own failures.
			_ = a.dispatch(core.SetMarks{Repo: m.repo, Days: m.days})
		}
	}

	// Only a load that shows a mark dated before today touches anything, so a
	// plain reload dispatches nothing.
	day, days := a.markToday(), a.store.State().Marks[msg.common].Days

	var stale []string

	for _, ids := range msg.ids {
		for _, id := range ids {
			if d, ok := days[id]; ok && d != day {
				stale = append(stale, id)
			}
		}
	}

	if len(stale) > 0 && a.dispatch(core.TouchMarks{Repo: msg.common, IDs: stale, Day: day}) == nil {
		a.saveMarks(msg.common)
	}

	v.syncMarks(a.store.State())
}

// markToday is today's UTC date, the day a mark is dated.
func (a *App) markToday() string { return a.clock.Now().UTC().Format(markDay) }

// saveMarks hands the repository's marks to the files.
func (a *App) saveMarks(common string) {
	if a.marks != nil {
		a.marks.Save(common, a.store.State().Marks[common].Days)
	}
}

// syncMarks takes the marks the rows are built from out of st.
func (v *reviewView) syncMarks(st core.State) {
	m := st.Marks[v.common]
	v.marks, v.marksRev = m.Days, m.Rev
}

// marksStale reports whether core's marks for the view's repository are not the
// ones its rows were built from.
func (v *reviewView) marksStale(st core.State) bool {
	return v.common != "" && st.Marks[v.common].Rev != v.marksRev
}

// rowOpts is how the view's rows are laid out.
func (v *reviewView) rowOpts(split bool) rowOpts {
	o := rowOpts{split: split, wrap: v.wrapW, full: v.full, whole: v.fullOn, folded: v.folded, md: v.mdRowsOf()}

	if v.common != "" {
		marks := v.marks
		o.ids, o.showRead, o.readOpen = v.ids, v.showRead, v.readOpen
		o.isRead = func(id string) bool {
			_, ok := marks[id]

			return ok
		}
	}

	return o
}

// hunksShown reports whether file f is laid out as hunks, the only way marks
// count: a file shown whole or rendered, a binary or too large one and one with
// no hunks show as they do without marks.
func (v *reviewView) hunksShown(f git.FileDiff) bool {
	_, have := v.full[f.Path]

	return !f.Binary && !f.Truncated && len(f.Hunks) > 0 && !v.mdOn[f.Path] && (!have || !v.fullOn[f.Path])
}

// hunkID is the id of hunk h of file f, "" when the view holds none.
func (v *reviewView) hunkID(f, h int) string {
	if f < 0 || f >= len(v.ids) || h < 0 || h >= len(v.ids[f]) {
		return ""
	}

	return v.ids[f][h]
}

// hunkRead reports whether hunk h of file f is reviewed.
func (v *reviewView) hunkRead(f, h int) bool {
	id := v.hunkID(f, h)
	if id == "" {
		return false
	}

	_, ok := v.marks[id]

	return ok
}

// fileRead reports whether file f is shown as hunks and every hunk is reviewed.
func (v *reviewView) fileRead(f int) bool {
	if f < 0 || f >= len(v.res.Files) || !v.hunksShown(v.res.Files[f]) {
		return false
	}

	for h := range v.res.Files[f].Hunks {
		if !v.hunkRead(f, h) {
			return false
		}
	}

	return true
}

// countHunks counts the hunks of the files shown as hunks and those read
// reports reviewed.
func (v *reviewView) countHunks(read func(file, hunk int) bool) (n, all int) {
	for fi, f := range v.res.Files {
		if !v.hunksShown(f) {
			continue
		}

		for hi := range f.Hunks {
			all++

			if read(fi, hi) {
				n++
			}
		}
	}

	return n, all
}

// markTarget is the file and the hunks the row at the cursor stands for, with
// ok false on a row marks do not apply to.
func (v *reviewView) markTarget() (file int, hunks []int, ok bool) {
	if v.common == "" || v.cursor < 0 || v.cursor >= len(v.rows) {
		return 0, nil, false
	}

	r := v.rows[v.cursor]
	if !v.hunksShown(v.res.Files[r.file]) {
		return 0, nil, false
	}

	switch r.kind {
	case rowFile:
		for hi := range v.res.Files[r.file].Hunks {
			hunks = append(hunks, hi)
		}
	case rowHunk, rowLine, rowPair:
		hunks = []int{r.hunk}
	default:
		return 0, nil, false
	}

	return r.file, hunks, true
}

// reviewMark marks the hunk at the cursor reviewed, or every hunk of the file on
// a file header; an already reviewed hunk, or a file with every hunk reviewed,
// is unmarked.
func (a *App) reviewMark() tea.Cmd {
	_, v, ok := a.reviewActive()
	if !ok {
		return nil
	}

	file, hunks, ok := v.markTarget()
	if !ok {
		return nil
	}

	onFile := v.rows[v.cursor].kind == rowFile
	mark := slices.ContainsFunc(hunks, func(h int) bool { return !v.hunkRead(file, h) })

	ids := make([]string, len(hunks))
	for i, h := range hunks {
		ids[i] = v.hunkID(file, h)
	}

	before := a.store.State().Marks[v.common].Rev

	if err := a.dispatch(core.MarkHunks{Repo: v.common, IDs: ids, Reviewed: mark, Day: a.markToday()}); err != nil {
		return nil
	}

	if a.store.State().Marks[v.common].Rev == before {
		return nil
	}

	a.saveMarks(v.common)

	v.syncMarks(a.store.State())

	next := v.nextUnread(file, hunks[len(hunks)-1])

	v.rebuild(v.rowsSplit)

	switch {
	case !mark && onFile:
		a.reviewGo(v, v.rowOfFileHeader(file))
	case !mark:
		a.reviewGo(v, v.rowOfHunkHeader(file, hunks[0]))
	case next != nil:
		if hdr := v.rowOfHunkHeader(next[0], next[1]); hdr >= 0 {
			a.reviewGo(v, v.firstChange(hdr))
		}
	case v.showRead && onFile:
		a.reviewGo(v, v.rowOfFileHeader(file))
	case v.showRead:
		a.reviewGo(v, v.rowOfHunkHeader(file, hunks[0]))
	default:
		a.reviewGo(v, 0)
	}

	return nil
}

// nextUnread is the first unread hunk after hunk h of file f in row order,
// wrapping to the first one, as {file, hunk}; nil when none is unread. It reads
// the marks as they are, so call it after the marking.
func (v *reviewView) nextUnread(f, h int) []int {
	var first, after []int

	for fi, file := range v.res.Files {
		if !v.hunksShown(file) {
			continue
		}

		for hi := range file.Hunks {
			if v.hunkRead(fi, hi) {
				continue
			}

			if first == nil {
				first = []int{fi, hi}
			}

			if after == nil && (fi > f || fi == f && hi > h) {
				after = []int{fi, hi}
			}
		}
	}

	if after != nil {
		return after
	}

	return first
}

// rowOfFileHeader is the row of file f's header, -1 for none.
func (v *reviewView) rowOfFileHeader(f int) int {
	return slices.IndexFunc(v.rows, func(r reviewRow) bool { return r.kind == rowFile && r.file == f })
}

// rowOfHunkHeader is the row of hunk h of file f's header, -1 for none.
func (v *reviewView) rowOfHunkHeader(f, h int) int {
	return slices.IndexFunc(v.rows, func(r reviewRow) bool { return r.kind == rowHunk && r.file == f && r.hunk == h })
}

// reviewShowRead switches between hiding reviewed hunks and showing them folded.
// It does nothing while the cursor is on a rendered file.
func (a *App) reviewShowRead() tea.Cmd {
	_, v, ok := a.reviewActive()
	if !ok {
		return nil
	}

	if v.cursor >= 0 && v.cursor < len(v.rows) && v.mdOn[v.res.Files[v.rows[v.cursor].file].Path] {
		return nil
	}

	v.showRead = !v.showRead
	v.rebuild(v.rowsSplit)

	return nil
}

// foldRead opens or folds the reviewed hunk under the cursor and reports whether
// there was one.
func (a *App) foldRead(v *reviewView, r reviewRow) bool {
	if r.kind != rowHunk && r.kind != rowLine && r.kind != rowPair || !v.showRead || !v.hunksShown(v.res.Files[r.file]) || !v.hunkRead(r.file, r.hunk) {
		return false
	}

	id := v.hunkID(r.file, r.hunk)

	if v.readOpen == nil {
		v.readOpen = map[string]bool{}
	}

	if v.readOpen[id] {
		delete(v.readOpen, id)
	} else {
		v.readOpen[id] = true
	}

	v.rebuild(v.rowsSplit)
	a.reviewGo(v, v.rowOfHunkHeader(r.file, r.hunk))

	return true
}
