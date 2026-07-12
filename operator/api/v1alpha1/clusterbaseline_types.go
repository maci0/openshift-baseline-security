package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// ProfileKey identifies one of the profile sets shipped by the Compliance
// Operator content that this operator knows how to bind.
// +kubebuilder:validation:Enum=cis;pci-dss;nist-moderate;nist-high;stig;nerc-cip;e8;bsi
type ProfileKey string

// Named ProfileKey values. Keep in lockstep with the kubebuilder Enum marker,
// AllProfileKeys, Profiles MaxItems, and the console PROFILE_KEYS list.
const (
	ProfileCIS          ProfileKey = "cis"
	ProfilePCIDSS       ProfileKey = "pci-dss"
	ProfileNISTModerate ProfileKey = "nist-moderate"
	ProfileNISTHigh     ProfileKey = "nist-high"
	ProfileSTIG         ProfileKey = "stig"
	ProfileNERCCIP      ProfileKey = "nerc-cip"
	ProfileE8           ProfileKey = "e8"
	ProfileBSI          ProfileKey = "bsi"
)

// HistoryMax caps overall and per-profile score history rings. Must match the
// CRD MaxItems on status.history / profiles[].history / tailoredProfiles[].history.
const HistoryMax = 30

// DefaultScanSchedule is used when ClusterBaselineSpec.schedule is empty or
// whitespace-only. Keep in lockstep with the kubebuilder default on Schedule
// and the console DEFAULT_SCAN_SCHEDULE constant.
const DefaultScanSchedule = "0 1 * * *"

// DefaultComplianceCatalogSource is used when ClusterBaselineSpec.complianceCatalogSource
// is empty or whitespace-only. Keep in lockstep with the kubebuilder default on
// ComplianceCatalogSource.
const DefaultComplianceCatalogSource = "redhat-operators"

// RemediationBatchPhaseApplying is the only phase written on
// status.remediationBatch while MachineConfigPools are paused for a batch apply.
// Absence of remediationBatch means idle; there is no terminal phase.
const RemediationBatchPhaseApplying = "Applying"

// InstallPolicy controls whether the operator installs a dependency itself.
// +kubebuilder:validation:Enum=Automatic;Manual
type InstallPolicy string

const (
	// InstallAutomatic creates the dependency's OLM Subscription when absent.
	InstallAutomatic InstallPolicy = "Automatic"
	// InstallManual leaves dependency installation to the cluster admin.
	InstallManual InstallPolicy = "Manual"
)

// ManagementState controls whether a managed component is deployed.
// +kubebuilder:validation:Enum=Managed;Removed
type ManagementState string

const (
	// Managed deploys and reconciles the component.
	Managed ManagementState = "Managed"
	// Removed removes the component.
	Removed ManagementState = "Removed"
)

// RemediationApplyPolicy controls how ComplianceRemediations are applied.
// +kubebuilder:validation:Enum=Automatic;Manual
type RemediationApplyPolicy string

const (
	// RemediationApplyAutomatic applies remediations after each scan.
	RemediationApplyAutomatic RemediationApplyPolicy = "Automatic"
	// RemediationApplyManual requires explicit per-remediation apply.
	RemediationApplyManual RemediationApplyPolicy = "Manual"
)

// ClusterBaselineSpec defines the desired baseline compliance posture.
type ClusterBaselineSpec struct {
	// profiles selects which benchmark profile sets to scan with. An empty list
	// (together with no tailoredProfiles) disables scanning: the operator prunes
	// the ScanSettingBindings and clears the score, keeping the CR and history.
	// New installs still default to {cis}; clear it explicitly to stop scanning.
	// Capped at the number of known ProfileKey enum values (listType=set).
	// +kubebuilder:default={cis}
	// +listType=set
	// +kubebuilder:validation:MaxItems=8
	Profiles []ProfileKey `json:"profiles"`

	// tailoredProfiles names TailoredProfiles in the openshift-compliance
	// namespace to also scan with. Create the TailoredProfile with the
	// Compliance Operator first; this binds it into the baseline scan and
	// includes its results in the score. Names are capped so the generated
	// baseline-tp-<name> suite label remains a valid Kubernetes label value.
	// +optional
	// +listType=set
	// +kubebuilder:validation:MaxItems=32
	// +kubebuilder:validation:items:MaxLength=51
	// +kubebuilder:validation:items:Pattern=`^[a-z0-9]([-a-z0-9]*[a-z0-9])?(\.[a-z0-9]([-a-z0-9]*[a-z0-9])?)*$`
	TailoredProfiles []string `json:"tailoredProfiles,omitempty"`

	// schedule is the scan cron schedule, applied to the owned ScanSetting.
	// Must be standard five-field cron (minute hour dom month dow), for example
	// "0 1 * * *". Descriptors such as "@daily" or "@every 1h" are rejected at
	// reconcile (InvalidSchedule Degraded) because Compliance Operator
	// ScanSettings only accept five-field forms. Empty uses the default.
	// Bounded so a hostile or accidental multi-megabyte string cannot bloat the
	// CR or inflate condition messages that embed the value.
	// +kubebuilder:default="0 1 * * *"
	// +kubebuilder:validation:MaxLength=128
	// +optional
	// Empty/whitespace uses DefaultScanSchedule at reconcile (same as CRD default).
	Schedule string `json:"schedule,omitempty"`

	// installComplianceOperator controls whether the operator creates an OLM
	// Subscription for the Compliance Operator when it is not installed.
	// +kubebuilder:default=Automatic
	// +optional
	InstallComplianceOperator InstallPolicy `json:"installComplianceOperator,omitempty"`

	// complianceCatalogSource is the OLM CatalogSource providing the
	// compliance-operator package (override for OKD or disconnected clusters).
	// Must be a DNS-1123 subdomain (CatalogSource metadata.name).
	// +kubebuilder:default="redhat-operators"
	// +kubebuilder:validation:MaxLength=253
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:Pattern=`^[a-z0-9]([-a-z0-9]*[a-z0-9])?(\.[a-z0-9]([-a-z0-9]*[a-z0-9])?)*$`
	// +optional
	// Empty/whitespace uses DefaultComplianceCatalogSource at reconcile (same as CRD default).
	ComplianceCatalogSource string `json:"complianceCatalogSource,omitempty"`

	// console configures the console plugin deployment.
	// +optional
	Console ConsoleSpec `json:"console,omitempty"`

	// remediation configures how ComplianceRemediations are handled.
	// +optional
	Remediation RemediationSpec `json:"remediation,omitempty"`

	// scoring configures how the compliance score is computed.
	// +optional
	Scoring ScoringSpec `json:"scoring,omitempty"`

	// waivers exclude specific failing checks from the score as accepted risk.
	// Each entry names a ComplianceCheckResult (stable across rescans). Only a
	// current FAIL with a non-expired waiver is remapped to the Waived bucket and
	// dropped from the pass/fail denominator; a waived check that later PASSes is
	// scored as PASS again. Capped so a hostile list cannot bloat the CR.
	// +optional
	// +listType=map
	// +listMapKey=name
	// +kubebuilder:validation:MaxItems=256
	Waivers []WaiverEntry `json:"waivers,omitempty"`
}

// WaiverEntry marks one ComplianceCheckResult as accepted risk.
type WaiverEntry struct {
	// name is the ComplianceCheckResult metadata.name to waive.
	// Must be a DNS-1123 subdomain (same shape as Kubernetes resource names).
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=253
	// +kubebuilder:validation:Pattern=`^[a-z0-9]([-a-z0-9]*[a-z0-9])?(\.[a-z0-9]([-a-z0-9]*[a-z0-9])?)*$`
	Name string `json:"name"`
	// reason records why this check is accepted (for audit).
	// +optional
	// +kubebuilder:validation:MaxLength=1024
	Reason string `json:"reason,omitempty"`
	// requestedBy records who requested the waiver (for audit).
	// +optional
	// +kubebuilder:validation:MaxLength=253
	RequestedBy string `json:"requestedBy,omitempty"`
	// approvedBy records who approved the waiver (for audit).
	// +optional
	// +kubebuilder:validation:MaxLength=253
	ApprovedBy string `json:"approvedBy,omitempty"`
	// expiresAt is when the waiver stops applying. After it passes, the check is
	// scored by its raw status again (an accepted risk is not permanent).
	// +optional
	ExpiresAt *metav1.Time `json:"expiresAt,omitempty"`
	// reviewBy is a reminder date to re-evaluate the waiver; informational.
	// +optional
	ReviewBy *metav1.Time `json:"reviewBy,omitempty"`
}

// RemediationSpec configures remediation handling.
type RemediationSpec struct {
	// apply set to Automatic applies remediations after each scan
	// (ScanSetting autoApplyRemediations/autoUpdateRemediations). Node
	// remediations render into MachineConfigs and reboot nodes.
	// +kubebuilder:default=Manual
	// +optional
	Apply RemediationApplyPolicy `json:"apply,omitempty"`
}

// ScoringMode selects how the compliance score is computed.
// +kubebuilder:validation:Enum=Flat;SeverityWeighted
type ScoringMode string

const (
	// ScoringFlat is the default pooled PASS/(PASS+FAIL) ratio; every check counts equally.
	ScoringFlat ScoringMode = "Flat"
	// ScoringSeverityWeighted weights each PASS/FAIL by the check's severity, so a
	// high-severity failure lowers the score more than a low-severity one.
	ScoringSeverityWeighted ScoringMode = "SeverityWeighted"
)

// ScoringSpec configures score computation.
type ScoringSpec struct {
	// mode selects flat (default) or severity-weighted scoring.
	// +kubebuilder:default=Flat
	// +optional
	Mode ScoringMode `json:"mode,omitempty"`
}

// ConsoleSpec configures the console plugin.
type ConsoleSpec struct {
	// managementState set to Managed deploys the baseline-security console
	// plugin and registers it with the console operator; Removed tears it down.
	// +kubebuilder:default=Managed
	// +optional
	ManagementState ManagementState `json:"managementState,omitempty"`
}

// ResultCounts holds check-result tallies shared by profile status types.
// Only Pass and Fail enter the score denominator (after waivers and benign
// INCONSISTENT collapse); the other buckets are reported for UI totals.
type ResultCounts struct {
	// Pass counts checks that passed (including benign INCONSISTENT collapsed
	// to PASS). Used in the score numerator and denominator.
	// +kubebuilder:validation:Minimum=0
	Pass int32 `json:"pass"`
	// Fail counts checks that failed and are not actively waived. Used in the
	// score denominator; waived FAILs are counted under Waived instead.
	// +kubebuilder:validation:Minimum=0
	Fail int32 `json:"fail"`
	// Manual counts checks that require human review (CO status MANUAL).
	// Excluded from the score; still reported so Overview totals match Results.
	// +kubebuilder:validation:Minimum=0
	Manual int32 `json:"manual"`
	// Info counts informational checks (CO status INFO). Excluded from the
	// score like Manual; still reported so Overview totals match Results.
	// +kubebuilder:validation:Minimum=0
	Info int32 `json:"info"`
	// Error counts checks that could not be evaluated (CO status ERROR, or an
	// empty/unknown raw status). Excluded from the score; still reported.
	// +kubebuilder:validation:Minimum=0
	Error int32 `json:"error"`
	// Inconsistent counts checks that remain INCONSISTENT after benign collapse
	// (PASS where applicable, NOT-APPLICABLE elsewhere becomes PASS). Genuine
	// PASS-vs-FAIL/ERROR node splits stay here. Excluded from the score like
	// Manual/Error; it flags a real discrepancy that needs review.
	// +kubebuilder:validation:Minimum=0
	Inconsistent int32 `json:"inconsistent"`
	// Waived counts checks excluded from the score by a spec.waivers entry
	// (accepted risk). Excluded from the pass/fail denominator.
	// +kubebuilder:validation:Minimum=0
	Waived int32 `json:"waived"`
	// NotApplicable counts checks that do not apply on this cluster (CO status
	// NOT-APPLICABLE, SKIP, or benign INCONSISTENT collapsed to N/A). Excluded
	// from the score.
	// +kubebuilder:validation:Minimum=0
	NotApplicable int32 `json:"notApplicable"`
}

// ProfileStatus summarizes check results for one selected profile key.
type ProfileStatus struct {
	// key is the ProfileKey from spec.profiles this status row describes.
	Key ProfileKey `json:"key"`
	// profileNames are the Compliance Operator Profile names bound for this key.
	// +optional
	// +listType=set
	// +kubebuilder:validation:MaxItems=16
	// +kubebuilder:validation:items:MaxLength=253
	ProfileNames []string `json:"profileNames,omitempty"`
	ResultCounts `json:",inline"`
	// history holds this profile's score snapshots, oldest first, capped at 30.
	// +optional
	// +kubebuilder:validation:MaxItems=30
	History []ScoreSnapshot `json:"history,omitempty"`
}

// TailoredProfileStatus summarizes check results for one bound TailoredProfile.
type TailoredProfileStatus struct {
	// name is the TailoredProfile metadata.name in openshift-compliance.
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=51
	// +kubebuilder:validation:Pattern=`^[a-z0-9]([-a-z0-9]*[a-z0-9])?(\.[a-z0-9]([-a-z0-9]*[a-z0-9])?)*$`
	Name         string `json:"name"`
	ResultCounts `json:",inline"`
	// history holds this profile's score snapshots, oldest first, capped at 30.
	// +optional
	// +kubebuilder:validation:MaxItems=30
	History []ScoreSnapshot `json:"history,omitempty"`
}

// ObjectRef points at a cluster resource this baseline owns or drives; consumed
// by must-gather and support tooling.
type ObjectRef struct {
	// group is the API group (empty for core resources).
	// +optional
	// +kubebuilder:validation:MaxLength=253
	Group string `json:"group,omitempty"`
	// resource is the plural resource name (for example, scansettings).
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=253
	Resource string `json:"resource"`
	// name is the object metadata.name.
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=253
	Name string `json:"name"`
	// namespace is set for namespaced objects; empty for cluster-scoped ones.
	// +optional
	// +kubebuilder:validation:MaxLength=63
	Namespace string `json:"namespace,omitempty"`
}

// RemediationBatchStatus tracks an in-progress batch apply that pauses the
// affected MachineConfigPools so node remediations reboot once, not per apply.
type RemediationBatchStatus struct {
	// phase is Applying while pools are paused and remediations are being applied.
	// +kubebuilder:validation:Enum=Applying
	// +kubebuilder:validation:MaxLength=32
	Phase string `json:"phase"`
	// pools are the MachineConfigPools paused for this batch.
	// +listType=set
	// +kubebuilder:validation:MaxItems=32
	// +kubebuilder:validation:items:MaxLength=253
	Pools []string `json:"pools,omitempty"`
	// pauseOwner is copied to each pool the operator actually pauses. It lets the
	// resume path preserve pools that an administrator had already paused.
	// Empty identifies a legacy in-flight batch created before ownership tracking.
	// +optional
	// +kubebuilder:validation:MaxLength=253
	PauseOwner string `json:"pauseOwner,omitempty"`
	// remediations are the ComplianceRemediation names in this batch.
	// +listType=set
	// +kubebuilder:validation:MaxItems=256
	// +kubebuilder:validation:items:MaxLength=253
	Remediations []string `json:"remediations,omitempty"`
	// startedAt bounds how long pools stay paused before a forced resume.
	StartedAt metav1.Time `json:"startedAt"`
}

// ScoreSnapshot is one point of score history, recorded when a scan completes.
// Score is under the scoring mode active at capture time (Flat or
// SeverityWeighted). Mode flips do not rewrite prior points under the new
// formula; on the next completed scan under the new mode the operator clears
// overall and per-profile history rings and writes a fresh point so charts never
// mix Flat and SeverityWeighted values (ADR-008).
type ScoreSnapshot struct {
	// time is when this score was recorded (typically the scan completion time).
	Time metav1.Time `json:"time"`
	// score is 0-100 under the scoring mode active when the point was written.
	// +kubebuilder:validation:Minimum=0
	// +kubebuilder:validation:Maximum=100
	Score int32 `json:"score"`
}

// ClusterBaselineStatus is the observed state.
type ClusterBaselineStatus struct {
	// conditions report readiness rollups and detail (Available, Progressing,
	// Degraded, plus ComplianceOperatorReady / ScanConfigured / etc.).
	// Map by type so Server-Side Apply and strategic merges update one condition
	// without replacing the whole list (Kubernetes API convention).
	// +optional
	// +listType=map
	// +listMapKey=type
	// +patchStrategy=merge
	// +patchMergeKey=type
	Conditions []metav1.Condition `json:"conditions,omitempty" patchStrategy:"merge" patchMergeKey:"type"`
	// score across all profiles, 0-100: pass/(pass+fail) in the default Flat
	// mode, a severity-weighted ratio when spec.scoring.mode is
	// SeverityWeighted. Benign INCONSISTENT is remapped to PASS or
	// NOT-APPLICABLE first. Residual genuine INCONSISTENT, plus MANUAL, INFO,
	// ERROR, WAIVED, and NOT-APPLICABLE, are excluded from the score; their
	// counts are still reported per profile.
	// +optional
	// +kubebuilder:validation:Minimum=0
	// +kubebuilder:validation:Maximum=100
	Score *int32 `json:"score,omitempty"`
	// lastScanTime is when the last completed scan finished (suite phase DONE).
	// Nil until the first scan completes. Kept when scanning is disabled (empty
	// profiles and tailored) so re-enable does not invent false regressions;
	// the last-scan Prometheus gauge is suppressed while scanning is off.
	// +optional
	LastScanTime *metav1.Time `json:"lastScanTime,omitempty"`
	// nextScanTime is the next scheduled scan, derived from spec.schedule.
	// +optional
	NextScanTime *metav1.Time `json:"nextScanTime,omitempty"`
	// complianceOperatorVersion is the adopted Compliance Operator CSV version
	// string when a matching CSV is found; empty while CO is not yet installed.
	// +optional
	// +kubebuilder:validation:MaxLength=128
	ComplianceOperatorVersion string `json:"complianceOperatorVersion,omitempty"`
	// profiles summarizes results per selected built-in profile key.
	// +optional
	// +listType=map
	// +listMapKey=key
	// +kubebuilder:validation:MaxItems=16
	Profiles []ProfileStatus `json:"profiles,omitempty"`
	// tailoredProfiles reports results for bound TailoredProfiles.
	// +optional
	// +listType=map
	// +listMapKey=name
	// +kubebuilder:validation:MaxItems=32
	TailoredProfiles []TailoredProfileStatus `json:"tailoredProfiles,omitempty"`
	// history holds score snapshots, oldest first, capped at 30 entries.
	// +optional
	// +kubebuilder:validation:MaxItems=30
	History []ScoreSnapshot `json:"history,omitempty"`
	// relatedObjects lists the resources this baseline owns or drives.
	// +optional
	// +kubebuilder:validation:MaxItems=64
	RelatedObjects []ObjectRef `json:"relatedObjects,omitempty"`
	// newlyFailed lists owned checks that are FAIL now but were not FAIL at the
	// previous completed scan (regressions since last scan). Waived FAILs still
	// count as FAIL outcomes here (accepting risk is not a regression clear).
	// Bounded by fail count and hard-capped so a hostile status cannot brick
	// Status().Update admission.
	// +optional
	// +listType=set
	// +kubebuilder:validation:MaxItems=4096
	// +kubebuilder:validation:items:MaxLength=253
	NewlyFailed []string `json:"newlyFailed,omitempty"`
	// fixed lists owned checks that were FAIL at the previous scan but are no
	// longer FAIL now (improvements since last scan). A waived FAIL is still a
	// FAIL outcome and does not appear here until the check actually PASSes.
	// +optional
	// +listType=set
	// +kubebuilder:validation:MaxItems=4096
	// +kubebuilder:validation:items:MaxLength=253
	Fixed []string `json:"fixed,omitempty"`
	// previousFailures is the internal FAIL snapshot from the last completed scan,
	// used to compute newlyFailed/fixed on the next scan. Includes waived FAILs
	// (score exclusion is separate). Not a consumer contract: shape and presence
	// may change in 0.x without a major bump; use newlyFailed and fixed for
	// user-facing regression views.
	// +optional
	// +listType=set
	// +kubebuilder:validation:MaxItems=4096
	// +kubebuilder:validation:items:MaxLength=253
	PreviousFailures []string `json:"previousFailures,omitempty"`
	// diffBaseFailures retains the scan-before-last FAIL snapshot while results
	// for lastScanTime settle, allowing late CheckResult events to correct the
	// current newlyFailed/fixed diff. Internal bookkeeping only; not a consumer
	// contract (may change in 0.x without a major bump).
	// +optional
	// +listType=set
	// +kubebuilder:validation:MaxItems=4096
	// +kubebuilder:validation:items:MaxLength=253
	DiffBaseFailures []string `json:"diffBaseFailures,omitempty"`
	// diffBaseScanTime identifies the lastScanTime whose diffBaseFailures apply.
	// It is nil for the first completed scan, which has no comparison baseline.
	// Internal bookkeeping only; not a consumer contract: shape and presence
	// may change in 0.x without a major bump; use newlyFailed and fixed for
	// user-facing regression views (and history length for "prior scan exists").
	// +optional
	DiffBaseScanTime *metav1.Time `json:"diffBaseScanTime,omitempty"`
	// remediationBatch tracks an in-progress MachineConfigPool-paused batch apply.
	// +optional
	RemediationBatch *RemediationBatchStatus `json:"remediationBatch,omitempty"`
}

// ClusterBaseline is the cluster-scoped singleton configuring baseline
// compliance scanning. Must be named "cluster".
// +kubebuilder:object:root=true
// +kubebuilder:resource:scope=Cluster
// +kubebuilder:subresource:status
// +kubebuilder:validation:XValidation:rule="self.metadata.name == 'cluster'",message="ClusterBaseline must be named 'cluster'"
// +kubebuilder:printcolumn:name="Score",type=integer,JSONPath=`.status.score`
// +kubebuilder:printcolumn:name="Last Scan",type=date,JSONPath=`.status.lastScanTime`
type ClusterBaseline struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   ClusterBaselineSpec   `json:"spec"`
	Status ClusterBaselineStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// ClusterBaselineList contains a list of ClusterBaseline.
type ClusterBaselineList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []ClusterBaseline `json:"items"`
}

func init() {
	SchemeBuilder.Register(&ClusterBaseline{}, &ClusterBaselineList{})
}

// AllProfileKeys returns every ProfileKey enum value in stable display order.
// Length must equal Profiles MaxItems and the Enum marker cardinality.
func AllProfileKeys() []ProfileKey {
	return []ProfileKey{
		ProfileCIS, ProfilePCIDSS, ProfileNISTModerate, ProfileNISTHigh,
		ProfileSTIG, ProfileNERCCIP, ProfileE8, ProfileBSI,
	}
}

// Known reports whether k is a ProfileKey admitted by the CRD enum.
func (k ProfileKey) Known() bool {
	return k.ProfileNames() != nil
}

// ProfileNames maps a profile key to the Compliance Operator Profile names it
// binds. Single source of truth for the key -> profile expansion.
func (k ProfileKey) ProfileNames() []string {
	switch k {
	case ProfileCIS:
		return []string{"ocp4-cis", "ocp4-cis-node"}
	case ProfilePCIDSS:
		return []string{"ocp4-pci-dss", "ocp4-pci-dss-node"}
	case ProfileNISTModerate:
		return []string{"ocp4-moderate", "ocp4-moderate-node", "rhcos4-moderate"}
	case ProfileNISTHigh:
		return []string{"ocp4-high", "ocp4-high-node", "rhcos4-high"}
	case ProfileSTIG:
		return []string{"ocp4-stig", "ocp4-stig-node", "rhcos4-stig"}
	case ProfileNERCCIP:
		return []string{"ocp4-nerc-cip", "ocp4-nerc-cip-node", "rhcos4-nerc-cip"}
	case ProfileE8:
		return []string{"ocp4-e8", "rhcos4-e8"}
	case ProfileBSI:
		return []string{"ocp4-bsi", "ocp4-bsi-node", "rhcos4-bsi"}
	}
	return nil
}
