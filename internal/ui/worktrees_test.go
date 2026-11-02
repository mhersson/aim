package ui

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/mhersson/aim/internal/core"
	"github.com/mhersson/aim/internal/git"
)

// wtFix is repository A with linked worktrees feat-a and feat-b and a worktree
// whose directory is gone (prunable), and another repository B.
type wtFix struct {
	fake                            *fakeGit
	git                             Git // what the list asks: the fake, or a wrapper that names a worktree through a symlink
	parent                          string
	a, b, featA, featB              string
	rootA, rootB, rootFeatA, rootFB string
	repoFeatA, repoFeatB            *fakeRepo
}

func newWtFix(t *testing.T) *wtFix {
	t.Helper()

	parent, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}

	fk := newFakeGit(t)
	f := &wtFix{fake: fk, git: fk, parent: parent}

	repo := func(name string) *fakeRepo {
		rp := fk.repoAt(t, filepath.Join(parent, name))
		write(t, filepath.Join(rp.Root(), "a.txt"), "one\n")
		rp.commit("init")

		return rp
	}

	a, b := repo("A"), repo("B")
	f.a, f.b = a.Root(), b.Root()
	f.repoFeatA = a.worktree(t, filepath.Join(f.a, ".worktrees", "feat-a"), "feat-a")
	f.repoFeatB = a.worktree(t, filepath.Join(f.a, ".worktrees", "feat-b"), "feat-b")
	f.featA, f.featB = f.repoFeatA.Root(), f.repoFeatB.Root()

	gone := a.worktree(t, filepath.Join(f.a, ".worktrees", "gone"), "gone")
	if err := os.RemoveAll(gone.Root()); err != nil {
		t.Fatal(err)
	}

	gone.prunable("gitdir file points to non-existent location")

	f.rootA, f.rootB, f.rootFeatA, f.rootFB = f.a, f.b, f.featA, f.featB

	return f
}

// edits are feat-a (the second edit) and then B (the third).
func (f *wtFix) edits() []core.Edit {
	return []core.Edit{
		{Root: f.rootFeatA, Seq: 2, At: t0.Add(-4 * time.Minute)},
		{Root: f.rootB, Seq: 3, At: t0.Add(-8 * time.Second)},
	}
}

func (f *wtFix) input() wtInput {
	return wtInput{own: f.a, followed: f.rootB, agent: "A", edits: f.edits()}
}

// describeRows is the list as text, one entry per row.
func describeRows(rows []wtRow) []string {
	out := make([]string, 0, len(rows))

	for _, r := range rows {
		switch r.kind {
		case wtFollow:
			out = append(out, "follow")
		case wtHead:
			out = append(out, "head "+r.text)
		case wtGap:
			out = append(out, "gap")
		case wtPlace:
			s := "place " + r.repo + " · " + r.branch
			if r.latest {
				s += " latest"
			}

			if r.here {
				s += " here"
			}

			out = append(out, s)
		}
	}

	return out
}

func TestWorktreeListRows(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name  string
		setup func(t *testing.T, f *wtFix, in *wtInput)
		want  []string
		now   string
	}{
		{"edits in two repositories", func(t *testing.T, f *wtFix, in *wtInput) {}, []string{
			"follow", "head Touched by A",
			"place B · main latest", "place A · feat-a", "place A · main here",
			"gap", "head Other worktrees of A", "place A · feat-b",
		}, "B · main"},
		{"symlinked worktree path", func(t *testing.T, f *wtFix, in *wtInput) {
			linkWorktree(t, f)
			in.edits = append(in.edits, core.Edit{Root: f.rootFB, Seq: 1, At: t0.Add(-time.Hour)})
		}, []string{
			"follow", "head Touched by A",
			"place B · main latest", "place A · feat-a", "place A · feat-b", "place A · main here",
		}, "B · main"},
		{"symlinked worktree path that was not edited", func(t *testing.T, f *wtFix, in *wtInput) {
			linkWorktree(t, f)
		}, []string{
			"follow", "head Touched by A",
			"place B · main latest", "place A · feat-a", "place A · main here",
			"gap", "head Other worktrees of A", "place A · feat-b",
		}, "B · main"},
		{"own directory through a symlink", func(t *testing.T, f *wtFix, in *wtInput) {
			link := filepath.Join(f.parent, "Alink")
			if err := os.Symlink(f.a, link); err != nil {
				t.Fatal(err)
			}

			in.own = link
		}, []string{
			"follow", "head Touched by A",
			"place B · main latest", "place A · feat-a", "place A · main here",
			"gap", "head Other worktrees of A", "place A · feat-b",
		}, "B · main"},
		{"an edited worktree is gone", func(t *testing.T, f *wtFix, in *wtInput) {
			in.edits = append(in.edits, core.Edit{Root: filepath.Join(f.parent, "removed"), Seq: 1})
		}, []string{
			"follow", "head Touched by A",
			"place B · main latest", "place A · feat-a", "place A · main here",
			"gap", "head Other worktrees of A", "place A · feat-b",
		}, "B · main"},
		{"the agent works in a worktree", func(t *testing.T, f *wtFix, in *wtInput) {
			in.own, in.followed, in.edits = f.featA, f.featA, nil
		}, []string{
			"follow", "head Touched by A",
			"place A · feat-a here",
			"gap", "head Other worktrees of A", "place A · main", "place A · feat-b",
		}, "A · feat-a"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			f := newWtFix(t)
			in := f.input()
			tt.setup(t, f, &in)

			rows, now, err := listPlaces(context.Background(), f.git, in)
			if err != nil {
				t.Fatalf("listPlaces() error = %v", err)
			}

			if got := describeRows(rows); !slices.Equal(got, tt.want) {
				t.Errorf("rows =\n%s\nwant\n%s", strings.Join(got, "\n"), strings.Join(tt.want, "\n"))
			}

			if now != tt.now {
				t.Errorf("now = %q, want %q", now, tt.now)
			}

			seen := map[string]bool{}

			for _, r := range rows {
				if r.kind != wtPlace {
					continue
				}

				if seen[resolved(r.root)] {
					t.Errorf("root %q is listed twice", r.root)
				}

				seen[resolved(r.root)] = true

				if r.root != resolved(r.root) {
					t.Errorf("root %q is not the resolved path, which git reports for the worktree", r.root)
				}
			}
		})
	}
}

// namedByLink answers as a git that names a worktree by a path through a
// symlink, as one does that does not resolve the path of a new worktree.
type namedByLink struct {
	*fakeGit
	root, as string
}

func (g namedByLink) ListWorktrees(ctx context.Context, r git.Repo) ([]git.Worktree, error) {
	wts, err := g.fakeGit.ListWorktrees(ctx, r)
	for i := range wts {
		if wts[i].Path == g.root {
			wts[i].Path = g.as
		}
	}

	return wts, err
}

// linkWorktree makes git name feat-b by a path through a symlink.
func linkWorktree(t *testing.T, f *wtFix) {
	t.Helper()

	link := filepath.Join(f.parent, "link")
	if err := os.Symlink(filepath.Dir(f.featB), link); err != nil {
		t.Fatal(err)
	}

	f.git = namedByLink{fakeGit: f.fake, root: f.featB, as: filepath.Join(link, "feat-b")}

	wts, err := f.git.ListWorktrees(context.Background(), git.Repo{Root: f.a})
	if err != nil {
		t.Fatal(err)
	}

	if !slices.ContainsFunc(wts, func(w git.Worktree) bool { return w.Path == filepath.Join(link, "feat-b") }) {
		t.Fatalf("git lists %+v, want feat-b by its symlinked path", wts)
	}
}

func TestWorktreeListRowFields(t *testing.T) {
	t.Parallel()
	f := newWtFix(t)

	rows, _, err := listPlaces(context.Background(), f.git, f.input())
	if err != nil {
		t.Fatal(err)
	}

	byBranch := map[string]wtRow{}

	for _, r := range rows {
		if r.kind == wtPlace {
			byBranch[r.repo+"/"+r.branch] = r
		}
	}

	for _, tt := range []struct {
		key  string
		root string
		at   time.Time
	}{
		{"B/main", f.rootB, t0.Add(-8 * time.Second)},
		{"A/feat-a", f.rootFeatA, t0.Add(-4 * time.Minute)},
		{"A/main", f.rootA, time.Time{}},
		{"A/feat-b", f.rootFB, time.Time{}},
	} {
		if r := byBranch[tt.key]; r.root != tt.root || !r.at.Equal(tt.at) {
			t.Errorf("%s = root %q at %v, want %q at %v", tt.key, r.root, r.at, tt.root, tt.at)
		}
	}
}

func TestWorktreeListPinnedElsewhere(t *testing.T) {
	t.Parallel()
	f := newWtFix(t)
	in := wtInput{own: f.a, followed: f.a, pin: f.rootB, agent: "A"}

	rows, now, err := listPlaces(context.Background(), f.git, in)
	if err != nil {
		t.Fatal(err)
	}

	want := []string{
		"follow", "head Touched by A",
		"place A · main here", "place B · main",
		"gap", "head Other worktrees of A", "place A · feat-a", "place A · feat-b",
	}
	if got := describeRows(rows); !slices.Equal(got, want) {
		t.Errorf("rows =\n%s\nwant\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}

	if now != "A · main" {
		t.Errorf("now = %q, want the own directory's place", now)
	}
}

func TestWorktreeListOwnDirOutsideRepo(t *testing.T) {
	t.Parallel()
	f := newWtFix(t)

	outside, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}

	in := wtInput{own: outside, followed: f.rootB, agent: "x", edits: []core.Edit{{Root: f.rootB, Seq: 1}}}

	rows, _, err := listPlaces(context.Background(), f.git, in)
	if err != nil {
		t.Fatal(err)
	}

	want := []string{"follow", "head Touched by x", "place B · main latest"}
	if got := describeRows(rows); !slices.Equal(got, want) {
		t.Errorf("rows =\n%s\nwant\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
}

func TestWorktreeListStopsWithItsContext(t *testing.T) {
	t.Parallel()
	f := newWtFix(t)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	if _, _, err := listPlaces(ctx, f.git, f.input()); err == nil {
		t.Error("listPlaces() under a cancelled context returned no error")
	}
}

func TestDetachedBranchesShowTheCommit(t *testing.T) {
	t.Parallel()
	f := newWtFix(t)
	f.repoFeatA.detach()
	f.repoFeatB.detach()

	rows, _, err := listPlaces(context.Background(), f.git, wtInput{own: f.a, followed: f.a, agent: "A", edits: []core.Edit{{Root: f.rootFeatA, Seq: 1}}})
	if err != nil {
		t.Fatal(err)
	}

	head, err := f.fake.Head(context.Background(), f.repoFeatA.Repo())
	if err != nil {
		t.Fatal(err)
	}

	short := head.OID[:7]
	want := []string{
		"follow", "head Touched by A",
		"place A · " + short + " latest", "place A · main here",
		"gap", "head Other worktrees of A", "place A · " + short,
	}

	if got := describeRows(rows); !slices.Equal(got, want) {
		t.Errorf("rows =\n%s\nwant\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
}

// ---- the list in the drawer ----

// openList presses w in the drawer and waits for the list.
func (r *followRig) openList(t *testing.T) *wtList {
	t.Helper()

	r.keys(keyRune('w'))
	r.until(t, "the worktree list", func() bool { return r.drawer.list != nil })

	return r.drawer.list
}

// rowOf is the index of the place row for root.
func (l *wtList) rowOf(root string) int {
	return slices.IndexFunc(l.rows, func(r wtRow) bool { return r.kind == wtPlace && r.root == root })
}

// toRoot moves the selection with j until it is on root's row.
func (r *followRig) toRoot(t *testing.T, root string) {
	t.Helper()

	for range 20 {
		if r.drawer.list.selectedRoot() == root {
			return
		}

		r.keys(keyRune('j'))
	}

	t.Fatalf("the selection never reached %q", root)
}

// followC leaves the agent's edits in C, so its place is the latest.
func (r *followRig) followC(t *testing.T) {
	t.Helper()

	r.edit(filepath.Join(r.dirC, "c.txt"))
	r.until(t, "the followed place to be C", func() bool { return r.pane().FollowDir() == r.rootC })
}

func (r *followRig) listShape() []string {
	return describeRows(r.drawer.list.rows)
}

func TestWorktreeListSelectsShown(t *testing.T) {
	t.Parallel()
	t.Run("following", func(t *testing.T) {
		r := newFollowRig(t, "")
		r.open(t, r.id)
		r.followC(t)

		if l := r.openList(t); l.sel != 0 || l.selectedRoot() != "" {
			t.Errorf("selected row %d (%q), want the Follow row", l.sel, l.selectedRoot())
		}
	})

	t.Run("pinned", func(t *testing.T) {
		r := newFollowRig(t, "")
		r.open(t, r.id)
		r.followC(t)
		r.pinTo(t, r.rootB)
		r.until(t, "the drawer to show b", r.showing(r.rootB))

		if l := r.openList(t); l.selectedRoot() != r.rootB {
			t.Errorf("selected row %d (%q), want b's row", l.sel, l.selectedRoot())
		}
	})
}

// The pin may name its root through a symlink; the list still opens on that
// worktree's row.
func TestWorktreeListSelectsPinnedThroughSymlink(t *testing.T) {
	t.Parallel()
	r := newFollowRig(t, "")
	r.open(t, r.id)

	link := filepath.Join(t.TempDir(), "b-link")
	if err := os.Symlink(r.dirB, link); err != nil {
		t.Fatal(err)
	}

	r.pinTo(t, link)
	r.idle(t)

	if l := r.openList(t); l.selectedRoot() != r.rootB {
		t.Errorf("selected row %d (%q), want b's row %q", l.sel, l.selectedRoot(), r.rootB)
	}
}

// A pinned worktree that is gone has no row, so the list opens on Follow, which
// does not read "on" while the pin holds.
func TestWorktreeListPinnedGone(t *testing.T) {
	t.Parallel()
	r := newFollowRig(t, "")
	r.open(t, r.id)
	r.pinTo(t, r.rootB)
	r.until(t, "the drawer to show b", r.showing(r.rootB))

	if err := r.fake.RemoveWorktree(context.Background(), r.repoA.Repo(), r.dirB); err != nil {
		t.Fatal(err)
	}

	l := r.openList(t)

	if l.sel != 0 || l.rowOf(r.rootB) >= 0 {
		t.Errorf("selected row %d, b has row %d, want Follow selected and no row for the removed worktree", l.sel, l.rowOf(r.rootB))
	}

	follow := lineHolding(plainLines(listLines(t, r.App)), "Follow the agent")
	if follow < 0 {
		t.Fatal("the list lacks its Follow row")
	}

	if row := plainLines(listLines(t, r.App))[follow]; strings.Contains(row, "Follow the agent  on") {
		t.Errorf("the Follow row reads %q while the drawer is pinned", row)
	}
}

func TestWorktreeListPick(t *testing.T) {
	t.Parallel()
	r := newFollowRig(t, "")
	r.open(t, r.id)
	r.followC(t)

	nameA := filepath.Base(r.dirA)
	nameC := filepath.Base(r.dirC)

	want := []string{
		"follow", "head Touched by " + nameA,
		"place " + nameC + " · other latest", "place " + nameA + " · main here",
		"gap", "head Other worktrees of " + nameA, "place " + nameA + " · b",
	}

	r.openList(t)

	if got := r.listShape(); !slices.Equal(got, want) {
		t.Fatalf("rows =\n%s\nwant\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}

	r.toRoot(t, r.rootB)
	r.keys(keyRune('j'))
	r.keys(tea.KeyPressMsg{Code: tea.KeyEnter})

	if r.drawer.list != nil {
		t.Error("the list stayed open after enter")
	}

	if pin := r.store.State().Tab().Pin; pin == nil || pin.Root != r.rootB {
		t.Fatalf("Pin = %+v, want b's root %q", pin, r.rootB)
	}

	r.until(t, "the drawer to load b", r.showing(r.rootB))

	if !r.drawerHasKeys() {
		t.Error("the drawer lost the keyboard on a pick")
	}

	// Follow turns the pin off, and the drawer goes to the followed place.
	r.openList(t)
	r.keys(keyRune('k'), keyRune('k'), keyRune('k'))
	r.keys(tea.KeyPressMsg{Code: tea.KeyEnter})

	if pin := r.store.State().Tab().Pin; pin != nil {
		t.Fatalf("Pin = %+v after Follow, want none", pin)
	}

	r.until(t, "the drawer to follow C", r.showing(r.rootC))
}

func TestWorktreeListKeys(t *testing.T) {
	t.Parallel()
	r := newFollowRig(t, "")
	r.open(t, r.id)
	r.followC(t)
	r.openList(t)

	// Rows: follow, head, C, A, gap, head, b.
	tests := []struct {
		key  tea.KeyPressMsg
		want int
	}{
		{keyRune('j'), 2},
		{keyRune('j'), 3},
		{keyRune('j'), 6},
		{keyRune('j'), 6},
		{keyRune('k'), 3},
		{keyRune('k'), 2},
		{keyRune('k'), 0},
		{keyRune('k'), 0},
		{tea.KeyPressMsg{Code: tea.KeyDown}, 2},
		{tea.KeyPressMsg{Code: tea.KeyDown}, 3},
		{tea.KeyPressMsg{Code: tea.KeyUp}, 2},
		{tea.KeyPressMsg{Code: tea.KeyUp}, 0},
	}

	for _, tt := range tests {
		r.keys(tt.key)

		if got := r.drawer.list.sel; got != tt.want {
			t.Fatalf("after %q the selection is row %d, want %d", tt.key.String(), got, tt.want)
		}
	}
}

func TestWorktreeListEscChangesNothing(t *testing.T) {
	t.Parallel()
	r := newFollowRig(t, "")
	r.open(t, r.id)
	r.followC(t)
	r.pinTo(t, r.rootB)
	r.until(t, "the drawer to show b", r.showing(r.rootB))
	r.openList(t)
	r.keys(keyRune('k'))

	r.keys(keyEsc)

	if r.drawer.list != nil {
		t.Fatal("esc left the list open")
	}

	if pin := r.store.State().Tab().Pin; pin == nil || pin.Root != r.rootB {
		t.Errorf("Pin = %+v after esc, want b's root", pin)
	}

	if !r.drawerHasKeys() || r.store.State().Tab().Review == nil {
		t.Error("esc closed the list and took the keyboard from the drawer or closed it")
	}

	if v := r.view(); v.root != r.rootB {
		t.Errorf("the drawer shows %q, want %q", v.root, r.rootB)
	}
}

func TestWorktreeListRefreshKeepsSelection(t *testing.T) {
	t.Parallel()
	r := newFollowRig(t, "")
	r.open(t, r.id)
	r.followC(t)

	first := r.openList(t)
	r.toRoot(t, r.rootB)

	sel := first.sel

	r.keys(keyRune('w'))
	r.until(t, "the list to be rebuilt", func() bool { return r.drawer.list != first })

	l := r.drawer.list
	if l.selectedRoot() != r.rootB || l.sel != sel {
		t.Errorf("selected row %d (%q) after the refresh, want row %d on b", l.sel, l.selectedRoot(), sel)
	}
}

func TestWorktreeListDropsOtherKeys(t *testing.T) {
	t.Parallel()
	r := newFollowRig(t, "")
	r.open(t, r.id)
	r.followC(t)
	l := r.openList(t)

	v := r.view()
	tree := treeIsOpen(r.App)
	root := v.root

	for _, k := range []tea.KeyPressMsg{
		keyRune('x'), keyRune('g'), keyRune('G'), keyRune('f'), keyRune('/'), keyRune('t'), keyRune('b'), keyRune('e'),
		{Code: tea.KeyTab},
		{Code: tea.KeyLeft},
		{Code: tea.KeyRight},
		{Code: 'd', Mod: tea.ModCtrl},
	} {
		r.keys(k)
	}

	r.step(tea.PasteMsg{Content: "pasted"})

	if r.drawer.list != l || l.sel != 0 {
		t.Errorf("the list changed: open = %v, sel = %d", r.drawer.list == l, l.sel)
	}

	if v.search != nil || treeIsOpen(r.App) != tree || v.root != root || r.store.State().Tab().Review == nil || !r.drawerHasKeys() {
		t.Error("a key the list does not know reached the drawer")
	}
}

func TestWorktreeListLeaderStillWorks(t *testing.T) {
	t.Parallel()
	r := newFollowRig(t, "")
	r.open(t, r.id)
	r.followC(t)
	l := r.openList(t)

	r.keys(keyLeader, keyRune(':'))

	if r.overlay == nil {
		t.Fatal("leader : did not open the palette")
	}

	r.keys(keyEsc)
	r.until(t, "the palette to close", func() bool { return r.overlay == nil })

	if r.drawer.list != l {
		t.Error("the palette closed the list")
	}

	r.keys(keyRune('j'))

	if l.sel != 2 {
		t.Errorf("the selection is row %d, want 2: the list lost the keyboard", l.sel)
	}
}

func TestWorktreeListFromThePalette(t *testing.T) {
	t.Parallel()
	r := newFollowRig(t, "")
	r.open(t, r.id)

	// The drawer's keyboard is somewhere else.
	r.drawerRelease()

	c, ok := r.reg.get("review.worktrees")
	if !ok {
		t.Fatal("no command review.worktrees")
	}

	r.run(c.run(r.App))
	r.until(t, "the worktree list", func() bool { return r.drawer.list != nil })

	if !r.drawerHasKeys() {
		t.Error("the command did not give the drawer the keyboard")
	}
}

func TestWorktreeListNeedsADrawer(t *testing.T) {
	t.Parallel()
	r := newFollowRig(t, "")

	r.keys(keyLeader, keyRune(':'))
	r.typed("Review: worktrees")
	r.keys(tea.KeyPressMsg{Code: tea.KeyEnter})
	r.until(t, "the notice", func() bool { return r.notice == noticeNoDrawer })

	if r.drawer.list != nil {
		t.Error("a list opened with no drawer")
	}
}

func TestWorktreeListCloses(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name  string
		close func(t *testing.T, r *followRig)
	}{
		{"leader h", func(t *testing.T, r *followRig) { r.leader('h') }},
		{"leader d", func(t *testing.T, r *followRig) { r.leader('d') }},
		{"a tab switch", func(t *testing.T, r *followRig) {
			openIn(t, r.App, core.NewTab{Kind: core.KindShell, Cwd: r.dirA})
			r.settle()
		}},
		{"a drawer close", func(t *testing.T, r *followRig) {
			openIn(t, r.App, core.CloseReview{})
			r.settle()
		}},
		{"a retarget", func(t *testing.T, r *followRig) {
			other := r.shell(t, r.dirC)
			openIn(t, r.App, core.OpenReview{Pane: other, Mode: core.ReviewBranch})
			r.settle()
		}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			r := newFollowRig(t, "")
			r.open(t, r.id)
			r.followC(t)
			l := r.openList(t)
			r.until(t, "the row counts", func() bool { return l.rows[l.rowOf(r.rootC)].statsKnown })

			gen, root := l.gen, r.rootC

			tt.close(t, r)

			if r.drawer.list != nil {
				t.Fatalf("the list is still open after %s", tt.name)
			}

			// What was in flight for the closed list is dropped.
			r.step(wtOpenMsg{tab: l.tab, pane: l.pane, gen: gen, rows: l.rows, now: l.now})
			r.step(wtStatsMsg{gen: gen, root: root})

			if r.drawer.list != nil {
				t.Errorf("an answer for a closed list opened it again")
			}
		})
	}
}

// fillProbes takes n probe slots, so no more than the rest can start, and
// returns the function that frees them.
func (r *followRig) fillProbes(n int) (free func()) {
	for range n {
		r.probeSem <- struct{}{}
	}

	var once sync.Once

	return func() {
		once.Do(func() {
			for range n {
				<-r.probeSem
			}
		})
	}
}

// statsUntil feeds the queued messages to Update until done holds for the stats
// answers among them, and returns those answers.
func (r *followRig) statsUntil(t *testing.T, what string, done func([]wtStatsMsg) bool) []wtStatsMsg {
	t.Helper()

	var out []wtStatsMsg

	deadline := time.Now().Add(r.wait)

	for !done(out) {
		select {
		case m := <-r.pm.msgs:
			if s, ok := m.(wtStatsMsg); ok {
				out = append(out, s)
			}

			r.run(r.step(m))
		case <-time.After(10 * time.Millisecond):
			r.settle()
		}

		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
	}

	return out
}

// Each place row's count holds one probe slot while it runs, and a row that finds
// every slot taken waits, so the rows never run more at once than the slots free.
func TestWorktreeListStatsHoldProbeSlots(t *testing.T) {
	t.Parallel()
	r := newFollowRig(t, "")
	r.open(t, r.id)
	r.followC(t)
	r.until(t, "the followed place to settle", r.landed)

	// Two of the four slots are taken, so two of the three rows can run.
	free := r.fillProbes(railProbeMax - 2)
	defer free()

	release := r.fake.hold("Stats")
	defer release()

	l := r.openList(t)

	eventually(t, "two row counts running", func() bool { return r.fake.inflight("Stats") == 2 })

	if got := len(r.probeSem); got != railProbeMax {
		t.Errorf("held slots = %d while two rows run, want all %d taken", got, railProbeMax)
	}

	if got := r.fake.count("Stats"); got != 2 {
		t.Errorf("%d rows were counted while the slots were taken, want only the 2 that held one", got)
	}

	for _, row := range l.rows {
		if row.statsKnown {
			t.Fatalf("row %+v has counts while its count is held", row)
		}
	}

	release()
	r.until(t, "every place to be counted", func() bool {
		return !slices.ContainsFunc(l.rows, func(row wtRow) bool { return row.kind == wtPlace && !row.statsKnown })
	})

	if got := r.fake.peak("Stats"); got != 2 {
		t.Errorf("peak concurrent counts = %d, want 2: the free slots", got)
	}

	if got := r.fake.count("Stats"); got != 3 {
		t.Errorf("%d rows were counted, want all 3: the third once a slot was free", got)
	}
}

func TestWorktreeListCloseCancelsProbes(t *testing.T) {
	t.Parallel()
	r := newFollowRig(t, "")
	r.open(t, r.id)
	r.followC(t)
	r.until(t, "the followed place to settle", r.landed)

	free := r.fillProbes(railProbeMax)
	defer free()

	r.openList(t)
	r.keys(keyEsc)

	// The probes waiting for a slot give up when the list closes, though no slot is free.
	got := map[string]bool{}

	r.statsUntil(t, "the three row probes to end", func(answers []wtStatsMsg) bool {
		for _, s := range answers {
			if s.err != nil {
				got[s.root] = true
			}
		}

		return len(got) == 3
	})

	if n := r.fake.count("Stats"); n != 0 {
		t.Errorf("%d rows were counted though no probe held a slot", n)
	}
}

func TestWorktreeListRebuildInFlight(t *testing.T) {
	t.Parallel()
	r := newFollowRig(t, "")
	r.open(t, r.id)
	r.followC(t)
	r.until(t, "the followed place to settle", r.landed)

	free := r.fillProbes(railProbeMax)
	defer free()

	first := r.openList(t)

	// Rebuild while the first list's row probes wait for a slot.
	r.keys(keyRune('w'))
	r.until(t, "the rebuilt list", func() bool { return r.drawer.list != first })

	second := r.drawer.list
	if second.gen <= first.gen {
		t.Fatalf("the rebuilt list has generation %d, want one past %d", second.gen, first.gen)
	}

	// An answer of the first list, late, is dropped.
	row := second.rowOf(r.rootC)
	r.step(wtStatsMsg{gen: first.gen, root: r.rootC, stats: git.Stats{Files: 99, Added: 99}})
	r.step(wtOpenMsg{tab: first.tab, pane: first.pane, gen: first.gen, rows: first.rows})

	if r.drawer.list != second || second.rows[row].statsKnown {
		t.Fatalf("an answer for the first list reached the rebuilt one: same list = %v, row = %+v", r.drawer.list == second, second.rows[row])
	}

	free()

	r.until(t, "the rebuilt list to be counted", func() bool {
		return !slices.ContainsFunc(second.rows, func(row wtRow) bool { return row.kind == wtPlace && !row.statsKnown })
	})

	if got := second.rows[row].stats.Files; got == 99 {
		t.Error("the rebuilt list shows the stale counts")
	}
}

func TestWorktreeListLastOpenWins(t *testing.T) {
	t.Parallel()
	r := newFollowRig(t, "")
	r.open(t, r.id)
	r.followC(t)

	// Two presses before either answer: only the second answer opens the list.
	cmd1 := r.step(keyRune('w'))
	cmd2 := r.step(keyRune('w'))
	msgs1, msgs2 := flatten(cmd1), flatten(cmd2)

	for _, m := range msgs1 {
		r.step(m)
	}

	if r.drawer.list != nil {
		t.Fatal("the first press's answer opened the list after a second press")
	}

	for _, m := range msgs2 {
		r.step(m)
	}

	if r.drawer.list == nil {
		t.Fatal("the second press's answer did not open the list")
	}
}

func TestReviewHintsWorktree(t *testing.T) {
	t.Parallel()

	s := reviewScene{theme: "catppuccin-mocha", icons: false, w: 240, h: 52, res: turn4Result(), turn: 4, turns: 4, focused: true}
	got := ansi.Strip(s.build(t).View().Content)

	const want = "tab tree   w worktree   f full"
	if !strings.Contains(got, want) {
		t.Errorf("the diff hints lack %q:\n%s", want, got)
	}
}

func TestWorktreeKeyIsNotATreeKey(t *testing.T) {
	t.Parallel()

	if treeCommand("review.worktrees") {
		t.Error("review.worktrees works while the tree has the keyboard")
	}

	i := slices.IndexFunc(reviewKeys, func(k reviewKey) bool { return k.key == "w" })
	if i < 0 || reviewKeys[i].command != "review.worktrees" {
		t.Errorf("w is bound to %+v, want review.worktrees", reviewKeys[i])
	}
}

// ---- drawing ----

// wtDemoRows are rows of a list in a drawer scene: a place in another
// repository (the latest), the agent's own place, one with a clean tree, one
// whose counts have not arrived and one more of the agent's repository.
func wtDemoRows() []wtRow {
	return []wtRow{
		{kind: wtFollow},
		{kind: wtHead, text: "Touched by multiplex"},
		{
			kind: wtPlace, root: "/w/other", repo: "other", branch: "main", latest: true, at: t0.Add(-8 * time.Second),
			stats: git.Stats{Files: 3, Added: 41, Removed: 7}, statsKnown: true,
		},
		{
			kind: wtPlace, root: "/w/p1/.worktrees/feat-a", repo: "multiplex", branch: "feat/follow-edits", at: t0.Add(-4 * time.Minute),
			stats: git.Stats{Files: 1, Added: 12}, statsKnown: true,
		},
		{kind: wtPlace, root: "/w/p1", repo: "multiplex", branch: "main", here: true, stats: git.Stats{}, statsKnown: true},
		{kind: wtGap},
		{kind: wtHead, text: "Other worktrees of multiplex"},
		{kind: wtPlace, root: "/w/p1/.worktrees/feat-b", repo: "multiplex", branch: "feat/pr-status"},
	}
}

// showList opens a list of rows in the scene's drawer.
func showList(a *App, rows []wtRow, now string, sel int) *wtList {
	l := &wtList{
		tab: a.store.State().Tab().ID, pane: "p1", gen: 1, sel: sel, rows: rows, now: now,
		cancel: func() {},
	}
	a.drawer.list, a.drawer.listGen = l, 1

	return l
}

// listLines are the drawer's lines, with styling.
func listLines(t *testing.T, a *App) []string {
	t.Helper()

	return a.drawerLines(a.store.State(), drawerRectOf(t, a, 0))
}

func plainLines(lines []string) []string {
	out := make([]string, len(lines))
	for i, l := range lines {
		out[i] = ansi.Strip(l)
	}

	return out
}

// lineWith is the index of the first line holding sub, or -1.
func lineHolding(lines []string, sub string) int {
	return slices.IndexFunc(lines, func(l string) bool { return strings.Contains(l, sub) })
}

func TestWorktreeListCountsArrive(t *testing.T) {
	t.Parallel()

	s := reviewScene{theme: "catppuccin-mocha", icons: true, w: 200, h: 52, res: turn4Result(), turn: 4, turns: 4, focused: true}
	a := s.build(t)

	rows := wtDemoRows()
	for i := range rows {
		rows[i].stats, rows[i].statsKnown = git.Stats{}, false
	}

	l := showList(a, rows, "other · main", 0)

	before := plainLines(listLines(t, a))
	if i := lineHolding(before, "other · main  latest"); i < 0 || strings.Contains(before[i], "+") || strings.Contains(before[i], "clean") {
		t.Fatalf("a row shows counts before they arrived: %q", before[i])
	}

	a.Update(wtStatsMsg{gen: l.gen, root: "/w/other", stats: git.Stats{Files: 3, Added: 41, Removed: 7}})
	a.Update(wtStatsMsg{gen: l.gen, root: "/w/p1", stats: git.Stats{}})
	a.Update(wtStatsMsg{gen: l.gen + 1, root: "/w/p1/.worktrees/feat-a", stats: git.Stats{Files: 1, Added: 12}})

	lines := listLines(t, a)
	after := plainLines(lines)

	i := lineHolding(after, "other · main  latest")
	if i < 0 || !strings.Contains(after[i], "+41 -7  3 files") {
		t.Errorf("the counted row reads %q, want +41 -7  3 files", after[i])
	}

	// An answer for another list does not count.
	if j := lineHolding(after, "feat/follow-edits"); j < 0 || strings.Contains(after[j], "+12") {
		t.Errorf("the row of another generation's answer reads %q", after[j])
	}

	j := lineHolding(after, "multiplex · main  agent here")
	if j < 0 || !strings.Contains(after[j], "clean") {
		t.Fatalf("the clean row reads %q", after[j])
	}

	ui := a.current().theme.UI
	vt := lineVT(t, lines[j], ansi.StringWidth(lines[j]))
	lead, _, _ := strings.Cut(after[j], "clean")
	x := ansi.StringWidth(lead)

	assertColours(t, vt, x, 0, "c", ui.Subtle, ui.Panel, false)
}

func TestWorktreeListBlocksMouse(t *testing.T) {
	t.Parallel()

	s := reviewScene{theme: "catppuccin-mocha", icons: true, w: 200, h: 52, res: turn4Result(), turn: 4, turns: 4, focused: true}
	a := s.build(t)

	if a.mouseBlocked() {
		t.Fatal("the mouse is blocked with no list open")
	}

	showList(a, wtDemoRows(), "", 0)

	if !a.mouseBlocked() {
		t.Error("the mouse is not blocked while the list is open")
	}

	focus := a.store.State().Tab().Focus

	a.Update(tea.MouseClickMsg{X: 5, Y: 10, Button: tea.MouseLeft})
	a.Update(tea.MouseReleaseMsg{X: 5, Y: 10, Button: tea.MouseLeft})

	if got := a.store.State().Tab().Focus; got != focus {
		t.Errorf("a click moved the focus from %s to %s while the list was open", focus, got)
	}
}

func TestWorktreeListCleansNames(t *testing.T) {
	t.Parallel()

	s := reviewScene{theme: "catppuccin-mocha", icons: true, w: 200, h: 52, res: turn4Result(), turn: 4, turns: 4, focused: true}
	a := s.build(t)

	rows := []wtRow{
		{kind: wtFollow},
		{kind: wtHead, text: "Touched by ag\x1b[31m\u200bent"},
		{kind: wtPlace, root: "/w/x", repo: "re\x1b[2J\u200bpo", branch: "feat\x1b[31m\u200bx", latest: true},
	}
	showList(a, rows, "re\x1b[2J\u200bpo · feat\x1b[31m\u200bx", 2)

	lines := listLines(t, a)
	raw := strings.Join(lines, "\n")

	for _, bad := range []string{"\x1b[31m", "\x1b[2J", "\u200b"} {
		if strings.Contains(raw, bad) {
			t.Errorf("the drawer carries %q", bad)
		}
	}

	// What is left is printable text.
	text := strings.Join(plainLines(lines), "\n")
	for _, want := range []string{"Touched by ag[31ment", "re[2Jpo · feat[31mx  latest", "now re[2Jpo · feat[31mx"} {
		if !strings.Contains(text, want) {
			t.Errorf("the list lacks %q:\n%s", want, text)
		}
	}
}

// wtManyRows is a Follow row, a header and n places.
func wtManyRows(n int) []wtRow {
	rows := []wtRow{{kind: wtFollow}, {kind: wtHead, text: "Touched by multiplex"}}

	for i := range n {
		rows = append(rows, wtRow{kind: wtPlace, root: fmt.Sprintf("/w/wt%02d", i), repo: "multiplex", branch: fmt.Sprintf("branch-%02d", i)})
	}

	return rows
}

func TestWorktreeListScrolls(t *testing.T) {
	t.Parallel()

	// The body is two rows shorter than the screen, so a screen of 32 rows has a drawer of 30.
	s := reviewScene{theme: "catppuccin-mocha", icons: true, w: 200, h: 32, res: turn4Result(), turn: 4, turns: 4, focused: true}
	a := s.build(t)

	rows := wtManyRows(40)
	l := showList(a, rows, "multiplex · main", 0)

	rect := drawerRectOf(t, a, 0)
	if rect.H != 30 {
		t.Fatalf("the drawer is %d rows tall, want 30", rect.H)
	}

	check := func(step string) {
		t.Helper()

		lines := plainLines(listLines(t, a))
		if len(lines) != rect.H {
			t.Fatalf("%s: the drawer has %d lines, want %d", step, len(lines), rect.H)
		}

		for i, line := range lines {
			if w := ansi.StringWidth(line); w != rect.W {
				t.Errorf("%s: line %d is %d cells wide, want %d", step, i, w, rect.W)
			}
		}

		sel := l.rows[l.sel]
		if sel.kind == wtPlace {
			if i := lineHolding(lines, sel.branch); i < 0 {
				t.Errorf("%s: the selected row %q is not drawn:\n%s", step, sel.branch, strings.Join(lines, "\n"))
			}
		}

		top := lineHolding(lines, "Worktrees")
		hints := lineHolding(lines, "j/k move")

		if top != 3 {
			t.Errorf("%s: the box starts on line %d, want line 3", step, top)
		}

		// The box's bottom border is the last line before the drawer's own.
		bottom := hints + 1
		if hints < 0 || !strings.HasPrefix(strings.TrimLeft(lines[bottom], "│ "), "╰") || bottom > rect.H-2 {
			t.Errorf("%s: the hint row is on line %d and the box ends on line %d of %d:\n%s", step, hints, bottom, rect.H, strings.Join(lines, "\n"))
		}

		if last := lines[rect.H-1]; !strings.HasPrefix(last, "╰") || !strings.HasSuffix(last, "╯") {
			t.Errorf("%s: the drawer's bottom border reads %q", step, last)
		}
	}

	check("opened")

	for range len(rows) {
		a.Update(keyRune('j'))
		check(fmt.Sprintf("selected row %d", l.sel))
	}

	if l.sel != len(rows)-1 {
		t.Fatalf("the selection ended on row %d, want the last row %d", l.sel, len(rows)-1)
	}

	for range len(rows) {
		a.Update(keyRune('k'))
		check(fmt.Sprintf("selected row %d", l.sel))
	}

	if l.sel != 0 {
		t.Errorf("the selection ended on row %d, want the Follow row", l.sel)
	}
}

func TestWorktreeGeometry(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name         string
		w, h, rows   int
		ok           bool
		boxH, vis, y int
	}{
		{"every row fits", 100, 30, 8, true, 12, 8, 3},
		{"more rows than room", 100, 30, 42, true, 26, 22, 3},
		{"one visible row", 100, 9, 8, true, 5, 1, 3},
		{"no room for a row", 100, 8, 8, false, 0, 0, 0},
		{"too narrow", 12, 30, 8, false, 0, 0, 0},
		{"no rows", 100, 30, 0, false, 0, 0, 0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			g, ok := wtGeometry(tt.w, tt.h, tt.rows)
			if ok != tt.ok || (ok && (g.h != tt.boxH || g.vis != tt.vis || g.y != tt.y || g.x != 2 || g.w != tt.w-4)) {
				t.Errorf("wtGeometry(%d, %d, %d) = %+v, %v; want box %d tall with %d rows at y %d, ok %v", tt.w, tt.h, tt.rows, g, ok, tt.boxH, tt.vis, tt.y, tt.ok)
			}

			// The box always ends above the drawer's bottom border.
			if ok && g.y+g.h > tt.h-1 {
				t.Errorf("the box ends on row %d of a drawer %d tall", g.y+g.h-1, tt.h)
			}
		})
	}
}

func TestWorktreeTop(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name                string
		top, sel, vis, n, w int
	}{
		{"selected is shown", 3, 5, 4, 20, 3},
		{"above the window", 3, 1, 4, 20, 1},
		{"below the window", 3, 9, 4, 20, 6},
		{"the window is past the end", 30, 19, 4, 20, 16},
		{"a negative top", -2, 0, 4, 20, 0},
		{"fewer rows than room", 5, 2, 10, 4, 0},
	}

	for _, tt := range tests {
		if got := wtTop(tt.top, tt.sel, tt.vis, tt.n); got != tt.w {
			t.Errorf("%s: wtTop(%d, %d, %d, %d) = %d, want %d", tt.name, tt.top, tt.sel, tt.vis, tt.n, got, tt.w)
		}
	}
}

func TestWorktreeListSearchClosesIt(t *testing.T) {
	t.Parallel()

	s := reviewScene{theme: "catppuccin-mocha", icons: true, w: 200, h: 52, res: turn4Result(), turn: 4, turns: 4, focused: true}
	a := s.build(t)

	showList(a, wtDemoRows(), "", 0)

	if cmd := a.reviewSearch(); cmd != nil {
		t.Error("review.search returned a command")
	}

	if a.drawer.list != nil || a.reviewViewOf(a.store.State().Tab().ID).search == nil {
		t.Error("the search prompt and the list are open together")
	}
}
