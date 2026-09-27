package controller

import (
	"context"
	"testing"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	baselinev1alpha1 "github.com/maci0/baseline-security-operator/api/v1alpha1"
)

// virtualClock is the deterministic clock a simulation drives: it holds time
// still until the harness advances it, so a whole reconcile run is a function
// of the seed that produced the inputs and the advance schedule, not of when
// the test happens to run.
type virtualClock struct{ at time.Time }

func (c *virtualClock) Now() time.Time { return c.at }

// Sleep advances simulated time instead of blocking, so a retry loop driven by
// this clock reaches its next attempt in no wall time at all. A real deadline
// still wins: a cancelled context ends the wait the way a real timer would.
func (c *virtualClock) Sleep(ctx context.Context, d time.Duration) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	c.at = c.at.Add(d)
	return nil
}

func (c *virtualClock) advance(d time.Duration) { c.at = c.at.Add(d) }

// TestReconcileReadsTheInjectedClock pins the seam every time-based decision
// reads: with a virtual clock, the next scan time and the requeue cadence are
// derived from simulated time, not wall clock. Without the seam these asserts
// would have to tolerate whatever the machine's clock says.
func TestReconcileReadsTheInjectedClock(t *testing.T) {
	scheme := testScheme(t)
	cb := newCB("cis") // schedule "0 1 * * *"
	clk := &virtualClock{at: time.Date(2024, 3, 1, 0, 30, 0, 0, time.UTC)}
	r := &ClusterBaselineReconciler{
		Client: fake.NewClientBuilder().WithScheme(scheme).WithObjects(cb).
			WithStatusSubresource(&baselinev1alpha1.ClusterBaseline{}).Build(),
		Scheme: scheme,
		Clock:  clk,
	}
	req := ctrl.Request{NamespacedName: types.NamespacedName{Name: clusterBaselineName}}
	// First pass only adds the finalizer and returns; the timed work is pass two.
	if _, err := r.Reconcile(context.Background(), req); err != nil {
		t.Fatal(err)
	}
	res, err := r.Reconcile(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	// No Compliance Operator exists in this fake cluster, so the CR is
	// Progressing and the fast 15s cadence is the expected result of the pass.
	if res.RequeueAfter != 15*time.Second {
		t.Fatalf("RequeueAfter = %v, want 15s while Progressing", res.RequeueAfter)
	}
	got := &baselinev1alpha1.ClusterBaseline{}
	if err := r.Get(context.Background(), types.NamespacedName{Name: clusterBaselineName}, got); err != nil {
		t.Fatal(err)
	}
	// 01:00 UTC the same simulated day, not 01:00 tomorrow as the wall clock
	// would compute from whatever day the test runs on.
	want := time.Date(2024, 3, 1, 1, 0, 0, 0, time.UTC)
	if got.Status.NextScanTime == nil || !got.Status.NextScanTime.Time.Equal(want) {
		t.Fatalf("status.nextScanTime = %v, want the injected clock's %v", got.Status.NextScanTime, want)
	}

	// Advancing simulated time is what a simulation does between steps.
	clk.advance(30 * time.Minute)
	if want := time.Date(2024, 3, 1, 1, 0, 0, 0, time.UTC); clk.Now() != want {
		t.Fatalf("clock = %v, want %v after one advance step", clk.Now(), want)
	}
}

// TestWaiverExpiryShortensThePollOnSimulatedTime is the pair of the test
// above: the same CR, one clock step later, must requeue sooner because
// simulated time advanced, not because real time passed. Asserted on the
// cadence helper because the steady-vs-fast split is set by install progress
// the fake cluster cannot fake.
func TestWaiverExpiryShortensThePollOnSimulatedTime(t *testing.T) {
	cb := newCB("cis")
	expires := metav1.NewTime(time.Date(2024, 3, 1, 1, 0, 0, 0, time.UTC))
	cb.Spec.Waivers = []baselinev1alpha1.WaiverEntry{{Name: "no-gtitle", ExpiresAt: &expires}}
	setCond(cb, "Progressing", metav1.ConditionFalse, "AsExpected", "")

	// The expiry only shortens the poll while it is sooner than the steady 1m,
	// so start 20s out.
	clk := &virtualClock{at: time.Date(2024, 3, 1, 0, 59, 40, 0, time.UTC)}
	if got := requeueAfterAt(cb, clk.Now()); got != 20*time.Second {
		t.Fatalf("requeue 20s before expiry = %v, want 20s", got)
	}
	clk.advance(10 * time.Second)
	if got := requeueAfterAt(cb, clk.Now()); got != 10*time.Second {
		t.Fatalf("requeue after advancing = %v, want 10s", got)
	}
	// Past the expiry the waiver stops shortening the poll, on simulated time.
	clk.advance(10*time.Minute + time.Second)
	if got := requeueAfterAt(cb, clk.Now()); got != time.Minute {
		t.Fatalf("requeue after expiry = %v, want the steady 1m", got)
	}
}
