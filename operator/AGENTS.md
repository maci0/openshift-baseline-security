# Agent rules: operator

Narrows the repo-root `AGENTS.md`. Kubebuilder go/v4 layout; one reconciler
over the cluster-scoped singleton `ClusterBaseline/cluster`.

## Gate

```sh
make test          # fmt-check, vet, mod-verify, go test ./..., must-gather --self-test
make test-race     # the same unit suite under the race detector
make mod-tidy-check # go.mod/go.sum match the imports; run `go mod tidy` when it fails
make lint          # golangci-lint, shellcheck hack/*.sh, ruff check + format --check on hack/ (../ruff.toml), yamllint ../.yamllint.yaml
make ci            # local replica of the GHA operator job (needs docker)
make verify-reproducible  # build ./cmd twice (path, TZ, locale, umask) and diff the SHA-256
make verify        # every static check below (CRD, kustomize, bundle, CSV, version
                   # lockstep, Go/TS lockstep); no docker, the docker-free half of bundle
make fuzz          # short timed fuzz per target; run before a release cut
make govulncheck
make bundle        # make verify, then operator-sdk bundle validate (needs docker)
make test-one      # one package (PKG=) or one test (PKG= RUN=), same env as make test
make help          # contributor-facing targets
```

GNU Make 3.82+ is required (`.SHELLFLAGS` pipefail). macOS `/usr/bin/make` is 3.81; use Homebrew `gmake`.

`make test` uses `fmt-check` (does not rewrite). Run `make fmt` yourself.
`make build` runs `generate`/`manifests` first and can rewrite generated files.

`GOFLAGS=-mod=readonly` and `GOSUMDB=sum.golang.org` are exported by the
Makefile so no recipe can silently edit `go.mod`/`go.sum` or skip checksum
verification. Override `GOFLAGS=` only when deliberately changing the module
graph, and commit the result.

## Generated files

`controller-gen` writes `config/crd/bases/`, `config/rbac/role.yaml` (the
manager ClusterRole), and `api/v1alpha1/zz_generated.deepcopy.go`; `make bundle`
copies the CRD to `bundle/manifests/`. Never hand-edit those; run
`make generate manifests` (plus `make bundle` for the CRD copy) and commit the
output. CI fails on `git diff --exit-code` after that command.

The rest of `config/rbac/` (user_roles, leader-election, metrics, SA) is
hand-maintained. The CSV (`bundle/manifests/*.clusterserviceversion.yaml`)
is too: `make bundle` validates it but does not write it.

No static operator PodDisruptionBudget (ADR-028): it deadlocks a node drain
on single-node OpenShift. The plugin PDB is reconciled at runtime and
deleted on SingleReplica; do not add a PDB to the operator bundle.

## Lockstep the Makefile enforces

Each has its own target and its own failure message; read the message rather
than guessing.

- `verify-manifests`: the kustomize tree itself. Every entry point
  (`config/{crd,rbac,manager,prometheus,default}`) must build, every manifest
  under `config/` must reach the `config/default` render (a file no
  kustomization lists ships to nobody), and the tree-local references must
  resolve: a RoleBinding `roleRef` to a declared Role/ClusterRole, a Service
  selector to a pod template, a ServiceMonitor selector and scraped port name
  to a Service, and the Secret/ConfigMap a ServiceMonitor names. It also holds
  the metrics port and the probe port to one number each across the flag
  argument, the `containerPort`, the Service port and `targetPort`, the
  NetworkPolicy ingress port, and every probe. Renders with `kustomize`, else
  `kubectl kustomize`, else `oc kustomize`; override with `KUSTOMIZE=`.
  Adding a manifest means listing it in its kustomization, or this fails.
- `verify-versions`: release version, toolchain pins, image-build flags, the
  `ARG VERSION=` default in every Dockerfile, an `ARG SOURCE_DATE_EPOCH` in
  every stage of every Dockerfile, the `Dockerfile.ci` builder tag against the
  `.ci-operator.yaml` build root, CSV
  `capabilities: Basic Install` with no `spec.replaces` / `spec.skipRange`.
  The CHANGELOG half covers the `## [Unreleased]` / `## [VERSION]` ordering and
  rejects a `### ` heading that appears twice inside one release: Keep a Changelog
  has one section per kind, and a repeat hides entries from a reader who stops
  at the first one. It also rejects a `## [VERSION]` section with no entry under
  it, which is a cut that shipped no change or notes that were never written.
- `verify-product-lockstep`: score weights, caps, the `ProfileKey` set, and
  annotation keys shared between Go and the console plugin (ADR-024). Adding a
  profile means touching the CRD enum, the Go constants, and the plugin's
  `PROFILE_KEYS`/`PROFILE_INFO` together.
- `verify-csv-rbac`: CSV permissions against `config/rbac/role.yaml`.
- `verify-csv-deploy`: CSV `install.spec.deployments[].spec` against the
  Deployment in `config/manager/manager.yaml`. The image tag,
  `imagePullPolicy`, and the `app.kubernetes.io/version` pod label are the only
  allowed divergences; anything else means a base change shipped to
  `make deploy` and not to an OLM install. It also checks the one pair of values
  a line-for-line diff cannot relate: `GOMEMLIMIT` must be set and stay under
  `resources.limits.memory`, or the GC stops collecting before the cgroup cap
  and the pod is OOMKilled.
- `verify-bundle-static`: hand-copied bundle manifests against their `config/`
  sources (not the CSV, CRD, or monitoring CRs).
- `verify-monitoring-bundle`: ServiceMonitor and PrometheusRule.
- `verify-crd`: no `uniqueItems` (kube rejects it at apply time and
  operator-sdk does not catch it); use `+listType=set`. No schema default on
  `complianceCatalogSource`, or the OKD auto-detection becomes dead code.
- `test-alerts`: promtool over `config/prometheus/testdata/alerts_test.yaml`.

## Reconcile rules

- Foreign CRs (Compliance Operator, OLM, Console) are read as `unstructured`
  so their Go modules stay out of `go.mod`. Every `NestedString`/`NestedSlice`
  read returns an error on a type mismatch: surface it as a Degraded reason,
  never drop it. A hostile or half-written status must not panic or wedge.
- Tolerate missing CRDs (`NoKindMatch`) on every foreign-API path, not only
  Console.
- Returning an error requeues with backoff. Input only an admin can fix (an
  invalid schedule, a permanent Forbidden) becomes a condition instead, with a
  rule-specific `//nolint:nilerr` and its reason. `nolintlint` requires both.
- Every threshold and grace period is a named package-level constant with a
  comment saying what breaks at the boundary. No bare durations at call sites.
- No real timers in a runnable. A retry wait goes through the injected clock
  (`clock.Sleep` via `l.sleep` / `DefaultClusterBaseline.sleep`), so
  a simulated run spends simulated time on it. `time.NewTimer` in a reconcile
  or Runnable loop makes a seeded replay depend on wall time. Apiserver
  conflicts go through `r.retryOnConflict`, never
  `retry.RetryOnConflict`: its backoff sleeps in wall time behind a globally
  seeded `math/rand` jitter, so a conflicting reconcile replays differently
  every run.
- A status condition's `LastTransitionTime` is stamped from the injected
  clock, so it is passed in explicitly (`setCond`, `setCondFalseLogOnce`,
  `setCondTrueLogRecovered`, `sanitizeStatusForUpdate`, and the free helpers
  wrapping them). `meta.SetStatusCondition` stamps the wall clock, and the
  install-stall and plugin-unavailable graces subtract that stamp from
  `r.now()`: a wall-clock write makes a grace fire (or not) depending on when
  the run started.
- Fuzz any parser of cluster-supplied text (suite labels, scan names, CSV
  versions, timestamps). Commit corpus files under
  `internal/controller/testdata/fuzz/`; a crasher
  written during the fuzz CI job fails the run.

## Tests

`envtest` is not wired up; the suite runs against the controller-runtime fake
client. `test/e2e/` needs a live cluster and `KUBECONFIG` (`make test-e2e`),
and is never part of the per-PR gate. Assert on returned errors from fake-client
calls rather than discarding them.

One package or test: `make test-one PKG=./internal/controller/ [RUN=TestName]`.
A bare `go test` in your shell is not the same run: it inherits your GOFLAGS,
GOTOOLCHAIN, TZ and LC_ALL rather than the ones the gate exports.
