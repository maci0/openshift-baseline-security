# Design decisions

ADR-style record of product design choices for OpenShift Baseline Security.
Folder layout and module boundaries are out of scope here; see
[PATTERNS.md](PATTERNS.md) and [SPEC.md](SPEC.md) for architecture and API shape.

## ADR-001: Orchestration wrapper, not a scanner

**Decision:** Reuse the Red Hat Compliance Operator (OpenSCAP + content) for
scanning and remediations. This project owns defaults, status aggregation, and
the console UI only.

**Alternatives:** Bundle a custom scanner; reimplement OpenSCAP content.

**Tradeoff:** Zero scanner maintenance and free content updates via Red Hat
catalogs; dependent on CO CRDs, labels, and remediation semantics.

**Status:** Keep. Revisit only if CO is unavailable on a target platform.

## ADR-002: Single cluster-scoped CR (`ClusterBaseline/cluster`)

**Decision:** One singleton CR for desired posture and observed score/history.
No per-profile CRDs and no separate Waiver CRD.

**Alternatives:** Multi-instance baselines; Waiver CRD; ConfigMap-only config.

**Tradeoff:** Simple onboarding and OpenShift config-CR convention; status must
stay bounded (history rings, failure-name caps) to protect etcd and admission.

**Status:** Keep for single-cluster product scope (fleet is ACS/ACM territory).

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

## ADR-004: String enums for install/remediation/console/scoring

**Decision:** Spec uses string enums (`Automatic`/`Manual`, `Managed`/`Removed`,
`Flat`/`SeverityWeighted`), not booleans, per OpenShift API conventions.

**Alternatives:** Booleans; free-form strings without CRD enum.

**Tradeoff:** Explicit third-state room and stable CEL/CRD validation; slightly
more verbose YAML.

**Status:** Keep.

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

## ADR-006: Batch remediation via annotation + MCP pause

**Decision:** UI sets `baselinesecurity.io/batch-apply` (comma-separated
remediation names, capped at 256). Operator pauses target MachineConfigPools,
applies, then resumes. Pause ownership
(`baselinesecurity.io/batch-pause-owner` on each MCP the operator actually
paused) ensures admin-paused pools stay paused on resume. In-flight state is
dual-written: `status.remediationBatch` for the console, plus
`baselinesecurity.io/batch-started-at` and `baselinesecurity.io/batch-pools`
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

## ADR-007: Console plugin has no backend

**Decision:** All data and writes use the console k8s proxy and the user's
token (`useK8sWatchResource`, `useAccessReview`).

**Alternatives:** Operator REST proxy; dedicated API server.

**Tradeoff:** RBAC falls out of the platform; no second auth surface. Plugin
cannot do privileged work the user cannot.

**Status:** Keep.

## ADR-008: Severity-weighted scoring is opt-in; history is mode-stamped

**Decision:** Default score is flat `pass/(pass+fail)`. `SeverityWeighted` uses
fixed weights (high=10, medium=5, low=2, else=1; see ADR-022). History points
are captured under the mode active at write time; an annotation
`baselinesecurity.io/history-scoring-mode` prevents late CheckResult refresh
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

## ADR-012: Lazy dynamic informer with poll fallback

**Decision:** Watch compliance GVKs once CRDs exist (lazy RESTMapper probe +
dynamic source mapping every event to the singleton reconcile). Keep a
requeue as a fallback: 1m steady, 15s while Progressing or batch Applying, and
shorten toward the soonest active waiver `expiresAt` (floored at 1s; see
ADR-005) so accepted-risk expiry is not stuck behind a full minute when watches
lag or are not yet up.

**Alternatives:** Poll only; fail manager start if CRDs are absent; import CO
typed clients and static watches.

**Tradeoff:** Event-driven when CO is present; still works during install or if
CRDs disappear. Dual paths mean a short lag is still possible when watches are
down; MaxConcurrentReconciles stays 1 so status writes stay simple.

**Status:** Keep.

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
status carries larger fail-name lists (capped at 4096 each). Consumers must not
build external tools on the bookkeeping fields.

**Status:** Keep; promote to a versioned subresource or annotation store only if
CR size or API clarity becomes a problem.

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

## ADR-017: UI score color bands vs ComplianceScoreLow threshold

**Decision:** Console badges use danger below 60, warning mid-band, success at
or above 90. Prometheus `ComplianceScoreLow` fires when score is below **80**
for 30m (excluding the -1 "no score" sentinel; see ADR-018).

**Alternatives:** One shared threshold for UI and alerts; alert at 60 or 90.

**Tradeoff:** Operators see graduated color before paging; alerts stay less
noisy than badge color. The two scales can look inconsistent without this ADR.

**Status:** Keep. Revisit if support volume shows 80 is too high or too low.

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

## ADR-019: Default `ClusterBaseline/cluster` on operator start

**Decision:** On manager start, create `ClusterBaseline/cluster` with defaults
(CIS profile, Automatic CO install, Managed console) when no CR exists. Opt
out with env `BASELINE_SECURITY_SKIP_DEFAULT_CR=true` (GitOps / pre-seeded
CRs). Permanent auth/RBAC create failures stop retrying; transient errors
retry.

**Alternatives:** Require the admin to apply a sample CR; only document
`kubectl apply -f samples/`.

**Tradeoff:** Zero-config onboarding (G1) without a second install step;
clusters that manage the CR via GitOps can disable auto-create. Creating
desired state from a controller is unusual but matches OpenShift singleton
config CRs (`cluster` name, cluster-scoped).

**Status:** Keep.

## ADR-020: Deleting the baseline does not uninstall the Compliance Operator

**Decision:** Finalizer cleanup removes owned ScanSetting/bindings, console
plugin resources, dashboard ConfigMap, and resumes any MCP pause this operator
owns. It does **not** delete the Compliance Operator Subscription, namespace,
or foreign CO objects (other bindings, remediations already applied).

**Alternatives:** Uninstall CO on CR delete; leave ScanSettingBindings
orphaned.

**Tradeoff:** Adoption-safe: if CO was pre-installed or shared, removing the
baseline cannot tear down another team's scans. Operators who want CO gone
uninstall it separately. Owned bindings are pruned so the baseline does not
leave scan noise behind.

**Status:** Keep.

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

## ADR-024: Dual Go/TS product contracts, CI lockstep

**Decision:** Product constants that shape score math, caps, suite naming,
and annotation keys are duplicated in the Go operator and the TypeScript
console (no shared codegen). A `make verify-product-lockstep` gate (also in
CI and `make bundle`) asserts the two surfaces stay equal: ProfileKey set,
default schedule, MaxItems caps (profiles/tailored/waivers/batch), severity
weights, history scoring-mode annotation, and batch-apply annotation/key.

**Alternatives:** Generate TS from Go (or CRD OpenAPI); single shared JSON
contract file; trust dual unit tests only.

**Tradeoff:** No codegen pipeline for two languages; risk of silent UI vs
status score drift if someone edits one side. Explicit lockstep check is
cheap and fails the PR before merge. Dual unit tests remain for behavior;
lockstep covers the constant table.

**Status:** Keep while monorepo holds both deliverables. Drop or replace with
codegen if the console splits to its own repo without a shared contract.
