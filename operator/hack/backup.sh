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
  -h | --help) usage; exit 0 ;;
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
chmod 700 -- "$OUT"
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
# onto a different cluster instance, or one whose CR has since moved on, shows
# up here rather than as a silently-stale apply. kubectl quotes these scalars,
# so the quotes are stripped to keep the MANIFEST greppable.
RESOURCE_VERSION="$(sed -n 's/^  resourceVersion: *"\{0,1\}\([^"]*\)"\{0,1\}$/\1/p' "$TMP" | head -1)"
UID_VALUE="$(sed -n 's/^  uid: *"\{0,1\}\([^"]*\)"\{0,1\}$/\1/p' "$TMP" | head -1)"
DIGEST="$(command sha256sum < "$TMP" | cut -d' ' -f1)"

{
  printf 'takenAt=%s\n' "$(date -u +%Y-%m-%dT%H:%M:%SZ)"
  printf 'resourceVersion=%s\n' "$RESOURCE_VERSION"
  printf 'uid=%s\n' "$UID_VALUE"
  printf 'sha256=%s\n' "$DIGEST"
  printf 'file=clusterbaseline.yaml\n'
} > "$OUT/MANIFEST"

mv -- "$TMP" "$OUT/clusterbaseline.yaml"
trap - EXIT

chmod 600 -- "$OUT/clusterbaseline.yaml" "$OUT/MANIFEST"

echo "backup.sh: wrote $OUT (resourceVersion=${RESOURCE_VERSION:-unknown}, sha256=${DIGEST})"
echo "backup.sh: copy this directory off-cluster now; it restores only via:"
echo "backup.sh:   hack/restore.sh $OUT"
