package github

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"sync"
	"time"
)

const (
	defaultFast       = 10 * time.Second
	defaultSlow       = 60 * time.Second
	defaultMaxBackoff = 5 * time.Minute
	maxRateLimitWait  = time.Hour
	updatesBuffer     = 16
)

// Watch is one repository to poll.
type Watch struct {
	Repo    Repo
	Visible []int // PR numbers on screen
	Focused int   // the PR whose checks are fetched over REST; 0 = none
}

// FocusedChecks are the checks of the focused PR's head commit.
type FocusedChecks struct {
	Number  int
	HeadOID string
	Checks  []Check
	Err     error // the checks could not be read; Checks is empty
}

// Snapshot is the result of one poll of one repository.
type Snapshot struct {
	Repo      Repo
	Info      RepoInfo
	PRs       []PR
	Focused   *FocusedChecks
	RateLimit RateLimit
	Err       error // the poll failed; Info, PRs and Focused are empty
	At        time.Time
}

// PollerOptions configure a Poller.
type PollerOptions struct {
	// Client returns the client for a host; a success is kept per host until
	// the host refuses its token.
	Client                 func(ctx context.Context, host string) (*Client, error)
	Fast, Slow, MaxBackoff time.Duration // 0 = 10 s, 60 s, 5 min

	// now and after are the clock the waits run on; nil is the real one.
	now   func() time.Time
	after func(time.Duration) <-chan time.Time
}

// Poller polls the open pull requests of every watched repository, one
// goroutine each, and sends a Snapshot per poll on Updates. It is safe for
// concurrent use.
type Poller struct {
	opts PollerOptions
	wg   sync.WaitGroup
	out  chan Snapshot
	done chan struct{} // closed once out is closed

	mu      sync.Mutex
	closed  bool
	repos   map[Repo]*repoPoller
	clients map[string]*Client
}

// repoPoller is the state one repository's goroutine shares with Set and
// Refresh.
type repoPoller struct {
	repo   Repo
	cancel context.CancelFunc // stops the goroutine and its in-flight request
	wake   chan struct{}      // cap 1: poll now

	mu    sync.Mutex
	watch Watch
}

// NewPoller returns a Poller that watches nothing until Set is called.
func NewPoller(o PollerOptions) *Poller {
	if o.Fast <= 0 {
		o.Fast = defaultFast
	}

	if o.Slow <= 0 {
		o.Slow = defaultSlow
	}

	if o.MaxBackoff <= 0 {
		o.MaxBackoff = defaultMaxBackoff
	}

	if o.now == nil {
		o.now = time.Now
	}

	if o.after == nil {
		o.after = time.After
	}

	return &Poller{
		opts:    o,
		out:     make(chan Snapshot, updatesBuffer),
		done:    make(chan struct{}),
		repos:   make(map[Repo]*repoPoller),
		clients: make(map[string]*Client),
	}
}

// Updates delivers snapshots until Close, which closes the channel.
func (p *Poller) Updates() <-chan Snapshot { return p.out }

// Set replaces the watched repositories. A new one is polled at once, a
// removed one stops, and one whose focused PR changed is polled at once.
func (p *Poller) Set(ws []Watch) {
	p.mu.Lock()
	defer p.mu.Unlock()

	if p.closed {
		return
	}

	next := make(map[Repo]Watch, len(ws))

	for _, w := range ws {
		w.Visible = append([]int(nil), w.Visible...)
		next[w.Repo] = w
	}

	for repo, rp := range p.repos {
		if _, ok := next[repo]; !ok {
			rp.cancel()
			delete(p.repos, repo)
		}
	}

	for repo, w := range next {
		if rp, ok := p.repos[repo]; ok {
			rp.mu.Lock()
			refocused := rp.watch.Focused != w.Focused
			rp.watch = w
			rp.mu.Unlock()

			if refocused {
				rp.poke()
			}

			continue
		}

		ctx, cancel := context.WithCancel(context.Background())
		rp := &repoPoller{repo: repo, cancel: cancel, wake: make(chan struct{}, 1), watch: w}
		p.repos[repo] = rp

		p.wg.Add(1)

		go p.run(ctx, rp)
	}
}

// Refresh polls a watched repository now; any other is ignored. During a rate
// limit wait the poll comes at the reset instead; after another error it still
// comes at once.
func (p *Poller) Refresh(r Repo) {
	p.mu.Lock()
	rp := p.repos[r]
	closed := p.closed
	p.mu.Unlock()

	if rp != nil && !closed {
		rp.poke()
	}
}

// Close cancels every request, waits for every goroutine and closes Updates.
// Every call returns only once Updates is closed, and is safe to repeat.
func (p *Poller) Close() error {
	p.mu.Lock()
	if p.closed {
		p.mu.Unlock()
		<-p.done

		return nil
	}

	p.closed = true

	for _, rp := range p.repos {
		rp.cancel()
	}

	p.mu.Unlock()

	p.wg.Wait()
	close(p.out)
	close(p.done)

	return nil
}

func (rp *repoPoller) poke() {
	select {
	case rp.wake <- struct{}{}:
	default:
	}
}

func (rp *repoPoller) current() Watch {
	rp.mu.Lock()
	defer rp.mu.Unlock()

	return rp.watch
}

func (p *Poller) run(ctx context.Context, rp *repoPoller) {
	defer p.wg.Done()

	var (
		failures  int
		wait      time.Duration // the first poll is immediate
		notBefore time.Time     // a rate limit's reset: no wake polls earlier
	)

	for {
		select {
		case <-ctx.Done():
			return
		case <-rp.wake:
			if !p.sleepUntil(ctx, notBefore) {
				return
			}
		case <-p.opts.after(wait):
		}

		snap, pending := p.poll(ctx, rp)
		if ctx.Err() != nil {
			return
		}

		now := p.opts.now()
		wait = p.nextWait(snap, pending, &failures, now)

		if _, limited := rateLimitOf(snap); limited {
			notBefore = now.Add(wait)
		} else {
			notBefore = time.Time{}
		}

		if ctx.Err() != nil {
			return
		}

		select {
		case p.out <- snap:
		case <-ctx.Done():
			return
		}
	}
}

// sleepUntil waits for t; false means ctx ended first.
func (p *Poller) sleepUntil(ctx context.Context, t time.Time) bool {
	d := t.Sub(p.opts.now())
	if d <= 0 {
		return true
	}

	select {
	case <-ctx.Done():
		return false
	case <-p.opts.after(d):
		return true
	}
}

// nextWait is how long to wait after a poll: the error backoff, the rate
// limit reset, or the cadence for the checks' state.
func (p *Poller) nextWait(s Snapshot, pending bool, failures *int, now time.Time) time.Duration {
	if s.Err == nil {
		*failures = 0
	}

	rl, limited := rateLimitOf(s)

	switch {
	case limited:
		return min(max(rl.Reset.Sub(now), p.opts.Fast), maxRateLimitWait)
	case s.Err == nil && pending:
		return p.opts.Fast
	case s.Err == nil:
		return p.opts.Slow
	}

	*failures++

	wait := p.opts.Fast
	for i := 1; i < *failures && wait < p.opts.MaxBackoff; i++ {
		wait *= 2
	}

	return min(wait, p.opts.MaxBackoff)
}

// rateLimitOf is the rate limit a poll met: its own, else the focused checks'.
func rateLimitOf(s Snapshot) (*RateLimitError, bool) {
	switch {
	case s.Err != nil:
		return errors.AsType[*RateLimitError](s.Err)
	case s.Focused != nil:
		return errors.AsType[*RateLimitError](s.Focused.Err)
	}

	return nil, false
}

// poll fetches one repository; pending reports whether a visible or focused
// PR still has checks running.
func (p *Poller) poll(ctx context.Context, rp *repoPoller) (snap Snapshot, pending bool) {
	w := rp.current()
	snap = Snapshot{Repo: rp.repo}

	fail := func(err error) (Snapshot, bool) {
		snap.Err = err
		snap.At = time.Now()

		return snap, false
	}

	c, err := p.client(ctx, rp.repo.Host)
	if err != nil {
		return fail(err)
	}

	info, prs, rl, err := c.PullRequests(ctx, rp.repo)
	if err != nil {
		return fail(err)
	}

	var focused *FocusedChecks

	for _, pr := range prs {
		if w.Focused == 0 || pr.Number != w.Focused || pr.HeadOID == "" {
			continue
		}

		// Checks the token may not read fail only themselves, so the pull
		// requests stay fresh.
		checks, err := c.Checks(ctx, rp.repo, pr.HeadOID)
		focused = &FocusedChecks{Number: pr.Number, HeadOID: pr.HeadOID, Checks: checks, Err: err}

		break
	}

	for _, pr := range prs {
		if (pr.Number == w.Focused || containsInt(w.Visible, pr.Number)) && checksPending(pr.Checks) {
			pending = true
		}
	}

	snap.Info, snap.PRs, snap.Focused, snap.RateLimit, snap.At = info, prs, focused, rl, time.Now()

	return snap, pending
}

func checksPending(c Checks) bool { return c.State == CheckPending || c.Done < c.Total }

func containsInt(s []int, v int) bool {
	return slices.Contains(s, v)
}

// client returns the kept client for host, or builds one when none is kept or
// the host refused its token. The lock is not held while the factory runs; a
// lost race keeps the first client.
func (p *Poller) client(ctx context.Context, host string) (*Client, error) {
	p.mu.Lock()
	c := p.clients[host]
	p.mu.Unlock()

	if c != nil && !c.TokenRefused() {
		return c, nil
	}

	if p.opts.Client == nil {
		return nil, errors.New("github: poller has no client factory")
	}

	c, err := p.opts.Client(ctx, host)
	if err != nil {
		return nil, fmt.Errorf("github: client for %s: %w", host, err)
	}

	if c == nil {
		return nil, fmt.Errorf("github: no client for %s", host)
	}

	p.mu.Lock()
	defer p.mu.Unlock()

	if kept := p.clients[host]; kept != nil && !kept.TokenRefused() {
		return kept, nil
	}

	p.clients[host] = c

	return c, nil
}
