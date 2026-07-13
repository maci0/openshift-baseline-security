# Security policy

## Supported versions

This project is pre-1.0 (SemVer **0.x**, OLM channel and CSV maturity
`alpha`, API `baselinesecurity.openshift.io/v1alpha1`).

| Version line | Supported |
|--------------|-----------|
| Latest published 0.x (see README **Current release**) | Yes: bugfixes and security updates |
| Older 0.x lines | No: no backport stream; upgrade to the latest 0.x |

Supported host: OpenShift 4.22 only. See [CHANGELOG.md](CHANGELOG.md) and
[README.md](README.md) **Versioning and upgrades**.

## Reporting a vulnerability

Please do **not** open a public GitHub issue for security-sensitive reports.

1. Email the maintainer listed on the OLM ClusterServiceVersion
   (`spec.maintainers` in
   `operator/bundle/manifests/baseline-security-operator.clusterserviceversion.yaml`)
   with a description, impact, and steps to reproduce if possible.
2. Allow a reasonable time for a fix and coordinated disclosure before
   public discussion.
3. Fixed releases will note the issue under **### Security** in
   [CHANGELOG.md](CHANGELOG.md) (with a CVE ID when one is assigned).

## Scope

In scope: the operator, console plugin, OLM bundle manifests, and metrics
scrape path as shipped for a published version tag.

Out of scope: the Red Hat Compliance Operator and its content images (report
those upstream), and cluster misconfiguration outside this project's control.
