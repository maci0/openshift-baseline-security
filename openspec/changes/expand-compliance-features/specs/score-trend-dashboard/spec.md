## ADDED Requirements

### Requirement: Shipped score-trend visualization

The addon SHALL ship a trend visualization of the compliance score over time,
driven by the `baseline_security_compliance_score` metric, so operators can watch
posture drift without building their own dashboard.

#### Scenario: Native console dashboard provided
- **WHEN** user-workload monitoring and the metrics ServiceMonitor are in place
  and the operator opens Observe -> Dashboards -> "Baseline Security / Compliance"
- **THEN** the console renders the shipped dashboard (a ConfigMap labeled
  `console.openshift.io/dashboard` in `openshift-config-managed`) showing the
  compliance score, its 30-day trend, and per-profile check counts, with no
  Grafana process involved

#### Scenario: Console trend card
- **WHEN** the operator prefers the console
- **THEN** a trend of the score is available in-console (extending the existing
  status history chart) without leaving the compliance UI
