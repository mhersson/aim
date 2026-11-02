# Copy, paste and the mouse

AIM copies text from a pane, the review drawer and the reading boxes, either with the mouse or with copy mode, a vim-style cursor over a pane's text and scrollback. This page covers both, where a copy ends up, and what the mouse does elsewhere.

The keys are the defaults. A changed `leader_key` or `[keys]` table changes them; see [Keys](keys.md) for the full list and [Configuration](config.md#remapping-keys) for how to remap. The keys inside copy mode and the mouse gestures are fixed and cannot be remapped.

## Copy mode

To pick text with the keyboard, focus a pane and press `ctrl+space [`. A block cursor appears at the bottom row of the pane's view, in column 0, and the pane's border shows a `COPY` tag. A drawer that covers the pane closes first, and any mouse selection is cleared. While copy mode is open it takes every key, and the mouse does nothing.

To move:

| To | Press |
| --- | --- |
| Left, right, down, up | `h`, `l`, `j`, `k` |
| Start of the line, first non-blank, last non-blank | `0`, `^`, `$` |
| Next word, end of word, previous word | `w`, `e`, `b` |
| Top of the history (the first non-blank of line 1) | `g g` |
| Last line | `G` |
| Top, middle, bottom of the view | `H`, `M`, `L` |
| Half a page up, down | `ctrl+u`, `ctrl+d` |
| A page up, down | `ctrl+b`, `ctrl+f` |

The view scrolls to keep the cursor in sight, and the whole scrollback is reachable.

To select, press `v` for a stream, `V` for whole lines or `ctrl+v` for a rectangle, then move. The selection runs from where you started to the cursor. Pressing the same key again clears the selection; pressing another of the three changes its shape.

To search, press `/` (forward) or `?` (backward). A prompt opens in the pane's bottom border; type up to 32 characters and press `enter` to jump to the next match, or `esc` to close the prompt and stay in copy mode. The search is a plain substring match. With nothing typed, `enter` repeats the last search. After a search, `n` repeats it in the same direction and `N` in the opposite one; both do nothing before the first search. A miss leaves the cursor where it was and shows the notice `not found: <query>`.

To copy, press `y`. The selected text is copied and copy mode ends. With no selection, `y` copies nothing and still ends copy mode.

To leave without copying, press `q` or `esc`.

These are all of copy mode's keys. Any other key does nothing. Copy mode also ends by itself when its pane closes or is resized, switches between the main and the alternate screen, moves to another place in the layout, or restarts. After `y`, `q` or `esc` the pane returns to its live screen.

## Where copies go

A copy from copy mode, `ctrl+space y` on a pane or drawer selection, `y` in the drawer and the reading boxes, a mouse selection with `copy_on_select`, or a program's own OSC 52 write, goes out by two paths:

- **OSC 52 to the outer terminal**, which puts the text on the clipboard of the machine your terminal runs on, also over SSH. On Linux AIM also sends it as the primary selection.
- **Native tools on a local session**: `wl-copy` when `WAYLAND_DISPLAY` is set, else `xclip` when `DISPLAY` is set (Linux), and `pbcopy` (macOS). On Linux the tools write both the clipboard and the primary selection. A session counts as local unless `SSH_CONNECTION` or `SSH_TTY` is set. On Linux with neither display set, nothing native runs.

The `[clipboard]` settings `osc52` and `native` apply to these two paths; see [`config.example.toml`](../../config.example.toml). AIM shows no notice when it copies.

To paste what you selected, middle-click in a pane. AIM pastes into the pane as the terminal would, with bracketed paste when the program asked for it. A mouse selection (see below) writes the primary selection when the button is released, so on Linux a middle-click pastes it in any application. The text a middle-click pastes comes from:

- the primary selection, on a local session with `native` on: `wl-paste --primary` or `xclip -selection primary -o` on Linux, and `pbpaste` on macOS, which has no primary selection and reads the clipboard;
- else AIM's own last copy: over SSH, with neither display set, with `native` off, or when the tool is not installed.

A middle-click does not paste in a pane whose program asked for mouse reporting; the click goes to the program. Some terminals (Kitty and Ghostty by default) take `shift` with a middle-click and paste their own primary selection.

## The mouse

The mouse works in panes, the agents rail, the review drawer, the tab bar and the reading boxes. The settings are in `[mouse]` in [`config.example.toml`](../../config.example.toml).

### Click, hover and the wheel

- A click in a pane's content focuses the pane and clears the selection. A click on its top border only focuses it. A click on a tab in the tab bar switches to it, and a click on the Claude usage module in the tray opens its details ([Claude usage](agents.md#claude-usage)). A pane's side and bottom borders ignore clicks.
- A click in the agents rail selects a card, a double-click jumps to the agent's pane, and a click on its plan mark opens the plan. A click in the drawer gives it the keyboard; a click on a file in the tree jumps to that file.
- To focus a pane by hovering, set `focus_follows_mouse`. Hovering a pane's content or top border focuses it; hovering the rail or the drawer gives it the keyboard. Nothing moves while a button is held, while a modal is open (see below), while the rail's search is being typed, or while a zoomed drawer or the drawer's search is open.
- The wheel acts on what is under the pointer without moving focus. Over a pane it scrolls the scrollback three lines a notch; in the alternate screen (an editor or pager) it sends three `up` or `down` key presses a notch; a program that asked for mouse reporting gets the wheel itself. In the rail it moves the selection a card a notch, in the drawer it scrolls three rows, and over the plan, markdown file and conversation boxes and the checks log it scrolls three lines a notch.
- A program that asked for mouse reporting gets a click when the button is released on the cell where it went down, and its middle and right buttons. A drag never reaches it: it selects.

### Select with the mouse

Drag with the left button; no modifier is needed. `alt`+drag selects a rectangle. A double-click selects a word and a triple-click a line, with soft-wrapped lines counting as one (the presses must land on the same cell within 400 ms). The selection stays in the pane or box where the drag began. The same gestures select in the review drawer's diff and rendered pages, and in the plan, markdown file and conversation boxes. A drag held past the top or bottom of a drawer page or a box scrolls it a row every 50 ms.

There is one selection at a time; a new click, drag or double-click anywhere clears the one before. A pane's selection also clears, for example, when you type or paste into the pane, middle-click to paste, or when the pane closes, is resized, switches screen, moves or restarts, and when the program redraws the text under it. A plain click never selects or copies.

When the button is released after a drag or a double or triple click:

- by default the text goes to the primary selection only (see above) and the selection stays, so `ctrl+space y` (or `y` in a drawer or box) still copies it to the clipboard;
- with `copy_on_select` on, the text is copied as `ctrl+space y` or `y` does and the selection clears.

### Copy a selection

To copy a selection in a pane or the drawer, press `ctrl+space y`. In a plan, markdown file or conversation box the leader key is not available, so press `y`; in the review drawer `y` works too (see [Keys](keys.md#review-drawer) for the review drawer and [Keys](keys.md#reading-overlays) for the boxes); with no selection it copies the unit at the cursor, such as a paragraph, a list item or a diff line. See [Review](review.md) and [Reading](reading.md) for those views.

### When the mouse is ignored

While one of these is open the pointer does nothing, the rail included, apart from the exceptions below: a confirm, a prompt, an overlay such as the command palette, the compose or merge box, the pull request list, the worktree picker, the drawer's worktree list or link chooser, copy mode, or a leader key waiting for its next key.

While the Claude usage details are open, a click anywhere outside them only closes them, and the wheel and hover do nothing.

The exceptions, each with its own rule:

- The wheel over the plan or markdown file box scrolls it whatever else is open. Over the conversation box it scrolls unless the conversation's own link chooser is open, which leaves it still. Over the checks log page it scrolls, except that the summary view does not scroll while its link chooser is open.
- The left button selects in the plan, markdown file and conversation boxes unless a confirm, prompt, overlay, compose or merge box, copy mode or a link chooser is open.
