// Package controller reconciles ClusterBaseline: Compliance Operator install,
// scan bindings, score aggregation (including waivers and scan diff), console
// plugin deploy, and MachineConfigPool-paused remediation batching.
//
// Compliance Operator CRDs may be absent at startup. SetupWithManager registers
// a lazy dynamic informer for suite/scan/remediation/check-result events once
// those CRDs exist; Reconcile still requeues periodically as a fallback.
//
// Files are split by concern (same package, no import cycles):
//   - clusterbaseline_controller.go: Reconcile loop, owned resources, watches
//   - matching.go: suite/binding names, profile matching, small pure helpers
//   - tailored.go: TailoredProfile suite/binding name helpers
//   - scanstorage.go: Pending PVC / StorageClass readiness condition
//   - scoring.go: pass/fail and severity-weighted score math
//   - history.go: score history rings, failure-diff, severity helpers
//   - conditions.go: status condition helpers and Available/Progressing/Degraded rollups
//   - inconsistent.go: benign INCONSISTENT collapse for multi-node checks
//   - batch.go: remediation-batch annotation constants and grace timer
//   - schedule.go: cron normalization and next-scan time
//   - compliance_version.go: OLM CSV version comparison for CO selection
//   - metrics.go: Prometheus gauges published after rollup (score, checks, conditions)
package controller
