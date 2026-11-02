package ui

import (
	"errors"
	"fmt"
	"maps"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mhersson/aim/internal/core"
)

const (
	sweepKeyA = "0123456789abcdef"
	sweepKeyB = "fedcba9876543210"

	sessH = "aaaaaaaa-0000-4000-8000-00000000000a" // held by a pane
	sessU = "bbbbbbbb-0000-4000-8000-00000000000b" // unheld
)

func turnRefName(session, key string, n int) string {
	return fmt.Sprintf("refs/aim/turns/%s/%s/%d/start", session, key, n)
}

// sweepRepo is a fake repository with two commits.
type sweepRepo struct {
	*drawerRig
	f            *fakeGit
	rp           *fakeRepo
	dir          string
	first, other string
}

func newSweepRig(t *testing.T) *sweepRepo {
	t.Helper()

	f := newFakeGit(t)
	rp := f.repo(t, "")
	dir := rp.Root()

	write(t, filepath.Join(dir, "a.txt"), "a\n")

	first := rp.commit("one")

	write(t, filepath.Join(dir, "a.txt"), "b\n")

	other := rp.commit("two")

	return &sweepRepo{drawerRig: newKeysRig(t, dir, gitOf(f)), f: f, rp: rp, dir: dir, first: first, other: other}
}

func (s *sweepRepo) ref(name, oid string) { s.rp.aimRef(name, oid) }

// refs are the refs under refs/aim/ the fake holds, by name.
func (s *sweepRepo) refs() map[string]string {
	out := map[string]string{}

	s.f.edit(func() { maps.Copy(out, s.rp.c.aimRefs) })

	return out
}

func (s *sweepRepo) has(prefix string) bool {
	for name := range s.refs() {
		if strings.HasPrefix(name, prefix) {
			return true
		}
	}

	return false
}

// deleted are the names of the refs the app asked to delete, with their listed values.
func (s *sweepRepo) deleted(t *testing.T) map[string]string {
	t.Helper()

	out := map[string]string{}

	for _, c := range s.f.calls("DeleteRefs") {
		for _, arg := range c.Args {
			name, oid, _ := strings.Cut(arg, " ")
			if !strings.HasPrefix(name, "refs/aim/") {
				t.Errorf("a delete named %q, outside refs/aim/", name)
			}

			out[name] = oid
		}
	}

	return out
}

func (s *sweepRepo) seed() {
	s.ref(turnRefName(sessH, sweepKeyA, 1), s.first)
	s.ref(turnRefName(sessU, sweepKeyA, 1), s.first)
	s.ref(turnRefName(sessU, sweepKeyB, 1), s.first)

	for _, n := range []int{5, 6, 8} {
		s.ref(fmt.Sprintf("refs/aim/pr/%d/head", n), s.first)
		s.ref(fmt.Sprintf("refs/aim/pr/%d/base", n), s.first)
	}
}

func (s *sweepRepo) turnsOf(session string) bool { return s.has("refs/aim/turns/" + session + "/") }

func (s *sweepRepo) prRefs(n int) bool { return s.has(fmt.Sprintf("refs/aim/pr/%d/", n)) }

func (s *sweepRepo) list(t *testing.T) refsListedMsg {
	t.Helper()

	if len(s.swept) != 0 {
		t.Fatalf("swept = %v before the direct call, want none", s.swept)
	}

	s.sweepRefs = true

	cmd := s.refSweep(s.rp.Repo())
	if cmd == nil {
		t.Fatal("refSweep returned no Cmd")
	}

	msg, ok := cmd().(refsListedMsg)
	if !ok {
		t.Fatal("refSweep did not answer with refsListedMsg")
	}

	return msg
}

func TestRefSweepKeepsWhatPanesAndDrawersHold(t *testing.T) {
	t.Parallel()

	s := newSweepRig(t)
	id := s.agent(t, s.dir, core.SessionID(strings.ToUpper(sessH)))
	openIn(t, s.App, core.OpenReview{Pane: id, Mode: core.ReviewTurn},
		core.ShowReviewPR{Repo: "github.com/o/r", Number: 5, Root: s.dir})

	s.drawer.fetching = map[int]int{6: 1}
	s.seed()

	msg := s.list(t)
	flatten(s.onRefsListed(msg))

	for _, c := range []struct {
		name string
		got  bool
		want bool
	}{
		{"held session", s.turnsOf(sessH), true},
		{"unheld session", s.turnsOf(sessU), false},
		{"shown PR", s.prRefs(5), true},
		{"fetching PR", s.prRefs(6), true},
		{"other PR", s.prRefs(8), false},
	} {
		if c.got != c.want {
			t.Errorf("%s present = %v, want %v", c.name, c.got, c.want)
		}
	}

	// What the app asked to delete is the stale refs with the values it listed.
	want := map[string]string{
		turnRefName(sessU, sweepKeyA, 1): s.first,
		turnRefName(sessU, sweepKeyB, 1): s.first,
		"refs/aim/pr/8/head":             s.first,
		"refs/aim/pr/8/base":             s.first,
	}

	if got := s.deleted(t); !maps.Equal(got, want) {
		t.Errorf("deleted %v, want %v", got, want)
	}
}

func TestRefSweepFiltersAtTheAnswer(t *testing.T) {
	t.Parallel()

	s := newSweepRig(t)
	s.seed()

	msg := s.list(t)

	s.agent(t, s.dir, core.SessionID(sessU))
	s.drawer.fetching = map[int]int{8: 1}

	flatten(s.onRefsListed(msg))

	if !s.turnsOf(sessU) {
		t.Error("a session taken after the listing lost its refs")
	}

	if !s.prRefs(8) {
		t.Error("a pull request fetched after the listing lost its refs")
	}

	if s.prRefs(5) {
		t.Error("an unshown pull request kept its refs")
	}
}

func TestRefSweepTurnsGoWhenAPRRefMoved(t *testing.T) {
	t.Parallel()

	s := newSweepRig(t)
	s.seed()

	msg := s.list(t)

	s.ref("refs/aim/pr/8/head", s.other)
	flatten(s.onRefsListed(msg))

	if got := s.refs()["refs/aim/pr/8/head"]; got != s.other {
		t.Errorf("moved head = %s, want it kept at %s", got, s.other)
	}

	if got := s.deleted(t)["refs/aim/pr/8/head"]; got != s.first {
		t.Errorf("the delete carried the head at %s, want the listed %s", got, s.first)
	}

	if s.turnsOf(sessU) {
		t.Error("unheld turn refs stayed behind a moved PR ref")
	}
}

func TestRefSweepOncePerRepository(t *testing.T) {
	t.Parallel()

	// repos are a main checkout, a linked worktree of it and another repository, on one fake git.
	repos := func(t *testing.T) (f *fakeGit, mainRepo, linked, elsewhere *fakeRepo) {
		t.Helper()

		f = newFakeGit(t)
		mainRepo = f.repo(t, "")
		write(t, filepath.Join(mainRepo.Root(), "a.txt"), "a\n")
		mainRepo.commit("one")

		linked = mainRepo.worktree(t, filepath.Join(t.TempDir(), "wt"), "side")

		elsewhere = f.repo(t, "")
		write(t, filepath.Join(elsewhere.Root(), "a.txt"), "a\n")
		elsewhere.commit("one")

		return f, mainRepo, linked, elsewhere
	}

	t.Run("refSweep", func(t *testing.T) {
		t.Parallel()

		f, mainRepo, linked, elsewhere := repos(t)
		r := newKeysRig(t, mainRepo.Root(), gitOf(f))
		r.sweepRefs = true

		if r.refSweep(mainRepo.Repo()) == nil {
			t.Error("the first resolution of a repository started no sweep")
		}

		if r.refSweep(linked.Repo()) != nil {
			t.Error("a linked worktree of a swept repository started another sweep")
		}

		if r.refSweep(elsewhere.Repo()) == nil {
			t.Error("another repository started no sweep")
		}

		off := newKeysRig(t, mainRepo.Root(), gitOf(f))
		if off.refSweep(mainRepo.Repo()) != nil {
			t.Error("a sweep started with SweepRefs off")
		}
	})

	t.Run("onPaneRepo", func(t *testing.T) {
		t.Parallel()

		f, mainRepo, linked, _ := repos(t)
		mainDir, linkedDir := mainRepo.Root(), linked.Root()
		r := newKeysRig(t, mainDir, gitOf(f))
		first := r.agent(t, mainDir, "")
		second := r.agent(t, linkedDir, "")
		r.watches.panes[first] = &paneWatch{cwd: mainDir}
		r.watches.panes[second] = &paneWatch{cwd: linkedDir}
		r.sweepRefs = true

		cmd := r.onPaneRepo(paneRepoMsg{id: first, cwd: mainDir, repo: mainRepo.Repo()})
		if n := countOf[refsListedMsg](flatten(cmd)); n != 1 {
			t.Errorf("first agent started %d listings, want 1", n)
		}

		cmd = r.onPaneRepo(paneRepoMsg{id: second, cwd: linkedDir, repo: linked.Repo()})
		if n := countOf[refsListedMsg](flatten(cmd)); n != 0 {
			t.Errorf("second agent in a linked worktree started %d listings, want 0", n)
		}
	})
}

func TestRefSweepAnswersThatDeleteNothing(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		msg  func(*testing.T, *sweepRepo) refsListedMsg
		kept string
	}{
		{
			name: "a listing error",
			msg: func(t *testing.T, s *sweepRepo) refsListedMsg {
				t.Helper()
				s.seed()

				msg := s.list(t)
				msg.err = errors.New("listing failed")

				return msg
			},
			kept: "refs/aim/pr/8/",
		},
		{
			name: "nothing stale",
			msg: func(t *testing.T, s *sweepRepo) refsListedMsg {
				t.Helper()
				s.agent(t, s.dir, core.SessionID(sessH))
				s.ref(turnRefName(sessH, sweepKeyA, 1), s.first)

				return s.list(t)
			},
			kept: "refs/aim/turns/" + sessH + "/",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			s := newSweepRig(t)
			msg := tc.msg(t, s)

			before := s.refs()

			if cmd := s.onRefsListed(msg); cmd != nil {
				t.Error("onRefsListed returned a Cmd, want nil")
			}

			if after := s.refs(); !maps.Equal(after, before) {
				t.Errorf("refs changed:\n%v\nwant\n%v", after, before)
			}

			if n := s.f.count("DeleteRefs"); n != 0 {
				t.Errorf("%d deletes, want none", n)
			}

			if !s.has(tc.kept) {
				t.Errorf("%s is gone", tc.kept)
			}
		})
	}
}
