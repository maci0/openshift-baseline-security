#!/usr/bin/env bash
# ADR-024: fail if operator Go and console TypeScript product contracts drift.
# Run from operator/ (make verify-product-lockstep) or any cwd with REPO_ROOT set.
set -euo pipefail

prog="$(basename "$0")"

usage() {
  cat <<EOF
Usage: ${prog}

Fail if operator Go and console TypeScript product contracts drift.
Run from operator/ (make verify-product-lockstep) or with REPO_ROOT set.
EOF
}

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

ROOT="${REPO_ROOT:-$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)}"
API="${ROOT}/operator/api/v1alpha1/clusterbaseline_types.go"
SCORE="${ROOT}/operator/internal/controller/scoring.go"
BATCH="${ROOT}/operator/internal/controller/batch.go"
MODELS="${ROOT}/console-plugin/src/models.ts"
SCORING_TS="${ROOT}/console-plugin/src/scoring.ts"
STATUS_TS="${ROOT}/console-plugin/src/status.ts"
INCONSISTENT="${ROOT}/operator/internal/controller/inconsistent.go"
PATCHES="${ROOT}/console-plugin/src/patches.ts"
PLUGIN_GO="${ROOT}/operator/internal/controller/clusterbaseline_controller.go"
NGINX_CONF="${ROOT}/console-plugin/nginx.conf"
PLUGIN_DOCKERFILE="${ROOT}/console-plugin/Dockerfile"

fail=0
die() { echo "${prog}: $*" >&2; fail=1; }

need() {
  local f="$1"
  if [[ ! -f "$f" ]]; then
    die "missing $f"
    return 1
  fi
  return 0
}

need "$API" || true
need "$SCORE" || true
need "$BATCH" || true
need "$MODELS" || true
need "$SCORING_TS" || true
need "$STATUS_TS" || true
need "$INCONSISTENT" || true
need "$PATCHES" || true
need "$PLUGIN_GO" || true
need "$NGINX_CONF" || true
need "$PLUGIN_DOCKERFILE" || true
if [[ "$fail" -ne 0 ]]; then
  exit 1
fi

# ProfileKey enum values from Go const block (quoted ProfileKey = "..." lines).
go_keys=$(
  sed -n '/^const (/,/^)/p' "$API" \
    | grep -E 'ProfileKey = "' \
    | sed -E 's/.*ProfileKey = "([^"]+)".*/\1/' \
    | sort || true
)
# Console PROFILE_KEYS array string literals.
ts_keys=$(
  sed -n '/export const PROFILE_KEYS/,/] as const/p' "$MODELS" \
    | grep -oE "'[^']+'" \
    | tr -d "'" \
    | sort || true
)
if [[ -z "$go_keys" ]]; then
  die "no ProfileKey constants in $API"
elif [[ -z "$ts_keys" ]]; then
  die "no PROFILE_KEYS entries in $MODELS"
elif [[ "$go_keys" != "$ts_keys" ]]; then
  die "ProfileKey set differs between operator and console"
  echo "  go: $(echo "$go_keys" | tr '\n' ' ')" >&2
  echo "  ts: $(echo "$ts_keys" | tr '\n' ' ')" >&2
fi

# Default scan schedule.
go_sched=$(grep -E 'DefaultScanSchedule[[:space:]]*=[[:space:]]*"' "$API" | head -1 | sed -E 's/.*"([^"]+)".*/\1/' || true)
ts_sched=$(grep -E "DEFAULT_SCAN_SCHEDULE[[:space:]]*=" "$MODELS" | head -1 | sed -E "s/.*'([^']+)'.*/\1/" || true)
if [[ -z "$go_sched" || -z "$ts_sched" ]]; then
  die "could not read DefaultScanSchedule / DEFAULT_SCAN_SCHEDULE"
elif [[ "$go_sched" != "$ts_sched" ]]; then
  die "DefaultScanSchedule ($go_sched) != DEFAULT_SCAN_SCHEDULE ($ts_sched)"
fi

# CRD MaxItems vs console client caps.
go_prof_max=$(grep -E 'MaxItems=8([^0-9]|$)' "$API" | head -1 || true)
ts_prof_max=$(grep -E 'PROFILE_MAX_ITEMS[[:space:]]*=[[:space:]]*8([^0-9]|$)' "$MODELS" | head -1 || true)
if [[ -z "$go_prof_max" || -z "$ts_prof_max" ]]; then
  die "Profiles MaxItems=8 / PROFILE_MAX_ITEMS=8 lockstep missing"
fi

go_tp_max=$(grep -E 'MaxItems=32([^0-9]|$)' "$API" | head -1 || true)
ts_tp_max=$(grep -E 'TAILORED_PROFILE_MAX_ITEMS[[:space:]]*=[[:space:]]*32([^0-9]|$)' "$MODELS" | head -1 || true)
if [[ -z "$go_tp_max" || -z "$ts_tp_max" ]]; then
  die "TailoredProfiles MaxItems=32 / TAILORED_PROFILE_MAX_ITEMS=32 lockstep missing"
fi

go_w_max=$(grep -E 'MaxItems=256([^0-9]|$)' "$API" | head -1 || true)
ts_w_max=$(grep -E 'WAIVER_MAX_ITEMS[[:space:]]*=[[:space:]]*256([^0-9]|$)' "$MODELS" | head -1 || true)
if [[ -z "$go_w_max" || -z "$ts_w_max" ]]; then
  die "Waivers MaxItems=256 / WAIVER_MAX_ITEMS=256 lockstep missing"
fi

# History ring cap.
go_hist=$(grep -E 'HistoryMax[[:space:]]*=[[:space:]]*30([^0-9]|$)' "$API" | head -1 || true)
if [[ -z "$go_hist" ]]; then
  die "HistoryMax = 30 missing from API (CRD MaxItems=30)"
fi

# Scan-diff failure-name list cap (ADR-013). API constant must match every
# MaxItems=4096 on newlyFailed/fixed/previousFailures/diffBaseFailures.
go_fail_max=$(grep -E 'FailureListMax[[:space:]]*=[[:space:]]*4096([^0-9]|$)' "$API" | head -1 || true)
if [[ -z "$go_fail_max" ]]; then
  die "FailureListMax = 4096 missing from API (CRD MaxItems=4096)"
fi
fail_maxitems=$(grep -cE 'MaxItems=4096([^0-9]|$)' "$API" || true)
if [[ "$fail_maxitems" -lt 4 ]]; then
  die "expected >=4 MaxItems=4096 markers on failure-name lists in $API (got $fail_maxitems)"
fi

# Severity weights (ADR-022).
for pair in 'High:10' 'Medium:5' 'Low:2' 'Other:1'; do
  name=${pair%%:*}
  val=${pair##*:}
  if ! grep -qE "severityWeight${name}[[:space:]]+int64[[:space:]]*=[[:space:]]*${val}([^0-9]|$)" "$SCORE"; then
    die "operator severityWeight${name} != ${val}"
  fi
done
for pair in 'HIGH:10' 'MEDIUM:5' 'LOW:2' 'OTHER:1'; do
  name=${pair%%:*}
  val=${pair##*:}
  if ! grep -qE "SEVERITY_WEIGHT_${name}[[:space:]]*=[[:space:]]*${val}([^0-9]|$)" "$SCORING_TS"; then
    die "console SEVERITY_WEIGHT_${name} != ${val}"
  fi
done

# History scoring-mode annotation key.
go_ann=$(grep -E 'historyScoringModeAnn[[:space:]]*=' "$SCORE" | head -1 | sed -E 's/.*"([^"]+)".*/\1/' || true)
ts_ann=$(grep -E 'HISTORY_SCORING_MODE_ANN[[:space:]]*=' "$SCORING_TS" | head -1 | sed -E "s/.*'([^']+)'.*/\1/" || true)
if [[ -z "$go_ann" || -z "$ts_ann" ]]; then
  die "could not read history scoring-mode annotation key"
elif [[ "$go_ann" != "$ts_ann" ]]; then
  die "historyScoringModeAnn ($go_ann) != HISTORY_SCORING_MODE_ANN ($ts_ann)"
fi

# Compliance Operator label and annotation keys the console reads off cluster
# objects to route a result, weight it, or collapse an INCONSISTENT state. Both
# sides hard-code the strings, and the console only carries a comment saying they
# are lockstep: a CO key rename would silently drop every result into the wrong
# bucket (or out of the score) with no compile error on either side.
label_key() {
  local go_name="$1" go_re="$2" go_file="$3"
  local ts_name="$4" ts_re="$5" ts_file="$6"
  local go_val ts_val
  go_val=$(grep -E "$go_re" "$go_file" | head -1 | sed -E "s/.*\"([^\"]+)\".*/\1/" || true)
  ts_val=$(grep -E "$ts_re" "$ts_file" | head -1 | sed -E "s/.*'([^']+)'.*/\1/" || true)
  if [[ -z "$go_val" || -z "$ts_val" ]]; then
    die "could not read operator ${go_name} / console ${ts_name}"
  elif [[ "$go_val" != "$ts_val" ]]; then
    die "operator ${go_name} (${go_val}) != console ${ts_name} (${ts_val})"
  fi
}

label_key suiteLabel '^[[:space:]]*suiteLabel[[:space:]]*=' "$PLUGIN_GO" \
  SUITE_LABEL "^(export )?const SUITE_LABEL[[:space:]]*=" "$MODELS"
label_key checkSeverityLabel '^[[:space:]]*checkSeverityLabel[[:space:]]*=' "$PLUGIN_GO" \
  checkSeverityLabel "^(export )?const checkSeverityLabel[[:space:]]*=" "$SCORING_TS"
label_key scanNameLabel '^[[:space:]]*scanNameLabel[[:space:]]*=' "$PLUGIN_GO" \
  SCAN_NAME_LABEL "^(export )?const SCAN_NAME_LABEL[[:space:]]*=" "$MODELS"
label_key inconsistentSourceAnn '^[[:space:]]*inconsistentSourceAnn[[:space:]]*=' "$INCONSISTENT" \
  inconsistentSourceAnn "^(export )?const inconsistentSourceAnn[[:space:]]*=" "$STATUS_TS"
label_key mostCommonStatusAnn '^[[:space:]]*mostCommonStatusAnn[[:space:]]*=' "$INCONSISTENT" \
  mostCommonStatusAnn "^(export )?const mostCommonStatusAnn[[:space:]]*=" "$STATUS_TS"

# Node-scan name delimiter. poolFromRemediation (operator) and
# nodePoolFromScanName (console) both split a CO scan name on the last "-node-"
# to find the pool an apply will reboot. The separator is load-bearing on both
# sides and lives in each only as a literal inside a call, so both are matched
# at the call, not as a bare string in the file: the doc comments on either side
# quote the delimiter too, and a file-level grep cannot tell them apart.
if ! grep -qE 'LastIndex\([^,]+, "-node-"\)' "$BATCH"; then
  die "operator poolFromRemediation no longer splits the CO scan name on \"-node-\""
fi
if ! grep -qE "lastIndexOf\('-node-'\)" "$MODELS"; then
  die "console nodePoolFromScanName no longer splits the CO scan name on \"-node-\""
fi

# Batch apply annotation + cap.
go_batch_ann=$(grep -E 'batchApplyAnnotation[[:space:]]*=' "$BATCH" | head -1 | sed -E 's/.*"([^"]+)".*/\1/' || true)
ts_batch_ann=$(grep -E 'BATCH_APPLY_ANNOTATION[[:space:]]*=' "$PATCHES" | head -1 | sed -E "s/.*'([^']+)'.*/\1/" || true)
if [[ -z "$go_batch_ann" || -z "$ts_batch_ann" ]]; then
  die "could not read batch-apply annotation key"
elif [[ "$go_batch_ann" != "$ts_batch_ann" ]]; then
  die "batchApplyAnnotation ($go_batch_ann) != BATCH_APPLY_ANNOTATION ($ts_batch_ann)"
fi

go_batch_max=$(grep -E 'batchMaxRemediations[[:space:]]*=' "$BATCH" | head -1 | sed -E 's/.*=[[:space:]]*([0-9]+).*/\1/' || true)
ts_batch_max=$(grep -E 'batchApplyMaxNames[[:space:]]*=' "$PATCHES" | head -1 | sed -E 's/.*=[[:space:]]*([0-9]+).*/\1/' || true)
if [[ -z "$go_batch_max" || -z "$ts_batch_max" ]]; then
  die "could not read batch max remediations"
elif [[ "$go_batch_max" != "$ts_batch_max" ]]; then
  die "batchMaxRemediations ($go_batch_max) != batchApplyMaxNames ($ts_batch_max)"
fi

# Console plugin serving contract: the Go constants, the nginx config copied
# into the plugin image, and the image's EXPOSE are one port and one health
# path. The Service, the container port, the ConsolePlugin backend, and the
# kubelet probes all read the Go constants, so a drift on the nginx or Dockerfile
# side is the only one nothing in Go would catch: the pod still becomes Ready
# (the probe path is nginx's own constant return) while the console cannot reach
# the assets.
go_plugin_port=$(grep -E '^[[:space:]]*pluginPort = [0-9]+' "$PLUGIN_GO" | head -1 | sed -E 's/.*= *([0-9]+).*/\1/' || true)
go_healthz=$(grep -E '^[[:space:]]*pluginHealthzPath = ' "$PLUGIN_GO" | head -1 | sed -E 's/.*"([^"]+)".*/\1/' || true)
if [[ -z "$go_plugin_port" || -z "$go_healthz" ]]; then
  die "could not read pluginPort / pluginHealthzPath"
else
  nginx_port=$(grep -E '^[[:space:]]*listen [0-9]+ ssl' "$NGINX_CONF" | head -1 | sed -E 's/.*listen ([0-9]+) ssl.*/\1/' || true)
  if [[ "$nginx_port" != "$go_plugin_port" ]]; then
    die "nginx.conf listens on ${nginx_port:-<none>}, operator pluginPort is ${go_plugin_port}"
  fi
  if ! grep -qE "^EXPOSE ${go_plugin_port}\$" "$PLUGIN_DOCKERFILE"; then
    die "console-plugin Dockerfile must EXPOSE ${go_plugin_port} (matches nginx.conf and the operator Service)"
  fi
  if ! grep -qF "location = ${go_healthz}" "$NGINX_CONF"; then
    die "nginx.conf has no \`location = ${go_healthz}\`; the operator probes it for readiness and liveness"
  fi
fi

if [[ "$fail" -ne 0 ]]; then
  exit 1
fi
echo "${prog}: ok"
