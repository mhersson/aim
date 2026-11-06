# Plans, markdown and conversations

AIM shows three kinds of text in one reading box: an agent's plan, any markdown file, and a pull request's conversation. They share their keys, search and links. This page covers opening each, then the keys.

The keys are the defaults. A changed `leader_key` or `[keys]` table changes them; see [Keys](keys.md#reading-overlays) for the full list and [Configuration](config.md#remapping-keys) for how to remap. The search prompt and the link chooser take fixed keys, given below.

## The agent's plan

To read the focused agent's plan, press `ctrl+space p`. From the agents rail, press `p` on the selected card (see [Keys](keys.md#agents-rail)). With no plan, the notice is `no plan from <name> yet`; in a shell pane it is `the focused pane is not an agent`.

AIM records a plan from the agent's own work:

- the file Claude Code writes when it leaves plan mode, if it is a markdown file (the box tag reads `plan mode`);
- a markdown file under one of the folders in `[plans] paths` (the tag reads `plan file`) that the agent edited, or that a finished shell command of the agent changed. Each folder is matched anywhere in the file's path, and the plan's links reach up to the directory above the deepest match; see [`config.example.toml`](../../config.example.toml).

The latest plan wins, and only the agent's edits count, not yours. While a new plan is unread, the agent's card on the rail shows the plan mark; opening the plan clears it. A plan that the agent rewrites while you read it reloads in place and the tag reads `rewritten` until your next key. A new conversation in the pane (`/clear`, or a fresh start) drops the plan.

A plan that cannot be read leaves the box closed with a notice such as `not opening <path>: no such file`.

## A markdown file

To read any markdown file, press `ctrl+space m`, or choose "Open a markdown file" (`md.open`) in the command palette (`ctrl+space :`). The finder opens in a box the size of the reading box, with the prompt on top, the folder in the title and the list below it. From 100 terminal columns the box also splits in two, with the list on the left and a preview of the selected row on the right (see [The preview](#the-preview)). In a terminal too small for the box the finder stays open unseen and only `esc` works.

It starts in the folder you are looking at: the one the review drawer shows while the drawer has the keyboard, else the focused pane's followed folder (the worktree of its latest edit, else where it works), then your home folder, then `/`. It needs no repository. A new open always starts there with an empty prompt.

### The list

The list shows folders and markdown files (`.md` and `.markdown`) only. With nothing typed it shows this folder alone: `..` on top (none at `/`), then the folders, then the files, each by name with case ignored; a folder ends in `/`. The cursor starts on the first entry under `..`, and going up leaves it on the folder you came from. A folder that cannot be read shows `can't read this folder: <reason>` under `..`, and an empty one `no folders or markdown files here`.

Hidden entries (a leading `.`) are not listed or searched. Type `.` alone to list this folder's hidden entries, or start a search with `.` (`.co`) to search with them included.

### The preview

The right column shows the selected row rendered like the reading box, with no pictures. The row above it holds the file's path (`~` for your home folder, cut on the left when it is long), its size and when it changed, as in `~/notes/plan.md · 2.4 KB · 3h`, then a blank row. A folder shows its `README.md` or `README.markdown` rendered, with that file's path above it, else its folders and markdown files as a list; `..` shows the parent the same way, and `~/` shows your home folder. An empty folder reads `no folders or markdown files here`, and a file that cannot be read `can't read this file: <reason>`. A file over 4 MiB shows its first 4 MiB. The preview follows the cursor, and a slow read never holds the cursor back. Under 100 columns there is no preview and nothing is read for it.

### Search

Typing searches everything below this folder, ranked by the same fuzzy match as the command palette; spaces are ignored. The matched letters are painted. A result reads the file name first, then two spaces and its folder relative to the current one, dimmed. Folders are results too, and `enter` goes into one. `..` is not listed while you search, and `No matches` says nothing fits.

The search is a walk of the folder, run in the background from your first letter; later letters filter what it has found, with no new reading. Going to another folder, or closing the finder, ends it. The count in the bottom border reads `3 of 37`, and while the walk runs `3 of 37 · searching…`. The walk skips hidden folders, `.git` always, `node_modules`, `vendor`, `target`, `__pycache__`, Go's module cache (`pkg/mod`), `/proc`, `/sys`, `~/Library` on macOS and any folder on another filesystem than the one it started in. It does not follow symlinked folders (browsing can go into one), skips a folder it cannot read, and does not ask git, so ignored files are searched. It stops at 20,000 markdown files or after 10 seconds, and the count then says `stopped at 20,000 files` or `stopped after 10 s`.

### Keys

These keys are fixed, like the palette's, and are not remapped by `[keys]`.

| Action | Key |
| --- | --- |
| Move the cursor | `up` `down`, `ctrl+p` `ctrl+n`, `shift+tab` `tab` |
| Open the file, go into the folder, go up on `..` | `enter` |
| Go up a level (on an empty prompt; at `/` nothing) | `backspace` |
| Scroll the preview | `ctrl+d` `ctrl+u`, `pgdown` `pgup` |
| Jump to `/` (as the first character) | `/` |
| Jump home | `~` then `/` |
| Go up | `..` then `/` |
| Take the one jump row for `~` or `..` | `enter` |
| Close the finder | `esc` |

A `/` anywhere else is an ordinary character. While the prompt reads exactly `~` or `..` the list has one row for that jump. Going to another folder clears the prompt. `ctrl+d` and `pgdown` scroll the preview down half its height, `ctrl+u` and `pgup` up; a new row starts at the top. A paste goes into the prompt and never jumps.

### Opening a file

`enter` on a file opens it in the reading box over the finder, read-only, with the keys and search described below. The file's root is the repository it is in, else its own folder; symlinks in the path are resolved first, so a file linked from another folder opens as the file it is. Links in the file follow inside that root. The title reads `<repository> · <path>` in a repository, else the path with `~`.

`esc` in the box goes back to the finder exactly where you were: the folder, the prompt, the results, the cursor and the walk. A page followed from a link steps back first. A second `esc` closes the finder. `e` opens the file in the editor, starting in the root, and closes the finder too.

In a repository the file is read again when the worktree changes; outside one nothing watches it. `r` reads it at once in both cases.

The review drawer's `m` shows a changed markdown file rendered in the drawer instead; see [Review](review.md).

## The conversation

To read a pull request's description and timeline, press `i` in the review drawer (the branch needs a pull request, else the notice is `no pull request for <branch>`), or on a row of the PR list. The box opens at once and reads `loading the conversation…` until GitHub answers. A failed first read shows `could not read the conversation: <reason>` in the box, and a failed re-read shows the same text as a notice. Without GitHub the notice is `pull requests are off`.

`r` reads it again, and it also reloads when the next poll shows the pull request changed. `esc` closes it and returns to the drawer or the list. See [Pull requests](pull-requests.md) for the rest of the pull request workflow.

A comment or review from a bot is one folded row; `enter` opens it.

## Move, sections and folds

These keys work in all three boxes. If the terminal is too small to draw the box, it stays open unseen and only the close key (`esc`) works.

| To | Press |
| --- | --- |
| Move a line | `j` or `down`, `k` or `up` |
| Half a page down, up | `ctrl+d`, `ctrl+u` |
| A screen down, up | `ctrl+f` or `pgdown`, `ctrl+b` or `pgup` |
| First, last line | `g`, `G` |
| Next, previous heading (plan and file) or item (conversation) | `]`, `[` |
| Open or close a fold, else follow a link | `enter` |

In a plan or file, `]` and `[` jump between headings, and `enter` on the head of a `<details>` block opens or closes it. In the conversation they jump between items. The mouse wheel scrolls the box three lines a notch; dragging with the left button selects text.

## Search

To search, press `/`, type, and press `enter`. The cursor jumps to the next line after it that holds the text, ignoring case and wrapping round at the end. When the box has no hint row (a very short terminal), `/` does nothing.

| In the prompt | Press |
| --- | --- |
| Find | `enter` (closes the prompt) |
| Delete the last character | `backspace` |
| Close without searching | `esc` |

A paste goes into the prompt. These keys are fixed. Pressing `enter` with nothing typed repeats the last search. After a search, `n` goes to the next matching line and `N` to the previous one. When nothing matches, the notice is `not found: <query>`.

## Links and pictures

To follow a link, put the cursor on its line and press `enter`. A line with one link follows it; with several, a chooser titled `Links` lists them, and with none nothing happens. Only web links and links to files are offered: a `mailto:` link, or a link of another scheme such as `ftp:`, is not.

| In the chooser | Press |
| --- | --- |
| Select the next, previous link | `j` or `down`, `k` or `up` |
| Follow the selected link | `enter` |
| Close the chooser | `esc` |

Other keys, and a paste, are dropped while the chooser is open. These keys are fixed, and the review drawer's chooser for a rendered markdown file uses them too.

- A web link opens in the browser (`xdg-open` on Linux, `open` on macOS) and the box stays.
- A link to a markdown file inside the worktree opens as a page over the plan or file; the title shows the page's path. A page's links open further pages, and `esc` goes back one page.
- Any other file inside the worktree opens in the editor at line 1, in a new shell pane beside the focused one, and closes the box. The editor is `$VISUAL`, else `$EDITOR`, else `vi`.
- A path outside the worktree says `not opening <path>: outside the worktree`, a missing one `not opening <path>: no such file` and a folder `not opening <path>: it is a folder`; the box stays open.
- In the conversation only web links are offered.

`esc` steps back one level: the chooser, then the search prompt, then a page, then the box.

### Pictures

AIM draws an image in the text as a picture only when the terminal answers AIM's Kitty graphics query with `OK` before it answers the device-attributes request, reports its cell size, and the colour profile is truecolor. AIM sends no query when `TMUX` or `AIM_PANE` is set in its environment, so inside tmux or inside another AIM pane images are lines. A terminal that supports graphics but not truecolor is asked once for `RGB` and `Tc`, and an answer to either turns pictures on.

Otherwise an image is one line holding the image icon (or `image: ` when `icons = false`), its alt text and its host (a local image shows its file name instead). While a picture's size loads the line ends in `loading…`, and a picture that fails to load stays a line.

Only some images load as pictures: an `https` image on `github.com`, a `.githubusercontent.com` host or the pull request's own GitHub host, and, in a plan or file, a local image inside the worktree (for a plan-mode plan, the agent's working directory). An `.svg` never loads. A picture is at most 100 cells wide, 20 rows tall and 5 MiB. The cursor steps through a picture a row at a time, and `enter` on it follows the image's link.

## Copy, edit, read again, close

| To | Press |
| --- | --- |
| Copy the selection, else the paragraph or item at the cursor | `y` |
| Open the file in the editor at the cursor's line | `e` |
| Read again | `r` |
| Close, or go back to the finder for a file opened from it | `esc` |

`y` copies as described in [Copy, paste and the mouse](copy-paste-mouse.md). `e` works in a plan and in a markdown file: it opens the file in a new shell pane beside the focused one and closes the box; in the conversation, which has no file to open, it only says so. `r` reads the shown file or the conversation again; a plan or file keeps its place.
