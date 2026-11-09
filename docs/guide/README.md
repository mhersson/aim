# AIM guide

This guide is for Claude Code users who are new to AIM. AIM runs Claude Code agents in live terminal panes and puts their diffs, pull requests, checks and merges beside them. The pages start from what you want to do and give the keys to do it.

AIM's own keys sit behind a leader key, `ctrl+space` by default: press it, then the key of a command, as in `ctrl+space n`. The keys in the guide are the defaults. A changed `leader_key` or `[keys]` table changes them; see [Keys](keys.md) for the full list and [Configuration](config.md#remapping-keys) for how to remap.

## Pages, in reading order

1. [Agents side by side](agents.md): start agents, move between panes and tabs, the rail, notifications, the command palette, closing things and what a restart restores.
2. [Layouts](layouts.md): the three layout modes of a tab, switching between them, the layout commands, and moving panes between tabs.
3. [Review an agent's work](review.md): the review drawer, its two modes, reading and marking the diff, and following the agent from worktree to worktree.
4. [Pull requests](pull-requests.md): push, open a pull request, review it in the drawer, the checks, approve, merge and clean up.
5. [Plans, markdown and conversations](reading.md): the reading box for an agent's plan, a markdown file and a pull request's conversation, with its keys and search.
6. [Copy, paste and the mouse](copy-paste-mouse.md): copy mode, where a copy ends up, and what the mouse does.

## Reference

- [Keys](keys.md): every default key and command, generated from the keymap.
- [Configuration](config.md): where the config file lives, errors at start and the rules for remapping keys.
- [Themes](themes.md): themes of your own: where the files go, what a file holds and what AIM paints with each colour.
- [`config.example.toml`](../../config.example.toml): every setting with its meaning, default and allowed values.
