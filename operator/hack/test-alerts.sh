#!/usr/bin/env bash
# Unit-test the PrometheusRule alerts with promtool (run in a container so the
# host needs no prometheus install). Generates the plain rules file from the
# PrometheusRule CR so the alert expressions have a single source of truth.
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
RULE_CR="$ROOT/config/prometheus/prometheusrule.yaml"
TEST_FILE="$ROOT/config/prometheus/testdata/alerts_test.yaml"
PROM_IMAGE="${PROM_IMAGE:-prom/prometheus:v2.55.1}"

work="$(mktemp -d)"
trap 'rm -rf "$work"' EXIT

# Use uv to pull PyYAML on demand when available (no host/CI pollution); fall
# back to the ambient python3 (which has PyYAML) otherwise.
if command -v uv >/dev/null 2>&1; then
  PY=(uv run --quiet --with pyyaml python3)
else
  PY=(python3)
fi
"${PY[@]}" "$ROOT/hack/prometheusrule_to_rules.py" "$RULE_CR" "$work/rules.yaml"
cp "$TEST_FILE" "$work/alerts_test.yaml"
# promtool runs as an unprivileged user in the image; make the mounted files
# world-readable (mktemp dirs are 0700).
chmod -R a+rX "$work"

docker run --rm -v "$work:/t:Z" --entrypoint promtool "$PROM_IMAGE" test rules /t/alerts_test.yaml
