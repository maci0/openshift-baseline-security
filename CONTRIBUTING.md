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
| shellcheck | n/a | `make lint` only (`lint-shell`); preinstalled on the CI runner, `brew install shellcheck` / `apt-get install shellcheck` elsewhere |
| uv | n/a | `make lint` only (`lint-python` runs `uvx ruff`); the Makefile names it if `uvx` is missing |
| docker | n/a | only for `make ci`, `make bundle`, `make catalog-prepare`, `make test-alerts`, and image builds |
| `oc` | n/a | only for `make run` / `make deploy` / live e2e |

No other system packages are required for unit tests. `make lint` is the
only target needing shellcheck and uv. Alert unit tests need `python3` on
PATH (stdlib only) plus docker.

## Setup

```sh
git clone https://github.com/maci0/openshift-baseline-security.git
cd openshift-baseline-security

# operator (GOTOOLCHAIN matches go.mod; first run may download that toolchain)
cd operator
make test test-race lint

# console plugin
cd ../console-plugin
corepack enable
yarn_pm=$(node -p "require('./package.json').packageManager")
corepack prepare "${yarn_pm}" --activate
yarn install --immutable
yarn lint && yarn lint:oxlint && yarn typecheck && yarn test
```

From the repo root, `make setup` runs the `yarn install --immutable` line above
once Yarn 4 is on PATH (it prints the two `corepack` commands and stops if
`yarn` is missing) and then runs `make check`, which names anything else the
setup above is missing (wrong Node major, no shellcheck or `uvx` for
`make lint`) before a build starts. Docker is the one thing `check` only warns
about: `make test` and `make lint` run without it, and `make ci` needs it.
`make test`, `make lint`, and `make ci` run both halves and only delegate to
the per-module Makefiles; there is no second copy of a rule.
`make help`, `make -C operator help`, and `make -C console-plugin help` list
the rest.

## Edit-test loop

| What you changed | Fast loop | Full local replica of CI |
|---|---|---|
| `operator/` | `make test` or one package: `go test ./internal/controller/ -count=1 -run TestName` | `make ci` (needs docker for alerts + bundle validate) |
| `console-plugin/` | `yarn test` or one file: `yarn test src/scoring.test.ts`; watch: `yarn test:watch` | `yarn ci` |

`make test` / `yarn test` do not need a cluster. Live e2e is
`make test-e2e` (`KUBECONFIG`) and `yarn test-e2e` (`CONSOLE_URL` and
`KUBEADMIN_PASSWORD`; copy `console-plugin/.env.example` to `.env`).

`yarn test-e2e` also needs the Playwright chromium build, which a clean clone
does not have: `.yarnrc.yml` sets `enableScripts: false`, so no install script
downloads it and CI sets `PLAYWRIGHT_SKIP_BROWSER_DOWNLOAD` for the same
reason. Fetch it once with `yarn playwright install chromium` (a user-cache
download, no system packages). On a bare Linux host the shared libraries are
missing too and `yarn playwright install --with-deps chromium` installs them;
that step needs root, so it is not part of the per-clone loop.

## Before a PR

1. Branch `fix/`, `feat/`, `docs/`, or `chore/` from `main`. Never commit to `main`.
2. Operator edits: `cd operator && make test test-race lint`. Also `make generate manifests`
   if you touched API markers or the manager ClusterRole (`config/rbac/role.yaml`),
   and commit the output. CI fails on `git diff --exit-code` after that command.
3. Plugin edits: `cd console-plugin && yarn lint && yarn lint:oxlint && yarn typecheck && yarn test`.
   `yarn ci` also runs the production webpack build (CI does).
4. Consumer-visible behavior: a `[Unreleased]` entry in `CHANGELOG.md` (symptom,
   not the patch). See the changelog header for what is in contract.
5. Regenerated files (`operator/config/crd/`, `operator/config/rbac/role.yaml`,
   `operator/api/v1alpha1/zz_generated.deepcopy.go`, the CRD copy under
   `operator/bundle/manifests/`): `cd operator && make generate manifests`.
   Other `config/rbac/` files and the CSV are hand-maintained; do not generate them.

`make ci` at the repo root runs both: `operator/Makefile ci` (unit tests,
lint, govulncheck, alert tests, generated-file drift, binary reproducibility,
bundle validate) and `console-plugin` `yarn ci`. Together they match the
GitHub Actions `operator` and `console-plugin` jobs; the plugin side excludes
`yarn npm audit`, which is CI-only. Image and catalog builds stay in CI.

`cd operator && make verify-reproducible` on its own rebuilds the manager from
a second absolute path with a different timezone, locale, and umask and fails
unless the two binaries have the same SHA-256. Run it after touching the build
flags or the Go toolchain pin; a green `make build` alone does not prove the
binary is reproducible.

## Adding a test

- Operator: table-driven `*_test.go` next to the code (`operator/internal/controller/`,
  `operator/cmd/`). Live-cluster cases go under `operator/test/e2e/` with the `e2e`
  build tag. Name the new case in [docs/TEST-PLAN.md](docs/TEST-PLAN.md).
- Plugin: colocated `src/<module>.test.ts`. Playwright specs live in
  `console-plugin/e2e/`.
