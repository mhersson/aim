package ui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/mhersson/aim/internal/core"
)

var keyCtrlE = tea.KeyPressMsg{Code: 'e', Mod: tea.ModCtrl}

// editorRig is a compose box whose editor pane starts on a fake process.
type editorRig struct {
	*composeRig
	st  *starts
	run string
	v   *composeView
}

func newEditorRig(t *testing.T) *editorRig {
	t.Helper()

	r := &editorRig{composeRig: newComposeRig(t), st: &starts{}, run: t.TempDir()}
	r.start = r.st.start
	r.runDir = r.run
	r.v = r.mustOpen(t)

	return r
}

// ctrlE presses ctrl+e and answers the file write; the pane's wait command is
// never run, since it blocks until the pane exits.
func (r *editorRig) ctrlE(t *testing.T) {
	t.Helper()

	cmd := press(r.App, keyCtrlE)
	if cmd == nil {
		return
	}

	r.step(cmd())
}

func (r *editorRig) file(t *testing.T) string {
	t.Helper()

	files, err := filepath.Glob(filepath.Join(r.run, "pr-body-*.md"))
	if err != nil || len(files) != 1 {
		t.Fatalf("body files = %q, %v, want one", files, err)
	}

	return files[0]
}

func (r *editorRig) edit(t *testing.T, text string) {
	t.Helper()

	if err := os.WriteFile(r.file(t), []byte(text), 0o600); err != nil {
		t.Fatal(err)
	}
}

func (r *editorRig) exit(t *testing.T) {
	t.Helper()

	r.churn(t, r.step(paneExitedMsg{id: "p3", p: r.panes["p3"]}))
}

func TestComposeEditorRoundTrip(t *testing.T) {
	t.Parallel()

	r := newEditorRig(t)
	r.v.body = newTextArea("old body", maxComposeBody)

	r.ctrlE(t)

	file := r.file(t)

	if fi, err := os.Stat(file); err != nil || fi.Mode().Perm() != 0o600 {
		t.Fatalf("body file = %v, %v, want mode 0600", fi, err)
	}

	if b, _ := os.ReadFile(file); string(b) != "old body" {
		t.Errorf("file = %q, want the body", b)
	}

	if r.st.count() != 1 {
		t.Fatalf("starts = %d, want the editor pane", r.st.count())
	}

	argv := r.st.cmds[0].Argv
	if strings.Contains(strings.Join(argv, " "), "exec") || argv[len(argv)-1] != file {
		t.Errorf("argv = %q, want the editor alone on the file", argv)
	}

	if r.composeShown() || strings.Contains(r.text(), "Open a pull request") {
		t.Error("the box shows while the editor runs")
	}

	r.edit(t, "new body\r\nline two")
	r.exit(t)

	if !r.composeShown() || r.compose.field != composeBody || r.compose.body.Value() != "new body\nline two" {
		t.Errorf("shown = %v, field = %v, body = %q", r.composeShown(), r.compose.field, r.compose.body.Value())
	}

	if b := r.compose.body; b.row != 0 || b.col != 0 {
		t.Errorf("cursor = (%d, %d) after the editor, want the start of the text", b.row, b.col)
	}

	if _, err := os.Stat(file); !os.IsNotExist(err) {
		t.Errorf("body file still there: %v", err)
	}
}

func TestComposeEditorClosedByLeaderX(t *testing.T) {
	t.Parallel()

	r := newEditorRig(t)
	r.ctrlE(t)
	r.edit(t, "from the editor")

	r.churn(t, press(r.App, keyLeader, keyRune('x')))

	if !r.composeShown() || r.compose.body.Value() != "from the editor" {
		t.Errorf("shown = %v, body = %q", r.composeShown(), r.compose.body.Value())
	}
}

func TestComposeEditorNoRunDir(t *testing.T) {
	t.Parallel()

	r := newEditorRig(t)
	r.runDir = ""
	r.ctrlE(t)

	if r.notice != noticeEditorNoRunDir || r.st.count() != 0 || !r.composeShown() {
		t.Errorf("notice = %q, starts = %d, shown = %v", r.notice, r.st.count(), r.composeShown())
	}
}

func TestComposeEditorTwice(t *testing.T) {
	t.Parallel()

	r := newEditorRig(t)
	r.ctrlE(t)

	r.churn(t, press(r.App, keyLeader, keyRune('g'), keyRune('o')))

	if r.notice != noticeEditorOpen || r.st.count() != 1 {
		t.Errorf("notice = %q, starts = %d", r.notice, r.st.count())
	}
}

func TestComposeEditorCloseRemovesFile(t *testing.T) {
	t.Parallel()

	r := newEditorRig(t)
	r.ctrlE(t)

	file := r.file(t)
	closeApp(t, r.App)

	if _, err := os.Stat(file); !os.IsNotExist(err) {
		t.Errorf("body file still there after Close: %v", err)
	}
}

func TestComposeEditorReadError(t *testing.T) {
	t.Parallel()

	r := newEditorRig(t)
	r.v.body = newTextArea("keep me", maxComposeBody)
	r.ctrlE(t)

	if err := os.Remove(r.file(t)); err != nil {
		t.Fatal(err)
	}

	r.exit(t)

	if !r.composeShown() || !strings.HasPrefix(r.compose.status, "could not read the edited body: ") || r.compose.body.Value() != "keep me" {
		t.Errorf("shown = %v, status = %q, body = %q", r.composeShown(), r.compose.status, r.compose.body.Value())
	}
}

func TestComposeEditorBoxClosedMeanwhile(t *testing.T) {
	t.Parallel()

	r := newEditorRig(t)
	r.ctrlE(t)

	file := r.file(t)
	r.compose = nil

	r.exit(t)

	if _, err := os.Stat(file); !os.IsNotExist(err) || r.compose != nil {
		t.Errorf("file removed = %v, compose = %v", os.IsNotExist(err), r.compose)
	}
}

func TestComposeEditorCloseRemovesUndeliveredFile(t *testing.T) {
	t.Parallel()

	r := newEditorRig(t)

	// The write finished but its answer never reached the update loop.
	cmd := press(r.App, keyCtrlE)
	if cmd == nil {
		t.Fatal("ctrl+e raised no write")
	}

	_ = cmd()
	file := r.file(t)

	closeApp(t, r.App)

	if _, err := os.Stat(file); !os.IsNotExist(err) {
		t.Errorf("body file still there after Close: %v", err)
	}

	// A write that finishes after Close removes its own file.
	_ = cmd()

	if files, _ := filepath.Glob(filepath.Join(r.run, "pr-body-*.md")); len(files) != 0 {
		t.Errorf("body files %q after Close, want none", files)
	}
}

func TestComposeEditorTabClosedHandsFileBack(t *testing.T) {
	t.Parallel()

	r := newEditorRig(t)
	r.ctrlE(t)
	r.edit(t, "from the editor")

	file := r.file(t)
	r.churn(t, r.closeTabByID(r.store.State().Tab().ID))

	if r.compose == nil || r.compose.body.Value() != "from the editor" {
		t.Errorf("compose = %v, want the edited body handed back", r.compose)
	}

	if _, err := os.Stat(file); !os.IsNotExist(err) {
		t.Errorf("body file still there after the tab closed: %v", err)
	}
}

func TestComposeOpensFromTheLeaderKeys(t *testing.T) {
	t.Parallel()

	r := newComposeRig(t)

	r.churn(t, press(r.App, keyLeader, keyRune('g'), keyRune('o')))

	if r.compose == nil {
		t.Fatalf("leader g o opened no box; notice %q", r.notice)
	}
}

func TestComposeEditorSaysWhyThereIsNoPane(t *testing.T) {
	t.Parallel()

	r := newEditorRig(t)

	for len(r.store.State().Panes) < core.MaxPanes {
		openIn(t, r.App, core.OpenPane{Kind: core.KindShell, Cwd: r.dir, Split: core.SplitVertical})
	}

	// The key's own commands include the layout's; the write is what matters.
	r.step(r.composeEditor()())

	if !r.composeShown() || r.compose.status == "" || !r.compose.statusErr {
		t.Errorf("shown = %v, status = %q (error %v), want the reason", r.composeShown(), r.compose.status, r.compose.statusErr)
	}
}
