package ui

import (
	"bytes"
	"context"
	"fmt"
	"image"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/mhersson/aim/internal/core"
	"github.com/mhersson/aim/internal/harness"
	"github.com/mhersson/aim/internal/ui/kitty"
)

func writePNG(t *testing.T, path string, w, h int) {
	t.Helper()

	var buf bytes.Buffer
	if err := png.Encode(&buf, image.NewRGBA(image.Rect(0, 0, w, h))); err != nil {
		t.Fatal(err)
	}

	if err := os.WriteFile(path, buf.Bytes(), 0o600); err != nil {
		t.Fatal(err)
	}
}

func sentApp(t *testing.T) (*App, string) {
	t.Helper()

	dir := t.TempDir()
	a, _, _ := agentApp(t)
	a.sent.dir = dir

	return a, dir
}

// sentHook feeds ev to a and runs the commands it returns.
func sentHook(a *App, ev harness.Event) {
	run(a, hook(a, ev))
}

func sentEvent(name string, images ...string) harness.Event {
	return harness.Event{Pane: "p2", Name: name, Images: images}
}

func TestSentCopiesAtArrival(t *testing.T) {
	t.Parallel()

	a, dir := sentApp(t)
	src := filepath.Join(t.TempDir(), "a.png")
	writePNG(t, src, 3, 2)

	ev := sentEvent("PostToolUse", src)
	ev.Caption = "c"
	sentHook(a, ev)

	got := a.sentBatches("p2")
	if len(got) != 1 {
		t.Fatalf("batches = %d, want 1", len(got))
	}

	b := got[0]
	if b.name != a.cardName(a.store.State(), "p2") || b.tab != 0 || b.caption != "c" || len(b.images) != 1 {
		t.Fatalf("batch = %+v", b)
	}

	im := b.images[0]
	if !strings.HasPrefix(im.copy, dir) || im.w != 3 || im.h != 2 || !im.decoded() {
		t.Fatalf("image = %+v", im)
	}

	first, err := os.ReadFile(im.copy)
	if err != nil {
		t.Fatal(err)
	}

	writePNG(t, src, 5, 4)
	sentHook(a, harness.Event{Pane: "p2", Name: "UserPromptSubmit", TurnStart: true})
	sentHook(a, sentEvent("PostToolUse", src))

	got = a.sentBatches("p2")
	if len(got) != 2 {
		t.Fatalf("batches = %d, want 2", len(got))
	}

	if n := got[1].images[0]; n.w != 5 || n.h != 4 || n.copy == im.copy {
		t.Fatalf("second image = %+v", n)
	}

	after, err := os.ReadFile(im.copy)
	if err != nil || !bytes.Equal(first, after) {
		t.Fatalf("first copy changed: %v", err)
	}
}

func TestSentDropsAPathAlreadySentThisTurn(t *testing.T) {
	t.Parallel()

	a, _ := sentApp(t)
	src := filepath.Join(t.TempDir(), "a.png")
	writePNG(t, src, 1, 1)

	named := sentEvent("Stop", src)
	named.Named = true

	sentHook(a, sentEvent("PostToolUse", src))
	sentHook(a, named)

	if n := len(a.sentBatches("p2")); n != 1 {
		t.Fatalf("batches after send and name = %d, want 1", n)
	}

	sentHook(a, harness.Event{Pane: "p2", Name: "UserPromptSubmit", TurnStart: true})
	sentHook(a, named)

	if n := len(a.sentBatches("p2")); n != 2 {
		t.Fatalf("batches in a new turn = %d, want 2", n)
	}

	show := harness.Event{Pane: "p2", Name: "show", Show: true, Images: []string{src}}
	sentHook(a, sentEvent("PostToolUse", src))
	sentHook(a, show)

	if n := len(a.sentBatches("p2")); n != 4 {
		t.Fatalf("batches after two explicit sends = %d, want 4", n)
	}
}

func TestSentNamedSkipsWhatIsNotAFile(t *testing.T) {
	t.Parallel()

	a, _ := sentApp(t)
	d := t.TempDir()
	adir := filepath.Join(d, "adir.png")
	realPNG := filepath.Join(d, "real.png")

	if err := os.Mkdir(adir, 0o700); err != nil {
		t.Fatal(err)
	}

	writePNG(t, realPNG, 1, 1)

	ev := sentEvent("Stop", filepath.Join(d, "missing.png"), adir, realPNG)
	ev.Named = true
	sentHook(a, ev)

	got := a.sentBatches("p2")
	if len(got) != 1 || len(got[0].images) != 1 || got[0].images[0].path != realPNG {
		t.Fatalf("batches = %+v", got)
	}

	only := sentEvent("Stop", filepath.Join(d, "missing.png"))
	only.Named = true
	sentHook(a, only)

	if n := len(a.sentBatches("p2")); n != 1 {
		t.Fatalf("batches = %d, want 1", n)
	}
}

func TestSentNamedKeepsTheFirstEight(t *testing.T) {
	t.Parallel()

	a, _ := sentApp(t)
	d := t.TempDir()
	paths := []string{filepath.Join(d, "missing.png")}

	for i := range harness.MaxImages + 1 {
		p := filepath.Join(d, fmt.Sprintf("%d.png", i))
		writePNG(t, p, 1, 1)

		paths = append(paths, p)
	}

	ev := sentEvent("Stop", paths...)
	ev.Named = true
	sentHook(a, ev)

	got := a.sentBatches("p2")
	if len(got) != 1 || len(got[0].images) != harness.MaxImages {
		t.Fatalf("batches = %+v", got)
	}

	if last := got[0].images[harness.MaxImages-1].path; last != paths[harness.MaxImages] {
		t.Fatalf("last kept = %s, want %s", last, paths[harness.MaxImages])
	}
}

func TestCopySentReasons(t *testing.T) {
	t.Parallel()

	d := t.TempDir()
	fifo := filepath.Join(d, "f.png")

	if err := syscall.Mkfifo(fifo, 0o600); err != nil {
		t.Fatal(err)
	}

	adir := filepath.Join(d, "d.png")
	if err := os.Mkdir(adir, 0o700); err != nil {
		t.Fatal(err)
	}

	big := filepath.Join(d, "big.png")
	if err := os.WriteFile(big, nil, 0o600); err != nil {
		t.Fatal(err)
	}

	if err := os.Truncate(big, pictureMaxBytes+1); err != nil {
		t.Fatal(err)
	}

	text := filepath.Join(d, "x.png")
	if err := os.WriteFile(text, []byte("hello, not an image"), 0o600); err != nil {
		t.Fatal(err)
	}

	missing := filepath.Join(d, "missing.png")
	good := filepath.Join(d, "good.png")
	writePNG(t, good, 2, 2)

	tests := []struct {
		name   string
		dir    string
		path   string
		named  bool
		none   bool
		reason string
	}{
		{name: "fifo", dir: t.TempDir(), path: fifo, reason: errSentNotFile.Error()},
		{name: "directory", dir: t.TempDir(), path: adir, reason: errSentNotFile.Error()},
		{name: "too big", dir: t.TempDir(), path: big, reason: errPictureTooBig.Error()},
		{name: "not an image", dir: t.TempDir(), path: text, reason: kitty.ErrFormat.Error()},
		{name: "no dir", dir: "", path: good, reason: errSentNoDir.Error()},
		{name: "missing, named", dir: t.TempDir(), path: missing, named: true, none: true},
		{name: "missing", dir: t.TempDir(), path: missing, reason: "no such file"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			done := make(chan []sentImage, 1)

			go func() { done <- copySent(context.Background(), tt.dir, 1, []string{tt.path}, tt.named) }()

			var got []sentImage

			select {
			case got = <-done:
			case <-time.After(hangGuard):
				t.Fatal("copySent hung")
			}

			if tt.none {
				if len(got) != 0 {
					t.Fatalf("images = %+v, want none", got)
				}

				return
			}

			if len(got) != 1 || !strings.Contains(got[0].reason, tt.reason) || got[0].decoded() || got[0].path != tt.path {
				t.Fatalf("images = %+v, want reason %q", got, tt.reason)
			}
		})
	}
}

func TestSentShowFromAShellPane(t *testing.T) {
	t.Parallel()

	a, dir := sentApp(t)
	chord(a, 's')

	var shell core.PaneID

	for id, m := range a.store.State().Panes {
		if m.Kind == core.KindShell {
			shell = id
		}
	}

	if shell == "" {
		t.Fatal("no shell pane")
	}

	src := filepath.Join(t.TempDir(), "a.png")
	writePNG(t, src, 1, 1)

	ev := harness.Event{Pane: shell, Name: "show", Show: true, Images: []string{src}}
	sentHook(a, ev)
	sentHook(a, ev)

	got := a.sentBatches(shell)
	if len(got) != 2 || !strings.HasPrefix(got[0].images[0].copy, dir) {
		t.Fatalf("batches = %+v", got)
	}

	if got[0].seq >= got[1].seq {
		t.Fatalf("seq = %d, %d", got[0].seq, got[1].seq)
	}
}

func TestSentDropsALateCopy(t *testing.T) {
	t.Parallel()

	a, _ := sentApp(t)
	src := filepath.Join(t.TempDir(), "a.png")
	writePNG(t, src, 1, 1)

	cmd := hook(a, sentEvent("PostToolUse", src))

	var copied *sentCopiedMsg

	for _, m := range flatten(cmd) {
		if c, ok := m.(sentCopiedMsg); ok {
			copied = &c
		}
	}

	if copied == nil {
		t.Fatal("no sentCopiedMsg")
	}

	// The id stays in the state; the answer came from a pane since replaced.
	copied.p = &pane{id: "p2"}

	a.Update(*copied)

	if n := len(a.sentBatches("p2")); n != 0 {
		t.Fatalf("batches after the pane was replaced = %d, want 0", n)
	}

	b, _ := sentApp(t)
	sentHook(b, sentEvent("PostToolUse", src))

	if len(b.sentBatches("p2")) != 1 {
		t.Fatal("no batch")
	}

	if err := b.dispatch(core.ClosePane{ID: "p2"}); err != nil {
		t.Fatal(err)
	}

	b.Update(tea.WindowSizeMsg{Width: 100, Height: 30})

	if n := len(b.sentBatches("p2")); n != 0 {
		t.Fatalf("batches of a closed pane = %d, want 0", n)
	}
}
