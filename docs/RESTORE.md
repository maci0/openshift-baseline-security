# Backup and restore

What survives a disaster, what is lost, and how to get it back.

The whole of this operator's durable state is one cluster-scoped object,
`ClusterBaseline/cluster`. The operator writes nothing to any filesystem (both
its container and the console plugin run `readOnlyRootFilesystem: true`, with
`emptyDir` scratch only), it owns no PersistentVolume, and it stores no Secret.
Two scripts and this document are the backup and restore path.

## What exists, and who owns it

| State | Where it lives | Owner | Backed up here |
| --- | --- | --- | --- |
| `ClusterBaseline/cluster` spec: waivers, schedule, profiles, scoring mode | etcd, in the CR | this operator | yes |
| `ClusterBaseline/cluster` status: score, conditions, score history, `newlyFailed` / `fixed`, in-flight remediation batch | etcd, in the CR | this operator | yes |
| Batch progress annotations (`batch-apply`, `batch-pools`, `batch-started-at`, `batch-pause-owner`) | etcd, in the CR metadata | this operator | yes |
| `ScanSetting` / `ScanSettingBinding` in `openshift-compliance` | etcd | this operator, but regenerated from the CR spec on every reconcile | not needed |
| Plugin `Deployment` / `Service` / `PDB` / `ConsolePlugin`, dashboard `ConfigMap` | etcd | this operator, all carry an ownerRef to the CR | not needed |
| `ComplianceCheckResult`, `ComplianceScan`, `ComplianceSuite` | etcd, Compliance Operator PVCs | Compliance Operator | no, out of scope |
| Scan raw results (the `1Gi`, `rotation: 3` PVCs the operator configures) | Compliance Operator PVCs | Compliance Operator | no, out of scope |

The derived rows regenerate: deleting a `ScanSettingBinding` or the plugin
`Deployment` costs one reconcile, and the operator rewrites both from the CR
spec. The only rows that cannot be regenerated are the two in the CR itself,
because nothing else records them: the waiver list and its audit attribution,
and the score history ring (30 points per profile ring) that the charts trend
from. Deleting the CR deletes all of it, with no soft-delete window.

## RPO and RTO

Stated, so they are decisions rather than surprises:

- **RPO: the interval at which `backup.sh` last ran.** Nothing in this repo
  schedules it. On a cluster that runs it daily, a lost etcd costs up to a
  day of waiver edits, and the score history is gone for the scans since the
  last capture (it is recomputed forward from the next scan, not backfilled).
- **RTO: under two minutes** for a `restore.sh` run against a reachable
  apiserver. It is two API calls plus one SHA-256 digest (`sha256sum`, or
  `shasum`/`openssl` where coreutils is absent, as on macOS). A full etcd
  restore is OpenShift's, not this project's, and is orders of magnitude slower.
- **RPO for the Compliance Operator's own data is zero** here, because this
  project has no copy of it. If the Compliance Operator's results are the loss,
  a fresh scan rebuilds them, at the cost of one scan interval.

## Taking a backup

```sh
cd operator
./hack/backup.sh /var/backups/baseline/$(date -u +%Y%m%dT%H%M%SZ)
```

Writes `clusterbaseline.yaml` (the full object, spec and status) and
`MANIFEST` (timestamp, resourceVersion, uid, sha256) into a `0700` directory,
files `0600`. It refuses to finish with an empty capture, with an object of
the wrong kind or api group, or on an unauthenticated `oc`, so a run that
produced nothing never leaves a MANIFEST that reads as a good backup.

It does not redact, unlike `must-gather.sh`. A backup that cannot be applied
back is not a backup, and `spec.waivers[].requestedBy` / `approvedBy` are the
audit record an incident needs. Treat the output directory as sensitive.

**Copy it off-cluster.** A backup living on the cluster it protects does not
survive the loss of that cluster. Nothing in this repo can enforce that; the
etcd snapshot policy and the off-cluster copy are the cluster admin's.

## Restoring

```sh
cd operator
./hack/restore.sh /var/backups/baseline/20260920T030000Z
```

Every check runs before any write: the artifact exists, is non-empty, is a
`baselinesecurity.openshift.io` `ClusterBaseline`, and matches the sha256 in
`MANIFEST`. A truncated transfer, a hand-edited artifact, or a missing
MANIFEST is refused, because a half-restored object gets reconciled and the
evidence of what was lost is overwritten with a plausible-looking new state.

The script then applies the spec and replaces the status subresource:

```sh
oc apply -f clusterbaseline.yaml
oc replace --subresource=status -f clusterbaseline.yaml
```

`oc apply` ignores status entirely, so a restore that only applies silently
drops the score, the conditions, the history rings, and any in-flight
remediation batch. The operator would rebuild a partial view from Compliance
Operator results, which is the right steady state but is not a restore.

Watch it converge with
`oc get clusterbaseline cluster -o yaml --watch`.

## Order of operations in a disaster

1. **etcd is intact, the CR is gone or corrupt** (hand edit, bad deploy, a
   bad batch status): run `restore.sh`. Nothing else is needed; the operator
   recreates every owned object from the restored spec.
2. **The whole control plane is gone**: restore etcd by OpenShift's own
   procedure first. Only if the restored CRD or CR is missing or inconsistent
   does `restore.sh` add anything, and then it runs against the recovered
   cluster.
3. **`openshift-baseline-security` namespace deleted but the CR survived**: the
   cluster-scoped CR is not in that namespace, so nothing is lost. The operator
   recreates the namespace and its objects.
4. **Both the CR and the namespace are gone**: run `restore.sh`. Do not fight
   the terminating namespace first; `apply` will fail against it, and deleting
   the CR first is what a namespace delete wants anyway.
5. **The CR was deleted on purpose**: nothing restores it. Deleting
   `ClusterBaseline/cluster` removes the finalizer-protected plugin, the scan
   bindings, and the CRD record of your waivers, in that order. Take a backup
   before any uninstall.

### Two cases the restore cannot fix alone

- **A future `status.lastScanTime`** (the operator's clock was wrong when the
  backup was taken). The operator will not advance past a scan that has not
  happened, so the score stays frozen. `restore.sh` prints the fix when it sees
  one:
  `oc patch clusterbaseline cluster --subresource=status --type=merge -p '{"status":{"lastScanTime":null}}'`
- **A status that a bad version wrote.** Every field is clamped to the CRD
  schema and to a serialized size budget on the way out
  (`operator/internal/controller/sanitize.go`), so a restored object that
  violates the schema will not freeze later writes, but its out-of-range
  values are dropped on the next reconcile. The waiver spec, which is the part
  that cannot be regenerated, is not affected.

## What is verified, and what is not

`operator/hack/backup_restore_test.go` runs both scripts against a stub `oc`
on every `make test`, and pins the behavior that matters:

- the round trip preserves the spec, the status, and all four batch
  annotations byte for byte;
- the restore writes the status subresource, which `apply` alone would drop;
- an empty capture, a wrong-kind object, a truncated artifact, an edited
  artifact, a missing MANIFEST, and a MANIFEST without a checksum are each
  refused, and refused **before** any call that writes to the cluster;
- a future `lastScanTime` warns with the recovery command and still restores.

Not covered, because it needs a live cluster: running the real restore
against a real cluster with a paused `MachineConfigPool` mid-batch. The
`docs/TEST-PLAN.md` section AQ list is the outstanding manual plan.
