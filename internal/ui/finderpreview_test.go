package ui

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/mhersson/aim/internal/git"
	"github.com/mhersson/aim/internal/md"
)

// sync delivers what the sync after an update asks for.
func (r *finderRig) sync() { r.flow(r.step(struct{}{}), nil) }

func holdPreview(m tea.Msg) bool {
	_, ok := m.(finderPreviewMsg)

	return ok
}

var (
	keyCtrlD  = tea.KeyPressMsg{Code: 'd', Mod: tea.ModCtrl}
	keyCtrlU  = tea.KeyPressMsg{Code: 'u', Mod: tea.ModCtrl}
	keyPgUp   = tea.KeyPressMsg{Code: tea.KeyPgUp}
	keyPgDown = tea.KeyPressMsg{Code: tea.KeyPgDown}
)

// entryNames is a preview's folder list as the column writes it.
func entryNames(p *finderPreview) []string {
	var out []string

	for _, e := range p.list {
		out = append(out, e.Name+map[bool]string{true: "/", false: ""}[e.Dir])
	}

	return out
}

func TestReadFinderPreview(t *testing.T) {
	t.Parallel()

	home, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}

	at := func(rel string) string { return filepath.Join(home, filepath.FromSlash(rel)) }

	for name, text := range map[string]string{
		"plain.md":             "# Plain\n",
		"upper/README.md":      "# Upper\n",
		"mark/README.markdown": "# Mark\n",
		"lower/readme.md":      "# Lower\n",
		"both/README.markdown": "# Second\n",
		"both/README.md":       "# First\n",
		"plain-dir/sub/x.md":   "# X\n",
		"plain-dir/a.md":       "# A\n",
		"plain-dir/.hidden.md": "# H\n",
		"plain-dir/notes.txt":  "text\n",
		"empty/x.txt":          "text\n",
		"elsewhere/real.md":    "# Real\n",
	} {
		write(t, at(name), text)
	}

	if err := os.Symlink(at("elsewhere/real.md"), at("link.md")); err != nil {
		t.Fatal(err)
	}

	write(t, at("denied.md"), "# No\n")

	if err := os.Chmod(at("denied.md"), 0); err != nil {
		t.Fatal(err)
	}

	canRead := func() bool {
		f, err := os.Open(at("denied.md"))
		if err == nil {
			_ = f.Close()
		}

		return err == nil
	}()

	if err := os.MkdirAll(at("pipe"), 0o755); err != nil {
		t.Fatal(err)
	}

	fifo := syscall.Mkfifo(at("pipe/x.md"), 0o644) == nil

	tests := []struct {
		name  string
		path  string
		dir   bool
		skip  bool
		head  string
		doc   bool
		list  []string
		text  string
		isErr bool
	}{
		{name: "a file", path: at("plain.md"), head: "~/plain.md", doc: true},
		{name: "a symlink to a file", path: at("link.md"), head: "~/link.md", doc: true},
		{name: "a folder with README.md", path: at("upper"), dir: true, head: "~/upper/README.md", doc: true},
		{name: "a folder with README.markdown", path: at("mark"), dir: true, head: "~/mark/README.markdown", doc: true},
		{name: "a folder with a lowercase readme.md", path: at("lower"), dir: true, head: "~/lower/readme.md", doc: true},
		{name: "README.md before README.markdown", path: at("both"), dir: true, head: "~/both/README.md", doc: true},
		{
			name: "a folder without a README", path: at("plain-dir"), dir: true, head: "~/plain-dir",
			list: []string{"sub/", "a.md"},
		},
		{name: "an empty folder", path: at("empty"), dir: true, head: "~/empty", text: finderEmpty},
		{name: "home itself", path: home, dir: true, head: "~", list: []string{
			"both/", "elsewhere/", "empty/", "lower/", "mark/", "pipe/", "plain-dir/", "upper/", "denied.md", "link.md", "plain.md",
		}},
		{
			name: "a file that is gone", path: at("gone.md"), head: "~/gone.md", isErr: true,
			text: finderUnreadableFile + "no such file",
		},
		{
			name: "a file that cannot be read", path: at("denied.md"), head: "~/denied.md", isErr: true, skip: canRead,
			text: finderUnreadableFile + fs.ErrPermission.Error(),
		},
		{
			name: "a pipe named x.md", path: at("pipe/x.md"), head: "~/pipe/x.md", isErr: true, skip: !fifo,
			text: finderUnreadableFile + "not a regular file",
		},
		{
			name: "a folder that is gone", path: at("gone"), dir: true, head: "~/gone", isErr: true,
			text: finderUnreadable + "no such file or directory",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if tt.skip {
				t.Skip("the setup cannot make this case here")
			}

			p := readFinderPreview(context.Background(), tt.path, tt.dir, finderPreviewOpts{home: home, width: 40, icons: true})

			if p.head != tt.head || (p.doc != nil) != tt.doc || p.text != tt.text || p.isErr != tt.isErr {
				t.Fatalf("head %q, doc %v, text %q, error %v; want %q, %v, %q, %v",
					p.head, p.doc != nil, p.text, p.isErr, tt.head, tt.doc, tt.text, tt.isErr)
			}

			if got := entryNames(p); !slices.Equal(got, tt.list) {
				t.Errorf("list %q, want %q", got, tt.list)
			}

			if p.file != tt.doc {
				t.Errorf("file = %v, want %v: size and time belong to a read file", p.file, tt.doc)
			}
		})
	}

	t.Run("a superseded ask stops before it parses", func(t *testing.T) {
		t.Parallel()

		ctx, cancel := context.WithCancel(context.Background())
		cancel()

		if p := readFinderPreview(ctx, at("plain.md"), false, finderPreviewOpts{home: home, width: 40}); p.doc != nil {
			t.Error("a cancelled ask parsed the file")
		}
	})

	t.Run("a read that timed out says so", func(t *testing.T) {
		t.Parallel()

		p := readFinderPreview(&timesOutAfterRead{}, at("plain.md"), false, finderPreviewOpts{home: home, width: 40})
		if p.doc != nil || !p.isErr || p.text == "" || p.file {
			t.Errorf("a timed out read showed %+v, want the failure line and no document", p)
		}
	})
}

// timesOutAfterRead is a context that is live for the first Err call, which
// guards the read, and expired from then on.
type timesOutAfterRead struct {
	context.Context //nolint:containedctx // a test double that overrides Err

	calls int
}

func (c *timesOutAfterRead) Err() error {
	c.calls++
	if c.calls > 1 {
		return context.DeadlineExceeded
	}

	return nil
}

func TestFinderSize(t *testing.T) {
	t.Parallel()

	tests := []struct {
		n    int64
		want string
	}{
		{0, "0 B"},
		{1023, "1023 B"},
		{1024, "1.0 KB"},
		{1536, "1.5 KB"},
		{1<<20 - 1, "1.0 MB"},
		{1 << 20, "1.0 MB"},
		{4<<20 + 1<<19, "4.5 MB"},
	}

	for _, tt := range tests {
		if got := finderSize(tt.n); got != tt.want {
			t.Errorf("finderSize(%d) = %q, want %q", tt.n, got, tt.want)
		}
	}
}

func TestFinderPreviewHead(t *testing.T) {
	t.Parallel()

	now := finderMod.Add(2 * time.Hour)
	file := &finderPreview{head: "~/Development/aim/docs/guide/reading.md", file: true, size: 2048, mod: finderMod}

	tests := []struct {
		name string
		p    *finderPreview
		w    int
		want string
	}{
		{"a file with its size and age", file, 80, "~/Development/aim/docs/guide/reading.md · 2.0 KB · 2h"},
		{"the path is cut on the left, the tail stays", file, 40, "…/aim/docs/guide/reading.md · 2.0 KB · 2h"},
		{"a folder is its path alone", &finderPreview{head: "~/Development"}, 80, "~/Development"},
		{"home", &finderPreview{head: "~"}, 80, "~"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got := finderPreviewHead(tt.p, now, tt.w)
			if w := ansi.StringWidth(got); w > tt.w || !strings.HasSuffix(got, tt.want[max(len(tt.want)-tt.w, 0):]) {
				t.Errorf("head = %q (%d cells), want it to end %q within %d cells", got, w, tt.want, tt.w)
			}

			if tt.w >= len(tt.want) && got != tt.want {
				t.Errorf("head = %q, want %q", got, tt.want)
			}
		})
	}
}

// longDoc is a document many screens tall.
func longDoc() string {
	var sb strings.Builder

	for i := range 200 {
		fmt.Fprintf(&sb, "line %d of the long document\n\n", i)
	}

	return sb.String()
}

var previewTree = map[string]string{
	"proj/README.md":   "# Readme\n\nThe readme.\n",
	"proj/long.md":     longDoc(),
	"proj/docs/a.md":   "# A\n",
	"proj/docs/b.md":   "# B\n",
	"proj/pic.md":      "![x](https://example.com/x.png)\n\n![y](pics/y.png)\n",
	"proj/pics/y.png":  "not a png",
	"proj/sub/z.md":    "# Z\n",
	"proj/sub/deep/.k": "",
}

func TestFinderPreviewFollowsTheCursor(t *testing.T) {
	t.Parallel()

	r := newFinderRig(t, "proj", previewTree)
	r.listed(t)

	f := r.finder
	if f.preview == nil || f.preview.head != finderTitle(r.at("proj/docs"), r.home) || len(f.preview.list) != 2 {
		t.Fatalf("on the first entry the preview is %+v, want the docs folder's contents", f.preview)
	}

	r.finderSelect(t, "README.md")
	r.sync()

	if f.preview.head != finderTitle(r.at("proj/README.md"), r.home) || f.preview.doc == nil || !f.preview.file {
		t.Fatalf("on README.md the preview is %+v, want that file", f.preview)
	}

	text := strings.Join(r.screen(), "\n")
	if !strings.Contains(text, "The readme.") || !strings.Contains(text, "~/proj/README.md · ") || !strings.Contains(text, " · 2h") {
		t.Errorf("the screen lacks the file's text and its row above:\n%s", text)
	}

	r.finderSelect(t, "..")
	r.sync()

	if f.preview.head != "~" {
		t.Errorf("on .. the preview is headed %q, want home", f.preview.head)
	}
}

func TestFinderPreviewAsksOnlyForANewPath(t *testing.T) {
	t.Parallel()

	r := newFinderRig(t, "proj", previewTree)
	r.listed(t)
	r.typeKeys("md")
	r.press(keyDown)

	f, before := r.finder, r.selectedRel(t)
	batch := []md.WalkEntry{{Rel: "aaa/md.md"}}

	held := r.flow(r.step(finderWalkMsg{view: f, seq: f.walk.seq, batch: batch}), holdPreview)

	if got := r.selectedRel(t); got != before || len(held) != 0 {
		t.Errorf("a batch under a moved cursor: row %q (was %q), %d previews asked, want the same row and none", got, before, len(held))
	}
}

func TestFinderPreviewStale(t *testing.T) {
	t.Parallel()

	t.Run("an older ask landing after a newer one", func(t *testing.T) {
		t.Parallel()

		r := newFinderRig(t, "proj", previewTree)
		r.listed(t)

		first := r.flow(r.step(keyDown), holdPreview)
		second := r.flow(r.step(keyDown), holdPreview)

		if len(first) != 1 || len(second) != 1 {
			t.Fatalf("held %d and %d previews, want one each", len(first), len(second))
		}

		r.step(second[0])
		r.step(first[0])

		if want := second[0].(finderPreviewMsg).pv; r.finder.preview != want {
			t.Errorf("preview is headed %q, want the newer ask's %q", r.finder.preview.head, want.head)
		}
	})

	t.Run("an answer after the finder closed", func(t *testing.T) {
		t.Parallel()

		r := newFinderRig(t, "proj", previewTree)
		r.listed(t)

		held := r.flow(r.step(keyDown), holdPreview)
		f := r.finder
		r.press(keyEsc)

		r.step(held[0])

		if f.preview == held[0].(finderPreviewMsg).pv || r.finder != nil {
			t.Error("an answer after esc reached the closed finder")
		}
	})

	t.Run("an answer after the folder changed", func(t *testing.T) {
		t.Parallel()

		r := newFinderRig(t, "proj", previewTree)
		r.listed(t)

		held := r.flow(r.step(keyDown), holdPreview)
		stale := held[0].(finderPreviewMsg).pv

		r.finderSelect(t, "docs")
		r.press(keyEnter)

		r.step(held[0])

		if r.finder.preview == stale {
			t.Errorf("a preview asked in %s shows in %s", r.at("proj"), r.finder.dir)
		}
	})

	t.Run("the old preview stays until the new one lands", func(t *testing.T) {
		t.Parallel()

		r := newFinderRig(t, "proj", previewTree)
		r.listed(t)

		old := r.finder.preview
		held := r.flow(r.step(keyDown), holdPreview)

		if len(held) != 1 || r.finder.preview != old || old == nil {
			t.Errorf("preview changed before the answer landed: %+v", r.finder.preview)
		}
	})
}

func TestFinderPreviewScroll(t *testing.T) {
	t.Parallel()

	for name, tt := range map[string]struct{ down, up tea.KeyPressMsg }{
		"ctrl+d and ctrl+u": {keyCtrlD, keyCtrlU},
		"pgdown and pgup":   {keyPgDown, keyPgUp},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			r := newFinderRig(t, "proj", previewTree)
			r.listed(t)
			r.finderSelect(t, "long.md")
			r.sync()

			p := r.finder.preview
			if p == nil || p.doc == nil {
				t.Fatalf("no document previewed: %+v", p)
			}

			r.press(tt.down)

			moved := p.view.top
			if moved <= 0 || moved != p.view.h/2 {
				t.Errorf("scrolled to %d, want half the preview's %d rows", moved, p.view.h)
			}

			r.press(tt.down, tt.up)

			if p.view.top != moved {
				t.Errorf("down, down, up left the top at %d, want %d", p.view.top, moved)
			}

			if strings.Contains(strings.Join(r.screen(), "\n"), "line 0 of") {
				t.Errorf("the first line is still on the screen after scrolling down")
			}
		})
	}

	t.Run("a folder's list scrolls and a new file starts at the top", func(t *testing.T) {
		t.Parallel()

		tree := map[string]string{"proj/long.md": longDoc()}
		for i := range 60 {
			tree[fmt.Sprintf("proj/many/n%02d.md", i)] = "x\n"
		}

		r := newFinderRig(t, "proj", tree)
		r.listed(t)
		r.finderSelect(t, "many")
		r.sync()

		r.press(keyCtrlD)

		if r.finder.preview.top <= 0 {
			t.Errorf("the list did not scroll: top %d", r.finder.preview.top)
		}

		r.finderSelect(t, "long.md")
		r.sync()
		r.press(keyCtrlD)

		if r.finder.preview.view.top <= 0 {
			t.Fatal("the document did not scroll")
		}

		r.finderSelect(t, "many")
		r.sync()
		r.finderSelect(t, "long.md")
		r.sync()

		if r.finder.preview.view.top != 0 {
			t.Errorf("back on the file its scroll is %d, want a fresh 0", r.finder.preview.view.top)
		}
	})

	t.Run("they leave the prompt alone", func(t *testing.T) {
		t.Parallel()

		r := newFinderRig(t, "proj", previewTree)
		r.listed(t)
		r.typeKeys("ab")
		r.press(keyCtrlD, keyCtrlU, keyPgDown, keyPgUp)

		if r.finder.input.Value() != "ab" {
			t.Errorf("prompt = %q, want ab", r.finder.input.Value())
		}
	})
}

func TestFinderPreviewWidth(t *testing.T) {
	t.Parallel()

	t.Run("a resize across the limit asks", func(t *testing.T) {
		t.Parallel()

		r := newFinderRig(t, "proj", previewTree)
		sized(t, r.App, finderPreviewMin-1, 40)
		r.listed(t)

		if r.finder.preview != nil {
			t.Fatalf("a %d column terminal asked for a preview", finderPreviewMin-1)
		}

		held := r.flow(r.step(tea.WindowSizeMsg{Width: finderPreviewMin, Height: 40}), holdPreview)
		if len(held) != 1 {
			t.Fatalf("widening to %d asked for %d previews, want 1", finderPreviewMin, len(held))
		}

		r.step(held[0])

		if r.finder.preview == nil || r.finder.preview.head != finderTitle(r.at("proj/docs"), r.home) {
			t.Errorf("preview = %+v, want the selected row's", r.finder.preview)
		}

		held = r.flow(r.step(tea.WindowSizeMsg{Width: finderPreviewMin - 1, Height: 40}), holdPreview)
		if len(held) != 0 {
			t.Errorf("narrowing asked for %d previews", len(held))
		}

		held = r.flow(r.step(tea.WindowSizeMsg{Width: finderPreviewMin + 20, Height: 40}), holdPreview)
		if len(held) != 1 {
			t.Errorf("widening again asked for %d previews, want 1", len(held))
		}
	})

	t.Run("under the limit no read is asked", func(t *testing.T) {
		t.Parallel()

		r := newFinderRig(t, "proj", previewTree)
		sized(t, r.App, finderPreviewMin-1, 40)
		r.mdOpen()

		for _, k := range []tea.KeyPressMsg{keyDown, keyDown, keyUp, keyCtrlD} {
			if held := r.flow(r.step(k), holdPreview); len(held) != 0 {
				t.Fatalf("%v asked for %d previews", k, len(held))
			}
		}
	})
}

func TestFinderPreviewShowsNoPictures(t *testing.T) {
	t.Parallel()

	r := newFinderRig(t, "proj", previewTree)
	r.listed(t)
	r.finderSelect(t, "pic.md")
	r.sync()

	p := r.finder.preview
	if p == nil || p.doc == nil {
		t.Fatalf("no document previewed: %+v", p)
	}

	if p.doc.pics.reg != nil {
		t.Error("the previewed document is wired to the picture registry, which would fetch")
	}
}

func TestFinderBackspaceHint(t *testing.T) {
	t.Parallel()

	r := newFinderRig(t, "proj", previewTree)
	r.listed(t)

	hint := func() bool { return strings.Contains(strings.Join(r.screen(), "\n"), "backspace up") }

	if !hint() {
		t.Error("an empty prompt with a parent hides backspace up")
	}

	r.press(keyRune('/'))

	if r.finder.dir != "/" || hint() {
		t.Errorf("at %s backspace up shows, but the key does nothing there", r.finder.dir)
	}
}

// recordHandler keeps the records it is handed, for a test of what was logged.
type recordHandler struct {
	mu   *sync.Mutex
	recs *[]slog.Record
}

func newRecordHandler() recordHandler {
	return recordHandler{mu: &sync.Mutex{}, recs: &[]slog.Record{}}
}

func (recordHandler) Enabled(context.Context, slog.Level) bool { return true }
func (h recordHandler) WithAttrs([]slog.Attr) slog.Handler     { return h }
func (h recordHandler) WithGroup(string) slog.Handler          { return h }

func (h recordHandler) Handle(_ context.Context, r slog.Record) error {
	h.mu.Lock()
	defer h.mu.Unlock()

	*h.recs = append(*h.recs, r)

	return nil
}

func (h recordHandler) warnings() []slog.Record {
	h.mu.Lock()
	defer h.mu.Unlock()

	var out []slog.Record

	for _, r := range *h.recs {
		if r.Level >= slog.LevelWarn {
			out = append(out, r)
		}
	}

	return out
}

func TestOpenFinderFileGitError(t *testing.T) {
	t.Parallel()

	boom := errors.New("git: boom")

	for _, tt := range []struct {
		name string
		err  error
		warn bool
	}{
		{"not a repository", git.ErrNotRepo, false},
		{"the finder closed mid-open", context.Canceled, false},
		{"a failed open", boom, true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			logs := newRecordHandler()
			fake := newFakeGit(t)

			r := newFinderRig(t, "proj", previewTree, gitOf(fake), func(o *Options) {
				o.Logger = slog.New(logs)
			})
			r.listed(t)
			fake.fail("Open", tt.err)
			r.finderSelect(t, "README.md")
			r.press(keyEnter)

			if r.plan == nil || r.plan.file == nil || r.plan.root != r.at("proj") {
				t.Fatalf("the file did not open in its own folder: plan %+v, notice %q", r.plan, r.notice)
			}

			warns := logs.warnings()
			if !tt.warn {
				if len(warns) != 0 {
					t.Fatalf("logged %d warnings, want none", len(warns))
				}

				return
			}

			if len(warns) != 1 {
				t.Fatalf("logged %d warnings, want one", len(warns))
			}

			got := map[string]any{}

			warns[0].Attrs(func(a slog.Attr) bool {
				got[a.Key] = a.Value.Any()

				return true
			})

			if got["path"] != r.at("proj/README.md") || got["err"] != boom {
				t.Errorf("warning attributes %v, want path %q and err %v", got, r.at("proj/README.md"), boom)
			}
		})
	}
}
