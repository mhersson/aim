package github

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/cli/go-gh/v2/pkg/api"
)

const (
	testFast = 20 * time.Millisecond
	testSlow = 200 * time.Millisecond
	// idle is the Slow value for tests that must never see a scheduled poll.
	idle = time.Hour
)

var (
	repoA = Repo{Host: "github.com", Owner: "octo", Name: "a"}
	repoB = Repo{Host: "github.com", Owner: "octo", Name: "b"}
)

// pollServer serves two open pull requests (1 and 2) and their checks.
type pollServer struct {
	pendingPR atomic.Int64 // the PR number whose checks are pending; 0 = none
	gql, rest atomic.Int64
	single    atomic.Int64 // the by-number queries, which gql counts too

	// past maps a PR number outside the list to its state; any other number
	// asked by number is not found. singleFails answers every such query with
	// a GraphQL error instead.
	past        map[int]string
	singleFails bool

	// handle, when set, may answer the nth GraphQL request itself, and
	// restHandle the nth REST request.
	handle     func(n int64, w http.ResponseWriter, r *http.Request) bool
	restHandle func(n int64, w http.ResponseWriter, r *http.Request) bool

	mu       sync.Mutex
	gqlTimes []time.Time
	restPath []string
}

func (s *pollServer) arrivals() []time.Time {
	s.mu.Lock()
	defer s.mu.Unlock()

	return append([]time.Time(nil), s.gqlTimes...)
}

func (s *pollServer) restPaths() []string {
	s.mu.Lock()
	defer s.mu.Unlock()

	return append([]string(nil), s.restPath...)
}

func (s *pollServer) node(n int) string { return s.nodeIn(n, "OPEN") }

func (s *pollServer) nodeIn(n int, prState string) string {
	counts := `{"state":"SUCCESS","count":2}`
	state := "SUCCESS"

	if int64(n) == s.pendingPR.Load() {
		counts = `{"state":"IN_PROGRESS","count":1},{"state":"SUCCESS","count":1}`
		state = "PENDING"
	}

	return fmt.Sprintf(`{"number":%d,"title":"pr","url":"u","state":%q,"headRefName":"b%d",
"headRefOid":"aaa%d","baseRefName":"main","updatedAt":"2026-10-27T09:30:00Z",
"commits":{"nodes":[{"commit":{"statusCheckRollup":{"state":%q,"contexts":{
"checkRunCount":2,"checkRunCountsByState":[%s],"statusContextCount":0,"statusContextCountsByState":[]}}}}]}}`,
		n, prState, n, n, state, counts)
}

func (s *pollServer) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	// The server only notices a closed connection once the body is read.
	body, _ := io.ReadAll(r.Body)
	r.Body = io.NopCloser(bytes.NewReader(body))

	w.Header().Set("Content-Type", "application/json")

	if strings.Contains(r.URL.Path, "graphql") {
		n := s.gql.Add(1)

		s.mu.Lock()
		s.gqlTimes = append(s.gqlTimes, time.Now())
		s.mu.Unlock()

		if s.handle != nil && s.handle(n, w, r) {
			return
		}

		if bytes.Contains(body, []byte("pullRequest(number")) {
			s.single.Add(1)
			s.serveSingle(w, body)

			return
		}

		fmt.Fprintf(w, `{"data":{"repository":{"defaultBranchRef":{"name":"main"},
"mergeCommitAllowed":true,"squashMergeAllowed":false,"rebaseMergeAllowed":false,"deleteBranchOnMerge":false,
"pullRequests":{"nodes":[%s,%s]}},"rateLimit":{"remaining":4999,"resetAt":"2026-10-27T10:00:00Z"}}}`,
			s.node(1), s.node(2))

		return
	}

	n := s.rest.Add(1)

	s.mu.Lock()
	s.restPath = append(s.restPath, r.URL.Path)
	s.mu.Unlock()

	if s.restHandle != nil && s.restHandle(n, w, r) {
		return
	}

	if strings.HasSuffix(r.URL.Path, "/status") {
		fmt.Fprint(w, `{"state":"success","statuses":[]}`)

		return
	}

	fmt.Fprint(w, `{"total_count":1,"check_runs":[{"id":1,"name":"ci","status":"completed","conclusion":"success",
"html_url":"h","details_url":"d"}]}`)
}

func (s *pollServer) serveSingle(w http.ResponseWriter, body []byte) {
	var req struct {
		Variables struct {
			Number int `json:"number"`
		} `json:"variables"`
	}

	_ = json.Unmarshal(body, &req)

	state, ok := s.past[req.Variables.Number]

	switch {
	case s.singleFails:
		fmt.Fprint(w, `{"errors":[{"type":"INTERNAL","message":"boom"}]}`)
	case !ok:
		fmt.Fprint(w, `{"data":{"repository":{"pullRequest":null}},"errors":[{"type":"NOT_FOUND","path":["repository","pullRequest"],"message":"Could not resolve to a PullRequest."}]}`)
	default:
		fmt.Fprintf(w, `{"data":{"repository":{"pullRequest":%s}}}`, s.nodeIn(req.Variables.Number, state))
	}
}

// collector drains Updates in a goroutine it owns.
type collector struct {
	t    *testing.T
	done chan struct{}

	mu    sync.Mutex
	snaps []Snapshot
}

// startPoller builds a poller for srv, drains its updates and closes it when
// the test ends.
func startPoller(t *testing.T, srv *pollServer, o PollerOptions, each func(*Poller, Snapshot)) (*Poller, *collector) {
	t.Helper()

	if o.Client == nil {
		c, _ := newTestClient(t, "github.com", srv)
		o.Client = func(context.Context, string) (*Client, error) { return c, nil }
	}

	if o.Fast == 0 {
		o.Fast = testFast
	}

	if o.Slow == 0 {
		o.Slow = idle
	}

	if o.MaxBackoff == 0 {
		o.MaxBackoff = time.Second
	}

	p := NewPoller(o)

	col := &collector{t: t, done: make(chan struct{})}

	go func() {
		defer close(col.done)

		for s := range p.Updates() {
			col.mu.Lock()
			col.snaps = append(col.snaps, s)
			col.mu.Unlock()

			if each != nil {
				each(p, s)
			}
		}
	}()

	t.Cleanup(func() {
		_ = p.Close()

		select {
		case <-col.done:
		case <-time.After(5 * time.Second):
			t.Error("Updates was not closed after Close")
		}
	})

	return p, col
}

func (c *collector) all() []Snapshot {
	c.mu.Lock()
	defer c.mu.Unlock()

	return append([]Snapshot(nil), c.snaps...)
}

// forRepo returns the snapshots of one repository.
func (c *collector) forRepo(r Repo) []Snapshot {
	var out []Snapshot

	for _, s := range c.all() {
		if s.Repo == r {
			out = append(out, s)
		}
	}

	return out
}

// waitFor polls until pred holds for the snapshots seen so far and returns
// the first one that matched, failing the test after d.
func (c *collector) waitFor(d time.Duration, desc string, pred func(Snapshot) bool) Snapshot {
	c.t.Helper()

	deadline := time.Now().Add(d)

	for {
		for _, s := range c.all() {
			if pred(s) {
				return s
			}
		}

		if time.Now().After(deadline) {
			c.t.Fatalf("no snapshot within %v: %s (saw %d)", d, desc, len(c.all()))
		}

		time.Sleep(5 * time.Millisecond)
	}
}

func (c *collector) waitCount(d time.Duration, n int) {
	c.t.Helper()

	deadline := time.Now().Add(d)
	for len(c.all()) < n {
		if time.Now().After(deadline) {
			c.t.Fatalf("saw %d snapshots within %v, want at least %d", len(c.all()), d, n)
		}

		time.Sleep(5 * time.Millisecond)
	}
}

func anySnap(Snapshot) bool { return true }

// snapSignal returns an each callback for startPoller and the channel that
// receives every snapshot it sees, so a test waits on the event itself.
func snapSignal() (chan Snapshot, func(*Poller, Snapshot)) {
	seen := make(chan Snapshot, 64)

	return seen, func(_ *Poller, s Snapshot) { seen <- s }
}

// awaitSnap returns the next snapshot on seen; the timeout only fires when
// the poller has stalled.
func awaitSnap(t *testing.T, seen <-chan Snapshot) Snapshot {
	t.Helper()

	select {
	case s := <-seen:
		return s
	case <-time.After(5 * time.Second):
		t.Fatal("no snapshot arrived")
	}

	return Snapshot{}
}

// fakeClock is a poller clock that stands still: each wait the poller asks for
// arrives on asks and ends only when the test ends it. A test that leaves more
// than asks holds unread blocks the poller.
type fakeClock struct {
	at   time.Time
	asks chan ask
}

type ask struct {
	d    time.Duration
	fire chan time.Time
}

func newFakeClock() *fakeClock {
	return &fakeClock{at: time.Date(2026, 10, 27, 9, 0, 0, 0, time.UTC), asks: make(chan ask, 16)}
}

// options returns o running on the clock.
func (c *fakeClock) options(o PollerOptions) PollerOptions {
	o.now = func() time.Time { return c.at }
	o.after = func(d time.Duration) <-chan time.Time {
		a := ask{d: d, fire: make(chan time.Time, 1)}
		c.asks <- a

		return a.fire
	}

	return o
}

// next returns the next wait the poller asks for.
func (c *fakeClock) next(t *testing.T) ask {
	t.Helper()

	select {
	case a := <-c.asks:
		return a
	case <-time.After(5 * time.Second):
		t.Fatal("the poller asked for no wait")
	}

	return ask{}
}

func (a ask) end() { a.fire <- time.Time{} }

// rateLimited answers the first request with an exhausted rate limit that
// resets at reset.
func rateLimited(reset time.Time) func(int64, http.ResponseWriter, *http.Request) bool {
	return func(n int64, w http.ResponseWriter, _ *http.Request) bool {
		if n != 1 {
			return false
		}

		w.Header().Set("X-RateLimit-Remaining", "0")
		w.Header().Set("X-RateLimit-Reset", strconv.FormatInt(reset.Unix(), 10))
		w.WriteHeader(http.StatusForbidden)
		fmt.Fprint(w, `{"message":"API rate limit exceeded"}`)

		return true
	}
}

func TestPollerPollsOnSet(t *testing.T) {
	srv := &pollServer{}
	p, col := startPoller(t, srv, PollerOptions{}, nil)

	p.Set([]Watch{{Repo: repoA}})

	s := col.waitFor(5*time.Second, "first poll", anySnap)
	if s.Err != nil {
		t.Fatalf("Err = %v", s.Err)
	}

	if s.Repo != repoA || len(s.PRs) != 2 || s.Info.DefaultBranch != "main" || s.RateLimit.Remaining != 4999 || s.At.IsZero() {
		t.Errorf("snapshot = %+v", s)
	}

	if s.Focused != nil {
		t.Errorf("Focused = %+v, want nil without a focused PR", s.Focused)
	}
}

func TestPollerSlowOtherwise(t *testing.T) {
	tests := []struct {
		name    string
		pending int64
		visible []int
		focused int
		want    time.Duration
	}{
		{"nothing pending", 0, []int{1, 2}, 0, testSlow},
		{"pending PR is not visible", 2, []int{1}, 0, testSlow},
		{"pending PR is visible", 1, []int{1}, 0, testFast},
		{"pending PR is focused", 2, nil, 2, testFast},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			clk := newFakeClock()
			srv := &pollServer{}
			srv.pendingPR.Store(tt.pending)

			p, _ := startPoller(t, srv, clk.options(PollerOptions{Slow: testSlow}), nil)

			p.Set([]Watch{{Repo: repoA, Visible: tt.visible, Focused: tt.focused}})
			clk.next(t).end()

			if a := clk.next(t); a.d != tt.want {
				t.Fatalf("waits %v after the poll, want %v", a.d, tt.want)
			}
		})
	}
}

func TestPollerFocusedFetchesChecks(t *testing.T) {
	srv := &pollServer{}
	p, col := startPoller(t, srv, PollerOptions{}, nil)

	p.Set([]Watch{{Repo: repoA, Focused: 2}})

	s := col.waitFor(5*time.Second, "focused poll", anySnap)
	if s.Err != nil {
		t.Fatalf("Err = %v", s.Err)
	}

	if s.Focused == nil || s.Focused.Number != 2 || s.Focused.HeadOID != "aaa2" || len(s.Focused.Checks) != 1 {
		t.Fatalf("Focused = %+v", s.Focused)
	}

	var sawHead bool

	for _, p := range srv.restPaths() {
		sawHead = sawHead || strings.Contains(p, "/commits/aaa2/")
	}

	if !sawHead {
		t.Errorf("no checks request for the focused head: %v", srv.restPaths())
	}
}

func TestPollerFocusedMissingPR(t *testing.T) {
	srv := &pollServer{}
	p, col := startPoller(t, srv, PollerOptions{}, nil)

	p.Set([]Watch{{Repo: repoA, Focused: 99}})

	s := col.waitFor(5*time.Second, "poll", anySnap)
	if s.Err != nil || s.Focused != nil || len(s.PRs) != 2 {
		t.Errorf("snapshot = %+v, want PRs and no focused checks", s)
	}

	if n := srv.rest.Load(); n != 0 {
		t.Errorf("%d checks requests for a PR that is not open", n)
	}
}

func TestPollerFocusedPastFirstPage(t *testing.T) {
	srv := &pollServer{past: map[int]string{150: "OPEN"}}
	p, col := startPoller(t, srv, PollerOptions{}, nil)

	p.Set([]Watch{{Repo: repoA, Focused: 150}})

	s := col.waitFor(5*time.Second, "poll", anySnap)
	if s.Err != nil || len(s.PRs) != 3 || s.PRs[2].Number != 150 {
		t.Fatalf("snapshot = %+v, want the 2 listed and #150", s)
	}

	if s.Focused == nil || s.Focused.Number != 150 || s.Focused.HeadOID != "aaa150" || len(s.Focused.Checks) != 1 {
		t.Fatalf("Focused = %+v", s.Focused)
	}

	var sawHead bool

	for _, p := range srv.restPaths() {
		sawHead = sawHead || strings.Contains(p, "/commits/aaa150/")
	}

	if !sawHead {
		t.Errorf("no checks request for the focused head: %v", srv.restPaths())
	}
}

func TestPollerFocusedOnFirstPageAsksNothingMore(t *testing.T) {
	srv := &pollServer{}
	p, col := startPoller(t, srv, PollerOptions{}, nil)

	p.Set([]Watch{{Repo: repoA, Focused: 2}})
	col.waitFor(5*time.Second, "poll", anySnap)

	if n := srv.single.Load(); n != 0 {
		t.Errorf("%d by-number queries for a PR on the first page", n)
	}
}

func TestPollerFocusedPastFirstPageNotOpen(t *testing.T) {
	srv := &pollServer{past: map[int]string{150: "MERGED"}}
	p, col := startPoller(t, srv, PollerOptions{}, nil)

	p.Set([]Watch{{Repo: repoA, Focused: 150}})

	s := col.waitFor(5*time.Second, "poll", anySnap)
	if s.Err != nil || s.Focused != nil || len(s.PRs) != 2 {
		t.Errorf("snapshot = %+v, want the listed PRs and no focused checks", s)
	}

	if n := srv.rest.Load(); n != 0 {
		t.Errorf("%d checks requests for a PR that is not open", n)
	}
}

func TestPollerFocusedQueryFailureKeepsThePoll(t *testing.T) {
	srv := &pollServer{singleFails: true}
	p, col := startPoller(t, srv, PollerOptions{}, nil)

	p.Set([]Watch{{Repo: repoA, Focused: 150}})

	s := col.waitFor(5*time.Second, "poll", anySnap)
	if s.Err != nil || len(s.PRs) != 2 {
		t.Fatalf("snapshot = %+v, want the poll kept", s)
	}

	if s.Focused == nil || s.Focused.Number != 150 || s.Focused.Err == nil {
		t.Errorf("Focused = %+v, want #150 with its error", s.Focused)
	}
}

func TestPollerSnapshotSaysWhatItAsked(t *testing.T) {
	for name, fail := range map[string]bool{"good": false, "failed": true} {
		t.Run(name, func(t *testing.T) {
			srv := &pollServer{}
			if fail {
				srv.handle = func(_ int64, w http.ResponseWriter, _ *http.Request) bool {
					fmt.Fprint(w, `{"errors":[{"type":"INTERNAL","message":"boom"}]}`)

					return true
				}
			}

			p, col := startPoller(t, srv, PollerOptions{}, nil)
			p.Set([]Watch{{Repo: repoA, Focused: 2}})

			s := col.waitFor(5*time.Second, "poll", anySnap)
			if (s.Err != nil) != fail || s.Asked != 2 {
				t.Errorf("Err = %v, Asked = %d, want Asked 2", s.Err, s.Asked)
			}
		})
	}
}

func TestPollerFocusChangePollsNow(t *testing.T) {
	srv := &pollServer{}
	p, col := startPoller(t, srv, PollerOptions{}, nil)

	p.Set([]Watch{{Repo: repoA}})
	col.waitCount(5*time.Second, 1)

	p.Set([]Watch{{Repo: repoA, Focused: 2}})

	col.waitFor(5*time.Second, "focused snapshot", func(s Snapshot) bool {
		return s.Focused != nil && s.Focused.Number == 2
	})
}

func TestPollerUnreadableChecksKeepThePoll(t *testing.T) {
	t.Parallel()

	clk := newFakeClock()
	srv := &pollServer{}
	srv.restHandle = func(n int64, w http.ResponseWriter, _ *http.Request) bool {
		if n != 1 {
			return false
		}

		w.WriteHeader(http.StatusForbidden)
		fmt.Fprint(w, `{"message":"Resource not accessible by personal access token"}`)

		return true
	}

	p, col := startPoller(t, srv, clk.options(PollerOptions{Slow: testSlow}), nil)

	p.Set([]Watch{{Repo: repoA, Focused: 2}})
	clk.next(t).end()

	s := col.waitFor(5*time.Second, "poll", anySnap)
	if s.Err != nil || len(s.PRs) != 2 {
		t.Fatalf("Err = %v with %d PRs, want the poll kept", s.Err, len(s.PRs))
	}

	f := s.Focused
	if f == nil || f.Number != 2 || f.HeadOID != "aaa2" || len(f.Checks) != 0 || f.Err == nil {
		t.Fatalf("Focused = %+v, want PR 2 at aaa2 with the error and no checks", f)
	}

	if he, ok := errors.AsType[*api.HTTPError](f.Err); !ok || he.StatusCode != http.StatusForbidden {
		t.Errorf("Focused.Err = %v, want the 403", f.Err)
	}

	for _, secret := range []string{testToken, "http://", "https://"} {
		if strings.Contains(f.Err.Error(), secret) {
			t.Errorf("Focused.Err %q holds %q", f.Err, secret)
		}
	}

	a := clk.next(t)
	if a.d != testSlow {
		t.Fatalf("waits %v after the poll, want Slow (%v): unreadable checks fail no poll", a.d, testSlow)
	}

	a.end()

	s = col.waitFor(5*time.Second, "poll with readable checks", func(s Snapshot) bool {
		return s.Focused != nil && s.Focused.Err == nil
	})
	if len(s.Focused.Checks) != 1 {
		t.Errorf("Focused = %+v, want the check read again", s.Focused)
	}
}

func TestPollerRateLimitedChecksWaitForTheReset(t *testing.T) {
	t.Parallel()

	clk := newFakeClock()
	reset := clk.at.Add(90 * time.Second)
	srv := &pollServer{}
	srv.restHandle = func(n int64, w http.ResponseWriter, _ *http.Request) bool {
		if n != 1 {
			return false
		}

		w.Header().Set("X-RateLimit-Remaining", "0")
		w.Header().Set("X-RateLimit-Reset", strconv.FormatInt(reset.Unix(), 10))
		w.WriteHeader(http.StatusForbidden)
		fmt.Fprint(w, `{"message":"API rate limit exceeded"}`)

		return true
	}

	p, col := startPoller(t, srv, clk.options(PollerOptions{}), nil)

	p.Set([]Watch{{Repo: repoA, Focused: 2}})
	clk.next(t).end()

	s := col.waitFor(5*time.Second, "poll", anySnap)
	if s.Err != nil || len(s.PRs) != 2 {
		t.Fatalf("Err = %v with %d PRs, want the poll kept", s.Err, len(s.PRs))
	}

	if s.Focused == nil || !errors.Is(s.Focused.Err, ErrRateLimited) {
		t.Fatalf("Focused = %+v, want the rate limit", s.Focused)
	}

	if a := clk.next(t); a.d != 90*time.Second {
		t.Fatalf("waits %v after the poll, want the 90s to the reset", a.d)
	}

	// The scheduled poll never comes; a refresh still waits for the reset.
	p.Refresh(repoA)

	a := clk.next(t)
	if a.d != 90*time.Second {
		t.Fatalf("a refresh waits %v, want the 90s to the reset", a.d)
	}

	if n := srv.rest.Load(); n != 1 {
		t.Fatalf("%d checks requests before the reset, want 1", n)
	}

	a.end()
	col.waitFor(5*time.Second, "poll after the reset", func(s Snapshot) bool {
		return s.Focused != nil && s.Focused.Err == nil
	})
}

func TestPollerRemovedRepoStops(t *testing.T) {
	clk := newFakeClock()
	srv := &pollServer{}
	seen, each := snapSignal()

	p, col := startPoller(t, srv, clk.options(PollerOptions{}), each)

	p.Set([]Watch{{Repo: repoA}})
	clk.next(t).end()
	awaitSnap(t, seen)

	aWait := clk.next(t)

	p.Set([]Watch{{Repo: repoA}, {Repo: repoB}})
	clk.next(t).end()

	if got := awaitSnap(t, seen).Repo; got != repoB {
		t.Fatalf("second snapshot is for %v, want %v", got, repoB)
	}

	bWait := clk.next(t)

	p.Set([]Watch{{Repo: repoB}})

	// Both waits end; only the remaining repository may poll again.
	aWait.end()
	bWait.end()

	for range 3 {
		if got := awaitSnap(t, seen).Repo; got != repoB {
			t.Fatalf("a poll of %v after it was removed", got)
		}

		clk.next(t).end()
	}

	if n := len(col.forRepo(repoA)); n != 1 {
		t.Errorf("removed repository has %d snapshots, want only its first", n)
	}
}

func TestPollerRateLimitWaitsForReset(t *testing.T) {
	clk := newFakeClock()
	srv := &pollServer{}
	srv.handle = rateLimited(clk.at.Add(90 * time.Second))

	p, col := startPoller(t, srv, clk.options(PollerOptions{}), nil)

	p.Set([]Watch{{Repo: repoA}})
	clk.next(t).end()

	s := col.waitFor(5*time.Second, "rate limited snapshot", anySnap)
	if !errors.Is(s.Err, ErrRateLimited) {
		t.Fatalf("Err = %v, want a rate limit", s.Err)
	}

	if len(s.PRs) != 0 || s.Focused != nil {
		t.Errorf("a failed poll carries data: %+v", s)
	}

	a := clk.next(t)
	if a.d != 90*time.Second {
		t.Fatalf("waits %v after the rate limit, want the 90s to its reset", a.d)
	}

	if n := len(srv.arrivals()); n != 1 {
		t.Fatalf("%d requests before the reset, want 1", n)
	}

	a.end()
	col.waitFor(5*time.Second, "poll after the reset", func(s Snapshot) bool { return s.Err == nil })
}

func TestPollerErrorBacksOff(t *testing.T) {
	srv := &pollServer{}
	srv.handle = func(_ int64, w http.ResponseWriter, _ *http.Request) bool {
		w.Header().Set("Content-Type", "text/html")
		w.WriteHeader(http.StatusBadGateway)
		fmt.Fprint(w, "<html>bad gateway</html>")

		return true
	}

	clk := newFakeClock()
	p, col := startPoller(t, srv, clk.options(PollerOptions{MaxBackoff: 100 * time.Millisecond}), nil)

	p.Set([]Watch{{Repo: repoA}})
	clk.next(t).end()

	for i, want := range []time.Duration{testFast, 2 * testFast} {
		a := clk.next(t)
		if a.d != want {
			t.Fatalf("wait %d after a failure = %v, want %v", i+1, a.d, want)
		}

		a.end()
	}

	for _, s := range col.all() {
		if s.Err == nil {
			t.Fatal("a failing server gave a snapshot without Err")
		}
	}
}

func TestNextWait(t *testing.T) {
	const (
		fast    = 20 * time.Millisecond
		slow    = 200 * time.Millisecond
		maxWait = 100 * time.Millisecond
	)

	now := time.Date(2026, 10, 27, 9, 0, 0, 0, time.UTC)
	fail := Snapshot{Err: errors.New("boom")}
	ok := Snapshot{}
	limited := func(d time.Duration) Snapshot {
		return Snapshot{Err: fmt.Errorf("poll: %w", &RateLimitError{Reset: now.Add(d)})}
	}
	unreadable := Snapshot{Focused: &FocusedChecks{Err: errors.New("HTTP 403")}}
	checksLimited := func(d time.Duration) Snapshot {
		return Snapshot{Focused: &FocusedChecks{Err: fmt.Errorf("checks: %w", &RateLimitError{Reset: now.Add(d)})}}
	}

	type step struct {
		snap    Snapshot
		pending bool
		want    time.Duration
	}

	tests := []struct {
		name  string
		steps []step
	}{
		{"errors double up to the cap", []step{
			{fail, false, fast},
			{fail, false, 2 * fast},
			{fail, false, 4 * fast},
			{fail, false, maxWait},
			{fail, false, maxWait},
		}},
		{"a success resets the backoff", []step{
			{fail, false, fast},
			{fail, false, 2 * fast},
			{fail, false, 4 * fast},
			{ok, true, fast},
			{fail, false, fast},
		}},
		{"cadence follows pending checks", []step{
			{ok, true, fast}, {ok, false, slow}, {ok, true, fast},
		}},
		{"a rate limit waits for the reset", []step{
			{limited(50 * time.Millisecond), false, 50 * time.Millisecond},
		}},
		{"a rate limit waits at least Fast", []step{
			{limited(time.Millisecond), false, fast}, {limited(-time.Minute), false, fast},
		}},
		{"a rate limit waits at most an hour", []step{
			{limited(3 * time.Hour), false, time.Hour},
		}},
		{"a rate limit leaves the error backoff alone", []step{
			{fail, false, fast},
			{fail, false, 2 * fast},
			{limited(time.Second), false, time.Second},
			{fail, false, 4 * fast},
		}},
		{"unreadable checks keep the cadence and reset the backoff", []step{
			{fail, false, fast},
			{unreadable, true, fast},
			{unreadable, false, slow},
			{fail, false, fast},
		}},
		{"rate-limited checks wait for the reset and reset the backoff", []step{
			{fail, false, fast},
			{fail, false, 2 * fast},
			{checksLimited(time.Second), true, time.Second},
			{fail, false, fast},
		}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := NewPoller(PollerOptions{Fast: fast, Slow: slow, MaxBackoff: maxWait})

			var failures int

			for i, st := range tt.steps {
				if got := p.nextWait(st.snap, st.pending, &failures, now); got != st.want {
					t.Errorf("step %d: wait = %v, want %v", i, got, st.want)
				}
			}
		})
	}
}

func TestPollerClientErrorIsReported(t *testing.T) {
	clk := newFakeClock()
	srv := &pollServer{}
	seen, each := snapSignal()

	var calls atomic.Int64

	p, _ := startPoller(t, srv, clk.options(PollerOptions{
		Client: func(context.Context, string) (*Client, error) {
			calls.Add(1)

			return nil, fmt.Errorf("no login: %w", ErrNoToken)
		},
	}), each)

	p.Set([]Watch{{Repo: repoA}})
	clk.next(t).end()

	s := awaitSnap(t, seen)
	if !errors.Is(s.Err, ErrNoToken) {
		t.Fatalf("Err = %v, want ErrNoToken", s.Err)
	}

	if len(s.PRs) != 0 {
		t.Errorf("PRs = %v on a failed poll", s.PRs)
	}

	for range 2 {
		clk.next(t).end()
		awaitSnap(t, seen)
	}

	if n := calls.Load(); n != 3 {
		t.Errorf("factory called %d times, want it retried and not cached", n)
	}

	if srv.gql.Load() != 0 {
		t.Error("a request was sent without a client")
	}
}

func TestPollerRebuildsAClientItsHostRefused(t *testing.T) {
	t.Parallel()

	repoE := Repo{Host: "ghe.example.com", Owner: "octo", Name: "e"}

	tests := []struct {
		name   string
		status int
		builds int64 // of the first host's client
	}{
		{"a 401 rebuilds it", http.StatusUnauthorized, 2},
		{"a 403 keeps it", http.StatusForbidden, 1},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			first, other := &pollServer{}, &pollServer{}
			first.pendingPR.Store(1)
			other.pendingPR.Store(1)

			first.handle = func(n int64, w http.ResponseWriter, _ *http.Request) bool {
				if n != 1 {
					return false
				}

				w.WriteHeader(tc.status)
				fmt.Fprint(w, `{"message":"Bad credentials"}`)

				return true
			}

			firstClients := make([]*Client, 2)
			for i := range firstClients {
				firstClients[i], _ = newTestClient(t, repoA.Host, first)
			}

			otherClient, _ := newTestClient(t, repoE.Host, other)

			var firstBuilds, otherBuilds atomic.Int64

			p, col := startPoller(t, first, PollerOptions{
				Client: func(_ context.Context, host string) (*Client, error) {
					if host == repoE.Host {
						otherBuilds.Add(1)

						return otherClient, nil
					}

					n := firstBuilds.Add(1)

					return firstClients[min(n, 2)-1], nil
				},
			}, nil)

			p.Set([]Watch{{Repo: repoA, Visible: []int{1}}, {Repo: repoE, Visible: []int{1}}})

			col.waitFor(5*time.Second, "the first host's failed poll", func(s Snapshot) bool {
				return s.Repo == repoA && s.Err != nil
			})
			col.waitFor(5*time.Second, "a later good poll of the first host", func(s Snapshot) bool {
				return s.Repo == repoA && s.Err == nil
			})

			for len(col.forRepo(repoA)) < 4 || len(col.forRepo(repoE)) < 4 {
				col.waitCount(5*time.Second, len(col.all())+1)
			}

			if n := firstBuilds.Load(); n != tc.builds {
				t.Errorf("the first host's client was built %d times after a %d, want %d", n, tc.status, tc.builds)
			}

			if n := otherBuilds.Load(); n != 1 {
				t.Errorf("the other host's client was built %d times, want it kept", n)
			}
		})
	}
}

func TestPollerRefresh(t *testing.T) {
	clk := newFakeClock()
	srv := &pollServer{}
	seen, each := snapSignal()

	p, col := startPoller(t, srv, clk.options(PollerOptions{}), each)

	p.Set([]Watch{{Repo: repoA}})
	clk.next(t).end()
	awaitSnap(t, seen)

	p.Refresh(repoA)
	awaitSnap(t, seen)

	p.Refresh(repoB)
	p.Refresh(repoA)
	awaitSnap(t, seen)

	if got := srv.gql.Load(); got != 3 {
		t.Errorf("%d requests, want 3: the first poll and two refreshes of the watched repository", got)
	}

	for _, s := range col.all() {
		if s.Repo != repoA {
			t.Errorf("snapshot for %v, want only %v", s.Repo, repoA)
		}
	}
}

func TestPollerConsumerMayCallBack(t *testing.T) {
	srv := &pollServer{}
	srv.pendingPR.Store(1)

	each := func(p *Poller, s Snapshot) {
		p.Set([]Watch{{Repo: s.Repo, Visible: []int{1}}})
		p.Refresh(s.Repo)
	}

	p, col := startPoller(t, srv, PollerOptions{}, each)

	p.Set([]Watch{{Repo: repoA, Visible: []int{1}}})
	col.waitCount(5*time.Second, 20)
}

// newUnpooledClient is newTestClient without idle connections, whose
// goroutines would outlive the poller and blur a goroutine count.
func newUnpooledClient(t *testing.T, h http.Handler) *Client {
	t.Helper()

	ts := httptest.NewServer(h)
	t.Cleanup(ts.Close)

	target, err := url.Parse(ts.URL)
	if err != nil {
		t.Fatal(err)
	}

	rt := &routeTransport{t: t, host: "api.github.com", target: target, next: &http.Transport{DisableKeepAlives: true}}

	c, err := NewClient("github.com", Options{Token: testToken, Transport: rt})
	if err != nil {
		t.Fatal(err)
	}

	return c
}

// settledGoroutines reads NumGoroutine until two reads a short time apart
// agree, so goroutines of earlier tests have wound down.
func settledGoroutines() int {
	prev := -1

	for range 200 {
		n := runtime.NumGoroutine()
		if n == prev {
			return n
		}

		prev = n

		time.Sleep(20 * time.Millisecond)
	}

	return prev
}

func pollerGoroutines() bool {
	buf := make([]byte, 1<<20)

	return strings.Contains(string(buf[:runtime.Stack(buf, true)]), "(*Poller).run")
}

func TestPollerCloseStopsEverything(t *testing.T) {
	started := make(chan struct{}, 1)
	srv := &pollServer{}
	srv.handle = func(_ int64, _ http.ResponseWriter, r *http.Request) bool {
		select {
		case started <- struct{}{}:
		default:
		}

		<-r.Context().Done()

		return true
	}

	c := newUnpooledClient(t, srv)
	baseline := settledGoroutines()

	p := NewPoller(PollerOptions{
		Client: func(context.Context, string) (*Client, error) { return c, nil },
		Fast:   testFast, Slow: testSlow,
	})

	t.Cleanup(func() { _ = p.Close() })

	done := make(chan struct{})

	go func() {
		defer close(done)

		for range p.Updates() { //nolint:revive // drained until closed
		}
	}()

	p.Set([]Watch{{Repo: repoA}, {Repo: repoB}})

	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatal("no request started")
	}

	if err := p.Close(); err != nil {
		t.Fatal(err)
	}

	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("Updates was not closed")
	}

	if err := p.Close(); err != nil {
		t.Errorf("second Close = %v", err)
	}

	p.Set([]Watch{{Repo: repoA}})
	p.Refresh(repoA)

	var now int

	for deadline := time.Now().Add(3 * time.Second); time.Now().Before(deadline); time.Sleep(10 * time.Millisecond) {
		if now = runtime.NumGoroutine(); now <= baseline && !pollerGoroutines() {
			return
		}
	}

	buf := make([]byte, 1<<16)
	t.Errorf("%d goroutines after Close, baseline %d\n%s", now, baseline, buf[:runtime.Stack(buf, true)])
}

func TestPollerCloseWithoutConsumer(t *testing.T) {
	p := NewPoller(PollerOptions{
		Client: func(context.Context, string) (*Client, error) { return nil, ErrNoToken },
		Fast:   time.Millisecond, Slow: idle,
	})

	t.Cleanup(func() { _ = p.Close() })

	p.Set([]Watch{{Repo: repoA}, {Repo: repoB}})

	// Only Refresh drives the polls, so the buffer fills when they have all run.
	deadline := time.Now().Add(5 * time.Second)

	for len(p.Updates()) < updatesBuffer {
		if time.Now().After(deadline) {
			t.Fatalf("%d snapshots queued, want the buffer of %d", len(p.Updates()), updatesBuffer)
		}

		p.Refresh(repoA)
		p.Refresh(repoB)
		runtime.Gosched()
	}

	if n := len(p.Updates()); n != updatesBuffer {
		t.Fatalf("%d snapshots queued, want the buffer of %d full", n, updatesBuffer)
	}

	if err := p.Close(); err != nil {
		t.Fatal(err)
	}

	closed := make(chan struct{})

	go func() {
		defer close(closed)

		for range p.Updates() { //nolint:revive // drained until closed
		}
	}()

	select {
	case <-closed:
	case <-time.After(time.Second):
		t.Error("Updates was not closed")
	}
}

func TestPollerRaceSetRefreshClose(t *testing.T) {
	repos := []Repo{repoA, repoB, {Host: "github.com", Owner: "octo", Name: "c"}}

	for range 5 {
		p := NewPoller(PollerOptions{
			Client: func(context.Context, string) (*Client, error) { return nil, ErrNoToken },
			Fast:   time.Millisecond, Slow: time.Millisecond,
		})

		t.Cleanup(func() { _ = p.Close() })

		drained := make(chan struct{})

		go func() {
			defer close(drained)

			for range p.Updates() { //nolint:revive // drained until closed
			}
		}()

		var (
			workers sync.WaitGroup
			start   = make(chan struct{})
			stop    = make(chan struct{})
		)

		for g := range 4 {
			workers.Go(func() {
				<-start

				for i := 0; ; i++ {
					select {
					case <-stop:
						return
					default:
					}

					switch (g + i) % 3 {
					case 0:
						p.Set([]Watch{{Repo: repos[i%3], Visible: []int{1}, Focused: i % 3}})
					case 1:
						p.Refresh(repos[i%3])
					default:
						p.Set(nil)
					}
				}
			})
		}

		closeDone := make(chan struct{})

		go func() {
			defer close(closeDone)

			<-start

			_ = p.Close()
		}()

		close(start)

		select {
		case <-closeDone:
		case <-time.After(time.Second):
			t.Fatal("Close did not return within 1 s")
		}

		close(stop)
		workers.Wait()

		select {
		case <-drained:
		case <-time.After(time.Second):
			t.Fatal("Updates was not closed")
		}
	}
}
