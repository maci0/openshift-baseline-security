package controller

import (
	"context"
	_ "embed"
	"fmt"
	"maps"
	"os"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/robfig/cron/v3"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/client-go/util/retry"
	"k8s.io/utils/ptr"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/log"

	baselinev1alpha1 "github.com/maci0/baseline-security-operator/api/v1alpha1"
)

const (
	complianceNamespace = "openshift-compliance"
	scanSettingName     = "baseline"
	finalizerName       = "baselinesecurity.io/cleanup"
	pluginName          = "baseline-security-console-plugin"
	pluginNS            = "openshift-baseline-security"
	// The console renders dashboards from ConfigMaps in this namespace; ours is
	// created here so it shows under Observe -> Dashboards without a Grafana.
	dashboardNS   = "openshift-config-managed"
	dashboardName = "baseline-security-compliance-dashboard"
	suiteLabel          = "compliance.openshift.io/suite"
	checkSeverityLabel  = "compliance.openshift.io/check-severity"
	historyMax          = 30
	// Grace before a not-ready Compliance Operator install rolls up to Degraded
	// (OLM resolve + CSV install + pods can take several minutes on a slow cluster).
	coInstallGrace = 15 * time.Minute
	// Desired HA for the console plugin Deployment.
	pluginReplicas = int32(2)
	// Ready threshold for ConsolePluginReady=True: one ready pod is enough for
	// the plugin to serve; partial (1/2) must not Progress forever as WaitingForPods.
	pluginReadyMin = int32(1)
)

// Foreign CRs are unstructured so we do not import their Go API modules.
var (
	subscriptionGVK  = schema.GroupVersionKind{Group: "operators.coreos.com", Version: "v1alpha1", Kind: "Subscription"}
	csvGVK           = schema.GroupVersionKind{Group: "operators.coreos.com", Version: "v1alpha1", Kind: "ClusterServiceVersion"}
	scanSettingGVK   = schema.GroupVersionKind{Group: "compliance.openshift.io", Version: "v1alpha1", Kind: "ScanSetting"}
	bindingGVK       = schema.GroupVersionKind{Group: "compliance.openshift.io", Version: "v1alpha1", Kind: "ScanSettingBinding"}
	checkResultGVK   = schema.GroupVersionKind{Group: "compliance.openshift.io", Version: "v1alpha1", Kind: "ComplianceCheckResult"}
	scanGVK          = schema.GroupVersionKind{Group: "compliance.openshift.io", Version: "v1alpha1", Kind: "ComplianceScan"}
	consolePluginGVK = schema.GroupVersionKind{Group: "console.openshift.io", Version: "v1", Kind: "ConsolePlugin"}
	consoleGVK       = schema.GroupVersionKind{Group: "operator.openshift.io", Version: "v1", Kind: "Console"}
	operatorGroupGVK = schema.GroupVersionKind{Group: "operators.coreos.com", Version: "v1", Kind: "OperatorGroup"}
	remediationGVK   = schema.GroupVersionKind{Group: "compliance.openshift.io", Version: "v1alpha1", Kind: "ComplianceRemediation"}
	mcpGVK           = schema.GroupVersionKind{Group: "machineconfiguration.openshift.io", Version: "v1", Kind: "MachineConfigPool"}
)

//go:embed assets/compliance-dashboard.json
var complianceDashboardJSON string

// Compliance Operator annotations on an INCONSISTENT ComplianceCheckResult: the
// diverging nodes ("node:STATE,node:STATE") and the state the rest share.
const (
	inconsistentSourceAnn = "compliance.openshift.io/inconsistent-source"
	mostCommonStatusAnn   = "compliance.openshift.io/most-common-status"
)

// effectiveInconsistentStatus collapses a benign INCONSISTENT check to the status
// a user actually cares about. The Compliance Operator flags a check INCONSISTENT
// whenever nodes in a pool disagree, including when the check simply does not
// apply on some nodes (PASS where it applies, NOT-APPLICABLE elsewhere). That is
// not a real conflict, so:
//   - any FAIL or ERROR among the node states -> INCONSISTENT (genuine, review it)
//   - else at least one PASS                  -> PASS (passes where it applies)
//   - else only NOT-APPLICABLE/SKIP           -> NOT-APPLICABLE
//   - unknown/empty states                    -> INCONSISTENT (keep the raw signal)
func effectiveInconsistentStatus(item *unstructured.Unstructured) string {
	states := inconsistentStates(item)
	switch {
	case states["FAIL"] || states["ERROR"]:
		return "INCONSISTENT"
	case states["PASS"]:
		return "PASS"
	case states["NOT-APPLICABLE"] || states["SKIP"]:
		return "NOT-APPLICABLE"
	default:
		return "INCONSISTENT"
	}
}

// inconsistentStates returns the set of per-node states of an INCONSISTENT check,
// gathered from the inconsistent-source annotation and most-common-status.
// Untrusted cluster data: tolerant of malformed values, never panics.
func inconsistentStates(item *unstructured.Unstructured) map[string]bool {
	ann := item.GetAnnotations()
	states := map[string]bool{}
	for _, s := range strings.Split(ann[inconsistentSourceAnn], ",") {
		if i := strings.IndexByte(s, ':'); i >= 0 {
			if st := strings.ToUpper(strings.TrimSpace(s[i+1:])); st != "" {
				states[st] = true
			}
		}
	}
	if mc := strings.ToUpper(strings.TrimSpace(ann[mostCommonStatusAnn])); mc != "" {
		states[mc] = true
	}
	return states
}

// batchApplyAnnotation on the ClusterBaseline carries a comma-separated list of
// ComplianceRemediation names to batch-apply with MachineConfigPool pause/resume.
const batchApplyAnnotation = "baselinesecurity.io/batch-apply"

// batchResumeGrace forces a resume even if remediations never reach Applied, so
// a MachineConfigPool is never left paused.
const batchResumeGrace = 10 * time.Minute

// ClusterBaselineReconciler reconciles the ClusterBaseline singleton.
type ClusterBaselineReconciler struct {
	client.Client
	Scheme *runtime.Scheme
}

// +kubebuilder:rbac:groups=baselinesecurity.io,resources=clusterbaselines,verbs=get;list;watch;create;update;patch
// +kubebuilder:rbac:groups=baselinesecurity.io,resources=clusterbaselines/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=baselinesecurity.io,resources=clusterbaselines/finalizers,verbs=update
// +kubebuilder:rbac:groups=compliance.openshift.io,resources=scansettings;scansettingbindings,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=compliance.openshift.io,resources=compliancecheckresults;compliancescans,verbs=get;list;watch
// +kubebuilder:rbac:groups=compliance.openshift.io,resources=complianceremediations,verbs=get;list;watch;patch
// +kubebuilder:rbac:groups=machineconfiguration.openshift.io,resources=machineconfigpools,verbs=get;list;watch;patch
// Subscriptions need update/patch so complianceCatalogSource can be synced after
// the initial createIfMissing (OKD / disconnected catalog moves).
// +kubebuilder:rbac:groups=operators.coreos.com,resources=subscriptions,verbs=get;list;watch;create;update;patch
// +kubebuilder:rbac:groups=operators.coreos.com,resources=operatorgroups,verbs=get;list;watch;create
// +kubebuilder:rbac:groups=operators.coreos.com,resources=clusterserviceversions,verbs=get;list;watch
// +kubebuilder:rbac:groups="",resources=namespaces,verbs=get;list;watch;create
// +kubebuilder:rbac:groups="",resources=services,verbs=get;list;watch;create;update;patch;delete
// ConfigMaps: the console score-trend dashboard in openshift-config-managed.
// +kubebuilder:rbac:groups="",resources=configmaps,verbs=get;list;watch;create;update;patch
// +kubebuilder:rbac:groups="",resources=persistentvolumeclaims,verbs=get;list;watch
// +kubebuilder:rbac:groups=apps,resources=deployments,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=console.openshift.io,resources=consoleplugins,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=operator.openshift.io,resources=consoles,verbs=get;list;watch;update;patch

func (r *ClusterBaselineReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	logger := log.FromContext(ctx)

	cb := &baselinev1alpha1.ClusterBaseline{}
	if err := r.Get(ctx, req.NamespacedName, cb); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	if !cb.DeletionTimestamp.IsZero() {
		if err := r.deregisterConsolePlugin(ctx); err != nil {
			return ctrl.Result{}, err
		}
		if controllerutil.RemoveFinalizer(cb, finalizerName) {
			return ctrl.Result{}, r.Update(ctx, cb)
		}
		return ctrl.Result{}, nil
	}
	if controllerutil.AddFinalizer(cb, finalizerName) {
		return ctrl.Result{}, r.Update(ctx, cb) // update requeues
	}

	if err := r.reconcileOwned(ctx, cb); err != nil {
		// Persist a Degraded condition (best-effort) so a persistently failing
		// reconcile is visible on the CR instead of leaving stale healthy status.
		sanitizeStatusForUpdate(cb)
		setRollupConditions(cb)
		setCond(cb, "Degraded", metav1.ConditionTrue, "ReconcileError", err.Error())
		if serr := r.Status().Update(ctx, cb); serr != nil {
			logger.V(1).Info("status update after reconcile error failed", "error", serr)
		}
		return ctrl.Result{}, err
	}
	// OpenShift-style rollup conditions (Available / Progressing / Degraded).
	sanitizeStatusForUpdate(cb)
	setRollupConditions(cb)
	if err := r.Status().Update(ctx, cb); err != nil {
		return ctrl.Result{}, err
	}
	logger.V(1).Info("reconciled", "score", cb.Status.Score)
	// Poll while CRDs may be absent; requeue faster while still installing.
	requeue := time.Minute
	if progressing := meta.FindStatusCondition(cb.Status.Conditions, "Progressing"); progressing != nil && progressing.Status == metav1.ConditionTrue {
		requeue = 15 * time.Second
	}
	return ctrl.Result{RequeueAfter: requeue}, nil
}

// reconcileOwned drives every owned object and refreshes status fields.
func (r *ClusterBaselineReconciler) reconcileOwned(ctx context.Context, cb *baselinev1alpha1.ClusterBaseline) error {
	if err := r.ensureComplianceOperator(ctx, cb); err != nil {
		return fmt.Errorf("ensuring compliance operator: %w", err)
	}
	if err := r.ensureScanConfig(ctx, cb); err != nil {
		return fmt.Errorf("ensuring scan config: %w", err)
	}
	if err := r.ensureConsolePlugin(ctx, cb); err != nil {
		return fmt.Errorf("ensuring console plugin: %w", err)
	}
	if err := r.ensureComplianceDashboard(ctx, cb); err != nil {
		return fmt.Errorf("ensuring compliance dashboard: %w", err)
	}
	if err := r.applyRemediationBatch(ctx, cb); err != nil {
		return fmt.Errorf("applying remediation batch: %w", err)
	}
	if err := r.aggregateStatus(ctx, cb); err != nil {
		return fmt.Errorf("aggregating status: %w", err)
	}
	publishMetrics(cb)
	if err := r.checkScanStorage(ctx, cb); err != nil {
		return fmt.Errorf("checking scan storage: %w", err)
	}
	return nil
}

func bindingName(key baselinev1alpha1.ProfileKey) string { return "baseline-" + string(key) }

// tailoredBindingName names the binding for a TailoredProfile. The "tp-" infix
// keeps its suite label distinct from a built-in profile's "baseline-<key>".
func tailoredBindingName(name string) string { return "baseline-tp-" + name }

// tailoredNameFromSuite returns the TailoredProfile name for a tailored suite
// label ("baseline-tp-<name>"), or ("", false) otherwise.
func tailoredNameFromSuite(suite string) (string, bool) {
	n, ok := strings.CutPrefix(suite, "baseline-tp-")
	if !ok || n == "" {
		return "", false
	}
	return n, true
}

func ownedSuites(cb *baselinev1alpha1.ClusterBaseline) map[string]bool {
	s := make(map[string]bool, len(cb.Spec.Profiles)+len(cb.Spec.TailoredProfiles))
	for _, key := range cb.Spec.Profiles {
		s[bindingName(key)] = true
	}
	for _, name := range cb.Spec.TailoredProfiles {
		s[tailoredBindingName(name)] = true
	}
	return s
}

// matchesAnyProfile: name equals a profile/tailored base or a role-suffixed
// variant (ocp4-cis-node -> ocp4-cis-node-master, custom -> custom-worker).
// Only known ScanSetting roles are accepted after the "<base>-" boundary so a
// short or ambiguous base (e.g. tailored "ocp4") cannot prefix-match foreign
// PVCs like "ocp4-cis". name is untrusted cluster data.
func matchesAnyProfile(name string, profiles map[string]bool) bool {
	for p := range profiles {
		if name == p {
			return true
		}
		if rest, ok := strings.CutPrefix(name, p+"-"); ok && scanRoleSuffix(rest) {
			return true
		}
	}
	return false
}

// scanRoleSuffix is true for the role path we may append after a scan/profile
// base name. Matches ScanSetting roles we set (worker/master) and common extras,
// including the "node-<role>" form used by CO node profiles.
func scanRoleSuffix(rest string) bool {
	switch rest {
	case "worker", "master", "control-plane", "infra", "node":
		return true
	}
	if role, ok := strings.CutPrefix(rest, "node-"); ok {
		switch role {
		case "worker", "master", "control-plane", "infra":
			return true
		}
	}
	return false
}

// profileKeyFromSuite inverts bindingName ("baseline-<key>").
// Requires a non-empty key after the prefix so "baseline-" alone is rejected.
// Tailored suites ("baseline-tp-<name>") are excluded so they are only handled
// by tailoredNameFromSuite.
func profileKeyFromSuite(suite string) (baselinev1alpha1.ProfileKey, bool) {
	p, ok := strings.CutPrefix(suite, "baseline-")
	if !ok || p == "" || strings.HasPrefix(p, "tp-") {
		return "", false
	}
	return baselinev1alpha1.ProfileKey(p), true
}

// score is pass/(pass+fail)*100, or nil when there are no countable results.
// All arithmetic is int64 so pass+fail and pass*100 cannot overflow int32.
func score(pass, fail int32) *int32 {
	if pass < 0 || fail < 0 {
		return nil
	}
	total := int64(pass) + int64(fail)
	if total == 0 {
		return nil
	}
	s := int32(int64(pass) * 100 / total)
	return &s
}

// score64 is score() over already-int64 (severity-weighted) sums.
func score64(pass, fail int64) *int32 {
	if pass < 0 || fail < 0 || pass+fail == 0 {
		return nil
	}
	s := int32(pass * 100 / (pass + fail))
	return &s
}

// severityWeight maps a ComplianceCheckResult severity to a score weight, so a
// high-severity failure moves the severity-weighted score more than a low one.
func severityWeight(sev string) int64 {
	switch sev {
	case "high":
		return 10
	case "medium":
		return 5
	case "low":
		return 2
	default: // "unknown", "info", or missing
		return 1
	}
}

// splitCSV splits a comma-separated list, trimming and dropping empty items.
func splitCSV(s string) []string {
	var out []string
	for _, p := range strings.Split(s, ",") {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

// notIn returns the sorted members of a that are absent from set b.
func notIn(a []string, b map[string]bool) []string {
	var out []string
	for _, x := range a {
		if !b[x] {
			out = append(out, x)
		}
	}
	slices.Sort(out)
	return out
}

// withoutPlugin returns plugins without name (copy; does not mutate input).
func withoutPlugin(plugins []string, name string) []string {
	return slices.DeleteFunc(slices.Clone(plugins), func(p string) bool { return p == name })
}

// preferredHostnameAntiAffinity spreads pods across nodes (CONVENTIONS.md HA).
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

// appendHistoryRing appends a snapshot and keeps at most max entries (oldest first).
// The returned slice does not alias the input backing array after truncation.
func appendHistoryRing(hist []baselinev1alpha1.ScoreSnapshot, t metav1.Time, s int32, max int) []baselinev1alpha1.ScoreSnapshot {
	hist = append(hist, baselinev1alpha1.ScoreSnapshot{Time: t, Score: s})
	return clampHistory(hist, max)
}

// clampHistory trims history to the CRD MaxItems bound and clamps each score
// into [0,100]. Without this, a status already over the limit or with an
// out-of-range score (hand-edit, old bug) makes every Status().Update fail
// admission and freezes reconciliation feedback.
func clampHistory(hist []baselinev1alpha1.ScoreSnapshot, max int) []baselinev1alpha1.ScoreSnapshot {
	if max > 0 && len(hist) > max {
		hist = append([]baselinev1alpha1.ScoreSnapshot(nil), hist[len(hist)-max:]...)
	}
	for i := range hist {
		if hist[i].Score < 0 {
			hist[i].Score = 0
		} else if hist[i].Score > 100 {
			hist[i].Score = 100
		}
	}
	return hist
}

// clampScore keeps status.score inside the CRD [0,100] bounds so a hand-edited
// out-of-range value cannot lock out Status().Update admission.
func clampScore(s *int32) *int32 {
	if s == nil {
		return nil
	}
	switch {
	case *s < 0:
		z := int32(0)
		return &z
	case *s > 100:
		z := int32(100)
		return &z
	default:
		return s
	}
}

// sanitizeStatusForUpdate applies admission-safe bounds to status fields the
// reconciler writes so a hostile or stale status cannot brick updates.
func sanitizeStatusForUpdate(cb *baselinev1alpha1.ClusterBaseline) {
	cb.Status.History = clampHistory(cb.Status.History, historyMax)
	cb.Status.Score = clampScore(cb.Status.Score)
}

// condMessage caps condition messages so a huge wrapped error, invalid cron,
// or long PVC list cannot exceed the Condition message budget or fail status
// admission. Truncates on a UTF-8 boundary so the CR JSON stays valid.
func condMessage(s string) string {
	const max = 1024
	if len(s) <= max {
		return s
	}
	// Drop incomplete trailing rune; leave room for "...".
	end := max - 3
	for end > 0 && !utf8.ValidString(s[:end]) {
		end--
	}
	return s[:end] + "..."
}

// parseScanEndTimestamp parses a ComplianceScan status.endTimestamp. Accepts
// RFC3339 with optional fractional seconds. Far-future values are rejected so
// clock skew / corrupt data cannot pin LastScanTime ahead of real scans.
func parseScanEndTimestamp(ts string, now time.Time) (time.Time, bool) {
	if ts == "" {
		return time.Time{}, false
	}
	t, err := time.Parse(time.RFC3339Nano, ts)
	if err != nil {
		t, err = time.Parse(time.RFC3339, ts)
		if err != nil {
			return time.Time{}, false
		}
	}
	// Allow modest clock skew; anything further ahead is treated as garbage.
	if t.After(now.Add(time.Hour)) {
		return time.Time{}, false
	}
	return t, true
}

func setCond(cb *baselinev1alpha1.ClusterBaseline, typ string, status metav1.ConditionStatus, reason, msg string) {
	// Reason is required (minLength 1) and pattern-constrained on the CRD.
	// Never write empty: a hand-edited detail condition with Reason "" would
	// otherwise brick Status().Update when rolled up into Degraded.
	if reason == "" {
		reason = "Unknown"
	}
	meta.SetStatusCondition(&cb.Status.Conditions, metav1.Condition{
		Type:   typ,
		Status: status,
		Reason: reason,
		// Cap every message: InvalidSchedule embeds user cron text; storage
		// embeds PVC names; wrap errors can be huge. One path keeps status
		// updates from failing admission on an oversized message.
		Message:            condMessage(msg),
		ObservedGeneration: cb.Generation,
	})
}

// conditionProgressing is true for non-terminal False detail reasons that mean
// work is still in flight (not permanent admin action like Manual NotInstalled).
func conditionProgressing(c *metav1.Condition) bool {
	if c == nil || c.Status != metav1.ConditionFalse {
		return false
	}
	switch c.Reason {
	// Steady states (must not Progress / 15s-poll forever):
	// - ImageMissing: permanent deployment misconfig
	// - ConsoleMissing: Console capability disabled
	// - CRDsMissing: no compliance CRDs (common with installComplianceOperator=Manual
	//   until the admin installs CO; Automatic install is already Progressing via
	//   Installing/CSVNotReady on ComplianceOperatorReady)
	case "Installing", "CSVNotReady", "WaitingForPods":
		return true
	default:
		return false
	}
}

// setRollupConditions sets Available, Progressing, and Degraded from the
// detail conditions (ClusterOperator-style rollups).
func setRollupConditions(cb *baselinev1alpha1.ClusterBaseline) {
	co := meta.FindStatusCondition(cb.Status.Conditions, "ComplianceOperatorReady")
	scan := meta.FindStatusCondition(cb.Status.Conditions, "ScanConfigured")
	plugin := meta.FindStatusCondition(cb.Status.Conditions, "ConsolePluginReady")
	storage := meta.FindStatusCondition(cb.Status.Conditions, "ScanStorageReady")

	coReady := co != nil && co.Status == metav1.ConditionTrue
	scanOK := scan != nil && scan.Status == metav1.ConditionTrue
	// A Compliance Operator install that never becomes ready (bad catalog source,
	// unresolvable Subscription) would otherwise Progress + fast-poll forever. Past
	// a grace window, stop treating it as progress so it rolls up to Degraded and
	// the poll backs off, mirroring the console plugin's Unavailable-past-grace.
	coStuck := co != nil && co.Status == metav1.ConditionFalse &&
		(co.Reason == "Installing" || co.Reason == "CSVNotReady") &&
		!co.LastTransitionTime.IsZero() &&
		time.Since(co.LastTransitionTime.Time) > coInstallGrace
	progressing := (conditionProgressing(co) && !coStuck) ||
		conditionProgressing(scan) || conditionProgressing(plugin)

	if progressing {
		setCond(cb, "Progressing", metav1.ConditionTrue, "Reconciling", "installing or configuring dependencies")
	} else {
		setCond(cb, "Progressing", metav1.ConditionFalse, "AsExpected", "")
	}
	if coReady && scanOK {
		setCond(cb, "Available", metav1.ConditionTrue, "AsExpected", "compliance operator ready and scans configured")
	} else {
		setCond(cb, "Available", metav1.ConditionFalse, "NotReady", "waiting for compliance operator and scan configuration")
	}
	// Degraded: persistent failures that are not mere installation progress:
	// failed Compliance Operator CSV, invalid schedule, scan result storage
	// wedged, or the plugin down past its grace period.
	// Use fixed CamelCase reasons (never copy a possibly empty/hostile detail
	// Reason) so status admission cannot fail on Reason pattern/minLength.
	switch {
	case co != nil && co.Status == metav1.ConditionFalse && co.Reason == "CSVFailed":
		setCond(cb, "Degraded", metav1.ConditionTrue, "CSVFailed", co.Message)
	case coStuck:
		// Prefer the detail message; fall back to reason so we never end with a
		// trailing empty ": ".
		detail := co.Message
		if detail == "" {
			detail = co.Reason
		}
		setCond(cb, "Degraded", metav1.ConditionTrue, "InstallStalled",
			fmt.Sprintf("Compliance Operator not ready after %s: %s", coInstallGrace, detail))
	case scan != nil && scan.Status == metav1.ConditionFalse && scan.Reason == "InvalidSchedule":
		setCond(cb, "Degraded", metav1.ConditionTrue, "InvalidSchedule", scan.Message)
	case storage != nil && storage.Status == metav1.ConditionFalse:
		// Fixed reason only: never copy storage.Reason (hand-edit can violate
		// Condition Reason pattern and brick Status().Update admission).
		setCond(cb, "Degraded", metav1.ConditionTrue, "ScanStorageNotReady", storage.Message)
	case plugin != nil && plugin.Status == metav1.ConditionFalse && plugin.Reason == "Unavailable":
		setCond(cb, "Degraded", metav1.ConditionTrue, "ConsolePluginUnavailable", plugin.Message)
	default:
		setCond(cb, "Degraded", metav1.ConditionFalse, "AsExpected", "")
	}
}

func createIfMissing(ctx context.Context, c client.Client, obj client.Object) error {
	if err := c.Create(ctx, obj); err != nil && !apierrors.IsAlreadyExists(err) {
		return err
	}
	return nil
}

func u(gvk schema.GroupVersionKind) *unstructured.Unstructured {
	o := &unstructured.Unstructured{}
	o.SetGroupVersionKind(gvk)
	return o
}

func uList(gvk schema.GroupVersionKind) *unstructured.UnstructuredList {
	l := &unstructured.UnstructuredList{}
	l.SetGroupVersionKind(gvk.GroupVersion().WithKind(gvk.Kind + "List"))
	return l
}

func (r *ClusterBaselineReconciler) ensureComplianceOperator(ctx context.Context, cb *baselinev1alpha1.ClusterBaseline) error {
	sub := u(subscriptionGVK)
	err := r.Get(ctx, types.NamespacedName{Namespace: complianceNamespace, Name: "compliance-operator"}, sub)
	if err == nil {
		// Keep catalog source in sync when we manage install. createIfMissing only
		// writes the Subscription once; without this, changing
		// spec.complianceCatalogSource (OKD / disconnected) is a silent no-op.
		if cb.Spec.InstallComplianceOperator != baselinev1alpha1.InstallManual {
			if err := r.syncComplianceSubscriptionSource(ctx, cb, sub); err != nil {
				return err
			}
		}
		// Always evaluate readiness, including InstallManual, so Available cannot
		// stay True after CO is removed.
		return r.setComplianceOperatorReady(ctx, cb, sub)
	}
	if meta.IsNoMatchError(err) {
		cb.Status.ComplianceOperatorVersion = ""
		setCond(cb, "ComplianceOperatorReady", metav1.ConditionFalse, "NotInstalled",
			"OLM Subscription API not available")
		return nil
	}
	if !apierrors.IsNotFound(err) {
		return err
	}

	csv, err := r.findComplianceOperatorCSV(ctx)
	if err != nil {
		return err
	}
	if csv != nil {
		setComplianceOperatorReadyFromCSV(cb, csv)
		return nil
	}

	if cb.Spec.InstallComplianceOperator == baselinev1alpha1.InstallManual {
		cb.Status.ComplianceOperatorVersion = ""
		setCond(cb, "ComplianceOperatorReady", metav1.ConditionFalse, "NotInstalled",
			"compliance-operator Subscription not found; install manually or set installComplianceOperator=Automatic")
		return nil
	}

	if err := createIfMissing(ctx, r.Client, &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: complianceNamespace}}); err != nil {
		return err
	}
	og := u(operatorGroupGVK)
	og.SetName("compliance-operator")
	og.SetNamespace(complianceNamespace)
	_ = unstructured.SetNestedStringSlice(og.Object, []string{complianceNamespace}, "spec", "targetNamespaces")
	if err := createIfMissing(ctx, r.Client, og); err != nil {
		return err
	}

	sub = u(subscriptionGVK)
	sub.SetName("compliance-operator")
	sub.SetNamespace(complianceNamespace)
	sub.Object["spec"] = map[string]any{
		"name": "compliance-operator", "channel": "stable",
		"source": desiredComplianceCatalogSource(cb), "sourceNamespace": "openshift-marketplace",
	}
	if err := createIfMissing(ctx, r.Client, sub); err != nil {
		return err
	}
	setCond(cb, "ComplianceOperatorReady", metav1.ConditionFalse, "Installing", "waiting for CSV")
	return nil
}

// desiredComplianceCatalogSource is the OLM CatalogSource name for the CO
// Subscription (default redhat-operators).
func desiredComplianceCatalogSource(cb *baselinev1alpha1.ClusterBaseline) string {
	if s := cb.Spec.ComplianceCatalogSource; s != "" {
		return s
	}
	return "redhat-operators"
}

// syncComplianceSubscriptionSource updates an existing Subscription's
// spec.source when it diverges from the CR. No-op when already matched.
func (r *ClusterBaselineReconciler) syncComplianceSubscriptionSource(
	ctx context.Context, cb *baselinev1alpha1.ClusterBaseline, sub *unstructured.Unstructured,
) error {
	desired := desiredComplianceCatalogSource(cb)
	current, _, _ := unstructured.NestedString(sub.Object, "spec", "source")
	if current == desired {
		return nil
	}
	if err := unstructured.SetNestedField(sub.Object, desired, "spec", "source"); err != nil {
		return err
	}
	return r.Update(ctx, sub)
}

func (r *ClusterBaselineReconciler) findComplianceOperatorCSV(ctx context.Context) (*unstructured.Unstructured, error) {
	csvs := uList(csvGVK)
	if err := r.List(ctx, csvs); err != nil {
		if meta.IsNoMatchError(err) {
			return nil, nil
		}
		return nil, err
	}
	// Priority (newest version within each tier):
	//  1. Succeeded in openshift-compliance (where we install / Get installedCSV)
	//  2. Succeeded anywhere (manual install in another NS)
	//  3. Non-Succeeded in openshift-compliance
	//  4. Non-Succeeded anywhere
	// Tiering avoids two attacks: (a) stale high-version Succeeded leftovers in a
	// foreign NS beating the live local CSV; (b) a local Failed/Installing remnant
	// hiding a healthy Succeeded CSV elsewhere.
	if csv := pickComplianceOperatorCSV(csvs.Items, complianceNamespace, true); csv != nil {
		return csv, nil
	}
	if csv := pickComplianceOperatorCSV(csvs.Items, "", true); csv != nil {
		return csv, nil
	}
	if csv := pickComplianceOperatorCSV(csvs.Items, complianceNamespace, false); csv != nil {
		return csv, nil
	}
	return pickComplianceOperatorCSV(csvs.Items, "", false), nil
}

// pickComplianceOperatorCSV chooses the newest compliance-operator CSV among items.
// If ns is non-empty, only that namespace is considered. If succeededOnly, only
// phase=Succeeded CSVs are candidates; otherwise only non-Succeeded.
func pickComplianceOperatorCSV(items []unstructured.Unstructured, ns string, succeededOnly bool) *unstructured.Unstructured {
	var best *unstructured.Unstructured
	for i := range items {
		csv := &items[i]
		if ns != "" && csv.GetNamespace() != ns {
			continue
		}
		if !strings.HasPrefix(csv.GetName(), "compliance-operator.v") {
			continue
		}
		phase, _, _ := unstructured.NestedString(csv.Object, "status", "phase")
		isSucceeded := phase == "Succeeded"
		if succeededOnly != isSucceeded {
			continue
		}
		if best == nil || compareComplianceCSVVersion(csv.GetName(), best.GetName()) > 0 {
			best = csv.DeepCopy()
		}
	}
	return best
}

type complianceVersion struct {
	parts      []int
	prerelease string
}

func compareComplianceCSVVersion(a, b string) int {
	av, aok := complianceCSVVersion(a)
	bv, bok := complianceCSVVersion(b)
	switch {
	case aok && bok:
		if cmp := compareComplianceVersions(av, bv); cmp != 0 {
			return cmp
		}
		return strings.Compare(a, b)
	case aok:
		return 1
	case bok:
		return -1
	default:
		return strings.Compare(a, b)
	}
}

func complianceCSVVersion(name string) (complianceVersion, bool) {
	v, ok := strings.CutPrefix(name, "compliance-operator.v")
	if !ok || v == "" {
		return complianceVersion{}, false
	}
	v, _, _ = strings.Cut(v, "+")
	core, _, _ := strings.Cut(v, "-")
	_, prerelease, _ := strings.Cut(v, "-")
	parts := strings.Split(core, ".")
	out := make([]int, len(parts))
	for i, p := range parts {
		if p == "" {
			return complianceVersion{}, false
		}
		n, err := strconv.Atoi(p)
		if err != nil {
			return complianceVersion{}, false
		}
		out[i] = n
	}
	return complianceVersion{parts: out, prerelease: prerelease}, true
}

func compareComplianceVersions(a, b complianceVersion) int {
	if cmp := compareVersionParts(a.parts, b.parts); cmp != 0 {
		return cmp
	}
	switch {
	case a.prerelease == "" && b.prerelease != "":
		return 1
	case a.prerelease != "" && b.prerelease == "":
		return -1
	case a.prerelease != "" && b.prerelease != "":
		return comparePrerelease(a.prerelease, b.prerelease)
	default:
		return 0
	}
}

func compareVersionParts(a, b []int) int {
	n := max(len(a), len(b))
	for i := range n {
		var av, bv int
		if i < len(a) {
			av = a[i]
		}
		if i < len(b) {
			bv = b[i]
		}
		if av > bv {
			return 1
		}
		if av < bv {
			return -1
		}
	}
	return 0
}

func comparePrerelease(a, b string) int {
	ap := strings.Split(a, ".")
	bp := strings.Split(b, ".")
	n := min(len(ap), len(bp))
	for i := range n {
		ai, aNum := parsePrereleaseNumber(ap[i])
		bi, bNum := parsePrereleaseNumber(bp[i])
		switch {
		case aNum && bNum && ai != bi:
			if ai > bi {
				return 1
			}
			return -1
		case aNum && !bNum:
			return -1
		case !aNum && bNum:
			return 1
		case !aNum && !bNum:
			if cmp := strings.Compare(ap[i], bp[i]); cmp != 0 {
				return cmp
			}
		}
	}
	switch {
	case len(ap) > len(bp):
		return 1
	case len(ap) < len(bp):
		return -1
	default:
		return 0
	}
}

func parsePrereleaseNumber(s string) (int, bool) {
	if s == "" {
		return 0, false
	}
	for _, r := range s {
		if r < '0' || r > '9' {
			return 0, false
		}
	}
	n, err := strconv.Atoi(s)
	return n, err == nil
}

func (r *ClusterBaselineReconciler) setComplianceOperatorReady(ctx context.Context, cb *baselinev1alpha1.ClusterBaseline, sub *unstructured.Unstructured) error {
	csvName, _, _ := unstructured.NestedString(sub.Object, "status", "installedCSV")
	if csvName == "" {
		cb.Status.ComplianceOperatorVersion = ""
		setCond(cb, "ComplianceOperatorReady", metav1.ConditionFalse, "Installing", "installedCSV empty")
		return nil
	}

	csv := u(csvGVK)
	if err := r.Get(ctx, types.NamespacedName{Namespace: complianceNamespace, Name: csvName}, csv); err != nil {
		if apierrors.IsNotFound(err) {
			cb.Status.ComplianceOperatorVersion = ""
			setCond(cb, "ComplianceOperatorReady", metav1.ConditionFalse, "Installing", "waiting for CSV "+csvName)
			return nil
		}
		return err
	}
	setComplianceOperatorReadyFromCSV(cb, csv)
	return nil
}

func setComplianceOperatorReadyFromCSV(cb *baselinev1alpha1.ClusterBaseline, csv *unstructured.Unstructured) {
	phase, _, _ := unstructured.NestedString(csv.Object, "status", "phase")
	if phase == "Succeeded" {
		cb.Status.ComplianceOperatorVersion = strings.TrimPrefix(csv.GetName(), "compliance-operator.v")
		setCond(cb, "ComplianceOperatorReady", metav1.ConditionTrue, "CSVSucceeded", "")
		return
	}
	// Keep version empty until Succeeded so the UI does not show a green-looking
	// version string while the CSV is still Installing/Failed.
	cb.Status.ComplianceOperatorVersion = ""
	// Failed is terminal (not install progress); rollup marks Degraded.
	if phase == "Failed" {
		setCond(cb, "ComplianceOperatorReady", metav1.ConditionFalse, "CSVFailed", "phase=Failed")
		return
	}
	setCond(cb, "ComplianceOperatorReady", metav1.ConditionFalse, "CSVNotReady", "phase="+phase)
}

func (r *ClusterBaselineReconciler) ensureScanConfig(ctx context.Context, cb *baselinev1alpha1.ClusterBaseline) error {
	// Validate schedule first, but still reconcile ScanSetting fields other than
	// schedule and all bindings so a bad cron does not freeze profile/tp or
	// auto-apply changes. Invalid schedule is reported as Degraded at the end.
	schedule, schedErr := normalizedSchedule(cb.Spec.Schedule)
	invalidScheduleMessage := ""
	if schedErr != nil {
		invalidScheduleMessage = schedErr.Error()
	}

	ss := u(scanSettingGVK)
	ss.SetName(scanSettingName)
	ss.SetNamespace(complianceNamespace)
	_, err := controllerutil.CreateOrUpdate(ctx, r.Client, ss, func() error {
		autoApply := cb.Spec.Remediation.Apply == baselinev1alpha1.RemediationApplyAutomatic
		// Only write a validated schedule; keep the last-good cron if invalid.
		// On first create with a bad cron there is no last-good value: fall back
		// to the operator default so CO is not left with an empty schedule.
		if schedErr == nil {
			ss.Object["schedule"] = schedule
		} else if existing, found, _ := unstructured.NestedString(ss.Object, "schedule"); !found || existing == "" {
			ss.Object["schedule"] = "0 1 * * *"
		}
		ss.Object["roles"] = []any{"worker", "master"}
		// Set only the storage leaves we own; preserve server-defaulted siblings
		// (e.g. pvAccessModes) so this does not diff on every reconcile.
		storage, _, _ := unstructured.NestedMap(ss.Object, "rawResultStorage")
		if storage == nil {
			storage = map[string]any{}
		}
		storage["size"] = "1Gi"
		storage["rotation"] = int64(3)
		ss.Object["rawResultStorage"] = storage
		ss.Object["autoApplyRemediations"] = autoApply
		ss.Object["autoUpdateRemediations"] = autoApply
		return controllerutil.SetControllerReference(cb, ss, r.Scheme)
	})
	if err != nil {
		if meta.IsNoMatchError(err) {
			setCond(cb, "ScanConfigured", metav1.ConditionFalse, "CRDsMissing",
				"compliance.openshift.io CRDs not installed")
			return nil
		}
		return err
	}

	for _, key := range cb.Spec.Profiles {
		binding := u(bindingGVK)
		binding.SetName(bindingName(key))
		binding.SetNamespace(complianceNamespace)
		_, err := controllerutil.CreateOrUpdate(ctx, r.Client, binding, func() error {
			names := key.ProfileNames()
			profiles := make([]any, 0, len(names))
			for _, p := range names {
				profiles = append(profiles, map[string]any{
					"apiGroup": "compliance.openshift.io/v1alpha1", "kind": "Profile", "name": p,
				})
			}
			binding.Object["profiles"] = profiles
			binding.Object["settingsRef"] = map[string]any{
				"apiGroup": "compliance.openshift.io/v1alpha1", "kind": "ScanSetting", "name": scanSettingName,
			}
			return controllerutil.SetControllerReference(cb, binding, r.Scheme)
		})
		if err != nil {
			return err
		}
	}

	for _, name := range cb.Spec.TailoredProfiles {
		binding := u(bindingGVK)
		binding.SetName(tailoredBindingName(name))
		binding.SetNamespace(complianceNamespace)
		_, err := controllerutil.CreateOrUpdate(ctx, r.Client, binding, func() error {
			binding.Object["profiles"] = []any{map[string]any{
				"apiGroup": "compliance.openshift.io/v1alpha1", "kind": "TailoredProfile", "name": name,
			}}
			binding.Object["settingsRef"] = map[string]any{
				"apiGroup": "compliance.openshift.io/v1alpha1", "kind": "ScanSetting", "name": scanSettingName,
			}
			return controllerutil.SetControllerReference(cb, binding, r.Scheme)
		})
		if err != nil {
			return err
		}
	}

	bindings := uList(bindingGVK)
	if err := r.List(ctx, bindings, client.InNamespace(complianceNamespace)); err != nil {
		if meta.IsNoMatchError(err) {
			setCond(cb, "ScanConfigured", metav1.ConditionFalse, "CRDsMissing",
				"compliance.openshift.io CRDs not installed")
			return nil
		}
		return err
	}
	selected := ownedSuites(cb)
	for i := range bindings.Items {
		b := &bindings.Items[i]
		if selected[b.GetName()] || !metav1.IsControlledBy(b, cb) {
			continue
		}
		if err := r.Delete(ctx, b); err != nil && !apierrors.IsNotFound(err) {
			return err
		}
	}
	if invalidScheduleMessage != "" {
		setCond(cb, "ScanConfigured", metav1.ConditionFalse, "InvalidSchedule",
			fmt.Sprintf("spec.schedule %q is not a valid standard cron schedule: %s", cb.Spec.Schedule, invalidScheduleMessage))
		return nil
	}
	setCond(cb, "ScanConfigured", metav1.ConditionTrue, "BindingsCreated", "")
	return nil
}

func normalizedSchedule(schedule string) (string, error) {
	if schedule == "" {
		schedule = "0 1 * * *"
	}
	if _, err := cron.ParseStandard(schedule); err != nil {
		return "", err
	}
	return schedule, nil
}

// checkScanStorage flags Degraded when owned scan PVCs stay Pending (no default SC).
// checkScanStorage sets the ScanStorageReady detail condition; the Degraded
// rollup propagates it. Listing in a nonexistent namespace returns an empty
// list, so no NotFound handling is needed.
func (r *ClusterBaselineReconciler) checkScanStorage(ctx context.Context, cb *baselinev1alpha1.ClusterBaseline) error {
	pvcs := &corev1.PersistentVolumeClaimList{}
	if err := r.List(ctx, pvcs, client.InNamespace(complianceNamespace)); err != nil {
		return err
	}
	// Owned scan PVC names: built-in CO profile names and TailoredProfile names,
	// plus known role suffixes only (see matchesAnyProfile / scanRoleSuffix).
	names := map[string]bool{}
	for _, key := range cb.Spec.Profiles {
		for _, p := range key.ProfileNames() {
			names[p] = true
		}
	}
	for _, name := range cb.Spec.TailoredProfiles {
		names[name] = true
	}
	var pending []string
	for _, pvc := range pvcs.Items {
		owned := matchesAnyProfile(pvc.Name, names)
		// Require a real CreationTimestamp: a zero time makes time.Since huge and
		// would false-Degrade brand-new objects in some test/API edge paths.
		if owned &&
			pvc.Status.Phase == corev1.ClaimPending &&
			!pvc.CreationTimestamp.IsZero() &&
			time.Since(pvc.CreationTimestamp.Time) > 2*time.Minute {
			pending = append(pending, pvc.Name)
		}
	}
	if len(pending) > 0 {
		setCond(cb, "ScanStorageReady", metav1.ConditionFalse, "ScanStoragePending",
			fmt.Sprintf("PVC(s) %s/%s Pending >2m; need a default StorageClass",
				complianceNamespace, strings.Join(pending, ", ")))
		return nil
	}
	setCond(cb, "ScanStorageReady", metav1.ConditionTrue, "AsExpected", "")
	return nil
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
		plugins, _, _ := unstructured.NestedStringSlice(console.Object, "spec", "plugins")
		kept := withoutPlugin(plugins, pluginName)
		if len(kept) == len(plugins) {
			return nil
		}
		_ = unstructured.SetNestedStringSlice(console.Object, kept, "spec", "plugins")
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
		return err
	}
	for _, obj := range []client.Object{
		&appsv1.Deployment{ObjectMeta: metav1.ObjectMeta{Name: pluginName, Namespace: pluginNS}},
		&corev1.Service{ObjectMeta: metav1.ObjectMeta{Name: pluginName, Namespace: pluginNS}},
	} {
		if err := r.Delete(ctx, obj); err != nil && !apierrors.IsNotFound(err) {
			return err
		}
	}
	if err := r.deregisterConsolePlugin(ctx); err != nil {
		return err
	}
	setCond(cb, "ConsolePluginReady", metav1.ConditionFalse, "Disabled", "")
	return nil
}

func (r *ClusterBaselineReconciler) aggregateStatus(ctx context.Context, cb *baselinev1alpha1.ClusterBaseline) error {
	list := uList(checkResultGVK)
	if err := r.List(ctx, list, client.InNamespace(complianceNamespace)); err != nil {
		if meta.IsNoMatchError(err) {
			// CRDs gone: do not leave a stale score/profile rollup on the CR.
			cb.Status.Score = nil
			cb.Status.Profiles = nil
			cb.Status.TailoredProfiles = nil
			cb.Status.LastScanTime = nil
			cb.Status.NextScanTime = nil
			cb.Status.History = nil
			// Keep relatedObjects in sync with desired ownership even when CO is absent.
			cb.Status.RelatedObjects = relatedObjects(cb)
			return nil
		}
		return err
	}

	byProfile := map[baselinev1alpha1.ProfileKey]*baselinev1alpha1.ProfileStatus{}
	for _, key := range cb.Spec.Profiles {
		byProfile[key] = &baselinev1alpha1.ProfileStatus{Key: key, ProfileNames: key.ProfileNames()}
	}
	byTailored := map[string]*baselinev1alpha1.TailoredProfileStatus{}
	for _, name := range cb.Spec.TailoredProfiles {
		byTailored[name] = &baselinev1alpha1.TailoredProfileStatus{Name: name}
	}

	// Checks waived as accepted risk are pulled out of the pass/fail denominator
	// and reported in the Waived bucket, keyed by ComplianceCheckResult name.
	// Skip empty names so a corrupt entry cannot match every empty-named object.
	// An expired waiver no longer applies: the check is scored by its raw status.
	nowT := time.Now()
	waived := make(map[string]bool, len(cb.Spec.Waivers))
	for _, w := range cb.Spec.Waivers {
		if w.Name == "" {
			continue
		}
		if w.ExpiresAt != nil && !w.ExpiresAt.After(nowT) {
			continue
		}
		waived[w.Name] = true
	}

	var pass, fail int32
	var wPass, wFail int64 // severity-weighted totals
	var currentFails []string
	// tally routes one check result's status into the counts and the score.
	// INFO is counted (excluded from score) so Overview totals match Results.
	// SKIP is folded into NotApplicable (CO: check skipped for this system).
	// WAIVED is our synthetic status for accepted-risk checks (excluded from score).
	tally := func(c *baselinev1alpha1.ResultCounts, status string) {
		switch status {
		case "PASS":
			c.Pass++
			pass++
		case "FAIL":
			c.Fail++
			fail++
		case "MANUAL":
			c.Manual++
		case "INFO":
			c.Info++
		case "ERROR":
			c.Error++
		case "INCONSISTENT":
			c.Inconsistent++
		case "WAIVED":
			c.Waived++
		case "SKIP", "NOT-APPLICABLE":
			c.NotApplicable++
		}
	}
	for _, item := range list.Items {
		suite := item.GetLabels()[suiteLabel]
		// Route to the owning bucket first so weighting/regression only see owned checks.
		var rc *baselinev1alpha1.ResultCounts
		if name, ok := tailoredNameFromSuite(suite); ok {
			if ts := byTailored[name]; ts != nil {
				rc = &ts.ResultCounts
			}
		} else if key, ok := profileKeyFromSuite(suite); ok {
			if ps := byProfile[key]; ps != nil {
				rc = &ps.ResultCounts
			}
		}
		if rc == nil {
			continue
		}
		status, _, _ := unstructured.NestedString(item.Object, "status")
		// A check the Compliance Operator marks INCONSISTENT only because it does
		// not apply on some nodes (PASS where it applies, NOT-APPLICABLE elsewhere)
		// is benign; collapse it so it does not read as "review each". A real
		// PASS-vs-FAIL split stays INCONSISTENT.
		if status == "INCONSISTENT" {
			status = effectiveInconsistentStatus(&item)
		}
		// Waivers apply to failing checks only: a waived FAIL leaves the score
		// denominator into the Waived bucket. If a waived check later passes it
		// counts as PASS again (self-healing), so a stale waiver never silently
		// depresses the score; the admin can still remove it from the UI.
		if status == "FAIL" && waived[item.GetName()] {
			status = "WAIVED"
		}
		tally(rc, status)
		if status == "FAIL" {
			currentFails = append(currentFails, item.GetName())
			wFail += severityWeight(item.GetLabels()[checkSeverityLabel])
		} else if status == "PASS" {
			wPass += severityWeight(item.GetLabels()[checkSeverityLabel])
		}
	}
	slices.Sort(currentFails)

	// Preserve per-profile score history across the status.Profiles rebuild.
	profHist := map[baselinev1alpha1.ProfileKey][]baselinev1alpha1.ScoreSnapshot{}
	for _, p := range cb.Status.Profiles {
		profHist[p.Key] = p.History
	}
	tpHist := map[string][]baselinev1alpha1.ScoreSnapshot{}
	for _, tp := range cb.Status.TailoredProfiles {
		tpHist[tp.Name] = tp.History
	}
	cb.Status.Profiles = cb.Status.Profiles[:0]
	for _, key := range cb.Spec.Profiles {
		p := *byProfile[key]
		p.History = profHist[key]
		cb.Status.Profiles = append(cb.Status.Profiles, p)
	}
	cb.Status.TailoredProfiles = cb.Status.TailoredProfiles[:0]
	for _, name := range cb.Spec.TailoredProfiles {
		tp := *byTailored[name]
		tp.History = tpHist[name]
		cb.Status.TailoredProfiles = append(cb.Status.TailoredProfiles, tp)
	}
	// LastScanTime is tracked even when no score is computable (all MANUAL /
	// ERROR / NOT-APPLICABLE results) so completed scans stay visible.
	if cb.Spec.Scoring.Mode == baselinev1alpha1.ScoringSeverityWeighted {
		cb.Status.Score = score64(wPass, wFail)
	} else {
		cb.Status.Score = score(pass, fail)
	}
	// Fill deterministic status fields before history so a scan-list failure
	// still leaves a coherent rollup on the error-path status update.
	cb.Status.NextScanTime = nextScanTime(cb.Spec.Schedule, time.Now())
	cb.Status.RelatedObjects = relatedObjects(cb)
	return r.recordHistory(ctx, cb, cb.Status.Score, currentFails)
}

// nextScanTime computes the next cron fire after now, or nil on an invalid or
// empty schedule.
func nextScanTime(schedule string, now time.Time) *metav1.Time {
	normalized, err := normalizedSchedule(schedule)
	if err != nil {
		return nil
	}
	sched, err := cron.ParseStandard(normalized)
	if err != nil {
		return nil
	}
	// A degenerate-but-parseable schedule (e.g. an impossible day/month combo)
	// yields the zero time from Next; report no next scan rather than year 0001.
	nextTime := sched.Next(now)
	if nextTime.IsZero() {
		return nil
	}
	next := metav1.NewTime(nextTime)
	return &next
}

// relatedObjects lists the resources this baseline owns or drives, for
// must-gather / support tooling.
func relatedObjects(cb *baselinev1alpha1.ClusterBaseline) []baselinev1alpha1.ObjectRef {
	refs := []baselinev1alpha1.ObjectRef{
		{Group: "compliance.openshift.io", Resource: "scansettings", Name: scanSettingName, Namespace: complianceNamespace},
		{Group: "apps", Resource: "deployments", Name: pluginName, Namespace: pluginNS},
		{Group: "console.openshift.io", Resource: "consoleplugins", Name: pluginName},
	}
	suites := ownedSuites(cb)
	names := make([]string, 0, len(suites))
	for name := range suites {
		names = append(names, name)
	}
	slices.Sort(names) // deterministic order so status does not flap
	for _, name := range names {
		refs = append(refs, baselinev1alpha1.ObjectRef{
			Group: "compliance.openshift.io", Resource: "scansettingbindings", Name: name, Namespace: complianceNamespace,
		})
	}
	return refs
}

// poolFromRemediation returns the MachineConfigPool a node remediation targets,
// read from its rendered MachineConfig's role label, or "" for a non-node one.
func poolFromRemediation(rem *unstructured.Unstructured) string {
	obj, _, _ := unstructured.NestedMap(rem.Object, "spec", "current", "object")
	if obj == nil {
		return ""
	}
	if kind, _, _ := unstructured.NestedString(obj, "kind"); kind != "MachineConfig" {
		return ""
	}
	role, _, _ := unstructured.NestedString(obj, "metadata", "labels", "machineconfiguration.openshift.io/role")
	return role
}

func (r *ClusterBaselineReconciler) setMCPPaused(ctx context.Context, pool string, paused bool) error {
	mcp := u(mcpGVK)
	mcp.SetName(pool)
	patch := []byte(fmt.Sprintf(`{"spec":{"paused":%t}}`, paused))
	err := r.Patch(ctx, mcp, client.RawPatch(types.MergePatchType, patch))
	// A missing pool or absent MCP CRD must not wedge the batch.
	if apierrors.IsNotFound(err) || meta.IsNoMatchError(err) {
		return nil
	}
	return err
}

// applyRemediationBatch runs a two-phase batch apply driven by the batch-apply
// annotation: pause the affected MachineConfigPools and set apply on all listed
// remediations, then resume once they are Applied (or after a grace) so the pools
// reboot once. Resume is guaranteed: any failure still resumes the pools.
func (r *ClusterBaselineReconciler) applyRemediationBatch(ctx context.Context, cb *baselinev1alpha1.ClusterBaseline) error {
	batch := cb.Status.RemediationBatch
	names := cb.Annotations[batchApplyAnnotation]

	if batch == nil {
		if strings.TrimSpace(names) == "" {
			return nil
		}
		list := splitCSV(names)
		pools := map[string]bool{}
		for _, name := range list {
			rem := u(remediationGVK)
			if err := r.Get(ctx, types.NamespacedName{Namespace: complianceNamespace, Name: name}, rem); err != nil {
				if apierrors.IsNotFound(err) || meta.IsNoMatchError(err) {
					continue
				}
				return err
			}
			if p := poolFromRemediation(rem); p != "" {
				pools[p] = true
			}
		}
		poolList := slices.Sorted(maps.Keys(pools))
		// Pause first so all apply-triggered MachineConfig renders coalesce.
		for _, p := range poolList {
			if err := r.setMCPPaused(ctx, p, true); err != nil {
				return err
			}
		}
		for _, name := range list {
			rem := u(remediationGVK)
			rem.SetName(name)
			rem.SetNamespace(complianceNamespace)
			patch := []byte(`{"spec":{"apply":true}}`)
			if err := r.Patch(ctx, rem, client.RawPatch(types.MergePatchType, patch)); err != nil {
				if apierrors.IsNotFound(err) || meta.IsNoMatchError(err) {
					continue
				}
				// Resume any paused pools so a failure never leaves them paused.
				for _, p := range poolList {
					_ = r.setMCPPaused(ctx, p, false)
				}
				return err
			}
		}
		// Clear the one-shot annotation first (a meta Update refreshes cb from the
		// server, which would strip an in-memory status), then record the batch so
		// the end-of-reconcile Status().Update persists it.
		delete(cb.Annotations, batchApplyAnnotation)
		if err := r.Update(ctx, cb); err != nil {
			return err
		}
		cb.Status.RemediationBatch = &baselinev1alpha1.RemediationBatchStatus{
			Phase: "Applying", Pools: poolList, Remediations: list, StartedAt: metav1.Now(),
		}
		return nil
	}

	// Applying: resume when every listed remediation is Applied, or past grace.
	applied := true
	for _, name := range batch.Remediations {
		rem := u(remediationGVK)
		if err := r.Get(ctx, types.NamespacedName{Namespace: complianceNamespace, Name: name}, rem); err != nil {
			continue
		}
		if s, _, _ := unstructured.NestedString(rem.Object, "status", "applicationState"); s != "Applied" {
			applied = false
		}
	}
	if applied || time.Since(batch.StartedAt.Time) > batchResumeGrace {
		for _, p := range batch.Pools {
			if err := r.setMCPPaused(ctx, p, false); err != nil {
				return err
			}
		}
		cb.Status.RemediationBatch = nil
	}
	return nil
}

func (r *ClusterBaselineReconciler) recordHistory(ctx context.Context, cb *baselinev1alpha1.ClusterBaseline, s *int32, currentFails []string) error {
	scans := uList(scanGVK)
	if err := r.List(ctx, scans, client.InNamespace(complianceNamespace)); err != nil {
		if meta.IsNoMatchError(err) {
			return nil
		}
		return err
	}
	suites := ownedSuites(cb)
	now := time.Now()
	var latest time.Time
	for _, item := range scans.Items {
		if suite := item.GetLabels()[suiteLabel]; suite == "" || !suites[suite] {
			continue
		}
		ts, _, _ := unstructured.NestedString(item.Object, "status", "endTimestamp")
		if t, ok := parseScanEndTimestamp(ts, now); ok && t.After(latest) {
			latest = t
		}
	}
	if latest.IsZero() {
		return nil
	}
	last := metav1.NewTime(latest)
	if cb.Status.LastScanTime != nil && !last.After(cb.Status.LastScanTime.Time) {
		// Never rewind LastScanTime when the suite with the newest endTimestamp
		// is dropped (profile/tp removed). On equal end time:
		// - refresh the latest history score when late results change the rollup
		// - append a first history point when an earlier pass had score=nil
		//   (all MANUAL/INFO) and a countable score appears for the same scan
		if last.Equal(cb.Status.LastScanTime) && s != nil {
			if n := len(cb.Status.History); n > 0 && cb.Status.History[n-1].Time.Equal(cb.Status.LastScanTime) {
				cb.Status.History[n-1].Score = *s
			} else {
				cb.Status.History = appendHistoryRing(cb.Status.History, last, *s, historyMax)
			}
		}
		return nil
	}
	cb.Status.LastScanTime = &last
	if s != nil {
		cb.Status.History = appendHistoryRing(cb.Status.History, last, *s, historyMax)
	}
	// A new scan completed: compute regressions vs the previous scan's failures,
	// then snapshot the current failures for next time, and append a per-profile
	// history point so each benchmark can be trended.
	prev := make(map[string]bool, len(cb.Status.PreviousFailures))
	for _, n := range cb.Status.PreviousFailures {
		prev[n] = true
	}
	cur := make(map[string]bool, len(currentFails))
	for _, n := range currentFails {
		cur[n] = true
	}
	cb.Status.NewlyFailed = notIn(currentFails, prev)
	cb.Status.Fixed = notIn(cb.Status.PreviousFailures, cur)
	cb.Status.PreviousFailures = currentFails
	for i := range cb.Status.Profiles {
		if ps := score(cb.Status.Profiles[i].Pass, cb.Status.Profiles[i].Fail); ps != nil {
			cb.Status.Profiles[i].History = appendHistoryRing(cb.Status.Profiles[i].History, last, *ps, historyMax)
		}
	}
	for i := range cb.Status.TailoredProfiles {
		if ps := score(cb.Status.TailoredProfiles[i].Pass, cb.Status.TailoredProfiles[i].Fail); ps != nil {
			cb.Status.TailoredProfiles[i].History = appendHistoryRing(cb.Status.TailoredProfiles[i].History, last, *ps, historyMax)
		}
	}
	return nil
}

// ensureComplianceDashboard creates the score-trend dashboard as a ConfigMap in
// openshift-config-managed labeled console.openshift.io/dashboard, so the console
// renders it under Observe -> Dashboards (no Grafana). Data needs user-workload
// monitoring + the metrics ServiceMonitor; the dashboard renders regardless.
// Best-effort: a write failure here is logged, not Degrading, since the dashboard
// is cosmetic and must never block scanning or status.
func (r *ClusterBaselineReconciler) ensureComplianceDashboard(ctx context.Context, cb *baselinev1alpha1.ClusterBaseline) error {
	cm := &corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{Name: dashboardName, Namespace: dashboardNS}}
	_, err := controllerutil.CreateOrUpdate(ctx, r.Client, cm, func() error {
		if cm.Labels == nil {
			cm.Labels = map[string]string{}
		}
		cm.Labels["console.openshift.io/dashboard"] = "true"
		cm.Labels["app.kubernetes.io/part-of"] = "baseline-security"
		cm.Data = map[string]string{"baseline-security-compliance.json": complianceDashboardJSON}
		// cb is cluster-scoped, so a namespaced dependent in another namespace is a
		// valid ownerref target; the ConfigMap is GCed when the CR is deleted.
		return controllerutil.SetControllerReference(cb, cm, r.Scheme)
	})
	if err != nil {
		log.FromContext(ctx).V(1).Info("compliance dashboard configmap not reconciled", "error", err)
	}
	return nil
}

func (r *ClusterBaselineReconciler) ensureConsolePlugin(ctx context.Context, cb *baselinev1alpha1.ClusterBaseline) error {
	if cb.Spec.Console.ManagementState == baselinev1alpha1.Removed {
		return r.removeConsolePlugin(ctx, cb)
	}
	image := os.Getenv("RELATED_IMAGE_CONSOLE_PLUGIN")
	if image == "" {
		// Soft-fail: still reconcile scans/status; requeue will retry when env is fixed.
		setCond(cb, "ConsolePluginReady", metav1.ConditionFalse, "ImageMissing", "RELATED_IMAGE_CONSOLE_PLUGIN not set")
		return nil
	}
	if err := createIfMissing(ctx, r.Client, &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: pluginNS}}); err != nil {
		return err
	}

	labels := map[string]string{"app": pluginName}

	// Service first so service-ca can mint the serving-cert Secret before pods start.
	svc := &corev1.Service{ObjectMeta: metav1.ObjectMeta{Name: pluginName, Namespace: pluginNS}}
	if _, err := controllerutil.CreateOrUpdate(ctx, r.Client, svc, func() error {
		if svc.Annotations == nil {
			svc.Annotations = map[string]string{}
		}
		svc.Annotations["service.beta.openshift.io/serving-cert-secret-name"] = pluginName + "-cert"
		svc.Spec.Selector = labels
		svc.Spec.Ports = []corev1.ServicePort{{
			Name: "https", Port: 9443, TargetPort: intstr.FromInt32(9443), Protocol: corev1.ProtocolTCP,
		}}
		return controllerutil.SetControllerReference(cb, svc, r.Scheme)
	}); err != nil {
		return err
	}

	dep := &appsv1.Deployment{ObjectMeta: metav1.ObjectMeta{Name: pluginName, Namespace: pluginNS}}
	if _, err := controllerutil.CreateOrUpdate(ctx, r.Client, dep, func() error {
		// Mutate owned fields only; leave selector immutable after create.
		if dep.Spec.Selector == nil {
			dep.Spec.Selector = &metav1.LabelSelector{MatchLabels: labels}
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
		if dep.Spec.Template.Labels == nil {
			dep.Spec.Template.Labels = map[string]string{}
		}
		for k, v := range labels {
			dep.Spec.Template.Labels[k] = v
		}
		dep.Spec.Template.Spec.SecurityContext = &corev1.PodSecurityContext{
			RunAsNonRoot: ptr.To(true),
			SeccompProfile: &corev1.SeccompProfile{
				Type: corev1.SeccompProfileTypeRuntimeDefault,
			},
		}
		dep.Spec.Template.Spec.Affinity = preferredHostnameAntiAffinity(labels)
		applyPluginContainer(&dep.Spec.Template.Spec, image)
		return controllerutil.SetControllerReference(cb, dep, r.Scheme)
	}); err != nil {
		return err
	}

	cp := u(consolePluginGVK)
	cp.SetName(pluginName)
	if _, err := controllerutil.CreateOrUpdate(ctx, r.Client, cp, func() error {
		cp.Object["spec"] = map[string]any{
			"displayName": "Baseline Security",
			"backend": map[string]any{
				"type": "Service",
				"service": map[string]any{
					"name": pluginName, "namespace": pluginNS, "port": int64(9443), "basePath": "/",
				},
			},
		}
		return controllerutil.SetControllerReference(cb, cp, r.Scheme)
	}); err != nil {
		if meta.IsNoMatchError(err) {
			// Console capability disabled: no ConsolePlugin CRD on the cluster.
			setCond(cb, "ConsolePluginReady", metav1.ConditionFalse, "ConsoleMissing",
				"console CRDs not available (Console capability disabled)")
			return nil
		}
		return err
	}

	if err := retry.RetryOnConflict(retry.DefaultRetry, func() error {
		console := u(consoleGVK)
		if err := r.Get(ctx, types.NamespacedName{Name: "cluster"}, console); err != nil {
			return err
		}
		plugins, _, _ := unstructured.NestedStringSlice(console.Object, "spec", "plugins")
		if slices.Contains(plugins, pluginName) {
			return nil
		}
		_ = unstructured.SetNestedStringSlice(console.Object, append(plugins, pluginName), "spec", "plugins")
		return r.Update(ctx, console)
	}); err != nil {
		if apierrors.IsNotFound(err) || meta.IsNoMatchError(err) {
			// Soft-fail: still deploy plugin objects; registration retries later.
			setCond(cb, "ConsolePluginReady", metav1.ConditionFalse, "ConsoleMissing",
				"consoles.operator.openshift.io/cluster not available")
			return nil
		}
		return err
	}

	// Re-read Deployment status so Ready is not claimed before pods are up.
	// Use pluginReadyMin (not full pluginReplicas) so a partial HA outage still
	// reports Deployed once the plugin can serve traffic.
	if err := r.Get(ctx, types.NamespacedName{Namespace: pluginNS, Name: pluginName}, dep); err != nil {
		return err
	}
	if dep.Status.ReadyReplicas < pluginReadyMin {
		reason, msg := "WaitingForPods",
			fmt.Sprintf("Deployment %s/%s has %d ready replicas (want >= %d of %d)",
				pluginNS, pluginName, dep.Status.ReadyReplicas, pluginReadyMin, pluginReplicas)
		if pluginDeploymentUnavailable(dep) {
			reason = "Unavailable"
			msg = fmt.Sprintf("Deployment %s/%s has no ready pods for >5m", pluginNS, pluginName)
		}
		setCond(cb, "ConsolePluginReady", metav1.ConditionFalse, reason, msg)
		return nil
	}
	if !deploymentAvailable(dep) {
		reason, msg := "WaitingForPods",
			fmt.Sprintf("Deployment %s/%s ready pods present but Available is not True", pluginNS, pluginName)
		// Ready pods with Available=False past grace (e.g. progress deadline)
		// must not Progress forever.
		if deploymentAvailableFalsePastGrace(dep) {
			reason = "Unavailable"
			msg = fmt.Sprintf("Deployment %s/%s Available=False for >5m", pluginNS, pluginName)
		}
		setCond(cb, "ConsolePluginReady", metav1.ConditionFalse, reason, msg)
		return nil
	}
	setCond(cb, "ConsolePluginReady", metav1.ConditionTrue, "Deployed", "")
	return nil
}

// deploymentAvailable is true when the Deployment Available condition is True.
// Missing condition is treated as not yet available.
func deploymentAvailable(dep *appsv1.Deployment) bool {
	for _, c := range dep.Status.Conditions {
		if c.Type == appsv1.DeploymentAvailable {
			return c.Status == corev1.ConditionTrue
		}
	}
	return false
}

// deploymentAvailableFalsePastGrace is true when Available has been False longer
// than pluginUnavailableGrace (distinct from zero-ready; ready pods may exist).
func deploymentAvailableFalsePastGrace(dep *appsv1.Deployment) bool {
	for _, c := range dep.Status.Conditions {
		if c.Type != appsv1.DeploymentAvailable || c.Status != corev1.ConditionFalse {
			continue
		}
		return !c.LastTransitionTime.IsZero() && time.Since(c.LastTransitionTime.Time) > pluginUnavailableGrace
	}
	return false
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
	timeout := pluginUnavailableGrace
	for _, c := range dep.Status.Conditions {
		if c.Type != appsv1.DeploymentAvailable {
			continue
		}
		if c.LastTransitionTime.IsZero() {
			break
		}
		// Available False: time since it went down. Available True with zero
		// ready pods is pathological; still time-box from the last transition
		// so we do not Progress forever.
		return time.Since(c.LastTransitionTime.Time) > timeout
	}
	// No Available condition yet (brand-new object): use creation time.
	return !dep.CreationTimestamp.IsZero() && time.Since(dep.CreationTimestamp.Time) > timeout
}

// applyPluginContainer sets the plugin container, volume mounts, and volumes on the pod spec.
func applyPluginContainer(pod *corev1.PodSpec, image string) {
	// nginx serves static files; it never talks to the API server.
	pod.AutomountServiceAccountToken = ptr.To(false)
	container := corev1.Container{
		Name:  pluginName,
		Image: image,
		Ports: []corev1.ContainerPort{{Name: "https", ContainerPort: 9443, Protocol: corev1.ProtocolTCP}},
		SecurityContext: &corev1.SecurityContext{
			AllowPrivilegeEscalation: ptr.To(false),
			Capabilities:             &corev1.Capabilities{Drop: []corev1.Capability{"ALL"}},
			RunAsNonRoot:             ptr.To(true),
		},
		Resources: corev1.ResourceRequirements{
			Requests: corev1.ResourceList{
				corev1.ResourceCPU:    resource.MustParse("10m"),
				corev1.ResourceMemory: resource.MustParse("32Mi"),
			},
		},
		ReadinessProbe: &corev1.Probe{
			ProbeHandler: corev1.ProbeHandler{
				TCPSocket: &corev1.TCPSocketAction{Port: intstr.FromInt32(9443)},
			},
			InitialDelaySeconds: 5,
		},
		VolumeMounts: []corev1.VolumeMount{
			{Name: "serving-cert", MountPath: "/var/serving-cert", ReadOnly: true},
		},
	}
	found := false
	for i := range pod.Containers {
		if pod.Containers[i].Name == pluginName {
			pod.Containers[i] = container
			found = true
			break
		}
	}
	if !found {
		pod.Containers = append(pod.Containers, container)
	}

	vol := corev1.Volume{
		Name: "serving-cert",
		VolumeSource: corev1.VolumeSource{
			// Optional until service-ca mints the Secret.
			Secret: &corev1.SecretVolumeSource{
				SecretName: pluginName + "-cert",
				Optional:   ptr.To(true),
			},
		},
	}
	volFound := false
	for i := range pod.Volumes {
		if pod.Volumes[i].Name == "serving-cert" {
			pod.Volumes[i] = vol
			volFound = true
			break
		}
	}
	if !volFound {
		pod.Volumes = append(pod.Volumes, vol)
	}
}

func (r *ClusterBaselineReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&baselinev1alpha1.ClusterBaseline{}).
		Owns(&appsv1.Deployment{}).
		Owns(&corev1.Service{}).
		Named("clusterbaseline").
		Complete(r)
}
