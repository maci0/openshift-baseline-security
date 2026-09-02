// Package v1alpha1 contains API Schema definitions for the baselinesecurity.openshift.io v1alpha1 API group.
// +kubebuilder:object:generate=true
// +groupName=baselinesecurity.openshift.io
package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

var (
	// GroupVersion is the baselinesecurity.openshift.io/v1alpha1 API group-version.
	GroupVersion = schema.GroupVersion{Group: "baselinesecurity.openshift.io", Version: "v1alpha1"}

	// SchemeBuilder registers this package's types with GroupVersion.
	SchemeBuilder = runtime.NewSchemeBuilder(addKnownTypes)

	// AddToScheme adds this package's types to a runtime.Scheme.
	AddToScheme = SchemeBuilder.AddToScheme
)

func addKnownTypes(s *runtime.Scheme) error {
	s.AddKnownTypes(GroupVersion, &ClusterBaseline{}, &ClusterBaselineList{})
	metav1.AddToGroupVersion(s, GroupVersion)
	return nil
}
