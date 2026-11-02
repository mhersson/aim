package github

import (
	"errors"
	"fmt"
	"net/http"
	"testing"
	"time"

	"github.com/cli/go-gh/v2/pkg/api"
)

func TestClassify(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	reset := time.Unix(1_700_000_900, 0)

	hdr := func(kv map[string]string) http.Header {
		h := http.Header{}
		for k, v := range kv {
			h.Set(k, v)
		}

		return h
	}

	tests := []struct {
		name      string
		err       error
		wantReset time.Time // zero = not a rate limit
	}{
		{
			"403 with an exhausted limit",
			&api.HTTPError{StatusCode: 403, Headers: hdr(map[string]string{"X-RateLimit-Remaining": "0", "X-RateLimit-Reset": "1700000900"})},
			reset,
		},
		{
			"429 with Retry-After",
			&api.HTTPError{StatusCode: 429, Headers: hdr(map[string]string{"Retry-After": "30"})},
			now.Add(30 * time.Second),
		},
		{
			"403 with Retry-After",
			&api.HTTPError{StatusCode: 403, Headers: hdr(map[string]string{"Retry-After": "5"})},
			now.Add(5 * time.Second),
		},
		{
			"wrapped rate limit",
			fmt.Errorf("outer: %w", &api.HTTPError{StatusCode: 429, Headers: hdr(map[string]string{"Retry-After": "30"})}),
			now.Add(30 * time.Second),
		},
		{
			"graphql RATE_LIMITED",
			&api.GraphQLError{Errors: []api.GraphQLErrorItem{{Type: "RATE_LIMITED", Message: "slow down"}}},
			now.Add(time.Minute),
		},
		{
			"403 secondary limit without headers",
			&api.HTTPError{StatusCode: 403, Message: "You have exceeded a Secondary Rate Limit. Please wait.", Headers: http.Header{}},
			now.Add(time.Minute),
		},
		{"404 mentioning a secondary limit", &api.HTTPError{StatusCode: 404, Message: "secondary rate limit", Headers: http.Header{}}, time.Time{}},
		{"403 without headers", &api.HTTPError{StatusCode: 403, Headers: http.Header{}}, time.Time{}},
		{"403 with remaining left", &api.HTTPError{StatusCode: 403, Headers: hdr(map[string]string{"X-RateLimit-Remaining": "10"})}, time.Time{}},
		{
			"graphql error beside data",
			&api.GraphQLError{Errors: []api.GraphQLErrorItem{{Type: "NOT_FOUND", Message: "gone"}}},
			time.Time{},
		},
		{"plain error", errors.New("boom"), time.Time{}},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := classify(tc.err, now)

			var rl *RateLimitError

			isRate := errors.As(got, &rl)
			if isRate != !tc.wantReset.IsZero() {
				t.Fatalf("rate limit = %v, want %v (%v)", isRate, !tc.wantReset.IsZero(), got)
			}

			if isRate {
				if !rl.Reset.Equal(tc.wantReset) {
					t.Errorf("reset = %v, want %v", rl.Reset, tc.wantReset)
				}

				if !errors.Is(got, ErrRateLimited) {
					t.Error("not errors.Is ErrRateLimited")
				}
			} else if errors.Is(got, ErrRateLimited) {
				t.Error("errors.Is ErrRateLimited for a non-rate-limit")
			}

			switch tc.err.(type) { //nolint:errorlint // the table holds bare API errors
			case *api.HTTPError:
				if _, ok := errors.AsType[*api.HTTPError](got); !ok {
					t.Error("original *api.HTTPError lost")
				}
			case *api.GraphQLError:
				if _, ok := errors.AsType[*api.GraphQLError](got); !ok {
					t.Error("original *api.GraphQLError lost")
				}
			}
		})
	}

	if classify(nil, now) != nil {
		t.Error("classify(nil) != nil")
	}
}
