package controller

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"slices"
	"strings"
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/types"
	utilvalidation "k8s.io/apimachinery/pkg/util/validation"
	"k8s.io/client-go/util/retry"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"

	baselinev1alpha1 "github.com/maci0/baseline-security-operator/api/v1alpha1"
)

// setMCPPaused changes an MCP only when this batch owns the pause. A pool that
// was already paused without our marker is left alone and therefore remains
// paused after the batch. Empty owner is the upgrade path for a legacy batch
// status created before pause ownership was tracked.
func (r *ClusterBaselineReconciler) setMCPPaused(ctx context.Context, pool string, paused bool, owner string) error {
	if pool == "" {
		return nil
	}
	// Pool names come from untrusted remediation labels / scan-name suffixes.
	// An invalid name would make Get return a non-NotFound error and could
	// wedge batch pause/resume; skip rather than fail the batch.
	if len(utilvalidation.IsDNS1123Subdomain(pool)) > 0 {
		log.FromContext(ctx).Info("skipping MachineConfigPool with invalid name", "pool", pool)
		return nil
	}
	return retry.RetryOnConflict(retry.DefaultRetry, func() error {
		mcp := u(mcpGVK)
		if err := r.Get(ctx, types.NamespacedName{Name: pool}, mcp); err != nil {
			// A missing pool or absent MCP CRD must not wedge the batch.
			if apierrors.IsNotFound(err) || meta.IsNoMatchError(err) {
				// Info: silent skip leaves on-call unable to explain why a
				// batch listed this pool but never paused/resumed it (typo
				// scan-name suffix, CRDs uninstalled mid-batch).
				log.FromContext(ctx).Info("MachineConfigPool missing; skipping pause change",
					"pool", pool, "paused", paused, "owner", owner, "notFound", apierrors.IsNotFound(err))
				return nil
			}
			return fmt.Errorf("getting MachineConfigPool %q: %w", pool, err)
		}

		current, _, err := unstructured.NestedBool(mcp.Object, "spec", "paused")
		if err != nil {
			return fmt.Errorf("reading MachineConfigPool %q spec.paused: %w", pool, err)
		}
		annotations := maps.Clone(mcp.GetAnnotations())
		marker := annotations[batchPauseOwnerAnnotation]
		before := mcp.DeepCopy()

		if paused {
			if owner == "" {
				return fmt.Errorf("pause owner is empty for MachineConfigPool %q", pool)
			}
			if marker != "" && marker != owner {
				return fmt.Errorf("MachineConfigPool %q pause is owned by another batch", pool)
			}
			if current && marker == "" {
				// Administrator-owned pause: use it, but never claim or undo it.
				// Log so RemediationBatchStuck investigation can distinguish
				// "we paused" from "admin already paused" without claiming it.
				log.FromContext(ctx).Info("using administrator-owned MachineConfigPool pause",
					"pool", pool, "owner", owner)
				return nil
			}
			if current && marker == owner {
				return nil
			}
			if annotations == nil {
				annotations = map[string]string{}
			}
			annotations[batchPauseOwnerAnnotation] = owner
			mcp.SetAnnotations(annotations)
			if err := unstructured.SetNestedField(mcp.Object, true, "spec", "paused"); err != nil {
				return fmt.Errorf("setting MachineConfigPool %q paused=true: %w", pool, err)
			}
		} else {
			if owner == "" {
				// Legacy active batches did not mark ownership. Preserve a marker from a
				// newer batch if one somehow overlaps the upgrade window.
				if marker != "" || !current {
					return nil
				}
				if err := unstructured.SetNestedField(mcp.Object, false, "spec", "paused"); err != nil {
					return fmt.Errorf("setting MachineConfigPool %q paused=false: %w", pool, err)
				}
			} else {
				if marker != owner {
					// Resume skipped: another batch owns the pause, or an
					// admin marker is present without our owner. Silent here
					// makes stuck-paused MCPs look like the operator never tried.
					if marker != "" || current {
						log.FromContext(ctx).Info("skipping MachineConfigPool resume; pause not owned by this batch",
							"pool", pool, "owner", owner, "marker", marker, "paused", current)
					}
					return nil
				}
				delete(annotations, batchPauseOwnerAnnotation)
				if len(annotations) == 0 {
					annotations = nil
				}
				mcp.SetAnnotations(annotations)
				if err := unstructured.SetNestedField(mcp.Object, false, "spec", "paused"); err != nil {
					return fmt.Errorf("setting MachineConfigPool %q paused=false: %w", pool, err)
				}
			}
		}

		// Patch first: logging before a conflict retry would claim a change that
		// never landed. After success, record the flip so orphan/delete/grace
		// resumes leave an audit trail when a pool is stuck paused.
		if err := r.Patch(ctx, mcp, client.MergeFromWithOptions(before, client.MergeFromWithOptimisticLock{})); err != nil {
			return fmt.Errorf("patching MachineConfigPool %q paused=%v: %w", pool, paused, err)
		}
		log.FromContext(ctx).Info("changed MachineConfigPool pause state", "pool", pool, "paused", paused, "owner", owner)
		return nil
	})
}

func (r *ClusterBaselineReconciler) ensureBatchMetadata(
	ctx context.Context, cb *baselinev1alpha1.ClusterBaseline, pools []string,
	requested, kept []string,
) (metav1.Time, error) {
	desiredPools := strings.Join(uniqueSortedStrings(pools), ",")
	var started metav1.Time
	// RetryOnConflict: a concurrent console patch (waiver, schedule, rescan)
	// must not abort batch start after validation; without a stable
	// batch-started-at the grace clock can reset across attempts.
	err := retry.RetryOnConflict(retry.DefaultRetry, func() error {
		// Re-read on every attempt so the ResourceVersion and any annotations
		// written by a racing client are current before we merge ours.
		latest := &baselinev1alpha1.ClusterBaseline{}
		if err := r.Get(ctx, types.NamespacedName{Name: cb.Name}, latest); err != nil {
			return err
		}
		annotations := maps.Clone(latest.GetAnnotations())
		if annotations == nil {
			annotations = map[string]string{}
		}
		changed := false
		started = metav1.Time{}
		if raw := annotations[batchStartedAtAnnotation]; raw != "" {
			if parsed, err := time.Parse(time.RFC3339Nano, raw); err == nil && !parsed.IsZero() {
				started = metav1.NewTime(parsed)
			} else {
				// Corrupt clock must not reset to "now" (that extends grace and
				// can leave MachineConfigPools paused another full window).
				// Fail closed with a stable past epoch so batchPastGrace fires.
				started = metav1.NewTime(time.Unix(1, 0).UTC())
				annotations[batchStartedAtAnnotation] = started.Format(time.RFC3339Nano)
				changed = true
				// Info: early grace-forced resume is surprising without this
				// marker (hand-edit / truncated annotation).
				log.FromContext(ctx).Info("corrupt batch-started-at; failing closed past grace",
					"name", cb.Name, "raw", raw)
			}
		}
		if started.IsZero() {
			started = metav1.Now()
			annotations[batchStartedAtAnnotation] = started.UTC().Format(time.RFC3339Nano)
			changed = true
		}
		if annotations[batchPoolsAnnotation] != desiredPools {
			annotations[batchPoolsAnnotation] = desiredPools
			changed = true
		}
		// A partial batch (NotFound targets dropped at open) must rewrite the
		// one-shot request to the surviving set: clearBatchAnnotations finish-
		// matches on the persisted batch.Remediations (kept), so an annotation
		// still holding the original CSV would never clear, reopening the batch
		// every reconcile and thrashing MachineConfigPool pause/resume. Guard on
		// the request still equaling what we opened with so a concurrent console
		// resubmit (different names) is preserved, not clobbered.
		if len(kept) < len(requested) {
			if cur, ok := annotations[batchApplyAnnotation]; ok &&
				slices.Equal(batchRemediationNames(cur), uniqueSortedStrings(requested)) {
				if want := strings.Join(uniqueSortedStrings(kept), ","); cur != want {
					annotations[batchApplyAnnotation] = want
					changed = true
				}
			}
		}
		if !changed {
			// Keep the in-memory CR aligned even when nothing was written.
			cb.SetAnnotations(annotations)
			cb.SetResourceVersion(latest.GetResourceVersion())
			return nil
		}
		// Update latest (not cb): cb.Spec is from the start of reconcile and would
		// clobber a concurrent console patch (schedule, waiver, profiles) that
		// landed in latest while we only meant to persist batch annotations.
		latest.SetAnnotations(annotations)
		if err := r.Update(ctx, latest); err != nil {
			return err
		}
		// Later batch steps and Status().Update reuse this object and need the
		// batch annotations + fresh RV.
		cb.SetAnnotations(annotations)
		cb.SetResourceVersion(latest.GetResourceVersion())
		return nil
	})
	if err != nil {
		return metav1.Time{}, fmt.Errorf("persisting batch metadata for ClusterBaseline %q: %w", cb.Name, err)
	}
	return started, nil
}

// resumeOrphanedBatch handles the crash/cancel window where metadata was
// persisted and MCPs may be paused, but status.remediationBatch is absent and
// the request annotation was removed.
func (r *ClusterBaselineReconciler) resumeOrphanedBatch(
	ctx context.Context, cb *baselinev1alpha1.ClusterBaseline,
) error {
	annotations := cb.GetAnnotations()
	requestValue, hasRequest := annotations[batchApplyAnnotation]
	emptyRequest := hasRequest && len(batchRemediationNames(requestValue)) == 0
	if annotations[batchStartedAtAnnotation] == "" && annotations[batchPoolsAnnotation] == "" && !emptyRequest {
		return nil
	}
	pools := batchRemediationNames(annotations[batchPoolsAnnotation])
	// Info: crash/cancel recovery unpauses MCPs with no active batch status.
	// Without this marker, on-call cannot tell orphan resume from a normal finish.
	log.FromContext(ctx).Info("resuming orphaned remediation batch",
		"pools", pools, "emptyRequest", emptyRequest, "name", cb.Name)
	owner := batchPauseOwner(cb)
	for _, pool := range pools {
		if err := r.setMCPPaused(ctx, pool, false, owner); err != nil {
			return fmt.Errorf("resuming orphaned batch pool %q: %w", pool, err)
		}
	}
	// RetryOnConflict: a concurrent console patch (waiver/schedule/rescan) must
	// not leave recovery annotations stuck after pools are already unpaused.
	// Clone + re-Get so we never mutate a shared annotation map in place.
	if err := retry.RetryOnConflict(retry.DefaultRetry, func() error {
		latest := &baselinev1alpha1.ClusterBaseline{}
		if err := r.Get(ctx, types.NamespacedName{Name: cb.Name}, latest); err != nil {
			return err
		}
		ann := maps.Clone(latest.GetAnnotations())
		if ann == nil {
			cb.SetAnnotations(nil)
			cb.SetResourceVersion(latest.GetResourceVersion())
			return nil
		}
		reqVal, hasReq := ann[batchApplyAnnotation]
		emptyReq := hasReq && len(batchRemediationNames(reqVal)) == 0
		if ann[batchStartedAtAnnotation] == "" && ann[batchPoolsAnnotation] == "" && !emptyReq {
			cb.SetAnnotations(ann)
			cb.SetResourceVersion(latest.GetResourceVersion())
			return nil
		}
		delete(ann, batchStartedAtAnnotation)
		delete(ann, batchPoolsAnnotation)
		if emptyReq {
			delete(ann, batchApplyAnnotation)
		}
		if len(ann) == 0 {
			ann = nil
		}
		latest.SetAnnotations(ann)
		if err := r.Update(ctx, latest); err != nil {
			return err
		}
		cb.SetAnnotations(ann)
		cb.SetResourceVersion(latest.GetResourceVersion())
		return nil
	}); err != nil {
		return fmt.Errorf("clearing orphaned batch annotations for ClusterBaseline %q: %w", cb.Name, err)
	}
	return nil
}

// clearBatchAnnotations drops the one-shot request and/or recovery annotations
// with RetryOnConflict so a concurrent console patch cannot stick them after
// pools are already safe. When requestMatch is non-nil, batch-apply is cleared
// only if its CSV matches those remediation names (finish path). A nil match
// clears any present request (skip-empty path). Keeps cb annotations + RV aligned.
func (r *ClusterBaselineReconciler) clearBatchAnnotations(
	ctx context.Context, cb *baselinev1alpha1.ClusterBaseline,
	clearRequest bool, requestMatch []string, clearRecovery bool,
) error {
	if err := retry.RetryOnConflict(retry.DefaultRetry, func() error {
		latest := &baselinev1alpha1.ClusterBaseline{}
		if err := r.Get(ctx, types.NamespacedName{Name: cb.Name}, latest); err != nil {
			return err
		}
		ann := maps.Clone(latest.GetAnnotations())
		if ann == nil {
			cb.SetAnnotations(nil)
			cb.SetResourceVersion(latest.GetResourceVersion())
			return nil
		}
		changed := false
		if clearRequest {
			if value, ok := ann[batchApplyAnnotation]; ok {
				if requestMatch == nil ||
					slices.Equal(batchRemediationNames(value), uniqueSortedStrings(requestMatch)) {
					delete(ann, batchApplyAnnotation)
					changed = true
				}
			}
		}
		if clearRecovery {
			if _, ok := ann[batchStartedAtAnnotation]; ok {
				delete(ann, batchStartedAtAnnotation)
				changed = true
			}
			if _, ok := ann[batchPoolsAnnotation]; ok {
				delete(ann, batchPoolsAnnotation)
				changed = true
			}
		}
		cb.SetResourceVersion(latest.GetResourceVersion())
		if !changed {
			cb.SetAnnotations(ann)
			return nil
		}
		if len(ann) == 0 {
			ann = nil
		}
		latest.SetAnnotations(ann)
		if err := r.Update(ctx, latest); err != nil {
			return err
		}
		cb.SetAnnotations(ann)
		cb.SetResourceVersion(latest.GetResourceVersion())
		return nil
	}); err != nil {
		return fmt.Errorf("clearing batch annotations for ClusterBaseline %q: %w", cb.Name, err)
	}
	return nil
}

// remediationOwnedByBaseline is true when rem's suite label is in suites
// (prebuilt via ownedSuites). Callers must build the map once per batch of
// targets: rebuilding ownedSuites per Get was O(profiles) × N remediations.
func remediationOwnedByBaseline(suites map[string]bool, rem *unstructured.Unstructured) bool {
	// Single-key label read: avoid GetLabels full-map copy on each batch target.
	return suites[unstructuredLabel(rem.Object, suiteLabel)]
}

// Permanent batch-start rejects: the request cannot succeed without an external
// change (profiles/deps). Callers clear the one-shot annotation instead of
// sticky-Degrading every reconcile. Transient API errors stay plain errors.
var (
	errBatchForeignSuite = fmt.Errorf("does not belong to a selected baseline suite")
	errBatchMissingDeps  = fmt.Errorf("has missing dependencies")
)

// isPermanentBatchTargetReject is true for foreign-suite and MissingDependencies
// validation failures from getBatchRemediation (not transient Get failures).
func isPermanentBatchTargetReject(err error) bool {
	return err != nil && (errors.Is(err, errBatchForeignSuite) || errors.Is(err, errBatchMissingDeps))
}

// getBatchRemediation validates the confused-deputy boundary before the
// operator uses its stronger service-account permissions to apply a request.
// NotFound returns (nil, nil) so a race-deleted remediation can be skipped.
// NoMatch (CRDs absent) returns the error so batch start retries rather than
// pretending every target was missing and clearing the request annotation.
// suites is the prebuilt ownedSuites map (build once per batch of Gets).
func (r *ClusterBaselineReconciler) getBatchRemediation(
	ctx context.Context, name string, suites map[string]bool,
) (*unstructured.Unstructured, error) {
	rem := u(remediationGVK)
	if err := r.Get(ctx, types.NamespacedName{Namespace: complianceNamespace, Name: name}, rem); err != nil {
		if apierrors.IsNotFound(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("getting remediation %q: %w", name, err)
	}
	if !remediationOwnedByBaseline(suites, rem) {
		return nil, fmt.Errorf("remediation %q: %w", name, errBatchForeignSuite)
	}
	state, _, err := unstructured.NestedString(rem.Object, "status", "applicationState")
	if err != nil {
		return nil, fmt.Errorf("reading applicationState for remediation %q: %w", name, err)
	}
	if state == "MissingDependencies" {
		return nil, fmt.Errorf("remediation %q: %w", name, errBatchMissingDeps)
	}
	return rem, nil
}

// applyOwnedRemediation sets spec.apply=true on one owned remediation.
// suites is the prebuilt ownedSuites map (build once per batch, not per name).
func (r *ClusterBaselineReconciler) applyOwnedRemediation(
	ctx context.Context, cb *baselinev1alpha1.ClusterBaseline, name string, suites map[string]bool,
) error {
	return retry.RetryOnConflict(retry.DefaultRetry, func() error {
		rem, err := r.getBatchRemediation(ctx, name, suites)
		if err != nil {
			return err
		}
		// Race-deleted after batch validation: skip apply (wait path treats
		// NotFound as done). Log so a missing apply is visible in operator logs.
		if rem == nil {
			log.FromContext(ctx).Info("batch apply skipped: remediation not found",
				"remediation", name, "baseline", cb.Name)
			return nil
		}
		apply, _, err := unstructured.NestedBool(rem.Object, "spec", "apply")
		if err != nil {
			return fmt.Errorf("reading spec.apply for remediation %q: %w", name, err)
		}
		if apply {
			return nil
		}
		before := rem.DeepCopy()
		if err := unstructured.SetNestedField(rem.Object, true, "spec", "apply"); err != nil {
			return fmt.Errorf("setting spec.apply on remediation %q: %w", name, err)
		}
		if err := r.Patch(ctx, rem, client.MergeFromWithOptions(before, client.MergeFromWithOptimisticLock{})); err != nil {
			return fmt.Errorf("patching remediation %q apply=true: %w", name, err)
		}
		return nil
	})
}

// resumeBatchPoolsOnDelete unpauses every MachineConfigPool a remediation batch
// may have paused. Prefer status.remediationBatch.pools; if status was lost but
// the batch-apply annotation remains, re-resolve pools from those remediations.
// Best-effort per pool: NotFound/NoMatch ignored; other errors fail deletion so
// we retry rather than drop the finalizer with pools still paused.
func (r *ClusterBaselineReconciler) resumeBatchPoolsOnDelete(ctx context.Context, cb *baselinev1alpha1.ClusterBaseline) error {
	pools := map[string]bool{}
	owner := ""
	if batch := cb.Status.RemediationBatch; batch != nil {
		owner = batch.PauseOwner
		for _, p := range batch.Pools {
			if p != "" {
				pools[p] = true
			}
		}
	}
	// Status lost but annotation still present: rediscover pools from rem names.
	if len(pools) == 0 && cb.Annotations != nil {
		owner = batchPauseOwner(cb)
		for _, pool := range batchRemediationNames(cb.Annotations[batchPoolsAnnotation]) {
			pools[pool] = true
		}
		// One suite set for the whole recovery list (up to batchMaxRemediations).
		suites := ownedSuites(cb)
		names := batchRemediationNames(cb.Annotations[batchApplyAnnotation])
		// Best-effort recovery must never wedge the CR's own deletion. Cap the
		// Gets (an oversized annotation would otherwise fire thousands serially in
		// the finalizer) and skip names that are not DNS-1123 subdomains: a Get
		// with an invalid name can return 400 (not 404), which would fall through
		// to the error return below and block finalizer removal. The apply path
		// rejects these outright; here we skip and keep resuming what we can.
		if len(names) > batchMaxRemediations {
			log.FromContext(ctx).Info("batch-apply annotation exceeds max during pool recovery; capping",
				"count", len(names), "max", batchMaxRemediations, "baseline", cb.Name)
			names = names[:batchMaxRemediations]
		}
		for _, name := range names {
			if len(utilvalidation.IsDNS1123Subdomain(name)) > 0 {
				log.FromContext(ctx).Info("skipping invalid remediation name while recovering batch pools",
					"remediation", name, "baseline", cb.Name)
				continue
			}
			rem := u(remediationGVK)
			if err := r.Get(ctx, types.NamespacedName{Namespace: complianceNamespace, Name: name}, rem); err != nil {
				if apierrors.IsNotFound(err) || meta.IsNoMatchError(err) {
					// Info: pool rediscovery skipped this name; if batch-pools
					// was also empty, on-call needs to know why no MCP resume ran.
					log.FromContext(ctx).Info("remediation missing while recovering batch pools",
						"remediation", name, "baseline", cb.Name, "notFound", apierrors.IsNotFound(err))
					continue
				}
				return fmt.Errorf("getting remediation %q while recovering batch pools: %w", name, err)
			}
			// A crafted foreign remediation must not make this finalizer mutate its
			// MachineConfigPool through the operator's service account.
			if !remediationOwnedByBaseline(suites, rem) {
				log.FromContext(ctx).Info("skipping foreign remediation while recovering batch pools",
					"remediation", name, "baseline", cb.Name)
				continue
			}
			if p := poolFromRemediation(rem); p != "" {
				pools[p] = true
			}
		}
	}
	for _, p := range slices.Sorted(maps.Keys(pools)) {
		if err := r.setMCPPaused(ctx, p, false, owner); err != nil {
			return fmt.Errorf("resuming MachineConfigPool %q on ClusterBaseline delete: %w", p, err)
		}
	}
	return nil
}

// resumePoolsBestEffort unpauses every pool, logging (not aborting) on each
// failure so one stuck pool does not prevent resuming the rest. Returns true
// when any resume failed, so the caller can record the batch for a forced retry.
func (r *ClusterBaselineReconciler) resumePoolsBestEffort(ctx context.Context, pools []string, owner, msg string) bool {
	logger := log.FromContext(ctx)
	failed := false
	for _, p := range pools {
		if err := r.setMCPPaused(ctx, p, false, owner); err != nil {
			logger.Error(err, msg, "pool", p)
			failed = true
		}
	}
	return failed
}
