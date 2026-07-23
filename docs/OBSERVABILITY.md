# Observability

The operator exports Prometheus metrics and ships PrometheusRule alerts plus a
native Observe → Dashboards ConfigMap. The install namespace is `openshift-*`
(platform-reserved) and carries `openshift.io/cluster-monitoring: "true"`, so
**cluster (platform) Prometheus** scrapes it; user-workload monitoring never
scrapes `openshift-*` namespaces. The OLM bundle and non-OLM `make deploy` ship
the same ServiceMonitor / PrometheusRule / dashboard.

## Metrics

| Metric | Meaning |
|---|---|
| `baseline_security_compliance_score` | Overall score 0-100 (`-1` when unavailable). Flat: pass/(pass+fail); SeverityWeighted: severity-weighted ratio. |
| `baseline_security_checks` | Check-result count by `profile` and `status` label. |
| `baseline_security_condition` | Rollup condition, 1 True / 0 False-or-absent. Label `type` = Available\|Progressing\|Degraded. |
| `baseline_security_last_scan_timestamp_seconds` | Unix time of the last completed scan; 0 when never scanned or scanning disabled. |
| `baseline_security_scan_interval_seconds` | Approx seconds between scans for `spec.schedule` (drives the stale-scan alert threshold); 0 when disabled/invalid. |
| `baseline_security_newly_failed` | Checks newly failed since the previous completed scan (`len(status.newlyFailed)`). |
| `baseline_security_status_observed_timestamp_seconds` | When this replica last published status metrics (HA scrape selection). |
| `baseline_security_remediation_batch_active` | 1 while a remediation batch is in progress (MCPs may be paused). |
| `baseline_security_remediation_batch_started_timestamp_seconds` | When the active batch started (batch-age alerting); 0 when none. |

## Alerts (PrometheusRule)

| Alert | Fires when |
|---|---|
| `ComplianceScoreLow` | Score below the warning threshold. |
| `ComplianceChecksFailing` | Failing checks present. |
| `ComplianceChecksInError` | Checks in ERROR (scan/content problem). |
| `ComplianceChecksInconsistent` | Checks INCONSISTENT across nodes. |
| `ComplianceRegressions` | Checks newly failed since the previous scan. |
| `ComplianceStatusStale` | Status metrics not published recently (operator wedged/down). |
| `ComplianceScanStale` | Last scan older than 1.5x the configured scan interval. |
| `RemediationBatchStuck` | A remediation batch has not cleared past its grace window (MCPs may stay paused). |
| `ClusterBaselineDegraded` | The ClusterBaseline `Degraded` condition is True. |

Authoritative definitions: `operator/internal/controller/metrics.go` and
`operator/config/prometheus/prometheusrule.yaml`.
