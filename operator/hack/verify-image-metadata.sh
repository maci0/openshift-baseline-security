#!/usr/bin/env bash
# Assert the image metadata every published baseline-security image must carry:
# a non-root USER (where the image is executed) and the OCI source/license
# labels consumers and scanners read.
#
#   Usage: hack/verify-image-metadata.sh <image> [--allow-scratch-user]
#
# --allow-scratch-user is for FROM scratch bundles, which have no USER because
# they are unpacked by OLM rather than run as a process.
set -euo pipefail

EXPECTED_LICENSES="Apache-2.0"
EXPECTED_SOURCE="https://github.com/maci0/openshift-baseline-security"

usage() {
  cat <<EOF
Usage: $(basename "$0") <image> [--allow-scratch-user]

Asserts that <image> declares a non-root USER (unless --allow-scratch-user)
and carries the expected org.opencontainers.image licenses and source labels.
--allow-scratch-user is for FROM scratch bundles: OLM unpacks them, no process
runs, so no USER is required, but a declared root USER is still rejected.
EOF
}

case "${1:-}" in
  -h | --help)
    if [ "$#" -ne 1 ]; then
      echo "--help takes no arguments" >&2
      exit 2
    fi
    usage
    exit 0
    ;;
  -*)
    echo "unknown option: $1" >&2
    usage >&2
    exit 2
    ;;
esac

if [ "$#" -lt 1 ] || [ "$#" -gt 2 ]; then
  echo "unexpected arguments; expected <image> [--allow-scratch-user]" >&2
  usage >&2
  exit 2
fi
image="$1"
allow_scratch_user="${2:-}"

case "$allow_scratch_user" in
  "") ;;
  --allow-scratch-user) ;;
  *)
    echo "unknown option: $allow_scratch_user" >&2
    usage >&2
    exit 2
    ;;
esac

if [ "$allow_scratch_user" = "--allow-scratch-user" ]; then
  user=$(docker image inspect -f '{{.Config.User}}' "$image")
  echo "Config.User=${user} (scratch image, USER not required)"
  if [ -n "$user" ] && { [ "$user" = "0" ] || [ "$user" = "root" ] || [ "$user" = "0:0" ]; }; then
    echo "scratch image ${image} must not declare a root USER" >&2
    exit 1
  fi
else
  user=$(docker image inspect -f '{{.Config.User}}' "$image")
  echo "Config.User=${user}"
  [ -n "$user" ] || { echo "${image} has no USER; it would run as root" >&2; exit 1; }
  [ "$user" != "0" ] || { echo "${image} runs as root (USER 0)" >&2; exit 1; }
  [ "$user" != "root" ] || { echo "${image} runs as root (USER root)" >&2; exit 1; }
  [ "$user" != "0:0" ] || { echo "${image} runs as root (USER 0:0)" >&2; exit 1; }
fi

lic=$(docker image inspect -f '{{index .Config.Labels "org.opencontainers.image.licenses"}}' "$image")
echo "org.opencontainers.image.licenses=${lic}"
[ "$lic" = "$EXPECTED_LICENSES" ] || {
  echo "${image} OCI licenses label is ${lic}, want ${EXPECTED_LICENSES}" >&2
  exit 1
}

src=$(docker image inspect -f '{{index .Config.Labels "org.opencontainers.image.source"}}' "$image")
echo "org.opencontainers.image.source=${src}"
[ "$src" = "$EXPECTED_SOURCE" ] || {
  echo "${image} OCI source label is ${src}, want ${EXPECTED_SOURCE}" >&2
  exit 1
}
