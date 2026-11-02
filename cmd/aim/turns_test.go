package main

import (
	"bytes"
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"github.com/mhersson/aim/internal/core"
	"github.com/mhersson/aim/internal/git"
	"github.com/mhersson/aim/internal/harness"
)

const gateSession = "11111111-2222-4333-8444-555555555555"

// snapshotFunc is a fake snapshot or marker; funcWriter adapts a pair of them to
// the gate's turnWriter. A marker ignores unchanged.
type snapshotFunc func(ctx context.Context, dir, session string, unchanged bool) error

type funcWriter struct{ snap, mark snapshotFunc }

func (w funcWriter) Start(ctx context.Context, dir, session string, _, _ int, unchanged bool) error {
	return w.snap(ctx, dir, session, unchanged)
}

func (w funcWriter) Mark(ctx context.Context, dir, session string, _ int, _ bool) error {
	return w.mark(ctx, dir, session, false)
}

func (funcWriter) End(context.Context, string, string, int, bool) error { return nil }

func (funcWriter) OpenTurns(context.Context, string, string) ([]git.OpenTurn, error) {
	return nil, nil
}

// gateRig runs a gate over fake snapshot functions.
type gateRig struct {
	in   chan harness.Event
	out  chan harness.Event
	done chan struct{}
}

func noSeq(context.Context, string) (string, uint64, bool) { return "", 0, false }

func startGate(t *testing.T, snap, mark snapshotFunc) *gateRig {
	t.Helper()

	return startSeqGate(t, snap, mark, noSeq)
}

func startSeqGate(t *testing.T, snap, mark snapshotFunc, seq seqFunc) *gateRig {
	t.Helper()

	return startWriterGate(t, funcWriter{snap, mark}, seq)
}

func startWriterGate(t *testing.T, w turnWriter, seq seqFunc) *gateRig {
	t.Helper()

	return startPlacesGate(t, w, noPlaces, seq)
}

// noPlaces finds no edit worth a before-picture.
func noPlaces(context.Context, string) (git.Place, error) { return git.Place{}, git.ErrNotCounted }

func startPlacesGate(t *testing.T, w turnWriter, places placeFunc, seq seqFunc) *gateRig {
	t.Helper()

	r := &gateRig{in: make(chan harness.Event, 16), out: make(chan harness.Event, 16), done: make(chan struct{})}
	ctx, cancel := context.WithCancel(context.Background())

	go func() {
		defer close(r.done)

		runTurnGate(ctx, r.in, r.out, w, places, seq, discardLogger())
	}()

	t.Cleanup(func() {
		cancel()
		<-r.done
	})

	return r
}

// turnEvent returns a turn start whose Done closes the returned channel.
func turnEvent(name, session, cwd string) (harness.Event, <-chan struct{}) {
	released := make(chan struct{})
	ev := harness.Event{
		Name: name, Session: core.SessionID(session), Cwd: cwd, TurnStart: true,
		Done: sync.OnceFunc(func() { close(released) }),
	}

	return ev, released
}

func within(t *testing.T, ch <-chan struct{}, d time.Duration, what string) {
	t.Helper()

	select {
	case <-ch:
	case <-time.After(d):
		t.Fatalf("%s did not happen within %v", what, d)
	}
}

// notYet fails if ch is already closed; the caller holds what would close it.
func notYet(t *testing.T, ch <-chan struct{}, what string) {
	t.Helper()

	select {
	case <-ch:
		t.Fatalf("%s happened too early", what)
	default:
	}
}

func TestGateForwardsEventsInOrderWithoutWaiting(t *testing.T) {
	release := make(chan struct{})
	snap := func(ctx context.Context, _, _ string, _ bool) error {
		select {
		case <-release:
		case <-ctx.Done():
		}

		return nil
	}

	r := startGate(t, snap, func(context.Context, string, string, bool) error { return nil })
	turn, released := turnEvent("UserPromptSubmit", gateSession, "/repo")

	r.in <- turn

	r.in <- harness.Event{Name: "PreToolUse"}

	r.in <- harness.Event{Name: "Stop"}

	for _, want := range []string{"UserPromptSubmit", "PreToolUse", "Stop"} {
		select {
		case ev := <-r.out:
			if ev.Name != want {
				t.Fatalf("event = %q, want %q", ev.Name, want)
			}
		case <-time.After(time.Second):
			t.Fatalf("event %q was held behind the snapshot", want)
		}
	}

	notYet(t, released, "Done before the snapshot returned")
	close(release)
	within(t, released, time.Second, "Done after the snapshot")
}

// forwarded reads the next event the gate forwards.
func forwarded(t *testing.T, r *gateRig) harness.Event {
	t.Helper()

	select {
	case ev := <-r.out:
		return ev
	case <-time.After(time.Second):
		t.Fatal("the gate forwarded nothing")

		return harness.Event{}
	}
}

// A forwarded turn start carries a channel that closes when its snapshot is
// done, so the app can wait for the turn to exist before it counts turns.
func TestGateSnappedClosesWhenTheSnapshotIsDone(t *testing.T) {
	release := make(chan struct{})
	snap := func(ctx context.Context, _, _ string, _ bool) error {
		select {
		case <-release:
		case <-ctx.Done():
		}

		return nil
	}

	r := startGate(t, snap, func(context.Context, string, string, bool) error { return nil })
	turn, _ := turnEvent("UserPromptSubmit", gateSession, "/repo")

	r.in <- turn

	ev := forwarded(t, r)
	if ev.Snapped == nil {
		t.Fatal("the forwarded turn start has no Snapped channel")
	}

	notYet(t, ev.Snapped, "Snapped before the snapshot returned")
	close(release)
	within(t, ev.Snapped, time.Second, "Snapped after the snapshot")
}

// A turn whose snapshot failed gets a marker that counts as a turn, so Snapped
// closes after the marker is written, not before.
func TestGateSnappedWaitsForTheMarkerOfAFailedSnapshot(t *testing.T) {
	releaseMark := make(chan struct{})
	snap := func(context.Context, string, string, bool) error { return errors.New("no tree") }
	mark := func(ctx context.Context, _, _ string, _ bool) error {
		select {
		case <-releaseMark:
		case <-ctx.Done():
		}

		return nil
	}

	r := startGate(t, snap, mark)
	turn, _ := turnEvent("UserPromptSubmit", gateSession, "/repo")

	r.in <- turn

	ev := forwarded(t, r)
	if ev.Snapped == nil {
		t.Fatal("the forwarded turn start has no Snapped channel")
	}

	notYet(t, ev.Snapped, "Snapped before the marker was written")
	close(releaseMark)
	within(t, ev.Snapped, time.Second, "Snapped after the marker")
}

// startUnreadGate runs a gate whose out channel is unbuffered and never read.
func startUnreadGate(t *testing.T, snap snapshotFunc) chan<- harness.Event {
	t.Helper()

	in := make(chan harness.Event, 4)
	done := make(chan struct{})
	ctx, cancel := context.WithCancel(context.Background())

	go func() {
		defer close(done)

		runTurnGate(ctx, in, make(chan harness.Event), funcWriter{snap, func(context.Context, string, string, bool) error { return nil }}, noPlaces, noSeq, discardLogger())
	}()

	t.Cleanup(func() {
		cancel()
		<-done
	})

	return in
}

func TestGateUsesTheEventDeadline(t *testing.T) {
	got := make(chan time.Time, 1)
	snap := func(ctx context.Context, _, _ string, _ bool) error {
		dl, _ := ctx.Deadline()
		got <- dl

		return nil
	}

	r := startGate(t, snap, func(context.Context, string, string, bool) error { return nil })
	turn, _ := turnEvent("UserPromptSubmit", gateSession, "/repo")
	turn.Deadline = time.Now().Add(300 * time.Millisecond)

	r.in <- turn

	select {
	case dl := <-got:
		if !dl.Equal(turn.Deadline) {
			t.Errorf("snapshot deadline = %v, want the event's %v", dl, turn.Deadline)
		}
	case <-time.After(time.Second):
		t.Fatal("no snapshot")
	}
}

func TestGateWithoutDeadlineUsesArrival(t *testing.T) {
	got := make(chan time.Time, 1)
	snap := func(ctx context.Context, _, _ string, _ bool) error {
		dl, _ := ctx.Deadline()
		got <- dl

		return nil
	}

	r := startGate(t, snap, func(context.Context, string, string, bool) error { return nil })
	turn, _ := turnEvent("UserPromptSubmit", gateSession, "/repo")

	before := time.Now()

	r.in <- turn

	select {
	case dl := <-got:
		if dl.Before(before.Add(turnBudget)) || dl.After(time.Now().Add(turnBudget)) {
			t.Errorf("snapshot deadline = %v, want arrival + %v", dl, turnBudget)
		}
	case <-time.After(time.Second):
		t.Fatal("no snapshot")
	}
}

func TestGateSnapshotsBeforeTheAppReadsTheEvent(t *testing.T) {
	snapped := make(chan struct{})
	snap := func(context.Context, string, string, bool) error {
		close(snapped)

		return nil
	}

	in := startUnreadGate(t, snap)
	turn, released := turnEvent("UserPromptSubmit", gateSession, "/repo")

	in <- turn

	within(t, snapped, time.Second, "the snapshot with the app channel blocked")
	within(t, released, time.Second, "the hold release with the app channel blocked")
}

func TestGateSnapshotsOfOneSessionStayInArrivalOrder(t *testing.T) {
	var (
		mu       sync.Mutex
		order    []string
		inFlight atomic.Int32
		maxSeen  atomic.Int32
	)

	snap := func(_ context.Context, cwd, _ string, _ bool) error {
		n := inFlight.Add(1)

		for {
			m := maxSeen.Load()
			if n <= m || maxSeen.CompareAndSwap(m, n) {
				break
			}
		}

		time.Sleep(5 * time.Millisecond)

		mu.Lock()

		order = append(order, cwd)

		mu.Unlock()
		inFlight.Add(-1)

		return nil
	}

	r := startGate(t, snap, func(context.Context, string, string, bool) error { return nil })

	var releases []<-chan struct{}

	want := make([]string, 0, 3)

	for i := range 3 {
		cwd := "/repo" + string(rune('A'+i%26)) + string(rune('a'+i/26))
		turn, released := turnEvent("UserPromptSubmit", gateSession, cwd)
		want = append(want, cwd)
		releases = append(releases, released)

		r.in <- turn
	}

	for _, released := range releases {
		within(t, released, 2*time.Second, "a snapshot")
	}

	mu.Lock()
	defer mu.Unlock()

	if strings.Join(order, ",") != strings.Join(want, ",") {
		t.Errorf("snapshot order = %v, want %v", order, want)
	}

	if m := maxSeen.Load(); m != 1 {
		t.Errorf("snapshots in flight at once = %d, want 1", m)
	}
}

func TestGateSlowSnapshotIsCancelledAtTheBudget(t *testing.T) {
	t.Parallel()

	var (
		order      []string
		mu         sync.Mutex
		snapErr    error
		snapCancel = make(chan struct{})
	)

	record := func(s string) {
		mu.Lock()

		order = append(order, s)
		mu.Unlock()
	}

	snap := func(ctx context.Context, _, _ string, _ bool) error {
		<-ctx.Done()
		snapErr = ctx.Err()

		close(snapCancel)
		record("snap-cancelled")

		return ctx.Err()
	}
	marked := make(chan struct{})
	mark := func(context.Context, string, string, bool) error {
		record("mark")
		close(marked)

		return nil
	}

	r := startGate(t, snap, mark)
	turn, released := turnEvent("UserPromptSubmit", gateSession, "/repo")
	turn.Deadline = time.Now().Add(300 * time.Millisecond)

	r.in <- turn

	// The snapshot only ends on its context, so Done comes at the budget.
	within(t, released, hangGuard, "Done at the budget")

	within(t, marked, time.Second, "marker")
	within(t, snapCancel, time.Second, "snapshot cancellation")

	if !errors.Is(snapErr, context.DeadlineExceeded) {
		t.Errorf("snapshot context error = %v, want deadline exceeded", snapErr)
	}

	mu.Lock()
	defer mu.Unlock()

	if len(order) != 2 || order[0] != "snap-cancelled" || order[1] != "mark" {
		t.Errorf("order = %v, want snapshot cancelled then marker", order)
	}
}

func TestGateBudgetSpentWaitingForTheLockSkipsTheSnapshot(t *testing.T) {
	t.Parallel()

	var snaps, marks atomic.Int32

	snap := func(ctx context.Context, _, _ string, _ bool) error {
		snaps.Add(1)
		<-ctx.Done()

		return ctx.Err()
	}
	// The first marker holds the session's lock until the test lets it go.
	markHold := make(chan struct{})
	bothMarked := make(chan struct{})
	mark := func(ctx context.Context, _, _ string, _ bool) error {
		select {
		case <-markHold:
		case <-ctx.Done():
		}

		if marks.Add(1) == 2 {
			close(bothMarked)
		}

		return nil
	}

	r := startGate(t, snap, mark)
	a, relA := turnEvent("UserPromptSubmit", gateSession, "/repo")
	b, relB := turnEvent("UserPromptSubmit", gateSession, "/repo")
	a.Deadline = time.Now().Add(50 * time.Millisecond)
	b.Deadline = a.Deadline

	r.in <- a

	r.in <- b

	within(t, relA, hangGuard, "first Done")
	within(t, relB, hangGuard, "second Done")
	close(markHold)
	within(t, bothMarked, hangGuard, "both markers")

	if snaps.Load() != 1 || marks.Load() != 2 {
		t.Errorf("snapshots = %d, markers = %d, want 1 and 2", snaps.Load(), marks.Load())
	}
}

func TestGateSkipsEventsItCannotSnapshot(t *testing.T) {
	var calls atomic.Int32

	fn := func(context.Context, string, string, bool) error {
		calls.Add(1)

		return nil
	}
	r := startGate(t, fn, fn)

	for _, ev := range []struct{ session, cwd string }{
		{gateSession, ""},
		{"not-a-uuid", "/repo"},
		{"", "/repo"},
	} {
		turn, released := turnEvent("UserPromptSubmit", ev.session, ev.cwd)
		r.in <- turn

		within(t, released, time.Second, "Done for "+ev.session+"|"+ev.cwd)

		// Only a turn that gets a snapshot carries Snapped.
		if got := forwarded(t, r); got.Snapped != nil {
			t.Errorf("%s (session %q, cwd %q) carries Snapped, want nil", got.Name, got.Session, got.Cwd)
		}
	}

	if calls.Load() != 0 {
		t.Errorf("snapshot functions ran %d times, want none", calls.Load())
	}
}

func TestGateNoMarkerOutsideARepository(t *testing.T) {
	var marks atomic.Int32

	for _, sentinel := range []error{git.ErrNotRepo, git.ErrBadSession} {
		snap := func(context.Context, string, string, bool) error { return sentinel }
		mark := func(context.Context, string, string, bool) error {
			marks.Add(1)

			return nil
		}
		r := startGate(t, snap, mark)

		turn, released := turnEvent("UserPromptSubmit", gateSession, "/repo")
		r.in <- turn

		within(t, released, time.Second, "Done")
		// Closing the input ends the gate after its snapshot goroutines.
		close(r.in)
		within(t, r.done, time.Second, "gate return")
	}

	if marks.Load() != 0 {
		t.Errorf("markers = %d, want none", marks.Load())
	}
}

func TestGateCancelWaitsForSnapshotsAndWritesNoMarker(t *testing.T) {
	var (
		marks  atomic.Int32
		ended  atomic.Bool
		inSnap = make(chan struct{})
	)

	snap := func(ctx context.Context, _, _ string, _ bool) error {
		close(inSnap)
		<-ctx.Done()
		time.Sleep(100 * time.Millisecond)
		ended.Store(true)

		return ctx.Err()
	}
	mark := func(context.Context, string, string, bool) error {
		marks.Add(1)

		return nil
	}

	in := make(chan harness.Event, 1)
	out := make(chan harness.Event, 1)
	ctx, cancel := context.WithCancel(context.Background())
	returned := make(chan struct{})

	go func() {
		defer close(returned)

		runTurnGate(ctx, in, out, funcWriter{snap, mark}, noPlaces, noSeq, discardLogger())
	}()

	turn, _ := turnEvent("UserPromptSubmit", gateSession, "/repo")
	in <- turn

	within(t, inSnap, time.Second, "snapshot start")
	cancel()
	within(t, returned, 2*time.Second, "gate return")

	if !ended.Load() {
		t.Error("runTurnGate returned before its snapshot goroutine ended")
	}

	if marks.Load() != 0 {
		t.Errorf("markers = %d after cancel, want none", marks.Load())
	}
}

// --- real git ---

func isolateGit(t *testing.T) {
	t.Helper()

	// Detached auto-maintenance would race the removal of a test's repository.
	cfg := filepath.Join(t.TempDir(), "gitconfig")
	if err := os.WriteFile(cfg, []byte("[maintenance]\n\tauto = false\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	t.Setenv("GIT_CONFIG_GLOBAL", cfg)
	t.Setenv("GIT_AUTHOR_NAME", "Test")
	t.Setenv("GIT_AUTHOR_EMAIL", "test@example.com")
	t.Setenv("GIT_COMMITTER_NAME", "Test")
	t.Setenv("GIT_COMMITTER_EMAIL", "test@example.com")
	t.Setenv("HOME", shortDir(t))
	t.Setenv("XDG_STATE_HOME", shortDir(t))
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
}

// No git a test runs under isolateGit may start auto-maintenance: its detached
// run outlives the git that started it and races the removal of the repository.
// It needs real git: it reads the trace of the commands git itself ran.
func TestIsolateGitStartsNoAutoMaintenance(t *testing.T) {
	dir := seededRepo(t)
	trace := filepath.Join(t.TempDir(), "trace")
	t.Setenv("GIT_TRACE2_EVENT", trace)

	gitOut(t, dir, "commit", "-q", "--allow-empty", "-m", "two")

	if names := gitCommands(t, trace); !slices.Contains(names, "commit") || slices.Contains(names, "maintenance") {
		t.Errorf("ran %v, want commit and no maintenance", names)
	}
}

// gitCommands returns the name of every git command the trace2 event file at
// path records, children included.
func gitCommands(t *testing.T, path string) []string {
	t.Helper()

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}

	var names []string

	for line := range bytes.Lines(data) {
		var ev struct {
			Event string `json:"event"`
			Name  string `json:"name"`
		}

		if err := json.Unmarshal(line, &ev); err != nil {
			t.Fatalf("trace line %q: %v", line, err)
		}

		if ev.Event == "cmd_name" {
			names = append(names, ev.Name)
		}
	}

	return names
}

// seededRepo returns a repository with one commit and an untracked file, so a
// snapshot has something to stage.
func seededRepo(t *testing.T) string {
	t.Helper()
	isolateGit(t)

	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}

	gitOut(t, dir, "init", "-q", "-b", "main")

	if err := os.WriteFile(filepath.Join(dir, "a.txt"), []byte("a\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	gitOut(t, dir, "add", "-A")
	gitOut(t, dir, "commit", "-q", "-m", "init")

	if err := os.WriteFile(filepath.Join(dir, "new.txt"), []byte("new\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	return dir
}

func gitOut(t *testing.T, dir string, args ...string) string {
	t.Helper()

	cmd := exec.Command("git", args...)
	cmd.Dir = dir

	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
	}

	return strings.TrimSpace(string(out))
}

// writeExecutable writes a script. A concurrent fork can inherit the write fd
// and make exec fail with ETXTBSY (go.dev/issue/22315), so the write holds the
// fork lock.
func writeExecutable(t *testing.T, path, script string) {
	t.Helper()

	syscall.ForkLock.RLock()

	err := os.WriteFile(path, []byte(script), 0o755)

	syscall.ForkLock.RUnlock()

	if err != nil {
		t.Fatal(err)
	}
}

// blockedStaging puts a git wrapper first on PATH whose staging (update-index)
// blocks until the release file exists, which t.Cleanup creates so no wrapper
// outlives the test; other calls run the real git. A caller that waited for
// git would hang instead of racing a timer.
func blockedStaging(t *testing.T) (started string) {
	t.Helper()

	gitPath, err := exec.LookPath("git")
	if err != nil {
		t.Skip("git not found")
	}

	dir := t.TempDir()
	started = filepath.Join(dir, "started")
	release := filepath.Join(dir, "release")
	script := "#!/bin/sh\ncase \"$*\" in *update-index*) : > '" + started + "'; while [ ! -e '" + release + "' ]; do sleep 0.05; done;; esac\nexec '" + gitPath + "' \"$@\"\n"

	writeExecutable(t, filepath.Join(dir, "git"), script)

	t.Cleanup(func() { _ = os.WriteFile(release, nil, 0o600) })
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))

	return started
}

func gitTurns(t *testing.T, dir string) []git.Turn {
	t.Helper()

	r, err := git.Open(t.Context(), dir)
	if err != nil {
		t.Fatal(err)
	}

	turns, err := git.Turns(t.Context(), r, gateSession)
	if err != nil {
		t.Fatal(err)
	}

	return turns
}

func sendTurn(t *testing.T, sock, cwd, session string) {
	t.Helper()

	ctx, cancel := context.WithTimeout(t.Context(), 4*time.Second)
	defer cancel()

	ev := harness.Event{Name: "UserPromptSubmit", Session: core.SessionID(session), Cwd: cwd, TurnStart: true, Pane: "p"}

	if err := harness.Send(ctx, sock, ev); err != nil {
		t.Fatalf("Send() error = %v", err)
	}
}

// settles waits for the goroutine count to fall back to at most base.
func settles(t *testing.T, base int) {
	t.Helper()

	deadline := time.Now().Add(3 * time.Second)
	for runtime.NumGoroutine() > base && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
	}

	if n := runtime.NumGoroutine(); n > base {
		buf := make([]byte, 1<<16)
		t.Errorf("goroutines = %d, want at most %d\n%s", n, base, buf[:runtime.Stack(buf, true)])
	}
}

// It needs real git: it is the one check that gitTurnWriter maps onto
// internal/git and that the place cache resolves real worktrees.
func TestHooksRecordTurnsWithGit(t *testing.T) {
	repo := seededRepo(t)
	wt := linkedWorktree(t, repo)
	base := runtime.NumGoroutine()

	// Long enough that a loaded machine cannot turn a snapshot into a marker.
	h, err := setupHooks(discardLogger(), shortDir(t), "", testWatches(t), testPlaces(), false,
		hookTimes{hold: 10 * time.Second, budget: 9 * time.Second})
	if err != nil {
		t.Fatal(err)
	}

	t.Cleanup(h.Stop)

	// The answer comes after the snapshot exists.
	sendTurn(t, h.Sock, repo, gateSession)

	turns := gitTurns(t, repo)
	if len(turns) != 1 || turns[0].Missing {
		t.Fatalf("turns = %+v, want one snapshot by the time the hook was answered", turns)
	}

	select {
	case ev := <-h.Events:
		if ev.Name != "UserPromptSubmit" || !ev.TurnStart {
			t.Errorf("event = %+v", ev)
		}
	case <-time.After(time.Second):
		t.Fatal("the app never got the event")
	}

	// A first edit in another worktree is held until that worktree's start exists.
	if err := sendEdit(t.Context(), h.Sock, repo, filepath.Join(wt, "new.go")); err != nil {
		t.Fatalf("Send(edit) error = %v", err)
	}

	wtTurns, own := gitTurns(t, wt), gitTurns(t, repo)
	if len(wtTurns) != 1 || wtTurns[0].Missing || len(own) != 1 || wtTurns[0].N != own[0].N {
		t.Fatalf("turns = %+v, own = %+v, want the worktree's start at the turn's number", wtTurns, own)
	}

	// A turn ends at the pane's next prompt, so it keeps what changed after Stop.
	if err := os.WriteFile(filepath.Join(repo, "during.txt"), []byte("during\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	stop := harness.Event{Name: "Stop", Session: gateSession, Cwd: repo, Pane: "p"}
	if err := harness.Send(t.Context(), h.Sock, stop); err != nil {
		t.Fatalf("Send(Stop) error = %v", err)
	}

	if turns := gitTurns(t, repo); len(turns) != 1 || turns[0].EndCommit != "" {
		t.Fatalf("turns after Stop = %+v, want one open turn", turns)
	}

	if err := os.WriteFile(filepath.Join(repo, "after.txt"), []byte("after\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	sendTurn(t, h.Sock, repo, gateSession)

	turns = gitTurns(t, repo)
	if len(turns) != 2 || turns[0].EndCommit == "" || turns[0].EndMissing || turns[1].EndCommit != "" {
		t.Fatalf("turns = %+v, want the first ended and the second open", turns)
	}

	for _, name := range []string{"during.txt", "after.txt"} {
		if out := gitOut(t, repo, "ls-tree", "--name-only", turns[0].EndTree, name); out != name {
			t.Errorf("first turn's end tree lacks %s", name)
		}
	}

	h.Stop()
	settles(t, base)
}

func TestDefaultHookTimes(t *testing.T) {
	t.Parallel()

	if got, want := defaultHookTimes(), (hookTimes{hold: 1500 * time.Millisecond, budget: 1400 * time.Millisecond}); got != want {
		t.Errorf("defaultHookTimes() = %+v, want %+v", got, want)
	}

	if turnBudget >= turnHold || turnHold >= hookBudget {
		t.Errorf("turnBudget %v, turnHold %v, hookBudget %v, want them in increasing order", turnBudget, turnHold, hookBudget)
	}
}

// It needs real git: the wrapper on PATH blocks a real staging call.
func TestHookBudgetSlowSnapshot(t *testing.T) {
	repo := seededRepo(t)
	_ = blockedStaging(t)

	base := runtime.NumGoroutine()

	h, err := setupHooks(discardLogger(), shortDir(t), "", testWatches(t), testPlaces(), false,
		hookTimes{hold: 200 * time.Millisecond, budget: 50 * time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}

	// Staging never finishes, so an answer means the hook did not wait for it.
	sendTurn(t, h.Sock, repo, gateSession)

	deadline := time.Now().Add(5 * time.Second)

	var turns []git.Turn

	for time.Now().Before(deadline) {
		if turns = gitTurns(t, repo); len(turns) > 0 {
			break
		}

		time.Sleep(20 * time.Millisecond)
	}

	if len(turns) != 1 || !turns[0].Missing || turns[0].N == 0 {
		t.Fatalf("turns = %+v, want only a marker for the turn", turns)
	}

	if out := gitOut(t, repo, "for-each-ref", "refs/aim/"); strings.Count(out, "\n") != 0 {
		t.Errorf("refs = %q, want exactly the marker", out)
	}

	h.Stop()
	settles(t, base)
}

// It needs real git: the wrapper on PATH blocks a real staging call.
func TestHookBudgetServerClosingMidSnapshot(t *testing.T) {
	repo := seededRepo(t)
	started := blockedStaging(t)

	base := runtime.NumGoroutine()

	h, err := setupHooks(discardLogger(), shortDir(t), "", testWatches(t), testPlaces(), false, defaultHookTimes())
	if err != nil {
		t.Fatal(err)
	}

	sent := make(chan error, 1)

	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
		defer cancel()

		sent <- harness.Send(ctx, h.Sock, harness.Event{
			Name: "UserPromptSubmit", Session: gateSession, Cwd: repo, TurnStart: true, Pane: "p",
		})
	}()

	select {
	case <-h.Events:
	case <-time.After(2 * time.Second):
		t.Fatal("event never arrived")
	}

	for deadline := time.Now().Add(3 * time.Second); !exists(started) && time.Now().Before(deadline); {
		time.Sleep(10 * time.Millisecond)
	}

	if !exists(started) {
		t.Fatal("the snapshot never reached staging")
	}

	// The snapshot's git stays blocked until cleanup, so Stop returning at
	// all means it cancelled the snapshot instead of waiting for it.
	returns(t, "Stop", h.Stop)

	if err := <-sent; err == nil {
		t.Error("Send() succeeded though the server closed mid-snapshot")
	}

	if turns := gitTurns(t, repo); len(turns) != 0 {
		t.Errorf("turns = %+v after Stop, want none (no ref, no marker)", turns)
	}

	settles(t, base)
}

// seqRig is a gate whose snapshots record their unchanged flag and whose
// watcher counter the test controls.
type seqRig struct {
	*gateRig

	n         atomic.Uint64
	watched   atomic.Bool
	flags     chan bool
	beforeSnp func()
}

func newSeqRig(t *testing.T) *seqRig {
	t.Helper()

	s := &seqRig{flags: make(chan bool, 16)}
	s.watched.Store(true)

	snap := func(_ context.Context, _, _ string, unchanged bool) error {
		if s.beforeSnp != nil {
			s.beforeSnp()
		}

		s.flags <- unchanged

		return nil
	}
	seq := func(context.Context, string) (string, uint64, bool) {
		return "/root", s.n.Load(), s.watched.Load()
	}
	s.gateRig = startSeqGate(t, snap, func(context.Context, string, string, bool) error { return nil }, seq)

	return s
}

// turn sends a turn start and returns the unchanged flag its snapshot saw.
func (s *seqRig) turn(t *testing.T) bool {
	t.Helper()

	ev, released := turnEvent("UserPromptSubmit", gateSession, "/repo")
	s.in <- ev

	within(t, released, time.Second, "turn release")

	select {
	case f := <-s.flags:
		return f
	case <-time.After(time.Second):
		t.Fatal("no snapshot")
	}

	return false
}

func TestGateSkipsAnUnchangedTurn(t *testing.T) {
	s := newSeqRig(t)

	if s.turn(t) {
		t.Fatal("first turn skipped")
	}

	if !s.turn(t) {
		t.Fatal("second turn with an unchanged counter was not skipped")
	}

	if !s.turn(t) {
		t.Fatal("third turn was not skipped")
	}
}

func TestGateDoesNotSkipAfterAChange(t *testing.T) {
	s := newSeqRig(t)

	s.turn(t)
	s.n.Add(1)

	if s.turn(t) {
		t.Fatal("turn after a change was skipped")
	}

	if !s.turn(t) {
		t.Fatal("turn after the changed one was not skipped")
	}
}

func TestGateDoesNotSkipWithoutAWatcher(t *testing.T) {
	s := newSeqRig(t)

	s.turn(t)
	s.watched.Store(false)

	if s.turn(t) {
		t.Fatal("turn without a watcher was skipped")
	}

	s.watched.Store(true)

	if s.turn(t) {
		t.Fatal("turn right after an unwatched one was skipped")
	}
}

func TestGateDoesNotSkipAfterAChangeDuringTheSnapshot(t *testing.T) {
	s := newSeqRig(t)

	s.beforeSnp = func() { s.n.Add(1) } // after the counter was read, before the snapshot ends
	s.turn(t)

	s.beforeSnp = nil

	if s.turn(t) {
		t.Fatal("turn after a change during the previous snapshot was skipped")
	}
}

func TestGateDoesNotSkipAfterAFailedSnapshot(t *testing.T) {
	var fail atomic.Bool

	flags := make(chan bool, 4)
	snap := func(_ context.Context, _, _ string, unchanged bool) error {
		flags <- unchanged

		if fail.Load() {
			return errors.New("boom")
		}

		return nil
	}
	seq := func(context.Context, string) (string, uint64, bool) { return "/root", 1, true }
	r := startSeqGate(t, snap, func(context.Context, string, string, bool) error { return nil }, seq)

	send := func() bool {
		ev, released := turnEvent("UserPromptSubmit", gateSession, "/repo")
		r.in <- ev

		within(t, released, time.Second, "turn release")

		return <-flags
	}

	send()
	fail.Store(true)
	send()
	fail.Store(false)

	if send() {
		t.Fatal("turn after a failed snapshot was skipped")
	}
}

func TestGateKeepsSeqPerSession(t *testing.T) {
	s := newSeqRig(t)
	other := "99999999-2222-4333-8444-555555555555"

	s.turn(t)

	ev, released := turnEvent("UserPromptSubmit", other, "/repo")
	s.in <- ev

	within(t, released, time.Second, "turn release")

	if <-s.flags {
		t.Fatal("another session's first turn was skipped")
	}
}

func TestNextTurnID(t *testing.T) {
	t.Parallel()

	at := time.UnixMilli(1_700_000_000_500)

	tests := []struct {
		name string
		prev int
		want int
	}{
		{"a later time", 1_700_000_000_000, 1_700_000_000_500},
		{"the same millisecond", 1_700_000_000_500, 1_700_000_000_501},
		{"a clock set back", 1_700_000_009_000, 1_700_000_009_001},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if got := nextTurnID(tt.prev, at); got != tt.want {
				t.Errorf("nextTurnID(%d) = %d, want %d", tt.prev, got, tt.want)
			}
		})
	}
}

// recordedStart is one call of a recording writer, or with a pane, dir and
// session one turn start to send.
type recordedStart struct {
	pane       core.PaneID
	dir        string
	session    string
	turn, ends int
	mark, end  bool
	unchanged  bool
}

// recordingWriter records what the gate writes, in the order the calls finish
// (a start is recorded when it begins). failing starts return an error.
type recordingWriter struct {
	mu    sync.Mutex
	calls []recordedStart
	fail  func(dir string) bool
	// onStart runs inside Start, after it is recorded; its error fails the start.
	onStart func(ctx context.Context, dir string) error
	// failEnd makes the End of the root fail with the error.
	failEnd func(root string) error
	// open is what OpenTurns reports.
	open []git.OpenTurn
	// markHold, when set, makes Mark announce itself on markStarted and wait.
	markHold    chan struct{}
	markStarted chan recordedStart
}

func (w *recordingWriter) record(c recordedStart) {
	w.mu.Lock()
	defer w.mu.Unlock()

	w.calls = append(w.calls, c)
}

func (w *recordingWriter) Start(ctx context.Context, dir, session string, turn, ends int, unchanged bool) error {
	w.record(recordedStart{dir: dir, session: session, turn: turn, ends: ends, unchanged: unchanged})

	if w.onStart != nil {
		if err := w.onStart(ctx, dir); err != nil {
			return err
		}
	}

	if w.fail != nil && w.fail(dir) {
		return errors.New("boom")
	}

	return nil
}

func (w *recordingWriter) End(_ context.Context, root, session string, turn int, unchanged bool) error {
	w.record(recordedStart{dir: root, session: session, turn: turn, end: true, unchanged: unchanged})

	if w.failEnd != nil {
		return w.failEnd(root)
	}

	return nil
}

func (w *recordingWriter) Mark(ctx context.Context, dir, session string, turn int, end bool) error {
	c := recordedStart{dir: dir, session: session, turn: turn, mark: true, end: end}

	if w.markHold != nil {
		w.markStarted <- c

		select {
		case <-w.markHold:
		case <-ctx.Done():
			return ctx.Err()
		}
	}

	w.record(c)

	return nil
}

func (w *recordingWriter) OpenTurns(context.Context, string, string) ([]git.OpenTurn, error) {
	return w.open, nil
}

func (w *recordingWriter) filter(keep func(recordedStart) bool) []recordedStart {
	w.mu.Lock()
	defer w.mu.Unlock()

	var out []recordedStart

	for _, c := range w.calls {
		if keep(c) {
			out = append(out, c)
		}
	}

	return out
}

func (w *recordingWriter) starts() []recordedStart {
	return w.filter(func(c recordedStart) bool { return !c.mark && !c.end })
}

func (w *recordingWriter) endings() []recordedStart {
	return w.filter(func(c recordedStart) bool { return c.end && !c.mark })
}

func (w *recordingWriter) all() []recordedStart {
	return w.filter(func(recordedStart) bool { return true })
}

// dirSeq resolves a directory to the worktree root named like it.
func dirSeq(_ context.Context, dir string) (string, uint64, bool) { return "root" + dir, 1, true }

// sendTurns sends one turn start per (pane, cwd) pair and waits for each to be
// answered, so the starts run in order.
func sendTurns(t *testing.T, w turnWriter, seq seqFunc, sends ...recordedStart) {
	t.Helper()

	in, out := make(chan harness.Event, 16), make(chan harness.Event, 16)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})

	go func() {
		defer close(done)

		runTurnGate(ctx, in, out, w, noPlaces, seq, discardLogger())
	}()

	t.Cleanup(func() {
		cancel()
		<-done
	})

	for _, s := range sends {
		session := cmp.Or(s.session, gateSession)
		ev, released := turnEvent("UserPromptSubmit", session, s.dir)

		ev.Pane = s.pane
		in <- ev

		within(t, released, time.Second, "turn release")
	}
}

func TestGateEndsThePreviousTurnInTheSameWorktree(t *testing.T) {
	t.Parallel()

	w := &recordingWriter{}

	sendTurns(t, w, dirSeq, recordedStart{dir: "/a"}, recordedStart{dir: "/a"}, recordedStart{dir: "/a"})

	got := w.starts()
	if len(got) != 3 {
		t.Fatalf("starts = %+v, want 3", got)
	}

	if got[0].ends != 0 || got[1].ends != got[0].turn || got[2].ends != got[1].turn {
		t.Errorf("ends = %d, %d, %d, want 0 then each previous number %d, %d", got[0].ends, got[1].ends, got[2].ends, got[0].turn, got[1].turn)
	}

	if got[0].turn <= 0 || got[1].turn <= got[0].turn || got[2].turn <= got[1].turn {
		t.Errorf("numbers %d, %d, %d do not increase", got[0].turn, got[1].turn, got[2].turn)
	}

	if e := w.endings(); len(e) != 0 {
		t.Errorf("End calls = %+v, want none for the own root", e)
	}
}

func TestGateEndsNothingAcrossWorktrees(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		sends []recordedStart
		fail  func(dir string) bool
	}{
		{"after a failed start", []recordedStart{{dir: "/a"}, {dir: "/a"}}, func(string) bool { return true }},
		{"another pane", []recordedStart{{dir: "/a", pane: "p1"}, {dir: "/a", pane: "p2"}}, nil},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			w := &recordingWriter{fail: tt.fail}

			sendTurns(t, w, dirSeq, tt.sends...)

			got := w.starts()
			if len(got) != 2 {
				t.Fatalf("starts = %+v, want 2", got)
			}

			if got[0].ends != 0 || got[1].ends != 0 {
				t.Errorf("ends = %d, %d, want 0 and 0", got[0].ends, got[1].ends)
			}
		})
	}
}

func TestGateEndsTheOldRootWhenTheOwnDirectoryMoved(t *testing.T) {
	t.Parallel()

	w := &recordingWriter{}

	sendTurns(t, w, dirSeq, recordedStart{dir: "/a"}, recordedStart{dir: "/b"})

	calls := w.all()
	if len(calls) != 3 {
		t.Fatalf("calls = %+v, want start, end, start", calls)
	}

	first, end, second := calls[0], calls[1], calls[2]

	if !end.end || end.dir != "root/a" || end.turn != first.turn {
		t.Errorf("second call = %+v, want End of root/a at turn %d", end, first.turn)
	}

	if second.end || second.dir != "/b" || second.ends != 0 {
		t.Errorf("third call = %+v, want the start in /b with ends 0, after the End", second)
	}
}

func TestGateStopEndsNothing(t *testing.T) {
	t.Parallel()

	w := &recordingWriter{}
	r := startWriterGate(t, w, dirSeq)

	first, released := turnEvent("UserPromptSubmit", gateSession, "/a")
	r.in <- first

	within(t, released, time.Second, "first turn release")
	forwarded(t, r)

	for _, name := range []string{"Stop", "StopFailure", "SessionEnd"} {
		r.in <- harness.Event{Name: name, Session: gateSession, Cwd: "/a"}

		if ev := forwarded(t, r); ev.Name != name || ev.Snapped != nil {
			t.Errorf("%s forwarded as %+v, want no Snapped", name, ev)
		}
	}

	if got := w.all(); len(got) != 1 {
		t.Fatalf("calls after Stop, StopFailure and SessionEnd = %+v, want only the first start", got)
	}

	second, released := turnEvent("UserPromptSubmit", gateSession, "/a")
	r.in <- second

	within(t, released, time.Second, "second turn release")

	got := w.starts()
	if len(got) != 2 || got[1].ends != got[0].turn {
		t.Errorf("starts = %+v, want the next prompt to end the first turn", got)
	}
}

func TestGateNewSessionEndsTheOldTurn(t *testing.T) {
	t.Parallel()

	const other = "99999999-2222-4333-8444-555555555555"

	w := &recordingWriter{}

	sendTurns(t, w, dirSeq, recordedStart{dir: "/a"}, recordedStart{dir: "/a", session: other})

	calls := w.all()
	if len(calls) != 3 {
		t.Fatalf("calls = %+v, want start, end, start", calls)
	}

	if end := calls[1]; !end.end || end.session != gateSession || end.dir != "root/a" || end.turn != calls[0].turn {
		t.Errorf("second call = %+v, want End of the old session's turn %d", end, calls[0].turn)
	}

	if second := calls[2]; second.end || second.session != other || second.ends != 0 {
		t.Errorf("third call = %+v, want the new session's start with ends 0, after the End", second)
	}
}

func TestGateEndsTheLastTurnAfterARestart(t *testing.T) {
	t.Parallel()

	w := &recordingWriter{open: []git.OpenTurn{{Root: "root/a", N: 7}, {Root: "root/b", N: 7}}}

	sendTurns(t, w, dirSeq, recordedStart{dir: "/a"})

	starts, ends := w.starts(), w.endings()
	if len(starts) != 1 || starts[0].ends != 7 {
		t.Errorf("starts = %+v, want one that ends the own root's turn 7", starts)
	}

	if len(ends) != 1 || ends[0].dir != "root/b" || ends[0].turn != 7 {
		t.Errorf("End calls = %+v, want only root/b at turn 7", ends)
	}
}

func TestGateFailedEndWritesAnEndMarker(t *testing.T) {
	t.Parallel()

	w := &recordingWriter{
		failEnd:     func(string) error { return errors.New("boom") },
		markHold:    make(chan struct{}),
		markStarted: make(chan recordedStart, 4),
	}

	sendTurns(t, w, dirSeq, recordedStart{dir: "/a"}, recordedStart{dir: "/b"})

	// The second start was answered while the marker is still unwritten.
	select {
	case m := <-w.markStarted:
		if !m.end || m.dir != "root/a" || m.session != gateSession || m.turn != w.starts()[0].turn {
			t.Errorf("marker = %+v, want the end of the first turn in root/a", m)
		}

		close(w.markHold)
	case <-time.After(5 * time.Second):
		t.Fatal("no end marker")
	}
}

func TestGateEndOfATurnThatIsNotOpenWritesNoMarker(t *testing.T) {
	t.Parallel()

	w := &recordingWriter{failEnd: func(string) error { return git.ErrTurnNotOpen }}
	r := startWriterGate(t, w, dirSeq)

	for _, dir := range []string{"/a", "/b"} {
		ev, released := turnEvent("UserPromptSubmit", gateSession, dir)
		r.in <- ev

		within(t, released, time.Second, "turn release")
	}

	// The gate runs its goroutines, markers included, before it returns.
	close(r.in)
	within(t, r.done, 5*time.Second, "gate drain")

	if len(w.endings()) != 1 {
		t.Fatalf("End calls = %+v, want one", w.endings())
	}

	if marks := w.filter(func(c recordedStart) bool { return c.mark }); len(marks) != 0 {
		t.Errorf("markers = %+v, want none", marks)
	}
}

func TestGateUnchangedEndReusesTheStart(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		seq  func() seqFunc
		want bool
	}{
		{
			name: "a counter that did not move",
			seq:  func() seqFunc { return dirSeq },
			want: true,
		},
		{
			name: "a counter that moved",
			seq: func() seqFunc {
				var n atomic.Uint64

				return func(_ context.Context, dir string) (string, uint64, bool) {
					return "root" + dir, n.Add(1), true
				}
			},
		},
		{
			name: "a root that is not watched",
			seq: func() seqFunc {
				return func(_ context.Context, dir string) (string, uint64, bool) { return "root" + dir, 1, false }
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			w := &recordingWriter{}

			sendTurns(t, w, tt.seq(), recordedStart{dir: "/a"}, recordedStart{dir: "/b"})

			ends := w.endings()
			if len(ends) != 1 || ends[0].unchanged != tt.want {
				t.Errorf("End calls = %+v, want one with unchanged %v", ends, tt.want)
			}
		})
	}
}

// --- before-pictures ---

// rootOf puts a path under the worktree root named like its first directory,
// the way dirSeq names the root of a directory.
func rootOf(path string) string {
	first, _, _ := strings.Cut(strings.TrimPrefix(path, "/"), "/")

	return "root/" + first
}

// countingPlaces resolves every path to its rootOf and counts the lookups.
type countingPlaces struct {
	calls atomic.Int32
	// fail returns an error for the paths it names.
	fail func(path string) error
}

func (c *countingPlaces) place(_ context.Context, path string) (git.Place, error) {
	c.calls.Add(1)

	if c.fail != nil {
		if err := c.fail(path); err != nil {
			return git.Place{}, err
		}
	}

	return git.Place{Root: rootOf(path)}, nil
}

// blockedStart holds the Start of one directory: it announces itself on entered
// and returns once unblock closes or its context ends. returned is true from
// when the start has returned.
type blockedStart struct {
	dir      string
	entered  chan struct{}
	unblock  chan struct{}
	returned atomic.Bool
}

func newBlockedStart(dir string) *blockedStart {
	return &blockedStart{dir: dir, entered: make(chan struct{}, 8), unblock: make(chan struct{})}
}

func (b *blockedStart) onStart(ctx context.Context, dir string) error {
	if dir != b.dir {
		return nil
	}

	b.entered <- struct{}{}

	defer b.returned.Store(true)

	select {
	case <-b.unblock:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// editEvent is an edit tool's before-event; its Done closes the returned
// channel, and records in answeredAfter whether flag was set by then.
func editEvent(session, path string, flag *atomic.Bool, answeredAfter *atomic.Bool) (harness.Event, <-chan struct{}) {
	released := make(chan struct{})
	ev := harness.Event{
		Name: "PreToolUse", Session: core.SessionID(session), Cwd: "/a", Edit: path, Before: true,
		Done: sync.OnceFunc(func() {
			if flag != nil {
				answeredAfter.Store(flag.Load())
			}

			close(released)
		}),
	}

	return ev, released
}

// startedTurn runs a gate with the places over the writer and sends the turn
// start for /a, whose own root is root/a.
func startedTurn(t *testing.T, w turnWriter, places placeFunc) *gateRig {
	t.Helper()

	r := startPlacesGate(t, w, places, dirSeq)
	ev, released := turnEvent("UserPromptSubmit", gateSession, "/a")

	r.in <- ev

	within(t, released, time.Second, "turn release")

	return r
}

func drain(t *testing.T, r *gateRig) {
	t.Helper()

	close(r.in)
	within(t, r.done, 5*time.Second, "gate drain")
}

func callsIn(calls []recordedStart, dir string) []recordedStart {
	var out []recordedStart

	for _, c := range calls {
		if c.dir == dir {
			out = append(out, c)
		}
	}

	return out
}

func TestGateFirstEditTakesTheStartBeforeAnswering(t *testing.T) {
	t.Parallel()

	blocked := newBlockedStart("root/b")
	w := &recordingWriter{onStart: blocked.onStart}
	places := &countingPlaces{}
	r := startedTurn(t, w, places.place)

	var answeredAfter atomic.Bool

	ev, released := editEvent(gateSession, "/b/f.go", &blocked.returned, &answeredAfter)
	r.in <- ev

	within(t, blocked.entered, time.Second, "the edit's start")
	close(blocked.unblock)
	within(t, released, time.Second, "Done of the edit")

	if !answeredAfter.Load() {
		t.Error("the edit was answered before its start returned")
	}

	starts := callsIn(w.starts(), "root/b")
	if len(starts) != 1 || starts[0].ends != 0 || starts[0].turn != w.starts()[0].turn {
		t.Errorf("starts in root/b = %+v, want one with the turn's number and ends 0", starts)
	}
}

func TestGateLaterEditsInAStartedWorktreeAnswerAtOnce(t *testing.T) {
	t.Parallel()

	blocked := newBlockedStart("root/b")
	w := &recordingWriter{onStart: blocked.onStart}
	places := &countingPlaces{}
	r := startedTurn(t, w, places.place)

	t.Cleanup(func() { close(blocked.unblock) })

	first, _ := editEvent(gateSession, "/b/f.go", nil, nil)
	r.in <- first

	within(t, blocked.entered, time.Second, "the first edit's start")

	own, released := editEvent(gateSession, "/a/g.go", nil, nil)
	r.in <- own

	within(t, released, time.Second, "Done of the edit in the own root")

	if got := w.starts(); len(got) != 2 {
		t.Errorf("starts = %+v, want only the turn's and root/b's", got)
	}
}

func TestGateParallelFirstEditsShareOneStart(t *testing.T) {
	t.Parallel()

	blocked := newBlockedStart("root/b")
	w := &recordingWriter{onStart: blocked.onStart}
	places := &countingPlaces{}
	r := startedTurn(t, w, places.place)

	var early, late atomic.Bool

	one, released1 := editEvent(gateSession, "/b/one.go", &blocked.returned, &early)
	two, released2 := editEvent(gateSession, "/b/two.go", &blocked.returned, &late)

	r.in <- one

	r.in <- two

	within(t, blocked.entered, time.Second, "the shared start")

	// Both holds are open before the start returns: wait for the second lookup.
	waitUntil(t, "both lookups did not finish", func() bool { return places.calls.Load() == 2 })

	for i, released := range []<-chan struct{}{released1, released2} {
		select {
		case <-released:
			t.Errorf("edit %d was answered while the shared start was still being taken", i+1)
		default:
		}
	}

	close(blocked.unblock)
	within(t, released1, time.Second, "Done of the first edit")
	within(t, released2, time.Second, "Done of the second edit")

	if !early.Load() || !late.Load() {
		t.Errorf("answered after the start returned: first %v, second %v, want both", early.Load(), late.Load())
	}

	drain(t, r)

	if got := callsIn(w.starts(), "root/b"); len(got) != 1 {
		t.Errorf("starts in root/b = %+v, want one", got)
	}
}

func TestGateForgottenTurnClearsTheUnchangedSkip(t *testing.T) {
	t.Parallel()

	w := &recordingWriter{}
	r := startPlacesGate(t, w, noPlaces, dirSeq)

	send := func(deadline time.Time) {
		t.Helper()

		ev, released := turnEvent("UserPromptSubmit", gateSession, "/a")
		ev.Deadline = deadline

		r.in <- ev

		within(t, released, 5*time.Second, "turn release")
		within(t, forwardedAfter(t, r, "UserPromptSubmit").Snapped, 5*time.Second, "the turn's snapshot")
	}

	send(time.Time{})

	// The budget is already spent when this turn start runs: nothing is
	// snapshotted, the first turn's end and the new start are markers.
	send(time.Now().Add(-time.Millisecond))

	waitUntil(t, "the end marker was not written", func() bool {
		return len(w.filter(func(c recordedStart) bool { return c.mark && c.end })) == 1
	})

	send(time.Time{})
	drain(t, r)

	starts := w.starts()

	if len(starts) != 2 {
		t.Fatalf("starts = %+v, want the first and the third turn's", starts)
	}

	if starts[1].unchanged {
		t.Error("the third start skipped as unchanged, want a fresh snapshot: the forgotten turn took none")
	}
}

func TestGateWaiterCancelsTheStartItWouldOutrun(t *testing.T) {
	t.Parallel()

	blocked := newBlockedStart("root/b")
	w := &recordingWriter{onStart: blocked.onStart}
	slow := make(chan struct{})
	place := func(ctx context.Context, path string) (git.Place, error) {
		if strings.HasSuffix(path, "slow.go") {
			select {
			case <-slow:
			case <-ctx.Done():
				return git.Place{}, ctx.Err()
			}
		}

		return git.Place{Root: rootOf(path)}, nil
	}

	r := startedTurn(t, w, place)
	turn := w.starts()[0].turn

	// The waiter's deadline comes long before the claimer's.
	var answeredAfterStart atomic.Bool

	waiter, releasedWaiter := editEvent(gateSession, "/b/slow.go", &blocked.returned, &answeredAfterStart)
	waiter.Deadline = time.Now().Add(300 * time.Millisecond)
	claimer, _ := editEvent(gateSession, "/b/fast.go", nil, nil)

	r.in <- waiter

	r.in <- claimer

	within(t, blocked.entered, time.Second, "the claimer's start")
	close(slow)

	within(t, releasedWaiter, 5*time.Second, "Done of the waiter")

	if !answeredAfterStart.Load() {
		t.Error("the waiter was answered while the start in its worktree was still running")
	}

	waitUntil(t, "the start marker in root/b was not written", func() bool {
		return len(callsIn(w.all(), "root/b")) == 2
	})
	drain(t, r)

	want := []recordedStart{
		{dir: "root/b", session: gateSession, turn: turn},
		{dir: "root/b", session: gateSession, turn: turn, mark: true},
	}

	if got := callsIn(w.all(), "root/b"); !slices.Equal(got, want) {
		t.Errorf("calls in root/b = %+v, want the cancelled start, then its start marker", got)
	}
}

func TestGateEditOutsideATurnAnswersAtOnce(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		turnFirst bool
		session   string
	}{
		{"before any turn start", false, gateSession},
		{"a main-session edit of another session", true, "aaaaaaaa-2222-4333-8444-555555555555"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			w := &recordingWriter{}
			places := &countingPlaces{}

			var r *gateRig
			if tt.turnFirst {
				r = startedTurn(t, w, places.place)
			} else {
				r = startPlacesGate(t, w, places.place, dirSeq)
			}

			ev, released := editEvent(tt.session, "/b/f.go", nil, nil)
			r.in <- ev

			within(t, released, time.Second, "Done of the edit")

			if got := forwardedAfter(t, r, "PreToolUse"); got.Snapped != nil {
				t.Error("the edit has Snapped, want no snapshot")
			}

			drain(t, r)

			if n := places.calls.Load(); n != 0 {
				t.Errorf("place lookups = %d, want none", n)
			}

			if got := callsIn(w.all(), "root/b"); len(got) != 0 {
				t.Errorf("calls in root/b = %+v, want none", got)
			}
		})
	}
}

// forwardedAfter reads events from the gate's output until one named name.
func forwardedAfter(t *testing.T, r *gateRig, name string) harness.Event {
	t.Helper()

	for {
		select {
		case ev := <-r.out:
			if ev.Name == name {
				return ev
			}
		case <-time.After(time.Second):
			t.Fatalf("no %s event forwarded", name)
		}
	}
}

func TestGateEditAfterStopBelongsToTheTurn(t *testing.T) {
	t.Parallel()

	w := &recordingWriter{}
	places := &countingPlaces{}
	r := startedTurn(t, w, places.place)

	r.in <- harness.Event{Name: "Stop", Session: gateSession, Cwd: "/a"}

	ev, released := editEvent(gateSession, "/b/f.go", nil, nil)
	r.in <- ev

	within(t, released, time.Second, "Done of the edit")

	turn := w.starts()[0].turn
	if got := callsIn(w.starts(), "root/b"); len(got) != 1 || got[0].turn != turn {
		t.Fatalf("starts in root/b = %+v, want one at the turn %d", got, turn)
	}

	next, released := turnEvent("UserPromptSubmit", gateSession, "/a")
	r.in <- next

	within(t, released, time.Second, "next turn release")

	if got := callsIn(w.endings(), "root/b"); len(got) != 1 || got[0].turn != turn {
		t.Errorf("ends in root/b = %+v, want one of turn %d", got, turn)
	}
}

func TestGateIgnoredEditsStartNothing(t *testing.T) {
	t.Parallel()

	w := &recordingWriter{}
	r := startedTurn(t, w, noPlaces)

	ev, done := editEvent(gateSession, "/ignored/ledger.md", nil, nil)
	r.in <- ev

	within(t, done, time.Second, "Done of an ignored edit")
	drain(t, r)

	if got := w.starts(); len(got) != 1 {
		t.Errorf("starts = %+v, want only the turn's", got)
	}
}

func TestGateFailedStartMarksTheTurn(t *testing.T) {
	t.Parallel()

	w := &recordingWriter{onStart: func(ctx context.Context, dir string) error {
		if dir != "root/b" {
			return nil
		}

		<-ctx.Done()

		return ctx.Err()
	}}
	places := &countingPlaces{}
	r := startedTurn(t, w, places.place)

	first, released := editEvent(gateSession, "/b/f.go", nil, nil)
	first.Deadline = time.Now().Add(500 * time.Millisecond)

	r.in <- first

	within(t, released, 5*time.Second, "Done of the first edit")

	second, released := editEvent(gateSession, "/b/g.go", nil, nil)
	r.in <- second

	within(t, released, time.Second, "Done of the second edit")
	drain(t, r)

	if got := callsIn(w.starts(), "root/b"); len(got) != 1 {
		t.Errorf("starts in root/b = %+v, want one", got)
	}

	turn := w.starts()[0].turn

	marks := callsIn(w.filter(func(c recordedStart) bool { return c.mark }), "root/b")
	if len(marks) != 1 || marks[0].end || marks[0].turn != turn {
		t.Errorf("markers in root/b = %+v, want one start marker of turn %d", marks, turn)
	}
}

func TestGateUnsureTurnMarksLaterStarts(t *testing.T) {
	t.Parallel()

	w := &recordingWriter{}
	places := &countingPlaces{fail: func(path string) error {
		if strings.HasPrefix(path, "/broken/") {
			return errors.New("git hiccup")
		}

		return nil
	}}
	r := startedTurn(t, w, places.place)

	lost, released := editEvent(gateSession, "/broken/f.go", nil, nil)
	r.in <- lost

	within(t, released, time.Second, "Done of the edit with no place")

	ev, released := editEvent(gateSession, "/b/f.go", nil, nil)
	r.in <- ev

	within(t, released, time.Second, "Done of the edit in the new root")
	drain(t, r)

	if got := callsIn(w.starts(), "root/b"); len(got) != 0 {
		t.Errorf("starts in root/b = %+v, want none", got)
	}

	if got := callsIn(w.filter(func(c recordedStart) bool { return c.mark }), "root/b"); len(got) != 1 {
		t.Errorf("markers in root/b = %+v, want one", got)
	}
}

func TestGateBashChangesMarkANewWorktree(t *testing.T) {
	t.Parallel()

	w := &recordingWriter{}
	places := &countingPlaces{}
	r := startedTurn(t, w, places.place)

	r.in <- harness.Event{
		Name: "PostToolUse", Session: gateSession, Cwd: "/a",
		Changed: []string{"/b/out.txt", "/a/out.txt"},
	}

	drain(t, r)

	marks := w.filter(func(c recordedStart) bool { return c.mark })
	if len(marks) != 1 || marks[0].dir != "root/b" || marks[0].end || marks[0].turn != w.starts()[0].turn {
		t.Errorf("markers = %+v, want one start marker in root/b", marks)
	}

	if got := callsIn(w.starts(), "root/b"); len(got) != 0 {
		t.Errorf("starts in root/b = %+v, want none", got)
	}
}

func TestGateSubagentEditUsesThePanesTurn(t *testing.T) {
	t.Parallel()

	w := &recordingWriter{}
	places := &countingPlaces{}
	r := startedTurn(t, w, places.place)

	ev, released := editEvent("bbbbbbbb-2222-4333-8444-555555555555", "/b/f.go", nil, nil)
	ev.Subagent = true

	r.in <- ev

	within(t, released, time.Second, "Done of the subagent's edit")

	got := callsIn(w.starts(), "root/b")
	if len(got) != 1 || got[0].turn != w.starts()[0].turn || got[0].session != gateSession {
		t.Errorf("starts in root/b = %+v, want one in the pane's turn and session", got)
	}
}

func TestGateNextPromptEndsEveryStartedWorktree(t *testing.T) {
	t.Parallel()

	w := &recordingWriter{}
	places := &countingPlaces{}
	r := startedTurn(t, w, places.place)

	for _, path := range []string{"/b/f.go", "/c/f.go"} {
		ev, released := editEvent(gateSession, path, nil, nil)
		r.in <- ev

		within(t, released, time.Second, "Done of an edit")
	}

	r.in <- harness.Event{Name: "PostToolUse", Session: gateSession, Cwd: "/a", Changed: []string{"/d/out.txt"}}

	waitUntil(t, "the marker in root/d was not written", func() bool {
		return len(callsIn(w.all(), "root/d")) == 1
	})

	turn := w.starts()[0].turn

	next, released := turnEvent("UserPromptSubmit", gateSession, "/a")
	r.in <- next

	within(t, released, time.Second, "next turn release")

	var ended []string

	for _, e := range w.endings() {
		if e.turn != turn {
			t.Errorf("end %+v is not of turn %d", e, turn)
		}

		ended = append(ended, e.dir)
	}

	slices.Sort(ended)

	if !slices.Equal(ended, []string{"root/b", "root/c"}) {
		t.Errorf("ended = %v, want root/b and root/c (the own root ends through the start, root/d has a marker)", ended)
	}

	if got := w.starts(); got[len(got)-1].ends != turn {
		t.Errorf("the next start = %+v, want it to end the own root's turn %d", got[len(got)-1], turn)
	}
}

// --- before-pictures with real hooks and git ---

// linkedWorktree adds a worktree to repo and returns its path.
func linkedWorktree(t *testing.T, repo string) string {
	t.Helper()

	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}

	wt := filepath.Join(dir, "wt")
	gitOut(t, repo, "worktree", "add", "-q", "-b", "other", wt)

	return wt
}

func sendEdit(ctx context.Context, sock, repo, path string) error {
	return harness.Send(ctx, sock, harness.Event{
		Name: "PreToolUse", Session: gateSession, Cwd: repo, Pane: "p", Edit: path, Before: true,
	})
}

func TestGateNextPromptMarksAStartThatLandsLate(t *testing.T) {
	t.Parallel()

	blocked := newBlockedStart("root/b")
	w := &recordingWriter{onStart: blocked.onStart}
	places := &countingPlaces{}
	r := startedTurn(t, w, places.place)
	turn := w.starts()[0].turn

	ev, _ := editEvent(gateSession, "/b/f.go", nil, nil)
	r.in <- ev

	within(t, blocked.entered, time.Second, "the edit's start")

	next, released := turnEvent("UserPromptSubmit", gateSession, "/a")
	next.Deadline = time.Now().Add(200 * time.Millisecond)

	r.in <- next

	within(t, released, 5*time.Second, "next turn release")

	// The start is still blocked when the prompt's ends and start are done.
	within(t, forwardedAfter(t, r, "UserPromptSubmit").Snapped, 5*time.Second, "the next turn's snapshot")
	close(blocked.unblock)

	waitUntil(t, "the end marker in root/b was not written", func() bool {
		return len(callsIn(w.all(), "root/b")) == 2
	})
	drain(t, r)

	want := []recordedStart{
		{dir: "root/b", session: gateSession, turn: turn},
		{dir: "root/b", session: gateSession, turn: turn, mark: true, end: true},
	}

	if got := callsIn(w.all(), "root/b"); !slices.Equal(got, want) {
		t.Errorf("calls in root/b = %+v, want the start, then an end marker, no real end", got)
	}
}

func TestGateLateEditOfThePreviousTurnWritesAMarker(t *testing.T) {
	t.Parallel()

	entered, unblock := make(chan struct{}, 1), make(chan struct{})

	var starts atomic.Int32

	w := &recordingWriter{onStart: func(ctx context.Context, dir string) error {
		if dir != "/a" {
			return nil
		}

		if starts.Add(1) < 2 {
			return nil
		}

		entered <- struct{}{}

		select {
		case <-unblock:
			return nil
		case <-ctx.Done():
			return ctx.Err()
		}
	}}

	lookup := make(chan struct{})
	place := func(ctx context.Context, path string) (git.Place, error) {
		if strings.HasSuffix(path, "late.go") {
			select {
			case <-lookup:
			case <-ctx.Done():
				return git.Place{}, ctx.Err()
			}
		}

		return git.Place{Root: rootOf(path)}, nil
	}

	r := startedTurn(t, w, place)
	turn := w.starts()[0].turn

	ev, _ := editEvent(gateSession, "/b/late.go", nil, nil)
	r.in <- ev

	next, _ := turnEvent("UserPromptSubmit", gateSession, "/a")
	r.in <- next

	within(t, entered, 5*time.Second, "the next turn's own start")

	// The edit's lookup answers while the next turn is taking its start.
	close(lookup)

	waitUntil(t, "the start marker in root/b was not written", func() bool {
		return len(callsIn(w.all(), "root/b")) == 1
	})
	close(unblock)
	drain(t, r)

	want := []recordedStart{{dir: "root/b", session: gateSession, turn: turn, mark: true}}

	if got := callsIn(w.all(), "root/b"); !slices.Equal(got, want) {
		t.Errorf("calls in root/b = %+v, want one start marker and no start or end", got)
	}
}
