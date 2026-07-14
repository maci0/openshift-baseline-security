package controller

import (
	"context"
	"testing"

	appsv1 "k8s.io/api/apps/v1"
	policyv1 "k8s.io/api/policy/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

// infra builds the cluster Infrastructure singleton with the given topology.
// topology == "" omits status.infrastructureTopology entirely (unset field).
func infra(topology string) *unstructured.Unstructured {
	o := &unstructured.Unstructured{}
	o.SetGroupVersionKind(infrastructureGVK)
	o.SetName("cluster")
	if topology != "" {
		_ = unstructured.SetNestedField(o.Object, topology, "status", "infrastructureTopology")
	}
	return o
}

func TestInfrastructureSingleReplica(t *testing.T) {
	scheme := testScheme(t)
	cases := []struct {
		name    string
		objects []client.Object
		want    bool
	}{
		{"single-node", []client.Object{infra("SingleReplica")}, true},
		{"highly-available", []client.Object{infra("HighlyAvailable")}, false},
		{"topology-unset", []client.Object{infra("")}, false},
		{"infra-missing", nil, false}, // fail safe to HA when unreadable
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := &ClusterBaselineReconciler{
				Client: fake.NewClientBuilder().WithScheme(scheme).WithObjects(tc.objects...).Build(),
				Scheme: scheme,
			}
			if got := r.infrastructureSingleReplica(context.Background()); got != tc.want {
				t.Fatalf("infrastructureSingleReplica = %v, want %v", got, tc.want)
			}
		})
	}
}

// On SNO the plugin must ship one replica and no PDB, else a minAvailable=1 PDB
// refuses eviction of the last pod and deadlocks the single node's drain.
func TestEnsureConsolePluginSingleNodeDropsPDB(t *testing.T) {
	t.Setenv("RELATED_IMAGE_CONSOLE_PLUGIN", "example.test/plugin:test")
	scheme := testScheme(t)
	cb := newCB()
	// Seed a stale HA PDB to prove the SNO path deletes it, not just skips create.
	stalePDB := &policyv1.PodDisruptionBudget{ObjectMeta: metav1.ObjectMeta{Name: pluginName, Namespace: pluginNS}}
	r := &ClusterBaselineReconciler{
		Client: fake.NewClientBuilder().WithScheme(scheme).
			WithObjects(infra("SingleReplica"), stalePDB).Build(),
		Scheme: scheme,
	}
	if err := r.ensureConsolePlugin(context.Background(), cb); err != nil {
		t.Fatalf("ensureConsolePlugin: %v", err)
	}
	dep := &appsv1.Deployment{}
	if err := r.Get(context.Background(), types.NamespacedName{Name: pluginName, Namespace: pluginNS}, dep); err != nil {
		t.Fatalf("get Deployment: %v", err)
	}
	if dep.Spec.Replicas == nil || *dep.Spec.Replicas != 1 {
		t.Fatalf("SNO replicas = %v, want 1", dep.Spec.Replicas)
	}
	pdb := &policyv1.PodDisruptionBudget{}
	err := r.Get(context.Background(), types.NamespacedName{Name: pluginName, Namespace: pluginNS}, pdb)
	if !apierrors.IsNotFound(err) {
		t.Fatalf("SNO PDB should be absent, got err=%v", err)
	}
}

// On a multi-node cluster the plugin keeps two replicas plus the guarding PDB.
func TestEnsureConsolePluginHAKeepsPDB(t *testing.T) {
	t.Setenv("RELATED_IMAGE_CONSOLE_PLUGIN", "example.test/plugin:test")
	scheme := testScheme(t)
	cb := newCB()
	r := &ClusterBaselineReconciler{
		Client: fake.NewClientBuilder().WithScheme(scheme).
			WithObjects(infra("HighlyAvailable")).Build(),
		Scheme: scheme,
	}
	if err := r.ensureConsolePlugin(context.Background(), cb); err != nil {
		t.Fatalf("ensureConsolePlugin: %v", err)
	}
	dep := &appsv1.Deployment{}
	if err := r.Get(context.Background(), types.NamespacedName{Name: pluginName, Namespace: pluginNS}, dep); err != nil {
		t.Fatalf("get Deployment: %v", err)
	}
	if dep.Spec.Replicas == nil || *dep.Spec.Replicas != pluginReplicas {
		t.Fatalf("HA replicas = %v, want %d", dep.Spec.Replicas, pluginReplicas)
	}
	pdb := &policyv1.PodDisruptionBudget{}
	if err := r.Get(context.Background(), types.NamespacedName{Name: pluginName, Namespace: pluginNS}, pdb); err != nil {
		t.Fatalf("HA PDB should exist: %v", err)
	}
	if pdb.Spec.MinAvailable == nil || pdb.Spec.MinAvailable.IntValue() != int(pluginReadyMin) {
		t.Fatalf("HA PDB minAvailable = %v, want %d", pdb.Spec.MinAvailable, pluginReadyMin)
	}
}
