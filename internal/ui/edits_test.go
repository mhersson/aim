package ui

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/mhersson/aim/internal/core"
	"github.com/mhersson/aim/internal/git"
	"github.com/mhersson/aim/internal/harness"
)

// fakeResolver answers the edit resolver from a table and counts its calls.
// A path in neither table is not counted.
type fakeResolver struct {
	mu     sync.Mutex
	places map[string]git.Place
	errs   map[string]error
	calls  []string
}

func (f *fakeResolver) resolve(_ context.Context, path string) (git.Place, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	f.calls = append(f.calls, path)

	if err := f.errs[path]; err != nil {
		return git.Place{}, err
	}

	if p, ok := f.places[path]; ok {
		return p, nil
	}

	return git.Place{}, fmt.Errorf("edit %s: %w", path, git.ErrNotCounted)
}

func (f *fakeResolver) count() int {
	f.mu.Lock()
	defer f.mu.Unlock()

	return len(f.calls)
}

func placeAt(root string) git.Place {
	return git.Place{Root: root, Common: root + "/.git", Repo: filepath.Base(root)}
}

// hookEditRig is the agent app with p2 an agent and a fake edit resolver.
type hookEditRig struct {
	*App
	id  core.PaneID
	res *fakeResolver
	log *bytes.Buffer // everything the app logs, debug included
}

func newHookEditRig(t *testing.T, places map[string]git.Place, errs map[string]error) *hookEditRig {
	t.Helper()

	a, _, _ := agentApp(t)

	// Opening the agent in a directory that is no repository left a notice.
	a.notice = ""

	r := &hookEditRig{App: a, id: "p2", res: &fakeResolver{places: places, errs: errs}, log: &bytes.Buffer{}}
	a.edits.resolve = r.res.resolve
	a.log = slog.New(slog.NewTextHandler(r.log, &slog.HandlerOptions{Level: slog.LevelDebug}))

	return r
}

func (r *hookEditRig) event(name, path string) harness.Event {
	return harness.Event{Pane: r.id, Name: name, Edit: path}
}

// changed is a finished Bash command that changed paths.
func (r *hookEditRig) changed(paths ...string) harness.Event {
	return harness.Event{Pane: r.id, Name: "PostToolUse", Changed: paths}
}

// levels lists the levels the lines of the log that hold text were written at.
func (r *hookEditRig) levels(text string) []string {
	var levels []string

	for line := range strings.SplitSeq(r.log.String(), "\n") {
		if !strings.Contains(line, text) {
			continue
		}

		for f := range strings.FieldsSeq(line) {
			if l, ok := strings.CutPrefix(f, "level="); ok {
				levels = append(levels, l)
			}
		}
	}

	return levels
}

// hook delivers an event and returns the commands it raised.
func (r *hookEditRig) hook(ev harness.Event) tea.Cmd { return hook(r.App, ev) }

// deliver runs cmd and feeds every message it returns back to the app.
func (r *hookEditRig) deliver(cmd tea.Cmd) {
	for _, m := range flatten(cmd) {
		if m != nil {
			r.Update(m)
		}
	}
}

func (r *hookEditRig) pane() core.Pane { return r.store.State().Panes[r.id] }

func (r *hookEditRig) roots() []string {
	var out []string
	for _, e := range r.pane().Edits {
		out = append(out, e.Root)
	}

	slices.Sort(out)

	return out
}

func TestEditMovesFollowedPlace(t *testing.T) {
	t.Parallel()

	r := newHookEditRig(t, map[string]git.Place{"/w/b/x.go": placeAt("/w/b")}, nil)
	own := r.pane().FollowDir()

	cmd := r.hook(r.event("PreToolUse", "/w/b/x.go"))
	if got := r.pane().FollowDir(); got != own {
		t.Fatalf("FollowDir() = %q before the answer, want %q", got, own)
	}

	r.deliver(cmd)

	if got := r.pane().FollowDir(); got != "/w/b" {
		t.Errorf("FollowDir() = %q, want /w/b", got)
	}

	if e, ok := r.pane().Followed(); !ok || e.Repo != "b" || e.Common != "/w/b/.git" || e.At.IsZero() {
		t.Errorf("Followed() = %+v, %v, want the resolved place stamped with the hook's time", e, ok)
	}
}

func TestEditAnswersOutOfOrder(t *testing.T) {
	t.Parallel()

	r := newHookEditRig(t, map[string]git.Place{"/w/a/x.go": placeAt("/w/a"), "/w/b/x.go": placeAt("/w/b")}, nil)

	inA := r.hook(r.event("PreToolUse", "/w/a/x.go"))
	inB := r.hook(r.event("PreToolUse", "/w/b/x.go"))

	r.deliver(inB)
	r.deliver(inA)

	if got := r.pane().FollowDir(); got != "/w/b" {
		t.Errorf("FollowDir() = %q, want /w/b: the edit that arrived last", got)
	}

	if got := r.roots(); !slices.Equal(got, []string{"/w/a", "/w/b"}) {
		t.Errorf("edited places = %v, want both", got)
	}
}

func TestEditNotCounted(t *testing.T) {
	t.Parallel()

	// level is how the answer's error is logged; "" for not at all, since an
	// error that names the edited path must not reach the log for a normal case.
	tests := []struct {
		name  string
		err   error
		level string
	}{
		{"not counted", fmt.Errorf("edit /w/b/x.go: %w: outside any work tree", git.ErrNotCounted), ""},
		{"another error", errors.New("edit /w/b/x.go: git exploded"), "WARN"},
		{"the context ends", context.Canceled, "DEBUG"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			r := newHookEditRig(t, nil, map[string]error{"/w/b/x.go": tt.err})
			own := r.pane().FollowDir()

			r.deliver(r.hook(r.event("PostToolUse", "/w/b/x.go")))

			if r.res.count() != 1 {
				t.Fatalf("resolver ran %d times, want 1", r.res.count())
			}

			if p := r.pane(); len(p.Edits) != 0 || p.FollowDir() != own {
				t.Errorf("pane = %+v, want no edits and FollowDir() %q", p, own)
			}

			if r.notice != "" {
				t.Errorf("notice = %q, want none", r.notice)
			}

			var want []string
			if tt.level != "" {
				want = []string{tt.level}
			}

			if got := r.levels(tt.err.Error()); !slices.Equal(got, want) {
				t.Errorf("the error was logged at %v, want %v", got, want)
			}
		})
	}
}

func TestEditSamePathResolvesOnce(t *testing.T) {
	t.Parallel()

	tests := []struct{ name, file string }{
		{"a counted path", "a.txt"},
		{"an ignored path", "ledger.log"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			r := newFollowRig(t, "")
			path := filepath.Join(r.dirA, tt.file)

			// The tool runs between the two hooks, so the first answer is in by the second.
			r.hookNow(harness.Event{Pane: r.id, Name: "PreToolUse", Edit: path, Before: true})
			r.hookNow(r.editHook(path))

			if got := r.resolverCalls(); got != 1 {
				t.Errorf("resolver ran %d times for a PreToolUse and a PostToolUse, want 1", got)
			}
		})
	}
}

func TestEditOtherPathResolvesAgain(t *testing.T) {
	t.Parallel()

	r := newHookEditRig(t, map[string]git.Place{"/w/b/x.go": placeAt("/w/b"), "/w/b/y.go": placeAt("/w/b")}, nil)

	r.deliver(r.hook(r.event("PostToolUse", "/w/b/x.go")))
	r.deliver(r.hook(r.event("PostToolUse", "/w/b/y.go")))

	if got := r.res.count(); got != 2 {
		t.Errorf("resolver ran %d times, want one per path", got)
	}
}

func TestEditAfterSessionChange(t *testing.T) {
	t.Parallel()

	r := newHookEditRig(t, map[string]git.Place{"/w/b/x.go": placeAt("/w/b"), "/w/c/y.go": placeAt("/w/c")}, nil)
	start := func(session core.SessionID) harness.Event {
		return harness.Event{Pane: r.id, Name: "SessionStart", Start: true, Session: session}
	}

	r.hook(start("s1"))

	edit := r.hook(r.event("PostToolUse", "/w/b/x.go"))

	r.hook(start("s2"))
	r.deliver(edit)

	if p := r.pane(); len(p.Edits) != 0 || p.Session != "s2" {
		t.Errorf("pane = session %q with %d edits, want s2 with none: the answer is of the old session", p.Session, len(p.Edits))
	}

	r.deliver(r.hook(r.event("PostToolUse", "/w/c/y.go")))

	if got := r.roots(); !slices.Equal(got, []string{"/w/c"}) {
		t.Errorf("edited places = %v after an edit in the new session, want /w/c", got)
	}
}

func TestSubagentCwdKeepsOwnDirectory(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		subagent bool
		want     string
	}{
		{"a subagent's event", true, ""},
		{"the agent's own event", false, "/w/other"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			r := newHookEditRig(t, nil, nil)

			r.hook(harness.Event{Pane: r.id, Name: "PostToolUse", Cwd: "/w/other", Subagent: tt.subagent})

			if got := r.pane().AgentCwd; got != tt.want {
				t.Errorf("AgentCwd = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestSubagentEditCounts(t *testing.T) {
	t.Parallel()

	r := newHookEditRig(t, map[string]git.Place{"/w/b/x.go": placeAt("/w/b")}, nil)

	ev := r.event("PostToolUse", "/w/b/x.go")
	ev.Subagent = true

	r.deliver(r.hook(ev))

	if got := r.pane().FollowDir(); got != "/w/b" {
		t.Errorf("FollowDir = %q, want the edit recorded", got)
	}
}

func TestBashChangedFollows(t *testing.T) {
	t.Parallel()

	r := newHookEditRig(t, map[string]git.Place{"/w/a/x.go": placeAt("/w/a"), "/w/b/y.go": placeAt("/w/b")}, nil)

	cmd := r.hook(r.changed("/w/a/x.go", "/w/b/y.go"))
	if r.pane().FollowDir() == "/w/b" || len(r.pane().Edits) != 0 {
		t.Fatalf("pane = %+v before the answer, want nothing recorded", r.pane())
	}

	r.deliver(cmd)

	if got := r.roots(); !slices.Equal(got, []string{"/w/a", "/w/b"}) {
		t.Errorf("edited places = %v, want both", got)
	}

	if got := r.pane().FollowDir(); got != "/w/b" {
		t.Errorf("FollowDir() = %q, want /w/b: the last place the list reaches", got)
	}

	if e, ok := r.pane().Followed(); !ok || e.Repo != "b" || e.Common != "/w/b/.git" || e.At.IsZero() {
		t.Errorf("Followed() = %+v, %v, want the resolved place stamped with the hook's time", e, ok)
	}
}

func TestBashChangedNotCounted(t *testing.T) {
	t.Parallel()

	notCounted := fmt.Errorf("edit /w/n/x.go: %w: outside any work tree", git.ErrNotCounted)
	boom := errors.New("edit /w/e/x.go: git exploded")

	tests := []struct {
		name  string
		err   error // the answer for /w/n/x.go or /w/e/x.go
		path  string
		roots []string
		calls int
		level string // how the error is logged; "" for not at all
	}{
		{"not counted is skipped", notCounted, "/w/n/x.go", []string{"/w/a", "/w/b"}, 3, ""},
		{"another error ends the list", boom, "/w/e/x.go", []string{"/w/a"}, 2, "WARN"},
		{"the context ends the list", context.Canceled, "/w/e/x.go", []string{"/w/a"}, 2, "DEBUG"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			r := newHookEditRig(t,
				map[string]git.Place{"/w/a/x.go": placeAt("/w/a"), "/w/b/y.go": placeAt("/w/b")},
				map[string]error{tt.path: tt.err})

			r.deliver(r.hook(r.changed("/w/a/x.go", tt.path, "/w/b/y.go")))

			if got := r.roots(); !slices.Equal(got, tt.roots) {
				t.Errorf("edited places = %v, want %v", got, tt.roots)
			}

			if got := r.res.count(); got != tt.calls {
				t.Errorf("resolver ran %d times, want %d", got, tt.calls)
			}

			if r.notice != "" {
				t.Errorf("notice = %q, want none", r.notice)
			}

			var want []string
			if tt.level != "" {
				want = []string{tt.level}
			}

			if got := r.levels(tt.err.Error()); !slices.Equal(got, want) {
				t.Errorf("the error was logged at %v, want %v", got, want)
			}
		})
	}
}

func TestBashChangedAfterSessionChange(t *testing.T) {
	t.Parallel()

	r := newHookEditRig(t, map[string]git.Place{"/w/a/x.go": placeAt("/w/a"), "/w/b/y.go": placeAt("/w/b")}, nil)
	start := func(session core.SessionID) harness.Event {
		return harness.Event{Pane: r.id, Name: "SessionStart", Start: true, Session: session}
	}

	r.hook(start("s1"))

	list := r.hook(r.changed("/w/a/x.go", "/w/b/y.go"))

	r.hook(start("s2"))
	r.deliver(list)

	if p := r.pane(); len(p.Edits) != 0 || p.Session != "s2" {
		t.Errorf("pane = session %q with %d edits, want s2 with none: the answer is of the old session", p.Session, len(p.Edits))
	}

	r.deliver(r.hook(r.changed("/w/b/y.go")))

	if got := r.roots(); !slices.Equal(got, []string{"/w/b"}) {
		t.Errorf("edited places = %v after a list in the new session, want /w/b", got)
	}
}

func TestBashChangedOrderAgainstEdits(t *testing.T) {
	t.Parallel()

	r := newHookEditRig(t, map[string]git.Place{"/w/a/x.go": placeAt("/w/a"), "/w/b/y.go": placeAt("/w/b")}, nil)

	inA := r.hook(r.event("PreToolUse", "/w/a/x.go"))
	inB := r.hook(r.changed("/w/b/y.go"))

	r.deliver(inB)
	r.deliver(inA)

	if got := r.pane().FollowDir(); got != "/w/b" {
		t.Errorf("FollowDir() = %q, want /w/b: the list arrived after the edit", got)
	}

	if got := r.roots(); !slices.Equal(got, []string{"/w/a", "/w/b"}) {
		t.Errorf("edited places = %v, want both", got)
	}
}

func TestBashChangedOneSlot(t *testing.T) {
	t.Parallel()

	var paths []string

	places := map[string]git.Place{}

	for i := range 16 {
		root := fmt.Sprintf("/w/d%02d", i)
		paths = append(paths, root+"/x.go")
		places[root+"/x.go"] = placeAt(root)
	}

	r := newHookEditRig(t, places, nil)

	// The list resolves under the one slot left, and holds it for the whole list.
	for range cap(r.probeSem) - 1 {
		r.probeSem <- struct{}{}
	}

	var inFlight, peak, calls, slotsFree atomic.Int32

	r.edits.resolve = func(ctx context.Context, path string) (git.Place, error) {
		peak.Store(max(peak.Load(), inFlight.Add(1)))
		calls.Add(1)
		slotsFree.Add(int32(cap(r.probeSem) - len(r.probeSem)))
		runtime.Gosched()
		inFlight.Add(-1)

		return r.res.resolve(ctx, path)
	}

	r.deliver(r.hook(r.changed(paths...)))

	if got := calls.Load(); got != int32(len(paths)) {
		t.Errorf("resolver ran %d times, want %d", got, len(paths))
	}

	if got := peak.Load(); got != 1 {
		t.Errorf("resolutions at once = %d, want 1", got)
	}

	if got := slotsFree.Load(); got != 0 {
		t.Errorf("a slot was free while the list resolved, %d times in all", got)
	}

	if got := len(r.probeSem); got != cap(r.probeSem)-1 {
		t.Errorf("held slots after the list = %d, want %d: the list gave its slot back", got, cap(r.probeSem)-1)
	}

	if got := len(r.pane().Edits); got != len(paths) {
		t.Errorf("recorded places = %d, want %d", got, len(paths))
	}
}

func TestBashChangedSubagent(t *testing.T) {
	t.Parallel()

	r := newHookEditRig(t, map[string]git.Place{"/w/b/x.go": placeAt("/w/b")}, nil)

	ev := r.changed("/w/b/x.go")
	ev.Subagent = true

	r.deliver(r.hook(ev))

	if got := r.pane().FollowDir(); got != "/w/b" {
		t.Errorf("FollowDir = %q, want the list recorded", got)
	}
}

func TestBashChangedInLinkedWorktree(t *testing.T) {
	t.Parallel()

	r := newRailApp(t, false)

	mainRepo := r.fake.repo(t, "")
	main := mainRepo.Root()
	write(t, filepath.Join(main, "a.txt"), "one\n")
	mainRepo.commit("init")

	// Inside the main checkout, as the worktree convention puts it.
	linked := mainRepo.worktree(t, filepath.Join(main, ".worktrees", "feat"), "feat").Root()

	id := r.addAgent(t, false, main)

	r.deliver(hook(r.App, harness.Event{Pane: id, Name: "PostToolUse", Changed: []string{
		filepath.Join(main, "a.txt"), filepath.Join(linked, "a.txt"),
	}}))

	p := r.store.State().Panes[id]

	var roots []string
	for _, e := range p.Edits {
		roots = append(roots, e.Root)
	}

	slices.Sort(roots)

	if want := []string{main, linked}; !slices.Equal(roots, want) {
		t.Errorf("edited places = %v, want %v", roots, want)
	}

	if p.FollowDir() != linked {
		t.Errorf("FollowDir() = %q, want the linked worktree %q", p.FollowDir(), linked)
	}
}

func TestEditInLinkedWorktree(t *testing.T) {
	t.Parallel()

	r := newRailApp(t, false)
	tr := r.trees(t)
	id := r.addAgent(t, false, tr.main)

	r.deliver(hook(r.App, harness.Event{Pane: id, Name: "PostToolUse", Edit: filepath.Join(tr.linked, "a.txt")}))

	p := r.store.State().Panes[id]

	e, ok := p.Followed()
	if !ok || p.FollowDir() != tr.linked || e.Root != tr.linked || e.Repo != filepath.Base(tr.main) || e.Common == "" {
		t.Errorf("Followed() = %+v, %v, FollowDir %q, want the linked worktree %q of repository %q", e, ok, p.FollowDir(), tr.linked, filepath.Base(tr.main))
	}
}
