//go:build e2e

// Package e2e drives the operator against a live cluster reachable via the
// current KUBECONFIG. It assumes the operator is already installed (via OLM or
// `make deploy`) and that a ClusterBaseline/cluster exists or will be
// default-created. Run with: make test-e2e (or `go test -tags e2e ./test/e2e`).
package e2e

import (
	"context"
	"strings"
	"testing"
	"time"

	baselinev1alpha1 "github.com/maci0/baseline-security-operator/api/v1alpha1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/config"
)

const (
	complianceNamespace = "openshift-compliance"
	pluginNS            = "openshift-baseline-security"
	pluginName          = "baseline-security-console-plugin"
	suiteLabel          = "compliance.openshift.io/suite"
)

var (
	consoleGVK       = schema.GroupVersionKind{Group: "operator.openshift.io", Version: "v1", Kind: "Console"}
	consolePluginGVK = schema.GroupVersionKind{Group: "console.openshift.io", Version: "v1", Kind: "ConsolePlugin"}
	bindingGVK       = schema.GroupVersionKind{Group: "compliance.openshift.io", Version: "v1alpha1", Kind: "ScanSettingBinding"}
	scanSettingGVK   = schema.GroupVersionKind{Group: "compliance.openshift.io", Version: "v1alpha1", Kind: "ScanSetting"}
	scanGVK          = schema.GroupVersionKind{Group: "compliance.openshift.io", Version: "v1alpha1", Kind: "ComplianceScan"}
	checkResultGVK   = schema.GroupVersionKind{Group: "compliance.openshift.io", Version: "v1alpha1", Kind: "ComplianceCheckResult"}
	remediationGVK   = schema.GroupVersionKind{Group: "compliance.openshift.io", Version: "v1alpha1", Kind: "ComplianceRemediation"}
	tailoredGVK      = schema.GroupVersionKind{Group: "compliance.openshift.io", Version: "v1alpha1", Kind: "TailoredProfile"}
)

// ownedSuites returns the suite labels this baseline owns: "baseline-<profile>"
// for built-ins and "baseline-tp-<name>" for tailored profiles.
func ownedSuites(cb *baselinev1alpha1.ClusterBaseline) map[string]bool {
	s := map[string]bool{}
	for _, k := range cb.Spec.Profiles {
		s["baseline-"+string(k)] = true
	}
	for _, n := range cb.Spec.TailoredProfiles {
		s["baseline-tp-"+n] = true
	}
	return s
}

// activeWaivers returns names of non-expired waivers on the baseline. Empty names
// are dropped (corrupt entries never match). Matches aggregateStatus expiry:
// ExpiresAt is inactive once !After(now).
func activeWaivers(cb *baselinev1alpha1.ClusterBaseline, now time.Time) map[string]bool {
	waived := make(map[string]bool, len(cb.Spec.Waivers))
	for _, w := range cb.Spec.Waivers {
		if w.Name == "" {
			continue
		}
		if w.ExpiresAt != nil && !w.ExpiresAt.After(now) {
			continue
		}
		waived[w.Name] = true
	}
	return waived
}

// countOwnedResults tallies live ComplianceCheckResults by status across every
// suite this baseline owns: the ground truth the operator's status should match.
// Mirrors aggregateStatus: benign INCONSISTENT collapse, SKIP→NOT-APPLICABLE,
// and FAIL+active-waiver→WAIVED. Without waiver exclusion the flat score oracle
// would disagree with status.score whenever any check is waived.
func countOwnedResults(ctx context.Context, c client.Client, cb *baselinev1alpha1.ClusterBaseline) (map[string]int, error) {
	list := &unstructured.UnstructuredList{}
	list.SetGroupVersionKind(checkResultGVK.GroupVersion().WithKind(checkResultGVK.Kind + "List"))
	if err := c.List(ctx, list, client.InNamespace(complianceNamespace)); err != nil {
		return nil, err
	}
	owned := ownedSuites(cb)
	waived := activeWaivers(cb, time.Now())
	counts := map[string]int{}
	for i := range list.Items {
		suite := list.Items[i].GetLabels()[suiteLabel]
		if !owned[suite] {
			continue
		}
		status := effectiveCheckStatus(&list.Items[i])
		if status == "FAIL" && waived[list.Items[i].GetName()] {
			status = "WAIVED"
		}
		counts[status]++
	}
	return counts, nil
}

// effectiveCheckStatus mirrors the operator: a benign INCONSISTENT (PASS where it
// applies, NOT-APPLICABLE elsewhere) collapses to PASS / NOT-APPLICABLE, while a
// genuine PASS-vs-FAIL split stays INCONSISTENT. Top-level SKIP is folded into
// NOT-APPLICABLE (operator ResultCounts). The e2e ground truth must apply the
// same rule as the controller or the two disagree by construction.
func effectiveCheckStatus(item *unstructured.Unstructured) string {
	status, _, _ := unstructured.NestedString(item.Object, "status")
	if status != "INCONSISTENT" {
		if status == "SKIP" {
			return "NOT-APPLICABLE"
		}
		return status
	}
	ann := item.GetAnnotations()
	states := map[string]bool{}
	for _, s := range strings.Split(ann["compliance.openshift.io/inconsistent-source"], ",") {
		if i := strings.IndexByte(s, ':'); i >= 0 {
			if st := strings.ToUpper(strings.TrimSpace(s[i+1:])); st != "" {
				states[st] = true
			}
		}
	}
	if mc := strings.ToUpper(strings.TrimSpace(ann["compliance.openshift.io/most-common-status"])); mc != "" {
		states[mc] = true
	}
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

// newClient builds a controller-runtime client from the ambient kubeconfig with
// the core + ClusterBaseline schemes registered.
func newClient(t *testing.T) client.Client {
	t.Helper()
	cfg, err := config.GetConfig()
	if err != nil {
		t.Fatalf("load kubeconfig: %v", err)
	}
	scheme := clientgoscheme.Scheme
	if err := baselinev1alpha1.AddToScheme(scheme); err != nil {
		t.Fatalf("add scheme: %v", err)
	}
	c, err := client.New(cfg, client.Options{Scheme: scheme})
	if err != nil {
		t.Fatalf("build client: %v", err)
	}
	return c
}

// eventually polls fn until it returns nil or the timeout elapses.
func eventually(t *testing.T, timeout time.Duration, desc string, fn func() error) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	var last error
	for time.Now().Before(deadline) {
		if last = fn(); last == nil {
			return
		}
		time.Sleep(5 * time.Second)
	}
	t.Fatalf("timed out after %s waiting for %s: %v", timeout, desc, last)
}

func getBaseline(ctx context.Context, c client.Client) (*baselinev1alpha1.ClusterBaseline, error) {
	cb := &baselinev1alpha1.ClusterBaseline{}
	err := c.Get(ctx, client.ObjectKey{Name: "cluster"}, cb)
	return cb, err
}

func conditionTrue(cb *baselinev1alpha1.ClusterBaseline, typ string) bool {
	for _, cond := range cb.Status.Conditions {
		if cond.Type == typ {
			return cond.Status == "True"
		}
	}
	return false
}

func conditionStatus(cb *baselinev1alpha1.ClusterBaseline, typ string) string {
	for _, cond := range cb.Status.Conditions {
		if cond.Type == typ {
			return string(cond.Status)
		}
	}
	return ""
}

func conditionReason(cb *baselinev1alpha1.ClusterBaseline, typ string) string {
	for _, cond := range cb.Status.Conditions {
		if cond.Type == typ {
			return cond.Reason
		}
	}
	return ""
}
