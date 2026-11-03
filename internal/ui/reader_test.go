package ui

import (
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"

	"github.com/mhersson/aim/internal/core"
)

// readerHost opens one of the reading overlays and returns its app.
type readerHost struct {
	name string
	open func(t *testing.T, w, h int) *App
}

func readerHosts() []readerHost {
	return []readerHost{
		{"plan", func(t *testing.T, w, h int) *App {
			r := newPlanRig(t, w, h)
			r.filePlan(t, "plan.md", demoPlan(t))
			r.open(t)

			return r.a
		}},
		{"file", func(t *testing.T, w, h int) *App {
			r := newMDOpenRig(t, nil)
			sized(t, r.App, w, h)
			r.opens(t, "docs/a.md")

			return r.App
		}},
		{"conversation", func(t *testing.T, w, h int) *App {
			a, _ := convApp(t, true, w, h)
			convOpen(t, a)

			return a
		}},
	}
}

func readerOpen(a *App) bool { return a.plan != nil || a.conv != nil }

// readerMoved is the keys config the remap tests share.
func readerMoved() core.KeysConfig {
	return contextKeys("reader", "n", "reader.line.next", "j", "none", "q", "reader.close", "esc", "none")
}

func TestReaderRemappedKeys(t *testing.T) {
	t.Parallel()

	for _, h := range readerHosts() {
		t.Run(h.name, func(t *testing.T) {
			t.Parallel()

			t.Run("moves", func(t *testing.T) {
				t.Parallel()

				a := h.open(t, 120, 40)
				remapTables(t, a, readerMoved())

				v := a.readerView()
				start := v.cursor

				run(a, press(a, keyRune('j')))

				if v.cursor != start {
					t.Fatalf("j moved the cursor to %d from %d", v.cursor, start)
				}

				run(a, press(a, keyRune('n')))

				if v.cursor != start+1 {
					t.Fatalf("n moved the cursor to %d, want %d", v.cursor, start+1)
				}
			})

			t.Run("selection", func(t *testing.T) {
				t.Parallel()

				a := h.open(t, 120, 40)
				remapTables(t, a, readerMoved())
				fakeClip(a)

				rect, ok := planDocRect(a.width, a.height)
				if a.conv != nil {
					rect, ok = convDocRect(a.width, a.height)
				}

				if !ok {
					t.Fatal("no doc rect")
				}

				dragRun(a, rect.Min.X, rect.Min.Y, rect.Min.X+6, rect.Min.Y, 0)

				if a.text == nil {
					t.Fatal("the drag selected nothing")
				}

				run(a, press(a, keyRune('x')))

				if a.text == nil {
					t.Error("an unbound key cleared the selection")
				}

				run(a, press(a, keyRune('n')))

				if a.text != nil {
					t.Error("a bound key kept the selection")
				}
			})

			t.Run("prompt", func(t *testing.T) {
				t.Parallel()

				a := h.open(t, 120, 40)
				remapTables(t, a, readerMoved())

				run(a, press(a, keyRune('/')))
				run(a, press(a, keyRune('n')))

				query := func() (bool, string) {
					if a.plan != nil {
						return a.plan.searching, a.plan.query
					}

					return a.conv.searching, a.conv.query
				}

				if on, q := query(); !on || q != "n" {
					t.Fatalf("searching %v, query %q; want the prompt open on n", on, q)
				}

				run(a, press(a, keyEsc))

				if on, _ := query(); on || !readerOpen(a) {
					t.Fatalf("esc left the prompt open %v, overlay open %v", on, readerOpen(a))
				}
			})

			t.Run("close", func(t *testing.T) {
				t.Parallel()

				a := h.open(t, 120, 40)
				remapTables(t, a, readerMoved())

				run(a, press(a, keyEsc))

				if !readerOpen(a) {
					t.Fatal("esc closed the overlay though it is unbound")
				}

				run(a, press(a, keyRune('q')))

				if readerOpen(a) {
					t.Fatal("q left the overlay open")
				}
			})
		})
	}
}

func TestReaderChooserKeepsItsKeys(t *testing.T) {
	t.Parallel()

	r := linkPlan(t, 120, 40)
	remapTables(t, r.a, contextKeys("reader", "j", "none", "esc", "none", "q", "reader.close"))
	r.onLine(t, "first then second")

	r.page(r.enter())

	pv := r.a.plan
	if pv.chooser == nil {
		t.Fatal("enter opened no chooser")
	}

	r.send(planKey("j"))

	if pv.chooser.sel != 1 {
		t.Fatalf("j left the chooser on %d, want 1", pv.chooser.sel)
	}

	r.send(planKey("esc"))

	if r.a.plan == nil || pv.chooser != nil {
		t.Fatalf("esc: overlay open %v, chooser open %v; want only the chooser closed", r.a.plan != nil, pv.chooser != nil)
	}
}

func TestReaderTooSmall(t *testing.T) {
	t.Parallel()

	for _, h := range readerHosts() {
		t.Run(h.name, func(t *testing.T) {
			t.Parallel()

			a := h.open(t, 120, 40)
			remapTables(t, a, readerMoved())
			sized(t, a, 20, 8)

			for _, k := range []string{"esc", "j", "n"} {
				run(a, press(a, planKey(k)))

				if !readerOpen(a) {
					t.Fatalf("%s closed the overlay", k)
				}
			}

			run(a, press(a, keyRune('q')))

			if readerOpen(a) {
				t.Fatal("q left the overlay open")
			}
		})
	}
}

func TestReaderHintsFollowBindings(t *testing.T) {
	t.Parallel()

	cfg := contextKeys("reader", "n", "reader.line.next", "p", "reader.line.prev", "q", "reader.close", "esc", "none")

	for _, h := range readerHosts() {
		t.Run(h.name, func(t *testing.T) {
			t.Parallel()

			a := h.open(t, 120, 40)
			remapTables(t, a, cfg)

			rows := strings.Split(ansi.Strip(a.View().Content), "\n")
			hint := slices.IndexFunc(rows, func(r string) bool { return strings.Contains(r, "n/p line") })

			if hint < 0 || hint+1 >= len(rows) || !strings.Contains(rows[hint+1], "╰─") {
				t.Fatal("no hint row above the bottom border")
			}

			row := rows[hint]
			for _, want := range []string{"n/p line", "q close"} {
				if !strings.Contains(row, want) {
					t.Errorf("hint row %q lacks %q", row, want)
				}
			}

			for _, bad := range []string{"j/k", "esc close"} {
				if strings.Contains(row, bad) {
					t.Errorf("hint row %q holds %q", row, bad)
				}
			}
		})
	}
}

const gammaDoc = "# Doc\n\nAlpha paragraph\n\n## Section\n\nBeta paragraph\n\nGamma paragraph\n\nDelta paragraph\n"

// editHost opens a reading overlay on gammaDoc and returns its app and the
// starts of the panes it opens.
type editHost struct {
	name string
	open func(t *testing.T) (*App, *starts)
}

func editHosts() []editHost {
	return []editHost{
		{"plan file", func(t *testing.T) (*App, *starts) {
			r := newPlanRig(t, 120, 40)
			r.filePlan(t, "docs/p.md", gammaDoc)
			r.open(t)

			return r.a, r.st
		}},
		{"plan mode", func(t *testing.T) (*App, *starts) {
			r := newPlanRig(t, 120, 40)
			write(t, filepath.Join(r.home, ".claude", "plans", "demo.md"), gammaDoc)
			openIn(t, r.a, core.SetAgentCwd{ID: "p2", Cwd: r.wt})
			r.notePlan(t, core.Plan{Path: filepath.Join(r.home, ".claude", "plans", "demo.md"), Kind: core.PlanMode})
			r.open(t)

			return r.a, r.st
		}},
		{"page", func(t *testing.T) (*App, *starts) {
			r := newPlanRig(t, 120, 40)
			r.filePlan(t, "docs/p.md", "# Plan\n\n[next](next.md)\n")
			write(t, filepath.Join(r.wt, "docs", "next.md"), gammaDoc)
			r.open(t)
			r.onLine(t, "next")
			r.page(r.enter())

			if len(r.a.plan.pages) != 1 {
				t.Fatalf("following the link left %d pages, want 1", len(r.a.plan.pages))
			}

			return r.a, r.st
		}},
		{"markdown file", func(t *testing.T) (*App, *starts) {
			m := newMDOpenRig(t, map[string]string{"docs/a.md": gammaDoc})
			m.opens(t, "docs/a.md")

			return m.App, m.st
		}},
	}
}

// cursorOnGamma searches for Gamma and confirms, which puts the cursor on it.
func cursorOnGamma(a *App) {
	pressRun(a, keyRune('/'))
	typeText(a, "Gamma")
	pressRun(a, planKey("enter"))
}

func TestReaderEdit(t *testing.T) {
	t.Parallel()

	for _, h := range editHosts() {
		t.Run(h.name, func(t *testing.T) {
			t.Parallel()

			a, st := h.open(t)
			path, root := a.plan.shownPath(), a.plan.root

			src, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}

			want := 0

			for i, l := range strings.Split(string(src), "\n") {
				if strings.Contains(l, "Gamma") {
					want = i + 1
				}
			}

			cursorOnGamma(a)

			base := st.count()

			press(a, keyRune('e'))

			if a.plan != nil {
				t.Error("the overlay stayed open")
			}

			if got := st.count() - base; got != 1 {
				t.Fatalf("%d panes started, want 1", got)
			}

			c := st.cmds[base]
			if got, wantTail := c.Argv[len(c.Argv)-2:], []string{"+" + strconv.Itoa(want), path}; !slices.Equal(got, wantTail) {
				t.Errorf("the editor got %q, want %q", got, wantTail)
			}

			if c.Dir != root {
				t.Errorf("the pane's directory = %q, want %q", c.Dir, root)
			}
		})
	}
}

func TestReaderEditInConversation(t *testing.T) {
	t.Parallel()

	a, _ := convApp(t, true, 120, 40)
	convOpen(t, a)
	fakeClip(a)

	rect, ok := convDocRect(a.width, a.height)
	if !ok {
		t.Fatal("no doc rect")
	}

	dragRun(a, rect.Min.X, rect.Min.Y, rect.Min.X+6, rect.Min.Y, 0)

	if a.text == nil {
		t.Fatal("the drag selected nothing")
	}

	panes := len(a.store.State().Panes)

	press(a, keyRune('e'))

	if a.conv == nil {
		t.Error("e closed the conversation")
	}

	if a.text == nil {
		t.Error("e cleared the selection")
	}

	if got := len(a.store.State().Panes); got != panes {
		t.Errorf("e opened a pane: %d panes, was %d", got, panes)
	}
}

func TestReaderRefresh(t *testing.T) {
	t.Parallel()

	const added = "Fresh words here."

	withAdded := strings.Replace(gammaDoc, "Alpha paragraph\n", "Alpha paragraph\n\n"+added+"\n", 1)

	planRefresh := func(t *testing.T, mode bool) {
		r := newPlanRig(t, 120, 40)

		path := filepath.Join(r.home, ".claude", "plans", "demo.md")
		if mode {
			write(t, path, gammaDoc)
			openIn(t, r.a, core.SetAgentCwd{ID: "p2", Cwd: r.wt})
			r.notePlan(t, core.Plan{Path: path, Kind: core.PlanMode})
		} else {
			path = r.filePlan(t, "docs/p.md", gammaDoc)
		}

		r.open(t)
		cursorOnGamma(r.a)

		// The top sits on the section heading, which the added paragraph moves.
		v := r.a.plan.shown()
		v.top = slices.IndexFunc(v.doc.text(), func(l string) bool { return strings.Contains(l, "Section") })

		write(t, path, withAdded)
		pressRun(r.a, keyRune('r'))

		v = r.a.plan.shown()
		if !strings.Contains(strings.Join(v.doc.text(), "\n"), added) {
			t.Error("the new paragraph is not in the doc")
		}

		if line := v.doc.text()[v.cursor]; !strings.Contains(line, "Gamma") {
			t.Errorf("the cursor's line reads %q, want Gamma", line)
		}

		if strings.Contains(r.tagLine(t), planRewritten) {
			t.Errorf("the tag reads %q after r", r.tagLine(t))
		}
	}

	t.Run("plan file", func(t *testing.T) {
		t.Parallel()
		planRefresh(t, false)
	})

	t.Run("plan mode", func(t *testing.T) {
		t.Parallel()
		planRefresh(t, true)
	})

	t.Run("page", func(t *testing.T) {
		t.Parallel()

		r := newPlanRig(t, 120, 40)
		r.filePlan(t, "docs/p.md", "# Plan\n\n[next](next.md)\n")
		next := filepath.Join(r.wt, "docs", "next.md")
		write(t, next, gammaDoc)
		r.open(t)
		r.onLine(t, "next")
		r.page(r.enter())

		before := r.a.plan.view.doc

		write(t, next, withAdded)
		pressRun(r.a, keyRune('r'))

		if !strings.Contains(strings.Join(r.a.plan.shown().doc.text(), "\n"), added) {
			t.Error("the page shows no edit after r")
		}

		pressRun(r.a, keyEsc)

		if r.a.plan == nil || len(r.a.plan.pages) != 0 || r.a.plan.view.doc != before {
			t.Error("the plan below the page changed")
		}
	})

	t.Run("markdown file", func(t *testing.T) {
		t.Parallel()

		m := newMDOpenRig(t, map[string]string{"docs/a.md": gammaDoc})
		m.opens(t, "docs/a.md")
		write(t, filepath.Join(m.dir, "docs", "a.md"), withAdded)
		pressRun(m.App, keyRune('r'))

		if !strings.Contains(strings.Join(m.plan.shown().doc.text(), "\n"), added) {
			t.Error("the file shows no edit after r")
		}
	})

	t.Run("unchanged", func(t *testing.T) {
		t.Parallel()

		m := newMDOpenRig(t, map[string]string{"docs/a.md": gammaDoc})
		m.opens(t, "docs/a.md")

		before := m.plan.shown().doc
		pressRun(m.App, keyRune('r'))

		if m.plan.shown().doc != before {
			t.Error("r parsed an unchanged file again")
		}
	})

	for _, mode := range []bool{false, true} {
		name := "unchanged plan file"
		if mode {
			name = "unchanged plan mode"
		}

		t.Run(name, func(t *testing.T) {
			t.Parallel()

			r := newPlanRig(t, 120, 40)

			if mode {
				path := filepath.Join(r.home, ".claude", "plans", "demo.md")
				write(t, path, gammaDoc)
				openIn(t, r.a, core.SetAgentCwd{ID: "p2", Cwd: r.wt})
				r.notePlan(t, core.Plan{Path: path, Kind: core.PlanMode})
			} else {
				r.filePlan(t, "docs/p.md", gammaDoc)
			}

			r.open(t)

			before := r.a.readerView().doc
			pressRun(r.a, keyRune('r'))

			if r.a.readerView().doc != before {
				t.Error("r parsed an unchanged plan again")
			}
		})
	}

	t.Run("removed", func(t *testing.T) {
		t.Parallel()

		r := newPlanRig(t, 120, 40)
		path := r.filePlan(t, "docs/p.md", gammaDoc)
		r.open(t)

		before := r.a.plan.view.doc

		if err := os.Remove(path); err != nil {
			t.Fatal(err)
		}

		pressRun(r.a, keyRune('r'))

		if want := markdownNotice(r.wt, path, r.home, fs.ErrNotExist); r.a.notice != want {
			t.Errorf("notice = %q, want %q", r.a.notice, want)
		}

		if r.a.plan == nil || r.a.plan.view.doc != before {
			t.Error("the doc was not kept")
		}
	})

	t.Run("removed page", func(t *testing.T) {
		t.Parallel()

		r := newPlanRig(t, 120, 40)
		r.filePlan(t, "docs/p.md", "# Plan\n\n[next](next.md)\n")
		next := filepath.Join(r.wt, "docs", "next.md")
		write(t, next, gammaDoc)
		r.open(t)
		r.onLine(t, "next")
		r.page(r.enter())

		before := r.a.plan.shown().doc

		if err := os.Remove(next); err != nil {
			t.Fatal(err)
		}

		pressRun(r.a, keyRune('r'))

		if want := markdownNotice(r.wt, next, r.home, fs.ErrNotExist); r.a.notice != want {
			t.Errorf("notice = %q, want %q", r.a.notice, want)
		}

		if r.a.plan == nil || r.a.plan.shown().doc != before {
			t.Error("the page was not kept")
		}
	})

	t.Run("removed file", func(t *testing.T) {
		t.Parallel()

		m := newMDOpenRig(t, map[string]string{"docs/a.md": gammaDoc})
		m.opens(t, "docs/a.md")

		before := m.plan.shown().doc
		path := filepath.Join(m.dir, "docs", "a.md")

		if err := os.Remove(path); err != nil {
			t.Fatal(err)
		}

		pressRun(m.App, keyRune('r'))

		if want := markdownNotice(m.plan.root, path, m.getenv("HOME"), fs.ErrNotExist); m.notice != want {
			t.Errorf("notice = %q, want %q", m.notice, want)
		}

		if m.plan == nil || m.plan.shown().doc != before {
			t.Error("the file was not kept")
		}
	})

	t.Run("hook rewrite", func(t *testing.T) {
		t.Parallel()

		r := newPlanRig(t, 120, 40)
		path := r.filePlan(t, "docs/p.md", gammaDoc)
		r.open(t)

		msgs := r.rewrite(t, "p2", path, withAdded)
		if len(msgs) != 1 {
			t.Fatalf("the hook raised %d reads, want 1", len(msgs))
		}

		r.a.Update(msgs[0])

		if !strings.Contains(r.tagLine(t), planRewritten) {
			t.Errorf("the tag reads %q, want rewritten", r.tagLine(t))
		}
	})
}
