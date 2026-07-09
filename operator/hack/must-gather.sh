#!/usr/bin/env bash
# Collect baseline-security state for support/debugging.
# Usage: hack/must-gather.sh [output-dir]   (defaults to ./must-gather)
set -euo pipefail
OUT="${1:-must-gather}"
mkdir -p "$OUT"

oc get clusterbaseline cluster -o yaml > "$OUT/clusterbaseline.yaml" 2>/dev/null || true
oc -n openshift-baseline-security get all,configmap,servicemonitor,prometheusrule -o yaml > "$OUT/operator-namespace.yaml" 2>/dev/null || true
oc -n openshift-baseline-security logs deploy/baseline-security-operator --tail=-1 > "$OUT/operator.log" 2>/dev/null || true
oc -n openshift-compliance get scansettings,scansettingbindings,tailoredprofiles,compliancesuites,compliancescans,compliancecheckresults,complianceremediations -o yaml > "$OUT/compliance.yaml" 2>/dev/null || true
oc get consoleplugin baseline-security-console-plugin -o yaml > "$OUT/consoleplugin.yaml" 2>/dev/null || true

# relatedObjects declared by the CR (group/resource/name[/namespace]).
{ oc get clusterbaseline cluster -o jsonpath='{range .status.relatedObjects[*]}{.resource}.{.group} {.name} {.namespace}{"\n"}{end}' 2>/dev/null || true; } \
  | while read -r res name ns; do
      [ -z "$res" ] && continue
      if [ -n "$ns" ]; then oc -n "$ns" get "$res" "$name" -o yaml >> "$OUT/related-objects.yaml" 2>/dev/null || true
      else oc get "$res" "$name" -o yaml >> "$OUT/related-objects.yaml" 2>/dev/null || true; fi
      echo '---' >> "$OUT/related-objects.yaml"
    done

echo "Collected baseline-security must-gather into $OUT"
