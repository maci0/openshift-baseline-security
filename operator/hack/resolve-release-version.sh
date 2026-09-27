#!/usr/bin/env bash
# Resolve the release version from a git tag or a workflow_dispatch input, prove
# it matches the checked-in sources, and emit GITHUB_ENV assignments.
#
# Used by both release.yml jobs so a tag push, a dispatch, and the SBOM step
# cannot disagree about what is being published.
#
#   INPUT_VERSION  workflow_dispatch input; wins over the ref name when set.
#                 Passed through the environment, never as a shell argument, so
#                 a crafted input cannot be interpolated into this script.
#   GITHUB_REF_NAME  tag ref (vX.Y.Z) on a tag push.
#
# Reads GITHUB_ENV when set and appends there; otherwise prints to stdout.
set -euo pipefail

# Diagnostics are prefixed with the script name, as in the other hack/ scripts.
prog="$(basename "$0")"

usage() {
  cat <<EOF
Usage: ${prog}

Resolves the release version from the workflow_dispatch INPUT_VERSION or the
vX.Y.Z tag in GITHUB_REF_NAME, proves it matches operator/Makefile VERSION and
that the tag points at HEAD, then writes VERSION= and TAG= to GITHUB_ENV
(stdout when GITHUB_ENV is unset). Takes no arguments.
EOF
}

case "${1:-}" in
  -h | --help)
    if [ "$#" -ne 1 ]; then
      echo "${prog}: --help takes no arguments" >&2
      exit 2
    fi
    usage
    exit 0
    ;;
  "") ;;
  *)
    echo "${prog}: unexpected argument: $1" >&2
    usage >&2
    exit 2
    ;;
esac

cd "$(dirname "${BASH_SOURCE[0]}")/.."

if [ -n "${INPUT_VERSION:-}" ]; then
  ver="$INPUT_VERSION"
else
  # ":-" so a local run (no Actions env at all) reaches the diagnostic below
  # instead of dying on an unbound variable under `set -u`.
  ver="${GITHUB_REF_NAME:-}"
  ver="${ver#v}"
fi

if [ -z "$ver" ]; then
  echo "${prog}: no release version: neither INPUT_VERSION nor a vX.Y.Z tag ref" >&2
  exit 1
fi

# The image labels, the CSV, and console-plugin/package.json all derive from
# this one line; publishing a tag that disagrees ships unreleased work.
mk=$(sed -n 's/^VERSION ?= //p' Makefile | head -1)
if [ "$ver" != "$mk" ]; then
  echo "${prog}: tag/input version ($ver) != operator/Makefile VERSION ($mk)" >&2
  exit 1
fi

# Refuse a cut whose git tag is missing, or points at another commit than
# HEAD: the images are built from the checkout, so a moved tag would publish
# content that never passed CI under that name.
if ! git rev-parse -q --verify "refs/tags/v$ver" >/dev/null; then
  echo "${prog}: git tag v$ver missing; tag the cut before publishing" >&2
  exit 1
fi
tagged=$(git rev-parse "refs/tags/v$ver^{commit}")
head=$(git rev-parse HEAD)
if [ "$tagged" != "$head" ]; then
  echo "${prog}: refusing to publish $ver from $head; v$ver points at $tagged" >&2
  exit 1
fi

if [ -n "${GITHUB_ENV:-}" ]; then
  printf 'VERSION=%s\nTAG=v%s\n' "$ver" "$ver" >> "$GITHUB_ENV"
else
  printf 'VERSION=%s\nTAG=v%s\n' "$ver" "$ver"
fi
