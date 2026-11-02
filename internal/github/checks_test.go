package github

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"reflect"
	"strings"
	"sync"
	"testing"
)

const checksBase = "https://api.github.com/repos/octo/repo/commits/abc123"

func TestChecksPaginates(t *testing.T) {
	pages := [][]byte{
		readFixture(t, "check_runs_1.json"), readFixture(t, "check_runs_2.json"), readFixture(t, "check_runs_3.json"),
	}
	status := readFixture(t, "status.json")

	c, reqs := newTestClient(t, "github.com", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/status"):
			okJSON(w, string(status))
		case r.URL.Query().Get("page") == "":
			w.Header().Set("Link", fmt.Sprintf(`<%s/check-runs?per_page=100&page=2>; rel="next"`, checksBase))
			okJSON(w, string(pages[0]))
		case r.URL.Query().Get("page") == "2":
			w.Header().Set("Link", fmt.Sprintf(`<%s/check-runs?per_page=100&page=1>; rel="prev", `+
				`<%s/check-runs?per_page=100&page=3>; rel="next"`, checksBase, checksBase))
			okJSON(w, string(pages[1]))
		default:
			okJSON(w, string(pages[2]))
		}
	}))

	got, err := c.Checks(context.Background(), testRepo, "abc123")
	if err != nil {
		t.Fatal(err)
	}

	if len(got) != 303 {
		t.Fatalf("got %d checks, want 300 runs and 3 statuses", len(got))
	}

	if got[0].Name != "p1-000" || got[299].Name != "p3-099" || got[0].Kind != "run" {
		t.Errorf("runs out of order: first %q, last run %q", got[0].Name, got[299].Name)
	}

	var statuses []string

	for _, ch := range got[300:] {
		if ch.Kind != "status" {
			t.Errorf("%q has kind %q, want status", ch.Name, ch.Kind)
		}

		statuses = append(statuses, ch.Name)
	}

	if want := []string{"build/a", "deploy/m", "lint/z"}; !reflect.DeepEqual(statuses, want) {
		t.Errorf("statuses = %v, want %v by name", statuses, want)
	}

	if n := len(reqs()); n != 4 {
		t.Errorf("%d requests, want 3 pages and one status", n)
	}
}

func TestChecksStopsAtFivePages(t *testing.T) {
	var (
		mu    sync.Mutex
		pages int
	)

	c, _ := newTestClient(t, "github.com", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/status") {
			okJSON(w, `{"state":"success","statuses":[]}`)

			return
		}

		mu.Lock()
		pages++
		n := pages
		mu.Unlock()

		w.Header().Set("Link", fmt.Sprintf(`<%s/check-runs?per_page=100&page=%d>; rel="next"`, checksBase, n+1))
		okJSON(w, fmt.Sprintf(`{"total_count":99,"check_runs":[{"name":"r%d","status":"completed","conclusion":"success"}]}`, n))
	}))

	got, err := c.Checks(context.Background(), testRepo, "abc123")
	if err != nil {
		t.Fatalf("err = %v, want what was read without an error", err)
	}

	mu.Lock()
	served := pages
	mu.Unlock()

	if served != 5 || len(got) != 5 {
		t.Errorf("pages = %d, checks = %d, want 5 and 5", served, len(got))
	}
}

func TestChecksRejectsForeignNextLink(t *testing.T) {
	for name, next := range map[string]string{
		"other host":   "https://evil.example.com/steal?token=1",
		"other scheme": "http://api.github.com/repos/octo/repo/commits/abc123/check-runs?page=2",
		"not a url":    "://",
	} {
		t.Run(name, func(t *testing.T) {
			c, reqs := newTestClient(t, "github.com", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if strings.HasSuffix(r.URL.Path, "/status") {
					okJSON(w, `{"state":"success","statuses":[]}`)

					return
				}

				w.Header().Set("Link", fmt.Sprintf(`<%s>; rel="next"`, next))
				okJSON(w, `{"total_count":1,"check_runs":[{"name":"r","status":"completed","conclusion":"success"}]}`)
			}))

			_, err := c.Checks(context.Background(), testRepo, "abc123")
			if err == nil {
				t.Fatal("a next link to another origin was followed")
			}

			if strings.Contains(err.Error(), "evil.example.com") || strings.Contains(err.Error(), "token=1") {
				t.Errorf("error echoes the link: %v", err)
			}

			for _, r := range reqs() {
				if r.URL.Host != "api.github.com" || r.URL.Scheme != "https" {
					t.Errorf("request to %s://%s", r.URL.Scheme, r.URL.Host)
				}
			}
		})
	}
}

func TestChecksMapping(t *testing.T) {
	type run struct{ status, conclusion string }

	runs := []run{
		{"completed", "success"},
		{"completed", "neutral"},
		{"completed", "skipped"},
		{"completed", "stale"},
		{"completed", "failure"},
		{"completed", "timed_out"},
		{"completed", "cancelled"},
		{"completed", "action_required"},
		{"completed", "startup_failure"},
		{"queued", ""},
		{"in_progress", ""},
		{"waiting", ""},
		{"pending", ""},
		{"completed", "something_new"},
	}
	wantRuns := []CheckState{
		CheckSuccess, CheckSkipped, CheckSkipped, CheckSkipped,
		CheckFailure, CheckFailure, CheckFailure,
		CheckFailure, CheckFailure,
		CheckPending, CheckPending, CheckPending, CheckPending,
		CheckFailure,
	}
	wantConclusions := []string{
		"success", "neutral", "skipped", "stale", "failure", "timed_out", "cancelled", "action_required",
		"startup_failure", "queued", "in_progress", "waiting", "pending", "something_new",
	}

	var apiRuns []map[string]any

	for i, r := range runs {
		m := map[string]any{"name": fmt.Sprintf("run%02d", i), "status": r.status}
		if r.conclusion != "" {
			m["conclusion"] = r.conclusion
		}

		apiRuns = append(apiRuns, m)
	}

	// Both URLs present, html_url wins; only details_url present, it is used; neither, empty.
	apiRuns[0]["html_url"] = "https://github.com/h"
	apiRuns[0]["details_url"] = "https://ci.example.com/d"
	apiRuns[1]["details_url"] = "https://ci.example.com/only"
	apiRuns[0]["started_at"] = "2026-10-27T09:00:00Z"
	apiRuns[0]["completed_at"] = "2026-10-27T09:01:00Z"

	statuses := []map[string]any{
		{"context": "s-pending", "state": "pending", "created_at": "2026-10-27T09:00:00Z", "updated_at": "2026-10-27T09:09:00Z"},
		{
			"context": "s-success", "state": "success", "created_at": "2026-10-27T09:00:00Z", "updated_at": "2026-10-27T09:09:00Z",
			"target_url": "https://ci.example.com/s",
		},
		{"context": "s-failure", "state": "failure", "created_at": "2026-10-27T09:00:00Z", "updated_at": "2026-10-27T09:09:00Z"},
		{"context": "s-error", "state": "error", "created_at": "2026-10-27T09:00:00Z", "updated_at": "2026-10-27T09:09:00Z"},
		{"context": "s-odd", "state": "odd", "created_at": "2026-10-27T09:00:00Z", "updated_at": "2026-10-27T09:09:00Z"},
	}

	runsBody, _ := json.Marshal(map[string]any{"total_count": len(apiRuns), "check_runs": apiRuns})
	statusBody, _ := json.Marshal(map[string]any{"state": "failure", "statuses": statuses})

	c, _ := newTestClient(t, "github.com", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/status") {
			okJSON(w, string(statusBody))

			return
		}

		okJSON(w, string(runsBody))
	}))

	got, err := c.Checks(context.Background(), testRepo, "abc123")
	if err != nil {
		t.Fatal(err)
	}

	if len(got) != len(runs)+len(statuses) {
		t.Fatalf("got %d checks", len(got))
	}

	for i := range runs {
		if got[i].State != wantRuns[i] || got[i].Conclusion != wantConclusions[i] {
			t.Errorf("%s (%s/%s): state %q conclusion %q, want %q %q", got[i].Name, runs[i].status,
				runs[i].conclusion, got[i].State, got[i].Conclusion, wantRuns[i], wantConclusions[i])
		}
	}

	if got[0].URL != "https://github.com/h" || got[1].URL != "https://ci.example.com/only" || got[2].URL != "" {
		t.Errorf("urls = %q, %q, %q", got[0].URL, got[1].URL, got[2].URL)
	}

	if got[0].StartedAt.IsZero() || got[0].CompletedAt.IsZero() || !got[9].CompletedAt.IsZero() {
		t.Errorf("run times = %v %v, queued completed = %v", got[0].StartedAt, got[0].CompletedAt, got[9].CompletedAt)
	}

	wantStatuses := []struct {
		name      string
		state     CheckState
		completed bool
	}{
		{"s-error", CheckFailure, true},
		{"s-failure", CheckFailure, true},
		{"s-odd", CheckFailure, true},
		{"s-pending", CheckPending, false},
		{"s-success", CheckSuccess, true},
	}

	for i, w := range wantStatuses {
		s := got[len(runs)+i]
		if s.Name != w.name || s.Kind != "status" || s.State != w.state || s.CompletedAt.IsZero() == w.completed {
			t.Errorf("status %d = %+v, want %+v", i, s, w)
		}

		if s.StartedAt.IsZero() {
			t.Errorf("%s has no start", s.Name)
		}
	}

	if got[len(runs)+4].URL != "https://ci.example.com/s" {
		t.Errorf("status url = %q", got[len(runs)+4].URL)
	}
}

func TestChecksNeedsSHA(t *testing.T) {
	c, reqs := newTestClient(t, "github.com", http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		t.Error("a request was sent without a commit")
	}))

	if _, err := c.Checks(context.Background(), testRepo, ""); !errors.Is(err, ErrNoSHA) {
		t.Fatalf("err = %v, want ErrNoSHA", err)
	}

	if len(reqs()) != 0 {
		t.Error("requests recorded")
	}
}
