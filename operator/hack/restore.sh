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
# Usage: hack/restore.sh [backup-dir]   (defaults to ./baseline-backup)
#        hack/restore.sh --help
set -euo pipefail

# shellcheck source-path=SCRIPTDIR
# shellcheck source=lib-sha256.sh
. "$(dirname "${BASH_SOURCE[0]}")/lib-sha256.sh"

usage() {
  cat <<'EOF'
Usage: restore.sh [backup-dir]

Restore ClusterBaseline/cluster from a directory written by backup.sh
(default ./baseline-backup). Validates the artifact's checksum, kind, and
apiVersion before any write, then applies the spec and replaces the status
subresource.

Run it during an incident, before or alongside the etcd restore: this brings
the user-owned spec and the derived status back onto a cluster that no longer
has them, without waiting on a full control-plane restore.

Options:
  -h, --help   print this usage and exit 0
EOF
}

case "${1:-}" in
  -h | --help) usage; exit 0 ;;
esac

DIR="${1:-./baseline-backup}"
if [[ $# -gt 1 ]]; then
  echo "restore.sh: unexpected arguments after backup-dir: ${*:2}" >&2
  usage >&2
  exit 2
fi
if [[ -z "$DIR" || "$DIR" == -* ]]; then
  echo "restore.sh: invalid backup directory: ${DIR:-<empty>}" >&2
  usage >&2
  exit 2
fi

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

# --- write ------------------------------------------------------------------

oc() { command oc --request-timeout=30s "$@"; }

if ! oc whoami >/dev/null 2>&1; then
  echo "restore.sh: oc is not authenticated (oc whoami failed); nothing was changed" >&2
  exit 1
fi

# Spec first. If the CR does not exist at all (deleted namespace, lost CR),
# apply creates it, and the controller recreates every owned object from the
# restored spec on its next reconcile.
echo "restore.sh: applying spec"
oc apply -f "$ARTIFACT"

echo "restore.sh: replacing status subresource"
if ! oc replace --subresource=status -f "$ARTIFACT"; then
  echo "restore.sh: status replace failed. The spec IS restored; the operator will" >&2
  echo "restore.sh: rebuild status from Compliance Operator results on its next" >&2
  echo "restore.sh: reconcile. Re-run once the cause is fixed to recover the" >&2
  echo "restore.sh: score history and remediation batch progress." >&2
  exit 1
fi

echo "restore.sh: restored ClusterBaseline/cluster (backup taken $(sed -n 's/^takenAt=//p' "$MANIFEST" | head -1))"
echo "restore.sh: watch it converge with:"
echo "restore.sh:   oc get clusterbaseline cluster -o yaml --watch"
