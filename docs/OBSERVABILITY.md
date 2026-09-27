# Observability

The operator exports Prometheus metrics and ships PrometheusRule alerts plus a
native Observe → Dashboards ConfigMap. The install namespace is `openshift-*`
(platform-reserved) and carries `openshift.io/cluster-monitoring: "true"`, so
**cluster (platform) Prometheus** scrapes it; user-workload monitoring never
scrapes `openshift-*` namespaces. The OLM bundle and non-OLM `make deploy` ship
the same ServiceMonitor / PrometheusRule. The dashboard ConfigMap is not a
manifest: the operator writes it from an embedded asset at reconcile time.

The install also ships a `prometheus-k8s` Role/RoleBinding granting the
platform Prometheus ServiceAccount (`openshift-monitoring/prometheus-k8s`)
read on services/endpoints/pods in the namespace. The cluster-monitoring
label adds the namespace to Prometheus's selector, but the monitoring operator
only auto-grants this service-discovery RBAC to namespaces it manages; without
it, discovery finds zero targets and nothing is scraped (`ComplianceStatusStale`).

## Metrics

| Metric | Meaning |
|---|---|
| `baseline_security_compliance_score` | Overall score 0-100 (`-1` when unavailable). Flat: pass/(pass+fail); SeverityWeighted: severity-weighted ratio. |
| `baseline_security_checks` | Check-result count by `profile` and `status` label. `profile` is a built-in ProfileKey or `tp:<name>` for a TailoredProfile. `status` is `pass`, `fail`, `manual`, `info`, `error`, `inconsistent`, `waived`, or `notApplicable` (camelCase matches `status.*.notApplicable`). |
| `baseline_security_condition` | Condition, 1 True / 0 False-or-absent. Label `type` = Available\|Progressing\|Degraded (rollups; `ClusterBaselineDegraded` reads Degraded) or ComplianceOperatorReady\|ScanConfigured\|ScanStorageReady\|ConsolePluginReady (detail). ImageMissing / CRDsMissing never Degrade, so the detail series are how Prometheus sees plugin and scan-CRD readiness. |
| `baseline_security_last_scan_timestamp_seconds` | Unix time of the last completed scan; 0 when never scanned or scanning disabled. |
| `baseline_security_scan_interval_seconds` | Approx seconds between scans for `spec.schedule` (drives the stale-scan alert threshold); 0 when disabled/invalid. |
| `baseline_security_newly_failed` | Checks newly failed since the previous completed scan (`len(status.newlyFailed)`). |
| `baseline_security_status_observed_timestamp_seconds` | When this replica last published status metrics (HA scrape selection). |
| `baseline_security_remediation_batch_active` | 1 while a remediation batch is in progress (MCPs may be paused). |
| `baseline_security_remediation_batch_started_timestamp_seconds` | When the active batch started (batch-age alerting); 0 when none. |

The same endpoint also serves the controller-runtime series for the reconciler
itself, which the dashboard's Reconcile-loop row reads:
`controller_runtime_reconcile_total` (by `result`), `controller_runtime_reconcile_errors_total`,
and the `controller_runtime_reconcile_time_seconds` histogram. Only the leader
reconciles, so these are zero on a standby replica.

## Dashboards

The `Baseline Security / Compliance` ConfigMap dashboard (Observe → Dashboards,
`openshift-config-managed/baseline-security-compliance-dashboard`) has five
rows to look at, in the order an incident usually needs them:

1. **Score** row: current score, Degraded flag, remediation batch state and age.
2. **Score trend** row: 30-day score history, the regression check after a change.
3. **Checks by status** row: totals per status, failing checks per profile.
4. **Operator health** row: rollup and detail conditions, metric freshness, last
   scan age, regressions, ERROR and INCONSISTENT counts.
5. **Reconcile loop** row: reconcile error rate against the total reconcile rate,
   and p50/p99 reconcile duration. A duration climbing toward the 5m
   `reconcileTimeout` bound means the loop is about to start failing reconciles;
   the error-rate series says whether it already has.

`operator/internal/controller/dashboard_test.go` pins the panel grid and asserts
every panel query names a metric in the table above, so a renamed or removed
gauge cannot leave a blank panel behind.

## Logs

Structured JSON (zap, `--zap-encoder`) to stderr, readable from the pod logs.
Levels:

- **Error**: a reconcile step failed. Each carries the CR `name` and the
  `duration` of the whole reconcile so far. The top-level reconcile failure
  also carries `generation`, so that one is attributable to a spec change.
- **Info (default level)**: transitions only. A Degraded or not-Available posture
  logs once on entry (with `score`, `fail`, `error`, `inconsistent`,
  `newlyFailed`, `available`, `progressing`, `batchActive`) and the same posture
  repeats at V(1) while it persists, so a steady failure does not emit a line per
  1m poll. Setup-time configuration, watch registration, MachineConfigPool
  pause-state changes, and CR deletion are also Info.
- **V(1)**: the per-reconcile `reconciled` line and the steady-state repeats
  above. Raise verbosity with `--zap-log-level=1` for a live investigation; do
  not run the operator at that level permanently.

There is no request ID: the reconciler is a single singleton worker driven by one
cluster-scoped CR, so `name` plus `generation` and the log timestamp identify
the pass. `generation` is the pivot back to the CR: it tells you which spec
version a failure belongs to.

## Tracing

There is deliberately no OpenTelemetry tracing. The operator serves no inbound
request: its work is one periodic reconcile loop plus the compliance CRDs it
watches, and every step is already visible as a log line with a `duration` and a
`generation`. Spans would add a collector dependency and sampling configuration
without adding a signal that metrics plus logs do not already carry.

## Alerts (PrometheusRule)

| Alert | Fires when |
|---|---|
| `ComplianceScoreLow` | Score below 80 for 30m, on a real score only (the `-1` "no score" sentinel is excluded; threshold is above the console color bands, ADR-017). |
| `ComplianceChecksFailing` | Failing checks present. |
| `ComplianceChecksInError` | Checks in ERROR (scan/content problem). |
| `ComplianceChecksInconsistent` | Checks INCONSISTENT across nodes. |
| `ComplianceRegressions` | Checks newly failed since the previous scan. |
| `ComplianceStatusStale` | Status metrics not published recently (operator wedged/down). |
| `ComplianceScanStale` | Last scan older than 1.5x the configured scan interval. |
| `RemediationBatchStuck` | A remediation batch has not cleared past its grace window (MCPs may stay paused). |
| `ClusterBaselineDegraded` | The ClusterBaseline `Degraded` condition is True. |
| `ClusterBaselineNotAvailable` | `Available=False` for 1h while `Progressing=False`: an admin-owned steady state (Compliance Operator not installed under `installComplianceOperator=Manual`, compliance CRDs absent, console plugin image unset) that no other alert covers because those states never set `Degraded`. |

Authoritative definitions: `operator/internal/controller/metrics.go` and
`operator/config/prometheus/prometheusrule.yaml`.
