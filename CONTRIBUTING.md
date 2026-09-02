# Contributing

Runnable path from a clean clone to a PR. House rules (branch names, changelog
contract, generated files) live in [AGENTS.md](AGENTS.md); this file is the
commands that have to work.

## Prerequisites

| Tool | Pin | Where |
|---|---|---|
| Go | `go` directive in `operator/go.mod` | Makefile sets `GOTOOLCHAIN` from that line; host Go 1.21+ downloads it |
| Node | major 22, exact patch in `console-plugin/.nvmrc` | `package.json` `engines.node` is `>=22 <23`; `yarn` scripts refuse any other major |
| Yarn 4 | `packageManager` in `console-plugin/package.json` | `corepack enable` then `corepack prepare` (same as CI) |
| docker | n/a | only for `make ci`, `make bundle`, `make test-alerts`, and image builds |
| `oc` | n/a | only for `make run` / `make deploy` / live e2e |

No other system packages are required for unit tests. Alert unit tests need
`python3` on PATH (stdlib only) plus docker.

## Setup

```sh
git clone https://github.com/maci0/openshift-baseline-security.git
cd openshift-baseline-security

# operator (GOTOOLCHAIN matches go.mod; first run may download that toolchain)
cd operator
make test lint

# console plugin
cd ../console-plugin
corepack enable
yarn_pm=$(node -p "require('./package.json').packageManager")
corepack prepare "${yarn_pm}" --activate
yarn install --immutable
yarn lint && yarn lint:oxlint && yarn typecheck && yarn test
```

`make -C operator help` and `make -C console-plugin help` list the rest.

## Edit-test loop

| What you changed | Fast loop | Full local replica of CI |
|---|---|---|
| `operator/` | `make test` or one package: `go test ./internal/controller/ -count=1 -run TestName` | `make ci` (needs docker for alerts + bundle validate) |
| `console-plugin/` | `yarn test` or one file: `yarn test src/scoring.test.ts`; watch: `yarn test:watch` | `yarn ci` |

`make test` / `yarn test` do not need a cluster. Live e2e is
`make test-e2e` (`KUBECONFIG`) and `yarn test-e2e` (`CONSOLE_URL` and
`KUBEADMIN_PASSWORD`; copy `console-plugin/.env.example` to `.env`).

## Before a PR

1. Branch `fix/`, `feat/`, `docs/`, or `chore/` from `main`. Never commit to `main`.
2. Operator edits: `cd operator && make test lint`. Also `make generate manifests`
   if you touched API markers or RBAC, and commit the output. CI fails on
   `git diff --exit-code` after that command.
3. Plugin edits: `cd console-plugin && yarn lint && yarn lint:oxlint && yarn typecheck && yarn test`.
   `yarn ci` also runs the production webpack build (CI does).
4. Consumer-visible behavior: a `[Unreleased]` entry in `CHANGELOG.md` (symptom,
   not the patch). See the changelog header for what is in contract.
5. Regenerated files (`operator/config/crd/`, `operator/config/rbac/`,
   `operator/api/v1alpha1/zz_generated.deepcopy.go`, the CRD copy under
   `operator/bundle/manifests/`): `cd operator && make generate manifests`.
   The CSV is hand-maintained; do not generate it.

`cd operator && make ci` is the single command that matches the GitHub Actions
`operator` job (unit tests, lint, govulncheck, alert tests, generated-file
drift, bundle validate). `cd console-plugin && yarn ci` matches the
`console-plugin` job except `yarn npm audit`. Image builds stay in CI.

## Adding a test

- Operator: table-driven `*_test.go` next to the code (`operator/internal/controller/`,
  `operator/cmd/`). Live-cluster cases go under `operator/test/e2e/` with the `e2e`
  build tag. Name the new case in [docs/TEST-PLAN.md](docs/TEST-PLAN.md).
- Plugin: colocated `src/<module>.test.ts`. Playwright specs live in
  `console-plugin/e2e/`.
