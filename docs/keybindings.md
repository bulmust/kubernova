# Keybindings and commands

The in-app help page (`?`) is the source of truth. It is generated from the
same registry that dispatches keys and commands, so it always matches the
running binary and your configuration. This document lists the built-in
defaults and is checked against the registry by a test
(`internal/registry`).

Keys only act while the table has focus. While the filter or command bar, a
form, or a popup is active, every key goes to that input.

## Shortcuts

Each shortcut has a configuration identifier. Change it under `keybindings:`
in `config.yaml`.

| Key | Identifier | Action |
| --- | --- | --- |
| `ctrl+r` | `refresh` | refresh |
| `/` | `filter` | filter text |
| `:` | `command` | commands |
| `?` | `help` | open help |
| `c` | `columns` | filter columns |
| `ctrl+p` | `options` | edit config.yaml |
| `ctrl+g` | `prometheus_config` | prometheus config |
| `ctrl+e` | `export` | export report |
| `ctrl+t` | `toggle_replicas` | toggle replicas |
| `space` | `hide_row` | hide row to end |
| `f` | `fullscreen` | fullscreen |
| `j` | `move_down` | move down |
| `k` | `move_up` | move up |
| `0` | `namespace_all` | all namespaces |
| `H` | `select_previous_column` | select left column |
| `L` | `select_next_column` | select right column |
| `<` | `sort_ascending` | sort ascending |
| `>` | `sort_descending` | sort descending |

`H` and `L` move the column selection without changing the sort. `<` and `>`
sort by the selected column, or by the first visible column when none is
selected.

## Fixed keys

These keys have a built-in meaning and cannot be rebound.

| Key | Action |
| --- | --- |
| `1-9` | select namespace shortcut shown in the header |
| `g / G` | jump to first / last row |
| `arrows, pgup, pgdn` | move in the table |
| `tab` | accept inline completion |
| `enter` | confirm |
| `esc` | close, cancel, or clear the filter |
| `ctrl+c` | quit |

## Binding rules

- Bindings must be unique. A duplicate is rejected with an error that names
  both shortcuts.
- A single character keeps its case: `H` is Shift+h and is a different key
  from `h`.
- Other bindings are lower case, for example `ctrl+p` or `space`.
- The fixed keys, including the digits `1` to `9`, cannot be used.
- If `config.yaml` is invalid, the previous configuration stays active and
  the error is shown. `:reload` applies edits without a restart.

## Commands

Open the command bar with `:`. Names and aliases complete inline; `tab`
accepts the suggestion. An exact name or alias wins over prefix completion.

| Command | Action |
| --- | --- |
| `:quit`, `:q` | quit |
| `:help` | open help |
| `:columns`, `:c` | choose visible columns |
| `:fullscreen`, `:fs` | toggle table-only fullscreen |
| `:option`, `:options` | edit config.yaml |
| `:ns`, `:namespace` `<namespace\|all>` | filter by namespace |
| `:prometheusurl`, `:promurl` `<url>` | set the Prometheus URL without connecting |
| `:promconfig`, `:promconf` | edit per-context Prometheus settings |
| `:connect`, `:prometheus` `[url]` | connect Prometheus and run resource queries |
| `:kubeconfigpath`, `:kubeapipath` `[path]` | choose the kubeconfig file |
| `:context`, `:ctx` `<context>` | switch Kubernetes context |
| `:refresh` | refresh Kubernetes data and connections |
| `:reload` | reload configuration and keybindings |
| `:export` `[json\|md\|all] [path]` | export the complete report and table |
| `:status` | show status message history |
