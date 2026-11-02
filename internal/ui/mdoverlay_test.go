package ui

import (
	"errors"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/charmbracelet/x/exp/golden"

	"github.com/mhersson/aim/internal/core"
	"github.com/mhersson/aim/internal/git"
	"github.com/mhersson/aim/internal/md"
	"github.com/mhersson/aim/internal/ui/palette"
)

// mdLinks are the links docs/a.md holds in the md.open rig.
const mdLinks = "\n[readme](../README.md)\n\n[code](../main.go)\n"

// newMDOpenRig is the page rig's repository and options with no drawer open; v is
// nil. docs/a.md is pageDoc plus a link to README.md and one to main.go.
func newMDOpenRig(t *testing.T, extra map[string]string, opts ...func(*Options)) *pageRig {
	t.Helper()

	files := map[string]string{"docs/a.md": pageDoc + mdLinks}
	maps.Copy(files, extra)

	return bareRig(t, pageRepo(t, files), opts...)
}

func holdLists(m tea.Msg) bool {
	_, ok := m.(mdListMsg)

	return ok
}

func holdMDFiles(m tea.Msg) bool {
	_, ok := m.(mdFileMsg)

	return ok
}

// mdOpen runs the md.open command and returns the list answers it held.
func (r *drawerRig) mdOpen() []tea.Msg {
	c, ok := r.reg.get("md.open")
	if !ok {
		panic("md.open is not a command")
	}

	cmd := c.run(r.App)
	_, after := r.Update(struct{}{})

	return r.flow(tea.Batch(cmd, after), holdLists)
}

// listed runs md.open and delivers its list, which opens the palette.
func (r *drawerRig) listed(t *testing.T) {
	t.Helper()

	held := r.mdOpen()
	if len(held) != 1 {
		t.Fatalf("md.open held %d answers, want 1 (notice %q)", len(held), r.notice)
	}

	r.flow(r.step(held[0]), nil)

	if r.overlay == nil || r.overlay.kind != overlayMarkdown {
		t.Fatalf("the list did not open the palette (notice %q)", r.notice)
	}
}

// pick chooses rel in the open list and delivers the answers.
func (m *pageRig) pick(t *testing.T, rel string) {
	t.Helper()

	m.flow(m.step(palette.ChosenMsg{ID: rel}), nil)
}

// opens runs md.open and picks rel; the overlay is open afterwards.
func (m *pageRig) opens(t *testing.T, rel string) {
	t.Helper()

	m.listed(t)
	m.pick(t, rel)

	if m.plan == nil || m.plan.file == nil {
		t.Fatalf("the overlay did not open on %s (notice %q)", rel, m.notice)
	}
}

// screen is the screen as text, one string per row.
func (r *drawerRig) screen() []string {
	return strings.Split(ansi.Strip(r.View().Content), "\n")
}

// viewLine is the layout line of v that holds sub.
func viewLine(t *testing.T, v *mdView, sub string) int {
	t.Helper()

	v.layout()

	for i, l := range v.doc.text() {
		if strings.Contains(l, sub) {
			return i
		}
	}

	t.Fatalf("no line holds %q", sub)

	return 0
}

// linkedWorktree adds a worktree of m's repository and returns its root.
func linkedWorktree(t *testing.T, m *pageRig) string {
	t.Helper()

	path := filepath.Join(filepath.Dir(m.dir), "b")
	gitIn(t, "git", m.dir, "worktree", "add", "-q", path, "-b", "b")

	return rootOf(t, path)
}

func TestMDOpen(t *testing.T) {
	t.Parallel()

	m := newMDOpenRig(t, nil)

	held := m.mdOpen()
	if len(held) != 1 {
		t.Fatalf("md.open held %d answers, want 1", len(held))
	}

	if msg, ok := held[0].(mdListMsg); !ok || msg.root != m.root {
		t.Fatalf("held %+v, want a list of %s", held[0], m.root)
	}

	m.flow(m.step(held[0]), nil)

	if want := []string{"README.md", "docs/a.md", "notes/b.md"}; !slices.Equal(overlayIDs(m.App), want) || m.overlay.kind != overlayMarkdown {
		t.Fatalf("palette lists %v, want %v", overlayIDs(m.App), want)
	}

	box := ansi.Strip(m.overlay.pal.View(60))
	for _, want := range []string{iconMarkdown + " Markdown files", "enter open"} {
		if !strings.Contains(box, want) {
			t.Errorf("the palette lacks %q:\n%s", want, box)
		}
	}

	if m.store.State().Tab().Review != nil {
		t.Fatal("md.open opened a drawer")
	}

	m.pick(t, "docs/a.md")

	if m.plan == nil || m.plan.file == nil || m.plan.file.path != filepath.Join(m.root, "docs/a.md") || m.plan.root != m.root {
		t.Fatalf("plan = %+v, want the file docs/a.md under %s (notice %q)", m.plan, m.root, m.notice)
	}

	if m.store.State().Tab().Review != nil {
		t.Fatal("the pick opened a drawer")
	}

	screen := m.screen()
	if want := " " + iconMarkdown + " " + filepath.Base(m.dir) + " · docs/a.md "; !strings.Contains(screen[2], want) || strings.Contains(screen[2], "plan file") || !strings.HasSuffix(screen[2], "─╮") {
		t.Errorf("top border = %q, want %q, no tag and the corner", screen[2], want)
	}

	bottom := slices.IndexFunc(screen, func(l string) bool { return strings.Contains(l, "╯") })
	if bottom < 1 || !strings.Contains(screen[bottom-1], "esc close") {
		t.Errorf("the row above the bottom border (%d) lacks the hint:\n%s", bottom, strings.Join(screen, "\n"))
	}

	t.Run("without icons", func(t *testing.T) {
		m.icons = false

		if want := " " + filepath.Base(m.dir) + " · docs/a.md "; !strings.Contains(m.screen()[2], want) || strings.Contains(m.screen()[2], iconMarkdown) {
			t.Errorf("top border = %q, want %q and no icon", m.screen()[2], want)
		}
	})

	t.Run("a linked worktree", func(t *testing.T) {
		m := newMDOpenRig(t, nil)
		wt := linkedWorktree(t, m)

		openIn(t, m.App, core.SetPaneCwd{ID: m.store.State().Tab().Focus, Cwd: wt})

		held := m.mdOpen()
		if len(held) != 1 || held[0].(mdListMsg).root != wt { //nolint:forcetypeassert // the held message is a list
			t.Fatalf("held %+v, want a list of %s", held, wt)
		}

		m.flow(m.step(held[0]), nil)
		m.pick(t, "docs/a.md")

		if m.plan == nil || m.plan.root != wt {
			t.Fatalf("plan = %+v, want the worktree %s", m.plan, wt)
		}

		if want := " " + iconMarkdown + " " + filepath.Base(m.dir) + " · docs/a.md "; !strings.Contains(m.screen()[2], want) {
			t.Errorf("top border = %q, want the main checkout's name in %q", m.screen()[2], want)
		}
	})
}

func TestMDOpenWhere(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		keys  bool // the drawer holds the keyboard after setup
		setup func(t *testing.T, m *pageRig, wt string)
		want  func(m *pageRig, wt string) string // the list's root
	}{
		{"no drawer", false, func(*testing.T, *pageRig, string) {}, func(m *pageRig, _ string) string { return m.root }},
		{"a pinned drawer with the keyboard", true, func(t *testing.T, m *pageRig, wt string) {
			m.opened(t)
			openIn(t, m.App, core.PinReview{Root: wt})
			m.settle()
		}, func(_ *pageRig, wt string) string { return wt }},
		{"the pinned drawer without the keyboard", false, func(t *testing.T, m *pageRig, wt string) {
			m.opened(t)
			openIn(t, m.App, core.PinReview{Root: wt})
			m.settle()
			m.drawerRelease()
		}, func(m *pageRig, _ string) string { return m.root }},
		{"a drawer on a pull request", true, func(t *testing.T, m *pageRig, wt string) {
			m.opened(t)
			openIn(t, m.App, core.ShowReviewPR{Repo: "github.com/o/r", Number: 7, Root: wt})
			m.settle()
		}, func(_ *pageRig, wt string) string { return wt }},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			m := newMDOpenRig(t, nil)
			wt := linkedWorktree(t, m)
			tt.setup(t, m, wt)

			if tt.keys && !m.drawerHasKeys() {
				t.Fatal("setup: the drawer lacks the keyboard")
			}

			held := m.mdOpen()
			if len(held) != 1 {
				t.Fatalf("md.open held %d answers, want 1 (notice %q)", len(held), m.notice)
			}

			if got, want := held[0].(mdListMsg).root, tt.want(m, wt); got != want { //nolint:forcetypeassert // the held message is a list
				t.Errorf("listed %q, want %q", got, want)
			}
		})
	}

	t.Run("an empty tab", func(t *testing.T) {
		t.Parallel()

		a := newTestApp(t)
		sized(t, a, 120, 40)

		if cmd := a.mdOpenCommand(); cmd != nil || a.notice != noticeNotRepo {
			t.Errorf("cmd %v, notice %q, want none and %q", cmd != nil, a.notice, noticeNotRepo)
		}
	})

	t.Run("a pane outside a repository", func(t *testing.T) {
		t.Parallel()

		r := newKeysRig(t, t.TempDir())

		held := r.mdOpen()
		if len(held) != 1 {
			t.Fatalf("md.open held %d answers, want 1", len(held))
		}

		if msg := held[0].(mdListMsg); !errors.Is(msg.err, git.ErrNotRepo) { //nolint:forcetypeassert // the held message is a list
			t.Fatalf("list error = %v, want %v", msg.err, git.ErrNotRepo)
		}

		r.notice = ""
		r.flow(r.step(held[0]), nil)

		if r.notice != noticeNotRepo || r.overlay != nil {
			t.Errorf("notice %q, palette %v, want %q and none", r.notice, r.overlay != nil, noticeNotRepo)
		}
	})

	t.Run("a pinned root that was removed", func(t *testing.T) {
		t.Parallel()

		m := newMDOpenRig(t, nil)
		wt := linkedWorktree(t, m)

		m.opened(t)
		openIn(t, m.App, core.PinReview{Root: wt})
		m.settle()

		if err := os.RemoveAll(wt); err != nil {
			t.Fatal(err)
		}

		held := m.mdOpen()
		if len(held) != 1 {
			t.Fatalf("md.open held %d answers, want 1", len(held))
		}

		m.notice = ""
		m.flow(m.step(held[0]), nil)

		if want := "worktree removed: " + wt; m.notice != want || m.overlay != nil {
			t.Errorf("notice %q, palette %v, want %q and none", m.notice, m.overlay != nil, want)
		}
	})
}

func TestMDOpenNotices(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		mutate func(*mdListMsg)
		want   string
		items  []string // the palette's titles, when it opens
		ids    []string
	}{
		{name: "no files", mutate: func(m *mdListMsg) { m.files = nil }, want: "no markdown files in this worktree"},
		{name: "a listing error", mutate: func(m *mdListMsg) { m.files, m.err = nil, errors.New("boom") }, want: "markdown files: boom"},
		{
			name:   "a hostile name",
			mutate: func(m *mdListMsg) { m.files = []string{"a\x1b[31mb\u200b.md"} },
			items:  []string{"a[31mb.md"},
			ids:    []string{"a\x1b[31mb\u200b.md"},
		},
	}

	m := newMDOpenRig(t, nil)

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			held := m.mdOpen()
			if len(held) != 1 {
				t.Fatalf("md.open held %d answers, want 1", len(held))
			}

			msg := held[0].(mdListMsg) //nolint:forcetypeassert // the held message is a list
			tt.mutate(&msg)

			m.notice = ""
			m.flow(m.step(msg), nil)

			if tt.items == nil {
				if m.notice != tt.want || m.overlay != nil {
					t.Errorf("notice = %q, palette open = %v, want %q and none", m.notice, m.overlay != nil, tt.want)
				}

				return
			}

			if m.overlay == nil || !slices.Equal(overlayIDs(m.App), tt.ids) {
				t.Fatalf("palette ids = %q, want %q", overlayIDs(m.App), tt.ids)
			}

			var titles []string
			for _, it := range m.overlay.items {
				titles = append(titles, it.Title)
			}

			if !slices.Equal(titles, tt.items) {
				t.Errorf("titles = %q, want %q", titles, tt.items)
			}

			m.closeOverlay()
		})
	}
}

func TestMDOpenLate(t *testing.T) {
	t.Parallel()

	lists := []struct {
		name string
		// setup runs before md.open; after runs before the answer; undo after it.
		setup, after, undo func(t *testing.T, m *pageRig)
		opens              bool // the answer still opens the palette
	}{
		{name: "a second md.open", after: func(_ *testing.T, m *pageRig) { m.mdOpen() }},
		{
			name:  "the compose box opened",
			after: func(_ *testing.T, m *pageRig) { m.compose = &composeView{} },
			undo:  func(_ *testing.T, m *pageRig) { m.compose = nil },
		},
		{
			name:  "a confirm opened",
			after: func(_ *testing.T, m *pageRig) { m.confirm = &confirm{} },
			undo:  func(_ *testing.T, m *pageRig) { m.confirm = nil },
		},
		{name: "the focus moved to a pane elsewhere", after: func(t *testing.T, m *pageRig) {
			chord(m.App, 'n')
			openIn(t, m.App, core.SetPaneCwd{ID: m.store.State().Tab().Focus, Cwd: t.TempDir()})
		}},
		{name: "a new tab", after: func(t *testing.T, m *pageRig) {
			if err := m.dispatch(core.NewTab{Kind: core.KindShell, Cwd: "/w/c"}); err != nil {
				t.Fatal(err)
			}
		}},
		{
			name: "the drawer took the keyboard on a pinned drawer",
			setup: func(t *testing.T, m *pageRig) {
				wt := linkedWorktree(t, m)
				m.opened(t)
				openIn(t, m.App, core.PinReview{Root: wt})
				m.settle()
				m.drawerRelease()
			},
			after: func(_ *testing.T, m *pageRig) { m.drawerTake() },
		},
		{
			name: "the drawer's worktree list is open",
			setup: func(t *testing.T, m *pageRig) {
				m.opened(t)
			},
			after: func(_ *testing.T, m *pageRig) {
				tab := m.store.State().Tab()
				m.drawer.list = &wtList{tab: tab.ID, pane: tab.Review.Pane, cancel: func() {}}
			},
			undo:  func(_ *testing.T, m *pageRig) { m.drawer.list = nil },
			opens: true,
		},
	}

	for _, tt := range lists {
		t.Run("a list after "+tt.name, func(t *testing.T) {
			t.Parallel()

			m := newMDOpenRig(t, nil)
			if tt.setup != nil {
				tt.setup(t, m)
			}

			held := m.mdOpen()
			if len(held) != 1 {
				t.Fatalf("md.open held %d answers, want 1", len(held))
			}

			tt.after(t, m)
			m.flow(m.step(held[0]), nil)

			if opened := m.overlay != nil && m.overlay.kind == overlayMarkdown; opened != tt.opens {
				t.Errorf("palette opened = %v, want %v", opened, tt.opens)
			}

			if tt.undo != nil {
				tt.undo(t, m)
			}
		})
	}

	reads := []struct {
		name        string
		after, undo func(t *testing.T, m *pageRig)
	}{
		{name: "a newer md.open", after: func(_ *testing.T, m *pageRig) { m.mdOpen() }},
		{name: "a plan ask", after: func(t *testing.T, m *pageRig) {
			chord(m.App, 'n')

			id := m.store.State().Tab().Focus
			openIn(t, m.App, core.NotePlan{ID: id, Plan: core.Plan{Path: filepath.Join(m.root, "docs/a.md"), Root: m.root, Kind: core.PlanFile}})
			press(m.App, keyLeader, keyRune('p'))
		}},
		{
			name:  "the compose box opened",
			after: func(_ *testing.T, m *pageRig) { m.compose = &composeView{} },
			undo:  func(_ *testing.T, m *pageRig) { m.compose = nil },
		},
		{
			name:  "a confirm opened",
			after: func(_ *testing.T, m *pageRig) { m.confirm = &confirm{} },
			undo:  func(_ *testing.T, m *pageRig) { m.confirm = nil },
		},
	}

	for _, tt := range reads {
		t.Run("a read after "+tt.name, func(t *testing.T) {
			t.Parallel()

			m := newMDOpenRig(t, nil)
			m.listed(t)

			held := m.flow(m.step(palette.ChosenMsg{ID: "docs/a.md"}), holdMDFiles)
			if len(held) != 1 {
				t.Fatalf("the pick held %d answers, want 1", len(held))
			}

			tt.after(t, m)
			m.flow(m.step(held[0]), nil)

			if m.plan != nil {
				t.Errorf("the late read opened the overlay: %+v", m.plan.file)
			}

			if tt.undo != nil {
				tt.undo(t, m)
			}
		})
	}
}

func TestMDOverlayOverDrawer(t *testing.T) {
	t.Parallel()

	m := newRealPageRig(t, map[string]string{"docs/a.md": pageDoc + mdLinks})
	pane, root := m.store.State().Tab().Review.Pane, m.v.root

	if !m.drawerHasKeys() {
		t.Fatal("setup: the drawer lacks the keyboard")
	}

	m.opens(t, "docs/a.md")

	if tab := m.store.State().Tab(); tab.Review == nil || tab.Review.Pane != pane || m.v.root != root || len(m.v.pages) != 0 {
		t.Fatalf("the drawer changed: review %+v, root %q, %d pages", tab.Review, m.v.root, len(m.v.pages))
	}

	cursor := m.v.cursor

	m.press(keyRune('j'))

	if m.plan.view.cursor != 1 || m.v.cursor != cursor {
		t.Errorf("after j: overlay cursor %d, drawer cursor %d (was %d), want the overlay's alone to move", m.plan.view.cursor, m.v.cursor, cursor)
	}

	m.press(keyEsc)

	if m.plan != nil || !m.drawerHasKeys() {
		t.Errorf("after esc: overlay open %v, drawer has the keyboard %v, want it closed and the keyboard back", m.plan != nil, m.drawerHasKeys())
	}
}

func TestMDOverlayKeys(t *testing.T) {
	t.Parallel()

	m := newMDOpenRig(t, nil)
	clip := fakeClip(m.App)
	m.opens(t, "docs/a.md")

	v := &m.plan.view
	base := m.st.count()

	m.press(keyRune('j'))

	if v.cursor != 1 {
		t.Errorf("j: cursor %d, want 1", v.cursor)
	}

	m.press(keyRune(']'))

	if got := v.doc.text()[v.cursor]; !strings.Contains(got, "Beta") {
		t.Errorf("]: cursor on %q, want the Beta heading", got)
	}

	m.press(keyRune('/'))
	m.typeKeys("Gamma")
	m.press(keyEnter)

	if got := v.doc.text()[v.cursor]; !strings.Contains(got, "Gamma") {
		t.Errorf("/ Gamma: cursor on %q, want the Gamma line", got)
	}

	v.cursor = viewLine(t, v, "More")

	m.press(keyEnter)

	if !slices.ContainsFunc(v.doc.text(), func(l string) bool { return strings.Contains(l, "hidden text") }) {
		t.Error("enter on the fold's head did not open it")
	}

	v.cursor = viewLine(t, v, "intro text")

	m.press(keyRune('y'))

	if got := clipboardWrites(clip); !slices.Equal(got, []string{"intro text"}) {
		t.Errorf("y copied %q, want the paragraph at the cursor", got)
	}

	cursor := v.cursor

	m.press(keyRune('z'))

	m.press(keyLeader, keyRune('d'))

	if m.st.count() != base || m.store.State().Tab().Review != nil || m.plan == nil || v.cursor != cursor || m.v != nil {
		t.Errorf("z and leader d changed something: %d panes started, review %v, cursor %d (was %d)", m.st.count()-base, m.store.State().Tab().Review != nil, v.cursor, cursor)
	}

	m.press(keyEsc)

	if m.plan != nil {
		t.Error("esc did not close the overlay")
	}
}

func TestMDOverlayLinks(t *testing.T) {
	t.Parallel()

	title := func(m *pageRig) string { return m.screen()[2] }

	t.Run("a markdown link pushes a page", func(t *testing.T) {
		t.Parallel()

		m := newMDOpenRig(t, nil)
		m.opens(t, "docs/a.md")

		v := &m.plan.view
		v.cursor = viewLine(t, v, "readme")

		m.press(keyEnter)

		if len(m.plan.pages) != 1 {
			t.Fatalf("%d pages, want 1 (notice %q)", len(m.plan.pages), m.notice)
		}

		if want := filepath.Base(m.dir) + " · README.md "; !strings.Contains(title(m), want) {
			t.Errorf("top border = %q, want %q", title(m), want)
		}

		m.press(keyEsc)

		if want := filepath.Base(m.dir) + " · docs/a.md "; len(m.plan.pages) != 0 || !strings.Contains(title(m), want) {
			t.Errorf("after esc: %d pages, top border %q, want %q", len(m.plan.pages), title(m), want)
		}
	})

	t.Run("a code link opens the editor", func(t *testing.T) {
		t.Parallel()

		m := newMDOpenRig(t, nil)
		m.opens(t, "docs/a.md")

		v := &m.plan.view
		v.cursor = viewLine(t, v, "code")
		base := m.st.count()

		// The new pane's wait command blocks until its process ends, so only the link's answer is delivered.
		for _, msg := range flatten(m.step(keyEnter)) {
			if le, ok := msg.(linkEditMsg); ok {
				m.Update(le)
			}
		}

		if m.plan != nil || m.st.count()-base != 1 {
			t.Fatalf("overlay open %v, %d panes started, want it closed and one editor", m.plan != nil, m.st.count()-base)
		}

		argv := m.st.cmds[base].Argv
		if got, want := argv[len(argv)-2:], []string{"+1", filepath.Join(m.root, "main.go")}; !slices.Equal(got, want) {
			t.Errorf("the editor got %q, want %q", got, want)
		}
	})

	t.Run("a link out of the worktree is refused", func(t *testing.T) {
		t.Parallel()

		m := newMDOpenRig(t, map[string]string{"docs/out.md": "[out](../../x.md)\n"})
		m.opens(t, "docs/out.md")

		v := &m.plan.view
		v.cursor = viewLine(t, v, "out")

		m.press(keyEnter)

		want := "not opening " + shortPath(filepath.Join(filepath.Dir(m.root), "x.md"), "", "/home/test") + ": outside the worktree"
		if m.notice != want || m.plan == nil || len(m.plan.pages) != 0 {
			t.Errorf("notice %q, overlay open %v, want %q and the overlay kept", m.notice, m.plan != nil, want)
		}
	})
}

func TestMDOverlayMouse(t *testing.T) {
	t.Parallel()

	long := new(strings.Builder)
	long.WriteString("Alpha beta gamma delta epsilon zeta eta theta.\n\n")

	for i := range 120 {
		fmt.Fprintf(long, "paragraph %d\n\n", i)
	}

	m := newMDOpenRig(t, map[string]string{"docs/long.md": long.String()})
	clip := fakeClip(m.App)
	m.opens(t, "docs/long.md")

	sized(t, m.App, 120, 40)

	v := &m.plan.view

	m.Update(tea.MouseWheelMsg{X: 60, Y: 10, Button: tea.MouseWheelDown})

	if v.top != 3 {
		t.Errorf("a notch down moved top to %d, want 3", v.top)
	}

	v.top = 0

	var y, x0, x1 int

	for row, line := range m.screen() {
		if before, _, ok := strings.Cut(line, "beta gamma"); ok {
			y, x0 = row, len([]rune(before))
			x1 = x0 + len("beta gamma") - 1

			break
		}
	}

	if x1 == 0 {
		t.Fatalf("no row holds the words:\n%s", strings.Join(m.screen(), "\n"))
	}

	dragRun(m.App, x0, y, x1, y, 0)

	if got := primaryWrites(clip); !slices.Equal(got, []string{"beta gamma"}) {
		t.Errorf("primary writes %q, want the two words", got)
	}
}

func TestMDOverlayReadErrors(t *testing.T) {
	t.Parallel()

	big := strings.Repeat("x", md.MaxBytes+10)
	m := newMDOpenRig(t, map[string]string{"docs/big.md": big})

	if err := os.Symlink("/etc/hostname", filepath.Join(m.dir, "docs/link.md")); err != nil {
		t.Skipf("no symlinks: %v", err)
	}

	write(t, filepath.Join(m.dir, "docs/dir.md"), "x\n")

	tests := []struct {
		name, rel string
		before    func()
		want      string // the notice, or its prefix when it ends with a colon and space
	}{
		{"removed after the list", "docs/a.md", func() { _ = os.Remove(filepath.Join(m.dir, "docs/a.md")) }, "not opening docs/a.md: no such file"},
		{"a symlink out of the root", "docs/link.md", nil, "not opening docs/link.md: "},
		{"a folder", "docs/dir.md", func() {
			_ = os.Remove(filepath.Join(m.dir, "docs/dir.md"))
			_ = os.MkdirAll(filepath.Join(m.dir, "docs/dir.md"), 0o755)
		}, "not opening docs/dir.md: it is a folder"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m.listed(t)

			if tt.before != nil {
				tt.before()
			}

			m.notice = ""
			m.pick(t, tt.rel)

			if m.plan != nil || !strings.HasPrefix(m.notice, tt.want) || (!strings.HasSuffix(tt.want, ": ") && m.notice != tt.want) {
				t.Errorf("notice %q, overlay open %v, want %q and none", m.notice, m.plan != nil, tt.want)
			}
		})
	}

	t.Run("a file past the cap", func(t *testing.T) {
		m.listed(t)
		m.pick(t, "docs/big.md")

		if m.plan == nil {
			t.Fatalf("the overlay did not open (notice %q)", m.notice)
		}

		doc, ok := m.plan.view.doc.(*mdDoc)
		if !ok || !doc.doc.Cut() {
			t.Errorf("the document is not marked cut: %T", m.plan.view.doc)
		}
	})
}

func TestMDOverlayTitle(t *testing.T) {
	t.Parallel()

	t.Run("a long path is cut from its start", func(t *testing.T) {
		t.Parallel()

		rel := "docs/" + strings.Repeat("a-long-folder-name/", 6) + "a.md"
		m := newMDOpenRig(t, map[string]string{rel: "# Deep\n"})

		sized(t, m.App, 30, 20)
		m.opens(t, rel)
		assertScreen(t, m.App, 30, 20)

		if top := m.screen()[2]; !strings.Contains(top, "…") || !strings.Contains(top, "a.md ") || strings.Contains(top, "docs/") {
			t.Errorf("top border = %q, want the path cut from its start with … and ending a.md", top)
		}
	})

	t.Run("a hostile repository name", func(t *testing.T) {
		t.Parallel()

		dir := pageRepo(t, nil)
		hostile := filepath.Join(filepath.Dir(dir), "ho\x1b[31mst\u200bile")

		if err := os.Rename(dir, hostile); err != nil {
			t.Fatal(err)
		}

		m := bareRig(t, hostile)
		m.opens(t, "docs/a.md")

		raw := m.View().Content
		if strings.Contains(ansi.Strip(raw), "\x1b") || strings.Contains(ansi.Strip(raw), "\u200b") {
			t.Error("the screen holds a control or invisible character from the name")
		}

		if top := m.screen()[2]; !strings.Contains(top, "ho[31mstile · docs/a.md") {
			t.Errorf("top border = %q, want the cleaned name", top)
		}
	})

	t.Run("too small to draw", func(t *testing.T) {
		t.Parallel()

		m := newMDOpenRig(t, nil)
		m.opens(t, "docs/a.md")
		sized(t, m.App, 20, 8)

		if screen := strings.Join(m.screen(), "\n"); strings.Contains(screen, "esc close") || strings.Contains(screen, "Alpha") {
			t.Errorf("a box too small to draw drew:\n%s", screen)
		}

		m.press(keyEsc)

		if m.plan != nil {
			t.Error("esc did not close the overlay")
		}
	})
}

type mdOverlayScene struct {
	name string
	w, h int
	page bool // after following the readme link
}

func mdOverlayScenes() []mdOverlayScene {
	return []mdOverlayScene{{"md-file", 120, 40, false}, {"md-80x24", 80, 24, false}, {"md-page", 120, 40, true}}
}

// mdSceneApp opens docs/guide.md in a repository named multiplex.
func mdSceneApp(t *testing.T, icons bool, sc mdOverlayScene) *App {
	t.Helper()

	dir := pageRepo(t, map[string]string{"docs/guide.md": demoPlan(t) + "\n[readme](../README.md)\n"})
	named := filepath.Join(filepath.Dir(dir), "multiplex")

	if err := os.Rename(dir, named); err != nil {
		t.Fatal(err)
	}

	m := bareRig(t, named)
	m.icons = icons

	sized(t, m.App, sc.w, sc.h)
	m.opens(t, "docs/guide.md")

	if sc.page {
		v := &m.plan.view
		v.cursor = viewLine(t, v, "readme")

		m.press(keyEnter)

		if len(m.plan.pages) != 1 {
			t.Fatalf("the link did not open a page (notice %q)", m.notice)
		}
	}

	return m.App
}

// TestMDOverlayGolden draws each scene once in colour; it compares highlighted
// colours, so it runs serially.
func TestMDOverlayGolden(t *testing.T) {
	for _, sc := range mdOverlayScenes() {
		t.Run("catppuccin-mocha/icons=true/"+sc.name, func(t *testing.T) {
			golden.RequireEqual(t, []byte(mdSceneApp(t, true, sc).View().Content))
		})
	}
}

// TestMDOverlayText is the same screens without styling, so the layout reads.
func TestMDOverlayText(t *testing.T) {
	t.Parallel()

	for _, icons := range []bool{true, false} {
		for _, sc := range mdOverlayScenes() {
			t.Run(fmt.Sprintf("icons=%v/%s", icons, sc.name), func(t *testing.T) {
				t.Parallel()
				golden.RequireEqual(t, []byte(ansi.Strip(mdSceneApp(t, icons, sc).View().Content)))
			})
		}
	}
}

// mdWatched waits until the overlay's watch reference is held.
func (m *pageRig) mdWatched(t *testing.T) {
	t.Helper()

	m.until(t, "the overlay's watch", func() bool {
		return m.watches.md != nil && m.watches.md.release != nil && m.watched(m.root)
	})
}

func TestMDOverlayWatch(t *testing.T) {
	t.Parallel()

	t.Run("held while open", func(t *testing.T) {
		t.Parallel()

		m := newMDOpenRig(t, nil)

		if m.rootWatched(m.root) {
			t.Fatal("the root is watched before the overlay opens")
		}

		m.opens(t, "docs/a.md")
		m.mdWatched(t)

		if !m.rootWatched(m.root) {
			t.Error("rootWatched is false while only the overlay holds the root")
		}

		m.press(keyEsc)
		m.until(t, "the watch to be released", func() bool { return !m.watched(m.root) })

		if m.watches.md != nil || m.rootWatched(m.root) {
			t.Errorf("md = %+v after esc, want no reference", m.watches.md)
		}
	})

	t.Run("shared with an agent", func(t *testing.T) {
		t.Parallel()

		m := newMDOpenRig(t, nil)
		id := m.agent(t, m.dir, "")
		m.watching(t, id, m.root)

		before := m.holdCount()

		m.opens(t, "docs/a.md")
		m.mdWatched(t)

		if !m.watched(m.root) {
			t.Fatal("the root stopped being watched")
		}

		if got := m.holdCount(); got != before+1 {
			t.Fatalf("holds = %d with the overlay open, want %d", got, before+1)
		}

		m.press(keyEsc)
		m.until(t, "the overlay's reference to be released", func() bool { return m.holdCount() == before })

		if m.watches.md != nil || !m.watched(m.root) {
			t.Errorf("md = %+v, watched %v after esc, want the agent's watch kept", m.watches.md, m.watched(m.root))
		}
	})

	t.Run("without a watch set", func(t *testing.T) {
		t.Parallel()

		m := newMDOpenRig(t, nil)
		m.watches.set = nil

		m.opens(t, "docs/a.md")
		m.settle()

		if m.watches.md != nil {
			t.Errorf("md = %+v, want none without a watch set", m.watches.md)
		}
	})
}

// mdLive opens docs/a.md with the More fold open and the cursor on Gamma, at
// the top of its line.
func mdLive(t *testing.T) *pageRig {
	t.Helper()

	m := newMDOpenRig(t, nil)
	m.opens(t, "docs/a.md")
	m.mdWatched(t)

	sized(t, m.App, 80, 16) // short enough that top can rest below the first line

	v := &m.plan.view
	v.cursor = viewLine(t, v, "More")

	m.press(keyEnter)

	v.top = viewLine(t, v, "Gamma")
	v.cursor = v.top

	return m
}

func (m *pageRig) batchOf(paths ...string) tea.Cmd {
	return m.batch(m.root, git.Batch{Paths: paths})
}

func (m *pageRig) docText() string { return strings.Join(m.plan.shown().doc.text(), "\n") }

func TestMDOverlayLive(t *testing.T) {
	t.Parallel()

	a := func(m *pageRig) string { return filepath.Join(m.dir, "docs/a.md") }
	changed := strings.Replace(pageDoc, "intro text", "added above\n\nand more\n\nand more", 1) + mdLinks

	t.Run("changed", func(t *testing.T) {
		t.Parallel()

		m := mdLive(t)
		v := &m.plan.view
		old := v.doc

		// The top rests below the Beta heading and the cursor below the top.
		beta := viewLine(t, v, "Beta")
		v.top = viewLine(t, v, "item one")
		v.cursor = v.top + 1

		below, cursorOff := v.top-beta, v.cursor-v.top
		if below < 1 || cursorOff < 1 {
			t.Fatalf("top is %d below the heading and the cursor %d below the top, want both at least 1", below, cursorOff)
		}

		write(t, a(m), changed)
		m.flow(m.batchOf("docs/a.md"), nil)

		if v.doc == old || !strings.Contains(m.docText(), "added above") || strings.Contains(m.docText(), "intro text") {
			t.Fatalf("the doc did not change:\n%s", m.docText())
		}

		if got := viewLine(t, v, "Beta"); got == beta {
			t.Fatalf("the heading stayed on line %d, so the case does not move it", got)
		}

		if got := v.top - viewLine(t, v, "Beta"); got != below {
			t.Errorf("top is %d below the heading, want %d as before", got, below)
		}

		if got := v.cursor - v.top; got != cursorOff {
			t.Errorf("cursor is %d below the top, want %d as before", got, cursorOff)
		}
	})

	t.Run("another path", func(t *testing.T) {
		t.Parallel()

		m := mdLive(t)

		write(t, a(m), changed)
		m.flow(m.batchOf("main.go"), nil)

		if !strings.Contains(m.docText(), "added above") {
			t.Errorf("a batch naming main.go did not re-read:\n%s", m.docText())
		}
	})

	t.Run("unchanged", func(t *testing.T) {
		t.Parallel()

		m := mdLive(t)
		clip := fakeClip(m.App)
		sized(t, m.App, 120, 40)

		v := &m.plan.view
		old := v.doc

		var y, x0, x1 int

		for row, line := range m.screen() {
			if before, _, ok := strings.Cut(line, "last words"); ok {
				y, x0 = row, len([]rune(before))
				x1 = x0 + len("last") - 1

				break
			}
		}

		if x1 == 0 {
			t.Fatalf("no row holds the words:\n%s", strings.Join(m.screen(), "\n"))
		}

		dragRun(m.App, x0, y, x1, y, 0)

		if got := primaryWrites(clip); !slices.Equal(got, []string{"last"}) {
			t.Fatalf("primary writes %q, want the word", got)
		}

		m.flow(m.batchOf("docs/a.md"), nil)

		if v.doc != old || !strings.Contains(m.docText(), "hidden text") || m.textSelected() != "last" {
			t.Errorf("same doc %v, fold open %v, selection %q: an unchanged file reset the view", v.doc == old, strings.Contains(m.docText(), "hidden text"), m.textSelected())
		}
	})

	gone := []struct {
		name string
		gone func(t *testing.T, m *pageRig)
	}{
		{"removed", func(t *testing.T, m *pageRig) {
			if err := os.Remove(a(m)); err != nil {
				t.Fatal(err)
			}
		}},
		{"renamed", func(t *testing.T, m *pageRig) {
			if err := os.Rename(a(m), a(m)+".moved"); err != nil {
				t.Fatal(err)
			}
		}},
		{"turned into a folder", func(t *testing.T, m *pageRig) {
			if err := os.Remove(a(m)); err != nil {
				t.Fatal(err)
			}

			if err := os.Mkdir(a(m), 0o755); err != nil {
				t.Fatal(err)
			}
		}},
	}

	for _, tt := range gone {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			m := mdLive(t)
			old := m.plan.view.doc

			tt.gone(t, m)

			m.notice = ""
			m.flow(m.batchOf("docs/a.md"), nil)

			if m.plan == nil || m.plan.view.doc != old || m.notice != "" {
				t.Errorf("overlay open %v, notice %q, want the old text kept in silence", m.plan != nil, m.notice)
			}
		})
	}

	t.Run("back", func(t *testing.T) {
		t.Parallel()

		m := mdLive(t)

		if err := os.Remove(a(m)); err != nil {
			t.Fatal(err)
		}

		m.flow(m.batchOf("docs/a.md"), nil)
		write(t, a(m), "# Alpha\n\nback again\n")
		m.flow(m.batchOf("docs/a.md"), nil)

		if !strings.Contains(m.docText(), "back again") {
			t.Errorf("the file came back and the next batch did not show it:\n%s", m.docText())
		}
	})

	t.Run("a page on top", func(t *testing.T) {
		t.Parallel()

		m := mdLive(t)
		m.plan.view.cursor = viewLine(t, &m.plan.view, "readme")
		m.flow(m.step(keyEnter), nil)

		if len(m.plan.pages) != 1 {
			t.Fatalf("pages = %d, want README.md followed (notice %q)", len(m.plan.pages), m.notice)
		}

		old := m.plan.view.doc

		write(t, filepath.Join(m.dir, "README.md"), "# Readme\n\nchanged page\n")
		m.flow(m.batchOf("README.md"), nil)

		if !strings.Contains(m.docText(), "changed page") {
			t.Fatalf("the page did not change:\n%s", m.docText())
		}

		write(t, a(m), changed)
		m.flow(m.batchOf("docs/a.md"), nil)

		if m.plan.view.doc != old {
			t.Fatal("a change under the page was read while the page showed")
		}

		m.flow(m.step(keyEsc), nil)

		if len(m.plan.pages) != 0 || !strings.Contains(m.docText(), "added above") {
			t.Errorf("pages %d, text:\n%s\nwant the file's new text after the pop", len(m.plan.pages), m.docText())
		}
	})
}

func TestMDOverlayReloadLate(t *testing.T) {
	t.Parallel()

	changed := strings.Replace(pageDoc, "intro text", "added above", 1) + mdLinks

	tests := []struct {
		name string
		// late makes the held answer stale and returns what to step to apply a newer one.
		late func(t *testing.T, m *pageRig) (newer []tea.Msg)
	}{
		{"after esc", func(t *testing.T, m *pageRig) []tea.Msg {
			m.press(keyEsc)

			return nil
		}},
		{"after a newer check", func(t *testing.T, m *pageRig) []tea.Msg {
			return m.flow(m.batchOf("docs/a.md"), holdMDFiles)
		}},
		{"after a page push", func(t *testing.T, m *pageRig) []tea.Msg {
			m.plan.view.cursor = viewLine(t, &m.plan.view, "readme")
			m.flow(m.step(keyEnter), nil)

			if len(m.plan.pages) != 1 {
				t.Fatalf("no page pushed (notice %q)", m.notice)
			}

			return nil
		}},
		{"after a pop", func(t *testing.T, m *pageRig) []tea.Msg {
			m.plan.view.cursor = viewLine(t, &m.plan.view, "readme")
			m.flow(m.step(keyEnter), nil)

			return m.flow(m.step(keyEsc), holdMDFiles)
		}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			m := mdLive(t)
			old := m.plan.view.doc

			write(t, filepath.Join(m.dir, "docs/a.md"), changed)

			held := m.flow(m.batchOf("docs/a.md"), holdMDFiles)
			if len(held) != 1 {
				t.Fatalf("held %d answers, want 1", len(held))
			}

			newer := tt.late(t, m)

			m.flow(m.step(held[0]), holdMDFiles)

			if m.plan != nil && m.plan.view.doc != old {
				t.Fatal("the late answer changed the doc")
			}

			if len(newer) == 1 {
				m.flow(m.step(newer[0]), nil)

				if !strings.Contains(m.docText(), "added above") {
					t.Errorf("the newest answer did not apply:\n%s", m.docText())
				}
			}
		})
	}
}
