package git

import (
	"context"
	"errors"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"
)

const (
	sessC = "cccccccc-dddd-4eee-8fff-000000000000"
	sessD = "dddddddd-eeee-4fff-8000-111111111111"
	sessE = "eeeeeeee-ffff-4000-8111-222222222222"
	day   = 24 * time.Hour
)

var sweepNow = time.Date(2026, 11, 2, 12, 0, 0, 0, time.UTC)

func ago(d time.Duration) int { return int(sweepNow.Add(-d).UnixMilli()) }

// writeTurnRefs creates every named ref at HEAD in one update-ref.
func writeTurnRefs(t *testing.T, dir string, refs ...string) {
	t.Helper()

	head := gitOut(t, dir, "rev-parse", "HEAD")

	var script strings.Builder
	for _, ref := range refs {
		script.WriteString("create " + ref + " " + head + "\n")
	}

	gitInput(t, dir, script.String(), "update-ref", "--stdin")
}

// prRefs points refs/aim/pr/<n>/head and base at HEAD.
func prRefs(t *testing.T, dir string, n int) {
	t.Helper()

	head := gitOut(t, dir, "rev-parse", "HEAD")
	for _, leaf := range []string{"head", "base"} {
		gitOut(t, dir, "update-ref", "refs/aim/pr/"+strconv.Itoa(n)+"/"+leaf, head)
	}
}

// names lists the refs under prefix, sorted.
func names(t *testing.T, dir, prefix string) []string {
	t.Helper()

	var out []string

	for l := range strings.SplitSeq(gitOut(t, dir, "for-each-ref", "--format=%(refname)", prefix), "\n") {
		if l = strings.TrimSpace(l); l != "" {
			out = append(out, l)
		}
	}

	slices.Sort(out)

	return out
}

func refNames(refs []Ref) []string {
	var out []string
	for _, r := range refs {
		out = append(out, r.Name)
	}

	slices.Sort(out)

	return out
}

func sameNames(t *testing.T, what string, got []Ref, want ...string) {
	t.Helper()
	slices.Sort(want)

	if g := refNames(got); !slices.Equal(g, want) {
		t.Errorf("%s = %q, want %q", what, g, want)
	}
}

func TestStaleReadsTheSessionsNewestTurnInTheRepository(t *testing.T) {
	t.Parallel()
	dir := seedRepo(t)
	wt := linkedWorktree(t, dir, "other")
	r := openRepo(t, dir)

	key, wtKey := turnKey(r.Root), turnKey(openRepo(t, wt).Root)
	writeTurnRefs(t, dir,
		turnRef(sessA, key, ago(20*day), false),
		turnRef(sessB, key, ago(20*day), false),
		turnRef(sessB, wtKey, ago(time.Hour), false),
		turnRef(sessC, key, ago(15*day), false),
		turnRef(sessD, wtKey, ago(14*day), false),
		turnRef(sessE, key, ago(-time.Hour), false),
	)
	legacyTurnRef(t, dir, r.Root, sessC, 3)
	prRefs(t, dir, 5)
	prRefs(t, dir, 8)

	l, err := ListAimRefs(context.Background(), r)
	if err != nil {
		t.Fatal(err)
	}

	got := l.Stale(sweepNow, func(s string) bool { return s == sessA }, func(n int) bool { return n == 5 })

	sameNames(t, "turns", got.Turns, turnRef(sessC, turnKey(r.Root), ago(15*day), false), "refs/aim/"+sessC+"/3")
	sameNames(t, "prs", got.PRs, "refs/aim/pr/8/base", "refs/aim/pr/8/head")

	for _, ref := range slices.Concat(got.Turns, got.PRs) {
		if want := gitOut(t, dir, "rev-parse", ref.Name); ref.OID != want {
			t.Errorf("%s OID = %s, want %s", ref.Name, ref.OID, want)
		}
	}
}

func TestDeleteRefsRefusesRefsOutsideAim(t *testing.T) {
	t.Parallel()
	dir := seedRepo(t)
	r := openRepo(t, dir)
	prRefs(t, dir, 8)

	head := gitOut(t, dir, "rev-parse", "HEAD")
	before := names(t, dir, "refs/")

	cases := []struct {
		name string
		refs []Ref
	}{
		{"outside", []Ref{{Name: "refs/heads/main", OID: head}, {Name: "refs/aim/pr/8/head", OID: head}}},
		{"no oid", []Ref{{Name: "refs/aim/pr/8/head"}}},
		{"newline in name", []Ref{
			{Name: "refs/aim/pr/8/head", OID: head},
			{Name: "refs/aim/x\ndelete refs/heads/main " + head, OID: head},
		}},
		{"nul in name", []Ref{{Name: "refs/aim/x\x00y", OID: head}}},
		{"whitespace in oid", []Ref{{Name: "refs/aim/pr/8/head", OID: head + "\ndelete refs/heads/main " + head}}},
	}

	for _, tc := range cases {
		n, err := DeleteRefs(context.Background(), r, tc.refs)
		if n != 0 || !errors.Is(err, ErrNotAimRef) {
			t.Errorf("%s: DeleteRefs = %d, %v; want 0, ErrNotAimRef", tc.name, n, err)
		}

		if got := names(t, dir, "refs/"); !slices.Equal(got, before) {
			t.Errorf("%s: refs = %q, want %q", tc.name, got, before)
		}
	}

	if n, err := DeleteRefs(context.Background(), r, nil); n != 0 || err != nil {
		t.Errorf("DeleteRefs(nil) = %d, %v", n, err)
	}
}

func TestSweepLeavesOtherRefs(t *testing.T) {
	t.Parallel()
	dir := seedRepo(t)
	r := openRepo(t, dir)
	head := gitOut(t, dir, "rev-parse", "HEAD")

	for _, ref := range []string{
		"refs/heads/side", "refs/tags/v1", "refs/keep/x", "refs/aim/other/x", "refs/aim/pr/7/other",
		"refs/aim/turns/" + sessD + "/zzzz/1/start",
	} {
		gitOut(t, dir, "update-ref", ref, head)
	}

	writeTurnRefs(t, dir,
		turnRef(sessB, turnKey(r.Root), ago(20*day), false),
		turnRef(sessC, turnKey(r.Root), ago(15*day), false),
	)
	prRefs(t, dir, 9)

	before := names(t, dir, "refs/")
	goneTurn := turnRef(sessC, turnKey(r.Root), ago(15*day), false)

	l, err := ListAimRefs(context.Background(), r)
	if err != nil {
		t.Fatal(err)
	}

	got := l.Stale(sweepNow, func(s string) bool { return s == sessB }, func(int) bool { return false })

	for _, half := range [][]Ref{got.Turns, got.PRs} {
		if _, err := DeleteRefs(context.Background(), r, half); err != nil {
			t.Fatal(err)
		}
	}

	var want []string

	for _, n := range before {
		if n != goneTurn && n != "refs/aim/pr/9/head" && n != "refs/aim/pr/9/base" {
			want = append(want, n)
		}
	}

	if got := names(t, dir, "refs/"); !slices.Equal(got, want) {
		t.Errorf("refs = %q, want %q", got, want)
	}
}

// bulkRefs creates n refs refs/aim/bulk/<i>/r, each in its own directory, at
// HEAD, and returns them in creation order.
func bulkRefs(t *testing.T, dir string, n int) []Ref {
	t.Helper()

	head := gitOut(t, dir, "rev-parse", "HEAD")
	refs := make([]Ref, 0, n)

	var script strings.Builder

	for i := range n {
		name := "refs/aim/bulk/" + strconv.Itoa(i) + "/r"
		refs = append(refs, Ref{Name: name, OID: head})
		script.WriteString("create " + name + " " + head + "\n")
	}

	gitInput(t, dir, script.String(), "update-ref", "--stdin")

	return refs
}

func TestDeleteRefsRunsOneUpdateRefPerChunk(t *testing.T) {
	t.Parallel()
	dir := seedRepo(t)
	r := openRepo(t, dir)
	refs := bulkRefs(t, dir, 7)
	ctx, logPath := wrapGit(t, "")

	n, err := deleteRefsIn(ctx, r, refs, 3)
	if err != nil || n != len(refs) {
		t.Fatalf("DeleteRefs = %d, %v; want %d", n, err, len(refs))
	}

	if got := strings.Count(wrappedCalls(t, logPath), "update-ref --stdin"); got != 3 {
		t.Errorf("update-ref calls = %d, want 3", got)
	}

	if left := names(t, dir, "refs/aim/"); len(left) != 0 {
		t.Errorf("refs left: %d", len(left))
	}
}

func TestDeleteRefsKeepsAParentDirectoryInOneChunk(t *testing.T) {
	t.Parallel()
	dir := seedRepo(t)
	r := openRepo(t, dir)
	head := gitOut(t, dir, "rev-parse", "HEAD")

	refs := bulkRefs(t, dir, 1)
	refs = append(refs, Ref{Name: "refs/aim/pr/8/base", OID: head}, Ref{Name: "refs/aim/pr/8/head", OID: head})
	gitOut(t, dir, "update-ref", "refs/aim/pr/8/base", head)
	gitOut(t, dir, "update-ref", "refs/aim/pr/8/head", head)

	writeFile(t, dir, "a.txt", "moved\n")
	commitAll(t, dir, "next")
	gitOut(t, dir, "update-ref", "refs/aim/pr/8/head", gitOut(t, dir, "rev-parse", "HEAD"))

	if _, err := deleteRefsIn(context.Background(), r, refs, 2); err == nil {
		t.Fatal("deleteRefsIn: err = nil, want the moved head to fail its chunk")
	}

	want := []string{"refs/aim/pr/8/base", "refs/aim/pr/8/head"}
	if got := names(t, dir, "refs/aim/pr/"); !slices.Equal(got, want) {
		t.Errorf("pr refs = %q, want both kept together", got)
	}
}

func TestDeleteRefsFailsOnlyTheChunkWithAMovedRef(t *testing.T) {
	t.Parallel()
	dir := seedRepo(t)
	r := openRepo(t, dir)
	refs := bulkRefs(t, dir, 9)

	writeFile(t, dir, "a.txt", "moved\n")
	commitAll(t, dir, "next")
	gitOut(t, dir, "update-ref", refs[4].Name, gitOut(t, dir, "rev-parse", "HEAD"))

	n, err := deleteRefsIn(context.Background(), r, refs, 3)
	if err == nil || n != 6 {
		t.Fatalf("deleteRefsIn = %d, %v; want 6 and an error", n, err)
	}

	var want []string
	for _, ref := range refs[3:6] {
		want = append(want, ref.Name)
	}

	slices.Sort(want)

	if got := names(t, dir, "refs/aim/"); !slices.Equal(got, want) {
		t.Errorf("refs left = %q, want %q", got, want)
	}
}

func TestDeleteChunkDefault(t *testing.T) {
	t.Parallel()

	if deleteChunk != 500 {
		t.Fatalf("deleteChunk = %d, want 500", deleteChunk)
	}
}
