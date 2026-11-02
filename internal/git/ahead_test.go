package git

import (
	"context"
	"testing"
)

// trackUpstream makes origin/feat the upstream of feat and puts its
// remote-tracking ref at tip.
func trackUpstream(t *testing.T, dir, tip string) {
	t.Helper()
	gitOut(t, dir, "config", "branch.feat.remote", "origin")
	gitOut(t, dir, "config", "branch.feat.merge", "refs/heads/feat")
	gitOut(t, dir, "update-ref", "refs/remotes/origin/feat", tip)
}

func TestAhead(t *testing.T) {
	t.Parallel()
	dir, c, r := trackingFixture(t)

	// The cases share one repository and run in order, each setting the refs it needs.
	tests := []struct {
		name   string
		setup  func()
		want   int
		wantOK bool
	}{
		{name: "no upstream", setup: func() {}},
		{name: "up to date", setup: func() { trackUpstream(t, dir, c[2]) }, wantOK: true},
		{
			name:   "two local commits",
			setup:  func() { gitOut(t, dir, "update-ref", "refs/remotes/origin/feat", c[0]) },
			want:   2,
			wantOK: true,
		},
		{name: "upstream ref gone", setup: func() { gitOut(t, dir, "update-ref", "-d", "refs/remotes/origin/feat") }},
		{
			name:   "upstream on a local branch",
			setup:  func() { gitOut(t, dir, "branch", "--set-upstream-to=main") },
			want:   2,
			wantOK: true,
		},
		{
			name: "behind only",
			setup: func() {
				gitOut(t, dir, "update-ref", "refs/heads/main", c[2])
				gitOut(t, dir, "update-ref", "refs/heads/feat", c[1])
			},
			wantOK: true,
		},
		{name: "detached", setup: func() { gitOut(t, dir, "update-ref", "--no-deref", "HEAD", c[1]) }},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tt.setup()

			n, ok, err := Ahead(context.Background(), r)
			if err != nil {
				t.Fatalf("Ahead: %v", err)
			}

			if n != tt.want || ok != tt.wantOK {
				t.Errorf("Ahead = (%d, %v), want (%d, %v)", n, ok, tt.want, tt.wantOK)
			}
		})
	}
}

func TestAheadReportsFailures(t *testing.T) {
	t.Parallel()
	dir, c, r := trackingFixture(t)
	trackUpstream(t, dir, c[2])

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	if n, ok, err := Ahead(ctx, r); err == nil {
		t.Errorf("cancelled context: Ahead = (%d, %v), want an error", n, ok)
	}

	// An upstream that resolves but a history that cannot be walked is an error, not "no upstream".
	writeFile(t, dir, ".git/refs/remotes/origin/feat", "1111111111111111111111111111111111111111\n")

	if n, ok, err := Ahead(context.Background(), r); err == nil {
		t.Errorf("upstream ref to a missing object: Ahead = (%d, %v), want an error", n, ok)
	}
}

func TestUnpushed(t *testing.T) {
	t.Parallel()
	dir, c, r := trackingFixture(t)

	// The cases share one repository and run in order; HEAD is the third commit.
	tests := []struct {
		name  string
		setup func()
		want  int
	}{
		{name: "origin has HEAD", setup: func() { gitOut(t, dir, "update-ref", "refs/remotes/origin/feat", c[2]) }},
		{
			name:  "origin one commit behind",
			setup: func() { gitOut(t, dir, "update-ref", "refs/remotes/origin/feat", c[1]) },
			want:  1,
		},
		{
			name:  "no tracking ref counts every commit",
			setup: func() { gitOut(t, dir, "update-ref", "-d", "refs/remotes/origin/feat") },
			want:  3,
		},
		{
			name:  "another remote does not count",
			setup: func() { gitOut(t, dir, "update-ref", "refs/remotes/backup/feat", c[2]) },
			want:  3,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tt.setup()

			got, err := Unpushed(context.Background(), r, "origin")
			if err != nil {
				t.Fatalf("Unpushed: %v", err)
			}

			if got != tt.want {
				t.Errorf("Unpushed = %d, want %d", got, tt.want)
			}
		})
	}
}
