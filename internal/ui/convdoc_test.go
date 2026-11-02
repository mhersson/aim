package ui

import (
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/charmbracelet/x/ansi"

	"github.com/mhersson/aim/internal/git/invisible"
	"github.com/mhersson/aim/internal/github"
	"github.com/mhersson/aim/internal/md"
	"github.com/mhersson/aim/internal/ui/diffview"
)

var convNow = time.Date(2026, 10, 30, 12, 0, 0, 0, time.UTC)

const (
	convBase    = "https://github.com/octo/repo/pull/24"
	convShotSrc = "https://github.com/user-attachments/assets/e8b9a952"
	convShotURL = "https://private-user-images.githubusercontent.com/1/301-e8b9a952.png?jwt=abc"
)

const convHunk = "@@ -209,2 +209,5 @@\n \tcase a.drawer.tree:\n-\t\ta.reviewLeaveTree(v)\n+\t\tif k == keyTab {\n+\t\t\treturn a.reviewHideTree(v)\n+\t\t}"

func convAgo(d time.Duration) time.Time { return convNow.Add(-d) }

func convText(s string) github.Text { return github.Text{Markdown: s} }

// convFixture is the shape of the github package's conversation fixture.
func convFixture() github.Conversation {
	return github.Conversation{
		PR:      github.PR{Number: 24, Author: "mhersson", URL: convBase},
		Created: convAgo(3 * time.Hour),
		Body: github.Text{
			Markdown: "Fixes the drawer.\n\n![shot](" + convShotSrc + ")",
			Images:   map[string]string{convShotSrc: convShotURL},
		},
		Items: []github.TimelineItem{
			{Kind: github.ItemComment, ID: "IC_1", Author: "codecov[bot]", Bot: true, At: convAgo(2 * time.Hour), Body: convText("Coverage is 80%.")},
			{Kind: github.ItemCommits, ID: "aaa3", Author: "mhersson", At: convAgo(3 * time.Hour), Commits: 3},
			{
				Kind: github.ItemReview, ID: "PRR_1", Author: "kari-dev", At: convAgo(time.Hour), State: "CHANGES_REQUESTED", Body: convText("Please fix."),
				Comments: []github.ReviewComment{{Path: "internal/ui/reviewkeys.go", Line: 212, Hunk: convHunk, Body: convText("Tab should hide the tree.")}},
			},
			{Kind: github.ItemForcePush, ID: "FP_1", Author: "mhersson", At: convAgo(50 * time.Minute), Ref: "fix/review-drawer", Before: "b50f694", After: "4c37f33"},
			{Kind: github.ItemReviewRequest, ID: "RR_1", Author: "mhersson", At: convAgo(40 * time.Minute), Reviewer: "octo-reviewer"},
			{Kind: github.ItemReviewRequest, ID: "RR_2", Author: "mhersson", At: convAgo(40 * time.Minute), Reviewer: "acme/core"},
			{Kind: github.ItemReview, ID: "PRR_3", Author: "kari-dev", At: convAgo(30 * time.Minute), State: "APPROVED"},
			{Kind: github.ItemReview, ID: "PRR_4", Author: "pat", At: convAgo(25 * time.Minute), State: "COMMENTED", Body: convText("Looks fine.")},
			{Kind: github.ItemReview, ID: "PRR_5", Author: "sam", At: convAgo(20 * time.Minute), State: "DISMISSED", Body: convText("Old.")},
			{Kind: github.ItemMerged, ID: "ME_1", Author: "mhersson", At: convAgo(12 * time.Minute), Commit: "4c37f33", Base: "main"},
			{Kind: github.ItemClosed, ID: "CE_1", Author: "mhersson", At: convAgo(12 * time.Minute)},
		},
	}
}

func parseConv(c github.Conversation) *convDoc {
	return parseConversation(c, convBase, nil, nil, convNow)
}

// convPlain is the layout's lines without the padding that fills them to the width.
func convPlain(lay md.Layout) []string {
	out := lay.Plain()
	for i := range out {
		out[i] = strings.TrimRight(out[i], " ")
	}

	return out
}

func convLine(t *testing.T, lay md.Layout, s string) int {
	t.Helper()

	for i, p := range convPlain(lay) {
		if strings.Contains(p, s) {
			return i
		}
	}

	t.Fatalf("no line holds %q in\n%s", s, strings.Join(convPlain(lay), "\n"))

	return -1
}

// convRun is the first run whose trimmed text is text.
func convRun(t *testing.T, lay md.Layout, text string) md.Run {
	t.Helper()

	for _, l := range lay.Lines {
		for _, r := range l {
			if strings.TrimSpace(r.Text) == text {
				return r
			}
		}
	}

	t.Fatalf("no run holds %q", text)

	return md.Run{}
}

func TestConvDocItems(t *testing.T) {
	t.Parallel()

	d := parseConv(convFixture())
	lay := d.layout(96, true)

	roles := []struct {
		text string
		role md.Role
	}{
		{"requested changes", roleConvChanges},
		{"approved", roleConvApproved},
		{"reviewed", roleConvVerb},
		{"opened #24", roleConvVerb},
		{"commented", roleConvVerb},
		{"@kari-dev", roleConvAuthor},
		{"internal/ui/reviewkeys.go:212", roleConvPath},
		{"if k == keyTab {", roleHunkAdd},
		{"▸", md.RoleSummaryMark},
		{"enter opens", md.RoleFoldHint},
	}

	for _, r := range roles {
		if got := convRun(t, lay, r.text); got.Role != r.role {
			t.Errorf("%q has role %d, want %d", r.text, got.Role, r.role)
		}
	}
}

func TestConvDocNamedAuthor(t *testing.T) {
	t.Parallel()

	c := github.Conversation{PR: github.PR{Number: 1, Author: "a"}, Items: []github.TimelineItem{
		{Kind: github.ItemCommits, ID: "x", Author: "Kari Nordmann", Named: true, At: convAgo(3 * time.Hour), Commits: 3},
		{Kind: github.ItemCommits, ID: "y", Author: "kari", At: convAgo(3 * time.Hour), Commits: 1},
	}}
	plain := convPlain(parseConv(c).layout(96, true))

	for _, want := range []string{"  Kari Nordmann ", "  @kari "} {
		if !slices.ContainsFunc(plain, func(l string) bool { return strings.HasPrefix(l, want) }) {
			t.Errorf("no row starts %q in\n%s", want, strings.Join(plain, "\n"))
		}
	}
}

func TestConvDocHunkHighlight(t *testing.T) {
	t.Parallel()

	var gotPath string

	hl := func(path string, lines []string) [][]diffview.Span {
		gotPath = path

		out := make([][]diffview.Span, len(lines))
		for i, l := range lines {
			if len(l) > 1 {
				out[i] = []diffview.Span{{Start: 0, End: 1, Class: 3}}
			}
		}

		return out
	}

	lay := parseConversation(convFixture(), convBase, nil, hl, convNow).layout(96, true)

	if gotPath != "internal/ui/reviewkeys.go" {
		t.Errorf("highlighted %q", gotPath)
	}

	found := false

	for _, l := range lay.Lines {
		for _, r := range l {
			if r.Role == roleHunkAdd && r.Class == 3 {
				found = true
			}
		}
	}

	if !found {
		t.Error("no added hunk run carries its syntax class")
	}
}

func TestConvDocHunkRows(t *testing.T) {
	t.Parallel()

	// the last three lines: removed b (old 2), added c (new 2), context d (new 3)
	for name, hunk := range map[string]string{
		"no newline marker": "@@ -1,3 +1,3 @@\n a\n-b\n+c\n d\n\\ No newline at end of file",
		"trailing newline":  "@@ -1,3 +1,3 @@\n a\n-b\n+c\n d\n",
	} {
		c := github.Conversation{PR: github.PR{Number: 1, Author: "a"}, Items: []github.TimelineItem{
			{Kind: github.ItemReview, ID: "r", Author: "k", State: "COMMENTED", Comments: []github.ReviewComment{
				{Path: "f.go", Line: 3, Hunk: hunk, Body: convText("x")},
			}},
		}}
		plain := convPlain(parseConv(c).layout(60, true))
		all := strings.Join(plain, "\n")

		for _, want := range []string{"   2 - b", "   2 + c", "   3   d"} {
			if !strings.Contains(all, want) {
				t.Errorf("%s: no hunk row %q in\n%s", name, want, all)
			}
		}
	}
}

func TestConvDocWidths(t *testing.T) {
	t.Parallel()

	long := strings.Repeat("x", 200)
	table := "| a | b |\n|---|---|\n| 1 | 2 |\n| 長い | 日本語の文章 |"
	body := "日本語の長い文章がここにあります。折り返しが必要です。\n\n" + long + "\n\n" + table

	c := convFixture()
	c.Body = convText(body)
	c.Items = append(c.Items,
		github.TimelineItem{Kind: github.ItemComment, ID: "C2", Author: "日本語", At: convAgo(time.Hour), Body: convText(body)},
		github.TimelineItem{
			Kind: github.ItemReview, ID: "R2", Author: "kari", At: convAgo(time.Hour), State: "COMMENTED", Body: convText(body),
			Comments: []github.ReviewComment{{Path: "a/" + long + ".go", Line: 5, Hunk: "@@ -1 +1 @@\n+\t日本語" + long, Body: convText(body)}},
		},
		github.TimelineItem{Kind: github.ItemForcePush, ID: "F", Author: "mhersson", At: convAgo(time.Hour), Ref: long, Before: "a", After: "b"},
	)
	c.Cut = true

	for _, w := range []int{40, 61, 96} {
		d := parseConv(c)

		for _, open := range []bool{false, true} {
			if open {
				for _, f := range d.layout(w, true).Folds {
					d.toggleFold(f.ID)
				}
			}

			for i, l := range d.layout(w, true).Lines {
				got := 0
				for _, r := range l {
					got += ansi.StringWidth(r.Text)
				}

				if got != w {
					t.Fatalf("width %d (bots open %v): line %d is %d cells: %q", w, open, i, got, md.Layout{Lines: []md.Line{l}}.Plain()[0])
				}
			}
		}
	}
}

func TestConvDocFolds(t *testing.T) {
	t.Parallel()

	details := func(secret string) string {
		return "<details>\n<summary>S</summary>\n\n" + secret + "\n\n</details>"
	}

	c := convFixture()
	c.Body = convText(details("secret one"))
	c.Items[0].Body = convText("Coverage is 80%.")
	c.Items = append(c.Items, github.TimelineItem{Kind: github.ItemComment, ID: "C2", Author: "pat", At: convAgo(time.Hour), Body: convText(details("secret two"))})

	d := parseConv(c)
	lay := d.layout(96, true)

	row := convLine(t, lay, "@codecov[bot] commented")
	bot := slices.IndexFunc(lay.Folds, func(f md.Fold) bool { return f.Line == row })

	if bot < 0 {
		t.Fatalf("no fold on the bot row %d: %+v", row, lay.Folds)
	}

	d.toggleFold(lay.Folds[bot].ID)
	lay = d.layout(96, true)

	if h := convPlain(lay)[row]; !strings.HasPrefix(h, "╭─") || !strings.Contains(h, "▾") || !strings.Contains(h, "@codecov[bot]") {
		t.Errorf("opened bot header = %q", h)
	}

	if f := slices.IndexFunc(lay.Folds, func(f md.Fold) bool { return f.Line == row && f.Open }); f < 0 {
		t.Errorf("the open bot box has no fold on its top line: %+v", lay.Folds)
	}

	d.toggleFold(lay.Folds[slices.IndexFunc(lay.Folds, func(f md.Fold) bool { return f.Line == row })].ID)

	if strings.Contains(convPlain(d.layout(96, true))[row], "▾") {
		t.Error("the bot item did not fold again")
	}

	// the second comment's details open alone
	lay = d.layout(96, true)
	at := convLine(t, lay, "@pat commented")
	two := slices.IndexFunc(lay.Folds, func(f md.Fold) bool { return f.Line > at })

	if two < 0 {
		t.Fatalf("no fold below the second comment's header: %+v", lay.Folds)
	}

	one := slices.IndexFunc(lay.Folds, func(f md.Fold) bool { return f.Line < row })
	if one < 0 || lay.Folds[one].ID == lay.Folds[two].ID {
		t.Fatalf("folds %+v do not tell the bodies apart", lay.Folds)
	}

	d.toggleFold(lay.Folds[two].ID)

	all := strings.Join(convPlain(d.layout(96, true)), "\n")
	if !strings.Contains(all, "secret two") || strings.Contains(all, "secret one") {
		t.Errorf("toggling the second body's fold: two shown %v, one shown %v", strings.Contains(all, "secret two"), strings.Contains(all, "secret one"))
	}
}

func TestConvDocLinks(t *testing.T) {
	t.Parallel()

	c := convFixture()
	c.Body = convText("see [first](https://example.com/first)")
	c.Items = append(c.Items, github.TimelineItem{Kind: github.ItemComment, ID: "C2", Author: "pat", At: convAgo(time.Hour), Body: convText("see [second](https://example.com/second)")})

	lay := parseConv(c).layout(96, true)
	line := convLine(t, lay, "second")

	link := -1

	for _, r := range lay.Lines[line] {
		if r.Text == "second" {
			link = r.Link
		}
	}

	if link < 0 || link >= len(lay.Links) || lay.Links[link].URL != "https://example.com/second" {
		t.Fatalf("the run's link %d is not the second body's URL in %+v", link, lay.Links)
	}

	got := lineTargets(lay, line)
	if len(got) != 1 || got[0].URL != "https://example.com/second" {
		t.Errorf("targets = %+v", got)
	}

	if first := lineTargets(lay, convLine(t, lay, "first")); len(first) != 1 || first[0].URL != "https://example.com/first" {
		t.Errorf("first targets = %+v", first)
	}

	for _, r := range lay.Lines[0] {
		if r.Link != -1 {
			t.Errorf("frame run %q links %d, want -1", r.Text, r.Link)
		}
	}
}

func TestConvDocPictures(t *testing.T) {
	t.Parallel()

	const (
		src2 = "https://github.com/user-attachments/assets/bbb"
		url2 = "https://private-user-images.githubusercontent.com/1/2-bbb.png?jwt=x"
		src3 = "https://github.com/user-attachments/assets/ccc"
	)

	c := convFixture()
	c.Items = []github.TimelineItem{
		{
			Kind: github.ItemComment, ID: "C2", Author: "pat", At: convAgo(time.Hour),
			Body: github.Text{Markdown: "![two](" + src2 + ")", Images: map[string]string{src2: url2}},
		},
		{
			Kind: github.ItemReview, ID: "R", Author: "kari", At: convAgo(time.Hour), State: "COMMENTED",
			Comments: []github.ReviewComment{{Path: "a.go", Line: 1, Hunk: "@@ -1 +1 @@\n+x", Body: convText("![three](" + src3 + ")")}},
		},
	}

	reg, jobs, _ := onPictures(t)
	d := parseConv(c).withPictures(docPictures{reg: reg, host: "github.com"})
	d.layout(96, true)

	var keys []string
	for _, j := range jobs.jobs {
		keys = append(keys, j.key.src)
	}

	for _, want := range []string{convShotURL, url2, src3} {
		if !slices.Contains(keys, want) {
			t.Errorf("no job for %q in %v", want, keys)
		}
	}

	if slices.Contains(keys, src2) || slices.Contains(keys, convShotSrc) {
		t.Errorf("a source with an alias loaded as written: %v", keys)
	}

	answerDoc(t, reg, jobs, 50, 160)

	lay := d.layout(96, true)
	if len(lay.Images) != 3 {
		t.Fatalf("images = %+v", lay.Images)
	}

	if _, ok := d.pictureMove(); !ok {
		t.Error("a picture-driven relayout gave no pictureMove")
	}

	if _, ok := d.pictureMove(); ok {
		t.Error("pictureMove answered twice")
	}

	for k, want := range []struct {
		src string
		col int
	}{{convShotSrc, 2}, {src2, 2}, {src3, 4}} {
		im := lay.Images[k]
		if im.Src != want.src {
			t.Errorf("image %d src = %q, want %q", k, im.Src, want.src)
		}

		if im.Rows != 8 {
			t.Fatalf("image %d has %d rows", k, im.Rows)
		}

		_, row, ok := pictureAt(lay, im.Line)
		if !ok || row != 0 {
			t.Errorf("image %d line %d is not a picture row", k, im.Line)

			continue
		}

		if got := pictureCol(lay.Lines[im.Line]); got != want.col {
			t.Errorf("image %d column = %d, want %d", k, got, want.col)
		}
	}
}

func TestConvDocItemJump(t *testing.T) {
	t.Parallel()

	d := parseConv(convFixture())
	lay := d.layout(96, true)

	review := convLine(t, lay, "@kari-dev requested changes")
	force := convLine(t, lay, "force-pushed")
	inside := convLine(t, lay, "Tab should hide the tree.")

	if got, ok := d.itemLine(inside, 1); !ok || got != force {
		t.Errorf("next from inside the review = %d, %v, want %d", got, ok, force)
	}

	if got, ok := d.itemLine(force, -1); !ok || got != review {
		t.Errorf("previous from the force push = %d, %v, want %d", got, ok, review)
	}

	if got, ok := d.itemLine(inside, -1); !ok || got != review {
		t.Errorf("previous from inside the review = %d, %v, want its top %d", got, ok, review)
	}

	if got, ok := d.itemLine(review, -1); !ok || got != review-1 {
		t.Errorf("previous from the review's top = %d, %v, want the row above %d", got, ok, review-1)
	}

	if _, ok := d.itemLine(len(lay.Lines)-1, 1); ok {
		t.Error("next past the last item answered")
	}

	if _, ok := d.itemLine(0, -1); ok {
		t.Error("previous before the first item answered")
	}

	for i := range lay.Lines {
		id, off := d.itemAt(i)
		if got := d.lineOf(id, off); got != i {
			t.Fatalf("line %d is item %q offset %d, which maps to %d", i, id, off, got)
		}
	}

	if id, off := d.itemAt(inside); id != "PRR_1" || off != inside-review {
		t.Errorf("itemAt(inside) = %q, %d", id, off)
	}

	if got := d.lineOf("gone", 3); got != -1 {
		t.Errorf("lineOf an unknown id = %d, want -1", got)
	}
}

func TestConvDocCut(t *testing.T) {
	t.Parallel()

	c := convFixture()

	if slices.Contains(convPlain(parseConv(c).layout(96, true)), "  "+convCutRow) {
		t.Error("an uncut conversation says earlier items are not loaded")
	}

	c.Cut = true
	lay := parseConv(c).layout(96, true)
	plain := convPlain(lay)
	bottom := slices.IndexFunc(plain, func(s string) bool { return strings.HasPrefix(s, "╰") })

	if bottom < 0 || plain[bottom+1] != "  "+convCutRow {
		t.Fatalf("the row after the description is %q, want the cut row", plain[bottom+1])
	}

	if r := convRun(t, lay, convCutRow); r.Role != roleConvMeta {
		t.Errorf("cut row role = %d", r.Role)
	}
}

func TestConvDocCleansText(t *testing.T) {
	t.Parallel()

	c := convFixture()
	c.PR.Author = "own\x1b[2Jer"
	c.Items = []github.TimelineItem{
		{Kind: github.ItemComment, ID: "a", Author: "a\x1b[31mb", At: convAgo(time.Hour), Body: convText("hi")},
		{Kind: github.ItemForcePush, ID: "b", Author: "x\u202ey", At: convAgo(time.Hour), Ref: "ref\u202egnp", Before: "b\x07", After: "a\x1b"},
		{Kind: github.ItemReviewRequest, ID: "c", Author: "x", At: convAgo(time.Hour), Reviewer: "who\x1b]52;c;x\x07"},
		{Kind: github.ItemMerged, ID: "d", Author: "x", At: convAgo(time.Hour), Commit: "c\x1b", Base: "ma\u202ein"},
		{Kind: github.ItemReview, ID: "e", Author: "k", State: "COMMENTED", At: convAgo(time.Hour), Comments: []github.ReviewComment{
			{Path: "pa\x07th.go", Line: 3, Hunk: "@@ -1 +1 @@\n+ev\x1b]52;c;x\x07il\u202e", Body: convText("x")},
		}},
		{Kind: github.ItemCommits, ID: "f", Author: "Kari\x1b N", Named: true, At: convAgo(time.Hour), Commits: 2},
	}

	lay := parseConv(c).layout(120, true)

	for i, l := range lay.Lines {
		for _, r := range l {
			for _, ch := range r.Text {
				if ch < 0x20 || ch == 0x7f || (ch >= 0x80 && ch < 0xa0) || invisible.Rune(ch) {
					t.Errorf("line %d holds U+%04X in %q", i, ch, r.Text)
				}
			}
		}
	}

	plain := strings.Join(convPlain(lay), "\n")
	for _, want := range []string{"a[31mb", "refgnp", "pa" + "th.go:3", "⟨U+001B⟩", "⟨U+202E⟩"} {
		if !strings.Contains(plain, want) {
			t.Errorf("no %q in\n%s", want, plain)
		}
	}

	push := convPlain(lay)[convLine(t, lay, "refgnp")]
	if f := strings.Fields(push); !slices.Contains(f, "b") || !slices.Contains(f, "a") {
		t.Errorf("the force push row lost its cleaned commits: %q", push)
	}
}

func TestConvDocEmptyBody(t *testing.T) {
	t.Parallel()

	c := convFixture()
	c.Body = github.Text{}
	lay := parseConv(c).layout(96, true)
	plain := convPlain(lay)

	if !strings.HasPrefix(plain[1], "│ "+convEmptyBody) {
		t.Errorf("line 1 = %q", plain[1])
	}

	if r := convRun(t, lay, convEmptyBody); r.Role != roleConvMeta {
		t.Errorf("role = %d, want meta", r.Role)
	}
}

func copyAll(d *convDoc, from, to int) string {
	var (
		lines []md.LineText
		picks []md.CellRange
	)

	for i := from; i <= to; i++ {
		lines = append(lines, d.lineText(i))
		picks = append(picks, md.CellRange{From: 0, To: 1000})
	}

	return md.CopyLines(lines, picks, false)
}

func TestConvDocLineText(t *testing.T) {
	t.Parallel()

	para := "alpha beta gamma delta epsilon zeta eta theta iota kappa lambda mu nu xi omicron pi rho sigma tau upsilon"

	c := convFixture()
	c.Body = github.Text{Markdown: para + "\n\n![shot](" + convShotSrc + ")", Images: map[string]string{convShotSrc: convShotURL}}
	c.Items[2].Comments[0].Hunk = convHunk
	c.Items = append(c.Items, github.TimelineItem{
		Kind: github.ItemComment, ID: "C2", Author: "pat", At: convAgo(time.Hour),
		Body: convText("![two](https://example.com/two.png)"),
	})

	d := parseConv(c)
	lay := d.layout(40, true)
	plain := convPlain(lay)

	bottom := slices.IndexFunc(plain, func(s string) bool { return strings.HasPrefix(s, "╰") })
	rows := 0

	for _, p := range plain[:bottom] {
		if strings.Contains(p, "alpha") || strings.Contains(p, "tau") || strings.Contains(p, "kappa") {
			rows++
		}
	}

	if rows < 3 {
		t.Fatalf("the paragraph wraps over %d rows, want 3 or more:\n%s", rows, strings.Join(plain, "\n"))
	}

	desc := copyAll(d, 0, bottom)
	if want := "@mhersson opened #24 · 3h\n" + para + "\n\n![shot](" + convShotSrc + ")"; desc != want {
		t.Errorf("description copies\n%q\nwant\n%q", desc, want)
	}

	for _, glyph := range []string{"│", "╭", "╰", "─"} {
		if strings.Contains(desc, glyph) {
			t.Errorf("the description copies %q", glyph)
		}
	}

	if lt := d.lineText(bottom); !lt.Skip {
		t.Errorf("the bottom border copies %+v", lt)
	}

	top := convLine(t, lay, "@kari-dev requested changes")
	end := convLine(t, lay, "Tab should hide the tree.")

	want := "@kari-dev requested changes · 1h\nPlease fix.\n\ninternal/ui/reviewkeys.go:212\n" +
		"\t\tif k == keyTab {\n\t\t\treturn a.reviewHideTree(v)\n\t\t}\nTab should hide the tree."
	if got := copyAll(d, top, end); got != want {
		t.Errorf("review copies\n%q\nwant\n%q", got, want)
	}

	all := copyAll(d, 0, len(lay.Lines)-1)
	for _, want := range []string{"![shot](" + convShotSrc + ")", "![two](https://example.com/two.png)"} {
		if !strings.Contains(all, want) {
			t.Errorf("the conversation does not copy %q:\n%s", want, all)
		}
	}

	// A bot's folded row copies its header; the leading spaces and marks stay out.
	if got := copyAll(d, convLine(t, lay, "enter opens"), convLine(t, lay, "enter opens")); got != "@codecov[bot] commented · 2h" {
		t.Errorf("bot row copies %q", got)
	}

	row := convLine(t, lay, "merged commit")
	if got := copyAll(d, row, row); got != "@mhersson merged commit 4c37f33 into main · 12m" {
		t.Errorf("event copies %q", got)
	}
}

func TestConvDocLineTextShiftsPieces(t *testing.T) {
	t.Parallel()

	c := convFixture()
	d := parseConv(c)
	lay := d.layout(96, true)
	line := convLine(t, lay, "Tab should hide the tree.")

	got := d.lineText(line)
	if len(got.Pieces) == 0 {
		t.Fatal("the comment line copies nothing")
	}

	if col := got.Pieces[0].Col; col != 4 {
		t.Errorf("the inline comment's first piece is at cell %d, want 4", col)
	}

	plain := lay.Plain()[line]
	if before, _, _ := strings.Cut(plain, "Tab"); utf8.RuneCountInString(before) != 4 {
		t.Errorf("the comment is drawn at cell %d, want 4", utf8.RuneCountInString(before))
	}
}

func TestConvDocGen(t *testing.T) {
	t.Parallel()

	d := parseConv(convFixture())
	steps := []struct {
		name string
		do   func()
		want int
	}{
		{"first", func() { d.layout(96, true) }, 1},
		{"same", func() { d.layout(96, true) }, 1},
		{"width", func() { d.layout(80, true) }, 2},
		{"fold", func() { d.toggleFold(d.layout(80, true).Folds[0].ID) }, 2},
		{"after fold", func() { d.layout(80, true) }, 3},
	}

	for _, s := range steps {
		s.do()

		if got := d.gen(); got != s.want {
			t.Fatalf("%s: %d layouts, want %d", s.name, got, s.want)
		}
	}

	if got := fmt.Sprint(d.text()[0]); !strings.HasPrefix(got, "╭─") {
		t.Errorf("text()[0] = %q", got)
	}
}

func TestConvRoleStyles(t *testing.T) {
	t.Parallel()

	th := mochaTheme(t)
	p := newMDPainter(th, th.UI.Bg)
	u := th.UI

	tests := []struct {
		role md.Role
		want string
	}{
		{roleConvFrame, string(u.Border)},
		{roleConvAuthor, string(u.Fg)},
		{roleConvVerb, string(u.Muted)},
		{roleConvApproved, string(u.Success)},
		{roleConvChanges, string(u.Warning)},
		{roleConvMeta, string(u.Subtle)},
		{roleConvPath, string(u.Dim)},
		{roleHunkAddGutter, string(u.Subtle)},
		{roleHunkDelGutter, string(u.Subtle)},
		{roleHunkCtxGutter, string(u.Subtle)},
		{roleHunkAdd, string(u.Fg)},
		{roleHunkDel, string(u.Fg)},
		{roleHunkCtx, string(u.Fg)},
	}

	for _, tc := range tests {
		st := p.style(md.Run{Role: tc.role})
		if got := string(colorHex(st.GetForeground())); got != strings.ToLower(tc.want) {
			t.Errorf("role %d foreground = %s, want %s", tc.role, got, tc.want)
		}
	}

	bg := func(r md.Role) string { return string(colorHex(p.style(md.Run{Role: r}).GetBackground())) }

	if got, want := bg(roleHunkAdd), string(mixHex(th.UI.Bg, u.Success, tintHunkLine)); got != want {
		t.Errorf("added ground = %s, want %s", got, want)
	}

	if got, want := bg(roleHunkDel), string(mixHex(th.UI.Bg, u.Error, tintHunkLine)); got != want {
		t.Errorf("deleted ground = %s, want %s", got, want)
	}

	if got := bg(roleHunkCtx); got != string(th.UI.Bg) {
		t.Errorf("context ground = %s, want the painter's", got)
	}

	if got := p.style(md.Run{Role: roleConvAuthor}); !got.GetBold() {
		t.Error("the author is not bold")
	}
}
