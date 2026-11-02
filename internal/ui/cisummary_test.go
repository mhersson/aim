package ui

import (
	"errors"
	"fmt"
	"slices"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/charmbracelet/x/exp/golden"

	"github.com/mhersson/aim/internal/core"
	"github.com/mhersson/aim/internal/github"
)

const (
	codeqlID      = 77001
	codeqlSummary = "## CodeQL\n\n**No new alerts** in [the run](https://example.com/r).\n"
)

// sumApp is ciApp with a completed CodeQL run (not an Actions job) selected,
// whose read answers summary.
func sumApp(t *testing.T, icons bool, w, h int, summary string) (*App, *fakeReads, *fakeOpen) {
	t.Helper()

	a, reads, opener := ciApp(t, icons, w, h)

	info, _ := prInfoByNumber(a.store.State(), prRepoKey, 7)
	checks := append(ciChecks(), core.CheckInfo{Name: "CodeQL", URL: "https://ci.example.com/codeql", State: "success", ID: codeqlID, App: "github-code-scanning"})
	openIn(t, a, core.SetRepoPRs{
		Repo: prRepoKey, DefaultBranch: "main", Open: []core.PRInfo{info},
		Focused: &core.PRChecks{Number: 7, HeadOID: info.HeadOID, Checks: checks},
	})

	a.checks.sel = slices.IndexFunc(checkRows(a.checksOf(a.store.State(), a.checks), a.clock.Now()), func(r checkRow) bool { return r.name == "CodeQL" })
	reads.runOut = github.CheckRunOutput{Title: "CodeQL", Summary: summary}

	return a, reads, opener
}

// sumPage opens the summary of the CodeQL run and delivers its answer.
func sumPage(t *testing.T, a *App) {
	t.Helper()

	ciDeliver(a, press(a, keyRune('l')))

	if p := a.checks.page; p == nil || p.loading || p.summary == nil {
		t.Fatalf("l opened no loaded summary; notice %q", a.notice)
	}
}

func TestCheckSummary(t *testing.T) {
	t.Parallel()

	a, reads, opener := sumApp(t, true, 160, 48, codeqlSummary)
	sumPage(t, a)

	if got := reads.runReads(); len(got) != 1 || got[0].id != codeqlID || len(reads.logReads()) != 0 {
		t.Errorf("reads = %v (log reads %v), want one run read of %d", got, reads.logReads(), codeqlID)
	}

	v := a.checks.page.summary
	if v.cursor != 0 {
		t.Fatalf("cursor = %d, want 0", v.cursor)
	}

	press(a, keyRune('j'))

	if v.cursor != 1 {
		t.Errorf("j: cursor = %d, want 1", v.cursor)
	}

	press(a, keyRune('k'))

	if v.cursor != 0 {
		t.Errorf("k: cursor = %d, want 0", v.cursor)
	}

	for v.cursor < len(v.layout().Lines)-1 && len(v.targets()) == 0 {
		press(a, keyRune('j'))
	}

	ciDeliver(a, press(a, keyEnter))

	if got := opener.opened(t, 1); !slices.Equal(got, []string{"https://example.com/r"}) {
		t.Errorf("enter opened %v, want the link", got)
	}

	if a.checks.page == nil {
		t.Fatal("enter closed the page")
	}

	press(a, keyEsc)

	if a.checks == nil || a.checks.page != nil {
		t.Errorf("esc left %+v, want the list", a.checks)
	}
}

func TestCheckSummaryEmpty(t *testing.T) {
	t.Parallel()

	// "" takes the same TrimSpace branch.
	const sum = " \n\t\n"

	a, reads, _ := sumApp(t, true, 160, 48, sum)
	ciDeliver(a, press(a, keyRune('l')))

	if a.checks.page != nil {
		t.Error("a page opened")
	}

	if want := fmt.Sprintf(noticeNoLog, "CodeQL"); a.notice != want {
		t.Errorf("notice = %q, want %q", a.notice, want)
	}

	if len(reads.runReads()) != 1 {
		t.Errorf("%d reads, want 1", len(reads.runReads()))
	}
}

func TestCheckSummaryStale(t *testing.T) {
	t.Parallel()

	t.Run("after esc", func(t *testing.T) {
		t.Parallel()

		a, _, _ := sumApp(t, true, 160, 48, codeqlSummary)
		cmd := press(a, keyRune('l'))
		press(a, keyEsc)
		ciDeliver(a, cmd)

		if a.checks.page != nil {
			t.Error("a summary that landed after esc opened the page")
		}
	})

	t.Run("after the overlay closed", func(t *testing.T) {
		t.Parallel()

		a, _, _ := sumApp(t, true, 160, 48, codeqlSummary)
		cmd := press(a, keyRune('l'))
		press(a, keyEsc, keyEsc)
		ciDeliver(a, cmd)

		if a.checks != nil {
			t.Error("a summary that landed after the overlay closed reopened it")
		}
	})

	t.Run("after a newer read", func(t *testing.T) {
		t.Parallel()

		a, reads, _ := sumApp(t, true, 160, 48, "old text\n")
		first := flatten(press(a, keyRune('l')))

		reads.runOut.Summary = "new text\n"
		second := flatten(press(a, keyRune('r')))

		for _, m := range first {
			a.Update(m)
		}

		if p := a.checks.page; !p.loading || p.summary != nil {
			t.Fatalf("the first answer changed the page: loading %v", p.loading)
		}

		for _, m := range second {
			a.Update(m)
		}

		if text := shownText(a); !strings.Contains(text, "new text") || strings.Contains(text, "old text") {
			t.Errorf("the second answer is not shown:\n%s", text)
		}
	})
}

func TestCheckSummaryFailed(t *testing.T) {
	t.Parallel()

	a, reads, _ := sumApp(t, true, 160, 48, codeqlSummary)
	reads.runErr = errors.New("HTTP 502 Bad Gateway")

	ciDeliver(a, press(a, keyRune('l')))

	if text := shownText(a); !strings.Contains(text, fmt.Sprintf(summaryReadFailed, "HTTP 502 Bad Gateway")) {
		t.Errorf("no failure shown:\n%s", text)
	}

	reads.runErr = nil

	ciDeliver(a, press(a, keyRune('r')))

	if text := shownText(a); !strings.Contains(text, "No new alerts") {
		t.Errorf("r did not read again:\n%s", text)
	}
}

func TestCheckSummaryHostile(t *testing.T) {
	t.Parallel()

	sum := "[bad](javascript:alert(1))\n\nsee \x1b]52;c;AAAA\x07 and \x1b[2J here\n"
	a, _, opener := sumApp(t, true, 160, 48, sum)
	sumPage(t, a)

	ciDeliver(a, press(a, keyEnter))

	if got := opener.urls(); len(got) != 0 {
		t.Errorf("a javascript link opened %v", got)
	}

	out := a.View().Content
	for _, bad := range []string{"\x1b]52", "\x1b[2J", "\x07"} {
		if strings.Contains(out, bad) {
			t.Errorf("the screen holds %q", bad)
		}
	}
}

func TestCheckSummaryChooser(t *testing.T) {
	t.Parallel()

	a, _, opener := sumApp(t, true, 160, 48, "[one](https://example.com/1) and [two](https://example.com/2)\n")
	sumPage(t, a)

	press(a, keyEnter)

	if a.checks.page.chooser == nil {
		t.Fatal("enter on two links opened no chooser")
	}

	press(a, keyEsc)

	if a.checks.page == nil || a.checks.page.chooser != nil {
		t.Fatal("esc did not close the chooser alone")
	}

	press(a, keyEnter, keyRune('j'))
	ciDeliver(a, press(a, keyEnter))

	if got := opener.opened(t, 1); !slices.Equal(got, []string{"https://example.com/2"}) {
		t.Errorf("opened %v, want the second link", got)
	}
}

func TestCheckSummaryScroll(t *testing.T) {
	t.Parallel()

	var b strings.Builder
	for i := range 60 {
		fmt.Fprintf(&b, "line %d\n\n", i)
	}

	a, _, _ := sumApp(t, true, 80, 20, b.String())
	sumPage(t, a)

	v := a.checks.page.summary
	x, y, _, _, _ := logBox(a.width, a.height)
	wheel := func(btn tea.MouseButton) { a.Update(tea.MouseWheelMsg{X: x + 5, Y: y + 5, Button: btn}) }

	wheel(tea.MouseWheelDown)

	if v.top != scrollStep || v.cursor != scrollStep {
		t.Errorf("wheel down: top %d, cursor %d, want %d, %d", v.top, v.cursor, scrollStep, scrollStep)
	}

	v.cursor = v.top + 2

	wheel(tea.MouseWheelDown)

	if v.top != 2*scrollStep || v.cursor < v.top || v.cursor >= v.top+v.h {
		t.Errorf("wheel down again: top %d, cursor %d", v.top, v.cursor)
	}

	wheel(tea.MouseWheelUp)
	wheel(tea.MouseWheelUp)

	if v.top != 0 || v.cursor < v.top || v.cursor >= v.top+v.h {
		t.Errorf("wheel up: top %d, cursor %d, rows %d", v.top, v.cursor, v.h)
	}

	press(a, keyRune('G'))

	if last := len(v.layout().Lines) - 1; v.cursor != last || v.top+v.h <= last {
		t.Errorf("G: cursor %d, top %d, last line %d", v.cursor, v.top, last)
	}

	press(a, keyRune('g'))
	press(a, tea.KeyPressMsg{Code: 'd', Mod: tea.ModCtrl})

	if v.cursor != max(v.h/2, 1) {
		t.Errorf("ctrl+d: cursor %d, want %d", v.cursor, max(v.h/2, 1))
	}
}

func sumScene(t *testing.T, icons bool, w, h int, mode string) *App {
	t.Helper()

	a, _, _ := sumApp(t, icons, w, h, codeqlSummary)
	cmd := press(a, keyRune('l'))

	if mode != "loading" {
		ciDeliver(a, cmd)
	}

	return a
}

var sumScenes = []ciSceneCase{{"summary-160x48", 160, 48, "summary"}, {"loading", 120, 30, "loading"}, {"40x12", 40, 12, "summary"}}

// TestCheckSummaryGolden compares highlighted colours, so it runs serially.
func TestCheckSummaryGolden(t *testing.T) {
	for _, c := range sumScenes {
		t.Run("catppuccin-mocha/icons=true/"+c.name, func(t *testing.T) {
			golden.RequireEqual(t, []byte(sumScene(t, true, c.w, c.h, c.mode).View().Content))
		})
	}
}

// TestCheckSummaryText is the icons=false screens without styling; the colour
// golden of the icons=true ones holds the same text.
func TestCheckSummaryText(t *testing.T) {
	t.Parallel()

	for _, c := range sumScenes {
		t.Run("icons=false/"+c.name, func(t *testing.T) {
			t.Parallel()
			golden.RequireEqual(t, []byte(ansi.Strip(sumScene(t, false, c.w, c.h, c.mode).View().Content)))
		})
	}
}
