package github

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
)

func TestParseRemoteURL(t *testing.T) {
	tests := []struct {
		name string
		raw  string
		want Repo
		err  bool
	}{
		{name: "scp-like", raw: "git@github.com:o/r.git", want: Repo{"github.com", "o", "r"}},
		{name: "scp-like without user", raw: "github.com:o/r", want: Repo{"github.com", "o", "r"}},
		{name: "ssh with port", raw: "ssh://git@github.com:22/o/r", want: Repo{"github.com", "o", "r"}},
		{name: "https upper case trailing slash", raw: "https://GitHub.com/o/r/", want: Repo{"github.com", "o", "r"}},
		{name: "https userinfo dropped", raw: "https://user:secret@github.com/o/r.git", want: Repo{"github.com", "o", "r"}}, //nolint:gosec // credentials are the point of the case
		{name: "http", raw: "http://github.com/o/r", want: Repo{"github.com", "o", "r"}},
		{name: "git protocol", raw: "git://github.com/o/r", want: Repo{"github.com", "o", "r"}},
		{name: "enterprise host", raw: "https://ghe.corp.example/o/r.git", want: Repo{"ghe.corp.example", "o", "r"}},
		{name: "ssh subdomain", raw: "git@ssh.github.com:o/r.git", want: Repo{"github.com", "o", "r"}},
		{name: "www subdomain", raw: "https://www.github.com/o/r", want: Repo{"github.com", "o", "r"}},
		{name: "local path", raw: "/srv/repo.git", err: true},
		{name: "relative path", raw: "../a:b/c", err: true},
		{name: "file url", raw: "file:///x", err: true},
		{name: "one segment", raw: "https://github.com/o", err: true},
		{name: "three segments", raw: "https://github.com/o/r/extra", err: true},
		{name: "empty", raw: "", err: true},
		{name: "empty name", raw: "https://github.com/o/.git", err: true},
		{name: "unknown scheme", raw: "ftp://github.com/o/r", err: true},
		{name: "secret in rejected url", raw: "https://user:secret@github.com/o", err: true}, //nolint:gosec // credentials are the point of the case
		{name: "scp-like userinfo in the owner", raw: "user:tok@host:o/r", err: true},
		{name: "scp-like userinfo and port", raw: "user:tok@github.com:o/r", err: true},
		{name: "owner with a colon", raw: "github.com:a:b/r", err: true},
		{name: "name with a quote", raw: "https://github.com/o/r'x", err: true},
		{name: "owner with an at sign", raw: "https://github.com/o@x/r", err: true},
		{name: "option-like host", raw: "-oProxyCommand=x:o/r", err: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ParseRemoteURL(tt.raw)
			if tt.err {
				if !errors.Is(err, ErrNotGitHub) {
					t.Fatalf("err = %v, want ErrNotGitHub", err)
				}

				if strings.Contains(err.Error(), "secret") || strings.Contains(err.Error(), "tok") {
					t.Fatal("error carries the remote's credentials")
				}

				return
			}

			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			if got != tt.want {
				t.Fatalf("repo = %+v, want %+v", got, tt.want)
			}
		})
	}
}

func TestRepoString(t *testing.T) {
	if got := (Repo{"github.com", "o", "r"}).String(); got != "github.com/o/r" {
		t.Fatalf("String = %q", got)
	}
}

// fakeSSH writes an ssh stand-in that prints out, counts its runs in a file
// and records its arguments. It returns the script and the counter paths.
func fakeSSH(t *testing.T, body string) (script, runs, args string) {
	t.Helper()
	dir := t.TempDir()
	script = filepath.Join(dir, "ssh")
	runs = filepath.Join(dir, "runs")
	args = filepath.Join(dir, "args")

	src := "#!/bin/sh\necho x >> '" + runs + "'\necho \"$@\" >> '" + args + "'\n" + body

	// A concurrent fork can inherit the write fd and make exec fail with ETXTBSY (go.dev/issue/22315).
	syscall.ForkLock.RLock()

	err := os.WriteFile(script, []byte(src), 0o755)

	syscall.ForkLock.RUnlock()

	if err != nil {
		t.Fatal(err)
	}

	return script, runs, args
}

func stubHosts(hosts ...string) func() []string { return func() []string { return hosts } }

func TestResolveSSHAlias(t *testing.T) {
	ssh, _, args := fakeSSH(t, "printf 'user git\\nhostname github.com\\nport 22\\n'\n")
	r := NewResolver(ResolverOptions{SSH: ssh, KnownHosts: stubHosts()})

	got, err := r.Resolve(t.Context(), "git@work-gh:o/r.git")
	if err != nil {
		t.Fatal(err)
	}

	if want := (Repo{"github.com", "o", "r"}); got != want {
		t.Fatalf("repo = %+v, want %+v", got, want)
	}

	b, _ := os.ReadFile(args)
	if strings.TrimSpace(string(b)) != "-G -- work-gh" {
		t.Fatalf("ssh args = %q, want exactly -G -- work-gh", b)
	}
}

func TestResolveSSHSubdomainHostname(t *testing.T) {
	ssh, _, _ := fakeSSH(t, "echo hostname ssh.github.com\n")
	r := NewResolver(ResolverOptions{SSH: ssh, KnownHosts: stubHosts()})

	got, err := r.Resolve(t.Context(), "gh-alias:o/r")
	if err != nil {
		t.Fatal(err)
	}

	if got.Host != "github.com" {
		t.Fatalf("host = %q, want github.com", got.Host)
	}
}

func TestResolveSSHFailureKeepsAlias(t *testing.T) {
	ssh, _, _ := fakeSSH(t, "exit 255\n")
	r := NewResolver(ResolverOptions{SSH: ssh, KnownHosts: stubHosts("ghe.corp.example")})

	got, err := r.Resolve(t.Context(), "git@ghe.corp.example:o/r.git")
	if err != nil {
		t.Fatal(err)
	}

	if want := (Repo{"ghe.corp.example", "o", "r"}); got != want {
		t.Fatalf("repo = %+v, want %+v", got, want)
	}

	_, err = r.Resolve(t.Context(), "git@unknown-alias:o/r.git")
	if !errors.Is(err, ErrNotGitHub) {
		t.Fatalf("err = %v, want ErrNotGitHub", err)
	}
}

func TestResolveSSHContextEndIsAnError(t *testing.T) {
	t.Parallel()

	ssh, _, args := fakeSSH(t, "exec sleep 600\n")
	r := NewResolver(ResolverOptions{SSH: ssh, KnownHosts: stubHosts()})

	// The fake never answers; the cancel comes once it has started.
	_, err := r.Resolve(cancelWhenWritten(t, args), "git@work-gh:o/r.git")
	if !errors.Is(err, context.Canceled) || errors.Is(err, ErrNotGitHub) {
		t.Fatalf("err = %v, want the context's error and not ErrNotGitHub", err)
	}
}

func TestResolveUnknownHostIsNotGitHub(t *testing.T) {
	ssh, _, _ := fakeSSH(t, "exit 1\n")
	r := NewResolver(ResolverOptions{SSH: ssh, KnownHosts: stubHosts("GHE.corp.example")})

	ctx := context.Background()
	if _, err := r.Resolve(ctx, "https://gitlab.com/o/r"); !errors.Is(err, ErrNotGitHub) {
		t.Fatalf("gitlab err = %v, want ErrNotGitHub", err)
	}

	got, err := r.Resolve(ctx, "https://ghe.corp.example/o/r")
	if err != nil || got.Host != "ghe.corp.example" {
		t.Fatalf("ghe = %+v, %v", got, err)
	}

	if _, err := r.Resolve(ctx, "https://github.com/o/r"); err != nil {
		t.Fatalf("github.com err = %v", err)
	}

	if _, err := r.Resolve(ctx, "/srv/repo.git"); !errors.Is(err, ErrNotGitHub) {
		t.Fatalf("local path err = %v, want ErrNotGitHub", err)
	}
}

func TestResolveNeverPassesOptionLikeAlias(t *testing.T) {
	ssh, runs, _ := fakeSSH(t, "echo hostname github.com\n")

	r := NewResolver(ResolverOptions{SSH: ssh, KnownHosts: stubHosts()})
	if _, err := r.Resolve(t.Context(), "ssh://-oProxyCommand=x/o/r"); !errors.Is(err, ErrNotGitHub) {
		t.Fatalf("err = %v, want ErrNotGitHub", err)
	}

	if _, err := os.Stat(runs); err == nil {
		t.Fatal("ssh ran for an option-like alias")
	}
}

func TestResolveCachesAliases(t *testing.T) {
	ssh, runs, _ := fakeSSH(t, "echo hostname github.com\n")

	r := NewResolver(ResolverOptions{SSH: ssh, KnownHosts: stubHosts()})
	for range 2 {
		if _, err := r.Resolve(t.Context(), "git@work-gh:o/r.git"); err != nil {
			t.Fatal(err)
		}
	}

	b, _ := os.ReadFile(runs)
	if n := strings.Count(string(b), "x"); n != 1 {
		t.Fatalf("ssh ran %d times, want 1", n)
	}
}

func TestResolveHTTPSDoesNotRunSSH(t *testing.T) {
	ssh, runs, _ := fakeSSH(t, "echo hostname other.example\n")

	r := NewResolver(ResolverOptions{SSH: ssh, KnownHosts: stubHosts()})
	if _, err := r.Resolve(t.Context(), "https://github.com/o/r"); err != nil {
		t.Fatal(err)
	}

	if _, err := os.Stat(runs); err == nil {
		t.Fatal("ssh ran for an https remote")
	}
}
