package github

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/cli/go-gh/v2/pkg/api"
)

var logRepo = Repo{Host: "github.com", Owner: "acme", Name: "web"}

const (
	logHost    = "results.blob.example.net"
	logPath    = "/repos/acme/web/actions/jobs/51234987/logs"
	wantRange  = "bytes=-1048576"
	logSigPath = "/log?sig=x"
)

// logAPI redirects the job log to the storage host.
func logAPI() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != logPath {
			http.NotFound(w, r)

			return
		}

		http.Redirect(w, r, "https://"+logHost+logSigPath, http.StatusFound)
	})
}

func logClient(t *testing.T, storage http.HandlerFunc) (*Client, func() []*http.Request) {
	t.Helper()

	return newTestClientWith(t, "github.com", logAPI(), map[string]http.Handler{logHost: storage})
}

func TestJobLogRedirectDropsTheToken(t *testing.T) {
	t.Parallel()

	c, reqs := logClient(t, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Range", "bytes 0-4/5")
		w.WriteHeader(http.StatusPartialContent)
		_, _ = w.Write([]byte("hello"))
	})

	if _, _, err := c.JobLog(context.Background(), logRepo, 51234987); err != nil {
		t.Fatal(err)
	}

	var apiReq, storageReq *http.Request

	for _, r := range reqs() {
		switch r.URL.Host {
		case "api.github.com":
			apiReq = r
		case logHost:
			storageReq = r
		}
	}

	if apiReq == nil || storageReq == nil {
		t.Fatalf("api request %v, storage request %v; want both", apiReq, storageReq)
	}

	if apiReq.Header.Get("Authorization") == "" || apiReq.Header.Get("Range") != wantRange {
		t.Errorf("api request headers %v, want Authorization and Range", apiReq.Header)
	}

	if got := storageReq.Header.Get("Authorization"); got != "" {
		t.Errorf("storage request carries Authorization %q", got)
	}

	if got := storageReq.Header.Get("Range"); got != wantRange {
		t.Errorf("storage Range = %q, want %q", got, wantRange)
	}
}

func TestJobLogRange(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name, contentRange string
		cut                bool
	}{
		{"tail of a longer log", "bytes 10-19/20", true},
		{"whole log", "bytes 0-19/20", false},
		{"unknown start, short body", "bytes */20", false},
		{"missing header, short body", "", false},
		{"garbled header, short body", "garbled", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			body := []byte("0123456789abcdefghij")

			c, _ := logClient(t, func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Range", tt.contentRange)
				w.WriteHeader(http.StatusPartialContent)
				_, _ = w.Write(body)
			})

			got, cut, err := c.JobLog(context.Background(), logRepo, 51234987)
			if err != nil {
				t.Fatal(err)
			}

			if !bytes.Equal(got, body) || cut != tt.cut {
				t.Errorf("got %q cut %v, want %q cut %v", got, cut, body, tt.cut)
			}
		})
	}
}

func TestJobLogWithoutRange(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		size int
		cut  bool
	}{
		{"over the tail", JobLogTail + 10, true},
		{"small", 100, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			body := make([]byte, tt.size)
			for i := range body {
				body[i] = byte('a' + i%26)
			}

			c, _ := logClient(t, func(w http.ResponseWriter, _ *http.Request) {
				_, _ = w.Write(body)
			})

			got, cut, err := c.JobLog(context.Background(), logRepo, 51234987)
			if err != nil {
				t.Fatal(err)
			}

			want := body
			if len(want) > JobLogTail {
				want = want[len(want)-JobLogTail:]
			}

			if !bytes.Equal(got, want) || cut != tt.cut {
				t.Errorf("got %d bytes cut %v, want %d bytes cut %v", len(got), cut, len(want), tt.cut)
			}
		})
	}
}

func TestJobLogEmpty(t *testing.T) {
	t.Parallel()

	c, _ := logClient(t, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusRequestedRangeNotSatisfiable)
	})

	got, cut, err := c.JobLog(context.Background(), logRepo, 51234987)
	if err != nil || len(got) != 0 || cut {
		t.Errorf("got %q cut %v err %v, want an empty log", got, cut, err)
	}
}

func TestJobLogNotFound(t *testing.T) {
	t.Parallel()

	c, _ := newTestClient(t, "github.com", http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, `{"message":"Not Found"}`, http.StatusNotFound)
	}))

	_, _, err := c.JobLog(context.Background(), logRepo, 51234987)
	if err == nil {
		t.Fatal("want an error")
	}

	var he *api.HTTPError
	if !errors.As(err, &he) || he.StatusCode != http.StatusNotFound {
		t.Errorf("err = %v, want a 404 HTTPError", err)
	}

	for _, secret := range []string{testToken, "sig=x", "https://"} {
		if strings.Contains(err.Error(), secret) {
			t.Errorf("error %q holds %q", err, secret)
		}
	}
}

func TestJobLogStorageNotFound(t *testing.T) {
	t.Parallel()

	c, _ := logClient(t, func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "gone", http.StatusNotFound)
	})

	_, _, err := c.JobLog(context.Background(), logRepo, 51234987)
	if err == nil {
		t.Fatal("want an error")
	}

	for _, secret := range []string{testToken, "sig=x"} {
		if strings.Contains(err.Error(), secret) {
			t.Errorf("error %q holds %q", err, secret)
		}
	}
}

// The storage host never gets the token, so its 401 says nothing about it.
func TestJobLogStorageRefusalKeepsTheToken(t *testing.T) {
	t.Parallel()

	c, _ := logClient(t, func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "no", http.StatusUnauthorized)
	})

	if _, _, err := c.JobLog(context.Background(), logRepo, 51234987); err == nil {
		t.Fatal("want an error")
	}

	if c.TokenRefused() {
		t.Error("a 401 from the storage host marked the token refused")
	}
}

func TestJobLogSkipsETags(t *testing.T) {
	t.Parallel()

	body := []byte("line one\nline two\n")

	c, reqs := logClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("If-None-Match") != "" {
			w.WriteHeader(http.StatusNotModified)

			return
		}

		w.Header().Set("ETag", `"abc"`)
		w.Header().Set("Content-Range", fmt.Sprintf("bytes 0-%d/%d", len(body)-1, len(body)))
		w.WriteHeader(http.StatusPartialContent)
		_, _ = w.Write(body)
	})

	for i := range 2 {
		got, cut, err := c.JobLog(context.Background(), logRepo, 51234987)
		if err != nil || !bytes.Equal(got, body) || cut {
			t.Fatalf("read %d: got %q cut %v err %v, want %q", i, got, cut, err, body)
		}
	}

	for _, r := range reqs() {
		if r.Header.Get("If-None-Match") != "" {
			t.Errorf("request to %s carries If-None-Match", r.URL.Host)
		}
	}
}

func TestChecksIDAndApp(t *testing.T) {
	t.Parallel()

	c, _ := newTestClient(t, "github.com", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/status") {
			okJSON(w, `{"statuses":[{"context":"ci/x","state":"success"}]}`)

			return
		}

		okJSON(w, `{"check_runs":[
			{"id":11,"name":"build","status":"completed","conclusion":"success","app":{"slug":"github-actions"}},
			{"id":12,"name":"cov","status":"completed","conclusion":"success","app":{"slug":"codecov"}}]}`)
	}))

	got, err := c.Checks(context.Background(), logRepo, "abc123")
	if err != nil {
		t.Fatal(err)
	}

	want := []struct {
		id  int64
		app string
	}{{11, "github-actions"}, {12, "codecov"}, {0, ""}}

	if len(got) != len(want) {
		t.Fatalf("got %d checks, want %d", len(got), len(want))
	}

	for i, w := range want {
		if got[i].ID != w.id || got[i].App != w.app {
			t.Errorf("check %d (%s) = id %d app %q, want %d %q", i, got[i].Name, got[i].ID, got[i].App, w.id, w.app)
		}
	}
}

func TestCheckRun(t *testing.T) {
	t.Parallel()

	c, _ := newTestClient(t, "github.com", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/repos/acme/web/check-runs/77" {
			http.NotFound(w, r)

			return
		}

		okJSON(w, `{"id":77,"output":{"title":"Coverage","summary":"92% of lines"}}`)
	}))

	got, err := c.CheckRun(context.Background(), logRepo, 77)
	if err != nil {
		t.Fatal(err)
	}

	if want := (CheckRunOutput{Title: "Coverage", Summary: "92% of lines"}); got != want {
		t.Errorf("got %+v, want %+v", got, want)
	}
}

func TestReadTail(t *testing.T) {
	t.Parallel()

	got, total, err := readTail(strings.NewReader("0123456789"), 4, 20)
	if err != nil || string(got) != "6789" || total != 10 {
		t.Fatalf("readTail = %q, %d, %v", got, total, err)
	}

	got, total, err = readTail(strings.NewReader("0123456789"), 4, 9)
	if !errors.Is(err, ErrJobLogTooLarge) || got != nil || total <= 9 {
		t.Fatalf("over the limit: %q, %d, %v", got, total, err)
	}
}

func TestContentRangeStart(t *testing.T) {
	t.Parallel()

	tests := []struct {
		in    string
		start int64
		ok    bool
	}{
		{"bytes 10-19/20", 10, true},
		{"bytes 0-19/20", 0, true},
		{"bytes */20", 0, false},
		{"", 0, false},
		{"garbled", 0, false},
		{"bytes x-19/20", 0, false},
	}
	for _, tc := range tests {
		start, ok := contentRangeStart(tc.in)
		if start != tc.start || ok != tc.ok {
			t.Errorf("contentRangeStart(%q) = %d, %v; want %d, %v", tc.in, start, ok, tc.start, tc.ok)
		}
	}
}

func TestJobLogRangeWithoutStart(t *testing.T) {
	t.Parallel()

	for _, header := range []string{"bytes */9999999", "", "garbled"} {
		c, _ := logClient(t, func(w http.ResponseWriter, _ *http.Request) {
			if header != "" {
				w.Header().Set("Content-Range", header)
			}

			w.WriteHeader(http.StatusPartialContent)
			_, _ = w.Write(bytes.Repeat([]byte("x"), JobLogTail))
		})

		_, cut, err := c.JobLog(context.Background(), logRepo, 51234987)
		if err != nil || !cut {
			t.Errorf("Content-Range %q: cut %v, err %v; a full tail with no start reads cut", header, cut, err)
		}
	}
}

func TestCheckRunErrorsCarryNoURL(t *testing.T) {
	t.Parallel()

	for _, status := range []int{http.StatusNotFound, http.StatusBadGateway} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			t.Parallel()

			c, _ := newTestClient(t, "github.com", http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				http.Error(w, `{"message":"nope"}`, status)
			}))

			_, err := c.CheckRun(context.Background(), logRepo, 77)
			if err == nil {
				t.Fatal("want an error")
			}

			for _, bad := range []string{"https://", "api.github.com"} {
				if strings.Contains(err.Error(), bad) {
					t.Errorf("error %q holds %q", err, bad)
				}
			}

			var he *api.HTTPError
			if !errors.As(err, &he) || he.StatusCode != status {
				t.Errorf("err = %v, want a %d HTTPError", err, status)
			}
		})
	}
}
