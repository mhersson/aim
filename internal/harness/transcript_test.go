package harness

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"testing"
	"time"
)

// entry is a custom-title line as Claude writes it.
func entry(name string) string {
	return `{"type":"custom-title","customTitle":"` + name + `","sessionId":"fixture"}`
}

// window is the 256 KiB of a transcript's end that CustomName reads.
const window = 256 * 1024

// padFrame is the shortest padLine.
const padFrame = `{"type":"user","pad":""}` + "\n"

// padLine is one JSON line of exactly n bytes, newline included.
func padLine(n int) string {
	return `{"type":"user","pad":"` + strings.Repeat("x", n-len(padFrame)) + `"}` + "\n"
}

func writeTranscript(t *testing.T, content string) string {
	t.Helper()

	path := filepath.Join(t.TempDir(), "session.jsonl")
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}

	return path
}

func TestCustomName(t *testing.T) {
	fixture := func(name string) string { return filepath.Join("testdata", name) }

	tests := []struct {
		name string
		path string
		want string
	}{
		// The fixture holds two entries, a user line quoting an entry, and a last
		// entry cut off mid-write: the last whole top-level entry wins.
		{"the last entry wins and a quoted one is not an entry", fixture("transcript_named.jsonl"), "muxr-orchestrator"},
		{"no entry", fixture("transcript_unnamed.jsonl"), ""},
		{"no path", "", ""},
		{"a file that does not exist", filepath.Join(t.TempDir(), "gone.jsonl"), ""},
		{"an empty file", writeTranscript(t, ""), ""},
		{"an entry with an empty name clears the name", writeTranscript(t, entry("old")+"\n"+entry("")+"\n"), ""},
		{"a name after an empty one", writeTranscript(t, entry("")+"\n"+entry("new")+"\n"), "new"},
		{"the last line needs no newline", writeTranscript(t, entry("a")+"\n"+entry("b")), "b"},
		{"a malformed line is skipped", writeTranscript(t, entry("kept")+"\n"+`{"type":"custom-title","customTitle":}`+"\n"+"not json at all\n"), "kept"},
		{"a nested type is not an entry", writeTranscript(t, `{"type":"user","message":{"type":"custom-title","customTitle":"nested"}}`+"\n"), ""},
		{"only whole lines count", writeTranscript(t, `{"type":"custom-title","customTitle":"cut`), ""},
		{"a line ending in a carriage return", writeTranscript(t, entry("crlf")+"\r\n"), "crlf"},
		{"a name is kept as written", writeTranscript(t, `{"type":"custom-title","customTitle":"a\u202eb\u001b[31m"}`+"\n"), "a\u202eb\x1b[31m"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := Claude{}.CustomName(context.Background(), tt.path)
			if err != nil {
				t.Fatalf("CustomName() error = %v", err)
			}

			if got != tt.want {
				t.Errorf("CustomName() = %q, want %q", got, tt.want)
			}
		})
	}
}

// Only the last 256 KiB are read: an entry whose line starts inside the window
// is seen, one that starts before it is not.
func TestCustomNameWindow(t *testing.T) {
	line := entry("windowed") + "\n"

	tests := []struct {
		name string
		// before is how many bytes of the entry's line lie before the window.
		before int
		want   string
	}{
		{"the line fills the window to its start", 0, "windowed"},
		{"the first byte is before the window", 1, ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// A lead line, the entry line, then one padding line sized so that the
			// window starts tt.before bytes into the entry line.
			tail := window - len(line) + tt.before
			if tail < len(padFrame) {
				t.Fatalf("bad case: tail = %d", tail)
			}

			content := padLine(300) + line + padLine(tail)

			got, err := Claude{}.CustomName(context.Background(), writeTranscript(t, content))
			if err != nil {
				t.Fatal(err)
			}

			if got != tt.want {
				t.Errorf("CustomName() = %q, want %q", got, tt.want)
			}
		})
	}
}

// A file far larger than the window is read by seeking to its end: the
// process allocates a small fraction of the file's size.
func TestCustomNameReadsOnlyTheEnd(t *testing.T) {
	path := filepath.Join(t.TempDir(), "big.jsonl")

	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}

	// A sparse head of NUL bytes; read whole, it would be 4 MiB of memory.
	const head = 4 << 20
	if err := f.Truncate(head); err != nil {
		t.Fatal(err)
	}

	if _, err := f.WriteAt([]byte("\n"+entry("tail-name")+"\n"), head); err != nil {
		t.Fatal(err)
	}

	if err := f.Close(); err != nil {
		t.Fatal(err)
	}

	var before, after runtime.MemStats

	runtime.ReadMemStats(&before)

	got, err := Claude{}.CustomName(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}

	runtime.ReadMemStats(&after)

	if got != "tail-name" {
		t.Errorf("CustomName() = %q, want tail-name", got)
	}

	if alloc := after.TotalAlloc - before.TotalAlloc; alloc > 2*window {
		t.Errorf("CustomName() allocated %d bytes for a %d byte file, want at most %d", alloc, head, 2*window)
	}
}

func TestCustomNameBoundsTheName(t *testing.T) {
	long := strings.Repeat("é", 500)

	got, err := Claude{}.CustomName(context.Background(), writeTranscript(t, entry(long)+"\n"))
	if err != nil {
		t.Fatal(err)
	}

	if want := strings.Repeat("é", maxNameRunes); got != want {
		t.Errorf("a 500-rune name came back with %d runes, want %d", len([]rune(got)), maxNameRunes)
	}
}

func TestCustomNameErrors(t *testing.T) {
	t.Run("a directory", func(t *testing.T) {
		if name, err := (Claude{}).CustomName(context.Background(), t.TempDir()); err == nil || name != "" {
			t.Errorf("CustomName() = %q, %v, want no name and an error", name, err)
		}
	})

	t.Run("a pipe does not block", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "fifo")
		if err := syscall.Mkfifo(path, 0o600); err != nil {
			t.Skipf("no named pipes here: %v", err)
		}

		done := make(chan error, 1)

		go func() {
			_, err := Claude{}.CustomName(context.Background(), path)
			done <- err
		}()

		select {
		case err := <-done:
			if err == nil {
				t.Error("CustomName() read a pipe without an error")
			}
		case <-time.After(5 * time.Second):
			t.Fatal("CustomName() blocked on a pipe")
		}
	})

	// The rail tells a probe that gave no answer from a failed read by these.
	t.Run("a cancelled context", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		cancel()

		if name, err := (Claude{}).CustomName(ctx, writeTranscript(t, entry("x")+"\n")); !errors.Is(err, context.Canceled) || name != "" {
			t.Errorf("CustomName() = %q, %v, want no name and context.Canceled", name, err)
		}
	})

	t.Run("an expired context", func(t *testing.T) {
		ctx, cancel := context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
		defer cancel()

		if name, err := (Claude{}).CustomName(ctx, writeTranscript(t, entry("x")+"\n")); !errors.Is(err, context.DeadlineExceeded) || name != "" {
			t.Errorf("CustomName() = %q, %v, want no name and context.DeadlineExceeded", name, err)
		}
	})
}
