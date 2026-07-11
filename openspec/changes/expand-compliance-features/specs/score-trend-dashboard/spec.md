## ADDED Requirements

### Requirement: Shipped score-trend visualization

The addon SHALL ship a trend visualization of the compliance score over time,
driven by the `baseline_security_compliance_score` metric, so operators can watch
posture drift without building their own dashboard.

#### Scenario: Grafana dashboard provided
- **WHEN** an operator imports the shipped dashboard JSON into a Grafana with the
  cluster metrics datasource
- **THEN** it shows the compliance score over time and per-profile check counts

#### Scenario: Console trend card
- **WHEN** the operator prefers the console
- **THEN** a trend of the score is available in-console (extending the existing
  status history chart) without leaving the compliance UI
