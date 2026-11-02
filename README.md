<div align="center">

# AIM

**A terminal multiplexer for coding agents: watch them work, review their diffs, merge their pull requests**

<a href="https://github.com/mhersson/aim/actions/workflows/ci.yaml"><img src="https://github.com/mhersson/aim/actions/workflows/ci.yaml/badge.svg?branch=main" alt="CI"></a>
<a href="https://github.com/mhersson/aim/releases/latest"><img src="https://img.shields.io/github/v/release/mhersson/aim" alt="Latest release"></a>
<a href="LICENSE"><img src="https://img.shields.io/badge/license-MIT-blue" alt="License: MIT"></a>

</div>

AIM (Agents In-flight Multiplexer) hosts Claude Code agents in live terminal panes, with each agent's diff, its pull request and the CI checks beside it. You watch the agents work, review what they change while they change it, and push, open, check and merge their pull requests without leaving the terminal.

<div align="center">

<img src="https://github.com/user-attachments/assets/e601a7bf-da03-4bde-b2dd-efbc1b48a322" alt="AIM running two Claude Code agents in their own worktrees: the agents rail, the live diff in the review drawer by last turn and by whole branch, a pull request pushed, checked and merged with its README rendered from the file tree, the other agent's plan approved so it starts to code, and which-key opening" width="100%">

Watch it as a [video (MP4)](https://github.com/user-attachments/assets/26f3b30c-16e2-496d-ba32-31f5e8bf964f).

</div>

## Features

- **Agents in panes.** BSP, master-stack and scrolling layouts, tabs, and a git worktree per agent from the picker.
- **Agents rail.** Agents grouped as Blocked, Working, Idle or No hooks, with what it does now, its branch, its pull request and its diff stats.
- **Claude usage.** Your Claude subscription's 5-hour and weekly usage in the tray, in the context meter's colours.
- **Live review.** Turn and branch mode, unified or side by side, highlighted, with markdown rendered; the drawer follows the agent's edits from worktree to worktree.
- **Pull requests and CI.** Push, open a pull request from the compose box, read its conversation and checks with their logs, approve, and merge the reviewed head after a confirm.
- **Plans and markdown.** The agent's plan, any markdown file and a pull request's conversation open in a reading box, with pictures over the Kitty graphics protocol.
- **Keyboard first.** One leader key, which-key, a command palette, every key remappable, copy mode and mouse selection.
- **Picks up where you left off.** Tabs, layouts and each agent's Claude session come back on restart.
- **Themes.** Catppuccin Latte, Frappé, Macchiato and Mocha, switched from the palette while AIM runs.

## Screenshots

<table>
  <tr>
    <td width="50%">
      <img src="docs/images/review-checks.png" alt="AIM with the review drawer showing README.md rendered, and the checks box for pull request 1 listing one passed check, vet and test">
      <br>
      The review drawer on a pull request, with the checks box open over it: one check, vet and test, passed.
    </td>
    <td width="50%">
      <img src="docs/images/pr-list.png" alt="AIM with the Pull requests list open over the agents, showing pull request 1 with its checks passed, 1 of 1">
      <br>
      The pull request list on its Agents tab: pull request 1, Add -shout and -repeat flags, with 1/1 checks passed.
    </td>
  </tr>
  <tr>
    <td width="50%">
      <img src="docs/images/plan-overlay.png" alt="The plan overlay showing demo-agent-2's plan to add a -lang flag, with its context, files to change and verification steps">
      <br>
      The agent's plan in the plan overlay: demo-agent-2's plan to add a -lang flag.
    </td>
    <td width="50%">
      <img src="docs/images/which-key.png" alt="Which-key open over the agents, listing the leader keys by group: Panes, Layout, Tabs, Git and App">
      <br>
      Which-key after the leader, over the agents while demo-agent-2 codes.
    </td>
  </tr>
</table>

## Requirements

- Linux or macOS (there is no Windows build).
- A true-colour terminal. AIM is tested in Kitty and Ghostty; pictures need the Kitty graphics protocol.
- A Nerd Font, or `icons = false` in the config.
- Claude Code, with `claude` on the PATH of your login shell: agents start through `$SHELL -l -i -c`.
- On macOS with a second input source, the system takes `ctrl+space`, AIM's default leader key, for "Select the previous input source". Turn that shortcut off in System Settings, Keyboard, Keyboard Shortcuts, Input Sources, or set another leader, such as `leader_key = "ctrl+a"` under `[keys]` (see [the leader key](docs/guide/config.md#the-leader-key)).
- git 2.36 or newer.
- For pull requests and checks, a GitHub token, found the way `gh` finds one: `GH_TOKEN` or `GITHUB_TOKEN`, else the token `gh auth login` stored (see [what AIM needs](docs/guide/pull-requests.md#what-aim-needs)). Without one, everything but the GitHub features works.

## Install

### Homebrew

macOS and Linux:

```sh
brew install mhersson/formulas/aim
```

### Release binaries

Download `aim_<version>_<os>_<arch>.tar.gz` for Linux or macOS on amd64 or arm64 from the [latest release](https://github.com/mhersson/aim/releases/latest), check its SHA-256 against `aim_<version>_checksums.txt`, and put `aim` on your PATH. On macOS, a binary downloaded with a browser needs this once:

```sh
xattr -d com.apple.quarantine aim
```

The archive also holds `LICENSE`, `THIRD_PARTY.md`, this README and `config.example.toml`.

### Go

```sh
go install github.com/mhersson/aim/cmd/aim@latest
```

This needs Go 1.27.3 or newer. Check any install with `aim --version`.

## First run

The keys here are the defaults, and every key can be remapped (see [the keys page](docs/guide/keys.md)).

1. Run `aim` in a git repository.
2. Press `ctrl+space n`. The picker, titled "New agent", lists the repository's worktrees; its last row, `new worktree…`, makes a branch and its worktree and starts an agent there.
3. Give Claude a task.
4. Press `ctrl+space a` for the agents rail, which shows who is working and who is blocked on you.
5. Press `ctrl+space d` for the review drawer: `t` shows the agent's last turn, `b` the whole branch.
6. Press `ctrl+space g P` to push, after a confirm. `ctrl+space g o` opens the compose box and `ctrl+s` there creates the pull request. `ctrl+space g l` lists pull requests with their checks, and `M` there merges.

To see every leader key, press `ctrl+space` and wait for which-key.

## Keys

Everything starts with the leader, `ctrl+space`; every key can be remapped. [The keys page](docs/guide/keys.md) lists all of them.

| Keys | Does |
| --- | --- |
| `ctrl+space n` | New agent pane |
| `ctrl+space a` | Agents rail |
| `ctrl+space d` | Review drawer |
| `ctrl+space p` | Open the agent's plan |
| `ctrl+space g P` | Push branch |
| `ctrl+space g o` | Open a pull request |
| `ctrl+space g l` | Pull requests |
| `ctrl+space g m` | Merge pull request |
| `ctrl+space u` | Claude usage |
| `ctrl+space :` | Open command palette |
| `ctrl+space q` | Quit AIM |

## Documentation

The guide starts from what you want to do and gives the keys to do it:

1. [Agents side by side](docs/guide/agents.md)
2. [Layouts](docs/guide/layouts.md)
3. [Review an agent's work](docs/guide/review.md)
4. [Pull requests](docs/guide/pull-requests.md)
5. [Plans, markdown and conversations](docs/guide/reading.md)
6. [Copy, paste and the mouse](docs/guide/copy-paste-mouse.md)

Reference: [Keys](docs/guide/keys.md), [Configuration](docs/guide/config.md) and [`config.example.toml`](config.example.toml).

## Configuration

AIM needs no config. To change the theme, layouts, keys and more, copy `config.example.toml` to `~/.config/aim/config.toml` (`$XDG_CONFIG_HOME/aim/config.toml` when that is set; the path is the same on macOS) and uncomment what you change. See [Configuration](docs/guide/config.md).

## Limitations

- Claude Code is the only agent AIM hosts in v1.
- Linux and macOS only.
- A pull request from a fork never matches an agent's branch, and worktree cleanup refuses one with `#<n>'s branch is in another repository`.

## Contributing

[AGENTS.md](AGENTS.md) is the guide for people and coding agents alike. Run `make check-fast` before every commit.

## Acknowledgements

Terminal emulation comes from TUIOS's `tuios.dev/vt`. The tiling layouts, the line diff and the diff view are ported from [TUIOS](https://github.com/Gaurav-Gosain/tuios) (MIT); [THIRD_PARTY.md](THIRD_PARTY.md) lists everything copied.

## License

MIT, see [LICENSE](LICENSE).
