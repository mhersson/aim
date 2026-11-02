package main

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/mhersson/aim/internal/term"
)

func TestSettingsForSock(t *testing.T) {
	t.Parallel()

	tests := []struct {
		sock   string
		want   string
		wantOK bool
	}{
		{"/r/123.sock", "/r/123-claude.json", true},
		{"/r/x.sock", "/r/x-claude.json", true},
		{"/r/123.socket", "", false},
		{"", "", false},
	}
	for _, tt := range tests {
		got, ok := settingsForSock(tt.sock)
		if got != tt.want || ok != tt.wantOK {
			t.Errorf("settingsForSock(%q) = %q, %v; want %q, %v", tt.sock, got, ok, tt.want, tt.wantOK)
		}
	}
}

// shimFS is a set of real directories for the shim tests: a file standing for
// the running binary, wrapper dirs holding symlinks to it and a fake real claude.
type shimFS struct {
	self      os.FileInfo
	selfPath  string
	w, w2, f  string
	d         string
	sockPath  string
	settings  string
	fakeClaud string
}

func newShimFS(t *testing.T) shimFS {
	t.Helper()

	root := t.TempDir()
	fs := shimFS{selfPath: filepath.Join(root, "aim")}

	if err := os.WriteFile(fs.selfPath, []byte("x"), 0o755); err != nil {
		t.Fatal(err)
	}

	var err error
	if fs.self, err = os.Stat(fs.selfPath); err != nil {
		t.Fatal(err)
	}

	for name, dir := range map[string]*string{"w": &fs.w, "w2": &fs.w2, "f": &fs.f, "d": &fs.d} {
		*dir = filepath.Join(root, name)
		if err := os.MkdirAll(*dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}

	for _, w := range []string{fs.w, fs.w2} {
		if err := os.Symlink(fs.selfPath, filepath.Join(w, "claude")); err != nil {
			t.Fatal(err)
		}
	}

	if err := os.Mkdir(filepath.Join(fs.d, "claude"), 0o755); err != nil {
		t.Fatal(err)
	}

	fs.fakeClaud = filepath.Join(fs.f, "claude")
	if err := os.WriteFile(fs.fakeClaud, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}

	fs.sockPath = filepath.Join(root, "123.sock")
	fs.settings = filepath.Join(root, "123-claude.json")

	if err := os.WriteFile(fs.settings, []byte("{}"), 0o644); err != nil {
		t.Fatal(err)
	}

	return fs
}

func TestPlanShim(t *testing.T) {
	fs := newShimFS(t)
	join := func(parts ...string) string { return strings.Join(parts, string(os.PathListSeparator)) }
	base := func(path string, extra ...string) []string {
		return append([]string{"A=1", "PATH=" + path, "AIM_SOCK=" + fs.sockPath, "Z=last"}, extra...)
	}

	cwd := t.TempDir()
	if err := os.WriteFile(filepath.Join(cwd, "claude"), []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name     string
		args     []string
		env      []string
		leads    bool
		chdir    string
		wantPath string
		wantArgv []string
		wantPATH string
		wantOK   bool
	}{
		{
			name: "adds", args: []string{"a"}, env: base(join(fs.w, fs.f)), leads: true,
			wantPath: fs.fakeClaud, wantArgv: []string{"claude", "--settings", fs.settings, "a"}, wantPATH: fs.f, wantOK: true,
		},
		{
			name: "not the leader", args: []string{"a"}, env: base(join(fs.w, fs.f)),
			wantPath: fs.fakeClaud, wantArgv: []string{"claude", "a"}, wantPATH: fs.f, wantOK: true,
		},
		{
			name: "CLAUDECODE set", args: []string{"a"}, env: base(join(fs.w, fs.f), "CLAUDECODE=1"), leads: true,
			wantPath: fs.fakeClaud, wantArgv: []string{"claude", "a"}, wantPATH: fs.f, wantOK: true,
		},
		{
			name: "no AIM_SOCK", args: []string{"a"}, env: []string{"A=1", "PATH=" + join(fs.w, fs.f), "Z=last"}, leads: true,
			wantPath: fs.fakeClaud, wantArgv: []string{"claude", "a"}, wantPATH: fs.f, wantOK: true,
		},
		{
			name: "settings gone", args: []string{"a"}, env: []string{"A=1", "PATH=" + join(fs.w, fs.f), "AIM_SOCK=" + filepath.Join(filepath.Dir(fs.sockPath), "999.sock"), "Z=last"}, leads: true,
			wantPath: fs.fakeClaud, wantArgv: []string{"claude", "a"}, wantPATH: fs.f, wantOK: true,
		},
		{
			name: "own settings", args: []string{"--settings=U", "a"}, env: base(join(fs.w, fs.f)), leads: true,
			wantPath: fs.fakeClaud, wantArgv: []string{"claude", "--settings=U", "a"}, wantPATH: fs.f, wantOK: true,
		},
		{
			name: "nested AIM", args: []string{"a"}, env: base(join(fs.w, fs.w2, fs.f)), leads: true,
			wantPath: fs.fakeClaud, wantArgv: []string{"claude", "--settings", fs.settings, "a"}, wantPATH: fs.f, wantOK: true,
		},
		{
			name: "empty entry is the cwd", args: []string{"a"}, env: base(join(fs.w, "", fs.f)), leads: true, chdir: cwd,
			wantPath: "claude", wantArgv: []string{"claude", "--settings", fs.settings, "a"}, wantPATH: join("", fs.f), wantOK: true,
		},
		{name: "none", args: []string{"a"}, env: base(join(fs.w, "/nonexistent")), leads: true},
		{
			name: "a directory named claude", args: []string{"a"}, env: base(join(fs.w, fs.d, fs.f)), leads: true,
			wantPath: fs.fakeClaud, wantArgv: []string{"claude", "--settings", fs.settings, "a"}, wantPATH: join(fs.d, fs.f), wantOK: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.chdir != "" {
				t.Chdir(tt.chdir)
			}

			plan, ok := planShim(tt.args, tt.env, fs.self, func() bool { return tt.leads })
			if ok != tt.wantOK {
				t.Fatalf("ok = %v, want %v (plan %+v)", ok, tt.wantOK, plan)
			}

			if !ok {
				return
			}

			if plan.path != tt.wantPath {
				t.Errorf("path = %q, want %q", plan.path, tt.wantPath)
			}

			if !slices.Equal(plan.argv, tt.wantArgv) {
				t.Errorf("argv = %q, want %q", plan.argv, tt.wantArgv)
			}

			var wantEnv []string

			for _, e := range tt.env {
				if strings.HasPrefix(e, "PATH=") {
					e = "PATH=" + tt.wantPATH
				}

				wantEnv = append(wantEnv, e)
			}

			if !slices.Equal(plan.env, wantEnv) {
				t.Errorf("env = %q, want %q", plan.env, wantEnv)
			}
		})
	}
}

func TestPlanShimWithoutSelf(t *testing.T) {
	fs := newShimFS(t)
	shellDir := filepath.Join(filepath.Dir(fs.selfPath), "shell")

	if err := os.MkdirAll(filepath.Join(shellDir, "bin"), 0o755); err != nil {
		t.Fatal(err)
	}

	pathEnv := filepath.Join(shellDir, "bin") + string(os.PathListSeparator) + fs.f
	env := []string{"PATH=" + pathEnv, "AIM_SHELL_DIR=" + shellDir}

	plan, ok := planShim(nil, env, nil, func() bool { return false })
	if !ok || plan.path != fs.fakeClaud {
		t.Errorf("with AIM_SHELL_DIR: plan %+v, %v; want %s", plan, ok, fs.fakeClaud)
	}

	if _, ok := planShim(nil, []string{"PATH=" + pathEnv}, nil, func() bool { return false }); ok {
		t.Error("without self and AIM_SHELL_DIR the plan must fail rather than risk exec'ing itself")
	}
}

func TestShimNotFound(t *testing.T) {
	fs := newShimFS(t)
	t.Setenv("PATH", fs.w)
	t.Setenv("AIM_SHELL_DIR", "")

	var buf bytes.Buffer

	// The test binary is not the symlink target, so the wrapper dir holds a
	// claude that is another file: point it at the binary itself.
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}

	_ = os.Remove(filepath.Join(fs.w, "claude"))

	if err := os.Symlink(exe, filepath.Join(fs.w, "claude")); err != nil {
		t.Fatal(err)
	}

	if got := runShim([]string{"a"}, &buf); got != 127 {
		t.Errorf("exit = %d, want 127", got)
	}

	if buf.String() != "claude: command not found\n" {
		t.Errorf("stderr = %q", buf.String())
	}
}

// shimRig is a bash on a pty with the wrapper first on PATH and a fake real claude.
type shimRig struct {
	p       *term.Process
	out     string
	fakeDir string
	mu      sync.Mutex
	output  bytes.Buffer
	sock    string
	set     string
}

func newShimRig(t *testing.T, fake string, extraEnv ...string) *shimRig {
	t.Helper()

	bash, err := exec.LookPath("bash")
	if err != nil {
		t.Skip("bash not installed")
	}

	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}

	root := t.TempDir()
	wrapperDir := filepath.Join(root, "wrap")
	fakeDir := filepath.Join(root, "fake")
	out := filepath.Join(root, "out")

	for _, d := range []string{wrapperDir, fakeDir, out} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}

	if err := os.Symlink(exe, filepath.Join(wrapperDir, "claude")); err != nil {
		t.Fatal(err)
	}

	// A concurrent fork can inherit the write fd and make exec fail with ETXTBSY.
	syscall.ForkLock.RLock()

	err = os.WriteFile(filepath.Join(fakeDir, "claude"), []byte(fake), 0o755)

	syscall.ForkLock.RUnlock()

	if err != nil {
		t.Fatal(err)
	}

	r := &shimRig{out: out, fakeDir: fakeDir, sock: filepath.Join(root, "42.sock"), set: filepath.Join(root, "42-claude.json")}
	if err := os.WriteFile(r.set, []byte("{}"), 0o644); err != nil {
		t.Fatal(err)
	}

	env := []string{
		"PATH=" + wrapperDir + ":" + fakeDir + ":/usr/bin:/bin",
		"AIM_SOCK=" + r.sock, "OUT_DIR=" + out,
		"HOME=" + root, "TERM=dumb", "LANG=C",
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	t.Cleanup(cancel)

	p, err := term.Start(ctx, term.Command{
		Argv: []string{bash, "--noprofile", "--norc", "-i"},
		Dir:  root,
		Env:  append(env, extraEnv...),
	}, 120, 30)
	if err != nil {
		t.Fatalf("Start: %v", err)
	}

	t.Cleanup(func() { _ = p.Close() })

	r.p = p

	go func() {
		buf := make([]byte, 4096)

		for {
			n, err := p.Read(buf)

			r.mu.Lock()
			r.output.Write(buf[:n])
			r.mu.Unlock()

			if err != nil {
				return
			}
		}
	}()

	return r
}

func (r *shimRig) typeLine(t *testing.T, line string) {
	t.Helper()

	if _, err := r.p.Write([]byte(line + "\n")); err != nil {
		t.Fatal(err)
	}
}

func (r *shimRig) screen() string {
	r.mu.Lock()
	defer r.mu.Unlock()

	return r.output.String()
}

func (r *shimRig) waitFile(t *testing.T, name string) []byte {
	t.Helper()

	deadline := time.Now().Add(10 * time.Second)

	for {
		b, err := os.ReadFile(filepath.Join(r.out, name))
		if err == nil {
			return b
		}

		if time.Now().After(deadline) {
			t.Fatalf("%s never appeared; screen:\n%s", name, r.screen())
		}

		time.Sleep(20 * time.Millisecond)
	}
}

func (r *shimRig) waitScreen(t *testing.T, want string) {
	t.Helper()

	deadline := time.Now().Add(10 * time.Second)

	for !strings.Contains(r.screen(), want) {
		if time.Now().After(deadline) {
			t.Fatalf("screen never held %q:\n%s", want, r.screen())
		}

		time.Sleep(20 * time.Millisecond)
	}
}

const recordingFake = "#!/bin/sh\nprintf '%s\\0' \"$@\" > \"$OUT_DIR/args.tmp\"\nmv \"$OUT_DIR/args.tmp\" \"$OUT_DIR/args\"\nexit ${FAKE_EXIT:-0}\n"

func splitArgs(raw []byte) []string {
	s := strings.TrimSuffix(string(raw), "\x00")
	if s == "" {
		return nil
	}

	return strings.Split(s, "\x00")
}

func TestShimOnAPty(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("needs a pty")
	}

	tests := []struct {
		name  string
		typed string
		want  func(settings string) []string
		out   string
	}{
		{"leader", "claude a 'b c'", func(s string) []string { return []string{"--settings", s, "a", "b c"} }, ""},
		{"child of the leader", "sh -c 'claude -p x; :'", func(string) []string { return []string{"-p", "x"} }, ""},
		{"not the leader of a pipeline", "echo hi | claude -p", func(string) []string { return []string{"-p"} }, ""},
		{"inside claude code", "CLAUDECODE=1 claude y", func(string) []string { return []string{"y"} }, ""},
		{"own settings", "claude --settings=/u.json z", func(string) []string { return []string{"--settings=/u.json", "z"} }, ""},
		{"exit status", "FAKE_EXIT=3 claude q; echo rc=$?", func(s string) []string { return []string{"--settings", s, "q"} }, "rc=3"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := newShimRig(t, recordingFake)
			r.typeLine(t, tt.typed)

			got := splitArgs(r.waitFile(t, "args"))
			if want := tt.want(r.set); !slices.Equal(got, want) {
				t.Errorf("args = %q, want %q", got, want)
			}

			if tt.out != "" {
				r.waitScreen(t, tt.out)
			}
		})
	}
}

func TestShimKeepsTheProbeName(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("the foreground name is tested on Linux")
	}

	r := newShimRig(t, "#!/bin/sh\nsleep 30\n")

	// The real claude is a symlink to a versioned file, as the installer lays it out.
	versions := filepath.Join(r.fakeDir, "versions")
	if err := os.MkdirAll(versions, 0o755); err != nil {
		t.Fatal(err)
	}

	if err := os.Rename(filepath.Join(r.fakeDir, "claude"), filepath.Join(versions, "2.1.316")); err != nil {
		t.Fatal(err)
	}

	if err := os.Symlink(filepath.Join("versions", "2.1.316"), filepath.Join(r.fakeDir, "claude")); err != nil {
		t.Fatal(err)
	}

	r.typeLine(t, "claude")

	deadline := time.Now().Add(10 * time.Second)

	for {
		got, err := r.p.Foreground()
		if err == nil && got == "claude" {
			return
		}

		if time.Now().After(deadline) {
			t.Fatalf("Foreground = %q, %v; want claude", got, err)
		}

		time.Sleep(20 * time.Millisecond)
	}
}

func TestShimLeavesTheTerminalAlone(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("/proc is Linux only")
	}

	const probe = "#!/bin/sh\n{ grep -E '^(SigBlk|SigIgn)' /proc/$$/status; ls /proc/$$/fd; } > \"$OUT_DIR/$STATE_NAME.tmp\"\nmv \"$OUT_DIR/$STATE_NAME.tmp\" \"$OUT_DIR/$STATE_NAME\"\n"

	r := newShimRig(t, probe)

	fakeAbs := filepath.Join(r.fakeDir, "claude")

	r.typeLine(t, "STATE_NAME=direct "+fakeAbs)
	direct := r.waitFile(t, "direct")

	r.typeLine(t, "STATE_NAME=wrapped claude")
	wrapped := r.waitFile(t, "wrapped")

	if !bytes.Equal(direct, wrapped) {
		t.Errorf("state through the wrapper differs from a direct run:\nwrapped:\n%s\ndirect:\n%s", wrapped, direct)
	}
}
