package ui

import (
	"errors"
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/x/ansi"
	"github.com/charmbracelet/x/exp/golden"

	"github.com/mhersson/aim/internal/core"
	"github.com/mhersson/aim/internal/github"
	"github.com/mhersson/aim/internal/ui/theme"
)

func TestCheckOrder(t *testing.T) {
	t.Parallel()

	now := t0
	c := &core.PRChecks{Number: 7, Checks: []core.CheckInfo{
		{Name: "build", State: "success"},
		{Name: "docs", State: "skipped"},
		{Name: "lint", State: "failure"},
		{Name: "e2e", State: "pending", StartedAt: now.Add(-64 * time.Second)},
		{Name: "unit", State: "success"},
		{Name: "vet", State: "failure"},
		{Name: "deploy", State: "queued"},
	}}

	var got []string
	for _, r := range checkRows(c, now) {
		got = append(got, r.name)
	}

	want := []string{"lint", "vet", "e2e", "deploy", "build", "unit", "docs"}
	if !slices.Equal(got, want) {
		t.Errorf("order = %v, want %v", got, want)
	}

	if rows := checkRows(nil, now); len(rows) != 0 {
		t.Errorf("rows of no checks = %v", rows)
	}
}

func TestCheckLook(t *testing.T) {
	t.Parallel()

	ui := mustTheme(t, "catppuccin-mocha").UI

	tests := []struct {
		kind         checkKind
		icons, plain string
		fg           theme.Hex
		bold         bool
	}{
		{checkFailed, iconFailed, "\u2717", ui.Error, true},
		{checkRunning, iconRunning, "\u2026", ui.Warning, false},
		{checkPassed, iconPassed, "\u2713", ui.Success, false},
		{checkSkipped, iconPassed, "\u2713", ui.Subtle, false},
	}

	for _, tc := range tests {
		if got := checkGlyph(tc.kind, true); got != tc.icons {
			t.Errorf("glyph %d with icons = %q, want %q", tc.kind, got, tc.icons)
		}

		if got := checkGlyph(tc.kind, false); got != tc.plain {
			t.Errorf("glyph %d without icons = %q, want %q", tc.kind, got, tc.plain)
		}

		if fg, bold := checkTone(tc.kind, ui); fg != tc.fg || bold != tc.bold {
			t.Errorf("tone %d = %v bold %v, want %v bold %v", tc.kind, fg, bold, tc.fg, tc.bold)
		}
	}
}

func TestCheckDuration(t *testing.T) {
	t.Parallel()

	start := t0

	tests := []struct {
		name string
		c    core.CheckInfo
		want string
	}{
		{"seconds", core.CheckInfo{State: "success", StartedAt: start, CompletedAt: start.Add(12 * time.Second)}, "12s"},
		{"minutes", core.CheckInfo{State: "failure", StartedAt: start, CompletedAt: start.Add(3*time.Minute + 4*time.Second)}, "3m 04s"},
		{"an hour", core.CheckInfo{State: "success", StartedAt: start, CompletedAt: start.Add(time.Hour + 2*time.Minute + 30*time.Second)}, "1h 02m"},
		{"running", core.CheckInfo{State: "pending", StartedAt: start.Add(-64 * time.Second)}, "running 1m 04s"},
		{"no start", core.CheckInfo{State: "success", CompletedAt: start}, ""},
		{"running with no start", core.CheckInfo{State: "pending"}, ""},
		{"done with no end", core.CheckInfo{State: "success", StartedAt: start}, ""},
		{"a clock behind the start", core.CheckInfo{State: "pending", StartedAt: start.Add(time.Minute)}, "running 0s"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			if got := checkDuration(tc.c, start); got != tc.want {
				t.Errorf("checkDuration = %q, want %q", got, tc.want)
			}
		})
	}
}

// snapshotWithChecks is a poll listing pull request 7 at head with the checks.
func snapshotWithChecks(head, checksHead string, number int, checks ...github.Check) prSnapshotMsg {
	pr := prSeven()
	pr.HeadOID = head
	msg := snapshotOf(pr)
	msg.Focused = &github.FocusedChecks{Number: number, HeadOID: checksHead, Checks: checks}

	return msg
}

func sampleChecks() []github.Check {
	return []github.Check{
		{Name: "build", URL: "https://ci.example.com/build", State: github.CheckSuccess, StartedAt: t0.Add(-time.Minute), CompletedAt: t0.Add(-48 * time.Second)},
		{Name: "lint", URL: "https://ci.example.com/lint", State: github.CheckFailure},
		{Name: "e2e", URL: "javascript:alert(1)", State: github.CheckPending},
	}
}

// inChecks opens a drawer rig in PR mode on pull request 7 with the checks overlay open.
func inChecks(t *testing.T) (*prDrawer, *fakeOpen) {
	t.Helper()

	r := newPRDrawer(t)
	f := &fakeOpen{}
	r.openLink = f.open

	r.poll()
	r.opened(t)
	r.typed("p")
	r.until(t, "PR mode", r.inPRMode(7))
	r.typed("c")

	if r.checks == nil {
		t.Fatalf("c opened no overlay; notice %q", r.notice)
	}

	return r, f
}

func TestChecksBrowserOnACheckWithoutAPage(t *testing.T) {
	t.Parallel()

	r, f := inChecks(t)

	r.run(r.step(snapshotWithChecks("aaaaaaa", "aaaaaaa", 7, github.Check{Name: "bare", State: github.CheckSuccess})))
	r.typed("O")

	if r.notice != noticeCheckNoPage {
		t.Errorf("notice = %q, want %q", r.notice, noticeCheckNoPage)
	}

	if got := f.urls(); len(got) != 0 {
		t.Errorf("opened %v, want nothing opened", got)
	}
}

func TestChecksOverlay(t *testing.T) {
	t.Parallel()

	r, f := inChecks(t)

	if r.checks.repo != prRepoKey || r.checks.number != 7 {
		t.Errorf("checks = %+v, want pull request 7 of %s", *r.checks, prRepoKey)
	}

	if text := ansi.Strip(r.View().Content); !strings.Contains(text, noticeChecksLoading) || !strings.Contains(text, "#7 checks") {
		t.Errorf("the overlay lacks %q and its title:\n%s", noticeChecksLoading, text)
	}

	// The checks of another pull request are not shown.
	r.run(r.step(snapshotWithChecks("", "", 8, sampleChecks()...)))

	if text := ansi.Strip(r.View().Content); !strings.Contains(text, noticeChecksLoading) || strings.Contains(text, "lint") {
		t.Errorf("another pull request's checks were shown:\n%s", text)
	}

	// Neither are the checks of another head.
	r.run(r.step(snapshotWithChecks("aaaaaaa", "bbbbbbb", 7, sampleChecks()...)))

	if text := ansi.Strip(r.View().Content); !strings.Contains(text, noticeChecksLoading) || strings.Contains(text, "lint") {
		t.Errorf("another head's checks were shown:\n%s", text)
	}

	r.run(r.step(snapshotWithChecks("aaaaaaa", "aaaaaaa", 7, sampleChecks()...)))

	text := ansi.Strip(r.View().Content)
	for _, want := range []string{"lint", "e2e", "build", "12s"} {
		if !strings.Contains(text, want) {
			t.Errorf("the overlay lacks %q:\n%s", want, text)
		}
	}

	if strings.Index(text, "lint") > strings.Index(text, "e2e") || strings.Index(text, "e2e") > strings.Index(text, "build") {
		t.Errorf("the checks are not failed, running, passed:\n%s", text)
	}

	// j and k move the selection, held in range; O opens the selected check.
	r.typed("k")

	if r.checks.sel != 0 {
		t.Errorf("sel = %d after k at the top, want 0", r.checks.sel)
	}

	r.typed("jj")

	if r.checks.sel != 2 {
		t.Errorf("sel = %d after jj, want 2", r.checks.sel)
	}

	r.typed("j")

	if r.checks.sel != 2 {
		t.Errorf("sel = %d after j at the bottom, want 2", r.checks.sel)
	}

	r.typed("O")

	if got := f.opened(t, 1); !slices.Equal(got, []string{"https://ci.example.com/build"}) {
		t.Errorf("opened %v, want the build's page", got)
	}

	r.typed("kk")
	r.typed("O")

	if got := f.opened(t, 2); len(got) != 2 || got[1] != "https://ci.example.com/lint" {
		t.Errorf("opened %v, want the lint's page second", got)
	}

	// The running check's page is not a web link: it is refused, never opened.
	r.typed("j")
	r.typed("O")

	if got := f.urls(); len(got) != 2 {
		t.Errorf("opened %v, want a javascript link refused", got)
	}

	if r.notice != noticeLinkRefused {
		t.Errorf("notice = %q, want the refusal", r.notice)
	}

	// Keys that are not the overlay's are swallowed.
	before := r.focus()

	r.typed("x")
	r.keys(keyLeader, keyRune('d'))

	if r.checks == nil || r.focus() != before || r.pending {
		t.Errorf("a stray key reached past the overlay (checks %v, pending %v)", r.checks, r.pending)
	}

	n := len(r.rec.refreshed())

	r.typed("r")

	if got := r.rec.refreshed(); len(got) != n+1 || got[n] != prRepo {
		t.Errorf("refreshed %v after r, want one more for %s", got, prRepo)
	}

	r.keys(keyEsc)

	if r.checks != nil {
		t.Error("esc left the overlay open")
	}
}

func TestChecksNone(t *testing.T) {
	t.Parallel()

	r, _ := inChecks(t)
	r.run(r.step(snapshotWithChecks("a1b2c3d4e5f6", "a1b2c3d4e5f6", 7)))

	if text := ansi.Strip(r.View().Content); !strings.Contains(text, "no checks on a1b2c3d") {
		t.Errorf("the overlay lacks the empty line:\n%s", text)
	}
}

func TestChecksUnreadable(t *testing.T) {
	t.Parallel()

	r, _ := inChecks(t)

	refused := snapshotWithChecks("a1b2c3d4e5f6", "a1b2c3d4e5f6", 7)
	refused.Focused.Err = errors.New("github: GET repos/o/r/commits/a1b2c3d4e5f6/check-runs: HTTP 403: Resource not accessible by personal access token")
	r.run(r.step(refused))

	text := ansi.Strip(r.View().Content)
	for _, want := range []string{"could not read the checks", "token"} {
		if !strings.Contains(text, want) {
			t.Errorf("the overlay lacks %q:\n%s", want, text)
		}
	}

	for _, not := range []string{noticeChecksLoading, strings.TrimSpace(fmt.Sprintf(noticeChecksNone, ""))} {
		if strings.Contains(text, not) {
			t.Errorf("the overlay shows %q for unreadable checks:\n%s", not, text)
		}
	}

	if got := r.store.State().PRs[prRepoKey]; got.Err != "" || len(got.Open) != 1 {
		t.Errorf("repository = %+v, want the poll kept without an error", got)
	}

	r.run(r.step(snapshotWithChecks("a1b2c3d4e5f6", "a1b2c3d4e5f6", 7, sampleChecks()...)))

	if text := ansi.Strip(r.View().Content); !strings.Contains(text, "lint") || strings.Contains(text, "could not read the checks") {
		t.Errorf("a poll that read the checks does not show them:\n%s", text)
	}
}

func TestChecksFocusOrder(t *testing.T) {
	t.Parallel()

	r := newPRDrawer(t)
	r.poll()
	r.opened(t)
	r.typed("p")
	r.until(t, "PR mode", r.inPRMode(7))

	focused := func() int {
		last := r.rec.last()
		if len(last) != 1 {
			t.Fatalf("watches = %+v, want one repository", last)
		}

		return last[0].Focused
	}

	if got := focused(); got != 7 {
		t.Fatalf("focused = %d with the drawer in PR mode, want 7", got)
	}

	// The overlay's pull request outranks the drawer's.
	r.run(r.openChecks(prRepoKey, 9))
	r.settle()

	if got := focused(); got != 9 {
		t.Errorf("focused = %d with the overlay open, want its 9", got)
	}

	if got := r.rec.last()[0].Visible; !slices.Equal(got, []int{7, 9}) {
		t.Errorf("visible = %v, want 7 and 9", got)
	}

	r.keys(keyEsc)
	r.settle()

	if got := focused(); got != 7 {
		t.Errorf("focused = %d after the overlay closed, want the drawer's 7", got)
	}
}

func TestBrowserKey(t *testing.T) {
	t.Parallel()

	t.Run("in PR mode", func(t *testing.T) {
		t.Parallel()

		r := newPRDrawer(t)
		f := &fakeOpen{}
		r.openLink = f.open

		pr := prSeven()
		pr.URL = "https://github.com/o/r/pull/7"
		r.run(r.step(snapshotOf(pr)))
		r.opened(t)
		r.typed("p")
		r.until(t, "PR mode", r.inPRMode(7))
		r.typed("O")

		if got := f.opened(t, 1); !slices.Equal(got, []string{"https://github.com/o/r/pull/7"}) {
			t.Errorf("opened %v, want the pull request's page", got)
		}
	})

	t.Run("from branch mode", func(t *testing.T) {
		t.Parallel()

		r := newPRDrawer(t)
		f := &fakeOpen{}
		r.openLink = f.open

		r.poll()
		r.opened(t)
		r.typed("O")
		r.until(t, "PR mode", r.inPRMode(7))

		// The page is the repository's own when the poll gave no link.
		if got := f.opened(t, 1); !slices.Equal(got, []string{"https://github.com/o/r/pull/7"}) {
			t.Errorf("opened %v, want the pull request's page after showing it", got)
		}
	})

	t.Run("c from branch mode", func(t *testing.T) {
		t.Parallel()

		r := newPRDrawer(t)
		r.poll()
		r.opened(t)
		r.typed("c")

		if r.checks != nil {
			t.Fatal("c opened the overlay before the pull request loaded")
		}

		r.until(t, "the overlay", func() bool { return r.checks != nil })
		r.until(t, "PR mode", r.inPRMode(7))

		if r.checks.number != 7 {
			t.Errorf("checks = %+v, want pull request 7", *r.checks)
		}
	})
}

// opened waits for the opener's n-th call and returns what it was given.
func (f *fakeOpen) opened(t *testing.T, n int) []string {
	t.Helper()

	deadline := time.Now().Add(10 * time.Second)

	for len(f.urls()) < n {
		if time.Now().After(deadline) {
			t.Fatal("the opener was never called")
		}

		time.Sleep(time.Millisecond)
	}

	return f.urls()
}

func TestChecksKeyDropsAStaleAnswer(t *testing.T) {
	t.Parallel()

	closeDrawer := func(t *testing.T, r *prDrawer) {
		openIn(t, r.App, core.CloseReview{})
		r.settle()
	}

	for _, tc := range []struct {
		key, stage string
		move       func(t *testing.T, r *prDrawer)
	}{
		{"c", "the place answered", func(t *testing.T, r *prDrawer) { r.typed("b") }},
		{"O", "the fetch answered", closeDrawer},
	} {
		t.Run(tc.key+" then the move before "+tc.stage, func(t *testing.T) {
			t.Parallel()

			r := newPRDrawer(t)
			f := &fakeOpen{}
			r.openLink = f.open

			r.poll()
			r.opened(t)

			msgs := flatten(r.step(keyRune(rune(tc.key[0]))))
			if len(msgs) != 1 {
				t.Fatalf("%s raised %d messages, want the place's answer", tc.key, len(msgs))
			}

			late := msgs[0]

			if tc.stage == "the fetch answered" {
				fm := flatten(r.step(late))
				if len(fm) != 1 {
					t.Fatalf("the place raised %d messages, want the fetch's answer", len(fm))
				}

				late = fm[0]
			}

			tc.move(t, r)
			r.settle()

			r.run(r.step(late))
			r.settle()

			if r.checks != nil {
				t.Error("a stale answer opened the checks")
			}

			if got := f.urls(); len(got) != 0 {
				t.Errorf("a stale answer opened %v", got)
			}

			if rv := r.review(); rv != nil && rv.PR != nil {
				t.Errorf("review = %+v after the late answer, want no PR", rv)
			}
		})
	}
}

// prChecksScene is the PR drawer with the checks overlay open on a mixed list.
func prChecksScene(t *testing.T, a *App, v *reviewView) {
	t.Helper()

	prSceneWith(nil, 0)(t, a, v)

	info, _ := prInfoByNumber(a.store.State(), prRepoKey, 7)
	started := t0.Add(-3*time.Minute - 4*time.Second)

	checks := []core.CheckInfo{
		{Name: "build", URL: "https://ci.example.com/build", State: "success", StartedAt: started, CompletedAt: started.Add(12 * time.Second)},
		{Name: "docs", URL: "https://ci.example.com/docs", State: "skipped"},
		{Name: "lint \x1b[31mand\x07 vet with a very long name that does not fit in the box at all, not even at the widest", URL: "https://ci.example.com/lint", State: "failure", StartedAt: started, CompletedAt: started.Add(3*time.Minute + 4*time.Second)},
		{Name: "e2e", URL: "https://ci.example.com/e2e", State: "pending", StartedAt: t0.Add(-64 * time.Second)},
		{Name: "unit", URL: "https://ci.example.com/unit", State: "success", StartedAt: started, CompletedAt: started.Add(time.Hour + 2*time.Minute)},
		{Name: "vet", URL: "https://ci.example.com/vet", State: "failure"},
	}

	openIn(t, a, core.SetRepoPRs{
		Repo: prRepoKey, DefaultBranch: "main", Open: []core.PRInfo{info},
		Focused: &core.PRChecks{Number: 7, HeadOID: info.HeadOID, Checks: checks},
	})

	a.checks = &checksView{repo: prRepoKey, number: 7, sel: 1}
}

// checksScenes are the checks overlay over the PR drawer, at the usual size and
// at 80x24.
func checksScenes() []struct {
	name string
	w, h int
} {
	return []struct {
		name string
		w, h int
	}{{"wide", 200, 52}, {"80x24", 80, 24}}
}

func checksScene(t *testing.T, icons bool, w, h int) *App {
	t.Helper()

	s := reviewScene{
		theme: "catppuccin-mocha", icons: icons, w: w, h: h, res: turn4Result(), turn: 4, turns: 4, focused: true,
		branch: true, tune: prChecksScene,
	}

	return s.build(t)
}

// TestChecksGolden draws the overlay once in colour; it compares highlighted
// colours, so it runs serially.
func TestChecksGolden(t *testing.T) {
	for _, c := range checksScenes() {
		t.Run("catppuccin-mocha/icons=true/"+c.name, func(t *testing.T) {
			golden.RequireEqual(t, []byte(checksScene(t, true, c.w, c.h).View().Content))
		})
	}
}

// TestChecksText is the same screens without styling, so the layout reads.
func TestChecksText(t *testing.T) {
	t.Parallel()

	for _, icons := range []bool{true, false} {
		for _, c := range checksScenes() {
			t.Run(fmt.Sprintf("icons=%v/%s", icons, c.name), func(t *testing.T) {
				t.Parallel()
				golden.RequireEqual(t, []byte(ansi.Strip(checksScene(t, icons, c.w, c.h).View().Content)))
			})
		}
	}
}
