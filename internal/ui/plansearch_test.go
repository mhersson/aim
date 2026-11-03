package ui

import (
	"fmt"
	"strconv"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/charmbracelet/x/exp/golden"

	"github.com/mhersson/aim/internal/md"
)

func TestMDPainterQuery(t *testing.T) {
	t.Parallel()

	th := mochaTheme(t)
	ui := th.UI
	p := newMDPainter(th, ui.Bg)
	plain := func(s string) string { return lipgloss.NewStyle().Foreground(ui.Fg).Background(ui.Bg).Render(s) }
	hit := func(s string) string {
		return lipgloss.NewStyle().Foreground(ui.Match).Background(ui.Bg).Bold(true).Render(s)
	}
	codeHit := func(s string) string {
		return lipgloss.NewStyle().Foreground(ui.Match).Background(ui.Surface).Bold(true).Render(s)
	}
	run := func(s string) md.Run { return md.Run{Text: s, Link: -1} }

	tests := []struct {
		name  string
		line  md.Line
		query string
		want  string
	}{
		{"one run", md.Line{run("a Foo b")}, "foo", plain("a ") + hit("Foo") + plain(" b")},
		{"across two runs", md.Line{run("a fo"), run("o b")}, "foo", plain("a ") + hit("fo") + hit("o") + plain(" b")},
		{"twice on a line", md.Line{run("ab ab")}, "ab", hit("ab") + plain(" ") + hit("ab")},
		{"the whole run", md.Line{run("foo")}, "foo", hit("foo")},
		{
			"code keeps its ground",
			md.Line{{Text: "x foo", Role: md.RoleCode, Link: -1}},
			"foo",
			lipgloss.NewStyle().Foreground(ui.Code).Background(ui.Surface).Render("x ") + codeHit("foo"),
		},
		{"no match", md.Line{run("abc")}, "zzz", plain("abc")},
		{"lower-casing changes the length", md.Line{run("İ foo")}, "foo", plain("İ foo")},
		{"empty query", md.Line{run("abc")}, "", plain("abc")},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			w := 0
			for _, r := range tt.line {
				w += ansi.StringWidth(r.Text)
			}

			if got := p.withQuery(tt.query).line(tt.line, nil, w); got != tt.want {
				t.Errorf("painted %q, want %q", got, tt.want)
			}

			// The painted width is the same with and without the query.
			for _, width := range []int{w, w + 5, max(w-3, 1)} {
				got := p.withQuery(tt.query).line(tt.line, nil, width)
				if ansi.StringWidth(got) != width || ansi.Strip(got) != ansi.Strip(p.line(tt.line, nil, width)) {
					t.Errorf("width %d: painted %q, want the text of %q", width, ansi.Strip(got), ansi.Strip(p.line(tt.line, nil, width)))
				}
			}
		})
	}
}

func TestMDViewFindPrev(t *testing.T) {
	t.Parallel()

	v := newTestView(movesDoc, 40, 10)
	lay := v.layout()
	gamma, delta := lineOf(lay, "Gamma"), lineOf(lay, "Delta")

	v.move(delta - v.cursor)

	if !v.findPrev("GAMMA") || v.cursor != gamma {
		t.Fatalf("findPrev GAMMA: cursor %d, want %d", v.cursor, gamma)
	}

	if !v.findPrev("gamma") || v.cursor != gamma {
		t.Errorf("findPrev from its own line must wrap back to it, got %d", v.cursor)
	}

	v.end(false)

	if !v.findPrev("delta") || v.cursor != delta {
		t.Errorf("findPrev did not wrap from the top, cursor %d, want %d", v.cursor, delta)
	}

	cur := v.cursor
	if v.findPrev("no such text") || v.findPrev("") || v.cursor != cur {
		t.Error("a miss or an empty query must change nothing")
	}
}

const searchPlan = "# Title\n\nalpha one\n\nbeta\n\nAlpha two\n\ngamma\n\n## Tail\n\nalpha three\n"

func searchRig(t *testing.T, w, h int) *planRig {
	t.Helper()

	r := newPlanRig(t, w, h)
	r.filePlan(t, "docs/superpowers/plans/p.md", searchPlan)
	r.open(t)

	return r
}

func (r *planRig) typed(s string) {
	for _, c := range s {
		r.a.Update(keyRune(c))
	}
}

func (r *planRig) cursorText() string {
	v := &r.a.plan.view

	return v.doc.text()[v.cursor]
}

// matchCount is how many cells paint in the Match colour.
func matchCount(t *testing.T, a *App) int {
	t.Helper()

	hex := string(a.theme.UI.Match)
	n := func(s string) string {
		v, err := strconv.ParseUint(s, 16, 8)
		if err != nil {
			t.Fatal(err)
		}

		return strconv.Itoa(int(v))
	}
	seq := fmt.Sprintf("38;2;%s;%s;%s", n(hex[1:3]), n(hex[3:5]), n(hex[5:7]))

	return strings.Count(a.View().Content, seq)
}

func TestPlanSearch(t *testing.T) {
	t.Parallel()

	t.Run("the prompt takes the hint row", func(t *testing.T) {
		t.Parallel()

		r := searchRig(t, 120, 40)
		r.keys("/")

		if s := r.shown(t); !strings.Contains(s, "/ ▏") {
			t.Errorf("empty prompt:\n%s", s)
		}

		r.typed("alp")
		r.a.Update(tea.PasteMsg{Content: "ha"})

		if s := r.shown(t); !strings.Contains(s, "/ alpha▏") {
			t.Errorf("typed and pasted:\n%s", s)
		}

		r.a.Update(tea.KeyPressMsg{Code: tea.KeyBackspace})

		if s := r.shown(t); !strings.Contains(s, "/ alph▏") {
			t.Errorf("after backspace:\n%s", s)
		}

		assertScreen(t, r.a, 120, 40)
	})

	t.Run("matches paint as typed", func(t *testing.T) {
		t.Parallel()

		r := searchRig(t, 120, 40)
		if n := matchCount(t, r.a); n != 0 {
			t.Fatalf("%d cells paint in the Match colour before any query", n)
		}

		r.keys("/")
		r.typed("alpha")

		if matchCount(t, r.a) == 0 {
			t.Error("no match painted while typing")
		}

		if r.a.plan.view.cursor != 0 {
			t.Error("typing moved the cursor")
		}
	})

	t.Run("enter finds, n and N step", func(t *testing.T) {
		t.Parallel()

		r := searchRig(t, 120, 40)
		r.keys("/")
		r.typed("alpha")
		r.keys("enter")

		if r.a.plan.searching || r.cursorText() != "alpha one" {
			t.Fatalf("after enter: searching %v, cursor on %q", r.a.plan.searching, r.cursorText())
		}

		if matchCount(t, r.a) == 0 {
			t.Error("the kept query is not painted")
		}

		if hint := r.a.keys.reader.hint("reader.line.next", "reader.line.prev"); !strings.Contains(r.shown(t), hint) {
			t.Errorf("the hints did not come back (no %q)", hint)
		}

		for _, want := range []string{"Alpha two", "alpha three", "alpha one"} {
			r.keys("n")

			if got := r.cursorText(); got != want {
				t.Errorf("n: cursor on %q, want %q", got, want)
			}
		}

		for _, want := range []string{"alpha three", "Alpha two"} {
			r.keys("N")

			if got := r.cursorText(); got != want {
				t.Errorf("N: cursor on %q, want %q", got, want)
			}
		}
	})

	t.Run("an empty enter repeats the last query", func(t *testing.T) {
		t.Parallel()

		r := searchRig(t, 120, 40)
		r.keys("/")
		r.typed("beta")
		r.keys("enter", "g", "/", "enter")

		if got := r.cursorText(); got != "beta" || r.a.plan.query != "beta" {
			t.Errorf("cursor on %q, query %q", got, r.a.plan.query)
		}
	})

	t.Run("a miss says so", func(t *testing.T) {
		t.Parallel()

		r := searchRig(t, 120, 40)
		r.keys("/")
		r.typed("zzz")
		r.keys("enter")

		if r.a.notice != noticeNotFound+"zzz" || r.a.plan == nil {
			t.Errorf("notice %q", r.a.notice)
		}

		r.keys("n")

		if r.a.notice != noticeNotFound+"zzz" {
			t.Errorf("n after a miss: notice %q", r.a.notice)
		}

		r.keys("j")

		if r.a.notice != "" {
			t.Errorf("a key kept the notice %q", r.a.notice)
		}
	})

	t.Run("n and N with no query do nothing", func(t *testing.T) {
		t.Parallel()

		r := searchRig(t, 120, 40)
		r.keys("n", "N")

		if r.a.plan.view.cursor != 0 || r.a.notice != "" {
			t.Errorf("cursor %d, notice %q", r.a.plan.view.cursor, r.a.notice)
		}
	})

	t.Run("esc cancels the prompt, then closes the overlay", func(t *testing.T) {
		t.Parallel()

		r := searchRig(t, 120, 40)
		r.keys("/")
		r.typed("alpha")
		r.keys("esc")

		if r.a.plan == nil || r.a.plan.searching || r.a.plan.query != "" {
			t.Fatalf("after esc while typing: %+v", r.a.plan)
		}

		if matchCount(t, r.a) != 0 {
			t.Error("the matches stayed painted")
		}

		r.keys("esc")

		if r.a.plan != nil {
			t.Error("the second esc did not close the overlay")
		}
	})

	t.Run("esc closes the overlay under a kept query", func(t *testing.T) {
		t.Parallel()

		r := searchRig(t, 120, 40)
		r.keys("/")
		r.typed("alpha")
		r.keys("enter", "esc")

		if r.a.plan != nil {
			t.Error("esc did not close the overlay")
		}
	})

	t.Run("the query is printable", func(t *testing.T) {
		t.Parallel()

		r := searchRig(t, 120, 40)
		r.keys("/")
		r.a.Update(tea.PasteMsg{Content: "a\x1b[31mb\nc"})

		if strings.ContainsAny(r.a.plan.query, "\x1b\n") {
			t.Errorf("query %q", r.a.plan.query)
		}

		assertScreen(t, r.a, 120, 40)
	})

	t.Run("a paste is dropped unless the prompt is open", func(t *testing.T) {
		t.Parallel()

		r := searchRig(t, 120, 40)
		r.a.Update(tea.PasteMsg{Content: "alpha"})

		if r.a.plan.query != "" || r.a.plan.searching {
			t.Errorf("a paste reached the closed prompt: %+v", r.a.plan)
		}
	})

	t.Run("a box without a hint row opens no prompt", func(t *testing.T) {
		t.Parallel()

		r := searchRig(t, 80, 9)
		if _, _, hint := planRows(5); hint {
			t.Fatal("setup: the box still shows the hint row")
		}

		r.keys("/", "a")

		if pv := r.a.plan; pv.searching || pv.query != "" {
			t.Errorf("searching %v, query %q; want no prompt and the letter dropped", pv.searching, pv.query)
		}

		assertScreen(t, r.a, 80, 9)
	})

	t.Run("a box too small to draw takes esc alone", func(t *testing.T) {
		t.Parallel()

		r := searchRig(t, 120, 40)
		sized(t, r.a, 20, 10)
		r.keys("/")

		if r.a.plan.searching {
			t.Error("the prompt opened on a hidden box")
		}
	})
}

func TestPlanSearchSizes(t *testing.T) {
	t.Parallel()

	for _, sz := range [][2]int{{80, 24}, {40, 12}} {
		t.Run(fmt.Sprintf("%dx%d", sz[0], sz[1]), func(t *testing.T) {
			t.Parallel()

			r := searchRig(t, sz[0], sz[1])
			r.keys("/")
			r.typed(strings.Repeat("alphabet ", 20))
			assertScreen(t, r.a, sz[0], sz[1])
		})
	}
}

func searchScene(t *testing.T, icons bool) *App {
	t.Helper()

	r := newPlanRig(t, 120, 40)
	r.a.icons = icons
	r.filePlan(t, "docs/superpowers/plans/p.md", demoPlan(t))
	chord(r.a, 'a')
	r.open(t)
	r.keys("/")
	r.typed("count")

	return r.a
}

// TestPlanSearchGolden compares highlighted colours, so it runs serially.
func TestPlanSearchGolden(t *testing.T) {
	t.Run("catppuccin-mocha/icons=true/plan-search", func(t *testing.T) {
		golden.RequireEqual(t, []byte(searchScene(t, true).View().Content))
	})
}

func TestPlanSearchText(t *testing.T) {
	t.Parallel()

	t.Run("icons=false/plan-search", func(t *testing.T) {
		t.Parallel()
		golden.RequireEqual(t, []byte(ansi.Strip(searchScene(t, false).View().Content)))
	})
}
