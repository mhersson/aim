package github

import (
	"context"
	"encoding/json"
	"reflect"
	"slices"
	"testing"
	"time"
)

func TestMyPRs(t *testing.T) {
	t.Parallel()

	rec := &gqlRecorder{body: readFixture(t, "search.json")}
	c, _ := newTestClient(t, "github.com", rec)

	got, err := c.MyPRs(context.Background(), time.Time{})
	if err != nil {
		t.Fatal(err)
	}

	type brief struct {
		Repo   Repo
		Number int
		Author string
	}

	briefs := func(in []SearchPR) []brief {
		var out []brief
		for _, s := range in {
			out = append(out, brief{s.Repo, s.PR.Number, s.PR.Author})
		}

		return out
	}

	wantReq := []brief{
		{Repo{"github.com", "acme", "web"}, 311, "kari-dev"},
		{Repo{"github.com", "acme", "web"}, 305, "lars"},
		{Repo{"github.com", "acme", "infra"}, 88, "bot-user"},
	}
	if g := briefs(got.Requested); !reflect.DeepEqual(g, wantReq) {
		t.Fatalf("Requested = %v, want %v", g, wantReq)
	}

	if c := got.Requested[0].PR.Checks; c.Total != 6 || c.Done != 6 || c.State != CheckSuccess {
		t.Errorf("checks of #311 = %+v, want 6 of 6", c)
	}

	wantAuth := []brief{{Repo{"github.com", "mhersson", "aim"}, 24, "mhersson"}}
	if g := briefs(got.Authored); !reflect.DeepEqual(g, wantAuth) {
		t.Errorf("Authored = %v, want %v", g, wantAuth)
	}

	if !got.Authored[0].PR.ViewerAuthored {
		t.Error("ViewerAuthored = false on an authored pull request")
	}

	wantOwned := []brief{
		{Repo{"github.com", "mhersson", "aim"}, 31, "dependabot"},
		{Repo{"github.com", "mhersson", "aim"}, 24, "mhersson"},
	}
	if g := briefs(got.Owned); !reflect.DeepEqual(g, wantOwned) {
		t.Errorf("Owned = %v, want %v", g, wantOwned)
	}
}

func TestMyPRsSearches(t *testing.T) {
	t.Parallel()

	east := time.FixedZone("UTC+2", 2*60*60)

	tests := []struct {
		name          string
		since         time.Time
		wantRequested string
	}{
		{name: "no limit", wantRequested: "is:open is:pr archived:false review-requested:@me"},
		{
			name: "since a date", since: time.Date(2026, 10, 24, 10, 0, 0, 0, time.UTC),
			wantRequested: "is:open is:pr archived:false review-requested:@me updated:>=2026-10-24",
		},
		{
			// 01:00 on the 25th at UTC+2 is still the 24th in UTC.
			name: "since in another zone", since: time.Date(2026, 10, 25, 1, 0, 0, 0, east),
			wantRequested: "is:open is:pr archived:false review-requested:@me updated:>=2026-10-24",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			rec := &gqlRecorder{body: readFixture(t, "search.json")}
			c, _ := newTestClient(t, "github.com", rec)

			if _, err := c.MyPRs(context.Background(), tt.since); err != nil {
				t.Fatal(err)
			}

			var body struct {
				Variables map[string]any `json:"variables"`
			}
			if err := json.Unmarshal([]byte(rec.requests()[0]), &body); err != nil {
				t.Fatal(err)
			}

			want := map[string]any{
				"requested": tt.wantRequested,
				"authored":  "is:open is:pr archived:false author:@me",
				"owned":     "is:open is:pr archived:false user:@me",
			}
			if !reflect.DeepEqual(body.Variables, want) {
				t.Errorf("variables = %v, want %v", body.Variables, want)
			}
		})
	}
}

func TestMyPRsGHE(t *testing.T) {
	t.Parallel()

	rec := &gqlRecorder{body: readFixture(t, "search.json")}
	c, _ := newTestClient(t, "ghe.example.com", rec)

	got, err := c.MyPRs(context.Background(), time.Time{})
	if err != nil {
		t.Fatal(err)
	}

	all := slices.Concat(got.Requested, got.Authored, got.Owned)
	if len(got.Owned) == 0 {
		t.Fatal("no owned pull requests")
	}

	for _, s := range all {
		if s.Repo.Host != "ghe.example.com" {
			t.Errorf("repo host = %q, want ghe.example.com", s.Repo.Host)
		}
	}
}
