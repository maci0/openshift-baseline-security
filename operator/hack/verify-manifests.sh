#!/usr/bin/env bash
# Build every kustomize entry point and check the rendered objects against the
# sources that produced them.
#
# The other verify-* targets compare the hand-copied bundle manifests with their
# config/ sources. None did the other half: the kustomize tree itself was only
# ever assembled by `make deploy`, on a developer machine, after the fact. A
# kustomization.yaml naming a file that does not exist, a base moved out of the
# tree, or a manifest written and never listed (so it ships to nobody) applied
# cleanly on a laptop and broke the install.
#
# Three checks, against the render of config/default:
#   1. every entry point builds (crd, rbac, manager, prometheus, default);
#   2. every manifest under config/ contributes an object to that render, so a
#      file no kustomization lists is caught;
#   3. the tree-local cross-resource references resolve: RoleBinding roleRef to
#      a declared Role/ClusterRole, Service selector to a pod template,
#      ServiceMonitor selector and scraped port name to a Service, and the
#      Secret/ConfigMap a ServiceMonitor names to declared objects. Those are
#      the references that fail at scrape time with no apply-time error.
#   4. the metrics port and the probe port each name one number across every
#      place that spells them (the flag argument, the containerPort, the
#      Service port and targetPort, the NetworkPolicy ingress port, and every
#      probe). A mismatch still applies and still leaves the manager Ready.
#
# Run from operator/ (make verify-manifests) or with REPO_ROOT set.
set -euo pipefail

# Every diagnostic is prefixed with the script name, as in the other hack/ scripts.
prog="$(basename "$0")"

usage() {
	cat <<EOF
Usage: ${prog}

Build the kustomize tree and check the render against its sources.
Run from operator/ (make verify-manifests) or with REPO_ROOT set.
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
CONFIG="${OP}/config"

# Entry points, in the order apply would use them. config/default is the one
# `make deploy` builds and the one the reference checks read.
ENTRY_POINTS="crd rbac manager prometheus default"

# Renderer: an explicit override first, then whichever kustomize the host
# already has. kubectl and oc embed kustomize, so a CI runner needs nothing
# installed beyond the kubectl it ships with.
if [ -z "${KUSTOMIZE:-}" ]; then
	if command -v kustomize >/dev/null 2>&1; then
		KUSTOMIZE="kustomize build"
	elif command -v kubectl >/dev/null 2>&1; then
		KUSTOMIZE="kubectl kustomize"
	elif command -v oc >/dev/null 2>&1; then
		KUSTOMIZE="oc kustomize"
	else
		echo "${prog}: no kustomize, kubectl or oc on PATH" >&2
		exit 1
	fi
fi

work="$(mktemp -d)"
trap 'rm -rf "$work"' EXIT

# Indent-tracking path parser over a multi-document YAML file. It prints one
# `OBJ <kind> <namespace|-> <name>` line per object and, with -v resolve=1,
# resolves the tree-local references in END.
cat >"$work/facts.awk" <<'AWK'
function path_of(	s, i) {
	s = ""
	for (i = 1; i <= depth; i++) {
		if (key[i] == "") continue # anonymous sequence-item segment
		s = s (s == "" ? "" : ".") key[i]
	}
	return s
}

# A sequence item (`- key: value`) opens an anonymous mapping two columns in,
# so its siblings stay inside the item and the path skips the empty segment.
function pop_to(indent) {
	while (depth > 0 && ind[depth] >= indent) depth--
}

function push(indent, k) {
	depth++
	ind[depth] = indent
	key[depth] = k
}

function fail(msg) {
	print prog ": " msg > "/dev/stderr"
	bad = 1
}

function flush(	p, i) {
	if (kind == "") return

	objkey = kind "/" (ns == "" ? "-" : ns) "/" name
	objlabel = kind " " (ns == "" ? name : ns "/" name)
	print "OBJ " objkey

	if (kind == "Role" || kind == "ClusterRole") {
		role[name] = 1
	} else if (kind == "Secret") {
		secret[name] = 1
	} else if (kind == "ConfigMap") {
		configmap[name] = 1
	} else if (kind == "Service") {
		label_of[objkey] = objlabel
		for (p in svc_label) svc_pair[objkey, p] = 1
		for (p in sel) svc_sel[objkey, p] = 1
	} else if (kind == "ServiceMonitor") {
		label_of[objkey] = objlabel
		for (p in sm_sel) sm_want[p] = 1
		for (p in ref_secret) sm_secret[p] = 1
		for (p in ref_configmap) sm_configmap[p] = 1
	} else if (kind == "Deployment" || kind == "StatefulSet" ||
		kind == "DaemonSet" || kind == "ReplicaSet" || kind == "Pod") {
		# The pod template labels, plus the Deployment selector: the API
		# server requires the two to agree, so a Service matching either is
		# matching the pods.
		for (p in pod_label) pod_pool[p] = 1
	}

	kind = ""; name = ""; ns = ""
	for (p in svc_label) delete svc_label[p]
	for (p in sel) delete sel[p]
	for (p in sm_sel) delete sm_sel[p]
	for (p in ref_secret) delete ref_secret[p]
	for (p in ref_configmap) delete ref_configmap[p]
	for (p in pod_label) delete pod_label[p]
	depth = 0
}

{
	line = $0
	if (line == "---") { flush(); next }
	if (line ~ /^[ ]*$/ || line ~ /^[ ]*#/) next

	indent = match(line, /[^ ]/) - 1
	body = substr(line, indent + 1)
	if (substr(body, 1, 2) == "- ") {
		# kustomize writes sequence items at their key's own indent, so the
		# item's parent is a key at this very indent: keep it on the stack or
		# every value in the item would read as a top-level one.
		savek = ""
		for (i = depth; i >= 1; i--) {
			if (ind[i] < indent) break
			if (ind[i] == indent && key[i] != "") { savek = key[i]; break }
		}
		pop_to(indent)
		if (savek != "") push(indent, savek)
		push(indent, "")
		indent += 2
		body = substr(body, 3)
	}

	p = index(body, ":")
	if (p == 0) next
	kv = substr(body, 1, p - 1)
	v = substr(body, p + 1)
	sub(/^ /, "", v)

	# Pop the siblings this line replaces, then read the parent path, which is
	# what identifies the block the value sits in.
	pop_to(indent)
	pfx = path_of()
	push(indent, kv)
	# A key with no value on the line opens a block; it carries no value.
	if (v == "" || substr(v, 1, 1) == "#") next

	if (pfx == "" && kv == "kind") {
		kind = v
	} else if (pfx == "metadata" && kv == "name") {
		name = v
	} else if (pfx == "metadata" && kv == "namespace") {
		ns = v
	} else if (pfx == "roleRef" && kv == "name") {
		role_ref[v] = 1
	} else if (pfx == "metadata.labels") {
		svc_label[kv "=" v] = 1
	} else if (pfx == "spec.selector") {
		if (kind == "Service") sel[kv "=" v] = 1
	} else if (pfx == "spec.selector.matchLabels") {
		if (kind == "ServiceMonitor") sm_sel[kv "=" v] = 1
		else pod_label[kv "=" v] = 1
	} else if (pfx == "spec.template.metadata.labels") {
		pod_label[kv "=" v] = 1
	} else if (pfx "." kv ~ /\.authorization\.credentials\.name$/) {
		ref_secret[v] = 1
	} else if (pfx "." kv ~ /\.ca\.configMap\.name$/) {
		ref_configmap[v] = 1
	} else if (pfx ~ /\.ports$/ && kv == "name" && kind == "Service") {
		svc_portname[v] = 1
	} else if (pfx ~ /\.ports$/ && kv == "port" && kind == "Service") {
		svc_port[v] = 1
	} else if (pfx ~ /\.ports$/ && kv == "targetPort" && kind == "Service") {
		svc_target[v] = 1
	} else if (pfx ~ /\.endpoints$/ && kv == "port" && kind == "ServiceMonitor") {
		sm_portname[v] = 1
	} else if (kv == "containerPort") {
		container_port[v] = 1
	} else if (pfx ~ /\.httpGet$/ && kv == "port") {
		probe_port[v] = 1
	} else if (pfx ~ /\.ingress\..*\.ports$/ && kv == "port" && kind == "NetworkPolicy") {
		np_port[v] = 1
	} else if (pfx ~ /\.args$/ && kv == "--metrics-bind-address=") {
		# An args item is a bare scalar, so this parser splits it at the first
		# colon: kv is "--<flag>=" and v is the port. A value that is not a port
		# (":0" to disable, or a host) leaves metrics_arg empty and the check
		# below reports it rather than passing on it.
		metrics_arg = (v ~ /^[0-9]+$/) ? v : ""
	} else if (pfx ~ /\.args$/ && kv == "--health-probe-bind-address=") {
		probe_arg = (v ~ /^[0-9]+$/) ? v : ""
	}
}

END {
	flush()

	# Only the full render carries the whole tree; a single source file cannot
	# resolve a reference that lives in its sibling.
	if (resolve != 1) exit bad

	for (r in role_ref) {
		if (!(r in role)) fail("roleRef " r " names a Role/ClusterRole the tree does not declare")
	}
	for (k in svc_sel) {
		split(k, parts, SUBSEP)
		svc = parts[1]
		pair = parts[2]
		if (!(pair in pod_pool))
			fail(label_of[svc] " selector " pair " matches no pod template label")
	}
	for (k in sm_want) {
		pair = k
		hit = 0
		for (s in svc_pair) {
			split(s, parts, SUBSEP)
			if (parts[2] == pair) hit++
		}
		if (!hit) fail("a ServiceMonitor selector (" pair ") matches no Service label")
	}
	for (p in sm_secret) {
		if (!(p in secret)) fail("the ServiceMonitor bearer token Secret " p " is not declared in the tree")
	}
	for (p in sm_configmap) {
		if (!(p in configmap)) fail("the ServiceMonitor serving CA ConfigMap " p " is not declared in the tree")
	}
	# A ServiceMonitor endpoint scrapes the Service port whose *name* it names,
	# not the one whose number matches. A Service port renamed without the
	# ServiceMonitor following leaves the selector matching (the labels are
	# untouched) and every other check green, while the endpoint resolves to no
	# port and the scrape silently finds zero targets.
	for (p in sm_portname) {
		if (!(p in svc_portname))
			fail("a ServiceMonitor endpoint scrapes the Service port named " p ", which no Service declares")
	}

	# The metrics port and the probe port are each one setting spelled in
	# several places: the flag argument, the containerPort, the Service port
	# and targetPort, the NetworkPolicy ingress port, and every probe. A change
	# to one of them still renders, still applies, and still leaves the manager
	# Ready, so nothing fails until the metric is silently unscrapeable or the
	# probes hit a closed port. There is one metrics Service, one NetworkPolicy
	# and one manager Deployment in this tree, so the sets are unambiguous.
	nm = 0
	for (k in svc_port)     { metrics_val[++nm] = k; metrics_src[nm] = "the metrics Service port" }
	for (k in svc_target)   { metrics_val[++nm] = k; metrics_src[nm] = "the metrics Service targetPort" }
	for (k in container_port) { metrics_val[++nm] = k; metrics_src[nm] = "a containerPort" }
	for (k in np_port)      { metrics_val[++nm] = k; metrics_src[nm] = "a NetworkPolicy ingress port" }
	if (metrics_arg == "") {
		fail("no --metrics-bind-address=:<port> argument in any container args")
	} else {
		metrics_val[++nm] = metrics_arg
		metrics_src[nm] = "the --metrics-bind-address argument"
	}
	for (i = 2; i <= nm; i++) {
		if (metrics_val[i] != metrics_val[1]) {
			fail(metrics_src[i] " is " metrics_val[i] " but " metrics_src[1] " is " metrics_val[1] \
				", so the metrics endpoint moves where nothing reaches it")
		}
	}

	np = 0
	for (k in probe_port) { probe_val[++np] = k; probe_src[np] = "a probe httpGet port" }
	if (probe_arg == "") {
		fail("no --health-probe-bind-address=:<port> argument in any container args")
	} else {
		probe_val[++np] = probe_arg
		probe_src[np] = "the --health-probe-bind-address argument"
	}
	for (i = 2; i <= np; i++) {
		if (probe_val[i] != probe_val[1]) {
			fail(probe_src[i] " is " probe_val[i] " but " probe_src[1] " is " probe_val[1] \
				", so the kubelet probes a port the manager does not listen on")
		}
	}
	if (bad) exit 1
}
AWK

for e in $ENTRY_POINTS; do
	[ -d "$CONFIG/$e" ] || { echo "${prog}: missing entry point config/$e" >&2; exit 1; }
	# shellcheck disable=SC2086 # KUSTOMIZE is a command plus a fixed subcommand.
	if ! $KUSTOMIZE "$CONFIG/$e" >"$work/$e.yaml"; then
		echo "${prog}: kustomize build config/$e failed" >&2
		exit 1
	fi
done

awk -v prog="$prog" -v resolve=1 -f "$work/facts.awk" "$work/default.yaml" >"$work/render-objs.txt"

# Every hand-maintained manifest must reach the config/default render. A file no
# kustomization lists renders nowhere and ships to nobody; the guardrails that
# compare bundle/ against config/ compare content, never reachability, so they
# cannot see it.
for src in "$CONFIG"/crd/bases/*.yaml "$CONFIG"/manager/*.yaml \
	"$CONFIG"/prometheus/*.yaml "$CONFIG"/rbac/*.yaml; do
	[ -e "$src" ] || continue
	if [ "$(basename "$src")" = kustomization.yaml ]; then continue; fi
	while read -r _ objkey; do
		grep -qxF "OBJ $objkey" "$work/render-objs.txt" || {
			echo "${prog}: ${src#"$OP"/} declares $objkey, which no kustomization includes" >&2
			exit 1
		}
	done < <(awk -v prog="$prog" -f "$work/facts.awk" "$src")
done

echo "${prog}: $(grep -c '^OBJ ' "$work/render-objs.txt") objects rendered from config/default; every source included, every tree reference resolved"
