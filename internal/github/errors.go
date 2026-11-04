package github

import (
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/cli/go-gh/v2/pkg/api"
)

var (
	// ErrRateLimited is what a *RateLimitError matches.
	ErrRateLimited = errors.New("github: rate limited")
	// ErrNoSHA is returned by an approve or merge given no commit; nothing is sent.
	ErrNoSHA = errors.New("github: no commit given")
	// ErrNoNodeID is returned by a mark ready given no pull request node id; nothing is sent.
	ErrNoNodeID = errors.New("github: no pull request id given")
	// ErrHeadMoved is returned by a merge whose head commit changed since the review.
	ErrHeadMoved = errors.New("github: the head moved since the review")
	// ErrNotMergeable is returned by a merge GitHub refuses as not mergeable.
	ErrNotMergeable = errors.New("github: not mergeable")
	// ErrPRExists is returned by a create when the branch already has a pull request.
	ErrPRExists = errors.New("github: a pull request already exists")
	// ErrNoPR is returned when a branch has no pull request.
	ErrNoPR = errors.New("github: no pull request")
)

// rateLimitFallback is the wait assumed when a rate limit names no reset time.
const rateLimitFallback = time.Minute

// RateLimitError reports a primary or secondary rate limit; it matches
// ErrRateLimited and keeps the API error it came from reachable.
type RateLimitError struct {
	Reset time.Time
	cause error
}

func (e *RateLimitError) Error() string {
	return "github: rate limited until " + e.Reset.Format(time.RFC3339)
}

func (e *RateLimitError) Is(target error) bool { return target == ErrRateLimited }

func (e *RateLimitError) Unwrap() error { return e.cause }

// classify maps rate limits to *RateLimitError. Everything else is returned
// unchanged, so the original *api.HTTPError or *api.GraphQLError stays
// reachable with errors.As.
func classify(err error, now time.Time) error {
	return classifyAt(err, now, time.Time{})
}

// classifyAt is classify with the reset a response header gave for a GraphQL
// rate limit, which arrives as a 200 whose body carries the error. A zero
// reset means none was seen.
func classifyAt(err error, now, gqlReset time.Time) error {
	if err == nil {
		return nil
	}

	if _, ok := errors.AsType[*RateLimitError](err); ok {
		return err
	}

	if he, ok := errors.AsType[*api.HTTPError](err); ok {
		if reset, ok := httpRateLimitReset(he, now); ok {
			return &RateLimitError{Reset: reset, cause: err}
		}

		return err
	}

	if ge, ok := errors.AsType[*api.GraphQLError](err); ok {
		for _, item := range ge.Errors {
			if item.Type == "RATE_LIMITED" {
				reset := gqlReset
				if reset.IsZero() {
					reset = now.Add(rateLimitFallback)
				}

				return &RateLimitError{Reset: reset, cause: err}
			}
		}
	}

	return err
}

func httpRateLimitReset(he *api.HTTPError, now time.Time) (time.Time, bool) {
	if he.StatusCode != http.StatusForbidden && he.StatusCode != http.StatusTooManyRequests {
		return time.Time{}, false
	}

	if v := he.Headers.Get("Retry-After"); v != "" {
		if secs, err := strconv.Atoi(v); err == nil && secs >= 0 {
			return now.Add(time.Duration(secs) * time.Second), true
		}

		return now.Add(rateLimitFallback), true
	}

	if he.Headers.Get("X-RateLimit-Remaining") == "0" {
		if unix, err := strconv.ParseInt(he.Headers.Get("X-RateLimit-Reset"), 10, 64); err == nil {
			return time.Unix(unix, 0), true
		}

		return now.Add(rateLimitFallback), true
	}

	// A secondary limit may carry neither header.
	if strings.Contains(strings.ToLower(he.Message), "secondary rate limit") {
		return now.Add(rateLimitFallback), true
	}

	return time.Time{}, false
}

func wrapAPI(op string, err error, now time.Time) error {
	if err == nil {
		return nil
	}

	return fmt.Errorf("%s: %w", op, classify(err, now))
}
