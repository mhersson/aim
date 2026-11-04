# Themes

AIM ships the four Catppuccin flavors: Latte, Frappé, Macchiato and Mocha. You can add your own, one file per theme, in a `themes` folder beside the config file. AIM reads them at start and offers them after the built-ins wherever a theme is chosen: the theme picker (`ctrl+space t`) with its preview, the palette's "Theme: ..." commands, `theme` in the config and the picker's `ctrl+s` save. This page covers where the files go, what a file holds, the errors at start and what AIM paints with each colour.

## Where the files go

The folder is `themes` beside the config file: `~/.config/aim/themes/`, or `$XDG_CONFIG_HOME/aim/themes/` when `XDG_CONFIG_HOME` is set to an absolute path (see [where the file lives](config.md#where-the-file-lives)). With `aim --config <path>`, it is the `themes` folder beside that file. A missing folder is not an error: AIM offers the built-ins only.

Each theme is a file called `<name>.toml`. The name is the file name without `.toml`, in lowercase letters, digits and hyphens, such as `my-mocha`. It is what `theme` in the config takes and what the theme's command is called, `theme.set.<name>`, which [`[keys.leader]`](config.md#remapping-keys) can bind like any other. A name a built-in has, such as `catppuccin-mocha`, is refused. AIM skips files that do not end in `.toml`, and folders. Themes of your own come after the built-ins, ordered by name.

AIM reads the folder once, at start. To add or change a theme, edit its file and start AIM again.

## The file

A theme file has three keys, all required, and no others:

- `title`: the name the picker and the palette show, such as `"My Mocha"`.
- `dark`: `true` for a dark theme, `false` for a light one.
- `[palette]`: the 26 colours, one per slot, each written `"#rrggbb"` (the hex digits in either case). The slots are Catppuccin's names; [what each slot paints](#what-each-slot-paints) says where AIM uses each.

This file, `~/.config/aim/themes/my-mocha.toml`, is Catppuccin Mocha under a name of its own, a starting point for a theme of yours:

```toml
title = "My Mocha"
dark = true

[palette]
rosewater = "#f5e0dc"
flamingo = "#f2cdcd"
pink = "#f5c2e7"
mauve = "#cba6f7"
red = "#f38ba8"
maroon = "#eba0ac"
peach = "#fab387"
yellow = "#f9e2af"
green = "#a6e3a1"
teal = "#94e2d5"
sky = "#89dceb"
sapphire = "#74c7ec"
blue = "#89b4fa"
lavender = "#b4befe"
text = "#cdd6f4"
subtext1 = "#bac2de"
subtext0 = "#a6adc8"
overlay2 = "#9399b2"
overlay1 = "#7f849c"
overlay0 = "#6c7086"
surface2 = "#585b70"
surface1 = "#45475a"
surface0 = "#313244"
base = "#1e1e2e"
mantle = "#181825"
crust = "#11111b"
```

With it in place, `theme = "my-mocha"` in the config starts AIM in it.

## Errors at start

A theme file is as strict as the config. Invalid TOML, an unknown key, a missing key, a value of the wrong type, a colour that is not `#rrggbb` and a bad file name all stop AIM before it takes over the terminal. AIM prints one line that names the file and the problem, to the terminal, and exits with status 1. Every unknown key is listed, and so is every missing one; of the colours that are not `#rrggbb`, the first is named. The line starts with `aim: theme <path>:`. For example:

```text
aim: theme /home/you/.config/aim/themes/my-mocha.toml: unknown key palette.purple
aim: theme /home/you/.config/aim/themes/my-mocha.toml: missing key dark, palette.maroon
aim: theme /home/you/.config/aim/themes/my-mocha.toml: palette.red: "#f38ba" is not a #rrggbb colour
aim: theme /home/you/.config/aim/themes/My-Mocha.toml: name "My-Mocha": want lowercase letters, digits and hyphens
aim: theme /home/you/.config/aim/themes/catppuccin-mocha.toml: name "catppuccin-mocha" is a built-in theme
```

An invalid TOML or wrong-type error adds the line number and the key, as the config's does, such as `line 2: dark: cannot decode TOML string ...` for `dark = "yes"`.

A theme the config's `theme` names is checked after the folder is read, so removing its file while the config still names it stops the start with the config's `unknown theme` error, which lists the themes AIM found.

## What each slot paints

The 26 slots are Catppuccin's. The shades run from `crust` through `mantle`, `base`, `surface0` to `surface2`, `overlay0` to `overlay2` and `subtext0` and `subtext1` to `text`: `base` is the background, `mantle` and `crust` are a step and two steps darker, and each shade after `base` is a step nearer the text. The other fourteen are the accent colours. When you port a palette, keep that order of shades, and pick each accent by the role in the table rather than by its name.

The "In code" column is the syntax colours, in the code blocks of plans, markdown and pull request conversations and in the review drawer's diffs; where the two differ, the cell says which. The CI log colours are the ANSI colours a check's log asks for.

| Slot | AIM paints with it | In code |
| --- | --- | --- |
| `rosewater` | | variables (code blocks) |
| `flamingo` | branch names and the git icon: the rail, the worktree picker, the merge box and a pull request's header | |
| `pink` | magenta in CI logs | preprocessor lines (diffs) |
| `mauve` | the accent: the active tab, the pickers' prompt and marker, markdown headings and bullets, the review cursor's tint; agent panes' borders and labels | keywords, tags |
| `red` | errors, blocked agents, removed lines, a usage meter from 90 %; red in CI logs | declarations and types (code blocks), errors |
| `maroon` | nothing yet | |
| `peach` | search matches, working agents, inline code in markdown | numbers, constants, namespaces, properties, headings |
| `yellow` | warnings, a usage meter from 70 %; yellow in CI logs | classes (code blocks), types (diffs) |
| `green` | success, idle agents, added lines, a done task's mark; green in CI logs | strings |
| `teal` | the review drawer's title, and its edge while it has the keys; the usage meter, the tray's load sparkline; cyan in CI logs | namespace keywords, regular expressions (code blocks) |
| `sky` | | built-ins, operators, labels |
| `sapphire` | nothing yet | |
| `blue` | key hints, links, information, shell panes' borders and labels; blue in CI logs | functions, attributes, escapes |
| `lavender` | the borders of the palette and the other pop-ups, the tray's clock | |
| `text` | text | plain code |
| `subtext1` | white in CI logs | |
| `subtext0` | dim text, such as a done task's text, quotes and pop-up notes; bright white in CI logs | |
| `overlay2` | | punctuation (diffs) |
| `overlay1` | secondary text, such as tab numbers | line numbers (code blocks), the no-newline-at-end mark (diffs) |
| `overlay0` | the faintest text, such as hints | comments |
| `surface2` | bright black in CI logs | shebang lines (code blocks) |
| `surface1` | borders and rules, the selected row's ground; black in CI logs | |
| `surface0` | the ground of the tabs, the tray's modules, inline code and the drawer's cursor line | the ground of added and removed lines (code blocks) |
| `base` | the background and the tab bar | |
| `mantle` | the ground of the palette, the pickers, the other pop-ups and code blocks | |
| `crust` | text on the accent, such as the active tab's | |
