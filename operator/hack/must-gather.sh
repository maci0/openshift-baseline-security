#!/usr/bin/env bash
# Collect baseline-security state for support/debugging.
# Usage: hack/must-gather.sh [output-dir]   (defaults to ./must-gather)
#        hack/must-gather.sh --self-test    (redaction unit test; no cluster)
#        hack/must-gather.sh --help
set -euo pipefail

# Every diagnostic is prefixed with the script name, as in the other hack/ scripts.
prog="$(basename "$0")"

# The redaction program lives beside this script, so the dumps are rewritten by
# the same rules whether they come from the collector or from --self-test.
script_dir="$(cd "$(dirname "$0")" && pwd)"
redactor="$script_dir/redact-yaml.awk"

# Redact a YAML dump in place, under one of redact-yaml.awk's policies. awk
# reads the dump by path, so the unredacted copy only ever exists in a temp file
# this function owns. GNU sed -i is not an option: BSD sed (macOS) treats the
# next argument as a required backup suffix.
#
# The rewrite runs in a subshell that owns the temp file, whose EXIT trap
# removes it on every exit. Hand-written rm calls covered only the two failure
# paths and the success path, so an interrupted must-gather left the
# pre-redaction copy behind in TMPDIR, still carrying the identities these
# redactions exist to strip. The signal traps turn a signal into an ordinary
# exit so the EXIT trap still runs.
_redact_yaml_dump() {
  local f="$1" policy="$2"
  [ -s "$f" ] || return 0
  # kubectl emits each mapping key on its own line, and a value containing a
  # newline (allowed: the CRD caps length only) as a literal/folded block, with
  # the text on the following more-indented lines. Deleting the key line alone
  # would leave that text in the dump, so a dropped key also swallows its
  # continuation. last-applied-configuration is a single JSON blob per key, so
  # its continuation is kept and the JSON substitutions redact it instead.
  #
  # The signal traps turn a signal into an ordinary exit so the EXIT trap
  # still runs.
  (
    local tmp
    tmp="$(mktemp)"
    trap 'rm -f -- "$tmp"' EXIT
    trap 'exit 129' HUP
    trap 'exit 130' INT
    trap 'exit 143' TERM
    awk -v policy="$policy" -f "$redactor" "$f" > "$tmp" || exit 1
    # The subshell's exit status is the function's return value, so a failed
    # cat fails the caller under set -e.
    cat "$tmp" > "$f"
  )
}

# spec.waivers[].requestedBy and approvedBy identify cluster users (audit
# attribution), which must not leave the cluster in a support archive. Waiver
# name and reason stay: they are required to debug scoring.
redact_clusterbaseline_dump() {
  _redact_yaml_dump "$1" clusterbaseline
}

# Scanner output from the Compliance Operator, redacted out of the compliance
# dump. The account-related CIS and CCSR rules return what they found verbatim:
# /etc/passwd and /etc/shadow listings, `getent` output, audit entries naming a
# user. Those names and hashes are personal data, and this archive is built to
# be attached to a support case, so that text must not leave the cluster the
# way waiver attribution must not. Control identity, the compliant flag, the
# scan verdict, and the timestamps stay, which is what a scan-failure triage
# reads. Nothing in this repo consumes the dropped fields: the console plugin
# renders the ClusterBaseline status and links out to OpenShift for check
# detail, so the redaction costs the support bundle nothing.
redact_compliance_dump() {
  _redact_yaml_dump "$1" compliance
}

# True when a status.relatedObjects entry names a kind the operator actually
# writes, as the "<resource>.<group>" pair relatedObjectsFromSuites produces.
#
# The character filter in collect_related_objects is a shell-safety guard, not a
# content guard: it lets any well-shaped resource through, so a hand-edited,
# corrupt, or etcd-restored status naming `secrets.<group>` would dump that
# Secret's data (metrics TLS private key, scraper SA token) into a support
# archive, which is exactly what the secrets rule further down refuses and what
# this script's own --help promises to omit. A support attachment is a copy the
# operator can no longer redact, so the kind is pinned to the six the reconciler
# can produce rather than trusted from the CR.
related_object_allowed() {
  case "$1:$2" in
    scansettings:compliance.openshift.io) return 0 ;;
    scansettingbindings:compliance.openshift.io) return 0 ;;
    deployments:apps) return 0 ;;
    services:) return 0 ;; # core group: the jsonpath emits a trailing dot
    poddisruptionbudgets:policy) return 0 ;;
    consoleplugins:console.openshift.io) return 0 ;;
  esac
  return 1
}

# Concatenate the YAML of every object in status.relatedObjects into one file,
# so the per-object `oc get` needs `>>`. Truncate first: the output dir is
# reused across runs, and appending would duplicate every document on a second
# run, and leave a prior run's objects behind when the CR is gone and the loop
# never executes.
collect_related_objects() {
  local out="$1"
  : > "$out"
  # relatedObjects declared by the CR (group/resource/name[/namespace]).
  # Only DNS-1123-shaped tokens are passed to oc (status is operator-written, but
  # a hand-edited or corrupted relatedObjects list must not become shell noise).
  # Reject leading dashes (oc flag injection) and '/' in resource (type/name
  # shorthand). Tokens: alnum / dash / dot only.
  { oc get clusterbaseline cluster -o jsonpath='{range .status.relatedObjects[*]}{.resource}.{.group} {.name} {.namespace}{"\n"}{end}' 2>/dev/null || true; } \
    | while read -r res name ns; do
        [ -z "$res" ] && continue
        case "$res" in -*|*[!a-z0-9.-]*) continue ;; esac
        # The jsonpath always joins the two fields with a dot, so a resource
        # without one is malformed rather than core-group: skip it instead of
        # reading it as a group-less name.
        case "$res" in
          *.*) kind="${res%%.*}"; kind_group="${res#*.}" ;;
          *) continue ;;
        esac
        related_object_allowed "$kind" "$kind_group" || continue
        case "$name" in ''|-*|*[!a-z0-9.-]*) continue ;; esac
        if [ -n "$ns" ]; then
          case "$ns" in -*|*[!a-z0-9.-]*) continue ;; esac
          oc -n "$ns" get "$res" "$name" -o yaml >> "$out" 2>/dev/null || true
        else
          oc get "$res" "$name" -o yaml >> "$out" 2>/dev/null || true
        fi
        echo '---' >> "$out"
      done
}

# Count of best-effort dumps that failed in the current run, so an empty file is
# not mistaken for a successful collection (e.g. CR missing, RBAC, wrong
# namespace). Soft-fail targets (plugin absent, no previous logs) stay as
# `|| true` without counting.
failures=0
warn_fail() {
  echo "warning: failed to collect $1" >&2
  failures=$((failures + 1))
}

# Every `oc` call that writes into the output directory, in one place so the
# re-run property is a property of the whole set and not of whichever collector
# happens to be read first. Each one redirects straight into $out/<name>, which
# is what makes a second run into the same directory converge: the shell
# truncates the target before oc runs, so a target that no longer exists (CR
# deleted, plugin undeployed, log rotated away) ends up empty rather than still
# holding the previous run's object, and a target that still exists is
# overwritten with the same content. A collector that appends must truncate
# first (collect_related_objects); the truncation is what stops a re-run from
# duplicating every document.
#
# Split out of the script body so --self-test can drive all of them against a
# stub oc. It used to be inline, which left "every collector must converge"
# asserted for one collector and assumed for the rest.
collect_all() {
  local out="$1"

  # ClusterBaseline status (score, conditions, remediationBatch, relatedObjects).
  # Strip waiver attribution after a successful get so names do not leave the
  # cluster; keep the dump even if redaction is a no-op (no waivers present).
  if oc get clusterbaseline cluster -o yaml > "$out/clusterbaseline.yaml" 2>/dev/null; then
    redact_clusterbaseline_dump "$out/clusterbaseline.yaml"
  else
    warn_fail clusterbaseline.yaml
  fi
  oc get clusterbaseline cluster -o jsonpath='{range .status.conditions[*]}{.type}={.status} reason={.reason} msg={.message}{"\n"}{end}' \
    > "$out/clusterbaseline-conditions.txt" 2>/dev/null \
    || warn_fail clusterbaseline-conditions.txt

  # Operator namespace: workloads, monitoring CRs, recent events.
  # Never dump Secret objects: metrics TLS keys and scraper SA tokens would land
  # on disk (and in support attachments). Names/types only for triage.
  # Include PDBs (not in `all`): operator + plugin minAvailable during drains.
  oc -n openshift-baseline-security get all,configmap,servicemonitor,prometheusrule,poddisruptionbudget -o yaml \
    > "$out/operator-namespace.yaml" 2>/dev/null \
    || warn_fail operator-namespace.yaml
  # The console dashboard ConfigMap lives in openshift-config-managed, outside the
  # operator namespace dumped above. Collect it so "dashboard missing from the
  # console" can be triaged (never created vs wrong labels vs user-deleted).
  oc -n openshift-config-managed get configmap baseline-security-compliance-dashboard -o yaml \
    > "$out/dashboard-configmap.yaml" 2>/dev/null || true
  oc -n openshift-baseline-security get secrets \
    -o custom-columns=NAME:.metadata.name,TYPE:.type,AGE:.metadata.creationTimestamp \
    > "$out/operator-secrets.txt" 2>/dev/null \
    || warn_fail operator-secrets.txt
  oc -n openshift-baseline-security get events --sort-by='.lastTimestamp' \
    > "$out/operator-events.txt" 2>/dev/null \
    || warn_fail operator-events.txt
  # All replicas + previous container (crash-loop) when present.
  oc -n openshift-baseline-security logs deploy/baseline-security-operator --all-containers --tail=-1 \
    > "$out/operator.log" 2>/dev/null \
    || warn_fail operator.log
  # Previous logs are often absent (no restart); do not count as a failure.
  oc -n openshift-baseline-security logs deploy/baseline-security-operator --all-containers --previous --tail=-1 \
    > "$out/operator-previous.log" 2>/dev/null || true
  oc -n openshift-baseline-security describe deploy/baseline-security-operator \
    > "$out/operator-deploy-describe.txt" 2>/dev/null \
    || warn_fail operator-deploy-describe.txt

  # Console plugin Deployment (same namespace): nginx access/error streams and
  # rollout state. Absent when ConsolePluginReady is ImageMissing/Disabled.
  # Soft-fail: plugin may not be deployed.
  oc -n openshift-baseline-security logs deploy/baseline-security-console-plugin --all-containers --tail=-1 \
    > "$out/console-plugin.log" 2>/dev/null || true
  oc -n openshift-baseline-security logs deploy/baseline-security-console-plugin --all-containers --previous --tail=-1 \
    > "$out/console-plugin-previous.log" 2>/dev/null || true
  oc -n openshift-baseline-security describe deploy/baseline-security-console-plugin \
    > "$out/console-plugin-deploy-describe.txt" 2>/dev/null || true

  # Compliance Operator objects (scans, results, remediations). The scanner
  # output they carry is redacted below: account-related rules return user
  # listings verbatim, and this archive is meant to be attached to a support case.
  if oc -n openshift-compliance get scansettings,scansettingbindings,tailoredprofiles,compliancesuites,compliancescans,compliancecheckresults,complianceremediations -o yaml \
    > "$out/compliance.yaml" 2>/dev/null; then
    redact_compliance_dump "$out/compliance.yaml"
  else
    warn_fail compliance.yaml
  fi
  oc -n openshift-compliance get events --sort-by='.lastTimestamp' \
    > "$out/compliance-events.txt" 2>/dev/null \
    || warn_fail compliance-events.txt

  # MachineConfigPools: pause state is critical for RemediationBatchStuck.
  oc get mcp -o yaml > "$out/machineconfigpools.yaml" 2>/dev/null \
    || warn_fail machineconfigpools.yaml
  oc get mcp -o custom-columns=NAME:.metadata.name,PAUSED:.spec.paused,UPDATED:.status.updatedMachineCount,UPDATING:.status.updatingMachineCount,DEGRADED:.status.degradedMachineCount \
    > "$out/machineconfigpools-pause.txt" 2>/dev/null \
    || warn_fail machineconfigpools-pause.txt

  # Soft-fail: Console capability may be disabled.
  oc get consoleplugin baseline-security-console-plugin -o yaml > "$out/consoleplugin.yaml" 2>/dev/null || true

  collect_related_objects "$out/related-objects.yaml"
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
    head -n 1 "$out" | grep -q '^apiVersion:' || {
      echo "FAIL: first line of the dump was rewritten" >&2
      cat "$out" >&2
      exit 1
    }
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

    # Multi-line attribution: the CRD caps length only, so kubectl emits a
    # value with a newline as a literal block and the name lands on the
    # continuation lines. Deleting the key line alone would keep it.
    block="$work/block.yaml"
    cat > "$block" <<'EOF'
spec:
  waivers:
    - name: chk1
      reason: accepted risk
      requestedBy: |-
        alice
        ops
      approvedBy: >-
        bob
        qa
    - name: chk2
      reason: still here
      requestedBy: carol
EOF
    redact_clusterbaseline_dump "$block"
    for identity in alice ops 'bob' qa carol requestedBy approvedBy; do
      if grep -q "$identity" "$block"; then
        echo "FAIL: block-scalar attribution survived: $identity" >&2
        cat "$block" >&2
        exit 1
      fi
    done
    grep -q 'name: chk1' "$block" || {
      echo "FAIL: block-scalar waiver name dropped" >&2
      cat "$block" >&2
      exit 1
    }
    grep -q 'reason: still here' "$block" || {
      echo "FAIL: block-scalar sibling waiver dropped" >&2
      cat "$block" >&2
      exit 1
    }
    # Scanner output in the compliance dump. An account rule returns the
    # /etc/shadow listing it checked, and that text must not reach a support
    # case. The per-check ComplianceCheckResult message carries the same text
    # under a different key, and the ComplianceScan verdict does not: it is the
    # OK/FAILED enum triage reads.
    compliance="$work/compliance.yaml"
    cat > "$compliance" <<'EOF'
apiVersion: compliance.openshift.io/v1alpha1
kind: ComplianceScan
metadata:
  name: worker-scan
  namespace: openshift-compliance
status:
  result: FAILED
  summary: |
    Scan Summary
    2 rules failed
  results:
    - id: x
      check: accounts_no_empty_password
      title: Ensure no accounts have empty passwords
      compliant: false
      details: |-
        root:$6$abcdef$hash:19000:0:99999:7:::
        jsmith:$6$ghijkl$hash:19000:0:99999:7:::
      summary: rule output naming jsmith
    - id: y
      check: audit_rules_enabled
      compliant: true
---
apiVersion: compliance.openshift.io/v1alpha1
kind: ComplianceCheckResult
metadata:
  name: accounts_no_empty_password-worker
status:
  result: |-
    jsmith has an empty password
  checkError: scan error naming jsmith
  timestamp: "2026-01-01T00:00:00Z"
EOF
    redact_compliance_dump "$compliance"
    # jsmith is the account name the scanner reported; the title is the
    # benchmark's own control text and stays.
    for leaked in jsmith 'Scan Summary' 'rule output naming' details summary checkError; do
      if grep -q "$leaked" "$compliance"; then
        echo "FAIL: scanner output survived redaction: $leaked" >&2
        cat "$compliance" >&2
        exit 1
      fi
    done
    for kept in 'kind: ComplianceScan' 'result: FAILED' 'check: accounts_no_empty_password' 'compliant: false' 'kind: ComplianceCheckResult' 'check: audit_rules_enabled'; do
      if ! grep -q "$kept" "$compliance"; then
        echo "FAIL: redaction dropped triage data: $kept" >&2
        cat "$compliance" >&2
        exit 1
      fi
    done
    grep -q 'timestamp: "2026-01-01T00:00:00Z"' "$compliance" || {
      echo "FAIL: redaction dropped the check timestamp" >&2
      cat "$compliance" >&2
      exit 1
    }
    # The redaction temp file holds the unredacted dump (it still carries the
    # requestedBy/approvedBy identities). Point TMPDIR at a directory we own so
    # "nothing left behind" is observable, and assert it on the success and the
    # failure path.
    tmproot="$work/tmpdir"
    mkdir -p "$tmproot"
    leakcheck="$work/leakcheck.yaml"
    cat > "$leakcheck" <<'EOF'
spec:
  waivers:
    - name: chk1
      reason: accepted risk
      requestedBy: alice
EOF
    TMPDIR="$tmproot" redact_clusterbaseline_dump "$leakcheck"
    if [ -n "$(ls -A "$tmproot")" ]; then
      echo "FAIL: redaction left a temp file behind: $(ls -A "$tmproot")" >&2
      exit 1
    fi
    if grep -q 'requestedBy' "$leakcheck"; then
      echo "FAIL: leakcheck fixture was not redacted" >&2
      exit 1
    fi
    # Same invariant on the failure path: awk cannot read a directory, so the
    # subshell exits nonzero and the temp still has to be gone. (A missing file
    # would short-circuit the `-s` guard and never create one.)
    if TMPDIR="$tmproot" redact_clusterbaseline_dump "$work" 2>/dev/null; then
      echo "FAIL: redaction of an unreadable path reported success" >&2
      exit 1
    fi
    if [ -n "$(ls -A "$tmproot")" ]; then
      echo "FAIL: failed redaction left a temp file behind: $(ls -A "$tmproot")" >&2
      exit 1
    fi

    echo "must-gather redaction self-test ok"
  )
  # Rerun property: the output dir is reused across runs, so every collector must
  # converge. A stub `oc` stands in for the cluster; two runs into the same
  # directory must produce byte-identical output, and a run that collects
  # nothing must clear the file rather than leave the prior run's objects.
  (
    work="$(mktemp -d)"
    trap 'rm -rf -- "$work"' EXIT
    oc() {
      case "$*" in
        *jsonpath*) printf 'scansettings.compliance.openshift.io scansettings compliance-operator\n' ;;
        *) printf 'kind: ScanSetting\nmetadata:\n  name: scansettings\n' ;;
      esac
    }
    rel="$work/related-objects.yaml"

    collect_related_objects "$rel"
    first="$(cat "$rel")"
    [ "$(grep -c '^kind: ScanSetting' "$rel")" -eq 1 ] || {
      echo "FAIL: first run did not collect exactly one object" >&2
      exit 1
    }

    collect_related_objects "$rel"
    [ "$(cat "$rel")" = "$first" ] || {
      echo "FAIL: second run into the same dir changed related-objects.yaml" >&2
      cat "$rel" >&2
      exit 1
    }

    # CR gone: jsonpath yields nothing, so the loop body never runs. The file
    # must end up empty, not still holding the objects from the run before.
    oc() {
      case "$*" in
        *jsonpath*) : ;;
        *) printf 'kind: ScanSetting\n' ;;
      esac
    }
    collect_related_objects "$rel"
    [ ! -s "$rel" ] || {
      echo "FAIL: stale related-objects.yaml survived a run that collected nothing" >&2
      exit 1
    }

    # Kind allowlist. The reconciler only ever advertises the six kinds
    # relatedObjectsFromSuites writes, so a status naming anything else is
    # hand-edited or restored and must not be collected: `secrets` would put a
    # TLS private key or SA token in a support archive. Each listed kind must
    # still be collected, or the allowlist has silently broken the gather.
    oc() {
      case "$*" in
        *jsonpath*) printf '%s\n' \
          'scansettings.compliance.openshift.io baseline openshift-compliance' \
          'scansettingbindings.compliance.openshift.io baseline-ocp4-cis openshift-compliance' \
          'deployments.apps baseline-security-console-plugin openshift-baseline-security' \
          'services. baseline-security-console-plugin openshift-baseline-security' \
          'poddisruptionbudgets.policy baseline-security-console-plugin openshift-baseline-security' \
          'consoleplugins.console.openshift.io baseline-security-console-plugin ' \
          'secrets. baseline-security-console-plugin-cert openshift-baseline-security' \
          'configmaps. any-configmap openshift-config-managed' \
          'scansettings.compliance.openshift.io --dash-flags openshift-compliance' \
          'deployments.apps' ;;
        *)
          # Echo the resource actually requested, so an assertion on the
          # collected file names the kind rather than a constant.
          req_kind='' req_name='' seen=0
          for arg in "$@"; do
            case "$seen" in
              0) [ "$arg" = "get" ] && seen=1 ;;
              1) req_kind="$arg"; seen=2 ;;
              2) req_name="$arg"; break ;;
            esac
          done
          printf 'resource: %s\nname: %s\n' "$req_kind" "$req_name"
          ;;
      esac
    }
    collect_related_objects "$rel"
    for kind in scansettings.compliance.openshift.io scansettingbindings.compliance.openshift.io \
                deployments.apps services. poddisruptionbudgets.policy consoleplugins.console.openshift.io; do
      grep -qx "resource: $kind" "$rel" || {
        echo "FAIL: allowlisted kind $kind was not collected" >&2
        cat "$rel" >&2
        exit 1
      }
    done
    for kind in secrets. configmaps.; do
      if grep -qx "resource: $kind" "$rel"; then
        echo "FAIL: non-allowlisted kind $kind reached related-objects.yaml" >&2
        cat "$rel" >&2
        exit 1
      fi
    done
    if grep -q 'name: --dash-flags' "$rel"; then
      echo "FAIL: a flag-shaped object name reached related-objects.yaml" >&2
      cat "$rel" >&2
      exit 1
    fi

    echo "must-gather rerun self-test ok"
  )
  # The same property for every other collector, not just related-objects.yaml.
  # They were inline in the script body and therefore untestable: "every
  # collector must converge" was asserted for one of them and assumed for the
  # rest. Two runs of the same cluster state into the same directory must land
  # byte-identical files, and a target that stops existing must end up empty
  # rather than still holding the previous run's dump, so a re-run after the CR
  # is deleted cannot ship the deleted object's YAML as if it were current.
  (
    work="$(mktemp -d)"
    trap 'rm -rf -- "$work"' EXIT
    out="$work/gather"
    mkdir -p "$out"
    oc() {
      case "$*" in
        # conditions jsonpath yields a line per condition; relatedObjects yields
        # nothing, so related-objects.yaml stays empty and its own coverage
        # lives in the rerun self-test above.
        *'.status.conditions'*) printf 'Available=True reason=Ready msg=ok\n' ;;
        *jsonpath*) : ;;
        *) printf 'kind: Collected\nmetadata:\n  name: gathered\n' ;;
      esac
    }
    collect_all "$out"
    # A collector that stopped writing would make the comparison below
    # vacuously true, so assert the run produced the files it claims to.
    for f in clusterbaseline.yaml clusterbaseline-conditions.txt \
      operator-namespace.yaml operator-secrets.txt operator-events.txt \
      operator.log operator-deploy-describe.txt consoleplugin.yaml \
      compliance.yaml compliance-events.txt machineconfigpools.yaml \
      machineconfigpools-pause.txt; do
      [ -s "$out/$f" ] || {
        echo "FAIL: $f was not collected" >&2
        exit 1
      }
    done
    [ -e "$out/related-objects.yaml" ] || {
      echo "FAIL: related-objects.yaml was not collected" >&2
      exit 1
    }
    cp -R "$out" "$work/run1"
    collect_all "$out"
    diff -r "$work/run1" "$out" >/dev/null || {
      echo "FAIL: a second collect_all run into the same dir changed the output" >&2
      diff -r "$work/run1" "$out" >&2 || true
      exit 1
    }
    # Every target gone (RBAC revoked, CR deleted, plugin undeployed): each
    # file must be empty, not the previous run's content.
    oc() { return 1; }
    collect_all "$out" 2>/dev/null || true
    for f in clusterbaseline.yaml clusterbaseline-conditions.txt \
      operator-namespace.yaml operator-secrets.txt operator-events.txt \
      operator.log operator-deploy-describe.txt compliance.yaml \
      compliance-events.txt machineconfigpools.yaml \
      machineconfigpools-pause.txt; do
      [ ! -s "$out/$f" ] || {
        echo "FAIL: stale $f survived a run that collected nothing" >&2
        exit 1
      }
    done

    echo "must-gather collect_all rerun self-test ok"
  )
}

usage() {
  cat <<EOF
Usage: ${prog} [output-dir]
       ${prog} --self-test
       ${prog} --help

Collect baseline-security operator and Compliance Operator state for support.
Writes YAML and logs into output-dir (default: ./must-gather). Requires an
authenticated oc context. Secret objects and waiver requestedBy/approvedBy
are omitted.

Options:
  --self-test   Run redaction unit tests; no cluster required
  -h, --help    print this usage and exit 0
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

if [ "${1:-}" = "--self-test" ]; then
  if [ "$#" -ne 1 ]; then
    echo "${prog}: --self-test takes no arguments" >&2
    usage >&2
    exit 2
  fi
  self_test
  exit 0
fi

if [ "$#" -gt 1 ]; then
  echo "${prog}: unexpected arguments: $*" >&2
  usage >&2
  exit 2
fi

OUT="${1:-must-gather}"
# Refuse empty, stdout marker, or flag-shaped paths so a bad invocation cannot
# mkdir "-" / "" or treat an option as a directory.
if [ -z "$OUT" ] || [ "$OUT" = "-" ]; then
  echo "${prog}: invalid output directory: ${OUT:-<empty>}" >&2
  usage >&2
  exit 2
fi
if [[ "$OUT" == -* ]]; then
  echo "${prog}: unknown option: $OUT" >&2
  usage >&2
  exit 2
fi
mkdir -p -- "$OUT"
# Owner-only: dumps include logs/events that may carry cluster-sensitive data.
# Waiver requestedBy/approvedBy are stripped from clusterbaseline.yaml and the
# scanner output from compliance.yaml below.
# No `--` guard: BSD chmod (macOS) reads it as a filename. A flag-shaped OUT
# was refused as an unknown option above.
chmod 700 "$OUT"

# Bound every API call. must-gather is run precisely when the cluster is
# unhealthy, so a wedged apiserver (stuck webhook, slow etcd) would otherwise
# hang the whole collector on a single oc call with no output. command avoids
# recursing into this wrapper; the flag applies to every oc below, including the
# auth gate and the relatedObjects loop.
oc() { command oc --request-timeout=30s "$@"; }

# Fail fast when the kubeconfig is missing or expired so support does not get
# an empty directory that looks like a successful collection.
if ! oc whoami >/dev/null 2>&1; then
  echo "${prog}: oc is not authenticated (oc whoami failed); refusing empty must-gather" >&2
  exit 1
fi

collect_all "$OUT"

echo "Collected baseline-security must-gather into $OUT"
if [ "$failures" -gt 0 ]; then
  echo "warning: ${failures} collection step(s) failed (see warnings above); archive may be incomplete" >&2
  exit 1
fi
