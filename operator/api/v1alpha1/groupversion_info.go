// Package v1alpha1 contains API Schema definitions for the baselinesecurity.openshift.io v1alpha1 API group.
// +kubebuilder:object:generate=true
// +groupName=baselinesecurity.openshift.io
package v1alpha1

import (
	"k8s.io/apimachinery/pkg/runtime/schema"
	"sigs.k8s.io/controller-runtime/pkg/scheme"
)

var (
	// GroupVersion is the baselinesecurity.openshift.io/v1alpha1 API group-version.
	GroupVersion = schema.GroupVersion{Group: "baselinesecurity.openshift.io", Version: "v1alpha1"}

	// SchemeBuilder registers this package's types with GroupVersion.
	SchemeBuilder = &scheme.Builder{GroupVersion: GroupVersion}

	// AddToScheme adds this package's types to a runtime.Scheme.
	AddToScheme = SchemeBuilder.AddToScheme
)
