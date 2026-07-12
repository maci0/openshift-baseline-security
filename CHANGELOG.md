# Changelog

All notable changes to this project are documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project follows [Semantic Versioning](https://semver.org/spec/v2.0.0.html)
with **0.x** rules: the public contract is still evolving. The API group version
is `v1alpha1`, the OLM channel and CSV maturity are `alpha`, and breaking
changes may land in minor bumps until 1.0. Prefer reading each release's
**Changed** / **Removed** sections before upgrading.

Supported host: OpenShift 4.22 (`com.redhat.openshift.versions: =v4.22`,
`minKubeVersion: 1.35.0`). Older or newer OCP releases are not claimed (the
`=` pins to exactly 4.22; a bare `v4.22` would also advertise 4.22 and later).

## [Unreleased]

### Added

- Disable scanning by clearing `spec.profiles` (with no `spec.tailoredProfiles`):
  the operator prunes the ScanSettingBindings and clears the score while keeping
  the CR and its history. New installs still default to `{cis}`.

### Changed

- `spec.profiles` no longer requires at least one entry (the `MinItems=1`
  constraint was dropped) so scanning can be turned off as above. **Upgrade
  impact**: none for existing CRs; validation is only relaxed.
- `spec.complianceCatalogSource` is now validated as a non-empty DNS-1123
  subdomain (a CatalogSource `metadata.name`). Previously any string up to 253
  characters was accepted. **Upgrade impact**: a CR whose catalog-source
  override is not a valid DNS-1123 subdomain (uppercase, spaces, or empty) is
  rejected on next apply; `redhat-operators` and standard names are unaffected.

### Fixed

- `status.newlyFailed` / `status.fixed` no longer flip transiently while a
  scan's results settle: a scan-before-last FAIL snapshot is retained so late
  CheckResult events correct the diff.

### Removed

## [0.4.0] - 2026-07-11

OLM upgrade edge: `baseline-security-operator.v0.4.0` replaces `v0.3.1`.

### Added

- Waiver governance on `ClusterBaseline.spec.waivers` (expiry, requester/approver,
  review date). Expired waivers stop excluding checks from the score.
- Scan regression status: `status.newlyFailed` / `status.fixed` since the previous
  scan, surfaced on the Overview.
- Guided remediation: MachineConfigPool-paused batch apply (single reboot window)
  plus a console batch flow; MissingDependencies surfaced as blocked.
- TailoredProfile authoring from the console (create/edit rules, bind).
- Editable scan schedule from the UI; per-profile score history and trend.
- Optional severity-weighted score (`spec.scoring.mode`: `Flat` default or
  `SeverityWeighted`).
- Compliance report export (printable HTML).
- Native console score-trend dashboard: operator reconciles a
  `console.openshift.io/dashboard` ConfigMap under Observe → Dashboards (no
  Grafana). ServiceMonitor and PrometheusRule ship in the OLM bundle (inert until
  user-workload monitoring is enabled).
- NSA/CISA hardening sample TailoredProfile
  (`operator/config/samples/tailored-nsa-cisa.yaml`).
- Dynamic informer watch on Compliance Operator CRs (event-driven reconcile;
  1-minute poll retained as fallback).

### Changed

- **Scoring / status behavior**: a check the Compliance Operator marks
  `INCONSISTENT` only because it PASSes on nodes where it applies and is
  NOT-APPLICABLE elsewhere is now treated as PASS in score, counts, metrics,
  and the console. Only a genuine PASS-vs-FAIL (or ERROR) node split stays
  INCONSISTENT. **Upgrade impact**: existing clusters may see fewer
  INCONSISTENT checks and a higher compliance score and
  `baseline_security_compliance_score` after upgrade without any remediations
  being applied. Dashboards and alerts keyed on those series can change.

### Removed

- **Helm chart** (`deploy/helm/`): OLM bundle + file-based catalog is the only
  supported install path. **Upgrade impact**: clusters installed via Helm must
  migrate to an OLM CatalogSource + Subscription (or `make deploy` for
  development). There is no automated Helm → OLM conversion.

### Migration notes (0.3.x → 0.4.0)

1. Install path must be OLM (or `make deploy`); Helm is gone.
2. Expect score/INCONSISTENT metrics and UI badges to shift as described under
   Changed. If you alert on absolute score thresholds, re-baseline after upgrade.
3. New API fields (`spec.waivers`, `spec.scoring`, batch remediation status) are
   optional and default-safe; existing CRs keep working without edits.
4. Metrics scrape objects now ship in the bundle. You no longer need to hand-apply
   `operator/config/prometheus/servicemonitor.yaml` for a standard OLM install
   (user-workload monitoring still must be enabled for scrapes to fire).

## [0.3.1] - 2026-07-11

OLM upgrade edge: `v0.3.1` replaces `v0.3.0`.

### Changed

- Per-profile Overview cards show Inconsistent counts (previously only on the
  composition donut).
- Dark-theme console coverage and screenshots.

### Fixed

- Stuck-install grace and errorMessage guard behavior from the 0.3.0 line
  carried forward; full e2e re-verified on OCP 4.22 / Compliance Operator 1.9.1.

## [0.3.0] - 2026-07-10

OLM upgrade edge: `v0.3.0` replaces `v0.2.1`.

### Added

- TailoredProfile binding via `spec.tailoredProfiles`; tailored results in
  score/status.
- Scheduled next-run time in status; `relatedObjects`; `hack/must-gather.sh`.
- Prometheus metrics and PrometheusRule alerts
  (`ComplianceScoreLow`, `ComplianceChecksFailing`).
- Console: composition donut, per-profile and tailored score cards, CSV export,
  check-resource deep-link, remediation rendered-object view and MCP-aware apply,
  loading skeletons, next-scan time.
- Console cluster Overview details item for the compliance score.
- Waivers and INCONSISTENT drill-down (MachineConfigPool) foundations used by
  later 0.4 work.

### Changed

- Dropped the premature `features.operators.openshift.io/disconnected: "true"`
  claim until published images are digest-pinned for air-gapped installs.

## [0.2.1] - 2026-07-09

OLM upgrade edge: `v0.2.1` replaces `v0.2.0`.

### Fixed

- Bundle `installModes` aligned for cluster-wide (`AllNamespaces`) install.
- Packaging: relatedImages, upgrade edge, bundle validation in CI.

## [0.2.0] - 2026-07-09

Initial packaged release.

### Added

- Cluster-scoped `ClusterBaseline` API (`baselinesecurity.io/v1alpha1`).
- Operator: install/adopt Compliance Operator, own ScanSetting + bindings,
  deploy console plugin, aggregate score + history into status.
- Console plugin under Administration → Compliance (Overview, Results,
  Remediations, Profiles).
- OLM bundle + file-based catalog; string-enum spec; OpenShift-style conditions.

[Unreleased]: https://github.com/maci0/baseline-security-operator/compare/v0.4.0...HEAD
[0.4.0]: https://github.com/maci0/baseline-security-operator/compare/v0.3.1...v0.4.0
[0.3.1]: https://github.com/maci0/baseline-security-operator/compare/v0.3.0...v0.3.1
[0.3.0]: https://github.com/maci0/baseline-security-operator/compare/v0.2.1...v0.3.0
[0.2.1]: https://github.com/maci0/baseline-security-operator/compare/v0.2.0...v0.2.1
[0.2.0]: https://github.com/maci0/baseline-security-operator/releases/tag/v0.2.0
