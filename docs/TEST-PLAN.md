# Test plan

Catalog of unit, e2e, and adversarial edge cases for the operator + console
plugin. Status: `[x]` covered by an existing test, `[ ]` gap, `[~]` partial
(asserted in spirit or by a neighboring case, but not pinned exactly).

Existing unit/fuzz tests live in `operator/internal/controller/*_test.go`,
`operator/cmd/*_test.go`, and `console-plugin/src/*.test.ts`. Live e2e lives in
`operator/test/e2e/` (Go, build tag `e2e`) and `console-plugin/e2e/` (Playwright).

When adding a case, prefer the cheapest layer that would catch a regression:
unit/fake-client first, envtest/e2e only when the failure needs a real API or
console.

## Test tiers and commands

Use these tiers when deciding what a PR, nightly, or release candidate must
run. A checklist item should name the cheapest tier that can prove it.

| Tier | Purpose | Command / harness | Gate |
|---|---|---|---|
| 0 | Fast local correctness | `operator: make lint test`; `console-plugin: yarn lint && yarn typecheck && yarn test --runInBand`; repo: `git diff --check` | Every PR |
| 1 | Generated/build artifacts | `operator: make build && make bundle`; repo: `kubectl kustomize operator/config/default`; `console-plugin: yarn build` | Every PR touching manifests, packaging, or frontend |
| 2 | Hardening | `operator: go test -race ./... && make fuzz` | Nightly and before release |
| 3 | API admission | envtest or server-side dry-run against generated CRDs | PRs touching API markers/CRD schema |
| 4 | Live OpenShift | Go e2e with `KUBECONFIG`; Playwright with `CONSOLE_URL`, `KUBEADMIN_PASSWORD`, and seeded Compliance Operator data | Release candidates |
| 5 | Release / supply chain | image smoke, bundle install/upgrade, vulnerability scan, SBOM/provenance validation | Release candidates |

Rules:

- `[x]` means a named automated test exists, or the item explicitly says
  "verified live" with the topology/date in the bullet.
- `[~]` means adjacent coverage exists but the exact behavior can still regress.
- Screenshots prove rendering only after they include hard Playwright
  assertions; otherwise they are artifacts, not coverage.
- Do not promote a live-only manual verification to `[x]` unless the exact
  command and expected output are recorded in the bullet or the e2e test.

## Run ledger

Where "was this run, and when" lives. Do **not** add a per-item last-run column
to the hundreds of checkboxes below: automated tiers are re-run every PR, so
their real last-run is the latest CI job, and a hand-maintained column would rot
within a day. Track it at two useful granularities instead.

### Tier last-run summary

Update the row for a tier when you run it. Automated tiers point at CI rather
than a copied date.

| Tier | How it's tracked | Last run | Result |
|---|---|---|---|
| 0 Fast local correctness | CI `ci.yml`, every PR/push | see latest Actions run on `main` | gating |
| 1 Generated/build artifacts | CI `ci.yml` (`make bundle`, `yarn build`) | see latest Actions run | gating |
| 2 Hardening (`-race`, fuzz) | CI `ci.yml` job `fuzz` on schedule + workflow_dispatch; seeds also run under `make test` | see latest scheduled Actions run | gating (nightly) |
| 3 API admission (envtest) | manual (not yet automated) | envtest harness not yet run; live informer/batch rows below reuse this label | — |
| 4 Live OpenShift (Go e2e + Playwright) | manual, logged below | 2026-07-11 | pass |
| 5 Release / supply chain | manual (pre-release) | not yet run | — |

### Live verification log (Tiers 4–5)

Append one row per live run; never edit past rows (it is history, so it does not
rot). Record topology, versions, and the concrete result.

| Date | Tier | Scope | Cluster topology | OCP / CO | Result |
|---|---|---|---|---|---|
| 2026-07-09 | 4 | First zero-config CIS scan; Overview donut, Results, Remediations, nav placement | SNO (1 master+worker) | 4.22 / CO 1.9.1 | pass — score ~95, Available=True, Degraded=False |
| 2026-07-10 | 4 | Multi-node + multi-benchmark: CIS+PCI-DSS on 3 nodes; INCONSISTENT surfaced; dashboard item; donut legend | SNO + 2 day-2 workers | 4.22 / CO 1.9.1 | pass — score 94 pooled, 86 INCONSISTENT counted, node scan fans out 3 pods |
| 2026-07-10 | 1 | OLM bundle validation | n/a (local) | operator-sdk bundle validate | pass — "All validation tests completed successfully" |
| 2026-07-10 | 4 | Full automated e2e after round-3 deploy: Go 11 tests (incl. score-vs-live-results ground truth, per-profile+INCONSISTENT match, nextScanTime future, node-scan fan-out, tailored scored) + Playwright 20 (incl. Inconsistent slice, PCI-DSS cards, dashboard link, INCONSISTENT filter, CSV download) | SNO + 2 day-2 workers | 4.22 / CO 1.9.1 | pass — Go 11/11, Playwright 20/20; score 94 = 514 PASS/(514+31 FAIL) verified against live CheckResults |
| 2026-07-10 | 2+4 | Waivers + INCONSISTENT drill-down round. Battery: Go e2e 12 (added live waiver round-trip: waive a FAIL → moves to Waived bucket, out of denominator) + Playwright 22 (added per-node drill-down table, Waive action) + promtool alerts + operator -race + all 11 fuzz targets + bundle validate | SNO + 2 day-2 workers | 4.22 / CO 1.9.1 | pass — Go 12/12, Playwright 22/22, jest 68, promtool SUCCESS, fuzz 11/11; live waive dropped cis.fail 7→6, waived 0→1 |
| 2026-07-10 | 4 | Lifecycle scenarios after waiver-self-healing + MCP-drill-down deploy: invalid-schedule Degrade+recover, schedule→NextScanTime advance, console Removed→teardown+deregister→Managed→redeploy. Full battery re-run | SNO + 2 day-2 workers | 4.22 / CO 1.9.1 | pass — Go 15/15, Playwright 22/22, jest 72, -race clean |
| 2026-07-10 | 4 | Deploy 0.3.0 (r6: stuck-install grace, errorMessage guard) + full e2e re-run | SNO + 2 day-2 workers | 4.22 / CO 1.9.1 | pass — Go 15/15, Playwright 22/22; Available=True, score 94, cluster self-cleaned |
| 2026-07-11 | 4 | 0.3.1: per-profile cards now show Inconsistent (was donut-only); light+dark screenshots; dark-theme spec | SNO + 2 day-2 workers | 4.22 / CO 1.9.1 | pass — Go e2e 16/16, Playwright 26/26 (incl. dark theme + per-profile Inconsistent), jest 78 |
| 2026-07-11 | 4 | 0.4.0 compliance-features expansion (waiver governance, regressions, guided remediation/MCP batch, tailored authoring, schedule editor, severity-weighted score, Helm, report, console dashboard, hardening profile). Full battery | SNO + 2 day-2 workers | 4.22 / CO 1.9.1 | pass — operator unit+race+fuzz(15)+promtool, jest 90, Go e2e 16/16, Playwright 26/26, bundle validate, helm lint |
| 2026-07-11 | 4 | Operator-managed console dashboard (Observe -> Dashboards, no Grafana; verified live with UWM: score singlestat + trend + checks), metrics ServiceMonitor/PrometheusRule in the bundle, benign-INCONSISTENT collapse, Helm dropped. Rebuilt operator + plugin (r10), redeployed | SNO + 2 day-2 workers | 4.22 / CO 1.9.1 | pass — Go e2e 16/16, Playwright 26/26, jest 95; INCONSISTENT 86->3 (only genuine PASS/FAIL splits kept), score 94->95, dashboard renders live |
| 2026-07-12 | 3 | Dynamic informer (openspec 6.2/6.3): lazy watches on ComplianceScan/Remediation/CheckResult added once CRDs register (RESTMapper probe, 30s retry), events reconcile the singleton (coalesced), poll kept as fallback. Operator r16 | SNO + 2 day-2 workers | 4.22 / CO 1.9.1 | pass — leader logged all 3 watches established; Go e2e green; operator unit incl. enqueue mapping; Available=True, score 95 |
| 2026-07-12 | 3 | Batch remediation (openspec 3.4): fixed poolFromRemediation (scan-name fallback; the MC role label is often empty so the batch paused no pool), added cancel-resume (all remediations reverted to apply=false resumes at once), live Go e2e for pause/resume (cancel path, non-control-plane pool, skips otherwise). Operator r15 | SNO + 2 day-2 workers | 4.22 / CO 1.9.1 | pass — Go e2e 16 pass + 1 safe skip, operator unit incl. pool-fallback + cancel-resume; score 95 |
| 2026-07-11 | 4 | Phase 2 close-out: regressions "Recent changes" card on Overview (newly-failing + fixed since last scan, deep-linked per check, empty state distinguishes no-change vs no-prior-scan). Plugin r14 | SNO + 2 day-2 workers | 4.22 / CO 1.9.1 | pass — Playwright 27/27, jest 105; card + empty state render live |
| 2026-07-11 | 4 | UI bug/eyesore pass (plugin r11): Results Profile column to disambiguate repeated check titles, remediation batch-error feedback + empty state + centered spinner, tailored-profile DNS-1123 name validation + orphan-safe create, distinct donut colors (Error/Waived), time-scaled score-trend axis (deduped dates), expiring-waiver alert now links to waived checks, missing i18n keys added. Cluster cleaned (build clutter + stale image tags pruned) | SNO + 2 day-2 workers | 4.22 / CO 1.9.1 | pass — Playwright 27/27, jest 100, typecheck clean; Profile column + fixes verified live on r11 |

## Fixture and harness strategy

Prefer small deterministic fixtures over ad hoc live-cluster assertions.

| Need | Preferred harness | Fixture notes |
|---|---|---|
| Controller pure logic | Go unit test | table-driven helpers in `operator/internal/controller/*_test.go` |
| Kubernetes CRUD / owner refs / NoKindMatch | controller-runtime fake client with interceptors | use unstructured builders (`u`, `uList`, `checkResult`) and inject NoKindMatch via interceptors |
| CRD admission/defaulting/CEL | envtest or API server dry-run | install generated CRD from `operator/config/crd/bases` before applying samples |
| Console helper logic | Jest | keep untrusted strings and missing-field cases in `utils.test.ts` / `models.test.ts` |
| Console component branches | Jest + Testing Library/jsdom, or Playwright when SDK hooks are hard to mock | assert disabled states, alert text, modal focus, and download side effects |
| Real OLM / Console / Compliance Operator behavior | OpenShift e2e | record cluster topology (SNO, compact, multi-worker), OCP version, CO CSV version, and profile content image |
| Prometheus rules | promtool | synthetic series for score sentinel, low score, fail counts, and HA duplicate pods |

## Goal-to-coverage traceability

Each product goal should have at least one cheap regression test and one
release-candidate confidence check. If a goal lacks both, it is not release
ready even if nearby unit tests pass.

| Product promise | Primary sections | Must-have automated evidence |
|---|---|---|
| Zero-config onboarding creates useful scans | B, D, O, X, AU | default CR creation, Subscription/ScanSettingBinding creation, install-guide smoke, kill-switch matrix |
| Compliance score is trustworthy | A, A2, K, R, S, AT, AW, AF | pooled score tests, boundaries table, goldens, ownership exclusivity, history tests |
| Console is safe for read-only admins | F, G, H, Q, U, AE, AO | RBAC disabled states, dark-UX banners, keyboard/modal checks |
| Remediation UX is explicit about blast radius | G, U, AE, AZ | node-remediation warning, unapply/no-warning path, forced confirm |
| Operator behaves like an OpenShift component | D, J, M, O, R, T, AR, AN | condition matrix, negative-space suite, multi-admin races, finalizer cleanup |
| Release artifacts install and upgrade cleanly | L, N, P, W, AJ, AQ | CRD drift/admission, OLM upgrade ladder, disaster restore notes |
| Admins can troubleshoot failures | E, I, T, X, AS, AA | PVC degraded path, promtool rules, log/audit quality, alert boundaries |
| Content bumps do not invent health | AP, V, AY | missing Profile, prune/re-add, unknown status strings |

## Test evidence contract

Every new `[x]` entry should include enough detail that another maintainer can
find and trust the coverage.

- Name the exact test, command, or live verification note in parentheses.
- Say what input makes the test adversarial; avoid only testing happy-path
  object construction.
- Assert observable behavior, not implementation details, unless the
  implementation detail is the contract (for example, JSON Patch shape).
- For fake-client tests, include at least one foreign object when ownership is
  part of the contract.
- For UI tests, assert user-visible text/state and one negative condition
  (for example, "foreign remediation absent" or "button disabled").
- For live tests, record topology, OCP version, Compliance Operator CSV, and
  whether ACS/ACM/other console plugins were installed.
- For performance tests, record data size, time budget, hardware/cluster shape,
  and the failure threshold.

## Fixture inventory to build next

These reusable fixtures keep future tests smaller and reduce copy/paste bugs.

- [ ] `testCSV(name, namespace, phase)` with status phase and creation labels.
- [ ] `testSubscription(installedCSV)` with optional empty/missing status.
- [ ] `testClusterBaseline(spec options...)` with defaults matching the CRD.
- [ ] `testCheckResult(name, suite, status, fields...)` for all known CO status
      values plus unknown strings.
- [ ] `testComplianceScan(name, suite, phase, endTimestamp)` including malformed
      and future timestamps.
- [ ] `testScanSettingBinding(name, controlledByBaseline)` for owned/foreign
      pruning cases.
- [ ] Console fixture factories for baseline status, check results,
      remediations, and SDK watch errors.
- [ ] Prometheus fixture file with sentinel, low score, high score, fail count,
      and duplicate-HA-pod time series.

---

## A. Score aggregation semantics

The score is a single pooled ratio `ΣPASS*100 / (ΣPASS+ΣFAIL)` over all owned
results (built-in + tailored). These pin the behavior so a refactor cannot
silently turn it into a mean of per-profile scores.

- [x] Single profile: pass/fail ratio correct (`TestScore`, `FuzzScore`).
- [x] Multi-group pooling: built-in + tailored counted together
      (`TestAggregateStatusWithTailored`, jest `aggregateCounts` composition).
- [x] **Two built-in benchmarks pooled**: enable CIS + STIG (or cis+pci-dss),
      assert `Status.Score` equals combined `PASS/(PASS+FAIL)`, not the mean of
      the two per-profile scores (`TestAggregateStatusPoolsMultipleBenchmarks`).
- [x] **Large-benchmark dominance**: a profile with many checks outweighs a
      small one in the pooled score; per-profile counts stay independent
      (`TestAggregateStatusLargeBenchmarkDominance`).
- [x] MANUAL/ERROR/NOT-APPLICABLE/INFO/INCONSISTENT excluded from denominator
      (`TestScore`, `TestAggregateStatus`, `TestAggregateStatusWithTailored`).
- [x] **SKIP folded into NotApplicable** and excluded from score
      (`TestAggregateStatusWithTailored`).
- [x] **All-MANUAL scan**: `pass+fail==0` → `Status.Score==nil`, with
      per-profile counts preserved (`TestAggregateStatusAllManualNilScore`).
- [x] **Zero owned results** (no matching suite labels) → score nil, no panic
      (`TestRecordHistoryNoOwnedScans`, empty aggregate paths).
- [x] Stale score cleared when CRDs vanish (`TestAggregateStatusClearsStaleScore`,
      `TestReconcileWithoutComplianceCRDs`).
- [x] **int64 score math**: adversarial huge pass/fail counts do not overflow
      into a false nil score (`FuzzScore` with int32-max seeds).
- [x] **INFO-only profile**: score nil, Info count preserved
      (`TestAggregateStatusInfoOnlyNilScore`). Overview donut / Results filter
      still live-only.
- [x] **ERROR-only profile**: score nil, Error count preserved, no false "0 / 100"
      (`TestAggregateStatusErrorOnlyNilScore`).
- [x] **Single FAIL among thousands of PASS**: score floors (not rounds)
      (`TestScore` score(999,1)=99; jest `flatProfileScore(999, 1)`).
- [x] **Per-profile card vs global score mismatch is intentional**: large failing
      profile dominates pool while a small perfect profile stays 100%
      (`TestAggregateStatusLargeBenchmarkDominance`).

## A2. Multi-node behavior (>1 node in a MachineConfigPool)

Only visible with worker nodes joined. Node scans consolidate one
ComplianceCheckResult per rule across all nodes in the pool; the operator emits
status INCONSISTENT (with a `compliance.openshift.io/inconsistent-source`
per-node annotation) when nodes disagree.

- [x] Node scan fans out one pod per node in the pool (verified live: 3 pods
      for `ocp4-cis-node-worker` once 2 workers joined).
- [x] Results are consolidated per rule, not multiplied per node (94 unique =
      94 total for the worker node scan).
- [x] **INCONSISTENT counted, not silently dropped** — the original tally had no
      case, hiding checks from the rollup (`TestAggregateStatus` asserts the
      Inconsistent bucket; donut has an Inconsistent slice).
- [x] **Inconsistent excluded from the score denominator** (like Manual)
      (`TestAggregateStatus`).
- [x] **Per-node drill-down**: the Results detail modal parses the
      `inconsistent-source` annotation into a node/status table plus the
      most-common status (jest `inconsistentSources` incl. fuzz; Playwright
      asserts the "Per-node results" table names a node).
- [x] **MachineConfigPool surfaced**: node scans run per-MCP; the drill-down
      shows the pool (parsed from the `<profile>-node-<pool>` scan-name) with a
      deep-link (jest `nodeScanPool`, `machineConfigPoolHref`, incl. fuzz).
- [x] **Node scan fan-out verified live** across 3 worker nodes
      (`TestNodeScanCoversAllNodes`, e2e).
- [ ] **All-nodes-agree case**: same rule PASS on every node → status PASS, not
      INCONSISTENT (guards against mislabeling uniform results).
- [ ] **Master-only vs worker MCP**: control-plane-file rules are INCONSISTENT
      on a mixed master+worker node vs pure workers; pin which rules land
      inconsistent so a content bump is noticed.
- [ ] **Node added mid-cycle**: a new worker joins between scans → next scan
      includes it; score/counts reflect the larger pool without a restart.
- [ ] **Node NotReady during scan**: scan pod cannot run on it → result is
      ERROR/absent for that node, surfaced not hidden.
- [ ] **Compact 3-master (masters_schedulable)**: node scans cover 3 masters;
      no worker MCP; aggregate still correct.
- [ ] **Drain + reschedule mid-scan**: cordon a worker while the node scan is
      RUNNING; scan completes or ERROR is explicit, no zombie Progressing score.
- [ ] **Heterogeneous RHCOS versions in one pool**: content that is version-gated
      produces INCONSISTENT or NOT-APPLICABLE; neither silently drops from UI.
- [ ] **Single-node OpenShift (SNO)**: no worker MCP; node + platform scans still
      score; no false "waiting for workers" UX.
- [ ] **Spot / preemptible workers**: node vanishes forever mid-scan; next scan
      pool shrinks; INCONSISTENT from the missing node clears rather than
      sticky-failing the cluster score forever.
- [ ] **MachineConfigPool paused**: node remediations queue; scans still run;
      UI MCP-pause guidance remains accurate; score not blamed on "scan broken".

## B. Profile lifecycle (multi-benchmark)

- [x] Toggle one profile on/off updates bindings (`TestProfileToggle` e2e,
      `TestEnsureScanConfigCreatesAndPrunes`, jest `toggledProfiles`).
- [ ] **Enable all eight built-ins at once**: 8 ScanSettingBindings created,
      8 profiles appear in `Status.Profiles`, aggregate score spans all.
- [ ] **Disable a profile mid-scan**: its binding/suite is pruned, its results
      drop out of the aggregate and out of `Status.Profiles` on next reconcile.
- [x] Clearing the last profile is allowed and disables scanning
      (jest `toggledProfiles` returns `[]`; operator prunes bindings via
      `TestEnsureScanConfigScanningDisabled`).
- [ ] **Rapid toggle churn**: add+remove same profile within one reconcile
      window leaves no orphaned bindings.
- [ ] **Unknown/invalid profile key** in spec rejected by CRD enum validation
      (apply a bad key, expect admission error).
- [ ] **Optimistic lock on profile toggle**: Profiles tab `test`+`replace` patch
      fails cleanly when another writer changed `spec.profiles`; UI surfaces the
      API message, not a generic failure.
- [ ] **Order stability**: reordering `spec.profiles` without set change does
      not flap `status.profiles` order or relatedObjects order
      (bindings are sorted for relatedObjects today).

## C. Tailored profiles

- [x] Owned tailored suite recognized (`TestTailoredSuiteHelpers`, jest
      `suiteTailoredName` / `isOwnedByBaseline` tailored).
- [x] Tailored PVC counted in scan storage, including role-suffixed names
      (`TestCheckScanStorageTailoredPVC`).
- [x] **Ambiguous tailored base does not steal foreign PVCs**: base `ocp4`
      does not match Pending `ocp4-cis`; short `a` does not match `anything`
      (`TestCheckScanStorageTailoredPVC`, `TestMatchesAnyProfile`).
- [x] **Empty / `baseline-tp-` suite labels rejected** (operator
      `tailoredNameFromSuite`, jest `suiteTailoredName`).
- [x] **suiteFilterKey / deep-link ids**: tailored results filter as `tp-<name>`
      (jest `suiteFilterKey`; Overview `resultsHref('…', 'tp-…')`).
- [ ] **Tailored + built-in of the same base** (e.g. `cis` and a `cis-custom`
      TailoredProfile) both bound, both scored, not mutually clobbered.
- [ ] **TailoredProfile CRD absent** (older Compliance Operator): binding create
      fails soft or surfaces a clear condition; no reconcile crash loop.
- [ ] **Tailored name collision** with a built-in suite label → ownership
      routes to tailored first (code order: tailoredNameFromSuite before
      profileKeyFromSuite; add a regression unit test with dual maps).
- [ ] Deep-link + filter labels strip the `tp-` prefix for display (Playwright
      already screenshots this; assert filter chip title text).
- [ ] **Delete TailoredProfile CR while still listed in spec**: binding may
      exist, results go empty; Available stays true if CO ready; no panic.
- [ ] **Max-length tailored name (51 chars)**: admission accepts; suite label
      `baseline-tp-…` stays a valid label value (63-char budget).
- [x] **Metrics label `tp:<name>`**: publishMetrics emits tailored series
      (`TestPublishMetrics` asserts `tp:custom` pass/fail labels).

## D. Operator install lifecycle

- [x] Auto-install creates Subscription (`TestEnsureComplianceOperatorCreatesSubscription`).
- [x] Manual mode still detects an existing operator
      (`TestEnsureComplianceOperatorManualStillChecksExisting`).
- [x] Opt-out path (`TestEnsureComplianceOperatorOptOut`).
- [x] Adopt a pre-existing CSV / already-installed operator
      (`TestEnsureComplianceOperatorAlreadyInstalled`,
      `TestEnsureComplianceOperatorAdoptsExistingCSV`).
- [x] **Two CSV versions present** (upgrade in flight): newest Succeeded wins,
      else newest overall (`TestFindComplianceOperatorCSVChoosesNewestSucceeded`,
      `TestFindComplianceOperatorCSVFallsBackToNewestNonSucceeded`).
- [x] **CSV version suffix ordering**: release beats prerelease; build metadata
      tie-breaks on full name (`TestCompareComplianceCSVVersion`,
      `TestFindComplianceOperatorCSVPrefersReleaseOverPrerelease`).
- [x] **CSV Failed is terminal**: version remains empty, Degraded=True,
      Progressing=False (`TestSetComplianceOperatorReady`,
      `TestSetRollupConditions`).
- [x] **Stale high-version Succeeded in a foreign NS** loses to live
      Succeeded in `openshift-compliance`
      (`TestFindComplianceOperatorCSVChoosesNewestSucceeded`).
- [x] **Local Failed remnant vs remote Succeeded**: remote healthy CSV wins
      (`TestFindComplianceOperatorCSVRemoteSucceededBeatsLocalFailed`).
- [x] **Manual install only outside openshift-compliance**: still adopted
      (`TestFindComplianceOperatorCSVFallsBackOutsideComplianceNS`).
- [ ] **CSV present but not yet Succeeded** (Installing/Pending): condition
      Progressing, version empty; scan config may still run once CRDs exist.
- [ ] **Subscription exists but CSV never appears** (stuck install): surfaces
      bounded Progressing; requeue stays at 15s only while Progressing, not
      forever after a terminal reason.
- [ ] **Subscription status points at a deleted CSV**: condition stays
      Installing, version is cleared, no stale Ready condition remains
      (`TestSetComplianceOperatorReady` covers empty/missing pieces).
- [ ] **CatalogSource rename / disconnected**: `spec.complianceCatalogSource`
      override creates Subscription against the named source.
- [ ] **Remove CO under Manual mode**: Available flips False; score eventually
      clears when CRDs disappear; no reconcile crash.

## E. Scan storage & failure modes

- [x] Pending PVC → ScanStorageReady False / Degraded rollup
      (`TestCheckScanStorageDegradedOnPendingPVC`).
- [x] Empty namespace tolerated (`TestCheckScanStorageEmptyNamespace`).
- [x] **Fresh Pending (<2m)** does not Degrade yet
      (`TestCheckScanStorageDegradedOnPendingPVC`).
- [x] **Zero CreationTimestamp** ignored (no false age)
      (guard in `checkScanStorage`).
- [x] **Role-suffixed PVCs** for built-in and tailored names
      (`TestCheckScanStorageTailoredPVC`, `TestMatchesAnyProfile`).
- [ ] **No default StorageClass**: scans hang; operator reports Degraded with a
      clear reason (README claims this; assert end-to-end).
- [ ] **PVC bound but scan pod OOM/Error**: ComplianceScan `ERROR` phase
      reflected somewhere usable (UI and/or condition), not counted as pass.
- [ ] **Scan in progress**: Progressing or UI skeletons; score remains last
      completed value (not cleared mid-scan).
- [ ] **PVC Pending then Bound within grace**: Degraded never flaps True.
- [ ] **Foreign suite Pending PVC** (other operator / hand-made suite) does not
      Degrade this baseline.
- [ ] **StorageClass exists but WaitForFirstConsumer + no consumer**: PVC stays
      Pending; after 2m Degraded still fires (same path as no SC); message remains
      about Pending, not a wrong "no StorageClass" guess if we cannot know.
- [ ] **CSI provisioning slow >2m then succeeds**: Degraded true then false;
      no sticky Degraded after Bound without a reconcile (next requeue clears).

## F. Console plugin states & RBAC

- [x] Overview renders score + profile breakdown (Playwright).
- [x] Results table lists + filters (Playwright, jest `resultsHref`).
- [x] Reachable under Administration nav (Playwright).
- [x] **errorMessage** normalizes string / Error / `{message}` watch errors
      (jest `errorMessage`).
- [ ] **No ClusterBaseline yet**: Overview item shows "Not scanned", page shows
      empty/onboarding state, no crash (Playwright + jest on
      `ClusterScoreItem` loaded/empty branch).
- [ ] **Results CSV export UI path**: exports only filtered rows, attaches a
      temporary download link, removes it after click, revokes the object URL
      asynchronously (jsdom component test).
- [ ] **RBAC read-only user**: profile toggles + apply + rescan disabled
      (`useAccessReview` false path); assert disabled state in Playwright.
- [ ] **List error / RBAC denied on ClusterBaseline**: Overview item falls back
      to "—" (the `error` branch), no thrown error.
- [ ] **List error / RBAC denied on ComplianceCheckResult**: Results tab shows
      the SDK table load error, Export CSV stays disabled, page shell remains
      usable.
- [ ] **List error / RBAC denied on ComplianceRemediation**: warning banner
      remains visible, remediation load error is shown, auto-apply toggle still
      respects ClusterBaseline RBAC independently.
- [ ] **Partial rescan failure**: N of M scan patches fail → inline alert with
      counts; successful scans still rescanned.
- [x] **Console capability disabled**: plugin deregisters cleanly
      (`TestEnsureConsolePluginDisabled`, `TestConsoleTeardownToleratesMissingCRDs`).
- [ ] **HorizontalNav stability**: watch updates to ClusterBaseline do not
      remount tab roots (module-level route components / context pattern).
- [ ] **i18n key coverage**: every `t('…')` string exists in
      `locales/en/plugin__baseline-security-console-plugin.json` (CI grep or
      i18next extraction); Info donut slice is translated (locale has `Info`).

## G. Remediations

- [x] Owned remediations filtered (jest `isOwnedByBaseline`), node remediation
      detected (`isNodeRemediation`), rendered-object text (`remediationObjectText`).
- [x] Apply/auto-apply patch shape uses `add` (jest `remediationApplyPatch`).
- [x] k8s Status-shaped errors surface real messages (RemediationsTab /
      ProfilesTab use `errorMessage`).
- [ ] **Apply a node remediation**: confirmation modal warns about reboots;
      MachineConfigPool-pause guidance shown (Playwright screenshot exists;
      add assertion).
- [ ] **Outdated / MissingDependencies / Error state** rendered with the right
      Label color (jest on `stateColor` mapping).
- [ ] **Auto-apply toggle** writes `spec.remediation.apply` and survives a
      round-trip when the field was server-defaulted-absent (the `add`-patch
      fix).
- [ ] **Unapply path** does not show the reboot warning modal.
- [ ] **Foreign remediations** (other suite labels) never appear in the table.
- [ ] **Apply when CO auto-apply is already Automatic**: UI toggle and CO
      ScanSetting stay consistent after reconcile.

## G2. Waivers (risk acceptance)

`spec.waivers []{name, reason, …expiry/attribution}` marks a ComplianceCheckResult
as accepted risk. The controller remaps only FAIL results whose name is actively
waived (not expired) into the Waived bucket, out of the pass/fail denominator, so
an accepted risk neither inflates nor tanks the score.

- [x] **Waived FAIL leaves the denominator** into the Waived bucket, raising the
      score (`TestAggregateStatusWaivers`).
- [x] **FAIL-only, self-healing**: a waiver applies only while the check is FAIL;
      if it later PASSes it counts as PASS again (no silent score depression),
      and the UI still shows the Remove control for any waived check
      (`TestAggregateStatusWaivers` waive-a-PASS case; ResultsTab `showWaiver`).
- [x] **Live waiver round-trip**: waive a real FAIL → Waived bucket +1, fail -1,
      remove → reverts; built-in and tailored buckets both summed
      (`TestWaiverExcludesCheck`, e2e).
- [x] **JSON-patch helpers**: add-array when absent vs append when present;
      remove test-guards the name by index (jest `addWaiverPatch`,
      `removeWaiverPatch`, `isWaived`, incl. fuzz).
- [x] **Waive UI gated + present**: modal offers Waive on FAIL checks (score-
      affecting only) and Remove for any already-waived name; enabled only with
      `clusterbaselines:patch` (Playwright asserts enabled for kubeadmin;
      ResultsTab `showWaiver`).
- [x] **Waived donut slice + metric** (`aggregateCounts` waived; `setCheckCounts`
      waived series in `TestPublishMetrics`).
- [ ] **Waive every FAIL** → score 100 (pass/(pass+0)); **waive all results** →
      pass+fail==0 → score nil. Add explicit unit boundary cases.
- [ ] **Waiver for a non-owned / stale result name**: harmless, never affects the
      score (only owned suites are tallied); add a negative unit case.
- [ ] **Duplicate waiver name** rejected by `+listType=map`+`listMapKey=name`
      admission; **RBAC read-only user** cannot see the enabled Waive button.
- [ ] **Cross-profile rule**: a rule scanned by both cis and pci-dss has distinct
      result names; waiving one does not waive the other (document the by-name
      contract; add an assertion).

## H. Overview dashboard item (cluster Overview)

- [x] Renders `<n> / 100` deep-link when scored (Playwright `dashboard-score`).
- [x] **clusterScore singleton selection** prefers `metadata.name == cluster`
      (jest `clusterScore`).
- [x] **scoreColor thresholds**: >=90 success, >=60 warning, else danger
      (jest `scoreColor`).
- [ ] **ClusterScoreItem render branches**: loading/error → "—"; scored →
      colored link; not-scanned → "Not scanned" (component unit test).
- [ ] **Extra ClusterBaselines in the list**: still prefers `cluster` name.

## I. Metrics & alerts

- [x] Sentinel `-1` before first score, gauge set after
      (`TestComplianceScoreSeededSentinel`, `TestPublishMetrics`).
- [x] **Per-status series including info/inconsistent/waived** and tailored `tp:`
      prefix (`TestPublishMetrics`).
- [x] **Alert expressions are HA-safe**: `max()` / `max by (profile,status)` in
      `config/prometheus/prometheusrule.yaml`, pinned by promtool HA-dedup case.
- [x] **PrometheusRule** `ComplianceScoreLow` / `ComplianceChecksFailing` fire
      against synthetic metric values (`make test-alerts`, promtool, no cluster):
      score 79 fires, 80 does not, `-1` sentinel never fires, HA dup pods dedup
      to value 5, fail=0 no alert (`config/prometheus/testdata/alerts_test.yaml`).
- [x] **Profile removed from spec**: old `{profile,status}` series are deleted
      via set-then-delete (not GaugeVec.Reset) so scrapers never see an empty
      gap (`TestPublishMetricsDropsRemovedProfile` CollectAndCount).
- [x] **Remediation batch started-at gauge**: tracks
      `status.remediationBatch.startedAt`, clears when the batch ends
      (`TestPublishMetricsBatchStartedTimestamp`).
- [ ] **ServiceMonitor scrape**: with cluster monitoring (namespace
      openshift.io/cluster-monitoring label) + scraper SA token, metrics
      endpoint returns 200 and includes custom gauges (live or kind).

## J. Deletion & finalizer

- [x] Finalizer added + requeue (`TestReconcileAddsFinalizerAndRequeues`).
- [x] Deletion deregisters plugin + removes finalizer
      (`TestReconcileDeletionDeregistersAndRemovesFinalizer`).
- [x] Teardown tolerates missing Console/CRDs
      (`TestConsoleTeardownToleratesMissingCRDs`,
      `TestDeregisterConsolePluginMissingConsole`).
- [x] **Deregister is a no-op** when plugin was never registered
      (`TestDeregisterConsolePluginNoop`).
- [ ] **Delete with remediations applied**: MachineConfigs are NOT reverted on
      CR delete (document + assert the non-destructive contract).
- [ ] **Delete while ScanSettingBinding still has running scans**: finalizer
      path does not block forever; owned objects GC; CO may leave results.
- [ ] **Double-delete / already-finalizing**: second delete is NotFound, no
      log spam / panic.

## K. Malformed / adversarial input

- [x] Fuzz: suite-label round-trip, profile-key parse, score, CSV export,
      results href, history ring, profile names, `withoutPlugin`,
      `matchesAnyProfile` (role-suffix oracle).
- [x] CSV formula-injection neutralized, including whitespace-prefixed sigils
      (jest `resultsCsv`).
- [x] **Unpaired surrogates in filter URLs** do not throw (jest `resultsHref`
      fuzz).
- [x] **History never rewinds** when the newest suite is dropped
      (`TestRecordHistoryDoesNotRewind`).
- [x] **Late score for the same endTimestamp** appends/refreshes history
      (`TestRecordHistoryAppendsWhenScoreAppearsLater`,
      `TestRecordHistoryRing` equal-scan refresh).
- [ ] **Check result with missing/garbage `status`** string → tally ignores it,
      no miscount, no panic.
- [ ] **ComplianceCheckResult with no suite label** → not attributed, excluded
      from score.
- [ ] **ComplianceCheckResult with malformed suite** (`baseline-`,
      `baseline-tp-`, very long string, uppercase) → not owned unless it
      exactly matches the accepted shape.
- [ ] **ComplianceScan with malformed endTimestamp** → ignored for history,
      does not clear a valid previous LastScanTime.
- [ ] **Score history with multiple suites completing out of order**: newest
      endTimestamp wins; multi-suite fixture.
- [ ] **Huge result set** (thousands of checks): aggregate int math doesn't
      overflow; VirtualizedTable remains usable.
- [ ] **Huge untrusted descriptions/instructions**: Results modal remains
      responsive; text is not interpreted as HTML; CSV stays browser-safe.
- [ ] **Unicode / RTL / emoji in rule titles**: filter, CSV, and modal do not
      corrupt surrounding layout.
- [ ] **CSV formula variants**: fullwidth `＝`, tab-only cells, multiline
      cells with formula on line 2 (document residual risk if not neutralized).

## L. Deployment & upgrade

- [ ] **Console bridge caches the plugin manifest**: after a plugin image
      change the console may keep old chunk names until it re-reads the
      manifest. Rolling `deploy/console` forces a refresh. Document in the
      install guide; not necessarily a code bug.
- [x] **CRD field added across versions**: console treats older status without
      `inconsistent` / `info` as zero (`aggregateCounts` missing-field
      regression).
- [x] **Bundle validates**: `make bundle` runs operator-sdk bundle validation.
- [ ] **OLM `replaces` chain**: 0.2.1 replaces 0.2.0 cleanly; bundle CRD is not
      stale vs `config/crd` (CI `make manifests && git diff --exit-code`).
- [ ] **Image digest pinning**: `RELATED_IMAGE_CONSOLE_PLUGIN` @sha256 change
      rolls the plugin deployment (tag reuse would silently keep the old layer).
- [ ] **Generated CRD ordering drift**: `make build` / `make bundle` does not
      leave uncommitted generated YAML diffs in CI.
- [ ] **Downgrade / rollback**: installing the previous bundle over a cluster
      with newer status fields (e.g. `info`) does not break the older
      console/plugin.
- [ ] **CRD score bounds**: status.score and history[].score reject values
      outside 0–100 at the API if a buggy client writes them (schema
      Minimum/Maximum present in CRD).

## M. Concurrency & requeue

- [x] **Invalid schedule soft-fail**: bindings and auto-apply still reconcile;
      last-good ScanSetting schedule preserved; first create seeds default
      cron; ScanConfigured=False/InvalidSchedule
      (`TestEnsureScanConfigCreatesAndPrunes`).
- [x] **NextScanTime parsing**: valid cron advances; invalid yields nil
      (`TestNextScanTime`).
- [ ] **Two rapid spec edits**: optimistic-lock conflict on status update is
      retried via requeue, not left as permanent Degraded.
- [ ] **Reconcile during an in-progress scan**: score is the last completed
      value (not cleared mid-scan).
- [ ] **Schedule change**: editing `spec.schedule` updates `NextScanTime` on
      the next reconcile; invalid cron yields nil NextScanTime while Degraded.
- [ ] **Status-update conflict**: another writer updates status between
      aggregation and `Status().Update`; next reconcile recovers rollups.
- [ ] **Finalizer update conflict**: add/remove finalizer paths requeue cleanly
      instead of losing deletion cleanup.
- [ ] **Console plugins list concurrent edit**: RetryOnConflict on register /
      deregister wins without dropping foreign plugins
      (`withoutPlugin` already unit-tested).

## N. API admission & CRD schema

- [ ] **Singleton name admission**: server-side dry-run rejects any
      ClusterBaseline not named `cluster` (CEL rule).
- [ ] **Profiles validation**: duplicate profiles and unknown profile keys are
      rejected by CRD schema; an empty list is allowed (disables scanning when
      `tailoredProfiles` is also empty; see CHANGELOG [0.5.0] / SPEC §4.1).
- [ ] **TailoredProfiles validation**: duplicate, empty, too-long, uppercase,
      underscore, and path-like values are rejected; DNS-subdomain-like names
      are accepted; max length 51 leaves room for `baseline-tp-` suite labels.
- [ ] **Defaulting**: omitted spec fields default as documented
      (`profiles: [cis]`, daily schedule, Automatic install, Managed console,
      Manual remediation) in a real API server dry-run.
- [ ] **Status schema completeness**: generated CRD includes all count fields
      (`pass`, `fail`, `manual`, `info`, `error`, `inconsistent`, `waived`,
      `notApplicable`) for built-in and tailored profile status objects.
- [ ] **OpenAPI printer columns**: `oc get clusterbaseline` shows Score and
      Last Scan from status after reconcile.
- [ ] **Admission vs controller validation split**: invalid cron is accepted by
      the API but reported by the controller; invalid enum/name fields are
      rejected before reconcile.

## O. Startup, HA & runtime behavior

- [x] **Metrics cert fallback/reload**: self-signed fallback, service-ca pair
      load, and mtime reload (`TestMetricsCertProviderSelfSignedWhenMissing`,
      `TestMetricsCertProviderLoadsAndReloads`).
- [x] **Insecure metrics guard**: non-loopback insecure metrics forced secure
      (`TestIsLoopbackMetricsAddr`).
- [x] **Plugin HA strategy**: Deployment maxUnavailable=1 so Available stays
      True at 1/2 ready (`TestEnsureConsolePlugin` strategy assert).
- [x] **pluginReadyMin=1**: partial ready is Deployed, not forever Progressing
      (`TestEnsureConsolePlugin`).
- [x] **Available=False past grace** becomes Unavailable even with some ready
      pods (`TestDeploymentAvailableFalsePastGrace`).
- [x] **ConsoleMissing is not Progressing** (no 15s poll storm)
      (`TestConditionProgressing`).
- [x] **CRDsMissing is not Progressing** (Manual install without CO settles)
      (`TestConditionProgressing`).
- [ ] **Default ClusterBaseline creation**: manager creates `cluster` once when
      none exists, after cache sync, and tolerates AlreadyExists.
- [ ] **Default creation opt-out**:
      `BASELINE_SECURITY_SKIP_DEFAULT_CR=true` prevents default CR creation.
- [ ] **Leader-only default creation**: two operator replicas do not race the
      default CR creation; only the elected leader runs the runnable.
- [ ] **Leader loss**: demoted process exits (controller-runtime safety);
      successor publishes metrics; alerts using `max()` do not false-fire from
      empty non-leader series.
- [ ] **Read-only root filesystem runtime**: operator pod starts with
      `readOnlyRootFilesystem: true`, optional metrics cert volume missing.
- [ ] **API outage resilience**: transient list/create/update failures do not
      terminate the manager; reconcile retries and status recovers.
- [ ] **Watch-start before dependency CRDs exist**: manager starts without
      Compliance Operator CRDs; NoKindMatch paths keep the CR usable instead of
      crashing.
- [ ] **RELATED_IMAGE_CONSOLE_PLUGIN unset**: ImageMissing, scans still
      reconcile, Available can still become True when CO+scans ready.

## P. Packaging, images & supply chain

- [ ] **Operator image smoke**: built manager runs as non-root, health probes
      and secure metrics only.
- [ ] **Console image smoke**: nginx non-root, TLS only on 9443,
      `ssl_protocols TLSv1.2 TLSv1.3`, `server_tokens off`; fails closed until
      service-ca cert files exist.
- [ ] **Docker context audit**: `.dockerignore` excludes `node_modules`,
      `dist`, local e2e artifacts, and logs while preserving lockfiles.
- [ ] **Dependency vulnerability scan**: Go module and Yarn dependency scan has
      no high/critical unfixed findings before release.
- [ ] **SBOM/provenance**: release pipeline emits SBOMs for operator and console
      images and records source commit + base image digests.
- [ ] **Multi-arch build**: operator and console images build and run on amd64
      and arm64 (or the catalog declares supported arches).

## Q. Accessibility, i18n & UI scale

- [ ] **Axe smoke**: Overview, Results, Remediations, and Profiles tabs have no
      critical accessibility violations under Playwright.
- [ ] **Keyboard-only flows**: profile toggle, result detail modal, remediation
      confirmation modal, and Export CSV are reachable without a mouse.
- [ ] **Focus management**: opening and closing modals restores focus to the
      triggering control.
- [ ] **Translation coverage**: every user-facing string exists in the en
      locale file; no raw key leakage for new donut slices (Info, Inconsistent).
- [ ] **Narrow viewport**: dashboard cards, donut legend, table filters, and
      remediation modals do not overlap or clip text at common laptop/tablet
      widths.
- [ ] **Large data UI**: thousands of results remain scrollable with
      VirtualizedTable; filters stay responsive; export uses the filtered
      subset only.
- [ ] **High-contrast / forced-colors**: status Labels and donut colors remain
      distinguishable (or have text fallbacks).

## R. Condition rollup matrix (adversarial truth table)

Pin combinations so a future "simplify conditions" PR cannot reintroduce
stale Available or eternal Progressing.

| Scenario | Available | Progressing | Degraded | Covered |
|---|---|---|---|---|
| Happy path CO+scans ready | True | False | False | [x] `TestSetRollupConditions`, happy reconcile |
| CO Installing | False | True | False | [x] |
| CO CSV Failed | False | False | True (CSVFailed) | [x] |
| Invalid schedule | False | False | True (InvalidSchedule) | [x] |
| Scan storage Pending >2m | True* | False | True (ScanStorageNotReady) | [x] *if CO+scan cfg already True; detail is ScanStorageReady False / ScanStoragePending |
| Plugin Unavailable >5m | True* | False | True (ConsolePluginUnavailable) | [x] |
| Plugin WaitingForPods | True* | True | False | [x] |
| ConsoleMissing | True* | False | False | [x] Progressing only |
| ImageMissing | True* | False | False | [x] |
| CRDsMissing (Manual, no CO) | False | False | False | [x] Progressing only |
| Reconcile hard error | (prior) | (prior) | True (ReconcileError) | [~] best-effort status write |
| Plugin Disabled (Removed) | True* | False | False | [x] reason Disabled |

\* Available depends only on CO Ready + ScanConfigured, not on the plugin.

- [ ] **Automated matrix test**: table-driven `setRollupConditions` cases for
      every row above in one `TestSetRollupConditionsMatrix`.

## S. Time, schedule & history theater

- [x] Invalid cron soft-fail preserves last-good schedule
      (`TestEnsureScanConfigCreatesAndPrunes`).
- [x] History ring cap 30, no aliasing after truncate
      (`TestAppendHistoryRing`, `FuzzAppendHistoryRing`).
- [ ] **DST / timezone**: container runs UTC; cron `0 1 * * *` means 01:00 UTC
      not admin local; document and assert NextScanTime offset.
- [ ] **Leap second / clock skew**: endTimestamp in the future is ignored or
      handled without panicking; does not pin LastScanTime forever in the future.
- [ ] **Two suites complete in the same second**: one history point, score is
      the pooled value after both are visible.
- [ ] **Rescan annotation storm**: double-click Rescan with unique tokens;
      both patches apply; CO triggers at least one rescan.
- [ ] **Schedule every minute** (`* * * * *` if admitted): controller requeue
      does not amplify into API hammering beyond Progressing 15s cadence.
- [ ] **Empty schedule string**: treated as default `0 1 * * *` for ScanSetting
      and NextScanTime; Overview shows the effective schedule, not "—".

## T. Chaos, topology & "day 2" environments

- [ ] **SNO vs multi-node**: same operator image; only node-scan fan-out differs.
- [ ] **Disconnected cluster**: custom `complianceCatalogSource`; no
      redhat-operators; install still succeeds when the catalog is mirrored.
- [ ] **Proxy / trusted CA**: operator and plugin pods honor cluster proxy
      where required (CO content pulls); plugin itself is static nginx.
- [ ] **Cluster-admin vs namespace admin**: baseline is cluster-scoped; namespace
      admin without clusterbaseline RBAC cannot toggle profiles.
- [ ] **Compliance Operator upgrade under us**: CSV rolls vN → vN+1; we keep
      Ready on newest Succeeded; no dual-binding storm.
- [ ] **Hand-edited ScanSetting**: controller reasserts owned fields
      (schedule when valid, roles, autoApply*, rawResultStorage) without
      deleting foreign annotations.
- [ ] **Hand-created foreign binding** in openshift-compliance: never pruned
      (only controller-owned bindings deleted)
      (`TestEnsureScanConfigCreatesAndPrunes` foreign survival).
- [ ] **etcd full / API 429**: reconcile backs off; no tight error loop
      thrashing leader election.
- [ ] **Node pressure / plugin pod eviction**: with maxUnavailable=1 and
      replicas=2, Available stays True at 1 ready; no false Degraded within grace.

## U. Security & tenancy

- [x] CSV export formula hardening (jest).
- [x] Plugin pods: non-root, drop ALL caps, no automount SA token, optional
      serving-cert volume (unit asserts on Deployment mutate).
- [x] Metrics: authn/authz filter + non-loopback insecure refuse.
- [ ] **SSRF / open redirect**: `checkResultHref` and `resultsHref` stay
      path-relative; no `//evil` host injection via crafted names.
- [ ] **XSS in rule description**: modal uses text/pre wrappers, not
      `dangerouslySetInnerHTML`.
- [ ] **CSV content-type / download**: export uses `text/csv` blob and a fixed
      filename; no user-controlled path.
- [ ] **Metrics endpoint without token**: returns 401/403; scraper SA with
      metrics-reader role succeeds.
- [ ] **ConsolePlugin serving cert rotation**: pods pick up new service-ca
      secret without crashloop (optional secret volume).
- [ ] **Must-gather script**: collects ClusterBaseline, relatedObjects, and
      recent CO scans without collecting unrelated secrets.

## V. Cross-version Compatibility Operator content

- [ ] **ProfileBundle rename / content image bump**: Profile names in
      `ProfileNames()` still resolve; missing Profile yields CO binding error
      that we surface rather than silent zero score.
- [ ] **New CO status value** (future enum): unknown statuses ignored in tally,
      still visible raw in Results table via fallback Label.
- [ ] **CO without TailoredProfile support**: binding create fails predictably.
- [ ] **Older plugin against newer CR status** (extra fields): ignore unknown
      JSON; score still renders.
- [ ] **Newer plugin against older CR status** (missing `info`): zero-fill,
      no NaN badges.

## W. CI quality, flakes & test maintainability

- [ ] **Presubmit mirrors Tier 0**: CI runs operator lint/test, console
      lint/typecheck/Jest, and `git diff --check` on every PR.
- [ ] **Generated drift gate**: CI runs controller-gen / bundle generation and
      fails if CRDs, RBAC, deepcopy, or bundle manifests drift.
- [ ] **Frontend build gate**: CI runs `yarn build`; the known vendor chunk
      warning is allowed, but new webpack errors or missing locale assets fail.
- [ ] **Race/fuzz scheduled job**: Tier 2 runs nightly or on demand with
      persisted fuzz corpora; new crashers are committed as regression seeds.
- [ ] **Playwright artifact policy**: failed runs upload trace, console logs,
      screenshots, and relevant Kubernetes YAML/events.
- [ ] **E2E namespace cleanup**: every e2e leaves no extra ClusterBaselines,
      ScanSettingBindings, ConsolePlugin registration, or remediations applied.
- [ ] **Flake triage rule**: no silent retries in PR jobs; a flaky test gets an
      issue, owner, and quarantine label before retries are added.
- [ ] **Test fixture builders**: new tests use shared builders for
      ClusterBaseline, CSV, ScanSettingBinding, ComplianceScan, and
      ComplianceCheckResult objects instead of hand-rolled maps.
- [ ] **Coverage smoke**: unit coverage does not drop materially for
      `internal/controller` and console helper modules when code changes.
- [ ] **Version pin audit**: Go, Node, Yarn, kubectl, operator-sdk, and
      controller-gen versions used locally match CI and Dockerfiles.

## X. Supportability, docs & runbooks

- [ ] **Install guide smoke**: every command in README/SPEC install steps works
      against a fresh cluster or kind/OpenShift test environment.
- [ ] **Uninstall guide smoke**: deleting ClusterBaseline and uninstalling the
      bundle leaves no console plugin registration and does not uninstall the
      Compliance Operator by surprise.
- [ ] **Troubleshooting runbooks**: documented checks exist for Compliance
      Operator stuck install, missing StorageClass, ConsolePlugin unavailable,
      RBAC denied, and no scan results.
- [ ] **Alert runbooks**: `ComplianceScoreLow` and `ComplianceChecksFailing`
      annotations point admins to actionable console/CLI steps.
- [ ] **Must-gather smoke**: `operator/hack/must-gather.sh` runs without
      cluster-admin-only assumptions beyond documented RBAC and redacts or
      avoids secrets.
- [ ] **Screenshot freshness**: screenshots in `docs/screenshots/` are
      regenerated or explicitly accepted whenever UI layout changes.
- [ ] **Known warning ledger**: accepted warnings, like the current webpack
      vendor chunk size warning, are documented with a revisit trigger.
- [ ] **Disconnected docs**: catalog source override, mirrored images, and
      digest-pinned `RELATED_IMAGE_CONSOLE_PLUGIN` are documented and tested.
- [ ] **RBAC docs**: viewer/admin roles describe exactly which console actions
      are enabled or disabled.
- [ ] **Release notes regression map**: every shipped bugfix references the
      test-plan bullet and automated test that prevents recurrence.

## Y. Rescan & scan lifecycle theater

CO rescans and suite lifecycle are easy to get "mostly right" and still wrong
under racey human clicks.

- [ ] **Double-click Rescan**: two distinct annotation values applied; CO sees
      at least one change; UI does not leave `isLoading` stuck if the second
      promise rejects.
- [ ] **Rescan with zero owned scans**: button disabled; no empty
      `Promise.allSettled` success path that clears prior rescan errors oddly.
- [ ] **Rescan while scans are already RUNNING**: second rescan is accepted or
      clearly no-ops; score still reflects last completed endTimestamp only.
- [ ] **Rescan when annotations map is nil vs empty object**: both patch shapes
      from `rescanPatch` succeed against a live API (envtest or e2e).
- [ ] **Partial rescan RBAC**: user can patch some scans but not others → alert
      "N of M failed"; successful scans still annotated.
- [ ] **Scan phase matrix**: fixture each of DONE / RUNNING / ERROR / AGGREGATING
      (whatever CO emits) and assert Overview never shows a green "complete"
      score while the only owned scan is mid-flight without a prior history
      point.
- [ ] **Suite disappears mid-UI session**: watch removes all results for a
      deselected profile; filters and donut update without a full page reload.
- [ ] **Stale modal**: result detail modal open when the watched object is
      deleted; close path does not throw; content does not claim live data.
- [ ] **endTimestamp equals LastScanTime but result counts change**: history
      point score refreshes (unit already partial); UI trend line updates after
      reconcile without inventing a second x-axis tick.

## Z. Browser, console shell & client weirdness

The plugin runs inside the OpenShift console host; the host is not a blank
page.

- [ ] **Console language switch**: after changing console language, plugin
      strings still resolve from the plugin namespace (no missing-key flash of
      English keys for Info/Inconsistent).
- [ ] **Hard refresh mid-tab**: deep link
      `/baseline-security/results?rowFilter-result-status=FAIL` restores filters
      after reload.
- [ ] **Back/forward navigation**: Overview → Results (filtered) → back preserves
      Overview scroll position reasonably; no double-mount error boundary.
- [ ] **Multiple console tabs**: two browser tabs open; rescan in one does not
      deadlock the other (watches independent).
- [ ] **Slow network / throttled CPU**: skeletons appear; no blank white flash
      for >2s when watches are pending (Playwright slow-3G optional).
- [ ] **Popup blockers**: CSV download still works without `window.open`; uses
      anchor click + object URL (current implementation).
- [ ] **Safari / Firefox matrix** (if supported): modal close, Switch, and
      VirtualizedTable keyboard nav; document unsupported browsers if any.
- [ ] **Zoom 200%**: donut legend and description lists wrap without covering
      the Rescan button.
- [ ] **Prefers-reduced-motion**: charts do not rely on motion for meaning
      (score number still visible).
- [ ] **Console plugin disable/enable**: after `spec.console.managementState:
      Removed` then Managed, registration returns and UI loads without a
      manual console rollout (or document the required rollout).

## AA. Alert storms, silence & observability false friends

- [x] **Score exactly 80**: `ComplianceScoreLow` does not fire (`< 80`, not
      `<=`); promtool case (`alerts_test.yaml`).
- [x] **Score sentinel -1**: never fires ComplianceScoreLow (promtool `-1` case).
- [x] **HA duplicate pods**: two pods reporting fail=5 for one profile dedup via
      `max by` to value 5, not 10 (promtool HA case).
- [ ] **Flapping score 79↔81**: `for: 30m` prevents page storms; document how
      to validate with promtool `eval_time` steps.
- [ ] **Fail count goes 5→0**: ComplianceChecksFailing clears after `for: 1h`
      window; no sticky alert from a demoted leader process (process exit on
      leader loss is the safety net; assert in runbook).
- [x] **Profile removed while fails remain on disk**: check series for that
      profile are deleted on the next publish (set-then-delete, unit
      `TestPublishMetricsDropsRemovedProfile`); live scrape lag is one interval.
- [ ] **Cardinality bomb**: enabling all 8 profiles + N tailored does not create
      unbounded metric label sets (fixed status enum × profile set only).
- [ ] **Scrape interval vs reconcile**: 30s scrape + 60s requeue still shows
      score within one scrape of reconcile on a quiet cluster.
- [ ] **Alertmanager inhibit**: optional recording that Degraded=True on the CR
      correlates with ops noise; not required, but document if product wants it.

## AB. Ghost objects, GC & "who owns this?"

- [ ] **Orphan ScanSetting `baseline`**: ClusterBaseline deleted but finalizer
      interrupted; GC eventually removes owned ScanSetting/bindings via owner
      refs; document manual cleanup if finalizer stuck.
- [ ] **OwnerRef on cluster-scoped ConsolePlugin**: deleting ClusterBaseline
      GCs ConsolePlugin; no cluster-admin needed beyond normal delete rights.
- [ ] **Namespace `openshift-baseline-security` left behind**: after uninstall,
      empty NS may remain; document whether that is expected.
- [ ] **Namespace `openshift-compliance` left behind**: never deleted by us;
      assert uninstall does not remove it.
- [ ] **ConsolePlugin name collision**: another product ships the same plugin
      name (unlikely); register path is idempotent; deregister only removes
      our name from the plugins list.
- [ ] **Dangling rescan annotations**: leftover
      `compliance.openshift.io/rescan` on scans after CR delete is harmless;
      document.
- [ ] **Status.relatedObjects drift**: after profile prune, relatedObjects no
      longer lists deleted bindings (deterministic sort still holds).
- [ ] **Finalizer name typo migration**: if finalizer string ever changes,
      old CRs with the previous finalizer must not brick deletion (document
      dual-finalizer or one-shot migration test).

## AC. Performance budgets & scale assumptions

These are product contracts, not just "hope it's fast".

| Surface | Budget (proposal) | How to test |
|---|---|---|
| Reconcile happy path (fake client, 1k check results) | < 200ms CPU | Go benchmark / unit timer |
| Reconcile 10k check results | < 2s CPU; no O(n²) label maps | unit with generated list |
| Console Results first paint with 5k rows | interactive filters < 100ms after load | Playwright performance marks or manual |
| CSV export 5k rows | < 3s in Chromium; no tab freeze dialog | Playwright |
| History ring | max 30 points; status JSON stays small | unit size assert |
| Metrics series count | O(profiles × statuses) only | unit CollectAndCount |

- [ ] Encode the table as CI budgets where cheap (Go benchmarks for aggregate).
- [ ] **Memory**: operator RSS does not climb unboundedly across 1000 reconcile
      loops with fixed fixture size (leak regression).
- [ ] **API QPS**: single reconcile does not list the same resource type more
      than a documented maximum (count List calls via fake client wrapper).

## AD. Failure injection catalog (chaos engineering lite)

Injectable faults for envtest or a "faulty client" wrapper:

- [ ] **List CheckResults returns timeout once, then success**: no permanent
      Degraded; score recovers next loop.
- [ ] **Status().Update always conflicts 3 times, then succeeds**: requeue
      recovers; no ReconcileError left sticky if the next full reconcile is
      clean.
- [ ] **Create Subscription returns AlreadyExists**: createIfMissing ignores;
      install continues.
- [ ] **Delete binding returns NotFound**: prune path continues.
- [ ] **Get Console returns NoKindMatch**: deregister and ensure paths soft-fail.
- [ ] **Patch rescan returns 403 for half the scans**: UI partial failure alert.
- [ ] **Watch stream dies and restarts**: console shows transient error then
      recovers without requiring a full reload.
- [ ] **Clock jumps forward 2 hours mid-process**: NextScanTime and PVC age
      logic do not panic; Degraded may flip based on new "now" (document).

## AE. Persona journeys (scripted e2e stories)

Each journey is a Playwright or documented click-path with expected outcomes.

- [ ] **Day-0 cluster admin**: install operator → default CR appears → wait for
      first CIS score → open Administration → Compliance → see donut.
- [ ] **Auditor (view-only)**: can open all tabs and export CSV; cannot toggle
      profiles, apply remediations, or rescan.
- [ ] **Platform SRE**: enables STIG + CIS; confirms relatedObjects and
      bindings; sets Invalid schedule → Degraded banner; fixes schedule →
      clears.
- [ ] **Security engineer**: opens FAIL high-severity row; follows
      instructions; applies non-node remediation; rescans; score moves.
- [ ] **Break-glass**: sets console Removed for airgap console-less ops; scans
      continue; Available still True when CO+scans OK.
- [ ] **Compliance content owner**: binds a TailoredProfile; deep-links
      `tp-<name>` filter; exports CSV of tailored fails only.
- [ ] **On-call at 3am**: must-gather + `oc get clusterbaseline cluster -o
      yaml` + metrics curl with SA token; runbook steps complete in <15 min.

## AF. Generative / property-based ideas

Beyond existing fuzz targets, properties that should always hold:

- [ ] **Ownership exclusivity**: no CheckResult is counted in both a built-in
      profile bucket and a tailored bucket in one reconcile.
- [ ] **Score monotonic bounds**: whenever score is non-nil, `0 <= score <= 100`.
- [ ] **History monotonic times**: after no-rewind fix, `LastScanTime` never
      decreases across reconciles for a fixed wall clock of scan objects.
- [ ] **RelatedObjects ⊆ owned names**: every binding name in relatedObjects is
      in `ownedSuites(spec)` and sorted.
- [ ] **Filter idempotence**: applying the same Results filters twice yields
      the same row set.
- [ ] **CSV round-trip sanity**: every exported row has 4 columns; quote count
      even (already fuzz-adjacent in jest).
- [ ] **Toggle involution**: enable profile X then disable X (when >1 profiles)
      returns to the prior set (modulo ordering).

## AG. Multi-tenant & shared-cluster politics

- [ ] **Two teams share one cluster**: only one ClusterBaseline/cluster allowed;
      second name rejected by CEL; document the singleton product choice.
- [ ] **Parallel CO use by another product**: foreign suites never enter score;
      foreign bindings never pruned.
- [ ] **ACS installed alongside**: no API fight; both may create scans; our
      suite labels remain the ownership boundary.
- [ ] **Policy engine (Gatekeeper/Kyverno) denies ScanSetting create**:
      ScanConfigured False with ReconcileError or binding error; message
      actionable.
- [ ] **NetworkPolicy denies operator → API**: probes fail; document symptoms
      vs CO install failure.

## AH. "Wrong layer" traps (tests that catch design confusion)

- [ ] **UI cannot invent score**: with empty status.score but results in the
      API, Overview shows "—" / no score until the operator reconciles (no
      client-side recompute of global score from raw results).
- [ ] **Operator cannot invent Profiles not in spec**: status.profiles keys
      always ⊆ spec.profiles.
- [ ] **Plugin image tag mutable**: changing only the tag without digest may
      not roll pods if imagePullPolicy is IfNotPresent; document digest
      requirement for airgap.
- [ ] **Default CR vs user CR**: if user deletes default `cluster` and
      recreates with different profiles, default-create runnable does not
      stomp (list non-empty / AlreadyExists).
- [ ] **Score 0 is not "not scanned"**: zero score renders as `0 / 100` with
      danger color; dashboard item does not show "Not scanned"
      (jest `clusterScore` treats 0 as a value; add render assert).

## AI. Localization & copy edge cases

- [ ] **Long German/Finnish translations**: Profile card titles and alert
      messages wrap; no horizontal page scroll on Overview.
- [ ] **Pluralization**: rescan failure string with count=1 vs count=5.
- [ ] **Empty message Degraded**: condition message empty → UI still shows
      title "Scanning degraded" without "undefined".
- [ ] **Bidirectional text in check titles**: CSV and modal do not reorder
      surrounding punctuation into garbage.
- [ ] **Locale file parity with dist**: production webpack emits the same keys
      as `locales/en/…` (build gate).

## AJ. Upgrade & migration storyboard

Scripted version ladder (bundle N-1 → N → N+1):

- [ ] **0.2.0 → 0.2.1**: CRD gains fields; existing status objects still read;
      console zero-fills new counts.
- [ ] **Mid-upgrade operator pod old+new**: two replicas during rollout; leader
      election single active reconcile; no dual Subscription creates.
- [ ] **CRD conversion** (if ever v1beta1→v1alpha1 changes): round-trip
      preserved; until then document "no conversion webhook".
- [ ] **Webhook-less admission only**: CEL/schema failures are the only
      admission path; controller validations remain for cron.
- [ ] **Storage version migration**: large clusters with many CheckResults do
      not block our CRD upgrade (we do not store those objects).

## AK. Exploratory test charters

Run these manually when the product shape changes, then convert any bug into a
specific automated row above.

- [ ] **First-hour admin**: install from scratch, wait for first scan, open the
      console, export CSV, apply one safe non-node remediation, and rescan.
- [ ] **Skeptical auditor**: log in with read-only access, confirm the score,
      inspect raw ComplianceCheckResult links, and verify no write controls are
      active.
- [ ] **Disconnected cluster admin**: install with mirrored catalog/images,
      digest-pinned plugin image, no outbound internet, and custom catalog
      source.
- [ ] **Broken dependency operator**: Compliance Operator Subscription exists
      but CSV is Pending/Failed; verify conditions, alerts, and runbook text
      point to the same root cause.
- [ ] **No storage class cluster**: first scan stalls on PVC Pending; verify
      Degraded, alert, runbook, and UI all name storage rather than generic
      "scan failed".
- [ ] **Console upgrade operator**: restart/upgrade console while plugin chunks
      change; verify route still loads and stale chunk failures recover.
- [ ] **Security reviewer**: try crafted suite labels, rule descriptions,
      check-result names, CSV formula strings, and path-looking values.
- [ ] **Scale operator**: seed 1k/10k check results and 8 profiles; watch
      reconcile latency, memory, UI filter latency, and CSV export behavior.

## AL. Release-blocker rubric

Use this when a test fails near release. It keeps waivers explicit and prevents
"docs-only" exceptions from hiding user-impacting bugs.

| Severity | Blocks release? | Examples |
|---|---|---|
| Critical | Always | wrong compliance score, foreign suite/remediation shown as owned, XSS, uninstall leaves console broken, operator crash loop |
| High | Usually | install/upgrade failure, RBAC write control enabled for read-only user, remediation warning missing, Degraded not set for persistent storage/install failure |
| Medium | Case-by-case with owner/date | non-critical UI layout issue, missing screenshot refresh, one unsupported topology failing, known vendor chunk warning growth |
| Low | No, track | copy typo, docs clarification, non-user-visible test refactor |

Waiver template:

```text
Test-plan item:
Observed failure:
User impact:
Affected versions/topologies:
Reason release can proceed:
Owner:
Fix-by date:
Release note needed: yes/no
```

## AM. Regression intake template

When a bug is found, add a row and fill this out before fixing it. The row can
move to `[x]` only after the test lands.

```text
Bug:
Smallest failing fixture:
Cheapest harness:
Expected observable behavior:
Foreign/negative case:
Release-blocker severity:
Regression test name:
```

## AN. Concurrent humans (multi-actor races)

Two operators, one cluster, no coordination. These catch optimistic-lock and
UI-stale-state bugs that single-actor tests never see.

- [ ] **Admin A toggles STIG on while Admin B toggles STIG off**: one patch
      loses on `test` of `spec.profiles`; loser sees a clear conflict message,
      winner's set is the source of truth after refresh.
- [ ] **Admin A sets schedule invalid while Admin B sets auto-apply Automatic**:
      both writes apply or one conflicts; ScanSetting never ends with garbage
      cron; auto-apply still matches the surviving CR.
- [ ] **Admin A applies remediation while Admin B unapplies the same one**:
      final `spec.apply` is one of the two; no double-modal stuck open; CO
      remediations stay consistent.
- [ ] **Admin A deletes ClusterBaseline while Admin B clicks Rescan**: rescan
      fails gracefully (NotFound/Forbidden); delete still removes finalizer;
      no orphan rescan storm.
- [ ] **Admin A sets console Removed while Admin B has the Compliance page
      open**: page degrades to missing plugin / blank route; no infinite
      console error loop; re-enable restores without browser restart.
- [ ] **Two browsers, different users, same profile switch**: watch updates both
      UIs; neither shows a switch state that disagrees with the live CR for
      more than one watch tick after settle.
- [ ] **CLI `oc patch` races console Profiles tab**: console does not silently
      overwrite CLI changes without the `test` op failing.
- [ ] **must-gather while reconcile is hot**: gather completes; operator does
      not deadlock on shared locks (there should be none beyond API).

## AO. Dark UX & "helpful" footguns

Cases where the UI is "working" but trains admins to do the wrong thing.

- [ ] **Score 100 with hundreds of MANUAL**: Overview looks perfect; Manual
      slice or profile cards still force the human to notice residual work.
- [ ] **Score high, Degraded True (storage)**: green-ish score does not hide
      the Degraded banner; severity of banner ≥ score color optimism.
- [ ] **Next scan "—" with InvalidSchedule**: user is not told "scans stopped"
      if last-good cron is still firing; copy should say schedule invalid /
      using last good, not "no schedule".
- [ ] **Rescan succeeds, score unchanged for hours**: UI does not imply rescan
      failed; optional "last rescan requested at" is future work; at least no
      false error toast.
- [ ] **Filter URL shared with colleague without access**: they see empty or
      forbidden, not a crash; no leak of check titles in the error path.
- [ ] **Deep link to `tp-missing` filter**: empty table, not "all results".
- [ ] **Donut with a single tiny FAIL slice**: slice remains clickable/visible
      (not sub-pixel invisible); legend still lists Fail (n).
- [ ] **History trend with two points equal score**: chart still renders (not
      "need variance"); axis labels do not collapse into one unreadable tick.
- [ ] **Remediation Apply on something already Applied**: button shows Unapply;
      no second Apply that no-ops confusingly.
- [ ] **Profiles tab all switches on**: still can turn one off; turning the
      last built-in off is allowed and disables scanning when no tailored
      profiles remain (not disabled, not a silent no-op).

## AP. Compliance content & OpenSCAP surprises

The content image is not under our control; our product must not lie when
content is weird.

- [ ] **Profile renamed upstream** (`ocp4-cis` → something else): binding
      references missing Profile; score empty or CO error; condition/message
      not "healthy 100".
- [ ] **Rule ID stable, title text changes**: Results sort/filter by status
      still work; CSV gets new title; no key-by-title assumptions.
- [ ] **Duplicate check names across profiles**: table keys by object name +
      suite; no React key collision wiping rows.
- [ ] **Result status string with unexpected casing** (`pass` vs `PASS`): tally
      ignores or normalizes; document which; UI shows raw label.
- [ ] **Severity value outside enum** (`critical`, empty, `CRITICAL`): filter
      still lists known severities; row still visible under "unknown" or raw.
- [ ] **Instructions field multi-megabyte**: modal scroll works; export may
      omit instructions today (only name/title/status/severity); document.
- [ ] **NOT-APPLICABLE storm** (entire node profile N/A on non-RHCOS): score
      from platform profile only; donut not empty-looking "success".
- [ ] **TailoredProfile that disables every rule**: suite completes with zero
      countable results; score nil for that bucket; global score may still
      compute from other profiles.
- [ ] **Content image pull failure**: scans ERROR; we do not keep last score
      forever without showing scan failure somewhere (UI and/or conditions).

## AQ. Disaster recovery & backup

- [ ] **Restore etcd from backup taken mid-scan**: ClusterBaseline generation
      and conditions converge; no permanent Progressing from stale observed
      generation confusion.
- [ ] **Restore CR without status**: operator rebuilds status on next
      reconcile; score may be nil until results listed.
- [ ] **Restore CR with future LastScanTime** (clock was wrong at backup):
      history no-rewind / future endTimestamp rules prevent permanent freeze;
      document recovery (`oc patch --subresource=status` or wait).
- [ ] **Backup tool excludes status**: same as empty status rebuild.
- [ ] **Disaster: delete openshift-compliance NS**: CRDs/results gone; we clear
      stale score; Available False; CO reinstall path (Automatic) recovers.
- [ ] **Disaster: delete openshift-baseline-security NS with finalizers**:
      namespace terminating stuck? Plugin NS delete vs cluster-scoped CR;
      document order (delete CR first).
- [ ] **Velero / OADP restore of only plugin Deployment**: operator re-owns
      and rewrites image/env from RELATED_IMAGE_*.

## AR. Negative-space testing ("prove it does not…")

Every bullet is a forbidden behavior. Easier to review than positive lists.

- [ ] Does **not** uninstall the Compliance Operator on ClusterBaseline delete.
- [ ] Does **not** prune ScanSettingBindings it does not own.
- [ ] Does **not** count foreign suite CheckResults in score.
- [ ] Does **not** show foreign remediations in the Remediations tab.
- [ ] Does **not** set Available=True when ComplianceOperatorReady is False.
- [ ] Does **not** set Progressing=True for NotInstalled / ConsoleMissing /
      ImageMissing / CRDsMissing / CSVFailed / InvalidSchedule / Unavailable.
- [ ] Does **not** rewind LastScanTime when a profile is removed.
- [ ] Does **not** write an invalid cron string into ScanSetting.schedule.
- [ ] Does **not** serve plugin metrics or API tokens from the nginx pod.
- [ ] Does **not** put plaintext HTTP on the plugin Service (9443 TLS only).
- [ ] Does **not** expose insecure metrics on non-loopback without auth.
- [ ] Does **not** render check descriptions as HTML.
- [ ] Does **not** use client-side global score math that disagrees with status.
- [ ] Does **not** require cluster-admin to *view* compliance if viewer role
      is bound (document exact verbs).
- [ ] Does **not** leave our name in `consoles…/cluster.spec.plugins` after
      successful finalizer cleanup.

## AS. Event, log & audit signal quality

- [ ] **Reconcile error** logs include namespace/name and error cause; no
      secret values from serving-cert files.
- [ ] **No spam**: steady-state happy path does not log every minute at Info
      for "reconciled" (V(1) is fine); warn on persistent Degraded transitions.
- [ ] **Kubernetes events** (if any are emitted later): reason strings stable
      for alert routing; until then document "status conditions are the API".
- [ ] **API audit**: profile toggle and remediation apply appear as patches from
      the user, not the operator SA impersonating.
- [ ] **Operator SA** does not get unexpected cluster-admin; impersonation not
      used.
- [ ] **Correlation**: must-gather output enough to pair a Degraded reason with
      a log line timestamp within 1 minute.

## AT. Empty, zero, max, and "boring" boundaries

Classic boundary table. Automate as table-driven unit tests where possible.

| Input | Expected |
|---|---|
| 0 profiles (admission) | accept; scanning disabled when tailored also empty |
| 1 profile | ok; UI may clear the last profile to disable scanning |
| 8 profiles | ok; score pools all |
| 0 tailored | status.tailoredProfiles empty/nil |
| 1 tailored, 0 built-in | accept; tailored-only scan (empty profiles allowed) |
| history 0 points | no trend card |
| history 1 point | no trend card (need >1) |
| history 30 points | cap; 31st drops oldest |
| history 31st with same timestamp refresh | no growth past 30 |
| pass=0,fail=0 | score nil |
| pass=0,fail=1 | score 0 |
| pass=1,fail=0 | score 100 |
| pass=2,fail=1 | score 66 (floor) |
| schedule `""` | default daily 01:00 |
| schedule invalid | Degraded; last-good cron kept |
| plugin replicas ready 0 | Waiting / Unavailable by grace |
| plugin replicas ready 1 of 2 | Deployed (ReadyMin) |
| plugin replicas ready 2 of 2 | Deployed |

- [ ] Encode this table as `TestBoundaries_*` unit tests with one assertion
      per row (or subtests).

## AU. Feature-flag & kill-switch matrix

| Knob | On | Off / unset | Test |
|---|---|---|---|
| `installComplianceOperator=Automatic` | creates Sub | — | [x] unit |
| `installComplianceOperator=Manual` | never creates Sub | — | [x] unit |
| `BASELINE_SECURITY_SKIP_DEFAULT_CR=true` | no default CR | creates default | [ ] |
| `console.managementState=Removed` | teardown plugin | Managed deploy | [x] partial |
| `remediation.apply=Automatic` | ScanSetting auto flags | Manual false | [x] unit |
| `RELATED_IMAGE_CONSOLE_PLUGIN` empty | ImageMissing | deploy image | [x] unit |
| `complianceCatalogSource` custom | Sub.source override | redhat-operators | [x] unit |
| metrics `--metrics-bind-address=0` | metrics off | :8443 | [ ] |
| metrics insecure + non-loopback | forced secure | — | [x] addr class |

- [ ] One integration test file that walks every knob and asserts the primary
      side effect (table-driven).

## AV. "Time travel" operator scenarios

- [ ] **Reconcile delayed 24h** (leader stuck): NextScanTime in the past is
      acceptable; score still from last results; no crash on negative
      duration math.
- [ ] **Scan endTimestamp older than CR creation**: still counts if suite
      owned; history may append once.
- [ ] **Multiple scans same suite, different endTimestamps**: latest wins for
      LastScanTime; older does not append after newer.
- [ ] **Results from suite deleted hours ago still in etcd**: if suite no
      longer in ownedSuites, results ignored even if endTimestamp is newest.
- [ ] **PVC CreationTimestamp after "now"** (clock skew): age check does not
      Degrade (negative age); when clock catches up, normal rules apply.

## AW. Comparison contracts (golden snapshots)

- [ ] **Golden status JSON** for a fixed set of CheckResults (cis only,
      known pass/fail/manual/info/skip/inconsistent mix): compare
      `status.profiles`, score, and conditions reasons (not timestamps).
- [ ] **Golden ScanSetting** after reconcile with Automatic remediation:
      autoApply/autoUpdate true, roles worker+master, rotation 3.
- [ ] **Golden relatedObjects** for profiles [cis,e8] + tailored [custom]:
      sorted binding names exactly `baseline-cis`, `baseline-e8`,
      `baseline-tp-custom`.
- [ ] **Golden CSV export** for a fixture of 3 results including formula-like
      title: file bytes match (modulo `\r\n` policy).
- [ ] Refresh goldens only with explicit `UPDATE_GOLDEN=1` and PR review.

## AX. Composability with the OpenShift platform

- [ ] **Console dynamic plugin SDK upgrade**: plugin builds against the
      supported SDK range; HorizontalNav / useK8sWatchResource still work.
- [ ] **PatternFly major bump**: visual regression on Overview cards and
      modals (screenshot diff optional).
- [ ] **Cluster with Monitoring disabled**: no PrometheusRule install; operator
      still healthy; metrics endpoint still scrapable manually.
- [ ] **Cluster monitoring scrape**: openshift-* install NS carries the
      openshift.io/cluster-monitoring label so platform Prometheus scrapes the
      ServiceMonitor (UWM never scrapes openshift-* namespaces).
- [ ] **Hosted control plane / HyperShift**: operator in management vs guest
      (document support boundary; skip if unsupported).
- [ ] **ROSA / managed offering constraints**: if SCCs or webhooks block
      operator, document unsupported rather than silent fail.

## AY. Data integrity under prune & re-add

- [ ] **Remove cis, wait, re-add cis**: new binding created; old results may
      still exist with same suite label; score includes them again (CO may
      also rescan); no duplicate bindings.
- [ ] **Rename tailored in spec** (remove old name, add new): old suite
      results excluded; new suite empty until scan; history LastScanTime not
      rewound solely due to rename.
- [ ] **Switch install Manual → Automatic with CO already present**: no second
      Subscription fight; Ready stays True.
- [ ] **Switch Automatic → Manual**: existing Subscription not deleted (we
      never delete CO Sub); document.
- [ ] **Remediation Manual → Automatic → Manual**: ScanSetting flags follow;
      applied remediations not bulk-unapplied by the toggle alone.

## AZ. Humorless "could a smart intern break it?" checklist

Ten minutes, no mercy. If any pass, file AM tickets.

- [ ] Paste a 10k-character string into a TailoredProfile name via raw YAML
      (should fail admission at 51).
- [ ] Set `profiles: [cis, cis]` (duplicate; should fail UniqueItems).
- [ ] Set `metadata.name: Cluster` (wrong case; should fail CEL).
- [ ] Point `complianceCatalogSource` at a name that does not exist; install
      stuck with readable condition, not CrashLoop.
- [ ] `oc delete csv -n openshift-compliance --all` while Automatic; recover or
      Degrade clearly.
- [ ] Fill Results search/filter with only spaces / emoji; no exception.
- [ ] Export CSV, open in spreadsheet, confirm formula cells are text.
- [ ] Apply node remediation without reading modal; modal still forced a
      confirm click (no single-click Apply on node type).
- [ ] Scale plugin deploy to 0 replicas manually; operator heals to 2 or
      reports Unavailable after grace.
- [ ] Annotate ConsolePlugin with random junk; CreateOrUpdate still converges
      owned spec fields.

---

## Priority gaps

Rough order: high value / low cost first.

1. **R / AT**: table-driven rollup + boundary tables (locks conditions & score).
2. **N**: API admission dry-run for singleton name, profile uniqueness,
   tailored name constraints.
3. **I / AA**: promtool tests for score sentinel, score=80 boundary, fail
   clear, HA-safe exprs.
4. **H/F / AH / AO**: `ClusterScoreItem` branches, score 0 vs not scanned,
      Degraded+high score banner dominance.
5. **K / AF / AR**: garbage suite labels + ownership exclusivity + negative-space
   suite as unit asserts.
6. **D**: stuck Subscription / deleted installedCSV bounded conditions.
7. **Y / AN**: rescan double-click + multi-admin profile toggle conflict.
8. **Q**: axe + keyboard smoke on the four tabs and two modals.
9. **U**: XSS/SSRF href tests + metrics 401 without token.
10. **AE / AK**: day-0 admin + view-only auditor journeys / charters.
11. **AW**: golden status + relatedObjects snapshots (cheap, high regression value).
12. **C**: tailored + built-in same base coexistence e2e.
13. **AC**: reconcile benchmark budget for 1k/10k check results.
14. **AD / AV**: faulty-client injection + time-travel endTimestamps.
15. **AU**: kill-switch matrix one-file integration test.
16. **W**: generated drift gate + Playwright artifact policy in CI.
17. **X**: install/uninstall guide smoke and alert runbooks.
18. **A2 / AP**: multi-node NotReady + content-image surprises.
19. **AQ**: delete openshift-compliance NS recovery path.
20. **AZ**: smart-intern checklist as a release-candidate script.

---

## How to use this plan

- **Before a release**: walk Priority gaps 1–8; anything still open needs a
  waiver in the release notes with owner and follow-up date.
- **For PR review**: require Tier 0 for all changes; add Tier 1 when generated
  artifacts, Dockerfiles, manifests, or frontend code changed.
- **After a bugfix**: add a regression case here with `[x]` and the test
  name, even if the scenario feels "too specific". Prefer a row under K, R,
  Y, AA–AZ when the bug was environmental, multi-actor, or adversarial.
- **Adversarial review rounds**: invent in K, R, T, U, Y, AA–AZ first; only
  re-open A/D when the score formula or CSV selection actually changed.
- **Negative-space day**: once a month run AR as a checklist against main;
  any violation is a release-blocker candidate under AL.
- **Coverage myth-busting**: Playwright screenshots are not assertions; promote
  them to `[x]` only when a hard expect exists.
- **Persona days**: once per quarter run AE journeys on a live cluster and
  refresh screenshots + known-issue ledger.
- **Exploratory runs**: use AK and AZ charters when behavior changes; convert
  any surprising result into an AM regression entry before patching.
- **Golden updates**: AW snapshots change only with `UPDATE_GOLDEN=1` and
  explicit PR justification.
- **Release candidates**: Tier 2, Tier 4, and Tier 5 failures block release
  unless an AL waiver states exact user impact and follow-up owner.
- **Budget negotiation**: if AC budgets fail, either fix the regression or
  consciously raise the budget in this file with a reason (never silent).
