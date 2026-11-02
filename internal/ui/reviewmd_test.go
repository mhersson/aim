package ui

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/mhersson/aim/internal/core"
	"github.com/mhersson/aim/internal/git"
)

const planPath = "docs/plan.md"

// planBase is docs/plan.md as committed. Source line 12 is "Remember the
// milestone." and lines 14 to 19 are a fold.
var planBase = []string{
	"# Plan",
	"",
	"Intro paragraph here.",
	"",
	"## Steps",
	"",
	"- first step",
	"- second step",
	"- third step",
	"",
	"## Notes",
	"Remember the milestone.",
	"",
	"<details>",
	"<summary>More</summary>",
	"",
	"Hidden detail text.",
	"",
	"</details>",
}

// planWith is planBase with the lines named by number replaced.
func planWith(edits map[int]string) string {
	out := slices.Clone(planBase)
	for n, s := range edits {
		out[n-1] = s
	}

	return strings.Join(out, "\n") + "\n"
}

// planEdited is the working tree's plan: two blocks changed.
func planEdited() string {
	return planWith(map[int]string{3: "Intro paragraph edited.", 8: "- 2nd step"})
}

// planLive is what an agent rewrites the plan to under the rendered view.
func planLive() string { return planWith(map[int]string{9: "- 3rd step"}) }

// mdRig is a drawer on a branch with a changed markdown file, a changed Go
// file, a binary markdown file and a deleted one.
type mdRig struct {
	*drawerRig
	v    *reviewView
	dir  string
	root string
	st   *starts
}

func newMDRig(t *testing.T) *mdRig {
	t.Helper()

	return newMDRigOf(t, planWith(nil), planEdited())
}

// newMDRigOf is newMDRig with the plan's committed and working text given.
func newMDRigOf(t *testing.T, committed, edited string) *mdRig {
	t.Helper()

	f := newFakeGit(t)
	fr := f.repo(t, "")
	dir := fr.Root()
	write(t, filepath.Join(dir, planPath), committed)
	write(t, filepath.Join(dir, "main.go"), numberedLines("line", 40))
	write(t, filepath.Join(dir, "bin.md"), "a\x00b\x00")
	write(t, filepath.Join(dir, "gone.md"), "gone content\n")
	fr.commit("init")
	write(t, filepath.Join(dir, planPath), edited)
	write(t, filepath.Join(dir, "main.go"), editedLines(40, 3, 30))
	write(t, filepath.Join(dir, "bin.md"), "c\x00d\x00")

	if err := os.Remove(filepath.Join(dir, "gone.md")); err != nil {
		t.Fatal(err)
	}

	r := newKeysRig(t, dir, gitOf(f), func(o *Options) {
		o.Getenv = func(k string) string { return map[string]string{"SHELL": "/bin/testsh", "EDITOR": "ed"}[k] }
	})
	st := &starts{}
	r.start = st.start

	v := r.opened(t)

	return &mdRig{drawerRig: r, v: v, dir: dir, root: dir, st: st}
}

// drive feeds the messages of cmd and of what they return to Update, except
// the parse answers, which it hands back unsent.
func (r *drawerRig) drive(cmd tea.Cmd) (held []reviewMDMsg) {
	queue := flatten(cmd)

	for len(queue) > 0 {
		m := queue[0]
		queue = queue[1:]

		switch m := m.(type) {
		case nil:
		case reviewMDMsg:
			held = append(held, m)
		default:
			queue = append(queue, flatten(r.step(m))...)
		}
	}

	return held
}

// send delivers parse answers.
func (r *drawerRig) send(held []reviewMDMsg) {
	for _, m := range held {
		r.drive(r.step(m))
	}
}

// press presses keys and delivers every answer, the parse's included.
func (r *drawerRig) press(keys ...tea.KeyPressMsg) {
	for _, k := range keys {
		r.send(r.drive(r.step(k)))
	}
}

func (m *mdRig) onFile(t *testing.T, path string) {
	t.Helper()

	for i, row := range m.v.rows {
		if row.kind == rowFile && m.v.res.Files[row.file].Path == path {
			m.v.cursor = i

			return
		}
	}

	t.Fatalf("no file row for %s", path)
}

// kindsOf counts the rows of path by kind.
func kindsOf(v *reviewView, path string) map[reviewRowKind]int {
	out := map[reviewRowKind]int{}

	for _, row := range v.rows {
		if v.res.Files[row.file].Path == path {
			out[row.kind]++
		}
	}

	return out
}

// marked are the texts of the rendered rows that carry a change mark.
func marked(v *reviewView, path string) []string {
	var out []string

	for _, row := range v.rows {
		if row.kind == rowMD && row.mark != markNone && v.res.Files[row.file].Path == path {
			out = append(out, row.text)
		}
	}

	return out
}

func (m *mdRig) rowText(t *testing.T, sub string) int {
	t.Helper()

	return rowWith(t, m.v, sub)
}

// holdsSource reports whether the cursor is on a rendered row whose range holds line n.
func holdsSource(v *reviewView, n int) bool {
	if v.cursor < 0 || v.cursor >= len(v.rows) {
		return false
	}

	row := v.rows[v.cursor]

	return row.kind == rowMD && row.edit <= n && n <= row.other
}

func TestReviewMarkdownToggle(t *testing.T) {
	t.Parallel()

	m := newMDRig(t)
	v := m.v

	m.onFile(t, planPath)

	// m shows the info row until the parse lands.
	held := m.drive(m.step(keyRune('m')))

	if got := kindsOf(v, planPath); got[rowInfo] != 1 || got[rowMD] != 0 || got[rowHunk] != 0 {
		t.Fatalf("rows while parsing = %v, want one info row", got)
	}

	if row := v.rows[m.rowText(t, "rendering…")]; row.kind != rowInfo {
		t.Errorf("rendering… is a %d row, want an info row", row.kind)
	}

	m.send(held)

	if got := kindsOf(v, planPath); got[rowMD] == 0 || got[rowInfo] != 0 || got[rowHunk] != 0 {
		t.Fatalf("rows once rendered = %v, want rendered rows", got)
	}

	if got := m.drawn(t); !strings.Contains(got, " rendered") {
		t.Errorf("the header lacks the rendered flag:\n%s", got)
	}

	m.press(keyRune('m'))

	if got := kindsOf(v, planPath); got[rowHunk] == 0 || got[rowMD] != 0 || v.mdFiles[planPath] != nil || v.mdOn[planPath] {
		t.Errorf("rows after m again = %v, mdFiles %v, want the hunks and no doc", got, v.mdFiles)
	}

	// With f on, m on and off ends on the full view.
	m.onFile(t, planPath)
	m.press(keyRune('f'))

	if got := kindsOf(v, planPath); got[rowFull] == 0 {
		t.Fatalf("rows with f on = %v, want the full view", got)
	}

	m.press(keyRune('m'))

	if got := kindsOf(v, planPath); got[rowMD] == 0 || got[rowFull] != 0 {
		t.Fatalf("rows with f and m on = %v, want rendered rows only", got)
	}

	m.press(keyRune('m'))

	if got := kindsOf(v, planPath); got[rowFull] == 0 || got[rowMD] != 0 {
		t.Fatalf("rows after m off with f on = %v, want the full view", got)
	}

	// f off while rendered keeps the rendered rows, and m then shows the hunks.
	m.press(keyRune('m'))
	m.press(keyRune('f'))

	if got := kindsOf(v, planPath); got[rowMD] == 0 || got[rowHunk] != 0 || got[rowFull] != 0 {
		t.Fatalf("rows with f off while rendered = %v, want rendered rows", got)
	}

	m.press(keyRune('m'))

	if got := kindsOf(v, planPath); got[rowHunk] == 0 || got[rowMD] != 0 || got[rowFull] != 0 {
		t.Errorf("rows after m off with f off = %v, want the hunks", got)
	}

	if _, kept := v.full[planPath]; kept {
		t.Error("the text is kept with f and m both off")
	}
}

func TestReviewMarkdownNotices(t *testing.T) {
	t.Parallel()

	m := newMDRig(t)
	v := m.v

	tests := []struct {
		name, path, notice string
	}{
		{"a Go file", "main.go", "not a markdown file"},
		{"a binary markdown file", "bin.md", "not a markdown file"},
		{"a deleted markdown file", "gone.md", "the file is deleted"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			m.onFile(t, tc.path)

			before := slices.Clone(v.rows)
			m.notice = ""

			m.press(keyRune('m'))

			if m.notice != tc.notice {
				t.Errorf("notice = %q, want %q", m.notice, tc.notice)
			}

			if len(v.mdOn) != 0 || !slices.Equal(v.rows, before) {
				t.Errorf("m changed the view: mdOn %v, %d rows, was %d", v.mdOn, len(v.rows), len(before))
			}
		})
	}
}

// describe names a row for the jump tests.
func describe(v *reviewView, i int) string {
	row := v.rows[i]
	if row.kind == rowMD {
		return "md:" + row.text
	}

	return "go:" + diffLineAt(v, i)
}

func TestReviewMarkdownBlockJumps(t *testing.T) {
	t.Parallel()

	m := newMDRig(t)
	v := m.v

	m.onFile(t, planPath)
	m.press(keyRune('m'))

	want := []string{"md:Intro paragraph edited.", "md:2nd step"}

	for _, from := range v.blockStarts() {
		if v.rows[from].kind == rowMD && !slices.ContainsFunc(want, func(w string) bool { return strings.Contains(describe(v, from), strings.TrimPrefix(w, "md:")) }) {
			t.Errorf("block start %q is not a marked block", describe(v, from))
		}
	}

	var got []string

	v.cursor = 0

	for range 8 {
		before := v.cursor

		m.press(keyRune(']'))

		if v.cursor == before {
			break
		}

		got = append(got, describe(v, v.cursor))
	}

	// The binary file has no block, the deleted one has one, then the Go file's two hunks.
	if len(got) != 5 || !strings.Contains(got[0], "Intro paragraph edited.") || !strings.Contains(got[1], "2nd step") ||
		got[2] != "go:gone content" || got[3] != "go:line 3" || got[4] != "go:line 30" {
		t.Fatalf("] visited %q, want the two marked blocks, the deleted file's block, then the Go file's two hunks", got)
	}

	var back []string

	for range 4 {
		m.press(keyRune('['))

		back = append(back, describe(v, v.cursor))
	}

	for i := range back {
		if back[i] != got[len(got)-2-i] {
			t.Errorf("[ visited %q, want the reverse of %q", back, got)

			break
		}
	}
}

func TestReviewMarkdownBigFileMarksOnlyItsChanges(t *testing.T) {
	t.Parallel()

	// 1500 paragraphs, 2999 lines; the second and the second to last are edited.
	var committed, edited strings.Builder

	for i := 1; i <= 1500; i++ {
		if i > 1 {
			committed.WriteString("\n")
			edited.WriteString("\n")
		}

		fmt.Fprintf(&committed, "para %d\n", i)

		if i == 2 || i == 1499 {
			fmt.Fprintf(&edited, "para %d edited\n", i)
		} else {
			fmt.Fprintf(&edited, "para %d\n", i)
		}
	}

	m := newMDRigOf(t, committed.String(), edited.String())

	m.onFile(t, planPath)
	m.press(keyRune('m'))

	if got, want := marked(m.v, planPath), []string{"para 2 edited", "para 1499 edited"}; !slices.Equal(got, want) {
		t.Errorf("marked rows = %s, want %q", brief(got), want)
	}
}

func TestReviewMarkdownWidth(t *testing.T) {
	t.Parallel()

	m := newMDRig(t)
	v := m.v

	m.onFile(t, planPath)
	m.press(keyRune('m'))

	v.cursor = m.rowText(t, "Remember the milestone.")

	if !holdsSource(v, 12) {
		t.Fatalf("setup: the cursor row's range is %d to %d, want one holding 12", v.rows[v.cursor].edit, v.rows[v.cursor].other)
	}

	check := func(what string) {
		t.Helper()
		m.settle()

		lay := m.layout(t)
		if v.mdW != lay.diffW-4 {
			t.Errorf("%s: rows laid out at %d, want the diff width %d less 4", what, v.mdW, lay.diffW)
		}

		if doc := v.mdFiles[planPath].doc; doc.width != v.mdW {
			t.Errorf("%s: the doc is laid out at %d, rows at %d", what, doc.width, v.mdW)
		}

		if !holdsSource(v, 12) {
			t.Errorf("%s: the cursor left source line 12 for a row of %d to %d", what, v.rows[v.cursor].edit, v.rows[v.cursor].other)
		}

		for _, row := range v.rows {
			if row.kind == rowMD && ansi.StringWidth(row.text) > v.mdW {
				t.Errorf("%s: row %q is wider than %d", what, row.text, v.mdW)
			}
		}
	}

	sized(t, m.App, 130, 50)
	check("after a resize")

	openIn(t, m.App, core.SetRail{Open: true})
	check("after opening the rail")

	m.showTree(t)
	check("after opening the tree")

	// At the floor every drawn row is exactly the column's width.
	sized(t, m.App, 120, 40)
	check("at the floor")

	rect := mustRect(t, m.drawerRig)
	if rect.W != drawerMinW {
		t.Fatalf("the drawer is %d columns, want the floor %d", rect.W, drawerMinW)
	}

	for i, l := range m.drawerLines(m.store.State(), rect) {
		if w := ansi.StringWidth(l); w != rect.W {
			t.Errorf("drawn row %d is %d cells, want %d", i, w, rect.W)
		}
	}
}

func TestReviewMarkdownLive(t *testing.T) {
	t.Parallel()

	m := newMDRig(t)
	v := m.v

	m.onFile(t, planPath)
	m.press(keyRune('m'))

	v.cursor = m.rowText(t, "Remember the milestone.")

	if got := marked(v, planPath); len(got) != 2 {
		t.Fatalf("marked blocks = %q, want the two edits", got)
	}

	// The agent rewrites the plan; the batch reads it again.
	write(t, filepath.Join(m.dir, planPath), planLive())

	var held []reviewMDMsg

	cmd := m.batch(m.root, git.Batch{Paths: []string{planPath}})
	held = m.drive(cmd)

	if len(held) != 1 {
		t.Fatalf("%d parses started, want 1", len(held))
	}

	// Before the parse answers, the old doc and its text show together.
	if got := v.full[planPath].new; len(got) == 0 || got[8] != "- 3rd step" {
		t.Fatalf("the new text is not read yet: %q", got)
	}

	if got := marked(v, planPath); len(got) != 2 || !strings.Contains(got[0], "Intro paragraph edited.") || !strings.Contains(got[1], "2nd step") {
		t.Errorf("marks before the parse answers = %q, want the old text's", got)
	}

	for _, row := range v.rows {
		if strings.Contains(row.text, "3rd step") {
			t.Errorf("the old doc shows the new text: %q", row.text)
		}
	}

	m.send(held)

	if got := marked(v, planPath); len(got) != 1 || !strings.Contains(got[0], "3rd step") {
		t.Errorf("marks after the parse = %q, want the 3rd step", got)
	}

	if !holdsSource(v, 12) {
		t.Errorf("the cursor left source line 12: row %d, range %d to %d", v.cursor, v.rows[v.cursor].edit, v.rows[v.cursor].other)
	}
}

func TestReviewMarkdownLate(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		act  func(t *testing.T, m *mdRig)
	}{
		{"m off", func(_ *testing.T, m *mdRig) { m.press(keyRune('m')) }},
		{"b", func(_ *testing.T, m *mdRig) { m.press(keyRune('b')) }},
		{"a retarget", func(_ *testing.T, m *mdRig) { m.run(m.reviewRetarget(m.store.State().Tab().ID, m.v, 0)) }},
		{"a close", func(t *testing.T, m *mdRig) {
			openIn(t, m.App, core.CloseReview{})
			m.settle()
		}},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			m := newMDRig(t)
			m.onFile(t, planPath)

			held := m.drive(m.step(keyRune('m')))
			if len(held) != 1 {
				t.Fatalf("%d parses started, want 1", len(held))
			}

			tc.act(t, m)

			if v := m.view(); v != nil {
				m.idle(t)
			}

			m.send(held)

			v := m.view()
			if v == nil {
				return
			}

			if len(v.mdFiles) != 0 || kindsOf(v, planPath)[rowMD] != 0 {
				t.Errorf("a late answer changed the view: mdFiles %v, rows %v", v.mdFiles, kindsOf(v, planPath))
			}
		})
	}
}

func TestReviewMarkdownFoldSearchEdit(t *testing.T) {
	t.Parallel()

	m := newMDRig(t)
	v := m.v

	m.onFile(t, planPath)
	m.press(keyRune('m'))

	file := v.mdFiles[planPath]
	lay := file.doc.layout(v.mdW, m.icons)

	if len(lay.Folds) != 1 {
		t.Fatalf("%d folds in the plan, want 1", len(lay.Folds))
	}

	hidden := func() bool {
		return slices.ContainsFunc(v.rows, func(r reviewRow) bool { return r.kind == rowMD && strings.Contains(r.text, "Hidden detail") })
	}

	before := hidden()

	head := slices.IndexFunc(v.rows, func(r reviewRow) bool { return r.kind == rowMD && r.line == lay.Folds[0].Line })
	if head < 0 {
		t.Fatal("no row for the fold head")
	}

	v.cursor = head

	m.press(keyEnter)

	if hidden() == before {
		t.Errorf("enter on the fold head left the fold's text shown = %v", hidden())
	}

	// enter anywhere else does nothing yet.
	v.cursor = m.rowText(t, "Intro paragraph edited.")
	rows := len(v.rows)

	m.press(keyEnter)

	if len(v.rows) != rows {
		t.Errorf("enter on a plain rendered row changed the rows: %d, was %d", len(v.rows), rows)
	}

	// / finds text inside the rendered rows.
	v.cursor = 0

	m.press(keyRune('/'))
	m.typed("milestone")
	m.press(keyEnter)

	if row := v.rows[v.cursor]; row.kind != rowMD || !strings.Contains(row.text, "milestone") {
		t.Errorf("search landed on %+v, want a rendered row holding milestone", row)
	}

	// e opens the editor at the range's first line.
	m.step(keyRune('e'))

	if m.st.count() != 1 {
		t.Fatalf("e started %d processes, want 1", m.st.count())
	}

	argv := m.st.cmds[0].Argv
	if got, want := argv[len(argv)-2:], []string{"+12", filepath.Join(m.dir, planPath)}; !slices.Equal(got, want) {
		t.Errorf("the editor got %q, want %q", got, want)
	}
}

// e on a rendered row with no source opens the editor at the next block, else the previous one.
func TestReviewMarkdownEditBlankRow(t *testing.T) {
	t.Parallel()

	m := newMDRig(t)
	v := m.v

	m.onFile(t, planPath)
	m.press(keyRune('m'))

	blank := slices.IndexFunc(v.rows, func(r reviewRow) bool { return r.kind == rowMD && r.text == "" && r.other < 0 })
	if blank < 0 {
		t.Fatal("no blank rendered row")
	}

	if got := v.rows[blank].edit; got != 3 {
		t.Errorf("the blank row after the heading edits line %d, want 3 (the intro)", got)
	}

	v.cursor = blank

	m.step(keyRune('e'))

	if m.st.count() != 1 {
		t.Fatalf("e started %d processes, want 1", m.st.count())
	}

	argv := m.st.cmds[0].Argv
	if got, want := argv[len(argv)-2:], []string{"+3", filepath.Join(m.dir, planPath)}; !slices.Equal(got, want) {
		t.Errorf("the editor got %q, want %q", got, want)
	}
}

// hintText is the key hints at the cursor as one string.
func hintText(t *testing.T, r *drawerRig) string {
	t.Helper()

	d, _, ok := r.drawCtxFor(r.store.State())
	if !ok {
		t.Fatal("no drawer to draw")
	}

	var b strings.Builder

	for _, run := range d.hintRuns(500) {
		b.WriteString(run.text)
	}

	return b.String()
}

func TestReviewMarkdownHints(t *testing.T) {
	t.Parallel()

	m := newMDRig(t)

	m.onFile(t, planPath)

	if got := hintText(t, m.drawerRig); !strings.Contains(got, "m render   f full") {
		t.Errorf("hints on a markdown file's source = %q, want m render before f full", got)
	}

	m.onFile(t, "main.go")

	if got := hintText(t, m.drawerRig); strings.Contains(got, "render") {
		t.Errorf("hints on a Go file = %q, want no m", got)
	}

	m.onFile(t, planPath)
	m.press(keyRune('m'))

	want := "j/k line   ]/[ change   z zoom   enter open   m source   f full   / search"
	if got := hintText(t, m.drawerRig); got != want {
		t.Errorf("hints on a rendered file = %q, want %q", got, want)
	}

	// The cursor on another file brings the usual list back.
	m.onFile(t, "main.go")

	if got := hintText(t, m.drawerRig); strings.Contains(got, "enter open") {
		t.Errorf("hints on the Go file under a rendered one = %q", got)
	}

	// Pull request mode keeps its own list.
	m.v.src = git.PRSource{Worktree: m.root, Number: 7}
	m.onFile(t, "main.go")

	wantPR := "j/k line   ]/[ hunk   z zoom   i conversation   c checks   a approve   M merge   O browser   space reviewed   R show reviewed   b/t mode"
	if got := hintText(t, m.drawerRig); got != wantPR {
		t.Errorf("PR hints = %q, want %q", got, wantPR)
	}
}

func TestReviewMarkdownKeyIsACommand(t *testing.T) {
	t.Parallel()

	reg := newTestApp(t).reg

	if _, ok := reg.get("review.markdown"); !ok {
		t.Error("review.markdown is not a command")
	}

	if !slices.Contains(reviewKeys, reviewKey{"m", "review.markdown"}) {
		t.Error("m is not bound to review.markdown")
	}

	if treeCommand("review.markdown") {
		t.Error("review.markdown works while the tree has the keyboard")
	}
}

// mdScene is a drawer on a branch holding one rendered plan.
func mdScene(narrow bool) func(th string, icons bool) reviewScene {
	return func(th string, icons bool) reviewScene {
		s := reviewScene{theme: th, icons: icons, w: 200, h: 52, branch: true, focused: true}
		if narrow {
			s.w, s.h = 100, 40
		}

		s.res = mdResult()
		s.tune = func(t *testing.T, a *App, v *reviewView) {
			t.Helper()

			old, nw := splitLines(planWith(nil)), splitLines(planEdited())
			doc := parseMarkdown([]byte(strings.Join(nw, "\n")+"\n"), "", nil)
			f := v.res.Files[0]

			v.mdOn = map[string]bool{planPath: true}
			v.mdFiles = map[string]*mdFile{planPath: {doc: doc, text: newFullText(old, nw, nil), ids: blobIDs{f.OldOID, f.NewOID}}}
			v.mdW, v.mdIcons = 100, icons
			v.rebuild(false)
			v.cursor = rowWith(t, v, "Remember the milestone.")
		}

		return s
	}
}

// mdResult is the plan changed in two places and a Go file changed in one.
func mdResult() git.Result {
	plan := fileSpec(planPath, git.Modified,
		hunkSpec(1, 1, "", " # Plan", " ", "-Intro paragraph here.", "+Intro paragraph edited.", " "),
		hunkSpec(7, 7, "", " - first step", "-- second step", "+- 2nd step", " - third step"),
	)
	goFile := fileSpec("main.go", git.Modified, hunkSpec(3, 3, "func main()", " a", "-b", "+c", " d"))

	return git.Result{Files: []git.FileDiff{plan, goFile}}
}

// A full-text read that answers after m went off keeps no text and starts no parse.
func TestReviewMarkdownLateRead(t *testing.T) {
	t.Parallel()

	m := newMDRig(t)
	v := m.v

	m.onFile(t, planPath)

	reads := flatten(m.step(keyRune('m')))
	if len(reads) != 1 {
		t.Fatalf("m started %d reads, want 1", len(reads))
	}

	if _, ok := reads[0].(reviewFullMsg); !ok {
		t.Fatalf("m started %T, want a full-text read", reads[0])
	}

	m.press(keyRune('m'))

	rows := slices.Clone(v.rows)

	if held := m.drive(m.step(reads[0])); len(held) != 0 {
		t.Errorf("the late read started %d parses", len(held))
	}

	if _, kept := v.full[planPath]; kept || len(v.mdOn) != 0 || len(v.mdAsk) != 0 || len(v.mdFiles) != 0 || !slices.Equal(v.rows, rows) {
		t.Errorf("a late read changed the view: text kept %v, mdOn %v, mdAsk %v, rows %d, was %d", kept, v.mdOn, v.mdAsk, len(v.rows), len(rows))
	}
}

// A parse that answers after p moved the drawer to the pull request changes nothing.
func TestReviewMarkdownLateAfterP(t *testing.T) {
	t.Parallel()

	f := newFakeGit(t)
	rp := f.repo(t, "")
	dir := rp.Root()
	write(t, filepath.Join(dir, "a.txt"), "a\n")
	write(t, filepath.Join(dir, planPath), planWith(nil))
	rp.commit("init")
	write(t, filepath.Join(dir, "a.txt"), "a changed\n")
	rp.commit("feature work")

	r := newPRDrawerOn(t, f, prScaffold(t, rp))
	r.poll()

	// The plan is edited in the working tree, which branch mode shows.
	write(t, filepath.Join(dir, planPath), planEdited())

	v := r.opened(t)

	for i, row := range v.rows {
		if row.kind == rowFile && v.res.Files[row.file].Path == planPath {
			v.cursor = i
		}
	}

	held := r.drive(r.step(keyRune('m')))
	if len(held) != 1 {
		t.Fatalf("%d parses started, want 1 (files %v, notice %q)", len(held), viewPaths(v), r.notice)
	}

	r.typed("p")
	r.until(t, "PR mode", r.inPRMode(7))

	r.send(held)

	if len(v.mdFiles) != 0 || len(v.mdOn) != 0 || kindsOf(v, planPath)[rowMD] != 0 {
		t.Errorf("a late answer changed the PR view: mdFiles %v, mdOn %v, rows %v", v.mdFiles, v.mdOn, kindsOf(v, planPath))
	}
}
