package github

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/cli/go-gh/v2/pkg/api"
)

func readFixture(t *testing.T, name string) []byte {
	t.Helper()

	b, err := os.ReadFile("testdata/" + name)
	if err != nil {
		t.Fatal(err)
	}

	return b
}

// gqlRecorder serves one canned GraphQL response and keeps the request bodies.
type gqlRecorder struct {
	body []byte

	mu     sync.Mutex
	bodies []string
}

func (g *gqlRecorder) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	b, _ := io.ReadAll(r.Body)

	g.mu.Lock()
	g.bodies = append(g.bodies, string(b))
	g.mu.Unlock()

	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write(g.body)
}

func (g *gqlRecorder) requests() []string {
	g.mu.Lock()
	defer g.mu.Unlock()

	return append([]string(nil), g.bodies...)
}

var testRepo = Repo{Host: "github.com", Owner: "octo", Name: "repo"}

func mustTime(t *testing.T, s string) time.Time {
	t.Helper()

	v, err := time.Parse(time.RFC3339, s)
	if err != nil {
		t.Fatal(err)
	}

	return v
}

func TestPullRequestsDecodes(t *testing.T) {
	rec := &gqlRecorder{body: readFixture(t, "pulls.json")}
	c, _ := newTestClient(t, "github.com", rec)

	info, prs, rl, err := c.PullRequests(context.Background(), testRepo)
	if err != nil {
		t.Fatal(err)
	}

	wantInfo := RepoInfo{
		DefaultBranch:       "main",
		Methods:             []MergeMethod{MergeCommit, Squash},
		DeleteBranchOnMerge: true,
	}
	if !reflect.DeepEqual(info, wantInfo) {
		t.Errorf("info = %+v, want %+v", info, wantInfo)
	}

	wantRL := RateLimit{Remaining: 4990, Reset: mustTime(t, "2026-10-27T10:00:00Z")}
	if rl != wantRL {
		t.Errorf("rate limit = %+v, want %+v", rl, wantRL)
	}

	want := []PR{
		{
			ID: "PR_kwDOAAAB12", Number: 12, Title: "Add the poller", URL: "https://github.com/octo/repo/pull/12", Author: "octocat",
			State: "OPEN", ViewerAuthored: true, HeadBranch: "feat/poller", BaseBranch: "main",
			HeadOID:        "1111111111111111111111111111111111111111",
			ReviewDecision: "APPROVED", MergeState: "BLOCKED",
			Checks:    Checks{State: CheckFailure, Total: 5, Done: 4, Failed: 1},
			UpdatedAt: mustTime(t, "2026-10-27T09:30:00Z"),
		},
		{
			ID: "PR_kwDOAAAB9", Number: 9, Title: "Fix a typo", URL: "https://github.com/octo/repo/pull/9", Author: "ghost",
			State: "OPEN", Draft: true, CrossRepo: true, HeadBranch: "patch-1", BaseBranch: "main",
			HeadOID:    "2222222222222222222222222222222222222222",
			MergeState: "CLEAN",
			Checks:     Checks{State: CheckSuccess, Total: 2, Done: 2},
			UpdatedAt:  mustTime(t, "2026-10-26T18:00:00Z"),
		},
	}
	if !reflect.DeepEqual(prs, want) {
		t.Errorf("prs = %+v\nwant %+v", prs, want)
	}
}

func TestPullRequestsNullRollup(t *testing.T) {
	rec := &gqlRecorder{body: readFixture(t, "pulls_null_rollup.json")}
	c, _ := newTestClient(t, "github.com", rec)

	info, prs, _, err := c.PullRequests(context.Background(), testRepo)
	if err != nil {
		t.Fatal(err)
	}

	if info.DefaultBranch != "" {
		t.Errorf("default branch = %q, want none for a null defaultBranchRef", info.DefaultBranch)
	}

	if !reflect.DeepEqual(info.Methods, []MergeMethod{Rebase}) {
		t.Errorf("methods = %v", info.Methods)
	}

	if len(prs) != 2 {
		t.Fatalf("got %d prs", len(prs))
	}

	for _, pr := range prs {
		if pr.Checks != (Checks{}) {
			t.Errorf("#%d checks = %+v, want none", pr.Number, pr.Checks)
		}
	}

	if prs[0].ReviewDecision != "REVIEW_REQUIRED" || prs[1].ReviewDecision != "" {
		t.Errorf("review decisions = %q, %q", prs[0].ReviewDecision, prs[1].ReviewDecision)
	}
}

func TestPullRequestsPartialErrors(t *testing.T) {
	rec := &gqlRecorder{body: readFixture(t, "pulls_errors.json")}
	c, _ := newTestClient(t, "github.com", rec)

	_, prs, _, err := c.PullRequests(context.Background(), testRepo)

	if _, ok := errors.AsType[*api.GraphQLError](err); !ok {
		t.Fatalf("err = %v, want a *api.GraphQLError", err)
	}

	if len(prs) != 0 {
		t.Errorf("prs = %v, want none", prs)
	}
}

func TestPullRequestsQueryShape(t *testing.T) {
	rec := &gqlRecorder{body: readFixture(t, "pulls.json")}
	c, _ := newTestClient(t, "github.com", rec)

	if _, _, _, err := c.PullRequests(context.Background(), testRepo); err != nil {
		t.Fatal(err)
	}

	reqs := rec.requests()
	if len(reqs) != 1 {
		t.Fatalf("%d requests", len(reqs))
	}

	body := strings.Join(strings.Fields(reqs[0]), "")

	for _, want := range []string{
		"defaultBranchRef{name}", "mergeCommitAllowed", "squashMergeAllowed", "rebaseMergeAllowed",
		"deleteBranchOnMerge", "states:OPEN", "first:100", "orderBy:{field:UPDATED_AT,direction:DESC}",
		"number", "title", "url", "isDraft", "state", "isCrossRepository", "headRefName", "headRefOid",
		"baseRefName", "updatedAt", "reviewDecision", "mergeStateStatus", "author{login}", "viewerDidAuthor",
		"commits(last:1)", "statusCheckRollup{state", "checkRunCount", "checkRunCountsByState{state,count}",
		"statusContextCount", "statusContextCountsByState{state,count}", "rateLimit{remaining,resetAt}",
		`"owner":"octo"`, `"name":"repo"`, "$owner:String!", "$name:String!",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("query lacks %q\n%s", want, body)
		}
	}
}

// branchNode is one pull request node of a branch lookup response.
func branchNode(n int, state string, cross bool) string {
	return fmt.Sprintf(`{"number":%d,"title":"t%d","url":"https://github.com/octo/repo/pull/%d","isDraft":false,`+
		`"state":%q,"isCrossRepository":%t,"headRefName":"feat/x","headRefOid":"abc","baseRefName":"main",`+
		`"updatedAt":"2026-10-27T09:00:00Z","reviewDecision":null,"mergeStateStatus":"CLEAN","author":{"login":"a"},`+
		`"commits":{"nodes":[]}}`, n, n, n, state, cross)
}

func branchResponse(nodes ...string) []byte {
	return []byte(`{"data":{"repository":{"pullRequests":{"nodes":[` + strings.Join(nodes, ",") + `]}}}}`)
}

func TestPRForBranchPrefersOpen(t *testing.T) {
	rec := &gqlRecorder{body: readFixture(t, "pull_for_branch.json")}
	c, _ := newTestClient(t, "github.com", rec)

	pr, err := c.PRForBranch(context.Background(), testRepo, "feat/x")
	if err != nil {
		t.Fatal(err)
	}

	if pr.Number != 25 || pr.State != "OPEN" || !pr.Draft {
		t.Errorf("pr = %+v, want the open #25", pr)
	}

	if !pr.ViewerAuthored {
		t.Errorf("ViewerAuthored = false, want true for #25")
	}

	if pr.Checks != (Checks{State: CheckPending, Total: 1}) {
		t.Errorf("checks = %+v", pr.Checks)
	}

	reqs := rec.requests()
	if len(reqs) != 1 {
		t.Fatalf("%d requests, want 1", len(reqs))
	}

	body := strings.Join(strings.Fields(reqs[0]), "")
	for _, want := range []string{
		"headRefName:$branch", "first:10", "orderBy:{field:CREATED_AT,direction:DESC}",
		"states:[OPEN,MERGED,CLOSED]", "viewerDidAuthor", `"branch":"feat/x"`, "$branch:String!",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("query lacks %q\n%s", want, body)
		}
	}
}

func TestPRForBranchFallsBackToNewest(t *testing.T) {
	rec := &gqlRecorder{body: branchResponse(
		branchNode(30, "CLOSED", false), branchNode(20, "MERGED", false),
	)}
	c, _ := newTestClient(t, "github.com", rec)

	pr, err := c.PRForBranch(context.Background(), testRepo, "feat/x")
	if err != nil {
		t.Fatal(err)
	}

	if pr.Number != 30 {
		t.Errorf("number = %d, want the newest, 30", pr.Number)
	}

	if pr.ViewerAuthored {
		t.Errorf("ViewerAuthored = true, want false when the node omits it")
	}
}

func TestPRForBranchSkipsCrossRepo(t *testing.T) {
	rec := &gqlRecorder{body: branchResponse(
		branchNode(40, "OPEN", true), branchNode(30, "CLOSED", false),
	)}
	c, _ := newTestClient(t, "github.com", rec)

	pr, err := c.PRForBranch(context.Background(), testRepo, "feat/x")
	if err != nil {
		t.Fatal(err)
	}

	if pr.Number != 30 {
		t.Errorf("number = %d, want 30: a fork's pull request must not match", pr.Number)
	}
}

func TestPRForBranchNone(t *testing.T) {
	for name, body := range map[string][]byte{
		"empty":      branchResponse(),
		"only forks": branchResponse(branchNode(40, "OPEN", true)),
	} {
		t.Run(name, func(t *testing.T) {
			c, _ := newTestClient(t, "github.com", &gqlRecorder{body: body})

			_, err := c.PRForBranch(context.Background(), testRepo, "feat/x")
			if !errors.Is(err, ErrNoPR) {
				t.Fatalf("err = %v, want ErrNoPR", err)
			}
		})
	}
}

func TestPullRequestDecodes(t *testing.T) {
	rec := &gqlRecorder{body: []byte(`{"data":{"repository":{"pullRequest":` + branchNode(150, "OPEN", false) + `}}}`)}
	c, _ := newTestClient(t, "github.com", rec)

	pr, err := c.PullRequest(context.Background(), testRepo, 150)
	if err != nil {
		t.Fatal(err)
	}

	var n pullNode
	if err := json.Unmarshal([]byte(branchNode(150, "OPEN", false)), &n); err != nil {
		t.Fatal(err)
	}

	if pr != n.pr() || pr.Number != 150 {
		t.Errorf("pr = %+v, want %+v", pr, n.pr())
	}

	body := strings.Join(strings.Fields(rec.requests()[0]), "")
	if !strings.Contains(body, "pullRequest(number:$number)") || !strings.Contains(body, `"number":150`) {
		t.Errorf("query = %s", body)
	}
}

func TestPullRequestNotFound(t *testing.T) {
	rec := &gqlRecorder{body: []byte(`{"data":{"repository":{"pullRequest":null}},"errors":[{"type":"NOT_FOUND","path":["repository","pullRequest"],"message":"Could not resolve to a PullRequest with the number of 99."}]}`)}
	c, _ := newTestClient(t, "github.com", rec)

	_, err := c.PullRequest(context.Background(), testRepo, 99)
	if !errors.Is(err, ErrNoPR) {
		t.Fatalf("err = %v, want ErrNoPR", err)
	}
}

func TestMergeBase(t *testing.T) {
	t.Parallel()

	var got *http.Request

	h := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = r.Clone(r.Context())

		_, _ = w.Write(readFixture(t, "compare.json"))
	})
	c, _ := newTestClient(t, "github.com", h)

	sha, err := c.MergeBase(t.Context(), Repo{Host: "github.com", Owner: "o", Name: "r"}, "main", "abc123")
	if err != nil {
		t.Fatal(err)
	}

	if want := "91c32c2bbd92976fe0d36f3a3e7b36db8a39c640"; sha != want {
		t.Errorf("merge base = %q, want %q", sha, want)
	}

	if got.Method != http.MethodGet || got.URL.EscapedPath() != "/repos/o/r/compare/main...abc123" {
		t.Errorf("request = %s %s", got.Method, got.URL.EscapedPath())
	}

	if q := got.URL.Query().Get("per_page"); q != "1" {
		t.Errorf("per_page = %q, want 1", q)
	}
}

func TestMergeBaseBranchWithSlash(t *testing.T) {
	t.Parallel()

	c, reqs := setup(t, step{body: `{"merge_base_commit":{"sha":"deadbeef"}}`})

	if _, err := c.MergeBase(t.Context(), Repo{Host: "github.com", Owner: "o", Name: "r"}, "release/1.0", "abc123"); err != nil {
		t.Fatal(err)
	}

	got := reqs()
	if len(got) != 1 || got[0].path != "/repos/o/r/compare/release/1.0...abc123" {
		t.Errorf("requests = %+v", got)
	}
}

func TestMergeBaseRefuses(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		step step
		api  bool
	}{
		{"not found", step{status: http.StatusNotFound, body: `{"message":"Not Found"}`}, true},
		{"no sha", step{body: `{"status":"ahead"}`}, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			c, _ := setup(t, tt.step)

			_, err := c.MergeBase(t.Context(), Repo{Host: "github.com", Owner: "o", Name: "r"}, "main", "abc123")
			if err == nil {
				t.Fatal("want an error")
			}

			var he *api.HTTPError
			if errors.As(err, &he) != tt.api {
				t.Errorf("error = %v, API error wanted: %v", err, tt.api)
			}
		})
	}
}
