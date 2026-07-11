package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// ProfileKey identifies one of the profile sets shipped by the Compliance
// Operator content that this operator knows how to bind.
// +kubebuilder:validation:Enum=cis;pci-dss;nist-moderate;nist-high;stig;nerc-cip;e8;bsi
type ProfileKey string

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
	// profiles selects which benchmark profile sets to scan with.
	// +kubebuilder:default={cis}
	// +kubebuilder:validation:MinItems=1
	// +listType=set
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
	// Bounded so a hostile or accidental multi-megabyte string cannot bloat the
	// CR or inflate condition messages that embed the value.
	// +kubebuilder:default="0 1 * * *"
	// +kubebuilder:validation:MaxLength=128
	// +optional
	Schedule string `json:"schedule,omitempty"`

	// installComplianceOperator controls whether the operator creates an OLM
	// Subscription for the Compliance Operator when it is not installed.
	// +kubebuilder:default=Automatic
	// +optional
	InstallComplianceOperator InstallPolicy `json:"installComplianceOperator,omitempty"`

	// complianceCatalogSource is the OLM CatalogSource providing the
	// compliance-operator package (override for OKD or disconnected clusters).
	// +kubebuilder:default="redhat-operators"
	// +kubebuilder:validation:MaxLength=253
	// +optional
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
	// Each entry names a ComplianceCheckResult and records why it is waived. A
	// waived check is removed from the pass/fail denominator and reported in the
	// Waived bucket instead, so an accepted risk neither inflates nor tanks the
	// score. Waivers are keyed by result name, which is stable across rescans.
	// Capped so a hostile list cannot bloat the CR past practical audit size.
	// +optional
	// +listType=map
	// +listMapKey=name
	// +kubebuilder:validation:MaxItems=256
	Waivers []WaiverEntry `json:"waivers,omitempty"`
}

// WaiverEntry marks one ComplianceCheckResult as accepted risk.
type WaiverEntry struct {
	// name is the ComplianceCheckResult metadata.name to waive.
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=253
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
type ResultCounts struct {
	Pass   int32 `json:"pass"`
	Fail   int32 `json:"fail"`
	Manual int32 `json:"manual"`
	// Info counts informational checks (CO status INFO). Excluded from the
	// score like Manual; still reported so Overview totals match Results.
	Info  int32 `json:"info"`
	Error int32 `json:"error"`
	// Inconsistent counts checks whose per-node results disagree across a
	// MachineConfigPool (compliance operator status INCONSISTENT). Excluded from
	// the score like Manual/Error; it flags a discrepancy that needs review.
	Inconsistent int32 `json:"inconsistent"`
	// Waived counts checks excluded from the score by a spec.waivers entry
	// (accepted risk). Excluded from the pass/fail denominator.
	Waived        int32 `json:"waived"`
	NotApplicable int32 `json:"notApplicable"`
}

// ProfileStatus summarizes check results for one selected profile key.
type ProfileStatus struct {
	Key          ProfileKey `json:"key"`
	ProfileNames []string   `json:"profileNames,omitempty"`
	ResultCounts `json:",inline"`
	// history holds this profile's score snapshots, oldest first, capped at 30.
	// +optional
	// +kubebuilder:validation:MaxItems=30
	History []ScoreSnapshot `json:"history,omitempty"`
}

// TailoredProfileStatus summarizes check results for one bound TailoredProfile.
type TailoredProfileStatus struct {
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
	Group     string `json:"group,omitempty"`
	Resource  string `json:"resource"`
	Name      string `json:"name"`
	Namespace string `json:"namespace,omitempty"`
}

// RemediationBatchStatus tracks an in-progress batch apply that pauses the
// affected MachineConfigPools so node remediations reboot once, not per apply.
type RemediationBatchStatus struct {
	// phase is Applying while pools are paused and remediations are being applied.
	Phase string `json:"phase"`
	// pools are the MachineConfigPools paused for this batch.
	// +listType=set
	Pools []string `json:"pools,omitempty"`
	// remediations are the ComplianceRemediation names in this batch.
	// +listType=set
	Remediations []string `json:"remediations,omitempty"`
	// startedAt bounds how long pools stay paused before a forced resume.
	StartedAt metav1.Time `json:"startedAt"`
}

// ScoreSnapshot is one point of score history, recorded when a scan completes.
type ScoreSnapshot struct {
	Time metav1.Time `json:"time"`
	// +kubebuilder:validation:Minimum=0
	// +kubebuilder:validation:Maximum=100
	Score int32 `json:"score"`
}

// ClusterBaselineStatus is the observed state.
type ClusterBaselineStatus struct {
	// +optional
	Conditions []metav1.Condition `json:"conditions,omitempty"`
	// score is pass/(pass+fail) across all profiles, 0-100. MANUAL, INFO,
	// ERROR, INCONSISTENT, WAIVED, and NOT-APPLICABLE are excluded from the
	// score; their counts are still reported per profile.
	// +optional
	// +kubebuilder:validation:Minimum=0
	// +kubebuilder:validation:Maximum=100
	Score *int32 `json:"score,omitempty"`
	// +optional
	LastScanTime *metav1.Time `json:"lastScanTime,omitempty"`
	// nextScanTime is the next scheduled scan, derived from spec.schedule.
	// +optional
	NextScanTime *metav1.Time `json:"nextScanTime,omitempty"`
	// +optional
	ComplianceOperatorVersion string `json:"complianceOperatorVersion,omitempty"`
	// +optional
	Profiles []ProfileStatus `json:"profiles,omitempty"`
	// tailoredProfiles reports results for bound TailoredProfiles.
	// +optional
	TailoredProfiles []TailoredProfileStatus `json:"tailoredProfiles,omitempty"`
	// history holds score snapshots, oldest first, capped at 30 entries.
	// +optional
	// +kubebuilder:validation:MaxItems=30
	History []ScoreSnapshot `json:"history,omitempty"`
	// relatedObjects lists the resources this baseline owns or drives.
	// +optional
	RelatedObjects []ObjectRef `json:"relatedObjects,omitempty"`
	// newlyFailed lists owned checks that are FAIL now but were not FAIL at the
	// previous completed scan (regressions since last scan). Bounded by fail count.
	// +optional
	// +listType=set
	NewlyFailed []string `json:"newlyFailed,omitempty"`
	// fixed lists owned checks that were FAIL at the previous scan but are no
	// longer FAIL now (improvements since last scan).
	// +optional
	// +listType=set
	Fixed []string `json:"fixed,omitempty"`
	// previousFailures is the internal FAIL snapshot from the last completed scan,
	// used to compute newlyFailed/fixed on the next scan.
	// +optional
	// +listType=set
	PreviousFailures []string `json:"previousFailures,omitempty"`
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

// ProfileNames maps a profile key to the Compliance Operator Profile names it
// binds. Single source of truth for the key -> profile expansion.
func (k ProfileKey) ProfileNames() []string {
	switch k {
	case "cis":
		return []string{"ocp4-cis", "ocp4-cis-node"}
	case "pci-dss":
		return []string{"ocp4-pci-dss", "ocp4-pci-dss-node"}
	case "nist-moderate":
		return []string{"ocp4-moderate", "ocp4-moderate-node", "rhcos4-moderate"}
	case "nist-high":
		return []string{"ocp4-high", "ocp4-high-node", "rhcos4-high"}
	case "stig":
		return []string{"ocp4-stig", "ocp4-stig-node", "rhcos4-stig"}
	case "nerc-cip":
		return []string{"ocp4-nerc-cip", "ocp4-nerc-cip-node", "rhcos4-nerc-cip"}
	case "e8":
		return []string{"ocp4-e8", "rhcos4-e8"}
	case "bsi":
		return []string{"ocp4-bsi", "ocp4-bsi-node", "rhcos4-bsi"}
	}
	return nil
}
