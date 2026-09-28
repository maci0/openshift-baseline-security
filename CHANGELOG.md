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

## [0.7.0] - 2026-09-28

### Added

- State chips on the Remediations tab, alongside the name filter. A blocked or
  failing remediation could otherwise only be found by knowing its name; the
  chips narrow the list to the states an admin acts on and each carries its own
  count. Selecting several unions them, and the list reports how many of the
  total are shown. Batch apply is unaffected: it still acts on every batchable
  remediation, not on the filtered view.

- A confirmation after the create-baseline button succeeds, so the click is
  never a silent no-op while the watch catches up.

- A name filter on the Remediations tab. A full benchmark run lists thousands
  of remediations and the tab had no way to narrow them, so finding one rule
  meant scrolling the whole list; Results has had chips for the same reason.
  The search matches on the remediation name, ignoring case and diacritics,
  and reports how many of the total are shown. Batch apply is unaffected: it
  still acts on every batchable remediation, not on the filtered view.

- `hack/verify-backup.sh`, a cluster-free check that a backup directory is
  still restorable: the artifact is present, non-empty, the right kind, matches
  the sha256 in its MANIFEST, and is within an age limit. A scheduled backup
  that stops running, whose off-cluster copy never landed, or that was
  truncated in transit fails silently until an incident; this gives that
  schedule something to alert on, and it runs against a copy pulled back from
  remote storage. `--max-age-days` sets the limit.

- A cluster timestamp naming a day that does not exist (`2026-02-31T00:00:00Z`)
  rendered as a real instant several days later, because `new Date` rolls an
  impossible calendar day forward instead of rejecting it. A corrupt
  `status.lastScanTime` or scan end timestamp therefore read as a scan that had
  already run, and the row moved on every mount. Such a value now resolves to
  no date, the same as any other unparseable timestamp.

- `yarn typecheck` and `yarn build` failed in the console plugin: the cluster
  Overview score item closed a raw `<a>` with `</ConsoleLink>`, and the
  Remediations tab imported `BaselineUnavailable` by name from a module that
  only has a default export. The score item's link is now the same
  SPA-navigating `ConsoleLink` the rest of the component uses, so a plain click
  on it no longer leaves the console and reloads the shell.

- `hack/normalize-mtimes.sh` treated any argument as a path, so a mistyped
  option came back as `no such path: --flag` with exit 1, reading as a missing
  tree rather than the typo it was, and a usage error did not print the usage
  text. It now rejects option-shaped arguments and prints the usage, matching
  the contract every other `hack/` script already follows.

- A `MachineConfigPool` whose `spec.paused` is not a bool was read as "not
  paused" instead of as a failed read. A pool named in the
  `batch-pools` annotation is re-admitted to a re-opened batch only when it
  carries that batch's own pause marker, and a malformed field answered that
  question wrongly, so the pool dropped out of the pause set, the batch status,
  and every resume path, and was left paused with no path back. The batch start
  now fails and Degrades instead, which retries.

- `hack/backup.sh` and `hack/restore.sh`, a backup and restore path for
  `ClusterBaseline/cluster`, the only durable state this operator owns. Before
  them, recovering a lost or corrupted CR meant an out-of-band etcd restore,
  and the DR plan in `docs/TEST-PLAN.md` AQ had never been executed. The
  backup captures the object with its spec, status, and batch annotations plus
  a sha256 MANIFEST, and refuses to write one for an empty or wrong-kind
  capture. The restore validates that MANIFEST before it writes anything, and
  replaces the status subresource rather than only applying, so the score,
  conditions, score history, and an in-flight remediation batch come back
  instead of being silently dropped. RPO and RTO are now stated in
  `docs/RESTORE.md`. Both scripts are driven end to end by
  `hack/backup_restore_test.go` on every `make test`.

- `baseline_security_remediation_batches_total`, a counter of finished
  remediation batches by outcome (`applied`, `cancelled`, `grace`, `orphaned`),
  and a `RemediationBatchGraceResume` alert on it. A batch that ends on the
  resume grace window or through crash/cancel recovery unpauses the
  MachineConfigPools before every remediation has reported Applied, and then
  clears `status.remediationBatch`, so nothing left in the cluster recorded
  that the fixes may never have landed: the only trace was a log line at the
  moment it happened. The new Observe panel counts the same event over 24h.

- `make setup` at the repo root, documented in `README.md` and
  `CONTRIBUTING.md`, runs `yarn install` for the console plugin and then the
  `check` preflight. Before it, a clone with no dependencies surfaced
  `jest: command not found` or a mid-run toolchain download as the first
  failure. The preflight now names every missing tool before any build starts.
  `make help` lists the delegating targets.

- `make verify-reproducible` (in `make ci` and the GitHub Actions `operator`
  job) builds the manager twice, from two different absolute paths and under a
  different timezone, locale, and umask, and fails unless both binaries hash
  identically. The `-trimpath`, `-buildvcs=false`, and `-buildid=` flags were
  already set in the Makefile and both Dockerfiles, and the release claims the
  local and in-image binaries match, but nothing checked it: a dropped or
  misspelled flag produced a different binary and every image and test run
  still passed.

- The operator binary takes `--version` and prints the release version to
  stdout, exiting 0 before any cluster or port work. The value is stamped by
  the linker from the same `VERSION` the image label and the CSV carry, so
  `manager --version` inside a running pod reports the build it came from
  instead of a bare digest. A binary built without the stamp (plain `go
  build`) reports `dev`.

- `yarn size` (in `yarn build` and `yarn ci`) reports the transferred size of
  the built console plugin and fails over the ceilings in
  `console-plugin/tools/size/budget.ts`: the initial JS the browser must
  download before CompliancePage can paint, the largest async chunk, and the
  whole `dist/` tree, all gzipped. Nothing measured page weight before, so a
  library pulled back into the entry bundle accreted silently. The numbers
  print into the CI log beside the commit that produced them.

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

- Docs: `docs/DESIGN-DECISIONS.md` had no index for its 32 records and the
  operator-namespace `NetworkPolicy` shipped in **[Unreleased]** without a
  decision record, although its shape (ingress-only, egress deliberately
  undeclared) is a choice with a stated rationale. The file now carries a
  table of contents, ADR-033 records that decision, and `docs/SPEC.md`
  cross-references it where the policy is specified.

### Changed

- The Remediations tab no longer shows a full-page spinner while its watch
  loads. It now paints skeleton rows, the shape the tab resolves into, the way
  Overview and Profiles already show the cards they are about to fill. Each tab
  had picked its own loading treatment, and a spinner alone in the middle of an
  otherwise empty page was the one that told an admin nothing about what was
  arriving.

- Every `hack/` script now reports a bad invocation the same way: the
  diagnostic first, then the usage, both on stderr, exit 2, with stdout left
  empty. `backup.sh`, `restore.sh`, and `verify-backup.sh` accepted a stray
  argument after `--help` and printed the help anyway, exiting 0, so a caller
  that passed a path it meant to use saw success and no work done. Every
  `Usage:` line now names the script by basename rather than by a `hack/`
  path, and the operator's `--help` prints flags as `--long` to match the
  README, the CSV args, and the usage errors, which already spell them so.

- The cluster-wide ClusterServiceVersion fallback that finds an externally
  installed Compliance Operator no longer deep-copies every candidate it beats.
  A CSV carries the whole install spec plus the `alm-examples` annotation, and
  the apiserver returns a name-sorted page, which is ascending by version, so
  the old fold copied every CSV on the page instead of the one it kept. The
  page is now scanned for each tier's winner and copied once; a 200-CSV page
  drops from 366 KB and 2992 allocations to 17.8 KB and 422 (9x faster,
  measured). Each candidate's version is also parsed once instead of both
  sides being re-parsed on every comparison. The CSV selected is unchanged:
  the winner still does not depend on how the apiserver split the walk.

- The console plugin's Overview no longer re-derives the expiring-soon waiver
  list on every render. `expiringWaivers` parses an `expiresAt` per waiver (up
  to 256), and the CCR list watch re-renders Overview on every check-result
  event without touching the waiver list. The derivation is now memoized on
  the same waiver content key and expiry clock the page already computes, so a
  waiver entering or leaving the two-week window still updates the alert.

- The console plugin now gzips its assets at level 9 instead of level 5. Every
  file it serves is content-hashed and marked immutable for a year, so the
  bytes are compressed once at image build and each browser pays the cost at
  most once per plugin version; the extra effort is spent on a cache miss and
  the bytes it saves are spent on every cold fill. The build-time size report
  and the CI size step already measured at level 9, so the number in the run
  log is now the number on the wire. Precompressed files plus `gzip_static`
  are not available here (the UBI module set has no `gzip_static`), and
  brotli and zstd are extra module builds, so gzip stays the served encoding
  and the level is the lever.

- The two OpenTelemetry OTLP trace exporter modules move from v1.40.0 to
  v1.44.0, onto the same version as the `go.opentelemetry.io/otel` core
  modules. The exporter builds on the core trace SDK and the two are released
  together, so leaving the exporters two minors behind is skew the graph only
  tolerated. It brings `go.opentelemetry.io/proto/otlp` to v1.10.0 and
  `grpc-ecosystem/grpc-gateway/v2` to v2.29.0.

- The operator builds against controller-runtime v0.25.1 and Kubernetes 0.37.0
  (`k8s.io/api`, `apimachinery`, `client-go`, `streaming`, and the apiserver
  staging modules). v0.24.1 could not take the 0.37 client libraries: their
  client-go adds `HasSyncedChecker` to an interface that release does not
  implement, and no controller-runtime before v0.25 does. No observable
  behavior change.

- The CRD and manager ClusterRole are generated with controller-gen v0.22.0
  instead of v0.21.0. The Makefile asks for the controller-tools release whose
  `k8s.io/*` matches the operator's, and the move to Kubernetes 0.37 left
  v0.21.0 a minor behind (it builds against k8s v0.36). The generated schema is
  byte-identical apart from the `controller-gen.kubebuilder.io/version`
  annotation, so no field changes.

- `yarn size` reports the first-paint download (entry bundles plus the manifest
  and locale the console fetches ahead of them) and no longer counts
  `THIRD-PARTY-NOTICES.txt` in the dist total. No page links that file, so it
  was inflating the ceiling that stands for what a browser actually
  downloads, and the locale bundle the first paint waits on was invisible to
  the report. No shipped bytes changed.

- The Observe → Dashboards view and the exported HTML report now paint statuses
  in the same colors the console paints them in. Every dashboard graph panel
  took Grafana's default categorical palette, where a failing-check series can
  render green and a passing one blue, and the report and dashboard carried
  PatternFly 4-era hexes while the console plugin reads PatternFly 6 status
  tokens. All three surfaces share one palette now (success `#3d7317`, danger
  `#b1380b`, warning `#dca614`, info `#5e40be`, custom `#147878`, orangered
  `#fbbea8` for Error, neutral `#a3a3a3`), and the 30-day score trend follows
  the same 60/90 bands as the score beside it.

- `docs/THREAT_MODEL.md` brought back in line with the code. The commit stamp
  and eleven line citations across `role.yaml`, `cmd/main.go`, `plugin.go`,
  `plugin_pod.go`, `nginx.conf`, both Dockerfiles, `CompliancePage.tsx`,
  `RemediationsTab.tsx`, and the e2e dotenv loader were stale, and two surfaces
  the model never named are now covered: the leader-election Lease and its
  separate Role, and the operator's cluster-wide RBAC grants. No shipped
  behavior changed.

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
  (k8s.io v0.35.x, controller-runtime v0.23.3, webpack 5.107) while the line
  cutting in this release is built on k8s.io v0.37.0 / controller-runtime
  v0.25.1 / webpack 5.110. The spec pin table now matches `operator/go.mod`
  and `console-plugin/package.json`, the roadmap covers 0.5.5 through this
  release, and two shipped decisions that had no record are captured: ADR-030
  (no OLM `replaces` graph, CSV `capabilities: Basic Install`) and ADR-031
  (waiver names unique at admission).

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

- `hack/backup.sh` and `hack/must-gather.sh` failed on a host whose `chmod` is
  BSD's (macOS, where the `portability` job runs), because they passed `--`
  before the path. BSD `chmod` has no end-of-options marker and read `--` as a
  filename, so the backup exited 1 with `chmod: --: No such file or directory`
  before it wrote anything, and every test that drives either script through a
  fake `oc` failed with it. Both scripts already refuse a flag-shaped output
  directory, so the marker only ever did something on the hosts that accept it.

- `yarn typecheck` failed in the console plugin, for the reasons the audit step
  had been hiding. The Overview's `ClusterTimestamp` imported the console SDK's
  `Timestamp`, which takes `timestamp` and renders relative time, then passed it
  `date` and `locale`: the scan rows were mistyped and wrong at runtime, and
  `Timestamp` now comes from PatternFly, which is the component that takes
  `date`. The Playwright `use` block carried `animations` and `caret`, which are
  `page.screenshot` options and never `use` options, and `reducedMotion`, which
  moved under `contextOptions` in Playwright 1.62, so the settings that exist to
  make a committed screenshot reproducible were rejected or ignored; `shot()`
  now passes `animations` and `caret` at the capture site. `e2e/helpers.ts`
  passed the masthead selectors to `page.evaluate` as an array, which matches no
  overload of it.

- The console plugin's test suite failed in five places, all of them stale
  fixtures rather than broken behavior. The locale ordering case sorted
  `string[]` with a `ComplianceRemediation` comparator. `profileScore`'s history
  case passed points with no `time`, which `latestSnapshotScore` skips as
  unparseable, so it never reached the branch it named. Two `toThrow` patterns
  and the fuzz message check spelled out the pre-i18n message text instead of
  the exported constants `ProfilesTab` renders. The attribution fixture's hooks
  were scoped to one `describe`, so the three suites after it ran against a
  directory the `afterAll` had already removed. The clock seam case asserted
  that two consecutive reads landed in the same millisecond, which is a race
  rather than evidence that the real clock is in use.

- The backup age limit accepted a full extra day. `verify-backup.sh` and
  `restore.sh` truncated the age to whole days before comparing it to the
  limit, so a backup taken 7 days 23 hours ago passed a 7-day limit and
  `restore.sh` said nothing about it, while the RPO that limit exists to
  bound is the whole time since the capture, not the number of midnights it
  spans. Both now compare the age in seconds, and report it to the hour.

- `baseline_security_status_observed_timestamp_seconds` was computed from
  `UnixNano()`, whose int64 nanosecond count overflows in 2262. Past that
  instant the gauge reads negative, and ComplianceStatusStale reads it as a
  replica that stopped publishing and pages forever. The gauge now takes the
  epoch second plus the nanosecond remainder, which is exact at any instant.

- Moving from an installed 0.6.x CSV is not an OLM auto-upgrade (no `replaces`
  graph since 0.5.5). Point the CatalogSource at the new catalog tag and install
  that head; delete a leftover Subscription/CSV. ClusterBaseline CRs stay, and
  `hack/backup.sh` / `hack/restore.sh` capture and recover the object if the
  reinstall goes wrong.

- `hack/restore.sh --force` failed on a host whose `mktemp` is BSD's (macOS,
  where the `portability` job runs), because it passed `--` before the template.
  BSD `mktemp` has no end-of-options marker and treated `--` as the template, so
  the temporary artifact landed in the caller's working directory under that
  name instead of beside the backup, the `oc apply` that followed read the wrong
  file, and the restore stopped with nothing changed. The `--` is gone; the
  other `hack/` scripts already use the portable form.

- An e2e test that could not restore the shared `ClusterBaseline` at the end of
  its run reported nothing. The cleanup read the CR without checking the read,
  and `getBaseline` returns an empty object alongside its error, so a failed
  read turned into a write of a spec carrying only the one field the test had
  changed: the operator dropped the selected profiles, tailored profiles, and
  waivers for every later test in the run, with no test reporting why. The
  write error was discarded too. Both now go through one `restoreSpec` helper
  that restores the whole spec the test started from and reports either
  failure.

- The console plugin's readiness probe asked a question nginx answered
  unconditionally. All three probes targeted the constant-return `/healthz`
  location, so an nginx pod holding the 9443 listener over an unreadable asset
  root (a bad `fsGroup`, a truncated image) still reported ready and stayed in
  the Service, 404ing every asset the console fetched. Readiness now targets
  `/readyz`, which returns 503 unless the worker can read the asset root and so
  pulls that pod out of the endpoints. Startup and liveness keep `/healthz`: the
  tree is baked into the image under a read-only rootfs, so a restart cannot
  make it readable and failing liveness would only CrashLoop a broken image.

- `hack/restore.sh` could not tell a live `ClusterBaseline` from the one its
  backup was taken from. Its only guard was a `resourceVersion` comparison,
  and a `resourceVersion` counts writes within one object's lifetime, so a CR
  deleted and recreated under the same name, a backup carried over from
  another cluster, or an etcd snapshot predating the object all read as "merely
  older". Where the versions happened to match, the restore went through and
  overwrote an unrelated object's waivers, which nothing else records. The
  `uid` `hack/backup.sh` has always recorded is now compared, and a difference
  is refused before any write, naming both uids and the three causes; a `uid`
  that cannot be read stops the restore rather than counting as absent, and
  `--force` does not cover it.

- `hack/backup.sh` recorded a `resourceVersion` and `uid` of `unknown` when its
  capture did not match the shape it reads them from, and
  `hack/verify-backup.sh` passed such a MANIFEST. Both guards above are keyed
  on those two fields, so a MANIFEST without them restores with the guards
  silently off. `backup.sh` now refuses to write one, and `verify-backup.sh`
  fails a directory whose MANIFEST is missing either.

- A Compliance Operator CSV name whose prerelease segment was a run of digits
  wider than int64 was compared as a string instead of as a number, so
  `compliance-operator.v1.2.3-rc.18446744073709551616` sorted below
  `compliance-operator.v1.2.3-rc.9223372036854775808` and the older CSV could
  win the newest-version selection. Such a segment is now ordered by value, by
  significant-digit count and then lexically, with no machine int in the path.

- Unbinding a tailored profile while its Edit form was still loading left the
  modal open on a profile no scan includes any more, and saving it reported
  `Tailored profile updated.` for a TailoredProfile nothing bound. Edit and
  Unbind are both enabled on the same card, and the Unbind write now supersedes
  the in-flight load, as creating one already did.

- Focus returned to a modal's trigger could land a frame late, after the admin
  had opened the next dialog, and pull focus out of it behind its backdrop.
  The deferred restore is now cancelled when its effect re-runs or the view
  unmounts, and yields when focus is already inside a dialog.

- Two waiver changes started in the same frame: the second click reached the
  patch while the first was in flight and did nothing, with no message. It now
  reports that a waiver change is already in progress.

- The console and the operator could read a different status from the same
  Compliance Operator annotation. Both trim the per-node tokens, but JavaScript
  `String#trim` and Go `strings.TrimSpace` disagree on exactly two characters:
  the console trims U+FEFF, which Go does not, and Go trims U+0085, which
  JavaScript does not. An `inconsistent-source` or `most-common-status` value
  carrying either, from text pasted out of a web page or a word processor, was
  therefore a status the console displayed and the operator did not count. The
  console now trims the set Go trims, so an INCONSISTENT check, a batch-apply
  request, and a remediation dependency list all read the same on both sides.

- The e2e `.env` loader rejected a whole file that began with a UTF-8 BOM. The
  BOM is decoded to U+FEFF, is not whitespace to `trim()`, and so became part of
  the first key, which was then reported as an unknown key. Any `.env` written
  or re-saved by a Windows editor carries one.

- A mis-set boolean environment variable whose value was mostly non-ASCII was
  truncated mid-character in the resulting startup error, so the operator logged
  a mojibake fragment of a value the admin had set in full. The value is now cut
  on a rune boundary.

- `hack/restore.sh` refused a second run against an object the first run had
  just restored. Both writes bump the live `resourceVersion`, so the staleness
  guard fired on the restore's own write, printed a warning claiming waiver
  edits made since the backup would be discarded (the ones that run had just
  put there), and pointed at `--force`, whose status replace is an
  unconditional overwrite of state an operator reads as current. The script now
  also compares the live object's `spec` with the artifact's. The operator
  never writes `spec`, so a match means the object is already at this backup
  and the run is a re-run: it is announced and allowed, sending the status
  without the captured `resourceVersion` that its own previous write staled. A
  spec that differs in any way is an admin's edit and the guard is unchanged.

- A cluster on which no compliance scan had ever completed produced no alert
  and no dashboard data while every other signal read healthy. The scan
  staleness rule needs a non-zero last-scan timestamp, so the "never scanned
  once" case fell through it; the score stayed at the `-1` no-score sentinel,
  which the low-score rule excludes, and `Available` was True because scanning
  was configured. The new `ComplianceNeverScanned` alert fires after 6h on a
  cluster whose schedule parses and whose profiles are selected but whose
  `status.lastScanTime` has never been set. Scanning deliberately turned off
  is not paged: it publishes a zero scan interval.

- Removing the console plugin went unalerted. `Available` ignores the plugin,
  and `Degraded` only fires on the `Unavailable` reason, so `ImageMissing`,
  `ImageInvalid` and `ConsoleMissing` set the detail condition alone. A
  mis-set `RELATED_IMAGE_CONSOLE_PLUGIN` therefore removed all of
  Administration → Compliance with nothing paging. The new
  `ConsolePluginNotReady` alert covers those reasons while
  `spec.console.managementState` is `Managed`; a plugin an admin deliberately
  removed is not paged, and requiring `Available=True` keeps it from
  double-reporting `ClusterBaselineNotAvailable`. It reads the new
  `baseline_security_console_plugin_managed` gauge, which separates "the
  plugin is missing" from "the plugin was removed on purpose".

- A `ClusterServiceVersion` whose `status.phase` had the wrong type was read
  as not `Succeeded` with no log, so a healthy Compliance Operator was
  reported as `ComplianceOperatorReady=False/CSVFailed` and rolled up to
  `Degraded`, paging with nothing to attribute it to a type mismatch on one
  CSV. The read is now logged, naming the CSV and the error.

- The operator emitted nothing on shutdown, so its last log line was always
  "starting manager" and a clean drain looked identical to a pod that was
  killed. It now logs when the drain begins and when the manager has stopped.

- A hand-edited `baselinesecurity.openshift.io/batch-pools` annotation, or a
  corrupt `batch-started-at` value, was logged in full and again on every
  reconcile while the failure persisted, so one corrupt annotation could emit
  a multi-kilobyte line once a minute. Those fields, and the Pending-PVC list
  in the scan-storage log, are now bounded for logging. The full lists are
  still acted on.

- A failed `CatalogSource` read while auto-detecting the Compliance Operator
  catalog was discarded with no log. Detection then fails safe to "assume the
  catalog is present", so a persistent RBAC denial or apiserver error left the
  `compliance-operator` Subscription pinned to a source that was never
  verified, and the Subscription sync path declined to correct it for the same
  reason. The only symptom was a `ScanConfigured` / `ComplianceOperatorReady`
  stuck on `Installing` with nothing in the operator logs. The read failure is
  now logged at Error (rate-limited to one line per 30m, V(1) in between) with
  the CatalogSource name and the underlying cause, and the Subscription create
  records when the source it wrote came from an unverified guess.

- Typing one more character than the last match in the Remediations search
  unmounted the search box itself, leaving no way to back off by a character
  short of clearing the query and retyping it. The search box and the
  "Showing X of Y" count now stay on screen in the no-match state.

- Every link between the Compliance tabs ("Go to Profiles", "Review check
  results", "Clear filters", the Overview drill-downs) was a bare anchor to a
  console route, so each one left the single-page app and reloaded the whole
  console shell, dropping the plugin's open watches. They now navigate inside
  the console; a modified click still opens the target in a new tab.

- A remediation whose apply the operator had accepted but not yet written to
  `status.applicationState` read "Not applied" next to a clickable "Unapply".
  It now reads "Applying…" until the operator confirms the state.

- The Results table sorted Status and Severity alphabetically, so an ascending
  sort read Error, Fail, ..., Pass and High, Info, Low, Medium. Both now sort
  by their facet order, matching the order of the filter chips above them.

- Exporting a CSV while a filter was active wrote the filtered rows but
  confirmed only "Results downloaded as compliance-results.csv.", so a subset
  was indistinguishable from a full export. The confirmation now names the row
  count written.

- The "Showing X of Y checks" and "Showing X of Y remediations" search counts
  carried no plural form, so a set of one read "Showing 1 of 1 remediations" in
  English and gave a translator no form to pick for languages that inflect the
  noun by count. Both are plural keys now, selected by the total.

- The singular form of the filtered-export confirmation read "Exported 1 of N
  filtered checks" with a hardcoded numeral. French counts zero in its
  singular form, so exporting an empty filtered set told a French session it
  had exported one row. The form interpolates the locale-formatted count.

- Removing an orphaned waiver reported "The check counts toward the score
  again", which is false for a waiver that matches no result. The button in the
  orphan list now shows a progress spinner on the click that is in flight and
  carries the check name as its tooltip.

- The Results table gave no count, so a filter that dropped most of the set
  looked like the whole set. It now shows the filtered count under the filter
  chips, using the same wording the Remediations search already used.

- On a single-node cluster the console plugin rolled out with
  `maxUnavailable: 1` against a one-replica Deployment, so a plugin upgrade
  could take the only pod down and blank Administration → Compliance until its
  replacement was ready. The plugin Deployment now pins `maxUnavailable: 0`
  whenever it runs a single replica; two-replica clusters keep `1`, which is
  what keeps the Deployment Available while a node is drained.

- The Overview tab rendered one link per newly failing and per fixed check on
  first paint, and `status.newlyFailed` / `status.fixed` hold up to 4096 names
  each, so a large scan delta put thousands of elements into the DOM before the
  rest of the page painted. Both surfaces now render the first 25 of each group
  and the Recent changes card shows the remainder on request; the counts in the
  alert and the group headings still report the full totals. The compliance
  score card on the cluster Overview also passed a fresh watch options object
  on every render, re-subscribing to the `ClusterBaseline` list each time.

- A render failure in Administration → Compliance left a blank page. The
  console mounts an extension page with no error boundary, so a throw while
  rendering a tab or the page shell unmounted the whole route, and the browser
  console carried no record of what threw. Every tab route and the page shell
  now sit behind an error boundary that names the view, reports the reason and
  the error object to the browser console, and offers Retry, so a bad
  `ClusterBaseline` or Compliance Operator object an admin fixes no longer
  needs a full page reload to recover from.

- The same check status was drawn in different colors depending on which view
  read it. `MANUAL` was the icon-token amber on the console composition donut
  and a brighter yellow in the Observe dashboard; `WAIVED` was teal on the
  console (donut wedge and Results status chip) and the same grey as
  not-applicable in the dashboard, which stacks the two adjacent. The dashboard
  now paints both from the same PatternFly 6 tokens the console reads, and the
  exported HTML report's score and severity type now uses the text status
  tokens it claimed to use (the warning amber and the success green were
  hand-picked values that matched no token), so a status is one color across
  the console, the report, and the dashboard. `TestDashboardUsesStatusPalette`
  pins the widened set.

- The Results tab reported "Baseline not configured" with a Create button when
  the `ClusterBaseline` watch failed, for example on a 403 or a missing CRD.
  The page forces its loaded flag true on a watch error so the tabs stop
  skeletonning, and Results was the one tab that did not read
  `baselineError` off the shared context, so it could not tell a failed read
  from an absent CR. It now renders the same danger state, naming the reason,
  that Overview, Remediations, and Profiles already render.

- The Overview "newly failing" banner and the Recent changes card disagreed
  with each other. The banner counted only the regressions whose check result
  was still present, so a scan whose failing rules had all been removed or
  unbound read "0 checks newly failing" while the status behind it listed
  them, and Recent changes then claimed there were no changes at all. Both now
  fall back to the operator's own count and say how many of those have no
  result left to open.

- The Degraded and Progressing banners on Overview printed the condition
  message with no `dir="auto"`, so a right-to-left message reordered the
  punctuation around it, and a Degraded condition carrying no message rendered
  a title with no explanation. Both render the message as its own element now,
  with fallback text when the operator set none.

- Apiserver and cluster-object text shown in error banners (Remediations
  apply, unapply, batch, auto-apply and clipboard failures; the schedule
  editor; baseline create) rendered without `dir="auto"`, unlike every other
  banner in the plugin, so an RTL resource name inside the message could
  reorder the text around it.

- `Export HTML report` disappeared from the page header whenever no
  `ClusterBaseline` existed, while `Rescan now` stayed visible, disabled, and
  carrying its reason. The two header controls now behave the same way.

- The Profiles tab hid `New tailored profile` from anyone without the create
  verb, leaving no reason and no hint that tailored profiles exist. It renders
  disabled with the permission reason now, like every other write control in
  the plugin.

- `hack/verify-backup.sh` computed the backup age with `date -u -d`, which is
  GNU coreutils only. On a host with BSD `date` (macOS, which `hack/backup.sh`
  and `hack/restore.sh` already support for the digest) the conversion failed,
  the age check was skipped with a note on stderr, and the script exited 0: a
  backup that had not been refreshed in a year verified as restorable, which
  is the one failure it exists to catch. The age is now read off the stamp
  itself (`hack/lib-timestamp.sh`, no external `date` call), and a MANIFEST
  whose `takenAt` is missing or unparseable fails the check instead of
  passing it, so an unmeasurable age can no longer be alerted on as a healthy
  one. `hack/restore.sh` reports the same case as an unknown RPO rather than
  printing no age at all.

- `hack/verify-backup.sh` digested the artifact with `sha256sum` directly,
  where the other two scripts go through `hack/lib-sha256.sh`, so the check
  could not run at all on a host without GNU coreutils, which is the host the
  doc tells an admin to pull the off-cluster copy back onto.

- `hack/restore.sh` now refuses, before any write, an artifact taken at an
  `apiVersion` the cluster's CRD does not serve. `oc apply` reports that as
  `no matches for kind`, which during an incident points at RBAC rather than
  at the version; the refusal names the artifact's version and the served
  ones, and `--force` overrides it. A cluster whose CRD cannot be read (an
  etcd restore still in progress) is left to the apply.

- `hack/restore.sh --force` restores again. The artifact carries the
  `resourceVersion` it was captured at, and that field is a precondition on
  both writes, so restoring over a live object that had moved on (exactly the
  case `--force` exists for) sent a precondition that could never be satisfied:
  `oc apply` and the status replace were both refused, the script exited with
  the spec half restored and told the operator to re-run, and the re-run hit
  the identical conflict. Under `--force` both writes now go out from a copy of
  the artifact with that field removed, so the restore completes and a repeated
  run reaches the state the first one reached. Without `--force` nothing
  changed: the artifact is sent as captured, and the staleness guard and the
  write still agree.

- `baseline_security_remediation_batches_total` no longer counts one batch once
  per retry. The finish path strips the batch annotations from the cluster and
  then counts the outcome, but clears `status.remediationBatch` only in the
  trailing `Status().Update`. When that write failed, every reconcile re-ran
  the whole finish: the pool resumes were no-ops, the counter was not, and the
  outcome of a single batch accrued once per requeue for as long as the status
  subresource stayed unwritable. The finish path now counts only when the
  cluster still carries that batch's annotations, which the earlier finish
  removed.

- The score trend, the per-profile sparklines, and the per-profile score badges
  no longer assume `status.history` is stored oldest-first. That ordering is a
  write-side convention (the operator appends) and not a schema constraint, so
  a restored or hand-edited ring could arrive in any order. The trend's
  accessible label reads the first and last points as the direction of travel,
  so an out-of-order ring announced the trend backwards ("moved from 70 to
  90"), and a per-profile badge could show an older scan's score than the
  current one. Points are now ordered by instant when the ring is read, and the
  newest point is resolved by instant rather than by array position.

- The score trend and per-profile sparklines clamp a `status.history` score
  into the CRD `[0,100]` bounds before plotting it, the same bound the operator
  enforces on write and every other `status.score` read already applied. A
  restored or hand-edited snapshot outside the range was labelled verbatim
  ("Score moved from 5,000 to 90"), colored from an impossible value, and drawn
  as a point the chart domain had to clip.

- Console text renders correctly in non-English locales. Untrusted cluster and
  API messages (apiserver `Status.message`, compliance-operator condition
  text, a chunk-load rejection reason) are now bidi-isolated with `dir="auto"`
  the way check titles and waiver names already were, so an Arabic or Hebrew
  value no longer reorders the punctuation around it. Counted strings
  (orphaned waivers, extra rules, chart scan counts) carry a real plural key,
  so a locale with more than two forms selects the right one instead of
  always reading the `_other` form. The waiver field-length message quotes the
  limit constants through the locale-aware number formatter rather than baking
  Latin digits into a translatable string, the two Remediation confirmation
  sentences name the remediation as its own element so a translated locale can
  put it where the grammar needs it, and the empty-state link pair is joined
  with the locale's own list punctuation instead of a hardcoded middle dot.

- The metrics endpoint serves the service-ca certificate again. The manager
  Deployment projected the service-ca Secret with `defaultMode: 0400`, and the
  kubelet writes secret-volume files root-owned while the manager runs at the
  image UID 65532 with no `fsGroup` (OLM owns the pod spec), so the projected
  `tls.crt` and `tls.key` were never readable. The metrics server fell back to
  its self-signed pair, permanently, and the `serving-cert-secret-name`
  annotation on the metrics Service means the cluster metrics stack verified
  against the service CA, so the ServiceMonitor had no working target and the
  bundled PrometheusRule alerts could not fire. The mode is now 0644: the
  serving certificate is published in the cluster service CA bundle anyway, and
  a projected volume cannot be tightened past what the process UID can read.

- The console plugin image builds again. `yarn build` runs the transferred-byte
  size gate, but `.dockerignore` excluded `tools/*` and re-included only
  `tools/attribution`, so `tools/size` was missing from the build context and
  the build stage failed on the `ts-node tools/size/check.ts` step. The size
  generator is now copied into the build stage alongside the attribution one.

- `restore.sh` no longer restores a backup over a live `ClusterBaseline` that
  has moved on since it was taken. The MANIFEST records the `resourceVersion`
  the backup holds, and the script now reads the live one before writing: a
  mismatch discarded every waiver edit and batch annotation made in between,
  silently, with no soft-delete window behind it. The restore is refused with
  both versions named until `--force` says it was meant. The age of the
  artifact, which is the RPO the restore actually buys, is now reported in the
  restore summary and called out when it is past a week.

- The status size budget mis-sized text that is not valid UTF-8, which a
  status restored from a protobuf backup can carry. An ill-formed byte is
  coerced rather than rejected, and the count of what the encoder then writes
  has changed with the toolchain: the Go release this operator pins spells the
  coercion as the six-byte `\ufffd` escape, Go 1.27 as the raw three-byte
  U+FFFD rune. Counting the three-byte form under-counted on the pinned
  toolchain, so a failure list built from the budget could land past the CRD
  bound and the next status write would be rejected, freezing reconcile until
  the object was hand-edited. The budget counts the six-byte form, which is
  exact on the pinned toolchain and trims three bytes early per ill-formed
  byte on a newer one.

- Deleting `ClusterBaseline/cluster` logs, at the moment the finalizer drops,
  that the waivers and score history are not recoverable and names
  `hack/backup.sh`. Nothing restores a deleted CR, and that was the last
  moment the operator could still see the object.

- A waiver reason, a remediation error message, and the text in the printable
  report lost their zero-width joiners and non-joiners on the way in and out.
  The invisible-character filter dropped every Unicode format character, so a
  family emoji was stored as three separate emoji and a Persian or Arabic
  compound lost the joiner that carries its meaning. Free text now keeps those
  two joiners while still dropping the characters that only exist to hide the
  next one (BIDI controls, zero-width space, BOM, word joiner). The waiver
  `requestedBy` / `approvedBy` fields keep the full filter, where a joiner in
  front of a name is a spoof and no identity needs one. CSV export is
  unchanged: a hidden character in front of a formula sigil is still neutralized
  there.

- A failed ClusterBaseline watch rendered the Overview, Profiles, and
  Remediations tabs as "Baseline not configured" with a **Create default
  baseline** button. A 403 on `clusterbaselines.compliance.openshift.io`, or a
  missing CRD, was indistinguishable from an absent CR, so the page told the
  admin a resource the operator may already have created did not exist. The
  tabs now render a danger state naming the read failure. The **Create default
  baseline** button also stays silent when it loses the create race, which on a
  broken watch left the click with no output at all; it now says the CR exists.

- Two failing watches showed only the first message in the page banner, and the
  second one's text was never rendered anywhere. The banner now carries every
  watch error.

- A failed async-chunk load and a failed report-exporter load both reported one
  fixed sentence and discarded the rejection, so a stale chunk id after a
  console upgrade was indistinguishable from an unreachable CDN. The reason is
  now shown.

- A report download whose `click()` threw left its hidden anchor element in the
  page, one per failed export.

- `baseline_security_scan_interval_seconds` could report a value that depended
  on which process published it first. The walk behind the gauge started at the
  publisher's clock, so an annual schedule crossing a leap year reported 365d
  from one phase of the year and 366d from another, and the per-process memo
  froze whichever phase arrived first under a key that carries the schedule
  alone. The walk is now anchored to a fixed epoch, so the value is a function
  of `spec.schedule` and nothing else. The `ComplianceScanStale` threshold moves
  by at most one day, and only for annual schedules.

- `manager --help` documented the `KUBECONFIG` fallback without pinning the
  `--kubeconfig` flag it sits behind. The flag was registered by a transitive
  package's `init`, which upstream marks for removal, so the documented
  precedence could have outlived the flag. It is now registered by the binary
  itself, the help names the full resolution order, the start-up
  `configuration` log records whether a kubeconfig was passed (not its path),
  and a test fails if the flag ever goes missing again.

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

- The failure-list size budget over-counted a name carrying ill-formed UTF-8 by
  four bytes per bad byte, so a list of such names was truncated well before
  the status limit it is clamped against. `encoding/json` writes the
  three-byte U+FFFD replacement character for an ill-formed byte; the estimate
  still assumed the six-byte `\ufffd` escape it emitted in older toolchains.
  A `newlyFailed` / `fixed` list built from a restored protobuf backup, which
  can carry lone continuation bytes, now keeps the names it used to drop.

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

- `newlyFailed` reported long-standing failures as new regressions on a
  heavily-failing cluster. The apiserver object-size budget can only keep a
  prefix of `previousFailures` and `diffBaseFailures`, but the scan diff was
  computed against the full in-memory FAIL set, so the check names the budget
  dropped came back as regressions on every subsequent scan. The FAIL set is now
  trimmed to the per-list size share before it is diffed or stored, which makes
  all four failure lists subsets of one set the budget already admits. Checks
  past the share are now invisible rather than permanently reported; a cluster
  whose failing-check names exceed the share reports a bounded, stable subset.

- A downloaded report could be saved under a name Windows refuses. The
  `Content-Disposition` name is derived from the cluster baseline, so a cluster
  name ending in a dot or a space (`prod.`, `prod `) lost the suffix and saved
  as `report.csv` or as nothing at all, and a cluster whose name began with a
  device Windows reserves (`con`, `nul`, `com1`, `lpt1`) landed in the
  filesystem root or nowhere. Trailing dots and spaces are now trimmed and a
  reserved stem is prefixed with `_`. The length cap still applies and still
  never ends on half a surrogate pair.

- A slow profile read or a slow clipboard write could land after a newer one.
  The per-row **Edit** buttons stay live while a profile is loading, so a second
  click started a second fetch and the slower response pre-filled the form with
  the other profile; a clipboard copy that resolved late reported a failure, or
  a success, for a copy the admin had already replaced. Both paths fence on a
  monotonic token and drop a result that a later action superseded.

- The bundle, catalog, and console plugin image digests followed the clock.
  `SOURCE_DATE_EPOCH` clamps image and layer creation timestamps, not the
  mtimes a `COPY` carries into a layer, so `bundle/` (stamped by the checkout),
  `catalog/` (only clamped locally, not on the CI path) and `dist/` (stamped
  when webpack ran) each put build time into the layer. The same source built
  twice, or built locally and in CI, produced two different digests, so a digest
  comparison could not distinguish a real change from a clock. The three trees
  are now clamped to `SOURCE_DATE_EPOCH` through one script
  (`operator/hack/normalize-mtimes.sh`), and `make verify-versions` fails if a
  clamp is dropped.

- `yarn build` failed on the console plugin's installed dependency closure with
  "no LICENSE file to reproduce" for 120 packages, most of which do ship their
  license text: npm's canonical spelling is the lowercase `license`, and the
  gate only looked for `LICENSE` and its upper-case siblings. The lookup is now
  case-insensitive, and a package that ships no license file at all no longer
  fails the build when its `package.json` declares an identifier the project
  already accepts: the notice records `(no file shipped)` and the declared
  grant is the entry. The gate still refuses a copyleft term, an identifier
  nobody has read here, free text, and a missing license field, whether or not
  a file ships. Three identifiers the closure needed were read and added to the
  accepted list with their reasons: `BlueOak-1.0.0` (glob, lru-cache,
  minimatch, minipass, path-scurry) and `CC-BY-3.0` / `CC-BY-4.0` (caniuse-lite,
  spdx-exceptions, both data packages). None is copyleft or source-available,
  and each ships its license text into the notices file.
  `dist/THIRD-PARTY-NOTICES.txt` therefore lists more packages with a file name,
  and names the fileless ones.

### Security

- The console plugin's dependency tree carried two advisories the audit step
  reported on every run. `js-yaml` 3.15.1 (GHSA-2883-xcg3-v3hh: CPU exhaustion
  from empty merge sources) came in through `@istanbuljs/load-nyc-config`, and
  `qs` 6.15.3 (GHSA-x5fp-wj9c-mxmx: array-limit bypass through bracket-key
  comma parsing, and GHSA-4mjr-xmp4-gh2g: denial of service through an
  attacker-controlled `isBuffer`) came in through `express`. Both are pinned up
  in `resolutions`, and `yarn npm audit` no longer reports them.

- `react-router` stays on the version the console 4.22 line provides and is
  excluded from the audit by name, with the reason recorded beside
  `--exclude immutable` in the workflow. The console provides it as a shared
  singleton (`ConsoleRemotePlugin` sets `import: false`, so no react-router
  code reaches `dist/`), the plugin SDK declares the peer range as `~7.13.1`,
  and the build asserts the installed version against that range, so a bump
  fails `yarn build` with `Console provides shared module react-router ~7.13.1
  but plugin uses version X`. The advisories the audit lists for 7.13.x are in
  RSC mode, SSR hydration, single-fetch, and the `__manifest` server path,
  none of which a browser-only plugin runs. Clearing them means the console
  moving react-router and the SDK peer range following it; re-audit when the
  SDK reaches `~7.18.1` (`4.23.0-prerelease.5`).

- Base-image CVE patches: digest bumps for ubi9 `go-toolset`, `ubi-micro`,
  `nodejs-22`, and `nginx-120`, and for `operator-framework/opm`. The Node
  patch in the plugin build image moved from 22.23.1 to 22.23.2, and
  `.nvmrc` follows it. Go stays on the `GOTOOLCHAIN` pin.

- `hack/must-gather.sh` collected the Compliance Operator objects with their
  scanner output intact. The account-related CIS and CCSR rules return what
  they checked, so `compliance.yaml` could carry a `/etc/passwd` or
  `/etc/shadow` listing, `getent` output, or an audit line naming a real
  account, into an archive that is built to be attached to a support case. The
  scanner prose (`details`, `standardOutput`, `summary`, `checkError`, and a
  `ComplianceCheckResult`'s `status.result`) is now dropped from the dump.
  Control identity, the compliant flag, the scan verdict, and the timestamps
  stay, so a scan-failure triage is unaffected. `hack/backup.sh` is unchanged
  and still writes the unredacted object, as `docs/RESTORE.md` says.

- `hack/must-gather.sh` no longer dumps a Secret into a support archive. It
  collected every object named in `status.relatedObjects`, and the only filter
  on that list was a character check, so a hand-edited or etcd-restored
  `relatedObjects` entry naming `secrets` was collected like any other object,
  putting the metrics TLS private key and the scraper service-account token
  into an attachment the operator can no longer redact. Collection is now
  pinned to the six kinds the reconciler actually writes.

- The operator built against `google.golang.org/grpc` v1.82.1, which is
  affected by GO-2026-6348 (heap exhaustion from HTTP/2 DATA frame
  fragmentation) and is fixed in v1.83.1. `govulncheck` reaches it from
  `cmd/main.go` through the manager start, so an API server that fragments its
  responses could drive the operator out of memory. Pinned to v1.83.1, which
  brings the `go.opentelemetry.io/otel` core modules to v1.44.0 with it.

- The operator namespace now ships a `NetworkPolicy`. Any pod in the cluster
  could previously open a TCP connection to the operator's metrics port 8443;
  the bearer token was the only control. Ingress is now denied on every
  operator port except 8443, and only for `openshift-monitoring` (the
  platform Prometheus scrape) and the service-ca operator (which mints the
  serving cert the scrape verifies against). Egress is deliberately left
  unrestricted so the policy cannot intersect the platform's own policies and
  cut the operator off from the API server.

- The serialized-size budget that trims `status` failure lists under-counted
  a string carrying ill-formed UTF-8 by up to 4 bytes per bad byte, because it
  counted the three-byte replacement rune where `encoding/json` writes the
  six-byte escape. A `ClusterBaseline` whose failure names came back from a
  protobuf restore with lone continuation bytes could therefore exceed the
  size bound and fail every subsequent status write, wedging conditions,
  score, and phase. The count now uses the wider of the two forms, which can
  only trim a list early.

- A console write is now denied while its access review is still in flight, not
  only once the review comes back negative. `mayWrite` is the single chokepoint
  every mutation passes through, and it read `allowed` alone, so a permission
  revoked between the moment a control rendered and the moment it was clicked
  could still be spent on the wire when the review had not resolved yet. The
  check now fails closed on an unresolved review, matching what the plugin's
  contributor rules already stated.

- `hack/restore.sh` now refuses a backup artifact that holds more than one YAML
  document. `oc apply -f` and `oc replace -f` apply every document in a
  multi-document file, so a backup directory with a second document appended
  after the `ClusterBaseline` would have been written to the cluster with the
  restoring operator's own credentials, whatever privilege it held. The
  existing kind and apiVersion checks match on any line and could not see the
  extra document, and the MANIFEST sha256 does not help: it lives in the same
  directory and is recomputable by anyone who can edit the artifact. Backups
  taken by `hack/backup.sh` are a single named object and never contain a
  `---` separator, so no valid backup is affected.

- `hack/restore.sh` now stops, changing nothing, when it cannot read the live
  `ClusterBaseline/cluster`. A failed read left the resourceVersion comparison
  with an empty value, which read the same as an absent object: the rollback
  guard was skipped, and an out-of-date backup was applied over a live object
  that had moved on, discarding every waiver edit and remediation batch
  annotation made since, with no `--force` and no warning. `--force` does not
  override it, since the operator cannot have meant to clobber an object whose
  current resourceVersion was never read.

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

### Migration notes

- The operator namespace now ships a default-deny ingress `NetworkPolicy` (see
  **Security** above). **Before:** any pod in the cluster could open a TCP
  connection to the operator's metrics port 8443, with the bearer token as the
  only control, so a scraper, a sidecar, or an attacker pod could read the
  metrics. **After:** only pods in `openshift-monitoring` (the platform
  Prometheus scrape) and `openshift-service-ca-operator` /
  `openshift-service-ca` (which mints and refreshes the serving cert) may
  reach port 8443, and every other operator port is denied everywhere. A
  custom Prometheus, a federating scrape, or any other client outside those
  namespaces now gets a refused connection with no error on the operator side,
  and no series to show for it. Platform monitoring on a supported 4.22 host
  is unaffected. Egress is unrestricted on purpose, so the operator keeps its
  cluster-wide reads and its path to the API server. To scrape from a
  namespace of your own, add its name to the `from.namespaceSelector` in
  `operator/config/manager/networkpolicy.yaml` (or grant a `NetworkPolicy` of
  your own that selects the operator pods and allows 8443 from that
  namespace); egress from the scraper is unaffected.

- A custom role that granted only `patch` on `clusterbaselines.compliance.openshift.io`
  loses two remediation controls and one authoring control on upgrade (see
  **Changed** above). **Before:** that role batch-applied remediations, toggled
  auto-apply, and authored and edited tailored profiles. **After:** **Batch
  apply** and **Auto-apply** need `patch` on `complianceremediations` in
  `openshift-compliance`, the **Author** button needs `create` on
  `tailoredprofiles` there, and **Edit** on a bound profile needs `update` on
  the same resource. The controls render disabled with the reason on hover
  rather than failing on submit, and the toggle refuses rather than writing the
  baseline. `cluster-admin` and the built-in `admin` ClusterRole in that
  namespace already hold all three verbs. An install that reconciled through the
  custom role needs no change, because the operator holds its own grants.

- Recording rules and dashboard panels that read
  `baseline_security_last_scan_timestamp_seconds`,
  `baseline_security_newly_failed`, `baseline_security_remediation_batch_active`,
  `baseline_security_remediation_batch_started_timestamp_seconds`, or
  `baseline_security_scan_interval_seconds` see no series on a replica that has
  not published since it started, where the seed removal (see **Changed** above)
  leaves them absent rather than 0. Add `or vector(0)` to the PromQL wherever a
  missing series should read as zero; alert firing is unchanged, because
  `ComplianceStatusStale` catches the never-published case through its
  `absent()` disjunct.

- A `ClusterBaseline/cluster` whose failing check names exceed the per-list size
  share reports a bounded, stable subset (see **Fixed** above). Checks past the
  share are no longer counted in `newlyFailed`, `fixed`, `previousFailures`, or
  `diffBaseFailures`, and the same check stops counting in both directions, so a
  score or a failure list is no longer complete on such a cluster. No stored
  object changes shape; the lists are shorter and self-consistent.

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


### Docs

- Threat model: add `docs/THREAT_MODEL.md` (entry points, trust boundaries,
  ranked threats, mitigations mapped to code) and link it from SECURITY.md.
- Document operator process flags and env vars in the README, and comment
  the optional ClusterBaseline spec fields on the sample CR.
- README Versioning: the no-`replaces` upgrade path is install-the-new-head
  (CatalogSource tag + delete a leftover Subscription/CSV); OperatorHub
  capability is Basic Install.



## [0.5.15] - 2026-08-26
### Fixed

- CI: run `yarn lint:oxlint` in the console-plugin job. The type-aware
  anti-slop rules were configured but never gated, so a violation could land
  on `main` unnoticed.


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
### Added

- Console e2e coverage for the Overview/Remediations governance affordances
  (inline schedule edit, invalid-cron rejection, scoring-mode readout, score
  trend card, HTML report export, batch-apply confirmation).


### Changed

- Dependency maintenance: CI actions (actions/checkout v7, docker/setup-buildx
  v4, the latter clearing the Node 20 deprecation warning), ubi9 base-image
  digests (same Node 22.23.1 / Go 1.26.5), and console-plugin
  @patternfly/react-charts 8.6.0, eslint 10, webpack-dev-server 6. k8s.io/*
  0.36, controller-runtime 0.24, @types/node 26, and react 19 were held back to
  stay on the OpenShift 4.22 / k8s 1.35 / React 18 support baseline.


### Fixed

- e2e: the remediation apply-confirmation screenshot spec matched the wrong
  button (name-scoped "Apply <name>") and silently skipped; it now captures the
  confirmation modal.



## [0.5.5] - 2026-07-14
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



## [0.5.0] - 2026-07-13
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



OLM upgrade edge: `baseline-security-operator.v0.5.0` replaces `v0.4.0`.

**Breaking:** the API group was renamed `baselinesecurity.io` →
`baselinesecurity.openshift.io`. This minor carries it (a hard rename at
`v1alpha1`, no conversion) per the project's 0.x policy that breaking changes
land in a minor bump. Existing `ClusterBaseline` CRs are under the old group and
must be recreated after upgrade (see Migration notes).

## [0.4.0] - 2026-07-11
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



OLM upgrade edge: `baseline-security-operator.v0.4.0` replaces `v0.3.1`.

## [0.3.1] - 2026-07-11
### Changed

- Per-profile Overview cards show Inconsistent counts (previously only on the
  composition donut).
- Dark-theme console coverage and screenshots.


### Fixed

- Stuck-install grace and errorMessage guard behavior from the 0.3.0 line
  carried forward; full e2e re-verified on OCP 4.22 / Compliance Operator 1.9.1.



OLM upgrade edge: `v0.3.1` replaces `v0.3.0`.

## [0.3.0] - 2026-07-10
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



OLM upgrade edge: `v0.3.0` replaces `v0.2.1`.

## [0.2.1] - 2026-07-09
### Fixed

- Bundle `installModes` aligned for cluster-wide (`AllNamespaces`) install.
- Packaging: relatedImages, upgrade edge, bundle validation in CI.



OLM upgrade edge: `v0.2.1` replaces `v0.2.0`.

## [0.2.0] - 2026-07-09
### Added

- Cluster-scoped `ClusterBaseline` API (`baselinesecurity.openshift.io/v1alpha1`).
- Operator: install/adopt Compliance Operator, own ScanSetting + bindings,
  deploy console plugin, aggregate score + history into status.
- Console plugin under Administration → Compliance (Overview, Results,
  Remediations, Profiles).
- OLM bundle + file-based catalog; string-enum spec; OpenShift-style conditions.



Initial packaged release.

[Unreleased]: https://github.com/maci0/openshift-baseline-security/compare/v0.7.0...HEAD
[0.7.0]: https://github.com/maci0/openshift-baseline-security/compare/v0.6.1...v0.7.0
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

