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
| `P10` | SEVERITY | hidden until Prometheus data exists |

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
    oomkilled_limit_multiplier: 1.25
    percentiles: { request: 90, limit: 99 }
    suggestion_buffer_percentage: { request: 0, limit: 10 }
    suggestion_minimum: { request: 32Mi, limit: 32Mi }
  severity:
    medium: { percent: 10 }
    high:
      percent: 50
      minimum: { cpu: 50m, memory: 50Mi }
```

A percentile of `0` disables that calculation and suggestion. A suggestion is
`percentile * (1 + buffer / 100)`, raised to `suggestion_minimum` when lower.
For containers observed as OOMKilled, the memory limit suggestion is raised to
at least `current limit * oomkilled_limit_multiplier` so it never recommends
less headroom than the container already has. Set the multiplier to `0` to
disable this behavior. Suggestion cells show the current value and the
suggestion, for example `100m -> 150m`. `query_step` is `auto` or an explicit
duration.

Usage columns (`P01`, `P03`, `P05`, `P07`) show the percentile of that pod and
container. Suggestions keep the owner-level algorithm: the highest sample
across the workload's pods is used for every replica and container of the same
name, so all replicas receive the same recommendation.

The `P10` severity column grades CPU and memory limit suggestions. A suggestion
is `MEDIUM` above `severity.medium.percent` and `HIGH` when it reaches both
`severity.high.percent` and the matching `severity.high.minimum` quantity.
Containers that Kubernetes or Prometheus observed as OOMKilled are always
`HIGH`. Exports include the count of containers at each severity level.

The commented default file, [`config/kubernova/prometheus.yaml`](../config/kubernova/prometheus.yaml),
documents every PromQL query, the exporters it needs and its limitations.

## Exports

`:export [json|md|all] [path]` (or `ctrl+e`) writes the report:
every row of every namespace, in the configured sort order. Filters,
namespace selection, hidden columns and replica collapse do not apply. Files
are written atomically. An export made from cached data is marked as cached.
Exports never contain secrets, authentication values or URL credentials.

```yaml
export:
  path: clusters/<cluster>/<context>/exports/
  filename: kubernova-<cluster>-<context>-YYMMDD-HHmm:ss
  type: all
  markdown_table_padded: true
  columns: all
  sort_column: namespace
  sort_descending: false
```

- `path`: destination directory. Relative paths are resolved under
  `$XDG_CONFIG_HOME/kubernova/`, so the default lands beside `state.yaml`.
  `<cluster>` and `<context>` are sanitized and substituted. An absolute path
  or a leading `~` writes elsewhere.
- `filename`: template without an extension. Supports `<cluster>`,
  `<context>`, `<timestamp>`, and the `YYMMDD-HHmm:ss` time pattern. The
  selected type's extension is appended.
- `type`: `json`, `md`, or `all`. `ctrl+e` and `:export` without a type use
  this value.
- `markdown_table_padded`: align Markdown table cells with spaces. Set to
  `false` for compact `| value |` rows.
- `columns`: `all`, or comma-separated column IDs or headers such as
  `K02,K05,K08,P10`. Selected columns keep the order given.
- `sort_column`: stable ID or header used for exported rows. The default
  `namespace` is `K02`. Empty uses the current table sort. `sort_descending`
  reverses it.

