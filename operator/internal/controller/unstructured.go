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
