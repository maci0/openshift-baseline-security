package controller

import (
	"strings"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/utils/ptr"
)

// preferredHostnameAntiAffinity spreads plugin pods across nodes (CONVENTIONS.md HA).
func preferredHostnameAntiAffinity(labels map[string]string) *corev1.Affinity {
	return &corev1.Affinity{
		PodAntiAffinity: &corev1.PodAntiAffinity{
			PreferredDuringSchedulingIgnoredDuringExecution: []corev1.WeightedPodAffinityTerm{{
				Weight: 100,
				PodAffinityTerm: corev1.PodAffinityTerm{
					LabelSelector: &metav1.LabelSelector{MatchLabels: labels},
					TopologyKey:   "kubernetes.io/hostname",
				},
			}},
		},
	}
}

// availableCondition returns the Deployment's Available condition, or nil when
// it has none yet (brand-new object). Shared by the readiness/grace helpers.
func availableCondition(dep *appsv1.Deployment) *appsv1.DeploymentCondition {
	for i := range dep.Status.Conditions {
		if dep.Status.Conditions[i].Type == appsv1.DeploymentAvailable {
			return &dep.Status.Conditions[i]
		}
	}
	return nil
}

// deploymentAvailable is true when the Deployment Available condition is True.
// Missing condition is treated as not yet available.
func deploymentAvailable(dep *appsv1.Deployment) bool {
	c := availableCondition(dep)
	return c != nil && c.Status == corev1.ConditionTrue
}

// deploymentAvailableFalsePastGrace is true when Available has been False longer
// than pluginUnavailableGrace (distinct from zero-ready; ready pods may exist).
func deploymentAvailableFalsePastGrace(dep *appsv1.Deployment) bool {
	c := availableCondition(dep)
	if c == nil || c.Status != corev1.ConditionFalse {
		return false
	}
	return !c.LastTransitionTime.IsZero() && time.Since(c.LastTransitionTime.Time) > pluginUnavailableGrace
}

// pluginUnavailableGrace is how long the plugin Deployment may be unavailable
// before it is reported as Degraded rather than merely progressing.
const pluginUnavailableGrace = 5 * time.Minute

// pluginDeploymentUnavailable is true when the Deployment has been continuously
// below pluginReadyMin ready replicas longer than pluginUnavailableGrace.
// Prefer the Available condition's LastTransitionTime so a brief ReadyReplicas
// dip on an old Deployment is not treated as a permanent failure.
func pluginDeploymentUnavailable(dep *appsv1.Deployment) bool {
	if dep.Status.ReadyReplicas >= pluginReadyMin {
		return false
	}
	// Available False: time since it went down. Available True with zero ready
	// pods is pathological; still time-box from the last transition so we do not
	// Progress forever. No condition yet (brand-new object): use creation time.
	if c := availableCondition(dep); c != nil && !c.LastTransitionTime.IsZero() {
		return time.Since(c.LastTransitionTime.Time) > pluginUnavailableGrace
	}
	return !dep.CreationTimestamp.IsZero() && time.Since(dep.CreationTimestamp.Time) > pluginUnavailableGrace
}

// applyPluginContainer sets the plugin container, volume mounts, and volumes on the pod spec.
func applyPluginContainer(pod *corev1.PodSpec, image string) {
	// nginx serves static files; it never talks to the API server.
	pod.AutomountServiceAccountToken = ptr.To(false)
	pod.ServiceAccountName = "default"
	// Drop hand-injected pull secrets so they cannot outlive a reconcile.
	// nil (not empty slice): kubelet may still use the ServiceAccount's
	// imagePullSecrets for private RELATED_IMAGE registries.
	pod.ImagePullSecrets = nil
	pod.HostNetwork = false
	pod.HostPID = false
	pod.HostIPC = false
	pod.ShareProcessNamespace = nil
	pod.EphemeralContainers = nil
	pod.NodeName = ""
	pod.NodeSelector = nil
	pod.Tolerations = nil
	pod.TopologySpreadConstraints = nil
	pod.RuntimeClassName = nil
	pod.PriorityClassName = ""
	pod.Priority = nil
	pod.PreemptionPolicy = ptr.To(corev1.PreemptLowerPriority)
	pod.ActiveDeadlineSeconds = nil
	pod.ReadinessGates = nil
	pod.HostAliases = nil
	pod.Hostname = ""
	pod.Subdomain = ""
	pod.SetHostnameAsFQDN = ptr.To(false)
	pod.OS = nil
	pod.SchedulingGates = nil
	pod.ResourceClaims = nil
	pod.Resources = nil
	pod.Overhead = nil
	pod.HostnameOverride = nil
	pod.WorkloadRef = nil
	pod.DNSConfig = nil
	pod.EnableServiceLinks = ptr.To(false)
	pod.DNSPolicy = corev1.DNSClusterFirst
	pod.RestartPolicy = corev1.RestartPolicyAlways
	pod.SchedulerName = corev1.DefaultSchedulerName
	pod.TerminationGracePeriodSeconds = ptr.To(int64(30))
	pullPolicy := corev1.PullIfNotPresent
	imageLeaf := image[strings.LastIndex(image, "/")+1:]
	if !strings.Contains(imageLeaf, ":") || strings.HasSuffix(imageLeaf, ":latest") {
		pullPolicy = corev1.PullAlways
	}
	container := corev1.Container{
		Name:            pluginName,
		Image:           image,
		ImagePullPolicy: pullPolicy,
		Ports:           []corev1.ContainerPort{{Name: "https", ContainerPort: 9443, Protocol: corev1.ProtocolTCP}},
		SecurityContext: &corev1.SecurityContext{
			AllowPrivilegeEscalation: ptr.To(false),
			// Explicit false: do not rely on API default if a mutating webhook
			// or prior hand-edit left Privileged set before this replace.
			Privileged:   ptr.To(false),
			Capabilities: &corev1.Capabilities{Drop: []corev1.Capability{"ALL"}},
			RunAsNonRoot: ptr.To(true),
			// Container-level RuntimeDefault: Restricted PSS and audit tools that
			// only inspect container SC still see seccomp even if pod SC is stripped.
			SeccompProfile: &corev1.SeccompProfile{
				Type: corev1.SeccompProfileTypeRuntimeDefault,
			},
			// nginx pid/logs/temp use /tmp (emptyDir); rootfs stays immutable.
			ReadOnlyRootFilesystem: ptr.To(true),
		},
		Resources: corev1.ResourceRequirements{
			Requests: corev1.ResourceList{
				corev1.ResourceCPU:    resource.MustParse("10m"),
				corev1.ResourceMemory: resource.MustParse("32Mi"),
			},
			// Static asset server; bound usage so a runaway cannot starve the node.
			Limits: corev1.ResourceList{
				corev1.ResourceCPU:    resource.MustParse("200m"),
				corev1.ResourceMemory: resource.MustParse("128Mi"),
			},
		},
		// TCP only: the serving cert may be absent at first start, so HTTP
		// probes would fail closed until service-ca mints the Secret.
		// startupProbe owns the cold-start window (image pull lag, nginx
		// init); liveness must not kill the pod before listen is ready.
		// failureThreshold*periodSeconds = 2.5m (matches manager pattern).
		StartupProbe: &corev1.Probe{
			ProbeHandler: corev1.ProbeHandler{
				TCPSocket: &corev1.TCPSocketAction{Port: intstr.FromInt32(9443)},
			},
			PeriodSeconds:    5,
			TimeoutSeconds:   1,
			SuccessThreshold: 1,
			FailureThreshold: 30,
		},
		ReadinessProbe: &corev1.Probe{
			ProbeHandler: corev1.ProbeHandler{
				TCPSocket: &corev1.TCPSocketAction{Port: intstr.FromInt32(9443)},
			},
			TimeoutSeconds:   1,
			PeriodSeconds:    10,
			SuccessThreshold: 1,
			FailureThreshold: 3,
		},
		LivenessProbe: &corev1.Probe{
			ProbeHandler: corev1.ProbeHandler{
				TCPSocket: &corev1.TCPSocketAction{Port: intstr.FromInt32(9443)},
			},
			TimeoutSeconds:   1,
			PeriodSeconds:    20,
			SuccessThreshold: 1,
			FailureThreshold: 3,
		},
		TerminationMessagePath: corev1.TerminationMessagePathDefault,
		// Prefer container logs when nginx dies before writing the termination file
		// (matches the manager Deployment; ReadFile alone hides crash context).
		TerminationMessagePolicy: corev1.TerminationMessageFallbackToLogsOnError,
		VolumeMounts: []corev1.VolumeMount{
			{Name: "serving-cert", MountPath: "/var/serving-cert", ReadOnly: true},
			// Writable scratch for pid file and nginx temp paths (read-only rootfs).
			{Name: "tmp", MountPath: "/tmp"},
		},
	}
	// The Deployment is fully owned. Replacing the lists removes injected or
	// hand-added sidecars/init containers that would otherwise run unreviewed in
	// the plugin pod and survive every reconcile.
	pod.Containers = []corev1.Container{container}
	pod.InitContainers = nil

	// 0440 (owner+group read), not 0400: secret-volume files are owned by root,
	// not by the container's UID, so owner-only would be unreadable by the non-root
	// nginx process. On OpenShift the restricted-v2 SCC assigns an fsGroup that
	// group-owns this volume, so group-read lets nginx read its serving-cert key
	// while still keeping the private key off world-read (unlike the 0644 default).
	const certMode int32 = 0o440
	// Bound /tmp so a compromised nginx process cannot fill the node disk.
	tmpLimit := resource.MustParse("32Mi")
	pod.Volumes = []corev1.Volume{
		{
			Name: "serving-cert",
			VolumeSource: corev1.VolumeSource{
				// Optional until service-ca mints the Secret.
				Secret: &corev1.SecretVolumeSource{
					SecretName:  pluginName + "-cert",
					Optional:    ptr.To(true),
					DefaultMode: ptr.To(certMode),
				},
			},
		},
		{
			Name: "tmp",
			VolumeSource: corev1.VolumeSource{
				EmptyDir: &corev1.EmptyDirVolumeSource{SizeLimit: &tmpLimit},
			},
		},
	}
}
