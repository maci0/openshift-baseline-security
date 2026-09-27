package controller

import (
	"context"
	"maps"
	"slices"
	"strings"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	utilvalidation "k8s.io/apimachinery/pkg/util/validation"
	"sigs.k8s.io/controller-runtime/pkg/log"

	baselinev1alpha1 "github.com/maci0/baseline-security-operator/api/v1alpha1"
)

// batchApplyAnnotation on the ClusterBaseline carries a comma-separated list of
// ComplianceRemediation names to batch-apply with MachineConfigPool pause/resume.
const batchApplyAnnotation = "baselinesecurity.openshift.io/batch-apply"

// batchStartedAtAnnotation persists the safety-valve clock before any MCP is
// paused, so repeated status-subresource failures cannot reset the grace period.
const batchStartedAtAnnotation = "baselinesecurity.openshift.io/batch-started-at"

// batchPoolsAnnotation records affected pools before pausing so deletion or a
// removed request annotation can still recover when status was never persisted.
const batchPoolsAnnotation = "baselinesecurity.openshift.io/batch-pools"

// batchPauseOwnerAnnotation marks an MCP pause as ours. Without ownership,
// finishing a batch would unpause a pool an administrator had already paused.
const batchPauseOwnerAnnotation = "baselinesecurity.openshift.io/batch-pause-owner"

// Bound the work a single metadata annotation can make the controller perform.
const batchMaxRemediations = 256

// batchMaxPools mirrors the CRD MaxItems on status.remediationBatch.pools. A
// batch spanning more distinct MachineConfigPools than this cannot be recorded
// in status, so it must be refused before any pool is paused; otherwise the
// oversized Pools list fails Status().Update admission and freezes reconcile
// with pools stuck paused. Clamping the status field instead would drop pools
// the operator actually paused, leaking them from the resume path.
const batchMaxPools = 32

// batchResumeGrace forces a resume even if remediations never reach Applied, so
// a MachineConfigPool is never left paused.
const batchResumeGrace = 10 * time.Minute

// batchPastGrace is true when the batch safety timer has elapsed (or is unusable).
// Zero StartedAt (hand-edit / corrupt) must not disable the valve forever.
// Far-future StartedAt (beyond grace, matching the spirit of parseScanEndTimestamp's
// clock-skew bound) is also treated as garbage. Modest future skew (NTP / leader
// handoff of a few seconds) must NOT force an immediate resume of a live pause.
func batchPastGrace(started metav1.Time, now time.Time) bool {
	if started.IsZero() {
		return true
	}
	// Started more than `grace` ahead of now is corrupt, not skew.
	if started.After(now.Add(batchResumeGrace)) {
		return true
	}
	return now.Sub(started.Time) > batchResumeGrace
}

// poolFromRemediation returns the MachineConfigPool a node remediation targets,
// or "" for a non-node one. Prefer the rendered MachineConfig's role label, but
// the Compliance Operator does not always set it, so fall back to the scan-name
// label: node scans run per-MCP, named "<profile>-node-<pool>". Without this
// fallback a node remediation whose MachineConfig has no role label would pause
// no pool, so its apply would reboot the node uncoalesced.
// Role labels and scan-name suffixes are untrusted cluster data; non-DNS1123
// values are dropped so they never enter batch pool lists or MCP Get calls.
//
// Untrusted / partial objects: avoid unstructured.NestedMap, which DeepCopyJSON-
// panics on non-JSON types (e.g. int) that can appear in hand-built or partially
// converted maps. NestedFieldNoCopy + type assert matches completedSuiteTimes.
func poolFromRemediation(rem *unstructured.Unstructured) string {
	raw, found, err := unstructured.NestedFieldNoCopy(rem.Object, "spec", "current", "object")
	// Wrong-type object: ignore it and fall through to the scan-name label.
	if err == nil && found {
		if obj, ok := raw.(map[string]any); ok {
			// A MachineConfig names its pool precisely via the role label.
			if kind, _, _ := unstructured.NestedString(obj, "kind"); kind == "MachineConfig" {
				// Non-allocating single-label read (batch path can hit 256 remediations).
				if role := unstructuredLabel(obj, "machineconfiguration.openshift.io/role"); role != "" {
					if pool := validK8sName(role); pool != "" {
						return pool
					}
				}
			}
		}
	}
	// Fallback for any node remediation the MCO reboots but that has no usable
	// MachineConfig role label: a KubeletConfig, a partially rendered object, or a
	// MachineConfig with a missing/invalid role. A "…-node-<pool>" scan name
	// identifies the pool to pause, so it must NOT be skipped just because the
	// rendered kind is not MachineConfig. Platform scans have no "-node-" and
	// correctly yield no pool (not a node remediation).
	// Single-key read: GetLabels copies the whole map (batch path can hit 256 remediations).
	scan := unstructuredLabel(rem.Object, scanNameLabel)
	if i := strings.LastIndex(scan, "-node-"); i >= 0 {
		return validK8sName(scan[i+len("-node-"):])
	}
	return ""
}

// validK8sName returns name when it is a non-empty DNS-1123 subdomain
// (Kubernetes resource name shape), otherwise "". One definition of the rule
// for every untrusted name the reconciler feeds to a Get or a name list: a
// non-conforming name returns 400 rather than 404, so it must be dropped before
// it reaches the apiserver.
func validK8sName(name string) string {
	if name == "" || len(utilvalidation.IsDNS1123Subdomain(name)) > 0 {
		return ""
	}
	return name
}

// capBatchRemediations trims an untrusted remediation name list to
// batchMaxRemediations, logging when it had to. source names the list it came
// from ("status remediation batch", "batch-apply annotation") so the log says
// which one was capped. Without the cap one oversized list drives a read per
// entry (the wait path polls every 15s; the finalizer runs once) with no bound.
func capBatchRemediations(
	ctx context.Context, cb *baselinev1alpha1.ClusterBaseline, names []string, source string,
) []string {
	if len(names) <= batchMaxRemediations {
		return names
	}
	log.FromContext(ctx).Info(source+" exceeds max; capping",
		"count", len(names), "max", batchMaxRemediations, "name", cb.Name)
	return names[:batchMaxRemediations]
}

// splitValidRemediationNames separates a remediation name list into the names a
// Get accepts and the ones it rejects. A non-DNS-1123 name returns 400, not
// 404, which would otherwise fall through to the transient-error path and wedge
// the wait (pools paused until grace) or block finalizer removal. Each rejected
// name is logged under logMsg; both are returned so a caller that reports
// missing remediations can include them.
func splitValidRemediationNames(
	ctx context.Context, cb *baselinev1alpha1.ClusterBaseline, names []string, logMsg string,
) (valid, invalid []string) {
	valid = make([]string, 0, len(names))
	for _, name := range names {
		if validK8sName(name) == "" {
			log.FromContext(ctx).Info(logMsg, "remediation", name, "name", cb.Name)
			invalid = append(invalid, name)
			continue
		}
		valid = append(valid, name)
	}
	return valid, invalid
}

func batchPauseOwner(cb *baselinev1alpha1.ClusterBaseline) string {
	if cb.UID != "" {
		return string(cb.UID)
	}
	if cb.Name != "" {
		return cb.Name
	}
	return clusterBaselineName
}

// batchUnreported is true when the CR still carries a durable marker of an
// unfinished batch: the one-shot request or either recovery key. Both the
// finish path and the orphan path strip all of them from the cluster before
// they count an outcome, so a reconcile that arrives with the markers already
// gone and status.remediationBatch still set is a retry of a batch that was
// already reported, not a new batch.
//
// The status write that clears status.remediationBatch is the trailing
// Status().Update, not the annotation Patch: when it fails, every reconcile
// re-runs the finish path (resuming pools, which is an idempotent no-op, and
// counting again). The annotations are the only durable record that the count
// has already happened, so the counter reads them.
func batchUnreported(cb *baselinev1alpha1.ClusterBaseline) bool {
	ann := cb.GetAnnotations()
	return ann[batchApplyAnnotation] != "" ||
		ann[batchStartedAtAnnotation] != "" ||
		ann[batchPoolsAnnotation] != ""
}

func uniqueSortedStrings(values []string) []string {
	set := make(map[string]bool, len(values))
	for _, value := range values {
		if value != "" {
			set[value] = true
		}
	}
	return slices.Sorted(maps.Keys(set))
}

func batchRemediationNames(raw string) []string {
	return uniqueSortedStrings(splitCSV(raw))
}
