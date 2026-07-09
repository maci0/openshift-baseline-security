# OpenShift Baseline Security

Design specification. Status: draft. Targets OpenShift Container Platform 4.22.

## 1. Summary

OpenShift Baseline Security is an OLM operator plus an admin-console dynamic
plugin that gives every OpenShift cluster an out-of-the-box compliance
baseline: install it, and the cluster continuously benchmarks itself against
the CIS OpenShift Benchmark (and optionally PCI-DSS, NIST 800-53, DISA STIG,
and the other profiles Red Hat already ships), with results rendered natively
in the OpenShift web console.

It does not implement a scanner. The scan engine is the Red Hat
**Compliance Operator** (OpenSCAP + ComplianceAsCode content), which is
included with every OpenShift subscription at no extra cost. What is missing
today, and what this project adds, is:

1. **Zero-configuration onboarding**: one CR (or just installing the operator)
   gets you a scheduled CIS scan with sane defaults. Today users must learn
   ProfileBundle/ScanSetting/ScanSettingBinding YAML first.
2. **A console UI**: today there is no compliance UI in the OpenShift console
   at all. Results are CLI-only (`oc get compliancecheckresults`) unless you
   buy Red Hat Advanced Cluster Security, whose compliance UI is the paid
   answer. This project is effectively the single-cluster, baseline slice of
   ACS compliance, built into the cluster console.

## 2. Motivation and landscape

Facts established from current (2026-07) Red Hat sources:

| Fact | Source |
|---|---|
| Compliance Operator is free with all OCP versions; ACS is a separate paid SKU | redhat.com blog "A guide to OpenShift Compliance Operator best practices" |
| Compliance Operator v1.8.2 (2026-02), upstream `github.com/ComplianceAsCode/compliance-operator`, ships via `redhat-operators` OLM catalog, channel `stable`, namespace `openshift-compliance` | GitHub releases, RHSA-2026:1859 |
| No compliance UI in the OpenShift console; no official console plugin exists; results consumption is CLI-first or via ACS | OpenShift docs, openshift org survey |
| ACS 4.8 "Schedules and Coverage" (compliance v2) is GA and is Red Hat's official graphical compliance answer; it drives the same Compliance Operator on secured clusters | RHACS 4.8 release notes |
| Console dynamic plugins are the sanctioned console extension mechanism (used by ODF, monitoring, netobserv, kubevirt) | openshift/enhancements dynamic-plugins |

Gap: a cluster admin without an ACS subscription has no graphical way to
answer "is this cluster CIS-compliant?". The engine and the content are
already on the cluster (or one Subscription away); only orchestration
defaults and presentation are missing. Both are cheap. That is this project.

## 3. Goals and non-goals

### Goals (v1)

- G1: Single-action enablement of baseline scanning: installing the operator
  and creating one `ClusterBaseline` CR (a default is offered) yields a
  scheduled `ocp4-cis` + `ocp4-cis-node` scan.
- G2: Profile selection: expose the full Red Hat profile catalog
  (CIS, PCI-DSS, NIST 800-53 moderate/high, DISA STIG, NERC CIP, ACSC E8,
  BSI) as a checkbox list, not YAML.
- G3: Console UI (admin perspective): compliance dashboard (score, severity
  breakdown, per-profile status), filterable check-result list with rule
  detail (description, instructions, severity), scan status and "rescan now".
- G4: Compliance Operator lifecycle: install it automatically if absent,
  adopt it if present, never fight an existing installation.
- G5: Ship as an OLM bundle installable from a catalog source; repo layout
  and build conventions match openshift org components so productization is
  a re-namespace, not a rewrite.

### Stretch goals (implemented)

- S1: **Remediation apply from the UI**: one-click `spec.apply: true` on a
  `ComplianceRemediation`, with an explicit warning that node remediations
  render into MachineConfigs and reboot nodes; "auto-apply" toggle mapping
  to `ScanSetting.autoApplyRemediations`. (MachineConfigPool pause awareness
  is future work.)
- S2: **Trend and score history**: persist per-scan score snapshots
  (operator writes a compact history into the `ClusterBaseline` status or a
  ConfigMap ring buffer; long-term via the Compliance Operator's Prometheus
  metrics) and render a trendline on the dashboard.

### Non-goals

- No scanner, no benchmark content authoring. Content is
  ComplianceAsCode, consumed as the content images Red Hat builds.
- No multi-cluster / fleet view. That is ACS and ACM territory. This is
  deliberately "this cluster" only.
- No vulnerability (CVE) scanning. Different problem, different tooling
  (Quay/Clair, ACS).
- No custom rule authoring UI (Compliance Operator 1.8 CustomRule/CEL is
  Tech Preview; revisit later).

## 4. Architecture

Two deliverables, one repo (split-ready, see §9):

```
                       ┌─────────────────────────────────────────┐
                       │ OpenShift web console                   │
                       │  ┌───────────────────────────────────┐  │
                       │  │ baseline-security console plugin  │  │
                       │  │ (React/PF6, served by nginx pod)  │  │
                       │  └───────────────┬───────────────────┘  │
                       └──────────────────┼──────────────────────┘
                                          │ user's own bearer token,
                                          │ k8s API via console proxy
                                          ▼
 ┌──────────────────────┐   creates   ┌──────────────────────────────┐
 │ baseline-security-   │────────────▶│ compliance.openshift.io CRs  │
 │ operator             │             │ ScanSetting, ScanSetting-    │
 │                      │   watches   │ Binding, ComplianceSuite,    │
 │ reconciles:          │◀────────────│ ComplianceScan, CheckResult, │
 │  - ClusterBaseline   │             │ Remediation                  │
 │  - plugin Deployment │             └──────────────┬───────────────┘
 │  - ConsolePlugin CR  │                            │ reconciled by
 │  - CO Subscription   │             ┌──────────────▼───────────────┐
 └──────────────────────┘             │ Compliance Operator          │
                                      │ (Red Hat, openshift-         │
                                      │  compliance ns, OpenSCAP)    │
                                      └──────────────────────────────┘
```

### 4.1 Operator (`baseline-security-operator`)

Go, kubebuilder go/v4 layout (operator-sdk CLI is deprecated by Red Hat;
kubebuilder is the current scaffolder, OLM bundle targets kept in the
Makefile). Runs in namespace `openshift-baseline-security` (suggested).

Responsibilities:

1. **Compliance Operator lifecycle** (G4). On reconcile of `ClusterBaseline`:
   - If the `compliance-operator` CSV is present in any namespace: adopt,
     record version in status, touch nothing of its config.
   - If absent and `spec.installComplianceOperator: true` (default): create
     `openshift-compliance` Namespace, OperatorGroup, and a Subscription to
     package `compliance-operator`, channel `stable`, in the
     `redhat-operators` catalog (catalog source name configurable for
     disconnected/OKD clusters, where the ghcr.io upstream catalog is used).
   - An OLM bundle `dependencies.yaml` on the compliance-operator package is
     deliberately NOT used: OLM v0 resolves dependencies into the dependent's
     namespace/OperatorGroup, but compliance-operator expects its own
     namespace. Explicit Subscription reconciliation is the reliable path.
     Revisit when OLM v1 lands.
2. **Baseline defaults** (G1, G2). Own a `ScanSetting`
   (`baseline`, schedule from `spec.schedule`, default `0 1 * * *`, 1Gi PV,
   rotation 3) and one `ScanSettingBinding` per selected profile set,
   mapping the CR's profile keys to real Profile names
   (`cis` → `ocp4-cis` + `ocp4-cis-node`, `stig` → `ocp4-stig` +
   `ocp4-stig-node` + `rhcos4-stig`, etc.).
3. **Console plugin deployment** (G3): nginx Deployment (2 replicas,
   service-serving-cert TLS, hardened securityContext + probes), Service,
   `ConsolePlugin` CR in namespace `openshift-baseline-security` (created if
   missing), and appending the plugin name to
   `consoles.operator.openshift.io/cluster` `spec.plugins` (removed on CR
   deletion via finalizer, or when `spec.console.enabled` is false).
4. **Status aggregation**: poll `ComplianceCheckResult`s labeled with
   `compliance.openshift.io/suite=baseline-<profile>` (suite name equals the
   owned ScanSettingBinding). Foreign CO suites are ignored. Aggregate into
   `ClusterBaseline.status`: per-profile pass/fail/manual/error counts, a
   0-100 score (pass / (pass+fail), MANUAL and NOT-APPLICABLE excluded;
   score is cleared when there are no countable results), lastScanTime,
   history (oldest first, capped at 30), conditions
   (`ComplianceOperatorReady` from CSV phase Succeeded, `ScanConfigured`,
   `ConsolePluginReady`, `Degraded` for owned Pending PVCs). Compliance CRDs
   are not watched at manager start (they may be absent); the controller
   requeues every minute and Owns the plugin Deployment/Service/ConfigMap.
   Deleting ClusterBaseline does **not** uninstall the Compliance Operator.

### 4.2 API: `ClusterBaseline` CRD

Group `baselinesecurity.io/v1alpha1` (neutral OSS domain; productization
would move it under an openshift.io group). Cluster-scoped singleton named
`cluster`, enforced by CEL validation on metadata.name (openshift config CR
convention).

```yaml
apiVersion: baselinesecurity.io/v1alpha1
kind: ClusterBaseline
metadata:
  name: cluster
spec:
  profiles: [cis]              # enum keys: cis, pci-dss, nist-moderate,
                               # nist-high, stig, nerc-cip, e8, bsi
  schedule: "0 1 * * *"        # cron, passed to ScanSetting
  installComplianceOperator: true
  complianceCatalogSource: redhat-operators   # okd/disconnected override
  console:
    enabled: true
  remediation:
    autoApply: false           # ScanSetting autoApplyRemediations/autoUpdateRemediations
status:
  conditions: [...]
  complianceOperatorVersion: 1.8.2
  lastScanTime: "2026-07-09T01:00:00Z"
  score: 87                    # 0-100, pass/(pass+fail)
  profiles:
    - key: cis
      profileNames: [ocp4-cis, ocp4-cis-node]
      pass: 142
      fail: 21
      manual: 9
      error: 0
      notApplicable: 3
  history: []                  # stretch S2: bounded ring, oldest first, max 30
```

### 4.3 Console plugin (`baseline-security-console-plugin`)

Dynamic plugin per `openshift/console-plugin-template` (main / 4.22 line):
`@openshift-console/dynamic-plugin-sdk` 4.22-latest, React 18, PatternFly 6,
TypeScript 5.9, webpack 5 module federation, Yarn 4 (Berry), i18n namespace
`plugin__baseline-security-console-plugin`. **No backend**: all data comes
from the Kubernetes API through the console's proxy using
`useK8sWatchResource` against `compliance.openshift.io/v1alpha1` and
`baselinesecurity.io/v1alpha1` resources, with the logged-in user's own
token. RBAC therefore falls out for free: users see what their role allows
(see §6).

Extension points (exact SDK types):

| Extension | Type | Purpose |
|---|---|---|
| Nav item "Compliance" under Administration | `console.navigation/href` (`perspective: admin`, `section: administration`) | entry point |
| Page `/baseline-security` with HorizontalNav tabs | `console.page/route` | Overview (score, severity, trend), Results, Remediations, Profiles |
| Results tab | (in-page) | virtualized ComplianceCheckResult table: filter by status/severity; detail modal for description + instructions; suite-scoped to baseline bindings |
| Profiles tab | (in-page) | catalog of shipped profiles with enable switches writing `ClusterBaseline.spec.profiles` (`useAccessReview`) |
| Remediations tab | (in-page) | apply/unapply with confirmation; auto-apply toggle |
| Cluster overview card / health item | future | not in v0.1.0 |

Behaviors that write to the cluster:

- **Rescan now**: empty-value annotation `compliance.openshift.io/rescan=`
  on the owned ComplianceScans (documented Compliance Operator mechanism).
- **Profile toggle**: patch `ClusterBaseline.spec.profiles`.
- **Remediation apply (stretch S1)**: patch `ComplianceRemediation.spec.apply`,
  behind a confirmation modal spelling out the MachineConfig/reboot blast
  radius.

All writes go through the user's token; a read-only user gets disabled
buttons (SDK `useAccessReview`), not errors.

## 5. Reused vs built

| Piece | Reuse | Build |
|---|---|---|
| Scan engine | `registry.redhat.io/compliance/openshift-compliance-rhel8-operator` (upstream: `ghcr.io/complianceascode/compliance-operator`) | |
| Benchmark content | `registry.redhat.io/compliance/openshift-compliance-content-rhel8` (upstream: `ghcr.io/complianceascode/k8scontent`) | |
| OpenSCAP scanner image | `registry.redhat.io/compliance/openshift-compliance-openscap-rhel8` (upstream: `ghcr.io/complianceascode/openscap-ocp`) | |
| Scan scheduling, PV storage, result CRs, remediations | Compliance Operator CRDs | |
| Console framework, auth, RBAC, proxy | console dynamic-plugin SDK | |
| Plugin web server | `registry.access.redhat.com/ubi9/nginx-120` | |
| Operator base image | `registry.access.redhat.com/ubi9/ubi-micro` (build: ubi9 go-toolset) | |
| Orchestration + defaults + status aggregation | | `baseline-security-operator` (Go) |
| UI | | `baseline-security-console-plugin` (TS/React) |
| OLM packaging | | bundle + FBC catalog for both-in-one package |

Net-new code is one small controller and one frontend. Everything
security-critical (checks, scanner, remediation content) stays Red Hat-built
and Red Hat-updated.

## 6. Security and RBAC

- Operator ClusterRole: CRUD on `compliance.openshift.io` resources; create
  Namespace/OperatorGroup/Subscription (scoped by resourceNames where OLM
  allows); patch `consoles.operator.openshift.io/cluster`; own CRD full
  access; Deployment/Service/ConfigMap in its own namespace. No secrets
  access, no node access, no exec.
- Plugin: no service account of consequence (nginx serves static files);
  every API call is the user's own token via the console proxy.
- Aggregated ClusterRoles shipped for humans:
  `baseline-security-viewer` (read CRs + check results, bound via
  cluster-reader aggregation label) and `baseline-security-admin`
  (edit ClusterBaseline, annotate scans, and, when S1 lands, patch
  remediations).
- Remediation apply is the only dangerous write in the system and is
  stretch-gated, confirmation-gated, and RBAC-gated three ways.
- Plugin ConsolePlugin CSP directives pinned to self only.

## 7. Toolchain pins (OCP 4.22 line)

Verified against openshift release-4.22 branches (go.mod/package.json of
openshift/kubernetes, openshift/api, openshift/console, openshift/library-go,
ComplianceAsCode/compliance-operator master, and npm dist-tags).

| Tool | Version | Matches |
|---|---|---|
| Go | 1.25 | openshift 4.22 builder (`rhel-9-golang-1.25-openshift-4.22`); compliance-operator master is go 1.25.8 |
| Kubernetes | 1.35 (`k8s.io/*` v0.35.x) | OCP 4.22 kube level (4.21 = 1.34, 4.20 = 1.33) |
| controller-runtime | v0.23.3 | release-0.23 targets k8s 1.35; same pairing as compliance-operator master |
| dynamic-plugin-sdk | 4.22.0 (`4.22-latest` dist-tag) | console 4.22 (SDK major.minor == console version since 4.18) |
| React | ^18.3.1 | console 4.22 frontend |
| PatternFly | ~6.4.x | console 4.22 frontend |
| TypeScript | 5.9.3 | console 4.22 frontend |
| webpack | ^5.107.x | console-plugin-template main |
| Node (build image) | 22 (`ubi9/nodejs-22`) | console 4.22 build image stream |
| Yarn | 4.14 via corepack | console-plugin-template |
| Scaffold | kubebuilder go/v4 layout | operator-sdk CLI deprecated (last shipped in OCP 4.18); note operator-sdk v1.42.3 scaffolds still pin k8s 1.33, hence hand-pinned versions here |

## 8. Packaging and delivery

- **Images**: `quay.io/<org>/baseline-security-operator`,
  `quay.io/<org>/baseline-security-console-plugin`, both multi-stage UBI9
  builds, Dockerfiles at each component root (a `Dockerfile.rhel` variant
  using `registry.ci.openshift.org` builders is added at productization).
- **OLM**: one package `baseline-security-operator`; bundle carries the
  ClusterBaseline CRD, CSV (with `console.openshift.io` related-images for
  the plugin image via `RELATED_IMAGE_CONSOLE_PLUGIN` env, so disconnected
  mirroring works), channel `alpha` → `stable`. File-based catalog (FBC)
  image for a CatalogSource; goal: community-operators submission once v1
  is stable.
- **Install UX**: OperatorHub → install → operator creates a default
  `ClusterBaseline/cluster` if none exists (or console form; decide during
  implementation, default-create is the zero-config goal, opt-out via env).

## 9. Repo layout

Monorepo during incubation, each half shaped exactly like its standalone
openshift counterpart so a later split into two repos (the org's dominant
pattern: plugin repos are always separate from operator repos) is `git mv`:

```
openshift-baseline-security/
├── docs/SPEC.md                    # this document
├── operator/                       # kubebuilder go/v4 shape
│   ├── api/v1alpha1/
│   ├── cmd/main.go
│   ├── internal/controller/
│   ├── config/{crd,rbac,manager,default,samples,manifests}/
│   ├── bundle/                     # OLM bundle (generated)
│   ├── Dockerfile
│   ├── Makefile
│   └── go.mod
├── console-plugin/                 # console-plugin-template shape
│   ├── src/{components,hooks,i18n}/
│   ├── locales/en/
│   ├── console-extensions.json
│   ├── package.json
│   ├── webpack.config.ts
│   └── Dockerfile
├── OWNERS
└── LICENSE                         # Apache-2.0
```

CI at productization: `.ci-operator.yaml` + config in `openshift/release`
per docs.ci.openshift.org onboarding; until then GitHub Actions running the
same Makefile targets (`test`, `lint`, `docker-build`).

## 10. Roadmap

| Milestone | Content | Status |
|---|---|---|
| 0.1 | Operator: CO install/adopt + CIS default binding + status score. Plugin: dashboard + results list. | Done; verified e2e on SNO 4.22.0 (score 96 from 277 CIS results) |
| 0.2 | Full profile catalog (G2), rescan button, OLM bundle + catalog. | Done; OLM install path verified on-cluster. community-operators submission pending |
| 0.3 (S2) | Score history + trendline. | Done (30-entry status ring + trend chart) |
| 0.4 (S1) | Remediation viewing + gated apply, auto-apply toggle. | Done (confirmation modal, useAccessReview gating) |
| Productization | Rename API group to openshift.io namespace, Dockerfile.rhel + ci-operator onboarding, split repos, Red Hat enhancement proposal referencing this spec. | Open |

## 11. Prerequisites

- A default StorageClass. Compliance scans persist raw ARF results to a PVC
  (`ScanSetting.rawResultStorage`, 1Gi); without a default StorageClass the
  PVCs stay Pending and scans hang without any error from the Compliance
  Operator. The operator surfaces this as a `Degraded` condition
  (`ScanStoragePending`) on the ClusterBaseline. Verified on SNO: LVM
  Storage (LVMS) on a spare disk is sufficient.
- Cluster reachability to an OLM catalog carrying `compliance-operator`
  (`redhat-operators` by default, `spec.complianceCatalogSource` to
  override for OKD/disconnected).

## 12. Open questions

Resolved during implementation:

- Default-create: implemented. The operator creates `ClusterBaseline/cluster`
  (CIS) on start when none exists; opt out with
  `BASELINE_SECURITY_SKIP_DEFAULT_CR=true`.
- Nav placement: "Compliance" at the top of the Administration section
  (`insertBefore` cluster-settings).
- MANUAL results: excluded from the score, surfaced as a separate count per
  profile and as a results filter.

Still open:

- OKD support: upstream catalog/content images differ (ghcr.io); the
  `complianceCatalogSource` knob covers the Subscription, content image
  override may also be needed.
