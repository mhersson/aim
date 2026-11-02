package git

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/creack/pty"
)

// pushFixture is a repository on branch feat with one commit and a bare
// remote named origin.
type pushFixture struct {
	root, bare string
	repo       Repo
}

func newPushFixture(t *testing.T) pushFixture {
	t.Helper()

	dir := copyTemplate(t, &pushTemplate, func(dir string) error {
		root, bare := filepath.Join(dir, "root"), filepath.Join(dir, "bare")

		for _, d := range []string{root, bare} {
			if err := os.Mkdir(d, 0o755); err != nil {
				return err
			}
		}

		if err := buildRepo(root, map[string]string{"f": "1\n"}); err != nil {
			return err
		}

		for _, step := range []struct {
			dir  string
			args []string
		}{
			{root, []string{"switch", "-q", "-c", "feat"}},
			{bare, []string{"init", "-q", "--bare", "-b", "main"}},
			{root, []string{"remote", "add", "origin", "../bare"}},
		} {
			if err := gitRun(step.dir, step.args...); err != nil {
				return err
			}
		}

		return nil
	})

	root := filepath.Join(dir, "root")

	return pushFixture{root: root, bare: filepath.Join(dir, "bare"), repo: openRepo(t, root)}
}

// trackingFixture is a repository on feat with three commits and a remote
// named origin that points nowhere. Tests set refs/remotes/origin/feat and
// branch.feat.* themselves. commits run oldest first.
func trackingFixture(t *testing.T) (dir string, commits []string, r Repo) {
	t.Helper()

	dir = copyTemplate(t, &trackingTemplate, func(dir string) error {
		if err := buildRepo(dir, map[string]string{"f": "1\n"}); err != nil {
			return err
		}

		for _, args := range [][]string{
			{"switch", "-q", "-c", "feat"},
			{"commit", "-q", "--allow-empty", "-m", "two"},
			{"commit", "-q", "--allow-empty", "-m", "three"},
			{"remote", "add", "origin", "https://example.invalid/o/r.git"},
		} {
			if err := gitRun(dir, args...); err != nil {
				return err
			}
		}

		return nil
	})

	commits = strings.Fields(gitOut(t, dir, "rev-list", "--reverse", "feat"))

	return dir, commits, openRepo(t, dir)
}

func newBare(t *testing.T) string {
	t.Helper()

	bare := t.TempDir()
	gitOut(t, bare, "init", "-q", "--bare", "-b", "main")

	return bare
}

// clearSSHEnv makes core.sshCommand the ssh that runs: both variables outrank it.
func clearSSHEnv(t *testing.T) {
	t.Helper()

	for _, name := range []string{"GIT_SSH_COMMAND", "GIT_SSH"} {
		t.Setenv(name, "")
		os.Unsetenv(name)
	}
}

func writeScript(t *testing.T, body string) string {
	t.Helper()

	dir := t.TempDir()
	writeFile(t, dir, "script.sh", "#!/bin/sh\n"+body+"\n")

	path := filepath.Join(dir, "script.sh")
	if err := os.Chmod(path, 0o755); err != nil {
		t.Fatal(err)
	}

	return path
}

func refs(t *testing.T, dir string) string {
	t.Helper()

	return gitOut(t, dir, "for-each-ref", "--format=%(refname) %(objectname)")
}

func TestPushFirstSetsUpstream(t *testing.T) {
	t.Parallel()
	f := newPushFixture(t)
	ctx, logPath := wrapGit(t, "")

	res, err := Push(ctx, f.repo, "feat")
	if err != nil {
		t.Fatal(err)
	}

	want := PushResult{Remote: "origin", Branch: "feat", Dst: "refs/heads/feat", Created: true, SetUpstream: true}
	if res != want {
		t.Errorf("result = %+v, want %+v", res, want)
	}

	if got := gitOut(t, f.root, "config", "branch.feat.merge"); got != "refs/heads/feat" {
		t.Errorf("branch.feat.merge = %q", got)
	}

	if got, want := gitOut(t, f.bare, "rev-parse", "refs/heads/feat"), gitOut(t, f.root, "rev-parse", "feat"); got != want {
		t.Errorf("remote feat = %s, want %s", got, want)
	}

	// A push never forces.
	seen := false

	for line := range strings.SplitSeq(wrappedCalls(t, logPath), "\n") {
		fields := strings.Fields(line)
		if !slices.Contains(fields, "push") {
			continue
		}

		seen = true

		for _, a := range fields {
			if a == "-f" || a == "--mirror" || strings.HasPrefix(a, "+") || strings.HasPrefix(a, "--force") {
				t.Errorf("push argument %q in %q", a, line)
			}
		}
	}

	if !seen {
		t.Error("no push in the git log")
	}
}

func TestPushAgainFastForwards(t *testing.T) {
	t.Parallel()
	f := newPushFixture(t)
	ctx := context.Background()

	if _, err := Push(ctx, f.repo, "feat"); err != nil {
		t.Fatal(err)
	}

	writeFile(t, f.root, "f", "2\n")
	commitAll(t, f.root, "second")

	res, err := Push(ctx, f.repo, "feat")
	if err != nil {
		t.Fatal(err)
	}

	if res.Created || res.UpToDate || res.SetUpstream || res.Dst != "refs/heads/feat" {
		t.Errorf("result = %+v", res)
	}

	if got, want := gitOut(t, f.bare, "rev-parse", "refs/heads/feat"), gitOut(t, f.root, "rev-parse", "feat"); got != want {
		t.Errorf("remote feat = %s, want %s", got, want)
	}

	res, err = Push(ctx, f.repo, "feat")
	if err != nil {
		t.Fatal(err)
	}

	if !res.UpToDate || res.Created || res.SetUpstream {
		t.Errorf("result = %+v", res)
	}
}

func TestPushIgnoresUpstreamOfAnotherName(t *testing.T) {
	t.Parallel()
	f := newPushFixture(t)
	gitOut(t, f.root, "config", "branch.feat.remote", "origin")
	gitOut(t, f.root, "config", "branch.feat.merge", "refs/heads/other")

	res, err := Push(context.Background(), f.repo, "feat")
	if err != nil {
		t.Fatal(err)
	}

	if res.Dst != "refs/heads/feat" || res.SetUpstream || !res.Created {
		t.Errorf("result = %+v", res)
	}

	if got := gitOut(t, f.bare, "for-each-ref", "--format=%(refname)"); got != "refs/heads/feat" {
		t.Errorf("remote refs = %q", got)
	}

	if got := gitOut(t, f.root, "config", "branch.feat.merge"); got != "refs/heads/other" {
		t.Errorf("branch.feat.merge = %q, want it unchanged", got)
	}
}

func TestPushNeverLandsOnTheBaseBranch(t *testing.T) {
	t.Parallel()
	f := newPushFixture(t)
	ctx := context.Background()

	gitOut(t, f.root, "push", "-q", "origin", "feat:main")
	gitOut(t, f.root, "fetch", "-q", "origin")
	gitOut(t, f.root, "switch", "-q", "-c", "agent-fix", "origin/main")
	writeFile(t, f.root, "f", "2\n")
	commitAll(t, f.root, "fix")

	mainBefore := gitOut(t, f.bare, "rev-parse", "refs/heads/main")

	res, err := Push(ctx, f.repo, "agent-fix")
	if err != nil {
		t.Fatal(err)
	}

	if res.Dst != "refs/heads/agent-fix" || res.SetUpstream || !res.Created {
		t.Errorf("result = %+v", res)
	}

	if got := gitOut(t, f.bare, "rev-parse", "refs/heads/main"); got != mainBefore {
		t.Errorf("remote main = %s, want %s", got, mainBefore)
	}

	if got, want := gitOut(t, f.bare, "rev-parse", "refs/heads/agent-fix"), gitOut(t, f.root, "rev-parse", "agent-fix"); got != want {
		t.Errorf("remote agent-fix = %s, want %s", got, want)
	}

	if got := gitOut(t, f.root, "config", "branch.agent-fix.merge"); got != "refs/heads/main" {
		t.Errorf("branch.agent-fix.merge = %q, want refs/heads/main", got)
	}
}

func TestPushRefusesURLAsRemote(t *testing.T) {
	t.Parallel()

	root := newRepo(t)
	gitOut(t, root, "config", "branch.feat.remote", "https://user:secret@example.invalid/o/r.git") //nolint:gosec // G101: fixture URL, no real credential

	r := openRepo(t, root)
	ctx, logPath := wrapGit(t, "")

	_, pushErr := Push(ctx, r, "feat")
	_, remoteErr := PushRemote(ctx, r, "feat")

	for name, err := range map[string]error{"Push": pushErr, "PushRemote": remoteErr} {
		if !errors.Is(err, ErrNoRemote) {
			t.Errorf("%s: err = %v, want ErrNoRemote", name, err)

			continue
		}

		for _, leak := range []string{"secret", "example.invalid"} {
			if strings.Contains(err.Error(), leak) {
				t.Errorf("%s: error %q contains %q", name, err, leak)
			}
		}
	}

	log := wrappedCalls(t, logPath)
	for line := range strings.SplitSeq(log, "\n") {
		if slices.Contains(strings.Fields(line), "push") {
			t.Errorf("a push ran:\n%s", log)
		}
	}
}

func TestPushKeepsUpstreamOnAnotherRemote(t *testing.T) {
	t.Parallel()
	dir, _, r := trackingFixture(t)
	gitOut(t, dir, "config", "branch.feat.remote", "origin")
	gitOut(t, dir, "config", "branch.feat.merge", "refs/heads/feat")

	backup := newBare(t)
	gitOut(t, dir, "remote", "add", "backup", backup)
	gitOut(t, dir, "config", "remote.pushDefault", "backup")

	res, err := Push(context.Background(), r, "feat")
	if err != nil {
		t.Fatal(err)
	}

	if res.Remote != "backup" || res.SetUpstream || !res.Created || res.Dst != "refs/heads/feat" {
		t.Errorf("result = %+v", res)
	}

	if got := gitOut(t, dir, "config", "branch.feat.remote"); got != "origin" {
		t.Errorf("branch.feat.remote = %q, want origin", got)
	}

	if got := gitOut(t, dir, "config", "branch.feat.merge"); got != "refs/heads/feat" {
		t.Errorf("branch.feat.merge = %q", got)
	}

	if got, want := gitOut(t, backup, "rev-parse", "refs/heads/feat"), gitOut(t, dir, "rev-parse", "feat"); got != want {
		t.Errorf("backup feat = %s, want %s", got, want)
	}
}

func TestPushRejectedChangesNothing(t *testing.T) {
	t.Parallel()
	f := newPushFixture(t)

	gitOut(t, f.root, "remote", "set-url", "origin", f.bare) // an absolute URL, so a leak of it can show

	ctx := context.Background()

	gitOut(t, f.root, "push", "-q", "-u", "origin", "feat")

	other := t.TempDir()
	gitOut(t, other, "clone", "-q", "-b", "feat", f.bare, ".")
	writeFile(t, other, "g", "x\n")
	commitAll(t, other, "moved")
	gitOut(t, other, "push", "-q", "origin", "feat")

	writeFile(t, f.root, "f", "2\n")
	commitAll(t, f.root, "local")

	localRefs, bareRefs := refs(t, f.root), refs(t, f.bare)
	localCfg := gitOut(t, f.root, "config", "--local", "--list")

	_, err := Push(ctx, f.repo, "feat")
	if !errors.Is(err, ErrRejected) {
		t.Fatalf("err = %v, want ErrRejected", err)
	}

	if !strings.Contains(err.Error(), "(fetch first)") || strings.Contains(err.Error(), f.bare) {
		t.Errorf("err = %q, want the fetch-first summary and no remote path", err)
	}

	if got := refs(t, f.root); got != localRefs {
		t.Errorf("local refs changed:\n%s\nwas:\n%s", got, localRefs)
	}

	if got := refs(t, f.bare); got != bareRefs {
		t.Errorf("remote refs changed:\n%s\nwas:\n%s", got, bareRefs)
	}

	if got := gitOut(t, f.root, "config", "--local", "--list"); got != localCfg {
		t.Errorf("config changed:\n%s\nwas:\n%s", got, localCfg)
	}
}

// sshRemote points origin at an unresolvable ssh URL and core.sshCommand at
// script, so no real ssh runs.
func sshRemote(t *testing.T, dir, script string) {
	t.Helper()
	clearSSHEnv(t)
	gitOut(t, dir, "remote", "set-url", "origin", "ssh://example.invalid/r.git")
	gitOut(t, dir, "config", "core.sshCommand", script)
}

// The remote hangs once it has recorded its pid; it marks its end only if the
// cancel failed to kill it.
func TestPushHungRemoteIsKilled(t *testing.T) {
	dir, _, r := trackingFixture(t)
	mark := filepath.Join(t.TempDir(), "remote")
	sshRemote(t, dir, writeScript(t, fmt.Sprintf(`echo $$ > %[1]q.tmp && mv %[1]q.tmp %[1]q.pid
sleep 30
: > %[1]q.finished`, mark)))

	_, err := Push(cancelWhenWritten(t, mark+".pid"), r, "feat")

	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want canceled", err)
	}

	raw, err := os.ReadFile(mark + ".pid")
	if err != nil {
		t.Fatal(err)
	}

	pid, err := strconv.Atoi(strings.TrimSpace(string(raw)))
	if err != nil {
		t.Fatal(err)
	}

	for alive(pid) {
		time.Sleep(10 * time.Millisecond)
	}

	if _, err := os.Stat(mark + ".finished"); err == nil {
		t.Error("the hung remote ran to its end")
	}
}

const ttyChildEnv = "AIM_TTY_CHILD"

// TestNetCallCannotOpenTTY runs itself under a pty, so the child has a
// controlling terminal: a plain call must reach /dev/tty (the control that
// proves the check can fail) and a network call must not.
func TestNetCallCannotOpenTTY(t *testing.T) {
	if os.Getenv(ttyChildEnv) != "" {
		ttyChild(t)

		return
	}

	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}

	cmd := exec.Command(exe, "-test.v", "-test.run=^TestNetCallCannotOpenTTY$")

	cmd.Env = append(os.Environ(), ttyChildEnv+"=1")

	ptmx, err := pty.Start(cmd)
	if err != nil {
		t.Skipf("no pty: %v", err)
	}
	defer ptmx.Close()

	var childOut bytes.Buffer

	drained := make(chan struct{})

	go func() {
		defer close(drained)

		_, _ = io.Copy(&childOut, ptmx)
	}()

	timer := time.AfterFunc(30*time.Second, func() { _ = cmd.Process.Kill() })
	defer timer.Stop()

	werr := cmd.Wait()

	<-drained

	if werr != nil || !strings.Contains(childOut.String(), "--- PASS: TestNetCallCannotOpenTTY") {
		t.Fatalf("child: %v\n%s", werr, childOut.String())
	}
}

// ttyChild runs with a controlling terminal. The fake ssh reports on stderr,
// which git passes through as the first line of the error.
func ttyChild(t *testing.T) {
	t.Helper()
	clearSSHEnv(t)

	script := writeScript(t, `if (exec 3</dev/tty) 2>/dev/null; then echo tty; else echo notty; fi >&2
exit 1`)

	for _, tt := range []struct {
		net  bool
		want string
	}{{false, ": tty"}, {true, ": notty"}} {
		_, err := run(context.Background(), call{
			dir:  t.TempDir(),
			args: []string{"-c", "core.sshCommand=" + script, "ls-remote", "ssh://example.invalid/r.git"},
			net:  tt.net,
		})
		if err == nil || !strings.HasSuffix(err.Error(), tt.want) {
			t.Errorf("net=%v: err = %v, want suffix %q", tt.net, err, tt.want)
		}
	}
}
