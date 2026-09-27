package controller

import (
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

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

// unstructuredName returns metadata.name without NestedString path walks.
func unstructuredName(obj map[string]any) string {
	meta := unstructuredMeta(obj)
	if meta == nil {
		return ""
	}
	return stringMapValue(meta, "name")
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
