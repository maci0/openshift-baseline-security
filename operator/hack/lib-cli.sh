#!/usr/bin/env bash
# Argument handling for the hack/ scripts that take no arguments.
#
# Nine of them spell -h/--help and the unexpected-argument error the same way,
# and a ninth edit of one copy is a tenth place the usage can drift. The help
# text itself stays in the caller, because that is the part that differs: only
# the parsing and the two error messages are shared.
#
# Sourced, not executed. Callers do:
#   . "$(dirname "${BASH_SOURCE[0]}")/lib-cli.sh"
#   usage() { cat <<EOF ... EOF }
#   reject_arguments "$@"
#
# Reads the caller's `prog` and `usage`; `prog` must be set before this runs.
# shellcheck disable=SC2154 # prog is the caller's script name, set before this is sourced.
reject_arguments() {
  if [ "${1:-}" = "-h" ] || [ "${1:-}" = "--help" ]; then
    if [ "$#" -ne 1 ]; then
      echo "${prog}: --help takes no arguments" >&2
      usage >&2
      exit 2
    fi
    usage
    exit 0
  fi
  if [ "$#" -ne 0 ]; then
    echo "${prog}: unexpected arguments: $*" >&2
    usage >&2
    exit 2
  fi
}
