package ui

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mhersson/aim/internal/core"
	"github.com/mhersson/aim/internal/harness"
	"github.com/mhersson/aim/internal/md"
)

func layoutOf(src string) md.Layout { return parseMarkdown([]byte(src), "", nil).layout(80, false) }

func layoutLine(lay md.Layout, i int) string {
	var b strings.Builder

	for _, r := range lay.Lines[i] {
		b.WriteString(r.Text)
	}

	return b.String()
}

func lineIndex(t *testing.T, lay md.Layout, text string) int {
	t.Helper()

	for i := range lay.Lines {
		if strings.TrimSpace(layoutLine(lay, i)) == text {
			return i
		}
	}

	t.Fatalf("no line reads %q", text)

	return -1
}

func TestKeepPlace(t *testing.T) {
	t.Parallel()

	const old = "# Title\n\nintro\n\n## Alpha\n\nalpha text\n\n## Beta\n\nbeta one\n\nbeta two\n\n## Beta\n\nsecond beta\n\nmore\n\nlast\n"

	tests := []struct {
		name string
		old  string
		new  string
		// top and cursor of the old layout: the line holding topAt, then cur
		// lines below it.
		topAt     string
		cur       int
		wantTopAt string // the line the new top reads
		wantTop   int    // used when wantTopAt is empty
	}{
		{
			name: "a paragraph added above the top heading",
			old:  old, new: strings.Replace(old, "intro\n", "intro\n\nadded one\n\nadded two\n", 1),
			topAt: "Beta", cur: 2, wantTopAt: "Beta",
		},
		{
			name: "the offset below the heading is kept",
			old:  old, new: strings.Replace(old, "intro\n", "intro\n\nadded one\n", 1),
			topAt: "beta two", cur: 1, wantTopAt: "beta two",
		},
		{
			name: "a renamed heading falls back to the source line",
			old:  old, new: strings.Replace(old, "## Alpha", "## Gamma", 1),
			topAt: "alpha text", cur: 0, wantTopAt: "alpha text",
		},
		{
			name: "two headings with the same text keep the second",
			old:  old, new: strings.Replace(old, "intro\n", "intro\n\nadded\n", 1),
			topAt: "second beta", cur: 0, wantTopAt: "second beta",
		},
		{
			name: "a doc that got shorter clamps",
			old:  old, new: "# Title\n\nshort\n",
			topAt: "last", cur: 0, wantTop: 2,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			ol, nl := layoutOf(tt.old), layoutOf(tt.new)
			top := lineIndex(t, ol, tt.topAt)
			gotTop, gotCur := keepPlace(ol, nl, top, top+tt.cur)

			if tt.wantTopAt != "" {
				if got := strings.TrimSpace(layoutLine(nl, gotTop)); got != tt.wantTopAt {
					t.Errorf("top reads %q, want %q", got, tt.wantTopAt)
				}

				if gotCur-gotTop != tt.cur {
					t.Errorf("cursor offset = %d, want %d", gotCur-gotTop, tt.cur)
				}

				return
			}

			n := len(nl.Lines)
			if gotTop < 0 || gotTop > n-1 || gotCur < 0 || gotCur > n-1 {
				t.Errorf("top %d and cursor %d are not inside %d lines", gotTop, gotCur, n)
			}
		})
	}
}

// rewrite writes text over the plan file and delivers the edit hook, returning
// the plan answers it raised.
func (r *planRig) rewrite(t *testing.T, pane core.PaneID, path, text string) []planReadMsg {
	t.Helper()

	write(t, path, text)

	return planMsgs(hook(r.a, harness.Event{Pane: pane, Name: "PostToolUse", Edit: path}))
}

func (r *planRig) tagLine(t *testing.T) string {
	t.Helper()

	return strings.SplitN(r.shown(t), "\n", 4)[2]
}

func openPlanRig(t *testing.T, text string) (*planRig, string) {
	t.Helper()

	r := newPlanRig(t, 120, 40)
	r.a.plans = core.DefaultConfig().Plans
	path := r.filePlan(t, "docs/superpowers/plans/p.md", text)
	chord(r.a, 'a')
	r.open(t)

	if r.a.plan == nil {
		t.Fatal("the overlay did not open")
	}

	return r, path
}

func TestPlanRewriteShown(t *testing.T) {
	t.Parallel()

	r, path := openPlanRig(t, longPlan(t))
	v := &r.a.plan.view
	lay := v.layout()
	v.top = lineIndex(t, lay, "Paragraph 5.")
	v.cursor = v.top + 2
	v.clamp()

	top, cur := v.top, v.cursor

	if r.unread() {
		t.Fatal("opening left the plan unread")
	}

	r.clk.Set(t0.Add(10 * time.Minute))

	msgs := r.rewrite(t, "p2", path, strings.Replace(longPlan(t), "# Title\n", "# Title\n\nAdded sentence here.\n\nAnother added one.\n", 1))
	if len(msgs) != 1 || !msgs[0].reload {
		t.Fatalf("the rewrite raised %+v, want one reload", msgs)
	}

	if r.unread() {
		t.Error("a rewrite of the shown plan was recorded unread")
	}

	r.a.Update(msgs[0])

	if !strings.Contains(strings.Join(v.doc.text(), "\n"), "Added sentence here.") {
		t.Fatal("the new text did not replace the old")
	}

	lay = v.layout()
	if got := strings.TrimSpace(layoutLine(lay, v.top)); got != "Paragraph 5." {
		t.Errorf("top reads %q, want Paragraph 5.", got)
	}

	if v.cursor-v.top != cur-top {
		t.Errorf("cursor offset = %d, want %d", v.cursor-v.top, cur-top)
	}

	cursor := v.cursor

	r.clk.Set(t0.Add(13 * time.Minute))
	r.keys("j")

	if v.cursor != cursor+1 {
		t.Errorf("j left the cursor at %d, want %d", v.cursor, cursor+1)
	}

	if tag := r.tagLine(t); strings.Contains(tag, planRewritten) || !strings.Contains(tag, planKindWord(core.PlanFile)+" · 3m") {
		t.Errorf("tag after the next key = %q, want the age", tag)
	}

	if r.unread() {
		t.Error("the plan came back unread")
	}
}

func TestPlanRewriteOther(t *testing.T) {
	t.Parallel()

	r, path := openPlanRig(t, demoPlan(t))
	before := r.a.plan.view.doc

	other := filepath.Join(r.wt, "docs/superpowers/specs/q.md")
	if msgs := r.rewrite(t, "p2", other, "# Q\n"); len(msgs) != 0 {
		t.Errorf("a plan with another path raised %d reads", len(msgs))
	}

	if !r.unread() {
		t.Error("a new plan with another path was not recorded unread")
	}

	if r.a.plan.view.doc != before || r.a.plan.rewritten {
		t.Error("the overlay changed for another plan")
	}

	// The same path on a pane that is not the one shown.
	if msgs := r.rewrite(t, "p1", path, "# Elsewhere\n"); len(msgs) != 0 {
		t.Errorf("an edit for another pane raised %d reads", len(msgs))
	}
}

func TestPlanRewriteUnderPage(t *testing.T) {
	t.Parallel()

	r := linkPlan(t, 120, 40)
	r.a.plans = core.DefaultConfig().Plans
	r.onLine(t, "the b page")
	r.page(r.enter())

	if len(r.a.plan.pages) != 1 {
		t.Fatal("the page did not open")
	}

	path := filepath.Join(r.wt, planLinkPath)

	msgs := r.rewrite(t, "p2", path, planLinkDoc+"\nAppended below.\n")
	if len(msgs) != 1 {
		t.Fatalf("the rewrite under a page raised %d reads, want 1", len(msgs))
	}

	r.a.Update(msgs[0])

	pv := r.a.plan
	if len(pv.pages) != 1 || !strings.Contains(r.shown(t), "Bee") {
		t.Error("the page did not stay")
	}

	if !strings.Contains(strings.Join(pv.view.doc.text(), "\n"), "Appended below.") {
		t.Error("the plan below the page did not reload")
	}
}

func TestPlanRewriteBurst(t *testing.T) {
	t.Parallel()

	r, path := openPlanRig(t, longPlan(t))
	v := &r.a.plan.view
	v.top = lineIndex(t, v.layout(), "Paragraph 5.")
	v.cursor = v.top + 1
	v.clamp()

	const n = 3

	msgs := make([]planReadMsg, 0, n)

	for i := 1; i <= n; i++ {
		text := strings.Replace(longPlan(t), "# Title\n", fmt.Sprintf("# Title\n\nRewrite number %d.\n", i), 1)

		got := r.rewrite(t, "p2", path, text)
		if len(got) != 1 {
			t.Fatalf("rewrite %d raised %d reads", i, len(got))
		}

		msgs = append(msgs, got[0])

		if r.unread() {
			t.Fatalf("rewrite %d came back unread", i)
		}
	}

	// An older answer lands before the newest, the newest, then another older one:
	// every older answer is stale and changes nothing, so the newest shows from i = 1.
	order := []planReadMsg{msgs[0], msgs[2], msgs[1]}

	for i, m := range order {
		r.a.Update(m)

		text := strings.Join(v.doc.text(), "\n")
		// Every answer but the newest is stale, whenever it lands.
		if has := strings.Contains(text, fmt.Sprintf("Rewrite number %d.", n)); has != (i >= 1) {
			t.Fatalf("after answer %d the newest rewrite shows = %v", i, has)
		}

		if strings.Contains(text, "Rewrite number 1.") || strings.Contains(text, "Rewrite number 2.") {
			t.Fatalf("after answer %d an older rewrite shows", i)
		}
	}

	if got := strings.TrimSpace(layoutLine(v.layout(), v.top)); got != "Paragraph 5." {
		t.Errorf("top reads %q, want Paragraph 5.", got)
	}

	if v.cursor != v.top+1 || r.unread() {
		t.Errorf("cursor offset %d, unread %v", v.cursor-v.top, r.unread())
	}
}

func TestPlanReloadLate(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		run      func(t *testing.T, r *planRig, path string) []planReadMsg // the answers, in order
		wantOpen bool
		wantText string // what the overlay shows after them, when open
		notice   string // a substring of the notice, or ""
	}{
		{"after esc", func(t *testing.T, r *planRig, path string) []planReadMsg {
			msgs := r.rewrite(t, "p2", path, "# New\n\nnewer text\n")
			r.keys("esc")

			return msgs
		}, false, "", ""},
		{"after a newer reload", func(t *testing.T, r *planRig, path string) []planReadMsg {
			first := r.rewrite(t, "p2", path, "# New\n\nfirst rewrite\n")
			second := r.rewrite(t, "p2", path, "# New\n\nsecond rewrite\n")

			return []planReadMsg{second[0], first[0]}
		}, true, "second rewrite", ""},
		{"after a reopen", func(t *testing.T, r *planRig, path string) []planReadMsg {
			msgs := r.rewrite(t, "p2", path, "# New\n\nnewer text\n")
			r.keys("esc")
			write(t, path, demoPlan(t))
			r.open(t)

			return msgs
		}, true, "Session Caching", ""},
		{"failed", func(t *testing.T, r *planRig, path string) []planReadMsg {
			msgs := r.rewrite(t, "p2", path, "# New\n")
			msgs[0].doc, msgs[0].err = nil, os.ErrNotExist

			return msgs
		}, true, "Session Caching", "no such file"},
		{"cancelled", func(t *testing.T, r *planRig, path string) []planReadMsg {
			msgs := r.rewrite(t, "p2", path, "# New\n")
			msgs[0].doc, msgs[0].err = nil, context.Canceled

			return msgs
		}, true, "Session Caching", ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			r, path := openPlanRig(t, demoPlan(t))
			before := r.a.plan

			for _, m := range tt.run(t, r, path) {
				r.a.Update(m)
			}

			if open := r.a.plan != nil; open != tt.wantOpen {
				t.Fatalf("overlay open = %v, want %v", open, tt.wantOpen)
			}

			if !tt.wantOpen {
				return
			}

			if tt.name != "after a reopen" && r.a.plan != before {
				t.Error("a reload replaced the overlay")
			}

			if text := strings.Join(r.a.plan.view.doc.text(), "\n"); !strings.Contains(text, tt.wantText) {
				t.Errorf("the overlay shows %q, want %q in it", text, tt.wantText)
			}

			if tt.notice == "" && r.a.notice != "" || !strings.Contains(r.a.notice, tt.notice) {
				t.Errorf("notice = %q, want it to hold %q", r.a.notice, tt.notice)
			}

			if tt.notice != "" && r.a.plan.rewritten {
				t.Error("a failed reload marked the plan rewritten")
			}
		})
	}
}

func TestPlanRewriteShownNotLatest(t *testing.T) {
	t.Parallel()

	t.Run("plan file", func(t *testing.T) {
		t.Parallel()

		r, path := openPlanRig(t, demoPlan(t))
		other := filepath.Join(r.wt, "docs/superpowers/specs/q.md")

		if msgs := r.rewrite(t, "p2", other, "# Q\n"); len(msgs) != 0 {
			t.Fatalf("another plan raised %d reads", len(msgs))
		}

		msgs := r.rewrite(t, "p2", path, "# Title\n\nFresh words here.\n")
		if len(msgs) != 1 || !msgs[0].reload {
			t.Fatalf("the edit raised %+v, want one reload", msgs)
		}

		r.a.Update(msgs[0])

		if !strings.Contains(strings.Join(r.a.plan.view.doc.text(), "\n"), "Fresh words here.") {
			t.Error("the new text did not replace the old")
		}

		if got := r.a.store.State().Panes["p2"].Plan; got.Path != path || got.Unread {
			t.Errorf("plan = %+v, want %s recorded read", got, path)
		}
	})

	t.Run("plan mode", func(t *testing.T) {
		t.Parallel()

		r := newPlanRig(t, 120, 40)
		r.a.plans = core.DefaultConfig().Plans
		mode := filepath.Join(r.wt, "mode", "x.md")
		write(t, mode, "# Mode\n")
		r.notePlan(t, core.Plan{Path: mode, Kind: core.PlanMode})
		chord(r.a, 'a')
		r.open(t)

		if r.a.plan == nil {
			t.Fatal("the overlay did not open")
		}

		newer := r.filePlan(t, "docs/superpowers/plans/n.md", "# N\n")

		msgs := r.rewrite(t, "p2", mode, "# Mode\n\nFresh words here.\n")
		if len(msgs) != 1 || !msgs[0].reload {
			t.Fatalf("the edit raised %+v, want one reload", msgs)
		}

		r.a.Update(msgs[0])

		if !strings.Contains(strings.Join(r.a.plan.view.doc.text(), "\n"), "Fresh words here.") {
			t.Error("the new text did not replace the old")
		}

		if got := r.a.store.State().Panes["p2"].Plan.Path; got != newer {
			t.Errorf("latest plan = %s, want %s", got, newer)
		}
	})
}
