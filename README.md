# kubernova

[![CI](https://github.com/bulmust/kubernova/actions/workflows/ci.yml/badge.svg)](https://github.com/bulmust/kubernova/actions/workflows/ci.yml)
[![Go version](https://img.shields.io/github/go-mod/go-version/bulmust/kubernova)](go.mod)

A terminal UI for browsing the containers of a Kubernetes cluster and
checking their resource requests and limits. It uses a table view, vim-style
keys, a command bar, skins and a YAML configuration file, in the style of
k9s. With an optional Prometheus server it can also suggest new CPU and
memory requests and limits from observed usage.

<!-- Screenshot: add docs/screenshot.png and reference it here. -->
*Screenshot placeholder.*

## Install

Requirements:

- Go 1.26 or newer, to build from source.
- A kubeconfig with access to a cluster. Kubernova always uses the Kubernetes
  API; there is no offline mode.
- Optional: a Prometheus server for usage percentiles and suggestions.

```bash
git clone https://github.com/bulmust/kubernova.git
cd kubernova
go build -o kubernova .
./kubernova
```

To install into `GOBIN` (default `$(go env GOPATH)/bin`), run
`go install .` from the repository root and make sure that directory is on
your `PATH`.

## Quick start

Run `kubernova` in a shell where `kubectl` works. It reads `KUBECONFIG`, or
`~/.kube/config`, and lists every container in every namespace, sorted by
namespace and then pod.

- Press `?` for the help page.
- Press `/` and type to filter rows; `esc` clears the filter.
- Press `c` to choose columns, `H` and `L` to select a column, and `<` or `>`
  to sort by it.
- Press `0` for all namespaces, or `1` to `9` for the namespaces in the
  header. `:ns <name>` switches by name.
- Press `:` for the command bar, for example `:ctx` to switch context.
- Quit with `:q` or `ctrl+c`.

To add usage data from Prometheus, run `:prometheusurl <url>` and then
`:connect`. The `P` columns appear once data arrives. See
[docs/configuration.md](docs/configuration.md#prometheus).

## Keys

| Key | Action |
| --- | --- |
| `?` | help |
| `/` | filter |
| `:` | command bar |
| `j` / `k` | move down / up |
| `H` / `L` | select previous / next column |
| `<` / `>` | sort ascending / descending |
| `c` | choose columns |
| `0`, `1`-`9` | all namespaces, namespace shortcuts |
| `space` | hide the row and move it to the end |
| `ctrl+t` | collapse or expand replica pods |
| `f` | fullscreen table |
| `ctrl+e` | export a JSON and Markdown report |
| `ctrl+r` | refresh (asks for confirmation) |
| `ctrl+p` | edit `config.yaml` |
| `ctrl+g` | Prometheus settings for this context |
| `ctrl+c` | quit |

Every shortcut except the fixed keys is configurable. The in-app help (`?`)
is the source of truth; the full list is in
[docs/keybindings.md](docs/keybindings.md).

## Configuration

No configuration is needed. Settings live in
`~/.config/kubernova/config.yaml` (or under `$XDG_CONFIG_HOME`), and a partial
file is merged over the defaults. Per-cluster view state, the table cache and
exports are stored beside it. `config-default.yaml` in the same directory
lists every default with comments.

```yaml
kubernetes:
  request_timeout: 6s
keybindings:
  fullscreen: F
```

See [docs/configuration.md](docs/configuration.md) for all settings, and
[docs/architecture.md](docs/architecture.md) for how the application works.

## Build from source

```bash
make build   # go build -o kubernova .
make test    # go vet and go test -race
make lint    # golangci-lint run
make run
```

See [CONTRIBUTING.md](CONTRIBUTING.md).

## License

GNU General Public License v3.0. See [LICENSE](LICENSE).
