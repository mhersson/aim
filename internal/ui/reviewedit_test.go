package ui

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/mhersson/aim/internal/core"
	"github.com/mhersson/aim/internal/git"
	"github.com/mhersson/aim/internal/term"
)

// writeExecutable writes an executable script under the fork lock: a concurrent fork can
// inherit the write fd and make exec fail with ETXTBSY (go.dev/issue/22315).
func writeExecutable(t *testing.T, path, body string) {
	t.Helper()

	syscall.ForkLock.RLock()

	err := os.WriteFile(path, []byte(body), 0o755)

	syscall.ForkLock.RUnlock()

	if err != nil {
		t.Fatal(err)
	}
}

func TestReviewEditor(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name, visual, editor, want string
	}{
		{"visual wins", "code", "nano", "code"},
		{"editor", "", "nano", "nano"},
		{"neither", "", "", "vi"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := reviewEditor(func(k string) string {
				return map[string]string{"VISUAL": tt.visual, "EDITOR": tt.editor}[k]
			})
			if got != tt.want {
				t.Errorf("editor = %q, want %q", got, tt.want)
			}
		})
	}
}

// editRig is a drawer with a changed file whose path holds a quote and a space,
// files whose paths an editor would read as an option or a command, a deleted
// file and an editor that writes the arguments it got to a file.
type editRig struct {
	*drawerRig
	st       *starts
	v        *reviewView
	dir      string
	name     string
	editor   string
	argsFile string
}

// hostileEditNames are file names that start like an editor's options and
// commands: vim runs +{command}, and reads -c and a lone - as options.
func hostileEditNames() []string {
	return []string{"+call system('touch pwned')", "-c", "-"}
}

func newEditRig(t *testing.T, editorArgs string) *editRig {
	t.Helper()

	f := newFakeGit(t)
	fr := f.repo(t, "")
	dir := fr.Root()
	name := "dir/it's a file.txt"

	write(t, filepath.Join(dir, name), numberedLines("line", 20))
	write(t, filepath.Join(dir, "gone.txt"), "x\n")
	write(t, filepath.Join(dir, "-dash.txt"), "one\n")
	fr.commit("init")
	write(t, filepath.Join(dir, name), editedLines(20, 7))
	write(t, filepath.Join(dir, "-dash.txt"), "two\n")

	for _, n := range hostileEditNames() {
		write(t, filepath.Join(dir, n), "file named "+n+"\n")
	}

	if err := os.Remove(filepath.Join(dir, "gone.txt")); err != nil {
		t.Fatal(err)
	}

	argsFile, editor := filepath.Join(t.TempDir(), "args"), filepath.Join(t.TempDir(), "ed.sh")
	writeExecutable(t, editor, "#!/bin/sh\nfor a in \"$@\"; do printf '%s\\n' \"$a\"; done > \"$ARGS_FILE\"\n")

	r := newKeysRig(t, dir, gitOf(f), func(o *Options) {
		o.Getenv = func(k string) string {
			return map[string]string{"SHELL": "/bin/testsh", "EDITOR": editor + editorArgs}[k]
		}
	})
	st := &starts{}
	r.start = st.start

	return &editRig{drawerRig: r, st: st, v: r.opened(t), dir: dir, name: name, editor: editor, argsFile: argsFile}
}

// cursorOn puts the cursor on the first row want accepts.
func (e *editRig) cursorOn(t *testing.T, want func(git.FileDiff, reviewRow) bool) {
	t.Helper()

	for i, row := range e.v.rows {
		if want(e.v.res.Files[row.file], row) {
			e.v.cursor = i

			return
		}
	}

	t.Fatal("no such row")
}

func TestEditOpensAnEditorPane(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		editorArgs string // appended to $EDITOR
		wantWords  string // the editor as the script holds it, before its path
		wantArgs   []string
	}{
		{"a plain editor", "", "", nil},
		{"an editor with arguments runs as git runs it", " -w", " -w", []string{"-w"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e := newEditRig(t, tt.editorArgs)

			e.cursorOn(t, func(f git.FileDiff, row reviewRow) bool {
				return f.Path == e.name && row.kind == rowLine && f.Hunks[row.hunk].Lines[row.line].Kind == git.Add
			})

			row := e.v.rows[e.v.cursor]
			line := e.v.res.Files[row.file].Hunks[row.hunk].Lines[row.line].New

			e.keys(keyRune('e'))

			if e.st.count() != 1 {
				t.Fatalf("e started %d processes, want 1", e.st.count())
			}

			c := e.st.cmds[0]
			script := fmt.Sprintf(`%s%s "$@"; exec "$SHELL" -l`, e.editor, tt.wantWords)
			plusLine := fmt.Sprintf("+%d", line)

			// The path is its own argument, never part of the script, and absolute.
			abs := filepath.Join(e.dir, e.name)
			if want := []string{"/bin/testsh", "-l", "-i", "-c", script, "testsh", plusLine, abs}; !slices.Equal(c.Argv, want) || c.Dir != e.dir {
				t.Errorf("command = %q in %q, want %q in %s", c.Argv, c.Dir, want, e.dir)
			}

			got := e.store.State()
			if got.Tab().Review != nil || e.drawer.focused {
				t.Errorf("drawer open = %v, has the keyboard = %v, want it closed", got.Tab().Review != nil, e.drawer.focused)
			}

			if m := got.Panes[got.Tab().Focus]; m.Kind != core.KindShell || m.Cwd != e.dir || got.Tab().Focus == "p1" {
				t.Errorf("focused pane = %+v, want the new shell in %s", m, e.dir)
			}

			// The shell reads the script back as the arguments the editor needs.
			truePath, err := exec.LookPath("true")
			if err != nil {
				t.Skip("no true(1)")
			}

			cmd := exec.Command("/bin/sh", append([]string{"-c"}, c.Argv[4:]...)...)

			cmd.Dir, cmd.Env = e.dir, append(os.Environ(), "ARGS_FILE="+e.argsFile, "SHELL="+truePath)

			if out, err := cmd.CombinedOutput(); err != nil {
				t.Fatalf("running the command: %v\n%s", err, out)
			}

			args, err := os.ReadFile(e.argsFile)
			if err != nil {
				t.Fatal(err)
			}

			want := strings.Join(append(slices.Clone(tt.wantArgs), plusLine, abs), "\n") + "\n"
			if string(args) != want {
				t.Errorf("the editor got %q, want %q", args, want)
			}
		})
	}

	t.Run("a deleted file", func(t *testing.T) {
		e := newEditRig(t, "")
		e.cursorOn(t, func(f git.FileDiff, row reviewRow) bool { return f.Path == "gone.txt" && row.kind == rowFile })
		e.keys(keyRune('e'))

		if e.notice != noticeFileDeleted || e.st.count() != 0 {
			t.Errorf("notice = %q, panes started = %d, want the notice and none", e.notice, e.st.count())
		}
	})

	// An editor reads +{command} as a command and a leading dash as an option, so
	// the path it gets is the absolute one: it starts with neither, and it does not
	// depend on the editor pane's directory, which falls back when the root is gone.
	t.Run("a path an editor would read as an option or a command", func(t *testing.T) {
		for _, name := range append(hostileEditNames(), "-dash.txt") {
			t.Run(name, func(t *testing.T) {
				e := newEditRig(t, "")
				e.cursorOn(t, func(f git.FileDiff, row reviewRow) bool { return f.Path == name && row.kind == rowFile })
				e.keys(keyRune('e'))

				if e.st.count() != 1 {
					t.Fatalf("e started %d processes, want 1", e.st.count())
				}

				want := filepath.Join(e.dir, name)

				if got := e.st.cmds[0].Argv[len(e.st.cmds[0].Argv)-1]; got != want {
					t.Errorf("the editor's path = %q, want %q", got, want)
				}

				// The recording editor, run as the shell runs it, gets the line and the path as plain arguments.
				got := e.runPaneCommand(t, e.st.cmds[0])
				if wantArgs := []string{"+1", want}; !slices.Equal(got, wantArgs) {
					t.Errorf("the editor got %q, want %q", got, wantArgs)
				}
			})
		}
	})

	// vim runs the +{command} it is given, as it would for a file named like one.
	t.Run("vim runs no command from a file name", func(t *testing.T) {
		vim, err := exec.LookPath("vim")
		if err != nil {
			t.Skip("vim not installed")
		}

		// vim runs system() through $SHELL, which the pane command points at true(1) for its closing exec.
		wrapper := filepath.Join(t.TempDir(), "vim-ed.sh")
		writeExecutable(t, wrapper, "#!/bin/sh\nSHELL=/bin/sh exec '"+vim+"' -u NONE -i NONE -N -n -es \"$@\" -c 'qa!'\n")

		// The control: given such a name as a command, this vim does run it, so the check below can fail.
		control, home := t.TempDir(), t.TempDir()

		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()

		ctl := exec.CommandContext(ctx, wrapper, "+call system('touch pwned')")

		ctl.Dir, ctl.Env = control, append(os.Environ(), "HOME="+home, "VIMINIT=", "EXINIT=")
		_ = ctl.Run() // exit status is not the point

		if _, err := os.Stat(filepath.Join(control, "pwned")); err != nil {
			t.Skip("this vim does not run a +command here")
		}

		e := newEditRig(t, "")
		e.getenv = func(k string) string { return map[string]string{"SHELL": "/bin/testsh", "EDITOR": wrapper}[k] }

		name := hostileEditNames()[0]

		e.cursorOn(t, func(f git.FileDiff, row reviewRow) bool { return f.Path == name && row.kind == rowFile })
		e.keys(keyRune('e'))

		if e.st.count() != 1 {
			t.Fatalf("e started %d processes, want 1", e.st.count())
		}

		e.runPaneCommand(t, e.st.cmds[0])

		if _, err := os.Stat(filepath.Join(e.dir, "pwned")); err == nil {
			t.Error("vim ran the command in the file name: pwned exists")
		}
	})
}

// runPaneCommand runs a pane command of the editor rig as the shell would, in
// the pane's directory under a deadline, with no terminal and a SHELL that exits
// at once, and returns the recording editor's arguments (none when the editor
// is another program).
func (e *editRig) runPaneCommand(t *testing.T, c term.Command) []string {
	t.Helper()

	truePath, err := exec.LookPath("true")
	if err != nil {
		t.Skip("no true(1)")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	_ = os.Remove(e.argsFile)

	cmd := exec.CommandContext(ctx, "/bin/sh", append([]string{"-c"}, c.Argv[4:]...)...)
	cmd.Dir, cmd.Env = c.Dir, append(os.Environ(), "ARGS_FILE="+e.argsFile, "SHELL="+truePath, "HOME="+t.TempDir(), "VIMINIT=", "EXINIT=")

	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("running the command: %v\n%s", err, out)
	}

	raw, err := os.ReadFile(e.argsFile)
	if err != nil {
		return nil
	}

	return strings.Split(strings.TrimSuffix(string(raw), "\n"), "\n")
}

// editLine is the line the editor opens at, for every kind of row the cursor can be on.
func TestEditLine(t *testing.T) {
	t.Parallel()

	file := git.FileDiff{Path: "f.go", Status: git.Modified, Hunks: []git.Hunk{{
		OldStart: 10, OldLines: 5, NewStart: 20, NewLines: 5,
		Lines: []git.Line{
			{Kind: git.Context, Old: 10, New: 20, Text: "c1"},
			{Kind: git.Del, Old: 11, Text: "d1"},
			{Kind: git.Add, New: 21, Text: "a1"},
			{Kind: git.Add, New: 22, Text: "a2"},
			{Kind: git.Context, Old: 12, New: 23, Text: "c2"},
			{Kind: git.Del, Old: 13, Text: "d2"},
			{Kind: git.Context, Old: 14, New: 24, Text: "c3"},
		},
	}}}
	res := git.Result{Files: []git.FileDiff{file}}
	whole := newFullText([]string{"keep", "gone1", "gone2", "tail"}, []string{"keep", "new", "tail"}, nil)

	unified := buildRows(res, rowOpts{})
	split := buildRows(res, rowOpts{split: true})
	shown := buildRows(res, rowOpts{full: map[string]fullText{"f.go": whole}, whole: map[string]bool{"f.go": true}})
	folded := buildRows(res, rowOpts{full: map[string]fullText{"f.go": whole}, whole: map[string]bool{"f.go": true}, ghostsStartFolded: true})
	info := buildRows(git.Result{Files: []git.FileDiff{{Path: "b.bin", Binary: true}}}, rowOpts{})

	// nth is the n-th row of a kind, counting from 0, that has the given text ("" for any).
	nth := func(rows []reviewRow, kind reviewRowKind, text string, n int) reviewRow {
		t.Helper()

		for _, r := range rows {
			if r.kind != kind || (text != "" && r.text != text) {
				continue
			}

			if n == 0 {
				return r
			}

			n--
		}

		t.Fatalf("no row of kind %d with text %q", kind, text)

		return reviewRow{}
	}

	// Ghost blocks where the old and the new numbering differ, so a ghost's own
	// old number cannot pass for the line the editor opens at.
	ghosts := git.Result{Files: []git.FileDiff{
		{Path: "mid.go", Status: git.Modified},
		{Path: "end.go", Status: git.Modified},
		{Path: "start.go", Status: git.Modified},
		{Path: "none.go", Status: git.Modified},
	}}
	ghostText := map[string]fullText{
		"mid.go":   newFullText([]string{"x", "g0", "k1", "k2", "g1", "g2", "k3"}, []string{"x", "k1", "k2", "k3"}, nil),
		"end.go":   newFullText([]string{"x", "k1", "g1", "g2"}, []string{"x", "k1"}, nil),
		"start.go": newFullText([]string{"g1", "g2", "k1"}, []string{"k1"}, nil),
		"none.go":  newFullText([]string{"g1", "g2"}, nil, nil),
	}
	ghostShown := buildRows(ghosts, rowOpts{full: ghostText, whole: allWhole(ghostText)})
	ghostFolded := buildRows(ghosts, rowOpts{full: ghostText, whole: allWhole(ghostText), ghostsStartFolded: true})

	// inFile is the n-th row of a kind, counting from 0, in the file at index file.
	inFile := func(rows []reviewRow, file int, kind reviewRowKind, text string, n int) reviewRow {
		t.Helper()

		var of []reviewRow

		for _, r := range rows {
			if r.file == file {
				of = append(of, r)
			}
		}

		return nth(of, kind, text, n)
	}

	tests := []struct {
		name string
		file git.FileDiff
		row  reviewRow
		want int
	}{
		{"file header", file, nth(unified, rowFile, "", 0), 1},
		{"hunk header: the hunk's new start", file, nth(unified, rowHunk, "", 0), 20},
		{"context line: its new number", file, unified[2], 20},
		{"removed line: its old number", file, unified[3], 11},
		{"added line: its new number", file, unified[4], 21},
		{"second added line", file, unified[5], 22},
		{"side by side, a replaced pair: the new side", file, nth(split, rowPair, "", 1), 21},
		{"side by side, a removal alone: its old number", file, nth(split, rowPair, "", 4), 13},
		{"side by side, an addition alone: its new number", file, nth(split, rowPair, "", 2), 22},
		{"side by side, a context pair: its new number", file, nth(split, rowPair, "", 3), 23},
		{"full file, a kept line: its place in the new text", file, nth(shown, rowFull, "keep", 0), 1},
		{"full file, an added line", file, nth(shown, rowFull, "new", 0), 2},
		{"full file, the last kept line", file, nth(shown, rowFull, "tail", 0), 3},
		{"full file, a ghost line: the new line after its block", file, nth(shown, rowGhost, "gone1", 0), 2},
		{"full file, the next ghost line: the same", file, nth(shown, rowGhost, "gone2", 0), 2},
		{"full file, a folded block: the new line after it", file, nth(folded, rowFold, "", 0), 2},
		{"an info row: the top of the file", git.FileDiff{Path: "b.bin"}, nth(info, rowInfo, "", 0), 1},

		{"a ghost in the middle: the new line after its block", ghosts.Files[0], inFile(ghostShown, 0, rowGhost, "g1", 0), 4},
		{"the last ghost of that block: the same line", ghosts.Files[0], inFile(ghostShown, 0, rowGhost, "g2", 0), 4},
		{"an earlier block of the same file", ghosts.Files[0], inFile(ghostShown, 0, rowGhost, "g0", 0), 2},
		{"a folded block in the middle", ghosts.Files[0], inFile(ghostFolded, 0, rowFold, "", 1), 4},
		{"a ghost block at the end: the last line of the new file", ghosts.Files[1], inFile(ghostShown, 1, rowGhost, "g1", 0), 2},
		{"its last ghost: the same", ghosts.Files[1], inFile(ghostShown, 1, rowGhost, "g2", 0), 2},
		{"a folded block at the end", ghosts.Files[1], inFile(ghostFolded, 1, rowFold, "", 0), 2},
		{"a ghost block at the start: the first new line", ghosts.Files[2], inFile(ghostShown, 2, rowGhost, "g2", 0), 1},
		{"a file with no new lines left: line 1", ghosts.Files[3], inFile(ghostShown, 3, rowGhost, "g2", 0), 1},
		{"a folded block of a file with no new lines left", ghosts.Files[3], inFile(ghostFolded, 3, rowFold, "", 0), 1},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := editLine(tt.file, tt.row); got != tt.want {
				t.Errorf("editLine() = %d, want %d", got, tt.want)
			}
		})
	}
}

// The editor pane is placed beside the drawer's pane without focusing that pane
// first, so it, which the operator never moved the focus to, hears nothing.
func TestEditSendsNoFocusReportsToTheDrawersPane(t *testing.T) {
	t.Parallel()
	r, _ := fakeKeysRig(t)
	r.run(r.openPane(core.KindShell, core.SplitVertical))

	procs := map[core.PaneID]*fakeProc{}

	for _, id := range []core.PaneID{"p1", "p2"} {
		p := r.panes[id]
		fp, ok := p.proc.(*fakeProc)

		if !ok {
			t.Fatalf("%s runs %T, want the fake process", id, p.proc)
		}

		procs[id] = fp

		if _, err := fp.feed.Write([]byte("\x1b[?1004h")); err != nil {
			t.Fatal(err)
		}

		eventually(t, "focus reporting in "+string(id), func() bool {
			p.mu.Lock()
			defer p.mu.Unlock()

			return p.vt.FocusReporting()
		})
	}

	// The drawer reviews p1 and has the keyboard while the focus is on p2, which it covers.
	openIn(t, r.App, core.FocusPane{ID: "p1"})
	r.opened(t)
	openIn(t, r.App, core.FocusPane{ID: "p2"})

	reports := func(id core.PaneID) (in, out int) {
		sent := procs[id].sent()

		return strings.Count(sent, "\x1b[I"), strings.Count(sent, "\x1b[O")
	}

	// Each focus move so far told the pane it left and the pane it reached.
	eventually(t, "the focus reports of the setup", func() bool {
		in1, out1 := reports("p1")
		in2, out2 := reports("p2")

		return in1 == 1 && out1 == 1 && in2 == 1 && out2 == 1
	})

	r.keys(keyRune('e'))

	if r.store.State().Tab().Review != nil {
		t.Fatal("e left the drawer open")
	}

	// p2 had the focus and loses it to the editor pane; p1 was only the anchor.
	eventually(t, "p2 to be told it lost the focus", func() bool {
		_, out := reports("p2")

		return out == 2
	})

	// Reports reach a pane through one ordered pipe, so a marker sent after e
	// returned arrives after every report e caused for p1.
	p1 := r.panes["p1"]

	p1.mu.Lock()
	p1.vt.SendBytes([]byte("marker"))
	p1.mu.Unlock()

	eventually(t, "the marker to reach p1", func() bool { return strings.Contains(procs["p1"].sent(), "marker") })

	if in, out := reports("p1"); in != 1 || out != 1 {
		t.Errorf("p1 was sent %d focus-in and %d focus-out reports, want the 1 and 1 from before e", in, out)
	}
}
