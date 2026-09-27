# Design decisions

ADR-style record of product design choices for OpenShift Baseline Security.
Folder layout and module boundaries are out of scope here; see
[PATTERNS.md](PATTERNS.md) and [SPEC.md](SPEC.md) for architecture and API shape.
Each record ends with its git creation date; new records must carry one.

Every record here is a decision already made, so every record is current: there
are no proposed, superseded, or deprecated entries. `**Status:** Keep` means
accepted and active; the sentence after it names the condition that would
reopen the decision. A record that stops matching the code is corrected in
place, and a reversed decision is a new ADR that names the record it replaces
(`Supersedes ADR-NNN`) while the old record's status line reads
`Superseded by ADR-MMM` and stops being `Keep`.

## Index

| ADR | Decision | Recorded |
|---|---|---|
| [ADR-001](#adr-001-orchestration-wrapper-not-a-scanner) | Orchestration wrapper, not a scanner | 2026-07-12 |
| [ADR-002](#adr-002-single-cluster-scoped-cr-clusterbaselinecluster) | Single cluster-scoped CR (`ClusterBaseline/cluster`) | 2026-07-12 |
| [ADR-003](#adr-003-score-and-history-live-on-the-cr-status) | Score and history live on the CR status | 2026-07-12 |
| [ADR-004](#adr-004-string-enums-for-installremediationconsolescoring) | String enums for install/remediation/console/scoring | 2026-07-12 |
| [ADR-005](#adr-005-waivers-as-specwaivers-entries-keyed-by-check-name) | Waivers as `spec.waivers` entries keyed by check name | 2026-07-12 |
| [ADR-006](#adr-006-batch-remediation-via-annotation--mcp-pause) | Batch remediation via annotation + MCP pause | 2026-07-12 |
| [ADR-007](#adr-007-console-plugin-has-no-backend) | Console plugin has no backend | 2026-07-12 |
| [ADR-008](#adr-008-severity-weighted-scoring-is-opt-in-history-is-mode-stamped) | Severity-weighted scoring is opt-in; history is mode-stamped | 2026-07-12 |
| [ADR-009](#adr-009-benign-inconsistent-collapse) | Benign INCONSISTENT collapse | 2026-07-12 |
| [ADR-010](#adr-010-ownership-via-suite-labels-not-namespace-wide-lists) | Ownership via suite labels, not namespace-wide lists | 2026-07-12 |
| [ADR-011](#adr-011-explicit-co-subscription-not-olm-package-dependency) | Explicit CO Subscription, not OLM package dependency | 2026-07-12 |
| [ADR-012](#adr-012-lazy-dynamic-informer-with-poll-fallback) | Lazy dynamic informer with poll fallback | 2026-07-12 |
| [ADR-013](#adr-013-scan-diff-bookkeeping-fields-are-internal) | Scan-diff bookkeeping fields are internal | 2026-07-12 |
| [ADR-014](#adr-014-pooled-score-not-the-mean-of-per-profile-scores) | Pooled score, not the mean of per-profile scores | 2026-07-13 |
| [ADR-015](#adr-015-history-advances-only-when-every-owned-suite-is-complete) | History advances only when every owned suite is complete | 2026-07-13 |
| [ADR-016](#adr-016-unstructured-clients-for-foreign-crs) | Unstructured clients for foreign CRs | 2026-07-13 |
| [ADR-017](#adr-017-ui-score-color-bands-vs-compliancescorelow-threshold) | UI score color bands vs `ComplianceScoreLow` threshold | 2026-07-13 |
| [ADR-018](#adr-018-score-gauge-uses--1-sentinel-ha-picks-newest-publisher) | Score gauge uses -1 sentinel; HA picks newest publisher | 2026-07-13 |
| [ADR-019](#adr-019-default-clusterbaselinecluster-on-operator-start) | Default `ClusterBaseline/cluster` on operator start | 2026-07-13 |
| [ADR-020](#adr-020-deleting-the-baseline-does-not-uninstall-the-compliance-operator) | Deleting the baseline does not uninstall the Compliance Operator | 2026-07-13 |
| [ADR-021](#adr-021-integer-floor-score-in-0-100-not-a-float) | Integer floor score in [0, 100], not a float | 2026-07-13 |
| [ADR-022](#adr-022-fixed-severity-weight-table-product-contract) | Fixed severity weight table (product contract) | 2026-07-13 |
| [ADR-023](#adr-023-scansetting-storage-and-roles-are-fixed-product-defaults) | ScanSetting storage and roles are fixed product defaults | 2026-07-13 |
| [ADR-024](#adr-024-dual-gots-product-contracts-ci-lockstep) | Dual Go/TS product contracts, CI lockstep | 2026-07-13 |
| [ADR-025](#adr-025-compliance-report-is-client-side-printable-html) | Compliance report is client-side printable HTML | 2026-07-13 |
| [ADR-026](#adr-026-score-trend-dashboard-is-a-native-console-configmap) | Score-trend dashboard is a native console ConfigMap | 2026-07-13 |
| [ADR-027](#adr-027-prometheus-score-gauge-has-no-scoring-mode-label) | Prometheus score gauge has no scoring-mode label | 2026-07-13 |
| [ADR-028](#adr-028-no-static-poddisruptionbudget-for-the-operator) | No static PodDisruptionBudget for the operator | 2026-07-15 |
| [ADR-029](#adr-029-an-impossible-date-schedule-degrades-it-does-not-silently-disable) | An impossible-date schedule Degrades, it does not silently disable | 2026-07-15 |
| [ADR-030](#adr-030-no-olm-replaces-graph-csv-capability-is-basic-install) | No OLM `replaces` graph; CSV capability is `Basic Install` | 2026-07-14 |
| [ADR-031](#adr-031-waiver-names-are-unique-at-admission-cel) | Waiver names are unique at admission (CEL) | 2026-09-02 |
| [ADR-032](#adr-032-manager-cache-is-namespace-scoped-not-cluster-wide) | Manager cache is namespace-scoped, not cluster-wide | 2026-09-27 |
| [ADR-033](#adr-033-operator-namespace-networkpolicy-is-ingress-only) | Operator-namespace NetworkPolicy is ingress-only | 2026-09-27 |

## ADR-001: Orchestration wrapper, not a scanner

**Decision:** Reuse the Red Hat Compliance Operator (OpenSCAP + content) for
scanning and remediations. This project owns defaults, status aggregation, and
the console UI only.

**Alternatives:** Bundle a custom scanner; reimplement OpenSCAP content.

**Tradeoff:** Zero scanner maintenance and free content updates via Red Hat
catalogs; dependent on CO CRDs, labels, and remediation semantics.

**Status:** Keep. Revisit only if CO is unavailable on a target platform.

*Recorded: 2026-07-12 (git history).*

## ADR-002: Single cluster-scoped CR (`ClusterBaseline/cluster`)

**Decision:** One singleton CR for desired posture and observed score/history.
No per-profile CRDs and no separate Waiver CRD.

**Alternatives:** Multi-instance baselines; Waiver CRD; ConfigMap-only config.

**Tradeoff:** Simple onboarding and OpenShift config-CR convention; status must
stay bounded (history rings, failure-name caps) to protect etcd and admission.

**Status:** Keep for single-cluster product scope (fleet is ACS/ACM territory).

*Recorded: 2026-07-12 (git history).*

## ADR-003: Score and history live on the CR status

**Decision:** Pooled score, per-profile counts, 30-entry history rings, and scan
diff (`newlyFailed`/`fixed` plus internal fail-name baselines) are written to
`ClusterBaseline.status`. Prometheus gauges mirror the same rollup.

**Alternatives:** External DB; ConfigMap history; full per-check status history.

**Tradeoff:** Zero external state and console can watch one object; CR size and
admission bounds limit how much detail we retain.

**Status:** Keep. Scan diff stores **fail-name sets** (`previousFailures` /
`diffBaseFailures`), not a map of last-two statuses per check (cheaper, enough
for regressions).

*Recorded: 2026-07-12 (git history).*

## ADR-004: String enums for install/remediation/console/scoring

**Decision:** Spec uses string enums (`Automatic`/`Manual`, `Managed`/`Removed`,
`Flat`/`SeverityWeighted`), not booleans, per OpenShift API conventions.

**Alternatives:** Booleans; free-form strings without CRD enum.

**Tradeoff:** Explicit third-state room and stable CEL/CRD validation; slightly
more verbose YAML.

**Status:** Keep.

*Recorded: 2026-07-12 (git history).*

## ADR-005: Waivers as `spec.waivers` entries keyed by check name

**Decision:** Accepted risk is a list on the baseline, keyed by
ComplianceCheckResult name, with optional expiry and attribution. Expired
entries stop excluding from the score.

**Alternatives:** Waiver CRD; annotations on each CheckResult.

**Tradeoff:** Audit fields stay with the baseline; no CO object mutation for
waivers. Expiry uses reconcile wall-clock; the poll requeue shortens to the
nearest active `expiresAt` (floored at 1s) so score/waived counts drop without
waiting the full steady 1m interval. Scan-diff (`newlyFailed`/`fixed` /
`previousFailures`) still tracks the raw FAIL outcome: accepting risk is not
reported as Fixed and does not hide a regression.

**Status:** Keep.

*Recorded: 2026-07-12 (git history).*

## ADR-006: Batch remediation via annotation + MCP pause

**Decision:** UI sets `baselinesecurity.openshift.io/batch-apply` (comma-separated
remediation names, capped at 256). Operator pauses target MachineConfigPools,
applies, then resumes. Pause ownership
(`baselinesecurity.openshift.io/batch-pause-owner` on each MCP the operator actually
paused) ensures admin-paused pools stay paused on resume. In-flight state is
dual-written: `status.remediationBatch` for the console, plus
`baselinesecurity.openshift.io/batch-started-at` and `baselinesecurity.openshift.io/batch-pools`
annotations so grace and pool recovery still work if a status-subresource write
fails. Safety valve: `batchResumeGrace` is 10m (zero or far-future `StartedAt`
treated as corrupt so the valve cannot stick forever). Alert
`RemediationBatchStuck` fires at ~20m so on-call has slack after forced resume.

**Alternatives:** UI pauses MCPs; durable `spec.remediation.batch` intent;
status-only bookkeeping without recovery annotations.

**Tradeoff:** Privileged pause stays in the operator; annotation is one-shot and
does not bloat desired state. Concurrent annotation writes need care (resource
version / merge patches). Dual-write costs two annotation keys but prevents
permanently paused MCPs when status updates fail mid-batch.

**Status:** Keep.

*Recorded: 2026-07-12 (git history).*

## ADR-007: Console plugin has no backend

**Decision:** All data and writes use the console k8s proxy and the user's
token (`useK8sWatchResource`, `useAccessReview`).

**Alternatives:** Operator REST proxy; dedicated API server.

**Tradeoff:** RBAC falls out of the platform; no second auth surface. Plugin
cannot do privileged work the user cannot.

**Status:** Keep.

*Recorded: 2026-07-12 (git history).*

## ADR-008: Severity-weighted scoring is opt-in; history is mode-stamped

**Decision:** Default score is flat `pass/(pass+fail)`. `SeverityWeighted` uses
fixed weights (high=10, medium=5, low=2, else=1; see ADR-022). History points
are captured under the mode active at write time; an annotation
`baselinesecurity.openshift.io/history-scoring-mode` prevents late CheckResult refresh
from rewriting completed snapshots after a mode flip.

**Alternatives:** Always weighted; store mode per `ScoreSnapshot` field; clear
history on mode change.

**Tradeoff:** Headline score can change on mode flip without a new scan; the
console warns while the history stamp mismatches. On the **next completed scan**
under the new mode, overall and per-profile history rings are cleared and a
fresh point is written (so MiniTrend / Score trend never mix Flat and
SeverityWeighted values). Avoids expanding the CRD with a per-snapshot mode
field; one ring of trend is lost across a mode flip.

**Status:** Keep. Per-snapshot mode on `ScoreSnapshot` only if product needs
continuous multi-mode charts without a break.

*Recorded: 2026-07-12 (git history).*

## ADR-009: Benign INCONSISTENT collapse

**Decision:** When the Compliance Operator marks a check `INCONSISTENT` only
because nodes disagree on PASS vs NOT-APPLICABLE/SKIP (the check does not apply
on some nodes), treat it as PASS (or NOT-APPLICABLE if no PASS). Any FAIL or
ERROR among node states stays INCONSISTENT. Applied in operator aggregation and
mirrored in the console `effectiveStatus` path so score, counts, metrics, and
Results agree.

**Alternatives:** Surface every CO INCONSISTENT as residual; only collapse in
the UI; require operators to tailor profiles.

**Tradeoff:** Multi-node pools stop looking broken for applicability splits;
genuine PASS-vs-FAIL splits still need review. Depends on CO annotations
(`inconsistent-source`, `most-common-status`); unknown node states fail closed
to INCONSISTENT.

**Status:** Keep.

*Recorded: 2026-07-12 (git history).*

## ADR-010: Ownership via suite labels, not namespace-wide lists

**Decision:** Built-in suites are `baseline-<profileKey>`; tailored suites are
`baseline-tp-<name>`. Status aggregation and console watches select
ComplianceCheckResults/Scans/Remediations by `compliance.openshift.io/suite`
membership so foreign CO bindings never enter the score or UI.

**Alternatives:** List the whole `openshift-compliance` namespace and filter in
memory; ownership annotations on every CCR; separate namespaces per baseline.

**Tradeoff:** Cheap and correct for multi-tenant CO use; suite naming is a
product contract (MaxLength on tailored names keeps label values valid). A
misnamed suite is invisible to the baseline.

**Status:** Keep.

*Recorded: 2026-07-12 (git history).*

## ADR-011: Explicit CO Subscription, not OLM package dependency

**Decision:** Install or adopt the Compliance Operator by reconciling Namespace,
OperatorGroup, and Subscription in `openshift-compliance`. Do not declare the
compliance-operator package in OLM `dependencies.yaml`.

**Alternatives:** Bundle dependency; require admin pre-install only; pin a
specific CSV.

**Tradeoff:** CO lands in its expected namespace and OperatorGroup (OLM v0
dependency resolution co-locates deps with the dependent, which breaks CO).
More reconcile code and catalog-source overrides for disconnected/OKD. Revisit
when OLM v1 dependency placement is reliable.

**Status:** Keep until OLM v1 dependency placement is proven for CO.

*Recorded: 2026-07-12 (git history).*

## ADR-012: Lazy dynamic informer with poll fallback

**Decision:** Watch compliance GVKs once CRDs exist (lazy RESTMapper probe +
dynamic source mapping every event to the singleton reconcile). The watches
use `PartialObjectMetadata`: the handler only needs namespace and the suite
label, and caching full CheckResult/Scan/Remediation bodies would pin
hundreds of MB on multi-profile clusters. Aggregation still live-lists
CheckResults (unstructured client, server-side suite selector) in pages of
500 so a single List cannot timeout or hold the whole set. The manager cache is
scoped to the namespaces those reads use (ADR-032). Keep a
requeue as a fallback: 1m steady, 15s while Progressing or batch Applying, and
shorten toward the soonest active waiver `expiresAt` (floored at 1s; see
ADR-005) so accepted-risk expiry is not stuck behind a full minute when watches
lag or are not yet up.

**Alternatives:** Poll only; fail manager start if CRDs are absent; import CO
typed clients and static watches; cache full unstructured objects for a
cached List.

**Tradeoff:** Event-driven when CO is present; still works during install or if
CRDs disappear. Dual paths mean a short lag is still possible when watches are
down; MaxConcurrentReconciles stays 1 so status writes stay simple. Metadata
watches trade a second live List (already required: unstructured bypasses the
cache) for bounded informer RSS.

**Status:** Keep.

*Recorded: 2026-07-12 (git history).*

## ADR-013: Scan-diff bookkeeping fields are internal

**Decision:** `status.previousFailures`, `status.diffBaseFailures`, and
`status.diffBaseScanTime` exist only so late CheckResults can correct
`newlyFailed`/`fixed`. They are not a consumer contract: shape and presence may
change in 0.x without a major bump. User-facing regression views use
`newlyFailed` and `fixed` only (Overview may read `diffBaseScanTime` as a
boolean "prior scan exists" signal).

**Alternatives:** Hide bookkeeping in a Secret/ConfigMap; full per-check status
history; omit late-arrival correction.

**Tradeoff:** Zero external state and correct diffs under slow CCR delivery;
status carries larger fail-name lists (capped at 4096 each). Because four such
lists at max length would exceed the ~1.5 MiB apiserver object limit and freeze
`Status().Update`, `sanitizeStatusForUpdate` also trims the four lists jointly to
a combined byte budget (`clampFailureListsToBudget`), degrading the diff on an
extreme cluster rather than wedging the reconcile. The current FAIL set is
truncated to one quarter of that budget (`failureListShareBudget`) before it is
diffed or stored, so all four lists are subsets of one truncated set: the
budget holds by construction and the diff can never report a check the trim hid
as a new regression. Consumers must not build external tools on the bookkeeping
fields.

**Status:** Keep; promote to a versioned subresource or annotation store only if
CR size or API clarity becomes a problem.

*Recorded: 2026-07-12 (git history).*

## ADR-014: Pooled score, not the mean of per-profile scores

**Decision:** `status.score` is one pooled ratio over every owned check result
across selected built-in and tailored profiles: `ΣPASS / (ΣPASS+ΣFAIL)` (or the
severity-weighted form). It is not the arithmetic mean of per-profile scores.

**Alternatives:** Mean of per-profile scores; min-profile (worst benchmark wins);
separate headline per selected profile only.

**Tradeoff:** A large profile dominates the headline (honest "overall posture");
a small tailored binding cannot hide a large CIS fail mass. Per-profile cards
and `status.profiles[]` still expose each benchmark independently.

**Status:** Keep. Revisit only if product wants "every selected benchmark must
pass" as the headline (min-profile) rather than overall mass.

*Recorded: 2026-07-13 (git history).*

## ADR-015: History advances only when every owned suite is complete

**Decision:** `LastScanTime`, history rings, and scan-diff baselines advance only
after every selected ScanSettingBinding's ComplianceSuite is DONE with valid
member-scan endTimestamps. A fast suite must not snapshot while another is still
running. The next generation advances only when every suite's earliest member
scan is newer than the prior global `LastScanTime`.

**Alternatives:** Per-suite history points; advance on first suite DONE; use
ComplianceScan end times without the suite transaction boundary.

**Tradeoff:** Multi-profile score/history stays coherent (one point per global
run); a stuck suite blocks history advance for healthy ones until it completes
or is deselected (surface via Progressing/scan-stale signals).

**Status:** Keep.

*Recorded: 2026-07-13 (git history).*

## ADR-016: Unstructured clients for foreign CRs

**Decision:** Touch Compliance Operator, OLM, console-operator, and MachineConfig
objects via unstructured/dynamic clients. Import typed APIs only for owned CRDs
and core Kubernetes types.

**Alternatives:** Vendor every foreign Go module; generate typed clients per
dependency version.

**Tradeoff:** No hard pin to CO/OLM API module versions and smaller go.mod; lose
compile-time field checks on foreign schemas (mitigated by tests, fuzz, and
defensive NestedField reads).

**Status:** Keep while foreign APIs are integration seams rather than owned
surface.

*Recorded: 2026-07-13 (git history).*

## ADR-017: UI score color bands vs ComplianceScoreLow threshold

**Decision:** Console badges use danger below 60, warning mid-band, success at
or above 90. The Overview donut title, HTML report score, and Observe
dashboard score singlestat use the same bands. Prometheus `ComplianceScoreLow`
fires when score is below **80** for 30m (excluding the -1 "no score"
sentinel; see ADR-018).

**Alternatives:** One shared threshold for UI and alerts; alert at 60 or 90.

**Tradeoff:** Operators see graduated color before paging; alerts stay less
noisy than badge color. The two scales can look inconsistent without this ADR.

**Status:** Keep. Revisit if support volume shows 80 is too high or too low.

*Recorded: 2026-07-13 (git history).*

## ADR-018: Score gauge uses -1 sentinel; HA picks newest publisher

**Decision:** `baseline_security_compliance_score` is **-1** when
`status.score` is nil (never scored, CRDs missing, scanning disabled, or
cleared). Alerts that care about a real score require `>= 0` before comparing
to a threshold. When multiple replicas can scrape gauges, alert expressions
select the series on the instance with the newest
`baseline_security_status_observed_timestamp_seconds` (not a plain
`max`/`sum` across instances).

**Alternatives:** Use gauge default 0 for "no score"; average all replicas;
only scrape the leader.

**Tradeoff:** -1 avoids false `ComplianceScoreLow` pages for a missing score
(0 would look like total non-compliance). Newest-publisher selection is
HA-safe after leader failover without requiring leader-only scrapes. Callers
must treat -1 as "absent", not as a numeric score.

**Status:** Keep.

*Recorded: 2026-07-13 (git history).*

## ADR-019: Default `ClusterBaseline/cluster` on operator start

**Decision:** On manager start, create `ClusterBaseline/cluster` with defaults
(CIS profile, Automatic CO install, Managed console) when no CR exists. Opt
out with env `BASELINE_SECURITY_SKIP_DEFAULT_CR=true` (GitOps / pre-seeded
CRs). Unrecognized values fail process start so a typo cannot silently
create the CR. Permanent auth/RBAC create failures stop retrying; transient
errors retry.

**Alternatives:** Require the admin to apply a sample CR; only document
`kubectl apply -f samples/`.

**Tradeoff:** Zero-config onboarding (G1) without a second install step;
clusters that manage the CR via GitOps can disable auto-create. Creating
desired state from a controller is unusual but matches OpenShift singleton
config CRs (`cluster` name, cluster-scoped).

**Status:** Keep.

*Recorded: 2026-07-13 (git history).*

## ADR-020: Deleting the baseline does not uninstall the Compliance Operator

**Decision:** Deleting the CR takes the owned objects with it, but through the
apiserver rather than the finalizer: the ScanSetting, ScanSettingBindings,
plugin Service/Deployment/PDB, ConsolePlugin CR, and dashboard ConfigMap all
carry a controller owner reference to the ClusterBaseline, so garbage collection
removes them. The finalizer does the two things GC cannot: it resumes any MCP
pause this operator owns and deregisters the plugin from
`consoles.operator.openshift.io/cluster`. It does **not** delete the Compliance
Operator Subscription, namespace, or foreign CO objects (other bindings,
remediations already applied).

**Alternatives:** Uninstall CO on CR delete; leave ScanSettingBindings
orphaned.

**Tradeoff:** Adoption-safe: if CO was pre-installed or shared, removing the
baseline cannot tear down another team's scans. Operators who want CO gone
uninstall it separately. Owned bindings are pruned so the baseline does not
leave scan noise behind.

**Status:** Keep.

*Recorded: 2026-07-13 (git history).*

## ADR-021: Integer floor score in [0, 100], not a float

**Decision:** `status.score` and history points are `int32` percent values
computed with floor division (`pass*100/total` or the severity-weighted
equivalent). Nil means uncountable (no PASS/FAIL mass). Out-of-range values
are clamped before Status update so admission cannot freeze reconcile.

**Alternatives:** Float 0.0-1.0 or 0.0-100.0; ceil/round-half-up; always
publish 0 when unscored.

**Tradeoff:** Matches OpenShift printcolumn / badge UX and CRD
Minimum/Maximum validation; avoids float noise in history and Prometheus.
Floor under-reports by less than one percent versus true ratio; product
accepts that for stable integers.

**Status:** Keep.

*Recorded: 2026-07-13 (git history).*

## ADR-022: Fixed severity weight table (product contract)

**Decision:** SeverityWeighted scoring uses a fixed, case-sensitive weight
table shared by the operator and console: high=10, medium=5, low=2, and
unknown/info/missing/other=1. Weights are not admin-configurable.

**Alternatives:** Configurable weights on the CR; CVSS-style continuous scale;
case-insensitive severity matching.

**Tradeoff:** Identical headline scores on every cluster without another knob;
admin cannot tune "how much high costs." Case-sensitive matching matches
Compliance Operator's lowercase severity field/label; unexpected casing falls
through to weight 1 (fail closed, not silent half-weight).

**Status:** Keep. Revisit only if product requires per-org weight policy.

*Recorded: 2026-07-13 (git history).*

## ADR-023: ScanSetting storage and roles are fixed product defaults

**Decision:** Owned ScanSetting always sets `roles: [worker, master]`,
`rawResultStorage.size: 1Gi`, and `rawResultStorage.rotation: 3`. These are
not ClusterBaseline spec fields. Schedule and auto-apply remediations remain
the only ScanSetting knobs driven by the CR.

**Alternatives:** Expose storage size/rotation/roles on the CR; leave CO
server defaults untouched after first create.

**Tradeoff:** Zero-config scans that match Compliance Operator docs teaching
(1Gi, rotation 3, both node roles); less flexibility for clusters with no
default StorageClass or custom role labels. Pending PVC >2m already surfaces
via `ScanStorageReady` / Degraded. Admins who need different storage policy
edit the ScanSetting carefully or own scanning outside this product.

**Status:** Keep until a real customer needs CR-level storage/role policy.

*Recorded: 2026-07-13 (git history).*

## ADR-024: Dual Go/TS product contracts, CI lockstep

**Decision:** Product constants that shape score math, caps, suite naming,
and annotation keys are duplicated in the Go operator and the TypeScript
console (no shared codegen). A `make verify-product-lockstep` gate (also in
CI and `make bundle`) asserts the two surfaces stay equal: ProfileKey set,
default schedule, MaxItems caps (profiles/tailored/waivers/batch), severity
weights, history scoring-mode annotation, batch-apply annotation/key, and
the operator-side failure-list cap (`FailureListMax` / MaxItems=4096 on
scan-diff fields; console does not write those lists). It also pins the
Compliance Operator keys both sides hard-code off cluster objects (suite,
check-severity and scan-name labels; `inconsistent-source` and
`most-common-status` annotations), the `-node-` scan-name delimiter, the
operator-side `HistoryMax = 30` history ring, and the plugin serving contract
across Go, `nginx.conf`, and the plugin Dockerfile (listen port, `EXPOSE`,
healthz location).

**Alternatives:** Generate TS from Go (or CRD OpenAPI); single shared JSON
contract file; trust dual unit tests only.

**Tradeoff:** No codegen pipeline for two languages; risk of silent UI vs
status score drift if someone edits one side. Explicit lockstep check is
cheap and fails the PR before merge. Dual unit tests remain for behavior;
lockstep covers the constant table.

**Status:** Keep while monorepo holds both deliverables. Drop or replace with
codegen if the console splits to its own repo without a shared contract.

*Recorded: 2026-07-13 (git history).*

## ADR-025: Compliance report is client-side printable HTML

**Decision:** The console builds a self-contained printable HTML report in
the browser from already-watched ClusterBaseline / CheckResult / waiver
data. No server-side PDF library, no operator endpoint, no extra dependency.

**Alternatives:** Bundle a PDF renderer in the plugin; operator REST export;
server-side template in a sidecar.

**Tradeoff:** Zero new deps and no second auth surface (ADR-007); report
fidelity is limited to what the user can already see via RBAC. Large FAIL
lists and print CSS are the browser's problem. Untrusted rule text and
waiver reasons are HTML-escaped at build time.

**Status:** Keep. Revisit only if product requires offline PDF packaging or
bulk multi-cluster reports (out of single-cluster scope).

*Recorded: 2026-07-13 (git history).*

## ADR-026: Score-trend dashboard is a native console ConfigMap

**Decision:** The operator reconciles a `console.openshift.io/dashboard`
ConfigMap in `openshift-config-managed` (embedded Grafana-schema JSON, no
Grafana server). It renders under Observe → Dashboards once cluster (platform)
monitoring scrapes the bundle ServiceMonitor (the install namespace is
openshift-*, which user-workload monitoring never scrapes; the
`openshift.io/cluster-monitoring` namespace label opts it into platform
Prometheus). Dashboard write failures are best-effort (log only; never Degrade
scanning).

**Alternatives:** Ship a Grafana dashboard CR; require cluster monitoring
only; omit Observe and keep only the in-console MiniTrend.

**Tradeoff:** Same install path as ODF-style console dashboards; works on
direct and OLM installs; depends on cluster monitoring for live series.
Best-effort avoid
blocking the primary compliance path when the ConfigMap namespace or RBAC is
missing.

**Status:** Keep.

*Recorded: 2026-07-13 (git history).*

## ADR-027: Prometheus score gauge has no scoring-mode label

**Decision:** `baseline_security_compliance_score` is a single gauge with no
`mode` label. Flat and SeverityWeighted both publish into the same series.
CR history rings are mode-stamped and cleared on the next completed scan
after a mode flip (ADR-008); Prometheus historical samples are not rewritten
or split.

**Alternatives:** Label the gauge by mode (two series over time); reset the
gauge to -1 on mode flip; dual gauges.

**Tradeoff:** Simple alert expressions and one series for Observe dashboards;
a mode flip reinterprets subsequent samples under the new formula, so
long-range Prometheus charts can mix incomparable points across the flip.
Product accepts that discontinuity (same class of issue as pre-clear history
points). Callers that need mode-aware history should use CR `status.history`
plus the history-scoring-mode annotation, not raw PromQL ranges across flips.

**Status:** Keep. Add a mode label only if support volume shows mixed-mode
Prom charts are a real operator pain.

*Recorded: 2026-07-13 (git history).*

## ADR-028: No static PodDisruptionBudget for the operator

**Decision:** The operator Deployment runs `replicas: 2` with no shipped
PodDisruptionBudget. Correctness comes from leader election (a single active
reconciler); the second replica is warm standby for fast failover only.

**Alternatives:** Ship `minAvailable: 1` (or `maxUnavailable: 1`) as before;
have the operator reconcile its own topology-aware PDB at runtime.

**Tradeoff:** A static PDB deadlocks a voluntary node drain on single-node
OpenShift: both replicas necessarily sit on the one node, so the first
eviction succeeds, its replacement stays `Pending` (only node cordoned), and
the PDB then denies the second eviction forever. No static PDB value avoids
this with a fixed replica count (`maxUnavailable: 1` deadlocks identically once
the replacement cannot schedule). A runtime-reconciled PDB would fight OLM
ownership for marginal benefit. Since the operator is not in any data path, a
brief both-pods-down window during a rare voluntary drain is acceptable, and
leader election already guarantees no split-brain. The console-plugin PDB is
unaffected: the operator reconciles it at runtime and already deletes it on
SingleReplica (that is the plugin's serving path, where the same deadlock would
drop the UI).

**Status:** Keep. Revisit only if a customer needs guaranteed operator uptime
across voluntary disruptions on a multi-node cluster.

*Recorded: 2026-07-15 (git history).*

## ADR-029: An impossible-date schedule Degrades, it does not silently disable

**Decision:** A `spec.schedule` cron that parses cleanly but can never fire (an
impossible calendar date such as `0 0 31 4 *` / `0 0 30 2 *`) is treated as
`ScanConfigured=False` / `InvalidSchedule` (Degraded), exactly like a
syntactically-invalid cron. The last-good schedule is kept on the ScanSetting.

**Alternatives:** Write the never-firing cron to the ScanSetting as-is (report
Ready); reject it at admission with a CEL/pattern rule.

**Tradeoff:** A parseable-but-never-fires cron would otherwise be written to the
Compliance Operator, which then never scans, while the cadence-aware
`ComplianceScanStale` alert (keyed on `scan_interval_seconds`, which is 0 for a
never-firing schedule) stays permanently suppressed: a silent compliance gap.
Rolling it up to Degraded surfaces it through `ClusterBaselineDegraded`. Full
calendar validation in CEL is impractical, so detection lives in the
reconciler via `nextScanTime(...) == nil` on the raw spec, consistent with the
metric and next-fire computations.

**Status:** Keep.

*Recorded: 2026-07-15 (git history).*

## ADR-030: No OLM `replaces` graph; CSV capability is `Basic Install`

**Decision:** Every bundle is a standalone head on the `alpha` channel: the CSV
carries no `spec.replaces` and no `spec.skipRange`, and the catalog ships no
channel edges between versions. `spec.capabilities` is `Basic Install`.
Upgrading is install-the-new-head (point the CatalogSource at the new catalog
tag, install it, delete a leftover Subscription/CSV); ClusterBaseline CRs and
the CRD stay. `make verify-versions` fails if either field reappears.

**Alternatives:** Maintain a `replaces` chain (what 0.5.0 to 0.5.4 shipped);
publish `Seamless Upgrades` and hope OLM resolves the gaps.

**Tradeoff:** No in-place OLM upgrade for an installed CSV, so an upgrader has
to reinstall the channel head, which the CHANGELOG **Migration notes** spell
out. In exchange, no channel is left advertising a version that OLM cannot
actually deliver, and a pre-1.0 break (the 0.5.0 group rename) does not need a
`replaces` edge. `Basic Install` in OperatorHub describes that install-the-head
path honestly instead of promising an upgrade that does not exist.

**Status:** Keep while the channel is `alpha` with no 1.0 API. Reintroduce a
`replaces` graph only when a stable API exists and multi-version upgrade is a
stated requirement.

*Recorded: 2026-07-14 (git history, 0.5.5).*

## ADR-031: Waiver names are unique at admission (CEL)

**Decision:** `spec.waivers` entries must have unique `name` values, enforced
by a CEL rule on the list
(`self.all(x, self.exists_one(y, y.name == x.name))`, message "waiver names
must be unique"). The console replaces an existing entry for a check rather
than appending a second one, so the CR never carries duplicates.
`listType=map` is a merge key for strategic merge, not a uniqueness guarantee,
so it cannot carry this rule.

**Alternatives:** Rely on `listType=map` merge semantics; deduplicate in the
controller on read; keep duplicates admitted and let the console overwrite.

**Tradeoff:** A stored CR that already holds two waivers for the same check
(both were admitted before 0.6.0) is rejected by the apiserver on its next
write: patching, re-applying, or waiving from the console fails until an admin
removes the duplicate. That is a one-time upgrade step, documented in CHANGELOG
0.6.0 **Migration notes** with the `oc get` command that finds the offending
names. In exchange the score never double-counts one accepted risk and the rule
is visible to any client instead of being an operator convention.
Controller-side deduplication was rejected: it leaves the stored spec
self-contradictory and fails only after the write.

**Status:** Keep. The CRD is `v1alpha1` and 0.x, so a stored object can be
repaired by hand; a future change to this rule needs a migration note.

*Recorded: 2026-09-02 (git history, 0.6.0).*

## ADR-032: Manager cache is namespace-scoped, not cluster-wide

**Decision:** `ManagerCacheOptions` bounds the manager's informer cache to the
two namespaces the reconciler reads: `openshift-compliance` (scan-storage PVCs
and the compliance CRs the lazy watcher follows) and the plugin namespace
(Deployment, Service, PodDisruptionBudget). `ClusterBaseline` is cluster-scoped
and stays cache-wide; foreign CO/OLM/console objects are read unstructured and
bypass the cache. The metadata-only compliance informers cannot be named in
`ByObject` (the key would be a bare `*metav1.PartialObjectMetadata`, for which
`apiutil.GVKForObject` resolves no GVK, so `cache.New` would refuse to start),
so they ride `DefaultNamespaces` instead. No `cache.AllNamespaces` catch-all: it
re-admits the named namespaces through a field selector, and a namespaced read
added outside the two should fail loudly on a cache miss rather than quietly
widen the cache.

**Alternatives:** Leave the default cluster-wide cache per type; scope only the
explicit `ByObject` entries; watch the compliance CRs as typed full objects
instead of `PartialObjectMetadata`; keep a single dedicated cluster for the
plugin namespace.

**Tradeoff:** The first typed read is what starts an informer, so the scan-storage
PVC check cached every PersistentVolumeClaim in the cluster and the plugin check
cached every Deployment, Service, and PDB, and the unbounded compliance informers
held name, labels, and resourceVersion for every compliance object in every
namespace for the life of the process. Bounding them trades that heap for a
constraint: a namespaced typed read outside these two namespaces has to be added
here as a new `ByObject` entry or it will not be found. Cluster-scoped types are
unaffected (a multi-namespace cache routes them to its own cluster-wide cache).

**Status:** Keep. Revisit only if a namespaced typed read the reconciler cannot
avoid appears outside the two namespaces.

*Recorded: 2026-09-27 (git history).*

## ADR-033: Operator-namespace NetworkPolicy is ingress-only

**Decision:** The operator ships one `NetworkPolicy` in
`openshift-baseline-security` selecting only its own pods, with
`policyTypes: [Ingress]` and a single ingress rule: TCP 8443 from the
`openshift-monitoring`, `openshift-service-ca-operator`, and
`openshift-service-ca` namespaces. Egress is deliberately undeclared, so it
stays unrestricted. The console-plugin pods in the same namespace are not
selected (the policy matches `app: baseline-security-operator`), and the
operator holds no create rights on `networking.k8s.io`, so nothing in the
reconcile path can widen or add a policy at runtime.

**Alternatives:** A full default-deny (egress rules for the API server,
`openshift-compliance`, and the plugin namespace); one policy per
workload type; leave the namespace open and rely on the RBAC boundary.

**Tradeoff:** A default-deny policy needs the API server's address to be
written into a manifest, so it would go stale the moment a cluster is served
through a different endpoint. A NetworkPolicy is additive over the platform's
own policies in an `openshift-*` namespace, so an egress rule here intersects
with the cluster network operator's rather than replacing it, and an
over-narrow rule is a silent reconciliation failure. Enumerating the real
egress set needs a live cluster and a CNI whose enforcement is verified, not
a manifest change. In exchange the metrics port is no longer reachable from
any tenant namespace (the bearer token was the only thing between such a pod
and the metric values) and the plugin's `:9443` traffic is untouched. Both
namespace selectors are load-bearing and both fail silently: without the
service-ca namespaces the manager still starts, serves its self-signed
fallback cert, and the platform scrape fails verification; without
`openshift-monitoring` there is simply no scraper. Neither shows up as an
error the operator can report.

**Status:** Keep. Revisit when the operator's egress set is small and stable
enough to enumerate (a fixed API-server CIDR plus the plugin namespace), or
when a platform requirement forces egress to be declared.

*Recorded: 2026-09-27 (git history).*
