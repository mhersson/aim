//go:build live

package github

import (
	"context"
	"maps"
	"net/http"
	"os"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"
)

// statusCounter counts responses by status code; it sits below the ETag layer
// so it sees what the server answered.
type statusCounter struct {
	next   http.RoundTripper
	mu     sync.Mutex
	counts map[int]int
}

func (c *statusCounter) RoundTrip(req *http.Request) (*http.Response, error) {
	resp, err := c.next.RoundTrip(req)
	if err == nil {
		c.mu.Lock()
		c.counts[resp.StatusCode]++
		c.mu.Unlock()
	}

	return resp, err
}

func (c *statusCounter) snapshot() map[int]int {
	c.mu.Lock()
	defer c.mu.Unlock()

	out := make(map[int]int, len(c.counts))
	maps.Copy(out, c.counts)

	return out
}

// restoreLoginEnv undoes TestMain's isolation so go-gh and gh find the
// operator's login.
func restoreLoginEnv(t *testing.T) {
	t.Helper()

	for _, k := range isolatedEnv {
		if k == "GH_DEBUG" {
			continue
		}

		saved := originalEnv[k]
		t.Setenv(k, saved.value)

		if !saved.set {
			if err := os.Unsetenv(k); err != nil {
				t.Fatalf("unset %s: %v", k, err)
			}
		}
	}
}

// TestLiveReadOnly reads a real repository named by AIM_LIVE_REPO
// (owner/name; AIM_LIVE_HOST defaults to github.com) and never writes.
// It must run alone (-run '^TestLiveReadOnly$'): go-gh reads its config once
// per process.
func TestLiveReadOnly(t *testing.T) {
	slug := os.Getenv("AIM_LIVE_REPO")
	if slug == "" {
		t.Skip("AIM_LIVE_REPO is not set")
	}

	owner, name, ok := strings.Cut(slug, "/")
	if !ok || owner == "" || name == "" {
		t.Fatal("AIM_LIVE_REPO must be owner/name")
	}

	host := os.Getenv("AIM_LIVE_HOST")
	if host == "" {
		host = "github.com"
	}

	repo := Repo{Host: host, Owner: owner, Name: name}

	restoreLoginEnv(t)

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	token, err := Token(ctx, host, TokenOptions{})
	if err != nil {
		t.Fatalf("Token for %s: %v", host, err)
	}

	counter := &statusCounter{next: http.DefaultTransport, counts: map[int]int{}}

	c, err := NewClient(host, Options{Token: token, Transport: counter})
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}

	info, prs, rate, err := c.PullRequests(ctx, repo)
	if err != nil {
		t.Fatalf("PullRequests %s: %v", repo, err)
	}

	t.Logf("%s: default branch %q, merge methods %v, %d open PRs, rate limit remaining %d",
		repo, info.DefaultBranch, info.Methods, len(prs), rate.Remaining)

	var openPR *PR

	for i := range prs {
		if !prs[i].CrossRepo {
			openPR = &prs[i]

			break
		}
	}

	branch := os.Getenv("AIM_LIVE_BRANCH")

	if openPR == nil && branch == "" {
		t.Logf("%s: no same-repository open PR and no AIM_LIVE_BRANCH; nothing more to read", repo)

		return
	}

	if openPR != nil {
		t.Run("open pr", func(t *testing.T) {
			readBranch(ctx, t, c, counter, repo, openPR.HeadBranch, openPR.Number)
		})
	}

	if branch != "" {
		t.Run("branch", func(t *testing.T) {
			readBranch(ctx, t, c, counter, repo, branch, 0)
		})
	}
}

// readBranch looks the branch's PR up and reads its head's checks twice; the
// second read must be answered at least in part by a 304. wantNumber 0 skips
// the number assertion.
func readBranch(ctx context.Context, t *testing.T, c *Client, counter *statusCounter, repo Repo, branch string, wantNumber int) {
	t.Helper()

	pr, err := c.PRForBranch(ctx, repo, branch)
	if err != nil {
		t.Fatalf("PRForBranch %s %q: %v", repo, branch, err)
	}

	head := pr.HeadOID
	if len(head) > 7 {
		head = head[:7]
	}

	t.Logf("PR #%d on %q: state %s, head %s, viewer authored %t", pr.Number, branch, pr.State, head, pr.ViewerAuthored)

	if wantNumber != 0 && pr.Number != wantNumber {
		t.Errorf("PRForBranch returned #%d, want #%d", pr.Number, wantNumber)
	}

	if pr.BaseBranch != "" {
		mb, err := c.MergeBase(ctx, repo, pr.BaseBranch, pr.HeadOID)
		if err != nil {
			t.Fatalf("MergeBase %s..%s: %v", pr.BaseBranch, head, err)
		}

		t.Logf("merge base of %q and %s: %s", pr.BaseBranch, head, mb)
	}

	first, err := c.Checks(ctx, repo, pr.HeadOID)
	if err != nil {
		t.Fatalf("Checks (first): %v", err)
	}

	before := counter.snapshot()

	second, err := c.Checks(ctx, repo, pr.HeadOID)
	if err != nil {
		t.Fatalf("Checks (second): %v", err)
	}

	after := counter.snapshot()

	perState := map[CheckState]int{}
	for _, ch := range first {
		perState[ch.State]++
	}

	t.Logf("%d checks; per state %v", len(first), perState)

	delta := map[int]int{}
	total := 0

	for code, n := range after {
		if d := n - before[code]; d > 0 {
			delta[code] = d
			total += d
		}
	}

	if delta[http.StatusNotModified] < 1 {
		t.Errorf("second Checks saw no 304; response statuses %v", delta)
	}

	allNotModified := total > 0 && delta[http.StatusNotModified] == total
	t.Logf("second Checks: statuses %v, every response a 304: %t", delta, allNotModified)

	if allNotModified && !reflect.DeepEqual(first, second) {
		t.Error("second Checks answered entirely by 304 but differs from the first")
	}
}
