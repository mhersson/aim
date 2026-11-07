# AIM

AIM (Agents In-flight Multiplexer) is a Go TUI that hosts coding agents (Claude
Code in v1) in live terminal panes, with live diffs, PR review, CI status, push,
open PR and merge beside them. Terminal emulation comes from `tuios.dev/vt`;
everything above it is ours.

## Sources of truth

- `docs/architecture.md`: how the running app fits together, why the
  non-obvious designs are so, and the invariants a change must keep. Read the
  sections your change touches before you change it.
- `docs/gotchas.md`: platform and tooling quirks; skim it before your first
  commit each session.

If the code and the architecture note disagree, stop and ask. Don't silently
change the design; propose an edit to the note instead.

## Workflow

- Work only on the task you were given; raise anything outside it with the
  maintainer instead of building it.
- Plan before code: `superpowers:brainstorming` settles the design with the
  maintainer, then `superpowers:writing-plans` writes the plan, which the
  maintainer approves.
- Run an approved plan as written with
  `superpowers:subagent-driven-development`.
- Small changes skip the plan: a fix, or a test-only or tooling change the
  maintainer approves as a short scope, is built test-first, reviewed once and
  opened as its own PR.
- One branch and one PR per plan. Each task is its own reviewed commit.
- Commit messages: Conventional Commits, e.g.
  `feat(git): turn snapshots via temp index`. No requirement, milestone, plan or
  task IDs in commit messages, branch names, or PR titles and descriptions: they
  mean nothing once the plan is done.
- Never force-push to `main`. Never merge your own PR.

## Commands

```sh
make build        # bin/aim
make run          # run the app; make run ARGS="..." passes arguments
make test         # go test ./...
make test-changed # the tests of the changed packages; TEST_PKGS=... overrides
make test-race    # go test -race ./...
make doc-tests    # TestKeysPage, TestGuideLinks, TestConfigExample, TestReadmeKeys
make lint         # golangci-lint run
make fmt          # golangci-lint fmt
make scan         # the glyph, em-dash and wall-clock scan
make scan-commits # IDs, session links, em-dashes and big blobs in the branch's commits
make check-fast   # the per-commit check
make check        # CI's Linux go-checks gates, for a local run
make install      # go install into $GOBIN, else $GOPATH/bin
make release-dry  # the GoReleaser dry run: archives, checksums, binaries, changelog and notes
```

## Layout

| Package            | Responsibility                                                    |
| ------------------ | ----------------------------------------------------------------- |
| `cmd/aim`          | Binary: CLI, `hook` and `statusline` subcommands, wiring          |
| `internal/core`    | State store, actions, config, tab layouts (`internal/core/tiling` holds the ported tuios layouts) |
| `internal/ui`      | Compositor, frames and chrome, widgets, keymap, themes, syntax highlighting, image placement |
| `internal/term`    | PTY plus the `VT` interface; `tuios.dev/vt` adapter               |
| `internal/harness` | `Harness` interface; Claude Code adapter                          |
| `internal/git`     | Worktrees, turn snapshots, watcher, diff engine                   |
| `internal/github`  | Auth via `go-gh`, GraphQL and REST client, poller, PR actions     |
| `internal/md`      | Markdown to styled cells plus image references; the folder walk   |
| `internal/store`   | `state.json`, reviewed hunks and the config's `theme` line        |
| `spikes/`          | Throwaway experiments; nothing outside `spikes/` imports them     |
| `scripts/`         | POSIX shell helpers for the Makefile                              |

Dependency rules:

- Only `internal/ui` and `cmd/aim` import Bubble Tea, Lip Gloss or
  Bubbles.
- `internal/git`, `internal/github`, `internal/md` and `internal/store` have no
  UI imports and are testable on their own.
- Only `internal/core` mutates app state, through actions on its store.
  Services report to the UI's update loop as messages, which it turns into
  actions; the UI renders from the state.
- Only the adapter in `internal/term` imports `tuios.dev/vt`. Everything else
  uses the `VT` interface.

## Code conventions

- Go 1.27. Idiomatic Go: small interfaces defined where they're used, accept
  interfaces and return structs, no package-level mutable state.
- Every blocking call takes a `context.Context` as its first argument. Git,
  diff, HTTP and file IO never run on the Bubble Tea update loop; they run as
  `tea.Cmd`s or goroutines that send messages back.
- Errors: wrap with `fmt.Errorf("...: %w", err)`; sentinel errors with
  `errors.Is`. No `panic` outside `main` and tests.
- Every goroutine has an owner and a shutdown path; no leaks on pane close.
- Logging: `log/slog` to the log file in the state dir. Never print to stdout or
  stderr, since the TUI owns the terminal.
- Colors come only from the theme. No hardcoded colors in widgets.
- Every user action is a named command, so it's reachable from the keymap and
  the command palette.
- Shell out to `git` with explicit arguments (never through `sh -c`), `-z`
  output where available, and `GIT_OPTIONAL_LOCKS=0` for reads.

## Documentation

- Documentation must always be true: every statement matches the code at HEAD,
  and a change that makes a doc line false updates it in the same commit.
- Document the current state - what exists now and why, not how it got here.
- Comments explain only non-obvious decisions, constraints, safety invariants,
  or workarounds. Do not narrate what code does or record change history; git
  holds history. Keep comments to one or two tight lines unless a longer
  explanation is genuinely necessary. Rewrite or delete if you find comments
  that don't follow this rule.
- Never use em-dashes; use hyphens (-).
- Never reference plan phases, task numbers, or private card IDs in doc
  comments.
- The user docs are `README.md`, `docs/guide/` and `config.example.toml`. A
  change to behaviour, keys or config updates them in the same commit.
- `docs/guide/keys.md` is generated from the keymap:
  `go test ./internal/ui -run TestKeysPage -update` rewrites it, and the test
  fails while it is stale.
- `TestConfigExample` (`cmd/aim`) checks that `config.example.toml` loads to the
  defaults, names every setting and lists the allowed values the code accepts.
  `TestGuideLinks` (`internal/ui`) checks the links and image paths of the
  guides, `README.md` and `docs/architecture.md`, and the guide pages the
  example names.
  `TestReadmeKeys` (`internal/ui`) checks that the README's Keys table is built
  from the default keymap and command titles, and that every leader key the
  README names is a default binding. When only non-code files changed,
  `make check-fast` runs these three and `TestKeysPage`; otherwise it tests
  only the changed packages.
- A change to the config's settings or checks (`internal/core`'s config and
  keys, `internal/ui`'s key checks, the themes) also runs
  `go test ./cmd/aim -run TestConfigExample` before its commit, since
  `check-fast` tests only the changed packages. A change to the command
  registry, the keymap or the themes (the `theme.set.*` rows) also runs
  `go test ./internal/ui -run 'TestKeysPage|TestReadmeKeys'`, since the keys
  page and the README's Keys table and leader keys are built from them.

## Testing

- New behavior needs a test. Prefer table-driven tests.
- Tests check behavior. A test that fails only when someone rewords text no code
  reads is not a test: don't write it, and delete it when found. Rendering
  goldens are behavior.
- A test of a notice asserts that it shows, its level and the values the code
  put in, or compares with the constant or helper the code builds it from;
  never the sentence around them.
- Every test checks one feature or behaviour. Don't write, and delete when
  found, a test that is a duplicate, the same feature tested again by an
  obscure route, an insane edge case, or a sweep where a few chosen cases check
  the same thing (pick the chosen cases, the boundaries included).
- A hostile-input test (escape bytes, bidi characters, huge lines, deep
  nesting) uses the smallest input that still proves the limit.
- `internal/ui` tests never start git: they use the fake in
  `internal/ui/fakegit_test.go`, and the package's `TestMain` fails the run
  when one does, outside the `bench` tag. Real git is tested in `internal/git`.
- New tests call `t.Parallel()` unless they use `t.Setenv`, change the
  process's working directory, or count process-wide resources such as open
  descriptors or goroutines.
- Tests don't assert on wall-clock durations: inject a clock or wait for the
  event. A real-time budget belongs in a test behind the `bench` tag.
  `make scan` fails on `time.Since`, `time.Until` and `time.Now().Sub` in a
  test without that tag.
- Rendering: golden files under `testdata/` (`-update` flag to regenerate);
  `teatest` for Bubble Tea flows.
- Diff engine: golden-file tests for hunks, word spans and full-file view.
- `internal/git`: tests run against temporary repos created in `t.TempDir()`,
  never the real repo.
- GitHub: recorded fixtures with `httptest`; no network in unit tests. Live
  tests behind a build tag.
- VT: feed recorded byte streams (including real Claude Code output) and assert
  on the screen grid.

## Commit discipline

Run `make check-fast` before every commit; it must be clean. It runs the format
check, lint, the glyph, em-dash and wall-clock scan and the tests of the
packages the change touches (`make check-fast TEST_PKGS=./...` runs them all),
and it builds and vets the whole module, so a change that stops another package
or its tests from compiling fails it. A commit that changes no code (only
Markdown at the root, `docs/`, `.github/`, `config.example.toml` and the like;
`scripts/code-changed.sh` lists them) gets only the scan and the doc tests.
Whether the other packages still behave is CI's to check. A change to
goroutines, channels or locks also runs the race detector on the tests it adds
or changes:
`go test -race -run '<those tests>' ./<package>`.

CI runs the full gates on every pull request that changes Go code (every test
under the race detector, the darwin build, `go mod tidy` and the native macOS
tests), only the doc tests on one that changes none, and the commit scan on
every pull request. A branch goes up as a draft pull request, so CI checks it
while the work finishes. Run `make scan-commits` before every push;
`make check` runs CI's Linux go-checks gates locally when you need them without
a push.

- No Claude session links (`Claude-Session` lines or `claude.ai/code/session_`
  URLs) in commits or PRs.
- **Never commit to `main`.** Work on a feature branch; tasks of a plan or scope
  the user approved commit there without asking again.
- Push only feature branches, and only with the user's approval; a standing
  approval may cover a plan's branch and its draft pull request.
- Conventional commits: `type(scope): concise summary`. Always include a scope.
- Body uses bullet points for the what and why - no long paragraphs.
- Never reference requirement IDs, milestones, plan phases, task numbers, or
  private card IDs in messages.

## Safety rules

- Never modify the user's `~/.claude` settings. Agents get hooks only through a
  generated `--settings` file (see `docs/architecture.md`).
- Never modify the user's shell startup files. A shell AIM starts gets its
  integration only through its argv and environment (see
  `docs/architecture.md`).
- Never touch the user's git index, stash or branches as a side effect. Turn
  snapshots use a temporary `GIT_INDEX_FILE` and `refs/aim/*` only. A
  local branch is deleted only from the worktree cleanup confirm, with its box
  checked, when it holds nothing the merged pull request did not.
- Write only to the platform state and cache dirs, worktrees under AIM's
  worktree root, `.git/info/exclude`, and the `theme` line of the user's
  `config.toml`. Only the cleanup confirm removes such a worktree, and only the
  theme picker's `ctrl+s` writes that line.
- Merges always pass a pinned head `sha`: the head of the diff the drawer
  loaded or, from the pull request list, the head the row shows. Every approve
  or merge needs a confirm step in the UI.
- Code ported from TUIOS (MIT) keeps its license header and is listed in
  `THIRD_PARTY.md`.
