# Roadmap

Status of **openshift-baseline-security**. Current release **0.2.1**
(cluster-scoped `ClusterBaseline` API, string-enum spec). Verified end to end
on a single-node OpenShift 4.22 cluster: operator + console plugin installed
via OLM, CIS scan scored, both e2e suites green, five adversarial review
rounds converged clean.

Legend: `[x]` done · `[ ]` planned · **(H/M/L)** rough value.

## Done

### Core
- [x] Design spec, `PATTERNS.md`, `STANDARDS.md`; repo layout mirroring
      OpenShift components; Apache-2.0.
- [x] Operator (kubebuilder go/v4) reconciling `ClusterBaseline/cluster`:
      installs/adopts the Compliance Operator, owns ScanSetting +
      per-profile ScanSettingBindings, deploys the console plugin,
      aggregates score + 30-entry history into status.
- [x] String-enum spec per api-conventions.md
      (`installComplianceOperator`, `console.managementState`,
      `remediation.apply`); no booleans.
- [x] OpenShift-style rollup conditions (Available / Progressing /
      Degraded) plus detail conditions, with `observedGeneration`.
- [x] Default-create `ClusterBaseline/cluster` on start
      (`BASELINE_SECURITY_SKIP_DEFAULT_CR=true` to opt out).
- [x] Finalizer cleanup; prune deselected-profile bindings; tolerate the
      Console capability being disabled (NoKindMatch) on every path.

### Console plugin (React 18 / PatternFly 6 / SDK 4.22)
- [x] Compliance page under **Administration**: Overview, Results,
      Remediations, Profiles (HorizontalNav), rescan button.
- [x] Composition donut (Pass/Fail/Manual slices) + per-profile score badge.
- [x] Results: VirtualizedTable with status/severity/profile filters,
      human-readable titles, detail modal (description, instructions, link
      to the raw ComplianceCheckResult), CSV export of the filtered view.
- [x] Remediations: gated apply (confirmation modal), auto-apply switch.
- [x] `useAccessReview` gating on every write; TLS via service-serving cert.

### Observability
- [x] Prometheus metrics on the secure endpoint:
      `baseline_security_compliance_score`,
      `baseline_security_checks{profile,status}`.
- [x] PrometheusRule: `ComplianceScoreLow`, `ComplianceChecksFailing`.
- [x] Aggregated `baseline-security-viewer` / `-admin` ClusterRoles.

### Packaging & quality
- [x] OLM bundle + FBC catalog (`make bundle` validates); 0.2.1 replaces
      0.2.0 in the upgrade graph; images/tools digest-pinned.
- [x] CI (unit, fuzz, lint, generated-file drift, image builds).
- [x] E2E: operator Go (`make test-e2e`) + console Playwright
      (`yarn test-e2e`, also regenerates `docs/screenshots`).
- [x] Full OLM install + upgrade verified on the SNO via the internal
      registry (no quay dependency).

## Planned

### Next up
- [ ] **(H)** Push versioned images + bundle + catalog to quay.io; submit to
      community-operators once stable. Needs a quay robot token.

### Observability
- [ ] **(H)** Console dashboard card surfacing the score on the cluster
      Overview (`console.dashboards/*` extension; SPEC §4.3).
- [ ] **(L)** Trend dashboard from the score metric.

### UI / UX
- [ ] **(M)** Show the rendered remediation object / MachineConfig diff on
      the Remediations tab.
- [ ] **(L)** Distinct "scan in progress" empty state; loading skeletons.
- [ ] **(L)** PDF report; severity-weighted score option.

### Operator / API
- [ ] **(M)** Watch compliance CRs via a dynamic informer once the CRDs
      exist, replacing the 1-minute poll.
- [ ] **(M)** `TailoredProfile` support (enable/disable individual rules;
      also the path to an NSA/CISA K8s-hardening mapping onto existing rules,
      since the Compliance Operator ships no dedicated NSA profile).
- [ ] **(M)** `relatedObjects` in status + a must-gather script.
- [ ] **(L)** Node-remediation apply with MachineConfigPool pause awareness.
- [ ] **(L)** Scheduled next-run time in status.

### Delivery
- [ ] **(L)** Helm chart for non-OLM installs.

## Productization

Rename the API group under an `openshift.io` domain, add a
`registry.ci.openshift.org` build variant, onboard ci-operator, split the
plugin into its own repo, and file an enhancement proposal referencing
`docs/SPEC.md`.

## External

- vmetal-openshift lvms playbook bug:
  [maci0/vmetal-openshift#1](https://github.com/maci0/vmetal-openshift/issues/1).
