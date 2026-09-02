# Threat model

Last reviewed: 2026-09-02 (against `main` at 0.5.15 plus unreleased tree).
Owner and review cadence are organizational; this file does not name either.

This is the CISO-facing map of what can be attacked, what it costs, and what
the code actually implements. Individual CVEs belong in `CHANGELOG.md` under
**Security**; point-fix work belongs with the code. Re-verify every file
reference on the next pass.

The product is not an internet listener of its own. It is a cluster-scoped
OLM operator plus a console dynamic plugin. The internet-facing and
authentication boundaries are the OpenShift web console (platform) and the
Kubernetes API. Model those first.

## Risk-ranked summary

| Rank | Threat | Boundary | Exploitability | Impact | Mitigation in tree |
|------|--------|----------|----------------|--------|--------------------|
| 1 | Operator SA or operator image takeover | Build to runtime; privilege transition | Needs write to the operator Deployment, CSV, or image that OLM runs | Operator ClusterRole can patch MachineConfigPools, ComplianceRemediations, `consoles.operator.openshift.io/cluster`, install a Compliance Operator Subscription, and deploy the console plugin image | Restricted PSS on the manager pod (`operator/config/manager/manager.yaml`); least-privilege ClusterRole (`operator/config/rbac/role.yaml`); no `secrets` / `nodes` / `pods/exec`. Does not stop a stolen SA token. |
| 2 | Hostile console-plugin image via `RELATED_IMAGE_CONSOLE_PLUGIN` | Build to runtime | Needs write to the operator Deployment env or the CSV that sets it | Arbitrary JS loaded into every admin console session that opens Administration → Compliance | `ValidRelatedImage` in `operator/internal/controller/plugin.go` is syntactic only (length, charset). Not a registry allowlist. Digest-pinned Dockerfiles on the build side. |
| 3 | Remediation apply, auto-apply, or batch-apply reboots nodes | Authenticated API user → operator SA | User with `patch` on `ComplianceRemediation` **or** `patch` on `ClusterBaseline` (batch annotation / `spec.remediation.apply`). The confirmation modal is UI-only. | MachineConfigs and node reboots. Batch path also pauses MachineConfigPools. | RBAC (`operator/config/rbac/user_roles.yaml`); CRD default `spec.remediation.apply: Manual` (`operator/api/v1alpha1/clusterbaseline_types.go`); batch name/count guards (`operator/internal/controller/batch.go`). Modal in `console-plugin/src/components/RemediationsTab.tsx` is not a server control. |
| 4 | ClusterBaseline patch: waivers, schedule, profiles, catalog source | Authenticated API user → operator | User with `update`/`patch` on `clusterbaselines` (`baseline-security-admin` ClusterRoleBinding, or cluster-admin) | Score integrity (waive FAILs), scan enablement, `ScanSetting` schedule, optional auto-apply remediations, CO catalog override | CRD MaxItems/MaxLength/Pattern (`clusterbaseline_types.go`); writes name-scoped to `cluster` (`user_roles.yaml`); no validating admission webhook. Kubernetes API audit is the attribution trail. |
| 5 | Untrusted Compliance Operator fields rendered in the console | CO CRs → browser | Needs write to ComplianceCheckResult (or similar) in `openshift-compliance`, or a content-image that ships hostile description/instruction text | Stored XSS in an admin session if a render path uses HTML; spreadsheet formula injection on CSV export; path injection in deep-links | React text nodes (no `dangerouslySetInnerHTML` in `console-plugin/src`); HTML escape in `console-plugin/src/report.ts`; CSV formula hardening in `console-plugin/src/results.ts`; path-relative hrefs in `console-plugin/src/links.ts`. Recurring class: TEST-PLAN §U still has open XSS/SSRF rows. |
| 6 | Compliance posture disclosure via metrics | In-cluster network → `/metrics` | Needs a token allowed `get` on nonResourceURL `/metrics`, or access to platform Prometheus | Score, fail counts, last-scan time, batch-active | HTTPS + `filters.WithAuthenticationAndAuthorization` (`operator/cmd/main.go`); non-loopback insecure metrics refused; scraper SA + `baseline-security-metrics-reader` (`operator/config/prometheus/servicemonitor.yaml`, `operator/config/rbac/metrics_reader_role.yaml`). |
| 7 | Operator memory / reconcile exhaustion from check-result volume | CO CRs → operator | Large genuine result sets, or a flood of ComplianceCheckResults the operator lists | Manager OOM or wedged reconcile; stale score (`ComplianceStatusStale`) | `GOMEMLIMIT=440MiB` and memory limit 512Mi (`manager.yaml`); CRD caps on waivers (256), profiles (8), tailored profiles (32); batch caps in `batch.go`. No admission quota on foreign CO objects. |
| 8 | In-cluster reachability of plugin :9443 and metrics :8443 | Pod network | Any pod can TCP to the ClusterIP Services. No NetworkPolicy is shipped. | Plugin: static assets only. Metrics: still needs a token. | Plugin Service forced ClusterIP (`plugin.go`); GET/HEAD only, 1k body, TLS 1.2+ (`console-plugin/nginx.conf`). Gap: no NetworkPolicy. |

Gaps ranked above existing controls are 2 (image ref not pinned at deploy time), 3 (UI confirm is not authz), 4 (no webhook, waiver fields are free-form), and 8 (no NetworkPolicy).

## 1. Attack surface inventory

Not internet sockets. Entry points the code actually has:

| Entry point | Where | Trust of input |
|-------------|-------|----------------|
| OpenShift console → dynamic plugin JS | `console-plugin/src/components/*`, extensions in `console-plugin/console-extensions.json` | Authenticated console user. Data is k8s objects via the console proxy and the user's bearer token (ADR-007 in `docs/DESIGN-DECISIONS.md`). |
| Kubernetes API: `ClusterBaseline` spec + annotations | `operator/api/v1alpha1/clusterbaseline_types.go`; reconcile in `operator/internal/controller/` | Untrusted. Any client with patch. Includes `baselinesecurity.openshift.io/batch-apply` (`batch.go`). |
| Kubernetes API: Compliance Operator CRs (CheckResult, Scan, Suite, Remediation, Profile, TailoredProfile) | Watched in `operator/internal/controller/`; rendered in `console-plugin/src/{models,results,status,scoring,report}.ts` | Untrusted cluster data (labels, annotations, description, instructions, severity). Ownership filtered by suite label `baseline-<profile>` / `baseline-tp-<name>` (`matching.go`, `isOwnedByBaseline` in `models.ts`). |
| Console writes (user token) | Rescan, profile toggle, schedule, waivers, TailoredProfile authoring, remediation apply/unapply, auto-apply, batch annotation: `docs/SPEC.md` §4.3 and `console-plugin/src/patches.ts` | Authenticated; gated in the UI with `useAccessReview`. The API is the real gate. |
| Manager flags and env | `operator/cmd/main.go`: `--metrics-bind-address`, `--metrics-secure`, `--metrics-cert-dir`, `--health-probe-bind-address`, `--leader-elect`, `--zap-devel`; `RELATED_IMAGE_CONSOLE_PLUGIN`; `BASELINE_SECURITY_SKIP_DEFAULT_CR` | Deployment/CSV author. Treated as config, not end-user input. `RELATED_IMAGE_CONSOLE_PLUGIN` is still validated (`ValidRelatedImage`) because a mis-set or hostile env must not become a Deployment. |
| Metrics `:8443` `/metrics` | `operator/cmd/main.go`, Service `operator/config/manager/metrics_service.yaml` | Authenticated scrape (TokenReview + SAR). ClusterIP only. |
| Health `:8081` `/healthz` (liveness) and cache-sync readyz | `operator/cmd/main.go`; probes in `manager.yaml` | Unauthenticated ping. Port is **not** on a Service; kubelet to the pod. |
| Plugin nginx `:9443` | `console-plugin/nginx.conf`; Service created in `plugin.go` | TLS ClusterIP. Static files. Console proxy is the intended client. |
| Default CR creator | `operator/cmd/default_cr.go` (leader-elected `ClusterBaseline/cluster`) | Operator SA. Opt-out env `BASELINE_SECURITY_SKIP_DEFAULT_CR`. |
| OLM / catalog / CSV | `operator/bundle/manifests/baseline-security-operator.clusterserviceversion.yaml` | Install-time. Sets image refs, RBAC, env. |
| CI release publish | `.github/workflows/release.yml` | `workflow_dispatch` version is passed through an env var (fixed after command-injection in 0.5.11). Quay credentials are GitHub secrets. |
| Cron schedule | `spec.schedule` → owned `ScanSetting` (`operator/internal/controller/schedule.go`, `scanconfig.go`) | Untrusted CR field; five-field cron only. Invalid → `InvalidSchedule` Degraded, not a panic. |

Absent from the code (do not model as present):

- No admission webhook server, no conversion webhook, no mutating webhook.
- No operator REST API, no gRPC, no webhook receiver, no upload parser.
- No NetworkPolicy manifests.
- No `pprof` / debug bind in `main.go`.
- No `secrets` API access on the operator ClusterRole.

CLI is `oc` / `kubectl` against the API, not a product binary that parses files.

## 2. Trust boundaries and data flow

```
[Browser / oc]
    |  OpenShift console SSO  (platform)
    |  user's kube token
    v
[Console proxy] ---- plugin static JS (nginx :9443, ClusterIP)
    |
    |  same user token
    v
[kube-apiserver]  <---- CRD OpenAPI (no webhook)
    |                      |
    | ClusterBaseline      | Compliance* CRs
    v                      v
[Operator SA] ---------> [Compliance Operator]
    |  elevated: MCP patch, remediation patch,
    |  ScanSetting, Subscription, ConsolePlugin,
    |  consoles.operator.openshift.io/cluster
    v
[Plugin Deployment]  image = RELATED_IMAGE_CONSOLE_PLUGIN
```

Named boundaries:

| Boundary | What crosses | Validation / authn point |
|----------|--------------|--------------------------|
| User → app (console plugin) | Clicks, form fields, filters | Console SSO (platform). Plugin has no session of its own. |
| User → Kubernetes API | CR patches, list/watch | RBAC on the user token. CRD schema for `ClusterBaseline`. |
| App → API (plugin) | `useK8sWatchResource` / `k8sPatch` | Same user token. `useAccessReview` only disables UI. |
| Operator SA → cluster APIs | Reconcile writes | Operator ClusterRole. This is the privilege transition. |
| CO CRs → operator/plugin | Labels, descriptions, timestamps, remediation objects | Narrowed at the boundary: unstructured helpers, DNS-1123 checks, `isOwnedByBaseline`. Not treated as trusted. |
| Tenant → tenant | N/A (single cluster, one `ClusterBaseline/cluster`) | Isolation is Kubernetes RBAC, not a product tenancy layer. |
| Build → runtime | Images, CSV, `RELATED_IMAGE_*` | Digest-pinned Dockerfiles; OLM relatedImages; `ValidRelatedImage` (syntax). |
| Secrets → code | Service-ca TLS files; scraper SA token Secret; operator SA token | Operator ClusterRole has no `secrets` verbs. Certs are volume-mounted. Plugin sets `automountServiceAccountToken: false` (`plugin_pod.go`). |

Privilege transitions the model must keep:

1. **Batch-apply confused deputy.** A client with only `ClusterBaseline` patch sets `baselinesecurity.openshift.io/batch-apply`. The operator then `get`/`patch`es MachineConfigPools and `patch`es ComplianceRemediations (`batch_apply.go`, `batch_reconcile.go`). The user typically cannot pause MCPs themselves.
2. **Auto-apply.** `spec.remediation.apply: Automatic` is reconciled onto `ScanSetting.autoApplyRemediations` (`scanconfig.go`). Subsequent CO remediations apply without a per-item UI confirm.
3. **Console registration.** Operator patches `consoles.operator.openshift.io/cluster` `spec.plugins` (`plugin.go`).
4. **Default CR.** Leader creates `ClusterBaseline/cluster` (`default_cr.go`), which starts CIS scanning.

Secrets flow:

- Enter: service-ca annotation on the metrics and plugin Services; OLM/kubelet injects TLS Secrets as volumes. Scraper token Secret is declared in `servicemonitor.yaml`. GitHub Actions hold `QUAY_USERNAME` / `QUAY_TOKEN` (build only).
- Live: files under `/var/run/metrics-certs` and `/var/serving-cert`. Operator SA token is automounted for API calls. Plugin does not automount a token.
- Leave: metrics scrape uses the scraper Secret as a Bearer token. No product code copies cluster secrets into `ClusterBaseline` status. `main.go` logs related-image set/valid booleans, not the ref path.

Rotation: service-ca rotation is the platform's; metrics TLS reloads via `GetCertificate` (`operator/cmd/metrics_cert.go`). No application-level credential rotation.

## 3. Assets and impact

| Asset | What happens if stolen / corrupted / denied | Where it lives |
|-------|---------------------------------------------|----------------|
| Node configuration and uptime | Applied remediations render MachineConfigs and reboot nodes | CO `ComplianceRemediation` + MCP pause/resume |
| Admin browser session | Hostile plugin JS runs as the logged-in console user | ConsolePlugin backend + plugin image |
| Compliance score and history | False PASS/waiver theater; auditors trust `status.score` / reports | `ClusterBaseline` status; Prometheus gauges in `metrics.go` |
| Scan enablement and schedule | Scans stopped (`spec.profiles: []`) or hammered via cron | `ClusterBaseline` spec → `ScanSetting` |
| Operator SA privileges | Full product blast radius (MCP, remediations, console, CO install) | `operator/config/rbac/role.yaml` bound cluster-wide |
| Console operator plugin list | Plugin removed or extra plugins injected if the patch is broader than intended | `consoles.operator.openshift.io/cluster` |
| Metrics | Disclosure of fail counts and score to anyone who can scrape | `/metrics` |
| Availability of the plugin and operator | Compliance UI gone; scans unconfigured; `Degraded` | Deployments in `openshift-baseline-security` |

Reputation: a cooked score or a remediation-driven outage is attributed to "the compliance operator / baseline" by operators. That is the concrete blast radius, not a generic "data breach". This product does not store customer PII; waiver `reason` / `requestedBy` / `approvedBy` are free-text a customer might put names into.

## 4. Threats per boundary

STRIDE, tied to entry points. Not a generic checklist.

### User / console → Kubernetes API (authentication boundary)

| Class | Concrete threat |
|-------|-----------------|
| Spoofing | Console session takeover is a platform problem. Waiver `requestedBy` / `approvedBy` are typed strings (`ResultsTab.tsx`, `WaiverEntry` in `clusterbaseline_types.go`), not the authenticated user. A patch can impersonate an approver. |
| Tampering | Patch `ClusterBaseline` to waive FAILs (max 256), set `remediation.apply: Automatic`, change `schedule`, empty `profiles` (stops scanning), or set `complianceCatalogSource` to a hostile CatalogSource name (DNS-1123 only). Patch `ComplianceRemediation.spec.apply: true` directly, skipping the modal. |
| Repudiation | No Kubernetes Event is emitted on apply/waive (`Eventf` is unused). Attribution is API audit + optional free-form waiver fields. |
| Information disclosure | Viewer role can list check results (`user_roles.yaml` aggregates to `view` / `cluster-reader`). Expected. |
| Denial of service | Hostile `schedule` is bounded (MaxLength 128) and rejected if not five-field cron. Batch annotation over 256 names or non-DNS-1123 is cleared (`reconcile_test.go` `TestApplyRemediationBatchGuardrails`). Rescan annotation on every owned scan is a user-triggered load on CO, not amplified by this operator beyond the owned set. |
| Elevation of privilege | Batch-apply and auto-apply (confused deputy, above). `baseline-security-admin` is **not** aggregated onto `admin`: a RoleBinding to `admin` in `openshift-compliance` does not inherit remediation/scan patch. Bind `baseline-security-admin` cluster-wide, or use cluster-admin. |

### CO CRs → operator and plugin (untrusted cluster data)

| Class | Concrete threat |
|-------|-----------------|
| Tampering | Foreign suite labels must not enter the score. Mitigated by `ownedSuites` / `matchesAnyProfile` (`matching.go`) and `isOwnedByBaseline` (`models.ts`). Recurring: fuzz targets in `fuzz_extra_test.go`, `matching_test.go`. |
| Information disclosure / XSS | `description` and `instructions` are untrusted. Modal uses text (`ResultsTab.tsx` `Content` / pre-wrap). Report HTML escapes (`report.ts`). CSV formula prefix (`results.ts`, CWE-1236). Deep-links are path-relative (`links.ts`). |
| Denial of service | Unstructured maps from CO objects: helpers avoid `NestedMap` panic on non-JSON types (`batch.go` comment). Huge result lists are the residual DoS. |
| Elevation | Hostile remediation labels driving MCP names: non-DNS-1123 dropped (`poolFromRemediation`). |

### Operator process (in-cluster listeners)

| Class | Concrete threat |
|-------|-----------------|
| Spoofing | Metrics without authn. Mitigated: default `--metrics-secure=true`; non-loopback insecure forced back to secure (`main.go`). |
| Information disclosure | `/metrics` with a stolen scraper token or overly broad `get` on `/metrics`. Healthz is unauthenticated but not Service-exposed. |
| Denial of service | nginx `client_max_body_size 1k` and GET/HEAD only. Metrics bind validated (`validateListenAddr`). Empty metrics addr is restored to `:8443` rather than controller-runtime's `:8080`. |
| Elevation | `--leader-elect=false` on a 2-replica Deployment races default-CR create (`main.go` logs a warning). |

### Build → runtime

| Class | Concrete threat |
|-------|-----------------|
| Tampering | Substituted operator or plugin image. Recurring: `workflow_dispatch` shell injection (fixed 0.5.11 by env-passing the version). `ValidRelatedImage` does not pin digest or registry. A compromised npm package with a `postinstall` cannot run during `yarn install` (`enableScripts: false` in `console-plugin/.yarnrc.yml`; image `YARN_ENABLE_SCRIPTS=false`). |
| Denial of service | Standard-library infinite loop on invalid input via status text (fixed: `golang.org/x/text` bump, 0.5.9). Recurring class: untrusted string → parser. Fuzz targets exist; `make fuzz` is release-gate, not per-PR. |

## 5. Mitigations mapping

Existing controls, with the threats they cover:

| Control | File | Covers |
|---------|------|--------|
| User-token data path, no plugin backend | `docs/DESIGN-DECISIONS.md` ADR-007; plugin uses SDK hooks | Plugin cannot act beyond the user's RBAC |
| Viewer aggregated to view/cluster-reader; admin ClusterRole not aggregated | `operator/config/rbac/user_roles.yaml` | Readers get results without extra bindings; namespace admin of `openshift-compliance` does not inherit node-reboot writes |
| Operator ClusterRole without secrets/nodes/exec | `operator/config/rbac/role.yaml` | Limits blast radius of SA theft vs cluster-admin |
| Restricted PSS, read-only rootfs, drop ALL, non-root | `manager.yaml`, `plugin_pod.go`, Dockerfiles `USER 65532` / `1001` | Container breakout cost |
| Plugin: no automount SA token, no hostNetwork/PID/IPC | `plugin_pod.go` | Plugin pod is a static file server |
| Metrics HTTPS + TokenReview/SAR; insecure non-loopback refused | `operator/cmd/main.go`, `metrics_auth_role.yaml` | Unauthenticated metrics scrape |
| CRD MaxItems/MaxLength/Pattern and enums | `clusterbaseline_types.go` | CR bloat, junk catalog names, junk waiver names |
| Suite-label ownership filter | `matching.go`, `models.ts` `isOwnedByBaseline` | Foreign CO results in score/UI |
| DNS-1123 + count guards on batch-apply | `batch.go` | Hostile annotation pausing MCPs / wedging reconcile |
| `ValidRelatedImage` | `plugin.go` | Shell metacharacters / huge env in image ref |
| Plugin nginx: TLS1.2+, no tickets, nosniff, DENY frame, CSP `default-src 'none'`, GET/HEAD, 1k body | `console-plugin/nginx.conf` | Direct hits on the plugin Service |
| React text rendering; report `esc()`; CSV formula prefix; `safeDownloadName` | `ResultsTab.tsx`, `report.ts`, `results.ts`, `download.ts` | XSS / CWE-1236 / download path |
| Fuzz targets on untrusted maps, image refs, cron, scoring | `operator/internal/controller/*_test.go`, `console-plugin/src/fuzz.test.ts` | Recurring parser panics |
| Hermetic image builds (`--network=none`, lockfile, digest bases) | `operator/Dockerfile`, `console-plugin/Dockerfile` | Build-time supply chain |
| Yarn install without lifecycle scripts | `console-plugin/.yarnrc.yml` `enableScripts: false`; `console-plugin/Dockerfile` `YARN_ENABLE_SCRIPTS=false` | Compromised registry package cannot run `preinstall`/`install`/`postinstall` |
| Release version not interpolated into `run:` | `.github/workflows/release.yml` | Workflow command injection |

Threats with no (or only UI) mitigation:

| Threat | Rank | Why it stays |
|--------|------|----------------|
| Arbitrary plugin image if Deployment env is writable | High | `ValidRelatedImage` is not an allowlist |
| Remediation apply via API, skipping the modal | High | By design (ADR-007); RBAC is the control. Docs that call the modal a security gate overstate it. |
| Waiver attribution spoof | Medium | Fields are spec strings; not bound to the user token |
| No NetworkPolicy | Medium | Any pod can reach ClusterIP ports |
| No validating webhook | Medium | Schema-only; `Automatic` remediations are a legal spec |
| Operator SA is a single point of failure for MCP + console + CO install | High | One ClusterRole carries several high-impact writes |
| No Kubernetes Events on apply/waive | Low (investigation) | API audit exists cluster-wide; product emits none |

`docs/SPEC.md` §6 and `docs/PATTERNS.md` §6 describe remediation apply as "confirmation-gated and RBAC-gated". The confirmation gate is the modal in `RemediationsTab.tsx`. It does not run on `oc patch`. Treat RBAC as the control; treat the modal as UX.

## 6. Abuse cases

Hostile but authenticated. Enabling path named. Not demonstrated.

1. **Compliance theater.** User with `ClusterBaseline` patch adds waivers for every FAIL (up to 256) with a far-future `expiresAt`. Score climbs; Prometheus `ComplianceScoreLow` quiets. Path: `ResultsTab.tsx` → `addWaiverPatch` (`patches.ts`) → CR spec; operator scoring in `aggregate.go` / `scoring.go`. Attribution fields can name someone else.

2. **Node reboot without the modal.** `oc patch complianceremediation … --type merge -p '{"spec":{"apply":true}}'` with `baseline-security-admin` (or cluster-admin). Path: CO applies; this operator is not in the loop. Same for `spec.remediation.apply: Automatic` on the CR, which the operator copies onto `ScanSetting`. A RoleBinding to `admin` in `openshift-compliance` is not enough.

3. **Batch pause as deputy.** Patch annotation `baselinesecurity.openshift.io/batch-apply` with owned remediation names. Operator pauses MCPs (`batch_apply.go`). A 10-minute grace resumes (`batchResumeGrace`) even if apply never completes. Still a window of paused pools.

4. **Stop scanning, keep the UI.** `spec.profiles: []` and empty tailored list prunes bindings and clears the score (`clusterbaseline_types.go` comment). The CR remains; Overview shows an empty baseline rather than an uninstall.

5. **Client-side enforcement.** Disabled buttons via `useAccessReview` (`RemediationsTab.tsx`, `ProfilesTab.tsx`, `Overview.tsx`). A user who bypasses the UI with the same token gets the API's decision, not the button's.

6. **CSV / HTML export of hostile rule text.** Export is client-side (`results.ts`, `report.ts`, `download.ts`). Hardening is in those files; a regression would execute in the admin's spreadsheet or browser, not on the operator.

## 7. Document quality

This file is the starter model. It is current as of the date above. Re-check on any change to RBAC, plugin nginx, metrics flags, `RELATED_IMAGE_*`, remediation/batch paths, or CRD validation.

`SECURITY.md` supported-version table matches README **Current release** and CHANGELOG **Support window**: latest published 0.x only; OpenShift 4.22 only. Disclosure contact is the CSV `spec.maintainers` email in `operator/bundle/manifests/baseline-security-operator.clusterserviceversion.yaml` (not a public GitHub issue). Scope matches the shipped operator, plugin, bundle, and metrics scrape path; Compliance Operator content is correctly out of scope.

## 8. Response readiness (note only)

Security-relevant actions with no product Event/audit object of their own:

- Remediation apply / unapply / batch (API audit on the CO objects and on `ClusterBaseline` annotations only)
- Waiver add/remove (API audit on `ClusterBaseline`; `requestedBy`/`approvedBy` are not authenticated identity)
- Plugin image change (Deployment env / pod spec)

Operator logs reconcile errors; metrics/alerts are in `docs/OBSERVABILITY.md`. Log shape is not owned here.

Path from "vulnerability reported" to "fix shipped": `SECURITY.md` (email CSV maintainer → CHANGELOG **Security** heading on the fix release). There is no documented SLA, on-call, or advisory process beyond that.
