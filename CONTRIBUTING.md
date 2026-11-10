# Contributing

Bug reports and pull requests are welcome. For larger changes, open an issue
first to agree on the approach.

## Setup

You need the Go version in [go.mod](go.mod). Then:

```bash
make build
make test
make lint   # requires golangci-lint v2
```

## Guidelines

- Run `gofmt`, `go vet ./...` and `go test -race ./...` before opening a pull
  request. CI runs the same checks plus `golangci-lint`.
- Keep changes small and focused. Add or update tests for behavior changes.
- Commit messages follow Conventional Commits, for example
  `fix: handle empty namespace list`.
- Commands, shortcuts and their defaults are defined only in
  `internal/registry`. If you change them, update
  [docs/keybindings.md](docs/keybindings.md); a test fails when they differ.
- Default configuration lives in `config/kubernova/*.yaml`. Document new
  settings there and in [docs/configuration.md](docs/configuration.md).
- Column IDs (`K01`, `P01`, ...) are stored in user state and must not change.
  Keep `model.Columns` and `Container.Row` in the same order.
- New dependencies need a clear justification in the pull request.
