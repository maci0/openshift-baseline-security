## What

## How to test

- [ ] Operator: `cd operator && make test test-race lint verify` (or `make ci` for the full GHA operator job; needs docker)
- [ ] Plugin: `cd console-plugin && bun run ci` (or `bun run lint && bun run lint:oxlint && bun run typecheck && bun run test` without the production build)
- [ ] `[Unreleased]` in CHANGELOG.md if a consumer can observe the change
- [ ] `make generate manifests` output committed if API markers or RBAC changed
- [ ] `make verify` clean if the CSV, `config/rbac/role.yaml`, `config/prometheus/`, or `VERSION` changed
