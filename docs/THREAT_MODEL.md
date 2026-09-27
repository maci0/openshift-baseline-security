# Threat model

Last reviewed: 2026-09-27 (against `main` at 0.6.1, `a87d43b`).
Every file reference below was re-read against that commit on that date.
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
| 1 | Operator SA or operator image takeover | Build to runtime; privilege transition | Needs write to the operator Deployment, CSV, or image that OLM runs | Operator ClusterRole can patch MachineConfigPools cluster-wide (no `resourceNames`, `role.yaml:165-171`), create/delete/get/list/patch/update `scansettingbindings` (`role.yaml:113-123`), patch ComplianceRemediations, `consoles.operator.openshift.io/cluster` (group `operator.openshift.io`, `role.yaml:172-181`), create namespaces, install a Compliance Operator Subscription, and deploy the console plugin image. It also holds cluster-wide unscoped grants that no code path uses: `persistentvolumeclaims` list+watch (`role.yaml:24-30`, the only read is `client.InNamespace(complianceNamespace)` at `scanstorage.go:26`), `services` create+list/watch (`role.yaml:31-38`), and `poddisruptionbudgets` create+list+watch (`role.yaml:215-222`, the operator only touches the one name-scoped PDB at `role.yaml:223-233`). | Pod spec *satisfies* Restricted PSS but **no `pod-security.kubernetes.io/enforce` label exists anywhere in the repo** (`manager.yaml:1-11`); least-privilege ClusterRole (`operator/config/rbac/role.yaml`); no `secrets` / `nodes` / `pods/exec` rules at all. Does not stop a stolen SA token. |
| 2 | Hostile console-plugin image via `RELATED_IMAGE_CONSOLE_PLUGIN` | Build to runtime | Needs write to the operator Deployment env or the CSV that sets it | Arbitrary JS loaded into every admin console session that opens Administration → Compliance | `ValidRelatedImage` in `operator/internal/controller/plugin.go:50` is syntactic only (length, charset). Not a registry allowlist, not a digest pin. |
| 3 | Remediation apply, auto-apply, or batch-apply reboots nodes | Authenticated API user → operator SA | User with `patch` on `ComplianceRemediation` **or** `patch` on `ClusterBaseline` (batch annotation / `spec.remediation.apply`). The four confirmation modals in `RemediationsTab.tsx` are UI-only. | MachineConfigs and node reboots. Batch path also pauses MachineConfigPools. | RBAC (`operator/config/rbac/user_roles.yaml`); CRD default `spec.remediation.apply: Manual` (`operator/api/v1alpha1/clusterbaseline_types.go`); batch count/DNS-1123 guards with limits in `batch.go` enforced in `batch_apply.go`. Modals are not a server control. |
| 4 | ClusterBaseline patch: waivers, schedule, profiles, catalog source | Authenticated API user → operator | User with `update`/`patch` on `clusterbaselines` (`baseline-security-admin` ClusterRoleBinding, or cluster-admin) | Score integrity (waive FAILs), scan enablement, `ScanSetting` schedule, optional auto-apply remediations, CO catalog override | CRD MaxItems/MaxLength/Pattern (`clusterbaseline_types.go`); writes name-scoped to `cluster` (`user_roles.yaml`); no validating admission webhook. Kubernetes API audit is the attribution trail. |
| 5 | Hand-edited `status` wedges every later status write | Operator SA → API server | Needs `update` on `clusterbaselines/status`, or a restore-from-backup of a hostile status | A status that violates the CRD schema fails admission on every subsequent `Status().Update`, freezing conditions, score, and phase for the whole CR. Silent: the object still reads as present. | `operator/internal/controller/sanitize.go` clamps every value written to status to the CRD schema (MaxItems, MaxLength, Enum, Pattern, Minimum) plus an aggregate serialized-size budget before the update leaves the reconciler. Recurring class: unbounded untrusted list → schema. |
| 6 | Untrusted Compliance Operator fields rendered in the console | CO CRs → browser | Needs write to ComplianceCheckResult (or similar) in `openshift-compliance`, or a content image that ships hostile description/instruction text | Stored XSS in an admin session if a render path uses HTML; spreadsheet formula injection on CSV export; path injection in deep-links | React text nodes (no `dangerouslySetInnerHTML`, `innerHTML`, `document.write`, or `eval` anywhere in `console-plugin/src`; the one `document.write` hit is a comment at `CompliancePage.tsx:246` explaining why the blob path replaced it); HTML escape in `console-plugin/src/report.ts`; CSV formula hardening in `console-plugin/src/results.ts`; path-relative hrefs in `console-plugin/src/links.ts`. Recurring class: TEST-PLAN §U still carries XSS and href-injection as open manual checks. |
| 7 | Compliance posture disclosure via metrics | In-cluster network → `/metrics` | Needs a token allowed `get` on nonResourceURL `/metrics`, or access to platform Prometheus | Score, fail counts, last-scan time, batch-active | HTTPS + `filters.WithAuthenticationAndAuthorization` (`operator/cmd/main.go:232`); non-loopback insecure metrics refused (`main.go:169`); scraper SA + `baseline-security-metrics-reader` (`operator/config/prometheus/metrics_scraper.yaml`, `operator/config/rbac/metrics_reader_role.yaml`). The cert Secret volume is `optional: true` (`manager.yaml:179-183`), so a missing Secret leaves the process on its self-signed fallback; the ServiceMonitor pins `serverName`, so the scrape fails closed rather than trusting it. |
| 8 | Operator memory / reconcile exhaustion from check-result volume | CO CRs → operator | Large genuine result sets, or a flood of ComplianceCheckResults the operator lists | Manager OOM or wedged reconcile; stale score (`ComplianceStatusStale`) | `GOMEMLIMIT=440MiB` (`manager.yaml:89`) and memory limit 512Mi (`manager.yaml:158-159`); CRD caps on waivers (256), profiles (8), tailored profiles (32); batch caps in `batch.go`; status clamps in `sanitize.go`. No admission quota on foreign CO objects. |
| 9 | In-cluster reachability of plugin :9443 and metrics :8443 | Pod network | A pod in a tenant namespace can TCP to the plugin Service. The operator's metrics port is denied at the network layer. | Plugin: static assets only. | Operator: ingress-only `NetworkPolicy` (`operator/config/manager/networkpolicy.yaml`) denies every operator port except 8443 from `openshift-monitoring` and the service-ca operator. Plugin: no NetworkPolicy; its Service is forced ClusterIP (`plugin.go:242-254`) and nginx serves GET/HEAD only, 1k body, TLS 1.2+ (`console-plugin/nginx.conf`). The plugin pod lives in `openshift-console`, a platform-owned namespace this operator does not write policies into. |

Gaps ranked above existing controls are 2 (image ref not pinned at deploy time), 3 (the four UI confirm modals are not authz), and 4 (no webhook, waiver fields are free-form). Gap 9 now has a partial control: the operator namespace is covered, the plugin's is not.

One claimed mitigation the code does not support: "Restricted PSS" on the manager pod. The pod spec satisfies Restricted (`runAsNonRoot`, `RuntimeDefault` seccomp, `allowPrivilegeEscalation: false`, drop ALL, read-only rootfs; `manager.yaml:55-58,106-119`) but nothing enforces it, because no `pod-security.kubernetes.io/enforce` label exists on the `openshift-baseline-security` Namespace or anywhere else in the tree. A namespace admin can widen the pod spec and the pod will still be admitted. Treat the pod spec as defense in depth, not as an enforced control.

## 1. Attack surface inventory

Not internet sockets. Entry points the code actually has:

| Entry point | Where | Trust of input |
|-------------|-------|----------------|
| OpenShift console → dynamic plugin JS | `console-plugin/src/components/*`, extensions in `console-plugin/console-extensions.json` | Authenticated console user. Data is k8s objects via the console proxy and the user's bearer token (ADR-007 in `docs/DESIGN-DECISIONS.md`). |
| Kubernetes API: `ClusterBaseline` spec, annotations, **and `status`** | `operator/api/v1alpha1/clusterbaseline_types.go`; reconcile in `operator/internal/controller/`; status clamps in `operator/internal/controller/sanitize.go` | Untrusted. Any client with patch on the object; `status` is writable separately and is read back by the reconciler. Includes `baselinesecurity.openshift.io/batch-apply` (`batch.go`). |
| Kubernetes API: Compliance Operator CRs (CheckResult, Scan, Suite, Remediation, Profile, TailoredProfile) | Watched in `operator/internal/controller/`; rendered in `console-plugin/src/{models,results,status,scoring,report}.ts` | Untrusted cluster data (labels, annotations, description, instructions, severity). Ownership filtered by suite label `baseline-<profile>` / `baseline-tp-<name>` (`matching.go`, `isOwnedByBaseline` in `models.ts`). |
| Console writes (user token) | Rescan, profile toggle, schedule, waivers, TailoredProfile authoring, remediation apply/unapply, auto-apply, batch annotation: `docs/SPEC.md` §4.3 and `console-plugin/src/patches.ts` | Authenticated; gated by `mayWrite` (`console-plugin/src/permissions.ts`) over `useAccessReview`. The API is the real gate. |
| Manager flags | `operator/cmd/main.go:95-99`: `--metrics-bind-address`, `--metrics-secure`, `--metrics-cert-dir`, `--health-probe-bind-address`, `--leader-elect`; `opts.BindFlags` (`main.go:102`) adds the controller-runtime zap set `--zap-devel`, `--zap-encoder`, `--zap-log-level`, `--zap-stacktrace-level` | Deployment/CSV author. Treated as config, not end-user input. `RELATED_IMAGE_CONSOLE_PLUGIN` is still validated (`ValidRelatedImage`) because a mis-set or hostile env must not become a Deployment. |
| Manager env | `os.Getenv` has exactly two call sites, `plugin.go:42` (`EnvRelatedImageConsolePlugin`) and `main.go:176` (`RELATED_IMAGE_CONSOLE_PLUGIN`, start-up logging); `BASELINE_SECURITY_SKIP_DEFAULT_CR` is read through `parseEnvBool` (`main.go:180,379`); the kubeconfig is resolved by `ctrl.GetConfigOrDie` (`main.go`), which reads `--kubeconfig` then `KUBECONFIG` then in-cluster then `$HOME/.kube/config` | Deployment/CSV author. `parseEnvBool` fails start on an unrecognized value and truncates the logged value to 64 chars. `--kubeconfig` is registered explicitly by `clientconfig.RegisterFlags` in `main()` rather than left to that package's `init`, whose registration upstream marks for removal; `TestKubeconfigFlagIsDefined` (`cmd/cli_test.go`) pins the flag so the documented precedence cannot outlive the flag. The start-up `configuration` log records `kubeconfigFlagSet` as a bool, not the path. Not a control; recorded so the next pass does not read the help as the flag surface. |
| Metrics `:8443` `/metrics` | `operator/cmd/main.go`, Service `operator/config/manager/metrics_service.yaml` | Authenticated scrape (TokenReview + SAR). ClusterIP only. `--metrics-cert-dir` defaults to `/var/run/metrics-certs` (`main.go:97`) and a relative value is rejected at `main.go:163`. |
| Leader-election Lease | `main.go:240` `LeaderElectionID: "baseline-security-operator-lock"`; RBAC in `operator/config/rbac/leader_election_role.yaml` (a Role separate from `role.yaml`, so the "no leases rule in role.yaml" reading is correct) | Operator SA. A second replica holding the lease suppresses the other. `LeaderElectionReleaseOnCancel` (`main.go:245`) and a 20s graceful shutdown (`main.go:250`) bound the handover window. |
| Health `:8081` `/healthz` (startup + liveness) and `/readyz` (cache sync) | registered at `main.go:285-286`; `cacheSyncReadyz` at `main.go:502`; probes in `manager.yaml:128-150` | Unauthenticated ping. Port is **not** on a Service (`metrics_service.yaml` is the only Service in `operator/config`); kubelet to the pod. Readyz also fails once SIGTERM arrives, so a draining pod leaves the endpoints. `--health-probe-bind-address=` empty is a hard exit 2 rather than a silent disable, because the Deployment still probes :8081 (`main.go:145`). |
| Platform Prometheus scrape | `operator/config/prometheus/servicemonitor.yaml`; the Namespace label `openshift.io/cluster-monitoring: "true"` (`manager.yaml:10`) | The label is what opts the namespace into *platform* monitoring, so this is scraped by cluster monitoring, not user workload monitoring. Without it the ServiceMonitor and PrometheusRule ship with no targets. A cluster-monitoring reader can read everything on `/metrics`. |
| Prometheus service discovery | `operator/config/prometheus/metrics_scraper.yaml:46-70` | A namespaced Role/RoleBinding grants the `prometheus-k8s` SA in `openshift-monitoring` `get/list/watch` on `services`, `endpoints`, `pods` in the operator namespace. It exists only so discovery works, but it is a real cross-namespace RBAC edge to name. |
| Plugin TailoredProfile reads | `console-plugin/src/components/ProfilesTab.tsx:459,599` (`k8sGet`) | A read the UI needs to populate the edit form; adds `tailoredprofiles` to the user's effective read surface. |
| `ClusterScoreItem` watch | `console-plugin/src/components/ClusterScoreItem.tsx:15` (`useK8sWatchResource` on `ClusterBaseline`) | An independent watch independent of the main page tree; its RBAC surface is the baseline read, not the compliance reads. |
| Clipboard copy of a remediation | `console-plugin/src/components/RemediationsTab.tsx:1134` (`navigator.clipboard?.writeText`) | Fully attacker-influenceable text (remediation instructions, labels, annotations) is copied into the operator's clipboard. No HTML parsing, so not XSS, but it is a paste-into-terminal vector and a one-click exfiltration path into a local buffer. The call is optional-chained and branches on success, so a denied clipboard permission is a no-op rather than an unhandled rejection. |
| Plugin nginx `:9443` | `console-plugin/nginx.conf:58-145` (server block); Service created in `plugin.go:233-261` | TLS ClusterIP. Static files. Console proxy is the intended client. startup/readiness/liveness are HTTPS `GET /healthz` over the serving cert (`plugin_pod.go:139-145`); kubelet does not verify that cert, and a missing cert means nginx never listens, which the HTTP probe (unlike a bare TCP connect) reflects as a real failure. |
| Plugin access log (stdout) | `console-plugin/nginx.conf:37-40` | The `pii_safe` format records method, `$uri`, protocol, status, and body size only. Client IP, `Referer`, `User-Agent`, and the query string are deliberately dropped because the line is collected verbatim into must-gather's `console-plugin.log` and from there into support archives. Consequence for investigation: a 4xx spike cannot be attributed to a source IP from this log alone. |
| Report export (browser) | `console-plugin/src/components/CompliancePage.tsx:210-262` (`buildReportHtml` → `Blob` → `openBlobInTab`, `downloadBlob` on popup block) | The exported HTML is built from untrusted CO text and then **executed in a new tab in the operator's browser session** by an explicit click. This is the one place the plugin hands attacker-influenceable markup to a document; it rests entirely on `report.ts` escaping. |
| Default CR creator | `operator/internal/controller/default_cr.go` (leader-elected `ClusterBaseline/cluster`) | Operator SA. Opt-out env `BASELINE_SECURITY_SKIP_DEFAULT_CR`. |
| OLM / catalog / CSV | `operator/bundle/manifests/baseline-security-operator.clusterserviceversion.yaml` | Install-time. Sets image refs, RBAC, env. |
| CI release publish | `.github/workflows/release.yml` | `workflow_dispatch` version is passed through an env var (fixed after command-injection in 0.5.11). Quay credentials are GitHub secrets. |
| e2e `.env` loader (developer workstation only) | `console-plugin/e2e/dotenv.ts:23-76` | Reads a developer-supplied file into `CONSOLE_URL`, `KUBEADMIN_USER`, `KUBEADMIN_PASSWORD`, `SCREENSHOT_DIR`. Key allowlist at `dotenv.ts:12-17`; every unknown key, duplicate, non-`KEY=value` line, and unterminated quote is collected and raised as one hard error (`dotenv.ts:72-74`) rather than a silent drop, so the runner cannot inherit `PATH`, `NODE_OPTIONS`, or an unrelated variable. This is the only place a password is read from disk, and it is a dev-only path, not a shipped one. |
| Cron schedule | `spec.schedule` → owned `ScanSetting` (`operator/internal/controller/schedule.go`, `scanconfig.go`) | Untrusted CR field; five-field cron only. Invalid → `InvalidSchedule` Degraded, not a panic. |

Absent from the code (do not model as present):

- No admission webhook server, no conversion webhook, no mutating webhook. The
  only CR-level validation is the `ClusterBaseline` OpenAPI schema.
- No operator REST API, no gRPC, no webhook receiver, no upload parser.
- No NetworkPolicy for the console-plugin pod. The operator namespace has one
  (`operator/config/manager/networkpolicy.yaml`); the plugin is deployed into
  `openshift-console`, which this operator does not write policies into.
- No `pprof` / debug bind in `main.go`.
- No `secrets` API access on the operator ClusterRole (`role.yaml` contains no
  `secrets`, `nodes`, or `exec` rule at all).
- In the plugin, no `dangerouslySetInnerHTML`, `innerHTML`,
  `insertAdjacentHTML`, live `document.write`, `eval`, `new Function`,
  `postMessage` listener, `localStorage`, `fetch`, `axios`, or
  `XMLHttpRequest`. The plugin holds no session of its own and makes no
  outbound request.

Flags are the stdlib `flag` package, not cobra; a positional argument is a hard
exit 2, which is what rejects `--metrics-secure false`.

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
    |  (spec + status)     |
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
| Operator SA → cluster APIs | Reconcile writes, including `ClusterBaseline` status | Operator ClusterRole. This is the privilege transition. Status values written back are clamped in `sanitize.go`. |
| CO CRs → operator/plugin | Labels, descriptions, timestamps, remediation objects | Narrowed at the boundary: unstructured helpers, DNS-1123 checks, `isOwnedByBaseline`. Not treated as trusted. |
| Tenant → tenant | N/A (single cluster, one `ClusterBaseline/cluster`) | Isolation is Kubernetes RBAC, not a product tenancy layer. |
| Build → runtime | Images, CSV, `RELATED_IMAGE_*` | Digest-pinned Dockerfiles; OLM relatedImages; `ValidRelatedImage` (syntax). |
| Secrets → code | Service-ca TLS files; scraper SA token Secret; operator SA token; the e2e `.env` on a developer workstation | Operator ClusterRole has no `secrets` verbs. Certs are volume-mounted. Plugin sets `automountServiceAccountToken: false` and `ServiceAccountName: "default"` (`plugin_pod.go:86-91`). The e2e loader allowlists four keys and never dumps its map (`dotenv.ts`). |

Privilege transitions the model must keep:

1. **Batch-apply confused deputy.** A client with only `ClusterBaseline` patch sets `baselinesecurity.openshift.io/batch-apply`. The operator then `get`/`patch`es MachineConfigPools and `patch`es ComplianceRemediations (`batch_apply.go`, `batch_reconcile.go`). The user typically cannot pause MCPs themselves. The console reviews for batch apply and auto-apply cover both the baseline patch and the remediation patch (`canDelegateRemediationApply`), so the UI does not offer a request the caller could not make per item; the API is still the real gate for anyone who edits the CR directly.
2. **Auto-apply.** `spec.remediation.apply: Automatic` is reconciled onto `ScanSetting.autoApplyRemediations` (`scanconfig.go`). Subsequent CO remediations apply without a per-item UI confirm.
3. **Console registration.** Operator patches `consoles.operator.openshift.io/cluster` `spec.plugins` (`plugin.go`).
4. **Default CR.** Leader creates `ClusterBaseline/cluster` (`internal/controller/default_cr.go`), which starts CIS scanning.

Secrets flow:

- Enter: service-ca annotation on the metrics and plugin Services; OLM/kubelet injects TLS Secrets as volumes. The metrics cert volume is `optional: true` (`manager.yaml:179-183`), so the process can run on the self-signed fallback identity in `metrics_cert.go`; the ServiceMonitor pins `serverName` so the scrape fails closed. Scraper token Secret is declared in `metrics_scraper.yaml`. GitHub Actions hold `QUAY_USERNAME` / `QUAY_TOKEN` (build only). A developer's `console-plugin/.env` supplies `KUBEADMIN_PASSWORD` to the Playwright runner; process env wins over the file, and the file is never a fallback for a key CI injects.
- Live: files under `/var/run/metrics-certs` and `/var/serving-cert`. Operator SA token is automounted for API calls. Plugin does not automount a token and has no pull secrets of its own (`plugin_pod.go:85-91`).
- Leave: metrics scrape uses the scraper Secret as a Bearer token. No product code copies cluster secrets into `ClusterBaseline` status. `main.go` logs related-image set/valid booleans, not the ref path. The plugin logs no client identity by design (see §1).

Rotation: service-ca rotation is the platform's; the metrics cert reloads through `metricsCertProvider.GetCertificate` (`operator/cmd/metrics_cert.go:89`), which is called from concurrent TLS handshakes. No application-level credential rotation.

## 3. Assets and impact

| Asset | What happens if stolen / corrupted / denied | Where it lives |
|-------|---------------------------------------------|----------------|
| Node configuration and uptime | Applied remediations render MachineConfigs and reboot nodes | CO `ComplianceRemediation` + MCP pause/resume |
| Admin browser session | Hostile plugin JS runs as the logged-in console user | ConsolePlugin backend + plugin image |
| Compliance score and history | False PASS/waiver theater; auditors trust `status.score` / reports | `ClusterBaseline` status; Prometheus gauges in `metrics.go` |
| Scan enablement and schedule | Scans stopped (`spec.profiles: []`) or hammered via cron | `ClusterBaseline` spec → `ScanSetting` |
| Operator SA privileges | Full product blast radius. Two rules carry most of it: `machineconfigpools` `get,patch` is cluster-wide with **no `resourceNames`** (`role.yaml:165-171`), and `scansettingbindings` has create/delete/get/list/patch/update in `openshift-compliance` (`role.yaml:113-123`), so a stolen token can repoint the `baseline` ScanSetting. Also `namespaces` create, `subscriptions` create, `operatorgroups` create. | `operator/config/rbac/role.yaml` bound cluster-wide |
| Cluster-wide read surface the operator does need | `compliance_operator.go:280-300` deliberately pages a **cluster-wide** CSV List on a fallback path (a `CSVCatalog` CR carries the full install spec), reading only name, namespace, and status.phase, with a page cap and a repeated-token guard. The matching `clusterserviceversions`/`catalogsources` get/list/watch (`role.yaml:182-190`) is what makes that lookup possible. | A stolen token reads the installed-operator inventory: every CSV, its version, channel, and image reference. That is a supply-chain reconnaissance surface, accepted knowingly and bounded by the page cap. |
| Cluster-wide grants the operator never uses | `persistentvolumeclaims` list+watch (`role.yaml:24-30`), `services` create+list/watch (`role.yaml:31-38`), `poddisruptionbudgets` create+list/watch (`role.yaml:215-222`). Every corresponding code path is namespace-scoped (`scanstorage.go:26`, `plugin_pod.go`, `plugin.go:119,301`) and the manager cache is already scoped to two namespaces. | Pure excess authority on a stolen token: cluster-wide PVC inventory, the ability to create Services anywhere, and a PDB-create lever that can block a legitimate node drain. Scoping fix belongs to authz-review. |
| Platform monitoring config | Repointing or silencing the scrape and alerts that expose a cooked score | `servicemonitor.yaml`, `metrics_scraper.yaml`, `prometheusrule.yaml`, the `openshift.io/cluster-monitoring` Namespace label |
| Console operator plugin list | Plugin removed or extra plugins injected if the patch is broader than intended | `consoles.operator.openshift.io/cluster` |
| Metrics | Disclosure of fail counts and score to anyone who can scrape | `/metrics` |
| The operator's own status write path | A hostile or corrupt status blocks every later update, so the product reports nothing rather than something wrong | `ClusterBaseline.status`, guarded by `sanitize.go` |
| Availability of the plugin and operator | Compliance UI gone; scans unconfigured; `Degraded` | Deployments in `openshift-baseline-security` |
| Administrator identity, indirectly | The plugin access log deliberately carries no client IP, so a support archive cannot be traced back to a person; conversely a stolen log leaks nothing about who browsed | `console-plugin/nginx.conf:37-40` |

Reputation: a cooked score or a remediation-driven outage is attributed to "the compliance operator / baseline" by operators. That is the concrete blast radius, not a generic "data breach". This product does not store customer PII; waiver `reason` / `requestedBy` / `approvedBy` are free-text a customer might put names into, and that text is reachable by anyone with viewer rights and by every report export.

## 4. Threats per boundary

STRIDE, tied to entry points. Not a generic checklist.

### User / console → Kubernetes API (authentication boundary)

| Class | Concrete threat |
|-------|-----------------|
| Spoofing | Console session takeover is a platform problem. Waiver `requestedBy` / `approvedBy` are typed strings (`ResultsTab.tsx`, `WaiverEntry` in `clusterbaseline_types.go`), not the authenticated user. A patch can impersonate an approver. |
| Tampering | Patch `ClusterBaseline` to waive FAILs (max 256), set `remediation.apply: Automatic`, change `schedule`, empty `profiles` (stops scanning), or set `complianceCatalogSource` to a hostile CatalogSource name (DNS-1123 only). Patch `ComplianceRemediation.spec.apply: true` directly, skipping the modal. |
| Tampering (status) | Write `clusterbaselines/status` directly with a schema-violating or oversized value. Mitigated by `sanitize.go` on the operator's writes; a client with status write access can still leave a status the operator must live with until the next reconcile replaces it. |
| Repudiation | No Kubernetes Event is emitted on apply/waive (`Eventf` has no call site in `operator/`). Attribution is API audit + optional free-form waiver fields. |
| Information disclosure | Viewer role can list check results (`user_roles.yaml` aggregates to `view` / `cluster-reader`). Expected. |
| Denial of service | Hostile `schedule` is bounded (MaxLength 128) and rejected if not five-field cron. Batch annotation over 256 names or non-DNS-1123 is cleared (`batch_apply.go`; `TestApplyRemediationBatchGuardrails`). A hand-edited status is bounded by the sanitize clamps. Rescan annotation on every owned scan is a user-triggered load on CO, not amplified by this operator beyond the owned set. |
| Elevation of privilege | Batch-apply and auto-apply (confused deputy, above). `baseline-security-admin` is **not** aggregated onto `admin` (no `aggregationRule` in `user_roles.yaml`): a RoleBinding to `admin` in `openshift-compliance` does not inherit remediation/scan patch. Bind `baseline-security-admin` cluster-wide, or use cluster-admin. The unbounded `machineconfigpools` patch is the operator SA's alone; no user role carries it. |

### CO CRs → operator and plugin (untrusted cluster data)

| Class | Concrete threat |
|-------|-----------------|
| Tampering | Foreign suite labels must not enter the score. Mitigated by `ownedSuites` / `matchesAnyProfile` (`matching.go`) and `isOwnedByBaseline` (`models.ts`). Recurring: 19 fuzz targets in `matching_test.go` plus 12 in `fuzz_extra_test.go`. |
| Information disclosure / XSS | `description` and `instructions` are untrusted. Modal uses text (`ResultsTab.tsx` `Content` / pre-wrap). Report HTML escapes (`report.ts`). CSV formula prefix (`results.ts`, CWE-1236). Deep-links are path-relative (`links.ts`). |
| Denial of service | Unstructured maps from CO objects: the batch path avoids `NestedMap` (which DeepCopyJSON-panics on non-JSON types) in favor of `NestedFieldNoCopy` + type assert (`batch.go` `poolFromRemediation`). Huge result lists and a hostile status are the residual DoS. |
| Elevation | Hostile remediation labels driving MCP names: non-DNS-1123 dropped by `poolFromRemediation` (`batch.go:77-102`, `validK8sName` at `batch.go:111-116`), re-checked in `batch_reconcile.go`. |

### Operator process (in-cluster listeners)

| Class | Concrete threat |
|-------|-----------------|
| Spoofing | Metrics without authn. Mitigated: default `--metrics-secure=true`; non-loopback insecure forced back to secure (`main.go:169`, loopback test at `main.go:356-362`). |
| Information disclosure | `/metrics` with a stolen scraper token or overly broad `get` on `/metrics`. Health probes are unauthenticated but not Service-exposed. |
| Denial of service | nginx `client_max_body_size 1k` and GET/HEAD only (`nginx.conf:80,118-122`). Metrics and probe bind addresses are validated (`validateListenAddr`, `main.go:331`); an empty probe address is a hard exit rather than a never-ready pod, and a relative `--metrics-cert-dir` is rejected (`main.go:163`). Empty metrics addr is restored to `:8443` (`main.go:139`) rather than controller-runtime's `:8080`. Recurring class: a hostile or buggy apiserver that returns a repeated `continue` token would spin a paged List until the reconcile deadline. Guarded at all four paged call sites by a token-advance check (`nextPageToken`, `internal/controller/helpers.go:66-71`; inlined at `compliance_operator.go:295-300`) and covered by `helpers_test.go`. |
| Elevation | `--leader-elect=false` on a 2-replica Deployment races default-CR create (`main.go:202-205` logs a warning). |
| Resource exhaustion | The manager informer cache is cluster-wide per type unless scoped, so a typed read caches every object of that type in the cluster. Scoped by `controller.ManagerCacheOptions()` (`internal/controller/managercache.go`): plugin Deployment/Service/PDB in `openshift-baseline-security`, scan-storage PVCs in `openshift-compliance`. Named ConfigMap reads bypass the cache entirely (`main.go:268`, `DisableFor: []client.Object{&corev1.ConfigMap{}}`), so the dashboard CM costs one `get` and no `list/watch`. RBAC still grants cluster-wide `list`/`watch` on the four scoped types, which is the residual edge. |

### Build → runtime

| Class | Concrete threat |
|-------|-----------------|
| Tampering | Substituted operator or plugin image. Recurring: `workflow_dispatch` shell injection (fixed 0.5.11 by env-passing the version). `ValidRelatedImage` does not pin digest or registry. A compromised npm package with a `postinstall` cannot run during `yarn install` (`enableScripts: false` in `console-plugin/.yarnrc.yml`; image `YARN_ENABLE_SCRIPTS=false`). |
| Denial of service | Standard-library infinite loop on invalid input via status text (fixed: `golang.org/x/text` bump, 0.5.9). Recurring class: untrusted string → parser, now including the status sanitizer. 57 fuzz targets exist; `make fuzz` is a release gate, not a per-PR one. |

## 5. Mitigations mapping

Existing controls, with the threats they cover:

| Control | File | Covers |
|---------|------|--------|
| User-token data path, no plugin backend | `docs/DESIGN-DECISIONS.md` ADR-007; plugin uses SDK hooks | Plugin cannot act beyond the user's RBAC |
| Viewer aggregated to view/cluster-reader; admin ClusterRole not aggregated | `operator/config/rbac/user_roles.yaml` (no `aggregationRule` block exists) | Readers get results without extra bindings; namespace admin of `openshift-compliance` does not inherit node-reboot writes |
| Operator ClusterRole without secrets/nodes/exec | `operator/config/rbac/role.yaml` (no such rule); leases live in a separate Role at `config/rbac/leader_election_role.yaml` | Limits blast radius of SA theft vs cluster-admin. Does **not** bound `machineconfigpools` patch (cluster-wide, no `resourceNames`), `scansettingbindings` CRUD, the deliberate cluster-wide CSV fallback read, or the three unused cluster-wide grants named in §3. The manager *cache* is namespace-scoped (`ManagerCacheOptions`, `internal/controller/managercache.go`) but the *RBAC* is wider, so the cache scope is a memory optimization, not a privilege boundary |
| Pod spec satisfies Restricted PSS; read-only rootfs, drop ALL, non-root | `manager.yaml:55-58,106-119`, `plugin_pod.go:151-165`, `operator/Dockerfile:49` `USER 65532:65532`, `console-plugin/Dockerfile:65` `USER 1001:1001` | Container breakout cost. **No `pod-security.kubernetes.io/enforce` label exists in the repo**, so this is admitted-anyway posture, not an enforced control |
| Plugin: no automount SA token, no pull secrets, no host namespaces | `plugin_pod.go:85-96` | Plugin pod is a static file server. The Service is re-coerced to ClusterIP and every external-exposure field is cleared on every pass (`plugin.go:242-254`, inside a `CreateOrUpdate` mutate func at `plugin.go:234-261`) |
| Plugin access log carries no client identity | `console-plugin/nginx.conf:37-40` | Personal data does not reach must-gather or support archives. Cost: the log cannot attribute a 4xx to a source |
| Status sanitizing layer | `operator/internal/controller/sanitize.go`; clamps mirror the CRD markers in `clusterbaseline_types.go` | A hostile, hand-edited, or restored status cannot wedge `Status().Update` and freeze every later write |
| Metrics HTTPS + TokenReview/SAR; insecure non-loopback refused | `operator/cmd/main.go:225-233,169`; `metrics_auth_role.yaml` | Unauthenticated metrics scrape |
| CRD MaxItems/MaxLength/Pattern | `clusterbaseline_types.go`: waivers 256, profiles 8 plus a `ProfileKey` enum, tailored profiles 32, schedule MaxLength 128, catalog source MaxLength 253 + DNS-1123-ish pattern (deliberately no enum so unset auto-detect still works) | CR bloat, junk catalog names, junk waiver names |
| Suite-label ownership filter | `matching.go`, `models.ts` `isOwnedByBaseline` | Foreign CO results in score/UI |
| DNS-1123 + count guards on batch-apply | `batch.go` | Hostile annotation pausing MCPs / wedging reconcile |
| `ValidRelatedImage` | `plugin.go:50-68` | Shell metacharacters / huge env in image ref |
| Plugin nginx: TLS1.2+, no tickets, nosniff, DENY frame, CSP `default-src 'none'`, GET/HEAD, 1k body, `server_tokens off`, `Referrer-Policy no-referrer`, `Permissions-Policy`, HSTS, `Cross-Origin-Resource-Policy`/`-Opener-Policy same-origin`; plaintext 8080 explicitly excluded so the S2I snippets cannot add one | `console-plugin/nginx.conf:48-124` (server block 58-145; the 8080 exclusion rationale at :55-56) | Direct hits on the plugin Service |
| Report export via `Blob` + `openBlobInTab` (opener dropped, revoked on every path, falls back to download on popup block) | `CompliancePage.tsx:210-262`, `download.ts` | Window-opener takeover and silent no-op from the one path that hands untrusted text to a browser document |
| React text rendering; report `esc()`; CSV formula prefix; `safeDownloadName` | `ResultsTab.tsx`, `report.ts`, `results.ts`, `download.ts` | XSS / CWE-1236 / download path |
| 57 operator fuzz targets plus a deterministic sweep in the plugin | `operator/{cmd,internal/controller}/*_test.go`, `console-plugin/src/{fuzz.test.ts,report.test.ts,links.test.ts}` | Recurring parser panics. `report.ts` and `links.ts` are covered by seeded sweeps in their test files (which include `<img src=x onerror=alert(1)>` and `javascript:` payloads) rather than by a Go-style fuzz target |
| e2e `.env` key allowlist with hard errors | `console-plugin/e2e/dotenv.ts:12-17,23-76`, `dotenv.test.ts` | A typo'd key or an unrelated variable silently inheriting into the runner (which would also pull in `KUBEADMIN_PASSWORD` where it was not meant to go) |
| Hermetic, digest-pinned image builds (`--network=none`, `GOPROXY=off`, lockfile, digest bases) | `operator/Dockerfile`, `console-plugin/Dockerfile`, `operator/catalog.Dockerfile` | Build-time supply chain for released images. `operator/Dockerfile.ci` is tag-pinned against `registry.ci.openshift.org`, not digest-pinned; it never ships |
| Yarn install without lifecycle scripts | `console-plugin/.yarnrc.yml` `enableScripts: false`; `console-plugin/Dockerfile` `YARN_ENABLE_SCRIPTS=false` | Compromised registry package cannot run `preinstall`/`install`/`postinstall` |
| Release version not interpolated into `run:` | `.github/workflows/release.yml` | Workflow command injection |

Threats with no (or only UI) mitigation:

| Threat | Rank | Why it stays |
|--------|------|----------------|
| Arbitrary plugin image if Deployment env is writable | High | `ValidRelatedImage` is not an allowlist |
| Remediation apply via API, skipping the modals | High | By design (ADR-007); RBAC is the control. Docs that call the modal a security gate overstate it. |
| Operator SA is a single point of failure for MCP + scansettingbindings + console + CO install | High | One ClusterRole carries several high-impact writes; the MCP patch is cluster-wide and unname-scoped |
| Unused cluster-wide grants on the operator SA: `persistentvolumeclaims` list+watch, `services` create+list/watch, `poddisruptionbudgets` create+list+watch | High | `role.yaml:24-30,31-38,215-222`. Every code path that touches these is namespace-scoped and the manager cache is already scoped to two namespaces, so these grants buy nothing and cost a stolen token cluster-wide PVC enumeration plus a node-drain lever. Scoping fix handed to authz-review. |
| Cluster-wide CSV fallback read discloses the installed-operator inventory | Medium (accepted) | Deliberate and page-capped (`compliance_operator.go:274-300`). Recorded so a later pass does not "fix" it into a narrower grant that breaks the OKD/disconnected install path. |
| No `pod-security.kubernetes.io/enforce` label | Medium | The pod spec satisfies Restricted but nothing enforces it; a namespace admin can widen it and the pod still schedules |
| Waiver attribution spoof | Medium | Fields are spec strings; not bound to the user token |
| No NetworkPolicy on the console-plugin pod | Medium | Any pod can reach the plugin ClusterIP; the operator namespace is covered by `config/manager/networkpolicy.yaml` |
| No validating webhook | Medium | Schema-only; `Automatic` remediations are a legal spec |
| Direct status write by a client with status RBAC | Low | Clamped on the operator's side, but a third-party writer's value is only corrected on the next reconcile |
| No Kubernetes Events on apply/waive | Low (investigation) | API audit exists cluster-wide; product emits none |

`docs/SPEC.md:395` calls remediation apply "confirmation-gated and RBAC-gated (user
token + admin role + modal)" and `docs/PATTERNS.md:155` says dangerous actions are
"opt-in, confirmation-gated, and RBAC-gated". The confirmation gate is the four
modals in `RemediationsTab.tsx` (apply, unapply, batch, auto-apply). None of them
run on `oc patch`. Treat RBAC as the control; treat the modals as UX. Those two
docs are owned elsewhere; the claim is recorded here so sec-review can aim at it.

`docs/SPEC.md:396-397` also claims the plugin loads no external sources so the
console default CSP applies unmodified. Verified: no `fetch`, `axios`,
`XMLHttpRequest`, `src=` from data, or absolute href anywhere in
`console-plugin/src`. That claim holds today and is load-bearing.

## 6. Abuse cases

Hostile but authenticated. Enabling path named. Not demonstrated.

1. **Compliance theater.** User with `ClusterBaseline` patch adds waivers for every FAIL (up to 256) with a far-future `expiresAt`. Score climbs; Prometheus `ComplianceScoreLow` quiets. Path: `ResultsTab.tsx` → `addWaiverPatch` (`patches.ts`) → CR spec; operator scoring in `aggregate.go` / `scoring.go`. Attribution fields can name someone else.
2. **Node reboot without the modal.** `oc patch complianceremediation … --type merge -p '{"spec":{"apply":true}}'` with `baseline-security-admin` (or cluster-admin). Path: CO applies; this operator is not in the loop. Same for `spec.remediation.apply: Automatic` on the CR, which the operator copies onto `ScanSetting`. A RoleBinding to `admin` in `openshift-compliance` is not enough.
3. **Batch pause as deputy.** Patch annotation `baselinesecurity.openshift.io/batch-apply` with owned remediation names. Operator pauses MCPs (`batch_apply.go`). A 10-minute grace (`batchResumeGrace`, `batch.go:45`) resumes the pools even if apply never completes, and a zero or far-future `StartedAt` is treated as garbage rather than disabling the valve forever. The guards are count-based, not per-item: more than 256 remediations, or **one** non-DNS-1123 name in the list, aborts the whole batch and clears the annotation (`batch_apply.go`) rather than skipping the bad entry, so a partial pause is not reachable through this path.
4. **Stop scanning, keep the UI.** `spec.profiles: []` and empty tailored list prunes bindings and clears the score (`clusterbaseline_types.go` comment). The CR remains; Overview shows an empty baseline rather than an uninstall.
5. **Freeze the score by poisoning status.** A writer with `clusterbaselines/status` writes a condition list that is pattern-valid per entry but exceeds the aggregate size budget, or a count outside its `Minimum`. Any unclamped write would fail admission and wedge the CR. Enabling path today: the writer calls the API directly, not the operator. `sanitize.go` is why the operator cannot be the one to do this by accident; the third-party writer is bounded only on the next reconcile.
6. **Client-side enforcement.** One `useAccessReview` result feeds two consumers. Every mutation calls `mayWrite` (`console-plugin/src/permissions.ts:23`) immediately before it sends, and no unguarded write exists in the plugin: `BaselineNotConfigured.tsx:37`, `CompliancePage.tsx:147`, `Overview.tsx:193`, `ProfilesTab.tsx:510,721,779`, `RemediationsTab.tsx:296,330,360`, `ResultsTab.tsx:270`. Separately the same review values are read by each control to compute its own `disabled` prop (`RemediationsTab.tsx:413`, `ResultsTab.tsx:301`, `ProfilesTab.tsx:681`, `CompliancePage.tsx:320,331`); that is a second read of the same hook, not a second call through `mayWrite`. The re-check before send is what stops a modal, editor, or form held open across a revocation from spending the request the button had already admitted; an unresolved review denies. This remains client-side: a user who bypasses the UI with the same token gets the API's decision, not the plugin's. The write still goes to the API server, which authorizes independently.
7. **CSV / HTML export of hostile rule text.** Export is client-side (`results.ts`, `report.ts`, `download.ts`) and the HTML opens in a new tab in the operator's session (`CompliancePage.tsx:210-262`). Hardening is in those files; a regression would execute in the admin's spreadsheet or browser, not on the operator.
8. **Clipboard as a paste-into-terminal vector.** A user with remediation read copies a `ComplianceRemediation` whose instructions or labels carry attacker-chosen text (`RemediationsTab.tsx:1134`), then pastes it into a shell or a support ticket. The plugin's own controls do not cover the destination. The blob paths are hardened; the clipboard is a raw passthrough.

## 7. Document quality

This file is current as of the date above. Every file reference in it was
re-read against that commit on that date. Re-check on any change to RBAC, plugin
nginx, metrics flags, `RELATED_IMAGE_*`, remediation/batch paths, the status
sanitizing layer, Namespace labels, CRD validation, the paged-List guards in
`internal/controller/helpers.go`, or the operator's own flag and help surface in
`cmd/main.go`.

The previous pass found and closed: a stale commit stamp; eleven drifted line
citations across `role.yaml`, `main.go`, `plugin.go`, `plugin_pod.go`,
`nginx.conf`, both Dockerfiles, `CompliancePage.tsx`, `RemediationsTab.tsx`,
and `dotenv.ts`; two surfaces the model never named (cluster-wide
`persistentvolumeclaims` and CSV/CatalogSource reads, and the leader-election
Lease plus its separate Role); an imprecise `mayWrite` claim; and the
`consoles.operator.openshift.io` group confirmed against `role.yaml:172-181`
rather than assumed.

The pass that produced this revision re-verified every `main.go` citation and
found the whole flag/serve surface had drifted by 13 to 240 lines: the flag
block, `opts.BindFlags`, the metrics cert-dir default, the listen-address
validator, the non-loopback refusal, the empty-address and empty-probe exits,
both `os.Getenv` and `parseEnvBool` sites, the leader-election ID / release /
shutdown entries, the ConfigMap cache bypass, the healthz/readyz registration,
and `cacheSyncReadyz`. It also corrected four more: a cited function that does
not exist (`validMCPPoolName`; the real name is `validK8sName`, `batch.go:111-116`),
the optional metrics-cert volume (it is `manager.yaml:179-183`, not the
lifecycle block the old citation pointed into), the nginx server block (it runs
to line 145, the end of the file), the operator fuzz-target count (57, not 55),
and the `SPEC.md` / `PATTERNS.md` line numbers for the confirmation-gate claim
(`395` and `155`, not `381` and `138`). Every `role.yaml`, `manager.yaml`
security-context, and `nginx.conf` header citation re-checked clean, as did the
"no `pod-security.kubernetes.io/enforce` label anywhere in the tree" claim
(`rg pod-security operator/config` returns nothing).

No mitigation claim in the tree proved false. The one unsupported claim remains
the unenforced PSS label below, and the `SPEC.md:395` confirmation-gate wording
recorded at the end of §5.

`SECURITY.md` was checked against reality on 2026-09-27 and every claim holds:

- Supported versions: latest published 0.x only. README **Current release**
  (`README.md:10`) and `operator/Makefile` `VERSION` are both `0.6.1`, and
  `CHANGELOG.md` carries a `0.6.1` section. The table does not overstate a
  backport stream that does not exist.
- Supported host: OpenShift 4.22, matching CHANGELOG **Support window** and
  `README.md:279-281`. The enforcement is `minKubeVersion: 1.35.0`
  (`baseline-security-operator.clusterserviceversion.yaml:62`) plus the plugin's
  `@console/pluginAPI` range, not a `com.redhat.openshift.versions` label: no such
  label exists on the CSV, so an install on a newer OCP with a still-1.35 kube
  version is not refused. That is a support claim, not a control.
- Disclosure contact: the CSV `spec.maintainers` email
  (`baseline-security-operator.clusterserviceversion.yaml:64-66`). The address
  exists; `SECURITY.md` does not publish a public issue tracker for
  security-sensitive reports, which is the accurate posture.
- Scope matches what ships: operator, plugin, bundle manifests, metrics scrape.
  The e2e harness is named nowhere in the file and is a developer-workstation
  path, not a shipped one.

`SECURITY.md` names no owner, no SLA, and no advisory identifier process, and
this file does not invent any.

## 8. Response readiness (note only)

Security-relevant actions with no product Event/audit object of their own:

- Remediation apply / unapply / batch (API audit on the CO objects and on `ClusterBaseline` annotations only)
- Waiver add/remove (API audit on `ClusterBaseline`; `requestedBy`/`approvedBy` are not authenticated identity)
- Plugin image change (Deployment env / pod spec)
- Direct writes to `ClusterBaseline.status` (API audit only; nothing records the clamped value the operator substituted)

One audit source is deliberately degraded: the plugin access log records the
request shape and the status, never the client IP, so an investigation into
plugin 4xx/5xx has to start from the console-proxy side or the apiserver audit
log, not from `console-plugin.log`.

Operator logs reconcile errors; metrics/alerts are in `docs/OBSERVABILITY.md`. Log shape is not owned here.

Path from "vulnerability reported" to "fix shipped": `SECURITY.md` (email CSV maintainer → CHANGELOG **Security** heading on the fix release). There is no documented SLA, on-call, or advisory process beyond that.
