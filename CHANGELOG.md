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
  `baseline-security-operator` channel `alpha` (each bundle a standalone
  channel head; no `replaces` upgrade graph is maintained pre-release);
  console plugin routes and extensions under Administration → Compliance.
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

### Fixed

- OperatorHub lists the package as Basic Install. The CSV advertised Seamless
  Upgrades even though 0.x bundles have no `replaces` graph, so OLM cannot
  upgrade between versions.
- Console: repeated Export HTML report clicks no longer pin extra blob URLs
  (and their report documents) in the browser until the tab is closed.
- Console: the score scale "100" is formatted with the console locale, so
  native-digit locales (ar-SA, fa, ...) no longer mix Latin 100 with a
  localized score.
- Console: exported HTML reports take the console language for dates and
  counts, set RTL from the locale when document dir is unset, and isolate
  bidirectional check and waiver text so it cannot reverse surrounding
  punctuation.
- Operator and console: INCONSISTENT per-node status tokens fold ASCII
  case only. A sharp s (`paß`) or Turkish dotless i (`faıl`) in a
  Compliance Operator annotation no longer maps to PASS or FAIL.

### Changed

- Operator, plugin, bundle, and catalog images ship the Apache-2.0 license at
  `/licenses/LICENSE` and OCI source, license, and version labels.
- OperatorHub CSV links include the project License.
- Console: exported HTML compliance reports use PatternFly type scale and the
  same 60/90 score colors as Overview, so a printed report is not a generic
  table dump.
- Observe dashboard: the compliance score singlestat uses the same 60/90
  color bands as the console (PatternFly danger red, not a generic Grafana
  red). Alert threshold ComplianceScoreLow stays at 80.
- Operator and plugin image COPY steps pin file modes so a host umask cannot
  change the published layer digest.
- Console plugin image install skips package lifecycle scripts and Playwright
  browser download, so the webpack layer cannot fetch unpinned binaries.
- OperatorHub CSV `capabilities` is `Basic Install` (was `Seamless
  Upgrades`). Pre-1.0 bundles have no `replaces` graph, so the listing must
  not advertise an OLM upgrade path that does not exist.
- Operator: `baseline_security_condition` now also publishes the detail
  conditions ComplianceOperatorReady, ScanConfigured, ScanStorageReady, and
  ConsolePluginReady, so plugin and dependency readiness is visible on the
  Observe dashboard without scraping the CR.
- Support: ClusterBaseline must-gather dumps omit waiver `requestedBy` and
  `approvedBy`, and drop `kubectl.kubernetes.io/last-applied-configuration` so
  those names are not copied into a support archive.
- Operator: watch compliance CRs as metadata only, and page
  ComplianceCheckResult lists (500 per call), so multi-profile scans with
  thousands of results no longer pin full objects in the informer or a
  single unbounded List response.
- Console: Profiles shows the same "Scanning is disabled" banner as the other
  tabs when no profile is selected.
- Console: Remediations empty copy no longer tells admins to rescan after new
  failures when failing checks may already exist without auto-fixes.
- Console plugin nginx now gzip-compresses JavaScript and JSON (`Vary:
  Accept-Encoding`) and accepts HTTP/2 on 9443, so plugin assets transfer in
  fewer bytes and multiplex on one connection.
- Overview paints score cards before loading Victory charts. Results,
  Remediations, and Profiles load when those tabs are opened. A Retry alert
  is shown if a chunk fails to load.

### Security

- Console: `yarn install` no longer runs package lifecycle scripts
  (`enableScripts: false`, image `YARN_ENABLE_SCRIPTS=false`). A compromised
  transitive with a `postinstall` cannot execute during local/CI install or
  the plugin image build.

### Fixed

- Support: must-gather waiver redaction runs with BSD sed, so a dump collected
  on macOS still strips `requestedBy`/`approvedBy`.
- Operator CI image runtime stage now honors `SOURCE_DATE_EPOCH` (it was only
  set on the builder), so CI and release image timestamps can match.
- Operator: a hung apiserver call during reconcile now times out after 5
  minutes instead of occupying the singleton worker until process restart
  (batch resume, score, and plugin ensure could not proceed while one List
  was stuck).
- Operator: a Compliance Operator CSV whose `status.phase` is the wrong type
  now reports the parse error on `ComplianceOperatorReady` instead of
  `phase=unknown`.
- Operator: RESTMapper failures other than missing CRDs while starting
  compliance watches are logged, so a mapper or RBAC problem is visible
  instead of only the 1m poll.
- Console: a date-only waiver expiry on a DST fall-back that repeats 23:00
  (for example America/Sao_Paulo 2019-02-16) stays active through the selected
  local day instead of ending an hour early.
- Console: `expiresAt`/`reviewBy` values with hour 24, minute 60, or second 60
  are rejected in the waive form, matching apiserver RFC3339 admission, instead
  of a patch that then 422s.
- Support: `must-gather.sh --help` prints usage instead of treating the flag
  as an output directory; unknown options and extra arguments exit 2; a
  partial collection now exits 1 so scripts notice an incomplete archive.
- Operator: invalid process flags (listen address, cert dir, leftover
  arguments) exit 2; `--help` writes usage to stdout, including env vars.
- Console: a second Waive of the same check could add a duplicate
  `spec.waivers` entry when the first patch had already landed.
- ClusterBaseline: two `spec.waivers` entries with the same check name
  were admitted (`listType=map` is a merge key, not uniqueness).
- Aggregated `baseline-security-viewer` can list TailoredProfiles and Rules
  (the Profiles tab watches both; a view-only user previously got a catalog
  watch error). `baseline-security-admin` can create and update
  TailoredProfiles, matching the console authoring flow.
- Operator: an unrecognized `BASELINE_SECURITY_SKIP_DEFAULT_CR` value now
  fails at process start instead of silently creating
  `ClusterBaseline/cluster`. Known true/false spellings (true/false, 1/0,
  yes/no, on/off, and the same aliases as before) are unchanged; unset still
  creates the default CR.
- Console: the tailored-profile editor's "Scans N of M base rules" readout
  counted extra enabled rules inside N, so enabling extras on a 100-rule base
  could read as "Scans 105 of 100 base rules, plus 5 added." N is now the
  remaining base rules; extras stay in the plus-added clause. Disable rules
  that are not in the current base no longer drag N below the real remainder.
- Console: with a profile selected but no scans yet, Rescan now told admins
  to enable a profile. The tooltip now says the first scan starts once
  profiles are bound.
- Console: Overview Details still showed a next-scan time after scanning was
  turned off, so it looked like a scan was still scheduled.
- Console: the missing-ClusterBaseline empty state sent admins to OperatorHub
  even though the plugin is already running. It now says the operator creates
  `ClusterBaseline/cluster` and offers Create default baseline when the user
  can create the CR.
- Console: Results with no rows told first-time admins to click Rescan now
  (often still disabled) instead of waiting for the automatic first scan.
- Console: the newly-failing Overview alert linked to the unfiltered Results
  table. It now lists the named checks.
- Console: waived checks on the Results table used the same grey label as
  not-applicable rows (WAIVED had no status style of its own). They now use
  a teal label, matching the Overview composition donut.

### Security

- `baseline-security-admin` is no longer aggregated onto the built-in
  `admin` ClusterRole. A RoleBinding to `admin` in `openshift-compliance`
  no longer grants namespaced patch on ComplianceRemediations (node
  reboots) or ComplianceScans. Bind `baseline-security-admin` explicitly,
  or use cluster-admin. ClusterBaseline writes on that role are
  name-scoped to `cluster`.
- Console: CSV export strips Unicode format characters (zero-width, BIDI, BOM)
  before formula-prefix hardening, so a hidden `=` in a check title cannot
  reach a spreadsheet as a live formula.
- Console: waiver `requestedBy` / `approvedBy` / `reason` drop control and
  BIDI marks so audit names cannot spoof another identity in the UI or
  printable report.
- Console plugin nginx no longer includes the UBI extra conf directories,
  which can start a plaintext listener beside TLS port 9443.

### Security

- Operator binary is linked as a PIE (`-buildmode=pie`) in the Makefile and
  both Dockerfiles.

### Docs

- Threat model: add `docs/THREAT_MODEL.md` (entry points, trust boundaries,
  ranked threats, mitigations mapped to code) and link it from SECURITY.md.
- Document operator process flags and env vars in the README, and comment
  the optional ClusterBaseline spec fields on the sample CR.
- README Versioning: the no-`replaces` upgrade path is install-the-new-head
  (CatalogSource tag + delete a leftover Subscription/CSV); OperatorHub
  capability is Basic Install.

### Migration notes

- If `BASELINE_SECURITY_SKIP_DEFAULT_CR` is set to anything other than a
  known true/false spelling, the operator now exits at process start
  instead of silently creating `ClusterBaseline/cluster`. Unset, empty,
  and known false spellings still create the CR; known true spellings
  still skip it.
- Moving from an installed 0.5.x CSV is not an OLM auto-upgrade (no
  `replaces` graph since 0.5.5). Point the CatalogSource at the new catalog
  tag and install that head; delete a leftover Subscription/CSV.
  ClusterBaseline CRs stay.

## [0.5.15] - 2026-08-26

### Security

- Go: bump the toolchain pin 1.26.5 -> 1.26.6 (`go.mod`, both Dockerfiles).
  Clears six standard-library advisories govulncheck reported as reachable
  from `Reconcile` and `main`, among them GO-2026-5972 (`encoding/asn1`) and
  GO-2026-5026 (`net/http` via `golang.org/x/net/idna`). govulncheck now
  reports no called vulnerabilities.
- Console: pin resolutions `fast-uri` ^3.1.5 (GHSA-7p8r-x3mc-p8w7, high),
  `js-yaml@^3.13.1` ^3.15.1 (GHSA-5p4m-2wfm-xmqj, high), and `undici` ^8.9.0
  (GHSA-4cwx-7wf7-3272 high, GHSA-8xcm-r25x-g524 moderate, via node-gyp).
  `yarn npm audit` reports no suggestions.

### Fixed

- CI: run `yarn lint:oxlint` in the console-plugin job. The type-aware
  anti-slop rules were configured but never gated, so a violation could land
  on `main` unnoticed.

### Docs

- Add `AGENTS.md` at the repo root and under `operator/` and `console-plugin/`:
  the gate, the version lockstep, generated-file rules, and the per-component
  conventions. `CLAUDE.md` symlinks to each.

## [0.5.14] - 2026-08-26

### Added

- Release workflow: CycloneDX SBOMs for the operator, console-plugin, bundle,
  and catalog images, generated in a separate job (publish digests unchanged),
  uploaded as artifacts and attached to the GitHub release when one exists.

### Changed

- Console plugin installs only the Victory chart components it uses instead of
  the `victory` umbrella package, and no longer pulls `@types/react-dom`. No
  rendering change; the build no longer carries candlestick, histogram,
  errorbar, canvas, or brush-line modules.

### Fixed

- Console: a compliance score whose pass/fail counts overflow now renders as no
  score instead of `NaN`/`Infinity`. A `NaN` score compares false against every
  threshold, which painted the badge green on unscoreable data. Donut segments
  and report totals saturate so they stay finite.
- Console: the tailored-profile rule pickers silently dropped matches past the
  100-option cap; they now render a notice saying how many results are hidden.
- Console: profile-catalog watch failures surface as an alert, and a rescan that
  throws synchronously reaches the error banner instead of being swallowed.
- Console: a waiver's stored expiry and the comparison against it now agree on
  the local day boundary, and fail closed on an invalid date-only value.
- Console: the auto-dismiss timer sets its ref in an effect rather than during
  render (a render-phase ref write is unsafe under concurrent rendering).
- Operator: default-`ClusterBaseline` creation retries the cache sync while the
  process is live instead of giving up after one failed pass.
- Operator: an unreadable metrics certificate is logged once per episode with
  its cause, and `Infrastructure` topology read failures are rate-limited
  instead of silently forcing the HA console-plugin layout on single-node.
- Operator: finishing a remediation batch tolerates a corrupt restored batch
  (list capped, invalid remediation names treated as missing) instead of issuing
  a Get per entry until the grace period expires.
- Operator: resuming an orphaned batch clears its annotations through the shared
  helper, so a concurrent resubmit carrying real remediation names survives.

### Security

- Release workflow: the `workflow_dispatch` version input is passed through an
  env var instead of being expanded into `run:`, where a crafted input could
  execute arbitrary commands.
- Go: bump indirect grpc 1.79.3 -> 1.82.1 (GO-2026-6061) and otel 1.40.0 ->
  1.43.0 (GO-2026-5506; grpc 1.82.1 requires otel 1.43.0). govulncheck reports
  no called vulnerabilities.
- Console: pin resolutions brace-expansion ^5.0.8 (GHSA-mh99-v99m-4gvg, high)
  and tar ^7.5.21 (GHSA-r292-9mhp-454m, via node-gyp). `yarn npm audit` is
  clean.

### Docs

- README: add a Quickstart with a complete `oc apply` install sequence at the
  top of the file.
- Clarify that `spec.scoring.mode` is only ever changed by editing the CR; the
  console reads it and never patches it.
- Each ADR now carries the date it was recorded, and new records must have one.
- Repo hygiene: text files pinned to LF via `.gitattributes` (a CRLF checkout
  breaks the make recipes, `hack/*.sh` shebangs, and Dockerfile `RUN` lines).

## [0.5.13] - 2026-07-23

### Changed

- Console: the tailored-profile rule pickers are now typeahead multi-selects.
  Type to filter the base profile's rules (disable) or search the full catalog
  (enable extra rules); selections show as removable chips, and a live readout
  reports the effective rule count (`Scans N of M base rules`) as you edit. The
  dropdown scrolls for long result lists.

### Fixed

- Install: ship a `prometheus-k8s` Role/RoleBinding granting the platform
  Prometheus service account read on services/endpoints/pods in the operator
  namespace. The `openshift.io/cluster-monitoring` label adds the namespace to
  Prometheus's selector, but cluster monitoring only auto-grants this
  service-discovery RBAC to namespaces it manages; without it, target discovery
  found zero endpoints and no metrics were scraped, firing `ComplianceStatusStale`
  on an otherwise healthy operator.

### Docs

- README: document installing from the published Quay catalog
  (`quay.io/openshift-baseline-security/baseline-security-operator-catalog`) as
  the recommended path, alongside the existing build-from-source instructions.

## [0.5.12] - 2026-07-23

### Changed

- Console: reworked the create/edit tailored-profile modal. Current rule
  selections show as removable chips with a count badge; the advanced
  enable-extra-rules picker is a collapsible section (auto-opens when editing a
  profile that already has enable rules); a one-line intro explains the base +
  disable + enable model; clearer search/chip copy.

## [0.5.11] - 2026-07-23

### Added

- Console: the "New tailored profile" form now uses selections instead of
  free text: a base-profile dropdown, a filterable disable-rules picker (from
  the base profile's rules), and an enable-extra-rules picker over the full
  Compliance Operator rule catalog.
- Console: existing bound tailored profiles can be edited (base profile and
  enable/disable rule sets) from an Edit action on each card.

### Fixed

- Console Overview: per-benchmark score cards bottom-align their trend charts,
  the compliance-score donut is centered (no empty gap), and the score-trend
  x-axis label no longer clips at the card edge.
- Console: a create retry after a transient bind failure now correctly adopts
  a tailored profile that has enable rules (the AlreadyExists match previously
  ignored enableRules).

## [0.5.10] - 2026-07-23

### Fixed

- Console: the per-benchmark score cards now bottom-align their trend charts,
  so the sparklines line up across cards even when a card has an extra status
  row (e.g. PCI-DSS with Inconsistent).

### Security

- Base-image CVE patches (digest bumps to ubi9 `nodejs-22`, `nginx-120`, and
  `go-toolset`; the language versions stay pinned by `.nvmrc` / `GOTOOLCHAIN`).

## [0.5.9] - 2026-07-23

### Added

- Release workflow publishing the operator, console-plugin, OLM bundle, and
  catalog images to `quay.io/openshift-baseline-security/*` (at `:VERSION` and
  `:latest`) on a version tag, so a tag push yields an OLM-installable release.
- README: in-cluster build + kustomize install path (binary `oc` BuildConfigs
  to the internal registry, then `make deploy`) for dev/lab/disconnected
  clusters with no external registry.

### Security

- Bump `golang.org/x/text` v0.37.0 -> v0.39.0 (GO-2026-5970: infinite loop on
  invalid input, reachable via the status-update text-normalization path).
- Pin `fast-uri` to `^3.1.4` (host confusion via a literal backslash authority
  delimiter in ajv's transitive 3.1.3).

## [0.5.8] - 2026-07-20

### Fixed

- Switching `spec.scoring.mode` no longer wipes the score history on every
  subsequent scan: the durable history scoring-mode stamp was read back from
  the status-update response (which resets in-memory metadata), so it never
  advanced and each completed scan re-entered the mode-mismatch clear path,
  permanently capping `status.history` at one point.
- `ComplianceScanStale` no longer false-fires on irregular cron cadences: the
  `baseline_security_scan_interval_seconds` gauge now reports the largest gap
  between consecutive fires (a weekday-only schedule reports the 72h weekend
  gap, not the 24h midweek one).
- A ComplianceCheckResult carrying a raw `WAIVED` status (tampered or a future
  Compliance Operator status) now counts as Error instead of silently entering
  the Waived bucket without a `spec.waivers` entry, matching the console.
- `status.conditions` is now bounded in aggregate size (256 KiB, measured at
  serialized size); hand-written oversize condition lists can no longer push
  the object past the apiserver limit and freeze status updates.
- Upgraded clusters whose ClusterBaseline was written under the 0.5.6 CRD no
  longer stay pinned to the `redhat-operators` catalog on OKD: a spec value
  equal to the old schema default falls through to catalog auto-detection.
- `make deploy` upgrades now remove the pre-0.5.7 static operator
  PodDisruptionBudget that `oc apply` never prunes (SNO drain deadlock).
- Console: remediation Apply/Unapply confirmations patch the live object, so
  a retry after a concurrent update no longer fails indefinitely on a stale
  resourceVersion; orphan-waiver removal failures now surface an error where
  the action was taken; the tailored-profile form resets fully on success.
- Console plugin assets now ship explicit Cache-Control headers (immutable
  hashed chunks, no-cache manifest), fixing a stale-manifest failure after
  operator upgrades that broke the plugin until a hard refresh.

## [0.5.7] - 2026-07-15

### Changed

- Removed the operator's static PodDisruptionBudget (ADR-028). On single-node
  OpenShift both operator replicas sit on the one node, so a `minAvailable: 1`
  PDB deadlocked a voluntary node drain (first eviction succeeds, its
  replacement cannot schedule, the second is denied forever). Leader election
  already guarantees a single active reconciler; two replicas are kept for fast
  failover.

### Removed

- Trimmed three unused verbs from the operator ClusterRole (least privilege):
  `persistentvolumeclaims` get, `scansettings` delete, and `consoleplugins`
  list/watch. None were exercised on any code path.

### Fixed

- ComplianceScanStale alert is now cadence-aware: it fires when the last scan is
  older than 1.5x the configured scan interval instead of a hardcoded 36h, which
  false-paged continuously on any non-daily schedule (a weekly scan is
  legitimately days old). Adds a `baseline_security_scan_interval_seconds` metric.
- A syntactically valid but never-firing schedule (an impossible calendar date
  such as April 31) now reports `InvalidSchedule`/Degraded and keeps the
  last-good cron, instead of silently disabling scanning while suppressing the
  stale-scan alert (ADR-029).
- ScanStorageReady no longer flaps: the pending-PVC names in the condition
  message are sorted, so an unchanged set of stuck PVCs stops rewriting the
  status (and requeuing) every reconcile.
- Score history no longer accumulates duplicate points when a scan's
  `endTimestamp` carries sub-second precision; it is truncated to the whole
  second that status persistence uses.
- Console: a compliance result with a non-string check name no longer crashes
  the Results tab, and an unserializable remediation object renders a localized
  placeholder instead of a raw sentinel.

## [0.5.6] - 2026-07-14

### Changed

- Dependency maintenance: CI actions (actions/checkout v7, docker/setup-buildx
  v4, the latter clearing the Node 20 deprecation warning), ubi9 base-image
  digests (same Node 22.23.1 / Go 1.26.5), and console-plugin
  @patternfly/react-charts 8.6.0, eslint 10, webpack-dev-server 6. k8s.io/*
  0.36, controller-runtime 0.24, @types/node 26, and react 19 were held back to
  stay on the OpenShift 4.22 / k8s 1.35 / React 18 support baseline.

### Added

- Console e2e coverage for the Overview/Remediations governance affordances
  (inline schedule edit, invalid-cron rejection, scoring-mode readout, score
  trend card, HTML report export, batch-apply confirmation).

### Fixed

- e2e: the remediation apply-confirmation screenshot spec matched the wrong
  button (name-scoped "Apply <name>") and silently skipped; it now captures the
  confirmation modal.

## [0.5.5] - 2026-07-14

### Fixed

- Single-node OpenShift (SNO) console-plugin drain deadlock. On `SingleReplica`
  infrastructure topology the plugin now deploys a single replica and no
  PodDisruptionBudget, so draining the one node during a cluster upgrade is no
  longer refused by an un-evictable second plugin pod. Multi-node clusters keep
  the 2-replica Deployment plus the `minAvailable=1` PDB. Topology is read from
  the cluster `Infrastructure` singleton; any read error fails safe to the HA
  layout.
- History scoring-mode stamp is now realigned on the reconcile error path. A
  scoring-mode change (flat vs severity-weighted) coinciding with a transient
  post-aggregation error no longer leaves the durable stamp lagging its rings,
  which had fired a spurious `historyScoringModeMismatch` for one scan interval.

### Changed

- Operator RBAC tightened to least privilege: dropped unused `list`/`watch` on
  `scansettings` and `machineconfigpools`, and `watch` on `scansettingbindings`.
  These are accessed by name (Get/Patch) or a one-shot List only, never watched,
  so the verbs were dead grants. OLM applies the narrowed ClusterRole on upgrade.
- Dropped the OLM `replaces` upgrade graph (CSV `spec.replaces` and catalog
  channel edges). Each bundle is a standalone `alpha` channel head.
  **Upgrade impact**: OLM will not auto-upgrade an installed 0.5.0 (or
  earlier) CSV to 0.5.5. Point the CatalogSource at the 0.5.5 catalog tag
  and install that head; delete the previous Subscription/CSV if it
  remains. ClusterBaseline CRs and the CRD stay.

### Added

- `make verify-bundle-static` (run in CI and `make bundle`): fails if a
  hand-copied `bundle/manifests/` file drifts from its `config/` source, closing
  the last unguarded release-packaging path (CRD, PrometheusRule, ServiceMonitor,
  and CSV RBAC were already checked).

## [0.5.0] - 2026-07-13

OLM upgrade edge: `baseline-security-operator.v0.5.0` replaces `v0.4.0`.

**Breaking:** the API group was renamed `baselinesecurity.io` →
`baselinesecurity.openshift.io`. This minor carries it (a hard rename at
`v1alpha1`, no conversion) per the project's 0.x policy that breaking changes
land in a minor bump. Existing `ClusterBaseline` CRs are under the old group and
must be recreated after upgrade (see Migration notes).

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
- OKD support: when `spec.complianceCatalogSource` is unset, the operator
  auto-detects the Compliance Operator catalog: `redhat-operators` on OCP,
  else `community-operators` when only that exists (OKD). An explicit value
  still wins (disconnected mirrors). Read-only `catalogsources` RBAC added.
- `registry.ci.openshift.org` build variant (`operator/Dockerfile.ci` +
  `.ci-operator.yaml`) for OpenShift CI / ci-operator onboarding.

### Changed

- **BREAKING: API group renamed** `baselinesecurity.io` →
  `baselinesecurity.openshift.io` (CRD group, `apiVersion`, RBAC, the
  `.../cleanup` finalizer, and all `baselinesecurity.io/...` annotations:
  `batch-apply`, `batch-*`, `history-scoring-mode`, `waived`). Hard rename at
  `v1alpha1`: no conversion. **Upgrade impact**: existing `ClusterBaseline` CRs
  are under the old group and must be recreated after upgrade (see Migration).
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
- Reconcile no longer hangs when the operator holds only named (not cluster-wide
  list/watch) ConfigMap RBAC: ConfigMaps are read uncached, so the console
  dashboard ConfigMap reconcile cannot block on a never-syncing informer.
- Console plugin no longer crash-loops: the nginx `access_log` directive needs a
  format name before `if=`.

### Migration notes (0.4.x → 0.5.0)

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

[Unreleased]: https://github.com/maci0/openshift-baseline-security/compare/v0.5.15...HEAD
[0.5.15]: https://github.com/maci0/openshift-baseline-security/compare/v0.5.14...v0.5.15
[0.5.14]: https://github.com/maci0/openshift-baseline-security/compare/v0.5.13...v0.5.14
[0.5.13]: https://github.com/maci0/openshift-baseline-security/compare/v0.5.12...v0.5.13
[0.5.12]: https://github.com/maci0/openshift-baseline-security/compare/v0.5.11...v0.5.12
[0.5.11]: https://github.com/maci0/openshift-baseline-security/compare/v0.5.10...v0.5.11
[0.5.10]: https://github.com/maci0/openshift-baseline-security/compare/v0.5.9...v0.5.10
[0.5.9]: https://github.com/maci0/openshift-baseline-security/compare/v0.5.8...v0.5.9
[0.5.8]: https://github.com/maci0/openshift-baseline-security/compare/v0.5.7...v0.5.8
[0.5.7]: https://github.com/maci0/openshift-baseline-security/compare/v0.5.6...v0.5.7
[0.5.6]: https://github.com/maci0/openshift-baseline-security/compare/v0.5.5...v0.5.6
[0.5.5]: https://github.com/maci0/openshift-baseline-security/compare/v0.5.0...v0.5.5
[0.5.0]: https://github.com/maci0/openshift-baseline-security/compare/v0.4.0...v0.5.0
[0.4.0]: https://github.com/maci0/openshift-baseline-security/compare/v0.3.1...v0.4.0
[0.3.1]: https://github.com/maci0/openshift-baseline-security/compare/v0.3.0...v0.3.1
[0.3.0]: https://github.com/maci0/openshift-baseline-security/compare/v0.2.1...v0.3.0
[0.2.1]: https://github.com/maci0/openshift-baseline-security/compare/v0.2.0...v0.2.1
[0.2.0]: https://github.com/maci0/openshift-baseline-security/releases/tag/v0.2.0
