package controller

import (
	"context"
	"fmt"
	"os"
	"slices"
	"strings"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	policyv1 "k8s.io/api/policy/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/client-go/util/retry"
	"k8s.io/utils/ptr"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"

	baselinev1alpha1 "github.com/maci0/baseline-security-operator/api/v1alpha1"
)

// withoutPlugin returns plugins without name (copy; does not mutate input).
func withoutPlugin(plugins []string, name string) []string {
	return slices.DeleteFunc(slices.Clone(plugins), func(p string) bool { return p == name })
}

// EnvRelatedImageConsolePlugin names the env var carrying the console plugin
// image. Single source of truth so cmd startup logging and the reconciler read
// the same key (a rename here cannot silently drift the two apart).
const EnvRelatedImageConsolePlugin = "RELATED_IMAGE_CONSOLE_PLUGIN"

// relatedImageConsolePlugin is the plugin image the operator deploys. Whitespace
// alone is treated as unset so a mis-set env (padding, empty quotes) does not
// create a Deployment with an unpullable image ref.
func relatedImageConsolePlugin() string {
	return strings.TrimSpace(os.Getenv(EnvRelatedImageConsolePlugin))
}

// ValidRelatedImage rejects refs that cannot be a container image, so a
// mis-set RELATED_IMAGE_CONSOLE_PLUGIN fails with ImageInvalid instead of
// creating a Deployment that ImagePullBackOff forever.
// Deliberately loose: registries with ports, digests, and short names are OK.
// Also used by cmd for startup logging (single source of truth).
func ValidRelatedImage(ref string) bool {
	if ref == "" || len(ref) > 1024 {
		return false
	}
	for _, r := range ref {
		if r <= 0x20 || r == 0x7f {
			return false
		}
	}
	// Shell / URL / path noise that never appears in a legal image reference.
	// Includes % # \ so a mis-set env cannot smuggle encoding or fragments.
	if strings.ContainsAny(ref, "<>|;&$`\\\"'*?[]{}()!%#") {
		return false
	}
	for _, r := range ref {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') {
			return true
		}
	}
	return false
}

// deregisterConsolePlugin drops our entry from consoles.operator.openshift.io/cluster.
// Owned Deployment/Service/ConsolePlugin are GCed via owner refs on CR delete.
func (r *ClusterBaselineReconciler) deregisterConsolePlugin(ctx context.Context) error {
	return retry.RetryOnConflict(retry.DefaultRetry, func() error {
		console := u(consoleGVK)
		if err := r.Get(ctx, types.NamespacedName{Name: "cluster"}, console); err != nil {
			// Console capability disabled (CRD absent) or config gone: nothing to
			// deregister. Must tolerate NoMatch so CR deletion is not wedged.
			if meta.IsNoMatchError(err) {
				return nil
			}
			return client.IgnoreNotFound(err)
		}
		plugins, _, err := unstructured.NestedStringSlice(console.Object, "spec", "plugins")
		if err != nil {
			return fmt.Errorf("reading console plugins: %w", err)
		}
		kept := withoutPlugin(plugins, pluginName)
		if len(kept) == len(plugins) {
			return nil
		}
		if err := unstructured.SetNestedStringSlice(console.Object, kept, "spec", "plugins"); err != nil {
			return fmt.Errorf("removing console plugin registration: %w", err)
		}
		return r.Update(ctx, console)
	})
}

// removeConsolePlugin tears down plugin objects when managementState is Removed.
func (r *ClusterBaselineReconciler) removeConsolePlugin(ctx context.Context, cb *baselinev1alpha1.ClusterBaseline) error {
	cp := u(consolePluginGVK)
	cp.SetName(pluginName)
	// NoMatch: the ConsolePlugin CRD is absent (Console capability disabled),
	// so there is nothing to remove; do not wedge on a permanent Degraded.
	if err := r.Delete(ctx, cp); err != nil && !apierrors.IsNotFound(err) && !meta.IsNoMatchError(err) {
		return fmt.Errorf("deleting ConsolePlugin %s: %w", pluginName, err)
	}
	for _, obj := range []client.Object{
		&appsv1.Deployment{ObjectMeta: metav1.ObjectMeta{Name: pluginName, Namespace: pluginNS}},
		&corev1.Service{ObjectMeta: metav1.ObjectMeta{Name: pluginName, Namespace: pluginNS}},
		&policyv1.PodDisruptionBudget{ObjectMeta: metav1.ObjectMeta{Name: pluginName, Namespace: pluginNS}},
	} {
		if err := r.Delete(ctx, obj); err != nil && !apierrors.IsNotFound(err) {
			return fmt.Errorf("deleting plugin %T %s/%s: %w", obj, obj.GetNamespace(), obj.GetName(), err)
		}
	}
	if err := r.deregisterConsolePlugin(ctx); err != nil {
		return fmt.Errorf("deregistering console plugin: %w", err)
	}
	setCond(cb, "ConsolePluginReady", metav1.ConditionFalse, "Disabled", "")
	return nil
}

func (r *ClusterBaselineReconciler) ensureConsolePlugin(ctx context.Context, cb *baselinev1alpha1.ClusterBaseline) error {
	if cb.Spec.Console.ManagementState == baselinev1alpha1.Removed {
		return r.removeConsolePlugin(ctx, cb)
	}
	image := relatedImageConsolePlugin()
	if image == "" {
		// Soft-fail: still reconcile scans/status; requeue will retry when env is fixed.
		// Does not roll up to Degraded (scanning still works), so log on transition
		// only: without this, a missing RELATED_IMAGE leaves only a CR condition.
		logConsolePluginNotReady(ctx, cb, "ImageMissing", "RELATED_IMAGE_CONSOLE_PLUGIN not set")
		return nil
	}
	if !ValidRelatedImage(image) {
		logConsolePluginNotReady(ctx, cb, "ImageInvalid",
			"RELATED_IMAGE_CONSOLE_PLUGIN is not a valid container image reference")
		return nil
	}
	if err := createIfMissing(ctx, r.Client, &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: pluginNS}}); err != nil {
		return fmt.Errorf("ensuring plugin namespace %s: %w", pluginNS, err)
	}

	// Fresh map per consumer: Service/Deployment/PDB/Affinity must not share one
	// map header (client-go and API machinery may retain object graphs).
	pluginLabels := func() map[string]string { return map[string]string{"app": pluginName} }

	// Service first so service-ca can mint the serving-cert Secret before pods start.
	svc := &corev1.Service{ObjectMeta: metav1.ObjectMeta{Name: pluginName, Namespace: pluginNS}}
	if _, err := controllerutil.CreateOrUpdate(ctx, r.Client, svc, func() error {
		// Replace annotations entirely: hand-injected keys (external-dns, cloud
		// LB controllers, route annotations) must not outlive a reconcile on an
		// internal ClusterIP console backend. Fresh map: do not mutate a map
		// shared with a cached object.
		svc.Annotations = map[string]string{
			"service.beta.openshift.io/serving-cert-secret-name": pluginName + "-cert",
		}
		svc.Spec.Type = corev1.ServiceTypeClusterIP
		// This is an internal console backend. Clear every field that can retain
		// external exposure or is invalid after reconciling a hand-edited
		// LoadBalancer/ExternalName Service back to ClusterIP.
		svc.Spec.ExternalIPs = nil
		svc.Spec.ExternalName = ""
		svc.Spec.LoadBalancerIP = ""
		svc.Spec.LoadBalancerSourceRanges = nil
		svc.Spec.LoadBalancerClass = nil
		svc.Spec.AllocateLoadBalancerNodePorts = nil
		svc.Spec.ExternalTrafficPolicy = ""
		svc.Spec.HealthCheckNodePort = 0
		svc.Spec.PublishNotReadyAddresses = false
		svc.Spec.Selector = pluginLabels()
		svc.Spec.Ports = []corev1.ServicePort{{
			Name: "https", Port: 9443, TargetPort: intstr.FromInt32(9443), Protocol: corev1.ProtocolTCP,
		}}
		return controllerutil.SetControllerReference(cb, svc, r.Scheme)
	}); err != nil {
		return fmt.Errorf("ensuring plugin Service %s/%s: %w", pluginNS, pluginName, err)
	}

	dep := &appsv1.Deployment{ObjectMeta: metav1.ObjectMeta{Name: pluginName, Namespace: pluginNS}}
	if _, err := controllerutil.CreateOrUpdate(ctx, r.Client, dep, func() error {
		// Mutate owned fields only; leave selector immutable after create.
		if dep.Spec.Selector == nil {
			dep.Spec.Selector = &metav1.LabelSelector{MatchLabels: pluginLabels()}
		}
		dep.Spec.Replicas = ptr.To(pluginReplicas)
		// maxUnavailable=1 makes DeploymentAvailable True at 1/2 ready, matching
		// pluginReadyMin=1: a single drained node must not false-Degrade the plugin.
		dep.Spec.Strategy = appsv1.DeploymentStrategy{
			Type: appsv1.RollingUpdateDeploymentStrategyType,
			RollingUpdate: &appsv1.RollingUpdateDeployment{
				MaxUnavailable: ptr.To(intstr.FromInt32(1)),
				MaxSurge:       ptr.To(intstr.FromInt32(1)),
			},
		}
		// Replace labels entirely so foreign labels cannot stick across reconciles
		// (selector matching, NetworkPolicy targeting, admission webhooks).
		dep.Spec.Template.Labels = pluginLabels()
		// Drop hand-injected pod annotations (e.g. AppArmor unconfined, seccomp
		// overrides) that would otherwise survive every CreateOrUpdate.
		dep.Spec.Template.Annotations = nil
		dep.Spec.Template.Spec.SecurityContext = &corev1.PodSecurityContext{
			RunAsNonRoot: ptr.To(true),
			SeccompProfile: &corev1.SeccompProfile{
				Type: corev1.SeccompProfileTypeRuntimeDefault,
			},
		}
		dep.Spec.Template.Spec.Affinity = preferredHostnameAntiAffinity(pluginLabels())
		applyPluginContainer(&dep.Spec.Template.Spec, image)
		return controllerutil.SetControllerReference(cb, dep, r.Scheme)
	}); err != nil {
		return fmt.Errorf("ensuring plugin Deployment %s/%s: %w", pluginNS, pluginName, err)
	}

	// Preferred anti-affinity alone does not block eviction of both pods on drain.
	pdb := &policyv1.PodDisruptionBudget{ObjectMeta: metav1.ObjectMeta{Name: pluginName, Namespace: pluginNS}}
	if _, err := controllerutil.CreateOrUpdate(ctx, r.Client, pdb, func() error {
		pdb.Spec.MinAvailable = ptr.To(intstr.FromInt32(pluginReadyMin))
		pdb.Spec.Selector = &metav1.LabelSelector{MatchLabels: pluginLabels()}
		// Clear maxUnavailable when minAvailable is set (mutually exclusive).
		pdb.Spec.MaxUnavailable = nil
		return controllerutil.SetControllerReference(cb, pdb, r.Scheme)
	}); err != nil {
		return fmt.Errorf("ensuring plugin PodDisruptionBudget %s/%s: %w", pluginNS, pluginName, err)
	}

	cp := u(consolePluginGVK)
	cp.SetName(pluginName)
	if _, err := controllerutil.CreateOrUpdate(ctx, r.Client, cp, func() error {
		// Set only the fields we own (field-wise, not a wholesale spec replace):
		// the ConsolePlugin CRD may server-default sibling spec fields (e.g. i18n)
		// that we do not set, and overwriting the whole spec each reconcile would
		// diff against those defaults into a hot update loop with the console operator.
		if err := unstructured.SetNestedField(cp.Object, "Baseline Security", "spec", "displayName"); err != nil {
			return err
		}
		if err := unstructured.SetNestedField(cp.Object, "Service", "spec", "backend", "type"); err != nil {
			return err
		}
		if err := unstructured.SetNestedField(cp.Object, map[string]any{
			"name": pluginName, "namespace": pluginNS, "port": int64(9443), "basePath": "/",
		}, "spec", "backend", "service"); err != nil {
			return err
		}
		return controllerutil.SetControllerReference(cb, cp, r.Scheme)
	}); err != nil {
		if meta.IsNoMatchError(err) {
			// Console capability disabled: no ConsolePlugin CRD on the cluster.
			logConsolePluginNotReady(ctx, cb, "ConsoleMissing",
				"console CRDs not available (Console capability disabled)")
			return nil
		}
		return fmt.Errorf("ensuring ConsolePlugin %s: %w", pluginName, err)
	}

	if err := retry.RetryOnConflict(retry.DefaultRetry, func() error {
		console := u(consoleGVK)
		if err := r.Get(ctx, types.NamespacedName{Name: "cluster"}, console); err != nil {
			return err
		}
		plugins, _, err := unstructured.NestedStringSlice(console.Object, "spec", "plugins")
		if err != nil {
			return fmt.Errorf("reading console plugins: %w", err)
		}
		if slices.Contains(plugins, pluginName) {
			return nil
		}
		if err := unstructured.SetNestedStringSlice(console.Object, append(plugins, pluginName), "spec", "plugins"); err != nil {
			return fmt.Errorf("registering console plugin: %w", err)
		}
		return r.Update(ctx, console)
	}); err != nil {
		if apierrors.IsNotFound(err) || meta.IsNoMatchError(err) {
			// Soft-fail: still deploy plugin objects; registration retries later.
			logConsolePluginNotReady(ctx, cb, "ConsoleMissing",
				"consoles.operator.openshift.io/cluster not available")
			return nil
		}
		return fmt.Errorf("registering console plugin on consoles/cluster: %w", err)
	}

	// Re-read Deployment status so Ready is not claimed before pods are up.
	// Use pluginReadyMin (not full pluginReplicas) so a partial HA outage still
	// reports Deployed once the plugin can serve traffic.
	if err := r.Get(ctx, types.NamespacedName{Namespace: pluginNS, Name: pluginName}, dep); err != nil {
		return fmt.Errorf("reading plugin Deployment %s/%s status: %w", pluginNS, pluginName, err)
	}
	if dep.Status.ReadyReplicas < pluginReadyMin {
		reason, msg := "WaitingForPods",
			fmt.Sprintf("Deployment %s/%s has %d ready replicas (want >= %d of %d)",
				pluginNS, pluginName, dep.Status.ReadyReplicas, pluginReadyMin, pluginReplicas)
		if pluginDeploymentUnavailable(dep) {
			reason = "Unavailable"
			// Minutes from pluginUnavailableGrace so the message cannot drift.
			msg = fmt.Sprintf("Deployment %s/%s has no ready pods for >%dm",
				pluginNS, pluginName, int(pluginUnavailableGrace.Minutes()))
		}
		// Transition-only Info so WaitingForPods / Unavailable appear in default
		// logs without re-logging every requeue (matches ImageMissing path).
		logConsolePluginNotReady(ctx, cb, reason, msg)
		return nil
	}
	if !deploymentAvailable(dep) {
		reason, msg := "WaitingForPods",
			fmt.Sprintf("Deployment %s/%s ready pods present but Available is not True", pluginNS, pluginName)
		// Ready pods with Available=False past grace (e.g. progress deadline)
		// must not Progress forever.
		if deploymentAvailableFalsePastGrace(dep) {
			reason = "Unavailable"
			msg = fmt.Sprintf("Deployment %s/%s Available=False for >%dm",
				pluginNS, pluginName, int(pluginUnavailableGrace.Minutes()))
		}
		logConsolePluginNotReady(ctx, cb, reason, msg)
		return nil
	}
	setCond(cb, "ConsolePluginReady", metav1.ConditionTrue, "Deployed", "")
	return nil
}

// logConsolePluginNotReady sets ConsolePluginReady=False and Info-logs only when
// status or reason changes. Permanent soft-fails (ImageMissing/ImageInvalid) never
// Degrade the rollup; transition logs are the only default-level operator signal.
func logConsolePluginNotReady(ctx context.Context, cb *baselinev1alpha1.ClusterBaseline, reason, msg string) {
	setCondFalseLogOnce(ctx, cb, "ConsolePluginReady", reason, msg,
		"console plugin not ready", "name", cb.Name, "reason", reason, "message", msg)
}
