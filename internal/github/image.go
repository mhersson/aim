package github

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
)

var (
	// ErrNotImageHost is returned for a URL that may not load; no request is made.
	ErrNotImageHost = errors.New("github: not an image host")
	// ErrImageTooLarge is returned for an image over the fetch limit.
	ErrImageTooLarge = errors.New("github: image too large")
)

const maxImageRedirects = 5

// ImageHost reports whether rawURL may be fetched: https with no user info, on
// port 443 or none, at github.com, a .githubusercontent.com host or docHost,
// and not an .svg path. Host comparisons ignore case.
func ImageHost(rawURL, docHost string) bool {
	u, err := url.Parse(rawURL)
	if err != nil || u.Scheme != "https" || u.User != nil {
		return false
	}

	if p := u.Port(); p != "" && p != "443" {
		return false
	}

	if strings.HasSuffix(strings.ToLower(u.Path), ".svg") {
		return false
	}

	h := strings.ToLower(u.Hostname())

	switch {
	case h == "":
		return false
	case h == "github.com", strings.HasSuffix(h, ".githubusercontent.com"):
		return true
	}

	return docHost != "" && h == strings.ToLower(docHost)
}

// ImageOptions are the seams of an ImageFetcher.
type ImageOptions struct {
	Token     func(ctx context.Context, host string) (string, error) // nil = Token with default options
	Transport http.RoundTripper                                      // nil = http.DefaultTransport
}

// ImageFetcher fetches images from GitHub's hosts, sending the document host's
// token to that host only.
type ImageFetcher struct {
	token     func(ctx context.Context, host string) (string, error)
	transport http.RoundTripper

	mu     sync.Mutex
	tokens map[string]string
}

// NewImageFetcher returns a fetcher; it looks up each host's token once.
func NewImageFetcher(o ImageOptions) *ImageFetcher {
	f := &ImageFetcher{token: o.Token, transport: o.Transport, tokens: map[string]string{}}
	if f.token == nil {
		f.token = func(ctx context.Context, host string) (string, error) {
			return Token(ctx, host, TokenOptions{})
		}
	}

	if f.transport == nil {
		f.transport = http.DefaultTransport
	}

	return f
}

// hostToken is host's token, or "" when there is none; a failed lookup counts
// as none and is not retried, unless ctx ended it.
func (f *ImageFetcher) hostToken(ctx context.Context, host string) string {
	f.mu.Lock()
	defer f.mu.Unlock()

	if t, ok := f.tokens[host]; ok {
		return t
	}

	t, err := f.token(ctx, host)
	if err != nil {
		if ctx.Err() != nil {
			return "" // a cancelled lookup says nothing about the host
		}

		t = ""
	}

	f.tokens[host] = t

	return t
}

// onHost reports whether u is the document host's https endpoint, where the
// token may go.
func onHost(u *url.URL, docHost string) bool {
	if docHost == "" || u.Scheme != "https" {
		return false
	}

	if p := u.Port(); p != "" && p != "443" {
		return false
	}

	return strings.EqualFold(u.Hostname(), docHost)
}

// Fetch returns the image at rawURL, at most limit bytes. The token goes only
// to docHost and is dropped for good once a redirect has left it.
func (f *ImageFetcher) Fetch(ctx context.Context, rawURL, docHost string, limit int64) ([]byte, error) {
	if !ImageHost(rawURL, docHost) {
		return nil, ErrNotImageHost
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, fmt.Errorf("fetch image: %w", err)
	}

	req.Header.Set("User-Agent", "aim")

	if onHost(req.URL, docHost) {
		if t := f.hostToken(ctx, docHost); t != "" {
			req.Header.Set("Authorization", "token "+t)
		}
	}

	client := &http.Client{
		Transport: f.transport,
		CheckRedirect: func(next *http.Request, via []*http.Request) error {
			if len(via) > maxImageRedirects {
				return errors.New("too many redirects")
			}

			if next.URL.Scheme != "https" {
				return errors.New("redirect to a non-https URL")
			}

			// net/http copies the first request's headers to each hop.
			keep := onHost(next.URL, docHost)
			for _, v := range via {
				keep = keep && onHost(v.URL, docHost)
			}

			if !keep {
				next.Header.Del("Authorization")
			}

			return nil
		},
	}

	resp, err := client.Do(req)
	if err != nil {
		if ue, ok := errors.AsType[*url.Error](err); ok {
			err = ue.Err // url.Error would print the URL, which may be signed
		}

		return nil, fmt.Errorf("fetch image: %w", err)
	}

	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("fetch image: status %d", resp.StatusCode)
	}

	if resp.ContentLength > limit {
		return nil, ErrImageTooLarge
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, limit+1))
	if err != nil {
		return nil, fmt.Errorf("fetch image: read: %w", err)
	}

	if int64(len(body)) > limit {
		return nil, ErrImageTooLarge
	}

	return body, nil
}
