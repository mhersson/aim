package github

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/cli/go-gh/v2/pkg/api"
)

// step is one canned response; status 0 means 200.
type step struct {
	status int
	body   string
	retry  string // Retry-After
}

type reqRecord struct {
	method, path, body string
}

// sequence serves steps in order and records each request's body.
type sequence struct {
	t     *testing.T
	steps []step
	n     int
}

func (s *sequence) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if s.n >= len(s.steps) {
		s.t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
		w.WriteHeader(http.StatusTeapot)

		return
	}

	st := s.steps[s.n]
	s.n++

	if st.status == 0 {
		st.status = http.StatusOK
	}

	w.Header().Set("Content-Type", "application/json")

	if st.retry != "" {
		w.Header().Set("Retry-After", st.retry)
	}

	w.WriteHeader(st.status)
	_, _ = io.WriteString(w, st.body)
}

func records(t *testing.T, reqs []*http.Request) []reqRecord {
	t.Helper()

	out := make([]reqRecord, 0, len(reqs))

	for _, r := range reqs {
		var body string

		if r.Body != nil {
			b, _ := io.ReadAll(r.Body)
			body = string(b)
		}

		out = append(out, reqRecord{r.Method, r.URL.EscapedPath(), body})
	}

	return out
}

// bodyHandler records bodies server side, since the transport's clone of a
// request body is already consumed.
type bodyHandler struct {
	seq    *sequence
	bodies []string
}

func (b *bodyHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	data, _ := io.ReadAll(r.Body)
	b.bodies = append(b.bodies, string(data))
	b.seq.ServeHTTP(w, r)
}

func setup(t *testing.T, steps ...step) (*Client, func() []reqRecord) {
	t.Helper()

	h := &bodyHandler{seq: &sequence{t: t, steps: steps}}
	c, reqs := newTestClient(t, "github.com", h)

	return c, func() []reqRecord {
		out := records(t, reqs())
		if len(out) != len(h.bodies) {
			t.Fatalf("recorded %d requests but %d bodies", len(out), len(h.bodies))
		}

		for i := range out {
			out[i].body = h.bodies[i]
		}

		return out
	}
}

func assertJSON(t *testing.T, got, want string) {
	t.Helper()

	var g, w any
	if err := json.Unmarshal([]byte(got), &g); err != nil {
		t.Fatalf("body %q is not JSON: %v", got, err)
	}

	if err := json.Unmarshal([]byte(want), &w); err != nil {
		t.Fatal(err)
	}

	if !reflect.DeepEqual(g, w) {
		t.Errorf("body = %s, want %s", got, want)
	}
}

func TestCreatePR(t *testing.T) {
	c, reqs := setup(t, step{status: 201, body: string(readFixture(t, "create_pr.json"))})

	got, err := c.CreatePR(t.Context(), testRepo, NewPR{Title: "Add the thing", Body: "details", Base: "main", Head: "feat/x"})
	if err != nil {
		t.Fatal(err)
	}

	want := PR{
		Number: 42, Title: "Add the thing", URL: "https://github.com/octo/repo/pull/42", Author: "monalisa",
		State: "OPEN", HeadBranch: "feat/x", HeadOID: "abc123", BaseBranch: "main",
		UpdatedAt: time.Date(2026, 10, 27, 10, 0, 0, 0, time.UTC),
	}
	if got != want {
		t.Errorf("PR = %+v, want %+v", got, want)
	}

	rs := reqs()
	if len(rs) != 1 || rs[0].method != "POST" || rs[0].path != "/repos/octo/repo/pulls" {
		t.Fatalf("requests = %+v", rs)
	}

	assertJSON(t, rs[0].body, `{"title":"Add the thing","body":"details","base":"main","head":"feat/x","draft":false}`)
}

func TestCreatePRDraft(t *testing.T) {
	c, reqs := setup(t, step{status: 201, body: `{"number":7,"state":"open","draft":true,"user":null,
		"head":{"ref":"h","sha":"s","repo":null},"base":{"ref":"main","repo":{"full_name":"octo/repo"}}}`})

	got, err := c.CreatePR(t.Context(), testRepo, NewPR{Title: "t", Base: "main", Head: "h", Draft: true})
	if err != nil {
		t.Fatal(err)
	}

	if !got.Draft || !got.CrossRepo || got.Author != "ghost" {
		t.Errorf("PR = %+v, want draft, cross-repo (null head repo) and ghost author", got)
	}

	rs := reqs()
	if len(rs) != 1 {
		t.Fatalf("requests = %+v", rs)
	}

	assertJSON(t, rs[0].body, `{"title":"t","body":"","base":"main","head":"h","draft":true}`)
}

func TestCreatePRCrossRepoByName(t *testing.T) {
	c, _ := setup(t, step{status: 201, body: `{"number":7,"state":"open","user":{"login":"u"},
		"head":{"ref":"h","sha":"s","repo":{"full_name":"fork/repo"}},"base":{"ref":"main","repo":{"full_name":"octo/repo"}}}`})

	got, err := c.CreatePR(t.Context(), testRepo, NewPR{Title: "t", Base: "main", Head: "fork:h"})
	if err != nil {
		t.Fatal(err)
	}

	if !got.CrossRepo {
		t.Errorf("CrossRepo = false, want true")
	}
}

func TestCreatePRExists(t *testing.T) {
	body := `{"message":"Validation Failed","errors":[{"resource":"PullRequest","code":"custom","message":"A pull request already exists for octo:feat/x."}]}`
	c, _ := setup(t, step{status: 422, body: body})

	_, err := c.CreatePR(t.Context(), testRepo, NewPR{Title: "t", Base: "main", Head: "feat/x"})
	if !errors.Is(err, ErrPRExists) {
		t.Fatalf("err = %v, want ErrPRExists", err)
	}

	var he *api.HTTPError
	if !errors.As(err, &he) || he.StatusCode != 422 {
		t.Errorf("HTTPError not reachable: %v", err)
	}
}

func TestCreatePROtherValidationError(t *testing.T) {
	c, _ := setup(t, step{status: 422, body: `{"message":"Validation Failed","errors":[{"resource":"PullRequest","code":"custom","message":"No commits between main and feat/x"}]}`})

	_, err := c.CreatePR(t.Context(), testRepo, NewPR{Title: "t", Base: "main", Head: "feat/x"})
	if err == nil || errors.Is(err, ErrPRExists) {
		t.Fatalf("err = %v, want a plain error", err)
	}
}

func TestCreatePRRateLimited(t *testing.T) {
	c, _ := setup(t, step{status: 429, body: `{"message":"slow down"}`, retry: "30"})

	_, err := c.CreatePR(t.Context(), testRepo, NewPR{Title: "t", Base: "main", Head: "h"})
	if !errors.Is(err, ErrRateLimited) {
		t.Fatalf("err = %v, want ErrRateLimited", err)
	}
}

func TestApproveSendsCommit(t *testing.T) {
	c, reqs := setup(t, step{body: `{"id":1}`})

	if err := c.Approve(t.Context(), testRepo, 42, "abc"); err != nil {
		t.Fatal(err)
	}

	rs := reqs()
	if len(rs) != 1 || rs[0].method != "POST" || rs[0].path != "/repos/octo/repo/pulls/42/reviews" {
		t.Fatalf("requests = %+v", rs)
	}

	assertJSON(t, rs[0].body, `{"event":"APPROVE","commit_id":"abc"}`)
}

func TestApproveWithoutSHASendsNothing(t *testing.T) {
	c, reqs := setup(t)

	if err := c.Approve(t.Context(), testRepo, 42, ""); !errors.Is(err, ErrNoSHA) {
		t.Fatalf("err = %v, want ErrNoSHA", err)
	}

	if n := len(reqs()); n != 0 {
		t.Errorf("%d requests sent", n)
	}
}

func TestApproveBadNumberSendsNothing(t *testing.T) {
	c, reqs := setup(t)

	if err := c.Approve(t.Context(), testRepo, 0, "abc"); err == nil {
		t.Error("no error")
	}

	if got := len(reqs()); got != 0 {
		t.Errorf("%d requests sent", got)
	}
}

func mergeablePR() PR {
	return PR{Number: 42, HeadBranch: "feat/x", HeadOID: "abc", BaseBranch: "main"}
}

var mergeInfo = RepoInfo{DefaultBranch: "main", Methods: []MergeMethod{MergeCommit, Squash, Rebase}}

func TestMergeSendsSHAAndMethod(t *testing.T) {
	for _, m := range []MergeMethod{MergeCommit, Squash, Rebase} {
		c, reqs := setup(t, step{body: string(readFixture(t, "merge.json"))})

		got, err := c.Merge(t.Context(), testRepo, mergeablePR(), mergeInfo, MergeOptions{Method: m, SHA: "abc"})
		if err != nil {
			t.Fatal(err)
		}

		if got.SHA != "6dcb09b5b57875f334f61aebed695e2e4193db5e" || got.BranchDeleted || got.DeleteErr != nil {
			t.Errorf("Merged = %+v", got)
		}

		rs := reqs()
		if len(rs) != 1 || rs[0].method != "PUT" || rs[0].path != "/repos/octo/repo/pulls/42/merge" {
			t.Fatalf("requests = %+v", rs)
		}

		assertJSON(t, rs[0].body, `{"merge_method":"`+string(m)+`","sha":"abc"}`)
	}
}

func TestMergeWithoutSHASendsNothing(t *testing.T) {
	c, reqs := setup(t)

	_, err := c.Merge(t.Context(), testRepo, mergeablePR(), mergeInfo, MergeOptions{Method: Squash})
	if !errors.Is(err, ErrNoSHA) {
		t.Fatalf("err = %v, want ErrNoSHA", err)
	}

	if n := len(reqs()); n != 0 {
		t.Errorf("%d requests sent", n)
	}
}

func TestMergeUnknownMethod(t *testing.T) {
	for _, m := range []MergeMethod{"", "SQUASH"} {
		c, reqs := setup(t)

		_, err := c.Merge(t.Context(), testRepo, mergeablePR(), mergeInfo, MergeOptions{Method: m, SHA: "abc"})
		if err == nil {
			t.Errorf("method %q: no error", m)
		}

		if n := len(reqs()); n != 0 {
			t.Errorf("method %q: %d requests sent", m, n)
		}
	}
}

func TestMergeBadNumberSendsNothing(t *testing.T) {
	c, reqs := setup(t)
	pr := mergeablePR()
	pr.Number = 0

	if _, err := c.Merge(t.Context(), testRepo, pr, mergeInfo, MergeOptions{Method: Squash, SHA: "abc"}); err == nil {
		t.Error("no error")
	}

	if n := len(reqs()); n != 0 {
		t.Errorf("%d requests sent", n)
	}
}

func TestMergeHeadMoved(t *testing.T) {
	c, reqs := setup(t, step{status: 409, body: `{"message":"Head branch was modified. Review and try the merge again."}`})

	_, err := c.Merge(t.Context(), testRepo, mergeablePR(), mergeInfo, MergeOptions{Method: Squash, SHA: "abc", DeleteBranch: true})
	if !errors.Is(err, ErrHeadMoved) {
		t.Fatalf("err = %v, want ErrHeadMoved", err)
	}

	var he *api.HTTPError
	if !errors.As(err, &he) || he.StatusCode != 409 {
		t.Errorf("HTTPError not reachable: %v", err)
	}

	if n := len(reqs()); n != 1 {
		t.Errorf("%d requests, want 1 (no delete after a failed merge)", n)
	}
}

func TestMergeNotMergeable(t *testing.T) {
	c, _ := setup(t, step{status: 405, body: `{"message":"Pull Request is not mergeable"}`})

	_, err := c.Merge(t.Context(), testRepo, mergeablePR(), mergeInfo, MergeOptions{Method: Squash, SHA: "abc"})
	if !errors.Is(err, ErrNotMergeable) {
		t.Fatalf("err = %v, want ErrNotMergeable", err)
	}

	if !strings.Contains(err.Error(), "Pull Request is not mergeable") {
		t.Errorf("message lost: %v", err)
	}
}

func TestMergeRateLimited(t *testing.T) {
	c, _ := setup(t, step{status: 429, body: `{"message":"slow down"}`, retry: "30"})

	_, err := c.Merge(t.Context(), testRepo, mergeablePR(), mergeInfo, MergeOptions{Method: Squash, SHA: "abc"})
	if !errors.Is(err, ErrRateLimited) || errors.Is(err, ErrHeadMoved) {
		t.Fatalf("err = %v, want ErrRateLimited", err)
	}
}

func TestMergeOtherStatusUnchanged(t *testing.T) {
	c, _ := setup(t, step{status: 500, body: `{"message":"boom"}`})

	_, err := c.Merge(t.Context(), testRepo, mergeablePR(), mergeInfo, MergeOptions{Method: Squash, SHA: "abc"})
	if err == nil || errors.Is(err, ErrHeadMoved) || errors.Is(err, ErrNotMergeable) {
		t.Fatalf("err = %v, want a plain error", err)
	}
}

func TestMergeDeletesBranch(t *testing.T) {
	c, reqs := setup(t, step{body: string(readFixture(t, "merge.json"))}, step{status: 204})
	pr := mergeablePR()
	pr.HeadBranch = "feat/a b#c"

	got, err := c.Merge(t.Context(), testRepo, pr, mergeInfo, MergeOptions{Method: Squash, SHA: "abc", DeleteBranch: true})
	if err != nil {
		t.Fatal(err)
	}

	if !got.BranchDeleted || got.DeleteErr != nil || got.SHA == "" {
		t.Errorf("Merged = %+v", got)
	}

	rs := reqs()
	if len(rs) != 2 {
		t.Fatalf("requests = %+v", rs)
	}

	if rs[1].method != "DELETE" || rs[1].path != "/repos/octo/repo/git/refs/heads/feat/a%20b%23c" {
		t.Errorf("delete = %+v", rs[1])
	}
}

func TestMergeEscapesRepoSegments(t *testing.T) {
	c, reqs := setup(t, step{body: string(readFixture(t, "merge.json"))})
	r := Repo{Host: "github.com", Owner: "o wner", Name: "re?po"}

	if _, err := c.Merge(t.Context(), r, mergeablePR(), mergeInfo, MergeOptions{Method: Squash, SHA: "abc"}); err != nil {
		t.Fatal(err)
	}

	rs := reqs()
	if len(rs) != 1 || rs[0].path != "/repos/o%20wner/re%3Fpo/pulls/42/merge" {
		t.Errorf("requests = %+v", rs)
	}
}

func TestMergeSkipsDeleteForFork(t *testing.T) {
	c, reqs := setup(t, step{body: string(readFixture(t, "merge.json"))})
	pr := mergeablePR()
	pr.CrossRepo = true

	got, err := c.Merge(t.Context(), testRepo, pr, mergeInfo, MergeOptions{Method: Squash, SHA: "abc", DeleteBranch: true})
	if err != nil {
		t.Fatal(err)
	}

	if got.BranchDeleted || got.DeleteErr != nil || len(reqs()) != 1 {
		t.Errorf("Merged = %+v, requests = %+v", got, reqs())
	}
}

func TestMergeSkipsDeleteForDefaultBranch(t *testing.T) {
	c, reqs := setup(t, step{body: string(readFixture(t, "merge.json"))})
	pr := mergeablePR()
	pr.HeadBranch = "main"

	got, err := c.Merge(t.Context(), testRepo, pr, mergeInfo, MergeOptions{Method: Squash, SHA: "abc", DeleteBranch: true})
	if err != nil {
		t.Fatal(err)
	}

	if got.BranchDeleted || got.DeleteErr != nil || len(reqs()) != 1 {
		t.Errorf("Merged = %+v, requests = %+v", got, reqs())
	}
}

func TestMergeDeleteAlreadyGone(t *testing.T) {
	for _, status := range []int{422, 404} {
		c, _ := setup(t, step{body: string(readFixture(t, "merge.json"))}, step{status: status, body: `{"message":"Reference does not exist"}`})

		got, err := c.Merge(t.Context(), testRepo, mergeablePR(), mergeInfo, MergeOptions{Method: Squash, SHA: "abc", DeleteBranch: true})
		if err != nil {
			t.Fatal(err)
		}

		if !got.BranchDeleted || got.DeleteErr != nil {
			t.Errorf("status %d: Merged = %+v", status, got)
		}
	}
}

func TestMergeDeleteFails(t *testing.T) {
	c, _ := setup(t, step{body: string(readFixture(t, "merge.json"))}, step{status: 500, body: `{"message":"boom"}`})

	got, err := c.Merge(t.Context(), testRepo, mergeablePR(), mergeInfo, MergeOptions{Method: Squash, SHA: "abc", DeleteBranch: true})
	if err != nil {
		t.Fatalf("err = %v, want the merge reported as done", err)
	}

	if got.SHA == "" || got.BranchDeleted || got.DeleteErr == nil {
		t.Errorf("Merged = %+v", got)
	}
}

func TestMergeSkipsDeleteWhenDefaultBranchUnknown(t *testing.T) {
	c, reqs := setup(t, step{body: string(readFixture(t, "merge.json"))})

	got, err := c.Merge(t.Context(), testRepo, mergeablePR(), RepoInfo{}, MergeOptions{Method: Squash, SHA: "abc", DeleteBranch: true})
	if err != nil {
		t.Fatal(err)
	}

	if got.BranchDeleted || got.DeleteErr != nil || len(reqs()) != 1 {
		t.Errorf("Merged = %+v, requests = %+v", got, reqs())
	}
}

func TestMergeNotMergedResponse(t *testing.T) {
	c, reqs := setup(t, step{body: `{"merged":false,"message":"Required status checks are pending"}`})

	_, err := c.Merge(t.Context(), testRepo, mergeablePR(), mergeInfo, MergeOptions{Method: Squash, SHA: "abc", DeleteBranch: true})
	if !errors.Is(err, ErrNotMergeable) {
		t.Fatalf("err = %v, want ErrNotMergeable", err)
	}

	if !strings.Contains(err.Error(), "Required status checks are pending") {
		t.Errorf("message lost: %v", err)
	}

	if n := len(reqs()); n != 1 {
		t.Errorf("%d requests, want 1 (no delete)", n)
	}
}
