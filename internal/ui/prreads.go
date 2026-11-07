package ui

import (
	"context"
	"time"

	"github.com/mhersson/aim/internal/github"
)

// prReads is every GitHub read the pull request views make beyond the poller.
// cmd/aim fills it from its client cache, picking the client by host.
type prReads interface {
	MyPRs(ctx context.Context, host string, since time.Time) (github.MyPRs, error)
	JobLog(ctx context.Context, r github.Repo, jobID int64) (data []byte, cut bool, err error)
	CheckRun(ctx context.Context, r github.Repo, id int64) (github.CheckRunOutput, error)
	Conversation(ctx context.Context, r github.Repo, number int) (github.Conversation, error)
	MergeBase(ctx context.Context, r github.Repo, base, head string) (string, error)
	GitToken(ctx context.Context, host string) (string, error)
}
