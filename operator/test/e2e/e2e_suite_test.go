//go:build e2e

// Package e2e drives the operator against a live cluster reachable via the
// current KUBECONFIG. It assumes the operator is already installed (via OLM or
// `make deploy`) and that a ClusterBaseline/cluster exists or will be
// default-created. Run with: make test-e2e (or `go test -tags e2e ./test/e2e`).
package e2e

import (
	"context"
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

// countOwnedResults tallies live ComplianceCheckResults by status across every
// suite this baseline owns: the ground truth the operator's status should match.
func countOwnedResults(ctx context.Context, c client.Client, cb *baselinev1alpha1.ClusterBaseline) (map[string]int, error) {
	list := &unstructured.UnstructuredList{}
	list.SetGroupVersionKind(checkResultGVK.GroupVersion().WithKind(checkResultGVK.Kind + "List"))
	if err := c.List(ctx, list, client.InNamespace(complianceNamespace)); err != nil {
		return nil, err
	}
	owned := ownedSuites(cb)
	counts := map[string]int{}
	for i := range list.Items {
		suite := list.Items[i].GetLabels()[suiteLabel]
		if !owned[suite] {
			continue
		}
		status, _, _ := unstructured.NestedString(list.Items[i].Object, "status")
		counts[status]++
	}
	return counts, nil
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
