## Context

The operator reconciles a singleton ClusterBaseline, aggregates Compliance
Operator results into a score + per-profile ResultCounts, and drives a console
plugin. Waivers, score, and history already live in the CR (spec.waivers,
status.score, status.history, status.profiles[]). This change adds governance and
depth on top of that model. The Compliance Operator remains the source of scan
results; we never re-run scans ourselves.

## Goals / Non-Goals

**Goals:**
- Add governance (waiver expiry/attribution), regression visibility, safer batch
  remediation, TailoredProfile authoring, schedule editing, weighted scoring,
  report export, a shipped trend dashboard, a hardening profile, and
  informer-driven watches.
- Keep every addition backward compatible: new spec fields optional, default
  behavior unchanged, older status tolerated.

**Non-Goals:**
- Reimplementing scanning, remediation rendering, or rule content (the Compliance
  Operator owns those).
- Multi-cluster/fleet, image scanning, non-CO engines (out of product scope).
- Publishing images to quay / OLM certification (tracked separately, needs creds).

## Decisions

- **Waiver governance as CR fields, not a new CRD.** Extend WaiverEntry with
  `requestedBy`, `approvedBy`, `reviewBy`, `expiresAt` (all optional). Expiry is
  evaluated in aggregateStatus against reconcile time: an expired waiver is simply
  not added to the waived set. Alternative (a Waiver CRD) rejected: waivers are a
  property of our view, keyed to a baseline, so they belong on the CR.
- **Scan diff via bounded fail-name sets, not full per-check history.** Store the
  previous completed scan's FAIL names in `status.previousFailures`, retain
  `diffBaseFailures`/`diffBaseScanTime` while late CheckResults settle, and
  compute `newlyFailed`/`fixed` as set differences. Avoids unbounded status
  growth; the transition set is what the UI needs. Alternative (store every
  scan's full status map) rejected for CR size. (Earlier draft said "last two
  statuses per check"; the fail-set design is what shipped.)
- **Batch remediation = pause MCP, set apply on each, resume.** Trigger is a
  one-shot annotation `baselinesecurity.io/batch-apply` from the UI. The operator
  patches MachineConfigPool `spec.paused` around remediation applies, with resume
  in a deferred/guaranteed path and pause ownership so admin-paused pools stay
  paused. Node reboot coalesces to once. Alternative (UI does the pausing)
  rejected: pausing MCPs is privileged and belongs in the operator with RBAC
  already scoped. Alternative (`spec.remediation.batch` intent) rejected to avoid
  desired-state bloat for a transient action.
- **TailoredProfile authoring writes CO CRs directly from the plugin.** The plugin
  creates/patches TailoredProfile CRs (enable/disable rules, set values) via
  k8sCreate/k8sPatch, gated on RBAC, then patches spec.tailoredProfiles. No new
  operator API. The operator already binds tailored names.
- **Severity weighting is an opt-in scoring mode.** Add `spec.scoring.mode`
  (Flat default | SeverityWeighted) and weight FAIL/PASS by the check severity
  when weighted. Keep the flat path untouched so existing tests and scores hold.
  Weights are a small fixed map (high=10, medium=5, low=2, unknown/info/missing=1).
  History points stay under the mode that wrote them; late same-scan refresh does
  not rewrite rings after a mode flip (annotation
  `baselinesecurity.io/history-scoring-mode`).
- **Per-profile history** extends the existing history ring to a per-profile
  keyed structure, same 30-cap semantics.
- **Report/dashboard/hardening are packaging + client artifacts**, not new
  controller logic: report is printable HTML generated in-browser from watched
  data; the console dashboard is a ConfigMap of Grafana-schema JSON reconciled by
  the operator (Observe → Dashboards, no Grafana server); the hardening profile
  is a shipped TailoredProfile YAML with a documented rule mapping.
- **Dynamic informer**: register watches for the compliance GVKs once the CRDs are
  present (controller-runtime source with a lazy/dynamic REST mapper), keeping
  the current requeue as a fallback. Ownership filtering already exists; map
  watch events to the singleton reconcile request.

## Risks / Trade-offs

- Expiry uses reconcile wall-clock → a paused/slow operator could keep an expired
  waiver active until the next reconcile. Mitigation: expiry re-evaluated every
  reconcile; requeue cadence already bounded.
- Fail-name lists for scan diff are capped at 4096 names each (CRD MaxItems) so a
  hostile or huge rule set cannot brick Status().Update admission.
- MCP pause/resume is destructive-adjacent (reboots). Mitigation: explicit
  confirm, guaranteed resume even on failure, e2e that asserts resume, and keep
  the existing single-apply path.
- Informer wiring against CRDs that may be absent at startup is fiddly.
  Mitigation: tolerate NoKindMatch, start watches lazily, keep polling fallback;
  e2e that the CR stays usable without the CRDs.
- Severity-weighted score changes a headline number. Mitigation: opt-in only,
  default flat; document the formula; unit tests pin both modes. History points
  across a mode flip may be incomparable until the next completed scan.

## Migration Plan

- Ship in phases; each phase is independently deployable and backward compatible:
  1. API + scoring (waiver fields, scoring mode, per-check/per-profile history) +
     controller + tests; regen CRD/bundle.
  2. Console: waiver form + expiry surfacing, regressions view, schedule editor,
     per-profile trend, report export.
  3. Guided remediation (operator MCP pause/resume + UI batch flow).
  4. TailoredProfile authoring.
  5. Packaging artifacts: native console dashboard ConfigMap, hardening TailoredProfile.
  6. Dynamic informer (internal; no user-visible surface).
- Rollback: new spec fields are optional; reverting the operator image restores
  prior behavior since defaults are unchanged and status additions are ignored by
  older code.

## Open Questions

Resolved during implementation:

- **Report format:** printable HTML (zero deps), not a bundled PDF library.
- **Batch-remediation trigger:** transient annotation
  `baselinesecurity.io/batch-apply`, not a durable spec field.
- **Score-trend dashboard:** native console dashboard ConfigMap
  (`console.openshift.io/dashboard`) under Observe → Dashboards, plus the
  in-console history chart. Metrics need UWM + the bundle ServiceMonitor.
