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
