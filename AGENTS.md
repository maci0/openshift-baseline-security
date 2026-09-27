# Agent rules: openshift-baseline-security

Repo-wide contract. `operator/AGENTS.md` and `console-plugin/AGENTS.md` hold
the per-component detail; read the one that owns the files you are touching.

## What this is

A layered OLM operator plus a console dynamic plugin that drive the Red Hat
Compliance Operator and render its results under **Administration →
Compliance**. Single cluster-scoped CR, `ClusterBaseline/cluster`.

Read `docs/SPEC.md` before changing behavior. `docs/STANDARDS.md` records the
external OpenShift/Kubernetes conventions this repo follows and the deliberate
deviations from them; `docs/DESIGN-DECISIONS.md` holds the ADRs.

## Gate

Both halves must be green before a change lands. Neither runs the other's.

```sh
make check                                    # preflight, both modules
cd operator       && make test test-race lint  # + make fuzz before a release
cd console-plugin && yarn lint && yarn lint:oxlint && yarn typecheck && yarn test
```

`make ci` at the repo root runs both local replicas: `operator/Makefile ci`, the
GHA `operator` job (also build, the reproducible-binary check, `govulncheck`,
`mod-tidy-check`, alert tests, generated-file drift, `make bundle`; needs
docker), and `console-plugin` `yarn ci`, the replica of the GHA
`console-plugin` job except `yarn npm audit`. The required `images` and
`catalog` jobs have no local replica beyond `make bundle` and
`make catalog-prepare` (both need docker); a Dockerfile, bundle, or CSV change
should have run those first. Extended fuzzing and live-cluster e2e are
scheduled or on-demand, never per-PR. `make help` at the root and in each
module directory lists the rest. Human clone-to-PR path: `CONTRIBUTING.md`.

## Version lockstep

One version string, ten files: `operator/Makefile` (`VERSION`), the CSV
(`name`, `spec.version`, `containerImage`, and every image ref; the CSV is
hand-maintained, nothing generates those), `console-plugin/package.json`
(`version` and `consolePlugin.version`), `CHANGELOG.md` (section heading and
both compare footers), `README.md` (**Current release** plus the catalog
tags in the install snippets), and the `ARG VERSION=` default in each of the
five Dockerfiles (`operator/Dockerfile`, `operator/Dockerfile.ci`,
`operator/bundle.Dockerfile`, `operator/catalog.Dockerfile`,
`console-plugin/Dockerfile`). `operator/catalog/package.yaml` is rendered
from `VERSION` by `make catalog-prepare`; never hand-edit it. `make
verify-versions` enforces all of it; run it after any bump rather than
eyeballing the diff.

Cutting a release:

1. Land the work with `[Unreleased]` entries in `CHANGELOG.md`.
2. Bump all ten; promote `[Unreleased]` to `## [X.Y.Z] - <date>` and
   leave a fresh empty `[Unreleased]` above it.
3. `make bundle` (regenerates the CRD copy, runs every verify target, and
   validates the bundle in operator-sdk).
4. `RELEASE_GATE=1 make verify-versions`, which fails if `[Unreleased]` still has
   entries.
5. Tag `vX.Y.Z`. `REQUIRE_GIT_TAGS=1` adds the tag's existence to the check.
   The release workflow runs both gates and the tagged commit's own
   `make -C operator test` plus the console plugin's `yarn typecheck` and
   `yarn test` before pushing images, and refuses to publish a version from a
   commit other than `vX.Y.Z`. Version resolution lives in
   `operator/hack/resolve-release-version.sh`; the publish and SBOM jobs both
   call it so they cannot disagree about what is being released. Its
   `INPUT_VERSION` env is the `workflow_dispatch` `version` input, so a manual
   cut is the version that ships, not the ref it was dispatched from.

Published image, tag, and CSV version strings are immutable: never re-push,
re-tag, or force-move one. OLM unpack caches serve stale content on a same-tag
republish. No `replaces` upgrade graph exists pre-1.0: every bundle is a
standalone channel head, so there is no `PREV_VERSION` anywhere. CSV
`capabilities` is `Basic Install` (`make verify-versions` rejects
`spec.replaces` / `spec.skipRange` and any other capability).

## Docs that must move with the code

- `CHANGELOG.md` for anything a consumer can observe. Its own header defines
  what is in and out of contract; entries state the symptom, not the patch.
- `docs/SPEC.md` for behavior, the repo tree, and the roadmap table.
- `docs/TEST-PLAN.md` names the test that covers each case. Renaming or
  deleting a test updates its row in the same change.
- `docs/OBSERVABILITY.md` for any metric or alert change; alert expressions
  are additionally covered by `operator/config/prometheus/testdata`.
- `docs/THREAT_MODEL.md` for a new entry point, trust boundary, privilege
  transition, or mitigation claim.
- `README.md` for a new flag, install path, or release version.

## Layout

- root `Makefile`: contributor entry point. `help`, `check` (preflight), and
  `test` / `lint` / `ci` wrappers that delegate to the two module Makefiles.
  It holds no rule of its own, so add a target here only when both modules
  want it.
- `operator/`: Go operator (kubebuilder go/v4), OLM bundle, file-based catalog
- `console-plugin/`: React 18 / PatternFly 6 dynamic plugin
- `docs/`: SPEC, STANDARDS, PATTERNS, DESIGN-DECISIONS, TEST-PLAN,
  OBSERVABILITY, THREAT_MODEL, RESTORE, and `screenshots/` (generated by the
  Playwright suite, never hand-added)
- `.github/workflows/`: `ci.yml` and `release.yml`
- `.scratch/`: gitignored working files. Nothing temporary belongs anywhere
  else in the tree, and never in `/tmp`.

## House rules

- Branch for the work (`fix/`, `feat/`, `docs/`, `chore/`); never commit
  straight to `main`.
- No AI tool or model names anywhere git can see: commit messages, branch
  names, PR text, code comments. No `Co-Authored-By` for a tool. Human
  co-authors are credited normally.
- No em dashes in prose. The `—` glyph is a legitimate UI value in the plugin
  (an empty score, an absent field); leave those and the comments quoting them
  alone.
- A suppression (`//nolint:`, `eslint-disable`, `oxlint-disable`) names its
  rule and carries its reason inline. An unused one fails lint.
- Untrusted input is anything read off a cluster object: CR spec fields,
  Compliance Operator labels and annotations, status strings. Narrow it at the
  boundary; never let a malformed value throw where it would abort a reconcile
  or a report export.
