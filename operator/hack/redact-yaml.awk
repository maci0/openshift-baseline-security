# Redact personal data out of a kubectl/oc YAML dump, in place, driven by
# must-gather.sh. Two policies, selected with -v policy:
#
#   clusterbaseline  spec.waivers[].requestedBy and .approvedBy, the audit
#                    attribution naming a cluster user, plus the same fields
#                    embedded in the kubectl last-applied-configuration JSON
#                    blob. Waiver name and reason stay: scoring cannot be
#                    debugged without them.
#   compliance       the Compliance Operator scanner output (details,
#                    standardOutput, summary, checkError, and a
#                    ComplianceCheckResult's status.result), which for the
#                    account rules is a /etc/passwd or /etc/shadow listing
#                    naming real accounts. Control identity, the compliant
#                    flag, the scan verdict, and the timestamps stay.
#
# A dropped key takes its value with it: kubectl emits each mapping key on its
# own line and a value containing a newline as a literal/folded block, with the
# text on the following more-indented lines, so deleting the key line alone
# would leave that text in the dump. A folded last-applied-configuration is the
# one exception: its JSON blob lives on the continuation lines and still names
# the waiver, so the key line goes and the blob is redacted in place.
function indent(s) { match(s, /^[ \t]*/); return RLENGTH }

BEGIN {
  # -1 is "not dropping": an uninitialized variable compares as 0 and would
  # swallow the first indented line of the dump.
  dropping = -1
  check_result = 0
  if (policy != "clusterbaseline" && policy != "compliance") {
    print "redact-yaml.awk: unknown policy: " policy > "/dev/stderr"
    exit 2
  }
}

{
  if (dropping >= 0) {
    # Blank lines inside a block scalar belong to it; a line at or left of
    # the key indent is the next sibling and must be kept.
    if ($0 ~ /^[ \t]*$/) next
    if (indent($0) > dropping) next
    dropping = -1
  }
  if (drop($0)) {
    dropping = keeps_continuation($0) ? -1 : indent($0)
    next
  }
  print rewrite($0)
}

function drop(line) {
  if (policy == "compliance") return drop_compliance(line)
  if (line ~ /^[ \t]*(requestedBy|approvedBy):/) return 1
  if (line ~ /kubectl\.kubernetes\.io\/last-applied-configuration:/) return 1
  return 0
}

function keeps_continuation(line) {
  if (policy == "compliance") return 0
  return line ~ /kubectl\.kubernetes\.io\/last-applied-configuration:/ ? 1 : 0
}

function rewrite(line) {
  if (policy == "compliance") return line
  gsub(/"(requestedBy|approvedBy)"[ \t]*:[ \t]*"[^"]*"[ \t]*,?[ \t]*/, "", $0)
  return $0
}

# Compliance Operator kinds arrive as one document per object, so the kind line
# is what separates the per-check message key from the scan verdict key: both
# are named `result`, and only the verdict is worth keeping.
function drop_compliance(line) {
  if (line ~ /^---[ \t]*$/) { check_result = 0; return 0 }
  if (line ~ /^kind:[ \t]*"?ComplianceCheckResult"?[ \t]*$/) { check_result = 1; return 0 }
  if (line ~ /^[ \t]*(details|standardOutput|summary|checkError):/) return 1
  if (check_result && line ~ /^[ \t]*result:/) return 1
  return 0
}
