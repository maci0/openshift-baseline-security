## Why

The addon covers baseline scanning, scoring, waivers, and a native console UI for
one cluster, but a compliance product needs governance and depth the Compliance
Operator does not provide: waivers are permanent and unattributed, there is no
"what regressed since the last scan", node remediations reboot repeatedly,
TailoredProfiles can only be bound (not authored) from the console, the schedule
is read-only in the UI, the score ignores severity, and there is no non-OLM
install, no exportable report, no shipped trend dashboard, and no curated
hardening profile. This change closes those gaps. The Compliance Operator owns
scanning and check results; everything here is aggregation, governance, and UX we
layer on top, not a reimplementation of the operator.

## What Changes

- Waiver entries gain expiry, review date, and requester/approver attribution;
  expired waivers stop excluding their check and are surfaced for review.
- Per-check status is recorded across scans so the UI can show regressions
  (PASS->FAIL) and newly-failing checks since the previous scan.
- Remediations can be batch-applied in dependency order with automatic
  MachineConfigPool pause/resume so affected nodes reboot once, not per apply.
- TailoredProfiles can be created/edited (enable/disable rules, set variable
  values) and bound from the console, not only bound if pre-existing.
- The scan schedule is editable from the console; score history is tracked and
  trended per profile, not only globally.
- An opt-in severity-weighted score weights FAILs by check severity.
- A Helm chart installs the operator without OLM.
- A compliance report (PDF/HTML) can be exported for a point-in-time snapshot.
- A score-trend dashboard is shipped as a native console dashboard (Observe -> Dashboards, no Grafana) plus the in-console history chart.
- A curated NSA/CISA Kubernetes-hardening profile ships as a TailoredProfile.
- The controller watches Compliance CRs via a dynamic informer instead of
  requeue polling once the CRDs exist.

## Capabilities

### New Capabilities
- `waiver-governance`: expiry, review date, and attribution on waivers; expiry
  enforcement in the score; surfacing of expiring/expired waivers.
- `scan-diff`: per-check status history and a regression/newly-failing view
  between the current and previous scan.
- `guided-remediation`: dependency-aware batch apply with MachineConfigPool
  pause/resume so node remediations reboot once.
- `tailored-profile-authoring`: create/edit/bind a TailoredProfile (enable and
  disable rules, set variable values) from the console.
- `schedule-and-trend`: edit the scan schedule from the UI; track and trend
  per-profile score history.
- `severity-weighted-score`: opt-in scoring mode weighting FAILs by severity.
- `helm-chart`: non-OLM install of the operator and plugin via Helm.
- `compliance-report`: export a point-in-time compliance report (PDF/HTML).
- `score-trend-dashboard`: shipped trend visualization from the score metric.
- `hardening-profile`: curated NSA/CISA Kubernetes-hardening TailoredProfile.
- `dynamic-informer`: watch Compliance Operator CRs via informers, not polling.

### Modified Capabilities
<!-- Existing behavior is not yet baselined as openspec specs; the two features
     that alter existing behavior (waiver exclusion, score computation) are
     captured in full inside waiver-governance and severity-weighted-score. -->

## Impact

- operator API: ClusterBaseline.spec.waivers gains fields; spec gains
  scoring mode, and status gains per-check history / per-profile history and a
  regression summary. CRD regen + bundle.
- operator controller: aggregateStatus (waiver expiry, severity weighting,
  regression compute), a remediation batch/ordering path with MCP pause/resume,
  TailoredProfile create/update, informer-based watches.
- console plugin: waiver form fields + expiry surfacing, a Regressions view, a
  batch-apply flow, a TailoredProfile editor, a schedule editor, per-profile
  trend, a report export, a trend card.
- packaging/docs: Helm chart, shipped hardening TailoredProfile manifest,
  console dashboard ConfigMap, report assets; metrics may gain a severity-weighted
  score series.
- tests: unit + fuzz for new parsers/among untrusted input (waiver dates,
  inconsistent/severity parsing), e2e for each user-visible flow.
