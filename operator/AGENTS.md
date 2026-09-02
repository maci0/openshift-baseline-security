# Agent rules: operator

Narrows the repo-root `AGENTS.md`. Kubebuilder go/v4 layout; one reconciler
over the cluster-scoped singleton `ClusterBaseline/cluster`.

## Gate

```sh
make test          # fmt-check, vet, mod-verify, go test ./..., must-gather --self-test
make lint          # golangci-lint, pinned version, 0 issues required
make ci            # local replica of the GHA operator job (needs docker)
make fuzz          # short timed fuzz per target; run before a release cut
make govulncheck
make bundle        # every verify-* target, then operator-sdk bundle validate
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
manager ClusterRole), `api/v1alpha1/zz_generated.deepcopy.go`, and the CRD
copy under `bundle/manifests/`. Never hand-edit those; run
`make generate manifests` and commit the output. CI fails on
`git diff --exit-code` after that command.

The rest of `config/rbac/` (user_roles, leader-election, metrics, SA) is
hand-maintained. The CSV (`bundle/manifests/*.clusterserviceversion.yaml`)
is too: `make bundle` validates it but does not write it.

No static operator PodDisruptionBudget (ADR-028): it deadlocks a node drain
on single-node OpenShift. The plugin PDB is reconciled at runtime and
deleted on SingleReplica; do not add a PDB to the operator bundle.

## Lockstep the Makefile enforces

Each has its own target and its own failure message; read the message rather
than guessing.

- `verify-versions`: release version, toolchain pins, image-build flags, CSV
  `capabilities: Basic Install` with no `spec.replaces` / `spec.skipRange`.
- `verify-product-lockstep`: score weights, caps, the `ProfileKey` set, and
  annotation keys shared between Go and the console plugin (ADR-024). Adding a
  profile means touching the CRD enum, the Go constants, and the plugin's
  `PROFILE_KEYS`/`PROFILE_INFO` together.
- `verify-csv-rbac`: CSV permissions against `config/rbac/role.yaml`.
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
- Fuzz any parser of cluster-supplied text (suite labels, scan names, CSV
  versions, timestamps). Commit corpus files under `testdata/fuzz/`; a crasher
  written during the fuzz CI job fails the run.

## Tests

`envtest` is not wired up; the suite runs against the controller-runtime fake
client. `test/e2e/` needs a live cluster and `KUBECONFIG` (`make test-e2e`),
and is never part of the per-PR gate. Assert on returned errors from fake-client
calls rather than discarding them.

One package or test: `go test ./internal/controller/ -count=1 -run TestName`.
