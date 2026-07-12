package controller

import (
	"context"
	"fmt"
	"maps"
	"slices"
	"time"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"sigs.k8s.io/controller-runtime/pkg/client"

	baselinev1alpha1 "github.com/maci0/baseline-security-operator/api/v1alpha1"
)

// requeueAfter picks the poll cadence. Steady state is 1m; any Progressing
// rollup and an in-flight remediation batch use 15s so cancel/grace/Applied are
// not stuck behind a full minute when the dynamic informer is lagging or not yet up.
func requeueAfter(cb *baselinev1alpha1.ClusterBaseline) time.Duration {
	const fast = 15 * time.Second
	const slow = time.Minute
	if progressing := meta.FindStatusCondition(cb.Status.Conditions, "Progressing"); progressing != nil && progressing.Status == metav1.ConditionTrue {
		return fast
	}
	if cb.Status.RemediationBatch != nil {
		return fast
	}
	return slow
}

// preferredHostnameAntiAffinity spreads pods across nodes (CONVENTIONS.md HA).
func preferredHostnameAntiAffinity(labels map[string]string) *corev1.Affinity {
	return &corev1.Affinity{
		PodAntiAffinity: &corev1.PodAntiAffinity{
			PreferredDuringSchedulingIgnoredDuringExecution: []corev1.WeightedPodAffinityTerm{{
				Weight: 100,
				PodAffinityTerm: corev1.PodAffinityTerm{
					LabelSelector: &metav1.LabelSelector{MatchLabels: labels},
					TopologyKey:   "kubernetes.io/hostname",
				},
			}},
		},
	}
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

func u(gvk schema.GroupVersionKind) *unstructured.Unstructured {
	o := &unstructured.Unstructured{}
	o.SetGroupVersionKind(gvk)
	return o
}

func uList(gvk schema.GroupVersionKind) *unstructured.UnstructuredList {
	l := &unstructured.UnstructuredList{}
	l.SetGroupVersionKind(gvk.GroupVersion().WithKind(gvk.Kind + "List"))
	return l
}

// stringMapValue reads key from a JSON-decoded map (map[string]any) or a
// typed map[string]string without allocating a full copy.
func stringMapValue(m any, key string) string {
	switch labels := m.(type) {
	case map[string]any:
		s, _ := labels[key].(string)
		return s
	case map[string]string:
		return labels[key]
	default:
		return ""
	}
}

// unstructuredMeta returns the metadata map without NestedMap path walks.
// Nil obj or missing/wrong-type metadata yields nil (callers treat as empty).
func unstructuredMeta(obj map[string]any) map[string]any {
	if obj == nil {
		return nil
	}
	meta, _ := obj["metadata"].(map[string]any)
	return meta
}

// unstructuredLabel reads one metadata label. Prefer this over GetLabels() on
// multi-thousand CCR hot paths: GetLabels copies the entire label map.
func unstructuredLabel(obj map[string]any, key string) string {
	meta := unstructuredMeta(obj)
	if meta == nil {
		return ""
	}
	return stringMapValue(meta["labels"], key)
}

// unstructuredAnnotation reads one metadata annotation without GetAnnotations()
// full-map copy (same cost concern as GetLabels).
func unstructuredAnnotation(obj map[string]any, key string) string {
	meta := unstructuredMeta(obj)
	if meta == nil {
		return ""
	}
	return stringMapValue(meta["annotations"], key)
}

// unstructuredName returns metadata.name without NestedString path walks.
func unstructuredName(obj map[string]any) string {
	meta := unstructuredMeta(obj)
	if meta == nil {
		return ""
	}
	s, _ := meta["name"].(string)
	return s
}

// relatedObjects lists the resources this baseline owns or drives, for
// must-gather / support tooling.
func relatedObjects(cb *baselinev1alpha1.ClusterBaseline) []baselinev1alpha1.ObjectRef {
	suites := ownedSuites(cb)
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
