# AIM architecture

AIM is one Go binary that hosts Claude Code agents in terminal panes it
emulates itself, with each agent's diff, pull request and CI checks beside
them. What it does for the user is in the [README](../README.md) and
[the guide](guide/README.md). The rules, the package table and the dependency
rules are in [AGENTS.md](../AGENTS.md), and platform quirks are in
[gotchas.md](gotchas.md).

This note holds what those and the code do not say on their own: how the
running app fits together, the designs whose reasons the code does not show,
and the invariants a change must keep. Paths are from the repository root;
releases are built from `.goreleaser.yaml` and `.github/workflows/release.yaml`.

## The running app

One process owns the screen, every PTY and every service. There is no daemon:
quitting AIM ends its agents, and the next start resumes their sessions.

- **State is a value.** `core.Store.Dispatch` clones `core.State`, applies one
  `core.Action` and keeps the copy only when the action succeeds
  (`internal/core/state.go`). The store has no lock: only the Bubble Tea
  goroutine touches it. `State.PRs` and `State.Marks` are shared between
  copies, so an action replaces those maps and never edits them in place.
- **Core is a pure reducer.** `internal/core` imports nothing of AIM but
  `internal/core/tiling`. Push, approve, merge, worktree creation and cleanup
  are `tea.Cmd`s in `internal/ui` that call `internal/git` and
  `internal/github`; core only records what they found (`SetRepoPRs`,
  `SetBranchPR`, `NoteEdit` and the like).
- **The UI drives everything.** `ui.App` (`internal/ui/app.go`) owns the store,
  the panes and the overlays. Every service reports to its update loop as a
  message; the message's handler builds a `core.Action`, and `App.dispatch`
  applies it and hands the new state to the saver. `View` renders from the
  state and the UI's caches.

`run` in `cmd/aim/main.go` builds the services and tears them down:

| Service | Reaches the loop through | Stopped by |
| --- | --- | --- |
| Pane read loop, one per pane | dirty and exit messages | `App.Close` |
| Hook server and turn gate | `App.waitEvent` | `hookRuntime.Stop` |
| Watch set, one watcher per worktree | `App.waitWatch` | `WatchSet.Close` |
| Pull request poller | `App.waitPRs` | `Poller.Close` |
| State saver and mark files | (writes only) | `Saver.Close`, `MarkFiles.Close` |

**Off the update loop** run git, GitHub, file reads, markdown parsing
(`md.Parse` recurses once per nesting level), syntax highlighting and image
decoding, as `tea.Cmd`s or goroutines. Each runs under a context derived from
the app's, which quit cancels, with its own limit as a named constant beside
its caller (`reviewLoadTimeout`, `pushTimeout`, `mergeTimeout`). The rail's
probes and the edit resolutions share 4 slots (`railProbeMax`).

**Late answers are normal.** The pane may have closed or been replaced under
the same id, a newer request gone out, the session changed or the overlay
closed. Messages carry what they answer (the `*pane`, a request counter, the
source, the session), and the handler drops a stale one before it dispatches.
New async work must do the same.

**Layout.** `core.Arrange` (`internal/core/geometry.go`) is the one function
that turns a tab into the rects drawn and the PTY sizes. The agents rail
narrows the body, so it is part of the layout. The review drawer is an overlay
held in `core.Tab.Review`, outside `Arrange`, so opening it never resizes a PTY
or makes an agent redraw.

**The tray's click.** Row 0 holds the tabs and the tray. `hitTab` places the
usage module from the same `planBar` plan the bar draws with (`trayModuleAt`),
so a click lands where the module is drawn. While the usage popover shows with
no modal above it, it takes every press first: inside its box a press does
nothing, and anywhere else it closes the popover and goes no further.

## Claude Code: launch, hooks and the socket

**Launch.** An agent pane runs
`$SHELL -l -i -c '<fixed script>' <shell name> claude --settings <file>`, plus
`--resume <id>` on a restore (`term.AgentCommand`, `internal/term/env.go`).

- A login, interactive shell finds `claude` on the user's own PATH, and its job
  control makes `claude` lead the terminal's foreground group (for fish the
  script adds `status job-control full`).
- The arguments follow a fixed script and are never quoted into it, because no
  quoting is safe in every shell. The script ends in `exec "$SHELL" -l`, so
  leaving Claude leaves a shell.
- Every pane gets `TERM=xterm-256color` and `COLORTERM=truecolor` and loses an
  inherited `TMUX`, `TMUX_PANE`, `AIM_PANE` and `AIM_SOCK` (`term.PaneEnv`).

**Pane kind.** A pane is an agent while its terminal's foreground process
group leader is `claude` (`internal/term/fg_linux.go`, `fg_darwin.go`), probed
with the cwd at most every 500 ms, so a hand-typed `claude` makes an agent and
an exited one a shell. Hooks never change the kind; events for a shell are
dropped. A launched agent has 10 s (`agentGrace`) to show `claude` first.
The leader's name is its comm on Linux. On macOS it is argv[0]'s base name, or
argv[1]'s when argv[0] is `node` or `bun` and argv[1] is not an option, since an
npm install's `claude` runs as `node <path>/claude` (`internal/term/procargs.go`).

**The settings file.** AIM never edits `~/.claude`. Each instance writes
`<state dir>/run/<pid>-claude.json` (`setupHooks`, `cmd/aim/hooks.go`) with one
handler per hook event in the exec form (`command` plus `args`), so no shell
parses the binary's path. Claude Code merges settings levels, so the user's and
the project's hooks keep running. The status line has no exec form, so
`harness.QuotePath` single-quotes the path and refuses one holding `'`, `\`, a
control character or invalid UTF-8; such a binary gets hooks and no status line.

**The subcommands.** Claude runs `aim hook claude-code` per event and
`aim statusline` for its status line (`cmd/aim/hook.go`, `statusline.go`). Each
reads stdin, parses it with the harness, adds `AIM_PANE` and sends one JSON
line to `AIM_SOCK`, within 2 s (1 s for the status line). Both always exit 0
and print nothing: Claude adds the stdout of `SessionStart` and
`UserPromptSubmit` hooks to the model's context.

**The socket.** `<state dir>/run/<pid>.sock` is 0600 in a 0700 directory
(`internal/harness/socket.go`). A connection carries one line of at most
64 KiB, answered `ok` once queued, except a prompt or an edit's `PreToolUse`,
held until its snapshot is written or 1.5 s pass (`turnHold`). The adapter caps
what one event carries (16 changed paths, 32 KiB) to stay inside the line. When
the socket or settings file cannot be set up, agents run without `--settings`
and a notice says why; AIM still starts.

**From payload to state.** `harness.Claude.ParseEvent` reduces a payload to a
`harness.Event`, and `App.onHook` (`internal/ui/agents.go`) applies it. The
event-to-state table is in `internal/harness/claude.go`, pinned by recorded
payloads in `internal/harness/testdata`. Two rules cover what hooks miss:

- No hook fires when a permission prompt is dismissed with `esc`. So a working
  pane silent for 3 s becomes idle, and a key that answers a dialog makes a
  blocked pane working. Hooks always win.
- One `esc` interrupts a Claude turn, so after an `esc` that hands a pane the
  keyboard, further ones are held back briefly (`handleKey`,
  `internal/ui/input.go`; [Esc and Claude Code](guide/agents.md#esc-and-claude-code)).

The status line also carries the account's usage. `rate_limits` becomes
`Event.FiveHour` and `SevenDay` (a window counts only with a percent and a reset
time, `rateWindow`), and `onStatus` dispatches `core.NoteUsage` only when it
changes `State.Usage`. Claude Code reruns the status line with a session's
last-known limits, which can be hours old, so per window a reading with the same
reset only raises the figure, a later reset replaces it, and an earlier or
already-past one is ignored.

## Turn snapshots

At each prompt AIM records the agent's worktree as a commit, so turn mode can
show what one turn changed even when the agent commits or stages during it.
The first edit of a turn in another worktree takes that worktree's start
before the edit runs.

**How** (`internal/git/worktree_tree.go`, `snapshot.go`): copy the worktree's
index to a temporary file in `<state dir>/run`, keeping its mtime so git trusts
the stat cache (a fresh index would rehash every file); with `GIT_INDEX_FILE`
on it, stage what `ls-files` lists as changed (untracked files over 50 MiB left
out) and `write-tree`; `commit-tree --no-gpg-sign` as `AIM <aim@localhost>`, so
no user identity or signing setup is needed; then one `update-ref --stdin`
transaction of `create` lines. Refreshed index copies are cached under
`<user cache dir>/aim/index`. Each start removes a copy not refreshed for 14
days (its mtime is that of the temporary index it was stored from, which staging
rewrites) and a temporary file over an hour old
(`git.SweepIndexCache`).

**Refs.** `refs/aim/turns/<session>/<key>/<n>/start` and `.../end`, `<key>`
being the first 16 hex digits of the SHA-256 of the worktree root. They live
in the common dir, which keeps the commits from `git gc` and each worktree's
turns apart. `<n>` is the prompt's arrival in milliseconds (raised past the
previous number if the clock went back), so every repository one turn touched
shares it. A turn ends at the next prompt, not at `Stop`: the new start's tree
is also the old turn's end, in one transaction.

**The gate** (`runTurnGate`, `cmd/aim/turns.go`) sits between the socket server
and the app. It forwards every event in order, starts a snapshot before
forwarding its event (so a backed-up app channel cannot delay it), runs one
pane's snapshots one at a time, and releases the held hook once the snapshot is
written. It lives in `cmd/aim` because it joins `internal/harness` and
`internal/git`, which do not import each other. The forwarded event carries
`Snapped`, which the drawer waits on before it reloads the turn.

**Budgets.** A snapshot has 1.4 s from when the server read its line
(`turnBudget`); the server holds the answer at most 1.5 s, and the hook command
gives up at 2 s. A tree not written in time is cancelled. Once it exists, the
commit and ref get their own 2 s (`refWriteBudget`), because a killed
`update-ref` leaves its lock file behind. A turn without a snapshot gets a
marker commit (the empty tree, trailer `Snapshot: none`), so numbering holds
and the drawer says so instead of showing a wrong diff.

**The skip.** When the worktree is watched and its watcher's counter
(`Watcher.Seq`) has not moved since the pane's last good snapshot there, the
snapshot reuses that tree. A watcher that failed or overflowed reports itself
unwatched, so the skip never trusts one that may have missed a change.

**Git calls** each run in their own process group. Cancelling one kills the
group and re-sends the kill for up to 100 ms, since on macOS a child forked
during the kill can miss it (`internal/git/proc_unix.go`). Every call drops
inherited repository-local `GIT_*` variables (`cleanEnv`, `run.go`), so it
reads only the repository it was pointed at.

**Pruning.** The clean-up confirm's `enter` deletes the merged pull request's
refs (`git.DeletePRRefs`, under the ref-write budget) whatever happens to the
worktree, plus a removed worktree's turn refs (`git.PruneWorktree`, by the root
`git.Open` reported before removal). A failed prune is logged and never fails
the clean-up.

A sweep covers the rest (`internal/ui/refsweep.go`). It runs once per run per
repository, keyed by the common dir, and starts when the first pane resolves
there. Only the instance that holds `state.lock` sweeps, since another starts
with a fresh state and would read every restored session of the first as unheld.
It has two phases: the listing reads ref names and values only, off the loop; the
update loop then filters it against the state as it is at the answer, and a
delete off the loop removes what is left. It leaves out the sessions any pane
holds and the pull requests any drawer shows or is fetching
(`drawerState.fetching`). It deletes a session's turn refs, in both layouts, when
its newest `<n>` in the repository is over 14 days old (`staleAge`), and every
other pull request's refs. Each delete carries the listed value. The turn refs and the pull
request refs are deleted separately, and each in chunks of 500 (`deleteChunk`),
one `update-ref` transaction per chunk, since a call killed by the ref-write
budget leaves lock files in the user's repository, `packed-refs.lock` among them.
A pull request's head and base share a chunk. A ref that moved fails only its own
chunk, so it cannot hold back the turn refs or the other chunks. There is no lock
shared with a fetch: one that starts after the filter and writes an unchanged
head before the delete runs loses its refs, which costs one drawer load error
that `r` fixes. The converse holds for a second instance: the lock holder cannot
see its panes or drawers, so it deletes the refs of a pull request that
instance's drawer shows (the drawer keeps its loaded diff, `r` fetches again,
and the automatic refresh skips pull request drawers) and the turn refs of its
sessions idle over 14 days.

**Invariants.** A snapshot writes only its temporary files and create-only refs
under `refs/aim/`, and never holds an agent past the hold. A session id that is
not a UUID, or a `cwd` outside a repository, gets no snapshot.

## The diff engine and the watcher

**One engine, three sources** (`internal/git/diff.go`): `BranchSource` (the
merge base with the base branch against the working tree, untracked files
included), `TurnSource` and `PRSource` (`refs/aim/pr/<n>/head` and `base`,
fetched with an empty refmap so nothing else is written). The drawer's modes
therefore share the full-file view, reviewed hunks and rendering.

- It compares tree ids, never HEAD, the working tree made a tree through the
  snapshot's temporary index, so agent commits never break a diff.
- Two passes: a listing (`diff --raw --numstat -z -M -C --histogram`) of every
  file, then hunks only for the files that need them, parsed with go-gitdiff
  and cached by blob pair.
- Caps (the `[diff]` config) bound files, bytes and lines a file. At the byte
  cap git is killed; a file past a cap keeps its counts, marked `Truncated`.
- `Engine.Update` (`internal/git/update.go`) re-lists only the touched paths,
  and its result must equal a fresh `Diff`. It falls back to a full `Diff`
  whenever that is not certain: a possible rename or copy, over 200 touched
  paths, a `.gitignore` or git metadata change, an overflow, a truncated file,
  a result near a cap.
- A reviewed hunk's id (`git.HunkID`) hashes the path and the added and removed
  lines only, so a mark survives moved line numbers and rebases.

**The watcher** (`internal/git/watch.go`) watches every directory but `.git`,
the main checkout's `.worktrees` and the directories git ignores, so
`node_modules` gets no watches; on macOS kqueue holds a descriptor per watched
file, so this keeps a large repository under the open-file limit. Git metadata
(`HEAD`, `index`, `packed-refs`, `refs/` without `refs/aim/`) is watched apart
and reported as `Batch.Git`; leaving out `refs/aim/` keeps AIM's own snapshot
writes from waking it. A batch goes out 100 ms after the last event, or 1 s
after the first while events keep coming. `internal/git/fsnotify` is a patched
copy of fsnotify ([gotchas.md](gotchas.md#file-watching)).

**The watch set** (`internal/git/watchset.go`) shares one reference-counted
watcher per worktree root, keyed by `Repo.Root`, never by a cwd string.

## Following the agent

A pane has three directories (`internal/core/layout.go`):

| Directory | Is | Used by | Saved |
| --- | --- | --- | --- |
| `Cwd` | OSC 7 path, else the foreground group's cwd (Linux), else the start dir | restore | yes |
| `WorkDir()` | the cwd the agent last reported in a hook, else `Cwd` | tab name, new panes, picker | no |
| `FollowDir()` | the worktree of the latest counted edit, else `WorkDir()` | rail, watch, drawer, poller, push | no |

Claude Code files its sessions under the directory it was launched in, and
`--resume` must start there, so a hook's cwd never changes the saved `Cwd`. An
agent edits in other worktrees (a `cd` in Bash, an absolute path), so what the
operator reviews follows its edits, not its launch directory.

Edits come from the edit tools' paths and from Claude Code's list of files a
Bash command changed. `git.EditPlace` resolves a path to its worktree (the
nearest existing directory, symlinks resolved; `.git` paths and ignored files
do not count), behind one `git.PlaceCache` the app and the gate share, so a
`PreToolUse` and its `PostToolUse` cost one lookup. Edits are ordered by hook
arrival (`core.Edit.Seq`), not by when git answers, so a late answer never moves
the followed place back, and one after a session change records nothing.

## GitHub

- **Auth** (`internal/github/token.go`): go-gh's environment and config, else
  `gh auth token --secure-storage`, one client per host. Without a token the
  GitHub features are off and the rest works. Errors never hold a token, and
  git's and the API's errors have their URLs removed, since a URL may carry
  credentials.
- **Repositories.** The remote URL names the repository, ssh aliases mapped
  through `ssh -G` (`internal/github/repo.go`). Only watched repositories are
  polled: each agent's followed place, the focused shell's, and whatever the
  drawer, merge box or checks overlay shows.
- **The poller** (`internal/github/poller.go`) runs one goroutine per watched
  repository and one GraphQL query per poll. The focused pull request's checks
  come over REST through an ETag cache (`internal/github/etag.go`), since
  GraphQL has no conditional requests and a `304` costs no rate limit. Polls
  come every 10 s while a shown pull request has pending checks, else every
  60 s; a rate limit waits for its reset, other errors back off to 5 min. A
  failed poll keeps the last good data on show.
- **Approve and merge are pinned to the reviewed head.** Both send `Result.Head`
  of the diff the drawer loaded, never the poll's head (`internal/ui/reviewpr.go`),
  and every route to them (drawer, `leader g m`, the list) loads that diff
  first. GitHub refuses a merge whose `sha` is not the head (`409`,
  `ErrHeadMoved`), so a pull request that moved after it was read is never
  merged. Both run only after a confirm; `internal/github` refuses either
  without a `sha` (`ErrNoSHA`).
- **Push** (`internal/git/push.go`, `internal/ui/push.go`) always targets
  `refs/heads/<branch>`, so a branch cut from `origin/main` never lands on
  `main`, and is never forced. The confirm freezes the target; the push
  re-reads branch, HEAD, remote and push URLs first and pushes nothing when any
  changed.
- **Network git** starts in a new session with no controlling terminal and
  `GIT_TERMINAL_PROMPT=0`, so a credential prompt fails instead of taking over
  AIM's terminal.
- **Pictures** load only from GitHub's hosts, and the token goes only to the
  document's own host and is dropped once a redirect leaves it
  (`internal/github/image.go`).

## The VT boundary

- `internal/term/vt.go` defines `VT`. Only the adapter in `internal/term`
  (`vt.go`, `keys.go`) imports `tuios.dev/vt`; the compositor, selection and
  copy mode see `VT`, so another emulator is another `VT`.
- Each pane owns its process, its `VT` and one goroutine that reads the PTY
  into the `VT` under the pane's mutex and signals a one-slot dirty channel
  (`internal/ui/pane.go`). `VT` is not safe for concurrent use. The update loop
  re-renders only changed rows into the pane's row cache.
- Synchronized output (DEC 2026): tuios's hold reads the wall clock, so AIM
  follows the mode from the emulator's callbacks and holds a pane at most 1 s
  on an injectable clock (`syncMaxHold`, `internal/ui/clock.go`).
- A scrolled view anchors to its row plus `VT.HistoryTrimmed()`, so it stays
  on the same text while output scrolls or the 10000-line cap drops lines.
- The hidden `--record <dir>` flag writes each pane's PTY output in the
  `internal/term/rec` format, which the benches replay through the emulator.

## Persistence and restore

| Path | Holds | Written |
| --- | --- | --- |
| `<state dir>/state.json` | tabs, layouts, panes, sessions, plans | after every dispatch; atomic, fsynced |
| `<state dir>/state.lock` | the `flock` that makes one instance the saver | held, not written |
| `<state dir>/reviews/*.json` | reviewed-hunk marks, per repository | atomic, not fsynced |
| `<state dir>/run/` | socket, settings, temporary indexes | per run; swept |
| `<user cache dir>/aim/` | index copies, fetched images | as used; swept at start: index copies not refreshed for 14 days, images untouched 30 |

- **Only what cannot come back is saved.** Agent states, actions, reported
  directories, edits, models, Claude usage, pull request data and a drawer's pin
  come back from hooks, polls and probes; saving them would restore stale facts.
- **Format.** Version 2, encoded only in `internal/store` (`state.go`,
  `layout.go`); core has no JSON tags. New fields are optional, so the version
  stays 2. A file that does not decode is kept as `state.json.bad`.
- **Resume.** Before the TUI starts, an agent whose transcript file is gone gets
  a fresh `claude` (`cmd/aim/restore.go`); the rest run `--resume <id>` in the
  saved `Cwd`. A refused resume exits fast with no `SessionStart`, and at the
  10 s probe the pane restarts once with a fresh `claude`.
- **Quit** closes the saver before any pane (`cmd/aim/main.go`), so agents that
  die on quit, `SIGHUP` or `SIGTERM` are not saved as shells.

## Frame budget and idle cost

- A bench fails on a frame over 16.7 ms, one 60 Hz frame, and reports a p99
  over 8 ms, which leaves half the frame to the terminal (`frameBudget`,
  `p99Headroom`, `internal/ui/benchkit_support_test.go`). Benches build under
  the `bench` tag: `go test -tags bench -run <name> ./internal/ui`.
- Bubble Tea re-parses and diffs the whole view string after every message, so
  `View` must stay cheap: panes and the drawer render from row caches and
  `View` joins them.
- AIM has no fixed tick. A tick runs only while something animates: the agent
  tick (125 ms) while an agent works, the rail's pulse while one is blocked and
  the rail shows, the 16 ms slide tick while the drawer slides. A new animation
  follows the same rule, so an idle AIM costs next to no CPU. The usage module
  and popover have no tick of their own; the clock's minute wake redraws their
  relative times and drops a window once its reset passes.

## Why the safety rules

[AGENTS.md](../AGENTS.md#safety-rules) has the rules; these are the reasons.

- **`~/.claude`** holds the user's settings for Claude everywhere, not only in
  AIM. A per-instance `--settings` file needs nothing in it, merges with it,
  and a crash leaves only a run file the next start sweeps.
- **Index, stash and branches.** The user and the agent share the worktree. A
  snapshot that wrote the index would change what the next `git commit` takes,
  and one stash serves every worktree of a repository.
- **Where AIM writes.** `.gitignore` is tracked, so an entry there would land in
  the agent's diff and pull request; `.git/info/exclude` is local. A worktree
  may hold the only copy of uncommitted work, so only the cleanup confirm
  removes one, and `git worktree remove` is never forced.
- **Deleting a branch.** `git branch -d` refuses a squash-merged branch, so AIM
  uses `-D` after its own check: the tip is still the one the confirm showed
  and has no commit past the merged head (`git.BranchAhead`,
  `git.DeleteBranch`).
- **The merge `sha` and the confirm.** A pull request can move between reading
  and merging; the `sha` makes GitHub refuse that merge, and the confirm makes
  the operator's intent explicit.
- **TUIOS code.** Its MIT license asks that the notice travel with the code.
