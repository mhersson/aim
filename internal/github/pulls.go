package github

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"strings"
	"time"

	graphql "github.com/cli/shurcooL-graphql"
)

// CheckState is the combined state of a set of checks, or of one check.
type CheckState string

const (
	CheckNone    CheckState = ""
	CheckPending CheckState = "pending"
	CheckSuccess CheckState = "success"
	CheckFailure CheckState = "failure"
	CheckSkipped CheckState = "skipped" // one check only: neutral, skipped or stale
)

// Checks summarises the check runs and status contexts of a head commit.
type Checks struct {
	State               CheckState
	Total, Done, Failed int
}

// PR is a pull request as the poll sees it.
type PR struct {
	ID                              string // the node id GraphQL mutations take
	Number                          int
	Title, URL, Author              string
	State                           string // OPEN, MERGED, CLOSED
	Draft, CrossRepo                bool
	ViewerAuthored                  bool // the token's user wrote it; REST answers leave it false
	HeadBranch, HeadOID, BaseBranch string
	ReviewDecision, MergeState      string // GitHub's enums as given; "" when null
	Checks                          Checks
	UpdatedAt                       time.Time
}

// MergeMethod is a way GitHub can merge a pull request.
type MergeMethod string

const (
	MergeCommit MergeMethod = "merge"
	Squash      MergeMethod = "squash"
	Rebase      MergeMethod = "rebase"
)

// RepoInfo holds the repository settings a merge needs.
type RepoInfo struct {
	DefaultBranch       string
	Methods             []MergeMethod // allowed, in the order merge, squash, rebase
	DeleteBranchOnMerge bool
}

// RateLimit is the GraphQL budget left after a query.
type RateLimit struct {
	Remaining int
	Reset     time.Time
}

type stateCount struct {
	State string
	Count int
}

type rollup struct {
	State    string
	Contexts struct {
		CheckRunCount              int
		CheckRunCountsByState      []stateCount
		StatusContextCount         int
		StatusContextCountsByState []stateCount
	} `graphql:"contexts(first: 1)"` // the counts are totals; a connection needs a page size
}

type pullNode struct {
	ID                string `graphql:"id"`
	Number            int
	Title             string
	URL               string `graphql:"url"`
	IsDraft           bool
	State             string
	IsCrossRepository bool
	HeadRefName       string
	HeadRefOid        string
	BaseRefName       string
	UpdatedAt         time.Time
	ReviewDecision    string
	MergeStateStatus  string
	Author            *struct{ Login string }
	ViewerDidAuthor   bool
	Commits           struct {
		Nodes []struct {
			Commit struct {
				StatusCheckRollup *rollup
			}
		}
	} `graphql:"commits(last: 1)"`
}

type pullsQuery struct {
	Repository struct {
		DefaultBranchRef    *struct{ Name string }
		MergeCommitAllowed  bool
		SquashMergeAllowed  bool
		RebaseMergeAllowed  bool
		DeleteBranchOnMerge bool
		PullRequests        struct{ Nodes []pullNode } `graphql:"pullRequests(states: OPEN, first: 100, orderBy: {field: UPDATED_AT, direction: DESC})"`
	} `graphql:"repository(owner: $owner, name: $name)"`
	RateLimit struct {
		Remaining int
		ResetAt   time.Time
	}
}

type branchQuery struct {
	Repository struct {
		PullRequests struct{ Nodes []pullNode } `graphql:"pullRequests(headRefName: $branch, first: 10, orderBy: {field: CREATED_AT, direction: DESC}, states: [OPEN, MERGED, CLOSED])"`
	} `graphql:"repository(owner: $owner, name: $name)"`
}

// PullRequests returns the repository's open pull requests, newest update
// first, with its merge settings and the GraphQL budget left.
func (c *Client) PullRequests(ctx context.Context, r Repo) (RepoInfo, []PR, RateLimit, error) {
	var q pullsQuery

	vars := map[string]any{"owner": graphql.String(r.Owner), "name": graphql.String(r.Name)}
	if err := c.query(ctx, "PullRequests", &q, vars); err != nil {
		return RepoInfo{}, nil, RateLimit{}, err
	}

	repo := q.Repository

	info := RepoInfo{DeleteBranchOnMerge: repo.DeleteBranchOnMerge}
	if repo.DefaultBranchRef != nil {
		info.DefaultBranch = repo.DefaultBranchRef.Name
	}

	for _, m := range []struct {
		allowed bool
		method  MergeMethod
	}{
		{repo.MergeCommitAllowed, MergeCommit},
		{repo.SquashMergeAllowed, Squash},
		{repo.RebaseMergeAllowed, Rebase},
	} {
		if m.allowed {
			info.Methods = append(info.Methods, m.method)
		}
	}

	prs := make([]PR, 0, len(repo.PullRequests.Nodes))
	for _, n := range repo.PullRequests.Nodes {
		prs = append(prs, n.pr())
	}

	return info, prs, RateLimit{Remaining: q.RateLimit.Remaining, Reset: q.RateLimit.ResetAt}, nil
}

// PRForBranch returns the same-repository pull request for branch: the newest
// open one, else the newest of any state. A branch with none is ErrNoPR.
func (c *Client) PRForBranch(ctx context.Context, r Repo, branch string) (PR, error) {
	var q branchQuery

	vars := map[string]any{
		"owner": graphql.String(r.Owner), "name": graphql.String(r.Name), "branch": graphql.String(branch),
	}
	if err := c.query(ctx, "PRForBranch", &q, vars); err != nil {
		return PR{}, err
	}

	var found *pullNode

	for i, n := range q.Repository.PullRequests.Nodes {
		if n.IsCrossRepository {
			continue
		}

		if n.State == "OPEN" {
			return n.pr(), nil
		}

		if found == nil {
			found = &q.Repository.PullRequests.Nodes[i]
		}
	}

	if found == nil {
		return PR{}, ErrNoPR
	}

	return found.pr(), nil
}

func (n pullNode) pr() PR {
	pr := PR{
		ID: n.ID, Number: n.Number, Title: n.Title, URL: n.URL, Author: "ghost",
		State: n.State, Draft: n.IsDraft, CrossRepo: n.IsCrossRepository,
		HeadBranch: n.HeadRefName, HeadOID: n.HeadRefOid, BaseBranch: n.BaseRefName,
		ReviewDecision: n.ReviewDecision, MergeState: n.MergeStateStatus,
		UpdatedAt: n.UpdatedAt, ViewerAuthored: n.ViewerDidAuthor,
	}

	if n.Author != nil && n.Author.Login != "" {
		pr.Author = n.Author.Login
	}

	if len(n.Commits.Nodes) > 0 {
		if ru := n.Commits.Nodes[0].Commit.StatusCheckRollup; ru != nil {
			pr.Checks = ru.checks()
		}
	}

	return pr
}

func (ru rollup) checks() Checks {
	ctxs := ru.Contexts
	total := ctxs.CheckRunCount + ctxs.StatusContextCount

	pending := countStates(ctxs.CheckRunCountsByState, "QUEUED", "IN_PROGRESS", "PENDING", "WAITING") +
		countStates(ctxs.StatusContextCountsByState, "PENDING", "EXPECTED")
	failed := countStates(ctxs.CheckRunCountsByState, "FAILURE", "TIMED_OUT", "CANCELLED", "ACTION_REQUIRED", "STARTUP_FAILURE") +
		countStates(ctxs.StatusContextCountsByState, "FAILURE", "ERROR")

	return Checks{State: rollupState(ru.State), Total: total, Done: total - pending, Failed: failed}
}

func countStates(counts []stateCount, states ...string) int {
	n := 0

	for _, c := range counts {
		for _, s := range states {
			if c.State == s {
				n += c.Count
			}
		}
	}

	return n
}

func rollupState(s string) CheckState {
	switch s {
	case "SUCCESS":
		return CheckSuccess
	case "FAILURE", "ERROR":
		return CheckFailure
	case "PENDING", "EXPECTED":
		return CheckPending
	default:
		return CheckNone
	}
}

// MergeBase is the sha of the commit where head branched from base. per_page=1
// pages only the commits; the file list still comes whole.
func (c *Client) MergeBase(ctx context.Context, r Repo, base, head string) (string, error) {
	segs := strings.Split(base, "/")
	for i, s := range segs {
		segs[i] = url.PathEscape(s)
	}

	var out struct {
		MergeBaseCommit struct {
			SHA string `json:"sha"`
		} `json:"merge_base_commit"`
	}

	path := repoPath(r) + "/compare/" + strings.Join(segs, "/") + "..." + url.PathEscape(head) + "?per_page=1"
	if err := c.rest(ctx, http.MethodGet, path, nil, &out); err != nil {
		return "", err
	}

	if out.MergeBaseCommit.SHA == "" {
		return "", errors.New("github: compare " + base + "..." + head + " has no merge base")
	}

	return out.MergeBaseCommit.SHA, nil
}
