package controller

import (
	"reflect"
	"testing"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	policyv1 "k8s.io/api/policy/v1"
	"sigs.k8s.io/controller-runtime/pkg/cache"
	"sigs.k8s.io/controller-runtime/pkg/client"

	baselinev1alpha1 "github.com/maci0/baseline-security-operator/api/v1alpha1"
)

// TestManagerCacheOptionsScoped pins the informer cache to the namespaces the
// reconciler reads. A regression here is silent: the cache still works, it just
// holds every Deployment/Service/PDB/PVC in the cluster in the operator's heap
// for the life of the process, so assert the scope rather than the reads.
func TestManagerCacheOptionsScoped(t *testing.T) {
	byObject := ManagerCacheOptions().ByObject
	if len(byObject) == 0 {
		t.Fatal("ByObject is empty: the cache fell back to cluster-wide per type")
	}
	for obj, cfg := range byObject {
		if obj.GetNamespace() != "" {
			t.Errorf("ByObject key %T carries namespace %q; keys are types, not instances",
				obj, obj.GetNamespace())
		}
		if len(cfg.Namespaces) != 1 {
			t.Errorf("%T: cached in %d namespaces, want exactly 1", obj, len(cfg.Namespaces))
			continue
		}
		for got := range cfg.Namespaces {
			if got != pluginNS && got != complianceNamespace {
				t.Errorf("%T: cached namespace %q is neither the plugin namespace %q nor %q",
					obj, got, pluginNS, complianceNamespace)
			}
		}
	}
}

// cacheScopeFor resolves a ByObject entry by type. The map is keyed by object
// pointers, so a pointer built here never matches a pointer built there;
// controller-runtime itself resolves the entry by reflect.Type, and so must any
// test that asserts on it.
func cacheScopeFor(byObject map[client.Object]cache.ByObject, want client.Object) (cache.ByObject, bool) {
	t := reflect.TypeOf(want)
	for obj, cfg := range byObject {
		if reflect.TypeOf(obj) == t {
			return cfg, true
		}
	}
	return cache.ByObject{}, false
}

// TestManagerCacheOptionsExpectedTypes fails when a new typed Get/List appears
// that ManagerCacheOptions does not account for. A type missing from the map
// is still correct, but it caches cluster-wide, which is exactly the cost this
// option exists to remove.
func TestManagerCacheOptionsExpectedTypes(t *testing.T) {
	byObject := ManagerCacheOptions().ByObject
	for _, tc := range []struct {
		name string
		obj  client.Object
		ns   string
	}{
		{"Deployment", &appsv1.Deployment{}, pluginNS},
		{"Service", &corev1.Service{}, pluginNS},
		{"PodDisruptionBudget", &policyv1.PodDisruptionBudget{}, pluginNS},
		{"PersistentVolumeClaim", &corev1.PersistentVolumeClaim{}, complianceNamespace},
	} {
		cfg, ok := cacheScopeFor(byObject, tc.obj)
		if !ok {
			t.Errorf("%s missing from ManagerCacheOptions: it will cache cluster-wide", tc.name)
			continue
		}
		if _, ok := cfg.Namespaces[tc.ns]; !ok {
			t.Errorf("%s not scoped to %s (got %v)", tc.name, tc.ns, cfg.Namespaces)
		}
	}
	// ClusterBaseline is the one cluster-scoped cached type; scoping it is
	// impossible and leaving it out is what makes it stay cache-wide.
	if _, ok := cacheScopeFor(byObject, &baselinev1alpha1.ClusterBaseline{}); ok {
		t.Error("ClusterBaseline is cluster-scoped and must not be namespace-scoped")
	}
}

// TestManagerCacheOptionsDefaultNamespaces pins the default scope for namespaced
// types with no ByObject entry, which is every compliance CR lazyComplianceWatch
// watches as a metadata-only informer. Those informers cannot be named in
// ByObject (a bare *metav1.PartialObjectMetadata has no resolvable GVK), so
// without DefaultNamespaces they list and watch every namespace in the cluster
// and the reconciler's foreign-namespace filter in enqueueSingleton is the only
// thing standing between a scan and a heap full of unrelated compliance objects.
func TestManagerCacheOptionsDefaultNamespaces(t *testing.T) {
	opts := ManagerCacheOptions()
	if len(opts.DefaultNamespaces) != 2 {
		t.Fatalf("DefaultNamespaces = %v, want exactly %q and %q",
			opts.DefaultNamespaces, complianceNamespace, pluginNS)
	}
	for ns := range opts.DefaultNamespaces {
		if ns != complianceNamespace && ns != pluginNS {
			t.Errorf("DefaultNamespaces caches %q, which the reconciler never reads", ns)
		}
	}
	// A catch-all re-admits the named namespaces through a field selector, so
	// the compliance informers would be back to watching everything.
	if _, ok := opts.DefaultNamespaces[cache.AllNamespaces]; ok {
		t.Error("DefaultNamespaces must not carry the cache.AllNamespaces catch-all")
	}
}
