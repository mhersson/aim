package ui

import (
	"path/filepath"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/charmbracelet/x/exp/teatest/v2"

	"github.com/mhersson/aim/internal/core"
	"github.com/mhersson/aim/internal/git"
)

// openDrawerMsg opens the drawer on a pane: the keys that do it come later.
type openDrawerMsg struct{ pane core.PaneID }

// drawerFlow is the app with that one message added.
type drawerFlow struct{ *App }

func (m drawerFlow) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	if o, ok := msg.(openDrawerMsg); ok {
		return m, m.openReview(o.pane, true)
	}

	_, cmd := m.App.Update(msg)

	return m, cmd
}

func waitForText(t *testing.T, tm *teatest.TestModel, want string) {
	t.Helper()

	teatest.WaitFor(t, tm.Output(), func(out []byte) bool { return strings.Contains(ansi.Strip(string(out)), want) },
		teatest.WithDuration(10*time.Second))
}

// A drawer opened in a running program shows the worktree's diff, and a file
// saved afterwards reaches the screen once the watcher reports it.
func TestReviewDrawerFlow(t *testing.T) {
	t.Parallel()

	f := newFakeGit(t)
	rp := editedRepo(t, f)
	dir := rp.Root()
	tmp := t.TempDir()
	a, _, _ := kindApp(t, gitOf(f), func(o *Options) { o.Cwd, o.GitTempDir = dir, tmp })

	if got := a.store.State().Panes["p1"].Cwd; got != dir {
		t.Fatalf("the first pane starts in %q, want %q", got, dir)
	}

	tm := teatest.NewTestModel(t, drawerFlow{a}, teatest.WithInitialTermSize(200, 50))
	tm.Send(openDrawerMsg{pane: "p1"})
	waitForText(t, tm, "fifty")

	write(t, filepath.Join(dir, "f.txt"), editedF(0)+"LIVEUPDATE\n")
	f.watches.send(dir, git.Batch{Paths: []string{"f.txt"}})
	waitForText(t, tm, "LIVEUPDATE")

	if err := tm.Quit(); err != nil {
		t.Fatalf("Quit() error = %v", err)
	}

	final := tm.FinalModel(t, teatest.WithFinalTimeout(5*time.Second)).(drawerFlow)
	v := final.shown()

	if v == nil || len(v.res.Files) != 1 || !v.loaded {
		t.Fatalf("final view = %+v, want the loaded diff of f.txt", v)
	}
}

func (m drawerFlow) shown() *reviewView { return m.drawer.views[m.store.State().Tab().ID] }
