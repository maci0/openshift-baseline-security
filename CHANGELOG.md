# Changelog

All notable changes to this project are documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project follows [Semantic Versioning](https://semver.org/spec/v2.0.0.html)
with **0.x** rules: the public contract is still evolving. The API group version
is `v1alpha1`, the OLM channel and CSV maturity are `alpha`, and breaking
changes may land in minor bumps until 1.0. Prefer reading each release's
**Changed** / **Removed** sections and **Migration notes** before upgrading.
Security fixes belong under a **### Security** heading (include CVE IDs when
assigned).

Supported host: OpenShift 4.22 (`com.redhat.openshift.versions: =v4.22`,
`minKubeVersion: 1.35.0`). Older or newer OCP releases are not claimed (the
`=` pins to exactly 4.22; a bare `v4.22` would also advertise 4.22 and later).
The console plugin declares `@console/pluginAPI` as `>=4.22.0-0 <4.23.0-0` so
it does not advertise compatibility with untested console majors.

**Consumer contract** (what versioning and this changelog cover):

- **In contract**: `ClusterBaseline` `spec` and user-facing `status` fields
  (score, conditions, profiles, tailoredProfiles, history, newlyFailed, fixed,
  remediationBatch, relatedObjects, scan times, complianceOperatorVersion);
  Prometheus metric and alert names shipped in a given release; OLM package
  `baseline-security-operator` channel `alpha` and the `replaces` upgrade
  edge; console plugin routes and extensions under Administration → Compliance.
- **Out of contract (may change in 0.x without a major bump)**:
  `status.previousFailures`, `status.diffBaseFailures`,
  `status.diffBaseScanTime` (scan-diff bookkeeping; use newlyFailed/fixed);
  controller-internal env vars and RBAC names not exposed on the CR; anything
  still only under **[Unreleased]**.

**Support window**: only the latest published 0.x release receives fixes and
security updates. There is no backport stream on older 0.x lines; upgrade to
the latest 0.x for patches. See [SECURITY.md](SECURITY.md) for reporting.
Published image/tag/CSV version strings are immutable: never re-push or
re-tag an already published version. Each cut must also create an immutable
git tag `vX.Y.Z` (never force-moved); the compare links in the footer below
depend on those tags.

## [Unreleased]

### Added

- Disable scanning by clearing `spec.profiles` to an empty list (with no
  `spec.tailoredProfiles`): the operator prunes the ScanSettingBindings and
  clears the score while keeping the CR and its history. New installs still
  default to `{cis}`. The console Profiles tab allows clearing the last profile;
  Overview shows a "Scanning is disabled" notice.
- Overview **Recent changes** card for `status.newlyFailed` / `status.fixed`
  regressions and recoveries since the previous completed scan.
- Results table **Profile** column (filterable with the existing profile facet).
- Prometheus metrics (post-0.4.0; not in published 0.4.0 tags):
  `baseline_security_status_observed_timestamp_seconds` (Unix time this
  replica last published status metrics; HA scrapers pick the newest
  publisher), `baseline_security_remediation_batch_active` (1 while an
  MCP-paused batch is in progress), `baseline_security_condition{type}`
  (Available/Progressing/Degraded as 0/1 gauges),
  `baseline_security_last_scan_timestamp_seconds` (Unix time of the last
  completed scan, `status.lastScanTime`; 0 when never scanned or when
  scanning is disabled via empty profiles/tailored so `ComplianceScanStale`
  does not page for intentional off), `baseline_security_newly_failed`
  (count of `status.newlyFailed` regressions since the previous completed
  scan), and `baseline_security_remediation_batch_started_timestamp_seconds`
  (Unix start of the active MCP-paused remediation batch from
  `status.remediationBatch.startedAt`; 0 when no batch; Observe dashboard
  pause-age panel). Score/check series
  (`baseline_security_compliance_score`, `baseline_security_checks`) remain
  as in 0.3/0.4.
- PrometheusRule alerts (post-0.4.0): `ComplianceChecksInError`,
  `ComplianceChecksInconsistent` (genuine multi-node PASS-vs-FAIL drift after
  benign NOT-APPLICABLE collapse; `for: 1h`), `ComplianceStatusStale`,
  `RemediationBatchStuck`, `ClusterBaselineDegraded`, `ComplianceScanStale`
  (no completed scan in 36h), and `ComplianceRegressions` (new check failures
  since the last scan). 0.3/0.4 still ship only `ComplianceScoreLow` and
  `ComplianceChecksFailing`.
- Dynamic informer watch on Compliance Operator CRs (event-driven reconcile;
  1-minute poll retained as fallback). Deferred from the 0.4.0 cut; not in
  any published 0.4.0 image/CSV tag.

### Changed

- `spec.profiles` no longer requires at least one entry (the `MinItems=1`
  constraint was dropped) so scanning can be turned off as above. The field
  remains required in the OpenAPI schema and still defaults to `{cis}` when
  omitted; only an explicit empty list disables scanning. **Upgrade
  impact**: none for existing CRs; validation is only relaxed.
- `spec.complianceCatalogSource` is now validated as a non-empty DNS-1123
  subdomain (a CatalogSource `metadata.name`). Previously any string up to 253
  characters was accepted. **Upgrade impact**: a CR whose catalog-source
  override is not a valid DNS-1123 subdomain (uppercase, spaces, or empty) is
  rejected on next apply; `redhat-operators` and standard names are unaffected.
- Remediation batch reconcile runs before Compliance Operator / scan / plugin
  ensure, and requeues every 15s while a batch is `Applying`, so MCP pause
  lifecycle is less likely to stall behind dependency install.
- CRD status lists `status.conditions`, `status.profiles`, and
  `status.tailoredProfiles` are now `x-kubernetes-list-type: map` (keyed by
  `type` / `key` / `name`; conditions also `patchStrategy: merge`) so
  Server-Side Apply and strategic merges update one entry without replacing the
  whole list. **Upgrade impact**: none for the operator, which owns and rewrites
  status with unique keys; a client doing SSA or strategic-merge-patch against
  these status arrays now gets keyed map-merge instead of atomic replacement.
- Scan-diff (`status.newlyFailed` / `status.fixed`) now tracks the raw FAIL
  outcome: a waived FAIL still counts as a FAIL for regression tracking, so
  waiving a check no longer lists it under `fixed` and un-waiving no longer
  lists it under `newlyFailed`. Score, `ResultCounts`, and the Waived bucket
  are unchanged (waivers still exclude the check from the pass/fail
  denominator). **Upgrade impact**: on the first scan after upgrade, clusters
  with checks that are both FAIL and waived may see those checks appear in
  `status.newlyFailed`, raising `baseline_security_newly_failed` and possibly
  firing `ComplianceRegressions`. This is a display/alert change only; the
  compliance score is not affected.
- `ComplianceScoreLow` and `ComplianceChecksFailing` expressions now select
  the newest publishing replica via
  `baseline_security_status_observed_timestamp_seconds` (HA-safe) instead of
  a plain `max`/`sum` over all instances. **Upgrade impact**: single-replica
  installs behave the same; multi-replica HA no longer double-counts checks
  or lets a stale leader mask a lower score after failover.

### Fixed

- `status.newlyFailed` / `status.fixed` no longer flip transiently while a
  scan's results settle: a scan-before-last FAIL snapshot is retained so late
  CheckResult events correct the diff. Regression lists clear when compliance
  CRDs are missing.
- MachineConfigPool-paused batch apply: stuck pauses from corrupt/far-future
  `StartedAt`, transient remediation Get errors, partial pause rollback,
  cancel-resume, resume pools on ClusterBaseline delete, and pool derivation
  for multi-pool node remediations.

### Migration notes (0.4.x → next)

1. If you set `spec.complianceCatalogSource`, ensure it is a DNS-1123 subdomain
   matching a CatalogSource `metadata.name` (for example `redhat-operators`).
   Invalid overrides that previously applied will be rejected on the next
   create/update after upgrade.
2. To disable scanning, set `spec.profiles: []` (and leave
   `spec.tailoredProfiles` empty or omit it). Do not omit `spec.profiles`:
   the field is still required and defaults to `{cis}`. Existing non-empty
   profiles keep working without edits.
3. If user-workload monitoring scrapes the operator, expect additional alerts
   after upgrade beyond the 0.4 set (`ComplianceScoreLow`,
   `ComplianceChecksFailing`): `ComplianceChecksInError`,
   `ComplianceChecksInconsistent` (genuine multi-node PASS-vs-FAIL drift after
   benign NOT-APPLICABLE collapse; `for: 1h`), `ComplianceStatusStale`,
   `RemediationBatchStuck`, `ClusterBaselineDegraded`, `ComplianceScanStale`
   (no completed scan for 36h), and `ComplianceRegressions`
   (`status.newlyFailed` non-empty). Silence or retune if your schedule is
   intentionally slower than daily, if multi-node drift is expected in your
   topology, or if you run multi-replica and previously relied on non-HA
   alert math.
4. Waiving a FAIL no longer clears it from regression tracking: expect
   waived FAILs to stay out of `status.fixed` and to remain (or appear) in
   `status.newlyFailed` until the check actually PASSes. Score and the
   Waived result bucket are unchanged.
5. Do not depend on `status.previousFailures`, `status.diffBaseFailures`, or
   `status.diffBaseScanTime`; they are internal scan-diff bookkeeping and
   may change in 0.x without a major bump. Use `status.newlyFailed` and
   `status.fixed` for user-facing regression views.
6. Clients that Server-Side Apply or strategic-merge-patch
   `status.conditions` / `status.profiles` / `status.tailoredProfiles` should
   expect keyed map-merge (by `type` / `key` / `name`) instead of atomic
   list replacement.

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
  supported install path. The chart existed only on `main` during early 0.4
  development (never an OLM channel alternative for published 0.2/0.3).
  **Upgrade impact**: OLM installs are unaffected. Anyone who applied the
  pre-release chart from `main` must migrate to an OLM CatalogSource +
  Subscription (or `make deploy` for development). There is no automated
  Helm → OLM conversion.

### Migration notes (0.3.x → 0.4.0)

1. Stay on OLM (or `make deploy` for development). Published 0.2/0.3 never
   shipped a Helm chart; only pre-release installs from `main` need to leave
   Helm for CatalogSource + Subscription.
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
- Scheduled next-run time in status; `relatedObjects`; `operator/hack/must-gather.sh`.
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

- Cluster-scoped `ClusterBaseline` API (`baselinesecurity.openshift.io/v1alpha1`).
- Operator: install/adopt Compliance Operator, own ScanSetting + bindings,
  deploy console plugin, aggregate score + history into status.
- Console plugin under Administration → Compliance (Overview, Results,
  Remediations, Profiles).
- OLM bundle + file-based catalog; string-enum spec; OpenShift-style conditions.

[Unreleased]: https://github.com/maci0/openshift-baseline-security/compare/v0.4.0...HEAD
[0.4.0]: https://github.com/maci0/openshift-baseline-security/compare/v0.3.1...v0.4.0
[0.3.1]: https://github.com/maci0/openshift-baseline-security/compare/v0.3.0...v0.3.1
[0.3.0]: https://github.com/maci0/openshift-baseline-security/compare/v0.2.1...v0.3.0
[0.2.1]: https://github.com/maci0/openshift-baseline-security/compare/v0.2.0...v0.2.1
[0.2.0]: https://github.com/maci0/openshift-baseline-security/releases/tag/v0.2.0
