package github

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func fakeGH(t *testing.T, body string) (script, args string) {
	t.Helper()
	dir := t.TempDir()
	script = filepath.Join(dir, "gh")
	args = filepath.Join(dir, "args")

	src := "#!/bin/sh\necho \"$@\" > '" + args + "'\n" + body
	if err := os.WriteFile(script, []byte(src), 0o755); err != nil {
		t.Fatal(err)
	}

	return script, args
}

func TestTokenFromEnvOrConfig(t *testing.T) {
	gh, args := fakeGH(t, "echo from-gh\n")

	var asked string

	o := TokenOptions{
		GH: gh,
		FromEnvOrConfig: func(host string) string {
			asked = host

			return "env-tok"
		},
	}

	got, err := Token(t.Context(), "github.com", o)
	if err != nil || got != "env-tok" {
		t.Fatalf("token = %q, %v", got, err)
	}

	if asked != "github.com" {
		t.Fatalf("asked for %q", asked)
	}

	if _, err := os.Stat(args); err == nil {
		t.Fatal("gh ran although a token was found")
	}
}

func TestTokenFallsBackToGh(t *testing.T) {
	gh, args := fakeGH(t, "echo tok\n")
	o := TokenOptions{GH: gh, FromEnvOrConfig: func(string) string { return "" }}

	got, err := Token(t.Context(), "h", o)
	if err != nil || got != "tok" {
		t.Fatalf("token = %q, %v", got, err)
	}

	b, _ := os.ReadFile(args)
	if want := "auth token --secure-storage --hostname h"; strings.TrimSpace(string(b)) != want {
		t.Fatalf("gh args = %q, want %q", b, want)
	}
}

func TestTokenNone(t *testing.T) {
	o := TokenOptions{
		GH:              filepath.Join(t.TempDir(), "no-gh"),
		FromEnvOrConfig: func(string) string { return "" },
	}

	_, err := Token(t.Context(), "h", o)
	if !errors.Is(err, ErrNoToken) {
		t.Fatalf("err = %v, want ErrNoToken", err)
	}
}

func TestTokenGhFailureHidesStderr(t *testing.T) {
	gh, _ := fakeGH(t, "echo secret >&2\nexit 1\n")
	o := TokenOptions{GH: gh, FromEnvOrConfig: func(string) string { return "" }}

	_, err := Token(t.Context(), "h", o)
	if !errors.Is(err, ErrNoToken) {
		t.Fatalf("err = %v, want ErrNoToken", err)
	}

	if strings.Contains(err.Error(), "secret") {
		t.Fatal("error carries gh's stderr")
	}
}

func TestTokenGhEmptyOutput(t *testing.T) {
	gh, _ := fakeGH(t, "echo\n")

	o := TokenOptions{GH: gh, FromEnvOrConfig: func(string) string { return "" }}
	if _, err := Token(t.Context(), "h", o); !errors.Is(err, ErrNoToken) {
		t.Fatalf("err = %v, want ErrNoToken", err)
	}
}

// A gh left to run out exits cleanly with no token, so only a cancelled one
// fails with the context's error.
func TestTokenGhHangs(t *testing.T) {
	gh, args := fakeGH(t, "exec sleep 30\n")
	o := TokenOptions{GH: gh, FromEnvOrConfig: func(string) string { return "" }}

	_, err := Token(cancelWhenWritten(t, args), "h", o)
	if !errors.Is(err, ErrNoToken) || !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want ErrNoToken from the cancel", err)
	}
}

// cancelWhenWritten returns a context that is cancelled once path exists, so a
// test cancels a call at a known point rather than after a guessed delay.
func cancelWhenWritten(t *testing.T, path string) context.Context {
	t.Helper()

	ctx, cancel := context.WithCancel(t.Context())
	t.Cleanup(cancel)

	go func() {
		for ctx.Err() == nil {
			if _, err := os.Stat(path); err == nil {
				cancel()

				return
			}

			time.Sleep(time.Millisecond)
		}
	}()

	return ctx
}

func TestTokenRefusesAnEmptyHostBeforeAnyLookup(t *testing.T) {
	t.Setenv("GH_ENTERPRISE_TOKEN", "enterprise-tok")

	gh, args := fakeGH(t, "echo from-gh\n")

	for _, host := range []string{"", "-oProxyCommand=x"} {
		got, err := Token(t.Context(), host, TokenOptions{GH: gh})
		if !errors.Is(err, ErrNoToken) || got != "" {
			t.Errorf("host %q: token = %q, err = %v, want ErrNoToken", host, got, err)
		}
	}

	if _, err := os.Stat(args); err == nil {
		t.Error("gh ran for an unusable host")
	}
}
