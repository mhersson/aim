package github

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
)

const (
	imgToken = "SECRET123"
	imgLimit = 5 << 20
)

func TestImageHost(t *testing.T) {
	t.Parallel()

	tests := []struct {
		url, doc string
		want     bool
	}{
		{"https://github.com/user-attachments/assets/x", "", true},
		{"https://private-user-images.githubusercontent.com/1/a.png", "", true},
		{"https://ghe.example.com/a.png", "ghe.example.com", true},
		{"https://ghe.example.com/a.png", "GHE.example.com", true},
		{"https://github.com:443/a.png", "", true},
		{"http://github.com/a.png", "", false},
		{"https://github.com/a.SVG", "", false},
		{"https://u:p@github.com/a.png", "", false},
		{"https://github.com:8443/a.png", "", false},
		{"https://ghe.example.com/a.png", "", false},
		{"notaurl", "", false},
	}
	for _, tc := range tests {
		t.Run(tc.url+"|"+tc.doc, func(t *testing.T) {
			t.Parallel()

			if got := ImageHost(tc.url, tc.doc); got != tc.want {
				t.Errorf("ImageHost = %v, want %v", got, tc.want)
			}
		})
	}
}

type imgHit struct{ auth, ua string }

type imgRig struct {
	mu      sync.Mutex
	hits    map[string][]imgHit
	servers map[string]*httptest.Server
	handler map[string]http.HandlerFunc
	tr      *http.Transport
	tokens  atomic.Int32
}

func newImgRig(t *testing.T, hosts ...string) *imgRig {
	t.Helper()

	r := &imgRig{hits: map[string][]imgHit{}, servers: map[string]*httptest.Server{}, handler: map[string]http.HandlerFunc{}}

	for _, h := range hosts {
		r.servers[h] = httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, q *http.Request) {
			r.mu.Lock()
			r.hits[h] = append(r.hits[h], imgHit{q.Header.Get("Authorization"), q.Header.Get("User-Agent")})
			fn := r.handler[h]
			r.mu.Unlock()

			if fn == nil {
				_, _ = w.Write([]byte("png"))

				return
			}

			fn(w, q)
		}))
		t.Cleanup(r.servers[h].Close)
	}

	r.tr = &http.Transport{
		TLSClientConfig: &tls.Config{InsecureSkipVerify: true}, //nolint:gosec // test servers' certificates name no fake host
		DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
			host, _, _ := net.SplitHostPort(addr)

			s, ok := r.servers[host]
			if !ok {
				return nil, fmt.Errorf("no route to %s", addr)
			}

			return (&net.Dialer{}).DialContext(ctx, network, s.Listener.Addr().String())
		},
	}
	t.Cleanup(r.tr.CloseIdleConnections)

	return r
}

func (r *imgRig) fetcher() *ImageFetcher {
	return NewImageFetcher(ImageOptions{
		Transport: r.tr,
		Token: func(context.Context, string) (string, error) {
			r.tokens.Add(1)

			return imgToken, nil
		},
	})
}

func (r *imgRig) seen(host string) []imgHit {
	r.mu.Lock()
	defer r.mu.Unlock()

	return append([]imgHit(nil), r.hits[host]...)
}

func redirectTo(loc string) http.HandlerFunc {
	return func(w http.ResponseWriter, q *http.Request) {
		http.Redirect(w, q, loc, http.StatusFound)
	}
}

func TestFetchToken(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name, url, doc, host, wantAuth string
	}{
		{"document host", "https://github.com/a.png", "github.com", "github.com", "token " + imgToken},
		{"other github host", "https://objects.githubusercontent.com/a.png", "github.com", "objects.githubusercontent.com", ""},
		{"no document host", "https://github.com/a.png", "", "github.com", ""},
		{"different document host", "https://github.com/a.png", "ghe.example.com", "github.com", ""},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			r := newImgRig(t, "github.com", "objects.githubusercontent.com", "ghe.example.com")

			if _, err := r.fetcher().Fetch(t.Context(), tc.url, tc.doc, imgLimit); err != nil {
				t.Fatal(err)
			}

			hits := r.seen(tc.host)
			if len(hits) != 1 {
				t.Fatalf("hits = %d", len(hits))
			}

			if hits[0].auth != tc.wantAuth {
				t.Errorf("Authorization = %q, want %q", hits[0].auth, tc.wantAuth)
			}

			if hits[0].ua != "aim" {
				t.Errorf("User-Agent = %q", hits[0].ua)
			}
		})
	}
}

func TestFetchRedirect(t *testing.T) {
	t.Parallel()

	t.Run("to another host", func(t *testing.T) {
		t.Parallel()

		r := newImgRig(t, "github.com", "s3.example")
		r.handler["github.com"] = redirectTo("https://s3.example/x")

		body, err := r.fetcher().Fetch(t.Context(), "https://github.com/a.png", "github.com", imgLimit)
		if err != nil || string(body) != "png" {
			t.Fatalf("body %q, err %v", body, err)
		}

		if got := r.seen("s3.example"); len(got) != 1 || got[0].auth != "" {
			t.Errorf("s3 saw %+v", got)
		}
	})

	t.Run("to a subdomain", func(t *testing.T) {
		t.Parallel()

		r := newImgRig(t, "github.com", "assets.github.com")
		r.handler["github.com"] = redirectTo("https://assets.github.com/x")

		if _, err := r.fetcher().Fetch(t.Context(), "https://github.com/a.png", "github.com", imgLimit); err != nil {
			t.Fatal(err)
		}

		if got := r.seen("assets.github.com"); len(got) != 1 || got[0].auth != "" {
			t.Errorf("subdomain saw %+v", got)
		}
	})

	t.Run("to another port", func(t *testing.T) {
		t.Parallel()

		r := newImgRig(t, "github.com")
		r.handler["github.com"] = func(w http.ResponseWriter, q *http.Request) {
			if q.URL.Path == "/a.png" {
				http.Redirect(w, q, "https://github.com:8443/b.png", http.StatusFound)

				return
			}

			_, _ = w.Write([]byte("png"))
		}

		if _, err := r.fetcher().Fetch(t.Context(), "https://github.com/a.png", "github.com", imgLimit); err != nil {
			t.Fatal(err)
		}

		hits := r.seen("github.com")
		if len(hits) != 2 || hits[0].auth == "" || hits[1].auth != "" {
			t.Errorf("hits %+v", hits)
		}
	})

	t.Run("same host", func(t *testing.T) {
		t.Parallel()

		r := newImgRig(t, "github.com")
		r.handler["github.com"] = func(w http.ResponseWriter, q *http.Request) {
			if q.URL.Path == "/a.png" {
				http.Redirect(w, q, "https://github.com/b.png", http.StatusFound)

				return
			}

			_, _ = w.Write([]byte("png"))
		}

		if _, err := r.fetcher().Fetch(t.Context(), "https://github.com/a.png", "github.com", imgLimit); err != nil {
			t.Fatal(err)
		}

		hits := r.seen("github.com")
		if len(hits) != 2 || hits[1].auth != "token "+imgToken {
			t.Errorf("hits %+v", hits)
		}
	})

	t.Run("leaves and returns", func(t *testing.T) {
		t.Parallel()

		r := newImgRig(t, "github.com", "s3.example")
		r.handler["github.com"] = func(w http.ResponseWriter, q *http.Request) {
			if q.URL.Path == "/a.png" {
				http.Redirect(w, q, "https://s3.example/x", http.StatusFound)

				return
			}

			_, _ = w.Write([]byte("png"))
		}
		r.handler["s3.example"] = redirectTo("https://github.com/b.png")

		if _, err := r.fetcher().Fetch(t.Context(), "https://github.com/a.png", "github.com", imgLimit); err != nil {
			t.Fatal(err)
		}

		hits := r.seen("github.com")
		if len(hits) != 2 || hits[0].auth == "" || hits[1].auth != "" {
			t.Errorf("hits %+v", hits)
		}
	})

	t.Run("five redirects pass, six fail", func(t *testing.T) {
		t.Parallel()

		for _, n := range []int{5, 6} {
			r := newImgRig(t, "github.com")
			r.handler["github.com"] = func(w http.ResponseWriter, q *http.Request) {
				var i int

				_, _ = fmt.Sscanf(q.URL.Path, "/%d", &i)
				if i < n {
					http.Redirect(w, q, fmt.Sprintf("https://github.com/%d", i+1), http.StatusFound)

					return
				}

				_, _ = w.Write([]byte("png"))
			}

			_, err := r.fetcher().Fetch(t.Context(), "https://github.com/0", "github.com", imgLimit)
			if (n == 5) != (err == nil) {
				t.Errorf("%d redirects: err %v", n, err)
			}
		}
	})

	t.Run("to http", func(t *testing.T) {
		t.Parallel()

		r := newImgRig(t, "github.com")
		r.handler["github.com"] = redirectTo("http://s3.example/x")

		if _, err := r.fetcher().Fetch(t.Context(), "https://github.com/a.png", "github.com", imgLimit); err == nil {
			t.Error("want an error")
		}
	})
}

func TestFetchLimits(t *testing.T) {
	t.Parallel()

	const limit = 4 << 10

	sized := func(n int, chunked bool) http.HandlerFunc {
		return func(w http.ResponseWriter, _ *http.Request) {
			if !chunked {
				w.Header().Set("Content-Length", fmt.Sprint(n))
			}

			buf := make([]byte, 64<<10)
			for left := n; left > 0; {
				k := min(left, len(buf))
				if _, err := w.Write(buf[:k]); err != nil {
					return
				}

				if chunked {
					w.(http.Flusher).Flush()
				}

				left -= k
			}
		}
	}

	tests := []struct {
		name    string
		h       http.HandlerFunc
		url     string
		wantErr error
		wantStr string
		hits    int
	}{
		{"content length over", sized(limit+1, false), "https://github.com/a.png", ErrImageTooLarge, "", 1},
		{"chunked over", sized(limit+1, true), "https://github.com/a.png", ErrImageTooLarge, "", 1},
		{"exactly the limit", sized(limit, false), "https://github.com/a.png", nil, "", 1},
		{"not found", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(404) }, "https://github.com/a.png", nil, "404", 1},
		{"other host", nil, "https://img.shields.io/a.png", ErrNotImageHost, "", 0},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			r := newImgRig(t, "github.com", "img.shields.io")
			r.handler["github.com"] = tc.h

			body, err := r.fetcher().Fetch(t.Context(), tc.url, "github.com", limit)

			switch {
			case tc.wantErr != nil:
				if !errors.Is(err, tc.wantErr) {
					t.Errorf("err = %v", err)
				}
			case tc.wantStr != "":
				if err == nil || !strings.Contains(err.Error(), tc.wantStr) {
					t.Errorf("err = %v", err)
				}
			case err != nil || len(body) != limit:
				t.Errorf("len %d, err %v", len(body), err)
			}

			if got := len(r.seen("img.shields.io")) + len(r.seen("github.com")); got != tc.hits {
				t.Errorf("hits = %d, want %d", got, tc.hits)
			}
		})
	}
}

func TestFetchTokenSafe(t *testing.T) {
	t.Parallel()

	t.Run("cancelled lookup is not cached", func(t *testing.T) {
		t.Parallel()

		r := newImgRig(t, "github.com")
		f := NewImageFetcher(ImageOptions{
			Transport: r.tr,
			Token: func(ctx context.Context, _ string) (string, error) {
				if err := ctx.Err(); err != nil {
					return "", err
				}

				return imgToken, nil
			},
		})

		ctx, cancel := context.WithCancel(t.Context())
		cancel()

		_, _ = f.Fetch(ctx, "https://github.com/a.png", "github.com", imgLimit)

		if _, err := f.Fetch(t.Context(), "https://github.com/a.png", "github.com", imgLimit); err != nil {
			t.Fatal(err)
		}

		hits := r.seen("github.com")
		if len(hits) != 1 || hits[0].auth != "token "+imgToken {
			t.Errorf("hits %+v", hits)
		}
	})

	t.Run("server error", func(t *testing.T) {
		t.Parallel()

		r := newImgRig(t, "github.com")
		r.handler["github.com"] = func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(500) }
		f := r.fetcher()

		for range 2 {
			_, err := f.Fetch(t.Context(), "https://github.com/a.png", "github.com", imgLimit)
			if err == nil || strings.Contains(err.Error(), imgToken) {
				t.Errorf("err = %v", err)
			}
		}

		if n := r.tokens.Load(); n != 1 {
			t.Errorf("Token called %d times", n)
		}
	})

	t.Run("dial failure", func(t *testing.T) {
		t.Parallel()

		r := newImgRig(t) // no routes
		_, err := r.fetcher().Fetch(t.Context(), "https://github.com/a.png", "github.com", imgLimit)

		if err == nil || strings.Contains(err.Error(), imgToken) {
			t.Errorf("err = %v", err)
		}
	})

	t.Run("failed lookup", func(t *testing.T) {
		t.Parallel()

		r := newImgRig(t, "github.com")
		f := NewImageFetcher(ImageOptions{
			Transport: r.tr,
			Token:     func(context.Context, string) (string, error) { return "", ErrNoToken },
		})

		if _, err := f.Fetch(t.Context(), "https://github.com/a.png", "github.com", imgLimit); err != nil {
			t.Fatal(err)
		}

		if h := r.seen("github.com"); len(h) != 1 || h[0].auth != "" {
			t.Errorf("hits %+v", h)
		}
	})
}

func TestImageKey(t *testing.T) {
	t.Parallel()

	const signed = "https://private-user-images.githubusercontent.com/1/301-e8b9.png"

	tests := []struct{ name, in, want string }{
		{"the token goes", signed + "?jwt=abc", signed},
		{"other parameters stay", signed + "?x=1&jwt=abc&y=2", signed + "?x=1&y=2"},
		{"no token", signed + "?x=1", signed + "?x=1"},
		{"another host keeps its jwt", "https://example.com/a.png?jwt=abc", "https://example.com/a.png?jwt=abc"},
		{"a github host keeps its query", "https://raw.githubusercontent.com/a/b.png?jwt=abc", "https://raw.githubusercontent.com/a/b.png?jwt=abc"},
		{"a local path", "/repo/a.png", "/repo/a.png"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if got := ImageKey(tt.in); got != tt.want {
				t.Errorf("ImageKey(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}
