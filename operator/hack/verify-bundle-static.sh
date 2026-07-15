#!/usr/bin/env bash
# Fail if a hand-copied bundle manifest drifts from its config/ source.
#
# Most operator/bundle/manifests/ files are generated or already guarded
# (CRD by cp+git-diff, PrometheusRule/ServiceMonitor by verify-monitoring-bundle,
# CSV RBAC by verify-csv-rbac). The static RBAC/Service/ConfigMap/PDB/scraper
# manifests below are hand-copied from config/ with no other guardrail, so a
# permission or setting change in config/ can silently ship a stale bundle.
#
# Check: the normalized bundle file must appear as a contiguous block inside its
# normalized config source (which may be multi-doc). Normalization strips
# comments, blank lines, and the namespace:/apiVersion: lines that kustomize/OLM
# add or move, so the check is robust to those without a full YAML parser.
#
# Run from operator/ (make verify-bundle-static) or with REPO_ROOT set.
set -euo pipefail

ROOT="${REPO_ROOT:-$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)}"
OP="${ROOT}/operator"
BUNDLE="${OP}/bundle/manifests"

# bundle-file <TAB> config-source. The config source is searched for the bundle
# object's normalized body as a contiguous substring.
PAIRS=$(cat <<'EOF'
baseline-security-admin.clusterrole.yaml	config/rbac/user_roles.yaml
baseline-security-viewer.clusterrole.yaml	config/rbac/user_roles.yaml
baseline-security-metrics-reader_rbac.authorization.k8s.io_v1_clusterrole.yaml	config/rbac/metrics_reader_role.yaml
baseline-security-metrics-scraper_v1_serviceaccount.yaml	config/prometheus/servicemonitor.yaml
baseline-security-metrics-scraper-token_v1_secret.yaml	config/prometheus/servicemonitor.yaml
baseline-security-metrics-scraper_rbac.authorization.k8s.io_v1_clusterrolebinding.yaml	config/prometheus/servicemonitor.yaml
baseline-security-metrics-serving-ca_v1_configmap.yaml	config/prometheus/serving-ca-configmap.yaml
baseline-security-operator-metrics_v1_service.yaml	config/manager/metrics_service.yaml
EOF
)

fail=0
while IFS=$'\t' read -r bfile csrc; do
	[ -n "$bfile" ] || continue
	b="${BUNDLE}/${bfile}"
	s="${OP}/${csrc}"
	if [ ! -f "$b" ]; then echo "verify-bundle-static: missing bundle file $b" >&2; fail=1; continue; fi
	if [ ! -f "$s" ]; then echo "verify-bundle-static: missing config source $s" >&2; fail=1; continue; fi
	awk -v src="$s" '
		function norm(l){ sub(/[[:space:]]+$/,"",l); return l }
		{
			if ($0 ~ /^[[:space:]]*#/) next
			if ($0 ~ /^[[:space:]]*$/) next
			if ($0 ~ /^[[:space:]]*namespace:/) next
			if ($0 ~ /^apiVersion:/) next
			if (FILENAME==src) S=S norm($0) "\n"; else B=B norm($0) "\n"
		}
		END{ if (index(S,B)==0) exit 1 }
	' "$s" "$b" || { echo "verify-bundle-static: $bfile drifted from $csrc (re-copy the object)" >&2; fail=1; }
done <<< "$PAIRS"

test "$fail" -eq 0
