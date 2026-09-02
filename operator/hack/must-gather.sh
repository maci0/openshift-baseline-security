#!/usr/bin/env bash
# Collect baseline-security state for support/debugging.
# Usage: hack/must-gather.sh [output-dir]   (defaults to ./must-gather)
#        hack/must-gather.sh --self-test    (redaction unit test; no cluster)
set -euo pipefail

# spec.waivers[].requestedBy and approvedBy identify cluster users (audit
# attribution). kubectl last-applied-configuration can embed the same fields
# as a JSON blob. Strip both from a ClusterBaseline YAML dump so those
# identities do not leave the cluster in a support archive. Waiver name and
# reason stay: they are required to debug scoring.
redact_clusterbaseline_dump() {
  local f="$1"
  [ -s "$f" ] || return 0
  # kubectl YAML emits each mapping key on its own line;
  # last-applied-configuration is typically one quoted line.
  # The JSON substitutions catch a folded annotation value that survives the
  # key-line delete. Rewrite via a temp file: GNU sed -i is not accepted by
  # BSD sed (macOS), which treats the next argument as a required backup suffix.
  local tmp
  tmp="$(mktemp)"
  sed -E \
    -e '/kubectl\.kubernetes\.io\/last-applied-configuration:/d' \
    -e '/^[[:space:]]+(requestedBy|approvedBy):/d' \
    -e 's/"requestedBy":"[^"]*"[[:space:]]*,?[[:space:]]*//g' \
    -e 's/"approvedBy":"[^"]*"[[:space:]]*,?[[:space:]]*//g' \
    "$f" > "$tmp" || { rm -f -- "$tmp"; return 1; }
  cat "$tmp" > "$f" || { rm -f -- "$tmp"; return 1; }
  rm -f -- "$tmp"
}

# Offline check that attribution does not survive a typical kubectl YAML dump.
# No oc, no cluster. Invoked as --self-test and from `make test`.
self_test() {
  (
    work="$(mktemp -d)"
    trap 'rm -rf -- "$work"' EXIT
    out="$work/clusterbaseline.yaml"
    cat > "$out" <<'EOF'
apiVersion: baselinesecurity.openshift.io/v1alpha1
kind: ClusterBaseline
metadata:
  name: cluster
  annotations:
    kubectl.kubernetes.io/last-applied-configuration: '{"spec":{"waivers":[{"requestedBy":"alice","approvedBy":"bob","name":"ocp4-cis-some-check"}]}}'
    baselinesecurity.openshift.io/other: keep
spec:
  waivers:
    - name: ocp4-cis-some-check
      reason: accepted risk
      requestedBy: alice
      approvedBy: bob
      expiresAt: "2099-01-01T00:00:00Z"
EOF
    redact_clusterbaseline_dump "$out"
    if grep -q 'requestedBy' "$out"; then
      echo "FAIL: requestedBy still present" >&2
      cat "$out" >&2
      exit 1
    fi
    if grep -q 'approvedBy' "$out"; then
      echo "FAIL: approvedBy still present" >&2
      cat "$out" >&2
      exit 1
    fi
    if grep -q 'last-applied-configuration' "$out"; then
      echo "FAIL: last-applied-configuration still present" >&2
      cat "$out" >&2
      exit 1
    fi
    grep -q 'name: ocp4-cis-some-check' "$out" || {
      echo "FAIL: waiver name dropped" >&2
      cat "$out" >&2
      exit 1
    }
    grep -q 'reason: accepted risk' "$out" || {
      echo "FAIL: waiver reason dropped" >&2
      cat "$out" >&2
      exit 1
    }
    grep -q 'baselinesecurity.openshift.io/other: keep' "$out" || {
      echo "FAIL: unrelated annotation dropped" >&2
      cat "$out" >&2
      exit 1
    }

    # Folded last-applied-configuration: the key line is deleted but the JSON
    # continuation remains; JSON substitutions must still strip attribution.
    folded="$work/folded.yaml"
    cat > "$folded" <<'EOF'
metadata:
  annotations:
    kubectl.kubernetes.io/last-applied-configuration: >
      {"spec":{"waivers":[{"requestedBy":"alice","approvedBy":"bob","name":"x"}]}}
spec:
  waivers:
    - name: x
      reason: r
EOF
    redact_clusterbaseline_dump "$folded"
    if grep -q 'requestedBy' "$folded"; then
      echo "FAIL: folded requestedBy still present" >&2
      cat "$folded" >&2
      exit 1
    fi
    if grep -q 'approvedBy' "$folded"; then
      echo "FAIL: folded approvedBy still present" >&2
      cat "$folded" >&2
      exit 1
    fi
    grep -q '"name":"x"' "$folded" || {
      echo "FAIL: folded JSON name dropped" >&2
      cat "$folded" >&2
      exit 1
    }
    echo "must-gather redaction self-test ok"
  )
}

if [ "${1:-}" = "--self-test" ]; then
  self_test
  exit 0
fi

OUT="${1:-must-gather}"
# Refuse empty, stdout marker, or flag-shaped paths so a bad invocation cannot
# mkdir "-" / "" or treat an option as a directory.
if [ -z "$OUT" ] || [ "$OUT" = "-" ] || [[ "$OUT" == -* ]]; then
  echo "invalid output directory: ${OUT:-<empty>}" >&2
  exit 1
fi
mkdir -p -- "$OUT"
# Owner-only: dumps include logs/events that may carry cluster-sensitive data.
# Waiver requestedBy/approvedBy are stripped from clusterbaseline.yaml below.
chmod 700 -- "$OUT"

# Bound every API call. must-gather is run precisely when the cluster is
# unhealthy, so a wedged apiserver (stuck webhook, slow etcd) would otherwise
# hang the whole collector on a single oc call with no output. command avoids
# recursing into this wrapper; the flag applies to every oc below, including the
# auth gate and the relatedObjects loop.
oc() { command oc --request-timeout=30s "$@"; }

# Fail fast when the kubeconfig is missing or expired so support does not get
# an empty directory that looks like a successful collection.
if ! oc whoami >/dev/null 2>&1; then
  echo "oc is not authenticated (oc whoami failed); refusing empty must-gather" >&2
  exit 1
fi

# Track which best-effort dumps failed so an empty file is not mistaken for a
# successful collection (e.g. CR missing, RBAC, wrong namespace). Soft-fail
# targets (plugin absent, no previous logs) stay as || true without counting.
failures=0
warn_fail() {
  echo "warning: failed to collect $1" >&2
  failures=$((failures + 1))
}

# ClusterBaseline status (score, conditions, remediationBatch, relatedObjects).
# Strip waiver attribution after a successful get so names do not leave the
# cluster; keep the dump even if redaction is a no-op (no waivers present).
if oc get clusterbaseline cluster -o yaml > "$OUT/clusterbaseline.yaml" 2>/dev/null; then
  redact_clusterbaseline_dump "$OUT/clusterbaseline.yaml"
else
  warn_fail clusterbaseline.yaml
fi
oc get clusterbaseline cluster -o jsonpath='{range .status.conditions[*]}{.type}={.status} reason={.reason} msg={.message}{"\n"}{end}' \
  > "$OUT/clusterbaseline-conditions.txt" 2>/dev/null \
  || warn_fail clusterbaseline-conditions.txt

# Operator namespace: workloads, monitoring CRs, recent events.
# Never dump Secret objects: metrics TLS keys and scraper SA tokens would land
# on disk (and in support attachments). Names/types only for triage.
# Include PDBs (not in `all`): operator + plugin minAvailable during drains.
oc -n openshift-baseline-security get all,configmap,servicemonitor,prometheusrule,poddisruptionbudget -o yaml \
  > "$OUT/operator-namespace.yaml" 2>/dev/null \
  || warn_fail operator-namespace.yaml
# The console dashboard ConfigMap lives in openshift-config-managed, outside the
# operator namespace dumped above. Collect it so "dashboard missing from the
# console" can be triaged (never created vs wrong labels vs user-deleted).
oc -n openshift-config-managed get configmap baseline-security-compliance-dashboard -o yaml \
  > "$OUT/dashboard-configmap.yaml" 2>/dev/null || true
oc -n openshift-baseline-security get secrets \
  -o custom-columns=NAME:.metadata.name,TYPE:.type,AGE:.metadata.creationTimestamp \
  > "$OUT/operator-secrets.txt" 2>/dev/null \
  || warn_fail operator-secrets.txt
oc -n openshift-baseline-security get events --sort-by='.lastTimestamp' \
  > "$OUT/operator-events.txt" 2>/dev/null \
  || warn_fail operator-events.txt
# All replicas + previous container (crash-loop) when present.
oc -n openshift-baseline-security logs deploy/baseline-security-operator --all-containers --tail=-1 \
  > "$OUT/operator.log" 2>/dev/null \
  || warn_fail operator.log
# Previous logs are often absent (no restart); do not count as a failure.
oc -n openshift-baseline-security logs deploy/baseline-security-operator --all-containers --previous --tail=-1 \
  > "$OUT/operator-previous.log" 2>/dev/null || true
oc -n openshift-baseline-security describe deploy/baseline-security-operator \
  > "$OUT/operator-deploy-describe.txt" 2>/dev/null \
  || warn_fail operator-deploy-describe.txt

# Console plugin Deployment (same namespace): nginx access/error streams and
# rollout state. Absent when ConsolePluginReady is ImageMissing/Disabled.
# Soft-fail: plugin may not be deployed.
oc -n openshift-baseline-security logs deploy/baseline-security-console-plugin --all-containers --tail=-1 \
  > "$OUT/console-plugin.log" 2>/dev/null || true
oc -n openshift-baseline-security logs deploy/baseline-security-console-plugin --all-containers --previous --tail=-1 \
  > "$OUT/console-plugin-previous.log" 2>/dev/null || true
oc -n openshift-baseline-security describe deploy/baseline-security-console-plugin \
  > "$OUT/console-plugin-deploy-describe.txt" 2>/dev/null || true

# Compliance Operator objects (scans, results, remediations).
oc -n openshift-compliance get scansettings,scansettingbindings,tailoredprofiles,compliancesuites,compliancescans,compliancecheckresults,complianceremediations -o yaml \
  > "$OUT/compliance.yaml" 2>/dev/null \
  || warn_fail compliance.yaml
oc -n openshift-compliance get events --sort-by='.lastTimestamp' \
  > "$OUT/compliance-events.txt" 2>/dev/null \
  || warn_fail compliance-events.txt

# MachineConfigPools: pause state is critical for RemediationBatchStuck.
oc get mcp -o yaml > "$OUT/machineconfigpools.yaml" 2>/dev/null \
  || warn_fail machineconfigpools.yaml
oc get mcp -o custom-columns=NAME:.metadata.name,PAUSED:.spec.paused,UPDATED:.status.updatedMachineCount,UPDATING:.status.updatingMachineCount,DEGRADED:.status.degradedMachineCount \
  > "$OUT/machineconfigpools-pause.txt" 2>/dev/null \
  || warn_fail machineconfigpools-pause.txt

# Soft-fail: Console capability may be disabled.
oc get consoleplugin baseline-security-console-plugin -o yaml > "$OUT/consoleplugin.yaml" 2>/dev/null || true

# relatedObjects declared by the CR (group/resource/name[/namespace]).
# Only DNS-1123-shaped tokens are passed to oc (status is operator-written, but
# a hand-edited or corrupted relatedObjects list must not become shell noise).
# Reject leading dashes (oc flag injection) and '/' in resource (type/name
# shorthand). Tokens: alnum / dash / dot only.
{ oc get clusterbaseline cluster -o jsonpath='{range .status.relatedObjects[*]}{.resource}.{.group} {.name} {.namespace}{"\n"}{end}' 2>/dev/null || true; } \
  | while read -r res name ns; do
      [ -z "$res" ] && continue
      case "$res" in -*|*[!a-z0-9.-]*) continue ;; esac
      case "$name" in ''|-*|*[!a-z0-9.-]*) continue ;; esac
      if [ -n "$ns" ]; then
        case "$ns" in -*|*[!a-z0-9.-]*) continue ;; esac
        oc -n "$ns" get "$res" "$name" -o yaml >> "$OUT/related-objects.yaml" 2>/dev/null || true
      else
        oc get "$res" "$name" -o yaml >> "$OUT/related-objects.yaml" 2>/dev/null || true
      fi
      echo '---' >> "$OUT/related-objects.yaml"
    done

echo "Collected baseline-security must-gather into $OUT"
if [ "$failures" -gt 0 ]; then
  echo "warning: ${failures} collection step(s) failed (see warnings above); archive may be incomplete" >&2
fi
