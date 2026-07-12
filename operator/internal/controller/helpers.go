package controller

import (
	"context"
	"fmt"
	"maps"
	"slices"
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	baselinev1alpha1 "github.com/maci0/baseline-security-operator/api/v1alpha1"
)

// requeueAfter picks the poll cadence. Steady state is 1m; any Progressing
// rollup and an in-flight remediation batch use 15s so cancel/grace/Applied are
// not stuck behind a full minute when the dynamic informer is lagging or not yet up.
// Active waiver expiry also shortens the poll so accepted-risk drops from the
// score without waiting for the full steady interval (ADR-005).
func requeueAfter(cb *baselinev1alpha1.ClusterBaseline) time.Duration {
	return requeueAfterAt(cb, time.Now())
}

// requeueAfterAt is requeueAfter with an injected clock so unit tests can pin
// waiver-expiry shortening without wall-clock lag under load.
func requeueAfterAt(cb *baselinev1alpha1.ClusterBaseline, now time.Time) time.Duration {
	const fast = 15 * time.Second
	const slow = time.Minute
	d := slow
	progressing := meta.FindStatusCondition(cb.Status.Conditions, "Progressing")
	if (progressing != nil && progressing.Status == metav1.ConditionTrue) || cb.Status.RemediationBatch != nil {
		d = fast
	}
	if until := nearestWaiverExpiry(cb, now); until > 0 && until < d {
		// Floor at 1s so clock skew / near-zero expiry cannot hot-loop.
		if until < time.Second {
			return time.Second
		}
		return until
	}
	return d
}

// nearestWaiverExpiry is the duration until the soonest still-active waiver
// expires, or 0 when none. Expired and open-ended entries are ignored.
func nearestWaiverExpiry(cb *baselinev1alpha1.ClusterBaseline, now time.Time) time.Duration {
	var soonest time.Duration
	for i := range cb.Spec.Waivers {
		exp := cb.Spec.Waivers[i].ExpiresAt
		if exp == nil || !exp.After(now) {
			continue
		}
		d := exp.Sub(now)
		if soonest == 0 || d < soonest {
			soonest = d
		}
	}
	return soonest
}

func createIfMissing(ctx context.Context, c client.Client, obj client.Object) error {
	if err := c.Create(ctx, obj); err != nil && !apierrors.IsAlreadyExists(err) {
		// Identity in the message: callers wrap with step names, but on-call still
		// needs which object Create rejected (namespace vs Subscription vs OG).
		if ns := obj.GetNamespace(); ns != "" {
			return fmt.Errorf("creating %s/%s: %w", ns, obj.GetName(), err)
		}
		return fmt.Errorf("creating %s: %w", obj.GetName(), err)
	}
	return nil
}

// relatedObjectsFromSuites lists the resources this baseline owns or drives
// (must-gather / support tooling) from an already-built owned suite map so
// reconcile does not allocate ownedSuites twice.
func relatedObjectsFromSuites(suites map[string]bool) []baselinev1alpha1.ObjectRef {
	// Cap at fixed refs + suite count so the slice does not thrash under multi-profile.
	refs := make([]baselinev1alpha1.ObjectRef, 0, 4+len(suites))
	refs = append(refs,
		baselinev1alpha1.ObjectRef{Group: "compliance.openshift.io", Resource: "scansettings", Name: scanSettingName, Namespace: complianceNamespace},
		baselinev1alpha1.ObjectRef{Group: "apps", Resource: "deployments", Name: pluginName, Namespace: pluginNS},
		baselinev1alpha1.ObjectRef{Group: "policy", Resource: "poddisruptionbudgets", Name: pluginName, Namespace: pluginNS},
		baselinev1alpha1.ObjectRef{Group: "console.openshift.io", Resource: "consoleplugins", Name: pluginName},
	)
	// Deterministic order so status does not flap.
	for _, name := range slices.Sorted(maps.Keys(suites)) {
		refs = append(refs, baselinev1alpha1.ObjectRef{
			Group: "compliance.openshift.io", Resource: "scansettingbindings", Name: name, Namespace: complianceNamespace,
		})
	}
	return refs
}
