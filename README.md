# OpenShift Baseline Security

Baseline compliance scanning for a single OpenShift cluster, built on the
Red Hat Compliance Operator, with results in the admin console.

- `docs/SPEC.md`: design specification (read this first)
- `operator/`: Go operator (kubebuilder go/v4 layout) reconciling the
  `ClusterBaseline` CRD: installs/adopts the Compliance Operator, owns
  ScanSetting/ScanSettingBinding defaults, deploys the console plugin,
  aggregates a compliance score into status
- `console-plugin/`: OpenShift console dynamic plugin (React 18,
  PatternFly 6, dynamic-plugin-sdk 4.22) rendering dashboard, check
  results, and profile selection

## Screenshots

Live against a single-node OpenShift 4.22 cluster (CIS profile):

![Compliance overview](docs/screenshots/overview.png)
![Check results](docs/screenshots/results.png)
![Remediations](docs/screenshots/remediations.png)

## Quick start (development)

```sh
# operator
cd operator && make test build

# console plugin
cd console-plugin && yarn install && yarn build
# against a live console: yarn start (serves on :9001)
```

Targets OpenShift 4.22. License: Apache-2.0.
