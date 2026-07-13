//go:build e2e

package e2e

import (
	"context"
	"strings"
	"testing"
	"time"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

const batchApplyAnnotation = "baselinesecurity.openshift.io/batch-apply"

var mcpGVK = schema.GroupVersionKind{
	Group: "machineconfiguration.openshift.io", Version: "v1", Kind: "MachineConfigPool",
}

// poolOfRemediation mirrors the operator: the MachineConfig role label, else the
// scan-name label ("<profile>-node-<pool>").
func poolOfRemediation(rem unstructured.Unstructured) string {
	if role, _, _ := unstructured.NestedString(rem.Object, "spec", "current", "object",
		"metadata", "labels", "machineconfiguration.openshift.io/role"); role != "" {
		return role
	}
	scan := rem.GetLabels()["compliance.openshift.io/scan-name"]
	// LastIndex: tailored profile names may themselves contain "-node-"
	// (mirrors operator poolFromRemediation).
	if i := strings.LastIndex(scan, "-node-"); i >= 0 {
		return scan[i+len("-node-"):]
	}
	return ""
}

func mcpPaused(ctx context.Context, c client.Client, pool string) (bool, error) {
	mcp := &unstructured.Unstructured{}
	mcp.SetGroupVersionKind(mcpGVK)
	if err := c.Get(ctx, types.NamespacedName{Name: pool}, mcp); err != nil {
		return false, err
	}
	p, _, _ := unstructured.NestedBool(mcp.Object, "spec", "paused")
	return p, nil
}

// TestRemediationBatchLive drives the batch pause/resume against the live
// operator and asserts the pool is paused during the batch and never left
// paused after. It uses the cancel path (revert apply=false) so no MachineConfig
// rolls, and restricts to a NON-control-plane pool: on a single-master cluster a
// master reboot would disrupt the API mid-test. Skips when no such remediation
// exists (as on a plain SNO whose only node remediations target master).
func TestRemediationBatchLive(t *testing.T) {
	ctx := context.Background()
	c := newClient(t)
	cb, err := getBaseline(ctx, c)
	if err != nil {
		t.Fatal(err)
	}

	// Find a batchable, worker-pool node remediation.
	list := &unstructured.UnstructuredList{}
	list.SetGroupVersionKind(remediationGVK.GroupVersion().WithKind(remediationGVK.Kind + "List"))
	if err := c.List(ctx, list, client.InNamespace(complianceNamespace)); err != nil {
		t.Fatal(err)
	}
	var remName, pool string
	for _, rem := range list.Items {
		kind, _, _ := unstructured.NestedString(rem.Object, "spec", "current", "object", "kind")
		apply, _, _ := unstructured.NestedBool(rem.Object, "spec", "apply")
		p := poolOfRemediation(rem)
		if kind == "MachineConfig" && !apply && p != "" && p != "master" {
			remName, pool = rem.GetName(), p
			break
		}
	}
	if remName == "" {
		t.Skip("no non-control-plane node remediation to safely batch-test")
	}

	if paused, err := mcpPaused(ctx, c, pool); err != nil {
		t.Fatalf("read pool %s: %v", pool, err)
	} else if paused {
		t.Skipf("pool %s already paused; skip to avoid interfering", pool)
	}

	setApply := func(apply bool) {
		rem := &unstructured.Unstructured{}
		rem.SetGroupVersionKind(remediationGVK)
		rem.SetName(remName)
		rem.SetNamespace(complianceNamespace)
		patch := []byte(`{"spec":{"apply":false}}`)
		if apply {
			patch = []byte(`{"spec":{"apply":true}}`)
		}
		if err := c.Patch(ctx, rem, client.RawPatch(types.MergePatchType, patch)); err != nil {
			t.Fatalf("set apply=%t: %v", apply, err)
		}
	}
	// Always revert on exit so a failed run never leaves the remediation applied.
	defer setApply(false)

	// Trigger the batch via the one-shot annotation.
	patch := []byte(`{"metadata":{"annotations":{"` + batchApplyAnnotation + `":"` + remName + `"}}}`)
	if err := c.Patch(ctx, cb, client.RawPatch(types.MergePatchType, patch)); err != nil {
		t.Fatalf("annotate batch: %v", err)
	}

	// Phase 1: the operator pauses the pool and sets apply=true.
	eventually(t, 2*time.Minute, "pool paused + batch applying", func() error {
		paused, err := mcpPaused(ctx, c, pool)
		if err != nil {
			return err
		}
		cur, err := getBaseline(ctx, c)
		if err != nil {
			return err
		}
		b := cur.Status.RemediationBatch
		if !paused {
			return errf("pool %s not paused yet", pool)
		}
		if b == nil || b.Phase != "Applying" {
			return errf("batch not Applying: %+v", b)
		}
		return nil
	})

	// Cancel: revert apply=false so nothing rolls, then the operator must resume.
	setApply(false)
	eventually(t, 3*time.Minute, "pool resumed + batch cleared", func() error {
		paused, err := mcpPaused(ctx, c, pool)
		if err != nil {
			return err
		}
		cur, err := getBaseline(ctx, c)
		if err != nil {
			return err
		}
		if paused {
			return errf("pool %s still paused", pool)
		}
		if cur.Status.RemediationBatch != nil {
			return errf("batch not cleared: %+v", cur.Status.RemediationBatch)
		}
		return nil
	})
}
