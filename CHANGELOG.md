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

Supported host: OpenShift 4.22. Older or newer OCP releases are not claimed.
What the bundle actually enforces is `minKubeVersion: 1.35.0` in the CSV and
the console plugin's `@console/pluginAPI` range `>=4.22.0-0 <4.23.0-0`. There is
no `com.redhat.openshift.versions` label on the CSV, so OLM does not refuse an
install on a 4.23 cluster whose kube version is still 1.35: the pin is
documentation and a declared range, not an admission-time check.

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

### Added

- `make verify-reproducible` (in `make ci` and the GitHub Actions `operator`
  job) builds the manager twice, from two different absolute paths and under a
  different timezone, locale, and umask, and fails unless both binaries hash
  identically. The `-trimpath`, `-buildvcs=false`, and `-buildid=` flags were
  already set in the Makefile and both Dockerfiles, and the release claims the
  local and in-image binaries match, but nothing checked it: a dropped or
  misspelled flag produced a different binary and every image and test run
  still passed.

### Changed

- `docs/THREAT_MODEL.md` brought back in line with the code. The commit stamp
  and eleven line citations across `role.yaml`, `cmd/main.go`, `plugin.go`,
  `plugin_pod.go`, `nginx.conf`, both Dockerfiles, `CompliancePage.tsx`,
  `RemediationsTab.tsx`, and the e2e dotenv loader were stale, and two surfaces
  the model never named are now covered: the leader-election Lease and its
  separate Role, and the operator's cluster-wide RBAC grants. No shipped
  behavior changed.

### Fixed

- `baseline_security_scan_interval_seconds` could report a value that depended
  on which process published it first. The walk behind the gauge started at the
  publisher's clock, so an annual schedule crossing a leap year reported 365d
  from one phase of the year and 366d from another, and the per-process memo
  froze whichever phase arrived first under a key that carries the schedule
  alone. The walk is now anchored to a fixed epoch, so the value is a function
  of `spec.schedule` and nothing else. The `ComplianceScanStale` threshold moves
  by at most one day, and only for annual schedules.
- Release images stamped `org.opencontainers.image.version` from the
  `ARG VERSION` default in each Dockerfile rather than the version being
  published. The release job replaced `DOCKER_BUILD_FLAGS` to drop the
  buildx-only `--provenance`/`--sbom`, and the replacement also dropped
  `--build-arg VERSION`, so all four images fell back to the default. The two
  values were kept equal by `verify-versions`, so nothing shipped mislabeled,
  but the label depended on that gate rather than on what was built. The flags
  are now assembled after `resolve-release-version.sh` has resolved the
  version, so every image is built with it explicitly.
- Operator pod termination skipped the drain window. The manager Deployment's
  `preStop` hook was `exec: /bin/sh -c "sleep 5"`, but the runtime image is
  `ubi9/ubi-micro`, which ships no shell and no `sleep`. The hook could not run,
  so SIGTERM reached the process immediately and a pod being removed from the
  Service could still receive scrapes or hold the leader lease during its final
  seconds. Both the manager Deployment (kustomize and CSV) and the console
  plugin container now use the kubelet's native `preStop.sleep` handler, which
  needs no binary in the image. Requires Kubernetes 1.29+; the CSV already
  declares `minKubeVersion: 1.35.0`.
- Console plugin image failed to build. The `COPY` that places
  `THIRD-PARTY-NOTICES.txt` in `/licenses/` named a path from the build stage
  without `--from=build`, so it resolved against the build context instead,
  where `dist/` is excluded by `.dockerignore`. The file the build stage
  generated was never reachable from the runtime stage.

### Security

- Results CSV export hardened against a formula sigil hidden behind a leading
  control character. `csvCell` dropped NULs and Unicode format characters, then
  checked the cell for a formula starter, so a cell such as `\u0001=cmd` kept a
  control prefix that a spreadsheet trims before deciding whether the cell is a
  formula. A tampered `ComplianceCheckResult` name, description first line, or
  `check-severity` label could therefore reach a downloaded export as an
  evaluated formula. Export rows now drop the controls a spreadsheet trims
  (tab, CR, and LF stay, since RFC 4180 quoting needs them). Cells that held a
  control character other than a delimiter lose it from the export.
- Console write controls were gated on `useAccessReview` through their
  `isDisabled` prop alone. A tab holding an open confirm modal, a stopped
  editor, or a pending form across a permission revocation would still send the
  patch the button had already admitted. Every mutation now re-checks the
  reviewed permission at the request boundary through one chokepoint
  (`console-plugin/src/permissions.ts`), and an unresolved review denies rather
  than defaulting to allow. Covered: rescan, profile toggle, schedule save,
  waiver add and remove, TailoredProfile create, update, and bind, tailored
  profile unbind, default baseline create, and every remediation path
  (per-row apply, unapply, auto-apply, batch apply).

### Changed

- Console plugin built a fresh `Intl.NumberFormat` / `Intl.DateTimeFormat` on
  every count, date label, and chart tick. A console session sets an explicit
  locale, and the engine only caches the runtime default, so each card, waiver
  row, remediation row, and axis label paid a formatter construction on the
  main thread. `formatCount`, `formatLocalDate`, and `formatChartDate` now hold
  one formatter per locale tag, matching how the display collator is already
  cached. Output is unchanged, including the fallback for an invalid tag.
- Console plugin re-canonicalized the console locale on every count, date label,
  collator comparison, and list join: `safeLocale` ran `Intl.getCanonicalLocales`
  per call, and `compareForDisplay` calls it once per comparison, so sorting the
  rule catalog canonicalized the tag thousands of times per sort. `safeLocale`
  now holds one entry per locale tag, like the formatters above it. Output is
  unchanged, including the invalid-tag fallback.
- Profiles typeahead re-folded the whole rule catalog, plus the query once per
  option, on every keystroke, so typing in the enable-rules picker redid a
  thousand NFD normalizations per character over unchanged names. The catalog
  is now folded when it changes and the query when it is typed, leaving a
  substring test per option. Matching is unchanged, including diacritic and
  Turkish dotted/dotless i handling.

### Added

- Observe dashboard gained a Reconcile loop row (reconcile errors against total
  reconciles, and p50/p99 reconcile duration). The operator's own failure rate
  and loop latency were visible only in pod logs, so a reconcile loop slowing
  toward its 5m bound had no metric to graph before it started failing.
- `docs/OBSERVABILITY.md` documents the log levels and the posture line, the
  dashboard rows and the order to read them, and why the operator ships no
  OpenTelemetry tracing.

- Console plugin image ships `/licenses/THIRD-PARTY-NOTICES.txt` (and the same
  file under `dist/`, so the console serves it). The bundle redistributes
  PatternFly, victory, React, and the rest of the installed closure, and the
  image previously carried only its own `LICENSE`, so a downstream consumer
  could not trace those grants back to their origin. The file is generated by
  `yarn licenses` from the installed tree, never hand-edited, and the build now
  fails when any installed package declares a missing, unrecognised, or
  copyleft license, or ships no license text to reproduce.

- Alert `ClusterBaselineNotAvailable`: fires when the ClusterBaseline has been
  `Available=False` for 1h without `Progressing`. Admin-owned steady states
  (Compliance Operator not installed under `installComplianceOperator=Manual`,
  compliance CRDs absent, console plugin image unset) never set `Degraded`, so
  the operator previously reported healthy while producing no compliance score
  and no alert fired.
- Operator memory grew with the cluster, not with what it reconciles. The
  manager's informer cache is cluster-wide per type, and the first typed read
  is what starts the informer, so the scan-storage PVC check cached every
  PersistentVolumeClaim in the cluster and the console-plugin check cached
  every Deployment, Service, and PodDisruptionBudget. The cache is now scoped
  to the namespaces those reads actually use (`openshift-compliance` for scan
  storage, `openshift-baseline-security` for the plugin). `ClusterBaseline` is
  cluster-scoped and stays cache-wide; foreign Compliance Operator objects are
  read as unstructured and already bypass the cache. The compliance CRs the
  event-driven watches follow (ComplianceSuite, ComplianceScan,
  ComplianceRemediation, ComplianceCheckResult) are cached as metadata only and
  cannot be named per type, so they took the cluster-wide default; the default
  is now scoped to the same two namespaces, which keeps foreign compliance
  objects out of the heap instead of only out of the reconcile queue.
- Operator and console plugin pods: neither declared a `preStop` hook, so a
  terminating pod kept its endpoint for the seconds between SIGTERM and
  endpoint removal, and a scrape or console request could still land on a
  draining pod. Both containers now sleep 5s in `preStop` before the process
  sees SIGTERM, inside the existing 30s grace period.
- Operator `/readyz` reported ready for the whole drain. A SIGTERM now flips
  the readiness check to failing, so the pod leaves the Service endpoints as
  soon as the process starts shutting down.
- Console plugin rule and profile typeahead: typing `securite` did not find
  `sécurité`, because the filter folded case with `toLowerCase()` and left
  diacritics in place. The filter now ignores case and diacritics and keeps the
  Turkish dotted and dotless I distinct, so a query matches in either case.
- Console plugin rule and profile pickers: the option lists were sorted by byte
  value, so an accented name sorted after every plain letter and embedded
  numbers ordered `rule_10` before `rule_2`. They now sort by the session
  locale's collation.
### Changed

- The operator seeded every gauge to 0 at startup, and
  `baseline_security_status_observed_timestamp_seconds` was seeded to 0 as a
  "never published" sentinel. Only `baseline_security_compliance_score` (the -1
  sentinel of ADR-018) and the `baseline_security_condition` children need that;
  every other gauge is written on each publish, and a published timestamp is a
  wall clock, never 0. The seeds are gone, so on a replica that has not
  reconciled yet `baseline_security_last_scan_timestamp_seconds`,
  `baseline_security_newly_failed`, `baseline_security_remediation_batch_active`,
  `baseline_security_remediation_batch_started_timestamp_seconds`, and
  `baseline_security_scan_interval_seconds` are absent rather than 0. A
  dashboard panel or recording rule that read one of them as 0 in that window
  now sees no series; add `or vector(0)` in PromQL where the zero is the answer
  you want. Alert firing is unchanged: `ComplianceStatusStale` catches the
  never-published case through its `absent()` disjunct, which the seed removal
  makes the primary path, and the HA newest-publisher selection every other
  alert uses simply matches nothing until the first publish.
- Console plugin built a fresh `Intl.NumberFormat` / `Intl.DateTimeFormat` on
  every count, date label, and chart tick. A console session sets an explicit
  locale, and the engine only caches the runtime default, so each card, waiver
  row, remediation row, and axis label paid a formatter construction on the
  main thread. `formatCount`, `formatLocalDate`, and `formatChartDate` now hold
  one formatter per locale tag, matching how the display collator is already
  cached. Output is unchanged, including the fallback for an invalid tag.
- Console plugin re-canonicalized the console locale on every count, date label,
  collator comparison, and list join: `safeLocale` ran `Intl.getCanonicalLocales`
  per call, and `compareForDisplay` calls it once per comparison, so sorting the
  rule catalog canonicalized the tag thousands of times per sort. `safeLocale`
  now holds one entry per locale tag, like the formatters above it. Output is
  unchanged, including the invalid-tag fallback.
- Profiles typeahead re-folded the whole rule catalog, plus the query once per
  option, on every keystroke, so typing in the enable-rules picker redid a
  thousand NFD normalizations per character over unchanged names. The catalog
  is now folded when it changes and the query when it is typed, leaving a
  substring test per option. Matching is unchanged, including diacritic and
  Turkish dotted/dotless i handling.
- The console plugin's nginx access log no longer uses the `combined` format.
  It logged the admin's client IP, the referring console URL, and the browser
  user agent, none of which triage a failed static-asset fetch, and the log
  line is copied verbatim into `hack/must-gather.sh` output and from there into
  support archives. The line now carries the method, the path without its query
  string, the protocol, the status, and the response size. Log volume, the
  non-2xx/3xx filter, and the destination are unchanged.

- The manager printed its usage text to stdout on a usage error, so a caller
  that captured stdout on the exit-2 path (an unknown flag, an unexpected
  positional argument) read the whole help text as command output while the
  error itself went to stderr. `--help` still writes to stdout, so
  `manager --help | less` keeps working; a bad invocation now writes the
  message and the usage text to stderr, and the message names the binary.
- Kubernetes objects the operator ships (manager Deployment, metrics Service,
  ServiceMonitor, PrometheusRule, and the plugin Service/Deployment/PDB) now
  carry the recommended `app.kubernetes.io/name`, `component`, `part-of`, and
  `managed-by` labels, and the CSV pod template carries
  `app.kubernetes.io/version`. Selectors still match on `app` alone, because
  the Deployment selector is immutable and a selector requiring a new label
  would stop matching pods created before it.
- Exported HTML report: the frame rule was a fixed brand red on every report,
  including a passing one. It now takes its color from the score band using the
  same success/warning/danger hexes as the Grafana dashboard thresholds, and an
  unscored report is framed in neutral grey rather than danger red. The palette
  and the type scale moved into one `REPORT_TOKENS` table so a report's colors
  are edited in one place, the score is a clear step above the page title rather
  than four pixels, section headings carry a rule so the three tables read as
  three groups, and the page is held to a 72rem measure so the six-column waiver
  table stops stretching across a wide monitor.

- Per-profile score sparkline on the Overview tab took the charting library's
  default blue, so a profile card showed a green score chip above a blue trend
  bar while the overall trend chart beside it was banded by score. It now uses
  the same `scoreColor` band as the chip, the donut center, the cluster Overview
  detail item, and the exported report, driven by the latest snapshot.

- Threat model: the manager pod was described as running under Restricted PSS.
  The pod spec satisfies Restricted, but no
  `pod-security.kubernetes.io/enforce` label exists anywhere in the repo, so
  nothing enforces it. The model now records it as defense in depth and lists
  the missing label as a named gap, alongside the unbounded
  `machineconfigpools` patch and full CRUD on `scansettingbindings` in the
  operator ClusterRole, the platform-Prometheus scrape as a boundary, and the
  remediation clipboard copy as an untrusted-output sink. Every file reference
  was re-read against 0.6.1.
- Docs: `docs/SPEC.md` tracked the 0.5.x line and the pre-0.6.0 toolchain pins
  (k8s.io v0.35.x, controller-runtime v0.23.3, webpack 5.107) while the released
  line is 0.6.1 built on k8s.io v0.36.4 / controller-runtime v0.24.1 / webpack
  5.110. The spec header and pin table now match `operator/go.mod` and
  `console-plugin/package.json`, the roadmap covers 0.5.5 through 0.6.1, and
  two shipped decisions that had no record are captured: ADR-030 (no OLM
  `replaces` graph, CSV `capabilities: Basic Install`) and ADR-031 (waiver
  names unique at admission).
- Operator, reconcile: several passes read the Compliance Operator objects they
  need one object at a time, so a full pass cost dozens of live apiserver round
  trips (one `Get` per selected ScanSettingBinding, on every pass, on every
  replica). They now take one paged `List` and derive the
  per-object decisions from it, and the cluster-wide CSV lookup (the fallback
  used when the Compliance Operator is installed outside
  `openshift-compliance`) is paged too, since a CSV carries its whole install
  spec and `alm-examples`. Reconcile latency and apiserver QPS drop on clusters
  with many profiles or check results; the objects written are unchanged, and
  every read is still a live unstructured read rather than a cached one.
- Console, Remediations: **Batch apply** and **Auto-apply** now need `patch`
  on `complianceremediations` in `openshift-compliance` as well as `patch` on
  the `ClusterBaseline`. Both controls write only the baseline (an annotation,
  or `spec.remediation.apply`), and the operator then patches the
  remediations, which rolls a node reboot, so a user granted the baseline patch
  alone could spend a write the per-row Apply action already refused.
  **Before:** a user with the baseline patch could batch apply and toggle
  auto-apply. **After:** the same user sees both controls disabled, with the
  reason on hover, and the toggle refuses rather than writing the baseline.
  Grant `patch complianceremediations.compliance.openshift.io` in
  `openshift-compliance` to restore the previous behavior. `cluster-admin` and
  the built-in `admin` ClusterRole in that namespace already hold it; a custom
  role that granted only the baseline patch does not.
- Console, Profiles: authoring a tailored profile now needs `create` on
  `tailoredprofiles` in `openshift-compliance`, and editing a profile bound to
  a baseline needs `update` on the same resource, each on top of the
  `ClusterBaseline` patch the controls already spent. **Before:** the baseline
  patch alone gated Author and Edit. **After:** a user without those verbs
  loses the Author button and the Edit control (the Unbind control beside Edit
  still patches only the baseline and is unchanged). This closes a path where
  a baseline-only patch created and rewrote objects the operator then consumed.
### Fixed

- A paged apiserver `List` that returned the continue token it had just been
  given replayed the same page until `reconcileTimeout`, so the reconcile failed
  with a deadline instead of the real cause. The three paged read paths
  (ComplianceCheckResult aggregation, remediation batch selection, scan-diff
  history) now stop when the token does not advance, write the partial rollup
  they have, and re-list from the start on the next reconcile.
- The console plugin's startup, readiness, and liveness probes were a bare TCP
  connect on 9443, so an nginx pod holding the listener open over an unreadable
  asset tree (a bad `fsGroup`, a truncated image) reported ready and drew console
  traffic while every asset 404ed. The probes now request `/healthz` over HTTPS
  on the serving path, which nginx answers from a constant return that touches no
  asset.
- The Remediations tab read `metadata.name`, `metadata.labels`, and
  `status.errorMessage` off a `ComplianceRemediation` without narrowing, so one
  object that arrived without `metadata` (hand-edited, or a partial list) threw
  during render and took the whole tab down, and a `status.errorMessage` that was
  not a string, or carried Unicode format characters, reached the table
  unfiltered. Name and labels are read through optional access, the error detail
  through the same `isString` and `stripFormatChars` guards the rest of the
  console applies to untrusted status.
- Console plugin image failed to build. The `COPY` that places
  `THIRD-PARTY-NOTICES.txt` in `/licenses/` named a path from the build stage
  without `--from=build`, so it resolved against the build context instead,
  where `dist/` is excluded by `.dockerignore`. The file the build stage
  generated was never reachable from the runtime stage.
- Release workflow: the `version` input of a manual `workflow_dispatch` run was
  never read, so the cut published whatever the dispatched ref resolved to and a
  mistyped version was accepted silently. The input is now passed to
  `resolve-release-version.sh`, which already prefers it over the ref name.
- Release workflow: the console plugin image was published from the tagged
  commit without running that commit's plugin tests (the operator half was
  gated). `yarn typecheck` and `yarn test` now run, on the Node version
  `.nvmrc` pins, before any image is pushed. Each image is also checked by
  `hack/verify-image-metadata.sh` between build and push, as CI already did:
  a published tag is immutable, so a root `USER` or a missing version label
  has to fail the release rather than reach a consumer.
- CI image smoke check: the per-matrix checks ran without `set -e`, so a failing
  check was masked by the next command in the same branch (a missing plugin
  manifest still passed when the license check after it succeeded). The step
  now fails on the first failure, and the matrix values come from the
  environment as the metadata step above already did, rather than being
  interpolated into the script body.
- Console plugin: a score-history time or waiver name containing the character
  the content key used as its separator made two different sets produce the same
  key. The score trend and the waiver expiry clock then skipped their recompute
  and kept painting the previous values after the CR had changed. The keys are
  now length-prefixed and type-tagged, so no value can forge another's.
- Console plugin, Overview trend chart: a `status.history` entry that was `null`
  rather than a snapshot threw while the history was read, blanking the page
  instead of drawing the ring. Such an entry is now dropped with the rest of the
  unparseable ones.
- Console plugin, waive form: a waiver reason, requestedBy, or approvedBy
  longer than 1024 / 253 characters was refused client-side with "fields are
  invalid or exceed length limits" whenever the text was mostly non-ASCII, and
  a waiver the apiserver would have admitted could not be saved. The bound was
  counted in UTF-16 code units while a CRD `MaxLength` counts Unicode code
  points, so an emoji (one code point, two code units) counted double. Both
  bounds are now counted in code points, the unit the apiserver applies.
- `console-plugin/.env` (live-console Playwright run): a key other than the four
  the runner reads, a duplicate key, a line that is not `KEY=value`, or an
  unterminated quote was dropped without a word, so a misspelled `CONSOLE_URL`
  surfaced much later as "CONSOLE_URL must be set" with nothing pointing at the
  file. The loader now names the file, the line, and the allowed keys, and
  reports every bad line at once. `.env.example` also no longer ships a
  placeholder `CONSOLE_URL` that would run the suite against a host that does
  not exist.
- Console plugin, non-English consoles: the Remediations table sorted
  remediation names by the browser's default collation rather than the console
  session's, so a German or Swedish console ordered that list differently from
  every other sorted list on the page. List separators for the blocked
  dependency summary, the expiring-waiver alert, and the newly-failing alert
  were a literal `", "`, which is wrong in every locale that has its own list
  punctuation (German "und", Arabic "، و") and leaves a stray comma in locales
  that have none (Japanese, Chinese). Both now follow the session locale.
- Console plugin, bidirectional text: the Overview alerts and the Remediations
  dependency and error details render untrusted Compliance Operator text
  (check titles, waiver names, `status.errorMessage`) without a text direction,
  so an RTL value reordered the punctuation and links around it.
- Console plugin, apply-remediation confirmation: the node-remediation warning
  told the admin to batch changes by pausing the target MachineConfigPool under
  Compute and resuming it afterwards, which is exactly what the Remediations tab
  does for them with the Batch apply button at the top of the same page. The
  copy now points at that button, so the manual detour through another page is
  no longer the only documented route.
- Console plugin, per-profile score cards: the compact score sparkline was
  drawn in the charting library's default blue, so the one chart that plots a
  profile's own score did not carry the score color band the badge beside it,
  the donut center, and the score trend card all use. It is now colored from
  its latest point, which is the profile's current score.
- Contributor setup: `make lint` reached for `shellcheck` and `uvx ruff`, and
  neither was in the prerequisites table, so the documented clean-clone command
  (`make test lint`, in both README and CONTRIBUTING) failed on a host without
  them. Both are now declared, and `make lint-python` names uv the way
  `make lint-shell` already named shellcheck instead of failing with a bare
  `uvx: command not found`.
- `make help` did not list the release-path targets a version bump needs
  (`make verify-versions`, `make catalog-prepare`), which are only mentioned in
  AGENTS.md and the CSV comments.
- `hack/resolve-release-version.sh` run outside GitHub Actions died on
  `GITHUB_REF_NAME: unbound variable` instead of reporting that no release
  version was found, because the tag ref was read without a default under
  `set -u`. The two scripts whose diagnostics were not prefixed with their own
  name (`resolve-release-version.sh`, `verify-image-metadata.sh`) now use the
  same `script: message` form as the rest of `hack/`, and
  `verify-image-metadata.sh` sends its per-label output to stderr, since it is
  a pass/fail gate whose exit code is the result.
- Console plugin: a `status.score` that was not a number, or was outside 0-100,
  rendered as an empty or out-of-range score. The console reads the value
  unverified from the CR, so a hand-edited or restored object could paint a
  green empty score in the cluster Overview detail item and the compliance
  score donut, and print `Score:  / 100` in the exported HTML report. Every
  score read now goes through one guard: a non-finite or non-numeric value
  renders as no score (`—`, "Not scanned", neutral donut), and an out-of-range
  one is clamped to 0-100, which is what the operator publishes for it. The
  report's per-profile counters got the same treatment, folding a non-finite
  or negative count to 0 as the operator does on write.
- Console plugin `Rescan now` did not always start a scan. The rescan
  annotation value came from a counter that restarted at 1 on every page load,
  so the first rescan after a reload or a tab switch back to the plugin wrote
  the value the apiserver already held. The Compliance Operator watches that
  annotation for a change, so it observed nothing, no scan started, and the UI
  still reported "Rescan started". The token now carries the wall clock, with
  the per-page counter kept only to separate two clicks in the same
  millisecond.
- `status.relatedObjects` listed the console plugin Deployment,
  PodDisruptionBudget, and ConsolePlugin even with
  `spec.console.managementState: Removed`, where the operator has deleted them
  and refuses to recreate them. The list declares what the baseline owns, so it
  now tracks the management state and must-gather stops chasing disowned
  objects.
- CI: the operator's helper scripts were unanalyzed. `make lint` now runs
  shellcheck over `operator/hack/*.sh` (the bundle, alert, and must-gather
  checks that ship with the operator) and ruff over `operator/hack/*.py`, so a
  quoting bug or an unhandled path in a script that gates a release fails the
  per-PR job instead of surfacing on a runner.
- Operator and console plugin pods: neither declared a `preStop` hook, so a
  terminating pod kept its endpoint for the seconds between SIGTERM and
  endpoint removal, and a scrape or console request could still land on a
  draining pod. Both containers now sleep 5s in `preStop` before the process
  sees SIGTERM, inside the existing 30s grace period.
- Operator `/readyz` reported ready for the whole drain. A SIGTERM now flips
  the readiness check to failing, so the pod leaves the Service endpoints as
  soon as the process starts shutting down.
- Operator images shipped `/usr/bin/manager` owned by the runtime UID (65532),
  so the process could rewrite the binary it executes wherever the root
  filesystem is not read-only. The binary is now root-owned, matching
  `/licenses` and the rest of the image. The console-plugin and catalog images
  pinned the uid but not the group, leaving the primary group to the base
  image's passwd entry; both now run as `1001:1001`.
- `operator/hack/must-gather.sh` appended to `related-objects.yaml` instead of
  rewriting it, and the output directory is never cleared. Collecting a second
  must-gather into the same directory duplicated every object document, and when
  the CR was gone the file kept the previous run's objects with nothing marking
  it stale. The file is now truncated at the start of collection, so a rerun
  converges on the current cluster state.
- Operator logs: a detail condition returning to True (Compliance Operator
  ready, scan configuration valid, scan storage ready, console plugin deployed)
  now logs at Info on the recovery. Only the transition into a failure was
  logged, so a cleared alert had no default-level breadcrumb to pair with the
  failure line.
- `operator/hack/must-gather.sh` dropped the `requestedBy` and `approvedBy`
  key lines but not the value text under them. The CRD caps those fields by
  length only, so a value containing a newline is dumped by kubectl as a
  literal or folded block and the name landed on the continuation lines, in the
  support archive, unredacted. A dropped attribution key now takes its
  block-scalar continuation with it; waiver name and reason are still kept.
- Console: the waiver "Requested by" and "Approved by" fields autofilled from
  the browser profile, so a personal name could be written into the
  cluster-scoped ClusterBaseline (and every report exported from it) without
  being typed. Autofill is off on both fields; the attribution is entered
  deliberately.
- Operator logs: a detail condition that changed `reason` while staying False
  (for example scan storage moving from `ScanStoragePending` to a different
  failure) was never logged. The transition guard compared against the
  condition entry that `SetStatusCondition` had already overwritten in place,
  so it always compared the new value with itself.
- Operator `/readyz` still reported ready for the whole drain on a replica that
  was not the leader. The runnable that flips readiness on SIGTERM did not
  declare itself non-leader-elected, so controller-runtime only started it after
  winning the lease; the shipped Deployment has 2 replicas, so a terminating
  standby kept reporting Ready and stayed in the Service endpoints.
- Operator `status.complianceOperatorVersion` kept the previously installed
  version when the Compliance Operator was uninstalled and the Subscription had
  to be re-created. The field is documented as empty while CO is not installed,
  and every other not-installed path clears it, so the console showed a version
  for an operator that was not there.
- Operator metrics logs: a pod started with an empty `--metrics-cert-dir`
  (self-signed metrics identity by design) logged `failed to parse metrics TLS
  cert/key` on the first scrape, because the parse path ran on a pair of
  zero-length buffers. The false error was indistinguishable from a corrupt
  projected Secret and consumed the once-per-episode log slot a real corruption
  needs.
- Operator batch: when the Compliance CRDs were uninstalled while a remediation
  batch was applying, the operator resumed the paused MachineConfigPools and
  finished the batch as `applied`, but logged each remediation as individually
  `notFound`. The real cause (the CRD disappeared mid-batch) left no trace.
- Console plugin: the cluster Overview "Compliance score" item stayed on the
  loading placeholder forever when the `ClusterBaseline` watch failed before its
  first successful list, instead of showing the distinct "Unavailable" state the
  rest of the page shows for the same failure.
- Console plugin: opening the check detail dialog on a list item that arrived
  without `metadata` threw during render and took down the whole Results tab,
  even though the row itself was written to survive such an item.
- Console plugin: a failed GET of the async Overview charts chunk logged an
  unhandled promise rejection in the browser console, because the eager
  `import()` promise carried no rejection handler while the chart gate that
  consumes it already caught and showed Retry. A stale cached console after a
  plugin upgrade is the common way to hit it, and the unhandled rejection
  obscured the Retry control for the same failure.
### Security

- Results CSV export hardened against a formula sigil hidden behind a leading
  control character. `csvCell` dropped NULs and Unicode format characters, then
  checked the cell for a formula starter, so a cell such as `\u0001=cmd` kept a
  control prefix that a spreadsheet trims before deciding whether the cell is a
  formula. A tampered `ComplianceCheckResult` name, description first line, or
  `check-severity` label could therefore reach a downloaded export as an
  evaluated formula. Export rows now drop the controls a spreadsheet trims
  (tab, CR, and LF stay, since RFC 4180 quoting needs them). Cells that held a
  control character other than a delimiter lose it from the export.
- Console write controls were gated on `useAccessReview` through their
  `isDisabled` prop alone. A tab holding an open confirm modal, a stopped
  editor, or a pending form across a permission revocation would still send the
  patch the button had already admitted. Every mutation now re-checks the
  reviewed permission at the request boundary through one chokepoint
  (`console-plugin/src/permissions.ts`), and an unresolved review denies rather
  than defaulting to allow. Covered: rescan, profile toggle, schedule save,
  waiver add and remove, TailoredProfile create, update, and bind, tailored
  profile unbind, default baseline create, and every remediation path
  (per-row apply, unapply, auto-apply, batch apply).

### Security

- Results CSV export hardened against a formula sigil hidden behind a leading
  control character. `csvCell` dropped NULs and Unicode format characters, then
  checked the cell for a formula starter, so a cell such as `\u0001=cmd` kept a
  control prefix that a spreadsheet trims before deciding whether the cell is a
  formula. A tampered `ComplianceCheckResult` name, description first line, or
  `check-severity` label could therefore reach a downloaded export as an
  evaluated formula. Export rows now drop the controls a spreadsheet trims
  (tab, CR, and LF stay, since RFC 4180 quoting needs them). Cells that held a
  control character other than a delimiter lose it from the export.
- Console write controls were gated on `useAccessReview` through their
  `isDisabled` prop alone. A tab holding an open confirm modal, a stopped
  editor, or a pending form across a permission revocation would still send the
  patch the button had already admitted. Every mutation now re-checks the
  reviewed permission at the request boundary through one chokepoint
  (`console-plugin/src/permissions.ts`), and an unresolved review denies rather
  than defaulting to allow. Covered: rescan, profile toggle, schedule save,
  waiver add and remove, TailoredProfile create, update, and bind, tailored
  profile unbind, default baseline create, and every remediation path
  (per-row apply, unapply, auto-apply, batch apply).

## [0.6.1] - 2026-09-02

### Fixed

- Operator, console plugin, bundle, and catalog images: `/licenses` was
  created with mode 0644 and so could not be traversed by the image's
  non-root user, making the shipped `/licenses/LICENSE` unreadable. A
  single `COPY --chmod=0644` also stamps the parent directory BuildKit
  creates; the directory is now copied at 0755 and the file at 0644.

### Security

- Console plugin build pulls browserslist 4.28.8, clearing two high-severity
  advisories (GHSA-c83g-rgw3-j3cx unbounded cache growth, GHSA-73wf-gq98-2v4g
  crash / prototype write via custom stats) that reached the tree through
  webpack. Build-time only; nothing shipped in the plugin bundle changes.

## [0.6.0] - 2026-09-02

### Changed

- Operator builds against controller-runtime 0.24.1 and the Kubernetes 0.36.4
  client libraries (was 0.23.3 / 0.35.1). `PodSpec.workloadRef` is tombstoned
  upstream in 1.36, so the console plugin pod no longer clears that field, and
  the API group registers through apimachinery's SchemeBuilder because
  controller-runtime deprecated its own. No CRD schema change.
- Console plugin builds with eslint 10.9.1, webpack 5.110.1, and
  `@patternfly/react-icons` 6.6.1. i18next and react-i18next stay on 25.x and
  16.x: react-i18next is a no-fallback singleton shared with the console, and
  OpenShift 4.22 provides 16.5.x.
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

- Console: `yarn install` no longer runs package lifecycle scripts
  (`enableScripts: false`, image `YARN_ENABLE_SCRIPTS=false`). A compromised
  transitive with a `postinstall` cannot execute during local/CI install or
  the plugin image build.
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

- A `ClusterBaseline/cluster` that already holds two `spec.waivers` entries
  with the same `name` (both were admitted before this release) is rejected
  by the apiserver on the next write, so patching it, re-applying it, or
  waiving another check from the console fails until the duplicate entry is
  removed. Check with
  `oc get clusterbaseline cluster -o jsonpath='{.spec.waivers[*].name}'` and
  drop the repeated name before or right after the upgrade; nothing else
  about the stored object changes.
- `baseline-security-admin` is no longer aggregated onto the built-in `admin`
  ClusterRole (see **Security** above). An install that relied on a
  RoleBinding to `admin` in `openshift-compliance` must bind
  `baseline-security-admin` explicitly before remediations can be applied.
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

- `make verify-csv-deploy` (run in CI and `make bundle`): fails if the CSV
  `install.spec.deployments[].spec` drifts from the Deployment in
  `config/manager/manager.yaml`. The CSV is hand-maintained, so its pod spec was
  a second, unchecked copy of the kustomize base: a probe, resource limit, or
  volume added to the base reached `make deploy` and not an OLM install. The
  image tag, `imagePullPolicy`, and the `app.kubernetes.io/version` pod label
  are the only allowed divergences.

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

[Unreleased]: https://github.com/maci0/openshift-baseline-security/compare/v0.6.1...HEAD
[0.6.1]: https://github.com/maci0/openshift-baseline-security/compare/v0.6.0...v0.6.1
[0.6.0]: https://github.com/maci0/openshift-baseline-security/compare/v0.5.15...v0.6.0
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
