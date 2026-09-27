package controller

import (
	"encoding/json"
	"regexp"
	"strings"
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

// dashboardPanel is the subset of the console dashboard schema the drift guards
// below assert on. Unknown fields are ignored, so a console schema change does
// not fail the test.
type dashboardPanel struct {
	ID      int
	Title   string
	Span    int
	Targets []struct {
		Expr string
	} `json:"targets"`
}

type dashboardRow struct {
	Panels []dashboardPanel
}

type dashboardSpec struct {
	Rows []dashboardRow `json:"rows"`
}

// promIdent matches a PromQL metric name and the histogram suffixes a bucket
// query appends to it. Label names (`on(instance)`) and function names carry no
// metric prefix, so the prefix filter in the test leaves them out.
var promIdent = regexp.MustCompile(`[a-zA-Z_][a-zA-Z0-9_]*`)

// dashboardMetricNames is every metric the embedded dashboard may query: the
// operator's own gauges plus the controller-runtime reconcile series the
// Reconcile-loop row reads. Kept explicit rather than scraped from the registry
// so a panel is still checked in a fresh process, where the controller-runtime
// histogram has no series to gather until the first reconcile.
var dashboardMetricNames = []string{
	"baseline_security_compliance_score",
	"baseline_security_checks",
	"baseline_security_condition",
	"baseline_security_last_scan_timestamp_seconds",
	"baseline_security_scan_interval_seconds",
	"baseline_security_newly_failed",
	"baseline_security_status_observed_timestamp_seconds",
	"baseline_security_remediation_batch_active",
	"baseline_security_remediation_batch_started_timestamp_seconds",
	"baseline_security_remediation_batches_total",
	"controller_runtime_reconcile_total",
	"controller_runtime_reconcile_errors_total",
	"controller_runtime_reconcile_time_seconds",
}

// TestDashboardRowsFillGrid pins the Grafana grid: a row whose spans do not sum
// to 12 wraps into a second visual row and shifts every panel below it, so a
// panel added without rebalancing silently changes the layout.
func TestDashboardRowsFillGrid(t *testing.T) {
	const rowWidth = 12
	var spec dashboardSpec
	if err := json.Unmarshal([]byte(complianceDashboardJSON), &spec); err != nil {
		t.Fatalf("embedded dashboard is not valid JSON: %v", err)
	}
	if len(spec.Rows) == 0 {
		t.Fatal("embedded dashboard has no rows")
	}
	seen := map[int]bool{}
	for i, row := range spec.Rows {
		total := 0
		for _, p := range row.Panels {
			if seen[p.ID] {
				t.Errorf("row %d: duplicate panel id %d", i, p.ID)
			}
			seen[p.ID] = true
			total += p.Span
		}
		if total != rowWidth {
			t.Errorf("row %d: panel spans sum to %d, want %d", i, total, rowWidth)
		}
	}
}

// TestDashboardQueriesKnownMetrics keeps every panel query pointed at a metric
// that exists. A renamed or deleted gauge leaves the panel rendering an empty
// graph, which reads as "nothing wrong" on the dashboard operators open first
// during an incident.
func TestDashboardQueriesKnownMetrics(t *testing.T) {
	var spec dashboardSpec
	if err := json.Unmarshal([]byte(complianceDashboardJSON), &spec); err != nil {
		t.Fatalf("embedded dashboard is not valid JSON: %v", err)
	}
	known := make(map[string]bool, len(dashboardMetricNames))
	for _, name := range dashboardMetricNames {
		known[name] = true
	}
	histSuffixes := []string{"_bucket", "_sum", "_count"}
	for _, row := range spec.Rows {
		for _, p := range row.Panels {
			for _, target := range p.Targets {
				for _, ident := range promIdent.FindAllString(target.Expr, -1) {
					if !strings.HasPrefix(ident, "baseline_security_") &&
						!strings.HasPrefix(ident, "controller_runtime_") {
						continue
					}
					base := ident
					for _, suffix := range histSuffixes {
						base = strings.TrimSuffix(base, suffix)
					}
					if !known[base] {
						t.Errorf("panel %d (%q) queries unknown metric %q in %q",
							p.ID, p.Title, base, target.Expr)
					}
				}
			}
		}
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
