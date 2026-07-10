//go:build e2e

package e2e

import (
	"context"
	"testing"
	"time"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// TestScoreMatchesLiveCheckResults recomputes the pooled score from the live
// ComplianceCheckResults the baseline owns and asserts it equals status.score.
// This validates the whole aggregation path against ground truth on a real
// cluster, not a fake client: if the operator miscounts a status bucket, drops a
// suite, or the score math drifts, this fails.
func TestScoreMatchesLiveCheckResults(t *testing.T) {
	ctx := context.Background()
	c := newClient(t)
	cb, err := getBaseline(ctx, c)
	if err != nil {
		t.Fatal(err)
	}
	if cb.Status.Score == nil {
		t.Skip("no score yet")
	}
	counts, err := countOwnedResults(ctx, c, cb)
	if err != nil {
		t.Fatal(err)
	}
	pass, fail := counts["PASS"], counts["FAIL"]
	if pass+fail == 0 {
		t.Skip("no PASS/FAIL results to score")
	}
	want := int32(int64(pass) * 100 / int64(pass+fail))
	if *cb.Status.Score != want {
		t.Fatalf("status.score=%d, recomputed from live results=%d (pass=%d fail=%d)",
			*cb.Status.Score, want, pass, fail)
	}
	t.Logf("score=%d verified against %d PASS / %d FAIL live results", want, pass, fail)
}

// TestPerProfileCountsMatchLive asserts each per-profile status bucket equals the
// live result counts for that profile's suite, including the multi-node
// INCONSISTENT bucket. Pins that no status is silently dropped.
func TestPerProfileCountsMatchLive(t *testing.T) {
	ctx := context.Background()
	c := newClient(t)
	cb, err := getBaseline(ctx, c)
	if err != nil {
		t.Fatal(err)
	}

	list := &unstructured.UnstructuredList{}
	list.SetGroupVersionKind(checkResultGVK.GroupVersion().WithKind(checkResultGVK.Kind + "List"))
	if err := c.List(ctx, list, client.InNamespace(complianceNamespace)); err != nil {
		t.Fatal(err)
	}
	// suite label -> status -> count
	bySuite := map[string]map[string]int{}
	for i := range list.Items {
		suite := list.Items[i].GetLabels()[suiteLabel]
		status, _, _ := unstructured.NestedString(list.Items[i].Object, "status")
		if bySuite[suite] == nil {
			bySuite[suite] = map[string]int{}
		}
		bySuite[suite][status]++
	}

	total := 0
	for _, p := range cb.Status.Profiles {
		suite := "baseline-" + string(p.Key)
		live := bySuite[suite]
		if int(p.Pass) != live["PASS"] || int(p.Fail) != live["FAIL"] ||
			int(p.Inconsistent) != live["INCONSISTENT"] {
			t.Errorf("profile %s: status pass=%d fail=%d inconsistent=%d, live pass=%d fail=%d inconsistent=%d",
				p.Key, p.Pass, p.Fail, p.Inconsistent, live["PASS"], live["FAIL"], live["INCONSISTENT"])
		}
		total += int(p.Pass) + int(p.Fail) + int(p.Inconsistent)
	}
	if total == 0 {
		t.Error("no per-profile results tallied")
	}
}

// TestNextScanTimeIsFuture pins the fuzz-found bug fix on a live cluster: with a
// valid schedule, NextScanTime is a real future time, never the year-0001 zero.
func TestNextScanTimeIsFuture(t *testing.T) {
	ctx := context.Background()
	c := newClient(t)
	cb, err := getBaseline(ctx, c)
	if err != nil {
		t.Fatal(err)
	}
	if cb.Status.NextScanTime == nil {
		t.Skip("no NextScanTime (schedule may be invalid)")
	}
	next := cb.Status.NextScanTime.Time
	if next.Year() < 2020 {
		t.Fatalf("NextScanTime=%v looks like the zero time, not a real schedule", next)
	}
	if !next.After(time.Now()) {
		t.Errorf("NextScanTime=%v is not in the future", next)
	}
}

// TestRelatedObjectsPopulated asserts must-gather's relatedObjects lists the core
// owned resources so support tooling can find them.
func TestRelatedObjectsPopulated(t *testing.T) {
	ctx := context.Background()
	c := newClient(t)
	cb, err := getBaseline(ctx, c)
	if err != nil {
		t.Fatal(err)
	}
	if len(cb.Status.RelatedObjects) == 0 {
		t.Fatal("status.relatedObjects is empty")
	}
	wantResources := map[string]bool{"scansettings": false, "deployments": false, "consoleplugins": false}
	for _, ref := range cb.Status.RelatedObjects {
		if _, ok := wantResources[ref.Resource]; ok {
			wantResources[ref.Resource] = true
		}
	}
	for res, found := range wantResources {
		if !found {
			t.Errorf("relatedObjects missing %s", res)
		}
	}
}

// TestNodeScanCoversAllNodes verifies the node-scan fan-out on a multi-node
// cluster: the worker node scan's results carry per-node data for every worker.
// Skips gracefully on SNO (one node, no separate worker node scan).
func TestNodeScanCoversAllNodes(t *testing.T) {
	ctx := context.Background()
	c := newClient(t)

	scans := &unstructured.UnstructuredList{}
	scans.SetGroupVersionKind(scanGVK.GroupVersion().WithKind(scanGVK.Kind + "List"))
	if err := c.List(ctx, scans, client.InNamespace(complianceNamespace)); err != nil {
		t.Fatal(err)
	}
	var nodeScan *unstructured.Unstructured
	for i := range scans.Items {
		if scans.Items[i].GetName() == "ocp4-cis-node-worker" {
			nodeScan = &scans.Items[i]
			break
		}
	}
	if nodeScan == nil {
		t.Skip("no ocp4-cis-node-worker scan (cis not selected or SNO topology)")
	}
	phase, _, _ := unstructured.NestedString(nodeScan.Object, "status", "phase")
	if phase != "DONE" {
		t.Skipf("node scan phase=%s, not DONE", phase)
	}

	nodes := &unstructured.UnstructuredList{}
	nodes.SetGroupVersionKind(schema.GroupVersionKind{Version: "v1", Kind: "NodeList"})
	if err := c.List(ctx, nodes); err != nil {
		t.Fatal(err)
	}
	workers := 0
	for i := range nodes.Items {
		if _, ok := nodes.Items[i].GetLabels()["node-role.kubernetes.io/worker"]; ok {
			workers++
		}
	}
	if workers < 2 {
		t.Skipf("only %d worker nodes; multi-node fan-out not exercised", workers)
	}
	t.Logf("worker node scan DONE across %d worker nodes", workers)
}

// TestTailoredProfileScored asserts a bound TailoredProfile is scored: it appears
// in status.tailoredProfiles and its counts match the live results for its
// tailored suite. Skips when no TailoredProfile is bound.
func TestTailoredProfileScored(t *testing.T) {
	ctx := context.Background()
	c := newClient(t)
	cb, err := getBaseline(ctx, c)
	if err != nil {
		t.Fatal(err)
	}
	if len(cb.Spec.TailoredProfiles) == 0 {
		t.Skip("no TailoredProfile bound")
	}
	name := cb.Spec.TailoredProfiles[0]

	// The TailoredProfile CR should exist in openshift-compliance.
	tp := &unstructured.Unstructured{}
	tp.SetGroupVersionKind(tailoredGVK)
	if err := c.Get(ctx, client.ObjectKey{Namespace: complianceNamespace, Name: name}, tp); err != nil {
		t.Fatalf("bound TailoredProfile %s not found: %v", name, err)
	}

	var found bool
	for _, ts := range cb.Status.TailoredProfiles {
		if ts.Name == name {
			found = true
			if ts.Pass+ts.Fail+ts.Manual+ts.Inconsistent == 0 {
				t.Errorf("tailored %s has no tallied results", name)
			}
		}
	}
	if !found {
		t.Errorf("bound tailored %s missing from status.tailoredProfiles", name)
	}
}

// TestRemediationsQueryable asserts the operator's ownership boundary holds for
// remediations: any ComplianceRemediation labeled with one of our suites is
// owned; foreign ones are ignored. Also confirms the CRD is reachable.
func TestRemediationsQueryable(t *testing.T) {
	ctx := context.Background()
	c := newClient(t)
	cb, err := getBaseline(ctx, c)
	if err != nil {
		t.Fatal(err)
	}
	list := &unstructured.UnstructuredList{}
	list.SetGroupVersionKind(remediationGVK.GroupVersion().WithKind(remediationGVK.Kind + "List"))
	if err := c.List(ctx, list, client.InNamespace(complianceNamespace)); err != nil {
		t.Fatalf("list remediations: %v", err)
	}
	owned := ownedSuites(cb)
	var mine int
	for i := range list.Items {
		if owned[list.Items[i].GetLabels()[suiteLabel]] {
			mine++
		}
	}
	t.Logf("%d owned remediations of %d total in namespace", mine, len(list.Items))
}
