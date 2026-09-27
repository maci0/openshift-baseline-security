package controller

import (
	"context"
	"errors"
	"strings"
	"testing"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"
)

func TestCreateIfMissing(t *testing.T) {
	scheme := testScheme(t)
	c := fake.NewClientBuilder().WithScheme(scheme).Build()
	ns := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: "create-if-missing-test"}}
	if err := createIfMissing(context.Background(), c, ns); err != nil {
		t.Fatal(err)
	}
	got := &corev1.Namespace{}
	if err := c.Get(context.Background(), types.NamespacedName{Name: ns.Name}, got); err != nil {
		t.Fatal(err)
	}
	again := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: ns.Name}}
	if err := createIfMissing(context.Background(), c, again); err != nil {
		t.Fatal("AlreadyExists should be ignored:", err)
	}
}

// TestCreateIfMissingErrorIdentity: non-AlreadyExists Create failures must name
// the object so on-call can tell namespace vs Subscription vs OperatorGroup.
func TestCreateIfMissingErrorIdentity(t *testing.T) {
	scheme := testScheme(t)
	deny := errors.New("injected create denial")
	c := fake.NewClientBuilder().WithScheme(scheme).
		WithInterceptorFuncs(interceptor.Funcs{
			Create: func(ctx context.Context, c client.WithWatch, obj client.Object, opts ...client.CreateOption) error {
				return deny
			},
		}).Build()

	// Cluster-scoped: "creating <name>: …"
	ns := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: "target-ns"}}
	err := createIfMissing(context.Background(), c, ns)
	if err == nil {
		t.Fatal("expected create error")
	}
	if got := err.Error(); !strings.Contains(got, "creating target-ns:") || !strings.Contains(got, deny.Error()) {
		t.Fatalf("cluster-scoped wrap = %q, want creating target-ns: …", got)
	}

	// Namespaced: "creating <ns>/<name>: …"
	cm := &corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{Name: "cfg", Namespace: "openshift-compliance"}}
	err = createIfMissing(context.Background(), c, cm)
	if err == nil {
		t.Fatal("expected create error")
	}
	if got := err.Error(); !strings.Contains(got, "creating openshift-compliance/cfg:") {
		t.Fatalf("namespaced wrap = %q, want creating openshift-compliance/cfg: …", got)
	}

	// AlreadyExists is still swallowed (identity wrap must not reclassify it).
	exists := fake.NewClientBuilder().WithScheme(scheme).
		WithInterceptorFuncs(interceptor.Funcs{
			Create: func(ctx context.Context, c client.WithWatch, obj client.Object, opts ...client.CreateOption) error {
				return apierrors.NewAlreadyExists(schema.GroupResource{Resource: "namespaces"}, "x")
			},
		}).Build()
	if err := createIfMissing(context.Background(), exists, ns); err != nil {
		t.Fatal("AlreadyExists must be ignored:", err)
	}
}
