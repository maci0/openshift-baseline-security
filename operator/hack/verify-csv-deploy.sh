#!/usr/bin/env bash
# Fail if the CSV install.spec.deployments[].spec drifted from the kustomize base.
#
# The CSV is hand-maintained, so install.spec.deployments[0].spec is a second
# copy of the Deployment spec in config/manager/manager.yaml. It is nested
# inside the CSV, not a standalone bundle manifest, so verify-bundle-static.sh
# cannot reach it, and no other target compares the two. Without this a change
# to the base (a probe, a resource limit, a volume) ships to `make deploy` and
# silently not to an OLM install.
#
# Four divergences are intentional and normalized away:
#   * the image tag. The base pins :latest for local `make deploy`; the CSV
#     pins the release VERSION. Both refs must otherwise be the same repo.
#   * imagePullPolicy: Always. Base-only: :latest needs Always to pick up a
#     rebuild, the CSV's versioned tag wants the IfNotPresent default.
#   * the app.kubernetes.io/version pod label. CSV-only release stamp.
#   * quoting of the --drop=ALL log level (base writes drop: ["ALL"], the CSV
#     drop: [ALL]). Semantically the same, so the gsub folds them.
#
# Everything else must match line for line.
#
# The last check is the one pair of values a line-for-line diff cannot relate:
# GOMEMLIMIT and the container memory limit. They are independent knobs that
# only work together (GOMEMLIMIT above the cgroup cap makes the GC stop
# collecting early and the pod is OOMKilled with a heap that never grew), and
# nothing else in the tree ties them, so a memory-limit change alone would ship
# a pod that the limit outruns. The diff keeps the two files in sync; this keeps
# the value inside the limit.
#
# Run from operator/ (make verify-csv-deploy) or with REPO_ROOT set.
set -euo pipefail

# Every diagnostic is prefixed with the script name, as in the other hack/ scripts.
prog="$(basename "$0")"

usage() {
	cat <<EOF
Usage: ${prog}

Fail if the CSV install.spec.deployments[].spec drifted from
config/manager/manager.yaml. Run from operator/ (make verify-csv-deploy) or
with REPO_ROOT set.

Options:
  -h, --help   print this usage and exit 0
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
OP="${ROOT}/operator"
BASE="${OP}/config/manager/manager.yaml"
CSV="${OP}/bundle/manifests/baseline-security-operator.clusterserviceversion.yaml"

for f in "$BASE" "$CSV"; do
	[ -f "$f" ] || { echo "${prog}: missing $f" >&2; exit 1; }
done

# csv-spec normalizes install.spec.deployments[0].spec out of the CSV.
# base-spec normalizes .spec out of the Deployment document in the base.
extract_csv_spec() {
	awk '
		/^[[:space:]]*deployments:[[:space:]]*$/ { in_dep = 1 }
		in_dep && /^[[:space:]]+spec:[[:space:]]*$/ { ind = length($0) - 5; in_spec = 1; next }
		in_spec && /^[[:space:]]*$/ { print; next }
		in_spec && (match($0, /[^ ]/) - 1) < ind { exit }
		in_spec { print }
	' "$CSV"
}

extract_base_spec() {
	awk '
		/^kind:[[:space:]]*Deployment[[:space:]]*$/ { in_dep = 1; next }
		in_dep && /^[[:space:]]*spec:[[:space:]]*$/ { ind = length($0) - 5; in_spec = 1; next }
		in_spec && /^[[:space:]]*$/ { print; next }
		in_spec && (match($0, /[^ ]/) - 1) < ind { exit }
		in_spec { print }
	' "$BASE"
}

normalize() {
	awk '
		# strip the documented, intentional divergences
		/^[[:space:]]*imagePullPolicy:[[:space:]]*/ { next }
		/^[[:space:]]*app\.kubernetes\.io\/version:[[:space:]]*/ { next }
		# the tag is a release value, the repo is not
		{
			gsub(/quay\.io\/openshift-baseline-security\/baseline-security-operator:[^[:space:]]+/, "<IMG>/operator:<VERSION>")
			gsub(/quay\.io\/openshift-baseline-security\/baseline-security-console-plugin:[^[:space:]]+/, "<IMG>/console-plugin:<VERSION>")
		}
		{ print }
	' |
		awk '
		/^[[:space:]]*#/ { next }
		/^[[:space:]]*$/ { next }
		/^[[:space:]]*namespace:[[:space:]]*/ { next }
		{ sub(/[[:space:]]+$/, "") }
		{ gsub(/drop: \["ALL"\]/, "drop: [ALL]") }
		{ lines[n++] = $0 }
		END {
			min = -1
			for (i = 0; i < n; i++)
				if (match(lines[i], /[^ ]/)) {
					if (min < 0 || RSTART - 1 < min) min = RSTART - 1
				}
			if (min < 0) exit 1
			for (i = 0; i < n; i++) print substr(lines[i], min + 1)
		}'
}

tmp="$(mktemp -d)"
trap 'rm -rf "$tmp"' EXIT

extract_csv_spec | normalize >"$tmp/csv"
extract_base_spec | normalize >"$tmp/base"

if ! diff -u "$tmp/base" "$tmp/csv"; then
	echo "" >&2
	echo "${prog}: the CSV deployment spec drifted from config/manager/manager.yaml." >&2
	echo "Apply the same change to both. The image tag, imagePullPolicy, and the" >&2
	echo "app.kubernetes.io/version label are normalized away; anything else" >&2
	echo "printed above is real drift." >&2
	exit 1
fi

# quantity_bytes prints a quantity as a whole number of bytes, or nothing when
# the spelling is one it does not know. Refusing the unknown is the point: a
# silent "treat it as 0" would let this check pass on a value it never read.
# The binary and decimal SI suffixes take an optional trailing B, which is how
# both spell GOMEMLIMIT (440MiB) and the manifests' limit (512Mi). The metric
# suffixes (m, u, n) are not accepted: they have no meaning for a heap cap.
#
# %.0f, not %d: %d converts through the awk implementation's C int, and the
# awks this gate runs under disagree above 2^31-1. mawk (busybox awk) prints
# "-2147483648" for 4Gi, so a GOMEMLIMIT of 4Gi against an 8Gi limit compared as
# negative and the check passed on a value it never read. %.0f formats the
# double, exact for every whole-byte quantity up to 2^53.
quantity_bytes() {
	awk -v q="$1" '
		BEGIN {
			if (q !~ /^[0-9]+(\.[0-9]+)?(k|M|G|T|Ki|Mi|Gi|Ti)?[Bb]?$/) exit 1
			num = q
			sub(/[A-Za-z]+$/, "", num)
			unit = substr(q, length(num) + 1)
			sub(/[Bb]$/, "", unit)
			mul = (unit == "" ? 1 :
				unit == "k" ? 1000 :
				unit == "M" ? 1000000 :
				unit == "G" ? 1000000000 :
				unit == "T" ? 1000000000000 :
				unit == "Ki" ? 1024 :
				unit == "Mi" ? 1048576 :
				unit == "Gi" ? 1073741824 :
				unit == "Ti" ? 1099511627776 : 0)
			printf "%.0f\n", num * mul
		}'
}

# base_gomemlimit is the GOMEMLIMIT env value from the base Deployment.
base_gomemlimit() {
	awk '
		/^[[:space:]]*-?[[:space:]]*name:[[:space:]]*GOMEMLIMIT[[:space:]]*$/ { want = 1; next }
		want && match($0, /value:[[:space:]]*/) {
			v = substr($0, RSTART + RLENGTH)
			sub(/[[:space:]]+$/, "", v)
			print v
			exit
		}
	' "$BASE"
}

# base_memory_limit is the first memory entry under a resources.limits block,
# so it is the cgroup cap and not the (lower) request.
base_memory_limit() {
	awk '
		/^[[:space:]]*limits:[[:space:]]*$/ { ind = match($0, /[^ ]/) - 1; in_limits = 1; next }
		in_limits && match($0, /[^ ]/) && (RSTART - 1) <= ind { in_limits = 0 }
		in_limits && /^[[:space:]]*memory:[[:space:]]*/ {
			v = $0
			sub(/^[[:space:]]*memory:[[:space:]]*/, "", v)
			sub(/[[:space:]]+$/, "", v)
			print v
			exit
		}
	' "$BASE"
}

gomemlimit="$(base_gomemlimit)"
memlimit="$(base_memory_limit)"
if [ -z "$gomemlimit" ]; then
	echo "${prog}: config/manager/manager.yaml sets no GOMEMLIMIT on the manager container." >&2
	echo "Without it the Go heap is free to grow past limits.memory and the pod is" >&2
	echo "OOMKilled on a large cluster instead of the GC collecting harder." >&2
	exit 1
fi
if [ -z "$memlimit" ]; then
	echo "${prog}: could not read limits.memory for the manager container in config/manager/manager.yaml." >&2
	exit 1
fi
# `|| true`: under set -e an unparsable quantity would otherwise end the script
# here, before it can say which value it could not read.
gomemlimit_bytes="$(quantity_bytes "$gomemlimit" || true)"
memlimit_bytes="$(quantity_bytes "$memlimit" || true)"
if [ -z "$gomemlimit_bytes" ] || [ -z "$memlimit_bytes" ]; then
	echo "${prog}: GOMEMLIMIT=${gomemlimit} / limits.memory=${memlimit} is not a quantity this check can compare." >&2
	echo "Fix the spelling in config/manager/manager.yaml (and the CSV) so the two" >&2
	echo "stay comparable here." >&2
	exit 1
fi
if [ "$gomemlimit_bytes" -gt "$memlimit_bytes" ]; then
	echo "${prog}: GOMEMLIMIT=${gomemlimit} is above the container memory limit ${memlimit}." >&2
	echo "The GC would stop collecting before the cgroup cap, so the pod is" >&2
	echo "OOMKilled rather than collecting. Keep GOMEMLIMIT under the limit" >&2
	echo "(the current pair is ~85%)." >&2
	exit 1
fi
