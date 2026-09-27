#!/usr/bin/env bash
# SHA-256 helper, sourced by the hack/ scripts that digest an artifact.
#
# `sha256sum` is GNU coreutils: macOS ships none, so a contributor or an admin
# on a Mac reaches a script that digests an artifact and gets "command not
# found" (exit 127) rather than a digest. The scripts that need one run
# wherever `oc` runs, so the tool is resolved here once instead of per caller.
#
# Sourced, not executed. Callers do:
#   . "$(dirname "${BASH_SOURCE[0]}")/lib-sha256.sh"
#   sha256_init || exit 1
# then call sha256_stdin or sha256_file. Both read stdin, so no tool has to
# accept a `--` end-of-options marker (openssl dgst does not) and a path that
# begins with a dash needs no special case.
#
# Output shapes differ: `sha256sum` and `shasum` print `<hex>  -` for stdin,
# `openssl dgst` prints `SHA256(stdin)= <hex>`. Dropping everything up to and
# including the last `= ` leaves the bare hex in both cases, so the digest is
# the first field of that.

SHA256_TOOL=()

sha256_init() {
  if command -v sha256sum >/dev/null 2>&1; then
    SHA256_TOOL=(sha256sum)
  elif command -v shasum >/dev/null 2>&1; then
    SHA256_TOOL=(shasum -a 256)
  elif command -v openssl >/dev/null 2>&1; then
    SHA256_TOOL=(openssl dgst -sha256)
  else
    echo "no SHA-256 tool found: need coreutils (sha256sum), perl's shasum, or openssl" >&2
    return 1
  fi
  return 0
}

sha256_stdin() {
  if [ "${#SHA256_TOOL[@]}" -eq 0 ]; then
    echo "sha256_stdin: sha256_init was not called or found no tool" >&2
    return 1
  fi
  "${SHA256_TOOL[@]}" | sed -E 's/^.*= //' | awk '{print $1}'
}

sha256_file() {
  if [ "$#" -ne 1 ]; then
    echo "sha256_file: expected exactly one path" >&2
    return 2
  fi
  sha256_stdin <"$1"
}
