package main

import (
	"bytes"
	"context"
	"errors"
	"image"
	"image/png"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/mhersson/aim/internal/harness"
	"github.com/mhersson/aim/internal/ui/kitty"
)

// showServer serves a socket into a channel and returns its path.
func showServer(t *testing.T) (string, <-chan harness.Event) {
	t.Helper()

	path := shortSock(t)

	srv, err := harness.Listen(path)
	if err != nil {
		t.Fatal(err)
	}

	out := make(chan harness.Event, 4)
	done := make(chan error, 1)

	go func() { done <- srv.Serve(context.Background(), out) }()

	t.Cleanup(func() {
		_ = srv.Close()

		if err := <-done; err != nil {
			t.Errorf("Serve() error = %v", err)
		}
	})

	return path, out
}

func writePNG(t *testing.T, path string) {
	t.Helper()

	var buf bytes.Buffer
	if err := png.Encode(&buf, image.NewRGBA(image.Rect(0, 0, 2, 2))); err != nil {
		t.Fatal(err)
	}

	if err := os.WriteFile(path, buf.Bytes(), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestRunShowSends(t *testing.T) {
	t.Parallel()

	sock, out := showServer(t)
	dir := t.TempDir()
	abs := filepath.Join(dir, "abs.png")
	writePNG(t, abs)
	writePNG(t, filepath.Join(dir, "rel.png"))

	getenv := env(map[string]string{"AIM_SOCK": sock, "AIM_PANE": "p1"})

	line, err := runShow(context.Background(), []string{"rel.png", abs}, "a\x1b[31mb", getenv,
		func() (string, error) { return dir, nil })
	if err != nil {
		t.Fatal(err)
	}

	if want := "sent 2 images to AIM"; line != want {
		t.Errorf("line = %q, want %q", line, want)
	}

	ev := <-out
	if ev.Pane != "p1" || ev.Name != "show" || !ev.Show || ev.Caption != "a[31mb" ||
		!slices.Equal(ev.Images, []string{filepath.Join(dir, "rel.png"), abs}) {
		t.Errorf("event = %+v", ev)
	}
}

func TestRunShowDropsRepeats(t *testing.T) {
	t.Parallel()

	sock, out := showServer(t)
	dir := t.TempDir()
	a, b := filepath.Join(dir, "a.png"), filepath.Join(dir, "b.png")
	writePNG(t, a)
	writePNG(t, b)

	getenv := env(map[string]string{"AIM_SOCK": sock, "AIM_PANE": "p1"})

	line, err := runShow(context.Background(), []string{"a.png", b, a}, "", getenv,
		func() (string, error) { return dir, nil })
	if err != nil {
		t.Fatal(err)
	}

	if want := "sent 2 images to AIM"; line != want {
		t.Errorf("line = %q, want %q", line, want)
	}

	if ev := <-out; !slices.Equal(ev.Images, []string{a, b}) {
		t.Errorf("images = %v", ev.Images)
	}
}

func TestRunShowRefuses(t *testing.T) {
	t.Parallel()

	sock, out := showServer(t)
	dir := t.TempDir()

	if err := os.Mkdir(filepath.Join(dir, "d.png"), 0o700); err != nil {
		t.Fatal(err)
	}

	for name, data := range map[string]string{"a.txt": "x", "x.png": "plain text"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(data), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	writePNG(t, filepath.Join(dir, "ok.png"))

	big, err := os.Create(filepath.Join(dir, "big.png"))
	if err != nil {
		t.Fatal(err)
	}

	if err := big.Truncate(kitty.MaxFileBytes + 1); err != nil {
		t.Fatal(err)
	}

	_ = big.Close()

	nine := make([]string, 9)
	for i := range nine {
		nine[i] = "ok.png"
	}

	envOK := map[string]string{"AIM_SOCK": sock, "AIM_PANE": "p1"}

	tests := []struct {
		name  string
		env   map[string]string
		files []string
		want  error // nil accepts any error
	}{
		{"no socket", map[string]string{"AIM_PANE": "p1"}, []string{"ok.png"}, errNoAIM},
		{"no pane", map[string]string{"AIM_SOCK": sock}, []string{"ok.png"}, errNoAIM},
		{"nine files", envOK, nine, errTooMany},
		{"missing", envOK, []string{"missing.png"}, fs.ErrNotExist},
		{"directory", envOK, []string{"d.png"}, errNotFile},
		{"text name", envOK, []string{"a.txt"}, errNotImageName},
		{"text bytes", envOK, []string{"x.png"}, kitty.ErrFormat},
		{"too big", envOK, []string{"big.png"}, errTooBig},
		{"long path", envOK, []string{strings.Repeat("l", harness.MaxImagePath) + ".png"}, nil},
		{"one bad file among good", envOK, []string{"ok.png", "a.txt"}, errNotImageName},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			_, err := runShow(context.Background(), tt.files, "", env(tt.env),
				func() (string, error) { return dir, nil })
			if err == nil || (tt.want != nil && !errors.Is(err, tt.want)) {
				t.Errorf("error = %v, want %v", err, tt.want)
			}
		})
	}

	t.Cleanup(func() {
		select {
		case ev := <-out:
			t.Errorf("a refused call sent %+v", ev)
		default:
		}
	})
}

func TestRunShowSaysWhenAIMDoesNotAnswer(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	writePNG(t, filepath.Join(dir, "a.png"))

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	_, err := runShow(ctx, []string{"a.png"}, "", env(map[string]string{"AIM_SOCK": silentSock(t), "AIM_PANE": "p1"}),
		func() (string, error) { return dir, nil })
	if err == nil {
		t.Fatal("error = nil, want one")
	}

	if errors.Is(err, errSocketBlocked) {
		t.Errorf("error = %v, want the not-answering one", err)
	}
}

func TestShowCommandIsListed(t *testing.T) {
	t.Parallel()

	cmd := newRootCmd()

	var help bytes.Buffer

	cmd.SetOut(&help)
	cmd.SetErr(&help)
	cmd.SetArgs([]string{"--help"})

	if err := cmd.ExecuteContext(context.Background()); err != nil {
		t.Fatal(err)
	}

	if !strings.Contains(help.String(), "show") {
		t.Errorf("help does not list show:\n%s", help.String())
	}
}
