package ui

import (
	"context"

	"github.com/mhersson/aim/internal/github"
)

// prActions is every GitHub write the app makes. cmd/aim fills it from
// its client cache, picking the client by the repository's host.
type prActions interface {
	CreatePR(ctx context.Context, r github.Repo, n github.NewPR) (github.PR, error)
	Approve(ctx context.Context, r github.Repo, number int, sha string) error
	MarkReady(ctx context.Context, r github.Repo, id string) error
	Merge(ctx context.Context, r github.Repo, pr github.PR, info github.RepoInfo, m github.MergeOptions) (github.Merged, error)
}
