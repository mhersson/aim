package git

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/mhersson/aim/internal/git/fsnotify"
)

const watchWait = 5 * time.Second

var fastWatch = WatchOptions{Debounce: 30 * time.Millisecond, MaxDelay: 300 * time.Millisecond}

func startWatch(t *testing.T, dir string, opts WatchOptions) *Watcher {
	t.Helper()

	return startWatchContext(context.Background(), t, dir, opts)
}

// startWatchContext is startWatch with ctx for the repository and the watcher,
// so a context from wrapGit routes the watcher's git calls through the wrapper.
func startWatchContext(ctx context.Context, t *testing.T, dir string, opts WatchOptions) *Watcher {
	t.Helper()

	r, err := Open(ctx, dir)
	if err != nil {
		t.Fatal(err)
	}

	w, err := Watch(ctx, r, opts)
	if err != nil {
		t.Fatal(err)
	}

	t.Cleanup(func() { _ = w.Close() })

	return w
}

// collect gathers batches until done accepts everything seen so far, failing at
// the deadline.
func collect(t *testing.T, w *Watcher, done func(Batch) bool) Batch {
	t.Helper()

	var all Batch

	deadline := time.After(watchWait)

	for {
		select {
		case b, ok := <-w.Batches():
			if !ok {
				t.Fatalf("batches closed; saw %+v", all)
			}

			all = mergeBatches(all, b)
			if done(all) {
				return all
			}
		case err := <-w.Errors():
			t.Fatalf("watch error: %v", err)
		case <-deadline:
			t.Fatalf("timed out; saw %+v", all)
		}
	}
}

func waitPaths(t *testing.T, w *Watcher, want ...string) Batch {
	t.Helper()

	return collect(t, w, func(b Batch) bool {
		for _, p := range want {
			if !slices.Contains(b.Paths, p) {
				return false
			}
		}

		return true
	})
}

// barrier writes name in dir and waits for the batch that names it. Events are
// queued and handled in order, so that batch carries everything written before.
func barrier(t *testing.T, w *Watcher, dir, name string) {
	t.Helper()

	writeFile(t, dir, name, "barrier\n")
	waitPaths(t, w, name)
}

// noGitBefore writes a sentinel in dir and collects batches until it arrives,
// failing when a batch on the way sets Git or lists a path under .git.
func noGitBefore(t *testing.T, w *Watcher, dir, sentinel string) {
	t.Helper()

	writeFile(t, dir, sentinel, "sentinel\n")

	collect(t, w, func(b Batch) bool {
		if b.Git || anyPathWithPrefix(b, ".git") {
			t.Fatalf("snapshot ref woke the watcher: %+v", b)
		}

		return slices.Contains(b.Paths, sentinel)
	})
}

func anyPathWithPrefix(b Batch, prefix string) bool {
	return slices.ContainsFunc(b.Paths, func(p string) bool { return strings.HasPrefix(p, prefix) })
}

func watchedDirs(t *testing.T, w *Watcher) []string {
	t.Helper()

	return w.fs.WatchList()
}

func hasWatch(t *testing.T, w *Watcher, dir string) bool {
	t.Helper()

	return slices.Contains(watchedDirs(t, w), dir)
}

func eventually(t *testing.T, what string, cond func() bool) {
	t.Helper()

	deadline := time.Now().Add(watchWait)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}

		time.Sleep(10 * time.Millisecond)
	}
}

// settle waits until nothing arrives for a debounce plus a margin.
func settle(t *testing.T, w *Watcher) {
	t.Helper()

	for {
		select {
		case <-w.Batches():
		case <-time.After(400 * time.Millisecond):
			return
		}
	}
}

func baseRepo(t *testing.T) string {
	t.Helper()

	return copyTemplate(t, &baseTemplate, func(dir string) error {
		return buildRepo(dir, map[string]string{"tracked.txt": "one\n", "src/a.go": "package a\n"})
	})
}

func TestWatchWriteTrackedFile(t *testing.T) {
	t.Parallel()
	dir := baseRepo(t)
	w := startWatch(t, dir, fastWatch)

	writeFile(t, dir, "tracked.txt", "two\n")

	b := waitPaths(t, w, "tracked.txt")
	if b.Git || b.Overflow {
		t.Fatalf("plain write flagged %+v", b)
	}
}

func TestWatchNewNestedDirectory(t *testing.T) {
	t.Parallel()
	dir := baseRepo(t)
	w := startWatch(t, dir, fastWatch)

	writeFile(t, dir, "x/y/z/new.txt", "n\n")
	waitPaths(t, w, "x/y/z/new.txt")

	if !hasWatch(t, w, filepath.Join(dir, "x", "y", "z")) {
		t.Fatalf("no watch on the new directory: %v", watchedDirs(t, w))
	}

	writeFile(t, dir, "x/y/z/second.txt", "n\n")
	waitPaths(t, w, "x/y/z/second.txt")
}

func TestWatchDeletedAndRecreatedDirectory(t *testing.T) {
	t.Parallel()
	dir := baseRepo(t)
	w := startWatch(t, dir, fastWatch)

	if err := os.RemoveAll(filepath.Join(dir, "src")); err != nil {
		t.Fatal(err)
	}

	waitPaths(t, w, "src/a.go")
	barrier(t, w, dir, "barrier1.txt")

	writeFile(t, dir, "src/b.go", "package a\n")
	waitPaths(t, w, "src/b.go")

	writeFile(t, dir, "src/c.go", "package a\n")
	waitPaths(t, w, "src/c.go")

	// Removed and recreated before the debounce fires.
	if err := os.RemoveAll(filepath.Join(dir, "src")); err != nil {
		t.Fatal(err)
	}

	writeFile(t, dir, "src/d.go", "package a\n")
	waitPaths(t, w, "src/d.go")
	barrier(t, w, dir, "barrier2.txt")

	writeFile(t, dir, "src/e.go", "package a\n")
	waitPaths(t, w, "src/e.go")
}

func TestWatchIgnoredDirectoryGetsNoWatch(t *testing.T) {
	t.Parallel()
	dir := newRepo(t)
	writeFile(t, dir, ".gitignore", "node_modules/\n*.log\n")
	writeFile(t, dir, "tracked.txt", "one\n")
	writeFile(t, dir, "node_modules/pkg/index.js", "x\n")
	commitAll(t, dir, "init")

	w := startWatch(t, dir, fastWatch)

	for _, d := range watchedDirs(t, w) {
		if strings.Contains(d, "node_modules") {
			t.Fatalf("watch on ignored directory %s", d)
		}
	}

	writeFile(t, dir, "node_modules/pkg/index.js", "y\n")
	writeFile(t, dir, "visible.txt", "v\n")

	b := waitPaths(t, w, "visible.txt")
	if anyPathWithPrefix(b, "node_modules") {
		t.Fatalf("ignored path in %+v", b)
	}
}

func TestWatchNewIgnoredDirectoryWithManyFiles(t *testing.T) {
	t.Parallel()
	dir := newRepo(t)
	writeFile(t, dir, ".gitignore", "node_modules/\n")
	writeFile(t, dir, "tracked.txt", "one\n")
	commitAll(t, dir, "init")

	w := startWatch(t, dir, fastWatch)

	for i := range 40 {
		writeFile(t, dir, fmt.Sprintf("node_modules/p%d/index.js", i%4), fmt.Sprint(i))
	}

	writeFile(t, dir, "visible.txt", "v\n")

	b := waitPaths(t, w, "visible.txt")
	if anyPathWithPrefix(b, "node_modules") {
		t.Fatalf("ignored path in %+v", b)
	}

	for _, d := range watchedDirs(t, w) {
		if strings.Contains(d, "node_modules") {
			t.Fatalf("watch on ignored directory %s", d)
		}
	}
}

func TestWatchIgnoredFileInWatchedDirectory(t *testing.T) {
	t.Parallel()
	dir := newRepo(t)
	writeFile(t, dir, ".gitignore", "*.log\n")
	writeFile(t, dir, "src/a.go", "package a\n")
	commitAll(t, dir, "init")

	w := startWatch(t, dir, fastWatch)

	writeFile(t, dir, "src/debug.log", "noise\n")
	writeFile(t, dir, "src/a.go", "package b\n")

	b := waitPaths(t, w, "src/a.go")
	if slices.Contains(b.Paths, "src/debug.log") {
		t.Fatalf("ignored file listed: %+v", b)
	}

	// An ignored file created inside a new directory is dropped too.
	writeFile(t, dir, "fresh/keep.txt", "k\n")
	writeFile(t, dir, "fresh/skip.log", "s\n")

	b = waitPaths(t, w, "fresh/keep.txt")
	if slices.Contains(b.Paths, "fresh/skip.log") {
		t.Fatalf("ignored file listed: %+v", b)
	}
}

func TestWatchTrackedFileMatchingIgnorePatternIsReported(t *testing.T) {
	t.Parallel()
	dir := newRepo(t)
	writeFile(t, dir, ".gitignore", "*.log\n")
	writeFile(t, dir, "kept.log", "one\n")
	commitAll(t, dir, "init")
	gitOut(t, dir, "add", "-f", "kept.log")

	w := startWatch(t, dir, fastWatch)

	writeFile(t, dir, "kept.log", "two\n")
	waitPaths(t, w, "kept.log")
}

func TestWatchMainCheckoutSkipsWorktreesDirectory(t *testing.T) {
	t.Parallel()
	dir := baseRepo(t)
	writeFile(t, dir, ".worktrees/feat/file.txt", "x\n")

	w := startWatch(t, dir, fastWatch)

	for _, d := range watchedDirs(t, w) {
		if strings.Contains(d, ".worktrees") {
			t.Fatalf("watch inside .worktrees: %s", d)
		}
	}

	writeFile(t, dir, ".worktrees/feat/file.txt", "y\n")
	writeFile(t, dir, "tracked.txt", "two\n")

	b := waitPaths(t, w, "tracked.txt")
	if anyPathWithPrefix(b, ".worktrees") {
		t.Fatalf(".worktrees listed: %+v", b)
	}
}

func TestWatchCommitSetsGitWithoutGitPaths(t *testing.T) {
	t.Parallel()
	dir := baseRepo(t)

	w := startWatch(t, dir, fastWatch)

	writeFile(t, dir, "tracked.txt", "two\n")
	waitPaths(t, w, "tracked.txt")
	barrier(t, w, dir, "barrier.txt")

	gitOut(t, dir, "commit", "-q", "-am", "second")

	b := collect(t, w, func(b Batch) bool { return b.Git })
	if anyPathWithPrefix(b, ".git") {
		t.Fatalf("git internals listed: %+v", b)
	}
}

func TestWatchRefChangesSetGit(t *testing.T) {
	t.Parallel()
	dir := baseRepo(t)

	w := startWatch(t, dir, fastWatch)

	gitOut(t, dir, "update-ref", "refs/heads/other", "HEAD")
	collect(t, w, func(b Batch) bool { return b.Git })
	barrier(t, w, dir, "barrier1.txt")

	gitOut(t, dir, "update-ref", "refs/heads/feat/one", "HEAD")
	collect(t, w, func(b Batch) bool { return b.Git })
	barrier(t, w, dir, "barrier2.txt")

	// The new refs/heads/feat directory has a watch by now.
	gitOut(t, dir, "update-ref", "refs/heads/feat/two", "HEAD")
	collect(t, w, func(b Batch) bool { return b.Git })
	barrier(t, w, dir, "barrier3.txt")

	gitOut(t, dir, "pack-refs", "--all")
	collect(t, w, func(b Batch) bool { return b.Git })
}

func TestWatchSnapshotRefsDoNotWake(t *testing.T) {
	t.Parallel()

	t.Run("directory created after start", func(t *testing.T) {
		t.Parallel()
		dir := baseRepo(t)
		w := startWatch(t, dir, fastWatch)

		gitOut(t, dir, "update-ref", "refs/aim/x/1", "HEAD")
		gitOut(t, dir, "update-ref", "refs/aim/x/2", "HEAD")

		// A turn's refs sit three directories deep.
		gitOut(t, dir, "update-ref", turnRef(sessA, "0123456789abcdef", 1, false), "HEAD")
		gitOut(t, dir, "update-ref", turnRef(sessA, "0123456789abcdef", 1, true), "HEAD")
		noGitBefore(t, w, dir, "sentinel.txt")
	})

	t.Run("directory exists at start", func(t *testing.T) {
		t.Parallel()
		dir := baseRepo(t)
		gitOut(t, dir, "update-ref", "refs/aim/x/1", "HEAD")
		gitOut(t, dir, "update-ref", turnRef(sessA, "0123456789abcdef", 1, false), "HEAD")

		w := startWatch(t, dir, fastWatch)

		for _, d := range watchedDirs(t, w) {
			if strings.Contains(d, filepath.Join("refs", "aim")) {
				t.Fatalf("watch on snapshot refs: %s", d)
			}
		}

		gitOut(t, dir, "update-ref", "refs/aim/x/2", "HEAD")
		gitOut(t, dir, "update-ref", turnRef(sessA, "0123456789abcdef", 2, false), "HEAD")
		noGitBefore(t, w, dir, "sentinel.txt")
	})
}

// fakeTimer is a watchTimer that fires only when a test says so. Each Reset
// is reported on resets, so a test can wait for the watcher to see an event.
type fakeTimer struct {
	c      chan time.Time
	resets chan time.Duration
}

func newFakeTimer() *fakeTimer {
	return &fakeTimer{c: make(chan time.Time, 1), resets: make(chan time.Duration, 64)}
}

func (f *fakeTimer) C() <-chan time.Time { return f.c }
func (f *fakeTimer) Reset(d time.Duration) {
	select {
	case f.resets <- d:
	default:
	}
}
func (f *fakeTimer) Stop() {}

func (f *fakeTimer) fire() { f.c <- time.Time{} }

// awaitReset waits for the watcher to reset the timer to want.
func (f *fakeTimer) awaitReset(t *testing.T, want time.Duration) {
	t.Helper()

	select {
	case d := <-f.resets:
		if d != want {
			t.Fatalf("timer reset to %v, want %v", d, want)
		}
	case <-time.After(watchWait):
		t.Fatalf("timer never reset to %v", want)
	}
}

// fakeTimers sets o to run on fake timers and returns them in the order Watch
// takes them: the debounce, then the max delay.
func fakeTimers(o *WatchOptions) *[]*fakeTimer {
	var timers []*fakeTimer

	o.newTimer = func() watchTimer {
		f := newFakeTimer()
		timers = append(timers, f)

		return f
	}

	return &timers
}

func TestWatchSteadyStreamIsSentByMaxDelay(t *testing.T) {
	t.Parallel()
	dir := baseRepo(t)

	o := WatchOptions{Debounce: 400 * time.Millisecond, MaxDelay: time.Second}
	timers := fakeTimers(&o)
	w := startWatch(t, dir, o)

	if len(*timers) != 2 {
		t.Fatalf("Watch made %d timers, want 2", len(*timers))
	}

	debounce, maxDelay := (*timers)[0], (*timers)[1]

	// Each write waits for a debounce reset before the next (one write can raise
	// several events, so extra resets may queue), so the debounce never has a
	// quiet gap. The max delay is armed once.
	path := filepath.Join(dir, "tracked.txt")

	for i := range 5 {
		if err := os.WriteFile(path, []byte(fmt.Sprint(i)), 0o644); err != nil {
			t.Fatal(err)
		}

		debounce.awaitReset(t, 400*time.Millisecond)

		if i == 0 {
			maxDelay.awaitReset(t, time.Second)
		}
	}

	select {
	case b := <-w.Batches():
		t.Fatalf("batch %v before any timer fired", b.Paths)
	default:
	}

	maxDelay.fire()

	select {
	case b := <-w.Batches():
		if len(b.Paths) != 1 || b.Paths[0] != "tracked.txt" {
			t.Fatalf("batch paths = %v, want tracked.txt", b.Paths)
		}
	case <-time.After(watchWait):
		t.Fatal("no batch after the max delay fired")
	}
}

func TestWatchBatchWaitsForTheDebounce(t *testing.T) {
	t.Parallel()
	dir := baseRepo(t)
	o := WatchOptions{Debounce: 400 * time.Millisecond}
	timers := fakeTimers(&o)
	w := startWatch(t, dir, o)
	debounce := (*timers)[0]

	writeFile(t, dir, "tracked.txt", "two\n")
	debounce.awaitReset(t, 400*time.Millisecond)

	// No fake timer has fired, so nothing may have sent a batch yet.
	select {
	case b := <-w.Batches():
		t.Fatalf("batch %v before the debounce fired", b.Paths)
	default:
	}

	debounce.fire()

	select {
	case b := <-w.Batches():
		if !slices.Contains(b.Paths, "tracked.txt") {
			t.Fatalf("batch paths = %v, want tracked.txt", b.Paths)
		}
	case <-time.After(watchWait):
		t.Fatal("no batch after the debounce fired")
	}
}

// The watcher has no clock seam, and a timing bound on a real run would flake
// under load, so the default is pinned on the options the watcher runs with.
func TestWatchDefaultDebounce(t *testing.T) {
	t.Parallel()
	w := startWatch(t, newRepo(t), WatchOptions{})

	if got, want := w.opts.Debounce, 100*time.Millisecond; got != want {
		t.Errorf("default debounce = %v, want %v", got, want)
	}

	if got, want := w.opts.MaxDelay, time.Second; got != want {
		t.Errorf("default max delay = %v, want %v", got, want)
	}
}

func TestWatchGitignoreChangeAddsDirectoryWatch(t *testing.T) {
	t.Parallel()
	dir := newRepo(t)
	writeFile(t, dir, ".gitignore", "vendor/\n")
	writeFile(t, dir, "vendor/lib/lib.go", "package lib\n")
	writeFile(t, dir, "tracked.txt", "one\n")
	commitAll(t, dir, "init")

	w := startWatch(t, dir, fastWatch)

	if hasWatch(t, w, filepath.Join(dir, "vendor")) {
		t.Fatal("ignored directory watched")
	}

	writeFile(t, dir, ".gitignore", "")

	eventually(t, "watch on the un-ignored directory", func() bool {
		return hasWatch(t, w, filepath.Join(dir, "vendor", "lib"))
	})

	waitPaths(t, w, ".gitignore")
	writeFile(t, dir, "vendor/lib/more.go", "package lib\n")
	waitPaths(t, w, "vendor/lib/more.go")
}

func TestWatchLinkedWorktree(t *testing.T) {
	t.Parallel()
	dir := baseRepo(t)
	linked := filepath.Join(t.TempDir(), "wt")
	gitOut(t, dir, "worktree", "add", "-q", "-b", "feat", linked)

	linked, err := filepath.EvalSymlinks(linked)
	if err != nil {
		t.Fatal(err)
	}

	w := startWatch(t, linked, fastWatch)

	writeFile(t, linked, "tracked.txt", "linked\n")
	b := waitPaths(t, w, "tracked.txt")

	if anyPathWithPrefix(b, ".git") {
		t.Fatalf("git file listed: %+v", b)
	}

	barrier(t, w, linked, "barrier1.txt")
	gitOut(t, linked, "commit", "-q", "-am", "in linked")
	collect(t, w, func(b Batch) bool { return b.Git })
	barrier(t, w, linked, "barrier2.txt")

	// A branch moved from the main checkout shows up through the shared refs.
	gitOut(t, dir, "update-ref", "refs/heads/elsewhere", "HEAD")
	collect(t, w, func(b Batch) bool { return b.Git })
}

func TestWatchCheckoutOfManyFiles(t *testing.T) {
	t.Parallel()
	dir := newRepo(t)

	const files = 40

	for i := range files {
		writeFile(t, dir, fmt.Sprintf("d%d/f%d.txt", i%4, i), "base\n")
	}

	commitAll(t, dir, "base")
	gitOut(t, dir, "checkout", "-q", "-b", "other")

	for i := range files {
		writeFile(t, dir, fmt.Sprintf("d%d/f%d.txt", i%4, i), "other\n")
	}

	commitAll(t, dir, "other")
	gitOut(t, dir, "checkout", "-q", "main")

	w := startWatch(t, dir, fastWatch)

	gitOut(t, dir, "checkout", "-q", "other")

	// Either every changed path arrives, or the kernel queue overflowed and the
	// watcher says so.
	b := collect(t, w, func(b Batch) bool { return b.Overflow || (b.Git && len(b.Paths) >= files) })
	if anyPathWithPrefix(b, ".git") {
		t.Fatalf("git internals listed")
	}
}

func TestWatchCheckIgnoreCallsAreBatched(t *testing.T) {
	t.Parallel()
	dir := baseRepo(t)
	ctx, log := wrapGit(t, "")
	w := startWatchContext(ctx, t, dir, WatchOptions{Debounce: 200 * time.Millisecond, MaxDelay: time.Second})
	atStart := countCalls(log, "check-ignore")

	for i := range 50 {
		writeFile(t, dir, fmt.Sprintf("many/f%d.txt", i), "x\n")
	}

	waitPaths(t, w, "many/f49.txt")

	calls := countCalls(log, "check-ignore") - atStart
	if calls == 0 || calls > 5 {
		t.Fatalf("%d check-ignore calls for 50 files:\n%s", calls, wrappedCalls(t, log))
	}
}

// countCalls counts the logged git calls that mention arg.
func countCalls(logPath, arg string) int {
	data, _ := os.ReadFile(logPath)

	return strings.Count(string(data), arg)
}

func TestWatchCloseStopsEverything(t *testing.T) {
	dir := baseRepo(t)

	before := runtime.NumGoroutine()

	ctx := context.Background()

	r, err := Open(ctx, dir)
	if err != nil {
		t.Fatal(err)
	}

	w, err := Watch(ctx, r, fastWatch)
	if err != nil {
		t.Fatal(err)
	}

	writeFile(t, dir, "tracked.txt", "two\n")

	if err := w.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}

	// Events after Close are dropped without a panic.
	writeFile(t, dir, "tracked.txt", "three\n")

	for range w.Batches() {
	}

	if err := w.Close(); err != nil {
		t.Fatalf("second close: %v", err)
	}

	eventually(t, "goroutines to settle", func() bool { return runtime.NumGoroutine() <= before })
}

func TestWatchContextEndClosesBatches(t *testing.T) {
	t.Parallel()
	dir := baseRepo(t)

	r, err := Open(context.Background(), dir)
	if err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithCancel(context.Background())

	w, err := Watch(ctx, r, fastWatch)
	if err != nil {
		t.Fatal(err)
	}

	t.Cleanup(func() { _ = w.Close() })
	cancel()

	select {
	case _, ok := <-w.Batches():
		if ok {
			t.Fatal("batch after cancel")
		}
	case <-time.After(time.Second):
		t.Fatal("batches not closed after the context ended")
	}
}

func TestWatchSlowConsumerKeepsLatestChanges(t *testing.T) {
	t.Parallel()
	dir := baseRepo(t)
	ctx, log := wrapGit(t, "")
	w := startWatchContext(ctx, t, dir, fastWatch)
	atStart := countCalls(log, "check-ignore")

	// Nobody reads, so the first batch waits for its consumer while the
	// second flush merges into it. The ignore check marks the first flush.
	writeFile(t, dir, "tracked.txt", "a\n")

	eventually(t, "the first batch to be flushed", func() bool {
		return countCalls(log, "check-ignore") > atStart
	})

	writeFile(t, dir, "src/a.go", "package a // b\n")
	waitPaths(t, w, "tracked.txt", "src/a.go")
}

func TestWatchErrorNamesInotifyLimit(t *testing.T) {
	t.Parallel()

	if err := watchError("/repo/dir", syscall.ENOSPC); !errors.Is(err, syscall.ENOSPC) {
		t.Fatalf("got %v, want one wrapping ENOSPC", err)
	}
}

func TestMergeBatches(t *testing.T) {
	t.Parallel()

	got := mergeBatches(Batch{Paths: []string{"b", "a"}, Git: true}, Batch{Paths: []string{"c", "a"}, Overflow: true})

	want := Batch{Paths: []string{"a", "b", "c"}, Git: true, Overflow: true}
	if !slices.Equal(got.Paths, want.Paths) || got.Git != want.Git || got.Overflow != want.Overflow {
		t.Fatalf("got %+v want %+v", got, want)
	}
}

func TestWatchSkipsNestedRepositories(t *testing.T) {
	t.Parallel()
	dir := baseRepo(t)

	nested := filepath.Join(dir, "nested")
	writeFile(t, nested, "inner/b.txt", "b\n")
	gitOut(t, nested, "init", "-q", "-b", "main")

	w := startWatch(t, dir, fastWatch)

	for _, d := range watchedDirs(t, w) {
		if strings.HasPrefix(d, nested) {
			t.Fatalf("watch inside a nested repository: %s", d)
		}
	}

	writeFile(t, dir, "nested/inner/b.txt", "changed\n")
	writeFile(t, dir, "tracked.txt", "two\n")

	b := waitPaths(t, w, "tracked.txt")
	if anyPathWithPrefix(b, "nested/") {
		t.Fatalf("nested repository path listed: %+v", b)
	}
}

func TestWatchErrorNamesInstanceLimit(t *testing.T) {
	t.Parallel()

	if err := instanceError("/repo", syscall.EMFILE); !errors.Is(err, syscall.EMFILE) {
		t.Fatalf("got %v, want one wrapping EMFILE", err)
	}
}

func TestWatchOverflowRewalksUnwatchedDirectories(t *testing.T) {
	t.Parallel()
	dir := newRepo(t)

	r, err := Open(context.Background(), dir)
	if err != nil {
		t.Fatal(err)
	}

	fsw, err := fsnotify.NewBufferedWatcher(eventBuffer)
	if err != nil {
		t.Fatal(err)
	}

	// No loop goroutine runs, so the Create events below are never handled.
	w := &Watcher{repo: r, fs: fsw, watched: map[string]struct{}{}}

	t.Cleanup(func() { _ = fsw.Close() })

	if err := w.start(context.Background()); err != nil {
		t.Fatal(err)
	}

	writeFile(t, dir, "fresh/deep/a.go", "package a\n")

	if hasWatch(t, w, filepath.Join(dir, "fresh")) {
		t.Fatal("fresh is watched before the flush")
	}

	p := newPending()
	p.overflow = true

	b, err := w.flush(context.Background(), p)
	if err != nil {
		t.Fatal(err)
	}

	for _, d := range []string{"fresh", "fresh/deep"} {
		if !hasWatch(t, w, filepath.Join(dir, d)) {
			t.Errorf("%s has no watch after an overflow", d)
		}
	}

	if !b.Overflow || !slices.Contains(b.Paths, "fresh/deep/a.go") {
		t.Errorf("batch = %+v, want overflow and fresh/deep/a.go", b)
	}
}

// eventWatcher is a Watcher that was never started, for feeding handle
// synthetic events (what kqueue reports on macOS) on any platform.
func eventWatcher(t *testing.T) *Watcher {
	t.Helper()

	dir := newRepo(t)

	r, err := Open(t.Context(), dir)
	if err != nil {
		t.Fatal(err)
	}

	fsw, err := fsnotify.NewWatcher()
	if err != nil {
		t.Fatal(err)
	}

	t.Cleanup(func() { _ = fsw.Close() })

	w := &Watcher{
		repo:       r,
		fs:         fsw,
		refsDir:    filepath.Join(r.CommonDir, "refs"),
		ownRefsDir: filepath.Join(r.CommonDir, "refs", "aim"),
		watched:    map[string]struct{}{},
	}

	for _, d := range []string{
		r.GitDir, w.refsDir, filepath.Join(w.refsDir, "heads"), w.ownRefsDir, filepath.Join(r.Root, "src"),
	} {
		w.watched[d] = struct{}{}
	}

	return w
}

func TestKqueueEvents(t *testing.T) {
	t.Parallel()
	w := eventWatcher(t)
	gitDir, refs := w.repo.GitDir, w.refsDir

	tests := []struct {
		name     string
		ev       fsnotify.Event
		wantGit  bool
		wantPath string
		accepted bool
	}{
		{name: "dir write plus chmod on refs/heads", ev: fsnotify.Event{Name: filepath.Join(refs, "heads"), Op: fsnotify.Write | fsnotify.Chmod}},
		{name: "dir write plus chmod in worktree", ev: fsnotify.Event{Name: filepath.Join(w.repo.Root, "src"), Op: fsnotify.Write | fsnotify.Chmod}},
		{name: "file write in own refs", ev: fsnotify.Event{Name: filepath.Join(w.ownRefsDir, "s1"), Op: fsnotify.Write}},
		{name: "chmod on a ref", ev: fsnotify.Event{Name: filepath.Join(refs, "heads", "main"), Op: fsnotify.Chmod}},
		{name: "write on index", ev: fsnotify.Event{Name: filepath.Join(gitDir, "index"), Op: fsnotify.Write}, wantGit: true, accepted: true},
		{name: "write on a ref", ev: fsnotify.Event{Name: filepath.Join(refs, "heads", "main"), Op: fsnotify.Write}, wantGit: true, accepted: true},
		{name: "file write in worktree", ev: fsnotify.Event{Name: filepath.Join(w.repo.Root, "src", "a.go"), Op: fsnotify.Write}, wantPath: "src/a.go", accepted: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := newPending()
			before := w.Seq()

			got := w.handle(tt.ev, p)

			if got != tt.accepted || p.git != tt.wantGit {
				t.Errorf("handle = %v, git = %v; want %v, %v", got, p.git, tt.accepted, tt.wantGit)
			}

			if _, ok := p.paths[tt.wantPath]; tt.wantPath != "" && !ok {
				t.Errorf("paths = %v, want %q", p.paths, tt.wantPath)
			}

			if tt.wantPath == "" && len(p.paths) != 0 {
				t.Errorf("paths = %v, want none", p.paths)
			}

			if rose := w.Seq() - before; (rose == 1) != tt.accepted || rose > 1 {
				t.Errorf("Seq rose by %d, accepted = %v", rose, tt.accepted)
			}
		})
	}
}

func TestSeqCountsEventsBeforeTheBatch(t *testing.T) {
	t.Parallel()
	dir := baseRepo(t)
	opts := WatchOptions{Debounce: time.Second, MaxDelay: 3 * time.Second, SeqBase: 100}
	timers := fakeTimers(&opts)
	w := startWatch(t, dir, opts)
	debounce := (*timers)[0]

	if got := w.Seq(); got != 100 {
		t.Fatalf("Seq at start = %d, want the base 100", got)
	}

	writeFile(t, dir, "tracked.txt", "two\n")

	eventually(t, "Seq to rise", func() bool { return w.Seq() > 100 })

	select {
	case b := <-w.Batches():
		t.Fatalf("batch %+v arrived before the debounce", b)
	default:
	}

	debounce.fire()
	waitPaths(t, w, "tracked.txt")
}

type metaRow struct {
	name    string
	ev      fsnotify.Event
	wantGit bool
}

func runMetaRows(t *testing.T, w *Watcher, rows []metaRow) {
	t.Helper()

	for _, tt := range rows {
		t.Run(tt.name, func(t *testing.T) {
			p := newPending()
			before := w.Seq()

			got := w.handle(tt.ev, p)
			if got != tt.wantGit || p.git != tt.wantGit {
				t.Errorf("handle = %v, git = %v; want %v", got, p.git, tt.wantGit)
			}

			want := uint64(0)
			if tt.wantGit {
				want = 1
			}

			if rose := w.Seq() - before; rose != want {
				t.Errorf("Seq rose by %d, want %d", rose, want)
			}
		})
	}
}

func TestKqueueMetaOps(t *testing.T) {
	t.Parallel()
	w := eventWatcher(t)
	gitDir, refs := w.repo.GitDir, w.refsDir
	ref := filepath.Join(refs, "heads", "main")

	runMetaRows(t, w, []metaRow{
		{name: "create on a ref", ev: fsnotify.Event{Name: ref, Op: fsnotify.Create}, wantGit: true},
		{name: "rename on the index", ev: fsnotify.Event{Name: filepath.Join(gitDir, "index"), Op: fsnotify.Rename}, wantGit: true},
		{name: "remove on HEAD", ev: fsnotify.Event{Name: filepath.Join(gitDir, "HEAD"), Op: fsnotify.Remove}, wantGit: true},
		{name: "lock write", ev: fsnotify.Event{Name: ref + ".lock", Op: fsnotify.Write}},
		{name: "packed-refs write", ev: fsnotify.Event{Name: filepath.Join(gitDir, "packed-refs"), Op: fsnotify.Write}, wantGit: true},
		{name: "unrelated git file", ev: fsnotify.Event{Name: filepath.Join(gitDir, "config"), Op: fsnotify.Write}},
	})
}

func TestKqueueNewDirectoryUnderRefs(t *testing.T) {
	t.Parallel()
	w := eventWatcher(t)
	heads := filepath.Join(w.refsDir, "heads")

	mk := func(t *testing.T, dir string, files ...string) string {
		t.Helper()

		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}

		for _, f := range files {
			if err := os.WriteFile(filepath.Join(dir, f), []byte("x\n"), 0o644); err != nil {
				t.Fatal(err)
			}
		}

		return dir
	}

	rows := []metaRow{
		{name: "holding a ref", ev: fsnotify.Event{Name: mk(t, filepath.Join(heads, "feat"), "x"), Op: fsnotify.Create}, wantGit: true},
		{name: "holding a ref below", ev: fsnotify.Event{Name: mk(t, filepath.Join(heads, "a", "b"), "x"), Op: fsnotify.Create}, wantGit: true},
		{name: "empty", ev: fsnotify.Event{Name: mk(t, filepath.Join(heads, "empty")), Op: fsnotify.Create}},
		{name: "only a lock", ev: fsnotify.Event{Name: mk(t, filepath.Join(heads, "locked"), "x.lock"), Op: fsnotify.Create}},
		{name: "under refs/aim", ev: fsnotify.Event{Name: mk(t, filepath.Join(w.ownRefsDir, "sub"), "x"), Op: fsnotify.Create}},
	}

	runMetaRows(t, w, rows)
}

func TestKqueueLinkedWorktreeGitDir(t *testing.T) {
	t.Parallel()
	main := eventWatcher(t)
	common := main.repo.CommonDir
	linkedGit := filepath.Join(common, "worktrees", "x")

	if err := os.MkdirAll(linkedGit, 0o755); err != nil {
		t.Fatal(err)
	}

	linked := &Watcher{
		repo:       Repo{Root: t.TempDir(), GitDir: linkedGit, CommonDir: common},
		fs:         main.fs,
		refsDir:    main.refsDir,
		ownRefsDir: main.ownRefsDir,
		watched:    map[string]struct{}{linkedGit: {}},
	}

	runMetaRows(t, linked, []metaRow{
		{name: "linked HEAD", ev: fsnotify.Event{Name: filepath.Join(linkedGit, "HEAD"), Op: fsnotify.Write}, wantGit: true},
		{name: "linked index", ev: fsnotify.Event{Name: filepath.Join(linkedGit, "index"), Op: fsnotify.Write}, wantGit: true},
		{name: "main HEAD", ev: fsnotify.Event{Name: filepath.Join(main.repo.GitDir, "HEAD"), Op: fsnotify.Write}},
		{name: "linked config", ev: fsnotify.Event{Name: filepath.Join(linkedGit, "config"), Op: fsnotify.Write}},
	})
}

func TestWatchOverflowBumpsSeq(t *testing.T) {
	t.Parallel()

	injectErrs := make(chan error, 1)
	opts := fastWatch
	opts.injectErrs = injectErrs
	w := startWatch(t, baseRepo(t), opts)
	before := w.Seq()

	// The lost events never reach handle, so the overflow itself must count.
	injectErrs <- fsnotify.ErrEventOverflow

	b := collect(t, w, func(b Batch) bool { return b.Overflow })
	if !b.Overflow {
		t.Fatalf("batch = %+v", b)
	}

	if got := w.Seq(); got <= before {
		t.Fatalf("Seq = %d after an overflow, was %d", got, before)
	}
}

func TestWatchIgnoresVanishedPaths(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name         string
		errno        syscall.Errno
		wantReported bool
	}{
		{name: "a file that vanished is dropped", errno: syscall.ENOENT},
		{name: "any other filesystem error is reported", errno: syscall.EACCES, wantReported: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			dir := baseRepo(t)
			injectErrs := make(chan error, 1)
			opts := fastWatch
			opts.injectErrs = injectErrs
			w := startWatch(t, dir, opts)

			// A PathError shaped like the one fsnotify's kqueue backend sends; the
			// errno varies by case.
			injectErrs <- &fs.PathError{
				Op:   "lstat",
				Path: filepath.Join(dir, ".git", "refs", "heads", "x.lock"),
				Err:  tt.errno,
			}

			if tt.wantReported {
				select {
				case err := <-w.Errors():
					if !errors.Is(err, tt.errno) {
						t.Fatalf("error = %v, want one wrapping %v", err, tt.errno)
					}
				case <-time.After(watchWait):
					t.Fatal("the error never reached Errors")
				}

				if !w.Degraded() {
					t.Fatal("Degraded = false after a reported error")
				}

				return
			}

			// The watcher handles the injected error before the real change's batch
			// goes out, so an empty Errors afterwards is not vacuous.
			writeFile(t, dir, "real.txt", "x")
			waitPaths(t, w, "real.txt")

			select {
			case err := <-w.Errors():
				t.Fatalf("a vanished path was reported: %v", err)
			default:
			}

			if w.Degraded() {
				t.Fatal("Degraded = true after a vanished path")
			}
		})
	}
}
