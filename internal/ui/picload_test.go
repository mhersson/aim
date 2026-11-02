package ui

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"image"
	"image/png"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"github.com/charmbracelet/x/ansi"

	"github.com/mhersson/aim/internal/ui/kitty"
)

func testPNG(t testing.TB, w, h int) []byte {
	t.Helper()

	var b bytes.Buffer
	if err := png.Encode(&b, image.NewRGBA(image.Rect(0, 0, w, h))); err != nil {
		t.Fatal(err)
	}

	return b.Bytes()
}

// countFetcher answers every fetch with data and counts the calls.
type countFetcher struct {
	data  []byte
	calls atomic.Int32
}

func (f *countFetcher) Fetch(_ context.Context, _, _ string, _ int64) ([]byte, error) {
	f.calls.Add(1)

	return f.data, nil
}

// recv waits for the loader's next answer.
func recv(t testing.TB, l *pictureLoader) pictureDoneMsg {
	t.Helper()

	ch := make(chan pictureDoneMsg, 1)

	go func() {
		if m, ok := l.wait()().(pictureDoneMsg); ok {
			ch <- m
		}
	}()

	select {
	case m := <-ch:
		return m
	case <-time.After(30 * time.Second):
		t.Fatal("no answer from the loader")

		return pictureDoneMsg{}
	}
}

func localJob(root, src string) pictureJob {
	return pictureJob{key: pictureKey{src, root}, root: root}
}

func TestLoaderLocal(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	outside := t.TempDir()

	write := func(dir, name string, data []byte) string {
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, data, 0o600); err != nil {
			t.Fatal(err)
		}

		return p
	}

	good := write(root, "ok.png", testPNG(t, 40, 20))
	out := write(outside, "out.png", testPNG(t, 4, 4))
	link := filepath.Join(root, "link.png")

	if err := os.Symlink(out, link); err != nil {
		t.Skipf("no symlinks: %v", err)
	}

	if err := os.Mkdir(filepath.Join(root, "dir"), 0o700); err != nil {
		t.Fatal(err)
	}

	fifo := filepath.Join(root, "pipe.png")
	if err := syscall.Mkfifo(fifo, 0o600); err != nil {
		t.Fatal(err)
	}

	// A valid PNG padded to one byte past the cap, so only the size refuses it.
	big := write(root, "big.png", testPNG(t, 4, 4))
	if err := os.Truncate(big, pictureMaxBytes+1); err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name    string
		src     string
		w, h    int
		wantErr bool
	}{
		{"inside", good, 40, 20, false},
		{"symlink outside", link, 0, 0, true},
		{"folder", filepath.Join(root, "dir"), 0, 0, true},
		{"missing", filepath.Join(root, "none.png"), 0, 0, true},
		{"too big", big, 0, 0, true},
		{"fifo", fifo, 0, 0, true},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			l := newPictureLoader(t.Context(), nil, "", nil)
			t.Cleanup(l.close)

			j := localJob(root, tc.src)
			l.add(j)

			m := recv(t, l)
			if m.job != j {
				t.Fatalf("answer for %+v, want %+v", m.job, j)
			}

			if (m.err != nil) != tc.wantErr {
				t.Fatalf("err = %v, want error %v", m.err, tc.wantErr)
			}

			if m.w != tc.w || m.h != tc.h {
				t.Fatalf("size = %dx%d, want %dx%d", m.w, m.h, tc.w, tc.h)
			}
		})
	}
}

func TestLoaderRemoteCache(t *testing.T) {
	t.Parallel()

	const src = "https://github.com/o/r/raw/x.png"

	dir := filepath.Join(t.TempDir(), "images")
	sum := sha256.Sum256([]byte(src))
	file := filepath.Join(dir, hex.EncodeToString(sum[:]))
	job := pictureJob{key: pictureKey{src: src}}

	f := &countFetcher{data: testPNG(t, 8, 6)}

	l := newPictureLoader(t.Context(), f, dir, nil)
	l.add(job)

	if m := recv(t, l); m.err != nil || m.w != 8 || m.h != 6 {
		t.Fatalf("answer = %+v", m)
	}

	l.close()

	if f.calls.Load() != 1 {
		t.Fatalf("fetches = %d, want 1", f.calls.Load())
	}

	fi, err := os.Stat(file)
	if err != nil {
		t.Fatalf("cache file: %v", err)
	}

	if runtime.GOOS != "windows" && fi.Mode().Perm() != 0o600 {
		t.Fatalf("file mode = %v, want 0600", fi.Mode().Perm())
	}

	di, err := os.Stat(dir)
	if err != nil {
		t.Fatal(err)
	}

	if runtime.GOOS != "windows" && di.Mode().Perm() != 0o700 {
		t.Fatalf("dir mode = %v, want 0700", di.Mode().Perm())
	}

	old := filepath.Join(dir, "old")
	young := filepath.Join(dir, "young")

	for name, age := range map[string]time.Duration{old: 31 * 24 * time.Hour, young: 29 * 24 * time.Hour} {
		if err := os.WriteFile(name, []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}

		at := time.Now().Add(-age)
		if err := os.Chtimes(name, at, at); err != nil {
			t.Fatal(err)
		}
	}

	l2 := newPictureLoader(t.Context(), f, dir, nil)
	l2.add(job)

	if m := recv(t, l2); m.err != nil || m.w != 8 {
		t.Fatalf("cached answer = %+v", m)
	}

	l2.close() // worker 0 finishes its sweep before close returns

	if f.calls.Load() != 1 {
		t.Fatalf("fetches after a cache hit = %d, want 1", f.calls.Load())
	}

	if _, err := os.Stat(old); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("31-day file: %v, want gone", err)
	}

	if _, err := os.Stat(young); err != nil {
		t.Fatalf("29-day file: %v, want kept", err)
	}

	if _, err := os.Stat(file); err != nil {
		t.Fatalf("fresh cache file swept: %v", err)
	}
}

func TestLoaderRejectsNonImageRemote(t *testing.T) {
	t.Parallel()

	dir := filepath.Join(t.TempDir(), "images")
	f := &countFetcher{data: []byte("<html>sign in</html>")}
	job := pictureJob{key: pictureKey{src: "https://github.com/o/r/login.png"}}

	for range 2 {
		l := newPictureLoader(t.Context(), f, dir, nil)
		l.add(job)

		if m := recv(t, l); m.err == nil {
			t.Fatal("want an error for an HTML body")
		}

		l.close()
	}

	if f.calls.Load() != 2 {
		t.Fatalf("fetches = %d, want 2 (a bad body is not cached)", f.calls.Load())
	}

	if entries, _ := os.ReadDir(dir); len(entries) != 0 {
		t.Fatalf("cache holds %d files, want none", len(entries))
	}
}

func TestLoaderLocalRewrite(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	src := filepath.Join(root, "shot.png")

	l := newPictureLoader(t.Context(), nil, "", nil)
	t.Cleanup(l.close)

	for _, w := range []int{10, 30} {
		if err := os.WriteFile(src, testPNG(t, w, 5), 0o600); err != nil {
			t.Fatal(err)
		}

		l.add(localJob(root, src))

		if m := recv(t, l); m.err != nil || m.w != w {
			t.Fatalf("answer = %+v, want width %d", m, w)
		}
	}
}

func TestLoaderRender(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	src := filepath.Join(root, "a.png")

	if err := os.WriteFile(src, testPNG(t, 40, 20), 0o600); err != nil {
		t.Fatal(err)
	}

	cell := kitty.Cell{W: 10, H: 20}
	j := localJob(root, src)
	j.render, j.id, j.cols, j.rows, j.cell = true, 7, 4, 1, cell

	l := newPictureLoader(t.Context(), nil, "", nil)
	t.Cleanup(l.close)
	l.add(j)

	m := recv(t, l)
	if m.err != nil {
		t.Fatal(m.err)
	}

	if !strings.HasPrefix(m.seq, "\x1b_Ga=t,f=100,t=d,i=7,q=2,") {
		t.Fatalf("seq starts %q", m.seq[:min(len(m.seq), 40)])
	}

	place := ansi.KittyGraphics(nil, "a=p", "U=1", "i=7", "c=4", "r=1", "q=2")
	if !strings.HasSuffix(m.seq, place) {
		t.Fatalf("seq does not end with the placement %q", place)
	}

	data, err := os.ReadFile(src)
	if err != nil {
		t.Fatal(err)
	}

	pngData, err := kitty.Render(data, 4, 1, cell)
	if err != nil {
		t.Fatal(err)
	}

	if want := kitty.Transmit(7, pngData, 4, 1); m.seq != want {
		t.Fatal("seq is not Transmit of Render")
	}
}

func TestLoaderNoFetcher(t *testing.T) {
	t.Parallel()

	l := newPictureLoader(t.Context(), nil, t.TempDir(), nil)
	t.Cleanup(l.close)

	l.add(pictureJob{key: pictureKey{src: "https://github.com/o/r/x.png"}})

	if m := recv(t, l); m.err == nil {
		t.Fatal("want an error with no fetcher")
	}
}

// gateFetcher blocks every fetch until release closes and tracks how many run.
type gateFetcher struct {
	data     []byte
	release  chan struct{}
	entered  chan struct{}
	inflight atomic.Int32
	peak     atomic.Int32
	calls    atomic.Int32
}

func (f *gateFetcher) Fetch(ctx context.Context, _, _ string, _ int64) ([]byte, error) {
	n := f.inflight.Add(1)
	defer f.inflight.Add(-1)

	for {
		p := f.peak.Load()
		if n <= p || f.peak.CompareAndSwap(p, n) {
			break
		}
	}

	f.calls.Add(1)

	f.entered <- struct{}{}

	select {
	case <-f.release:
		return f.data, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func TestLoaderConcurrency(t *testing.T) {
	t.Parallel()

	const n = 20

	f := &gateFetcher{data: testPNG(t, 4, 4), release: make(chan struct{}), entered: make(chan struct{}, n)}
	l := newPictureLoader(t.Context(), f, "", nil)

	t.Cleanup(l.close)

	var jobs []pictureJob
	for i := range n {
		jobs = append(jobs, pictureJob{key: pictureKey{src: "https://github.com/o/r/" + string(rune('a'+i)) + ".png"}})
	}

	for _, j := range jobs {
		l.add(j)
	}

	l.add(jobs[0]) // a duplicate of a running job
	l.add(jobs[n-1])

	for range 4 {
		<-f.entered
	}

	close(f.release)

	seen := map[string]int{}

	for range n {
		m := recv(t, l)
		if m.err != nil {
			t.Fatal(m.err)
		}

		seen[m.job.key.src]++
	}

	if p := f.peak.Load(); p > 4 {
		t.Fatalf("peak in flight = %d, want at most 4", p)
	}

	if c := f.calls.Load(); c != n {
		t.Fatalf("fetches = %d, want %d (duplicates run once)", c, n)
	}

	for src, c := range seen {
		if c != 1 {
			t.Fatalf("%s answered %d times", src, c)
		}
	}
}

// Not parallel: it counts goroutines.
func TestLoaderClose(t *testing.T) {
	before := runtime.NumGoroutine()

	l := newPictureLoader(context.Background(), &countFetcher{data: testPNG(t, 4, 4)}, "", nil)
	l.add(pictureJob{key: pictureKey{src: "https://github.com/o/r/y.png"}})
	recv(t, l)
	l.close()

	deadline := time.Now().Add(10 * time.Second)
	for runtime.NumGoroutine() > before && time.Now().Before(deadline) {
		runtime.Gosched()
	}

	if got := runtime.NumGoroutine(); got > before {
		t.Fatalf("goroutines = %d, want at most %d", got, before)
	}

	l.add(pictureJob{key: pictureKey{src: "https://github.com/o/r/x.png"}})
	l.close() // twice is fine

	l.mu.Lock()
	queued := len(l.queue)
	l.mu.Unlock()

	if queued != 0 {
		t.Fatalf("add after close queued %d jobs", queued)
	}
}
