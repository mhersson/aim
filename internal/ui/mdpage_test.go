package ui

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/mhersson/aim/internal/core"
	"github.com/mhersson/aim/internal/git"
	"github.com/mhersson/aim/internal/md"
)

func TestReadLocalMarkdown(t *testing.T) {
	t.Parallel()

	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}

	outside, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}

	write(t, filepath.Join(root, "in.md"), "inside\n")
	write(t, filepath.Join(root, "sub", "deep.md"), "deep\n")
	write(t, filepath.Join(root, "big.md"), strings.Repeat("x", 5<<20))
	write(t, filepath.Join(outside, "secret.md"), "secret\n")

	if err := os.Symlink(filepath.Join(outside, "secret.md"), filepath.Join(root, "link.md")); err != nil {
		t.Skipf("no symlinks: %v", err)
	}

	if err := os.Symlink("in.md", filepath.Join(root, "alias.md")); err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name    string
		path    string
		want    string // the text read; "" when an error is wanted
		wantLen int
		err     error
		anyErr  bool // an error that is none of the sentinels
	}{
		{name: "a relative file", path: "in.md", want: "inside\n"},
		{name: "an absolute file", path: filepath.Join(root, "sub", "deep.md"), want: "deep\n"},
		{name: "a symlink that stays inside", path: "alias.md", want: "inside\n"},
		{name: "a path with dots that stays inside", path: "sub/../in.md", want: "inside\n"},
		{name: "a relative path out of the worktree", path: "../outside.md", err: errOutsideWorktree},
		{name: "an absolute path elsewhere", path: filepath.Join(outside, "secret.md"), err: errOutsideWorktree},
		{name: "the root's parent", path: filepath.Dir(root), err: errOutsideWorktree},
		{name: "a symlink to a file outside", path: "link.md", anyErr: true},
		{name: "a folder", path: "sub", err: errIsFolder},
		{name: "the root", path: root, err: errIsFolder},
		{name: "a missing file", path: "nope.md", err: fs.ErrNotExist},
		{name: "a file past the cap", path: "big.md", wantLen: md.MaxBytes + 1},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got, err := readLocalMarkdown(context.Background(), root, tt.path)

			switch {
			case tt.err != nil:
				if !errors.Is(err, tt.err) || got != nil {
					t.Errorf("read = %q, %v, want error %v and nothing read", got, err, tt.err)
				}
			case tt.anyErr:
				if err == nil || got != nil || errors.Is(err, errOutsideWorktree) {
					t.Errorf("read = %q, %v, want a read error and nothing read", got, err)
				}
			case tt.wantLen > 0:
				if err != nil || len(got) != tt.wantLen {
					t.Errorf("read %d bytes, %v, want %d", len(got), err, tt.wantLen)
				}
			default:
				if err != nil || string(got) != tt.want {
					t.Errorf("read = %q, %v, want %q", got, err, tt.want)
				}
			}
		})
	}
}

func TestMarkdownNotice(t *testing.T) {
	t.Parallel()

	home := "/home/me"
	root := "/home/me/code/repo"

	tests := []struct {
		name string
		path string
		err  error
		want string
	}{
		{"outside, under home", "/home/me/other/x.md", errOutsideWorktree, "not opening ~/other/x.md: outside the worktree"},
		{"outside, elsewhere", "/etc/hosts", errOutsideWorktree, "not opening /etc/hosts: outside the worktree"},
		{"missing", root + "/docs/gone.md", &fs.PathError{Op: "stat", Path: "docs/gone.md", Err: fs.ErrNotExist}, "not opening docs/gone.md: no such file"},
		{"a folder", root + "/docs", errIsFolder, "not opening docs: it is a folder"},
		{"another reason", root + "/link.md", &fs.PathError{Op: "statat", Path: "link.md", Err: errors.New("path escapes from parent")}, "not opening link.md: path escapes from parent"},
		{"a control character", root + "/a\x1b[31m.md", errIsFolder, "not opening a[31m.md: it is a folder"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if got := markdownNotice(root, tt.path, home, tt.err); got != tt.want {
				t.Errorf("notice = %q, want %q", got, tt.want)
			}
		})
	}
}

const pageDoc = `# Alpha

intro text

## Beta

- item one
- item two

<details>
<summary>More</summary>

hidden text

</details>

## Gamma

last words
`

// pageRig is a drawer on a repository with markdown files: README.md, docs/a.md
// and an ignored notes/b.md.
type pageRig struct {
	*drawerRig
	v    *reviewView
	dir  string
	root string
	st   *starts
}

// newPageRig opens a drawer on a fake git's repository; extra adds files to the worktree.
func newPageRig(t *testing.T, extra map[string]string, opts ...func(*Options)) *pageRig {
	t.Helper()

	f := newFakeGit(t)
	dir := fakePageRepo(t, f, extra).Root()
	m := bareRigAt(t, dir, dir, append([]func(*Options){gitOf(f)}, opts...)...)
	m.v = m.opened(t)

	return m
}

// newRealPageRig is newPageRig on real git.
func newRealPageRig(t *testing.T, extra map[string]string, opts ...func(*Options)) *pageRig {
	t.Helper()

	return openPageRig(t, pageRepo(t, extra), opts...)
}

// fakePageRepo is pageRepo on a fake git.
func fakePageRepo(t *testing.T, f *fakeGit, extra map[string]string) *fakeRepo {
	t.Helper()

	r := f.repo(t, "")
	dir := r.Root()
	write(t, filepath.Join(dir, "README.md"), "# Readme\n\nhello there\n")
	write(t, filepath.Join(dir, "docs/a.md"), pageDoc)
	write(t, filepath.Join(dir, ".gitignore"), "notes/\n")
	write(t, filepath.Join(dir, "main.go"), numberedLines("line", 40))
	r.ignore("notes/")
	r.commit("init")
	write(t, filepath.Join(dir, "notes/b.md"), "# Ignored\n")
	write(t, filepath.Join(dir, "main.go"), editedLines(40, 3, 30))

	for name, text := range extra {
		write(t, filepath.Join(dir, name), text)
	}

	return r
}

// pageRepo is the page rig's repository: README.md, docs/a.md and an ignored
// notes/b.md, with main.go edited; extra adds files to the worktree.
func pageRepo(t *testing.T, extra map[string]string) string {
	t.Helper()

	dir := newRepoDir(t)
	write(t, filepath.Join(dir, "README.md"), "# Readme\n\nhello there\n")
	write(t, filepath.Join(dir, "docs/a.md"), pageDoc)
	write(t, filepath.Join(dir, ".gitignore"), "notes/\n")
	write(t, filepath.Join(dir, "main.go"), numberedLines("line", 40))
	commitEverything(t, dir, "init")
	write(t, filepath.Join(dir, "notes/b.md"), "# Ignored\n")
	write(t, filepath.Join(dir, "main.go"), editedLines(40, 3, 30))

	for name, text := range extra {
		write(t, filepath.Join(dir, name), text)
	}

	return dir
}

func openPageRig(t *testing.T, dir string, opts ...func(*Options)) *pageRig {
	t.Helper()

	m := bareRig(t, dir, opts...)
	m.v = m.opened(t)

	return m
}

// bareRig is the page rig's app on the repository in dir, with no drawer.
func bareRig(t *testing.T, dir string, opts ...func(*Options)) *pageRig {
	t.Helper()

	return bareRigAt(t, dir, rootOf(t, dir), opts...)
}

// bareRigAt is bareRig with the repository's root given.
func bareRigAt(t *testing.T, dir, root string, opts ...func(*Options)) *pageRig {
	t.Helper()

	r := newKeysRig(t, dir, append([]func(*Options){func(o *Options) {
		o.Getenv = func(k string) string {
			return map[string]string{"SHELL": "/bin/testsh", "EDITOR": "ed", "HOME": "/home/test"}[k]
		}
	}}, opts...)...)
	st := &starts{}
	r.start = st.start
	r.icons = true

	return &pageRig{drawerRig: r, dir: dir, st: st, root: root}
}

// flow feeds the messages of cmd, and of what Update returns for them, to Update,
// except those hold names, which it hands back unsent.
func (r *drawerRig) flow(cmd tea.Cmd, hold func(tea.Msg) bool) (held []tea.Msg) {
	queue := flatten(cmd)

	for len(queue) > 0 {
		m := queue[0]
		queue = queue[1:]

		switch {
		case m == nil:
		case hold != nil && hold(m):
			held = append(held, m)
		default:
			queue = append(queue, flatten(r.step(m))...)
		}
	}

	return held
}

func holdPages(m tea.Msg) bool {
	_, ok := m.(mdPageMsg)

	return ok
}

func (r *drawerRig) tabID() core.TabID { return r.store.State().Tab().ID }

// typeKeys presses a rune for each character of s.
func (r *drawerRig) typeKeys(s string) {
	for _, c := range s {
		r.press(keyRune(c))
	}
}

// openPage pushes rel as a page.
func (m *pageRig) openPage(t *testing.T, rel string) *mdPage {
	t.Helper()

	m.flow(m.openMarkdownPage(m.tabID(), filepath.Join(m.root, rel)), nil)

	p := m.v.page()
	if p == nil || p.rel != rel {
		t.Fatalf("page = %+v, want %s (notice %q)", p, rel, m.notice)
	}

	return p
}

// overlayIDs are the ids of the items in the open palette.
func overlayIDs(a *App) []string {
	var out []string

	if a.overlay != nil {
		for _, it := range a.overlay.items {
			out = append(out, it.ID)
		}
	}

	return out
}

// pageLine is the layout line of the page that holds sub.
func pageLine(t *testing.T, p *mdPage, sub string) int {
	t.Helper()

	p.view.layout()

	for i, l := range p.view.doc.text() {
		if strings.Contains(l, sub) {
			return i
		}
	}

	t.Fatalf("no line holds %q", sub)

	return 0
}

func TestMarkdownPageLate(t *testing.T) {
	t.Parallel()

	for _, tt := range []struct {
		name         string
		after, undo  func(m *pageRig)
		wantKeyboard bool
	}{
		{"a page read after the compose box opened", func(m *pageRig) { m.compose = &composeView{} }, func(m *pageRig) { m.compose = nil }, true},
		{"a page read after esc to the panes", func(m *pageRig) { m.drawerRelease() }, nil, false},
		{"a page read after another tab opened", func(m *pageRig) {
			if err := m.dispatch(core.NewTab{Kind: core.KindShell, Cwd: "/w/c"}); err != nil {
				t.Fatal(err)
			}
			// A focused drawer on the new tab must not make the old tab's answer count.
			m.drawer.focused = true
		}, nil, false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			m := newRealPageRig(t, nil)
			shown := m.openPage(t, "README.md")
			held := m.flow(m.openMarkdownPage(m.tabID(), filepath.Join(m.root, "docs/a.md")), holdPages)

			tt.after(m)
			m.flow(m.step(held[0]), nil)

			if len(m.v.pages) != 1 || m.v.page() != shown || m.drawer.focused != tt.wantKeyboard {
				t.Errorf("pages = %v, keyboard %v, want the stack unchanged and the keyboard %v", m.v.pages, m.drawer.focused, tt.wantKeyboard)
			}

			if tt.undo != nil {
				tt.undo(m)
			}
		})
	}

	t.Run("a page read after b", func(t *testing.T) {
		t.Parallel()

		m := newRealPageRig(t, nil)
		held := m.flow(m.openMarkdownPage(m.tabID(), filepath.Join(m.root, "docs/a.md")), holdPages)

		m.press(keyRune('b'))
		m.flow(m.step(held[0]), nil)

		if len(m.v.pages) != 0 {
			t.Errorf("a page read opened after b: %v", m.v.pages)
		}
	})

	t.Run("a page read after a retarget", func(t *testing.T) {
		t.Parallel()

		m := newRealPageRig(t, nil)
		held := m.flow(m.openMarkdownPage(m.tabID(), filepath.Join(m.root, "docs/a.md")), holdPages)

		m.flow(m.reviewRetarget(m.tabID(), m.v, 0), nil)
		m.flow(m.step(held[0]), nil)

		if len(m.v.pages) != 0 {
			t.Errorf("a page read opened after a retarget: %v", m.v.pages)
		}
	})

	t.Run("a page read after another", func(t *testing.T) {
		t.Parallel()

		m := newRealPageRig(t, nil)
		first := m.flow(m.openMarkdownPage(m.tabID(), filepath.Join(m.root, "docs/a.md")), holdPages)
		second := m.flow(m.openMarkdownPage(m.tabID(), filepath.Join(m.root, "README.md")), holdPages)

		m.flow(m.step(second[0]), nil)
		m.flow(m.step(first[0]), nil)

		if p := m.v.page(); len(m.v.pages) != 1 || p.rel != "README.md" {
			t.Errorf("pages = %v, want README.md only", m.v.pages)
		}
	})
}

func TestMarkdownOpenPageErrors(t *testing.T) {
	t.Parallel()

	m := newRealPageRig(t, nil)
	write(t, filepath.Join(m.dir, "docs/link.md"), "x\n")

	if err := os.Remove(filepath.Join(m.dir, "docs/link.md")); err != nil {
		t.Fatal(err)
	}

	if err := os.Symlink("/etc/hostname", filepath.Join(m.dir, "docs/link.md")); err != nil {
		t.Skipf("no symlinks: %v", err)
	}

	tests := []struct {
		name, path, want string
	}{
		{"a missing file", "docs/gone.md", "not opening docs/gone.md: no such file"},
		{"a folder", "docs", "not opening docs: it is a folder"},
		{"a path out of the worktree", "../x.md", "not opening ~/x.md: outside the worktree"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			path := filepath.Join(m.root, tt.path)
			if strings.HasPrefix(tt.path, "../") {
				path = "/home/test/x.md"
			}

			m.notice = ""
			m.flow(m.openMarkdownPage(m.tabID(), path), nil)

			if m.notice != tt.want || len(m.v.pages) != 0 {
				t.Errorf("notice = %q, pages %d, want %q and none", m.notice, len(m.v.pages), tt.want)
			}
		})
	}

	t.Run("a symlink out of the worktree", func(t *testing.T) {
		m.notice = ""
		m.flow(m.openMarkdownPage(m.tabID(), filepath.Join(m.root, "docs/link.md")), nil)

		if !strings.HasPrefix(m.notice, "not opening docs/link.md: ") || len(m.v.pages) != 0 {
			t.Errorf("notice = %q, pages %d, want a refusal and no page", m.notice, len(m.v.pages))
		}
	})
}

func TestMarkdownPageKeys(t *testing.T) {
	t.Parallel()

	long := new(strings.Builder)
	for i := range 120 {
		fmt.Fprintf(long, "paragraph %d\n\n", i)
	}

	m := newRealPageRig(t, map[string]string{"docs/long.md": long.String()})

	type snap struct {
		pages, rows, cursor, lines int
		split, tree                bool
		fullOn                     int
		list                       bool
	}

	snapOf := func() snap {
		p := m.v.page()

		return snap{len(m.v.pages), len(m.v.rows), p.view.cursor, len(p.view.layout().Lines), m.v.split, m.drawer.tree, len(m.v.fullOn), m.drawer.list != nil}
	}

	t.Run("lines", func(t *testing.T) {
		m.dropPages(m.v)

		p := m.openPage(t, "docs/a.md")

		m.press(keyRune('j'))
		m.press(keyRune('j'))

		if p.view.cursor != 2 {
			t.Errorf("after j j: cursor = %d, want 2", p.view.cursor)
		}

		m.press(keyRune('k'))
		m.press(tea.KeyPressMsg{Code: tea.KeyDown})
		m.press(tea.KeyPressMsg{Code: tea.KeyUp})

		if p.view.cursor != 1 {
			t.Errorf("after k down up: cursor = %d, want 1", p.view.cursor)
		}
	})

	t.Run("headings", func(t *testing.T) {
		m.dropPages(m.v)

		p := m.openPage(t, "docs/a.md")
		beta, gamma := pageLine(t, p, "Beta"), pageLine(t, p, "Gamma")

		m.press(keyRune(']'))

		if p.view.cursor != beta {
			t.Errorf("] from the top: cursor = %d, want Beta's %d", p.view.cursor, beta)
		}

		m.press(keyRune(']'))

		if p.view.cursor != gamma {
			t.Errorf("] again: cursor = %d, want Gamma's %d", p.view.cursor, gamma)
		}

		m.press(keyRune('['))

		if p.view.cursor != beta {
			t.Errorf("[: cursor = %d, want Beta's %d", p.view.cursor, beta)
		}
	})

	t.Run("pages and ends", func(t *testing.T) {
		m.dropPages(m.v)

		p := m.openPage(t, "docs/long.md")
		last := len(p.view.layout().Lines) - 1
		half := p.view.h / 2

		if last < p.view.h*2 {
			t.Fatalf("the long page has %d lines for a window of %d", last+1, p.view.h)
		}

		m.press(tea.KeyPressMsg{Code: 'd', Mod: tea.ModCtrl})

		if p.view.cursor != half {
			t.Errorf("ctrl+d: cursor = %d, want half a window, %d", p.view.cursor, half)
		}

		m.press(tea.KeyPressMsg{Code: 'u', Mod: tea.ModCtrl})

		if p.view.cursor != 0 {
			t.Errorf("ctrl+u: cursor = %d, want 0", p.view.cursor)
		}

		m.press(keyRune('G'))

		if p.view.cursor != last {
			t.Errorf("G: cursor = %d, want %d", p.view.cursor, last)
		}

		m.press(keyRune('g'))

		if p.view.cursor != 0 {
			t.Errorf("g: cursor = %d, want 0", p.view.cursor)
		}
	})

	t.Run("enter on a fold head and elsewhere", func(t *testing.T) {
		m.dropPages(m.v)

		p := m.openPage(t, "docs/a.md")
		before := len(p.view.layout().Lines)

		m.press(keyEnter)

		if got := len(p.view.layout().Lines); got != before || len(m.v.pages) != 1 {
			t.Errorf("enter off a fold: %d lines (was %d), %d pages, want no change", got, before, len(m.v.pages))
		}

		folds := p.view.layout().Folds
		if len(folds) == 0 {
			t.Fatal("the page has no fold")
		}

		p.view.cursor = folds[0].Line
		p.view.clamp()
		m.press(keyEnter)

		if got := len(p.view.layout().Lines); got == before {
			t.Errorf("enter on the fold head left %d lines", got)
		}

		m.press(keyEnter)

		if got := len(p.view.layout().Lines); got != before {
			t.Errorf("enter on the fold head again: %d lines, want %d", got, before)
		}
	})

	t.Run("search", func(t *testing.T) {
		m.dropPages(m.v)

		p := m.openPage(t, "docs/a.md")

		m.press(keyRune('/'))
		m.typeKeys("gamma")
		m.press(keyEnter)

		if want := pageLine(t, p, "Gamma"); p.view.cursor != want || m.v.search != nil {
			t.Errorf("after /gamma: cursor = %d, search open = %v, want %d and closed", p.view.cursor, m.v.search != nil, want)
		}

		m.press(keyRune('/'))
		m.typeKeys("zzz")
		m.notice = ""
		m.press(keyEnter)

		if m.notice != "not found: zzz" {
			t.Errorf("notice = %q, want not found: zzz", m.notice)
		}
	})

	t.Run("n and N", func(t *testing.T) {
		m.dropPages(m.v)

		p := m.openPage(t, "docs/a.md")
		p.view.layout()

		var hits []int

		for i, l := range p.view.doc.text() {
			if strings.Contains(strings.ToLower(l), "a") {
				hits = append(hits, i)
			}
		}

		if len(hits) < 3 {
			t.Fatalf("lines %v hold a, want at least 3", hits)
		}

		m.press(keyRune('/'))
		m.typeKeys("a")
		m.press(keyEnter)

		j := slices.Index(hits, p.view.cursor)
		if j < 0 {
			t.Fatalf("/a: cursor = %d, want one of the matches %v", p.view.cursor, hits)
		}

		for _, step := range []struct {
			key rune
			to  int
		}{{'n', j + 1}, {'N', j}, {'N', j - 1}} {
			m.press(keyRune(step.key))

			if want := hits[(step.to+len(hits))%len(hits)]; p.view.cursor != want {
				t.Errorf("%c: cursor = %d, want %d of the matches %v", step.key, p.view.cursor, want, hits)
			}
		}
	})

	t.Run("r reads the file again", func(t *testing.T) {
		m.dropPages(m.v)

		p := m.openPage(t, "docs/a.md")
		write(t, filepath.Join(m.dir, "docs/a.md"), strings.Replace(pageDoc, "intro text", "brand new intro", 1))

		m.press(keyRune('r'))

		if got := strings.Join(p.view.doc.text(), "\n"); !strings.Contains(got, "brand new intro") {
			t.Errorf("the page after r:\n%s\nwant the new text", got)
		}

		write(t, filepath.Join(m.dir, "docs/a.md"), pageDoc)
	})

	t.Run("other keys are dropped", func(t *testing.T) {
		m.dropPages(m.v)

		m.openPage(t, "docs/a.md")

		before := snapOf()

		for _, k := range []tea.KeyPressMsg{keyRune('f'), keyRune('u'), {Code: tea.KeyTab}, keyRune('w'), keyRune('m'), keyRune('c'), keyRune('}'), keyRune(','), keyRune('x')} {
			m.notice = ""
			m.press(k)

			if got := snapOf(); got != before || m.notice != "" {
				t.Errorf("%s: state %+v (was %+v), notice %q, want it dropped", k.String(), got, before, m.notice)
			}
		}
	})
}

func TestMarkdownPageStack(t *testing.T) {
	t.Parallel()

	m := newRealPageRig(t, nil)
	m.v.cursor = 3
	diffCursor := m.v.cursor

	first := m.openPage(t, "docs/a.md")
	second := m.openPage(t, "README.md")

	if len(m.v.pages) != 2 || m.v.page() != second || m.v.pages[0] != first {
		t.Fatalf("pages = %v, want a.md under README.md", m.v.pages)
	}

	first.view.cursor = 2

	m.press(keyEsc)

	if len(m.v.pages) != 1 || m.v.page() != first || first.view.cursor != 2 || !m.drawer.focused {
		t.Fatalf("after esc: %d pages, top %v, drawer has the keyboard %v, want a.md alone with its cursor", len(m.v.pages), m.v.page(), m.drawer.focused)
	}

	m.press(keyEsc)

	if len(m.v.pages) != 0 || m.v.cursor != diffCursor || !m.drawer.focused || m.store.State().Tab().Review == nil {
		t.Fatalf("after the second esc: %d pages, diff cursor %d (was %d), keyboard %v, want the diff as it was", len(m.v.pages), m.v.cursor, diffCursor, m.drawer.focused)
	}

	if got := m.drawn(t); !strings.Contains(got, "main.go") {
		t.Errorf("the drawer after the last pop:\n%s\nwant the diff", got)
	}

	for _, key := range []rune{'b', 't', 'p'} {
		t.Run(string(key)+" drops the stack", func(t *testing.T) {
			m.openPage(t, "docs/a.md")
			m.openPage(t, "README.md")

			m.press(keyRune(key))

			if len(m.v.pages) != 0 {
				t.Errorf("%c left %d pages", key, len(m.v.pages))
			}
		})
	}
}

func TestMarkdownPageReload(t *testing.T) {
	t.Parallel()

	// The cursor keeps its source line, so the edit keeps every line where it was.
	newDoc := strings.Replace(pageDoc, "intro text", "added above", 1)

	tests := []struct {
		name  string
		batch git.Batch
		pr    bool
		reads bool
	}{
		{"the page's path", git.Batch{Paths: []string{"docs/a.md"}}, false, true},
		{"another path", git.Batch{Paths: []string{"main.go", "docs/other.md"}}, false, true},
		{"an overflow", git.Batch{Overflow: true}, false, true},
		{"a removed parent", git.Batch{Paths: []string{"docs"}}, false, true},
		{"the page's path in pull request mode", git.Batch{Paths: []string{"docs/a.md"}}, true, true},
		{"an overflow in pull request mode", git.Batch{Overflow: true}, true, true},
		{"a head change only", git.Batch{Git: true}, false, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			m := newRealPageRig(t, nil)
			p := m.openPage(t, "docs/a.md")
			p.view.cursor = pageLine(t, p, "Gamma")
			p.view.clamp()

			old := p.view.doc

			write(t, filepath.Join(m.dir, "docs/a.md"), newDoc)

			if tt.pr {
				m.v.pr = core.ReviewPR{Repo: "o/r", Number: 7, Root: m.root}
			}

			cmd := m.batch(m.root, tt.batch)

			m.v.pr = core.ReviewPR{}

			var reloads []tea.Msg

			for _, msg := range flatten(cmd) {
				if pm, ok := msg.(mdPageMsg); ok && pm.reload {
					reloads = append(reloads, msg)
				}
			}

			if got := len(reloads) > 0; got != tt.reads {
				t.Fatalf("the batch read the page again = %v, want %v", got, tt.reads)
			}

			for _, msg := range reloads {
				m.flow(m.step(msg), nil)
			}

			changed := p.view.doc != old

			if changed != tt.reads {
				t.Fatalf("the page's doc changed = %v, want %v", changed, tt.reads)
			}

			if !tt.reads {
				return
			}

			text := strings.Join(p.view.doc.text(), "\n")
			if !strings.Contains(text, "added above") {
				t.Errorf("the page lacks the new text:\n%s", text)
			}

			if line := p.view.doc.text()[p.view.cursor]; !strings.Contains(line, "Gamma") {
				t.Errorf("the cursor is on %q, want it on Gamma", line)
			}
		})
	}

	t.Run("a batch that finds the file as it was keeps the doc", func(t *testing.T) {
		t.Parallel()

		m := newRealPageRig(t, nil)
		p := m.openPage(t, "docs/a.md")
		old := p.view.doc

		m.flow(m.batch(m.root, git.Batch{Paths: []string{"main.go"}}), nil)

		if p.view.doc != old {
			t.Error("the page was parsed again though its file did not change")
		}
	})

	t.Run("a page of an ignored file follows its file", func(t *testing.T) {
		t.Parallel()

		m := newRealPageRig(t, nil)
		p := m.openPage(t, "notes/b.md")

		write(t, filepath.Join(m.dir, "notes/b.md"), "# Ignored\n\nfirst\n\nsecond\n")
		p.view.goSource(5)

		// The watcher never names an ignored file; a batch for another path comes.
		m.flow(m.batch(m.root, git.Batch{Paths: []string{"main.go"}}), nil)

		if text := strings.Join(p.view.doc.text(), "\n"); !strings.Contains(text, "second") {
			t.Fatalf("the page lacks the new text:\n%s", text)
		}

		write(t, filepath.Join(m.dir, "notes/b.md"), "# Ignored\n\nadded\n\nfirst\n\nsecond\n")
		p.view.goSource(5)
		m.flow(m.batch(m.root, git.Batch{Paths: []string{"main.go"}}), nil)

		if got := p.view.sourceLine(); got != 5 {
			t.Errorf("the cursor is on source line %d, want 5", got)
		}
	})

	t.Run("a page under another reads again when it shows", func(t *testing.T) {
		t.Parallel()

		m := newRealPageRig(t, nil)
		low := m.openPage(t, "docs/a.md")
		m.openPage(t, "README.md")

		old := low.view.doc

		write(t, filepath.Join(m.dir, "docs/a.md"), newDoc)

		for _, read := range m.flow(m.batch(m.root, git.Batch{Paths: []string{"docs/a.md"}}), holdPages) {
			if pm := read.(mdPageMsg); pm.path == low.path {
				t.Fatalf("a covered page was read again at once: %v", pm)
			}
		}

		m.press(keyEsc)

		if m.v.page() != low || low.view.doc == old {
			t.Errorf("the page under was not read again once it showed (doc changed %v)", low.view.doc != old)
		}
	})

	t.Run("an answer for a page that is gone", func(t *testing.T) {
		t.Parallel()

		m := newRealPageRig(t, nil)
		m.openPage(t, "docs/a.md")

		held := m.flow(m.batch(m.root, git.Batch{Paths: []string{"docs/a.md"}}), holdPages)
		if len(held) != 1 {
			t.Fatalf("%d reads held, want 1", len(held))
		}

		m.press(keyEsc)
		m.flow(m.step(held[0]), nil)

		if len(m.v.pages) != 0 {
			t.Errorf("a late reload brought back a page: %v", m.v.pages)
		}
	})
}

func TestMarkdownPageDropped(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		drop func(m *pageRig) *reviewView // the view that should hold no page after
	}{
		{"a retarget", func(m *pageRig) *reviewView {
			m.flow(m.reviewRetarget(m.tabID(), m.v, 0), nil)

			return m.v
		}},
		{"reviewReset", func(m *pageRig) *reviewView {
			return m.reviewReset(m.tabID(), m.v.pane)
		}},
		{"closing the drawer", func(m *pageRig) *reviewView {
			m.closeDrawer(false)

			return m.v
		}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			m := newRealPageRig(t, nil)
			m.openPage(t, "docs/a.md")
			m.openPage(t, "README.md")

			if v := tt.drop(m); len(v.pages) != 0 {
				t.Errorf("%d pages left", len(v.pages))
			}
		})
	}
}

func TestMarkdownPageMouse(t *testing.T) {
	t.Parallel()

	long := new(strings.Builder)
	for i := range 120 {
		fmt.Fprintf(long, "paragraph %d\n\n", i)
	}

	m := newRealPageRig(t, map[string]string{"docs/long.md": long.String()})
	p := m.openPage(t, "docs/long.md")
	rect := mustRect(t, m.drawerRig)

	click := func(row int) {
		clickRun(m.App, rect.X+6, rect.Y+1+row)
	}

	click(4)

	if p.view.cursor != 4 {
		t.Errorf("a click on row 4: cursor = %d, want 4", p.view.cursor)
	}

	wheel := func(b tea.MouseButton) {
		m.Update(tea.MouseWheelMsg{X: rect.X + 6, Y: rect.Y + 5, Button: b})
	}

	wheel(tea.MouseWheelDown)

	if p.view.top != wheelRows {
		t.Errorf("a wheel notch down: top = %d, want %d", p.view.top, wheelRows)
	}

	wheel(tea.MouseWheelDown)
	wheel(tea.MouseWheelUp)

	if p.view.top != wheelRows {
		t.Errorf("down, down, up: top = %d, want %d", p.view.top, wheelRows)
	}

	if p.view.cursor < p.view.top || p.view.cursor >= p.view.top+p.view.h {
		t.Errorf("the cursor %d left the window at %d", p.view.cursor, p.view.top)
	}

	click(2)

	if want := p.view.top + 2; p.view.cursor != want {
		t.Errorf("a click on row 2 of a scrolled page: cursor = %d, want %d", p.view.cursor, want)
	}

	if m.drawer.tree || len(m.v.pages) != 1 {
		t.Error("the mouse changed more than the page")
	}
}

func TestMarkdownPageEdit(t *testing.T) {
	t.Parallel()

	m := newRealPageRig(t, nil)
	p := m.openPage(t, "docs/a.md")
	p.view.cursor = pageLine(t, p, "Beta")
	p.view.clamp()

	m.keys(keyRune('e'))

	if m.st.count() != 1 {
		t.Fatalf("e started %d processes, want 1", m.st.count())
	}

	argv := m.st.cmds[0].Argv
	if got, want := argv[len(argv)-2:], []string{"+5", filepath.Join(m.root, "docs", "a.md")}; !slices.Equal(got, want) {
		t.Errorf("the editor got %q, want %q", got, want)
	}

	if m.store.State().Tab().Review != nil || m.drawer.focused || len(m.v.pages) != 0 {
		t.Error("e left the drawer open")
	}
}

// mdPageScene is a drawer on a branch with a markdown page over its diff.
func mdPageScene(th string, icons bool) reviewScene {
	return reviewScene{
		theme: th, icons: icons, w: 200, h: 52, branch: true, focused: true, res: mdResult(),
		tune: func(t *testing.T, a *App, v *reviewView) {
			t.Helper()

			doc := parseMarkdown([]byte(planWith(nil)), "", nil)
			p := &mdPage{path: "/w/p1/" + planPath, rel: planPath, view: mdView{doc: doc}}

			v.pages = []*mdPage{p}
			p.resize(120, 40, icons)
			p.view.cursor = pageLine(t, p, "Steps")
		},
	}
}

func TestMarkdownPagePushes(t *testing.T) {
	t.Parallel()

	m := newRealPageRig(t, nil)

	first := m.openPage(t, "docs/a.md")
	second := m.openPage(t, "README.md")

	if len(m.v.pages) != 2 || m.v.page() != second || m.v.pages[0] != first {
		t.Fatalf("pages = %v, want a.md under README.md", m.v.pages)
	}

	m.press(keyEsc)

	if len(m.v.pages) != 1 || m.v.page() != first {
		t.Fatalf("after esc: %d pages, top %v, want a.md alone", len(m.v.pages), m.v.page())
	}

	m.press(keyEsc)

	if len(m.v.pages) != 0 || m.store.State().Tab().Review == nil {
		t.Fatalf("after the second esc: %d pages, want the diff", len(m.v.pages))
	}
}
