# Configuration

AIM runs without a config file. A file changes what you want changed: the theme, the layout a new tab starts in, the mouse and clipboard behaviour, the shell integration, the diff limits, the desktop notifications and the keys. This page covers where the file lives, how to start one, what happens when it has a mistake, and the rules for remapping keys. The settings themselves, each with its meaning, default and allowed values, are in [`config.example.toml`](../../config.example.toml).

## Where the file lives

AIM reads `$XDG_CONFIG_HOME/aim/config.toml` when `XDG_CONFIG_HOME` is set to an absolute path, else `~/.config/aim/config.toml`. A relative `XDG_CONFIG_HOME` is ignored. This is the same on Linux and macOS: on macOS the file is not under `~/Library`.

To use another file, start AIM with `aim --config <path>`. That file must exist; a missing one stops the start with `aim: config: stat <path>: no such file or directory`. A missing file at the default path is not an error: AIM runs with the defaults.

AIM reads the file once, at start. To change a setting, edit the file and start AIM again.

## Start from the example

[`config.example.toml`](../../config.example.toml) lists every setting, commented out at its default. To start a config, copy it to the path above and uncomment the lines you want to change by deleting the leading `# `. The table headers such as `[layout]` are live, so one uncommented line lands in the right table. With every line still commented, the copy changes nothing.

```sh
mkdir -p ~/.config/aim
cp config.example.toml ~/.config/aim/config.toml
```

The `theme` setting decides the theme at each start. A theme you pick while AIM runs lasts for that session only, unless you save it with `ctrl+s` in the theme picker: that sets `theme` in the config file AIM started with and leaves the rest of it as it was (see [the command palette and themes](agents.md#the-command-palette-and-themes)). It is the only time AIM writes the file. Themes of your own go in a `themes` folder beside the config file; see [Themes](themes.md).

## Errors at start

The file is strict. Invalid TOML, an unknown key, a value of the wrong type and a value that is out of range or not one of the allowed names all stop AIM before it takes over the terminal. AIM prints one line that names the file and the problem it found (the first one, except that every unknown key is listed: `unknown key a, b`), to the terminal, and exits with status 1. The line starts with `aim: config <path>:`. For example:

```text
aim: config /home/you/.config/aim/config.toml: unknown key theem
aim: config /home/you/.config/aim/config.toml: layout.master_ratio: 95 is out of range 10-90
aim: config /home/you/.config/aim/config.toml: unknown theme "nope" (known: catppuccin-latte, catppuccin-frappe, catppuccin-macchiato, catppuccin-mocha)
```

An invalid TOML or wrong-type error adds the line number: `line 1: theme: cannot decode TOML integer into struct field core.Config.Theme of type string`. A mistake in the `[keys]` section names the key; the examples in the next section show them.

A theme file of your own with a mistake stops the start the same way, with a line that starts `aim: theme <path>:` (see [Themes](themes.md#errors-at-start)).

Fix the line and start again.

When something goes wrong after the start, look in the log. It is `aim.log` in AIM's state directory, `$XDG_STATE_HOME/aim` (`~/.local/state/aim` when that is unset or relative) on Linux and `~/Library/Application Support/aim` on macOS. Each start keeps the previous run's log as `aim.log.1`, replacing any older one. A [second AIM](agents.md#two-aims) leaves both alone and appends to `aim-<pid>.log`, starting each run with a line that names the first AIM's log. An AIM that saves removes such a log as it starts, once the AIM that wrote it has ended and the log has not been written for 7 days. A config error is printed instead of logged, because it stops AIM before the log opens.

## Remapping keys

The `[keys]` table changes the leader key, the delay before the which-key popup and the keys of each view. [Keys](keys.md) lists the default bindings and the command each runs; this section holds the rules for changing them.

### Entries

An entry is `"<key>" = "<command>"` inside the table of the view whose key it is. Quote the key: `+` and a space are not allowed in a bare TOML key. The command ids are the ones in the Command column of [Keys](keys.md), and the commands with no default key are listed under [Commands without a default key](keys.md#commands-without-a-default-key).

The tables are `[keys.leader]` (the keys after the leader key), `[keys.drawer]`, `[keys.rail]`, `[keys.pr_list]`, `[keys.checks]`, `[keys.reader]`, `[keys.merge]` and `[keys.compose]`. Each takes its own commands:

- `[keys.leader]` takes the commands that are in the command palette, such as `pr.list`, `pane.zoom` or `theme.set.catppuccin-latte`.
- `[keys.drawer]` takes commands whose id starts with `review.`.
- The other six tables take the commands of their own view: `rail.next` and the other commands scoped to the agents rail under `[keys.rail]`, ids starting `prlist.` under `[keys.pr_list]`, `checks.` under `[keys.checks]`, `reader.` under `[keys.reader]`, `merge.` under `[keys.merge]` and `compose.` under `[keys.compose]`. A command of one view is not in the command palette, and `[keys.leader]` refuses it. `rail.toggle` is a leader command despite its prefix, so it belongs under `[keys.leader]` and `[keys.rail]` refuses it.

A command from the wrong table is an error that names the table:

```text
aim: config <path>: keys.drawer: "o" = "pr.list": not a drawer command
aim: config <path>: keys.leader: "w" = "rail.next": a rail command
aim: config <path>: keys.leader: "w" = "pr.lst": no such command
```

To free a key, bind it to `"none"`. This is how the example moves quit from `q` to `Q`:

```toml
[keys.leader]
"Q" = "app.quit"
"q" = "none"
```

A key you bind that is a default of another command takes that command's place. A key you bind to a command that already has a default key goes before the default, so it is the command's first key; the default still works. A command with no default key left goes at the end of its group in the which-key popup under `[keys.leader]`, and at the end of the table in the others. Hints show a command's first key. When you bind several keys to one command, they keep the order of the file.

### Key syntax

A key is a Bubble Tea key string. Modifiers come first, in this order: `ctrl+`, `alt+`, `shift+`, `meta+`, `hyper+`, `super+`. `ctrl+alt+x` is a key; `alt+ctrl+x` is not and stops the start with `is not a key name`. After the modifiers comes one of:

- a single printable character, such as `w`, `Q`, `[` or `?`;
- a name: `enter`, `tab`, `backspace`, `esc`, `space`, `up`, `down`, `left`, `right`, `pgup`, `pgdown`, `home`, `end`, `insert`, `delete`, `begin`, `find`, `select`, `menu`, `pause` and `printscreen`;
- a function key, `f1` to `f63`.

Write a shifted letter as the character, not with `shift+`. `"shift+a"` is an error, and so is `shift+` before any character:

```text
aim: config <path>: keys.leader: "shift+a": write the shifted character, such as "A"
```

A name takes `shift+`: `shift+tab` is a key. A space bar is `space`, not a space character.

### Two-key sequences

Only `[keys.leader]` takes a sequence of two keys, written with a space: `"g l"` is `g` then `l` after the leader key. The other tables refuse one, and so does a sequence of three:

```text
aim: config <path>: keys.pr_list: "g l": only keys.leader takes two-key sequences
aim: config <path>: keys.leader: "a b c": a sequence has at most two keys
```

A key cannot be both a command and the start of a sequence: the default `g` starts several sequences, so binding `"g"` to a command is refused with the first of them, `keys.leader: "g" is bound and starts "g l"`.

### The leader key

`leader_key` takes the same syntax and needs a modifier other than `shift` alone, or must be a function key:

```text
aim: config <path>: keys.leader_key: "a" needs a modifier, such as ctrl+a, or a function key
```

It cannot also be the first key of an entry under `[keys.leader]`:

```text
aim: config <path>: keys.leader_key: "ctrl+space" is also bound under keys.leader
```

`[keys.drawer]`, `[keys.rail]` and `[keys.compose]` refuse an entry whose key is the leader key, because the leader is routed before the drawer and the rail and still works in the compose box while a pull request is being created:

```text
aim: config <path>: keys.drawer: "ctrl+space" is the leader key
```

The other four tables, `[keys.pr_list]`, `[keys.checks]`, `[keys.reader]` and `[keys.merge]`, take the leader key as an ordinary key.

On macOS, once a second input source is on, the system takes `ctrl+space` for "Select the previous input source", so the default leader never reaches AIM. Turn that shortcut off in System Settings, Keyboard, Keyboard Shortcuts, Input Sources, or set another leader:

```toml
[keys]
leader_key = "ctrl+a"
```

### Keep a way to close a view

The leader closes the drawer and the rail. The pull request list, the checks overlay, the reading overlays, the image viewer, the merge box and the compose box are closed by their own command, so after your entries are laid over the defaults, a key must remain for that command: `prlist.close` under `[keys.pr_list]`, `checks.close` under `[keys.checks]`, `reader.close` under `[keys.reader]`, `images.close` under `[keys.images]`, `merge.cancel` under `[keys.merge]` and `compose.cancel` under `[keys.compose]`. Binding the default `esc` to `"none"` without another key for the command is refused:

```text
aim: config <path>: keys.pr_list: nothing is bound to prlist.close, so the PR list could not close
```

### The compose box

The compose box's title and body fields take typing. `[keys.compose]` refuses a key they use: a printable character, or one of `space`, `enter`, `backspace`, `delete`, `up`, `down`, `left`, `right`, `home`, `end`, `pgup` and `pgdown`, with no modifier or with `shift` alone.

```text
aim: config <path>: keys.compose: "s" is a key the title and body take
```

A key with another modifier, such as the default `ctrl+s`, is fine. A `"none"` entry is not checked. The rest of the compose box's typing, and typing in the other views' searches and boxes, cannot be remapped; see [Keys you cannot remap](keys.md#keys-you-cannot-remap).
