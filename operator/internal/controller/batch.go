package controller

import (
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// batchApplyAnnotation on the ClusterBaseline carries a comma-separated list of
// ComplianceRemediation names to batch-apply with MachineConfigPool pause/resume.
const batchApplyAnnotation = "baselinesecurity.io/batch-apply"

// batchStartedAtAnnotation persists the safety-valve clock before any MCP is
// paused, so repeated status-subresource failures cannot reset the grace period.
const batchStartedAtAnnotation = "baselinesecurity.io/batch-started-at"

// batchPoolsAnnotation records affected pools before pausing so deletion or a
// removed request annotation can still recover when status was never persisted.
const batchPoolsAnnotation = "baselinesecurity.io/batch-pools"

// batchPauseOwnerAnnotation marks an MCP pause as ours. Without ownership,
// finishing a batch would unpause a pool an administrator had already paused.
const batchPauseOwnerAnnotation = "baselinesecurity.io/batch-pause-owner"

// Bound the work a single metadata annotation can make the controller perform.
const batchMaxRemediations = 256

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
