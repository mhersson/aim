# Keys

This page is generated from AIM's keymap by `TestKeysPage` in `internal/ui`. Do not edit it by hand; regenerate it with `go test ./internal/ui -run TestKeysPage -update`.

These are the defaults. `[keys]` in the config changes them (see [Remapping keys](config.md#remapping-keys)). The Command column is what a `[keys.*]` entry names.

## The leader

The leader key is `ctrl+space` (`leader_key`). Pressing it and waiting 300 ms (`which_key_delay`) opens the which-key popup. A key written as two keys, such as `g l`, is two keys pressed in turn after the leader.

On macOS, once a second input source is on, the system takes `ctrl+space` for "Select the previous input source", so AIM never sees it. Turn that shortcut off in System Settings, Keyboard, Keyboard Shortcuts, Input Sources, or set another leader, such as `leader_key = "ctrl+a"` under `[keys]` (see [The leader key](config.md#the-leader-key)).

## After the leader

Keys pressed after the leader key. Remap them under `[keys.leader]`.

### Panes

| Key | Command | Title |
| --- | --- | --- |
| `n` | `agent.new` | New agent pane |
| `a` | `rail.toggle` | Agents rail |
| `d` | `review.toggle` | Review drawer |
| `p` | `plan.open` | Open the agent's plan |
| `s` | `pane.split.down` | Split: shell below |
| `v` | `pane.split.right` | Split: shell right |
| `h` | `pane.focus.left` | Focus left |
| `j` | `pane.focus.down` | Focus down |
| `k` | `pane.focus.up` | Focus up |
| `l` | `pane.focus.right` | Focus right |
| `H` | `pane.resize.left` | Resize left |
| `J` | `pane.resize.down` | Resize down |
| `K` | `pane.resize.up` | Resize up |
| `L` | `pane.resize.right` | Resize right |
| `ctrl+h` | `pane.swap.left` | Swap left |
| `ctrl+j` | `pane.swap.down` | Swap down |
| `ctrl+k` | `pane.swap.up` | Swap up |
| `ctrl+l` | `pane.swap.right` | Swap right |
| `z` | `pane.zoom` | Zoom pane |
| `x` | `pane.close` | Close pane |
| `y` | `copy.selection` | Copy selection |
| `[` | `copy.mode` | Copy mode |

### Layout

| Key | Command | Title |
| --- | --- | --- |
| `space` | `layout.cycle` | Cycle layout |
| `; h` | `pane.preselect.left` | Preselect left |
| `; j` | `pane.preselect.down` | Preselect down |
| `; k` | `pane.preselect.up` | Preselect up |
| `; l` | `pane.preselect.right` | Preselect right |
| `; esc` | `pane.preselect.clear` | Clear preselection |
| `r` | `layout.rotate` | Rotate split |
| `=` | `layout.equalize` | Equalize |
| `enter` | `layout.promote` | Promote to master |

### Tabs

| Key | Command | Title |
| --- | --- | --- |
| `c` | `tab.new` | New tab |
| `,` | `tab.rename` | Rename tab |
| `X` | `tab.close` | Close tab |
| `tab` | `tab.next` | Next tab |
| `shift+tab` | `tab.prev` | Previous tab |
| `1` | `tab.jump.1` | Jump to tab 1 |
| `2` | `tab.jump.2` | Jump to tab 2 |
| `3` | `tab.jump.3` | Jump to tab 3 |
| `4` | `tab.jump.4` | Jump to tab 4 |
| `5` | `tab.jump.5` | Jump to tab 5 |
| `6` | `tab.jump.6` | Jump to tab 6 |
| `7` | `tab.jump.7` | Jump to tab 7 |
| `8` | `tab.jump.8` | Jump to tab 8 |
| `9` | `tab.jump.9` | Jump to tab 9 |
| `!` | `pane.break` | Break pane into new tab |
| `> 1` | `pane.move.tab.1` | Move pane to tab 1 |
| `. 1` | `pane.send.tab.1` | Send pane to tab 1 |
| `> 2` | `pane.move.tab.2` | Move pane to tab 2 |
| `. 2` | `pane.send.tab.2` | Send pane to tab 2 |
| `> 3` | `pane.move.tab.3` | Move pane to tab 3 |
| `. 3` | `pane.send.tab.3` | Send pane to tab 3 |
| `> 4` | `pane.move.tab.4` | Move pane to tab 4 |
| `. 4` | `pane.send.tab.4` | Send pane to tab 4 |
| `> 5` | `pane.move.tab.5` | Move pane to tab 5 |
| `. 5` | `pane.send.tab.5` | Send pane to tab 5 |
| `> 6` | `pane.move.tab.6` | Move pane to tab 6 |
| `. 6` | `pane.send.tab.6` | Send pane to tab 6 |
| `> 7` | `pane.move.tab.7` | Move pane to tab 7 |
| `. 7` | `pane.send.tab.7` | Send pane to tab 7 |
| `> 8` | `pane.move.tab.8` | Move pane to tab 8 |
| `. 8` | `pane.send.tab.8` | Send pane to tab 8 |
| `> 9` | `pane.move.tab.9` | Move pane to tab 9 |
| `. 9` | `pane.send.tab.9` | Send pane to tab 9 |

### Git

| Key | Command | Title |
| --- | --- | --- |
| `g l` | `pr.list` | Pull requests |
| `g P` | `git.push` | Push branch |
| `g o` | `pr.open` | Open a pull request |
| `g m` | `pr.merge` | Merge pull request |

### App

| Key | Command | Title |
| --- | --- | --- |
| `:` | `palette.open` | Open command palette |
| `t` | `theme.pick` | Pick theme |
| `u` | `usage.toggle` | Claude usage |
| `q` | `app.quit` | Quit AIM |

## Review drawer

The table is `[keys.drawer]`. These keys apply while the review drawer has the keyboard.

| Key | Command | Title |
| --- | --- | --- |
| `j` | `review.line.next` | Review: next line |
| `down` | `review.line.next` | Review: next line |
| `k` | `review.line.prev` | Review: previous line |
| `up` | `review.line.prev` | Review: previous line |
| `]` | `review.hunk.next` | Review: next hunk |
| `[` | `review.hunk.prev` | Review: previous hunk |
| `}` | `review.file.next` | Review: next file |
| `{` | `review.file.prev` | Review: previous file |
| `ctrl+d` | `review.page.down` | Review: half a page down |
| `ctrl+u` | `review.page.up` | Review: half a page up |
| `g` | `review.top` | Review: top |
| `G` | `review.bottom` | Review: bottom |
| `tab` | `review.tree` | Review: file tree |
| `f` | `review.full` | Review: full file |
| `m` | `review.markdown` | Review: rendered or source |
| `u` | `review.split` | Review: unified or side by side |
| `enter` | `review.fold` | Review: fold or show removed lines |
| `space` | `review.mark` | Review: mark the hunk reviewed |
| `R` | `review.show.reviewed` | Review: show reviewed hunks too |
| `b` | `review.mode.branch` | Review: branch mode |
| `t` | `review.mode.turn` | Review: turn mode |
| `p` | `review.mode.pr` | Review: pull request |
| `i` | `review.conversation` | Review: conversation |
| `c` | `review.checks` | Review: checks |
| `a` | `review.approve` | Review: approve |
| `M` | `review.merge` | Review: merge |
| `O` | `review.browser` | Review: open in browser |
| `,` | `review.turn.prev` | Review: previous turn |
| `.` | `review.turn.next` | Review: next turn |
| `/` | `review.search` | Review: search |
| `y` | `review.copy` | Review: copy the selection |
| `z` | `review.zoom` | Review: zoom |
| `w` | `review.worktrees` | Review: worktrees |
| `e` | `review.edit` | Review: open in the editor |
| `r` | `review.refresh` | Review: refresh |
| `esc` | `review.back` | Review: back, else close the drawer |

While the file tree has the keyboard, only these commands work: `review.line.next`, `review.line.prev`, `review.tree`, `review.fold`, `review.zoom`, `review.back`.

## Agents rail

The table is `[keys.rail]`. These keys apply while the agents rail has the keyboard.

| Key | Command | Title |
| --- | --- | --- |
| `j` | `rail.next` | Agents rail: next card |
| `down` | `rail.next` | Agents rail: next card |
| `k` | `rail.prev` | Agents rail: previous card |
| `up` | `rail.prev` | Agents rail: previous card |
| `/` | `rail.search` | Agents rail: search |
| `enter` | `rail.jump` | Agents rail: jump to the agent |
| `p` | `rail.plan` | Agents rail: open the agent's plan |
| `esc` | `rail.back` | Agents rail: end the search or go back to the panes |

## PR list

The table is `[keys.pr_list]`. These keys apply while the pull request list is open.

| Key | Command | Title |
| --- | --- | --- |
| `j` | `prlist.next` | Pull requests: next |
| `down` | `prlist.next` | Pull requests: next |
| `k` | `prlist.prev` | Pull requests: previous |
| `up` | `prlist.prev` | Pull requests: previous |
| `tab` | `prlist.tab.next` | Pull requests: next tab |
| `shift+tab` | `prlist.tab.prev` | Pull requests: previous tab |
| `1` | `prlist.tab.agents` | Pull requests: agents tab |
| `2` | `prlist.tab.requested` | Pull requests: review requested tab |
| `3` | `prlist.tab.all` | Pull requests: all open tab |
| `enter` | `prlist.show` | Pull requests: show the diff |
| `i` | `prlist.conversation` | Pull requests: show the conversation |
| `c` | `prlist.checks` | Pull requests: show the checks |
| `a` | `prlist.approve` | Pull requests: approve |
| `M` | `prlist.merge` | Pull requests: merge |
| `O` | `prlist.browser` | Pull requests: open in the browser |
| `r` | `prlist.refresh` | Pull requests: refresh |
| `esc` | `prlist.close` | Pull requests: close |

## Checks overlay

The table is `[keys.checks]`. These keys apply while the checks overlay is open.

| Key | Command | Title |
| --- | --- | --- |
| `j` | `checks.next` | Checks: next check or line |
| `down` | `checks.next` | Checks: next check or line |
| `k` | `checks.prev` | Checks: previous check or line |
| `up` | `checks.prev` | Checks: previous check or line |
| `l` | `checks.log` | Checks: show the job's log |
| `O` | `checks.browser` | Checks: open in the browser |
| `enter` | `checks.fold` | Checks: open or close the group, or open the link |
| `ctrl+d` | `checks.page.down` | Checks: half a page down |
| `ctrl+u` | `checks.page.up` | Checks: half a page up |
| `g` | `checks.top` | Checks: first line |
| `G` | `checks.bottom` | Checks: last line |
| `]` | `checks.error.next` | Checks: next error |
| `[` | `checks.error.prev` | Checks: previous error |
| `r` | `checks.refresh` | Checks: refresh |
| `esc` | `checks.close` | Checks: close |

## Reading overlays

The table is `[keys.reader]`. These keys apply in the plan overlay, a markdown file overlay and the conversation.

| Key | Command | Title |
| --- | --- | --- |
| `j` | `reader.line.next` | Reader: next line |
| `down` | `reader.line.next` | Reader: next line |
| `k` | `reader.line.prev` | Reader: previous line |
| `up` | `reader.line.prev` | Reader: previous line |
| `ctrl+d` | `reader.page.down` | Reader: half a page down |
| `ctrl+u` | `reader.page.up` | Reader: half a page up |
| `ctrl+f` | `reader.screen.down` | Reader: a screen down |
| `pgdown` | `reader.screen.down` | Reader: a screen down |
| `ctrl+b` | `reader.screen.up` | Reader: a screen up |
| `pgup` | `reader.screen.up` | Reader: a screen up |
| `g` | `reader.top` | Reader: first line |
| `G` | `reader.bottom` | Reader: last line |
| `]` | `reader.section.next` | Reader: next heading or item |
| `[` | `reader.section.prev` | Reader: previous heading or item |
| `enter` | `reader.open` | Reader: open the fold or follow the link |
| `/` | `reader.search` | Reader: search |
| `n` | `reader.match.next` | Reader: next match |
| `N` | `reader.match.prev` | Reader: previous match |
| `y` | `reader.copy` | Reader: copy the selection |
| `e` | `reader.edit` | Reader: open in the editor |
| `r` | `reader.refresh` | Reader: read again |
| `esc` | `reader.close` | Reader: close the page or the overlay |

## Merge box

The table is `[keys.merge]`. These keys apply while the merge box is open.

| Key | Command | Title |
| --- | --- | --- |
| `s` | `merge.method.squash` | Merge: squash |
| `m` | `merge.method.merge` | Merge: merge commit |
| `r` | `merge.method.rebase` | Merge: rebase |
| `d` | `merge.delete` | Merge: delete the branch or keep it |
| `enter` | `merge.run` | Merge: merge |
| `esc` | `merge.cancel` | Merge: cancel |

## Compose box

The table is `[keys.compose]`. These keys apply while the compose box is open.

| Key | Command | Title |
| --- | --- | --- |
| `tab` | `compose.field.next` | Compose: next field |
| `shift+tab` | `compose.field.prev` | Compose: previous field |
| `ctrl+s` | `compose.create` | Compose: create the pull request |
| `ctrl+e` | `compose.editor` | Compose: body in the editor |
| `ctrl+p` | `compose.preview` | Compose: preview or write |
| `esc` | `compose.cancel` | Compose: cancel |

## Commands without a default key

These commands are in the command palette. Bind one under a table that takes it.

| Command | Title | Bind under |
| --- | --- | --- |
| `layout.set.bsp` | Layout: BSP | `[keys.leader]` |
| `layout.set.master-stack` | Layout: master-stack | `[keys.leader]` |
| `layout.set.scrolling` | Layout: scrolling | `[keys.leader]` |
| `md.open` | Open a markdown file | `[keys.leader]` |
| `worktree.cleanup` | Clean up a merged worktree | `[keys.leader]` |
| `theme.set.catppuccin-latte` | Theme: Catppuccin Latte | `[keys.leader]` |
| `theme.set.catppuccin-frappe` | Theme: Catppuccin Frappé | `[keys.leader]` |
| `theme.set.catppuccin-macchiato` | Theme: Catppuccin Macchiato | `[keys.leader]` |
| `theme.set.catppuccin-mocha` | Theme: Catppuccin Mocha | `[keys.leader]` |

## Keys you cannot remap

These keys are fixed. Each is described where it is used.

- Copy mode's keys: [copy mode](copy-paste-mouse.md#copy-mode).
- The leader pressed twice, which sends it to the pane, and `esc` in a two-key sequence: [the leader key](agents.md#the-leader-key).
- Renaming a tab: [tabs](agents.md#tabs).
- The command palette and the theme picker: [the command palette and themes](agents.md#the-command-palette-and-themes).
- The worktree picker: [start an agent](agents.md#start-an-agent).
- Typing in the rail's search: [the agents rail](agents.md#the-agents-rail).
- Confirm prompts: [close panes and tabs](agents.md#close-panes-and-tabs) and [push](pull-requests.md#push).
- The drawer's search and worktree list: [search](review.md#search) and [the worktree list](review.md#the-worktree-list).
- The reading overlays' search and link chooser: [search](reading.md#search) and [links and pictures](reading.md#links-and-pictures).
- The compose box's fields: [open a pull request](pull-requests.md#open-a-pull-request).
- The Claude usage details: [Claude usage](agents.md#claude-usage).
- The mouse: [the mouse](copy-paste-mouse.md#the-mouse).
