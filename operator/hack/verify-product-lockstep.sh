#!/usr/bin/env bash
# ADR-024: fail if operator Go and console TypeScript product contracts drift.
# Run from operator/ (make verify-product-lockstep) or any cwd with REPO_ROOT set.
set -euo pipefail

ROOT="${REPO_ROOT:-$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)}"
API="${ROOT}/operator/api/v1alpha1/clusterbaseline_types.go"
SCORE="${ROOT}/operator/internal/controller/scoring.go"
BATCH="${ROOT}/operator/internal/controller/batch.go"
MODELS="${ROOT}/console-plugin/src/models.ts"
SCORING_TS="${ROOT}/console-plugin/src/scoring.ts"
PATCHES="${ROOT}/console-plugin/src/patches.ts"

fail=0
die() { echo "verify-product-lockstep: $*" >&2; fail=1; }

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
need "$PATCHES" || true
if [[ "$fail" -ne 0 ]]; then
  exit 1
fi

# ProfileKey enum values from Go const block (quoted ProfileKey = "..." lines).
go_keys=$(
  sed -n '/^const (/,/^)/p' "$API" \
    | grep -E 'ProfileKey = "' \
    | sed -E 's/.*ProfileKey = "([^"]+)".*/\1/' \
    | sort
)
# Console PROFILE_KEYS array string literals.
ts_keys=$(
  sed -n '/export const PROFILE_KEYS/,/] as const/p' "$MODELS" \
    | grep -oE "'[^']+'" \
    | tr -d "'" \
    | sort
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
go_sched=$(grep -E 'DefaultScanSchedule\s*=\s*"' "$API" | head -1 | sed -E 's/.*"([^"]+)".*/\1/')
ts_sched=$(grep -E "DEFAULT_SCAN_SCHEDULE\s*=" "$MODELS" | head -1 | sed -E "s/.*'([^']+)'.*/\1/")
if [[ -z "$go_sched" || -z "$ts_sched" ]]; then
  die "could not read DefaultScanSchedule / DEFAULT_SCAN_SCHEDULE"
elif [[ "$go_sched" != "$ts_sched" ]]; then
  die "DefaultScanSchedule ($go_sched) != DEFAULT_SCAN_SCHEDULE ($ts_sched)"
fi

# CRD MaxItems vs console client caps.
go_prof_max=$(grep -E 'MaxItems=8' "$API" | head -1 || true)
ts_prof_max=$(grep -E 'PROFILE_MAX_ITEMS\s*=\s*8' "$MODELS" | head -1 || true)
if [[ -z "$go_prof_max" || -z "$ts_prof_max" ]]; then
  die "Profiles MaxItems=8 / PROFILE_MAX_ITEMS=8 lockstep missing"
fi

go_tp_max=$(grep -E 'MaxItems=32' "$API" | head -1 || true)
ts_tp_max=$(grep -E 'TAILORED_PROFILE_MAX_ITEMS\s*=\s*32' "$MODELS" | head -1 || true)
if [[ -z "$go_tp_max" || -z "$ts_tp_max" ]]; then
  die "TailoredProfiles MaxItems=32 / TAILORED_PROFILE_MAX_ITEMS=32 lockstep missing"
fi

go_w_max=$(grep -E 'MaxItems=256' "$API" | head -1 || true)
ts_w_max=$(grep -E 'WAIVER_MAX_ITEMS\s*=\s*256' "$MODELS" | head -1 || true)
if [[ -z "$go_w_max" || -z "$ts_w_max" ]]; then
  die "Waivers MaxItems=256 / WAIVER_MAX_ITEMS=256 lockstep missing"
fi

# History ring cap.
go_hist=$(grep -E 'HistoryMax\s*=\s*30' "$API" | head -1 || true)
if [[ -z "$go_hist" ]]; then
  die "HistoryMax = 30 missing from API (CRD MaxItems=30)"
fi

# Severity weights (ADR-022).
for pair in 'High:10' 'Medium:5' 'Low:2' 'Other:1'; do
  name=${pair%%:*}
  val=${pair##*:}
  if ! grep -qE "severityWeight${name}\\s+int64\\s*=\\s*${val}" "$SCORE"; then
    die "operator severityWeight${name} != ${val}"
  fi
done
for pair in 'HIGH:10' 'MEDIUM:5' 'LOW:2' 'OTHER:1'; do
  name=${pair%%:*}
  val=${pair##*:}
  if ! grep -qE "SEVERITY_WEIGHT_${name}\\s*=\\s*${val}" "$SCORING_TS"; then
    die "console SEVERITY_WEIGHT_${name} != ${val}"
  fi
done

# History scoring-mode annotation key.
go_ann=$(grep -E 'historyScoringModeAnn\s*=' "$SCORE" | head -1 | sed -E 's/.*"([^"]+)".*/\1/')
ts_ann=$(grep -E 'HISTORY_SCORING_MODE_ANN\s*=' "$SCORING_TS" | head -1 | sed -E "s/.*'([^']+)'.*/\1/")
if [[ -z "$go_ann" || -z "$ts_ann" ]]; then
  die "could not read history scoring-mode annotation key"
elif [[ "$go_ann" != "$ts_ann" ]]; then
  die "historyScoringModeAnn ($go_ann) != HISTORY_SCORING_MODE_ANN ($ts_ann)"
fi

# Batch apply annotation + cap.
go_batch_ann=$(grep -E 'batchApplyAnnotation\s*=' "$BATCH" | head -1 | sed -E 's/.*"([^"]+)".*/\1/')
ts_batch_ann=$(grep -E 'BATCH_APPLY_ANNOTATION\s*=' "$PATCHES" | head -1 | sed -E "s/.*'([^']+)'.*/\1/")
if [[ -z "$go_batch_ann" || -z "$ts_batch_ann" ]]; then
  die "could not read batch-apply annotation key"
elif [[ "$go_batch_ann" != "$ts_batch_ann" ]]; then
  die "batchApplyAnnotation ($go_batch_ann) != BATCH_APPLY_ANNOTATION ($ts_batch_ann)"
fi

go_batch_max=$(grep -E 'batchMaxRemediations\s*=' "$BATCH" | head -1 | sed -E 's/.*=\s*([0-9]+).*/\1/')
ts_batch_max=$(grep -E 'batchApplyMaxNames\s*=' "$PATCHES" | head -1 | sed -E 's/.*=\s*([0-9]+).*/\1/')
if [[ -z "$go_batch_max" || -z "$ts_batch_max" ]]; then
  die "could not read batch max remediations"
elif [[ "$go_batch_max" != "$ts_batch_max" ]]; then
  die "batchMaxRemediations ($go_batch_max) != batchApplyMaxNames ($ts_batch_max)"
fi

if [[ "$fail" -ne 0 ]]; then
  exit 1
fi
echo "verify-product-lockstep: ok"
