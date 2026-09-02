## What

## How to test

- [ ] Operator: `cd operator && make test lint` (or `make ci` for the full GHA operator job; needs docker)
- [ ] Plugin: `cd console-plugin && yarn ci` (or `yarn lint && yarn lint:oxlint && yarn typecheck && yarn test` without the production build)
- [ ] `[Unreleased]` in CHANGELOG.md if a consumer can observe the change
- [ ] `make generate manifests` output committed if API markers or RBAC changed
