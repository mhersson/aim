# Gotchas

Platform and tooling quirks that contributors trip over in this repo, each with
what to do about it. Skim it before your first commit each session.

## Glyphs, em-dashes, wall-clock bounds and the scan

- AI file tools drop or alter raw private-use characters, so write Nerd Font
  glyphs in Go source as `\u` or `\U` escapes.
- `make scan` is the scan of record, and part of `make check-fast` and
  `make check`. It fails on private-use glyphs in Go, TOML and Markdown files
  and on em-dashes in Go, Markdown and TOML files (`docs/superpowers` aside in
  both), untracked files included. It first proves grep can match its
  patterns, so a pass means something.
- `make check-fast` skips the Go gates when every changed file is non-code:
  Markdown at the repository root, anything under `docs/` or `.github/`,
  `config.example.toml`, `LICENSE`, `.goreleaser.yaml` and `.gitignore`. It then
  runs only `make scan` and the doc tests. Anything else is code, a Markdown
  file inside a package (a golden) included, and `TEST_PKGS` set forces the
  full check. `scripts/code-changed.sh` decides, from the same file list as
  `scripts/changed-packages.sh`.
- The scan's `scripts/scan-wall-clock.sh` fails on `time.Since`, `time.Until`
  and `time.Now().Sub` in a `_test.go` file without the `bench` build tag. A
  test waits for the event or runs on an injected clock, as the poller and
  the hook socket server allow through their unexported `now` and `after`. A
  hang guard (`time.After`, a deadline from `time.Now().Add`) passes, and so
  does a line that measures without asserting when it ends with
  `// wall-clock: <why>`, as the bench kit's timings do.
- Never scan with `grep -P`. BSD grep on macOS has no `-P` and exits 2, which a
  script that only tests grep's status reads as "no match", so the scan passes.
  Use `LC_ALL=C` byte patterns, as `make scan` does.

## Shell tools and make recipes

- macOS has BSD grep, sed, xargs, mktemp and awk, and GNU make 3.81. Write
  recipes and scripts that work with both sets of tools: `sed -i` takes a
  suffix argument on BSD (`sed -i ''`) and fails with one on GNU, so write to
  a temp file and move it instead; give `mktemp` an explicit `XXXXXX`
  template; use no make feature newer than 3.81. CI's `macos` job runs
  `make scan` there.
- BSD make runs each recipe line under `sh -e`, so a recipe must stay correct
  there: end a command that is allowed to fail with `|| :`.
- GNU xargs runs its command once on empty input and BSD xargs does not. Put
  `/dev/null` among grep's arguments so an empty list never leaves grep
  reading stdin.
- Set `LC_ALL=C` for byte patterns in grep and for sort, uniq and awk, so a
  UTF-8 locale cannot change what matches or the order.
- macOS's `/bin/bash` is 3.2: no associative arrays, `mapfile` or `${var,,}`.
  Write POSIX sh, or bash that 3.2 runs.

## macOS filesystems

- APFS refuses file names that are not valid UTF-8 (`EILSEQ`). A test that
  needs such a name checks with `fsAcceptsName` (internal/git) and skips when
  the filesystem refuses it.
- APFS is case-insensitive by default, so two paths that differ only in case
  are one file on a Mac. CI's `static-checks` job fails on such paths, also
  when they differ only after dropping their file extensions.
- Go's `File.Sync` on macOS is `F_FULLFSYNC`: the drive flushes all its
  buffered data, every process's, so the call costs more the busier the
  machine's disk is. Sync only what must survive a crash, such as `state.json`
  (`store.WriteAtomic`); write a per-run file, like the hook settings file,
  with `store.WriteAtomicNoSync`.
- `t.TempDir()` on macOS is under `/var/folders`, and `/var` is a symlink to
  `/private/var`; git reports the resolved path. Run a temp dir through
  `filepath.EvalSymlinks` before comparing it with git output, as `newRepo`
  does.

## Unix sockets

- A Unix socket path holds at most 103 bytes on macOS and 107 on Linux.
  macOS's `$TMPDIR`, and so `t.TempDir()`, is deep enough to push a socket
  path past that. Tests put sockets in a dir from `os.MkdirTemp("/tmp", ...)`
  (the `shortDir` helpers in `cmd/aim` and `internal/harness`) and
  remove it in `t.Cleanup`.
- The hook socket is `<state dir>/run/<pid>.sock`, and on macOS the state dir
  is under `$HOME`. A test or rig that starts the hook server on macOS sets
  `HOME` to a short dir, such as one from `mktemp -d /tmp/mr.XXXX`. Over the
  limit, agents run without hooks and the notice reads
  `agent hooks are off: socket path too long (N bytes, at most M)`.

## State, config and cache dirs

- The state dir is `$XDG_STATE_HOME/aim` on Linux but always
  `$HOME/Library/Application Support/aim` on macOS, which ignores
  `XDG_STATE_HOME`. Isolate a test or rig on macOS through `HOME`.
- The index cache is under `os.UserCacheDir()`: `$XDG_CACHE_HOME`, else
  `~/.cache`, on Linux, but `$HOME/Library/Caches` on macOS whatever
  `XDG_CACHE_HOME` says.
- The config file honours `XDG_CONFIG_HOME` on both platforms, else
  `~/.config/aim/config.toml`; `--config` overrides it.

## File watching

- On macOS `fsnotify` uses kqueue, which holds one file descriptor per watched
  file, so a large repository can reach the open-file limit. The watch then
  fails with the notice `watching <project> failed: <reason>`. Ignored
  directories get no watches, so ignoring build output, or a higher
  `ulimit -n`, is the way out.
- kqueue sends events inotify does not: a Write on a watched directory when
  its entries change, and Chmod-only events. The watcher keeps them from
  counting as changes; a change to its event filter needs its tests to pass on
  macOS as well as Linux.
- `fsnotify` is a copy in `internal/git/fsnotify` because v1.10.1's kqueue
  backend can close a watch descriptor twice while a watcher closes. The
  second close shuts whatever took the number, such as another git call's
  pipe, which then holds a stranger's output. Keep the copy until a release
  fixes https://github.com/fsnotify/fsnotify/issues/788, then swap it whole
  for the module. Its kqueue tests run only on macOS.

## Platform-specific code

- Put Linux-only code in a `_linux.go` file and macOS-only code in
  `_darwin.go`; the file name is the build constraint. A fallback for the rest
  needs an explicit constraint, such as `//go:build !linux && !darwin` in
  `internal/term/fg_other.go`.
- `make check` builds for darwin (`build-darwin`), and CI's `macos` job builds
  and tests natively. Nothing in the Makefile cross-compiles for Linux, so on a
  Mac run `GOOS=linux go vet ./...` after touching platform files.

## Running the TUI

- Run the app for a manual check or a screenshot only inside a private tmux
  server, `tmux -L <name> -f /dev/null`, started from an environment without
  `CLAUDE*`, `TMUX`, `TMUX_PANE` and `AIM_*`, so the run starts as from a
  fresh terminal. `-L` keeps it off your own tmux server and `-f /dev/null`
  skips your tmux config.
- Kill only that server (`tmux -L <name> kill-server`) and processes you
  started, by PID. Never kill by name: `claude` and `aim` processes may
  be someone's live sessions.
- Give the run a short `HOME`, which on macOS holds the state dir and the hook
  socket. Put a `.zshrc` in it: zsh can start its new-user setup in a `HOME`
  without one.
- A pane's kind follows its foreground process's name: its comm on Linux, and
  on macOS its `argv[0]`, or the script in `argv[1]` when `argv[0]` is `node`
  or `bun`. On macOS a shebang script's `argv[0]` is its interpreter
  (`/bin/sh`), so a fake `claude` that must make its pane an agent is a
  compiled binary named `claude`.
- Agents start through `$SHELL -l -i -c`, and a login profile may put other
  dirs ahead of a fake's on `PATH`. Point `SHELL` at a wrapper that puts the
  fake's dir first, as `fakeClaude` in `cmd/aim/restore_test.go` does.

## Tests

- In internal/git, `TestMain` sets `GIT_CONFIG_NOSYSTEM`, a fixed identity and
  `GIT_CONFIG_GLOBAL` to a config file of its own once for the test binary, so
  its tests need no `t.Setenv` and can call `t.Parallel()`. Packages without
  such a `TestMain`, like `cmd/aim`, isolate git per test with `isolateGit`,
  which uses `t.Setenv`. A helper that builds its own `cmd.Env` keeps that
  `GIT_CONFIG_GLOBAL`. internal/ui's `TestMain` sets none of it: it puts a
  failing `git` first on `PATH`, since no test there starts git.
- `commit`, `merge`, `fetch` and a push's `receive-pack` start
  `git maintenance run --auto`, which detaches by default
  (`maintenance.autoDetach`) and removes `.git/objects/maintenance.lock` after
  the command has returned, racing a copy or removal of the repository. The
  tests' global config sets `maintenance.auto = false`; `GIT_CONFIG_COUNT`
  would not reach every git, since internal/git's `cleanEnv` and git's local
  transport drop it.
- To see or change git calls in an internal/git test, use `wrapGit`: it
  returns a context that routes git through a logging wrapper and leaves
  `PATH` alone, so the test can run in parallel. `fakeGit` puts the wrapper on
  `PATH` with `t.Setenv`, which rules out `t.Parallel()`. Read the log with
  `wrappedCalls`, which fails when it is empty, so an assertion that a call
  never ran cannot pass because nothing was wrapped.
- `t.Setenv` and `t.Chdir` panic in a parallel test. That is why tests that
  use them, and tests that count or scan the process's goroutines or open
  descriptors, skip `t.Parallel()`.
- Writing an executable and then running it while parallel tests fork can
  fail with `ETXTBSY`: a concurrent fork inherits the still-open write
  descriptor (go.dev/issue/22315). Hold `syscall.ForkLock.RLock()` around the
  write, as `writeGitWrapper` does.
- Chroma gives each lexer rule a 250 ms wall-clock match timeout and skips a
  rule that times out. Under `-race` on a loaded runner a highlighted span can
  then come out in another colour with no error. A test that compares
  highlighted colours (the drawer's code spans, the welcome card's sample)
  skips `t.Parallel()`; one that compares only text, counts or widths can run
  in parallel.
- `kill(2)` still finds a killed process, and its process group, until the
  process is reaped, and that can come after `Close` has returned: init reaps
  orphans, and on macOS an exec leaves the old proc behind as a shadow, with
  the same pid and group, until it finishes exiting. A test that checks that a
  pane's processes are gone after `Close` waits for them to go, as the `gone`
  checks in internal/ui do, rather than checking at once.
- A flaky test, or one that fails only on macOS, has a cause to find. Never
  give it a longer timeout, skip it or re-run it until it passes.
- The nightly workflow runs the race suite at `-count=2`, so every test must
  pass when run twice in one test binary: leave nothing behind in package
  variables, files or the environment.
- `-race` needs cgo on Linux (`go: -race requires cgo`), and `CGO_ENABLED` may
  be off in your environment. `make test-race` sets `CGO_ENABLED=1`; outside
  it, run `CGO_ENABLED=1 go test -race`. Expect a race run to take longer than
  a plain one; the Go docs put the slowdown at 2-20x.
- `go test` caches passing results and prints `(cached)`. Pass `-count=1` when
  timing a package or proving a change: a cached `ok` is an earlier run's
  result.
- Tests behind the `bench` and `live` build tags are vetted by `make vet` but
  never run by `make test`. Run them with `go test -tags bench` or
  `-tags live` when you change them.
- Regenerate golden files under `testdata/` with `-update` only for the change
  you intend, and read the golden diff before committing it: an unexpected
  change there is a bug.

## Shell integration

- The shell tests (`internal/term/shellinit_*_test.go`) run real shells on a
  pty under a temporary `HOME` with every `XDG_*` dir under it and `ZDOTDIR`
  and `ENV` unset, so no real startup file is ever read or written. The rig
  drops any `PATH` dir that holds a `claude`, and the integration dir sits
  under a path with a space. They use `t.Setenv`, so they are serial, and a
  missing shell skips them.
- The native installer's `.zshrc` line puts `~/.local/bin` first on PATH, and
  a mise-style `precmd` hook puts its shims first on every prompt. Both land
  ahead of AIM's `bin`, which is why zsh also gets a `claude` function and the
  PATH entry is not enough alone.
- An `/etc/zshenv` that sets `ZDOTDIR` defeats the zsh route: zsh reads the
  user's files from there instead of AIM's `.zshenv`.
- zsh runs its first-run setup when none of `.zshenv`, `.zprofile`, `.zshrc`
  and `.zlogin` exists, and AIM's `ZDOTDIR` would hide that from it. So
  `Integration.Zsh` is false then and zsh gets the wrapper on PATH only.
- macOS `path_helper` (run from `/etc/zprofile`) moves inherited PATH entries
  after the system ones, so the entry AIM put first does not stay first.
- The `claude` function is written `function claude {`, not `claude() {`: with
  an alias named `claude`, the alias expands inside the second form's
  definition.
- Debian and Ubuntu `/etc/profile` overwrite PATH for a login bash, which is
  why bash gets a `claude` function and AIM's `init.bash` puts `bin` first
  again after the login files.
- A `claude` on a pty nested in the pane (an nvim terminal, tmux, zellij,
  screen, `script`) gets no settings: the pane's terminal, named by `AIM_TTY`,
  is not led by it. `/dev/tty` is a node of its own, so the wrapper compares
  foreground groups, not device numbers.
- Apple's `/bin/bash` 3.2 ignores `ENV`, so it gets the wrapper on PATH only
  (plain `-l`, plain tail).
- `bash --posix` turns on `inherit_errexit`, sets `HISTFILE=~/.sh_history` and
  `MAILCHECK=600`, and `set +o posix` does not undo them. `init.bash` restores
  bash's defaults unless the value was exported.
- `claude() {` is a syntax error while an alias `claude` exists, so the
  function is written `function claude {` in bash too.
- A hashed `claude` outlives its file: the function drops the hash entry
  before it falls back to `command claude`.
- `fish_add_path` puts its dir in `fish_user_paths`, which fish keeps ahead of
  PATH, so it lands before AIM's entry (and `--move` moves it first again).
- A fish `alias` is a function that calls `command claude`, which only the PATH
  entry catches.
- `-C` runs after `conf.d` and `config.fish`. A `vendor_conf.d` route through
  `XDG_DATA_DIRS` would run after the user's `conf.d` had seen AIM's value.
- A fish variable whose name ends in `PATH` is split on colons, so AIM's is
  `AIM_SHELL_DIR`.
- Ubuntu's `/etc/zsh/zshrc` runs `compinit`, which can prompt and eat typed
  input; the zsh tests set `skip_global_compinit=1`.

## Git and worktrees

- Tests run with their package dir as the working directory, so in a checkout
  they run inside a repository, and in a `git archive` copy, which has no
  `.git`, they do not. A test can pass in one and fail in the other. A test
  that must not see a repository calls `t.Chdir(t.TempDir())`, and
  repo-dependent behaviour is checked in a real checkout or after `git init`
  in the copy.
- All worktrees of a repository share one stash. Never run `git stash` in a
  worktree you share; commit work in progress instead.
- git removes its lock files (a ref's `.lock`, `packed-refs.lock`) when SIGTERM
  stops it, but SIGKILL leaves them, and the next git in that repository fails
  with `Unable to create '<path>.lock': File exists`. So a cancelled call that
  may write gets SIGTERM before the kill (`stopProcessGroup`); a test of that
  checks the git dir for `*.lock` files, as `TestCancelledRefWriteLeavesNoLock`
  does.

## CI

- A pull request that changes only `README.md`, `AGENTS.md`,
  `THIRD_PARTY.md`, `LICENSE` or files under `docs/` skips the Go jobs, and
  `static-checks` still runs the case check and `make scan`. Any other file,
  or a `.go` file anywhere, brings the Go jobs back. A push to `main` that
  changes only those files runs no CI at all.
- CI pins golangci-lint to v2.15.0. Use the same version locally: another one
  can format or lint differently and fail `fmt-check` or `lint` in CI only.
- `release.yaml` calls `ci.yaml`, and inside a called workflow the event is the
  caller's: a tag push arrives as `push`. A gate job whose `if` excludes `push`
  needs `|| github.ref_type == 'tag'`, or a tag releases without it.
- The `github` context of a called workflow is the caller's, so `release.yaml`
  must not take the concurrency group `${{ github.workflow }}-${{ github.ref }}`.
  That is `ci.yaml`'s group too, and the two runs would block each other.
- git's version sort ranks `v1.0.0-rc.1` above `v1.0.0`. So GoReleaser, left to
  pick the tag on a commit that has both, takes the candidate. The release step
  passes the pushed tag and the previous final tag for that reason, and
  `scripts/release-notes.sh` compares versions by number.

## Go version upgrades

- Lint runs `modernize`, so new code takes the modern forms at commit time;
  `golangci-lint run --fix` applies them.
- A new Go release can bring new `go fix` fixers. After raising the Go version
  in `go.mod`, run `go fix ./...` and `go fix -tags bench,live ./...`, then
  `make fmt` and `make check-fast TEST_PKGS=./...`, fix by hand the lint the
  rewrite trips (helpers it leaves unused, blank lines), and put it up as its
  own pull request, apart from feature work. The fsnotify copy must stay
  untouched.
