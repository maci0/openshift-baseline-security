#!/usr/bin/env bash
# Assert the image metadata every published baseline-security image must carry:
# a non-root USER (where the image is executed), a version label, and the OCI
# source/license labels consumers and scanners read.
#
#   Usage: hack/verify-image-metadata.sh <image> [--allow-scratch-user]
#
# --allow-scratch-user is for FROM scratch bundles, which have no USER because
# they are unpacked by OLM rather than run as a process.
set -euo pipefail

# Every diagnostic goes to stderr under this name: the script is a pass/fail
# gate, so stdout stays empty and a caller can rely on the exit code alone.
prog="$(basename "$0")"

EXPECTED_LICENSES="Apache-2.0"
EXPECTED_SOURCE="https://github.com/maci0/openshift-baseline-security"

usage() {
  cat <<EOF
Usage: ${prog} <image> [--allow-scratch-user]

Asserts that <image> declares a non-root USER (unless --allow-scratch-user)
and carries the expected org.opencontainers.image source, license, and
version labels.
--allow-scratch-user is for FROM scratch bundles: OLM unpacks them, no process
runs, so no USER is required, but a declared root USER is still rejected.
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
  -*)
    echo "${prog}: unknown option: $1" >&2
    usage >&2
    exit 2
    ;;
esac

if [ "$#" -lt 1 ] || [ "$#" -gt 2 ]; then
  echo "${prog}: unexpected arguments; expected <image> [--allow-scratch-user]" >&2
  usage >&2
  exit 2
fi
image="$1"
allow_scratch_user="${2:-}"

case "$allow_scratch_user" in
  "") ;;
  --allow-scratch-user) ;;
  *)
    echo "${prog}: unknown option: $allow_scratch_user" >&2
    usage >&2
    exit 2
    ;;
esac

if [ "$allow_scratch_user" = "--allow-scratch-user" ]; then
  user=$(docker image inspect -f '{{.Config.User}}' "$image")
  echo "${prog}: Config.User=${user} (scratch image, USER not required)" >&2
  if [ -n "$user" ] && { [ "$user" = "0" ] || [ "$user" = "root" ] || [ "$user" = "0:0" ]; }; then
    echo "${prog}: scratch image ${image} must not declare a root USER" >&2
    exit 1
  fi
else
  user=$(docker image inspect -f '{{.Config.User}}' "$image")
  echo "${prog}: Config.User=${user}" >&2
  [ -n "$user" ] || { echo "${prog}: ${image} has no USER; it would run as root" >&2; exit 1; }
  [ "$user" != "0" ] || { echo "${prog}: ${image} runs as root (USER 0)" >&2; exit 1; }
  [ "$user" != "root" ] || { echo "${prog}: ${image} runs as root (USER root)" >&2; exit 1; }
  [ "$user" != "0:0" ] || { echo "${prog}: ${image} runs as root (USER 0:0)" >&2; exit 1; }
fi

lic=$(docker image inspect -f '{{index .Config.Labels "org.opencontainers.image.licenses"}}' "$image")
echo "${prog}: org.opencontainers.image.licenses=${lic}" >&2
[ "$lic" = "$EXPECTED_LICENSES" ] || {
  echo "${prog}: ${image} OCI licenses label is ${lic}, want ${EXPECTED_LICENSES}" >&2
  exit 1
}

src=$(docker image inspect -f '{{index .Config.Labels "org.opencontainers.image.source"}}' "$image")
echo "${prog}: org.opencontainers.image.source=${src}" >&2
[ "$src" = "$EXPECTED_SOURCE" ] || {
  echo "${prog}: ${image} OCI source label is ${src}, want ${EXPECTED_SOURCE}" >&2
  exit 1
}

# Every Dockerfile sets the version label from ARG VERSION, so a published image
# that lost it (an unexpanded ARG, a dropped LABEL line) still builds and still
# passes the checks above: only the built artifact shows it.
ver=$(docker image inspect -f '{{index .Config.Labels "org.opencontainers.image.version"}}' "$image")
echo "${prog}: org.opencontainers.image.version=${ver}" >&2
[ -n "$ver" ] || {
  echo "${image} has no org.opencontainers.image.version label" >&2
  exit 1
}
case "$ver" in
[0-9]*.[0-9]*.[0-9]*) ;;
*)
  echo "${image} OCI version label is ${ver}, want X.Y.Z (matches Makefile VERSION)" >&2
  exit 1
  ;;
esac
