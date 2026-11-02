package core

import (
	"errors"
	"maps"
	"slices"
	"time"
)

// PRInfo is a pull request as the poll sees it. It mirrors the GitHub client's
// type so that core needs no import of it.
type PRInfo struct {
	Number                          int
	Title, URL, Author              string
	State                           string // OPEN, MERGED or CLOSED
	HeadBranch, HeadOID, BaseBranch string
	ReviewDecision, MergeState      string // GitHub's enums as given; "" when null
	Draft, CrossRepo                bool
	ViewerAuthored                  bool // the signed-in user opened it
	ChecksTotal, ChecksDone         int
	ChecksFailed                    int
	UpdatedAt                       time.Time
}

// CheckInfo is one check of a pull request. State is pending, success, failure
// or skipped.
type CheckInfo struct {
	Name, URL, State       string
	StartedAt, CompletedAt time.Time
	// ID and App are the run's id and the app that made it ("github-actions" for
	// an Actions job); a commit status has ID 0.
	ID  int64
	App string
}

// PRChecks is the checks of one pull request at one head commit.
type PRChecks struct {
	Number  int
	HeadOID string
	Checks  []CheckInfo
	Err     string // why the checks could not be read; Checks is then empty
}

// RepoPRs is what is known of one repository's pull requests. State copies
// share it, so an action replaces a RepoPRs and never edits one in place.
type RepoPRs struct {
	DefaultBranch string
	Open          []PRInfo // the last successful poll, newest update first
	Err           string   // the last poll's failure; "" after a success
	// Methods are the merge methods the repository allows, in the order merge,
	// squash, rebase.
	Methods             []string
	DeleteBranchOnMerge bool
	Focused             *PRChecks         // the focused pull request with its checks, when there is one
	Branch              map[string]PRInfo // per branch, the pull request a lookup found
}

// SetRepoPRs records a poll of the repository keyed "host/owner/name": its
// default branch, its open pull requests, merge methods, delete-branch setting,
// focused pull request's checks and the poll's error, replacing all of them.
// The branch lookups stay.
type SetRepoPRs struct {
	Repo          string
	DefaultBranch string
	Open          []PRInfo
	Err           string

	Methods             []string
	DeleteBranchOnMerge bool
	Focused             *PRChecks
}

func (a SetRepoPRs) apply(s *State) error {
	if a.Repo == "" {
		return errors.New("core: SetRepoPRs with an empty repository")
	}

	s.setRepoPRs(a.Repo, RepoPRs{
		DefaultBranch: a.DefaultBranch,
		Open:          slices.Clone(a.Open),

		Methods:             slices.Clone(a.Methods),
		DeleteBranchOnMerge: a.DeleteBranchOnMerge,
		Focused:             clonePRChecks(a.Focused),
		Err:                 a.Err,
		Branch:              s.PRs[a.Repo].Branch,
	})

	return nil
}

func clonePRChecks(c *PRChecks) *PRChecks {
	if c == nil {
		return nil
	}

	cp := *c
	cp.Checks = slices.Clone(c.Checks)

	return &cp
}

// SetBranchPR records the pull request a lookup found for a branch of the
// repository keyed "host/owner/name"; without Found it forgets the branch's.
type SetBranchPR struct {
	Repo, Branch string
	PR           PRInfo
	Found        bool
}

func (a SetBranchPR) apply(s *State) error {
	if a.Repo == "" || a.Branch == "" {
		return errors.New("core: SetBranchPR needs a repository and a branch")
	}

	r := s.PRs[a.Repo]

	if _, known := r.Branch[a.Branch]; !a.Found && !known {
		return nil
	}

	branch := maps.Clone(r.Branch)

	if a.Found {
		if branch == nil {
			branch = map[string]PRInfo{}
		}

		branch[a.Branch] = a.PR
	} else {
		delete(branch, a.Branch)
	}

	r.Branch = branch
	s.setRepoPRs(a.Repo, r)

	return nil
}

// setRepoPRs stores r under repo in a new map, since copies of the state share
// the old one.
func (s *State) setRepoPRs(repo string, r RepoPRs) {
	prs := maps.Clone(s.PRs)
	if prs == nil {
		prs = map[string]RepoPRs{}
	}

	prs[repo] = r
	s.PRs = prs
}

// OpenPRFor returns the newest same-repository open pull request of a branch
// in the repository keyed "host/owner/name", from the last poll alone. Newest is
// the highest number, which is the order of creation.
func (s State) OpenPRFor(repo, branch string) (PRInfo, bool) {
	var best PRInfo

	found := false

	for _, p := range s.PRs[repo].Open {
		if p.HeadBranch == branch && !p.CrossRepo && (!found || p.Number > best.Number) {
			best, found = p, true
		}
	}

	return best, found
}

// PRFor returns the pull request of a branch of the repository keyed
// "host/owner/name": the open one the last poll lists (see OpenPRFor), else the
// one a lookup found. A merged lookup record of the same number outranks the
// poll's entry.
func (s State) PRFor(repo, branch string) (PRInfo, bool) {
	if p, ok := s.OpenPRFor(repo, branch); ok {
		// A merge never reverts: the lookup's merged record outranks a poll
		// that still lists the pull request.
		if b, ok := s.PRs[repo].Branch[branch]; ok && b.Number == p.Number && b.State == "MERGED" && !b.CrossRepo {
			return b, true
		}

		return p, true
	}

	p, ok := s.PRs[repo].Branch[branch]
	if !ok || p.CrossRepo {
		return PRInfo{}, false
	}

	return p, true
}
