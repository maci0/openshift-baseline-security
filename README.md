# OpenShift Baseline Security

Baseline compliance scanning for a single OpenShift cluster, built on the
Red Hat Compliance Operator, with results in the admin console.

Install it and the cluster benchmarks itself against the CIS OpenShift
Benchmark out of the box (PCI-DSS, NIST 800-53, DISA STIG, NERC CIP,
ACSC E8, and BSI selectable per profile), rendered natively in the console
under **Administration → Compliance**: score, filterable check results,
gated remediation apply, score trend.

- `docs/SPEC.md`: design specification (read this first)
- `docs/PATTERNS.md`: OpenShift addon patterns this repo follows
- `operator/`: Go operator (kubebuilder go/v4) reconciling the
  `ClusterBaseline` CRD: installs/adopts the Compliance Operator, owns
  ScanSetting/ScanSettingBinding defaults, deploys the console plugin,
  aggregates score + history into status
- `console-plugin/`: console dynamic plugin (React 18, PatternFly 6,
  dynamic-plugin-sdk 4.22)

## Screenshots

Live against a single-node OpenShift 4.22.0 cluster (CIS profile):

![Compliance overview](docs/screenshots/overview.png)
![Check results](docs/screenshots/results.png)
![Remediations](docs/screenshots/remediations.png)

## Prerequisites

- OpenShift 4.22
- A default StorageClass (scan results are stored on a PVC; without one,
  scans hang and the operator reports a `Degraded` condition)
- Cluster access to an OLM catalog carrying `compliance-operator`
  (`redhat-operators` by default)

## Install (OLM)

Build and push the three images plus bundle and file-based catalog, then:

```sh
cd operator
make docker-build docker-push          # operator image
make bundle bundle-build bundle-push   # validated OLM bundle
make catalog-build && docker push $(CATALOG_IMG)
oc apply -f - <<EOF
apiVersion: operators.coreos.com/v1alpha1
kind: CatalogSource
metadata:
  name: baseline-security
  namespace: openshift-marketplace
spec:
  displayName: Baseline Security
  sourceType: grpc
  image: <CATALOG_IMG>
EOF
```

Then install "Baseline Security" from OperatorHub (or create a Subscription
in `openshift-operators`). The operator default-creates a
`ClusterBaseline/cluster` with the CIS profile and starts scanning; opt out
with `BASELINE_SECURITY_SKIP_DEFAULT_CR=true` on the CSV deployment.

Never reuse bundle/catalog image tags between pushes; OLM and kubelet caches
will serve the stale content.

## Development

```sh
# operator: build, test, run against the current kubeconfig
cd operator && make test && make install && make run

# console plugin
cd console-plugin && yarn install && yarn build
# against a live console: yarn start (serves on :9001)
```

`make run` needs `RELATED_IMAGE_CONSOLE_PLUGIN` pointing at a plugin image
the cluster can pull.

Targets OpenShift 4.22. License: Apache-2.0.
