package ui

import (
	"errors"
	"fmt"
	"os"
	"slices"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/charmbracelet/x/exp/golden"

	"github.com/mhersson/aim/internal/core"
	"github.com/mhersson/aim/internal/github"
)

const ciJobID = 51234987

// ciChecks lists, in the order the overlay shows them: a failed Actions job, a
// commit status, a running Actions job, a passed one and a run of another app.
func ciChecks() []core.CheckInfo {
	started := t0.Add(-3*time.Minute - 4*time.Second)

	return []core.CheckInfo{
		{Name: "lint", URL: "https://ci.example.com/lint", State: "failure", ID: ciJobID, App: "github-actions", StartedAt: started, CompletedAt: started.Add(3*time.Minute + 4*time.Second)},
		{Name: "vet", State: "failure"},
		{Name: "e2e", URL: "https://ci.example.com/e2e", State: "pending", ID: 9, App: "github-actions", StartedAt: t0.Add(-64 * time.Second)},
		{Name: "build", URL: "https://ci.example.com/build", State: "success", ID: 11, App: "github-actions", StartedAt: started, CompletedAt: started.Add(12 * time.Second)},
		{Name: "docs", URL: "https://ci.example.com/docs", State: "skipped", ID: 12, App: "codecov"},
	}
}

func ciFixture(t *testing.T) []byte {
	t.Helper()

	data, err := os.ReadFile("testdata/joblog.txt")
	if err != nil {
		t.Fatal(err)
	}

	return data
}

// ciApp is the PR drawer with the checks overlay open on ciChecks, the first
// check selected, and reads that answer from the returned fake.
func ciApp(t *testing.T, icons bool, w, h int) (*App, *fakeReads, *fakeOpen) {
	t.Helper()

	s := reviewScene{
		theme: "catppuccin-mocha", icons: icons, w: w, h: h, res: turn4Result(), turn: 4, turns: 4, focused: true,
		branch: true, tune: func(t *testing.T, a *App, v *reviewView) {
			t.Helper()

			prSceneWith(nil, 0)(t, a, v)

			info, _ := prInfoByNumber(a.store.State(), prRepoKey, 7)
			openIn(t, a, core.SetRepoPRs{
				Repo: prRepoKey, DefaultBranch: "main", Open: []core.PRInfo{info},
				Focused: &core.PRChecks{Number: 7, HeadOID: info.HeadOID, Checks: ciChecks()},
			})

			a.checks = &checksView{repo: prRepoKey, number: 7}
		},
	}

	a := s.build(t)
	reads := &fakeReads{logData: ciFixture(t), fail: map[string]error{}}
	opener := &fakeOpen{}
	a.reads, a.openLink = reads, opener.open

	return a, reads, opener
}

// ciDeliver feeds the messages a command produces to the app.
func ciDeliver(a *App, cmd tea.Cmd) {
	for _, m := range flatten(cmd) {
		a.Update(m)
	}
}

// ciPage opens the page of the first check and delivers its log.
func ciPage(t *testing.T, a *App) {
	t.Helper()

	ciDeliver(a, press(a, keyRune('l')))

	if a.checks.page == nil || a.checks.page.loading {
		t.Fatalf("l opened no loaded page; notice %q", a.notice)
	}
}

func shownText(a *App) string { return ansi.Strip(a.View().Content) }

func TestChecksLogKey(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		sel    int
		notice string
		page   bool
		reads  []jobLogCall
	}{
		{name: "a status", sel: 1, notice: fmt.Sprintf(noticeNoLog, "vet")},
		{name: "a running job", sel: 2, notice: noticeLogRunning},
		{name: "a failed job", sel: 0, page: true, reads: []jobLogCall{{repo: prRepo, id: ciJobID}}},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			a, reads, _ := ciApp(t, true, 160, 48)
			a.checks.sel = tc.sel
			ciDeliver(a, press(a, keyRune('l')))

			if got := a.checks.page != nil; got != tc.page {
				t.Errorf("page open = %v, want %v", got, tc.page)
			}

			if a.notice != tc.notice {
				t.Errorf("notice = %q, want %q", a.notice, tc.notice)
			}

			if got := reads.logReads(); !slices.Equal(got, tc.reads) {
				t.Errorf("reads = %v, want %v", got, tc.reads)
			}
		})
	}

	t.Run("a skipped job", func(t *testing.T) {
		t.Parallel()

		a, reads, _ := ciApp(t, true, 160, 48)
		ciDeliver(a, a.checksLog(a.checks, checkRow{kind: checkSkipped, name: "gate", id: 13, app: actionsApp}))

		if want := fmt.Sprintf(noticeNoLog, "gate"); a.checks.page != nil || a.notice != want || len(reads.logReads()) != 0 {
			t.Errorf("page %v, notice %q (want %q), %d reads", a.checks.page, a.notice, want, len(reads.logReads()))
		}
	})

	t.Run("enter in the list", func(t *testing.T) {
		t.Parallel()

		a, reads, opener := ciApp(t, true, 160, 48)
		ciDeliver(a, press(a, keyEnter))

		if a.checks.page != nil || a.checks.sel != 0 || len(reads.logReads()) != 0 || len(opener.urls()) != 0 {
			t.Errorf("enter changed the list: page %v, sel %d, %d reads, opened %v", a.checks.page, a.checks.sel, len(reads.logReads()), opener.urls())
		}
	})
}

// ciGrouped is a log whose first error is inside a group, followed by enough
// lines to scroll.
func ciGrouped() []byte {
	var b strings.Builder

	b.WriteString("##[group]Set up\nsetup a\nsetup b\n##[endgroup]\n")
	b.WriteString("##[group]Run tests\ngo test\n##[error]boom\n##[endgroup]\n")

	for i := range 30 {
		fmt.Fprintf(&b, "after %d\n", i)
	}

	b.WriteString("##[error]second\n")

	return []byte(b.String())
}

func TestCILogOpensAtTheFirstError(t *testing.T) {
	t.Parallel()

	t.Run("an error", func(t *testing.T) {
		t.Parallel()

		a, reads, _ := ciApp(t, true, 80, 16)
		reads.logData = ciGrouped()

		ciPage(t, a)

		p := a.checks.page
		rows := p.rows()

		if line := p.log.Lines[rows[p.cursor].line]; line.Kind != github.LogError || line.Spans[0].Text == "" {
			t.Fatalf("the cursor is on %+v, want the first error", line)
		}

		if p.log.Lines[rows[p.cursor].line].Group != 1 || p.top != p.cursor-logLead {
			t.Errorf("top = %d with the cursor at %d, want %d above it", p.top, p.cursor, logLead)
		}
	})

	t.Run("no error", func(t *testing.T) {
		t.Parallel()

		a, reads, _ := ciApp(t, true, 80, 16)
		reads.logData = []byte("one\ntwo\nthree\nfour\nfive\nsix\nseven\neight\nnine\nten\n")

		ciPage(t, a)

		p := a.checks.page
		if p.cursor != len(p.rows())-1 || p.top != len(p.rows())-a.logPageRows() {
			t.Errorf("cursor %d, top %d of %d rows; want the last row at the end", p.cursor, p.top, len(p.rows()))
		}
	})
}

func TestCILogFolds(t *testing.T) {
	t.Parallel()

	a, reads, _ := ciApp(t, true, 160, 48)
	reads.logData = ciGrouped()

	ciPage(t, a)

	text := shownText(a)

	for _, want := range []string{"▸ Set up", "▾ Run tests", "go test", "Error: boom"} {
		if !strings.Contains(text, want) {
			t.Errorf("the page lacks %q:\n%s", want, text)
		}
	}

	if strings.Contains(text, "setup a") {
		t.Errorf("a closed group shows its lines:\n%s", text)
	}

	// Move to the closed group's head and open it.
	press(a, keyRune('g'))

	if line := a.checks.page.rows()[a.checks.page.cursor].line; line != 0 {
		t.Fatalf("the cursor is on line %d, want the first head", line)
	}

	press(a, keyEnter)

	if text := shownText(a); !strings.Contains(text, "▾ Set up") || !strings.Contains(text, "setup a") {
		t.Errorf("enter did not open the group:\n%s", text)
	}

	// j moves over the shown rows only.
	press(a, keyRune('j'))

	if line := a.checks.page.rows()[a.checks.page.cursor].line; line != 1 {
		t.Errorf("j moved to line %d, want 1 (the group's first line)", line)
	}

	press(a, keyRune('k'), keyEnter)

	if text := shownText(a); strings.Contains(text, "setup a") || !strings.Contains(text, "▸ Set up") {
		t.Errorf("enter did not close the group:\n%s", text)
	}

	// enter on a plain line changes nothing.
	press(a, keyRune('G'))

	before := len(a.checks.page.rows())
	press(a, keyEnter)

	if len(a.checks.page.rows()) != before {
		t.Error("enter on a plain line changed the rows")
	}
}

func TestCILogErrors(t *testing.T) {
	t.Parallel()

	a, reads, _ := ciApp(t, true, 160, 48)
	reads.logData = ciGrouped()

	ciPage(t, a)

	p := a.checks.page
	first := p.cursor
	text := func() string {
		return p.log.Lines[p.rows()[p.cursor].line].Spans[0].Text
	}

	press(a, keyRune(']'))

	if p.cursor <= first || text() != "Error: second" {
		t.Fatalf("] went to row %d (%q), want the second error", p.cursor, text())
	}

	last := p.cursor

	press(a, keyRune(']'))

	if p.cursor != last {
		t.Errorf("] past the last error moved to %d", p.cursor)
	}

	press(a, keyRune('['))

	if p.cursor != first {
		t.Errorf("[ went to %d, want the first error at %d", p.cursor, first)
	}

	press(a, keyRune('['))

	if p.cursor != first {
		t.Errorf("[ past the first error moved to %d", p.cursor)
	}

	// An error inside a closed group is not visited: close the group, then go
	// from the top to the next error.
	press(a, keyRune('k'), keyRune('k'), keyEnter, keyRune('g'), keyRune(']'))

	if text() != "Error: second" {
		t.Errorf("] from the top landed on %q with the first error's group closed", text())
	}
}

func TestCILogBack(t *testing.T) {
	t.Parallel()

	a, _, _ := ciApp(t, true, 160, 48)
	press(a, keyRune('j'))
	press(a, keyRune('k'))
	ciPage(t, a)
	press(a, keyEsc)

	if a.checks == nil || a.checks.page != nil || a.checks.sel != 0 {
		t.Fatalf("esc left %+v, want the list with its selection", a.checks)
	}

	press(a, keyEsc)

	if a.checks != nil {
		t.Error("the second esc left the overlay open")
	}
}

func TestCILogStale(t *testing.T) {
	t.Parallel()

	t.Run("after esc", func(t *testing.T) {
		t.Parallel()

		a, _, _ := ciApp(t, true, 160, 48)
		cmd := press(a, keyRune('l'))
		press(a, keyEsc)
		ciDeliver(a, cmd)

		if a.checks.page != nil {
			t.Error("a log that landed after esc opened the page")
		}
	})

	t.Run("after the overlay closed", func(t *testing.T) {
		t.Parallel()

		a, _, _ := ciApp(t, true, 160, 48)
		cmd := press(a, keyRune('l'))
		press(a, keyEsc, keyEsc)
		ciDeliver(a, cmd)

		if a.checks != nil {
			t.Error("a log that landed after the overlay closed reopened it")
		}
	})

	t.Run("after a second l", func(t *testing.T) {
		t.Parallel()

		a, reads, _ := ciApp(t, true, 160, 48)
		reads.logData = []byte("old\n")
		first := press(a, keyRune('l'))
		press(a, keyEsc)

		reads.logData = []byte("new\n")
		second := press(a, keyRune('l'))

		firstMsgs, secondMsgs := flatten(first), flatten(second)
		reads.logData = nil

		for _, m := range firstMsgs {
			a.Update(m)
		}

		if p := a.checks.page; !p.loading || len(p.log.Lines) != 0 {
			t.Fatalf("the first answer changed the page: loading %v, %d lines", p.loading, len(p.log.Lines))
		}

		for _, m := range secondMsgs {
			a.Update(m)
		}

		if p := a.checks.page; p.loading || len(p.log.Lines) != 1 || p.log.Lines[0].Spans[0].Text != "new" {
			t.Errorf("the second answer is not on the page: %+v", p.log.Lines)
		}
	})
}

func TestCILogRefreshKeepsThePlace(t *testing.T) {
	t.Parallel()

	a, _, _ := ciApp(t, true, 160, 48)
	ciPage(t, a)

	p := a.checks.page
	if len(p.log.Groups) == 0 {
		t.Fatal("the fixture has no groups")
	}

	p.open[0] = !logGroupOpen(p.log, p.open, 0)
	p.move(2, a.logPageRows())

	line := p.rows()[p.cursor].line
	want := p.open[0]

	ciDeliver(a, press(a, keyRune('r')))

	p = a.checks.page
	if got, ok := p.open[0]; !ok || got != want {
		t.Errorf("open[0] = %v (set %v), want %v", got, ok, want)
	}

	if got := p.rows()[p.cursor].line; got != line {
		t.Errorf("the cursor is on line %d, want %d", got, line)
	}
}

func TestCILogRefresh(t *testing.T) {
	t.Parallel()

	a, reads, _ := ciApp(t, true, 160, 48)
	ciPage(t, a)

	reads.logData = []byte("only line\n")

	ciDeliver(a, press(a, keyRune('r')))

	if got := reads.logReads(); len(got) != 2 {
		t.Fatalf("%d reads after r, want 2", len(got))
	}

	p := a.checks.page
	if len(p.log.Lines) != 1 || p.log.Lines[0].Spans[0].Text != "only line" {
		t.Errorf("the new answer did not replace the lines: %+v", p.log.Lines)
	}

	// A failed refresh says so, and r reads again.
	reads.logErr = errors.New("boom")

	ciDeliver(a, press(a, keyRune('r')))

	if text := shownText(a); !strings.Contains(text, fmt.Sprintf(logReadFailed, "boom")) {
		t.Errorf("the failure is not shown:\n%s", text)
	}

	reads.logErr = nil

	ciDeliver(a, press(a, keyRune('r')))

	if p := a.checks.page; p.err != "" || len(p.log.Lines) != 1 {
		t.Errorf("a refresh after a failure did not recover: err %q, %d lines", p.err, len(p.log.Lines))
	}
}

func TestCILogWheel(t *testing.T) {
	t.Parallel()

	a, reads, _ := ciApp(t, true, 80, 16)
	reads.logData = ciGrouped()

	ciPage(t, a)

	p := a.checks.page
	n := a.logPageRows()
	x, y, _, _, _ := logBox(a.width, a.height)

	press(a, keyRune('g'))

	if p.top != 0 || p.cursor != 0 {
		t.Fatalf("top %d, cursor %d after g, want 0, 0", p.top, p.cursor)
	}

	wheel := func(b tea.MouseButton) { a.Update(tea.MouseWheelMsg{X: x + 5, Y: y + 5, Button: b}) }

	wheel(tea.MouseWheelDown)

	if p.top != scrollStep || p.cursor != scrollStep {
		t.Errorf("top %d, cursor %d after a notch; want %d, %d (the cursor follows to stay in view)", p.top, p.cursor, scrollStep, scrollStep)
	}

	// The cursor stays while it is in view.
	kept := p.top + n - 1
	p.cursor = kept

	wheel(tea.MouseWheelDown)

	if p.top != 2*scrollStep || p.cursor != kept {
		t.Errorf("top %d, cursor %d; want the cursor kept at %d", p.top, p.cursor, kept)
	}

	wheel(tea.MouseWheelUp)

	if p.top != scrollStep {
		t.Errorf("top = %d after a notch up, want %d", p.top, scrollStep)
	}

	// Outside the box the wheel does nothing to the page.
	top := p.top

	a.Update(tea.MouseWheelMsg{X: 0, Y: 0, Button: tea.MouseWheelDown})

	if p.top != top {
		t.Errorf("the wheel outside the box moved the page to %d", p.top)
	}
}

func TestCILogDrawsNoEscapes(t *testing.T) {
	t.Parallel()

	a, reads, _ := ciApp(t, true, 160, 48)
	reads.logData = []byte("plain \x1b]52;c;ZXZpbA==\x07text\n" +
		"link \x1b]8;;https://evil.example/\x07click\x1b]8;;\x07 done\n" +
		"wipe \x1b[2Kgone \x1b[31mred\x1b[0m\n" +
		"##[error]bad \x1b]0;title\x07 thing\n")

	ciPage(t, a)

	raw := a.View().Content
	shown := ansi.Strip(raw)

	for _, bad := range []string{"\x1b]52", "evil.example", "\x1b[2K", "\x1b]0;"} {
		if strings.Contains(raw, bad) {
			t.Errorf("the frame holds %q", bad)
		}
	}

	if strings.Contains(raw, "\x1b]") {
		t.Error("the frame holds an OSC sequence")
	}

	for _, want := range []string{"plain", "click", "gone", "red", "Error: bad"} {
		if !strings.Contains(shown, want) {
			t.Errorf("the page lost the text %q:\n%s", want, shown)
		}
	}
}

func TestCILogRows(t *testing.T) {
	t.Parallel()

	l := github.ParseJobLog(ciGrouped(), true)

	rows := logRows(l, map[int]bool{})
	if rows[0].line != -1 {
		t.Errorf("a cut log's first row is line %d, want -1", rows[0].line)
	}

	// Closing the group that holds the first error hides its lines.
	flipped := logRows(l, map[int]bool{0: false})
	if len(flipped) == len(rows) {
		t.Errorf("flipping the groups changed no row: %d rows", len(rows))
	}
}

func TestAnsiColor(t *testing.T) {
	t.Parallel()

	p := mustTheme(t, "catppuccin-mocha").Palette
	want := map[int8]string{
		0: string(p.Surface1), 1: string(p.Red), 7: string(p.Subtext1), 8: string(p.Surface2),
		9: string(p.Red), 15: string(p.Subtext0), -1: "",
	}

	for i, w := range want {
		if got := string(ansiColor(i, p)); got != w {
			t.Errorf("ansiColor(%d) = %q, want %q", i, got, w)
		}
	}
}

// ciScene opens the page on the first check at the given size. mode says what
// the read answers: "log", "cut", "loading" (nothing delivered) or "error".
func ciScene(t *testing.T, icons bool, w, h int, mode string) *App {
	t.Helper()

	a, reads, _ := ciApp(t, icons, w, h)

	switch mode {
	case "cut":
		reads.logCut = true
	case "error":
		reads.logErr = errors.New("HTTP 502 Bad Gateway")
	}

	cmd := press(a, keyRune('l'))
	if mode != "loading" {
		ciDeliver(a, cmd)
	}

	return a
}

type ciSceneCase struct {
	name string
	w, h int
	mode string
}

func ciScenes() []ciSceneCase {
	return []ciSceneCase{
		{"failed-160x48", 160, 48, "log"},
		{"cut", 120, 30, "cut"},
		{"loading", 120, 30, "loading"},
		{"read-failed", 120, 30, "error"},
		{"80x24", 80, 24, "log"},
		{"40x12", 40, 12, "log"},
		{"20x8", 20, 8, "log"},
	}
}

// TestCILogGolden draws the page once in colour; it compares highlighted
// colours, so it runs serially.
func TestCILogGolden(t *testing.T) {
	for _, c := range ciScenes() {
		t.Run("catppuccin-mocha/icons=true/"+c.name, func(t *testing.T) {
			golden.RequireEqual(t, []byte(ciScene(t, true, c.w, c.h, c.mode).View().Content))
		})
	}
}

// TestCILogText is the icons=false screens without styling; the colour golden
// of the icons=true ones holds the same text.
func TestCILogText(t *testing.T) {
	t.Parallel()

	for _, c := range ciScenes() {
		t.Run("icons=false/"+c.name, func(t *testing.T) {
			t.Parallel()
			golden.RequireEqual(t, []byte(ansi.Strip(ciScene(t, false, c.w, c.h, c.mode).View().Content)))
		})
	}
}

func TestCILogSmall(t *testing.T) {
	t.Parallel()

	if rows, _, _, _ := logSplit(8); rows < logMinRows {
		t.Errorf("a box of 8 rows keeps %d log rows", rows)
	}

	// 20x8 draws nothing, and esc goes back.
	a := ciScene(t, true, 20, 8, "log")

	press(a, keyEsc)

	if a.checks == nil || a.checks.page != nil {
		t.Error("esc did not go back to the list")
	}
}
