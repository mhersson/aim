# Plans, markdown and conversations

AIM shows three kinds of text in one reading box: an agent's plan, a markdown file from a worktree, and a pull request's conversation. They share their keys, search and links. This page covers opening each, then the keys.

The keys are the defaults. A changed `leader_key` or `[keys]` table changes them; see [Keys](keys.md#reading-overlays) for the full list and [Configuration](config.md#remapping-keys) for how to remap. The search prompt and the link chooser take fixed keys, given below.

## The agent's plan

To read the focused agent's plan, press `ctrl+space p`. From the agents rail, press `p` on the selected card (see [Keys](keys.md#agents-rail)). With no plan, the notice is `no plan from <name> yet`; in a shell pane it is `the focused pane is not an agent`.

AIM records a plan from the agent's own work:

- the file Claude Code writes when it leaves plan mode, if it is a markdown file (the box tag reads `plan mode`);
- a markdown file under one of the folders in `[plans] paths` (the tag reads `plan file`) that the agent edited, or that a finished shell command of the agent changed. The folders are in [`config.example.toml`](../../config.example.toml).

The latest plan wins, and only the agent's edits count, not yours. While a new plan is unread, the agent's card on the rail shows the plan mark; opening the plan clears it. A plan that the agent rewrites while you read it reloads in place and the tag reads `rewritten` until your next key. A new conversation in the pane (`/clear`, or a fresh start) drops the plan.

A plan that cannot be read leaves the box closed with a notice such as `not opening <path>: no such file`.

## A markdown file

To read any markdown file of a worktree, open the command palette with `ctrl+space :` and choose "Open a markdown file" (`md.open`; it has no key until you bind one, see [Keys](keys.md)). The palette lists the `.md` and `.markdown` files of the worktree you are looking at: the one the drawer shows while the drawer has the keyboard, else the focused pane's. Pick one with `enter`.

- Outside a git repository the notice is `not a git repository`; with no files, `no markdown files in this worktree`.
- The file is read-only. It is read again when the worktree changes, and `r` reads it at once.
- The review drawer's `m` shows a changed markdown file rendered in the drawer instead; see [Review](review.md).

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

Only some images load as pictures: an `https` image on `github.com`, a `.githubusercontent.com` host or the pull request's own GitHub host, and, in a plan or file, a local image inside the worktree (for a plan-mode plan, the agent's working directory). An `.svg` never loads. A picture is at most 100 cells wide, 20 rows tall and 5 MiB. The cursor steps through a picture a row at a time, and `enter` on it follows the image's link. A read that is not `r` keeps the pictures it already has, except that a local image whose file changed since it loaded (a new size or modification time) loads again, and one that failed to load is tried again. A private repository's picture keeps its place across reads although GitHub signs its address anew each time.

## Copy, edit, read again, close

| To | Press |
| --- | --- |
| Copy the selection, else the paragraph or item at the cursor | `y` |
| Open the file in the editor at the cursor's line | `e` |
| Read again | `r` |
| Close | `esc` |

`y` copies as described in [Copy, paste and the mouse](copy-paste-mouse.md). `e` works in a plan and in a markdown file: it opens the file in a new shell pane beside the focused one and closes the box; in the conversation, which has no file to open, it only says so. `r` reads the shown file or the conversation again, and loads every picture on the page again, so a changed one shows; a plan or file keeps its place.
