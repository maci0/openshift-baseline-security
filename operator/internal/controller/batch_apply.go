package controller

import (
	"context"
	"fmt"
	"maps"
	"slices"
	"strings"
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/types"
	utilvalidation "k8s.io/apimachinery/pkg/util/validation"
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
	batch := cb.Status.RemediationBatch
	names := cb.Annotations[batchApplyAnnotation]

	if batch == nil {
		if strings.TrimSpace(names) == "" {
			return r.resumeOrphanedBatch(ctx, cb)
		}
		list := batchRemediationNames(names)
		// Annotation of only commas/whitespace: do not open an empty batch.
		if len(list) == 0 {
			return r.resumeOrphanedBatch(ctx, cb)
		}
		if len(list) > batchMaxRemediations {
			return fmt.Errorf("batch requests %d remediations; maximum is %d", len(list), batchMaxRemediations)
		}
		for _, name := range list {
			if errs := utilvalidation.IsDNS1123Subdomain(name); len(errs) > 0 {
				return fmt.Errorf("invalid remediation name %q: %s", name, strings.Join(errs, "; "))
			}
		}

		// Validate every existing target before any mutation. In particular, the
		// suite check prevents ClusterBaseline patch permission from becoming a
		// deputy that can apply arbitrary ComplianceRemediations.
		// Drop race-deleted (NotFound) names so status only lists remediations we
		// will actually apply. If none remain, clear the one-shot annotation
		// instead of opening a fake batch that "succeeds" with no work.
		// Build owned suites once for the whole list (up to batchMaxRemediations).
		suites := ownedSuites(cb)
		pools := map[string]bool{}
		keep := make([]string, 0, len(list))
		for _, name := range list {
			rem, err := r.getBatchRemediation(ctx, name, suites)
			if err != nil {
				return err
			}
			if rem == nil {
				// Race-deleted between UI submit and start: log each drop so a
				// partial batch (started with fewer remediations) is explainable.
				log.FromContext(ctx).Info("remediation batch: target not found, skipping",
					"remediation", name, "name", cb.Name)
				continue
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
			if err := r.clearBatchAnnotations(ctx, cb, true, nil, false); err != nil {
				return fmt.Errorf("clearing empty batch-apply annotation: %w", err)
			}
			return nil
		}
		requested := batchRemediationNames(names)
		list = keep
		poolList := slices.Sorted(maps.Keys(pools))
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

	// Applying: resume when every listed remediation is Applied, or past grace.
	// NotFound/NoMatch: remediation or CRDs gone; skip (do not block resume forever).
	// Transient Get errors must not look like Applied (would unpause early), but
	// must not bypass batchResumeGrace either (pools must never stay paused forever).
	// Also track whether any remediation is still apply=true: if none are (the
	// user reverted them all), the batch is cancelled and we resume at once.
	applied := true
	anyApplying := false
	var getErr error
	// Names gone mid-batch (delete / CRDs uninstalled). Treated as done so resume
	// is not blocked forever; collect for the finish log so reason=applied is not
	// misread as "every listed remediation reached Applied".
	var missing []string
	for _, name := range batch.Remediations {
		rem := u(remediationGVK)
		if err := r.Get(ctx, types.NamespacedName{Namespace: complianceNamespace, Name: name}, rem); err != nil {
			if apierrors.IsNotFound(err) || meta.IsNoMatchError(err) {
				// Info: silent skip leaves on-call unable to explain why a batch
				// finished as applied/cancelled while listed remediations vanished
				// (UI delete, GC, or compliance CRDs removed mid-batch).
				log.FromContext(ctx).Info("remediation missing while waiting for batch; treating as done",
					"remediation", name, "name", cb.Name, "notFound", apierrors.IsNotFound(err))
				missing = append(missing, name)
				continue
			}
			// Name every failure so a multi-rem batch requeue is actionable.
			getErr = fmt.Errorf("getting remediation %q: %w", name, err)
			applied = false
			continue
		}
		if s, _, err := unstructured.NestedString(rem.Object, "status", "applicationState"); err != nil {
			// Wrong-type status must not look like Applied (would unpause early).
			getErr = fmt.Errorf("reading applicationState for remediation %q: %w", name, err)
			applied = false
			continue
		} else if s != "Applied" {
			applied = false
		}
		if a, _, err := unstructured.NestedBool(rem.Object, "spec", "apply"); err != nil {
			// Corrupt apply must not cancel the batch (false negative on anyApplying).
			getErr = fmt.Errorf("reading spec.apply for remediation %q: %w", name, err)
			applied = false
			continue
		} else if a {
			anyApplying = true
		}
	}
	// Cancelled only when we saw every remediation cleanly (no transient error hid
	// an apply=true one), so a flaky Get never triggers an early resume.
	cancelled := !anyApplying && getErr == nil
	pastGrace := batchPastGrace(batch.StartedAt, time.Now())
	if applied || pastGrace || cancelled {
		for _, p := range batch.Pools {
			if err := r.setMCPPaused(ctx, p, false, batch.PauseOwner); err != nil {
				return fmt.Errorf("resuming MachineConfigPool %q after batch: %w", p, err)
			}
		}
		// Clear one-shot + recovery annotations after pools are resumed.
		// Conflict retry: concurrent console patches must not leave the request
		// annotation stuck (would re-enter Applying forever while pools are free).
		if err := r.clearBatchAnnotations(ctx, cb, true, batch.Remediations, true); err != nil {
			return fmt.Errorf("clearing batch annotations after resume: %w", err)
		}
		reason := "applied"
		if cancelled {
			reason = "cancelled"
		} else if pastGrace {
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
