#!/usr/bin/env bash
# Clamp the mtime of every file under each given path to SOURCE_DATE_EPOCH.
#
# BuildKit's SOURCE_DATE_EPOCH build-arg clamps the *image and layer* creation
# timestamps. It does not touch the mtimes a COPY carries into a layer: those
# come from the file in the build context or the build stage. So a file whose
# mtime is the moment the checkout was made, or the moment a generator ran,
# gives two builds of one source two different image digests, and a digest
# comparison between a local build and a CI build says nothing.
#
# Every tree that goes into an image through COPY therefore passes through
# here: bundle/ (the OLM bundle image) and catalog/ (the FBC image, which the
# opm cache also reads). One script for both, so the two cannot derive their
# stamp differently.
#
#   Usage: hack/normalize-mtimes.sh <path> [path...]
#
# A path that does not exist is a failure, not a silent skip: every caller
# passes a tree it has just produced or validated, so a missing one means the
# build did not do what the caller believes it did. An argument that starts
# with a dash is a bad invocation, not a path: no caller passes one, and
# reading it as a path would report "no such path" for a typo'd flag.
set -euo pipefail

# `stamp` is computed with `date -u` but `touch -t` reads it in the local zone,
# so a caller in any other TZ stamps every file at the wrong instant and the
# image digest follows the builder's offset. Pin the zone the stamp is read in.
export TZ=UTC

prog="$(basename "$0")"

usage() {
  cat <<EOF
Usage: ${prog} <path> [path...]

Set the mtime of every file under each path to SOURCE_DATE_EPOCH (default 0),
so image layers built from them are byte-identical across build times.
EOF
}

case "${1:-}" in
-h | --help)
  if [ "$#" -ne 1 ]; then
    echo "${prog}: --help takes no arguments" >&2
    usage >&2
    exit 2
  fi
  usage
  exit 0
  ;;
esac

# This script takes no options, so a flag-shaped argument is a bad invocation
# rather than a path. Without this the flag falls through to the existence check
# and comes back as "no such path: --foo" with exit 1, which reads as a missing
# tree rather than the typo it is. It would also reach `find` unparsed by option
# handling, where a flag like -exec is an instruction rather than a filename.
# Checked before the argument count so `--typo` with no path reads as the flag
# it is; with no arguments at all the loop is empty and the count check fires.
for arg in "$@"; do
  case "$arg" in
  -?*)
    echo "${prog}: unknown option: ${arg}" >&2
    usage >&2
    exit 2
    ;;
  esac
done

if [ "$#" -eq 0 ]; then
  echo "${prog}: expected at least one path" >&2
  usage >&2
  exit 2
fi

epoch="${SOURCE_DATE_EPOCH:-0}"
case "$epoch" in
'' | *[!0-9]*)
  echo "${prog}: SOURCE_DATE_EPOCH must be a non-negative integer, got '${epoch}'" >&2
  exit 2
  ;;
esac

if [ "$epoch" = "0" ]; then
  stamp=197001010000.00
else
  # GNU date takes -d @seconds; BSD date (macOS) takes -r seconds. Either
  # `touch -t` stamp is the same instant, so the result does not depend on
  # which date(1) the host has.
  if ! stamp="$(date -u -d "@${epoch}" +%Y%m%d%H%M.%S 2>/dev/null ||
    date -u -r "${epoch}" +%Y%m%d%H%M.%S)"; then
    echo "${prog}: cannot convert SOURCE_DATE_EPOCH=${epoch} to a timestamp" >&2
    exit 1
  fi
fi

for path in "$@"; do
  if [ ! -e "$path" ]; then
    echo "${prog}: no such path: ${path}" >&2
    usage >&2
    exit 2
  fi
  # `find <path>` covers the path itself, so a directory's own mtime is clamped
  # too and a later `cp` into it does not reintroduce build time.
  find "$path" -exec touch -t "$stamp" {} +
done
