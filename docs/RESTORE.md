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
  apiserver. It is a few API reads (`whoami`, the live `resourceVersion` and
  `uid`, the CRD's served versions) plus one SHA-256 digest (`sha256sum`, or
  `shasum`/`openssl` where coreutils is absent, as on macOS) and the two
  writes. A full etcd restore is OpenShift's, not this project's, and is
  orders of magnitude slower.
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
can be proven rather than assumed. It checks the same kind, non-empty,
`resourceVersion`, `uid`, and sha256 conditions `restore.sh` does, plus the
age, and exits non-zero on any of them. `--max-age-days N` sets the age limit (default 7, the same threshold
`restore.sh` warns at). Alert on its exit status from whatever runs the
schedule; a backup nobody re-reads is a hypothesis.

The age comes off the `takenAt` stamp in the MANIFEST, with no dependency on
which `date` the host has, and a MANIFEST whose `takenAt` is missing or is
not a `YYYY-MM-DDTHH:MM:SSZ` stamp fails the check. "Not old enough to
matter" and "old enough to matter, unmeasurable" must not both exit 0, or the
alert carries nothing.

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

Then it reads the live object's `resourceVersion` and `uid`. The MANIFEST
records the ones the backup was taken at. A restore that has fallen behind is
a rollback: it discards every waiver edit and batch annotation made since, and
there is no soft-delete window behind that. The script refuses, naming both
versions, and takes `--force` to proceed anyway. On a cluster where the CR is
gone, there is nothing to compare against and the restore goes ahead.

The `uid` answers a question the `resourceVersion` cannot. A `resourceVersion`
counts writes within one object's lifetime, so it says nothing about *which*
object is live. A `ClusterBaseline` that was deleted and recreated under the
same name, a backup carried over from another cluster, and an etcd snapshot
taken from a point before the object existed all put a live object in front of
a backup, and the `resourceVersion` comparison reads each of them as "merely
older" and names a rollback that is not what happened. Where the two coincide
it is worse than a wrong message: the guard passes and the restore overwrites
an unrelated object's waivers, which is exactly the state nothing else records.
A `uid` is minted per object and never reused, so a difference is proof, and
the script refuses on one, naming both uids and the three causes. That read
failing is not an absent `uid` either, so it stops with nothing changed, the
same way a failed `resourceVersion` read does, and `--force` does not cover it:
there is nothing to compare against.

Because those two fields are what the guards are keyed on, `backup.sh` refuses
to write a MANIFEST without them, and `verify-backup.sh` refuses a directory
whose MANIFEST is missing either. A capture whose shape the `sed` no longer
matches would otherwise be recorded as a good backup, and every later restore
would run with the guards quietly off.

A `resourceVersion` the restore moved itself is not a moved-on object, so the
script also compares the live object's `spec` against the artifact's. The
operator never writes `spec` (it patches annotations and the status subresource
only), so a live object already carrying the artifact's spec is either a
restore that has already been applied or a hand-apply of the same spec, not an
admin's work. That case is a re-run, and it is announced and allowed rather
than refused: the version guard would otherwise fire on the previous run's own
write and offer to protect waiver edits that run had just put there. A spec
that differs in any way is an edit, and the version guard applies unchanged.

An absent object and an unreadable one are different states, and only the first
makes those guards unnecessary. A read that fails (an expired token
mid-incident, an apiserver blip) is not an absent object, so the script stops
with nothing changed rather than reading the empty result as "no live object"
and restoring over an object that had moved on. `--force` does not cover it:
the operator cannot have meant to clobber an object whose current
`resourceVersion` was never read.

The age of the artifact is the RPO the restore buys, so it is in the restore
summary, and a backup more than a week old says so before anything is written.
A MANIFEST whose `takenAt` cannot be read still restores, and says the RPO is
unknown rather than showing no age.

The artifact also has to be a version the cluster can accept. `restore.sh`
reads the served versions off the CRD and refuses an artifact taken at an
`apiVersion` that is not among them, naming both, because `oc apply` reports
that as `no matches for kind`, which sends the reader after RBAC instead of
after the version. `--force` overrides it. A CRD that cannot be read at all,
which is the state of a cluster whose etcd restore has not finished, is left
to the apply, which reports it.

The script then applies the spec and replaces the status subresource:

```sh
oc apply -f clusterbaseline.yaml
oc replace --subresource=status -f clusterbaseline.yaml
```

`oc apply` ignores status entirely, so a restore that only applies silently
drops the score, the conditions, the history rings, and any in-flight
remediation batch. The operator would rebuild a partial view from Compliance
Operator results, which is the right steady state but is not a restore.

A restore runs more than once per incident as often as it runs once (after a
transient API error, to be sure, by the next person on the incident), and the
second run has to reach the state the first one reached. Under `--force` the
two writes are sent from a copy of the artifact with `metadata.resourceVersion`
removed, because that field is a precondition on both: it is what the staleness
guard above compares, and once the operator has accepted the rollback there is
no live `resourceVersion` left that could satisfy it. The writes are then
unconditional, so a re-run converges instead of conflicting. A re-run that
recognises its own previous write (the `spec` match above) takes the same
unconditional path, for the same reason: the captured `resourceVersion` is
stale by the write that applied it. Otherwise the artifact is sent as
captured, and the guard above and the write agree. The artifact on disk is
never modified.

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
5. **The CR was deleted, and something recreated it** (an install, a
   `kubectl apply` of a manifest, an etcd snapshot older than the delete): the
   live object is a different object under the same name, so `restore.sh`
   refuses on the uid, naming both. If it holds no waivers of its own, or none
   you would rather keep, `--force` restores over it; the artifact's waiver
   list and score history are the ones that survive.
6. **The CR was deleted on purpose**: nothing restores it. Deleting
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
- an artifact taken at an `apiVersion` the CRD does not serve is refused
  before the apply, naming the served versions, and a CRD that cannot be
  read does not block the restore;
- an artifact with a second YAML document appended (a smuggled
  `ClusterRoleBinding`, say) is refused even when its checksum is valid, so the
  refusal cannot be dismissed as checksum damage;
- a restore over a live object that has moved on is refused before any write,
  names both resourceVersions, and proceeds under `--force`;
- a restore run twice with no `--force`, against an object the first run left
  holding the artifact's own spec, converges on the second run instead of
  refusing, and sends both writes without the stale `resourceVersion`;
- a restore over a live object that is a *different object* (uid mismatch, at
  the same resourceVersion, which is the only way past the rollback guard) is
  refused before any write and names both uids, and a live uid that cannot be
  read is refused rather than treated as a match;
- `backup.sh` refuses a capture carrying no resourceVersion or uid, and
  `verify-backup.sh` refuses a MANIFEST missing either, so neither can be
  recorded and later trusted with a guard switched off;
- a live object that cannot be read is refused before any write, and
  `--force` does not override it;
- the artifact age is reported, and a backup older than a week says so;
- `verify-backup.sh` passes a good directory and fails each way a scheduled
  backup dies quietly: missing directory, truncated copy, lost MANIFEST,
  zero-byte artifact, wrong kind, a schedule that stopped running, a clock
  that was wrong when it was taken, and a MANIFEST whose age cannot be
  measured;
- the age is read on a PATH with no GNU `date -d` (the macOS shape) and a
  digest on a PATH with no GNU `sha256sum`, so neither check can be lost to
  the host it runs on;
- a future `lastScanTime` warns with the recovery command and still restores;
- deleting the CR logs that the waivers are not recoverable and names the
  backup command.

Not covered, because it needs a live cluster: running the real restore
against a real cluster with a paused `MachineConfigPool` mid-batch. The
`docs/TEST-PLAN.md` section AQ list is the outstanding manual plan.
