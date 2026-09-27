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

The install also ships a default-deny ingress `NetworkPolicy`
(`config/manager/networkpolicy.yaml`) on the operator pods: port 8443 is
reachable only from `openshift-monitoring` and the service-ca namespaces,
every other port is denied everywhere, and egress is unrestricted. A scraper
outside those namespaces gets a refused connection and no error on the
operator side; add the namespace to the policy's `from` selector to admit it.

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
| `baseline_security_remediation_batches_total` | Finished remediation batches by `outcome`: `applied`, `cancelled`, `grace` (pools unpaused by `batchResumeGrace`, or with a listed remediation that could not be observed, so Applied was never confirmed), `orphaned` (crash/cancel recovery unpaused pools with no batch status). |

The same endpoint also serves the controller-runtime series for the reconciler
itself, which the dashboard's Reconcile-loop row reads:
`controller_runtime_reconcile_total` (by `result`), `controller_runtime_reconcile_errors_total`,
and the `controller_runtime_reconcile_time_seconds` histogram. Only the leader
reconciles, so these are zero on a standby replica.

## Dashboards

The `Baseline Security / Compliance` ConfigMap dashboard (Observe → Dashboards,
`openshift-config-managed/baseline-security-compliance-dashboard`) has five
rows to look at, in the order an incident usually needs them:

1. **Score** row: current score, Degraded flag, remediation batch state and age,
   and how many batches in the last 24h were unpaused before their
   remediations applied.
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

Panels are colored from one status palette, read out of
`@patternfly/react-tokens` rather than chosen: the values below are what the
PatternFly 6 light-theme tokens the console plugin reads live resolve to, so a
status is one color in the console Overview, the exported HTML report, and this
dashboard. Icon status success `#3d7317`, danger `#b1380b`, info `#5e40be`,
custom `#147878`, the disabled neutral `#a3a3a3` for not-applicable and the age
gauges, and two nonstatus tints that keep a status from being read as its
neighbor: orangered `#fbbea8` for Error, apart from Fail, and teal `#b9e5e5` for
Waived, apart from Not applicable. Manual is the one series that does not take
its icon token: a chart series is a filled area, so it takes the text-status
amber `#73480b` rather than the icon-token amber the donut wedge uses. The
middle step of a three-step threshold scale is the warning-200 tint `#dca614`,
which is a different job from the Manual series and is listed in the guard
separately. The report's score and severity type use the text status tokens
(`#b1380b`, `#73480b`, `#204d00`) for the same reason, colored type wants the
text family. A graph panel with no `colors` falls back to Grafana's default
categorical palette, where a failing series can render green, so
`TestDashboardUsesStatusPalette` fails the build on a color outside the palette
or an uncolored graph panel. The score singlestat and the 30-day trend share the
60/90 bands (`TestDashboardScoreBandsShared`), so the trend is never a second
verdict.

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

The console plugin has no server-side log surface: it is a static bundle served
by nginx and rendered in the browser tab, so its only sink is the browser
console. A failed `ComplianceCheckResult` watch is already an on-page Alert; a
throw *while rendering* is caught by `TabErrorBoundary` on every tab route and
on the page shell, which names the view, reports the reason and the error object
to the browser console, and offers Retry. So a blank Compliance page is either a
watch failure named in the banner or a render throw with a line in the browser
console; a blank page with neither is a plugin bundle that failed to load at
all, which is the nginx access log.

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
| `RemediationBatchGraceResume` | A batch ended by the resume grace window or by crash/cancel recovery: the pools came back before every remediation reported Applied. The batch status is cleared, so nothing else in the cluster records it. |
| `ClusterBaselineDegraded` | The ClusterBaseline `Degraded` condition is True. |
| `ClusterBaselineNotAvailable` | `Available=False` for 1h while `Progressing=False`: an admin-owned steady state (Compliance Operator not installed under `installComplianceOperator=Manual`, compliance CRDs absent, console plugin image unset) that no other alert covers because those states never set `Degraded`. |

Authoritative definitions: `operator/internal/controller/metrics.go` and
`operator/config/prometheus/prometheusrule.yaml`.
