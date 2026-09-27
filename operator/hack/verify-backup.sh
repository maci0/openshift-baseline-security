#!/usr/bin/env bash
# Verify that a backup directory written by backup.sh is still restorable.
#
# backup.sh refuses to write a bad capture, so its exit code catches a
# truncated or unauthenticated run. It cannot catch what happens after: a
# half-finished off-cluster copy, a filesystem that lost the directory, or a
# cron that has not run in a week because its token expired. Those failures
# are silent, and the first sign of them is an incident.
#
# This check needs no cluster and writes nothing, so it can run on a copy that
# was pulled back from remote storage, which is the only place a scheduled
# backup can be proven rather than assumed. Point it at the same artifact
# restore.sh would read, and alert on its exit status.
#
# Usage: hack/verify-backup.sh [--max-age-days N] [backup-dir]
#        hack/verify-backup.sh --help
set -euo pipefail

# A backup older than this is restorable but no longer protects anything: the
# RPO it buys is the whole gap since it was taken. The default matches
# restore.sh's stale threshold, so a directory that passes here restores
# without a staleness warning.
MAX_AGE_DAYS=7

# shellcheck source-path=SCRIPTDIR
# shellcheck source=lib-sha256.sh
. "$(dirname "${BASH_SOURCE[0]}")/lib-sha256.sh"
# shellcheck source-path=SCRIPTDIR
# shellcheck source=lib-timestamp.sh
. "$(dirname "${BASH_SOURCE[0]}")/lib-timestamp.sh"

usage() {
  cat <<'EOF'
Usage: verify-backup.sh [--max-age-days N] [backup-dir]

Check that backup-dir is a complete, checksummed, recent backup that
restore.sh can apply: the artifact is present, non-empty, the right kind, and
matches the sha256 in MANIFEST. Needs no cluster and writes nothing, so it
also runs against a copy pulled back from remote storage.

Exit status is the signal: 0 restorable, 1 not restorable, 2 bad invocation.
Alert on it from whatever schedules the backup.

Options:
      --max-age-days N   fail when the backup is older than N days (default 7)
  -h, --help             print this usage and exit 0
EOF
}

DIR=""
case "${1:-}" in
  -h | --help)
    usage
    exit 0
    ;;
esac
while [[ $# -gt 0 ]]; do
  case "$1" in
    --max-age-days)
      if [[ $# -lt 2 ]]; then
        echo "verify-backup.sh: --max-age-days needs a value" >&2
        usage >&2
        exit 2
      fi
      MAX_AGE_DAYS="$2"
      shift 2
      ;;
    -h | --help)
      usage
      exit 0
      ;;
    -*)
      echo "verify-backup.sh: unknown option: $1" >&2
      usage >&2
      exit 2
      ;;
    *)
      DIR="$1"
      shift
      ;;
  esac
done
DIR="${DIR:-./baseline-backup}"
if [[ -z "$DIR" ]]; then
  echo "verify-backup.sh: invalid backup directory: <empty>" >&2
  usage >&2
  exit 2
fi
if ! [[ "$MAX_AGE_DAYS" =~ ^[0-9]+$ ]]; then
  echo "verify-backup.sh: --max-age-days takes a whole number, got '${MAX_AGE_DAYS}'" >&2
  usage >&2
  exit 2
fi

ARTIFACT="$DIR/clusterbaseline.yaml"
MANIFEST="$DIR/MANIFEST"

fail() {
  echo "verify-backup.sh: $1" >&2
  exit 1
}

[[ -d "$DIR" ]] || fail "$DIR does not exist; the backup is gone or was never written there"
[[ -f "$ARTIFACT" ]] || fail "$ARTIFACT not found; nothing was checked"
[[ -s "$ARTIFACT" ]] || fail "$ARTIFACT is empty; a zero-byte capture is not a backup"
[[ -f "$MANIFEST" ]] || fail "$MANIFEST not found; an unverifiable backup will not restore"

grep -q '^apiVersion: baselinesecurity.openshift.io/' "$ARTIFACT" ||
  fail "$ARTIFACT is not a baselinesecurity.openshift.io object"
grep -q '^kind: ClusterBaseline$' "$ARTIFACT" ||
  fail "$ARTIFACT is not a ClusterBaseline"

EXPECTED="$(sed -n 's/^sha256=//p' "$MANIFEST" | head -1)"
[[ -n "$EXPECTED" ]] || fail "MANIFEST has no sha256; the artifact cannot be proven intact"
if ! sha256_init; then
  fail "no SHA-256 tool found, so the artifact cannot be checked at all"
fi
ACTUAL="$(sha256_file "$ARTIFACT")"
[[ "$ACTUAL" == "$EXPECTED" ]] || fail "checksum mismatch; the artifact was modified or truncated (expected $EXPECTED, actual $ACTUAL)"

# The age is half of what this script checks, so a MANIFEST whose takenAt
# cannot be read is a failed check, not a passed one: "not old enough to
# matter" and "old enough to matter, unmeasurable" must not both exit 0, or
# the alert on this exit status carries no information.
TAKEN_AT="$(sed -n 's/^takenAt=//p' "$MANIFEST" | head -1)"
if [[ -z "$TAKEN_AT" ]]; then
  fail "MANIFEST has no takenAt; the age that decides whether this backup still protects anything cannot be checked"
fi
if ! TAKEN_EPOCH="$(iso8601_to_epoch "$TAKEN_AT")"; then
  fail "MANIFEST takenAt '$TAKEN_AT' is not a YYYY-MM-DDTHH:MM:SSZ stamp; the age cannot be checked"
fi
AGE_NOTE="${TAKEN_AT:-unknown}"
if (( $(date -u +%s) < TAKEN_EPOCH )); then
  fail "takenAt $TAKEN_AT is in the future; the host clock was wrong when the backup was taken"
else
  AGE_DAYS=$(( ($(date -u +%s) - TAKEN_EPOCH) / 86400 ))
  AGE_NOTE="$TAKEN_AT, ${AGE_DAYS}d old"
  if (( AGE_DAYS > MAX_AGE_DAYS )); then
    fail "backup is ${AGE_DAYS} days old, past the ${MAX_AGE_DAYS}-day limit; schedule is not running or its copy is stale"
  fi
fi

echo "verify-backup.sh: $DIR is restorable (backup taken $AGE_NOTE, sha256 ok)"
