## ADDED Requirements

### Requirement: Edit the scan schedule from the console

The console SHALL let an admin edit the scan schedule (cron) that drives the owned
ScanSetting, with validation, gated on ClusterBaseline patch permission.

#### Scenario: Valid schedule applied
- **WHEN** an admin sets a valid cron schedule and saves
- **THEN** `spec.schedule` is patched, the next reconcile updates the ScanSetting
  and `status.nextScanTime`

#### Scenario: Invalid schedule rejected in UI
- **WHEN** an admin enters an invalid cron
- **THEN** the UI blocks save with a validation message and does not patch

### Requirement: Per-profile score history

The operator SHALL record score history per profile (built-in and tailored), not
only globally, so each benchmark can be trended.

#### Scenario: Per-profile trend rendered
- **WHEN** several scans have completed with a profile enabled
- **THEN** that profile's score card can show its own trend line, and the global
  trend remains available
