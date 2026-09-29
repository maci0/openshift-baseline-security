# Threat model

Last reviewed: 2026-09-29 (against `main` at 0.8.0, `0305f69`).
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
| 2 | Hostile console-plugin image via `RELATED_IMAGE_CONSOLE_PLUGIN` | Build to runtime | Needs write to the operator Deployment env or the CSV that sets it | Arbitrary JS loaded into every admin console session that opens Administration → Compliance | `ValidRelatedImage` in `operator/internal/controller/plugin.go:73` is syntactic only (length, charset). Not a registry allowlist, not a digest pin. |
| 3 | Remediation apply, auto-apply, or batch-apply reboots nodes | Authenticated API user → operator SA | User with `patch` on `ComplianceRemediation` **or** `patch` on `ClusterBaseline` (batch annotation / `spec.remediation.apply`). The four confirmation modals in `RemediationsTab.tsx` are UI-only. | MachineConfigs and node reboots. Batch path also pauses MachineConfigPools. | RBAC (`operator/config/rbac/user_roles.yaml`); CRD default `spec.remediation.apply: Manual` (`operator/api/v1alpha1/clusterbaseline_types.go`); batch count/DNS-1123 guards with limits in `batch.go` enforced in `batch_apply.go`. Modals are not a server control. |
| 4 | ClusterBaseline patch: waivers, schedule, profiles, catalog source | Authenticated API user → operator | User with `update`/`patch` on `clusterbaselines` (`baseline-security-admin` ClusterRoleBinding, or cluster-admin) | Score integrity (waive FAILs), scan enablement, `ScanSetting` schedule, optional auto-apply remediations, CO catalog override | CRD MaxItems/MaxLength/Pattern (`clusterbaseline_types.go`); writes name-scoped to `cluster` (`user_roles.yaml`); no validating admission webhook. Kubernetes API audit is the attribution trail. |
| 5 | Hand-edited `status` wedges every later status write | Operator SA → API server | Needs `update` on `clusterbaselines/status`, or a restore-from-backup of a hostile status | A status that violates the CRD schema fails admission on every subsequent `Status().Update`, freezing conditions, score, and phase for the whole CR. Silent: the object still reads as present. | `operator/internal/controller/sanitize.go` clamps every value written to status to the CRD schema (MaxItems, MaxLength, Enum, Pattern, Minimum; bounds documented at `sanitize.go:62-84`) plus an aggregate serialized-size budget (`conditionsSizeBudget`, `sanitize.go:50`) before the update leaves the reconciler. Recurring class: unbounded untrusted list → schema. |
| 6 | Untrusted Compliance Operator fields rendered in the console | CO CRs → browser | Needs write to ComplianceCheckResult (or similar) in `openshift-compliance`, or a content image that ships hostile description/instruction text | Stored XSS in an admin session if a render path uses HTML; spreadsheet formula injection on CSV export; path injection in deep-links; a spoofed-looking error string rendered as if it were plugin text | React text nodes (no `dangerouslySetInnerHTML`, `innerHTML`, `document.write`, or `eval` anywhere in `console-plugin/src`; the one `document.write` mention is a comment at `CompliancePage.tsx:254` explaining why the blob path replaced it); HTML escape in `console-plugin/src/report.ts:42`; CSV formula hardening in `console-plugin/src/results.ts:80,83`; path-relative hrefs in `console-plugin/src/links.ts:12,18,26`; untrusted values carried in the `UntrustedValue` type (`parse.ts:34-38`) rather than as loose `any`; bidi isolation before interpolation of untrusted text (`text.ts:220`, applied at `TabErrorBoundary.tsx:70`). Recurring class: TEST-PLAN §U still carries XSS and href-injection as open manual checks. |
| 7 | Compliance posture disclosure via metrics | In-cluster network → `/metrics` | Needs a token allowed `get` on nonResourceURL `/metrics`, or access to platform Prometheus | Score, fail counts, last-scan time, batch-active | HTTPS + `filters.WithAuthenticationAndAuthorization` (`operator/cmd/main.go:272`); non-loopback insecure metrics refused (`main.go:208-212`); scraper SA + `baseline-security-metrics-reader` (`operator/config/prometheus/metrics_scraper.yaml`, `operator/config/rbac/metrics_reader_role.yaml`). The cert Secret volume is `optional: true` (`manager.yaml:183`), so a missing Secret leaves the process on its self-signed fallback; the ServiceMonitor pins `serverName`, so the scrape fails closed rather than trusting it. |
| 8 | Operator memory / reconcile exhaustion from check-result volume | CO CRs → operator | Large genuine result sets, or a flood of ComplianceCheckResults the operator lists | Manager OOM or wedged reconcile; stale score (`ComplianceStatusStale`) | `GOMEMLIMIT=440MiB` (`manager.yaml:89-90`) and memory limit 512Mi (`manager.yaml:156-160`); CRD caps on waivers (256), profiles (8), tailored profiles (32); batch caps in `batch.go`; status clamps in `sanitize.go`. No admission quota on foreign CO objects. |
| 9 | In-cluster reachability of plugin :9443 and metrics :8443 | Pod network | A pod in a tenant namespace can TCP to the plugin Service. The operator's metrics port is denied at the network layer. | Plugin: static assets only. | Operator: ingress-only `NetworkPolicy` (`operator/config/manager/networkpolicy.yaml`, selector at :33-35, sole open port at :57-59) denies every operator port except 8443 from `openshift-monitoring` and the service-ca operator. Plugin: no NetworkPolicy; its Service is forced ClusterIP (`plugin.go:265-277`) and nginx serves GET/HEAD only, 1k body, TLS 1.2+ (`console-plugin/nginx.conf`). The plugin pod runs in the operator's own namespace but is selected by `app=baseline-security-console-plugin`, which the operator's NetworkPolicy does not match, and the operator ClusterRole grants no `networkpolicies` rule. |

Gaps ranked above existing controls are 2 (image ref not pinned at deploy time), 3 (the four UI confirm modals are not authz), and 4 (no webhook, waiver fields are free-form). Gap 9 now has a partial control: the operator namespace is covered, the plugin's is not.

One claimed mitigation the code does not support: "Restricted PSS" on the manager pod. The pod spec satisfies Restricted (`runAsNonRoot`, `RuntimeDefault` seccomp, `allowPrivilegeEscalation: false`, drop ALL, read-only rootfs; `manager.yaml:55-58,107-119`) but nothing enforces it, because no `pod-security.kubernetes.io/enforce` label exists on the `openshift-baseline-security` Namespace or anywhere else in the tree (`rg pod-security operator/config console-plugin` returns nothing). A namespace admin can widen the pod spec and the pod will still be admitted. Treat the pod spec as defense in depth, not as an enforced control.

## 1. Attack surface inventory

Not internet sockets. Entry points the code actually has:

| Entry point | Where | Trust of input |
|-------------|-------|----------------|
| OpenShift console → dynamic plugin JS | `console-plugin/src/components/*`, extensions in `console-plugin/console-extensions.json` | Authenticated console user. Data is k8s objects via the console proxy and the user's bearer token (ADR-007 in `docs/DESIGN-DECISIONS.md`). |
| Kubernetes API: `ClusterBaseline` spec, annotations, **and `status`** | `operator/api/v1alpha1/clusterbaseline_types.go`; reconcile in `operator/internal/controller/`; status clamps in `operator/internal/controller/sanitize.go` | Untrusted. Any client with patch on the object; `status` is writable separately and is read back by the reconciler. Includes `baselinesecurity.openshift.io/batch-apply` (`batch.go`). |
| Kubernetes API: Compliance Operator CRs (CheckResult, Scan, Suite, Remediation, Profile, TailoredProfile) | Watched in `operator/internal/controller/`; rendered in `console-plugin/src/{models,results,status,scoring,report}.ts` | Untrusted cluster data (labels, annotations, description, instructions, severity). Ownership filtered by suite label `baseline-<profile>` / `baseline-tp-<name>` (`matching.go`, `isOwnedByBaseline` in `models.ts`). |
| Console writes (user token) | Rescan, profile toggle, schedule, waivers, TailoredProfile authoring, remediation apply/unapply, auto-apply, batch annotation: `docs/SPEC.md` §4.3 and `console-plugin/src/patches.ts` | Authenticated; gated by `mayWrite` (`console-plugin/src/permissions.ts:23`) over `useAccessReview`. The API is the real gate. |
| Manager flags | `operator/cmd/main.go:129-134`: `--metrics-bind-address`, `--metrics-secure`, `--metrics-cert-dir`, `--health-probe-bind-address`, `--leader-elect`, `--version`; `o.zap.BindFlags(fs)` (`main.go:135`) adds the controller-runtime zap set `--zap-devel`, `--zap-encoder`, `--zap-log-level`, `--zap-stacktrace-level`; `clientconfig.RegisterFlags` (`main.go:141`) adds `--kubeconfig` | Deployment/CSV author. Treated as config, not end-user input. `--version` prints the version to stdout and exits 0 before any cluster, config, or port work (`main.go:162-165`), so it is a data path for scripts and support bundles, not a listener. `RELATED_IMAGE_CONSOLE_PLUGIN` is still validated (`ValidRelatedImage`) because a mis-set or hostile env must not become a Deployment. |
| Manager env | `os.Getenv` has exactly two call sites, `plugin.go:47` (via `EnvRelatedImageConsolePlugin`, const at `plugin.go:36`) and `main.go:216` (`RELATED_IMAGE_CONSOLE_PLUGIN`, start-up logging); `BASELINE_SECURITY_SKIP_DEFAULT_CR` is read through `parseEnvBool` (const `main.go:49`, call `main.go:220`, implementation `main.go:422`); the kubeconfig is resolved by `ctrl.GetConfigOrDie` (`main.go`), which reads `--kubeconfig` then `KUBECONFIG` then in-cluster then `$HOME/.kube/config` | Deployment/CSV author. `parseEnvBool` fails start on an unrecognized value and truncates the logged value to 64 chars. `--kubeconfig` is registered explicitly by `clientconfig.RegisterFlags` in `registerFlags` rather than left to that package's `init`, whose registration upstream marks for removal; `TestKubeconfigFlagIsDefined` (`cmd/cli_test.go`) pins the flag so the documented precedence cannot outlive the flag. The start-up `configuration` log records `kubeconfigFlagSet` as a bool, not the path. Not a control; recorded so the next pass does not read the help as the flag surface. |
| Metrics `:8443` `/metrics` | `operator/cmd/main.go:262-273`, Service `operator/config/manager/metrics_service.yaml` | Authenticated scrape (TokenReview + SAR). ClusterIP only. `--metrics-cert-dir` defaults to `/var/run/metrics-certs` (`main.go:131`) and a relative value is rejected at `main.go:200-206`. |
| Leader-election Lease | `main.go:280` `LeaderElectionID: "baseline-security-operator-lock"`; RBAC in `operator/config/rbac/leader_election_role.yaml` (a Role separate from `role.yaml`, so the "no leases rule in role.yaml" reading is correct) | Operator SA. A second replica holding the lease suppresses the other. `LeaderElectionReleaseOnCancel` (`main.go:285`) and a 20s graceful shutdown (`gracefulShutdownTimeout`, `main.go:100`; applied `main.go:290`) bound the handover window. |
| Health `:8081` `/healthz` (startup + liveness) and `/readyz` (cache sync) | registered at `main.go:325-326`; `cacheSyncReadyz` at `main.go:652`; probes in `manager.yaml:129-151` | Unauthenticated ping. Port is **not** on a Service (`metrics_service.yaml` is the only Service in `operator/config`); kubelet to the pod. Readyz also fails once SIGTERM arrives, so a draining pod leaves the endpoints. `--health-probe-bind-address=` empty is a hard exit 2 rather than a silent disable (`main.go:184-187`), because the Deployment still probes :8081. |
| Platform Prometheus scrape | `operator/config/prometheus/servicemonitor.yaml`; the Namespace label `openshift.io/cluster-monitoring: "true"` (`manager.yaml:10`) | The label is what opts the namespace into *platform* monitoring, so this is scraped by cluster monitoring, not user workload monitoring. Without it the ServiceMonitor and PrometheusRule ship with no targets. A cluster-monitoring reader can read everything on `/metrics`. |
| Prometheus service discovery | `operator/config/prometheus/metrics_scraper.yaml:45-71` | A namespaced Role/RoleBinding grants the `prometheus-k8s` SA in `openshift-monitoring` `get/list/watch` on `services`, `endpoints`, `pods` in the operator namespace. It exists only so discovery works, but it is a real cross-namespace RBAC edge to name. |
| Plugin TailoredProfile reads | `console-plugin/src/components/ProfilesTab.tsx:482,625` (`k8sGet`) | A read the UI needs to populate the edit form; adds `tailoredprofiles` to the user's effective read surface. |
| `ClusterScoreItem` watch | `console-plugin/src/components/ClusterScoreItem.tsx:27` (`useK8sWatchResource` on `ClusterBaseline`) | An independent watch independent of the main page tree; its RBAC surface is the baseline read, not the compliance reads. |
| Clipboard copy of a remediation | `console-plugin/src/components/RemediationsTab.tsx:1345` (`navigator.clipboard?.writeText`) | Fully attacker-influenceable text (remediation instructions, labels, annotations) is copied into the operator's clipboard. No HTML parsing, so not XSS, but it is a paste-into-terminal vector and a one-click exfiltration path into a local buffer. The call is optional-chained and branches on success, so a denied clipboard permission is a no-op rather than an unhandled rejection. |
| Plugin nginx `:9443` | `console-plugin/nginx.conf:58-170` (server block; `listen 9443 ssl http2` at :62-63); Service created in `plugin.go:255-286` | TLS ClusterIP. Static files. Console proxy is the intended client. startup/liveness are HTTPS `GET /healthz` (`nginx.conf:105-117`) and readiness `GET /readyz` (`nginx.conf:119-133`) over the serving cert, wired at `plugin_pod.go:141-160` and `plugin_pod.go:196-216`; kubelet does not verify that cert, and a missing cert means nginx never listens, which the HTTP probe (unlike a bare TCP connect) reflects as a real failure. `/readyz` returns 503 unless the worker can read the asset root, so a pod that 404s every asset leaves the endpoints; liveness keeps the constant return because a restart cannot change a baked-in tree. |
| Plugin access log (stdout) | `console-plugin/nginx.conf:37-40` | The `pii_safe` format records method, `$uri`, protocol, status, and body size only. Client IP, `Referer`, `User-Agent`, and the query string are deliberately dropped because the line is collected verbatim into must-gather's `console-plugin.log` and from there into support archives. Consequence for investigation: a 4xx spike cannot be attributed to a source IP from this log alone. |
| Report export (browser) | `console-plugin/src/components/CompliancePage.tsx:246-266` (`buildReportHtml` → `Blob` → `openBlobInTab`, `downloadBlob` on popup block) | The exported HTML is built from untrusted CO text and then **executed in a new tab in the operator's browser session** by an explicit click. This is the one place the plugin hands attacker-influenceable markup to a document; it rests entirely on `report.ts` escaping. |
| Default CR creator | `operator/internal/controller/default_cr.go` (leader-elected `ClusterBaseline/cluster`) | Operator SA. Opt-out env `BASELINE_SECURITY_SKIP_DEFAULT_CR`. |
| OLM / catalog / CSV | `operator/bundle/manifests/baseline-security-operator.clusterserviceversion.yaml` | Install-time. Sets image refs, RBAC, env. |
| CI release publish | `.github/workflows/release.yml` | `workflow_dispatch` version is passed through an env var (fixed after command-injection in 0.5.11). Quay credentials are GitHub secrets. |
| e2e `.env` loader (developer workstation only) | `console-plugin/e2e/dotenv.ts:78` (`loadDotEnv`) over `parseDotEnv` (`dotenv.ts:23-76`) | Reads a developer-supplied file into `CONSOLE_URL`, `KUBEADMIN_USER`, `KUBEADMIN_PASSWORD`, `SCREENSHOT_DIR`. Key allowlist at `dotenv.ts:12-17`; every unknown key, duplicate, non-`KEY=value` line, and unterminated quote is collected and raised as one hard error (`dotenv.ts:72-74`) rather than a silent drop, so the runner cannot inherit `PATH`, `NODE_OPTIONS`, or an unrelated variable. This is the only place a password is read from disk, and it is a dev-only path, not a shipped one. |
| e2e Playwright storage state (developer workstation only) | `console-plugin/e2e/global-setup.ts:57-68` | Holds a live kubeadmin console session cookie, written to `console-plugin/e2e/.auth/state.json`. This is the highest-value secret the product touches and it is the only one written to disk in cleartext; the directory is created `0o700` (`:57`) and the file is created `0o600` (`:65`) **before** `storageState` truncates it, so it is never briefly world-readable under the process umask. The trailing `chmod 0o600` (`:68`) still tightens a file left by an older run. Gitignored (`console-plugin/.gitignore:1`). Dev-only: the e2e harness ships in no image and runs from a workstation. |
| Console tab render-error path | `console-plugin/src/components/renderError.ts:16-25`, boundary at `TabErrorBoundary.tsx:34-82` | The console mounts an extension page with no error boundary of its own, so a throw inside a tab body used to blank the page and discard the stack. `TabErrorBoundary` now catches it, logs via `reportRenderError`, and renders `"<component>: <reason>"` into an `Alert`. The reason can be an apiserver error string that quotes an attacker-influenced object name or field value, so this is a new untrusted-text path into the DOM. It is a React text node interpolated by i18next (`TabErrorBoundary.tsx:70-78`, `t('Reason: {{detail}}', …)`) after a bidi isolate, and the component name is one of four literals, so it is display-only. The value of the path is diagnosability: the stack now reaches the browser console, which a support bundle collects. |
| Cron schedule | `spec.schedule` → owned `ScanSetting` (`operator/internal/controller/schedule.go`, `scanconfig.go`) | Untrusted CR field; five-field cron only. Invalid → `InvalidSchedule` Degraded, not a panic. |

Absent from the code (do not model as present):

- No admission webhook server, no conversion webhook, no mutating webhook. The
  only CR-level validation is the `ClusterBaseline` OpenAPI schema.
- No operator REST API, no gRPC, no webhook receiver, no upload parser.
- No NetworkPolicy for the console-plugin pod. The operator namespace has one
  (`operator/config/manager/networkpolicy.yaml`) selecting
  `app=baseline-security-operator`; the plugin pod shares that namespace under
  `app=baseline-security-console-plugin` and is not selected, and the operator
  ClusterRole has no `networkpolicies` rule to add one (the policy's own header
  says so at `networkpolicy.yaml:16-17`).
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
| CO CRs → operator/plugin | Labels, descriptions, timestamps, remediation objects | Narrowed at the boundary: unstructured helpers, DNS-1123 checks, `isOwnedByBaseline`, and a distinct `UntrustedValue` type in the plugin (`parse.ts:34-38`). Not treated as trusted. |
| Tenant → tenant | N/A (single cluster, one `ClusterBaseline/cluster`) | Isolation is Kubernetes RBAC, not a product tenancy layer. |
| Build → runtime | Images, CSV, `RELATED_IMAGE_*` | Digest-pinned Dockerfiles; OLM relatedImages; `ValidRelatedImage` (syntax). |
| Secrets → code | Service-ca TLS files; scraper SA token Secret; operator SA token; the e2e `.env` and Playwright `state.json` on a developer workstation | Operator ClusterRole has no `secrets` verbs. Certs are volume-mounted. Plugin sets `automountServiceAccountToken: false` and `ServiceAccountName: "default"` (`plugin_pod.go:88-96`). The e2e loader allowlists four keys and never dumps its map (`dotenv.ts`); the session file it derives is created `0o600` in a `0o700` directory (`global-setup.ts:57-68`). |

Privilege transitions the model must keep:

1. **Batch-apply confused deputy.** A client with only `ClusterBaseline` patch sets `baselinesecurity.openshift.io/batch-apply`. The operator then `get`/`patch`es MachineConfigPools and `patch`es ComplianceRemediations (`batch_apply.go`, `batch_reconcile.go`). The user typically cannot pause MCPs themselves. The console reviews for batch apply and auto-apply cover both the baseline patch and the remediation patch (`canDelegateRemediationApply`, `RemediationsTab.tsx:245-252`), so the UI does not offer a request the caller could not make per item; the API is still the real gate for anyone who edits the CR directly.
2. **Auto-apply.** `spec.remediation.apply: Automatic` is reconciled onto `ScanSetting.autoApplyRemediations` (`scanconfig.go`). Subsequent CO remediations apply without a per-item UI confirm.
3. **Console registration.** Operator patches `consoles.operator.openshift.io/cluster` `spec.plugins` (`plugin.go`).
4. **Default CR.** Leader creates `ClusterBaseline/cluster` (`internal/controller/default_cr.go`), which starts CIS scanning.

Secrets flow:

- Enter: service-ca annotation on the metrics and plugin Services; OLM/kubelet injects TLS Secrets as volumes. The metrics cert volume is `optional: true` (`manager.yaml:183`), so the process can run on the self-signed fallback identity in `metrics_cert.go`; the ServiceMonitor pins `serverName` so the scrape fails closed. Scraper token Secret is declared in `metrics_scraper.yaml:15-22`. GitHub Actions hold `QUAY_USERNAME` / `QUAY_TOKEN` (build only). A developer's `console-plugin/.env` supplies `KUBEADMIN_PASSWORD` to the Playwright runner; process env wins over the file, and the file is never a fallback for a key CI injects.
- Live: files under `/var/run/metrics-certs` and `/var/serving-cert`. Operator SA token is automounted for API calls. Plugin does not automount a token and has no pull secrets of its own (`plugin_pod.go:88-96`).
- Leave: metrics scrape uses the scraper Secret as a Bearer token. No product code copies cluster secrets into `ClusterBaseline` status. `main.go:225-241` logs related-image set/valid booleans, not the ref path. The plugin logs no client identity by design (see §1).
- Developer workstation: the e2e runner holds two credentials at once, the kubeadmin password from `console-plugin/.env` and the session cookie it mints from it in `console-plugin/e2e/.auth/state.json`. The cookie is the derived, longer-lived one; the `.env` key allowlist (`dotenv.ts:12-17`) does not cover this file, and its protection is the `0o600` create plus the gitignore. Neither is in a shipped image.

Rotation: service-ca rotation is the platform's; the metrics cert reloads through `metricsCertProvider.GetCertificate` (`operator/cmd/metrics_cert.go:89`), which is called from concurrent TLS handshakes. No application-level credential rotation.

## 3. Assets and impact

| Asset | What happens if stolen / corrupted / denied | Where it lives |
|-------|---------------------------------------------|----------------|
| Node configuration and uptime | Applied remediations render MachineConfigs and reboot nodes | CO `ComplianceRemediation` + MCP pause/resume |
| Admin browser session | Hostile plugin JS runs as the logged-in console user | ConsolePlugin backend + plugin image |
| Compliance score and history | False PASS/waiver theater; auditors trust `status.score` / reports | `ClusterBaseline` status; Prometheus gauges in `metrics.go` |
| Scan enablement and schedule | Scans stopped (`spec.profiles: []`) or hammered via cron | `ClusterBaseline` spec → `ScanSetting` |
| Operator SA privileges | Full product blast radius. Two rules carry most of it: `machineconfigpools` `get,patch` is cluster-wide with **no `resourceNames`** (`role.yaml:165-171`), and `scansettingbindings` has create/delete/get/list/patch/update in `openshift-compliance` (`role.yaml:113-123`), so a stolen token can repoint the `baseline` ScanSetting. Also `namespaces` create, `subscriptions` create, `operatorgroups` create. | `operator/config/rbac/role.yaml` bound cluster-wide |
| Cluster-wide read surface the operator does need | `compliance_operator.go:321-359` deliberately pages a **cluster-wide** CSV List on a fallback path (a `CSVCatalog` CR carries the full install spec), reading only name, namespace, and status.phase, with a page cap (`csvListMaxPages`, `compliance_operator.go:290`) and a repeated-token guard (`:355`). The matching `clusterserviceversions`/`catalogsources` get/list/watch (`role.yaml:182-190`) is what makes that lookup possible. | A stolen token reads the installed-operator inventory: every CSV, its version, channel, and image reference. That is a supply-chain reconnaissance surface, accepted knowingly and bounded by the page cap. |
| Cluster-wide grants the operator never uses | `persistentvolumeclaims` list+watch (`role.yaml:24-30`), `services` create+list/watch (`role.yaml:31-38`), `poddisruptionbudgets` create+list+watch (`role.yaml:215-222`). Every corresponding code path is namespace-scoped (`scanstorage.go:26`, `plugin_pod.go`, `plugin.go:256,288`) and the manager cache is already scoped to two namespaces. | Pure excess authority on a stolen token: cluster-wide PVC inventory, the ability to create Services anywhere, and a PDB-create lever that can block a legitimate node drain. Scoping fix belongs to authz-review. |
| Platform monitoring config | Repointing or silencing the scrape and alerts that expose a cooked score | `servicemonitor.yaml`, `metrics_scraper.yaml`, `prometheusrule.yaml`, the `openshift.io/cluster-monitoring` Namespace label |
| Console operator plugin list | Plugin removed or extra plugins injected if the patch is broader than intended | `consoles.operator.openshift.io/cluster` |
| Metrics | Disclosure of fail counts and score to anyone who can scrape | `/metrics` |
| The operator's own status write path | A hostile or corrupt status blocks every later update, so the product reports nothing rather than something wrong | `ClusterBaseline.status`, guarded by `sanitize.go` |
| Availability of the plugin and operator | Compliance UI gone; scans unconfigured; `Degraded` | Deployments in `openshift-baseline-security` |
| Administrator identity, indirectly | The plugin access log deliberately carries no client IP, so a support archive cannot be traced back to a person; conversely a stolen log leaks nothing about who browsed | `console-plugin/nginx.conf:37-40` |
| A live console session cookie | Read from `console-plugin/e2e/.auth/state.json`, it is a kubeadmin session for the developer's cluster. The `.env` password is recoverable from it and the cookie works until the session expires. Local-file read, so it is a developer-workstation threat, not a cluster one. | `console-plugin/e2e/.auth/state.json`, created `0o600` in a `0o700` dir (`global-setup.ts:57-68`), gitignored |

Reputation: a cooked score or a remediation-driven outage is attributed to "the compliance operator / baseline" by operators. That is the concrete blast radius, not a generic "data breach". This product does not store customer PII; waiver `reason` / `requestedBy` / `approvedBy` are free-text a customer might put names into, and that text is reachable by anyone with viewer rights and by every report export. What the code does about it is narrow and worth stating precisely, because it is not PII handling: browser autofill is switched off on the waiver `reason` textarea and the two attribution inputs (`ResultsTab.tsx:1211,1235,1249`), so the console does not volunteer the operator's own name into a field that is about to be persisted cluster-wide. Free text still reaches the CR and the report.

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
| Tampering | Foreign suite labels must not enter the score. Mitigated by `ownedSuites` / `matchesAnyProfile` (`matching.go`) and `isOwnedByBaseline` (`models.ts`). Recurring: 19 fuzz targets in `matching_test.go` plus 13 in `fuzz_extra_test.go`. |
| Information disclosure / XSS | `description` and `instructions` are untrusted. Modal uses text (`ResultsTab.tsx` `Content` / pre-wrap). Report HTML escapes (`report.ts:42`). CSV formula prefix (`results.ts:80,83`, CWE-1236). Deep-links are path-relative (`links.ts`). The render-error path added after an earlier pass (`renderError.ts`, `TabErrorBoundary.tsx:70-78`) is a fourth such route: an apiserver error string reaches the DOM as an i18next-interpolated React text node, never as HTML, bidi-isolated first, and the component half of the message is a literal. |
| Spoofing of plugin text | Untrusted cluster text rendered in plugin chrome (a tab error reason, a check title) can be styled to look like a plugin sentence. `bidiIsolate` (`text.ts:220`) pins the direction so an RTL run cannot reorder the surrounding label, and the interpolation stays inside one text node. Display-only, and named here because it is the class that recurs whenever a new untrusted string is put into a translated sentence. |
| Denial of service | Unstructured maps from CO objects: the batch path avoids `NestedMap` (which DeepCopyJSON-panics on non-JSON types) in favor of `NestedFieldNoCopy` + type assert (`batch.go` `poolFromRemediation`). Huge result lists and a hostile status are the residual DoS. |
| Elevation | Hostile remediation labels driving MCP names: non-DNS-1123 dropped by `poolFromRemediation` (`batch.go:77-105`, `validK8sName` at `batch.go:112-117`), re-checked in `batch_reconcile.go`. |

### Operator process (in-cluster listeners)

| Class | Concrete threat |
|-------|-----------------|
| Spoofing | Metrics without authn. Mitigated: default `--metrics-secure=true`; non-loopback insecure forced back to secure (`main.go:208-212`, loopback table test at `metrics_cert_test.go:215`). |
| Information disclosure | `/metrics` with a stolen scraper token or overly broad `get` on `/metrics`. Health probes are unauthenticated but not Service-exposed. |
| Denial of service | nginx `client_max_body_size 1k` (`nginx.conf:80`) and GET/HEAD only (`nginx.conf:135-139`). Metrics and probe bind addresses are validated (`validateListenAddr`, `main.go:374`); an empty probe address is a hard exit rather than a never-ready pod, and a relative `--metrics-cert-dir` is rejected (`main.go:200-206`). Empty metrics addr is restored to `:8443` (`main.go:178-181`) rather than controller-runtime's `:8080`. Recurring class: a hostile or buggy apiserver that returns a repeated `continue` token would spin a paged List until the reconcile deadline. Guarded at all four paged call sites by a token-advance check (`nextPageToken`, `internal/controller/unstructured.go:70-75`; inlined in the CSV walk at `compliance_operator.go:355`) and covered by `unstructured_test.go`. |
| Elevation | `--leader-elect=false` on a 2-replica Deployment races default-CR create (`main.go:242-245` logs a warning). |
| Resource exhaustion | The manager informer cache is cluster-wide per type unless scoped, so a typed read caches every object of that type in the cluster. Scoped by `controller.ManagerCacheOptions()` (`internal/controller/managercache.go:27`): plugin Deployment/Service/PDB in `openshift-baseline-security`, scan-storage PVCs in `openshift-compliance`. Named ConfigMap reads bypass the cache entirely (`main.go:302-310`, `DisableFor: []client.Object{&corev1.ConfigMap{}}` at :308), so the dashboard CM costs one `get` and no `list/watch`. RBAC still grants cluster-wide `list`/`watch` on the four scoped types, which is the residual edge. |

### Build → runtime

| Class | Concrete threat |
|-------|-----------------|
| Tampering | Substituted operator or plugin image. Recurring: `workflow_dispatch` shell injection (fixed 0.5.11 by env-passing the version). `ValidRelatedImage` does not pin digest or registry. A compromised npm package with a `postinstall` cannot run during `yarn install` (`enableScripts: false` in `console-plugin/.yarnrc.yml:11`; image `YARN_ENABLE_SCRIPTS=false`). |
| Denial of service | Standard-library infinite loop on invalid input via status text (fixed: `golang.org/x/text` bump, 0.5.9). Recurring class: untrusted string → parser, now including the status sanitizer. 60 fuzz targets exist across 16 files; `make fuzz` is a release gate, not a per-PR one. |

## 5. Mitigations mapping

Existing controls, with the threats they cover:

| Control | File | Covers |
|---------|------|--------|
| User-token data path, no plugin backend | `docs/DESIGN-DECISIONS.md` ADR-007; plugin uses SDK hooks | Plugin cannot act beyond the user's RBAC |
| Viewer aggregated to view/cluster-reader; admin ClusterRole not aggregated | `operator/config/rbac/user_roles.yaml` (no `aggregationRule` block exists) | Readers get results without extra bindings; namespace admin of `openshift-compliance` does not inherit node-reboot writes |
| Operator ClusterRole without secrets/nodes/exec | `operator/config/rbac/role.yaml` (no such rule); leases live in a separate Role at `config/rbac/leader_election_role.yaml` | Limits blast radius of SA theft vs cluster-admin. Does **not** bound `machineconfigpools` patch (cluster-wide, no `resourceNames`), `scansettingbindings` CRUD, the deliberate cluster-wide CSV fallback read, or the three unused cluster-wide grants named in §3. The manager *cache* is namespace-scoped (`ManagerCacheOptions`, `internal/controller/managercache.go:27`) but the *RBAC* is wider, so the cache scope is a memory optimization, not a privilege boundary |
| Pod spec satisfies Restricted PSS; read-only rootfs, drop ALL, non-root | `manager.yaml:55-58,107-119`, `plugin_pod.go:166-180`, `operator/Dockerfile:57` `USER 65532:65532`, `console-plugin/Dockerfile:72` `USER 1001:1001` | Container breakout cost. **No `pod-security.kubernetes.io/enforce` label exists in the repo**, so this is admitted-anyway posture, not an enforced control |
| Plugin: no automount SA token, no pull secrets, no host namespaces | `plugin_pod.go:88-96` | Plugin pod is a static file server. The Service is re-coerced to ClusterIP and every external-exposure field is cleared on every pass (`plugin.go:265-277`, inside a `CreateOrUpdate` mutate func at `plugin.go:257-286`) |
| Plugin access log carries no client identity | `console-plugin/nginx.conf:37-40` | Personal data does not reach must-gather or support archives. Cost: the log cannot attribute a 4xx to a source |
| Status sanitizing layer | `operator/internal/controller/sanitize.go`; entry point `sanitizeStatusForUpdate` (`sanitize.go:86`), clamps mirror the CRD markers in `clusterbaseline_types.go`, aggregate budget `conditionsSizeBudget` (`sanitize.go:50`) | A hostile, hand-edited, or restored status cannot wedge `Status().Update` and freeze every later write |
| `resourceVersion`-guarded console patch ops | `console-plugin/src/patches.ts:42` (`resourceVersionTest`), emitted by `tailoredProfileBindingPatch` (`:51`), `batchApplyPatch` (`:96`), `addWaiverPatch` (`:205`), `removeWaiverPatch` (`:270`), `rescanPatch` (`:299`) | Replay of a stale write. The batch annotation is a one-shot request the operator consumes and clears, so a resubmit built from a stale read would otherwise re-pause pools and re-apply remediations, meaning two reboots; the guard turns that into a 409 the caller surfaces. On the waiver create shape, RFC 6902 `add` on a member *replaces* it, so a second submit reading the list as absent would drop the first submit's waiver. The test is emitted by the builder, not the call site, so an unguarded builder is not reachable |
| `UntrustedValue` as a distinct type at the plugin boundary | `console-plugin/src/parse.ts:34-38`; consumed in `errors.ts`, `contentKey.ts`, `profiles.ts`, `remediation.ts`, `scoring.ts`, `chunkLoad.ts`, `renderError.ts`, `TabErrorBoundary.tsx` | Untrusted cluster values cannot be passed to a consumer as an already-validated `string` without a visible narrowing step. Narrows at the boundary rather than re-checking per render |
| Bidi isolation before interpolating untrusted text | `console-plugin/src/text.ts:220`; applied at `TabErrorBoundary.tsx:70` | A hostile RTL run in untrusted cluster text cannot reorder the plugin's own label around it, so the text cannot be made to read as plugin chrome |
| Metrics HTTPS + TokenReview/SAR; insecure non-loopback refused | `operator/cmd/main.go:262-273,208-212`; `metrics_auth_role.yaml` | Unauthenticated metrics scrape |
| CRD MaxItems/MaxLength/Pattern | `clusterbaseline_types.go`: waivers 256, profiles 8 plus a `ProfileKey` enum, tailored profiles 32, schedule MaxLength 128, catalog source MaxLength 253 + DNS-1123-ish pattern (deliberately no enum so unset auto-detect still works) | CR bloat, junk catalog names, junk waiver names |
| Suite-label ownership filter | `matching.go`, `models.ts` `isOwnedByBaseline` | Foreign CO results in score/UI |
| DNS-1123 + count guards on batch-apply | `patches.ts:19` (`batchApplyMaxNames`, the same ceiling the operator enforces), `batch.go:77-117` | Hostile annotation pausing MCPs / wedging reconcile |
| `ValidRelatedImage` | `plugin.go:68-73` | Shell metacharacters / huge env in image ref |
| Plugin nginx: TLS1.2+, no tickets, nosniff, DENY frame, CSP `default-src 'none'`, GET/HEAD, 1k body, `server_tokens off`, `Referrer-Policy no-referrer`, `Permissions-Policy`, HSTS, `Cross-Origin-Resource-Policy`/`-Opener-Policy same-origin`; plaintext 8080 explicitly excluded so the S2I snippets cannot add one | `console-plugin/nginx.conf:48-103` (server block 58-170; the 8080 exclusion rationale at :55-56) | Direct hits on the plugin Service |
| Report export via `Blob` + `openBlobInTab` (opener dropped, revoked on every path, falls back to download on popup block) | `CompliancePage.tsx:246-266`, `download.ts:59,94` | Window-opener takeover and silent no-op from the one path that hands untrusted text to a browser document |
| React text rendering; report `esc()`; CSV formula prefix; `safeDownloadName` | `ResultsTab.tsx`, `report.ts:42`, `results.ts:80,83`, `download.ts:33` | XSS / CWE-1236 / download path |
| Every console write gated by `mayWrite`, pinned by a source-walking test | `console-plugin/src/permissions.ts:23`; `console-plugin/src/writeGates.test.ts:52` | A new mutation added without its access check. The gate itself remains client-side (see §6.6); what the test removes is the regression, not the limitation. |
| 60 operator fuzz targets plus a deterministic sweep in the plugin | `operator/{cmd,internal/controller}/*_test.go` (16 files), `console-plugin/src/{fuzz.test.ts,report.test.ts,links.test.ts}` | Recurring parser panics. `report.ts` and `links.ts` are covered by seeded sweeps in their test files (which include `<img src=x onerror=alert(1)>` and `javascript:` payloads) rather than by a Go-style fuzz target |
| e2e `.env` key allowlist with hard errors | `console-plugin/e2e/dotenv.ts:12-17,23-76`, `dotenv.test.ts` | A typo'd key or an unrelated variable silently inheriting into the runner (which would also pull in `KUBEADMIN_PASSWORD` where it was not meant to go) |
| e2e session file created owner-only before it is written | `console-plugin/e2e/global-setup.ts:57-68` | Another local user reading a live kubeadmin session cookie out of a world-readable file for the duration of the write |
| Hermetic, digest-pinned image builds (`--network=none`, `GOPROXY=off`, lockfile, digest bases) | `operator/Dockerfile`, `console-plugin/Dockerfile`, `operator/catalog.Dockerfile` | Build-time supply chain for released images. `operator/Dockerfile.ci` is tag-pinned against `registry.ci.openshift.org`, not digest-pinned; it never ships |
| Yarn install without lifecycle scripts | `console-plugin/.yarnrc.yml:11` `enableScripts: false`; `console-plugin/Dockerfile:12` `YARN_ENABLE_SCRIPTS=false` | Compromised registry package cannot run `preinstall`/`install`/`postinstall` |
| Release version not interpolated into `run:` | `.github/workflows/release.yml` | Workflow command injection |

Threats with no (or only UI) mitigation:

| Threat | Rank | Why it stays |
|--------|------|----------------|
| Arbitrary plugin image if Deployment env is writable | High | `ValidRelatedImage` is not an allowlist |
| Remediation apply via API, skipping the modals | High | By design (ADR-007); RBAC is the control. Docs that call the modal a security gate overstate it. |
| Operator SA is a single point of failure for MCP + scansettingbindings + console + CO install | High | One ClusterRole carries several high-impact writes; the MCP patch is cluster-wide and unname-scoped |
| Unused cluster-wide grants on the operator SA: `persistentvolumeclaims` list+watch, `services` create+list/watch, `poddisruptionbudgets` create+list+watch | High | `role.yaml:24-30,31-38,215-222`. Every code path that touches these is namespace-scoped and the manager cache is already scoped to two namespaces, so these grants buy nothing and cost a stolen token cluster-wide PVC enumeration plus a node-drain lever. Scoping fix handed to authz-review. |
| Cluster-wide CSV fallback read discloses the installed-operator inventory | Medium (accepted) | Deliberate and page-capped (`compliance_operator.go:321-359`). Recorded so a later pass does not "fix" it into a narrower grant that breaks the OKD/disconnected install path. |
| No `pod-security.kubernetes.io/enforce` label | Medium | The pod spec satisfies Restricted but nothing enforces it; a namespace admin can widen it and the pod still schedules |
| Waiver attribution spoof | Medium | Fields are spec strings; not bound to the user token |
| No NetworkPolicy on the console-plugin pod | Medium | Any pod can reach the plugin ClusterIP; the operator namespace is covered by `config/manager/networkpolicy.yaml` |
| No validating webhook | Medium | Schema-only; `Automatic` remediations are a legal spec |
| Direct status write by a client with status RBAC | Low | Clamped on the operator's side, but a third-party writer's value is only corrected on the next reconcile |
| No Kubernetes Events on apply/waive | Low (investigation) | API audit exists cluster-wide; product emits none |

`docs/SPEC.md:409-410` calls remediation apply "confirmation-gated and RBAC-gated
(user token + admin role + modal)" and `docs/PATTERNS.md:155-157` says dangerous
actions are "opt-in, confirmation-gated, and RBAC-gated" (restated at
`docs/PATTERNS.md:159-161`). The confirmation gate is the four modals in
`RemediationsTab.tsx` (apply, unapply, batch, auto-apply). None of them run on
`oc patch`. Treat RBAC as the control; treat the modals as UX. Those two docs
are owned elsewhere; the claim is recorded here so sec-review can aim at it.

`docs/SPEC.md:411-412` also claims the plugin loads no external sources so the
console default CSP applies unmodified. Verified: no `fetch`, `axios`,
`XMLHttpRequest`, `src=` from data, or absolute href anywhere in
`console-plugin/src`. That claim holds today and is load-bearing.

## 6. Abuse cases

Hostile but authenticated. Enabling path named. Not demonstrated.

1. **Compliance theater.** User with `ClusterBaseline` patch adds waivers for every FAIL (up to 256) with a far-future `expiresAt`. Score climbs; Prometheus `ComplianceScoreLow` quiets. Path: `ResultsTab.tsx` → `addWaiverPatch` (`patches.ts:205`) → CR spec; operator scoring in `aggregate.go` / `scoring.go`. Attribution fields can name someone else.
2. **Node reboot without the modal.** `oc patch complianceremediation … --type merge -p '{"spec":{"apply":true}}'` with `baseline-security-admin` (or cluster-admin). Path: CO applies; this operator is not in the loop. Same for `spec.remediation.apply: Automatic` on the CR, which the operator copies onto `ScanSetting`. A RoleBinding to `admin` in `openshift-compliance` is not enough.
3. **Batch pause as deputy.** Patch annotation `baselinesecurity.openshift.io/batch-apply` with owned remediation names. Operator pauses MCPs (`batch_apply.go`). The sibling `baselinesecurity.openshift.io/batch-pools` annotation is the same kind of input: it is read back when a status-lost batch re-opens, and a name in it is only paused when the surviving remediations derived it or when the pool is already paused carrying this batch's own pause marker (`poolPausedBy`, `batch_reconcile.go`), so a CR author with no MachineConfigPool verb cannot name an unrelated pool. A 10-minute grace (`batchResumeGrace`, `batch.go:47`) resumes the pools even if apply never completes, and a zero or far-future `StartedAt` is treated as garbage rather than disabling the valve forever. The guards are count-based, not per-item: more than 256 remediations, or **one** non-DNS-1123 name in the list, aborts the whole batch and clears the annotation (`batch_apply.go`) rather than skipping the bad entry, so a partial pause is not reachable through this path. A double submit is now a 409 rather than a second pause-and-reboot, because every annotation shape carries the `resourceVersion` test (`patches.ts:42,96`).
4. **Stop scanning, keep the UI.** `spec.profiles: []` and empty tailored list prunes bindings and clears the score (`clusterbaseline_types.go` comment). The CR remains; Overview shows an empty baseline rather than an uninstall.
5. **Freeze the score by poisoning status.** A writer with `clusterbaselines/status` writes a condition list that is pattern-valid per entry but exceeds the aggregate size budget, or a count outside its `Minimum`. Any unclamped write would fail admission and wedge the CR. Enabling path today: the writer calls the API directly, not the operator. `sanitize.go` is why the operator cannot be the one to do this by accident; the third-party writer is bounded only on the next reconcile.
6. **Client-side enforcement.** One `useAccessReview` result feeds two consumers. Every mutation calls `mayWrite` (`console-plugin/src/permissions.ts:23`) immediately before it sends, and no unguarded write exists in the plugin: `BaselineNotConfigured.tsx:37`, `CompliancePage.tsx:156`, `Overview.tsx:247`, `ProfilesTab.tsx:535,772,830`, `RemediationsTab.tsx:401,439,469`, `ResultsTab.tsx:285`. That claim is now enforced rather than asserted: `console-plugin/src/writeGates.test.ts:52` walks the source of every component that calls a `k8s` mutation and fails if one sends without a `mayWrite` guard in the same function. Separately the same review values are read by each control to compute its own `disabled` prop (`RemediationsTab.tsx:533,547`, `ResultsTab.tsx:313`, `ProfilesTab.tsx:961`, `CompliancePage.tsx:316,327`); that is a second read of the same hook, not a second call through `mayWrite`. The re-check before send is what stops a modal, editor, or form held open across a revocation from spending the request the button had already admitted; an unresolved review denies. This remains client-side: a user who bypasses the UI with the same token gets the API's decision, not the plugin's. The write still goes to the API server, which authorizes independently.
7. **Replay a one-shot write.** A user with `ClusterBaseline` patch holds a terminal open across a batch apply, or double-clicks Add waiver before the watch delivers the first write. Without a `resourceVersion` test the second request is accepted and, on the waiver create shape, replaces the whole list. The builder now emits the test on every shape (`patches.ts:42`), so the only way to get this back is to call the API directly, which is the same trust level as any other patch. Named as a scenario because it is a *lost update* against the operator's own annotation contract, not a privilege escalation.
8. **CSV / HTML export of hostile rule text.** Export is client-side (`results.ts`, `report.ts`, `download.ts`) and the HTML opens in a new tab in the operator's session (`CompliancePage.tsx:246-266`). Hardening is in those files; a regression would execute in the admin's spreadsheet or browser, not on the operator.
9. **Clipboard as a paste-into-terminal vector.** A user with remediation read copies a `ComplianceRemediation` whose instructions or labels carry attacker-chosen text (`RemediationsTab.tsx:1345`), then pastes it into a shell or a support ticket. The plugin's own controls do not cover the destination. The blob paths are hardened; the clipboard is a raw passthrough.

## 7. Document quality

This file is current as of the date above. Every file reference in it was
re-read against that commit on that date. Re-check on any change to RBAC, plugin
nginx, metrics flags, `RELATED_IMAGE_*`, remediation/batch paths, the status
sanitizing layer, Namespace labels, CRD validation, the paged-List guards in
`internal/controller/unstructured.go`, the console patch builders in
`console-plugin/src/patches.ts`, or the operator's own flag and help surface in
`cmd/main.go`.

The previous pass found and closed: a stale commit stamp; eleven drifted line
citations across `role.yaml`, `main.go`, `plugin.go`, `plugin_pod.go`,
`nginx.conf`, both Dockerfiles, `CompliancePage.tsx`, `RemediationsTab.tsx`,
and `dotenv.ts`; two surfaces the model never named (cluster-wide
`persistentvolumeclaims` and CSV/CatalogSource reads, and the leader-election
Lease plus its separate Role); an imprecise `mayWrite` claim; and the
`consoles.operator.openshift.io` group confirmed against `role.yaml:172-181`
rather than assumed.

This pass re-verified the whole file after two releases and 38+ commits landed
since the previous stamp (`ef6f499`, 0.6.1 → `0305f69`, 0.8.0). Drift was
concentrated in `cmd/main.go` and the console plugin, both of which grew
substantially. Corrected here:

- The commit stamp and every version reference (0.6.1 → 0.8.0).
- The entire `cmd/main.go` citation set, which had moved by 13 to 240 lines:
  the flag block (`129-134` plus `BindFlags` at `135` and the explicit
  `clientconfig.RegisterFlags` at `141`), the empty-address and empty-probe
  exits, the metrics cert-dir validation, the non-loopback refusal, both
  `os.Getenv` and `parseEnvBool` sites, the leader-election ID / release /
  shutdown entries, the ConfigMap cache bypass, the healthz/readyz
  registration, `validateListenAddr`, and `cacheSyncReadyz`.
- `plugin.go`: the env constant and reader (`:36`, `:47`), `ValidRelatedImage`
  (`:73`), and the Service `CreateOrUpdate` (`:257-286`, ClusterIP coercion at
  `:265-277`). Two old citations (`:119`, `:301`) named code that is no longer
  there; the namespace-scoped paths they stood for are `plugin.go:256,288`.
- `plugin_pod.go`: the probe handlers (`:141-160`) and the probe objects
  (`:196-216`) are separate from the security context (`:166-180`).
- `compliance_operator.go`: the cluster-wide paged CSV fallback is `:321-359`,
  not `:274-300`; the namespaced read it falls back from is `:306-315`; the
  inlined repeated-token guard is the single condition at `:355`.
- `manager.yaml`: the container security context starts at `:107`, probes at
  `:129-151`, limits at `:156-160`, and the optional metrics cert volume at
  `:180-189` (`optional: true` at `:183`).
- `metrics_scraper.yaml`: the discovery Role/RoleBinding is `:45-71`.
- `nginx.conf`: the server block runs to `:170` (the file is 171 lines, and
  `http {}` closes at 171), the hardened header block is `:48-103` with
  GET/HEAD at `:135-139`, and `location /healthz` is `:105-117`.
- The loopback test is `metrics_cert_test.go:215` (`TestIsLoopbackMetricsAddr`),
  not `:634`, which is a fuzz seed inside `FuzzMetricsCertCorruptPair`.
- Every console-plugin component citation moved with plugin growth: the nine
  `mayWrite` chokepoints, the access-review-derived `disabled` props, the
  clipboard copy (`:1218`→`:1345`), the `ClusterScoreItem` watch (`:26`→`:27`),
  the `ProfilesTab` `k8sGet` reads (`:460,600`→`:482,625`), and the report
  export.
- The e2e session-file creation is `global-setup.ts:57-68`, and the `.env`
  reader is `loadDotEnv` at `dotenv.ts:78` over `parseDotEnv` at `:23-76`.
- Both Dockerfiles: `operator/Dockerfile:57`, `console-plugin/Dockerfile:72`.
- The operator fuzz-target count (60 across 16 files, not 58), which was
  restated in four places.
- The `SPEC.md` / `PATTERNS.md` line numbers for the confirmation-gate claim
  (`:409-410` and `:155-157`, not `:395` and `:155`), and for the no-external-
  sources CSP claim (`:411-412`).
- `README.md:279-281` for host support is `:287-289`.

`role.yaml` was re-read rule by rule and every citation still resolves: the
three unused cluster-wide grants, the `machineconfigpools` rule with no
`resourceNames`, the name-scoped PDB, the name-scoped `consoles/cluster` rule,
and the continued absence of any `secrets`, `nodes`, or `exec` rule. The
no-`networkpolicies`-rule claim was also re-checked and still holds, and
`networkpolicy.yaml`'s own header says so.

Added in this pass, all from code that landed after the previous stamp:

- `--version` as a documented operator flag (`main.go:134`, handled
  `:162-165`, documented `README.md:304,317`). It exits 0 before any cluster,
  config, or port work, so it is a data path for scripts and support bundles,
  not a listener, and it is named in the entry-point table rather than omitted.
- The `resourceVersion`-guarded console patch builders (`patches.ts:42`) as a
  mitigation and as a lost-update abuse case. The batch annotation is a
  one-shot request the operator consumes and clears, so an unguarded resubmit
  meant a second MCP pause and a second reboot.
- The `UntrustedValue` type at the plugin boundary (`parse.ts:34-38`) and bidi
  isolation of untrusted text before translation (`text.ts:220`,
  `TabErrorBoundary.tsx:70`) as the general form of the XSS class that recurs
  whenever a new untrusted string reaches a translated sentence.

No mitigation claim in the tree proved false. The unsupported claims that remain
are the unenforced PSS label recorded at the top of this file and the
`SPEC.md:409-410` confirmation-gate wording recorded at the end of §5.

The stamp drifting without the file being re-read is the failure mode worth
naming: an unchanged header is not evidence of an unchanged model. Re-run the
re-verification whenever the stamp is behind `main`, not only when this file is
edited.

`SECURITY.md` was checked against reality on 2026-09-29 and every claim holds:

- Supported versions: latest published 0.x only. README **Current release**
  (`README.md:10`) and `operator/Makefile` `VERSION` are both `0.8.0`, the CSV
  `name`/`spec.version`/`containerImage` agree, and `CHANGELOG.md` carries a
  `0.8.0` section. The table does not overstate a backport stream that does not
  exist.
- Supported host: OpenShift 4.22, matching CHANGELOG **Support window** and
  `README.md:287-289`. The enforcement is `minKubeVersion: 1.35.0`
  (`baseline-security-operator.clusterserviceversion.yaml:62`) plus the plugin's
  `@console/pluginAPI` range, not a `com.redhat.openshift.versions` label: no
  such label exists on the CSV, so an install on a newer OCP with a still-1.35
  kube version is not refused. That is a support claim, not a control.
- Disclosure contact: the CSV `spec.maintainers` email
  (`baseline-security-operator.clusterserviceversion.yaml:64-66`). The address
  exists; `SECURITY.md:18` explicitly directs reports away from the public
  issue tracker, which is the accurate posture.
- Scope matches what ships: operator, plugin, bundle manifests, metrics scrape.
  The e2e harness is named nowhere in the file and is a developer-workstation
  path, not a shipped one.
- `SECURITY.md:16-27` does describe a minimal disclosure path (email the CSV
  maintainer, allow time for a fix, note the fix under **### Security** in the
  CHANGELOG with a CVE ID when assigned). An earlier revision of this file said
  there was no process beyond the changelog note, which overstated the gap; the
  three numbered steps are the process. No numeric SLA, no named on-call, and
  no advisory-identifier convention exist, and this file does not invent any.

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
