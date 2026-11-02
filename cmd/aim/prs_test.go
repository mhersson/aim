package main

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/mhersson/aim/internal/github"
)

var testRepo = github.Repo{Host: "github.com", Owner: "o", Name: "r"}

// noClients refuses to build a client, so a run in a test never reaches GitHub.
func noClients(context.Context, string) (*github.Client, error) {
	return nil, errors.New("test: no GitHub client")
}

// offline is a transport that fails every request, so no test reaches GitHub.
type offline struct{ requests atomic.Int32 }

func (o *offline) RoundTrip(*http.Request) (*http.Response, error) {
	o.requests.Add(1)

	return nil, errors.New("offline")
}

func TestStartPRsBuildsThePollerWithTheFactory(t *testing.T) {
	var (
		mu    sync.Mutex
		hosts []string
	)

	failing := errors.New("stub: no client")

	p := startPRs(func(_ context.Context, host string) (*github.Client, error) {
		mu.Lock()
		defer mu.Unlock()

		hosts = append(hosts, host)

		return nil, failing
	})

	p.poller.Set([]github.Watch{{Repo: testRepo}})

	select {
	case s := <-p.poller.Updates():
		if !errors.Is(s.Err, failing) || s.Repo != testRepo {
			t.Errorf("snapshot = %+v, want the factory's error for %s", s, testRepo)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the poller never used the factory")
	}

	if err := p.Close(); err != nil {
		t.Errorf("Close() error = %v", err)
	}

	select {
	case _, ok := <-p.poller.Updates():
		if ok {
			t.Error("Updates delivered after Close")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Close left Updates open")
	}

	mu.Lock()
	defer mu.Unlock()

	if len(hosts) == 0 || hosts[0] != "github.com" {
		t.Errorf("factory hosts = %v, want github.com", hosts)
	}
}

func TestPRLookupSharesTheClientCache(t *testing.T) {
	var (
		built atomic.Int32
		net   offline
	)

	p := startPRs(func(_ context.Context, host string) (*github.Client, error) {
		built.Add(1)

		return github.NewClient(host, github.Options{Token: "test-token", Transport: &net})
	})

	t.Cleanup(func() { _ = p.Close() })

	for range 2 {
		if _, err := p.lookup(t.Context(), testRepo, "feat"); err == nil || !strings.Contains(err.Error(), "offline") {
			t.Fatalf("lookup error = %v, want the transport's", err)
		}
	}

	if n := built.Load(); n != 1 {
		t.Errorf("the factory built %d clients for one host, want 1", n)
	}

	if n := net.requests.Load(); n != 2 {
		t.Errorf("the transport saw %d requests, want 2", n)
	}
}

func TestPRLookupDoesNotCacheAFailedClient(t *testing.T) {
	var built atomic.Int32

	failing := errors.New("stub: no token")

	p := startPRs(func(context.Context, string) (*github.Client, error) {
		built.Add(1)

		return nil, failing
	})

	t.Cleanup(func() { _ = p.Close() })

	for range 2 {
		if _, err := p.lookup(t.Context(), testRepo, "feat"); !errors.Is(err, failing) {
			t.Fatalf("lookup error = %v, want the factory's", err)
		}
	}

	if n := built.Load(); n != 2 {
		t.Errorf("the factory ran %d times, want 2: a failure is not kept", n)
	}
}

// answering is a transport that answers every request with status.
type answering struct {
	status   int
	requests atomic.Int32
}

func (a *answering) RoundTrip(r *http.Request) (*http.Response, error) {
	a.requests.Add(1)

	return &http.Response{
		StatusCode: a.status, Header: http.Header{"Content-Type": {"application/json"}},
		Body: io.NopCloser(strings.NewReader(`{"message":"Bad credentials"}`)), Request: r,
	}, nil
}

func TestClientCacheRebuildsAClientItsHostRefused(t *testing.T) {
	t.Parallel()

	other := github.Repo{Host: "ghe.example.com", Owner: "o", Name: "r"}

	tests := []struct {
		name   string
		status int
		builds int
	}{
		{"a 401 rebuilds it", http.StatusUnauthorized, 2},
		{"a 403 keeps it", http.StatusForbidden, 1},
		{"a 500 keeps it", http.StatusInternalServerError, 1},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			var (
				mu     sync.Mutex
				builds = map[string]int{}
				net    offline
			)

			first := &answering{status: tc.status}

			p := startPRs(func(_ context.Context, host string) (*github.Client, error) {
				mu.Lock()
				builds[host]++
				mu.Unlock()

				rt := http.RoundTripper(&net)
				if host == testRepo.Host {
					rt = first
				}

				return github.NewClient(host, github.Options{Token: "test-token", Transport: rt})
			})

			t.Cleanup(func() { _ = p.Close() })

			for range 2 {
				for _, r := range []github.Repo{testRepo, other} {
					if _, err := p.lookup(t.Context(), r, "feat"); err == nil {
						t.Fatalf("lookup(%s) succeeded, want the transport's error", r)
					}
				}
			}

			mu.Lock()
			defer mu.Unlock()

			if n := builds[testRepo.Host]; n != tc.builds {
				t.Errorf("%s's client was built %d times after a %d, want %d", testRepo.Host, n, tc.status, tc.builds)
			}

			if n := builds[other.Host]; n != 1 {
				t.Errorf("%s's client was built %d times, want it kept", other.Host, n)
			}
		})
	}
}

func TestPollerRebuildsARefusedClientThroughTheCache(t *testing.T) {
	t.Parallel()

	var built atomic.Int32

	refusing := &answering{status: http.StatusUnauthorized}

	p := startPRs(func(_ context.Context, host string) (*github.Client, error) {
		built.Add(1)

		return github.NewClient(host, github.Options{Token: "test-token", Transport: refusing})
	})

	t.Cleanup(func() { _ = p.Close() })

	next := func() github.Snapshot {
		t.Helper()

		select {
		case s := <-p.poller.Updates():
			return s
		case <-time.After(5 * time.Second):
			t.Fatal("no poll")
		}

		return github.Snapshot{}
	}

	p.poller.Set([]github.Watch{{Repo: testRepo}})

	if s := next(); s.Err == nil {
		t.Fatal("a refused poll has no error")
	}

	p.poller.Refresh(testRepo)

	if s := next(); s.Err == nil {
		t.Fatal("a refused poll has no error")
	}

	if n := built.Load(); n != 2 {
		t.Errorf("the factory built %d clients, want a new one for the poll after the 401", n)
	}
}

type closeLog struct {
	mu     sync.Mutex
	events []string
}

func (l *closeLog) add(e string) {
	l.mu.Lock()
	defer l.mu.Unlock()

	l.events = append(l.events, e)
}

type stubApp struct {
	log *closeLog
	err error
}

func (a stubApp) Close(context.Context) error {
	a.log.add("app")

	return a.err
}

type stubPRs struct{ log *closeLog }

func (p stubPRs) Close() error {
	p.log.add("poller")

	return nil
}

func TestCloseRunClosesThePollerAfterTheApp(t *testing.T) {
	for _, appErr := range []error{nil, errors.New("close panes: deadline")} {
		var l closeLog

		closeRun(t.Context(), discardLogger(), stubApp{log: &l, err: appErr}, stubPRs{log: &l})

		if got := strings.Join(l.events, ","); got != "app,poller" {
			t.Errorf("closed %q with app error %v, want the app first and the poller after", got, appErr)
		}
	}
}

func TestGhActionsUseTheClientOfTheRepositorysHost(t *testing.T) {
	var (
		mu    sync.Mutex
		hosts []string
		net   offline
	)

	p := startPRs(func(_ context.Context, host string) (*github.Client, error) {
		mu.Lock()

		hosts = append(hosts, host)

		mu.Unlock()

		return github.NewClient(host, github.Options{Token: "test-token", Transport: &net})
	})

	t.Cleanup(func() { _ = p.Close() })

	actions := p.actions()
	other := github.Repo{Host: "ghe.example.com", Owner: "o", Name: "r"}

	for _, r := range []github.Repo{testRepo, other, other} {
		if err := actions.Approve(t.Context(), r, 7, "abc123"); err == nil || !strings.Contains(err.Error(), "offline") {
			t.Fatalf("Approve(%s) error = %v, want the transport's", r, err)
		}
	}

	mu.Lock()
	defer mu.Unlock()

	if got := strings.Join(hosts, ","); got != "github.com,ghe.example.com" {
		t.Errorf("clients built for %q, want one per host: github.com,ghe.example.com", got)
	}

	if n := net.requests.Load(); n != 3 {
		t.Errorf("the transport saw %d requests, want 3", n)
	}
}

func TestGhActionsReportAClientThatCannotBeBuilt(t *testing.T) {
	failing := errors.New("stub: no token")

	p := startPRs(func(context.Context, string) (*github.Client, error) { return nil, failing })

	t.Cleanup(func() { _ = p.Close() })

	if err := p.actions().Approve(t.Context(), testRepo, 7, "abc123"); !errors.Is(err, failing) {
		t.Errorf("Approve error = %v, want the factory's", err)
	}
}
