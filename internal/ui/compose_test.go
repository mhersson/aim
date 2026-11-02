package ui

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"unicode/utf8"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/charmbracelet/x/exp/golden"

	"github.com/mhersson/aim/internal/core"
	"github.com/mhersson/aim/internal/git"
	"github.com/mhersson/aim/internal/github"
)

var (
	keyCtrlS = tea.KeyPressMsg{Code: 's', Mod: tea.ModCtrl}
	keyLeft  = tea.KeyPressMsg{Code: tea.KeyLeft}
	keyRight = tea.KeyPressMsg{Code: tea.KeyRight}
	keySpace = tea.KeyPressMsg{Code: tea.KeySpace, Text: " "}
)

const composeRepo = "github.com/o/r"

// composeRig is the push rig with the remote on GitHub for fetches and writes
// faked: feat/x is two commits past main, which origin has.
type composeRig struct {
	*pushRig
	acts *fakeActions
}

func newComposeRig(t *testing.T) *composeRig {
	t.Helper()

	r := &composeRig{pushRig: newPushRig(t), acts: &fakeActions{}}
	r.splitURLs(t, githubURL)
	r.actions = r.acts
	r.acts.createRes = github.PR{Number: 12}

	return r
}

// openCompose runs the pr.open command and answers what it reads.
func (r *composeRig) openCompose(t *testing.T) {
	t.Helper()

	r.openComposeMsgs(t)
}

// openComposeMsgs is openCompose that returns every message it fed back.
func (r *composeRig) openComposeMsgs(t *testing.T) []tea.Msg {
	t.Helper()

	c, ok := r.reg.get("pr.open")
	if !ok {
		t.Fatal("no pr.open command")
	}

	return r.churnMsgs(t, c.run(r.App))
}

func (r *composeRig) mustOpen(t *testing.T) *composeView {
	t.Helper()

	r.openCompose(t)

	if r.compose == nil {
		t.Fatalf("no compose box; notice = %q", r.notice)
	}

	return r.compose
}

func (r *composeRig) keys(t *testing.T, ks ...tea.KeyPressMsg) {
	t.Helper()

	for _, k := range ks {
		r.churn(t, press(r.App, k))
	}
}

func (r *composeRig) typed(t *testing.T, s string) {
	t.Helper()

	for _, c := range s {
		r.keys(t, keyRune(c))
	}
}

// pushBranch pushes feat/x so the box has nothing to push.
func (r *composeRig) pushBranch(t *testing.T) {
	t.Helper()

	r.publish(t)
}

// oneCommit leaves feat/x one commit past main.
func (r *composeRig) oneCommit(t *testing.T, subject, body string) {
	t.Helper()

	r.repo.reset(r.branchTip("main"))
	r.repo.commit(subject + "\n\n" + body)
}

// midBranch has the remote hold the first commit past main as mid, which makes
// main two commits away and mid one.
func (r *composeRig) midBranch(t *testing.T) {
	t.Helper()

	var first string

	r.fake.edit(func() { first = r.fake.commits[r.repo.headOID()].parent })

	r.origin.setBranch("mid", first)
	r.repo.track("origin", "mid", first)
}

func (r *composeRig) text() string { return ansi.Strip(r.View().Content) }

// The box starts from the worktree the drawer shows, which PR mode holds
// while the pane follows another.
func TestComposeTargetsThePRModeWorktree(t *testing.T) {
	t.Parallel()

	r := newComposeRig(t)
	b := r.worktree(t, "b", "feat/b")
	r.editIn(t, r.worktree(t, "c", "feat/c"))
	r.churn(t, r.openReview(r.agent, true))

	openIn(t, r.App, core.ShowReviewPR{Repo: "github.com/o/r", Number: 7, Root: b})

	if got := r.mustOpen(t).t.branch; got != "feat/b" {
		t.Errorf("the box opened for %q, want feat/b", got)
	}
}

func TestComposeBodyOpensAtItsStart(t *testing.T) {
	t.Parallel()

	r := newComposeRig(t)
	r.mustOpen(t)

	if r.compose.body.Value() == "" {
		t.Fatal("the body starts empty")
	}

	if b := r.compose.body; b.row != 0 || b.col != 0 || b.top != 0 {
		t.Errorf("cursor = (%d, %d), top %d: want the first line's start", b.row, b.col, b.top)
	}
}

func TestComposePrefill(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		setup      func(t *testing.T, r *composeRig)
		wantTitle  string
		wantBody   string
		wantSource string
		wantCount  string
	}{
		{
			name: "a template",
			setup: func(t *testing.T, r *composeRig) {
				t.Helper()

				if err := os.MkdirAll(filepath.Join(r.dir, ".github"), 0o755); err != nil {
					t.Fatal(err)
				}

				body := "## Summary\n\nWhat changed?\n"
				if err := os.WriteFile(filepath.Join(r.dir, ".github", "pull_request_template.md"), []byte(body), 0o644); err != nil {
					t.Fatal(err)
				}
			},
			wantTitle:  "Feat/x",
			wantBody:   "## Summary\n\nWhat changed?\n",
			wantSource: ".github/pull_request_template.md",
			wantCount:  "2 commits",
		},
		{
			name:       "one commit",
			setup:      func(t *testing.T, r *composeRig) { t.Helper(); r.oneCommit(t, "Add the lexer", "It reads tokens.") },
			wantTitle:  "Add the lexer",
			wantBody:   "It reads tokens.",
			wantSource: "the commits",
			wantCount:  "1 commit",
		},
		{
			name:       "several commits",
			setup:      func(t *testing.T, r *composeRig) { t.Helper() },
			wantTitle:  "Feat/x",
			wantBody:   "- add the lexer\n- fix the parser\n",
			wantSource: "the commits",
			wantCount:  "2 commits",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			r := newComposeRig(t)
			tt.setup(t, r)

			v := r.mustOpen(t)

			if got := v.title.Value(); got != tt.wantTitle {
				t.Errorf("title = %q, want %q", got, tt.wantTitle)
			}

			if got := strings.TrimSpace(v.body.Value()); got != strings.TrimSpace(tt.wantBody) {
				t.Errorf("body = %q, want %q", got, tt.wantBody)
			}

			if v.source != tt.wantSource {
				t.Errorf("source = %q, want %q", v.source, tt.wantSource)
			}

			text := r.text()
			for _, want := range []string{"o/r", "feat/x", "main", tt.wantCount, " from " + tt.wantSource + " "} {
				if !strings.Contains(text, want) {
					t.Errorf("the box lacks %q:\n%s", want, text)
				}
			}
		})
	}
}

func TestComposeFields(t *testing.T) {
	t.Parallel()

	r := newComposeRig(t)
	r.midBranch(t)

	v := r.mustOpen(t)

	if want := []string{"main", "mid"}; fmt.Sprint(v.t.bases) != fmt.Sprint(want) || v.base != 0 {
		t.Fatalf("bases = %v at %d, want %v at 0", v.t.bases, v.base, want)
	}

	if v.field != composeTitle {
		t.Fatalf("first field = %v, want the title", v.field)
	}

	order := []composeField{composeBase, composeDraft, composeBody, composeTitle}
	for _, want := range order {
		r.keys(t, keyTab)

		if v.field != want {
			t.Fatalf("after tab the field is %v, want %v", v.field, want)
		}
	}

	r.keys(t, keyShiftTab)

	if v.field != composeBody {
		t.Fatalf("after shift+tab the field is %v, want the body", v.field)
	}

	r.keys(t, keyTab, keyTab)

	if v.field != composeBase {
		t.Fatalf("field = %v, want the base", v.field)
	}

	r.keys(t, keyRight)

	if v.base != 1 || !strings.Contains(r.text(), "1 commit") {
		t.Errorf("→: base %d, view:\n%s\nwant mid and 1 commit", v.base, r.text())
	}

	r.keys(t, keyRight)

	if v.base != 0 || !strings.Contains(r.text(), "2 commits") {
		t.Errorf("→ wraps: base %d, want main and 2 commits", v.base)
	}

	r.keys(t, keyLeft)

	if v.base != 1 {
		t.Errorf("←: base %d, want 1", v.base)
	}

	r.keys(t, keyTab, keySpace)

	if !v.draft {
		t.Error("space did not set draft")
	}

	r.keys(t, keySpace)

	if v.draft {
		t.Error("space did not clear draft")
	}

	r.keys(t, keyTab)
	r.typed(t, "xyz")
	r.keys(t, keyEnter)
	r.typed(t, "q")

	if got := v.body.Value(); !strings.HasPrefix(got, "xyz\nq") {
		t.Errorf("body = %q, want the typed text at the start", got)
	}

	// Typed text survives a change of base.
	r.keys(t, keyShiftTab, keyShiftTab, keyRight)

	if !strings.HasPrefix(v.body.Value(), "xyz\nq") {
		t.Errorf("a change of base lost the typed body: %q", v.body.Value())
	}

	r.keys(t, keyEsc)

	if r.compose != nil {
		t.Error("esc left the box open")
	}
}

func TestComposeCreates(t *testing.T) {
	t.Parallel()

	r := newComposeRig(t)
	r.pushBranch(t)

	v := r.mustOpen(t)
	if v.t.unpushed != 0 {
		t.Fatalf("unpushed = %d, want 0", v.t.unpushed)
	}

	r.keys(t, keyTab, keyTab, keySpace)
	r.keys(t, keyCtrlS)

	got := r.acts.created()
	if len(got) != 1 {
		t.Fatalf("creates = %v, want one", got)
	}

	want := createCall{
		repo: github.Repo{Host: "github.com", Owner: "o", Name: "r"},
		pr: github.NewPR{
			Title: "Feat/x", Body: "- add the lexer\n- fix the parser\n", Base: "main", Head: "feat/x", Draft: true,
		},
	}
	if got[0] != want {
		t.Errorf("create = %+v, want %+v", got[0], want)
	}

	if r.compose != nil {
		t.Error("the box is still open after the PR was created")
	}

	if r.notice != "opened #12" {
		t.Errorf("notice = %q, want opened #12", r.notice)
	}

	if refreshed := r.rec.refreshed(); len(refreshed) == 0 || refreshed[len(refreshed)-1].String() != composeRepo {
		t.Errorf("refreshed = %v, want %s", refreshed, composeRepo)
	}
}

func TestComposePushesFirst(t *testing.T) {
	t.Parallel()

	r := newComposeRig(t)
	v := r.mustOpen(t)

	if v.t.unpushed != 2 {
		t.Fatalf("unpushed = %d, want 2", v.t.unpushed)
	}

	if want := "\u21912 not pushed yet: creating the PR pushes feat/x to origin first"; !strings.Contains(r.text(), want) {
		t.Errorf("the box lacks %q:\n%s", want, r.text())
	}

	r.keys(t, keyCtrlS)

	if got := r.pushes(); !slices.Equal(got, []string{"feat/x"}) {
		t.Errorf("pushed %q, want feat/x once", got)
	}

	if refs := r.remoteRefs(t); !strings.Contains(refs, "refs/heads/feat/x") {
		t.Errorf("the remote holds %q, want feat/x pushed", refs)
	}

	if got := r.acts.created(); len(got) != 1 || got[0].pr.Head != "feat/x" {
		t.Errorf("creates = %v, want one for feat/x after the push", got)
	}

	if r.compose != nil || r.notice != "opened #12" {
		t.Errorf("box %v, notice %q; want it closed with opened #12", r.compose, r.notice)
	}
}

func TestComposeTargetIsFrozen(t *testing.T) {
	t.Parallel()

	for _, pushed := range []bool{false, true} {
		t.Run(fmt.Sprintf("branch pushed=%v", pushed), func(t *testing.T) {
			t.Parallel()

			r := newComposeRig(t)
			if pushed {
				r.pushBranch(t)
			}

			r.mustOpen(t)

			r.repo.commit("late commit")
			r.keys(t, keyCtrlS)

			if got := r.acts.created(); len(got) != 0 {
				t.Errorf("creates = %v after the branch moved, want none", got)
			}

			if n := r.pushCalls(); n != 0 {
				t.Errorf("a moved branch was pushed %d times", n)
			}

			if r.compose == nil {
				t.Fatal("the box closed")
			}

			if want := "feat/x changed since you opened this; nothing created"; r.compose.status != want || !r.compose.statusErr || r.compose.busy {
				t.Errorf("status %q (err %v, busy %v), want %q as an error", r.compose.status, r.compose.statusErr, r.compose.busy, want)
			}

			if !strings.Contains(r.text(), r.compose.status) {
				t.Errorf("the box does not show its status:\n%s", r.text())
			}
		})
	}
}

func TestComposeErrors(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		setup      func(t *testing.T, r *composeRig)
		afterOpen  func(t *testing.T, r *composeRig)
		want       string
		wantPushes int
		wantCreate int
	}{
		{
			name:  "a pull request exists",
			setup: func(t *testing.T, r *composeRig) { t.Helper(); r.pushBranch(t) },
			afterOpen: func(t *testing.T, r *composeRig) {
				t.Helper()

				r.acts.createFail = fmt.Errorf("x: %w", github.ErrPRExists)
			},
			want:       "feat/x already has an open pull request",
			wantCreate: 1,
		},
		{
			name: "a rejected push",
			setup: func(t *testing.T, r *composeRig) {
				t.Helper()
				r.origin.setBranch("feat/x", fakeOID("remote work"))
			},
			afterOpen:  func(t *testing.T, r *composeRig) { t.Helper() },
			want:       git.ErrRejected.Error(),
			wantPushes: 1,
		},
		{
			name:      "an empty title",
			setup:     func(t *testing.T, r *composeRig) { t.Helper() },
			afterOpen: func(t *testing.T, r *composeRig) { t.Helper(); r.compose.title.SetValue("") },
			want:      "enter a title",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			r := newComposeRig(t)
			tt.setup(t, r)
			r.mustOpen(t)
			tt.afterOpen(t, r)
			r.keys(t, keyCtrlS)

			v := r.compose
			if v == nil {
				t.Fatalf("the box closed; notice %q", r.notice)
			}

			if !strings.Contains(v.status, tt.want) || !v.statusErr || v.busy {
				t.Errorf("status %q (err %v, busy %v), want it to contain %q as an error", v.status, v.statusErr, v.busy, tt.want)
			}

			if n := len(r.acts.created()); n != tt.wantCreate {
				t.Errorf("creates = %d, want %d", n, tt.wantCreate)
			}

			if n := r.pushCalls(); n != tt.wantPushes {
				t.Errorf("pushes = %d, want %d", n, tt.wantPushes)
			}

			// A failed run leaves the keys working.
			r.keys(t, keyEsc)

			if r.compose != nil {
				t.Error("esc did not close the box after the failure")
			}
		})
	}
}

func TestComposeNotices(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		setup func(t *testing.T, r *composeRig)
		want  string
		// resolved checks the answer the notice came from; nil where no resolve state tells the row apart.
		resolved func(t *testing.T, m composeResolvedMsg)
	}{
		{
			name: "a branch with an open pull request",
			setup: func(t *testing.T, r *composeRig) {
				t.Helper()
				openIn(t, r.App, core.SetRepoPRs{
					Repo: composeRepo, DefaultBranch: "main",
					Open: []core.PRInfo{{Number: 9, HeadBranch: "feat/x", State: prOpenState}},
				})
			},
			want: "feat/x already has #9",
		},
		{
			name: "a remote that is not on GitHub",
			setup: func(t *testing.T, r *composeRig) {
				t.Helper()
				r.splitURLs(t, pushRemoteURL)
			},
			want: fmt.Sprintf(noticePRNotGitHub, "feat/x"),
			resolved: func(t *testing.T, m composeResolvedMsg) {
				t.Helper()

				if !m.notGitHub {
					t.Error("the answer does not say the remote is not on GitHub")
				}
			},
		},
		{
			name:  "a detached HEAD",
			setup: func(t *testing.T, r *composeRig) { t.Helper(); r.repo.detach() },
			want:  noticeComposeDetached,
			resolved: func(t *testing.T, m composeResolvedMsg) {
				t.Helper()

				if m.state != pushDetached {
					t.Errorf("state = %v, want pushDetached", m.state)
				}
			},
		},
		{
			name: "no pull request writes",
			setup: func(t *testing.T, r *composeRig) {
				t.Helper()

				r.actions = nil
			},
			want: noticePRsOff,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			r := newComposeRig(t)
			tt.setup(t, r)
			msgs := r.openComposeMsgs(t)

			if tt.resolved != nil {
				var got []composeResolvedMsg

				for _, m := range msgs {
					if res, ok := m.(composeResolvedMsg); ok {
						got = append(got, res)
					}
				}

				if len(got) != 1 {
					t.Fatalf("%d composeResolvedMsg, want 1", len(got))
				}

				tt.resolved(t, got[0])
			}

			if r.compose != nil {
				t.Errorf("the box opened:\n%s", r.text())
			}

			if r.notice != tt.want {
				t.Errorf("notice = %q, want %q", r.notice, tt.want)
			}
		})
	}
}

func TestComposeBusyDropsKeys(t *testing.T) {
	t.Parallel()

	r := newComposeRig(t)
	r.pushBranch(t)
	r.acts.hold = true

	v := r.mustOpen(t)
	cmd := press(r.App, keyCtrlS)

	if !v.busy || v.status != "opening the pull request…" {
		t.Fatalf("busy %v, status %q; want it running", v.busy, v.status)
	}

	before := v.title.Value()

	r.keys(t, keyRune('z'), keyTab, keyEsc, keyEnter)

	if v.title.Value() != before || v.field != composeTitle || r.compose != v {
		t.Errorf("a key reached the busy box: title %q, field %v, open %v", v.title.Value(), v.field, r.compose == v)
	}

	// Closing the app ends the run, and a run cut short says nothing.
	r.cancel()
	r.churn(t, cmd)

	if r.notice != "" && strings.Contains(r.notice, "opened") {
		t.Errorf("a cancelled run reported %q", r.notice)
	}
}

func TestComposeIgnoresStaleAnswer(t *testing.T) {
	t.Parallel()

	r := newComposeRig(t)

	c, _ := r.reg.get("pr.open")
	stale := c.run(r.App)
	// A second request supersedes the first, and a modal opened meanwhile blocks both.
	fresh := c.run(r.App)

	r.churn(t, stale)

	if r.compose != nil {
		t.Error("an answer to a superseded request opened the box")
	}

	r.churn(t, fresh)

	if r.compose == nil {
		t.Errorf("the current answer did not open the box; notice %q", r.notice)
	}
}

func composeScene(t *testing.T, r *composeRig, w, h int) string {
	t.Helper()

	blank := strings.TrimSuffix(strings.Repeat(strings.Repeat(" ", w)+"\n", h), "\n")
	r.width, r.height = w, h

	return r.overlayCompose(nil, blank, r.current())
}

func TestComposeGolden(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name  string
		setup func(t *testing.T, r *composeRig)
		w, h  int
	}{
		{"template", func(t *testing.T, r *composeRig) {
			t.Helper()
			r.pushBranch(t)

			if err := os.MkdirAll(filepath.Join(r.dir, ".github"), 0o755); err != nil {
				t.Fatal(err)
			}

			body := "## Summary\n\nWhat changed?\n\n## Testing\n"
			if err := os.WriteFile(filepath.Join(r.dir, ".github", "pull_request_template.md"), []byte(body), 0o644); err != nil {
				t.Fatal(err)
			}
		}, 110, 30},
		{"unpushed", func(t *testing.T, r *composeRig) { t.Helper() }, 110, 30},
		{"small", func(t *testing.T, r *composeRig) { t.Helper() }, 80, 24},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			r := newComposeRig(t)
			tc.setup(t, r)
			r.mustOpen(t)

			t.Run("catppuccin-mocha/icons=true", func(t *testing.T) {
				r.icons = true
				golden.RequireEqual(t, []byte(composeScene(t, r, tc.w, tc.h)))
			})

			for _, icons := range []bool{true, false} {
				t.Run(fmt.Sprintf("text/icons=%v", icons), func(t *testing.T) {
					r.icons = icons
					golden.RequireEqual(t, []byte(ansi.Strip(composeScene(t, r, tc.w, tc.h))))
				})
			}
		})
	}
}

func TestComposeSizes(t *testing.T) {
	t.Parallel()

	r := newComposeRig(t)
	r.mustOpen(t)

	for _, sz := range [][2]int{{110, 30}, {80, 24}, {40, 10}, {30, 8}, {12, 5}, {5, 3}, {1, 1}} {
		t.Run(fmt.Sprintf("%dx%d", sz[0], sz[1]), func(t *testing.T) {
			r.width, r.height = sz[0], sz[1]
			sized(t, r.App, sz[0], sz[1])

			lines := strings.Split(r.View().Content, "\n")
			if len(lines) != sz[1] {
				t.Fatalf("view has %d lines, want %d", len(lines), sz[1])
			}

			for i, l := range lines {
				if got := ansi.StringWidth(l); got != sz[0] {
					t.Errorf("line %d is %d cells wide, want %d", i, got, sz[0])
				}
			}
		})
	}
}

func TestComposeBaseChangeRecount(t *testing.T) {
	t.Parallel()

	r := newComposeRig(t)
	r.midBranch(t)

	v := r.mustOpen(t)
	if v.commits != 2 {
		t.Fatalf("commits = %d on main, want 2", v.commits)
	}

	// A base whose remote-tracking ref is gone has no commits, and that is no error.
	r.repo.track("origin", "mid", "")
	r.churn(t, r.composeStep(v, 1))

	// Log ran for mid and failed with errNoBaseRef, not an empty list.
	logs := r.fake.calls("Log")
	if last := logs[len(logs)-1]; len(last.Args) == 0 || last.Args[0] != "origin/mid" {
		t.Errorf("last Log call = %+v, want one for origin/mid", last)
	}

	if v.commits != 0 || v.status != "" || v.statusErr {
		t.Errorf("a missing base: commits %d, status %q (err %v), want 0 and no status", v.commits, v.status, v.statusErr)
	}

	// Any other failure is shown, not counted as no commits.
	v.commits = 5
	r.onComposeLog(composeLogMsg{logSeq: v.logSeq, box: v, err: errors.New("git log: boom")})

	if !v.statusErr || !strings.Contains(v.status, "boom") || v.commits != 5 {
		t.Errorf("a git error: status %q (err %v), commits %d, want the error shown and the count kept", v.status, v.statusErr, v.commits)
	}

	// An answer for another box is dropped.
	v.status, v.statusErr = "", false
	r.onComposeLog(composeLogMsg{logSeq: v.logSeq, box: &composeView{}, commits: []github.Commit{{}}})

	if v.commits != 5 {
		t.Errorf("commits = %d after another box's answer, want 5", v.commits)
	}
}

func TestReadBodyFileKeepsWholeRunes(t *testing.T) {
	t.Parallel()

	// "é" is two bytes, so the limit splits the last one.
	text := strings.Repeat("a", maxEditedBody-1) + "é"
	file := filepath.Join(t.TempDir(), "body")

	if err := os.WriteFile(file, []byte(text), 0o600); err != nil {
		t.Fatal(err)
	}

	got, err := readBodyFile(file)
	if err != nil {
		t.Fatal(err)
	}

	if want := strings.Repeat("a", maxEditedBody-1); got != want {
		t.Errorf("read %d bytes (valid %v), want the %d bytes before the split rune", len(got), utf8.ValidString(got), len(want))
	}
}

func TestComposeRemappedKeys(t *testing.T) {
	t.Parallel()

	r := newComposeRig(t)
	r.pushBranch(t)
	r.mustOpen(t)
	remapTables(t, r.App, contextKeys("compose", "ctrl+o", "compose.create", "ctrl+s", "none", "ctrl+q", "compose.cancel", "ctrl+k", "compose.preview"))

	r.typed(t, "sq")

	if got := r.compose.title.Value(); !strings.HasSuffix(got, "sq") {
		t.Fatalf("typing left the title %q", got)
	}

	title := r.compose.title.Value()

	r.keys(t, tea.KeyPressMsg{Code: 'k', Mod: tea.ModCtrl})

	if !r.compose.preview.on || r.compose.title.Value() != title {
		t.Errorf("ctrl+k: preview = %v, title %q, want preview on and title %q", r.compose.preview.on, r.compose.title.Value(), title)
	}

	hints := r.text()
	for _, want := range []string{"ctrl+o create", "ctrl+q cancel", "ctrl+k write"} {
		if !strings.Contains(hints, want) {
			t.Errorf("the hints lack %q:\n%s", want, hints)
		}
	}

	r.keys(t, keyCtrlS)

	if got := r.acts.created(); len(got) != 0 {
		t.Fatalf("ctrl+s created %v", got)
	}

	r.keys(t, tea.KeyPressMsg{Code: 'o', Mod: tea.ModCtrl})

	if got := r.acts.created(); len(got) != 1 {
		t.Fatalf("ctrl+o creates = %v, want one", got)
	}

	r.openCompose(t)
	r.mustOpen(t)
	r.keys(t, tea.KeyPressMsg{Code: 'q', Mod: tea.ModCtrl})

	if r.compose != nil {
		t.Error("ctrl+q did not close the box")
	}
}
