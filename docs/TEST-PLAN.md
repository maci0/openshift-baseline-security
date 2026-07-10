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
| Zero-config onboarding creates useful scans | B, D, O, X | default CR creation, Subscription/ScanSettingBinding creation, install-guide smoke |
| Compliance score is trustworthy | A, A2, K, R, S | pooled score tests, status-value exclusions, history/LastScanTime tests, malformed input tests |
| Console is safe for read-only admins | F, G, H, Q, U, AE | RBAC disabled states, no-baseline/error branches, keyboard/modal checks |
| Remediation UX is explicit about blast radius | G, U, AE | node-remediation warning, unapply/no-warning path, auto-apply patch round trip |
| Operator behaves like an OpenShift component | D, J, M, O, R, T | condition matrix, HA/leader paths, finalizer cleanup, dependency-CRD absence |
| Release artifacts install and upgrade cleanly | L, N, P, W, AJ | CRD drift/admission checks, bundle validation, image smoke, OLM upgrade ladder |
| Admins can troubleshoot failures | E, I, T, X | PVC degraded path, promtool rules, runbook smoke, must-gather smoke |

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
- [ ] **Large-benchmark dominance**: a profile with many checks outweighs a
      small one in the pooled score; assert per-profile cards still show each
      profile's own ratio independently.
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
- [ ] **INFO-only profile**: score nil, Overview donut still shows an Info
      slice, Results filter for INFO returns rows.
- [ ] **ERROR-only profile**: score nil, Error slice visible, no false "0 / 100"
      success color on the dashboard item.
- [ ] **Single FAIL among thousands of PASS**: score floors (not rounds) to
      match Go integer division; Overview per-profile badge uses the same
      floor (`Math.floor` vs operator `int32` truncate).
- [ ] **Per-profile card vs global score mismatch is intentional**: document and
      assert that a profile at 100% can coexist with a global score of 50 when
      another profile is large and failing.

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
- [x] Cannot remove the last profile (jest `toggledProfiles` rejects empty).
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
- [ ] **Metrics label `tp:<name>`**: publishMetrics emits tailored series
      (`TestPublishMetrics` partial; assert `tp:` prefix explicitly).

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
- [x] **Per-status series including info/inconsistent** and tailored `tp:`
      prefix (`TestPublishMetrics`).
- [x] **Alert expressions are HA-safe**: `max()` / `max by (profile,status)` in
      `config/prometheus/prometheusrule.yaml` (review; add promtool).
- [ ] **PrometheusRule** `ComplianceScoreLow` / `ComplianceChecksFailing` fire
      against synthetic metric values (promtool rule test, no cluster needed).
- [ ] **Profile removed from spec**: old `{profile,status}` series do not linger
      after `publishMetrics` Reset (unit assert CollectAndCount / label set).
- [ ] **ServiceMonitor scrape**: with UWM + scraper SA token, metrics endpoint
      returns 200 and includes custom gauges (live or kind).

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
- [ ] **Profiles validation**: duplicate profiles, empty profile list, and
      unknown profile keys are rejected by CRD schema.
- [ ] **TailoredProfiles validation**: duplicate, empty, too-long, uppercase,
      underscore, and path-like values are rejected; DNS-subdomain-like names
      are accepted; max length 51 leaves room for `baseline-tp-` suite labels.
- [ ] **Defaulting**: omitted spec fields default as documented
      (`profiles: [cis]`, daily schedule, Automatic install, Managed console,
      Manual remediation) in a real API server dry-run.
- [ ] **Status schema completeness**: generated CRD includes all count fields
      (`pass`, `fail`, `manual`, `info`, `error`, `inconsistent`,
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
| Scan storage Pending >2m | True* | False | True (ScanStoragePending) | [x] *if CO+scan cfg already True |
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

- [ ] **Score exactly 80**: `ComplianceScoreLow` does not fire (`< 80`, not
      `<=`); promtool case.
- [ ] **Score sentinel -1**: never fires ComplianceScoreLow even when scraped
      from multiple pods.
- [ ] **Flapping score 79↔81**: `for: 30m` prevents page storms; document how
      to validate with promtool `eval_time` steps.
- [ ] **Fail count goes 5→0**: ComplianceChecksFailing clears after `for: 1h`
      window; no sticky alert from a demoted leader process (process exit on
      leader loss is the safety net; assert in runbook).
- [ ] **Profile removed while fails remain on disk**: gauges Reset; alert series
      for that profile disappear after scrape interval.
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

---

## Priority gaps

Rough order: high value / low cost first.

1. **R**: table-driven rollup matrix (locks Available/Progressing/Degraded).
2. **N**: API admission dry-run for singleton name, profile uniqueness,
   tailored name constraints.
3. **I / AA**: promtool tests for score sentinel, score=80 boundary, fail
   clear, HA-safe exprs.
4. **H/F / AH**: `ClusterScoreItem` branches including **score 0 vs not scanned**.
5. **K / AF**: garbage suite labels/statuses + ownership exclusivity property.
6. **D**: stuck Subscription / deleted installedCSV bounded conditions.
7. **Y**: rescan double-click + nil annotations patch against API.
8. **Q**: axe + keyboard smoke on the four tabs and two modals.
9. **U**: XSS/SSRF href tests + metrics 401 without token.
10. **AE**: day-0 admin + view-only auditor Playwright journeys.
11. **C**: tailored + built-in same base coexistence e2e.
12. **AC**: reconcile benchmark budget for 1k/10k check results.
13. **AD**: faulty-client injection for Status conflict and NoKindMatch.
14. **W**: generated drift gate + Playwright artifact policy in CI.
15. **X**: install/uninstall guide smoke and alert runbooks.
16. **A2**: node-added-mid-cycle + NotReady multi-node paths.
17. **S**: DST/UTC NextScanTime documentation test.
18. **AK**: first-hour admin and skeptical auditor exploratory charters.
19. **AL/AM**: waiver and regression intake templates in release checklist.
20. **T / AB**: foreign binding survival + orphan GC documentation tests.

---

## How to use this plan

- **Before a release**: walk Priority gaps 1–8; anything still open needs a
  waiver in the release notes with owner and follow-up date.
- **For PR review**: require Tier 0 for all changes; add Tier 1 when generated
  artifacts, Dockerfiles, manifests, or frontend code changed.
- **After a bugfix**: add a regression case here with `[x]` and the test
  name, even if the scenario feels "too specific". Prefer a row under K, R,
  Y, AA, AB, AD, or AH when the bug was environmental or adversarial.
- **Adversarial review rounds**: invent in K, R, T, U, Y, AA–AJ first; only
  re-open A/D when the score formula or CSV selection actually changed.
- **Coverage myth-busting**: Playwright screenshots are not assertions; promote
  them to `[x]` only when a hard expect exists.
- **Persona days**: once per quarter run AE journeys on a live cluster and
  refresh screenshots + known-issue ledger.
- **Exploratory runs**: use AK charters when behavior changes; convert any
  surprising result into an AM regression entry before patching.
- **Release candidates**: Tier 2, Tier 4, and Tier 5 failures block release
  unless an AL waiver states exact user impact and follow-up owner.
- **Budget negotiation**: if AC budgets fail, either fix the regression or
  consciously raise the budget in this file with a reason (never silent).
