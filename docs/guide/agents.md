# Agents side by side

AIM runs Claude Code in panes next to each other, in tabs, with a rail that lists every agent and a layout that comes back after a restart. This page covers starting agents, moving between panes and tabs, the rail, the command palette, closing things and what a restart restores.

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
- On success the notice is `created <path>`.
- For a worktree inside the repository's `.worktrees` folder, the first one in a repository adds `/.worktrees/` to the repository's `.git/info/exclude`, and the page lists tools that need their own ignore entry, such as an editor's file watcher.
- `root` under `[worktrees]` in [`config.example.toml`](../../config.example.toml) sets where new worktrees go.

### Shells beside the agent

- `ctrl+space s` opens a shell below the focused pane, `ctrl+space v` one to its right. Both start in the focused pane's working directory and take focus. That is where a BSP tab puts them; the other layouts place a new pane by their own rule, see [Layouts](layouts.md).
- Run `claude` in a shell and the pane becomes an agent pane; when Claude exits in an agent pane, it becomes a shell again. The rail lists agent panes only.
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

With a Claude subscription the tray shows how much of its limits you have used: `5h` for the 5-hour window and `7d` for the weekly one, each with its percent. With icons on, the module starts with a gauge icon. The figures come from the agents' status lines, so they appear after an agent's first reply and are the latest any agent reported; a status line that repeats an older, lower figure for the same window does not pull it down.

A plan with only a weekly limit shows only `7d`. Without limits (API billing, Team, Enterprise, Bedrock and Vertex, or an older Claude Code) nothing shows.

A percent takes the theme's warning colour from 70 % and its error colour, bold, from 90 %, as the context meter does. A window leaves the tray when it resets, until an agent reports it again. On a narrow terminal the module leaves the bar right after the load module.

Click the module or press `ctrl+space u` (command "Claude usage") for the details: each window's bar and percent, when it resets (`resets in 2d 4h (Wed 14:00)`), and how long ago the figures last changed. `esc`, `ctrl+space u` again or a click outside them closes them. While they are open the leader works as usual and every other key and a paste are dropped. With no figures yet, `ctrl+space u` says `no Claude usage yet`. When the module has left the bar, the details open at the right edge.

## The command palette and themes

Every action is a command, and the palette runs any that is not specific to one view: press `ctrl+space :`, type part of the name, move with `up` and `down` (or `ctrl+n`, `ctrl+p`, `tab`, `shift+tab`), and press `enter` to run it. `esc` closes it. Each row shows the leader key of its command, if it has one. Commands without a key, such as "Layout: BSP", are listed in [Keys](keys.md#commands-without-a-default-key).

To change the theme, press `ctrl+space t`. The list previews each theme as you move; `enter` applies it, and `esc` closes the picker and keeps the theme you had. The same themes are in the palette as "Theme: ...".

A theme you pick lasts until AIM quits; the next start uses `theme` from the config. Set it in [`config.example.toml`](../../config.example.toml) to change the theme AIM starts with.

These keys are fixed: typing, the movement keys above, `enter` and `esc`.

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

Only one AIM saves. A second one started while the first runs starts fresh, never saves, and shows `another AIM is running; this one does not save its layout`.

### Where it lives

The state is in `$XDG_STATE_HOME/aim` (`~/.local/state/aim` when unset or relative) on Linux and `~/Library/Application Support/aim` on macOS. `state.json` is the layout and `aim.log` is the log.
