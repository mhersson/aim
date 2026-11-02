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
	"github.com/mhersson/aim/internal/ui/theme"
)

// linkDoc is docs/a.md of the link rig: a line with no link, one with a link,
// one with three targets, a link to another page and a fold head.
const linkDoc = `# Links

plain words only

one [site](https://example.com/a) here

[first](https://example.com/1) then [second](b.md) then [third](code.go)

[the b page](b.md)

<details>
<summary>More https://example.com/f</summary>

hidden text

</details>
`

// linkRig is the page rig with docs/a.md changed to linkDoc, so that it shows in
// the diff, and the files its links lead to.
type linkRig struct {
	*pageRig
	open *fakeOpen
}

func newLinkRig(t *testing.T) *linkRig {
	t.Helper()

	m := newRealPageRig(t, map[string]string{
		"docs/a.md":    linkDoc,
		"docs/b.md":    "# Bee\n\nbee words\n",
		"docs/code.go": "package docs\n",
	})
	f := &fakeOpen{}
	m.openLink = f.open

	return &linkRig{pageRig: m, open: f}
}

// render shows docs/a.md rendered in the diff and puts the cursor on the row holding sub.
func (m *linkRig) render(t *testing.T, sub string) {
	t.Helper()

	for i, row := range m.v.rows {
		if row.kind == rowFile && m.v.res.Files[row.file].Path == "docs/a.md" {
			m.v.cursor = i

			break
		}
	}

	m.press(keyRune('m'))

	if !m.v.mdOn["docs/a.md"] {
		t.Fatalf("docs/a.md is not rendered (notice %q)", m.notice)
	}

	m.v.cursor = rowWith(t, m.v, sub)
}

// onPageLine opens docs/a.md as a page with the cursor on the line holding sub.
func (m *linkRig) onPageLine(t *testing.T, sub string) *mdPage {
	t.Helper()

	m.dropPages(m.v)

	p := m.openPage(t, "docs/a.md")
	p.view.cursor = pageLine(t, p, sub)
	p.view.clamp()

	return p
}

func (m *linkRig) chooserTexts() []string {
	c := m.drawer.links
	if c == nil {
		return nil
	}

	var out []string
	for _, t := range c.targets {
		out = append(out, t.Text)
	}

	return out
}

// chooser opens the chooser on the three targets of the page's third line.
func (m *linkRig) chooser(t *testing.T) *linkChooser {
	t.Helper()

	m.onPageLine(t, "first then second")
	m.press(keyEnter)

	c := m.drawer.links
	if c == nil {
		t.Fatalf("enter on three targets opened no chooser (notice %q)", m.notice)
	}

	return c
}

func TestFollowTarget(t *testing.T) {
	t.Parallel()

	// want is what following a target does.
	type want struct {
		url    string // the URL the opener gets
		page   string // the path openMD gets
		editor string // the file the editor opens
		notice string
	}

	// link makes a symlink named name in the worktree's docs folder.
	link := func(name, to string) func(t *testing.T, root string) {
		return func(t *testing.T, root string) {
			t.Helper()

			if to == "" {
				to = filepath.Join(t.TempDir(), "outside"+filepath.Ext(name))
				write(t, to, "outside\n")
			} else if !filepath.IsAbs(to) {
				to = filepath.Join(root, "docs", to)
			}

			if err := os.Symlink(to, filepath.Join(root, "docs", name)); err != nil {
				t.Skipf("no symlinks: %v", err)
			}
		}
	}

	tests := []struct {
		name  string
		setup func(t *testing.T, root string)
		do    func(root string) (mdTarget, want)
	}{
		{"a markdown symlink to a file outside", link("out.md", ""), func(root string) (mdTarget, want) {
			// openMD is reached; the read through os.Root refuses the link (TestReadLocalMarkdown).
			p := filepath.Join(root, "docs", "out.md")

			return mdTarget{Path: p}, want{page: p}
		}},
		{"a symlink to a file outside", link("out.go", ""), func(root string) (mdTarget, want) {
			p := filepath.Join(root, "docs", "out.go")

			return mdTarget{Path: p}, want{notice: "not opening " + p + ": outside the worktree"}
		}},
		{"a symlink to a file inside", link("in.go", "code.go"), func(root string) (mdTarget, want) {
			p := filepath.Join(root, "docs", "in.go")

			return mdTarget{Path: p}, want{editor: p}
		}},
		{"a file that is missing", nil, func(root string) (mdTarget, want) {
			return mdTarget{Path: filepath.Join(root, "docs", "nope.go")}, want{notice: "not opening docs/nope.go: no such file"}
		}},
		{"a folder", nil, func(root string) (mdTarget, want) {
			return mdTarget{Path: filepath.Join(root, "docs")}, want{notice: "not opening docs: it is a folder"}
		}},
		{"a symlink to a folder", func(t *testing.T, root string) {
			t.Helper()

			if err := os.Symlink(filepath.Join(root, "docs"), filepath.Join(root, "alias")); err != nil {
				t.Skipf("no symlinks: %v", err)
			}
		}, func(root string) (mdTarget, want) {
			return mdTarget{Path: filepath.Join(root, "alias")}, want{notice: "not opening alias: it is a folder"}
		}},
		{"an https URL", nil, func(string) (mdTarget, want) {
			return mdTarget{URL: "https://example.com/x"}, want{url: "https://example.com/x"}
		}},
		{"an http URL", nil, func(string) (mdTarget, want) {
			return mdTarget{URL: "http://example.com/y"}, want{url: "http://example.com/y"}
		}},
		{"a URL that is not a web page", nil, func(string) (mdTarget, want) {
			return mdTarget{URL: "javascript:alert(1)"}, want{notice: noticeLinkRefused}
		}},
		{"a markdown file inside", nil, func(root string) (mdTarget, want) {
			return mdTarget{Path: filepath.Join(root, "docs", "..", "docs", "b.md")}, want{page: filepath.Join(root, "docs", "b.md")}
		}},
		{"a file with another extension inside", nil, func(root string) (mdTarget, want) {
			p := filepath.Join(root, "docs", "code.go")

			return mdTarget{Path: p}, want{editor: p}
		}},
		{"a markdown file above the root", nil, func(root string) (mdTarget, want) {
			p := filepath.Join(filepath.Dir(root), "x.md")

			return mdTarget{Path: p}, want{notice: "not opening " + p + ": outside the worktree"}
		}},
		{"a file elsewhere", nil, func(string) (mdTarget, want) {
			return mdTarget{Path: "/etc/hosts"}, want{notice: "not opening /etc/hosts: outside the worktree"}
		}},
		{"a file of a sibling worktree", nil, func(root string) (mdTarget, want) {
			p := filepath.Join(filepath.Dir(root), "sibling-worktree", "a.md")

			return mdTarget{Path: p}, want{notice: "not opening " + p + ": outside the worktree"}
		}},
		{"a folder that only starts like the root", nil, func(root string) (mdTarget, want) {
			p := root + "-more/a.md"

			return mdTarget{Path: p}, want{notice: "not opening " + p + ": outside the worktree"}
		}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			m := newLinkRig(t)
			if tt.setup != nil {
				tt.setup(t, m.root)
			}

			target, w := tt.do(m.root)

			var pages []string

			cmd := m.followTarget(m.root, target, func(p string) tea.Cmd {
				pages = append(pages, p)

				return nil
			})

			// A browser open answers nothing; a file's resolution answers a message
			// that opens the editor's pane, which the rig's pump runs.
			if cmd != nil {
				if msg, ok := cmd().(linkEditMsg); ok {
					m.run(m.step(msg))
				}
			}

			var wantURLs, wantPages []string
			if w.url != "" {
				wantURLs = []string{w.url}
			}

			if w.page != "" {
				wantPages = []string{w.page}
			}

			if got := m.open.urls(); !slices.Equal(got, wantURLs) {
				t.Errorf("the opener got %q, want %q", got, wantURLs)
			}

			if !slices.Equal(pages, wantPages) {
				t.Errorf("openMD got %q, want %q", pages, wantPages)
			}

			wantEditors := 0
			if w.editor != "" {
				wantEditors = 1
			}

			if got := m.st.count(); got != wantEditors {
				t.Fatalf("%d editors started, want %d", got, wantEditors)
			}

			if w.editor != "" {
				argv := m.st.cmds[0].Argv
				if got, want := argv[len(argv)-2:], []string{"+1", w.editor}; !slices.Equal(got, want) {
					t.Errorf("the editor got %q, want %q", got, want)
				}
			}

			if m.notice != w.notice {
				t.Errorf("notice = %q, want %q", m.notice, w.notice)
			}
		})
	}
}

func TestEnterOnLinks(t *testing.T) {
	t.Parallel()

	type view struct {
		name string
		at   func(t *testing.T, m *linkRig, sub string)
		// lines is how many lines the view shows, which a fold changes.
		lines func(m *linkRig) int
	}

	views := []view{
		{
			"a rendered row",
			func(t *testing.T, m *linkRig, sub string) { m.render(t, sub) },
			func(m *linkRig) int { return len(m.v.rows) },
		},
		{
			"a page",
			func(t *testing.T, m *linkRig, sub string) { m.onPageLine(t, sub) },
			func(m *linkRig) int { return len(m.v.page().view.layout().Lines) },
		},
	}

	tests := []struct {
		name    string
		sub     string
		urls    []string
		chooser []string
		folds   bool // the fold toggles
	}{
		{name: "no target", sub: "plain words only"},
		{name: "one target is followed", sub: "one site here", urls: []string{"https://example.com/a"}},
		{name: "a fold head", sub: "More", folds: true},
		{name: "three targets open the chooser", sub: "first then second", chooser: []string{"first", "second", "third"}},
	}

	for _, vw := range views {
		for _, tt := range tests {
			t.Run(vw.name+"/"+tt.name, func(t *testing.T) {
				t.Parallel()

				m := newLinkRig(t)
				vw.at(t, m, tt.sub)

				before := vw.lines(m)

				m.press(keyEnter)

				if got := m.open.urls(); !slices.Equal(got, tt.urls) {
					t.Errorf("the opener got %q, want %q", got, tt.urls)
				}

				if got := m.chooserTexts(); !slices.Equal(got, tt.chooser) {
					t.Errorf("chooser lists %q, want %q", got, tt.chooser)
				}

				if changed := vw.lines(m) != before; changed != tt.folds {
					t.Errorf("fold toggled = %v (%d lines, was %d), want %v", changed, vw.lines(m), before, tt.folds)
				}

				if m.st.count() != 0 || m.notice != "" {
					t.Errorf("%d editors started, notice %q, want neither", m.st.count(), m.notice)
				}
			})
		}
	}
}

func TestLinkChooserKeys(t *testing.T) {
	t.Parallel()

	t.Run("the chooser", func(t *testing.T) {
		t.Parallel()

		c := &linkChooser{targets: []mdTarget{{Text: "a"}, {Text: "b"}, {Text: "c"}}}

		steps := []struct {
			key  string
			sel  int
			pick string
			done bool
		}{
			{"k", 0, "", false},
			{"j", 1, "", false},
			{"down", 2, "", false},
			{"j", 2, "", false},
			{"up", 1, "", false},
			{"x", 1, "", false},
			{"tab", 1, "", false},
			{"enter", 1, "b", true},
			{"esc", 1, "", true},
		}

		for _, s := range steps {
			pick, done := c.key(s.key)

			got := ""
			if pick != nil {
				got = pick.Text
			}

			if c.sel != s.sel || got != s.pick || done != s.done {
				t.Errorf("%q: sel %d, pick %q, done %v; want %d, %q, %v", s.key, c.sel, got, done, s.sel, s.pick, s.done)
			}
		}
	})

	t.Run("in the drawer", func(t *testing.T) {
		t.Parallel()

		m := newLinkRig(t)
		c := m.chooser(t)
		rect := mustRect(t, m.drawerRig)

		m.press(keyRune('j'), keyRune('j'), keyRune('j'))

		if c.sel != 2 {
			t.Errorf("j j j: sel = %d, want 2 (the end)", c.sel)
		}

		m.press(keyRune('k'))

		if c.sel != 1 {
			t.Errorf("k: sel = %d, want 1", c.sel)
		}

		// Everything else is dropped: keys, a paste and the mouse.
		cursor, pages := m.v.page().view.cursor, len(m.v.pages)

		for _, k := range []tea.KeyPressMsg{keyRune('x'), keyRune('f'), keyRune('/'), keyRune('G'), {Code: tea.KeyTab}, keyRune('e'), keyRune('w')} {
			m.press(k)
		}

		m.Update(tea.PasteMsg{Content: "pasted"})
		m.Update(tea.MouseClickMsg{X: rect.X + 6, Y: rect.Y + 3, Button: tea.MouseLeft})
		m.Update(tea.MouseWheelMsg{X: rect.X + 6, Y: rect.Y + 5, Button: tea.MouseWheelDown})

		if m.drawer.links != c || c.sel != 1 || m.v.page().view.cursor != cursor || len(m.v.pages) != pages || m.v.search != nil || m.st.count() != 0 {
			t.Errorf("the chooser changed: open %v, sel %d, cursor %d (was %d), %d pages (was %d), search %v, %d editors",
				m.drawer.links == c, c.sel, m.v.page().view.cursor, cursor, len(m.v.pages), pages, m.v.search != nil, m.st.count())
		}

		m.press(keyEnter)

		if m.drawer.links != nil {
			t.Error("enter left the chooser open")
		}

		if got := m.open.urls(); got != nil {
			t.Errorf("the second target is a page, not a URL: the opener got %q", got)
		}

		if p := m.v.page(); p == nil || p.rel != "docs/b.md" || len(m.v.pages) != 2 {
			t.Errorf("enter on the second target: pages %v, want docs/b.md pushed", m.v.pages)
		}
	})

	t.Run("enter on a URL and esc", func(t *testing.T) {
		t.Parallel()

		m := newLinkRig(t)
		m.chooser(t)
		m.press(keyEnter)

		if got := m.open.urls(); !slices.Equal(got, []string{"https://example.com/1"}) || m.drawer.links != nil {
			t.Errorf("enter on the first target: opener %q, chooser open %v", got, m.drawer.links != nil)
		}

		m.chooser(t)
		m.press(keyEsc)

		if m.drawer.links != nil || len(m.open.urls()) != 1 || len(m.v.pages) != 1 {
			t.Errorf("esc: chooser open %v, urls %q, %d pages", m.drawer.links != nil, m.open.urls(), len(m.v.pages))
		}

		// The drawer's own keys work again.
		m.press(keyRune('j'))

		if m.v.page().view.cursor == 0 {
			t.Error("j after esc did not move the page")
		}
	})
}

func TestLinkChooserCloses(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		close func(t *testing.T, m *linkRig)
	}{
		{"leader h", func(t *testing.T, m *linkRig) { m.leader('h') }},
		{"leader d", func(t *testing.T, m *linkRig) { m.leader('d') }},
		{"a tab switch", func(t *testing.T, m *linkRig) {
			openIn(t, m.App, core.NewTab{Kind: core.KindShell, Cwd: m.dir})
			m.settle()
		}},
		{"a retarget", func(t *testing.T, m *linkRig) {
			other := m.shell(t, m.dir)
			openIn(t, m.App, core.OpenReview{Pane: other, Mode: core.ReviewBranch})
			m.settle()
		}},
		{"b", func(t *testing.T, m *linkRig) {
			c, ok := m.reg.get("review.mode.branch")
			if !ok {
				t.Fatal("no branch mode command")
			}

			m.run(c.run(m.App))
			m.settle()
		}},
	}

	opens := []struct {
		name string
		open func(t *testing.T, m *linkRig)
	}{
		{"from a page", func(t *testing.T, m *linkRig) { m.chooser(t) }},
		{"from a rendered row", func(t *testing.T, m *linkRig) {
			m.render(t, "first then second")
			m.press(keyEnter)

			if m.drawer.links == nil {
				t.Fatalf("enter on three targets opened no chooser (notice %q)", m.notice)
			}
		}},
	}

	for _, o := range opens {
		for _, tt := range tests {
			t.Run(o.name+"/"+tt.name, func(t *testing.T) {
				t.Parallel()

				m := newLinkRig(t)
				o.open(t, m)

				tt.close(t, m)

				if m.drawer.links != nil {
					t.Fatalf("the chooser is still open after %s", tt.name)
				}

				m.settle()

				if m.drawer.links != nil {
					t.Errorf("the chooser came back after %s", tt.name)
				}
			})
		}
	}
}

// A file that resolves after the drawer closed opens nothing.
func TestFollowMarkdownSymlinkOutside(t *testing.T) {
	t.Parallel()

	m := newLinkRig(t)
	outside := filepath.Join(t.TempDir(), "outside.md")
	write(t, outside, "outside\n")

	if err := os.Symlink(outside, filepath.Join(m.root, "docs", "out.md")); err != nil {
		t.Skipf("no symlinks: %v", err)
	}

	m.notice = ""
	m.flow(m.openMarkdownPage(m.tabID(), filepath.Join(m.root, "docs", "out.md")), nil)

	const want = "not opening docs/out.md: path escapes from parent"
	if m.notice != want || len(m.v.pages) != 0 {
		t.Errorf("notice = %q, pages %d, want %q and none", m.notice, len(m.v.pages), want)
	}
}

func TestFollowTargetLate(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		do   func(m *linkRig)
	}{
		{"the drawer closed", func(m *linkRig) { openIn(t, m.App, core.CloseReview{}) }},
		{"a palette opened", func(m *linkRig) { m.openCommandPalette() }},
		{"the keyboard went back to the panes", func(m *linkRig) { m.drawerRelease() }},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			m := newLinkRig(t)
			cmd := m.followTarget(m.root, mdTarget{Path: filepath.Join(m.root, "docs", "code.go")}, nil)

			msg, ok := cmd().(linkEditMsg)
			if !ok {
				t.Fatal("following a file asked no resolution")
			}

			tt.do(m)
			m.settle()
			m.run(m.step(msg))

			if m.st.count() != 0 {
				t.Errorf("%d editors started after the answer went stale, want none", m.st.count())
			}
		})
	}
}

func TestLinkChooserLines(t *testing.T) {
	t.Parallel()

	ui := theme.UI{}
	c := &linkChooser{
		root: "/w/p1",
		sel:  1,
		targets: []mdTarget{
			{Text: "short", URL: "https://example.com/a"},
			{Text: "a rather long piece of link text that has to be cut", Path: "/w/p1/docs/deeply/nested/folder/of/things/b.md"},
			{Text: "bell\x07 and\x1b[31m escape", URL: "https://example.com/c\x1b[0m"},
			{Path: "/home/me/notes.md"},
		},
	}

	for _, w := range []int{10, 24, 40, 80} {
		lines := c.lines(ui, w, 12, true, "/home/me")
		if len(lines) != len(c.targets)+4 {
			t.Fatalf("w=%d: %d lines, want %d", w, len(lines), len(c.targets)+4)
		}

		for i, l := range lines {
			if got := ansi.StringWidth(l); got != w {
				t.Errorf("w=%d line %d is %d cells wide", w, i, got)
			}

			if strings.ContainsAny(ansi.Strip(l), "\x07\x1b") {
				t.Errorf("w=%d line %d carries a control character: %q", w, i, ansi.Strip(l))
			}
		}
	}

	if got := c.lines(ui, 9, 12, true, ""); got != nil {
		t.Errorf("a box 9 cells wide drew %d lines, want none", len(got))
	}

	if got := c.lines(ui, 40, 4, true, ""); got != nil {
		t.Errorf("a box with no room for a row drew %d lines, want none", len(got))
	}

	// Fewer rows than targets: the selection stays in view.
	c.sel = 3
	lines := c.lines(ui, 40, 6, false, "/home/me")

	if len(lines) != 6 || !strings.Contains(ansi.Strip(strings.Join(lines, "\n")), "~/notes.md") {
		t.Errorf("a short box does not show the selected target:\n%s", ansi.Strip(strings.Join(lines, "\n")))
	}
}

func TestLinkChooserSmall(t *testing.T) {
	t.Parallel()

	targets := []mdTarget{{Text: "one", URL: "https://example.com/1"}, {Text: "two", URL: "https://example.com/2"}}

	for _, h := range []int{7, 8, 12} {
		t.Run(fmt.Sprintf("h=%d", h), func(t *testing.T) {
			t.Parallel()

			s := reviewScene{
				theme: "catppuccin-mocha", icons: true, w: 100, h: h, branch: true, focused: true, res: mdResult(),
				tune: func(t *testing.T, a *App, v *reviewView) {
					t.Helper()

					a.drawer.links = &linkChooser{targets: targets, root: "/w/p1"}
					a.drawer.linksOf = linksOwner{tab: a.store.State().Tab().ID, pane: "p1", view: v}
				},
			}
			a := s.build(t)

			rect := drawerRectOf(t, a, 0)
			drawn := ansi.Strip(strings.Join(a.drawerLines(a.store.State(), rect), "\n"))

			// The box needs its frame and one row: 5 lines, ending a row above the border.
			room := rect.H - 1 - wtTopY
			if shown := strings.Contains(drawn, "Links"); shown != (room >= wtChrome+1) {
				t.Errorf("h=%d (room %d): box shown = %v\n%s", h, room, shown, drawn)
			}

			for i, l := range a.drawerLines(a.store.State(), rect) {
				if ansi.StringWidth(l) != rect.W {
					t.Errorf("line %d is %d cells wide, want %d", i, ansi.StringWidth(l), rect.W)
				}
			}

			a.Update(keyEsc)

			if a.drawer.links != nil {
				t.Error("esc did not close the chooser")
			}
		})
	}
}

func TestLinkFromRenderedFile(t *testing.T) {
	t.Parallel()

	m := newLinkRig(t)
	m.render(t, "the b page")

	row := m.v.cursor
	m.press(keyEnter)

	p := m.v.page()
	if p == nil || p.rel != "docs/b.md" || len(m.v.pages) != 1 {
		t.Fatalf("enter on the link: pages %v (notice %q), want docs/b.md", m.v.pages, m.notice)
	}

	if m.drawer.links != nil || m.open.urls() != nil {
		t.Errorf("chooser open %v, opener %q, want neither", m.drawer.links != nil, m.open.urls())
	}

	m.press(keyEsc)

	if len(m.v.pages) != 0 {
		t.Fatalf("esc left %d pages", len(m.v.pages))
	}

	if m.v.cursor != row || m.v.rows[row].kind != rowMD || !m.v.mdOn["docs/a.md"] {
		t.Errorf("esc: cursor %d (was %d) on %v, rendered %v, want the rendered row again", m.v.cursor, row, m.v.rows[row].kind, m.v.mdOn["docs/a.md"])
	}
}

// linksScene is a drawer holding a page with the link chooser open over it.
func linksScene(th string, icons bool) reviewScene {
	return reviewScene{
		theme: th, icons: icons, w: 200, h: 52, branch: true, focused: true, res: mdResult(),
		tune: func(t *testing.T, a *App, v *reviewView) {
			t.Helper()

			doc := parseMarkdown([]byte(planWith(nil)), "", nil)
			p := &mdPage{path: "/w/p1/" + planPath, rel: planPath, view: mdView{doc: doc}}

			v.pages = []*mdPage{p}
			p.resize(120, 40, icons)
			p.view.cursor = pageLine(t, p, "Steps")

			a.drawer.links = &linkChooser{
				root: "/w/p1",
				sel:  1,
				targets: []mdTarget{
					{Text: "the project site", URL: "https://example.com/project"},
					{Text: "design notes", Path: "/w/p1/docs/design.md"},
					{Text: "a link with rather long text that does not fit in the box", URL: "https://example.com/a/very/long/path/that/goes/on/and/on/and/on"},
					{Path: "/w/p1/internal/ui/mdlinks.go"},
				},
			}
			a.drawer.linksOf = linksOwner{tab: a.store.State().Tab().ID, pane: "p1", view: v}
		},
	}
}

// A closed fold names the key bound to review.fold, in a page and in rendered
// rows, and no key when that command has none.
func TestFoldHintFollowsBinding(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		cfg  core.KeysConfig
		want string
		not  string
	}{
		{"default", core.KeysConfig{}, "More https://example.com/f   enter opens", ""},
		{"moved", drawerKeys("o", "review.fold", "enter", "none"), "More https://example.com/f   o opens", "enter opens"},
		{"unbound", drawerKeys("enter", "none"), "More https://example.com/f", "opens"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			m := &linkRig{pageRig: newRealPageRig(t, map[string]string{"docs/a.md": linkDoc}, func(o *Options) { o.Keys = tt.cfg })}

			check := func(where string) {
				t.Helper()

				got := ansi.Strip(m.drawn(t))
				if !strings.Contains(got, tt.want) {
					t.Errorf("%s lacks %q:\n%s", where, tt.want, got)
				}

				if tt.not != "" && strings.Contains(got, tt.not) {
					t.Errorf("%s holds %q:\n%s", where, tt.not, got)
				}
			}

			m.render(t, "More https://example.com")
			check("the rendered rows")

			m.press(keyRune('m'))
			m.openPage(t, "docs/a.md")
			check("the page")
		})
	}
}
