package ui

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/mhersson/aim/internal/md"
)

// finderTree is the folders and files the finder tests browse, under a home.
var finderTree = map[string]string{
	"proj/README.md":          "# Readme\n",
	"proj/docs/guide.md":      "# Guide\n\nthe guide\n",
	"proj/docs/deep/notes.md": "# Notes\n",
	"proj/.config/aim.md":     "# Aim\n",
	"proj/.secret.md":         "# Secret\n",
	"proj/main.go":            "package main\n",
	"top.md":                  "# Top\n",
}

// finderRig is an app on a fake git with no repository, whose focused pane works
// in start under a fresh home that holds tree.
type finderRig struct {
	*pageRig
	home string
}

// finderMod is the time every file of a rig's tree was changed, and the rig's
// clock reads two hours later, so the preview's age is 2h.
var finderMod = time.Unix(1_700_000_000, 0)

func newFinderRig(t *testing.T, start string, tree map[string]string, opts ...func(*Options)) *finderRig {
	t.Helper()

	home, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}

	for name, text := range tree {
		path := filepath.Join(home, name)
		write(t, path, text)

		if err := os.Chtimes(path, finderMod, finderMod); err != nil {
			t.Fatal(err)
		}
	}

	dir := filepath.Join(home, start)
	r := newKeysRig(t, dir, append([]func(*Options){gitOf(newFakeGit(t)), func(o *Options) {
		o.Getenv = func(k string) string {
			return map[string]string{"SHELL": "/bin/testsh", "EDITOR": "ed", "HOME": home}[k]
		}
	}}, opts...)...)
	st := &starts{}
	r.start = st.start
	r.icons = true
	r.clock = &fakeClock{t: finderMod.Add(2 * time.Hour)}

	sized(t, r.App, 120, 40)

	return &finderRig{pageRig: &pageRig{drawerRig: r, dir: dir, root: dir, st: st}, home: home}
}

// at is a path under the rig's home.
func (r *finderRig) at(rel string) string { return filepath.Join(r.home, filepath.FromSlash(rel)) }

// selectedRel is the rel of the row under the cursor.
func (r *finderRig) selectedRel(t *testing.T) string {
	t.Helper()

	row, ok := r.finder.selected()
	if !ok {
		t.Fatalf("no row under the cursor among %q", rowRels(r.finder.rows))
	}

	return row.rel
}

// clearQuery deletes the prompt's text with backspace.
func (r *finderRig) clearQuery() {
	for r.finder.input.Value() != "" {
		r.press(keyBackspace)
	}
}

func TestFinderBrowse(t *testing.T) {
	t.Parallel()

	r := newFinderRig(t, "proj", finderTree)
	r.listed(t)

	if want := []string{"^..", "/docs", "-README.md"}; !slices.Equal(rowRels(r.finder.rows), want) || r.finder.dir != r.at("proj") {
		t.Fatalf("rows %q in %s, want %q in %s", rowRels(r.finder.rows), r.finder.dir, want, r.at("proj"))
	}

	if got := r.selectedRel(t); got != "docs" {
		t.Errorf("the cursor starts on %q, want the first entry under ..", got)
	}

	r.press(keyEnter)

	if r.finder.dir != r.at("proj/docs") || !slices.Equal(rowRels(r.finder.rows), []string{"^..", "/deep", "-guide.md"}) {
		t.Fatalf("after going in: %s with %q", r.finder.dir, rowRels(r.finder.rows))
	}

	r.press(keyEnter)
	r.press(keyBackspace)

	if r.finder.dir != r.at("proj/docs") || r.selectedRel(t) != "deep" {
		t.Errorf("backspace on an empty prompt: %s with the cursor on %q, want proj/docs on the folder it came from", r.finder.dir, r.selectedRel(t))
	}

	r.finderSelect(t, "..")
	r.press(keyEnter)

	if r.finder.dir != r.at("proj") || r.selectedRel(t) != "docs" {
		t.Errorf("enter on ..: %s with the cursor on %q, want proj on docs", r.finder.dir, r.selectedRel(t))
	}
}

func TestFinderTop(t *testing.T) {
	t.Parallel()

	r := newFinderRig(t, "proj", finderTree)
	r.listed(t)
	r.typeKeys("/")

	if r.finder.dir != "/" || slices.Contains(rowRels(r.finder.rows), "^..") {
		t.Fatalf("after /: %s with %q, want the top with no .. row", r.finder.dir, rowRels(r.finder.rows))
	}

	r.press(keyBackspace)

	if r.finder.dir != "/" {
		t.Errorf("backspace at the top went to %s, want it to do nothing", r.finder.dir)
	}
}

func TestFinderJumps(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		keys      string
		wantDir   string // under the home; "/" is the top
		wantQuery string
		wantRows  []string // nil to not check
	}{
		{name: "tilde and slash go home", keys: "~/", wantDir: "."},
		{name: "two dots and slash go up", keys: "../", wantDir: "."},
		{name: "a lone tilde is one jump row", keys: "~", wantDir: "proj", wantQuery: "~", wantRows: []string{"~~/"}},
		{name: "a lone pair of dots is one up row", keys: "..", wantDir: "proj", wantQuery: "..", wantRows: []string{"^.."}},
		{name: "a slash after letters is a letter", keys: "do/", wantDir: "proj", wantQuery: "do/"},
		{name: "a slash after a dot and letters is a letter", keys: ".c/", wantDir: "proj", wantQuery: ".c/"},
		{name: "a dot lists the hidden entries", keys: ".", wantDir: "proj", wantQuery: ".", wantRows: []string{"/.config", "-.secret.md"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			r := newFinderRig(t, "proj", finderTree)
			r.listed(t)
			r.typeKeys(tt.keys)

			if got, want := r.finder.dir, r.at(tt.wantDir); got != want {
				t.Errorf("dir = %s, want %s", got, want)
			}

			if got := r.finder.input.Value(); got != tt.wantQuery {
				t.Errorf("query = %q, want %q", got, tt.wantQuery)
			}

			if tt.wantRows != nil && !slices.Equal(rowRels(r.finder.rows), tt.wantRows) {
				t.Errorf("rows = %q, want %q", rowRels(r.finder.rows), tt.wantRows)
			}
		})
	}

	t.Run("enter takes the tilde row", func(t *testing.T) {
		t.Parallel()

		r := newFinderRig(t, "proj", finderTree)
		r.listed(t)
		r.typeKeys("~")
		r.press(keyEnter)

		if r.finder.dir != r.home || r.finder.input.Value() != "" {
			t.Errorf("dir %s, query %q, want home and an empty prompt", r.finder.dir, r.finder.input.Value())
		}
	})

	t.Run("enter takes the two-dots row and lands on the folder it left", func(t *testing.T) {
		t.Parallel()

		r := newFinderRig(t, "proj", finderTree)
		r.listed(t)
		r.typeKeys("..")
		r.press(keyEnter)

		if r.finder.dir != r.home || r.selectedRel(t) != "proj" {
			t.Errorf("dir %s on %q, want home on proj", r.finder.dir, r.selectedRel(t))
		}
	})
}

func TestFinderSearch(t *testing.T) {
	t.Parallel()

	r := newFinderRig(t, "proj", finderTree)
	r.listed(t)
	r.typeKeys("guide")

	rows := rowRels(r.finder.rows)
	if len(rows) == 0 || rows[0] != "-docs/guide.md" || slices.Contains(rows, "^..") {
		t.Fatalf("rows for guide = %q, want docs/guide.md first and no ..", rows)
	}

	if r.finder.walk == nil || r.finder.walk.running {
		t.Errorf("walk = %+v, want one that finished", r.finder.walk)
	}

	t.Run("a folder in the results is entered and the query cleared", func(t *testing.T) {
		r.clearQuery()
		r.typeKeys("deep")

		if got := rowRels(r.finder.rows); len(got) == 0 || got[0] != "/docs/deep" {
			t.Fatalf("rows for deep = %q, want the folder docs/deep first", got)
		}

		r.press(keyEnter)

		if r.finder.dir != r.at("proj/docs/deep") || r.finder.input.Value() != "" || r.finder.walk != nil {
			t.Errorf("dir %s, query %q, walk %v, want the folder entered with the query and walk gone", r.finder.dir, r.finder.input.Value(), r.finder.walk != nil)
		}
	})

	t.Run("hidden entries need a leading dot", func(t *testing.T) {
		r := newFinderRig(t, "proj", finderTree)
		r.listed(t)
		r.typeKeys("aim")

		if got := rowRels(r.finder.rows); slices.Contains(got, "-.config/aim.md") {
			t.Errorf("rows for aim = %q, want the hidden file left out", got)
		}

		r.clearQuery()
		r.typeKeys(".co")

		if got := rowRels(r.finder.rows); !slices.Contains(got, "/.config") || !slices.Contains(got, "-.config/aim.md") {
			t.Errorf("rows for .co = %q, want .config and the file under it", got)
		}
	})
}

// walkFixture is a batch of names a crafted walk message delivers.
func walkFixture(n int) []md.WalkEntry {
	out := make([]md.WalkEntry, n)
	for i := range out {
		out[i] = md.WalkEntry{Rel: fmt.Sprintf("d%03d/f%05d.md", i%100, i)}
	}

	return out
}

func TestFinderWalkState(t *testing.T) {
	t.Parallel()

	r := newFinderRig(t, "proj", finderTree)
	r.listed(t)

	held := r.flow(r.step(keyRune('q')), holdWalk)
	if len(held) != 1 {
		t.Fatalf("typing held %d walk messages, want 1", len(held))
	}

	f := r.finder
	if !f.walk.running || len(f.rows) != 0 {
		t.Fatalf("walk running %v with %d rows, want it running and nothing yet", f.walk.running, len(f.rows))
	}

	if text, isErr := finderMessage(f); text != finderSearching || isErr {
		t.Errorf("message = %q, error %v, want %q", text, isErr, finderSearching)
	}

	if got := f.countTail(); got != (&finderWalk{running: true}).tail() {
		t.Errorf("count tail = %q while the walk runs", got)
	}

	r.Update(finderWalkMsg{view: f, seq: f.walk.seq, batch: []md.WalkEntry{{Rel: "q/quick.md"}, {Rel: "other.md"}}})

	if got := rowRels(f.rows); !slices.Equal(got, []string{"-q/quick.md"}) {
		t.Errorf("rows = %q, want the batch filtered by the query", got)
	}

	r.Update(finderWalkMsg{view: f, seq: f.walk.seq, done: true, stop: md.WalkFileLimit})

	if f.walk.running || f.countTail() != (&finderWalk{stop: md.WalkFileLimit}).tail() {
		t.Errorf("after the end: running %v, tail %q, want the file limit's", f.walk.running, f.countTail())
	}

	t.Run("a longer query filters what the walk found without a new walk", func(t *testing.T) {
		walk := f.walk

		r.typeKeys("u")

		if f.walk != walk || !slices.Equal(rowRels(f.rows), []string{"-q/quick.md"}) {
			t.Errorf("walk replaced %v, rows %q, want the same walk and the filtered rows", f.walk != walk, rowRels(f.rows))
		}
	})

	t.Run("a dotted query needs another walk", func(t *testing.T) {
		walk := f.walk

		r.clearQuery()
		r.typeKeys(".c")

		if f.walk == walk || !f.walk.hidden {
			t.Errorf("walk replaced %v, hidden %v, want a new walk that reaches hidden entries", f.walk != walk, f.walk != nil && f.walk.hidden)
		}
	})
}

func TestFinderFilterOffLoop(t *testing.T) {
	t.Parallel()

	r := newFinderRig(t, "proj", finderTree)
	r.listed(t)
	r.typeKeys("f0")

	f := r.finder
	before := rowRels(f.rows)

	big := walkFixture(finderFilterInline + 1)
	cmd := r.step(finderWalkMsg{view: f, seq: f.walk.seq, batch: big})

	if !f.filter.running || !slices.Equal(rowRels(f.rows), before) {
		t.Fatalf("filter running %v, rows changed %v, want a filter off the loop and the old rows on show", f.filter.running, !slices.Equal(rowRels(f.rows), before))
	}

	more := r.step(finderWalkMsg{view: f, seq: f.walk.seq, batch: []md.WalkEntry{{Rel: "late/f0late.md"}}})
	if filters := filterMsgs(more); len(filters) != 0 || !f.filter.dirty {
		t.Fatalf("a second batch started %d filters, dirty %v, want none and a dirty mark", len(filters), f.filter.dirty)
	}

	answers := filterMsgs(cmd)
	if len(answers) != 1 {
		t.Fatalf("the first batch led to %d filter answers, want 1", len(answers))
	}

	next := r.step(answers[0])

	if len(f.rows) < finderFilterInline || f.filter.dirty || !f.filter.running {
		t.Fatalf("rows %d, dirty %v, running %v, want the answer shown and the next filter started", len(f.rows), f.filter.dirty, f.filter.running)
	}

	r.step(filterMsgs(next)[0])

	if !slices.Contains(rowRels(f.rows), "-late/f0late.md") || f.filter.running {
		t.Errorf("the second batch's name is not in the rows after the next filter, or a filter still runs")
	}
}

// filterMsgs runs cmd and returns the filter answers among its messages.
func filterMsgs(cmd tea.Cmd) []finderFilterMsg {
	var out []finderFilterMsg

	for _, m := range flatten(cmd) {
		if fm, ok := m.(finderFilterMsg); ok {
			out = append(out, fm)
		}
	}

	return out
}

func TestFinderCursorOnRefilter(t *testing.T) {
	t.Parallel()

	r := newFinderRig(t, "proj", finderTree)
	r.listed(t)
	r.typeKeys("x")

	f := r.finder
	r.Update(finderWalkMsg{view: f, seq: f.walk.seq, batch: []md.WalkEntry{{Rel: "a/x1.md"}, {Rel: "b/x2.md"}, {Rel: "c/x3.md"}}})

	if f.sel != 0 {
		t.Fatalf("the cursor is on row %d after a batch, want the first", f.sel)
	}

	r.press(keyDown)

	moved := f.rows[f.sel].rel

	r.Update(finderWalkMsg{view: f, seq: f.walk.seq, batch: []md.WalkEntry{{Rel: "0/x.md"}}})

	if got := f.rows[f.sel].rel; got != moved {
		t.Errorf("a batch moved the cursor from %q to %q, want it kept on the entry", moved, got)
	}
}

func TestFinderOpenAndBack(t *testing.T) {
	t.Parallel()

	r := newFinderRig(t, "proj", finderTree)
	r.listed(t)
	r.typeKeys("md")
	r.finderSelect(t, "docs/guide.md")

	f := r.finder
	dir, query, rows, sel, walk := f.dir, f.input.Value(), rowRels(f.rows), f.sel, f.walk

	if sel == 0 {
		t.Fatalf("setup: the cursor is on the first of %q, so the case cannot tell a kept cursor", rows)
	}

	r.press(keyEnter)

	if r.plan == nil || r.plan.file == nil || r.plan.file.path != r.at("proj/docs/guide.md") {
		t.Fatalf("plan = %+v, want guide.md open (notice %q)", r.plan, r.notice)
	}

	r.press(keyEsc)

	if r.plan != nil || r.finder != f {
		t.Fatalf("esc in the file: plan open %v, same finder %v, want back on the finder", r.plan != nil, r.finder == f)
	}

	if f.dir != dir || f.input.Value() != query || !slices.Equal(rowRels(f.rows), rows) || f.sel != sel || f.walk != walk {
		t.Errorf("the finder changed: dir %s, query %q, rows %q, cursor %d, walk kept %v", f.dir, f.input.Value(), rowRels(f.rows), f.sel, f.walk == walk)
	}

	r.press(keyEsc)

	if r.finder != nil {
		t.Error("the second esc left the finder open")
	}

	t.Run("a new open starts over in the current folder", func(t *testing.T) {
		r.mdOpen()

		if r.finder == nil || r.finder.dir != r.at("proj") || r.finder.input.Value() != "" || r.finder.walk != nil || r.finder.moved {
			t.Errorf("finder = %+v, want a fresh one in proj with an empty prompt", r.finder)
		}
	})
}

func TestFinderBackWhileWalking(t *testing.T) {
	t.Parallel()

	r := newFinderRig(t, "proj", finderTree)
	r.listed(t)

	if held := r.flow(r.step(keyRune('g')), holdWalk); len(held) != 1 {
		t.Fatalf("typing held %d walk messages, want 1", len(held))
	}

	r.typeKeys("uide")

	f := r.finder
	r.Update(finderWalkMsg{view: f, seq: f.walk.seq, batch: []md.WalkEntry{{Rel: "docs/guide.md"}}})

	opened := r.selectedRel(t)

	r.press(keyEnter)

	if r.plan == nil || !f.walk.running {
		t.Fatalf("plan open %v, walk running %v, want a file open over a running walk (notice %q)", r.plan != nil, f.walk.running, r.notice)
	}

	r.Update(finderWalkMsg{view: f, seq: f.walk.seq, batch: []md.WalkEntry{{Rel: "guide.md"}}})
	r.press(keyEsc)

	if got := r.selectedRel(t); got != opened {
		t.Errorf("esc put the cursor on %q, want the opened %q among %q", got, opened, rowRels(f.rows))
	}
}

func TestFinderRoot(t *testing.T) {
	t.Parallel()

	t.Run("outside any repository the file's own folder", func(t *testing.T) {
		t.Parallel()

		r := newFinderRig(t, "proj", finderTree)
		r.listed(t)
		r.goTo(t, "docs/guide.md")

		if r.plan == nil || r.plan.root != r.at("proj/docs") || r.plan.file.repo != "" {
			t.Fatalf("plan = %+v (notice %q), want the file's folder as the root and no repository", r.plan, r.notice)
		}

		if top := r.screen()[2]; !strings.Contains(top, "~/proj/docs/guide.md") || strings.Contains(top, " · ") {
			t.Errorf("top border = %q, want the path with ~ and no repository lead", top)
		}

		r.settle()

		if w := r.watches.file; w == nil || w.w == nil || w.path != r.at("proj/docs/guide.md") || r.notice != "" {
			t.Errorf("file watch = %+v, notice %q, want the file watched and no failure outside a repository", w, r.notice)
		}
	})

	t.Run("outside any repository the file reloads on a signal", func(t *testing.T) {
		t.Parallel()

		r := newFinderRig(t, "proj", finderTree)
		r.listed(t)
		r.goTo(t, "docs/guide.md")
		r.settle()

		write(t, r.at("proj/docs/guide.md"), "# Guide\n\nchanged on disk\n")
		r.flow(r.step(fileChangedMsg{ref: r.watches.file}), nil)

		if !strings.Contains(r.docText(), "changed on disk") {
			t.Errorf("the file did not reload:\n%s", r.docText())
		}
	})

	t.Run("a symlink to a file in another folder opens by its resolved path", func(t *testing.T) {
		t.Parallel()

		r := newFinderRig(t, "proj", finderTree)
		target := r.at("elsewhere/real.md")
		write(t, target, "# Real\n\nlinked text\n")

		if err := os.Symlink(target, r.at("proj/link.md")); err != nil {
			t.Skipf("no symlinks: %v", err)
		}

		r.listed(t)
		r.finderSelect(t, "link.md")
		r.press(keyEnter)

		if r.plan == nil || r.plan.file.path != target || r.plan.root != filepath.Dir(target) {
			t.Fatalf("plan = %+v (notice %q), want %s under %s", r.plan, r.notice, target, filepath.Dir(target))
		}

		if !strings.Contains(r.docText(), "linked text") {
			t.Errorf("the box shows %q, want the target's text", r.docText())
		}
	})

	t.Run("in a repository the repository's root", func(t *testing.T) {
		t.Parallel()

		m := newMDOpenRig(t, nil)
		m.opens(t, "docs/a.md")

		if m.plan.root != m.root || m.plan.file.repo == "" {
			t.Errorf("root %s, repo %q, want %s and the repository's name", m.plan.root, m.plan.file.repo, m.root)
		}
	})
}

func TestFinderEditorClosesIt(t *testing.T) {
	t.Parallel()

	m := newMDOpenRig(t, nil)
	m.opens(t, "docs/a.md")

	base := m.st.count()

	// The new pane's wait command blocks until its process ends, so the key is stepped, not driven.
	m.step(keyRune('e'))

	if m.plan != nil || m.finder != nil || m.st.count()-base != 1 {
		t.Errorf("plan open %v, finder open %v, %d panes started, want both closed and one editor", m.plan != nil, m.finder != nil, m.st.count()-base)
	}
}

func TestFinderTooSmall(t *testing.T) {
	t.Parallel()

	r := newFinderRig(t, "proj", finderTree)
	r.listed(t)
	sized(t, r.App, 20, 8)

	r.typeKeys("x")
	r.press(keyEnter)

	if r.finder == nil || r.finder.input.Value() != "" || r.plan != nil || r.finder.dir != r.at("proj") {
		t.Fatalf("keys below the size changed the finder: %+v", r.finder)
	}

	if strings.Contains(strings.Join(r.screen(), "\n"), "esc close") {
		t.Error("a box too small to draw drew")
	}

	r.press(keyEsc)

	if r.finder != nil {
		t.Error("esc did not close the finder")
	}
}

func TestFinderLate(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		// late runs after a held answer was made; it makes that answer stale.
		late func(t *testing.T, r *finderRig)
	}{
		{"a folder change", func(t *testing.T, r *finderRig) {
			r.clearQuery()
			r.finderSelect(t, "docs")
			r.press(keyEnter)
		}},
		{"the finder closed", func(t *testing.T, r *finderRig) { r.press(keyEsc) }},
		{"a second open", func(t *testing.T, r *finderRig) { r.mdOpen() }},
	}

	for _, tt := range tests {
		t.Run("a walk batch after "+tt.name, func(t *testing.T) {
			t.Parallel()

			r := newFinderRig(t, "proj", finderTree)
			r.listed(t)

			held := r.flow(r.step(keyRune('g')), holdWalk)
			if len(held) != 1 {
				t.Fatalf("held %d walk messages, want 1", len(held))
			}

			tt.late(t, r)

			var (
				rows []string
				walk *finderWalk
			)

			if r.finder != nil {
				rows, walk = rowRels(r.finder.rows), r.finder.walk
			}

			r.step(held[0])

			if r.finder == nil {
				return
			}

			if got := rowRels(r.finder.rows); !slices.Equal(got, rows) || r.finder.walk != walk {
				t.Errorf("a stale walk message changed the finder: rows %q (was %q), walk kept %v", got, rows, r.finder.walk == walk)
			}
		})

		t.Run("a filter answer after "+tt.name, func(t *testing.T) {
			t.Parallel()

			r := newFinderRig(t, "proj", finderTree)
			r.listed(t)

			f := r.finder
			stale := finderFilterMsg{view: f, gen: f.gen, query: "", rows: []finderRow{{rel: "zzz.md"}}}

			tt.late(t, r)
			r.Update(stale)

			if r.finder != nil && slices.Contains(rowRels(r.finder.rows), "-zzz.md") {
				t.Errorf("a stale filter answer put its rows on show: %q", rowRels(r.finder.rows))
			}
		})
	}

	t.Run("a walk batch of a walk since replaced", func(t *testing.T) {
		t.Parallel()

		r := newFinderRig(t, "proj", finderTree)
		r.listed(t)

		held := r.flow(r.step(keyRune('g')), holdWalk)
		if len(held) != 1 {
			t.Fatalf("held %d walk messages, want 1", len(held))
		}

		r.clearQuery()
		r.typeKeys(".c")

		f := r.finder
		found := len(f.walk.entries)

		r.step(held[0])

		if len(f.walk.entries) != found {
			t.Errorf("the replaced walk's batch joined the new walk's names: %d entries, was %d", len(f.walk.entries), found)
		}
	})

	t.Run("a read after esc", func(t *testing.T) {
		t.Parallel()

		r := newFinderRig(t, "proj", finderTree)
		r.listed(t)
		r.finderSelect(t, "README.md")

		held := r.flow(r.step(keyEnter), holdMDFiles)
		if len(held) != 1 {
			t.Fatalf("enter held %d answers, want 1", len(held))
		}

		r.press(keyEsc)
		r.step(held[0])

		if r.plan != nil {
			t.Error("a read that esc cancelled opened the box")
		}
	})

	t.Run("a read while the compose box is open", func(t *testing.T) {
		t.Parallel()

		r := newFinderRig(t, "proj", finderTree)
		r.listed(t)
		r.finderSelect(t, "README.md")

		held := r.flow(r.step(keyEnter), holdMDFiles)
		r.compose = &composeView{}
		r.step(held[0])

		if r.plan != nil {
			t.Error("a read under the compose box opened the box")
		}
	})
}

func TestFinderFedUnderTheBox(t *testing.T) {
	t.Parallel()

	r := newFinderRig(t, "proj", finderTree)
	r.listed(t)
	r.typeKeys("guide")
	r.press(keyEnter)

	f := r.finder
	if r.plan == nil || f == nil {
		t.Fatalf("plan open %v, finder open %v", r.plan != nil, f != nil)
	}

	r.Update(finderWalkMsg{view: f, seq: f.walk.seq, batch: []md.WalkEntry{{Rel: "z/guide2.md"}}})

	if !slices.Contains(rowRels(f.rows), "-z/guide2.md") {
		t.Errorf("rows = %q, want a batch under the reading box to reach the finder", rowRels(f.rows))
	}
}
