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

## Proving a backup is still good

`backup.sh` failing loudly only covers the run it watched. A schedule that
stopped running, a copy that never finished landing off-cluster, a transfer
that truncated, an expired token: none of these make the job exit non-zero,
and the first sign of any of them is an incident.

```sh
cd operator
./hack/verify-backup.sh /var/backups/baseline/20260920T030000Z
```

No cluster needed and nothing is written, so it also runs against a copy
pulled back from remote storage, which is the only place a scheduled backup
can be proven rather than assumed. It checks the same kind, non-empty, and
sha256 conditions `restore.sh` does, plus the age, and exits non-zero on any
of them. `--max-age-days N` sets the age limit (default 7, the same threshold
`restore.sh` warns at). Alert on its exit status from whatever runs the
schedule; a backup nobody re-reads is a hypothesis.

## Restoring

```sh
cd operator
./hack/restore.sh /var/backups/baseline/20260920T030000Z
```

Every check runs before any write: the artifact exists, is non-empty, is a
`baselinesecurity.openshift.io` `ClusterBaseline`, holds exactly one YAML
document, and matches the sha256 in `MANIFEST`. A truncated transfer, a
hand-edited artifact, or a missing MANIFEST is refused, because a
half-restored object gets reconciled and the evidence of what was lost is
overwritten with a plausible-looking new state.

The single-document check matters because `oc apply -f` and `oc replace -f`
apply **every** document in a multi-document YAML, not just the first. A
backup directory carrying a second document would otherwise be written to the
cluster with whatever cluster-admin credentials ran the restore. The MANIFEST
checksum is not a defence against that: it lives in the same directory, so
anyone who can edit the artifact can recompute it. `backup.sh` captures a
single named object and never emits a `---` separator, so a real backup always
passes.

Then it reads the live object's `resourceVersion`. The MANIFEST records the one
the backup was taken at, and a restore that has fallen behind is a rollback:
it discards every waiver edit and batch annotation made since, and there is no
soft-delete window behind that. The script refuses, naming both versions, and
takes `--force` to proceed anyway. On a cluster where the CR is gone, there is
nothing to compare against and the restore goes ahead.

The age of the artifact is the RPO the restore buys, so it is in the restore
summary, and a backup more than a week old says so before anything is written.

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
   before any uninstall. The operator logs, at the moment it drops the
   finalizer, that the waivers and score history are not recoverable and names
   `hack/backup.sh`; that log line is the last trace of the object.

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

`operator/hack/backup_restore_test.go` runs the scripts against a stub `oc`
on every `make test`, and pins the behavior that matters:

- the round trip preserves the spec, the status, and all four batch
  annotations byte for byte;
- the restore writes the status subresource, which `apply` alone would drop;
- an empty capture, a wrong-kind object, a truncated artifact, an edited
  artifact, a missing MANIFEST, and a MANIFEST without a checksum are each
  refused, and refused **before** any call that writes to the cluster;
- an artifact with a second YAML document appended (a smuggled
  `ClusterRoleBinding`, say) is refused even when its checksum is valid, so the
  refusal cannot be dismissed as checksum damage;
- a restore over a live object that has moved on is refused before any write,
  names both resourceVersions, and proceeds under `--force`;
- the artifact age is reported, and a backup older than a week says so;
- `verify-backup.sh` passes a good directory and fails each way a scheduled
  backup dies quietly: missing directory, truncated copy, lost MANIFEST,
  zero-byte artifact, wrong kind, a schedule that stopped running, and a
  clock that was wrong when it was taken;
- a future `lastScanTime` warns with the recovery command and still restores;
- deleting the CR logs that the waivers are not recoverable and names the
  backup command.

Not covered, because it needs a live cluster: running the real restore
against a real cluster with a paused `MachineConfigPool` mid-batch. The
`docs/TEST-PLAN.md` section AQ list is the outstanding manual plan.
