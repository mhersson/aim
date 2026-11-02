package ui

import (
	"context"
	"errors"
	"runtime"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
)

// fakeOpen records the links the app opens, so no test starts a browser.
type fakeOpen struct {
	mu   sync.Mutex
	got  []string
	fail error
	hold bool // block until the context ends
}

func (f *fakeOpen) open(ctx context.Context, url string) error {
	f.mu.Lock()
	f.got = append(f.got, url)
	fail, hold := f.fail, f.hold
	f.mu.Unlock()

	if hold {
		<-ctx.Done()

		return ctx.Err()
	}

	return fail
}

func (f *fakeOpen) urls() []string {
	f.mu.Lock()
	defer f.mu.Unlock()

	return slices.Clone(f.got)
}

func TestBrowserArgv(t *testing.T) {
	t.Parallel()

	const link = "https://github.com/o/r/pull/7"

	argv, err := browserArgv(link)

	switch runtime.GOOS {
	case "linux":
		if err != nil || !slices.Equal(argv, []string{"xdg-open", link}) {
			t.Errorf("browserArgv = %v, %v, want xdg-open with the link", argv, err)
		}
	case "darwin":
		if err != nil || !slices.Equal(argv, []string{"open", link}) {
			t.Errorf("browserArgv = %v, %v, want open with the link", argv, err)
		}
	default:
		if err == nil {
			t.Errorf("browserArgv = %v, want an error on %s", argv, runtime.GOOS)
		}
	}
}

func TestOpenURLRefuses(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name, url string
	}{
		{"javascript", "javascript:alert(1)"},
		{"file with a host", "file://host/etc/passwd"}, // reaches the scheme check
		{"control character", "https://github.com/o/r\x1b[31m"},
		{"space", "https://github.com/o r"},
		{"invisible", "https://github.com/o\xe2\x80\x8b/r"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			a := newTestApp(t)
			f := &fakeOpen{}
			a.openLink = f.open

			if cmd := a.openURL(tc.url); cmd != nil {
				t.Errorf("openURL returned a command for %q", tc.url)
			}

			if a.notice != noticeLinkRefused {
				t.Errorf("notice = %q, want the refusal", a.notice)
			}

			if got := f.urls(); len(got) != 0 {
				t.Errorf("the opener was called with %v", got)
			}
		})
	}
}

func TestOpenURLOpensWebLinks(t *testing.T) {
	t.Parallel()

	for _, link := range []string{"https://github.com/o/r/pull/7", "http://ci.example.com:8080/job?id=3#log"} {
		a := newTestApp(t)
		f := &fakeOpen{}
		a.openLink = f.open

		cmd := a.openURL(link)
		if cmd == nil {
			t.Fatalf("openURL(%q) refused a web link: %q", link, a.notice)
		}

		if msg := cmd(); msg != nil {
			t.Errorf("a successful open raised %v", msg)
		}

		if got := f.urls(); !slices.Equal(got, []string{link}) {
			t.Errorf("opened %v, want %q", got, link)
		}
	}
}

func TestOpenURLReportsAFailure(t *testing.T) {
	t.Parallel()

	a := newTestApp(t)
	fail := errors.New("exec: \"xdg-open\": executable file not found in $PATH")
	a.openLink = (&fakeOpen{fail: fail}).open

	msg := a.openURL("https://github.com/o/r")()
	if _, ok := msg.(browserDoneMsg); !ok {
		t.Fatalf("a failed open raised %T, want browserDoneMsg", msg)
	}

	a.Update(msg)

	if a.noticeLevel() != levelError || !strings.Contains(a.notice, gitReason(fail)) {
		t.Errorf("notice = %q at level %v, want an error carrying %q", a.notice, a.noticeLevel(), gitReason(fail))
	}
}

func TestOpenURLUsesTheAppContext(t *testing.T) {
	t.Parallel()

	a := newTestApp(t)
	f := &fakeOpen{hold: true}
	a.openLink = f.open

	done := make(chan tea.Msg, 1)

	go func() { done <- a.openURL("https://github.com/o/r")() }()

	deadline := time.After(10 * time.Second)

	for len(f.urls()) == 0 {
		select {
		case <-deadline:
			t.Fatal("the opener never started")
		case <-time.After(time.Millisecond):
		}
	}

	select {
	case <-done:
		t.Fatal("the opener returned before the app closed")
	default:
	}

	_ = a.Close(context.Background())

	select {
	case msg := <-done:
		if msg != nil {
			t.Errorf("closing the app raised %v, want nothing", msg)
		}
	case <-deadline:
		t.Fatal("the opener did not return after the app closed")
	}
}

func TestOpenBrowserReportsACancelledContext(t *testing.T) {
	t.Parallel()

	if runtime.GOOS != "linux" && runtime.GOOS != "darwin" {
		t.Skip("no opener on this platform")
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	if err := openBrowser(ctx, "https://github.com/o/r"); !errors.Is(err, context.Canceled) {
		t.Errorf("openBrowser on a cancelled context = %v, want context.Canceled", err)
	}
}
