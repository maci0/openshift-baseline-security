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
// complianceNamespace for scan storage. Foreign CRs (CSV, Subscription,
// OperatorGroup, MCP, Console, ConsolePlugin, Infrastructure, ComplianceSuite,
// remediation) are unstructured, so they bypass the cache already and need no
// entry here. A type absent from the map keeps the default cluster-wide cache,
// so adding a typed read elsewhere stays correct without touching this file.
func ManagerCacheOptions() cache.Options {
	inNamespace := func(ns string) cache.ByObject {
		return cache.ByObject{Namespaces: map[string]cache.Config{ns: {}}}
	}
	return cache.Options{
		ByObject: map[client.Object]cache.ByObject{
			&appsv1.Deployment{}:            inNamespace(pluginNS),
			&corev1.Service{}:               inNamespace(pluginNS),
			&policyv1.PodDisruptionBudget{}: inNamespace(pluginNS),
			&corev1.PersistentVolumeClaim{}: inNamespace(complianceNamespace),
		},
	}
}
