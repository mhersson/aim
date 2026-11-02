package ui

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/mhersson/aim/internal/core"
)

// planRig is a sized app with an agent in p2 (a shell is p1), a temporary home
// and a temporary worktree.
type planRig struct {
	a      *App
	clk    *fakeClock
	home   string
	wt     string
	st     *starts
	opener *fakeOpen
}

func realTemp(t *testing.T) string {
	t.Helper()

	d, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}

	return d
}

func newPlanRig(t *testing.T, w, h int) *planRig {
	t.Helper()

	home := realTemp(t)
	getenv := func(k string) string {
		if k == "HOME" {
			return home
		}

		return testGetenv(k)
	}

	a, st, clk := kindApp(t, gitOf(newFakeGit(t)), func(o *Options) { o.Getenv = getenv })
	f := &fakeOpen{}
	a.openLink = f.open
	chord(a, 'n')
	sized(t, a, w, h)
	a.railEntry("p2").probe = railProbe{known: true, project: "work"}

	return &planRig{a: a, clk: clk, home: home, wt: realTemp(t), st: st, opener: f}
}

// notePlan records plan for p2, arrived at t0 and unread, and moves the clock
// four minutes on.
func (r *planRig) notePlan(t *testing.T, p core.Plan) {
	t.Helper()

	p.At, p.Unread = t0, true
	openIn(t, r.a, core.NotePlan{ID: "p2", Plan: p})
	r.clk.Set(t0.Add(4 * time.Minute))
}

// filePlan writes text as a plan file of the worktree and records it.
func (r *planRig) filePlan(t *testing.T, rel, text string) string {
	t.Helper()

	path := filepath.Join(r.wt, filepath.FromSlash(rel))
	write(t, path, text)
	r.notePlan(t, core.Plan{Path: path, Root: r.wt, Kind: core.PlanFile})

	return path
}

func (r *planRig) ask() tea.Cmd { return press(r.a, keyLeader, keyRune('p')) }

// planMsgs runs cmd and returns the plan answers it holds.
func planMsgs(cmd tea.Cmd) []planReadMsg {
	var out []planReadMsg

	for _, m := range flatten(cmd) {
		if pm, ok := m.(planReadMsg); ok {
			out = append(out, pm)
		}
	}

	return out
}

// open asks for the plan and delivers its answer.
func (r *planRig) open(t *testing.T) {
	t.Helper()

	msgs := planMsgs(r.ask())
	if len(msgs) != 1 {
		t.Fatalf("leader p raised %d plan reads, want 1", len(msgs))
	}

	r.a.Update(msgs[0])
}

func (r *planRig) shown(t *testing.T) string {
	t.Helper()

	return ansi.Strip(r.a.View().Content)
}

func (r *planRig) unread() bool { return r.a.store.State().Panes["p2"].Plan.Unread }

func planKey(s string) tea.KeyPressMsg {
	switch s {
	case "ctrl+d", "ctrl+u", "ctrl+f", "ctrl+b":
		return tea.KeyPressMsg{Code: rune(s[5]), Mod: tea.ModCtrl}
	case "pgdown":
		return tea.KeyPressMsg{Code: tea.KeyPgDown}
	case "pgup":
		return tea.KeyPressMsg{Code: tea.KeyPgUp}
	case "down":
		return tea.KeyPressMsg{Code: tea.KeyDown}
	case "up":
		return tea.KeyPressMsg{Code: tea.KeyUp}
	case "enter":
		return tea.KeyPressMsg{Code: tea.KeyEnter}
	case "tab":
		return tea.KeyPressMsg{Code: tea.KeyTab}
	case "esc":
		return keyEsc
	case "leader":
		return keyLeader
	}

	return keyRune(rune(s[0]))
}

func (r *planRig) keys(ks ...string) {
	for _, k := range ks {
		r.a.Update(planKey(k))
	}
}

func demoPlan(t *testing.T) string {
	t.Helper()

	b, err := os.ReadFile("testdata/plan-demo.md")
	if err != nil {
		t.Fatal(err)
	}

	return string(b)
}

func assertScreen(t *testing.T, a *App, w, h int) string {
	t.Helper()

	content := a.View().Content
	lines := strings.Split(content, "\n")

	if len(lines) != h {
		t.Errorf("view has %d lines, want %d", len(lines), h)
	}

	for i, l := range lines {
		if got := ansi.StringWidth(l); got != w {
			t.Errorf("line %d is %d cells, want %d", i, got, w)
		}
	}

	return content
}

func TestPlanOpen(t *testing.T) {
	t.Parallel()

	r := newPlanRig(t, 120, 40)
	r.filePlan(t, "docs/superpowers/plans/p.md", demoPlan(t))

	cmd := r.ask()
	if r.a.plan != nil {
		t.Fatal("the overlay showed before the read answered")
	}

	if !r.unread() {
		t.Fatal("asking for the plan cleared its mark")
	}

	msgs := planMsgs(cmd)
	if len(msgs) != 1 || msgs[0].err != nil || msgs[0].doc == nil {
		t.Fatalf("answers = %+v, want one with a doc", msgs)
	}

	r.a.Update(msgs[0])

	if r.a.plan == nil {
		t.Fatal("the overlay did not open")
	}

	if r.unread() {
		t.Error("the plan is still unread after it opened")
	}

	out := r.shown(t)
	for _, want := range []string{"work · docs/superpowers/plans/p.md", " plan file · 4m ", "Session Caching Implementation Plan", "esc close"} {
		if !strings.Contains(out, want) {
			t.Errorf("view lacks %q:\n%s", want, out)
		}
	}

	r.keys("esc")

	if r.a.plan != nil {
		t.Error("esc did not close the overlay")
	}
}

func TestPlanOpenModePlan(t *testing.T) {
	t.Parallel()

	r := newPlanRig(t, 120, 40)
	path := filepath.Join(r.home, ".claude", "plans", "x.md")
	write(t, path, "# A mode plan\n\nSee [the doc](docs/a.md).\n")
	openIn(t, r.a, core.SetAgentCwd{ID: "p2", Cwd: r.wt})
	r.notePlan(t, core.Plan{Path: path, Kind: core.PlanMode})

	r.open(t)

	if r.a.plan == nil {
		t.Fatal("the overlay did not open")
	}

	if r.a.plan.root != r.wt || r.a.plan.base != r.wt {
		t.Errorf("root, base = %q, %q; want the agent's directory %q", r.a.plan.root, r.a.plan.base, r.wt)
	}

	out := r.shown(t)
	for _, want := range []string{"work · ~/.claude/plans/x.md", " plan mode · 4m "} {
		if !strings.Contains(out, want) {
			t.Errorf("view lacks %q:\n%s", want, out)
		}
	}

	var got string

	lay := r.a.plan.view.layout()
	for _, l := range lay.Lines {
		for _, run := range l {
			if run.Path != "" {
				got = run.Path
			}
		}
	}

	if want := filepath.Join(r.wt, "docs", "a.md"); got != want {
		t.Errorf("link path = %q, want %q", got, want)
	}
}

func TestPlanOpenNotices(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		setup func(t *testing.T, r *planRig) *App
		want  string
	}{
		{"a shell", func(t *testing.T, r *planRig) *App {
			r.filePlan(t, "docs/superpowers/plans/p.md", "# x\n")
			openIn(t, r.a, core.FocusPane{ID: "p1"})

			return r.a
		}, "the focused pane is not an agent"},
		{"an empty tab", func(t *testing.T, _ *planRig) *App {
			a := newTestApp(t)
			sized(t, a, 120, 40)

			return a
		}, "the focused pane is not an agent"},
		{"no plan yet", func(*testing.T, *planRig) *App { return nil }, "no plan from work yet"},
		{"a removed plan file", func(t *testing.T, r *planRig) *App {
			path := r.filePlan(t, "docs/superpowers/plans/gone.md", "# x\n")
			if err := os.Remove(path); err != nil {
				t.Fatal(err)
			}

			return nil
		}, "not opening docs/superpowers/plans/gone.md: no such file"},
		{"a folder", func(t *testing.T, r *planRig) *App {
			dir := filepath.Join(r.wt, "docs", "superpowers", "plans", "dir.md")
			if err := os.MkdirAll(dir, 0o755); err != nil {
				t.Fatal(err)
			}

			r.notePlan(t, core.Plan{Path: dir, Root: r.wt, Kind: core.PlanFile})

			return nil
		}, "not opening docs/superpowers/plans/dir.md: it is a folder"},
		{"a symlink out of the root", func(t *testing.T, r *planRig) *App {
			secret := filepath.Join(realTemp(t), "secret.md")
			write(t, secret, "# the secret text\n")

			link := filepath.Join(r.wt, "docs", "superpowers", "plans", "link.md")
			if err := os.MkdirAll(filepath.Dir(link), 0o755); err != nil {
				t.Fatal(err)
			}

			if err := os.Symlink(secret, link); err != nil {
				t.Skipf("no symlinks: %v", err)
			}

			r.notePlan(t, core.Plan{Path: link, Root: r.wt, Kind: core.PlanFile})

			return nil
		}, "not opening docs/superpowers/plans/link.md: "},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			r := newPlanRig(t, 120, 40)

			a := tt.setup(t, r)
			if a == nil {
				a = r.a
			}

			cmd := press(a, keyLeader, keyRune('p'))
			for _, m := range planMsgs(cmd) {
				a.Update(m)
			}

			if a.plan != nil {
				t.Fatal("the overlay opened")
			}

			if !strings.HasPrefix(a.notice, tt.want) {
				t.Errorf("notice = %q, want it to start %q", a.notice, tt.want)
			}

			if strings.Contains(ansi.Strip(a.View().Content), "the secret text") {
				t.Error("the text of a file outside the root reached the screen")
			}
		})
	}
}

func TestPlanOpenLate(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		run      func(t *testing.T, r *planRig) []planReadMsg // the answers to deliver, in order
		wantOpen bool
		stale    bool // a newer plan noted meanwhile stays unread and the overlay shows the older text
	}{
		{"after esc", func(t *testing.T, r *planRig) []planReadMsg {
			msgs := planMsgs(r.ask())
			r.keys("esc")

			return msgs
		}, false, false},
		{"after a second leader p", func(t *testing.T, r *planRig) []planReadMsg {
			first := planMsgs(r.ask())
			second := planMsgs(r.ask())

			r.a.Update(first[0])

			if r.a.plan != nil {
				t.Fatal("the first answer opened the overlay")
			}

			return []planReadMsg{second[0], first[0]}
		}, true, false},
		{"after the palette opened", func(t *testing.T, r *planRig) []planReadMsg {
			msgs := planMsgs(r.ask())
			r.a.openCommandPalette()

			return msgs
		}, false, false},
		{"after the pane closed", func(t *testing.T, r *planRig) []planReadMsg {
			msgs := planMsgs(r.ask())
			openIn(t, r.a, core.ClosePane{ID: "p2"})

			return msgs
		}, false, false},
		{"after a newer plan was noted", func(t *testing.T, r *planRig) []planReadMsg {
			msgs := planMsgs(r.ask())
			r.notePlan(t, core.Plan{Path: filepath.Join(r.wt, "docs/superpowers/specs/s.md"), Root: r.wt, Kind: core.PlanFile})

			return msgs
		}, true, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			r := newPlanRig(t, 120, 40)
			r.filePlan(t, "docs/superpowers/plans/p.md", demoPlan(t))

			var shown *planView

			for _, m := range tt.run(t, r) {
				r.a.Update(m)

				if shown != nil && r.a.plan != shown {
					t.Fatal("a late answer replaced the overlay")
				}

				shown = r.a.plan
			}

			if open := r.a.plan != nil; open != tt.wantOpen {
				t.Errorf("overlay open = %v, want %v", open, tt.wantOpen)
			}

			if p, ok := r.a.store.State().Panes["p2"]; ok && !tt.wantOpen && !p.Plan.Unread {
				t.Error("a late answer marked the plan read")
			}

			if tt.stale {
				if !r.unread() {
					t.Error("showing an older plan marked the newer one read")
				}

				if !strings.HasSuffix(r.a.plan.plan.Path, "plans/p.md") {
					t.Errorf("the overlay shows %s, want the plan that was read", r.a.plan.plan.Path)
				}
			}
		})
	}
}

func longPlan(t *testing.T) string {
	t.Helper()

	var b strings.Builder

	b.WriteString("# Title\n\n")

	for i := 1; i < 100; i++ {
		fmt.Fprintf(&b, "## Section %d\n\nParagraph %d.\n\n", i, i)
	}

	return b.String()
}

func TestPlanKeys(t *testing.T) {
	t.Parallel()

	// r is the doc rows and n the layout's lines.
	tests := []struct {
		name string
		keys []string
		want func(n, r int) (top, cursor int)
	}{
		{"j", []string{"j"}, func(int, int) (int, int) { return 0, 1 }},
		{"down", []string{"down"}, func(int, int) (int, int) { return 0, 1 }},
		{"k after j", []string{"j", "j", "k"}, func(int, int) (int, int) { return 0, 1 }},
		{"up after down", []string{"down", "down", "up"}, func(int, int) (int, int) { return 0, 1 }},
		{"ctrl+d is half the rows", []string{"ctrl+d"}, func(_, r int) (int, int) { return 0, r / 2 }},
		{"ctrl+u comes back", []string{"ctrl+d", "ctrl+u"}, func(int, int) (int, int) { return 0, 0 }},
		{"ctrl+f is the rows less one", []string{"ctrl+f"}, func(_, r int) (int, int) { return r - 1, r - 1 }},
		{"pgdown is ctrl+f", []string{"pgdown"}, func(_, r int) (int, int) { return r - 1, r - 1 }},
		{"ctrl+b comes back", []string{"ctrl+f", "ctrl+f", "ctrl+b"}, func(_, r int) (int, int) { return r - 1, r - 1 }},
		{"pgup comes back", []string{"pgdown", "pgup"}, func(int, int) (int, int) { return 0, 0 }},
		{"G", []string{"G"}, func(n, r int) (int, int) { return n - r, n - 1 }},
		{"g", []string{"G", "g"}, func(int, int) (int, int) { return 0, 0 }},
		{"x does nothing", []string{"x"}, func(int, int) (int, int) { return 0, 0 }},
		{"tab does nothing", []string{"tab"}, func(int, int) (int, int) { return 0, 0 }},
		{"the leader does nothing", []string{"leader"}, func(int, int) (int, int) { return 0, 0 }},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			r := newPlanRig(t, 120, 40)
			r.filePlan(t, "docs/superpowers/plans/p.md", longPlan(t))
			r.open(t)

			v := &r.a.plan.view
			n, rows := len(v.layout().Lines), v.h

			if n < 200 {
				t.Fatalf("the plan has %d lines, want at least 200", n)
			}

			r.keys(tt.keys...)

			top, cursor := tt.want(n, rows)
			if v.top != top || v.cursor != cursor {
				t.Errorf("top, cursor = %d, %d; want %d, %d", v.top, v.cursor, top, cursor)
			}

			if r.a.plan == nil || r.a.pending {
				t.Error("the key closed the overlay or began a leader sequence")
			}
		})
	}

	t.Run("a paste", func(t *testing.T) {
		t.Parallel()

		r := newPlanRig(t, 120, 40)
		r.filePlan(t, "docs/superpowers/plans/p.md", longPlan(t))
		r.open(t)
		r.a.Update(tea.PasteMsg{Content: "j\nj"})

		if v := r.a.plan.view; v.cursor != 0 || v.top != 0 {
			t.Errorf("a paste moved the view to %d, %d", v.top, v.cursor)
		}
	})

	t.Run("headings", func(t *testing.T) {
		t.Parallel()

		r := newPlanRig(t, 120, 40)
		r.filePlan(t, "docs/superpowers/plans/p.md", longPlan(t))
		r.open(t)

		v := &r.a.plan.view
		r.keys("]")

		next := v.cursor
		if next == 0 || !isHeadingLine(v.layout().Lines[next]) {
			t.Fatalf("] landed on line %d, not on a later heading", next)
		}

		r.keys("]", "[")

		if v.cursor != next {
			t.Errorf("[ landed on %d, want %d", v.cursor, next)
		}
	})

	t.Run("enter on a fold head", func(t *testing.T) {
		t.Parallel()

		r := newPlanRig(t, 120, 40)
		r.filePlan(t, "docs/superpowers/plans/p.md", demoPlan(t))
		r.open(t)

		v := &r.a.plan.view
		before := len(v.layout().Lines)
		v.cursor = lineOf(v.layout(), "For reviewers")

		r.keys("enter")

		if after := len(v.layout().Lines); after <= before {
			t.Errorf("enter on the fold head left %d lines of %d", after, before)
		}

		r.keys("enter")

		if len(v.layout().Lines) != before {
			t.Error("a second enter did not close the fold")
		}

		v.cursor = 0

		r.keys("enter")

		if len(v.layout().Lines) != before {
			t.Error("enter on a plain line changed the doc")
		}
	})
}

func TestPlanWheel(t *testing.T) {
	t.Parallel()

	setup := func(t *testing.T) *planRig {
		r := newPlanRig(t, 120, 40)
		r.filePlan(t, "docs/superpowers/plans/p.md", longPlan(t))
		r.open(t)

		return r
	}

	t.Run("over the box", func(t *testing.T) {
		t.Parallel()

		r := setup(t)
		v := &r.a.plan.view
		v.cursor = 5

		r.a.Update(tea.MouseWheelMsg{X: 60, Y: 10, Button: tea.MouseWheelDown})

		if v.top != 3 || v.cursor != 5 {
			t.Errorf("top, cursor = %d, %d; want 3, 5", v.top, v.cursor)
		}

		r.a.Update(tea.MouseWheelMsg{X: 60, Y: 10, Button: tea.MouseWheelDown})

		if v.top != 6 || v.cursor != 6 {
			t.Errorf("top, cursor = %d, %d; want 6, 6 (the cursor follows only when it would leave)", v.top, v.cursor)
		}

		r.a.Update(tea.MouseWheelMsg{X: 60, Y: 10, Button: tea.MouseWheelUp})

		if v.top != 3 {
			t.Errorf("top = %d after a notch up, want 3", v.top)
		}
	})

	t.Run("outside the box and clicks", func(t *testing.T) {
		t.Parallel()

		r := setup(t)
		v := &r.a.plan.view

		r.a.Update(tea.MouseWheelMsg{X: 1, Y: 1, Button: tea.MouseWheelDown})
		r.a.Update(tea.MouseWheelMsg{X: 60, Y: 39, Button: tea.MouseWheelDown})
		r.a.Update(tea.MouseClickMsg{X: 60, Y: 10, Button: tea.MouseLeft})
		r.a.Update(tea.MouseClickMsg{X: 1, Y: 1, Button: tea.MouseLeft})

		if v.top != 0 || v.cursor != 0 || r.a.plan == nil {
			t.Errorf("top, cursor = %d, %d, open %v; want nothing changed", v.top, v.cursor, r.a.plan != nil)
		}
	})
}

func TestPlanOverlayResize(t *testing.T) {
	t.Parallel()

	var src strings.Builder

	for i := 1; i <= 60; i++ {
		fmt.Fprintf(&src, "- item %d %s\n", i, strings.Repeat("word ", 30))
	}

	r := newPlanRig(t, 120, 40)
	r.filePlan(t, "docs/superpowers/plans/p.md", src.String())
	r.open(t)

	v := &r.a.plan.view
	v.goSource(30)

	before := v.layout().Source[v.cursor]
	if before.First > 30 || before.Last < 30 {
		t.Fatalf("setup: cursor range %+v does not hold 30", before)
	}

	for _, size := range [][2]int{{80, 24}, {40, 12}, {120, 40}} {
		sized(t, r.a, size[0], size[1])
		assertScreen(t, r.a, size[0], size[1])

		if got := v.layout().Source[v.cursor]; got.First > 30 || got.Last < 30 {
			t.Errorf("at %dx%d the cursor is on source %+v, want a line holding 30", size[0], size[1], got)
		}
	}
}

func TestPlanOverlayResizeSmallAndBack(t *testing.T) {
	t.Parallel()

	var src strings.Builder

	for i := 1; i <= 60; i++ {
		fmt.Fprintf(&src, "- item %d %s\n", i, strings.Repeat("word ", 30))
	}

	r := newPlanRig(t, 120, 40)
	r.filePlan(t, "docs/superpowers/plans/p.md", src.String())
	r.open(t)

	v := &r.a.plan.view
	v.goSource(30)

	sized(t, r.a, 20, 6)

	if s := assertScreen(t, r.a, 20, 6); strings.Contains(ansi.Strip(s), "item") {
		t.Error("a box too small to draw still shows the doc")
	}

	sized(t, r.a, 120, 40)
	assertScreen(t, r.a, 120, 40)

	if got := v.layout().Source[v.cursor]; got.First > 30 || got.Last < 30 {
		t.Errorf("back at 120x40 the cursor is on source %+v, want a line holding 30", got)
	}
}

func TestPlanOverlayHugeDoc(t *testing.T) {
	t.Parallel()

	unit := "A paragraph with a [link](https://a.example/x), **bold** and `code` in it.\n\n"

	r := newPlanRig(t, 120, 40)
	r.filePlan(t, "docs/superpowers/plans/p.md", strings.Repeat(unit, (5<<20)/len(unit)))

	msgs := planMsgs(r.ask())
	if len(msgs) != 1 || msgs[0].doc == nil || !msgs[0].doc.doc.Cut() {
		t.Fatalf("answers = %+v, want one parsed doc that is cut", msgs)
	}

	r.a.Update(msgs[0])

	if r.a.plan == nil {
		t.Fatal("the overlay did not open")
	}

	assertScreen(t, r.a, 120, 40)
	r.keys("G")
	assertScreen(t, r.a, 120, 40)
}

func TestPlanOpensOverDrawerModals(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		set  func(a *App, v *reviewView)
	}{
		{"link chooser", func(a *App, v *reviewView) {
			a.drawer.links = &linkChooser{root: "/w/p1", targets: []mdTarget{{Text: "a", URL: "https://example.com/a"}, {Text: "b", URL: "https://example.com/b"}}}
			a.drawer.linksOf = linksOwner{tab: a.store.State().Tab().ID, pane: "p1", view: v}
		}},
		{"worktree list", func(a *App, _ *reviewView) { showList(a, nil, "", 0) }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			root := realTemp(t)
			path := filepath.Join(root, "docs/plans/a.md")
			write(t, path, "# Title\n\nbody\n")

			s := reviewScene{
				theme: "catppuccin-mocha", w: 200, h: 52, branch: true, focused: true, res: mdResult(),
				tune: func(t *testing.T, a *App, v *reviewView) {
					t.Helper()

					openIn(t, a,
						core.SetPaneKind{ID: "p1", Kind: core.KindAgent, Harness: "claude-code"},
						core.NotePlan{ID: "p1", Plan: core.Plan{Path: path, Root: root, Kind: core.PlanFile, At: t0, Unread: true}},
					)
					tt.set(a, v)
				},
			}
			a := s.build(t)

			if a.drawer.links == nil && a.drawer.list == nil {
				t.Fatal("the drawer modal is not open")
			}

			msgs := planMsgs(press(a, keyLeader, keyRune('p')))
			if len(msgs) != 1 {
				t.Fatalf("leader p raised %d plan reads, want 1", len(msgs))
			}

			a.Update(msgs[0])

			if a.plan == nil {
				t.Fatal("the overlay did not open")
			}

			if a.store.State().Panes["p1"].Plan.Unread {
				t.Error("the plan is still unread while shown")
			}
		})
	}
}
