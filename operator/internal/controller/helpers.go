package controller

import (
	"context"
	"fmt"
	"maps"
	"slices"
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	"sigs.k8s.io/controller-runtime/pkg/client"

	baselinev1alpha1 "github.com/maci0/baseline-security-operator/api/v1alpha1"
)

// requeueAfterAt picks the poll cadence. Steady state is 1m; any Progressing
// rollup and an in-flight remediation batch use 15s so cancel/grace/Applied are
// not stuck behind a full minute when the dynamic informer is lagging or not yet up.
// Active waiver expiry also shortens the poll so accepted-risk drops from the
// score without waiting for the full steady interval (ADR-005). The caller
// passes its clock reading, so the cadence is a function of reconciled state
// and the injected clock, not of when the reconcile happened to run.
func requeueAfterAt(cb *baselinev1alpha1.ClusterBaseline, now time.Time) time.Duration {
	const fast = 15 * time.Second
	const slow = time.Minute
	d := slow
	progressing := meta.FindStatusCondition(cb.Status.Conditions, "Progressing")
	if condIsTrue(progressing) || cb.Status.RemediationBatch != nil {
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

// nextPageToken advances a paged List loop and reports whether another page
// exists. The apiserver is expected to hand back an empty token on the last
// page, but a token identical to the one just used would make the next List
// replay the same page: the loop would spin on one page until
// reconcileTimeout and the reconcile would fail with a deadline instead of the
// real cause. Stop with what has been collected; the unread remainder takes
// the same path as any other partial read.
func nextPageToken(next, used string) (string, bool) {
	if next == "" || next == used {
		return "", false
	}
	return next, true
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
//
// The list declares ownership, so it must track what the reconciler actually
// holds. With console.managementState=Removed, removeConsolePlugin deletes the
// plugin Service, Deployment, PDB, and ConsolePlugin, so advertising them would
// send must-gather after objects the operator has disowned.
//
// The PDB is advertised even on a SingleReplica cluster, where ensureConsolePlugin
// deletes it: the topology read fails safe to HA, so "PDB present" is the state
// the reconciler can confirm from the CR alone, and a must-gather that finds it
// absent is the signal that topology was read as HA. The Service is listed
// because it is the only object carrying the serving-cert annotation, so it is
// what explains the baseline-security-console-plugin-cert Secret in a gather.
func relatedObjectsFromSuites(cb *baselinev1alpha1.ClusterBaseline, suites map[string]bool) []baselinev1alpha1.ObjectRef {
	// Cap at fixed refs + suite count so the slice does not thrash under multi-profile.
	refs := make([]baselinev1alpha1.ObjectRef, 0, 5+len(suites))
	refs = append(refs,
		baselinev1alpha1.ObjectRef{Group: "compliance.openshift.io", Resource: "scansettings", Name: scanSettingName, Namespace: complianceNamespace},
	)
	if cb.Spec.Console.ManagementState != baselinev1alpha1.Removed {
		refs = append(refs,
			baselinev1alpha1.ObjectRef{Group: "apps", Resource: "deployments", Name: pluginName, Namespace: pluginNS},
			baselinev1alpha1.ObjectRef{Group: "", Resource: "services", Name: pluginName, Namespace: pluginNS},
			baselinev1alpha1.ObjectRef{Group: "policy", Resource: "poddisruptionbudgets", Name: pluginName, Namespace: pluginNS},
			baselinev1alpha1.ObjectRef{Group: "console.openshift.io", Resource: "consoleplugins", Name: pluginName},
		)
	}
	// Deterministic order so status does not flap.
	for _, name := range slices.Sorted(maps.Keys(suites)) {
		refs = append(refs, baselinev1alpha1.ObjectRef{
			Group: "compliance.openshift.io", Resource: "scansettingbindings", Name: name, Namespace: complianceNamespace,
		})
	}
	return refs
}
