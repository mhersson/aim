package ui

import (
	"strconv"
	"strings"

	"github.com/mhersson/aim/internal/git"
	"github.com/mhersson/aim/internal/git/linediff"
	"github.com/mhersson/aim/internal/ui/diffview"
)

// reviewRowKind is what a row of the diff column is.
type reviewRowKind uint8

// Row kinds.
const (
	rowFile  reviewRowKind = iota // a file's header
	rowInfo                       // a note under a header; text holds it
	rowHunk                       // a hunk header
	rowLine                       // a line of a hunk, unified
	rowPair                       // a line pair of a hunk, side by side
	rowFull                       // a new-side line of the full file view
	rowGhost                      // a removed line in the full file view
	rowFold                       // a folded block of ghost lines
	rowMD                         // a line of a rendered markdown file
)

// reviewRow is one row of the diff column. It holds indexes into the result,
// -1 where the kind has none, and text only for info rows.
//
//   - rowLine: hunk and line are the hunk's and the line's indexes.
//   - rowPair: line is the left (old) side's index, else the right side's;
//     other is the right side's index, -1 when it has no line.
//   - rowFull: line indexes the new text; other indexes the old text for a kept
//     line and is -1 for an added one. text is the line.
//   - rowGhost: line indexes the old text; block is the ghost block. text is
//     the line.
//   - rowFold: line is the block's first old line, other the number of lines.
//   - rowMD: line indexes the layout; edit and other are the first and last
//     source line that made it; with none, edit is the editor's line (the next
//     block's first, else the previous block's last, else 1) and other is -1;
//     text is its plain text; mark is its change mark.
//
// On a rowFile, read says every hunk of the file is reviewed; on a rowHunk, that
// the hunk is. On the rowInfo that ends the rows once every hunk is reviewed,
// block holds the number of hunks.
//
// A ghost or fold row also holds edit, the line of the new file an editor
// opens at for it: the first new line after its block, or the last line when
// the block ends the file. The old numbers of removed lines mean nothing there.
type reviewRow struct {
	kind                    reviewRowKind
	file, hunk, line, other int
	text                    string
	block                   int
	edit                    int
	mark                    changeMark
	read                    bool
	// part is 0 on a line's first row and k on its k-th continuation row, which
	// copies the first row's kind and indexes.
	part int
}

// rowOpts selects how buildRows lays the result out.
type rowOpts struct {
	split bool
	// wrap holds the code columns that long lines wrap at.
	wrap codeWidths
	// full holds the text of the files read whole, by path; whole says which of
	// them are shown so.
	full  map[string]fullText
	whole map[string]bool
	// md holds the rendered files, by path; a nil entry is a file still parsing.
	md map[string]*mdRows
	// folded says whether a ghost block is folded, by foldKey; a block without
	// an entry takes ghostsStartFolded.
	folded            map[string]bool
	ghostsStartFolded bool
	// ids holds the hunk ids per file and hunk and isRead says whether one is
	// reviewed. A reviewed hunk is left out, or with showRead folded to its
	// header unless readOpen holds its id.
	ids      [][]string
	isRead   func(id string) bool
	showRead bool
	readOpen map[string]bool
}

// fullText is both sides of a file and the edit script from old to new, or the
// error that kept them from loading.
type fullText struct {
	old, new []string
	ops      []linediff.Op
	err      error
}

// newFullText holds both sides of a file with their edit script: the hunks'
// when they turn old into nw, so the full view marks what unified marks, else
// linediff.Diff's. Call it off the update loop: Diff can run long on a big
// file.
func newFullText(old, nw []string, hunks []git.Hunk) fullText {
	ops, ok := hunkOps(old, nw, hunks)
	if !ok {
		ops = linediff.Diff(old, nw)
	}

	return fullText{old: old, new: nw, ops: ops}
}

// hunkOps is the edit script from old to nw that the hunks give, the lines
// between them kept; false when the hunks do not turn old into nw.
func hunkOps(old, nw []string, hunks []git.Hunk) ([]linediff.Op, bool) {
	// A type change is two patches: one removes the old file, the next adds the new.
	if len(hunks) == 2 && hunks[0].NewStart == 0 && hunks[0].NewLines == 0 && hunks[1].OldStart == 0 && hunks[1].OldLines == 0 {
		gone, okGone := hunkOps(old, nil, hunks[:1])
		added, okAdded := hunkOps(nil, nw, hunks[1:])

		if okGone && okAdded {
			return append(gone, added...), true
		}
	}

	ops := make([]linediff.Op, 0, max(len(old), len(nw)))
	oi, ni := 0, 0

	keep := func(to int) bool {
		for ; oi < to; oi, ni = oi+1, ni+1 {
			if ni >= len(nw) || old[oi] != nw[ni] {
				return false
			}

			ops = append(ops, linediff.Op{Op: ' ', Line: nw[ni]})
		}

		return true
	}

	for _, h := range hunks {
		oldAt, newAt := hunkStart(h.OldStart, h.OldLines), hunkStart(h.NewStart, h.NewLines)
		if oldAt < oi || oldAt > len(old) || newAt-ni != oldAt-oi || !keep(oldAt) {
			return nil, false
		}

		for _, l := range h.Lines {
			oldOK := oi < len(old) && old[oi] == l.Text
			newOK := ni < len(nw) && nw[ni] == l.Text

			switch {
			case l.Kind == git.Context && oldOK && newOK:
				ops = append(ops, linediff.Op{Op: ' ', Line: l.Text})
				oi, ni = oi+1, ni+1
			case l.Kind == git.Del && oldOK:
				ops = append(ops, linediff.Op{Op: '-', Line: l.Text})
				oi++
			case l.Kind == git.Add && newOK:
				ops = append(ops, linediff.Op{Op: '+', Line: l.Text})
				ni++
			default:
				return nil, false
			}
		}
	}

	if len(old)-oi != len(nw)-ni || !keep(len(old)) {
		return nil, false
	}

	return ops, true
}

// hunkStart is the index of a hunk range's first line. A range of no lines
// names the line before it.
func hunkStart(start, lines int) int {
	if lines == 0 {
		return start
	}

	return start - 1
}

// foldKey names ghost block n of the file at path.
func foldKey(path string, n int) string { return path + "\x00" + strconv.Itoa(n) }

const (
	infoBinary    = "binary file, not shown"
	infoTruncated = "too large, press f for the full file"
	infoRendering = "rendering…"
	// infoAllRead marks the note that ends the rows once every hunk is reviewed;
	// the drawing words it with the bound key.
	infoAllRead = "all hunks reviewed"
)

func newRow(kind reviewRowKind, file int) reviewRow {
	return reviewRow{kind: kind, file: file, hunk: -1, line: -1, other: -1, block: -1, edit: -1}
}

func infoRow(file int, text string) reviewRow {
	r := newRow(rowInfo, file)
	r.text = text

	return r
}

// buildRows lays the files out as rows: a header per file, then its info,
// hunks or full text.
func buildRows(res git.Result, o rowOpts) []reviewRow {
	var rows []reviewRow

	unread, hunks := 0, 0

	for i, f := range res.Files {
		rows = append(rows, newRow(rowFile, i))
		fileRow := len(rows) - 1

		ft, have := o.full[f.Path]
		whole := have && o.whole[f.Path]
		mr, rendered := o.md[f.Path]

		switch {
		case f.Binary:
			rows = append(rows, infoRow(i, infoBinary))
		case rendered && have && ft.err != nil:
			rows = append(rows, infoRow(i, ft.err.Error()))
		case rendered && mr == nil:
			rows = append(rows, infoRow(i, infoRendering))
		case rendered:
			rows = appendMD(rows, i, mr)
		case whole && ft.err != nil:
			rows = append(rows, infoRow(i, ft.err.Error()))
		case whole:
			rows = appendFull(rows, i, f.Path, ft, o)
		case f.Truncated:
			rows = append(rows, infoRow(i, infoTruncated))
		case len(f.Hunks) == 0 && f.Status == git.ModeChanged:
			rows = append(rows, infoRow(i, "mode "+f.OldMode+" → "+f.NewMode))
		default:
			var u, n int

			rows, u, n = appendHunks(rows, i, f, o)
			unread, hunks = unread+u, hunks+n

			rows[fileRow].read = n > 0 && u == 0
		}
	}

	if hunks > 0 && unread == 0 && !o.showRead {
		last := len(res.Files) - 1
		note := infoRow(last, infoAllRead)
		note.block = hunks
		rows = append(rows, infoRow(last, ""), note)
	}

	return rows
}

// appendMD adds a rendered file's lines.
func appendMD(rows []reviewRow, file int, mr *mdRows) []reviewRow {
	plain := mr.lay.Plain()

	for i := range mr.lay.Lines {
		r := newRow(rowMD, file)
		r.line, r.text, r.edit = i, plain[i], blockEditLine(mr.lay.Source, i)

		if i < len(mr.lay.Source) && mr.lay.Source[i].First != 0 {
			r.other = mr.lay.Source[i].Last
		}

		if i < len(mr.marks) {
			r.mark = mr.marks[i]
		}

		rows = append(rows, r)
	}

	return rows
}

// appendHunks adds a file's hunks and reports how many are unread and how many
// there are.
func appendHunks(rows []reviewRow, file int, f git.FileDiff, o rowOpts) (_ []reviewRow, unread, hunks int) {
	for hi, h := range f.Hunks {
		hunks++

		read, id := o.reviewed(file, hi)
		if !read {
			unread++
		}

		if read && !o.showRead {
			continue
		}

		hr := newRow(rowHunk, file)
		hr.hunk, hr.read = hi, read
		rows = append(rows, hr)

		if read && !o.readOpen[id] {
			continue
		}

		if !o.split {
			for li := range h.Lines {
				r := newRow(rowLine, file)
				r.hunk, r.line = hi, li
				rows = withParts(rows, r, lineRowParts(h.Lines, li, o.wrap.line))
			}

			continue
		}

		kinds := make([]diffview.Kind, len(h.Lines))
		for li, l := range h.Lines {
			switch l.Kind {
			case git.Add:
				kinds[li] = diffview.Add
			case git.Del:
				kinds[li] = diffview.Delete
			}
		}

		for _, p := range diffview.Pairs(kinds) {
			r := newRow(rowPair, file)
			r.hunk, r.line, r.other = hi, p.Left, p.Right

			if p.Left < 0 {
				r.line, r.other = p.Right, -1
			}

			rows = withParts(rows, r, pairRowParts(h, r, o.wrap))
		}
	}

	return rows, unread, hunks
}

// reviewed reports whether hunk h of file f is reviewed, with its id.
func (o rowOpts) reviewed(f, h int) (bool, string) {
	if o.isRead == nil || f >= len(o.ids) || h >= len(o.ids[f]) {
		return false, ""
	}

	id := o.ids[f][h]

	return o.isRead(id), id
}

// appendFull adds a file's new lines in order, its removed lines as ghost
// blocks where its edit script puts them.
func appendFull(rows []reviewRow, file int, path string, ft fullText, o rowOpts) []reviewRow {
	old, nw, ops := ft.old, ft.new, ft.ops

	kept := func(oi, ni int) reviewRow {
		r := newRow(rowFull, file)
		r.line, r.other, r.text = ni, oi, nw[ni]

		return r
	}

	add := func(rows []reviewRow, r reviewRow) []reviewRow {
		return withParts(rows, r, len(lineParts(r.text, false, o.wrap.full)))
	}

	oi, ni, block := 0, 0, 0

	for i := 0; i < len(ops); {
		switch ops[i].Op {
		case ' ':
			rows = add(rows, kept(oi, ni))
			oi++
			ni++
			i++
		case '+':
			rows = add(rows, kept(-1, ni))
			ni++
			i++
		default:
			n := 0
			for i+n < len(ops) && ops[i+n].Op == '-' {
				n++
			}

			folded, ok := o.folded[foldKey(path, block)]
			if !ok {
				folded = o.ghostsStartFolded
			}

			// The block sits before new line ni; past the end it is the last line.
			edit := min(ni, len(nw)-1) + 1

			if folded {
				r := newRow(rowFold, file)
				r.line, r.other, r.block, r.edit = oi, n, block, edit
				rows = append(rows, r)
			} else {
				for k := range n {
					r := newRow(rowGhost, file)
					r.line, r.block, r.text, r.edit = oi+k, block, old[oi+k], edit
					rows = add(rows, r)
				}
			}

			oi += n
			i += n
			block++
		}
	}

	return rows
}

// lineStart is the first row of row i's line.
func (v *reviewView) lineStart(i int) int {
	return min(max(i-v.rows[i].part, 0), i)
}

// lineEnd is the row after the last row of row i's line.
func (v *reviewView) lineEnd(i int) int {
	i++
	for i < len(v.rows) && v.rows[i].part > 0 {
		i++
	}

	return i
}

// withParts appends r and its n-1 continuation rows.
func withParts(rows []reviewRow, r reviewRow, n int) []reviewRow {
	for k := range max(n, 1) {
		r.part = k
		rows = append(rows, r)
	}

	return rows
}

// lineRowParts is how many rows unified line li takes at a code width.
func lineRowParts(ls []git.Line, li, width int) int {
	if width <= 0 {
		return 1
	}

	other, p := "", partner(ls, li)
	if p >= 0 {
		other = ls[p].Text
	}

	return len(lineParts(ls[li].Text, tagsCR(ls[li].Text, other, p >= 0), width))
}

// pairRowParts is how many rows a side by side row takes: the taller side's.
func pairRowParts(h git.Hunk, r reviewRow, w codeWidths) int {
	if w.left <= 0 && w.right <= 0 {
		return 1
	}

	left, right := pairSides(h, r)
	paired := left >= 0 && right >= 0 && left != right
	n := 1

	sides := [2]int{left, right}
	widths := [2]int{w.left, w.right}

	for side, idx := range sides {
		if idx < 0 || widths[side] <= 0 {
			continue
		}

		o := ""
		if paired {
			o = h.Lines[sides[1-side]].Text
		}

		raw := h.Lines[idx].Text
		n = max(n, len(lineParts(raw, tagsCR(raw, o, paired), widths[side])))
	}

	return n
}

// hunkHeader is git's range line without the section, such as "@@ -43,9 +43,9 @@".
func hunkHeader(h git.Hunk) string {
	return "@@ -" + hunkRange(h.OldStart, h.OldLines) + " +" + hunkRange(h.NewStart, h.NewLines) + " @@"
}

func hunkRange(start, n int) string {
	if n == 1 {
		return strconv.Itoa(start)
	}

	return strconv.Itoa(start) + "," + strconv.Itoa(n)
}

// rowAnchor remembers what a row showed so that it can be found again in a
// later result. index and row are the fallbacks for a result that no longer
// holds it.
type rowAnchor struct {
	path                       string
	hunkNewStart, hunkNewLines int
	kind                       git.LineKind
	text                       string
	row                        reviewRowKind
	index                      int
	block                      int // a fold row's ghost block
	first, last                int // a rendered row's source range
	part                       int // a continuation row's place in its line
}

// anchorAt describes row i. Rows that hold no hunk keep only the file.
func anchorAt(res git.Result, rows []reviewRow, i int) rowAnchor {
	a := rowAnchor{index: i}
	if i < 0 || i >= len(rows) {
		return a
	}

	r := rows[i]
	a.row, a.part = r.kind, r.part
	a.path = res.Files[r.file].Path

	switch r.kind {
	case rowFold:
		a.block = r.block
	case rowMD:
		a.first, a.last, a.text = r.edit, r.other, r.text
	case rowFull:
		a.kind, a.text = git.Context, r.text
		if r.other < 0 {
			a.kind = git.Add
		}
	case rowGhost:
		a.kind, a.text = git.Del, r.text
	case rowHunk, rowLine, rowPair:
		h := res.Files[r.file].Hunks[r.hunk]
		a.hunkNewStart, a.hunkNewLines = h.NewStart, h.NewLines

		if r.kind != rowHunk {
			l := h.Lines[r.line]
			a.kind, a.text = l.Kind, l.Text
		}
	}

	return a
}

// restoreAnchor finds the row for a in rows, built from res. It keeps the
// line, else the hunk overlapping the old hunk's new-side range, else the
// file, else the nearest index. A line is kept in a hunk that overlaps the old
// range or has the old hunk's size, so a hunk that only moved still matches.
// A row of a wrapped line is found on the anchor's part, or the last one the
// line has.
func restoreAnchor(res git.Result, rows []reviewRow, a rowAnchor) int {
	if len(rows) == 0 {
		return 0
	}

	i := restoreLine(res, rows, a)
	i -= rows[i].part // the nearest-index fallback can land on a continuation row

	for k := 0; k < a.part && i+1 < len(rows) && rows[i+1].part == rows[i].part+1; k++ {
		i++
	}

	return i
}

func restoreLine(res git.Result, rows []reviewRow, a rowAnchor) int {
	if len(rows) == 0 {
		return 0
	}

	header := -1
	hunkRow := -1
	line, lineDist := -1, 0
	whole := -1
	fold := -1
	md := mdAnchorMatch{exact: -1, holds: -1, next: -1, last: -1}

	for i, r := range rows {
		f := res.Files[r.file]
		if f.Path != a.path || r.part > 0 {
			continue
		}

		switch r.kind {
		case rowFile:
			header = i
		case rowHunk:
			if hunkRow < 0 && overlaps(f.Hunks[r.hunk], a) {
				hunkRow = i
			}
		case rowMD:
			if a.row == rowMD {
				md.consider(r, i, a)
			}
		case rowFold:
			if a.row == rowFold && r.block == a.block && (fold < 0 || abs(i-a.index) < abs(fold-a.index)) {
				fold = i
			}
		case rowFull, rowGhost:
			if r.kind == a.row && r.text == a.text && (r.kind == rowGhost || (r.other < 0) == (a.kind == git.Add)) &&
				(whole < 0 || abs(i-a.index) < abs(whole-a.index)) {
				whole = i
			}
		case rowLine, rowPair:
			h := f.Hunks[r.hunk]
			if a.row == rowFile || a.row == rowHunk || (!overlaps(h, a) && h.NewLines != a.hunkNewLines) {
				continue
			}

			if !sameLine(h, r, a) {
				continue
			}

			d := abs(h.NewStart - a.hunkNewStart)
			if overlaps(h, a) {
				d = 0
			}

			if line < 0 || d < lineDist || (d == lineDist && abs(i-a.index) < abs(line-a.index)) {
				line, lineDist = i, d
			}
		}
	}

	switch {
	case a.row == rowFile && header >= 0:
		return header
	case a.row == rowMD && md.pick() >= 0:
		return md.pick()
	case a.row == rowFold && fold >= 0:
		return fold
	case (a.row == rowFull || a.row == rowGhost) && whole >= 0:
		return whole
	case (a.row == rowLine || a.row == rowPair) && line >= 0:
		return line
	case (a.row == rowLine || a.row == rowPair || a.row == rowHunk) && hunkRow >= 0:
		return hunkRow
	case header >= 0:
		return header
	}

	return min(max(a.index, 0), len(rows)-1)
}

// mdAnchorMatch finds a rendered row again by its source range: the row with
// the same range nearest the old index, else the narrowest range holding the
// first source line, else the next block, else the file's last row.
type mdAnchorMatch struct {
	exact, holds, next, last int // row indexes, -1 for none
	holdsW, nextFirst        int
}

func (m *mdAnchorMatch) consider(r reviewRow, i int, a rowAnchor) {
	m.last = i

	switch {
	case r.edit == a.first && r.other == a.last:
		if m.exact < 0 || abs(i-a.index) < abs(m.exact-a.index) {
			m.exact = i
		}
	case r.other <= 0 || a.last <= 0:
	case r.edit <= a.first && a.first <= r.other:
		if w := r.other - r.edit; m.holds < 0 || w < m.holdsW {
			m.holds, m.holdsW = i, w
		}
	case r.edit > a.first && (m.next < 0 || r.edit < m.nextFirst):
		m.next, m.nextFirst = i, r.edit
	}
}

func (m *mdAnchorMatch) pick() int {
	for _, i := range [...]int{m.exact, m.holds, m.next, m.last} {
		if i >= 0 {
			return i
		}
	}

	return -1
}

func overlaps(h git.Hunk, a rowAnchor) bool {
	return h.NewStart < a.hunkNewStart+max(a.hunkNewLines, 1) && a.hunkNewStart < h.NewStart+max(h.NewLines, 1)
}

func sameLine(h git.Hunk, r reviewRow, a rowAnchor) bool {
	for _, li := range []int{r.line, r.other} {
		if li >= 0 && h.Lines[li].Kind == a.kind && h.Lines[li].Text == a.text {
			return true
		}
	}

	return false
}

// searchRows returns the first row after from, or before it when dir is -1,
// whose text holds q, ignoring case, wrapping around and ending on from itself.
// A file row's text is its path, a hunk row's its header and section.
func searchRows(res git.Result, rows []reviewRow, from, dir int, q string) (int, bool) {
	if q == "" || len(rows) == 0 {
		return 0, false
	}

	q = strings.ToLower(q)
	n := len(rows)

	for k := 1; k <= n; k++ {
		i := ((from+dir*k)%n + n) % n

		if rows[i].part > 0 {
			continue
		}

		for _, s := range rowTexts(res, rows[i]) {
			if strings.Contains(strings.ToLower(s), q) {
				return i, true
			}
		}
	}

	return 0, false
}

// rowTexts is the text a row matches a search by; a continuation row has none,
// as its line matches on its first row.
func rowTexts(res git.Result, r reviewRow) []string {
	if r.part > 0 {
		return nil
	}

	f := res.Files[r.file]

	switch r.kind {
	case rowFile:
		return []string{f.Path, f.OldPath}
	case rowInfo, rowFull, rowGhost, rowMD:
		return []string{r.text}
	case rowHunk:
		h := f.Hunks[r.hunk]

		return []string{hunkHeader(h) + " " + h.Section}
	case rowLine:
		return []string{f.Hunks[r.hunk].Lines[r.line].Text}
	case rowPair:
		ls := f.Hunks[r.hunk].Lines
		out := []string{ls[r.line].Text}

		if r.other >= 0 && r.other != r.line {
			out = append(out, ls[r.other].Text)
		}

		return out
	}

	return nil
}

// freshRows marks the rows of next whose line is not among the lines prev held
// for the same file, matching by kind and text. A pair is fresh when either of
// its lines is. A file prev lacks is all fresh.
func freshRows(prev, next git.Result, rows []reviewRow) map[int]bool {
	type key struct {
		kind git.LineKind
		text string
	}

	had := make(map[string]map[key]bool, len(prev.Files))

	for _, f := range prev.Files {
		set := map[key]bool{}

		for _, h := range f.Hunks {
			for _, l := range h.Lines {
				set[key{l.Kind, l.Text}] = true
			}
		}

		had[f.Path] = set
	}

	out := map[int]bool{}

	for i, r := range rows {
		if r.kind != rowLine && r.kind != rowPair {
			continue
		}

		f := next.Files[r.file]
		ls := f.Hunks[r.hunk].Lines
		set := had[f.Path]

		for _, li := range []int{r.line, r.other} {
			if li >= 0 && !set[key{ls[li].Kind, ls[li].Text}] {
				out[i] = true
			}
		}
	}

	return out
}
