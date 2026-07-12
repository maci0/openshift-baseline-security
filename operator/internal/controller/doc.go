// Package controller reconciles ClusterBaseline: Compliance Operator install,
// scan bindings, score aggregation (including waivers and scan diff), console
// plugin deploy, and MachineConfigPool-paused remediation batching.
//
// Compliance Operator CRDs may be absent at startup. SetupWithManager registers
// a lazy dynamic informer for suite/scan/remediation/check-result events once
// those CRDs exist; Reconcile still requeues as a fallback (1m steady, 15s while
// Progressing or a remediation batch is Applying; also shortens toward the
// soonest active waiver expiresAt, floored at 1s).
//
// Files are split by concern (same package, no import cycles):
//   - clusterbaseline_controller.go: Reconcile loop, reconcileOwned, SetupWithManager
//   - helpers.go: requeue cadence, createIfMissing, relatedObjectsFromSuites
//   - unstructured.go: unstructured object/list helpers and metadata field readers
//   - compliance_operator.go: CO Subscription/OperatorGroup/CSV readiness
//   - scanconfig.go: ScanSetting + per-profile/tailored ScanSettingBindings
//   - scanstorage.go: Pending PVC / StorageClass readiness condition
//   - aggregate.go: check-result scoring, counts, profile status
//   - history_reconcile.go: suite-completion history advance and scan-diff base
//   - history.go: score history rings, failure-diff, per-profile ring sync
//   - scoring.go: pass/fail and severity-weighted score math, severity lookup
//   - conditions.go: status condition helpers and Available/Progressing/Degraded rollups
//   - inconsistent.go: benign INCONSISTENT collapse for multi-node checks
//   - matching.go: suite/binding names (built-in + tailored), profile matching, pure set/list helpers
//   - batch.go: remediation-batch annotations, pool/name helpers, grace timer
//   - batch_reconcile.go: MCP pause/resume, batch metadata, orphan recovery
//   - batch_apply.go: applyRemediationBatch state machine
//   - plugin.go: ConsolePlugin CR, Deployment/Service, registration, image ref checks
//   - plugin_pod.go: plugin pod template, affinity, Deployment availability helpers
//   - dashboard.go: Observe -> Dashboards ConfigMap (embedded JSON in assets/)
//   - schedule.go: cron normalization and next-scan time
//   - compliance_version.go: OLM CSV version comparison for CO selection
//   - metrics.go: Prometheus gauges after rollup (score, checks, observed
//     timestamp, last scan, newly failed, remediation batch active/started,
//     condition rollups)
package controller
