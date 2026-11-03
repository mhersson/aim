# Review an agent's work

The review drawer shows the diff of the agent in the focused pane, live, beside the pane: what it changed in its last turn, or everything the branch changed. This page covers opening the drawer, the two modes, reading and marking the diff, and how the drawer follows the agent from worktree to worktree.

The keys are the defaults. A changed `leader_key` or `[keys]` table changes them; see [Keys](keys.md#review-drawer) for the full list and [Configuration](config.md#remapping-keys) for how to remap. `[diff]` in [`config.example.toml`](../../config.example.toml) sets what a new drawer starts in.

## Open, use and close the drawer

To open the drawer for the focused pane, press `ctrl+space d`. It slides in over the right half of the body (at least 60 columns, so more than half on a narrow one) and takes the keyboard; the panes underneath keep their sizes.

- Outside a git repository the notice is `not a git repository`. With no drawer open, the other drawer commands show `no review drawer is open`.
- With the drawer open and the keyboard in a pane, `ctrl+space d` points the drawer at the focused pane, if that pane is in a repository, and gives it the keyboard. With the keyboard in the drawer, it closes the drawer.
- To give the keyboard back to the panes and leave the drawer open, press `ctrl+space h`. With the drawer showing and no visible pane to the right, `ctrl+space l` gives the drawer the keyboard.
- `esc` steps back one level: it closes the search prompt, then a page opened from a link in a rendered markdown file, then takes the keyboard from the file tree (the tree stays open), then un-zooms, and when none of those is on it closes the drawer. A markdown file shown with `m` is not a page, so `esc` leaves it on.

Each tab has its own drawer, and a restart restores whether it was open. The drawer's keys apply while it has the keyboard; the leader keys work as everywhere.

## Turn and branch mode

- **Branch mode** (`b`) shows everything the branch changed against its base branch, untracked files included.
- **Turn mode** (`t`) shows what the agent changed in one turn. A turn runs from one prompt you send to the next, so a turn that is still going shows live. Turns of a session that no pane has held for 14 days are removed, so resuming an old session in a new pane shows no earlier turns.

A new drawer opens in turn mode when the agent has turns in that worktree and `mode` under `[diff]` asks for it; otherwise it opens in branch mode.

To step through turns, press `,` for the previous turn and `.` for the next. In branch mode either key enters turn mode on the latest turn. The turns listed are those of the worktree the drawer shows; a turn that changed nothing there is left out. The notices:

| Notice | When |
| --- | --- |
| `no turns: this pane has no agent session` | the pane is a shell, or an agent without a session |
| `no turns yet` | the session has no turn yet |
| `no turns in <project> · <branch> yet` | the drawer shows another worktree than the agent's own, and no turn changed it |
| `turn 1 is the first turn` | `,` on the first turn |
| `turn <n> is the latest turn` | `.` on the latest turn |

A shell, or an agent without a session, has branch mode only. A turn drawer whose pane has no turns shows the branch.

## Read the diff

- `j` and `k` (or `down` and `up`) move a line, `ctrl+d` and `ctrl+u` half a page, `g` and `G` go to the top and bottom.
- `]` and `[` jump to the next and previous hunk, `}` and `{` to the next and previous file.
- `u` switches between unified and side by side. When the drawer is too narrow for two sides, it stays unified and the notice is `side by side needs a wider drawer`. `layout` under `[diff]` sets the starting layout.
- `f` shows the file under the cursor whole, with its changes marked, and `f` again returns to hunks. A binary file shows `a binary file has no full view`. A file too large for its hunks reads `too large, press f for the full file`.
- `enter` on a block of removed lines in the full view folds it into one row, and `enter` on the row shows it again.
- `m` shows a markdown file rendered, and `m` again shows its diff (or its full text while `f` is on). On another file the notice is `not a markdown file`, and on a deleted one `the file is deleted`. In a rendered file, `enter` on a line follows its links; see [Reading](reading.md).
- `z` widens the drawer over the whole body right of the rail, and back. `ctrl+space z` does the same while the drawer has the keyboard.
- `r` reads the diff again. The drawer also updates by itself as files change.
- `y` copies the selection or, with none, the line under the cursor; see [Copy, paste and the mouse](copy-paste-mouse.md).
- `e` opens the file at the cursor line in a new shell pane beside the drawer's pane and closes the drawer. The editor is `$VISUAL`, else `$EDITOR`, else `vi`.

### The file tree

`tab` opens the file tree, on the left of the drawer, and gives it the keyboard; the tree is hidden when a drawer opens. Pressing `tab` again with the tree focused hides it. The tree lists folders before files at each level, each by name, and the diff lists its files in the same order.

| To | Press |
| --- | --- |
| Select the next, previous file | `j` or `down`, `k` or `up` |
| Jump to the selected file and give the diff the keyboard | `enter` |
| Give the diff the keyboard, leaving the cursor | `esc` |
| Zoom | `z` |

Only these keys, `tab` and the leader work in the tree; the others are dropped. Whether the tree is open is saved with the tab.

## Search

To search the diff, press `/`, type, and press `enter`. The cursor jumps to the next row after it that holds the text, ignoring case and wrapping round at the end; only rows that are shown count.

| In the prompt | Press |
| --- | --- |
| Search | `enter` (closes the prompt) |
| Delete the last character | `backspace` |
| Close without searching | `esc` |

A paste goes into the prompt. These keys are fixed. Pressing `enter` with nothing typed repeats the last search. After a search, `n` jumps to the next match and `N` to the previous one, wrapping round both ways; before the first search both do nothing. When nothing matches, the notice is `not found: <query>`.

## Reviewed hunks

To mark the hunk under the cursor as reviewed, press `space`. On a file's header it marks every hunk of the file, or unmarks them all when they are all marked; on a reviewed hunk it unmarks it. After marking, the cursor moves to the next unread hunk, wrapping round.

- A reviewed hunk is hidden. A file with all hunks reviewed shows its header alone, and when every hunk is reviewed and `R` is off, the drawer ends with a note that says so and names the key that shows them.
- `R` shows the reviewed hunks too, each folded to its header; `enter` on one opens it and again folds it. `R` is a switch for the drawer and is not saved.
- The top border counts the reviewed hunks of the files shown as hunks.
- Marks count only for files shown as hunks. A file shown whole (`f`) or rendered (`m`), a binary or too large file, and a markdown page count for nothing.

A mark belongs to a hunk's content, its path and its added and removed lines, so it survives moved line numbers and a rebase. Marks are kept per repository and apply in every worktree of it and in every mode. They last 90 days from the day they were set or last shown: opening a diff that shows a marked hunk renews it. They are saved under `reviews` in the state dir (see [Agents side by side](agents.md#where-it-lives)).

## The worktree list

An agent can work in more than one worktree. By default the drawer follows the agent: it shows the worktree of the agent's latest edit. To choose a worktree yourself, press `w`.

The list has, from the top:

- the "Follow the agent" row, marked `on` while the drawer follows;
- "Touched by <agent>" (the name on the agent's card in the rail): the places the agent edited this session, newest first, plus the agent's own directory and a pinned place even when no edit names them, each as `<repo> · <branch>`, with `latest` on the newest and `agent here` on the worktree of the agent's own directory;
- "Other worktrees of <project>": the other checked-out worktrees of the agent's repository.

A place shows its changes (`+A -R N files`, or `clean`) once they are counted, and the age of the latest edit there.

| To | Press |
| --- | --- |
| Select the next, previous row | `j` or `down`, `k` or `up` |
| Pin the drawer to the selected place, or follow the agent again on the first row | `enter` |
| Build the list again | `w` |
| Close the list | `esc` |

A pinned drawer stays on its worktree whatever the agent edits and shows `pinned` in its border; the drawer opens the list on the pinned place. The leader works as usual; every other key is dropped. Picking a place also leaves pull request mode. The pin ends when the drawer's pane closes. A pinned worktree that has been removed has no row in the list, and the drawer shows `worktree removed: <path>`.

## Follow the agent's edits

An unpinned drawer shows the worktree of the agent's latest edit. When the agent edits a file in another worktree or repository, the drawer moves there, and its title names that project and branch.

These edits move the drawer:

- Claude Code's `Edit`, `Write`, `MultiEdit` and `NotebookEdit` tools, seen when the tool starts and when it finishes. A subagent's edit counts like the agent's own. An edit that you then refuse still moves the drawer, since AIM sees it before the permission prompt.
- Files a shell command changed, only when Claude Code reports them. Claude Code reports the files a Bash command changed in auto and `bypassPermissions` mode, and in every mode when the `bashEditDiffEnabled` setting is on. You set that in your own Claude Code settings; AIM never changes them. A command that moves the working tree, such as `git checkout`, reports none.

A file git ignores, or one outside every worktree, moves nothing. A new session starts with no edits.

Pull request mode (`p`) is in [Pull requests](pull-requests.md).
