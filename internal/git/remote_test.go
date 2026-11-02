package git

import (
	"context"
	"errors"
	"os"
	"slices"
	"strings"
	"testing"
)

func TestPushRemoteOrder(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		config  [][]string
		want    string
		wantErr error
	}{
		{"default origin", nil, "origin", nil},
		{"branch remote", [][]string{{"branch.feat.remote", "second"}}, "second", nil},
		{"dot ignored", [][]string{{"branch.feat.remote", "."}}, "origin", nil},
		{"push default over branch remote", [][]string{{"branch.feat.remote", "origin"}, {"remote.pushDefault", "second"}}, "second", nil},
		{"push remote over push default", [][]string{{"remote.pushDefault", "origin"}, {"branch.feat.pushRemote", "second"}}, "second", nil},
		{"not a remote", [][]string{{"branch.feat.pushRemote", "nope"}}, "", ErrNoRemote},
		{"option name", [][]string{{"branch.feat.pushRemote", "-x"}}, "", ErrNoRemote},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			root := newRepo(t)
			gitOut(t, root, "remote", "add", "origin", "/nonexistent/origin.git")
			gitOut(t, root, "remote", "add", "second", "/nonexistent/second.git")

			for _, kv := range tt.config {
				gitOut(t, root, "config", kv[0], kv[1])
			}

			got, err := PushRemote(context.Background(), openRepo(t, root), "feat")
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("err = %v, want %v", err, tt.wantErr)
			}

			if got != tt.want {
				t.Errorf("remote = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestRemoteURLAppliesInsteadOf(t *testing.T) {
	t.Parallel()
	root := newRepo(t)
	gitOut(t, root, "remote", "add", "origin", "gh:o/r.git")
	gitOut(t, root, "config", "url.https://example.invalid/.insteadOf", "gh:")

	got, err := RemoteURL(context.Background(), openRepo(t, root), "origin")
	if err != nil {
		t.Fatal(err)
	}

	if want := "https://example.invalid/o/r.git"; got != want {
		t.Errorf("url = %q, want %q", got, want)
	}

	if _, err := RemoteURL(context.Background(), openRepo(t, root), "missing"); !errors.Is(err, ErrNoRemote) {
		t.Errorf("missing remote: err = %v, want ErrNoRemote", err)
	}
}

func TestRemoteURLNeverEchoesItsArgument(t *testing.T) {
	t.Parallel()
	root := newRepo(t)
	arg := "https://user:secret@example.invalid/o/r.git" //nolint:gosec // G101: fixture URL, no real credential

	_, err := RemoteURL(context.Background(), openRepo(t, root), arg)
	if !errors.Is(err, ErrNoRemote) {
		t.Fatalf("err = %v, want ErrNoRemote", err)
	}

	for _, leak := range []string{"secret", "example.invalid"} {
		if strings.Contains(err.Error(), leak) {
			t.Errorf("error %q contains %q", err, leak)
		}
	}
}

func TestPushURLs(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		config [][]string
		want   []string
	}{
		{"the fetch url without a push url", nil, []string{"https://example.invalid/o/r.git"}},
		{
			"a rewrite of the push url",
			[][]string{{"url./srv/git/.pushInsteadOf", "https://example.invalid/"}},
			[]string{"/srv/git/o/r.git"},
		},
		{"a push url with a space", [][]string{{"remote.origin.pushurl", "/srv/my git/r.git"}}, []string{"/srv/my git/r.git"}},
		{
			"two push urls in order",
			[][]string{{"remote.origin.pushurl", "https://one.invalid/r.git"}, {"--add", "remote.origin.pushurl", "https://two.invalid/r.git"}},
			[]string{"https://one.invalid/r.git", "https://two.invalid/r.git"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			root := newRepo(t)
			gitOut(t, root, "remote", "add", "origin", "https://example.invalid/o/r.git")

			for _, kv := range tt.config {
				gitOut(t, root, append([]string{"config"}, kv...)...)
			}

			got, err := PushURLs(context.Background(), openRepo(t, root), "origin")
			if err != nil {
				t.Fatal(err)
			}

			if !slices.Equal(got, tt.want) {
				t.Errorf("push urls = %q, want %q", got, tt.want)
			}
		})
	}
}

func errOnly[T any](_ T, err error) error { return err }

// The guarded calls refuse an empty or option-like name before they start git.
func TestRemoteReadsRefuseOptionNames(t *testing.T) {
	t.Parallel()
	r := openRepo(t, newRepo(t))

	calls := []struct {
		name string
		call func(ctx context.Context, remote string) error
	}{
		{"RemoteURL", func(ctx context.Context, v string) error { return errOnly(RemoteURL(ctx, r, v)) }},
		{"PushURLs", func(ctx context.Context, v string) error { return errOnly(PushURLs(ctx, r, v)) }},
		{"RemoteHasBranch", func(ctx context.Context, v string) error { return errOnly(RemoteHasBranch(ctx, r, v, "feat")) }},
		{"RemoteBranches", func(ctx context.Context, v string) error { return errOnly(RemoteBranches(ctx, r, v)) }},
		{"Unpushed", func(ctx context.Context, v string) error { return errOnly(Unpushed(ctx, r, v)) }},
	}

	for _, c := range calls {
		for _, remote := range []string{"", "-x"} {
			t.Run(c.name+"/"+remote, func(t *testing.T) {
				t.Parallel()
				ctx, log := wrapGit(t, "")

				if err := c.call(ctx, remote); !errors.Is(err, ErrNoRemote) {
					t.Errorf("err = %v, want ErrNoRemote", err)
				}

				if ran, err := os.ReadFile(log); err == nil && len(ran) > 0 {
					t.Errorf("a refused remote ran git:\n%s", ran)
				}
			})
		}
	}

	// Positive control: a valid name does start git, so the checks above can see it.
	t.Run("a valid remote name runs git", func(t *testing.T) {
		t.Parallel()
		ctx, log := wrapGit(t, "")

		if _, err := PushURLs(ctx, r, "missing"); !errors.Is(err, ErrNoRemote) {
			t.Errorf("err = %v, want ErrNoRemote", err)
		}

		if ran, err := os.ReadFile(log); err != nil || len(ran) == 0 {
			t.Errorf("a valid remote name did not run git: %v", err)
		}
	})

	t.Run("PushURLs/unknown remote", func(t *testing.T) {
		t.Parallel()

		if _, err := PushURLs(context.Background(), r, "missing"); !errors.Is(err, ErrNoRemote) {
			t.Errorf("err = %v, want ErrNoRemote", err)
		}
	})
}

func TestRefusesBadBranchNames(t *testing.T) {
	t.Parallel()
	r := openRepo(t, newRepo(t))

	calls := []struct {
		name string
		call func(branch string) error
	}{
		{"Push", func(b string) error { return errOnly(Push(context.Background(), r, b)) }},
		{"PushRemote", func(b string) error { return errOnly(PushRemote(context.Background(), r, b)) }},
		{"HasUpstream", func(b string) error { return errOnly(HasUpstream(context.Background(), r, b)) }},
		{"RemoteHasBranch", func(b string) error { return errOnly(RemoteHasBranch(context.Background(), r, "origin", b)) }},
	}

	for _, c := range calls {
		for _, branch := range []string{"-x", "a..b"} {
			t.Run(c.name+"/"+branch, func(t *testing.T) {
				t.Parallel()

				if err := c.call(branch); !errors.Is(err, ErrBadBranch) {
					t.Errorf("err = %v, want ErrBadBranch", err)
				}
			})
		}
	}
}

func TestRemoteHasBranch(t *testing.T) {
	t.Parallel()
	dir, c, r := trackingFixture(t)
	gitOut(t, dir, "update-ref", "refs/remotes/origin/feat", c[2])

	tests := []struct {
		name   string
		remote string
		want   bool
	}{
		{"the tracking ref exists", "origin", true},
		{"another remote has no such ref", "second", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := RemoteHasBranch(context.Background(), r, tt.remote, "feat")
			if err != nil || got != tt.want {
				t.Fatalf("RemoteHasBranch = %v, %v; want %v", got, err, tt.want)
			}
		})
	}
}

func TestHasUpstream(t *testing.T) {
	t.Parallel()
	dir, _, r := trackingFixture(t)

	// The rows run in order on one repository.
	tests := []struct {
		name  string
		setup func()
		want  bool
	}{
		{name: "no upstream", setup: func() {}},
		{name: "upstream set", setup: func() { gitOut(t, dir, "config", "branch.feat.merge", "refs/heads/feat") }, want: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tt.setup()

			got, err := HasUpstream(context.Background(), r, "feat")
			if err != nil || got != tt.want {
				t.Fatalf("HasUpstream = %v, %v; want %v", got, err, tt.want)
			}
		})
	}
}

// Every git call a read makes carries GIT_OPTIONAL_LOCKS=0, and a resolving
// upstream needs one call per read (HasUpstream also checks the name).
func TestRemoteReadsTakeNoLocks(t *testing.T) {
	t.Parallel()
	dir, c, r := trackingFixture(t)
	trackUpstream(t, dir, c[2])

	ctx, log := wrapGit(t, `echo "locks=$GIT_OPTIONAL_LOCKS" >> "$LOG.locks"`)

	if _, err := PushURLs(ctx, r, "origin"); err != nil {
		t.Fatalf("PushURLs: %v", err)
	}

	if _, err := HasUpstream(ctx, r, "feat"); err != nil {
		t.Fatalf("HasUpstream: %v", err)
	}

	if _, ok, err := Ahead(ctx, r); err != nil || !ok {
		t.Fatalf("Ahead = ok %v, err %v", ok, err)
	}

	if _, err := Unpushed(ctx, r, "origin"); err != nil {
		t.Fatalf("Unpushed: %v", err)
	}

	calls := wrappedCalls(t, log)

	for _, want := range []string{
		"remote get-url --push --all origin",
		"check-ref-format --branch feat",
		"config --get branch.feat.merge",
		"rev-list --count @{upstream}..HEAD",
		"rev-list --count HEAD --not --remotes=origin",
	} {
		if !strings.Contains(calls, want) {
			t.Errorf("git calls lack %q:\n%s", want, calls)
		}
	}

	if n := strings.Count(calls, "\n"); n != 5 {
		t.Errorf("git ran %d times, want 5:\n%s", n, calls)
	}

	locks, err := os.ReadFile(log + ".locks")
	if err != nil {
		t.Fatal(err)
	}

	if got := strings.TrimSpace(string(locks)); got != strings.TrimSpace(strings.Repeat("locks=0\n", 5)) {
		t.Errorf("GIT_OPTIONAL_LOCKS per call = %q, want 0 for all five", got)
	}
}

func TestRemoteBranches(t *testing.T) {
	t.Parallel()
	dir, c, r := trackingFixture(t)

	for _, ref := range []string{"origin/feat", "origin/release/1.0", "origin/alpha", "second/other"} {
		gitOut(t, dir, "update-ref", "refs/remotes/"+ref, c[2])
	}
	// A symbolic HEAD under the remote is not a branch.
	gitOut(t, dir, "symbolic-ref", "refs/remotes/origin/HEAD", "refs/remotes/origin/feat")

	tests := []struct {
		name   string
		remote string
		want   []string
	}{
		{"nested names, sorted, HEAD left out", "origin", []string{"alpha", "feat", "release/1.0"}},
		{"the other remote", "second", []string{"other"}},
		{"a remote that is not configured", "nope", nil},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := RemoteBranches(context.Background(), r, tt.remote)
			if err != nil {
				t.Fatalf("RemoteBranches(%q) error = %v", tt.remote, err)
			}

			if !slices.Equal(got, tt.want) {
				t.Errorf("RemoteBranches(%q) = %q, want %q", tt.remote, got, tt.want)
			}
		})
	}
}
