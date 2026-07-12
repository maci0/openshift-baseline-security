package controller

import (
	"strings"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

// Compliance Operator annotations on an INCONSISTENT ComplianceCheckResult: the
// diverging nodes ("node:STATE,node:STATE") and the state the rest share.
const (
	inconsistentSourceAnn = "compliance.openshift.io/inconsistent-source"
	mostCommonStatusAnn   = "compliance.openshift.io/most-common-status"
)

// effectiveInconsistentStatus collapses a benign INCONSISTENT check to the status
// a user actually cares about. The Compliance Operator flags a check INCONSISTENT
// whenever nodes in a pool disagree, including when the check simply does not
// apply on some nodes (PASS where it applies, NOT-APPLICABLE elsewhere). That is
// not a real conflict, so:
//   - any FAIL or ERROR among the node states -> INCONSISTENT (genuine, review it)
//   - else at least one PASS                  -> PASS (passes where it applies)
//   - else only NOT-APPLICABLE/SKIP           -> NOT-APPLICABLE
//   - unknown/empty states                    -> INCONSISTENT (keep the raw signal)
//
// Hot path: multi-node pools can yield many INCONSISTENT CCRs per reconcile.
// Walk annotations with bit flags (no map / Split allocations).
func effectiveInconsistentStatus(item *unstructured.Unstructured) string {
	var hasPass, hasFail, hasError, hasNA, hasSkip, hasUnknown bool
	visitInconsistentStates(item, func(st string) {
		switch st {
		case "PASS":
			hasPass = true
		case "FAIL":
			hasFail = true
		case "ERROR":
			hasError = true
		case "NOT-APPLICABLE":
			hasNA = true
		case "SKIP":
			hasSkip = true
		default:
			// Future or malformed states must fail closed; otherwise UNKNOWN+PASS
			// would be misreported as a benign PASS.
			hasUnknown = true
		}
	})
	switch {
	case hasUnknown || hasFail || hasError:
		return "INCONSISTENT"
	case hasPass:
		return "PASS"
	case hasNA || hasSkip:
		return "NOT-APPLICABLE"
	default:
		return "INCONSISTENT"
	}
}

// inconsistentStates returns the set of per-node states of an INCONSISTENT check,
// gathered from the inconsistent-source annotation and most-common-status.
// Untrusted cluster data: tolerant of malformed values, never panics.
// Used by fuzz tests; aggregateStatus uses effectiveInconsistentStatus (flags).
func inconsistentStates(item *unstructured.Unstructured) map[string]bool {
	states := map[string]bool{}
	visitInconsistentStates(item, func(st string) {
		states[st] = true
	})
	return states
}

// visitInconsistentStates walks CO inconsistent annotations and calls fn with
// each uppercased state token. Single-key annotation reads avoid GetAnnotations()
// full-map copy; comma walk avoids strings.Split on multi-node pools.
func visitInconsistentStates(item *unstructured.Unstructured, fn func(string)) {
	raw := unstructuredAnnotation(item.Object, inconsistentSourceAnn)
	start := 0
	for start <= len(raw) {
		comma := strings.IndexByte(raw[start:], ',')
		end := len(raw)
		if comma >= 0 {
			end = start + comma
		}
		s := strings.TrimSpace(raw[start:end])
		if s != "" {
			if i := strings.IndexByte(s, ':'); i >= 0 {
				if st := strings.ToUpper(strings.TrimSpace(s[i+1:])); st != "" {
					fn(st)
				}
			}
		}
		if comma < 0 {
			break
		}
		start = end + 1
	}
	if mc := strings.ToUpper(strings.TrimSpace(unstructuredAnnotation(item.Object, mostCommonStatusAnn))); mc != "" {
		fn(mc)
	}
}
