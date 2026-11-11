package main

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"sync"
	"time"

	"github.com/mhersson/aim/internal/github"
)

// clientFactory builds the GitHub client of a host.
type clientFactory func(ctx context.Context, host string) (*github.Client, error)

// githubClients builds a client with the host's token from the environment,
// gh's configuration or `gh auth token`.
func githubClients(ctx context.Context, host string) (*github.Client, error) {
	token, err := github.Token(ctx, host, github.TokenOptions{})
	if err != nil {
		return nil, err
	}

	return github.NewClient(host, github.Options{Token: token})
}

// clientCache keeps the first client built for each host, so the poller and the
// branch lookups ask the factory (which may run gh) once per host. A failure is
// not kept, and a client whose token the host refused is built again, so a new
// login takes effect without a restart.
type clientCache struct {
	factory clientFactory

	mu      sync.Mutex
	clients map[string]*github.Client
}

// client returns the kept client for host, or builds one when none is kept or
// the host refused its token. The lock is not held while the factory runs; a
// lost race keeps the first client.
func (c *clientCache) client(ctx context.Context, host string) (*github.Client, error) {
	c.mu.Lock()
	kept := c.clients[host]
	c.mu.Unlock()

	if kept != nil && !kept.TokenRefused() {
		return kept, nil
	}

	built, err := c.factory(ctx, host)
	if err != nil {
		return nil, err
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	if kept := c.clients[host]; kept != nil && !kept.TokenRefused() {
		return kept, nil
	}

	c.clients[host] = built

	return built, nil
}

// prRuntime is the pull request poller and the branch lookup, built on one
// client cache.
type prRuntime struct {
	poller  *github.Poller
	clients *clientCache
}

// startPRs builds a poller that watches nothing until the app gives it a set.
func startPRs(clients clientFactory) *prRuntime {
	cache := &clientCache{factory: clients, clients: map[string]*github.Client{}}

	return &prRuntime{poller: github.NewPoller(github.PollerOptions{Client: cache.client}), clients: cache}
}

// actions is the GitHub writes, on the same client cache.
func (p *prRuntime) actions() ghActions { return ghActions{clients: p.clients} }

// reads is the GitHub reads beyond the poller, on the same client cache.
func (p *prRuntime) reads() ghReads { return ghReads{clients: p.clients} }

// lookup finds the pull request of a branch with the cached client of its host.
func (p *prRuntime) lookup(ctx context.Context, r github.Repo, branch string) (github.PR, error) {
	c, err := p.clients.client(ctx, r.Host)
	if err != nil {
		return github.PR{}, fmt.Errorf("client for %s: %w", r.Host, err)
	}

	return c.PRForBranch(ctx, r, branch)
}

// ghActions makes the GitHub writes of the app, each with the cached client of
// the repository's host.
type ghActions struct{ clients *clientCache }

func (g ghActions) client(ctx context.Context, r github.Repo) (*github.Client, error) {
	c, err := g.clients.client(ctx, r.Host)
	if err != nil {
		return nil, fmt.Errorf("client for %s: %w", r.Host, err)
	}

	return c, nil
}

// CreatePR opens a pull request.
func (g ghActions) CreatePR(ctx context.Context, r github.Repo, n github.NewPR) (github.PR, error) {
	c, err := g.client(ctx, r)
	if err != nil {
		return github.PR{}, err
	}

	return c.CreatePR(ctx, r, n)
}

// Approve approves a pull request at the commit sha.
func (g ghActions) Approve(ctx context.Context, r github.Repo, number int, sha string) error {
	c, err := g.client(ctx, r)
	if err != nil {
		return err
	}

	return c.Approve(ctx, r, number, sha)
}

// MarkReady marks the pull request with node id ready for review.
func (g ghActions) MarkReady(ctx context.Context, r github.Repo, id string) error {
	c, err := g.client(ctx, r)
	if err != nil {
		return err
	}

	return c.MarkReady(ctx, id)
}

// Merge merges a pull request at the commit m.SHA.
func (g ghActions) Merge(ctx context.Context, r github.Repo, pr github.PR, info github.RepoInfo, m github.MergeOptions) (github.Merged, error) {
	c, err := g.client(ctx, r)
	if err != nil {
		return github.Merged{}, err
	}

	return c.Merge(ctx, r, pr, info, m)
}

// ghReads makes the GitHub reads of the app, each with the cached client of the
// host.
type ghReads struct{ clients *clientCache }

// SignedInHosts are the hosts gh is signed in to.
func (ghReads) SignedInHosts(context.Context) []string { return github.SignedInHosts() }

// MyPRs searches the host for the viewer's open pull requests.
func (g ghReads) MyPRs(ctx context.Context, host string, since time.Time) (github.MyPRs, error) {
	c, err := g.clients.client(ctx, host)
	if err != nil {
		return github.MyPRs{}, fmt.Errorf("client for %s: %w", host, err)
	}

	return c.MyPRs(ctx, since)
}

// JobLog reads the tail of an Actions job's log.
func (g ghReads) JobLog(ctx context.Context, r github.Repo, jobID int64) (data []byte, cut bool, err error) {
	c, err := g.clients.client(ctx, r.Host)
	if err != nil {
		return nil, false, fmt.Errorf("client for %s: %w", r.Host, err)
	}

	return c.JobLog(ctx, r, jobID)
}

// CheckRun reads the output of a check run that is not an Actions job.
func (g ghReads) CheckRun(ctx context.Context, r github.Repo, id int64) (github.CheckRunOutput, error) {
	c, err := g.clients.client(ctx, r.Host)
	if err != nil {
		return github.CheckRunOutput{}, fmt.Errorf("client for %s: %w", r.Host, err)
	}

	return c.CheckRun(ctx, r, id)
}

// Conversation reads a pull request's description and timeline.
func (g ghReads) Conversation(ctx context.Context, r github.Repo, number int) (github.Conversation, error) {
	c, err := g.clients.client(ctx, r.Host)
	if err != nil {
		return github.Conversation{}, fmt.Errorf("client for %s: %w", r.Host, err)
	}

	return c.Conversation(ctx, r, number)
}

// MergeBase reads the commit where head branched from base.
func (g ghReads) MergeBase(ctx context.Context, r github.Repo, base, head string) (string, error) {
	c, err := g.clients.client(ctx, r.Host)
	if err != nil {
		return "", fmt.Errorf("client for %s: %w", r.Host, err)
	}

	return c.MergeBase(ctx, r, base, head)
}

// GitToken is the host's token, read at each call so a new login needs no restart.
func (g ghReads) GitToken(ctx context.Context, host string) (string, error) {
	return github.Token(ctx, host, github.TokenOptions{})
}

// Close stops the poller: its requests are cancelled, its goroutines end and
// its updates close. It is safe to repeat.
func (p *prRuntime) Close() error { return p.poller.Close() }

// closeRun closes the app, then the poller. The app's context ends what the
// app started (repository resolutions and branch lookups) first, so nothing
// asks a poller that is gone.
func closeRun(ctx context.Context, logger *slog.Logger, app interface{ Close(context.Context) error }, prs io.Closer) {
	if err := app.Close(ctx); err != nil {
		logger.Error("close panes", "err", err)
	}

	if err := prs.Close(); err != nil {
		logger.Warn("close poller", "err", err)
	}
}
