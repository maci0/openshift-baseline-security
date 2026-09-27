package controller

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"slices"
	"strings"

	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"

	baselinev1alpha1 "github.com/maci0/baseline-security-operator/api/v1alpha1"
)

// applyRemediationBatch runs a two-phase batch apply driven by the batch-apply
// annotation: pause the affected MachineConfigPools and set apply on all listed
// remediations, then resume once they are Applied (or after a grace) so the pools
// reboot once. Resume is guaranteed: any failure still resumes the pools.
//
// The one-shot annotation is kept until pools are resumed. Clearing it before
// status.remediationBatch is persisted would leave pools paused forever if the
// end-of-reconcile Status().Update fails (annotation gone, batch nil, no recovery).
func (r *ClusterBaselineReconciler) applyRemediationBatch(ctx context.Context, cb *baselinev1alpha1.ClusterBaseline) error {
	if cb.Status.RemediationBatch == nil {
		return r.openRemediationBatch(ctx, cb, cb.Annotations[batchApplyAnnotation])
	}
	return r.finishRemediationBatch(ctx, cb)
}

// openRemediationBatch is phase one: no status.remediationBatch yet. Validates
// the request annotation, pauses pools, applies remediations, and records the
// batch on status for finishRemediationBatch.
func (r *ClusterBaselineReconciler) openRemediationBatch(
	ctx context.Context, cb *baselinev1alpha1.ClusterBaseline, names string,
) error {
	if strings.TrimSpace(names) == "" {
		return r.resumeOrphanedBatch(ctx, cb)
	}
	list := batchRemediationNames(names)
	// Annotation of only commas/whitespace: do not open an empty batch.
	if len(list) == 0 {
		return r.resumeOrphanedBatch(ctx, cb)
	}
	// Match finish/skip clears on the evaluated request so a concurrent
	// console resubmit (different CSV) is preserved, not silently deleted.
	// Alias before list is narrowed to the surviving set below.
	requested := list
	// Untrusted annotation: refuse before any MCP pause / remediation apply.
	// Clear the one-shot request (like too-many-pools) so a hostile or
	// console-buggy value cannot sticky-Degrade every reconcile forever.
	if len(list) > batchMaxRemediations {
		log.FromContext(ctx).Info("remediation batch skipped: too many remediations",
			"name", cb.Name, "count", len(list), "max", batchMaxRemediations)
		if err := r.clearBatchAnnotations(ctx, cb, requested, false); err != nil {
			return fmt.Errorf("clearing oversize batch-apply annotation: %w", err)
		}
		return nil
	}
	for _, name := range list {
		if validK8sName(name) == "" {
			log.FromContext(ctx).Info("remediation batch skipped: invalid remediation name",
				"name", cb.Name, "remediation", name)
			if err := r.clearBatchAnnotations(ctx, cb, requested, false); err != nil {
				return fmt.Errorf("clearing invalid batch-apply annotation: %w", err)
			}
			return nil
		}
	}

	// Validate every existing target before any mutation. In particular, the
	// suite check prevents ClusterBaseline patch permission from becoming a
	// deputy that can apply arbitrary ComplianceRemediations.
	// Drop race-deleted (NotFound) names so status only lists remediations we
	// will actually apply. If none remain, clear the one-shot annotation
	// instead of opening a fake batch that "succeeds" with no work.
	// Permanent validation rejects (foreign suite, MissingDependencies) skip
	// that name only: applying a mixed list must not drop valid remediations
	// or sticky-Degrade forever. Same as NotFound race drops. If none remain
	// eligible, clear the one-shot request (empty-keep path below).
	// Transient Get/API errors still return so the request is retried.
	// Build owned suites once for the whole list (up to batchMaxRemediations).
	suites := ownedSuites(cb)
	// One paged List replaces a Get per name, matching the wait path. Opening a
	// maximum batch validated 256 remediations through 256 sequential live
	// apiserver round trips (unstructured reads bypass the manager cache) before
	// a single pool was paused. Paging stops as soon as every name is found.
	rems, lerr := r.listRemediationsForBatch(ctx, list)
	if lerr != nil {
		// NoMatch (CRDs absent) and transient read failures both retry the one-shot
		// request rather than looking like every target was missing. Wrap with a
		// name so the log still identifies which request was not resolved.
		return fmt.Errorf("listing remediation %q for batch: %w", list[0], lerr)
	}
	pools := map[string]bool{}
	keep := make([]string, 0, len(list))
	for _, name := range list {
		rem, found := rems[name]
		if !found {
			// Race-deleted between UI submit and start: log each drop so a
			// partial batch (started with fewer remediations) is explainable.
			log.FromContext(ctx).Info("remediation batch: target not found, skipping",
				"remediation", name, "name", cb.Name)
			continue
		}
		if !remediationOwnedByBaseline(suites, rem) {
			err := fmt.Errorf("remediation %q: %w", name, errBatchForeignSuite)
			log.FromContext(ctx).Info("remediation batch: permanent target reject, skipping",
				"name", cb.Name, "remediation", name, "error", err.Error())
			continue
		}
		if err := validateBatchTarget(rem); err != nil {
			if isPermanentBatchTargetReject(err) {
				log.FromContext(ctx).Info("remediation batch: permanent target reject, skipping",
					"name", cb.Name, "remediation", name, "error", err.Error())
				continue
			}
			return err
		}
		keep = append(keep, name)
		if p := poolFromRemediation(rem); p != "" {
			pools[p] = true
		}
	}
	if len(keep) == 0 {
		log.FromContext(ctx).Info("remediation batch skipped: no remediations found",
			"name", cb.Name, "requested", list)
		// Drop the one-shot request only (recovery keys were never written).
		// Match on the request we evaluated so a console resubmit that landed
		// after we read the annotation is preserved, not silently deleted.
		// Covers all-NotFound and all-permanent-reject (foreign/MissingDeps).
		if err := r.clearBatchAnnotations(ctx, cb, requested, false); err != nil {
			return fmt.Errorf("clearing empty batch-apply annotation: %w", err)
		}
		return nil
	}
	list = keep
	// Union with pools recorded on a prior (pre-crash) open. This runs when
	// status.remediationBatch was lost (crash/leader handoff) and the batch is
	// re-opened from the annotation; poolList is then rebuilt from only the
	// surviving remediations. A pool paused before the crash whose remediation
	// vanished in the restart window is no longer in poolList, so without this
	// it drops out of the pause set, status.Pools, and every resume path and is
	// left paused forever (nodes stuck). Re-pausing an already-paused pool is an
	// idempotent no-op, and the union stays within batchMaxPools (it is a subset
	// of the original open, which the guard already bounded).
	for _, p := range batchRemediationNames(cb.Annotations[batchPoolsAnnotation]) {
		if p != "" {
			pools[p] = true
		}
	}
	poolList := slices.Sorted(maps.Keys(pools))
	if len(poolList) > batchMaxPools {
		// More target pools than status.remediationBatch.pools can hold. Refuse
		// before pausing anything (a paused-but-unrecorded pool would leak) and
		// drop the one-shot request so this does not retry-freeze the reconcile.
		log.FromContext(ctx).Info("remediation batch skipped: too many target MachineConfigPools",
			"name", cb.Name, "pools", len(poolList), "max", batchMaxPools)
		// Match on the evaluated request so a console resubmit that raced this
		// reconcile is preserved rather than unconditionally deleted.
		if err := r.clearBatchAnnotations(ctx, cb, requested, false); err != nil {
			return fmt.Errorf("clearing oversized batch-apply annotation: %w", err)
		}
		return nil
	}
	startedAt, err := r.ensureBatchMetadata(ctx, cb, poolList, requested, list)
	if err != nil {
		return err
	}
	owner := batchPauseOwner(cb)
	newBatch := func(pools []string) *baselinev1alpha1.RemediationBatchStatus {
		return &baselinev1alpha1.RemediationBatchStatus{
			Phase: baselinev1alpha1.RemediationBatchPhaseApplying, Pools: pools, Remediations: list, StartedAt: startedAt, PauseOwner: owner,
		}
	}
	// Pause first so all apply-triggered MachineConfig renders coalesce.
	// On a mid-list failure, unpause what we already paused this attempt so
	// a permanent error cannot leave a subset of pools paused with no batch.
	logger := log.FromContext(ctx)
	var paused []string
	for _, p := range poolList {
		if err := r.setMCPPaused(ctx, p, true, owner); err != nil {
			// If unpause itself failed, record the batch so batchResumeGrace
			// can force resume instead of leaving pools paused forever while
			// apply/pause keeps failing and status.remediationBatch stays nil.
			if r.resumePoolsBestEffort(ctx, paused, owner, "failed to resume MachineConfigPool after pause failure") {
				cb.Status.RemediationBatch = newBatch(slices.Clone(paused))
			}
			return fmt.Errorf("pausing MachineConfigPool %q for remediation batch: %w", p, err)
		}
		paused = append(paused, p)
	}
	for _, name := range list {
		// Reuse suites from validation (same membership for the whole batch).
		if err := r.applyOwnedRemediation(ctx, cb, name, suites); err != nil {
			// Resume any paused pools so a failure never leaves them paused.
			if r.resumePoolsBestEffort(ctx, poolList, owner, "failed to resume MachineConfigPool after batch apply error") {
				cb.Status.RemediationBatch = newBatch(poolList)
			}
			return fmt.Errorf("applying remediation %q in batch: %w", name, err)
		}
	}
	// Keep the annotation until resume. status.remediationBatch is written by
	// the end-of-reconcile Status().Update; if that fails, the annotation still
	// drives a restart rather than orphaning paused pools.
	cb.Status.RemediationBatch = newBatch(poolList)
	// Info: MCP pause is operationally sensitive; on-call needs a clear
	// start marker in logs when investigating stuck paused pools.
	logger.Info("remediation batch started",
		"name", cb.Name, "remediations", len(list), "pools", poolList)
	return nil
}

// remediationListPageSize bounds one apiserver List of ComplianceRemediations.
// The namespace also holds remediations CO created for foreign scans, so the
// page caps what a single response pins while the batch polls every 15s.
const remediationListPageSize int64 = 500

// errComplianceCRDsAbsent reports that the ComplianceRemediation CRD is no
// longer registered, so the batch cannot observe any of its names. The caller
// still treats every name as terminal (that is what unblocks the pools), but it
// must not log the same line as a per-name NotFound: the real cause is the CRD
// disappearing mid-batch, and on-call needs to see which one happened.
var errComplianceCRDsAbsent = errors.New("ComplianceRemediation CRD is not registered")

// listRemediationsForBatch returns the named ComplianceRemediations from the
// compliance namespace, indexed by name. want must be the batch's capped name
// list. An errComplianceCRDsAbsent error means the CRD is absent: every name is
// then missing, which the caller treats as terminal.
func (r *ClusterBaselineReconciler) listRemediationsForBatch(
	ctx context.Context, want []string,
) (map[string]*unstructured.Unstructured, error) {
	found := make(map[string]*unstructured.Unstructured, len(want))
	if len(want) == 0 {
		return found, nil
	}
	needed := make(map[string]bool, len(want))
	for _, n := range want {
		if validK8sName(n) != "" {
			needed[n] = true
		}
	}
	if len(needed) == 0 {
		return found, nil
	}
	list := uList(remediationGVK)
	cont := ""
	for {
		opts := []client.ListOption{
			client.InNamespace(complianceNamespace),
			client.Limit(remediationListPageSize),
		}
		if cont != "" {
			opts = append(opts, client.Continue(cont))
		}
		if err := r.List(ctx, list, opts...); err != nil {
			if meta.IsNoMatchError(err) {
				return nil, errComplianceCRDsAbsent
			}
			return nil, fmt.Errorf("listing ComplianceRemediations in %s: %w", complianceNamespace, err)
		}
		// Index range: no copy of each remediation's map header per item.
		for i := range list.Items {
			item := &list.Items[i]
			if needed[item.GetName()] {
				found[item.GetName()] = item
			}
		}
		// Every name is present: stop paging rather than walk the rest of the
		// namespace's remediations.
		if len(found) == len(needed) {
			return found, nil
		}
		// A token that does not advance means the next List would replay this
		// page until the reconcile deadline. Stop with what is found; the
		// missing names take the same terminal path as a NotFound.
		next, more := nextPageToken(list.GetContinue(), cont)
		if !more {
			return found, nil
		}
		cont = next
	}
}

// finishRemediationBatch is phase two: resume when every listed remediation is
// Applied, or past grace. NotFound/NoMatch: remediation or CRDs gone; skip (do
// not block resume forever). Transient Get errors must not look like Applied
// (would unpause early), but must not bypass batchResumeGrace either (pools
// must never stay paused forever). Also track whether any remediation is still
// apply=true: if none are (the user reverted them all), the batch is cancelled
// and we resume at once.
func (r *ClusterBaselineReconciler) finishRemediationBatch(
	ctx context.Context, cb *baselinev1alpha1.ClusterBaseline,
) error {
	batch := cb.Status.RemediationBatch
	// Captured before anything is written: clearBatchAnnotations below strips
	// these keys from the cluster, and on this same object from memory, so
	// reading them later would always say the batch had already been counted.
	countThis := batchUnreported(cb)
	applied := true
	anyApplying := false
	var getErr error
	// Status is hostile input here: this runs before sanitizeRemediationBatch, so
	// a corrupt restored list would otherwise fire one Get per entry every
	// reconcile until grace. Cap like resumeBatchPoolsOnDelete.
	//
	// A non-DNS1123 name can return 400 (not 404), which would fall through to
	// getErr and block resume as "still waiting" until grace. Treat those names as
	// terminal like NotFound and collect them with the names gone mid-batch (delete
	// / CRDs uninstalled), so the finish log can report both.
	names, missing := splitValidRemediationNames(ctx, cb,
		capBatchRemediations(ctx, cb, batch.Remediations, "status remediation batch"),
		"skipping invalid remediation name while waiting for batch")
	// One paged List replaces a Get per name. A batch lists up to 256
	// remediations and reconcile requeues every 15s while one is active, so the
	// old shape spent up to 256 sequential live apiserver round trips per poll
	// (unstructured reads bypass the manager cache) just to read apply and
	// applicationState. Paging stops as soon as every listed name is found.
	rems, lerr := r.listRemediationsForBatch(ctx, names)
	// Compliance CRDs uninstalled mid-batch: every name is unknowable, but the
	// pools must still be released, so take the same terminal path as a NotFound
	// rather than holding them paused until the grace deadline. Every name then
	// counts as missing, which leaves anyApplying false and getErr nil, so the
	// batch finishes with reason=cancelled. The Error line above carries the real
	// cause; reason alone would read as the user reverting the remediations.
	crdsAbsent := errors.Is(lerr, errComplianceCRDsAbsent)
	if crdsAbsent {
		log.FromContext(ctx).Error(lerr,
			"ComplianceRemediation CRD gone while waiting for batch; treating every remediation as done",
			"name", cb.Name, "remediationCount", len(names))
	} else if lerr != nil {
		// Transient read failure: keep the pools paused and let the grace
		// deadline below decide, exactly as a per-name Get failure did.
		getErr = lerr
		applied = false
	}
	for _, name := range names {
		if lerr != nil && !crdsAbsent {
			// State is unknown for every name this cycle.
			continue
		}
		rem, found := rems[name]
		if !found {
			// Info: silent skip leaves on-call unable to explain why a batch
			// finished as applied/cancelled while listed remediations vanished
			// (UI delete, GC, or compliance CRDs removed mid-batch).
			// notFound is false when the CRD is gone: nothing was looked up.
			log.FromContext(ctx).Info("remediation missing while waiting for batch; treating as done",
				"remediation", name, "name", cb.Name, "notFound", !crdsAbsent)
			missing = append(missing, name)
			continue
		}
		s, _, err := unstructured.NestedString(rem.Object, "status", "applicationState")
		if err != nil {
			// Permanent corruption: treat as done (like NotFound). Do not set getErr
			// or the wait path sticky-Degrades every reconcile until grace even when
			// sibling remediations are Applied / not applying.
			log.FromContext(ctx).Info("remediation unreadable applicationState while waiting; treating as done",
				"remediation", name, "name", cb.Name, "error", err.Error())
			missing = append(missing, name)
			continue
		}
		if s != "Applied" {
			applied = false
		}
		a, _, err := unstructured.NestedBool(rem.Object, "spec", "apply")
		if err != nil {
			// Permanent corruption: not Applied, not applying, not getErr. Allows
			// cancel when no healthy rem is still apply=true, and applied when
			// every other rem is Applied (this name counts as terminal).
			log.FromContext(ctx).Info("remediation unreadable spec.apply while waiting; treating as done",
				"remediation", name, "name", cb.Name, "error", err.Error())
			missing = append(missing, name)
			continue
		}
		if a {
			anyApplying = true
		}
	}
	// A listed remediation we could not observe (NotFound, CRD gone, unreadable
	// apply/applicationState) was never reported Applied, so it satisfies neither
	// `applied` ("every listed remediation reported Applied") nor `cancelled`
	// ("none still apply=true"): the state of that name is unknown, not negative.
	// Without this, a batch whose fixes never landed because its remediations
	// vanished mid-batch counted as a clean success and RemediationBatchGraceResume
	// stayed silent.
	unconfirmed := len(missing) > 0
	if unconfirmed {
		applied = false
	}
	// Cancelled only when we saw every remediation cleanly (no transient error and
	// no unobservable name hid an apply=true one), so a flaky Get never triggers an
	// early resume.
	cancelled := !anyApplying && getErr == nil && !unconfirmed
	pastGrace := batchPastGrace(batch.StartedAt, r.now())
	if applied || pastGrace || cancelled || unconfirmed {
		for _, p := range batch.Pools {
			if err := r.setMCPPaused(ctx, p, false, batch.PauseOwner); err != nil {
				return fmt.Errorf("resuming MachineConfigPool %q after batch: %w", p, err)
			}
		}
		// Clear one-shot + recovery annotations after pools are resumed.
		// Conflict retry: concurrent console patches must not leave the request
		// annotation stuck (would re-enter Applying forever while pools are free).
		if err := r.clearBatchAnnotations(ctx, cb, batch.Remediations, true); err != nil {
			return fmt.Errorf("clearing batch annotations after resume: %w", err)
		}
		reason := "applied"
		if cancelled {
			reason = "cancelled"
		} else if pastGrace || unconfirmed {
			// Unconfirmed is the same signal as grace: pools came back with
			// remediations outstanding, so RemediationBatchGraceResume must see it.
			reason = "grace"
		}
		// waitError: grace can force-resume while remediations were still unreadable;
		// include it so on-call sees why Applied was never confirmed.
		// missingRemediations: NotFound/NoMatch names treated as done (not Applied).
		kv := []any{
			"name", cb.Name,
			"reason", reason,
			"pools", batch.Pools,
			"remediations", len(batch.Remediations),
		}
		if getErr != nil {
			kv = append(kv, "waitError", getErr.Error())
		}
		if len(missing) > 0 {
			kv = append(kv, "missingRemediations", missing)
		}
		log.FromContext(ctx).Info("remediation batch finished", kv...)
		// Counted after the resume landed, so a reconcile that fails before this
		// point does not report an outcome for a batch that is still paused.
		// batchUnreported keeps a retried finish (the trailing Status().Update
		// failed, so status.remediationBatch survived while the annotations
		// above did not) from counting the same batch once per retry.
		if countThis {
			remediationBatches.WithLabelValues(reason).Inc()
		}
		cb.Status.RemediationBatch = nil
		return nil
	}
	// Still waiting: surface a transient Get so the controller requeues, but
	// only before grace expires (after grace we already resumed above).
	if getErr != nil {
		return fmt.Errorf("waiting for batch remediations: %w", getErr)
	}
	return nil
}
