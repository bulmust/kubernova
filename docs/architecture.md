# Architecture and behavior

## Package layout

| Package             | Responsibility                                                                                                      |
| ------------------- | ------------------------------------------------------------------------------------------------------------------- |
| `main.go`           | Entry point. Runs `app.Run` and exits with status 1 and a message on error.                                         |
| `internal/app`      | Wires the UI, configuration, state and clients together. Holds input handling, background work and the command bar. |
| `internal/ui`       | tview widgets: table, header, menu, help, skin, logo and status indicator.                                          |
| `internal/registry` | Single definition of commands, shortcuts and fixed keys.                                                            |
| `internal/config`   | Defaults, merge, validation, backups and file locations for `config.yaml`.                                          |
| `internal/state`    | Per-cluster, per-context `state.yaml`.                                                                              |
| `internal/client`   | Kubernetes client, owner resolution, Prometheus client and queries, snapshot cache.                                 |
| `internal/model`    | Row model, column definitions, image parsing.                                                                       |
| `internal/report`   | Builds the summary and suggestions used by exports.                                                                 |
| `internal/export`   | Renders JSON and Markdown reports and writes them atomically.                                                       |
| `config/kubernova`  | Commented default YAML files embedded in the binary.                                                                |

## Design points

**One registry.** `internal/registry` defines every command, shortcut and
fixed key. The input handler dispatches shortcuts by identifier from that
list, the header menu and the help page (`?`) are generated from it, and
startup fails if a shortcut has no handler. A test checks
[keybindings.md](keybindings.md) against it.

**Configuration pipeline.** The embedded defaults are merged with the user's
`config.yaml`, normalized and validated. The merged result is written back as
a comment-free effective `config.yaml`, and a commented `config-default.yaml`
is generated beside it. Mutable per-context data (view choices and Prometheus
connection settings) is kept in `state.yaml` and never written into the
defaults.

**Background work.** The UI runs on tview's event loop. Kubernetes and
Prometheus calls run in goroutines started through a small guard that turns a
panic into an orderly shutdown, so the terminal is restored and the error is
printed. Results are applied on the UI goroutine with `QueueUpdateDraw`. Work
carries a context and a generation counter: a context switch or refresh
cancels in-flight work, and results from an older generation are discarded.

**Logging.** client-go logging is discarded, because writing to stderr would
corrupt the terminal display. Errors surface in the UI status history
(`:status`).

## Kubernetes data

After connecting, Kubernova lists the containers of pods in every namespace,
including init containers. Active Job and CronJob pods are excluded; completed
or failed batch pods are listed so their final state is visible.

Each container is one row. The image is parsed into registry, repository and
tag: digests are removed, unqualified images use `docker.io`, and a missing
tag, including a digest-only reference, is shown as `-`.

Owners are resolved as far up the chain as possible, for example
pod, ReplicaSet, Deployment. Resolution stops at custom-resource owners, which
are reported by their own kind and name.

`RESOURCE SRC` reflects the `kubernetes.io/limit-ranger` annotation:
`LimitRange` when a LimitRange defaulted the resources, `Pod` when they are
set explicitly, and `-` when none are set.

`STATUS` shows the state and reason, for example `Running`,
`Waiting/CrashLoopBackOff`, `Completed` or `OOMKilled`. `OOM` records whether
the container has ever been `OOMKilled`.

Replica pods of the same owner are collapsed to one row by default
(`ctrl+t` toggles). `space` hides the selected row and moves it to the end.

### Sorting

Sorting is natural for text with digits (`pod-2` before `pod-10`), numeric
for replicas and Prometheus columns, and quantity-aware for CPU and memory.
Missing `-` values always sort last. Successfully completed containers sort
after every active row regardless of direction and are shown in yellow with a
`[completed]` marker. Rows that compare equal fall back to namespace and then
pod.

## Cache

Startup is cache-first. When a valid cache exists for the cluster and
context, it is loaded without Kubernetes API calls. A first visit connects,
fetches and writes a timestamped `cache_YYMMDD_HHMMSS.json`. An explicit
refresh asks for confirmation, redoes all connections and requests, then
replaces the cache. A cache file is written to a temporary file in the same
directory and renamed, so a partial file is never observed.

If a connection fails and a cache exists, the cached rows are shown with a
cached marker and the cache date appears in the header. The table title
includes the loaded cache file name.

The cache holds no secrets. It stores a schema version, fetch time, cluster
identity, a `workload` list keyed by namespace, owner kind and owner name,
and an optional node inventory (name, capacity and allocatable CPU and
memory only; labels and annotations are never stored). An absent `nodes`
block means unknown; an unavailable block means `n/a` after a node-list
permission failure. Each pod records its owner chain and its container
status.

## Prometheus behavior

Startup is cache-first for Prometheus too. A cached Prometheus block is
shown as `cached <age>` with no Prometheus requests. Without one, Kubernova
confirms Kubernetes is reachable and then connects automatically if
`prometheus.enabled` is true and a URL exists. If no URL exists, a URL prompt
appears; dismissing it suppresses further automatic prompts for the session.
Explicit refresh and `:connect` always rerun the queries. Context switches
cancel old work and apply the same rules to the new context. Queries are
cancellable, bounded in concurrency and never polled.

`P` cells that depend on usage mark stale values with `~` when the effective
query has changed since they were cached; suggestion cells are not marked.
Missing values render as `-`. Analysis settings and label filters are
fingerprinted, so cached observations remain visible but stale after a
change.

OOM detection uses `max_over_time` of
`kube_pod_container_status_last_terminated_reason{reason="OOMKilled"}` from
kube-state-metrics. It identifies containers whose last termination was
observed as `OOMKilled` during the query range. It is an observation, not an
exact event count. Unavailable OOM data is reported as unknown, not zero, and
will not necessarily match the Kubernetes API's view of OOMKilled containers.
The full queries and their limitations are documented in
[`config/kubernova/prometheus.yaml`](../config/kubernova/prometheus.yaml).

## Layout

The main view is a header (cluster information, namespaces, shortcuts and the
logo) above the table. On narrow terminals the logo is hidden first.
Fullscreen mode shows only the column titles and the table; it is ignored
while a filter or command input is active. The table title has the form
`Kubernova(<namespace>)[<rows>]`. `:status` opens a scrollable list of every
status message from the current session.
