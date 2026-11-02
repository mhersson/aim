package ui

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/mhersson/aim/internal/core"
	"github.com/mhersson/aim/internal/git"
)

// hunkSpec builds a hunk from lines written as a kind character and the text,
// numbering them from the starts.
func hunkSpec(oldStart, newStart int, section string, specs ...string) git.Hunk {
	h := git.Hunk{OldStart: oldStart, NewStart: newStart, Section: section}
	o, n := oldStart, newStart

	for _, s := range specs {
		l := git.Line{Kind: git.LineKind(s[0]), Text: s[1:]}

		switch l.Kind {
		case git.Add:
			l.New = n
			n++
			h.NewLines++
		case git.Del:
			l.Old = o
			o++
			h.OldLines++
		default:
			l.Old, l.New = o, n
			o++
			n++
			h.OldLines++
			h.NewLines++
		}

		h.Lines = append(h.Lines, l)
	}

	return h
}

// fileSpec is a changed file with the totals its hunks add up to.
func fileSpec(path string, status git.Status, hunks ...git.Hunk) git.FileDiff {
	f := git.FileDiff{Path: path, Status: status, Hunks: hunks, OldMode: "100644", NewMode: "100644"}

	for _, h := range hunks {
		for _, l := range h.Lines {
			switch l.Kind {
			case git.Add:
				f.Added++
			case git.Del:
				f.Removed++
			}
		}
	}

	return f
}

const appGoPath = "internal/ui/app.go"

// turn4Result is the fourth turn of the sample session: four files, one of
// them renamed and one with hidden characters.
func turn4Result() git.Result {
	state := fileSpec("internal/core/state.go", git.Modified, hunkSpec(22, 22, "type Pane struct",
		" \tID    PaneID",
		" \tKind  PaneKind",
		" \tCwd   string",
		"-\tTitle string",
		"+\tTitle  string",
		"+\tReview *ReviewSource",
		" }",
		"+",
		"+// ReviewSource is the agent a review pane follows and how it diffs it.",
		"+type ReviewSource struct {",
		"+\tAgent PaneID",
		"+\tMode  ReviewMode",
		"+\tTurn  int // 0 is the latest",
		"+}",
	))
	app := fileSpec(appGoPath, git.Modified, hunkSpec(40, 40, "func (a *App) Init() tea.Cmd",
		" ",
		" // openReview opens a review pane beside an agent, or focuses the open one.",
		" func (a *App) openReview(ctx context.Context, agent core.PaneID) tea.Cmd {",
		"-\tif a.store.Pane(agent).Kind != core.KindAgent {",
		"-\t\treturn nil",
		"-\t}",
		"+\tp, ok := a.store.Pane(agent)",
		"+\tif !ok || p.Kind != core.KindAgent {",
		"+\t\treturn a.notify(\"a review pane needs an agent pane\")",
		"+\t}",
		" ",
		" \tif r, ok := a.reviews[agent]; ok {",
		" \t\treturn a.focus(r.pane)",
	))
	keymap := fileSpec("internal/ui/keymap.go", git.Renamed, hunkSpec(3, 3, "",
		" // defaultLeader maps the keys after the leader to commands.",
		" var defaultLeader = map[string]string{",
		" \t\"a\": \"rail.toggle\",",
		"+\t\"d\": \"review.toggle\",",
		" \t\"n\": \"agent.new\",",
		" \t\"s\": \"pane.split.down\",",
		" \t\"v\": \"pane.split.right\",",
	))
	keymap.OldPath = "internal/ui/keys.go"

	test := fileSpec("internal/ui/review_test.go", git.Added, hunkSpec(0, 1, "",
		"+package ui",
		"+",
		"+import \"testing\"",
		"+",
		"+func TestReviewTitle(t *testing.T) {",
		"+\twant := \"multiplex\u200b · feat/review-pane\"",
		"+\t// the title reads left to right: \u202eenap-weiver\u202c",
		"+\tif got := reviewTitle(); got != want {",
		"+\t\tt.Fatalf(\"title = %q, want %q\", got, want)",
		"+\t}",
		"+}",
	))

	return git.Result{Turn: 4, Live: true, Files: []git.FileDiff{state, app, keymap, test}}
}

// appGoFull is app.go whole, before and after, with two removed blocks.
func appGoFull() fullText {
	old := []string{
		"package ui",
		"",
		"// App is the Bubble Tea model.",
		"type App struct {",
		"\tstore   *core.Store",
		"\tnotice  string",
		"\trail    rail",
		"}",
		"",
		"func (a *App) openReview(ctx context.Context, agent core.PaneID) tea.Cmd {",
		"\tif a.store.Pane(agent).Kind != core.KindAgent {",
		"\t\treturn nil",
		"\t}",
		"",
		"\treturn a.watch(ctx, agent)",
		"}",
	}
	nw := []string{
		"package ui",
		"",
		"// App is the Bubble Tea model.",
		"type App struct {",
		"\tstore   *core.Store",
		"\ttoast   toast",
		"\treviews map[core.PaneID]*review",
		"\trail    rail",
		"}",
		"",
		"func (a *App) openReview(ctx context.Context, agent core.PaneID) tea.Cmd {",
		"\tp, ok := a.store.Pane(agent)",
		"\tif !ok || p.Kind != core.KindAgent {",
		"\t\treturn a.notify(\"a review pane needs an agent pane\")",
		"\t}",
		"",
		"\treturn a.watch(ctx, agent)",
		"}",
	}

	return newFullText(old, nw, nil)
}

// longPlain is a 1 MiB line of one printable character; longTags is a 1 MiB
// line of "a" and an escape byte, which cleans into about half a million tags.
func longPlain() string { return strings.Repeat("x", 1<<20) }

func longTags() string { return strings.Repeat("a\x1b", 1<<19) }

// longLines are the over-long lines of the hostile fixtures: one plain, one of
// tags. kilobyteLines is the same shape small enough to draw in full, the
// baseline a 1 MiB line must not cost much more than.
type longLines struct{ plain, tags string }

func megabyteLines() longLines { return longLines{longPlain(), longTags()} }

func kilobyteLines() longLines {
	return longLines{strings.Repeat("x", 1<<10), strings.Repeat("a\x1b", 1<<9)}
}

// pastBudgetLines are lines just past lineBudget, the smallest that prove the cut:
// one plain, one of tags.
func pastBudgetLines() longLines {
	return longLines{strings.Repeat("x", lineBudget+16), strings.Repeat("a\x1b", lineBudget/2+8)}
}

// hostileResult holds every kind of text that must never reach the terminal
// raw, in lines and in paths, and, in a hunk of their own, two pairs of 1 MiB
// lines. The highlighter draws a whole chunk plain once a line of it is past its
// limit, so those lines must not share a hunk with the rest.
func hostileResult() git.Result { return hostileResultOf(megabyteLines()) }

func hostileResultOf(long longLines) git.Result {
	body := fileSpec("cmd/hostile.go", git.Modified, hunkSpec(1, 1, "func main()",
		" package main",
		" ",
		"+\tfmt.Println(\"a\u200bb\")",
		"+// bidi \u202eevil\u202c",
		"+x := 1\x1b[31m red\x1b[0m",
		"+crlf line\r",
		"+mid\rline",
		"+c1 \u0085 and \u009b",
		"-lone \x9b byte and \xff\xfe",
		"-\x00nul\x07bell\x7f",
		"-\ttab\tseparated\tcolumns",
		"+\ttab\tseparated\tcolumns!",
		" tail",
	), hunkSpec(30, 34, "func tail()",
		" }",
		"-"+long.plain,
		"-"+long.tags,
		"+"+long.plain+"!",
		"+"+long.tags+"\r",
		" }",
	))
	pathy := fileSpec("dir/evil\nname\x1b[31m.go", git.Added, hunkSpec(0, 1, "", "+one"))
	zero := fileSpec("zero\u200bwidth/\u202erevers.go", git.Modified, hunkSpec(1, 1, "", "-a", "+b"))
	zero.OldPath = "old\u200bname.go"
	zero.Status = git.Renamed

	return git.Result{Turn: 2, Files: []git.FileDiff{body, pathy, zero}}
}

func hostileFullOf(long longLines) fullText {
	return newFullText(
		[]string{"a\x1b[2J", "gone\u202e", "same", long.tags},
		[]string{"a\x1b[2J", "new\u200b", "same", "tab\there\x07", long.plain},
		nil,
	)
}

// edgeResult has a binary file, a truncated one, a mode change and a small edit.
func edgeResult() git.Result {
	bin := git.FileDiff{Path: "assets/review.png", Status: git.Added, Binary: true, NewMode: "100644"}
	big := git.FileDiff{Path: "internal/git/testdata/large.golden", Status: git.Modified, Truncated: true, Added: 5210, Removed: 4980}
	mode := git.FileDiff{Path: "scripts/build.sh", Status: git.ModeChanged, OldMode: "100644", NewMode: "100755"}
	small := fileSpec("go.mod", git.Modified, hunkSpec(3, 3, "",
		" go 1.27.3",
		" ",
		" require (",
		"-\tcharm.land/bubbletea/v2 v2.1.0",
		"+\tcharm.land/bubbletea/v2 v2.1.2",
		" \tcharm.land/lipgloss/v2 v2.0.1",
		" )",
	))

	return git.Result{Turn: 3, Files: []git.FileDiff{bin, big, small, mode}}
}

// bigResult is one added Go file of n lines in a single hunk.
func bigResult(n int) (git.Result, fullText) {
	specs := make([]string, n)
	text := make([]string, n)

	for i := range specs {
		text[i] = fmt.Sprintf("func f%d() int { return %d } // line %d", i, i, i)
		specs[i] = "+" + text[i]
	}

	f := fileSpec("big.go", git.Added, hunkSpec(0, 1, "", specs...))

	return git.Result{Turn: 1, Files: []git.FileDiff{f}}, newFullText(nil, text, nil)
}

// reviewScene is a master with two workers under an open drawer on the master,
// holding res as turn n of m.
type reviewScene struct {
	theme   string
	icons   bool
	w, h    int
	rail    bool
	res     git.Result
	turn    int
	turns   int
	branch  bool
	noSnap  bool
	focused bool
	tree    bool // the drawer's file tree is open
	pinned  bool // the drawer is held on its root
	tune    func(t *testing.T, a *App, v *reviewView)
	keys    core.KeysConfig
}

func (s reviewScene) build(t *testing.T) *App {
	t.Helper()

	if !s.branch && s.turn > 0 {
		s.res.Turn = s.turn
	}

	a := newKeyedApp(t, s.theme, s.keys)
	a.icons = s.icons
	a.clock = &fakeClock{t: t0}

	sized(t, a, s.w, s.h)

	for i := range 3 {
		openIn(t, a, core.OpenPane{Kind: core.KindShell, Cwd: fmt.Sprintf("/w/p%d", i+1), Split: core.SplitVertical})
	}

	openIn(t, a, core.SetLayoutMode{Mode: core.ModeMaster, Area: a.bodyArea()}, core.FocusPane{ID: "p1"})

	// A wide master reaches under the drawer; the workers lie wholly under it.
	for range 3 {
		openIn(t, a, core.ResizeDir{Dir: core.Right})
	}

	if s.rail {
		openIn(t, a, core.SetRail{Open: true})
	}

	mode := core.ReviewTurn
	if s.branch {
		mode = core.ReviewBranch
	}

	openIn(t, a, core.OpenReview{Pane: "p1", Mode: mode})

	if s.pinned {
		openIn(t, a, core.PinReview{Root: "/w/p1"})
	}

	if s.tree {
		openIn(t, a, core.SetReviewTree{Open: true})
	}

	texts := map[core.PaneID]string{
		"p1": "$ claude\r\nreviewing the diff of the last turn\r\n",
		"p2": "$ go test ./...\r\nok  github.com/mhersson/aim/internal/ui  0.4s\r\n",
		"p3": "$ git status\r\n",
	}
	attachPanes(t, a, texts)

	// The drawer's pane is resolved to its root, as the watch bookkeeping leaves it.
	a.watches.panes["p1"] = &paneWatch{cwd: "/w/p1", want: "/w/p1"}

	v := a.reviewViewOf(a.store.State().Tab().ID)
	v.pane, v.mode, v.loaded = "p1", mode, true
	v.root, v.project, v.branch = "/w/p1", "multiplex", "feat/review-pane"
	v.res, v.gen, v.turns, v.noSnapshot = s.res, 1, s.turns, s.noSnap

	switch {
	case s.branch:
		v.src, v.base = git.BranchSource{Worktree: "/w/p1"}, "origin/main"
	default:
		v.src = git.TurnSource{Worktree: "/w/p1", Session: "s", Turn: s.turn}
	}

	v.rebuild(false)

	a.drawer.focused = s.focused

	if s.tune != nil {
		s.tune(t, a, v)
	}

	settleDrawer(t, a)

	return a
}

// settleDrawer runs the drawer's post-update work until it has nothing left to do.
func settleDrawer(t *testing.T, a *App) {
	t.Helper()

	for range 200 {
		cmd := a.drawerAfterUpdate()

		if runHighlight(t, a) {
			continue
		}

		if cmd == nil {
			return
		}
	}

	t.Fatal("the drawer never settled")
}

// runHighlight runs the batch the drawer started, if any, on the test's
// goroutine, applies its answer and reports whether one ran.
func runHighlight(t *testing.T, a *App) bool {
	t.Helper()

	j := a.drawer.hlJob
	if j == nil {
		return false
	}

	msg, ok := j.run(context.Background(), a.drawer.hl).(drawerSpansMsg)
	if !ok {
		t.Fatal("the batch gave no answer")
	}

	a.onDrawerSpans(msg)

	return true
}

// rowWith is the first row of the diff whose text holds sub.
func rowWith(t *testing.T, v *reviewView, sub string) int {
	t.Helper()

	for i, r := range v.rows {
		for _, s := range rowTexts(v.res, r) {
			if strings.Contains(s, sub) {
				return i
			}
		}
	}

	t.Fatalf("no row holds %q", sub)

	return 0
}
