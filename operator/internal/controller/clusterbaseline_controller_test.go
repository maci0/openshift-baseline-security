package controller

import (
	"context"
	"testing"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	baselinev1alpha1 "github.com/openshift-baseline-security/baseline-security-operator/api/v1alpha1"
)

func checkResult(name, scan, status string) *unstructured.Unstructured {
	u := &unstructured.Unstructured{}
	u.SetGroupVersionKind(checkResultGVK)
	u.SetName(name)
	u.SetNamespace(complianceNamespace)
	u.SetLabels(map[string]string{"compliance.openshift.io/scan-name": scan})
	u.Object["status"] = status
	return u
}

func TestAggregateStatus(t *testing.T) {
	scheme := runtime.NewScheme()
	if err := clientgoscheme.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}
	if err := baselinev1alpha1.AddToScheme(scheme); err != nil {
		t.Fatal(err)
	}
	list := &unstructured.UnstructuredList{}
	list.SetGroupVersionKind(checkResultGVK.GroupVersion().WithKind(checkResultGVK.Kind + "List"))
	scheme.AddKnownTypeWithName(checkResultGVK, &unstructured.Unstructured{})
	scheme.AddKnownTypeWithName(list.GroupVersionKind(), list)

	r := &ClusterBaselineReconciler{
		Client: fake.NewClientBuilder().WithScheme(scheme).WithObjects(
			checkResult("a", "ocp4-cis", "PASS"),
			checkResult("b", "ocp4-cis", "PASS"),
			checkResult("c", "ocp4-cis-node-master", "FAIL"), // role-suffixed scan name
			checkResult("d", "ocp4-cis", "MANUAL"),
			checkResult("e", "unrelated-scan", "FAIL"), // not ours, ignored
		).Build(),
		Scheme: scheme,
	}

	cb := &baselinev1alpha1.ClusterBaseline{
		Spec: baselinev1alpha1.ClusterBaselineSpec{Profiles: []baselinev1alpha1.ProfileKey{"cis"}},
	}
	if err := r.aggregateStatus(context.Background(), cb); err != nil {
		t.Fatal(err)
	}

	if cb.Status.Score == nil || *cb.Status.Score != 66 {
		t.Fatalf("score = %v, want 66", cb.Status.Score)
	}
	p := cb.Status.Profiles[0]
	if p.Pass != 2 || p.Fail != 1 || p.Manual != 1 {
		t.Fatalf("profile counts = %+v, want pass=2 fail=1 manual=1", p)
	}
}
