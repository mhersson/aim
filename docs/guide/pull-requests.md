# Pull requests

AIM pushes an agent's branch, opens its pull request, shows the pull request's diff in the review drawer, lists the checks, approves and merges, and cleans up the worktree afterwards. This page covers each step and what AIM needs from GitHub.

The keys are the defaults. A changed `leader_key` or `[keys]` table changes them; see [Keys](keys.md) for the full list and [Configuration](config.md#remapping-keys) for how to remap. `[pull_requests]` in [`config.example.toml`](../../config.example.toml) has `review_requested_days`, which AIM uses for the Review requested tab.

## What AIM needs

AIM talks to GitHub with a token it finds the way `gh` does, for `github.com` and for a GitHub Enterprise host that `gh` knows (signed in with `gh auth login --hostname <host>`, or named by `GH_HOST`). It takes the first of these:

- `GH_TOKEN`, then `GITHUB_TOKEN` for `github.com`; `GH_ENTERPRISE_TOKEN`, then `GITHUB_ENTERPRISE_TOKEN` for a GitHub Enterprise Server host;
- the token in `gh`'s own config;
- the answer of `gh auth token --secure-storage --hostname <host>`, when `gh` is on your `PATH`.

Without a token, everything that does not talk to GitHub keeps working: panes, diffs, review and push. The pull request data does not load, and the notice is `pull requests for <host>/<owner>/<name>: run gh auth login --hostname <host>`. A creating, approving, marking ready or merging action fails with the reason. A remote that is not on a host AIM knows says `<branch>'s remote is not on GitHub`. When GitHub refuses a token that has been revoked or has expired, AIM looks the token up again at the next poll or action, so the pull requests, checks and actions pick up a new `gh auth login` without a restart.

## Push

To push the branch, press `ctrl+space g P`. The branch is the one checked out in what the open review drawer shows, else in the focused pane's working place, else in the directory AIM started in. AIM first reads what a push would do, then opens a confirm named `Push <branch>`. It lists the project, branch, remote with its push URLs, how many commits the remote lacks (with `sets the upstream` or `upstream already set`) and the newest commit.

- Press `enter` to push or `esc` to cancel; no other key works in this confirm.
- The push never forces, and goes to a branch of the same name on the push remote. Before it runs, AIM checks again that the branch, its head and the push URLs are still the ones in the confirm; if not, the notice is `push: <branch> changed since you asked; nothing pushed`.
- While it runs the notice is `pushing <branch> to <remote>…`; on success the toast is `pushed <branch> to <remote>: <n> commits` (`1 commit` for one), and AIM asks GitHub about the repository again.
- Nothing to push gives a notice instead of the confirm: `<branch> is up to date on <remote>`, `<branch> has nothing to push to <remote>`, `push: <branch> has no commits yet`, `push: <branch> has no remote to push to`, `push: HEAD is detached` or `push: not a git repository`.

## Open a pull request

To open one for the branch, press `ctrl+space g o`. It needs a GitHub remote; a branch that already has an open pull request says `<branch> already has #<n>`, and the refusals that stop the box include `open a PR: not a git repository`, `open a PR: HEAD is detached`, `open a PR: <branch> has no commits yet`, `open a PR: <branch> has no remote to push to`, `open a PR: <remote> has no branches to open against` and `<branch>'s remote is not on GitHub`.

The compose box has four fields: title, base, draft and body. `tab` and `shift+tab` move between them.

| Field | Starts as | Keys |
| --- | --- | --- |
| title | the only commit's subject, else the branch name made readable | type; a paste goes in |
| base | the repository's default branch, from the remote's branches | `left`, `right` change it; the commit count follows |
| draft | off, `[ ] open as a draft` | `space` switches it |
| body | the first pull request template, else the only commit's body, else a `- subject` line per commit | type; `enter` starts a line; arrows, `home`, `end`, `pgup`, `pgdown` move; a paste keeps its newlines |

The box's top row names the repository, the branch, the base and the number of commits. The body's top border says where it started: ` from <template path> ` or ` from the commits `. The templates AIM looks for are `pull_request_template.md` in the repository root, `.github` or `docs`, and the `.md` files of a `pull_request_template` directory there.

- `ctrl+e` edits the body in `$VISUAL`, else `$EDITOR`, else `vi`, in a pane beside the focused one. The box hides until the editor exits; then the file replaces the body. If another overlay is open by then, the box comes back once it closes. Without agent hooks the notice is `the editor needs the run directory, which is off with agent hooks`.
- `ctrl+p` switches the body between Write and Preview. In Preview `up`, `down`, `pgup` and `pgdown` scroll, and other body keys do nothing.
- `ctrl+g` generates the body with a one-off `claude --print` on Sonnet that has no tools and saves no session. Its prompt carries the commits, the diff summary (`git diff --stat`) and the template, whose sections it follows when there is one. When the box is for the worktree the focused agent works in, the run forks that agent's session, so it also knows why the changes were made; the live session is left as it is. Otherwise it starts fresh in the worktree.
  - While it runs the status row says `generating the description…` and the box stays editable. Closing the box stops the run, and so does `ctrl+s`, which creates with the body as it is.
  - An empty body, or one still as the box filled it, takes the description at once. A body you changed asks `replace the body with the generated description?`: `enter` replaces it, `esc` keeps it, and no other key answers.
  - A failure, or no answer within two minutes, reads `could not generate the description: <reason>` and leaves the body alone.
  - The run uses Sonnet whatever your default model is. A fork sends the session's whole conversation to Sonnet, so on a long session one press can cost as many Sonnet input tokens as the session holds. The run reads your Claude Code settings, so your own hooks run for it too.
- `ctrl+s` creates the pull request. An empty title says `enter a title`. When the remote lacks commits, the box says `↑<n> not pushed yet: creating the PR pushes <branch> to <remote> first`, and creating pushes first, without force. The status row reads `pushing <branch> to <remote>…`, then `opening the pull request…`. On success the toast is `opened #<n>`.
- `esc` cancels. A failure stays in the box with its reason; while it runs only the leader keys work.

## Review the pull request in the drawer

In the review drawer, press `p` to show the pull request of the drawer's branch. AIM fetches its refs into `refs/aim/pr/<n>/` (nothing else is written to your repository), deletes them at the clean-up confirm and once per run when no drawer shows the pull request, and fetches them again when the drawer opens it, and the drawer shows the pull request's diff against its base, with the same keys as other modes (see [Review](review.md)). The top of the diff holds a header with the number and title, the branch and base, the checks and the review in words. A row below it reads `↑<n> not pushed: the PR shows the pushed head; ctrl+space g P pushes` when the worktree has commits its push remote lacks, or `the PR moved to <sha7> since this diff; r fetches it` when the pull request got a new head; press `r` to fetch it again.

`b` leaves the mode, and so do `t`, `,` and `.` unless they stop with a notice (the pane has no session or no turns), and so does picking a place in the worktree list. From the drawer, `i` opens the conversation, `c` the checks, `a` approve, `M` merge and `O` the browser; when the pull request is not shown yet, AIM shows it first and then acts. `R` marks a draft ready without showing it first (see [Mark ready](#mark-ready)).

The notices when there is no pull request: `no pull request for <branch>`, `pull request: HEAD is detached`, `pull request: <branch> has no remote`, `#<n> is not on <remote>`.

## The pull request list

To list pull requests, press `ctrl+space g l`. It has three tabs, always opened on the first; `tab` and `shift+tab` switch (wrapping) and `1`, `2` and `3` jump:

- **Agents**: the open pull requests of the branches your agents are on, one row per pull request.
- **Review requested**: the open pull requests that ask for your review. `review_requested_days` ([`config.example.toml`](../../config.example.toml)) can narrow it.
- **All open**: the open pull requests you opened.

The last two search every GitHub host AIM watches when the list opens, every minute while it is open, and on `r`. Their empty texts are `loading pull requests…`, `no open pull requests ask for your review` or, when `review_requested_days` sets a limit, `no open pull requests updated in the last <N> days ask for your review` (`1 day` for one), and `you have no open pull requests`; a host whose search failed adds `pull requests on <host>: <reason>`. The Agents tab says `no open pull requests on agent branches` when it is empty.

The keys are in [Keys](keys.md#pr-list): `j` and `k` move; `enter` shows the diff; `i`, `c` and `O` open the conversation, checks and the browser; `a` and `M` open the approve confirm and the merge box over the list (see [Approve](#approve) and [Merge](#merge)); `R` marks a draft ready, on any tab and with no checkout; `r` refreshes; `esc` closes. On the Agents tab `enter` switches to the agent's tab and shows the pull request in its drawer. On the other tabs it opens the drawer on the focused pane. The pull request is read from a pane's checkout of the row's repository when there is one; with none, from AIM's private copy of the repository, so no clone is needed and your own clones are never touched. A tab with no pane says `no pane here to open the drawer on; O opens it in the browser`. A focused pane that is in no repository opens the row too, since the diff needs nothing from the pane. Leaving the pull request with `b` then shows `not a git repository`, and so does `t` when the pane has an agent session; with none, `t` says so and stays on the pull request. Without a cache folder for the copies the notice is `no cache folder for a copy of #<n>; O opens it in the browser`.

The first `enter` on a pull request waits on `fetching #<n>…` while AIM fetches it. A copy is shallow and holds no files, only the pull request's head, its merge base and the contents of the files it changes, so a large repository costs seconds and a few megabytes. The copy for a repository lives in `<user cache dir>/aim/prs/<host>/<owner>/<name>`, is safe to delete, and goes after 30 days unused. The fetch signs in with the token AIM already uses for GitHub, which has to be allowed to read the repository's contents; when it is not, the toast is `fetch #<n>: <reason>` and `O` still opens the browser. A copy has no checkout, so `e` says `no file to edit: this pull request has no checkout here`, push and open a PR act on the focused pane's own place (or say it is no repository), and following a link to another file in a rendered file says the file is not there, while a relative picture stays a line (see [Links and pictures](reading.md#links-and-pictures)). Hunks you mark in a copy belong to it: they do not show in a clone's drawer of the same pull request, or the reverse. In the drawer of a copy, `a` and `M` also wait for the repository's next poll while it does not list the pull request, with `fetching #<n>…` still showing, so the merge box has the repository's settings; a poll that fails says why, and one that no longer lists the pull request says `#<n> is no longer open`, and neither opens a box.

## Checks

To see the checks of the drawer's pull request, press `c` in the drawer, or `c` on a list row. The overlay lists each check with how long it took, failed first, then running, passed and skipped; before they arrive it says `loading checks…`, and with none `no checks on <sha7>`. When GitHub refuses them, as it does a token without access to checks, it says `could not read the checks: <reason>`; the pull requests keep updating, and each poll and `r` try again.

The keys are in [Keys](keys.md#checks-overlay). `j` and `k` select, `l` opens a log or summary page, `O` opens the check's page in the browser (`no page for that check` when it has none), `r` refreshes and `esc` closes.

- **A GitHub Actions job** has a log page with the tail of its log. `]` and `[` jump between errors, `enter` opens or closes a group, `g` and `G` go to the ends, `r` reads it again, `esc` goes back to the list. A job that is still running says `the log is ready when the job ends`.
- **A completed check run from another app**, such as CodeQL, opens a summary page; `enter` opens the link on the cursor's line in the browser. A run with no summary and a commit status say `no log for <name>; O opens its page`.

## The conversation

To read the description and the timeline, press `i` in the drawer or on a list row. Its keys are in [Reading](reading.md).

## Approve

To approve, press `a` in the drawer or on a list row. A confirm names the pull request and the commit it approves: `at head <sha7>, the commit you reviewed` from the drawer, the head its diff loaded, and `at head <sha7>, the commit listed` from the list, the head the row shows. The approval is sent for that commit. Press `enter` to approve or `esc` to cancel; no other key works. From the list the confirm opens over it at once, loads no diff and leaves the tab, the focus and the drawers where they are; `esc` goes back to the list, which stays open. The toast is `approved #<n>`.

- Your own pull request says `you can't approve your own pull request` and no confirm opens, and once the poll has the pull request, a merged or closed one says `#<n> is merged` or `#<n> is closed`. A merged or closed pull request cannot be merged either, with the same notice.

## Mark ready

To mark a draft ready for review, press `R` in the drawer or on a list row; the key hints show `R ready` only for a draft. It acts on the drawer's pull request, the shown one or else the branch's, or on the row's, and loads no diff. A confirm names the pull request and says `ready for review: GitHub notifies its reviewers`, since that cannot be taken back. Press `enter` to mark it ready or `esc` to cancel; no other key works. The toast is `marked #<n> ready`, and the next poll drops the `draft` label.

- A pull request that is not a draft says `#<n> is not a draft`, a merged or closed one `#<n> is merged` or `#<n> is closed`, and one the poll has not read yet `#<n>: its draft state is unknown`; no confirm opens.
- Marking ready needs the same token permission as merge (pull requests write). AIM cannot turn a pull request back into a draft.

## Merge

To merge, press `M` in the drawer or on a list row, or `ctrl+space g m`, which opens the drawer on the focused pane first if none is open. The merge box shows the pull request, `head <sha7>, the commit you reviewed; GitHub refuses if the PR moved since` from the drawer or `head <sha7>, the commit listed; GitHub refuses if the PR moved since` from the list, its checks, review and merge state, and warns about failed or running checks and a missing approval. `enter` still merges after a warning, if GitHub allows it.

| To | Press |
| --- | --- |
| Choose squash, merge commit or rebase | `s`, `m`, `r` |
| Delete the branch after merging, or keep it | `d` |
| Merge | `enter` |
| Cancel | `esc` |

The keys are in [Keys](keys.md#merge-box). Only the methods the repository allows are offered; the box starts on merge commit, else squash, else rebase. The delete row shows only for a branch in the same repository that is not the default branch, and starts on when the repository deletes head branches after merging.

From the list the box opens over it, loads no diff and leaves the tab, the focus and the drawers where they are; `esc` goes back to the list, which stays open, and so does a merge. The box takes the repository's merge settings from its poll, so `M` on a row of a repository that has not been polled yet shows `fetching #<n>…` and opens the box when the poll answers. A poll that fails says why, and one that no longer lists the pull request says `#<n> is no longer open`; neither opens a box. Moving to another row, switching tabs or closing the list drops the wait. A merged row leaves the list, and the clean-up confirm below opens over it.

The merge always carries the head the box shows: the drawer's loaded head, or the one the list showed. If the pull request moved since, nothing merges and the toast is `merge #<n>: the PR moved; nothing merged`; an open list is read again, so the next `M` has the new head. On success the toast is `merged #<n> (<method>)`, with `, branch deleted` when it was deleted.

## Open in the browser

`O` opens the pull request's page from the drawer or the list, and a check's page from the checks overlay. AIM runs `xdg-open` on Linux and `open` on macOS. Only `http` and `https` links open; any other says `not opening that link: only http and https links open`.

## Clean up a merged worktree

After a merge, AIM looks for a worktree that has the merged branch checked out. It skips a pull request from another repository and one from the default branch. To check later, run `worktree.cleanup` from the command palette (`ctrl+space :`) on the focused pane, once AIM knows its pull request is merged; the command has no default key, so bind it under `[keys.leader]` to get one.

When the worktree can go, a confirm titled `Clean up after #<n>` offers two boxes. `w` switches removing the worktree, `b` deleting its local branch, `enter` cleans up and `esc` keeps everything; no other key works.

- A pane that keeps the worktree, a shell there or an agent started there, stops the removal: the confirm names it, and only `esc` is offered. Cleanup closes no pane.
- The branch is offered only when it holds nothing the merged head does not: its tip is the head or behind it. It goes only with the worktree. Nothing is forced.
- The result is a toast such as `removed <path>; deleted <branch>`, or `kept <path>: <reason>`.

`enter` also deletes the pull request's refs under `refs/aim/pr/<n>/`, and the turn snapshots of a worktree it removed; `esc` keeps them.

The worktree stays, with a reason instead of the confirm, when it is not one of AIM's own worktrees (`<path> is outside AIM's worktrees, so it stays`, see [Agents side by side](agents.md#a-new-worktree)), is locked, has uncommitted changes, or an agent is still working in it. After a merge, the reason is added to the merge toast while it shows. From the command, the notices include `#<n> is not merged`, `#<n>: its merged head is unknown`, `#<n>'s branch is in another repository` and `<branch> is the default branch, so it stays`, `no worktree has <branch>` and `clean up: HEAD is detached`; a pull request that cannot be found gives the drawer's notices, such as `no pull request for <branch>`.
