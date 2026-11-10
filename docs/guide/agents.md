# Agents side by side

AIM runs Claude Code in panes next to each other, in tabs, with a rail that lists every agent and a layout that comes back after a restart. This page covers starting agents, moving between panes and tabs, the rail, notifications, the command palette, closing things and what a restart restores.

The keys here are the defaults. A changed `leader_key` or `[keys]` table changes them; see [Keys](keys.md) for the full list and [Configuration](config.md#remapping-keys) for how to remap.

## The leader key

AIM's own keys sit behind a leader key, `ctrl+space`, so every other key goes to the focused pane and the agent in it. Press the leader, then the key of a command: `ctrl+space n` opens an agent.

- Press the leader and wait a moment (`which_key_delay`, see [`config.example.toml`](../../config.example.toml)) and a which-key popup lists the keys, grouped as Panes, Layout, Tabs, Git and App.
- Some keys start a two-key sequence: `ctrl+space g l` opens the pull request list. After the first key (`g`, `>`, `.` or `;`) a smaller popup lists the second keys at once.
- `esc` cancels a pending leader or sequence, unless that sequence binds `esc` itself (`ctrl+space ; esc` clears the preselection).
- To send `ctrl+space` itself to the pane, press the leader twice: `ctrl+space ctrl+space`. Inside a sequence, the leader key does the same.
- A key that is not bound shows a notice: `ctrl+space x is not bound`, or `ctrl+space > x is not bound` inside a sequence.

### Esc and Claude Code

In Claude Code one `esc` interrupts a turn. When an `esc` closes something of AIM's (a popup, a prompt, an overlay, the rail, a confirm) and hands the pane the keyboard, a further `esc` is held back until 750 ms pass with no `esc`; after one that was held back, until 500 ms pass. A doubled or held `esc` is held back too. Any other key ends the wait.

## Start an agent

To start an agent next to the focused pane, press `ctrl+space n`.

- Outside a git repository, the agent starts at once, in the focused pane's working directory.
- Inside one, the worktree picker opens first (titled "New agent"). It lists the repository's worktrees, each with its branch, its path, how many agents already run there, and `locked` or `prunable` when git says so.

In the picker:

| To | Press |
| --- | --- |
| Filter the list | type; it matches the branch and the path |
| Move | `down`, `ctrl+n` or `tab`; `up`, `ctrl+p` or `shift+tab` (they wrap) |
| Start an agent in the selected worktree | `enter` |
| Close the picker | `esc` |

A `prunable` worktree has no directory any more; `enter` on it shows `that worktree is gone; git worktree prune removes it`.

### A new worktree

The last row, `new worktree…` (`+ new worktree…` with `icons = false`), opens the new-worktree page. Fill in the `branch` field; the `base` field starts at the repository's default branch (`origin/<name>` when `origin/HEAD` names a branch that exists, else `main`, else `master`; empty when there is none), and the `path` line shows where the worktree will go. `tab` or `shift+tab` switches between `branch` and `base`, `enter` creates the worktree and starts an agent in it, and `esc` goes back to the list, keeping what you typed.

- An existing branch is checked out; a new one is created from the base.
- A failure shows on the page: `enter a branch name`, `that path already exists`, `not a valid branch name`, `no usable base branch; name one in the base field`, `no main checkout; set root = "sibling" in [worktrees]`, or git's own reason. While git works the page reads `creating the worktree…`.
- A `.worktreeinclude` file in the main checkout's root names the files a new worktree gets copies of, such as a `.env`. Its patterns use `.gitignore` syntax, and a file is copied only when git also ignores it, so a tracked file never is. Only regular files are copied, with their permission bits; symlinks and files the worktree already has are left alone. A repository without the file copies nothing.
- On success the notice is `created <path>`, or `created <path>; copied <n> files from .worktreeinclude` (`1 file` for one) when files were copied. A copy failure makes the notice an error ending in `could not copy <file>: <why>` for the first file that failed, or `could not read .worktreeinclude: <why>`; the worktree and the agent start all the same.
- For a worktree inside the repository's `.worktrees` folder, the first one in a repository adds `/.worktrees/` to the repository's `.git/info/exclude`, and the page lists tools that need their own ignore entry, such as an editor's file watcher.
- `root` under `[worktrees]` in [`config.example.toml`](../../config.example.toml) sets where new worktrees go.

### Shells beside the agent

- `ctrl+space s` opens a shell below the focused pane, `ctrl+space v` one to its right. Both start in the focused pane's working directory and take focus. That is where a BSP tab puts them; the other layouts place a new pane by their own rule, see [Layouts](layouts.md).
- Run `claude` in a shell and the pane becomes an agent pane; when Claude exits in an agent pane, it becomes a shell again. The rail lists agent panes only.
- A `claude` you type in a shell AIM started is a full agent: hooks, states, turns, follow, context, model, usage and resume, in the directory it started in. AIM does this without touching your shell startup files: a wrapper first on the shell's PATH adds the hooks to your `claude`, and a `claude` function in the shell calls it.
- It may not be one when typed by full path, through `npx` or `bunx`, inside a shell or `tmux` started in the pane, in a terminal inside an editor (such as an nvim terminal) or another program run in the pane, in a shell other than zsh, bash or fish, under macOS's `/bin/bash`, in zsh when `/etc/zshenv` sets `ZDOTDIR`, or with its own `--settings`. Such a `claude` still makes the pane an agent pane, but may show under No hooks.
- To see it: the shell's PATH starts with an AIM directory, and `type claude` names a function.
- `[shell] integration = false` in the config turns it off; every pane then starts as it did without it (see [Configuration](config.md)).
- At most 64 panes fit: past that, the notice is `at most 64 panes`.

### Focus, resize, swap and zoom

| To | Press |
| --- | --- |
| Focus the pane to the left, below, above, right | `ctrl+space h`, `j`, `k`, `l` |
| Resize the focused pane | `ctrl+space H`, `J`, `K`, `L` |
| Swap it with its neighbour | `ctrl+space ctrl+h`, `ctrl+j`, `ctrl+k`, `ctrl+l` |
| Zoom it to fill the body, and back | `ctrl+space z` |

Opening a pane, closing one or moving focus ends the zoom. How resize and swap behave in each layout is in [Layouts](layouts.md).

## Tabs

Each tab has its own panes, layout and zoom. The workspace bar at the top shows them, numbered.

| To | Press |
| --- | --- |
| Open a tab with a shell | `ctrl+space c` |
| Next tab, previous tab (they wrap) | `ctrl+space tab`, `ctrl+space shift+tab` |
| Jump to tab 1 to 9 | `ctrl+space 1` to `ctrl+space 9` |
| Rename the tab | `ctrl+space ,` |
| Close the tab | `ctrl+space X` |

A new tab starts with a shell in the focused pane's working directory; start an agent in it with `ctrl+space n`. A tab with no name of its own shows the directory name of its focused pane.

Renaming edits the name in the workspace bar, up to 32 characters. `enter` saves, `esc` cancels, and an empty name brings back the automatic one. These keys are fixed.

Moving panes between tabs (`ctrl+space >`, `.` and `!`) is in [Layouts](layouts.md).

## The agents rail

The rail is a panel on the left that lists every agent in every tab, so you can see which one is blocked without switching tabs.

- `ctrl+space a` opens it and gives it the keyboard. With the rail open but the panes holding the keyboard, it takes the keyboard; with the rail holding it, it closes the rail.
- It stays open until you close it, and a restart keeps it open or closed.
- On a narrow terminal (a body under 64 columns) it is hidden but stays open; it comes back when the window widens.
- The mouse selects and jumps too; see [Copy, paste and the mouse](copy-paste-mouse.md).

While the rail has the keyboard (the focused pane's frame looks unfocused):

| To | Press |
| --- | --- |
| Select the next, previous card | `j` or `down`, `k` or `up` |
| Jump to the selected agent | `enter` |
| Open the selected agent's plan | `p` |
| Search | `/` |
| End a search, then give the panes the keyboard back | `esc` |

`enter` makes the agent's tab active, focuses its pane, flashes its frame and hands it the keyboard; the rail stays open. The leader works as usual, and focus and tab commands return the keyboard to the panes. Every other key is dropped.

In the search, type to filter by project, session name, branch, current action or tab. `enter` keeps the filter, `esc` clears it, `backspace` deletes, and a paste goes into the search. The typing is fixed; the keys above are `[keys.rail]`. With no match the list reads `no agent matches`, and with no agent pane at all `no running agents`.

Plans: `ctrl+space p` opens the focused agent's plan from a pane. With no plan the notice is `no plan from <name> yet`, and in a shell pane `the focused pane is not an agent`. Reading the plan is in [Reading](reading.md).

### What a card shows

Agents are grouped as Blocked, Working, Idle (and No hooks, for an agent that has sent no state), each with a count. A card has four lines:

1. the state, the session name (or the project), the model, the context meter, a plan mark while the plan is unread, the time since the last activity, and a dot when the agent changed state or went idle while its pane was not the focused one, until you focus it;
2. the branch, the commits ahead of its upstream (`↑N`), and its pull request with checks and review;
3. what the agent is doing now (the tool and its target, or the question a blocked agent asks); an idle agent reads `waiting for a prompt`;
4. the diff stats against the base branch: lines added, lines removed and the file count.

## Claude usage

AIM replaces Claude Code's own status line in its agent panes, to read the context, model and usage; outside AIM your own status line is unchanged.

With a Claude subscription the tray shows how much of its limits you have used: `5h` for the 5-hour window and `7d` for the weekly one, each with its percent. With icons on, the module starts with a gauge icon. The figures come from the agents' status lines, so they appear after an agent's first reply and are the latest any agent reported; a status line that repeats an older, lower figure for the same window does not pull it down.

A plan with only a weekly limit shows only `7d`. Without limits (API billing, Team, Enterprise, Bedrock and Vertex, or an older Claude Code) nothing shows.

A percent takes the theme's warning colour from 70 % and its error colour, bold, from 90 %, as the context meter does. A window leaves the tray when it resets, until an agent reports it again. On a narrow terminal the module leaves the bar right after the load module.

Click the module or press `ctrl+space u` (command "Claude usage") for the details: each window's bar and percent, when it resets (`resets in 2d 4h (Wed 14:00)`), and how long ago the figures last changed. `esc`, `ctrl+space u` again or a click outside them closes them. While they are open the leader works as usual and every other key and a paste are dropped. With no figures yet, `ctrl+space u` says `no Claude usage yet`. When the module has left the bar, the details open at the right edge.

## Notifications

AIM sends a desktop notification to the terminal it runs in when an agent needs you or has finished, and only for an agent you cannot see. It notifies when:

- an agent becomes blocked: a permission prompt or a question waits for you;
- an agent's turn ends: Claude stopped, with or without an error, after working.

An agent that goes quiet for a few seconds is not a finished turn, and an agent already blocked does not notify again until it has been in another state. Panes with no hooks never notify.

You cannot see an agent when the terminal window does not have focus, or when its pane is not drawn: it is on another tab, hidden behind a zoomed pane or off-screen in a scrolling layout, or the review drawer is zoomed over the tab. A pane under the half-width drawer, a dialog or the palette counts as seen. A terminal that never reports focus leaves only the pane rule.

Over OSC 99 and OSC 777 the notification is titled `AIM` and its text is `<agent name> needs you` or `<agent name> is done`. OSC 9 has no title, so its one text reads `AIM: <agent name> needs you` or `AIM: <agent name> is done`. The name is the one the rail shows.

Three settings in the `[notifications]` section of the config change this:

- `notify`: `all` (the default), `blocked` (no done notices) or `off` (nothing, the bell included);
- `bell`: `true` rings the terminal bell with each notification; the default is `false`;
- `protocol`: `auto` (the default), `osc9`, `osc99` or `osc777`.

With `auto`, AIM picks the escape from the terminal: kitty gets OSC 99; Ghostty, WezTerm, foot and urxvt get OSC 777; iTerm2 and every other terminal get OSC 9. Set `protocol` when it guesses wrong. urxvt needs its notify extension. With `bell = false` kitty (0.36 or newer) is asked to make the notification silent; on other terminals the sound is the operating system's setting.

Inside tmux, AIM wraps the notification in tmux's passthrough, which needs `set -g allow-passthrough all` in `tmux.conf`. With `on`, tmux passes a notification only while AIM's pane is visible, so it drops the ones sent while you are in another tmux window. tmux passes focus reports on only with `set -g focus-events on`; without it AIM learns nothing of focus inside tmux and only the pane rule applies. The choice of escape comes from the variables the terminal leaves in the environment.

## Images an agent sends you

An image counts as sent when the agent:

- sends it with `SendUserFile` (Claude Code offers that tool only with Remote Control on or in a cloud session), or
- names its path in the final reply of a turn: an absolute, `~/` or relative path, as plain text, in code or as a markdown link or image. Only files that exist count.
- runs `"$AIM_BIN" show <file>... --caption "<text>"` in any of AIM's panes, which you can also type yourself. `AIM_BIN` is the binary AIM runs its hooks with, set in every pane that has hooks; it is quoted because the macOS state folder holds a space. The caption is optional. It checks each file first (a regular file, one of the image types below, at most 5 MiB that decodes) and sends nothing when one fails. It prints `sent 1 image to AIM` or `sent 3 images to AIM`; otherwise it prints `aim: ` and the reason, and exits 1.

Only `png`, `jpg`, `jpeg`, `gif` and `webp` files are shown, a GIF as its first frame; any other file sent is not shown. A send holds at most 8 files at a time and each file at most 5 MiB. AIM copies each image when it arrives, so the viewer keeps what the agent sent even when the agent overwrites the file next. The list lasts for this run only.

`ctrl+space i`, or `Show images an agent sent` in the palette, opens the viewer on the focused agent's newest images, or on those of the agent that sent the newest when the focused pane has none. It fills the reading box with one page per image, newest send first. `h` and `l`, or the left and right arrows, step to newer and older pages, and the bottom border says where you are, as `3 of 7`. A send of two images shares a page when the box is 80 columns wide or more (a terminal 84 columns wide); below that, and for any other send, each image has a page. `o` hands AIM's copy of each image on the page to your system's image viewer (`xdg-open`, or `open` on macOS); an image AIM could not read or decode is not opened, and a notice says why. `esc` closes the viewer, which also closes when its agent's pane does. Without pictures the viewer shows each image's file name and names the `o` key under it. The keys are under [`[keys.images]`](keys.md#image-viewer).

When a send arrives, on any tab, a card appears at the bottom right, about a third of the window wide and directly above the toast's rows. It names the agent (and its tab when that is not the one you are on) and shows the images, with the caption under them, or the file names and the caption without pictures. The card takes no keys: everything you type still goes to the focused pane. A click on it, or `ctrl+space i`, opens the viewer, on the agent whose card it is. It goes away 15 seconds after you could first see it, which starts only while AIM's window has focus and no overlay is open over it, or when the viewer opens. A send from any agent replaces the card and starts the wait again. The which-key popup may cover the card while it is open. A terminal too small for the card shows a notice instead, which names the key that opens the viewer.

Pictures need a terminal with the Kitty graphics protocol (kitty and Ghostty), not inside tmux. Without them the viewer shows each image's file name instead.

AIM tells each agent about `aim show` at every session start, resume, `/clear` and compaction, so it can send you an image by itself. `tell_agents = false` under `[images]` in the config stops that. In Claude Code's auto mode `aim show` ran without asking in AIM's tests. In manual mode the agent asks every time it runs it, since the command starts with a variable, which no permission rule can match. Inside Claude's Bash sandbox `aim show` cannot reach AIM, and the agent names the image in its final reply instead.

## The command palette and themes

Every action is a command, and the palette runs any that is not specific to one view: press `ctrl+space :`, type part of the name, move with `up` and `down` (or `ctrl+n`, `ctrl+p`, `tab`, `shift+tab`), and press `enter` to run it. `esc` closes it. Each row shows the leader key of its command, if it has one. Commands without a key, such as "Layout: BSP", are listed in [Keys](keys.md#commands-without-a-default-key).

To change the theme, press `ctrl+space t`. The list previews each theme as you move; `enter` applies it, `ctrl+s` applies it and saves it in the config, and `esc` closes the picker and keeps the theme you had. The same themes are in the palette as "Theme: ...". The list holds the four Catppuccin flavors, then any [themes of your own](themes.md).

A theme you apply with `enter` or from the palette lasts until AIM quits; the next start uses `theme` from the config. `ctrl+s` sets `theme` in the config file AIM started with (the `--config` file, else the default path), so the next start uses it too. It changes only the value of the file's top-level `theme` line, or adds `theme = "<name>"` at the top when there is none, and leaves every other line as it was; with no config yet it creates the file. A config that is a symlink stays one, and the file it points to changes. A toast names the theme and the file. When the file is not valid TOML or cannot be written, an error toast says why and the file is left as it was; the theme still applies until AIM quits. To set the theme by hand, see [`config.example.toml`](../../config.example.toml).

These keys are fixed: typing, the movement keys above, `enter`, `ctrl+s` and `esc`.

## Close panes and tabs

| To | Press |
| --- | --- |
| Close the focused pane | `ctrl+space x` |
| Close the tab | `ctrl+space X` |
| Quit AIM | `ctrl+space q` |

A shell pane closes at once. Anything that stops an agent asks first, in a box with two answers: `y` or `enter` confirms, `n` or `esc` cancels, and any other key is ignored.

- Closing an agent pane: "Close this agent pane?" with "The agent stops running."
- Closing a tab with agents: `Close tab 2 "api"?` with "Its agent panes will stop running."
- Quitting with agents open: "Quit AIM?" with "Agent panes are running and will stop."

A tab with no agents closes without asking, and so does a quit with none. A pane whose process exits closes without asking. The last pane of a tab removes the tab; the only tab stays and shows a welcome card. The push and approve confirms take `enter` and `esc` only, and the cleanup confirm also `w` and `b`: see [Pull requests](pull-requests.md).

## After a restart

AIM saves your layout as you work and restores it on the next start, in the same directory or not:

- the tabs, their names, layouts, focus and zoom, and each pane's working directory;
- whether the rail and each tab's review drawer were open;
- each agent's latest plan.

Every agent pane starts `claude --resume` with its saved session, so the conversation continues. A shell starts fresh in its saved directory; what it was running and its scrollback are gone. The theme and the agents' states are not saved: the config decides the theme, and the states come back as the hooks report them.

An agent that cannot resume starts a fresh `claude` and the notice says so: `started a fresh claude in api: its session could not be resumed`, or `started 3 fresh claudes: their sessions could not be resumed`. Several start-up notices show together, joined with `; `. A pane whose directory is gone starts in the launch directory with `<dir> is not available, started in <launch dir>`.

To start with one shell instead, run `aim --fresh`. That shell is the first change, so the new layout replaces the saved one as soon as AIM starts.

If the saved layout cannot be read, AIM keeps it as `state.json.bad` and starts fresh with `the saved layout could not be read; it was kept as state.json.bad`.

### Two AIMs

Only one AIM saves. A second one started while the first runs starts fresh, never saves, and shows `another AIM is running; this one does not save its layout`. It logs to `aim-<pid>.log` and leaves the first AIM's log alone.

### Where it lives

The state is in `$XDG_STATE_HOME/aim` (`~/.local/state/aim` when unset or relative) on Linux and `~/Library/Application Support/aim` on macOS. `state.json` is the layout and `aim.log` is the log; a second AIM writes `aim-<pid>.log` ([Config](config.md) says how long each log stays).

`run/` holds each running AIM's hook files and a hard link to or a copy of its binary, which its agents' hooks run. So upgrading AIM while agents run keeps their hooks working, on the version that AIM started with; restart AIM to move them to the new one.
