# Test plan

Catalog of e2e and edge cases for the operator + console plugin. Status:
`[x]` covered by an existing test, `[ ]` gap. Existing unit/fuzz tests live in
`operator/internal/controller/*_test.go` and `console-plugin/src/*.test.ts`;
live e2e in `operator/test/e2e/` (Go, build tag `e2e`) and
`console-plugin/e2e/` (Playwright).

## A. Score aggregation semantics

The score is a single pooled ratio `ΣPASS*100 / (ΣPASS+ΣFAIL)` over all owned
results (built-in + tailored). These pin the behavior so a refactor can't
silently turn it into a mean.

- [x] Single profile: pass/fail ratio correct (`TestScore`, `FuzzScore`).
- [x] Multi-group pooling: built-in + tailored counted together
      (`TestAggregateStatusWithTailored`, jest `aggregateCounts` composition).
- [ ] **Two built-in benchmarks pooled**: enable CIS + STIG, assert
      `Status.Score` equals combined `PASS/(PASS+FAIL)`, not the mean of the
      two per-profile scores. (The exact question that prompted this plan.)
- [ ] **Large-benchmark dominance**: a profile with many checks outweighs a
      small one in the pooled score; assert per-profile cards still show each
      profile's own ratio independently.
- [x] MANUAL/ERROR/NOT-APPLICABLE excluded from denominator (`TestScore`).
- [ ] **All-MANUAL scan**: `pass+fail==0` → `Status.Score==nil`, but
      `LastScanTime` is still set and the Overview item shows "Not scanned".
- [ ] **Zero owned results** (scans exist but none match a baseline suite) →
      score nil, no panic.
- [x] Stale score cleared when CRDs vanish (`TestAggregateStatusClearsStaleScore`,
      `TestReconcileWithoutComplianceCRDs`).

## B. Profile lifecycle (multi-benchmark)

- [x] Toggle one profile on/off updates bindings (`TestProfileToggle` e2e,
      `TestEnsureScanConfigCreatesAndPrunes`, jest `toggledProfiles`).
- [ ] **Enable all eight built-ins at once**: 8 ScanSettingBindings created,
      8 profiles appear in `Status.Profiles`, aggregate score spans all.
- [ ] **Disable a profile mid-scan**: its binding/suite is pruned, its results
      drop out of the aggregate and out of `Status.Profiles` on next reconcile.
- [x] Cannot remove the last profile (jest `toggledProfiles` rejects empty).
- [ ] **Rapid toggle churn**: add+remove same profile within one reconcile
      window leaves no orphaned bindings.
- [ ] **Unknown/invalid profile key** in spec rejected by CRD enum validation
      (apply a bad key, expect admission error).

## C. Tailored profiles

- [x] Owned tailored suite recognized (`TestTailoredSuiteHelpers`, jest
      `suiteTailoredName` / `isOwnedByBaseline` tailored).
- [x] Tailored PVC counted in scan storage (`TestCheckScanStorageTailoredPVC`).
- [ ] **Tailored + built-in of the same base** (e.g. `cis` and a `cis-custom`
      TailoredProfile) both bound, both scored, not mutually clobbered.
- [ ] **TailoredProfile CRD absent** (older Compliance Operator): spec entry is
      ignored gracefully, condition explains why, no binding created.
- [ ] **Tailored name collision** with a built-in suite label → ownership
      routes to tailored first (already the code order; add a regression test).
- [ ] Deep-link + filter labels strip the `tp-` prefix (Playwright already
      screenshots this; add an assertion on the filter chip text).

## D. Operator install lifecycle

- [x] Auto-install creates Subscription (`TestEnsureComplianceOperatorCreatesSubscription`).
- [x] Manual mode still detects an existing operator
      (`TestEnsureComplianceOperatorManualStillChecksExisting`).
- [x] Opt-out path (`TestEnsureComplianceOperatorOptOut`).
- [x] Adopt a pre-existing CSV / already-installed operator
      (`TestEnsureComplianceOperatorAlreadyInstalled`,
      `TestEnsureComplianceOperatorAdoptsExistingCSV`).
- [ ] **CSV present but not yet Succeeded** (Installing/Pending): condition
      Progressing, version empty, no scan config attempted yet.
- [ ] **Two CSV versions present** (upgrade in flight): newest Succeeded wins,
      else newest overall (matches `findComplianceOperatorCSV` fallback).
- [ ] **Subscription exists but CSV never appears** (stuck install): surfaces a
      bounded Progressing/Degraded, no infinite fast requeue.

## E. Scan storage & failure modes

- [x] Pending PVC → Degraded (`TestCheckScanStorageDegradedOnPendingPVC`).
- [x] Empty namespace tolerated (`TestCheckScanStorageEmptyNamespace`).
- [ ] **No default StorageClass**: scans hang; operator reports Degraded with a
      clear reason (README claims this; assert it).
- [ ] **PVC bound but scan pod OOM/Error**: ComplianceScan `ERROR` phase
      reflected in status, not silently counted as pass.
- [ ] **Scan in progress**: Progressing condition true; plugin shows the
      "scan in progress" empty state / skeletons (jest/Playwright).

## F. Console plugin states & RBAC

- [x] Overview renders score + profile breakdown (Playwright).
- [x] Results table lists + filters (Playwright, jest `resultsHref`).
- [x] Reachable under Administration nav (Playwright).
- [ ] **No ClusterBaseline yet**: Overview item shows "Not scanned", page shows
      an empty/onboarding state, no crash (Playwright + jest on
      `ClusterScoreItem` loaded/empty branch).
- [ ] **RBAC read-only user**: profile toggles + apply buttons disabled
      (`useAccessReview` false path); assert disabled state in Playwright.
- [ ] **List error / RBAC denied on ClusterBaseline**: Overview item falls back
      to "—" (the `error` branch), no thrown error.
- [ ] **Console capability disabled**: plugin deregisters cleanly
      (`TestEnsureConsolePluginDisabled`, `TestConsoleTeardownToleratesMissingCRDs`
      cover operator side; no UI to assert).

## G. Remediations

- [x] Owned remediations filtered (jest `isOwnedByBaseline`), node remediation
      detected (`isNodeRemediation`), rendered-object text (`remediationObjectText`).
- [x] Apply/auto-apply patch shape uses `add` (jest `remediationApplyPatch`).
- [ ] **Apply a node remediation**: confirmation modal warns about reboots;
      MachineConfigPool-pause guidance shown (Playwright screenshot exists,
      add assertion).
- [ ] **Outdated / MissingDependencies state** rendered with the right Label
      color (jest on `stateColor` mapping).
- [ ] **Auto-apply toggle** writes `spec.remediation.apply` and survives a
      round-trip when the field was server-defaulted-absent (the `add`-patch fix).

## H. Overview dashboard item (cluster Overview)

- [x] Renders `<n> / 100` deep-link when scored (Playwright `dashboard-score`).
- [ ] **Score color thresholds**: >=90 green, >=60 orange, else red
      (jest on `scoreColor` + a render test of `ClusterScoreItem`).
- [ ] **Loading / error / not-scanned** branches of `ClusterScoreItem`
      (unit-test the component's three return paths).
- [ ] **Singleton selection**: picks the `cluster`-named baseline even if extra
      ClusterBaselines exist; falls back to first otherwise.

## I. Metrics & alerts

- [x] Sentinel `-1` before first score, gauge set after (`TestComplianceScoreSeededSentinel`,
      `TestPublishMetrics`).
- [ ] **`baseline_security_checks{profile,status}`** per-profile + `tp:<name>`
      series present after a multi-profile scan.
- [ ] **PrometheusRule** `ComplianceScoreLow` / `ComplianceChecksFailing` fire
      against synthetic metric values (promtool rule test, no cluster needed).

## J. Deletion & finalizer

- [x] Finalizer added + requeue (`TestReconcileAddsFinalizerAndRequeues`).
- [x] Deletion deregisters plugin + removes finalizer
      (`TestReconcileDeletionDeregistersAndRemovesFinalizer`).
- [x] Teardown tolerates missing Console/CRDs (`TestConsoleTeardownToleratesMissingCRDs`,
      `TestDeregisterConsolePluginMissingConsole`).
- [ ] **Delete with remediations applied**: MachineConfigs are NOT reverted on
      CR delete (document + assert the non-destructive contract).

## K. Malformed / adversarial input

- [x] Fuzz: suite-label round-trip, profile-key parse, score, CSV export,
      results href, history ring, profile names, `withoutPlugin`, `matchesAnyProfile`.
- [x] CSV formula-injection neutralized (jest `resultsCsv`).
- [ ] **Check result with missing/garbage `status`** string → tally ignores it,
      no miscount, no panic.
- [ ] **ComplianceCheckResult with no suite label** → not attributed to any
      profile, excluded from score.
- [ ] **Huge result set** (thousands of checks): aggregate int math doesn't
      overflow (score already int64-widened; add a boundary test at ~2^31 checks).

## Priority gaps

1. A: two-built-in pooling + all-MANUAL nil score (answers the score question).
2. F: no-ClusterBaseline + RBAC-disabled UI states.
3. H: `ClusterScoreItem` unit test (loading/error/scored/not-scanned).
4. I: promtool rule test for the two alerts (cheap, no cluster).
5. D: CSV-not-yet-Succeeded + stuck-install bounded requeue.
