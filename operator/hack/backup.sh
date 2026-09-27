#!/usr/bin/env bash
# Back up the one durable object this operator owns: ClusterBaseline/cluster.
#
# The operator keeps no state on any filesystem. Every recoverable fact (the
# user's spec: waivers, schedule, profiles, scoring mode; and the derived
# status: score, conditions, score history, remediation batch progress) lives
# in this single cluster-scoped object, so this is the whole backup. The
# Compliance Operator's own CheckResults and raw-result PVCs are NOT in scope:
# they are its data, it rewrites them, and the operator rebuilds status from
# them on the next reconcile.
#
# Unlike must-gather.sh this does not redact. A backup that cannot be applied
# back is not a backup, and the waiver attribution fields are exactly the
# audit record an incident needs. Treat the output directory as sensitive.
#
# Usage: hack/backup.sh [output-dir]   (defaults to ./baseline-backup)
#        hack/backup.sh --help
set -euo pipefail

# shellcheck source-path=SCRIPTDIR
# shellcheck source=lib-sha256.sh
. "$(dirname "${BASH_SOURCE[0]}")/lib-sha256.sh"

usage() {
  cat <<'EOF'
Usage: backup.sh [output-dir]

Write a restorable copy of ClusterBaseline/cluster into output-dir
(default ./baseline-backup). The directory holds:

  clusterbaseline.yaml  the full object, spec and status, unredacted
  MANIFEST              taken-at timestamp, resourceVersion, uid, sha256

Refuses to finish with a zero-byte or unparseable artifact, so a truncated
capture is never mistaken for a good backup. Copy the directory off-cluster;
a backup on the cluster it protects does not survive the loss of that
cluster.

Options:
  -h, --help   print this usage and exit 0
EOF
}

case "${1:-}" in
  -h | --help)
    if [[ $# -ne 1 ]]; then
      echo "backup.sh: --help takes no arguments" >&2
      usage >&2
      exit 2
    fi
    usage
    exit 0
    ;;
esac

OUT="${1:-./baseline-backup}"
if [[ $# -gt 1 ]]; then
  echo "backup.sh: unexpected arguments after output-dir: ${*:2}" >&2
  usage >&2
  exit 2
fi
if [[ -z "$OUT" || "$OUT" == -* ]]; then
  echo "backup.sh: invalid output directory: ${OUT:-<empty>}" >&2
  usage >&2
  exit 2
fi

command -v oc >/dev/null || {
  echo "backup.sh: oc not on PATH" >&2
  exit 1
}

sha256_init || {
  echo "backup.sh: cannot compute the MANIFEST digest" >&2
  exit 1
}

# Bound every API call: a wedged apiserver must fail the backup loudly rather
# than hang, and a half-finished capture must not look like a success.
oc() { command oc --request-timeout=30s "$@"; }

if ! oc whoami >/dev/null 2>&1; then
  echo "backup.sh: oc is not authenticated (oc whoami failed); refusing empty backup" >&2
  exit 1
fi

# Write to a sibling temp path and rename into place, so an interrupted run
# never leaves a half-written clusterbaseline.yaml that a later restore would
# happily apply. The temp file is removed on every failure path below.
mkdir -p -- "$OUT"
# No `--` guard: BSD chmod (macOS) reads it as a filename, and a flag-shaped
# OUT is refused above, so nothing here can be mistaken for an option.
chmod 700 "$OUT"
TMP="$OUT/.clusterbaseline.yaml.$$"
cleanup() { rm -f -- "$TMP"; }
trap cleanup EXIT

if ! oc get clusterbaseline cluster -o yaml > "$TMP" 2>/dev/null; then
  echo "backup.sh: oc get clusterbaseline cluster failed" >&2
  exit 1
fi

# A zero-byte or non-Kubernetes artifact is what an auth failure, an emptied
# RBAC rule, or a truncated connection all look like. Refuse them here rather
# than at restore time, when the incident is already running.
if [[ ! -s "$TMP" ]]; then
  echo "backup.sh: captured object is empty; refusing to record it as a backup" >&2
  exit 1
fi
if ! grep -q '^apiVersion: baselinesecurity.openshift.io/' "$TMP" ||
  ! grep -q '^kind: ClusterBaseline$' "$TMP"; then
  echo "backup.sh: captured object is not a baselinesecurity ClusterBaseline" >&2
  exit 1
fi

# resourceVersion and uid are the recovery operator's tie-breakers: a restore
# onto a different cluster instance, onto a ClusterBaseline that was deleted
# and recreated under the same name, or onto one whose CR has since moved on,
# each shows up here rather than as a silently-stale apply. Both are required,
# because restore.sh's guards are keyed on them and a guard with no key is no
# guard at all. kubectl quotes these scalars, so the quotes are stripped to
# keep the MANIFEST greppable.
RESOURCE_VERSION="$(sed -n 's/^  resourceVersion: *"\{0,1\}\([^"]*\)"\{0,1\}$/\1/p' "$TMP" | head -1)"
UID_VALUE="$(sed -n 's/^  uid: *"\{0,1\}\([^"]*\)"\{0,1\}$/\1/p' "$TMP" | head -1)"

# Both are what restore.sh's guards are keyed on: without a resourceVersion it
# cannot tell a rollback from a no-op, and without a uid it cannot tell the
# object this backup came from from an unrelated one that took its place. A
# capture whose shape this sed no longer matches would otherwise be written out
# and look like a good backup, and the guards would go silently quiet for every
# later restore. Refuse here, where the capture is still in hand.
if [[ -z "$RESOURCE_VERSION" || -z "$UID_VALUE" ]]; then
  if [[ -z "$RESOURCE_VERSION" && -z "$UID_VALUE" ]]; then
    echo "backup.sh: captured object carries neither a resourceVersion nor a uid." >&2
  elif [[ -z "$RESOURCE_VERSION" ]]; then
    echo "backup.sh: captured object carries no resourceVersion." >&2
  else
    echo "backup.sh: captured object carries no uid." >&2
  fi
  echo "backup.sh: restore.sh refuses a MANIFEST without them, because a backup" >&2
  echo "backup.sh: that cannot name the object it came from cannot be guarded" >&2
  echo "backup.sh: against restoring over a different one. Not writing a MANIFEST." >&2
  exit 1
fi

DIGEST="$(sha256_file "$TMP")"

{
  printf 'takenAt=%s\n' "$(date -u +%Y-%m-%dT%H:%M:%SZ)"
  printf 'resourceVersion=%s\n' "$RESOURCE_VERSION"
  printf 'uid=%s\n' "$UID_VALUE"
  printf 'sha256=%s\n' "$DIGEST"
  printf 'file=clusterbaseline.yaml\n'
} > "$OUT/MANIFEST"

mv -- "$TMP" "$OUT/clusterbaseline.yaml"
trap - EXIT

chmod 600 "$OUT/clusterbaseline.yaml" "$OUT/MANIFEST"

echo "backup.sh: wrote $OUT (resourceVersion=$RESOURCE_VERSION, uid=$UID_VALUE, sha256=$DIGEST)"
echo "backup.sh: copy this directory off-cluster now; it restores only via:"
echo "backup.sh:   hack/restore.sh $OUT"
