# Configuration

Kubernova follows the XDG Base Directory Specification. All files live under
`$XDG_CONFIG_HOME/kubernova/` (normally `~/.config/kubernova/`).

No configuration is required. On first start Kubernova creates the files
below with default values. A partial `config.yaml` is merged over the
defaults, so you only write the settings you want to change.

## Files

| Path | Purpose |
| --- | --- |
| `config.yaml` | Active configuration. Edit this file. |
| `config-default.yaml` | Generated, commented reference of every default. Do not edit. |
| `backup/` | Up to three previous copies of `config.yaml`, oldest removed first. |
| `clusters/<cluster>/<context>/state.yaml` | Per-context view state and Prometheus connection settings. |
| `clusters/<cluster>/<context>/cache_YYMMDD_HHMMSS.json` | Last successful table snapshot for the context. |
| `clusters/<cluster>/<context>/exports/` | Default destination for `:export`. |

The default values are maintained in the repository under
[`config/kubernova/`](../config/kubernova/) and embedded in the binary.
`config-default.yaml` is generated from them.

Unknown sections in `config.yaml` are preserved. Changing one shows a
restart-required warning, because the running application has no handler for
it.

## Editing and reloading

`:option` (or `ctrl+p`) opens `config.yaml` in `$VISUAL` or `$EDITOR`, falling
back to `vi`. A backup is written first. When the editor exits, supported
changes apply immediately.

`:reload` re-reads the configuration and keybindings without refreshing
Kubernetes data. Reload is transactional: malformed YAML, invalid values or a
binding collision leave the previous configuration active and show the error.
`:refresh` refreshes Kubernetes data and connections instead.

## Kubernetes

```yaml
kubernetes:
  access_mode: environment
  kubeconfig_path: ""
  request_timeout: 6s
  page_size: 100
```

- `access_mode`: `environment` uses `KUBECONFIG` or `~/.kube/config`; `file`
  uses `kubeconfig_path` (a leading `~` is expanded).
- `request_timeout`: timeout for each API request and the initial connectivity
  check. Accepts a duration such as `6s` or `1m`, or a whole number of seconds
  (`6`).
- `page_size`: resources per API request; larger clusters are paginated.

Kubernetes access is required; there is no offline mode. With a valid cache
for the same cluster and context, Kubernova starts from the cache and shows it
as cached if the cluster cannot be reached.

## View

```yaml
view:
  sort_column: K02
  sort_descending: false
  fullscreen: false
```

- `sort_column`: stable column ID, empty for server order. `K02` is
  `NAMESPACE`. Rows with equal values are ordered by namespace and then pod.
- `fullscreen`: show only the column titles and the table.

These are defaults. Changes made in the UI (sort, columns, namespaces, hidden
rows, replica collapse, fullscreen) are written to `state.yaml` for the
current context and override the defaults there.

### Columns

Column IDs are stable. Hide or show columns with `c`. Columns always render
in the order below. `state.yaml` stores the visible IDs under `columns`.

| ID | Header | Default |
| --- | --- | --- |
| `K02` | NAMESPACE | shown |
| `K04` | OWNER KIND | shown |
| `K03` | OWNER NAME | shown |
| `K05` | POD | shown |
| `K08` | CONTAINER | shown |
| `K19` | STATUS | shown |
| `K09` | CPU REQ | shown |
| `K10` | CPU LIM | shown |
| `K11` | MEM REQ | shown |
| `K12` | MEM LIM | shown |
| `K13` | OOM | shown |
| `K17` | REPLICAS | shown |
| `K16` | TAG | shown |
| `K01` | CLUSTER | hidden |
| `K06` | IMAGE | hidden |
| `K18` | REGISTRY | hidden |
| `K07` | RESOURCE SRC | hidden |
| `K14` | CONTAINER UID | hidden |
| `K15` | OWNER UID | hidden |
| `P01` | CPU_REQ_USE | hidden until Prometheus data exists |
| `P02` | CPU_REQ_SUG | hidden until Prometheus data exists |
| `P03` | CPU_LIM_USE | hidden until Prometheus data exists |
| `P04` | CPU_LIM_SUG | hidden until Prometheus data exists |
| `P05` | MEM_REQ_USE | hidden until Prometheus data exists |
| `P06` | MEM_REQ_SUG | hidden until Prometheus data exists |
| `P07` | MEM_LIM_USE | hidden until Prometheus data exists |
| `P08` | MEM_LIM_SUG | hidden until Prometheus data exists |
| `P09` | OOM_AFFECTED | hidden until Prometheus data exists |

Hidden columns are removed completely and are not searched by the filter.
Sorting by a hidden column still works. Exports always contain every column.
`P` columns are shown automatically once Prometheus data is available, until
you change column visibility yourself.

## Keybindings

```yaml
keybindings:
  fullscreen: F
```

Only the bindings you list change. See [keybindings.md](keybindings.md) for
the identifiers, defaults and rules.

## Skin

Colors are `#RRGGBB` values.

```yaml
skin:
  body:   { fgColor: "#ffffff", bgColor: "#000000", logoColor: "#ff00ff" }
  info:   { fgColor: "#ffffff", sectionColor: "#ffa500" }
  frame:
    menu: { fgColor: "#a8a8a8", keyColor: "#5fa8ff", numKeyColor: "#b266ff" }
  status: { errorColor: "#ff5555", warnColor: "#ffb86c", infoColor: "#61afef" }
```

Status is also shown as text and does not depend on color: `○` not connected
or not configured, `●` connected, `✗` error.

## Prometheus

Prometheus is optional. It adds usage percentiles and right-sizing
suggestions in the `P` columns.

Global settings in `config.yaml`:

```yaml
prometheus:
  enabled: true
  connection_timeout: 10s
  request_timeout: 30s
  query_range: 24h
  rate_window: 5m
  max_concurrent_queries: 2
```

- `enabled`: allow automatic connection when a URL is known. When `false`,
  nothing connects on its own and cached values stay visible.
- `connection_timeout`: readiness probe. `request_timeout`: each instant
  query.
- `query_range`: lookback for range expressions. `rate_window`: default rate
  window; `resources.cpu.rate_window` and `resources.memory.rate_window`
  override it.
- `max_concurrent_queries`: bound on concurrent instant queries.

Cluster-specific settings live in `state.yaml` and are edited with
`:promconfig` (or `ctrl+g`): URL, TLS verification, tenant header,
authentication, and label filters. Authentication stores environment-variable
names only (`bearer_token_env`, `basic_username_env`, `basic_password_env`).
Secret values are read from the environment when a request is made and are
never written to config, state, cache, exports, or logs. Do not put
credentials in the URL.

`:prometheusurl <url>` stores a URL without connecting. `:connect [url]`
probes Prometheus and runs at most one grouped query each for CPU
percentiles, memory percentiles and OOM observations. Connecting is refused
while Kubernetes is unreachable. Press `esc` to cancel a connection in
progress.

### Resource analysis

```yaml
resources:
  cpu:
    enabled: true
    range: ""                       # empty uses prometheus.query_range
    percentiles: { request: 90, limit: 0 }
    suggestion_buffer_percentage: { request: 10, limit: 0 }
    suggestion_minimum: { request: 10m, limit: 10m }
    query_step: auto
  memory:
    enabled: true
    oomkilled_check: true
    percentiles: { request: 90, limit: 99 }
    suggestion_buffer_percentage: { request: 0, limit: 10 }
    suggestion_minimum: { request: 32Mi, limit: 32Mi }
```

A percentile of `0` disables that calculation and suggestion. A suggestion is
`percentile * (1 + buffer / 100)`, raised to `suggestion_minimum` when lower.
Suggestion cells show the current value and the suggestion, for example
`100m -> 150m`. `query_step` is `auto` or an explicit duration.

The commented default file, [`config/kubernova/prometheus.yaml`](../config/kubernova/prometheus.yaml),
documents every PromQL query, the exporters it needs and its limitations.

## Exports

`:export [json|md|all] [path]` (or `ctrl+e`) writes the complete report:
every row of every namespace and every column, in the current sort order.
Filters, namespace selection, hidden columns and replica collapse do not
apply. Files are written atomically to `exports/` beside `state.yaml`, or to
the given path. An export made from cached data is marked as cached. Exports
never contain secrets, authentication values or URL credentials.
