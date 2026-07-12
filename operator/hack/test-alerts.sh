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
docker image inspect "$PROM_IMAGE" >/dev/null 2>&1 || docker pull "$PROM_IMAGE"
docker run --rm --network=none -v "$work:/t:Z" --entrypoint promtool "$PROM_IMAGE" test rules /t/alerts_test.yaml
