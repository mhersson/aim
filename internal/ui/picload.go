package ui

import (
	"container/list"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/mhersson/aim/internal/ui/kitty"
)

const (
	pictureWorkers     = 4
	pictureMemBytes    = 64 << 20
	pictureFetchTime   = 20 * time.Second
	pictureCacheMaxAge = 30 * 24 * time.Hour
	pictureCacheDir    = 0o700
)

var errPictureTooBig = errors.New("image over the size limit")

// pictureFetcher downloads a remote image of at most limit bytes.
type pictureFetcher interface {
	Fetch(ctx context.Context, url, docHost string, limit int64) ([]byte, error)
}

// pictureLoader runs the registry's jobs on worker goroutines: it reads or
// fetches the bytes, decodes sizes and encodes transmissions, and answers with
// pictureDoneMsg. The App owns it and Close stops it.
type pictureLoader struct {
	cancel   context.CancelFunc // ends the workers' context, so a running fetch stops
	fetch    pictureFetcher
	cacheDir string
	log      *slog.Logger
	now      func() time.Time

	out     chan pictureDoneMsg
	done    chan struct{}
	wg      sync.WaitGroup
	stop    func() bool // cancels the AfterFunc registered on the parent context
	spawn   func()      // starts the workers; add calls it once, under mu
	started bool

	mu      sync.Mutex
	cond    *sync.Cond
	closed  bool
	queue   []pictureJob
	pending map[pictureJob]struct{} // queued or running

	memMu sync.Mutex
	mem   map[memKey]*list.Element
	order *list.List // front is the most recent
	used  int
}

// memKey adds a local file's size and mtime to its key, so a rewritten file
// misses; remote keys leave the stamp zero.
type memKey struct {
	key       pictureKey
	size, mod int64
}

type memPicture struct {
	key  memKey
	data []byte
}

// newPictureLoader starts the workers. A nil f loads no remote image and an
// empty cacheDir keeps no disk cache.
func newPictureLoader(ctx context.Context, f pictureFetcher, cacheDir string, log *slog.Logger) *pictureLoader {
	if log == nil {
		log = slog.New(slog.DiscardHandler)
	}

	ctx, cancel := context.WithCancel(ctx)
	l := &pictureLoader{
		cancel: cancel, fetch: f, cacheDir: cacheDir, log: log, now: time.Now,
		out: make(chan pictureDoneMsg, 256), done: make(chan struct{}),
		pending: map[pictureJob]struct{}{},
		mem:     map[memKey]*list.Element{}, order: list.New(),
	}
	l.cond = sync.NewCond(&l.mu)

	l.spawn = func() {
		for i := range pictureWorkers {
			l.wg.Add(1)

			go l.work(ctx, i == 0)
		}
	}

	l.stop = context.AfterFunc(ctx, l.shutdown)

	return l
}

// add queues j unless it is already queued or running; it never blocks.
func (l *pictureLoader) add(j pictureJob) {
	l.mu.Lock()
	defer l.mu.Unlock()

	if l.closed {
		return
	}

	if _, ok := l.pending[j]; ok {
		return
	}

	if !l.started {
		l.started = true
		l.spawn()
	}

	l.pending[j] = struct{}{}
	l.queue = append(l.queue, j)
	l.cond.Signal()
}

// wait returns a command that delivers the next answer, or nothing once the
// loader stops. The update loop re-arms it after each answer.
func (l *pictureLoader) wait() tea.Cmd {
	return func() tea.Msg {
		select {
		case m := <-l.out:
			return m
		case <-l.done:
			return nil
		}
	}
}

// close stops the workers and waits for them.
func (l *pictureLoader) close() {
	l.stop()
	l.cancel()
	l.shutdown()
	l.wg.Wait()
}

func (l *pictureLoader) shutdown() {
	l.mu.Lock()
	defer l.mu.Unlock()

	if l.closed {
		return
	}

	l.closed = true
	l.queue = nil
	close(l.done)
	l.cond.Broadcast()
}

func (l *pictureLoader) work(ctx context.Context, sweeper bool) {
	defer l.wg.Done()

	if sweeper {
		l.sweep()
	}

	for {
		l.mu.Lock()
		for len(l.queue) == 0 && !l.closed {
			l.cond.Wait()
		}

		if l.closed {
			l.mu.Unlock()

			return
		}

		j := l.queue[0]
		l.queue = l.queue[1:]
		l.mu.Unlock()

		msg := l.run(ctx, j)

		l.mu.Lock()
		delete(l.pending, j)
		l.mu.Unlock()

		select {
		case l.out <- msg:
		case <-l.done:
			return
		}
	}
}

func (l *pictureLoader) run(ctx context.Context, j pictureJob) pictureDoneMsg {
	msg := pictureDoneMsg{job: j}

	data, err := l.bytes(ctx, j)
	if err != nil {
		msg.err = err

		return msg
	}

	if !j.render {
		msg.w, msg.h, msg.err = kitty.Size(data)

		return msg
	}

	png, err := kitty.Render(data, j.cols, j.rows, j.cell)
	if err != nil {
		msg.err = fmt.Errorf("render picture: %w", err)

		return msg
	}

	msg.seq = kitty.Transmit(j.id, png, j.cols, j.rows)

	return msg
}

// bytes returns the source's bytes from memory, else the file or the network.
func (l *pictureLoader) bytes(ctx context.Context, j pictureJob) ([]byte, error) {
	if !strings.Contains(j.key.src, "://") {
		return l.readLocal(j)
	}

	mk := memKey{key: j.key}
	if data, ok := l.memGet(mk); ok {
		return data, nil
	}

	data, err := l.readRemote(ctx, j)
	if err != nil {
		return nil, err
	}

	l.memPut(mk, data)

	return data, nil
}

// readLocal reads a regular file inside the root. It stats before it opens, so
// a FIFO or device never blocks a worker, and opens non-blocking as a second guard.
func (l *pictureLoader) readLocal(j pictureJob) ([]byte, error) {
	rel, err := filepath.Rel(j.root, j.key.src)
	if err != nil {
		return nil, fmt.Errorf("picture path: %w", err)
	}

	r, err := os.OpenRoot(j.root)
	if err != nil {
		return nil, fmt.Errorf("open picture root: %w", err)
	}

	defer func() { _ = r.Close() }()

	fi, err := r.Stat(rel)
	if err != nil {
		return nil, fmt.Errorf("stat picture: %w", err)
	}

	if !fi.Mode().IsRegular() {
		return nil, errors.New("picture is not a regular file")
	}

	mk := memKey{key: j.key, size: fi.Size(), mod: fi.ModTime().UnixNano()}
	if data, ok := l.memGet(mk); ok {
		return data, nil
	}

	f, err := r.OpenFile(rel, os.O_RDONLY|syscall.O_NONBLOCK, 0)
	if err != nil {
		return nil, fmt.Errorf("open picture: %w", err)
	}

	defer func() { _ = f.Close() }()

	data, err := io.ReadAll(io.LimitReader(f, pictureMaxBytes+1))
	if err != nil {
		return nil, fmt.Errorf("read picture: %w", err)
	}

	if len(data) > pictureMaxBytes {
		return nil, errPictureTooBig
	}

	l.memPut(mk, data)

	return data, nil
}

func (l *pictureLoader) readRemote(ctx context.Context, j pictureJob) ([]byte, error) {
	if l.fetch == nil {
		return nil, errors.New("remote pictures are off")
	}

	path := l.cachePath(j.key.src)
	if data, ok := l.cacheGet(path); ok {
		return data, nil
	}

	ctx, cancel := context.WithTimeout(ctx, pictureFetchTime)
	defer cancel()

	data, err := l.fetch.Fetch(ctx, j.key.src, j.host, pictureMaxBytes)
	if err != nil {
		return nil, fmt.Errorf("fetch picture: %w", err)
	}

	if len(data) > pictureMaxBytes {
		return nil, errPictureTooBig
	}

	if _, _, err := kitty.Size(data); err != nil {
		return nil, fmt.Errorf("fetched picture: %w", err)
	}

	l.cachePut(path, data)

	return data, nil
}

func (l *pictureLoader) cachePath(src string) string {
	if l.cacheDir == "" {
		return ""
	}

	sum := sha256.Sum256([]byte(src))

	return filepath.Join(l.cacheDir, hex.EncodeToString(sum[:]))
}

func (l *pictureLoader) cacheGet(path string) ([]byte, bool) {
	if path == "" {
		return nil, false
	}

	fi, err := os.Stat(path)
	if err != nil || !fi.Mode().IsRegular() || fi.Size() > pictureMaxBytes {
		return nil, false
	}

	data, err := os.ReadFile(path)
	if err != nil {
		return nil, false
	}

	now := l.now()
	if err := os.Chtimes(path, now, now); err != nil {
		l.log.Debug("touch picture cache", "err", err)
	}

	return data, true
}

// cachePut writes through a temp file and a rename; a failure only costs the
// next fetch.
func (l *pictureLoader) cachePut(path string, data []byte) {
	if path == "" {
		return
	}

	if err := os.MkdirAll(l.cacheDir, pictureCacheDir); err != nil {
		l.log.Debug("picture cache dir", "err", err)

		return
	}

	tmp, err := os.CreateTemp(l.cacheDir, ".tmp-*")
	if err != nil {
		l.log.Debug("picture cache write", "err", err)

		return
	}

	_, err = tmp.Write(data)
	if cerr := tmp.Close(); err == nil {
		err = cerr
	}

	if err == nil {
		err = os.Rename(tmp.Name(), path)
	}

	if err != nil {
		l.log.Debug("picture cache write", "err", err)

		_ = os.Remove(tmp.Name())
	}
}

// sweep removes cache files untouched for 30 days. It runs to the end whatever
// the context, so close leaves the cache swept.
func (l *pictureLoader) sweep() {
	if l.cacheDir == "" {
		return
	}

	entries, err := os.ReadDir(l.cacheDir)
	if err != nil {
		return
	}

	cutoff := l.now().Add(-pictureCacheMaxAge)

	for _, e := range entries {
		fi, err := e.Info()
		if err != nil || !fi.Mode().IsRegular() || !fi.ModTime().Before(cutoff) {
			continue
		}

		if err := os.Remove(filepath.Join(l.cacheDir, e.Name())); err != nil {
			l.log.Debug("sweep picture cache", "err", err)
		}
	}
}

func (l *pictureLoader) memGet(k memKey) ([]byte, bool) {
	l.memMu.Lock()
	defer l.memMu.Unlock()

	el, ok := l.mem[k]
	if !ok {
		return nil, false
	}

	l.order.MoveToFront(el)

	return el.Value.(*memPicture).data, true
}

func (l *pictureLoader) memPut(k memKey, data []byte) {
	l.memMu.Lock()
	defer l.memMu.Unlock()

	if _, ok := l.mem[k]; ok || len(data) > pictureMemBytes {
		return
	}

	l.mem[k] = l.order.PushFront(&memPicture{k, data})
	l.used += len(data)

	for l.used > pictureMemBytes {
		el := l.order.Back()
		m := el.Value.(*memPicture)
		l.order.Remove(el)
		delete(l.mem, m.key)
		l.used -= len(m.data)
	}
}
