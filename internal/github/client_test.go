package github

import (
	"context"
	"errors"
	"io"
	"net/http"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/cli/go-gh/v2/pkg/api"
)

func okJSON(w http.ResponseWriter, body string) {
	w.Header().Set("Content-Type", "application/json")
	_, _ = io.WriteString(w, body)
}

func gqlOK(w http.ResponseWriter, _ *http.Request) {
	okJSON(w, `{"data":{"viewer":{"login":"me"}}}`)
}

type viewerQuery struct {
	Viewer struct{ Login string }
}

func TestClientRoutesGitHubCom(t *testing.T) {
	c, reqs := newTestClient(t, "github.com", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/graphql" {
			gqlOK(w, r)

			return
		}

		okJSON(w, `{}`)
	}))

	ctx := context.Background()
	if err := c.rest(ctx, http.MethodGet, "repos/o/r", nil, nil); err != nil {
		t.Fatal(err)
	}

	var q viewerQuery
	if err := c.query(ctx, "Viewer", &q, nil); err != nil {
		t.Fatal(err)
	}

	if q.Viewer.Login != "me" {
		t.Errorf("login = %q", q.Viewer.Login)
	}

	got := []string{reqs()[0].URL.String(), reqs()[1].URL.String()}
	want := []string{"https://api.github.com/repos/o/r", "https://api.github.com/graphql"}

	for i := range want {
		if got[i] != want[i] {
			t.Errorf("request %d URL = %s, want %s", i, got[i], want[i])
		}
	}
}

func TestClientRoutesGHE(t *testing.T) {
	c, reqs := newTestClient(t, "ghe.corp.example", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/graphql" {
			gqlOK(w, r)

			return
		}

		okJSON(w, `{}`)
	}))

	ctx := context.Background()
	if err := c.rest(ctx, http.MethodGet, "repos/o/r", nil, nil); err != nil {
		t.Fatal(err)
	}

	var q viewerQuery
	if err := c.query(ctx, "Viewer", &q, nil); err != nil {
		t.Fatal(err)
	}

	got := []string{reqs()[0].URL.String(), reqs()[1].URL.String()}
	want := []string{
		"https://ghe.corp.example/api/v3/repos/o/r",
		"https://ghe.corp.example/api/graphql",
	}

	for i := range want {
		if got[i] != want[i] {
			t.Errorf("request %d URL = %s, want %s", i, got[i], want[i])
		}
	}
}

func TestClientSendsToken(t *testing.T) {
	c, reqs := newTestClient(t, "github.com", http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		okJSON(w, `{}`)
	}))

	if err := c.rest(context.Background(), http.MethodGet, "repos/o/r", nil, nil); err != nil {
		t.Fatal(err)
	}

	if got := reqs()[0].Header.Get("Authorization"); got != "token "+testToken {
		t.Errorf("Authorization header has the wrong form or token")
	}
}

func TestNewClientRequiresToken(t *testing.T) {
	_, err := NewClient("github.com", Options{})
	if !errors.Is(err, ErrNoToken) {
		t.Fatalf("err = %v, want ErrNoToken", err)
	}
}

func TestNewClientRefusesAnEmptyHost(t *testing.T) {
	_, err := NewClient("", Options{Token: testToken})
	if !errors.Is(err, ErrNotGitHub) {
		t.Fatalf("err = %v, want ErrNotGitHub", err)
	}
}

// Not parallel: it swaps os.Stderr and sets the environment.
func TestClientPrintsNothingWithGHDebug(t *testing.T) {
	t.Setenv("GH_DEBUG", "api")

	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}

	orig := os.Stderr
	os.Stderr = w

	t.Cleanup(func() { os.Stderr = orig })

	c, _ := newTestClient(t, "github.com", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/graphql" {
			gqlOK(w, r)

			return
		}

		okJSON(w, `{}`)
	}))

	ctx := context.Background()
	if err := c.rest(ctx, http.MethodGet, "repos/o/r", nil, nil); err != nil {
		t.Fatal(err)
	}

	var q viewerQuery
	if err := c.query(ctx, "Viewer", &q, nil); err != nil {
		t.Fatal(err)
	}

	os.Stderr = orig

	if err := w.Close(); err != nil {
		t.Fatal(err)
	}

	out, err := io.ReadAll(r)
	if err != nil {
		t.Fatal(err)
	}

	if len(out) != 0 {
		t.Errorf("client wrote %d bytes to stderr", len(out))
	}
}

func TestClientConcurrentGETs(t *testing.T) {
	c, _ := newTestClient(t, "github.com", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("If-None-Match") == `"v1"` {
			w.WriteHeader(http.StatusNotModified)

			return
		}

		w.Header().Set("ETag", `"v1"`)
		okJSON(w, `{"n":1}`)
	}))

	var wg sync.WaitGroup

	for i := range 32 {
		wg.Go(func() {
			var out struct{ N int }

			path := "repos/o/r/a"
			if i%2 == 0 {
				path = "repos/o/r/b"
			}

			if err := c.rest(context.Background(), http.MethodGet, path, nil, &out); err != nil {
				t.Error(err)

				return
			}

			if out.N != 1 {
				t.Errorf("n = %d", out.N)
			}
		})
	}

	wg.Wait()
}

func TestRESTBodyAndNoContent(t *testing.T) {
	var got string

	c, _ := newTestClient(t, "github.com", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		got = string(b)

		w.WriteHeader(http.StatusNoContent)
	}))

	err := c.rest(context.Background(), http.MethodPost, "repos/o/r/x", map[string]string{"a": "b"}, nil)
	if err != nil {
		t.Fatal(err)
	}

	if got != `{"a":"b"}` {
		t.Errorf("body = %s", got)
	}
}

func TestGraphQLHTTPErrors(t *testing.T) {
	tests := []struct {
		name   string
		status int
		header map[string]string
		rate   bool
	}{
		{"forbidden with exhausted limit", 403, map[string]string{"X-RateLimit-Remaining": "0", "X-RateLimit-Reset": "4102444800"}, true},
		{"too many requests", 429, map[string]string{"Retry-After": "30"}, true},
		{"bad gateway", 502, nil, false},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			c, _ := newTestClient(t, "github.com", http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				for k, v := range tc.header {
					w.Header().Set(k, v)
				}

				w.Header().Set("Content-Type", "text/html")
				w.WriteHeader(tc.status)
				_, _ = io.WriteString(w, "<html>oops</html>")
			}))

			var q viewerQuery

			err := c.query(context.Background(), "Viewer", &q, nil)
			if err == nil {
				t.Fatal("no error")
			}

			if got := errors.Is(err, ErrRateLimited); got != tc.rate {
				t.Errorf("rate limited = %v, want %v (%v)", got, tc.rate, err)
			}

			var he *api.HTTPError
			if !errors.As(err, &he) || he.StatusCode != tc.status {
				t.Errorf("no *api.HTTPError with status %d in %v", tc.status, err)
			}

			if strings.Contains(err.Error(), testToken) {
				t.Error("error carries the token")
			}
		})
	}
}

func TestClientTokenRefused(t *testing.T) {
	t.Parallel()

	rest := func(c *Client) error { return c.rest(context.Background(), http.MethodGet, "user", nil, nil) }
	gql := func(c *Client) error {
		var q viewerQuery

		return c.query(context.Background(), "Viewer", &q, nil)
	}

	tests := []struct {
		name    string
		call    func(*Client) error
		status  int
		refused bool
	}{
		{"a 401 to a REST call", rest, http.StatusUnauthorized, true},
		{"a 401 to a GraphQL query", gql, http.StatusUnauthorized, true},
		{"a 403 to a REST call", rest, http.StatusForbidden, false},
		{"a 403 to a GraphQL query", gql, http.StatusForbidden, false},
		{"a 500 to a REST call", rest, http.StatusInternalServerError, false},
		{"a 502 to a GraphQL query", gql, http.StatusBadGateway, false},
		{"a success", rest, http.StatusOK, false},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			c, _ := newTestClient(t, "github.com", http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(tc.status)
				_, _ = io.WriteString(w, `{"message":"Bad credentials"}`)
			}))

			if c.TokenRefused() {
				t.Fatal("a new client is refused")
			}

			if err := tc.call(c); (err != nil) != (tc.status != http.StatusOK) {
				t.Fatalf("call error = %v for a %d", err, tc.status)
			}

			if got := c.TokenRefused(); got != tc.refused {
				t.Errorf("TokenRefused() = %v after a %d, want %v", got, tc.status, tc.refused)
			}
		})
	}
}

func TestGraphQLRateLimitWaitsForItsReset(t *testing.T) {
	const body = `{"data":null,"errors":[{"type":"RATE_LIMITED","message":"API rate limit exceeded"}]}`

	tests := []struct {
		name   string
		header map[string]string
		check  func(reset, before, after time.Time) bool
	}{
		{
			"reset header",
			map[string]string{"X-RateLimit-Remaining": "0", "X-RateLimit-Reset": "4102444800"},
			func(r, _, _ time.Time) bool { return r.Unix() == 4102444800 },
		},
		{
			"no headers", nil,
			func(r, before, after time.Time) bool { return within(r, before, after, time.Minute) },
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			c, _ := newTestClient(t, "github.com", http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				for k, v := range tc.header {
					w.Header().Set(k, v)
				}

				okJSON(w, body)
			}))

			before := time.Now()
			err := c.query(context.Background(), "Viewer", new(viewerQuery), nil)
			after := time.Now()

			var rl *RateLimitError
			if !errors.As(err, &rl) || !errors.Is(err, ErrRateLimited) {
				t.Fatalf("err = %v, want a *RateLimitError", err)
			}

			if !tc.check(rl.Reset, before, after) {
				t.Errorf("reset = %v, call from %v to %v", rl.Reset, before, after)
			}

			if _, ok := errors.AsType[*api.GraphQLError](err); !ok {
				t.Error("original *api.GraphQLError lost")
			}
		})
	}
}

func TestErrorsCarryNoToken(t *testing.T) {
	c, _ := newTestClient(t, "github.com", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/graphql" {
			okJSON(w, `{"errors":[{"type":"NOT_FOUND","message":"gone"}]}`)

			return
		}

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = io.WriteString(w, `{"message":"boom"}`)
	}))

	ctx := context.Background()
	errs := map[string]error{
		"rest":    c.rest(ctx, http.MethodGet, "repos/o/r", nil, nil),
		"graphql": c.query(ctx, "Failing", new(viewerQuery), map[string]any{"n": 1}),
	}

	for name, err := range errs {
		if err == nil {
			t.Errorf("%s: no error", name)

			continue
		}

		if strings.Contains(err.Error(), testToken) {
			t.Errorf("%s: error carries the token", name)
		}
	}

	if _, ok := errors.AsType[*api.GraphQLError](errs["graphql"]); !ok {
		t.Errorf("graphql error is not a *api.GraphQLError: %v", errs["graphql"])
	}
}

func TestClientTransportErrorCarriesNoToken(t *testing.T) {
	rt := roundTripFunc(func(r *http.Request) (*http.Response, error) {
		return nil, errors.New("blocked " + r.URL.Host)
	})

	c, err := NewClient("github.com", Options{Token: testToken, Transport: rt})
	if err != nil {
		t.Fatal(err)
	}

	err = c.rest(context.Background(), http.MethodGet, "repos/o/r", nil, nil)
	if err == nil || strings.Contains(err.Error(), testToken) {
		t.Fatalf("err = %v", err)
	}
}

func TestGraphQLErrorsBesideData(t *testing.T) {
	c, _ := newTestClient(t, "github.com", http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		okJSON(w, `{"data":{"viewer":{"login":"me"}},"errors":[{"type":"SOMETHING","message":"m"}]}`)
	}))

	err := c.query(context.Background(), "Partial", new(viewerQuery), map[string]any{"n": 1})

	if _, ok := errors.AsType[*api.GraphQLError](err); !ok {
		t.Fatalf("err = %v, want a *api.GraphQLError", err)
	}

	if errors.Is(err, ErrRateLimited) {
		t.Error("a non-rate-limit GraphQL error is a RateLimitError")
	}
}

// within reports whether reset is d after a moment between before and after.
func within(reset, before, after time.Time, d time.Duration) bool {
	return !reset.Before(before.Add(d)) && !reset.After(after.Add(d))
}

func TestRESTRateLimits(t *testing.T) {
	tests := []struct {
		name   string
		status int
		header map[string]string
		check  func(reset, before, after time.Time) bool
	}{
		{
			"403 with an exhausted limit", 403,
			map[string]string{"X-RateLimit-Remaining": "0", "X-RateLimit-Reset": "4102444800"},
			func(r, _, _ time.Time) bool { return r.Unix() == 4102444800 },
		},
		{
			"429 with Retry-After", 429,
			map[string]string{"Retry-After": "30"},
			func(r, before, after time.Time) bool { return within(r, before, after, 30*time.Second) },
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			c, _ := newTestClient(t, "github.com", http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				for k, v := range tc.header {
					w.Header().Set(k, v)
				}

				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(tc.status)
				_, _ = io.WriteString(w, `{"message":"limited"}`)
			}))

			before := time.Now()
			err := c.rest(context.Background(), http.MethodGet, "repos/o/r", nil, nil)
			after := time.Now()

			var rl *RateLimitError
			if !errors.As(err, &rl) || !errors.Is(err, ErrRateLimited) {
				t.Fatalf("err = %v, want a *RateLimitError", err)
			}

			if !tc.check(rl.Reset, before, after) {
				t.Errorf("reset = %v, call from %v to %v", rl.Reset, before, after)
			}

			if _, ok := errors.AsType[*api.HTTPError](err); !ok {
				t.Error("original *api.HTTPError lost")
			}
		})
	}
}

func TestGraphQLErrorsCarryNoURL(t *testing.T) {
	t.Parallel()

	const secret = "sig=SECRET"

	t.Run("a transport failure names no URL", func(t *testing.T) {
		t.Parallel()

		cause := errors.New("connection refused")
		rt := roundTripFunc(func(*http.Request) (*http.Response, error) { return nil, cause })

		c, err := NewClient("github.com", Options{Token: testToken, Transport: rt})
		if err != nil {
			t.Fatal(err)
		}

		var q viewerQuery

		err = c.query(context.Background(), "Viewer", &q, nil)
		if err == nil {
			t.Fatal("no error")
		}

		if strings.Contains(err.Error(), "https://") || strings.Contains(err.Error(), "api.github.com") {
			t.Errorf("the error names the URL: %v", err)
		}

		if !errors.Is(err, cause) {
			t.Errorf("the cause is lost: %v", err)
		}
	})

	t.Run("a bad gateway names no query string", func(t *testing.T) {
		t.Parallel()

		c, _ := newTestClient(t, "github.com", http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusBadGateway)
		}))

		var q viewerQuery

		err := c.query(context.Background(), "Viewer", &q, map[string]any{"x": secret})
		if err == nil {
			t.Fatal("no error")
		}

		if strings.Contains(err.Error(), "https://") || strings.Contains(err.Error(), "api.github.com") || strings.Contains(err.Error(), secret) {
			t.Errorf("the error names the URL: %v", err)
		}

		var he *api.HTTPError
		if !errors.As(err, &he) || he.StatusCode != http.StatusBadGateway {
			t.Errorf("no 502 *api.HTTPError in %v", err)
		}
	})
}
