#!/usr/bin/env bash
# Prove the release binary is bit-for-bit reproducible: build the same source
# twice from two different absolute paths, under a different timezone and
# locale, and compare SHA-256.
#
# The Makefile asserts this property in comments (-trimpath maps the build path
# out, -buildvcs=false drops the VCS stamp, -ldflags "-buildid=" clears the GNU
# build-id note) and relies on it to claim that a local bin/manager and the
# one inside the image match. An unasserted comment is a comment; this is the
# gate that turns it into a checked fact.
#
#   Usage: hack/verify-reproducible.sh <go build flags> <go ldflags>
#
# The flags come from Makefile GO_BUILD_FLAGS / GO_LDFLAGS, so this script never
# carries its own copy of the build recipe.
set -euo pipefail

# shellcheck source-path=SCRIPTDIR
# shellcheck source=lib-sha256.sh
. "$(dirname "${BASH_SOURCE[0]}")/lib-sha256.sh"

prog="$(basename "$0")"

usage() {
  cat <<EOF
Usage: ${prog} <go build flags> <go ldflags>

Builds ./cmd twice, from two different absolute paths and under a different
TZ/LC_ALL, then fails unless both binaries have the same SHA-256.
No arguments of its own beyond the build flags passed through from the Makefile.

Options:
  -h, --help   print this usage and exit 0
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

if [ "$#" -ne 2 ]; then
  echo "${prog}: expected exactly 2 arguments (go build flags, go ldflags)" >&2
  usage >&2
  exit 2
fi
build_flags="$1"
ldflags="$2"

if [ -z "$build_flags" ] || [ -z "$ldflags" ]; then
  echo "${prog}: build flags and ldflags must both be non-empty" >&2
  usage >&2
  exit 2
fi

command -v go >/dev/null || {
  echo "${prog}: go is required on PATH" >&2
  exit 1
}

sha256_init || {
  echo "${prog}: no SHA-256 tool to compare the builds with" >&2
  exit 1
}

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"

# The same source set the release Dockerfile COPYs. -buildvcs=false means no
# .git is needed; anything the build needs and this list omits fails loudly
# rather than silently comparing two wrong builds.
required=(go.mod go.sum api cmd internal)
for path in "${required[@]}"; do
  if [ ! -e "$ROOT/$path" ]; then
    echo "${prog}: missing build input: $path" >&2
    exit 1
  fi
done

work="$(mktemp -d)"
trap 'rm -rf "$work"' EXIT

build() {
  # read -r -a so the flag string splits on whitespace without glob expansion.
  local -a flags
  read -r -a flags <<<"$build_flags"
  CGO_ENABLED=0 go build "${flags[@]}" -ldflags="$ldflags" -o "$1" ./cmd
}

# Build 1: the canonical build, from the repository at its real path.
build "$work/first"
# Build 2: a different absolute path (so an untrimmed -trimpath would leak a
# different module prefix), a different timezone and locale, and a different
# umask.
mkdir -p "$work/second"
for path in "${required[@]}"; do
  cp -a "$ROOT/$path" "$work/second/"
done
(
  cd "$work/second"
  export TZ=Asia/Tokyo LC_ALL=en_US.UTF-8
  # An absent tzdata or an ungenerated locale makes the exports above
  # silently ineffective: the second build then runs in the same zone and
  # encoding as the first, and the check reports a property it never tested.
  # A container built FROM alpine has no locale and no zone database at all,
  # so this is a reachable host, not a hypothetical. Both probes are POSIX
  # (date and locale); a host with no `locale` at all keeps the export and is
  # not failed here.
  if [ "$(date +%z)" = "+0000" ]; then
    echo "${prog}: this host has no tzdata, so TZ=Asia/Tokyo had no effect" >&2
    echo "and the locale leg of this check cannot run. Install tzdata, or point" >&2
    echo "TZ at a zone file this host can read." >&2
    exit 1
  fi
  if command -v locale >/dev/null 2>&1; then
    # An ungenerated locale makes `locale charmap` itself fail under the
    # exported LC_ALL, so an empty result is the signal, not a mismatch.
    charmap="$(locale charmap 2>/dev/null || true)"
    c_charmap="$(LC_ALL=C locale charmap 2>/dev/null || true)"
    if [ -z "$charmap" ] || { [ -n "$c_charmap" ] && [ "$charmap" = "$c_charmap" ]; }; then
      echo "${prog}: this host has no en_US.UTF-8 locale, so LC_ALL had no effect." >&2
      echo "Install the locale (musl and glibc images ship without one), or change" >&2
      echo "the locale this script exports to one the host actually has." >&2
      exit 1
    fi
  fi
  umask 077
  build "$work/second-out"
)

for out in "$work/first" "$work/second-out"; do
  if [ ! -s "$out" ]; then
    echo "${prog}: $out is missing or empty after go build" >&2
    exit 1
  fi
done

first_sum="$(sha256_file "$work/first")"
second_sum="$(sha256_file "$work/second-out")"

if [ "$first_sum" != "$second_sum" ]; then
  echo "${prog}: build is NOT reproducible" >&2
  echo "  from $ROOT            sha256=$first_sum" >&2
  echo "  from $work/second     sha256=$second_sum" >&2
  echo "  differing inputs: build path, TZ, LC_ALL, umask" >&2
  exit 1
fi

echo "${prog}: reproducible (sha256=$first_sum across build path, TZ, LC_ALL, umask)"
