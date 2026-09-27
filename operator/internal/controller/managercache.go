package controller

import (
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	policyv1 "k8s.io/api/policy/v1"
	"sigs.k8s.io/controller-runtime/pkg/cache"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// ManagerCacheOptions bounds the manager's informer cache to the namespaces the
// reconciler actually reads. Without it the default cache is cluster-wide per
// type, and the first typed read is what starts the informer: the plugin
// Deployment/Service/PDB Gets cache every one of those objects in the cluster,
// and the scan-storage PVC List (checkScanStorage) caches every
// PersistentVolumeClaim in the cluster. PVCs are among the largest objects in
// etcd, so a busy cluster parks thousands of them in the operator's heap for the
// life of the process to answer a question about a handful in
// openshift-compliance.
//
// Only ClusterBaseline is cluster-scoped and it stays cache-wide. Everything
// else is read in exactly one namespace: pluginNS for the console plugin,
// complianceNamespace for scan storage and for the compliance CRs
// lazyComplianceWatch watches. Foreign CRs (CSV, Subscription, OperatorGroup,
// MCP, Console, ConsolePlugin, Infrastructure) are unstructured, so they bypass
// the cache entirely and need no entry here.
func ManagerCacheOptions() cache.Options {
	inNamespace := func(ns string) cache.ByObject {
		return cache.ByObject{Namespaces: map[string]cache.Config{ns: {}}}
	}
	return cache.Options{
		// Default for every namespaced type with no explicit ByObject entry: the
		// compliance CRs lazyComplianceWatch watches as metadata-only informers
		// (ComplianceSuite, ComplianceScan, ComplianceRemediation,
		// ComplianceCheckResult). They cannot be named in ByObject: the key
		// would be a bare *metav1.PartialObjectMetadata, and apiutil.GVKForObject
		// cannot resolve a GVK for one, so cache.New would refuse to start.
		// Left on the default they would cache cluster-wide, holding name,
		// labels, and resourceVersion for every compliance object in every
		// namespace for the life of the process. enqueueSingleton already drops
		// foreign namespaces; this keeps them out of the cache too.
		//
		// No cache.AllNamespaces key: the catch-all re-admits the named
		// namespaces through a field selector, so the informers would come back.
		// A namespaced read added outside these two namespaces then fails loudly
		// on a cache miss instead of quietly widening the cache, and belongs
		// here as a new ByObject entry. Cluster-scoped types are unaffected:
		// a multi-namespace cache routes them to its own cluster-wide cache, so
		// ClusterBaseline keeps working.
		DefaultNamespaces: map[string]cache.Config{
			complianceNamespace: {},
			pluginNS:            {},
		},
		ByObject: map[client.Object]cache.ByObject{
			&appsv1.Deployment{}:            inNamespace(pluginNS),
			&corev1.Service{}:               inNamespace(pluginNS),
			&policyv1.PodDisruptionBudget{}: inNamespace(pluginNS),
			&corev1.PersistentVolumeClaim{}: inNamespace(complianceNamespace),
		},
	}
}
