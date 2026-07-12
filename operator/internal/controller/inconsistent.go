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
func effectiveInconsistentStatus(item *unstructured.Unstructured) string {
	states := inconsistentStates(item)
	for state := range states {
		switch state {
		case "PASS", "FAIL", "ERROR", "NOT-APPLICABLE", "SKIP":
		default:
			// Future or malformed states must fail closed; otherwise UNKNOWN+PASS
			// would be misreported as a benign PASS.
			return "INCONSISTENT"
		}
	}
	switch {
	case states["FAIL"] || states["ERROR"]:
		return "INCONSISTENT"
	case states["PASS"]:
		return "PASS"
	case states["NOT-APPLICABLE"] || states["SKIP"]:
		return "NOT-APPLICABLE"
	default:
		return "INCONSISTENT"
	}
}

// inconsistentStates returns the set of per-node states of an INCONSISTENT check,
// gathered from the inconsistent-source annotation and most-common-status.
// Untrusted cluster data: tolerant of malformed values, never panics.
func inconsistentStates(item *unstructured.Unstructured) map[string]bool {
	ann := item.GetAnnotations()
	states := map[string]bool{}
	for _, s := range strings.Split(ann[inconsistentSourceAnn], ",") {
		if i := strings.IndexByte(s, ':'); i >= 0 {
			if st := strings.ToUpper(strings.TrimSpace(s[i+1:])); st != "" {
				states[st] = true
			}
		}
	}
	if mc := strings.ToUpper(strings.TrimSpace(ann[mostCommonStatusAnn])); mc != "" {
		states[mc] = true
	}
	return states
}
