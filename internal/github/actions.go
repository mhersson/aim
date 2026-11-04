package github

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/cli/go-gh/v2/pkg/api"
	graphql "github.com/cli/shurcooL-graphql"
)

// NewPR describes a pull request to create.
type NewPR struct {
	Title, Body, Base, Head string
	Draft                   bool
}

// MergeOptions say how to merge. SHA is the reviewed head commit; a merge
// without one is refused before any request.
type MergeOptions struct {
	Method       MergeMethod
	SHA          string // the reviewed head: git Result.Head
	DeleteBranch bool
}

// Merged is the outcome of a merge that went through. A failed branch delete
// does not undo it: DeleteErr carries that failure.
type Merged struct {
	SHA           string // the merge commit
	BranchDeleted bool
	DeleteErr     error
}

type restRef struct {
	Ref  string
	SHA  string
	Repo *struct {
		FullName string `json:"full_name"`
	}
}

type restPull struct {
	Number    int
	Title     string
	HTMLURL   string `json:"html_url"`
	State     string
	Draft     bool
	UpdatedAt time.Time `json:"updated_at"`
	User      *struct{ Login string }
	Head      restRef
	Base      restRef
}

func (p restPull) pr() PR {
	pr := PR{
		Number: p.Number, Title: p.Title, URL: p.HTMLURL, Author: "ghost",
		State: strings.ToUpper(p.State), Draft: p.Draft,
		HeadBranch: p.Head.Ref, HeadOID: p.Head.SHA, BaseBranch: p.Base.Ref,
		UpdatedAt: p.UpdatedAt,
	}

	if p.User != nil && p.User.Login != "" {
		pr.Author = p.User.Login
	}

	pr.CrossRepo = p.Head.Repo == nil || p.Base.Repo == nil || p.Head.Repo.FullName != p.Base.Repo.FullName

	return pr
}

// repoPath is repos/{owner}/{name} with each segment escaped.
func repoPath(r Repo) string {
	return "repos/" + url.PathEscape(r.Owner) + "/" + url.PathEscape(r.Name)
}

// CreatePR opens a pull request. A branch that already has one is ErrPRExists.
func (c *Client) CreatePR(ctx context.Context, r Repo, n NewPR) (PR, error) {
	body := map[string]any{"title": n.Title, "body": n.Body, "base": n.Base, "head": n.Head, "draft": n.Draft}

	var out restPull
	if err := c.rest(ctx, http.MethodPost, repoPath(r)+"/pulls", body, &out); err != nil {
		var he *api.HTTPError
		if errors.As(err, &he) && !errors.Is(err, ErrRateLimited) &&
			he.StatusCode == http.StatusUnprocessableEntity && mentionsExisting(he) {
			return PR{}, fmt.Errorf("%w: %w", ErrPRExists, err)
		}

		return PR{}, err
	}

	return out.pr(), nil
}

func mentionsExisting(he *api.HTTPError) bool {
	if strings.Contains(strings.ToLower(he.Message), "pull request already exists") {
		return true
	}

	for _, e := range he.Errors {
		if strings.Contains(strings.ToLower(e.Message), "pull request already exists") {
			return true
		}
	}

	return false
}

// Approve approves number at the reviewed commit sha. An empty sha or a
// non-positive number sends nothing.
func (c *Client) Approve(ctx context.Context, r Repo, number int, sha string) error {
	if sha == "" {
		return ErrNoSHA
	}

	if number <= 0 {
		return fmt.Errorf("github: approve: invalid pull request number %d", number)
	}

	body := map[string]string{"event": "APPROVE", "commit_id": sha}

	return c.rest(ctx, http.MethodPost, fmt.Sprintf("%s/pulls/%d/reviews", repoPath(r), number), body, nil)
}

// MarkReady marks the pull request with node id ready for review. An empty id
// sends nothing.
func (c *Client) MarkReady(ctx context.Context, id string) error {
	if id == "" {
		return ErrNoNodeID
	}

	var m struct {
		MarkPullRequestReadyForReview struct {
			PullRequest struct{ IsDraft bool }
		} `graphql:"markPullRequestReadyForReview(input: {pullRequestId: $id})"`
	}

	return c.mutate(ctx, "MarkReady", &m, map[string]any{"id": graphql.ID(id)})
}

// Merge merges pr at the reviewed commit m.SHA, so GitHub refuses it when the
// head moved. Every check runs before any request.
func (c *Client) Merge(ctx context.Context, r Repo, pr PR, info RepoInfo, m MergeOptions) (Merged, error) {
	if m.SHA == "" {
		return Merged{}, ErrNoSHA
	}

	switch m.Method {
	case MergeCommit, Squash, Rebase:
	default:
		return Merged{}, fmt.Errorf("github: merge: unknown merge method %q", m.Method)
	}

	if pr.Number <= 0 {
		return Merged{}, fmt.Errorf("github: merge: invalid pull request number %d", pr.Number)
	}

	var out struct {
		SHA     string
		Merged  bool
		Message string
	}

	body := map[string]string{"merge_method": string(m.Method), "sha": m.SHA}

	if err := c.rest(ctx, http.MethodPut, fmt.Sprintf("%s/pulls/%d/merge", repoPath(r), pr.Number), body, &out); err != nil {
		return Merged{}, mergeError(err)
	}

	if !out.Merged {
		return Merged{}, fmt.Errorf("%w: %s", ErrNotMergeable, out.Message)
	}

	done := Merged{SHA: out.SHA}

	// An empty branch builds a malformed ref path, and an unknown default
	// branch means the guard below cannot rule it out.
	if !m.DeleteBranch || pr.CrossRepo || pr.HeadBranch == "" ||
		info.DefaultBranch == "" || pr.HeadBranch == info.DefaultBranch {
		return done, nil
	}

	done.BranchDeleted, done.DeleteErr = c.deleteBranch(ctx, r, pr.HeadBranch)

	return done, nil
}

func mergeError(err error) error {
	var he *api.HTTPError
	if !errors.As(err, &he) || errors.Is(err, ErrRateLimited) {
		return err
	}

	switch he.StatusCode {
	case http.StatusConflict:
		return fmt.Errorf("%w: %w", ErrHeadMoved, err)
	case http.StatusMethodNotAllowed:
		return fmt.Errorf("%w: %w", ErrNotMergeable, err)
	default:
		return err
	}
}

// deleteBranch removes a head branch; one already gone counts as deleted.
func (c *Client) deleteBranch(ctx context.Context, r Repo, branch string) (bool, error) {
	segs := strings.Split(branch, "/")
	for i, s := range segs {
		segs[i] = url.PathEscape(s)
	}

	err := c.rest(ctx, http.MethodDelete, repoPath(r)+"/git/refs/heads/"+strings.Join(segs, "/"), nil, nil)
	if err == nil {
		return true, nil
	}

	var he *api.HTTPError
	if errors.As(err, &he) && !errors.Is(err, ErrRateLimited) &&
		(he.StatusCode == http.StatusNotFound || he.StatusCode == http.StatusUnprocessableEntity) {
		return true, nil
	}

	return false, err
}
