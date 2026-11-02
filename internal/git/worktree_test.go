package git

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// seededRepo is newRepo with one commit, opened.
func seededRepo(t *testing.T) (string, Repo) {
	t.Helper()

	dir := copyTemplate(t, &seededTemplate, func(dir string) error {
		return buildRepo(dir, map[string]string{"a.txt": "a\n"})
	})

	r, err := Open(t.Context(), dir)
	if err != nil {
		t.Fatal(err)
	}

	return dir, r
}

func TestWorktreeDir(t *testing.T) {
	t.Parallel()

	tests := []struct {
		branch  string
		want    string
		wantErr bool
	}{
		{branch: "feat/auth", want: "feat-auth"},
		{branch: "a//b", want: "a-b"},
		{branch: "-x.", want: "x"},
		{branch: "v1.2_x", want: "v1.2_x"},
		{branch: "weird name!", want: "weird-name"},
		{branch: "..", wantErr: true},
		{branch: "", wantErr: true},
	}

	for _, tt := range tests {
		got, err := WorktreeDir(tt.branch)
		if tt.wantErr {
			if !errors.Is(err, ErrBadBranch) {
				t.Errorf("WorktreeDir(%q) err = %v, want ErrBadBranch", tt.branch, err)
			}

			continue
		}

		if err != nil || got != tt.want {
			t.Errorf("WorktreeDir(%q) = %q, %v; want %q", tt.branch, got, err, tt.want)
		}
	}
}

func TestListWorktrees(t *testing.T) {
	t.Parallel()
	dir, r := seededRepo(t)

	base, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}

	branchWT := filepath.Join(base, "on-branch")
	gitOut(t, dir, "worktree", "add", "-b", "feat/x", branchWT)

	detachedWT := filepath.Join(base, "detached")
	gitOut(t, dir, "worktree", "add", "--detach", detachedWT)

	lockedWT := filepath.Join(base, "locked")
	gitOut(t, dir, "worktree", "add", "-b", "locked-b", lockedWT)
	gitOut(t, dir, "worktree", "lock", "--reason", "on a usb stick", lockedWT)

	goneWT := filepath.Join(base, "gone")
	gitOut(t, dir, "worktree", "add", "-b", "gone-b", goneWT)

	if err := os.RemoveAll(goneWT); err != nil {
		t.Fatal(err)
	}

	oddWT := filepath.Join(base, "odd dir\nline")
	gitOut(t, dir, "worktree", "add", "-b", "odd-b", oddWT)

	got, err := ListWorktrees(t.Context(), r)
	if err != nil {
		t.Fatal(err)
	}

	byPath := map[string]Worktree{}
	for _, w := range got {
		byPath[w.Path] = w
	}

	if len(got) != 6 || got[0].Path != dir {
		t.Fatalf("worktrees = %+v", got)
	}

	head := gitOut(t, dir, "rev-parse", "HEAD")

	if w := got[0]; !w.Main || w.Branch != "main" || w.Head != head || w.Detached || w.Bare {
		t.Errorf("main = %+v", w)
	}

	if w := byPath[branchWT]; w.Main || w.Branch != "feat/x" || w.Head != head {
		t.Errorf("branch worktree = %+v", w)
	}

	if w := byPath[detachedWT]; !w.Detached || w.Branch != "" {
		t.Errorf("detached worktree = %+v", w)
	}

	if w := byPath[lockedWT]; !w.Locked || w.LockReason != "on a usb stick" {
		t.Errorf("locked worktree = %+v", w)
	}

	if w := byPath[goneWT]; !w.Prunable || w.PruneReason == "" {
		t.Errorf("prunable worktree = %+v", w)
	}

	if w, ok := byPath[oddWT]; !ok || w.Branch != "odd-b" {
		t.Errorf("odd path worktree = %+v (found %v)", w, ok)
	}
}

// bareWithWorktree clones a seeded repo to <parent>/repo.git (bare) and adds a
// linked worktree on branch side.
func bareWithWorktree(t *testing.T) (parent, bare, wt string) {
	t.Helper()

	src, _ := seededRepo(t)

	var err error

	parent, err = filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}

	bare = filepath.Join(parent, "repo.git")
	gitOut(t, parent, "clone", "-q", "--bare", src, bare)

	wt = filepath.Join(parent, "linked")
	gitOut(t, bare, "worktree", "add", "-q", "-b", "side", wt, "main")

	return parent, bare, wt
}

func TestCreateWorktreeInside(t *testing.T) {
	t.Parallel()
	dir, r := seededRepo(t)

	got, err := CreateWorktree(t.Context(), r, NewWorktree{Branch: "feat/auth", Root: RootInside})
	if err != nil {
		t.Fatal(err)
	}

	want := filepath.Join(dir, ".worktrees", "feat-auth")
	if got.Worktree.Path != want || got.Worktree.Branch != "feat/auth" || got.Worktree.Main {
		t.Errorf("worktree = %+v, want path %s", got.Worktree, want)
	}

	if !got.Excluded {
		t.Error("Excluded = false on first creation")
	}

	if gitOut(t, want, "rev-parse", "HEAD") != gitOut(t, dir, "rev-parse", "main") {
		t.Error("new branch does not start at main")
	}

	exclude := readFile(t, filepath.Join(dir, ".git", "info", "exclude"))
	if strings.Count(exclude, "/.worktrees/\n") != 1 {
		t.Errorf("exclude = %q", exclude)
	}

	if _, err := os.Stat(filepath.Join(dir, ".gitignore")); !errors.Is(err, os.ErrNotExist) {
		t.Errorf(".gitignore stat err = %v, want not exist", err)
	}
}

func TestCreateWorktreeExcludeFile(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name         string
		existing     string // "" = file absent
		absent       bool
		wantContent  string
		wantExcluded bool
	}{
		{name: "info missing", absent: true, wantContent: "/.worktrees/\n", wantExcluded: true},
		{name: "no trailing newline", existing: "foo", wantContent: "foo\n/.worktrees/\n", wantExcluded: true},
		{name: "already present", existing: "# c\n/.worktrees/\nfoo", wantContent: "# c\n/.worktrees/\nfoo"},
		{name: "similar line only", existing: ".worktrees\n", wantContent: ".worktrees\n/.worktrees/\n", wantExcluded: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			dir, r := seededRepo(t)
			info := filepath.Join(dir, ".git", "info")
			path := filepath.Join(info, "exclude")

			if err := os.RemoveAll(info); err != nil {
				t.Fatal(err)
			}

			if !tt.absent {
				writeFile(t, info, "exclude", tt.existing)
			}

			got, err := CreateWorktree(t.Context(), r, NewWorktree{Branch: "b", Root: RootInside})
			if err != nil {
				t.Fatal(err)
			}

			if got.Excluded != tt.wantExcluded {
				t.Errorf("Excluded = %v, want %v", got.Excluded, tt.wantExcluded)
			}

			if c := readFile(t, path); c != tt.wantContent {
				t.Errorf("exclude = %q, want %q", c, tt.wantContent)
			}
		})
	}
}

func TestCreateWorktreeSibling(t *testing.T) {
	t.Parallel()
	dir, r := seededRepo(t)

	got, err := CreateWorktree(t.Context(), r, NewWorktree{Branch: "feat/s", Root: RootSibling})
	if err != nil {
		t.Fatal(err)
	}

	want := dir + ".worktrees/feat-s"
	if got.Worktree.Path != want {
		t.Errorf("path = %s, want %s", got.Worktree.Path, want)
	}

	if got.Excluded || len(got.Tools) != 0 {
		t.Errorf("sibling result = %+v", got)
	}

	if strings.Contains(readFile(t, filepath.Join(dir, ".git", "info", "exclude")), "/.worktrees/") {
		t.Error("sibling creation touched exclude")
	}
}

func TestCreateWorktreeToolProblemsDoNotFail(t *testing.T) {
	t.Parallel()
	dir, r := seededRepo(t)
	writeFile(t, dir, ".vscode", "a file, not a directory")

	if err := os.MkdirAll(filepath.Join(dir, "Dockerfile"), 0o755); err != nil {
		t.Fatal(err)
	}

	writeFile(t, dir, ".idea/x", "x")

	got, err := CreateWorktree(t.Context(), r, NewWorktree{Branch: "t", Root: RootInside})
	if err != nil {
		t.Fatal(err)
	}

	if got.Worktree.Path == "" || len(got.Tools) != 1 || got.Tools[0].Name != "JetBrains" {
		t.Errorf("created = %+v", got)
	}
}

func TestCreateWorktreeExistingBranch(t *testing.T) {
	t.Parallel()
	dir, r := seededRepo(t)
	gitOut(t, dir, "branch", "topic")

	got, err := CreateWorktree(t.Context(), r, NewWorktree{Branch: "topic", Root: RootInside})
	if err != nil {
		t.Fatal(err)
	}

	if got.Worktree.Branch != "topic" {
		t.Errorf("branch = %q", got.Worktree.Branch)
	}
}

func TestCreateWorktreeBase(t *testing.T) {
	t.Parallel()
	dir, r := seededRepo(t)
	gitOut(t, dir, "branch", "release")
	writeFile(t, dir, "b.txt", "b\n")
	commitAll(t, dir, "second")

	got, err := CreateWorktree(t.Context(), r, NewWorktree{Branch: "fix", Base: "release", Root: RootInside})
	if err != nil {
		t.Fatal(err)
	}

	if gitOut(t, got.Worktree.Path, "rev-parse", "HEAD") != gitOut(t, dir, "rev-parse", "release") {
		t.Error("branch did not start at the base")
	}

	_, err = CreateWorktree(t.Context(), r, NewWorktree{Branch: "fix2", Base: "--orphan", Root: RootInside})
	if !errors.Is(err, ErrNoBase) {
		t.Errorf("dash base err = %v, want ErrNoBase", err)
	}
}

func TestCreateWorktreeFromLinked(t *testing.T) {
	t.Parallel()
	dir, r := seededRepo(t)

	first, err := CreateWorktree(t.Context(), r, NewWorktree{Branch: "one", Root: RootInside})
	if err != nil {
		t.Fatal(err)
	}

	linked, err := Open(t.Context(), first.Worktree.Path)
	if err != nil {
		t.Fatal(err)
	}

	second, err := CreateWorktree(t.Context(), linked, NewWorktree{Branch: "two", Root: RootInside})
	if err != nil {
		t.Fatal(err)
	}

	if want := filepath.Join(dir, ".worktrees", "two"); second.Worktree.Path != want {
		t.Errorf("path = %s, want %s", second.Worktree.Path, want)
	}

	if !strings.Contains(readFile(t, filepath.Join(dir, ".git", "info", "exclude")), "/.worktrees/\n") {
		t.Error("common dir exclude lacks the line")
	}

	if second.Excluded {
		t.Error("Excluded = true although the first creation added the line")
	}
}

func TestCreateWorktreeBare(t *testing.T) {
	t.Parallel()
	parent, _, wt := bareWithWorktree(t)

	r, err := Open(t.Context(), wt)
	if err != nil {
		t.Fatal(err)
	}

	listed, err := ListWorktrees(t.Context(), r)
	if err != nil {
		t.Fatal(err)
	}

	if len(listed) != 2 || !listed[0].Bare || !listed[0].Main || listed[0].Branch != "" || listed[1].Branch != "side" {
		t.Errorf("worktrees = %+v", listed)
	}

	_, err = CreateWorktree(t.Context(), r, NewWorktree{Branch: "x", Root: RootInside})
	if !errors.Is(err, ErrBare) {
		t.Errorf("inside err = %v, want ErrBare", err)
	}

	got, err := CreateWorktree(t.Context(), r, NewWorktree{Branch: "x", Base: "main", Root: RootSibling})
	if err != nil {
		t.Fatal(err)
	}

	if want := filepath.Join(parent, "repo.worktrees", "x"); got.Worktree.Path != want {
		t.Errorf("path = %s, want %s", got.Worktree.Path, want)
	}
}

// WorktreePath names the path CreateWorktree makes, without touching the disk.
func TestWorktreePath(t *testing.T) {
	t.Parallel()
	dir, r := seededRepo(t)

	tests := []struct {
		name   string
		style  RootStyle
		branch string
		want   string
		err    error
	}{
		{name: "inside", style: RootInside, branch: "feat/auth", want: filepath.Join(dir, ".worktrees", "feat-auth")},
		{name: "sibling", style: RootSibling, branch: "feat/s", want: dir + ".worktrees/feat-s"},
		{name: "no directory name", style: RootInside, branch: "!!!", err: ErrBadBranch},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got, err := WorktreePath(r, tt.style, tt.branch)
			if !errors.Is(err, tt.err) || got != tt.want {
				t.Fatalf("WorktreePath() = %q, %v, want %q, %v", got, err, tt.want, tt.err)
			}

			// It reads nothing: the path does not exist and still has an answer.
			if _, statErr := os.Lstat(got); got != "" && !errors.Is(statErr, os.ErrNotExist) {
				t.Errorf("Lstat(%s) = %v, want not exist", got, statErr)
			}
		})
	}
}

// git's first line of stderr is its progress report; a failure must carry the
// line that says why.
func TestCreateWorktreeFailureNamesGitsReason(t *testing.T) {
	t.Parallel()
	_, r := seededRepo(t)

	_, err := CreateWorktree(t.Context(), r, NewWorktree{Branch: "main", Root: RootInside})
	if err == nil || !strings.Contains(err.Error(), "fatal:") {
		t.Fatalf("err = %v, want it to carry a fatal: line", err)
	}

	if strings.Contains(err.Error(), "Preparing worktree") {
		t.Errorf("err = %v, want git's reason and not its progress line", err)
	}
}

func TestCreateWorktreeRefusals(t *testing.T) {
	t.Parallel()
	dir, r := seededRepo(t)

	if err := os.MkdirAll(filepath.Join(dir, ".worktrees", "taken"), 0o755); err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name   string
		branch string
		want   error
	}{
		{name: "existing path", branch: "taken", want: ErrExists},
		{name: "double dot", branch: "a..b", want: ErrBadBranch},
		{name: "empty", branch: "", want: ErrBadBranch},
		{name: "leading dash", branch: "-x", want: ErrBadBranch},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := CreateWorktree(t.Context(), r, NewWorktree{Branch: tt.branch, Root: RootInside})
			if !errors.Is(err, tt.want) {
				t.Errorf("err = %v, want %v", err, tt.want)
			}
		})
	}
}

func TestDefaultBranch(t *testing.T) {
	t.Parallel()
	t.Run("origin head", func(t *testing.T) {
		t.Parallel()
		dir, r := seededRepo(t)
		gitOut(t, dir, "update-ref", "refs/remotes/origin/trunk", "HEAD")
		gitOut(t, dir, "symbolic-ref", "refs/remotes/origin/HEAD", "refs/remotes/origin/trunk")
		assertDefault(t, r, "origin/trunk")
	})

	t.Run("main only", func(t *testing.T) {
		t.Parallel()
		_, r := seededRepo(t)
		assertDefault(t, r, "main")
	})

	t.Run("master only", func(t *testing.T) {
		t.Parallel()
		dir, r := seededRepo(t)
		gitOut(t, dir, "branch", "-m", "main", "master")
		assertDefault(t, r, "master")
	})

	t.Run("neither", func(t *testing.T) {
		t.Parallel()
		dir, r := seededRepo(t)
		gitOut(t, dir, "branch", "-m", "main", "dev")

		_, err := DefaultBranch(t.Context(), r)
		if !errors.Is(err, ErrNoBase) {
			t.Errorf("err = %v, want ErrNoBase", err)
		}
	})

	t.Run("origin head outside origin", func(t *testing.T) {
		t.Parallel()
		dir, r := seededRepo(t)
		gitOut(t, dir, "update-ref", "refs/remotes/up/dev", "HEAD")
		gitOut(t, dir, "symbolic-ref", "refs/remotes/origin/HEAD", "refs/remotes/up/dev")
		assertDefault(t, r, "main")
	})

	t.Run("origin head dangling", func(t *testing.T) {
		t.Parallel()
		dir, r := seededRepo(t)
		gitOut(t, dir, "symbolic-ref", "refs/remotes/origin/HEAD", "refs/remotes/origin/gone")
		assertDefault(t, r, "main")
	})
}

func assertDefault(t *testing.T, r Repo, want string) {
	t.Helper()

	got, err := DefaultBranch(context.Background(), r)
	if err != nil || got != want {
		t.Errorf("DefaultBranch = %q, %v; want %q", got, err, want)
	}
}

func readFile(t *testing.T, path string) string {
	t.Helper()

	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}

	return string(b)
}

func TestCreateWorktreeTracksNothing(t *testing.T) {
	t.Parallel()
	src, r := seededRepo(t)
	gitOut(t, src, "remote", "add", "origin", "https://example.invalid/o/r.git")
	gitOut(t, src, "update-ref", "refs/remotes/origin/main", "HEAD")
	gitOut(t, src, "symbolic-ref", "refs/remotes/origin/HEAD", "refs/remotes/origin/main")

	got, err := CreateWorktree(t.Context(), r, NewWorktree{Branch: "feat/up", Root: RootInside})
	if err != nil {
		t.Fatal(err)
	}

	if got.Worktree.Branch != "feat/up" {
		t.Fatalf("branch = %q", got.Worktree.Branch)
	}

	for _, key := range []string{"remote", "merge"} {
		cmd := exec.Command("git", "config", "--get", "branch.feat/up."+key)
		cmd.Dir = src

		if out, err := cmd.Output(); err == nil {
			t.Errorf("branch.feat/up.%s = %q, want unset", key, out)
		}
	}
}

func TestListWorktreesThroughSymlinkedPaths(t *testing.T) {
	t.Parallel()
	dir, _ := seededRepo(t)

	tmp := resolvedTemp(t)
	realParent := filepath.Join(tmp, "real")
	linkParent := filepath.Join(tmp, "link")

	if err := os.Mkdir(realParent, 0o755); err != nil {
		t.Fatal(err)
	}

	if err := os.Symlink(realParent, linkParent); err != nil {
		t.Fatal(err)
	}

	gitOut(t, dir, "worktree", "add", "-q", "-b", "viaLink", filepath.Join(linkParent, "wt"))

	linkedRepo := filepath.Join(tmp, "repolink")
	if err := os.Symlink(dir, linkedRepo); err != nil {
		t.Fatal(err)
	}

	r, err := Open(t.Context(), linkedRepo)
	if err != nil {
		t.Fatal(err)
	}

	got, err := ListWorktrees(t.Context(), r)
	if err != nil {
		t.Fatal(err)
	}

	found := false

	for _, w := range got {
		if w.Branch == "viaLink" {
			found = true

			if w.Path != filepath.Join(realParent, "wt") {
				t.Errorf("path = %s, want the resolved %s", w.Path, filepath.Join(realParent, "wt"))
			}
		}
	}

	if !found || len(got) != 2 {
		t.Errorf("worktrees = %+v, want main and viaLink", got)
	}
}

func TestCreateWorktreeBaseWithBidiOverride(t *testing.T) {
	t.Parallel()
	dir, r := seededRepo(t)

	base := "re" + string(rune(0x202e)) + "lease"
	gitOut(t, dir, "branch", base)
	writeFile(t, dir, "b.txt", "b\n")
	commitAll(t, dir, "second")

	got, err := CreateWorktree(t.Context(), r, NewWorktree{Branch: "fix", Base: base, Root: RootInside})
	if err != nil {
		t.Fatal(err)
	}

	if gitOut(t, got.Worktree.Path, "rev-parse", "HEAD") != gitOut(t, dir, "rev-parse", "refs/heads/"+base) {
		t.Error("branch did not start at the base")
	}
}
