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
waivers. Expiry uses reconcile wall-clock (slow operator can delay drop until
next reconcile).

**Status:** Keep.

## ADR-006: Batch remediation via annotation + MCP pause

**Decision:** UI sets `baselinesecurity.io/batch-apply` (comma-separated
remediation names). Operator pauses target MachineConfigPools, applies, resumes
with a grace safety valve and pause ownership so admin-paused pools stay paused.

**Alternatives:** UI pauses MCPs; durable `spec.remediation.batch` intent.

**Tradeoff:** Privileged pause stays in the operator; annotation is one-shot and
does not bloat desired state. Concurrent annotation writes need care (resource
version / merge patches).

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
fixed weights (high=10, medium=5, low=2, else=1). History points are captured
under the mode active at write time; an annotation
`baselinesecurity.io/history-scoring-mode` prevents late CheckResult refresh
from rewriting completed snapshots after a mode flip.

**Alternatives:** Always weighted; store mode per `ScoreSnapshot` field; clear
history on mode change.

**Tradeoff:** Headline score can change on mode flip without a new scan; older
ring points may be incomparable across a mode change until the next scan. Avoids
destructive history wipe and avoids expanding the CRD for a rare case.

**Status:** Keep; revisit if product needs mode-labeled history charts.

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
requeue (1m steady, 15s while Progressing or batch Applying) as a fallback.

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
