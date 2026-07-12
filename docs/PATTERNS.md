# OpenShift addon patterns and practices

How established OpenShift addons (monitoring-plugin, netobserv, kubevirt, ODF,
Compliance Operator) structure and ship things, and how this repo applies each
pattern. Sources: the repos named per section, docs.ci.openshift.org, OLM docs.

## 1. Repo and component layout

**Pattern** (netobserv, kubevirt, monitoring): the console plugin is always its
own image and (usually) its own repo, never part of the operator Go module.
The operator reconciles the plugin's Deployment + Service + ConsolePlugin CR
and registers it with the console operator. ODF is the exception that proves
the rule: a plugin *monorepo*, still separate from the operator.

**Here**: monorepo during incubation (`operator/`, `console-plugin/`), each
half shaped exactly like its standalone counterpart so a later split is
`git mv`. The operator deploys the plugin from `RELATED_IMAGE_CONSOLE_PLUGIN`.

## 2. Versioning across OCP releases

**Release contract (this repo)**: pre-1.0 SemVer on a single `alpha` OLM channel;
API is `v1alpha1`. Consumer notes live in root `CHANGELOG.md` (Keep a Changelog).
Version strings must match across `operator/Makefile` (`VERSION` /
`PREV_VERSION` for the OLM `replaces` edge), the CSV
(`bundle/manifests/baseline-security-operator.clusterserviceversion.yaml`
name/version/replaces/containerImage/image tags), `console-plugin/package.json`
(`version` and `consolePlugin.version`), `CHANGELOG.md` (`## [VERSION]`,
`## [PREV_VERSION]`, `## [Unreleased]`, and the `[VERSION]:` /
`[Unreleased]: ...vVERSION...HEAD` compare footers), and root `README.md`
(**Current release** and the upgrade-path chain ending at `VERSION`).
`make verify-versions` (also run from `make bundle` and CI) enforces that.
Each published cut also needs an immutable git tag `vVERSION` (never
force-moved) so changelog compare URLs resolve. Never reuse a published
CSV/image tag; OLM unpack caches make same-tag republishes serve stale
content. Breaking behavior in 0.x is allowed in minor bumps but must be
called out under Changed/Removed in the changelog with a migration note.
Only the latest 0.x line is supported (no backports); see CHANGELOG support
window.

**Pattern**: two models.
- Payload components: `main` tracks the next OCP; `release-4.y` branches cut
  at feature freeze carry the complete toolchain snapshot. Toolchain is pinned
  by the per-branch build-root image
  (`registry.ci.openshift.org/ocp/builder:rhel-9-golang-1.25-openshift-4.22`),
  declared in `.ci-operator.yaml`, bumped centrally by ART.
- Layered OLM operators (Compliance Operator model): own version stream,
  single branch spanning several OCP versions; compatibility declared in the
  bundle (`com.redhat.openshift.versions`, `minKubeVersion`,
  `olm.maxOpenShiftVersion`), enforced by OLM.

**Here**: hybrid. The console plugin pins per console version (SDK dist-tag
`4.22-latest`, PatternFly 6, shared-module versions), so real `release-4.y`
branches begin when 4.23 exists. The operator side declares the OLM compat
range. Toolchain pins live where branches snapshot them: `go.mod`,
`package.json`, Dockerfiles, Makefile (see SPEC §7).

## 3. Operator scaffolding and code

**Pattern**: kubebuilder go/v4 layout (`api/<version>/`, `cmd/main.go`,
`internal/controller/`, `config/` kustomize tree). The operator-sdk CLI is
deprecated by Red Hat (last shipped in OCP 4.18); kubebuilder is the
scaffolder, OLM bundle targets stay in the Makefile. controller-gen generates
deepcopy, CRDs, and RBAC from markers; CI verifies generated files are
current (`make generate manifests && git diff --exit-code`).

**Here**: exactly that layout; CI has the drift check.

Conventions applied:
- k8s.io/* modules match the target OCP kube level (4.22 = v0.35.x),
  controller-runtime matching (v0.23.x), Go per build root (1.25).
- Unstructured clients for foreign CRs touched only lightly (compliance,
  OLM, console operator config) rather than importing their Go modules.
  Typed APIs only for owned CRDs and core objects.
- `RELATED_IMAGE_*` env vars for every image the operator deploys, mirrored
  into CSV `relatedImages` so disconnected mirroring (oc-mirror) works.

## 4. CRD API design

**Pattern** (openshift config CRs): cluster-scoped singleton named `cluster`,
enforced by validation; `spec` holds intent, `status` holds observed state
with `metav1.Condition` arrays; printer columns for the obvious `oc get`
answer. Bounded lists in status (no unbounded growth).

**Here**: `ClusterBaseline/cluster` with CEL name enforcement, conditions
with `observedGeneration` (`Available` / `Progressing` / `Degraded`
rollups plus detail `ComplianceOperatorReady`, `ScanConfigured`,
`ScanStorageReady`, `ConsolePluginReady`), score + per-profile counts
(suite-scoped to `baseline-<profile>` bindings), history capped at 30
(oldest first), printer columns Score / Last Scan. Manager and plugin
Deployments use 2 replicas with preferred pod anti-affinity; manager uses
leader election.

## 5. Console dynamic plugin

**Pattern** (console-plugin-template, monitoring-plugin, kubevirt):
- Extensions declared in `console-extensions.json`; exposed modules via
  webpack module federation (`ConsoleRemotePlugin`).
- i18n namespace MUST be `plugin__<plugin-name>`; strings referenced as
  `%plugin__name~Key%` in extension declarations.
- Shared modules (react, react-i18next, PatternFly dynamic modules) must
  match the console's versions; the SDK webpack plugin validates this at
  build time.
- UI composition: nav `console.navigation/href` into an existing section,
  one `console.page/route`, `HorizontalNav` tabs inside; SDK components
  (`VirtualizedTable`, `ListPageFilter`, `Timestamp`, `useK8sWatchResource`,
  `useListPageFilter`) instead of hand-rolled tables and fetches.
- No backend: all data via the console's k8s API proxy with the logged-in
  user's token, so RBAC is the user's own. `useAccessReview` to disable
  writes the user cannot perform.
- Serving: ubi9/nginx-120 base, document root `/opt/app-root/src`, checked-in
  `nginx.conf` (TLS only on 9443, no plaintext 8080) with the service-serving
  certificate (`service.beta.openshift.io/serving-cert-secret-name`);
  ConsolePlugin CR points at the Service; plugin name appended to
  `consoles.operator.openshift.io/cluster` `spec.plugins` (and removed on
  uninstall).

**Here**: all of the above, including `useAccessReview` gating on rescan,
profile/schedule/scoring/waiver patches, TailoredProfile authoring,
remediation apply/unapply/batch, and the auto-apply toggle.

## 6. Security posture

**Pattern**: operator and plugin pods run `runAsNonRoot`, seccomp
`RuntimeDefault`, `allowPrivilegeEscalation: false`, drop ALL capabilities,
read-only root fs where possible; RBAC least-privilege generated from
markers; dangerous actions (anything that reboots nodes) are opt-in,
confirmation-gated, and RBAC-gated.

**Here**: manager.yaml and CSV carry those pod security settings; remediation
apply is the only dangerous write and is confirmation-gated in the UI plus
`spec.remediation.apply` defaults to Manual.

## 7. OLM packaging

**Pattern**: `bundle/` with `manifests/` (CSV + owned CRDs) and
`metadata/annotations.yaml`; `bundle.Dockerfile` FROM scratch with matching
labels; CSV carries `alm-examples`, install modes, `minKubeVersion`, feature
annotations (`features.operators.openshift.io/*`), `relatedImages`. OCP
compatibility via `com.redhat.openshift.versions`. Catalogs are file-based
(FBC) rendered with opm; SQLite catalogs are dead. Validate with
`operator-sdk bundle validate` (run via container, the CLI is deprecated on
hosts).

**Here**: install the operator deployment into
`openshift-baseline-security` with the cluster-wide `AllNamespaces` install
mode (the metrics Service and RBAC subjects are fixed to that namespace,
while the controller watches cluster-scoped resources). `make bundle`
validates in the operator-sdk container; `make
bundle-build/bundle-push/catalog-build` produce bundle + FBC catalog images.

Hard-won specifics (all hit during live OLM install testing):
- The CSV needs namespaced `permissions` (leases + events) for
  leader election, not just `clusterPermissions`; without them the manager
  runs but never acquires the lock, and no controller starts. `operator-sdk
  bundle validate` does not catch this.
- opm-served catalog images must precompute the cache at build time
  (`RUN opm serve /configs --cache-dir=... --cache-only`); otherwise the
  catalog pod crash-loops on an integrity check.
- Never reuse a bundle/catalog image tag: OLM unpack jobs and kubelet image
  caches happily serve the stale content for a same-tag repush. Version
  every push.
- installModes must match the target OperatorGroup: a cluster-scoped
  operator installed into `openshift-operators` (an AllNamespaces
  OperatorGroup) needs `AllNamespaces: supported: true`; OwnNamespace-only
  fails with "AllNamespaces InstallModeType not supported".
- OLM caches unpacked bundles by CSV name+version. Reusing a version string
  after fixing a bundle serves the stale unpack; bump the version (or clear
  the unpack jobs) rather than rebuilding the same version.

## 8. Dependency on another operator

**Pattern**: OLM v0 `dependencies.yaml` resolves into the dependent's
namespace/OperatorGroup, which breaks operators that expect their own
namespace (compliance-operator expects `openshift-compliance`). Established
workaround, used here: reconcile the dependency's Namespace + OperatorGroup +
Subscription explicitly, adopt an existing install, never fight it. Revisit
with OLM v1.

## 9. CI and governance

**Pattern**: openshift org repos onboard ci-operator (config in
openshift/release, `.ci-operator.yaml` build root, Prow jobs, OWNERS files
for /approve + reviewer assignment). Pre-onboarding OSS repos run the same
Makefile targets in GitHub Actions.

**Here**: GitHub Actions (`test`, generated-file drift check, plugin build,
image builds) + OWNERS at root. ci-operator onboarding is a productization
step (SPEC §10).

## 10. Naming and misc

- Namespace: `openshift-<component>` (here `openshift-baseline-security`).
  The `openshift-` prefix is reserved for shipped components; acceptable for
  an addon intended for productization, rename if that offends a cluster.
- Images: one component = one image; tags match the operator version;
  `:latest` only for development.
- Cron/scan defaults mirror Compliance Operator's own defaults (daily 1AM,
  1Gi PV, rotation 3) so behavior matches what its docs teach.
- Locales under `locales/en/plugin__<name>.json`; every user-visible string
  through `useTranslation`.
