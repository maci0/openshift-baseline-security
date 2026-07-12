package controller

import (
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"

	baselinev1alpha1 "github.com/maci0/baseline-security-operator/api/v1alpha1"
)

// TestDashboardConfigMapCurrent pins the fast-path guard that skips CreateOrUpdate
// every poll. Wrong labels, payload, or owner must return false so the reconciler
// re-applies; a missing negative here would hide thrash or skip repair.
func TestDashboardConfigMapCurrent(t *testing.T) {
	cb := &baselinev1alpha1.ClusterBaseline{}
	cb.SetUID("owner-uid")
	current := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Labels: map[string]string{
				"console.openshift.io/dashboard": "true",
				"app.kubernetes.io/part-of":      "baseline-security",
			},
			OwnerReferences: []metav1.OwnerReference{{UID: "owner-uid"}},
		},
		Data: map[string]string{dashboardJSONKey: complianceDashboardJSON},
	}
	if !dashboardConfigMapCurrent(current, cb) {
		t.Fatal("current dashboard must short-circuit reconcile")
	}

	// Missing dashboard label -> repair.
	noLabel := current.DeepCopy()
	delete(noLabel.Labels, "console.openshift.io/dashboard")
	if dashboardConfigMapCurrent(noLabel, cb) {
		t.Fatal("missing dashboard label must not be current")
	}

	// Wrong part-of -> repair (foreign CM must not be treated as ours).
	wrongPart := current.DeepCopy()
	wrongPart.Labels["app.kubernetes.io/part-of"] = "other"
	if dashboardConfigMapCurrent(wrongPart, cb) {
		t.Fatal("wrong part-of label must not be current")
	}

	// Stale embedded JSON -> repair on content bump.
	staleJSON := current.DeepCopy()
	staleJSON.Data[dashboardJSONKey] = `{"title":"stale"}`
	if dashboardConfigMapCurrent(staleJSON, cb) {
		t.Fatal("stale dashboard JSON must not be current")
	}

	// Missing / wrong owner UID -> repair so GC still chains to the CR.
	noOwner := current.DeepCopy()
	noOwner.OwnerReferences = nil
	if dashboardConfigMapCurrent(noOwner, cb) {
		t.Fatal("missing owner ref must not be current")
	}
	wrongOwner := current.DeepCopy()
	wrongOwner.OwnerReferences = []metav1.OwnerReference{{UID: "other-uid"}}
	if dashboardConfigMapCurrent(wrongOwner, cb) {
		t.Fatal("wrong owner UID must not be current")
	}
	// Empty UID on both sides must not match (ref.UID != "" guard).
	emptyUID := current.DeepCopy()
	emptyUID.OwnerReferences = []metav1.OwnerReference{{UID: ""}}
	cbEmpty := &baselinev1alpha1.ClusterBaseline{}
	if dashboardConfigMapCurrent(emptyUID, cbEmpty) {
		t.Fatal("empty owner UID must not be current")
	}

	// Nil labels / data must not panic and must not be current.
	if dashboardConfigMapCurrent(&corev1.ConfigMap{}, cb) {
		t.Fatal("empty ConfigMap must not be current")
	}
}

// FuzzDashboardConfigMapCurrent: ConfigMap labels/data/owners are cluster-
// editable. Must never panic; true only when labels, embedded JSON, and a
// non-empty matching owner UID all line up (reconcile fast-path guard).
func FuzzDashboardConfigMapCurrent(f *testing.F) {
	f.Add("true", "baseline-security", "owner-uid", "owner-uid", true)
	f.Add("false", "baseline-security", "owner-uid", "owner-uid", true)
	f.Add("true", "other", "owner-uid", "owner-uid", true)
	f.Add("true", "baseline-security", "owner-uid", "other-uid", true)
	f.Add("true", "baseline-security", "", "", true)
	f.Add("true", "baseline-security", "owner-uid", "owner-uid", false)
	f.Add("", "", "", "", false)
	f.Fuzz(func(t *testing.T, dashLabel, partOf, ownerUID, cbUID string, useEmbedJSON bool) {
		const max = 256
		if len(dashLabel) > max {
			dashLabel = dashLabel[:max]
		}
		if len(partOf) > max {
			partOf = partOf[:max]
		}
		if len(ownerUID) > max {
			ownerUID = ownerUID[:max]
		}
		if len(cbUID) > max {
			cbUID = cbUID[:max]
		}
		data := map[string]string{}
		if useEmbedJSON {
			data[dashboardJSONKey] = complianceDashboardJSON
		} else {
			data[dashboardJSONKey] = dashLabel // hostile / stale payload
		}
		cm := &corev1.ConfigMap{
			ObjectMeta: metav1.ObjectMeta{
				Labels: map[string]string{
					"console.openshift.io/dashboard": dashLabel,
					"app.kubernetes.io/part-of":      partOf,
				},
				OwnerReferences: []metav1.OwnerReference{{UID: types.UID(ownerUID)}},
			},
			Data: data,
		}
		cb := &baselinev1alpha1.ClusterBaseline{}
		cb.SetUID(types.UID(cbUID))

		got := dashboardConfigMapCurrent(cm, cb)
		// Empty / nil maps and empty ConfigMap must not panic and stay false.
		if dashboardConfigMapCurrent(&corev1.ConfigMap{}, cb) {
			t.Fatal("empty ConfigMap must not be current")
		}
		want := dashLabel == "true" &&
			partOf == "baseline-security" &&
			useEmbedJSON &&
			ownerUID != "" &&
			ownerUID == cbUID
		if got != want {
			t.Fatalf("dashboardConfigMapCurrent = %v, want %v (dash=%q part=%q owner=%q cb=%q embed=%v)",
				got, want, dashLabel, partOf, ownerUID, cbUID, useEmbedJSON)
		}
	})
}
