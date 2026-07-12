#!/usr/bin/env bash
# Collect baseline-security state for support/debugging.
# Usage: hack/must-gather.sh [output-dir]   (defaults to ./must-gather)
set -euo pipefail
OUT="${1:-must-gather}"
# Refuse empty, stdout marker, or flag-shaped paths so a bad invocation cannot
# mkdir "-" / "" or treat an option as a directory.
if [ -z "$OUT" ] || [ "$OUT" = "-" ] || [[ "$OUT" == -* ]]; then
  echo "invalid output directory: ${OUT:-<empty>}" >&2
  exit 1
fi
mkdir -p -- "$OUT"
# Owner-only: dumps include logs/events that may carry cluster-sensitive data.
chmod 700 -- "$OUT"

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
oc get clusterbaseline cluster -o yaml > "$OUT/clusterbaseline.yaml" 2>/dev/null \
  || warn_fail clusterbaseline.yaml
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
