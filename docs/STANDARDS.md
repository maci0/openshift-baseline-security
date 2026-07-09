# OpenShift coding standards reference

The style standards and guidelines this project follows, with the
authoritative sources (all URLs verified 2026-07). OpenShift layers its
conventions: upstream Kubernetes at the bottom, OpenShift-specific rules in
`openshift/enhancements`, per-area docs on top. Where this repo deviates,
the gap is called out.

## Go code style

There is no OpenShift-specific Go style document; OpenShift inherits
upstream Kubernetes conventions:

- [Kubernetes coding conventions](https://github.com/kubernetes/community/blob/master/contributors/guide/coding-conventions.md),
  which defer to [Effective Go](https://go.dev/doc/effective_go) and
  [Go Code Review Comments](https://go.dev/wiki/CodeReviewComments).
  Highlights: package names lowercase, no stutter (`storage.Interface`,
  not `storage.StorageInterface`); Go filenames lowercase with
  underscores; CLI flags use dashes.

Enforcement is mechanical, not prose:

- [openshift/build-machinery-go verify targets](https://github.com/openshift/build-machinery-go/blob/master/make/targets/golang/verify-update.mk):
  the standard `verify-gofmt` / `verify-govet` make targets Prow presubmits
  run across openshift repos.
- golangci-lint per repo, allowlist style. Examples:
  [installer](https://github.com/openshift/installer/blob/main/.golangci.yaml)
  (~30 linters incl. gosec, gocritic, revive),
  [cluster-monitoring-operator](https://github.com/openshift/cluster-monitoring-operator/blob/main/.golangci.yaml)
  (disable-all + 15 curated),
  [cluster-network-operator](https://github.com/openshift/cluster-network-operator/blob/master/.golangci.yaml)
  (12). Some repos (oc, compliance-operator) rely on gofmt/govet only.

**Here**: `operator/.golangci.yml` (staticcheck, misspell, unconvert,
unparam, nilerr + gofmt/goimports), `make lint`, CI drift check for
generated files.

## API design conventions

- [openshift/enhancements CONVENTIONS.md](https://github.com/openshift/enhancements/blob/master/CONVENTIONS.md):
  every component operator-managed; resource requests required, limits
  discouraged; HA expectations (survive 60s API outage); metrics over
  HTTPS; human-friendly consistent naming.
- [dev-guide/api-conventions.md](https://github.com/openshift/enhancements/blob/master/dev-guide/api-conventions.md):
  **no booleans in spec** (string enums like `Enabled`/`Disabled` for
  future evolvability); PascalCase enum values; CRD optional fields use
  `omitempty` without pointers unless unset-vs-zero matters; discriminated
  unions with a required enum discriminator; configuration APIs default in
  the controller, not the schema.
- [Upstream Kubernetes API conventions](https://github.com/kubernetes/community/blob/master/contributors/devel/sig-architecture/api-conventions.md):
  spec/status separation; conditions as `{type, status, reason, message}`;
  plural lowercase resources, CamelCase kinds.

**Here**: `ClusterBaseline` follows the singleton-named-`cluster`,
conditions, and printer-column conventions. **Known gap**: three spec
booleans (`installComplianceOperator`, `console.enabled`,
`remediation.autoApply`) violate the no-booleans rule; acceptable at
v1alpha1, must become string enums before v1beta1 (tracked in TODO.md).

## Operator conventions

- [dev-guide/operators.md](https://github.com/openshift/enhancements/blob/master/dev-guide/operators.md):
  operators report Available/Degraded/Progressing, expose metrics, build on
  library-go patterns.
- [ClusterOperator status contract](https://github.com/openshift/enhancements/blob/master/dev-guide/cluster-version-operator/dev/clusteroperator.md)
  (the authoritative conditions doc): `Available` must not go False during
  normal upgrades; `Degraded` must not go True during them; `Progressing`
  only while actually rolling out; `relatedObjects` feed must-gather.

**Here**: layered OLM operator, not a ClusterOperator, so the contract is
followed in spirit on the CR's own conditions
(`ComplianceOperatorReady`, `ScanConfigured`, `ConsolePluginReady`,
`Degraded`).

## Console / frontend

- [openshift/console STYLEGUIDE.md](https://github.com/openshift/console/blob/main/STYLEGUIDE.md):
  TypeScript mandatory; functional components + hooks; "use PatternFly for
  all styling" (no custom SCSS); no `any`; useCallback/useMemo for perf;
  WCAG 2.1 AA; i18n via `useTranslation()`.
- [console CONTRIBUTING.md](https://github.com/openshift/console/blob/main/CONTRIBUTING.md):
  commit messages say what and why; bug refs in commit + PR title.
- [Dynamic plugin SDK README](https://github.com/openshift/console/blob/main/frontend/packages/console-dynamic-plugin-sdk/README.md):
  module federation, declarative extensions, console-provided shared
  modules as peers, don't bundle base PatternFly styles, PF version matrix
  per console release (4.22 = PatternFly 6).
- [PatternFly design guidelines](https://www.patternfly.org/get-started/design).

**Here**: compliant on all points (TS strict, PF6 only, SDK components,
i18n namespace `plugin__baseline-security-console-plugin`).

## Commit / PR workflow

- [Prow OWNERS model](https://github.com/kubernetes/community/blob/master/contributors/guide/owners.md):
  two-phase review; `/lgtm` from any member, `/approve` from OWNERS;
  Tide merges when both labels present.
- [Jira integration](https://docs.ci.openshift.org/docs/architecture/jira/):
  PR titles prefixed `OCPBUGS-123: ...` (or `NO-JIRA:`); bug state
  validated by the bot.
- [Component onboarding](https://docs.ci.openshift.org/docs/how-tos/onboarding-a-new-component/):
  ci-operator config in openshift/release, OWNERS as prerequisite.
- Per-repo example: [cluster-image-registry-operator CONTRIBUTING.md](https://github.com/openshift/cluster-image-registry-operator/blob/main/CONTRIBUTING.md).

**Here**: OWNERS at repo root; Prow/Jira conventions apply only after
ci-operator onboarding (productization step, SPEC §10).

## Caveats found while verifying

- `openshift/community` is archived (2022); not a conventions source.
- `openshift/origin` HACKING.md only exists on old branches
  (release-3.11); origin/main carries no style docs.
- The operator conditions contract lives in openshift/enhancements, not in
  the cluster-version-operator repo (that path 404s).
