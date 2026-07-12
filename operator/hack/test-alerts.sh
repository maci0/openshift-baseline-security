#!/usr/bin/env bash
# Unit-test the PrometheusRule alerts with promtool (run in a container so the
# host needs no prometheus install). Generates the plain rules file from the
# PrometheusRule CR so the alert expressions have a single source of truth.
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
RULE_CR="$ROOT/config/prometheus/prometheusrule.yaml"
TEST_FILE="$ROOT/config/prometheus/testdata/alerts_test.yaml"
# Digest-pinned so CI alert unit tests cannot silently drift to a different promtool.
PROM_IMAGE="${PROM_IMAGE:-prom/prometheus@sha256:2659f4c2ebb718e7695cb9b25ffa7d6be64db013daba13e05c875451cf51b0d3}"

# Fail fast with a clear message instead of a cryptic python/docker error later.
command -v python3 >/dev/null || { echo "python3 is required on PATH for alert unit tests" >&2; exit 1; }
command -v docker >/dev/null || { echo "docker is required to run promtool for alert unit tests" >&2; exit 1; }
test -f "$RULE_CR" || { echo "missing PrometheusRule CR: $RULE_CR" >&2; exit 1; }
test -f "$TEST_FILE" || { echo "missing alert unit tests: $TEST_FILE" >&2; exit 1; }

work="$(mktemp -d)"
trap 'rm -rf "$work"' EXIT

# Stdlib-only extractor (no PyYAML / uv). Requires python3 on PATH.
python3 "$ROOT/hack/prometheusrule_to_rules.py" "$RULE_CR" "$work/rules.yaml"
cp "$TEST_FILE" "$work/alerts_test.yaml"
# promtool runs as an unprivileged user in the image; make the mounted files
# world-readable (mktemp dirs are 0700).
chmod -R a+rX "$work"

# --network=none: promtool only reads mounted rules/tests. Pull first when the
# digest is not already local so disconnected hosts with a warm cache still work.
# Retry pull on transient registry errors (rate limit / blip); do not retry the
# test itself so a real promtool failure stays a single clear exit.
if ! docker image inspect "$PROM_IMAGE" >/dev/null 2>&1; then
  pulled=0
  for attempt in 1 2 3; do
    if docker pull "$PROM_IMAGE"; then
      pulled=1
      break
    fi
    echo "docker pull failed (attempt ${attempt}/3); retrying..." >&2
    sleep $((attempt * 5))
  done
  if [ "$pulled" -ne 1 ]; then
    echo "failed to pull $PROM_IMAGE after 3 attempts" >&2
    exit 1
  fi
fi
# Hardened run: no network, read-only rootfs + host mount, drop caps.
# Matches operator/Makefile DOCKER_RUN_FLAGS for pure validation containers.
docker run --rm --network=none --read-only --tmpfs /tmp --cap-drop ALL \
  --security-opt no-new-privileges:true \
  -v "$work:/t:ro,Z" --entrypoint promtool "$PROM_IMAGE" test rules /t/alerts_test.yaml
