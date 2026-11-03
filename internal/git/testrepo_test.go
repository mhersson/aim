package git

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"syscall"
	"testing"
)

// repoTemplate is a fixture repository built once per test binary and copied
// per test. It is built on first use and removed by TestMain.
type repoTemplate struct {
	once sync.Once
	dir  string
	err  error
}

var (
	seedTemplate     repoTemplate
	baseTemplate     repoTemplate
	seededTemplate   repoTemplate
	updateTemplate   repoTemplate
	pushTemplate     repoTemplate
	trackingTemplate repoTemplate
)

// copyTemplate copies tpl into a fresh symlink-free temp dir and returns its
// path, building tpl first with build. Templates hold no refs/aim refs, linked
// worktrees or absolute symlinks (CopyFS keeps them, so they would point into the
// template), and reach other repositories by relative paths only.
func copyTemplate(t *testing.T, tpl *repoTemplate, build func(dir string) error) string {
	t.Helper()

	tpl.once.Do(func() {
		tpl.dir, tpl.err = os.MkdirTemp("", "git-template-")
		if tpl.err == nil {
			tpl.err = build(tpl.dir)
		}
	})

	if tpl.err != nil {
		t.Fatalf("build template: %v", tpl.err)
	}

	dst, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}

	if err := os.CopyFS(dst, os.DirFS(tpl.dir)); err != nil {
		t.Fatalf("copy template: %v", err)
	}

	return dst
}

// gitRun runs git for a template builder, with fixed commit dates so the ids
// are the same on every run. The error carries git's output.
func gitRun(dir string, args ...string) error {
	const date = "2020-01-01T00:00:00Z"

	cmd := exec.Command("git", args...)
	cmd.Dir = dir

	cmd.Env = append(os.Environ(), "GIT_AUTHOR_DATE="+date, "GIT_COMMITTER_DATE="+date)

	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("git %s: %w\n%s", strings.Join(args, " "), err, out)
	}

	return nil
}

// buildRepo makes dir a repository on main holding files and one commit.
func buildRepo(dir string, files map[string]string) error {
	if err := gitRun(dir, "init", "-q", "-b", "main"); err != nil {
		return err
	}

	for name, content := range files {
		if err := writeBuilt(dir, name, content); err != nil {
			return err
		}
	}

	if err := gitRun(dir, "add", "-A"); err != nil {
		return err
	}

	return gitRun(dir, "commit", "-q", "-m", "init")
}

func writeBuilt(dir, rel, content string) error {
	path := filepath.Join(dir, rel)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}

	return os.WriteFile(path, []byte(content), 0o644)
}

// TestMain isolates git from the machine once for the binary, so tests that
// build repositories need no t.Setenv.
func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "git-test-")
	if err != nil {
		panic(err)
	}

	env, err := gitTestEnv(dir)
	if err != nil {
		panic(err)
	}

	for k, v := range env {
		if err := os.Setenv(k, v); err != nil {
			panic(err)
		}
	}

	code := m.Run()

	for _, tpl := range []*repoTemplate{&seedTemplate, &baseTemplate, &seededTemplate, &updateTemplate, &pushTemplate, &trackingTemplate} {
		if tpl.dir != "" {
			_ = os.RemoveAll(tpl.dir)
		}
	}

	_ = os.RemoveAll(dir)

	os.Exit(code)
}

// gitTestEnv writes the tests' global git config into dir and returns the
// environment that points every git the tests start at it, with a fixed
// identity. The config turns auto-maintenance off; it is a file because
// cleanEnv and git's local transport drop GIT_CONFIG_COUNT. XDG_CONFIG_HOME
// moves git's default ignore and attributes files into dir, where none exist.
func gitTestEnv(dir string) (map[string]string, error) {
	cfg := filepath.Join(dir, "gitconfig")
	if err := os.WriteFile(cfg, []byte("[maintenance]\n\tauto = false\n"), 0o600); err != nil {
		return nil, err
	}

	return map[string]string{
		"GIT_CONFIG_NOSYSTEM": "1",
		"GIT_ATTR_NOSYSTEM":   "1",
		"GIT_CONFIG_GLOBAL":   cfg,
		"XDG_CONFIG_HOME":     dir,
		"GIT_AUTHOR_NAME":     "Test",
		"GIT_AUTHOR_EMAIL":    "test@example.com",
		"GIT_COMMITTER_NAME":  "Test",
		"GIT_COMMITTER_EMAIL": "test@example.com",
	}, nil
}

// The machine's ignore and attributes files, wherever its HOME and
// XDG_CONFIG_HOME put them, never reach a git the tests start.
func TestGitTestEnvReadsNoMachineFiles(t *testing.T) {
	t.Parallel()

	repo := newRepo(t)
	writeFile(t, repo, "f.planted", "x\n")

	home, xdg := t.TempDir(), t.TempDir()
	for _, dir := range []string{filepath.Join(home, ".config", "git"), filepath.Join(xdg, "git")} {
		writeFile(t, dir, "ignore", "*.planted\n")
		writeFile(t, dir, "attributes", "* planted\n")
	}

	env, err := gitTestEnv(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}

	for _, machine := range [][]string{{"HOME=" + home, "XDG_CONFIG_HOME=" + xdg}, {"HOME=" + home}} {
		vars := append([]string{"PATH=" + os.Getenv("PATH")}, machine...)
		for k, v := range env {
			vars = append(vars, k+"="+v)
		}

		git := func(args ...string) string {
			cmd := exec.Command("git", args...)
			cmd.Dir = repo
			cmd.Env = vars

			out, _ := cmd.Output()

			return strings.TrimSpace(string(out))
		}

		if got := git("ls-files", "--others", "--exclude-standard"); got != "f.planted" {
			t.Errorf("%v: untracked = %q, want f.planted (no machine ignore file)", machine, got)
		}

		if got := git("check-attr", "planted", "--", "f.planted"); got != "f.planted: planted: unspecified" {
			t.Errorf("%v: check-attr = %q, want planted unspecified (no machine attributes file)", machine, got)
		}

		if got := git("var", "GIT_ATTR_SYSTEM"); got != "" {
			t.Errorf("%v: system attributes file = %q, want none", machine, got)
		}
	}
}

// No git the tests run may start auto-maintenance, the app's own calls
// included: its detached run outlives the git that started it and races the
// removal of the repository.
func TestGitStartsNoAutoMaintenance(t *testing.T) {
	t.Parallel()

	repo, clone, remote := newRepo(t), newRepo(t), t.TempDir()
	gitOut(t, remote, "init", "-q", "--bare", "-b", "main")

	steps := []struct {
		name string
		dir  string
		args []string
		want string
	}{
		{"commit", repo, []string{"commit", "-q", "--allow-empty", "-m", "one"}, "commit"},
		{"push", repo, []string{"push", "-q", remote, "main"}, "receive-pack"},
		{"fetch", clone, []string{"fetch", "-q", repo, "main"}, "fetch"},
	}

	for _, s := range steps {
		trace := filepath.Join(t.TempDir(), "trace")

		_, err := run(context.Background(), call{dir: s.dir, args: s.args, env: []string{"GIT_TRACE2_EVENT=" + trace}})
		if err != nil {
			t.Fatalf("%s: %v", s.name, err)
		}

		names := gitCommands(t, trace)
		if !slices.Contains(names, s.want) || slices.Contains(names, "maintenance") {
			t.Errorf("%s ran %v, want %s and no maintenance", s.name, names, s.want)
		}
	}
}

// gitCommands returns the name of every git command the trace2 event file at
// path records, children included.
func gitCommands(t *testing.T, path string) []string {
	t.Helper()

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}

	var names []string

	for line := range bytes.Lines(data) {
		var ev struct {
			Event string `json:"event"`
			Name  string `json:"name"`
		}

		if err := json.Unmarshal(line, &ev); err != nil {
			t.Fatalf("trace line %q: %v", line, err)
		}

		if ev.Event == "cmd_name" {
			names = append(names, ev.Name)
		}
	}

	return names
}

// newRepo creates an empty repository on branch main in a temp dir and returns
// its symlink-free path.
func newRepo(t *testing.T) string {
	t.Helper()

	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}

	gitOut(t, dir, "init", "-q", "-b", "main")

	return dir
}

func writeFile(t *testing.T, dir, rel, content string) {
	t.Helper()

	path := filepath.Join(dir, rel)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}

	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// fsAcceptsName reports whether the temp filesystem takes name; APFS refuses names that are not valid UTF-8.
func fsAcceptsName(t *testing.T, name string) bool {
	t.Helper()

	err := os.WriteFile(filepath.Join(t.TempDir(), name), nil, 0o600)
	if errors.Is(err, syscall.EILSEQ) {
		return false
	}

	if err != nil {
		t.Fatal(err)
	}

	return true
}

func commitAll(t *testing.T, dir, msg string) {
	t.Helper()
	gitOut(t, dir, "add", "-A")
	gitOut(t, dir, "commit", "-q", "-m", msg)
}

// gitOut runs git directly for test setup and returns trimmed stdout.
func gitOut(t *testing.T, dir string, args ...string) string {
	t.Helper()

	return gitInput(t, dir, "", args...)
}

// gitInput is gitOut with stdin on the command's standard input.
func gitInput(t *testing.T, dir, stdin string, args ...string) string {
	t.Helper()

	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Stdin = strings.NewReader(stdin)

	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
	}

	return strings.TrimSpace(string(out))
}

// fakeGit puts a git wrapper first on PATH. It appends its arguments, one call
// per line, to the returned log, runs extraShell (which may use $LOG), then
// execs the real git.
func fakeGit(t *testing.T, extraShell string) (logPath string) {
	t.Helper()

	script, logPath := writeGitWrapper(t, extraShell)

	t.Setenv("PATH", filepath.Dir(script)+string(os.PathListSeparator)+os.Getenv("PATH"))

	return logPath
}

// wrapGit returns a context whose git calls go through the logging wrapper of
// fakeGit. Only calls given ctx (or a context derived from it) are wrapped; it
// leaves PATH alone, so the test can run in parallel.
func wrapGit(t *testing.T, extraShell string) (ctx context.Context, logPath string) {
	t.Helper()

	script, logPath := writeGitWrapper(t, extraShell)

	return context.WithValue(context.Background(), gitPathKey{}, script), logPath
}

// blockRefWrite returns a wrapGit context whose update-ref creates started,
// then waits for release to exist before it execs git. The wait is a hang
// guard that only expires when the test is already failing.
func blockRefWrite(t *testing.T) (ctx context.Context, started, release string) {
	t.Helper()

	dir := t.TempDir()
	started = filepath.Join(dir, "started")
	release = filepath.Join(dir, "release")

	ctx, _ = wrapGit(t, `case "$*" in
*update-ref*)
	: > '`+started+`'
	n=0
	while [ ! -e '`+release+`' ] && [ $n -lt 600 ]; do sleep 0.05; n=$((n+1)); done;;
esac`)

	return ctx, started, release
}

// endContextDuringRefWrite runs fn, cancels its context once its update-ref is
// blocked, then lets the update-ref go and returns what fn returned.
func endContextDuringRefWrite(t *testing.T, cancel context.CancelFunc, started, release string, fn func() error) error {
	t.Helper()

	done := make(chan error, 1)

	go func() { done <- fn() }()

	eventually(t, "the ref write to start", func() bool {
		_, err := os.Stat(started)

		return err == nil
	})
	cancel()

	if err := os.WriteFile(release, nil, 0o600); err != nil {
		t.Fatal(err)
	}

	return <-done
}

// wrappedCalls returns the wrapper's log and fails when it is missing or empty,
// so a test that asserts a call never ran cannot pass because nothing was wrapped.
func wrappedCalls(t *testing.T, logPath string) string {
	t.Helper()

	data, err := os.ReadFile(logPath)
	if err != nil || len(data) == 0 {
		t.Fatalf("the git wrapper saw no calls: %v", err)
	}

	return string(data)
}

// writeGitWrapper writes a git wrapper script and returns its path and its log.
// The script appends its arguments, one call per line, to the log, runs
// extraShell (which may use $LOG), then execs the real git.
func writeGitWrapper(t *testing.T, extraShell string) (script, logPath string) {
	t.Helper()

	realGit, err := exec.LookPath("git")
	if err != nil {
		t.Skip("git not found")
	}

	dir := t.TempDir()
	logPath = filepath.Join(dir, "calls.log")
	body := "#!/bin/sh\nLOG='" + logPath + "'\nprintf '%s ' \"$@\" >> \"$LOG\"\necho >> \"$LOG\"\n" +
		extraShell + "\nexec '" + realGit + "' \"$@\"\n"
	script = filepath.Join(dir, "git")

	// A concurrent fork can inherit the write fd and make exec fail with ETXTBSY (go.dev/issue/22315).
	syscall.ForkLock.RLock()

	err = os.WriteFile(script, []byte(body), 0o755)

	syscall.ForkLock.RUnlock()

	if err != nil {
		t.Fatal(err)
	}

	return script, logPath
}
