#!/usr/bin/env bash
# Restore ClusterBaseline/cluster from a directory written by backup.sh.
#
# Everything is validated before anything is written. A restore that half
# applies and then discovers a corrupt artifact is worse than one that refuses
# up front, because the operator will reconcile the partially-restored object
# and overwrite the evidence of what was lost.
#
# The status subresource is written with `oc replace --subresource=status`,
# not `oc apply`: apply ignores status entirely, which would silently drop the
# score, conditions, history, and any in-flight remediation batch, leaving the
# operator to rebuild a partial view from Compliance Operator results. That
# rebuild is the correct steady state, but it is not a restore.
#
# Usage: hack/restore.sh [--force] [backup-dir]   (defaults to ./baseline-backup)
#        hack/restore.sh --help
set -euo pipefail

# shellcheck source-path=SCRIPTDIR
# shellcheck source=lib-sha256.sh
. "$(dirname "${BASH_SOURCE[0]}")/lib-sha256.sh"
# shellcheck source-path=SCRIPTDIR
# shellcheck source=lib-timestamp.sh
. "$(dirname "${BASH_SOURCE[0]}")/lib-timestamp.sh"

# A restore of a backup whose resourceVersion is behind the live object's is a
# clobber of the one state nothing can regenerate (the waiver list and its
# audit attribution), so it is refused rather than warned about. Staleness
# beyond this age is only a warning: the operator decides whether an old
# backup beats a damaged live object.
STALE_BACKUP_MAX_AGE_DAYS=7

usage() {
  cat <<'EOF'
Usage: restore.sh [--force] [backup-dir]

Restore ClusterBaseline/cluster from a directory written by backup.sh
(default ./baseline-backup). Validates the artifact's checksum, kind, and
apiVersion before any write, then applies the spec and replaces the status
subresource.

Run it during an incident, before or alongside the etcd restore: this brings
the user-owned spec and the derived status back onto a cluster that no longer
has them, without waiting on a full control-plane restore.

Options:
  -f, --force   restore even when the live object has moved on since the
                backup was taken, or when the artifact was taken at an
                apiVersion this cluster's CRD does not serve. The writes then
                carry no resourceVersion precondition, so a repeated run
                converges on the same object instead of failing on a conflict
                it can never satisfy.
  -h, --help    print this usage and exit 0
EOF
}

FORCE=false
case "${1:-}" in
  -h | --help)
    usage
    exit 0
    ;;
esac
DIRS=()
for arg in "$@"; do
  case "$arg" in
    -f | --force) FORCE=true ;;
    -*)
      echo "restore.sh: unknown option: $arg" >&2
      usage >&2
      exit 2
      ;;
    *) DIRS+=("$arg") ;;
  esac
done
if [[ ${#DIRS[@]} -gt 1 ]]; then
  echo "restore.sh: unexpected arguments after backup-dir: ${DIRS[*]:1}" >&2
  usage >&2
  exit 2
fi
DIR="${DIRS[0]:-./baseline-backup}"

command -v oc >/dev/null || {
  echo "restore.sh: oc not on PATH" >&2
  exit 1
}

sha256_init || {
  echo "restore.sh: cannot verify the artifact digest" >&2
  exit 1
}

ARTIFACT="$DIR/clusterbaseline.yaml"
MANIFEST="$DIR/MANIFEST"

# --- validation, before any cluster write -----------------------------------

[[ -f "$ARTIFACT" ]] || {
  echo "restore.sh: $ARTIFACT not found; nothing was changed" >&2
  exit 1
}
[[ -s "$ARTIFACT" ]] || {
  echo "restore.sh: $ARTIFACT is empty; refusing to restore a zero-byte backup" >&2
  exit 1
}
[[ -f "$MANIFEST" ]] || {
  echo "restore.sh: $MANIFEST not found; refusing to restore an unverifiable backup" >&2
  exit 1
}

grep -q '^apiVersion: baselinesecurity.openshift.io/' "$ARTIFACT" || {
  echo "restore.sh: artifact is not a baselinesecurity.openshift.io object" >&2
  exit 1
}
grep -q '^kind: ClusterBaseline$' "$ARTIFACT" || {
  echo "restore.sh: artifact is not a ClusterBaseline" >&2
  exit 1
}
# `oc apply -f` / `oc replace -f` apply EVERY document in a multi-doc YAML, not
# just the first. The two greps above match on any line, so a second document
# appended after the ClusterBaseline (a ClusterRoleBinding, say) passes both
# while still being written to the cluster with the operator's own credentials.
# The MANIFEST checksum is not a defence: it lives in the same directory and is
# recomputable by anyone who can edit the artifact. Refuse a second document;
# backup.sh captures a single named object and never emits a `---` separator.
if grep -qE '^---[[:space:]]*($|#)' "$ARTIFACT"; then
  echo "restore.sh: artifact holds more than one YAML document; refusing to" >&2
  echo "restore.sh: apply every document in it" >&2
  exit 1
fi

EXPECTED="$(sed -n 's/^sha256=//p' "$MANIFEST" | head -1)"
if [[ -z "$EXPECTED" ]]; then
  echo "restore.sh: MANIFEST has no sha256; refusing to restore an unverifiable backup" >&2
  exit 1
fi
ACTUAL="$(sha256_file "$ARTIFACT")"
if [[ "$ACTUAL" != "$EXPECTED" ]]; then
  echo "restore.sh: checksum mismatch; artifact was modified or truncated in transit" >&2
  echo "restore.sh:   expected $EXPECTED" >&2
  echo "restore.sh:   actual   $ACTUAL" >&2
  exit 1
fi

# A backup taken while the operator's clock was wrong restores a future
# LastScanTime, and the operator will not advance past a scan that has not
# happened. Say so at restore time, where the operator is watching, rather
# than leaving it to be diagnosed from an empty score later.
if grep -qE '^  lastScanTime: "?([0-9]{4})' "$ARTIFACT"; then
  echo "restore.sh: note: artifact carries status.lastScanTime. If it is in the" >&2
  echo "restore.sh: future (clock was wrong when the backup was taken), clear it" >&2
  echo "restore.sh: after restoring:" >&2
  echo "restore.sh:   oc patch clusterbaseline cluster --subresource=status --type=merge -p '{\"status\":{\"lastScanTime\":null}}'" >&2
fi

# Age is the RPO this restore actually buys: everything edited or scanned
# since takenAt is not in the artifact and cannot be recovered from it. An
# unreadable stamp is not a young one.
TAKEN_AT="$(sed -n 's/^takenAt=//p' "$MANIFEST" | head -1)"
AGE_NOTE="${TAKEN_AT:-unknown time}"
if [[ -z "$TAKEN_AT" ]] || ! TAKEN_EPOCH="$(iso8601_to_epoch "$TAKEN_AT")"; then
  # Say the RPO is unknown rather than reporting nothing, so the operator
  # looks for a newer backup instead of assuming this is recent.
  echo "restore.sh: note: MANIFEST takenAt '${TAKEN_AT:-<none>}' is not a" >&2
  echo "restore.sh: YYYY-MM-DDTHH:MM:SSZ stamp, so the RPO this restore buys is" >&2
  echo "restore.sh: unknown. Check the artifact really is recent before relying" >&2
  echo "restore.sh: on it: anything changed since may be lost." >&2
elif (( $(date -u +%s) < TAKEN_EPOCH )); then
  echo "restore.sh: note: takenAt $TAKEN_AT is in the future, so the host clock" >&2
  echo "restore.sh: was wrong when this backup was taken; its real age is unknown." >&2
else
  AGE_SECONDS=$(( $(date -u +%s) - TAKEN_EPOCH ))
  AGE_DAYS=$(( AGE_SECONDS / 86400 ))
  AGE_NOTE="$TAKEN_AT, ${AGE_DAYS}d old"
  if (( AGE_DAYS > STALE_BACKUP_MAX_AGE_DAYS )); then
    echo "restore.sh: note: this backup is ${AGE_DAYS} days old, so it discards at" >&2
    echo "restore.sh: least that much waiver and scan history. Any newer backup?" >&2
  fi
fi

# --- write ------------------------------------------------------------------

oc() { command oc --request-timeout=30s "$@"; }

if ! oc whoami >/dev/null 2>&1; then
  echo "restore.sh: oc is not authenticated (oc whoami failed); nothing was changed" >&2
  exit 1
fi

# Refuse to roll a live object back to an older backup without being told to.
# The MANIFEST records the resourceVersion the backup was taken at; if the live
# object has a higher one, the waiver edits and batch progress made since are
# not in the artifact and apply would discard them silently. There is no
# soft-delete window behind that, so it takes --force.
BACKUP_RESOURCE_VERSION="$(sed -n 's/^resourceVersion=//p' "$MANIFEST" | head -1)"
# An absent object and a failed read are different states, and only the first
# one makes the guard below unnecessary. A plain `oc get` that fails (token
# expired mid-incident, apiserver blip) leaves the output empty, which read as
# "no live object" and skipped the guard: an old backup then clobbered a
# moved-on object with no --force and no message, losing every waiver edit made
# since. --ignore-not-found separates the two, by exiting 0 with empty output
# on NotFound and nonzero on every real failure.
LIVE_RESOURCE_VERSION=""
if ! LIVE_RESOURCE_VERSION="$(oc get clusterbaseline cluster --ignore-not-found \
  -o jsonpath='{.metadata.resourceVersion}')"; then
  echo "restore.sh: cannot read the live ClusterBaseline/cluster; nothing was changed." >&2
  echo "restore.sh: A failed read is not an absent object: skipping the resourceVersion" >&2
  echo "restore.sh: guard here would restore this backup over a live object that has" >&2
  echo "restore.sh: moved on, discarding the waiver and batch edits made since, with" >&2
  echo "restore.sh: no --force and no warning. Fix the API access and re-run." >&2
  exit 1
fi
if [[ -n "$BACKUP_RESOURCE_VERSION" && -n "$LIVE_RESOURCE_VERSION" &&
  "$LIVE_RESOURCE_VERSION" != "$BACKUP_RESOURCE_VERSION" ]]; then
  if [[ "$FORCE" == true ]]; then
    echo "restore.sh: note: --force; restoring over live object at resourceVersion" >&2
    echo "restore.sh: $LIVE_RESOURCE_VERSION with a backup taken at $BACKUP_RESOURCE_VERSION" >&2
  else
    echo "restore.sh: the live object has moved on since this backup was taken:" >&2
    echo "restore.sh:   live   resourceVersion $LIVE_RESOURCE_VERSION" >&2
    echo "restore.sh:   backup resourceVersion $BACKUP_RESOURCE_VERSION" >&2
    echo "restore.sh: restoring discards every waiver edit and batch annotation" >&2
    echo "restore.sh: made since. Nothing was changed. To restore anyway:" >&2
    echo "restore.sh:   hack/restore.sh --force $DIR" >&2
    exit 1
  fi
fi

# A backup is only restorable into a cluster that serves the version it was
# written at. After a version bump the apiVersion in the artifact no longer
# matches anything, and `oc apply` fails on "no matches for kind" partway
# through an incident, with no hint that the version is why. Checking first
# names the actual cause. A CRD that is not readable at all (a cluster
# recovered without it yet) is left to the apply, which reports it.
ARTIFACT_VERSION="$(sed -n 's|^apiVersion: *baselinesecurity\.openshift\.io/||p' "$ARTIFACT" | head -1)"
SERVED_VERSIONS="$(oc get crd clusterbaselines.baselinesecurity.openshift.io \
  -o jsonpath='{range .spec.versions[?(@.served==true)]}{.name}{"\n"}{end}' 2>/dev/null || true)"
if [[ -n "$SERVED_VERSIONS" ]] && ! grep -qxF "$ARTIFACT_VERSION" <<<"$SERVED_VERSIONS"; then
  if [[ "$FORCE" == true ]]; then
    echo "restore.sh: note: --force; artifact is ${ARTIFACT_VERSION}, this CRD serves" >&2
    echo "restore.sh: $(tr '\n' ' ' <<<"$SERVED_VERSIONS")" >&2
  else
    echo "restore.sh: the artifact was taken at ${ARTIFACT_VERSION}, which this" >&2
    echo "restore.sh: cluster's CRD does not serve. Served versions:" >&2
    echo "restore.sh: $(tr '\n' ' ' <<<"$SERVED_VERSIONS")" >&2
    echo "restore.sh: Nothing was changed. Restore the CRD that serves" >&2
    echo "restore.sh: ${ARTIFACT_VERSION} first, or re-take the backup against this" >&2
    echo "restore.sh: cluster, or force it anyway:" >&2
    echo "restore.sh:   hack/restore.sh --force $DIR" >&2
    exit 1
  fi
fi

# The artifact carries the resourceVersion it was captured at, and that field
# is an optimistic-concurrency precondition on both writes below: a PUT and a
# merge patch naming a stale resourceVersion are refused with a conflict. That
# is what makes the guard above work, and it is also what made --force
# unrestorable: the operator had already accepted that the live object moved
# on, so the precondition could never be satisfied again and every write was
# refused. The script then exited with the spec half-restored and told the
# operator to re-run, and the re-run hit the identical conflict forever.
#
# So under --force the restore is sent from a copy of the artifact with
# metadata.resourceVersion removed: the writes become unconditional, the
# restore completes, and a second run reaches the same state as the first
# instead of failing on a precondition nobody can meet. Without --force the
# artifact is sent as captured, so the guard above and the write agree.
WRITE_ARTIFACT="$ARTIFACT"
if [[ "$FORCE" == true && -n "$BACKUP_RESOURCE_VERSION" ]]; then
  if ! WRITE_ARTIFACT="$(mktemp -- "$DIR/.restore.XXXXXX")"; then
    echo "restore.sh: cannot write a temporary copy of the artifact in $DIR;" >&2
    echo "restore.sh: nothing was changed." >&2
    exit 1
  fi
  trap 'rm -f -- "$WRITE_ARTIFACT"' EXIT
  # A metadata field two columns in with a scalar value: `oc get -o yaml`
  # indents metadata's fields by two spaces, and nothing else in the object has
  # that shape. If a resourceVersion survives the delete, the artifact was not
  # written by that command and the write would carry a stale precondition the
  # check above cannot see: fail rather than send it.
  if ! sed -e '/^  resourceVersion:[[:space:]]*"\{0,1\}[0-9]*"\{0,1\}[[:space:]]*$/d' \
    "$ARTIFACT" > "$WRITE_ARTIFACT"; then
    echo "restore.sh: cannot strip the resourceVersion from the artifact;" >&2
    echo "restore.sh: nothing was changed." >&2
    exit 1
  fi
  if grep -qE '^[[:space:]]*resourceVersion:' "$WRITE_ARTIFACT"; then
    echo "restore.sh: artifact carries a resourceVersion in a shape this script does" >&2
    echo "restore.sh: not recognize, so --force would send a stale one and the writes" >&2
    echo "restore.sh: would be refused. Nothing was changed." >&2
    exit 1
  fi
fi

# Spec first. If the CR does not exist at all (deleted namespace, lost CR),
# apply creates it, and the controller recreates every owned object from the
# restored spec on its next reconcile.
echo "restore.sh: applying spec"
oc apply -f "$WRITE_ARTIFACT"

echo "restore.sh: replacing status subresource"
if ! oc replace --subresource=status -f "$WRITE_ARTIFACT"; then
  echo "restore.sh: status replace failed. The spec IS restored; the operator will" >&2
  echo "restore.sh: rebuild status from Compliance Operator results on its next" >&2
  echo "restore.sh: reconcile. Re-run once the cause is fixed to recover the" >&2
  echo "restore.sh: score history and remediation batch progress." >&2
  exit 1
fi

echo "restore.sh: restored ClusterBaseline/cluster (backup taken $AGE_NOTE)"
echo "restore.sh: watch it converge with:"
echo "restore.sh:   oc get clusterbaseline cluster -o yaml --watch"
